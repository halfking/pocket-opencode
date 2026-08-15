<template>
  <div class="sessions-page" :class="{ embedded }">
    <!-- 顶部工具栏 -->
    <div class="toolbar">
      <div class="search-bar">
        <input 
          v-model="searchQuery" 
          type="search" 
          placeholder="搜索会话..." 
          @input="handleSearch"
        />
      </div>
      <select v-model="selectedInstanceId" @change="handleInstanceChange" class="instance-filter">
        <option value="">所有实例</option>
        <option v-for="inst in instances" :key="inst.id" :value="inst.id">
          {{ inst.name }}
        </option>
      </select>
    </div>

    <div class="view-tabs" role="tablist" aria-label="会话视图">
      <button
        type="button"
        role="tab"
        :aria-selected="!showArchived"
        :class="{ active: !showArchived }"
        @click="showArchived = false"
      >
        当前
      </button>
      <button
        type="button"
        role="tab"
        :aria-selected="showArchived"
        :class="{ active: showArchived }"
        @click="showArchived = true"
      >
        已归档 <span v-if="archivedCount">{{ archivedCount }}</span>
      </button>
      <span v-if="connectivity.online === false" class="offline-search" role="status">
        {{ usingOfflineCache ? '离线 · 使用加密本地缓存' : '离线 · 暂无可用会话缓存' }}
      </span>
      <span v-if="searching" class="searching" role="status">搜索中…</span>
    </div>

    <!-- 加载状态：已有数据时降级为内联刷新，不整屏替换列表（08 §4.1）。 -->
    <div v-if="loading && sessions.length === 0" class="loading">
      <div class="spinner"></div>
      <p>加载会话中...</p>
    </div>

    <!-- 错误提示 -->
    <ErrorState
      v-else-if="error && sessions.length === 0"
      icon="⚠️"
      title="加载失败"
      :message="error"
      retry-label="重试"
      @retry="retryCurrent"
    />

    <!-- 会话列表 -->
    <PullToRefresh
      v-else
      class="session-list-wrap"
      :on-refresh="reloadSessions"
    >
      <div v-if="error" class="inline-error" role="alert">
        <span>{{ error }}</span>
        <button type="button" @click="retryCurrent">重试</button>
      </div>
      <div v-else-if="loading" class="inline-refreshing" role="status">刷新中…</div>
      <div class="session-list">
        <EmptyState
          v-if="pagedSessions.length === 0"
          icon="💬"
                    :title="searchQuery ? '无匹配结果' : showArchived ? '暂无归档会话' : '暂无会话'"
          :message="
            searchQuery
              ? selectedInstanceId
                ? `未找到包含 “${searchQuery}” 的会话`
                : '请先选择实例以全局搜索；当前仅在已加载的会话中过滤'
              : showArchived
                ? '左滑当前会话可将它收进归档'
                : '选择一个实例开始新的 AI 会话'
          "
          :hint="showArchived ? '归档只影响当前设备的列表显示，不删除服务端会话' : '在 AI 页面点击 + 新任务，或在下方选择实例'"
        />

        <SwipeableListItem
          v-for="session in pagedSessions"
          :key="session.id"
          class="session-card"
          :right-actions="getSwipeActions(session)"
        >
          <div @click="openSessionDetail(session)">
            <div class="session-header">
              <h3 class="session-title">{{ session.title }}</h3>
              <span :class="['status-badge', session.status]">
                {{ getStatusText(session.status) }}
              </span>
            </div>
            <p class="session-id">ID: {{ session.id }}</p>
            <div class="session-footer">
              <time v-if="session.timeUpdatedMs" :datetime="new Date(session.timeUpdatedMs).toISOString()">
                {{ formatUpdatedAt(session.timeUpdatedMs) }}
              </time>
              <div class="session-actions">
                <button
                  type="button"
                  class="archive-btn"
                  :aria-label="showArchived ? `恢复 ${session.title}` : `归档 ${session.title}`"
                  @click.stop="showArchived ? restoreSession(session) : archiveSession(session)"
                >
                  <span class="material-symbols-outlined" aria-hidden="true">
                    {{ showArchived ? 'unarchive' : 'archive' }}
                  </span>
                </button>
                <button
                @click.stop="attachToTask(session)"
                class="attach-btn"
                :disabled="attaching === session.id"
              >
                {{ attaching === session.id ? '附加中...' : '附加到任务' }}
                </button>
              </div>
            </div>
          </div>
        </SwipeableListItem>
      </div>
    </PullToRefresh>

    <!-- 分页 -->
    <div v-if="paginationTotal > limit" class="pagination">
      <button 
        @click="prevPage" 
        :disabled="offset === 0"
        class="page-btn"
      >
        上一页
      </button>
      <span class="page-info">
        {{ Math.floor(offset / limit) + 1 }} / {{ Math.ceil(paginationTotal / limit) }}
      </span>
      <button 
        @click="nextPage" 
        :disabled="offset + limit >= paginationTotal"
        class="page-btn"
      >
        下一页
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onActivated, onBeforeUnmount, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { useToast } from '@/composables/useToast'
import { useAuthStore } from '@/stores/auth'
import { useConnectivityStore } from '@/stores/connectivity'
import { isLobsterReady } from '@/native/lobster-init'
import { localDB, localDbAsSql } from '@/native/local-db'
import { SqliteMobileSyncStore } from '@/native/mobileSync'
import {
  readArchivedIds,
  setSessionArchived,
  type ArchiveScope,
} from './sessionArchive'
import EmptyState from '@/components/base/EmptyState.vue'
import ErrorState from '@/components/base/ErrorState.vue'
import PullToRefresh from '@/components/interactive/PullToRefresh.vue'
import SwipeableListItem, { type SwipeAction } from '@/components/interactive/SwipeableListItem.vue'

