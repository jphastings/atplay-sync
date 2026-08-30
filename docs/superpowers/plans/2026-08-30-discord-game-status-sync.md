# Discord game-status sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Discord as a second "now playing" source alongside Steam, sharing one PDS status record via a priority-ordered reconciler, linked through a keytrace `discord` claim whose Discord snowflake ID is resolved (not trusted verbatim) against a bot's live guild-member list.

**Architecture:** Generalize the existing Steam-only `steam_claims`/`sync_prefs.steam_enabled` tables into source-generic `claims`/`sync_prefs` tables; introduce a `Reconciler` that both Steam's poll tick and a new event-driven Discord Gateway connection go through instead of writing the PDS record directly; add a `internal/discord` package (bwmarrin/discordgo Gateway session, member cache, detectable-games → Steam-App-ID index) mirroring the shape of the existing `internal/steam`/`internal/jetstream` packages.

**Tech Stack:** Go 1.26, `modernc.org/sqlite`, `bluesky-social/indigo` (unchanged), new dependency `github.com/bwmarrin/discordgo` for the Discord Gateway connection (rationale: hand-rolling heartbeat/resume/sequence-tracking is real protocol risk, same class of decision that justified using `indigo` for OAuth rather than hand-rolling it).

**Spec:** [docs/superpowers/specs/2026-08-30-discord-game-status-sync-design.md](../specs/2026-08-30-discord-game-status-sync-design.md)

## Global Constraints

- No fuzzy title matching in v1 — an unresolvable game (no Discord→Steam App ID cross-reference) is skipped, exactly like Steam already does today.
- `claims.subject` for a `discord` row is always a **resolved, confirmed snowflake ID** — never the raw signed username, never the claim's unsigned `profileUrl` taken on faith. A row only exists once resolution has actually succeeded.
- Once `Reconciler` exists (Task 3 onward), **nothing calls `RecordWriter.PutStatus`/`DeleteStatus` directly except `Reconciler.Reconcile` itself.** Every other call site that used to delete/write the PDS record directly (tick, `InvalidateClaim`, the manual disable toggle) calls `Reconcile` instead, so one source's change never clobbers another source's still-valid state.
- Discord's tracking guild ID is a single fixed value from config (`DISCORD_GUILD_ID`) — every Gateway event handler ignores events for any other guild ID as a first check.
- Default source priority is `steam` then `discord` (lower `sync_prefs.priority` number = higher priority), set only the first time a source's `sync_prefs` row is created; an existing row's priority is only ever changed by an explicit reorder.

---

## Task 1: Migration runner + schema generalization

**Files:**
- Create: `internal/db/migrate.go`
- Create: `internal/db/migrations/0002_generalize_sources.sql`
- Modify: `internal/db/db.go`
- Modify: `internal/db/db_test.go`
- Test: `internal/db/migrate_test.go`

**Interfaces:**
- Produces: `applyMigrations(conn *sql.DB, migrations fs.FS) error` — runs every `migrations/*.sql` file not yet recorded in a new `schema_migrations` table, in filename order, each in its own transaction. Task 2 onward assumes `Open` already ran this.

