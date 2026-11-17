/**
 * vad-web-segmenter.ts — Phase 1 纯前端 VAD 方案
 *
 * 使用 @ricky0123/vad-web 进行高精度语音活动检测，替代能量阈值方案。
 * 优势：
 * 1. 基于 Silero VAD 模型，检测准确率更高
 * 2. 自适应噪音环境，无需手动调整阈值
 * 3. 更准确的语音/静音边界检测
 */

// ⚠ 该包只发 CJS（命名导出经 Object.defineProperty(exports, …, {get}) 提供）。
// vite dev 的 esbuild 预打包识别不出 `MicVAD` 这个命名导出
//（实测：SyntaxError: … does not provide an export named 'MicVAD'），
// 会让整条 app 图谱在浏览器里加载失败（email fixture 的 12 个浏览器用例只剩 1 个红即此因）。
// ⇒ 改用 namespace 导入 + 类型侧引用，dev/build 两端都稳。
import * as vadWeb from '@ricky0123/vad-web'
type MicVAD = import('@ricky0123/vad-web').MicVAD

export interface VadSegment {
  blob: Blob
  startMs: number
  endMs: number
}

export interface VadWebSegmenterOptions {
  /** 最短有效语音段（ms） */
  minSpeechMs?: number
  /** 单段语音硬上限（ms） */
  maxSpeechMs?: number
  /** MediaRecorder 切片间隔（ms） */
  sliceMs?: number
  /** VAD 采样率 */
  sampleRate?: number
  /** 语音开始前的预留时间（ms） */
  preSpeechPadMs?: number
  /** 语音结束后的留尾时间（ms） */
  postSpeechPadMs?: number
  onSegment: (seg: VadSegment) => void
  onVadReady?: () => void
  onVadError?: (error: Error) => void
}

const DEFAULTS = {
  minSpeechMs: 400,
  maxSpeechMs: 20_000,
  sliceMs: 250,
  sampleRate: 16000,
  preSpeechPadMs: 300,
  postSpeechPadMs: 300,
}

export class VadWebSegmenter {
  private opts: Required<Omit<VadWebSegmenterOptions, 'onSegment' | 'onVadReady' | 'onVadError'>> & {
    onSegment: VadWebSegmenterOptions['onSegment']
    onVadReady?: VadWebSegmenterOptions['onVadReady']
    onVadError?: VadWebSegmenterOptions['onVadError']
  }
  private vad: MicVAD | null = null
  private stream: MediaStream | null = null
  private recorder: MediaRecorder | null = null
  private startTime = 0
  private speechStartMs = 0
  private inSpeech = false
  private sliceBuffer: { blob: Blob; atMs: number }[] = []
  private fullChunks: Blob[] = []
  private finalFlush: (() => void) | null = null
  private carryAfterMs = -1
  private forcedCutWarned = false

  constructor(options: VadWebSegmenterOptions) {
    this.opts = { ...DEFAULTS, ...options }
  }

