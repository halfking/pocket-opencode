<template>
  <div class="session-detail-view">
    <!-- 顶部栏 -->
    <div class="top-bar">
      <button class="back-btn" @click="goBack">← 返回</button>
      <h1>会话详情</h1>
      <button class="export-btn" @click="exportSummary">📤</button>
    </div>

    <!-- 加载状态 -->
    <div v-if="loading" class="loading-state">
      <div class="spinner"></div>
      <p>加载会话详情...</p>
    </div>

    <!-- 会话详情 -->
    <div v-else-if="session" class="session-detail-container">
      <!-- 会话信息卡片 -->
      <div class="info-card">
        <div class="info-header">
          <h2>{{ session.title || session.id }}</h2>
          <span class="status-badge" :class="session.status">
            {{ statusText(session.status) }}
          </span>
        </div>
        
        <div class="info-grid">
          <div class="info-item">
            <span class="label">会话 ID</span>
            <span class="value code">{{ session.id }}</span>
          </div>
          <div class="info-item">
            <span class="label">创建时间</span>
            <span class="value">{{ formatDateTime(session.createdAt) }}</span>
          </div>
          <div class="info-item">
            <span class="label">更新时间</span>
            <span class="value">{{ formatDateTime(session.updatedAt) }}</span>
          </div>
          <div class="info-item">
            <span class="label">持续时间</span>
            <span class="value">{{ formatDuration(session.duration) }}</span>
          </div>
        </div>
      </div>

      <!-- 代码变更统计 -->
      <div v-if="session.fileChanges" class="stats-card">
        <h3>📊 代码变更统计</h3>
        <div class="stats-grid">
          <div class="stat-item additions">
            <div class="stat-value">+{{ session.fileChanges.additions }}</div>
            <div class="stat-label">新增行数</div>
          </div>
          <div class="stat-item deletions">
            <div class="stat-value">-{{ session.fileChanges.deletions }}</div>
            <div class="stat-label">删除行数</div>
          </div>
          <div class="stat-item files">
            <div class="stat-value">{{ session.fileChanges.files }}</div>
            <div class="stat-label">修改文件</div>
          </div>
          <div class="stat-item messages">
            <div class="stat-value">{{ session.messageCount || 0 }}</div>
            <div class="stat-label">消息数</div>
          </div>
        </div>
        
        <!-- 代码变更可视化条 -->
        <div class="change-bar">
          <div 
            class="change-additions" 
            :style="{ width: additionsPercentage + '%' }"
          ></div>
          <div 
            class="change-deletions" 
            :style="{ width: deletionsPercentage + '%' }"
          ></div>
        </div>
      </div>

      <!-- 会话摘要 -->
      <div v-if="summary" class="summary-card">
        <h3>📝 会话摘要</h3>
        <div class="summary-content">{{ summary }}</div>
        <button class="refresh-summary-btn" @click="loadSummary">刷新摘要</button>
      </div>

      <!-- 历史时间线 -->
      <div class="timeline-card">
        <div class="timeline-header">
          <h3>📜 执行历史</h3>
          <span class="timeline-count">{{ history.length }} 条记录</span>
        </div>

        <div v-if="historyLoading" class="timeline-loading">
          <div class="spinner small"></div>
          <p>加载历史记录...</p>
        </div>

        <div v-else-if="history.length > 0" class="timeline">
          <div 
            v-for="(event, index) in history" 
            :key="index"
            class="timeline-event"
            :class="event.type"
          >
            <div class="event-icon">{{ getEventIcon(event.type) }}</div>
            <div class="event-content">
              <div class="event-header">
                <span class="event-actor" :class="event.actor">
                  {{ getActorName(event.actor) }}
                </span>
                <span class="event-time">{{ formatTime(event.timestamp) }}</span>
              </div>
              <div class="event-body">{{ event.content }}</div>
              <div v-if="event.metadata" class="event-metadata">
                <div v-for="(value, key) in event.metadata" :key="key" class="metadata-item">
                  <span class="metadata-key">{{ key }}:</span>
                  <span class="metadata-value">{{ value }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div v-else class="timeline-empty">
          <p>暂无历史记录</p>
        </div>
      </div>
    </div>

    <!-- 错误状态 -->
    <div v-else class="error-state">
      <div class="error-icon">⚠️</div>
      <p>加载失败</p>
      <button class="retry-btn" @click="loadSession">重试</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useOpenCodeStore } from '../../stores/opencode'
import type { OpenCodeSession, HistoryEvent } from '../../stores/opencode'

const router = useRouter()
const route = useRoute()
const openCodeStore = useOpenCodeStore()

const session = ref<OpenCodeSession | null>(null)
const history = ref<HistoryEvent[]>([])
const summary = ref<string>('')
const loading = ref(true)
const historyLoading = ref(false)

const sessionId = computed(() => route.params.id as string)
const instanceId = computed(() => route.query.instance_id as string)

