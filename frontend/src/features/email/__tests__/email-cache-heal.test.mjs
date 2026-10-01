import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  BACKFILL_LIMIT,
  BACKFILL_WINDOW_MS,
  STALE_THRESHOLD_MS,
  backfillSince,
  detectCacheGap,
  nextPageSince,
  planSync,
  shouldContinueBackfill,
} from '../email-cache-heal.ts'
import { shouldRetryFullListPull } from '../email-fetch-plan.ts'

const DAY = 24 * 60 * 60 * 1000
const NOW = Date.UTC(2026, 9, 1, 12, 0, 0)

// ── 缺口检测 ────────────────────────────────────────────────────────────────

test('核心症状：本地只剩最新几封时必须判定为有缺口', () => {
  // 用户报的现象原样构造：本地 3 封（今天的），服务端 800 封（含一天前的）
  const gap = detectCacheGap({
    localCount: 3,
    serverCount: 800,
    localMaxUpdatedAt: NOW,
    localNewestEmailDateMs: NOW - 2 * 3600_000,
    nowMs: NOW,
  })
  assert.equal(gap.hasGap, true)
  assert.equal(gap.reason, 'server-ahead')
  assert.equal(gap.serverAheadBy, 797)
})

test('本地为空 → empty', () => {
  const gap = detectCacheGap({
    localCount: 0,
    serverCount: 500,
    localMaxUpdatedAt: 0,
    localNewestEmailDateMs: 0,
    nowMs: NOW,
  })
  assert.equal(gap.hasGap, true)
  assert.equal(gap.reason, 'empty')
})

test('数量一致但本地最新邮件已 5 天没更新 → stale（增量链路断了）', () => {
  const gap = detectCacheGap({
    localCount: 100,
    serverCount: 100,
    localMaxUpdatedAt: NOW - 5 * DAY,
    localNewestEmailDateMs: NOW - 5 * DAY,
    nowMs: NOW,
  })
  assert.equal(gap.hasGap, true)
  assert.equal(gap.reason, 'stale')
})

test('静默期 2 天不算 stale（出差/周末不该触发回补）', () => {
  const gap = detectCacheGap({
    localCount: 100,
    serverCount: 100,
    localMaxUpdatedAt: NOW - 2 * DAY,
    localNewestEmailDateMs: NOW - 2 * DAY,
    nowMs: NOW,
  })
  assert.equal(gap.hasGap, false)
  assert.equal(gap.reason, 'ok')
})

test('新鲜且数量一致 → 无缺口（正常路径不被误触发）', () => {
  const gap = detectCacheGap({
    localCount: 800,
    serverCount: 800,
    localMaxUpdatedAt: NOW - 3600_000,
    localNewestEmailDateMs: NOW - 3600_000,
    nowMs: NOW,
  })
  assert.equal(gap.hasGap, false)
})

test('阈值边界：恰好 3 天不判 stale，超过才判', () => {
  const atThreshold = detectCacheGap({
    localCount: 10, serverCount: 10,
    localMaxUpdatedAt: NOW, localNewestEmailDateMs: NOW - STALE_THRESHOLD_MS, nowMs: NOW,
  })
  assert.equal(atThreshold.hasGap, false)
  const beyond = detectCacheGap({
    localCount: 10, serverCount: 10,
    localMaxUpdatedAt: NOW, localNewestEmailDateMs: NOW - STALE_THRESHOLD_MS - 1, nowMs: NOW,
  })
  assert.equal(beyond.hasGap, true)
})

// ── 回补窗口 ────────────────────────────────────────────────────────────────

test('backfillSince 不是归零，而是退一个 7 天窗口', () => {
  const s = backfillSince(NOW, NOW - 2 * 3600_000, NOW)
  assert.equal(s, NOW - BACKFILL_WINDOW_MS)
  assert.ok(s > 0, '绝不能是 0：那会把全部历史一次性拉下来')
})

test('backfillSince 覆盖「一天前」的邮件（用户症状的时点）', () => {
  const s = backfillSince(NOW, NOW - 2 * 3600_000, NOW)
  assert.ok(s < NOW - DAY, '窗口起点必须早于一天前，否则补不回丢失的那批')
})

test('backfillSince：本地邮件比窗口还老时，以本地时间戳为锚', () => {
  const newest = NOW - 20 * DAY
  const s = backfillSince(newest, newest, NOW)
  assert.equal(s, newest - 1)
})

// ── 策略选择 ────────────────────────────────────────────────────────────────

