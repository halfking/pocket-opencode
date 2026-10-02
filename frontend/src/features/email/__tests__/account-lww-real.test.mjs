// account-lww-real.test.mjs — 需求 8 的 LWW 判定，用**真实库账户**做夹具。
//
// 为什么用真实数据而不是编造的两条记录：2026-10-01 巡检时用真实 7 个账户
// 跑 planAccountSync，发现「本地 id 与服务端 id 不同、但邮箱相同」这一组会
// 同时产出 pull 和 push——方向冲突，最终谁赢取决于执行顺序。编造的极小夹具
// 容易把这个组合漏掉，真实账户集（5 个共享同一 updated_at、id 形态各异）
// 反而能撞出来。
//
// 真实数据来源（2026-10-01，opencode_pocket.email_accounts）：
//   acct-1790782486625898900-1  invoice-fixture@example.com  1790802086
//   acct-1790811900843306300-1  audit-poc@pocket-audit.test  1790811900
//   acct-1790784254756356300-4  feikemanager1@163.com       1790846518
//   acct-1790784248178102200-3  56551681@qq.com              1790846518
//   acct-1790784212318350400-2  feikemanager@163.com         1790846518
//   acct-1790784255240360000-5  kimmy.huang@163.com          1790846518
//   acct-1790784184824054300-1  huangxutao@kxpms.cn          1790846518
// 全部 user_id=user-admin（users 表 username=admin role=admin），满足需求 8
// 「归到 admin 用户名下」的前提。

import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { planAccountSync } from '../account-lww.ts'

const remote = [
  { id: 'acct-1790782486625898900-1', emailAddress: 'invoice-fixture@example.com', updatedAt: 1790802086 },
  { id: 'acct-1790811900843306300-1', emailAddress: 'audit-poc@pocket-audit.test', updatedAt: 1790811900 },
  { id: 'acct-1790784254756356300-4', emailAddress: 'feikemanager1@163.com', updatedAt: 1790846518 },
  { id: 'acct-1790784248178102200-3', emailAddress: '56551681@qq.com', updatedAt: 1790846518 },
  { id: 'acct-1790784212318350400-2', emailAddress: 'feikemanager@163.com', updatedAt: 1790846518 },
  { id: 'acct-1790784255240360000-5', emailAddress: 'kimmy.huang@163.com', updatedAt: 1790846518 },
  { id: 'acct-1790784184824054300-1', emailAddress: 'huangxutao@kxpms.cn', updatedAt: 1790846518 },
]

function assertEq(actual, expected, msg) {
  assert.deepStrictEqual(actual, expected, `${msg}: got ${JSON.stringify(actual)}, want ${JSON.stringify(expected)}`)
}

// 首次登录：本地空 -> 全部下拉。
test('real accounts: empty local pulls everything', () => {
  const p = planAccountSync([], remote)
  assertEq(p.pullIds.length, 7, 'pull count')
  assertEq(p.pushIds.length, 0, 'push count')
})

// 完全一致 -> 零动作（避免每次同步都写库抖动）。
test('real accounts: identical is a no-op', () => {
  const p = planAccountSync(remote, remote)
  assertEq(p.pullIds.length, 0, 'pull count')
  assertEq(p.pushIds.length, 0, 'push count')
})

// 本地更新 -> 只 push 那一个。
test('real accounts: newer local pushes exactly one', () => {
  const local = remote.map((a, i) => (i === 3 ? { ...a, updatedAt: a.updatedAt + 100 } : a))
  const p = planAccountSync(local, remote)
  assertEq(p.pullIds.length, 0, 'pull count')
  assertEq(p.pushIds.length, 1, 'push count')
  assertEq(p.pushIds[0], 'acct-1790784248178102200-3', 'pushed id')
})

// 服务端更新 -> 只 pull 那一个。
test('real accounts: older local pulls exactly one', () => {
  const local = remote.map((a, i) => (i === 2 ? { ...a, updatedAt: a.updatedAt - 100 } : a))
  const p = planAccountSync(local, remote)
  assertEq(p.pullIds.length, 1, 'pull count')
  assertEq(p.pushIds.length, 0, 'push count')
  assertEq(p.pullIds[0], 'acct-1790784254756356300-4', 'pulled id')
})

// 本地独有账户不上行：镜像库不持有凭证，服务端无法据此建可用账户。
test('real accounts: local-only account is not pushed', () => {
  const local = [...remote, { id: 'local-only', emailAddress: 'new@local.dev', updatedAt: 1790900000 }]
  const p = planAccountSync(local, remote)
  assertEq(p.pushIds.length, 0, 'push count')
  assertEq(p.pullIds.length, 0, 'pull count')
})

// 核心修复：id 不同但邮箱相同、且**本地更新**时，只能 push（不能 pull）。
//
// 修复前下行只按 id 配对，配不上就判定「本地没有这个账户」而下行覆盖，把
// 用户较新的本地改动冲掉；上行又按邮箱配上同一条，于是既 pull 又 push，
// 结果取决于执行顺序——那不是 LWW。
// 场景是真实的：服务端重建账户 id 后客户端仍留着旧 id。
test('real accounts: local id differs but same email -> push wins', () => {
  const remoteSide = remote.map((a) =>
    a.id === 'acct-1790784248178102200-3' ? { ...a, updatedAt: a.updatedAt - 500 } : a,
  )
  const local = remote.map((a) =>
    a.id === 'acct-1790784248178102200-3' ? { ...a, id: 'local-renamed-id' } : a,
  )
  const p = planAccountSync(local, remoteSide)
  assertEq(p.pullIds.length, 0, 'local is newer, so nothing should be pulled')
  assertEq(p.pushIds.length, 1, 'push count')
  assertEq(p.pushIds[0], 'local-renamed-id', 'the local row is the one to push')
})

// 同上但**服务端更新**：此时 pull 是正确方向，且仍不能重复计划。
test('real accounts: remote newer wins when ids differ', () => {
  const remoteNewer = remote.map((a) =>
    a.id === 'acct-1790784248178102200-3' ? { ...a, updatedAt: a.updatedAt + 500 } : a,
  )
  const local = remote.map((a) =>
    a.id === 'acct-1790784248178102200-3' ? { ...a, id: 'local-renamed-id' } : a,
  )
  const p = planAccountSync(local, remoteNewer)
  assertEq(p.pullIds.length, 1, 'pull count')
  assertEq(p.pushIds.length, 0, 'push count')
})
