import { unref, type MaybeRef } from 'vue'
import { sttApi } from '../../api/stt'
import { extractEmbedding } from '../../native/speaker-embedding'
import { detectLang } from '../../native/detect-lang'
import { saveAudioPart } from './audio-parts'
import { liveTranslate } from './live-translate'
import { updateSegmentTranslation } from './meetings-live'
import {
  saveSegment, updateTranscript,
  type MeetingSegment,
} from './meetings-store'
import type { SpeakerDiarizer } from '../../native/speaker-diarization'

export async function ingestSpeechBlob(opts: {
  meetingId: MaybeRef<string>
  blob: Blob
  startMs: number
  endMs: number
  seq: number
  diarizer: SpeakerDiarizer
  segments: MeetingSegment[]
  segmentProfiles: Map<string, string>
  signal?: AbortSignal
}): Promise<MeetingSegment | null> {
  const meetingId = unref(opts.meetingId)
  if (!meetingId) return null
  // signal 一路传到 sttApi.transcribe：会议录音时每个语音块都是一次独立转写
  // （各自 3 分钟预算），此前这一层是整条录音链上唯一**既跨页存活又完全无法
  // 终止**的环节——录音 runtime 的 cancelTranscription() 只能停录完后的兜底
  // 全量转写，停不了这些分片。
  //
  // 中止不在这里吞：Promise.all 会把 AbortError 原样抛给调用方，由
  // processSegment 判断「是取消」还是「真失败」——真失败要给用户可行动的原因，
  // 取消则一个字都不该显示。
  const [sttResult, embedding] = await Promise.all([
    sttApi.transcribe({ audioBlob: opts.blob }, opts.signal),
    extractEmbedding(opts.blob).catch(() => new Float32Array()),
  ])
  if (!sttResult.text.trim()) return null

  void saveAudioPart(meetingId, opts.seq, opts.blob).catch(() => {})

  const { profileId, label } = opts.diarizer.identify(embedding)
  const lang = detectLang(sttResult.text)
  const seg: Omit<MeetingSegment, 'id'> = {
    meetingId,
    speakerLabel: label,
    lang,
    confidence: sttResult.confidence,
    startMs: opts.startMs,
    endMs: opts.endMs,
    text: sttResult.text.trim(),
  }
  const id = await saveSegment(seg)
  opts.segmentProfiles.set(id, profileId)
  const saved: MeetingSegment = { id, ...seg }
  // 按 startMs 有序插入：分段并发转写，完成顺序 ≠ 说话顺序，
  // 直接 push 会让 transcript 与实时列表乱序。
  let at = opts.segments.length
  while (at > 0 && opts.segments[at - 1].startMs > saved.startMs) at--
  opts.segments.splice(at, 0, saved)
  await updateTranscript(meetingId, opts.segments.map((s) => `[${s.speakerLabel}] ${s.text}`).join('\n'))

  void liveTranslate(saved.text, lang)
    .then((translation) => {
      if (!translation) return
      saved.translation = translation
      return updateSegmentTranslation(id, translation)
    })
    .catch(() => {})
  return saved
}
