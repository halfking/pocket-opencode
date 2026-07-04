<template>
  <div class="tasks-view">
    <!-- 顶部栏 -->
    <div class="top-bar">
      <button class="back-btn" @click="goBack">← 返回</button>
      <h1>任务列表</h1>
      <button class="add-btn" @click="showCreateModal = true">+</button>
    </div>

    <!-- 当前实例信息 -->
    <div class="instance-info-bar">
      <span class="instance-label">当前实例:</span>
      <span class="instance-name">{{ currentInstance?.displayName }}</span>
    </div>

    <!-- 加载状态 -->
    <div v-if="loading" class="loading-state">
      <div class="spinner"></div>
      <p>加载任务...</p>
    </div>

    <!-- 任务列表（按分组） -->
    <div v-else-if="groupedTasks.length > 0" class="tasks-container">
      <div v-for="group in groupedTasks" :key="group.name" class="task-group">
        <div class="group-header">
          <h2>{{ group.name }}</h2>
          <span class="task-count">{{ group.tasks.length }}</span>
        </div>
        
        <div class="task-list">
          <div
            v-for="task in group.tasks"
            :key="task.id"
            class="task-card"
            @click="viewTask(task.id)"
          >
            <div class="task-priority" :class="task.priority"></div>
            <div class="task-content">
              <h3>{{ task.title }}</h3>
              <p v-if="task.description" class="task-desc">{{ task.description }}</p>
              <div class="task-meta">
                <span class="meta-item">
                  <span class="meta-icon">💬</span>
                  {{ task.sessionCount || 0 }} 会话
                </span>
                <span class="meta-item status" :class="task.status">
                  {{ statusText(task.status) }}
                </span>
              </div>
            </div>
            <div class="task-arrow">›</div>
          </div>
        </div>
      </div>
    </div>

    <!-- 空状态 -->
    <div v-else class="empty-state">
      <div class="empty-icon">📝</div>
      <p>暂无任务</p>
      <button class="create-first-btn" @click="showCreateModal = true">
        创建第一个任务
      </button>
    </div>

    <!--
      ✅ 已移除硬编码底部导航（任务/会话/实例/设置）。
      App.vue 现在用 AppLayout 包裹 router-view，共享的 BottomNav 会自动渲染
      5模块 Tab（AI/笔记/会议/邮件/更多）。这里不再重复渲染以免双层 UI。
    -->

    <!-- 创建任务模态框 -->
    <div v-if="showCreateModal" class="modal-overlay" @click="showCreateModal = false">
      <div class="modal-content" @click.stop>
        <h2>创建任务</h2>
        
        <div class="form-group">
          <label>标题 *</label>
          <input v-model="newTask.title" type="text" placeholder="输入任务标题" />
        </div>

        <div class="form-group">
          <label>描述</label>
          <textarea v-model="newTask.description" placeholder="输入任务描述" rows="3"></textarea>
        </div>

        <div class="form-group">
          <label>优先级</label>
          <select v-model="newTask.priority">
            <option value="high">高</option>
            <option value="medium">中</option>
            <option value="low">低</option>
          </select>
        </div>

        <div class="form-group">
          <label>状态</label>
          <select v-model="newTask.status">
            <option value="active">进行中</option>
            <option value="blocked">已阻塞</option>
            <option value="completed">已完成</option>
          </select>
        </div>

        <div class="modal-actions">
          <button class="cancel-btn" @click="showCreateModal = false">取消</button>
          <button class="create-btn" @click="handleCreate" :disabled="!newTask.title">
            创建
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { api, type Task } from '../../api/client'
import wsClient from '../../api/websocket'

const router = useRouter()

const currentInstance = ref<any>(null)
const tasks = ref<Task[]>([])
const loading = ref(true)
const showCreateModal = ref(false)

const newTask = ref({
  title: '',
  description: '',
  priority: 'medium',
  status: 'active'
})

// 按分组组织任务
const groupedTasks = computed(() => {
  const groups = [
    { name: '进行中', tasks: tasks.value.filter(t => t.status === 'active') },
    { name: '已阻塞', tasks: tasks.value.filter(t => t.status === 'blocked') },
    { name: '已完成', tasks: tasks.value.filter(t => t.status === 'completed') }
  ]
  return groups.filter(g => g.tasks.length > 0)
})

