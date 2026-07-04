<!--
  TasksView — Phase 4: 主题任务抽象 + M3 强化 + 下滑关闭

  变化点（相对 Phase 3）：
  1. 顶部集成 ThemeTabs（5 主题 chip + 未完成 badge）
  2. 状态分组 chip 升级为 M3 AssistChip 风格（active/blocked/completed 三色）
  3. 任务卡片升级为 M3 ElevatedCard（圆角 12、shadow-md、press scale）
  4. "+" 按钮改为右下 FAB（圆形 primary-container）
  5. 创建 modal 加 ESC + 下滑关闭（usePullDownClose）
-->
<template>
  <div class="tasks-view">
    <!-- 主题切换器（M3 SegmentedButton） -->
    <ThemeTabs v-model="activeTheme" :tabs="themeTabs" />

    <!-- 加载状态 -->
    <div v-if="loading" class="loading-state">
      <div class="spinner"></div>
      <p>加载任务...</p>
    </div>

    <!-- 任务列表（按状态分组） -->
    <div v-else-if="groupedTasks.length > 0" class="tasks-container">
      <div v-for="group in groupedTasks" :key="group.name" class="task-group">
        <div class="group-header">
          <h2>{{ group.name }}</h2>
          <span class="task-count">{{ group.tasks.length }}</span>
        </div>

        <div class="task-list">
          <div
            v-for="task in group.tasks"
            :key="task.id"
            class="task-card"
            @click="viewTask(task.id)"
          >
            <div class="task-priority" :class="task.priority"></div>
            <div class="task-content">
              <h3>{{ task.title }}</h3>
              <p v-if="task.description" class="task-desc">{{ task.description }}</p>
              <div class="task-meta">
                <span class="meta-item">
                  <span class="meta-icon">💬</span>
                  {{ task.sessionCount || 0 }} 会话
                </span>
                <span class="meta-item" :class="['status-chip', `status-${task.status}`]">
                  {{ statusText(task.status) }}
                </span>
              </div>
            </div>
            <div class="task-arrow">›</div>
          </div>
        </div>
      </div>
    </div>

    <!-- 空状态 -->
    <div v-else class="empty-state">
      <div class="empty-icon">📝</div>
      <p>暂无任务</p>
      <button class="create-first-btn" @click="showCreateModal = true">
        创建第一个任务
      </button>
    </div>

    <!-- M3 FAB：右下圆形创建按钮 -->
    <button class="fab" aria-label="创建任务" @click="showCreateModal = true">
      <span class="fab-icon">+</span>
    </button>

    <!-- 创建任务 modal（M3 + 下滑关闭 + ESC） -->
    <div
      v-if="showCreateModal"
      ref="modalRef"
      class="modal-overlay"
      @click.self="closeModal"
    >
      <div
        ref="modalSheetRef"
        class="modal-sheet"
        :style="{ transform: `translateY(${pullDownOffset}px)` }"
        @touchstart="onSheetTouchStart"
        @touchmove="onSheetTouchMove"
        @touchend="onSheetTouchEnd"
      >
        <div class="modal-handle" />
        <div class="modal-body">
          <h2>创建任务</h2>

          <div class="form-group">
            <label>标题 *</label>
            <input v-model="newTask.title" type="text" placeholder="输入任务标题" />
          </div>

          <div class="form-group">
            <label>描述</label>
            <textarea v-model="newTask.description" placeholder="输入任务描述" rows="3" />
          </div>

          <div class="form-group">
            <label>优先级</label>
            <select v-model="newTask.priority">
              <option value="high">高</option>
              <option value="medium">中</option>
              <option value="low">低</option>
            </select>
          </div>

          <div class="form-group">
            <label>状态</label>
            <select v-model="newTask.status">
              <option value="active">进行中</option>
              <option value="blocked">已阻塞</option>
              <option value="completed">已完成</option>
            </select>
          </div>

          <div class="modal-actions">
            <button class="cancel-btn" @click="closeModal">取消</button>
            <button class="create-btn" :disabled="!newTask.title" @click="handleCreate">
              创建
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Task } from '../../api/client'
import wsClient from '../../api/websocket'
import ThemeTabs, { type ThemeTab } from '../../components/interactive/ThemeTabs.vue'
import { usePullDownClose } from '../../composables/usePullDownClose'

