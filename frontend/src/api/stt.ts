/**
 * Speech-to-text scheduler.
 *
 * ⚠⚠ §415（2026-10-11 更正）：本文件头此前描述的两件事**都与事实不符**，
 * 而它们是「意图」不是「描述」—— 按 `TestGatewayModelNotesAreNotStale`
 * 的立论（**注释一旦过期就会变成「不用再修了」的伪证据**），这是缺陷。
 *
 * **① 「local-first」那条本地路径目前 100% 不可用。**
 *   原文件头声称会先试设备端 sherpa-onnx（中文用 Paraformer），且仅 native 可用。
 *   而 `lib/shell/capabilities.ts` 已把 `Sherpa` 登记进 `NOT_IMPLEMENTED_PLUGINS`：
 *   Android 侧 6 个 `@PluginMethod` 里 5 个只 `call.reject`（`addListener` 是
 *   接口对齐用的 no-op）、iOS 侧无任何 Swift 实现、assets 里无 `.onnx` 模型。
 *   ⇒ `transcribe()` 里那个 local 分支**每次都走完就 reject**，
 *     `nativeListening` 恒为 false（`recordingRuntime.startLiveStt` 的 catch），
 *     **实际 100% 走云端**。
 *   ⚠ 本段**刻意不复述**原句：一段复述旧结论的注释会让「仍写着旧结论」这道
 *     判据被它自己满足（判据 `stt-local-first-claim-gate`）。要引用请去 git log。
 *   ⇒ 骨架真正接上时，`capabilities-stub-gate.test.mjs` 的指纹
 *     （`sherpa-onnx AAR not integrated`）会消失并主动转红。
 *
 * **② 云端模型不是写死的某一个。**
 *   原文件头把云端那一档写成了某一个托管 Whisper 型号。
 *   而 §406 起，网关候选已由「按用户 STT 设置 + 网关实时探测」解析
 *   （后端 `transcribe.go`：「Which cloud model answers is *not* hardcoded any more」），
 *   网关侧又补了 glm-asr / minimax-asr-1.0。
 *   ⇒ 在这里写死某个模型名，会让人以为换模型要改这个文件。
 *
 * **③ 当前真实形态（2026-11-14 层3修复后）**：
 *   传 `audioPath`（Android 原生链，WAV 分片）时**先试本地 sherpa**，
 *   高置信直接返回；低置信/本地不可用落云端；**云端失败时返回留存的
 *   本地低置信结果**（engine: 'local'）而不是向上抛 —— 会议录音不能
 *   因为云端挂了就整段丢字（2026-11-13 生产未配 GROQ key 的实测现场）。
 *   不传 audioPath（Web 链 / blob-only 调用方）行为不变：一律云端。
 *   中文云端质量仍由 `stt/target.go` 那份候选清单决定（⚠ 见 §414）。
 *   本地 sherpa 分支与 `startStreaming`/`stopStreaming` **保留**——
 *   调用点（`recordingRuntime`）已经做了能力缺失兜底（try/catch + 5s 超时），
 *   骨架接上后无需再改调用方；云端 3s 分片是它的兜底替代。
 */
import { sherpa } from '../native/sherpa'
import { http } from './http'
import { blobToBase64 } from '../utils/base64'
import { utf8ByteLength } from '../utils/text'
import { assertCloudModule } from './data-policy.ts'
import { isAbortError } from './abort.ts'

/**
 * 单段语音转写的客户端超时。
 *
 * 服务端 Transcriber 自身 120 秒。取 180 秒而不是取 120——见调用处的注释：
 * **与服务端相等等于客户端先到点**。
 */
export const STT_TRANSCRIBE_TIMEOUT_MS = 3 * 60_000

/**
 * 单次上传的**请求体**上限（字节）。
 *
 * ⚠ §922：这是**跨语言**的数字 —— 服务端 `requestBodyLimitMiddleware` 对
 *   `/api/stt/transcribe*` 用 `maxAudioBodyBytes = 25 << 20`（25 MiB）卡**整个请求体**。
 *   ⇒ 前端必须按**同一个上限**自己先拦一次：不然超限的请求会被服务端
 *   **截断成半截 JSON** ⇒ 回 400 "invalid body" ⇒「音频太大」被说成「请求体无效」
 *   （**实测**：26 MiB 的 base64 body 拿到的就是 400 "invalid body"，
 *   而真实原因是 body 超过了外层的 25 MiB 上限）。
 *
 * ⚠ 量的是 **base64 之后的 JSON 体**，不是原始音频：
 *   base64 把体积放大约 4/3 ⇒ 25 MiB 的请求体 ⇒ 原始音频最多约 **18.75 MiB**。
 *   ⇒ 判据会拿后端源码里的闸值与这个常量比对 ⇒ 后端改了这里必须同步。
 */
export const STT_TRANSCRIBE_MAX_BODY_BYTES = 25 * 1024 * 1024
import { filenameForMimeType } from './stt-filename'
import {
  CLOUD_STT_NEED_BLOB,
  requireCloudAudioBlob,
  type SttOptions,
} from './stt-cloud.ts'
import { ensureGatewayCompatible } from '../utils/wav-encode'

export interface SttResult {
  text: string
  confidence: number
  engine: 'local' | 'cloud'
  costCents?: number
}

export type { SttOptions }
export { CLOUD_STT_NEED_BLOB, requireCloudAudioBlob }