interface Session {
  id: string
  title: string
  status: string
  timeUpdatedMs?: number
}

interface Instance {
  id: string
  name: string
  baseURL: string
}

const props = withDefaults(defineProps<{
  embedded?: boolean
}>(), {
  embedded: false,
})

const emit = defineEmits<{
  select: [session: Session, instanceId: string]
  contextChange: [instanceId: string]
}>()

const router = useRouter()
const route = useRoute()
const toast = useToast()
const auth = useAuthStore()
const connectivity = useConnectivityStore()

// 状态
const sessions = ref<Session[]>([])
const allSessions = ref<Session[]>([])
const instances = ref<Instance[]>([])
const loading = ref(false)
const error = ref('')
const searchQuery = ref('')
const selectedInstanceId = ref(props.embedded ? String(route.query.instance_id || '') : '')
const attaching = ref('')
const offset = ref(0)
const limit = ref(20)
const total = ref(0)
const searching = ref(false)
const showArchived = ref(false)
const archivedIds = ref<Set<string>>(new Set())
const usingOfflineCache = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | null = null
let searchSeq = 0

const archiveScope = computed<ArchiveScope>(() => ({
  workspaceId: auth.workspaceId || 'default',
  instanceId: selectedInstanceId.value || 'all',
}))

const archivedCount = computed(() => allSessions.value.filter((session) => archivedIds.value.has(session.id)).length)

// 服务端搜索结果或当前页数据，最后按设备本地归档视图过滤。
const filteredSessions = computed(() => {
  return sessions.value.filter((session) => archivedIds.value.has(session.id) === showArchived.value)
})

// 客户端分页：offset 钳制到最后一页页首，归档/切页签后条目变少也不会
// 落进空区间或让分页条整体消失。
function clampOffset(totalItems: number): number {
  const lastPageStart = Math.max(0, (Math.ceil(totalItems / limit.value) - 1) * limit.value)
  return Math.min(offset.value, lastPageStart)
}

const pagedSessions = computed(() => {
  if (!selectedInstanceId.value) return filteredSessions.value
  return filteredSessions.value.slice(
    clampOffset(filteredSessions.value.length),
    clampOffset(filteredSessions.value.length) + limit.value,
  )
})

const paginationTotal = computed(() => selectedInstanceId.value ? filteredSessions.value.length : total.value)

// 加载实例列表
async function loadInstances() {
  try {
    const data = await api.getInstances()
    // api.getInstances 返回 client.ts 的 Instance[]（含 displayName/environment 等）
    // 映射为本地 SessionListView 用的 {id, name, baseURL} 形状
    instances.value = (data || []).map((i: any) => ({
      id: i.id,
      name: i.displayName || i.name || i.id,
      baseURL: i.baseURL || i.apiBaseURL || '',
    }))
  } catch (err: any) {
    console.error('Failed to load instances:', err)
  }
}

