/**
 * pkm-store.ts — S1.1 PKM 记事本数据层（基于 S0-C assetStore）
 *
 * 设计要点（spec §4.1 Roam 流派）：
 *   - 一篇笔记 = 一个 assetStore Asset（kind='note'），本地优先 e2ee_local_first
 *   - body_text 存 TipTap HTML（编辑器 getHTML()）。选 HTML 而非 JSON：
 *     FTS5 索引 HTML 时文本节点里的词仍命中，搜索可用；wikilink 体现为
 *     <a data-wikilink data-target="标题"> 标签，正则即可提取。
 *   - body 不加密（encryptBody=false）：PKM 核心是搜索 + 反向链接，
 *     加密 body 会让 FTS 触发器跳过（见 asset-store.ts:108 注释）。
 *     隐私敏感内容由用户自行判断是否放入 PKM；voice notes 走老 notes-store（加密）。
 *   - meta_json 存 { links: string[], dailyDate?: 'YYYY-MM-DD', tags?: string[] }
 *     links 在每次写入时由 extractLinks(html) 重建，供 backlink 反查。
 *
 * 反向链接查询用 SQLite json_each(meta_json,'$.links')，无需新表。
 *
 * 与老 notes-store 关系：完全独立。老 local_notes（语音笔记）保持不动。
 */
import { assetStore } from '../../native/asset-store'
import { localDB } from '../../native/local-db'
import { portalNoteReadScope } from '../../native/portal-sync/storage'

/** PKM 笔记的业务元数据（存进 Asset.meta_json）。 */
export interface PkmMeta {
  /** 本篇引用的其它笔记标题（由 body 里的 [[wikilink]] 提取）。 */
  links: string[]
  /** 若是 Daily Note，记日期 'YYYY-MM-DD'；普通笔记为空。 */
  dailyDate?: string
  tags?: string[]
}

/** PKM 笔记视图模型（解包 assetStore 的 Asset，业务层直接用）。 */
export interface PkmNote {
  id: string
  title: string
  html: string
  format: 'html' | 'markdown'
  links: string[]
  dailyDate?: string
  tags?: string[]
  createdAt: number
  updatedAt: number
}

/** Daily Note 的日期 key：本地时区 'YYYY-MM-DD'。 */
export function dailyKey(d = new Date()): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

/** Daily Note 的可读标题：'2026-07-13' 或 'July 13, 2026'。 */
export function dailyTitle(key: string): string {
  return key // 用 ISO 形式当标题，wikilink 直接 [[2026-07-13]]
}

const NOTE_KIND = 'note'

// ---- 内部：Asset <-> PkmNote 转换 ----

function toNote(
  id: string,
  title: string,
  bodyText: string,
  metaJson: string,
  createdAt: number,
  updatedAt: number,
  source?: string,
): PkmNote {
  let meta: PkmMeta = { links: [] }
  try {
    meta = JSON.parse(metaJson) as PkmMeta
  } catch {
    // 老数据或非法 meta，降级为空 links
  }
  return {
    id,
    title,
    html: bodyText,
    format: source === 'enex_import' ? 'markdown' : 'html',
    links: meta.links ?? [],
    dailyDate: meta.dailyDate,
    tags: meta.tags,
    createdAt,
    updatedAt,
  }
}

// ---- CRUD ----

/** 创建或更新一篇笔记。带 id 时更新。返回最终 PkmNote。 */
export async function saveNote(input: {
  id?: string
  title: string
  html: string
  dailyDate?: string
  tags?: string[]
  workspaceId?: string
}): Promise<PkmNote> {
  const links = extractLinks(input.html)
  const meta: PkmMeta = {
    links,
    dailyDate: input.dailyDate,
    tags: input.tags,
  }
  const asset = await assetStore.upsert({
    id: input.id,
    kind: NOTE_KIND,
    title: input.title,
    bodyText: input.html,
    // encryptBody=false：FTS 需要明文 body（见文件头注释）
    encryptBody: false,
    metaJson: JSON.stringify(meta),
    workspaceId: input.workspaceId,
    syncMode: 'e2ee_local_first',
  })
  return toNote(
    asset.id, asset.title, asset.bodyText, asset.metaJson,
    asset.createdAt, asset.updatedAt, asset.source,
  )
}

/**
 * 读单条 PKM 笔记。
 *
 * ⚠⚠ §884 D54：这里原本是 `assetStore.get(id)` —— 它**既不过滤 `deleted_at`、
 * 也不校验 `workspace_id`**（全仓唯一一处这样的读）。
 *   ⇒ 同一张 `local_assets` 上，一条被 `/notes` 侧删掉的 enex 笔记
 *      （kind 也是 'note'，见 import-service.ts:38）仍能被 `/pkm/n/:id` 深链
 *      打开全文并编辑；改完 `upsert` 报「已保存」，而那条内容永远不再出现在
 *      任何列表或搜索里。详见 asset-store.ts::upsert 里的注释。
 *
 * 现在与 notes 侧完全对齐：`notes-persist.getNote` 走 `assetStore.getForWorkspace`
 * （`AND workspace_id = ? AND deleted_at IS NULL`），PKM 读单条也走它。
 * ⚠ `workspaceId` 必须是**调用方的 auth.workspaceId**，不是默认的 'default'：
 *   保存（saveNote）与列表（listNotes）用的都是它，只读走 'default' 会让
 *   真机上 `ws_user-admin` 分区的笔记全部读不到 —— 那正是 PkmNoteView.vue:46
 *   注释记的「保存成功但列表看不到」的同族坑。
 */
