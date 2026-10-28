import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'node:http'
import { createServer as createViteServer } from 'vite'
import { createPinia, setActivePinia } from 'pinia'

test('真实策略 API 客户端与四模块门户契约兼容', async () => {
  let observed
  const backend = createServer(async (req, res) => {
    const chunks = []
    for await (const chunk of req) chunks.push(chunk)
    observed = JSON.parse(Buffer.concat(chunks).toString())
    const allowed = ['rss', 'email', 'notes', 'meetings']
    const valid = req.method === 'PUT' && req.url === '/api/v1/portal/sync-policy' &&
      req.headers.authorization === 'Bearer contract-token' &&
      Object.keys(observed.modules).every((module) => allowed.includes(module))
    res.writeHead(valid ? 200 : 400, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify(valid ? { system_default: 'cloud', allow_override: true, modules: observed.modules } : { error: 'invalid module' }))
  })
  await new Promise((resolve) => backend.listen(0, '127.0.0.1', resolve))
  const previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const values = new Map([
    ['pocket_token', 'contract-token'],
    ['op_redclaw_portal_base', `http://127.0.0.1:${backend.address().port}`],
  ])
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  } })
  setActivePinia(createPinia())
  const vite = await createViteServer({ configFile: false, server: { middlewareMode: true }, optimizeDeps: { noDiscovery: true } })
  try {
    const api = await vite.ssrLoadModule('/src/api/syncPolicy.ts')
    const result = await api.updateSyncPolicyModules({ 'ai-chat': 'local', tasks: 'local', rss: 'cloud', email: 'cloud', notes: 'local', meetings: 'cloud' })
    assert.deepEqual(Object.keys(observed.modules).sort(), ['email', 'meetings', 'notes', 'rss'])
    assert.equal(result.modules.notes, 'local')
  } finally {
    await vite.close()
    await new Promise((resolve) => backend.close(resolve))
    if (previousStorage) Object.defineProperty(globalThis, 'localStorage', previousStorage)
    else delete globalThis.localStorage
  }
})
