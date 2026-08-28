<!--
  TasksView — 指挥中心（设计方案 v2 §3.3/§4.2，路由 /ai）

  Layout:
  L0. 分诊条（sticky）— 聚合计数：需要你 / 疑似卡死 / 全部正常；点击展开 L1
  L1. 需介入列表   — 审批卡片（内联 批准/拒绝/候选 chips）+ 疑似卡死任务
  A.  运行中       — 任务卡片第二行 = 健康信号 · 当前动作 · 距上次活动（§4.1 五态）
  B.  会话         — 最近会话纵列（长按：停止 / 归档）
  C.  已完成       — collapsed expandable section
  D.  Voice bar    — 固定于底部导航之上（转写先入草稿可编辑）
-->
<template>
  <PullToRefresh :on-refresh="handleRefresh" class="ai-hub-scroll">
  <div class="ai-hub">
    <!-- L0: Triage Bar (sticky) -->
    <section class="triage-bar" :class="triage.hasAttention ? 'attention' : 'allclear'">
      <button class="triage-main" @click="toggleTriage">
        <template v-if="triage.hasAttention">
          <span class="triage-icon">🔴</span>
          <span class="triage-text">
            <strong>{{ triage.needsInput }} 项需要你</strong>
            <span v-if="triage.stalled > 0" class="triage-sub"> · {{ triage.stalled }} 疑似卡死</span>
          </span>
          <span class="triage-chevron" :class="{ open: showTriage }">›</span>
        </template>
        <template v-else>
          <span class="triage-icon">🟢</span>
          <span class="triage-text">全部正常 · {{ triage.running }} 在跑</span>
        </template>
      </button>

      <!-- L1: Needs-attention list（按等待时长排序） -->
      <div v-if="showTriage && triage.hasAttention" class="triage-list">
        <div v-if="attentionItems.length === 0" class="triage-empty">
          待办明细拉取中，可下拉刷新
        </div>
        <div v-for="card in attentionItems" :key="card.key" class="attention-card" :class="card.type">
          <!-- 审批 / 提问卡片：内联操作（两步介入） -->
          <template v-if="card.type === 'approval'">
            <div class="attn-head">
              <span class="attn-kind" :class="card.item.kind">{{ card.item.kind === 'permission' ? '等审批' : '提问' }}</span>
              <span class="attn-title">{{ approvalTitle(card.item) }}</span>
              <span class="attn-wait">等了 {{ formatDuration(card.waitMs) }}</span>
            </div>
            <div v-if="card.item.kind === 'permission'" class="attn-actions">
              <button
                class="attn-btn primary"
                :disabled="approvalBusy !== null"
                @click.stop="inlineReply(card.item, 'once')"
              >✓ 批准</button>
              <button
                class="attn-btn ghost-danger"
                :disabled="approvalBusy !== null"
                @click.stop="inlineReply(card.item, 'reject')"
              >✕ 拒绝</button>
              <button class="attn-btn ghost" @click.stop="openApprovalDetail(card.item)">详情</button>
            </div>
            <div v-else class="attn-actions">
              <button
                v-for="opt in questionOptions(card.item)"
                :key="opt"
                class="attn-btn chip"
                :disabled="approvalBusy !== null"
                @click.stop="inlineAnswer(card.item, opt)"
              >{{ opt }}</button>
              <button class="attn-btn ghost" @click.stop="openApprovalDetail(card.item)">详情</button>
            </div>
          </template>
          <!-- 疑似卡死任务卡片 -->
          <template v-else>
            <div class="attn-head" @click="viewTask(card.task.id)">
              <span class="attn-kind stalled">疑似卡死</span>
              <span class="attn-title">{{ card.task.title }}</span>
              <span class="attn-wait">{{ formatDuration(card.waitMs) }}无响应</span>
            </div>
            <div class="attn-actions">
              <button class="attn-btn ghost" @click.stop="viewTask(card.task.id)">查看</button>
              <button class="attn-btn ghost-danger" @click.stop="stopTaskSessions(card.task)">⏹ 停止会话</button>
            </div>
          </template>
        </div>
      </div>
    </section>

    <!-- Section A: Running Tasks -->
    <section class="section running-section">
      <div class="section-header">
        <h2>
          <span class="dot pulse" />运行中
          <span class="badge">{{ activeTasks.length }}</span>
        </h2>
        <button class="link-btn" @click="showCreateModal = true">+ 新任务</button>
        <button class="link-btn acc-delegate-btn" @click="openAccDelegate">委托 ACC</button>
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
          @touchmove="onTouchMove"
          @touchend="onTouchEnd"
        >
          <div class="priority-bar" :class="task.priority" />
          <div class="task-body">
            <div class="task-title">{{ task.title }}</div>
            <div class="task-meta-row">
              <span v-if="signalFor(task)" class="health-signal" :class="'tone-' + signalFor(task)!.tone">
                <span class="health-dot" />{{ signalFor(task)!.action }}<template v-if="signalFor(task)!.since"> · {{ signalFor(task)!.since }}</template>
              </span>
              <span v-if="task.instanceName" class="instance-tag">{{ task.instanceName }}</span>
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
          @touchmove="onTouchMove"
          @touchend="onTouchEnd"
        >
          <div class="priority-bar" :class="task.priority" />
          <div class="task-body">
            <div class="task-title">{{ task.title }}</div>
            <div class="task-meta-row">
              <span v-if="signalFor(task)" class="health-signal" :class="'tone-' + signalFor(task)!.tone">
                <span class="health-dot" />{{ signalFor(task)!.action }}<template v-if="signalFor(task)!.since"> · {{ signalFor(task)!.since }}</template>
              </span>
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

      <div v-else-if="visibleSessions.length > 0" class="session-list">
        <div
          v-for="s in visibleSessions"
          :key="s.id"
          class="session-item"
          @click="openSession(s)"
          @touchstart="onSessionTouchStart(s, $event)"
          @touchmove="onTouchMove"
          @touchend="onTouchEnd"
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
          @touchmove="onTouchMove"
          @touchend="onTouchEnd"
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
              v-if="contextTask.status === 'active'"
              class="ctx-btn danger"
              @click="ctxStopSessions"
            >⏹ 停止会话</button>
            <button class="ctx-btn" @click="ctxResume">▶ 继续跟进</button>
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

    <!-- Session Context Menu (long-press): 停止 / 归档 -->
    <div v-if="showSessionMenu && contextSession" class="modal-overlay" @click.self="closeSessionMenu">
      <div class="modal-sheet context-sheet" @click.stop>
        <div class="modal-handle" />
        <div class="modal-body">
          <h2 class="context-title">{{ contextSession.title || '未命名会话' }}</h2>
          <div class="context-actions">
            <button
              v-if="contextSession.status === 'active' || contextSession.status === 'streaming'"
              class="ctx-btn danger"
              @click="ctxStopSession"
            >⏹ 停止</button>
            <button class="ctx-btn" @click="ctxArchiveSession">📥 归档（本地）</button>
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

    <!-- Delegate to ACC Modal -->
    <Teleport to="body">
      <div
        v-if="showAccModal"
        class="modal-overlay"
        @click.self="closeAccDelegate"
      >
        <div class="modal-sheet acc-sheet">
          <div class="modal-handle" />
          <div class="modal-body">
            <h2>委托给 ACC</h2>
            <p class="acc-hint">ACC 会接管后续会话与执行，无需在手机上跟进。</p>
            <div class="form-group">
              <label>标题 *</label>
              <input
                v-model="accDraft.title"
                type="text"
                placeholder="例如：实现登录页面"
                maxlength="120"
              />
            </div>
            <div class="form-group">
              <label>描述（可选）</label>
              <textarea
                v-model="accDraft.description"
                placeholder="补充背景、目标或验收标准"
                rows="3"
                maxlength="500"
              />
              <div class="char-counter">{{ accDraft.description.length }} / 500</div>
            </div>
            <div v-if="accStore.error" class="acc-error">{{ accStore.error }}</div>
            <div class="modal-actions">
              <button class="btn cancel" :disabled="accStore.submitting" @click="closeAccDelegate">取消</button>
              <button
                class="btn primary"
                :disabled="!accDraft.title.trim() || accStore.submitting"
                @click="submitAccDelegate"
              >
                {{ accStore.submitting ? '提交中…' : '委托给 ACC' }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
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
import { useToast } from '../../composables/useToast'
import { useApprovalAlerts } from '../../composables/useApprovalAlerts'
import { useAccTasksStore } from '../../stores/accTasks'
import { useAuthStore } from '../../stores/auth'
import { EmptyState, PullToRefresh } from '../../components'
import { assessHealth, summarizeHealth, formatDuration, type HealthSignal } from './health'
import { useInstanceApprovals, type PendingItem } from './useInstanceApprovals'
import {
  readArchivedIds,
  setSessionArchived,
  type ArchiveScope,
} from '../sessions/sessionArchive'
import { useConfirm } from '../../composables/useConfirm'

const router = useRouter()
const { confirm } = useConfirm()
const { isRecording, isTranscribing, sttError, startRecording, stopRecording } = useVoiceInput()
const auth = useAuthStore()

// ── 健康度 / 分诊（设计方案 v2 §4.1/§4.2，P0 近似数据） ──
const approvals = useInstanceApprovals(() => currentInstance.value?.id || '')
useApprovalAlerts(approvals.pending, { instanceId: () => currentInstance.value?.id || '' })
const showTriage = ref(false)
/** 驱动 "距上次活动" 周期刷新的时钟（30s 一跳，避免整页轮询）。 */
const nowTick = ref(Date.now())
let nowTimer: ReturnType<typeof setInterval> | null = null
const approvalBusy = ref<string | null>(null)

function toggleTriage() {
  if (!triage.value.hasAttention) return
  showTriage.value = !showTriage.value
}

/** 每个任务的健康信号（P0：task.pendingApprovals + updatedAt 近似）。 */
function signalFor(task: Task): HealthSignal | undefined {
  if (!task) return undefined
  return assessHealth({
    active: task.status === 'active',
    updatedAt: task.updatedAt,
    pendingApprovals: task.status === 'active' ? task.pendingApprovals ?? 0 : 0,
    now: nowTick.value,
  })
}

/** L0 分诊条聚合计数：needs-input 以实例待审批拉取为准（最权威），任务信号兜底。 */
const triage = computed(() => {
  const signals: HealthSignal[] = []
  for (const t of activeTasks.value) {
    const sig = signalFor(t)
    if (sig) signals.push(sig)
  }
  const s = summarizeHealth(signals)
  const needsInput = Math.max(s.needsInput, approvals.pending.value.length)
  return {
    needsInput,
    stalled: s.stalled,
    running: s.running,
    hasAttention: needsInput + s.stalled > 0,
  }
})

/** L1 需介入：审批条目 + 疑似卡死任务，按等待时长降序。 */
const attentionItems = computed(() => {
  const approvalCards = approvals.pending.value.map((item) => ({
    type: 'approval' as const,
    key: item.requestId,
    waitMs: nowTick.value - item.firstSeenAt,
    item,
  }))
  const stalledCards: Array<{
    type: 'stalled'
    key: string
    waitMs: number
    task: Task
  }> = []
  for (const t of activeTasks.value) {
    const sig = signalFor(t)
    if (sig?.state === 'stalled') stalledCards.push({ type: 'stalled', key: `task-${t.id}`, waitMs: sig.sinceMs, task: t })
  }
  return [...approvalCards, ...stalledCards].sort((a, b) => b.waitMs - a.waitMs)
})

/** 审批卡标题：动作 / 问题 + 所属会话。 */
function approvalTitle(item: PendingItem): string {
  const base =
    item.kind === 'permission'
      ? item.action || '工具调用'
      : item.question?.question || 'AI 提问'
  const title = sessionTitleById.value.get(item.sessionId)
  return title ? `${base} · ${title}` : base
}

/** 问答候选 chips（最多展示 3 个，其余走详情）。 */
function questionOptions(item: PendingItem): string[] {
  return (item.question?.options ?? [])
    .map((o) => o.label)
    .filter(Boolean)
    .slice(0, 3)
}

const sessionTitleById = computed(
  () => new Map(sessions.value.map((s) => [s.id, s.title || s.id.slice(0, 8)])),
)

async function inlineReply(item: PendingItem, decision: 'once' | 'reject') {
  if (approvalBusy.value) return
  approvalBusy.value = item.requestId
  try {
    const status = await approvals.replyPermission(item, decision)
    if (status === 'confirmed') toast.success(decision === 'once' ? '已批准' : '已拒绝')
    else if (status === 'queued-offline') toast.info('当前离线，决定已入队，联网后自动发送')
    else if (status === 'conflict') toast.error('该请求已过期或已在别处处理')
    else toast.error('发送失败，请重试')
  } finally {
    approvalBusy.value = null
  }
}

async function inlineAnswer(item: PendingItem, optionLabel: string) {
  if (approvalBusy.value) return
  approvalBusy.value = item.requestId
  try {
    const status = await approvals.answerQuestion(item, optionLabel)
    if (status === 'confirmed') toast.success('已回答')
    else if (status === 'conflict') toast.error('该提问已过期或已在别处处理')
    else toast.error('发送失败，请重试')
  } finally {
    approvalBusy.value = null
  }
}

function openApprovalDetail(item: PendingItem) {
  router.push({
    path: `/sessions/${item.sessionId}`,
    query: { instance_id: currentInstance.value?.id || '', approval: 'open' },
  })
}

// ── 会话归档（本地元数据，sessionArchive.ts；归档从指挥中心列表隐藏） ──
const archivedSets = ref<Map<string, Set<string>>>(new Map())

function archiveScopeFor(instanceId?: string): ArchiveScope {
  return { workspaceId: auth.workspaceId || 'default', instanceId: instanceId || 'all' }
}

function loadArchivedSets() {
  const sets = new Map<string, Set<string>>()
  const instanceIds = new Set(sessions.value.map((s) => s.instanceId || 'all'))
  for (const iid of instanceIds) {
    sets.set(iid, readArchivedIds(localStorage, archiveScopeFor(iid === 'all' ? undefined : iid)))
  }
  archivedSets.value = sets
}

const visibleSessions = computed(() =>
  sessions.value.filter((s) => !archivedSets.value.get(s.instanceId || 'all')?.has(s.id)),
)

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

// ── Delegate to ACC ──
const toast = useToast()
const accStore = useAccTasksStore()
const showAccModal = ref(false)
const accDraft = ref({ title: '', description: '' })

function openAccDelegate() {
  accDraft.value = { title: '', description: '' }
  accStore.reset()
  showAccModal.value = true
}

function closeAccDelegate() {
  showAccModal.value = false
}

async function submitAccDelegate() {
  const title = accDraft.value.title.trim()
  if (!title) return
  const created = await accStore.createTask({
    title,
    description: accDraft.value.description.trim() || undefined,
  })
  if (created) {
    // 追加到本地任务列表，不重新拉取
    const accTask: Task = {
      id: created.id || `acc-${Date.now()}`,
      title: created.title,
      description: created.description,
      status: 'active',
      priority: 'medium',
      source: 'acc',
      createdAt: created.createdAt || new Date().toISOString(),
      updatedAt: created.updatedAt || new Date().toISOString(),
      sessionCount: 0,
      instanceName: currentInstance.value?.displayName || currentInstance.value?.name || 'ACC',
    }
    tasks.value.unshift(accTask)
    closeAccDelegate()
    toast.success(`已委托给 ACC：${created.title}`)
  } else {
    toast.error(accStore.error || '委托失败，请重试')
  }
}

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
  // L0/L1 数据源：实例级待审批实时流（WS 事件驱动 + 首拉）。
  approvals.startLive()
  nowTimer = setInterval(() => { nowTick.value = Date.now() }, 30_000)
  wsClient.on('task_created', handleTaskUpdate)
  wsClient.on('task_updated', handleTaskUpdate)
  wsClient.on('session_attached', handleSessionAttached)
})

