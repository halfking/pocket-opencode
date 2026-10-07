/**
 * recordingRuntime — 录音域的进程级所有权上移(2026-09-20 全任务后台化 P0)。
 *
 * 背景:useMeetingRecorder/useNoteRecording 原先把录音状态绑在组件实例上,
 * onBeforeUnmount 无条件 cleanupMedia() → 会议/笔记详情页路由一切,录音流就被
 * 杀掉(Android 的 BackgroundMic 前台服务本就为后台录音设计,却被前端停掉)。
 *
 * 本模块沿用 M1(aiStreamRuntime,2026-09-09)的模式:
 * - 录音状态与方法收进进程级 singleton(globalThis 挂载,HMR 不重复);
 * - 组件 unmount ≠ 停止录音;停止只属于用户显式操作(页面停止按钮 / 全局
 *   录音指示条 RecordingPill / 新会议录音顶替旧录音);
 * - 麦克风独占:meeting 与 note 两个 runtime 互相可见,一方在录另一方拒绝;
 * - 页面级 composable(useMeetingRecorder/useNoteRecording)只做「按 id 圈定
 *   的只读视图 + 动作转发」:正在录别的会议时本页不亮录音态,避免串台。
 *
 * 与 aiStreamRuntime 的差异:录音依赖硬件(mic)与原生前台服务,进程被杀即
 * 终止(meeting 有 status='recording' 落库标记可检测遗留;note 无持久标记,
 * 进程死亡即结束 —— 已知边界,见设计文档 §2.1)。
 */
import { ref } from 'vue'
import { Capacitor } from '@capacitor/core'
import { pickSupportedRecorderMime } from './recorderMime'
import { RollingWebmDecoder, arrayBufferToBase64 } from './recording-audio-transcode'
import { sttFailureText } from '../api/stt-error'
import { saveMeetingAudio } from './meeting-audio'
import { VadSegmenter } from './vad-segmenter'
import { SpeakerDiarizer } from './speaker-diarization'
import { loadSpeakerProfiles, saveVoiceprint } from '../features/meetings/voiceprints-store'
import {
  updateMeeting, updateSegmentSpeaker, getMeeting, saveSegment, updateTranscript,
  type MeetingSegment,
} from '../features/meetings/meetings-store'
import { syncMeetingMetadata } from '../features/meetings/meeting-ingest'
import { ingestSpeechBlob } from '../features/meetings/ingest-speech'
import { renderTranscript } from '../features/meetings/meeting-dedup.ts'
import {
  applyCaptionResult, createLiveCaption, pickSpeechRecognition, type SpeechRecLike,
} from '../features/meetings/meeting-live-caption'
import { buildUtteranceSegment } from '../features/meetings/meeting-utterance'
import { useMicPermission } from '../composables/useMicPermission'
import { useToast } from '../composables/useToast'
import { openPreferredMicStream, listAudioInputs, type AudioInput } from './audio-inputs'
import {
  isBackgroundMicSupported, listNativeMicInputs, startBackgroundMic,
  stopBackgroundMic, listenBackgroundMicParts, nativePartToBlob,
} from './background-mic'
import { sttApi } from '../api/stt'
import { sttSettingsApi } from '../api/stt-settings'
import { filenameForMimeType } from '../api/stt-filename'
import { sherpa } from './sherpa'
import {
  RecordingVoicePrompt, makeWebSpeaker, type MicTrackLike, type VoicePromptDeps,
} from './recording-voice-prompt'
import { setHeaderTitle } from '../composables/useAppHeaderTitle'
import {
  appendTranscript, EMPTY_RECORDING_NOTICE, emptyRecordingNotice, formatRecordingClock,
  nextRecordingState, type RecordingPhase,
} from '../features/notes/note-recording'
import { decideMeetingStart, decideNoteStart } from './recordingPolicy'

// ---------------------------------------------------------------------------
// 录音语音播报（进程级单例）
// ---------------------------------------------------------------------------

const VOICE_PROMPT_KEY = '__openpocket_recordingVoicePrompt__'
/**
 * BUG-AU 降级结论的持久化键：本机是否已被证明「系统 TTS 会抢走前台」。
 * 存 localStorage 而非只留内存，是因为被劫持过一次之后，用户重启 App
 * 也不该再被同一个弹窗打断。
 */
const VOICE_PROMPT_HIJACK_KEY = 'openpocket.voicePrompt.hijacked'
type GlobalWithVoicePrompt = typeof globalThis & {
  [VOICE_PROMPT_KEY]?: RecordingVoicePrompt
}

/**
 * 探测 TTS 引擎。Capacitor 的 isPluginAvailable 判据比 isNativePlatform 准：
 * 后者不区分「WebView 已注册插件」与「插件真的可用」，在无 TTS 数据的模拟器上
 * 会误报可用，导致播报静默失败却没有任何降级提示。
 */
function detectVoicePromptDeps(): VoicePromptDeps {
  let nativeRegistered = false
  try {
    nativeRegistered = Capacitor.isPluginAvailable('TextToSpeech')
  } catch {
    nativeRegistered = false
  }
  const webSpeechAvailable =
    typeof window !== 'undefined'
    && 'speechSynthesis' in window
    && typeof SpeechSynthesisUtterance !== 'undefined'
  return {
    nativeRegistered,
    webSpeechAvailable,
    // BUG-AU：系统 TTS 在部分 ROM（MIUI 实测）上会拉起授权页抢前台。
    // 播报前后比对可见性，命中就让播报永久让位于录音。
    readVisibility: () => (typeof document !== 'undefined' ? document.visibilityState : 'visible'),
    onForegroundHijack: () => {
      try { localStorage.setItem(VOICE_PROMPT_HIJACK_KEY, '1') } catch { /* 无痕模式等 */ }
    },
  }
}

/** 原生 TTS：与 useSpeech 同一个插件，不引入第二套引擎。动态 import，Web 构建不打包。 */
async function speakNativeText(_engine: 'native' | 'web', text: string): Promise<void> {
  const { TextToSpeech } = await import('@capacitor-community/text-to-speech')
  await TextToSpeech.speak({ text, lang: 'zh-CN', rate: 1.05 })
}

/**
 * 会议录音与笔记录音共用一个播报实例：两条链路的播报串在同一条队列里，
 * 避免状态切换时两路 speak 互相打断。
 */
function voicePrompt(): RecordingVoicePrompt {
  const g = globalThis as GlobalWithVoicePrompt
  g[VOICE_PROMPT_KEY] ??= new RecordingVoicePrompt(detectVoicePromptDeps(), (engine, text) => {
    if (engine === 'native') return speakNativeText(engine, text)
    if (typeof window === 'undefined') return Promise.resolve()
    return makeWebSpeaker(window as unknown as {
      speechSynthesis: { speak: (u: SpeechSynthesisUtterance) => void }
      SpeechSynthesisUtterance: new (t: string) => SpeechSynthesisUtterance
    })(engine, text)
  })
  // 恢复上次的降级结论：本机已被证明会被系统 TTS 抢前台，就别再试了。
  try {
    if (localStorage.getItem(VOICE_PROMPT_HIJACK_KEY) === '1') {
      g[VOICE_PROMPT_KEY]!.restoreForegroundHijack()
    }
  } catch { /* 无痕模式读不到就当没降级过 */ }
  return g[VOICE_PROMPT_KEY]
}

