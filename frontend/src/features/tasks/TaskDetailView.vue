<template>
  <div class="task-detail-view">
    <!-- 顶部栏 -->
    <div class="top-bar">
      <button class="back-btn" @click="goBack">← 返回</button>
      <h1>任务详情</h1>
      <button class="menu-btn">⋮</button>
    </div>

    <!-- 加载状态 -->
    <div v-if="loading" class="loading-state">
      <div class="spinner"></div>
      <p>加载中...</p>
    </div>

    <!-- 任务详情 -->
    <div v-else-if="task" class="task-detail-container">
      <!-- 任务头部 -->
      <div class="task-header">
        <div class="priority-badge" :class="task.priority">
          {{ priorityText(task.priority) }}
        </div>
        <h2>{{ task.title }}</h2>
        <p v-if="task.description" class="task-description">
          {{ task.description }}
        </p>
        <div class="task-status" :class="task.status">
          {{ statusText(task.status) }}
        </div>
      </div>

      <!-- 统计信息 -->
      <div class="stats-section">
        <div class="stat-card">
          <div class="stat-icon">💬</div>
          <div class="stat-info">
            <div class="stat-value">{{ task.sessionCount || 0 }}</div>
            <div class="stat-label">会话数</div>
          </div>
        </div>
        <div class="stat-card">
          <div class="stat-icon">📅</div>
          <div class="stat-info">
            <div class="stat-value">{{ formatDate(task.createdAt) }}</div>
            <div class="stat-label">创建时间</div>
          </div>
        </div>
      </div>

      <!-- 会话列表 -->
      <div class="section">
        <div class="section-header">
          <h3>关联会话</h3>
          <button class="attach-btn" @click="showAttachModal = true">+ 附加</button>
        </div>
        
        <div v-if="sessions.length > 0" class="session-list">
          <div v-for="session in sessions" :key="session.sessionId" class="session-card">
            <div class="session-icon">📝</div>
            <div class="session-info">
              <div class="session-id">{{ session.sessionId }}</div>
              <div class="session-meta">
                <span class="meta-tag">{{ session.role }}</span>
                <span class="meta-tag">{{ session.instanceId }}</span>
              </div>
            </div>
          </div>
        </div>
        
        <div v-else class="empty-sessions">
          <p>暂无关联会话</p>
          <button class="attach-first-btn" @click="showAttachModal = true">
            附加第一个会话
          </button>
        </div>
      </div>
    </div>

    <!-- 附加会话模态框 -->
    <div v-if="showAttachModal" class="modal-overlay" @click="showAttachModal = false">
      <div class="modal-content" @click.stop>
        <h2>附加会话</h2>
        
        <div class="form-group">
          <label>会话 ID *</label>
          <input v-model="newSession.sessionId" type="text" placeholder="输入会话 ID" />
        </div>

        <div class="form-group">
          <label>实例 ID *</label>
          <input v-model="newSession.instanceId" type="text" placeholder="输入实例 ID" />
        </div>

        <div class="form-group">
          <label>角色</label>
          <select v-model="newSession.role">
            <option value="primary">主要会话</option>
            <option value="supporting">支持会话</option>
            <option value="exploratory">探索会话</option>
            <option value="duplicate">重复会话</option>
          </select>
        </div>

        <div class="modal-actions">
          <button class="cancel-btn" @click="showAttachModal = false">取消</button>
          <button 
            class="attach-confirm-btn" 
            @click="handleAttach"
            :disabled="!newSession.sessionId || !newSession.instanceId"
          >
            附加
          </button>
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
const loading = ref(true)
const showAttachModal = ref(false)

const newSession = ref({
  sessionId: '',
  instanceId: '',
  role: 'primary'
})

onMounted(async () => {
  const taskId = route.params.id as string
  loading.value = true
  
  try {
    task.value = await api.getTask(taskId)
    sessions.value = await api.getTaskSessions(taskId)
  } catch (error) {
    console.error('Failed to load task:', error)
  } finally {
    loading.value = false
  }
})

async function handleAttach() {
  if (!task.value || !newSession.value.sessionId || !newSession.value.instanceId) return
  
  try {
    await api.attachSession(
      task.value.id,
      newSession.value.instanceId,
      newSession.value.sessionId,
      newSession.value.role || 'primary',
    )
    
    // 重新加载会话列表
    sessions.value = await api.getTaskSessions(task.value.id)
    
    // 重置表单
    newSession.value = {
      sessionId: '',
      instanceId: '',
      role: 'primary'
    }
    
    showAttachModal.value = false
  } catch (error) {
    console.error('Failed to attach session:', error)
    alert('附加会话失败')
  }
}

function goBack() {
  router.push('/tasks')
}

function priorityText(priority: string | undefined): string {
  const map: Record<string, string> = {
    high: '高优先级',
    medium: '中优先级',
    low: '低优先级'
  }
  return map[priority ?? ''] || priority || ''
}

function statusText(status: string): string {
  const map: Record<string, string> = {
    active: '进行中',
    blocked: '已阻塞',
    completed: '已完成'
  }
  return map[status] || status
}

