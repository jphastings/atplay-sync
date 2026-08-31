// internal/api/sync_handlers.go
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jphastings/game-status/internal/db"
)

type SyncHandlers struct {
	Conn       *sql.DB
	Reconciler db.Reconciler
}

type orderRequest struct {
	Order []string `json:"order"`
}

var validSources = map[string]bool{db.SteamSource: true, db.DiscordSource: true}

func (h *SyncHandlers) SetOrder(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	var body orderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	seen := map[string]bool{}
	for _, source := range body.Order {
		if !validSources[source] || seen[source] {
			http.Error(w, "invalid or duplicate source in order", http.StatusBadRequest)
			return
		}
		seen[source] = true
	}

	if err := db.SetSourceOrder(r.Context(), h.Conn, did, body.Order); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.Reconciler.Reconcile(r.Context(), did, time.Now()); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
