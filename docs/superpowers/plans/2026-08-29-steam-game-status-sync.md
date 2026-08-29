# Steam Game-Status Sync — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go service that lets an atproto user sign in, verify a Steam identity via a cryptographically-checked `dev.keytrace.claim`, and have their `games.gamesgamesgamesgames.actor.status` record kept live on their own PDS while they play, via a 5-minute Steam poll plus a real-time Jetstream feed that stops syncing the moment a claim is revoked.

**Architecture:** One Go binary (stdlib `net/http`, embedded static frontend, one SQLite file via `modernc.org/sqlite`) with two background goroutines: a 5-minute Steam sync ticker and a Jetstream listener. atproto OAuth/XRPC goes through `bluesky-social/indigo`. Frontend is a single Vite+TS page with no framework.

**Tech Stack:** Go (stdlib `net/http`, `database/sql`), `modernc.org/sqlite`, `github.com/bluesky-social/indigo` (OAuth, XRPC, identity resolution), `github.com/gorilla/websocket` (Jetstream), Vite + pnpm + TypeScript.

**Spec:** [`docs/superpowers/specs/2026-08-29-steam-game-status-sync-design.md`](../specs/2026-08-29-steam-game-status-sync-design.md) — read this first, this plan argues from it and doesn't repeat its rationale.

## Global Constraints

- Every write to the user's own `games.gamesgamesgamesgames.actor.status` record must be idempotent given the same upstream Steam response — no read-before-write against the PDS (spec: "Sync engine").
- The only local, non-idempotent state is `session_starts` (createdAt bookkeeping). No other table mirrors PDS content.
- Claim validity (`steam_claims` presence) is decoupled from user intent (`sync_prefs.steam_enabled`) — never let one silently overwrite the other.
- A `dev.keytrace.claim`'s `status == "verified"` is trusted only after its `attest:*` signature cryptographically verifies against a trusted keytrace signer (this plan's addition beyond the original spec — see Task 3).
- Never hardcode cartridge.dev's own frontend's `x-client-key` value — it must come from a `CARTRIDGE_CLIENT_KEY` env var the operator supplies themselves (see Task 7's prerequisite note).
- No thumbnail on the `app.bsky.embed.external` embed in v1 (would need a per-user blob upload) — `uri`, `title`, `description` only.
- Jetstream watch-list changes are always make-before-break: open the new connection, confirm it's live, only then close the old one (spec: "Ongoing validity").

---

## Prerequisites (do these before Task 1, or the app won't run end to end)

1. **A Steam Web API key** — https://steamcommunity.com/dev/apikey. Env var `STEAM_API_KEY`.
2. **A cartridge.dev (`gamesgamesgamesgames.games`) client key.** The key baked into cartridge's own public frontend bundle (`hvc_5345b14eb88321fa86f93221753e483b`) is *not* ours to reuse — it identifies their frontend, not this app. Contact the cartridge/HappyView team for a key of our own, or find their self-serve registration if one exists. Env var `CARTRIDGE_CLIENT_KEY`. Task 7 is untestable end-to-end without this (its automated tests use a fake HTTP server and don't need a real key; only a live smoke-test does).
3. **A public HTTPS domain** this binary is reachable on, for the OAuth `client-metadata.json`/callback/JWKS URLs. Env var `BASE_URL` (e.g. `https://game-status.example.com`).
4. **An OAuth confidential-client P-256 key.** Generate with the `goat` CLI: `goat key generate -t P-256` (from `bluesky-social/goat`; `go install github.com/bluesky-social/goat@latest`). Take the multibase secret key output as `OAUTH_PRIVATE_KEY`.
5. **A session-cookie signing secret** — any 32 random bytes, hex-encoded: `openssl rand -hex 32`. Env var `SESSION_SECRET`.

---

### Task 1: Project scaffold, config, SQLite schema

**Files:**
- Create: `go.mod`, `go.sum`
- Create: `internal/config/config.go`
- Create: `internal/db/db.go`
- Create: `internal/db/migrations/0001_init.sql`
- Create: `internal/db/db_test.go`
- Create: `cmd/server/main.go`

**Interfaces:**
- Produces: `config.Config{ListenAddr, DBPath string}`, `config.Load() (*Config, error)`; `db.Open(path string) (*sql.DB, error)`.

- [ ] **Step 1: Initialize the module and add the SQLite driver**

```bash
go mod init github.com/jphastings/game-status
go get modernc.org/sqlite
```

- [ ] **Step 2: Write the full schema as one migration**

```sql
-- internal/db/migrations/0001_init.sql
CREATE TABLE IF NOT EXISTS users (
  did TEXT PRIMARY KEY,
  active_session_id TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS oauth_sessions (
  did TEXT NOT NULL,
  session_id TEXT NOT NULL,
  data BLOB NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (did, session_id)
);

CREATE TABLE IF NOT EXISTS oauth_auth_requests (
  state TEXT PRIMARY KEY,
  data BLOB NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS steam_claims (
  did TEXT PRIMARY KEY REFERENCES users(did),
  subject TEXT NOT NULL,
  display_name TEXT NOT NULL,
  claim_uri TEXT NOT NULL,
  record_uri TEXT NOT NULL,
  last_verified_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sync_prefs (
  did TEXT PRIMARY KEY REFERENCES users(did),
  steam_enabled INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS session_starts (
  did TEXT NOT NULL,
  source TEXT NOT NULL,
  game_key TEXT NOT NULL,
  started_at TEXT NOT NULL,
  PRIMARY KEY (did, source)
);

CREATE TABLE IF NOT EXISTS game_cache (
  steam_id TEXT PRIMARY KEY,
  game_uri TEXT NOT NULL,
  page_url TEXT NOT NULL,
  name TEXT NOT NULL,
  summary TEXT NOT NULL,
  cached_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS keytrace_key_cache (
  at_uri TEXT PRIMARY KEY,
  public_jwk TEXT NOT NULL,
  valid_from TEXT NOT NULL,
  valid_until TEXT NOT NULL,
  cached_at TEXT NOT NULL
);
```

- [ ] **Step 3: Write the failing test for `db.Open`**

```go
// internal/db/db_test.go
package db

import (
	"path/filepath"
	"testing"
)

func TestOpen_AppliesSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	conn, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	tables := []string{"users", "oauth_sessions", "oauth_auth_requests", "steam_claims", "sync_prefs", "session_starts", "game_cache", "keytrace_key_cache"}
	for _, table := range tables {
		var name string
		if err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}
}

func TestOpen_IdempotentAcrossRestarts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if _, err := Open(dbPath); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := Open(dbPath); err != nil {
		t.Fatalf("second Open: %v", err)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/db/...`
Expected: FAIL — `Open` undefined.

- [ ] **Step 4: Implement `db.Open`**

```go
// internal/db/db.go
package db

import (
	"database/sql"
	"embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Open(path string) (*sql.DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	schema, err := migrationsFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(string(schema)); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return conn, nil
}
```

- [ ] **Step 5: Run the test again to confirm it passes**

Run: `go test ./internal/db/...`
Expected: PASS

- [ ] **Step 6: Config loader and main.go with a health check**

```go
// internal/config/config.go
package config

import (
	"fmt"
	"os"
)

type Config struct {
	ListenAddr string
	DBPath     string
}

func Load() (*Config, error) {
	return &Config{
		ListenAddr: envOr("LISTEN_ADDR", ":8080"),
		DBPath:     envOr("DB_PATH", "game-status.db"),
	}, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return v, nil
}
```

```go
// cmd/server/main.go
package main

import (
	"log"
	"log/slog"
	"net/http"

	"github.com/jphastings/game-status/internal/config"
	"github.com/jphastings/game-status/internal/db"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	conn, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer conn.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	slog.Info("listening", "addr", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
```

- [ ] **Step 7: Build and commit**

```bash
go build ./...
git add -A
git commit -m "feat: project scaffold, config, sqlite schema"
```

Note: `requireEnv` is unused until Task 4 — Go will fail the build on an unused *variable*, not an unused *function*, so this compiles fine as-is; later tasks call it.

---

### Task 2: Data access layer

**Files:**
- Create: `internal/db/users.go`, `internal/db/sync_prefs.go`, `internal/db/session_starts.go`, `internal/db/game_cache.go`, `internal/db/steam_claims.go`
- Create: `internal/db/users_test.go` (and one test file per other new file, same pattern)

**Interfaces:**
- Consumes: `db.Open` (Task 1).
- Produces: `db.User`, `db.UpsertUser`, `db.SetActiveSession`, `db.GetUser`, `db.ListSteamEnabledDIDs`; `db.SetSteamEnabled`, `db.IsSteamEnabled`; `db.SessionStart`, `db.GetSessionStart`, `db.SetSessionStart`, `db.ClearSessionStart`; `db.CachedGame`, `db.GetCachedGame`, `db.SetCachedGame`; `db.SteamClaim`, `db.UpsertSteamClaim`, `db.GetSteamClaim`, `db.InvalidateSteamClaim`.

- [ ] **Step 1: Write the failing tests** (one representative file shown; repeat the same table-driven-per-function pattern for the other four files)

```go
// internal/db/users_test.go
package db

import (
	"context"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestUpsertUser_CreatesThenLeavesExisting(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	if err := UpsertUser(ctx, conn, "did:plc:abc"); err != nil {
		t.Fatalf("first UpsertUser: %v", err)
	}
	if err := SetActiveSession(ctx, conn, "did:plc:abc", "sess-1"); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	if err := UpsertUser(ctx, conn, "did:plc:abc"); err != nil { // re-login shouldn't clobber the session
		t.Fatalf("second UpsertUser: %v", err)
	}

	u, err := GetUser(ctx, conn, "did:plc:abc")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u == nil || u.ActiveSessionID != "sess-1" {
		t.Fatalf("got %+v, want ActiveSessionID=sess-1", u)
	}
}

func TestGetUser_MissingReturnsNil(t *testing.T) {
	conn := openTestDB(t)
	u, err := GetUser(context.Background(), conn, "did:plc:nobody")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u != nil {
		t.Fatalf("got %+v, want nil", u)
	}
}

func TestListSteamEnabledDIDs_RequiresBothEnabledAndClaimed(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)

	for _, did := range []string{"did:plc:a", "did:plc:b", "did:plc:c"} {
		mustUpsertUser(t, conn, did)
	}
	mustSetSteamEnabled(t, conn, "did:plc:a", true) // enabled, no claim
	mustSetSteamEnabled(t, conn, "did:plc:b", true)
	mustUpsertSteamClaim(t, conn, "did:plc:b") // enabled AND claimed
	mustUpsertSteamClaim(t, conn, "did:plc:c") // claimed, not enabled

	dids, err := ListSteamEnabledDIDs(ctx, conn)
	if err != nil {
		t.Fatalf("ListSteamEnabledDIDs: %v", err)
	}
	if len(dids) != 1 || dids[0] != "did:plc:b" {
		t.Fatalf("got %v, want [did:plc:b]", dids)
	}
}

// Test helpers below are used across this task's test files.

func mustUpsertUser(t *testing.T, conn *sql.DB, did string) {
	t.Helper()
	if err := UpsertUser(context.Background(), conn, did); err != nil {
		t.Fatalf("UpsertUser(%s): %v", did, err)
	}
}

func mustSetSteamEnabled(t *testing.T, conn *sql.DB, did string, enabled bool) {
	t.Helper()
	if err := SetSteamEnabled(context.Background(), conn, did, enabled); err != nil {
		t.Fatalf("SetSteamEnabled(%s): %v", did, err)
	}
}

func mustUpsertSteamClaim(t *testing.T, conn *sql.DB, did string) {
	t.Helper()
	err := UpsertSteamClaim(context.Background(), conn, SteamClaim{
		DID: did, Subject: "76500000000000000", DisplayName: "Test",
		ClaimURI: "https://steamcommunity.com/profiles/76500000000000000",
		RecordURI: "at://" + did + "/dev.keytrace.claim/abc", LastVerifiedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("UpsertSteamClaim(%s): %v", did, err)
	}
}
```

Add `"database/sql"` and `"time"` imports to `users_test.go` (used by the helpers).

- [ ] **Step 2: Confirm the tests fail** — `go test ./internal/db/...` (undefined functions/types).

- [ ] **Step 3: Implement all five files**

```go
// internal/db/users.go
package db

import (
	"context"
	"database/sql"
	"time"
)

type User struct {
	DID             string
	ActiveSessionID string
	CreatedAt       time.Time
}

func UpsertUser(ctx context.Context, conn *sql.DB, did string) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO users (did, created_at) VALUES (?, ?)
		ON CONFLICT(did) DO NOTHING
	`, did, time.Now().UTC().Format(time.RFC3339))
	return err
}

func SetActiveSession(ctx context.Context, conn *sql.DB, did, sessionID string) error {
	_, err := conn.ExecContext(ctx, `UPDATE users SET active_session_id = ? WHERE did = ?`, sessionID, did)
	return err
}

func GetUser(ctx context.Context, conn *sql.DB, did string) (*User, error) {
	var u User
	u.DID = did
	var createdAt string
	var activeSessionID sql.NullString
	err := conn.QueryRowContext(ctx, `SELECT active_session_id, created_at FROM users WHERE did = ?`, did).
		Scan(&activeSessionID, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.ActiveSessionID = activeSessionID.String
	if u.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// ListSteamEnabledDIDs returns DIDs eligible to sync right now: user intent
// (sync_prefs) AND claim validity (steam_claims) both hold. See Global
// Constraints — these two are intentionally never merged into one flag.
func ListSteamEnabledDIDs(ctx context.Context, conn *sql.DB) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT sp.did FROM sync_prefs sp
		JOIN steam_claims sc ON sc.did = sp.did
		WHERE sp.steam_enabled = 1
	`)
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
```

```go
// internal/db/sync_prefs.go
package db

import (
	"context"
	"database/sql"
)

func SetSteamEnabled(ctx context.Context, conn *sql.DB, did string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := conn.ExecContext(ctx, `
		INSERT INTO sync_prefs (did, steam_enabled) VALUES (?, ?)
		ON CONFLICT(did) DO UPDATE SET steam_enabled = excluded.steam_enabled
	`, did, e)
	return err
}

func IsSteamEnabled(ctx context.Context, conn *sql.DB, did string) (bool, error) {
	var enabled int
	err := conn.QueryRowContext(ctx, `SELECT steam_enabled FROM sync_prefs WHERE did = ?`, did).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled == 1, nil
}
```

```go
// internal/db/session_starts.go
package db

import (
	"context"
	"database/sql"
	"time"
)

type SessionStart struct {
	GameKey   string
	StartedAt time.Time
}

func GetSessionStart(ctx context.Context, conn *sql.DB, did, source string) (*SessionStart, error) {
	var s SessionStart
	var startedAt string
	err := conn.QueryRowContext(ctx, `SELECT game_key, started_at FROM session_starts WHERE did = ? AND source = ?`, did, source).
		Scan(&s.GameKey, &startedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.StartedAt, err = time.Parse(time.RFC3339, startedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func SetSessionStart(ctx context.Context, conn *sql.DB, did, source, gameKey string, startedAt time.Time) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO session_starts (did, source, game_key, started_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(did, source) DO UPDATE SET game_key = excluded.game_key, started_at = excluded.started_at
	`, did, source, gameKey, startedAt.UTC().Format(time.RFC3339))
	return err
}

func ClearSessionStart(ctx context.Context, conn *sql.DB, did, source string) error {
	_, err := conn.ExecContext(ctx, `DELETE FROM session_starts WHERE did = ? AND source = ?`, did, source)
	return err
}
```

```go
// internal/db/game_cache.go
package db

import (
	"context"
	"database/sql"
	"time"
)

