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
    <div v-else-if="error" class="error">
      <p>{{ error }}</p>
      <button @click="loadSessions" class="retry-btn">重试</button>
    </div>

    <!-- 会话列表 -->
    <div v-else class="session-list">
      <div v-if="filteredSessions.length === 0" class="empty-state">
        <p>暂无会话</p>
      </div>
      
      <div 
        v-for="session in filteredSessions" 
        :key="session.id"
        class="session-card"
        @click="openSessionDetail(session)"
      >
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
    </div>

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
    instances.value = data.instances || []
  } catch (err: any) {
    console.error('Failed to load instances:', err)
  }
}

// 加载会话列表
async function loadSessions() {
  loading.value = true
  error.value = ''
  
  try {
    const data = await api.getAllSessions(
      selectedInstanceId.value || undefined,
      limit.value,
      offset.value
    )
    sessions.value = data.sessions || []
    total.value = data.total || 0
  } catch (err: any) {
    error.value = err.message || '加载会话失败'
    console.error('Failed to load sessions:', err)
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
  // TODO: 导航到会话详情页或在新窗口打开 OpenCode
  console.log('Open session:', session)
  alert(`会话详情功能开发中\n\nID: ${session.id}\nTitle: ${session.title}`)
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
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  padding: 1rem;
  padding-bottom: 70px; /* 为底部导航留空间 */
  overflow-y: auto;
}

.toolbar {
  display: flex;
  gap: 0.5rem;
  margin-bottom: 1rem;
}

.search-bar {
  flex: 1;
}

.search-bar input {
  width: 100%;
  padding: 0.75rem 1rem;
  border: none;
  border-radius: 12px;
  font-size: 1rem;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
}

.instance-filter {
  padding: 0.75rem 1rem;
  border: none;
  border-radius: 12px;
  font-size: 1rem;
  background: white;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
  cursor: pointer;
}

.loading, .error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 3rem 1rem;
  color: white;
  text-align: center;
}

.spinner {
  width: 40px;
  height: 40px;
  border: 3px solid rgba(255, 255, 255, 0.3);
  border-top-color: white;
  border-radius: 50%;
  animation: spin 1s linear infinite;
  margin-bottom: 1rem;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.retry-btn {
  margin-top: 1rem;
  padding: 0.75rem 2rem;
  background: white;
  color: #667eea;
  border: none;
  border-radius: 12px;
  font-size: 1rem;
  font-weight: 600;
  cursor: pointer;
}

.empty-state {
  text-align: center;
  padding: 3rem 1rem;
  color: white;
}

.session-list {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.session-card {
  background: white;
  border-radius: 16px;
  padding: 1rem;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
  cursor: pointer;
  transition: transform 0.2s, box-shadow 0.2s;
}

.session-card:active {
  transform: scale(0.98);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
}

.session-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 0.5rem;
}

.session-title {
  margin: 0;
  font-size: 1.1rem;
  font-weight: 600;
  color: #333;
  flex: 1;
  margin-right: 0.5rem;
}

.status-badge {
  padding: 0.25rem 0.75rem;
  border-radius: 12px;
  font-size: 0.85rem;
  font-weight: 600;
  white-space: nowrap;
}

.status-badge.active {
  background: #d4edda;
  color: #155724;
}

.status-badge.inactive {
  background: #f8d7da;
  color: #721c24;
}

.status-badge.empty {
  background: #fff3cd;
  color: #856404;
}

.session-id {
  margin: 0.5rem 0;
  font-size: 0.85rem;
  color: #666;
  font-family: monospace;
}

.session-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: 1rem;
}

.attach-btn {
  padding: 0.5rem 1.5rem;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  border: none;
  border-radius: 12px;
  font-size: 0.9rem;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.2s;
}

.attach-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.pagination {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 1rem;
  margin-top: 1rem;
  padding: 1rem;
  background: white;
  border-radius: 16px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
}

.page-btn {
  padding: 0.5rem 1.5rem;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  border: none;
  border-radius: 12px;
  font-size: 0.9rem;
  font-weight: 600;
  cursor: pointer;
}

.page-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.page-info {
  color: #666;
  font-weight: 600;
}
</style>
