<!--
  TaskDetailView — Codex-style compact detail with operation buttons
-->
<template>
  <div class="task-detail">
    <ErrorState
      v-if="loadError && !task"
      title="任务加载失败"
      :message="loadError"
      @retry="load"
    />
    <p v-else-if="loadError" class="form-error" role="alert">
      {{ loadError }}
      <button type="button" class="link-btn retry-link" @click="load">重试</button>
    </p>
    <!-- Header: priority + title + status on one line -->
    <div class="header">
      <span class="priority-chip" :class="task?.priority">
        {{ priorityText(task?.priority) }}
      </span>
      <h1 class="title">{{ task?.title || '加载中...' }}</h1>
      <span class="status-chip" :class="task?.status">
        {{ statusText(task?.status) }}
      </span>
    </div>

    <!-- Description -->
    <p v-if="task?.description" class="desc">{{ task.description }}</p>

    <!-- Stats strip -->
    <div class="stats-strip">
      <div class="stat">
        <span class="stat-icon">💬</span>
        <span class="stat-val">{{ task?.sessionCount || 0 }}</span>
        <span class="stat-lbl">会话</span>
      </div>
      <div class="stat">
        <span class="stat-icon">📅</span>
        <span class="stat-val">{{ formatDate(task?.createdAt) }}</span>
        <span class="stat-lbl">创建</span>
      </div>
      <div v-if="task?.workstreamId" class="stat">
        <span class="stat-icon" aria-hidden="true">💻</span>
        <span class="stat-val" :title="task?.workstreamId">{{ task?.workstreamId?.slice(0, 8) }}</span>
        <span class="stat-lbl">实例</span>
      </div>
    </div>

    <!-- Action Bar -->
    <div class="action-bar">
      <button
        v-if="task?.status !== 'active'"
        class="action-btn resume"
        :disabled="updating"
        @click="updateStatus('active')"
      >
        ▶ 恢复
      </button>
      <button
        v-if="task?.status === 'active'"
        class="action-btn pause"
        :disabled="updating"
        @click="updateStatus('blocked')"
      >
        ⏸ 暂停
      </button>
      <button
        v-if="task?.status !== 'completed'"
        class="action-btn complete"
        :disabled="updating"
        @click="updateStatus('completed')"
      >
        ✅ 完成
      </button>
      <button class="action-btn attach" @click="showAttachModal = true">
        📎 附加
      </button>
      <button class="action-btn delete" :disabled="deleting" @click="confirmDelete">
        🗑
      </button>
    </div>

    <!-- Sessions Section -->
    <div class="sessions-section">
      <div class="section-header">
        <h3>关联会话 <span class="badge">{{ sessions.length }}</span></h3>
        <button class="link-btn" @click="showAttachModal = true">+ 附加</button>
      </div>

      <div v-if="sessions.length > 0" class="session-list">
        <div
          v-for="s in sessions"
          :key="s.sessionId"
          class="session-row"
          tabindex="0"
          role="link"
          :aria-label="`打开会话 ${s.sessionId.slice(0, 16)}`"
          @click="openSession(s)"
          @keydown.enter.prevent="openSession(s)"
          @keydown.space.prevent="openSession(s)"
        >
          <span class="status-dot" aria-hidden="true" />
          <div class="session-info">
            <div class="session-id">{{ s.sessionId.slice(0, 16) }}…</div>
            <div class="session-tags">
              <span class="tag">{{ s.role }}</span>
              <span class="tag">{{ s.instanceId }}</span>
            </div>
          </div>
          <span class="chevron" aria-hidden="true">›</span>
        </div>
      </div>

      <div v-else class="empty-sessions">
        <span class="empty-text">暂无关联会话</span>
      </div>
    </div>

    <!-- Attach Modal -->
    <div v-if="showAttachModal" class="modal-overlay" role="dialog" aria-modal="true" aria-labelledby="attach-modal-title" @click.self="showAttachModal = false">
      <div class="modal-sheet" @click.stop>
        <div class="modal-handle" aria-hidden="true" />
        <div class="modal-body">
          <h2 id="attach-modal-title">附加会话</h2>
          <div class="form-group">
            <label for="attach-session-id">会话 ID *</label>
            <input id="attach-session-id" v-model="newSession.sessionId" type="text" placeholder="ses_..." />
          </div>
          <div class="form-group">
            <label for="attach-instance-id">实例 ID *</label>
            <input id="attach-instance-id" v-model="newSession.instanceId" type="text" placeholder="local-dev" />
          </div>
          <div class="form-group">
            <label for="attach-role">角色</label>
            <select id="attach-role" v-model="newSession.role">
              <option value="primary">主要</option>
              <option value="supporting">支持</option>
              <option value="exploratory">探索</option>
            </select>
          </div>
          <p v-if="attachError" class="form-error" role="alert">{{ attachError }}</p>
          <div class="modal-actions">
            <button type="button" class="btn cancel" @click="showAttachModal = false">取消</button>
            <button
              type="button"
              class="btn primary"
              :disabled="!newSession.sessionId || !newSession.instanceId || attaching"
              @click="handleAttach"
            >{{ attaching ? '附加中…' : '附加' }}</button>
          </div>
        </div>
      </div>
    </div>

    <p v-if="statusError" class="form-error" role="alert">{{ statusError }}</p>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { api, type Task } from '../../api/client'
