# Discord username-reuse hijack fix — report

## What was implemented

Added a `ConfirmDiscordSubject` method to both (deliberately duplicated)
`SubjectResolver` interfaces and its real implementation, then rewired the
three call sites so that full resolution (`ResolveDiscordSubject`, which
scans the guild member cache by username and can land on a *different*
snowflake) only ever runs when no `claims` row exists yet for `(did,
"discord")`. When a row already exists, re-verification now only confirms
the *stored* snowflake is still valid — never re-derives one.

The actual code matched the task's snippets almost exactly; no material
discrepancies. Minor notes below.

### `internal/discord/resolve.go`

```go
func (r *ClaimResolver) ConfirmDiscordSubject(ctx context.Context, claim keytrace.Claim, currentSubject string) bool {
	username, ok := r.Members.Username(currentSubject)
	return ok && strings.EqualFold(username, claim.Identity.Subject)
}
```
Added exactly as specified, alongside the existing `ResolveDiscordSubject`.

### `internal/claims/discover.go`

`SubjectResolver` interface gained the `ConfirmDiscordSubject` method
(doc comment as specified). `Discover`'s per-type loop's Discord branch:

```go
subject := fc.claim.Identity.Subject
if claimType == appdb.DiscordSource {
	existing, err := appdb.GetClaim(ctx, conn, did, appdb.DiscordSource)
	if err != nil {
		return err
	}
	if existing != nil {
		if !resolver.ConfirmDiscordSubject(ctx, fc.claim, existing.Subject) {
			if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
				return err
			}
			continue
		}
		subject = existing.Subject
	} else {
		resolved, ok := resolver.ResolveDiscordSubject(ctx, did, fc.claim)
		if !ok {
			continue // verified, but not resolvable yet — leave any prior state alone
		}
		subject = resolved
	}
}
```
Matches the spec's Step 3 exactly.

### `internal/claims/sweep.go`

`RunSweep`'s loop already had `claim` (existing DB row, fetched near the
top via `appdb.GetClaim`) and `c` (freshly re-fetched-from-PDS claim) in
scope — variable names matched the task description exactly, no renaming
needed:

```go
subject := c.Identity.Subject
if claimType == appdb.DiscordSource {
	if !resolver.ConfirmDiscordSubject(ctx, *c, claim.Subject) {
		if err := appdb.InvalidateClaim(ctx, conn, reconciler, did, claimType, time.Now()); err != nil {
			return err
		}
		continue
	}
	subject = claim.Subject
}
```
Matches Step 4 exactly (comment reworded slightly to explain the
never-re-resolve invariant, per the task's own comment text).

### `internal/jetstream/handler.go`

`SubjectResolver` interface gained `ConfirmDiscordSubject`. `HandleEvent`'s
verified-status branch:

```go
subject := claim.Identity.Subject
if claim.Type == appdb.DiscordSource {
	existing, err := store.GetClaim(ctx, ev.DID, appdb.DiscordSource)
	if err != nil {
		return err
	}
	if existing != nil {
		if !resolver.ConfirmDiscordSubject(ctx, claim, existing.Subject) {
			return invalidateIfTrackedType(ctx, store, ev.DID, appdb.DiscordSource, atURI)
		}
		subject = existing.Subject
	} else {
		resolved, ok := resolver.ResolveDiscordSubject(ctx, ev.DID, claim)
		if !ok {
			return nil
		}
		subject = resolved
	}
}
```
Matches Step 5 exactly. `invalidateIfTrackedType` already guards on
`current.RecordURI == atURI`, so an update event only invalidates the
record it's actually about.

## Test fakes updated

Each package's `fakeSubjectResolver` (three separate copies: `internal/claims`
shares one between `discover_test.go`/`sweep_test.go`; `internal/jetstream`
has its own) gained:

