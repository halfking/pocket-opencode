/**
 * vad-segmenter.ts — Web Audio VAD 语音分段
 *
 * 通过能量检测识别语音起止，替代固定 5s 切片。
 * cap-sherpa 原生 VAD 就绪后可切换为 sherpa 流式。
 */

export interface VadSegment {
  blob: Blob
  startMs: number
  endMs: number
}

export interface VadSegmenterOptions {
  /** 静音持续多久判定一句结束（ms） */
  silenceMs?: number
  /** 最短有效语音段（ms） */
  minSpeechMs?: number
  /** RMS 能量阈值（0-1） */
  energyThreshold?: number
  /** MediaRecorder 切片间隔（ms） */
  sliceMs?: number
  onSegment: (seg: VadSegment) => void
}

const DEFAULTS = {
  silenceMs: 1500,
  minSpeechMs: 400,
  energyThreshold: 0.015,
  sliceMs: 250,
}

export class VadSegmenter {
  private opts: Required<Omit<VadSegmenterOptions, 'onSegment'>> & { onSegment: VadSegmenterOptions['onSegment'] }
  private stream: MediaStream | null = null
  private recorder: MediaRecorder | null = null
  private audioCtx: AudioContext | null = null
  private analyserL: AnalyserNode | null = null
  private analyserR: AnalyserNode | null = null
  private rafId = 0
  private startTime = 0
  private speechStartMs = 0
  private lastSpeechMs = 0
  private inSpeech = false
  private sliceBuffer: { blob: Blob; atMs: number }[] = []
  private fullChunks: Blob[] = []

  constructor(options: VadSegmenterOptions) {
    this.opts = { ...DEFAULTS, ...options }
  }

  async start(stream: MediaStream): Promise<void> {
    this.stop()
    this.stream = stream
    this.startTime = Date.now()
    this.sliceBuffer = []
    this.fullChunks = []
    this.inSpeech = false

    this.audioCtx = new AudioContext({ sampleRate: 16000 })
    const source = this.audioCtx.createMediaStreamSource(stream)
    const splitter = this.audioCtx.createChannelSplitter(2)
    this.analyserL = this.audioCtx.createAnalyser()
    this.analyserR = this.audioCtx.createAnalyser()
    this.analyserL.fftSize = 512
    this.analyserR.fftSize = 512
    source.connect(splitter)
    splitter.connect(this.analyserL, 0)
    splitter.connect(this.analyserR, 1)

    this.recorder = new MediaRecorder(stream)
    this.recorder.ondataavailable = (e) => {
      if (e.data.size > 0) {
        const atMs = Date.now() - this.startTime
        this.sliceBuffer.push({ blob: e.data, atMs })
        this.fullChunks.push(e.data)
        // 保留最近 60s 切片
        const cutoff = atMs - 60_000
        this.sliceBuffer = this.sliceBuffer.filter((s) => s.atMs >= cutoff)
      }
    }
    this.recorder.start(this.opts.sliceMs)
    this.tick()
  }

  private channelRms(analyser: AnalyserNode | null): number {
    if (!analyser) return 0
    const data = new Uint8Array(analyser.frequencyBinCount)
    analyser.getByteTimeDomainData(data)
    let sum = 0
    for (let i = 0; i < data.length; i++) {
      const v = (data[i] - 128) / 128
      sum += v * v
    }
    return Math.sqrt(sum / data.length)
  }

  private tick = () => {
    if (!this.analyserL) return
    const rms = Math.max(this.channelRms(this.analyserL), this.channelRms(this.analyserR))
    const now = Date.now() - this.startTime

    if (rms >= this.opts.energyThreshold) {
      if (!this.inSpeech) {
        this.inSpeech = true
        this.speechStartMs = now
      }
      this.lastSpeechMs = now
    } else if (this.inSpeech && now - this.lastSpeechMs >= this.opts.silenceMs) {
      this.finalizeSegment(now)
      this.inSpeech = false
    }

    this.rafId = requestAnimationFrame(this.tick)
  }

  private finalizeSegment(endMs: number) {
    const duration = endMs - this.speechStartMs
    if (duration < this.opts.minSpeechMs) return

    const slices = this.sliceBuffer.filter(
      (s) => s.atMs >= this.speechStartMs - this.opts.sliceMs && s.atMs <= endMs,
    )
    if (slices.length === 0) return

    const blob = new Blob(slices.map((s) => s.blob), { type: 'audio/webm' })
    this.opts.onSegment({ blob, startMs: this.speechStartMs, endMs })
  }

  /** 停止并刷新最后一段语音 */
  stop(): Blob | null {
    cancelAnimationFrame(this.rafId)
    if (this.inSpeech) {
      this.finalizeSegment(Date.now() - this.startTime)
      this.inSpeech = false
    }
    if (this.recorder && this.recorder.state !== 'inactive') {
      try { this.recorder.stop() } catch { /* ok */ }
    }
    this.recorder = null
    if (this.audioCtx) {
      this.audioCtx.close()
      this.audioCtx = null
    }
    this.analyserL = null
    this.analyserR = null
    if (this.fullChunks.length === 0) return null
    return new Blob(this.fullChunks, { type: 'audio/webm' })
  }

  getStream(): MediaStream | null {
    return this.stream
  }
}
