<!--
  TasksView — Codex-style compact dual-panel AI hub

  Layout:
  A. 运行中 — horizontal compact task cards (active across all instances)
  B. 会话   — vertical session list (recent AI conversations)
  C. 已完成 — collapsed expandable section
  D. Voice bar — fixed above bottom nav
-->
<template>
  <div class="ai-hub">
    <!-- Section A: Running Tasks -->
    <section class="section running-section">
      <div class="section-header">
        <h2>
          <span class="dot pulse" />运行中
          <span class="badge">{{ activeTasks.length }}</span>
        </h2>
        <button class="link-btn" @click="showCreateModal = true">+ 新任务</button>
      </div>

      <div v-if="loading" class="skeleton-row">
        <div v-for="i in 3" :key="i" class="skeleton-card" />
      </div>

      <div v-else-if="activeTasks.length > 0" class="task-scroll">
        <div
          v-for="task in activeTasks"
          :key="task.id"
          class="task-card compact"
          @click="viewTask(task.id)"
        >
          <div class="priority-bar" :class="task.priority" />
          <div class="task-body">
            <div class="task-title">{{ task.title }}</div>
            <div class="task-meta-row">
              <span v-if="task.instanceName" class="instance-tag">{{ task.instanceName }}</span>
              <span class="meta-muted">
                <span class="meta-icon">💬</span>{{ task.sessionCount || 0 }}
              </span>
              <span v-if="task.updatedAt" class="meta-muted time">{{ timeAgo(task.updatedAt) }}</span>
            </div>
          </div>
          <span class="chevron">›</span>
        </div>
      </div>

      <div v-else class="empty-inline">
        <span class="empty-text">暂无运行中的任务</span>
      </div>

      <!-- Blocked tasks (inline) -->
      <div v-if="blockedTasks.length > 0" class="blocked-strip">
        <div class="strip-header">
          <span class="dot blocked" />已阻塞
          <span class="badge warn">{{ blockedTasks.length }}</span>
        </div>
        <div
          v-for="task in blockedTasks"
          :key="task.id"
          class="task-card compact blocked-card"
          @click="viewTask(task.id)"
        >
          <div class="priority-bar" :class="task.priority" />
          <div class="task-body">
            <div class="task-title">{{ task.title }}</div>
            <div class="task-meta-row">
              <span v-if="task.instanceName" class="instance-tag">{{ task.instanceName }}</span>
              <span class="meta-muted">💬 {{ task.sessionCount || 0 }}</span>
            </div>
          </div>
          <span class="chevron">›</span>
        </div>
      </div>
    </section>

    <!-- Section B: AI Sessions -->
    <section class="section sessions-section">
      <div class="section-header">
        <h2>
          <span class="dot session" />会话
          <span class="badge">{{ sessions.length }}</span>
        </h2>
        <button class="link-btn" @click="router.push('/sessions')">全部</button>
      </div>

      <div v-if="sessionsLoading" class="skeleton-row">
        <div v-for="i in 3" :key="i" class="skeleton-card" />
      </div>

      <div v-else-if="sessions.length > 0" class="session-list">
        <div
          v-for="s in sessions"
          :key="s.id"
          class="session-item"
          @click="openSession(s)"
        >
          <span class="status-dot" :class="s.status" />
          <div class="session-body">
            <div class="session-title">{{ s.title || '未命名会话' }}</div>
            <div class="session-meta">
              <span v-if="s.instanceName" class="instance-tag sm">{{ s.instanceName }}</span>
              <span v-if="s.updatedAt" class="meta-muted time">{{ timeAgo(s.updatedAt) }}</span>
            </div>
          </div>
          <span class="chevron">›</span>
        </div>
      </div>

      <div v-else class="empty-inline">
        <span class="empty-text">暂无会话</span>
      </div>
    </section>

    <!-- Section C: Completed (collapsed) -->
    <section v-if="completedTasks.length > 0" class="section completed-section">
      <div class="section-header" @click="showCompleted = !showCompleted">
        <h2>
          <span class="dot done" />已完成
          <span class="badge muted">{{ completedTasks.length }}</span>
        </h2>
        <span class="expand-icon" :class="{ open: showCompleted }">›</span>
      </div>

      <div v-if="showCompleted" class="completed-list">
        <div
          v-for="task in completedTasks"
          :key="task.id"
          class="task-card compact completed-card"
          @click="viewTask(task.id)"
        >
          <div class="task-body">
            <div class="task-title done">{{ task.title }}</div>
            <span class="meta-muted time">{{ timeAgo(task.updatedAt) }}</span>
          </div>
        </div>
      </div>
    </section>

    <!-- Voice Input Bar -->
    <div class="voice-bar">
      <div class="voice-input-wrap">
        <textarea
          v-model="quickPrompt"
          class="voice-textarea"
          placeholder="快速提问..."
          rows="1"
          @keydown.enter.exact.prevent="sendQuickPrompt"
        />
        <button
          class="voice-btn"
          :class="{ recording: isRecording }"
          @click="toggleVoice"
          @touchstart.prevent="onVoiceTouchStart"
          @touchend.prevent="onVoiceTouchEnd"
        >
          {{ isRecording ? '⏹' : '🎙' }}
        </button>
        <button
          v-if="quickPrompt.trim()"
          class="send-btn"
          :disabled="sending"
          @click="sendQuickPrompt"
        >
          {{ sending ? '⋯' : '↑' }}
        </button>
      </div>
    </div>

    <!-- Create Task Modal -->
    <div
      v-if="showCreateModal"
      ref="modalRef"
      class="modal-overlay"
      @click.self="showCreateModal = false"
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
            <textarea v-model="newTask.description" placeholder="输入任务描述" rows="2" />
          </div>
          <div class="form-row">
            <div class="form-group half">
              <label>优先级</label>
              <select v-model="newTask.priority">
                <option value="high">高</option>
                <option value="medium">中</option>
                <option value="low">低</option>
              </select>
            </div>
            <div class="form-group half">
              <label>状态</label>
              <select v-model="newTask.status">
                <option value="active">进行中</option>
                <option value="blocked">已阻塞</option>
              </select>
            </div>
          </div>
          <div class="modal-actions">
            <button class="btn cancel" @click="showCreateModal = false">取消</button>
            <button class="btn primary" :disabled="!newTask.title" @click="handleCreate">创建</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Task } from '../../api/client'
