/**
 * 录音语音播报（recording-voice-prompt）回归测试 —— 2026-10-01。
 *
 * 需求：「在录音时，需要用扬声器播放一段语音，不是警告声。」
 * 这组测试锁住三件最容易回退的事：
 *  1. 播报走 TTS 语音，不退化成 beep/蜂鸣（源码里不得出现合成音调用）；
 *  2. 播报**不阻塞**录音启动（announce 同步返回 void）；
 *  3. 播报时**麦克风被静音**，否则「开始录音」四个字会被录进会议记录。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (rel) => readFileSync(join(HERE, rel), 'utf8')

const {
  promptTextFor,
  detectVoicePromptSupport,
  RecordingVoicePrompt,
  makeWebSpeaker,
} = await import('../recording-voice-prompt.ts')

const NATIVE = { nativeRegistered: true, webSpeechAvailable: false }
const WEB = { nativeRegistered: false, webSpeechAvailable: true }
const NONE = { nativeRegistered: false, webSpeechAvailable: false }

describe('promptTextFor', () => {
  it('录音状态节点都有自解释中文文案', () => {
    assert.equal(promptTextFor('start'), '开始录音')
    assert.equal(promptTextFor('stop'), '录音结束')
    assert.equal(promptTextFor('pause'), '录音已暂停')
    assert.equal(promptTextFor('resume'), '继续录音')
  })

  it('未知事件返回空串（调用方据此不播报），而不是念一句不相干的话', () => {
    assert.equal(promptTextFor('bogus'), '')
  })
})

describe('detectVoicePromptSupport', () => {
  it('原生插件已注册 → native（优先于 web）', () => {
    assert.deepEqual(
      detectVoicePromptSupport({ nativeRegistered: true, webSpeechAvailable: true }),
      { supported: true, engine: 'native' },
    )
  })

  it('只有浏览器 speechSynthesis → web', () => {
    assert.deepEqual(detectVoicePromptSupport(WEB), { supported: true, engine: 'web' })
  })

  it('两者都没有 → none 且 supported=false（设置页应显示不可用）', () => {
    assert.deepEqual(detectVoicePromptSupport(NONE), { supported: false, engine: 'none' })
  })
})

describe('RecordingVoicePrompt · 基础契约', () => {
  it('announce 同步返回 void —— 播报不阻塞录音启动', async () => {
    let called = 0
    const p = new RecordingVoicePrompt(NATIVE, async () => { called++ })
    const ret = p.announce('start')
    assert.equal(ret, undefined, 'announce 若返回 promise，调用点 await 就会拖慢录音启动')
    await p.drain()
    assert.equal(called, 1)
  })

  it('用 TTS 引擎播报文本，而不是合成一个 beep', async () => {
    const spoken = []
    const p = new RecordingVoicePrompt(WEB, async (engine, text) => { spoken.push([engine, text]) })
    p.announce('start')
    p.announce('stop')
    await p.drain()
    assert.deepEqual(spoken, [['web', '开始录音'], ['web', '录音结束']])
  })

  it('连续事件串行播报，不会互相打断/叠音', async () => {
    const order = []
    const p = new RecordingVoicePrompt(NATIVE, async (_e, text) => {
      order.push(`enter:${text}`)
      await new Promise((r) => setTimeout(r, 5))
      order.push(`exit:${text}`)
    })
    p.announce('start')
    p.announce('pause')
    await p.drain()
    assert.deepEqual(order, [
      'enter:开始录音', 'exit:开始录音', 'enter:录音已暂停', 'exit:录音已暂停',
    ])
  })

  it('引擎抛异常被吞掉：不 reject，也不影响后续节点', async () => {
    const spoken = []
    let first = true
    const p = new RecordingVoicePrompt(WEB, async (_e, text) => {
      if (first) { first = false; throw new Error('TTS engine gone') }
      spoken.push(text)
    })
    p.announce('start')
    p.announce('stop')
    await p.drain() // 若第一个错误冒泡，这里会 reject
    assert.deepEqual(spoken, ['录音结束'], '一个节点播报失败不应连累后面的节点')
  })

  it('无 TTS 引擎时静默返回，不排空队列', async () => {
    let called = 0
    const p = new RecordingVoicePrompt(NONE, async () => { called++ })
    p.announce('start')
    await p.drain()
    assert.equal(called, 0)
    assert.deepEqual(p.support(), { supported: false, engine: 'none' })
  })

  it('setMuted(true) 后不播报', async () => {
    const spoken = []
    const p = new RecordingVoicePrompt(NATIVE, async (_e, t) => { spoken.push(t) })
    p.setMuted(true)
    p.announce('start')
    p.announce('stop')
    await p.drain()
    assert.equal(p.isMuted(), true)
    assert.deepEqual(spoken, [], '静音后连 start 都不该播')
  })

  it('clear() 丢弃排队内容：停止后不会突然响起「继续录音」', async () => {
    const spoken = []
    const p = new RecordingVoicePrompt(NATIVE, async (_e, t) => {
      spoken.push(t)
      await new Promise((r) => setTimeout(r, 5))
    })
    p.announce('pause')
    p.announce('resume')
    p.clear()
    p.announce('stop')
    await p.drain()
    assert.equal(spoken.includes('继续录音'), false)
    assert.equal(spoken[spoken.length - 1], '录音结束')
  })
})

describe('RecordingVoicePrompt · 麦克风静音保护', () => {
  it('播报期间麦克风被禁用，播完恢复 —— 否则「开始录音」会被录进会议', async () => {
    const track = { enabled: true }
    const observed = []
    const p = new RecordingVoicePrompt(NATIVE, async () => {
      observed.push(track.enabled) // 播报执行时，麦克风应为 disabled
    })
    p.announceSilenced('start', track, 50)
    await p.drain()
    assert.deepEqual(observed, [false], '播报期间麦克风必须在静音状态')
    assert.equal(track.enabled, true, '播报结束后必须恢复麦克风')
  })

  it('TTS 引擎吞掉 onend（永不 resolve）时，guardMs 兜底恢复麦克风', async () => {
    const track = { enabled: true }
    // 永不 resolve 的引擎：模拟内核丢事件
    const p = new RecordingVoicePrompt(NATIVE, () => new Promise(() => {}))
    p.announceSilenced('start', track, 30)
    assert.equal(track.enabled, false, '播报一开始麦克风就静音')
    await new Promise((r) => setTimeout(r, 60))
    assert.equal(track.enabled, true, 'guardMs 到期必须强制恢复，否则麦克风永久静音')
  })

  it('track 已被拆除时恢复失败也不能抛错（录音已结束的场景）', async () => {
    const track = {
      enabled: true,
      get enabled2() { return true },
    }
    const hostile = new Proxy({ enabled: true }, {
      get(t, k) {
        if (k === 'enabled') return t[k]
        return undefined
      },
      set(t, k, v) {
        // 模拟 track 结束后 set enabled 抛错的浏览器行为
        if (k === 'enabled' && v === true) throw new Error('InvalidStateError: track ended')
        t[k] = v
        return true
      },
    })
    void track
    const p = new RecordingVoicePrompt(NATIVE, async () => {})
    p.announceSilenced('start', hostile, 30) // 不应抛
    await new Promise((r) => setTimeout(r, 60))
  })

  it('无引擎或静音时不碰麦克风（不能因为「不播报」而误关麦克风）', async () => {
    const track = { enabled: true }
    const noEngine = new RecordingVoicePrompt(NONE, async () => {})
    noEngine.announceSilenced('start', track, 30)
    await noEngine.drain()
    assert.equal(track.enabled, true)

    const muted = new RecordingVoicePrompt(NATIVE, async () => {})
    muted.setMuted(true)
    muted.announceSilenced('start', track, 30)
    await muted.drain()
    assert.equal(track.enabled, true, '静音播报时绝不能动麦克风')
  })

  it('麦克风为 null（原生路径无 track）时不报错', async () => {
    const p = new RecordingVoicePrompt(NATIVE, async () => {})
    p.announceSilenced('start', null, 30)
    await p.drain()
  })
})

describe('makeWebSpeaker', () => {
  it('onend 触发时 resolve，并带中文语言标记', async () => {
    let utter = null
    const fake = {
      speechSynthesis: { speak: (u) => { utter = u; setTimeout(() => u.onend(), 1) } },
      SpeechSynthesisUtterance: class { constructor(t) { this.text = t } },
    }
    await makeWebSpeaker(fake)('web', '开始录音')
    assert.equal(utter.text, '开始录音')
    assert.equal(utter.lang, 'zh-CN')
  })

  it('onend 丢失时 4s 兜底定时器兜住 —— 队列不能因此永久卡死', async () => {
    const fake = {
      speechSynthesis: { speak: () => { /* 内核丢 onend 事件 */ } },
      SpeechSynthesisUtterance: class { constructor(t) { this.text = t } },
    }
    const sp = makeWebSpeaker(fake)
    const r = await Promise.race([
      sp('web', '录音结束').then(() => 'resolved'),
      new Promise((res) => setTimeout(() => res('hung'), 5500)),
    ])
    assert.equal(r, 'resolved')
  })

  it('引擎抛异常时 resolve（不 reject）', async () => {
    const fake = {
      speechSynthesis: { speak: () => { throw new Error('no voices') } },
      SpeechSynthesisUtterance: class { constructor(t) { this.text = t } },
    }
    await makeWebSpeaker(fake)('web', '开始录音') // 不应 reject
  })
})