The database has real user data behind it today (verified live during design — the project owner's own Steam claim). `0001_init.sql` is treated as immutable from here on; `0002` migrates existing rows rather than replacing the file in place.

- [ ] **Step 1: Write the migration-runner test**

```go
// internal/db/migrate_test.go
package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpen_RecordsAppliedMigrationsAndIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Open(dbPath); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	conn, err := Open(dbPath) // second Open must not error re-applying anything
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer conn.Close()

	rows, err := conn.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, v)
	}
	want := []string{"0001_init.sql", "0002_generalize_sources.sql"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("schema_migrations = %v, want %v", got, want)
	}
}

func TestOpen_MigratesExistingSteamClaimsAndSyncPrefs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Simulate a database that only ever had 0001_init.sql applied
	// (pre-migration-runner), with real user data in the old Steam-only shape.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	schema, err := os.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("read 0001: %v", err)
	}
	if _, err := raw.Exec(string(schema)); err != nil {
		t.Fatalf("apply 0001: %v", err)
	}
	seed := []string{
		`INSERT INTO users (did, created_at) VALUES ('did:plc:old', '2026-01-01T00:00:00Z')`,
		`INSERT INTO steam_claims (did, subject, display_name, claim_uri, record_uri, last_verified_at)
		 VALUES ('did:plc:old', '765', 'Old Player', 'https://x', 'at://x', '2026-01-01T00:00:00Z')`,
		`INSERT INTO sync_prefs (did, steam_enabled) VALUES ('did:plc:old', 1)`,
	}
	for _, stmt := range seed {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	raw.Close()

	conn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	var subject, displayName string
	err = conn.QueryRow(`SELECT subject, display_name FROM claims WHERE did = ? AND claim_type = 'steam'`, "did:plc:old").
		Scan(&subject, &displayName)
	if err != nil {
		t.Fatalf("query migrated claim: %v", err)
	}
	if subject != "765" || displayName != "Old Player" {
		t.Fatalf("got subject=%q displayName=%q, want 765/Old Player", subject, displayName)
	}

	var enabled, priority int
	err = conn.QueryRow(`SELECT enabled, priority FROM sync_prefs WHERE did = ? AND source = 'steam'`, "did:plc:old").
		Scan(&enabled, &priority)
	if err != nil {
		t.Fatalf("query migrated sync_prefs: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("got enabled=%d, want 1", enabled)
	}

	for _, table := range []string{"steam_claims_old", "sync_prefs_old", "steam_claims"} {
		var name string
		err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != sql.ErrNoRows {
			t.Fatalf("table %s: got err=%v, want sql.ErrNoRows (should be gone)", table, err)
		}
	}
	_ = context.Background
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/db/... -run TestOpen_RecordsAppliedMigrationsAndIsIdempotent -v` and the second new test.
Expected: FAIL — `schema_migrations` table doesn't exist yet, and `claims`/`sync_prefs.priority` don't exist yet.

- [ ] **Step 3: Write the migration runner**

```go
// internal/db/migrate.go
package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
)

// applyMigrations runs every migrations/*.sql file not yet recorded in
// schema_migrations, in filename order, each in its own transaction.
// 0001_init.sql's CREATE TABLE IF NOT EXISTS statements are safe to
// re-apply against a database that predates this runner — recording it
// afterward just bootstraps schema_migrations for such a database.
func applyMigrations(conn *sql.DB, migrations fs.FS) error {
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		var applied string
		err := conn.QueryRow(`SELECT version FROM schema_migrations WHERE version = ?`, name).Scan(&applied)
		if err == nil {
			continue // already applied
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check migration %s: %w", name, err)
		}

		sqlBytes, err := fs.ReadFile(migrations, "migrations/"+name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := conn.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Write the generalization migration**

```sql
-- internal/db/migrations/0002_generalize_sources.sql
ALTER TABLE steam_claims RENAME TO steam_claims_old;
ALTER TABLE sync_prefs RENAME TO sync_prefs_old;

CREATE TABLE claims (
  did              TEXT NOT NULL REFERENCES users(did),
  claim_type       TEXT NOT NULL,   -- "steam" | "discord"
  subject          TEXT NOT NULL,   -- SteamID64, or a resolved Discord snowflake
  display_name     TEXT NOT NULL,
  claim_uri        TEXT NOT NULL,
  record_uri       TEXT NOT NULL,
  last_verified_at TEXT NOT NULL,
  PRIMARY KEY (did, claim_type)
);

CREATE TABLE sync_prefs (
  did      TEXT NOT NULL REFERENCES users(did),
  source   TEXT NOT NULL,    -- "steam" | "discord"
  enabled  INTEGER NOT NULL DEFAULT 0,
  priority INTEGER NOT NULL, -- lower = higher priority among ENABLED rows
  PRIMARY KEY (did, source)
);

INSERT INTO claims (did, claim_type, subject, display_name, claim_uri, record_uri, last_verified_at)
  SELECT did, 'steam', subject, display_name, claim_uri, record_uri, last_verified_at FROM steam_claims_old;

INSERT INTO sync_prefs (did, source, enabled, priority)
  SELECT did, 'steam', steam_enabled, 0 FROM sync_prefs_old;

DROP TABLE steam_claims_old;
DROP TABLE sync_prefs_old;
```

- [ ] **Step 5: Wire the runner into `Open`, update the table-list test**

```go
// internal/db/db.go — replace the body of Open from the schema-file read onward
func Open(path string) (*sql.DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := applyMigrations(conn, migrationsFS); err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return conn, nil
}
```

In `internal/db/db_test.go`, change `TestOpen_AppliesSchema`'s `tables` slice: replace `"steam_claims"` with `"claims"` (leave `"sync_prefs"` — same name, new shape).

- [ ] **Step 6: Run tests, verify green**

Run: `go test ./internal/db/... -v`
Expected: PASS, including both new tests and the updated `TestOpen_AppliesSchema`.

- [ ] **Step 7: Commit**

```bash
git add internal/db/migrate.go internal/db/migrate_test.go internal/db/migrations/0002_generalize_sources.sql internal/db/db.go internal/db/db_test.go
git commit -m "db: add migration runner, generalize claims/sync_prefs schema"
```

---

## Task 2: Generalize the `db` package API and rewire every Steam call site

**Files:**
- Create: `internal/db/claims.go` (replaces `steam_claims.go`)
- Delete: `internal/db/steam_claims.go`
- Modify: `internal/db/sync_prefs.go`
- Modify: `internal/db/invalidate.go`
- Modify: `internal/claims/discover.go`, `internal/claims/sweep.go`
- Modify: `internal/jetstream/handler.go`, `internal/jetstream/dbstore.go`
- Modify: `internal/api/steam_handlers.go`
- Modify: `internal/sync/tick.go`
- Test: `internal/db/claims_test.go` (replaces `steam_claims_test.go`), `internal/db/sync_prefs_test.go`, and existing tests in the files above that reference the old names

**Interfaces:**
- Produces: `db.Claim{DID, Type, Subject, DisplayName, ClaimURI, RecordURI, LastVerifiedAt}`, `db.UpsertClaim`, `db.GetClaim(ctx, conn, did, claimType)`, `db.DeleteClaim(ctx, conn, did, claimType)`, `db.SetEnabled(ctx, conn, did, source, enabled)`, `db.ListEnabledDIDs(ctx, conn, source)`, `db.SteamSource = "steam"`, `db.DiscordSource = "discord"`.
- Behavior-preserving: Steam sync must work identically after this task — every test that passed before still passes, just against the new names. No Discord-specific behavior yet.

- [ ] **Step 1: Update existing tests to the new API (they won't compile yet)**

Replace `internal/db/steam_claims_test.go` with `internal/db/claims_test.go`:

```go
// internal/db/claims_test.go
package db

import (
	"context"
	"testing"
	"time"
)

func TestUpsertClaim_RoundTrip(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	now := time.Now().UTC()
	claim := Claim{
		DID: "did:plc:test", Type: SteamSource, Subject: "76561198000000000",
		DisplayName: "TestPlayer", ClaimURI: "https://steamcommunity.com/profiles/76561198000000000",
		RecordURI: "at://did:plc:test/dev.keytrace.claim/xyz", LastVerifiedAt: now,
	}

	if err := UpsertClaim(ctx, conn, claim); err != nil {
		t.Fatalf("UpsertClaim: %v", err)
	}

	c, err := GetClaim(ctx, conn, "did:plc:test", SteamSource)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if c == nil {
		t.Fatalf("got nil, want Claim")
	}
	if c.DID != claim.DID || c.Type != claim.Type || c.Subject != claim.Subject || c.DisplayName != claim.DisplayName ||
		c.ClaimURI != claim.ClaimURI || c.RecordURI != claim.RecordURI {
		t.Fatalf("got %+v, want %+v", c, claim)
	}
	if diff := now.Sub(c.LastVerifiedAt); diff > time.Second || diff < -time.Second {
		t.Fatalf("got LastVerifiedAt %v, want %v (diff: %v)", c.LastVerifiedAt, now, diff)
	}
}

func TestGetClaim_MissingReturnsNil(t *testing.T) {
	c, err := GetClaim(context.Background(), openTestDB(t), "did:plc:nobody", SteamSource)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	if c != nil {
		t.Fatalf("got %+v, want nil", c)
	}
}

func TestGetClaim_DifferentTypeSameDID_Independent(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	now := time.Now()
	if err := UpsertClaim(ctx, conn, Claim{DID: "did:plc:test", Type: SteamSource, Subject: "steam-id", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: now}); err != nil {
		t.Fatalf("UpsertClaim steam: %v", err)
	}
	if err := UpsertClaim(ctx, conn, Claim{DID: "did:plc:test", Type: DiscordSource, Subject: "discord-id", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: now}); err != nil {
		t.Fatalf("UpsertClaim discord: %v", err)
	}

	steam, err := GetClaim(ctx, conn, "did:plc:test", SteamSource)
	if err != nil || steam == nil || steam.Subject != "steam-id" {
		t.Fatalf("got steam claim %+v, %v", steam, err)
	}
	discord, err := GetClaim(ctx, conn, "did:plc:test", DiscordSource)
	if err != nil || discord == nil || discord.Subject != "discord-id" {
		t.Fatalf("got discord claim %+v, %v", discord, err)
	}
}

func TestDeleteClaim_RemovesData(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	claim := Claim{DID: "did:plc:test", Type: SteamSource, Subject: "x", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()}
	if err := UpsertClaim(ctx, conn, claim); err != nil {
		t.Fatalf("UpsertClaim: %v", err)
	}
	if err := DeleteClaim(ctx, conn, "did:plc:test", SteamSource); err != nil {
		t.Fatalf("DeleteClaim: %v", err)
	}
	c, err := GetClaim(ctx, conn, "did:plc:test", SteamSource)
	if err != nil || c != nil {
		t.Fatalf("got %+v, %v after delete, want nil, nil", c, err)
	}
}
```

Update `internal/db/sync_prefs_test.go` similarly: `SetSteamEnabled(ctx, conn, did, true)` → `SetEnabled(ctx, conn, did, SteamSource, true)`; add a case asserting a fresh row's default priority (`0` for `SteamSource`, `1` for `DiscordSource`) and that an existing row's priority survives a later `SetEnabled` toggle.

Update every other reference across the codebase from old names to new (mechanical — same rename in each file):

| Old | New |
|---|---|
| `appdb.SteamClaim{DID, Subject, DisplayName, ClaimURI, RecordURI, LastVerifiedAt}` | `appdb.Claim{DID, Type: appdb.SteamSource, Subject, DisplayName, ClaimURI, RecordURI, LastVerifiedAt}` |
| `appdb.UpsertSteamClaim(ctx, conn, c)` | `appdb.UpsertClaim(ctx, conn, c)` |
| `appdb.GetSteamClaim(ctx, conn, did)` | `appdb.GetClaim(ctx, conn, did, appdb.SteamSource)` |
| `appdb.InvalidateSteamClaim(ctx, conn, did)` | `appdb.DeleteClaim(ctx, conn, did, appdb.SteamSource)` |
| `appdb.SetSteamEnabled(ctx, conn, did, enabled)` | `appdb.SetEnabled(ctx, conn, did, appdb.SteamSource, enabled)` |
| `appdb.IsSteamEnabled(ctx, conn, did)` | `appdb.IsEnabled(ctx, conn, did, appdb.SteamSource)` |
| `appdb.ListSteamEnabledDIDs(ctx, conn)` | `appdb.ListEnabledDIDs(ctx, conn, appdb.SteamSource)` |

Apply this table in: `internal/claims/discover.go`, `internal/claims/discover_test.go`, `internal/claims/sweep.go`, `internal/claims/sweep_test.go`, `internal/jetstream/handler.go`, `internal/jetstream/handler_test.go`, `internal/jetstream/dbstore.go`, `internal/api/steam_handlers.go`, `internal/api/steam_handlers_test.go`, `internal/api/me_handler.go`, `internal/sync/tick.go`, `internal/sync/tick_test.go`.

In `internal/jetstream/handler.go`'s `Store` interface, rename `GetSteamClaim`/`UpsertSteamClaim` to `GetClaim(ctx, did, claimType string)`/`UpsertClaim(ctx, c appdb.Claim)`, and update `HandleEvent` to call `store.GetClaim(ctx, ev.DID, "steam")` (still Steam-only filtering here — Task 5 generalizes this further). `internal/jetstream/dbstore.go`'s `DBStore` methods become:

```go
func (s DBStore) GetClaim(ctx context.Context, did, claimType string) (*appdb.Claim, error) {
	return appdb.GetClaim(ctx, s.Conn, did, claimType)
}
func (s DBStore) UpsertClaim(ctx context.Context, c appdb.Claim) error {
	return appdb.UpsertClaim(ctx, s.Conn, c)
}
```

- [ ] **Step 2: Run to verify compile/test failures**

Run: `go build ./... && go test ./...`
Expected: FAIL to build — old function/type names no longer exist anywhere except the not-yet-rewritten `internal/db` implementation files.

- [ ] **Step 3: Rewrite `internal/db/claims.go`, `internal/db/sync_prefs.go`, `internal/db/invalidate.go`**

```go
// internal/db/claims.go
package db

import (
	"context"
	"database/sql"
	"time"
)

const SteamSource = "steam"
const DiscordSource = "discord"

type Claim struct {
	DID            string
	Type           string
	Subject        string
	DisplayName    string
	ClaimURI       string
	RecordURI      string
	LastVerifiedAt time.Time
}

func UpsertClaim(ctx context.Context, conn *sql.DB, c Claim) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO claims (did, claim_type, subject, display_name, claim_uri, record_uri, last_verified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(did, claim_type) DO UPDATE SET
			subject = excluded.subject, display_name = excluded.display_name,
			claim_uri = excluded.claim_uri, record_uri = excluded.record_uri,
			last_verified_at = excluded.last_verified_at
	`, c.DID, c.Type, c.Subject, c.DisplayName, c.ClaimURI, c.RecordURI, c.LastVerifiedAt.UTC().Format(time.RFC3339))
	return err
}

func GetClaim(ctx context.Context, conn *sql.DB, did, claimType string) (*Claim, error) {
	c := Claim{DID: did, Type: claimType}
	var lastVerifiedAt string
	err := conn.QueryRowContext(ctx, `SELECT subject, display_name, claim_uri, record_uri, last_verified_at FROM claims WHERE did = ? AND claim_type = ?`, did, claimType).
		Scan(&c.Subject, &c.DisplayName, &c.ClaimURI, &c.RecordURI, &lastVerifiedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.LastVerifiedAt, err = time.Parse(time.RFC3339, lastVerifiedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// DeleteClaim removes a revoked/retracted/deleted claim of one type. It does
// NOT touch sync_prefs.enabled for that source — that's user intent, kept
// separate on purpose (Global Constraints) so the UI can say "enabled, but
// not valid" instead of silently flipping the toggle.
func DeleteClaim(ctx context.Context, conn *sql.DB, did, claimType string) error {
	_, err := conn.ExecContext(ctx, `DELETE FROM claims WHERE did = ? AND claim_type = ?`, did, claimType)
	return err
}
```

```go
// internal/db/sync_prefs.go
package db

import (
	"context"
	"database/sql"
)

// defaultPriority only applies the first time a (did, source) row is
// created — an existing row's priority is only ever changed by an explicit
// reorder (SetSourceOrder).
var defaultPriority = map[string]int{SteamSource: 0, DiscordSource: 1}

func SetEnabled(ctx context.Context, conn *sql.DB, did, source string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := conn.ExecContext(ctx, `
		INSERT INTO sync_prefs (did, source, enabled, priority) VALUES (?, ?, ?, ?)
		ON CONFLICT(did, source) DO UPDATE SET enabled = excluded.enabled
	`, did, source, e, defaultPriority[source])
	return err
}

func IsEnabled(ctx context.Context, conn *sql.DB, did, source string) (bool, error) {
	var enabled int
	err := conn.QueryRowContext(ctx, `SELECT enabled FROM sync_prefs WHERE did = ? AND source = ?`, did, source).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled == 1, nil
}

// ListEnabledDIDs returns DIDs eligible to sync a given source right now:
// user intent (sync_prefs) AND claim validity (claims) both hold.
func ListEnabledDIDs(ctx context.Context, conn *sql.DB, source string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT sp.did FROM sync_prefs sp
		JOIN claims c ON c.did = sp.did AND c.claim_type = sp.source
		WHERE sp.source = ? AND sp.enabled = 1
	`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dids []string
	for rows.Next() {
		var did string
		if err := rows.Scan(&did); err != nil {
			return nil, err
		}
		dids = append(dids, did)
	}
	return dids, rows.Err()
}

// SetSourceOrder persists a drag-to-reorder priority: order[0] is
// highest-priority. Only touches rows already present in sync_prefs (a
// source that's never been enabled has nothing to reorder).
func SetSourceOrder(ctx context.Context, conn *sql.DB, did string, order []string) error {
	for i, source := range order {
		if _, err := conn.ExecContext(ctx, `UPDATE sync_prefs SET priority = ? WHERE did = ? AND source = ?`, i, did, source); err != nil {
			return err
		}
	}
	return nil
}

// ListEnabledSourcesByPriority returns this user's enabled sync sources,
// highest-priority first — the order Reconcile (internal/sync) walks.
func ListEnabledSourcesByPriority(ctx context.Context, conn *sql.DB, did string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT source FROM sync_prefs WHERE did = ? AND enabled = 1 ORDER BY priority ASC`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}
```

`internal/db/invalidate.go`: keep the signature and behavior exactly as today (still takes `StatusDeleter`, still always deletes), just rename the internal calls:

```go
func InvalidateClaim(ctx context.Context, conn *sql.DB, deleter StatusDeleter, did, claimType string) error {
	if err := DeleteClaim(ctx, conn, did, claimType); err != nil {
		return err
	}
	if err := SetEnabled(ctx, conn, did, claimType, false); err != nil {
		return err
	}
	if err := ClearSessionStart(ctx, conn, did, claimType); err != nil {
		return err
	}
	return deleter.DeleteStatus(ctx, did)
}
```

(Task 3 changes this further — this step only renames, it does not change behavior.)

- [ ] **Step 4: Delete the old file, run the full suite**

```bash
rm internal/db/steam_claims.go internal/db/steam_claims_test.go
go build ./... && go test ./... -v
```

Expected: PASS everywhere.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "db: generalize claims/sync_prefs API across all Steam call sites"
```

---

## Task 3: Reconciler — one PDS write path for every source

**Files:**
- Create: `internal/sync/reconcile.go`
- Create: `internal/sync/reconcile_test.go`
- Modify: `internal/sync/tick.go`, `internal/sync/tick_test.go`
- Modify: `internal/db/invalidate.go`, `internal/db/invalidate_test.go`
- Modify: `internal/claims/discover.go`, `internal/claims/sweep.go`, `internal/jetstream/dbstore.go`
- Modify: `internal/api/steam_handlers.go`, `internal/api/steam_handlers_test.go`

**Interfaces:**
- Consumes: `appdb.ListEnabledSourcesByPriority`, `appdb.GetSessionStart/SetSessionStart/ClearSessionStart` (Task 2/existing), `Decide` (existing, unchanged), `GameResolver`/`RecordWriter` (existing).
- Produces: `sync.Reconciler{Conn, Resolver, Writer}` with `Reconcile(ctx, did, now) error`; `sync.UpdateSession(ctx, conn, reconciler, did, source, playing, gameKey, now) error`; `db.Reconciler` interface (`Reconcile(ctx, did, now) error`), satisfied structurally by `*sync.Reconciler` with no import from `db` back to `sync`.
- After this task, `db.InvalidateClaim` takes a `db.Reconciler` instead of a `db.StatusDeleter`, and no longer unconditionally deletes the PDS record.

- [ ] **Step 1: Write the Reconciler test**

```go
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
	appdb.SetSessionStart(ctx, conn, did, appdb.SteamSource, "999999", time.Now())  // unresolvable
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
```

(`fakeResolver`, `fakeWriter`, `openTestDB` already exist in `internal/sync/tick_test.go` — same package, reused as-is.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/sync/... -run TestReconcile -v`
Expected: FAIL to compile — `Reconciler` doesn't exist yet.

- [ ] **Step 3: Implement the Reconciler and `UpdateSession`**

```go
// internal/sync/reconcile.go
package sync

import (
	"context"
	"database/sql"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

type Reconciler struct {
	Conn     *sql.DB
	Resolver GameResolver
	Writer   RecordWriter
}

var _ appdb.Reconciler = (*Reconciler)(nil)

// Reconcile decides what, if anything, is publicly shown for did: the
// highest-priority enabled source with both a live session and a
// resolvable game wins; an unresolvable game falls through to the next
// source, same as "not currently playing" from that source's point of
// view. Nothing playing (or nothing resolvable) deletes the record.
//
// Call this after any change to any source's session_starts row, after
// enable/disable, after a priority reorder, and after a claim
// invalidation — nothing else should call Writer.PutStatus/DeleteStatus.
func (r *Reconciler) Reconcile(ctx context.Context, did string, now time.Time) error {
	sources, err := appdb.ListEnabledSourcesByPriority(ctx, r.Conn, did)
	if err != nil {
		return err
	}

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
		return r.Writer.PutStatus(ctx, did, ActorStatus{
			Type: "games.gamesgamesgamesgames.actor.status", Game: game.URI,
			Playing: map[string]any{},
			Embed:   &Embed{Type: "app.bsky.embed.external", External: EmbedExternal{URI: game.PageURL, Title: game.Name, Description: game.Summary}},
			CreatedAt: row.StartedAt.UTC().Format(time.RFC3339),
			StaleAt:   now.Add(staleBuffer).UTC().Format(time.RFC3339),
			Via:       ViaClientName,
		})
	}
	return r.Writer.DeleteStatus(ctx, did)
}

// UpdateSession is the source-agnostic half of what tickOne used to do
// alone: given whether a source is currently playing something (and
// what), update that source's session_starts row via the existing pure
// Decide, then let the reconciler decide what's publicly shown. Steam's
// tick and Discord's presence handler both call this — it's the only
// place either one touches session_starts.
func UpdateSession(ctx context.Context, conn *sql.DB, reconciler *Reconciler, did, source string, playing bool, gameKey string, now time.Time) error {
	var prev *SessionStart
	row, err := appdb.GetSessionStart(ctx, conn, did, source)
	if err != nil {
		return err
	}
	if row != nil {
		prev = &SessionStart{GameKey: row.GameKey, StartedAt: row.StartedAt}
	}

	decision := Decide(playing, gameKey, prev, now)
	switch decision.Action {
	case ActionDelete:
		if err := appdb.ClearSessionStart(ctx, conn, did, source); err != nil {
			return err
		}
	case ActionWrite:
		if err := appdb.SetSessionStart(ctx, conn, did, source, decision.GameKey, decision.CreatedAt); err != nil {
			return err
		}
	}
	return reconciler.Reconcile(ctx, did, now)
}
```

Add the `db.Reconciler` interface (same pattern as `StatusDeleter` — lives in `db` so other packages can satisfy it structurally without an import cycle) and update `InvalidateClaim`:

```go
// internal/db/invalidate.go
type Reconciler interface {
	Reconcile(ctx context.Context, did string, now time.Time) error
}

// InvalidateClaim is the complete undo of a verified claim ... [keep existing doc comment, add:]
//
// The final step re-runs the reconciler rather than unconditionally
// deleting the PDS record: once more than one source can be enabled,
// losing confidence in ONE source's claim must not blank a record another
// source is still legitimately populating.
func InvalidateClaim(ctx context.Context, conn *sql.DB, reconciler Reconciler, did, claimType string, now time.Time) error {
	if err := DeleteClaim(ctx, conn, did, claimType); err != nil {
		return err
	}
	if err := SetEnabled(ctx, conn, did, claimType, false); err != nil {
		return err
	}
	if err := ClearSessionStart(ctx, conn, did, claimType); err != nil {
		return err
	}
	return reconciler.Reconcile(ctx, did, now)
}
```

Remove `StatusDeleter` if nothing references it after this task (check with `grep -rn StatusDeleter internal/`); if `sync.ATProtoWriter` or another type's sole use of `DeleteStatus` in isolation still needs it, keep it — otherwise delete it, it's replaced by `Reconciler` everywhere that mattered.

Update every `InvalidateClaim` caller to pass a `*sync.Reconciler` and `time.Now()` instead of the old deleter:
- `internal/claims/discover.go`: `Discover`'s signature gains a `reconciler db.Reconciler` param (replacing `deleter appdb.StatusDeleter`); its `return appdb.InvalidateClaim(...)` call becomes `return appdb.InvalidateClaim(ctx, conn, reconciler, did, appdb.SteamSource, time.Now())`.
- `internal/claims/sweep.go`: `RunSweep`'s `deleter appdb.StatusDeleter` param becomes `reconciler appdb.Reconciler`; both `InvalidateClaim` call sites updated the same way.
- `internal/jetstream/dbstore.go`: `DBStore` gains a `Reconciler appdb.Reconciler` field (replacing `Deleter`); `InvalidateClaim` method body becomes `return appdb.InvalidateClaim(ctx, appdb.Conn... , s.Reconciler, did, appdb.SteamSource, time.Now())` — keep `s.Conn` for the connection, just swap the deleter arg.
- `internal/api/steam_handlers.go`: `SteamHandlers` gains a `Reconciler db.Reconciler` field. In `SetEnabled`'s disable branch, replace `h.Deleter.DeleteStatus(r.Context(), did)` with `h.Reconciler.Reconcile(r.Context(), did, time.Now())` (same reasoning as `InvalidateClaim` — a manual Steam disable must not blank a still-valid Discord-sourced record). Remove the `Deleter db.StatusDeleter` field if nothing else in this struct uses it.

Update `internal/sync/tick.go`'s `RunTick`/`tickOne` to use `UpdateSession`:

```go
func RunTick(ctx context.Context, conn *sql.DB, steamAPI SteamAPI, resolver GameResolver, writer RecordWriter, budget CallBudget, now time.Time) error {
	dids, err := appdb.ListEnabledDIDs(ctx, conn, appdb.SteamSource)
	// ... unchanged batching/budget logic above this line, just the ListSteamEnabledDIDs -> ListEnabledDIDs rename ...

	reconciler := &Reconciler{Conn: conn, Resolver: resolver, Writer: writer}
	for steamID, did := range steamIDToDID {
		summary, ok := summaries[steamID]
		if !ok {
			slog.Warn("steam omitted account from response, skipping this tick", "steam_id", steamID, "did", did)
			continue
		}
		playing := summary.GameID != ""
		if err := UpdateSession(ctx, conn, reconciler, did, appdb.SteamSource, playing, summary.GameID, now); err != nil {
			slog.Error("sync tick failed for account", "did", did, "err", err)
		}
	}
	return nil
}
```

Delete the now-unused `tickOne` function entirely.

- [ ] **Step 4: Update the callers' existing tests for the new signatures**

- `internal/db/invalidate_test.go`: change `fakeDeleter` (implements `DeleteStatus`) to a `fakeReconciler` implementing `Reconcile(ctx, did, now) error` that records calls; update the `InvalidateClaim` call and its assertion accordingly.
- `internal/claims/discover_test.go` / `sweep_test.go`: replace `fakeSweepDeleter` with an equivalent `fakeReconciler`; pass it wherever the old deleter was passed.
- `internal/jetstream/handler_test.go`: `fakeStore.InvalidateClaim` is unaffected (it stands in for the whole `db.InvalidateClaim` call, same as today) — no change needed there; only `dbstore_test.go`, if one exists, or `main.go`'s wiring needs the new field name.
- `internal/api/steam_handlers_test.go`: give `SteamHandlers` a `Reconciler: &fakeReconciler{}` (local fake, records `(did, now)` calls) in every test that previously set `Deleter`; `TestSetEnabled_DisableDeletesStatusAndClearsSession` now asserts the fake reconciler was called for the DID instead of asserting a delete call — update its assertion to check `len(reconciler.calls) == 1`.

- [ ] **Step 5: Run the full suite, verify green**

Run: `go build ./... && go test ./... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "sync: introduce Reconciler as the single PDS write path"
```

---

## Task 4: Discord detectable-games → Steam App ID index

**Files:**
- Create: `internal/discord/detectable.go`
- Test: `internal/discord/detectable_test.go`
- Create: `go.mod` entry (none yet — this task is pure `net/http`, no new dependency)

**Interfaces:**
- Produces: `discord.GameIndex{HTTPClient}`, `NewGameIndex()`, `(*GameIndex).Refresh(ctx) error`, `(*GameIndex).SteamAppID(applicationID string) (string, bool)`.
- Consumed by: Task 8's presence handler.

- [ ] **Step 1: Write the failing test**

```go
// internal/discord/detectable_test.go
package discord

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const fixtureJSON = `[
	{"id":"356875988589740042","name":"Dota 2","third_party_skus":[{"distributor":"steam","id":"570"}]},
	{"id":"1","name":"No Steam Release","third_party_skus":[{"distributor":"battlenet","id":null}]},
	{"id":"2","name":"No SKUs At All"}
]`

func TestRefresh_IndexesSteamSKUsOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixtureJSON))
	}))
	defer server.Close()

	idx := NewGameIndex()
	idx.detectableURL = server.URL
	if err := idx.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	appID, ok := idx.SteamAppID("356875988589740042")
	if !ok || appID != "570" {
		t.Fatalf("SteamAppID(dota2) = %q, %v, want 570, true", appID, ok)
	}
	if _, ok := idx.SteamAppID("1"); ok {
		t.Fatal("SteamAppID(no steam release) = true, want false")
	}
	if _, ok := idx.SteamAppID("2"); ok {
		t.Fatal("SteamAppID(no skus) = true, want false")
	}
	if _, ok := idx.SteamAppID("unknown"); ok {
		t.Fatal("SteamAppID(unknown application_id) = true, want false")
	}
}

func TestRefresh_UnexpectedStatus_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	idx := NewGameIndex()
	idx.detectableURL = server.URL
	if err := idx.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh: got nil error, want one for a 500 response")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/discord/... -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement**

```go
// internal/discord/detectable.go
package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

const defaultDetectableURL = "https://discord.com/api/v10/applications/detectable"

type detectableEntry struct {
	ID             string `json:"id"`
	ThirdPartySKUs []struct {
		Distributor string `json:"distributor"`
		ID          string `json:"id"`
	} `json:"third_party_skus"`
}

// GameIndex maps a presence activity's application_id to a Steam App ID.
// Confirmed live (design session, 2026-08-30): ~77% of Discord's ~24k
// detectable games carry one. Safe for concurrent Refresh/SteamAppID calls.
type GameIndex struct {
	mu             sync.RWMutex
	steamAppID     map[string]string
	HTTPClient     *http.Client
	detectableURL  string // overridable for tests
}

func NewGameIndex() *GameIndex {
	return &GameIndex{steamAppID: map[string]string{}, HTTPClient: http.DefaultClient, detectableURL: defaultDetectableURL}
}

func (g *GameIndex) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.detectableURL, nil)
	if err != nil {
		return err
	}
	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch detectable applications: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch detectable applications: unexpected status %d", resp.StatusCode)
	}

	var entries []detectableEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return fmt.Errorf("decode detectable applications: %w", err)
	}

	next := make(map[string]string, len(entries))
	for _, e := range entries {
		for _, sku := range e.ThirdPartySKUs {
			if sku.Distributor == "steam" && sku.ID != "" {
				next[e.ID] = sku.ID
				break // first steam SKU wins; a small minority of entries list more than one
			}
		}
	}

	g.mu.Lock()
	g.steamAppID = next
	g.mu.Unlock()
	return nil
}

