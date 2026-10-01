/**
 * recording-voice-prompt — 录音关键节点的扬声器语音播报（2026-10-01）。
 *
 * 需求原文：「在录音时，需要用扬声器播放一段语音，不是警告声。」
 *
 * 为什么不是警告声（beep / 蜂鸣）：
 * 录音场景里「提示音」有个致命的歧义——用户听到一声短促的滴声，无法判断
 * 是「录音已经收进去了」（提示音被录进成品）还是「设备出错」。更要命的是
 * 提示音本身会被录进音频：那段「滴——」变成会议记录开头的一处噪音，而且
 * 用户往往回放时才发现，那时会议已经记完、无法补救。语音「开始录音」这
 * 四个字是自解释的：听清了就知道状态，也知道它不是会议内容。
 *
 * 与既有能力的关系：
 *  - 触觉（useHaptics / navigator.vibrate）保留，作为语音的**补充**而非替代。
 *    会议室里把音量调到 0 很常见，纯语音会漏；戴耳机时纯触觉又无感。
 *  - 复用 useSpeech 已打通的两个引擎（原生 @capacitor-community/text-to-speech
 *    与 Web speechSynthesis），不引入第二套 TTS 栈。
 *
 * 关键约束：**绝不阻塞录音**。播报是 fire-and-forget，任何异常都静默吞掉——
 * 提示音播不出来不能让用户开不了录音。
 *
 * 纯函数（promptTextFor / detectVoicePromptSupport）抽出供 node 单测直接加载。
 */

/** 播报节点：录音生命周期里用户需要知道状态切换的时刻。 */
export type RecordingEvent = 'start' | 'stop' | 'pause' | 'resume' | 'error'

/** 播报文案。新增语言请在此处补 key，不要在调用点散落字面量。 */
const PROMPTS: Record<RecordingEvent, string> = {
  start: '开始录音',
  stop: '录音结束',
  pause: '录音已暂停',
  resume: '继续录音',
  error: '录音出错',
}

/**
 * 取播报文案。未知事件返回空串而不是兜底成某句——调用方据此判定「不播报」，
 * 比念一句不相干的话安全。
 */
export function promptTextFor(event: RecordingEvent): string {
  return PROMPTS[event] ?? ''
}

/** TTS 引擎可用性探测结果。 */
export interface VoicePromptSupport {
  /** 是否有任一 TTS 引擎可用 */
  supported: boolean
  /** 可用引擎名，用于设置页展示与故障排查 */
  engine: 'native' | 'web' | 'none'
}

/** 引擎探测依赖，注入以便单测构造确定性环境。 */
export interface VoicePromptDeps {
  /** 原生 TTS 插件是否已注册（Capacitor.isPluginAvailable('TextToSpeech')） */
  nativeRegistered: boolean
  /** 浏览器是否具备 speechSynthesis */
  webSpeechAvailable: boolean
  /**
   * 读当前页面可见性。用于检测「系统 TTS 把 App 打到后台」。
   * 缺省视为**始终可见**——即不做自动降级（纯 Web 单测/无 DOM 环境安全）。
   */
  readVisibility?: () => 'visible' | 'hidden'
  /**
   * 检出「系统 TTS 抢走前台」时回调一次，用于持久化（重启后不再播报）。
   * 抛错必须被吞掉——降级路径本身绝不能让录音失败。
   */
  onForegroundHijack?: () => void
}

/**
 * 探测当前运行时有没有 TTS 引擎。
 *
 * 刻意**不**用 `Capacitor.isNativePlatform()` 一刀切：那个判据在「WebView 里
 * 已注册插件但插件实际不可用」（无 TTS 数据的模拟器就是）时会误报可用。
 */
export function detectVoicePromptSupport(deps: VoicePromptDeps): VoicePromptSupport {
  if (deps.nativeRegistered) return { supported: true, engine: 'native' }
  if (deps.webSpeechAvailable) return { supported: true, engine: 'web' }
  return { supported: false, engine: 'none' }
}

