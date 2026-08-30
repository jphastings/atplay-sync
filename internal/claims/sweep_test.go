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

type fakeReconciler struct{ reconciled []string }

func (f *fakeReconciler) Reconcile(ctx context.Context, did string, now time.Time) error {
	f.reconciled = append(f.reconciled, did)
	return nil
}

func TestRunSweep_InvalidatesWhenRecordDeleted(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.SteamSource, true)
	appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: "did:plc:a", Type: appdb.SteamSource, Subject: "765", ClaimURI: "x", RecordURI: "at://did:plc:a/dev.keytrace.claim/abc", LastVerifiedAt: time.Now()})

	fetcher := fakeRecordFetcher{deleted: map[string]bool{"at://did:plc:a/dev.keytrace.claim/abc": true}}
	reconciler := &fakeReconciler{}

	if err := RunSweep(ctx, conn, fetcher, testVerifier(), fakeSubjectResolver{}, reconciler); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}

	got, err := appdb.GetClaim(ctx, conn, "did:plc:a", appdb.SteamSource)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if got != nil || len(reconciler.reconciled) != 1 {
		t.Fatalf("got claim=%+v reconciled=%v, want invalidated", got, reconciler.reconciled)
	}
}

// A missed Jetstream *update* — re-verified against a different SteamID at the
// same record — is exactly what the sweep is the backstop for. Confirming the
// signature isn't enough; the stored subject has to catch up, or we keep
// polling the old one.
func TestRunSweep_ReconcilesAChangedSubject(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, realClaimDID)
	appdb.SetEnabled(ctx, conn, realClaimDID, appdb.SteamSource, true)
	appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: realClaimDID, Type: appdb.SteamSource, Subject: "an-old-steamid", ClaimURI: "x", RecordURI: "real-uri", LastVerifiedAt: time.Now()})

	var realClaim keytrace.Claim
	if err := json.Unmarshal([]byte(realClaimJSON), &realClaim); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	fetcher := fakeRecordFetcher{claims: map[string]*keytrace.Claim{"real-uri": &realClaim}}

	if err := RunSweep(ctx, conn, fetcher, testVerifier(), fakeSubjectResolver{}, &fakeReconciler{}); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}

	got, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.SteamSource)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if got == nil || got.Subject != "76561197994000231" {
		t.Fatalf("got %+v, want the subject from the re-fetched claim", got)
	}
}

func TestRunSweep_LeavesValidClaimAlone(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, realClaimDID)
	appdb.SetEnabled(ctx, conn, realClaimDID, appdb.SteamSource, true)
	err := appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: realClaimDID, Type: appdb.SteamSource, Subject: "76561197994000231", ClaimURI: "x", RecordURI: "real-uri", LastVerifiedAt: time.Now()})
	if err != nil {
		t.Fatalf("UpsertClaim: %v", err)
	}

	var realClaim keytrace.Claim
	if err := json.Unmarshal([]byte(realClaimJSON), &realClaim); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	fetcher := fakeRecordFetcher{claims: map[string]*keytrace.Claim{"real-uri": &realClaim}}
	reconciler := &fakeReconciler{}

	if err := RunSweep(ctx, conn, fetcher, testVerifier(), fakeSubjectResolver{}, reconciler); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}

	got, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.SteamSource)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if got == nil || len(reconciler.reconciled) != 0 {
		t.Fatalf("got claim=%+v reconciled=%v, want the claim left alone", got, reconciler.reconciled)
	}
}

func TestRunSweep_DiscordNoLongerResolvable_Invalidates(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, realClaimDID)
	appdb.SetEnabled(ctx, conn, realClaimDID, appdb.DiscordSource, true)
	appdb.UpsertClaim(ctx, conn, appdb.Claim{
		DID: realClaimDID, Type: appdb.DiscordSource, Subject: "690973862245957683",
		ClaimURI: "https://discord.gg/EvTSZhkk4P", RecordURI: "at://" + realClaimDID + "/dev.keytrace.claim/discord-rkey",
		LastVerifiedAt: time.Now(),
	})

	var discordClaim keytrace.Claim
	if err := json.Unmarshal([]byte(realDiscordClaimJSON), &discordClaim); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	fetcher := fakeRecordFetcher{claims: map[string]*keytrace.Claim{
		"at://" + realClaimDID + "/dev.keytrace.claim/discord-rkey": &discordClaim,
	}}
	resolver := fakeSubjectResolver{resolved: map[string]string{}} // no longer in the server / renamed
	reconciler := &fakeReconciler{}

	if err := RunSweep(ctx, conn, fetcher, testVerifier(), resolver, reconciler); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}
	got, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.DiscordSource)
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v, want invalidated", got, err)
	}
}