onMounted(() => {
  // 加载当前实例
  const instanceStr = localStorage.getItem('selected_instance')
  if (instanceStr) {
    currentInstance.value = JSON.parse(instanceStr)
  }
  
  loadTasks()
  
  // WebSocket 实时更新
  wsClient.on('task_created', handleTaskUpdate)
  wsClient.on('task_updated', handleTaskUpdate)
  wsClient.on('session_attached', handleSessionAttached)
})

onUnmounted(() => {
  wsClient.off('task_created', handleTaskUpdate)
  wsClient.off('task_updated', handleTaskUpdate)
  wsClient.off('session_attached', handleSessionAttached)
})

async function loadTasks() {
  loading.value = true
  try {
    console.log('🔍 开始加载任务...', '当前实例:', currentInstance.value)

    if (!currentInstance.value) {
      tasks.value = []
      console.warn('⚠️ 未选择实例，任务列表为空')
      return
    }

    // 直接从当前 OpenCode 实例获取开发会话（每个 Session = 一个任务）
    const instanceTasks = await api.getTasks(currentInstance.value.id)
    console.log('✅ 实例任务:', instanceTasks.length, instanceTasks)
    tasks.value = instanceTasks
  } catch (error) {
    console.error('❌ 加载任务失败:', error)
    tasks.value = []
  } finally {
    loading.value = false
  }
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
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
      sessionCount: 0
    }
    
    await api.createTask(task)
    
    // 重置表单
    newTask.value = {
      title: '',
      description: '',
      priority: 'medium',
      status: 'active'
    }
    
    showCreateModal.value = false
    loadTasks()
  } catch (error) {
    console.error('Failed to create task:', error)
    alert('创建任务失败')
  }
}

function handleTaskUpdate(task: Task) {
  const index = tasks.value.findIndex(t => t.id === task.id)
  if (index >= 0) {
    tasks.value[index] = task
  } else {
    tasks.value.unshift(task)
  }
}

function handleSessionAttached(link: any) {
  const task = tasks.value.find(t => t.id === link.taskId)
  if (task) {
    task.sessionCount = (task.sessionCount || 0) + 1
  }
}

function viewTask(taskId: string) {
  // Phase V3: 直接进入会话对话视图（task = session 1:1）
  const instanceId = (() => {
    try {
      const raw = localStorage.getItem('selected_instance')
      if (raw) return JSON.parse(raw)?.id || ''
    } catch {}
    return ''
  })()
  router.push({
    path: `/sessions/${taskId}`,
    query: { instance_id: instanceId, title: '' },
  })
}

function goBack() {
  router.push('/instances')
}

function statusText(status: string): string {
  const map: Record<string, string> = {
    active: '进行中',
    blocked: '已阻塞',
    completed: '已完成'
  }
  return map[status] || status
}
</script>

<style scoped>
.tasks-view {
  min-height: 100vh;
  background: #f5f7fa;
  display: flex;
  flex-direction: column;
  padding-bottom: 70px;
}

.top-bar {
  background: white;
  padding: var(--space-3) var(--space-4);   /* 修改：12px 16px（原 16px 20px） */
  display: flex;
  align-items: center;
  gap: var(--space-3);
  border-bottom: 1px solid var(--border);   /* 替代阴影 */
}

.back-btn, .add-btn {
  padding: var(--space-1-5) var(--space-2-5); /* 更紧凑 */
  font-size: 14px;
  background: transparent;
  border: 1px solid #e0e0e0;
  border-radius: var(--radius-md);          /* 修改：使用变量 (8px) */
  cursor: pointer;
}

.add-btn {
  font-size: 20px;
  font-weight: bold;
  color: #667eea;
  border-color: #667eea;
}

.top-bar h1 {
  flex: 1;
  font-size: 18px;                          /* 修改：18px（原 20px） */
  font-weight: 600;
  margin: 0;
}

.instance-info-bar {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  padding: var(--space-2-5) var(--space-4); /* 修改：10px 16px（原 12px 20px） */
  color: white;
  font-size: 13px;                          /* 修改：13px（原 14px） */
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

.tasks-container {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-4);                  /* 修改：使用变量 (14px，原 20px) */
}

.task-group {
  margin-bottom: var(--space-5);            /* 修改：使用变量 (18px，原 24px) */
}

.group-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.group-header h2 {
  font-size: 15px;                          /* 修改：15px（原 16px） */
  font-weight: 600;
  color: #333;
  margin: 0;
}

