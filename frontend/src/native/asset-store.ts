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
import { portalNotesStore, canReadPortalNote, portalNoteReadScope } from './portal-sync/storage'
import { notePayload, type SyncDb } from './portal-sync/contract'
import { captureNotesEpoch } from './portal-sync/epochs'

import { encryptString, decryptString } from './crypto'
import { buildFtsQuery, likePattern, needsCjkLike } from './fts-query.ts'

type AssetDb = Pick<typeof localDB, 'run' | 'queryOne'>

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

/**
 * §884：目标行已被软删（墓碑）时抛出。
 *
 * 为什么要有这个错：本仓的「软删」是**单向不可逆**的 —— 写侧只有
 * `softDelete` / `softDeleteForWorkspace` 两处把 `deleted_at` 置上，
 * **全仓没有任何一处把它清回 NULL**（schema.ts 的文件头写着「保留可恢复」，
 * 那个「恢复」从未实现）。所以一次对墓碑行的 UPDATE 不是「编辑已删除的内容」，
 * 它是**把新内容写进一个谁都读不到、且永远不会被读到的行**。
 */
export class AssetDeletedError extends Error {
  readonly assetId: string
  constructor(assetId: string) {
    super(`Asset 已被软删，拒绝改写：${assetId}`)
    this.name = 'AssetDeletedError'
    this.assetId = assetId
  }
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
  source?: string
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
  async upsert(input: AssetInput, db: AssetDb = localDB): Promise<Asset> {
    if (db === localDB) db = localDB.bindOperation(localDB.captureOperation({ kind: 'local-edit' }))
    const ts = now()
    const id = input.id ?? genId('ast')
    const owner = 'owner' in db ? (db as SyncDb).owner : null
    const operationEpoch = captureNotesEpoch(owner)
    const wsId = input.workspaceId ?? owner?.workspaceId ?? 'default'
    if (owner && wsId !== owner.workspaceId) throw new Error('Asset workspace differs from the verified owner')
    const syncMode = input.syncMode ?? 'e2ee_local_first'
    const encrypt = input.encryptBody ?? false

    let bodyText = input.bodyText ?? ''
    let bodyEncrypted = 0
    if (encrypt && bodyText) {
      bodyText = await encryptString(bodyText)
      bodyEncrypted = 1
    }

    // 先查是否已存在（决定 INSERT 还是 UPDATE + client_rev 自增）
    // ⚠ §884：`deleted_at` 必须一起取出来 —— 下面的 UPDATE 语句里**没有**这一列，
    //   也就是说它既不会被清掉、也不会被看一眼就放行。修复前这里只取
    //   `client_rev, workspace_id`，于是一条已被软删的 asset 照样被静默改写。
    const existing = await db.queryOne<{ client_rev: number; workspace_id: string; deleted_at: number | null; source: string | null }>(
      'SELECT client_rev, workspace_id, deleted_at, source FROM local_assets WHERE id = ?',
      [id],
    )

    const write = async (statement: string, values: unknown[], created: boolean) => {
      if (input.kind !== 'note' || !('owner' in db) || !('runInTransaction' in db)) {
        await db.run(statement, values)
        return
      }
      let meta: Record<string, unknown> = {}
      try { meta = JSON.parse(input.metaJson || '{}') } catch { /* Invalid metadata carries no sync fields. */ }
      const importedMarkdown = (input.source ?? existing?.source) === 'enex_import'
      await portalNotesStore(db as SyncDb).commit([{ statement, values }], {
        source: 'asset', epoch: operationEpoch, id, title: input.title ?? '', modifiedAt: ts,
        syncError: importedMarkdown ? 'ENEX Markdown sync is not supported yet; the complete original remains on this device' : undefined,
        payload: notePayload({ format: importedMarkdown ? 'text' : 'html', content: input.bodyText ?? '', tags: meta.tags,
          links: meta.links, daily_date: meta.dailyDate, summary: meta.summary, created_at: ts }),
      }, created)
    }

    if (existing) {
      if (owner && existing.workspace_id !== owner.workspaceId) throw new Error('Asset workspace differs from the verified owner')
      if (input.workspaceId && existing.workspace_id !== input.workspaceId) {
        throw new Error('Asset 不属于当前 workspace')
      }
      // ⚠⚠ §884 D54：命中墓碑必须**显式拒绝**，不能静默改写。
      //   实际触发路径（每一步都有代码证据，见设计文档 §884）：
      //     enex 导入 ⇒ kind='note', source='enex_import'
      //       ⇒ 同一个 kind 也让这条笔记出现在 PKM 列表（listNotes 不按 source 过滤）
      //       ⇒ 在 /notes 侧删掉它（notes-persist.deleteNote 的 storage==='asset' 分支）
      //       ⇒ 浏览器后退 / 分享深链 /pkm/n/:id 重新打开（PkmEditor → pkm.getNote）
      //          ⚠ 那条读路径用的是**不过滤墓碑**的 assetStore.get
      //       ⇒ 编辑器里正文全文照常显示，改一个字 → 界面显示「已保存」
      //       ⇒ 但本 UPDATE 既不清 deleted_at、也不保留 source
      //          （pkm.saveNote 不传 source ⇒ 这里写成 NULL）
      //     ⇒ 用户保存成功，那条内容却**永远不再出现在任何列表/搜索里**，
      //        且因 source 被抹成 NULL，notes 侧 `getNote` 的
      //        `source === 'enex_import'` 回退条件也不再成立 ⇒ 它从 notes 侧一并消失。
      //   一句话：**「保存成功」与「被完成的事」不是同一件**（与 §879 D51 同一族）。
      if (existing.deleted_at !== null && existing.deleted_at !== undefined) {
        throw new AssetDeletedError(id)
      }
      await write(
        `UPDATE local_assets
         SET kind=?, title=?, body_text=?, body_encrypted=?, meta_json=?, source=?, sync_mode=?,
             client_rev=?, dirty=1, updated_at=?
         WHERE id=? AND workspace_id=?`,
        [input.kind, input.title ?? '', bodyText, bodyEncrypted, input.metaJson ?? '{}',
         input.source ?? existing.source ?? null, syncMode, existing.client_rev + 1, ts, id, existing.workspace_id], false,
      )
    } else {
      await write(
        `INSERT INTO local_assets
         (id, workspace_id, kind, title, body_text, body_encrypted, meta_json, source,
          sync_mode, client_rev, server_rev, dirty, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,1,0,1,?,?)`,
        [id, wsId, input.kind, input.title ?? '', bodyText, bodyEncrypted, input.metaJson ?? '{}',
         input.source ?? null, syncMode, ts, ts], true,
      )
    }

    return (await this.get(id, db))!
  }

