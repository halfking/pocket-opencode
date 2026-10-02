// account-stamp-units.test.mjs — 需求 8：`local_email_accounts.updated_at`
// 的单位必须恒为 Unix 秒，否则 LWW 守卫会被静默架空。
//
// 背景见 handoff §7dg。实测（当时用 planAccountSync 跑出来的）：
//   server updated_at (s) = 1789480000
//   client updated_at (ms)= 1789480000000
//   plan = {"pullIds":[],"pushIds":["acc-1"]}
//   服务端守卫 `stored <= base` => 1789480000 <= 1789480000000 => true
// 也就是说不管服务端那份是不是更新的，这个写都会被接受。
//
// 修法分两半，缺一不可：
//   写侧 saveAccount 改成秒（挡住增量）
//   读侧 rowToAccount 归一（救**已经写进库的历史行**）
// 只有写侧的话，存量毫秒值仍会经读路径流进 planAccountSync。

import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { normalizeAccountStamp, planAccountSync } from '../account-lww.ts'

const SEC = 1789480000

test('seconds pass through unchanged', () => {
  for (const v of [1, SEC, SEC + 5, 1_000_000_000_000 - 1]) {
    assert.equal(normalizeAccountStamp(v), v, `seconds must not change: ${v}`)
  }
})

test('milliseconds are divided down to seconds', () => {
  assert.equal(normalizeAccountStamp(SEC * 1000), SEC)
  assert.equal(normalizeAccountStamp(SEC * 1000 + 999), SEC, 'ms remainder must be floored, not rounded')
  // 阈值之上才算毫秒（与服务端 server_since.go 的 parseSinceQuery 同阈值）
  assert.equal(normalizeAccountStamp(1_000_000_000_001), 1_000_000_000)
})

test('empty and invalid stamps become 0 rather than NaN', () => {
  for (const v of [null, undefined, 0, -1, -1789480000, NaN, Infinity, -Infinity]) {
    assert.equal(normalizeAccountStamp(v), 0, `want 0 for ${String(v)}`)
  }
  // 非数字类型不该让整条同步链在运行期炸掉
  assert.equal(normalizeAccountStamp('1789480000'), 0)
})

// 这条是**端到端**判据：把归一后的值喂进真实的 planAccountSync，
// 确认上行基准回到秒的量级。若 rowToAccount 忘了调归一（或调错），
// 它会转红。
test('normalized stamp keeps the LWW guard meaningful', () => {
  const remoteStamp = SEC // 服务端那份
  const localMs = SEC * 1000 // 设备上用 saveAccount 写下的毫秒值

  const plan = planAccountSync(
    [{ id: 'acct-1', emailAddress: 'a@x.com', updatedAt: normalizeAccountStamp(localMs) }],
    [{ id: 'acct-1', emailAddress: 'a@x.com', updatedAt: remoteStamp }],
  )
  assert.deepStrictEqual(plan.pushIds, [], '归一后本地不再比服务端「新」，不应上行')

  // 未归一时会怎样：留作对照，说明这条断言不是恒真。
  const broken = planAccountSync(
    [{ id: 'acct-1', emailAddress: 'a@x.com', updatedAt: localMs }],
    [{ id: 'acct-1', emailAddress: 'a@x.com', updatedAt: remoteStamp }],
  )
  assert.deepStrictEqual(broken.pushIds, ['acct-1'], '未归一时毫秒值会触发上行（守卫被架空）')
  assert.ok(remoteStamp <= localMs, '服务端守卫 stored<=base 恒成立')
})

// 本地确实更新时仍然要能上行 —— 防止「归一顺手把上行也废了」。
test('a genuinely newer local stamp still pushes', () => {
  const plan = planAccountSync(
    [{ id: 'acct-1', emailAddress: 'a@x.com', updatedAt: SEC + 5 }],
    [{ id: 'acct-1', emailAddress: 'a@x.com', updatedAt: SEC }],
  )
  assert.deepStrictEqual(plan.pushIds, ['acct-1'])
  assert.deepStrictEqual(plan.pullIds, [])
})

// 归一是幂等的：读两次不能越除越小。
test('normalization is idempotent', () => {
  const once = normalizeAccountStamp(SEC * 1000)
  assert.equal(normalizeAccountStamp(once), once)
  assert.equal(normalizeAccountStamp(normalizeAccountStamp(SEC * 1000)), SEC)
})
