// internal/api/steam_handlers.go
package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/claims"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

type SteamHandlers struct {
	App      *oauth.ClientApp
	Conn     *sql.DB
	Verifier *keytrace.Verifier
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