  /** 读取单个 Asset（含解密 body，如已加密）。 */
  async get(id: string, db: AssetDb = localDB): Promise<Asset | null> {
    const row = await db.queryOne<RawAssetRow>(
      `SELECT id, workspace_id, kind, title, body_text, body_encrypted, meta_json, source,
              sync_mode, client_rev, server_rev, dirty, created_at, updated_at, deleted_at
       FROM local_assets WHERE id = ?`,
      [id],
    )
    return row && (row.kind !== 'note' || await canReadPortalNote('asset', row.id)) ? this.fromRow(row) : null
  }

  /** 在明确 workspace 内读取 Asset，避免同一 id 跨租户误读。 */
  async getForWorkspace(id: string, workspaceId: string, db: AssetDb = localDB): Promise<Asset | null> {
    const row = await db.queryOne<RawAssetRow>(
      `SELECT id, workspace_id, kind, title, body_text, body_encrypted, meta_json, source,
              sync_mode, client_rev, server_rev, dirty, created_at, updated_at, deleted_at
       FROM local_assets WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL`,
      [id, workspaceId],
    )
    return row && (row.kind !== 'note' || await canReadPortalNote('asset', row.id)) ? this.fromRow(row) : null
  }

  /** 软删除（标记 deleted_at + dirty，保留墓碑供同步）。 */
  async softDelete(id: string): Promise<void> {
    await localDB.run(
      `UPDATE local_assets SET deleted_at=?, dirty=1, client_rev=client_rev+1, updated_at=? WHERE id=?`,
      [now(), now(), id],
    )
  }

