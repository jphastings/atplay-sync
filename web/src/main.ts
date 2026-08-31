import { getMe, recheckClaim, setSteamEnabled, recheckDiscordClaim, setDiscordEnabled, setSourceOrder, type Me } from './api'
import { resolveLiveStatus, resolveHandle, type LiveStatus } from './atproto'
import { watchOwnStatus } from './jetstream'
import { mockMe, mockLiveStatus } from './devmock'

const app = document.getElementById('app')!

// Streamline "Simple Icons" Steam mark (https://streamlinehq.com), inlined
// and recolored to currentColor. Decorative — the adjacent "Steam" text
// already labels it, so it's hidden from assistive tech.
const STEAM_ICON = `<svg class="steam-icon" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d="M11.979 0C5.678 0 0.511 4.86 0.022 11.037l6.432 2.658c0.545 -0.371 1.203 -0.59 1.912 -0.59 0.063 0 0.125 0.004 0.188 0.006l2.861 -4.142V8.91c0 -2.495 2.028 -4.524 4.524 -4.524 2.494 0 4.524 2.031 4.524 4.527s-2.03 4.525 -4.524 4.525h-0.105l-4.076 2.911c0 0.052 0.004 0.105 0.004 0.159 0 1.875 -1.515 3.396 -3.39 3.396 -1.635 0 -3.016 -1.173 -3.331 -2.727L0.436 15.27C1.862 20.307 6.486 24 11.979 24c6.627 0 11.999 -5.373 11.999 -12S18.605 0 11.979 0zM7.54 18.21l-1.473 -0.61c0.262 0.543 0.714 0.999 1.314 1.25 1.297 0.539 2.793 -0.076 3.332 -1.375 0.263 -0.63 0.264 -1.319 0.005 -1.949s-0.75 -1.121 -1.377 -1.383c-0.624 -0.26 -1.29 -0.249 -1.878 -0.03l1.523 0.63c0.956 0.4 1.409 1.5 1.009 2.455 -0.397 0.957 -1.497 1.41 -2.454 1.012H7.54zm11.415 -9.303c0 -1.662 -1.353 -3.015 -3.015 -3.015 -1.665 0 -3.015 1.353 -3.015 3.015 0 1.665 1.35 3.015 3.015 3.015 1.663 0 3.015 -1.35 3.015 -3.015zm-5.273 -0.005c0 -1.252 1.013 -2.266 2.265 -2.266 1.249 0 2.266 1.014 2.266 2.266 0 1.251 -1.017 2.265 -2.266 2.265 -1.253 0 -2.265 -1.014 -2.265 -2.265z" fill="currentColor"></path></svg>`

// Streamline "Simple Icons" Discord mark (https://streamlinehq.com), inlined
// and recolored to currentColor. Decorative — the adjacent "Discord" text
// already labels it, so it's hidden from assistive tech.
const DISCORD_ICON = `<svg class="discord-icon" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d="M20.317 4.37a19.791 19.791 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 0 0-5.487 0 12.64 12.64 0 0 0-.617-1.25.077.077 0 0 0-.079-.037A19.736 19.736 0 0 0 3.677 4.37a.07.07 0 0 0-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 0 0 .031.057 19.9 19.9 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028 14.09 14.09 0 0 0 1.226-1.994.076.076 0 0 0-.041-.106 13.107 13.107 0 0 1-1.872-.892.077.077 0 0 1-.008-.128 10.2 10.2 0 0 0 .372-.292.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 0 1 .078.01c.12.098.246.198.373.292a.077.077 0 0 1-.006.127 12.299 12.299 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.839 19.839 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 0 0-.031-.03zM8.02 15.33c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.955 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.946 2.418-2.157 2.418Z" fill="currentColor"></path></svg>`

const KNOWN_SOURCES = ['steam', 'discord'] as const
type Source = (typeof KNOWN_SOURCES)[number]

// Backend can omit rows for sources the user has never touched, and
// serializes as JSON `null` (not `[]`) when the user has zero sync_prefs
// rows at all — fall back to declaration order, appending any known source
// missing from what the server sent so every toggle row still renders.
function resolveSourceOrder(me: Me): Source[] {
  const order = me.sourceOrder ?? []
  return [...order, ...KNOWN_SOURCES.filter((s) => !order.includes(s))] as Source[]
}

