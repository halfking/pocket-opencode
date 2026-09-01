import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { buildWebSocketUrl } from '../websocket-url.ts'

test('builds a wss endpoint from an HTTPS API base', () => {
  assert.equal(
    buildWebSocketUrl('https://api.example.test/', 'token with spaces'),
    'wss://api.example.test/ws?token=token+with+spaces',
  )
})

test('builds a ws endpoint from an HTTP API base with a path', () => {
  assert.equal(
    buildWebSocketUrl('http://192.0.2.10:8088/api///', 'a&b'),
    'ws://192.0.2.10:8088/api/ws?token=a%26b',
  )
})

test('does not use the rawfile origin when the API base is configured', () => {
  const rawfileOrigin = 'https://rawfile.localhost'
  const apiUrl = buildWebSocketUrl('https://api.example.test', 'jwt')

  assert.equal(apiUrl, 'wss://api.example.test/ws?token=jwt')
  assert.notEqual(apiUrl, `${rawfileOrigin}/ws?token=jwt`)
})

test('returns a safe null result for missing or unsupported API bases', () => {
  assert.equal(buildWebSocketUrl('', 'jwt'), null)
  assert.equal(buildWebSocketUrl('not a URL', 'jwt'), null)
  assert.equal(buildWebSocketUrl('file:///tmp/index.html', 'jwt'), null)
})

test('omits the token query when no token is available', () => {
  assert.equal(buildWebSocketUrl('https://api.example.test', null), 'wss://api.example.test/ws')
})

test('preserves an API base path without inheriting its query or hash', () => {
  assert.equal(
    buildWebSocketUrl('https://api.example.test/pocket?tenant=demo#ignored', 'jwt'),
    'wss://api.example.test/pocket/ws?token=jwt',
  )
})
