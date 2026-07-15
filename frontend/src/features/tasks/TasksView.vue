<!--
  TasksView — Codex-style compact dual-panel AI hub

  Layout:
  A. 运行中 — horizontal compact task cards (active across all instances)
  B. 会话   — vertical session list (recent AI conversations)
  C. 已完成 — collapsed expandable section
  D. Voice bar — fixed above bottom nav
-->
<template>
  <PullToRefresh :on-refresh="handleRefresh" class="ai-hub-scroll">
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
          @click="onTaskClick(task.id)"
          @touchstart="onTaskTouchStart(task, $event)"
          @touchmove="onTaskTouchMove"
          @touchend="onTaskTouchEnd"
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
        <EmptyState
          icon="📋"
          title="暂无运行中的任务"
          hint="点击「+ 新任务」创建，或长按任务卡片操作"
          size="sm"
          variant="inline"
        />
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
          @click="onTaskClick(task.id)"
          @touchstart="onTaskTouchStart(task, $event)"
          @touchmove="onTaskTouchMove"
          @touchend="onTaskTouchEnd"
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
        <EmptyState
          icon="💬"
          title="暂无会话"
          hint="开始新对话后会显示在这里"
          size="sm"
          variant="inline"
        />
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
          @click="onTaskClick(task.id)"
          @touchstart="onTaskTouchStart(task, $event)"
          @touchmove="onTaskTouchMove"
          @touchend="onTaskTouchEnd"
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
      <div v-if="isRecording" class="recording-indicator">
        <span class="rec-dot" />
        <span class="rec-bars"><i /><i /><i /><i /><i /></span>
        <span class="rec-label">录音中…</span>
      </div>
      <div v-else-if="isTranscribing" class="recording-indicator transcribing">
        <span class="rec-label">转写中…</span>
      </div>
      <div v-if="sttError" class="stt-error">{{ sttError }}</div>
      <div class="voice-input-wrap">
        <textarea
          v-model="quickPrompt"
          class="voice-textarea"
          :placeholder="isRecording ? '🎙 录音中...' : isTranscribing ? '转写中...' : '快速提问...'"
          rows="1"
          @keydown.enter.exact.prevent="sendQuickPrompt"
          :disabled="isRecording || isTranscribing"
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
          @click="sendQuickPrompt"
        >
          ↑
        </button>
      </div>
    </div>

    <!-- Task Context Menu (long-press) -->
    <div v-if="showContextMenu && contextTask" class="modal-overlay" @click.self="closeContextMenu">
      <div class="modal-sheet context-sheet" @click.stop>
        <div class="modal-handle" />
        <div class="modal-body">
          <h2 class="context-title">{{ contextTask.title }}</h2>
          <div class="context-actions">
            <button class="ctx-btn" @click="ctxViewDetail">查看详情</button>
            <button
              v-if="contextTask.status !== 'active'"
              class="ctx-btn"
              @click="ctxUpdateStatus('active')"
            >▶ 恢复</button>
            <button
              v-if="contextTask.status === 'active'"
              class="ctx-btn"
              @click="ctxUpdateStatus('blocked')"
            >⏸ 暂停</button>
            <button
              v-if="contextTask.status !== 'completed'"
              class="ctx-btn"
              @click="ctxUpdateStatus('completed')"
            >✅ 完成</button>
            <button class="ctx-btn danger" @click="ctxDelete">🗑 删除</button>
          </div>
        </div>
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
  </PullToRefresh>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Task } from '../../api/client'
import wsClient from '../../api/websocket'
import { usePullDownClose } from '../../composables/usePullDownClose'
import { useVoiceInput } from '../../composables/useVoiceInput'
import { EmptyState, PullToRefresh } from '../../components'

const router = useRouter()
const { isRecording, isTranscribing, sttError, startRecording, stopRecording } = useVoiceInput()

// ── Context menu (long-press) ──
const showContextMenu = ref(false)
const contextTask = ref<Task | null>(null)
let longPressTimer: ReturnType<typeof setTimeout> | null = null
let pressStart = { x: 0, y: 0 }
let suppressClick = false

// ── State ──
const currentInstance = ref<any>(null)
const tasks = ref<Task[]>([])
const sessions = ref<any[]>([])
const loading = ref(true)
const sessionsLoading = ref(true)
const showCreateModal = ref(false)
const showCompleted = ref(false)
const quickPrompt = ref('')
const modalRef = ref<HTMLElement | null>(null)
const modalSheetRef = ref<HTMLElement | null>(null)

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
async function handleRefresh() {
  await Promise.all([loadTasks(), loadSessions()])
}

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

