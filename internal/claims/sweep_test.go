// internal/claims/sweep_test.go
package claims

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

func testVerifier() *keytrace.Verifier {
	return &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
}

type fakeRecordFetcher struct {
	claims  map[string]*keytrace.Claim
	deleted map[string]bool
}

func (f fakeRecordFetcher) FetchClaimRecord(ctx context.Context, atURI string) (*keytrace.Claim, bool, error) {
	return f.claims[atURI], f.deleted[atURI], nil
}

type fakeSweepDeleter struct{ deleted []string }

func (f *fakeSweepDeleter) DeleteStatus(ctx context.Context, did string) error {
	f.deleted = append(f.deleted, did)
	return nil
}

func TestRunSweep_InvalidatesWhenRecordDeleted(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.SetSteamEnabled(ctx, conn, "did:plc:a", true)
	appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: "did:plc:a", Subject: "765", ClaimURI: "x", RecordURI: "at://did:plc:a/dev.keytrace.claim/abc", LastVerifiedAt: time.Now()})

	fetcher := fakeRecordFetcher{deleted: map[string]bool{"at://did:plc:a/dev.keytrace.claim/abc": true}}
	deleter := &fakeSweepDeleter{}

	if err := RunSweep(ctx, conn, fetcher, testVerifier(), deleter); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}

	got, err := appdb.GetSteamClaim(ctx, conn, "did:plc:a")
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if got != nil || len(deleter.deleted) != 1 {
		t.Fatalf("got claim=%+v deleted=%v, want invalidated", got, deleter.deleted)
	}
}

func TestRunSweep_LeavesValidClaimAlone(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, realClaimDID)
	appdb.SetSteamEnabled(ctx, conn, realClaimDID, true)
	err := appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: realClaimDID, Subject: "76561197994000231", ClaimURI: "x", RecordURI: "real-uri", LastVerifiedAt: time.Now()})
	if err != nil {
		t.Fatalf("UpsertSteamClaim: %v", err)
	}

	var realClaim keytrace.Claim
	if err := json.Unmarshal([]byte(realClaimJSON), &realClaim); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	fetcher := fakeRecordFetcher{claims: map[string]*keytrace.Claim{"real-uri": &realClaim}}
	deleter := &fakeSweepDeleter{}

	if err := RunSweep(ctx, conn, fetcher, testVerifier(), deleter); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}

	got, err := appdb.GetSteamClaim(ctx, conn, realClaimDID)
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if got == nil || len(deleter.deleted) != 0 {
		t.Fatalf("got claim=%+v deleted=%v, want the claim left alone", got, deleter.deleted)
	}
}
