<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useChatAgentStore } from '../../stores/chatAgentStore'
import { useToast } from '../../composables/useToast'

const props = defineProps<{
  show: boolean
}>()

const emit = defineEmits<{
  'update:show': [value: boolean]
}>()

const agentStore = useChatAgentStore()
const toast = useToast()
const lastError = ref('')

onMounted(() => {
  // 首次打开时探测同步可用性
  if (agentStore.syncAvailable === false && !agentStore.syncStatus) {
    agentStore.checkSyncAvailable()
  }
})

const remoteStatus = computed(() => agentStore.syncStatus)
const localCustomCount = computed(() => agentStore.customAgents.length)
const remoteAgentCount = computed(() => remoteStatus.value?.agent_count ?? 0)

// 最近一次同步时间（本地视角）
const lastSyncDisplay = computed(() => {
  const ts = agentStore.lastSyncAt
  if (!ts) return '从未'
  const date = new Date(ts)
  return date.toLocaleString('zh-CN', { hour12: false })
})

const isUnavailable = computed(() => {
  // 探测完成后仍是 unavailable → 后端未启用
  return agentStore.syncAvailable === false && !agentStore.syncStatus
})

function close() {
  emit('update:show', false)
}

async function handleUpload() {
  lastError.value = ''
  try {
    const result = await agentStore.syncUpload()
    toast.success(`已上传 ${result.uploaded_count} 个自定义角色`)
  } catch (e: any) {
    if (e?.status === 409) {
      lastError.value = '云端版本比本地新，请先「下载」合并后再上传'
      toast.error('版本冲突：云端有更新的版本')
    } else {
      lastError.value = e?.message || String(e)
      toast.error('上传失败')
    }
  }
}

async function handleDownload() {
  lastError.value = ''
  try {
    const result = await agentStore.syncDownload()
    if (result.downloaded > 0) {
      toast.success(`已下载 ${result.downloaded} 个新/更新角色`)
    } else {
      toast.info('云端无新内容')
    }
  } catch (e: any) {
    lastError.value = e?.message || String(e)
    toast.error('下载失败')
  }
}

function formatTime(ms: number): string {
  if (!ms) return '—'
  return new Date(ms).toLocaleString('zh-CN', { hour12: false })
}
</script>

<template>
  <div v-if="show" class="sync-overlay" @click="close">
    <div class="sync-sheet" @click.stop>
      <!-- 标题栏（单标题） -->
      <div class="sheet-header">
        <h2>角色云端同步</h2>
        <button class="close-btn" aria-label="关闭" @click="close">×</button>
      </div>

      <!-- 不可用提示 -->
      <div v-if="isUnavailable" class="status-banner error">
        <span class="material-symbols-outlined">cloud_off</span>
        <div>
          <div class="banner-title">云端同步未启用</div>
          <div class="banner-hint">需要在 pocketd 配置 PostgreSQL（Acc 模式）才能使用</div>
        </div>
      </div>

      <!-- 状态卡：本地 vs 云端 -->
      <div v-else class="status-grid">
        <div class="status-card">
          <div class="status-label">本地自定义角色</div>
          <div class="status-value">{{ localCustomCount }}</div>
        </div>
        <div class="status-card">
          <div class="status-label">云端角色</div>
          <div class="status-value">{{ remoteAgentCount }}</div>
        </div>
        <div class="status-card">
          <div class="status-label">最近同步</div>
          <div class="status-value-sm">{{ lastSyncDisplay }}</div>
        </div>
        <div v-if="remoteStatus" class="status-card">
          <div class="status-label">云端版本</div>
          <div class="status-value-sm">{{ formatTime(remoteStatus.server_updated_at) }}</div>
        </div>
      </div>

      <!-- 操作说明 -->
      <div v-if="!isUnavailable" class="hint-section">
        <p class="hint">
          <strong>上传</strong>：把本机的自定义角色覆盖到云端。<br />
          <strong>下载</strong>：从云端拉取新/更新的角色并合并到本机。
        </p>
        <p class="hint">
          ⚠️ 版本冲突时（409）需先「下载」合并，再「上传」。
        </p>
      </div>

      <!-- 错误提示 -->
      <div v-if="lastError" class="status-banner error">
        <span class="material-symbols-outlined">error</span>
        <div>{{ lastError }}</div>
      </div>

      <!-- 操作按钮 -->
      <div v-if="!isUnavailable" class="action-row">
        <button
          class="action-btn primary"
          :disabled="agentStore.syncing || localCustomCount === 0"
          @click="handleUpload"
        >
          <span class="material-symbols-outlined">cloud_upload</span>
          <span>{{ agentStore.syncing ? '同步中…' : '上传到云端' }}</span>
        </button>
        <button
          class="action-btn"
          :disabled="agentStore.syncing"
          @click="handleDownload"
        >
          <span class="material-symbols-outlined">cloud_download</span>
          <span>{{ agentStore.syncing ? '同步中…' : '从云端下载' }}</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.sync-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 1000;
  display: flex;
  align-items: flex-end;
}

.sync-sheet {
  width: 100%;
  background: var(--bg-primary, #fff);
  border-radius: 16px 16px 0 0;
  padding: 16px 20px calc(20px + env(safe-area-inset-bottom));
  max-height: 85vh;
  overflow-y: auto;
}

.sheet-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border-color, #e5e7eb);
}

.sheet-header h2 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}

.close-btn {
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  font-size: 28px;
  line-height: 1;
  color: var(--text-secondary, #6b7280);
  cursor: pointer;
}

.status-banner {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  border-radius: 10px;
  margin-bottom: 16px;
}

.status-banner.error {
  background: color-mix(in srgb, var(--danger) 10%, transparent);
  color: var(--danger);
  font-size: 13px;
}

.banner-title {
  font-weight: 600;
  margin-bottom: 2px;
}

.banner-hint {
  font-size: 12px;
  opacity: 0.85;
}

.status-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-bottom: 16px;
}

.status-card {
  padding: 12px;
  background: var(--bg-secondary, #f9fafb);
  border: 1px solid var(--border-color, #e5e7eb);
  border-radius: 10px;
}

.status-label {
  font-size: 11px;
  color: var(--text-secondary, #6b7280);
  margin-bottom: 6px;
}

.status-value {
  font-size: 24px;
  font-weight: 700;
  color: var(--text-primary);
}

.status-value-sm {
  font-size: 13px;
  color: var(--text-primary);
  font-weight: 500;
}

.hint-section {
  margin-bottom: 16px;
}

.hint {
  margin: 8px 0;
  font-size: 13px;
  color: var(--text-secondary, #6b7280);
  line-height: 1.5;
}

.hint strong {
  color: var(--text-primary);
}

.action-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  padding-top: 16px;
  border-top: 1px solid var(--border-color, #e5e7eb);
}

.action-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 12px 16px;
  border: 1px solid var(--border-color, #e5e7eb);
  border-radius: 10px;
  background: var(--bg-secondary, #f9fafb);
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.2s;
}

.action-btn:hover:not(:disabled) {
  background: var(--bg-hover, #f3f4f6);
}

.action-btn.primary {
  background: var(--primary-color, #3b82f6);
  color: white;
  border-color: var(--primary-color, #3b82f6);
}

.action-btn.primary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--primary-color, #3b82f6) 90%, black);
}

.action-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.material-symbols-outlined {
  font-size: 20px;
}
</style>
