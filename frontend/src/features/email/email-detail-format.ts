import DOMPurify from 'dompurify'
import { looksLikeHtml } from './email-body-format'

export function sanitizeEmailHtml(raw: string): string {
  if (!looksLikeHtml(raw)) return ''
  return DOMPurify.sanitize(raw, {
    ALLOWED_TAGS: [
      'h1', 'h2', 'h3', 'h4', 'p', 'br', 'strong', 'em', 'u', 'a', 'img',
      'ul', 'ol', 'li', 'blockquote', 'pre', 'code', 'table', 'thead',
      'tbody', 'tr', 'th', 'td', 'hr', 'div', 'span', 'font',
    ],
    ALLOWED_ATTR: ['href', 'src', 'alt', 'title', 'class', 'style'],
    ALLOW_DATA_ATTR: false,
    FORBID_TAGS: ['script', 'iframe'],
  })
}
