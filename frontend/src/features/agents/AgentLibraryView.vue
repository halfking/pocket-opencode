<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useChatAgentStore } from '../../stores/chatAgentStore'
import { useConfirm } from '../../composables/useConfirm'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import AgentSyncSheet from '../ai-chat/AgentSyncSheet.vue'

const router = useRouter()
const agentStore = useChatAgentStore()
const { confirm } = useConfirm()

const searchQuery = ref('')
const selectedDepartment = ref<string>('') // 空字符串 = 全部
const showOnlyCustom = ref(false)
const syncSheetOpen = ref(false)

// 同步角标（云端有角色但本地没有 → 显示提醒）
const hasRemoteAgents = computed(() => {
  const status = agentStore.syncStatus
  return status?.has_remote && (status?.agent_count ?? 0) > 0
})

onMounted(() => {
  agentStore.loadAgents()
  // 后台探测同步可用性（PG 未启用时静默失败）
  agentStore.checkSyncAvailable()
})

// 过滤后的角色列表
const filteredAgents = computed(() => {
  let list = agentStore.agents

  // 部门筛选
  if (selectedDepartment.value) {
    list = list.filter((a) => a.department === selectedDepartment.value)
  }

  // 仅自定义
  if (showOnlyCustom.value) {
    list = list.filter((a) => !a.is_builtin)
  }

  // 搜索
  if (searchQuery.value) {
    list = agentStore.searchAgents(searchQuery.value)
    // 搜索后再应用部门和自定义筛选
    if (selectedDepartment.value) {
      list = list.filter((a) => a.department === selectedDepartment.value)
    }
    if (showOnlyCustom.value) {
      list = list.filter((a) => !a.is_builtin)
    }
  }

  return list
})

// 按部门分组（仅在无搜索/筛选时）
const groupedAgents = computed(() => {
  const groups: Record<string, typeof agentStore.agents> = {}
  for (const agent of filteredAgents.value) {
    if (!groups[agent.department]) groups[agent.department] = []
    groups[agent.department].push(agent)
  }
  return groups
})

// 部门标签映射（动态部门列表自带 label，未知部门回退原文）
const departmentLabels = computed(() => {
  const map: Record<string, string> = {}
  for (const d of agentStore.departments) {
    map[d.key] = d.label
  }
  return map
})

function goToDetail(agentId: string) {
  router.push(`/agents/${agentId}`)
}

function goToCreate() {
  router.push('/agents/new')
}

async function handleDelete(agentId: string, agentName: string, isBuiltin: boolean, e: Event) {
  e.stopPropagation()
  const message = isBuiltin
    ? `「${agentName}」是内置专家角色，删除后将从专家库永久移除（其他设备也不再可见）。确定删除吗？`
    : `确定要删除角色"${agentName}"吗？`
  if (!(await confirm({ title: '删除角色', message, confirmText: '删除', danger: true }))) return
  agentStore.deleteAgent(agentId).catch((err) => {
    alert(`删除失败：${err.message || err}`)
  })
}
</script>

