import type { MeetingSegment } from './meetings-store'

export function normalizeUtterance(text: string): string {
  return text.replace(/\s+/g, ' ').trim()
}

export function buildUtteranceSegment(input: {
  meetingId: string
  text: string
  startMs: number
  speakerLabel?: string
}): Omit<MeetingSegment, 'id'> | null {
  const text = normalizeUtterance(input.text)
  if (!text || !input.meetingId) return null
  const startMs = Number.isFinite(input.startMs) && input.startMs >= 0 ? input.startMs : 0
  return {
    meetingId: input.meetingId,
    speakerLabel: input.speakerLabel?.trim() || '说话人',
    lang: 'zh',
    confidence: 0.7,
    startMs,
    endMs: startMs + Math.max(800, text.length * 80),
    text,
  }
}
