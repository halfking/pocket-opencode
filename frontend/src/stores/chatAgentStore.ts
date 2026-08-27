import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { chatAgentApi } from '../api/chatAgent'
import type { ChatAgent, SyncPayload, SyncResult, SyncStatus } from '../types/chatAgent'

// 部门清单（与后端 agency-agents-zh 统计一致）
export const DEPARTMENTS = [
  { key: 'specialized', label: '专业领域', count: 46 },
  { key: 'marketing', label: '营销', count: 36 },
  { key: 'engineering', label: '工程', count: 35 },
  { key: 'game-development', label: '游戏开发', count: 20 },
  { key: 'strategy', label: '策略', count: 16 },
  { key: 'integrations', label: '集成', count: 13 },
  { key: 'testing', label: '测试', count: 9 },
  { key: 'sales', label: '销售', count: 8 },
  { key: 'finance', label: '财务', count: 8 },
  { key: 'design', label: '设计', count: 8 },
  { key: 'support', label: '支持', count: 7 },
  { key: 'paid-media', label: '付费媒体', count: 7 },
  { key: 'spatial-computing', label: '空间计算', count: 6 },
  { key: 'project-management', label: '项目管理', count: 6 },
  { key: 'academic', label: '学术', count: 6 },
  { key: 'supply-chain', label: '供应链', count: 5 },
  { key: 'product', label: '产品', count: 5 },
  { key: 'legal', label: '法律', count: 2 },
  { key: 'hr', label: '人力资源', count: 2 },
] as const

