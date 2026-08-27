<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useChatAgentStore, DEPARTMENTS } from '../../stores/chatAgentStore'
import type { ChatAgent } from '../../types/chatAgent'

const props = defineProps<{
  show: boolean
  currentAgentId?: string
}>()

const emit = defineEmits<{
  'update:show': [value: boolean]
  'select': [agent: ChatAgent]
  'clear': []
}>()

const agentStore = useChatAgentStore()
const searchQuery = ref('')
const selectedDepartment = ref<string>('') // 空字符串表示全部

onMounted(() => {
  if (agentStore.agents.length === 0) {
    agentStore.loadAgents()
  }
})

const filteredAgents = computed(() => {
  let list = agentStore.agents
  
  // 部门筛选
  if (selectedDepartment.value) {
    list = list.filter(a => a.department === selectedDepartment.value)
  }
  
  // 搜索
  if (searchQuery.value) {
    list = agentStore.searchAgents(searchQuery.value)
  }
  
  return list
})

// 按部门分组（仅显示有角色的部门）
const groupedAgents = computed(() => {
  const groups: Record<string, ChatAgent[]> = {}
  for (const agent of filteredAgents.value) {
    if (!groups[agent.department]) {
      groups[agent.department] = []
    }
    groups[agent.department].push(agent)
  }
  return groups
})

// 部门显示名映射
const departmentLabels = computed(() => {
  const map: Record<string, string> = {}
  for (const d of DEPARTMENTS) {
    map[d.key] = d.label
  }
  return map
})

function handleSelect(agent: ChatAgent) {
  emit('select', agent)
  emit('update:show', false)
}

function handleClear() {
  emit('clear')
  emit('update:show', false)
}

function close() {
  emit('update:show', false)
}
</script>

<template>
  <div v-if="show" class="agent-selector-overlay" @click="close">
    <div class="agent-selector-sheet" @click.stop>
      <!-- 标题栏（只有一个标题） -->
      <div class="sheet-header">
        <h2>选择智能体角色</h2>
        <button class="close-btn" @click="close" aria-label="关闭">×</button>
      </div>

      <!-- 搜索框 -->
      <div class="search-section">
        <input
          v-model="searchQuery"
          type="text"
          placeholder="搜索角色名称、部门或技能..."
          class="search-input"
        />
      </div>

      <!-- 部门筛选（横向滚动标签） -->
      <div class="department-filter">
        <button
          :class="['dept-chip', { active: selectedDepartment === '' }]"
          @click="selectedDepartment = ''"
        >
          全部
        </button>
        <button
          v-for="dept in DEPARTMENTS"
          :key="dept.key"
          :class="['dept-chip', { active: selectedDepartment === dept.key }]"
          @click="selectedDepartment = dept.key"
        >
          {{ dept.label }}
        </button>
      </div>

      <!-- 角色列表（按部门分组） -->
      <div class="agents-list">
        <div v-if="agentStore.loading" class="loading">加载中...</div>
        <div v-else-if="filteredAgents.length === 0" class="empty">
          未找到匹配的角色
        </div>
        <template v-else>
          <!-- 如果搜索/筛选，扁平展示；否则按部门分组 -->
          <template v-if="searchQuery || selectedDepartment">
            <div
              v-for="agent in filteredAgents"
              :key="agent.id"
              :class="['agent-item', { active: agent.id === currentAgentId }]"
              @click="handleSelect(agent)"
            >
              <div class="agent-emoji">{{ agent.emoji || '🤖' }}</div>
              <div class="agent-info">
                <div class="agent-name">{{ agent.name }}</div>
                <div class="agent-desc">{{ agent.description }}</div>
              </div>
            </div>
          </template>
          <template v-else>
            <!-- 按部门分组展示 -->
            <div v-for="(agents, dept) in groupedAgents" :key="dept" class="department-group">
              <div class="group-header">
                {{ departmentLabels[dept] || dept }} ({{ agents.length }})
              </div>
              <div
                v-for="agent in agents"
                :key="agent.id"
                :class="['agent-item', { active: agent.id === currentAgentId }]"
                @click="handleSelect(agent)"
              >
                <div class="agent-emoji">{{ agent.emoji || '🤖' }}</div>
                <div class="agent-info">
                  <div class="agent-name">{{ agent.name }}</div>
                  <div class="agent-desc">{{ agent.description }}</div>
                </div>
              </div>
            </div>
          </template>
        </template>
      </div>

      <!-- 底部操作 -->
      <div class="sheet-footer">
        <button class="clear-btn" @click="handleClear">清除角色</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.agent-selector-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 1000;
  display: flex;
  align-items: flex-end;
}

.agent-selector-sheet {
  width: 100%;
  max-height: 85vh;
  background: var(--bg-primary, #fff);
  border-radius: 16px 16px 0 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.sheet-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border-color, #e5e7eb);
}

.sheet-header h2 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}

.close-btn {
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  font-size: 28px;
  line-height: 1;
  color: var(--text-secondary, #6b7280);
  cursor: pointer;
}

.search-section {
  padding: 12px 20px;
  border-bottom: 1px solid var(--border-color, #e5e7eb);
}

.search-input {
  width: 100%;
  padding: 10px 16px;
  border: 1px solid var(--border-color, #e5e7eb);
  border-radius: 8px;
  font-size: 15px;
}

.department-filter {
  display: flex;
  gap: 8px;
  padding: 12px 20px;
  overflow-x: auto;
  border-bottom: 1px solid var(--border-color, #e5e7eb);
}

.dept-chip {
  padding: 6px 14px;
  border: 1px solid var(--border-color, #e5e7eb);
  border-radius: 16px;
  background: var(--bg-primary, #fff);
  font-size: 14px;
  white-space: nowrap;
  cursor: pointer;
  transition: all 0.2s;
}

.dept-chip.active {
  background: var(--primary-color, #3b82f6);
  color: white;
  border-color: var(--primary-color, #3b82f6);
}

.agents-list {
  flex: 1;
  overflow-y: auto;
  padding: 16px 20px;
}

.loading,
.empty {
  text-align: center;
  padding: 32px 20px;
  color: var(--text-secondary, #6b7280);
}

.department-group {
  margin-bottom: 24px;
}

.group-header {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-secondary, #6b7280);
  margin-bottom: 8px;
  padding-left: 4px;
}

.agent-item {
  display: flex;
  gap: 12px;
  padding: 12px;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.2s;
}

.agent-item:hover {
  background: var(--bg-hover, #f3f4f6);
}

.agent-item.active {
  background: var(--primary-color-light, #eff6ff);
  border: 1px solid var(--primary-color, #3b82f6);
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

.agent-name {
  font-size: 15px;
  font-weight: 500;
  margin-bottom: 4px;
}

.agent-desc {
  font-size: 13px;
  color: var(--text-secondary, #6b7280);
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.sheet-footer {
  padding: 16px 20px;
  border-top: 1px solid var(--border-color, #e5e7eb);
}

.clear-btn {
  width: 100%;
  padding: 12px;
  border: 1px solid var(--border-color, #e5e7eb);
  border-radius: 8px;
  background: white;
  font-size: 15px;
  color: var(--text-secondary, #6b7280);
  cursor: pointer;
}
</style>
