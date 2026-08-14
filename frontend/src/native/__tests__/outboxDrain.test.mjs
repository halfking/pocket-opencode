/**
 * outboxDrain 测试：断网入队 → 退避 → 恢复自动重放 → 死信 / 终态。
 * 跑真实 SQLite 存储 + 真实模块；发送器用可控假实现模拟网络通/断。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import { NodeSqlDb } from './helpers.mjs'
import { SqliteOutboxStore } from '../outboxStore.ts'
import { drainOutbox } from '../outboxDrain.ts'
import { enqueue } from '../../utils/outbox.ts'

const WS = 'ws-a'

function newOutbox() {
  return new SqliteOutboxStore(new NodeSqlDb())
}

function queuedRecord(overrides = {}) {
  return enqueue(
    {
      workspaceId: WS,
      action: 'approval.reply',
      payload: { requestId: 'req_1' },
      idempotencyKey: 'appr_permission_req_1',
    },
    1000,
  )
}

test('network down: attempts stay queued with future nextAttemptAt (backoff)', async () => {
  const outbox = newOutbox()
  await outbox.put(queuedRecord())

  let clock = 1000
  const result = await drainOutbox(outbox, {
    workspaceId: WS,
    senders: {
      'approval.reply': async () => { throw new Error('network unreachable') },
    },
    now: () => clock,
  })
  assert.equal(result.retried, 1)
  assert.equal(result.succeeded, 0)

  const record = await outbox.get((await outbox.listReady(clock, 10))[0]?.id ?? '')
  // listReady 在失败后不该立刻返回（退避未到）。
  assert.equal((await outbox.listReady(clock, 10)).length, 0)

  const stored = (await outbox.countByState(['queued'])) === 1
  assert.ok(stored)
  // 退避窗口过后重新可见。
  clock += 61_000
  const ready = await outbox.listReady(clock, 10)
  assert.equal(ready.length, 1)
  assert.equal(ready[0].attempts, 1)
  assert.equal(ready[0].lastError, 'network unreachable')
})

test('network recovery: queued records auto-replay and are removed on success', async () => {
  const outbox = newOutbox()
  await outbox.put(queuedRecord())

  let networkUp = false
  const sent = []
  let clock = 1000
  const senders = {
    'approval.reply': async (record) => {
      if (!networkUp) throw new Error('network unreachable')
      sent.push(record.idempotencyKey)
      return { ok: true, cursor: 'req_1' }
    },
  }

  // 断网：失败入退避。
  await drainOutbox(outbox, { workspaceId: WS, senders, now: () => clock })
  assert.equal(await outbox.countByState(['queued']), 1)

  // 网络恢复（时间推进过退避窗口）：自动重放成功。
  networkUp = true
  clock += 61_000
  const result = await drainOutbox(outbox, { workspaceId: WS, senders, now: () => clock })
  assert.equal(result.succeeded, 1)
  assert.deepEqual(sent, ['appr_permission_req_1'])
  assert.equal(await outbox.countByState(['queued', 'succeeded', 'dead_letter']), 0)
})

test('terminal outcome (approval expired 409) goes straight to dead letter', async () => {
  const outbox = newOutbox()
  await outbox.put(queuedRecord())
  const result = await drainOutbox(outbox, {
    workspaceId: WS,
    senders: {
      'approval.reply': async () => ({ ok: false, terminal: true, errorCode: 'conflict_not_pending' }),
    },
    now: () => 1000,
  })
  assert.equal(result.deadLettered, 1)
  assert.equal(await outbox.countByState(['dead_letter']), 1)
})

test('max attempts exhausted dead-letters the record', async () => {
  const outbox = newOutbox()
  await outbox.put(queuedRecord())
  let clock = 1000
  const senders = {
    'approval.reply': async () => ({ ok: false, errorCode: 'http_502' }),
  }
  for (let i = 0; i < 8; i++) {
    const result = await drainOutbox(outbox, {
      workspaceId: WS,
      senders,
      now: () => clock,
      maxAttempts: 8,
    })
    clock += 120_000 // 跳过退避窗口
    if (i < 7) {
      assert.equal(result.retried, 1, `round ${i}`)
    } else {
      assert.equal(result.deadLettered, 1)
    }
  }
  assert.equal(await outbox.countByState(['dead_letter']), 1)
  assert.equal(await outbox.countByState(['queued']), 0)
})

test('workspace mismatch records are dropped, never sent (SEC-06)', async () => {
  const outbox = newOutbox()
  await outbox.put(
    enqueue(
      { workspaceId: 'ws-old', action: 'approval.reply', payload: {}, idempotencyKey: 'k-old' },
      1000,
    ),
  )
  const result = await drainOutbox(outbox, {
    workspaceId: WS,
    senders: {
      'approval.reply': async () => { throw new Error('must not send') },
    },
    now: () => 1000,
  })
  assert.equal(result.droppedWorkspaceMismatch, 1)
  assert.equal(await outbox.countByState(['queued', 'dead_letter']), 0)
})

test('unknown action dead-letters with no_sender', async () => {
  const outbox = newOutbox()
  await outbox.put(
    enqueue(
      { workspaceId: WS, action: 'mystery.action', payload: {}, idempotencyKey: 'k-x' },
      1000,
    ),
  )
  const result = await drainOutbox(outbox, { workspaceId: WS, senders: {}, now: () => 1000 })
  assert.equal(result.deadLetteredNoSender, 1)
  assert.equal(await outbox.countByState(['dead_letter']), 1)
})

test('records before the retry window are not claimed early', async () => {
  const outbox = newOutbox()
  await outbox.put(queuedRecord())
  await outbox.put(
    enqueue(
      {
        workspaceId: WS,
        action: 'approval.reply',
        payload: {},
        idempotencyKey: 'k-later',
        delayMs: 300_000,
      },
      1000,
    ),
  )
  const rows = await outbox.listReady(2000, 10)
  assert.equal(rows.length, 1)
  assert.equal(rows[0].idempotencyKey, 'appr_permission_req_1')
})