// ---------------------------------------------------------------------------
// MeetingRecorderRuntime
// ---------------------------------------------------------------------------

export class MeetingRecorderRuntime {
  /** 全局「有录音在跑」——RecordingPill 与互斥判断用。 */
  readonly isRecording = ref(false)
  readonly isPaused = ref(false)
  readonly elapsedMs = ref(0)
  readonly segments = ref<MeetingSegment[]>([])
  readonly sttError = ref('')
  readonly interimCaption = ref('')
  readonly speakers = ref<{ profileId: string; label: string }[]>([])
  readonly processingCount = ref(0)
  readonly inputs = ref<AudioInput[]>([])
  readonly selectedInput = ref<AudioInput | undefined>()
  /** 当前录音归属的会议 id;空 = 没有会议录音进行中。 */
  readonly activeMeetingId = ref('')

  private mediaStream: MediaStream | null = null
  private vadSegmenter: VadSegmenter | null = null
  private diarizer: SpeakerDiarizer | null = null
  private startTime = 0
  private elapsedTimer: ReturnType<typeof setInterval> | null = null
  private segmentProfiles = new Map<string, string>()
  private partSeq = 0
  private unlistenNative: (() => void) | null = null
  private nativeMode = false
  /**
   * 后台录音不可用的真实原因（原生插件 reject 的原文）。
   * 空串 = 没试过或试成了。切后台时用它解释「为什么这台手机录不了后台」，
   * 否则用户只看到「请保持应用在前台」却不知道权限/占用等具体原因。
   */
  private backgroundMicReason = ref('')
  private stopCaption: (() => void) | null = null
  private stopping = false
  // 在途分段转写 promise:stop() 必须等它们落库,否则 refine 拿到的
  // transcript 缺尾巴,晚到的 updateTranscript 还会与 completed 状态竞争。
  private inFlightSegments = new Set<Promise<void>>()
  private toast = useToast()

  private async cleanupMedia() {
    if (this.mediaStream) {
      this.mediaStream.getTracks().forEach((t) => t.stop())
      this.mediaStream = null
    }
    this.vadSegmenter = null
    if (this.unlistenNative) { this.unlistenNative(); this.unlistenNative = null }
    if (this.nativeMode) await stopBackgroundMic()
    this.nativeMode = false
    this.stopCaption?.()
    this.stopCaption = null
    this.interimCaption.value = ''
    document.removeEventListener('visibilitychange', this.onHidden)
    navigator.mediaDevices?.removeEventListener?.('devicechange', this.onDeviceChange)
  }

  private onHidden = () => {
    if (document.visibilityState !== 'hidden' || !this.isRecording.value || this.nativeMode) return
    // 切到后台才告知来不及：用户是在录音途中切走的，此刻弹窗最贴近现场。
    // 把后台录音失败的真实原因一并带出来，否则用户只会得到一句
    // 「请保持应用在前台」而不知道为什么自己的手机明明支持。
    const reason = this.backgroundMicReason.value
    this.toast.error(
      reason
        ? `切到后台将停止录音（后台录音不可用：${reason}）。请保持应用在前台。`
        : '浏览器无法在后台继续录音，请保持应用在前台或使用 Android 客户端',
    )
  }

  /**
   * 会议录音期间**分片转写**的中止器。
   *
   * 与 NoteRecorderRuntime 的 transcribeAbort 分开是有意的：后者是
   * 「录完之后那次兜底全量转写」的，生命周期只有 stop() 收尾那一段；而分片
   * 转写从录音一开始就在跑，每次 processSegment 都是一次独立的
   * sttApi.transcribe（各自 3 分钟预算）。此前会议录音这一层**完全无法终止**。
   *
   * 生命周期跟着「一次录音会话」走：start() 换新（换之前先 abort 旧的，上一场
   * 遗留的分片不该继续烧配额，也不该被写进新会议），不在 stop() 上清——
   * stop() 还要等 inFlightSegments 收尾，中止它等于砍掉最后一个语音块。
   */
  private segmentAbort: AbortController | null = null

  /**
   * 强行终止在途的分片转写。
   *
   * 目前**没有界面调用它**——会议录音页还没有「停止转写」入口（这一点由
   * api/long-task-terminable.test.mjs 的缺口登记表盯着，新增/移除都要同步）。
   * 先把能力摆在这里，是为了让「录音中这批分片停不掉」不再是结构性缺陷。
   *
   * @returns 是否确实中止了在途分片转写（false = 没有可停的）
   */
  cancelSegmentTranscription(): boolean {
    const seg = this.segmentAbort
    // 用 processingCount 判定「真的在途」：只看 segmentAbort 非空是不够的，
    // 它在两场录音之间一直留着，不清就会在没有请求可停时也返回 true——
    // 那又是一个假成功。
    if (!seg || seg.signal.aborted || this.processingCount.value === 0) return false
    seg.abort()
    return true
  }

