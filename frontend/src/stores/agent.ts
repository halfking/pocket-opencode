/**
 * agent.ts — S0-D Agent Bridge 状态管理。
 *
 * 列出当前 workspace 的 agent，send 时记录 lastResult（UI 显示 session_id）。
 */
import { defineStore } from 'pinia'
import { agentsApi, type Agent, type SendResult } from '../api/agents'

export const useAgentStore = defineStore('agent', {
  state: () => ({
    agents: [] as Agent[],
    loading: false,
    lastSendResult: null as SendResult | null,
  }),
  getters: {
    online: (s) => s.agents.filter((a) => a.status === 'online'),
    busy: (s) => s.agents.filter((a) => a.status === 'busy'),
  },
  actions: {
    async load() {
      this.loading = true
      try {
        this.agents = await agentsApi.list()
      } finally {
        this.loading = false
      }
    },
    async create(input: {
      instance_id: string
      name: string
      role?: Agent['role']
      capabilities?: string[]
    }) {
      const a = await agentsApi.create(input)
      this.agents.push(a)
      return a
    },
    async send(
      agentId: string,
      input: Parameters<typeof agentsApi.send>[1],
    ) {
      this.lastSendResult = await agentsApi.send(agentId, input)
      // send 后该 agent 大概率变 busy，刷新状态。
      const idx = this.agents.findIndex((a) => a.id === agentId)
      if (idx >= 0) this.agents[idx].status = 'busy'
      return this.lastSendResult
    },
    async remove(id: string) {
      await agentsApi.delete(id)
      this.agents = this.agents.filter((a) => a.id !== id)
    },
  },
})