// ── Handlers ──
function handleTaskUpdate(task: Task) {
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

function onTaskClick(taskId: string) {
  if (suppressClick) return
  viewTask(taskId)
}

function onTaskTouchStart(task: Task, e: TouchEvent) {
  const t = e.touches[0]
  if (!t) return
  pressStart = { x: t.clientX, y: t.clientY }
  longPressTimer = setTimeout(() => {
    contextTask.value = task
    showContextMenu.value = true
    suppressClick = true
    if (typeof navigator !== 'undefined' && navigator.vibrate) navigator.vibrate(12)
    setTimeout(() => { suppressClick = false }, 400)
  }, 500)
}

function onTaskTouchMove(e: TouchEvent) {
  if (!longPressTimer) return
  const t = e.touches[0]
  if (!t) return
  if (Math.abs(t.clientX - pressStart.x) > 8 || Math.abs(t.clientY - pressStart.y) > 8) {
    clearTimeout(longPressTimer)
    longPressTimer = null
  }
}

function onTaskTouchEnd() {
  if (longPressTimer) {
    clearTimeout(longPressTimer)
    longPressTimer = null
  }
}

function closeContextMenu() {
  showContextMenu.value = false
  contextTask.value = null
}

function ctxViewDetail() {
  if (contextTask.value) viewTask(contextTask.value.id)
  closeContextMenu()
}

async function ctxUpdateStatus(status: string) {
  if (!contextTask.value) return
  const task = contextTask.value
  const old = task.status
  task.status = status
  try {
    await api.updateTask(task.id, { status })
    closeContextMenu()
  } catch (e) {
    task.status = old
    console.error('Failed to update task:', e)
    alert('操作失败，请重试')
  }
}

async function ctxDelete() {
  if (!contextTask.value || !confirm(`确定删除「${contextTask.value.title}」？`)) return
  const id = contextTask.value.id
  try {
    await api.deleteTask(id)
    tasks.value = tasks.value.filter((t) => t.id !== id)
    closeContextMenu()
  } catch (e) {
    console.error('Failed to delete task:', e)
    alert('删除失败，请重试')
  }
}

function openSession(s: any) {
  const instId = s.instanceId || currentInstance.value?.id || ''
  router.push({
    path: `/sessions/${s.id}`,
    query: { instance_id: instId, title: s.title },
  })
}

// ── Voice ──
async function toggleVoice() {
  if (isRecording.value) {
    const text = await stopRecording()
    if (text) quickPrompt.value = text
  } else {
    await startRecording()
  }
}

function onVoiceTouchStart() { /* long-press future */ }
function onVoiceTouchEnd() { /* noop */ }

function sendQuickPrompt() {
  const text = quickPrompt.value.trim()
  if (!text) return
  // Find the most recent active session, or navigate to sessions
  const activeSession = sessions.value.find((s) => s.status === 'active') || sessions.value[0]
  if (activeSession) {
    router.push({
      path: `/sessions/${activeSession.id}`,
      query: { instance_id: activeSession.instanceId, title: activeSession.title, prompt: text },
    })
  } else {
    router.push('/sessions')
  }
  quickPrompt.value = ''
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
.ai-hub-scroll {
  height: 100%;
  min-height: 0;
}
.ai-hub {
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
  background: var(--warning-bg);
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
.priority-bar.high { background: var(--danger); }
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
  background: var(--brand-bg);
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
  border-color: color-mix(in srgb, var(--warning) 20%, var(--border));
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
.status-dot.error { background: var(--danger); }

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
  bottom: calc(var(--bottomnav-height) - var(--bottom-chrome-hide, 0px));
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
  background: var(--danger);
  color: var(--text-inverse);
  animation: pulse 1s infinite;
}
.send-btn {
  background: var(--brand-primary);
  color: var(--text-inverse);
}
.send-btn:active,
.voice-btn:active {
  transform: scale(0.9);
}

/* ── Recording indicator ── */
.recording-indicator {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 0 var(--space-2) var(--space-1);
  font-size: var(--text-xs);
  color: var(--danger);
}
.rec-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--danger);
  animation: pulse 1s infinite;
}
.rec-bars {
  display: flex;
  align-items: center;
  gap: 2px;
  height: 14px;
}
.rec-bars i {
  display: block;
  width: 2px;
  height: 4px;
  background: var(--danger);
  border-radius: 1px;
  animation: wave 0.8s ease-in-out infinite;
}
.rec-bars i:nth-child(2) { animation-delay: 0.1s; }
.rec-bars i:nth-child(3) { animation-delay: 0.2s; }
.rec-bars i:nth-child(4) { animation-delay: 0.3s; }
.rec-bars i:nth-child(5) { animation-delay: 0.4s; }
@keyframes wave {
  0%, 100% { height: 4px; }
  50% { height: 14px; }
}
.rec-label { font-weight: var(--font-weight-medium); }
.stt-error {
  font-size: var(--text-xs);
  color: var(--danger);
  padding: 0 var(--space-2) var(--space-1);
}

/* ── Modal ── */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: var(--overlay);
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
  color: var(--text-inverse);
}
.btn.primary:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

/* ── Context menu ── */
.context-sheet .context-title {
  font-size: var(--text-md);
  font-weight: var(--font-weight-semibold);
  margin: 0 0 var(--space-3);
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.context-actions {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}
.ctx-btn {
  width: 100%;
  padding: var(--space-3);
  font-size: var(--text-base);
  font-weight: var(--font-weight-medium);
  text-align: left;
  background: var(--bg-subtle);
  color: var(--text-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.ctx-btn:active {
  background: var(--bg-elevated);
}
.ctx-btn.danger {
  color: var(--danger);
  border-color: var(--danger-bg);
}
</style>
