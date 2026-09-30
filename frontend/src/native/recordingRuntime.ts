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
import { sherpa } from './sherpa'
import {
  RecordingVoicePrompt, makeWebSpeaker, type MicTrackLike, type VoicePromptDeps,
} from './recording-voice-prompt'
import { setHeaderTitle } from '../composables/useAppHeaderTitle'
import {
  appendTranscript, formatRecordingClock, nextRecordingState, type RecordingPhase,
} from '../features/notes/note-recording'
import { decideMeetingStart, decideNoteStart } from './recordingPolicy'

// ---------------------------------------------------------------------------
// 录音语音播报（进程级单例）
// ---------------------------------------------------------------------------

const VOICE_PROMPT_KEY = '__openpocket_recordingVoicePrompt__'
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
  return { nativeRegistered, webSpeechAvailable }
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
    this.toast.error('浏览器无法在后台继续录音，请保持应用在前台或使用 Android 客户端')
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
      if (isBackgroundMicSupported()) {
        const nativeInputs = await listNativeMicInputs()
        if (nativeInputs.length) this.inputs.value = nativeInputs
        this.nativeMode = await startBackgroundMic({ meetingId, deviceId: opts?.deviceId })
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
    await updateTranscript(meetingId, this.segments.value.map((s) => `[${s.speakerLabel}] ${s.text}`).join('\n'))
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
        })
        this.syncSpeakers()
      } catch (e) {
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
  pendingResult: { text: string; audioBlob: Blob; durationMs: number } | null = null

  private mediaRecorder: MediaRecorder | null = null
  private mediaStream: MediaStream | null = null
  private chunks: Blob[] = []
  private startedAt = 0
  private tick: ReturnType<typeof setInterval> | null = null
  private committed = ''
  private unlisten: { remove: () => void } | null = null
  private nativeListening = false
  /** 正在执行的 stop() 收尾;重入直接复用同一个 promise,不重复拆麦克风。 */
  private stopInFlight: Promise<{ text: string; audioBlob: Blob; durationMs: number } | null> | null = null

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
      this.committed = ''
      this.transcript.value = ''
      const mimeType = detectRecorderMime()
      this.mediaRecorder = mimeType
        ? new MediaRecorder(this.mediaStream, { mimeType })
        : new MediaRecorder(this.mediaStream)
      this.mediaRecorder.ondataavailable = (e) => {
        if (e.data.size > 0) this.chunks.push(e.data)
        if (!this.nativeListening && e.data.size > 0 && this.phase.value === 'recording') {
          void this.transcribeSlice(e.data)
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

  private async transcribeSlice(blob: Blob) {
    try {
      const result = await sttApi.transcribe({ audioBlob: blob })
      if (result.text.trim()) {
        const next = appendTranscript(this.committed, this.transcript.value, '', result.text)
        this.committed = next.committed
        this.transcript.value = next.display
      }
    } catch {
      /* 分片失败等下一段 */
    }
  }

  async stop(): Promise<{ text: string; audioBlob: Blob; durationMs: number } | null> {
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
  private async runStop(): Promise<{ text: string; audioBlob: Blob; durationMs: number } | null> {
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
      // 分片转写一条都没回来时,用整段音频兜底转写。这是"停止"链路里
      // 唯一的长耗时 IO,必须加超时 —— 否则后端不响应时 phase 永远停在
      // 'stopping',录音按钮彻底锁死。
      if (!this.transcript.value.trim() && audioBlob.size > 0) {
        try {
          const result = await withTimeout(sttApi.transcribe({ audioBlob }), 20000)
          this.transcript.value = result.text
        } catch (e) {
          // 2026-10-01：原来直接甩 e.message，等于把 `stt_unavailable: …` 连错误码
          // 一起怼给界面；但反过来无脑显示原文又会把 `dial tcp …: i/o timeout`
          // 这类技术串甩给用户。sttFailureText 是窄口径：只放行带 stt_unavailable
          // 码的整理文案（剥前缀、截断 160 字），没有稳定错误码的一律走通用兜底。
          this.error.value = sttFailureText(e, '转写失败')
        }
      }
      this.pendingResult = { text: this.transcript.value.trim(), audioBlob, durationMs }
      return { text: this.transcript.value.trim(), audioBlob, durationMs }
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

  async toggle(): Promise<{ text: string; audioBlob: Blob; durationMs: number } | null> {
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
  consumePendingResult(): { text: string; audioBlob: Blob; durationMs: number } | null {
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