  /**
   * 开始(或幂等重入)一场会议录音。决策语义见 recordingPolicy.decideMeetingStart:
   * - 已在录同一场会议(页面重进)→ 直接 true,不重启采集;
   * - 别的会议在录 → 先正式收尾旧录音(落库),再开新的;顶替场景强制
   *   重置 segments/diarizer 等会话态(resume 只对同一场会议有效,否则
   *   新会议会继承旧会议的残留分段);
   * - 笔记录音占用麦克风 → 拒绝。
   */
  async start(meetingId: string, opts?: { deviceId?: string; resume?: boolean }): Promise<boolean> {
    const decision = decideMeetingStart({
      meetingId,
      meetingRecording: this.isRecording.value,
      activeMeetingId: this.activeMeetingId.value,
      stopping: this.stopping,
      noteRecording: noteRecorderRuntime.recording.value,
      resume: opts?.resume,
    })
    if (!decision.ok) {
      if (decision.action === 'reject-note-busy') {
        this.sttError.value = '笔记录音进行中，请先结束笔记录音'
      }
      return false
    }
    if (decision.action === 'noop-attached') return true
    if (decision.action === 'replace-other') await this.stop()

    this.sttError.value = ''
    const freshSession = decision.freshSession
    if (freshSession) {
      this.segments.value = []
      this.speakers.value = []
      this.segmentProfiles.clear()
      this.elapsedMs.value = 0
      this.processingCount.value = 0
      this.partSeq = 0
    }
    // 换场录音先中止上一场还在跑的分片转写：那些语音块属于已经结束的会议，
    // 留着只会白烧上游 ASR 配额，并且它们的分段会被写进新会议里。
    if (this.segmentAbort) this.segmentAbort.abort()
    this.segmentAbort = new AbortController()
    if (freshSession || !this.diarizer) {
      this.diarizer = new SpeakerDiarizer(0.72)
      try {
        const profiles = await loadSpeakerProfiles()
        this.diarizer.loadProfiles(profiles)
      } catch { /* 空库 */ }
    }

    const mic = useMicPermission()
    const ok = await mic.ensure()
    if (!ok) {
      this.sttError.value = mic.deniedLabel.value || '麦克风权限被拒绝，请在系统设置中授权后重试'
      return false
    }

    try {
      this.nativeMode = false
      this.backgroundMicReason.value = ''
      if (isBackgroundMicSupported()) {
        const nativeInputs = await listNativeMicInputs()
        if (nativeInputs.length) this.inputs.value = nativeInputs
        const started = await startBackgroundMic({ meetingId, deviceId: opts?.deviceId })
        this.nativeMode = started.ok
        if (!started.ok) this.backgroundMicReason.value = started.reason || '未知原因'
        if (this.nativeMode) {
          this.selectedInput.value = this.inputs.value.find((i) => i.deviceId === opts?.deviceId)
            || this.inputs.value[0]
          this.unlistenNative = await listenBackgroundMicParts((part) => {
            void this.processSegment(nativePartToBlob(part), part.startMs, part.endMs)
          }, (msg) => { this.sttError.value = msg })
        }
      }
      if (!this.nativeMode) {
        const opened = await openPreferredMicStream(opts?.deviceId)
        this.mediaStream = opened.stream
        this.inputs.value = opened.inputs
        this.selectedInput.value = opened.selected
        this.vadSegmenter = new VadSegmenter({
          silenceMs: 1500,
          minSpeechMs: 400,
          energyThreshold: 0.012,
          onSegment: (seg) => { void this.processSegment(seg.blob, seg.startMs, seg.endMs) },
        })
        await this.vadSegmenter.start(this.mediaStream)
        document.addEventListener('visibilitychange', this.onHidden)
      }

      this.startLiveCaption()
      this.startTime = Date.now()
      this.activeMeetingId.value = meetingId
      this.isRecording.value = true
      this.elapsedTimer = setInterval(() => {
        if (!this.isPaused.value) this.elapsedMs.value = Date.now() - this.startTime
      }, 200)
      navigator.mediaDevices?.addEventListener?.('devicechange', this.onDeviceChange)
      // 扬声器语音播报「开始录音」（需求：录音时要播一段语音，不是警告声）。
      // announceSilenced 在播报期间静音麦克风——否则这四个字会被录进会议
      // 记录成为第一句。fire-and-forget，不阻塞 start() 返回。
      voicePrompt().announceSilenced('start', this.micTrack())
      return true
    } catch {
      this.sttError.value = mic.deniedLabel.value || '麦克风权限被拒绝'
      await this.cleanupMedia()
      return false
    }
  }

  /**
   * 播报静音用的麦克风轨。原生后台录音（BackgroundMic）模式下没有
   * MediaStream 可静音——那种模式的音频由原生服务直接落盘，前端拿不到
   * track，此时返回 null，播报照常进行（原生侧靠 AEC/焦点抢占抑制回授）。
   */
  private micTrack(): MicTrackLike | null {
    return this.mediaStream?.getAudioTracks?.()[0] ?? null
  }

  private onDeviceChange = async () => {
    const next = Capacitor.isNativePlatform()
      ? await listNativeMicInputs()
      : await listAudioInputs()
    if (!next.length) return
    this.inputs.value = next
    const best = next[0]
    if (this.selectedInput.value && best.deviceId !== this.selectedInput.value.deviceId && best.rank < this.selectedInput.value.rank) {
      this.toast.success(`检测到更好的麦克风：${best.label}，可在录音条切换`)
    }
  }

  /**
   * 预置历史分段(恢复录音场景):页面把 localDB 里的既有 segments 灌进
   * runtime,使续录后的 updateTranscript 含完整历史。仅未在录时生效;
   * 正在录本会议时 segments 即实时态,无需 seed。
   */
  seedSegments(meetingId: string, stored: MeetingSegment[]): void {
    if (!meetingId || this.isRecording.value) return
    if (this.activeMeetingId.value && this.activeMeetingId.value !== meetingId) return
    this.segments.value = [...stored]
  }

  async switchDevice(meetingId: string, deviceId: string): Promise<boolean> {
    if (!this.isRecording.value) return this.start(meetingId, { deviceId })
    if (this.elapsedTimer) { clearInterval(this.elapsedTimer); this.elapsedTimer = null }
    const keptElapsed = this.elapsedMs.value
    this.vadSegmenter?.stop()
    await this.cleanupMedia()
    this.isRecording.value = false
    const ok = await this.start(meetingId, { deviceId, resume: true })
    if (ok) this.elapsedMs.value = keptElapsed
    return ok
  }

  private startLiveCaption() {
    // BUG-AU 第二条触发路径：实时字幕走 Web Speech API（Android 上是
    // webkitSpeechRecognition），同样委托给系统 ASR → MIUI「系统语音引擎」会拉起
    // 授权页抢前台。隔离实测（scripts/diag-asr-trigger.mjs）：不碰录音、不碰播报，
    // 只 new webkitSpeechRecognition().start()，系统包 5/5 抢前台。
    //
    // 为什么复用语音播报那个降级标志：两条路径打的是**同一个系统引擎**，
    // 所以「本机已被证明会被系统语音引擎抢前台」这个结论对两者同样成立，
    // 不需要第二套探测逻辑。播报那路在 start() 里更早被调用，会先把标志置上，
    // 于是从第二次录音起实时字幕就不再启动。
    if (voicePrompt().hasForegroundHijack()) return
    const Rec = pickSpeechRecognition(typeof window === 'undefined' ? null : (window as unknown as {
      SpeechRecognition?: new () => SpeechRecLike
      webkitSpeechRecognition?: new () => SpeechRecLike
    }))
    if (!Rec) return
    const caption = createLiveCaption({
      recognition: new Rec(),
      onResult: (result) => {
        applyCaptionResult(result, {
          setInterim: (text) => { this.interimCaption.value = text },
          commit: (text) => { void this.appendText(text) },
        })
      },
    })
    if (caption.start()) this.stopCaption = caption.stop
  }

  async appendText(text: string): Promise<MeetingSegment | null> {
    const meetingId = this.activeMeetingId.value
    if (!meetingId) return null
    const last = this.segments.value[this.segments.value.length - 1]
    const lastEnd = last?.endMs ?? this.elapsedMs.value
    const draft = buildUtteranceSegment({ meetingId, text, startMs: lastEnd })
    if (!draft) return null
    const id = await saveSegment(draft)
    const saved: MeetingSegment = { id, ...draft }
    this.segments.value.push(saved)
    // 去重渲染：与 ingest-speech.ts 同一口径（相邻段重叠消解）。
    // 本地 sherpa 实时字幕路径是**另一条**录音入口，此前也在原样拼接。
    await updateTranscript(meetingId, renderTranscript(this.segments.value))
    return saved
  }

