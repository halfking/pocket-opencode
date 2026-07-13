/**
 * asset-store.ts — 🦞 S0-C Lobster Vault: 统一 Asset 存储抽象
 *
 * 这是 S1/S2/S3 新业务的统一数据入口。一个 Asset = (元数据 + blob + 向量)，
 * 可以是笔记、会议录音、凭证图片、PDF、附件等任何"数字资产"。
 *
 * 设计原则（spec §3.2 决策 3、4）：
 *   - 三种 sync_mode：e2ee_local_first（默认，离线权威）/ cloud_authoritative /
 *     cloud_readonly。本文件只管 local 读写；云端同步走 syncAssets()。
 *   - 加密边界：title 留明文（FTS 要用）；body_text 可加密（body_encrypted=1）；
 *     blob 一律 AES-GCM 加密。
 *   - 修订号：client_rev 客户端自增，server_rev 服务端已知。dirty=1 表示
 *     本地有改动待推送给云端同步引擎。
 *
 * 与老表关系：local_notes/local_emails/local_vault_entries/local_meetings
 * 保持不动。新业务一律走 Asset。后续 sprint 可写适配器把老表迁过来。
 */
import { localDB } from './local-db'
import { encryptString, decryptString } from './crypto'

/** Asset 的业务类型。开放枚举——调用方可自由扩展。 */
export type AssetKind =
  | 'note'
  | 'meeting_audio'
  | 'meeting_transcript'
  | 'voucher_image'
  | 'pdf'
  | 'voice_memo'
  | 'screenshot'
  | 'mixed'
  | string // 允许自定义 kind

/** 同步模式，决定数据权威源。 */
export type SyncMode = 'e2ee_local_first' | 'cloud_authoritative' | 'cloud_readonly'

/** Asset 主记录。 */
export interface Asset {
  id: string
  workspaceId: string
  kind: AssetKind
  title: string
  bodyText: string
  bodyEncrypted: boolean
  metaJson: string // 调用方负责序列化业务元数据
  source?: string
  syncMode: SyncMode
  clientRev: number
  serverRev: number
  dirty: boolean
  createdAt: number
  updatedAt: number
  deletedAt?: number | null
}

/** Asset 关联的 blob（加密大文件）。 */
export interface AssetBlob {
  id: string
  assetId: string
  idx: number
  kind?: string
  cipherText?: string // 小文件直存
  filePath?: string // 大文件外部路径
  sizeBytes: number
  hash?: string
  createdAt: number
}

/** 创建/更新 Asset 的入参。 */
export interface AssetInput {
  id?: string // 省略则自动生成
  workspaceId?: string // 省略则 'default'
  kind: AssetKind
  title?: string
  bodyText?: string
  encryptBody?: boolean // 默认 false。true 时 bodyText 会被 AES-GCM 加密后存库
  metaJson?: string // 默认 '{}'
  source?: string
  syncMode?: SyncMode // 默认 'e2ee_local_first'
}

/** 搜索参数。 */
export interface AssetSearchQuery {
  workspaceId?: string
  kind?: AssetKind | AssetKind[]
  fts?: string // 全文检索关键词
  limit?: number
  includeDeleted?: boolean
}

const now = () => Date.now()

function genId(prefix: string): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

/**
 * AssetStore 是 S0-C 的前端单例。所有新业务通过它读写 Asset。
 *
 * 注意：本类不做云端同步——同步由 syncAssets() 在 App 启动/空闲时触发，
 * 调后端 /api/assets/sync。本类只标记 dirty=1。
 */
class AssetStore {
  /**
   * 创建或更新一个 Asset。带 id 时更新（client_rev +1, dirty=1）；
   * 无 id 时创建。
   *
   * encryptBody=true 时，bodyText 会被 encryptString 加密后存库，
   * body_encrypted=1，FTS 触发器自动跳过加密 body（见 schema.ts 注释）。
   */
  async upsert(input: AssetInput): Promise<Asset> {
    const ts = now()
    const id = input.id ?? genId('ast')
    const wsId = input.workspaceId ?? 'default'
    const syncMode = input.syncMode ?? 'e2ee_local_first'
    const encrypt = input.encryptBody ?? false

    let bodyText = input.bodyText ?? ''
    let bodyEncrypted = 0
    if (encrypt && bodyText) {
      bodyText = await encryptString(bodyText)
      bodyEncrypted = 1
    }

    // 先查是否已存在（决定 INSERT 还是 UPDATE + client_rev 自增）
    const existing = await localDB.queryOne<{ client_rev: number }>(
      'SELECT client_rev FROM local_assets WHERE id = ?',
      [id],
    )

    if (existing) {
      await localDB.run(
        `UPDATE local_assets
         SET kind=?, title=?, body_text=?, body_encrypted=?, meta_json=?, source=?, sync_mode=?,
             client_rev=?, dirty=1, updated_at=?
         WHERE id=?`,
        [input.kind, input.title ?? '', bodyText, bodyEncrypted, input.metaJson ?? '{}',
         input.source ?? null, syncMode, existing.client_rev + 1, ts, id],
      )
    } else {
      await localDB.run(
        `INSERT INTO local_assets
         (id, workspace_id, kind, title, body_text, body_encrypted, meta_json, source,
          sync_mode, client_rev, server_rev, dirty, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,1,0,1,?,?)`,
        [id, wsId, input.kind, input.title ?? '', bodyText, bodyEncrypted, input.metaJson ?? '{}',
         input.source ?? null, syncMode, ts, ts],
      )
    }

    return (await this.get(id))!
  }

