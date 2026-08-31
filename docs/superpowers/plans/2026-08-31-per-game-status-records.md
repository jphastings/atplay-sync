# Per-game status records Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Switch `games.atmosphere.status`'s rkey from the literal `"self"` to the rkey of the game record being played, so multiple sources reporting different games simultaneously each get their own status record, add a daily sweep that deletes any stale-past-`staleAt` status record as a safety net, and update the frontend to render every live status instead of assuming one.

**Architecture:** `Reconciler.Reconcile` moves from "walk sources, write the first resolvable one" to "build the desired `rkey -> ActorStatus` map across all enabled sources (highest-priority source wins a given rkey), list what's actually live on the PDS, and diff" — the diff itself handles game switches and multi-game publishing with no special-case code. `RecordWriter` gains a `ListStatuses` read (used by both the reconcile diff and the new daily sweep) and its `Put`/`DeleteStatus` methods take an explicit `rkey` instead of a constant. The frontend swaps a single `getRecord` for a `listRecords` call and renders a list instead of one hero.

**Tech Stack:** Go 1.x (`internal/sync`, `cmd/server`), `github.com/bluesky-social/indigo`'s `agnostic`/`comatproto` XRPC packages, TypeScript/Vite frontend (`web/src`), no new dependencies.

**Spec:** [docs/superpowers/specs/2026-08-31-per-game-status-records-design.md](../specs/2026-08-31-per-game-status-records-design.md)

## Global Constraints

- No lexicon change — `games.atmosphere.status`'s `key` is already `"any"`.
- No new DB table — "what's currently live" is read from the PDS (`listRecords`), never duplicated locally, per this repo's "PDS is authoritative" principle.
- Same-game-from-multiple-sources resolution: highest-priority enabled source wins outright (its `createdAt`/`embed`/`via`/`platform` populate the shared record); lower-priority sources reporting the same game are simply skipped.
- `staleBuffer` (`internal/sync/tick.go`) is unchanged — still 15 minutes.
- The daily stale-status sweep is its own 24h ticker in `main.go`, not merged into the existing daily claims sweep.
- Frontend has no unit test tooling (`web/package.json` has no test script) — verify frontend tasks with `pnpm -C web exec tsc --noEmit` plus the existing dev-mock (`?mock=`) fixtures, matching this repo's existing convention (no new test framework).

---

## Task 1: `parseAtURI` helper

Small, pure, and used by both `Reconcile` (Task 2, to derive a status rkey from a game's at-uri) and `ATProtoWriter.ListStatuses` (Task 2, to derive a rkey from a listed record's own uri). Landing it standalone first keeps Task 2 focused on the behavior change.

**Files:**
- Modify: `internal/sync/reconcile.go` (add import, append function)
- Test: `internal/sync/reconcile_test.go` (append test)

**Interfaces:**
- Produces: `parseAtURI(atURI string) (did, collection, rkey string, ok bool)` — package-private, used by Task 2's `Reconcile` and `ATProtoWriter.ListStatuses`.

- [ ] **Step 1: Write the failing test**

Append to `internal/sync/reconcile_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sync/... -run TestParseAtURI -v`
Expected: FAIL with `undefined: parseAtURI`

- [ ] **Step 3: Implement `parseAtURI`**

In `internal/sync/reconcile.go`, add `"strings"` to the import block:

```go
import (
	"context"
	"database/sql"
	"strings"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)
```

Append this function at the end of the file:

```go
// parseAtURI splits an at:// URI into its did/collection/rkey parts. Used
// both to derive a status record's rkey from the game record it links to,
// and by ATProtoWriter.ListStatuses to read the rkey back off a listed
// record's own uri.
func parseAtURI(atURI string) (did, collection, rkey string, ok bool) {
	const prefix = "at://"
	if !strings.HasPrefix(atURI, prefix) {
		return "", "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(atURI, prefix), "/", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sync/... -v`
