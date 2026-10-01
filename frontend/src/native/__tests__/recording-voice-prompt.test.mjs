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

describe('BUG-AU · 系统 TTS 抢前台的自愈降级（2026-10-01 真机确证）', () => {
  // MIUI 上开始录音 → TextToSpeech.speak() → 系统「系统语音引擎」拉起授权页
  // （索要「录制音频」）→ 页面 visibilityState=hidden → WebView 被节流
  // （rAF 停、setInterval 钳到 1/min）→ 全局录音指示条时钟冻在最后一帧。
  // 真机受控对照：未录音 0/3 被抢，录音中 8/8；手动关掉授权页后再录仍 6/6。
  it('播报把页面从 visible 打成 hidden → 判定为劫持，之后不再播报', async () => {
    const spoken = []
    let vis = 'visible'
    let notified = 0
    const p = new RecordingVoicePrompt(
      { ...NATIVE, readVisibility: () => vis, onForegroundHijack: () => { notified++ } },
      async (_e, t) => { spoken.push(t); vis = 'hidden' }, // 引擎把 App 打到后台
    )
    p.announce('start')
    await p.drain()
    assert.deepEqual(spoken, ['开始录音'], '第一次仍应播报（要先试出来）')
    assert.equal(p.hasForegroundHijack(), true, '必须判定为前台被抢')
    assert.equal(notified, 1, '降级结论要回调出去以便持久化')

    p.announce('stop')
    await p.drain()
    assert.deepEqual(spoken, ['开始录音'], '判定劫持后必须彻底闭嘴，不能再打扰录音')
  })

  it('正常播报（可见性不变）绝不能被误降级', async () => {
    const spoken = []
    let notified = 0
    const p = new RecordingVoicePrompt(
      { ...NATIVE, readVisibility: () => 'visible', onForegroundHijack: () => { notified++ } },
      async (_e, t) => { spoken.push(t) },
    )
    p.announce('start')
    await p.drain()
    p.announce('stop')
    await p.drain()
    assert.deepEqual(spoken, ['开始录音', '录音结束'], '正常设备上播报必须照常工作')
    assert.equal(p.hasForegroundHijack(), false, '没被打断就不该降级')
    assert.equal(notified, 0)
  })

  it('播报前页面本来就是后台（用户自己切走了）→ 不算引擎的锅', async () => {
    let notified = 0
    const p = new RecordingVoicePrompt(
      { ...NATIVE, readVisibility: () => 'hidden', onForegroundHijack: () => { notified++ } },
      async () => {},
    )
    p.announce('start')
    await p.drain()
    assert.equal(p.hasForegroundHijack(), false, '播报前后都是 hidden，不构成「被抢」')
    assert.equal(notified, 0)
  })

  it('restoreForegroundHijack 能从持久化恢复降级（重启后不再被同一个弹窗打断）', async () => {
    const spoken = []
    const p = new RecordingVoicePrompt(NATIVE, async (_e, t) => { spoken.push(t) })
    p.restoreForegroundHijack()
    p.announce('start')
    await p.drain()
    assert.deepEqual(spoken, [], '已标记降级的机器上不应该再播报')
  })

  it('降级路径自身抛错也不该影响录音（持久化失败必须被吞掉）', async () => {
    let vis = 'visible'
    const spoken = []
    const p = new RecordingVoicePrompt(
      {
        ...NATIVE,
        readVisibility: () => vis,
        onForegroundHijack: () => { throw new Error('localStorage 不可用') },
      },
      async (_e, t) => { spoken.push(t); vis = 'hidden' },
    )
    p.announce('start')
    await p.drain()
    assert.equal(p.hasForegroundHijack(), true, '即使回调抛错，降级结论本身也要生效')
    p.announce('stop')
    await p.drain()
    assert.deepEqual(spoken, ['开始录音'], '回调抛错后仍要闭嘴，且不能因为抛错而中断')
  })

  it('runtime 侧确实接上了可见性读取与持久化键', () => {
    const runtime = read('../recordingRuntime.ts')
    assert.ok(runtime.includes('readVisibility'), 'runtime 未传 readVisibility，降级永不触发')
    assert.ok(runtime.includes('VOICE_PROMPT_HIJACK_KEY'), 'runtime 未持久化降级结论')
    assert.ok(runtime.includes('restoreForegroundHijack'), 'runtime 未恢复上次的降级结论')
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