// CachedGame's URI is the games.gamesgamesgamesgames.game record's own
// AT-URI (goes in the status record's `game` field); PageURL is cartridge.dev's
// web page for the game (goes in the embed's external.uri) — deliberately
// separate fields, not interchangeable.
type CachedGame struct {
	URI     string
	PageURL string
	Name    string
	Summary string
}

const gameCacheTTL = 24 * time.Hour

func GetCachedGame(ctx context.Context, conn *sql.DB, steamID string) (*CachedGame, error) {
	var g CachedGame
	var cachedAt string
	err := conn.QueryRowContext(ctx, `SELECT game_uri, page_url, name, summary, cached_at FROM game_cache WHERE steam_id = ?`, steamID).
		Scan(&g.URI, &g.PageURL, &g.Name, &g.Summary, &cachedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	at, err := time.Parse(time.RFC3339, cachedAt)
	if err != nil {
		return nil, err
	}
	if time.Since(at) > gameCacheTTL {
		return nil, nil
	}
	return &g, nil
}

func SetCachedGame(ctx context.Context, conn *sql.DB, steamID string, g CachedGame) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO game_cache (steam_id, game_uri, page_url, name, summary, cached_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(steam_id) DO UPDATE SET game_uri = excluded.game_uri, page_url = excluded.page_url, name = excluded.name, summary = excluded.summary, cached_at = excluded.cached_at
	`, steamID, g.URI, g.PageURL, g.Name, g.Summary, time.Now().UTC().Format(time.RFC3339))
	return err
}
```

```go
// internal/db/steam_claims.go
package db

import (
	"context"
	"database/sql"
	"time"
)

type SteamClaim struct {
	DID            string
	Subject        string
	DisplayName    string
	ClaimURI       string
	RecordURI      string
	LastVerifiedAt time.Time
}

func UpsertSteamClaim(ctx context.Context, conn *sql.DB, c SteamClaim) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO steam_claims (did, subject, display_name, claim_uri, record_uri, last_verified_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(did) DO UPDATE SET
			subject = excluded.subject, display_name = excluded.display_name,
			claim_uri = excluded.claim_uri, record_uri = excluded.record_uri,
			last_verified_at = excluded.last_verified_at
	`, c.DID, c.Subject, c.DisplayName, c.ClaimURI, c.RecordURI, c.LastVerifiedAt.UTC().Format(time.RFC3339))
	return err
}

func GetSteamClaim(ctx context.Context, conn *sql.DB, did string) (*SteamClaim, error) {
	c := SteamClaim{DID: did}
	var lastVerifiedAt string
	err := conn.QueryRowContext(ctx, `SELECT subject, display_name, claim_uri, record_uri, last_verified_at FROM steam_claims WHERE did = ?`, did).
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

// InvalidateSteamClaim removes a revoked/retracted/deleted claim. It does NOT
// touch sync_prefs.steam_enabled — that's user intent, kept separate on
// purpose (Global Constraints) so the UI can say "enabled, but not valid"
// instead of silently flipping the toggle.
func InvalidateSteamClaim(ctx context.Context, conn *sql.DB, did string) error {
	_, err := conn.ExecContext(ctx, `DELETE FROM steam_claims WHERE did = ?`, did)
	return err
}
```

- [ ] **Step 4: Run all tests, confirm pass**

Run: `go test ./internal/db/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/db
git commit -m "feat: data access layer for users, prefs, sessions, caches"
```

---

### Task 3: Keytrace claim signature verification

This is the one deliberate addition beyond the original spec (see conversation: trusting a bare `status: "verified"` field on a record living in the *claimant's own* repo is spoofable — anyone can write anything to their own repo). This task ports keytrace's own reference algorithm from `orta/keytrace`'s `packages/claims/src/verify.ts`, with one intentional deviation: their `getPrimarySig` prefers the `attest:*` signature (which proves the original identity binding), and this task does the same — we are NOT attempting to re-verify `status` on every check (keytrace's own model doesn't re-sign status changes either; revocation is trusted from the plain field and from record deletion, exactly as upstream). What this closes is forgery: nobody can fabricate a claim record naming someone else's SteamID without keytrace's private key having actually signed exactly that `(did, type, identity.subject, claimUri)` tuple.

**Files:**
- Create: `internal/keytrace/types.go`, `internal/keytrace/canonicalize.go`, `internal/keytrace/verify.go`, `internal/keytrace/verifier.go`, `internal/keytrace/keyfetch.go`
- Create: `internal/db/keytrace_keys.go`
- Create: `internal/keytrace/canonicalize_test.go`, `internal/keytrace/verify_test.go` (uses a **real** captured claim + signing key as a golden vector — see below)
- Create: `internal/db/keytrace_keys_test.go`

**Interfaces:**
- Consumes: `db.Open` (Task 1).
- Produces: `keytrace.Claim`, `keytrace.ClaimSignature`, `keytrace.ClaimIdentity`; `keytrace.KeyFetcher` interface; `keytrace.Verifier{Keys KeyFetcher, TrustedDIDs map[string]bool}` with `(*Verifier) VerifyAttestation(ctx context.Context, did string, claim Claim) (bool, error)`; `keytrace.CachedKeyFetcher{Dir identity.Directory, Conn *sql.DB}` (implements `KeyFetcher`); `db.KeytraceKey`, `db.GetKeytraceKey`, `db.SetKeytraceKey`. `Verifier.TrustedDIDs` is resolved once at startup in Task 5 (where a real `Verifier` first gets constructed) — this task's tests use fakes and need no network access.

- [ ] **Step 1: Write the record types**

```go
// internal/keytrace/types.go
package keytrace

import "strings"

const ClaimCollection = "dev.keytrace.claim"
const ServerKeyCollection = "dev.keytrace.serverPublicKey"

// DefaultTrustedSignerHandles mirrors keytrace's own reference library default.
var DefaultTrustedSignerHandles = []string{"keytrace.dev"}

type ClaimSignature struct {
	Kid          string   `json:"kid"`
	Src          string   `json:"src"`
	SignedAt     string   `json:"signedAt"`
	Attestation  string   `json:"attestation"`
	SignedFields []string `json:"signedFields"`
}

type ClaimIdentity struct {
	Subject     string `json:"subject"`
	DisplayName string `json:"displayName,omitempty"`
	ProfileURL  string `json:"profileUrl,omitempty"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	Account     string `json:"account,omitempty"`
}

type Claim struct {
	Type           string           `json:"type"`
	Status         string           `json:"status"`
	ClaimURI       string           `json:"claimUri"`
	Identity       ClaimIdentity    `json:"identity"`
	Sigs           []ClaimSignature `json:"sigs"`
	CreatedAt      string           `json:"createdAt"`
	LastVerifiedAt string           `json:"lastVerifiedAt"`
}

// primarySig mirrors keytrace's getPrimarySig: prefer the identity-attestation
// signature, fall back to the first signature present.
func primarySig(c Claim) (ClaimSignature, bool) {
	for _, s := range c.Sigs {
		if strings.HasPrefix(s.Kid, "attest:") {
			return s, true
		}
	}
	if len(c.Sigs) > 0 {
		return c.Sigs[0], true
	}
	return ClaimSignature{}, false
}
```

- [ ] **Step 2: RFC 8785 canonicalization for the flat string-map case we need**

```go
// internal/keytrace/canonicalize.go
package keytrace

import (
	"encoding/json"
	"sort"
	"strings"
)

// canonicalizeStringMap reproduces keytrace's RFC 8785 canonicalization for
// the one shape a SignedClaimData payload ever takes: a flat map of string
// keys to string values. Keys are sorted by UTF-16 code unit order; every key
// this package uses is plain ASCII, where Go's default string ordering,
// UTF-16 code unit order, and Unicode code point order all agree.
func canonicalizeStringMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(m[k])
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.String()
}
```

```go
// internal/keytrace/canonicalize_test.go
package keytrace

import "testing"

func TestCanonicalizeStringMap_SortsKeysAndEscapes(t *testing.T) {
	got := canonicalizeStringMap(map[string]string{
		"type":     "steam",
		"claimUri": "https://example.com/a\"b",
		"did":      "did:plc:abc",
	})
	want := `{"claimUri":"https://example.com/a\"b","did":"did:plc:abc","type":"steam"}`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
```

- [ ] **Step 3: ES256 JWS verification (raw R\|\|S signature, ECDSA P-256/SHA-256)**

```go
// internal/keytrace/verify.go
package keytrace

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

type es256PublicJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func parsePublicJWK(raw string) (*ecdsa.PublicKey, error) {
	var jwk es256PublicJWK
	if err := json.Unmarshal([]byte(raw), &jwk); err != nil {
		return nil, fmt.Errorf("parse jwk: %w", err)
	}
	if jwk.Kty != "EC" || jwk.Crv != "P-256" {
		return nil, fmt.Errorf("unsupported key type %s/%s", jwk.Kty, jwk.Crv)
	}
	x, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		return nil, fmt.Errorf("decode x: %w", err)
	}
	y, err := base64.RawURLEncoding.DecodeString(jwk.Y)
	if err != nil {
		return nil, fmt.Errorf("decode y: %w", err)
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
}

// verifyJWS checks a compact JWS (header.payload.signature) was signed by pub
// over exactly the canonical form of claimData — mirrors keytrace's
// crypto/signature.ts verifyES256Signature.
func verifyJWS(claimData map[string]string, jws string, pub *ecdsa.PublicKey) (bool, error) {
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		return false, fmt.Errorf("malformed JWS: expected 3 parts, got %d", len(parts))
	}
	headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]

	expectedPayload := canonicalizeStringMap(claimData)
	actualPayload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return false, fmt.Errorf("decode payload: %w", err)
	}
	if string(actualPayload) != expectedPayload {
		return false, nil
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return false, fmt.Errorf("decode signature: %w", err)
	}
	if len(sigBytes) != 64 {
		return false, fmt.Errorf("unexpected signature length %d, want 64", len(sigBytes))
	}
	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:])

	hash := sha256.Sum256([]byte(headerB64 + "." + payloadB64))
	return ecdsa.Verify(pub, hash[:], r, s), nil
}
```

- [ ] **Step 4: The verifier — resolve signed fields, reconstruct the payload, verify**

```go
// internal/keytrace/verifier.go
package keytrace

import (
	"context"
	"fmt"
)

// KeyFetcher resolves a keytrace signing-key AT-URI (sig.src) to the raw
// publicJwk JSON string it contains.
type KeyFetcher interface {
	FetchPublicJWK(ctx context.Context, keyURI string) (string, error)
}

type Verifier struct {
	Keys        KeyFetcher
	TrustedDIDs map[string]bool
}

// VerifyAttestation checks that the identity binding on a claim (did, type,
// identity.subject, claimUri) was genuinely attested by a trusted keytrace
// signer. See this task's header note for why only the attest:* signature is
// checked, not `status`.
func (v *Verifier) VerifyAttestation(ctx context.Context, did string, claim Claim) (bool, error) {
	sig, ok := primarySig(claim)
	if !ok || sig.Src == "" || sig.Attestation == "" || sig.SignedAt == "" {
		return false, nil
	}

	signerDID, ok := didFromAtURI(sig.Src)
	if !ok || !v.TrustedDIDs[signerDID] {
		return false, nil
	}

	rawJWK, err := v.Keys.FetchPublicJWK(ctx, sig.Src)
	if err != nil {
		return false, fmt.Errorf("fetch signing key: %w", err)
	}
	pub, err := parsePublicJWK(rawJWK)
	if err != nil {
		return false, nil // malformed key -> bad claim, not a transient error
	}

	signedData, ok := reconstructSignedData(did, claim, sig)
	if !ok {
		return false, nil
	}
	return verifyJWS(signedData, sig.Attestation, pub)
}

func reconstructSignedData(did string, claim Claim, sig ClaimSignature) (map[string]string, bool) {
	isNewFormat := containsString(sig.SignedFields, "identity.subject")
	if !isNewFormat {
		// Legacy format: { did, subject, type, verifiedAt }
		return map[string]string{"did": did, "subject": claim.Identity.Subject, "type": claim.Type, "verifiedAt": sig.SignedAt}, true
	}

	values := map[string]string{
		"claimUri":         claim.ClaimURI,
		"createdAt":        sig.SignedAt, // signed at attestation time, NOT claim.CreatedAt
		"did":              did,
		"identity.subject": claim.Identity.Subject,
		"type":             claim.Type,
	}
	if claim.Identity.Account != "" {
		values["identity.account"] = claim.Identity.Account
	}

	signed := make(map[string]string, len(sig.SignedFields))
	for _, field := range sig.SignedFields {
		val, ok := values[field]
		if !ok {
			return nil, false // signer covered a field we can't reconstruct -> fail closed
		}
		signed[field] = val
	}
	return signed, true
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func didFromAtURI(atURI string) (string, bool) {
	const prefix = "at://"
	if len(atURI) <= len(prefix) || atURI[:len(prefix)] != prefix {
		return "", false
	}
	rest := atURI[len(prefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			return rest[:i], true
		}
	}
	return "", false
}
```

- [ ] **Step 5: Golden-vector test using a real captured claim + real signing key**

These values were fetched live during planning from `at://did:plc:ephkzpinhaqcabtkugtbzrwu/dev.keytrace.claim/3mkwoifsquv2p` and its signer's `at://did:plc:hcwfdlmprcc335oixyfsw7u3/dev.keytrace.serverPublicKey/2026-05-03` — a genuine keytrace-issued signature, not synthetic data.

```go
// internal/keytrace/verify_test.go
package keytrace

import (
	"context"
	"testing"
)

const realClaimDID = "did:plc:ephkzpinhaqcabtkugtbzrwu"
const realSignerDID = "did:plc:hcwfdlmprcc335oixyfsw7u3"
const realKeyJWK = `{"kty":"EC","x":"pretZ8lN1snAV4dNoyet54BTTs1-Mxv4-jNuVGazf8g","y":"wUT9JvxuvkRtPrufb6c4BPXoA60LhmvfaE_aH5d6A-o","crv":"P-256"}`

var realClaim = Claim{
	Type:     "steam",
	Status:   "verified",
	ClaimURI: "https://steamcommunity.com/profiles/76561197994000231",
	Identity: ClaimIdentity{Subject: "76561197994000231"},
	Sigs: []ClaimSignature{{
		Kid:          "attest:steam",
		Src:          "at://" + realSignerDID + "/dev.keytrace.serverPublicKey/2026-05-03",
		SignedAt:     "2026-05-03T07:53:39.639Z",
		Attestation:  "eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJjbGFpbVVyaSI6Imh0dHBzOi8vc3RlYW1jb21tdW5pdHkuY29tL3Byb2ZpbGVzLzc2NTYxMTk3OTk0MDAwMjMxIiwiZGlkIjoiZGlkOnBsYzplcGhrenBpbmhhcWNhYnRrdWd0Ynpyd3UiLCJpZGVudGl0eS5zdWJqZWN0IjoiNzY1NjExOTc5OTQwMDAyMzEiLCJ0eXBlIjoic3RlYW0ifQ.69NGYohaBKTQFtWoRPrqeIOIZN72Q7eEhESF2EPaQLRUfnFioQ3vtGWHsmSSEO5m8_7vd6UU347AlwcafaBBGA",
		SignedFields: []string{"claimUri", "did", "identity.subject", "type"},
	}},
}

type fakeKeyFetcher struct{ jwk string }

func (f fakeKeyFetcher) FetchPublicJWK(ctx context.Context, keyURI string) (string, error) {
	return f.jwk, nil
}

func TestVerifyAttestation_RealKeytraceClaim(t *testing.T) {
	v := &Verifier{Keys: fakeKeyFetcher{jwk: realKeyJWK}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	ok, err := v.VerifyAttestation(context.Background(), realClaimDID, realClaim)
	if err != nil {
		t.Fatalf("VerifyAttestation: %v", err)
	}
	if !ok {
		t.Fatal("expected the real captured signature to verify, it did not")
	}
}

func TestVerifyAttestation_RejectsUntrustedSigner(t *testing.T) {
	v := &Verifier{Keys: fakeKeyFetcher{jwk: realKeyJWK}, TrustedDIDs: map[string]bool{"did:plc:someone-else": true}}
	ok, err := v.VerifyAttestation(context.Background(), realClaimDID, realClaim)
	if err != nil {
		t.Fatalf("VerifyAttestation: %v", err)
	}
	if ok {
		t.Fatal("expected verification to fail for an untrusted signer, it passed")
	}
}

func TestVerifyAttestation_RejectsSubstitutedSubject(t *testing.T) {
	// The spoofing scenario this task exists to close: an attacker with a
	// genuinely different, unrelated claim can't just paste in someone else's
	// SteamID — the signature covers identity.subject, so tampering breaks it.
	tampered := realClaim
	tampered.Identity = ClaimIdentity{Subject: "1"}

	v := &Verifier{Keys: fakeKeyFetcher{jwk: realKeyJWK}, TrustedDIDs: map[string]bool{realSignerDID: true}}
	ok, err := v.VerifyAttestation(context.Background(), realClaimDID, tampered)
	if err != nil {
		t.Fatalf("VerifyAttestation: %v", err)
	}
	if ok {
		t.Fatal("expected verification to fail for a substituted identity.subject, it passed")
	}
}
```

- [ ] **Step 6: Confirm the golden-vector tests fail, then pass**

Run: `go test ./internal/keytrace/...`
Expected first: FAIL (`Verifier`/`Claim` etc. undefined, since only types.go/canonicalize.go exist until this step's other files are added — if implementing in the order shown above, this step is really "confirm PASS", since steps 1-4 already added the implementation before the test file). Run again after Step 5: PASS.

- [ ] **Step 7: The real key fetcher (indigo + SQLite cache)**

```go
// internal/db/keytrace_keys.go
package db

import (
	"context"
	"database/sql"
	"time"
)

type KeytraceKey struct {
	AtURI      string
	PublicJWK  string
	ValidFrom  string
	ValidUntil string
}

// GetKeytraceKey / SetKeytraceKey cache forever (no TTL): the record at a
// given AT-URI is immutable once published, and keytrace rotates to a *new*
// URI daily rather than mutating an old one.
func GetKeytraceKey(ctx context.Context, conn *sql.DB, atURI string) (*KeytraceKey, error) {
	k := KeytraceKey{AtURI: atURI}
	err := conn.QueryRowContext(ctx, `SELECT public_jwk, valid_from, valid_until FROM keytrace_key_cache WHERE at_uri = ?`, atURI).
		Scan(&k.PublicJWK, &k.ValidFrom, &k.ValidUntil)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func SetKeytraceKey(ctx context.Context, conn *sql.DB, k KeytraceKey) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO keytrace_key_cache (at_uri, public_jwk, valid_from, valid_until, cached_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(at_uri) DO NOTHING
	`, k.AtURI, k.PublicJWK, k.ValidFrom, k.ValidUntil, time.Now().UTC().Format(time.RFC3339))
	return err
}
```

```go
// internal/keytrace/keyfetch.go
package keytrace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bluesky-social/indigo/api/agnostic"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"

	appdb "github.com/jphastings/game-status/internal/db"
)

