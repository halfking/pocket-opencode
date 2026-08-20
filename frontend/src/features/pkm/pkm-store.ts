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
    asset.createdAt, asset.updatedAt,
  )
}

export async function getNote(id: string): Promise<PkmNote | null> {
  const a = await assetStore.get(id)
  if (!a) return null
  return toNote(a.id, a.title, a.bodyText, a.metaJson, a.createdAt, a.updatedAt)
}

/** 软删除（保留墓碑供多设备同步）。 */
export async function deleteNote(id: string): Promise<void> {
  await assetStore.softDelete(id)
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
    toNote(a.id, a.title, a.bodyText, a.metaJson, a.createdAt, a.updatedAt),
  )
}

/** 按标题精确查找（wikilink 跳转目标）。返回第一条匹配。 */
export async function findByTitle(
  title: string,
  workspaceId = 'default',
): Promise<PkmNote | null> {
  const rows = await localDB.query<{ id: string; body_text: string; meta_json: string; created_at: number; updated_at: number }>(
    `SELECT id, body_text, meta_json, created_at, updated_at
     FROM local_assets
     WHERE workspace_id = ? AND kind = ? AND title = ? AND deleted_at IS NULL
     LIMIT 1`,
    [workspaceId, NOTE_KIND, title],
  )
  if (rows.length === 0) return null
  const r = rows[0]
  return toNote(r.id, title, r.body_text, r.meta_json, r.created_at, r.updated_at)
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
    toNote(a.id, a.title, a.bodyText, a.metaJson, a.createdAt, a.updatedAt),
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
  const rows = await localDB.query<{ id: string; title: string; body_text: string; meta_json: string; created_at: number; updated_at: number }>(
    `SELECT id, title, body_text, meta_json, created_at, updated_at
     FROM local_assets a
     WHERE a.workspace_id = ? AND a.kind = ? AND a.deleted_at IS NULL
       AND EXISTS (
         SELECT 1 FROM json_each(a.meta_json, '$.links')
         WHERE json_each.value = ?
       )
     ORDER BY a.updated_at DESC`,
    [workspaceId, NOTE_KIND, targetTitle],
  )
  return rows.map((r) =>
    toNote(r.id, r.title, r.body_text, r.meta_json, r.created_at, r.updated_at),
  )
}

// ---- Daily Note ----

/** 获取某天的 Daily Note（不存在返回 null）。 */
export async function getDailyNote(
  dateKey = dailyKey(),
  workspaceId = 'default',
): Promise<PkmNote | null> {
  const rows = await localDB.query<{ id: string; title: string; body_text: string; meta_json: string; created_at: number; updated_at: number }>(
    `SELECT id, title, body_text, meta_json, created_at, updated_at
     FROM local_assets
     WHERE workspace_id = ? AND kind = ? AND deleted_at IS NULL
       AND json_extract(meta_json, '$.dailyDate') = ?
     LIMIT 1`,
    [workspaceId, NOTE_KIND, dateKey],
  )
  if (rows.length === 0) return null
  const r = rows[0]
  return toNote(r.id, r.title, r.body_text, r.meta_json, r.created_at, r.updated_at)
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
