import assert from 'node:assert/strict'
import { test } from 'node:test'

// acc-api-runtime-hosts.test.mjs —— acc-board 的**目标主机下拉**必须能在真实
// ACC 上拿到主机。
//
// 为什么需要它（2026-11-17 实测 + 源码对拍）：
//   HandoffDialog.vue 用 listAccAgents() 加载「重派/移交到哪台主机」的候选项。
//   旧实现只调 `GET /api/v2/runtime/agents` —— 而 ACC **没有**这个端点：
//     - 后端把 AgentDiscovery 挂在 `/api/v2/discovery` 组；
//     - TaskAssigner 的 `/agents` 是另一个前缀、另一种语义（agent 池）；
//     - 真实的主机池读面是 `GET /api/v2/runtime/runtimes`
//       （RuntimeControlHandler 的 fleet inventory）。
//   M0 E2E 脚本自己就在 agents 取不到后回退到 runtimes
//   （tests/e2e/m0_handoff_test.sh 的 m0_count_hosts_in_list）。
//   ⇒ 旧实现下，主机下拉在真实 ACC 上永远是空的 ⇒ acc-board 仍是孤岛 UI。
//
// 这个测试守四件事：
//   ① agents 端点不可用时**回退**到 runtimes（而不是直接失败）；
//   ② runtimes 的行能归一化出 host_id / runtime_id / agent_inventory / 心跳新鲜度；
//   ③ 后端把缺省字段序列化成 "" 时（host_id=""）标识仍能落到 runtime_id
//      —— `??` 取不到下一个候选，这是旧代码的真实坑；
//   ④ 两个端点都不可用时抛真实错误（不吞成空列表）。

import { listAccAgents } from '../../../api/accApi.ts'

/** 最小 localStorage 替身（accApi 只读它，不写）。 */
function stubStorage(entries = {}) {
  const map = new Map(Object.entries(entries))
  globalThis.localStorage = {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, String(v)),
    removeItem: (k) => map.delete(k),
    clear: () => map.clear(),
  }
}

/** 按路径表应答的 fetch 替身，记录调用顺序。 */
function stubFetch(routes) {
  const calls = []
  globalThis.fetch = async (url) => {
    calls.push(String(url))
    const path = String(url).replace('http://acc.test', '')
    const entry = routes[path]
    if (!entry) return new Response('not found', { status: 404 })
    const { status = 200, body = {} } = entry
    return new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })
  }
  return calls
}

function runtimeRow(overrides = {}) {
  return {
    runtime_id: 'runtime-a',
    host_id: 'host-A',
    api_endpoint: 'http://127.0.0.1:9101',
    registration_version: 1,
    epoch: 1,
    last_heartbeat_at: new Date(Date.now() - 5_000).toISOString(),
    agent_inventory: [{ agent_id: 'claude-code', agent_type: 'cli', status: 'ready' }],
    ...overrides,
  }
}

function fixture(t, routes) {
  stubStorage({ acc_board_base_url: 'http://acc.test', acc_board_token: 'token-a' })
  const calls = stubFetch(routes)
  t.after(() => {
    delete globalThis.fetch
    delete globalThis.localStorage
  })
  return calls
}

test('agents 端点 404 时回退到 runtimes，并归一化出主机行', async (t) => {
  const calls = fixture(t, {
    '/api/v2/runtime/agents': { status: 404, body: { error: 'not found' } },
    '/api/v2/runtime/runtimes': {
      status: 200,
      body: { ok: true, data: [runtimeRow()], meta: { total: 1 } },
    },
  })

  const hosts = await listAccAgents()

  // ① 回退确实发生了：先试契约口径，再试实现口径。
  assert.deepEqual(calls, [
    'http://acc.test/api/v2/runtime/agents',
    'http://acc.test/api/v2/runtime/runtimes',
  ])
  // ② 归一化：host_id 是主机标识；驱动清单来自 agent_inventory；心跳新鲜 ⇒ 在线。
  assert.equal(hosts.length, 1)
  assert.equal(hosts[0].hostId, 'host-A')
  assert.equal(hosts[0].name, 'host-A')
  assert.deepEqual(hosts[0].drivers, ['claude-code'])
  assert.equal(hosts[0].online, true)
  assert.equal(hosts[0].status, 'online')
})

test('agents 端点可用且有数据时不再请求 runtimes', async (t) => {
  const calls = fixture(t, {
    '/api/v2/runtime/agents': {
      status: 200,
      body: { ok: true, agents: [{ host_id: 'host-X', status: 'running', drivers: ['codex'] }] },
    },
    '/api/v2/runtime/runtimes': { status: 200, body: { ok: true, data: [runtimeRow()] } },
  })

  const hosts = await listAccAgents()

  assert.equal(calls.length, 1, 'agents 命中时不应再打 runtimes')
  assert.equal(hosts.length, 1)
  assert.equal(hosts[0].hostId, 'host-X')
  assert.equal(hosts[0].online, true)
})

test('host_id 为空串时标识落到 runtime_id（后端把缺省字段序列化成 ""）', async (t) => {
  fixture(t, {
    '/api/v2/runtime/agents': { status: 404 },
    '/api/v2/runtime/runtimes': {
      status: 200,
      body: { ok: true, data: [runtimeRow({ host_id: '', name: undefined })] },
    },
  })

  const hosts = await listAccAgents()

  assert.equal(hosts.length, 1)
  assert.equal(hosts[0].hostId, 'runtime-a', 'host_id 为空串时应兜底到 runtime_id')
})

test('同一主机的多个 runtime 合并成一行，在线取并集', async (t) => {
  fixture(t, {
    '/api/v2/runtime/agents': { status: 404 },
    '/api/v2/runtime/runtimes': {
      status: 200,
      body: {
        ok: true,
        data: [
          runtimeRow({ runtime_id: 'rt-1', agent_inventory: [{ agent_id: 'claude-code' }] }),
          runtimeRow({
            runtime_id: 'rt-2',
            agent_inventory: [{ agent_id: 'codex' }],
            last_heartbeat_at: new Date(Date.now() - 600_000).toISOString(),
          }),
        ],
      },
    },
  })

  const hosts = await listAccAgents()

  assert.equal(hosts.length, 1, '同一 host 的两个 runtime 应合并为一行')
  assert.equal(hosts[0].hostId, 'host-A')
  assert.equal(hosts[0].online, true, '任一 runtime 心跳新鲜即视为在线')
  assert.deepEqual(hosts[0].drivers.sort(), ['claude-code', 'codex'])
})

test('心跳过期的 runtime 不算在线', async (t) => {
  fixture(t, {
    '/api/v2/runtime/agents': { status: 404 },
    '/api/v2/runtime/runtimes': {
      status: 200,
      body: {
        ok: true,
        data: [runtimeRow({ last_heartbeat_at: new Date(Date.now() - 600_000).toISOString() })],
      },
    },
  })

  const hosts = await listAccAgents()

  assert.equal(hosts.length, 1)
  assert.equal(hosts[0].online, false)
  assert.equal(hosts[0].status, 'stale')
})

test('两个端点都不可用时抛真实错误，不吞成空列表', async (t) => {
  fixture(t, {
    '/api/v2/runtime/agents': { status: 401, body: { error: 'unauthorized' } },
    '/api/v2/runtime/runtimes': { status: 404 },
  })

  await assert.rejects(
    () => listAccAgents(),
    (err) => {
      assert.equal(err.status, 401)
      assert.match(String(err.message), /unauthorized/)
      return true
    },
  )
})
