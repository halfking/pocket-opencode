<!--
  TaskCollaborationPanel —— 工作项协作面板（P3，docs/学习muse/03-架构方案.md §4）。

  三块：参与者名单、委派入口、活动流 + 评论。

  降级策略与 AddToLearningButton 一致：协作接口 503（没装 PG 的部署）或
  403（不是参与者）时**整块不渲染**，而不是留一个点了报错的空壳。原因是
  协作是可选能力，不该让它成为任务详情页的可用性前提。

  文案沿用本 feature 既有约定：tasks 整个目录都是硬编码中文（TaskDetailView、
  TasksView 均未接 i18n），这里跟着走而不是半个 feature 两种风格。
-->
<template>
  <section v-if="available" class="collab" data-testid="task-collaboration">
    <header class="collab-head">
      <span class="material-symbols-outlined" aria-hidden="true">group</span>
      <h2>协作</h2>
      <span class="collab-count">{{ participants.length }}</span>
    </header>

    <!-- 待审批（agent 上游请求的任务域只读视图） -->
    <div v-if="approvals.length" class="approvals" data-testid="collab-approvals">
      <h3 class="stream-title">
        待审批 <span v-if="pendingApprovals" class="badge">{{ pendingApprovals }}</span>
      </h3>
      <ul class="approval-list">
        <li v-for="a in approvals" :key="a.requestId" class="approval" :class="a.state">
          <span class="approval-kind">{{ a.kind === 'question' ? '问答' : '权限' }}</span>
          <span class="approval-state">{{ stateText(a.state) }}</span>
        </li>
      </ul>
    </div>

    <!-- 子任务 / 目标进度 -->
    <div class="children" data-testid="collab-children">
      <h3 class="stream-title">子任务</h3>
      <div v-if="progress && progress.total > 0" class="progress">
        <div class="bar" role="progressbar" :aria-valuenow="progress.percent" aria-valuemin="0" aria-valuemax="100">
          <span class="fill" :style="{ width: progress.percent + '%' }"></span>
        </div>
        <span class="progress-text">
          {{ progress.done }}/{{ progress.total }}
          <em v-if="progress.blocked">· {{ progress.blocked }} 阻塞</em>
        </span>
      </div>
      <ul v-if="children.length" class="child-list">
        <li v-for="c in children" :key="c.id" class="child" :class="c.status">
          <span class="child-status">{{ statusText(c.status) }}</span>
          <span class="child-title">{{ c.title }}</span>
        </li>
      </ul>
      <p v-else-if="!progress || progress.total === 0" class="empty">还没有子任务</p>

      <form class="subtask" @submit.prevent="addSubtask">
        <input
          v-model="subtaskTitle"
          type="text"
          placeholder="新增子任务…"
          aria-label="新增子任务"
          data-testid="collab-subtask-input"
        />
        <button type="submit" :disabled="busy || !subtaskTitle.trim()">
          <span class="material-symbols-outlined" aria-hidden="true">add</span>
          添加
        </button>
      </form>
    </div>

    <!-- 参与者 -->
    <ul v-if="participants.length" class="participants" data-testid="collab-participants">
      <li v-for="p in participants" :key="p.userId" class="participant">
        <span class="avatar">{{ initial(p.userId) }}</span>
        <span class="who">{{ p.userId }}</span>
        <span class="role" :class="p.role">{{ roleText(p.role) }}</span>
      </li>
    </ul>
    <p v-else class="empty">还没有其他参与者</p>

    <!-- 委派 -->
    <form class="delegate" @submit.prevent="delegate">
      <input
        v-model="delegateUser"
        type="text"
        placeholder="用户 ID"
        aria-label="委派给"
        data-testid="collab-delegate-input"
      />
      <select v-model="delegateRole" aria-label="角色" data-testid="collab-delegate-role">
        <option value="assignee">执行</option>
        <option value="owner">负责人</option>
        <option value="watcher">关注</option>
      </select>
      <button type="submit" :disabled="busy || !delegateUser.trim()">
        <span class="material-symbols-outlined" aria-hidden="true">send</span>
        委派
      </button>
    </form>

    <!-- 活动流 -->
    <h3 class="stream-title">动态</h3>
    <ul v-if="events.length" class="stream" data-testid="collab-activity">
      <li v-for="e in events" :key="e.eventId" class="event" :class="e.eventType">
        <div class="event-body">
          <p class="event-line">
            <strong>{{ e.actorUserId || '系统' }}</strong>
            <span class="verb">{{ eventText(e) }}</span>
          </p>
          <p v-if="e.payload?.comment" class="event-comment">{{ e.payload.comment }}</p>
          <time class="when">{{ formatTime(e.createdAt) }}</time>
        </div>
      </li>
    </ul>
    <p v-else class="empty">还没有动态</p>

    <form class="comment" @submit.prevent="comment">
      <input
        v-model="commentText"
        type="text"
        placeholder="写评论…"
        aria-label="评论"
        data-testid="collab-comment-input"
      />
      <button type="submit" :disabled="busy || !commentText.trim()">
        <span class="material-symbols-outlined" aria-hidden="true">comment</span>
        发送
      </button>
    </form>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  api,
  type GoalProgress,
  type Task,
  type TaskApproval,
  type TaskParticipant,
  type TaskParticipantRole,
  type WorkItemEvent,
} from '../../api/client'
import { useToast } from '../../composables/useToast'