  private async processSegment(blob: Blob, startMs: number, endMs: number) {
    if (!this.diarizer) return
    this.processingCount.value++
    const task = (async () => {
      try {
        this.partSeq += 1
        await ingestSpeechBlob({
          meetingId: this.activeMeetingId.value, blob, startMs, endMs, seq: this.partSeq, diarizer: this.diarizer!,
          segments: this.segments.value, segmentProfiles: this.segmentProfiles,
          signal: this.segmentAbort?.signal,
        })
        this.syncSpeakers()
      } catch (e) {
        // 用户中止不是失败：显示「转写失败，将在下一段重试」会让他以为录音坏了，
        // 实际上是他自己刚点的停止。判据取 signal.aborted 而不是错误对象——
        // 这里 abort 传下去后 fetch 抛什么由运行时决定，signal 才是稳定可读的那个。
        if (this.segmentAbort?.signal.aborted) return
        // 2026-10-01：原来这里写死「转写失败，将在下一段重试」，真实原因只进
        // console.warn。结果「网关列了 ASR 模型但没开通 provider」和「网络断了」
        // 在用户眼里完全一样，而这两种要采取的动作完全不同（前者去设置里换模型，
        // 后者重试）。后端 §4.1 辛苦整理出的可行动原因到这一层被丢干净了。
        this.sttError.value = sttFailureText(e, '转写失败，将在下一段重试')
        console.warn('[meeting-recorder] segment failed:', e)
      } finally {
        this.processingCount.value--
      }
    })()
    this.inFlightSegments.add(task)
    try {
      await task
    } finally {
      this.inFlightSegments.delete(task)
    }
  }

  private syncSpeakers() {
    if (!this.diarizer) return
    this.speakers.value = this.diarizer.getProfiles().map((p) => ({ profileId: p.id, label: p.label }))
  }

  async labelSpeaker(profileId: string, displayName: string) {
    if (!this.diarizer) return
    this.diarizer.labelProfile(profileId, displayName)
    this.syncSpeakers()
    const profile = this.diarizer.getProfile(profileId)
    if (profile) await saveVoiceprint({ id: profileId, displayName, embedding: profile.embedding })
    for (const [segId, pid] of this.segmentProfiles) {
      if (pid !== profileId) continue
      await updateSegmentSpeaker(segId, displayName)
      const seg = this.segments.value.find((s) => s.id === segId)
      if (seg) seg.speakerLabel = displayName
    }
  }

  /**
   * 正式收尾录音(用户显式停止 / 新录音顶替 / 全局指示条停止)。
   * 落盘音频 + 等在途分段 + meeting 置 completed。
   */
  async stop(): Promise<{ audioPath: string; durationMs: number } | null> {
    if (!this.isRecording.value || this.stopping) return null
    this.stopping = true
    const meetingId = this.activeMeetingId.value
    try {
      this.isRecording.value = false
      if (this.elapsedTimer) { clearInterval(this.elapsedTimer); this.elapsedTimer = null }
      navigator.mediaDevices?.removeEventListener?.('devicechange', this.onDeviceChange)

      const fullBlob = this.vadSegmenter?.stop() ?? null
      await this.cleanupMedia()
      const durationMs = this.elapsedMs.value
      let audioPath = ''
      if (fullBlob && fullBlob.size > 0) {
        audioPath = URL.createObjectURL(fullBlob)
        if (meetingId) {
          try { await saveMeetingAudio(meetingId, fullBlob) } catch { /* ok */ }
        }
      }
      // 等在途分段转写落库再置 completed；上限 10s，防止个别请求挂死卡住停止流程。
      if (this.inFlightSegments.size) {
        await Promise.race([
          Promise.allSettled([...this.inFlightSegments]),
          new Promise((resolve) => setTimeout(resolve, 10_000)),
        ])
      }
      if (meetingId) {
        await updateMeeting(meetingId, {
          audioPath: audioPath || null,
          durationMs,
          status: 'completed',
        })
        void getMeeting(meetingId).then((m) => { if (m) syncMeetingMetadata(m) })
      }
      this.activeMeetingId.value = ''
      return { audioPath, durationMs }
    } finally {
      this.stopping = false
      // 播报「录音结束」。放在 finally：无论收尾成功还是抛错，用户都会听到
      // 状态终止的语音，不会面对一个「点了没反应」的按钮。cleanupMedia() 已
      // 在上面拆掉麦克风，这四个字不会被录进成品。
      // clear() 先丢弃排队内容，避免连点停止时把「继续录音」念在停止之后。
      voicePrompt().clear()
      voicePrompt().announce('stop')
    }
  }

  formatElapsed(): string {
    const s = Math.floor(this.elapsedMs.value / 1000)
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    const sec = s % 60
    if (h > 0) return `${h}:${pad(m)}:${pad(sec)}`
    return `${pad(m)}:${pad(sec)}`
  }
}

// ---------------------------------------------------------------------------
// NoteRecorderRuntime
// ---------------------------------------------------------------------------

const NOTE_CHUNK_MS = 3000

/**
 * 给「不能拖死 UI 的关键 await」封顶。录音停止链路依赖的状态机
 * (phase: idle/recording/stopping)一旦被一个挂死的 IO 卡住,录音按钮就再也
 * 点不动了,所以收尾阶段的每个 IO 都必须走这个上限。
 */
function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`操作超时（${ms}ms）`)), ms)
    p.then(
      (v) => { clearTimeout(timer); resolve(v) },
      (e) => { clearTimeout(timer); reject(e) },
    )
  })
}


/**
 * 在浏览器/WebView 运行时探测 MediaRecorder 支持:把全局 MediaRecorder 绑到
 * recorderMime.ts 的 probe 接口上(2026-09-21 抽离后)。Node SSR / 测试环境
 * 没有全局 MediaRecorder → 返回空串,让上层走 new MediaRecorder(stream) 默认路径。
 */
function detectRecorderMime(): string {
  if (typeof MediaRecorder === 'undefined') return ''
  return pickSupportedRecorderMime({
    isTypeSupported: (m) => MediaRecorder.isTypeSupported(m),
  })
}

/**
 * 选择 MediaRecorder 可用的 MIME 类型并切成录音 blob.
 * 真机(redmi 等 Android WebView,Chromium 内核)对 audio/webm 实际不支持或
 * timeslice 不工作——选不到 webm/opopus 时降级到 audio/mp4(aac);都选不到
 * 直接 new MediaRecorder(stream) 走默认路径(浏览器会挑最稳的)。
 *
 * 2026-09-21 重构为独立模块 recorderMime.ts(便于单测,probe 可注入)。
 */

/**
 * 笔记录音 stop() 的产物。
 *
 * `draftId` 非空 = 草稿**已经落库**了（见 NoteRecorderRuntime.stop 的「先落草稿
 * 再转写」）。界面据此**不得**再建一次笔记，只能接管这条已存在的草稿。
 * 2026-10-06 新增：早落库是为了让录音在兜底转写这段窗口里扛得住进程死亡，
 * 代价就是「谁建草稿」这件事有两个候选点，必须用返回值明确只有一个生效。
 */
