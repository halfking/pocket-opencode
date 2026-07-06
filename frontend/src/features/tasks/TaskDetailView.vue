<!--
  TaskDetailView — Codex-style compact detail with operation buttons
-->
<template>
  <div class="task-detail">
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
        <span class="stat-icon">💻</span>
        <span class="stat-val">{{ task?.workstreamId?.slice(0, 8) }}</span>
        <span class="stat-lbl">实例</span>
      </div>
    </div>

    <!-- Action Bar -->
    <div class="action-bar">
      <button
        v-if="task?.status !== 'active'"
        class="action-btn resume"
        @click="updateStatus('active')"
      >
        ▶ 恢复
      </button>
      <button
        v-if="task?.status === 'active'"
        class="action-btn pause"
        @click="updateStatus('blocked')"
      >
        ⏸ 暂停
      </button>
      <button
        v-if="task?.status !== 'completed'"
        class="action-btn complete"
        @click="updateStatus('completed')"
      >
        ✅ 完成
      </button>
      <button class="action-btn attach" @click="showAttachModal = true">
        📎 附加
      </button>
      <button class="action-btn delete" @click="confirmDelete">
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
          @click="openSession(s)"
        >
          <span class="status-dot" />
          <div class="session-info">
            <div class="session-id">{{ s.sessionId.slice(0, 16) }}…</div>
            <div class="session-tags">
              <span class="tag">{{ s.role }}</span>
              <span class="tag">{{ s.instanceId }}</span>
            </div>
          </div>
          <span class="chevron">›</span>
        </div>
      </div>

      <div v-else class="empty-sessions">
        <span class="empty-text">暂无关联会话</span>
      </div>
    </div>

    <!-- Attach Modal -->
    <div v-if="showAttachModal" class="modal-overlay" @click="showAttachModal = false">
      <div class="modal-sheet" @click.stop>
        <div class="modal-handle" />
        <div class="modal-body">
          <h2>附加会话</h2>
          <div class="form-group">
            <label>会话 ID *</label>
            <input v-model="newSession.sessionId" type="text" placeholder="ses_..." />
          </div>
          <div class="form-group">
            <label>实例 ID *</label>
            <input v-model="newSession.instanceId" type="text" placeholder="local-dev" />
          </div>
          <div class="form-group">
            <label>角色</label>
            <select v-model="newSession.role">
              <option value="primary">主要</option>
              <option value="supporting">支持</option>
              <option value="exploratory">探索</option>
            </select>
          </div>
          <div class="modal-actions">
            <button class="btn cancel" @click="showAttachModal = false">取消</button>
            <button
              class="btn primary"
              :disabled="!newSession.sessionId || !newSession.instanceId"
              @click="handleAttach"
            >附加</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { api, type Task } from '../../api/client'

const router = useRouter()
const route = useRoute()

const task = ref<Task | null>(null)
const sessions = ref<any[]>([])
const showAttachModal = ref(false)
const newSession = ref({ sessionId: '', instanceId: '', role: 'primary' })

onMounted(async () => {
  const taskId = route.params.id as string
  try {
    task.value = await api.getTask(taskId)
    sessions.value = await api.getTaskSessions(taskId)
  } catch (e) {
    console.error('Failed to load task:', e)
  }
})

async function updateStatus(status: string) {
  if (!task.value) return
  try {
    // Optimistic update
    const old = task.value.status
    task.value.status = status as any
    // TODO: call API to persist status change
    // await api.updateTask(task.value.id, { status })
  } catch (e) {
    console.error('Failed to update status:', e)
  }
}

function confirmDelete() {
  if (confirm('确定删除此任务？')) {
    // TODO: call API to delete
    router.push('/ai')
  }
}

async function handleAttach() {
  if (!task.value || !newSession.value.sessionId || !newSession.value.instanceId) return
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
  } catch (e) {
    console.error('Failed to attach session:', e)
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
.priority-chip.high { background: rgba(239, 68, 68, 0.12); color: var(--error, #ef4444); }
.priority-chip.medium { background: rgba(245, 158, 11, 0.12); color: var(--warning); }
.priority-chip.low { background: rgba(16, 185, 129, 0.12); color: var(--success); }

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
.status-chip.active { background: rgba(16, 185, 129, 0.12); color: var(--success); }
.status-chip.blocked { background: rgba(245, 158, 11, 0.12); color: var(--warning); }
.status-chip.completed { background: rgba(102, 126, 234, 0.12); color: var(--brand-primary); }

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
  border-radius: 8px;
  background: var(--bg-elevated);
  color: var(--text-primary);
  cursor: pointer;
  transition: background 120ms;
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
  border-radius: 6px;
  cursor: pointer;
  transition: background 120ms;
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
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: flex-end;
  z-index: 1000;
}
.modal-sheet {
  background: var(--bg-elevated);
  border-radius: 16px 16px 0 0;
  width: 100%;
  animation: slideUp 200ms ease;
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
.btn.primary { background: var(--brand-primary); color: #fff; }
.btn.primary:disabled { opacity: 0.4; cursor: not-allowed; }
</style>
