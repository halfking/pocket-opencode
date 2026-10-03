/**
 * digest-share.ts — 把每日摘要变成「一键分享到微博 / 微信朋友圈」的内容。
 *
 * 两个产物：
 *  1. shareDigestText(digest)  纯文本：任何地方都能粘（微博正文、朋友圈正文）。
 *  2. renderDigestCard(digest) 1080×1350 PNG 画幅：朋友圈九宫格/微博配图。
 *
 * 为什么卡片图在前端用 canvas 画，而不是复用后端 /api/rss/items/{id}/share-card：
 * 后端那张卡是纯 stdlib 的 5×7 ASCII 点阵字体，**中文会整段变成 "?"**
 * （backend/internal/rss/sharecard.go 的 truncateASCII/wrapASCII 对 r > 127
 * 一律替换）。而日报的标题几乎全是中文，用后端那张图发出去等于发一堆问号。
 * WebView 的 canvas 自带中文字体，零新增依赖。
 */
import type { RSSDigest } from '../../api/rss'

export const DIGEST_CARD_W = 1080
export const DIGEST_CARD_H = 1350

/** 分享文本：截到前 N 条，保证在微博/朋友圈正文长度内还能读完。 */
export function shareDigestText(digest: RSSDigest, maxItems = 12): string {
  if (!digest) return ''
  const lines: string[] = [digest.headline, '']
  let n = 0
  for (const sec of digest.sections) {
    if (n >= maxItems) break
    lines.push(`【${sec.label}】`)
    for (const it of sec.items) {
      if (n >= maxItems) break
      const src = it.sourceTitle ? ` — ${it.sourceTitle}` : ''
      lines.push(`${n + 1}. ${it.title}${src}`)
      if (it.url) lines.push(`   ${it.url}`)
      n++
    }
    lines.push('')
  }
  if (digest.itemCount > n) lines.push(`…另有 ${digest.itemCount - n} 条，打开 OpenPocket 看完整日报`)
  return lines.join('\n').trim()
}

/** 卡片图上真正画出来的条目数：多了就溢出画幅，反而看不清。 */
export function cardItems(digest: RSSDigest, max = 7): { section: string; item: string }[] {
  const out: { section: string; item: string }[] = []
  for (const sec of digest.sections) {
    for (const it of sec.items) {
      if (out.length >= max) return out
      out.push({ section: sec.label, item: it.title })
    }
  }
  return out
}

/** 按像素宽度粗略截断（CJK 约等于两倍宽度）。 */
function ellipsize(text: string, maxPx: number, fontPx: number): string {
  const perRune = fontPx * 0.62
  const maxRunes = Math.max(4, Math.floor(maxPx / perRune))
  const runes = Array.from(text)
  if (runes.length <= maxRunes) return text
  return runes.slice(0, maxRunes - 1).join('') + '…'
}

/** 导出给单测：截断行为本身需要被验证（卡片图挤爆画幅是最难肉眼发现的退化）。 */
export function ellipsizeForTest(text: string, maxPx: number, fontPx: number, maxLines = 1): string {
  return wrap(text, maxPx, fontPx, maxLines).join('')
}

/** 把文本按宽度折行。 */
function wrap(text: string, maxPx: number, fontPx: number, maxLines: number): string[] {
  const perRune = fontPx * 0.62
  const maxRunes = Math.max(4, Math.floor(maxPx / perRune))
  const out: string[] = []
  let cur = ''
  for (const ch of text) {
    if (cur.length >= maxRunes) {
      out.push(cur)
      cur = ''
      if (out.length >= maxLines) break
    }
    cur += ch
  }
  const rest = text.slice(out.join('').length)
  if (out.length < maxLines && cur) out.push(cur)
  else if (out.length >= maxLines && rest.trim()) {
    out[maxLines - 1] = ellipsize(out[maxLines - 1] + rest, maxPx, fontPx)
  }
  return out
}

export interface DigestCardTheme {
  bg: string
  fg: string
  muted: string
  accent: string
}

