// internal/sync/reconcile_test.go
package sync

import (
	"context"
	"testing"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestParseAtURI(t *testing.T) {
	cases := []struct {
		name                              string
		uri                               string
		wantDID, wantCollection, wantRkey string
		wantOK                            bool
	}{
		{"valid", "at://did:plc:abc/games.gamesgamesgamesgames.game/gta5", "did:plc:abc", "games.gamesgamesgamesgames.game", "gta5", true},
		{"missing prefix", "did:plc:abc/games.gamesgamesgamesgames.game/gta5", "", "", "", false},
		{"too few segments", "at://did:plc:abc/games.gamesgamesgamesgames.game", "", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			did, collection, rkey, ok := parseAtURI(c.uri)
			if did != c.wantDID || collection != c.wantCollection || rkey != c.wantRkey || ok != c.wantOK {
				t.Fatalf("parseAtURI(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
					c.uri, did, collection, rkey, ok, c.wantDID, c.wantCollection, c.wantRkey, c.wantOK)
			}
		})
	}
}

func TestReconcile_SameGame_HighestPrioritySourceWins(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	did := "did:plc:a"
	appdb.UpsertUser(ctx, conn, did)
	appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, true)   // default priority 0
	appdb.SetEnabled(ctx, conn, did, appdb.DiscordSource, true) // default priority 1
	steamStart := time.Now().Add(-30 * time.Minute)
	discordStart := time.Now().Add(-5 * time.Minute)
	appdb.SetSessionStart(ctx, conn, did, appdb.SteamSource, "570", steamStart)
	appdb.SetSessionStart(ctx, conn, did, appdb.DiscordSource, "570", discordStart) // same game, both sources

	resolver := fakeResolver{games: map[string]*appdb.CachedGame{
		"570": {URI: "at://cartridge/games.gamesgamesgamesgames.game/dota2", Name: "Dota 2"},
	}}
	// A leftover record for this exact game is already live — Put must still
	// refresh it (bumping staleAt), and it must not be deleted.
	writer := &fakeWriter{live: map[string][]StatusEntry{did: {{Rkey: "dota2", StaleAt: time.Now().Add(time.Hour)}}}}
	r := &Reconciler{Conn: conn, Resolver: resolver, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.puts) != 1 || writer.puts[0].rkey != "dota2" {
		t.Fatalf("got puts=%+v, want one put for rkey dota2", writer.puts)
	}
	if got, want := writer.puts[0].status.CreatedAt, steamStart.UTC().Format(time.RFC3339); got != want {
		t.Fatalf("got createdAt=%s, want Steam's (higher priority) start time %s", got, want)
	}
	if len(writer.deletes) != 0 {
		t.Fatalf("got deletes=%+v, want none — the live record for this game is still desired", writer.deletes)
	}
}

func TestReconcile_DifferentGames_BothPublished(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	did := "did:plc:a"
	appdb.UpsertUser(ctx, conn, did)
	appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, true)
	appdb.SetEnabled(ctx, conn, did, appdb.DiscordSource, true)
	appdb.SetSessionStart(ctx, conn, did, appdb.SteamSource, "570", time.Now())
	appdb.SetSessionStart(ctx, conn, did, appdb.DiscordSource, "271590", time.Now())

	resolver := fakeResolver{games: map[string]*appdb.CachedGame{
		"570":    {URI: "at://cartridge/games.gamesgamesgamesgames.game/dota2", Name: "Dota 2"},
		"271590": {URI: "at://cartridge/games.gamesgamesgamesgames.game/gta5", Name: "GTA V"},
	}}
	writer := &fakeWriter{}
	r := &Reconciler{Conn: conn, Resolver: resolver, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	gotRkeys := map[string]bool{}
	for _, p := range writer.puts {
		gotRkeys[p.rkey] = true
	}
	if len(writer.puts) != 2 || !gotRkeys["dota2"] || !gotRkeys["gta5"] {
		t.Fatalf("got puts=%+v, want one each for dota2 and gta5", writer.puts)
	}
}

