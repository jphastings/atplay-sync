# Steam game-status sync — design

## Purpose

A small hosted app that keeps a user's `games.gamesgamesgamesgames.actor.status`
record on their own PDS up to date with what they're currently playing on
Steam, sourced from their `dev.keytrace.claim` (verified Steam identity).

Open to any atproto account. Discord is an explicit non-goal for this build —
Discord has no polling API for arbitrary-user presence (only a bot's Gateway
connection, scoped to shared guilds), so it's deferred until that UX is worth
building. The claims model stays type-agnostic enough that adding Discord
later doesn't require a schema change to the claims table, but nothing in
this app pretends to support it yet.

## Architecture

One Go binary: stdlib `net/http` for the API, the built frontend served as
embedded static assets (`embed.FS`), one SQLite file (`modernc.org/sqlite` —
pure Go, no cgo, keeps the binary self-contained), and two long-running
background goroutines (the Steam sync ticker, the Jetstream listener). No
separate worker process, job queue, or router library — nothing here
justifies them at this scale.

atproto plumbing (OAuth, DPoP, identity/PDS resolution, XRPC calls) goes
through [`bluesky-social/indigo`](https://github.com/bluesky-social/indigo)
rather than being hand-rolled — this is the one place hand-rolling would be
a real risk (DPoP-bound OAuth is genuinely easy to get subtly wrong), and a
maintained implementation already exists.

## Auth & credential storage

Sign-in is "Sign in with atproto" OAuth: user enters their handle, we resolve
their PDS, redirect through their PDS's OAuth server, get back a DPoP-bound
token set (via indigo's `atproto/auth/oauth`). This app is a **confidential
OAuth client** (holds a private key, publishes `client-metadata.json` at a
fixed HTTPS URL) — required because syncing happens unattended in the
background, not just during a browser session.

One login does two jobs: it proves who's signing in (we set an HttpOnly
session cookie mapping the browser session to their DID), and it's the
long-lived credential the sync engine later uses to write records. No
separate "connect atproto" step.

**Refresh is inline, not scheduled.** Each time a stored session is about to
be used — a sync tick, a claim recheck — refresh first if the token is close
to expiry. An account being synced gets touched at least every 5 minutes, so
this alone keeps it fresh; there's no value in a standalone refresh sweep for
accounts that aren't actively syncing, since nothing needs their token while
idle.

`ponytail: if some PDS turns out to expire idle refresh tokens faster than a
user re-enabling sync can recover from, add a periodic keep-alive refresh
then — not before there's evidence it's needed.`

We do **not** cache handle or PDS endpoint. Both can change (handle renames,
PDS migration) independently of anything we're doing, so a local copy is a
correctness risk with a real failure mode: writes silently going to the
wrong place. Handle is resolved live for display; PDS endpoint is resolved
live via indigo's identity resolver (which already does its own sane
internal caching) at the moment of each XRPC call.

## Data model (SQLite)

```
users(did PK, created_at)

oauth_sessions(did PK/FK, token_set BLOB, expires_at, updated_at)
  -- opaque indigo session (access + refresh token, DPoP key)

steam_claims(did PK/FK, subject, display_name, claim_uri, record_uri, last_verified_at)
  -- one verified Steam dev.keytrace.claim per user; subject = SteamID64.
  -- row absent = not eligible to sync, regardless of sync_prefs.

sync_prefs(did PK/FK, steam_enabled BOOL)
  -- user intent, kept separate from claim validity (steam_claims presence)
  -- so the UI can say "enabled, but your claim isn't valid" rather than
  -- silently flipping the toggle behind the user's back.

session_starts(did FK, source, game_key, started_at, UNIQUE(did, source))
  -- the ONLY non-idempotent local state. "When did this session start" isn't
  -- recoverable from "what's being played right now" alone, so it's the one
  -- thing worth remembering locally. `source` stays a column (not hardcoded
  -- to "steam") so a future sync source doesn't need a schema change.

game_cache(steam_id PK, game_uri, name, cached_at)
  -- memoizes cartridge.dev getGame(steamId) lookups (TTL ~24h). This isn't
  -- identity state like handle/PDS above — it's caching an external,
  -- essentially-static fact purely to avoid hammering cartridge.dev on
  -- every tick for popular games.
```

No table mirrors "what's currently in the PDS record" — the PDS is the
authority on that, and every write below is idempotent enough not to need
one.

## Claim indexing

**Discovery** (finding a verified Steam claim for a DID we don't already
have one for) happens on demand only: once at first sign-in, and via a
"Recheck claim" button on the settings page (the user verifies on
keytrace.dev, a separate site, then comes back here). Both do
`com.atproto.repo.listRecords` on `dev.keytrace.claim`, keep the first
`type == "steam" && status == "verified"` record, upsert `steam_claims`.

**Ongoing validity**, once a claim is known, is event-driven:

