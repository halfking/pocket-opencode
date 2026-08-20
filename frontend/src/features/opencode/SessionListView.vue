<template>
  <div class="session-list-view">
    <!-- 顶部栏 -->
    <div class="top-bar">
      <button class="back-btn" @click="goBack">← 返回</button>
      <h1>会话列表</h1>
      <button class="refresh-btn" @click="refreshSessions">🔄</button>
    </div>

    <!-- 当前实例信息 -->
    <div v-if="selectedInstance" class="instance-banner">
      <div class="banner-content">
        <span class="banner-icon">💻</span>
        <div class="banner-info">
          <h3>{{ selectedInstance.displayName }}</h3>
          <p>{{ activeSessions.length }} 活跃 • {{ idleSessions.length }} 空闲</p>
        </div>
      </div>
    </div>

    <!-- 加载状态 -->
    <div v-if="loading" class="loading-state">
      <div class="spinner"></div>
      <p>加载会话...</p>
    </div>

    <!-- 会话列表（按状态分组） -->
    <div v-else-if="sessions.length > 0" class="sessions-container">
      <!-- 活跃会话 -->
      <div v-if="activeSessions.length > 0" class="session-group">
        <div class="group-header">
          <h2>🔴 进行中</h2>
          <span class="session-count">{{ activeSessions.length }}</span>
        </div>
        
        <div class="session-list">
          <div
            v-for="session in activeSessions"
            :key="session.id"
            class="session-card active"
            @click="viewSession(session)"
          >
            <div class="session-status-dot busy"></div>
            <div class="session-content">
              <h3>{{ session.title || session.id }}</h3>
              <div class="session-meta">
                <span class="meta-item">
                  <span class="meta-icon">💬</span>
                  {{ session.messageCount || 0 }} 条消息
                </span>
                <span v-if="session.fileChanges" class="meta-item">
                  <span class="meta-icon">📝</span>
                  {{ session.fileChanges.files || 0 }} 个文件
                </span>
                <span class="meta-item">
                  <span class="meta-icon">⏱️</span>
                  {{ formatDuration(session.duration) }}
                </span>
              </div>
              <div v-if="session.fileChanges" class="file-changes">
                <span class="additions">+{{ session.fileChanges.additions }}</span>
                <span class="deletions">-{{ session.fileChanges.deletions }}</span>
              </div>
            </div>
            <div class="session-arrow">›</div>
          </div>
        </div>
      </div>

      <!-- 空闲会话 -->
      <div v-if="idleSessions.length > 0" class="session-group">
        <div class="group-header">
          <h2>⚪ 空闲</h2>
          <span class="session-count">{{ idleSessions.length }}</span>
        </div>
        
        <div class="session-list">
          <div
            v-for="session in idleSessions"
            :key="session.id"
            class="session-card idle"
            @click="viewSession(session)"
          >
            <div class="session-status-dot idle"></div>
            <div class="session-content">
              <h3>{{ session.title || session.id }}</h3>
              <div class="session-meta">
                <span class="meta-item">
                  <span class="meta-icon">💬</span>
                  {{ session.messageCount || 0 }} 条消息
                </span>
                <span class="meta-item">
                  <span class="meta-icon">📅</span>
                  {{ formatLastUpdate(session.updatedAt) }}
                </span>
              </div>
            </div>
            <div class="session-arrow">›</div>
          </div>
        </div>
      </div>
    </div>

    <!-- 空状态 -->
    <div v-else class="empty-state">
      <div class="empty-icon">📝</div>
      <p>暂无会话</p>
      <p class="empty-hint">在 OpenCode 中开始一个新对话</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useOpenCodeStore } from '../../stores/opencode'
import type { OpenCodeSession } from '../../stores/opencode'

const router = useRouter()
const route = useRoute()
const openCodeStore = useOpenCodeStore()

const loading = computed(() => openCodeStore.loading)
const selectedInstance = computed(() => openCodeStore.selectedInstance)
const sessions = computed(() => openCodeStore.sessions)
const activeSessions = computed(() => openCodeStore.activeSessions)
const idleSessions = computed(() => openCodeStore.idleSessions)