export interface NoteRecordingStopResult {
  text: string
  audioBlob: Blob
  durationMs: number
  /** 早落库拿到的笔记 id；null = 没有 sink 或落库失败，走旧的「界面再建」路径。 */
  draftId: string | null
}

/**
 * 笔记录音的落库出口。
 *
 * 刻意**不 import** notes-store：录音运行时不该绑到 features/notes 上。
 * 这里只声明结构（鸭子类型），实现由笔记特性在 useNoteRecording.ts 注册。
 * 这样 native 层不认识 LocalNote，特性层不认识 MediaRecorder。
 */
export interface VoiceDraftSink {
  /** 在任何长 await 之前把音频落成一条草稿，返回笔记 id。 */
  create(input: { text: string; audioBlob: Blob; durationMs: number }): Promise<string | null>
  /** 转写有结果后回填正文。实现必须保证**不动音频附件**。 */
  update(id: string, text: string): Promise<void>
}

/**
 * 笔记录音(3s 分片 + sherpa 流式 + 云端兜底)。跨页面存续:NoteListView 被
 * KeepAlive 驱逐或应用级导航离开时录音不断;stop() 的结果同时暂存在
 * pendingResult,NoteListView 重进后可消费(draftBanner 流程)。
 */
export class NoteRecorderRuntime {
  readonly phase = ref<RecordingPhase>('idle')
  readonly elapsedMs = ref(0)
  readonly transcript = ref('')
  readonly error = ref('')
  readonly recording = ref(false)

  /** stop() 的产物(text/audioBlob/duration);页面不在场时暂存,NoteListView
   * 重进后 consumePendingResult() 取走创建语音草稿笔记。 */
  pendingResult: NoteRecordingStopResult | null = null

  private mediaRecorder: MediaRecorder | null = null
  private mediaStream: MediaStream | null = null
  private chunks: Blob[] = []
  /**
   * 滚动 webm 解码器（前端转码，见 recording-audio-transcode.ts）。
   *
   * 存在的理由是真机录不出网关能吃的容器：MediaRecorder 在这台 Redmi 上
   * isTypeSupported('audio/wav') 为 false，只能产出 audio/webm;codecs=opus，
   * 而网关对 webm 是硬拒（400，supported: mp3, wav）——实测同一段音频
   * webm 400 / wav 200 且转写正确。
   *
   * 为什么不逐片独立转码：webm 是流式容器，初始化段只在第一片出现，
   * 实测第 2 片起 decodeAudioData 一律抛 UnsupportedError。所以保留全部
   * 分片、按时间窗切出**新增**部分再打成 WAV 上传。
   */
  private audioDecoder: RollingWebmDecoder | null = null
  private startedAt = 0
  private tick: ReturnType<typeof setInterval> | null = null
  private committed = ''
  private unlisten: { remove: () => void } | null = null
  private nativeListening = false
  /**
   * 本场录音的即时转写会话 id（每场新生成）。
   *
   * 服务端靠它维持**跨片去重**状态：3 秒定长切片必然切在词中间，两片交界处
   * 同一个词会被各识别一次，本地拼接会得到「今天今天下午三点」。没有会话 id
   * 就只能各片独立转写，去重无从谈起。
   *
   * 空 = 本场没开即时转写（原生 sherpa 流式可用时走那条路，无需云端会话）。
   */
  private sttSessionId = ''
  /**
   * 分片转写的串行链。
   *
   * 必须存在的原因：服务端按**到达顺序**累积会话文本，客户端并发发送会让
   * 响应乱序返回，而会话已被按到达顺序改写——客户端拿到的累计文本会跳变丢字。
   */
  private sliceChain: Promise<void> = Promise.resolve()
  /**
   * 停止时置位：让 recorder.stop() 派发的最后一片带上 isFinal，
   * 让服务端立刻释放会话而不是等 LRU 淘汰。
   */
  private releaseOnNextSlice = false
  /** 正在执行的 stop() 收尾;重入直接复用同一个 promise,不重复拆麦克风。 */
  private stopInFlight: Promise<NoteRecordingStopResult | null> | null = null

  /** 兜底全量转写的在途请求;非 null = 用户可以中止它。 */
  private transcribeAbort: AbortController | null = null

  /**
   * 落库出口。由笔记特性注册（见 useNoteRecording.ts），process 级单例。
   *
   * 为什么需要它：stop() 里兜底转写是**最长 10 分钟的 await**，而在这段窗口里
   * 音频只存在于 `this.chunks` 拼出来的内存 Blob。2026-10-06 真机实测三臂对照：
   * 同一段挂住的兜底窗口，点「停止转写」音频在盘上（200364B），`am force-stop`
   * 则**一个笔记目录都没新建**。用户在这 10 分钟里没电 / 划掉应用 /
   * 被 MIUI SmartPower 回收（这台设备上非常频繁）⇒ 录音无声消失。
   * 所以草稿必须在长 await **之前**就落库。
   */
  private voiceDraftSink: VoiceDraftSink | null = null

  /**
   * 注册落库出口，返回注销函数。
   *
   * 进程级单例上只保留最后一次注册：笔记特性是唯一使用方，重复注册只会让
   * 「界面上看到的是哪条草稿」变模糊。
   */
  registerVoiceDraftSink(sink: VoiceDraftSink | null): void {
    this.voiceDraftSink = sink
  }

  /**
   * 强行终止兜底转写（需求「后台执行的 api 可以强行终止」）。
   *
   * 只有用户点「停止转写」才会调它——**切页绝不能调**：录音所有权在进程级
   * 单例就是为了跨页继续，用户离开页面不等于他改主意了。
   *
   * abort 会真的传到服务端：handleSttTranscribeFull 的 ctx 派生自
   * r.Context()，连接断开后 TranscribeFull 立刻带着错误返回，不再白烧
   * 上游 ASR 配额。
   *
   * @returns 是否确实中止了一个在途转写（false = 没有在途转写可停）
   */
  cancelTranscription(): boolean {
    const c = this.transcribeAbort
    if (!c || c.signal.aborted) return false
    c.abort()
    return true
  }

  private syncTitle() {
    if (this.phase.value === 'recording') setHeaderTitle(`录音 ${formatRecordingClock(this.elapsedMs.value)}`)
    else setHeaderTitle(null)
  }

  private startTick() {
    this.startedAt = Date.now()
    this.elapsedMs.value = 0
    this.tick = setInterval(() => {
      this.elapsedMs.value = Date.now() - this.startedAt
      this.syncTitle()
    }, 250)
  }

  private stopTick() {
    if (this.tick) clearInterval(this.tick)
    this.tick = null
  }

