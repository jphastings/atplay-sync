// Live-update trigger for the signed-in user's status records. This is
// purely a "something changed, go refetch" signal — the record content
// always comes from a real listRecords call (see atproto.ts), so a missed
// or out-of-order event here can never show stale data, only a slightly
// delayed refresh.

const JETSTREAM_HOST = 'jetstream2.us-east.bsky.network'
const RECONNECT_DELAY_MS = 3000

export function watchOwnStatus(did: string, onChange: () => void): () => void {
  let socket: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let stopped = false

  function connect() {
    const params = new URLSearchParams({
      wantedCollections: 'games.atmosphere.status',
      wantedDids: did,
    })
    socket = new WebSocket(`wss://${JETSTREAM_HOST}/subscribe?${params}`)
    socket.addEventListener('message', () => onChange())
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