Expected: PASS — `TestParseAtURI` and every pre-existing test in the package (untouched so far) both pass.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/reconcile.go internal/sync/reconcile_test.go
git commit -m "feat: add at-uri rkey parsing helper to internal/sync"
```

---

## Task 2: Rkey-based reconcile core

The behavior change itself. `RecordWriter`'s interface, `Reconciler.Reconcile`'s algorithm, and `ATProtoWriter`'s implementation all change together — Go requires the whole `internal/sync` package to compile, so these can't land as separate commits. Test files are rewritten first (they won't compile against the old code — that's this task's "red").

**Files:**
- Modify: `internal/sync/tick.go` (`RecordWriter` interface, new `StatusEntry` type)
- Modify: `internal/sync/reconcile.go` (`Reconcile` method body + doc comment)
- Modify: `internal/sync/writer.go` (full rewrite — rkey params, new `ListStatuses`)
- Modify: `internal/sync/status.go` (remove `statusRkey` const)
- Modify: `internal/sync/tick_test.go` (full rewrite — `fakeWriter`/`recordedPut`/new `recordedDelete`, plus the handful of assertions that depended on the old single-record delete semantics)
- Modify: `internal/sync/reconcile_test.go` (replace the 4 old test functions with 6 new ones; keep `TestParseAtURI` from Task 1 as-is)

**Interfaces:**
- Consumes: `parseAtURI` (Task 1).
- Produces: `RecordWriter{PutStatus(ctx, did, rkey string, status ActorStatus) error; DeleteStatus(ctx, did, rkey string) error; ListStatuses(ctx, did string) ([]StatusEntry, error)}`, `StatusEntry{Rkey string; StaleAt time.Time}` — both consumed by Task 3's `RunStatusSweep` and by `main.go` (Task 4).

- [ ] **Step 1: Rewrite the failing tests — `internal/sync/reconcile_test.go`**

Replace the whole file (keep `TestParseAtURI` verbatim from Task 1):

```go
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
```

- [ ] **Step 2: Rewrite the failing tests — `internal/sync/tick_test.go`**

Replace the whole file:

```go
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/sync/... -v`
Expected: FAIL to compile — `fakeWriter` doesn't satisfy `RecordWriter` (wrong `PutStatus`/`DeleteStatus` signatures, no `ListStatuses`), and `writer.puts[0].rkey`/`recordedDelete` don't exist yet on the old types.

- [ ] **Step 4: Update `internal/sync/tick.go`**

Replace the `RecordWriter` interface block (currently just above `CallBudget`):

```go
type RecordWriter interface {
	PutStatus(ctx context.Context, did, rkey string, status ActorStatus) error
	DeleteStatus(ctx context.Context, did, rkey string) error
	ListStatuses(ctx context.Context, did string) ([]StatusEntry, error)
}

// StatusEntry is one games.atmosphere.status record as read back off a PDS —
// just enough to diff against what should currently be live (Reconcile) or
// check for expiry (RunStatusSweep).
type StatusEntry struct {
	Rkey    string
	StaleAt time.Time
}
```

- [ ] **Step 5: Update `internal/sync/reconcile.go`**

Replace the `Reconcile` method (doc comment + body) with:

```go
// Reconcile computes what should be live for did — one status record per
// distinct game currently being played across all enabled sources, keyed by
// that game's own rkey — and diffs it against what's actually live on the
// PDS, writing new/changed entries and deleting anything no longer desired.
//
// Two sources reporting the same game resolve to the same rkey: the
// priority walk below lets only the first (highest-priority) source to
// claim a given rkey populate it, so same-game conflicts resolve exactly
// like the old single-record design did, just scoped per game instead of
// per account. A game switch (rkey A -> rkey B) needs no special case: A
// simply stops appearing in `desired` (so it's deleted) while B appears (so
// it's created).
//
// Call this after any change to any source's session_starts row, after
// enable/disable, after a priority reorder, and after a claim
// invalidation — nothing else should call Writer.PutStatus/DeleteStatus.
func (r *Reconciler) Reconcile(ctx context.Context, did string, now time.Time) error {
	sources, err := appdb.ListEnabledSourcesByPriority(ctx, r.Conn, did)
	if err != nil {
		return err
	}

	desired := map[string]ActorStatus{}
	for _, source := range sources {
		row, err := appdb.GetSessionStart(ctx, r.Conn, did, source)
		if err != nil {
			return err
		}
		if row == nil {
			continue
		}
		game, err := r.Resolver.GetGameBySteamID(ctx, row.GameKey)
		if err != nil {
			return err
		}
		if game == nil {
			continue
		}
		_, _, rkey, ok := parseAtURI(game.URI)
		if !ok {
			continue // malformed game URI — shouldn't happen from cartridge, but never publish garbage
		}
		if _, claimed := desired[rkey]; claimed {
			continue // a higher-priority source already claimed this game
		}
		desired[rkey] = ActorStatus{
			Type: "games.atmosphere.status", Game: game.URI,
			Playing:   map[string]any{},
			Embed:     &Embed{Type: "app.bsky.embed.external", External: EmbedExternal{URI: game.PageURL, Title: game.Name, Description: game.Summary}},
			CreatedAt: row.StartedAt.UTC().Format(time.RFC3339),
			StaleAt:   now.Add(staleBuffer).UTC().Format(time.RFC3339),
			Via:       ViaClientName,
		}
	}

	live, err := r.Writer.ListStatuses(ctx, did)
	if err != nil {
		return err
	}
	for _, entry := range live {
		if _, ok := desired[entry.Rkey]; !ok {
			if err := r.Writer.DeleteStatus(ctx, did, entry.Rkey); err != nil {
				return err
			}
		}
	}
	for rkey, status := range desired {
		if err := r.Writer.PutStatus(ctx, did, rkey, status); err != nil {
			return err
		}
	}
	return nil
}
```

(`UpdateSession`, below `Reconcile` in the same file, is unchanged — leave it as-is.)

- [ ] **Step 6: Update `internal/sync/status.go`**

Remove this line (the rkey is no longer a constant):

```go
const statusRkey = "self"
```

- [ ] **Step 7: Rewrite `internal/sync/writer.go`**

Replace the whole file:

```go
// internal/sync/writer.go
package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bluesky-social/indigo/api/agnostic"
	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/atsession"
	appdb "github.com/jphastings/game-status/internal/db"
)

