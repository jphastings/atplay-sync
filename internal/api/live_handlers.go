// internal/api/live_handlers.go
package api

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/jphastings/game-status/internal/livestate"
	"github.com/jphastings/game-status/internal/sync"
)

type LiveHandlers struct {
	Conn     *sql.DB
	Resolver sync.GameResolver
	Hub      *livestate.Hub
}

var liveUpgrader = websocket.Upgrader{}

// Serve upgrades an authenticated request into a long-lived push connection
// carrying the caller's own sync-state outcomes (internal/livestate.Hub):
// register, send one current snapshot computed the same way Reconcile
// computes it, then block reading (discarding frames — the client never
// sends any) purely to detect the connection going away, deregistering on
// exit either way.
func (h *LiveHandlers) Serve(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}

	conn, err := liveUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote its own HTTP error response on failure
	}
	defer closeGracefully(conn)

	h.Hub.Register(did, conn)
	defer h.Hub.Deregister(did, conn)

	// Published rather than written directly, so the Hub records it as this
	// did's current state and can re-assert it on the heartbeat.
	if _, outcomes, err := sync.ComputeDesired(r.Context(), h.Conn, h.Resolver, did, time.Now()); err == nil {
		h.Hub.Publish(did, outcomes)
	}

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// closeGracefully sends a close frame before dropping the connection.
// gorilla's Close "closes the underlying network connection without sending
// or waiting for a close message", which leaves the browser depending on
// that TCP close reaching it through whatever proxies sit in between. When
// it doesn't, the page's own close handler never fires, so it never
// reconnects and sits on stale state until someone reloads it.
//
// WriteControl is documented as safe to call concurrently with other
// writes, so this can't collide with a Publish landing at the same moment.
func closeGracefully(conn *websocket.Conn) {
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	)
	conn.Close()
}