// 计算代码变更百分比
const additionsPercentage = computed(() => {
  if (!session.value?.fileChanges) return 0
  const total = session.value.fileChanges.additions + session.value.fileChanges.deletions
  if (total === 0) return 0
  return (session.value.fileChanges.additions / total) * 100
})

const deletionsPercentage = computed(() => {
  if (!session.value?.fileChanges) return 0
  const total = session.value.fileChanges.additions + session.value.fileChanges.deletions
  if (total === 0) return 0
  return (session.value.fileChanges.deletions / total) * 100
})

onMounted(async () => {
  await loadSession()
  await loadHistory()
  await loadSummary()
})

async function loadSession() {
  loading.value = true
  try {
    // 从 store 中查找会话
    const sessions = openCodeStore.sessions
    const found = sessions.find(s => s.id === sessionId.value)
    
    if (found) {
      session.value = found
    } else {
      // 如果 store 中没有，重新加载
      if (instanceId.value) {
        await openCodeStore.loadSessions(instanceId.value)
        const foundAfterLoad = openCodeStore.sessions.find(s => s.id === sessionId.value)
        if (foundAfterLoad) {
          session.value = foundAfterLoad
        }
      }
    }
  } catch (error) {
    console.error('❌ 加载会话失败:', error)
  } finally {
    loading.value = false
  }
}

async function loadHistory() {
  historyLoading.value = true
  try {
    await openCodeStore.loadSessionHistory(sessionId.value)
    history.value = openCodeStore.sessionHistory[sessionId.value] || []
  } catch (error) {
    console.error('❌ 加载历史失败:', error)
  } finally {
    historyLoading.value = false
  }
}

async function loadSummary() {
  if (!instanceId.value) return
  
  try {
    summary.value = await openCodeStore.getSessionSummary(sessionId.value)
  } catch (error) {
    console.error('❌ 加载摘要失败:', error)
    summary.value = '获取摘要失败'
  }
}