  async start(): Promise<boolean> {
    const decision = decideNoteStart({
      phase: this.phase.value,
      meetingRecording: meetingRecorderRuntime.isRecording.value,
    })
    if (!decision.ok) {
      if (decision.action === 'reject-meeting-busy') {
        this.error.value = '会议录音进行中，请先结束会议录音'
      } else {
        // 静默失败会被读成"麦克风按钮坏了"：正在录就明确告知正在录，
        // 收尾中就等收尾（toggle 已把 stopping 归给 stop() 处理）。
        this.error.value = this.recording.value ? '正在录音中' : '上一段录音正在收尾，请稍候'
      }
      return false
    }
    this.error.value = ''
    const mic = useMicPermission()
    const ok = await mic.ensure()
    if (!ok) {
      this.error.value = mic.deniedLabel.value || '麦克风权限被拒绝'
      return false
    }
    try {
      this.mediaStream = await navigator.mediaDevices.getUserMedia({
        audio: { channelCount: 1, sampleRate: 16000 },
      })
      this.chunks = []
      // 旧解码器在上一场 stop() 的兜底全量转写里还会用到，所以**不能**在
      // cleanupMedia() 里 dispose（实测调用顺序：cleanupMedia 先于兜底转写）。
      // 改为在这里释放上一场的实例：start() 是唯一会重新分配它的入口，
      // 上一场此时一定已经收尾完毕。
      this.audioDecoder?.dispose()
      this.audioDecoder = new RollingWebmDecoder()
      this.committed = ''
      this.transcript.value = ''
      // 每场录音一个新会话：跨片去重的累积文本不能跨会话沿用，否则第二场
      // 笔记会接着第一场显示。id 带上时间戳是为了让服务端日志能分辨会话。
      this.sttSessionId = `note-${Date.now().toString(36)}`
      this.sliceChain = Promise.resolve()
      this.releaseOnNextSlice = false
      const mimeType = detectRecorderMime()
      this.mediaRecorder = mimeType
        ? new MediaRecorder(this.mediaStream, { mimeType })
        : new MediaRecorder(this.mediaStream)
      this.mediaRecorder.ondataavailable = (e) => {
        if (e.data.size > 0) {
          this.chunks.push(e.data)
          // 原始分片同时喂给解码器：webm 的初始化段只在第一片，
          // 只保留 blob 顺序不动内容，takeNewWindow 才能解出累计音频。
          this.audioDecoder?.push(e.data)
        }
        if (this.nativeListening || e.data.size === 0) return
        // 录音中：每 3 秒一片送即时转写。
        if (this.phase.value === 'recording') {
          void this.transcribeSlice(e.data)
          return
        }
        // 停止时 recorder.stop() 派发的最后一片：也要送，并带 isFinal 让服务端
        // 释放会话。不送的话这最后 3 秒会丢——而它通常正是用户最后说的那句。
        if (this.releaseOnNextSlice) {
          this.releaseOnNextSlice = false
          void this.transcribeSlice(e.data, true)
        }
      }
      this.mediaRecorder.start(NOTE_CHUNK_MS)
      this.phase.value = nextRecordingState('idle', 'toggle')
      this.recording.value = true
      this.startTick()
      this.syncTitle()
      void this.startLiveStt()
      // 扬声器语音播报「开始录音」（笔记 3s 分片会立刻把第一片送去转写，
      // 所以播报期必须静音麦克风，否则这四个字会出现在笔记正文第一句）。
      voicePrompt().announceSilenced('start', this.micTrack())
      return true
    } catch {
      this.error.value = mic.deniedLabel.value || '无法打开麦克风'
      this.cleanupMedia()
      // 启动失败必须把状态机完整复位:phase 留在非 idle 会让
      // decideNoteStart 拒绝下一次 start(),录音 FAB 表现为"点了没反应"。
      this.phase.value = 'idle'
      this.recording.value = false
      this.stopTick()
      setHeaderTitle(null)
      // 麦克风被占用/权限异常时用户往往没在看屏幕，出声比只给红字更早被察觉。
      voicePrompt().announce('error')
      return false
    }
  }

  private async startLiveStt() {
    try {
      this.unlisten = await sherpa.addListener('partialResult', (result) => {
        const next = result.isFinal
          ? appendTranscript(this.committed, this.transcript.value, '', result.text)
          : appendTranscript(this.committed, this.transcript.value, result.text)
        this.committed = next.committed
        this.transcript.value = next.display
      })
      await sttApi.startStreaming()
      this.nativeListening = true
    } catch {
      this.nativeListening = false
    }
  }

  private async transcribeSlice(blob: Blob, isFinal = false) {
    // 串行化：每一片都必须等上一片回来再发。
    //
    // 为什么必须排队：即时转写的去重状态是**服务端会话**里的，服务端按到达
    // 顺序累积文本。若两片并发发出，响应可能乱序返回，而服务端已经按到达
    // 顺序把两者都累积进会话了——客户端拿到的「累计文本」就会跳变、丢字，
    // 且这种问题只在网络抖动时偶发，极难排查。
    this.sliceChain = this.sliceChain.then(() => this.sendSlice(blob, isFinal)).catch(() => {
      /* 单片失败不阻断后续分片 */
    })
    await this.sliceChain
  }

  /**
   * 把累计 webm 的**新增**时间窗转成 16k 单声道 WAV。
   *
   * 失败时降级返回 null：转码失败不该让整场录音崩掉，上层会跳过这一片，
   * 下一片再试（累计音频还在，解码器游标也没推进，不会丢内容）。
   */
  private async transcodeSlice(blob: Blob, isFinal: boolean): Promise<ArrayBuffer | null> {
    if (!this.audioDecoder) {
      // 没有解码器（例如原生 sherpa 路径接管了录音）⇒ 退回原始 blob。
      // 这种情况网关会拒，但那是既有行为，不比转码路径更差。
      return blob.arrayBuffer().catch(() => null)
    }
    try {
      return await this.audioDecoder.takeNewWindow()
    } catch {
      // 解码失败不致命：跳过这片，等下一片重新解码累计音频。
      // isFinal 时也返回 null —— 此时游标未推进，最后一段会留给
      // stop() 里的兜底全量转写（那条路走 takeFull，拿的是完整音频）。
      return null
    }
  }