async function loadOfflineSessions(instanceId: string): Promise<Session[]> {
  usingOfflineCache.value = false
  if (!instanceId || !isLobsterReady()) return []
  try {
    const store = new SqliteMobileSyncStore(localDbAsSql(localDB))
    const rows = await store.listCachedSessions(auth.workspaceId || 'default', instanceId)
    usingOfflineCache.value = rows.length > 0
    return rows.map((row) => ({
      id: row.serverId || row.id,
      title: row.title,
      status: row.status,
      timeUpdatedMs: row.updatedAt,
    }))
  } catch {
    // 读取失败要有可执行的下一步（08 §4.1）：无数据时给错误+重试，而不是
    // 静默显示"暂无缓存"。
    if (sessions.value.length === 0) error.value = '本地会话缓存读取失败'
    return []
  }
}

function mapSession(value: unknown): Session {
  const s = (value && typeof value === 'object' ? value : {}) as Record<string, unknown>
  const nestedTime = s.time && typeof s.time === 'object' ? s.time as Record<string, unknown> : {}
  return {
    id: String(s.id || s.ID || ''),
    title: String(s.title || s.Title || ''),
    status: String(s.status || s.Status || 'idle'),
    timeUpdatedMs: Number(s.timeUpdatedMs || s.TimeUpdated || nestedTime.updated || 0) || undefined,
  }
}

function reloadArchivedIds(): void {
  archivedIds.value = readArchivedIds(localStorage, archiveScope.value)
}

// 加载会话列表
async function loadSessions() {
  const seq = ++searchSeq
  loading.value = true
  error.value = ''

  try {
    const instId = selectedInstanceId.value
    let rows: Session[]
    let responseTotal: number
    if (instId && !connectivity.online) {
      rows = await loadOfflineSessions(instId)
      responseTotal = rows.length
    } else if (instId) {
      usingOfflineCache.value = false
      const data = await api.getMobileSessions(instId)
      rows = (data.data || []).map(mapSession)
      responseTotal = data.total || rows.length
    } else if (!connectivity.online) {
      // 所有实例视图离线：本地缓存按实例隔离、旧 API 不带实例，无法安全
      // 重建；保留已加载的数据（08 §4.1 offline 不丢旧数据），只更新标志。
      usingOfflineCache.value = false
      rows = allSessions.value
      responseTotal = total.value
      if (rows.length === 0 && seq === searchSeq) {
        error.value = ''
      }
    } else {
      usingOfflineCache.value = false
      const data = await api.getAllSessions(undefined, limit.value, offset.value)
      rows = (data.sessions || []).map(mapSession)
      responseTotal = data.total || rows.length
    }
    if (seq !== searchSeq) return
    allSessions.value = rows
    sessions.value = applyLocalSearch(rows)
    total.value = responseTotal
    reloadArchivedIds()
  } catch (err: any) {
    if (seq === searchSeq) error.value = err.message || '加载会话失败'
  } finally {
    if (seq === searchSeq) loading.value = false
  }
}

function applyLocalSearch(rows: Session[]): Session[] {
  const query = searchQuery.value.trim().toLowerCase()
  if (!query) return rows
  return rows.filter((s) => s.title.toLowerCase().includes(query) || s.id.toLowerCase().includes(query))
}

async function runSearch(): Promise<void> {
  const seq = ++searchSeq
  loading.value = false
  searching.value = false
  const query = searchQuery.value.trim()
  offset.value = 0
  if (!query) {
    await loadSessions()
    return
  }
  const instId = selectedInstanceId.value
  // 搜索 API 需要 instance；离线/全部实例视图回退到当前页本地过滤。
  if (!instId || !connectivity.online) {
    sessions.value = applyLocalSearch(allSessions.value)
    error.value = ''
    return
  }

  searching.value = true
  error.value = ''
  try {
    const data = await api.searchMobileSessions(instId, query)
    if (seq !== searchSeq) return
    const rows = (data.data || []).map(mapSession)
    sessions.value = rows
    // 搜索命中也补进缓存，便于随后断网时继续本地过滤。
    const byId = new Map(allSessions.value.map((s) => [s.id, s]))
    for (const row of rows) byId.set(row.id, row)
    allSessions.value = [...byId.values()]
    total.value = data.total || sessions.value.length
  } catch (err: any) {
    if (seq === searchSeq) error.value = err.message || '搜索会话失败'
  } finally {
    if (seq === searchSeq) searching.value = false
  }
}

function retryCurrent(): void {
  if (searchQuery.value.trim()) void runSearch()
  else void loadSessions()
}