  /** 读取单个 Asset（含解密 body，如已加密）。 */
  async get(id: string): Promise<Asset | null> {
    const row = await localDB.queryOne<RawAssetRow>(
      `SELECT id, workspace_id, kind, title, body_text, body_encrypted, meta_json, source,
              sync_mode, client_rev, server_rev, dirty, created_at, updated_at, deleted_at
       FROM local_assets WHERE id = ?`,
      [id],
    )
    if (!row) return null
    return await this.fromRow(row)
  }

  /** 软删除（标记 deleted_at + dirty，保留墓碑供同步）。 */
  async softDelete(id: string): Promise<void> {
    await localDB.run(
      `UPDATE local_assets SET deleted_at=?, dirty=1, client_rev=client_rev+1, updated_at=? WHERE id=?`,
      [now(), now(), id],
    )
  }

  /** 列出/搜索 Asset。支持 kind 过滤 + FTS 全文检索。 */
  async search(q: AssetSearchQuery = {}): Promise<Asset[]> {
    const wsId = q.workspaceId ?? 'default'
    const limit = Math.min(q.limit ?? 50, 200)

    // FTS 路径
    if (q.fts && q.fts.trim()) {
      const ftsRows = await localDB.query<RawAssetRow & { rank: number }>(
        `SELECT a.*, rank
         FROM local_assets_fts f
         JOIN local_assets a ON a.rowid = f.rowid
         WHERE local_assets_fts MATCH ?
           AND a.workspace_id = ?
           ${q.includeDeleted ? '' : 'AND a.deleted_at IS NULL'}
           ${kindFilterClause(q.kind)}
         ORDER BY rank
         LIMIT ?`,
        [q.fts, wsId, limit],
      )
      const out: Asset[] = []
      for (const r of ftsRows) out.push(await this.fromRow(r))
      return out
    }

    // 非 FTS 路径
    const rows = await localDB.query<RawAssetRow>(
      `SELECT id, workspace_id, kind, title, body_text, body_encrypted, meta_json, source,
              sync_mode, client_rev, server_rev, dirty, created_at, updated_at, deleted_at
       FROM local_assets
       WHERE workspace_id = ?
         ${q.includeDeleted ? '' : 'AND deleted_at IS NULL'}
         ${kindFilterClause(q.kind)}
       ORDER BY updated_at DESC
       LIMIT ?`,
      [wsId, limit],
    )
    const out: Asset[] = []
    for (const r of rows) out.push(await this.fromRow(r))
    return out
  }

  /** 列出所有 dirty=1 的 Asset（同步引擎用）。 */
  async listDirty(limit = 100): Promise<Asset[]> {
    const rows = await localDB.query<RawAssetRow>(
      `SELECT id, workspace_id, kind, title, body_text, body_encrypted, meta_json, source,
              sync_mode, client_rev, server_rev, dirty, created_at, updated_at, deleted_at
       FROM local_assets WHERE dirty = 1 ORDER BY updated_at ASC LIMIT ?`,
      [limit],
    )
    const out: Asset[] = []
    for (const r of rows) out.push(await this.fromRow(r))
    return out
  }

  /** 标记 Asset 已成功同步（server_rev 更新，dirty 清除）。 */
  async markSynced(id: string, serverRev: number): Promise<void> {
    await localDB.run(
      `UPDATE local_assets SET server_rev=?, dirty=0 WHERE id=?`,
      [serverRev, id],
    )
  }

  // ---- Blob 管理 ----