import { ErrorState } from '../../components'

const router = useRouter()
const route = useRoute()

const task = ref<Task | null>(null)
const sessions = ref<any[]>([])
const showAttachModal = ref(false)
const newSession = ref({ sessionId: '', instanceId: '', role: 'primary' })
const updating = ref(false)
const deleting = ref(false)
const attaching = ref(false)
const attachError = ref('')
const statusError = ref('')
const loadError = ref('')

onMounted(() => load())

async function load() {
  const taskId = route.params.id as string
  loadError.value = ''
  try {
    task.value = await api.getTask(taskId)
    sessions.value = await api.getTaskSessions(taskId)
  } catch (e: any) {
    console.error('Failed to load task:', e)
    loadError.value = e?.message || '加载任务失败，请检查网络后重试'
  }
}

async function updateStatus(status: string) {
  if (!task.value || updating.value) return
  const oldStatus = task.value.status
  updating.value = true
  statusError.value = ''
  // Optimistic update — rewind on error.
  task.value.status = status as any
  try {
    await api.updateTask(task.value.id, { status })
  } catch (e: any) {
    console.error('Failed to update status:', e)
    task.value.status = oldStatus as any
    statusError.value = `状态更新失败：${e?.message || '未知错误'}`
  } finally {
    updating.value = false
  }
}

async function confirmDelete() {
  if (!task.value || deleting.value) return
  if (!confirm('确定删除此任务？此操作不可撤销。')) return
  const taskId = task.value.id
  deleting.value = true
  statusError.value = ''
  try {
    await api.deleteTask(taskId)
    router.push('/ai')
  } catch (e: any) {
    console.error('Failed to delete task:', e)
    deleting.value = false
    statusError.value = `删除失败：${e?.message || '未知错误'}，请重试或稍后再试。`
  }
}

async function handleAttach() {
  if (!task.value || !newSession.value.sessionId || !newSession.value.instanceId || attaching.value) return
  attaching.value = true
  attachError.value = ''
  try {
    await api.attachSession(
      task.value.id,
      newSession.value.instanceId,
      newSession.value.sessionId,
      newSession.value.role,
    )
    sessions.value = await api.getTaskSessions(task.value.id)
    newSession.value = { sessionId: '', instanceId: '', role: 'primary' }
    showAttachModal.value = false
  } catch (e: any) {
    console.error('Failed to attach session:', e)
    attachError.value = e?.message || '附加会话失败，请重试'
  } finally {
    attaching.value = false
  }
}

function openSession(s: any) {
  router.push({
    path: `/sessions/${s.sessionId}`,
    query: { instance_id: s.instanceId },
  })
}

function priorityText(p?: string): string {
  return { high: '高', medium: '中', low: '低' }[p ?? ''] ?? ''
}

function statusText(s?: string): string {
  return { active: '进行中', blocked: '已阻塞', completed: '已完成' }[s ?? ''] ?? s ?? ''
}

function formatDate(d?: string): string {
  if (!d) return '-'
  return new Date(d).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })
}
</script>

<style scoped>
.task-detail {
  min-height: 100vh;
  background: var(--bg-base);
  padding: 12px var(--space-3);
  padding-bottom: 80px;
}