// 350ms 防抖，避免每次键入都请求上游 OpenCode。
function handleSearch() {
  // 输入发生即作废在途请求；网络请求仍延后 350ms，避免逐键请求。
  searchSeq++
  loading.value = false
  searching.value = false
  if (searchTimer !== null) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchTimer = null
    void runSearch()
  }, 350)
}

// 处理实例切换
function handleInstanceChange() {
  offset.value = 0
  searchQuery.value = ''
  showArchived.value = false
  emit('contextChange', selectedInstanceId.value)
  void loadSessions()
}

// 上一页
function prevPage() {
  if (offset.value >= limit.value) {
    offset.value -= limit.value
    loadSessions()
  }
}

// 下一页
function nextPage() {
  if (offset.value + limit.value < paginationTotal.value) {
    offset.value += limit.value
    loadSessions()
  }
}

// 打开会话详情
function openSessionDetail(session: Session) {
  if (!selectedInstanceId.value) {
    toast.warning('请先选择会话所属实例')
    return
  }
  if (props.embedded) {
    emit('select', session, selectedInstanceId.value)
    return
  }
  router.push({
    path: `/sessions/${session.id}`,
    query: {
      instance_id: selectedInstanceId.value,
      title: session.title || '',
    },
  })
}

// 附加到任务
async function attachToTask(session: Session) {
  // 简化版：直接提示选择任务
  const taskId = prompt(`请输入要附加的任务 ID:\n\n会话: ${session.title}`)
  if (!taskId) return
  
  attaching.value = session.id
  try {
    await api.attachSessionToTask(taskId, session.id, selectedInstanceId.value || 'default')
    alert('附加成功!')
  } catch (err: any) {
    alert('附加失败: ' + (err.message || '未知错误'))
  } finally {
    attaching.value = ''
  }
}

// 获取状态文本
function getStatusText(status: string): string {
  const statusMap: Record<string, string> = {
    'active': '进行中',
    'inactive': '已归档',
    'empty': '空会话',
  }
  return statusMap[status] || status
}

// 下拉刷新包装器：保持 offset=0 让刷新回到第一页
async function reloadSessions(): Promise<void> {
  offset.value = 0
  await loadSessions()
}

// 左滑显示的操作按钮。归档是设备本地元数据，不删除服务端会话。
function getSwipeActions(session: Session): SwipeAction[] {
  if (showArchived.value) {
    return [
      {
        id: `restore-${session.id}`,
        icon: '↩',
        label: '恢复',
        type: 'warning',
        onAction: () => restoreSession(session),
      },
    ]
  }
  return [
    {
      id: `archive-${session.id}`,
      icon: '📥',
      label: '归档',
      type: 'warning',
      onAction: () => archiveSession(session),
    },
    {
      id: `delete-${session.id}`,
      icon: '🗑',
      label: '删除',
      type: 'danger',
      onAction: () => deleteSession(session),
    },
  ]
}

async function archiveSession(session: Session): Promise<void> {
  if (!selectedInstanceId.value) {
    toast.warning('请先选择会话所属实例')
    return
  }
  archivedIds.value = setSessionArchived(localStorage, archiveScope.value, session.id, true)
  toast.success(`已归档：${session.title}`)
}

async function restoreSession(session: Session): Promise<void> {
  archivedIds.value = setSessionArchived(localStorage, archiveScope.value, session.id, false)
  toast.success(`已恢复：${session.title}`)
}

async function deleteSession(session: Session): Promise<void> {
  if (!confirm(`确定删除会话 “${session.title}”？`)) return
  // 后端暂无 DELETE /api/sessions/:id；先提示用户，不做虚假删除避免
  // 下一次刷新后条目"复活"造成数据不一致。
  toast.warning('删除功能开发中，请到 OpenCode 实例侧手动删除')
}

function formatUpdatedAt(timestamp: number): string {
  const date = new Date(timestamp)
  const now = new Date()
  if (date.toDateString() === now.toDateString()) {
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }
  return date.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

watch(archiveScope, reloadArchivedIds)

// 页签切换条目数变化，回到第一页，避免落在空区间。
watch(showArchived, () => {
  offset.value = 0
})

// workspace 切换：归档 ID 之外，列表数据本身也要按新 workspace 重载。
watch(() => auth.workspaceId, () => {
  offset.value = 0
  void loadSessions()
})

onMounted(() => {
  reloadArchivedIds()
  void loadInstances()
  void loadSessions()
})

onActivated(() => {
  // 从其他页面返回时重新加载
  void loadSessions()
})

onBeforeUnmount(() => {
  if (searchTimer !== null) clearTimeout(searchTimer)
})
</script>

<style scoped>
.sessions-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--bg-base);
  padding: var(--space-3);
  padding-bottom: 70px;
  overflow-y: auto;
}