test('planSync：首次安装走全量', () => {
  const p = planSync({
    localMaxUpdatedAt: 0, localNewestEmailDateMs: 0,
    serverCount: 800, localCount: 0, nowMs: NOW,
  })
  assert.equal(p.kind, 'full')
  assert.equal(p.since, 0)
})

test('planSync：有缺口走 backfill，且窗口覆盖症状', () => {
  const p = planSync({
    localMaxUpdatedAt: NOW, localNewestEmailDateMs: NOW - 2 * 3600_000,
    serverCount: 800, localCount: 3, nowMs: NOW,
  })
  assert.equal(p.kind, 'backfill')
  assert.ok(p.since > 0, 'backfill 也不该是 0')
  assert.ok(p.since < NOW - DAY, '必须覆盖一天前的邮件')
  assert.equal(p.reason, 'server-ahead')
})

test('planSync：正常时走增量，不额外打扰服务端', () => {
  const max = NOW - 3600_000
  const p = planSync({
    localMaxUpdatedAt: max, localNewestEmailDateMs: max,
    serverCount: 800, localCount: 800, nowMs: NOW,
  })
  assert.equal(p.kind, 'incremental')
  assert.equal(p.since, max, '增量必须原样用本地最大 updated_at')
})

// ── 分页 ────────────────────────────────────────────────────────────────────

test('拉满一页就要继续翻（回补窗口可能有上千封）', () => {
  assert.equal(shouldContinueBackfill(BACKFILL_LIMIT, 0), true)
  assert.equal(shouldContinueBackfill(0, 0), false)
  assert.equal(shouldContinueBackfill(10, 0), false)
})

test('墓碑计入本页内容：否则有删除时会提前停止回补', () => {
  assert.equal(shouldContinueBackfill(150, 50), true)
})

test('nextPageSince：锚点取本页最早一封，能继续往更早翻', () => {
  const since = 1_000_000
  const r = nextPageSince(since, since - 1000)
  assert.equal(r.ok, true)
  assert.equal(r.since, since - 1001)
})

test('nextPageSince：误用「本地最新」当锚点会被拦下（no-progress）', () => {
  const since = 1_000_000
  const r = nextPageSince(since, since + 5000)
  assert.equal(r.ok, false)
  assert.equal(r.reason, 'no-progress')
  assert.equal(r.since, since, '原地不动比乱推进安全：不会死循环')
})

test('nextPageSince：空数据不推进', () => {
  const r = nextPageSince(1_000_000, 0)
  assert.equal(r.ok, false)
  assert.equal(r.reason, 'no-data')
})

test('连续三页推进严格递减，不会反复拿同一页', () => {
  let since = 10_000_000
  const seen = []
  for (let i = 0; i < 3; i++) {
    const r = nextPageSince(since, since - 5000)
    assert.equal(r.ok, true, `第 ${i + 1} 页应能继续`)
    seen.push(r.since)
    since = r.since
  }
  for (let i = 1; i < seen.length; i++) assert.ok(seen[i] < seen[i - 1])
  assert.equal(new Set(seen).size, seen.length)
})

// ── 负控对照：证明旧逻辑治不了这个病 ────────────────────────────────────────

test('负控：旧逻辑在「本地残留几封」时放弃，新逻辑必须回补', () => {
  // 旧逻辑只在本地完全为空时才全量重拉
  assert.equal(shouldRetryFullListPull(3, 1, NOW), false)
  const p = planSync({
    localMaxUpdatedAt: NOW, localNewestEmailDateMs: NOW - 2 * 3600_000,
    serverCount: 800, localCount: 3, nowMs: NOW,
  })
  assert.equal(p.kind, 'backfill')
})

test('负控：旧逻辑唯一的救赎路径（本地全空）新逻辑也覆盖', () => {
  assert.equal(shouldRetryFullListPull(0, 0, NOW), true)
  const p = planSync({
    localMaxUpdatedAt: 0, localNewestEmailDateMs: 0,
    serverCount: 800, localCount: 0, nowMs: NOW,
  })
  assert.equal(p.kind, 'full')
})

test('负控：新逻辑不会把「本地确实最新」误判成缺口', () => {
  const p = planSync({
    localMaxUpdatedAt: NOW - 3600_000, localNewestEmailDateMs: NOW - 3600_000,
    serverCount: 800, localCount: 800, nowMs: NOW,
  })
  assert.equal(p.kind, 'incremental')
})