type ATProtoWriter struct {
	Resumer *atsession.Resumer
	Conn    *sql.DB
}

var _ RecordWriter = (*ATProtoWriter)(nil)

func (w *ATProtoWriter) withClient(ctx context.Context, did string, fn func(*atclient.APIClient) error) error {
	user, err := appdb.GetUser(ctx, w.Conn, did)
	if err != nil {
		return err
	}
	if user == nil || user.ActiveSessionID == "" {
		return fmt.Errorf("no active session for %s", did)
	}
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return err
	}
	return w.Resumer.WithSession(ctx, parsedDID, user.ActiveSessionID, func(sess *oauth.ClientSession) error {
		return fn(sess.APIClient())
	})
}

func (w *ATProtoWriter) PutStatus(ctx context.Context, did, rkey string, status ActorStatus) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	record, err := atdata.UnmarshalJSON(raw)
	if err != nil {
		return fmt.Errorf("validate status record: %w", err)
	}
	return w.withClient(ctx, did, func(client *atclient.APIClient) error {
		_, err := agnostic.RepoPutRecord(ctx, client, &agnostic.RepoPutRecord_Input{
			Collection: StatusCollection, Repo: did, Rkey: rkey, Record: record,
		})
		return err
	})
}

func (w *ATProtoWriter) DeleteStatus(ctx context.Context, did, rkey string) error {
	return w.withClient(ctx, did, func(client *atclient.APIClient) error {
		_, err := comatproto.RepoDeleteRecord(ctx, client, &comatproto.RepoDeleteRecord_Input{
			Collection: StatusCollection, Repo: did, Rkey: rkey,
		})
		if err != nil && isRecordNotFound(err) {
			return nil // idempotent — deleting an already-gone record is success (Global Constraints)
		}
		return err
	})
}

// statusListLimit is comfortably above how many status records one account
// could ever have live at once (bounded by enabled source count).
// ponytail: fixed page, no cursor pagination loop; revisit if that bound changes.
const statusListLimit = 100

// ListStatuses reads every games.atmosphere.status record currently live
// for did straight off their PDS — the source of truth Reconcile diffs
// against, and RunStatusSweep checks for expiry.
func (w *ATProtoWriter) ListStatuses(ctx context.Context, did string) ([]StatusEntry, error) {
	var entries []StatusEntry
	err := w.withClient(ctx, did, func(client *atclient.APIClient) error {
		out, err := agnostic.RepoListRecords(ctx, client, StatusCollection, "", statusListLimit, did, false)
		if err != nil {
			return err
		}
		for _, rec := range out.Records {
			_, _, rkey, ok := parseAtURI(rec.Uri)
			if !ok {
				continue
			}
			var v struct {
				StaleAt string `json:"staleAt"`
			}
			if err := json.Unmarshal(*rec.Value, &v); err != nil {
				return fmt.Errorf("parse status record %s: %w", rec.Uri, err)
			}
			staleAt, err := time.Parse(time.RFC3339, v.StaleAt)
			if err != nil {
				return fmt.Errorf("parse staleAt for %s: %w", rec.Uri, err)
			}
			entries = append(entries, StatusEntry{Rkey: rkey, StaleAt: staleAt})
		}
		return nil
	})
	return entries, err
}

