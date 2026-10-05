/**
 * accBinding 透传测试（Node TAP 风格，node --test 直接运行）。
 *
 * 验证 Pocket↔ACC canonical ID 绑定在审批调用 payload 中的透传：
 *   1. accBindingPassthrough：Task 绑定字段 → snake_case payload 键，
 *      空值省略、空绑定/缺省不产生任何键；
 *   2. accBindingOf：Task 形状对象 → 绑定视图；
 *   3. 真实 payload 链路：outbox approval.reply sender（离线重放与在线
 *      replyPermissionFlat 同一后端契约）发出的 permission body 必须携带
 *      acc_task_id / acc_dispatch_id 等绑定键；未绑定时完全不带 acc_* 键
 *      （后端 DisallowUnknownFields，未知键会 400）。
 *
 * 刻意不 import api/approvals.ts（其 http.ts → stores/auth → pinia 链在
 * node:test 下不可加载）；payload 键契约由零依赖的 accBinding.ts 与
 * node 安全的 outboxDrain.ts 覆盖。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import { accBindingOf, accBindingPassthrough } from '../../../api/accBinding.ts'
import { createMobileOutboxSenders } from '../../../native/outboxDrain.ts'

const FULL_BINDING = {
  accTaskId: 'acc-task-9',
  accRunId: 'run-3',
  accDispatchId: 'disp-42',
  accSourceRef: 'pocket://ws-a/task-7',
  accCorrelationId: 'corr-77',
}

test('accBindingPassthrough: 完整绑定 → 全部 snake_case 键', () => {
  assert.deepEqual(accBindingPassthrough(FULL_BINDING), {
    acc_task_id: 'acc-task-9',
    acc_run_id: 'run-3',
    acc_dispatch_id: 'disp-42',
    acc_source_ref: 'pocket://ws-a/task-7',
    acc_correlation_id: 'corr-77',
  })
})

test('accBindingPassthrough: 部分绑定 → 只带非空键', () => {
  assert.deepEqual(accBindingPassthrough({ accDispatchId: 'disp-1' }), {
    acc_dispatch_id: 'disp-1',
  })
})

test('accBindingPassthrough: 空字符串/空对象/null/缺省 → 空 payload', () => {
  assert.deepEqual(accBindingPassthrough({ accTaskId: '', accRunId: undefined }), {})
  assert.deepEqual(accBindingPassthrough({}), {})
  assert.deepEqual(accBindingPassthrough(null), {})
  assert.deepEqual(accBindingPassthrough(undefined), {})
})

test('accBindingOf: Task 形状对象 → 绑定视图（剔除空值）', () => {
  const task = {
    id: 'task-7',
    source: 'acc',
    ...FULL_BINDING,
    accRunId: undefined,
  }
  assert.deepEqual(accBindingOf(task), {
    accTaskId: 'acc-task-9',
    accDispatchId: 'disp-42',
    accSourceRef: 'pocket://ws-a/task-7',
    accCorrelationId: 'corr-77',
  })
  assert.deepEqual(accBindingOf({ id: 'local-1' }), {})
  assert.deepEqual(accBindingOf(null), {})
})

/** 真实 outbox sender 的假 doFetch：捕获 body，回 200。 */
function captureSender() {
  const seen = []
  const senders = createMobileOutboxSenders({
    doFetch: async (_url, init) => {
      seen.push({ url: _url, body: JSON.parse(init.body) })
      return { status: 200, json: async () => ({}) }
    },
    syncStore: {},
  })
  return { senders, seen }
}

test('outbox approval.reply: permission body 携带 ACC 绑定', async () => {
  const { senders, seen } = captureSender()
  const outcome = await senders['approval.reply']({
    idempotencyKey: 'appr_permission_req_1',
    payload: {
      kind: 'permission',
      requestId: 'req_1',
      instanceId: 'inst-1',
      sessionId: 'sess-1',
      decision: 'once',
      ...FULL_BINDING,
    },
  })
  assert.equal(outcome.ok, true)
  assert.equal(seen.length, 1)
  const body = seen[0].body
  assert.equal(body.decision, 'once')
  assert.equal(body.acc_task_id, 'acc-task-9')
  assert.equal(body.acc_run_id, 'run-3')
  assert.equal(body.acc_dispatch_id, 'disp-42')
  assert.equal(body.acc_source_ref, 'pocket://ws-a/task-7')
  assert.equal(body.acc_correlation_id, 'corr-77')
})

test('outbox approval.reply: 未绑定任务的 permission body 不带任何 acc_* 键', async () => {
  const { senders, seen } = captureSender()
  const outcome = await senders['approval.reply']({
    idempotencyKey: 'appr_permission_req_2',
    payload: {
      kind: 'permission',
      requestId: 'req_2',
      instanceId: 'inst-1',
      sessionId: 'sess-1',
      decision: 'reject',
    },
  })
  assert.equal(outcome.ok, true)
  const body = seen[0].body
  assert.equal(body.decision, 'reject')
  for (const key of Object.keys(body)) {
    assert.ok(!key.startsWith('acc_'), `unexpected ${key} in unbound payload`)
  }
})

test('outbox approval.reply: question 回复不透传 acc_*（后端未契约化）', async () => {
  const { senders, seen } = captureSender()
  const outcome = await senders['approval.reply']({
    idempotencyKey: 'appr_question_req_3',
    payload: {
      kind: 'question',
      requestId: 'req_3',
      instanceId: 'inst-1',
      sessionId: 'sess-1',
      answers: [['a']],
      ...FULL_BINDING,
    },
  })
  assert.equal(outcome.ok, true)
  const body = seen[0].body
  assert.deepEqual(body.answers, [['a']])
  for (const key of Object.keys(body)) {
    assert.ok(!key.startsWith('acc_'), `unexpected ${key} in question payload`)
  }
})
