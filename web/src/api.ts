export interface Me {
  did: string
  steamSubject?: string
  steamDisplayName?: string
  steamEnabled: boolean
  discordSubject?: string
  discordDisplayName?: string
  discordEnabled: boolean
  sourceOrder: string[] | null
  discordInviteUrl: string
}

export async function getMe(): Promise<Me | null> {
  const res = await fetch('/api/me')
  if (res.status === 401) return null
  if (!res.ok) throw new Error(`GET /api/me: ${res.status}`)
  return res.json()
}

export async function recheckClaim(): Promise<void> {
  const res = await fetch('/api/steam/recheck', { method: 'POST' })
  if (!res.ok) throw new Error(`POST /api/steam/recheck: ${res.status}`)
}

export async function setSteamEnabled(enabled: boolean): Promise<void> {
  const res = await fetch('/api/steam/enabled', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled }),
  })
  if (!res.ok) throw new Error(`POST /api/steam/enabled: ${res.status}`)
}

export async function recheckDiscordClaim(): Promise<void> {
  const res = await fetch('/api/discord/recheck', { method: 'POST' })
  if (!res.ok) throw new Error(`POST /api/discord/recheck: ${res.status}`)
}

export async function setDiscordEnabled(enabled: boolean): Promise<void> {
  const res = await fetch('/api/discord/enabled', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ enabled }),
  })
  if (!res.ok) throw new Error(`POST /api/discord/enabled: ${res.status}`)
}

export async function setSourceOrder(order: string[]): Promise<void> {
  const res = await fetch('/api/sync/order', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ order }),
  })
  if (!res.ok) throw new Error(`POST /api/sync/order: ${res.status}`)
}
