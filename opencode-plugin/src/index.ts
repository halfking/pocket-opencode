/**
 * OpenCode Pocket Plugin - Main Entry Point
 * 
 * This plugin integrates OpenCode with Pocket Backend, enabling:
 * - Automatic instance registration
 * - Real-time session monitoring
 * - Remote control capabilities
 * - Status reporting
 */

import { EventEmitter } from 'eventemitter3'
import WebSocket from 'ws'
import type {
  PocketPluginConfig,
  SessionEvent,
  RemoteCommand,
  InstanceInfo,
  MigrateToInput
} from './types'
import { buildMigrationPrompts } from './prompts'

export class OpenCodePocketPlugin extends EventEmitter {
  private ws: WebSocket | null = null
  private config: PocketPluginConfig
  private reconnectTimer: NodeJS.Timeout | null = null
  private heartbeatTimer: NodeJS.Timeout | null = null
  private sessionPollTimer: NodeJS.Timeout | null = null
  private sessionWatchers: Map<string, any> = new Map()

  constructor(config: PocketPluginConfig) {
    super()
    this.config = config
  }

  /**
   * Activate the plugin
   */
  async activate(): Promise<void> {
    console.log('[OpenCode Pocket] Plugin activating...')
    
    // 1. Connect to Pocket Backend
    await this.connectToBackend()
    
    // 2. Register instance
    await this.registerInstance()
    
    // 3. Start session monitoring
    this.startSessionMonitoring()
    
    // 4. Start heartbeat
    this.startHeartbeat()
    
    console.log('[OpenCode Pocket] Plugin activated successfully')
  }

