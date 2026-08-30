# Discord game-status sync — design

## Purpose

Extend the existing Steam sync (see
[2026-08-29-steam-game-status-sync-design.md](2026-08-29-steam-game-status-sync-design.md))
with a second source: what a user is playing according to Discord's presence,
sourced from a verified `dev.keytrace.claim` of type `discord`. That earlier
doc anticipated this — "the claims model stays type-agnostic enough that
adding Discord later doesn't require a schema change" — but the shipped
implementation split into Steam-specific tables (`steam_claims`,
`sync_prefs.steam_enabled`) rather than a generic one. This design finishes
that generalization rather than bolting a parallel `discord_claims` table
onto the Steam-specific one.

A user may enable either source, both, or neither. When both are enabled and
both are simultaneously "playing," a per-user drag-to-reorder priority list
decides which one is shown; the loser is simply not reflected in the record
until the winner stops.

## Why Discord is fundamentally different from Steam

Steam has a polling API: given a SteamID, ask "what are they playing right
now," on our schedule. Discord has no equivalent for arbitrary users —
presence is only delivered over a bot's persistent Gateway (websocket)
connection, and only for members of a server (guild) the bot is in. So where
Steam sync is a scheduled poll, Discord sync is an always-on event stream,
and getting a user's presence at all requires them to join a server we
control.

**Privacy**, given that requirement: the tracking server is deliberately
inert. `@everyone` gets no channel visibility at all, so there's no member
list to browse and nothing to read — joining is pure plumbing to satisfy
Discord's guild-scoping rule, not a place anyone hangs out. Onboarding
(the linking instructions a Steam user gets via keytrace.dev) happens by DM
on join, since there's no shared channel to post it in. Presence delivery
over the Gateway is guild-scoped, not channel-scoped, so none of this
affects whether the bot receives it.

## Linking: keytrace's `discord` claim, and its gap

keytrace already ships a `discord` provider (confirmed by reading
[orta/keytrace's source](https://github.com/orta/keytrace/blob/main/packages/runner/src/serviceProviders/discord.ts)
and a live claim record), but it proves something different from Steam's:
you set a Discord server's name/description to your DID, create a permanent
invite, and paste it. keytrace resolves `identity.subject` to
`inviter.username` — the **Discord username of whoever created the invite**,
not a snowflake ID.

That's a problem for us on two counts:

1. Presence events are keyed by Discord's immutable snowflake user ID, not
   username. We need the ID, and the claim record doesn't carry a *signed*
   one.
2. A real claim record, read live off a PDS during this design session, has
   `signedFields: ["claimUri", "did", "identity.subject", "type"]`. The
   record also has `identity.profileUrl` (`https://discord.com/users/<id>`)
   with the snowflake in it — but that field isn't in `signedFields`. It's
   metadata the keytrace client filled in automatically, not something the
   keytrace server's signature vouches for. Since claim records live in the
   claimant's own PDS repo, they could edit `profileUrl` to name anyone's
   Discord ID and the signature would still verify. Only `identity.subject`
   (the username) is load-bearing.

**Resolution**, done entirely on our side, no keytrace changes needed: once
a claim's signature checks out (unchanged `Verifier.VerifyAttestation` —
this generalizes across claim types for free, it never hardcoded "steam"),
resolve the *username* it signed against our own bot's live guild-member
list.

- Fast path: parse the snowflake out of the unsigned `profileUrl`, look that
  ID up directly in our member cache. If present and its *current* username
  matches the *signed* `identity.subject`, trust it — cheap, and the
  security property rests entirely on the signed field, not the hint.
- Fallback (missing/stale/tampered `profileUrl`): scan the member cache for
  a username match instead. Discord usernames are globally unique (the 2023
  unification), so a match is unambiguous.
- No match either way (they haven't joined the tracking server yet, or
  changed their username since verifying) → treated the same as "no valid
  claim," same as an unrecognized Steam claim today. The settings UI gets a
  Discord-specific prompt for this state ("join the server" vs. Steam's
  "link your account"), refined during implementation.

This is worth one upstream note (a GitHub issue on `orta/keytrace`
suggesting `inviter.id` be signed alongside the username, mirroring why the
Steam provider signs a stable `steamID64` instead of a changeable vanity
URL) — not a blocker, and not a PR without sign-off.

## Game resolution: reuses the Steam pipeline, not fuzzy matching

