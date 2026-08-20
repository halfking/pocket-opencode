/**
 * parseApprovalEvent tests — 后端审批推送事件（approval_broadcaster.go 经
 * WS hub {type, payload} 下发）到前端 ApprovalEventInfo 的解析。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  APPROVAL_EVENT_TYPES,
  parseApprovalEvent,
} from '../../services/approvalEvents.ts'

// 服务端实际下发的形状：外层 hub 信封 {type, payload}，payload 是 WsEnvelopeV1。
const permissionPending = {
  v: 0,
  type: 'approval.permission.pending',
  data: {
    v: 1,
    id: 'approval_1_1',
    ts: 1755251400000,
    channel: 'approvals',
    topic: 'inst-a',
    type: 'approval.permission.pending',
    data: {
      instance_id: 'inst-a',
      session_id: 'ses-1',
      request: { id: 'per-1', sessionID: 'ses-1', action: 'bash', resources: ['ls'] },
    },
    cause: { approval_id: 'per-1' },
  },
}

const questionPending = {
  v: 0,
  type: 'approval.question.pending',
  data: {
    v: 1,
    id: 'approval_2_1',
    ts: 1755251400000,
    channel: 'approvals',
    topic: 'inst-a',
    type: 'approval.question.pending',
    data: {
      instance_id: 'inst-a',
      session_id: 'ses-1',
      request: { id: 'que-1', sessionID: 'ses-1', questions: [] },
    },
    cause: { approval_id: 'que-1' },
  },
}

const resolved = {
  v: 0,
  type: 'approval.resolved',
  data: {
    v: 1,
    id: 'approval_3_1',
    ts: 1755251400000,
    channel: 'approvals',
    topic: 'inst-a',
    type: 'approval.resolved',
    data: {
      instance_id: 'inst-a',
      session_id: 'ses-1',
      kind: 'permission',
      request_id: 'per-1',
      resolution: 'approved',
      decision: 'once',
    },
    cause: { approval_id: 'per-1' },
  },
}

test('APPROVAL_EVENT_TYPES 与后端事件常量一一对应', () => {
  assert.deepEqual([...APPROVAL_EVENT_TYPES], [
    'approval.permission.pending',
    'approval.question.pending',
    'approval.resolved',
  ])
})

test('permission pending：从 request.id 提取 requestId，kind=permission', () => {
  const info = parseApprovalEvent(permissionPending)
  assert.ok(info)
  assert.equal(info.instanceId, 'inst-a')
  assert.equal(info.sessionId, 'ses-1')
  assert.equal(info.requestId, 'per-1')
  assert.equal(info.kind, 'permission')
  assert.equal(info.resolution, undefined)
})

test('question pending：kind=question', () => {
  const info = parseApprovalEvent(questionPending)
  assert.ok(info)
  assert.equal(info.requestId, 'que-1')
  assert.equal(info.kind, 'question')
})

test('resolved：从 request_id 提取，保留 resolution', () => {
  const info = parseApprovalEvent(resolved)
  assert.ok(info)
  assert.equal(info.requestId, 'per-1')
  assert.equal(info.kind, 'permission')
  assert.equal(info.resolution, 'approved')
})

test('approvalId 优先取 cause.approval_id，缺失回退 requestId', () => {
  assert.equal(parseApprovalEvent(permissionPending)?.approvalId, 'per-1')
  const noCause = structuredClone(permissionPending)
  delete noCause.data.cause
  assert.equal(parseApprovalEvent(noCause)?.approvalId, 'per-1')
})

test('结构不符返回 null', () => {
  assert.equal(parseApprovalEvent(null), null)
  assert.equal(parseApprovalEvent(undefined), null)
  assert.equal(parseApprovalEvent('approval.permission.pending'), null)
  assert.equal(parseApprovalEvent({ v: 0, type: 'approval.permission.pending', data: null }), null)
  assert.equal(parseApprovalEvent({ v: 0, type: 'other.event', data: { data: {} } }), null)
})

test('缺 instance_id / session_id / requestId 任一字段返回 null', () => {
  for (const field of ['instance_id', 'session_id']) {
    const broken = structuredClone(permissionPending)
    delete broken.data.data[field]
    assert.equal(parseApprovalEvent(broken), null, `missing ${field}`)
  }
  const noIds = structuredClone(permissionPending)
  delete noIds.data.data.request
  assert.equal(parseApprovalEvent(noIds), null)
})
