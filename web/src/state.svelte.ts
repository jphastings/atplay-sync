// web/src/state.svelte.ts
import { getMe, recheckClaim, setSteamEnabled, recheckDiscordClaim, setDiscordEnabled, setSourceOrder, type Me } from './api'
import { resolveLiveStatuses, resolveHandle, type LiveStatus } from './atproto'
import { mockMe, mockLiveStatuses } from './devmock'

export const KNOWN_SOURCES = ['steam', 'discord'] as const
export type Source = (typeof KNOWN_SOURCES)[number]

// Named appState, not state: importing a binding literally named `state`
// into a file that also declares its own local `$state(...)` call causes
// Svelte 5's compiler to misparse the bare `$state` token as legacy store
// auto-subscription (`$` + identifier `state`) instead of the rune — a
// warning, not a build error, that silently generates wrong runtime code.
// This bit SourcesList.svelte once already; naming the export something
// that can never collide with the rune keyword removes the hazard for
// every future file that imports it, not just the one that already hit it.
export const appState = $state<{
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
    appState.me = null
    return
  }

  const rechecks = [
    !me.steamSubject && recheckClaim(),
    !me.discordSubject && recheckDiscordClaim(),
  ].filter(Boolean) as Promise<void>[]

  if (rechecks.length) {
    await Promise.all(rechecks).catch(() => { })
    const refreshed = await currentMe()
    appState.me = refreshed ?? me
    return
  }
  appState.me = me
}

export async function loadLiveStatuses(): Promise<void> {
  const me = appState.me
  if (!me) return
  appState.liveStatuses = await currentLiveStatuses(me.did)
}

export async function loadHandle(): Promise<void> {
  const me = appState.me
  if (!me) return
  const handle = await resolveHandle(me.did)
  if (handle) appState.handle = handle // resolution failed — the DID fallback in the template is fine
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
  const me = appState.me
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
// toggleSource, this does NOT re-impose an enabled-first sort: a manual
// drag is fully user-controlled. Fire-and-forget, like toggleSource's own
// persist — note this is weaker than the original vanilla-TS beginRowDrag,
// which awaited the request and re-rendered from a fresh fetch on failure
// so a rejected persist visibly snapped back; here the UI keeps showing
// the locally-dragged order until the next reload if the request is
// rejected.
export function reorderSources(order: Source[]): void {
  const me = appState.me
  if (!me) return
  me.sourceOrder = order
  setSourceOrder(order).catch(() => { })
}
