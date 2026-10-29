/**
 * voiceprints-store.ts — 本地声纹库 CRUD
 */
import { localDB } from '../../native/local-db'
import { captureRecordingDb, type RecordingDb } from './recording-operation'
import {
  blobToEmbedding, embeddingToBlob, extractEmbeddingResult,
} from '../../native/speaker-embedding'
import type { SpeakerProfile } from '../../native/speaker-diarization'
import {
  nextSampleCount, planVoiceprintWrite, SQL_DELETE_ALL, SQL_DELETE_ONE,
  SQL_ENROLL_AGAIN, SQL_EXISTS, SQL_INSERT, SQL_LIST, SQL_RENAME,
} from './voiceprint-writes'

export interface LocalVoiceprint {
  id: string
  displayName: string
  createdAt: number
}

// ⚠ 刻意**不**在这里暴露 sample_count。
// 全仓读数：sample_count 只被写、从不影响任何决策 —— speaker-diarization.ts:52
// 只在匹配命中时 `best.sampleCount++`，loadProfile 的阈值计算不读它；
// 而 saveVoiceprint 每被调用一次就 +1，labelSpeaker（改一次名调一次）
// 同样会 +1 ⇒ 它的真实含义是「被匹配过几次 + 改过几次名」，
// 不是「注册了几个音频样本」。
// 把它当「样本数」显示给用户是一个**可被证伪的假数字**，故不取。

export async function listVoiceprints(): Promise<LocalVoiceprint[]> {
  const rows = await localDB.query<any>(SQL_LIST)
  return rows.map((r) => ({
    id: r.id,
    displayName: r.display_name,
    createdAt: r.created_at,
  }))
}

export async function loadSpeakerProfiles(db: RecordingDb = captureRecordingDb()): Promise<SpeakerProfile[]> {
  const rows = await db.query<any>('SELECT * FROM local_voiceprints')
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
  if (planVoiceprintWrite(!!existing).op === 'rename') {
    await localDB.run(SQL_ENROLL_AGAIN, [
      input.displayName, embeddingToBlob(input.embedding),
      nextSampleCount(existing?.sample_count), id,
    ])
  } else {
    await localDB.run(SQL_INSERT, [id, input.displayName, embeddingToBlob(input.embedding), 1, now])
  }
  return id
}

/**
 * 从音频样本创建/更新声纹。
 *
 * ⚠ 提取不到特征时**抛错**，不写库。
 *   旧实现直接把 `extractEmbedding` 的返回值落库，而它在失败时返回空数组
 *   ⇒ 一行「零声纹」被写进 `local_voiceprints`，列表里看着存在，
 *   但它永远匹配不上任何一段语音 —— 是一个用户看得见、用不了的条目。
 */
export async function enrollFromAudio(
  displayName: string,
  audioBlob: Blob,
  existingId?: string,
): Promise<string> {
  const result = await extractEmbeddingResult(audioBlob)
  if (!result.ok) throw new Error(`无法从这段音频提取声纹（${result.reason}）`)
  return saveVoiceprint({ id: existingId, displayName, embedding: result.embedding })
}

export async function deleteVoiceprint(id: string): Promise<void> {
  await localDB.run(SQL_DELETE_ONE, [id])
}

/** 清空整个声纹库。⚠ 不可撤销，调用方必须先让用户确认。 */
export async function clearVoiceprints(): Promise<void> {
  await localDB.run(SQL_DELETE_ALL)
}

/**
 * 只改显示名 —— 不碰 embedding，不碰 sample_count。
 *
 * ⚠ 不要用 saveVoiceprint 改名：它对已存在的行会
 * `sample_count = existing + 1`，那是「多了一个音频样本」的语义。
 * 改一次名字多一个样本，是把两个无关的量绑在一起。
 */
export async function renameVoiceprint(id: string, displayName: string, db: RecordingDb = captureRecordingDb()): Promise<void> {
  await db.run(SQL_RENAME, [displayName, id])
}

/**
 * 录音中给一位说话人起名时的落库入口：**不存在则插入（带 embedding），
 * 已存在则只改名**（不动样本数）。
 *
 * 这正是 labelSpeaker 需要的形状：首次标注要有 embedding 落盘，
 * 之后再改名不该被算成「又 enroll 了一次」。
 */
export async function upsertVoiceprintLabel(input: {
  id: string
  displayName: string
  embedding: Float32Array
}, db: RecordingDb = captureRecordingDb()): Promise<void> {
  const existing = await db.queryOne<any>(SQL_EXISTS, [input.id])
  if (planVoiceprintWrite(!!existing).op === 'rename') {
    await renameVoiceprint(input.id, input.displayName, db)
    return
  }
  await db.run(SQL_INSERT, [
    input.id, input.displayName, embeddingToBlob(input.embedding), 1, Date.now(),
  ])
}