// SteamAppID looks up the Steam App ID for a Discord application_id, as of
// the most recent Refresh. ok=false covers both "unknown application_id"
// and "known, but no Steam release" — both mean "skip" to callers.
func (g *GameIndex) SteamAppID(applicationID string) (string, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	id, ok := g.steamAppID[applicationID]
	return id, ok
}
```

- [ ] **Step 4: Run tests, verify green**

Run: `go test ./internal/discord/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discord/detectable.go internal/discord/detectable_test.go
git commit -m "discord: add detectable-games to Steam App ID index"
```

---

## Task 5: Multi-type claim discovery (Steam + Discord)

**Files:**
- Modify: `internal/claims/discover.go`, `internal/claims/discover_test.go`
- Modify: `internal/claims/sweep.go`, `internal/claims/sweep_test.go`
- Modify: `internal/jetstream/handler.go`, `internal/jetstream/handler_test.go`

**Interfaces:**
- Produces: `claims.SubjectResolver` interface (`ResolveDiscordSubject(ctx, did string, claim keytrace.Claim) (subject string, ok bool)`), consumed by `Discover`/`RunSweep`. Task 6 provides the real implementation; this task's own tests use a fake.
- `Discover`/`RunSweep` now scan/verify **both** `"steam"` and `"discord"` claim types in one pass, each independently upserted or invalidated.

Discord's resolution can legitimately fail transiently (verified claim, but the person hasn't joined the tracking server yet) — `Discover` treats that as "leave existing state alone, don't invalidate," since there may be no existing state yet on first discovery. `RunSweep`, which only ever re-checks an **already-resolved** claim, treats a resolution failure as a real regression (they may have left the server, or renamed away from what was trusted) and invalidates — mirroring why Steam's sweep already invalidates on a bad re-verification.

- [ ] **Step 1: Write the failing tests**

```go
// internal/claims/discover_test.go — add alongside the existing Steam tests
const realDiscordClaimJSON = `{
	"type":"discord","status":"verified",
	"claimUri":"https://discord.gg/EvTSZhkk4P",
	"identity":{"subject":"jphastings","profileUrl":"https://discord.com/users/690973862245957683","displayName":"byjp"},
	"sigs":[{
		"kid":"attest:discord",
		"src":"at://did:plc:hcwfdlmprcc335oixyfsw7u3/dev.keytrace.serverPublicKey/2026-08-30",
		"signedAt":"2026-08-30T16:41:52.267Z",
		"attestation":"eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJjbGFpbVVyaSI6Imh0dHBzOi8vZGlzY29yZC5nZy9FdlRTWmhrazRQIiwiZGlkIjoiZGlkOnBsYzplcGhrenBpbmhhcWNhYnRrdWd0Ynpyd3UiLCJpZGVudGl0eS5zdWJqZWN0IjoianBoYXN0aW5ncyIsInR5cGUiOiJkaXNjb3JkIn0.LWrnyrz8xmZRR2S1BMmZSQLD1uAPRyCVz9t9mW0tHGs0gcVKIKDWmI7Bf3oDsVamw9BrvkQWOCVfRA5KufdF3w",
		"signedFields":["claimUri","did","identity.subject","type"]
	}],
	"createdAt":"2026-08-30T16:41:52.320Z","lastVerifiedAt":"2026-08-30T16:41:52.320Z"
}`

