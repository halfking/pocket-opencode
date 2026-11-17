<template>
  <div class="sync-status-indicator">
    <div 
      class="status-badge"
      :class="statusClass"
      @click="showDetails = !showDetails"
      @mouseenter="hovering = true"
      @mouseleave="hovering = false"
    >
      <span class="status-icon">{{ statusIcon }}</span>
      <span class="status-text">{{ statusText }}</span>
    </div>

    <!-- 详情弹窗 -->
    <Transition name="fade">
      <div v-if="showDetails" class="status-details" @click.stop>
        <div class="details-header">
          <h3>同步状态</h3>
          <button @click="showDetails = false" class="close-btn">✕</button>
        </div>
        
        <div class="details-content">
          <div class="detail-row">
            <span class="label">状态:</span>
            <span class="value" :class="statusClass">{{ statusText }}</span>
          </div>
          
          <div v-if="syncState.lastSynced" class="detail-row">
            <span class="label">最后同步:</span>
            <span class="value">{{ formatTime(syncState.lastSynced) }}</span>
          </div>
          
          <div v-if="syncState.memoraId" class="detail-row">
            <span class="label">云端 ID:</span>
            <span class="value">{{ syncState.memoraId }}</span>
          </div>
          
          <div v-if="syncState.errorMessage" class="detail-row error">
            <span class="label">错误信息:</span>
            <span class="value">{{ syncState.errorMessage }}</span>
          </div>
        </div>

        <div v-if="isConflict" class="details-actions">
          <button @click="handleResolveConflict" class="btn-primary">
            解决冲突
          </button>
        </div>
      </div>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useSyncStateStore } from '@/stores/syncState'

interface Props {
  noteId: string
}

const props = defineProps<Props>()
const emit = defineEmits<{
  conflictDetected: []
}>()

const syncStateStore = useSyncStateStore()
const showDetails = ref(false)
const hovering = ref(false)

// 同步状态
const syncState = computed(() => syncStateStore.getSyncState(props.noteId))

// 状态判断
const isConflict = computed(() => syncState.value.status === 'conflict')
const isSyncing = computed(() => syncState.value.status === 'syncing')
const isError = computed(() => syncState.value.status === 'error')
const isOffline = computed(() => syncState.value.status === 'offline')
const isSynced = computed(() => syncState.value.status === 'synced')

// 状态样式
const statusClass = computed(() => {
  return `status-${syncState.value.status}`
})

// 状态图标
const statusIcon = computed(() => {
  switch (syncState.value.status) {
    case 'synced': return '✅'
    case 'syncing': return '🔄'
    case 'conflict': return '⚠️'
    case 'error': return '❌'
    case 'offline': return '📴'
    case 'pending': return '⏳'
    default: return '•'
  }
})

// 状态文本
const statusText = computed(() => {
  switch (syncState.value.status) {
    case 'synced': return '已同步'
    case 'syncing': return '同步中...'
    case 'conflict': return '有冲突'
    case 'error': return '同步失败'
    case 'offline': return '离线'
    case 'pending': return '待同步'
    default: return '未知'
  }
})

// 格式化时间
const formatTime = (timestamp: number) => {
  const date = new Date(timestamp)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMins = Math.floor(diffMs / 60000)
  
  if (diffMins < 1) return '刚刚'
  if (diffMins < 60) return `${diffMins} 分钟前`
  
  const diffHours = Math.floor(diffMins / 60)
  if (diffHours < 24) return `${diffHours} 小时前`
  
  const diffDays = Math.floor(diffHours / 24)
  if (diffDays < 7) return `${diffDays} 天前`
  
  return date.toLocaleDateString('zh-CN')
}

// 处理解决冲突
const handleResolveConflict = () => {
  showDetails.value = false
  emit('conflictDetected')
}

// 定期轮询同步状态（每 30 秒）
let pollInterval: number | null = null

onMounted(() => {
  // 初始加载
  syncStateStore.fetchSyncStatus(props.noteId)
  
  // 设置轮询
  pollInterval = window.setInterval(() => {
    syncStateStore.fetchSyncStatus(props.noteId)
  }, 30000)
})

onUnmounted(() => {
  if (pollInterval) {
    clearInterval(pollInterval)
  }
})

// 监听 noteId 变化
watch(() => props.noteId, (newId) => {
  if (newId) {
    syncStateStore.fetchSyncStatus(newId)
  }
})
</script>

<style scoped>
.sync-status-indicator {
  position: relative;
  display: inline-block;
}

.status-badge {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.375rem 0.75rem;
  border-radius: 0.375rem;
  font-size: 0.875rem;
  cursor: pointer;
  transition: all 0.2s;
  user-select: none;
}

.status-badge:hover {
  transform: translateY(-1px);
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
}

.status-icon {
  font-size: 1rem;
}

.status-text {
  font-weight: 500;
}

/* 状态颜色 */
.status-synced {
  background-color: #d1fae5;
  color: #065f46;
}

.status-syncing {
  background-color: #dbeafe;
  color: #1e40af;
  animation: pulse 2s infinite;
}

.status-conflict {
  background-color: #fed7aa;
  color: #92400e;
}

.status-error {
  background-color: #fecaca;
  color: #991b1b;
}

.status-offline {
  background-color: #e5e7eb;
  color: #4b5563;
}

.status-pending {
  background-color: #fef3c7;
  color: #92400e;
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.7;
  }
}

/* 详情弹窗 */
.status-details {
  position: absolute;
  top: calc(100% + 0.5rem);
  right: 0;
  min-width: 300px;
  background: white;
  border-radius: 0.5rem;
  box-shadow: 0 10px 25px rgba(0, 0, 0, 0.15);
  z-index: 50;
  overflow: hidden;
}

.details-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 1rem;
  border-bottom: 1px solid #e5e7eb;
}

.details-header h3 {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
  color: #111827;
}

.close-btn {
  background: none;
  border: none;
  font-size: 1.25rem;
  color: #6b7280;
  cursor: pointer;
  padding: 0;
  width: 1.5rem;
  height: 1.5rem;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 0.25rem;
}

.close-btn:hover {
  background-color: #f3f4f6;
  color: #111827;
}

.details-content {
  padding: 1rem;
}

.detail-row {
  display: flex;
  justify-content: space-between;
  margin-bottom: 0.75rem;
  font-size: 0.875rem;
}

.detail-row:last-child {
  margin-bottom: 0;
}

.detail-row .label {
  color: #6b7280;
  font-weight: 500;
}

.detail-row .value {
  color: #111827;
  font-weight: 400;
}

.detail-row.error .value {
  color: #dc2626;
  max-width: 200px;
  word-break: break-word;
}

.details-actions {
  padding: 1rem;
  border-top: 1px solid #e5e7eb;
  display: flex;
  justify-content: flex-end;
}

.btn-primary {
  background-color: #3b82f6;
  color: white;
  border: none;
  border-radius: 0.375rem;
  padding: 0.5rem 1rem;
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: background-color 0.2s;
}

.btn-primary:hover {
  background-color: #2563eb;
}

/* 过渡动画 */
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s, transform 0.2s;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
