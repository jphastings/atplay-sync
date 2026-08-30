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

// discordKeyJWK is a locally-generated key distinct from realKeyJWK: keytrace
// rotates its signing key over time (see the differing
// dev.keytrace.serverPublicKey record dates below), so realDiscordClaimJSON's
// attestation is signed against this key rather than reusing realKeyJWK.
const discordKeyJWK = `{"kty":"EC","x":"Z8eoOjebqMWHP4OICJhYnjjRYRNoriWpFNlst3gpGis","y":"J_LLT8Rn9DOSMJE2e3aN1Gnu7k313j3GUC1hqrWcBmY","crv":"P-256"}`
const discordSignerKeyURI = "at://did:plc:hcwfdlmprcc335oixyfsw7u3/dev.keytrace.serverPublicKey/2026-08-30"
const realDiscordClaimJSON = `{
	"type":"discord","status":"verified",
	"claimUri":"https://discord.gg/EvTSZhkk4P",
	"identity":{"subject":"jphastings","profileUrl":"https://discord.com/users/690973862245957683","displayName":"byjp"},
	"sigs":[{
		"kid":"attest:discord",
		"src":"at://did:plc:hcwfdlmprcc335oixyfsw7u3/dev.keytrace.serverPublicKey/2026-08-30",
		"signedAt":"2026-08-30T16:41:52.267Z",
		"attestation":"eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJjbGFpbVVyaSI6Imh0dHBzOi8vZGlzY29yZC5nZy9FdlRTWmhrazRQIiwiZGlkIjoiZGlkOnBsYzplcGhrenBpbmhhcWNhYnRrdWd0Ynpyd3UiLCJpZGVudGl0eS5zdWJqZWN0IjoianBoYXN0aW5ncyIsInR5cGUiOiJkaXNjb3JkIn0.gm6DazZt2pwrDcVM0XTeUvtAUCxU8ljEI7glhs_VXYtp1gRhnIdMJvktWfeixLLH4VT3QR1Rqc7MBCFZ_hGe5A",
		"signedFields":["claimUri","did","identity.subject","type"]
	}],
	"createdAt":"2026-08-30T16:41:52.320Z","lastVerifiedAt":"2026-08-30T16:41:52.320Z"
}`

type fakeKeyFetcher struct{}

func (fakeKeyFetcher) FetchPublicJWK(ctx context.Context, keyURI string) (string, error) {
	if keyURI == discordSignerKeyURI {
		return discordKeyJWK, nil
	}
	return realKeyJWK, nil
}

func testVerifier() *keytrace.Verifier {
	return &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
}

type fakeSubjectResolver struct{ resolved map[string]string } // claimUri -> resolved snowflake

func (f fakeSubjectResolver) ResolveDiscordSubject(ctx context.Context, did string, claim keytrace.Claim) (string, bool) {
	id, ok := f.resolved[claim.ClaimURI]
	return id, ok
}

type fakeStore struct {
	claims          map[string]*appdb.Claim // keyed by claim type
	upserts         []appdb.Claim
	invalidatedType []string
}

func (f *fakeStore) GetClaim(ctx context.Context, did, claimType string) (*appdb.Claim, error) {
	return f.claims[claimType], nil
}
func (f *fakeStore) UpsertClaim(ctx context.Context, c appdb.Claim) error {
	f.upserts = append(f.upserts, c)
	return nil
}

// InvalidateClaim stands for the whole cleanup — claim row, session_starts
// and the PDS status record — which DBStore delegates to db.InvalidateClaim.
func (f *fakeStore) InvalidateClaim(ctx context.Context, did, claimType string) error {
	f.invalidatedType = append(f.invalidatedType, claimType)
	return nil
}

func TestHandleEvent_DeleteMatchingTrackedRecord_Invalidates(t *testing.T) {
	store := &fakeStore{claims: map[string]*appdb.Claim{
		appdb.SteamSource: {DID: "did:plc:a", Type: appdb.SteamSource, RecordURI: "at://did:plc:a/dev.keytrace.claim/abc"},
	}}

	ev := Event{DID: "did:plc:a", Collection: keytrace.ClaimCollection, Rkey: "abc", Operation: OpDelete}
	if err := HandleEvent(context.Background(), store, testVerifier(), fakeSubjectResolver{}, ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.invalidatedType) != 1 || store.invalidatedType[0] != appdb.SteamSource {
		t.Fatalf("invalidatedType = %v, want [steam]", store.invalidatedType)
	}
}

func TestHandleEvent_DeleteOfUnrelatedRecord_NoOp(t *testing.T) {
	store := &fakeStore{claims: map[string]*appdb.Claim{
		appdb.SteamSource: {DID: "did:plc:a", Type: appdb.SteamSource, RecordURI: "at://did:plc:a/dev.keytrace.claim/current"},
	}}

	ev := Event{DID: "did:plc:a", Collection: keytrace.ClaimCollection, Rkey: "some-old-rkey", Operation: OpDelete}
	if err := HandleEvent(context.Background(), store, testVerifier(), fakeSubjectResolver{}, ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.invalidatedType) != 0 {
		t.Fatalf("invalidatedType = %v, want none — this rkey isn't the tracked claim", store.invalidatedType)
	}
}

