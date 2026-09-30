/**
 * 「自定义后端地址保存后回落到 https://localhost」的定性测试。
 *
 * 复现路径：ServerSelectView.saveAndUse() 落盘 → 整页重载 → detectKind()
 * 重新读取 override。若落盘没写进去或写了空串，重载后就会退回 origin，
 * 登录页底部显示页面 origin（真机上是 https://localhost）。
 *
 * Run: node --test src/features/servers/__tests__/server-select-logic.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  detectServerChoice,
  previewServerBase,
  resolveServerSave,
  serverChoiceToPersistValue,
} from '../server-select-logic.ts'
import { API_BASE_STORAGE_KEY, BACKUP_API_BASE, PRODUCTION_API_BASE } from '../../../config/api-base.ts'

function memoryStorage(seed = {}) {
  const map = new Map(Object.entries(seed))
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
    _map: map,
  }
}

const ORIGIN = 'https://localhost'

describe('server choice detection', () => {
  it('未设置时按有无 build 默认值判定', () => {
    assert.deepEqual(detectServerChoice(null, 'http://10.0.2.2:8088'), {
      kind: 'build',
      custom: '',
    })
    assert.deepEqual(detectServerChoice(null, ''), { kind: 'origin', custom: '' })
  })

  it('空串 override 表示显式选择同源', () => {
    assert.equal(detectServerChoice('', 'http://x:1').kind, 'origin')
  })

  it('生产/备用预置可被识别', () => {
    assert.equal(detectServerChoice(PRODUCTION_API_BASE, '').kind, 'production')
    assert.equal(detectServerChoice(BACKUP_API_BASE, '').kind, 'backup')
  })
})

describe('persist value mapping', () => {
  it('build 档删除 key，其余写归一化字符串', () => {
    assert.equal(serverChoiceToPersistValue({ kind: 'build', custom: '' }, 'http://b:1', ORIGIN), null)
    assert.equal(serverChoiceToPersistValue({ kind: 'origin', custom: '' }, '', ORIGIN), '')
    assert.equal(
      serverChoiceToPersistValue({ kind: 'production', custom: '' }, '', ORIGIN),
      PRODUCTION_API_BASE,
    )
    assert.equal(
      serverChoiceToPersistValue({ kind: 'custom', custom: 'http://10.0.2.2:8088/' }, '', ORIGIN),
      'http://10.0.2.2:8088',
    )
  })

  it('与页面同源的绝对地址归一成空串（同源）', () => {
    assert.equal(
      serverChoiceToPersistValue({ kind: 'custom', custom: ORIGIN }, '', ORIGIN),
      '',
    )
  })
})

describe('保存自定义地址后重载不得回落到 origin', () => {
  it('空 storage 下保存自定义地址，重载后仍识别为 custom', () => {
    const storage = memoryStorage()
    const outcome = resolveServerSave(
      { kind: 'custom', custom: 'http://192.168.31.20:8088' },
      { buildDefault: '', pageOrigin: ORIGIN, storage },
    )

    assert.equal(storage.getItem(API_BASE_STORAGE_KEY), 'http://192.168.31.20:8088')
    assert.equal(outcome.fellBackToOrigin, false, '保存后不应退回 origin')
    assert.equal(outcome.changed, true)
    assert.equal(outcome.resolved, 'http://192.168.31.20:8088')

    // 重载等价于重新从 storage 读取
    const reloaded = detectServerChoice(storage.getItem(API_BASE_STORAGE_KEY), '')
    assert.deepEqual(reloaded, { kind: 'custom', custom: 'http://192.168.31.20:8088' })
  })

  it('从 build 默认值切到自定义地址，changed 为 true（需要整页重载）', () => {
    const storage = memoryStorage()
    const before = resolveServerSave(
      { kind: 'build', custom: '' },
      { buildDefault: 'http://10.0.2.2:8088', pageOrigin: ORIGIN, storage },
    )
    assert.equal(before.persistValue, null)
    assert.equal(storage.getItem(API_BASE_STORAGE_KEY), null)

    const after = resolveServerSave(
      { kind: 'custom', custom: 'http://127.0.0.1:8088' },
      { buildDefault: 'http://10.0.2.2:8088', pageOrigin: ORIGIN, storage },
    )
    assert.equal(after.changed, true)
    assert.equal(after.resolved, 'http://127.0.0.1:8088')
  })

  it('相同地址重复保存不触发重载', () => {
    const storage = memoryStorage({ [API_BASE_STORAGE_KEY]: 'http://127.0.0.1:8088' })
    const outcome = resolveServerSave(
      { kind: 'custom', custom: 'http://127.0.0.1:8088' },
      { buildDefault: '', pageOrigin: ORIGIN, storage },
    )
    assert.equal(outcome.changed, false)
    assert.equal(outcome.fellBackToOrigin, false)
  })

  it('故意选择同源时才写入空串（空串是合法选择，不是丢写）', () => {
    const storage = memoryStorage()
    const outcome = resolveServerSave(
      { kind: 'origin', custom: '' },
      { buildDefault: '', pageOrigin: ORIGIN, storage },
    )
    assert.equal(outcome.persistValue, '')
    assert.equal(storage.getItem(API_BASE_STORAGE_KEY), '')
    assert.equal(outcome.fellBackToOrigin, true)
  })

  it('preview 与落盘结果一致（预览不会骗人）', () => {
    const storage = memoryStorage()
    const choice = { kind: 'custom', custom: 'http://10.0.2.2:8088/' }
    const preview = previewServerBase(choice, '', ORIGIN)
    const outcome = resolveServerSave(choice, { buildDefault: '', pageOrigin: ORIGIN, storage })
    assert.equal(preview, outcome.persistValue)
  })
})
