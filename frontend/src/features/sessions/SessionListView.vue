<template>
  <div class="sessions-page">
    <ScrollChromePortal>
      <div class="toolbar">
        <div class="search-bar">
          <input
            v-model="searchQuery"
            type="search"
            placeholder="搜索会话..."
            @input="handleSearch"
          />
        </div>
        <select v-model="selectedInstanceId" class="instance-filter" @change="handleInstanceChange">
          <option value="">所有实例</option>
          <option v-for="inst in instances" :key="inst.id" :value="inst.id">
            {{ inst.name }}
          </option>
        </select>
      </div>

      <!-- 活跃 / 归档分区切换（与 Acc 任务视角对齐：默认只看活跃，归档需显式切换） -->
      <div class="mode-switch" role="tablist" aria-label="会话分区">
        <button
          type="button"
          class="mode-tab"
          role="tab"
          :class="{ active: listMode === 'active' }"
          :aria-selected="listMode === 'active'"
          @click="listMode = 'active'"
        >
          活跃<span class="mode-count">{{ activeCount }}</span>
        </button>
        <button
          type="button"
          class="mode-tab"
          role="tab"
          :class="{ active: listMode === 'archived' }"
          :aria-selected="listMode === 'archived'"
          @click="listMode = 'archived'"
        >
          归档<span class="mode-count">{{ archivedCount }}</span>
        </button>
      </div>
    </ScrollChromePortal>

    <PullToRefresh :on-refresh="handleRefresh" class="list-scroll">
      <!-- 加载状态 -->
      <div v-if="loading" class="state-wrap">
        <Skeleton :count="4" :rows="2" />
      </div>

      <!-- 错误提示 -->
      <EmptyState
        v-else-if="error"
        icon="⚠️"
        :title="error"
        hint="请检查网络连接后重试"
        action-label="重试"
        variant="inline"
        @action="loadSessions"
      />

      <!-- 会话列表 -->
      <template v-else>
        <EmptyState
          v-if="filteredSessions.length === 0"
          icon="💬"
          :title="listMode === 'archived' ? '归档区暂无会话' : '暂无会话'"
          :hint="listMode === 'archived' ? '左滑会话卡片选「归档」，归档的会话会收纳到这里' : '在 AI 页面开始新对话，或切换实例筛选'"
          size="sm"
          variant="inline"
        />

        <div v-else class="session-list">
          <SwipeableListItem
            v-for="session in filteredSessions"
            :key="session.id"
            :right-actions="getSwipeActions(session)"
          >
            <div class="session-card" @click="openSessionDetail(session)">
              <div class="session-header">
                <h3 class="session-title">{{ session.title }}</h3>
                <span
                  v-if="listMode === 'archived'"
                  class="status-badge archived"
                >
                  已归档
                </span>
                <span v-else :class="['status-badge', session.status]">
                  {{ getStatusText(session.status) }}
                </span>
              </div>
              <p class="session-id">{{ session.id.slice(0, 20) }}…</p>
            </div>
          </SwipeableListItem>
        </div>

        <!-- 分页（仅活跃分区；归档区跟随当前页数据的本地过滤） -->
        <div v-if="listMode === 'active' && total > limit" class="pagination">
          <button class="page-btn" :disabled="offset === 0" @click="prevPage">上一页</button>
          <span class="page-info">
            {{ Math.floor(offset / limit) + 1 }} / {{ Math.ceil(total / limit) }}
          </span>
          <button class="page-btn" :disabled="offset + limit >= total" @click="nextPage">下一页</button>
        </div>
      </template>
    </PullToRefresh>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onActivated } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { Skeleton, EmptyState, PullToRefresh, SwipeableListItem, type SwipeAction } from '@/components'
import ScrollChromePortal from '@/components/layout/ScrollChromePortal.vue'
import { useConfirm } from '@/composables/useConfirm'
import {
  readArchivedIds,
  setSessionArchived,
  writeArchivedIds,
  parseArchivedIds,
} from './sessionArchive'

interface Session {
  id: string
  title: string
  status: string
}

interface Instance {
  id: string
  name: string
  baseURL: string
}

/**
 * 会话两大分区（活跃/归档，与 Acc 任务视角对齐）：
 *   - 归档是设备侧列表元数据（OpenCode 上游无 archive 写接口——沿 sessionArchive.ts
 *     既有裁决，P2 E5-S4），按 workspace+instance 分区存 localStorage，fail-open；
 *   - 旧全局 key `archived_session_ids`（无分区）在此做一次性合并迁移：集合并集
 *     幂等，旧 key 保留供其他 scope 后续合并，不删除。
 */