  async start(stream: MediaStream): Promise<void> {
    await this.stop()
    
    this.stream = stream
    this.startTime = Date.now()
    this.sliceBuffer = []
    this.fullChunks = []
    this.inSpeech = false
    this.carryAfterMs = -1
    this.forcedCutWarned = false

    // 启动 MediaRecorder 收集音频数据
    this.recorder = new MediaRecorder(stream)
    this.recorder.ondataavailable = (e) => {
      this.finalFlush?.()
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

    // 初始化 VAD
    try {
      console.log('[vad-web-segmenter] Initializing Silero VAD...')
      
      this.vad = await vadWeb.MicVAD.new({
        // 提供 stream 的 getter
        getStream: async () => stream,
        onSpeechStart: () => {
          const now = Date.now() - this.startTime
          console.log(`[vad-web-segmenter] Speech started at ${now}ms`)
          
          if (!this.inSpeech) {
            this.inSpeech = true
            this.speechStartMs = Math.max(0, now - this.opts.preSpeechPadMs)
          }
        },
        onSpeechEnd: (audio: Float32Array) => {
          const now = Date.now() - this.startTime
          console.log(`[vad-web-segmenter] Speech ended at ${now}ms, audio samples: ${audio.length}`)
          
          if (this.inSpeech) {
            const endMs = now + this.opts.postSpeechPadMs
            this.finalizeSegment(endMs)
            this.inSpeech = false
          }
        },
        onVADMisfire: () => {
          console.log('[vad-web-segmenter] VAD misfire detected')
        },
        // 资源路径配置
        baseAssetPath: '/vad/',
        onnxWASMBasePath: '/vad/',
        // 使用 v5 模型
        model: 'v5',
      })

      this.opts.onVadReady?.()
      console.log('[vad-web-segmenter] VAD initialized successfully')

      // 启动 VAD
      this.vad.start()

      // 定期检查是否需要强制切段（防止单段过长）
      this.startMaxSpeechCheck()

    } catch (error) {
      const err = error instanceof Error ? error : new Error(String(error))
      console.error('[vad-web-segmenter] Failed to initialize VAD:', err)
      this.opts.onVadError?.(err)
      throw err
    }
  }

  private maxSpeechCheckInterval: number | null = null

  private startMaxSpeechCheck() {
    // 每秒检查一次是否需要强制切段
    this.maxSpeechCheckInterval = window.setInterval(() => {
      if (!this.inSpeech) return
      
      const now = Date.now() - this.startTime
      const duration = now - this.speechStartMs
      
      if (duration >= this.opts.maxSpeechMs) {
        if (!this.forcedCutWarned) {
          this.forcedCutWarned = true
          console.warn(
            `[vad-web-segmenter] 强制切分触发（连续 ${this.opts.maxSpeechMs}ms 未检测到静音）。` +
            `音频完整，但边界不在停顿上。`
          )
        }

        // 对齐到实际可用音频
        const earliest = this.sliceBuffer[0]?.atMs
        if (earliest !== undefined && earliest > this.speechStartMs) {
          const lostMs = earliest - this.speechStartMs
          this.speechStartMs = earliest - this.opts.sliceMs
          if (lostMs > this.opts.sliceMs) {
            console.warn(
              `[vad-web-segmenter] 段音频不完整：前面 ${lostMs - this.opts.sliceMs}ms 不可用`
            )
          }
        }

        // 拆分成多个不超过 maxSpeechMs 的段
        while (now - this.speechStartMs >= this.opts.maxSpeechMs) {
          const cut = this.speechStartMs + this.opts.maxSpeechMs
          this.finalizeSegment(cut, true)
          this.speechStartMs = cut
        }
      }
    }, 1000)
  }

  private finalizeSegment(endMs: number, forced = false) {
    const duration = endMs - this.speechStartMs
    if (duration < this.opts.minSpeechMs) {
      console.log(`[vad-web-segmenter] Segment too short: ${duration}ms < ${this.opts.minSpeechMs}ms`)
      return
    }

    const floor = Math.max(this.speechStartMs - this.opts.sliceMs, this.carryAfterMs + 1)
    const slices = this.sliceBuffer.filter((s) => s.atMs >= floor && s.atMs <= endMs)
    
    if (slices.length === 0) {
      console.warn('[vad-web-segmenter] No audio slices available for segment')
      return
    }
    
    if (forced) {
      this.carryAfterMs = slices[slices.length - 1].atMs
    }

    const coveredMs = slices.length * this.opts.sliceMs
    if (coveredMs + 2 * this.opts.sliceMs < duration) {
      console.warn(
        `[vad-web-segmenter] 段音频不完整：跨度 ${duration}ms，实际 ${coveredMs}ms`
      )
    }

    const blob = new Blob(slices.map((s) => s.blob), { type: 'audio/webm' })
    
    console.log(`[vad-web-segmenter] Finalizing segment: ${this.speechStartMs}ms - ${endMs}ms (${duration}ms, ${slices.length} slices)`)
    
    this.opts.onSegment({ blob, startMs: this.speechStartMs, endMs })
  }

  async stop(): Promise<Blob | null> {
    // 停止 maxSpeech 检查
    if (this.maxSpeechCheckInterval) {
      window.clearInterval(this.maxSpeechCheckInterval)
      this.maxSpeechCheckInterval = null
    }

    // 如果还在语音中，完成当前段
    if (this.inSpeech) {
      const now = Date.now() - this.startTime
      this.finalizeSegment(now + this.opts.postSpeechPadMs)
      this.inSpeech = false
    }

    // 停止 VAD
    if (this.vad) {
      try {
        this.vad.pause()
        this.vad.destroy()
      } catch (err) {
        console.error('[vad-web-segmenter] Error stopping VAD:', err)
      }
      this.vad = null
    }

    // 停止 recorder
    const recorder = this.recorder
    const flushed = this.awaitFinalChunk(recorder)
    if (recorder && recorder.state !== 'inactive') {
      try {
        recorder.stop()
      } catch {
        /* ok */
      }
    }
    this.recorder = null

    await flushed

    if (this.fullChunks.length === 0) return null
    return new Blob(this.fullChunks, { type: 'audio/webm' })
  }

  private awaitFinalChunk(recorder: MediaRecorder | null): Promise<void> {
    if (!recorder || recorder.state === 'inactive') return Promise.resolve()
    return new Promise<void>((resolve) => {
      let settled = false
      const done = () => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        this.finalFlush = null
        resolve()
      }
      const timer = setTimeout(done, 1000)
      this.finalFlush = done
    })
  }

  getStream(): MediaStream | null {
    return this.stream
  }
}
