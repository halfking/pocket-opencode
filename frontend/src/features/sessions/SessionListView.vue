<template>
  <div class="sessions-page">
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

    <!-- 加载状态 -->
    <div v-if="loading" class="loading">
      <div class="spinner"></div>
      <p>加载会话中...</p>
    </div>

    <!-- 错误提示 -->
    <ErrorState
      v-else-if="error"
      icon="⚠️"
      title="加载失败"
      :message="error"
      retry-label="重试"
      @retry="loadSessions"
    />

    <!-- 会话列表 -->
    <PullToRefresh
      v-else
      class="session-list-wrap"
      :on-refresh="reloadSessions"
    >
      <div class="session-list">
        <EmptyState
          v-if="filteredSessions.length === 0"
          icon="💬"
          :title="searchQuery ? '无匹配结果' : '暂无会话'"
          :message="searchQuery ? `未找到包含 “${searchQuery}” 的会话` : '选择一个实例开始新的 AI 会话'"
          hint="在 AI 页面点击 + 新任务，或在下方选择实例"
        />

        <SwipeableListItem
          v-for="session in filteredSessions"
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
              <button
                @click.stop="attachToTask(session)"
                class="attach-btn"
                :disabled="attaching === session.id"
              >
                {{ attaching === session.id ? '附加中...' : '附加到任务' }}
              </button>
            </div>
          </div>
        </SwipeableListItem>
      </div>
    </PullToRefresh>

    <!-- 分页 -->
    <div v-if="total > limit" class="pagination">
      <button 
        @click="prevPage" 
        :disabled="offset === 0"
        class="page-btn"
      >
        上一页
      </button>
      <span class="page-info">
        {{ Math.floor(offset / limit) + 1 }} / {{ Math.ceil(total / limit) }}
      </span>
      <button 
        @click="nextPage" 
        :disabled="offset + limit >= total"
        class="page-btn"
      >
        下一页
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onActivated } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { useToast } from '@/composables/useToast'
import EmptyState from '@/components/base/EmptyState.vue'
import ErrorState from '@/components/base/ErrorState.vue'
import PullToRefresh from '@/components/interactive/PullToRefresh.vue'
import SwipeableListItem, { type SwipeAction } from '@/components/interactive/SwipeableListItem.vue'

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

const router = useRouter()
const toast = useToast()

// 状态
const sessions = ref<Session[]>([])
const instances = ref<Instance[]>([])
const loading = ref(false)
const error = ref('')
const searchQuery = ref('')
const selectedInstanceId = ref('')
const attaching = ref('')
const offset = ref(0)
const limit = ref(20)
const total = ref(0)

// 计算属性 - 过滤会话
const filteredSessions = computed(() => {
  if (!searchQuery.value) return sessions.value
  
  const query = searchQuery.value.toLowerCase()
  return sessions.value.filter(s => 
    s.title.toLowerCase().includes(query) ||
    s.id.toLowerCase().includes(query)
  )
})

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

// 加载会话列表
async function loadSessions() {
  loading.value = true
  error.value = ''
  
  try {
    const instId = selectedInstanceId.value || undefined
    const data = await api.getAllSessions(instId, limit.value, offset.value)
    // API 返回大写字段 (ID, Title, Status)，映射为小写
    sessions.value = (data.sessions || []).map((s: any) => ({
      id: s.id || s.ID || '',
      title: s.title || s.Title || '',
      status: s.status || s.Status || 'idle',
    }))
    total.value = data.total || 0
  } catch (err: any) {
    error.value = err.message || '加载会话失败'
  } finally {
    loading.value = false
  }
}

// 处理搜索
function handleSearch() {
  // 搜索在客户端过滤，不需要重新请求
}

// 处理实例切换
function handleInstanceChange() {
  offset.value = 0
  loadSessions()
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
  if (offset.value + limit.value < total.value) {
    offset.value += limit.value
    loadSessions()
  }
}

// 打开会话详情
function openSessionDetail(session: Session) {
  // Phase V3: 跳转到实时会话对话视图
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

// 左滑显示的操作按钮（删除 + 归档占位）
function getSwipeActions(session: Session): SwipeAction[] {
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
  // 后端暂无 archive 接口；提示用户到详情页操作
  toast.info(`归档功能开发中：${session.title}`)
}

async function deleteSession(session: Session): Promise<void> {
  if (!confirm(`确定删除会话 “${session.title}”？`)) return
  // 后端暂无 DELETE /api/sessions/:id；先提示用户，不做虚假删除避免
  // 下一次刷新后条目"复活"造成数据不一致。
  toast.warning('删除功能开发中，请到 OpenCode 实例侧手动删除')
}

onMounted(() => {
  loadInstances()
  loadSessions()
})

onActivated(() => {
  // 从其他页面返回时重新加载
  loadSessions()
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
  font-size: var(--text-xs);
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
  font-size: var(--text-sm);
  color: var(--text-muted);
  font-family: monospace;
}

.session-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: var(--space-2);
}

.attach-btn {
  padding: var(--space-1) var(--space-3);
  background: var(--brand-primary);
  color: var(--text-inverse);
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
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
  padding: var(--space-1) var(--space-3);
  background: var(--brand-primary);
  color: var(--text-inverse);
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
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
  font-size: var(--text-sm);
}
</style>
