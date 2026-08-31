// internal/sync/reconcile_test.go
package sync

import (
	"context"
	"testing"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestReconcile_HighestPriorityPlayingSourceWins(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	did := "did:plc:a"
	appdb.UpsertUser(ctx, conn, did)
	appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, true)   // default priority 0
	appdb.SetEnabled(ctx, conn, did, appdb.DiscordSource, true) // default priority 1
	appdb.SetSessionStart(ctx, conn, did, appdb.SteamSource, "570", time.Now())
	appdb.SetSessionStart(ctx, conn, did, appdb.DiscordSource, "271590", time.Now())

	resolver := fakeResolver{games: map[string]*appdb.CachedGame{
		"570":    {URI: "at://cartridge/dota2", Name: "Dota 2"},
		"271590": {URI: "at://cartridge/gta5", Name: "GTA V"},
	}}
	writer := &fakeWriter{}
	r := &Reconciler{Conn: conn, Resolver: resolver, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.puts) != 1 || writer.puts[0].status.Game != "at://cartridge/dota2" {
		t.Fatalf("got puts=%+v, want Steam's game (higher priority)", writer.puts)
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

	resolver := fakeResolver{games: map[string]*appdb.CachedGame{"271590": {URI: "at://cartridge/gta5", Name: "GTA V"}}}
	writer := &fakeWriter{}
	r := &Reconciler{Conn: conn, Resolver: resolver, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.puts) != 1 || writer.puts[0].status.Game != "at://cartridge/gta5" {
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

	writer := &fakeWriter{}
	r := &Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.deletes) != 1 {
		t.Fatalf("got puts=%+v deletes=%+v, want a delete (source disabled)", writer.puts, writer.deletes)
	}
}

func TestReconcile_NobodyPlaying_Deletes(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	did := "did:plc:a"
	appdb.UpsertUser(ctx, conn, did)
	appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, true)

	writer := &fakeWriter{}
	r := &Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: writer}

	if err := r.Reconcile(ctx, did, time.Now()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(writer.deletes) != 1 || writer.deletes[0] != did {
		t.Fatalf("got deletes=%+v, want [%s]", writer.deletes, did)
	}
}

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
