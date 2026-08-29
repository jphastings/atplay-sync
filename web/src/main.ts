import { getMe, recheckClaim, type Me } from './api'

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
  const claimStatus = me.steamSubject
    ? `Verified as ${me.steamDisplayName ?? me.steamSubject}`
    : 'Not connected — verify at keytrace.dev, then recheck below'

  app.innerHTML = `
    <h1>Game Status Sync</h1>
    <p>Signed in as ${me.did}</p>
    <h2>Steam</h2>
    <p>${claimStatus}</p>
    <button id="recheck">Recheck claim</button>
  `
  document.getElementById('recheck')!.addEventListener('click', async () => {
    await recheckClaim()
    await render()
  })
}

render()
