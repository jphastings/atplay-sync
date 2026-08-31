# Per-game status records — design

## Purpose

Today `games.atmosphere.status` is a singleton per user: rkey is the literal
string `"self"`, and `Reconcile` picks one winning source and writes (or
deletes) that one record. This breaks down the moment two sources are
simultaneously reporting *different* games — only one can ever be shown.

Switch the rkey from `"self"` to the rkey of the game record being played
(`games.gamesgamesgamesgames.game`). One status record per distinct game
currently being played, across all enabled sources. Reordering (source
priority) still matters, but its job narrows: deciding whose metadata wins
when two sources report the *same* game, not deciding what's visible at all.

The lexicon needs no change — `status`'s `key` is already `"any"`
([lexicons/games.atmosphere/status.json](../../../lexicons/games.atmosphere/status.json));
`"self"` was always a Go-side convention, not a schema constraint.

## Reconcile: from priority-walk to desired-set diff

Today (`internal/sync/reconcile.go`): walk enabled sources in priority
order, write the first resolvable one, return; if none resolve, delete the
one record. That collapses to a single winner by construction.

New shape — compute what *should* exist, compare against what *does* exist,
reconcile the difference:

```
Reconcile(did, now):
  desired := {}  // rkey -> ActorStatus
  for source in enabled sources for did, in priority order:
    row := session_starts(did, source)
    if row == nil: continue
    game := resolver.GetGameBySteamID(row.game_key)
    if game == nil: continue          // unresolvable — same as "not playing" for this source
    rkey := gameRkey(game.URI)
    if rkey in desired: continue      // a higher-priority source already claimed this game
    desired[rkey] = ActorStatus{... using row.StartedAt, game.*}

  live := writer.ListStatuses(did)    // []{Rkey, StaleAt} — read straight off the PDS
  for entry in live:
    if entry.Rkey not in desired:
      writer.DeleteStatus(did, entry.Rkey)
  for rkey, status in desired:
    writer.PutStatus(did, rkey, status)
```

Two sources reporting the *same* game resolve to the same rkey, so the
priority walk's "first source to claim a rkey wins" is exactly last design's
"highest priority wins outright" — just scoped per-game instead of
per-account. `gameRkey` is a small unexported helper (`at://did/collection/rkey`
parsing) matching the pattern already duplicated in
[internal/keytrace/keyfetch.go](../../../internal/keytrace/keyfetch.go) and
[internal/claims/sweep.go](../../../internal/claims/sweep.go) — a third
private copy in `internal/sync`, consistent with the existing convention,
rather than extracting a shared package for this change to own.

**Game A → game B needs no special case.** The old rkey simply stops
appearing in `desired` (deleted), the new one appears (created). This also
means the `"self"`→game-rkey migration is free: on the first `Reconcile`
after deploy, any lingering `"self"` record shows up in `live` but never in
`desired` (nothing ever proposes that rkey again), so it's deleted like any
other stale entry. No migration script.

`UpdateSession` (`internal/sync/reconcile.go:68`) is unchanged in shape — it
still only touches its own source's `session_starts` row, then calls
`Reconcile`. `db.InvalidateClaim`'s `Reconcile` call is likewise unchanged.

## Writer interface

```go
type RecordWriter interface {
    PutStatus(ctx context.Context, did, rkey string, status ActorStatus) error
    DeleteStatus(ctx context.Context, did, rkey string) error
    ListStatuses(ctx context.Context, did string) ([]StatusEntry, error)
}

type StatusEntry struct {
    Rkey    string
    StaleAt time.Time
}
```

`ATProtoWriter` (`internal/sync/writer.go`) implements `ListStatuses` via
`agnostic.RepoListRecords` on `StatusCollection`, through the same
authenticated `withClient` session `Put`/`Delete` already use — one client,
no new plumbing, and it happens to also work unauthenticated (open CORS,
per CLAUDE.md) if that's ever useful later, but reusing the existing session
is the least code today.

## Daily stale sweep

A pure safety net, in case a `Reconcile` call was ever missed (crash,
process restart mid-tick, a source disabled without ever passing through
"not playing") — same rationale and shape as the existing
[internal/claims/sweep.go](../../../internal/claims/sweep.go)'s daily claim
re-verification.

```go
// internal/sync/sweep.go
func RunStatusSweep(ctx context.Context, conn *sql.DB, writer RecordWriter, now time.Time) error {
    for _, did := range appdb.ListAllDIDs(ctx, conn) {
        entries, err := writer.ListStatuses(ctx, did)
        ...
        for _, e := range entries {
            if e.StaleAt.Before(now) {
                writer.DeleteStatus(ctx, did, e.Rkey)
            }
        }
    }
}
```

Scoped to `ListAllDIDs` (`internal/db/users.go:51`, every signed-in user),
not `ListEnabledDIDs` — this exists to catch records that outlived their
owner's current sync state, so it shouldn't assume that state is trustworthy.
No game resolution needed; it only ever looks at `staleAt`.

Wired into `cmd/server/main.go` as its own 24h ticker, next to the existing
claims-sweep one (`main.go:177`) — same pattern, not merged into it (different
collection, different writer, no shared logic beyond "loop DIDs daily").

## Frontend

`web/src/atproto.ts`'s `resolveLiveStatus` reads a single record at rkey
`'self'`. Becomes `resolveLiveStatuses`: `com.atproto.repo.listRecords` on
`games.atmosphere.status` for the viewed DID, filter to entries with
`staleAt` still in the future, map each to a `LiveStatus` (same per-record
logic as today — resolve the linked game record, cover art), return
`LiveStatus[]`.

`web/src/main.ts`'s `loadLiveStatus`/`renderHero` change from one status to
a list: render the existing hero-card markup once per live status; the
existing empty-state markup covers the zero-length case unchanged. No new
layout system — stacked cards, same card design repeated.

`web/src/devmock.ts` fixtures move from `LiveStatus | null | 'error'` to
`LiveStatus[] | 'error'` (empty array standing in for "nothing playing"),
plus a new fixture with two simultaneous entries to exercise the list.

## Testing

- `Reconcile`'s desired-set diff: same-game priority resolution (already
  covered conceptually by the existing
  [reconcile_test.go](../../../internal/sync/reconcile_test.go) cases, ported
  to assert on rkeys/puts-and-deletes instead of a single winner), plus new
  cases for *different* games from different sources producing two puts, and
  a game switch producing one delete + one put.
- `RunStatusSweep`: a fake `RecordWriter` with mixed stale/live entries
  across DIDs — asserts exactly the stale ones get deleted.
- `gameRkey` parsing: valid at-uri, malformed input.
- Frontend: none of the existing test infra covers `atproto.ts` today
  (manual/dev-mock verification only) — no change to that in this design.

## Out of scope

- Deduplicating the three now-near-identical at-uri parsers
  (`keytrace`, `claims`, `sync`) into a shared helper — pre-existing
  duplication, not something this change should refactor in passing.
- Any UI treatment beyond stacking existing cards (e.g. a combined "playing
  2 games" summary view) — revisit if multiple simultaneous entries turn out
  to be common enough to want a denser layout.