type CachedKeyFetcher struct {
	Dir  identity.Directory
	Conn *sql.DB
}

var _ KeyFetcher = (*CachedKeyFetcher)(nil)

type serverKeyRecord struct {
	PublicJWK string `json:"publicJwk"`
}

func (f *CachedKeyFetcher) FetchPublicJWK(ctx context.Context, keyURI string) (string, error) {
	if cached, err := appdb.GetKeytraceKey(ctx, f.Conn, keyURI); err != nil {
		return "", err
	} else if cached != nil {
		return cached.PublicJWK, nil
	}

	did, collection, rkey, ok := parseAtURI(keyURI)
	if !ok {
		return "", fmt.Errorf("invalid key at-uri: %s", keyURI)
	}

	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return "", fmt.Errorf("parse did: %w", err)
	}
	ident, err := f.Dir.LookupDID(ctx, parsedDID)
	if err != nil {
		return "", fmt.Errorf("resolve signer did: %w", err)
	}

	client := atclient.NewAPIClient(ident.PDSEndpoint())
	resp, err := agnostic.RepoGetRecord(ctx, client, "", collection, did, rkey)
	if err != nil {
		return "", fmt.Errorf("fetch key record: %w", err)
	}

	var rec serverKeyRecord
	if err := json.Unmarshal(*resp.Value, &rec); err != nil {
		return "", fmt.Errorf("parse key record: %w", err)
	}

	if err := appdb.SetKeytraceKey(ctx, f.Conn, appdb.KeytraceKey{AtURI: keyURI, PublicJWK: rec.PublicJWK}); err != nil {
		return "", err
	}
	return rec.PublicJWK, nil
}

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

- [ ] **Step 8: `go get` the new indigo dependency, build, test, commit**

```bash
go get github.com/bluesky-social/indigo/api/agnostic
go build ./...
go test ./internal/keytrace/... ./internal/db/...
git add internal/keytrace internal/db go.mod go.sum
git commit -m "feat: verify keytrace claim attestation signatures"
```

---

### Task 4: atproto OAuth sign-in

Confidential-client OAuth via indigo's `atproto/auth/oauth`. `ClientMetadata()` does **not** auto-embed the JWKS (confirmed from indigo source — it's a documented TODO there); a confidential client must serve a separate `/oauth/jwks.json` and set `JWKSURI` itself. This task follows indigo's own `cmd/oauth-web-demo` reference exactly for that wiring.

**Files:**
- Modify: `internal/config/config.go` (add `BaseURL`, `SessionSecret`, `OAuthPrivateKeyMultibase`, `OAuthKeyID`)
- Create: `internal/webauth/cookie.go`, `internal/webauth/cookie_test.go`
- Create: `internal/authstore/store.go`, `internal/authstore/store_test.go`
- Create: `internal/api/oauth_handlers.go`, `internal/api/middleware.go`
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `db.Open`, `db.UpsertUser`, `db.SetActiveSession` (Tasks 1-2).
- Produces: `webauth.SignedCookies{Secret []byte}` with `Encode(did string) string`, `Decode(value string) (string, error)`; `authstore.SQLiteStore{Conn *sql.DB}` (implements `oauth.ClientAuthStore`); `api.OAuthHandlers{App *oauth.ClientApp, Conn *sql.DB, Cookies webauth.SignedCookies}` with `ClientMetadata`, `JWKS`, `Login`, `Callback` (all `http.HandlerFunc`); `api.DIDFromContext(ctx) (string, bool)`; `(*OAuthHandlers) RequireAuth(next http.HandlerFunc) http.HandlerFunc`.

- [ ] **Step 1: Extend config**

```go
// add to internal/config/config.go's Config struct
	BaseURL                  string
	SessionSecret            []byte
	OAuthPrivateKeyMultibase string
	OAuthKeyID               string
```

```go
// add to Load(), before the final return
	baseURL, err := requireEnv("BASE_URL")
	if err != nil {
		return nil, err
	}
	cfg.BaseURL = baseURL

	secretHex, err := requireEnv("SESSION_SECRET")
	if err != nil {
		return nil, err
	}
	secret, err := hex.DecodeString(secretHex)
	if err != nil {
		return nil, fmt.Errorf("SESSION_SECRET must be hex-encoded: %w", err)
	}
	cfg.SessionSecret = secret

	oauthKey, err := requireEnv("OAUTH_PRIVATE_KEY")
	if err != nil {
		return nil, err
	}
	cfg.OAuthPrivateKeyMultibase = oauthKey
	cfg.OAuthKeyID = envOr("OAUTH_KEY_ID", "1")
```

Add `"encoding/hex"` to the import block. `Config` must be built as a local var (`cfg := &Config{...}`) before these steps run, since they set fields on it — restructure `Load()` accordingly.

- [ ] **Step 2: Write the failing cookie tests**

```go
// internal/webauth/cookie_test.go
package webauth

import "testing"

func TestEncodeDecode_RoundTrips(t *testing.T) {
	c := SignedCookies{Secret: []byte("test-secret")}
	token := c.Encode("did:plc:abc")
	did, err := c.Decode(token)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if did != "did:plc:abc" {
		t.Fatalf("got %s, want did:plc:abc", did)
	}
}

func TestDecode_RejectsTamperedSignature(t *testing.T) {
	c := SignedCookies{Secret: []byte("test-secret")}
	token := c.Encode("did:plc:abc")
	tampered := token[:len(token)-1] + "0"
	if _, err := c.Decode(tampered); err == nil {
		t.Fatal("expected error for tampered cookie, got nil")
	}
}

func TestDecode_RejectsWrongSecret(t *testing.T) {
	a := SignedCookies{Secret: []byte("secret-a")}
	b := SignedCookies{Secret: []byte("secret-b")}
	token := a.Encode("did:plc:abc")
	if _, err := b.Decode(token); err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}
```

- [ ] **Step 3: Confirm it fails, then implement**

Run: `go test ./internal/webauth/...` — FAIL (`SignedCookies` undefined).

```go
// internal/webauth/cookie.go
package webauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const sessionTTL = 30 * 24 * time.Hour

type SignedCookies struct {
	Secret []byte
}

func (s SignedCookies) Encode(did string) string {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(did)) + "." + strconv.FormatInt(exp, 10)
	sig := s.sign(payload)
	return payload + "." + sig
}

func (s SignedCookies) Decode(value string) (string, error) {
	parts := strings.SplitN(value, ".", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed session cookie")
	}
	didB64, expStr, sig := parts[0], parts[1], parts[2]
	payload := didB64 + "." + expStr

	if !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return "", fmt.Errorf("invalid session signature")
	}

	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", fmt.Errorf("malformed expiry")
	}
	if time.Now().Unix() > exp {
		return "", fmt.Errorf("session expired")
	}

	didBytes, err := base64.RawURLEncoding.DecodeString(didB64)
	if err != nil {
		return "", fmt.Errorf("malformed did")
	}
	return string(didBytes), nil
}

func (s SignedCookies) sign(payload string) string {
	mac := hmac.New(sha256.New, s.Secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
```

Run: `go test ./internal/webauth/...` — PASS.

- [ ] **Step 4: Write the failing `ClientAuthStore` tests**

```go
// internal/authstore/store_test.go
package authstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	appdb "github.com/jphastings/game-status/internal/db"
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

func TestSQLiteStore_SessionRoundTrip(t *testing.T) {
	store := &SQLiteStore{Conn: openTestDB(t)}
	did := syntax.DID("did:plc:abc")

	in := oauth.ClientSessionData{AccountDID: did, SessionID: "sess-1", AccessToken: "at-1", RefreshToken: "rt-1"}
	if err := store.SaveSession(context.Background(), in); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	out, err := store.GetSession(context.Background(), did, "sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if out.AccessToken != "at-1" || out.RefreshToken != "rt-1" {
		t.Fatalf("got %+v, want AccessToken=at-1 RefreshToken=rt-1", out)
	}
}

func TestSQLiteStore_AuthRequestRoundTripAndDelete(t *testing.T) {
	store := &SQLiteStore{Conn: openTestDB(t)}

	in := oauth.AuthRequestData{State: "state-1", PKCEVerifier: "verifier-1"}
	if err := store.SaveAuthRequestInfo(context.Background(), in); err != nil {
		t.Fatalf("SaveAuthRequestInfo: %v", err)
	}

	out, err := store.GetAuthRequestInfo(context.Background(), "state-1")
	if err != nil {
		t.Fatalf("GetAuthRequestInfo: %v", err)
	}
	if out.PKCEVerifier != "verifier-1" {
		t.Fatalf("got %+v, want PKCEVerifier=verifier-1", out)
	}

	if err := store.DeleteAuthRequestInfo(context.Background(), "state-1"); err != nil {
		t.Fatalf("DeleteAuthRequestInfo: %v", err)
	}
	if _, err := store.GetAuthRequestInfo(context.Background(), "state-1"); err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}
```

- [ ] **Step 5: Implement the store**

