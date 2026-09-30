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

/** 转写通道。 */
export type SttChannel = 'auto' | 'gateway' | 'external'

/** 传输形态。 */
export type SttTransport = 'auto' | 'transcriptions' | 'chat-audio'

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
  language: string
  hasExternalKey: boolean
  updatedAt?: number
  /** 后端算出的当前生效模型（只读）。 */
  effectiveModel?: string
  effectiveNote?: string
}

export interface SttConfigResponse {
  settings: SttSettings
  recommended: SttRecommendedModel[]
  gateway?: SttDiscoveryResult
  gatewayBaseURL: string
  gatewayHasKey: boolean
  channelHints: Record<SttChannel, string>
}

export interface SttProbeResult {
  ok: boolean
  text?: string
  model?: string
  channel?: SttChannel
  transport?: SttTransport
  label?: string
  durationMs?: number
  costCents?: number
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
    language?: string
    /** 留空 = 保留已存的 key；传 '__clear__' 主动清空。 */
    externalApiKey?: string
  }): Promise<SttSettings> {
    return http<SttSettings>('/api/stt/config', {
      method: 'PUT',
      body: JSON.stringify(body),
    })
  },

  /** 强制重扫网关。会真实出网打网关（限流实测 12 次/分钟），所以只在用户点按钮时调。 */
  async discover(): Promise<SttDiscoveryResult> {
    return http<SttDiscoveryResult>('/api/stt/discover', {
      method: 'POST',
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
    opts: { model?: string; channel?: SttChannel; baseURL?: string; transport?: SttTransport } = {},
  ): Promise<SttProbeResult> {
    const base64 = await blobToBase64(audio)
    return http<SttProbeResult>('/api/stt/probe', {
      method: 'POST',
      body: JSON.stringify({
        audioBase64: base64,
        filename: filenameForMimeType(audio.type),
        ...opts,
      }),
      timeoutMs: LONG_REQUEST_TIMEOUT_MS,
    })
  },
}

function filenameForMimeType(mimeType: string): string {
  const normalized = (mimeType || '').toLowerCase().split(';', 1)[0]
  const ext = {
    'audio/mp4': 'm4a',
    'audio/mpeg': 'mp3',
    'audio/ogg': 'ogg',
    'audio/wav': 'wav',
    'audio/webm': 'webm',
  }[normalized]
  return `recording.${ext || 'webm'}`
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
