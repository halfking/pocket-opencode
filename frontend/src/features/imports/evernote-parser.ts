import { XMLParser } from 'fast-xml-parser'
import { enmlToMarkdown } from './enml-to-markdown'

export interface EvernoteResource {
  data: string
  mime: string
  filename: string | null
  hash: string
}

export interface EvernoteNote {
  title: string
  content: string
  contentMarkdown: string
  createdAt: number | null
  updatedAt: number | null
  tags: string[]
  author: string | null
  sourceUrl: string | null
  enexId: string
  resources: EvernoteResource[]
}

const parser = new XMLParser({
  ignoreAttributes: false,
  attributeNamePrefix: '@_',
  parseTagValue: false,
  trimValues: true,
})

export function parseEnex(xml: string): EvernoteNote[] {
  const root = parser.parse(xml)['en-export'] || {}
  const rawNotes = root.note ? (Array.isArray(root.note) ? root.note : [root.note]) : []
  return rawNotes.map(parseNote)
}

function parseNote(note: any): EvernoteNote {
  const title = decodeEntities(String(note.title || ''))
  const content = String(note.content || '')
  const attributes = note['note-attributes'] || {}
  const resources = toArray(note.resource).map((resource: any) => {
    const data = String(resource?.data?.['#text'] ?? resource?.data ?? '')
    const resourceAttributes = resource?.['resource-attributes'] || {}
    return {
      data,
      mime: String(resource?.mime || 'application/octet-stream'),
      filename: resourceAttributes['file-name'] ? String(resourceAttributes['file-name']) : null,
      hash: String(resourceAttributes['attachment-hash'] || stableHash(data)),
    }
  }).filter((resource: EvernoteResource) => resource.data)

  const createdAt = parseDate(note.created)
  const updatedAt = parseDate(note.updated)
  return {
    title,
    content,
    contentMarkdown: enmlToMarkdown(content),
    createdAt,
    updatedAt,
    tags: toArray(note.tag).map(String),
    author: attributes.author ? String(attributes.author) : null,
    sourceUrl: attributes['source-url'] ? String(attributes['source-url']) : null,
    enexId: stableHash(`${title}|${createdAt ?? ''}|${updatedAt ?? ''}|${content}`),
    resources,
  }
}

function toArray(value: unknown): any[] {
  if (value === undefined || value === null || value === '') return []
  return Array.isArray(value) ? value : [value]
}

function parseDate(value: unknown): number | null {
  if (!value) return null
  const raw = String(value).trim()
  const compact = raw.match(/^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})(Z)?$/)
  if (compact) {
    const [, year, month, day, hour, minute, second, utc] = compact
    const iso = `${year}-${month}-${day}T${hour}:${minute}:${second}${utc ? 'Z' : ''}`
    const timestamp = new Date(iso).getTime()
    return Number.isNaN(timestamp) ? null : timestamp
  }
  const date = new Date(raw)
  return Number.isNaN(date.getTime()) ? null : date.getTime()
}

function decodeEntities(value: string): string {
  return value
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
}

function stableHash(value: string): string {
  let hash = 2166136261
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index)
    hash = Math.imul(hash, 16777619)
  }
  return `enex-${(hash >>> 0).toString(36)}`
}
