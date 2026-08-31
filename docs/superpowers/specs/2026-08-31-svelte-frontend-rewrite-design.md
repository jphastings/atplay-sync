# Svelte frontend rewrite — design

## Purpose

`web/src` today is a hand-rolled vanilla-TS SPA: `main.ts` builds every
screen as template-literal HTML strings, swaps them in via `innerHTML`, and
manually re-attaches event listeners after each swap (`attachSourcesListeners`
called after both the initial render and every reorder). Drag-to-reorder is
hand-rolled Pointer Events tracking (`beginRowDrag`) because native HTML5
drag-and-drop can't be confined to a container. Rewrite this as a Svelte 5
(runes) + Vite app: components replace string-building, reactive state
replaces manual DOM diffing, and a drag-and-drop library replaces the
hand-rolled dragging.

**Not in scope:** SvelteKit. Investigated and rejected — this is a
single-screen dashboard (sign in → manage sync sources → see your own live
status) with no routing need anywhere in `PRODUCT.md`/`DESIGN.md`, and its
SSR doesn't apply here: every real value on the page depends on a
client-side cookie check (`/api/me`) and client-side atproto reads
(deliberately — see `CLAUDE.md`'s "PDS is authoritative" principle), neither
of which SvelteKit can resolve server-side, and `adapter-static` (the only
adapter that fits "one static `dist/` folder embedded in a Go binary,"
this repo's actual deployment shape) disables per-request SSR regardless.
Plain `svelte` + `@sveltejs/vite-plugin-svelte` on the existing Vite setup
is the whole addition. Revisit if a public per-DID/handle page is ever
added — that's the one thing that would earn file-based routing.

**Also not in scope:** any change to `atproto.ts`, `api.ts`, `jetstream.ts`,
or `style.css`'s logic/content. These stay framework-agnostic — Svelte
replaces the view and state layers, not the data-fetching or styling.

## Component breakdown

Every component's job maps to a function or template block that exists in
today's `main.ts`:

- **`App.svelte`** — root. Owns the top-level branch between sign-in and
  signed-in (today's `render()`), and kicks off `state.svelte.ts`'s initial
  load.
- **`lib/SignIn.svelte`** — the marquee/handle form (today's
  `renderSignIn` + its submit listener).
- **`lib/SignedIn.svelte`** — the panel shell: renders `HeroList`,
  `SourcesList`, and the footer (did-tag + sign-out button). Today's
  `renderSignedIn` plus the `loadHandle`/sign-out listener wiring.
- **`lib/HeroList.svelte`** — one `{#each}` over `state.liveStatuses`,
  handling the loading/error/empty/populated branches (today's
  `renderHeroList`). Loading state is the initial value before the first
  fetch resolves, not a separate render path.
- **`lib/HeroCard.svelte`** — one status card (today's `renderHero`),
  taking a single `LiveStatus` as a prop.
- **`lib/SourcesList.svelte`** — the ordered toggle-row list. Owns
  `svelte-dnd-action`'s `dndzone` and calls `setSourceOrder` on
  reorder/toggle, same as today's `sinkDisabledRows`/`beginRowDrag`, but
  see **Drag-and-drop** below for why this gets meaningfully smaller.
- **`lib/SourceRow.svelte`** — one toggle row (today's `sourceRowHTML`):
  icon, label, connect-or-sync subtitle, the checkbox.

Every component is presentational and reads/writes `state.svelte.ts` —
none holds fetch logic itself, matching "each unit does one thing."

## State: `state.svelte.ts`

A single module using Svelte 5's runes-outside-components pattern (a
`.svelte.ts` file can declare `$state`/`$derived` at module scope and
export it, which any component can import and react to):

```ts
// web/src/state.svelte.ts
export const state = $state({
  me: undefined as Me | null | undefined,   // undefined = not yet loaded
  liveStatuses: undefined as LiveStatus[] | 'error' | undefined,
  handle: undefined as string | undefined,
})

export async function loadMe() { /* mock branch or api.getMe(), assigns state.me */ }
export async function loadLiveStatuses() { /* mock branch or atproto.resolveLiveStatuses(state.me.did) */ }
export async function loadHandle() { /* atproto.resolveHandle(state.me.did) */ }
export async function toggleSource(source: Source, enabled: boolean) { /* api call, then patches state.me locally */ }
export async function reorderSources(order: Source[]) { /* api call; state.me.sourceOrder = order */ }
```

This replaces the current `innerHTML` swap + listener re-attach cycle
entirely: components read `state.me`/`state.liveStatuses` directly and
Svelte's reactivity re-renders just the parts that changed. `jetstream.ts`'s
`watchOwnStatus` callback becomes `() => loadLiveStatuses()`, wired in
`App.svelte`'s `$effect` (mirroring today's `watchOwnStatus(me.did, () =>
loadLiveStatus(me.did))` call in `renderSignedIn`).

The `rechecks` dance in today's `render()` (call `recheckClaim`/
`recheckDiscordClaim` for any unlinked source, then refetch `me`) becomes
`loadMe`'s own logic, run once at startup — same behavior, one place.

## Dev-mock

`devmock.ts`'s `FIXTURES` table and `key()` helper are unchanged. What
changes is the call site: `loadMe`/`loadLiveStatuses` check `?mock=` once
(via the existing `mockMe()`/`mockLiveStatuses()` functions) before falling
through to the real API/atproto calls — the same branch that exists today
in `currentMe()`/`currentLiveStatuses()`, just living inside `state.svelte.ts`
instead of being called from every render path separately.

## Drag-and-drop: `svelte-dnd-action`

Replaces `beginRowDrag`'s hand-rolled pointer tracking and
`sinkDisabledRows`'s manual transform-based animation. `SourcesList.svelte`
keeps an `items` array (the current source order) with `use:dndzone`;
`on:consider` updates local order live during a drag, `on:finalize` calls
`reorderSources`. Toggling a row's enabled state re-sorts `items`
(enabled-first, same rule as today), and wrapping the list in Svelte's
built-in `animate:flip` (from `svelte/animate`) gives the "row slides to its
new slot" animation today's `sinkDisabledRows` hand-builds — for free,
since `flip` is exactly "animate an element from its last measured position
to its new one," the same primitive `sinkDisabledRows` reimplements by hand
today.

## Build tooling

New files:
- `web/svelte.config.js` — `export default { preprocess: vitePreprocess() }`
- `web/src/App.svelte` and the `web/src/lib/*.svelte` components above
- `web/src/state.svelte.ts`

Changed files:
- `web/package.json` — add `svelte`, `@sveltejs/vite-plugin-svelte`,
  `svelte-dnd-action` (dependencies), `svelte-check` (devDependency); the
  `svelte-check` binary becomes the type-check step in place of a bare
  `tsc --noEmit` invocation (it understands `.svelte` files; `tsc` alone
  doesn't).
- `web/vite.config.ts` — add the `svelte()` plugin.
- `web/index.html` — script tag points at `main.ts`'s new bootstrap
  (`import App from './App.svelte'; mount(App, { target: document.getElementById('app')! })`)
  instead of the current `render()` call.
- `web/tsconfig.json` — no change expected (`moduleResolution: "Bundler"` and
  `strict` already suit Svelte 5 + TS); confirm during implementation.

Deleted: nothing removed at the repo level — `main.ts` shrinks to the
bootstrap above (its render/DOM-manipulation code moves into the
components and is deleted, not kept alongside).

## Testing / verification

No new test framework — matches this repo's existing frontend convention
(no unit tests for `web/src` today, verified via type-checking plus manual
`?mock=` fixture QA). `svelte-check` replaces `tsc --noEmit` as the
type-check step; manual verification stays the same `?mock=` fixtures
(`idle`, `playing`, `error`, `both-sources`, `multi-game`, etc.), spot-checked
in a real browser after the rewrite.

## Out of scope

- SvelteKit (see Purpose).
- Any visual/CSS change — `style.css` stays global, unscoped, byte-for-byte
  the same.
- Any product/behavior change beyond the drag-and-drop and state-management
  modernization explicitly scoped above.
- A public per-DID/handle page — flagged above as the one thing that would
  change the SvelteKit decision; not requested now.
