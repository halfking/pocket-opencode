/**
 * 邮件缓存自愈的**编排层**（把 email-cache-heal 的纯决策接到真实 IO 上）。
 *
 * 为什么要单独一层：email-cache-heal 里的 planSync / nextPageSince /
 * shouldContinueBackfill 都是纯函数，好测；但真正会出事的恰恰是它们之间的
 * **调用顺序**——比如「客户端回补一行都没写进去」到底该不该升级到服务端
 * IMAP 回补（该升，因为用户报的故障就是服务端库里没有那批邮件）。这种顺序
 * bug 靠纯函数测试永远抓不到，必须有一层能注入假 IO 的编排测试。
 *
 * 本模块不直接 import emails-store / api，而是要求调用方注入依赖，所以
 * node --test 能用普通假实现跑完全部路径，不依赖 SQLite、不依赖网络。
 */

import {
  BACKFILL_LIMIT,
  detectCacheGap,
  nextPageSince,
  planSync,
  shouldContinueBackfill,
} from './email-cache-heal.ts'
import type { CacheHealthInput, SyncStrategy } from './email-cache-heal.ts'

/** 单次拉取最多翻几页（覆盖 3 × 200 = 600 封）。 */
export const MAX_BACKFILL_PAGES = 3

/** 升级到服务端 IMAP 回补时的回补深度（天），与 backend 的 DefaultBackfillDays 一致。 */
export const SERVER_BACKFILL_DAYS = 30

/** 一页同步的结果，明细见 emailsStore.syncEmailsFromServerDetailed。 */
export interface SyncPageResult {
  inserted: number
  received: number
  tombstones: number
  oldestDateMs: number
}

export interface HealerDeps {
  countLocal(): Promise<number>
  maxUpdatedAt(): Promise<number>
  newestDate(): Promise<number>
  /** 服务端匹配总数（不受分页 limit 影响的真值）。 */
  serverCount(): Promise<number>
  syncPage(limit: number, since: number): Promise<SyncPageResult>
  /** 让服务端回 IMAP 源头按天数窗口重取。 */
  serverBackfill(days: number): Promise<{ saved: number; errors: string[] }>
  now?(): number
  log?: { info(m: string): void; warn(m: string): void }
}

interface GapSnapshot extends CacheHealthInput {
  gap: ReturnType<typeof detectCacheGap>
  plan: SyncStrategy
}

const noopLog = { info: () => {}, warn: () => {} }

async function snapshot(d: HealerDeps): Promise<GapSnapshot> {
  const [localCount, localMaxUpdatedAt, localNewestEmailDateMs, serverCount] = await Promise.all([
    d.countLocal(),
    d.maxUpdatedAt(),
    d.newestDate(),
    d.serverCount(),
  ])
  const nowMs = d.now ? d.now() : Date.now()
  const base = { localCount, serverCount, localMaxUpdatedAt, localNewestEmailDateMs, nowMs }
  return { ...base, gap: detectCacheGap(base), plan: planSync(base) }
}

/**
 * 按页回补，直到「本页没拉满」或达到翻页上限。
 *
 * 锚点推进见 nextPageSince：必须用**本页最早一封**的 date 再退 1ms，否则
 * 服务端会把同一页再要一遍——要么死循环，要么被 no-progress 守卫直接退出，
 * 回补永远只有一页。
 */
export async function runBackfill(
  d: HealerDeps,
  since: number,
  reason: string,
  maxPages = MAX_BACKFILL_PAGES,
): Promise<number> {
  const log = d.log ?? noopLog
  let cursor = since
  let total = 0
  for (let page = 0; page < maxPages; page++) {
    const r = await d.syncPage(BACKFILL_LIMIT, cursor)
    total += r.inserted
    if (!shouldContinueBackfill(r.received, r.tombstones)) break
    const next = nextPageSince(cursor, r.oldestDateMs)
    if (!next.ok) {
      if (next.reason === 'no-progress') {
        log.warn('[email] 回补无进展，已停止翻页（服务端可能忽略了 since）')
      }
      break
    }
    cursor = next.since
  }
  log.info(`[email] 缓存回补（${reason}）：写入 ${total} 封`)
  return total
}

/**
 * 按既定策略执行。
 *
 * incremental 也要真的拉一页——它是「没有缺口时的常规增量」，不是「没事可做」。
 * 把它当空操作会让 pullWithPlan 在最健康的那条路径上一次请求都不发，
 * 下拉刷新变成纯粹的空转。
 */
async function runPlan(d: HealerDeps, plan: SyncStrategy): Promise<number> {
  if (plan.kind === 'backfill') return runBackfill(d, plan.since, plan.reason)
  const r = await d.syncPage(BACKFILL_LIMIT, plan.since)
  return r.inserted
}

export interface PullResult {
  strategy: 'full' | 'backfill' | 'incremental'
  reason: string
  filled: number
}

/**
 * 常规下拉刷新用：按 planSync 的结论拉一次。
 *
 * 这里**不升级**到服务端 IMAP 回补——下拉刷新是高频手势，不能让它连真 IMAP。
 * 升级只发生在用户主动同步（healGap）路径。
 */
export async function pullWithPlan(d: HealerDeps): Promise<PullResult> {
  const { plan, gap } = await snapshot(d)
  const filled = await runPlan(d, plan)
  return { strategy: plan.kind, reason: gap.reason, filled }
}

export interface HealResult {
  /** 客户端回补写入的行数（含升级后的第二轮）。 */
  filled: number
  /** 是否升级到了服务端 IMAP 回补。 */
  serverBackfilled: boolean
  /** 服务端回补写入的行数。 */
  serverSaved: number
  reason: string
}

/**
 * 主动同步时的两级自愈：
 *
 * 1. 客户端按时间窗分页回补（拿回服务端库里**已有**的行）；
 * 2. 若补完**仍有缺口**，升级为服务端回 IMAP 源头按日期窗口重取。
 *
 * 第 2 级存在的唯一理由：增量同步只按 `LastSyncedUID` 往后搜、且每轮只取
 * 最近 50 封，早先被截断的历史邮件从来没进过服务端库。此时第 1 级从服务端
 * 拉回来的就是 0 行——所以**绝不能按「第 1 级写了多少行」短路**，那会让自愈
 * 在最需要它的场景下彻底失效。
 */
export async function healGap(d: HealerDeps): Promise<HealResult> {
  const log = d.log ?? noopLog
  const before = await snapshot(d)
  if (!before.gap.hasGap) {
    return { filled: 0, serverBackfilled: false, serverSaved: 0, reason: before.gap.reason }
  }

  let filled = await runPlan(d, before.plan)

  const after = await snapshot(d)
  if (!after.gap.hasGap) {
    return { filled, serverBackfilled: false, serverSaved: 0, reason: before.gap.reason }
  }

  log.warn(
    `[email] 客户端回补后仍有缺口（${after.gap.reason}，服务端 ${after.serverCount} / 本地 ${after.localCount}），触发服务端 IMAP 历史回补`,
  )
  const bf = await d.serverBackfill(SERVER_BACKFILL_DAYS)
  for (const e of bf.errors) log.warn(`[email] 服务端历史回补失败：${e}`)
  if (bf.saved > 0) filled += await runPlan(d, (await snapshot(d)).plan)

  return {
    filled,
    serverBackfilled: true,
    serverSaved: bf.saved,
    reason: before.gap.reason,
  }
}