type fakeSubjectResolver struct{ resolved map[string]string } // claimUri -> resolved snowflake

func (f fakeSubjectResolver) ResolveDiscordSubject(ctx context.Context, did string, claim keytrace.Claim) (string, bool) {
	id, ok := f.resolved[claim.ClaimURI]
	return id, ok
}

func TestDiscover_UpsertsBothTypesInOnePass(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	steamRaw := json.RawMessage(realClaimJSON)
	discordRaw := json.RawMessage(realDiscordClaimJSON)
	client := &fakeLexClient{records: []*agnostic.RepoListRecords_Record{
		{Uri: "at://" + realClaimDID + "/dev.keytrace.claim/steam-rkey", Value: &steamRaw},
		{Uri: "at://" + realClaimDID + "/dev.keytrace.claim/discord-rkey", Value: &discordRaw},
	}}
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	resolver := fakeSubjectResolver{resolved: map[string]string{"https://discord.gg/EvTSZhkk4P": "690973862245957683"}}

	if err := Discover(ctx, client, verifier, resolver, conn, &fakeReconciler{}, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	steam, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.SteamSource)
	if err != nil || steam == nil || steam.Subject != "76561197994000231" {
		t.Fatalf("got steam claim %+v, %v", steam, err)
	}
	discord, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.DiscordSource)
	if err != nil || discord == nil || discord.Subject != "690973862245957683" {
		t.Fatalf("got discord claim %+v, %v, want resolved snowflake as subject (never the raw username)", discord, err)
	}
}

func TestDiscover_DiscordVerifiedButUnresolved_LeavesNoRowRatherThanInvalidating(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	discordRaw := json.RawMessage(realDiscordClaimJSON)
	client := &fakeLexClient{records: []*agnostic.RepoListRecords_Record{
		{Uri: "at://" + realClaimDID + "/dev.keytrace.claim/discord-rkey", Value: &discordRaw},
	}}
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	resolver := fakeSubjectResolver{resolved: map[string]string{}} // hasn't joined the tracking server yet
	reconciler := &fakeReconciler{}

	if err := Discover(ctx, client, verifier, resolver, conn, reconciler, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	got, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.DiscordSource)
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v, want no row (unresolved is not the same as invalid)", got, err)
	}
}
```

Add matching `fakeReconciler` (records `(did, now)` calls, satisfies `appdb.Reconciler`) to `internal/claims`'s test files if Task 3 didn't already leave one there.

```go
// internal/claims/sweep_test.go — add
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

	fetcher := fakeRecordFetcher{records: map[string]*keytrace.Claim{
		"at://" + realClaimDID + "/dev.keytrace.claim/discord-rkey": unmarshalClaim(t, realDiscordClaimJSON),
	}}
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	resolver := fakeSubjectResolver{resolved: map[string]string{}} // no longer in the server / renamed
	reconciler := &fakeReconciler{}

	if err := RunSweep(ctx, conn, fetcher, verifier, resolver, reconciler); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}
	got, err := appdb.GetClaim(ctx, conn, realClaimDID, appdb.DiscordSource)
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v, want invalidated", got, err)
	}
}
```

(`fakeRecordFetcher`/`unmarshalClaim` mirror whatever helper `sweep_test.go` already uses for its existing Steam sweep tests — reuse that pattern, not a new one.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/claims/... -v`
Expected: FAIL to compile — `SubjectResolver`, the new `Discover`/`RunSweep` params don't exist yet.

