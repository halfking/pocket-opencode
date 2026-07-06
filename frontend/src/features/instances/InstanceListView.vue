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
    <ErrorState
      v-else-if="error"
      icon="⚠️"
      title="加载实例失败"
      :message="error"
      retry-label="重试"
      @retry="loadInstances"
    />

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
    <EmptyState
      v-else
      icon="📭"
      title="暂无可用的 OpenCode 实例"
      message="当前服务器未注册任何实例"
      hint="检查后端 POCKET_OPENCODE_INSTANCES 配置"
      action-label="重新加载"
      @action="loadInstances"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onActivated } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../../api/client'
import EmptyState from '../../components/base/EmptyState.vue'
import ErrorState from '../../components/base/ErrorState.vue'

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
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
}

.top-bar {
  background: var(--bg-card);
  padding: var(--space-3) var(--space-4);
  display: flex;
  align-items: center;
  gap: var(--space-3);
  border-bottom: 1px solid var(--border);
}

.back-btn, .refresh-btn {
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  background: transparent;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  cursor: pointer;
  color: var(--text-primary);
  transition: background 120ms;
}

.back-btn:active, .refresh-btn:active {
  background: var(--bg-subtle);
}

.top-bar h1 {
  flex: 1;
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0;
}

.server-info-bar {
  background: var(--brand-gradient);
  padding: var(--space-2) var(--space-4);
  color: var(--text-inverse);
  font-size: var(--text-sm);
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.server-label {
  opacity: 0.9;
}

.server-name {
  font-weight: var(--font-weight-semibold);
}

.loading-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  color: var(--text-muted);
}

.spinner {
  width: 32px;
  height: 32px;
  border: 3px solid var(--border);
  border-top: 3px solid var(--brand-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
  margin-bottom: var(--space-3);
}

@keyframes spin {
  0% { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}

.instance-list {
  flex: 1;
  padding: var(--space-3);
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
}

.instance-card {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--spacing-card-padding);
  border: 1px solid var(--border);
  display: flex;
  align-items: center;
  gap: var(--space-3);
  cursor: pointer;
  transition: background 120ms;
}

.instance-card:active {
  background: var(--bg-subtle);
}

.instance-icon {
  font-size: var(--text-xl);
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-subtle);
  border-radius: var(--radius-md);
  flex-shrink: 0;
}

.instance-info {
  flex: 1;
  min-width: 0;
}

.instance-info h3 {
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0 0 var(--space-1) 0;
}

.instance-id {
  font-size: var(--text-xs);
  color: var(--text-muted);
  margin: 0 0 var(--space-2) 0;
  font-family: monospace;
}

.instance-meta {
  display: flex;
  gap: var(--space-2);
}

.meta-tag {
  font-size: var(--text-xs);
  padding: var(--space-1) var(--space-2);
  background: rgba(102, 126, 234, 0.1);
  color: var(--brand-primary);
  border-radius: var(--radius-sm);
  font-weight: var(--font-weight-medium);
}

.instance-arrow {
  font-size: var(--text-lg);
  color: var(--text-muted);
  opacity: 0.5;
}

.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-6);
  color: var(--text-muted);
}

.empty-icon {
  font-size: 48px;
  margin-bottom: var(--space-3);
}

.empty-state p {
  font-size: var(--text-base);
  margin-bottom: var(--space-4);
}

.retry-btn {
  padding: var(--space-2) var(--space-5);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  color: var(--text-inverse);
  background: var(--brand-primary);
  border: none;
  border-radius: var(--radius-md);
  cursor: pointer;
}

.retry-btn:active {
  opacity: 0.8;
}

.error-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-6);
  color: var(--danger);
}

.error-icon {
  font-size: 48px;
  margin-bottom: var(--space-3);
}

.error-state p {
  font-size: var(--text-base);
  margin-bottom: var(--space-4);
  text-align: center;
}
</style>