/** 播报引擎签名：注入后单测可完全脱离真实 TTS。 */
export type VoiceSpeaker = (engine: 'native' | 'web', text: string) => Promise<void>

/** 最小麦克风轨接口：只需要 enabled 开关，便于单测用假对象。 */
export interface MicTrackLike {
  enabled: boolean
}

/**
 * RecordingVoicePrompt — 录音语音播报器。
 *
 * 设计要点：
 *  1. 串行队列：状态切换可能连续发生（快速点暂停/继续），两个引擎同时 speak
 *     会互相打断或叠音，播报内容错乱。用一条 Promise 链串起来。
 *  2. 不 await：announce() 同步返回 void，调用点一行搞定，不拖慢录音启动。
 *  3. 全异常静默：TTS 引擎缺失/抛错/被系统打断，都不能让录音链路失败。
 *  4. clear() 丢弃待播内容：连点停止时，队列里排队的「继续录音」不该在录音
 *     已经结束后才念出来。
 */
export class RecordingVoicePrompt {
  private queue: Promise<void> = Promise.resolve()
  /**
   * 队列代号。每次 clear() 递增。排队任务捕获入队时的代号，真正执行时若
   * 发现代号已变，说明期间发生过 clear()，直接丢弃。
   *
   * 为什么不用一个布尔 `dropped` 标志：用户「暂停 → 继续 → 停止」连点时，
   * stop() 会先 clear() 再 announce('stop')。若 announce 把 dropped 重置回
   * false，队列里排着的「继续录音」就会被念出来 —— 录音已经结束了，用户却
   * 听到「继续录音」，这是明确的错误反馈。布尔标志无法区分「入队时的状态」
   * 和「当前状态」，代号可以。
   */
  private generation = 0
  private muted = false
  /** 系统 TTS 是否已把本 App 打到后台过（见 speakSafely 的降级判定）。 */
  private foregroundHijacked = false
  private readonly deps: VoicePromptDeps
  private readonly speak: VoiceSpeaker

  constructor(deps: VoicePromptDeps, speak: VoiceSpeaker) {
    this.deps = deps
    this.speak = speak
  }

  /** 关闭播报（用户在设置里关掉语音提示）。已排队内容也一并丢弃。 */
  setMuted(muted: boolean) {
    this.muted = muted
    if (muted) this.clear()
  }

  isMuted(): boolean {
    return this.muted
  }

  /**
   * 系统 TTS 是否已把本 App 打到后台过。
   *
   * 设置页据此把「语音提示」显示成「已自动关闭（系统语音引擎会打断录音界面）」，
   * 而不是让用户以为功能坏了 —— 播报失败本来就是静默的（见 probeVoicePromptSupport）。
   */
  hasForegroundHijack(): boolean {
    return this.foregroundHijacked
  }

  /** 启动时恢复上次的降级结论（持久化在调用方，这里只管置位）。 */
  restoreForegroundHijack(): void {
    this.foregroundHijacked = true
  }

  support(): VoicePromptSupport {
    return detectVoicePromptSupport(this.deps)
  }

  /**
   * 播报一个录音事件。**永不 reject、永不 await**。
   *
   * 播报内容先过两道过滤：静音开关、引擎可用性。都不通过就直接返回、不占队列
   * ——否则关掉播报的用户仍要为每个节点排一个空 promise。
   */
  announce(event: RecordingEvent): void {
    const plan = this.plan(event)
    if (!plan) return
    const gen = this.generation
    this.queue = this.queue
      .then(() => this.speakSafely(gen, plan.engine, plan.text))
      .catch(() => { /* 引擎异常：静默，不影响录音 */ })
  }