// isRecordNotFound checks indigo's actual XRPC error type. Confirmed by
// reading atclient.APIClient.LexDo (atproto/atclient/lexclient.go): a
// non-2xx response body is decoded as {"error": <name>, "message": <msg>}
// and returned as *atclient.APIError{Name: <name>}, so we match on that
// typed field rather than substring-matching the formatted error text.
//
// Empirically, though, this is a defensive fallback rather than the primary
// idempotency mechanism: com.atproto.repo.deleteRecord's own lexicon says
// "Delete a repository record, or ensure it doesn't exist" and declares no
// RecordNotFound error (only InvalidSwap), and the reference PDS
// implementation (bluesky-social/atproto packages/pds
// src/api/com/atproto/repo/deleteRecord.ts) returns a plain 200 success
// no-op when the record is already gone — it never emits this error at all.
// This check exists in case a non-reference PDS behaves differently.
func isRecordNotFound(err error) bool {
	var apiErr *atclient.APIError
	return errors.As(err, &apiErr) && apiErr.Name == "RecordNotFound"
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `go build ./... && go test ./internal/sync/... -v`
Expected: PASS — every test in `internal/sync` (Task 1's `TestParseAtURI`, the 6 rewritten `Reconcile` tests, all 7 `RunTick` tests) passes, and the whole module still builds.

- [ ] **Step 9: Commit**

```bash
git add internal/sync/tick.go internal/sync/reconcile.go internal/sync/writer.go internal/sync/status.go internal/sync/tick_test.go internal/sync/reconcile_test.go
git commit -m "feat: key status records by game rkey instead of a fixed self rkey

Reconcile now publishes one record per distinct game currently being
played across enabled sources (keyed by that game's own rkey) instead
of a single winner-take-all record, diffing the desired set against
what's actually live on the PDS. A game switch or a source going idle
falls out of the same diff with no special-case code."
```

---

## Task 3: Daily stale-status sweep

Pure safety net, same shape as the existing `internal/claims/sweep.go`: catches any record `Reconcile` should have deleted but didn't (a crash, a missed event).

**Files:**
- Create: `internal/sync/sweep.go`
- Test: `internal/sync/sweep_test.go`

**Interfaces:**
- Consumes: `appdb.ListAllDIDs` (`internal/db/users.go`), `RecordWriter` (Task 2), `fakeWriter`/`recordedDelete`/`StatusEntry` (Task 2's `tick_test.go`).
- Produces: `RunStatusSweep(ctx context.Context, conn *sql.DB, writer RecordWriter, now time.Time) error`, consumed by `main.go` (Task 4).

- [ ] **Step 1: Write the failing test**

Create `internal/sync/sweep_test.go`:

```go
// internal/sync/sweep_test.go
package sync

import (
	"context"
	"testing"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestRunStatusSweep_DeletesOnlyStaleEntries(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.UpsertUser(ctx, conn, "did:plc:b")

	now := time.Now()
	writer := &fakeWriter{live: map[string][]StatusEntry{
		"did:plc:a": {
			{Rkey: "stale-game", StaleAt: now.Add(-time.Hour)},
			{Rkey: "live-game", StaleAt: now.Add(time.Hour)},
		},
		"did:plc:b": {
			{Rkey: "also-stale", StaleAt: now.Add(-time.Minute)},
		},
	}}

	if err := RunStatusSweep(ctx, conn, writer, now); err != nil {
		t.Fatalf("RunStatusSweep: %v", err)
	}

	want := map[recordedDelete]bool{
		{did: "did:plc:a", rkey: "stale-game"}: true,
		{did: "did:plc:b", rkey: "also-stale"}: true,
	}
	if len(writer.deletes) != len(want) {
		t.Fatalf("got deletes=%+v, want exactly %v", writer.deletes, want)
	}
	for _, d := range writer.deletes {
		if !want[d] {
			t.Fatalf("got unexpected delete %+v", d)
		}
	}
	if len(writer.puts) != 0 {
		t.Fatalf("got puts=%+v, want none — the sweep never writes", writer.puts)
	}
}

func TestRunStatusSweep_NoStaleEntries_NoDeletes(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, "did:plc:a")

	now := time.Now()
	writer := &fakeWriter{live: map[string][]StatusEntry{
		"did:plc:a": {{Rkey: "live-game", StaleAt: now.Add(time.Hour)}},
	}}

	if err := RunStatusSweep(ctx, conn, writer, now); err != nil {
		t.Fatalf("RunStatusSweep: %v", err)
	}
	if len(writer.deletes) != 0 {
		t.Fatalf("got deletes=%+v, want none", writer.deletes)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/sync/... -run TestRunStatusSweep -v`
Expected: FAIL with `undefined: RunStatusSweep`

- [ ] **Step 3: Implement `RunStatusSweep`**

Create `internal/sync/sweep.go`:

```go
// internal/sync/sweep.go
package sync

import (
	"context"
	"database/sql"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

// RunStatusSweep is a pure safety net — deletes any games.atmosphere.status
// record whose staleAt has passed, for every signed-in user, regardless of
// current sync state. Reconcile already deletes anything not in its desired
// set on every tick/presence event; this only catches whatever that missed
// (a crash, a process restart mid-tick, a source disabled without ever
// passing through "not playing"). Scoped to every signed-in user rather
// than just those currently syncing, since it exists to catch records that
// outlived their owner's current sync state.
func RunStatusSweep(ctx context.Context, conn *sql.DB, writer RecordWriter, now time.Time) error {
	dids, err := appdb.ListAllDIDs(ctx, conn)
	if err != nil {
		return err
	}
	for _, did := range dids {
		entries, err := writer.ListStatuses(ctx, did)
		if err != nil {
			continue // uncertain outcome (e.g. a network blip) — try again on tomorrow's sweep
		}
		for _, entry := range entries {
			if entry.StaleAt.Before(now) {
				if err := writer.DeleteStatus(ctx, did, entry.Rkey); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sync/... -v`
Expected: PASS — the whole `internal/sync` package, including the two new sweep tests.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/sweep.go internal/sync/sweep_test.go
git commit -m "feat: add daily sweep for stale status records"
```

---

## Task 4: Wire the sweep into `main.go`

**Files:**
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `sync.RunStatusSweep` (Task 3), the existing `writer` (`*sync.ATProtoWriter`) already constructed earlier in `main.go`.

- [ ] **Step 1: Add the ticker**

In `cmd/server/main.go`, immediately after the existing daily claims-sweep goroutine block (the one calling `claims.RunSweep` and `store.DeleteStaleAuthRequests`, right before `jetHandler := func(...)`), add:

```go
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := sync.RunStatusSweep(context.Background(), conn, writer, time.Now()); err != nil {
				slog.Error("daily status sweep", "err", err)
			}
		}
	}()
```

No new imports needed — `sync`, `time`, `slog`, `context`, and `writer` are all already in scope at that point in `main.go`.

- [ ] **Step 2: Verify it builds**

Run: `go build ./...`
Expected: builds cleanly. (`cmd/server` has no test file today — this is glue code covered by `RunStatusSweep`'s own unit tests from Task 3, matching the rest of `main.go`'s untested-glue convention.)

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: run the daily status sweep on its own 24h ticker"
```

---

## Task 5: Frontend — `resolveLiveStatuses`

**Files:**
- Modify: `web/src/atproto.ts`

**Interfaces:**
- Produces: `resolveLiveStatuses(did: string): Promise<LiveStatus[] | 'error'>` (replaces `resolveLiveStatus`), consumed by Task 6's `main.ts`/`devmock.ts`.
- Consumes: existing `resolvePDS`, `getRecord`, `blobURL`, `parseAtUri`, `LiveStatus`/`StatusRecord`/`GameRecord` types (all already in this file, unchanged).

- [ ] **Step 1: Replace `resolveLiveStatus`**

In `web/src/atproto.ts`, remove the existing `resolveLiveStatus` function (the one reading `getRecord(did, 'games.atmosphere.status', 'self')`) and replace it with:

```ts
/** Reads every live (non-stale) status the signed-in user has published across all their sources, resolving cover art from each linked game record. Returns 'error' if their PDS couldn't be reached. */
export async function resolveLiveStatuses(did: string): Promise<LiveStatus[] | 'error'> {
  const pds = await resolvePDS(did)
  if (!pds) return 'error'

  let records: { uri: string; cid: string; value: StatusRecord }[]
  try {
    const url = `${pds}/xrpc/com.atproto.repo.listRecords?${new URLSearchParams({ repo: did, collection: 'games.atmosphere.status' })}`
    const res = await fetch(url)
    if (!res.ok) return 'error'
    const body = await res.json()
    records = body.records ?? []
  } catch {
    return 'error'
  }

  const now = Date.now()
  const live = records.filter((r) => new Date(r.value.staleAt).getTime() > now)
  return Promise.all(live.map((r) => toLiveStatus(r.value)))
}

async function toLiveStatus(value: StatusRecord): Promise<LiveStatus> {
  const { game, embed, createdAt, staleAt } = value
  const base: LiveStatus = {
    title: embed?.external.title ?? game,
    description: embed?.external.description ?? '',
    pageURL: embed?.external.uri ?? game,
    createdAt,
    staleAt,
  }

  try {
    const { did: gameDID, rkey } = parseAtUri(game)
    const gameRec = await getRecord<GameRecord>(gameDID, 'games.gamesgamesgamesgames.game', rkey)
    if (gameRec.kind === 'found') {
      const cover = gameRec.value.media?.find((m) => m.mediaType === 'cover')
      const pds = await resolvePDS(gameDID)
      if (cover && pds) base.coverURL = blobURL(pds, gameDID, cover.blob.ref.$link)
    }
  } catch {
    // Cover art is a bonus, not a requirement — the text status stands on its own.
  }

  return base
}
```

The `StatusRecord`, `GameRecord`, and `LiveStatus` type declarations already in the file are unchanged.

- [ ] **Step 2: Type-check**

Run: `pnpm -C web exec tsc --noEmit`
Expected: no errors. (Task 6 still imports the old `resolveLiveStatus` name, so this will show one error in `main.ts` until Task 6 lands — that's expected here.)

- [ ] **Step 3: Commit**

```bash
git add web/src/atproto.ts
git commit -m "feat: read every live status via listRecords instead of a single self record"
```

---

## Task 6: Frontend — render every live status

**Files:**
- Modify: `web/src/main.ts`
- Modify: `web/src/devmock.ts`
- Modify: `web/src/style.css`

**Interfaces:**
- Consumes: `resolveLiveStatuses` (Task 5), `LiveStatus` (unchanged shape, `web/src/atproto.ts`).

- [ ] **Step 1: Update `web/src/main.ts`**

Line 2, change the import:

```ts
import { resolveLiveStatuses, resolveHandle, type LiveStatus } from './atproto'
```

Line 4, change the import:

```ts
import { mockMe, mockLiveStatuses } from './devmock'
```

Replace `currentLiveStatus`:

```ts
async function currentLiveStatuses(did: string): Promise<LiveStatus[] | 'error'> {
  if (import.meta.env.DEV) {
    const mocked = mockLiveStatuses()
    if (mocked !== undefined) return mocked
  }
  return resolveLiveStatuses(did)
}
```

In `renderSignedIn`, replace the single-hero markup with a `hero-list` wrapper (this is the only change in this function — the rest of the template, and everything below it, is unchanged):

```ts
function renderSignedIn(me: Me) {
  app.innerHTML = `
    <div class="panel-screen"><div class="panel">
      <div class="hero-list" id="hero-list">
        <section class="hero hero--loading" aria-live="polite">
          <div class="hero-cover hero-cover--loading"></div>
          <div class="hero-body">
            <p class="hero-eyebrow"><span class="live-dot"></span> Checking your PDS…</p>
          </div>
        </section>
      </div>

      <section class="consent-zone"><div id="sources">${sourcesHTML(me)}</div></section>

      <footer class="utility-row">
        <span class="did-tag" id="identity-tag">${me.did}</span>
        <button class="btn btn-ghost" id="signout" type="button">Sign out</button>
      </footer>
    </div></div>
  `

  attachSourcesListeners()
  document.getElementById('signout')!.addEventListener('click', () => {
    window.location.href = '/logout'
  })

  loadLiveStatus(me.did)
  watchOwnStatus(me.did, () => loadLiveStatus(me.did))
  loadHandle(me.did)
}
```

Replace `loadLiveStatus` and `renderHero`:

```ts
async function loadLiveStatus(did: string) {
  const list = document.getElementById('hero-list')
  if (!list) return // user navigated away from the signed-in screen since this was kicked off
  const statuses = await currentLiveStatuses(did)
  list.outerHTML = renderHeroList(statuses)
}

async function loadHandle(did: string) {
  const handle = await resolveHandle(did)
  const tag = document.getElementById('identity-tag')
  if (!handle || !tag) return // resolution failed, or the panel's moved on — the DID is a fine fallback
  tag.textContent = `@${handle}`
}

function renderHeroList(statuses: LiveStatus[] | 'error'): string {
  if (statuses === 'error') {
    return `<div class="hero-list" id="hero-list"><section class="hero hero--error" aria-live="polite">Couldn't reach your PDS to check status.</section></div>`
  }
  if (statuses.length === 0) {
    return `
      <div class="hero-list" id="hero-list">
        <section class="hero hero--empty" aria-live="polite">
          <div class="hero-body">
            <p class="hero-eyebrow"><span class="live-dot"></span> Idle</p>
            <p class="hero-meta">Not currently playing a game.</p>
          </div>
        </section>
      </div>
    `
  }
  return `<div class="hero-list" id="hero-list">${statuses.map(renderHero).join('')}</div>`
}