  async softDeleteForWorkspace(id: string, workspaceId: string, db: AssetDb = localDB): Promise<void> {
    if (db === localDB) db = localDB.bindOperation(localDB.captureOperation({ kind: 'local-edit' }))
    if ('owner' in db && (db as SyncDb).owner && (db as SyncDb).owner!.workspaceId !== workspaceId) throw new Error('Asset workspace differs from the verified owner')
    const operationEpoch = captureNotesEpoch('owner' in db ? (db as SyncDb).owner : null)
    const ts = now()
    const statement = `UPDATE local_assets SET deleted_at=?, dirty=1, client_rev=client_rev+1, updated_at=?
       WHERE id=? AND workspace_id=?`
    const values = [ts, ts, id, workspaceId]
    const asset = await db.queryOne<{ kind: string; title: string; source: string | null }>('SELECT kind,title,source FROM local_assets WHERE id=? AND workspace_id=?', [id, workspaceId])
    if (asset?.kind === 'note' && 'owner' in db && 'runInTransaction' in db) {
      await portalNotesStore(db as SyncDb).commit([{ statement, values }], {
        source: 'asset', epoch: operationEpoch, id, title: asset.title ?? '', modifiedAt: ts, deleted: true,
        syncError: asset.source === 'enex_import' ? 'ENEX Markdown sync is not supported yet; the original stays local' : undefined,
        payload: notePayload({ format: 'html', content: '' }),
      }, false)
    } else await db.run(statement, values)
  }

