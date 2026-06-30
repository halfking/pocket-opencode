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
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  z-index: 2000;
  animation: fadeIn 0.3s;
}

@keyframes fadeIn {
  from { opacity: 0; }
  to { opacity: 1; }
}

.update-dialog {
  background: white;
  border-radius: 20px;
  padding: 30px 24px;
  width: 100%;
  max-width: 400px;
  max-height: 80vh;
  overflow-y: auto;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.3);
  animation: slideUp 0.3s;
}

@keyframes slideUp {
  from {
    transform: translateY(50px);
    opacity: 0;
  }
  to {
    transform: translateY(0);
    opacity: 1;
  }
}

.update-icon {
  font-size: 64px;
  text-align: center;
  margin-bottom: 20px;
}

.update-title {
  font-size: 24px;
  font-weight: 700;
  text-align: center;
  color: #333;
  margin: 0 0 24px 0;
}

.version-info {
  background: #f8f9fa;
  border-radius: 12px;
  padding: 16px;
  margin-bottom: 20px;
}

.version-row {
  display: flex;
  justify-content: space-between;
  padding: 8px 0;
  border-bottom: 1px solid #e9ecef;
}

.version-row:last-child {
  border-bottom: none;
}

.version-row .label {
  font-size: 14px;
  color: #666;
}

.version-row .value {
  font-size: 14px;
  color: #333;
  font-weight: 500;
}

.version-row .value.highlight {
  color: #667eea;
  font-weight: 600;
}

.changelog-section {
  margin-bottom: 20px;
}

.changelog-section h3 {
  font-size: 16px;
  font-weight: 600;
  color: #333;
  margin: 0 0 12px 0;
}

.changelog-list {
  list-style: none;
  padding: 0;
  margin: 0;
}

.changelog-list li {
  font-size: 14px;
  color: #555;
  padding: 8px 0;
  padding-left: 8px;
  border-left: 3px solid #667eea;
  margin-bottom: 8px;
  line-height: 1.5;
}

.force-update-notice {
  background: #fff3cd;
  border: 1px solid #ffc107;
  border-radius: 8px;
  padding: 12px;
  margin-bottom: 20px;
  font-size: 13px;
  color: #856404;
  text-align: center;
}

.dialog-actions {
  display: flex;
  gap: 12px;
}

.cancel-btn,
.update-btn {
  flex: 1;
  padding: 14px;
  font-size: 16px;
  font-weight: 600;
  border: none;
  border-radius: 12px;
  cursor: pointer;
  transition: all 0.3s;
}

.cancel-btn {
  background: #f5f7fa;
  color: #666;
}

.cancel-btn:active {
  background: #e9ecef;
}

.update-btn {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  box-shadow: 0 4px 12px rgba(102, 126, 234, 0.3);
}

.update-btn:active:not(:disabled) {
  transform: translateY(2px);
  box-shadow: 0 2px 8px rgba(102, 126, 234, 0.3);
}

.update-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
