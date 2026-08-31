package atsession

import (
	"context"
	"sync"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

// clientApp is the slice of *oauth.ClientApp that Resumer needs — narrowed
// to an interface so tests can fake it instead of standing up a real
// oauth.ClientApp (store, config, network).
type clientApp interface {
	ResumeSession(ctx context.Context, did syntax.DID, sessionID string) (*oauth.ClientSession, error)
}

// Resumer serializes ResumeSession-and-use per DID. indigo's ClientSession
// only guards concurrent calls on a single in-memory instance; every
// ResumeSession call builds a fresh one from the same stored data, which
// indigo's own docs warn can clobber a concurrent call for the same DID
// (e.g. the background sync tick racing a user-triggered recheck).
type Resumer struct {
	app clientApp

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func NewResumer(app *oauth.ClientApp) *Resumer {
	return &Resumer{app: app, locks: make(map[string]*sync.Mutex)}
}

func (r *Resumer) lockFor(did string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.locks[did]
	if !ok {
		l = &sync.Mutex{}
		r.locks[did] = l
	}
	return l
}

// WithSession resumes did's session and runs fn with it, holding a per-DID
// lock for the duration so no concurrent WithSession call for the same DID
// can race on session state.
//
// ponytail: locks only ever grows, entries are never evicted — fine at this
// app's account count; add cleanup/an LRU if that ever changes.
func (r *Resumer) WithSession(ctx context.Context, did syntax.DID, sessionID string, fn func(*oauth.ClientSession) error) error {
	l := r.lockFor(did.String())
	l.Lock()
	defer l.Unlock()

	sess, err := r.app.ResumeSession(ctx, did, sessionID)
	if err != nil {
		return err
	}
	return fn(sess)
}