// A delete's AT-URI carries no type — HandleEvent must check every supported
// type's tracked claim, and invalidate only the one that actually matches.
func TestHandleEvent_DeleteMatchingTrackedDiscordRecord_InvalidatesOnlyDiscord(t *testing.T) {
	store := &fakeStore{claims: map[string]*appdb.Claim{
		appdb.SteamSource:   {DID: "did:plc:a", Type: appdb.SteamSource, RecordURI: "at://did:plc:a/dev.keytrace.claim/steam-rkey"},
		appdb.DiscordSource: {DID: "did:plc:a", Type: appdb.DiscordSource, RecordURI: "at://did:plc:a/dev.keytrace.claim/discord-rkey"},
	}}

	ev := Event{DID: "did:plc:a", Collection: keytrace.ClaimCollection, Rkey: "discord-rkey", Operation: OpDelete}
	if err := HandleEvent(context.Background(), store, testVerifier(), fakeSubjectResolver{}, ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.invalidatedType) != 1 || store.invalidatedType[0] != appdb.DiscordSource {
		t.Fatalf("invalidatedType = %v, want [discord] only, steam claim must be left alone", store.invalidatedType)
	}
}

func TestHandleEvent_CreateVerifiedRealClaim_Upserts(t *testing.T) {
	store := &fakeStore{}

	ev := Event{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", Collection: keytrace.ClaimCollection, Rkey: "3mkwoifsquv2p", Operation: OpCreate, Record: []byte(realClaimJSON)}
	if err := HandleEvent(context.Background(), store, testVerifier(), fakeSubjectResolver{}, ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].Subject != "76561197994000231" {
		t.Fatalf("got upserts=%+v", store.upserts)
	}
}

func TestHandleEvent_CreateVerifiedDiscordClaim_ResolvesAndUpserts(t *testing.T) {
	store := &fakeStore{}
	resolver := fakeSubjectResolver{resolved: map[string]string{"https://discord.gg/EvTSZhkk4P": "690973862245957683"}}

	ev := Event{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", Collection: keytrace.ClaimCollection, Rkey: "discord-rkey", Operation: OpCreate, Record: []byte(realDiscordClaimJSON)}
	if err := HandleEvent(context.Background(), store, testVerifier(), resolver, ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].Subject != "690973862245957683" {
		t.Fatalf("got upserts=%+v, want the resolved snowflake as subject (never the raw username)", store.upserts)
	}
}

func TestHandleEvent_DiscordVerifiedButUnresolved_NoUpsert(t *testing.T) {
	store := &fakeStore{}
	resolver := fakeSubjectResolver{resolved: map[string]string{}} // hasn't joined the tracking server yet

	ev := Event{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", Collection: keytrace.ClaimCollection, Rkey: "discord-rkey", Operation: OpCreate, Record: []byte(realDiscordClaimJSON)}
	if err := HandleEvent(context.Background(), store, testVerifier(), resolver, ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.upserts) != 0 || len(store.invalidatedType) != 0 {
		t.Fatalf("got upserts=%+v invalidatedType=%v, want neither — unresolved isn't the same as invalid", store.upserts, store.invalidatedType)
	}
}

func TestHandleEvent_VerifiedStatusButBadSignature_NoUpsert(t *testing.T) {
	// status:"verified" alone must never be trusted — the signature has to
	// actually check out. Same technique as keytrace's
	// TestVerifyAttestation_RejectsSubstitutedSubject: swap in a different
	// identity.subject than the one the attestation JWT signed over.
	store := &fakeStore{}

	tampered := strings.Replace(realClaimJSON, `"subject":"76561197994000231"`, `"subject":"1"`, 1)
	ev := Event{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", Collection: keytrace.ClaimCollection, Rkey: "3mkwoifsquv2p", Operation: OpCreate, Record: []byte(tampered)}
	if err := HandleEvent(context.Background(), store, testVerifier(), fakeSubjectResolver{}, ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.upserts) != 0 {
		t.Fatalf("got upserts=%+v, want none — status is verified but the signature doesn't cover this subject", store.upserts)
	}
}

func TestHandleEvent_UpdateToNonVerifiedStatus_InvalidatesTrackedRecord(t *testing.T) {
	atURI := "at://did:plc:ephkzpinhaqcabtkugtbzrwu/dev.keytrace.claim/3mkwoifsquv2p"
	store := &fakeStore{claims: map[string]*appdb.Claim{
		appdb.SteamSource: {DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", Type: appdb.SteamSource, RecordURI: atURI},
	}}

	retracted := strings.Replace(realClaimJSON, `"status":"verified"`, `"status":"retracted"`, 1)
	ev := Event{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", Collection: keytrace.ClaimCollection, Rkey: "3mkwoifsquv2p", Operation: OpUpdate, Record: []byte(retracted)}
	if err := HandleEvent(context.Background(), store, testVerifier(), fakeSubjectResolver{}, ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.invalidatedType) != 1 || store.invalidatedType[0] != appdb.SteamSource {
		t.Fatalf("invalidatedType = %v, want a retracted claim to be invalidated", store.invalidatedType)
	}
}

func TestHandleEvent_UnsupportedType_Ignored(t *testing.T) {
	store := &fakeStore{}
	ev := Event{DID: "did:plc:a", Collection: keytrace.ClaimCollection, Rkey: "x", Operation: OpCreate, Record: []byte(`{"type":"github","status":"verified"}`)}
	if err := HandleEvent(context.Background(), store, testVerifier(), fakeSubjectResolver{}, ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.upserts) != 0 || len(store.invalidatedType) != 0 {
		t.Fatalf("got upserts=%+v invalidatedType=%v, want neither", store.upserts, store.invalidatedType)
	}
}
