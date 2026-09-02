// web/src/synclive.ts
//
// Live-update channel for the settings page's per-source sync-state
// indicator — pushed by our own backend (internal/livestate.Hub) the
// instant a Steam tick or Discord presence event changes what a source is
// reporting, no polling. Unlike jetstream.ts's connection to Bluesky's
// public firehose, this is same-origin and cookie-authenticated, so
// there's no did/wantedDids to pass — the server already knows who's
// asking.

export interface SourceOutcome {
  source: string
  status: 'offline' | 'idle' | 'unknown' | 'matched' | 'synced'
  gameName?: string
}

const RECONNECT_DELAY_MS = 3000

// The server re-sends current state every 30s (livestate.HeartbeatInterval),
// so a longer silence than this means the socket is dead in a way the
// browser hasn't noticed — a half-open connection, or a server-side close
// that never made it back through a proxy. Waiting for a `close` event that
// is never coming would leave the page showing stale state indefinitely, so
// treat silence itself as the signal and reconnect.
const SILENCE_LIMIT_MS = 75_000

export function watchSyncLive(onUpdate: (outcomes: SourceOutcome[]) => void): () => void {
  let socket: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let silenceTimer: ReturnType<typeof setTimeout> | null = null
  let stopped = false

  function connect() {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    // Held in a local as well as the outer binding: a late event from a
    // socket we've already replaced must never act on its successor.
    const ws = new WebSocket(`${protocol}//${location.host}/api/sync/live`)
    socket = ws

    ws.addEventListener('message', (e) => {
      armSilenceTimer(ws)
      const outcomes = JSON.parse(e.data)
      if (Array.isArray(outcomes)) onUpdate(outcomes)
    })
    ws.addEventListener('close', () => {
      clearSilenceTimer()
      scheduleReconnect()
    })
    ws.addEventListener('error', () => ws.close())

    armSilenceTimer(ws)
  }

  function armSilenceTimer(ws: WebSocket) {
    clearSilenceTimer()
    // Closing fires the close handler, which schedules the reconnect.
    silenceTimer = setTimeout(() => ws.close(), SILENCE_LIMIT_MS)
  }

  function clearSilenceTimer() {
    if (silenceTimer) clearTimeout(silenceTimer)
    silenceTimer = null
  }

  function scheduleReconnect() {
    if (stopped) return
    reconnectTimer = setTimeout(connect, RECONNECT_DELAY_MS)
  }

  connect()

  return () => {
    stopped = true
    if (reconnectTimer) clearTimeout(reconnectTimer)
    clearSilenceTimer()
    socket?.close()
  }
}
