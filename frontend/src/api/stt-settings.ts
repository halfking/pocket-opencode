/**
 * 语音转写（STT）设置 API 客户端。
 *
 * 与网关设置分开的原因：录音转写有自己的约束（长耗时、大体积上传、需要成本核算），
 * 外部 ASR 服务还有独立的一把 key。混进 llm_gateway 会导致「换对话模型」时
 * 顺手改掉转写目标。
 *
 * 后端契约（backend/internal/server/server_stt_settings.go）：
 *   GET  /api/stt/config     设置 + 两组推荐模型 + 网关探测结论
 *   PUT  /api/stt/config     保存（externalApiKey 留空 = 保留原 key）
 *   POST /api/stt/discover   强制重扫网关并逐个真实探测候选 ASR 模型
 *   POST /api/stt/probe      用真实录音试转（JSON base64 / multipart / 原始音频）
 */
import { http, LONG_REQUEST_TIMEOUT_MS } from './http'
import { blobToBase64 } from '../utils/base64'
import { ensureGatewayCompatible } from '../utils/wav-encode'
import { filenameForMimeType } from './stt-filename'

/**
 * 全量转写的客户端超时。
 *
 * 为什么远大于 LONG_REQUEST_TIMEOUT_MS（120s）：服务端按静音边界把整场录音
 * 切成 N 段**串行**转写（两小时会议约 288 段），服务端自身给到 10 分钟。
 * 客户端必须比服务端更宽裕，否则会出现「服务端还在算、客户端先断开」，
 * 而用户看到的是「转写失败」。
 */
const FULL_TRANSCRIBE_TIMEOUT_MS = 11 * 60_000

/**
 * 「试转」的客户端超时。
 *
 * 服务端 120 秒（server.go 的 longLivedPaths 注释：/api/stt/probe 120 秒）。
 * 原先用的是通用的 LONG_REQUEST_TIMEOUT_MS，也正好 120 秒——**相等即错**：
 * 客户端计时含网络与鉴权开销，实际总是先到点，于是「试转刚好用满预算」的
 * 那一档必然失败，而这是设置页里用户主动点、最期待看到结果的一个按钮。
 */
export const STT_PROBE_TIMEOUT_MS = 3 * 60_000

/** 转写通道。 */
export type SttChannel = 'auto' | 'gateway' | 'external' | 'minimax'

/** 传输形态。 */
export type SttTransport = 'auto' | 'transcriptions' | 'chat-audio' | 'sse'

/** 网关候选模型的探测结论（与后端 stt.Probe* 一一对应）。 */
export type SttProbeStatus =
  | 'ok'
  | 'no_provider'
  | 'endpoint_missing'
  | 'audio_ignored'
  | 'failed'
  | ''

export interface SttRecommendedModel {
  model: string
  /** gateway = 网关目录里的；external = 外部 OpenAI 兼容服务。 */
  group: 'gateway' | 'external'
  note: string
  baseURL?: string
  /** 美元/小时。0 = 无公开报价（不猜价）。 */
  usdPerHour?: number
  accuracy?: string
  /**
   * 该模型是否支持**服务端真流式**（SSE 边收边下发）。
   *
   * 与「能否出字」是两件事：所有模型都能出字，只有部分能边收边出。
   * 调研结论（2026-10-01）：只有 MiniMax asr-1.0 与智谱 glm-asr-2512 支持；
   * OpenRouter 转写端点不支持（上游约 60 秒超时）。所以选 OpenRouter 省钱
   * 就只能接受「分段式即时」（每 3-15 秒冒出一段），而不是逐字。
   */
  streaming?: boolean
  /**
   * 该服务单次请求的音频时长上限（秒）。0 = 未知。
   *
   * 直接决定「全量转写」能否一把梭：智谱 30 秒 / OpenRouter 约 60 秒 /
   * MiniMax 500 秒。超限必须先切段，所以前端展示长录音时要知道这个数。
   */
  maxSeconds?: number
}

export interface SttGatewayCandidate {
  model: string
  modality?: string
  family?: string
  status: SttProbeStatus
  transport?: SttTransport
  sampleText?: string
  detail?: string
  probedAt?: number
}

