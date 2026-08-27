import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  PHASE_LABELS,
  activityFromSnapshot,
  applyRoundCompletedEvent,
  applySessionActivityEvent,
  applySnapshotRow,
  buildSessionMarkdown,
  countRoundEvents,
  deriveFallbackPhase,
  groupMessagesIntoRounds,
  newerActivityWins,
  phaseFromToolName,
  roundKey,
  roundSummaryFallback,
  statsFromMessages,
  statsFromRounds,
} from '../useSessionEvents.ts'

const SCOPE = { sessionId: 'sess-1', instanceId: 'inst-1' }

/** 构造 idempotentWsBus 归一化后的 env（外层 {type,data}，data 即 WsEnvelopeV1）。 */
function activityEnv(payload, { type = 'session.activity', id = 'evt-1' } = {}) {
  return {
    type,
    data: {
      v: 1,
      id,
      ts: payload.last_event_at ?? 0,
      channel: 'sessions',
      topic: payload.session_id ?? 'sess-1',
      type,
      data: payload,
    },
  }
}

function roundEnv(payload, { id = 'round-evt-1' } = {}) {
  return activityEnv(payload, { type: 'round.completed', id })
}

function msg(id, role, extra = {}) {
  return { id, role, text: '', time: 0, ...extra }
}

// ── session.activity 幂等：最新胜出 ──

test('newerActivityWins：last_event_at 大者胜，相等时 round_index 高者胜', () => {
  const cur = { phase: 'thinking', lastEventAt: 1000, roundIndex: 2 }
  assert.equal(newerActivityWins(null, { last_event_at: 1, round_index: 0 }), true)
  assert.equal(newerActivityWins(cur, { last_event_at: 999, round_index: 9 }), false)
  assert.equal(newerActivityWins(cur, { last_event_at: 1000, round_index: 1 }), false)
  // 相同 last_event_at、round_index 相同 → 允许覆盖（phase 切换事件时间戳可能相同）
  assert.equal(newerActivityWins(cur, { last_event_at: 1000, round_index: 2 }), true)
  assert.equal(newerActivityWins(cur, { last_event_at: 1001, round_index: 0 }), true)
})

test('applySessionActivityEvent：过滤 scope + 乱序旧事件不回退', () => {
  const env = activityEnv({
    instance_id: 'inst-1',
    session_id: 'sess-1',
    phase: 'file_write',
    last_event_at: 5000,
    round_index: 3,
  })
  const state = applySessionActivityEvent(null, env, SCOPE)
  assert.deepEqual(state, { phase: 'file_write', lastEventAt: 5000, roundIndex: 3 })

  // 旧事件（last_event_at 更小）不得回退状态
  const older = activityEnv(
    { instance_id: 'inst-1', session_id: 'sess-1', phase: 'thinking', last_event_at: 4000, round_index: 2 },
    { id: 'evt-old' },
  )
  assert.equal(applySessionActivityEvent(state, older, SCOPE), state)

  // 其他会话 / 其他实例的事件忽略
  const otherSession = activityEnv(
    { instance_id: 'inst-1', session_id: 'sess-2', phase: 'tool', last_event_at: 9000, round_index: 9 },
    { id: 'evt-other' },
  )
  assert.equal(applySessionActivityEvent(state, otherSession, SCOPE), state)
  const otherInstance = activityEnv(
    { instance_id: 'inst-2', session_id: 'sess-1', phase: 'tool', last_event_at: 9000, round_index: 9 },
    { id: 'evt-other-inst' },
  )
  assert.equal(applySessionActivityEvent(state, otherInstance, SCOPE), state)

  // 结构损坏的事件保持原状态
  assert.equal(applySessionActivityEvent(state, { type: 'garbage' }, SCOPE), state)
  assert.equal(applySessionActivityEvent(state, null, SCOPE), state)
})

// ── round.completed 幂等：session_id+round_index 去重覆盖 ──

function roundPayload(roundIndex, summary, status = 'completed') {
  return {
    instance_id: 'inst-1',
    session_id: 'sess-1',
    round_index: roundIndex,
    summary,
    changes: { added: 3, removed: 1, files: 2 },
    status,
    completed_at: 1234567890123,
  }
}