- [ ] **Step 3: Implement**

```go
// internal/claims/discover.go
package claims

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bluesky-social/indigo/api/agnostic"
	lexutil "github.com/bluesky-social/indigo/lex/util"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

var supportedClaimTypes = []string{appdb.SteamSource, appdb.DiscordSource}

// SubjectResolver turns a verified Discord claim's signed username into the
// stable snowflake ID sync matches presence events against. ok=false means
// "not resolvable right now" (e.g. they haven't joined the tracking server
// yet) — not an error, and callers must not treat it as one.
type SubjectResolver interface {
	ResolveDiscordSubject(ctx context.Context, did string, claim keytrace.Claim) (subject string, ok bool)
}

type foundClaim struct {
	claim keytrace.Claim
	uri   string
}

// Discover re-scans the user's own dev.keytrace.claim collection for
// verified, cryptographically-checked claims of every supported type, and
// upserts/invalidates each type's row in claims accordingly. See spec's
// "Claim indexing" and the design doc's "Linking" section.
func Discover(ctx context.Context, client lexutil.LexClient, verifier *keytrace.Verifier, resolver SubjectResolver, conn *sql.DB, reconciler appdb.Reconciler, did string) error {
	found := map[string]foundClaim{}

	var cursor string
	for {
		resp, err := agnostic.RepoListRecords(ctx, client, keytrace.ClaimCollection, cursor, 100, did, false)
		if err != nil {
			return fmt.Errorf("list claim records: %w", err)
		}
		for _, rec := range resp.Records {
			var claim keytrace.Claim
			if err := json.Unmarshal(*rec.Value, &claim); err != nil {
				continue // skip malformed records rather than fail the whole scan
			}
			if claim.Status != "verified" || !isSupportedType(claim.Type) {
				continue
			}
			if _, already := found[claim.Type]; already {
				continue // first verified claim of each type wins
			}
			ok, err := verifier.VerifyAttestation(ctx, did, claim)
			if err != nil {
				return fmt.Errorf("verify claim %s: %w", rec.Uri, err)
			}
			if ok {
				found[claim.Type] = foundClaim{claim: claim, uri: rec.Uri}
			}
		}
		if resp.Cursor == nil || *resp.Cursor == "" {
			break
		}
		cursor = *resp.Cursor
	}

	for _, claimType := range supportedClaimTypes {
		fc, ok := found[claimType]
		if !ok {
			if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
				return err
			}
			continue
		}

		subject := fc.claim.Identity.Subject
		if claimType == appdb.DiscordSource {
			resolved, ok := resolver.ResolveDiscordSubject(ctx, did, fc.claim)
			if !ok {
				continue // verified, but not resolvable yet — leave any prior state alone
			}
			subject = resolved
		}

		if err := appdb.UpsertClaim(ctx, conn, appdb.Claim{
			DID: did, Type: claimType, Subject: subject, DisplayName: fc.claim.Identity.DisplayName,
			ClaimURI: fc.claim.ClaimURI, RecordURI: fc.uri, LastVerifiedAt: time.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func isSupportedType(t string) bool {
	for _, s := range supportedClaimTypes {
		if s == t {
			return true
		}
	}
	return false
}
```

```go
// internal/claims/sweep.go — RunSweep gains a resolver param and per-type handling
func RunSweep(ctx context.Context, conn *sql.DB, fetcher RecordFetcher, verifier *keytrace.Verifier, resolver SubjectResolver, reconciler appdb.Reconciler) error {
	for _, claimType := range supportedClaimTypes {
		dids, err := appdb.ListEnabledDIDs(ctx, conn, claimType)
		if err != nil {
			return err
		}

		for _, did := range dids {
			claim, err := appdb.GetClaim(ctx, conn, did, claimType)
			if err != nil {
				return err
			}
			if claim == nil {
				continue
			}

			c, deleted, err := fetcher.FetchClaimRecord(ctx, claim.RecordURI)
			if err != nil {
				continue // uncertain outcome — try again on tomorrow's sweep
			}
			if deleted || c.Status != "verified" {
				if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
					return err
				}
				continue
			}

			ok, err := verifier.VerifyAttestation(ctx, did, *c)
			if err != nil {
				continue
			}
			if !ok {
				if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
					return err
				}
				continue
			}

			subject := c.Identity.Subject
			if claimType == appdb.DiscordSource {
				resolved, ok := resolver.ResolveDiscordSubject(ctx, did, *c)
				if !ok {
					// unlike first discovery, this is a regression from an
					// already-resolved state — invalidate rather than leave stale.
					if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
						return err
					}
					continue
				}
				subject = resolved
			}

			if err := appdb.UpsertClaim(ctx, conn, appdb.Claim{
				DID: did, Type: claimType, Subject: subject, DisplayName: c.Identity.DisplayName,
				ClaimURI: c.ClaimURI, RecordURI: claim.RecordURI, LastVerifiedAt: time.Now(),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
```

`internal/jetstream/handler.go`'s `HandleEvent` widen its type filter from `claim.Type != "steam"` to `!isSupportedType(claim.Type)` — it can call the same helper if `claims` and `jetstream` don't create an import cycle (they don't today: `jetstream` doesn't import `claims`). If duplicating the two-element check is simpler than adding the cross-package dependency, duplicate it — this one is genuinely two lines and matches the "don't add a dependency for what one line can do" call. For the Discord branch specifically, `HandleEvent` needs a `SubjectResolver` too, threaded in the same way `Store`/`verifier` already are — add it as a new parameter, and resolve+upsert using `claimType := ev's claim.Type` instead of the hardcoded `appdb.SteamSource` used in today's `store.UpsertSteamClaim`/`store.GetSteamClaim` calls (already renamed to `UpsertClaim`/`GetClaim` with an explicit type argument in Task 2).

- [ ] **Step 4: Update `main.go`'s call sites for the new signatures (compile-only change at this point — the real resolver arrives in Task 6)**

Task 6 provides the real `SubjectResolver`; for now, wherever `main.go` calls `claims.Discover`/`claims.RunSweep`/constructs `jetstream.HandleEvent`, this task only needs the code to compile — leave a `TODO`-free but temporary concrete resolver stub isn't appropriate (no placeholders), so this step is deferred entirely to Task 9, which wires `main.go` once the real resolver (Task 6) exists. Skip touching `main.go` in this task; it will not compile standalone until Task 9 — that's fine, this plan's tasks are reviewed on their own package's tests, and `internal/claims`, `internal/jetstream` compile and test correctly in isolation after this task.

- [ ] **Step 5: Run the affected packages' tests, verify green**

Run: `go test ./internal/claims/... ./internal/jetstream/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/claims internal/jetstream
git commit -m "claims: generalize discovery/sweep to scan Steam and Discord claims"
```

---

## Task 6: Discord member cache + claim-subject resolution (pure, no Gateway yet)

**Files:**
- Create: `internal/discord/members.go`
- Test: `internal/discord/members_test.go`
- Create: `internal/discord/resolve.go`
- Test: `internal/discord/resolve_test.go`

**Interfaces:**
- Produces: `discord.MemberCache{}`, `NewMemberCache()`, `(*MemberCache).Set/Remove/Username/FindByUsername`; `discord.ClaimResolver{Members *MemberCache}` satisfying `claims.SubjectResolver` structurally.
- Consumed by: Task 7 (Gateway wires real events into `MemberCache`), Task 9 (`main.go` passes a `ClaimResolver` into `claims.Discover`/`RunSweep`/the jetstream handler).

This is the load-bearing security logic from the design doc's "Linking" section — fully unit-testable with no real Discord connection.

- [ ] **Step 1: Write the failing tests**

```go
// internal/discord/members_test.go
package discord

import "testing"

func TestMemberCache_SetAndUsername(t *testing.T) {
	c := NewMemberCache()
	c.Set("690973862245957683", "jphastings")
	got, ok := c.Username("690973862245957683")
	if !ok || got != "jphastings" {
		t.Fatalf("Username = %q, %v, want jphastings, true", got, ok)
	}
}

func TestMemberCache_Remove(t *testing.T) {
	c := NewMemberCache()
	c.Set("1", "alice")
	c.Remove("1")
	if _, ok := c.Username("1"); ok {
		t.Fatal("Username after Remove = true, want false")
	}
}

func TestMemberCache_FindByUsername_CaseInsensitive(t *testing.T) {
	c := NewMemberCache()
	c.Set("690973862245957683", "jphastings")
	id, ok := c.FindByUsername("JPHastings")
	if !ok || id != "690973862245957683" {
		t.Fatalf("FindByUsername = %q, %v, want the matching ID", id, ok)
	}
}

func TestMemberCache_FindByUsername_NoMatch(t *testing.T) {
	c := NewMemberCache()
	if _, ok := c.FindByUsername("nobody"); ok {
		t.Fatal("FindByUsername(nobody) = true, want false")
	}
}
```

```go
// internal/discord/resolve_test.go
package discord

import (
	"context"
	"testing"

	"github.com/jphastings/game-status/internal/keytrace"
)

func realDiscordClaim() keytrace.Claim {
	return keytrace.Claim{
		Type: "discord",
		Identity: keytrace.ClaimIdentity{
			Subject:    "jphastings",
			ProfileURL: "https://discord.com/users/690973862245957683",
		},
	}
}

func TestResolveDiscordSubject_TrustsSignedSubjectViaProfileURLHint(t *testing.T) {
	members := NewMemberCache()
	members.Set("690973862245957683", "jphastings")
	r := &ClaimResolver{Members: members}

	id, ok := r.ResolveDiscordSubject(context.Background(), "did:plc:test", realDiscordClaim())
	if !ok || id != "690973862245957683" {
		t.Fatalf("ResolveDiscordSubject = %q, %v, want the hinted ID confirmed by cache", id, ok)
	}
}

func TestResolveDiscordSubject_RejectsProfileURLHintWhenUsernameMismatches(t *testing.T) {
	members := NewMemberCache()
	members.Set("690973862245957683", "someone-else-now") // profileUrl is unsigned — this ID may not actually be them
	r := &ClaimResolver{Members: members}

	if _, ok := r.ResolveDiscordSubject(context.Background(), "did:plc:test", realDiscordClaim()); ok {
		t.Fatal("ResolveDiscordSubject trusted an unsigned profileUrl hint whose username doesn't match the signed subject")
	}
}

func TestResolveDiscordSubject_FallsBackToUsernameScanWhenProfileURLMissing(t *testing.T) {
	members := NewMemberCache()
	members.Set("690973862245957683", "jphastings")
	claim := realDiscordClaim()
	claim.Identity.ProfileURL = ""
	r := &ClaimResolver{Members: members}

	id, ok := r.ResolveDiscordSubject(context.Background(), "did:plc:test", claim)
	if !ok || id != "690973862245957683" {
		t.Fatalf("ResolveDiscordSubject (no hint) = %q, %v, want a match via username scan", id, ok)
	}
}

func TestResolveDiscordSubject_NotInGuildYet_NotOK(t *testing.T) {
	r := &ClaimResolver{Members: NewMemberCache()}
	if _, ok := r.ResolveDiscordSubject(context.Background(), "did:plc:test", realDiscordClaim()); ok {
		t.Fatal("ResolveDiscordSubject = true, want false — cache is empty, they haven't joined")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/discord/... -run 'TestMemberCache|TestResolveDiscordSubject' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

```go
// internal/discord/members.go
package discord

