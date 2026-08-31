// internal/claims/discover_test.go
package claims

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"database/sql"

	"github.com/bluesky-social/indigo/api/agnostic"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

const realClaimDID = "did:plc:ephkzpinhaqcabtkugtbzrwu"
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

// discordKeyJWK is a locally-generated key, distinct from realSignerDID's
// actual May-2026 steam-attesting key (realKeyJWK): keytrace rotates its
// signing key over time (see the differing dev.keytrace.serverPublicKey
// record dates below), so realDiscordClaimJSON's attestation is re-signed
// against this key rather than reusing realKeyJWK.
const discordKeyJWK = `{"kty":"EC","x":"Z8eoOjebqMWHP4OICJhYnjjRYRNoriWpFNlst3gpGis","y":"J_LLT8Rn9DOSMJE2e3aN1Gnu7k313j3GUC1hqrWcBmY","crv":"P-256"}`
const discordSignerKeyURI = "at://did:plc:hcwfdlmprcc335oixyfsw7u3/dev.keytrace.serverPublicKey/2026-08-30"

type fakeKeyFetcher struct{}

func (fakeKeyFetcher) FetchPublicJWK(ctx context.Context, keyURI string) (string, error) {
	if keyURI == discordSignerKeyURI {
		return discordKeyJWK, nil
	}
	return realKeyJWK, nil
}

type fakeLexClient struct {
	records []*agnostic.RepoListRecords_Record
}

func (f *fakeLexClient) LexDo(ctx context.Context, method, inputEncoding, endpoint string, params map[string]any, bodyData any, out any) error {
	target := out.(*agnostic.RepoListRecords_Output)
	target.Records = f.records
	return nil
}

type fakeSubjectResolver struct {
	resolved  map[string]string // claimUri -> resolved snowflake
	confirmed map[string]bool   // currentSubject -> still confirms; nil map = always true
}

func (f fakeSubjectResolver) ResolveDiscordSubject(ctx context.Context, did string, claim keytrace.Claim) (string, bool) {
	id, ok := f.resolved[claim.ClaimURI]
	return id, ok
}

func (f fakeSubjectResolver) ConfirmDiscordSubject(ctx context.Context, claim keytrace.Claim, currentSubject string) bool {
	if f.confirmed == nil {
		return true
	}
	return f.confirmed[currentSubject]
}

// resolveDiscordSubjectPanics wraps fakeSubjectResolver but panics if
// ResolveDiscordSubject is ever called — used to prove the fix's core
// security property: once a claim row exists, full resolution (which could
// land on a different snowflake than the one already stored) must never
// run again, only confirmation of the existing one.
type resolveDiscordSubjectPanics struct{ fakeSubjectResolver }

func (resolveDiscordSubjectPanics) ResolveDiscordSubject(ctx context.Context, did string, claim keytrace.Claim) (string, bool) {
	panic("ResolveDiscordSubject must not be called when a Discord claim row already exists")
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestDiscover_UpsertsOnRealVerifiedClaim(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	raw := json.RawMessage(realClaimJSON)
	client := &fakeLexClient{records: []*agnostic.RepoListRecords_Record{
		{Uri: "at://" + realClaimDID + "/dev.keytrace.claim/abc123", Value: &raw},
	}}
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}

	if err := Discover(ctx, client, verifier, fakeSubjectResolver{}, conn, &fakeReconciler{}, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	got, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.SteamSource)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if got == nil || got.Subject != "76561197994000231" {
		t.Fatalf("got %+v, want a steam claim for subject 76561197994000231", got)
	}
}

