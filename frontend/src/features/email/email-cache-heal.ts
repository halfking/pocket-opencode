/**
 * 邮件本地缓存的自愈（2026-10-01 修复：一天前的邮件看不到）。
 *
 * ## 症状
 * 收件箱里「一天前的邮件」整体消失，但今天的新邮件正常。
 *
 * ## 根因
 * 增量同步的 `since` 取本地 `MAX(updated_at)`，服务端据此过滤：
 *
 *     WHERE GREATEST(e.date, COALESCE(e.processed_at,0), e.created_at) > since
 *
 * 这意味着**只要本地还残留一封较新的邮件，`since` 就是「那一封的时间」，
 * 比它更早的历史邮件会被服务端直接过滤掉、永远不下发**。
 *
 * 而原有的自愈条件 `shouldRetryFullListPull` 只在 `localCount <= 0`
 * （本地完全为空）时才触发全量重拉。于是出现一个**永远自愈不了**的空洞：
 *
 *     本地 = [今天的邮件]           ← 还有 1 封，所以 localCount > 0
 *     since = 今天                 ← 服务端据此过滤
 *     结果 = 旧邮件永远补不回来
 *
 * 触发空洞的现实原因很常见：localStorage/SQLite 配额写满后被静默丢弃、
 * App 更新重建库、schema 迁移清表、用户手动清缓存——都只影响历史行，
 * 不影响最近写入的几封。
 *
 * ## 修法
 * 判定「本地是否比服务端有缺口」，有缺口就回补一个时间窗（不是把 since
 * 归零重拉全量，那对大邮箱是灾难性流量）。
 */

/** 单次回补请求的封数（与服务端 limit 一致）。 */
export const BACKFILL_LIMIT = 200
/**
 * 回补的时间窗（毫秒）。一次向前回补多久。
 *
 * 取 7 天：足以覆盖用户报的「一天前」症状，同时避免一次拉回几十万封。
 */
export const BACKFILL_WINDOW_MS = 7 * 24 * 60 * 60 * 1000

export interface CacheHealthInput {
  /** 本地未删除邮件总数。 */
  localCount: number
  /** 服务端最近窗口内的邮件数。 */
  serverCount: number
  /** 本地已持有的最大 updated_at；0 表示本地为空。 */
  localMaxUpdatedAt: number
  /**
   * 本地**最近**一封邮件的 date（毫秒）。
   *
   * 用 date 而不是 updated_at 判断新鲜度：updated_at 会被重跑同步刷新，
   * 一封一年前的邮件重跑后 updated_at 也是今天，用它判会误以为本地很新。
   */
  localNewestEmailDateMs: number
  /** 判定「最近」的时间基准，一般传 Date.now()。 */
  nowMs: number
}

/** 本地比服务端新鲜度落后多久才判 stale（3 天静默期不算异常）。 */
export const STALE_THRESHOLD_MS = 3 * 24 * 60 * 60 * 1000

/**
 * 本地缓存相对服务端是否**确定缺失**邮件。
 *
 * 三个独立信号，任一成立即判定有缺口：
 *  1. 本地为空；
 *  2. 服务端比本地多 —— 最硬的信号；
 *  3. 本地最新邮件的 date 明显落后 —— 增量链路断了。
 */
export function detectCacheGap(input: CacheHealthInput): {
  hasGap: boolean
  reason: 'empty' | 'server-ahead' | 'stale' | 'ok'
  serverAheadBy: number
} {
  const { localCount, serverCount, localNewestEmailDateMs, nowMs } = input
  const serverAheadBy = Math.max(0, serverCount - localCount)

  if (localCount <= 0) {
    return { hasGap: true, reason: 'empty', serverAheadBy: Math.max(serverAheadBy, serverCount) }
  }
  if (serverAheadBy > 0) {
    return { hasGap: true, reason: 'server-ahead', serverAheadBy }
  }
  // 本地不比服务端少，但最新邮件已很久没更新 → 增量链路可能断了。
  // 3 天阈值：正常的静默期（出差/周末/假期）不该触发回补。
  if (localNewestEmailDateMs > 0 && nowMs - localNewestEmailDateMs > STALE_THRESHOLD_MS) {
    return { hasGap: true, reason: 'stale', serverAheadBy }
  }
  return { hasGap: false, reason: 'ok', serverAheadBy: 0 }
}

