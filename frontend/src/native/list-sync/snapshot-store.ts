/**
 * snapshot-store — 本地快照存储（list-sync 体系的通用持久化点）。
 *
 * 按 namespace 分组存 JSON 快照，支持 dirty 标记（离线先行、联网回推）。
 * 当前实现基于 localStorage（web / dev 环境可用）；原生壳后续可无缝
 * 替换为 SQLite 实现而不影响调用方接口。
 */

export interface SnapshotRow<T> {
  id: string
  updatedAt: number
  dirty: boolean
  payload: T
}

const PREFIX = 'pocket:snapshots:'

function storage(): Storage | null {
  try {
    if (typeof localStorage === 'undefined') return null
    return localStorage
  } catch {
    return null
  }
}

// ⚠ `.ts` 扩展名是有意的：§875 实测本仓 TS 配置已开
//   `allowImportingTsExtensions`，且 `features/messages/hub-view.test.ts`
//   等已有先例。带上它之后**本模块能被 node 的 strip-types loader 直接加载**
//   ⇒ §876/D45 的「损坏不许覆写」才有**行为级**判据（否则只有形状保证）。
import { preserveCorruptRaw } from '../../utils/corrupt-preserve.ts'

/**
 * ⚠⚠⚠§876/D45：`loadNS` 的 catch 原来直接 `return {}` ——
 *   **读失败被谎报成「这个命名空间本来就没有快照」**，
 *   而且比 §858/D21（aiChatStore 那一处）**更糟**：
 *   ① 不留证（而本仓已经有 `utils/corrupt-preserve.ts` 这个唯一留证落点，
 *      同一族的 `aiChatStore` / `crypto-config` 都接上了，只有这里没接）；
 *   ② **下一次 `upsertSnapshots` 会把整包覆盖掉** ——
 *      `loadNS` 返回 `{}` ⇒ 只写进新 row ⇒ `saveNS` 把整包覆写
 *      ⇒ **同一命名空间里其它 id 的行一起消失**。
 *      本仓的 ns 是 `ws:<workspaceId>:inst:<instanceId>` 一行一个
 *      ⇒ 一次损坏 = 所有 workspace × instance 的会话列表缓存全没。
 *
 * 修法按「降级是否破坏性」分两路（§862 的口径）：
 *   · `listSnapshots`（读）：仍然返回空数组，**但先留证** ——
 *     对调用方来说「没有缓存」与「缓存坏了」都要能正常渲染。
 *   · `upsertSnapshots`（写）：**在原始数据还损坏时拒绝写入并抛出** ——
 *     覆写是破坏性的，不能「顺手」做。唯一调用方 `SessionListView` 已经
 *     整个包了 try/catch，所以抛错不会打断界面。
 */

/** loadNS 的结果：要么有行，要么「数据是坏的」——两者绝不能混成同一个 `{}`。 */
type LoadResult<T> = { ok: true; rows: Record<string, SnapshotRow<T>> } | { ok: false; raw: string }

function loadNS<T>(ns: string): LoadResult<T> {
  let raw: string | null = null
  try {
    raw = storage()?.getItem(PREFIX + ns) ?? null
  } catch {
    // 读都读不出来（隐私模式等）：这是**环境**问题，不是数据损坏，不留证。
    return { ok: true, rows: {} }
  }
  if (!raw) return { ok: true, rows: {} }
  try {
    const parsed = JSON.parse(raw) as Record<string, SnapshotRow<T>>
    if (!parsed || typeof parsed !== 'object') return { ok: false, raw }
    return { ok: true, rows: parsed }
  } catch {
    return { ok: false, raw }
  }
}

/** 损坏时留证（复用 §858 起的唯一落点，不在这里另写一份键名实现）。 */
function preserveSnapshot(ns: string, raw: string): void {
  preserveCorruptRaw(`pocket:snapshots:corrupt`, ns, raw)
}

function saveNS<T>(ns: string, rows: Record<string, SnapshotRow<T>>): void {
  try {
    storage()?.setItem(PREFIX + ns, JSON.stringify(rows))
  } catch {
    // ⚠ 写侧：§866 的族规律问的是「丢的是什么」——
    //   这里存的是**缓存**（会话列表 / 邮件正文的兼容迁移源），
    //   服务端才是权威 ⇒ 不阻断、只静默降级是对的。
    //   ⚠ 但「静默」要真的静默到**可诊断**：下面 warn 一行，
    //   否则事后无法判断这条路是否已被存储容量关掉。
    console.warn(`[snapshot-store] 命名空间 ${ns} 未能写入本地存储（缓存降级为不落盘，服务端仍为权威）`)
  }
}

export async function listSnapshots<T>(ns: string): Promise<Array<SnapshotRow<T>>> {
  const loaded = loadNS<T>(ns)
  if (!loaded.ok) {
    // ⚠ 读路径仍返回空数组（调用方照常渲染「没有缓存」），
    //   但**证据留下来了** —— 而这一点是原实现完全没有的。
    preserveSnapshot(ns, loaded.raw)
    return []
  }
  return Object.values(loaded.rows)
}

export async function upsertSnapshots<T>(
  ns: string,
  rows: Array<SnapshotRow<T>>,
): Promise<void> {
  if (!rows.length) return
  const loaded = loadNS<T>(ns)
  if (!loaded.ok) {
    // ⛔ 不覆写。原始字节在存储里还完好，覆写等于**不可逆销毁**。
    preserveSnapshot(ns, loaded.raw)
    throw new Error(`命名空间 ${ns} 的快照已损坏，已留证并拒绝覆写（请先处理损坏数据）`)
  }
  const existing = loaded.rows
  for (const row of rows) {
    if (!row?.id) continue
    existing[row.id] = row
  }
  saveNS(ns, existing)
}
