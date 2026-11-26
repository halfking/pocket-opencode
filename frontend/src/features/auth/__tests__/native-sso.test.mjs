import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { createNativeSSOProof, readNativeSSOReturn } from '../native-sso.ts'

const apiBase = 'https://pocket.example.org'
const appOrigin = 'https://app-link.example.org'

test('the system browser gets only the challenge and returns to the bundled app origin', async () => {
  const proof = await createNativeSSOProof(apiBase, appOrigin, appOrigin)
  const start = new URL(proof.startUrl)
  assert.equal(start.origin, apiBase)
  assert.equal(start.pathname, '/api/auth/sso/native/start')
  assert.deepEqual([...start.searchParams], [['challenge', proof.challenge]])
  assert.equal(proof.challenge, createHash('sha256').update(proof.verifier).digest('base64url'))
  assert.notEqual(proof.challenge, proof.verifier)
  await assert.rejects(createNativeSSOProof(apiBase, 'https://other.example.org', appOrigin))
  await assert.rejects(createNativeSSOProof(apiBase, apiBase, apiBase))
  await assert.rejects(createNativeSSOProof('http://pocket.example.org', appOrigin, appOrigin))
})

test('foreign, malformed, duplicate, stale and superseded returns cannot redeem', async () => {
  const proof = await createNativeSSOProof(apiBase, appOrigin, appOrigin)
  const newer = await createNativeSSOProof(apiBase, appOrigin, appOrigin)
  const code = 'a'.repeat(64)
  const fragment = `#code=${code}&challenge=${proof.challenge}`
  const link = `${appOrigin}/auth/sso/app-return${fragment}`
  assert.equal(readNativeSSOReturn(link, proof), code)
  for (const bad of [
    `https://other.example.org/auth/sso/app-return${fragment}`,
    `${appOrigin}/other${fragment}`,
    `${appOrigin}/auth/sso/app-return?extra=true${fragment}`,
    `${link}&code=${code}`,
    `${link}&challenge=${proof.challenge}`,
    link.replace(code, 'bad'),
    'broken-url',
  ]) {
    assert.equal(readNativeSSOReturn(bad, proof), undefined, bad)
  }
  assert.equal(readNativeSSOReturn(link, newer), undefined)
  assert.equal(readNativeSSOReturn(link, { ...proof, expiresAt: 0 }), undefined)
})