import { agentsApi, type Agent } from '../../api/agents'
import wsClient from '../../api/websocket'
import { usePullDownClose } from '../../composables/usePullDownClose'
import { useVoiceRecording } from '../../composables/useVoiceRecording'
import { useToast } from '../../composables/useToast'

const router = useRouter()
const toast = useToast()

// ── State ──
const currentInstance = ref<any>(null)
type TaskWithInstance = Task & { instanceName?: string }
const tasks = ref<TaskWithInstance[]>([])
const sessions = ref<any[]>([])
const loading = ref(true)
const sessionsLoading = ref(true)
const showCreateModal = ref(false)
const showCompleted = ref(false)
const quickPrompt = ref('')
const modalRef = ref<HTMLElement | null>(null)
const modalSheetRef = ref<HTMLElement | null>(null)

// S1.2: workspace 内的 agent。v1.0 用单 Agent 模式——取列表第一个可用 agent。
const agents = ref<Agent[]>([])
const sending = ref(false)

const { isRecording, transcribing, toggleRecording } = useVoiceRecording({
  onTranscribed(text) {
    quickPrompt.value = quickPrompt.value
      ? `${quickPrompt.value.trimEnd()} ${text}`
      : text
  },
  onError(msg) {
    toast.error(msg)
  },
})

const newTask = ref({
  title: '',
  description: '',
  priority: 'medium',
  status: 'active',
})

// ── Computed ──
const activeTasks = computed(() =>
  tasks.value
    .filter((t) => t.status === 'active')
    .sort((a, b) => (b.updatedAt || '').localeCompare(a.updatedAt || ''))
)

const blockedTasks = computed(() =>
  tasks.value.filter((t) => t.status === 'blocked')
)

const completedTasks = computed(() =>
  tasks.value
    .filter((t) => t.status === 'completed')
    .sort((a, b) => (b.updatedAt || '').localeCompare(a.updatedAt || ''))
)

// ── Pull-down close ──
const { pullDownOffset, onSheetTouchStart, onSheetTouchMove, onSheetTouchEnd } =
  usePullDownClose({ threshold: 80, onClose: () => { showCreateModal.value = false } })

// ── Lifecycle ──
onMounted(() => {
  const instanceStr = localStorage.getItem('selected_instance')
  if (instanceStr) currentInstance.value = JSON.parse(instanceStr)
  loadTasks()
  loadSessions()
  loadAgents() // S1.2: 预载 workspace agent，供 sendQuickPrompt 单 Agent 模式使用
  wsClient.on('task_created', handleTaskUpdate)
  wsClient.on('task_updated', handleTaskUpdate)
  wsClient.on('session_attached', handleSessionAttached)
})