```go
// internal/authstore/store.go
package authstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

type SQLiteStore struct {
	Conn *sql.DB
}

var _ oauth.ClientAuthStore = (*SQLiteStore)(nil)

func (s *SQLiteStore) GetSession(ctx context.Context, did syntax.DID, sessionID string) (*oauth.ClientSessionData, error) {
	var raw []byte
	err := s.Conn.QueryRowContext(ctx, `SELECT data FROM oauth_sessions WHERE did = ? AND session_id = ?`, did.String(), sessionID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no session for %s/%s", did, sessionID)
	}
	if err != nil {
		return nil, err
	}
	var sess oauth.ClientSessionData
	if err := json.Unmarshal(raw, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *SQLiteStore) SaveSession(ctx context.Context, sess oauth.ClientSessionData) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	_, err = s.Conn.ExecContext(ctx, `
		INSERT INTO oauth_sessions (did, session_id, data, updated_at) VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(did, session_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at
	`, sess.AccountDID.String(), sess.SessionID, raw)
	return err
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, did syntax.DID, sessionID string) error {
	_, err := s.Conn.ExecContext(ctx, `DELETE FROM oauth_sessions WHERE did = ? AND session_id = ?`, did.String(), sessionID)
	return err
}

func (s *SQLiteStore) GetAuthRequestInfo(ctx context.Context, state string) (*oauth.AuthRequestData, error) {
	var raw []byte
	err := s.Conn.QueryRowContext(ctx, `SELECT data FROM oauth_auth_requests WHERE state = ?`, state).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no auth request for state %s", state)
	}
	if err != nil {
		return nil, err
	}
	var info oauth.AuthRequestData
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (s *SQLiteStore) SaveAuthRequestInfo(ctx context.Context, info oauth.AuthRequestData) error {
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	_, err = s.Conn.ExecContext(ctx, `INSERT INTO oauth_auth_requests (state, data, created_at) VALUES (?, ?, datetime('now'))`, info.State, raw)
	return err
}

func (s *SQLiteStore) DeleteAuthRequestInfo(ctx context.Context, state string) error {
	_, err := s.Conn.ExecContext(ctx, `DELETE FROM oauth_auth_requests WHERE state = ?`, state)
	return err
}
```

Run: `go test ./internal/authstore/...` — PASS.

- [ ] **Step 6: OAuth HTTP handlers**

```go
// internal/api/oauth_handlers.go
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"

	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/webauth"
)

const sessionCookieName = "gs_session"

type OAuthHandlers struct {
	App     *oauth.ClientApp
	Conn    *sql.DB
	Cookies webauth.SignedCookies
}

func (h *OAuthHandlers) ClientMetadata(w http.ResponseWriter, r *http.Request) {
	meta := h.App.Config.ClientMetadata()
	if h.App.Config.IsConfidential() {
		jwksURI := "https://" + r.Host + "/oauth/jwks.json"
		meta.JWKSURI = &jwksURI
	}
	name := "Game Status Sync"
	meta.ClientName = &name
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

func (h *OAuthHandlers) JWKS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.App.Config.PublicJWKS())
}

func (h *OAuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	identifier := r.URL.Query().Get("handle")
	if identifier == "" {
		http.Error(w, "missing handle", http.StatusBadRequest)
		return
	}
	redirectURL, err := h.App.StartAuthFlow(r.Context(), identifier)
	if err != nil {
		http.Error(w, "could not start sign-in: "+err.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *OAuthHandlers) Callback(w http.ResponseWriter, r *http.Request) {
	sess, err := h.App.ProcessCallback(r.Context(), r.URL.Query())
	if err != nil {
		http.Error(w, "sign-in failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	did := sess.AccountDID.String()
	if err := db.UpsertUser(r.Context(), h.Conn, did); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := db.SetActiveSession(r.Context(), h.Conn, did, sess.SessionID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: h.Cookies.Encode(did), Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 60 * 60,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}
```

```go
// internal/api/middleware.go
package api

import "context"

type contextKey string

const didContextKey contextKey = "did"

func DIDFromContext(ctx context.Context) (string, bool) {
	did, ok := ctx.Value(didContextKey).(string)
	return did, ok
}

func (h *OAuthHandlers) RequireAuth(next func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		did, err := h.Cookies.Decode(cookie.Value)
		if err != nil {
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), didContextKey, did)))
	}
}
```

Add `"net/http"` to `middleware.go`'s imports.

- [ ] **Step 7: Wire into main.go**

```go
// add to cmd/server/main.go, replacing the bare mux setup
	priv, err := atcrypto.ParsePrivateMultibase(cfg.OAuthPrivateKeyMultibase)
	if err != nil {
		log.Fatalf("oauth key: %v", err)
	}
	oauthConfig := oauth.NewPublicConfig(cfg.BaseURL+"/oauth/client-metadata.json", cfg.BaseURL+"/oauth/callback",
		[]string{
			"atproto",
			"repo:games.gamesgamesgamesgames.actor.status?action=create",
			"repo:games.gamesgamesgamesgames.actor.status?action=update",
			"repo:games.gamesgamesgamesgames.actor.status?action=delete",
			// This granular repo-scope-per-action pattern is confirmed from indigo's
			// own oauth-web-demo for a single "?action=create" case; it's not confirmed
			// for "read" or for arbitrary third-party collections like keytrace's. If the
			// PDS rejects this scope list at authorization time, fall back to the single
			// broader "transition:generic" scope instead.
			"repo:dev.keytrace.claim?action=read",
		})
	if err := oauthConfig.SetClientSecret(priv, cfg.OAuthKeyID); err != nil {
		log.Fatalf("oauth client secret: %v", err)
	}
	oauthApp := oauth.NewClientApp(&oauthConfig, &authstore.SQLiteStore{Conn: conn})

	oauthHandlers := &api.OAuthHandlers{App: oauthApp, Conn: conn, Cookies: webauth.SignedCookies{Secret: cfg.SessionSecret}}

	mux.HandleFunc("GET /oauth/client-metadata.json", oauthHandlers.ClientMetadata)
	mux.HandleFunc("GET /oauth/jwks.json", oauthHandlers.JWKS)
	mux.HandleFunc("GET /login", oauthHandlers.Login)
	mux.HandleFunc("GET /oauth/callback", oauthHandlers.Callback)
```

Add imports: `"github.com/bluesky-social/indigo/atproto/atcrypto"`, `"github.com/bluesky-social/indigo/atproto/auth/oauth"`, `"github.com/jphastings/game-status/internal/api"`, `"github.com/jphastings/game-status/internal/authstore"`, `"github.com/jphastings/game-status/internal/webauth"`.

- [ ] **Step 8: Build, run the automated tests, commit**

```bash
go get github.com/bluesky-social/indigo/atproto/auth/oauth github.com/bluesky-social/indigo/atproto/atcrypto
go build ./...
go test ./...
git add -A
git commit -m "feat: atproto OAuth sign-in"
```

- [ ] **Step 9: Manual verification (not automatable — needs a real PDS OAuth server)**

With the prerequisites' env vars set and the binary reachable at `BASE_URL`: visit `/login?handle=<your-handle>`, confirm it redirects to your PDS's real consent screen, approve, and confirm you land back on `/` with a `gs_session` cookie set and a new row in `users`.

---

### Task 5: Claim discovery

**Files:**
- Create: `internal/claims/discover.go`, `internal/claims/discover_test.go`
- Create: `internal/api/steam_handlers.go`
- Modify: `cmd/server/main.go` (resolve trusted keytrace signer DIDs, build the real `Verifier`, wire the recheck route)

**Interfaces:**
- Consumes: `keytrace.Verifier`, `keytrace.Claim`, `keytrace.ClaimCollection` (Task 3); `db.UpsertSteamClaim`, `db.InvalidateSteamClaim`, `db.GetUser` (Task 2); `OAuthHandlers.RequireAuth`, `DIDFromContext` (Task 4).
- Produces: `claims.Discover(ctx context.Context, client lexutil.LexClient, verifier *keytrace.Verifier, conn *sql.DB, did string) error`; `api.SteamHandlers{App *oauth.ClientApp, Conn *sql.DB, Verifier *keytrace.Verifier}` with `Recheck` (`http.HandlerFunc`).

- [ ] **Step 1: Write the failing tests, using the real golden-vector claim again**

```go
// internal/claims/discover_test.go
package claims

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"database/sql"

	"github.com/bluesky-social/indigo/api/agnostic"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

const realClaimDID = "did:plc:ephkzpinhaqcabtkugtbzrwu"
const realSignerDID = "did:plc:hcwfdlmprcc335oixyfsw7u3"
const realKeyJWK = `{"kty":"EC","x":"pretZ8lN1snAV4dNoyet54BTTs1-Mxv4-jNuVGazf8g","y":"wUT9JvxuvkRtPrufb6c4BPXoA60LhmvfaE_aH5d6A-o","crv":"P-256"}`
const realClaimJSON = `{
	"type":"steam","status":"verified",
	"claimUri":"https://steamcommunity.com/profiles/76561197994000231",
	"identity":{"subject":"76561197994000231","displayName":"JP"},
	"sigs":[{
		"kid":"attest:steam",
		"src":"at://did:plc:hcwfdlmprcc335oixyfsw7u3/dev.keytrace.serverPublicKey/2026-05-03",
		"signedAt":"2026-05-03T07:53:39.639Z",
		"attestation":"eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJjbGFpbVVyaSI6Imh0dHBzOi8vc3RlYW1jb21tdW5pdHkuY29tL3Byb2ZpbGVzLzc2NTYxMTk3OTk0MDAwMjMxIiwiZGlkIjoiZGlkOnBsYzplcGhrenBpbmhhcWNhYnRrdWd0Ynpyd3UiLCJpZGVudGl0eS5zdWJqZWN0IjoiNzY1NjExOTc5OTQwMDAyMzEiLCJ0eXBlIjoic3RlYW0ifQ.69NGYohaBKTQFtWoRPrqeIOIZN72Q7eEhESF2EPaQLRUfnFioQ3vtGWHsmSSEO5m8_7vd6UU347AlwcafaBBGA",
		"signedFields":["claimUri","did","identity.subject","type"]
	}],
	"createdAt":"2026-05-03T07:53:39.698Z","lastVerifiedAt":"2026-05-03T07:53:39.698Z"
}`

type fakeKeyFetcher struct{}

func (fakeKeyFetcher) FetchPublicJWK(ctx context.Context, keyURI string) (string, error) {
	return realKeyJWK, nil
}

type fakeLexClient struct {
	records []*agnostic.RepoListRecords_Record
}

func (f *fakeLexClient) LexDo(ctx context.Context, method, inputEncoding, endpoint string, params map[string]any, bodyData any, out any) error {
	target := out.(*agnostic.RepoListRecords_Output)
	target.Records = f.records
	return nil
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestDiscover_UpsertsOnRealVerifiedClaim(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	raw := json.RawMessage(realClaimJSON)
	client := &fakeLexClient{records: []*agnostic.RepoListRecords_Record{
		{Uri: "at://" + realClaimDID + "/dev.keytrace.claim/abc123", Value: &raw},
	}}
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}

	if err := Discover(ctx, client, verifier, conn, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	got, err := appdb.GetSteamClaim(ctx, conn, realClaimDID)
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if got == nil || got.Subject != "76561197994000231" {
		t.Fatalf("got %+v, want a steam claim for subject 76561197994000231", got)
	}
}

func TestDiscover_InvalidatesWhenNoVerifiedClaimFound(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, realClaimDID)
	appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: realClaimDID, Subject: "old", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})

	client := &fakeLexClient{records: nil} // the claim is gone
	verifier := &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}

	if err := Discover(ctx, client, verifier, conn, realClaimDID); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	got, err := appdb.GetSteamClaim(ctx, conn, realClaimDID)
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil after the claim disappears", got)
	}
}
```

- [ ] **Step 2: Confirm the tests fail, then implement**

Run: `go test ./internal/claims/...` — FAIL (`Discover` undefined).

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

// Discover re-scans the user's own dev.keytrace.claim collection for a
// verified, cryptographically-checked Steam claim and upserts/clears
// steam_claims accordingly. See spec's "Claim indexing".
func Discover(ctx context.Context, client lexutil.LexClient, verifier *keytrace.Verifier, conn *sql.DB, did string) error {
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
			if claim.Type != "steam" || claim.Status != "verified" {
				continue
			}
			ok, err := verifier.VerifyAttestation(ctx, did, claim)
			if err != nil {
				return fmt.Errorf("verify claim %s: %w", rec.Uri, err)
			}
			if !ok {
				continue
			}
			return appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{
				DID: did, Subject: claim.Identity.Subject, DisplayName: claim.Identity.DisplayName,
				ClaimURI: claim.ClaimURI, RecordURI: rec.Uri, LastVerifiedAt: time.Now(),
			})
		}

		if resp.Cursor == nil || *resp.Cursor == "" {
			break
		}
		cursor = *resp.Cursor
	}

	// No verified Steam claim found this pass — whatever we had before is stale.
	return appdb.InvalidateSteamClaim(ctx, conn, did)
}
```

Run: `go test ./internal/claims/...` — PASS.

- [ ] **Step 3: The recheck endpoint**

```go
// internal/api/steam_handlers.go
package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	"github.com/jphastings/game-status/internal/claims"
	"github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

type SteamHandlers struct {
	App      *oauth.ClientApp
	Conn     *sql.DB
	Verifier *keytrace.Verifier
}

func (h *SteamHandlers) Recheck(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	if err := h.discoverFor(r.Context(), did); err != nil {
		http.Error(w, "recheck failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SteamHandlers) discoverFor(ctx context.Context, did string) error {
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return err
	}
	user, err := db.GetUser(ctx, h.Conn, did)
	if err != nil {
		return err
	}
	if user == nil || user.ActiveSessionID == "" {
		return fmt.Errorf("no active session for %s", did)
	}
	sess, err := h.App.ResumeSession(ctx, parsedDID, user.ActiveSessionID)
	if err != nil {
		return err
	}
	return claims.Discover(ctx, sess.APIClient(), h.Verifier, h.Conn, did)
}
```

Note: discovery is deliberately triggered only by an explicit request (this handler), never automatically inside the OAuth callback — Task 6's frontend fires one `Recheck` call right after the sign-in redirect lands, which covers the spec's "once at first sign-in" without coupling the OAuth and claims packages together.

- [ ] **Step 4: Wire the real `Verifier` and the route into main.go**

```go
// add to cmd/server/main.go
	dir := identity.DefaultDirectory()
	trustedDIDs, err := resolveTrustedDIDs(context.Background(), dir, keytrace.DefaultTrustedSignerHandles)
	if err != nil {
		log.Fatalf("resolve trusted keytrace signers: %v", err)
	}
	verifier := &keytrace.Verifier{
		Keys:        &keytrace.CachedKeyFetcher{Dir: dir, Conn: conn},
		TrustedDIDs: trustedDIDs,
	}

	steamHandlers := &api.SteamHandlers{App: oauthApp, Conn: conn, Verifier: verifier}
	mux.HandleFunc("POST /api/steam/recheck", oauthHandlers.RequireAuth(steamHandlers.Recheck))
```

```go
// add as a function in cmd/server/main.go
func resolveTrustedDIDs(ctx context.Context, dir identity.Directory, handles []string) (map[string]bool, error) {
	dids := map[string]bool{}
	for _, h := range handles {
		handle, err := syntax.ParseHandle(h)
		if err != nil {
			return nil, err
		}
		ident, err := dir.LookupHandle(ctx, handle)
		if err != nil {
			return nil, err
		}
		dids[ident.DID.String()] = true
	}
	return dids, nil
}
```

Add imports: `"github.com/bluesky-social/indigo/atproto/identity"`, `"github.com/bluesky-social/indigo/atproto/syntax"`, `"github.com/jphastings/game-status/internal/claims"`, `"github.com/jphastings/game-status/internal/keytrace"`.

- [ ] **Step 5: Build, test, commit**

```bash
go get github.com/bluesky-social/indigo/lex/util
go build ./...
go test ./...
git add -A
git commit -m "feat: discover and verify steam keytrace claims"
```

---

### Task 6: Settings page shell + `GET /api/me`

**Files:**
- Create: `internal/api/me_handler.go`, `internal/api/me_handler_test.go`
- Create: `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`, `web/index.html`, `web/src/api.ts`, `web/src/main.ts`
- Modify: `cmd/server/main.go` (serve the embedded frontend build, wire `/api/me`)

**Interfaces:**
- Consumes: `db.GetSteamClaim`, `db.IsSteamEnabled` (Task 2); `DIDFromContext` (Task 4).
- Produces: `api.MeHandler{Conn *sql.DB}` with `Get` (`http.HandlerFunc`); frontend `getMe()`, `recheckClaim()` in `web/src/api.ts`.

- [ ] **Step 1: `GET /api/me` — write the failing test**

```go
// internal/api/me_handler_test.go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestMeHandler_ReturnsClaimAndPrefs(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:abc")
	appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: "did:plc:abc", Subject: "765", DisplayName: "JP", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})
	appdb.SetSteamEnabled(ctx, conn, "did:plc:abc", true)

	h := &MeHandler{Conn: conn}
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:abc"))
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	var got meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.DID != "did:plc:abc" || got.SteamSubject == nil || *got.SteamSubject != "765" || !got.SteamEnabled {
		t.Fatalf("got %+v", got)
	}
}
```

- [ ] **Step 2: Confirm it fails, then implement**

```go
// internal/api/me_handler.go
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/jphastings/game-status/internal/db"
)

type MeHandler struct {
	Conn *sql.DB
}

type meResponse struct {
	DID          string  `json:"did"`
	SteamSubject *string `json:"steamSubject,omitempty"`
	SteamDisplay *string `json:"steamDisplayName,omitempty"`
	SteamEnabled bool    `json:"steamEnabled"`
}

func (h *MeHandler) Get(w http.ResponseWriter, r *http.Request) {
	did, ok := DIDFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}

	resp := meResponse{DID: did}

	claim, err := db.GetSteamClaim(r.Context(), h.Conn, did)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if claim != nil {
		resp.SteamSubject = &claim.Subject
		resp.SteamDisplay = &claim.DisplayName
	}

	enabled, err := db.IsSteamEnabled(r.Context(), h.Conn, did)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp.SteamEnabled = enabled

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

Run: `go test ./internal/api/...` — PASS.

- [ ] **Step 3: Vite + TS scaffold**

```json
// web/package.json
{
  "name": "game-status-web",
  "private": true,
  "type": "module",
  "scripts": { "dev": "vite", "build": "vite build" },
  "devDependencies": { "typescript": "^5.6.0", "vite": "^5.4.0" }
}
```

```ts
// web/vite.config.ts
import { defineConfig } from 'vite'

export default defineConfig({
  build: { outDir: 'dist' },
  server: { proxy: { '/api': 'http://localhost:8080', '/login': 'http://localhost:8080' } },
})
```

```json
// web/tsconfig.json
{
  "compilerOptions": {
    "target": "ES2022", "module": "ESNext", "moduleResolution": "Bundler",
    "strict": true, "skipLibCheck": true
  },
  "include": ["src"]
}
```

```html
<!-- web/index.html -->
<!doctype html>
<html>
<head><meta charset="utf-8" /><title>Game Status Sync</title></head>
<body>
  <div id="app"></div>
  <script type="module" src="/src/main.ts"></script>
</body>
</html>
```

