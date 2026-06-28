// WebSocket 客户端管理
class WebSocketClient {
  private ws: WebSocket | null = null
  private reconnectTimer: number | null = null
  private reconnectDelay = 3000
  private listeners: Map<string, Set<(data: any) => void>> = new Map()
  private url: string

  constructor(url: string) {
    this.url = url
  }

  connect() {
    if (this.ws?.readyState === WebSocket.OPEN) {
      return
    }

    try {
      this.ws = new WebSocket(this.url)

      this.ws.onopen = () => {
        console.log('WebSocket connected')
        if (this.reconnectTimer) {
          clearTimeout(this.reconnectTimer)
          this.reconnectTimer = null
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

    this.reconnectTimer = window.setTimeout(() => {
      console.log('Reconnecting WebSocket...')
      this.connect()
    }, this.reconnectDelay)
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

// 创建全局 WebSocket 实例
const API_BASE = import.meta.env.VITE_API_BASE || 'http://localhost:8088'
const WS_URL = API_BASE.replace(/^http/, 'ws') + '/ws'

export const wsClient = new WebSocketClient(WS_URL)

// 自动连接
wsClient.connect()

export default wsClient