function renderHero(status: LiveStatus): string {
  const cover = status.coverURL
    ? `<img class="hero-cover" src="${status.coverURL}" alt="${escapeHTML(status.title)} cover art" loading="lazy" onerror="this.remove()" />`
    : ''
  return `
    <section class="hero" aria-live="polite">
      ${cover}
      <div class="hero-body">
        <p class="hero-eyebrow"><span class="live-dot"></span> Live</p>
        <h2 class="hero-title">${escapeHTML(status.title)}</h2>
        <p class="hero-meta">since ${timeAgo(status.createdAt)}</p>
      </div>
    </section>
  `
}
```

Everything else in `main.ts` (`sourcesHTML`, drag-reorder, `timeAgo`, `escapeHTML`, etc.) is unchanged.

- [ ] **Step 2: Update `web/src/devmock.ts`**

Replace the whole file:

```ts
// Dev-only fixtures for visually QA-ing every state without a real OAuth
// session. Driven by a `?mock=` query param; entirely gated behind
// `import.meta.env.DEV` at the call site, so Vite's production build
// tree-shakes this whole module out.
import type { Me } from './api'
import type { LiveStatus } from './atproto'

const FIXTURES: Record<string, { me: Me | null; live: LiveStatus[] | 'error' }> = {
  'signed-out': { me: null, live: [] },
  'no-claim': {
    me: {
      did: 'did:plc:mockuser', steamEnabled: false, discordEnabled: false, sourceOrder: ['steam', 'discord'],
      discordInviteUrl: 'https://discord.gg/example',
    },
    live: [],
  },
  idle: {
    me: {
      did: 'did:plc:mockuser', steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true,
      discordEnabled: false, sourceOrder: ['steam', 'discord'], discordInviteUrl: 'https://discord.gg/example',
    },
    live: [],
  },
  playing: {
    me: {
      did: 'did:plc:mockuser', steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true,
      discordEnabled: false, sourceOrder: ['steam', 'discord'], discordInviteUrl: 'https://discord.gg/example',
    },
    live: [{
      title: 'Slay the Spire II',
      description: 'The iconic roguelike deckbuilder returns!',
      pageURL: 'https://cartridge.dev/game/slay-the-spire-ii',
      createdAt: new Date(Date.now() - 47 * 60 * 1000).toISOString(),
      staleAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      coverURL: 'https://pds.gamesgamesgamesgames.games/xrpc/com.atproto.sync.getBlob?did=did:web:gamesgamesgamesgames.games&cid=bafkreiafbwc3a3y47qnaguvov6rp4dhispbqeyglfxi5q37nb7lse34h7m',
    }],
  },
  error: {
    me: {
      did: 'did:plc:mockuser', steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true,
      discordEnabled: false, sourceOrder: ['steam', 'discord'], discordInviteUrl: 'https://discord.gg/example',
    },
    live: 'error',
  },
  'both-sources': {
    me: {
      did: 'did:plc:mockuser',
      steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true,
      discordSubject: '690973862245957683', discordDisplayName: 'byjp', discordEnabled: true,
      sourceOrder: ['discord', 'steam'], discordInviteUrl: 'https://discord.gg/example',
    },
    live: [{
      title: 'Slay the Spire II',
      description: 'The iconic roguelike deckbuilder returns!',
      pageURL: 'https://cartridge.dev/game/slay-the-spire-ii',
      createdAt: new Date(Date.now() - 47 * 60 * 1000).toISOString(),
      staleAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      coverURL: 'https://pds.gamesgamesgamesgames.games/xrpc/com.atproto.sync.getBlob?did=did:web:gamesgamesgamesgames.games&cid=bafkreiafbwc3a3y47qnaguvov6rp4dhispbqeyglfxi5q37nb7lse34h7m',
    }],
  },
  'multi-game': {
    me: {
      did: 'did:plc:mockuser',
      steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true,
      discordSubject: '690973862245957683', discordDisplayName: 'byjp', discordEnabled: true,
      sourceOrder: ['steam', 'discord'], discordInviteUrl: 'https://discord.gg/example',
    },
    live: [
      {
        title: 'Slay the Spire II',
        description: 'The iconic roguelike deckbuilder returns!',
        pageURL: 'https://cartridge.dev/game/slay-the-spire-ii',
        createdAt: new Date(Date.now() - 47 * 60 * 1000).toISOString(),
        staleAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
        coverURL: 'https://pds.gamesgamesgamesgames.games/xrpc/com.atproto.sync.getBlob?did=did:web:gamesgamesgamesgames.games&cid=bafkreiafbwc3a3y47qnaguvov6rp4dhispbqeyglfxi5q37nb7lse34h7m',
      },
      {
        title: 'Dota 2',
        description: 'A game of unmatched depth and strategic complexity.',
        pageURL: 'https://cartridge.dev/game/dota-2',
        createdAt: new Date(Date.now() - 12 * 60 * 1000).toISOString(),
        staleAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      },
    ],
  },
}