function formatDate(date?: string): string {
  if (!date) return '-'
  return new Date(date).toLocaleDateString('zh-CN')
}
</script>

<style scoped>
.task-detail-view {
  min-height: 100vh;
  background: #f5f7fa;
  display: flex;
  flex-direction: column;
}

.top-bar {
  background: white;
  padding: 16px 20px;
  display: flex;
  align-items: center;
  gap: 12px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.05);
}

.back-btn, .menu-btn {
  padding: 8px 12px;
  font-size: 14px;
  background: transparent;
  border: 1px solid #e0e0e0;
  border-radius: 8px;
  cursor: pointer;
}

.top-bar h1 {
  flex: 1;
  font-size: 20px;
  font-weight: 600;
  margin: 0;
}

.loading-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.spinner {
  width: 40px;
  height: 40px;
  border: 4px solid #f3f3f3;
  border-top: 4px solid #667eea;
  border-radius: 50%;
  animation: spin 1s linear infinite;
  margin-bottom: 16px;
}

@keyframes spin {
  0% { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}

.task-detail-container {
  flex: 1;
  overflow-y: auto;
  padding: 20px;
}

.task-header {
  background: white;
  border-radius: 16px;
  padding: 24px;
  margin-bottom: 16px;
}

.priority-badge {
  display: inline-block;
  font-size: 12px;
  font-weight: 600;
  padding: 6px 12px;
  border-radius: 6px;
  margin-bottom: 12px;
}

.priority-badge.high {
  background: #ffe5e5;
  color: #ff4757;
}

.priority-badge.medium {
  background: #fff3e0;
  color: #ffa502;
}

.priority-badge.low {
  background: #e8f5e9;
  color: #2ed573;
}

.task-header h2 {
  font-size: 22px;
  font-weight: 700;
  color: #333;
  margin: 0 0 12px 0;
}

.task-description {
  font-size: 15px;
  color: #666;
  line-height: 1.6;
  margin: 0 0 16px 0;
}

.task-status {
  display: inline-block;
  font-size: 13px;
  font-weight: 600;
  padding: 8px 16px;
  border-radius: 8px;
}

.task-status.active {
  background: #d4f4dd;
  color: #2a8a4e;
}

.task-status.blocked {
  background: #fff3cd;
  color: #856404;
}

.task-status.completed {
  background: #e8f0fe;
  color: #667eea;
}

.stats-section {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-bottom: 16px;
}

.stat-card {
  background: white;
  border-radius: 12px;
  padding: 16px;
  display: flex;
  align-items: center;
  gap: 12px;
}

.stat-icon {
  font-size: 28px;
}

.stat-value {
  font-size: 20px;
  font-weight: 700;
  color: #333;
}

.stat-label {
  font-size: 12px;
  color: #999;
}

.section {
  background: white;
  border-radius: 16px;
  padding: 20px;
  margin-bottom: 16px;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.section-header h3 {
  font-size: 16px;
  font-weight: 600;
  margin: 0;
}

.attach-btn {
  padding: 8px 16px;
  font-size: 14px;
  font-weight: 600;
  color: #667eea;
  background: #e8f0fe;
  border: none;
  border-radius: 8px;
  cursor: pointer;
}

.session-card {
  background: #f8f9fa;
  border-radius: 12px;
  padding: 16px;
  margin-bottom: 8px;
  display: flex;
  align-items: center;
  gap: 12px;
}

.session-icon {
  font-size: 24px;
}

.session-id {
  font-size: 14px;
  font-weight: 600;
  color: #333;
  margin-bottom: 4px;
  font-family: monospace;
}

.session-meta {
  display: flex;
  gap: 8px;
}

.meta-tag {
  font-size: 11px;
  padding: 4px 8px;
  background: white;
  border-radius: 4px;
  color: #667eea;
}

.empty-sessions {
  text-align: center;
  padding: 40px 20px;
  color: #999;
}

.attach-first-btn {
  padding: 12px 24px;
  font-size: 14px;
  font-weight: 600;
  color: white;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  border: none;
  border-radius: 8px;
  cursor: pointer;
  margin-top: 16px;
}

.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  z-index: 1000;
}

.modal-content {
  background: white;
  border-radius: 16px;
  padding: 24px;
  width: 100%;
  max-width: 400px;
}

.modal-content h2 {
  font-size: 20px;
  margin: 0 0 20px 0;
}

.form-group {
  margin-bottom: 16px;
}

.form-group label {
  display: block;
  font-size: 14px;
  font-weight: 600;
  margin-bottom: 8px;
}

.form-group input,
.form-group select {
  width: 100%;
  padding: 12px;
  font-size: 14px;
  border: 1px solid #e0e0e0;
  border-radius: 8px;
  box-sizing: border-box;
}

.modal-actions {
  display: flex;
  gap: 12px;
  margin-top: 24px;
}

.cancel-btn,
.attach-confirm-btn {
  flex: 1;
  padding: 12px;
  font-size: 14px;
  font-weight: 600;
  border: none;
  border-radius: 8px;
  cursor: pointer;
}

.cancel-btn {
  background: #f5f7fa;
  color: #666;
}

.attach-confirm-btn {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
}

.attach-confirm-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
