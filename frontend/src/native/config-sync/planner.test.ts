/**
 * LWW：远程新下行；本地新上行；相等不动；本地独有默认可上行。
 * Run: node --test src/native/config-sync/planner.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { nowUnixSec, planConfigSync, type ConfigStamp } from './planner.ts'

const stamp = (namespace: string, id: string, updatedAt: number): ConfigStamp => ({
  namespace, id, updatedAt,
})

describe('planConfigSync', () => {
  it('remote newer is pulled', () => {
    const got = planConfigSync(
      [stamp('app_prefs', 'default', 10)],
      [stamp('app_prefs', 'default', 20)],
    )
    assert.deepEqual(got, { pullKeys: ['app_prefs:default'], pushKeys: [] })
  })

  it('local newer is pushed', () => {
    const got = planConfigSync(
      [stamp('llm_gateway', 'default', 30)],
      [stamp('llm_gateway', 'default', 20)],
    )
    assert.deepEqual(got, { pullKeys: [], pushKeys: ['llm_gateway:default'] })
  })

  it('missing local row is pulled', () => {
    const got = planConfigSync([], [stamp('chat_settings', 'default', 5)])
    assert.deepEqual(got, { pullKeys: ['chat_settings:default'], pushKeys: [] })
  })

  it('local-only row is pushed by default', () => {
    const got = planConfigSync([stamp('scheduled_task', 't1', 99)], [])
    assert.deepEqual(got, { pullKeys: [], pushKeys: ['scheduled_task:t1'] })
  })

  it('local-only row stays local when pushLocalOnly is false', () => {
    const got = planConfigSync(
      [stamp('email', 'c', 99)],
      [],
      { pushLocalOnly: false },
    )
    assert.deepEqual(got, { pullKeys: [], pushKeys: [] })
  })

  it('equal timestamps do nothing', () => {
    const got = planConfigSync(
      [stamp('connection', 'default', 7)],
      [stamp('connection', 'default', 7)],
    )
    assert.deepEqual(got, { pullKeys: [], pushKeys: [] })
  })
})

describe('nowUnixSec', () => {
  it('converts millisecond epoch to unix seconds', () => {
    assert.equal(nowUnixSec(1_725_753_600_500), 1_725_753_600)
  })
})
