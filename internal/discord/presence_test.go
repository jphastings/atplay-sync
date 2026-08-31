// internal/discord/presence_test.go
package discord

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	appdb "github.com/jphastings/game-status/internal/db"
	appsync "github.com/jphastings/game-status/internal/sync"
)

// openTestDB, fakeResolver and fakeWriter mirror internal/sync/tick_test.go's
// unexported test doubles exactly — duplicated here rather than imported,
// since Go test types aren't exported across package boundaries and this
// project already keeps each package's fakes local to it (see
// internal/claims/discover_test.go's own fakeKeyFetcher for the same pattern).
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

type fakeResolver struct{ games map[string]*appdb.CachedGame }

func (f fakeResolver) GetGameBySteamID(ctx context.Context, appID string) (*appdb.CachedGame, error) {
	return f.games[appID], nil
}

type recordedPut struct {
	did    string
	status appsync.ActorStatus
}

type fakeWriter struct {
	puts    []recordedPut
	deletes []string
}

func (f *fakeWriter) PutStatus(ctx context.Context, did string, status appsync.ActorStatus) error {
	f.puts = append(f.puts, recordedPut{did, status})
	return nil
}

func (f *fakeWriter) DeleteStatus(ctx context.Context, did string) error {
	f.deletes = append(f.deletes, did)
	return nil
}

func seedDiscordUser(t *testing.T, conn *sql.DB, did, discordID string) {
	t.Helper()
	ctx := context.Background()
	if err := appdb.UpsertUser(ctx, conn, did); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if err := appdb.SetEnabled(ctx, conn, did, appdb.DiscordSource, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := appdb.UpsertClaim(ctx, conn, appdb.Claim{DID: did, Type: appdb.DiscordSource, Subject: discordID, ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertClaim: %v", err)
	}
}

func TestPresenceHandler_PlayingResolvableGame_UpdatesSession(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t) // reuse the shared helper from internal/db's test package pattern; here, a local copy per internal/discord's own tests
	seedDiscordUser(t, conn, "did:plc:a", "690973862245957683")

	games := NewGameIndex()
	games.steamAppID = map[string]string{"356875988589740042": "570"}
	reconciler := &appsync.Reconciler{Conn: conn, Resolver: fakeResolver{games: map[string]*appdb.CachedGame{
		"570": {URI: "at://cartridge/dota2", Name: "Dota 2"},
	}}, Writer: &fakeWriter{}}
	h := &PresenceHandler{Conn: conn, GuildID: guildID, Games: games, Reconciler: reconciler}

	h.HandlePresenceUpdate(nil, &discordgo.PresenceUpdate{Presence: discordgo.Presence{
		User:       &discordgo.User{ID: "690973862245957683"},
		Activities: []*discordgo.Activity{{Type: discordgo.ActivityTypeGame, ApplicationID: "356875988589740042"}},
	}, GuildID: guildID})

	row, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", appdb.DiscordSource)
	if err != nil || row == nil || row.GameKey != "570" {
		t.Fatalf("GetSessionStart = %+v, %v, want game_key 570", row, err)
	}
}

func TestPresenceHandler_NotPlaying_ClearsSession(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedDiscordUser(t, conn, "did:plc:a", "690973862245957683")
	appdb.SetSessionStart(ctx, conn, "did:plc:a", appdb.DiscordSource, "570", time.Now())

	h := &PresenceHandler{Conn: conn, GuildID: guildID, Games: NewGameIndex(), Reconciler: &appsync.Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: &fakeWriter{}}}
	h.HandlePresenceUpdate(nil, &discordgo.PresenceUpdate{Presence: discordgo.Presence{
		User: &discordgo.User{ID: "690973862245957683"}, Activities: nil,
	}, GuildID: guildID})

	row, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", appdb.DiscordSource)
	if err != nil || row != nil {
		t.Fatalf("GetSessionStart = %+v, %v, want nil (cleared)", row, err)
	}
}

func TestPresenceHandler_UnclaimedUser_Ignored(t *testing.T) {
	conn := openTestDB(t)
	h := &PresenceHandler{Conn: conn, GuildID: guildID, Games: NewGameIndex(), Reconciler: &appsync.Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: &fakeWriter{}}}

	// No user with this Discord ID has an enabled+claimed sync_prefs row —
	// must not error, must not create any state.
	h.HandlePresenceUpdate(nil, &discordgo.PresenceUpdate{Presence: discordgo.Presence{
		User: &discordgo.User{ID: "999"}, Activities: []*discordgo.Activity{{Type: discordgo.ActivityTypeGame, ApplicationID: "x"}},
	}, GuildID: guildID})
	// no assertion beyond "did not panic/error" — covered by the test simply completing
}

func TestHandleGuildMemberRemove_InvalidatesDiscordClaim(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedDiscordUser(t, conn, "did:plc:a", "690973862245957683")

	h := &PresenceHandler{Conn: conn, GuildID: guildID, Games: NewGameIndex(), Reconciler: &appsync.Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: &fakeWriter{}}}
	if err := h.HandleGuildMemberRemove(ctx, "690973862245957683"); err != nil {
		t.Fatalf("HandleGuildMemberRemove: %v", err)
	}

	claim, err := appdb.GetClaim(ctx, conn, "did:plc:a", appdb.DiscordSource)
	if err != nil || claim != nil {
		t.Fatalf("got claim %+v, %v, want invalidated", claim, err)
	}
	enabled, err := appdb.IsEnabled(ctx, conn, "did:plc:a", appdb.DiscordSource)
	if err != nil || enabled {
		t.Fatalf("IsEnabled = %v, %v, want false", enabled, err)
	}
}
