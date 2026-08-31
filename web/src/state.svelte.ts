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