  private async sendSlice(blob: Blob, isFinal: boolean) {
    if (!this.sttSessionId) return
    // 网关只收 mp3/wav，而真机录出来的是 webm（recording-audio-transcode.ts
    // 头注释有完整实测）。这里把**新增**的时间窗转成 16k WAV 再发。
    //
    // 注意是「新增窗口」而不是整段：服务端会话按到达顺序累积文本，
    // 重发旧音频会得到「今天今天下午三点」这种重复。
    const wav = await this.transcodeSlice(blob, isFinal)
    if (!wav) {
      // 还没攒够新内容（刚 stop 时最后一片可能只有几十毫秒）。
      // 这种情况**不发**空请求：0 长度音频会让上游回 502 empty transcript，
      // 界面上凭空多出一条错误提示。
      if (isFinal) this.sttSessionId = ''
      return
    }
    const windowSec = wav.byteLength / 2 / 16000
    const endSec = this.elapsedMs.value / 1000
    const startSec = Math.max(0, endSec - windowSec)
    try {
      const res = await sttSettingsApi.transcribeIncremental({
        audioBase64: arrayBufferToBase64(wav),
        sessionId: this.sttSessionId,
        filename: 'chunk.wav',
        startSec,
        endSec,
        // 3 秒定长切片是**硬切**（不在停顿处），所以不能告诉服务端「这段
        // 尾部有静音」——那会让它按静音边界做去重，反而吃掉真实的相邻文字。
        silenceCut: false,
        isFinal,
      })
      if (isFinal) this.sttSessionId = ''
      // 清 error 的判据是「**这一片有没有出字**」，不是「这一片有没有报错」。
      //
      // 2026-10-05 真机实测（证据落盘在 docs/handoff/evidence/）：服务端
      // `internal/stt/incremental.go` 在**某片失败**时会返回
      // `{text: <已累积文本>, error: <本片错误>}` —— 两者同时存在是**常态**，
      // 16 片里实测有 8 片如此。而失败片多数是纯静音（`empty transcript`）。
      //
      // 我第一版把清理写在 `if (res.error) return` 之后，**等于永远走不到**：
      // 那些片确实带 error。⇒ 界面全程挂着「转写失败」，而 text 同时在增长。
      // 正确判据：只要这一片**产出了文字**（或后端说这段没失败），就清掉
      // 陈旧错误——因为 error 描述的是「上一段没转出来」，不是「现在坏了」。
      if (res.text.trim()) {
        this.committed = res.text.trim()
        this.transcript.value = this.committed
        // 出字了 ⇒ 之前的失败已经过去，错误提示应当消失。
        this.error.value = ''
        return
      }
      if (res.error) {
        // 单片失败**不清空**已有文本：服务端在这种情况下也会回填累计文本。
        this.error.value = sttFailureText(res.error, '转写失败，将在下一段重试')
      }
    } catch (e) {
      if (isFinal) this.sttSessionId = ''
      this.error.value = sttFailureText(e, '转写失败，将在下一段重试')
    }
  }

  async stop(): Promise<NoteRecordingStopResult | null> {
    if (this.stopInFlight) return this.stopInFlight
    this.stopInFlight = this.runStop()
    try {
      return await this.stopInFlight
    } finally {
      this.stopInFlight = null
    }
  }

  /**
   * 收尾主体。**必须**在 finally 里把 phase 拨回 'idle'：
   * phase 一旦卡在 'stopping',decideNoteStart 会拒绝一切新录音、toggle() 也
   * 直接返回 null —— 录音 FAB 就永久失灵（"点了停止没反应"）。真机上
   * 下面的转写兜底走 /api/stt/transcribe,该请求若迟迟不回就会稳定复现。
   */
  private async runStop(): Promise<NoteRecordingStopResult | null> {
    if (this.phase.value !== 'recording' || !this.mediaRecorder) return null
    const recorder = this.mediaRecorder
    // mimeType 必须在 cleanupMedia() 之前取：cleanupMedia 会把
    // this.mediaRecorder 置 null,之后再读只剩 undefined,拿到的就变成
    // detectRecorderMime() 的猜测值。猜错 → 拼出的 blob 类型与实际编码
    // 不符 → stt.filenameForMimeType() 发错扩展名 → 后端转写直接拒收,
    // 表现为"录音有内容但转不出文字"。
    const blobType = recorder.mimeType || detectRecorderMime() || 'audio/webm'
    this.phase.value = nextRecordingState('recording', 'toggle')
    this.recording.value = false
    this.stopTick()
    const durationMs = Date.now() - this.startedAt
    // 让 recorder.stop() 派发的最后一片带上 isFinal（见 ondataavailable）。
    // 不置位的话这最后 3 秒不会送去转写，而它通常正是用户最后说的那句。
    this.releaseOnNextSlice = this.sttSessionId !== ''
    // 兜底：部分 Android WebView 在 timeslice MediaRecorder 上,即便我们调 stop()
    // 也不会派 onstop(dataavailable 已停在 INACTIVE,但 recorder 没把队列里的
    // onstop 事件 flush)。三秒兜底后强制 resolve 并继续清理,避免 UI 卡死。
    let stopResolved = false
    await new Promise<void>((resolve) => {
      const onStopped = () => { if (stopResolved) return; stopResolved = true; resolve() }
      recorder.onstop = onStopped
      try { recorder.stop() } catch { onStopped() }
      setTimeout(() => { if (!stopResolved) { stopResolved = true; resolve() } }, 3000)
    })
    try {
      if (this.nativeListening) {
        // stopStreaming 同样没有内建超时,给 5s 上限;超时只是丢掉这一句
        // 尾部文本,不能把整个 stop() 拖死。
        const final = await withTimeout(sttApi.stopStreaming(), 5000)
        const text = (final as { text?: string }).text || ''
        if (text) {
          const next = appendTranscript(this.committed, this.transcript.value, '', text)
          this.committed = next.committed
          this.transcript.value = next.display
        }
        this.nativeListening = false
      }
      this.unlisten?.remove()
      this.unlisten = null
      this.cleanupMedia()
      const audioBlob = this.chunks.length
        ? new Blob(this.chunks, { type: blobType })
        : new Blob([], { type: blobType })
      // 等在途分片落库再继续收尾，否则最后一片的转写结果会晚于
      // pendingResult 交付，用户拿到的笔记少最后一句。上限 10s 防止个别
      // 请求挂死把停止流程卡住（那会让 phase 停在 'stopping'，录音按钮锁死）。
      if (this.sttSessionId !== '') {
        await Promise.race([
          this.sliceChain,
          new Promise((resolve) => setTimeout(resolve, 10_000)),
        ])
      }
      // ── 先落草稿，再转写 ──────────────────────────────────────────────
      // 位置是刻意选的：**分片收尾之后、兜底转写之前**。
      //   · 放分片之前 ⇒ 草稿正文会停在「最后一句还没回来」的状态，而分片出字
      //     时兜底整段转写**不会**跑，也就没人回填 ⇒ 笔记少了最后一句。
      //   · 放兜底之前 ⇒ 这段（最长 10 分钟）就是唯一的暴露窗口，已被消掉。
      // 落库失败不阻断后续：草稿只是「保命用」，转写与 pendingResult 照常。
      const settledText = this.transcript.value.trim()
      let draftId: string | null = null
      if (this.voiceDraftSink && audioBlob.size > 0) {
        try {
          draftId = await this.voiceDraftSink.create({
            text: settledText,
            audioBlob,
            durationMs,
          })
        } catch {
          // 落库异常不该让用户丢掉转写：退回旧的「界面再建」路径。
          draftId = null
        }
      }
      // 分片转写一条都没回来时,用整段音频兜底转写。
      //
      // 2026-10-01：这里从 sttApi.transcribe 换成 transcribeFull，因为任何 ASR
      // 都不允许无限长音频单次上传（智谱 30 秒 / OpenRouter ~60 秒 / MiniMax 500 秒）。
      // 原实现对超过上限的录音会直接失败，用户表现为「录了五分钟一句话都转不出来」。
      // 服务端现在会自动按静音边界切段并聚合，所以长录音也能兜住。
      //
      // 超时上限同步放大：服务端串行跑 N 段，用 20 秒会让长录音必然超时。
      // 但**必须有上限**——否则后端不响应时 phase 永远停在 'stopping'，
      // 录音按钮彻底锁死（这正是 withTimeout 存在的理由）。
      if (!this.transcript.value.trim() && audioBlob.size > 0) {
        // 这段兜底转写最长 10 分钟，是整条录音链路上唯一一段**用户既看不见
        // 进度、又完全没有中止手段**的等待（需求「后台执行的 api 可以强行
        // 终止」在这里是空的）。加上中止器：服务端 handleSttTranscribeFull
        // 派生自 r.Context()，abort 是真终止，不只是前端撒手。
        const controller = new AbortController()
        this.transcribeAbort = controller
        try {
          // 兜底这条路同样要转码：真机录的是 webm，网关只收 mp3/wav。
          // 转码失败就退回原始 blob——服务端的 webm 拒收是既有行为，
          // 不比「因为转码挂了所以什么都不发」更差，且保留了错误可见性。
          let payload: ArrayBuffer | null = null
          try {
            payload = await this.audioDecoder?.takeFull() ?? null
          } catch {
            payload = null
          }
          const uploadBlob = payload
            ? new Blob([payload], { type: 'audio/wav' })
            : audioBlob
          const result = await withTimeout(
            sttSettingsApi.transcribeFull(
              uploadBlob,
              payload ? 'note.wav' : `note${filenameForMimeType(blobType)}`,
              controller.signal,
            ),
            10 * 60_000,
          )
          this.transcript.value = result.text
          // 部分段失败时整体仍成功（保住成功段内容），但必须告诉用户
          // 「有 N 段没转出来」——否则用户会以为记录是完整的。
          if (result.failed > 0) {
            this.error.value = `有 ${result.failed} 段未能转写，已保留其余 ${result.succeeded} 段内容`
          }
        } catch (e) {
          if (controller.signal.aborted) {
            // 用户主动中止：不是错误，别把 AbortError 当转写失败弹给用户。
            // 音频已经拿到了（pendingResult 照常落），所以这句要说清楚
            // 「文字没转出来但录音还在」，否则用户会以为整段录音丢了。
            this.error.value = '已停止转写，本次录音没有文字；音频仍已保存'
          } else {
            // 2026-10-01：原来直接甩 e.message，等于把 `stt_unavailable: …` 连错误码
            // 一起怼给界面；但反过来无脑显示原文又会把 `dial tcp …: i/o timeout`
            // 这类技术串甩给用户。sttFailureText 是窄口径：只放行带 stt_unavailable
            // 码的整理文案（剥前缀、截断 160 字），没有稳定错误码的一律走通用兜底。
            this.error.value = sttFailureText(e, '转写失败')
          }
        } finally {
          this.transcribeAbort = null
        }
      }
      // 转写有结果后回填正文。**只改文字**：sink.update 的实现必须保留音频附件
      // （notes-persist 的 updateNote 在不传 media 时会合并回旧媒体，见
      // note-edit-preserves-media 那道门）。进程在窗口期死掉也不会走到这里，
      // 但草稿带着「（语音草稿）」+ 完整音频已经是用户能拿回的东西。
      const finalText = this.transcript.value.trim()
      if (draftId && finalText && finalText !== settledText) {
        try {
          await this.voiceDraftSink!.update(draftId, finalText)
        } catch {
          // 回填失败不该吞掉录音：草稿已经落库了，音频在，只是文字没跟上。
        }
      }
      this.pendingResult = { text: finalText, audioBlob, durationMs, draftId }
      // 一个音都没录到时必须说话，否则整段收尾是静默的。
      // 判据与理由见 note-recording.ts emptyRecordingNotice 的注释。
      const emptyNotice = emptyRecordingNotice(finalText !== '', audioBlob.size)
      if (emptyNotice) this.error.value = EMPTY_RECORDING_NOTICE
      return { text: finalText, audioBlob, durationMs, draftId }
    } finally {
      this.phase.value = nextRecordingState('stopping', 'drafted')
      setHeaderTitle(null)
      // 播报「录音结束」。麦克风已在 cleanupMedia() 拆除，这四个字不会进音频。
      // clear() 先丢弃排队内容，防止连点时把「继续录音」念在停止之后。
      voicePrompt().clear()
      voicePrompt().announce('stop')
    }
  }