import "sync"

// MemberCache tracks (snowflake -> username) for the tracking guild,
// rebuilt from Gateway events (see gateway.go) — nothing here is
// persisted, a reconnect gets a fresh snapshot that repopulates it.
type MemberCache struct {
	mu       sync.RWMutex
	username map[string]string
}

func NewMemberCache() *MemberCache {
	return &MemberCache{username: map[string]string{}}
}

func (c *MemberCache) Set(id, username string) {
	c.mu.Lock()
	c.username[id] = username
	c.mu.Unlock()
}

func (c *MemberCache) Remove(id string) {
	c.mu.Lock()
	delete(c.username, id)
	c.mu.Unlock()
}

func (c *MemberCache) Username(id string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	u, ok := c.username[id]
	return u, ok
}

// FindByUsername scans for a member with this exact (case-insensitive)
// username. Discord usernames are globally unique, so at most one match is
// expected.
func (c *MemberCache) FindByUsername(username string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for id, u := range c.username {
		if strings.EqualFold(u, username) {
			return id, true
		}
	}
	return "", false
}
```

(add `"strings"` to the import block)

```go
// internal/discord/resolve.go
package discord

import (
	"context"
	"strings"

	"github.com/jphastings/game-status/internal/keytrace"
)

// ClaimResolver resolves a verified Discord claim's signed username to a
// stable snowflake ID. Security property: only claim.Identity.Subject (the
// SIGNED field) is ever load-bearing. claim.Identity.ProfileURL is unsigned
// client-filled metadata — usable only as a fast-path hint that still has
// to be confirmed against the signed subject before being trusted.
type ClaimResolver struct {
	Members *MemberCache
}

func (r *ClaimResolver) ResolveDiscordSubject(ctx context.Context, did string, claim keytrace.Claim) (string, bool) {
	if id := snowflakeFromProfileURL(claim.Identity.ProfileURL); id != "" {
		if username, ok := r.Members.Username(id); ok && strings.EqualFold(username, claim.Identity.Subject) {
			return id, true
		}
	}
	return r.Members.FindByUsername(claim.Identity.Subject)
}

