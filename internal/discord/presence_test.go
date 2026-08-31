// internal/discord/presence_test.go
package discord

import (
	"context"
	"database/sql"
	"encoding/json"
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
	did, rkey string
	status    appsync.ActorStatus
}

type recordedDelete struct {
	did, rkey string
}

type fakeWriter struct {
	live    map[string][]appsync.StatusEntry
	puts    []recordedPut
	deletes []recordedDelete
}

func (f *fakeWriter) PutStatus(ctx context.Context, did, rkey string, status appsync.ActorStatus) error {
	f.puts = append(f.puts, recordedPut{did, rkey, status})
	return nil
}

func (f *fakeWriter) DeleteStatus(ctx context.Context, did, rkey string) error {
	f.deletes = append(f.deletes, recordedDelete{did, rkey})
	return nil
}

func (f *fakeWriter) ListStatuses(ctx context.Context, did string) ([]appsync.StatusEntry, error) {
	return f.live[did], nil
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
		"570": {URI: "at://cartridge/games.gamesgamesgamesgames.game/dota2", Name: "Dota 2"},
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

// newTestSession builds a *discordgo.Session whose State is seeded with the
// tracking guild and the given presences — discordgo's own real State type
// used as the test fixture, since HandlePresenceUpdate reads co-players'
// presences straight off it (see partyMemberDIDs).
func newTestSession(t *testing.T, presences ...*discordgo.Presence) *discordgo.Session {
	t.Helper()
	sess := &discordgo.Session{State: discordgo.NewState()}
	if err := sess.State.GuildAdd(&discordgo.Guild{ID: guildID}); err != nil {
		t.Fatalf("GuildAdd: %v", err)
	}
	for _, p := range presences {
		if err := sess.State.PresenceAdd(guildID, p); err != nil {
			t.Fatalf("PresenceAdd: %v", err)
		}
	}
	return sess
}

func TestPresenceHandler_StateDetailsAndParty_Captured(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	seedDiscordUser(t, conn, "did:plc:a", "690973862245957683") // primary, playing
	seedDiscordUser(t, conn, "did:plc:b", "111111111111111111") // co-player, linked+enabled
	seedDiscordUser(t, conn, "did:plc:c", "222222222222222222") // co-player, linked but disabled
	appdb.SetEnabled(ctx, conn, "did:plc:c", appdb.DiscordSource, false)
	// A fourth guild member is in the same party but was never linked to
	// this app at all — findClaimByDiscordID simply won't find them.

	games := NewGameIndex()
	games.steamAppID = map[string]string{"356875988589740042": "570"}
	reconciler := &appsync.Reconciler{Conn: conn, Resolver: fakeResolver{games: map[string]*appdb.CachedGame{
		"570": {URI: "at://cartridge/dota2", Name: "Dota 2"},
	}}, Writer: &fakeWriter{}}
	h := &PresenceHandler{Conn: conn, GuildID: guildID, Games: games, Reconciler: reconciler}

	party := discordgo.Party{ID: "party-1", Size: []int{3, 5}}
	inParty := func(id string) *discordgo.Presence {
		return &discordgo.Presence{User: &discordgo.User{ID: id}, Activities: []*discordgo.Activity{
			{Type: discordgo.ActivityTypeGame, ApplicationID: "356875988589740042", Party: party},
		}}
	}
	sess := newTestSession(t,
		inParty("690973862245957683"), // primary
		inParty("111111111111111111"), // linked+enabled co-player
		inParty("222222222222222222"), // linked but disabled co-player
		inParty("999999999999999999"), // never linked
	)

	h.HandlePresenceUpdate(sess, &discordgo.PresenceUpdate{Presence: discordgo.Presence{
		User: &discordgo.User{ID: "690973862245957683"},
		Activities: []*discordgo.Activity{
			{Type: discordgo.ActivityTypeGame, ApplicationID: "356875988589740042", State: "In a match", Details: "Diamond II", Party: party},
		},
	}, GuildID: guildID})

	row, err := appdb.GetSessionStart(ctx, conn, "did:plc:a", appdb.DiscordSource)
	if err != nil || row == nil {
		t.Fatalf("GetSessionStart = %+v, %v", row, err)
	}
	var extra appsync.SessionExtra
	if err := json.Unmarshal([]byte(row.Extra), &extra); err != nil {
		t.Fatalf("unmarshal extra %q: %v", row.Extra, err)
	}
	if extra.State != "In a match" || extra.Details != "Diamond II" {
		t.Fatalf("got state=%q details=%q, want %q/%q", extra.State, extra.Details, "In a match", "Diamond II")
	}
	if extra.PartyCurrent != 3 || extra.PartyMax != 5 {
		t.Fatalf("got party current=%d max=%d, want 3/5 (raw Discord count, regardless of resolvability)", extra.PartyCurrent, extra.PartyMax)
	}
	wantDIDs := map[string]bool{"did:plc:a": true, "did:plc:b": true}
	if len(extra.PartyDIDs) != len(wantDIDs) {
		t.Fatalf("got dids=%v, want exactly %v (disabled and unlinked co-players excluded)", extra.PartyDIDs, wantDIDs)
	}
	for _, d := range extra.PartyDIDs {
		if !wantDIDs[d] {
			t.Fatalf("got unexpected did %q in %v", d, extra.PartyDIDs)
		}
	}
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
