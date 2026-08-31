# Svelte Frontend Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `web/src`'s vanilla-TS, template-literal-and-`innerHTML` rendering with Svelte 5 (runes) components, backed by a shared reactive state module, with source reordering moved to `svelte-dnd-action`.

**Architecture:** `atproto.ts`/`api.ts`/`jetstream.ts` stay untouched, framework-agnostic. A new `state.svelte.ts` module holds `me`/`liveStatuses`/`handle` as shared `$state`, populated by thin wrapper functions that carry over today's mock-branching and recheck logic. Every screen becomes a small Svelte component reading that shared state directly (no prop-drilling for app-wide data); `main.ts` shrinks to a three-line mount call. `style.css` is untouched and stays global — every component's markup reuses today's exact class names and `data-*` attributes so no selector needs to change.

**Tech Stack:** Svelte 5 (runes) + `@sveltejs/vite-plugin-svelte` on the existing Vite 5 setup (no SvelteKit), `svelte-dnd-action` for drag-and-drop, `svelte-check` for type-checking.

**Spec:** [docs/superpowers/specs/2026-08-31-svelte-frontend-rewrite-design.md](../specs/2026-08-31-svelte-frontend-rewrite-design.md)

## Global Constraints

- No SvelteKit — plain `svelte` + `@sveltejs/vite-plugin-svelte` on the existing Vite config.
- `style.css` stays byte-for-byte unchanged; every component must emit the same class names and `data-*` attributes the current HTML does.
- No new test framework — verification is `svelte-check` (replacing bare `tsc --noEmit`, which cannot type-check `.svelte`/`.svelte.ts` files) plus manual `?mock=` fixture QA, matching this repo's existing frontend convention.
- Package versions (confirmed against the npm registry and each package's own peer-dependency metadata, chosen to avoid an unrelated Vite major-version bump): `svelte@^5.57.0`, `svelte-dnd-action@^0.9.79`, `@sveltejs/vite-plugin-svelte@^4.0.4` (the last major still supporting Vite 5 — 5.x/6.x/7.x of this plugin require Vite 6+), `svelte-check@^4.7.6`. Existing `vite@^5.4.0` and `typescript@^5.6.0` are unchanged.
- `svelte-dnd-action` has no per-item drag-disable (confirmed against its own docs) — connected/draggable sources and not-yet-linked/static sources render as two separate lists rather than one zone with some rows pinned (see Task 4).

---

## Task 1: Svelte build tooling + `state.svelte.ts`

**Files:**
- Modify: `web/package.json`
- Modify: `web/vite.config.ts`
- Create: `web/svelte.config.js`
- Create: `web/src/state.svelte.ts`

**Interfaces:**
- Produces: `state: { me: Me | null | undefined; liveStatuses: LiveStatus[] | 'error' | undefined; handle: string | undefined }`, `KNOWN_SOURCES: readonly ['steam', 'discord']`, `type Source = 'steam' | 'discord'`, `loadMe(): Promise<void>`, `loadLiveStatuses(): Promise<void>`, `loadHandle(): Promise<void>`, `resolveSourceOrder(me: Me): Source[]`, `isConnected(me: Me, source: Source): boolean`, `isEnabled(me: Me, source: Source): boolean`, `toggleSource(source: Source, enabled: boolean): Promise<boolean>`, `reorderSources(order: Source[]): void` — all consumed by Tasks 2-6.
- Consumes: existing `web/src/api.ts` (`getMe`, `recheckClaim`, `setSteamEnabled`, `recheckDiscordClaim`, `setDiscordEnabled`, `setSourceOrder`, `type Me`), `web/src/atproto.ts` (`resolveLiveStatuses`, `resolveHandle`, `type LiveStatus`), `web/src/devmock.ts` (`mockMe`, `mockLiveStatuses`) — none of these three files change in this plan.

- [ ] **Step 1: Add Svelte dependencies to `web/package.json`**

Replace the whole file:

```json
{
  "name": "game-status-web",
  "private": true,
  "type": "module",
  "scripts": { "dev": "vite", "build": "vite build", "check": "svelte-check --tsconfig ./tsconfig.json" },
  "dependencies": { "svelte": "^5.57.0", "svelte-dnd-action": "^0.9.79" },
  "devDependencies": { "@sveltejs/vite-plugin-svelte": "^4.0.4", "svelte-check": "^4.7.6", "typescript": "^5.6.0", "vite": "^5.4.0" }
}
```

- [ ] **Step 2: Install**

Run: `pnpm -C web install`
Expected: installs cleanly, no peer-dependency warnings about `vite`/`svelte` version mismatches (the pinned `@sveltejs/vite-plugin-svelte@4.0.4` targets `vite ^5.0.0`/`svelte ^5.0.0`, matching what's already in the repo and what Step 1 just added).

- [ ] **Step 3: Add the Svelte plugin to `web/vite.config.ts`**

Replace the whole file:

```ts
import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  build: { outDir: '../cmd/server/web/dist' },
  server: { proxy: { '/api': 'http://localhost:8080', '/login': 'http://localhost:8080' } },
})
```

- [ ] **Step 4: Create `web/svelte.config.js`**

```js
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'

export default {
  preprocess: vitePreprocess(),
}
```

- [ ] **Step 5: Create `web/src/state.svelte.ts`**

```ts
// web/src/state.svelte.ts
import { getMe, recheckClaim, setSteamEnabled, recheckDiscordClaim, setDiscordEnabled, setSourceOrder, type Me } from './api'
import { resolveLiveStatuses, resolveHandle, type LiveStatus } from './atproto'
import { mockMe, mockLiveStatuses } from './devmock'

export const KNOWN_SOURCES = ['steam', 'discord'] as const
export type Source = (typeof KNOWN_SOURCES)[number]

export const state = $state<{
  me: Me | null | undefined
  liveStatuses: LiveStatus[] | 'error' | undefined
  handle: string | undefined
}>({
  me: undefined,
  liveStatuses: undefined,
  handle: undefined,
})

// mockMe()/mockLiveStatuses() return `undefined` when `?mock=` isn't set
// (meaning "no override, hit the real API") — distinct from a fixture that
// legitimately resolves to `null`, which `||` would otherwise treat as "no
// override" too and incorrectly fall through.
async function currentMe(): Promise<Me | null> {
  if (import.meta.env.DEV) {
    const mocked = mockMe()
    if (mocked !== undefined) return mocked
  }
  return getMe()
}

async function currentLiveStatuses(did: string): Promise<LiveStatus[] | 'error'> {
  if (import.meta.env.DEV) {
    const mocked = mockLiveStatuses()
    if (mocked !== undefined) return mocked
  }
  return resolveLiveStatuses(did)
}

export async function loadMe(): Promise<void> {
  const me = await currentMe()
  if (!me) {
    state.me = null
    return
  }

  const rechecks = [
    !me.steamSubject && recheckClaim(),
    !me.discordSubject && recheckDiscordClaim(),
  ].filter(Boolean) as Promise<void>[]

  if (rechecks.length) {
    await Promise.all(rechecks).catch(() => { })
    const refreshed = await currentMe()
    state.me = refreshed ?? me
    return
  }
  state.me = me
}

export async function loadLiveStatuses(): Promise<void> {
  const me = state.me
  if (!me) return
  state.liveStatuses = await currentLiveStatuses(me.did)
}

export async function loadHandle(): Promise<void> {
  const me = state.me
  if (!me) return
  const handle = await resolveHandle(me.did)
  if (handle) state.handle = handle // resolution failed — the DID fallback in the template is fine
}

export function resolveSourceOrder(me: Me): Source[] {
  const order = (me.sourceOrder ?? []) as Source[]
  return [...order, ...KNOWN_SOURCES.filter((s) => !order.includes(s))]
}

export function isConnected(me: Me, source: Source): boolean {
  return !!(source === 'steam' ? me.steamSubject : me.discordSubject)
}

export function isEnabled(me: Me, source: Source): boolean {
  return source === 'steam' ? me.steamEnabled : me.discordEnabled
}

// Persists exactly the resorted order, same as today's sinkDisabledRows:
// toggling always re-imposes an enabled-first sort on top of whatever
// order is currently on screen.
export async function toggleSource(source: Source, enabled: boolean): Promise<boolean> {
  const me = state.me
  if (!me) return false
  try {
    await (source === 'steam' ? setSteamEnabled : setDiscordEnabled)(enabled)
  } catch {
    return false
  }
  if (source === 'steam') me.steamEnabled = enabled
  else me.discordEnabled = enabled

  const resorted = resolveSourceOrder(me).sort((a, b) => Number(isEnabled(me, b)) - Number(isEnabled(me, a)))
  me.sourceOrder = resorted
  setSourceOrder(resorted).catch(() => {
    // fire-and-forget, like a manual drag reorder — a lost persist just
    // means the next real reload reflects the server's last-known order
  })
  return true
}

// Persists exactly the order given (a manual drag result) — unlike
// toggleSource, this does NOT re-impose an enabled-first sort, matching
// today's beginRowDrag: a manual drag is fully user-controlled.
export function reorderSources(order: Source[]): void {
  const me = state.me
  if (!me) return
  me.sourceOrder = order
  setSourceOrder(order).catch(() => { })
}
```

- [ ] **Step 6: Verify**

Run: `pnpm -C web run check`
Expected: `svelte-check` reports 0 errors (there are no `.svelte` files yet, but it type-checks `state.svelte.ts`'s runes correctly — this is the proof the toolchain is wired up; if it instead reports it can't find `svelte-check` or errors on `$state`, stop and fix the tooling before continuing, don't work around it in the state file).

- [ ] **Step 7: Commit**

```bash
git add web/package.json web/pnpm-lock.yaml web/vite.config.ts web/svelte.config.js web/src/state.svelte.ts
git commit -m "feat: add Svelte tooling and a runes-based state module"
```

---

## Task 2: `SignIn.svelte`

**Files:**
- Create: `web/src/lib/SignIn.svelte`

**Interfaces:**
- Produces: `SignIn.svelte` (no props), consumed by Task 6's `App.svelte`.
- Consumes: nothing new.

- [ ] **Step 1: Create the component**

```svelte
<!-- web/src/lib/SignIn.svelte -->
<script lang="ts">
  function handleSubmit(e: SubmitEvent) {
    e.preventDefault()
    const form = e.currentTarget as HTMLFormElement
    const handle = (new FormData(form).get('handle') as string).trim()
    if (!handle) return
    window.location.href = `/login?handle=${encodeURIComponent(handle)}`
  }
</script>

<div class="screen">
  <div class="marquee">
    <h1 class="marquee-title">AT PLAY<br>SYNC</h1>
    <form class="signin-form" onsubmit={handleSubmit}>
      <label class="field-label" for="handle">Your Atmosphere handle</label>
      <input class="text-input" id="handle" name="handle" placeholder="your.handle" autocomplete="username" required />
      <button class="btn btn-primary" type="submit">Press Start</button>
    </form>
  </div>
</div>
```

- [ ] **Step 2: Verify**

Run: `pnpm -C web run check`
Expected: 0 errors. This component isn't mounted anywhere yet — Task 6 wires it into `App.svelte`.

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/SignIn.svelte
git commit -m "feat: add SignIn Svelte component"
```

---

## Task 3: `HeroCard.svelte` + `HeroList.svelte`

**Files:**
- Create: `web/src/lib/HeroCard.svelte`
- Create: `web/src/lib/HeroList.svelte`

**Interfaces:**
- Produces: `HeroCard.svelte` (`{ status: LiveStatus }` prop), `HeroList.svelte` (no props, reads `state.liveStatuses`) — consumed by Task 5's `SignedIn.svelte`.
- Consumes: `state` from `../state.svelte` (Task 1), `type LiveStatus` from `../atproto`.

- [ ] **Step 1: Create `HeroCard.svelte`**

```svelte
<!-- web/src/lib/HeroCard.svelte -->
<script lang="ts">
  import type { LiveStatus } from '../atproto'

  let { status }: { status: LiveStatus } = $props()

  function timeAgo(iso: string): string {
    const seconds = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)
    const units: [Intl.RelativeTimeFormatUnit, number][] = [
      ['day', 86400], ['hour', 3600], ['minute', 60],
    ]
    for (const [unit, secs] of units) {
      const value = Math.floor(seconds / secs)
      if (value >= 1) return new Intl.RelativeTimeFormat('en', { style: 'long' }).format(-value, unit)
    }
    return 'just now'
  }
</script>

<section class="hero" aria-live="polite">
  {#if status.coverURL}
    <img
      class="hero-cover"
      src={status.coverURL}
      alt={`${status.title} cover art`}
      loading="lazy"
      onerror={(e) => (e.currentTarget as HTMLImageElement).remove()}
    />
  {/if}
  <div class="hero-body">
    <p class="hero-eyebrow"><span class="live-dot"></span> Live</p>
    <h2 class="hero-title">{status.title}</h2>
    <p class="hero-meta">since {timeAgo(status.createdAt)}</p>
  </div>
</section>
```

Svelte auto-escapes `{expression}` text interpolation, so this needs no equivalent of `main.ts`'s manual `escapeHTML` helper.

- [ ] **Step 2: Create `HeroList.svelte`**

```svelte
<!-- web/src/lib/HeroList.svelte -->
<script lang="ts">
  import { state } from '../state.svelte'
  import HeroCard from './HeroCard.svelte'
</script>

<div class="hero-list" id="hero-list">
  {#if state.liveStatuses === undefined}
    <section class="hero hero--loading" aria-live="polite">
      <div class="hero-cover hero-cover--loading"></div>
      <div class="hero-body">
        <p class="hero-eyebrow"><span class="live-dot"></span> Checking your PDS…</p>
      </div>
    </section>
  {:else if state.liveStatuses === 'error'}
    <section class="hero hero--error" aria-live="polite">Couldn't reach your PDS to check status.</section>
  {:else if state.liveStatuses.length === 0}
    <section class="hero hero--empty" aria-live="polite">
      <div class="hero-body">
        <p class="hero-eyebrow"><span class="live-dot"></span> Idle</p>
        <p class="hero-meta">Not currently playing a game.</p>
      </div>
    </section>
  {:else}
    {#each state.liveStatuses as status (status.pageURL)}
      <HeroCard {status} />
    {/each}
  {/if}
</div>
```

`state.liveStatuses === undefined` (not yet loaded) is what today's hardcoded "Checking your PDS…" markup in `renderSignedIn` covers as a one-off initial state — here it's just one more branch of the same component, so there's a single source of truth for every hero-list state instead of two.

- [ ] **Step 3: Verify**

Run: `pnpm -C web run check`
Expected: 0 errors. Neither component is mounted yet — Task 5 wires `HeroList` into `SignedIn.svelte`.

- [ ] **Step 4: Commit**

```bash
git add web/src/lib/HeroCard.svelte web/src/lib/HeroList.svelte
git commit -m "feat: add HeroCard and HeroList Svelte components"
```

---

## Task 4: `SourceRow.svelte` + `SourcesList.svelte`

**Files:**
- Create: `web/src/lib/SourceRow.svelte`
- Create: `web/src/lib/SourcesList.svelte`

**Interfaces:**
- Produces: `SourceRow.svelte` (`{ source: Source; me: Me; onToggle: (source: Source, enabled: boolean) => Promise<boolean> }` props), `SourcesList.svelte` (no props, reads `state.me`) — consumed by Task 5's `SignedIn.svelte`.
- Consumes: `state`, `KNOWN_SOURCES`, `type Source`, `resolveSourceOrder`, `isConnected`, `isEnabled`, `toggleSource`, `reorderSources` from `../state.svelte` (Task 1).

- [ ] **Step 1: Create `SourceRow.svelte`**

```svelte
<!-- web/src/lib/SourceRow.svelte -->
<script lang="ts">
  import type { Me } from '../api'
  import { isConnected as isConnectedFn, isEnabled as isEnabledFn, type Source } from '../state.svelte'

  // Streamline "Simple Icons" Steam mark (https://streamlinehq.com), inlined
  // and recolored to currentColor. Decorative — the adjacent "Steam" text
  // already labels it, so it's hidden from assistive tech.
  const STEAM_ICON_PATH = "M11.979 0C5.678 0 0.511 4.86 0.022 11.037l6.432 2.658c0.545 -0.371 1.203 -0.59 1.912 -0.59 0.063 0 0.125 0.004 0.188 0.006l2.861 -4.142V8.91c0 -2.495 2.028 -4.524 4.524 -4.524 2.494 0 4.524 2.031 4.524 4.527s-2.03 4.525 -4.524 4.525h-0.105l-4.076 2.911c0 0.052 0.004 0.105 0.004 0.159 0 1.875 -1.515 3.396 -3.39 3.396 -1.635 0 -3.016 -1.173 -3.331 -2.727L0.436 15.27C1.862 20.307 6.486 24 11.979 24c6.627 0 11.999 -5.373 11.999 -12S18.605 0 11.979 0zM7.54 18.21l-1.473 -0.61c0.262 0.543 0.714 0.999 1.314 1.25 1.297 0.539 2.793 -0.076 3.332 -1.375 0.263 -0.63 0.264 -1.319 0.005 -1.949s-0.75 -1.121 -1.377 -1.383c-0.624 -0.26 -1.29 -0.249 -1.878 -0.03l1.523 0.63c0.956 0.4 1.409 1.5 1.009 2.455 -0.397 0.957 -1.497 1.41 -2.454 1.012H7.54zm11.415 -9.303c0 -1.662 -1.353 -3.015 -3.015 -3.015 -1.665 0 -3.015 1.353 -3.015 3.015 0 1.665 1.35 3.015 3.015 3.015 1.663 0 3.015 -1.35 3.015 -3.015zm-5.273 -0.005c0 -1.252 1.013 -2.266 2.265 -2.266 1.249 0 2.266 1.014 2.266 2.266 0 1.251 -1.017 2.265 -2.266 2.265 -1.253 0 -2.265 -1.014 -2.265 -2.265z"

  // Streamline "Simple Icons" Discord mark (https://streamlinehq.com), inlined
  // and recolored to currentColor. Decorative — the adjacent "Discord" text
  // already labels it, so it's hidden from assistive tech.
  const DISCORD_ICON_PATH = "M20.317 4.37a19.791 19.791 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 0 0-5.487 0 12.64 12.64 0 0 0-.617-1.25.077.077 0 0 0-.079-.037A19.736 19.736 0 0 0 3.677 4.37a.07.07 0 0 0-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 0 0 .031.057 19.9 19.9 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028 14.09 14.09 0 0 0 1.226-1.994.076.076 0 0 0-.041-.106 13.107 13.107 0 0 1-1.872-.892.077.077 0 0 1-.008-.128 10.2 10.2 0 0 0 .372-.292.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 0 1 .078.01c.12.098.246.198.373.292a.077.077 0 0 1-.006.127 12.299 12.299 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.839 19.839 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 0 0-.031-.03zM8.02 15.33c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.955 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.946 2.418-2.157 2.418Z"

  let { source, me, onToggle }: {
    source: Source
    me: Me
    onToggle: (source: Source, enabled: boolean) => Promise<boolean>
  } = $props()

  let connected = $derived(isConnectedFn(me, source))
  let label = $derived(source === 'steam' ? 'Steam' : 'Discord')
  let enabled = $state(isEnabledFn(me, source))

  // Re-sync if `me` changes out from under us (e.g. a fresh fetch after a
  // reorder round-trips) without clobbering an in-flight optimistic toggle.
  $effect(() => {
    enabled = isEnabledFn(me, source)
  })

  async function handleChange(e: Event) {
    const next = (e.target as HTMLInputElement).checked
    enabled = next // optimistic
    const ok = await onToggle(source, next)
    if (!ok) enabled = !next // revert on failure — don't leave the UI claiming a state that didn't take
  }
</script>

<label class="toggle-row" data-connected={connected} data-source={source}>
  <span class="toggle-label">
    <span class="toggle-label-title">
      {#if source === 'steam'}
        <svg class="steam-icon" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d={STEAM_ICON_PATH} fill="currentColor"></path></svg>
      {:else}
        <svg class="discord-icon" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d={DISCORD_ICON_PATH} fill="currentColor"></path></svg>
      {/if}
      {label}
    </span>
    <span class="toggle-label-sub">
      {#if connected}
        {#if source === 'steam'}
          Sync data from <a href={`https://steamcommunity.com/profiles/${encodeURIComponent(me.steamSubject!)}`} target="_blank" rel="noopener noreferrer">{me.steamDisplayName ?? me.steamSubject}</a> on Steam
        {:else}
          Sync data from <a href={`https://discord.com/users/${encodeURIComponent(me.discordSubject!)}`} target="_blank" rel="noopener noreferrer">{me.discordDisplayName ?? me.discordSubject}</a> on Discord
        {/if}
      {:else if source === 'discord'}
        You must <a href="https://keytrace.dev/add/discord" target="_blank" rel="noopener noreferrer">link your Discord account</a> and <a href={me.discordInviteUrl} target="_blank" rel="noopener noreferrer">join our tracking server</a> before you can sync it
      {:else}
        You must <a href={`https://keytrace.dev/add/${source}`} target="_blank" rel="noopener noreferrer">link your {label} account</a> before you can sync it
      {/if}
    </span>
  </span>
  <span class="toggle">
    <input type="checkbox" checked={enabled} disabled={!connected} onchange={handleChange} />
    <span class="toggle-track"></span>
    <span class="toggle-thumb"></span>
  </span>
</label>
```

`data-connected={connected}` stringifies the boolean to `"true"`/`"false"`, matching today's template literal output exactly — `style.css`'s `.toggle-row[data-connected="true"]` selectors (drag-affordance cursor, hover background) apply unchanged, with no separate "draggable" flag needed on this component.

- [ ] **Step 2: Create `SourcesList.svelte`**

```svelte
<!-- web/src/lib/SourcesList.svelte -->
<script lang="ts">
  import { dndzone, type DndEvent } from 'svelte-dnd-action'
  import { flip } from 'svelte/animate'
  import { state, resolveSourceOrder, isConnected, isEnabled, toggleSource, reorderSources, type Source } from '../state.svelte'
  import SourceRow from './SourceRow.svelte'

  interface Item { id: Source }

  let connectedItems = $state<Item[]>([])
  let disconnectedSources = $derived(
    state.me ? resolveSourceOrder(state.me).filter((s) => !isConnected(state.me!, s)) : [],
  )

  // Re-derives the draggable list from server-confirmed state whenever `me`
  // changes (initial load, a toggle, a reorder round-trip) — svelte-dnd-action
  // needs a locally-reassignable array for live drag feedback, so this can't
  // just be a plain $derived.
  $effect(() => {
    const me = state.me
    if (!me) {
      connectedItems = []
      return
    }
    connectedItems = resolveSourceOrder(me)
      .filter((s) => isConnected(me, s))
      .sort((a, b) => Number(isEnabled(me, b)) - Number(isEnabled(me, a)))
      .map((id) => ({ id }))
  })

  function handleDndConsider(e: CustomEvent<DndEvent<Item>>) {
    connectedItems = e.detail.items
  }

  function handleDndFinalize(e: CustomEvent<DndEvent<Item>>) {
    connectedItems = e.detail.items
    reorderSources([...connectedItems.map((i) => i.id), ...disconnectedSources])
  }
</script>

<section class="consent-zone">
  <div id="sources" use:dndzone={{ items: connectedItems, flipDurationMs: 200 }} onconsider={handleDndConsider} onfinalize={handleDndFinalize}>
    {#each connectedItems as item (item.id)}
      <div animate:flip={{ duration: 200 }}>
        {#if state.me}
          <SourceRow source={item.id} me={state.me} onToggle={toggleSource} />
        {/if}
      </div>
    {/each}
  </div>
  {#each disconnectedSources as source (source)}
    {#if state.me}
      <SourceRow {source} me={state.me} onToggle={toggleSource} />
    {/if}
  {/each}
</section>
```

`svelte-dnd-action` has no per-item drag-disable (confirmed against its own docs — only a whole-zone `dragDisabled` option), so connected (draggable) and not-yet-linked (static — nothing meaningful to reorder, the checkbox is always disabled) sources render as two lists rather than one zone with some rows pinned.
`svelte/animate`'s `flip` gives the "row slides to its new slot" animation today's `sinkDisabledRows` hand-builds with manual `getBoundingClientRect`/`transform` math — for free, since `flip` is exactly that primitive.
ponytail: two-list split instead of a per-item drag lock; revisit if a third source ever makes the split feel arbitrary (today there are only two: `steam`, `discord`).

- [ ] **Step 3: Verify**

Run: `pnpm -C web run check`
Expected: 0 errors. Neither component is mounted yet — Task 5 wires `SourcesList` into `SignedIn.svelte`.

- [ ] **Step 4: Commit**

```bash
git add web/src/lib/SourceRow.svelte web/src/lib/SourcesList.svelte
git commit -m "feat: add SourceRow and SourcesList Svelte components with drag-and-drop"
```

---

## Task 5: `SignedIn.svelte`

**Files:**
- Create: `web/src/lib/SignedIn.svelte`

**Interfaces:**
- Produces: `SignedIn.svelte` (no props, reads `state.me`/`state.handle`), consumed by Task 6's `App.svelte`.
- Consumes: `state`, `loadHandle` from `../state.svelte` (Task 1), `HeroList` (Task 3), `SourcesList` (Task 4).

- [ ] **Step 1: Create the component**

```svelte
<!-- web/src/lib/SignedIn.svelte -->
<script lang="ts">
  import { state, loadHandle } from '../state.svelte'
  import HeroList from './HeroList.svelte'
  import SourcesList from './SourcesList.svelte'

  $effect(() => {
    if (state.me) loadHandle()
  })

  function handleSignOut() {
    window.location.href = '/logout'
  }
</script>

<div class="panel-screen"><div class="panel">
  <HeroList />
  <SourcesList />
  <footer class="utility-row">
    <span class="did-tag">{state.handle ? `@${state.handle}` : state.me?.did}</span>
    <button class="btn btn-ghost" type="button" onclick={handleSignOut}>Sign out</button>
  </footer>
</div></div>
```

`SourcesList.svelte` (Task 4) already renders its own `<section class="consent-zone">` wrapper internally, so `SourcesList` is rendered directly here as a sibling of `HeroList` — not re-wrapped in a second `consent-zone` section.

- [ ] **Step 2: Verify**

Run: `pnpm -C web run check`
Expected: 0 errors. Not mounted yet — Task 6 wires this into `App.svelte`.

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/SignedIn.svelte
git commit -m "feat: add SignedIn Svelte component"
```

---

## Task 6: `App.svelte` + cutover

The integration task: assembles every component built in Tasks 2-5 into the real app, replaces `main.ts`'s entire vanilla implementation with a three-line mount call, and is the first point at which the rewritten app can actually be run and visually verified.

**Files:**
- Create: `web/src/App.svelte`
- Modify: `web/src/main.ts` (full replacement)

**Interfaces:**
- Consumes: `state`, `loadMe`, `loadLiveStatuses` (Task 1), `watchOwnStatus` from `../jetstream` (untouched), `SignIn` (Task 2), `SignedIn` (Task 5).

- [ ] **Step 1: Create `web/src/App.svelte`**

```svelte
<!-- web/src/App.svelte -->
<script lang="ts">
  import { state, loadMe, loadLiveStatuses } from './state.svelte'
  import { watchOwnStatus } from './jetstream'
  import SignIn from './lib/SignIn.svelte'
  import SignedIn from './lib/SignedIn.svelte'

  $effect(() => {
    loadMe()
  })

  $effect(() => {
    if (!state.me) return
    loadLiveStatuses()
    return watchOwnStatus(state.me.did, () => loadLiveStatuses())
  })
</script>

{#if state.me === null}
  <SignIn />
{:else if state.me}
  <SignedIn />
{/if}
```

`state.me === undefined` (not yet resolved) renders nothing, matching today's behavior of not calling either `renderSignIn`/`renderSignedIn` until `currentMe()` resolves.

- [ ] **Step 2: Replace `web/src/main.ts`**

Replace the whole file (all of today's rendering logic moves into the Task 1-5 components and is deleted here, not duplicated):

```ts
// web/src/main.ts
import { mount } from 'svelte'
import App from './App.svelte'

mount(App, { target: document.getElementById('app')! })
```

- [ ] **Step 3: Verify types**

Run: `pnpm -C web run check`
Expected: 0 errors across the whole `web/src` tree.

- [ ] **Step 4: Verify the build**

Run: `pnpm -C web run build`
Expected: builds cleanly into `cmd/server/web/dist` (same output path as before — no `Makefile`/`main.go` change needed).

- [ ] **Step 5: Manual QA against the dev server**

Run: `pnpm -C web run dev`, then check each of these `?mock=` fixtures in a browser (e.g. `http://localhost:5173/?mock=multi-game`) renders identically to before the rewrite:

- `signed-out` — sign-in marquee/form; submitting navigates to `/login?handle=...`
- `no-claim` — signed in, idle hero, both toggle rows showing "must link" prompts
- `idle` — signed in, idle hero, Steam row verified+toggleable
- `playing` — one hero card with cover art
- `error` — hero shows the "couldn't reach your PDS" message
- `both-sources` — two connected/toggleable rows (Discord above Steam, per that fixture's `sourceOrder`), one hero card
- `multi-game` — two stacked hero cards (Slay the Spire II with cover art, Dota 2 without), no particular order requirement beyond both being present

Then, interactively (on `?mock=both-sources` or `?mock=multi-game`, both of which have two connected sources): toggle a source off and on — confirm the row optimistically updates and re-sorts enabled-first; drag-reorder the two rows — confirm they animate to the new position and the drop commits (network calls to `/api/sync/order` will 404 against the Vite dev server with no backend running, which is expected and matches today's behavior in dev-mock mode — the row order should still update locally either way, matching `reorderSources`'s fire-and-forget persistence).

- [ ] **Step 6: Commit**

```bash
git add web/src/App.svelte web/src/main.ts
git commit -m "feat: assemble Svelte components into App and cut main.ts over to it"
```

---

## Self-Review Notes

- **Spec coverage:** tooling (Task 1), `state.svelte.ts` (Task 1), every component named in the spec's breakdown (Tasks 2-6), `svelte-dnd-action` reorder + `flip` animation (Task 4), dev-mock moved into the state module (Task 1), unchanged `style.css`/`atproto.ts`/`api.ts`/`jetstream.ts` (never touched by any task) — every spec section has a task.
- **Placeholder scan:** none — every step is complete, real code. Early tasks' components aren't mounted into a running app yet, but each is a complete, real implementation of its piece, verified by `svelte-check`; this mirrors how `main.ts` itself was always one file with no smaller runnable unit before this rewrite existed.
- **Type consistency:** `Source`/`KNOWN_SOURCES`/`state`/`resolveSourceOrder`/`isConnected`/`isEnabled`/`toggleSource`/`reorderSources` (Task 1) are used with identical names and signatures in Tasks 4 and 5; `LiveStatus` (from unchanged `atproto.ts`) flows into `HeroCard`/`HeroList` (Task 3) unchanged; `Me` (from unchanged `api.ts`) flows into `SourceRow` (Task 4) unchanged.
