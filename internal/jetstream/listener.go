// internal/jetstream/listener.go
package jetstream

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Jetstream sends nothing at all while the watched DIDs are quiet, so the
	// read deadline is kept alive by our own pings rather than by traffic.
	readTimeout    = 60 * time.Second
	pingInterval   = 30 * time.Second
	writeTimeout   = 10 * time.Second
	handlerTimeout = 30 * time.Second
	minBackoff     = time.Second
	maxBackoff     = 30 * time.Second
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

func dial(ctx context.Context, host string, collections, dids []string) (*websocket.Conn, error) {
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
	return conn, err
}

func connect(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error) {
	conn, err := dial(ctx, host, collections, dids)
	if err != nil {
		return nil, err
	}

	listenCtx, cancel := context.WithCancel(ctx)
	l := &Listener{cancel: cancel, done: make(chan struct{})}
	go l.run(listenCtx, conn, host, collections, dids, handler)
	return l, nil
}

// run reads from conn until it dies, then redials with capped exponential
// backoff until it succeeds or ctx is cancelled by Close. Without this a
// server restart or network blip would silently take realtime revocation
// offline until the next Manager.Restart, which may never come.
func (l *Listener) run(ctx context.Context, conn *websocket.Conn, host string, collections, dids []string, handler EventHandler) {
	defer close(l.done)

	backoff := minBackoff
	for {
		l.readLoop(ctx, conn, handler)
		if ctx.Err() != nil {
			return
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}

			next, err := dial(ctx, host, collections, dids)
			if err == nil {
				slog.Info("jetstream reconnected", "host", host, "dids", len(dids))
				conn, backoff = next, minBackoff
				break
			}
			slog.Error("jetstream reconnect", "host", host, "err", err, "retry_in", backoff)
			backoff = min(backoff*2, maxBackoff)
		}
	}
}

// readLoop consumes one connection, returning when it fails or ctx is cancelled.
func (l *Listener) readLoop(ctx context.Context, conn *websocket.Conn, handler EventHandler) {
	defer conn.Close()

	extend := func() error { return conn.SetReadDeadline(time.Now().Add(readTimeout)) }
	_ = extend()
	conn.SetPongHandler(func(string) error { return extend() })

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				conn.Close() // unblocks the ReadMessage call below
				return
			case <-stop:
				return
			case <-ticker.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout))
			}
		}
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("jetstream read", "err", err)
			}
			return
		}
		_ = extend()

		var we wireEvent
		if err := json.Unmarshal(raw, &we); err != nil || we.Kind != "commit" || we.Commit == nil {
			continue
		}

		ev := Event{DID: we.DID, Collection: we.Commit.Collection, Rkey: we.Commit.Rkey, Operation: Operation(we.Commit.Operation), Record: we.Commit.Record}
		l.handle(ctx, handler, ev)
	}
}

// handle runs the event handler on a context detached from the listener's own
// cancellation: Jetstream carries no cursor here, so an in-flight revocation
// aborted by a concurrent Manager.Restart would never be replayed.
func (l *Listener) handle(ctx context.Context, handler EventHandler, ev Event) {
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), handlerTimeout)
	defer cancel()
	if err := handler(hctx, ev); err != nil {
		slog.Error("jetstream handler", "did", ev.DID, "err", err)
	}
}

func (l *Listener) Close() {
	l.cancel()
	<-l.done
}
