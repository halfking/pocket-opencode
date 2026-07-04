/**
 * SSE 客户端 - 用于订阅 OpenCode 实时事件流。
 *
 * 后端通道：GET /api/mobile/sessions/{id}/event?instance_id=xxx
 * 服务端发出事件类型（OpenCode V2 envelope）：
 *   - server.connected     → 握手成功
 *   - session.next.text.delta
 *   - session.next.reasoning.delta
 *   - session.next.tool.called / .progress / .success / .failed
 *   - session.next.step.started / .ended / .failed
 *   - session.next.shell.started / .ended
 *   - session.next.compaction.*
 *   - error / upstream.closed
 */

export interface SessionEvent {
  type: string
  data: any
  /** 序号，用于重连时续传 */
  seq?: number
}

export interface SessionSSEHandlers {
  onOpen?: () => void
  onMessage?: (evt: SessionEvent) => void
  onError?: (err: Event | Error) => void
  onClose?: () => void
}

const API_BASE = import.meta.env.VITE_API_BASE || ''

export class SessionSSEClient {
  private es: EventSource | null = null
  private lastSeq: number | undefined
  private retryDelay = 1500
  private closed = false
  private heartbeatTimer: number | null = null

  constructor(
    private sessionID: string,
    private instanceID: string,
    private getToken: () => string | null,
    private handlers: SessionSSEHandlers = {},
  ) {}

  open() {
    this.closed = false
    this.connect()
    // 兜底心跳：30s 无消息主动 ping 后端（实际上 server 已经每 15s 发 : ping）
    this.heartbeatTimer = window.setInterval(() => {
      if (!this.es || this.es.readyState === EventSource.CLOSED) {
        this.reconnect()
      }
    }, 30000)
  }

  close() {
    this.closed = true
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }
    if (this.es) {
      this.es.close()
      this.es = null
    }
  }

  private connect() {
    if (this.closed) return

    const params = new URLSearchParams({ instance_id: this.instanceID })
    if (this.lastSeq !== undefined) {
      params.set('after', String(this.lastSeq))
    }
    const url = `${API_BASE}/api/mobile/sessions/${encodeURIComponent(this.sessionID)}/event?${params}`

    // EventSource 不支持自定义 header；token 走 query string（开发模式）
    // 生产应由 server 端做 JWT cookie 鉴权。当前 dev 模式 token 注入 query。
    const token = this.getToken()
    const finalUrl = token
      ? `${url}${url.includes('?') ? '&' : '?'}access_token=${encodeURIComponent(token)}`
      : url

    this.es = new EventSource(finalUrl)

    this.es.onopen = () => {
      this.handlers.onOpen?.()
    }

    this.es.onmessage = (e: MessageEvent) => {
      this.handleMessage('message', e.data)
    }

    // server 给每个事件类型发 "event: <type>"，onmessage 不会触发，
    // 我们必须用 addEventListener 监听。
    const KNOWN_TYPES = [
      'server.connected',
      'session.next.text.delta',
      'session.next.text.started',
      'session.next.text.ended',
      'session.next.reasoning.delta',
      'session.next.reasoning.started',
      'session.next.reasoning.ended',
      'session.next.tool.called',
      'session.next.tool.progress',
      'session.next.tool.success',
      'session.next.tool.failed',
      'session.next.tool.input.delta',
      'session.next.tool.input.started',
      'session.next.tool.input.ended',
      'session.next.step.started',
      'session.next.step.ended',
      'session.next.step.failed',
      'session.next.shell.started',
      'session.next.shell.ended',
      'session.next.compaction.started',
      'session.next.compaction.delta',
      'session.next.compaction.ended',
      'session.next.prompted',
      'session.next.prompt.admitted',
      'session.next.context.updated',
      'error',
      'upstream.closed',
    ]
    for (const t of KNOWN_TYPES) {
      this.es.addEventListener(t, (e: MessageEvent) => {
        this.handleMessage(t, e.data)
      })
    }

    this.es.onerror = (e) => {
      this.handlers.onError?.(e)
      if (!this.closed) {
        this.reconnect()
      }
    }
  }

  private handleMessage(type: string, dataStr: string | null) {
    if (!dataStr) return
    let parsed: any = null
    try {
      parsed = JSON.parse(dataStr)
    } catch {
      parsed = { raw: dataStr }
    }

    // 提取 seq（durable.seq / seq）
    const seq = parsed?.durable?.seq ?? parsed?.seq
    if (typeof seq === 'number') {
      this.lastSeq = seq
    }

    this.handlers.onMessage?.({ type, data: parsed, seq: this.lastSeq })
  }

  private reconnect() {
    if (this.closed) return
    if (this.es) {
      this.es.close()
      this.es = null
    }
    setTimeout(() => {
      if (!this.closed) this.connect()
    }, this.retryDelay)
    // 指数退避（最大 15s）
    this.retryDelay = Math.min(this.retryDelay * 1.5, 15000)
  }
}