Confirmed live during this design session: Discord publishes an
unauthenticated `GET /api/v10/applications/detectable` — ~24k locally
detectable games. **18,607 of them (77%) carry an exact Steam App ID** in a
`third_party_skus` field, and Discord stamps that same entry's `id` as
`application_id` on the presence activity it sends over the Gateway.

So: cache that list (refresh daily, plain HTTP, no auth), and on a "Playing"
activity, map `application_id` → Steam App ID → hand straight to the
**existing** `GameResolver.GetGameBySteamID`. Zero new game-data
integration; Discord-sourced sessions get the same cover art and cartridge
page Steam ones do, because they resolve through the identical Steam App ID
space.

For the ~23% with no Steam SKU, or a presence with no `application_id` at
all (e.g. a bare custom status, or a game with no Steam release): skip the
write, same as Steam already does today when a game can't be resolved. No
fuzzy title matching in v1 — real complexity for a minority tail; revisit if
the skip rate turns out to matter in practice.

One consequence worth naming: because both sources ultimately resolve to a
Steam App ID before they ever reach `session_starts`, the reconciler below
needs exactly one `GameResolver`, not one per source.

## Data model changes

`steam_claims` and `sync_prefs.steam_enabled` generalize to per-source
tables, replacing rather than sitting alongside the Steam-specific ones —
existing call sites (`claims/discover.go`, `claims/sweep.go`,
`jetstream/handler.go`, `api/steam_handlers.go`, `sync/tick.go`,
`db/invalidate.go`) all touch these and need updating regardless, so there's
no cost saved by leaving Steam's shape untouched and duplicating it for
Discord.

```sql
-- Replaces steam_claims. One verified claim per (did, type).
CREATE TABLE claims (
  did              TEXT NOT NULL REFERENCES users(did),
  type             TEXT NOT NULL,   -- "steam" | "discord"
  subject          TEXT NOT NULL,   -- SteamID64, or Discord snowflake once resolved
  display_name     TEXT NOT NULL,
  claim_uri        TEXT NOT NULL,
  record_uri       TEXT NOT NULL,
  last_verified_at TEXT NOT NULL,
  PRIMARY KEY (did, type)
);

-- Replaces sync_prefs.steam_enabled. One row per (did, source); priority
-- also lives here rather than a separate ordering table, since it only
-- ever needs to be read alongside enabled state.
CREATE TABLE sync_prefs (
  did      TEXT NOT NULL REFERENCES users(did),
  source   TEXT NOT NULL,    -- "steam" | "discord"
  enabled  INTEGER NOT NULL DEFAULT 0,
  priority INTEGER NOT NULL, -- lower = higher priority among ENABLED rows
  PRIMARY KEY (did, source)
);
```

`session_starts` needs no change — `source` was already a plain column, not
hardcoded, per the original design.

No new table for the guild-member cache used in linking resolution above:
it's rebuilt fresh from Discord's own member list on every Gateway
(re)connect, so it lives in memory (a mutex-guarded `map[snowflake]username`
in the new Discord package), not SQLite. Nothing is lost by not persisting
it across restarts.

## The reconciler

Today, `sync.RunTick`'s `tickOne` decides *and* writes in one step. With two
independently-updating sources sharing one PDS record, deciding what's
publicly visible has to become a separate step both sources go through:

```
Reconcile(did):
  for source in enabled sources for did, in priority order:
    if session_starts(did, source) exists:
      game := resolver.GetGameBySteamID(session_starts.game_key)
      if game != nil:
        writer.PutStatus(did, ...)   // using this source's createdAt
        return
      // unresolved game: fall through to the next-priority source, same as
      // "not currently playing" from this source's point of view
  writer.DeleteStatus(did)
```

Both write paths change to: update only your own `session_starts(did,
source)` row using the existing pure `sync.Decide` function (unchanged —
Steam's tick and Discord's event handler both feed it `playing, gameKey,
prev, now` and get back the same `ActionNone/Delete/Write` decision), then
call `Reconcile(did)`. Neither calls `writer.Put/DeleteStatus` directly
anymore.

`db.InvalidateClaim`'s last step changes from unconditionally deleting the
PDS record to calling `Reconcile(did)` instead. This matters now in a way it
didn't when there was one source: today, losing your Steam claim always
means "nothing to show." Once Discord exists, losing your *Discord* claim
while Steam is actively playing must not blank the record — it should fall
through to Steam. Unconditional delete was only ever correct by coincidence
of there being one source.

Triggers for `Reconcile(did)`: after a Steam tick's `tickOne`, after a
Discord presence event's session update, after `SetEnabled` (either
source), after a priority reorder, after `InvalidateClaim`, and after a
guild-member-remove (below).

