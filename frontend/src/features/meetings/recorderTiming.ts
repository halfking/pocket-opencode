/**
 * recorderTiming.ts — 会议录音计时与恢复判定的纯逻辑。
 *
 * 从 MeetingRecordView 抽出，便于 node --test 直接覆盖：
 * - ElapsedTracker：暂停/继续时的有效时长统计（排除暂停区间）；
 * - classifyRecovery：会议 + 音频分片状态 → 恢复动作分类。
 */

export interface ElapsedTracker {
  /** 进入录音态（或继续录音），从 now 起累计。 */
  start(now?: number): void
  /** 暂停：把 now-start 段计入累计并冻结。 */
  pause(now?: number): void
  /** 当前有效时长（ms，不含暂停区间）。未开始返回 0。 */
  elapsed(now?: number): number
}

/**
 * 创建有效时长跟踪器。now 可注入以便测试。
 * 用法约束：start 可重复调用（重复 start 先结掉上一段）；pause 幂等。
 */
export function createElapsedTracker(): ElapsedTracker {
  let accumulated = 0
  let segmentStart: number | null = null

  return {
    start(now = Date.now()) {
      if (segmentStart !== null) {
        accumulated += now - segmentStart
      }
      segmentStart = now
    },
    pause(now = Date.now()) {
      if (segmentStart !== null) {
        accumulated += now - segmentStart
        segmentStart = null
      }
    },
    elapsed(now = Date.now()) {
      if (segmentStart === null) return accumulated
      return accumulated + (now - segmentStart)
    },
  }
}

export type MeetingRecovery =
  | 'none' // 正常：无需恢复动作
  | 'recoverable' // 有已落盘音频分片但未完成转写 → 可恢复

/**
 * 判断一条会议记录是否处于"录音中断、可恢复"状态。
 * 判据：未删除、无转写、存在音频分片（partCount > 0）。
 * durationMs 由分片写入时同步推进，仅用于展示，不参与判定。
 */
export function classifyRecovery(input: {
  deletedAt: number | null
  transcript: string | null
  partCount: number
}): MeetingRecovery {
  if (input.deletedAt !== null) return 'none'
  if (input.transcript !== null && input.transcript !== '') return 'none'
  return input.partCount > 0 ? 'recoverable' : 'none'
}

/** Blob → base64（无 data: 前缀）。音频分片落盘用。 */
export function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onloadend = () => {
      const dataUrl = reader.result as string
      resolve(dataUrl.split(',')[1] || '')
    }
    reader.onerror = reject
    reader.readAsDataURL(blob)
  })
}

/** 分片 → 单个 Blob（恢复转写时按 seq 顺序拼接）。
 * base64 必须逐段解码再按字节拼接：字符串直连会被段内 padding 截断。 */
export function partsToBlob(
  parts: { seq: number; mimeType: string; dataBase64: string }[],
): Blob | null {
  if (parts.length === 0) return null
  const blobs = parts.map((p) => {
    const bin = atob(p.dataBase64)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    return new Blob([bytes], { type: p.mimeType })
  })
  return new Blob(blobs, { type: parts[0].mimeType })
}
