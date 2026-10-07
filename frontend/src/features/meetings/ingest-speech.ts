import { unref, type MaybeRef } from 'vue'
import { sttApi } from '../../api/stt'
import { extractEmbedding } from '../../native/speaker-embedding'
import { detectLang } from '../../native/detect-lang'
import { saveAudioPart } from './audio-parts'
import { liveTranslate } from './live-translate'
import { updateSegmentTranslation } from './meetings-live'
import { saveSegment, updateTranscript } from './meetings-store'
import type { SpeakerDiarizer } from '../../native/speaker-diarization'
import { renderTranscript } from './meeting-dedup.ts'
import type { MeetingSegment } from './meetings-store.ts'

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
  // ★ 相邻段去重（2026-10-06）。VadSegmenter 的取片窗口带 sliceMs 余量以
  //   补偿 MediaRecorder 缓冲延迟，RAF 节流又会让语音起点判定回退，
  //   相邻两段因此可能取到重叠音频并被转写两次 ⇒ 文本重复。
  //   整条前端链路此前**完全没有**去重（后端 mergeIncremental 属于
  //   IncrementalTranscriber，前端从不调用它），所以必须在这一层消解。
  await updateTranscript(meetingId, renderTranscript(opts.segments))

  void liveTranslate(saved.text, lang)
    .then((translation) => {
      if (!translation) return
      saved.translation = translation
      return updateSegmentTranslation(id, translation)
    })
    .catch(() => {})
  return saved
}