describe('录音提示契约（源码级）', () => {
  const src = read('../recording-voice-prompt.ts')

  it('不使用合成音（AudioContext / Oscillator）做提示', () => {
    for (const forbidden of ['createOscillator', 'OscillatorNode', 'new Audio(']) {
      assert.equal(
        src.includes(forbidden), false,
        `录音声提示不得用合成音（${forbidden}）——需求明确要「一段语音」而不是警告声`,
      )
    }
  })

  it('两个录音 runtime 都接入了语音播报', () => {
    const runtime = read('../recordingRuntime.ts')
    assert.ok(runtime.includes('recording-voice-prompt'), 'recordingRuntime 未接入语音播报模块')
    assert.ok(runtime.includes("announceSilenced('start'"), '录音开始未接语音播报')
    assert.ok(runtime.includes("announce('stop')"), '录音停止未接语音播报')
  })
})

describe('录音停止链路的转写能力（源码级）', () => {
  const runtime = read('../recordingRuntime.ts')

  it('停止时用全量转写端点，而不是单次转写', () => {
    // 任何 ASR 都不允许无限长音频单次上传（智谱 30 秒 / OpenRouter ~60 秒）。
    // 若停止兜底仍走单次端点，超过上限的录音会直接失败，表现为
    //「录了五分钟一句话都转不出来」。
    assert.ok(
      runtime.includes('transcribeFull'),
      '停止兜底未使用全量转写端点（长录音会因超过上游单次上限而失败）',
    )
  })

  it('全量转写有显式超时，不会把 phase 卡在 stopping', () => {
    // withTimeout 是这里唯一防止「录音按钮永久锁死」的东西。
    assert.ok(
      /transcribeFull[\s\S]{0,400}withTimeout|withTimeout[\s\S]{0,400}transcribeFull/.test(runtime),
      '全量转写未走 withTimeout（后端不响应时 phase 永远停在 stopping）',
    )
  })

  it('部分段失败要如实告诉用户，而不是当作完整记录', () => {
    assert.ok(
      runtime.includes('result.failed'),
      '未处理「有段失败」的情况，用户会以为记录是完整的',
    )
  })

  it('文件名推导与单次转写共用同一张映射表', () => {
    const stt = read('../../api/stt.ts')
    const shared = read('../../api/stt-filename.ts')
    assert.ok(shared.includes('filenameForMimeType'), '共享模块未导出 filenameForMimeType')
    // 两处各写一份映射表会导致「单次能转、全量转不出」
    assert.ok(
      stt.includes("from './stt-filename'"),
      'stt.ts 未复用共享的 filenameForMimeType（会与全量路径的映射表漂移）',
    )
    assert.ok(
      !/EXTENSION|audio\/mp4:\s*'m4a'/.test(stt),
      'stt.ts 里仍有内联映射表，应删掉以免与共享实现分叉',
    )
  })
})

