/**
 * GatewayLiveClient — 订阅网关实时请求流（泳道）。
 *
 * 后端通道：GET /api/llm-gateway/nodes/{id}/live/event
 * 路径含 /event 是有意的：pocketd 的 requireAuth 只对含 /event 的路径接受
 * ?token=，而 EventSource 无法设置 Authorization 头。
 *
 * 上游信封（llm-gateway-go admin.LiveStreamEnvelope）：
 *   initial_data — 连接后首帧，带 requests[] + 完整 snapshot
 *   request      — 单条请求完成/转入进行中，带 delta
 *   idle_marker  — 静默 1 分钟，带 delta + lane_ids
 *   ping         — 保活，忽略
 *   incident_update — 路由故障诊断更新
 *
 * 后端做字节级透传，不解析信封，所以这里直接消费上游契约。
 */

const API_BASE = import.meta.env.VITE_API_BASE || ''

export interface LiveStreamStats {
  total: number
  success: number
  failure: number
  rate_limited: number
  in_progress: number
}

export interface LiveStreamTile {
  request_id: string
  timestamp: string
  model: string
  vendor: string
  provider: string
  status: string
  error_kind?: string
  latency_ms?: number
  cost_usd?: number
  prompt_tokens?: number
  completion_tokens?: number
  is_probe?: boolean
  probe_origin?: string
  probe_attempt?: number
}

export interface LiveStreamLane {
  id: string
  name: string
  dimension: string
  requests: LiveStreamTile[]
  stats: LiveStreamStats
  isOthers: boolean
}

export interface LiveStreamLegendItem {
  key: string
  name: string
  count: number
}

export interface LiveStreamSnapshot {
  summary: LiveStreamStats
  detail_dimensions: Record<string, LiveStreamLane[]>
  dimensions: Record<string, LiveStreamLane[]>
  dimension_legends: Record<string, LiveStreamLegendItem[]>
  status_legends: LiveStreamLegendItem[]
  latest_request_ts?: string
}

export interface LiveStreamDelta {
  summary: LiveStreamStats
  changed_lanes: Record<string, LiveStreamLane[]>
  dimension_legends: Record<string, LiveStreamLegendItem[]>
  status_legends: LiveStreamLegendItem[]
}

export interface LiveStreamEnvelope {
  type: 'initial_data' | 'request' | 'idle_marker' | 'ping' | 'incident_update' | string
  ts: string
  request?: any
  requests?: any[]
  snapshot?: LiveStreamSnapshot
  delta?: LiveStreamDelta
  health?: any
  incident?: any
  lane_ids?: string[]
}

export interface GatewayLiveHandlers {
  onOpen?: () => void
  onEnvelope?: (env: LiveStreamEnvelope) => void
  onError?: (err: Event | Error) => void
}

export class GatewayLiveClient {
  private es: EventSource | null = null
  private closed = false
  private retryDelay = 1500
  private readonly maxRetryDelay = 30000
  private watchdog: number | null = null

  constructor(
    private nodeId: number,
    private getToken: () => string | null,
    private handlers: GatewayLiveHandlers = {},
  ) {}

  open() {
    this.closed = false
    this.connect()
    // 兜底：EventSource 自身的重连在某些 WebView 里不触发，
    // 这里每 30s 检查一次连接状态。
    this.watchdog = window.setInterval(() => {
      if (!this.es || this.es.readyState === EventSource.CLOSED) {
        this.reconnect()
      }
    }, 30000)
  }

  close() {
    this.closed = true
    if (this.watchdog !== null) {
      clearInterval(this.watchdog)
      this.watchdog = null
    }
    if (this.es) {
      this.es.close()
      this.es = null
    }
  }

  private connect() {
    if (this.closed) return

    const token = this.getToken()
    const params = new URLSearchParams()
    if (token) params.set('token', token)
    const url = `${API_BASE}/api/llm-gateway/nodes/${this.nodeId}/live/event?${params}`

    this.es = new EventSource(url)

    this.es.onopen = () => {
      // 连上就把退避重置，避免一次抖动后长期停在 30s 间隔。
      this.retryDelay = 1500
      this.handlers.onOpen?.()
    }

    // 上游用默认事件（data: {...}），信封里的 type 字段区分变体，
    // 所以只需 onmessage，不必 addEventListener 逐类型注册。
    this.es.onmessage = (e: MessageEvent) => {
      if (!e.data) return
      let parsed: LiveStreamEnvelope
      try {
        parsed = JSON.parse(e.data)
      } catch {
        return
      }
      if (parsed.type === 'ping') return
      this.handlers.onEnvelope?.(parsed)
    }

    this.es.onerror = (e) => {
      this.handlers.onError?.(e)
      if (!this.closed) this.reconnect()
    }
  }

  private reconnect() {
    if (this.closed) return
    if (this.es) {
      this.es.close()
      this.es = null
    }
    const delay = this.retryDelay
    this.retryDelay = Math.min(this.retryDelay * 2, this.maxRetryDelay)
    window.setTimeout(() => this.connect(), delay)
  }
}