const router = useRouter()

const currentInstance = ref<any>(null)
const tasks = ref<Task[]>([])
const loading = ref(true)
const showCreateModal = ref(false)
const activeTheme = ref<string>('all')

const modalRef = ref<HTMLElement | null>(null)
const modalSheetRef = ref<HTMLElement | null>(null)

const newTask = ref({
  title: '',
  description: '',
  priority: 'medium',
  status: 'active',
})

// Phase 4: 主题列表（与 BottomNav 5 模块对齐）
const themeTabs = computed<ThemeTab[]>(() => {
  const open = (s: string) => tasks.value.filter((t) => t.status === s).length
  const aiCount = tasks.value.filter(
    (t) => t.workstreamId === currentInstance.value?.id && t.status !== 'completed',
  ).length
  const noteCount = tasks.value.filter(
    (t) => t.source === 'local' && t.status !== 'completed',
  ).length
  // meeting/email 暂用 source 标识 fallback 到 0
  const meetingCount = tasks.value.filter(
    (t) => (t as any).category === 'meeting' && t.status !== 'completed',
  ).length
  const emailCount = tasks.value.filter(
    (t) => (t as any).category === 'email' && t.status !== 'completed',
  ).length

  return [
    { id: 'all', label: '全部', icon: '✦', count: open('active') + open('blocked') },
    { id: 'ai', label: 'AI', icon: '🤖', count: aiCount },
    { id: 'notes', label: '笔记', icon: '📝', count: noteCount },
    { id: 'meetings', label: '会议', icon: '🎙️', count: meetingCount },
    { id: 'email', label: '邮件', icon: '✉️', count: emailCount },
  ]
})

// 过滤后的 task 列表
const filteredTasks = computed(() => {
  if (activeTheme.value === 'all') return tasks.value
  const map: Record<string, (t: Task) => boolean> = {
    ai: (t) => t.workstreamId === currentInstance.value?.id,
    notes: (t) => t.source === 'local',
    meetings: (t) => (t as any).category === 'meeting',
    email: (t) => (t as any).category === 'email',
  }
  const pred = map[activeTheme.value]
  return pred ? tasks.value.filter(pred) : tasks.value
})

// 按状态分组（仅展示 open 状态；completed 折叠）
const groupedTasks = computed(() => {
  const groups = [
    { name: '进行中', tasks: filteredTasks.value.filter((t) => t.status === 'active') },
    { name: '已阻塞', tasks: filteredTasks.value.filter((t) => t.status === 'blocked') },
    { name: '已完成', tasks: filteredTasks.value.filter((t) => t.status === 'completed') },
  ]
  return groups.filter((g) => g.tasks.length > 0)
})

// Phase 4.3: 下滑关闭 modal
const { pullDownOffset, onSheetTouchStart, onSheetTouchMove, onSheetTouchEnd } =
  usePullDownClose({
    threshold: 80,
    onClose: () => closeModal(),
  })

function closeModal() {
  showCreateModal.value = false
}

// Phase 4.3: ESC 关闭 modal
function onKeyDown(e: KeyboardEvent) {
  if (e.key === 'Escape' && showCreateModal.value) closeModal()
}

onMounted(() => {
  const instanceStr = localStorage.getItem('selected_instance')
  if (instanceStr) currentInstance.value = JSON.parse(instanceStr)

  loadTasks()

  wsClient.on('task_created', handleTaskUpdate)
  wsClient.on('task_updated', handleTaskUpdate)
  wsClient.on('session_attached', handleSessionAttached)
  window.addEventListener('keydown', onKeyDown)
})

onUnmounted(() => {
  wsClient.off('task_created', handleTaskUpdate)
  wsClient.off('task_updated', handleTaskUpdate)
  wsClient.off('session_attached', handleSessionAttached)
  window.removeEventListener('keydown', onKeyDown)
})