  /** 给一个 Asset 追加加密 blob（小文件直存）。 */
  async addBlob(assetId: string, plainContent: string, opts: { kind?: string; hash?: string } = {}): Promise<AssetBlob> {
    const id = genId('blb')
    const ts = now()
    const cipher = await encryptString(plainContent)
    const sizeBytes = new Blob([plainContent]).size
    await localDB.run(
      `INSERT INTO local_asset_blobs (id, asset_id, idx, kind, cipher_text, size_bytes, hash, created_at)
       VALUES (?,?,?,?,?,?,?,?)`,
      [id, assetId, await nextBlobIdx(assetId), opts.kind ?? null, cipher, sizeBytes, opts.hash ?? null, ts],
    )
    return { id, assetId, idx: 0, kind: opts.kind, cipherText: cipher, sizeBytes, hash: opts.hash, createdAt: ts }
  }

  /** 读取并解密一个 blob。 */
  async getBlob(id: string): Promise<string | null> {
    const row = await localDB.queryOne<{ cipher_text: string | null; file_path: string | null }>(
      'SELECT cipher_text, file_path FROM local_asset_blobs WHERE id = ?',
      [id],
    )
    if (!row) return null
    if (row.cipher_text) return await decryptString(row.cipher_text)
    // 大文件路径暂不实现（后续 sprint 加文件系统读写）
    return null
  }

  /** 列出某 Asset 的所有 blob（不解密）。 */
  async listBlobs(assetId: string): Promise<AssetBlob[]> {
    return await localDB.query<AssetBlob>(
      `SELECT id, asset_id, idx, kind, cipher_text, file_path, size_bytes, hash, created_at
       FROM local_asset_blobs WHERE asset_id = ? ORDER BY idx`,
      [assetId],
    )
  }

  // ---- 向量管理（语义检索）----

  /** 给 Asset 追加一个 embedding（调用方先算好向量）。 */
  async addVector(assetId: string, embedding: Float32Array, model: string, chunkIdx = 0): Promise<void> {
    const id = genId('vec')
    const blob = new Uint8Array(embedding.buffer, embedding.byteOffset, embedding.byteLength)
    await localDB.run(
      `INSERT INTO local_asset_vectors (id, asset_id, embedding, dim, model, chunk_idx, created_at)
       VALUES (?,?,?,?,?,?,?)`,
      [id, assetId, blob.buffer, embedding.length, model, chunkIdx, now()],
    )
  }

  /** 删除某 Asset 的所有向量（重建索引前用）。 */
  async clearVectors(assetId: string): Promise<void> {
    await localDB.run('DELETE FROM local_asset_vectors WHERE asset_id = ?', [assetId])
  }

  // ---- 内部辅助 ----

  private async fromRow(r: RawAssetRow): Promise<Asset> {
    let bodyText = r.body_text ?? ''
    if (r.body_encrypted === 1 && bodyText) {
      try {
        bodyText = await decryptString(bodyText)
      } catch {
        bodyText = '[加密内容无法解密]'
      }
    }
    return {
      id: r.id,
      workspaceId: r.workspace_id,
      kind: r.kind,
      title: r.title ?? '',
      bodyText,
      bodyEncrypted: r.body_encrypted === 1,
      metaJson: r.meta_json ?? '{}',
      source: r.source ?? undefined,
      syncMode: r.sync_mode as SyncMode,
      clientRev: r.client_rev,
      serverRev: r.server_rev,
      dirty: r.dirty === 1,
      createdAt: r.created_at,
      updatedAt: r.updated_at,
      deletedAt: r.deleted_at,
    }
  }
}

interface RawAssetRow {
  id: string
  workspace_id: string
  kind: string
  title: string | null
  body_text: string | null
  body_encrypted: number
  meta_json: string | null
  source: string | null
  sync_mode: string
  client_rev: number
  server_rev: number
  dirty: number
  created_at: number
  updated_at: number
  deleted_at: number | null
}

function kindFilterClause(kind?: AssetKind | AssetKind[]): string {
  if (!kind) return ''
  const kinds = Array.isArray(kind) ? kind : [kind]
  if (kinds.length === 0) return ''
  const placeholders = kinds.map(() => '?').join(',')
  return `AND a.kind IN (${placeholders})`
}

async function nextBlobIdx(assetId: string): Promise<number> {
  const row = await localDB.queryOne<{ max_idx: number | null }>(
    'SELECT MAX(idx) AS max_idx FROM local_asset_blobs WHERE asset_id = ?',
    [assetId],
  )
  return (row?.max_idx ?? -1) + 1
}

/** Asset 单例。全 App 共享。 */
export const assetStore = new AssetStore()
