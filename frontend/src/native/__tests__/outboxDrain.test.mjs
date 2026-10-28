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

test('长期暂停会话同步不会堵住排在后面的审批回复', async () => {
  const outbox = newOutbox()
  for (let i = 0; i < 20; i++) {
    await outbox.put(enqueue({ workspaceId: WS, action: 'session.prompt', payload: {}, idempotencyKey: `held-${i}` }, 1000 + i))
  }
  await outbox.put(enqueue({ workspaceId: WS, action: 'approval.reply', payload: {}, idempotencyKey: 'approval' }, 1100))
  const result = await drainOutbox(outbox, {
    workspaceId: WS, now: () => 2000, batchSize: 16,
    skipActions: ['session.prompt'],
    senders: { 'approval.reply': async () => ({ ok: true }) },
  })
  assert.equal(result.succeeded, 1)
  assert.equal(await outbox.countByState(['queued']), 20)
})

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

/* ═══════════════════════════════════════════════════════════════════════════
 * §878：一条损坏的 payload 曾经能**永久堵死整个离线队列**。
 *
 * 缺陷形态（修之前）：`toRecord` 里裸 `JSON.parse`，而 `listReady` 的调用方
 * `drainOutbox` 外面没有 try/catch，`listReady` 又是 `ORDER BY created_at ASC`。
 * ⇒ 排在队首的一条坏 payload，让**每一次** drain 整个 reject，
 *   session.create / session.prompt / approval.reply 全部停摆，且零提示。
 * ═══════════════════════════════════════════════════════════════════════════ */

/** 直接往表里塞一条 payload 损坏的行（模拟写坏/截断/跨版本 schema）。 */
async function injectCorruptRow(db, id, createdAt) {
  await db.run(
    `INSERT INTO local_outbox
       (id, idempotency_key, workspace_id, action, payload,
        created_at, next_attempt_at, attempts, cursor, last_error, state, ttl_ms)
     VALUES (?,?,?,?,?,?,?,0,NULL,NULL,'queued',?)`,
    // ⚠ 少一个右括号：这是**真实的 JSON 截断**形态，不是随手写的乱码。
    [id, id, WS, 'approval.reply', '{"requestId":"req_1"', createdAt, createdAt, 86_400_000],
  )
}

/** 捕获 console.warn，用来证明判据区分得开「有损坏」与「没损坏」。 */
async function captureWarnings(fn) {
  const original = console.warn
  const seen = []
  console.warn = (...args) => void seen.push(args.join(' '))
  try {
    const value = await fn()
    return { value, seen }
  } finally {
    console.warn = original
  }
}

test('§878 一条坏 payload 不得堵死 drain（同批的其它记录照常重放）', async () => {
  const db = new NodeSqlDb()
  const outbox = new SqliteOutboxStore(db)

  // ⚠ 坏行排在**队首**（created_at 更小）—— 这正是「永久堵死」的形状。
  await injectCorruptRow(db, 'corrupt-1', 500)
  await outbox.put(queuedRecord()) // 队尾，createdAt = 1000

  const sent = []
  const { value: result, seen } = await captureWarnings(() =>
    drainOutbox(outbox, {
      workspaceId: WS,
      senders: {
        'approval.reply': async (record) => {
          sent.push(record.idempotencyKey)
          return { ok: true, cursor: 'req_1' }
        },
      },
      now: () => 1000,
    }),
  )

  assert.equal(result.succeeded, 1, '坏行连累了同批的好行 ⇒ 整个离线队列被一条数据堵死')
  assert.deepEqual(sent, ['appr_permission_req_1'])
  assert.equal(
    seen.some((w) => w.includes('local_outbox') && w.includes('corrupt-1')),
    true,
    `跳过了坏行却没有任何告警 ⇒ 下一个人无法知道队列里躺着一条坏数据（实际：${JSON.stringify(seen)}）`,
  )
})

test('§878 损坏的行必须留在表里（原始字节是证据，不许销毁）', async () => {
  const db = new NodeSqlDb()
  const outbox = new SqliteOutboxStore(db)
  await injectCorruptRow(db, 'corrupt-1', 500)
  await outbox.put(queuedRecord())

  await captureWarnings(() =>
    drainOutbox(outbox, {
      workspaceId: WS,
      senders: { 'approval.reply': async () => ({ ok: true, cursor: 'req_1' }) },
      now: () => 1000,
    }),
  )

  const rows = await db.all('SELECT id, payload FROM local_outbox')
  assert.equal(rows.length, 1, '好行已发送后应只剩坏行')
  assert.equal(rows[0].id, 'corrupt-1')
  assert.equal(rows[0].payload, '{"requestId":"req_1"',
    '坏行的原始字节被改写了 ⇒ 证据没了（§876「损坏不许覆写」是同一条原则）')
})

test('§878 点查不得把损坏报成「没有这一行」（§876/D45 的形态）', async () => {
  const db = new NodeSqlDb()
  const outbox = new SqliteOutboxStore(db)
  await injectCorruptRow(db, 'corrupt-1', 500)

  // ⚠ 断言错误标记本身：`assert.rejects` 接受任何 reject（§876 M33）。
  await assert.rejects(
    () => outbox.get('corrupt-1'),
    /outbox_corrupt/,
    'get 返回 null 会让调用方以为「这条记录不存在」—— 谎报',
  )
  // 不存在的 id 仍然老老实实返回 null（不能一律抛）。
  assert.equal(await outbox.get('never-existed'), null)
})

test('§878 对照组：数据全好时读数必须不同（判据有牙，不是碰巧绿）', async () => {
  const db = new NodeSqlDb()
  const outbox = new SqliteOutboxStore(db)
  await outbox.put(queuedRecord())

  const sent = []
  const { value: result, seen } = await captureWarnings(() =>
    drainOutbox(outbox, {
      workspaceId: WS,
      senders: {
        'approval.reply': async (record) => {
          sent.push(record.idempotencyKey)
          return { ok: true, cursor: 'req_1' }
        },
      },
      now: () => 1000,
    }),
  )

  assert.equal(result.succeeded, 1)
  assert.equal(sent.length, 1)
  assert.deepEqual(seen, [], '数据完好时不该有任何「损坏」告警 —— 否则告警就是噪声')
})