watch(activeTheme, () => {
  // 切换主题时如想刷后端可在此触发 loadTasks(opts)
})

async function loadTasks() {
  loading.value = true
  try {
    if (!currentInstance.value) {
      tasks.value = []
      return
    }
    // Phase 4: 拉三源任务（acc + opencode + local）
    const instanceTasks = await api.getTasks(currentInstance.value.id, {
      workstreamId: currentInstance.value.id,
    })
    tasks.value = instanceTasks
  } catch (error) {
    console.error('Failed to load tasks:', error)
    tasks.value = []
  } finally {
    loading.value = false
  }
}

async function handleCreate() {
  if (!newTask.value.title) return
  try {
    const task: Task = {
      id: `task-${Date.now()}`,
      title: newTask.value.title,
      description: newTask.value.description,
      status: newTask.value.status as any,
      priority: newTask.value.priority as any,
      workstreamId: currentInstance.value?.id,
      source: 'local',
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
      sessionCount: 0,
    }
    await api.createTask(task)
    newTask.value = { title: '', description: '', priority: 'medium', status: 'active' }
    closeModal()
    loadTasks()
  } catch (error) {
    console.error('Failed to create task:', error)
  }
}

function handleTaskUpdate(task: Task) {
  const index = tasks.value.findIndex((t) => t.id === task.id)
  if (index >= 0) tasks.value[index] = task
  else tasks.value.unshift(task)
}

function handleSessionAttached(link: any) {
  const task = tasks.value.find((t) => t.id === link.taskId)
  if (task) task.sessionCount = (task.sessionCount || 0) + 1
}

function viewTask(taskId: string) {
  const instanceId = currentInstance.value?.id || ''
  router.push({
    path: `/sessions/${taskId}`,
    query: { instance_id: instanceId, title: '' },
  })
}

function statusText(status: string): string {
  const map: Record<string, string> = {
    active: '进行中',
    blocked: '已阻塞',
    completed: '已完成',
  }
  return map[status] || status
}
</script>

<style scoped>
.tasks-view {
  min-height: 100vh;
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
  padding-bottom: 96px; /* FAB + bottom-nav 空间 */
}

.loading-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.spinner {
  width: 40px;
  height: 40px;
  border: 4px solid var(--bg-subtle);
  border-top: 4px solid var(--brand-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
  margin-bottom: 16px;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.tasks-container {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-3);
}

.task-group {
  margin-bottom: var(--space-4);
}

.group-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
  padding: 0 4px;
}

.group-header h2 {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-secondary);
  margin: 0;
  text-transform: uppercase;
  letter-spacing: 0.4px;
}

.task-count {
  font-size: 11px;
  padding: 2px 8px;
  background: var(--bg-subtle);
  color: var(--text-secondary);
  border-radius: 999px;
  font-weight: 600;
}

/* M3 ElevatedCard */
.task-card {
  background: var(--bg-elevated);
  border-radius: 12px;
  padding: 14px;
  margin-bottom: 10px;
  display: flex;
  align-items: center;
  gap: 12px;
  cursor: pointer;
  box-shadow: var(--shadow-sm);
  transition:
    transform 120ms ease,
    box-shadow 180ms ease;
}

.task-card:hover {
  box-shadow: var(--shadow-md);
}

.task-card:active {
  transform: scale(0.98);
  box-shadow: var(--shadow-sm);
}

.task-priority {
  width: 3px;
  height: 40px;
  border-radius: 2px;
  flex-shrink: 0;
}

.task-priority.high {
  background: var(--error, #ef4444);
}
.task-priority.medium {
  background: var(--warning);
}
.task-priority.low {
  background: var(--success);
}

.task-content {
  flex: 1;
  min-width: 0;
}

.task-content h3 {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0 0 4px 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-desc {
  font-size: 12px;
  color: var(--text-secondary);
  margin: 0 0 6px 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-meta {
  display: flex;
  gap: 8px;
  font-size: 11px;
  align-items: center;
}

.meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--text-muted);
}

/* M3 AssistChip：状态 chip 升级 */
.meta-item.status-chip {
  padding: 2px 8px;
  border-radius: 999px;
  font-weight: 600;
  font-size: 11px;
  line-height: 16px;
}

.meta-item.status-chip.status-active {
  background: rgba(16, 185, 129, 0.12);
  color: var(--success);
}

.meta-item.status-chip.status-blocked {
  background: rgba(245, 158, 11, 0.14);
  color: var(--warning);
}

.meta-item.status-chip.status-completed {
  background: rgba(102, 126, 234, 0.14);
  color: var(--brand-primary);
}

.task-arrow {
  font-size: 18px;
  color: var(--text-muted);
}

.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px;
}

