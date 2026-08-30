// internal/api/discord_handlers.go
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"

	"github.com/jphastings/game-status/internal/claims"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/jetstream"
	"github.com/jphastings/game-status/internal/keytrace"
)

type DiscordHandlers struct {
	App        *oauth.ClientApp
	Conn       *sql.DB
	Verifier   *keytrace.Verifier
	Resolver   claims.SubjectResolver
	Reconciler db.Reconciler
	Jetstream  *jetstream.Manager
}

func (h *DiscordHandlers) Recheck(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	if err := discoverFor(r.Context(), h.App, h.Conn, h.Verifier, h.Resolver, h.Reconciler, did); err != nil {
		http.Error(w, "recheck failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	restartJetstreamWatch(h.Jetstream, h.Conn)
	w.WriteHeader(http.StatusNoContent)
}

func (h *DiscordHandlers) SetEnabled(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	var body enableRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if body.Enabled {
		claim, err := db.GetClaim(r.Context(), h.Conn, did, db.DiscordSource)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if claim == nil {
			http.Error(w, "no verified discord claim — recheck first", http.StatusConflict)
			return
		}
	} else {
		if err := db.ClearSessionStart(r.Context(), h.Conn, did, db.DiscordSource); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	if err := db.SetEnabled(r.Context(), h.Conn, did, db.DiscordSource, body.Enabled); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.Reconciler.Reconcile(r.Context(), did, time.Now()); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	restartJetstreamWatch(h.Jetstream, h.Conn)
	w.WriteHeader(http.StatusNoContent)
}