  /**
   * 播报的同时**静音麦克风采集**（`announce` 的录音安全版）。
   *
   * 这是本模块最容易踩的坑：需求说「录音时用扬声器播放一段语音」，直觉实现
   * 就是采集照常跑、扬声器照常响——于是「开始录音」这四个字被麦克风一起收了
   * 进去，成为会议记录的第一句。
   *
   * 三种解法与取舍：
   *  - A. 先播报完再开录：语义最干净，但每次启动多等 1-2 秒，会议里很别扭。
   *  - B. 照常录，转写后正则剔除：脆弱——换文案就漏，且噪音已留在音频里。
   *  - C. **本实现**：MediaRecorder 照常跑（chunk 时序连续、不丢开头），只把 mic
   *    track 置 disabled，播报结束（或 guardMs 到期）后恢复。采到的是静音帧，
   *    VAD 与转写都会自然忽略。
   *
   * guardMs 是**安全兜底**，不是可选优化：TTS 引擎的 onend 可能不触发（见
   * makeWebSpeaker），没有兜底就会让麦克风永久静音——那比录进四个字严重得多。
   */
  announceSilenced(event: RecordingEvent, micTrack?: MicTrackLike | null, guardMs = 4000): void {
    const plan = this.plan(event)
    if (!plan) return

    const restore = () => {
      try { if (micTrack) micTrack.enabled = true } catch { /* track 已结束 */ }
    }
    try { if (micTrack) micTrack.enabled = false } catch { /* track 已结束 */ }

    const gen = this.generation
    this.queue = this.queue
      .then(() => this.speakSafely(gen, plan.engine, plan.text))
      .catch(() => { /* 引擎异常：静默 */ })
      .finally(restore)
    setTimeout(restore, guardMs)
  }

  /**
   * 清空待播内容（录音停止时调用）。
   *
   * 场景：用户「暂停 → 继续 → 停止」连点三下，「继续录音」可能还排在引擎队列
   * 里没念。停止后突然响起「继续录音」是明显的错误反馈。
   */
  clear(): void {
    this.generation++
  }

  /** 供测试与优雅停机等待队列排空。 */
  async drain(): Promise<void> {
    await this.queue
  }

  /** 前置过滤：静音 / 未知事件 / 无引擎 / 已被系统抢占过 → 返回 null 表示「本节点不播报」。 */
  private plan(event: RecordingEvent): { engine: 'native' | 'web'; text: string } | null {
    if (this.muted || this.foregroundHijacked) return null
    const text = promptTextFor(event)
    if (!text) return null
    const { supported, engine } = this.support()
    if (!supported || engine === 'none') return null
    return { engine, text }
  }

  private async speakSafely(gen: number, engine: 'native' | 'web', text: string): Promise<void> {
    if (gen !== this.generation) return // 入队后发生过 clear()，内容作废
    // 播报前先记可见性：系统 TTS 若把 App 打到后台，播报本身就是破坏者。
    const visBefore = this.visibility()
    try {
      await this.speak(engine, text)
    } catch {
      // 无 TTS 引擎 / 引擎被系统回收 / 权限异常：提示音缺失不应打断录音。
    }
    this.detectForegroundHijack(visBefore)
  }

  private visibility(): 'visible' | 'hidden' {
    try {
      return this.deps.readVisibility?.() ?? 'visible'
    } catch {
      return 'visible'
    }
  }

