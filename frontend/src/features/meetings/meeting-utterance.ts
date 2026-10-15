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
    // ⚠⚠ §372：这个 0.7 **不是置信度**，是占位常量。
    //
    // 本函数造的是**用户手工敲进去**的句子（下面 `origin: 'manual'`），
    // 它压根没有经过 ASR，因此「识别置信度」在这里没有定义。
    //
    // 与另两个同类占位常量并列（见 §372.3）：
    //   `backend/internal/stt/transcribe.go` 云端路径恒 0.95（上游不返回置信度）
    //   本文件手工段恒 0.7
    //   而 `native/schema.ts` 的 `local_meeting_segments.confidence` 默认 **1.0**
    //   —— 三处都不是测量值，而存储层默认**声称满信**。
    //
    // ⇒ 与其删字段（属契约决定），先钉死一条：
    //   **任何「按 confidence 过滤」的逻辑都不得消费这三个来源的取值**，
    //   否则闸门会恒定放行且不报错。
    confidence: 0.7,
    startMs,
    endMs: startMs + Math.max(800, text.length * 80),
    text,
    // §132：这一行是**人手工敲的**，不是机器转写出来的。
    // 不标的话 `dedupeSegments` 会把用户用来改正的相邻句子当成
    // 「同音频的重转写」裁掉尾巴（实测 `张伟下周三交对比表` → `伟`）。
    origin: 'manual',
  }
}