export async function getNote(id: string, workspaceId = 'default'): Promise<PkmNote | null> {
  const a = await assetStore.getForWorkspace(id, workspaceId)
  if (!a) return null
  return toNote(a.id, a.title, a.bodyText, a.metaJson, a.createdAt, a.updatedAt, a.source)
}

/**
 * 软删除（保留墓碑供多设备同步）。
 *
 * 走 softDeleteForWorkspace 而不是 softDelete：后者只按 id 匹配，跨 workspace
 * 也能删中；前者带 workspace_id 条件，传错时只会删 0 行（失败安全）。
 */
export async function deleteNote(id: string, workspaceId = 'default'): Promise<void> {
  await assetStore.softDeleteForWorkspace(id, workspaceId)
}

// ---- 查询 ----

/** 列出全部笔记（最新优先）。 */
export async function listNotes(
  opts: { workspaceId?: string; limit?: number } = {},
): Promise<PkmNote[]> {
  const assets = await assetStore.search({
    kind: NOTE_KIND,
    workspaceId: opts.workspaceId,
    limit: opts.limit ?? 200,
  })
  return assets.map((a) =>
    toNote(a.id, a.title, a.bodyText, a.metaJson, a.createdAt, a.updatedAt, a.source),
  )
}

/** 按标题精确查找（wikilink 跳转目标）。返回第一条匹配。 */
export async function findByTitle(
  title: string,
  workspaceId = 'default',
): Promise<PkmNote | null> {
  const scope = await portalNoteReadScope('asset', 'local_assets')
  const rows = await scope.db.query<{ id: string; body_text: string; meta_json: string; created_at: number; updated_at: number }>(
    `SELECT id, body_text, meta_json, created_at, updated_at
     FROM local_assets
     WHERE workspace_id = ? AND kind = ? AND title = ? AND deleted_at IS NULL AND ${scope.clause}
     LIMIT 1`,
    [workspaceId, NOTE_KIND, title, ...scope.values],
  )
  if (rows.length === 0) return null
  const r = rows[0]
  return getNote(r.id, workspaceId)
}

/** 全文搜索（复用 assetStore FTS）。 */
export async function searchNotes(
  query: string,
  opts: { workspaceId?: string; limit?: number } = {},
): Promise<PkmNote[]> {
  const assets = await assetStore.search({
    kind: NOTE_KIND,
    fts: query,
    workspaceId: opts.workspaceId,
    limit: opts.limit ?? 50,
  })
  return assets.map((a) =>
    toNote(a.id, a.title, a.bodyText, a.metaJson, a.createdAt, a.updatedAt, a.source),
  )
}

// ---- 反向链接 ----

/**
 * 找出引用了 targetTitle 的所有笔记。
 * 用 SQLite json_each 扫描 meta_json 的 links 数组，无需新表。
 */
export async function getBacklinks(
  targetTitle: string,
  workspaceId = 'default',
): Promise<PkmNote[]> {
  const scope = await portalNoteReadScope('asset', 'a')
  const rows = await scope.db.query<{ id: string; title: string; body_text: string; meta_json: string; created_at: number; updated_at: number }>(
    `SELECT id, title, body_text, meta_json, created_at, updated_at
     FROM local_assets a
     WHERE a.workspace_id = ? AND a.kind = ? AND a.deleted_at IS NULL AND ${scope.clause}
       AND EXISTS (
         SELECT 1 FROM json_each(a.meta_json, '$.links')
         WHERE json_each.value = ?
       )
     ORDER BY a.updated_at DESC`,
    [workspaceId, NOTE_KIND, ...scope.values, targetTitle],
  )
  const notes = await Promise.all(rows.map(r => getNote(r.id, workspaceId)))
  return notes.filter((note): note is PkmNote => note !== null)
}

// ---- Daily Note ----

/** 获取某天的 Daily Note（不存在返回 null）。 */
export async function getDailyNote(
  dateKey = dailyKey(),
  workspaceId = 'default',
): Promise<PkmNote | null> {
  const scope = await portalNoteReadScope('asset', 'local_assets')
  const rows = await scope.db.query<{ id: string; title: string; body_text: string; meta_json: string; created_at: number; updated_at: number }>(
    `SELECT id, title, body_text, meta_json, created_at, updated_at
     FROM local_assets
     WHERE workspace_id = ? AND kind = ? AND deleted_at IS NULL AND ${scope.clause}
       AND json_extract(meta_json, '$.dailyDate') = ?
     LIMIT 1`,
    [workspaceId, NOTE_KIND, ...scope.values, dateKey],
  )
  if (rows.length === 0) return null
  const r = rows[0]
  return getNote(r.id, workspaceId)
}

/**
 * 获取或创建今天的 Daily Note。
 * 首次创建时用空 body + 标题 = 日期 key（如 '2026-07-13'），
 * 便于用户直接 [[2026-07-13]] 引用。
 */
export async function getOrCreateDailyNote(
  dateKey = dailyKey(),
  workspaceId = 'default',
): Promise<PkmNote> {
  const existing = await getDailyNote(dateKey, workspaceId)
  if (existing) return existing
  return saveNote({
    title: dailyTitle(dateKey),
    html: '',
    dailyDate: dateKey,
    workspaceId,
  })
}

// ---- 内部工具 ----

/**
 * 从 TipTap HTML 提取 wikilink 目标标题。
 * wikilink 节点渲染为 <a data-wikilink data-target="标题">。
 * 去重保序返回。
 */
export function extractLinks(html: string): string[] {
  if (!html) return []
  const re = /data-target="([^"]+)"/g
  const seen = new Set<string>()
  const out: string[] = []
  let m: RegExpExecArray | null
  while ((m = re.exec(html)) !== null) {
    const t = m[1]
    if (t && !seen.has(t)) {
      seen.add(t)
      out.push(t)
    }
  }
  return out
}