  /**
   * 「播报把 App 打到后台」自愈降级（BUG-AU，2026-10-01 真机确证）。
   *
   * 现场：MIUI 上开始会议录音 → announceSilenced('start') → TextToSpeech.speak()
   * → 系统「系统语音引擎」拉起授权页（索要「录制音频」）并抢走前台
   * → 页面 visibilityState 变 hidden → WebView 被节流（rAF 停、setInterval 钳到
   *   1/min）→ 全局录音指示条的时钟冻在最后一帧。真机受控对照：未录音时 0/3
   *   被抢，开始录音后 8/8 被抢；手动关掉授权页后再录仍然 6/6 —— **不是一次性
   *   首启体验，是每次录音都被劫持**。
   *
   * 为什么必须让位：需求是「录音时播一段语音」，但**录音本身绝不能被打断**是
   * 更高阶的要求——被系统弹窗盖住时用户既看不到录音界面也停不下，而播报只是
   * 锦上添花。所以这里不是「禁用 TTS 功能」，而是「一旦发现它会破坏录音，就
   * 本机永久让位」，并把结论回调给调用方持久化。
   *
   * 判定用**播报前后的可见性差**，不用设备白名单：白名单要维护、且换个 ROM
   * 就失效；可见性是系统给的客观事实，跨设备通用。
   */
  private detectForegroundHijack(visBefore: 'visible' | 'hidden'): void {
    if (this.foregroundHijacked) return
    if (visBefore !== 'visible' || this.visibility() !== 'hidden') return
    this.foregroundHijacked = true
    try { this.deps.onForegroundHijack?.() } catch { /* 持久化失败不该影响录音 */ }
  }
}

/**
 * 探测当前运行时的语音播报可用性（供设置页展示）。
 *
 * 为什么要暴露这个：播报失败是**静默**的——没有 TTS 引擎时 announce() 直接
 * 返回，用户听不到任何声音，只会认为「这功能没做」或「手机坏了」。
 * 国内 ROM 常移除或禁用 Google TTS（无 GMS 设备尤其常见），所以这不是
 * 理论风险。设置页把结论显式写出来，用户才知道该做什么。
 *
 * 顺带返回引擎名，便于区分「原生插件不可用」与「系统没有 TTS」。
 */
export function probeVoicePromptSupport(): VoicePromptSupport & {
  /** 人话结论，直接可展示。 */
  reason: string
} {
  const s = detectVoicePromptSupport({
    nativeRegistered: safePluginCheck(),
    webSpeechAvailable: typeof window !== 'undefined'
      && 'speechSynthesis' in window
      && typeof SpeechSynthesisUtterance !== 'undefined',
  })
  let reason: string
  if (s.engine === 'native') {
    reason = '可用（设备原生语音引擎）'
  } else if (s.engine === 'web') {
    reason = '可用（浏览器语音引擎）'
  } else {
    reason = '不可用：本设备未提供语音引擎，录音时不会有语音提示（可改用震动）'
  }
  return { ...s, reason }
}

/** Capacitor.isPluginAvailable 在极早期或非原生环境可能抛错，必须兜住。 */
function safePluginCheck(): boolean {
  try {
    // 动态 require 避免在纯 Web 环境下把 Capacitor 拉进关键路径；
    // 这里直接用全局注册表判断，不依赖 isNativePlatform。
    const reg = (globalThis as { Capacitor?: { isPluginAvailable?: (n: string) => boolean } }).Capacitor
    return reg?.isPluginAvailable?.('TextToSpeech') === true
  } catch {
    return false
  }
}

/**
 * 浏览器 speechSynthesis 播报实现（工厂函数，便于注入测试）。
 *
 * 踩过的坑：Android WebView 的 speechSynthesis.getVoices() 首次调用常返回空
 * 数组（voiceschanged 事件还没到），此时不设 voice，让内核用默认声音。
 * 另一个坑：部分内核会丢 onend 事件，队列因此永久卡死——后续所有播报静默。
 * 所以这里必须有兜底定时器。
 */
export function makeWebSpeaker(
  win: {
    speechSynthesis: { speak: (u: SpeechSynthesisUtterance) => void }
    SpeechSynthesisUtterance: new (text: string) => SpeechSynthesisUtterance
  },
): VoiceSpeaker {
  return (_engine, text) =>
    new Promise<void>((resolve) => {
      try {
        const utter = new win.SpeechSynthesisUtterance(text)
        utter.lang = 'zh-CN'
        utter.rate = 1.05
        let settled = false
        const done = () => {
          if (settled) return
          settled = true
          resolve()
        }
        utter.onend = done
        utter.onerror = done
        win.speechSynthesis.speak(utter)
        setTimeout(done, 4000)
      } catch {
        resolve()
      }
    })
}
