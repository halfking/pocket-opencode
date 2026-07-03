/**
 * OpenCode 实例和会话管理 Store
 * 负责管理 OpenCode 实例、会话列表、实时状态等
 */
import { defineStore } from 'pinia'
import { api } from '../api/client'
import type { Instance, Session, Task } from '../api/client'

export interface OpenCodeSession extends Session {
  instanceId: string
  messageCount?: number
  fileChanges?: {
    additions: number
    deletions: number
    files: number
  }
  duration?: number
  createdAt?: string
  updatedAt?: string
}

export interface OpenCodeInstance extends Instance {
  activeSessions: number
  totalSessions: number
  status: 'online' | 'offline' | 'busy'
}

interface OpenCodeState {
  instances: OpenCodeInstance[]
  selectedInstance: OpenCodeInstance | null
  sessions: OpenCodeSession[]
  sessionHistory: Record<string, HistoryEvent[]>
  realTimeStatus: Record<string, string>
  loading: boolean
  error: string | null
}

export interface HistoryEvent {
  timestamp: string
  type: 'message' | 'edit' | 'test' | 'error'
  actor: 'user' | 'ai' | 'system'
  content: string
  metadata?: Record<string, any>
}

export const useOpenCodeStore = defineStore('opencode', {
  state: (): OpenCodeState => ({
    instances: [],
    selectedInstance: null,
    sessions: [],
    sessionHistory: {},
    realTimeStatus: {},
    loading: false,
    error: null
  }),

  getters: {
    // 活跃会话 (busy/retry 状态)
    activeSessions: (state) => 
      state.sessions.filter(s => s.status === 'busy' || s.status === 'retry'),
    
    // 空闲会话
    idleSessions: (state) => 
      state.sessions.filter(s => s.status === 'idle'),
    
    // 已完成会话 (可以通过 updatedAt 判断最近是否活跃)
    completedSessions: (state) => 
      state.sessions.filter(s => {
        if (s.status === 'idle' && s.updatedAt) {
          const lastUpdate = new Date(s.updatedAt)
          const hoursSinceUpdate = (Date.now() - lastUpdate.getTime()) / (1000 * 60 * 60)
          return hoursSinceUpdate > 24 // 24小时未更新视为已完成
        }
        return false
      }),
    
    // 在线实例
    onlineInstances: (state) => 
      state.instances.filter(i => i.status === 'online'),
    
    // 离线实例
    offlineInstances: (state) => 
      state.instances.filter(i => i.status === 'offline'),
    
    // 获取特定会话的实时状态
    getSessionStatus: (state) => (sessionId: string) => 
      state.realTimeStatus[sessionId] || 'unknown'
  },

  actions: {
    /**
     * 加载所有 OpenCode 实例
     */
    async loadInstances() {
      this.loading = true
      this.error = null
      try {
        const instances = await api.getInstances()
        
        // 增强实例数据：添加会话统计
        this.instances = await Promise.all(instances.map(async (inst) => {
          try {
            // 获取该实例的会话列表以统计数量
            const sessions = await api.getSessions(`http://${inst.id}`) // 假设 id 可以构造 URL
            const activeSessions = sessions.filter(s => s.status === 'busy' || s.status === 'retry').length
            
            return {
              ...inst,
              activeSessions,
              totalSessions: sessions.length,
              status: (inst.health === 'healthy' ? 'online' : 'offline') as 'online' | 'offline' | 'busy'
            }
          } catch (err) {
            // 如果获取会话失败，使用默认值
            return {
              ...inst,
              activeSessions: 0,
              totalSessions: 0,
              status: 'offline' as const
            }
          }
        }))
        
        console.log('✅ 加载实例成功:', this.instances.length)
      } catch (err: any) {
        console.error('❌ 加载实例失败:', err)
        this.error = err.message || '加载实例失败'
        this.instances = []
      } finally {
        this.loading = false
      }
    },

    /**
     * 选择实例并加载其会话
     */
    async selectInstance(instanceId: string) {
      const instance = this.instances.find(i => i.id === instanceId)
      if (!instance) {
        this.error = '实例不存在'
        return
      }

      this.selectedInstance = instance
      await this.loadSessions(instanceId)
    },

    /**
     * 加载指定实例的会话列表
     */
    async loadSessions(instanceId: string) {
      this.loading = true
      this.error = null
      try {
        // 通过任务 API 获取会话（OpenCode Session = Task）
        const tasks = await api.getTasks(instanceId)
        
        // 将 Task 映射为 OpenCodeSession
        this.sessions = tasks.map(task => ({
          id: task.id,
          instanceId,
          title: task.title,
          status: task.status,
          messageCount: task.sessionCount || 0,
          createdAt: task.createdAt,
          updatedAt: task.updatedAt,
          // fileChanges 需要从详情接口获取，这里先用默认值
          fileChanges: {
            additions: 0,
            deletions: 0,
            files: 0
          }
        }))
        
        console.log('✅ 加载会话成功:', this.sessions.length)
      } catch (err: any) {
        console.error('❌ 加载会话失败:', err)
        this.error = err.message || '加载会话失败'
        this.sessions = []
      } finally {
        this.loading = false
      }
    },

    /**
     * 加载会话的详细历史
     */
    async loadSessionHistory(sessionId: string) {
      this.loading = true
      this.error = null
      try {
        // TODO: 后端需要实现 /api/opencode/sessions/{id}/history
        const response = await fetch(`/api/opencode/sessions/${sessionId}/history`)
        if (!response.ok) {
          throw new Error(`Failed to load history: ${response.statusText}`)
        }
        const data = await response.json()
        this.sessionHistory[sessionId] = data.timeline || []
        
        console.log('✅ 加载历史成功:', sessionId)
      } catch (err: any) {
        console.error('❌ 加载历史失败:', err)
        this.error = err.message || '加载历史失败'
        // 提供模拟数据作为降级
        this.sessionHistory[sessionId] = []
      } finally {
        this.loading = false
      }
    },

    /**
     * 获取会话摘要
     */
    async getSessionSummary(sessionId: string): Promise<string> {
      try {
        // TODO: 后端需要实现 /api/opencode/sessions/{id}/summary
        const response = await fetch(`/api/opencode/sessions/${sessionId}/summary`)
        if (!response.ok) {
          throw new Error(`Failed to get summary: ${response.statusText}`)
        }
        const data = await response.json()
        return data.summary || '暂无摘要'
      } catch (err: any) {
        console.error('❌ 获取摘要失败:', err)
        return '获取摘要失败'
      }
    },

    /**
     * 更新会话的实时状态
     */
    updateRealTimeStatus(sessionId: string, status: string) {
      this.realTimeStatus[sessionId] = status
      
      // 同时更新 sessions 列表中的状态
      const session = this.sessions.find(s => s.id === sessionId)
      if (session) {
        session.status = status
      }
      
      console.log('🔄 更新会话状态:', sessionId, status)
    },

    /**
     * 订阅 WebSocket 实时更新
     */
    subscribeToRealTimeUpdates() {
      // WebSocket 连接
      const wsUrl = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`
      const ws = new WebSocket(wsUrl)

      ws.onopen = () => {
        console.log('✅ WebSocket 已连接')
      }

      ws.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data)
          
          switch (message.type) {
            case 'session_started':
            case 'session_updated':
              this.updateRealTimeStatus(message.sessionId, message.status)
              break
              
            case 'session_completed':
              this.updateRealTimeStatus(message.sessionId, 'completed')
              // 可以触发会话列表刷新
              if (this.selectedInstance) {
                this.loadSessions(this.selectedInstance.id)
              }
              break
          }
        } catch (err) {
          console.error('❌ 解析 WebSocket 消息失败:', err)
        }
      }

      ws.onerror = (error) => {
        console.error('❌ WebSocket 错误:', error)
      }

      ws.onclose = () => {
        console.log('⚠️ WebSocket 已断开，5秒后重连...')
        setTimeout(() => this.subscribeToRealTimeUpdates(), 5000)
      }

      return ws
    },

    /**
     * 刷新当前选中实例的数据
     */
    async refresh() {
      if (this.selectedInstance) {
        await this.loadSessions(this.selectedInstance.id)
      } else {
        await this.loadInstances()
      }
    },

    /**
     * 清空选择
     */
    clearSelection() {
      this.selectedInstance = null
      this.sessions = []
    }
  }
})
