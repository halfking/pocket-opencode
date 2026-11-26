import assert from 'node:assert/strict'
import { beforeEach, test } from 'node:test'
import { decryptString, encryptString, FieldSaltUnavailableError, initAppCrypto, isCryptoReady, resetCryptoKey } from '../crypto.ts'

const cache = new Map()
const key = 'pocket_crypto_salt'
const password = 'fixture passphrase'
globalThis.localStorage = { getItem: name => cache.get(name) ?? null, setItem: (name, value) => cache.set(name, value) }
beforeEach(() => { cache.clear(); resetCryptoKey() })

function metadata(newDatabase, salt = null) {
  return {
    newDatabase,
    read: async () => salt,
    save: async value => {
      assert.equal(salt, null, 'must never overwrite the database salt')
      salt = value
    },
  }
}

test('existing WebView salt migrates without changing encrypted fields', async () => {
  await initAppCrypto(password)
  const original = cache.get(key)
  const encrypted = await encryptString('an offline edit')
  const store = metadata(false)
  await initAppCrypto(password, store)
  assert.equal(await store.read(), original)
  cache.clear()
  resetCryptoKey()
  await initAppCrypto(password, store)
  assert.equal(await decryptString(encrypted), 'an offline edit')
  assert.equal(cache.has(key), false)
})

test('a restored database wins over stale WebView state', async () => {
  const store = metadata(true)
  await initAppCrypto(password, store)
  const encrypted = await encryptString('portable ciphertext')
  cache.set(key, 'unrelated WebView state')
  resetCryptoKey()
  await initAppCrypto(password, store)
  assert.equal(await decryptString(encrypted), 'portable ciphertext')
})

test('missing or corrupted salt on an established database never silently creates a replacement', async () => {
  const missing = metadata(false)
  await assert.rejects(initAppCrypto(password, missing), FieldSaltUnavailableError)
  assert.equal(await missing.read(), null)
  assert.equal(isCryptoReady(), false)
  await assert.rejects(initAppCrypto(password, metadata(false, 'invalid')), FieldSaltUnavailableError)
  assert.equal(isCryptoReady(), false)
})