  /** 播报静音用的麦克风轨（笔记链路独立持有自己的 MediaStream）。 */
  private micTrack(): MicTrackLike | null {
    return this.mediaStream?.getAudioTracks?.()[0] ?? null
  }

  async toggle(): Promise<NoteRecordingStopResult | null> {
    if (this.phase.value === 'idle') {
      await this.start()
      return null
    }
    if (this.phase.value === 'recording') return this.stop()
    // 'stopping':收尾还在跑(转写兜底 IO)。等它跑完并把结果交出去,
    // 避免"再点一次没反应"被用户读成按钮坏了。
    if (this.stopInFlight) return this.stopInFlight
    return null
  }

  /** 页面消费 pendingResult 后清空,防止重复弹草稿。 */
  consumePendingResult(): NoteRecordingStopResult | null {
    const r = this.pendingResult
    this.pendingResult = null
    return r
  }

  private cleanupMedia() {
    if (this.mediaRecorder && this.mediaRecorder.state !== 'inactive') {
      try { this.mediaRecorder.stop() } catch { /* already stopped */ }
    }
    this.mediaRecorder = null
    this.mediaStream?.getTracks().forEach((t) => t.stop())
    this.mediaStream = null
  }
}

function pad(n: number): string { return n.toString().padStart(2, '0') }

// ---------------------------------------------------------------------------
// 进程级单例(globalThis 挂载,防 HMR 重复实例;沿用 aiStreamRuntime 模式)
// ---------------------------------------------------------------------------

const MEETING_KEY = '__openpocket_meetingRecorderRuntime__'
const NOTE_KEY = '__openpocket_noteRecorderRuntime__'
type GlobalWithRuntimes = typeof globalThis & {
  [MEETING_KEY]?: MeetingRecorderRuntime
  [NOTE_KEY]?: NoteRecorderRuntime
}
const g = globalThis as GlobalWithRuntimes

export const meetingRecorderRuntime: MeetingRecorderRuntime =
  g[MEETING_KEY] ?? (g[MEETING_KEY] = new MeetingRecorderRuntime())
export const noteRecorderRuntime: NoteRecorderRuntime =
  g[NOTE_KEY] ?? (g[NOTE_KEY] = new NoteRecorderRuntime())

/** 全局「任意录音进行中」——RecordingPill 显示条件。 */
export function anyRecordingActive(): boolean {
  return meetingRecorderRuntime.isRecording.value || noteRecorderRuntime.recording.value
}
