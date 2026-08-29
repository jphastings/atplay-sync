// internal/jetstream/listener.go
package jetstream

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"

	"github.com/gorilla/websocket"
)

// wireEvent mirrors Jetstream's JSON message shape.
type wireEvent struct {
	DID    string `json:"did"`
	Kind   string `json:"kind"`
	Commit *struct {
		Operation  string          `json:"operation"`
		Collection string          `json:"collection"`
		Rkey       string          `json:"rkey"`
		Record     json.RawMessage `json:"record"`
	} `json:"commit"`
}

type EventHandler func(ctx context.Context, ev Event) error

type Listener struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func connect(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error) {
	u := url.URL{Scheme: "wss", Host: host, Path: "/subscribe"}
	q := u.Query()
	for _, c := range collections {
		q.Add("wantedCollections", c)
	}
	for _, d := range dids {
		q.Add("wantedDids", d)
	}
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return nil, err
	}

	listenCtx, cancel := context.WithCancel(ctx)
	l := &Listener{cancel: cancel, done: make(chan struct{})}
	go l.readLoop(listenCtx, conn, handler)
	return l, nil
}

func (l *Listener) readLoop(ctx context.Context, conn *websocket.Conn, handler EventHandler) {
	defer close(l.done)
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close() // unblocks the ReadMessage call below
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("jetstream read", "err", err)
			}
			return
		}

		var we wireEvent
		if err := json.Unmarshal(raw, &we); err != nil || we.Kind != "commit" || we.Commit == nil {
			continue
		}

		ev := Event{DID: we.DID, Collection: we.Commit.Collection, Rkey: we.Commit.Rkey, Operation: Operation(we.Commit.Operation), Record: we.Commit.Record}
		if err := handler(ctx, ev); err != nil {
			slog.Error("jetstream handler", "did", ev.DID, "err", err)
		}
	}
}

func (l *Listener) Close() {
	l.cancel()
	<-l.done
}
