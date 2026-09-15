/**
 * local-agent/agentStore.ts — 本地智能体的 Pinia 桥。
 *
 * localAgentRuntime 是纯 TS 进程单例(非响应式);store 负责把它的会话状态
 * 镜像成响应式数据。事件到达时 sync() 以浅拷贝替换会话数组/时间线数组,
 * 触发 Vue 重渲染(共享的条目对象即使被 runtime 原地改写,也会随数组
 * 换引用而重新读取)。
 */
import { defineStore } from 'pinia'
import {
  localAgentRuntime,
  type AgentSession,
  type PendingApproval,
} from '../../localagent/runtime.ts'

let subscribed = false

export const useLocalAgentStore = defineStore('localAgent', {
  state: () => ({
    sessions: [] as AgentSession[],
    activeId: null as string | null,
    pendingApproval: null as PendingApproval | null,
    /** 运行中状态(从 runtime 镜像,供 composer 禁用)。 */
    running: false,
  }),

  getters: {
    activeSession(state): AgentSession | null {
      if (!state.activeId) return null
      return state.sessions.find((s) => s.id === state.activeId) ?? null
    },
    /** 时间线里未定型的流式尾巴文本(渲染时隐藏尾部协议围栏)。 */
    streamingTail(state): string | null {
      const s = state.sessions.find((x) => x.id === state.activeId)
      if (!s) return null
      const last = s.timeline[s.timeline.length - 1]
      if (last && last.kind === 'assistant' && last.interim) return last.text ?? ''
      return null
    },
    experts() {
      return localAgentRuntime.listExperts()
    },
    skills() {
      return localAgentRuntime.listSkills()
    },
  },

  actions: {
    /** View onMounted 时调用一次:建订阅 + 恢复最近会话。 */
    init() {
      if (subscribed) {
        this.sync()
        return
      }
      subscribed = true
      localAgentRuntime.subscribe(() => this.sync())
      this.sync()
      if (!this.activeId) {
        const latest = this.sessions[0]
        if (latest) this.activeId = latest.id
      }
    },

    sync() {
      // 条目也要克隆:runtime 会原地改写工具卡状态(running→completed),
      // 共享引用会让 Vue 因 props 未变而跳过子组件重渲染(状态卡死在"执行中")。
      this.sessions = localAgentRuntime
        .listSessions()
        .map((s) => ({ ...s, timeline: s.timeline.map((it) => ({ ...it })) }))
      if (this.activeId) {
        this.running = localAgentRuntime.isRunning(this.activeId)
        this.pendingApproval = localAgentRuntime.getPendingApproval(this.activeId) ?? null
      } else {
        this.running = false
        this.pendingApproval = null
      }
    },

    newSession(expert = 'general') {
      const s = localAgentRuntime.createSession(expert)
      this.sessions = [s, ...this.sessions.map((x) => ({ ...x, timeline: x.timeline.map((it) => ({ ...it })) }))]
      this.activeId = s.id
      this.pendingApproval = null
      this.running = false
      return s
    },

    selectSession(id: string) {
      this.activeId = id
      this.sync()
    },

    deleteSession(id: string) {
      localAgentRuntime.deleteSession(id)
      if (this.activeId === id) {
        this.activeId = this.sessions.find((s) => s.id !== id)?.id ?? null
      }
      this.sync()
    },

    send(prompt: string, opts: { expert?: string; skills?: string[]; model?: string } = {}) {
      if (!this.activeId) this.newSession(opts.expert ?? 'general')
      const handle = localAgentRuntime.send(this.activeId as string, prompt, opts)
      this.sync()
      return handle
    },

    stop() {
      if (this.activeId) localAgentRuntime.abort(this.activeId)
      this.sync()
    },

    respondApproval(allow: boolean) {
      if (!this.activeId || !this.pendingApproval) return
      localAgentRuntime.respondApproval(this.activeId, this.pendingApproval.toolCallId, allow)
      this.sync()
    },
  },
})
