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
	defer conn.Close()

	h.Hub.Register(did, conn)
	defer h.Hub.Deregister(did, conn)

	if _, outcomes, err := sync.ComputeDesired(r.Context(), h.Conn, h.Resolver, did, time.Now()); err == nil {
		_ = conn.WriteJSON(outcomes)
	}

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
