import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  classifyRecovery,
  createElapsedTracker,
  partsToBlob,
} from '../recorderTiming.ts'

test('elapsed：无暂停时等于真实流逝时间', () => {
  const t = createElapsedTracker()
  t.start(1000)
  assert.equal(t.elapsed(4000), 3000)
})

test('elapsed：暂停区间不计入有效时长', () => {
  const t = createElapsedTracker()
  t.start(1000)
  t.pause(3000) // 录了 2s
  assert.equal(t.elapsed(8000), 2000) // 暂停 5s 期间不变
  t.start(8000) // 继续
  assert.equal(t.elapsed(9000), 3000) // 2s + 1s
})

test('elapsed：多次暂停/继续循环只累计录音区间', () => {
  const t = createElapsedTracker()
  t.start(0)
  t.pause(1000)
  t.start(5000)
  t.pause(7000)
  t.start(10000)
  t.pause(10500)
  assert.equal(t.elapsed(99999), 1000 + 2000 + 500)
})

test('elapsed：重复 start/pause 幂等，不重复计入', () => {
  const t = createElapsedTracker()
  t.start(0)
  t.start(1000) // 重复 start：先结掉上一段（1s）
  t.pause(2000) // 再录 1s
  t.pause(9000) // 重复 pause：无效果
  assert.equal(t.elapsed(10000), 2000)
})

test('classifyRecovery：未删除 + 无转写 + 有分片 → recoverable', () => {
  assert.equal(
    classifyRecovery({ deletedAt: null, transcript: null, partCount: 3 }),
    'recoverable',
  )
  assert.equal(
    classifyRecovery({ deletedAt: null, transcript: '', partCount: 1 }),
    'recoverable',
  )
})

test('classifyRecovery：已转写 / 已删除 / 无分片 → none', () => {
  assert.equal(
    classifyRecovery({ deletedAt: null, transcript: '会议内容', partCount: 5 }),
    'none',
  )
  assert.equal(
    classifyRecovery({ deletedAt: 123, transcript: null, partCount: 5 }),
    'none',
  )
  assert.equal(
    classifyRecovery({ deletedAt: null, transcript: null, partCount: 0 }),
    'none',
  )
})

test('partsToBlob：空分片返回 null', () => {
  assert.equal(partsToBlob([]), null)
})

test('partsToBlob：多分片按序拼接且字节不丢', async () => {
  // 两段各 3 字节（base64 每段 4 字符 + padding），
  // 字符串直连会因段内 '=' 截断——必须逐段解码拼接
  const a = btoa('abc')
  const b = btoa('xyz')
  const blob = partsToBlob([
    { seq: 0, mimeType: 'audio/webm', dataBase64: a },
    { seq: 1, mimeType: 'audio/webm', dataBase64: b },
  ])
  assert.ok(blob)
  const text = await blob.text()
  assert.equal(text, 'abcxyz')
  assert.equal(blob.type, 'audio/webm')
})

test('partsToBlob：乱序入参时保持入参顺序（调用方负责按 seq 排序）', async () => {
  const blob = partsToBlob([
    { seq: 1, mimeType: 'audio/webm', dataBase64: btoa('YZ') },
    { seq: 0, mimeType: 'audio/webm', dataBase64: btoa('ab') },
  ])
  assert.ok(blob)
  assert.equal(await blob.text(), 'YZab')
})
