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
	did, rkey string
	status    ActorStatus
}

type recordedDelete struct {
	did, rkey string
}

// fakeWriter's live map is what ListStatuses returns per did — seed it to
// simulate records already published in an earlier reconcile so Put/Delete
// diffing has something real to react to. It's a static snapshot (Put/Delete
// calls are only recorded, not applied back into `live`), which is enough
// for every test here: each one only asserts what Reconcile *decided* to do
// in a single pass, not multi-pass convergence.
type fakeWriter struct {
	live    map[string][]StatusEntry
	puts    []recordedPut
	deletes []recordedDelete
}

func (f *fakeWriter) PutStatus(ctx context.Context, did, rkey string, status ActorStatus) error {
	f.puts = append(f.puts, recordedPut{did, rkey, status})
	return nil
}

func (f *fakeWriter) DeleteStatus(ctx context.Context, did, rkey string) error {
	f.deletes = append(f.deletes, recordedDelete{did, rkey})
	return nil
}

func (f *fakeWriter) ListStatuses(ctx context.Context, did string) ([]StatusEntry, error) {
	return f.live[did], nil
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
	if got.did != "did:plc:a" || got.rkey != "gta5" || got.status.Game != "at://cartridge/games.gamesgamesgamesgames.game/gta5" || got.status.Embed.External.Title != "GTA V" {
		t.Fatalf("got %+v", got)
	}
}

func TestRunTick_PlayingUnresolvableGame_RecordsSessionButDeletesRecord(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765", GameID: "999999"}}}
	resolver := fakeResolver{games: map[string]*appdb.CachedGame{}} // cartridge doesn't know this appid
	writer := &fakeWriter{live: map[string][]StatusEntry{"did:plc:a": {{Rkey: "old-rkey", StaleAt: time.Now().Add(time.Hour)}}}}

	if err := RunTick(ctx, conn, steamAPI, resolver, writer, steam.NewBudget(1000), time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	// An unresolvable game is, from the reconciler's point of view, the same
	// as this source not currently playing — with no other source to fall
	// through to, that means deleting whatever record was live rather than
	// leaving a stale, wrong game showing.
	if len(writer.puts) != 0 || len(writer.deletes) != 1 || writer.deletes[0] != (recordedDelete{did: "did:plc:a", rkey: "old-rkey"}) {
		t.Fatalf("got puts=%+v deletes=%+v, want a delete of old-rkey", writer.puts, writer.deletes)
	}
	row, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if row == nil || row.GameKey != "999999" {
		t.Fatalf("got %+v, want session_starts recorded for appid 999999 even though unresolved", row)
	}
}

func TestRunTick_PlayingGame_PersistsRawName(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765", GameID: "999999", GameExtraInfo: "Some Unreleased Game"}}}
	writer := &fakeWriter{}

	if err := RunTick(ctx, conn, steamAPI, fakeResolver{}, writer, steam.NewBudget(1000), time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	row, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if row == nil || row.RawName != "Some Unreleased Game" {
		t.Fatalf("got %+v, want RawName from Steam's GameExtraInfo, even for an unresolvable app", row)
	}
}

func TestRunTick_NameWithoutAppID_RecordsUnresolvableSession(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	// Steam reports some non-Steam shortcuts by name only, with no app id
	// (a Steam Deck Game Mode shortcut, say).
	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765", GameExtraInfo: "Discord"}}}
	writer := &fakeWriter{}

	if err := RunTick(ctx, conn, steamAPI, fakeResolver{}, writer, steam.NewBudget(1000), time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}

	row, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if row == nil || row.GameKey != "steam-shortcut:Discord" || row.RawName != "Discord" {
		t.Fatalf("got %+v, want a namespaced session recorded under the reported name rather than treated as not playing", row)
	}
	if len(writer.puts) != 0 {
		t.Fatalf("got puts=%+v, want none — a name with no app id can never resolve to a game", writer.puts)
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
	writer := &fakeWriter{live: map[string][]StatusEntry{"did:plc:a": {{Rkey: "was-playing", StaleAt: time.Now().Add(time.Hour)}}}}

	if err := RunTick(ctx, conn, steamAPI, fakeResolver{}, writer, steam.NewBudget(1000), time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if len(writer.deletes) != 1 || writer.deletes[0] != (recordedDelete{did: "did:plc:a", rkey: "was-playing"}) {
		t.Fatalf("got deletes=%+v, want a delete of was-playing", writer.deletes)
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

func TestRunTick_RecordsOnlineStateEvenWithNoGame(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765", Online: true}}}
	if err := RunTick(ctx, conn, steamAPI, fakeResolver{}, &fakeWriter{}, steam.NewBudget(1000), time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	online, err := appdb.IsSourceOnline(ctx, conn, "did:plc:a", appdb.SteamSource)
	if err != nil || !online {
		t.Fatalf("IsSourceOnline = %v, %v, want true (online, just not playing)", online, err)
	}

	// Going offline has to be recorded too, or the dot would claim they're
	// still online forever.
	steamAPI = fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765", Online: false}}}
	if err := RunTick(ctx, conn, steamAPI, fakeResolver{}, &fakeWriter{}, steam.NewBudget(1000), time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	online, err = appdb.IsSourceOnline(ctx, conn, "did:plc:a", appdb.SteamSource)
	if err != nil || online {
		t.Fatalf("IsSourceOnline = %v, %v, want false", online, err)
	}
}