test('applyRoundCompletedEvent：同轮重发覆盖，异轮并存，scope 过滤', () => {
  const r1 = roundPayload(1, '第一轮')
  const map1 = applyRoundCompletedEvent(new Map(), roundEnv(r1), SCOPE)
  assert.equal(map1.size, 1)
  assert.equal(map1.get('sess-1:1').summary, '第一轮')

  const r1b = roundPayload(1, '第一轮（修订）')
  const map2 = applyRoundCompletedEvent(map1, roundEnv(r1b, { id: 'round-evt-2' }), SCOPE)
  assert.equal(map2.size, 1)
  assert.equal(map2.get('sess-1:1').summary, '第一轮（修订）')

  const r2 = roundPayload(2, '第二轮')
  const map3 = applyRoundCompletedEvent(map2, roundEnv(r2, { id: 'round-evt-3' }), SCOPE)
  assert.equal(map3.size, 2)
  assert.ok(map3.has('sess-1:2'))

  // 其他会话事件忽略
  const foreign = roundEnv(
    { ...roundPayload(9, '别家会话'), session_id: 'sess-2' },
    { id: 'round-evt-4' },
  )
  assert.equal(applyRoundCompletedEvent(map3, foreign, SCOPE), map3)
})

test('roundKey：session_id + round_index 复合键', () => {
  assert.equal(roundKey('sess-1', 3), 'sess-1:3')
})

// ── 快照追赶 ──

test('applySnapshotRow：快照 phase 更新鲜才覆盖；latest_round 写入', () => {
  const current = { phase: 'tool', lastEventAt: 100, roundIndex: 1 }
  const staleRow = {
    instance_id: 'inst-1',
    session_id: 'sess-1',
    health: null,
    phase: 'thinking',
    round_index: 1,
    last_event_at: 50,
    latest_round: null,
  }
  assert.equal(applySnapshotRow(current, new Map(), staleRow).activity, current)

  const freshRow = {
    ...staleRow,
    phase: 'idle',
    round_index: 2,
    last_event_at: 500,
    latest_round: roundPayload(2, '快照轮'),
  }
  const merged = applySnapshotRow(current, new Map(), freshRow)
  assert.deepEqual(merged.activity, { phase: 'idle', lastEventAt: 500, roundIndex: 2 })
  assert.equal(merged.rounds.get('sess-1:2').summary, '快照轮')

  // phase=null（会话无活跃事件）不覆盖既有 activity
  const idleRow = { ...staleRow, phase: null, last_event_at: null, latest_round: null }
  assert.equal(applySnapshotRow(current, new Map(), idleRow).activity, current)
})

test('activityFromSnapshot：phase null → null', () => {
  assert.equal(activityFromSnapshot({ phase: null, round_index: 1, last_event_at: null }), null)
  assert.deepEqual(
    activityFromSnapshot({ phase: 'pty', round_index: 4, last_event_at: 42 }),
    { phase: 'pty', lastEventAt: 42, roundIndex: 4 },
  )
})

// ── 轮次分组（契约 round_index 同规则：1-based 用户消息序数） ──

test('groupMessagesIntoRounds：用户 prompt 开轮，编号 1-based', () => {
  const groups = groupMessagesIntoRounds([
    msg('u1', 'user', { text: '帮我改登录页' }),
    msg('a1', 'assistant', { text: '好的' }),
    msg('a2', 'assistant', { text: '', content: [{ type: 'tool', state: 'completed' }] }),
    msg('u2', 'user', { text: '跑下测试' }),
    msg('a3', 'assistant', { text: '通过' }),
  ])
  assert.equal(groups.length, 2)
  assert.equal(groups[0].index, 1)
  assert.equal(groups[0].messages.length, 3)
  assert.equal(groups[1].index, 2)
  assert.equal(groups[1].messages.length, 2)
})

test('groupMessagesIntoRounds：首条用户消息前的杂散消息并入轮 1，不额外开轮', () => {
  const groups = groupMessagesIntoRounds([
    msg('s0', 'system', { text: 'session created' }),
    msg('u1', 'user', { text: 'hi' }),
    msg('a1', 'assistant', { text: 'hello' }),
  ])
  assert.equal(groups.length, 1)
  assert.equal(groups[0].index, 1)
  assert.equal(groups[0].messages.length, 3)
})

test('groupMessagesIntoRounds：无用户消息时整体归轮 1；空消息为空数组', () => {
  const only = groupMessagesIntoRounds([msg('a1', 'assistant', { text: 'x' })])
  assert.equal(only.length, 1)
  assert.equal(only[0].index, 1)
  assert.deepEqual(groupMessagesIntoRounds([]), [])
})

test('countRoundEvents：assistant 文本块 + content 项计数，用户/系统不计', () => {
  const group = groupMessagesIntoRounds([
    msg('u1', 'user', { text: 'hi' }),
    msg('a1', 'assistant', { text: '正文', content: [{ type: 'tool' }, { type: 'text' }] }),
    msg('s1', 'system', { text: 'note' }),
  ])[0]
  // 文本块 1 + content 2 项 = 3
  assert.equal(countRoundEvents(group), 3)
})