- A goroutine holds a Jetstream websocket subscription:
  `wantedCollections=dev.keytrace.claim`, `wantedDids=<DIDs with
  steam_enabled=true>`. This delivers events for *all* claim types on a
  watched DID, not just Steam, so the handler has to filter itself:
  - **Delete event**: these carry no record content (it's gone), only
    `{did, collection, rkey}`. Match by `rkey`/`record_uri` against the
    stored `steam_claims` row (not by `type` — it isn't present on a delete
    payload). A match means: delete the `steam_claims` row and immediately
    `deleteRecord` that DID's live status record, rather than waiting for it
    to go stale.
  - **Create/update event**: these do carry the full record, so filter on
    `type == "steam"` directly. `status == "verified"` → upsert
    `steam_claims` straight from the event payload (no extra fetch needed).
    `status != "verified"` (failed/retracted) → same invalidation as a
    delete match above. Anything of another `type` is ignored.
- The watched-DID set changes whenever a user enables or disables Steam
  sync. Jetstream connections are cheap enough that each change gets a
  **make-before-break restart**: open a new connection with the full updated
  `wantedDids`, confirm it's live, *then* close the old one. Duplicate events
  during the brief overlap are harmless (both handlers above are
  idempotent). A missed event is not harmless — the only backstop is the
  daily sweep, so a gap means up to 24h of syncing on a dead claim.
- A **daily sweep** (`getRecord` on the stored `claim_uri`, per
  steam-enabled user) is pure reconciliation for whatever Jetstream missed
  during a disconnect — not the primary mechanism, so it doesn't need to run
  more often than that.

## Sync engine

Every 5 minutes, for every user with `sync_prefs.steam_enabled` and a
`steam_claims` row:

```
1. Steam Web API: GetPlayerSummaries, batched up to 100 SteamIDs per call
   across all eligible users this tick (one server-owned API key).

2. not currently playing:
     deleteRecord(self)   -- idempotent; treat "not found" as success
     clear session_starts row for (did, "steam")

3. currently playing appid X:
     session_starts(did, "steam").game_key == X ?
       yes -> reuse started_at
       no  -> started_at = now; upsert game_key = X
     resolve X -> game via game_cache, else cartridge.dev getGame(steamId: X)
       not resolvable -> skip the write this tick (session_starts is already
                          updated, so createdAt is still correct if/when it
                          resolves on a later tick)
       resolvable ->
         putRecord(self, {
           game: <resolved at-uri>, platform: "steam",
           playing: {},   -- asserts "playing, not watching"; no party info
                           -- in v1 (see below)
           createdAt: started_at,
           staleAt: now + buffer,   -- comfortably longer than the 5-min tick
           embed: { $type: "app.bsky.embed.external",
                     external: { uri: <cartridge game page>, title: <name>,
                                  description: <cartridge summary, or "" if none } },
                           -- description is required by app.bsky.embed.external's
                           -- own schema even though it adds little here; no
                           -- thumbnail in v1 since that needs a blob upload to
                           -- the user's own PDS, which only pays for itself
                           -- once there's a reason to add it
         })
```

Every branch is safe to repeat verbatim given the same Steam response — no
read of the previous PDS record anywhere in this loop.

**Party/co-players are out of scope for v1.** The only signal Steam's public
API offers — "a friend is also on the same appid right now" — isn't a
reliable proxy for "playing together" (a popular release day makes it fire
constantly for people who've never met). `playing.party` stays unpopulated
until there's a real signal for it; `dev.keytrace.reverseLookup` exists and
would be the right tool if one shows up later, but nothing here is built
against it now.

**Unresolved games are skipped silently**, logged server-side for
visibility. Revisit surfacing this in the UI if it turns out to happen a
lot.

## Frontend

Vite + pnpm + TypeScript, no framework — one page, calling the Go JSON API
with the session cookie. Signed out: handle input, "Sign in with atproto".
Signed in:

- Steam claim status: not connected / verified as `<displayName>` / claim
  invalid, please re-verify at keytrace.dev — plus a "Recheck claim" button.
- An enable/disable toggle, disabled until a valid claim exists.
- Current status: the backend does a live `getRecord` against the user's own
  PDS when the page loads (consistent with treating the PDS as authoritative
  everywhere else) rather than mirroring it locally.

No priority/multi-source UI — there's exactly one syncable source right now,
so a priority picker would be a toggle with one option that always wins.
Add it, and the `sync_prefs` column it needs, when Discord sync actually
ships.

## Testing

The highest-value tests are the two pure decision functions, neither of
which needs network access to test:

- The sync-tick decision table in step 2/3 above: given a Steam play-state,
  a `session_starts` row, and a resolved-or-not game, what gets written.
- The Jetstream event handler: given a claim event, what happens to
  `steam_claims` and the live status record.

OAuth/Jetstream/Steam-API glue is thin, mostly integration-shaped, and not
worth heavy unit coverage. The frontend is one page with no real client
logic beyond fetch calls — no dedicated test suite needed there.

## Build order

1. Data model, atproto OAuth sign-in, session cookie, claim discovery
   (listRecords + "Recheck claim"), settings page shell.
2. Sync engine (Steam tick, game resolution/cache, idempotent writes),
   enable/disable toggle, live status display.
3. Jetstream realtime claim invalidation + daily sweep.

## Open items (deployment-time, not design-time)

- A public HTTPS domain (required for the OAuth client-metadata URL and
  callback).
- A Steam Web API key (server-owned, one env var).
- A place to run one Go binary with a persistent volume for the SQLite file.
  Not decided here — out of scope for this doc.
