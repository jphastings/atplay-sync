import { getMe, recheckClaim, setSteamEnabled, type Me } from './api'

const app = document.getElementById('app')!

async function render() {
  const me = await getMe()
  if (!me) {
    app.innerHTML = `
      <h1>Game Status Sync</h1>
      <input id="handle" placeholder="your.handle" />
      <button id="signin">Sign in with atproto</button>
    `
    document.getElementById('signin')!.addEventListener('click', () => {
      const handle = (document.getElementById('handle') as HTMLInputElement).value
      window.location.href = `/login?handle=${encodeURIComponent(handle)}`
    })
    return
  }

  // Covers the spec's "discovery once at sign-in": best-effort, silent on
  // failure (the claim may genuinely not exist yet) — the button below is
  // the reliable path.
  if (!me.steamSubject) {
    await recheckClaim().catch(() => {})
    renderSignedIn((await getMe()) ?? me)
    return
  }
  renderSignedIn(me)
}

function renderSignedIn(me: Me) {
  // Three states, not two: sync stays switched on when a claim is revoked
  // (intent and validity are deliberately separate), so "not connected" would
  // be a lie next to a checked box.
  const claimStatus = me.steamSubject
    ? `Verified as ${me.steamDisplayName ?? me.steamSubject}`
    : me.steamEnabled
      ? 'Claim invalid — re-verify at keytrace.dev, then click Recheck claim'
      : 'Not connected — verify at keytrace.dev, then recheck below'
  const toggleDisabled = me.steamSubject ? '' : 'disabled'
  const liveStatus = me.live ? `Currently: ${me.live.game}` : 'Not currently playing anything tracked'

  app.innerHTML = `
    <h1>Game Status Sync</h1>
    <p>Signed in as ${me.did}</p>
    <h2>Steam</h2>
    <p>${claimStatus}</p>
    <button id="recheck">Recheck claim</button>
    <label><input type="checkbox" id="enabled" ${me.steamEnabled ? 'checked' : ''} ${toggleDisabled} /> Sync Steam status</label>
    <p>${liveStatus}</p>
  `
  document.getElementById('recheck')!.addEventListener('click', async () => {
    await recheckClaim()
    await render()
  })
  document.getElementById('enabled')!.addEventListener('change', async (e) => {
    await setSteamEnabled((e.target as HTMLInputElement).checked)
    await render()
  })
}

render()