const props = defineProps<{ taskId: string }>()

const toast = useToast()

const participants = ref<TaskParticipant[]>([])
const events = ref<WorkItemEvent[]>([])
const children = ref<Task[]>([])
const progress = ref<GoalProgress | null>(null)
const approvals = ref<TaskApproval[]>([])
const pendingApprovals = ref(0)
const available = ref(true)
const busy = ref(false)

const delegateUser = ref('')
const delegateRole = ref<TaskParticipantRole>('assignee')
const commentText = ref('')
const subtaskTitle = ref('')

/**
 * 一次性生成评论的 eventId，让移动端重试不会发出两条一样的评论。
 * 服务端要求 client- 前缀，与它自己生成的 eventId 空间隔开。
 */
function newEventId(): string {
  const rand = Math.random().toString(36).slice(2, 10)
  return `client-${Date.now().toString(36)}-${rand}`
}

onMounted(load)

async function load() {
  if (!props.taskId) {
    available.value = false
    return
  }
  try {
    // 参与者决定谁能看；其余三块是给人看的，并行拉取。
    const [p, e, c, a] = await Promise.all([
      api.getTaskParticipants(props.taskId),
      api.getTaskActivity(props.taskId),
      api.getTaskChildren(props.taskId),
      api.getTaskApprovals(props.taskId),
    ])
    participants.value = p
    events.value = e
    children.value = c.children || []
    progress.value = c.progress || null
    approvals.value = a.approvals || []
    pendingApprovals.value = a.pending || 0
  } catch (err) {
    // 403（不是参与者）与 503（无 PG）都意味着这块内容当前不可用，
    // 区别对用户没有意义，所以统一隐藏而不是弹错误。
    console.debug('collaboration unavailable:', err)
    available.value = false
  }
}

// 委派 / 评论 / 子任务都要重算进度，所以共用一次刷新。
async function refreshProgress() {
  const c = await api.getTaskChildren(props.taskId)
  children.value = c.children || []
  progress.value = c.progress || null
}

async function addSubtask() {
  const title = subtaskTitle.value.trim()
  if (!title || busy.value) return
  busy.value = true
  try {
    await api.createSubtask(props.taskId, { title })
    subtaskTitle.value = ''
    await refreshProgress()
  } catch (err) {
    console.error('Failed to add sub-task:', err)
    toast.error('添加子任务失败，请重试')
  } finally {
    busy.value = false
  }
}

