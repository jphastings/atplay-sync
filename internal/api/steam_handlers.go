// internal/api/steam_handlers.go
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/claims"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/jetstream"
	"github.com/jphastings/game-status/internal/keytrace"
)

type SteamHandlers struct {
	App       *oauth.ClientApp
	Conn      *sql.DB
	Verifier  *keytrace.Verifier
	Jetstream *jetstream.Manager
}

func (h *SteamHandlers) Recheck(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	if err := h.discoverFor(r.Context(), did); err != nil {
		http.Error(w, "recheck failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type enableRequest struct {
	Enabled bool `json:"enabled"`
}

func (h *SteamHandlers) SetEnabled(w http.ResponseWriter, r *http.Request) {
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
		claim, err := db.GetSteamClaim(r.Context(), h.Conn, did)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if claim == nil {
			http.Error(w, "no verified steam claim — recheck first", http.StatusConflict)
			return
		}
	}

	if err := db.SetSteamEnabled(r.Context(), h.Conn, did, body.Enabled); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if h.Jetstream != nil {
		// Don't block the HTTP response on a reconnect; Restart reads the DID
		// list itself so two concurrent toggles can't apply a stale one.
		go func() {
			err := h.Jetstream.Restart(context.Background(), func(ctx context.Context) ([]string, error) {
				return db.ListSteamEnabledDIDs(ctx, h.Conn)
			})
			if err != nil {
				slog.Error("jetstream restart after steam toggle", "did", did, "enabled", body.Enabled, "err", err)
			}
		}()
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *SteamHandlers) discoverFor(ctx context.Context, did string) error {
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return err
	}
	user, err := db.GetUser(ctx, h.Conn, did)
	if err != nil {
		return err
	}
	if user == nil || user.ActiveSessionID == "" {
		return fmt.Errorf("no active session for %s", did)
	}
	sess, err := h.App.ResumeSession(ctx, parsedDID, user.ActiveSessionID)
	if err != nil {
		return err
	}
	return claims.Discover(ctx, sess.APIClient(), h.Verifier, h.Conn, did)
}
