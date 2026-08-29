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
	Deleter   db.StatusDeleter
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
	// A recheck can restore a claim that was revoked while steam_enabled
	// stayed true, in which case SetEnabled never fires and nothing else would
	// put this DID back on the Jetstream watch list.
	h.restartJetstream(did)
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

	h.restartJetstream(did)
	w.WriteHeader(http.StatusNoContent)
}

// restartJetstream re-applies the watch list off the request path: Restart
// reads the DID list itself, so two concurrent callers can't apply a stale one.
func (h *SteamHandlers) restartJetstream(did string) {
	if h.Jetstream == nil {
		return
	}
	go func() {
		err := h.Jetstream.Restart(context.Background(), func(ctx context.Context) ([]string, error) {
			return db.ListSteamEnabledDIDs(ctx, h.Conn)
		})
		if err != nil {
			slog.Error("jetstream restart", "did", did, "err", err)
		}
	}()
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
	return claims.Discover(ctx, sess.APIClient(), h.Verifier, h.Conn, h.Deleter, did)
}
