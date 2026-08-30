// internal/sync/tick_test.go
package sync

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/steam"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

type fakeSteamAPI struct {
	summaries map[string]steam.PlayerSummary
	err       error
}

func (f fakeSteamAPI) GetPlayerSummaries(ctx context.Context, ids []string) (map[string]steam.PlayerSummary, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.summaries, nil
}

type fakeResolver struct{ games map[string]*appdb.CachedGame }

func (f fakeResolver) GetGameBySteamID(ctx context.Context, appID string) (*appdb.CachedGame, error) {
	return f.games[appID], nil
}

type recordedPut struct {
	did    string
	status ActorStatus
}

type fakeWriter struct {
	puts    []recordedPut
	deletes []string
}

func (f *fakeWriter) PutStatus(ctx context.Context, did string, status ActorStatus) error {
	f.puts = append(f.puts, recordedPut{did, status})
	return nil
}

func (f *fakeWriter) DeleteStatus(ctx context.Context, did string) error {
	f.deletes = append(f.deletes, did)
	return nil
}

func seedEligibleUser(t *testing.T, conn *sql.DB, did, steamID string) {
	t.Helper()
	ctx := context.Background()
	if err := appdb.UpsertUser(ctx, conn, did); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	err := appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: did, Type: appdb.SteamSource, Subject: steamID, ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})
	if err != nil {
		t.Fatalf("UpsertClaim: %v", err)
	}
}

func TestRunTick_PlayingResolvableGame_WritesStatus(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765", GameID: "271590"}}}
	resolver := fakeResolver{games: map[string]*appdb.CachedGame{
		"271590": {URI: "at://cartridge/games.gamesgamesgamesgames.game/gta5", PageURL: "https://cartridge.dev/game/gta5", Name: "GTA V", Summary: "An open world game."},
	}}
	writer := &fakeWriter{}

	if err := RunTick(ctx, conn, steamAPI, resolver, writer, steam.NewBudget(1000), time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}

	if len(writer.puts) != 1 {
		t.Fatalf("got %d puts, want 1", len(writer.puts))
	}
	got := writer.puts[0]
	if got.did != "did:plc:a" || got.status.Game != "at://cartridge/games.gamesgamesgamesgames.game/gta5" || got.status.Embed.External.Title != "GTA V" {
		t.Fatalf("got %+v", got)
	}
}

func TestRunTick_PlayingUnresolvableGame_RecordsSessionButDeletesRecord(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765", GameID: "999999"}}}
	resolver := fakeResolver{games: map[string]*appdb.CachedGame{}} // cartridge doesn't know this appid
	writer := &fakeWriter{}

	if err := RunTick(ctx, conn, steamAPI, resolver, writer, steam.NewBudget(1000), time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	// An unresolvable game is, from the reconciler's point of view, the same
	// as this source not currently playing — with no other source to fall
	// through to, that means deleting the record rather than leaving a
	// stale, wrong game showing.
	if len(writer.puts) != 0 || len(writer.deletes) != 1 || writer.deletes[0] != "did:plc:a" {
		t.Fatalf("got puts=%+v deletes=%+v, want a delete", writer.puts, writer.deletes)
	}
	row, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if row == nil || row.GameKey != "999999" {
		t.Fatalf("got %+v, want session_starts recorded for appid 999999 even though unresolved", row)
	}
}

func TestRunTick_SteamOmitsAccount_SkipsWithoutDeleteOrSessionReset(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	startedAt := time.Now().Add(-30 * time.Minute)
	if err := appdb.SetSessionStart(ctx, conn, "did:plc:a", "steam", "271590", startedAt); err != nil {
		t.Fatalf("SetSessionStart: %v", err)
	}
	before, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart (before): %v", err)
	}
	if before == nil {
		t.Fatalf("expected seeded session_starts row before tick")
	}

	// Steam's response simply doesn't mention this account — not an explicit not-playing entry.
	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{}}
	writer := &fakeWriter{}

	if err := RunTick(ctx, conn, steamAPI, fakeResolver{}, writer, steam.NewBudget(1000), time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if len(writer.puts) != 0 || len(writer.deletes) != 0 {
		t.Fatalf("got puts=%+v deletes=%+v, want neither", writer.puts, writer.deletes)
	}

	after, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart (after): %v", err)
	}
	if after == nil || after.GameKey != before.GameKey || !after.StartedAt.Equal(before.StartedAt) {
		t.Fatalf("session_starts changed: before=%+v after=%+v, want untouched", before, after)
	}
}

func TestRunTick_NotPlaying_Deletes(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765"}}} // no GameID
	writer := &fakeWriter{}

	if err := RunTick(ctx, conn, steamAPI, fakeResolver{}, writer, steam.NewBudget(1000), time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if len(writer.deletes) != 1 || writer.deletes[0] != "did:plc:a" {
		t.Fatalf("got deletes=%+v, want [did:plc:a]", writer.deletes)
	}
}

func TestRunTick_BudgetExhausted_SkipsTickUntouched(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765", GameID: "271590"}}}
	resolver := fakeResolver{games: map[string]*appdb.CachedGame{
		"271590": {URI: "at://cartridge/games.gamesgamesgamesgames.game/gta5", Name: "GTA V"},
	}}
	writer := &fakeWriter{}
	budget := steam.NewBudget(0) // today's budget is already spent

	if err := RunTick(ctx, conn, steamAPI, resolver, writer, budget, time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if len(writer.puts) != 0 || len(writer.deletes) != 0 {
		t.Fatalf("got puts=%+v deletes=%+v, want neither (budget exhausted, Steam never called)", writer.puts, writer.deletes)
	}
}

func TestRunTick_RateLimited_ExhaustsBudgetAndReturnsError(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	steamAPI := fakeSteamAPI{err: steam.ErrRateLimited}
	writer := &fakeWriter{}
	budget := steam.NewBudget(1000)

	if err := RunTick(ctx, conn, steamAPI, fakeResolver{}, writer, budget, time.Now()); !errors.Is(err, steam.ErrRateLimited) {
		t.Fatalf("RunTick err = %v, want it to wrap steam.ErrRateLimited", err)
	}
	if budget.Reserve(1) {
		t.Fatal("budget.Reserve(1) after a 429 = true, want false (Exhaust should have zeroed it)")
	}
}