  /**
   * Deactivate the plugin
   */
  async deactivate(): Promise<void> {
    console.log('[OpenCode Pocket] Plugin deactivating...')

    // Stop session polling
    if (this.sessionPollTimer) {
      clearInterval(this.sessionPollTimer)
      this.sessionPollTimer = null
    }

    // Stop heartbeat
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer)
      this.heartbeatTimer = null
    }

    // Stop reconnect timer
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }

    // Unregister instance
    await this.unregisterInstance()

    // Close WebSocket
    if (this.ws) {
      this.ws.close()
      this.ws = null
    }

    console.log('[OpenCode Pocket] Plugin deactivated')
  }

  /**
   * Connect to Pocket Backend WebSocket
   */
  private async connectToBackend(): Promise<void> {
    return new Promise((resolve, reject) => {
      const wsUrl = `${this.config.backendURL}/plugin/ws?type=plugin&id=${this.config.instanceID}`
      
      console.log(`[OpenCode Pocket] Connecting to ${wsUrl}`)
      
      this.ws = new WebSocket(wsUrl, {
        headers: {
          'Authorization': `Bearer ${this.config.auth.token}`
        }
      })

      this.ws.on('open', () => {
        console.log('[OpenCode Pocket] WebSocket connected')
        resolve()
      })

      this.ws.on('error', (error) => {
        console.error('[OpenCode Pocket] WebSocket error:', error)
        reject(error)
      })

      this.ws.on('close', () => {
        console.log('[OpenCode Pocket] WebSocket closed')
        this.scheduleReconnect()
      })

      this.ws.on('message', (data) => {
        this.handleMessage(data.toString())
      })
    })
  }

  /**
   * Register instance with Backend
   */
  private async registerInstance(): Promise<void> {
    const info: InstanceInfo = {
      id: this.config.instanceID,
      displayName: this.config.displayName,
      version: await this.getOpenCodeVersion(),
      capabilities: ['session', 'summary', 'pty'],
      environment: this.detectEnvironment(),
      machine: {
        hostname: require('os').hostname(),
        platform: process.platform,
        arch: process.arch,
        cpus: require('os').cpus().length,
        memory: require('os').totalmem()
      },
      timestamp: new Date().toISOString()
    }

    this.sendMessage({
      type: 'instance.register',
      data: info
    })

    console.log('[OpenCode Pocket] Instance registered:', info.id)
  }

  /**
   * Unregister instance
   */
  private async unregisterInstance(): Promise<void> {
    this.sendMessage({
      type: 'instance.unregister',
      data: { id: this.config.instanceID }
    })
  }

  /**
   * Start session monitoring
   *
   * 实现说明：不依赖 OpenCode 内部事件（避免与版本耦合），而是周期性轮询
   * 公开的 GET /session 接口，diff 出 created/updated/completed 三类事件并上报。
   * 这与 discovery.go 的探测协议保持一致（都走 HTTP API），保证跨 OpenCode 版本兼容。
   */
  private startSessionMonitoring(): void {
    console.log('[OpenCode Pocket] Starting session monitoring (poll /session)')

    // 立即拉一次作为基线
    this.pollSessions().catch((err) => {
      console.warn('[OpenCode Pocket] initial session poll failed:', err?.message || err)
    })

    // 每 15s 轮询一次（与心跳解耦，避免 health 检查阻塞事件上报）
    this.sessionPollTimer = setInterval(() => {
      this.pollSessions().catch((err) => {
        console.warn('[OpenCode Pocket] session poll failed:', err?.message || err)
      })
    }, 15_000)
  }

  /** 轮询 /session 并 diff 出事件。 */
  private async pollSessions(): Promise<void> {
    const base = this.config.opencodeBaseURL.replace(/\/$/, '')
    const resp = await fetch(`${base}/session`, {
      method: 'GET',
      headers: this.ocHeaders(),
      signal: this.timeoutSignal(5000),
    })
    if (!resp.ok) {
      throw new Error(`GET /session returned ${resp.status}`)
    }
    const body = (await resp.json()) as any
    // OpenCode 返回 { sessions: [...] } 或裸数组，兼容两种
    const sessions: any[] = Array.isArray(body) ? body : (body.sessions || body.data || [])

    const now = new Map<string, any>()
    for (const s of sessions) {
      now.set(s.id, s)
    }

    // created: now 有而 prev 没有
    for (const [id, s] of now) {
      if (!this.sessionWatchers.has(id)) {
        this.onSessionCreated(s)
      } else {
        // updated: 时间戳或状态变了
        const prev = this.sessionWatchers.get(id)
        if (this.sessionChanged(prev, s)) {
          this.onSessionUpdated(s, this.diffChanges(prev, s))
        }
      }
    }
    // completed: prev 有而 now 没有（视为完成）
    for (const [id, s] of this.sessionWatchers) {
      if (!now.has(id)) {
        this.onSessionCompleted(s)
      }
    }

    // 更新基线
    this.sessionWatchers = now
  }

  private sessionChanged(prev: any, cur: any): boolean {
    if (!prev) return true
    return (
      prev.status !== cur.status ||
      prev.title !== cur.title ||
      (prev.time?.updated !== cur.time?.updated && prev.updatedAt !== cur.updatedAt)
    )
  }

  private diffChanges(prev: any, cur: any): any {
    const changes: any = {}
    if (!prev) return changes
    if (prev.status !== cur.status) changes.status = { from: prev.status, to: cur.status }
    if (prev.title !== cur.title) changes.title = { from: prev.title, to: cur.title }
    return changes
  }

  /** OpenCode HTTP API 公共请求头。 */
  private ocHeaders(): Record<string, string> {
    const h: Record<string, string> = { 'Content-Type': 'application/json' }
    if (this.config.auth?.token) {
      h['Authorization'] = `Bearer ${this.config.auth.token}`
    }
    return h
  }

  /** 构造一个 AbortSignal 超时控制（兼容 Node 18+）。 */
  private timeoutSignal(ms: number): AbortSignal {
    const ctrl = new AbortController()
    const t = setTimeout(() => ctrl.abort(), ms)
    // allow Node process to exit even if timer is pending
    if (typeof (t as any).unref === 'function') (t as any).unref()
    return ctrl.signal
  }

  /**
   * Handle session created event
   */
  private onSessionCreated(session: any): void {
    console.log('[OpenCode Pocket] Session created:', session.id)
    
    this.sendMessage({
      type: 'session.created',
      data: {
        instanceID: this.config.instanceID,
        session: this.serializeSession(session)
      }
    })
  }

  /**
   * Handle session updated event
   */
  private onSessionUpdated(session: any, changes: any): void {
    console.log('[OpenCode Pocket] Session updated:', session.id)
    
    this.sendMessage({
      type: 'session.updated',
      data: {
        instanceID: this.config.instanceID,
        sessionID: session.id,
        changes
      }
    })
  }

  /**
   * Handle session completed event
   */
  private onSessionCompleted(session: any): void {
    console.log('[OpenCode Pocket] Session completed:', session.id)
    
    this.sendMessage({
      type: 'session.completed',
      data: {
        instanceID: this.config.instanceID,
        session: this.serializeSession(session)
      }
    })
  }

  /**
   * Handle incoming WebSocket message
   */
  private handleMessage(data: string): void {
    try {
      const message = JSON.parse(data)
      
      switch (message.type) {
        case 'command':
          this.handleRemoteCommand(message.data)
          break
        case 'ping':
          this.sendMessage({ type: 'pong' })
          break
        default:
          console.warn('[OpenCode Pocket] Unknown message type:', message.type)
      }
    } catch (error) {
      console.error('[OpenCode Pocket] Failed to parse message:', error)
    }
  }

  /**
   * Handle remote control command
   */
  private async handleRemoteCommand(command: RemoteCommand): Promise<void> {
    console.log('[OpenCode Pocket] Received command:', command.type)
    
    try {
      let result: any
      
      switch (command.type) {
        case 'session.create':
          result = await this.createSession(command.data)
          break
        case 'session.prompt':
          result = await this.sendPrompt(command.data.sessionID, command.data.prompt)
          break
        case 'session.stop':
          result = await this.stopSession(command.data.sessionID)
          break
        case 'session.migrate_to':
          result = await this.migrateTo(command.data as MigrateToInput)
          break
        case 'instance.status':
          result = await this.getInstanceStatus()
          break
        default:
          throw new Error(`Unknown command type: ${command.type}`)
      }
      
      this.sendMessage({
        type: 'command.result',
        data: {
          commandID: command.id,
          success: true,
          result
        }
      })
    } catch (error: any) {
      console.error('[OpenCode Pocket] Command failed:', error)
      
      this.sendMessage({
        type: 'command.result',
        data: {
          commandID: command.id,
          success: false,
          error: error.message
        }
      })
    }
  }

  /**
   * Create a new session — OpenCode HTTP API POST /session
   * (see docs/opencode-contract.md §3.3, upstream
   * packages/opencode/src/server/routes/instance/httpapi/groups/session.ts:87).
   *
   * Body matches Session.CreateInput
   * (packages/opencode/src/session/session.ts:249-259):
   *   { parentID?, title?, agent?, model?, metadata?, permission?, workspaceID? }
   *
   * The pinned contract has NO `id` field at the top level; upstream assigns
   * the sessionID itself. We accept an optional `id` from the caller for
   * cross-instance migration tracking but strip it from the wire body.
   */
  private async createSession(data: any): Promise<any> {
    console.log('[OpenCode Pocket] Creating session:', data)
    const base = this.config.opencodeBaseURL.replace(/\/$/, '')
    const payload: any = {
      parentID: data?.parentID,
      title: data?.title,
      agent: data?.agent || data?.agentType,
      model: data?.model,
      metadata: data?.metadata,
      permission: data?.permission,
      workspaceID: data?.workspaceID,
    }
    const resp = await fetch(`${base}/session`, {
      method: 'POST',
      headers: this.ocHeaders(),
      body: JSON.stringify(payload),
      signal: this.timeoutSignal(10_000),
    })
    if (!resp.ok) {
      const txt = await resp.text().catch(() => '')
      throw new Error(`POST /session returned ${resp.status}: ${txt}`)
    }
    const created = (await resp.json()) as any
    return {
      sessionID: created?.id || created?.sessionID,
      status: 'created',
      raw: created,
    }
  }

  /**
   * Send prompt — OpenCode POST /session/{id}/message
   * (see docs/opencode-contract.md §3.3, upstream
   * packages/opencode/src/server/routes/instance/httpapi/groups/session.ts:95
   * and packages/opencode/src/session/prompt.ts:1579-1601).
   *
   * Pinned wire body (PromptPayload):
   *   { messageID?, model?, agent?, format?, system?, variant?, parts: [...] }
   * `parts` is REQUIRED and uses the SessionV1.PartInput union (text|file|agent|subtask).
   * The legacy `{ prompt: { text }, delivery }` shape is NOT accepted by upstream.
   */
  private async sendPrompt(sessionID: string, prompt: string): Promise<any> {
    console.log('[OpenCode Pocket] Sending prompt to session:', sessionID)
    const base = this.config.opencodeBaseURL.replace(/\/$/, '')
    const payload = {
      parts: [{ type: 'text', text: prompt }],
    }
    const resp = await fetch(`${base}/session/${encodeURIComponent(sessionID)}/message`, {
      method: 'POST',
      headers: this.ocHeaders(),
      body: JSON.stringify(payload),
      signal: this.timeoutSignal(30_000),
    })
    if (!resp.ok) {
      const txt = await resp.text().catch(() => '')
      throw new Error(`POST /session/${sessionID}/message returned ${resp.status}: ${txt}`)
    }
    const result = (await resp.json()) as any
    return {
      sessionID,
      messageID: result?.info?.id || result?.messageID || result?.id,
      status: 'sent',
      raw: result,
    }
  }

  /**
   * Stop a session — OpenCode POST /session/{id}/abort
   * (see docs/opencode-contract.md §3.3, upstream
   * packages/opencode/src/server/routes/instance/httpapi/groups/session.ts:91).
   * The legacy /interrupt path is removed in pinned upstream.
   */
  private async stopSession(sessionID: string): Promise<any> {
    console.log('[OpenCode Pocket] Stopping session:', sessionID)
    const base = this.config.opencodeBaseURL.replace(/\/$/, '')
    const resp = await fetch(`${base}/session/${encodeURIComponent(sessionID)}/abort`, {
      method: 'POST',
      headers: this.ocHeaders(),
      signal: this.timeoutSignal(5_000),
    })
    if (!resp.ok && resp.status !== 404) {
      const txt = await resp.text().catch(() => '')
      throw new Error(`POST /session/${sessionID}/interrupt returned ${resp.status}: ${txt}`)
    }
    return { sessionID, status: 'stopped' }
  }

  /**
   * 会话迁移入口（session.migrate_to 命令）：
   * 1. 从 packURL 拉取迁移包（llm-gateway-go /v1/sessions/{id}/pack）
   * 2. 按 promptTemplates 用 buildMigrationPrompts 拼接注入提示词
   * 3. 在本机 OpenCode 创建新会话并发送第一条 prompt
   * 4. 回报新 sessionID（Pocket 据此建立 task_session_links 映射）
   *
   * 这是跨主机迁移在本机的"执行端"，与 Pocket 迁移服务编排配合。
   */
  private async migrateTo(input: MigrateToInput): Promise<any> {
    console.log('[OpenCode Pocket] migrate_to from', input.packURL)

    // 1. 拉迁移包
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (input.packToken) headers['Authorization'] = `Bearer ${input.packToken}`
    const resp = await fetch(input.packURL, {
      method: 'GET',
      headers,
      signal: this.timeoutSignal(30_000),
    })
    if (!resp.ok) {
      const txt = await resp.text().catch(() => '')
      throw new Error(`fetch pack failed ${resp.status}: ${txt}`)
    }
    const pack = (await resp.json()) as any

    // 2. 拼接提示词
    const promptText = buildMigrationPrompts(
      pack,
      (input.promptTemplates || ['env_sync', 'task_resume', 'result_verify']) as any,
    )

    // 3. 创建新会话（工作目录优先用入参，其次包内 session_meta.directory）
    const createData: any = {
      agent: input.agent,
      model: input.model,
      workingDirectory: input.workingDirectory || pack?.session_meta?.directory,
    }
    const created = await this.createSession(createData)
    const newSessionID = created.sessionID
    if (!newSessionID) {
      throw new Error('migrate_to: createSession did not return sessionID')
    }

    // 4. 发送首条 prompt（续接）
    await this.sendPrompt(newSessionID, promptText)

    return {
      newSessionID,
      fromSessionID: pack?.session_meta?.id,
      status: 'migrated',
      promptChars: promptText.length,
    }
  }

  /**
   * Get instance status
   */
  private async getInstanceStatus(): Promise<any> {
    const os = require('os')
    
    return {
      instanceID: this.config.instanceID,
      uptime: process.uptime(),
      memory: {
        total: os.totalmem(),
        free: os.freemem(),
        used: os.totalmem() - os.freemem()
      },
      cpu: os.loadavg(),
      sessions: {
        active: this.sessionWatchers.size,
        total: this.sessionWatchers.size
      }
    }
  }

  /**
   * Start heartbeat
   */
  private startHeartbeat(): void {
    this.heartbeatTimer = setInterval(() => {
      this.sendMessage({
        type: 'heartbeat',
        data: {
          instanceID: this.config.instanceID,
          timestamp: new Date().toISOString()
        }
      })
    }, this.config.reportInterval * 1000)
  }

  /**
   * Schedule reconnect
   */
  private scheduleReconnect(): void {
    if (this.reconnectTimer) return
    
    console.log('[OpenCode Pocket] Scheduling reconnect in 5s')
    
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      this.connectToBackend().catch((error) => {
        console.error('[OpenCode Pocket] Reconnect failed:', error)
        this.scheduleReconnect()
      })
    }, 5000)
  }

  /**
   * Send message to Backend
   */
  private sendMessage(message: any): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      console.warn('[OpenCode Pocket] WebSocket not ready, message queued')
      return
    }
    
    this.ws.send(JSON.stringify(message))
  }

  /**
   * Serialize session for transmission
   */
  private serializeSession(session: any): any {
    return {
      id: session.id,
      title: session.title || 'Untitled Session',
      status: session.status || 'active',
      createdAt: session.createdAt || new Date().toISOString(),
      updatedAt: session.updatedAt || new Date().toISOString(),
      messageCount: session.messageCount || 0,
      // Add more fields as needed
    }
  }

  /**
   * Get OpenCode version — OpenCode GET /global/health
   * (see docs/opencode-contract.md §3.1, upstream
   * packages/opencode/src/server/routes/instance/httpapi/groups/global.ts:68).
   * Response: { healthy: true, version: string }.
   * Falls back to 0.1.0 if the request fails.
   */
  private async getOpenCodeVersion(): Promise<string> {
    try {
      const base = this.config.opencodeBaseURL.replace(/\/$/, '')
      const resp = await fetch(`${base}/global/health`, {
        signal: this.timeoutSignal(2000),
      })
      if (resp.ok) {
        const body = (await resp.json()) as any
        if (body?.version) return body.version
      }
    } catch {
      // ignore —— 回退默认值
    }
    return '0.1.0'
  }

  /**
   * Detect environment
   */
  private detectEnvironment(): 'development' | 'staging' | 'production' {
    // Simple environment detection
    const env = process.env.NODE_ENV
    if (env === 'production') return 'production'
    if (env === 'staging') return 'staging'
    return 'development'
  }
}

// Export types
export * from './types'
export default OpenCodePocketPlugin