export interface SttDiscoveryResult {
  baseURL: string
  totalModels: number
  candidates: SttGatewayCandidate[]
  scannedAt: number
  error?: string
}

export interface SttSettings {
  channel: SttChannel
  /** 空 = 用探测到的第一个可用网关模型。 */
  gatewayModel: string
  externalBaseURL: string
  externalModel: string
  externalTransport: SttTransport
  /** 转写模板 id（后端 stt.ProviderIDs() 之一）。空 = 按地址/模型推断。 */
  provider?: string
  minimaxBaseURL?: string
  minimaxModel?: string
  minimaxStream?: boolean
  minimaxDiarization?: boolean
  language: string
  hasExternalKey: boolean
  hasMiniMaxKey?: boolean
  updatedAt?: number
  /** 后端算出的当前生效模型（只读）。 */
  effectiveModel?: string
  effectiveNote?: string
}

/** 一个可选的转写模板（能力由后端给出，前端不猜）。 */
export interface SttTemplate {
  id: string
  label: string
  supportsStream: boolean
  supportsDiarization: boolean
  /** true = 流式与说话人分离**不能同时开**，UI 必须做成二选一。 */
  streamAndDiarizationExclusive: boolean
  maxSeconds: number
}

export interface SttConfigResponse {
  settings: SttSettings
  recommended: SttRecommendedModel[]
  gateway?: SttDiscoveryResult
  gatewayBaseURL: string
  gatewayHasKey: boolean
  channelHints: Record<string, string>
  templates: SttTemplate[]
}

export interface SttProbeResult {
  ok: boolean
  text?: string
  model?: string
  channel?: SttChannel
  transport?: SttTransport
  /** 实际使用的转写模板 id。 */
  provider?: string
  label?: string
  durationMs?: number
  costCents?: number
  /** 上游是否真的给了说话人标签。 */
  diarized?: boolean
  segments?: Array<{ speaker: string; text: string; startMs: number; endMs: number }>
  error?: string
}

/** 全量转写里的一段。失败段 error 非空、text 为空。 */
export interface SttFullSegment {
  index: number
  startSec: number
  endSec: number
  text?: string
  error?: string
}

export interface SttFullResult {
  ok: boolean
  text: string
  segments: SttFullSegment[]
  /** 转写成功的段数。0 表示这次没产出任何内容。 */
  succeeded: number
  /** 失败的段数。前端必须把「有段失败」呈现出来，不能装作完整。 */
  failed: number
  durationMs?: number
  costCents?: number
  model?: string
  channel?: SttChannel
  error?: string
}

export interface SttIncrementalResult {
  ok: boolean
  /** 累计文本（跨片去重后）。段失败时也回填已有文本，不会清空。 */
  text: string
  /** 相对上一片新增的部分，供前端只追加不重排。 */
  delta: string
  startSec: number
  endSec: number
  isFinal?: boolean
  model?: string
  channel?: SttChannel
  costCents?: number
  /** 单片失败原因。非致命：已有文本仍然有效。 */
  error?: string
}

