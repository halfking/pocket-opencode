/**
 * Type definitions for OpenCode Pocket Plugin
 */

export interface PocketPluginConfig {
  /** Backend WebSocket URL (Pocket pocketd, 默认 http://localhost:8088) */
  backendURL: string

  /** Unique instance identifier */
  instanceID: string

  /** Human-readable display name */
  displayName: string

  /** Auto-register on activation */
  autoRegister: boolean

  /** Status report interval (seconds) */
  reportInterval: number

  /** 本机 OpenCode/ZCode 实例自己的 HTTP API 根地址（默认 http://localhost:14096）。
   *  用于会话监控与远程命令执行（createSession/sendPrompt/stopSession/migrate_to）。
   *  与 discovery.go 扫描端口对齐。 */
  opencodeBaseURL: string

  /** Authentication configuration */
  auth: {
    type: 'token' | 'jwt'
    token: string
  }
}

export interface InstanceInfo {
  id: string
  displayName: string
  version: string
  capabilities: string[]
  environment: 'development' | 'staging' | 'production'
  machine: MachineInfo
  timestamp: string
}

export interface MachineInfo {
  hostname: string
  platform: string
  arch: string
  cpus: number
  memory: number
}

export interface SessionEvent {
  type: 'created' | 'updated' | 'completed'
  instanceID: string
  session: SessionInfo
  changes?: any
}

export interface SessionInfo {
  id: string
  title: string
  status: 'active' | 'busy' | 'completed' | 'error'
  createdAt: string
  updatedAt: string
  messageCount: number
  metadata?: Record<string, any>
}

export interface RemoteCommand {
  id: string
  /** session.create | session.prompt | session.stop | instance.status | session.migrate_to */
  type: string
  data: any
  timestamp: string
}

/** migrate_to 命令的入参：从云端拉取迁移包并在本机创建新会话。 */
export interface MigrateToInput {
  /** 迁移包拉取地址（llm-gateway-go /v1/sessions/{id}/pack 或 Pocket 中转） */
  packURL: string
  /** 鉴权 token（Bearer） */
  packToken?: string
  /** 要启用的提示词模板：env_sync / task_resume / result_verify / acc_report */
  promptTemplates?: string[]
  /** 工作目录覆盖（跨主机路径重映射） */
  workingDirectory?: string
  /** 默认 agent/model */
  agent?: string
  model?: string
}

export interface CommandResult {
  commandID: string
  success: boolean
  result?: any
  error?: string
}

export interface WebSocketMessage {
  type: string
  data?: any
}
