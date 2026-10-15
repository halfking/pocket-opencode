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
  /**
   * 单段语音硬上限（ms），超过就强制切段。
   *
   * §536.3：没有它，一段连续语音只要超过 60s（`sliceBuffer` 的保留窗口）
   * 就会**静默丢掉前面的音频**——因为那个窗口同时是段的音频来源。
   * 背景噪声高于 0.0118（−38.6 dBFS）时整场塌成一段，于是「超过一分钟的会议
   * 只剩最后一分钟」。20s 取在能力表里最紧的上游上限（`glm-asr-2512` 的 30s）
   * 之下：会议链的单次 `sttApi.transcribe` **不做服务端切段**
   * （`SplitWAV` 只在 `transcribeFull` 用），超长段会被上游直接拒绝。
   */
  maxSpeechMs?: number
  /** MediaRecorder 切片间隔（ms） */
  sliceMs?: number
  onSegment: (seg: VadSegment) => void
}

const DEFAULTS = {
  silenceMs: 1500,
  minSpeechMs: 400,
  energyThreshold: 0.015,
  maxSpeechMs: 20_000,
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
  /**
   * 强制切段（maxSpeechMs）后，下一段必须从「上一段最后一片之后」接上。
   * §536.3 甲：不记这个的话，下一段的预卷条件 `atMs >= speechStartMs - sliceMs`
   * 会把上一段的尾巴再算进来 ⇒ 相邻两段重叠 250ms（同一段音频转写两次）。
   * 只在强制切段时设置：按静音切的段之间本来就有 1.5s 空档，预卷取不到上一段。
   */
  private carryAfterMs = -1
  /**
   * 本场录音是否已经报过「VAD 没在停顿处切段」（§548.2）。
   * 每场只报一次：录音期间每 maxSpeechMs 就会触发一次，逐次报会刷屏。
   */
  private forcedCutWarned = false
  /** 最近一次 tick 的实测 RMS（§548.2 的告警要靠它，判别权不在本层）。 */
  private lastRms = 0

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
    this.carryAfterMs = -1
    this.forcedCutWarned = false

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
        // ⚠ §536：这个「省内存的保留窗口」同时是 finalizeSegment 的**音频来源**
        //   （见下）。因此当 inSpeech 连续超过 60s（没有 1500ms 静音）时，
        //   finalizeSegment 取到的只有尾巴，前面的音频被**静默丢弃**且无任何告警，
        //   而自报的 startMs/endMs 仍是真实起止。实测丢失量随时长无界增长：
        //   70s→11.2s / 90s→31.2s / 120s→61.2s（见设计文档 §536.3）。
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
    // §548.2：强制切分的告警要带上「此刻实测 RMS」——判别「底噪压住了判定」
    // 与「讲话确实连续」只能靠它，而那个判断不该由本层下（见告警处注释）。
    this.lastRms = rms
    const now = Date.now() - this.startTime

    if (rms >= this.opts.energyThreshold) {
      if (!this.inSpeech) {
        this.inSpeech = true
        this.speechStartMs = now
      }
      this.lastSpeechMs = now
      // §536.3 甲：强制封顶。没有它，连续语音超过 60s 就会丢掉前面的音频
      //（保留窗口 = 音频来源），而 `endMs` 只在 stop() 才给出 ⇒ 无告警。
      if (now - this.speechStartMs >= this.opts.maxSpeechMs) {
        // ⚠ §548.2：**强制切分这件事本身是沉默的**——它每天都在发生，但从来没人被告知。
        //
        // 为什么值得报：强制切出来的段**音频一段不少**（覆盖率 100%），
        // 但**段边界不落在静音上** ⇒ 转写质量对的是「定长硬切」那一行
        //（§544 sensevoice 5.33%），不是「静音感知切分」那一行（3.25%）。
        // §548.2 实测：静音段 RMS 一旦越过 energyThreshold，106.97s 语料从
        // 15 段塌到 6 段，且 **6 段里 0 段切在停顿上**——**收益整条归零，
        // 而现场没有任何信号**。
        //
        // ⚠⚠ **这里只报事实，不下诊断**（第一版想过诊断，被自己的测试打回来）：
        //   「底噪高于阈值所以没切到」与「讲话确实连续」在本层的读数**完全同形**
        //   ——都只是「连续 maxSpeechMs 没看到静音」。判别需要的信息
        //   （同期底噪估计）**这一层拿不到**。
        //   ⇒ 所以把实测 RMS 连同阈值一起打出来：**贴近阈值 ⇒ 更可能是底噪压住了；
        //   远高于阈值 ⇒ 更可能真的是连续讲话。**判断留给读日志的人。
        //
        // ⚠ 每场只报一次（forcedCutWarned）：录音期间每 maxSpeechMs 触发一次，
        //   逐次报会刷屏。⇒ 它是**一次采样**，不是持续监视。
        if (!this.forcedCutWarned) {
          this.forcedCutWarned = true
          const rms = this.lastRms
          console.warn(
            `[vad-segmenter] 本场出现强制切分（连续 ${this.opts.maxSpeechMs}ms 未判到静音）。` +
              `音频没有丢失，但这一段的边界不在停顿上 ⇒ 转写质量按「定长硬切」算而非「静音感知切分」。` +
              `此刻实测 RMS=${rms.toFixed(4)}、阈值 energyThreshold=${this.opts.energyThreshold}：` +
              `两者接近 ⇒ 更可能是底噪压住了静音判定（阈值是绝对值、不随底噪自适应）；` +
              `RMS 远高于阈值 ⇒ 更可能确实是连续讲话，本就无停顿可切。` +
              `本条每场只报一次，是采样而非监视。参见设计文档 §548.2 / 决策 #49。`,
          )
        }

        // 保留窗口可能已经把前面的片挤掉了（典型：页面切后台、rAF 停摆期间）。
        // 此时把自报起点对齐到**实际还拿得到的最早位置**，否则这一段会
        // 「声称覆盖一段自己并没有的音频」——下游按时间挂说话人就全挂错。
        const earliest = this.sliceBuffer[0]?.atMs
        if (earliest !== undefined && earliest > this.speechStartMs) {
          // ⚠ §536.8 乙：**告警必须发在这里**，不能只发在 finalizeSegment 的
          //   覆盖检查里 —— 起点一回推，span 就等于 covered，那条检查永远绿，
          //   而「前面那截音频已经被保留窗口挤掉、永远转不出来了」这件事
          //   就被对齐动作**藏起来**了。典型触发：页面切后台超过 60s。
          const lostMs = earliest - this.speechStartMs
          this.speechStartMs = earliest - this.opts.sliceMs
          if (lostMs > this.opts.sliceMs) {
            console.warn(
              `[vad-segmenter] 段音频不完整：语音起于 ${lostMs}ms 前，但保留窗口里` +
                `最早只剩 ${earliest}ms ⇒ 前面 ${lostMs - this.opts.sliceMs}ms 不会被转写` +
                `（保留窗口 60000ms；典型原因是页面切后台期间 rAF 停摆）`,
            )
          }
        }
        // ⚠ 起点回推之后 `now - speechStartMs` 可能已经远大于 maxSpeechMs
        //   （典型：页面切后台 30s+，rAF 停摆期间攒下的音频一次性回来）。
        //   一次 finalizeSegment 发出去的话，这一段会**比封顶值还长**
        //   ⇒ 上游按 MaxSeconds 拒收（glm-asr-2512 只有 30s）。
        //   所以这里拆成若干 ≤ maxSpeechMs 的段逐个发。
        while (now - this.speechStartMs >= this.opts.maxSpeechMs) {
          const cut = this.speechStartMs + this.opts.maxSpeechMs
          this.finalizeSegment(cut, true)
          this.speechStartMs = cut
        }
      }
    } else if (this.inSpeech && now - this.lastSpeechMs >= this.opts.silenceMs) {
      this.finalizeSegment(now)
      this.inSpeech = false
    }

    this.rafId = requestAnimationFrame(this.tick)
  }

  private finalizeSegment(endMs: number, forced = false) {
    const duration = endMs - this.speechStartMs
    if (duration < this.opts.minSpeechMs) return

    // ⚠ §536.3：slices 覆盖的是「sliceBuffer 里还活着的那部分」，
    //   不是 [speechStartMs, endMs] 这整个跨度。duration 用真实跨度算、
    //   这里的 slices 却可能只覆盖尾巴 ⇒ **两者可以相差整整 60s**，
    //   而这一段音频再也不会被任何一条链转写（收尾的 transcribeFull 能补回
    //   **文本**，但挂不回**说话人**：attributeFullTranscript 按时间重叠分桶，
    //   单段塌陷时全场回执全进那一个桶 ⇒ 整场会议塌成一个说话人，见 §536.6）。
    //   修法见 §536.8：给 opts 加 maxSpeechMs 强制分段（本处语义不动）。
    const floor = Math.max(this.speechStartMs - this.opts.sliceMs, this.carryAfterMs + 1)
    const slices = this.sliceBuffer.filter((s) => s.atMs >= floor && s.atMs <= endMs)
    // ⚠ slices.length === 0 时静默 return：录音期间不会有人知道这一段没了。
    if (slices.length === 0) return
    if (forced) this.carryAfterMs = slices[slices.length - 1].atMs

    // §536.8 乙：覆盖不完就**说出来**。甲（maxSpeechMs）已让常态够不到这条，
    //   但 rAF 停摆等路径仍可能触发；那时宁可让开发者/用户知道少了一截，
    //   也不要输出一段「看起来完整」的转写。
    const coveredMs = slices.length * this.opts.sliceMs
    if (coveredMs + 2 * this.opts.sliceMs < duration) {
      console.warn(
        `[vad-segmenter] 段音频不完整：跨度 ${duration}ms，实际只有 ${coveredMs}ms，` +
          `缺 ${duration - coveredMs}ms（保留窗口 ${60_000}ms / rAF 停摆？）`,
      )
    }

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
