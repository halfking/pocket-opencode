/**
 * 由 MIME 类型推导上传给转写后端的文件名（2026-10-01 抽离为共享模块）。
 *
 * 为什么文件名这么重要：后端按扩展名决定解码方式与「能不能切分」。
 * 发错扩展名的症状非常隐蔽——音频内容完全正确，但后端按错误格式解析，
 * 最终表现为「录音有内容但转不出文字」，且没有任何报错。
 *
 * 抽出原因：既有的单次转写与新增的全量/即时转写都要用同一张映射表。
 * 两处各写一份的话，改了一处忘了另一处就会出现「单次能转、全量不能转」。
 */

/** 常见录音容器的扩展名映射。缺省用 webm（MediaRecorder 在多数 WebView 的默认输出）。 */
const EXTENSION_BY_MIME: Record<string, string> = {
  'audio/mp4': 'm4a',
  'audio/mpeg': 'mp3',
  'audio/ogg': 'ogg',
  'audio/wav': 'wav',
  'audio/x-wav': 'wav',
  'audio/webm': 'webm',
  'audio/aac': 'aac',
  'audio/flac': 'flac',
}

/**
 * 把 MIME 类型转成 `recording.<ext>` 形式的文件名。
 *
 * 只取 `;` 前的媒体类型：`audio/webm;codecs=opus` 必须先剥掉参数，
 * 否则查表落空、拿到缺省的 webm 恰好掩盖了这个 bug——但 `audio/mp4;codecs=mp4a`
 * 这类会落空成 webm，扩展名就错了。
 */
export function filenameForMimeType(mimeType: string, baseName = 'recording'): string {
  const normalized = (mimeType || '').toLowerCase().split(';', 1)[0].trim()
  return `${baseName}.${EXTENSION_BY_MIME[normalized] || 'webm'}`
}
