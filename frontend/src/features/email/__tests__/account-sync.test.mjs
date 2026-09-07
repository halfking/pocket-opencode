/**
 * 邮箱账户 LWW 计划：远程新 → 下行；本地新且对得上远程 id/邮箱 → 上行。
 * 本地独有账户不能凭空上行（凭证不在镜像库）。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

const planAccountSync = (local, remote) => {
  const localById = new Map(local.map((a) => [a.id, a]))
  const remoteById = new Map(remote.map((a) => [a.id, a]))
  const remoteByEmail = new Map(remote.map((a) => [a.emailAddress.toLowerCase(), a]))
  const pullIds = []
  const pushIds = []
  for (const r of remote) {
    const l = localById.get(r.id)
    if (!l || r.updatedAt > l.updatedAt) pullIds.push(r.id)
  }
  for (const l of local) {
    const r = remoteById.get(l.id) ?? remoteByEmail.get(l.emailAddress.toLowerCase())
    if (r && l.updatedAt > r.updatedAt) pushIds.push(l.id)
  }
  return { pullIds, pushIds }
}

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