export const lightCardTheme: DigestCardTheme = {
  bg: '#FAFAFA', fg: '#1A1A1A', muted: '#6B7280', accent: '#2563EB',
}
export const darkCardTheme: DigestCardTheme = {
  bg: '#0F1419', fg: '#E5E7EB', muted: '#9CA3AF', accent: '#60A5FA',
}

/**
 * 把日报画到 canvas 上。返回 HTMLCanvasElement，调用方自行 toBlob/分享。
 * 抽成独立函数是为了能直接单测：布局退化（例如中文字体缺失导致全是方块）
 * 是肉眼很难在真机上第一时间看出来的。
 */
export function drawDigestCard(
  canvas: HTMLCanvasElement,
  digest: RSSDigest,
  theme: DigestCardTheme = lightCardTheme,
): void {
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('canvas 2d context unavailable')
  canvas.width = DIGEST_CARD_W
  canvas.height = DIGEST_CARD_H

  ctx.fillStyle = theme.bg
  ctx.fillRect(0, 0, DIGEST_CARD_W, DIGEST_CARD_H)

  // 顶部色条
  ctx.fillStyle = theme.accent
  ctx.fillRect(0, 0, DIGEST_CARD_W, 16)

  // 日期
  ctx.fillStyle = theme.muted
  ctx.font = '36px system-ui, -apple-system, "Noto Sans CJK SC", sans-serif'
  ctx.textBaseline = 'top'
  ctx.fillText(digest.date, 64, 72)

  // 标题（最多 2 行）
  ctx.fillStyle = theme.fg
  ctx.font = 'bold 64px system-ui, -apple-system, "Noto Sans CJK SC", sans-serif'
  const headLines = wrap(digest.headline, DIGEST_CARD_W - 128, 64, 2)
  let y = 132
  for (const line of headLines) {
    ctx.fillText(line, 64, y)
    y += 80
  }

  // 分隔线
  y += 12
  ctx.strokeStyle = theme.muted
  ctx.globalAlpha = 0.4
  ctx.beginPath()
  ctx.moveTo(64, y)
  ctx.lineTo(DIGEST_CARD_W - 64, y)
  ctx.stroke()
  ctx.globalAlpha = 1
  y += 40

  // 条目
  ctx.font = '40px system-ui, -apple-system, "Noto Sans CJK SC", sans-serif'
  const rows = cardItems(digest, 7)
  for (const row of rows) {
    if (y > DIGEST_CARD_H - 240) break
    ctx.fillStyle = theme.accent
    ctx.font = 'bold 30px system-ui, -apple-system, "Noto Sans CJK SC", sans-serif'
    ctx.fillText(row.section, 64, y)
    ctx.fillStyle = theme.fg
    ctx.font = '40px system-ui, -apple-system, "Noto Sans CJK SC", sans-serif'
    const lines = wrap(row.item, DIGEST_CARD_W - 180, 40, 2)
    for (const line of lines) {
      if (y > DIGEST_CARD_H - 240) break
      ctx.fillText(line, 116, y)
      y += 52
    }
    y += 18
  }

  // 底部
  ctx.fillStyle = theme.muted
  ctx.font = '30px system-ui, -apple-system, "Noto Sans CJK SC", sans-serif'
  ctx.fillText(`共 ${digest.itemCount} 条 · ${digest.sourceCount} 个来源 · OpenPocket 每日摘要`, 64, DIGEST_CARD_H - 110)
}

/** 生成卡片 PNG 的 dataURL。 */
export function renderDigestCardDataURL(digest: RSSDigest, theme: DigestCardTheme = lightCardTheme): string {
  if (typeof document === 'undefined') throw new Error('no document: card rendering needs a DOM')
  const canvas = document.createElement('canvas')
  drawDigestCard(canvas, digest, theme)
  return canvas.toDataURL('image/png')
}

/** 从 dataURL 取出 base64 部分，供 Capacitor Filesystem.writeFile 用。 */
export function dataURLToBase64(dataURL: string): string {
  const i = dataURL.indexOf(',')
  return i >= 0 ? dataURL.slice(i + 1) : dataURL
}
