import { resolveRuntimeApiBase } from '../config/api-base'
import { nextReconnectDelay } from './reconnectPolicy'
import { buildWebSocketUrl } from './websocket-url'

// WebSocket 客户端管理
class WebSocketClient {
  private ws: WebSocket | null = null
  private reconnectTimer: number | null = null
  private reconnectAttempts = 0
  private listeners: Map<string, Set<(data: any) => void>> = new Map()
  /** 连接成功回调(2026-09-20):每次成功 open 都触发(含登录后首连与断线
   * 重连)。消费方在此做增量 resync —— hub 不回放事件,断线窗口只能靠拉。 */
  private connectedCallbacks = new Set<() => void>()
  private url: string

  constructor(url: string) {
    this.url = url
  }

  connect() {
    if (this.ws?.readyState === WebSocket.OPEN) {
      return
    }

    const nextUrl = getWsUrl()
    if (!nextUrl) {
      // 基址不可用（未配置 / 非法 scheme）。这是配置问题不是网络抖动，
      // 重连多少次都不会变好，因此不排重连——否则会变成停不下来的重连循环。
      // 修好基址后调用方重新 connectWs() 即可。
      console.warn('WebSocket connect skipped: API base 无法构造合法的 ws 地址')
      return
    }

    try {
      // 每次连接用当前 pocketd 基址 + 最新 token（设置里改基址后 reload 即可）
      this.url = nextUrl
      this.ws = new WebSocket(this.url)

      this.ws.onopen = () => {
        console.log('WebSocket connected')
        if (this.reconnectTimer) {
          clearTimeout(this.reconnectTimer)
          this.reconnectTimer = null
        }
        this.reconnectAttempts = 0
        for (const cb of this.connectedCallbacks) {
          try { cb() } catch (err) { console.error('Error in connected handler:', err) }
        }
      }

      this.ws.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data)
          this.handleMessage(message)
        } catch (err) {
          console.error('Failed to parse WebSocket message:', err)
        }
      }

      this.ws.onerror = (error) => {
        console.error('WebSocket error:', error)
      }

      this.ws.onclose = () => {
        console.log('WebSocket disconnected')
        this.scheduleReconnect()
      }
    } catch (err) {
      console.error('Failed to connect WebSocket:', err)
      this.scheduleReconnect()
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) return

    const delay = nextReconnectDelay(this.reconnectAttempts)
    this.reconnectAttempts++
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null
      console.log(`Reconnecting WebSocket (attempt ${this.reconnectAttempts}, ${delay}ms backoff)...`)
      this.connect()
    }, delay)
  }

  /** 注册「连接成功」回调(每次成功 open 都触发,含首连与重连);返回反注册函数。 */
  onConnected(cb: () => void): () => void {
    this.connectedCallbacks.add(cb)
    return () => { this.connectedCallbacks.delete(cb) }
  }

  private handleMessage(message: { type: string; payload: any }) {
    const listeners = this.listeners.get(message.type)
    if (listeners) {
      listeners.forEach((callback) => {
        try {
          callback(message.payload)
        } catch (err) {
          console.error('Error in WebSocket message handler:', err)
        }
      })
    }

    // 同时触发 'message' 事件（通用监听）
    const generalListeners = this.listeners.get('message')
    if (generalListeners) {
      generalListeners.forEach((callback) => {
        try {
          callback(message)
        } catch (err) {
          console.error('Error in WebSocket general handler:', err)
        }
      })
    }
  }

  on(eventType: string, callback: (data: any) => void) {
    if (!this.listeners.has(eventType)) {
      this.listeners.set(eventType, new Set())
    }
    this.listeners.get(eventType)!.add(callback)
  }

  off(eventType: string, callback: (data: any) => void) {
    const listeners = this.listeners.get(eventType)
    if (listeners) {
      listeners.delete(callback)
    }
  }

  send(type: string, payload: any) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type, payload }))
    } else {
      console.warn('WebSocket is not connected')
    }
  }

  disconnect() {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    if (this.ws) {
      this.ws.close()
      this.ws = null
    }
  }

  getState(): number {
    return this.ws?.readyState ?? WebSocket.CLOSED
  }

  isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN
  }
}

const TOKEN_KEY = 'pocket_token'

function wsHttpBase(): string {
  // 与 HTTP API 共用运行时解析：Capacitor 的 https://localhost 在缺少
  // build default 时必须回退到生产/配置后的真实 pocketd 地址，不能让 WS
  // 误连 WebView 自身并被 mixed-content 拦截。
  return resolveRuntimeApiBase() || (typeof window !== 'undefined' ? window.location.origin : '')
}

/** 返回可用的 ws 地址；基址不可用时返回 null（= 不要连），而不是造一个非法 URL。 */
function getWsUrl(): string | null {
  return buildWebSocketUrl(wsHttpBase(), localStorage.getItem(TOKEN_KEY))
}

export const wsClient = new WebSocketClient(getWsUrl() ?? '')

/**
 * 延迟建立 WS 连接：仅在已登录（localStorage 中存在 pocket_token）时才 connect。
 *
 * 历史问题：模块加载即 wsClient.connect()，导致未登录也建立无认证的 WS。
 * 现在改为显式调用 —— 由 main.ts / LoginView 在认证成功后触发，
 * 重复调用安全（已连接则 no-op，未登录则 no-op）。
 *
 * 注意：handler 注册（ws-bus.initWsBus）与连接分离，互不影响。
 */
export function connectWs(): void {
  const token = localStorage.getItem(TOKEN_KEY)
  if (!token) {
    // 未登录：不建立 WS，避免无认证连接
    return
  }
  wsClient.connect()
}

export default wsClient
