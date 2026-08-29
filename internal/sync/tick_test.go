// internal/sync/tick_test.go
package sync

import (
	"context"
	"database/sql"
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

type fakeSteamAPI struct{ summaries map[string]steam.PlayerSummary }

func (f fakeSteamAPI) GetPlayerSummaries(ctx context.Context, ids []string) (map[string]steam.PlayerSummary, error) {
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
	if err := appdb.SetSteamEnabled(ctx, conn, did, true); err != nil {
		t.Fatalf("SetSteamEnabled: %v", err)
	}
	err := appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: did, Subject: steamID, ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})
	if err != nil {
		t.Fatalf("UpsertSteamClaim: %v", err)
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

	if err := RunTick(ctx, conn, steamAPI, resolver, writer, time.Now()); err != nil {
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

func TestRunTick_PlayingUnresolvableGame_SkipsWriteButRecordsSession(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765", GameID: "999999"}}}
	resolver := fakeResolver{games: map[string]*appdb.CachedGame{}} // cartridge doesn't know this appid
	writer := &fakeWriter{}

	if err := RunTick(ctx, conn, steamAPI, resolver, writer, time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if len(writer.puts) != 0 || len(writer.deletes) != 0 {
		t.Fatalf("got puts=%+v deletes=%+v, want neither", writer.puts, writer.deletes)
	}
	row, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", "steam")
	if err != nil {
		t.Fatalf("GetSessionStart: %v", err)
	}
	if row == nil || row.GameKey != "999999" {
		t.Fatalf("got %+v, want session_starts recorded for appid 999999 even though unresolved", row)
	}
}

func TestRunTick_NotPlaying_Deletes(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedEligibleUser(t, conn, "did:plc:a", "765")

	steamAPI := fakeSteamAPI{summaries: map[string]steam.PlayerSummary{"765": {SteamID: "765"}}} // no GameID
	writer := &fakeWriter{}

	if err := RunTick(ctx, conn, steamAPI, fakeResolver{}, writer, time.Now()); err != nil {
		t.Fatalf("RunTick: %v", err)
	}
	if len(writer.deletes) != 1 || writer.deletes[0] != "did:plc:a" {
		t.Fatalf("got deletes=%+v, want [did:plc:a]", writer.deletes)
	}
}