onUnmounted(() => {
  wsClient.off('task_created', handleTaskUpdate)
  wsClient.off('task_updated', handleTaskUpdate)
  wsClient.off('session_attached', handleSessionAttached)
})

// ── Data Loading ──
async function loadTasks() {
  loading.value = true
  try {
    if (!currentInstance.value) { tasks.value = []; return }
    const instanceTasks = await api.getTasks(currentInstance.value.id, {
      workstreamId: currentInstance.value.id,
    })
    tasks.value = (instanceTasks || []).map((t: any) => ({
      ...t,
      instanceName: currentInstance.value?.displayName || currentInstance.value?.name || '',
    }))
  } catch (e) {
    console.error('Failed to load tasks:', e)
    tasks.value = []
  } finally {
    loading.value = false
  }
}

async function loadSessions() {
  sessionsLoading.value = true
  try {
    const data = await api.getAllSessions(undefined, 10, 0)
    sessions.value = (data.sessions || []).map((s: any) => ({
      id: s.id || s.ID || '',
      title: s.title || s.Title || '',
      status: s.status || s.Status || 'idle',
      instanceId: s.instanceId || s.InstanceId || '',
      instanceName: s.instanceName || s.InstanceName || '',
      updatedAt: s.updatedAt || s.UpdatedAt || '',
    }))
  } catch (e) {
    console.error('Failed to load sessions:', e)
    sessions.value = []
  } finally {
    sessionsLoading.value = false
  }
}

/** S1.2: 预载 workspace agent。失败静默（agent bridge 未配置时发任务会单独报错）。 */
async function loadAgents() {
  try {
    agents.value = await agentsApi.list()
  } catch (e) {
    console.warn('[tasks] agent bridge 未配置或不可用:', e)
    agents.value = []
  }
}

// ── Handlers ──
function handleTaskUpdate(task: TaskWithInstance) {
  const idx = tasks.value.findIndex((t) => t.id === task.id)
  if (idx >= 0) tasks.value[idx] = { ...tasks.value[idx], ...task }
  else tasks.value.unshift({ ...task, instanceName: currentInstance.value?.displayName || '' })
}

function handleSessionAttached(link: any) {
  const task = tasks.value.find((t) => t.id === link.taskId)
  if (task) task.sessionCount = (task.sessionCount || 0) + 1
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
    showCreateModal.value = false
    loadTasks()
  } catch (e) {
    console.error('Failed to create task:', e)
  }
}

function viewTask(taskId: string) {
  router.push({ path: `/tasks/${taskId}` })
}

function openSession(s: any) {
  const instId = s.instanceId || currentInstance.value?.id || ''
  router.push({
    path: `/sessions/${s.id}`,
    query: { instance_id: instId, title: s.title },
  })
}

// ── Voice (via composable) ──
const toggleVoice = toggleRecording

function onVoiceTouchStart() { /* long-press future: auto-send on stop */ }
function onVoiceTouchEnd() { /* noop for now */ }

/**
 * S1.2: 真正的 Agent 任务闭环。
 *   1. 先用 prompt 标题建任务壳（createTask）
 *   2. 取 workspace 默认 agent（单 Agent 模式：第一个 online 的）
 *   3. agentsApi.send(agent, { task_id }) → 后端创建 session 并自动 attach
 *   4. 跳到会话详情，prompt 已在 session 里
 *
 * 失败处理：无 agent → toast 提示去绑定实例；send 失败 → toast 报错，任务壳保留。
 */
async function sendQuickPrompt() {
  const text = quickPrompt.value.trim()
  if (!text || sending.value) return
  sending.value = true
  try {
    // 1) 选 agent：优先 online，否则取第一个（可能 busy/offline 也能发）
    const agent = agents.value.find((a) => a.status === 'online') || agents.value[0]
    if (!agent) {
      toast.error('暂无可用 Agent，请先在实例页绑定')
      return
    }

    // 2) 建任务壳（标题取 prompt 首行/前 40 字）
    const title = text.split('\n')[0].slice(0, 40) || '新任务'
    const task = await api.createTask({ title, description: text, status: 'active', priority: 'medium' })

    // 3) 发给 agent，后端创建 session + 自动 attach task
    const res = await agentsApi.send(agent.id, {
      prompt: text,
      task_id: task.id,
    })

    // 4) 跳会话详情（session 已建好，prompt 已注入）
    router.push({
      path: `/sessions/${res.session_id}`,
      query: {
        instance_id: res.instance_id,
        title,
        agent_id: agent.id,
        task_id: res.task_id || task.id,
      },
    })
    quickPrompt.value = ''
  } catch (e: any) {
    console.error('[sendQuickPrompt] agent send failed:', e)
    toast.error(e?.message || '发送失败，请重试')
  } finally {
    sending.value = false
  }
}

