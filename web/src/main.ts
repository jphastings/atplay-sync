import { getMe, recheckClaim, setSteamEnabled, type Me } from './api'
import { resolveLiveStatus, resolveHandle, type LiveStatus } from './atproto'
import { watchOwnStatus } from './jetstream'
import { mockMe, mockLiveStatus } from './devmock'

const app = document.getElementById('app')!

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
    await recheckClaim().catch(() => {})
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
  const claim = claimStatus(me)

  app.innerHTML = `
    <div class="panel-screen"><div class="panel">
      <section class="hero hero--loading" id="hero" aria-live="polite">
        <div class="hero-cover hero-cover--loading"></div>
        <div class="hero-body">
          <p class="hero-eyebrow"><span class="live-dot"></span> Checking your PDS…</p>
        </div>
      </section>

      <section class="consent-zone">
        <div class="claim-row">
          <span class="claim-status ${claim.attention ? 'claim-status--attention' : ''}">${claim.html}</span>
          <button class="btn btn-ghost" id="recheck" type="button">Recheck claim</button>
        </div>
        <label class="toggle-row">
          <span class="toggle-label">
            <span class="toggle-label-title">Sync Steam status</span>
            <span class="toggle-label-sub">Broadcasts what you're playing to your PDS while enabled</span>
          </span>
          <span class="toggle">
            <input type="checkbox" id="enabled" ${me.steamEnabled ? 'checked' : ''} ${me.steamSubject ? '' : 'disabled'} />
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

  document.getElementById('recheck')!.addEventListener('click', async () => {
    await recheckClaim()
    await render()
  })
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
          <p class="hero-meta">Not currently playing anything tracked.</p>
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

function claimStatus(me: Me): { html: string; attention: boolean } {
  if (me.steamSubject) {
    const name = escapeHTML(me.steamDisplayName ?? me.steamSubject)
    const profileURL = `https://steamcommunity.com/profiles/${encodeURIComponent(me.steamSubject)}`
    return {
      html: `Verified as <a href="${profileURL}" target="_blank" rel="noopener noreferrer">${name}</a> on Steam`,
      attention: false,
    }
  }
  if (me.steamEnabled) {
    return { html: 'Claim needs re-verifying — verify at keytrace.dev, then Recheck', attention: true }
  }
  return {
    html: 'Unlinked Steam account. Please visit <a href="https://keytrace.dev/add/steam" target="_blank" rel="noopener noreferrer">Keytrace</a> and link your Atmosphere and Steam accounts.',
    attention: false,
  }
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
