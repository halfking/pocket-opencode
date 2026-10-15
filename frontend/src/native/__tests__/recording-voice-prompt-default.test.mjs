/**
 * 录音语音播报的**默认关闭**契约（2026-10-07）。
 *
 * 需求原文（用户）：「开启与关闭录音不要有语音提示」。
 *
 * 为什么这道门是行为门而不是源码门：
 *   「默认开还是关」这件事，真身在 recordingRuntime.ts 的 localStorage 读取里，
 *   而那个文件一 import Capacitor 在 Node 里就跑不起来 —— 只能在源码层断言
 *   「有没有写出某个字符串」，那测不出**默认到底是什么**。
 *   把判定抽成纯函数 `voicePromptEnabledByDefault`（在可 import 的
 *   recording-voice-prompt.ts 里）之后，这里能直接跑它的返回值。
 *
 * ⚠ 这里的边角值是**故意挑的**：`'true'` / `'0'` / `''` 都必须判**关闭**。
 *   一个写成 `stored !== '0'` 或 `stored === 'true'` 的实现，在最常见的
 *   「从来没设过」的情况下仍然会判关闭 —— 看起来对，但只要用户显式存过一次
 *   非 `'1'` 的值就会翻车。这类 bug 只有真跑一遍边界值才暴露。
 */

import assert from 'node:assert/strict'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const {
  voicePromptEnabledByDefault, RecordingVoicePrompt,
} = await import('../recording-voice-prompt.ts')

const HERE = dirname(fileURLToPath(import.meta.url))
void HERE
void join

const NATIVE = { nativeRegistered: true, webSpeechAvailable: false }

describe('录音语音提示的默认值', () => {
  it('只有显式写成 "1" 才算开启', () => {
    assert.equal(voicePromptEnabledByDefault('1'), true)
  })

  it('「没设过」的各种形态一律关闭', () => {
    for (const stored of [null, undefined, '', '0', 'false', 'true', 'on', 'YES']) {
      assert.equal(
        voicePromptEnabledByDefault(stored), false,
        `stored=${JSON.stringify(stored)} 必须判关闭 —— 默认选错的方向要是安静，不是吵`,
      )
    }
  })

  it('负控：一个「默认开启」的实现必须被本门抓到（用合成样本，不依赖活代码）', () => {
    // 两种看起来合理、实际会把默认值放错方向的写法
    const defaultOn = (stored) => stored !== '0'
    const parseTruthy = (stored) => stored === 'true'
    for (const stored of [null, undefined, '']) {
      assert.equal(defaultOn(stored), true)
      assert.equal(parseTruthy(stored), false)
    }
    // 真实现与它们在「显式关闭」这一格上就分道扬镳
    assert.equal(voicePromptEnabledByDefault('1'), true)
    assert.equal(parseTruthy('1'), false)
    assert.notDeepEqual(defaultOn('1'), voicePromptEnabledByDefault('0'))
  })
})

describe('关闭后录音开关节点不播报', () => {
  it('muted 时 start / stop / pause / resume 全部静默', async () => {
    const spoken = []
    const p = new RecordingVoicePrompt(NATIVE, async (_engine, text) => { spoken.push(text) })
    p.setMuted(true)
    const track = { enabled: true }

    p.announce('start')
    p.announceSilenced('start', track)
    p.announce('stop')
    p.announceSilenced('stop', track)
    p.announce('pause')
    p.announce('resume')
    p.announce('error')
    await p.drain()

    assert.deepEqual(spoken, [], `关闭后不该有任何播报，实得 ${JSON.stringify(spoken)}`)
  })

  it('负控：同一个实例不 muted 时确实会播 —— 否则上一条是恒真', async () => {
    const spoken = []
    const p = new RecordingVoicePrompt(NATIVE, async (_engine, text) => { spoken.push(text) })
    p.setMuted(false)
    p.announce('start')
    p.announce('stop')
    await p.drain()
    assert.deepEqual(spoken, ['开始录音', '录音结束'])
  })

  it('关闭不会把麦克风静音开关留在关的状态', async () => {
    // announceSilenced 会临时关麦克风；关闭状态下它根本不该动 track ——
    // 若实现先静音再判静音，录音会以「麦克风被关」的形式坏掉，且不报错。
    const p = new RecordingVoicePrompt(NATIVE, async () => {})
    p.setMuted(true)
    const track = { enabled: true }
    p.announceSilenced('start', track)
    await p.drain()
    assert.equal(track.enabled, true, '关闭状态下不得动麦克风采集开关')
  })
})