let ws: WebSocket | null = null

onMounted(async () => {
  const instanceId = route.query.instance_id as string
  if (instanceId) {
    await openCodeStore.selectInstance(instanceId)
  } else {
    // 尝试从本地存储恢复
    const savedInstance = localStorage.getItem('selected_opencode_instance')
    if (savedInstance) {
      const instance = JSON.parse(savedInstance)
      await openCodeStore.selectInstance(instance.id)
    }
  }

  // 订阅实时更新
  ws = openCodeStore.subscribeToRealTimeUpdates()
})

onUnmounted(() => {
  if (ws) {
    ws.close()
  }
})

async function refreshSessions() {
  await openCodeStore.refresh()
}

function viewSession(session: OpenCodeSession) {
  router.push(`/opencode/sessions/${session.id}?instance_id=${session.instanceId}`)
}

function goBack() {
  router.push('/opencode/hub')
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

function formatLastUpdate(timestamp?: string): string {
  if (!timestamp) return '未知'
  
  const date = new Date(timestamp)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMinutes = Math.floor(diffMs / (1000 * 60))
  const diffHours = Math.floor(diffMinutes / 60)
  
  if (diffMinutes < 1) return '刚刚'
  if (diffMinutes < 60) return `${diffMinutes} 分钟前`
  if (diffHours < 24) return `${diffHours} 小时前`
  return date.toLocaleDateString()
}
</script>

<style scoped>
.session-list-view {
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

.back-btn, .refresh-btn {
  padding: 8px 12px;
  font-size: 14px;
  background: transparent;
  border: 1px solid #e0e0e0;
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.3s;
}

.back-btn:active, .refresh-btn:active {
  background: #f5f7fa;
  transform: scale(0.95);
}

.top-bar h1 {
  flex: 1;
  font-size: 20px;
  font-weight: 600;
  margin: 0;
}

.instance-banner {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  padding: 16px 20px;
  color: white;
}

.banner-content {
  display: flex;
  align-items: center;
  gap: 12px;
}

.banner-icon {
  font-size: 32px;
}

.banner-info h3 {
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 4px 0;
}

.banner-info p {
  font-size: 13px;
  margin: 0;
  opacity: 0.9;
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

@keyframes spin {
  0% { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}

.sessions-container {
  flex: 1;
  padding: 20px;
  overflow-y: auto;
}

.session-group {
  margin-bottom: 28px;
}

.group-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  padding: 0 4px;
}

.group-header h2 {
  font-size: 16px;
  font-weight: 600;
  color: #333;
  margin: 0;
}

.session-count {
  font-size: 12px;
  padding: 4px 10px;
  background: #e8f0fe;
  color: #667eea;
  border-radius: 10px;
  font-weight: 600;
}

.session-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.session-card {
  background: white;
  border-radius: 12px;
  padding: 16px;
  display: flex;
  align-items: center;
  gap: 12px;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
  transition: all 0.3s;
}

.session-card:active {
  transform: scale(0.98);
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.1);
}

.session-status-dot {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  flex-shrink: 0;
}

.session-status-dot.busy {
  background: #ff4757;
  animation: pulse 2s infinite;
}

.session-status-dot.idle {
  background: #ddd;
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}

.session-content {
  flex: 1;
  min-width: 0;
}

.session-content h3 {
  font-size: 15px;
  font-weight: 600;
  color: #333;
  margin: 0 0 8px 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-meta {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}

.meta-item {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: #999;
}

.meta-icon {
  font-size: 13px;
}

.file-changes {
  display: flex;
  gap: 8px;
  font-size: 12px;
  font-family: monospace;
}

.additions {
  color: #2ed573;
  font-weight: 600;
}

.deletions {
  color: #ff4757;
  font-weight: 600;
}

.session-arrow {
  font-size: 24px;
  color: #ccc;
  flex-shrink: 0;
}

.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;
  color: #999;
}

.empty-icon {
  font-size: 80px;
  margin-bottom: 20px;
}

.empty-state p {
  font-size: 16px;
  margin: 0 0 8px 0;
}

.empty-hint {
  font-size: 14px;
  color: #bbb;
}
</style>
