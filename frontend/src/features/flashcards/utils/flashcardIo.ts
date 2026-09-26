/**
 * flashcardIo —— JSON 导入 / 导出（Phase 6 简化版；Phase 9.3 切流至 pocket-native）。
 *
 * 简化说明：
 *   - **不解析 .apkg**（Anki SQLite 格式）；改用 JSON（同 Anki collection 概念，
 *     但每张 note / card / deckConfig 都是显式 JSON 对象）。
 *   - 导出：notes + cards + deckConfigs + reviewLogs（可选）→ JSON 字符串。
 *   - 导入：JSON 字符串 → store 全量覆盖（带 confirmation prompt）。
 *
 * 用户场景：
 *   - 跨设备迁移（导出 JSON → 其他设备导入）。
 *   - 备份 / 还原。
 *   - 与 Anki .apkg 互通：Phase 6.1 增量 sql.js 解析。
 *
 * 平台分发（Phase 9.3）：
 *   - 原生壳：pocket-native.filesystem 写 Documents → pocket-native.share 弹面板。
 *   - Web：    Blob + URL.createObjectURL + <a download>（不走 filesystem,web filesystem
 *              仅 media 用,与导出无关）。
 *   - iOS stub：pocket-native.share 抛 notImpl；web fallback 继续工作。
 */
import { getPocketNative } from '../../../native/pocket-native'
import type { FlashcardCard, FlashcardDeckConfig, FlashcardNote, FlashcardReviewLog } from '../../../types/flashcards'

const EXPORT_VERSION = 1

export interface FlashcardExportBundle {
  version: number
  exportedAt: number
  notes: FlashcardNote[]
  cards: FlashcardCard[]
  deckConfigs: FlashcardDeckConfig[]
  /** 可选：本地复习日志（统计迁移用）。 */
  reviewLogs?: FlashcardReviewLog[]
}

export function buildExportBundle(input: {
  notes: FlashcardNote[]
  cards: FlashcardCard[]
  deckConfigs: FlashcardDeckConfig[]
  reviewLogs?: FlashcardReviewLog[]
}): FlashcardExportBundle {
  return {
    version: EXPORT_VERSION,
    exportedAt: Math.floor(Date.now() / 1000),
    ...input,
  }
}

/**
 * 导出：写 JSON 到本地，并通过 Share 让用户决定去向。
 *
 * - Web 浏览器：Blob + URL.createObjectURL 触发下载（不写 IndexedDB，因为 media
 *   的 web filesystem 是给 flashcardMedia 用的，导出 JSON 是用户文件,理应直接走浏览器下载）。
 * - 原生壳：pocket-native.filesystem 写 documents → getUri → pocket-native.share 弹面板。
 * - iOS stub：pocket-native.filesystem.writeFile 抛 notImpl,web fallback 路径不触发。
 */
export async function exportJson(opts: {
  bundle: FlashcardExportBundle
  filename?: string
}): Promise<{ method: 'web' | 'native'; url?: string }> {
  const filename = opts.filename ?? `openpocket-flashcards-${new Date().toISOString().slice(0, 10)}.json`
  const json = JSON.stringify(opts.bundle, null, 2)
  const native = getPocketNative()

  // 原生壳（Android 走 Capacitor,iOS 走 stub;Phase 7.1 接通 Swift 后扩展）
  if (native.platform === 'android' || native.platform === 'ios') {
    try {
      await native.filesystem.writeFile(filename, json, 'documents')
      const { uri: fileUri } = await native.filesystem.getUri(filename, 'documents')
      try {
        await native.share.share({
          title: 'OpenPocket Flashcards Export',
          text: `${opts.bundle.notes.length} notes · ${opts.bundle.cards.length} cards`,
          url: fileUri,
          dialogTitle: '导出卡片到',
        })
      } catch {
        /* 用户取消分享不报错 */
      }
      return { method: 'native' }
    } catch (e) {
      // iOS stub 走这里:notImpl 后退 web 下载
      if (native.platform === 'ios') {
        return webDownload(json, filename)
      }
      throw e
    }
  }

  // Web：返回 blob 让调用方下载
  return webDownload(json, filename)
}

function webDownload(json: string, filename: string): { method: 'web'; url: string } {
  const blob = new Blob([json], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  setTimeout(() => URL.revokeObjectURL(url), 1000)
  return { method: 'web', url }
}

/**
 * 导入：从 File 对象读 JSON，解析为 bundle。
 *
 * 校验：
 *   - 必须含 version / exportedAt / notes / cards / deckConfigs
 *   - version 范围兼容：当前仅 1
 *
 * 调用方拿到 bundle 后自行决定合并策略（覆盖 / 合并 / 选 deck 导入）。
 */
export async function importJsonFromFile(file: File): Promise<FlashcardExportBundle> {
  const text = await file.text()
  return importJsonFromText(text)
}

export function importJsonFromText(text: string): FlashcardExportBundle {
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new Error('not valid JSON')
  }
  if (typeof parsed !== 'object' || parsed === null) {
    throw new Error('expected JSON object')
  }
  const obj = parsed as Partial<FlashcardExportBundle>
  if (typeof obj.version !== 'number') throw new Error('missing version')
  if (obj.version > EXPORT_VERSION) throw new Error(`unsupported version ${obj.version}`)
  if (!Array.isArray(obj.notes)) throw new Error('missing notes[]')
  if (!Array.isArray(obj.cards)) throw new Error('missing cards[]')
  if (!Array.isArray(obj.deckConfigs)) throw new Error('missing deckConfigs[]')
  return {
    version: obj.version,
    exportedAt: typeof obj.exportedAt === 'number' ? obj.exportedAt : 0,
    notes: obj.notes as FlashcardNote[],
    cards: obj.cards as FlashcardCard[],
    deckConfigs: obj.deckConfigs as FlashcardDeckConfig[],
    reviewLogs: Array.isArray(obj.reviewLogs) ? obj.reviewLogs : undefined,
  }
}