// 文件名推导已抽到 stt-filename.ts：单次/全量/即时三条转写路径必须共用
// 同一张 MIME→扩展名映射，否则会出现「单次能转、全量转不出」的割裂。
export { filenameForMimeType }
export const sttApi = {
  /**
   * Transcribe recorded audio with automatic fallback.
   * Pass `audioBlob` for web recordings, `audioPath` for native file paths.
   */
  async transcribe(opts: SttOptions, signal?: AbortSignal, assertCurrent?: () => void): Promise<SttResult> {
    assertCurrent?.()
    const minConf = opts.minConfidence ?? 0.7

    // Try local sherpa-onnx first (native only, needs file path).
    // ⚠ 2026-11-14（层3修复）：低置信结果要**留下来**——云端失败时它就是兜底。
    //   之前本地结果不够格直接丢掉 ⇒ 云端一挂整段全灭（生产未配 GROQ key 实测）。
    let localBest: SttResult | null = null
    if (opts.forceEngine !== 'cloud' && opts.audioPath) {
      try {
        const local = await sherpa.transcribe({ audioPath: opts.audioPath })
        assertCurrent?.()
        if (local.text.trim()) {
          localBest = {
            text: local.text,
            confidence: local.confidence,
            engine: 'local',
          }
          if (local.confidence >= minConf) return localBest
        }
      } catch {
        // Local engine not available: fall through to cloud.
      }
    }

    // ⚠ 2026-11-14 真机实测（第四层故障）：模块策略 local ⇒ assertCloudModule
    //   在这里就抛 ModulePolicyLocalError，云端路径整段没跑。而「策略禁用云」
    //   恰恰是**最该用本地结果**的场景（用户明确选了仅本地）。⇒ 整条云端路径
    //   （断言+转码+上传）包进同一个 try，任何一步失败都让 localBest 兜底。
    try {
      assertCloudModule(opts.module)
      const audioBlob = await ensureGatewayCompatible(requireCloudAudioBlob(opts))
      const base64 = await blobToBase64(audioBlob)
      const body = JSON.stringify({
        audioBase64: base64,
        filename: filenameForMimeType(audioBlob.type || 'audio/webm'),
      })

      // ⚠ §922：超限就在**本地**拦，不要发出去。
      //   发了会被服务端截断成半截 JSON ⇒ 400 "invalid body" ⇒ 用户无从判断是「太大」。
      //   ⇒ 抛的错误与服务端 413 用**同一个错误码** ⇒ extractErrorCode 取得到
      //   ⇒ 界面上是一句「音频太大」，而不是「操作失败」。
      //   ⚠ 用 utf8ByteLength（§916/§917 那两道字节闸用的同一个 helper）而不是
      //     `body.length`：后者是 UTF-16 code units，不是字节。
      const bodyBytes = utf8ByteLength(body)
      if (bodyBytes > STT_TRANSCRIBE_MAX_BODY_BYTES) {
        throw new Error(
          `stt_audio_too_large: audio too large (max ${STT_TRANSCRIBE_MAX_BODY_BYTES} bytes per upload, got ${bodyBytes})`,
        )
      }

      // 音频转写比普通 CRUD 慢一个量级（整段 base64 上传 + 推理）。
      //
      // 2026-10-03：原先用的是通用的 LONG_REQUEST_TIMEOUT_MS，正好 120 秒，
      // 而服务端 Transcriber 自己就 120 秒（server.go 的 longLivedPaths
      // 注释里记着）。**相等即错**：客户端计时从请求发出开始，服务端的从
      // handler 进来开始，中间还隔着网络与鉴权，所以客户端实际总是先到点，
      // 于是「刚好用满预算」的那一档必然失败。取 3 分钟。
      // signal 透传给 http：调用方（如 useVoiceInput 的孤儿转写）据此可以
      // 在页面离开后仍跑的同时**仍被中止**。http 的 HttpOptions.signal 早就是
      // 接好的，这里只是把它从这条长任务链上暴露出来——此前这条链是整条
      // stt 链里唯一没有中止入口的环节，而它恰恰是设计上要跨页存活的。
      const res = await http<{ text: string; confidence: number; costCents?: number }>(
        '/api/stt/transcribe',
        { method: 'POST', body, timeoutMs: STT_TRANSCRIBE_TIMEOUT_MS, signal, syncModule: opts.module, assertCurrent },
      )
      return {
        text: res.text,
        confidence: res.confidence,
        engine: 'cloud',
        costCents: res.costCents,
      }
    } catch (error) {
      // 云端失败且本地有可用文本 ⇒ 降级返回本地低置信结果，不向上抛。
      //   这是「会议录音不能因为云端挂了就整段丢字」的最后一道闸：
      //   字还出着（engine: 'local' 标记降级来源），调用方不需要改任何逻辑。
      //   覆盖两类云端侧失败：① 网络/服务端错误（生产未配 GROQ key 实测）；
      //   ② ModulePolicyLocalError——策略禁云时 assertCloudModule 在 try 开头
      //      就抛，「仅本地」模式下本地引擎是唯一合法出口（真机 16:39 实测）。
      //   中止（AbortError）不属于「云端失败」，原样抛出——那不是降级时机。
      if (localBest && !isAbortError(error)) {
        console.warn('[stt] 云端转写失败，降级返回本地低置信结果：', error)
        return localBest
      }
      throw error
    }
  },

  /** Stream-oriented helper for the recorder widget (native only). */
  async startStreaming(): Promise<void> {
    return sherpa.startListening()
  },
  async stopStreaming() {
    const res = await sherpa.stopListening()
    return res.final ?? res
  },
}