const LEGACY_ARCHIVE_KEY = 'archived_session_ids'

const router = useRouter()
const auth = useAuthStore()
const { confirm } = useConfirm()

const sessions = ref<Session[]>([])
const instances = ref<Instance[]>([])
const loading = ref(false)
const error = ref('')
const searchQuery = ref('')
const selectedInstanceId = ref('')
const offset = ref(0)
const limit = ref(20)
const total = ref(0)
const archivedIds = ref<Set<string>>(new Set())
const listMode = ref<'active' | 'archived'>('active')

function archiveScope() {
  return {
    workspaceId: auth.workspaceId,
    instanceId: selectedInstanceId.value || 'all',
  }
}

function loadArchivedIds() {
  try {
    // 旧全局归档 key 合并迁移（幂等并集；见 LEGACY_ARCHIVE_KEY 注释）
    const legacy = parseArchivedIds(localStorage.getItem(LEGACY_ARCHIVE_KEY))
    const current = readArchivedIds(localStorage, archiveScope())
    if (legacy.size > 0) {
      const merged = new Set([...current, ...legacy])
      writeArchivedIds(localStorage, archiveScope(), merged)
      archivedIds.value = merged
      return
    }
    archivedIds.value = current
  } catch {
    archivedIds.value = new Set()
  }
}

const activeSessions = computed(() =>
  sessions.value.filter((s) => !archivedIds.value.has(s.id)),
)

const archivedSessions = computed(() =>
  sessions.value.filter((s) => archivedIds.value.has(s.id)),
)

function applySearch(list: Session[]): Session[] {
  if (!searchQuery.value) return list
  const query = searchQuery.value.toLowerCase()
  return list.filter(
    (s) =>
      s.title.toLowerCase().includes(query) ||
      s.id.toLowerCase().includes(query),
  )
}

const filteredSessions = computed(() =>
  listMode.value === 'archived'
    ? applySearch(archivedSessions.value)
    : applySearch(activeSessions.value),
)

const activeCount = computed(() => activeSessions.value.length)
const archivedCount = computed(() => archivedSessions.value.length)

async function loadInstances() {
  try {
    const data = await api.getInstances()
    instances.value = (data || []).map((i: any) => ({
      id: i.id,
      name: i.displayName || i.name || i.id,
      baseURL: i.baseURL || i.apiBaseURL || '',
    }))
  } catch (err: any) {
    console.error('Failed to load instances:', err)
  }
}

async function loadSessions() {
  loading.value = true
  error.value = ''
  try {
    const instId = selectedInstanceId.value || undefined
    const data = await api.getAllSessions(instId, limit.value, offset.value)
    sessions.value = (data.sessions || []).map((s: any) => ({
      id: s.id || s.ID || '',
      title: s.title || s.Title || '未命名会话',
      status: s.status || s.Status || 'idle',
    }))
    total.value = data.total || 0
  } catch (err: any) {
    error.value = err.message || '加载会话失败'
  } finally {
    loading.value = false
  }
}

async function handleRefresh() {
  offset.value = 0
  await loadSessions()
}

function handleSearch() {
  /* 客户端过滤 */
}

function handleInstanceChange() {
  offset.value = 0
  loadArchivedIds() // 归档分区按实例切换，重读对应 scope
  loadSessions()
}

function prevPage() {
  if (offset.value >= limit.value) {
    offset.value -= limit.value
    loadSessions()
  }
}

function nextPage() {
  if (offset.value + limit.value < total.value) {
    offset.value += limit.value
    loadSessions()
  }
}

function openSessionDetail(session: Session) {
  router.push({
    path: `/sessions/${session.id}`,
    query: {
      instance_id: selectedInstanceId.value,
      title: session.title || '',
    },
  })
}

function archiveSession(session: Session) {
  archivedIds.value = setSessionArchived(localStorage, archiveScope(), session.id, true)
}

/** 归档分区内的恢复动作：回到活跃分区（服务端会话状态不受影响）。 */
function unarchiveSession(session: Session) {
  archivedIds.value = setSessionArchived(localStorage, archiveScope(), session.id, false)
}

async function deleteSession(session: Session) {
  const instId = selectedInstanceId.value
  if (!instId) {
    alert('请先选择实例再删除会话')
    return
  }
  if (!(await confirm({ title: '删除会话', message: `确定删除会话「${session.title}」？`, confirmText: '删除', danger: true }))) return
  try {
    await api.deleteSession(session.id, instId)
    sessions.value = sessions.value.filter((s) => s.id !== session.id)
    total.value = Math.max(0, total.value - 1)
  } catch (err: any) {
    alert('删除失败: ' + (err.message || '未知错误'))
  }
}

