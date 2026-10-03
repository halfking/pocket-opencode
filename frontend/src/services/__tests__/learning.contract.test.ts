/**
 * learning.contract.test.ts — Learning Core 的 HTTP 契约锁定
 * （docs/学习muse/04-数据模型与API契约.md §2.2）。
 *
 * 运行：cd frontend && node --experimental-strip-types --test \
 *        src/services/__tests__/learning.contract.test.ts
 *
 * 锁定四件事，任何一侧改动都会打红：
 *  1. 路由表：/api/learning 下的每条路径与方法；
 *  2. 请求体形状（尤其：**不发送** userId / workspaceId，服务端以 JWT 为准）；
 *  3. 响应信封字段名（items / reminders / DueSummary 四个计数字段）；
 *  4. 错误信封沿用 {error, code?, retryable?, request_id?}。
 *
 * 为什么本文件自包含、不 import services/learning.ts：
 * service 依赖 ../api/http → ../stores/auth（pinia 等），而 node 的
 * strip-types 无法解析无扩展名的 ESM 说明符，整条链加载不了。
 * 这与 src/services/__tests__/flashcards.contract.test.ts 的做法一致：
 * 契约测试站在**客户端一侧**复述请求形状；服务端一侧由
 * backend/internal/server/learning_route_test.go 锁同一张路由表。
 * 展示策略（"今天没事就别打扰"）则由 src/utils/__tests__/learning-due.test.ts
 * 真正跑到函数。
 */

import { strict as assert } from 'node:assert'
import { afterEach, beforeEach, describe, it } from 'node:test'

// ---------- 契约常量（与 docs/学习muse/04 §2.2 一一对应） ----------

const BASE = '/api/learning'

// ---------- 类型（与 backend/internal/learning 的 JSON tag 对齐） ----------

type LearningSourceKind = 'note' | 'email' | 'rss' | 'meeting' | 'chat' | 'manual'
type LearningStage = 'inbox' | 'learning' | 'review' | 'mastered' | 'archived'
type LearningReminderKind = 'daily_digest' | 'spaced_review' | 'deadline' | 'streak'
type LearningRuleKind = 'daily' | 'interval' | 'once'
type LearningReminderState = 'pending' | 'sent' | 'acked' | 'snoozed' | 'done'

interface LearningItem {
  id: string
  workspaceId: string
  userId: string
  sourceKind: LearningSourceKind
  sourceId: string
  title: string
  summary?: string
  deckId?: string
  stage: LearningStage
  importance: number
  capturedAt: number
  updatedAt: number
}

interface LearningReminder {
  id: string
  workspaceId: string
  userId: string
  kind: LearningReminderKind
  itemId?: string
  cardId?: string
  ruleKind: LearningRuleKind
  ruleValue?: string
  nextDueAt: number
  state: LearningReminderState
  lastSentAt?: number
  snoozedUntil?: number
  createdAt: number
  updatedAt: number
}

interface LearningDueSummary {
  userId?: string
  dueCards: number
  inbox: number
  reviewItems: number
  dueTasks: number
  nextDueAt?: number
}

interface ApiErrorEnvelope {
  error: string
  code?: string
  retryable?: boolean
  request_id?: string
}

interface LearningCaptureInput {
  sourceKind: LearningSourceKind
  sourceId: string
  title: string
  summary?: string
  deckId?: string
  importance?: number
  stage?: LearningStage
}

interface LearningReminderInput {
  kind: LearningReminderKind
  itemId?: string
  cardId?: string
  ruleKind: LearningRuleKind
  ruleValue?: string
  nextDueAt: number
}

// ---------- fetch mock 工具 ----------

interface CapturedCall {
  url: string
  method: string
  body: any | null
  headers: Record<string, string>
}

let captured: CapturedCall[] = []
let originalFetch: typeof fetch
let responseQueue: Array<{ status: number; body: any }> = []

function queueResponse(status: number, body: any) {
  responseQueue.push({ status, body })
}

