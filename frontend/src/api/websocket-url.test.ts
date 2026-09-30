import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { buildWebSocketUrl } from './websocket-url.ts'

describe('buildWebSocketUrl', () => {
  it('https -> wss, http -> ws', () => {
    assert.equal(buildWebSocketUrl('https://pocket.example.com:8088', 'T'), 'wss://pocket.example.com:8088/ws?token=T')
    assert.equal(buildWebSocketUrl('http://192.168.31.20:8088', 'T'), 'ws://192.168.31.20:8088/ws?token=T')
  })

  it('returns null when the base is unusable — caller must skip, not retry', () => {
    // 这三条正是旧的字符串拼接会产出非法 URL 的三种输入
    assert.equal(buildWebSocketUrl(''), null, '空 base -> 不能拼出 "/ws"')
    assert.equal(buildWebSocketUrl('   '), null)
    assert.equal(buildWebSocketUrl('not a url'), null)
    assert.equal(buildWebSocketUrl('capacitor://localhost'), null, '非 http(s) scheme 必须拒绝')
    assert.equal(buildWebSocketUrl('ws://already-ws'), null)
  })

  it('collapses trailing slashes instead of producing "//ws"', () => {
    assert.equal(buildWebSocketUrl('https://h/', 'T'), 'wss://h/ws?token=T')
    assert.equal(buildWebSocketUrl('https://h///', 'T'), 'wss://h/ws?token=T')
  })

  it('preserves a base path prefix', () => {
    assert.equal(buildWebSocketUrl('https://h/gateway', 'T'), 'wss://h/gateway/ws?token=T')
  })

  it('omits the token parameter when not logged in', () => {
    assert.equal(buildWebSocketUrl('https://h'), 'wss://h/ws')
    assert.equal(buildWebSocketUrl('https://h', null), 'wss://h/ws')
    assert.equal(buildWebSocketUrl('https://h', ''), 'wss://h/ws')
  })

  it('drops any pre-existing query/hash on the base', () => {
    assert.equal(buildWebSocketUrl('https://h?stale=1#frag', 'T'), 'wss://h/ws?token=T')
  })

  it('percent-encodes the token instead of pasting it raw', () => {
    const url = buildWebSocketUrl('https://h', 'a b&c=d')!
    assert.equal(url, 'wss://h/ws?token=a+b%26c%3Dd')
    // 反证：旧的模板字符串拼接会把 & 和空格原样塞进 query
    assert.ok(!url.includes('token=a b'))
  })
})
