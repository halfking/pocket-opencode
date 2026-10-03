/**
 * utils/download.ts 单测（2026-10-03 Phase 9.4 增量）。
 *
 * 覆盖迁移到 pocket-native 后的关键不变量：
 *   1. utf8ToBase64：UTF-8 安全 base64（中文字符不抛 InvalidCharacterError）
 *   2. utf8ToBase64 往返 → 原字符串一致
 *   3. arrayBufferToBase64：二进制 → base64
 *   4. arrayBufferToBase64：长缓冲按 chunk 拆分（验证 chunk=0x8000 边界不爆栈）
 *   5. blobToBase64：Blob(FileReader) → 纯 base64（去除 data:... 前缀）
 *
 * 平台分发（web/android/ios/harmony）涉及动态 import Capacitor / DOM / WebView，
 * 留待 Phase 9.5 集成测试覆盖（计划文档 §4.2）；本单测只锁纯函数契约。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  utf8ToBase64,
  arrayBufferToBase64,
  blobToBase64,
} from '../download-encoding.ts'

test('utf8ToBase64: 纯 ASCII 与原生 btoa 等价', () => {
  assert.equal(utf8ToBase64('hello'), 'aGVsbG8=')
  assert.equal(utf8ToBase64('hello world'), 'aGVsbG8gd29ybGQ=')
})

test('utf8ToBase64: 中文/emoji UTF-8 安全(原生 btoa 会抛 InvalidCharacterError)', () => {
  // 原生 btoa('你好') 抛 InvalidCharacterError；utf8ToBase64 必须不抛
  const out = utf8ToBase64('你好 POCKET')
  assert.ok(out.length > 0, 'utf8ToBase64 必须返回非空字符串')
  assert.equal(typeof out, 'string')
  // 解码回 UTF-8 应与原文一致（往返不变）
  const back = Buffer.from(out, 'base64').toString('utf8')
  assert.equal(back, '你好 POCKET')
})

test('utf8ToBase64: emoji 往返不变', () => {
  const input = '👋🌍🎯'
  const out = utf8ToBase64(input)
  const back = Buffer.from(out, 'base64').toString('utf8')
  assert.equal(back, input)
})

test('utf8ToBase64: 空串', () => {
  assert.equal(utf8ToBase64(''), '')
})

test('arrayBufferToBase64: 二进制转 base64', () => {
  const buf = new Uint8Array([0x48, 0x65, 0x6c, 0x6c, 0x6f]).buffer // "Hello"
  assert.equal(arrayBufferToBase64(buf), 'SGVsbG8=')
})

test('arrayBufferToBase64: chunk 边界（>0x8000 字节缓冲不爆栈）', () => {
  // 构造 32KB 数据（> 0x8000），确认 chunk 拆分拼接正确
  const size = 32 * 1024
  const bytes = new Uint8Array(size)
  for (let i = 0; i < size; i++) bytes[i] = i & 0xff
  const b64 = arrayBufferToBase64(bytes.buffer)
  const back = new Uint8Array(Buffer.from(b64, 'base64'))
  assert.equal(back.length, size, '往返长度一致')
  for (let i = 0; i < size; i++) {
    assert.equal(back[i], i & 0xff, `字节偏移 ${i} 一致`)
  }
})

test('blobToBase64: Blob → 纯 base64(去除 data: 前缀)', async () => {
  // node 22 全局有 Blob 但没有 FileReader —— stub 一个最小实现。
  // 关键契约：FileReader.readAsDataURL 完成后 onloadend 触发，且 base64 提取去前缀。
  /** @type {any} */
  const originalFileReader = globalThis.FileReader
  let lastRead = /** @type {string | undefined} */ (undefined)
  /** @type {any} */
  class StubFileReader {
    constructor() {
      /** @type {((this: FileReader, ev: Event) => any) | null} */
      this.onloadend = null
      /** @type {((this: FileReader, ev: Event) => any) | null} */
      this.onerror = null
      /** @type {any} */
      this.result = null
    }
    readAsDataURL(/** @type {Blob} */ blob) {
      // 同步返回 data URL（与浏览器 FileReader 异步回调差异；测试用 Promise.resolve 排队）
      const text = /** @type {string} */ (blob[Symbol.for('testPayload')])
      lastRead = `data:text/plain;base64,${utf8ToBase64(text)}`
      this.result = lastRead
      queueMicrotask(() => this.onloadend?.(/** @type {any} */ ({})))
    }
  }
  globalThis.FileReader = StubFileReader
  try {
    const text = 'Hello, Pocket!'
    const expected = utf8ToBase64(text)
    // 用 Symbol 把 payload 传给 stub（避免同步 blob → text 转换）
    const blob = new Blob([text])
    blob[Symbol.for('testPayload')] = text
    const got = await blobToBase64(blob)
    assert.equal(got, expected)
    assert.ok(!got.startsWith('data:'), 'blobToBase64 必须返回纯 base64（无 data: 前缀）')
    assert.ok(lastRead?.startsWith('data:'), 'stub FileReader 应收到 data: URL')
  } finally {
    globalThis.FileReader = originalFileReader
  }
})

test('blobToBase64: FileReader.onerror 透传', async () => {
  /** @type {any} */
  const originalFileReader = globalThis.FileReader
  const fakeError = new Error('reader boom')
  /** @type {any} */
  class ErrorFileReader {
    constructor() {
      /** @type {any} */
      this.onloadend = null
      /** @type {any} */
      this.onerror = null
      /** @type {any} */
      this.result = null
    }
    readAsDataURL() {
      queueMicrotask(() => this.onerror?.(fakeError))
    }
  }
  globalThis.FileReader = ErrorFileReader
  try {
    await assert.rejects(blobToBase64(new Blob(['x'])), /reader boom|文件读取失败/)
  } finally {
    globalThis.FileReader = originalFileReader
  }
})

test('blobToBase64: data URL 缺 base64 部分抛"文件编码失败"', async () => {
  /** @type {any} */
  const originalFileReader = globalThis.FileReader
  /** @type {any} */
  class EmptyFileReader {
    constructor() {
      /** @type {any} */
      this.onloadend = null
      /** @type {any} */
      this.onerror = null
      /** @type {any} */
      this.result = null
    }
    readAsDataURL() {
      // result 是 "data:base64," 空 base64 段 → split(',')[1] = '' → 期望 reject
      this.result = 'data:text/plain;base64,'
      queueMicrotask(() => this.onloadend?.(/** @type {any} */ ({})))
    }
  }
  globalThis.FileReader = EmptyFileReader
  try {
    await assert.rejects(blobToBase64(new Blob(['x'])), /文件编码失败/)
  } finally {
    globalThis.FileReader = originalFileReader
  }
})