function getSwipeActions(session: Session): SwipeAction[] {
  // 活跃分区：归档/删除；归档分区：恢复/删除（破坏性动作保留确认）
  if (listMode.value === 'archived') {
    return [
      {
        id: 'unarchive',
        icon: '📦',
        label: '恢复',
        type: 'warning',
        onAction: () => unarchiveSession(session),
      },
      {
        id: 'delete',
        icon: '🗑',
        label: '删除',
        type: 'danger',
        onAction: () => deleteSession(session),
      },
    ]
  }
  return [
    {
      id: 'archive',
      icon: '📦',
      label: '归档',
      type: 'warning',
      onAction: () => archiveSession(session),
    },
    {
      id: 'delete',
      icon: '🗑',
      label: '删除',
      type: 'danger',
      onAction: () => deleteSession(session),
    },
  ]
}

function getStatusText(status: string): string {
  const statusMap: Record<string, string> = {
    active: '进行中',
    inactive: '已归档',
    empty: '空会话',
    idle: '空闲',
    streaming: '生成中',
  }
  return statusMap[status] || status
}

onMounted(() => {
  loadArchivedIds()
  loadInstances()
  loadSessions()
})

onActivated(() => {
  loadArchivedIds()
  loadSessions()
})
</script>

<style scoped>
.sessions-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.toolbar {
  display: flex;
  gap: var(--space-2);
  padding: var(--space-3);
}

/* 活跃/归档分区切换：分段控件，热区 ≥44px */
.mode-switch {
  display: flex;
  gap: var(--space-2);
  padding: 0 var(--space-3) var(--space-2);
}
.mode-tab {
  flex: 1;
  min-height: 44px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1-5, 6px);
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  background: var(--bg-subtle);
  color: var(--text-secondary);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
  cursor: pointer;
}
.mode-tab.active {
  background: var(--brand-primary);
  border-color: var(--brand-primary);
  color: var(--text-inverse);
  font-weight: var(--font-weight-semibold);
}
.mode-tab:active {
  transform: scale(0.98);
}
.mode-count {
  min-width: 18px;
  padding: 0 5px;
  border-radius: var(--radius-full);
  background: var(--bg-card);
  color: var(--text-secondary);
  font-size: var(--text-xs);
  line-height: 18px;
  font-variant-numeric: tabular-nums;
}
.mode-tab.active .mode-count {
  background: rgba(255, 255, 255, 0.22);
  color: var(--text-inverse);
}

.list-scroll {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.search-bar {
  flex: 1;
}

.search-bar input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  font-size: var(--text-base);
  background: var(--bg-card);
  color: var(--text-primary);
  outline: none;
}

.search-bar input:focus {
  border-color: var(--brand-primary);
}

.instance-filter {
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
  background: var(--bg-card);
  color: var(--text-primary);
  cursor: pointer;
}

.state-wrap {
  padding: var(--space-2) 0;
}

.session-list {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-list-gap);
}

.session-card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--spacing-card-padding);
  cursor: pointer;
  transition: background 120ms;
  min-height: 52px;
}

.session-card:active {
  background: var(--bg-subtle);
}

.session-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--space-2);
}

.session-title {
  margin: 0;
  font-size: var(--text-md);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.status-badge {
  padding: 2px var(--space-2);
  border-radius: var(--radius-full);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  white-space: nowrap;
  flex-shrink: 0;
}

.status-badge.active,
.status-badge.streaming {
  background: var(--success-bg);
  color: var(--success);
}

.status-badge.inactive {
  background: var(--danger-bg);
  color: var(--danger);
}

.status-badge.empty,
.status-badge.idle {
  background: var(--warning-bg);
  color: var(--warning);
}

/* 归档分区徽标（设备侧归档态，非上游 session status） */
.status-badge.archived {
  background: var(--bg-subtle);
  color: var(--text-muted);
  border: 1px solid var(--border);
}

.session-id {
  margin: var(--space-1) 0 0;
  font-size: var(--text-xs);
  color: var(--text-muted);
  font-family: monospace;
}

.pagination {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-3);
  padding: var(--spacing-card-padding);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.page-btn {
  padding: var(--space-1) var(--space-3);
  background: var(--brand-primary);
  color: var(--text-inverse);
  border: none;
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
}

.page-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.page-info {
  color: var(--text-secondary);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
}
</style>
