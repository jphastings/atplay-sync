export interface Me {
  did: string
  steamSubject?: string
  steamDisplayName?: string
  steamEnabled: boolean
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