describe('即时转写接线（源码级，2026-10-01）', () => {
  const runtime = read('../recordingRuntime.ts')

  it('分片走即时端点，而不是单次转写', () => {
    // 3 秒定长切片必然切在词中间，各片独立转写会在交界处重复识别同一个词，
    // 本地拼接得到「今天今天下午三点」。只有带 sessionId 的即时端点做跨片去重。
    assert.ok(runtime.includes('transcribeIncremental'), '分片未使用即时转写端点')
    assert.ok(runtime.includes('sessionId: this.sttSessionId'), '未传 sessionId（服务端无法跨片去重）')
  })

  it('分片必须串行发送', () => {
    // 服务端按到达顺序累积会话文本；并发发送会让响应乱序返回而会话已被改写，
    // 表现为网络抖动时文本偶发跳变丢字。
    assert.ok(runtime.includes('sliceChain'), '缺少串行链（sliceChain）')
    assert.ok(
      /sliceChain = this\.sliceChain\.then\(/.test(runtime),
      '分片未通过 sliceChain 串行化',
    )
  })

  it('服务端返回的累计文本要整体替换，不能本地拼接', () => {
    assert.ok(
      /this\.committed = res\.text\.trim\(\)/.test(runtime),
      '未整体替换 committed（本地拼接会把边界重复字叠加）',
    )
    assert.ok(
      !/appendTranscript\(this\.committed, this\.transcript\.value, '', res\.text\)/.test(runtime),
      '仍在用 appendTranscript 拼接服务端已去重的文本',
    )
  })

  it('定长切片不得声明为静音切', () => {
    // 3 秒定长切片是硬切。谎报 silenceCut 会让服务端按静音边界去重，
    // 反而吃掉真实的相邻文字。
    assert.ok(
      /silenceCut:\s*false/.test(runtime),
      '定长切片未显式声明 silenceCut: false',
    )
  })

  it('停止时最后一片要送出并带 isFinal', () => {
    // recorder.stop() 派发的最后一片通常正是用户最后说的那句，
    // 漏掉它等于丢掉结尾。不带 isFinal 则服务端会话要等 LRU 才释放。
    assert.ok(runtime.includes('releaseOnNextSlice'), '未处理停止时的最后一片')
    assert.ok(runtime.includes('isFinal: true') || runtime.includes(', true)'), '最后一片未带 isFinal')
  })

  it('停止时等在途分片落库，但有超时上限', () => {
    // 无上限的话一个挂死的分片请求会让 phase 停在 'stopping'，录音按钮锁死。
    assert.ok(
      /await Promise\.race\(\[\s*this\.sliceChain/.test(runtime),
      '停止时未等待在途分片',
    )
    assert.ok(
      /sliceChain,[\s\S]{0,120}setTimeout/.test(runtime),
      '等待在途分片没有超时上限（会锁死录音按钮）',
    )
  })

  it('单片失败不清空已有文本', () => {
    assert.ok(
      /if \(res\.error\)[\s\S]{0,200}return/.test(runtime),
      '单片失败时应提前返回，不覆盖已有累计文本',
    )
  })

  it('每场录音生成新的会话 id', () => {
    assert.ok(
      /this\.sttSessionId = `note-\$\{Date\.now\(\)/.test(runtime),
      '未在 start() 里为每场录音生成新会话 id（会跨会话沿用去重状态）',
    )
  })
})