func TestReconcile_FallsThroughWhenTopSourceUnresolvable(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	did := "did:plc:a"
	appdb.UpsertUser(ctx, conn, did)
	appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, true)
	appdb.SetEnabled(ctx, conn, did, appdb.DiscordSource, true)
	appdb.SetSessionStart(ctx, conn, did, appdb.SteamSource, "999999", time.Now()) // unresolvable
	appdb.SetSessionStart(ctx, conn, did, appdb.DiscordSource, "271590", time.Now())

	resolver := fakeResolver{games: map[string]*appdb.CachedGame{"271590": {URI: "at://cartridge/games.gamesgamesgamesgames.game/gta5", Name: "GTA V"}}}
	writer := &fakeWriter{}
	r := &Reconciler{Conn: conn, Resolver: resolver, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.puts) != 1 || writer.puts[0].rkey != "gta5" {
		t.Fatalf("got puts=%+v, want Discord's game (Steam unresolvable)", writer.puts)
	}
}

func TestReconcile_DisabledSourceIgnoredEvenIfPlaying(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	did := "did:plc:a"
	appdb.UpsertUser(ctx, conn, did)
	appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, false)
	appdb.SetSessionStart(ctx, conn, did, appdb.SteamSource, "570", time.Now())

	// A record from before the source was disabled is still live — disabling
	// must tear it down even though nothing is "desired" to replace it.
	writer := &fakeWriter{live: map[string][]StatusEntry{did: {{Rkey: "leftover", StaleAt: time.Now().Add(time.Hour)}}}}
	r := &Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.puts) != 0 || len(writer.deletes) != 1 || writer.deletes[0] != (recordedDelete{did: did, rkey: "leftover"}) {
		t.Fatalf("got puts=%+v deletes=%+v, want a delete of leftover (source disabled)", writer.puts, writer.deletes)
	}
}

func TestReconcile_NobodyPlaying_Deletes(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	did := "did:plc:a"
	appdb.UpsertUser(ctx, conn, did)
	appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, true)

	writer := &fakeWriter{live: map[string][]StatusEntry{did: {{Rkey: "leftover", StaleAt: time.Now().Add(time.Hour)}}}}
	r := &Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.deletes) != 1 || writer.deletes[0] != (recordedDelete{did: did, rkey: "leftover"}) {
		t.Fatalf("got deletes=%+v, want [{%s leftover}]", writer.deletes, did)
	}
}

func TestReconcile_GameSwitch_DeletesOldPutsNew(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	did := "did:plc:a"
	appdb.UpsertUser(ctx, conn, did)
	appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, true)
	appdb.SetSessionStart(ctx, conn, did, appdb.SteamSource, "271590", time.Now())

	resolver := fakeResolver{games: map[string]*appdb.CachedGame{
		"271590": {URI: "at://cartridge/games.gamesgamesgamesgames.game/gta5", Name: "GTA V"},
	}}
	// Simulates: was playing a different game a moment ago (still live on the
	// PDS), session_starts now says the new one.
	writer := &fakeWriter{live: map[string][]StatusEntry{did: {{Rkey: "old-game", StaleAt: time.Now().Add(time.Hour)}}}}
	r := &Reconciler{Conn: conn, Resolver: resolver, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.puts) != 1 || writer.puts[0].rkey != "gta5" {
		t.Fatalf("got puts=%+v, want one put for gta5", writer.puts)
	}
	if len(writer.deletes) != 1 || writer.deletes[0] != (recordedDelete{did: did, rkey: "old-game"}) {
		t.Fatalf("got deletes=%+v, want one delete for old-game", writer.deletes)
	}
}

func TestReconcile_ForeignViaRecord_NotDeleted(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	did := "did:plc:a"
	appdb.UpsertUser(ctx, conn, did)
	appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, true)

	writer := &fakeWriter{live: map[string][]StatusEntry{did: {{Rkey: "someone-elses-game", StaleAt: time.Now().Add(time.Hour), Via: "a-different-instance.example"}}}}
	r := &Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.deletes) != 0 {
		t.Fatalf("got deletes=%+v, want none — this record belongs to a different via, not ours to touch", writer.deletes)
	}
}

func TestReconcile_UnresolvableGameURI_Skipped(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	did := "did:plc:a"
	appdb.UpsertUser(ctx, conn, did)
	appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, true)
	appdb.SetSessionStart(ctx, conn, did, appdb.SteamSource, "570", time.Now())

	resolver := fakeResolver{games: map[string]*appdb.CachedGame{"570": {URI: "not-an-at-uri", Name: "Broken"}}}
	writer := &fakeWriter{}
	r := &Reconciler{Conn: conn, Resolver: resolver, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.puts) != 0 {
		t.Fatalf("got puts=%+v, want none — a malformed game URI can't be published", writer.puts)
	}
}