// 走项目统一的 http()：自动带 Bearer、401 刷新、默认 30s 超时。
// 试转/扫描都要出网等模型推理，所以显式给到 LONG 上限。
export const sttSettingsApi = {
  async getConfig(): Promise<SttConfigResponse> {
    return http<SttConfigResponse>('/api/stt/config')
  },

  async saveConfig(body: {
    channel: SttChannel
    gatewayModel: string
    externalBaseURL: string
    externalModel: string
    externalTransport: SttTransport
    /** 转写模板 id。留空 = 后端按地址/模型推断。 */
    provider?: string
    minimaxBaseURL?: string
    minimaxModel?: string
    minimaxStream?: boolean
    minimaxDiarization?: boolean
    language?: string
    /** 留空 = 保留已存的 key；传 '__clear__' 主动清空。 */
    externalApiKey?: string
    /**
     * MiniMax 的 key，与 externalApiKey **分开传**。
     *
     * 留空（undefined）= 本次不改动 MiniMax 凭据。
     * ⚠ 不能沿用 externalApiKey 那套「空串 = 清空」：保存设置时会带上
     * 一堆用户没改的字段，若把「没提交」当成「清空」，用户只改一下语种就会
     * 连带清掉两把 key。所以这里用「不传 = 不动」。
     */
    minimaxApiKey?: string
  }): Promise<SttSettings> {
    return http<SttSettings>('/api/stt/config', {
      method: 'PUT',
      body: JSON.stringify(body),
    })
  },

  /** 强制重扫网关。会真实出网打网关（限流实测 12 次/分钟），所以只在用户点按钮时调。 */
  async discover(signal?: AbortSignal): Promise<SttDiscoveryResult> {
    return http<SttDiscoveryResult>('/api/stt/discover', {
      method: 'POST',
      signal,
      timeoutMs: LONG_REQUEST_TIMEOUT_MS,
    })
  },

  /**
   * **全量**转写：整段长录音一次性拿到完整文字。
   *
   * 服务端会按静音边界自动切段（任何 ASR 都不允许无限长音频单次上传），
   * 逐段转写后按序聚合。所以**长录音不用在前端切**。
   *
   * 必须检查 `failed`：部分段失败时整体仍返回 ok=true（保住成功段的内容），
   * 但前端要把「有 N 段没转出来」告诉用户，否则用户会以为记录是完整的。
   *
   * 传 signal 才能中止（需求「后台执行的 api 可以强行终止」）：服务端
   * handleSttTranscribeFull 是 `context.WithTimeout(r.Context(),
   * fullTranscribeTimeout)`，客户端断开即中止——否则这段最长 10 分钟的兜底
   * 转写期间，用户只能干等。
   */
  async transcribeFull(audioBlob: Blob, filename = 'meeting.wav', signal?: AbortSignal): Promise<SttFullResult> {
    // ★ 与 `sttApi.transcribe` 对齐：容器适配放在**本方法内**，不指望调用方记得做。
    //
    //   §50 实测：录音产物是 webm/opus，而网关的 chat-audio bridge 只收 mp3/wav
    //   （实测 400 `audio format "webm" is not supported`）。
    //   此前只有 `transcribe`（录音中每块）内部调了 `ensureGatewayCompatible`，
    //   而 `transcribeFull`（录完整段）**没有** ⇒ 安全网只盖了一半，
    //   §50 的 bug 正是从这半边漏出去的。
    //
    //   调用方（`refetchFullTranscript`）已经先转过一次并把 filename 选成
    //   `meeting.wav`；这里再跑一次是**零成本**的：`needsWavTranscode('audio/wav')`
    //   为 false ⇒ 原样返回，不会二次转码。
    const blob = await ensureGatewayCompatible(audioBlob)
    const base64 = await blobToBase64(blob)
    // ★ filename 跟着**实际容器**走，不跟调用方给的字符串走。
    //   发 webm 字节却报 .wav，会让后端「按扩展名判容器」的分支与实际不符。
    //   调用方的 filename 只在 blob 没有 type 时兜底（那才是它唯一有信息的时候）。
    const name = blob.type ? filenameForMimeType(blob.type, 'meeting') : filename
    return http<SttFullResult>('/api/stt/transcribe-full', {
      method: 'POST',
      body: JSON.stringify({ audioBase64: base64, filename: name }),
      signal,
      timeoutMs: FULL_TRANSCRIBE_TIMEOUT_MS,
    })
  },

  /**
   * **即时**转写：录音进行中送一片，返回累计文本 + 增量。
   *
   * sessionId 必填——服务端靠它维持跨片去重状态（切片有重叠，不去重会出现
   *「今天今天下午三点」）。一场录音用同一个 id，最后一片传 isFinal。
   */
  async transcribeIncremental(body: {
    /** 原始音频（会被转 base64）。与 `audioBase64` 二选一。 */
    audioBlob?: Blob
    /**
     * 已转好的 base64。与 `audioBlob` 二选一。
     *
     * 录音分片在真机上是 webm，网关只收 mp3/wav，必须由调用方先转成 16k WAV
     * （见 native/recording-audio-transcode.ts）。这种路径下 base64 已在手上，
     * 再走一次 blobToBase64 是纯浪费——12 秒 PCM 实测能撞爆调用栈。
     */
    audioBase64?: string
    sessionId: string
    filename?: string
    startSec?: number
    endSec?: number
    silenceCut?: boolean
    isFinal?: boolean
    reset?: boolean
  }): Promise<SttIncrementalResult> {
    // 两者都缺就是调用方写错了，静默传空 base64 会换来一个 502 empty transcript，
    // 报错形态与「音频是静音」完全一样 —— 必须在边界上炸掉。
    if (!body.audioBlob && !body.audioBase64) {
      throw new Error('transcribeIncremental 需要 audioBlob 或 audioBase64 之一')
    }
    const audioBase64 = body.audioBase64 ?? await blobToBase64(body.audioBlob!)
    return http<SttIncrementalResult>('/api/stt/transcribe-incremental', {
      method: 'POST',
      body: JSON.stringify({
        audioBase64,
        filename: body.filename ?? 'chunk.wav',
        sessionId: body.sessionId,
        startSec: body.startSec ?? 0,
        endSec: body.endSec ?? 0,
        silenceCut: body.silenceCut ?? false,
        isFinal: body.isFinal ?? false,
        reset: body.reset ?? false,
      }),
      timeoutMs: LONG_REQUEST_TIMEOUT_MS,
    })
  },

  /**
   * 用真实录音试转。
   *
   * 自动发现只验证「端点/上游/是否丢音频」，验证不了识别准不准——那必须有
   * 真实语音，而真实语音只在设备上。所以试转收设备录的音频，走与
   * /api/stt/transcribe 相同的 JSON base64 形态。
   */
  async probe(
    audio: Blob,
    opts: {
      model?: string
      channel?: SttChannel
      baseURL?: string
      transport?: SttTransport
      /** 转写模板 id（如 minimax-speech-to-text）。 */
      provider?: string
    } = {},
    signal?: AbortSignal,
  ): Promise<SttProbeResult> {
    // ★ webm/ogg 必须先转成 16k WAV 再试转（缺陷8 后半截，2026-10-06 实测）。
    //   设置页的试转录音走 MediaRecorder，SettingsSTT.vue:529 显式挑 `audio/webm`
    //   （Android WebView 支持），于是 blob.type 是 audio/webm、
    //   filenameForMimeType 给出 `recording.webm`。而网关的 chat-audio bridge
    //   只收 mp3/wav，实测原样发过去会拿到：
    //     invalid_audio_request: audio format "webm" is not supported
    //       by the chat-audio bridge (supported: mp3, wav)
    //   ⇒ 不转码的话，「试转」按钮在真机上永远失败，而这正是用户判断转写
    //   通不通的唯一入口。
    //   api/stt.ts 的转写路径早就 ensureGatewayCompatible 过一处，这里是漏的第二个。
    const audioBlob = await ensureGatewayCompatible(audio)
    const base64 = await blobToBase64(audioBlob)
    return http<SttProbeResult>('/api/stt/probe', {
      method: 'POST',
      body: JSON.stringify({
        audioBase64: base64,
        // 文件名跟着**转码后**的 blob 走：转码成功时是 recording.wav，
        // 用原来的 webm 名会让服务端挑错 bridge。
        filename: filenameForMimeType(audioBlob.type),
        ...opts,
      }),
      timeoutMs: STT_PROBE_TIMEOUT_MS,
      signal,
    })
  },
}

/** 探测结论的中文说明。设置页直接展示，避免用户看到裸枚举值。 */
export function describeSttProbeStatus(c: SttGatewayCandidate): string {
  switch (c.status) {
    case 'ok':
      return '可用'
    case 'no_provider':
      return '网关无上游 provider'
    case 'endpoint_missing':
      return '无转写端点'
    case 'audio_ignored':
      return '上游丢弃音频'
    case 'failed':
      return c.detail ? `探测失败（${c.detail}）` : '探测失败'
    default:
      return '未探测'
  }
}

/** 该候选是否可以选。 */
export function isSttCandidateUsable(c: SttGatewayCandidate): boolean {
  return c.status === 'ok'
}