```ts
// web/src/api.ts
export interface Me {
  did: string
  steamSubject?: string
  steamDisplayName?: string
  steamEnabled: boolean
}

export async function getMe(): Promise<Me | null> {
  const res = await fetch('/api/me')
  if (res.status === 401) return null
  if (!res.ok) throw new Error(`GET /api/me: ${res.status}`)
  return res.json()
}

export async function recheckClaim(): Promise<void> {
  const res = await fetch('/api/steam/recheck', { method: 'POST' })
  if (!res.ok) throw new Error(`POST /api/steam/recheck: ${res.status}`)
}
```

```ts
// web/src/main.ts
import { getMe, recheckClaim, type Me } from './api'

const app = document.getElementById('app')!

async function render() {
  const me = await getMe()
  if (!me) {
    app.innerHTML = `
      <h1>Game Status Sync</h1>
      <input id="handle" placeholder="your.handle" />
      <button id="signin">Sign in with atproto</button>
    `
    document.getElementById('signin')!.addEventListener('click', () => {
      const handle = (document.getElementById('handle') as HTMLInputElement).value
      window.location.href = `/login?handle=${encodeURIComponent(handle)}`
    })
    return
  }

  // Covers the spec's "discovery once at sign-in": best-effort, silent on
  // failure (the claim may genuinely not exist yet) — the button below is
  // the reliable path.
  if (!me.steamSubject) {
    await recheckClaim().catch(() => {})
    renderSignedIn((await getMe()) ?? me)
    return
  }
  renderSignedIn(me)
}

function renderSignedIn(me: Me) {
  const claimStatus = me.steamSubject
    ? `Verified as ${me.steamDisplayName ?? me.steamSubject}`
    : 'Not connected — verify at keytrace.dev, then recheck below'

  app.innerHTML = `
    <h1>Game Status Sync</h1>
    <p>Signed in as ${me.did}</p>
    <h2>Steam</h2>
    <p>${claimStatus}</p>
    <button id="recheck">Recheck claim</button>
  `
  document.getElementById('recheck')!.addEventListener('click', async () => {
    await recheckClaim()
    await render()
  })
}

render()
```

- [ ] **Step 4: Serve the built frontend and wire `/api/me` into main.go**

```bash
cd web && pnpm install && pnpm build && cd ..
```

```go
// add to cmd/server/main.go
	distFS, err := fs.Sub(frontendFS, "web/dist")
	if err != nil {
		log.Fatalf("frontend embed: %v", err)
	}
	mux.Handle("GET /", http.FileServerFS(distFS))

	meHandler := &api.MeHandler{Conn: conn}
	mux.HandleFunc("GET /api/me", oauthHandlers.RequireAuth(meHandler.Get))
```

```go
// add near the top of cmd/server/main.go, above func main
//go:embed web/dist
var frontendFS embed.FS
```

Add `"embed"` and `"io/fs"` to the import block. `go:embed` requires `web/dist` to exist at *Go* build time — the frontend build in this step must run before `go build`; note this in the repo's README/build script once one exists (out of scope for this plan).

- [ ] **Step 5: Build, test, commit**

```bash
go build ./...
go test ./...
git add -A
git commit -m "feat: settings page shell, /api/me, serve embedded frontend"
```

---

### Task 7: Cartridge client

Resolves a Steam appid to a `games.gamesgamesgamesgames.game` record via cartridge's real host, confirmed by reading their deployed frontend bundle: `https://gamesgamesgamesgames.games` (not `cartridge.dev` — that domain is only the Next.js frontend). Every request there, including this "public" read, requires an `x-client-key` header. **The value baked into cartridge's own frontend bundle is not ours to use** — see Prerequisites #2. This task's automated tests use a fake HTTP server and need no real key; only a live smoke-test does.

**Files:**
- Modify: `internal/config/config.go` (add `CartridgeHost`, `CartridgeClientKey`)
- Create: `internal/cartridge/client.go`, `internal/cartridge/client_test.go`

**Interfaces:**
- Consumes: `db.CachedGame`, `db.GetCachedGame`, `db.SetCachedGame` (Task 2, extended just above).
- Produces: `cartridge.Client{API *atclient.APIClient, Conn *sql.DB}`, `cartridge.New(host, clientKey string, conn *sql.DB) *Client`, `(*Client) GetGameBySteamID(ctx, steamAppID string) (*db.CachedGame, error)` (returns `nil, nil` when cartridge has no matching game — caller skips the write, per spec), `cartridge.PageURL(slug string) string`.

- [ ] **Step 1: Extend config**

```go
// add to Config struct
	CartridgeHost      string
	CartridgeClientKey string
```

```go
// add to Load()
	cfg.CartridgeHost = envOr("CARTRIDGE_HOST", "https://gamesgamesgamesgames.games")
	cartridgeKey, err := requireEnv("CARTRIDGE_CLIENT_KEY")
	if err != nil {
		return nil, err
	}
	cfg.CartridgeClientKey = cartridgeKey
```

- [ ] **Step 2: Write the failing tests against a fake cartridge server**

```go
// internal/cartridge/client_test.go
package cartridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestGetGameBySteamID_SendsClientKeyAndSteamIDCachesResult(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("x-client-key"); got != "test-key" {
			t.Errorf("x-client-key = %q, want test-key", got)
		}
		if got := r.URL.Query().Get("steamId"); got != "271590" {
			t.Errorf("steamId = %q, want 271590", got)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"game": map[string]any{
				"uri": "at://did:plc:cartridge/games.gamesgamesgamesgames.game/gta5",
				"name": "Grand Theft Auto V", "summary": "An open world game.", "slug": "grand-theft-auto-v",
			},
		})
	}))
	defer server.Close()

	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	c := New(server.URL, "test-key", conn)

	got, err := c.GetGameBySteamID(context.Background(), "271590")
	if err != nil {
		t.Fatalf("GetGameBySteamID: %v", err)
	}
	if got == nil || got.Name != "Grand Theft Auto V" || got.PageURL != "https://cartridge.dev/game/grand-theft-auto-v" {
		t.Fatalf("got %+v", got)
	}

	if _, err := c.GetGameBySteamID(context.Background(), "271590"); err != nil {
		t.Fatalf("second GetGameBySteamID: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (second call should hit the cache)", calls)
	}
}

func TestGetGameBySteamID_UnresolvedReturnsNilNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"game": map[string]any{}})
	}))
	defer server.Close()

	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	got, err := New(server.URL, "test-key", conn).GetGameBySteamID(context.Background(), "999999")
	if err != nil {
		t.Fatalf("GetGameBySteamID: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil for an unresolvable game", got)
	}
}
```

- [ ] **Step 3: Confirm the tests fail, then implement**

```go
// internal/cartridge/client.go
package cartridge

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/bluesky-social/indigo/atproto/atclient"

	appdb "github.com/jphastings/game-status/internal/db"
)

type Client struct {
	API  *atclient.APIClient
	Conn *sql.DB
}

func New(host, clientKey string, conn *sql.DB) *Client {
	api := atclient.NewAPIClient(host)
	api.Headers.Set("x-client-key", clientKey)
	return &Client{API: api, Conn: conn}
}

type getGameOutput struct {
	Game struct {
		URI     string `json:"uri"`
		Name    string `json:"name"`
		Summary string `json:"summary"`
		Slug    string `json:"slug"`
	} `json:"game"`
}

func (c *Client) GetGameBySteamID(ctx context.Context, steamAppID string) (*appdb.CachedGame, error) {
	if cached, err := appdb.GetCachedGame(ctx, c.Conn, steamAppID); err != nil {
		return nil, err
	} else if cached != nil {
		return cached, nil
	}

	var out getGameOutput
	if err := c.API.Get(ctx, "games.gamesgamesgamesgames.getGame", map[string]any{"steamId": steamAppID}, &out); err != nil {
		return nil, fmt.Errorf("cartridge getGame: %w", err)
	}
	if out.Game.URI == "" {
		return nil, nil // not resolvable — caller skips the write this tick (spec)
	}

	game := appdb.CachedGame{URI: out.Game.URI, PageURL: PageURL(out.Game.Slug), Name: out.Game.Name, Summary: out.Game.Summary}
	if err := appdb.SetCachedGame(ctx, c.Conn, steamAppID, game); err != nil {
		return nil, err
	}
	return &game, nil
}

// PageURL is cartridge.dev's web page for a game. Route confirmed from
// gamesgamesgamesgamesgames/cartridge's src/app/(site)/game/[slug]/page.tsx —
// the (site) route group doesn't appear in the URL.
func PageURL(slug string) string {
	return "https://cartridge.dev/game/" + slug
}
```

- [ ] **Step 4: Run tests, build, commit**

```bash
go test ./internal/cartridge/...
go build ./...
git add -A
git commit -m "feat: cartridge.dev game resolution client"
```

---

### Task 8: Steam client

**Files:**
- Modify: `internal/config/config.go` (add `SteamAPIKey`)
- Create: `internal/steam/client.go`, `internal/steam/client_test.go`

**Interfaces:**
- Produces: `steam.PlayerSummary{SteamID, GameID, GameExtraInfo string}`; `steam.Client{APIKey string, HTTPClient *http.Client, BaseURL string}`, `steam.New(apiKey string) *Client`, `(*Client) GetPlayerSummaries(ctx, steamIDs []string) (map[string]PlayerSummary, error)` — batches into groups of ≤100 (the Steam Web API's own limit per call); a `PlayerSummary` with an empty `GameID` means not currently playing (or a private profile).

- [ ] **Step 1: Extend config**

```go
// add to Config struct
	SteamAPIKey string
```

```go
// add to Load()
	steamKey, err := requireEnv("STEAM_API_KEY")
	if err != nil {
		return nil, err
	}
	cfg.SteamAPIKey = steamKey
```

- [ ] **Step 2: Write the failing tests**

```go
// internal/steam/client_test.go
package steam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestGetPlayerSummaries_BatchesRequestsOfAtMost100(t *testing.T) {
	var batchSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids := strings.Split(r.URL.Query().Get("steamids"), ",")
		batchSizes = append(batchSizes, len(ids))
		players := make([]map[string]any, len(ids))
		for i, id := range ids {
			players[i] = map[string]any{"steamid": id}
		}
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"players": players}})
	}))
	defer server.Close()

	steamIDs := make([]string, 201)
	for i := range steamIDs {
		steamIDs[i] = strconv.Itoa(i)
	}

	c := &Client{APIKey: "key", HTTPClient: http.DefaultClient, BaseURL: server.URL}
	got, err := c.GetPlayerSummaries(context.Background(), steamIDs)
	if err != nil {
		t.Fatalf("GetPlayerSummaries: %v", err)
	}
	if len(got) != 201 {
		t.Fatalf("got %d players, want 201", len(got))
	}
	if want := []int{100, 100, 1}; !equalInts(batchSizes, want) {
		t.Fatalf("batch sizes = %v, want %v", batchSizes, want)
	}
}

func TestGetPlayerSummaries_ParsesCurrentGame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"players": []map[string]any{
			{"steamid": "76500000000000000", "gameid": "271590", "gameextrainfo": "Grand Theft Auto V"},
			{"steamid": "76500000000000001"}, // not currently playing
		}}})
	}))
	defer server.Close()

	c := &Client{APIKey: "key", HTTPClient: http.DefaultClient, BaseURL: server.URL}
	got, err := c.GetPlayerSummaries(context.Background(), []string{"76500000000000000", "76500000000000001"})
	if err != nil {
		t.Fatalf("GetPlayerSummaries: %v", err)
	}
	if got["76500000000000000"].GameID != "271590" {
		t.Fatalf("got %+v, want gameid 271590", got["76500000000000000"])
	}
	if got["76500000000000001"].GameID != "" {
		t.Fatalf("got %+v, want empty gameid", got["76500000000000001"])
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 3: Confirm the tests fail, then implement**

```go
// internal/steam/client.go
package steam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const defaultBaseURL = "https://api.steampowered.com/ISteamUser/GetPlayerSummaries/v0002/"
const batchSize = 100

type PlayerSummary struct {
	SteamID       string
	GameID        string
	GameExtraInfo string
}

type Client struct {
	APIKey     string
	HTTPClient *http.Client
	BaseURL    string // overridable for tests
}

func New(apiKey string) *Client {
	return &Client{APIKey: apiKey, HTTPClient: http.DefaultClient, BaseURL: defaultBaseURL}
}

type summariesResponse struct {
	Response struct {
		Players []struct {
			SteamID       string `json:"steamid"`
			GameID        string `json:"gameid"`
			GameExtraInfo string `json:"gameextrainfo"`
		} `json:"players"`
	} `json:"response"`
}

func (c *Client) GetPlayerSummaries(ctx context.Context, steamIDs []string) (map[string]PlayerSummary, error) {
	result := make(map[string]PlayerSummary, len(steamIDs))

	for start := 0; start < len(steamIDs); start += batchSize {
		end := min(start+batchSize, len(steamIDs))
		batch := steamIDs[start:end]

		reqURL := c.BaseURL + "?" + url.Values{"key": {c.APIKey}, "steamids": {strings.Join(batch, ",")}}.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("steam GetPlayerSummaries: %w", err)
		}
		var parsed summariesResponse
		err = json.NewDecoder(resp.Body).Decode(&parsed)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode steam response: %w", err)
		}

		for _, p := range parsed.Response.Players {
			result[p.SteamID] = PlayerSummary{SteamID: p.SteamID, GameID: p.GameID, GameExtraInfo: p.GameExtraInfo}
		}
	}

	return result, nil
}
```

`min` is a Go 1.21+ builtin — fine given `http.FileServerFS`/`GET /path` patterns elsewhere already assume Go 1.22+.

- [ ] **Step 4: Run tests, build, commit**

```bash
go test ./internal/steam/...
go build ./...
git add -A
git commit -m "feat: steam web api client"
```

---

### Task 9: Sync decision logic (pure)

**Files:**
- Create: `internal/sync/decide.go`, `internal/sync/decide_test.go`

**Interfaces:**
- Produces: `sync.SessionStart{GameKey string, StartedAt time.Time}`; `sync.Action` (`ActionNone`, `ActionDelete`, `ActionWrite`); `sync.Decision{Action Action, GameKey string, CreatedAt time.Time}`; `sync.Decide(playing bool, appID string, prev *SessionStart, now time.Time) Decision`.

No IO in this package on purpose — Task 10 does the DB/network calls and converts `db.SessionStart` to `sync.SessionStart` at the boundary.

- [ ] **Step 1: Write the failing tests**

```go
// internal/sync/decide_test.go
package sync

import (
	"testing"
	"time"
)

func TestDecide_NotPlaying_AlwaysDeletes(t *testing.T) {
	now := time.Now()
	if d := Decide(false, "", nil, now); d.Action != ActionDelete {
		t.Fatalf("got %+v, want ActionDelete", d)
	}
	// Idempotent by design (Global Constraints) — deletes even with no prior session.
	if d := Decide(false, "", &SessionStart{GameKey: "271590", StartedAt: now.Add(-time.Hour)}, now); d.Action != ActionDelete {
		t.Fatalf("got %+v, want ActionDelete even with a prior session", d)
	}
}

func TestDecide_PlayingSameGame_ReusesStartedAt(t *testing.T) {
	now := time.Now()
	started := now.Add(-2 * time.Hour)
	d := Decide(true, "271590", &SessionStart{GameKey: "271590", StartedAt: started}, now)
	if d.Action != ActionWrite || !d.CreatedAt.Equal(started) {
		t.Fatalf("got %+v, want ActionWrite with CreatedAt=%v", d, started)
	}
}

func TestDecide_PlayingDifferentGame_ResetsStartedAt(t *testing.T) {
	now := time.Now()
	d := Decide(true, "570", &SessionStart{GameKey: "271590", StartedAt: now.Add(-2 * time.Hour)}, now)
	if d.Action != ActionWrite || !d.CreatedAt.Equal(now) {
		t.Fatalf("got %+v, want ActionWrite with CreatedAt=now (%v)", d, now)
	}
}

