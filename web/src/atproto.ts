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

interface DIDDoc {
  service?: { id: string; serviceEndpoint: string }[]
  alsoKnownAs?: string[]
}

const didDocCache = new Map<string, Promise<DIDDoc | null>>()

function resolveDIDDoc(did: string): Promise<DIDDoc | null> {
  let promise = didDocCache.get(did)
  if (!promise) {
    promise = resolveDIDDocUncached(did)
    didDocCache.set(did, promise)
  }
  return promise
}

async function resolveDIDDocUncached(did: string): Promise<DIDDoc | null> {
  try {
    const docURL = did.startsWith('did:web:')
      ? `https://${did.slice('did:web:'.length)}/.well-known/did.json`
      : `https://plc.directory/${did}`
    const res = await fetch(docURL)
    if (!res.ok) return null
    return await res.json()
  } catch {
    return null
  }
}

/** Resolves a DID to its PDS's base URL (e.g. "https://eurosky.social"), or null if it can't be resolved. */
export async function resolvePDS(did: string): Promise<string | null> {
  const doc = await resolveDIDDoc(did)
  const svc = doc?.service?.find((s) => s.id === '#atproto_pds')
  return svc?.serviceEndpoint ?? null
}

/** Resolves a DID to its handle (e.g. "byjp.me"), or null if it can't be resolved. */
export async function resolveHandle(did: string): Promise<string | null> {
  const doc = await resolveDIDDoc(did)
  const handleURI = doc?.alsoKnownAs?.find((a) => a.startsWith('at://'))
  return handleURI ? handleURI.slice('at://'.length) : null
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

/** Reads every live (non-stale) status the signed-in user has published across all their sources, resolving cover art from each linked game record. Returns 'error' if their PDS couldn't be reached. */
export async function resolveLiveStatuses(did: string): Promise<LiveStatus[] | 'error'> {
  const pds = await resolvePDS(did)
  if (!pds) return 'error'

  let records: { uri: string; cid: string; value: StatusRecord }[]
  try {
    const url = `${pds}/xrpc/com.atproto.repo.listRecords?${new URLSearchParams({ repo: did, collection: 'games.atmosphere.status' })}`
    const res = await fetch(url)
    if (!res.ok) return 'error'
    const body = await res.json()
    records = body.records ?? []
  } catch {
    return 'error'
  }

  const now = Date.now()
  const live = records.filter((r) => new Date(r.value.staleAt).getTime() > now)
  return Promise.all(live.map((r) => toLiveStatus(r.value)))
}

async function toLiveStatus(value: StatusRecord): Promise<LiveStatus> {
  const { game, embed, createdAt, staleAt } = value
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