  /** 列出/搜索 Asset。支持 kind/source 过滤 + FTS 全文检索。 */
  async search(q: AssetSearchQuery = {}): Promise<Asset[]> {
    const scope = await portalNoteReadScope('asset', 'a')
    const wsId = q.workspaceId ?? 'default'
    const limit = Math.min(q.limit ?? 50, 200)
    const kind = kindFilter(q.kind)
    const source = q.source ? { sql: 'AND a.source = ?', args: [q.source] } : { sql: '', args: [] as unknown[] }

    // FTS 路径
    if (q.fts && q.fts.trim()) {
      const raw = q.fts
      // ⚠⚠ §886/D56：这里原本把 `q.fts` **原样**传给 `MATCH ?`。
      //   实测（真 SQLite + fts5，16 个探针）原样喂入的后果：
      //     手机号 `138-0013-8000` → `no such column: 0013`（`-` 是 NOT 前缀）
      //     `-quarterly`           → `no such column: quarterly`
      //     `database AND`         → `fts5: syntax error near ""`
      //     `"database`            → `unterminated string`
      //     `(a` / `*` / `---`     → syntax error
      //     中文「数据库」          → **0 行**（不报错）
      //   ⇒ 「语法安全」与「中文能搜到」是**两件不同的事**，两件都要处理。
      const ftsQuery = buildFtsQuery(raw)
      let ftsHits: Asset[] = []
      try {
        const ftsRows = await scope.db.query<RawAssetRow & { rank: number }>(
          `SELECT a.*, rank
           FROM local_assets_fts f
           JOIN local_assets a ON a.rowid = f.rowid
           WHERE local_assets_fts MATCH ?
             AND a.workspace_id = ? AND (a.kind <> 'note' OR ${scope.clause})
             ${q.includeDeleted ? '' : 'AND a.deleted_at IS NULL'}
             ${kind.sql}
             ${source.sql}
           ORDER BY rank
           LIMIT ?`,
          [ftsQuery, wsId, ...scope.values, ...kind.args, ...source.args, limit],
        )
        for (const r of ftsRows) if (r.kind !== 'note' || await canReadPortalNote('asset', r.id)) ftsHits.push(await this.fromRow(r))
      } catch {
        // ⚠ 构造器对纯符号输入可能产出空串（`MATCH ''` 必抛），Web 端也可能
        //   压根没有 FTS5 ⇒ 两种都落到降级，而不是把错误抛给用户。
        //   与 features/notes/notes-search.ts::searchFullText 的 try/catch 同一做法。
        ftsHits = []
      }
      // ⚠ 中文（或 FTS 一无所获）时并一层 LIKE：`local_assets_fts` 索引的是
      //   **原文** body_text，没有 local_notes 那张表的 `search_text`
      //   （§885.1 已核实：schema.ts:541 只有 title/body 两列，
      //   触发器写 `new.body_text`），而 unicode61 把连续汉字当成一个 token
      //   ⇒ 中文查询在 FTS 上**必然 0 行**且不报错。
      //   这不是推测：实测「数据库」「数据」「索引」全部 0 行。
      if (ftsHits.length > 0 && !needsCjkLike(raw)) return ftsHits
      const likeHits = await this.searchLike(raw, wsId, limit, q, kind, source, scope)
      const seen = new Set(ftsHits.map((a) => a.id))
      return [...ftsHits, ...likeHits.filter((a) => !seen.has(a.id))].slice(0, limit)
    }

    // 非 FTS 路径
    const rows = await scope.db.query<RawAssetRow>(
      `SELECT a.id, a.workspace_id, a.kind, a.title, a.body_text, a.body_encrypted, a.meta_json, a.source,
              a.sync_mode, a.client_rev, a.server_rev, a.dirty, a.created_at, a.updated_at, a.deleted_at
       FROM local_assets a
       WHERE a.workspace_id = ? AND (a.kind <> 'note' OR ${scope.clause})
         ${q.includeDeleted ? '' : 'AND a.deleted_at IS NULL'}
         ${kind.sql}
         ${source.sql}
       ORDER BY a.updated_at DESC
       LIMIT ?`,
      [wsId, ...scope.values, ...kind.args, ...source.args, limit],
    )
    const out: Asset[] = []
    for (const r of rows) if (r.kind !== 'note' || await canReadPortalNote('asset', r.id)) out.push(await this.fromRow(r))
    return out
  }

  /**
   * §886：LIKE 降级（中文在 FTS5 unicode61 下不可靠时的兜底）。
   *
   * 与 `features/notes/notes-search.ts::searchLike` 同一套参数口径
   * （`likePattern` 会**删除** `%` / `_`，不是转义 —— 那个取舍见
   * native/fts-query.ts 的注释与简报第 108 条）。
   */
  private async searchLike(
    raw: string,
    wsId: string,
    limit: number,
    q: AssetSearchQuery,
    kind: { sql: string; args: unknown[] },
    source: { sql: string; args: unknown[] },
    scope: Awaited<ReturnType<typeof portalNoteReadScope>>,
  ): Promise<Asset[]> {
    const pat = likePattern(raw)
    const rows = await scope.db.query<RawAssetRow>(
      `SELECT a.id, a.workspace_id, a.kind, a.title, a.body_text, a.body_encrypted, a.meta_json, a.source,
              a.sync_mode, a.client_rev, a.server_rev, a.dirty, a.created_at, a.updated_at, a.deleted_at
       FROM local_assets a
       WHERE a.workspace_id = ? AND (a.kind <> 'note' OR ${scope.clause})
         AND (a.title LIKE ? OR a.body_text LIKE ?)
         ${q.includeDeleted ? '' : 'AND a.deleted_at IS NULL'}
         ${kind.sql}
         ${source.sql}
       ORDER BY a.updated_at DESC
       LIMIT ?`,
      [wsId, ...scope.values, pat, pat, ...kind.args, ...source.args, limit],
    )
    const out: Asset[] = []
    for (const r of rows) if (r.kind !== 'note' || await canReadPortalNote('asset', r.id)) out.push(await this.fromRow(r))
    return out
  }

