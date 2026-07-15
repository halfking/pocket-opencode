<template>
  <div class="instance-list-view">
    <ScrollChromePortal>
      <div class="chrome-toolbar">
        <div class="server-info-bar">
          <span class="server-label">当前服务器:</span>
          <span class="server-name">{{ currentServer?.name }}</span>
        </div>
        <button class="refresh-btn" type="button" @click="loadInstances" aria-label="刷新">🔄</button>
      </div>
    </ScrollChromePortal>

    <!-- 加载状态 -->
    <div v-if="loading" class="loading-state">
      <Skeleton :count="3" />
    </div>

    <!-- 错误状态 -->
    <EmptyState
      v-else-if="error"
      icon="⚠️"
      :title="error"
      hint="请检查服务器连接"
      action-label="重试"
      @action="loadInstances"
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
      hint="请确认服务器已注册实例后重试"
      action-label="重试"
      @action="loadInstances"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onActivated } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../../api/client'
import { Skeleton, EmptyState } from '../../components'
import ScrollChromePortal from '@/components/layout/ScrollChromePortal.vue'

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
  localStorage.setItem('selected_instance', JSON.stringify(instance))
  router.push('/tasks')
}
</script>

<style scoped>
.instance-list-view {
  min-height: 100%;
}

.chrome-toolbar {
  display: flex;
  align-items: stretch;
}

.server-info-bar {
  flex: 1;
  background: var(--brand-gradient);
  padding: var(--space-2) var(--space-4);
  color: var(--text-inverse);
  font-size: var(--text-sm);
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.refresh-btn {
  flex-shrink: 0;
  width: 44px;
  padding: var(--space-2);
  font-size: var(--text-sm);
  background: var(--bg-card);
  border: none;
  border-left: 1px solid var(--border);
  color: var(--text-primary);
  cursor: pointer;
}

.refresh-btn:active {
  background: var(--bg-subtle);
}

.server-label {
  opacity: 0.9;
}

.server-name {
  font-weight: var(--font-weight-semibold);
}

.loading-state {
  flex: 1;
  padding: var(--space-3);
}

.instance-list {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: var(--spacing-list-gap);
}

.instance-card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--spacing-card-padding);
  display: flex;
  align-items: center;
  gap: var(--space-3);
  cursor: pointer;
  transition: background 120ms;
  min-height: 56px;
  max-height: 66px;
}

.instance-card:active {
  background: var(--bg-subtle);
}

.instance-icon {
  font-size: 20px;
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-subtle);
  border-radius: var(--radius-sm);
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
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.instance-id {
  font-size: var(--text-xs);
  color: var(--text-muted);
  margin: 0;
  font-family: monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.instance-meta {
  display: flex;
  gap: var(--space-1);
  margin-top: 2px;
}

.meta-tag {
  font-size: var(--text-xs);
  padding: 1px var(--space-2);
  background: var(--brand-bg);
  color: var(--brand-primary);
  border-radius: var(--radius-sm);
  font-weight: var(--font-weight-medium);
}

.instance-arrow {
  font-size: var(--text-lg);
  color: var(--text-muted);
  flex-shrink: 0;
}
</style>
