/**
 * recorderMime 单测(2026-09-21 真机回归补充)。
 *
 * 真机 redmi Android WebView(Chromium 内核)经常只支持 audio/mp4,audio/webm
 * 在某些固件上要么直接报 NotSupported,要么 timeslice 不下 dataavailable。
 * pickSupportedRecorderMime 必须按 webm→opus→mp4→aac 顺序探测,都失败时
 * 留空让浏览器走默认路径。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  SUPPORTED_RECORDER_MIME_CANDIDATES,
  pickSupportedRecorderMime,
} from '../recorderMime.ts'

test('候选链固定,任何改动都要更新真机验证', () => {
  assert.deepEqual(
    [...SUPPORTED_RECORDER_MIME_CANDIDATES],
    [
      'audio/webm;codecs=opus',
      'audio/webm',
      'audio/mp4;codecs=mp4a.40.2',
      'audio/mp4',
    ],
    'MIME 候选顺序不能乱,opus 必须在最前,mp4 系列排在 webm 之后',
  )
})

test('没有传入 probe 时返回空串,交给上层走默认路径', () => {
  assert.equal(pickSupportedRecorderMime(), '')
  assert.equal(pickSupportedRecorderMime(undefined), '')
})

test('只支持 audio/mp4(redmi 真机实际场景)时挑带 codec 的 mp4', () => {
  const got = pickSupportedRecorderMime({
    isTypeSupported: (mime) => mime === 'audio/mp4;codecs=mp4a.40.2' || mime === 'audio/mp4',
  })
  assert.equal(got, 'audio/mp4;codecs=mp4a.40.2')
})

test('所有 mime 都支持时挑第一个(opus 在前)', () => {
  const got = pickSupportedRecorderMime({
    isTypeSupported: () => true,
  })
  assert.equal(got, 'audio/webm;codecs=opus')
})

test('isTypeSupported 抛错时仍继续测试下一个 mime,不能整体挂死', () => {
  const got = pickSupportedRecorderMime({
    isTypeSupported: (m) => {
      if (m === 'audio/webm;codecs=opus') throw new Error('mock crash')
      if (m === 'audio/webm') throw new Error('mock crash')
      return m === 'audio/mp4'
    },
  })
  assert.equal(got, 'audio/mp4')
})

test('没有任何 mime 支持时返回空串(让浏览器走默认)', () => {
  const got = pickSupportedRecorderMime({
    isTypeSupported: () => false,
  })
  assert.equal(got, '')
})

test('webm 系列不可用时降级 mp4,aac 不支持时 fallback 到 mp4 裸 mime', () => {
  const got = pickSupportedRecorderMime({
    isTypeSupported: (m) => m === 'audio/mp4',
  })
  assert.equal(got, 'audio/mp4')
})
