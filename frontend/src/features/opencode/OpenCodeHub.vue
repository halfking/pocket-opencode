<template>
  <div class="opencode-hub">
    <!-- 顶部栏 -->
    <div class="top-bar">
      <button class="back-btn" @click="goBack">← 返回</button>
      <h1>OpenCode 实例中心</h1>
      <button class="refresh-btn" @click="refreshInstances">🔄</button>
    </div>

    <!-- 加载状态 -->
    <div v-if="loading" class="loading-state">
      <div class="spinner"></div>
      <p>加载实例...</p>
    </div>

    <!-- 错误状态 -->
    <div v-else-if="error" class="error-state">
      <div class="error-icon">⚠️</div>
      <p>{{ error }}</p>
      <button class="retry-btn" @click="refreshInstances">重试</button>
    </div>

    <!-- 实例列表 -->
    <div v-else class="instances-container">
      <!-- 在线实例 -->
      <div v-if="onlineInstances.length > 0" class="instance-group">
        <div class="group-header">
          <h2>🟢 在线实例</h2>
          <span class="instance-count">{{ onlineInstances.length }}</span>
        </div>
        
        <div class="instance-list">
          <div
            v-for="instance in onlineInstances"
            :key="instance.id"
            class="instance-card online"
            @click="selectInstance(instance)"
          >
            <div class="instance-icon">💻</div>
            <div class="instance-info">
              <h3>{{ instance.displayName }}</h3>
              <p class="instance-id">{{ instance.id }}</p>
              <div class="instance-stats">
                <span class="stat-item">
                  <span class="stat-icon">🔴</span>
                  {{ instance.activeSessions || 0 }} 活跃
                </span>
                <span class="stat-item">
                  <span class="stat-icon">📝</span>
                  {{ instance.totalSessions || 0 }} 总计
                </span>
                <span class="stat-item">
                  <span class="stat-icon">🏷️</span>
                  {{ instance.environment }}
                </span>
              </div>
            </div>
            <div class="instance-arrow">›</div>
          </div>
        </div>
      </div>

      <!-- 离线实例 -->
      <div v-if="offlineInstances.length > 0" class="instance-group">
        <div class="group-header">
          <h2>🔴 离线实例</h2>
          <span class="instance-count">{{ offlineInstances.length }}</span>
        </div>
        
        <div class="instance-list">
          <div
            v-for="instance in offlineInstances"
            :key="instance.id"
            class="instance-card offline"
          >
            <div class="instance-icon">💤</div>
            <div class="instance-info">
              <h3>{{ instance.displayName }}</h3>
              <p class="instance-id">{{ instance.id }}</p>
              <div class="instance-stats">
                <span class="stat-item offline-text">
                  上次活跃: {{ formatLastSeen(instance.lastHeartbeatAt) }}
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 空状态 -->
      <div v-if="instances.length === 0" class="empty-state">
        <div class="empty-icon">📭</div>
        <p>暂无 OpenCode 实例</p>
        <p class="empty-hint">请先注册 OpenCode 实例</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useOpenCodeStore } from '../../stores/opencode'
import type { OpenCodeInstance } from '../../stores/opencode'

const router = useRouter()
const openCodeStore = useOpenCodeStore()

const loading = computed(() => openCodeStore.loading)
const error = computed(() => openCodeStore.error)
const instances = computed(() => openCodeStore.instances)
const onlineInstances = computed(() => openCodeStore.onlineInstances)
const offlineInstances = computed(() => openCodeStore.offlineInstances)

onMounted(async () => {
  await refreshInstances()
})

async function refreshInstances() {
  await openCodeStore.loadInstances()
}

function selectInstance(instance: OpenCodeInstance) {
  // 保存选择的实例
  localStorage.setItem('selected_opencode_instance', JSON.stringify(instance))
  
  // 选择实例并加载会话
  openCodeStore.selectInstance(instance.id)
  
  // 跳转到会话列表
  router.push(`/opencode/sessions?instance_id=${instance.id}`)
}

function goBack() {
  router.push('/instances')
}

function formatLastSeen(timestamp: string): string {
  if (!timestamp) return '未知'
  
  const date = new Date(timestamp)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60))
  const diffDays = Math.floor(diffHours / 24)
  
  if (diffHours < 1) return '刚刚'
  if (diffHours < 24) return `${diffHours} 小时前`
  if (diffDays < 7) return `${diffDays} 天前`
  return date.toLocaleDateString()
}
</script>

<style scoped>
.opencode-hub {
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
  color: #333;
  margin: 0;
}

.loading-state, .error-state {
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

.error-state {
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
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  border: none;
  border-radius: 8px;
  cursor: pointer;
  margin-top: 16px;
}

.instances-container {
  flex: 1;
  padding: 20px;
  overflow-y: auto;
}

.instance-group {
  margin-bottom: 32px;
}

.group-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  padding: 0 4px;
}

.group-header h2 {
  font-size: 18px;
  font-weight: 600;
  color: #333;
  margin: 0;
}

.instance-count {
  font-size: 14px;
  padding: 4px 12px;
  background: #e8f0fe;
  color: #667eea;
  border-radius: 12px;
  font-weight: 600;
}

.instance-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.instance-card {
  background: white;
  border-radius: 16px;
  padding: 20px;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.08);
  display: flex;
  align-items: center;
  gap: 16px;
  transition: all 0.3s;
}

.instance-card.online {
  cursor: pointer;
  border-left: 4px solid #2ed573;
}

.instance-card.online:active {
  transform: scale(0.98);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
}

.instance-card.offline {
  opacity: 0.6;
  border-left: 4px solid #ff4757;
}

.instance-icon {
  font-size: 40px;
  width: 64px;
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #667eea20 0%, #764ba220 100%);
  border-radius: 16px;
  flex-shrink: 0;
}

.instance-info {
  flex: 1;
  min-width: 0;
}

.instance-info h3 {
  font-size: 17px;
  font-weight: 600;
  color: #333;
  margin: 0 0 4px 0;
}

.instance-id {
  font-size: 12px;
  color: #999;
  margin: 0 0 12px 0;
  font-family: monospace;
}

.instance-stats {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
}

.stat-item {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: #666;
}

.stat-icon {
  font-size: 14px;
}

.offline-text {
  color: #999;
  font-style: italic;
}

.instance-arrow {
  font-size: 28px;
  color: #ccc;
  flex-shrink: 0;
}

.empty-state {
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