.task-count {
  font-size: 11px;                          /* 修改：11px（原 12px） */
  padding: 2px 6px;                         /* 修改：2px 6px（原 4px 8px） */
  background: #e8f0fe;
  color: #667eea;
  border-radius: 12px;
  font-weight: 600;
}

.task-card {
  background: white;
  border-radius: var(--radius-md);          /* 修改：使用变量 (8px，原 12px) */
  padding: var(--space-3);                  /* 修改：使用变量 (12px，原 16px) */
  margin-bottom: var(--space-2);            /* 修改：使用变量 (8px，原 8px) */
  display: flex;
  align-items: center;
  gap: var(--space-3);
  cursor: pointer;
  border: 1px solid var(--border);          /* 新增：替代阴影 */
}

.task-card:active {
  transform: scale(0.98);
}

.task-priority {
  width: 3px;                               /* 修改：3px（原 4px） */
  height: 36px;                             /* 修改：36px（原 40px） */
  border-radius: 2px;
  flex-shrink: 0;
}

.task-priority.high { background: #ff4757; }
.task-priority.medium { background: #ffa502; }
.task-priority.low { background: #2ed573; }

.task-content {
  flex: 1;
  min-width: 0;
}

.task-content h3 {
  font-size: 14px;                          /* 修改：14px（原 15px） */
  font-weight: 600;
  color: #333;
  margin: 0 0 var(--space-1) 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-desc {
  font-size: 12px;                          /* 修改：12px（原 13px） */
  color: #666;
  margin: 0 0 var(--space-1-5) 0;           /* 修改：使用变量 */
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-meta {
  display: flex;
  gap: var(--space-2-5);                    /* 修改：使用变量 (10px，原 12px) */
  font-size: 11px;                          /* 修改：11px（原 12px） */
}

.meta-item {
  display: flex;
  align-items: center;
  gap: 4px;
  color: #999;
}

.meta-item.status {
  padding: 2px 6px;                         /* 修改：2px 6px（原 4px 8px） */
  border-radius: var(--radius-sm);          /* 修改：使用变量 */
  font-weight: 500;
}

.meta-item.status.active {
  background: #d4f4dd;
  color: #2a8a4e;
}

.meta-item.status.blocked {
  background: #fff3cd;
  color: #856404;
}

.meta-item.status.completed {
  background: #e8f0fe;
  color: #667eea;
}

.task-arrow {
  font-size: 18px;                          /* 修改：18px（原 20px） */
  color: var(--border-strong);              /* 修改：使用变量 */
}

.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px;
}

.empty-icon {
  font-size: 64px;
  margin-bottom: 16px;
}

.create-first-btn {
  padding: 12px 24px;
  font-size: 14px;
  font-weight: 600;
  color: white;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  border: none;
  border-radius: var(--radius-md);          /* 修改：使用变量 (8px) */
  cursor: pointer;
  margin-top: 16px;
}

/*
  ✅ 已删除硬编码底部导航的 CSS 样式（.bottom-nav / .nav-item / .nav-icon /
  .nav-label），由 AppLayout 提供的共享 BottomNav 接管。
*/

/*
  ✅ 已删除硬编码底部导航的 CSS 样式（.bottom-nav / .nav-item / .nav-icon /
  .nav-label），由 AppLayout 提供的共享 BottomNav 接管。
*/

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
  border-radius: var(--radius-lg);          /* 修改：使用变量 (10px，原 16px) */
  padding: 24px;
  width: 100%;
  max-width: 400px;
  max-height: 80vh;
  overflow-y: auto;
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
.form-group textarea,
.form-group select {
  width: 100%;
  padding: 12px;
  font-size: 14px;
  border: 1px solid #e0e0e0;
  border-radius: var(--radius-md);          /* 修改：使用变量 (8px) */
  box-sizing: border-box;
}

.modal-actions {
  display: flex;
  gap: 12px;
  margin-top: 24px;
}

.cancel-btn,
.create-btn {
  flex: 1;
  padding: 12px;
  font-size: 14px;
  font-weight: 600;
  border: none;
  border-radius: var(--radius-md);          /* 修改：使用变量 (8px) */
  cursor: pointer;
}

.cancel-btn {
  background: #f5f7fa;
  color: #666;
}

.create-btn {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
}

.create-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
