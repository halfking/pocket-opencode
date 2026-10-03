import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { buildTasksUrl } from './tasks-url.ts'

// BUG-J 回归锁（2026-09-30 真机验收）。
//
// 缺陷：getTasks 曾经把绝对 URL 过一遍
//   `new URL(base + '/api/tasks', origin).toString().replace(origin, '')`
// 想把同源绝对地址降成相对路径。CAP_ANDROID_SCHEME=http 时页面 origin 是
// 无端口的 `http://localhost`，而 API base 是带端口的 `http://localhost:8088`，
// replace 命中前缀后得到畸形串 `8088/api/tasks`，最终打到本地壳 index.html。
describe('buildTasksUrl', () => {
  it('keeps a port-bearing loopback base intact (the exact BUG-J case)', () => {
    assert.equal(
      buildTasksUrl('http://localhost:8088'),
      'http://localhost:8088/api/tasks',
    )
    // 反证旧实现：origin 无端口时 replace 把 base 的 scheme+host 当前缀删掉，
    // 只剩 `:8088/api/tasks` 这种畸形串（真机上它被 fetch 当相对路径解析，
    // 最终命中 Capacitor 本地壳返回 index.html，触发 assertNotHTML）。
    const origin = 'http://localhost'
    const old = new URL('http://localhost:8088/api/tasks', origin)
      .toString()
      .replace(origin, '')
    assert.equal(old, ':8088/api/tasks')
    assert.notEqual(old, buildTasksUrl('http://localhost:8088'))
  })

  it('appends filters without dropping the base', () => {
    assert.equal(
      buildTasksUrl('http://localhost:8088', 'inst-1', { workstreamId: 'ws-9', source: 'local' }),
      'http://localhost:8088/api/tasks?instance_id=inst-1&workstream_id=ws-9&source=local',
    )
  })

  it('omits the question mark when there are no filters', () => {
    assert.equal(buildTasksUrl('https://pocket.itestu.cn'), 'https://pocket.itestu.cn/api/tasks')
  })

  it('produces a same-origin relative path when base is empty', () => {
    assert.equal(buildTasksUrl(''), '/api/tasks')
  })
})
