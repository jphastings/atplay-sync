// Dev-only fixtures for visually QA-ing every state without a real OAuth
// session. Driven by a `?mock=` query param; entirely gated behind
// `import.meta.env.DEV` at the call site, so Vite's production build
// tree-shakes this whole module out.
import type { Me } from './api'
import type { LiveStatus } from './atproto'

const FIXTURES: Record<string, { me: Me | null; live: LiveStatus[] | 'error' }> = {
  'signed-out': { me: null, live: [] },
  'no-claim': {
    me: {
      did: 'did:plc:mockuser', steamEnabled: false, discordEnabled: false, sourceOrder: ['steam', 'discord'],
      discordInviteUrl: 'https://discord.gg/example',
    },
    live: [],
  },
  idle: {
    me: {
      did: 'did:plc:mockuser', steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true,
      discordEnabled: false, sourceOrder: ['steam', 'discord'], discordInviteUrl: 'https://discord.gg/example',
    },
    live: [],
  },
  playing: {
    me: {
      did: 'did:plc:mockuser', steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true,
      discordEnabled: false, sourceOrder: ['steam', 'discord'], discordInviteUrl: 'https://discord.gg/example',
    },
    live: [{
      title: 'Slay the Spire II',
      description: 'The iconic roguelike deckbuilder returns!',
      pageURL: 'https://cartridge.dev/game/slay-the-spire-ii',
      createdAt: new Date(Date.now() - 47 * 60 * 1000).toISOString(),
      staleAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      coverURL: 'https://pds.gamesgamesgamesgames.games/xrpc/com.atproto.sync.getBlob?did=did:web:gamesgamesgamesgames.games&cid=bafkreiafbwc3a3y47qnaguvov6rp4dhispbqeyglfxi5q37nb7lse34h7m',
    }],
  },
  error: {
    me: {
      did: 'did:plc:mockuser', steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true,
      discordEnabled: false, sourceOrder: ['steam', 'discord'], discordInviteUrl: 'https://discord.gg/example',
    },
    live: 'error',
  },
  'both-sources': {
    me: {
      did: 'did:plc:mockuser',
      steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true,
      discordSubject: '690973862245957683', discordDisplayName: 'byjp', discordEnabled: true,
      sourceOrder: ['discord', 'steam'], discordInviteUrl: 'https://discord.gg/example',
    },
    live: [{
      title: 'Slay the Spire II',
      description: 'The iconic roguelike deckbuilder returns!',
      pageURL: 'https://cartridge.dev/game/slay-the-spire-ii',
      createdAt: new Date(Date.now() - 47 * 60 * 1000).toISOString(),
      staleAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      coverURL: 'https://pds.gamesgamesgamesgames.games/xrpc/com.atproto.sync.getBlob?did=did:web:gamesgamesgamesgames.games&cid=bafkreiafbwc3a3y47qnaguvov6rp4dhispbqeyglfxi5q37nb7lse34h7m',
    }],
  },
  'multi-game': {
    me: {
      did: 'did:plc:mockuser',
      steamSubject: '76561197994000231', steamDisplayName: 'JP', steamEnabled: true,
      discordSubject: '690973862245957683', discordDisplayName: 'byjp', discordEnabled: true,
      sourceOrder: ['steam', 'discord'], discordInviteUrl: 'https://discord.gg/example',
    },
    live: [
      {
        title: 'Slay the Spire II',
        description: 'The iconic roguelike deckbuilder returns!',
        pageURL: 'https://cartridge.dev/game/slay-the-spire-ii',
        createdAt: new Date(Date.now() - 47 * 60 * 1000).toISOString(),
        staleAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
        coverURL: 'https://pds.gamesgamesgamesgames.games/xrpc/com.atproto.sync.getBlob?did=did:web:gamesgamesgamesgames.games&cid=bafkreiafbwc3a3y47qnaguvov6rp4dhispbqeyglfxi5q37nb7lse34h7m',
      },
      {
        title: 'Dota 2',
        description: 'A game of unmatched depth and strategic complexity.',
        pageURL: 'https://cartridge.dev/game/dota-2',
        createdAt: new Date(Date.now() - 12 * 60 * 1000).toISOString(),
        staleAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      },
    ],
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

export function mockLiveStatuses(): LiveStatus[] | 'error' | undefined {
  const k = key()
  if (!k) return undefined
  return FIXTURES[k]?.live ?? []
}
