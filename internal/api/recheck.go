// internal/api/recheck.go
package api

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/atsession"
	"github.com/jphastings/game-status/internal/claims"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

// sessionResumer is the slice of *atsession.Resumer that discoverFor needs,
// narrowed to an interface so tests can exercise the session-lock ordering
// below without a real oauth.ClientApp.
type sessionResumer interface {
	WithSession(ctx context.Context, did syntax.DID, sessionID string, fn func(*oauth.ClientSession) error) error
}

var _ sessionResumer = (*atsession.Resumer)(nil)

// deferredReconcile stands in for the real db.Reconciler while a session
// lock is held, only noting that a reconcile was requested (and the latest
// `now` it was asked for). Reconciler.Reconcile's writer re-enters the
// per-DID session lock that Resumer.WithSession is still holding at that
// point, and that lock isn't reentrant, so running it inline would
// deadlock.
type deferredReconcile struct {
	requested bool
	now       time.Time
}

func (d *deferredReconcile) Reconcile(_ context.Context, _ string, now time.Time) error {
	d.requested, d.now = true, now
	return nil
}

// flush runs the real reconciler exactly once if fn requested one, after
// the session lock has been released. discoverErr is returned first if both
// it and the flushed reconcile fail — the DB state Discover changed before
// erroring still needs reconciling.
func (d *deferredReconcile) flush(ctx context.Context, reconciler db.Reconciler, did string, discoverErr error) error {
	if !d.requested {
		return discoverErr
	}
	if err := reconciler.Reconcile(ctx, did, d.now); err != nil && discoverErr == nil {
		return err
	}
	return discoverErr
}

// withDeferredReconcile resumes did's session and runs fn with it, handing
// fn a db.Reconciler that only records a reconcile request instead of
// running it — then, once the session lock is released, runs the real
// reconciler at most once if one was requested.
func withDeferredReconcile(ctx context.Context, resumer sessionResumer, parsedDID syntax.DID, sessionID string, reconciler db.Reconciler, did string, fn func(sess *oauth.ClientSession, rec db.Reconciler) error) error {
	var deferred deferredReconcile
	discoverErr := resumer.WithSession(ctx, parsedDID, sessionID, func(sess *oauth.ClientSession) error {
		return fn(sess, &deferred)
	})
	return deferred.flush(ctx, reconciler, did, discoverErr)
}

// discoverFor resumes the caller's atproto session and re-scans their
// dev.keytrace.claim collection for every supported claim type (Steam and
// Discord alike) — shared by both SteamHandlers.Recheck and
// DiscordHandlers.Recheck, since claims.Discover itself isn't per-source.
func discoverFor(ctx context.Context, resumer sessionResumer, conn *sql.DB, verifier *keytrace.Verifier, resolver claims.SubjectResolver, reconciler db.Reconciler, did string) error {
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
	return withDeferredReconcile(ctx, resumer, parsedDID, user.ActiveSessionID, reconciler, did, func(sess *oauth.ClientSession, rec db.Reconciler) error {
		return claims.Discover(ctx, sess.APIClient(), verifier, resolver, conn, rec, did)
	})
}