test('roundSummaryFallback：取最后一条 assistant 文本首行并截断', () => {
  const group = {
    index: 1,
    messages: [
      msg('u1', 'user', { text: '改一下' }),
      msg('a1', 'assistant', { text: '第一段' }),
      msg('a2', 'assistant', { text: '结论在最后一行\n后面还有' }),
    ],
  }
  assert.equal(roundSummaryFallback(group), '结论在最后一行')

  const long = {
    index: 1,
    messages: [msg('u1', 'user', { text: 'x'.repeat(100) })],
  }
  assert.equal(roundSummaryFallback(long).length, 61)
  assert.ok(roundSummaryFallback(long).endsWith('…'))
})

// ── 断线降级推导 ──

test('deriveFallbackPhase：非流式 idle；流式按 running 工具细分', () => {
  assert.equal(deriveFallbackPhase({ streaming: false, messages: [] }), 'idle')
  assert.equal(deriveFallbackPhase({ streaming: true, messages: [] }), 'thinking')
  const streaming = [
    msg('u1', 'user', { text: 'go' }),
    msg('a1', 'assistant', {
      text: '…',
      content: [{ type: 'tool', state: 'running', name: 'edit' }],
    }),
  ]
  assert.equal(deriveFallbackPhase({ streaming: true, messages: streaming }), 'file_write')
  const bash = [
    msg('u1', 'user', { text: 'go' }),
    msg('a1', 'assistant', {
      text: '…',
      content: [{ type: 'tool', state: 'running', name: 'bash' }],
    }),
  ]
  assert.equal(deriveFallbackPhase({ streaming: true, messages: bash }), 'pty')
  const done = [
    msg('u1', 'user', { text: 'go' }),
    msg('a1', 'assistant', { text: '正文', content: [{ type: 'tool', state: 'completed' }] }),
  ]
  assert.equal(deriveFallbackPhase({ streaming: true, messages: done }), 'thinking')
})

test('phaseFromToolName / PHASE_LABELS：契约冻结的 phase 文案', () => {
  assert.equal(phaseFromToolName('edit'), 'file_write')
  assert.equal(phaseFromToolName('write_file'), 'file_write')
  assert.equal(phaseFromToolName('bash'), 'pty')
  assert.equal(phaseFromToolName('grep'), 'tool')
  assert.equal(phaseFromToolName(undefined), 'tool')
  assert.deepEqual(PHASE_LABELS, {
    thinking: '思考中',
    tool: '执行工具',
    file_write: '改文件中',
    pty: '跑命令',
    idle: '空闲',
  })
})

// ── 统计（详情抽屉） ──

test('statsFromRounds：按轮累计', () => {
  const rounds = [
    { changes: { added: 3, removed: 1, files: 2 } },
    { changes: { added: 10, removed: 4, files: 1 } },
  ]
  assert.deepEqual(statsFromRounds(rounds, 7), {
    added: 13,
    removed: 5,
    files: 3,
    messageCount: 7,
  })
})

test('statsFromMessages：从工具输出 diff 解析累计，消息数=流长度', () => {
  const diff = [
    'diff --git a/x.ts b/x.ts',
    '@@ -1,2 +1,3 @@',
    ' ctx',
    '-old',
    '+new',
    '+new2',
    'diff --git a/y.ts b/y.ts',
    '@@ -1,1 +1,1 @@',
    '-a',
    '+b',
  ].join('\n')
  const messages = [
    msg('u1', 'user', { text: 'go' }),
    msg('a1', 'assistant', {
      content: [{ type: 'tool', state: 'completed', output: { diff } }],
    }),
    msg('a2', 'assistant', {
      content: [{ type: 'tool', state: 'completed', output: '纯文本无 diff' }],
    }),
  ]
  assert.deepEqual(statsFromMessages(messages), {
    added: 3,
    removed: 2,
    files: 2,
    messageCount: 3,
  })
})

// ── 导出 ──

test('buildSessionMarkdown：统计 + 轮次摘要成稿', () => {
  const md = buildSessionMarkdown({
    title: '登录页改造',
    sessionId: 'sess-1',
    stats: { added: 13, removed: 5, files: 3, messageCount: 7 },
    rounds: [
      { index: 1, data: roundPayload(1, '完成登录页'), fallbackSummary: '' },
      { index: 2, data: null, fallbackSummary: '跑测试通过' },
    ],
  })
  assert.ok(md.includes('# 登录页改造'))
  assert.ok(md.includes('+13 行'))
  assert.ok(md.includes('-5 行'))
  assert.ok(md.includes('7 条'))
  assert.ok(md.includes('轮 1 [completed] +3/-1 · 2 文件：完成登录页'))
  assert.ok(md.includes('轮 2：跑测试通过'))
})