func TestDecide_PlayingWithNoPriorSession_StartsNow(t *testing.T) {
	now := time.Now()
	d := Decide(true, "271590", nil, now)
	if d.Action != ActionWrite || !d.CreatedAt.Equal(now) {
		t.Fatalf("got %+v, want ActionWrite with CreatedAt=now", d)
	}
}
```

- [ ] **Step 2: Confirm the tests fail, then implement**

```go
// internal/sync/decide.go
package sync

import "time"

type SessionStart struct {
	GameKey   string
	StartedAt time.Time
}

type Action int

const (
	ActionNone Action = iota
	ActionDelete
	ActionWrite
)

type Decision struct {
	Action    Action
	GameKey   string    // only meaningful for ActionWrite
	CreatedAt time.Time // only meaningful for ActionWrite
}

// Decide is pure and needs no read of the previous PDS record (Global
// Constraints): not playing always deletes, regardless of prior state — an
// idempotent no-op if nothing existed. Playing always writes; CreatedAt is
// reused only when the game hasn't changed, which is the one thing that
// can't be derived from "what's playing right now" alone.
func Decide(playing bool, appID string, prev *SessionStart, now time.Time) Decision {
	if !playing {
		return Decision{Action: ActionDelete}
	}
	if prev != nil && prev.GameKey == appID {
		return Decision{Action: ActionWrite, GameKey: appID, CreatedAt: prev.StartedAt}
	}
	return Decision{Action: ActionWrite, GameKey: appID, CreatedAt: now}
}
```

- [ ] **Step 3: Run tests, commit**

```bash
go test ./internal/sync/...
git add internal/sync
git commit -m "feat: pure sync decision logic"
```

---

### Task 10: Sync tick orchestration

**Files:**
- Create: `internal/sync/status.go` (the `ActorStatus` record type), `internal/sync/writer.go` (real atproto writes), `internal/sync/tick.go`, `internal/sync/tick_test.go`
- Modify: `cmd/server/main.go` (start the 5-minute ticker goroutine)

**Interfaces:**
- Consumes: `Decide`, `SessionStart`, `Action*` (Task 9); `steam.Client.GetPlayerSummaries` (Task 8); `cartridge.Client.GetGameBySteamID` (Task 7); `db.ListSteamEnabledDIDs`, `db.GetSteamClaim`, `db.GetSessionStart`, `db.SetSessionStart`, `db.ClearSessionStart`, `db.GetUser` (Task 2/5).
- Produces: `sync.ActorStatus`, `sync.Embed`, `sync.EmbedExternal`, `sync.StatusCollection`; `sync.SteamAPI`, `sync.GameResolver`, `sync.RecordWriter` interfaces; `sync.ATProtoWriter{App *oauth.ClientApp, Conn *sql.DB}` (implements `RecordWriter`); `sync.RunTick(ctx, conn *sql.DB, steamAPI SteamAPI, resolver GameResolver, writer RecordWriter, now time.Time) error`.

- [ ] **Step 1: The record type**

```go
// internal/sync/status.go
package sync

const StatusCollection = "games.gamesgamesgamesgames.actor.status"
const statusRkey = "self"

type ActorStatus struct {
	Type      string `json:"$type"`
	Game      string `json:"game"`
	Platform  string `json:"platform"`
	Playing   map[string]any `json:"playing"` // always {} in v1 — see Global Constraints, no party info yet
	Embed     *Embed `json:"embed,omitempty"`
	CreatedAt string `json:"createdAt"`
	StaleAt   string `json:"staleAt"`
}

type Embed struct {
	Type     string        `json:"$type"`
	External EmbedExternal `json:"external"`
}

type EmbedExternal struct {
	URI         string `json:"uri"`
	Title       string `json:"title"`
	Description string `json:"description"`
}
```

- [ ] **Step 2: Write the failing orchestration tests (against fakes)**

```go
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
```

- [ ] **Step 3: Confirm the tests fail, then implement the orchestration**

```go
// internal/sync/tick.go
package sync

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/steam"
)

// staleBuffer must comfortably exceed the tick interval so one missed tick
// doesn't make a live status look abandoned (spec: "staleAt: now + buffer").
const staleBuffer = 15 * time.Minute

type SteamAPI interface {
	GetPlayerSummaries(ctx context.Context, steamIDs []string) (map[string]steam.PlayerSummary, error)
}

type GameResolver interface {
	GetGameBySteamID(ctx context.Context, steamAppID string) (*appdb.CachedGame, error)
}

type RecordWriter interface {
	PutStatus(ctx context.Context, did string, status ActorStatus) error
	DeleteStatus(ctx context.Context, did string) error
}

func RunTick(ctx context.Context, conn *sql.DB, steamAPI SteamAPI, resolver GameResolver, writer RecordWriter, now time.Time) error {
	dids, err := appdb.ListSteamEnabledDIDs(ctx, conn)
	if err != nil {
		return err
	}
	if len(dids) == 0 {
		return nil
	}

	steamIDs := make([]string, 0, len(dids))
	steamIDToDID := make(map[string]string, len(dids))
	for _, did := range dids {
		claim, err := appdb.GetSteamClaim(ctx, conn, did)
		if err != nil {
			return err
		}
		if claim == nil {
			continue // claim vanished between the list and here — Jetstream/the daily sweep will settle this
		}
		steamIDs = append(steamIDs, claim.Subject)
		steamIDToDID[claim.Subject] = did
	}

	summaries, err := steamAPI.GetPlayerSummaries(ctx, steamIDs)
	if err != nil {
		return fmt.Errorf("steam GetPlayerSummaries: %w", err)
	}

	for steamID, did := range steamIDToDID {
		if err := tickOne(ctx, conn, resolver, writer, did, summaries[steamID], now); err != nil {
			slog.Error("sync tick failed for account", "did", did, "err", err) // one account's failure shouldn't stop the rest
		}
	}
	return nil
}

func tickOne(ctx context.Context, conn *sql.DB, resolver GameResolver, writer RecordWriter, did string, summary steam.PlayerSummary, now time.Time) error {
	playing := summary.GameID != ""

	var prev *SessionStart
	row, err := appdb.GetSessionStart(ctx, conn, did, "steam")
	if err != nil {
		return err
	}
	if row != nil {
		prev = &SessionStart{GameKey: row.GameKey, StartedAt: row.StartedAt}
	}

	decision := Decide(playing, summary.GameID, prev, now)

	switch decision.Action {
	case ActionDelete:
		if err := writer.DeleteStatus(ctx, did); err != nil {
			return err
		}
		return appdb.ClearSessionStart(ctx, conn, did, "steam")

	case ActionWrite:
		if err := appdb.SetSessionStart(ctx, conn, did, "steam", decision.GameKey, decision.CreatedAt); err != nil {
			return err
		}
		game, err := resolver.GetGameBySteamID(ctx, decision.GameKey)
		if err != nil {
			return err
		}
		if game == nil {
			return nil // not resolvable — skip the write this tick; session_starts is already correct (spec)
		}
		status := ActorStatus{
			Type: "games.gamesgamesgamesgames.actor.status", Game: game.URI, Platform: "steam",
			Playing:   map[string]any{},
			Embed:     &Embed{Type: "app.bsky.embed.external", External: EmbedExternal{URI: game.PageURL, Title: game.Name, Description: game.Summary}},
			CreatedAt: decision.CreatedAt.UTC().Format(time.RFC3339),
			StaleAt:   now.Add(staleBuffer).UTC().Format(time.RFC3339),
		}
		return writer.PutStatus(ctx, did, status)
	}
	return nil
}
```

Run: `go test ./internal/sync/...` — PASS.

- [ ] **Step 4: The real atproto writer**

```go
// internal/sync/writer.go
package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/agnostic"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	"github.com/bluesky-social/indigo/atproto/syntax"

	appdb "github.com/jphastings/game-status/internal/db"
)

type ATProtoWriter struct {
	App  *oauth.ClientApp
	Conn *sql.DB
}

var _ RecordWriter = (*ATProtoWriter)(nil)

func (w *ATProtoWriter) client(ctx context.Context, did string) (*atclient.APIClient, error) {
	user, err := appdb.GetUser(ctx, w.Conn, did)
	if err != nil {
		return nil, err
	}
	if user == nil || user.ActiveSessionID == "" {
		return nil, fmt.Errorf("no active session for %s", did)
	}
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return nil, err
	}
	sess, err := w.App.ResumeSession(ctx, parsedDID, user.ActiveSessionID)
	if err != nil {
		return nil, err
	}
	return sess.APIClient(), nil
}

func (w *ATProtoWriter) PutStatus(ctx context.Context, did string, status ActorStatus) error {
	client, err := w.client(ctx, did)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	record, err := atdata.UnmarshalJSON(raw)
	if err != nil {
		return fmt.Errorf("validate status record: %w", err)
	}
	validate := true
	_, err = agnostic.RepoPutRecord(ctx, client, &agnostic.RepoPutRecord_Input{
		Collection: StatusCollection, Repo: did, Rkey: statusRkey, Record: record, Validate: &validate,
	})
	return err
}

func (w *ATProtoWriter) DeleteStatus(ctx context.Context, did string) error {
	client, err := w.client(ctx, did)
	if err != nil {
		return err
	}
	_, err = comatproto.RepoDeleteRecord(ctx, client, &comatproto.RepoDeleteRecord_Input{
		Collection: StatusCollection, Repo: did, Rkey: statusRkey,
	})
	if err != nil && isRecordNotFound(err) {
		return nil // idempotent — deleting an already-gone record is success (Global Constraints)
	}
	return err
}

// isRecordNotFound is a best-effort check — indigo's exact XRPC error
// type/structure for this case wasn't confirmed while writing this plan.
// Verify against a real delete-on-missing-record response during
// implementation, and switch to matching indigo's actual error type/field
// (e.g. an XRPC error's Name) instead of string matching, if one exposes it.
func isRecordNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "RecordNotFound")
}
```

- [ ] **Step 5: Wire the 5-minute ticker into main.go**

```go
// add to cmd/server/main.go
	writer := &sync.ATProtoWriter{App: oauthApp, Conn: conn}
	cartridgeClient := cartridge.New(cfg.CartridgeHost, cfg.CartridgeClientKey, conn)
	steamClient := steam.New(cfg.SteamAPIKey)

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := sync.RunTick(context.Background(), conn, steamClient, cartridgeClient, writer, time.Now()); err != nil {
				slog.Error("sync tick", "err", err)
			}
		}
	}()
```

Add imports: `"github.com/jphastings/game-status/internal/cartridge"`, `"github.com/jphastings/game-status/internal/steam"`, `"github.com/jphastings/game-status/internal/sync"`, `"time"`.

- [ ] **Step 6: Build, test, commit**

```bash
go get github.com/bluesky-social/indigo/atproto/atdata
go build ./...
go test ./...
git add -A
git commit -m "feat: 5-minute steam sync tick, idempotent writes"
```

---

### Task 11: Enable/disable toggle, live status, frontend wiring

**Files:**
- Modify: `internal/api/steam_handlers.go` (add `SetEnabled`), `internal/api/me_handler.go` (add live status), `web/src/api.ts`, `web/src/main.ts`
- Create: `internal/api/steam_handlers_test.go`
- Modify: `cmd/server/main.go` (wire the enable route, pass `oauthApp` into `MeHandler`)

**Interfaces:**
- Consumes: `db.SetSteamEnabled`, `db.GetSteamClaim`, `db.GetUser` (Task 2); `sync.StatusCollection` (Task 10).
- Produces: `(*SteamHandlers) SetEnabled` (`http.HandlerFunc`, 409 if enabling without a valid claim); `MeHandler.Live *liveStatus` field on the JSON response.

- [ ] **Step 1: Write the failing enable-guard tests**

```go
// internal/api/steam_handlers_test.go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
)

func TestSetEnabled_RejectsEnableWithoutValidClaim(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	appdb.UpsertUser(context.Background(), conn, "did:plc:a")

	h := &SteamHandlers{Conn: conn}
	req := httptest.NewRequest(http.MethodPost, "/api/steam/enabled", strings.NewReader(`{"enabled":true}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestSetEnabled_AllowsEnableWithValidClaim(t *testing.T) {
	conn, err := appdb.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: "did:plc:a", Subject: "765", ClaimURI: "x", RecordURI: "y", LastVerifiedAt: time.Now()})

	h := &SteamHandlers{Conn: conn}
	req := httptest.NewRequest(http.MethodPost, "/api/steam/enabled", strings.NewReader(`{"enabled":true}`))
	req = req.WithContext(context.WithValue(req.Context(), didContextKey, "did:plc:a"))
	rec := httptest.NewRecorder()

	h.SetEnabled(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	enabled, err := appdb.IsSteamEnabled(ctx, conn, "did:plc:a")
	if err != nil || !enabled {
		t.Fatalf("IsSteamEnabled = %v, %v, want true, nil", enabled, err)
	}
}
```

- [ ] **Step 2: Confirm the tests fail, then implement `SetEnabled`**

```go
// add to internal/api/steam_handlers.go
type enableRequest struct {
	Enabled bool `json:"enabled"`
}

func (h *SteamHandlers) SetEnabled(w http.ResponseWriter, r *http.Request) {
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
		claim, err := db.GetSteamClaim(r.Context(), h.Conn, did)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if claim == nil {
			http.Error(w, "no verified steam claim — recheck first", http.StatusConflict)
			return
		}
	}

	if err := db.SetSteamEnabled(r.Context(), h.Conn, did, body.Enabled); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Add `"encoding/json"` to `steam_handlers.go`'s imports.

- [ ] **Step 3: Live status on `GET /api/me`** — reads straight from the user's own PDS, consistent with treating the PDS as authoritative everywhere else (spec's "Frontend" section). This path isn't practically unit-testable without a fake OAuth session + PDS server (more than this task's scope justifies) — verify it manually in Step 6.

```go
// modify internal/api/me_handler.go
type MeHandler struct {
	Conn *sql.DB
	App  *oauth.ClientApp
}

type liveStatus struct {
	Game     string `json:"game"`
	Platform string `json:"platform,omitempty"`
}

// add Live to meResponse
	Live *liveStatus `json:"live,omitempty"`

// add to Get(), before the final json.NewEncoder call
	live, err := h.getLiveStatus(r.Context(), did)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp.Live = live

func (h *MeHandler) getLiveStatus(ctx context.Context, did string) (*liveStatus, error) {
	user, err := db.GetUser(ctx, h.Conn, did)
	if err != nil {
		return nil, err
	}
	if user == nil || user.ActiveSessionID == "" {
		return nil, nil
	}
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return nil, err
	}
	sess, err := h.App.ResumeSession(ctx, parsedDID, user.ActiveSessionID)
	if err != nil {
		return nil, err
	}
	resp, err := agnostic.RepoGetRecord(ctx, sess.APIClient(), "", sync.StatusCollection, did, "self")
	if err != nil {
		return nil, nil // no record — not currently playing anything, not an error
	}
	var status struct {
		Game     string `json:"game"`
		Platform string `json:"platform"`
	}
	if err := json.Unmarshal(*resp.Value, &status); err != nil {
		return nil, err
	}
	return &liveStatus{Game: status.Game, Platform: status.Platform}, nil
}
```

Add imports to `me_handler.go`: `"context"`, `"github.com/bluesky-social/indigo/api/agnostic"`, `"github.com/bluesky-social/indigo/atproto/auth/oauth"`, `"github.com/bluesky-social/indigo/atproto/syntax"`, `"github.com/jphastings/game-status/internal/sync"`.

- [ ] **Step 4: Frontend — toggle and live status**

```ts
// add to web/src/api.ts
export interface Me {
  did: string
  steamSubject?: string
  steamDisplayName?: string
  steamEnabled: boolean
  live?: { game: string; platform?: string }
}

export async function setSteamEnabled(enabled: boolean): Promise<void> {
  const res = await fetch('/api/steam/enabled', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled }),
  })
  if (!res.ok) throw new Error(`POST /api/steam/enabled: ${res.status}`)
}
```

```ts
// replace renderSignedIn in web/src/main.ts
function renderSignedIn(me: Me) {
  const claimStatus = me.steamSubject
    ? `Verified as ${me.steamDisplayName ?? me.steamSubject}`
    : 'Not connected — verify at keytrace.dev, then recheck below'
  const toggleDisabled = me.steamSubject ? '' : 'disabled'
  const liveStatus = me.live ? `Currently: ${me.live.game}` : 'Not currently playing anything tracked'

  app.innerHTML = `
    <h1>Game Status Sync</h1>
    <p>Signed in as ${me.did}</p>
    <h2>Steam</h2>
    <p>${claimStatus}</p>
    <button id="recheck">Recheck claim</button>
    <label><input type="checkbox" id="enabled" ${me.steamEnabled ? 'checked' : ''} ${toggleDisabled} /> Sync Steam status</label>
    <p>${liveStatus}</p>
  `
  document.getElementById('recheck')!.addEventListener('click', async () => {
    await recheckClaim()
    await render()
  })
  document.getElementById('enabled')!.addEventListener('change', async (e) => {
    await setSteamEnabled((e.target as HTMLInputElement).checked)
    await render()
  })
}
```

Update the `import { getMe, recheckClaim, type Me }` line to also import `setSteamEnabled`.

- [ ] **Step 5: Wire into main.go**

```go
// modify the meHandler construction in cmd/server/main.go
	meHandler := &api.MeHandler{Conn: conn, App: oauthApp}
	mux.HandleFunc("POST /api/steam/enabled", oauthHandlers.RequireAuth(steamHandlers.SetEnabled))
