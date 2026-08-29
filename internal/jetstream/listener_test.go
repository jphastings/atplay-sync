// internal/jetstream/listener_test.go
package jetstream

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestListener_ReconnectReplaysFromLastCursor pins the behaviour the reconnect
// exists for: without a cursor, every event during the drop plus backoff is
// lost until the next daily sweep.
func TestListener_ReconnectReplaysFromLastCursor(t *testing.T) {
	const firstEventUS = 1756500000000000

	queries := make(chan url.Values, 4)
	upgrader := websocket.Upgrader{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if r.URL.Query().Get("cursor") != "" {
			<-r.Context().Done() // second connection: hold it open
			return
		}
		conn.WriteJSON(map[string]any{
			"did": "did:plc:a", "kind": "commit", "time_us": firstEventUS,
			"commit": map[string]any{"operation": "delete", "collection": "dev.keytrace.claim", "rkey": "abc"},
		})
	}))
	defer srv.Close()

	previous := dialer
	dialer = &websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	t.Cleanup(func() { dialer = previous })

	handled := make(chan Event, 4)
	l, err := connect(context.Background(), strings.TrimPrefix(srv.URL, "https://"), []string{"dev.keytrace.claim"}, []string{"did:plc:a"},
		func(ctx context.Context, ev Event) error {
			handled <- ev
			return nil
		})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer l.Close()

	select {
	case <-handled:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first event")
	}

	<-queries // the initial connection
	select {
	case q := <-queries:
		if got := q.Get("cursor"); got != "1756500000000000" {
			t.Fatalf("reconnect cursor = %q, want the last processed event's time_us", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the reconnect")
	}
}

func TestDial_OmitsCursorOnAFreshConnection(t *testing.T) {
	queries := make(chan url.Values, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.URL.Query()
	}))
	defer srv.Close()

	previous := dialer
	dialer = &websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	t.Cleanup(func() { dialer = previous })

	dial(context.Background(), strings.TrimPrefix(srv.URL, "https://"), []string{"dev.keytrace.claim"}, []string{"did:plc:a"}, 0)

	q := <-queries
	if _, ok := q["cursor"]; ok {
		t.Fatalf("cursor = %v, want it omitted when there's nothing to replay", q["cursor"])
	}
}