async function delegate() {
  const userId = delegateUser.value.trim()
  if (!userId || busy.value) return
  busy.value = true
  try {
    const res = await api.delegateTask(props.taskId, userId, delegateRole.value)
    participants.value = res.participants
    delegateUser.value = ''
    // 活动流里已经多了 assigned 条目，重新拉一次比本地拼一条更省心，
    // 也避免本地拼的形状和服务端不一致。
    events.value = await api.getTaskActivity(props.taskId)
  } catch (err) {
    console.error('Failed to delegate task:', err)
    toast.error('委派失败，请重试')
  } finally {
    busy.value = false
  }
}

async function comment() {
  const text = commentText.value.trim()
  if (!text || busy.value) return
  busy.value = true
  try {
    const event = await api.postTaskComment(props.taskId, text, newEventId())
    events.value = [event, ...events.value]
    commentText.value = ''
  } catch (err) {
    console.error('Failed to post comment:', err)
    toast.error('评论失败，请重试')
  } finally {
    busy.value = false
  }
}

function initial(userId: string): string {
  return (userId || '?').trim().charAt(0).toUpperCase()
}

function roleText(role: TaskParticipantRole): string {
  return { owner: '负责人', assignee: '执行', watcher: '关注' }[role] ?? role
}

function eventText(e: WorkItemEvent): string {
  switch (e.eventType) {
    case 'comment':
      return '评论了'
    case 'assigned':
      return `委派给 ${e.payload?.userId ?? ''}`
    case 'status_changed':
      return `把状态改为 ${e.payload?.status ?? ''}`
    case 'completed':
      return '完成了'
    case 'created':
      return '创建了工作项'
    case 'due_changed':
      return '改了截止时间'
    case 'reminded':
      return '被提醒'
    case 'child_added':
      return `新增了子任务 ${e.payload?.childTitle ?? ''}`
    default:
      return e.eventType
  }
}

function statusText(s?: string): string {
  return { active: '进行中', blocked: '阻塞', completed: '已完成', accepted: '已验收' }[s ?? ''] ?? s ?? ''
}

function stateText(s?: string): string {
  return {
    pending: '待处理',
    approved: '已通过',
    rejected: '已拒绝',
    answered: '已回答',
    expired: '已过期',
    failed: '失败',
    resolved: '已解决',
  }[s ?? ''] ?? s ?? ''
}

function formatTime(sec?: number): string {
  if (!sec) return ''
  return new Date(sec * 1000).toLocaleString('zh-CN', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}
</script>

<style scoped>
.collab {
  margin: var(--space-3) 0;
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 12px);
  background: var(--bg-card);
}

.collab-head {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-bottom: var(--space-2);
}

.collab-head h2 {
  font-size: 14px;
  font-weight: 600;
  margin: 0;
}

