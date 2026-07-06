/**
 * Markdown rendering helper for chat messages.
 *
 * Wraps the `marked` library with DOMPurify sanitization so assistant
 * output can be rendered as HTML without XSS risk. Falls back to a
 * safe plain-text escape if `marked` is unavailable (e.g., during
 * very early bootstrapping).
 */
import { marked } from 'marked'
import DOMPurify from 'dompurify'

// Configure marked once at module load — GFM + line breaks off (assistant
// already controls newlines through text content).
marked.setOptions({
  gfm: true,
  breaks: false,
})

/**
 * Render a markdown string to safe HTML.
 * Returns an empty string for null/undefined input.
 */
export function renderMarkdown(text: string | null | undefined): string {
  if (!text) return ''
  try {
    const rawHtml = marked.parse(text, { async: false }) as string
    return DOMPurify.sanitize(rawHtml, {
      ALLOWED_TAGS: [
        'a', 'b', 'i', 'em', 'strong', 'code', 'pre', 'br', 'p', 'span',
        'ul', 'ol', 'li', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
        'blockquote', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
        'hr', 'input',
      ],
      ALLOWED_ATTR: ['href', 'title', 'class', 'target', 'rel', 'checked', 'disabled'],
    })
  } catch (e) {
    // Sanity fallback: HTML-escape and preserve newlines.
    return escapeHtml(text).replace(/\n/g, '<br>')
  }
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}