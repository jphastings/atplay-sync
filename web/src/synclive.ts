// web/src/synclive.ts
//
// Live-update channel for the settings page's per-source sync-state
// indicator (Synced/Duplicate/Unknown) — pushed by our own backend
// (internal/livestate.Hub) the instant a Steam tick or Discord presence
// event changes what a source is reporting, no polling. Unlike
// jetstream.ts's connection to Bluesky's public firehose, this is
// same-origin and cookie-authenticated, so there's no did/wantedDids to
// pass — the server already knows who's asking.

export interface SourceOutcome {
  source: string
  status: 'synced' | 'duplicate' | 'unknown'
  gameName?: string
}

const RECONNECT_DELAY_MS = 3000

export function watchSyncLive(onUpdate: (outcomes: SourceOutcome[]) => void): () => void {
  let socket: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let stopped = false

  function connect() {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    socket = new WebSocket(`${protocol}//${location.host}/api/sync/live`)
    socket.addEventListener('message', (e) => onUpdate(JSON.parse(e.data)))
    socket.addEventListener('close', scheduleReconnect)
    socket.addEventListener('error', () => socket?.close())
  }

  function scheduleReconnect() {
    if (stopped) return
    reconnectTimer = setTimeout(connect, RECONNECT_DELAY_MS)
  }

  connect()

  return () => {
    stopped = true
    if (reconnectTimer) clearTimeout(reconnectTimer)
    socket?.close()
  }
}