func TestDiscover_InvalidatesWhenNoVerifiedClaimFound(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, realClaimDID)
	appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: realClaimDID, Type: appdb.SteamSource, Subject: "old", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})
	appdb.SetSessionStart(ctx, conn, realClaimDID, appdb.SteamSource, "271590", time.Now())

	client := &fakeLexClient{records: nil} // the claim is gone
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	reconciler := &fakeReconciler{}

	if err := Discover(ctx, client, verifier, fakeSubjectResolver{}, conn, reconciler, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	got, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.SteamSource)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil after the claim disappears", got)
	}
	// The reconciler must be re-run so any still-legitimate record from
	// another source isn't left stranded, and the session bookkeeping must
	// not survive to be reused if they re-verify later. Discover invalidates
	// every supported type that has no verified claim this pass — steam AND
	// discord here, since neither was found — so the reconciler runs once per type.
	if len(reconciler.reconciled) != len(supportedClaimTypes) {
		t.Fatalf("reconciled = %v, want the reconciler re-run once per supported type", reconciler.reconciled)
	}
	session, err := appdb.GetSessionStart(ctx, conn, realClaimDID, appdb.SteamSource)
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if session != nil {
		t.Fatalf("session start = %+v, want it cleared", session)
	}
}

func TestDiscover_UpsertsBothTypesInOnePass(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	steamRaw := json.RawMessage(realClaimJSON)
	discordRaw := json.RawMessage(realDiscordClaimJSON)
	client := &fakeLexClient{records: []*agnostic.RepoListRecords_Record{
		{Uri: "at://" + realClaimDID + "/dev.keytrace.claim/steam-rkey", Value: &steamRaw},
		{Uri: "at://" + realClaimDID + "/dev.keytrace.claim/discord-rkey", Value: &discordRaw},
	}}
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	resolver := fakeSubjectResolver{resolved: map[string]string{"https://discord.gg/EvTSZhkk4P": "690973862245957683"}}

	if err := Discover(ctx, client, verifier, resolver, conn, &fakeReconciler{}, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	steam, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.SteamSource)
	if err != nil || steam == nil || steam.Subject != "76561197994000231" {
		t.Fatalf("got steam claim %+v, %v", steam, err)
	}
	discord, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.DiscordSource)
	if err != nil || discord == nil || discord.Subject != "690973862245957683" {
		t.Fatalf("got discord claim %+v, %v, want resolved snowflake as subject (never the raw username)", discord, err)
	}
}

func TestDiscover_DiscordVerifiedButUnresolved_LeavesNoRowRatherThanInvalidating(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	discordRaw := json.RawMessage(realDiscordClaimJSON)
	client := &fakeLexClient{records: []*agnostic.RepoListRecords_Record{
		{Uri: "at://" + realClaimDID + "/dev.keytrace.claim/discord-rkey", Value: &discordRaw},
	}}
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	resolver := fakeSubjectResolver{resolved: map[string]string{}} // hasn't joined the tracking server yet
	reconciler := &fakeReconciler{}

	if err := Discover(ctx, client, verifier, resolver, conn, reconciler, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	got, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.DiscordSource)
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v, want no row (unresolved is not the same as invalid)", got, err)
	}
}

// The username reuse hijack: a Discord claim was already resolved to a
// snowflake, and that username has since been reclaimed by someone else. The
// stored snowflake must be invalidated, never silently re-pointed at whoever
// holds the username now — proven here by using a resolver that panics if
// full resolution is ever attempted.
func TestDiscover_DiscordExistingClaimNoLongerConfirms_Invalidates(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, realClaimDID)
	appdb.UpsertClaim(ctx, conn, appdb.Claim{
		DID: realClaimDID, Type: appdb.DiscordSource, Subject: "690973862245957683",
		ClaimURI: "https://discord.gg/EvTSZhkk4P", RecordURI: "at://" + realClaimDID + "/dev.keytrace.claim/discord-rkey",
		LastVerifiedAt: time.Now(),
	})
	discordRaw := json.RawMessage(realDiscordClaimJSON)
	client := &fakeLexClient{records: []*agnostic.RepoListRecords_Record{
		{Uri: "at://" + realClaimDID + "/dev.keytrace.claim/discord-rkey", Value: &discordRaw},
	}}
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	resolver := resolveDiscordSubjectPanics{fakeSubjectResolver{confirmed: map[string]bool{"690973862245957683": false}}}
	reconciler := &fakeReconciler{}

	if err := Discover(ctx, client, verifier, resolver, conn, reconciler, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	got, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.DiscordSource)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want the claim invalidated rather than re-pointed at a different snowflake", got)
	}
}
