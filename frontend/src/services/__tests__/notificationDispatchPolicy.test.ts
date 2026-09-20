/**
 * notificationDispatchPolicy 单测(2026-09-20 通知体系 P1)。
 *
 * 覆盖决策表:
 *   describeEvent — 五类事件的文案/回跳/宿主页;中间态返回 null。
 *   decideSurfacing — 宿主页 none / 前台 toast / 后台 system /
 *                     scheduledtask.succeeded 前台静默。
 */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  describeEvent,
  decideSurfacing,
  type EventDescriptor,
} from '../notificationDispatchPolicy.ts'

test('describeEvent: notification 推送取后端标题并指向通知中心', () => {
  const d = describeEvent('notification', {
    id: 'n1', title: '重要邮件', body: '来自老板', priority: 'high', read_at: 0, created_at: 1,
  })
  assert.ok(d)
  assert.equal(d!.title, '重要邮件')
  assert.equal(d!.body, '来自老板')
  assert.equal(d!.deepLink, '/notifications')
  assert.deepEqual(d!.hostPrefixes, ['/notifications'])
})

test('describeEvent: scheduledtask.failed 带错误摘要并指向定时任务页', () => {
  const d = describeEvent('scheduledtask.failed', { taskId: 't1', runId: 'r1', error: 'boom' })
  assert.ok(d)
  assert.equal(d!.title, '定时任务失败')
  assert.match(d!.body, /boom/)
  assert.equal(d!.deepLink, '/settings/scheduled-tasks')
})

test('describeEvent: scheduledtask 中间态(started/skipped)不感知', () => {
  assert.equal(describeEvent('scheduledtask.started', {}), null)
  assert.equal(describeEvent('scheduledtask.skipped', {}), null)
})

test('describeEvent: round.completed 生成会话回跳(含 instance_id query)', () => {
  const d = describeEvent('round.completed', {
    instance_id: 'inst-1', session_id: 'sess-1',
    round_index: 2, summary: '改了三个文件', status: 'completed',
  })
  assert.ok(d)
  assert.equal(d!.deepLink, '/sessions/sess-1?instance_id=inst-1')
  assert.deepEqual(d!.hostPrefixes, ['/sessions/sess-1'])
  // 出错轮次换标题
  const err = describeEvent('round.completed', {
    instance_id: 'inst-1', session_id: 'sess-1', status: 'error',
  })
  assert.equal(err!.title, 'AI 任务出错')
})

test('describeEvent: approval pending 解内层信封并指向会话审批', () => {
  const inner = {
    v: 1, type: 'approval.permission.pending',
    data: { instance_id: 'inst-1', session_id: 'sess-1', request: { id: 'req-1' } },
    cause: { approval_id: 'ap-1' },
  }
  const d = describeEvent('approval.permission.pending', inner)
  assert.ok(d)
  assert.equal(d!.deepLink, '/sessions/sess-1?instance_id=inst-1&approval=open')
  assert.ok(d!.hostPrefixes.includes('/tasks'))
  // question 变体
  const q = describeEvent('approval.question.pending', inner)
  assert.equal(q!.title, 'AI 在等你回答')
})

test('describeEvent: 同 seed 产生稳定 localId,不同事件不碰撞', () => {
  const a = describeEvent('scheduledtask.failed', { runId: 'r1' }, 'env-1')
  const b = describeEvent('scheduledtask.failed', { runId: 'r1' }, 'env-1')
  const c = describeEvent('scheduledtask.failed', { runId: 'r2' }, 'env-2')
  assert.equal(a!.localId, b!.localId)
  assert.notEqual(a!.localId, c!.localId)
})

// ---- decideSurfacing ----

const desc = (hostPrefixes: string[]): EventDescriptor => ({
  title: 't', body: 'b', deepLink: '/x', hostPrefixes, localId: 1,
})

test('decideSurfacing: 前台 + 宿主页 → none', () => {
  assert.equal(decideSurfacing('round.completed', '/sessions/sess-1', false, desc(['/sessions/sess-1'])), 'none')
  // 宿主子路径同样视为在场
  assert.equal(decideSurfacing('notification', '/notifications', false, desc(['/notifications'])), 'none')
})

test('decideSurfacing: 前台 + 非宿主页 → toast', () => {
  assert.equal(decideSurfacing('round.completed', '/email', false, desc(['/sessions/sess-1'])), 'toast')
})

test('decideSurfacing: 后台 → system(即使在宿主页,系统通知仍送达)', () => {
  assert.equal(decideSurfacing('round.completed', '/sessions/sess-1', true, desc(['/sessions/sess-1'])), 'system')
  assert.equal(decideSurfacing('notification', '/email', true, desc(['/notifications'])), 'system')
})

test('decideSurfacing: scheduledtask.succeeded 前台静默(防周期任务刷屏)', () => {
  assert.equal(decideSurfacing('scheduledtask.succeeded', '/email', false, desc(['/settings/scheduled-tasks'])), 'none')
  // 后台仍然送达(任务完成系统通知是明确需求)
  assert.equal(decideSurfacing('scheduledtask.succeeded', '/email', true, desc(['/settings/scheduled-tasks'])), 'system')
})
