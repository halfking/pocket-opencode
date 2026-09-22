/**
 * 给 MediaRecorder 选最稳的 MIME 类型(2026-09-21)。
 *
 * 真机(redmi 等 Android WebView,Chromium 内核)对 audio/webm 实际不支持或
 * timeslice 不工作——选不到 webm/opus 时降级到 audio/mp4(aac);都选不到
 * 留空字符串让浏览器走默认路径(浏览器会挑最稳的)。
 */
export const SUPPORTED_RECORDER_MIME_CANDIDATES: readonly string[] = [
  'audio/webm;codecs=opus',
  'audio/webm',
  'audio/mp4;codecs=mp4a.40.2',
  'audio/mp4',
] as const

export type MediaRecorderProbe = {
  isTypeSupported: (mime: string) => boolean
}

/** 内部助手:可注入探针,纯函数无副作用。 */
export function pickSupportedRecorderMime(
  probe?: MediaRecorderProbe,
): string {
  // 没有 MediaRecorder(SSR / Node 测试) → 留空让上层走默认路径
  if (!probe) return ''
  for (const c of SUPPORTED_RECORDER_MIME_CANDIDATES) {
    try {
      if (probe.isTypeSupported(c)) return c
    } catch {
      // 探针抛错时继续测下一个,不要因为单个 mime 探测失败就整体崩掉
    }
  }
  return ''
}
