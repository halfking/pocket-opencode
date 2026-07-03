/**
 * websocket-hub.ts — 📡 WebSocket 实时通信管理器
 * 
 * 职责：
 * - WebSocket 连接管理
 * - 消息收发处理
 * - 自动重连机制
 * - 实时数据更新
 * - 离线消息队列
 */

export type WSMessageType = 
  | 'note_created'
  | 'note_updated'
  | 'email_received'
  | 'ai_completed'
  | 'meeting_started'
  | 'notification'

export interface WSMessage {
  type: WSMessageType
  data: any
  timestamp: number
  id?: string
}

type MessageHandler = (message: WSMessage) => void

class WebSocketHub {
  private ws: WebSocket | null = null
  private url: string = ''
  private reconnectAttempts = 0
  private readonly maxReconnectAttempts = 5
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private pingTimer: ReturnType<typeof setInterval> | null = null
  private handlers: Map<WSMessageType, Set<MessageHandler>> = new Map()
  private offlineQueue: WSMessage[] = []
  private isConnected = false

  /**
   * 连接 WebSocket 服务器
   */
  connect(url: string): Promise<void> {
    this.url = url

    return new Promise((resolve, reject) => {
      try {
        this.ws = new WebSocket(url)

        this.ws.onopen = () => {
          console.log('[WebSocketHub] 连接成功')
          this.isConnected = true
          this.reconnectAttempts = 0
          this.startPing()
          this.flushOfflineQueue()
          resolve()
        }

        this.ws.onmessage = (event) => {
          try {
            const message: WSMessage = JSON.parse(event.data)
            this.handleMessage(message)
          } catch (error) {
            console.error('[WebSocketHub] 解析消息失败', error)
          }
        }

        this.ws.onerror = (error) => {
          console.error('[WebSocketHub] 连接错误', error)
          this.isConnected = false
          reject(error)
        }

        this.ws.onclose = () => {
          console.log('[WebSocketHub] 连接关闭')
          this.isConnected = false
          this.stopPing()
          this.reconnect()
        }
      } catch (error) {
        console.error('[WebSocketHub] 创建连接失败', error)
        reject(error)
      }
    })
  }

  /**
   * 断开连接
   */
  disconnect(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    
    this.stopPing()
    
    if (this.ws) {
      this.ws.close()
      this.ws = null
    }
    
    this.isConnected = false
    console.log('[WebSocketHub] 已断开连接')
  }

  /**
   * 发送消息
   */
  send(message: WSMessage): void {
    if (this.isConnected && this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(message))
    } else {
      // 离线时加入队列
      console.warn('[WebSocketHub] 连接未就绪，消息加入离线队列')
      this.offlineQueue.push(message)
    }
  }

  /**
   * 订阅消息类型
   */
  on(type: WSMessageType, handler: MessageHandler): () => void {
    if (!this.handlers.has(type)) {
      this.handlers.set(type, new Set())
    }
    this.handlers.get(type)!.add(handler)

    // 返回取消订阅函数
    return () => {
      this.handlers.get(type)?.delete(handler)
    }
  }

  /**
   * 取消订阅
   */
  off(type: WSMessageType, handler: MessageHandler): void {
    this.handlers.get(type)?.delete(handler)
  }

  /**
   * 获取连接状态
   */
  getStatus(): 'connected' | 'connecting' | 'disconnected' {
    if (!this.ws) return 'disconnected'
    
    switch (this.ws.readyState) {
      case WebSocket.CONNECTING:
        return 'connecting'
      case WebSocket.OPEN:
        return 'connected'
      default:
        return 'disconnected'
    }
  }

  /**
   * 处理收到的消息
   */
  private handleMessage(message: WSMessage): void {
    console.log(`[WebSocketHub] 收到消息: ${message.type}`, message.data)

    // 调用所有注册的处理器
    const handlers = this.handlers.get(message.type)
    if (handlers) {
      handlers.forEach(handler => {
        try {
          handler(message)
        } catch (error) {
          console.error(`[WebSocketHub] 处理器执行失败: ${message.type}`, error)
        }
      })
    }
  }

  /**
   * 自动重连
   */
  private reconnect(): void {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      console.error('[WebSocketHub] 达到最大重连次数，停止重连')
      return
    }

    this.reconnectAttempts++
    const delay = Math.min(1000 * Math.pow(2, this.reconnectAttempts), 30000)
    
    console.log(`[WebSocketHub] ${delay}ms 后尝试第 ${this.reconnectAttempts} 次重连...`)
    
    this.reconnectTimer = setTimeout(() => {
      this.connect(this.url).catch(error => {
        console.error('[WebSocketHub] 重连失败', error)
      })
    }, delay)
  }

  /**
   * 发送心跳包
   */
  private startPing(): void {
    this.pingTimer = setInterval(() => {
      if (this.isConnected) {
        this.send({
          type: 'notification',
          data: { action: 'ping' },
          timestamp: Date.now(),
        })
      }
    }, 30000) // 每 30 秒一次心跳
  }

  /**
   * 停止心跳
   */
  private stopPing(): void {
    if (this.pingTimer) {
      clearInterval(this.pingTimer)
      this.pingTimer = null
    }
  }

  /**
   * 清空离线队列
   */
  private flushOfflineQueue(): void {
    if (this.offlineQueue.length === 0) return

    console.log(`[WebSocketHub] 发送离线队列中的 ${this.offlineQueue.length} 条消息`)
    
    while (this.offlineQueue.length > 0) {
      const message = this.offlineQueue.shift()
      if (message) {
        this.send(message)
      }
    }
  }
}

// 单例导出
export const wsHub = new WebSocketHub()