- a `confirmed map[string]bool` field — `nil` map defaults to `true`
  (existing tests that don't care about this behavior needed zero changes);
  a non-nil map does an explicit lookup so a test can force `false` for a
  specific subject.
- `ConfirmDiscordSubject(ctx, claim, currentSubject) bool` implementing that.

Plus, in both `internal/claims/discover_test.go` (shared with
`sweep_test.go`, same package) and `internal/jetstream/handler_test.go`, a
small wrapper type:

```go
type resolveDiscordSubjectPanics struct{ fakeSubjectResolver }

func (resolveDiscordSubjectPanics) ResolveDiscordSubject(ctx context.Context, did string, claim keytrace.Claim) (string, bool) {
	panic("ResolveDiscordSubject must not be called when a Discord claim row already exists")
}
```
This directly proves the security property — the fake crashes the test if
the fix's code path ever falls through to full resolution when a row exists
— rather than only inferring it from the outcome.

## New tests added

- `internal/claims/discover_test.go`:
  `TestDiscover_DiscordExistingClaimNoLongerConfirms_Invalidates` — seeds an
  existing resolved Discord claim row, re-discovers the same (still
  `"jphastings"`-signing) claim record with a resolver configured to reject
  that stored snowflake and to panic if `ResolveDiscordSubject` is called.
  Asserts the row is invalidated (`GetClaim` returns nil).

- `internal/claims/sweep_test.go`: renamed/rewrote the existing
  `TestRunSweep_DiscordNoLongerResolvable_Invalidates` (which tested the
  *old* code path via `ResolveDiscordSubject` failing — no longer reachable
  once a row exists, since the sweep never calls it in that case anymore)
  into `TestRunSweep_DiscordNoLongerConfirms_Invalidates`, same shape as
  above: an already-resolved claim whose confirm fails, using the
  panic-on-resolve fake, gets invalidated.

- `internal/jetstream/handler_test.go`:
  `TestHandleEvent_UpdateToAlreadyTrackedDiscordClaim_NoLongerConfirms_Invalidates`
  — an `OpUpdate` event for an already-tracked Discord claim whose confirm
  fails (panic-on-resolve fake again) results in `InvalidateClaim` being
  called and zero upserts (i.e. not silently re-pointed to a new subject).

All three new/rewritten tests use the panic-wrapper, so the "never calls
`ResolveDiscordSubject` when a row exists" property is proven directly in
each file, not just inferred once.

## Verification

```
go build ./...   # clean
go test ./...    # all packages pass, including the new/rewritten tests
go vet ./...     # clean
```

Ran the new/changed tests individually with `-v` first to confirm they
actually exercise the new code path (not vacuously passing):
`TestDiscover_DiscordExistingClaimNoLongerConfirms_Invalidates`,
`TestRunSweep_DiscordNoLongerConfirms_Invalidates`,
`TestHandleEvent_UpdateToAlreadyTrackedDiscordClaim_NoLongerConfirms_Invalidates`
— all pass, and none panicked (which they would if `ResolveDiscordSubject`
were still being called on the existing-row path).

## Self-review findings

- Checked for any other implementer of `SubjectResolver` /
  `ResolveDiscordSubject` / `ConfirmDiscordSubject` outside the touched
  files and their tests (`grep` across the repo) — the only production
  implementer is `*discord.ClaimResolver`, wired once in `cmd/server/main.go`;
  `internal/api/steam_handlers.go`, `discord_handlers.go`, and `recheck.go`
  only reference the `claims.SubjectResolver` *type*, they don't implement
  it — no mocks needed updating there.
- Confirmed `sweep.go`'s `RunSweep` didn't need its own new interface
  declaration — it takes `resolver SubjectResolver` using the same
  `claims.SubjectResolver` interface already widened in `discover.go` (same
  package).
- `invalidateIfTrackedType` in `handler.go` was reused as-is for the new
  confirm-failure branch (Step 5) — it already does the right thing (only
  invalidates if the currently-tracked row's `RecordURI` matches the event's
  `atURI`), so no changes needed there.
- No other test files or mocks in the repo implement `SubjectResolver`
  outside the three files listed in the task.

## Concerns / discrepancies from the task description

None material. The described snippets matched the actual current file
structure almost verbatim (variable names `c`/`claim` in `sweep.go` were
exactly as described). The only adaptation was renaming
`TestRunSweep_DiscordNoLongerResolvable_Invalidates` to
`TestRunSweep_DiscordNoLongerConfirms_Invalidates` — the old test's setup
(`resolved: map[string]string{}`, i.e. "make `ResolveDiscordSubject` fail")
tested behavior that's no longer reachable once a row exists in the fixed
code, so it had to be rewritten around `ConfirmDiscordSubject` instead. This
is exactly the test case Step 6 asks for in `sweep_test.go`, so no coverage
was lost — it was folded into the existing test's slot rather than added
as a fully separate one, per the "shortest diff" bias.

## Files changed

- `/Users/jp/src/personal/game-status/.claude/worktrees/discord-sync/internal/discord/resolve.go`
- `/Users/jp/src/personal/game-status/.claude/worktrees/discord-sync/internal/claims/discover.go`
- `/Users/jp/src/personal/game-status/.claude/worktrees/discord-sync/internal/claims/sweep.go`
- `/Users/jp/src/personal/game-status/.claude/worktrees/discord-sync/internal/jetstream/handler.go`
- `/Users/jp/src/personal/game-status/.claude/worktrees/discord-sync/internal/claims/discover_test.go`
- `/Users/jp/src/personal/game-status/.claude/worktrees/discord-sync/internal/claims/sweep_test.go`
- `/Users/jp/src/personal/game-status/.claude/worktrees/discord-sync/internal/jetstream/handler_test.go`
