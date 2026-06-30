<template>
  <div class="instance-list-view">
    <!-- 顶部栏 -->
    <div class="top-bar">
      <button class="back-btn" @click="goBack">← 返回</button>
      <h1>OpenCode 实例</h1>
      <button class="refresh-btn" @click="loadInstances">🔄</button>
    </div>

    <!-- 当前服务器信息 -->
    <div class="server-info-bar">
      <span class="server-label">当前服务器:</span>
      <span class="server-name">{{ currentServer?.name }}</span>
    </div>

    <!-- 加载状态 -->
    <div v-if="loading" class="loading-state">
      <div class="spinner"></div>
      <p>加载实例列表...</p>
    </div>

    <!-- 错误状态 -->
    <div v-else-if="error" class="error-state">
      <div class="error-icon">⚠️</div>
      <p>{{ error }}</p>
      <button class="retry-btn" @click="loadInstances">重试</button>
    </div>

    <!-- 实例列表 -->
    <div v-else-if="instances.length > 0" class="instance-list">
      <div
        v-for="instance in instances"
        :key="instance.id"
        class="instance-card"
        @click="selectInstance(instance)"
      >
        <div class="instance-icon">💻</div>
        <div class="instance-info">
          <h3>{{ instance.displayName }}</h3>
          <p class="instance-id">{{ instance.id }}</p>
          <div class="instance-meta">
            <span class="meta-tag">{{ instance.environment }}</span>
            <span class="meta-tag">{{ instance.capabilities?.length || 0 }} 功能</span>
          </div>
        </div>
        <div class="instance-arrow">›</div>
      </div>
    </div>

    <!-- 空状态 -->
    <div v-else class="empty-state">
      <div class="empty-icon">📭</div>
      <p>暂无可用的 OpenCode 实例</p>
      <button class="retry-btn" @click="loadInstances">重试</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onActivated } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../../api/client'

const router = useRouter()

interface Instance {
  id: string
  displayName: string
  environment: string
  capabilities?: string[]
  npsClientId?: number
}

const currentServer = ref<any>(null)
const instances = ref<Instance[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

onMounted(() => {
  // 加载当前服务器
  const serverStr = localStorage.getItem('selected_server')
  if (serverStr) {
    currentServer.value = JSON.parse(serverStr)
  }
  
  // 加载实例列表
  loadInstances()
})

// 每次页面激活时重新加载（返回时）
onActivated(() => {
  console.log('🔄 页面激活，重新加载实例...')
  loadInstances()
})

async function loadInstances() {
  loading.value = true
  error.value = null
  try {
    console.log('🔍 开始加载实例...')
    instances.value = await api.getInstances()
    console.log('✅ 加载到实例:', instances.value.length, instances.value)
  } catch (err: any) {
    console.error('❌ 加载实例失败:', err)
    error.value = `加载失败: ${err.message || '未知错误'}`
    instances.value = []
  } finally {
    loading.value = false
  }
}

function selectInstance(instance: Instance) {
  // 保存选择的实例
  localStorage.setItem('selected_instance', JSON.stringify(instance))
  
  // 跳转到任务列表
  router.push('/tasks')
}

function goBack() {
  router.push('/servers')
}
</script>

<style scoped>
.instance-list-view {
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
}

.top-bar h1 {
  flex: 1;
  font-size: 20px;
  font-weight: 600;
  color: #333;
  margin: 0;
}

.server-info-bar {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  padding: 12px 20px;
  color: white;
  font-size: 14px;
  display: flex;
  align-items: center;
  gap: 8px;
}

.server-label {
  opacity: 0.9;
}

.server-name {
  font-weight: 600;
}

.loading-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  color: #999;
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

.instance-list {
  flex: 1;
  padding: 20px;
  overflow-y: auto;
}

.instance-card {
  background: white;
  border-radius: 16px;
  padding: 20px;
  margin-bottom: 12px;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.08);
  display: flex;
  align-items: center;
  gap: 16px;
  cursor: pointer;
  transition: all 0.3s;
}

.instance-card:active {
  transform: scale(0.98);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
}

.instance-icon {
  font-size: 36px;
  width: 56px;
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f0f2f5;
  border-radius: 12px;
  flex-shrink: 0;
}

.instance-info {
  flex: 1;
}

.instance-info h3 {
  font-size: 16px;
  font-weight: 600;
  color: #333;
  margin: 0 0 4px 0;
}

.instance-id {
  font-size: 12px;
  color: #999;
  margin: 0 0 8px 0;
  font-family: monospace;
}

.instance-meta {
  display: flex;
  gap: 8px;
}

.meta-tag {
  font-size: 11px;
  padding: 4px 8px;
  background: #e8f0fe;
  color: #667eea;
  border-radius: 4px;
  font-weight: 500;
}

.instance-arrow {
  font-size: 24px;
  color: #ccc;
}

.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px;
  color: #999;
}

.empty-icon {
  font-size: 64px;
  margin-bottom: 16px;
}

.empty-state p {
  font-size: 16px;
  margin-bottom: 20px;
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
}

.retry-btn:active {
  opacity: 0.8;
}
</style>

.error-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px;
  color: #c33;
}

.error-icon {
  font-size: 64px;
  margin-bottom: 16px;
}

.error-state p {
  font-size: 16px;
  margin-bottom: 20px;
  text-align: center;
}