.empty-icon {
  font-size: 64px;
  margin-bottom: 16px;
}

.create-first-btn {
  padding: 12px 24px;
  font-size: 14px;
  font-weight: 600;
  color: white;
  background: var(--brand-gradient);
  border: none;
  border-radius: 999px;
  cursor: pointer;
  margin-top: 16px;
}

/* M3 FloatingActionButton */
.fab {
  position: fixed;
  right: 20px;
  bottom: calc(56px + env(safe-area-inset-bottom, 0) + 16px);
  width: 56px;
  height: 56px;
  border-radius: 16px;
  border: none;
  background: var(--brand-primary);
  color: #fff;
  font-size: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  box-shadow: var(--shadow-lg);
  transition:
    transform 120ms ease,
    box-shadow 180ms ease;
  z-index: 50;
}

.fab:hover {
  box-shadow: 0 12px 20px -4px rgba(102, 126, 234, 0.4);
}

.fab:active {
  transform: scale(0.94);
}

.fab-icon {
  display: block;
  line-height: 1;
  margin-top: -2px;
}

/* ============ Modal (下滑关闭 + M3) ============ */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: flex-end;
  justify-content: center;
  z-index: 1000;
  animation: fadeIn 180ms ease;
}

@keyframes fadeIn {
  from {
    opacity: 0;
  }
  to {
    opacity: 1;
  }
}

.modal-sheet {
  background: var(--bg-elevated);
  border-radius: 24px 24px 0 0;
  width: 100%;
  max-width: 600px;
  max-height: 85vh;
  display: flex;
  flex-direction: column;
  animation: slideUp 240ms cubic-bezier(0.2, 0.8, 0.2, 1);
  touch-action: none;
  will-change: transform;
}

@keyframes slideUp {
  from {
    transform: translateY(100%);
  }
  to {
    transform: translateY(0);
  }
}

.modal-handle {
  width: 36px;
  height: 4px;
  border-radius: 2px;
  background: var(--border-strong);
  margin: 10px auto 6px;
  flex-shrink: 0;
}

.modal-body {
  padding: 12px 24px 24px;
  overflow-y: auto;
}

.modal-body h2 {
  font-size: 20px;
  font-weight: 600;
  margin: 8px 0 20px;
  color: var(--text-primary);
}

.form-group {
  margin-bottom: 16px;
}

.form-group label {
  display: block;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-secondary);
  margin-bottom: 6px;
}

.form-group input,
.form-group textarea,
.form-group select {
  width: 100%;
  padding: 12px;
  font-size: 14px;
  background: var(--bg-subtle);
  color: var(--text-primary);
  border: 1px solid transparent;
  border-radius: 8px;
  box-sizing: border-box;
  font-family: inherit;
  transition: border-color 180ms ease;
}

.form-group input:focus,
.form-group textarea:focus,
.form-group select:focus {
  border-color: var(--brand-primary);
  background: var(--bg-card);
}

.modal-actions {
  display: flex;
  gap: 12px;
  margin-top: 24px;
}

.cancel-btn,
.create-btn {
  flex: 1;
  padding: 12px;
  font-size: 14px;
  font-weight: 600;
  border: none;
  border-radius: 999px;
  cursor: pointer;
  transition: background 180ms ease;
}

.cancel-btn {
  background: var(--bg-subtle);
  color: var(--text-primary);
}

.cancel-btn:hover {
  background: var(--border);
}

.create-btn {
  background: var(--brand-primary);
  color: #fff;
}

.create-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
</style>