function installFetchMock() {
  captured = []
  responseQueue = []
  originalFetch = globalThis.fetch
  // @ts-expect-error 测试替身
  globalThis.fetch = async (input: any, init?: RequestInit): Promise<Response> => {
    const url = typeof input === 'string' ? input : (input?.url ?? String(input))
    const method = (init?.method ?? 'GET').toUpperCase()
    let body: any = null
    if (init?.body) body = typeof init.body === 'string' ? JSON.parse(init.body) : init.body
    const headers: Record<string, string> = {}
    if (init?.headers) {
      const h = init.headers as any
      if (h instanceof Headers) h.forEach((v, k) => (headers[k] = v))
      else if (Array.isArray(h)) for (const [k, v] of h) headers[k] = v
      else Object.assign(headers, h)
    }
    captured.push({ url, method, body, headers })
    const next = responseQueue.shift() ?? { status: 200, body: {} }
    return new Response(JSON.stringify(next.body), {
      status: next.status,
      headers: { 'Content-Type': 'application/json' },
    })
  }
}

function uninstallFetchMock() {
  if (originalFetch) globalThis.fetch = originalFetch
}

beforeEach(installFetchMock)
afterEach(uninstallFetchMock)

/**
 * 下面的 request* 函数逐字复述 src/services/learning.ts 的实现形状
 * （URL 拼装、body 字段、envelope 解构）。改 service 时这里必须同步改，
 * 这正是本测试的意图：让契约漂移显式失败，而不是悄悄跟着漂。
 */
function requestListItems(filter: {
  stage?: LearningStage
  sourceKind?: string
  limit?: number
} = {}): Promise<LearningItem[]> {
  const params = new URLSearchParams()
  if (filter.stage) params.set('stage', filter.stage)
  if (filter.sourceKind) params.set('sourceKind', filter.sourceKind)
  if (filter.limit && filter.limit > 0) params.set('limit', String(filter.limit))
  const query = params.toString()
  const path = `${BASE}/items${query ? `?${query}` : ''}`
  return fetch(path, { method: 'GET' })
    .then((r) => r.json() as Promise<{ items: LearningItem[] }>)
    .then((body) => body.items ?? [])
}

function requestCaptureItem(input: LearningCaptureInput): Promise<LearningItem> {
  return fetch(`${BASE}/items`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  }).then((r) => r.json() as Promise<LearningItem>)
}