/**
 * 计算回补时应使用的 `since`。
 *
 * 关键点：**不是把 since 归零**，而是从「本地最新一封的时间」往前退到窗口起点。
 * 归零会让服务端把所有历史邮件都算进结果，对大邮箱是灾难性流量。
 */
export function backfillSince(
  localMaxUpdatedAt: number,
  localNewestEmailDateMs: number,
  nowMs: number,
  windowMs = BACKFILL_WINDOW_MS,
): number {
  const windowStart = nowMs - windowMs
  if (localMaxUpdatedAt <= 0 || localNewestEmailDateMs <= 0) return windowStart
  // 取更早的那个：本地很新时用窗口起点（覆盖窗口内所有空洞），
  // 本地很旧时用本地时间戳（避免拉到「本地已有、只是没同步」的无用数据）。
  const fromLocal = Math.min(localMaxUpdatedAt, localNewestEmailDateMs) - 1
  return Math.max(0, Math.min(windowStart, fromLocal))
}

export type SyncStrategy =
  | { kind: 'incremental'; since: number }
  | { kind: 'backfill'; since: number; reason: string }
  | { kind: 'full'; since: 0 }

export function planSync(args: {
  localMaxUpdatedAt: number
  localNewestEmailDateMs: number
  serverCount: number
  localCount: number
  nowMs: number
}): SyncStrategy {
  const { localMaxUpdatedAt, localNewestEmailDateMs, serverCount, localCount, nowMs } = args
  if (localMaxUpdatedAt <= 0 || localCount <= 0) {
    return { kind: 'full', since: 0 }
  }
  const gap = detectCacheGap({
    localCount,
    serverCount,
    localMaxUpdatedAt,
    localNewestEmailDateMs,
    nowMs,
  })
  if (gap.hasGap) {
    return {
      kind: 'backfill',
      since: backfillSince(localMaxUpdatedAt, localNewestEmailDateMs, nowMs),
      reason: gap.reason,
    }
  }
  return { kind: 'incremental', since: localMaxUpdatedAt }
}

/**
 * 回补是否还需要继续翻页。
 *
 * 回补窗口可能有上千封，一页 200 封拉不完；只要这一页「拉满」了，
 * 说明还有更早的在后面。墓碑也算本页内容 —— 否则「有删除」会被
 * 误判为已到边界、提前停止回补。
 */
export function shouldContinueBackfill(pageSize: number, gotDeletedInPage: number, limit = BACKFILL_LIMIT): boolean {
  return pageSize + gotDeletedInPage >= limit
}

/**
 * 翻页锚点推进。
 *
 * 必须用**本页实际收到的最早一封**的时间戳再退 1ms。
 *
 * 不能用「本地最新」：那与 since 几乎相同，退 1ms 后仍 > since，
 * 服务端会把同一页再要一遍——要么死循环，要么被 no-progress 守卫直接
 * 退出，回补永远只有一页。也不能沿用同一个 since，结果同上。
 */
export function nextPageSince(
  since: number,
  oldestDateMs: number,
): { since: number; ok: boolean; reason: 'advanced' | 'no-data' | 'no-progress' } {
  if (oldestDateMs <= 0) return { since, ok: false, reason: 'no-data' }
  const advanced = oldestDateMs - 1
  if (advanced >= since) return { since, ok: false, reason: 'no-progress' }
  return { since: advanced, ok: true, reason: 'advanced' }
}