/* ── Header ── */
.header {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.priority-chip {
  font-size: 10px;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 4px;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}
.priority-chip.high { background: var(--color-danger-soft); color: var(--danger); }
.priority-chip.medium { background: var(--color-warning-soft); color: var(--warning); }
.priority-chip.low { background: var(--color-success-soft); color: var(--success); }

.title {
  font-size: 17px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.status-chip {
  font-size: 10px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 999px;
}
.status-chip.active { background: var(--color-success-soft); color: var(--success); }
.status-chip.blocked { background: var(--color-warning-soft); color: var(--warning); }
.status-chip.completed { background: var(--color-brand-soft); color: var(--brand-primary); }

.desc {
  font-size: 13px;
  color: var(--text-secondary);
  margin: 0 0 12px;
  line-height: 1.5;
}

/* ── Stats Strip ── */
.stats-strip {
  display: flex;
  gap: 4px;
  margin-bottom: 12px;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px 4px;
}
.stat {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
}
.stat-icon { font-size: 14px; }
.stat-val {
  font-size: 13px;
  font-weight: 700;
  color: var(--text-primary);
}
.stat-lbl {
  font-size: 10px;
  color: var(--text-muted);
}

/* ── Action Bar ── */
.action-bar {
  display: flex;
  gap: 6px;
  margin-bottom: 16px;
  flex-wrap: wrap;
}
.action-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 12px;
  font-size: 12px;
  font-weight: 600;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  color: var(--text-primary);
  cursor: pointer;
  transition: background var(--duration-fast), border-color var(--duration-fast), color var(--duration-fast);
}
.action-btn:active {
  background: var(--bg-subtle);
}
.action-btn.resume { border-color: var(--success); color: var(--success); }
.action-btn.complete { border-color: var(--brand-primary); color: var(--brand-primary); }
.action-btn.delete {
  border-color: transparent;
  background: transparent;
  color: var(--text-muted);
  padding: 6px 8px;
}

/* ── Sessions ── */
.sessions-section {
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 12px;
}
.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}
.section-header h3 {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0;
  display: flex;
  align-items: center;
  gap: 6px;
}
.badge {
  font-size: 10px;
  font-weight: 700;
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--bg-subtle);
  color: var(--text-secondary);
}
.link-btn {
  font-size: 12px;
  font-weight: 600;
  color: var(--brand-primary);
  background: none;
  border: none;
  cursor: pointer;
}
.session-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.session-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px;
  border-radius: var(--radius-sm);
  cursor: pointer;
  transition: background var(--duration-fast);
}
.session-row:hover { background: var(--bg-subtle); }
.session-row:focus-visible {
  outline: none;
  background: var(--bg-subtle);
  box-shadow: 0 0 0 2px var(--brand-primary);
}
.session-row:active { background: var(--bg-subtle); }
.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--brand-primary);
  flex-shrink: 0;
}
.session-info { flex: 1; min-width: 0; }
.session-id {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-primary);
  font-family: monospace;
}
.session-tags {
  display: flex;
  gap: 4px;
  margin-top: 2px;
}
.tag {
  font-size: 9px;
  padding: 1px 5px;
  border-radius: 3px;
  background: var(--bg-subtle);
  color: var(--text-muted);
}
.chevron {
  font-size: 14px;
  color: var(--text-muted);
  opacity: 0.5;
}
.empty-sessions {
  text-align: center;
  padding: 24px 0;
}
.empty-text {
  font-size: 12px;
  color: var(--text-muted);
}

/* ── Modal ── */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: var(--color-overlay-scrim);
  display: flex;
  align-items: flex-end;
  z-index: var(--z-modal);
}
.modal-sheet {
  background: var(--bg-elevated);
  border-radius: var(--radius-xl) var(--radius-xl) 0 0;
  width: 100%;
  animation: slideUp var(--duration-base) var(--ease-spring);
  touch-action: none;
}
@keyframes slideUp { from { transform: translateY(100%); } }
.modal-handle {
  width: 36px;
  height: 4px;
  border-radius: 2px;
  background: var(--border-strong);
  margin: 8px auto 4px;
}
.modal-body {
  padding: 8px 20px 20px;
}
.modal-body h2 {
  font-size: 16px;
  font-weight: 700;
  margin: 4px 0 16px;
  color: var(--text-primary);
}
.form-group {
  margin-bottom: 10px;
}
.form-group label {
  display: block;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
  margin-bottom: 4px;
}
.form-group input,
.form-group select {
  width: 100%;
  padding: 10px;
  font-size: 14px;
  background: var(--bg-subtle);
  color: var(--text-primary);
  border: 1px solid transparent;
  border-radius: 8px;
  box-sizing: border-box;
}
.form-group input:focus,
.form-group select:focus {
  border-color: var(--brand-primary);
  outline: none;
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
.btn.cancel { background: var(--bg-subtle); color: var(--text-primary); }
.btn.primary { background: var(--brand-primary); color: var(--text-inverse); }
.btn.primary:disabled { opacity: 0.4; cursor: not-allowed; }

.form-error {
  margin: var(--space-3) var(--space-3) 0;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-md);
  background: var(--color-danger-soft);
  color: var(--danger);
  font-size: 13px;
  font-weight: 500;
  line-height: 1.5;
}
.form-error:first-child { margin-top: var(--space-3); }
.retry-link {
  display: inline-block;
  margin-left: var(--space-2);
  font-size: 13px;
}
</style>
