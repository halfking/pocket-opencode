/**
 * workspace-binding.test.mjs — 工作区成员绑定断言的客户端契约（2026-10-29）。
 *
 * ⚠ 这里的每条用例都在问同一个问题：
 *   「客户端会不会把『没拿到证明』当成『拿到了』？」
 *
 *   绑定断言是跨服务授权的载体。它的价值全在「**服务端签过**」上 ——
 *   客户端一旦在缺失时返回空断言/假值，调用方就会拿一个不存在的东西
 *   当授权凭据用 ⇒ 整个信任链在客户端这一环断掉。
 */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'vite'
import { createPinia, setActivePinia } from 'pinia'

/**
 * 起一个 Vite SSR fixture，让模块能真实 import（不是源码替换）。
 */
async function fixture(run) {
  const originals = Object.fromEntries(
    ['localStorage', 'window', 'fetch'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]),
  )
  const values = new Map()
  const storage = {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key),
  }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  const browser = new EventTarget()
  browser.location = { origin: 'http://localhost', protocol: 'http:', hostname: 'localhost' }
  Object.defineProperty(globalThis, 'window', { configurable: true, value: browser })

  const calls = []
  const responses = []
  Object.defineProperty(globalThis, 'fetch', {
    configurable: true,
    value: async (url, init) => {
      calls.push({ url: String(url), init })
      const next = responses.shift()
      if (!next) throw new Error('unexpected fetch: ' + url)
      if (next.throw) throw next.throw
      return {
        ok: next.ok !== false,
        status: next.status ?? (next.ok === false ? 400 : 200),
        json: async () => next.body ?? {},
      }
    },
  })

  const vite = await createServer({
    configFile: false,
    server: { middlewareMode: true, hmr: false },
    optimizeDeps: { noDiscovery: true },
  })
  setActivePinia(createPinia())
  try {
    const authModule = await vite.ssrLoadModule('/src/stores/auth.ts')
    const bindingModule = await vite.ssrLoadModule('/src/api/workspaceBinding.ts')
    const auth = authModule.useAuthStore()
    await auth.setAuthWithWorkspace('token-1', 'User One', 'user-1', 'ws-1')
    await run({ ...bindingModule, auth, calls, responses, vite })
  } finally {
    await vite.close()
    for (const [key, descriptor] of Object.entries(originals)) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor)
      else delete globalThis[key]
    }
  }
}

test('binding fetch sends the session token and returns the signed scope', () => fixture(async f => {
  f.responses.push({
    body: {
      binding: 'signed.jwt.value',
      user_id: 'user-1',
      tenant_id: 'tenant-a',
      workspace_id: 'ws-1',
      role: 'owner',
      issuer: 'pocket',
      audience: 'workspace-binding',
      issued_at: 100,
      expires_at: 200,
    },
  })
  const result = await f.fetchWorkspaceBinding('ws-1')
  assert.equal(result.binding, 'signed.jwt.value')
  assert.equal(result.userId, 'user-1')
  assert.equal(result.workspaceId, 'ws-1')
  assert.equal(result.tenantId, 'tenant-a')
  assert.equal(f.calls.length, 1)
  assert.match(f.calls[0].url, /\/api\/workspaces\/ws-1\/binding$/)
  assert.equal(f.calls[0].init.headers.Authorization, 'Bearer ' + f.auth.token)
}))

test('binding fetch refuses to return a scope the server did not attest', () => fixture(async f => {
  // 服务端返回了 binding 但没给 user_id ⇒ 客户端不能把它当成一份可用断言。
  f.responses.push({ body: { binding: 'signed.jwt.value', workspace_id: 'ws-1' } })
  await assert.rejects(f.fetchWorkspaceBinding('ws-1'), /did not issue a workspace binding/)
}))

test('binding fetch rejects a response with no signed assertion', () => fixture(async f => {
  f.responses.push({ body: { user_id: 'user-1', workspace_id: 'ws-1' } })
  await assert.rejects(f.fetchWorkspaceBinding('ws-1'), /did not issue a workspace binding/)
}))

test('binding fetch surfaces a server rejection instead of an empty binding', () => fixture(async f => {
  f.responses.push({ ok: false, status: 403, body: { error: 'not a member of this workspace' } })
  await assert.rejects(f.fetchWorkspaceBinding('ws-1'), /did not issue a workspace binding/)
}))

test('binding fetch requires a workspace id', () => fixture(async f => {
  await assert.rejects(f.fetchWorkspaceBinding('   '), /workspace id is required/)
  assert.equal(f.calls.length, 0)
}))

test('verification rejects a server response that is not valid', () => fixture(async f => {
  f.responses.push({ body: { valid: false } })
  await assert.rejects(f.verifyWorkspaceBinding('signed.jwt.value'), /rejected the workspace binding/)
}))

test('verification returns the attested scope', () => fixture(async f => {
  f.responses.push({
    body: {
      valid: true,
      user_id: 'user-1',
      tenant_id: 'tenant-a',
      workspace_id: 'ws-1',
      role: 'owner',
      issuer: 'pocket',
      audience: 'workspace-binding',
      issued_at: 100,
      expires_at: 200,
    },
  })
  const result = await f.verifyWorkspaceBinding('signed.jwt.value', 'user-1', 'ws-1')
  assert.equal(result.valid, true)
  assert.equal(result.userId, 'user-1')
  assert.equal(result.workspaceId, 'ws-1')
  assert.match(f.calls[0].url, /\/api\/auth\/workspace-binding$/)
  const sent = JSON.parse(f.calls[0].init.body)
  assert.equal(sent.binding, 'signed.jwt.value')
  assert.equal(sent.user_id, 'user-1')
  assert.equal(sent.workspace_id, 'ws-1')
}))

test('verification requires a binding', () => fixture(async f => {
  await assert.rejects(f.verifyWorkspaceBinding('  '), /binding is required/)
  assert.equal(f.calls.length, 0)
}))
