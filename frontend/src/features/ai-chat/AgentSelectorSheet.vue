<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useChatAgentStore } from '../../stores/chatAgentStore'
import type { ChatAgent } from '../../types/chatAgent'
import BottomSheet from '../../components/base/BottomSheet.vue'
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
    // §847：store 会 rethrow，接住以免变成未处理 rejection；失败已被转成
    // `agentStore.error` 并由模板渲染（同一个 ref，跨视图共享）。
    agentStore.loadAgents().catch(() => {})
  }
})

function retryLoad() {
  agentStore.loadAgents().catch(() => {})
}

/**
 * §847：这里展示的错误位在 store 里**已经过 `storeApiError` 归一**，所以直接展示；
 * 若哪天那个 store 改成存原始 message，这一行要跟着改成走归一函数。
 * 另：这个 computed 顺手把错误文案收敛成一个具名值 ——
 * 模板里不再直接出现 store 的属性访问，「归一在哪一层」与「展示什么」一眼可分。
 */
const agentErrorText = computed(() => agentStore.error)

const filteredAgents = computed(() => {
  let list = agentStore.agents

  // 部门筛选
  if (selectedDepartment.value) {
    list = list.filter(a => a.department === selectedDepartment.value)
  }

  // 搜索（在当前部门筛选结果内搜，搜索不能丢掉部门限定）
  if (searchQuery.value) {
    const hits = new Set(agentStore.searchAgents(searchQuery.value).map(a => a.id))
    list = list.filter(a => hits.has(a.id))
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

// 部门显示名映射（动态部门列表自带 label，未知部门回退原文）
const departmentLabels = computed(() => {
  const map: Record<string, string> = {}
  for (const d of agentStore.departments) {
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
  <BottomSheet
    :model-value="show"
    title="选择智能体角色"
    height="full"
    swipeable
    @update:model-value="emit('update:show', $event)"
    @close="close"
  >

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
          v-for="dept in agentStore.departments"
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
        <!-- §847：失败分支必须排在真空态之前，否则「拉取失败」被显示成「未找到匹配的角色」 -->
        <div v-else-if="agentErrorText && filteredAgents.length === 0" class="load-error" role="alert">
          <p>角色列表拉取失败：{{ agentErrorText }}</p>
          <button type="button" class="retry-btn" @click="retryLoad">重试</button>
        </div>
        <div v-else-if="filteredAgents.length === 0" class="empty">
          未找到匹配的角色
        </div>
        <template v-else>
          <div v-if="agentErrorText" class="load-error load-error--inline" role="alert">
            <span>角色列表可能不是最新的：{{ agentErrorText }}</span>
            <button type="button" class="retry-btn" @click="retryLoad">重试</button>
          </div>
          <!-- 如果搜索/筛选，扁平展示；否则按部门分组 -->
          <template v-if="searchQuery || selectedDepartment">
            <div
              v-for="agent in filteredAgents"
              :key="agent.id"
              :class="['agent-item', { active: agent.id === currentAgentId }]"
              @click="handleSelect(agent)"
            >
              <div class="agent-emoji">{{ agent.emoji || '👤' }}</div>
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
                <div class="agent-emoji">{{ agent.emoji || '👤' }}</div>
                <div class="agent-info">
                  <div class="agent-name">{{ agent.name }}</div>
                  <div class="agent-desc">{{ agent.description }}</div>
                </div>
              </div>
            </div>
          </template>
        </template>
      </div>

    <template #footer>
      <button class="clear-btn" type="button" @click="handleClear">清除角色</button>
    </template>
  </BottomSheet>
</template>

<style scoped>
.search-section {
  padding: 12px 20px;
  border-bottom: 1px solid var(--border);
}

.search-input {
  width: 100%;
  padding: 10px 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: var(--text-md);
  background: var(--bg-subtle);
  color: var(--text-primary);
}

.search-input::placeholder {
  color: var(--text-muted);
}

.department-filter {
  display: flex;
  gap: 8px;
  padding: 12px 20px;
  overflow-x: auto;
  border-bottom: 1px solid var(--border);
}

.dept-chip {
  /* iOS 标准按钮：矩形 + 小圆角，44px 触摸热区（过滤类按钮统一规范） */
  display: inline-flex;
  align-items: center;
  min-height: 44px;
  padding: 0 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  color: var(--text-primary);
  font-size: var(--text-base);
  white-space: nowrap;
  cursor: pointer;
  transition: all 0.2s;
}

.dept-chip.active {
  background: var(--brand-primary);
  color: var(--text-inverse);
  border-color: var(--brand-primary);
}

.agents-list {
  flex: 1;
  overflow-y: auto;
  padding: 16px 20px;
}

.load-error {
  text-align: center;
  padding: 20px 16px;
  color: var(--danger);
  font-size: var(--text-sm);
}
.load-error--inline {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  text-align: left;
  padding: 8px 12px;
  margin: 0 0 8px;
  border: 1px solid var(--danger);
  border-radius: var(--radius-sm);
  background: var(--danger-bg);
}
.retry-btn {
  margin-top: 12px;
  padding: 6px 14px;
  background: transparent;
  color: var(--danger);
  border: 1px solid var(--danger);
  border-radius: 8px;
  font-size: var(--text-sm);
  cursor: pointer;
}
.load-error--inline .retry-btn { margin-top: 0; flex: 0 0 auto; }

.loading,
.empty {
  text-align: center;
  padding: 32px 20px;
  color: var(--text-secondary);
}

.department-group {
  margin-bottom: 24px;
}

.group-header {
  font-size: var(--text-base);
  font-weight: 600;
  color: var(--text-secondary);
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
  background: var(--bg-subtle);
}

.agent-item.active {
  background: var(--brand-bg);
  border: 1px solid var(--brand-primary);
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
  font-size: var(--text-md);
  font-weight: 500;
  margin-bottom: 4px;
  color: var(--text-primary);
}

.agent-desc {
  font-size: var(--text-smd);
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.sheet-footer {
  padding: 16px 20px;
  border-top: 1px solid var(--border);
}

.clear-btn {
  width: 100%;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--bg-elevated);
  font-size: var(--text-md);
  color: var(--text-secondary);
  cursor: pointer;
}
</style>
