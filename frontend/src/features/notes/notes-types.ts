export type NoteStatus = 'draft' | 'saved'
export type NoteStorageTier = 'inline' | 'file'
export type NoteFileKind = 'body' | 'audio' | 'image' | 'video' | 'file'

export interface LocalNote {
  id: string
  workspaceId: string | null
  title: string | null
  content: string
  contentType: string
  domain: string | null
  category: string | null
  tags: string[] | null
  audioPath: string | null
  audioDurationMs: number
  createdByVoice: boolean
  createdAt: number
  updatedAt: number
  storage?: 'local_notes' | 'asset'
  source?: string | null
  status?: NoteStatus
  storageTier?: NoteStorageTier
  summary?: string | null
  searchText?: string | null
  bodyPath?: string | null
  mediaJson?: string | null
}

export interface SearchResult {
  note: LocalNote
  score: number
  source: 'fts' | 'vector' | 'hybrid'
}

export interface NoteMediaInput {
  kind: Exclude<NoteFileKind, 'body'>
  blob: Blob
  mime?: string
  ext?: string
  durationMs?: number
}

export interface NoteFileRecord {
  id: string
  noteId: string
  kind: NoteFileKind
  relPath: string
  mime: string
  sizeBytes: number
  durationMs: number
}

export interface CreateNoteInput {
  title?: string
  content: string
  domain?: string
  tags?: string[]
  audioPath?: string
  audioDurationMs?: number
  audioBlob?: Blob
  contentType?: string
  workspaceId?: string
  status?: NoteStatus
  createdByVoice?: boolean
  media?: NoteMediaInput[]
}

export interface NoteRow {
  id: string
  workspace_id: string | null
  title: string | null
  content: string
  content_type: string
  domain: string | null
  category: string | null
  tags: string | null
  audio_path: string | null
  audio_duration_ms: number
  created_by_voice: number
  created_at: number
  updated_at: number
  deleted_at: number | null
  encrypted_content?: number
  status?: string | null
  storage_tier?: string | null
  summary?: string | null
  search_text?: string | null
  body_path?: string | null
  media_json?: string | null
}
