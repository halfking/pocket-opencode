/**
 * voiceprints-store.ts — 本地声纹库 CRUD
 */
import { localDB } from '../../native/local-db'
import {
  blobToEmbedding, embeddingToBlob, extractEmbedding,
} from '../../native/speaker-embedding'
import type { SpeakerProfile } from '../../native/speaker-diarization'

export interface LocalVoiceprint {
  id: string
  displayName: string
  sampleCount: number
  createdAt: number
}

export async function listVoiceprints(): Promise<LocalVoiceprint[]> {
  const rows = await localDB.query<any>(
    'SELECT id, display_name, sample_count, created_at FROM local_voiceprints ORDER BY created_at DESC',
  )
  return rows.map((r) => ({
    id: r.id,
    displayName: r.display_name,
    sampleCount: r.sample_count ?? 1,
    createdAt: r.created_at,
  }))
}

export async function loadSpeakerProfiles(): Promise<SpeakerProfile[]> {
  const rows = await localDB.query<any>('SELECT * FROM local_voiceprints')
  return rows.map((r) => ({
    id: r.id,
    label: r.display_name,
    embedding: blobToEmbedding(r.embedding),
    sampleCount: r.sample_count ?? 1,
  }))
}

export async function saveVoiceprint(input: {
  id?: string
  displayName: string
  embedding: Float32Array
}): Promise<string> {
  const id = input.id ?? `vp-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
  const now = Date.now()
  const existing = await localDB.queryOne<any>(
    'SELECT id, sample_count FROM local_voiceprints WHERE id = ?', [id],
  )
  if (existing) {
    await localDB.run(
      'UPDATE local_voiceprints SET display_name = ?, embedding = ?, sample_count = ? WHERE id = ?',
      [input.displayName, embeddingToBlob(input.embedding), (existing.sample_count ?? 1) + 1, id],
    )
  } else {
    await localDB.run(
      'INSERT INTO local_voiceprints (id, display_name, embedding, sample_count, created_at) VALUES (?,?,?,?,?)',
      [id, input.displayName, embeddingToBlob(input.embedding), 1, now],
    )
  }
  return id
}

/** 从音频样本创建/更新声纹 */
export async function enrollFromAudio(
  displayName: string,
  audioBlob: Blob,
  existingId?: string,
): Promise<string> {
  const embedding = await extractEmbedding(audioBlob)
  return saveVoiceprint({ id: existingId, displayName, embedding })
}

export async function deleteVoiceprint(id: string): Promise<void> {
  await localDB.run('DELETE FROM local_voiceprints WHERE id = ?', [id])
}
