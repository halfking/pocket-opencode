import type { NoteMediaInput } from './notes-types.ts'
import { extractLocalTags, inferDomain, mergeTags, suggestTitle } from './note-tags.ts'

export function parseTagsInput(raw: string): string[] {
  return raw.split(/[,，]/).map((t) => t.trim()).filter(Boolean)
}

export function tagsFromArray(tags: string[] | null | undefined): string {
  return tags && tags.length ? tags.join(', ') : ''
}

export function dataUrlToBlob(dataUrl: string): Blob {
  const [head, body] = dataUrl.split(',')
  const mime = /data:([^;]+)/.exec(head || '')?.[1] || 'application/octet-stream'
  const bin = atob(body || '')
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return new Blob([bytes], { type: mime })
}

export function attachmentsToMedia(raw: { dataUrl: string; name: string }[]): NoteMediaInput[] {
  return raw.map((a) => ({
    kind: 'image' as const,
    blob: dataUrlToBlob(a.dataUrl),
    mime: a.dataUrl.split(';')[0]?.replace('data:', '') || 'image/jpeg',
  }))
}

export function fileToVideoMedia(file: File): NoteMediaInput {
  return { kind: 'video', blob: file, mime: file.type || 'video/mp4' }
}

export async function extractTagsForForm(content: string, currentTags: string): Promise<{
  tagsInput: string
  title: string
  domain: ReturnType<typeof inferDomain>
}> {
  const local = extractLocalTags(content)
  let extra: string[] = []
  try {
    const { extractTagsWithAi } = await import('./note-search.ts')
    extra = await extractTagsWithAi(content)
  } catch { /* offline */ }
  return {
    tagsInput: mergeTags(parseTagsInput(currentTags), [...local, ...extra]).join(', '),
    title: suggestTitle(content),
    domain: inferDomain(content),
  }
}