function exportSummary() {
  if (!session.value) return

  const content = `
# ${session.value.title || session.value.id}

## 基本信息
- 会话ID: ${session.value.id}
- 状态: ${statusText(session.value.status)}
- 创建时间: ${formatDateTime(session.value.createdAt)}
- 持续时间: ${formatDuration(session.value.duration)}

## 代码变更
- 新增: +${session.value.fileChanges?.additions || 0} 行
- 删除: -${session.value.fileChanges?.deletions || 0} 行
- 文件: ${session.value.fileChanges?.files || 0} 个

## 会话摘要
${summary.value || '暂无摘要'}

## 历史记录
${history.value.map(e => `- [${formatTime(e.timestamp)}] ${getActorName(e.actor)}: ${e.content}`).join('\n')}
  `.trim()

  // 下载为文件
  const blob = new Blob([content], { type: 'text/markdown' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `session-${sessionId.value}.md`
  a.click()
  URL.revokeObjectURL(url)
}

function goBack() {
  router.back()
}

function statusText(status: string): string {
  const map: Record<string, string> = {
    busy: '进行中',
    idle: '空闲',
    retry: '重试中',
    completed: '已完成'
  }
  return map[status] || status
}

function formatDateTime(timestamp?: string): string {
  if (!timestamp) return '未知'
  return new Date(timestamp).toLocaleString('zh-CN')
}

function formatTime(timestamp: string): string {
  return new Date(timestamp).toLocaleTimeString('zh-CN')
}

function formatDuration(seconds?: number): string {
  if (!seconds) return '0 分钟'
  
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  
  if (hours > 0) {
    return `${hours} 小时 ${minutes} 分钟`
  }
  return `${minutes} 分钟`
}

function getEventIcon(type: string): string {
  const icons: Record<string, string> = {
    message: '💬',
    edit: '📝',
    test: '✅',
    error: '❌'
  }
  return icons[type] || '📌'
}

function getActorName(actor: string): string {
  const names: Record<string, string> = {
    user: '用户',
    ai: 'AI',
    system: '系统'
  }
  return names[actor] || actor
}
</script>

<style scoped>
.session-detail-view {
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
  position: sticky;
  top: 0;
  z-index: 10;
}

.back-btn, .export-btn {
  padding: 8px 12px;
  font-size: 14px;
  background: transparent;
  border: 1px solid #e0e0e0;
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.3s;
}

.back-btn:active, .export-btn:active {
  background: #f5f7fa;
  transform: scale(0.95);
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
  padding: 40px;
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

.spinner.small {
  width: 24px;
  height: 24px;
  border-width: 3px;
  margin-bottom: 8px;
}

@keyframes spin {
  0% { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}

.session-detail-container {
  flex: 1;
  padding: 20px;
  overflow-y: auto;
}

.info-card, .stats-card, .summary-card, .timeline-card {
  background: white;
  border-radius: 16px;
  padding: 20px;
  margin-bottom: 16px;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.08);
}

.info-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.info-header h2 {
  font-size: 20px;
  font-weight: 600;
  margin: 0;
  color: #333;
}

.status-badge {
  padding: 6px 14px;
  border-radius: 20px;
  font-size: 13px;
  font-weight: 600;
}

.status-badge.busy {
  background: #ffe5e5;
  color: #ff4757;
}

.status-badge.idle {
  background: #f0f0f0;
  color: #999;
}

.info-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 16px;
}

.info-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.info-item .label {
  font-size: 13px;
  color: #999;
}

.info-item .value {
  font-size: 15px;
  color: #333;
  font-weight: 500;
}

.info-item .value.code {
  font-family: monospace;
  font-size: 13px;
  color: #667eea;
}

.stats-card h3, .summary-card h3, .timeline-card h3 {
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 16px 0;
  color: #333;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(100px, 1fr));
  gap: 16px;
  margin-bottom: 16px;
}

.stat-item {
  text-align: center;
  padding: 16px;
  border-radius: 12px;
}

.stat-item.additions {
  background: linear-gradient(135deg, #d4f4dd 0%, #c8f0d4 100%);
}

.stat-item.deletions {
  background: linear-gradient(135deg, #ffe5e5 0%, #ffd5d5 100%);
}

.stat-item.files, .stat-item.messages {
  background: linear-gradient(135deg, #e8f0fe 0%, #d5e5fc 100%);
}

.stat-value {
  font-size: 24px;
  font-weight: 700;
  margin-bottom: 4px;
}

.stat-item.additions .stat-value {
  color: #2ed573;
}

.stat-item.deletions .stat-value {
  color: #ff4757;
}

.stat-item.files .stat-value, .stat-item.messages .stat-value {
  color: #667eea;
}

.stat-label {
  font-size: 12px;
  color: #666;
}

.change-bar {
  height: 8px;
  background: #f0f0f0;
  border-radius: 4px;
  display: flex;
  overflow: hidden;
}

.change-additions {
  background: #2ed573;
  transition: width 0.3s;
}

.change-deletions {
  background: #ff4757;
  transition: width 0.3s;
}

.summary-content {
  padding: 16px;
  background: #f8f9fa;
  border-radius: 8px;
  line-height: 1.6;
  color: #555;
  margin-bottom: 12px;
}

.refresh-summary-btn {
  padding: 8px 16px;
  font-size: 13px;
  background: #667eea;
  color: white;
  border: none;
  border-radius: 6px;
  cursor: pointer;
}

.timeline-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}

.timeline-count {
  font-size: 13px;
  padding: 4px 10px;
  background: #e8f0fe;
  color: #667eea;
  border-radius: 10px;
  font-weight: 600;
}

.timeline {
  position: relative;
  padding-left: 40px;
}

.timeline::before {
  content: '';
  position: absolute;
  left: 16px;
  top: 0;
  bottom: 0;
  width: 2px;
  background: #e0e0e0;
}

.timeline-event {
  position: relative;
  margin-bottom: 24px;
}

.event-icon {
  position: absolute;
  left: -40px;
  width: 32px;
  height: 32px;
  background: white;
  border: 2px solid #e0e0e0;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
}

.timeline-event.message .event-icon {
  border-color: #667eea;
  background: #e8f0fe;
}

.timeline-event.edit .event-icon {
  border-color: #ffa502;
  background: #fff4e5;
}

.timeline-event.test .event-icon {
  border-color: #2ed573;
  background: #d4f4dd;
}

.timeline-event.error .event-icon {
  border-color: #ff4757;
  background: #ffe5e5;
}

.event-content {
  background: #f8f9fa;
  padding: 12px 16px;
  border-radius: 8px;
}

.event-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.event-actor {
  font-size: 13px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 4px;
}

.event-actor.user {
  background: #e8f0fe;
  color: #667eea;
}

.event-actor.ai {
  background: #d4f4dd;
  color: #2ed573;
}

.event-actor.system {
  background: #f0f0f0;
  color: #999;
}

.event-time {
  font-size: 12px;
  color: #999;
}

.event-body {
  font-size: 14px;
  color: #555;
  line-height: 1.5;
  margin-bottom: 8px;
}

.event-metadata {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid #e0e0e0;
}

.metadata-item {
  font-size: 12px;
  color: #666;
}

.metadata-key {
  font-weight: 600;
  margin-right: 4px;
}

.timeline-loading, .timeline-empty {
  text-align: center;
  padding: 40px;
  color: #999;
}

.error-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px;
  color: #ff4757;
}

.error-icon {
  font-size: 64px;
  margin-bottom: 16px;
}

.retry-btn {
  padding: 12px 24px;
  font-size: 14px;
  font-weight: 600;
  color: white;
  background: #667eea;
  border: none;
  border-radius: 8px;
  cursor: pointer;
  margin-top: 16px;
}
</style>