```

- [ ] **Step 6: Build, test, commit, then verify manually**

```bash
cd web && pnpm build && cd ..
go build ./...
go test ./...
git add -A
git commit -m "feat: enable/disable toggle and live status"
```

Manually: sign in with a verified Steam claim, enable sync, wait for a tick (or trigger one), confirm the settings page's "Currently:" line matches what's actually on your PDS (check with any AT-URI browser, e.g. pdsls.dev).

---

### Task 12: Jetstream event handler (pure)

**Files:**
- Create: `internal/jetstream/handler.go`, `internal/jetstream/handler_test.go`

**Interfaces:**
- Consumes: `keytrace.Verifier`, `keytrace.Claim`, `keytrace.ClaimCollection` (Task 3); `db.SteamClaim`, `db.GetSteamClaim`, `db.UpsertSteamClaim`, `db.InvalidateSteamClaim` (Task 2).
- Produces: `jetstream.Operation` (`OpCreate`, `OpUpdate`, `OpDelete`); `jetstream.Event{DID, Collection, Rkey string, Operation Operation, Record []byte}`; `jetstream.Store` and `jetstream.StatusDeleter` interfaces; `jetstream.HandleEvent(ctx, store Store, deleter StatusDeleter, verifier *keytrace.Verifier, ev Event) error`.

No IO here either — Task 13 supplies the real websocket connection and the real `Store`/`StatusDeleter`.

- [ ] **Step 1: Write the failing tests, covering every branch from the spec's Jetstream section**

```go
// internal/jetstream/handler_test.go
package jetstream