  /** 查找带指定 metadata 的 Asset，供导入幂等去重使用。 */
  async findByMeta(opts: {
    workspaceId: string
    kind: AssetKind
    source?: string
    key: string
    value: string
  }): Promise<Asset | null> {
    const scope = await portalNoteReadScope('asset', 'local_assets')
    const row = await scope.db.queryOne<RawAssetRow>(
      `SELECT id, workspace_id, kind, title, body_text, body_encrypted, meta_json, source,
              sync_mode, client_rev, server_rev, dirty, created_at, updated_at, deleted_at
       FROM local_assets
       WHERE workspace_id = ? AND kind = ?
         AND deleted_at IS NULL AND (kind <> 'note' OR ${scope.clause})
         AND json_extract(meta_json, ?) = ?
         ${opts.source ? 'AND source = ?' : ''}
       LIMIT 1`,
      opts.source
        ? [opts.workspaceId, opts.kind, ...scope.values, `$.${opts.key}`, opts.value, opts.source]
        : [opts.workspaceId, opts.kind, ...scope.values, `$.${opts.key}`, opts.value],
    )
    return row && (row.kind !== 'note' || await canReadPortalNote('asset', row.id)) ? this.fromRow(row) : null
  }

  /** 列出所有 dirty=1 的 Asset（同步引擎用）。 */
  async listDirty(limit = 100): Promise<Asset[]> {
    const scope = await portalNoteReadScope('asset', 'local_assets')
    const rows = await scope.db.query<RawAssetRow>(
      `SELECT id, workspace_id, kind, title, body_text, body_encrypted, meta_json, source,
              sync_mode, client_rev, server_rev, dirty, created_at, updated_at, deleted_at
       FROM local_assets WHERE dirty = 1 AND (kind <> 'note' OR ${scope.clause}) ORDER BY updated_at ASC LIMIT ?`,
      [...scope.values, limit],
    )
    const out: Asset[] = []
    for (const r of rows) if (r.kind !== 'note' || await canReadPortalNote('asset', r.id)) out.push(await this.fromRow(r))
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
    const idx = await nextBlobIdx(assetId)
    const cipher = await encryptString(plainContent)
    const sizeBytes = new Blob([plainContent]).size
    await localDB.run(
      `INSERT INTO local_asset_blobs (id, asset_id, idx, kind, cipher_text, size_bytes, hash, created_at)
       VALUES (?,?,?,?,?,?,?,?)`,
      [id, assetId, idx, opts.kind ?? null, cipher, sizeBytes, opts.hash ?? null, ts],
    )
    return { id, assetId, idx, kind: opts.kind, cipherText: cipher, sizeBytes, hash: opts.hash, createdAt: ts }
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
    const rows = await localDB.query<{
      id: string; asset_id: string; idx: number; kind: string | null; cipher_text: string | null;
      file_path: string | null; size_bytes: number; hash: string | null; created_at: number
    }>(
      `SELECT id, asset_id, idx, kind, cipher_text, file_path, size_bytes, hash, created_at
       FROM local_asset_blobs WHERE asset_id = ? ORDER BY idx`,
      [assetId],
    )
    return rows.map((row) => ({
      id: row.id,
      assetId: row.asset_id,
      idx: row.idx,
      kind: row.kind ?? undefined,
      cipherText: row.cipher_text ?? undefined,
      filePath: row.file_path ?? undefined,
      sizeBytes: row.size_bytes,
      hash: row.hash ?? undefined,
      createdAt: row.created_at,
    }))
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

function kindFilter(kind?: AssetKind | AssetKind[]): { sql: string; args: unknown[] } {
  if (!kind) return { sql: '', args: [] }
  const kinds = Array.isArray(kind) ? kind : [kind]
  if (kinds.length === 0) return { sql: '', args: [] }
  return {
    sql: `AND a.kind IN (${kinds.map(() => '?').join(',')})`,
    args: kinds,
  }
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