onUnmounted(() => {
  approvals.stopLive()
  if (nowTimer !== null) {
    clearInterval(nowTimer)
    nowTimer = null
  }
  wsClient.off('task_created', handleTaskUpdate)
  wsClient.off('task_updated', handleTaskUpdate)
  wsClient.off('session_attached', handleSessionAttached)
})

// ── Data Loading ──
async function handleRefresh() {
  await Promise.all([loadTasks(), loadSessions(), approvals.refresh()])
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
    loadArchivedSets()
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

/** 通用长按：500ms 无移动触发 onFire（振动反馈 + 抑制后续 click）。 */
function startLongPress(e: TouchEvent, onFire: () => void) {
  const t = e.touches[0]
  if (!t) return
  pressStart = { x: t.clientX, y: t.clientY }
  longPressTimer = setTimeout(() => {
    onFire()
    suppressClick = true
    if (typeof navigator !== 'undefined' && navigator.vibrate) navigator.vibrate(12)
    setTimeout(() => { suppressClick = false }, 400)
  }, 500)
}

function onTouchMove(e: TouchEvent) {
  if (!longPressTimer) return
  const t = e.touches[0]
  if (!t) return
  if (Math.abs(t.clientX - pressStart.x) > 8 || Math.abs(t.clientY - pressStart.y) > 8) {
    clearTimeout(longPressTimer)
    longPressTimer = null
  }
}

function onTouchEnd() {
  if (longPressTimer) {
    clearTimeout(longPressTimer)
    longPressTimer = null
  }
}

function onTaskTouchStart(task: Task, e: TouchEvent) {
  startLongPress(e, () => {
    contextTask.value = task
    showContextMenu.value = true
  })
}

// ── 会话长按菜单：停止 / 归档 ──
const showSessionMenu = ref(false)
const contextSession = ref<any | null>(null)

function onSessionTouchStart(s: any, e: TouchEvent) {
  startLongPress(e, () => {
    contextSession.value = s
    showSessionMenu.value = true
  })
}

function closeSessionMenu() {
  showSessionMenu.value = false
  contextSession.value = null
}

async function ctxStopSession() {
  const s = contextSession.value
  if (!s) return
  closeSessionMenu()
  try {
    await api.interruptSession(s.id, s.instanceId || currentInstance.value?.id || '')
    toast.success('已发送停止指令')
    loadSessions()
  } catch (e) {
    console.error('Failed to interrupt session:', e)
    toast.error('停止失败，请重试')
  }
}

function ctxArchiveSession() {
  const s = contextSession.value
  if (!s) return
  const iid = s.instanceId || 'all'
  const next = setSessionArchived(
    localStorage,
    archiveScopeFor(iid === 'all' ? undefined : iid),
    s.id,
    true,
  )
  archivedSets.value = new Map(archivedSets.value).set(iid, next)
  closeSessionMenu()
  toast.success('已归档（本地隐藏，可在会话列表管理）')
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
  if (!contextTask.value || !(await confirm({ title: '删除任务', message: `确定删除「${contextTask.value.title}」？此操作不可撤销。`, confirmText: '删除', danger: true }))) return
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

/**
 * 停止任务关联的全部会话（POST /api/mobile/sessions/:id/interrupt，服务端经
 * opencode adapter 调上游 /session/:id/abort —— 设计方案 v2 §4.2-4 的 P0
 * 落地通道；plugin_hub 的 session.stop 命令信封存在协议缺口且无调用方，
 * 见 STATUS-MATRIX 备注）。
 */
async function stopTaskSessions(task: Task): Promise<void> {
  if (!(await confirm({ title: '停止会话', message: `停止「${task.title}」关联的会话？进行中的 agent 循环将被中断。`, confirmText: '停止', danger: true }))) return
  try {
    const links = await api.getTaskSessions(task.id)
    if (links.length === 0) {
      toast.error('该任务没有关联会话')
      return
    }
    let stopped = 0
    let failed = 0
    for (const link of links) {
      try {
        await api.interruptSession(link.sessionId, link.instanceId || currentInstance.value?.id || '')
        stopped++
      } catch {
        failed++
      }
    }
    if (stopped > 0) toast.success(`已停止 ${stopped} 个会话${failed ? `，${failed} 个失败` : ''}`)
    else toast.error('停止失败，请重试')
    loadSessions()
  } catch (e) {
    console.error('Failed to stop task sessions:', e)
    toast.error('停止失败，请重试')
  }
}

function ctxStopSessions() {
  const task = contextTask.value
  if (!task) return
  closeContextMenu()
  void stopTaskSessions(task)
}

/** 继续跟进：跳到任务最近的关联会话并预填「继续」草稿（可编辑再发送）。 */
async function ctxResume() {
  const task = contextTask.value
  if (!task) return
  closeContextMenu()
  try {
    const links = await api.getTaskSessions(task.id)
    if (links.length > 0) {
      const link = links[0]
      router.push({
        path: `/sessions/${link.sessionId}`,
        query: {
          instance_id: link.instanceId || currentInstance.value?.id || '',
          prompt: '继续',
        },
      })
    } else {
      viewTask(task.id)
    }
  } catch (e) {
    console.error('Failed to resume task:', e)
    viewTask(task.id)
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

/* ── L0 分诊条（sticky，唯一必读层） ── */
.triage-bar {
  position: sticky;
  top: 0;
  z-index: var(--z-base);
  margin: 8px var(--space-3) 0;
  border-radius: 10px;
  border: 1px solid var(--border);
  background: var(--bg-elevated);
  overflow: hidden;
}
.triage-bar.attention {
  border-color: color-mix(in srgb, var(--danger) 35%, var(--border));
}
.triage-bar.allclear {
  border-color: color-mix(in srgb, var(--success) 25%, var(--border));
}
.triage-main {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  background: none;
  border: none;
  cursor: pointer;
  text-align: left;
  min-height: 44px; /* 触摸热区 */
}
.triage-icon {
  font-size: 14px;
  flex-shrink: 0;
}
.triage-text {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.triage-text strong {
  font-weight: 700;
}
.triage-sub {
  color: var(--warning);
  font-weight: 600;
}
.triage-chevron {
  font-size: 16px;
  color: var(--text-muted);
  transition: transform 200ms;
}
.triage-chevron.open {
  transform: rotate(90deg);
}

/* ── L1 需介入列表 ── */
.triage-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 4px 10px 10px;
}
.triage-empty {
  font-size: 12px;
  color: var(--text-muted);
  text-align: center;
  padding: 8px 0;
}
.attention-card {
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-left: 3px solid var(--danger);
  border-radius: 8px;
  padding: 8px 10px;
}
.attention-card.stalled {
  border-left-color: var(--warning);
}
.attn-head {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
}
.attn-kind {
  flex-shrink: 0;
  font-size: 10px;
  font-weight: 700;
  padding: 1px 6px;
  border-radius: 4px;
  line-height: 15px;
  background: var(--danger-bg, rgba(239, 68, 68, 0.1));
  color: var(--danger);
}
.attn-kind.question,
.attn-kind.stalled {
  background: var(--warning-bg, rgba(245, 158, 11, 0.12));
  color: var(--warning);
}
.attn-title {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.attn-wait {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-muted);
}
.attn-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}
.attn-btn {
  min-height: 36px;
  padding: 6px 14px;
  font-size: 13px;
  font-weight: 600;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--bg-elevated);
  color: var(--text-primary);
  cursor: pointer;
}
.attn-btn:active {
  transform: scale(0.97);
}
.attn-btn:disabled {
  opacity: 0.5;
}
.attn-btn.primary {
  background: var(--brand-primary);
  border-color: var(--brand-primary);
  color: var(--text-inverse);
}
.attn-btn.ghost {
  color: var(--text-secondary);
}
.attn-btn.ghost-danger {
  color: var(--danger);
  border-color: var(--danger-bg, rgba(239, 68, 68, 0.2));
}
.attn-btn.chip {
  background: var(--brand-bg);
  border-color: color-mix(in srgb, var(--brand-primary) 25%, var(--border));
  color: var(--brand-primary);
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

/* ── Delegate to ACC ── */
.section-header .acc-delegate-btn {
  margin-left: 8px;
}
.acc-sheet {
  max-width: 560px;
  margin: 0 auto;
  width: 100%;
}
.acc-hint {
  font-size: 12px;
  color: var(--text-muted);
  margin: -8px 0 14px;
}
.acc-sheet .form-group input,
.acc-sheet .form-group textarea {
  width: 100%;
}
.char-counter {
  text-align: right;
  font-size: 11px;
  color: var(--text-muted);
  margin-top: 4px;
}
.acc-error {
  font-size: 12px;
  color: var(--danger);
  background: var(--danger-bg, rgba(239, 68, 68, 0.08));
  padding: 6px 10px;
  border-radius: 6px;
  margin: 4px 0 8px;
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
  min-width: 0;
}
/* ── 健康信号行（信号即界面：色点 + 当前动作 · 距上次活动） ── */
.health-signal {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  font-weight: 600;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-secondary);
}
.health-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--text-muted);
}
.health-signal.tone-danger { color: var(--danger); }
.health-signal.tone-danger .health-dot { background: var(--danger); animation: pulse 1.6s infinite; }
.health-signal.tone-warning { color: var(--warning); }
.health-signal.tone-warning .health-dot { background: var(--warning); }
.health-signal.tone-success { color: var(--success); }
.health-signal.tone-success .health-dot { background: var(--success); animation: pulse 2s infinite; }
.health-signal.tone-muted { color: var(--text-muted); }
.health-signal.tone-muted .health-dot { background: var(--text-muted); }

/* compact 断点（<560px，与 useBreakpoint 一致）：信号行让位，删实例标签 */
@media (max-width: 559px) {
  .task-meta-row .instance-tag {
    display: none;
  }
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
  z-index: var(--z-fab);
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
  z-index: var(--z-sheet);
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
