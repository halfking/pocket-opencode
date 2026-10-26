/**
 * ACC 待批项的纯逻辑判据（node --test 直接跑）。
 *
 * 守护的不变式：
 *   1. 「允许一次」用的 option id 必须是智能体**给过**的；
 *   2. 找不到可允许的选项时返回 null —— 前端据此**不渲染**那个按钮
 *      （渲染一个必然被 daemon 拒的按钮是假动作）；
 *   3. 拒绝类的判定与 daemon 侧 internal/intervention.isDenyKind 一致
 *      （daemon 是按 kind 决定允许/拒绝，不是按 id）。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  accApprovalSummary,
  isAccDenyOption,
  pickAccAllowOptionId,
  pickAccRejectOptionId,
} from '../accApprovalLogic.ts'

test('isAccDenyOption: 与 daemon 的 isDenyKind 一致（kind 决定允许/拒绝）', () => {
  for (const kind of ['reject_once', 'reject_always', 'deny']) {
    assert.equal(isAccDenyOption({ id: 'x', kind }), true, `kind=${kind} 必须是拒绝`)
  }
  for (const kind of ['allow_once', 'allow_always', '', undefined]) {
    assert.equal(isAccDenyOption({ id: 'x', kind }), false, `kind=${kind} 不能算拒绝`)
  }
})

test('pickAccAllowOptionId: 优先用 kind=allow_once 的那个选项', () => {
  const item = {
    id: 'int-1',
    options: [
      { id: 'once', kind: 'allow_once' },
      { id: 'always', kind: 'allow_always' },
      { id: 'reject', kind: 'reject_once' },
    ],
  }
  assert.equal(pickAccAllowOptionId(item), 'once')
})

test('pickAccAllowOptionId: 没有 allow_once 时退到 id 形如 once/allow 的选项', () => {
  const item = { id: 'int-2', options: [{ id: 'once' }, { id: 'reject', kind: 'reject_once' }] }
  assert.equal(pickAccAllowOptionId(item), 'once')
})

test('pickAccAllowOptionId: 没有可允许的选项 ⇒ null（绝不自造 id）', () => {
  const onlyReject = { id: 'int-3', options: [{ id: 'reject', kind: 'reject_once' }] }
  assert.equal(pickAccAllowOptionId(onlyReject), null)
  const empty = { id: 'int-4', options: [] }
  assert.equal(pickAccAllowOptionId(empty), null)
  const noOptions = { id: 'int-5' }
  assert.equal(pickAccAllowOptionId(noOptions), null)
})

test('pickAccRejectOptionId: 优先 kind 是拒绝类的；退到 id 形如 reject/deny', () => {
  assert.equal(
    pickAccRejectOptionId({
      id: 'int-6',
      options: [
        { id: 'once', kind: 'allow_once' },
        { id: 'reject', kind: 'reject_once' },
      ],
    }),
    'reject',
  )
  assert.equal(
    pickAccRejectOptionId({ id: 'int-7', options: [{ id: 'once' }, { id: 'deny' }] }),
    'deny',
  )
  assert.equal(pickAccRejectOptionId({ id: 'int-8', options: [] }), null)
})

test('accApprovalSummary: title → tool_call_id → id → 占位符', () => {
  assert.equal(accApprovalSummary({ id: 'i', title: 'mkdir /tmp/x' }), 'mkdir /tmp/x')
  assert.equal(accApprovalSummary({ id: 'i', tool_call_id: 'call-9' }), 'call-9')
  assert.equal(accApprovalSummary({ id: 'int-9' }), 'int-9')
  assert.equal(accApprovalSummary({ id: '' }), '（未描述的审批请求）')
})
