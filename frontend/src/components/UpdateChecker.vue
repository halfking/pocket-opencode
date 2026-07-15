<template>
  <div v-if="showUpdateDialog" class="update-overlay" @click="handleCancel">
    <div class="update-dialog" @click.stop>
      <!-- 更新图标 -->
      <div class="update-icon">🎉</div>
      
      <!-- 标题 -->
      <h2 class="update-title">发现新版本</h2>
      
      <!-- 版本信息 -->
      <div class="version-info">
        <div class="version-row">
          <span class="label">当前版本:</span>
          <span class="value">v{{ currentVersion }} (Build {{ currentBuild }})</span>
        </div>
        <div class="version-row">
          <span class="label">最新版本:</span>
          <span class="value highlight">v{{ updateInfo?.version }} (Build {{ updateInfo?.buildNumber }})</span>
        </div>
        <div class="version-row">
          <span class="label">更新大小:</span>
          <span class="value">{{ formatSize(updateInfo?.fileSize || 0) }}</span>
        </div>
        <div class="version-row">
          <span class="label">发布日期:</span>
          <span class="value">{{ updateInfo?.releaseDate }}</span>
        </div>
      </div>

      <!-- 更新日志 -->
      <div class="changelog-section">
        <h3>更新内容</h3>
        <ul class="changelog-list">
          <li v-for="(item, index) in updateInfo?.changelog" :key="index">
            {{ item }}
          </li>
        </ul>
      </div>

      <!-- 强制更新提示 -->
      <div v-if="forceUpdate" class="force-update-notice">
        ⚠️ 此更新为强制更新，必须升级才能继续使用
      </div>

      <!-- 操作按钮 -->
      <div class="dialog-actions">
        <button 
          v-if="!forceUpdate" 
          class="cancel-btn" 
          @click="handleCancel"
        >
          稍后提醒
        </button>
        <button 
          class="update-btn" 
          @click="handleUpdate"
          :disabled="downloading"
        >
          {{ downloading ? '下载中...' : '立即更新' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { checkUpdate, downloadAPK, APP_VERSION, formatFileSize, type VersionInfo } from '../utils/version'

const showUpdateDialog = ref(false)
const updateInfo = ref<VersionInfo | null>(null)
const forceUpdate = ref(false)
const downloading = ref(false)
const currentVersion = APP_VERSION.version
const currentBuild = APP_VERSION.buildNumber

onMounted(async () => {
  // 检查更新（启动时）
  await performUpdateCheck()
})

async function performUpdateCheck() {
  try {
    const response = await checkUpdate()
    
    if (response.hasUpdate && response.latest) {
      updateInfo.value = response.latest
      forceUpdate.value = response.forceUpdate
      showUpdateDialog.value = true
    }
  } catch (error) {
    console.error('Failed to check update:', error)
  }
}

function handleUpdate() {
  if (!updateInfo.value) return
  
  downloading.value = true
  
  // 下载 APK
  downloadAPK(updateInfo.value.downloadUrl)
  
  // 延迟重置状态
  setTimeout(() => {
    downloading.value = false
    if (!forceUpdate.value) {
      showUpdateDialog.value = false
    }
  }, 2000)
}

function handleCancel() {
  if (forceUpdate.value) return
  showUpdateDialog.value = false
}

function formatSize(bytes: number): string {
  return formatFileSize(bytes)
}

// 暴露方法供外部调用
defineExpose({
  checkUpdate: performUpdateCheck
})
</script>

<style scoped>
.update-overlay {
  position: fixed;
  inset: 0;
  background: var(--overlay);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  z-index: 2000;
  animation: fadeIn 0.3s;
}

@keyframes fadeIn {
  from { opacity: 0; }
  to { opacity: 1; }
}

.update-dialog {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: var(--space-5) var(--space-4);
  width: 100%;
  max-width: 400px;
  max-height: 80vh;
  overflow-y: auto;
  box-shadow: var(--shadow-lg);
  animation: slideUp 0.3s;
}

@keyframes slideUp {
  from { transform: translateY(50px); opacity: 0; }
  to { transform: translateY(0); opacity: 1; }
}

.update-icon {
  font-size: 56px;
  text-align: center;
  margin-bottom: var(--space-4);
}

.update-title {
  font-size: var(--text-xl);
  font-weight: var(--font-weight-bold);
  text-align: center;
  color: var(--text-primary);
  margin: 0 0 var(--space-4) 0;
}

.version-info {
  background: var(--bg-subtle);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  margin-bottom: var(--space-4);
}

.version-row {
  display: flex;
  justify-content: space-between;
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--border);
}

.version-row:last-child { border-bottom: none; }

.version-row .label {
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

.version-row .value {
  font-size: var(--text-sm);
  color: var(--text-primary);
  font-weight: var(--font-weight-medium);
}

.version-row .value.highlight {
  color: var(--brand-primary);
  font-weight: var(--font-weight-semibold);
}

.changelog-section { margin-bottom: var(--space-4); }

.changelog-section h3 {
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0 0 var(--space-3) 0;
}

.changelog-list { list-style: none; padding: 0; margin: 0; }

.changelog-list li {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  padding: var(--space-2) 0;
  padding-left: var(--space-2);
  border-left: 3px solid var(--brand-primary);
  margin-bottom: var(--space-2);
  line-height: 1.5;
}

.force-update-notice {
  background: var(--warning-bg);
  border: 1px solid var(--warning);
  border-radius: var(--radius-sm);
  padding: var(--space-3);
  margin-bottom: var(--space-4);
  font-size: var(--text-sm);
  color: var(--warning);
  text-align: center;
}

.dialog-actions { display: flex; gap: var(--space-3); }

.cancel-btn,
.update-btn {
  flex: 1;
  padding: var(--space-3);
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  border: none;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: opacity 150ms;
}

.cancel-btn {
  background: var(--bg-subtle);
  color: var(--text-secondary);
}

.cancel-btn:active { background: var(--border); }

.update-btn {
  background: var(--brand-gradient);
  color: var(--text-inverse);
  box-shadow: var(--shadow-md);
}

.update-btn:active:not(:disabled) { opacity: 0.9; }

.update-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
