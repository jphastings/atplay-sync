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

type fakeKeyFetcher struct{}

func (fakeKeyFetcher) FetchPublicJWK(ctx context.Context, keyURI string) (string, error) {
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

	if err := Discover(ctx, client, verifier, conn, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	got, err := appdb.GetSteamClaim(ctx, conn, realClaimDID)
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if got == nil || got.Subject != "76561197994000231" {
		t.Fatalf("got %+v, want a steam claim for subject 76561197994000231", got)
	}
}

func TestDiscover_InvalidatesWhenNoVerifiedClaimFound(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, realClaimDID)
	appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: realClaimDID, Subject: "old", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})

	client := &fakeLexClient{records: nil} // the claim is gone
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}

	if err := Discover(ctx, client, verifier, conn, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	got, err := appdb.GetSteamClaim(ctx, conn, realClaimDID)
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil after the claim disappears", got)
	}
}
