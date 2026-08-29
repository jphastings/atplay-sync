// internal/jetstream/handler_test.go
package jetstream

import (
	"context"
	"strings"
	"testing"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

const realSignerDID = "did:plc:hcwfdlmprcc335oixyfsw7u3"
const realKeyJWK = `{"kty":"EC","x":"pretZ8lN1snAV4dNoyet54BTTs1-Mxv4-jNuVGazf8g","y":"wUT9JvxuvkRtPrufb6c4BPXoA60LhmvfaE_aH5d6A-o","crv":"P-256"}`
const realClaimJSON = `{
	"type":"steam","status":"verified",
	"claimUri":"https://steamcommunity.com/profiles/76561197994000231",
	"identity":{"subject":"76561197994000231","displayName":"JP"},
	"sigs":[{
		"kid":"attest:steam",
		"src":"at://did:plc:hcwfdlmprcc335oixyfsw7u3/dev.keytrace.serverPublicKey/2026-05-03",
		"signedAt":"2026-05-03T07:53:39.639Z",
		"attestation":"eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJjbGFpbVVyaSI6Imh0dHBzOi8vc3RlYW1jb21tdW5pdHkuY29tL3Byb2ZpbGVzLzc2NTYxMTk3OTk0MDAwMjMxIiwiZGlkIjoiZGlkOnBsYzplcGhrenBpbmhhcWNhYnRrdWd0Ynpyd3UiLCJpZGVudGl0eS5zdWJqZWN0IjoiNzY1NjExOTc5OTQwMDAyMzEiLCJ0eXBlIjoic3RlYW0ifQ.69NGYohaBKTQFtWoRPrqeIOIZN72Q7eEhESF2EPaQLRUfnFioQ3vtGWHsmSSEO5m8_7vd6UU347AlwcafaBBGA",
		"signedFields":["claimUri","did","identity.subject","type"]
	}],
	"createdAt":"2026-05-03T07:53:39.698Z","lastVerifiedAt":"2026-05-03T07:53:39.698Z"
}`

type fakeKeyFetcher struct{}

func (fakeKeyFetcher) FetchPublicJWK(ctx context.Context, keyURI string) (string, error) {
	return realKeyJWK, nil
}

func testVerifier() *keytrace.Verifier {
	return &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
}

type fakeStore struct {
	claim       *appdb.SteamClaim
	upserts     []appdb.SteamClaim
	invalidated bool
}

func (f *fakeStore) GetSteamClaim(ctx context.Context, did string) (*appdb.SteamClaim, error) { return f.claim, nil }
func (f *fakeStore) UpsertSteamClaim(ctx context.Context, c appdb.SteamClaim) error {
	f.upserts = append(f.upserts, c)
	return nil
}
func (f *fakeStore) InvalidateSteamClaim(ctx context.Context, did string) error {
	f.invalidated = true
	return nil
}

type fakeDeleter struct{ deleted []string }

func (f *fakeDeleter) DeleteStatus(ctx context.Context, did string) error {
	f.deleted = append(f.deleted, did)
	return nil
}

func TestHandleEvent_DeleteMatchingTrackedRecord_Invalidates(t *testing.T) {
	store := &fakeStore{claim: &appdb.SteamClaim{DID: "did:plc:a", RecordURI: "at://did:plc:a/dev.keytrace.claim/abc"}}
	deleter := &fakeDeleter{}

	ev := Event{DID: "did:plc:a", Collection: keytrace.ClaimCollection, Rkey: "abc", Operation: OpDelete}
	if err := HandleEvent(context.Background(), store, deleter, testVerifier(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if !store.invalidated || len(deleter.deleted) != 1 {
		t.Fatalf("got invalidated=%v deleted=%v, want both to have fired", store.invalidated, deleter.deleted)
	}
}

func TestHandleEvent_DeleteOfUnrelatedRecord_NoOp(t *testing.T) {
	store := &fakeStore{claim: &appdb.SteamClaim{DID: "did:plc:a", RecordURI: "at://did:plc:a/dev.keytrace.claim/current"}}
	deleter := &fakeDeleter{}

	ev := Event{DID: "did:plc:a", Collection: keytrace.ClaimCollection, Rkey: "some-old-rkey", Operation: OpDelete}
	if err := HandleEvent(context.Background(), store, deleter, testVerifier(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if store.invalidated || len(deleter.deleted) != 0 {
		t.Fatalf("got invalidated=%v deleted=%v, want neither (this rkey isn't the tracked claim)", store.invalidated, deleter.deleted)
	}
}

func TestHandleEvent_CreateVerifiedRealClaim_Upserts(t *testing.T) {
	store := &fakeStore{}
	deleter := &fakeDeleter{}

	ev := Event{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", Collection: keytrace.ClaimCollection, Rkey: "3mkwoifsquv2p", Operation: OpCreate, Record: []byte(realClaimJSON)}
	if err := HandleEvent(context.Background(), store, deleter, testVerifier(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].Subject != "76561197994000231" {
		t.Fatalf("got upserts=%+v", store.upserts)
	}
}

func TestHandleEvent_UpdateToNonVerifiedStatus_InvalidatesTrackedRecord(t *testing.T) {
	atURI := "at://did:plc:ephkzpinhaqcabtkugtbzrwu/dev.keytrace.claim/3mkwoifsquv2p"
	store := &fakeStore{claim: &appdb.SteamClaim{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", RecordURI: atURI}}
	deleter := &fakeDeleter{}

	retracted := strings.Replace(realClaimJSON, `"status":"verified"`, `"status":"retracted"`, 1)
	ev := Event{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", Collection: keytrace.ClaimCollection, Rkey: "3mkwoifsquv2p", Operation: OpUpdate, Record: []byte(retracted)}
	if err := HandleEvent(context.Background(), store, deleter, testVerifier(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if !store.invalidated || len(deleter.deleted) != 1 {
		t.Fatalf("got invalidated=%v deleted=%v, want both", store.invalidated, deleter.deleted)
	}
}

func TestHandleEvent_NonSteamType_Ignored(t *testing.T) {
	store := &fakeStore{}
	deleter := &fakeDeleter{}
	ev := Event{DID: "did:plc:a", Collection: keytrace.ClaimCollection, Rkey: "x", Operation: OpCreate, Record: []byte(`{"type":"github","status":"verified"}`)}
	if err := HandleEvent(context.Background(), store, deleter, testVerifier(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.upserts) != 0 || store.invalidated {
		t.Fatalf("got upserts=%+v invalidated=%v, want neither", store.upserts, store.invalidated)
	}
}