.inline-error {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-error);
  border-radius: var(--radius-sm);
  color: var(--color-error);
  font-size: var(--text-base);
}

.inline-error button {
  min-height: 48px;
  color: inherit;
  font-weight: 600;
}

.inline-refreshing {
  min-height: 48px;
  margin-bottom: var(--space-2);
  color: var(--text-muted);
  font-size: var(--text-base);
}

.view-tabs {
  display: flex;
  min-height: 48px;
  align-items: center;
  gap: 4px;
  margin-bottom: var(--space-2);
  border-bottom: 1px solid var(--border);
}

.view-tabs button {
  min-height: 48px;
  padding: 6px 12px;
  border: 0;
  border-bottom: 2px solid transparent;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}

.view-tabs button.active {
  border-bottom-color: var(--brand-primary);
  color: var(--text-primary);
  font-weight: 600;
}

.view-tabs button span {
  margin-left: 3px;
  color: var(--text-muted);
  font-size: var(--text-base);
}

.offline-search,
.searching {
  margin-left: auto;
  color: var(--text-muted);
  font-size: var(--text-base);
}

.offline-search + .searching {
  margin-left: var(--space-2);
}

.sessions-page.embedded {
  height: 100%;
  padding: 0;
  padding-bottom: 0;
}

.toolbar {
  display: flex;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}

.search-bar {
  flex: 1;
}

.search-bar input {
  width: 100%;
  min-height: 48px;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  font-size: var(--text-base);
  background: var(--bg-card);
  color: var(--text-primary);
  box-sizing: border-box;
}

.search-bar input::placeholder {
  color: var(--text-muted);
}

.instance-filter {
  min-height: 48px;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  font-size: var(--text-base);
  background: var(--bg-card);
  color: var(--text-primary);
  cursor: pointer;
}

.loading, .error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 3rem var(--space-3);
  color: var(--text-secondary);
  text-align: center;
}

.spinner {
  width: 32px;
  height: 32px;
  border: 3px solid var(--border);
  border-top-color: var(--brand-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
  margin-bottom: var(--space-3);
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.session-list-wrap {
  flex: 1;
  min-height: 0;
}

.session-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
  padding-bottom: var(--space-4);
}

.session-card {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--spacing-card-padding);
  border: 1px solid var(--border);
  cursor: pointer;
  transition: background 120ms;
}

.session-card:active {
  background: var(--bg-subtle);
}

.session-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: var(--space-1);
}

.session-title {
  margin: 0;
  font-size: var(--text-md);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  flex: 1;
  margin-right: var(--space-2);
}

.status-badge {
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-full);
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  white-space: nowrap;
}

.status-badge.active {
  background: rgba(16, 185, 129, 0.12);
  color: var(--success);
}

.status-badge.inactive {
  background: rgba(239, 68, 68, 0.12);
  color: var(--danger);
}

.status-badge.empty {
  background: rgba(245, 158, 11, 0.12);
  color: var(--warning);
}

.session-id {
  margin: var(--space-1) 0;
  font-size: var(--text-base);
  color: var(--text-muted);
  font-family: monospace;
}

.session-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.session-footer time {
  color: var(--text-muted);
  font-size: var(--text-base);
}

.session-actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.archive-btn {
  display: inline-grid;
  width: 48px;
  height: 48px;
  place-items: center;
  padding: 0;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}

.archive-btn:hover,
.archive-btn:focus-visible {
  background: var(--bg-subtle);
  color: var(--brand-primary);
}

.attach-btn {
  min-height: 48px;
  padding: var(--space-1) var(--space-3);
  background: var(--brand-primary);
  color: var(--text-inverse);
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
  transition: opacity 120ms;
}

.attach-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.pagination {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-3);
  padding: var(--space-3);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.page-btn {
  min-height: 48px;
  padding: var(--space-1) var(--space-3);
  background: var(--brand-primary);
  color: var(--text-inverse);
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
}

.page-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.page-info {
  color: var(--text-secondary);
  font-weight: var(--font-weight-semibold);
  font-size: var(--text-base);
}
</style>