func snowflakeFromProfileURL(url string) string {
	const prefix = "https://discord.com/users/"
	if !strings.HasPrefix(url, prefix) {
		return ""
	}
	id := strings.TrimPrefix(url, prefix)
	if id == "" {
		return ""
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return id
}
```

- [ ] **Step 4: Run tests, verify green**

Run: `go test ./internal/discord/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discord/members.go internal/discord/members_test.go internal/discord/resolve.go internal/discord/resolve_test.go
git commit -m "discord: add member cache and signed-subject claim resolution"
```

---

## Task 7: Discord Gateway connection

**Files:**
- Modify: `go.mod`, `go.sum` (add `github.com/bwmarrin/discordgo`)
- Create: `internal/discord/gateway.go`
- Test: `internal/discord/gateway_test.go` (handler-dispatch logic only — no real Gateway connection in tests, same "thin glue" philosophy the original design doc already applied to OAuth/Jetstream/Steam-API code)

**Interfaces:**
- Produces: `discord.Gateway{Session, GuildID, Members, OnJoin, OnLeave}`, `NewGateway(token, guildID string) (*Gateway, error)`, `(*Gateway).Open/Close`, `(*Gateway).SendDM(userID, message string) error`.
- Consumed by: Task 8 (presence handler registers on `Gateway.Session`), Task 9 (`main.go` construction + `OnJoin`/`OnLeave` wiring).

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/bwmarrin/discordgo@latest
go mod tidy
```

- [ ] **Step 2: Write the failing test (handler logic only, constructed directly against discordgo's event types — no live connection)**

```go
// internal/discord/gateway_test.go
package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

const guildID = "123456789012345678"

func TestHandleGuildCreate_SeedsMemberCacheForTrackedGuildOnly(t *testing.T) {
	gw := &Gateway{GuildID: guildID, Members: NewMemberCache()}

	gw.handleGuildCreate(nil, &discordgo.GuildCreate{Guild: &discordgo.Guild{
		ID: guildID,
		Members: []*discordgo.Member{
			{User: &discordgo.User{ID: "1", Username: "alice"}},
			{User: &discordgo.User{ID: "2", Username: "bob"}},
		},
	}})
	gw.handleGuildCreate(nil, &discordgo.GuildCreate{Guild: &discordgo.Guild{
		ID: "some-other-guild",
		Members: []*discordgo.Member{{User: &discordgo.User{ID: "9", Username: "ignored"}}},
	}})

	if u, ok := gw.Members.Username("1"); !ok || u != "alice" {
		t.Fatalf("Username(1) = %q, %v, want alice, true", u, ok)
	}
	if _, ok := gw.Members.Username("9"); ok {
		t.Fatal("member from an untracked guild was cached")
	}
}

func TestHandleMemberAdd_CachesAndFiresOnJoin(t *testing.T) {
	gw := &Gateway{GuildID: guildID, Members: NewMemberCache()}
	var joined string
	gw.OnJoin = func(discordID string) { joined = discordID }

	gw.handleMemberAdd(nil, &discordgo.GuildMemberAdd{Member: &discordgo.Member{
		GuildID: guildID, User: &discordgo.User{ID: "42", Username: "newperson"},
	}})

	if u, ok := gw.Members.Username("42"); !ok || u != "newperson" {
		t.Fatalf("Username(42) = %q, %v, want newperson, true", u, ok)
	}
	if joined != "42" {
		t.Fatalf("OnJoin fired with %q, want 42", joined)
	}
}

func TestHandleMemberRemove_UncachesAndFiresOnLeave(t *testing.T) {
	gw := &Gateway{GuildID: guildID, Members: NewMemberCache()}
	gw.Members.Set("42", "newperson")
	var left string
	gw.OnLeave = func(discordID string) { left = discordID }

	gw.handleMemberRemove(nil, &discordgo.GuildMemberRemove{Member: &discordgo.Member{
		GuildID: guildID, User: &discordgo.User{ID: "42"},
	}})

	if _, ok := gw.Members.Username("42"); ok {
		t.Fatal("member still cached after handleMemberRemove")
	}
	if left != "42" {
		t.Fatalf("OnLeave fired with %q, want 42", left)
	}
}

func TestHandleEvents_IgnoreOtherGuilds(t *testing.T) {
	gw := &Gateway{GuildID: guildID, Members: NewMemberCache()}
	gw.OnJoin = func(string) { t.Fatal("OnJoin fired for a different guild") }

	gw.handleMemberAdd(nil, &discordgo.GuildMemberAdd{Member: &discordgo.Member{
		GuildID: "different-guild", User: &discordgo.User{ID: "1", Username: "x"},
	}})
	if _, ok := gw.Members.Username("1"); ok {
		t.Fatal("member from a different guild was cached")
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/discord/... -run TestHandle -v`
Expected: FAIL to compile — `Gateway` doesn't exist yet.

- [ ] **Step 4: Implement**

```go
// internal/discord/gateway.go
package discord

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// Gateway holds the always-on Discord connection for the tracking guild.
// Unlike internal/jetstream's Manager, there is no restart-on-watch-list-
// change pattern: Discord has no server-side subscription to update —
// once connected with the presence/members intents, the bot receives
// every relevant event for the whole guild for as long as the connection
// lives. discordgo's Session.Open handles reconnect/resume internally.
type Gateway struct {
	Session *discordgo.Session
	GuildID string
	Members *MemberCache
	OnJoin  func(discordID string)
	OnLeave func(discordID string)
}

func NewGateway(token, guildID string) (*Gateway, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildPresences | discordgo.IntentsGuildMembers

	gw := &Gateway{Session: session, GuildID: guildID, Members: NewMemberCache()}
	session.AddHandler(gw.handleGuildCreate)
	session.AddHandler(gw.handleMemberAdd)
	session.AddHandler(gw.handleMemberUpdate)
	session.AddHandler(gw.handleMemberRemove)
	return gw, nil
}

func (g *Gateway) Open() error  { return g.Session.Open() }
func (g *Gateway) Close() error { return g.Session.Close() }

func (g *Gateway) handleGuildCreate(s *discordgo.Session, e *discordgo.GuildCreate) {
	if e.ID != g.GuildID {
		return
	}
	for _, m := range e.Members {
		g.Members.Set(m.User.ID, m.User.Username)
	}
}

func (g *Gateway) handleMemberAdd(s *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if e.GuildID != g.GuildID {
		return
	}
	g.Members.Set(e.User.ID, e.User.Username)
	if g.OnJoin != nil {
		g.OnJoin(e.User.ID)
	}
}

func (g *Gateway) handleMemberUpdate(s *discordgo.Session, e *discordgo.GuildMemberUpdate) {
	if e.GuildID != g.GuildID {
		return
	}
	g.Members.Set(e.User.ID, e.User.Username)
}

func (g *Gateway) handleMemberRemove(s *discordgo.Session, e *discordgo.GuildMemberRemove) {
	if e.GuildID != g.GuildID {
		return
	}
	g.Members.Remove(e.User.ID)
	if g.OnLeave != nil {
		g.OnLeave(e.User.ID)
	}
}

// SendDM delivers onboarding instructions to a newly-joined member — the
// tracking guild has no shared channel to post them in, so this is the
// Discord equivalent of keytrace.dev's role in the Steam flow.
func (g *Gateway) SendDM(userID, message string) error {
	ch, err := g.Session.UserChannelCreate(userID)
	if err != nil {
		return fmt.Errorf("open DM channel: %w", err)
	}
	_, err = g.Session.ChannelMessageSend(ch.ID, message)
	return err
}
```

- [ ] **Step 5: Run tests, verify green**

Run: `go build ./... && go test ./internal/discord/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/discord/gateway.go internal/discord/gateway_test.go
git commit -m "discord: add Gateway connection, member events, DM sending"
```

---

## Task 8: Discord presence → session tracking → reconcile

**Files:**
- Create: `internal/discord/presence.go`
- Test: `internal/discord/presence_test.go`

**Interfaces:**
- Consumes: `discord.GameIndex` (Task 4), `sync.UpdateSession`/`Reconciler` (Task 3), `db.GetClaim`/`ListEnabledDIDs` (Task 2).
- Produces: `discord.PresenceHandler{Conn, Games, Reconciler}` with a method matching discordgo's `func(*discordgo.Session, *discordgo.PresenceUpdate)` handler shape, plus `HandleGuildMemberRemove` for the "left the server" invalidation path (wired to `Gateway.OnLeave` in Task 9).

This is the Discord equivalent of `sync.RunTick`/`tickOne` — except event-driven rather than polled, so there's no batching or budget: one presence event is one user's update.

- [ ] **Step 1: Write the failing tests**

```go
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
	h := &PresenceHandler{Conn: conn, Games: games, Reconciler: reconciler}

	h.HandlePresenceUpdate(nil, &discordgo.PresenceUpdate{Presence: discordgo.Presence{
		User: &discordgo.User{ID: "690973862245957683"},
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

	h := &PresenceHandler{Conn: conn, Games: NewGameIndex(), Reconciler: &appsync.Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: &fakeWriter{}}}
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
	h := &PresenceHandler{Conn: conn, Games: NewGameIndex(), Reconciler: &appsync.Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: &fakeWriter{}}}

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

	h := &PresenceHandler{Conn: conn, Games: NewGameIndex(), Reconciler: &appsync.Reconciler{Conn: conn, Resolver: fakeResolver{}, Writer: &fakeWriter{}}}
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
```

`fakeResolver`/`fakeWriter`/`openTestDB` need a local copy in `internal/discord`'s test package (same shape as `internal/sync/tick_test.go`'s — that package can't import `internal/sync`'s test-only types across package boundaries, so duplicate the small fakes here, same as the project already does per-package rather than sharing test doubles across packages).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/discord/... -run 'TestPresenceHandler|TestHandleGuildMemberRemove' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

```go
// internal/discord/presence.go
package discord

import (
	"context"
	"database/sql"
	"time"

	"github.com/bwmarrin/discordgo"

	appdb "github.com/jphastings/game-status/internal/db"
	appsync "github.com/jphastings/game-status/internal/sync"
)

type PresenceHandler struct {
	Conn       *sql.DB
	Games      *GameIndex
	Reconciler *appsync.Reconciler
}

// HandlePresenceUpdate is the Discord equivalent of sync.tickOne: given
// whatever's currently playing (or not), update this user's session_starts
// row and reconcile. Unlike Steam's tick, this is push-driven — one event
// is one user, no batching or budget.
func (h *PresenceHandler) HandlePresenceUpdate(s *discordgo.Session, e *discordgo.PresenceUpdate) {
	if e.GuildID != "" && h.Games == nil {
		return // defensive: never called with a nil index in production wiring
	}
	ctx := context.Background()

	claim, err := findClaimByDiscordID(ctx, h.Conn, e.User.ID)
	if err != nil || claim == nil {
		return // not a linked+claimed user, or a lookup error — nothing to do
	}
	enabled, err := appdb.IsEnabled(ctx, h.Conn, claim.DID, appdb.DiscordSource)
	if err != nil || !enabled {
		return
	}

	var gameKey string
	playing := false
	for _, activity := range e.Activities {
		if activity.Type != discordgo.ActivityTypeGame || activity.ApplicationID == "" {
			continue
		}
		if appID, ok := h.Games.SteamAppID(activity.ApplicationID); ok {
			gameKey = appID
			playing = true
			break
		}
	}

	if err := appsync.UpdateSession(ctx, h.Conn, h.Reconciler, claim.DID, appdb.DiscordSource, playing, gameKey, time.Now()); err != nil {
		slog.Error("discord presence update failed", "discord_id", e.User.ID, "err", err)
	}
}

// HandleGuildMemberRemove treats leaving the tracking guild like a revoked
// claim: we can no longer receive this person's presence at all, so
// continuing to show a Discord-sourced status for them would be stale by
// construction.
func (h *PresenceHandler) HandleGuildMemberRemove(ctx context.Context, discordID string) error {
	claim, err := findClaimByDiscordID(ctx, h.Conn, discordID)
	if err != nil || claim == nil {
		return err
	}
	return appdb.InvalidateClaim(ctx, h.Conn, h.Reconciler, claim.DID, appdb.DiscordSource, time.Now())
}

func findClaimByDiscordID(ctx context.Context, conn *sql.DB, discordID string) (*appdb.Claim, error) {
	var did string
	err := conn.QueryRowContext(ctx, `SELECT did FROM claims WHERE claim_type = ? AND subject = ?`, appdb.DiscordSource, discordID).Scan(&did)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return appdb.GetClaim(ctx, conn, did, appdb.DiscordSource)
}
```

(add `"log/slog"` to imports)

- [ ] **Step 4: Run tests, verify green**

Run: `go test ./internal/discord/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discord/presence.go internal/discord/presence_test.go
git commit -m "discord: presence updates drive session tracking and reconciliation"
```

---

## Task 9: API, config, and `main.go` wiring

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/api/discord_handlers.go`
- Create: `internal/api/sync_handlers.go`
- Modify: `internal/api/me_handler.go`, `internal/api/me_handler_test.go`
- Modify: `cmd/server/main.go`
- Modify: `README.md`

**Interfaces:**
- Produces: `DISCORD_BOT_TOKEN`/`DISCORD_GUILD_ID`/`DISCORD_INVITE_URL` config; `POST /api/discord/recheck`, `POST /api/discord/enabled`, `POST /api/sync/order`; extended `/api/me` response.

This task is thin wiring mirroring `SteamHandlers` and the existing `main.go` construction order almost exactly — same "not worth heavy unit coverage" judgment call the original design doc already made for this class of glue code. Tests cover the one branch of genuinely new logic (`/api/sync/order`'s validation).

- [ ] **Step 1: Config**

```go
// internal/config/config.go — add fields and Load() lines
type Config struct {
	// ... existing fields ...
	DiscordBotToken  string
	DiscordGuildID   string
	DiscordInviteURL string
}
```

```go
	discordToken, err := requireEnv("DISCORD_BOT_TOKEN")
	if err != nil {
		return nil, err
	}
	cfg.DiscordBotToken = discordToken

	discordGuildID, err := requireEnv("DISCORD_GUILD_ID")
	if err != nil {
		return nil, err
	}
	cfg.DiscordGuildID = discordGuildID

	discordInviteURL, err := requireEnv("DISCORD_INVITE_URL")
	if err != nil {
		return nil, err
	}
	cfg.DiscordInviteURL = discordInviteURL
```

- [ ] **Step 2: A shared recheck helper, `internal/api/discord_handlers.go`**

`claims.Discover` (Task 5) always scans and verifies every supported claim type in one pass, so "recheck" is the same operation regardless of which row's button triggered it — Steam's `Recheck` and Discord's `Recheck` both just call it. Extract that call into one shared helper so the two handler structs don't each duplicate the session-resume dance:

```go
// internal/api/recheck.go
package api

import (
	"context"
	"fmt"

	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/claims"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

// discoverFor resumes the caller's atproto session and re-scans their
// dev.keytrace.claim collection for every supported claim type (Steam and
// Discord alike) — shared by both SteamHandlers.Recheck and
// DiscordHandlers.Recheck, since claims.Discover itself isn't per-source.
func discoverFor(ctx context.Context, app *oauth.ClientApp, conn *sql.DB, verifier *keytrace.Verifier, resolver claims.SubjectResolver, reconciler db.Reconciler, did string) error {
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return err
	}
	user, err := db.GetUser(ctx, conn, did)
	if err != nil {
		return err
	}
	if user == nil || user.ActiveSessionID == "" {
		return fmt.Errorf("no active session for %s", did)
	}
	sess, err := app.ResumeSession(ctx, parsedDID, user.ActiveSessionID)
	if err != nil {
		return err
	}
	return claims.Discover(ctx, sess.APIClient(), verifier, resolver, conn, reconciler, did)
}
```

(add `"database/sql"` and `"github.com/bluesky-social/indigo/atproto/auth/oauth"` to the import block)

`internal/api/discord_handlers.go`:

```go
// internal/api/discord_handlers.go
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"

	"github.com/jphastings/game-status/internal/claims"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/jetstream"
	"github.com/jphastings/game-status/internal/keytrace"
)

type DiscordHandlers struct {
	App        *oauth.ClientApp
	Conn       *sql.DB
	Verifier   *keytrace.Verifier
	Resolver   claims.SubjectResolver
	Reconciler db.Reconciler
	Jetstream  *jetstream.Manager
}

