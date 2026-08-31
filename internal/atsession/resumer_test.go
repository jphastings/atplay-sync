package atsession

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

type fakeApp struct{}

func (fakeApp) ResumeSession(ctx context.Context, did syntax.DID, sessionID string) (*oauth.ClientSession, error) {
	return &oauth.ClientSession{}, nil
}

// trackConcurrency returns a WithSession callback that records the peak
// number of calls, across whichever goroutines share cur/max, active at once.
func trackConcurrency(mu *sync.Mutex, cur, max *int) func(*oauth.ClientSession) error {
	return func(*oauth.ClientSession) error {
		mu.Lock()
		*cur++
		if *cur > *max {
			*max = *cur
		}
		mu.Unlock()

		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		*cur--
		mu.Unlock()
		return nil
	}
}

func TestWithSessionSerializesSameDID(t *testing.T) {
	r := &Resumer{app: fakeApp{}, locks: make(map[string]*sync.Mutex)}
	did, err := syntax.ParseDID("did:example:alice")
	if err != nil {
		t.Fatalf("parse DID: %v", err)
	}

	var mu sync.Mutex
	var cur, max int
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.WithSession(context.Background(), did, "session", trackConcurrency(&mu, &cur, &max)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if max != 1 {
		t.Errorf("expected concurrent calls for the same DID to serialize (max concurrency 1), got %d", max)
	}
}

func TestWithSessionAllowsConcurrentDIDs(t *testing.T) {
	r := &Resumer{app: fakeApp{}, locks: make(map[string]*sync.Mutex)}
	alice, _ := syntax.ParseDID("did:example:alice")
	bob, _ := syntax.ParseDID("did:example:bob")

	var mu sync.Mutex
	var cur, max int
	var wg sync.WaitGroup
	for _, did := range []syntax.DID{alice, bob} {
		wg.Add(1)
		go func(did syntax.DID) {
			defer wg.Done()
			if err := r.WithSession(context.Background(), did, "session", trackConcurrency(&mu, &cur, &max)); err != nil {
				t.Error(err)
			}
		}(did)
	}
	wg.Wait()

	if max != 2 {
		t.Errorf("expected calls for different DIDs to run concurrently (max concurrency 2), got %d", max)
	}
}