## Discord Gateway connection

Recommend adding [`bwmarrin/discordgo`](https://github.com/bwmarrin/discordgo)
rather than hand-rolling the Gateway protocol. This is the same call the
existing Steam design already made for atproto ("this is the one place
hand-rolling would be a real risk... a maintained implementation already
exists") — Jetstream's listener was reasonable to hand-roll because it's a
plain JSON-lines-over-websocket firehose with no protocol state; Discord's
Gateway has heartbeat/ack tracking, sequence numbers, resume-vs-reconnect,
and privileged-intent identify payloads, which is a meaningfully bigger
surface to get subtly wrong.

Unlike Jetstream, this needs no `Manager`/restart-on-change pattern: Discord
has no server-side "watch list" to update. Once connected with
`GUILD_PRESENCES` and `GUILD_MEMBERS` intents, the bot receives every
presence and member event for the whole guild for as long as the connection
lives; discordgo's `Session.Open()` handles reconnect/resume internally. So
this is: one `discordgo.Session` opened in a goroutine at startup, with
handler funcs registered for `PresenceUpdate`, `GuildMemberAdd`,
`GuildMemberRemove`, and `GuildMemberUpdate` (keeps the username cache
current for members who rename without leaving).

- `PresenceUpdate` for a member with a verified+enabled Discord claim: find
  a `type == 0` ("Playing") activity, resolve `application_id` per the game
  resolution section, run `Decide`, update `session_starts`, `Reconcile`.
- `GuildMemberAdd`: DM the linking instructions (mirrors keytrace.dev's role
  for Steam); also worth an immediate presence check against whatever
  Discord includes in the member-add payload, so someone who joins already
  mid-session doesn't wait for their next presence change to show up.
- `GuildMemberRemove`: treat like a revoked claim — clear the `claims` row
  for `(did, "discord")` if one maps to this snowflake, disable
  `sync_prefs` for that source, clear `session_starts(did, "discord")`,
  `Reconcile(did)`.
- `GuildMemberUpdate`: keep the in-memory username cache correct (feeds the
  linking-resolution fallback above).

The detectable-games list fetch is a plain unauthenticated HTTP GET, unrelated
to discordgo's REST wrapper — fetched directly with `net/http`, same as the
Steam client.

No call-budget type is needed on the Discord side (unlike `steam.Budget`) —
there's no polling, so no self-imposed daily ceiling to enforce.

## API & frontend changes

- `POST /api/discord/recheck`, `POST /api/discord/enabled` — mirror the
  existing Steam handlers exactly, generalized over the new `claims`/
  `sync_prefs` shape.
- `POST /api/sync/order` — persists a priority reorder (writes `priority`
  for the caller's `sync_prefs` rows), then `Reconcile`s.
- `/api/me` gains `discordSubject`/`discordDisplayName`/`discordEnabled`,
  and an ordered list of sources for the frontend to render.
- Second toggle row (Discord logo, mirrors the existing Steam one) with
  drag handles; disabled rows always sort below enabled ones, matching the
  agreed UX. Two items only — plain HTML5 drag-and-drop covers it, no
  library.
- Dev-mock fixtures extended for the Discord states (unlinked / verified /
  both-enabled-different-priority).

## Config / ops

New required env vars, alongside the existing `STEAM_API_KEY`:
`DISCORD_BOT_TOKEN`, `DISCORD_GUILD_ID` (the tracking server), and
`DISCORD_INVITE_URL` (shown in the "join the server" UI prompt, the Discord
equivalent of today's keytrace.dev link).

## Testing

Same shape as the Steam design's testing section: the two pure decision
points are the highest-value tests and need no network —

- `Reconcile`'s priority-walk given a set of enabled sources, their
  `session_starts` rows, and resolvability.
- The linking-resolution logic (signed subject vs. cache, with and without
  a usable `profileUrl` hint).

`sync.Decide` is already tested and unchanged. Gateway/OAuth/HTTP glue
stays thin and integration-shaped, same call as before.

## Out of scope for v1

- Fuzzy title matching for the ~23% of Discord activities with no Steam SKU.
- More than two sources — the reconciler and `sync_prefs.priority` design
  already generalize to N without a further schema change, just not needed
  yet.
- Raising the upstream keytrace issue about signing `inviter.id` — flagged
  above, JP's call whether/when.

## Open items (deployment-time, not design-time)

- The Discord bot token, guild ID, and invite link (JP is creating the
  tracking server and bot application).
