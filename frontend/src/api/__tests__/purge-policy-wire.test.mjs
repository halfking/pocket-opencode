import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'vite'
import { createPinia, setActivePinia } from 'pinia'

async function withApi(run) {
  const previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const previousFetch = globalThis.fetch
  const values = new Map([['pocket_token', 'test-token'], ['pocket_user', 'Test user'], ['pocket_workspace_id', 'test-workspace']])
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key),
  } })
  setActivePinia(createPinia())
  const vite = await createServer({ configFile: false, server: { middlewareMode: true, hmr: false }, optimizeDeps: { noDiscovery: true } })
  try { await run(await vite.ssrLoadModule('/src/api/syncPolicy.ts'), values, vite) }
  finally {
    await vite.close()
    globalThis.fetch = previousFetch
    if (previousStorage) Object.defineProperty(globalThis, 'localStorage', previousStorage)
    else delete globalThis.localStorage
  }
}

test('cloud purge preserves the server module counts, including zero', () => withApi(async api => {
  globalThis.fetch = async () => Response.json({ purged: { notes: 3, email: 0 } })
  const result = await api.purgeCloudCopy(['notes', 'email'])
  assert.deepEqual(result, { purged: { notes: 3, email: 0 } })
}))

for (const body of [{}, null, { purged: [] }, { purged: { notes: -1 } }, { purged: { notes: 1.5 } }, { purged: { notes: 1, tasks: 3 } }, { purged: { email: 2 } }]) {
  test(`cloud purge rejects an unconfirmed or invalid response ${JSON.stringify(body)}`, () => withApi(async api => {
    globalThis.fetch = async () => Response.json(body)
    await assert.rejects(api.purgeCloudCopy(['notes']), /invalid purge response/i)
  }))
}

test('cloud purge rejects unsupported modules without network activity', () => withApi(async api => {
  let calls = 0
  globalThis.fetch = async () => { calls++; return Response.json({ purged: { tasks: 0 } }) }
  await assert.rejects(api.purgeCloudCopy(['tasks']), /portal module/i)
  assert.equal(calls, 0)
}))

test('a purge response cannot complete under another login', () => withApi(async (api, values, vite) => {
  let resolve
  globalThis.fetch = () => new Promise(done => { resolve = done })
  const { useAuthStore } = await vite.ssrLoadModule('/src/stores/auth.ts')
  const auth = useAuthStore()
  const pending = api.purgeCloudCopy(['notes'])
  auth.setAuthWithWorkspace('other-token', 'Other user', 'other-user', 'other-workspace')
  resolve(Response.json({ purged: { notes: 2 } }))
  await assert.rejects(pending, error => error.name === 'AbortError')
  assert.equal(values.get('pocket_token'), 'other-token')
}))
