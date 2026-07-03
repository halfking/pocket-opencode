/**
 * vault-store.ts — 🦞 龙虾钳子：密码箱本地存储 + 云同步载体
 *
 * 架构分层：
 *   cap-keystore（原生）→ AndroidKeyStore AES-GCM 加解密，生物识别解锁
 *   vault-store（本文件）→ 把密文存 local_vault_entries，提供云同步导出
 *   /api/vault/sync（pocketd）→ 只存整库密文 blob，零知识
 *
 * 当 cap-keystore 不可用（Web/dev）时，用 Web Crypto API + 用户主密码
 * 做 AES-GCM 加密的降级方案，保证功能可用。
 *
 * 隐私铁律：密码明文永不离设备。云同步只传 entry_ciphertext。
 */
import { localDB } from '../../native/local-db'
import { encryptString, decryptString } from '../../native/crypto'
import { vectorIndex } from '../../native/vector'

export interface VaultEntryMeta {
  id: string
  title: string
  category: string | null
  icon: string | null
  username: string | null
  url: string | null
  updatedAt: number
}

export interface VaultEntry extends VaultEntryMeta {
  /** 解密后的明文内容（JSON: {password, notes, totpSecret, customFields}） */
  data: VaultEntryData
  createdAt: number
}

export interface VaultEntryData {
  password?: string
  notes?: string
  totpSecret?: string
  customFields?: { key: string; value: string }[]
}

// ---- 加解密（委托共享 crypto 模块，cap-keystore 不可用时的降级）----

async function encryptData(data: VaultEntryData): Promise<string> {
  return encryptString(JSON.stringify(data))
}

async function decryptData(ciphertext: string): Promise<VaultEntryData> {
  return JSON.parse(await decryptString(ciphertext))
}

// ---- CRUD ----

export async function listEntries(): Promise<VaultEntryMeta[]> {
  const rows = await localDB.query<{
    id: string; title: string; category: string | null; icon: string | null;
    username: string | null; url: string | null; updated_at: number
  }>(`SELECT id, title, category, icon, username, url, updated_at FROM local_vault_entries ORDER BY updated_at DESC`)
  return rows.map((r) => ({
    id: r.id, title: r.title, category: r.category, icon: r.icon,
    username: r.username, url: r.url, updatedAt: r.updated_at,
  }))
}

export async function getEntry(id: string): Promise<VaultEntry | null> {
  const row = await localDB.queryOne<{
    id: string; title: string; category: string | null; icon: string | null;
    username: string | null; url: string | null;
    entry_ciphertext: string; created_at: number; updated_at: number
  }>('SELECT * FROM local_vault_entries WHERE id = ?', [id])
  if (!row) return null

  const data = await decryptData(row.entry_ciphertext)
  return {
    id: row.id, title: row.title, category: row.category, icon: row.icon,
    username: row.username, url: row.url, data, createdAt: row.created_at, updatedAt: row.updated_at,
  }
}

export async function saveEntry(input: Partial<VaultEntry> & { title: string }): Promise<string> {
  const now = Date.now()
  const id = input.id || `vault-${now}-${Math.random().toString(36).slice(2, 8)}`
  const data: VaultEntryData = input.data ?? {}
  const ciphertext = await encryptData(data)

  await localDB.run(
    `INSERT INTO local_vault_entries (id, title, username, url, entry_ciphertext, iv, category, icon, created_at, updated_at)
     VALUES (?,?,?,?,?,?,?,?,?,?)
     ON CONFLICT(id) DO UPDATE SET
       title = EXCLUDED.title, username = EXCLUDED.username, url = EXCLUDED.url,
       entry_ciphertext = EXCLUDED.entry_ciphertext, iv = EXCLUDED.iv,
       category = EXCLUDED.category, icon = EXCLUDED.icon, updated_at = EXCLUDED.updated_at`,
    [id, input.title, input.username ?? null, input.url ?? null, ciphertext, '',
     input.category ?? null, input.icon ?? null, now, now],
  )
  return id
}

export async function deleteEntry(id: string): Promise<void> {
  await localDB.run('DELETE FROM local_vault_entries WHERE id = ?', [id])
}

/**
 * 局部更新条目（编辑模式依赖）。仅 UPDATE，不 INSERT；条目不存在则 no-op。
 * 若 patch.data 包含新的明文字段（password/notes/totpSecret/customFields），
 * 会重新加密整个 data blob 并写回 entry_ciphertext。
 */
export async function updateEntry(id: string, patch: Partial<VaultEntry>): Promise<void> {
  const sets: string[] = []
  const vals: unknown[] = []
  if (patch.title !== undefined) { sets.push('title = ?'); vals.push(patch.title) }
  if (patch.username !== undefined) { sets.push('username = ?'); vals.push(patch.username) }
  if (patch.url !== undefined) { sets.push('url = ?'); vals.push(patch.url) }
  if (patch.category !== undefined) { sets.push('category = ?'); vals.push(patch.category) }
  if (patch.icon !== undefined) { sets.push('icon = ?'); vals.push(patch.icon) }
  if (patch.data !== undefined) {
    const ciphertext = await encryptData(patch.data)
    sets.push('entry_ciphertext = ?')
    vals.push(ciphertext)
  }
  if (sets.length === 0) return
  sets.push('updated_at = ?')
  vals.push(Date.now())
  vals.push(id)
  await localDB.run(`UPDATE local_vault_entries SET ${sets.join(', ')} WHERE id = ?`, vals)
}