<template>
  <div class="agent-library-view">
    <!-- 壳层渲染标题栏与返回键；同步/创建两个操作经 Portal 放入标题栏右侧 -->
    <HeaderActionsPortal>
      <button type="button" aria-label="云端同步" @click="syncSheetOpen = true">
        <span class="material-symbols-outlined">cloud_sync</span>
        <span v-if="hasRemoteAgents" class="sync-badge" aria-label="云端有新内容"></span>
      </button>
      <button type="button" aria-label="创建角色" @click="goToCreate">
        <span class="material-symbols-outlined">add</span>
      </button>
    </HeaderActionsPortal>

    <!-- 搜索框 -->
    <div class="search-section">
      <input
        v-model="searchQuery"
        type="text"
        placeholder="搜索角色名称、部门或技能..."
        class="search-input"
      />
    </div>

    <!-- 部门筛选 -->
    <div class="department-filter">
      <button
        :class="['dept-chip', { active: selectedDepartment === '' }]"
        @click="selectedDepartment = ''"
      >
        全部
      </button>
      <button
        v-for="dept in agentStore.departments"
        :key="dept.key"
        :class="['dept-chip', { active: selectedDepartment === dept.key }]"
        @click="selectedDepartment = dept.key"
      >
        {{ dept.label }}
      </button>
    </div>

    <!-- 自定义筛选 -->
    <div class="filter-row">
      <label class="toggle-label">
        <input v-model="showOnlyCustom" type="checkbox" />
        <span>仅显示我的自定义角色</span>
      </label>
    </div>

    <!-- 角色列表 -->
    <main class="agents-list">
      <div v-if="agentStore.loading" class="loading">加载中...</div>
      <div v-else-if="filteredAgents.length === 0" class="empty">
        <p>{{ searchQuery ? '未找到匹配的角色' : '暂无角色' }}</p>
        <button v-if="!searchQuery" class="create-btn" @click="goToCreate">
          创建第一个自定义角色
        </button>
      </div>

      <template v-else>
        <!-- 搜索/筛选时扁平展示 -->
        <template v-if="searchQuery || selectedDepartment || showOnlyCustom">
          <div
            v-for="agent in filteredAgents"
            :key="agent.id"
            class="agent-card"
            @click="goToDetail(agent.id)"
          >
            <div class="agent-emoji">{{ agent.emoji || '👤' }}</div>
            <div class="agent-info">
              <div class="agent-name-row">
                <span class="agent-name">{{ agent.name }}</span>
                <span v-if="!agent.is_builtin" class="custom-badge">自定义</span>
              </div>
              <div class="agent-desc">{{ agent.description }}</div>
              <div class="agent-dept">{{ departmentLabels[agent.department] || agent.department }}</div>
            </div>
            <button
              class="delete-btn"
              aria-label="删除"
              @click="handleDelete(agent.id, agent.name, agent.is_builtin, $event)"
            >
              <span class="material-symbols-outlined">delete</span>
            </button>
          </div>
        </template>

        <!-- 默认按部门分组 -->
        <template v-else>
          <div v-for="(agents, dept) in groupedAgents" :key="dept" class="department-group">
            <h2 class="group-header">
              {{ departmentLabels[dept] || dept }}
              <span class="group-count">({{ agents.length }})</span>
            </h2>
            <div
              v-for="agent in agents"
              :key="agent.id"
              class="agent-card"
              @click="goToDetail(agent.id)"
            >
              <div class="agent-emoji">{{ agent.emoji || '👤' }}</div>
              <div class="agent-info">
                <div class="agent-name-row">
                  <span class="agent-name">{{ agent.name }}</span>
                  <span v-if="!agent.is_builtin" class="custom-badge">自定义</span>
                </div>
                <div class="agent-desc">{{ agent.description }}</div>
              </div>
              <button
                class="delete-btn"
                aria-label="删除"
                @click="handleDelete(agent.id, agent.name, agent.is_builtin, $event)"
              >
                <span class="material-symbols-outlined">delete</span>
              </button>
            </div>
          </div>
        </template>
      </template>
    </main>

    <!-- 同步面板 -->
    <AgentSyncSheet v-model:show="syncSheetOpen" />
  </div>
</template>

<style scoped>
.agent-library-view {
  min-height: 100%;
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
}

.sync-badge {
  position: absolute;
  top: 6px;
  right: 6px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--brand-primary, #3b82f6);
  box-shadow: 0 0 0 2px var(--bg-card, #fff);
}

.title {
  flex: 1;
  margin: 0;
  font-size: 17px;
  font-weight: 600;
  text-align: center;
}

.search-section {
  padding: 12px 16px;
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}

.search-input {
  width: 100%;
  padding: 10px 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 14px;
  background: var(--bg-base);
  color: var(--text-primary);
}

.search-input:focus {
  outline: none;
  border-color: var(--brand-primary);
}

.department-filter {
  display: flex;
  gap: 6px;
  padding: 12px 16px;
  overflow-x: auto;
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}

.dept-chip {
  flex: none;
  padding: 6px 14px;
  border: 1px solid var(--border);
  border-radius: 16px;
  background: var(--bg-base);
  font-size: 13px;
  white-space: nowrap;
  cursor: pointer;
  color: var(--text-secondary);
}

.dept-chip.active {
  background: var(--brand-primary);
  color: white;
  border-color: var(--brand-primary);
}

.filter-row {
  padding: 8px 16px;
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}

.toggle-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--text-secondary);
  cursor: pointer;
}

.agents-list {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
}

.loading,
.empty {
  text-align: center;
  padding: 40px 20px;
  color: var(--text-secondary);
  font-size: 14px;
}

.create-btn {
  margin-top: 16px;
  padding: 10px 20px;
  background: var(--brand-primary);
  color: white;
  border: none;
  border-radius: 8px;
  font-size: 14px;
  cursor: pointer;
}

.department-group {
  margin-bottom: 24px;
}

.group-header {
  margin: 0 0 12px;
  font-size: 14px;
  font-weight: 600;
  color: var(--text-secondary);
  padding-left: 4px;
}

.group-count {
  font-weight: normal;
  color: var(--text-muted);
}

.agent-card {
  display: flex;
  gap: 12px;
  padding: 14px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 10px;
  margin-bottom: 8px;
  cursor: pointer;
  transition: background 0.2s;
}

.agent-card:hover {
  background: var(--bg-hover);
}

.agent-emoji {
  font-size: 32px;
  line-height: 1;
  flex-shrink: 0;
}

.agent-info {
  flex: 1;
  min-width: 0;
}

.agent-name-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.agent-name {
  font-size: 15px;
  font-weight: 600;
  color: var(--text-primary);
}

.custom-badge {
  font-size: 10px;
  padding: 2px 6px;
  background: var(--brand-primary);
  color: white;
  border-radius: 4px;
}

.agent-desc {
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.4;
  margin-bottom: 4px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.agent-dept {
  font-size: 11px;
  color: var(--text-muted);
}

.delete-btn {
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.delete-btn:hover {
  color: var(--danger);
}
</style>