.collab-head .material-symbols-outlined {
  font-size: 18px;
  color: var(--brand-primary, #4c8dff);
}

.collab-count {
  font-size: 12px;
  color: var(--text-tertiary, #888);
}

.participants {
  list-style: none;
  margin: 0 0 var(--space-2);
  padding: 0;
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.participant {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px 4px 4px;
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  font-size: 12px;
}

.avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--brand-bg, rgba(76, 141, 255, 0.15));
  color: var(--brand-primary, #4c8dff);
  font-size: 11px;
  font-weight: 600;
}

.who {
  color: var(--text-primary);
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.role {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: var(--radius-full);
  background: var(--bg-hover, rgba(0, 0, 0, 0.05));
  color: var(--text-tertiary, #888);
}

.role.owner {
  background: var(--brand-bg, rgba(76, 141, 255, 0.15));
  color: var(--brand-primary, #4c8dff);
}

.delegate,
.comment {
  display: flex;
  gap: var(--space-1);
  margin-bottom: var(--space-2);
}

.delegate input,
.comment input,
.delegate select {
  flex: 1;
  min-width: 0;
  padding: 6px 8px;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 6px);
  background: var(--bg-base);
  color: var(--text-primary);
}

.delegate select {
  flex: 0 0 auto;
}

.delegate button,
.comment button {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 6px 10px;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 6px);
  background: var(--bg-base);
  color: var(--text-secondary);
  cursor: pointer;
}

.delegate button:disabled,
.comment button:disabled {
  opacity: 0.5;
  cursor: default;
}

.delegate button .material-symbols-outlined,
.comment button .material-symbols-outlined {
  font-size: 14px;
}

.stream-title {
  font-size: 13px;
  font-weight: 600;
  margin: var(--space-2) 0 var(--space-1);
}

.stream {
  list-style: none;
  margin: 0 0 var(--space-2);
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.event {
  display: flex;
  gap: 6px;
  font-size: 12px;
  color: var(--text-secondary);
}

.event-body {
  min-width: 0;
}

.event-line {
  margin: 0;
}

.event-comment {
  margin: 2px 0 0;
  padding: 6px 8px;
  border-radius: var(--radius-sm, 6px);
  background: var(--bg-hover, rgba(0, 0, 0, 0.04));
  color: var(--text-primary);
  white-space: pre-wrap;
  word-break: break-word;
}

.when {
  font-size: 11px;
  color: var(--text-tertiary, #888);
}

.empty {
  margin: 0 0 var(--space-2);
  font-size: 12px;
  color: var(--text-tertiary, #888);
}

/* 子任务 / 进度 */
.children {
  margin: var(--space-2) 0;
}

.progress {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-bottom: var(--space-1);
}

.bar {
  flex: 1;
  height: 6px;
  border-radius: var(--radius-full);
  background: var(--bg-hover, rgba(0, 0, 0, 0.08));
  overflow: hidden;
}

.fill {
  display: block;
  height: 100%;
  background: var(--brand-primary, #4c8dff);
  transition: width 0.2s ease;
}

.progress-text {
  font-size: 11px;
  color: var(--text-tertiary, #888);
  white-space: nowrap;
}

.progress-text em {
  font-style: normal;
  color: #e8a33d;
}

.child-list {
  list-style: none;
  margin: 0 0 var(--space-1);
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.child {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: 12px;
}

.child-status {
  flex: 0 0 auto;
  font-size: 11px;
  padding: 1px 6px;
  border-radius: var(--radius-full);
  background: var(--bg-hover, rgba(0, 0, 0, 0.05));
  color: var(--text-tertiary, #888);
}

.child.completed .child-status,
.child.accepted .child-status {
  background: var(--brand-bg, rgba(76, 141, 255, 0.15));
  color: var(--brand-primary, #4c8dff);
}

.child.blocked .child-status {
  background: rgba(232, 163, 61, 0.15);
  color: #e8a33d;
}

.child-title {
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.subtask {
  display: flex;
  gap: var(--space-1);
}

.subtask input {
  flex: 1;
  min-width: 0;
  padding: 6px 8px;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 6px);
  background: var(--bg-base);
  color: var(--text-primary);
}

.subtask button {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 6px 10px;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 6px);
  background: var(--bg-base);
  color: var(--text-secondary);
  cursor: pointer;
}

.subtask button:disabled {
  opacity: 0.5;
  cursor: default;
}

.subtask button .material-symbols-outlined {
  font-size: 14px;
}

/* 审批 */
.approvals {
  margin: var(--space-2) 0;
}

.badge {
  display: inline-block;
  margin-left: 4px;
  padding: 0 6px;
  border-radius: var(--radius-full);
  background: #e8a33d;
  color: #fff;
  font-size: 11px;
  font-weight: 600;
}

.approval-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.approval {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: 12px;
}

.approval-kind {
  padding: 1px 6px;
  border-radius: var(--radius-sm, 4px);
  background: var(--bg-hover, rgba(0, 0, 0, 0.05));
  color: var(--text-secondary);
}

.approval.pending .approval-state {
  color: #e8a33d;
}

.approval-state {
  color: var(--text-tertiary, #888);
}
</style>