function requestUpdateStage(id: string, stage: LearningStage): Promise<void> {
  return fetch(`${BASE}/items/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ stage }),
  }).then(() => undefined)
}

function requestFetchDueSummary(): Promise<LearningDueSummary> {
  return fetch(`${BASE}/items/due`, { method: 'GET' }).then((r) => r.json())
}

function requestUpsertReminder(input: LearningReminderInput): Promise<LearningReminder> {
  return fetch(`${BASE}/reminders`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  }).then((r) => r.json())
}

function requestListReminders(state?: string, limit?: number): Promise<LearningReminder[]> {
  const params = new URLSearchParams()
  if (state) params.set('state', state)
  if (limit && limit > 0) params.set('limit', String(limit))
  const query = params.toString()
  return fetch(`${BASE}/reminders${query ? `?${query}` : ''}`, { method: 'GET' })
    .then((r) => r.json() as Promise<{ reminders: LearningReminder[] }>)
    .then((body) => body.reminders ?? [])
}

function requestSnooze(id: string, minutes?: number): Promise<number> {
  return fetch(`${BASE}/reminders/${encodeURIComponent(id)}/snooze`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(minutes ? { minutes } : {}),
  }).then((r) => r.json() as Promise<{ id: string; snoozedUntil: number }>)
    .then((body) => body.snoozedUntil)
}

function requestAck(id: string): Promise<void> {
  return fetch(`${BASE}/reminders/${encodeURIComponent(id)}/ack`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({}),
  }).then(() => undefined)
}

function requestSchedule(input: Record<string, unknown>): Promise<Record<string, unknown>> {
  return fetch(`${BASE}/schedule`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  }).then((r) => r.json())
}

const sampleItem: LearningItem = {
  id: 'litem-1',
  workspaceId: 'default',
  userId: 'u1',
  sourceKind: 'email',
  sourceId: 'em-42',
  title: 'Go 1.25 GC 变更要点',
  summary: '',
  deckId: '',
  stage: 'inbox',
  importance: 3,
  capturedAt: 1790000000,
  updatedAt: 1790000000,
}

const sampleReminder: LearningReminder = {
  id: 'lrem-1',
  workspaceId: 'default',
  userId: 'u1',
  kind: 'daily_digest',
  itemId: '',
  cardId: '',
  ruleKind: 'daily',
  ruleValue: '20:30',
  nextDueAt: 1790000000,
  state: 'pending',
  lastSentAt: 0,
  snoozedUntil: 0,
  createdAt: 1790000000,
  updatedAt: 1790000000,
}

// ============================================================================
// 1. POST /api/learning/items —— 收集材料（幂等）
// ============================================================================

describe('POST /api/learning/items', () => {
  it('路由与方法正确，请求体只带业务字段', async () => {
    queueResponse(201, sampleItem)
    await requestCaptureItem({ sourceKind: 'email', sourceId: 'em-42', title: 'Go 1.25 GC 变更要点' })

    const call = captured[0]
    assert.equal(call.method, 'POST')
    assert.equal(call.url, `${BASE}/items`)
    assert.deepEqual(call.body, {
      sourceKind: 'email',
      sourceId: 'em-42',
      title: 'Go 1.25 GC 变更要点',
    })
    assert.equal('userId' in call.body, false, 'userId must come from the JWT, never the body')
    assert.equal('workspaceId' in call.body, false, 'workspaceId must come from the JWT')
    assert.match(call.headers['Content-Type'] ?? '', /application\/json/)
  })

  it('可选字段原样透传', async () => {
    queueResponse(201, sampleItem)
    await requestCaptureItem({
      sourceKind: 'note',
      sourceId: 'n-9',
      title: 'T',
      summary: 'S',
      deckId: 'd1',
      importance: 4,
      stage: 'learning',
    })
    assert.deepEqual(captured[0].body, {
      sourceKind: 'note',
      sourceId: 'n-9',
      title: 'T',
      summary: 'S',
      deckId: 'd1',
      importance: 4,
      stage: 'learning',
    })
  })

  it('重复收集同一来源：服务端返回既有条目（200），客户端不视为失败', async () => {
    queueResponse(200, { ...sampleItem, updatedAt: 1790000500 })
    const item = await requestCaptureItem({ sourceKind: 'email', sourceId: 'em-42', title: 'x' })
    assert.equal(item.id, 'litem-1', 'the idempotent hit keeps the original id')
    assert.equal(item.updatedAt, 1790000500)
    assert.equal(item.capturedAt, 1790000000, 'the first capture time is never rewritten')
  })
})

// ============================================================================
// 2. GET /api/learning/items —— 列表与过滤
// ============================================================================

describe('GET /api/learning/items', () => {
  it('无过滤时不带 query，解构 items 字段', async () => {
    queueResponse(200, { items: [sampleItem] })
    const items = await requestListItems()
    const call = captured[0]
    assert.equal(call.method, 'GET')
    assert.equal(call.url, `${BASE}/items`)
    assert.equal(items.length, 1)
    assert.equal(items[0].id, 'litem-1')
  })

  it('stage / sourceKind / limit 进 query', async () => {
    queueResponse(200, { items: [] })
    await requestListItems({ stage: 'inbox', sourceKind: 'email', limit: 20 })
    const url = captured[0].url
    assert.ok(url.includes('stage=inbox'), url)
    assert.ok(url.includes('sourceKind=email'), url)
    assert.ok(url.includes('limit=20'), url)
  })

  it('缺省 items 时返回空数组而不是 undefined', async () => {
    queueResponse(200, {})
    assert.deepEqual(await requestListItems(), [])
  })
})

// ============================================================================
// 3. GET /api/learning/items/due —— 今日概览
// ============================================================================

describe('GET /api/learning/items/due', () => {
  it('路径与字段名与服务端 DueSummary 一致', async () => {
    queueResponse(200, { userId: 'u1', dueCards: 12, inbox: 3, reviewItems: 2, dueTasks: 1 })
    const summary = await requestFetchDueSummary()
    assert.equal(captured[0].url, `${BASE}/items/due`)
    assert.equal(captured[0].method, 'GET')
    assert.equal(summary.dueCards, 12)
    assert.equal(summary.inbox, 3)
    assert.equal(summary.reviewItems, 2)
    assert.equal(summary.dueTasks, 1)
  })
})

// ============================================================================
// 4. PATCH /api/learning/items/{id} —— 阶段流转
// ============================================================================

describe('PATCH /api/learning/items/{id}', () => {
  it('id 经过 URL 编码，body 只含 stage', async () => {
    queueResponse(200, { id: 'litem 1', stage: 'review' })
    await requestUpdateStage('litem 1', 'review')
    const call = captured[0]
    assert.equal(call.method, 'PATCH')
    assert.ok(call.url.includes(`${BASE}/items/litem%201`), call.url)
    assert.deepEqual(call.body, { stage: 'review' })
  })
})

// ============================================================================
// 5. 提醒：创建 / 列表 / 延后 / 确认
// ============================================================================

describe('POST /api/learning/reminders', () => {
  it('创建每日回顾提醒的 body 形状', async () => {
    queueResponse(201, sampleReminder)
    const reminder = await requestUpsertReminder({
      kind: 'daily_digest',
      ruleKind: 'daily',
      ruleValue: '20:30',
      nextDueAt: 1790000000,
    })
    assert.equal(captured[0].method, 'POST')
    assert.equal(captured[0].url, `${BASE}/reminders`)
    assert.deepEqual(captured[0].body, {
      kind: 'daily_digest',
      ruleKind: 'daily',
      ruleValue: '20:30',
      nextDueAt: 1790000000,
    })
    assert.equal(reminder.ruleValue, '20:30')
  })
})

describe('GET /api/learning/reminders', () => {
  it('state / limit 进 query，解构 reminders', async () => {
    queueResponse(200, { reminders: [sampleReminder] })
    const reminders = await requestListReminders('pending', 10)
    const url = captured[0].url
    assert.ok(url.includes('state=pending'), url)
    assert.ok(url.includes('limit=10'), url)
    assert.equal(reminders.length, 1)
  })

  it('缺省 reminders 时返回空数组', async () => {
    queueResponse(200, {})
    assert.deepEqual(await requestListReminders(), [])
  })
})

describe('POST /api/learning/reminders/{id}/snooze', () => {
  it('显式分钟数透传', async () => {
    queueResponse(200, { id: 'lrem-1', snoozedUntil: 1790001800 })
    const until = await requestSnooze('lrem-1', 30)
    assert.equal(captured[0].url, `${BASE}/reminders/lrem-1/snooze`)
    assert.equal(captured[0].method, 'POST')
    assert.deepEqual(captured[0].body, { minutes: 30 })
    assert.equal(until, 1790001800)
  })

  it('省略分钟数时发空 body，由服务端按 60 分钟处理', async () => {
    queueResponse(200, { id: 'lrem-1', snoozedUntil: 1790003600 })
    await requestSnooze('lrem-1')
    assert.deepEqual(captured[0].body, {})
  })
})

describe('POST /api/learning/reminders/{id}/ack', () => {
  it('确认提醒：POST 空 body', async () => {
    queueResponse(200, { id: 'lrem-1', state: 'acked' })
    await requestAck('lrem-1')
    assert.equal(captured[0].method, 'POST')
    assert.ok(captured[0].url.endsWith('/reminders/lrem-1/ack'), captured[0].url)
  })
})

// ============================================================================
// 6. POST /api/learning/schedule —— 服务端权威调度
// ============================================================================

describe('POST /api/learning/schedule', () => {
  it('body 形状与 ScheduleInput 一致，返回 ScheduleOutput', async () => {
    queueResponse(200, {
      state: 2,
      stability: 10.87,
      difficulty: 5.11,
      intervalDays: 10.87,
      due: 1790940000,
      relearning: false,
      reps: 4,
      lapses: 0,
    })
    const out = await requestSchedule({
      state: 2,
      stability: 10,
      difficulty: 5,
      rating: 3,
      elapsedDays: 10,
      now: 1790000000,
    })
    assert.equal(captured[0].url, `${BASE}/schedule`)
    assert.equal(captured[0].method, 'POST')
    assert.deepEqual(captured[0].body, {
      state: 2,
      stability: 10,
      difficulty: 5,
      rating: 3,
      elapsedDays: 10,
      now: 1790000000,
    })
    assert.equal(out.state, 2)
    assert.equal(out.relearning, false)
    assert.equal(out.due, 1790940000)
  })
})

// ============================================================================
// 7. 错误信封沿用全站约定
// ============================================================================

describe('error envelope', () => {
  it('服务端错误沿用 {error, code?, retryable?, request_id?}', async () => {
    queueResponse(400, { error: 'sourceKind must be one of note|email|rss|meeting|chat|manual' })
    const res = await fetch(`${BASE}/items`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sourceKind: 'sms', sourceId: 'x', title: 't' }),
    })
    assert.equal(res.status, 400)
    const envelope = (await res.json()) as ApiErrorEnvelope
    assert.match(envelope.error, /sourceKind/)
    assert.equal(typeof envelope.error, 'string')
  })
})
