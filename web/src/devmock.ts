// Dev-only fixtures for visually QA-ing every state without a real OAuth
// session. Driven by a `?mock=` query param; entirely gated behind
// `import.meta.env.DEV` at the call site, so Vite's production build
// tree-shakes this whole module out.
import type { Me } from './api'
import type { LiveStatus } from './atproto'

const FIXTURES: Record<string, { me: Me | null; live: LiveStatus | null | 'error' }> = {
  'signed-out': { me: null, live: null },
  'no-claim': { me: { did: 'did:plc:mockuser', steamEnabled: false }, live: null },
  idle: {
    me: { did: 'did:plc:mockuser', steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true },
    live: null,
  },
  playing: {
    me: { did: 'did:plc:mockuser', steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true },
    live: {
      title: 'Slay the Spire II',
      description: 'The iconic roguelike deckbuilder returns!',
      pageURL: 'https://cartridge.dev/game/slay-the-spire-ii',
      createdAt: new Date(Date.now() - 47 * 60 * 1000).toISOString(),
      staleAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      coverURL: 'https://pds.gamesgamesgamesgames.games/xrpc/com.atproto.sync.getBlob?did=did:web:gamesgamesgamesgames.games&cid=bafkreiafbwc3a3y47qnaguvov6rp4dhispbqeyglfxi5q37nb7lse34h7m',
    },
  },
  error: {
    me: { did: 'did:plc:mockuser', steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true },
    live: 'error',
  },
}

function key(): string | null {
  return new URLSearchParams(window.location.search).get('mock')
}

export function mockMe(): Me | null | undefined {
  const k = key()
  if (!k) return undefined
  return FIXTURES[k]?.me ?? null
}

export function mockLiveStatus(): LiveStatus | null | 'error' | undefined {
  const k = key()
  if (!k) return undefined
  return FIXTURES[k]?.live ?? null
}