import (
	"context"
	"strings"
	"testing"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

const realSignerDID = "did:plc:hcwfdlmprcc335oixyfsw7u3"
const realKeyJWK = `{"kty":"EC","x":"pretZ8lN1snAV4dNoyet54BTTs1-Mxv4-jNuVGazf8g","y":"wUT9JvxuvkRtPrufb6c4BPXoA60LhmvfaE_aH5d6A-o","crv":"P-256"}`
const realClaimJSON = `{
	"type":"steam","status":"verified",
	"claimUri":"https://steamcommunity.com/profiles/76561197994000231",
	"identity":{"subject":"76561197994000231","displayName":"JP"},
	"sigs":[{
		"kid":"attest:steam",
		"src":"at://did:plc:hcwfdlmprcc335oixyfsw7u3/dev.keytrace.serverPublicKey/2026-05-03",
		"signedAt":"2026-05-03T07:53:39.639Z",
		"attestation":"eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJjbGFpbVVyaSI6Imh0dHBzOi8vc3RlYW1jb21tdW5pdHkuY29tL3Byb2ZpbGVzLzc2NTYxMTk3OTk0MDAwMjMxIiwiZGlkIjoiZGlkOnBsYzplcGhrenBpbmhhcWNhYnRrdWd0Ynpyd3UiLCJpZGVudGl0eS5zdWJqZWN0IjoiNzY1NjExOTc5OTQwMDAyMzEiLCJ0eXBlIjoic3RlYW0ifQ.69NGYohaBKTQFtWoRPrqeIOIZN72Q7eEhESF2EPaQLRUfnFioQ3vtGWHsmSSEO5m8_7vd6UU347AlwcafaBBGA",
		"signedFields":["claimUri","did","identity.subject","type"]
	}],
	"createdAt":"2026-05-03T07:53:39.698Z","lastVerifiedAt":"2026-05-03T07:53:39.698Z"
}`

type fakeKeyFetcher struct{}

func (fakeKeyFetcher) FetchPublicJWK(ctx context.Context, keyURI string) (string, error) {
	return realKeyJWK, nil
}

func testVerifier() *keytrace.Verifier {
	return &keytrace.Verifier{Keys: fakeKeyFetcher{}, TrustedDIDs: map[string]bool{realSignerDID: true}}
}

type fakeStore struct {
	claim       *appdb.SteamClaim
	upserts     []appdb.SteamClaim
	invalidated bool
}

func (f *fakeStore) GetSteamClaim(ctx context.Context, did string) (*appdb.SteamClaim, error) { return f.claim, nil }
func (f *fakeStore) UpsertSteamClaim(ctx context.Context, c appdb.SteamClaim) error {
	f.upserts = append(f.upserts, c)
	return nil
}
func (f *fakeStore) InvalidateSteamClaim(ctx context.Context, did string) error {
	f.invalidated = true
	return nil
}

type fakeDeleter struct{ deleted []string }

func (f *fakeDeleter) DeleteStatus(ctx context.Context, did string) error {
	f.deleted = append(f.deleted, did)
	return nil
}

func TestHandleEvent_DeleteMatchingTrackedRecord_Invalidates(t *testing.T) {
	store := &fakeStore{claim: &appdb.SteamClaim{DID: "did:plc:a", RecordURI: "at://did:plc:a/dev.keytrace.claim/abc"}}
	deleter := &fakeDeleter{}

	ev := Event{DID: "did:plc:a", Collection: keytrace.ClaimCollection, Rkey: "abc", Operation: OpDelete}
	if err := HandleEvent(context.Background(), store, deleter, testVerifier(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if !store.invalidated || len(deleter.deleted) != 1 {
		t.Fatalf("got invalidated=%v deleted=%v, want both to have fired", store.invalidated, deleter.deleted)
	}
}

func TestHandleEvent_DeleteOfUnrelatedRecord_NoOp(t *testing.T) {
	store := &fakeStore{claim: &appdb.SteamClaim{DID: "did:plc:a", RecordURI: "at://did:plc:a/dev.keytrace.claim/current"}}
	deleter := &fakeDeleter{}

	ev := Event{DID: "did:plc:a", Collection: keytrace.ClaimCollection, Rkey: "some-old-rkey", Operation: OpDelete}
	if err := HandleEvent(context.Background(), store, deleter, testVerifier(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if store.invalidated || len(deleter.deleted) != 0 {
		t.Fatalf("got invalidated=%v deleted=%v, want neither (this rkey isn't the tracked claim)", store.invalidated, deleter.deleted)
	}
}

func TestHandleEvent_CreateVerifiedRealClaim_Upserts(t *testing.T) {
	store := &fakeStore{}
	deleter := &fakeDeleter{}

	ev := Event{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", Collection: keytrace.ClaimCollection, Rkey: "3mkwoifsquv2p", Operation: OpCreate, Record: []byte(realClaimJSON)}
	if err := HandleEvent(context.Background(), store, deleter, testVerifier(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.upserts) != 1 || store.upserts[0].Subject != "76561197994000231" {
		t.Fatalf("got upserts=%+v", store.upserts)
	}
}

func TestHandleEvent_UpdateToNonVerifiedStatus_InvalidatesTrackedRecord(t *testing.T) {
	atURI := "at://did:plc:ephkzpinhaqcabtkugtbzrwu/dev.keytrace.claim/3mkwoifsquv2p"
	store := &fakeStore{claim: &appdb.SteamClaim{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", RecordURI: atURI}}
	deleter := &fakeDeleter{}

	retracted := strings.Replace(realClaimJSON, `"status":"verified"`, `"status":"retracted"`, 1)
	ev := Event{DID: "did:plc:ephkzpinhaqcabtkugtbzrwu", Collection: keytrace.ClaimCollection, Rkey: "3mkwoifsquv2p", Operation: OpUpdate, Record: []byte(retracted)}
	if err := HandleEvent(context.Background(), store, deleter, testVerifier(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if !store.invalidated || len(deleter.deleted) != 1 {
		t.Fatalf("got invalidated=%v deleted=%v, want both", store.invalidated, deleter.deleted)
	}
}

func TestHandleEvent_NonSteamType_Ignored(t *testing.T) {
	store := &fakeStore{}
	deleter := &fakeDeleter{}
	ev := Event{DID: "did:plc:a", Collection: keytrace.ClaimCollection, Rkey: "x", Operation: OpCreate, Record: []byte(`{"type":"github","status":"verified"}`)}
	if err := HandleEvent(context.Background(), store, deleter, testVerifier(), ev); err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if len(store.upserts) != 0 || store.invalidated {
		t.Fatalf("got upserts=%+v invalidated=%v, want neither", store.upserts, store.invalidated)
	}
}
```

- [ ] **Step 2: Confirm the tests fail, then implement**

```go
// internal/jetstream/handler.go
package jetstream

import (
	"context"
	"encoding/json"
	"time"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/keytrace"
)

type Operation string

const (
	OpCreate Operation = "create"
	OpUpdate Operation = "update"
	OpDelete Operation = "delete"
)

// Event mirrors one Jetstream commit. Record is nil for deletes — Jetstream
// carries no record content on a delete, only did/collection/rkey, which is
// why delete matching below is by AT-URI, not by inspecting a `type` field.
type Event struct {
	DID        string
	Collection string
	Rkey       string
	Operation  Operation
	Record     []byte
}

type Store interface {
	GetSteamClaim(ctx context.Context, did string) (*appdb.SteamClaim, error)
	UpsertSteamClaim(ctx context.Context, c appdb.SteamClaim) error
	InvalidateSteamClaim(ctx context.Context, did string) error
}

type StatusDeleter interface {
	DeleteStatus(ctx context.Context, did string) error
}

func HandleEvent(ctx context.Context, store Store, deleter StatusDeleter, verifier *keytrace.Verifier, ev Event) error {
	if ev.Collection != keytrace.ClaimCollection {
		return nil
	}
	atURI := "at://" + ev.DID + "/" + ev.Collection + "/" + ev.Rkey

	if ev.Operation == OpDelete {
		return invalidateIfTracked(ctx, store, deleter, ev.DID, atURI)
	}

	var claim keytrace.Claim
	if err := json.Unmarshal(ev.Record, &claim); err != nil {
		return nil // malformed record — ignore rather than fail the whole listener
	}
	if claim.Type != "steam" {
		return nil
	}

	if claim.Status == "verified" {
		ok, err := verifier.VerifyAttestation(ctx, ev.DID, claim)
		if err != nil {
			return err
		}
		if !ok {
			return nil // fails crypto verification — don't trust it, but don't touch an unrelated existing claim either
		}
		return store.UpsertSteamClaim(ctx, appdb.SteamClaim{
			DID: ev.DID, Subject: claim.Identity.Subject, DisplayName: claim.Identity.DisplayName,
			ClaimURI: claim.ClaimURI, RecordURI: atURI, LastVerifiedAt: time.Now(),
		})
	}

	// failed/retracted — only invalidate if this is the specific record we're
	// tracking, so an unrelated old claim being updated can't knock out a good one.
	return invalidateIfTracked(ctx, store, deleter, ev.DID, atURI)
}

func invalidateIfTracked(ctx context.Context, store Store, deleter StatusDeleter, did, atURI string) error {
	current, err := store.GetSteamClaim(ctx, did)
	if err != nil {
		return err
	}
	if current == nil || current.RecordURI != atURI {
		return nil
	}
	if err := store.InvalidateSteamClaim(ctx, did); err != nil {
		return err
	}
	return deleter.DeleteStatus(ctx, did)
}
```

- [ ] **Step 3: Run tests, commit**

```bash
go test ./internal/jetstream/...
git add internal/jetstream
git commit -m "feat: pure jetstream claim-event decision logic"
```

---

### Task 13: Jetstream connection manager (make-before-break)

Production Jetstream instances (`jetstream2.us-east.bsky.network`, confirmed live) currently serve the **legacy** wire protocol — plain `wss://.../subscribe?wantedCollections=...&wantedDids=...`, not the newer rewrite's `/xrpc/...` endpoint (confirmed by probing both paths against the real host: the legacy path works, the rewrite path 404s). The upstream legacy Go client (`bluesky-social/jetstream-legacy`) has a `go.mod` path mismatch that requires an awkward `replace` directive to use at all — this task hand-rolls the (small, stable, well-documented) wire protocol with `gorilla/websocket` instead, which is simpler and avoids depending on an oddly-packaged upstream module.

**Files:**
- Create: `internal/jetstream/listener.go`, `internal/jetstream/manager.go`, `internal/jetstream/manager_test.go`, `internal/jetstream/dbstore.go`
- Modify: `internal/api/steam_handlers.go` (add `Jetstream *jetstream.Manager`, trigger `Restart` on toggle)
- Modify: `cmd/server/main.go` (build the real handler chain, start the initial listener)

**Interfaces:**
- Consumes: `jetstream.HandleEvent`, `jetstream.Event`, `jetstream.Store`, `jetstream.StatusDeleter` (Task 12); `sync.ATProtoWriter.DeleteStatus` (Task 10, satisfies `StatusDeleter` structurally — no adapter needed).
- Produces: `jetstream.EventHandler` (`func(ctx, Event) error`); `jetstream.Listener` with `Close()`; `jetstream.Manager{host string, handler EventHandler, connect func(...) (*Listener, error)}`, `jetstream.NewManager(host string, handler EventHandler) *Manager`, `(*Manager) Restart(ctx, dids []string) error`, `(*Manager) Close()`; `jetstream.DBStore{Conn *sql.DB}` (implements `Store`).

- [ ] **Step 1: The real listener (one websocket connection)**

```go
// internal/jetstream/listener.go
package jetstream

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"

	"github.com/gorilla/websocket"
)

// wireEvent mirrors Jetstream's JSON message shape.
type wireEvent struct {
	DID    string `json:"did"`
	Kind   string `json:"kind"`
	Commit *struct {
		Operation  string          `json:"operation"`
		Collection string          `json:"collection"`
		Rkey       string          `json:"rkey"`
		Record     json.RawMessage `json:"record"`
	} `json:"commit"`
}

type EventHandler func(ctx context.Context, ev Event) error

type Listener struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func connect(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error) {
	u := url.URL{Scheme: "wss", Host: host, Path: "/subscribe"}
	q := u.Query()
	for _, c := range collections {
		q.Add("wantedCollections", c)
	}
	for _, d := range dids {
		q.Add("wantedDids", d)
	}
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return nil, err
	}

	listenCtx, cancel := context.WithCancel(ctx)
	l := &Listener{cancel: cancel, done: make(chan struct{})}
	go l.readLoop(listenCtx, conn, handler)
	return l, nil
}

func (l *Listener) readLoop(ctx context.Context, conn *websocket.Conn, handler EventHandler) {
	defer close(l.done)
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close() // unblocks the ReadMessage call below
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("jetstream read", "err", err)
			}
			return
		}

		var we wireEvent
		if err := json.Unmarshal(raw, &we); err != nil || we.Kind != "commit" || we.Commit == nil {
			continue
		}

		ev := Event{DID: we.DID, Collection: we.Commit.Collection, Rkey: we.Commit.Rkey, Operation: Operation(we.Commit.Operation), Record: we.Commit.Record}
		if err := handler(ctx, ev); err != nil {
			slog.Error("jetstream handler", "did", ev.DID, "err", err)
		}
	}
}

func (l *Listener) Close() {
	l.cancel()
	<-l.done
}
```

- [ ] **Step 2: Write the failing make-before-break ordering test**

```go
// internal/jetstream/manager_test.go
package jetstream

import "context"

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func TestManager_Restart_OpensNewBeforeClosingOld(t *testing.T) {
	var events []string

	fakeConnect := func(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error) {
		events = append(events, "connect")
		return &Listener{cancel: func() { events = append(events, "close") }, done: closedChan()}, nil
	}

	m := &Manager{host: "test", handler: func(ctx context.Context, ev Event) error { return nil }, connect: fakeConnect}

	if err := m.Restart(context.Background(), []string{"did:plc:a"}); err != nil {
		t.Fatalf("first Restart: %v", err)
	}
	if err := m.Restart(context.Background(), []string{"did:plc:a", "did:plc:b"}); err != nil {
		t.Fatalf("second Restart: %v", err)
	}

	want := []string{"connect", "connect", "close"} // the 2nd connect must precede closing the 1st listener
	if !equalStrings(events, want) {
		t.Fatalf("got %v, want %v", events, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

Add `"testing"` to the import block (present via the `t *testing.T` param — the snippet above omits the surrounding `import (...)` block for brevity; write it with `"context"` and `"testing"`).

- [ ] **Step 3: Confirm it fails, then implement the manager**

```go
// internal/jetstream/manager.go
package jetstream

import (
	"context"
	"sync"
)

type Manager struct {
	mu      sync.Mutex
	host    string
	handler EventHandler
	current *Listener
	connect func(ctx context.Context, host string, collections, dids []string, handler EventHandler) (*Listener, error)
}

func NewManager(host string, handler EventHandler) *Manager {
	return &Manager{host: host, handler: handler, connect: connect}
}

// Restart opens a new connection with the given DIDs BEFORE closing the old
// one. A gap here means a claim revocation could go unnoticed until the next
// daily sweep (spec: "make-before-break restart"). Duplicate events during
// the brief overlap are harmless — HandleEvent's upsert/invalidate are idempotent.
func (m *Manager) Restart(ctx context.Context, dids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	next, err := m.connect(ctx, m.host, []string{"dev.keytrace.claim"}, dids, m.handler)
	if err != nil {
		return err
	}

	old := m.current
	m.current = next
	if old != nil {
		old.Close()
	}
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		m.current.Close()
		m.current = nil
	}
}
```

Run: `go test ./internal/jetstream/...` — PASS.

- [ ] **Step 4: The DB-backed `Store` adapter**

```go
// internal/jetstream/dbstore.go
package jetstream

import (
	"context"
	"database/sql"

	appdb "github.com/jphastings/game-status/internal/db"
)

type DBStore struct{ Conn *sql.DB }

var _ Store = DBStore{}

func (s DBStore) GetSteamClaim(ctx context.Context, did string) (*appdb.SteamClaim, error) {
	return appdb.GetSteamClaim(ctx, s.Conn, did)
}
func (s DBStore) UpsertSteamClaim(ctx context.Context, c appdb.SteamClaim) error {
	return appdb.UpsertSteamClaim(ctx, s.Conn, c)
}
func (s DBStore) InvalidateSteamClaim(ctx context.Context, did string) error {
	return appdb.InvalidateSteamClaim(ctx, s.Conn, did)
}
```

- [ ] **Step 5: Wire it up — initial listener at boot, restart on toggle**

```go
// add to internal/api/steam_handlers.go's SteamHandlers struct
	Jetstream *jetstream.Manager
```

```go
// in SetEnabled, after the successful db.SetSteamEnabled call, before w.WriteHeader
	if h.Jetstream != nil {
		if dids, err := db.ListSteamEnabledDIDs(r.Context(), h.Conn); err == nil {
			go func() { _ = h.Jetstream.Restart(context.Background(), dids) }() // don't block the HTTP response on a reconnect
		}
	}
```

```go
// add to cmd/server/main.go, after the sync writer/ticker wiring
	jetHandler := func(ctx context.Context, ev jetstream.Event) error {
		return jetstream.HandleEvent(ctx, jetstream.DBStore{Conn: conn}, writer, verifier, ev)
	}
	jetManager := jetstream.NewManager("jetstream2.us-east.bsky.network", jetHandler)

	initialDIDs, err := db.ListSteamEnabledDIDs(context.Background(), conn)
	if err != nil {
		log.Fatalf("initial jetstream dids: %v", err)
	}
	if err := jetManager.Restart(context.Background(), initialDIDs); err != nil {
		log.Fatalf("start jetstream: %v", err)
	}
	steamHandlers.Jetstream = jetManager
```

Add `"github.com/jphastings/game-status/internal/jetstream"` to `steam_handlers.go`'s and `main.go`'s imports.

- [ ] **Step 6: Build, test, commit**

```bash
go get github.com/gorilla/websocket
go build ./...
go test ./...
git add -A
git commit -m "feat: jetstream realtime claim invalidation, make-before-break restarts"
```

- [ ] **Step 7: Manual verification (real websocket, not covered by Step 3's ordering test)**

Run the server, revoke a test claim on keytrace.dev (or delete the record directly), and confirm the corresponding `steam_claims` row and PDS status record disappear within a few seconds — not after the next 5-minute tick or the daily sweep.

---

### Task 14: Daily claim sweep

**Files:**
- Create: `internal/claims/sweep.go`, `internal/claims/sweep_test.go`
- Modify: `cmd/server/main.go` (start the 24h ticker)

**Interfaces:**
- Consumes: `db.ListSteamEnabledDIDs`, `db.GetSteamClaim`, `db.InvalidateSteamClaim` (Task 2); `keytrace.Verifier`, `keytrace.Claim` (Task 3); `jetstream.StatusDeleter` (Task 12, reused rather than duplicated — `sync.ATProtoWriter` already satisfies it structurally).
- Produces: `claims.RecordFetcher` interface; `claims.IndigoRecordFetcher{Dir identity.Directory}` (implements it); `claims.RunSweep(ctx, conn *sql.DB, fetcher RecordFetcher, verifier *keytrace.Verifier, deleter jetstream.StatusDeleter) error`.

This task reuses `realClaimDID`, `realSignerDID`, `realKeyJWK`, `realClaimJSON`, `fakeKeyFetcher` from Task 5's `discover_test.go` and `openTestDB` from the same file — same package, no redefinition needed.

- [ ] **Step 1: Write the failing tests against a fake record fetcher**

```go
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

type fakeSweepDeleter struct{ deleted []string }

func (f *fakeSweepDeleter) DeleteStatus(ctx context.Context, did string) error {
	f.deleted = append(f.deleted, did)
	return nil
}

func TestRunSweep_InvalidatesWhenRecordDeleted(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, "did:plc:a")
	appdb.SetSteamEnabled(ctx, conn, "did:plc:a", true)
	appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: "did:plc:a", Subject: "765", ClaimURI: "x", RecordURI: "at://did:plc:a/dev.keytrace.claim/abc", LastVerifiedAt: time.Now()})

	fetcher := fakeRecordFetcher{deleted: map[string]bool{"at://did:plc:a/dev.keytrace.claim/abc": true}}
	deleter := &fakeSweepDeleter{}

	if err := RunSweep(ctx, conn, fetcher, testVerifier(), deleter); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}

	got, err := appdb.GetSteamClaim(ctx, conn, "did:plc:a")
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if got != nil || len(deleter.deleted) != 1 {
		t.Fatalf("got claim=%+v deleted=%v, want invalidated", got, deleter.deleted)
	}
}

func TestRunSweep_LeavesValidClaimAlone(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	appdb.UpsertUser(ctx, conn, realClaimDID)
	appdb.SetSteamEnabled(ctx, conn, realClaimDID, true)
	err := appdb.UpsertSteamClaim(ctx, conn, appdb.SteamClaim{DID: realClaimDID, Subject: "76561197994000231", ClaimURI: "x", RecordURI: "real-uri", LastVerifiedAt: time.Now()})
	if err != nil {
		t.Fatalf("UpsertSteamClaim: %v", err)
	}

	var realClaim keytrace.Claim
	if err := json.Unmarshal([]byte(realClaimJSON), &realClaim); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	fetcher := fakeRecordFetcher{claims: map[string]*keytrace.Claim{"real-uri": &realClaim}}
	deleter := &fakeSweepDeleter{}

	if err := RunSweep(ctx, conn, fetcher, testVerifier(), deleter); err != nil {
		t.Fatalf("RunSweep: %v", err)
	}

	got, err := appdb.GetSteamClaim(ctx, conn, realClaimDID)
	if err != nil {
		t.Fatalf("GetSteamClaim: %v", err)
	}
	if got == nil || len(deleter.deleted) != 0 {
		t.Fatalf("got claim=%+v deleted=%v, want the claim left alone", got, deleter.deleted)
	}
}
```

- [ ] **Step 2: Confirm the tests fail, then implement**

```go
// internal/claims/sweep.go
package claims

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bluesky-social/indigo/api/agnostic"
	"github.com/bluesky-social/indigo/atproto/atclient"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"

	appdb "github.com/jphastings/game-status/internal/db"
	"github.com/jphastings/game-status/internal/jetstream"
	"github.com/jphastings/game-status/internal/keytrace"
)

type RecordFetcher interface {
	// FetchClaimRecord returns (nil, true, nil) if the record no longer
	// exists, (claim, false, nil) if it does, or a non-nil error for an
	// uncertain outcome (e.g. a network blip) that the caller should NOT
	// treat as deletion.
	FetchClaimRecord(ctx context.Context, atURI string) (claim *keytrace.Claim, deleted bool, err error)
}

type IndigoRecordFetcher struct{ Dir identity.Directory }

var _ RecordFetcher = IndigoRecordFetcher{}

func (f IndigoRecordFetcher) FetchClaimRecord(ctx context.Context, atURI string) (*keytrace.Claim, bool, error) {
	did, collection, rkey, ok := parseSweepAtURI(atURI)
	if !ok {
		return nil, false, fmt.Errorf("invalid claim at-uri: %s", atURI)
	}
	parsedDID, err := syntax.ParseDID(did)
	if err != nil {
		return nil, false, err
	}
	ident, err := f.Dir.LookupDID(ctx, parsedDID)
	if err != nil {
		return nil, false, err
	}

	client := atclient.NewAPIClient(ident.PDSEndpoint())
	resp, err := agnostic.RepoGetRecord(ctx, client, "", collection, did, rkey)
	if err != nil {
		if isRecordNotFoundSweep(err) {
			return nil, true, nil
		}
		return nil, false, err // uncertain — caller skips this pass rather than invalidating on a network blip
	}

	var c keytrace.Claim
	if err := json.Unmarshal(*resp.Value, &c); err != nil {
		return nil, false, err
	}
	return &c, false, nil
}

// isRecordNotFoundSweep — same caveat as internal/sync/writer.go's
// isRecordNotFound: indigo's exact error shape for this case wasn't
// confirmed while writing this plan; verify during implementation.
func isRecordNotFoundSweep(err error) bool {
	return err != nil && strings.Contains(err.Error(), "RecordNotFound")
}

func parseSweepAtURI(atURI string) (did, collection, rkey string, ok bool) {
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

// RunSweep re-verifies every steam-enabled user's claim once a day — pure
// reconciliation for whatever the Jetstream listener missed during a
// disconnect (spec: "daily sweep"), not the primary revocation mechanism.
func RunSweep(ctx context.Context, conn *sql.DB, fetcher RecordFetcher, verifier *keytrace.Verifier, deleter jetstream.StatusDeleter) error {
	dids, err := appdb.ListSteamEnabledDIDs(ctx, conn)
	if err != nil {
		return err
	}

	for _, did := range dids {
		claim, err := appdb.GetSteamClaim(ctx, conn, did)
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
			if err := invalidateSweptClaim(ctx, conn, deleter, did); err != nil {
				return err
			}
			continue
		}

		ok, err := verifier.VerifyAttestation(ctx, did, *c)
		if err != nil {
			continue
		}
		if !ok {
			if err := invalidateSweptClaim(ctx, conn, deleter, did); err != nil {
				return err
			}
		}
	}
	return nil
}

func invalidateSweptClaim(ctx context.Context, conn *sql.DB, deleter jetstream.StatusDeleter, did string) error {
	if err := appdb.InvalidateSteamClaim(ctx, conn, did); err != nil {
		return err
	}
	return deleter.DeleteStatus(ctx, did)
}
```

Run: `go test ./internal/claims/...` — PASS.

- [ ] **Step 3: Wire the 24h ticker into main.go**

```go
// add to cmd/server/main.go
	recordFetcher := claims.IndigoRecordFetcher{Dir: dir}
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := claims.RunSweep(context.Background(), conn, recordFetcher, verifier, writer); err != nil {
				slog.Error("daily claim sweep", "err", err)
			}
		}
	}()
```

- [ ] **Step 4: Build, full test suite, commit**

```bash
go build ./...
go test ./...
git add -A
git commit -m "feat: daily claim re-verification sweep"
```

---

## Self-Review

**Spec coverage:** Architecture (Task 1), auth & credential storage incl. inline refresh (Task 4), no handle/PDS caching (Task 4/10 — resolved live via indigo throughout), data model (Tasks 1-2, 7 extends `game_cache`), claim indexing incl. Jetstream + daily sweep (Tasks 5, 12-14), sync engine incl. idempotent writes/embed/unresolved-game handling (Tasks 9-10), frontend (Tasks 6, 11), testing approach (pure-function tests throughout: Tasks 9, 12; DB/handler tests: Tasks 2, 4-8, 11). The additional signature-verification requirement from the conversation (beyond the original spec) is covered by Task 3 and threaded through every place a claim gets trusted (Tasks 5, 12, 14). Deployment/hosting is explicitly out of scope per both documents.

**Placeholders:** none — every step has real, complete code; the one broken aside caught while drafting Task 10 was removed. Two spots are flagged as **unconfirmed, not placeholder**: the OAuth scopes list (Task 4) and the `RecordNotFound` error match (Tasks 10, 14) — both are concrete working code with an honest note on what to verify, not TODOs.

**Type consistency:** `db.CachedGame` gained `PageURL` before Task 7 needed it (schema fixed retroactively in Task 1/2, not left inconsistent). `db.SteamClaim`, `keytrace.Claim`, `sync.ActorStatus`/`Embed`/`EmbedExternal`, and `jetstream.Event`/`Store`/`StatusDeleter` are each defined once and reused with matching field names across every later task that touches them.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-08-29-steam-game-status-sync.md`. Two execution options:

1. **Subagent-Driven (recommended)** — a fresh subagent per task, with review between tasks and fast iteration.
2. **Inline Execution** — batch execution in this session using `executing-plans`, with checkpoints for review.

Which approach?