export const useChatAgentStore = defineStore('chatAgent', () => {
  const agents = ref<ChatAgent[]>([])
  const loading = ref(false)
  const error = ref('')

  // ────────────────────────────────────────────────────────────────────────
  // 云端同步状态
  // ────────────────────────────────────────────────────────────────────────
  const syncAvailable = ref(false)     // 后端是否启用了 PG 同步
  const syncing = ref(false)
  const lastSyncAt = ref(0)             // 本地最近一次成功同步的毫秒时间戳
  const syncStatus = ref<SyncStatus | null>(null)
  const syncError = ref('')

  // 按部门分组
  const byDepartment = computed(() => {
    const groups: Record<string, ChatAgent[]> = {}
    for (const a of agents.value) {
      if (!groups[a.department]) groups[a.department] = []
      groups[a.department].push(a)
    }
    return groups
  })

  // 内置角色
  const builtinAgents = computed(() => agents.value.filter((a) => a.is_builtin))

  // 自定义角色
  const customAgents = computed(() => agents.value.filter((a) => !a.is_builtin))

  /**
   * 加载所有角色（内置 + 自定义）
   */
  async function loadAgents(department?: string) {
    loading.value = true
    error.value = ''
    try {
      agents.value = await chatAgentApi.list(department)
    } catch (e: any) {
      error.value = e?.message || String(e)
      throw e
    } finally {
      loading.value = false
    }
  }

  /**
   * 根据 id 查找角色（本地 cache）
   */
  function getAgent(id: string): ChatAgent | undefined {
    return agents.value.find((a) => a.id === id)
  }

  /**
   * 创建自定义角色
   */
  async function createAgent(
    req: Omit<ChatAgent, 'id' | 'workspace_id' | 'is_builtin' | 'created_at' | 'updated_at'>,
  ): Promise<ChatAgent> {
    const created = await chatAgentApi.create({
      name: req.name,
      description: req.description,
      department: req.department,
      emoji: req.emoji,
      color: req.color,
      system_prompt: req.system_prompt,
    })
    agents.value.push(created)
    return created
  }

  /**
   * 更新自定义角色
   */
  async function updateAgent(
    id: string,
    updates: Partial<Omit<ChatAgent, 'id' | 'workspace_id' | 'is_builtin'>>,
  ): Promise<ChatAgent> {
    const updated = await chatAgentApi.update(id, {
      name: updates.name,
      description: updates.description,
      department: updates.department,
      emoji: updates.emoji,
      color: updates.color,
      system_prompt: updates.system_prompt,
    })
    const idx = agents.value.findIndex((a) => a.id === id)
    if (idx >= 0) {
      agents.value[idx] = updated
    }
    return updated
  }

  /**
   * 删除自定义角色
   */
  async function deleteAgent(id: string): Promise<void> {
    await chatAgentApi.delete(id)
    const idx = agents.value.findIndex((a) => a.id === id)
    if (idx >= 0) {
      agents.value.splice(idx, 1)
    }
  }

  /**
   * 搜索角色（按 name/department/description）
   */
  function searchAgents(query: string): ChatAgent[] {
    if (!query) return agents.value
    const q = query.toLowerCase()
    return agents.value.filter(
      (a) =>
        a.name.toLowerCase().includes(q) ||
        a.department.toLowerCase().includes(q) ||
        a.description.toLowerCase().includes(q),
    )
  }

  // ────────────────────────────────────────────────────────────────────────
  // 云端同步方法
  // ────────────────────────────────────────────────────────────────────────

  /**
   * 检查后端是否启用了云端同步（PG 池）。
   * 端点 503 → 不可用。
   */
  async function checkSyncAvailable() {
    try {
      const status = await chatAgentApi.syncStatus()
      syncStatus.value = status
      syncAvailable.value = true
    } catch (e: any) {
      // 503 → 不可用（PG 未启用）
      if (e?.status === 503) {
        syncAvailable.value = false
      } else {
        syncError.value = e?.message || String(e)
      }
    }
  }

  /**
   * 上传本地自定义角色到云端。
   * @throws 409 Conflict 当本地 version 早于服务端
   */
  async function syncUpload(): Promise<SyncResult> {
    syncing.value = true
    syncError.value = ''
    try {
      const payload: SyncPayload = {
        version: lastSyncAt.value,
        agents: customAgents.value,
      }
      const result = await chatAgentApi.syncUpload(payload)
      lastSyncAt.value = result.version
      // 上传成功后刷新本地：服务端版本号已更新
      await refreshSyncStatus()
      return result
    } catch (e: any) {
      if (e?.status === 409) {
        // 版本冲突：解析 body 拿 server_version
        const body = e?.body || {}
        syncError.value = `版本冲突：服务端版本 ${body.server_version || '?'} 比本地新`
        throw e
      }
      syncError.value = e?.message || String(e)
      throw e
    } finally {
      syncing.value = false
    }
  }

  /**
   * 从云端下载并合并到本地。
   *
   * 合并策略（简化版）：
   *   - 按 id 比较：本地 + 服务端 id 集合
   *   - 服务端版本更新（updated_at > 本地）→ 用服务端覆盖本地
   *   - 本地独有（不在服务端）→ 保留
   *   - 冲突（两边都有且 updated_at 都 > lastSyncAt）→ 服务端优先（last-write-wins）
   */
  async function syncDownload(): Promise<{ merged: number; downloaded: number }> {
    syncing.value = true
    syncError.value = ''
    try {
      const remote = await chatAgentApi.syncDownload()
      lastSyncAt.value = remote.version

      // 按 id 建索引
      const localMap = new Map<string, ChatAgent>()
      for (const a of agents.value) {
        if (!a.is_builtin) {
          localMap.set(a.id, a)
        }
      }

      const merged: ChatAgent[] = [...agents.value.filter((a) => a.is_builtin)]
      let downloaded = 0

      for (const remoteAgent of remote.agents) {
        const local = localMap.get(remoteAgent.id)
        if (!local) {
          // 服务端独有 → 加入
          merged.push(remoteAgent)
          downloaded++
        } else if (remoteAgent.updated_at > local.updated_at) {
          // 服务端更新 → 覆盖本地
          merged.push(remoteAgent)
          downloaded++
        } else {
          // 本地更新 → 保留本地
          merged.push(local)
        }
      }

      agents.value = merged
      await refreshSyncStatus()
      return { merged: merged.length, downloaded }
    } catch (e: any) {
      syncError.value = e?.message || String(e)
      throw e
    } finally {
      syncing.value = false
    }
  }

  async function refreshSyncStatus() {
    try {
      syncStatus.value = await chatAgentApi.syncStatus()
    } catch (e: any) {
      // ignore
    }
  }

  return {
    agents,
    loading,
    error,
    byDepartment,
    builtinAgents,
    customAgents,
    loadAgents,
    getAgent,
    createAgent,
    updateAgent,
    deleteAgent,
    searchAgents,
    // 同步
    syncAvailable,
    syncing,
    lastSyncAt,
    syncStatus,
    syncError,
    checkSyncAvailable,
    syncUpload,
    syncDownload,
    refreshSyncStatus,
  }
})
