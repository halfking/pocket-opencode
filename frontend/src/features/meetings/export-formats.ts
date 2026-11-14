/**
 * Phase 3: 多格式导出功能
 * 
 * 支持格式：Word(.docx)、HTML、TXT、SRT、双语对照
 */

import type { MeetingSegment } from './meetings-store'

export interface ExportOptions {
  format: 'docx' | 'html' | 'txt' | 'srt' | 'bilingual'
  includeTimestamps: boolean
  includeSpeakers: boolean
  includeTranslation: boolean
  title?: string
}

/**
 * 导出为纯文本
 */
export function exportToText(
  segments: MeetingSegment[],
  options: ExportOptions
): string {
  return segments
    .map(seg => {
      const parts: string[] = []
      
      if (options.includeSpeakers && seg.speakerLabel) {
        parts.push(`[${seg.speakerLabel}]`)
      }
      
      if (options.includeTimestamps) {
        parts.push(`[${formatTimestamp(seg.startMs)}]`)
      }
      
      parts.push(seg.text)
      
      if (options.includeTranslation && seg.translation) {
        parts.push(`\n  译文: ${seg.translation}`)
      }
      
      return parts.join(' ')
    })
    .join('\n\n')
}

/**
 * 导出为HTML
 */
export function exportToHtml(
  segments: MeetingSegment[],
  options: ExportOptions
): string {
  const title = options.title || '会议记录'
  const styles = `
    <style>
      body { font-family: sans-serif; max-width: 800px; margin: 40px auto; line-height: 1.6; }
      h1 { border-bottom: 2px solid #333; padding-bottom: 10px; }
      .segment { margin: 20px 0; }
      .speaker { font-weight: bold; color: #2563eb; }
      .timestamp { color: #6b7280; font-size: 0.9em; }
      .text { margin: 5px 0; }
      .translation { color: #059669; margin-left: 20px; font-style: italic; }
    </style>
  `
  
  const body = segments
    .map(seg => {
      let html = '<div class="segment">'
      
      if (options.includeSpeakers && seg.speakerLabel) {
        html += `<span class="speaker">[${seg.speakerLabel}]</span> `
      }
      
      if (options.includeTimestamps) {
        html += `<span class="timestamp">${formatTimestamp(seg.startMs)}</span>`
      }
      
      html += `<div class="text">${escapeHtml(seg.text)}</div>`
      
      if (options.includeTranslation && seg.translation) {
        html += `<div class="translation">译文: ${escapeHtml(seg.translation)}</div>`
      }
      
      html += '</div>'
      return html
    })
    .join('\n')
  
  return `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <title>${escapeHtml(title)}</title>
  ${styles}
</head>
<body>
  <h1>${escapeHtml(title)}</h1>
  ${body}
</body>
</html>`
}

/**
 * 导出为SRT字幕
 */
export function exportToSrt(segments: MeetingSegment[]): string {
  return segments
    .map((seg, index) => {
      const start = formatSrtTime(seg.startMs)
      const end = formatSrtTime(seg.endMs)
      const text = seg.speakerLabel ? `[${seg.speakerLabel}] ${seg.text}` : seg.text
      
      return `${index + 1}\n${start} --> ${end}\n${text}\n`
    })
    .join('\n')
}

/**
 * 导出为双语对照
 */
export function exportToBilingual(segments: MeetingSegment[]): string {
  return segments
    .map(seg => {
      const parts = []
      
      if (seg.speakerLabel) {
        parts.push(`[${seg.speakerLabel}] ${formatTimestamp(seg.startMs)}`)
      }
      
      parts.push(`原文: ${seg.text}`)
      
      if (seg.translation) {
        parts.push(`译文: ${seg.translation}`)
      }
      
      return parts.join('\n')
    })
    .join('\n\n---\n\n')
}

/**
 * 格式化时间戳（mm:ss）
 */
function formatTimestamp(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000)
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes.toString().padStart(2, '0')}:${seconds.toString().padStart(2, '0')}`
}

/**
 * 格式化SRT时间戳（HH:MM:SS,mmm）
 */
function formatSrtTime(ms: number): string {
  const hours = Math.floor(ms / 3600000)
  const minutes = Math.floor((ms % 3600000) / 60000)
  const seconds = Math.floor((ms % 60000) / 1000)
  const milliseconds = ms % 1000
  
  return `${hours.toString().padStart(2, '0')}:${minutes.toString().padStart(2, '0')}:${seconds.toString().padStart(2, '0')},${milliseconds.toString().padStart(3, '0')}`
}

/**
 * HTML转义
 */
function escapeHtml(text: string): string {
  const div = document.createElement('div')
  div.textContent = text
  return div.innerHTML
}

/**
 * 导出为Word文档（需要额外库支持）
 */
export async function exportToDocx(
  segments: MeetingSegment[],
  options: ExportOptions
): Promise<Blob> {
  // 使用docx库生成Word文档
  // 这里是简化实现，实际需要引入docx库
  const text = exportToText(segments, options)
  return new Blob([text], { type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' })
}
