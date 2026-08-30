import { getMe, recheckClaim, setSteamEnabled, type Me } from './api'
import { resolveLiveStatus, resolveHandle, type LiveStatus } from './atproto'
import { watchOwnStatus } from './jetstream'
import { mockMe, mockLiveStatus } from './devmock'

const app = document.getElementById('app')!

// Streamline "Simple Icons" Steam mark (https://streamlinehq.com), inlined
// and recolored to currentColor. Decorative — the adjacent "Steam" text
// already labels it, so it's hidden from assistive tech.
const STEAM_ICON = `<svg class="steam-icon" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d="M11.979 0C5.678 0 0.511 4.86 0.022 11.037l6.432 2.658c0.545 -0.371 1.203 -0.59 1.912 -0.59 0.063 0 0.125 0.004 0.188 0.006l2.861 -4.142V8.91c0 -2.495 2.028 -4.524 4.524 -4.524 2.494 0 4.524 2.031 4.524 4.527s-2.03 4.525 -4.524 4.525h-0.105l-4.076 2.911c0 0.052 0.004 0.105 0.004 0.159 0 1.875 -1.515 3.396 -3.39 3.396 -1.635 0 -3.016 -1.173 -3.331 -2.727L0.436 15.27C1.862 20.307 6.486 24 11.979 24c6.627 0 11.999 -5.373 11.999 -12S18.605 0 11.979 0zM7.54 18.21l-1.473 -0.61c0.262 0.543 0.714 0.999 1.314 1.25 1.297 0.539 2.793 -0.076 3.332 -1.375 0.263 -0.63 0.264 -1.319 0.005 -1.949s-0.75 -1.121 -1.377 -1.383c-0.624 -0.26 -1.29 -0.249 -1.878 -0.03l1.523 0.63c0.956 0.4 1.409 1.5 1.009 2.455 -0.397 0.957 -1.497 1.41 -2.454 1.012H7.54zm11.415 -9.303c0 -1.662 -1.353 -3.015 -3.015 -3.015 -1.665 0 -3.015 1.353 -3.015 3.015 0 1.665 1.35 3.015 3.015 3.015 1.663 0 3.015 -1.35 3.015 -3.015zm-5.273 -0.005c0 -1.252 1.013 -2.266 2.265 -2.266 1.249 0 2.266 1.014 2.266 2.266 0 1.251 -1.017 2.265 -2.266 2.265 -1.253 0 -2.265 -1.014 -2.265 -2.265z" fill="currentColor"></path></svg>`

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

  if (!me.steamSubject) {
    await recheckClaim().catch(() => { })
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
        <h1 class="marquee-title">GAME<br>STATUS<br>SYNC</h1>
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
  const connected = !!me.steamSubject
  const toggleSubtitle = connected
    ? verifiedSyncHTML(me)
    : 'You must <a href="https://keytrace.dev/add/steam" target="_blank" rel="noopener noreferrer">link your Steam account</a> before you can sync it'

  app.innerHTML = `
    <div class="panel-screen"><div class="panel">
      <section class="hero hero--loading" id="hero" aria-live="polite">
        <div class="hero-cover hero-cover--loading"></div>
        <div class="hero-body">
          <p class="hero-eyebrow"><span class="live-dot"></span> Checking your PDS…</p>
        </div>
      </section>

      <section class="consent-zone">
        <label class="toggle-row">
          <span class="toggle-label">
            <span class="toggle-label-title">${STEAM_ICON} Steam</span>
            <span class="toggle-label-sub">${toggleSubtitle}</span>
          </span>
          <span class="toggle">
            <input type="checkbox" id="enabled" ${me.steamEnabled ? 'checked' : ''} ${connected ? '' : 'disabled'} />
            <span class="toggle-track"></span>
            <span class="toggle-thumb"></span>
          </span>
        </label>
      </section>

      <footer class="utility-row">
        <span class="did-tag" id="identity-tag">${me.did}</span>
        <button class="btn btn-ghost" id="signout" type="button">Sign out</button>
      </footer>
    </div></div>
  `

  document.getElementById('enabled')!.addEventListener('change', async (e) => {
    const input = e.target as HTMLInputElement
    const next = input.checked
    try {
      await setSteamEnabled(next)
    } catch {
      input.checked = !next // revert on failure — don't leave the UI claiming a state that didn't take
    }
  })
  document.getElementById('signout')!.addEventListener('click', () => {
    window.location.href = '/logout'
  })

  loadLiveStatus(me.did)
  watchOwnStatus(me.did, () => loadLiveStatus(me.did))
  loadHandle(me.did)
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