// ── Utils ──
function timeAgo(dateStr?: string): string {
  if (!dateStr) return ''
  const diff = Date.now() - new Date(dateStr).getTime()
  const mins = Math.floor(diff / 60000)
  if (mins < 1) return '刚刚'
  if (mins < 60) return `${mins}分钟前`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}小时前`
  const days = Math.floor(hrs / 24)
  return `${days}天前`
}
</script>

<style scoped>
.ai-hub {
  min-height: 100vh;
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-bottom: 110px; /* voice-bar + bottom-nav */
}

/* ── Section ── */
.section {
  padding: 10px var(--space-3) 6px;
}
.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
  padding: 0 2px;
}
.section-header h2 {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
  margin: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  letter-spacing: 0.3px;
}
.dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  display: inline-block;
  flex-shrink: 0;
}
.dot.pulse {
  background: var(--success);
  animation: pulse 2s infinite;
}
.dot.blocked { background: var(--warning); }
.dot.session { background: var(--brand-primary); }
.dot.done { background: var(--text-muted); }

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}

.badge {
  font-size: 10px;
  font-weight: 700;
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--bg-subtle);
  color: var(--text-secondary);
  line-height: 16px;
}
.badge.warn {
  background: rgba(245, 158, 11, 0.14);
  color: var(--warning);
}
.badge.muted {
  background: var(--bg-subtle);
  color: var(--text-muted);
}

.link-btn {
  font-size: 12px;
  font-weight: 600;
  color: var(--brand-primary);
  background: none;
  border: none;
  cursor: pointer;
  padding: 4px 8px;
}

/* ── Task Cards (Codex compact) ── */
.task-scroll {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.task-card.compact {
  display: flex;
  align-items: center;
  gap: 10px;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px 10px;
  cursor: pointer;
  transition: background 120ms;
}
.task-card.compact:active {
  background: var(--bg-subtle);
}
.priority-bar {
  width: 2px;
  height: 28px;
  border-radius: 1px;
  flex-shrink: 0;
}
.priority-bar.high { background: var(--error, #ef4444); }
.priority-bar.medium { background: var(--warning); }
.priority-bar.low { background: var(--success); }

.task-body {
  flex: 1;
  min-width: 0;
}
.task-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 18px;
}
.task-title.done {
  color: var(--text-muted);
  text-decoration: line-through;
}
.task-meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 2px;
}
.instance-tag {
  font-size: 10px;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 4px;
  background: rgba(102, 126, 234, 0.1);
  color: var(--brand-primary);
  line-height: 14px;
  max-width: 80px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.instance-tag.sm {
  font-size: 9px;
  padding: 0px 4px;
}
.meta-muted {
  font-size: 11px;
  color: var(--text-muted);
  display: inline-flex;
  align-items: center;
  gap: 2px;
}
.meta-muted .meta-icon {
  font-size: 10px;
}
.meta-muted.time {
  font-size: 10px;
}
.chevron {
  font-size: 16px;
  color: var(--text-muted);
  flex-shrink: 0;
  opacity: 0.5;
}

.blocked-strip {
  margin-top: 8px;
}
.strip-header {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
  margin-bottom: 6px;
  padding: 0 2px;
}
.blocked-card {
  border-color: rgba(245, 158, 11, 0.2);
}

/* ── Session List ── */
.session-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.session-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: 8px;
  cursor: pointer;
  transition: background 120ms;
}
.session-item:active {
  background: var(--bg-subtle);
}
.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--text-muted);
}
.status-dot.active,
.status-dot.streaming {
  background: var(--success);
  animation: pulse 2s infinite;
}
.status-dot.idle { background: var(--brand-primary); }
.status-dot.error { background: var(--error, #ef4444); }

.session-body {
  flex: 1;
  min-width: 0;
}
.session-title {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 18px;
}
.session-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 2px;
}

/* ── Empty State ── */
.empty-inline {
  padding: 16px 10px;
  text-align: center;
}
.empty-text {
  font-size: 12px;
  color: var(--text-muted);
}

/* ── Skeleton ── */
.skeleton-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.skeleton-card {
  height: 44px;
  background: var(--bg-subtle);
  border-radius: 8px;
  animation: shimmer 1.5s infinite;
}
@keyframes shimmer {
  0% { opacity: 0.6; }
  50% { opacity: 1; }
  100% { opacity: 0.6; }
}

/* ── Completed Section ── */
.completed-section .section-header {
  cursor: pointer;
}
.expand-icon {
  font-size: 16px;
  color: var(--text-muted);
  transition: transform 200ms;
}
.expand-icon.open {
  transform: rotate(90deg);
}
.completed-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.completed-card {
  opacity: 0.7;
  padding: 6px 10px;
}
.completed-card .task-body {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

/* ── Voice Bar ── */
.voice-bar {
  position: fixed;
  bottom: var(--bottomnav-height);
  left: 0;
  right: 0;
  padding: 6px 12px;
  padding-bottom: calc(6px + env(safe-area-inset-bottom, 0));
  background: var(--bg-card);
  border-top: 1px solid var(--border);
  z-index: 15;
}
.voice-input-wrap {
  display: flex;
  align-items: center;
  gap: 8px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: 20px;
  padding: 4px 6px 4px 14px;
}
.voice-textarea {
  flex: 1;
  border: none;
  background: transparent;
  font-size: 13px;
  color: var(--text-primary);
  resize: none;
  outline: none;
  font-family: inherit;
  line-height: 20px;
  max-height: 60px;
  padding: 4px 0;
}
.voice-textarea::placeholder {
  color: var(--text-muted);
}
.voice-btn,
.send-btn {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  border: none;
  font-size: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex-shrink: 0;
  transition: transform 100ms;
}
.voice-btn {
  background: var(--bg-elevated);
  color: var(--text-secondary);
}
.voice-btn.recording {
  background: var(--error, #ef4444);
  color: #fff;
  animation: pulse 1s infinite;
}
.send-btn {
  background: var(--brand-primary);
  color: #fff;
}
.send-btn:active,
.voice-btn:active {
  transform: scale(0.9);
}

/* ── Modal ── */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: flex-end;
  z-index: 1000;
  animation: fadeIn 150ms ease;
}
@keyframes fadeIn { from { opacity: 0; } to { opacity: 1; } }
.modal-sheet {
  background: var(--bg-elevated);
  border-radius: 16px 16px 0 0;
  width: 100%;
  max-height: 85vh;
  display: flex;
  flex-direction: column;
  animation: slideUp 200ms cubic-bezier(0.2, 0.8, 0.2, 1);
  touch-action: none;
}
@keyframes slideUp { from { transform: translateY(100%); } to { transform: translateY(0); } }
.modal-handle {
  width: 36px;
  height: 4px;
  border-radius: 2px;
  background: var(--border-strong);
  margin: 8px auto 4px;
}
.modal-body {
  padding: 8px 20px 20px;
  overflow-y: auto;
}
.modal-body h2 {
  font-size: 16px;
  font-weight: 700;
  margin: 4px 0 16px;
  color: var(--text-primary);
}
.form-group {
  margin-bottom: 12px;
}
.form-group label {
  display: block;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
  margin-bottom: 4px;
}
.form-group input,
.form-group textarea,
.form-group select {
  width: 100%;
  padding: 10px;
  font-size: 14px;
  background: var(--bg-subtle);
  color: var(--text-primary);
  border: 1px solid transparent;
  border-radius: 8px;
  box-sizing: border-box;
  font-family: inherit;
}
.form-group input:focus,
.form-group textarea:focus,
.form-group select:focus {
  border-color: var(--brand-primary);
  outline: none;
}
.form-row {
  display: flex;
  gap: 12px;
}
.form-group.half {
  flex: 1;
}
.modal-actions {
  display: flex;
  gap: 10px;
  margin-top: 16px;
}
.btn {
  flex: 1;
  padding: 10px;
  font-size: 14px;
  font-weight: 600;
  border: none;
  border-radius: 10px;
  cursor: pointer;
}
.btn.cancel {
  background: var(--bg-subtle);
  color: var(--text-primary);
}
.btn.primary {
  background: var(--brand-primary);
  color: #fff;
}
.btn.primary:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
</style>
