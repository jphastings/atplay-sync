// internal/api/recheck.go
package api

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/claims"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

// discoverFor resumes the caller's atproto session and re-scans their
// dev.keytrace.claim collection for every supported claim type (Steam and
// Discord alike) — shared by both SteamHandlers.Recheck and
// DiscordHandlers.Recheck, since claims.Discover itself isn't per-source.
func discoverFor(ctx context.Context, app *oauth.ClientApp, conn *sql.DB, verifier *keytrace.Verifier, resolver claims.SubjectResolver, reconciler db.Reconciler, did string) error {
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return err
	}
	user, err := db.GetUser(ctx, conn, did)
	if err != nil {
		return err
	}
	if user == nil || user.ActiveSessionID == "" {
		return fmt.Errorf("no active session for %s", did)
	}
	sess, err := app.ResumeSession(ctx, parsedDID, user.ActiveSessionID)
	if err != nil {
		return err
	}
	return claims.Discover(ctx, sess.APIClient(), verifier, resolver, conn, reconciler, did)
}
