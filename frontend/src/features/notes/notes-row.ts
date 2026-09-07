import { decryptString } from '../../native/crypto'
import { assetStore, type Asset } from '../../native/asset-store'
import type { LocalNote, NoteRow, NoteStatus, NoteStorageTier } from './notes-types'

export function parseTags(value: string | string[] | null | undefined, depth = 0): string[] | null {
  if (depth > 10) return null
  if (Array.isArray(value)) {
    const tags = value.filter((tag): tag is string => typeof tag === 'string')
    return tags.length > 0 ? tags : null
  }
  if (typeof value !== 'string' || !value.trim()) return null
  try {
    return parseTags(JSON.parse(value), depth + 1)
  } catch {
    return null
  }
}

function readAssetTimestamp(value: unknown, fallback: number): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim()) {
    const numeric = Number(value)
    if (Number.isFinite(numeric)) return numeric
    const parsed = Date.parse(value)
    if (!Number.isNaN(parsed)) return parsed
  }
  return fallback
}

export function assetToNote(asset: Asset): LocalNote {
  let meta: Record<string, unknown> = {}
  try { meta = JSON.parse(asset.metaJson || '{}') } catch { /* ignore */ }
  const tags = Array.isArray(meta.tags) ? meta.tags.filter((tag): tag is string => typeof tag === 'string') : null
  return {
    id: asset.id,
    workspaceId: asset.workspaceId,
    title: asset.title || null,
    content: asset.bodyText,
    contentType: 'text',
    domain: null,
    category: null,
    tags,
    audioPath: null,
    audioDurationMs: 0,
    createdByVoice: false,
    createdAt: readAssetTimestamp(meta.originalCreated, asset.createdAt),
    updatedAt: readAssetTimestamp(meta.originalUpdated, asset.updatedAt),
    storage: 'asset',
    source: asset.source ?? null,
    status: 'saved',
    storageTier: 'inline',
  }
}

export async function rowToNote(r: NoteRow | null): Promise<LocalNote | null> {
  if (!r) return null
  let decryptedContent = r.content
  if (r.encrypted_content === 1) {
    try {
      decryptedContent = await decryptString(r.content)
    } catch (e) {
      console.error(`[notes-store] 无法解密 note ${r.id} 的 content:`, e)
      decryptedContent = '[本地数据已锁定，请先解锁]'
    }
  }
  return {
    id: r.id,
    workspaceId: r.workspace_id,
    title: r.title,
    content: decryptedContent,
    contentType: r.content_type,
    domain: r.domain,
    category: r.category,
    tags: parseTags(r.tags),
    audioPath: r.audio_path,
    audioDurationMs: r.audio_duration_ms,
    createdByVoice: r.created_by_voice === 1,
    createdAt: r.created_at,
    updatedAt: r.updated_at,
    storage: 'local_notes',
    status: (r.status === 'draft' ? 'draft' : 'saved') as NoteStatus,
    storageTier: (r.storage_tier === 'file' ? 'file' : 'inline') as NoteStorageTier,
    summary: r.summary ?? null,
    searchText: r.search_text ?? null,
    bodyPath: r.body_path ?? null,
    mediaJson: r.media_json ?? null,
  }
}

export async function mergeImportedNotes(localNotes: LocalNote[], workspaceId: string, limit: number): Promise<LocalNote[]> {
  const assets = await assetStore.search({ workspaceId, kind: 'note', source: 'enex_import', limit })
  return [...localNotes, ...assets.map(assetToNote)]
    .sort((a, b) => b.updatedAt - a.updatedAt)
    .slice(0, limit)
}
