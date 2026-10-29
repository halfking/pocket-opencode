import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'vite'
import { fileURLToPath } from 'node:url'

async function fixture(switchAfterFirst, run) {
  const auth = { identity: 'alice' }, calls = []
  const key = `__purgeOwner${Math.random().toString(36).slice(2)}`
  globalThis[key] = {
    useAuthStore: () => auth,
    captureAuthSession: () => ({ owner: auth, identity: auth.identity }),
    assertAuthSession(owner, session) { if (owner.identity !== session.identity) throw Object.assign(new Error('Session changed'), { name: 'AbortError' }) },
    async http(path, opts) {
      opts.assertCurrent?.()
      const ids = JSON.parse(opts.body).ids
      calls.push({ owner: auth.identity, path, ids })
      // The transport promise has completed. The next business chunk resumes after this switch.
      if (switchAfterFirst && calls.length === 1) queueMicrotask(() => { auth.identity = 'bob' })
      return { purged: ids.length }
    },
  }
  const vite = await createServer({ root: fileURLToPath(new URL('../../../', import.meta.url)), configFile: false,
    server: { middlewareMode: true, hmr: false }, optimizeDeps: { noDiscovery: true }, plugins: [{ name: 'purge-owner-boundaries', enforce: 'pre',
      resolveId(source, importer) {
        if (!importer?.endsWith('/api/email.ts')) return
        if (source === './http') return '\0purge-http'
        if (source === '../stores/auth') return '\0purge-auth'
      }, load(id) {
        if (id === '\0purge-http') return `export const http=globalThis[${JSON.stringify(key)}].http;export const LONG_REQUEST_TIMEOUT_MS=120000;export class ApiError extends Error{}`
        if (id === '\0purge-auth') return `export const {useAuthStore,captureAuthSession,assertAuthSession}=globalThis[${JSON.stringify(key)}]`
      } }] })
  try { const { emailApi } = await vite.ssrLoadModule('/src/api/email.ts'); await run(emailApi, calls) }
  finally { delete globalThis[key]; await vite.close() }
}

test('purging over 100 messages stops before a second batch can use a replacement identity', () => fixture(true, async (api, calls) => {
  await assert.rejects(api.purgeEmails(Array.from({ length: 205 }, (_, i) => `mail-${i}`)), { name: 'AbortError' })
  assert.equal(calls.length, 1)
  assert.equal(calls[0].owner, 'alice')
  assert.equal(calls[0].ids.length, 100)
}))

test('unchanged-session purge keeps all IDs and sums every successful batch', () => fixture(false, async (api, calls) => {
  const ids = Array.from({ length: 205 }, (_, i) => `mail-${i}`)
  assert.deepEqual(await api.purgeEmails(ids), { purged: 205 })
  assert.deepEqual(calls.map(call => call.ids.length), [100, 100, 5])
  assert.deepEqual(calls.flatMap(call => call.ids), ids)
  assert.ok(calls.every(call => call.owner === 'alice'))
}))
