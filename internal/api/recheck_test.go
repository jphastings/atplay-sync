// internal/api/recheck_test.go
package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/db"
)

// fakeSessionResumer mimics atsession.Resumer's per-DID non-reentrant lock
// without a real oauth.ClientApp, so the deadlock this test guards against —
// WithSession called again for the same DID while the first call still
// holds it — reproduces exactly as it would with the real Resumer.
type fakeSessionResumer struct {
	mu sync.Mutex
}

func (f *fakeSessionResumer) WithSession(ctx context.Context, did syntax.DID, sessionID string, fn func(*oauth.ClientSession) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(&oauth.ClientSession{})
}

// reentrantReconciler simulates sync.Reconciler's real behavior when driven
// through sync.ATProtoWriter: reconciling re-enters WithSession for the same
// DID. Under the pre-fix behavior (Reconcile called synchronously from
// inside the WithSession callback) this would deadlock on resumer's mutex.
type reentrantReconciler struct {
	resumer sessionResumer
	did     syntax.DID
	called  bool
}

func (r *reentrantReconciler) Reconcile(ctx context.Context, did string, now time.Time) error {
	r.called = true
	return r.resumer.WithSession(ctx, r.did, "session", func(*oauth.ClientSession) error { return nil })
}

func TestWithDeferredReconcile_ReconcileRunsAfterSessionLockReleased(t *testing.T) {
	resumer := &fakeSessionResumer{}
	did, err := syntax.ParseDID("did:example:alice")
	if err != nil {
		t.Fatalf("parse DID: %v", err)
	}
	reconciler := &reentrantReconciler{resumer: resumer, did: did}

	done := make(chan error, 1)
	go func() {
		done <- withDeferredReconcile(context.Background(), resumer, did, "session", reconciler, did.String(),
			func(sess *oauth.ClientSession, rec db.Reconciler) error {
				// Stands in for claims.Discover requesting a reconcile
				// (e.g. via db.InvalidateClaim) while still inside
				// WithSession's callback.
				return rec.Reconcile(context.Background(), did.String(), time.Now())
			})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("withDeferredReconcile: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("withDeferredReconcile deadlocked: the reconcile ran before the session lock was released")
	}

	if !reconciler.called {
		t.Fatal("expected the reconciler to have run after WithSession returned")
	}
}
