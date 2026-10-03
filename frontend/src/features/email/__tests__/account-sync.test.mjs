/**
 * 邮箱账户 LWW 计划：远程新 → 下行；本地新且对得上远程 id/邮箱 → 上行。
 * 本地独有账户不能凭空上行（凭证不在镜像库）。
 *
 * 测的是**生产实现** account-sync.ts 导出的 planAccountSync。
 * 早期这个文件自己复制了一份同名函数，于是生产 LWW 逻辑改动时测试
 * 依然全绿——测试通过并不代表产品行为正确（假测试）。现在直接 import。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { planAccountSync } from '../account-lww.ts'

const stamp = (id, email, updatedAt) => ({ id, emailAddress: email, updatedAt })

test('remote newer overwrites local', () => {
  const got = planAccountSync(
    [stamp('a', 'a@x.com', 10)],
    [stamp('a', 'a@x.com', 20)],
  )
  assert.deepEqual(got, { pullIds: ['a'], pushIds: [] })
})

test('local newer pushes matching remote account', () => {
  const got = planAccountSync(
    [stamp('a', 'a@x.com', 30)],
    [stamp('a', 'a@x.com', 20)],
  )
  assert.deepEqual(got, { pullIds: [], pushIds: ['a'] })
})

test('missing local account is pulled', () => {
  const got = planAccountSync([], [stamp('b', 'b@x.com', 5)])
  assert.deepEqual(got, { pullIds: ['b'], pushIds: [] })
})

test('local-only account is not pushed (no credential on mirror)', () => {
  const got = planAccountSync([stamp('c', 'c@x.com', 99)], [])
  assert.deepEqual(got, { pullIds: [], pushIds: [] })
})

test('equal timestamps do nothing', () => {
  const got = planAccountSync(
    [stamp('a', 'a@x.com', 7)],
    [stamp('a', 'a@x.com', 7)],
  )
  assert.deepEqual(got, { pullIds: [], pushIds: [] })
})