// `mockMe()`/`mockLiveStatus()` return `undefined` when `?mock=` isn't set
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

async function currentLiveStatus(did: string): Promise<LiveStatus | null | 'error'> {
  if (import.meta.env.DEV) {
    const mocked = mockLiveStatus()
    if (mocked !== undefined) return mocked
  }
  return resolveLiveStatus(did)
}

async function render() {
  const me = await currentMe()
  if (!me) {
    renderSignIn()
    return
  }

  const rechecks = [
    !me.steamSubject && recheckClaim(),
    !me.discordSubject && recheckDiscordClaim(),
  ].filter(Boolean) as Promise<void>[]

  if (rechecks.length) {
    await Promise.all(rechecks).catch(() => { })
    const refreshed = await currentMe()
    renderSignedIn(refreshed ?? me)
    return
  }
  renderSignedIn(me)
}

function renderSignIn() {
  app.innerHTML = `
    <div class="screen">
      <div class="marquee">
        <h1 class="marquee-title">AT PLAY<br>SYNC</h1>
        <form class="signin-form" id="signin-form">
          <label class="field-label" for="handle">Your Atmosphere handle</label>
          <input class="text-input" id="handle" name="handle" placeholder="your.handle" autocomplete="username" required />
          <button class="btn btn-primary" type="submit">Press Start</button>
        </form>
      </div>
    </div>
  `
  document.getElementById('signin-form')!.addEventListener('submit', (e) => {
    e.preventDefault()
    const handle = (document.getElementById('handle') as HTMLInputElement).value.trim()
    if (!handle) return
    window.location.href = `/login?handle=${encodeURIComponent(handle)}`
  })
}

function renderSignedIn(me: Me) {
  app.innerHTML = `
    <div class="panel-screen"><div class="panel">
      <section class="hero hero--loading" id="hero" aria-live="polite">
        <div class="hero-cover hero-cover--loading"></div>
        <div class="hero-body">
          <p class="hero-eyebrow"><span class="live-dot"></span> Checking your PDS…</p>
        </div>
      </section>

      <section class="consent-zone"><div id="sources">${sourcesHTML(me)}</div></section>

      <footer class="utility-row">
        <span class="did-tag" id="identity-tag">${me.did}</span>
        <button class="btn btn-ghost" id="signout" type="button">Sign out</button>
      </footer>
    </div></div>
  `

  attachSourcesListeners()
  document.getElementById('signout')!.addEventListener('click', () => {
    window.location.href = '/logout'
  })

  loadLiveStatus(me.did)
  watchOwnStatus(me.did, () => loadLiveStatus(me.did))
  loadHandle(me.did)
}

function sourcesHTML(me: Me): string {
  return resolveSourceOrder(me).map((source) => sourceRowHTML(source, me)).join('')
}

function sourceRowHTML(source: Source, me: Me): string {
  const connected = !!(source === 'steam' ? me.steamSubject : me.discordSubject)
  const enabled = source === 'steam' ? me.steamEnabled : me.discordEnabled
  const icon = source === 'steam' ? STEAM_ICON : DISCORD_ICON
  const label = source === 'steam' ? 'Steam' : 'Discord'
  const subtitle = connected
    ? (source === 'steam' ? verifiedSyncHTML(me) : verifiedDiscordSyncHTML(me))
    : source === 'discord'
      ? `You must <a href="https://keytrace.dev/add/discord" target="_blank" rel="noopener noreferrer">link your Discord account</a> and <a href="${me.discordInviteUrl}" target="_blank" rel="noopener noreferrer">join our tracking server</a> before you can sync it`
      : `You must <a href="https://keytrace.dev/add/${source}" target="_blank" rel="noopener noreferrer">link your ${label} account</a> before you can sync it`

  return `
    <label class="toggle-row" data-connected="${connected}" data-source="${source}">
      <span class="toggle-label">
        <span class="toggle-label-title">${icon} ${label}</span>
        <span class="toggle-label-sub">${subtitle}</span>
      </span>
      <span class="toggle">
        <input type="checkbox" data-source-enabled="${source}" ${enabled ? 'checked' : ''} ${connected ? '' : 'disabled'} />
        <span class="toggle-track"></span>
        <span class="toggle-thumb"></span>
      </span>
    </label>
  `
}

