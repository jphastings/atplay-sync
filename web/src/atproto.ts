// Minimal client-side atproto reads. The PDS is the authoritative source for
// both the signed-in user's live status and any game/platform record it
// references — every public getRecord/getBlob endpoint on the reference PDS
// implementation serves open CORS (`access-control-allow-origin: *`), so
// none of this needs to go through our own backend.

export function parseAtUri(uri: string): { did: string; collection: string; rkey: string } {
  const m = /^at:\/\/([^/]+)\/([^/]+)\/([^/]+)$/.exec(uri)
  if (!m) throw new Error(`not an at-uri: ${uri}`)
  return { did: m[1], collection: m[2], rkey: m[3] }
}

const didDocCache = new Map<string, Promise<string | null>>()

/** Resolves a DID to its PDS's base URL (e.g. "https://eurosky.social"), or null if it can't be resolved. */
export function resolvePDS(did: string): Promise<string | null> {
  let promise = didDocCache.get(did)
  if (!promise) {
    promise = resolvePDSUncached(did)
    didDocCache.set(did, promise)
  }
  return promise
}

async function resolvePDSUncached(did: string): Promise<string | null> {
  try {
    const docURL = did.startsWith('did:web:')
      ? `https://${did.slice('did:web:'.length)}/.well-known/did.json`
      : `https://plc.directory/${did}`
    const res = await fetch(docURL)
    if (!res.ok) return null
    const doc = await res.json()
    const svc = (doc.service ?? []).find((s: { id: string }) => s.id === '#atproto_pds')
    return svc?.serviceEndpoint ?? null
  } catch {
    return null
  }
}

export type RecordResult<T> =
  | { kind: 'found'; value: T }
  | { kind: 'notfound' }
  | { kind: 'error' }

export async function getRecord<T = unknown>(
  did: string,
  collection: string,
  rkey: string,
): Promise<RecordResult<T>> {
  const pds = await resolvePDS(did)
  if (!pds) return { kind: 'error' }
  try {
    const url = `${pds}/xrpc/com.atproto.repo.getRecord?${new URLSearchParams({ repo: did, collection, rkey })}`
    const res = await fetch(url)
    if (res.status === 400) return { kind: 'notfound' } // RecordNotFound
    if (!res.ok) return { kind: 'error' }
    const body = await res.json()
    return { kind: 'found', value: body.value as T }
  } catch {
    return { kind: 'error' }
  }
}

export function blobURL(pds: string, did: string, cid: string): string {
  return `${pds}/xrpc/com.atproto.sync.getBlob?${new URLSearchParams({ did, cid })}`
}

interface StatusRecord {
  game: string
  embed?: { external: { uri: string; title: string; description: string } }
  createdAt: string
  staleAt: string
}

interface GameRecord {
  name: string
  media?: { mediaType: string; blob: { ref: { $link: string } } }[]
}

export interface LiveStatus {
  title: string
  description: string
  pageURL: string
  createdAt: string
  staleAt: string
  coverURL?: string
}

/** Reads the signed-in user's live status directly from their own PDS, resolving cover art from the linked game record. Returns null if they're not currently playing anything, or 'error' if their PDS couldn't be reached. */
export async function resolveLiveStatus(did: string): Promise<LiveStatus | null | 'error'> {
  const status = await getRecord<StatusRecord>(did, 'games.gamesgamesgamesgames.actor.status', 'self')
  if (status.kind === 'notfound') return null
  if (status.kind === 'error') return 'error'

  const { game, embed, createdAt, staleAt } = status.value
  const base: LiveStatus = {
    title: embed?.external.title ?? game,
    description: embed?.external.description ?? '',
    pageURL: embed?.external.uri ?? game,
    createdAt,
    staleAt,
  }

  try {
    const { did: gameDID, rkey } = parseAtUri(game)
    const gameRec = await getRecord<GameRecord>(gameDID, 'games.gamesgamesgamesgames.game', rkey)
    if (gameRec.kind === 'found') {
      const cover = gameRec.value.media?.find((m) => m.mediaType === 'cover')
      const pds = await resolvePDS(gameDID)
      if (cover && pds) base.coverURL = blobURL(pds, gameDID, cover.blob.ref.$link)
    }
  } catch {
    // Cover art is a bonus, not a requirement — the text status stands on its own.
  }

  return base
}