function key(): string | null {
  return new URLSearchParams(window.location.search).get('mock')
}

export function mockMe(): Me | null | undefined {
  const k = key()
  if (!k) return undefined
  return FIXTURES[k]?.me ?? null
}

export function mockLiveStatuses(): LiveStatus[] | 'error' | undefined {
  const k = key()
  if (!k) return undefined
  return FIXTURES[k]?.live ?? []
}
```

- [ ] **Step 3: Update `web/src/style.css`**

Immediately before the `.hero {` rule (under the `/* ---------- Status hero ---------- */` comment), add:

```css
.hero-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-lg);
}
```

- [ ] **Step 4: Type-check**

Run: `pnpm -C web exec tsc --noEmit`
Expected: no errors.

- [ ] **Step 5: Manual verification**

Run: `pnpm -C web dev`, then visit `http://localhost:5173/?mock=multi-game` — expect two stacked hero cards (Slay the Spire II with cover art, Dota 2 without). Also spot-check `?mock=playing` (one card), `?mock=idle` (empty state), and `?mock=error` (error state) still render correctly.

- [ ] **Step 6: Commit**

```bash
git add web/src/main.ts web/src/devmock.ts web/src/style.css
git commit -m "feat: render every live status as a stacked list of hero cards"
```

---

## Self-Review Notes