// Re-attaches toggle + drag-reorder handlers to whatever's currently in
// #sources — called after both the initial render and every re-render that
// follows a reorder, since innerHTML replacement drops all listeners.
function attachSourcesListeners() {
  const sources = document.getElementById('sources')
  if (!sources) return

  sources.querySelectorAll<HTMLInputElement>('input[data-source-enabled]').forEach((input) => {
    input.addEventListener('change', async (e) => {
      const target = e.target as HTMLInputElement
      const next = target.checked
      const setEnabled = target.dataset.sourceEnabled === 'steam' ? setSteamEnabled : setDiscordEnabled
      try {
        await setEnabled(next)
      } catch {
        target.checked = !next // revert on failure — don't leave the UI claiming a state that didn't take
        return
      }
      sinkDisabledRows(sources)
    })
  })

  sources.querySelectorAll<HTMLElement>('.toggle-row[data-connected="true"]').forEach((row) => {
    row.addEventListener('pointerdown', (e) => {
      if ((e.target as HTMLElement).closest('a, .toggle')) return // let links and the switch itself handle their own click
      beginRowDrag(sources, row, e)
    })
  })
}

// Keeps enabled rows above disabled ones — toggling a row off sinks it
// below any still-active row (and vice versa), animated the same way a
// manual drag settles.
function sinkDisabledRows(sources: HTMLElement) {
  const rows = Array.from(sources.querySelectorAll<HTMLElement>('.toggle-row'))
  const firstTops = new Map(rows.map((r) => [r, r.getBoundingClientRect().top]))

  const ordered = [
    ...rows.filter((r) => r.querySelector('input')!.checked),
    ...rows.filter((r) => !r.querySelector('input')!.checked),
  ]
  ordered.forEach((r) => sources.appendChild(r))

  for (const r of rows) {
    const delta = firstTops.get(r)! - r.getBoundingClientRect().top
    r.style.transition = 'none'
    r.style.transform = delta ? `translateY(${delta}px)` : ''
    requestAnimationFrame(() => {
      r.style.transition = ''
      r.style.transform = ''
    })
  }

  setSourceOrder(ordered.map((r) => r.dataset.source!)).catch(() => {
    // fire-and-forget, like the drag reorder — a lost persist just means the
    // next real reload reflects the server's last-known order
  })
}

// Drags `row` by tracking the pointer directly (native HTML5 drag-and-drop
// can't be confined to a container — its ghost image floats free over the
// whole page — so reordering is hand-rolled with Pointer Events instead).
// Siblings are shifted with a CSS transform sized to the dragged row's own
// footprint, standard "make room" reorder feedback; the real DOM order is
// only touched once, on release.
function beginRowDrag(sources: HTMLElement, row: HTMLElement, downEvent: PointerEvent) {
  const containerRect = sources.getBoundingClientRect()
  const rows = Array.from(sources.querySelectorAll<HTMLElement>('.toggle-row'))
  const slots = rows.map((r) => {
    const rect = r.getBoundingClientRect()
    return { row: r, top: rect.top - containerRect.top, height: rect.height }
  })
  const dragged = slots.find((s) => s.row === row)!
  const others = slots.filter((s) => s.row !== row)
  const gap = parseFloat(getComputedStyle(sources).rowGap || '0')
  const step = dragged.height + gap
  const maxTop = containerRect.height - dragged.height
  const startY = downEvent.clientY
  let targetIndex = others.findIndex((o) => o.top >= dragged.top)
  if (targetIndex === -1) targetIndex = others.length

  row.setPointerCapture(downEvent.pointerId)
  let dragging = false

  function onMove(e: PointerEvent) {
    if (!dragging) {
      if (Math.abs(e.clientY - startY) < 4) return // small-movement clicks still reach the checkbox untouched
      dragging = true
      row.classList.add('dragging')
    }

    const clampedTop = Math.min(Math.max(dragged.top + (e.clientY - startY), 0), maxTop)
    row.style.transform = `translateY(${clampedTop - dragged.top}px)`

    // Target slot follows the pointer itself, not the dragged row's own
    // (clamped) box — comparing box-to-box centers means the last slot's
    // trigger point coincides exactly with the clamp boundary and can never
    // fire. The pointer has no such ceiling, so it always resolves cleanly.
    const pointerY = Math.min(Math.max(e.clientY - containerRect.top, 0), containerRect.height)
    targetIndex = others.filter((o) => o.top + o.height / 2 < pointerY).length
    others.forEach((o, j) => {
      const wasAfter = o.top > dragged.top
      const shift = (j >= targetIndex ? step : 0) - (wasAfter ? step : 0)
      o.row.style.transform = shift ? `translateY(${shift}px)` : ''
    })
  }

  async function onUp() {
    row.removeEventListener('pointermove', onMove)
    row.removeEventListener('pointerup', onUp)
    row.removeEventListener('pointercancel', onUp)
    row.classList.remove('dragging')
    if (!dragging) return

    sources.insertBefore(row, others[targetIndex]?.row ?? null)
    row.style.transform = ''
    others.forEach((o) => { o.row.style.transform = '' })

    const newOrder = Array.from(sources.querySelectorAll<HTMLElement>('.toggle-row')).map((r) => r.dataset.source!)
    try {
      await setSourceOrder(newOrder)
    } catch {
      // fall through — the re-render below still reflects the server's actual (unchanged) order
    }
    const refreshed = await currentMe()
    if (refreshed) {
      sources.innerHTML = sourcesHTML(refreshed)
      attachSourcesListeners()
    }
  }

  row.addEventListener('pointermove', onMove)
  row.addEventListener('pointerup', onUp)
  row.addEventListener('pointercancel', onUp)
}