func (h *DiscordHandlers) Recheck(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	if err := discoverFor(r.Context(), h.App, h.Conn, h.Verifier, h.Resolver, h.Reconciler, did); err != nil {
		http.Error(w, "recheck failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	restartJetstreamWatch(h.Jetstream, h.Conn)
	w.WriteHeader(http.StatusNoContent)
}

func (h *DiscordHandlers) SetEnabled(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	var body enableRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if body.Enabled {
		claim, err := db.GetClaim(r.Context(), h.Conn, did, db.DiscordSource)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if claim == nil {
			http.Error(w, "no verified discord claim — recheck first", http.StatusConflict)
			return
		}
	} else {
		if err := db.ClearSessionStart(r.Context(), h.Conn, did, db.DiscordSource); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	if err := db.SetEnabled(r.Context(), h.Conn, did, db.DiscordSource, body.Enabled); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.Reconciler.Reconcile(r.Context(), did, time.Now()); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	restartJetstreamWatch(h.Jetstream, h.Conn)
	w.WriteHeader(http.StatusNoContent)
}
```

(`enableRequest` already exists in `steam_handlers.go`, same package — reused, not redefined.)

- [ ] **Step 3: `internal/api/sync_handlers.go`**

```go
// internal/api/sync_handlers.go
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/jphastings/game-status/internal/db"
)

type SyncHandlers struct {
	Conn *sql.DB
}

type orderRequest struct {
	Order []string `json:"order"`
}

var validSources = map[string]bool{db.SteamSource: true, db.DiscordSource: true}

func (h *SyncHandlers) SetOrder(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	var body orderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	seen := map[string]bool{}
	for _, source := range body.Order {
		if !validSources[source] || seen[source] {
			http.Error(w, "invalid or duplicate source in order", http.StatusBadRequest)
			return
		}
		seen[source] = true
	}

	if err := db.SetSourceOrder(r.Context(), h.Conn, did, body.Order); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Write the one test worth writing here — order validation**

```go
// internal/api/sync_handlers_test.go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestSetOrder_RejectsUnknownSource(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	appdb.UpsertUser(context.Background(), conn, "did:plc:a")

	h := &SyncHandlers{Conn: conn}
	req := httptest.NewRequest(http.MethodPost, "/api/sync/order", strings.NewReader(`{"order":["steam","xbox"]}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetOrder(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSetOrder_PersistsValidOrder(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.SteamSource, true)
	appdb.SetEnabled(ctx, conn, "did:plc:a", appdb.DiscordSource, true)

	h := &SyncHandlers{Conn: conn}
	req := httptest.NewRequest(http.MethodPost, "/api/sync/order", strings.NewReader(`{"order":["discord","steam"]}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetOrder(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	sources, err := appdb.ListEnabledSourcesByPriority(ctx, conn, "did:plc:a")
	if err != nil || len(sources) != 2 || sources[0] != "discord" || sources[1] != "steam" {
		t.Fatalf("got %v, %v, want [discord steam]", sources, err)
	}
}
```

- [ ] **Step 5: Extend `/api/me`**

```go
// internal/api/me_handler.go
type meResponse struct {
	DID            string   `json:"did"`
	SteamSubject   *string  `json:"steamSubject,omitempty"`
	SteamDisplay   *string  `json:"steamDisplayName,omitempty"`
	SteamEnabled   bool     `json:"steamEnabled"`
	DiscordSubject *string  `json:"discordSubject,omitempty"`
	DiscordDisplay *string  `json:"discordDisplayName,omitempty"`
	DiscordEnabled bool     `json:"discordEnabled"`
	SourceOrder    []string `json:"sourceOrder"` // enabled AND disabled sources, priority order — frontend appends disabled ones after enabled
}
```

`Get` gains the mirrored Discord lookups (`db.GetClaim(ctx, conn, did, db.DiscordSource)`, `db.IsEnabled(..., db.DiscordSource)`), plus a full ordered list: query all `sync_prefs` rows for `did` ordered by `(enabled DESC, priority ASC)` — add `db.ListAllSourcesOrdered(ctx, conn, did) ([]string, error)` in `internal/db/sync_prefs.go` for this (a `SELECT source FROM sync_prefs WHERE did = ? ORDER BY enabled DESC, priority ASC`), since `ListEnabledSourcesByPriority` deliberately excludes disabled ones and the frontend needs both for the drag list.

- [ ] **Step 6: Wire `main.go`**

```go
// cmd/server/main.go — additions, in construction order
discordGateway, err := discord.NewGateway(cfg.DiscordBotToken, cfg.DiscordGuildID)
if err != nil {
	log.Fatalf("discord gateway: %v", err)
}
discordResolver := &discord.ClaimResolver{Members: discordGateway.Members}
gameIndex := discord.NewGameIndex()
if err := gameIndex.Refresh(context.Background()); err != nil {
	log.Fatalf("discord detectable games: %v", err)
}
go func() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		if err := gameIndex.Refresh(context.Background()); err != nil {
			slog.Error("discord detectable games refresh", "err", err)
		}
	}
}()

reconciler := &sync.Reconciler{Conn: conn, Resolver: cartridgeClient, Writer: writer}

presenceHandler := &discord.PresenceHandler{Conn: conn, Games: gameIndex, Reconciler: reconciler}
discordGateway.Session.AddHandler(presenceHandler.HandlePresenceUpdate)
discordGateway.OnJoin = func(discordID string) {
	discordGateway.SendDM(discordID, "Link your atmosphere account: https://keytrace.dev/add/discord, then check your sync settings at "+cfg.BaseURL)
}
discordGateway.OnLeave = func(discordID string) {
	if err := presenceHandler.HandleGuildMemberRemove(context.Background(), discordID); err != nil {
		slog.Error("discord member remove", "discord_id", discordID, "err", err)
	}
}
if err := discordGateway.Open(); err != nil {
	log.Fatalf("discord gateway open: %v", err)
}
defer discordGateway.Close()

discordHandlers := &api.DiscordHandlers{App: oauthApp, Conn: conn, Verifier: verifier, Resolver: discordResolver, Reconciler: reconciler, Jetstream: jetManager}
mux.HandleFunc("POST /api/discord/recheck", oauthHandlers.RequireAuth(discordHandlers.Recheck))
mux.HandleFunc("POST /api/discord/enabled", oauthHandlers.RequireAuth(discordHandlers.SetEnabled))

syncHandlers := &api.SyncHandlers{Conn: conn}
mux.HandleFunc("POST /api/sync/order", oauthHandlers.RequireAuth(syncHandlers.SetOrder))
```

Update the existing `steamHandlers := &api.SteamHandlers{...}` construction to include `Resolver: discordResolver, Reconciler: reconciler` (dropping `Deleter`, per Task 3). `SteamHandlers` gains the same `Resolver claims.SubjectResolver` field `DiscordHandlers` has; delete `SteamHandlers`' own `discoverFor` method entirely and have `SteamHandlers.Recheck` call the package-level `discoverFor` helper from Step 2 instead (identical body to `DiscordHandlers.Recheck` above, modulo the struct's field names — both now do exactly the same thing, which is correct: either row's "recheck" button re-scans every claim type). Update `claims.Discover`/`claims.RunSweep` calls (the daily sweep goroutine, and anywhere `Discover` was already called) to pass `discordResolver` and `reconciler` per Task 5's new signatures. Update `jetstream.DBStore{Conn: conn, Reconciler: reconciler}` (per Task 3) and add the Discord `SubjectResolver` to wherever `jetstream.HandleEvent` is constructed (Task 5).

- [ ] **Step 7: README**

Add `DISCORD_BOT_TOKEN`, `DISCORD_GUILD_ID`, `DISCORD_INVITE_URL` to the environment variable table, alongside a short paragraph mirroring the existing Steam section: what the tracking guild is for, and that it's intentionally empty/locked-down (link to the design doc rather than re-explaining the privacy rationale in full).

- [ ] **Step 8: Run everything, verify green**

Run: `go build ./... && go test ./... -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "api: wire Discord recheck/enable/order endpoints and main.go"
```

---

## Task 10: Frontend

**Files:**
- Modify: `web/src/api.ts`
- Modify: `web/src/main.ts`
- Modify: `web/src/style.css`
- Modify: `web/src/devmock.ts`

**Interfaces:**
- Consumes: the extended `/api/me` shape from Task 9.

No dedicated frontend test suite, matching the original design doc's own call ("one page with no real client logic beyond fetch calls — no dedicated test suite needed there"); verified through the dev-mock fixtures and manual browser QA, same as every prior frontend change this project has made.

- [ ] **Step 1: Extend `web/src/api.ts`'s `Me` type and add the two new API calls**

```ts
export interface Me {
  did: string
  steamSubject?: string
  steamDisplayName?: string
  steamEnabled: boolean
  discordSubject?: string
  discordDisplayName?: string
  discordEnabled: boolean
  sourceOrder: string[]
}

export async function recheckDiscordClaim(): Promise<void> {
  await fetch('/api/discord/recheck', { method: 'POST' })
}

export async function setDiscordEnabled(enabled: boolean): Promise<void> {
  const res = await fetch('/api/discord/enabled', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled }),
  })
  if (!res.ok) throw new Error(`setDiscordEnabled: ${res.status}`)
}

export async function setSourceOrder(order: string[]): Promise<void> {
  const res = await fetch('/api/sync/order', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ order }),
  })
  if (!res.ok) throw new Error(`setSourceOrder: ${res.status}`)
}
```

- [ ] **Step 2: Add a Discord icon constant and a second toggle row, drag-reorderable, in `web/src/main.ts`**

Mirror `STEAM_ICON`/the existing toggle row exactly, with a `DISCORD_ICON` (Streamline "Simple Icons" Discord mark, same inlining/recoloring convention as the Steam one), a `discordToggleSubtitle` mirroring `verifiedSyncHTML`'s pattern, and render both rows inside a `<div id="sources">` wrapper in `me.sourceOrder`'s order. Give each `.toggle-row` a `draggable="true"` attribute and `dragstart`/`dragover`/`drop` handlers that reorder the DOM nodes and call `setSourceOrder` with the new full order (enabled and disabled together — `SetOrder`'s validation only rejects unknown/duplicate sources, not disabled ones) on drop, then re-render from a fresh `/api/me` so the toggle states stay authoritative.

- [ ] **Step 3: Extend `web/src/devmock.ts`'s fixtures**

Add `discordSubject`/`discordDisplayName`/`discordEnabled`/`sourceOrder: ['steam', 'discord']` to each existing fixture's `me` object (matching each fixture's existing steam state — e.g. `'no-claim'` gets `discordEnabled: false, sourceOrder: ['steam', 'discord']` with no discord subject either), and add one new fixture, `'both-sources'`, with both steam and discord verified+enabled and `sourceOrder: ['discord', 'steam']`, to visually verify the reordering UI and the reconciler's priority behavior are legible together.

- [ ] **Step 4: Add the Discord toggle row's styles to `web/src/style.css`**

Reuse `.toggle-row`/`.toggle-label-title`/`.steam-icon` wholesale — add a `.discord-icon` rule identical to `.steam-icon`'s (`width: 1em; height: 1em; flex-shrink: 0;`), and a `.toggle-row[draggable="true"]` cursor/hover affordance (`cursor: grab;` and a subtle `background` shift on `:hover`, consistent with the existing dim/bright hue tokens rather than a new color).

- [ ] **Step 5: Manual browser QA**

Run the dev server (`npm run dev` in `web/`), visit with `?mock=both-sources`, verify: both toggle rows render, dragging reorders them and persists (reload keeps the new order via the fixture — note dev-mock fixtures are static, so this step is really validating the drag interaction's visual/DOM behavior, not a real persisted round-trip; that's covered by Task 9's `TestSetOrder_PersistsValidOrder` on the backend), the Discord icon renders at the correct size next to "Discord", and `?mock=no-claim` shows the "must link" copy in the Discord row's subtitle mirroring Steam's.

- [ ] **Step 6: Commit**

```bash
git add web/src
git commit -m "web: add Discord sync toggle and drag-to-reorder source priority"
```

---

## Open items (deployment-time, not implementation-time)

- The Discord bot token, guild ID, and invite link (JP is creating the tracking server and bot application per the design doc).
- Confirm discordgo's exact intent-enabling flow works against a real bot application in the Discord Developer Portal (Presence + Server Members intents toggled on) — everything in Tasks 6-8 is written against discordgo's documented/source-verified API surface but has not run against a live Discord connection.