export async function touchLastUsed(id: string): Promise<void> {
  await localDB.run('UPDATE local_vault_entries SET last_used_at = ? WHERE id = ?', [Date.now(), id])
}

// ---- 云同步导出/导入 ----

/**
 * 把整个 vault 导出为加密 blob，用于 /api/vault/sync 上传。
 * 返回的是已加密的密文，服务端无法解密。
 */
export async function exportEncryptedBlob(): Promise<{ blob: string; version: number }> {
  const rows = await localDB.query('SELECT * FROM local_vault_entries')
  // entry_ciphertext 已是密文，这里整库再包一层加密
  const json = JSON.stringify(rows)
  const blob = await encryptString(json)
  const version = Math.floor(Date.now() / 1000)
  return { blob, version }
}

/**
 * 从云端下载的加密 blob 导入到本地库（解密 → 替换本地数据）。
 *
 * 安全策略：先解析+校验全部行，**全部成功后才**清空本地并写入。
 * 避免解密/解析失败时已清空本地导致数据丢失。
 * @param blob 密文 blob（从 pocketd GET /api/vault/sync/latest 获得）
 */
export async function importEncryptedBlob(blob: string): Promise<number> {
  // 1. 解密整库（若 blob 损坏/篡改此处会抛错，本地数据未动）
  const json = await decryptString(blob)
  const rows: any[] = JSON.parse(json)

  // 2. 校验每行必有字段（任何一行缺字段 → 抛错，本地数据未动）
  //    同时验证每条 entry 的密文可被当前主密码解密（第七轮审计 MEDIUM 修复）。
  //    失败则提示用户密码错误或数据损坏，避免导入后才报错。
  const validated: Array<{
    id: string; title: string; username: string | null; url: string | null;
    entry_ciphertext: string; iv: string; category: string | null;
    icon: string | null; created_at: number; updated_at: number
  }> = []
  for (const r of rows) {
    if (!r.id || !r.entry_ciphertext) {
      throw new Error(`importEncryptedBlob: row missing id or entry_ciphertext: ${JSON.stringify(r).slice(0, 100)}`)
    }
    
    // 验证密文可解密（防损坏/篡改的密文进入本地库）
    try {
      await decryptData(r.entry_ciphertext)
    } catch (e) {
      throw new Error(
        `importEncryptedBlob: row ${r.id} contains undecryptable ciphertext. ` +
        `可能原因：1) 主密码错误  2) 数据损坏或被篡改. ` +
        `Original: ${(e as Error).message}`
      )
    }
    
    validated.push({
      id: r.id, title: r.title ?? '', username: r.username ?? null, url: r.url ?? null,
      entry_ciphertext: r.entry_ciphertext, iv: r.iv ?? '',
      category: r.category ?? null, icon: r.icon ?? null,
      created_at: r.created_at ?? Date.now(), updated_at: r.updated_at ?? Date.now(),
    })
  }

  // 3. 全部校验通过后，才清空本地并写入。
  //    DELETE + 所有 INSERT 在同一事务中执行（runInTransaction），
  //    任意一条失败则整批回滚，避免解密成功→DELETE 后→INSERT 失败导致数据丢失。
  const statements: { statement: string; values: unknown[] }[] = [
    { statement: 'DELETE FROM local_vault_entries', values: [] },
  ]
  for (const v of validated) {
    statements.push({
      statement:
        `INSERT INTO local_vault_entries (id, title, username, url, entry_ciphertext, iv, category, icon, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?)`,
      values: [v.id, v.title, v.username, v.url, v.entry_ciphertext, v.iv, v.category, v.icon, v.created_at, v.updated_at],
    })
  }
  await localDB.runInTransaction(statements)
  return validated.length
}

/** 获取本地 vault 条目数（用于同步前检查） */
export async function countEntries(): Promise<number> {
  const row = await localDB.queryOne<{ cnt: number }>('SELECT COUNT(*) as cnt FROM local_vault_entries')
  return row?.cnt ?? 0
}

// ---- 本地缓存统计（用于"本地存储使用情况"页）----

/**
 * 返回本地龙虾钳子缓存的统计信息（同步、非阻塞）。
 * - vectorCount: 当前 vectorIndex 中已索引的笔记向量数
 * - ftsEnabled: schema 总是创建 FTS5 虚表（local_notes_fts），固定 true
 */
export function getCacheStats(): { vectorCount: number; ftsEnabled: boolean } {
  return {
    vectorCount: vectorIndex.size(),
    ftsEnabled: true,
  }
}