async function loadLiveStatus(did: string) {
  const hero = document.getElementById('hero')
  if (!hero) return // user navigated away from the signed-in screen since this was kicked off
  const status = await currentLiveStatus(did)
  hero.outerHTML = renderHero(status)
}

async function loadHandle(did: string) {
  const handle = await resolveHandle(did)
  const tag = document.getElementById('identity-tag')
  if (!handle || !tag) return // resolution failed, or the panel's moved on — the DID is a fine fallback
  tag.textContent = `@${handle}`
}

function renderHero(status: LiveStatus | null | 'error'): string {
  if (status === 'error') {
    return `<section class="hero hero--error" id="hero" aria-live="polite">Couldn't reach your PDS to check status.</section>`
  }
  if (status === null) {
    return `
      <section class="hero hero--empty" id="hero" aria-live="polite">
        <div class="hero-body">
          <p class="hero-eyebrow"><span class="live-dot"></span> Idle</p>
          <p class="hero-meta">Not currently playing a game.</p>
        </div>
      </section>
    `
  }
  const cover = status.coverURL
    ? `<img class="hero-cover" src="${status.coverURL}" alt="${escapeHTML(status.title)} cover art" loading="lazy" onerror="this.remove()" />`
    : ''
  return `
    <section class="hero" id="hero" aria-live="polite">
      ${cover}
      <div class="hero-body">
        <p class="hero-eyebrow"><span class="live-dot"></span> Live</p>
        <h2 class="hero-title">${escapeHTML(status.title)}</h2>
        <p class="hero-meta">since ${timeAgo(status.createdAt)}</p>
      </div>
    </section>
  `
}

// verifiedSyncHTML is only called once me.steamSubject is confirmed present.
function verifiedSyncHTML(me: Me): string {
  const name = escapeHTML(me.steamDisplayName ?? me.steamSubject!)
  const profileURL = `https://steamcommunity.com/profiles/${encodeURIComponent(me.steamSubject!)}`
  return `Sync data from <a href="${profileURL}" target="_blank" rel="noopener noreferrer">${name}</a> on Steam`
}

// verifiedDiscordSyncHTML is only called once me.discordSubject is confirmed present.
function verifiedDiscordSyncHTML(me: Me): string {
  const name = escapeHTML(me.discordDisplayName ?? me.discordSubject!)
  const profileURL = `https://discord.com/users/${encodeURIComponent(me.discordSubject!)}`
  return `Sync data from <a href="${profileURL}" target="_blank" rel="noopener noreferrer">${name}</a> on Discord`
}

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

function escapeHTML(s: string): string {
  const div = document.createElement('div')
  div.textContent = s
  return div.innerHTML
}

render()
