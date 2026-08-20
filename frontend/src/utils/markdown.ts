/**
 * Markdown rendering helper for chat messages and shared sanitization
 * utilities for any caller that needs to persist or render HTML safely.
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
 * Shared DOMPurify configuration. Aligns with the markdown rendering
 * needs of chat / notes / email / meeting summaries: a small set of
 * semantic tags plus limited attributes, no data-* attributes, no
 * inline event handlers, no style/script tags.
 */
const PURIFY_CONFIG = {
  ALLOWED_TAGS: [
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
    'p', 'br', 'strong', 'em', 'u', 'del', 's',
    'a', 'img',
    'ul', 'ol', 'li',
    'blockquote', 'pre', 'code',
    'table', 'thead', 'tbody', 'tr', 'th', 'td',
    'hr', 'div', 'span',
  ],
  ALLOWED_ATTR: ['href', 'src', 'alt', 'title', 'class', 'id'],
  ALLOW_DATA_ATTR: false,
  FORBID_TAGS: ['style', 'script'],
  FORBID_ATTR: ['onerror', 'onload', 'onclick', 'onmouseover'],
}

/**
 * Run an arbitrary HTML string through DOMPurify with the shared config.
 * Use this as the last step when persisting or rendering HTML built from
 * user / AI / transcript content (e.g., meeting note drafts).
 */
export function sanitizeHtml(rawHtml: string): string {
  if (!rawHtml) return ''
  return DOMPurify.sanitize(rawHtml, PURIFY_CONFIG)
}

/**
 * Render a markdown string to safe HTML.
 * Returns an empty string for null/undefined input.
 */
export function renderMarkdown(text: string | null | undefined): string {
  if (!text) return ''
  try {
    const rawHtml = marked.parse(text, { async: false }) as string
    return sanitizeHtml(rawHtml)
  } catch (e) {
    // Sanity fallback: HTML-escape and preserve newlines.
    return escapeHtml(text).replace(/\n/g, '<br>')
  }
}

/**
 * Render a plain-text fragment (no markdown) to safe HTML.
 *
 * Escapes angle brackets / quotes so the result cannot inject tags or
 * attributes, preserves user-intended newlines as `<br>`, and runs the
 * assembled string through DOMPurify as defense-in-depth before it is
 * persisted to PKM / notes.
 *
 * Used for transcripts and other free-form text that should not be
 * interpreted as Markdown.
 */
export function renderPlainText(text: string | null | undefined): string {
  if (!text) return ''
  const escaped = escapeHtml(text).replace(/\n/g, '<br>')
  return sanitizeHtml(escaped)
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}