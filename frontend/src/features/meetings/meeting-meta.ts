export interface MeetingTitleInput {
  startedAt: number
  location?: string | null
  topic?: string | null
  firstUtterance?: string | null
}

export function formatCapturedTitle(input: MeetingTitleInput): string {
  const topic = input.topic?.trim()
  if (topic) return topic
  const spoken = input.firstUtterance?.trim()
  if (spoken) {
    const cut = spoken.replace(/\s+/g, ' ').slice(0, 24)
    return cut.length < spoken.length ? `${cut}…` : cut
  }
  const when = new Date(input.startedAt).toLocaleString('zh-CN', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
  const loc = input.location?.trim()
  return loc ? `${when} · ${loc}` : `${when} 会议`
}

export function parseTagInput(raw: string): string[] {
  const seen = new Set<string>()
  const tags: string[] = []
  for (const part of raw.split(/[,，#]/)) {
    const tag = part.trim()
    if (!tag || seen.has(tag)) continue
    seen.add(tag)
    tags.push(tag)
  }
  return tags
}

export function suggestMeetingTags(text: string): string[] {
  const lower = text.toLowerCase()
  if (!lower.trim()) return []
  const rules: Array<[string, string[]]> = [
    ['周会', ['周会']], ['standup', ['站会']], ['评审', ['评审']],
    ['预算', ['预算']], ['招聘', ['招聘']], ['客户', ['客户']],
    ['sprint', ['Sprint']], ['okr', ['OKR']],
  ]
  const tags: string[] = []
  for (const [key, add] of rules) {
    if (lower.includes(key)) tags.push(...add)
  }
  return parseTagInput(tags.join(','))
}

export function formatCoords(lat: number, lng: number): string {
  if (!Number.isFinite(lat) || !Number.isFinite(lng)) return ''
  return `${lat.toFixed(4)}, ${lng.toFixed(4)}`
}

export type GeoLike = {
  getCurrentPosition: (
    ok: (pos: { coords: { latitude: number; longitude: number } }) => void,
    err?: (e: unknown) => void,
    opts?: { timeout?: number; maximumAge?: number },
  ) => void
}

export async function captureDeviceLocation(geo?: GeoLike | null): Promise<string | null> {
  const api = geo ?? (typeof navigator !== 'undefined' ? navigator.geolocation : null)
  if (!api?.getCurrentPosition) return null
  return new Promise((resolve) => {
    api.getCurrentPosition(
      (pos) => resolve(formatCoords(pos.coords.latitude, pos.coords.longitude) || null),
      () => resolve(null),
      { timeout: 8000, maximumAge: 300_000 },
    )
  })
}
