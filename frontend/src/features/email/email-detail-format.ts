import DOMPurify from 'dompurify'
import { looksLikeHtml } from './email-body-format.ts'
import { injectBaseStyle, normalizeStyleBlocks } from './email-body-style.ts'

/**
 * 邮件 HTML 净化 + 排版归一化。
 *
 * 顺序很关键：
 *   1. 注入基准样式（CJK 字体兜底、图片不溢出）——**在净化前**，这样注入的
 *      <style> 会被一起过白名单，净化后仍然保留。
 *   2. 归一化邮件自带的 <style>：剥掉远程 @import/@font-face，给 font-family
 *      补 CJK 兜底。
 *   3. 最后才 sanitize：此时字体样式已经就位。
 *
 * 反过来做（先 sanitize 再注入）会踩坑——DOMPurify 默认会剥掉 <style>，
 * 注入的基准样式就没了，等于什么都没做。
 */
export function sanitizeEmailHtml(raw: string): string {
  if (!looksLikeHtml(raw)) return ''
  const withBase = injectBaseStyle(raw)
  const withFonts = normalizeStyleBlocks(withBase)
  return DOMPurify.sanitize(withFonts, {
    ALLOWED_TAGS: [
      'h1', 'h2', 'h3', 'h4', 'p', 'br', 'strong', 'em', 'u', 'a', 'img',
      'ul', 'ol', 'li', 'blockquote', 'pre', 'code', 'table', 'thead',
      'tbody', 'tr', 'th', 'td', 'hr', 'div', 'span', 'font', 'style',
    ],
    ALLOWED_ATTR: ['href', 'src', 'alt', 'title', 'class', 'style', 'width', 'height', 'border', 'cellpadding', 'cellspacing'],
    ALLOW_DATA_ATTR: false,
    FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'form', 'input'],
    // 只放行 data: 与 http(s):/cid: 之外的常规图源；javascript: 由 DOMPurify 默认拦。
    ALLOWED_URI_REGEXP: /^(?:https?:|data:image\/(?:png|jpe?g|gif|webp|bmp|avif|svg\+xml);base64,)/i,
  })
}
