/**
 * Notes API — voice notes and knowledge base.
 * Backed by kxmemory FastAPI for AI (classify/SSOT/graph) and pocketd
 * SQLite for offline metadata caching. See notes module design in the
 * personal-assistant plan.
 */
import { http } from './http'

export type NoteDomain = 'work' | 'study' | 'life' | 'idea'
export type NoteContentType = 'voice' | 'text' | 'mixed'

export interface Note {
  id: string
  userId: string
  workspaceId?: string
  title?: string
  content: string
  contentType: NoteContentType
  domain?: NoteDomain
  category?: string
  tags?: string[]
  parentId?: string
  voiceSessionId?: string
  audioFilePath?: string
  audioDuration?: number
  isLatest: boolean
  versionNumber: number
  createdAt: string
  updatedAt: string
  createdByVoice: boolean
}

export interface NoteInput {
  content: string
  title?: string
  contentType?: NoteContentType
  domain?: NoteDomain
  tags?: string[]
  voiceSessionId?: string
  audioFilePath?: string
  audioDuration?: number
}

export const notesApi = {
  list(domain?: NoteDomain): Promise<{ notes: Note[] }> {
    const qs = domain ? `?domain=${domain}` : ''
    return http(`/api/notes${qs}`)
  },
  get(id: string): Promise<Note> {
    return http(`/api/notes/${id}`)
  },
  create(input: NoteInput): Promise<Note> {
    return http('/api/notes', {
      method: 'POST',
      body: JSON.stringify(input),
    })
  },
  update(id: string, patch: Partial<NoteInput>): Promise<Note> {
    return http(`/api/notes/${id}`, {
      method: 'PUT',
      body: JSON.stringify(patch),
    })
  },
  delete(id: string): Promise<void> {
    return http(`/api/notes/${id}`, { method: 'DELETE' })
  },
  /** Ask kxmemory to classify + auto-tag a note. */
  classify(id: string): Promise<{ domain: NoteDomain; category: string; tags: string[] }> {
    return http(`/api/notes/${id}/classify`, { method: 'POST' })
  },
  /** Hybrid search across notes. */
  search(query: string): Promise<{ notes: Note[] }> {
    return http(`/api/notes/search?q=${encodeURIComponent(query)}`)
  },
}