- **Spec coverage:** rkey-from-game (Task 2), same-game priority resolution (Task 2's `TestReconcile_SameGame_HighestPrioritySourceWins`), different-games-both-published (Task 2's `TestReconcile_DifferentGames_BothPublished`), game-switch diff (Task 2's `TestReconcile_GameSwitch_DeletesOldPutsNew`), `ListStatuses`/writer interface (Task 2), daily sweep (Task 3 + Task 4), frontend `listRecords` + multi-card render (Task 5 + Task 6) — every spec section has a task.
- **Placeholder scan:** none — every step shows complete, real code; no TODOs.
- **Type consistency:** `RecordWriter{PutStatus, DeleteStatus, ListStatuses}` and `StatusEntry{Rkey, StaleAt}` (Task 2) are used identically by `Reconciler.Reconcile` (Task 2), `RunStatusSweep` (Task 3), and `fakeWriter` (Task 2, reused by Task 3) — checked signatures match across every task. `recordedPut{did, rkey, status}`/`recordedDelete{did, rkey}` are consistent between their Task 2 definition and every test that constructs them (Tasks 2 and 3). Frontend: `LiveStatus[] | 'error'` is the consistent return/param shape across `resolveLiveStatuses` (Task 5), `mockLiveStatuses` (Task 6), `currentLiveStatuses`/`renderHeroList` (Task 6).
