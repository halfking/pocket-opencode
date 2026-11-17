<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="show" class="modal-overlay" @click="handleOverlayClick">
        <div class="modal-container" @click.stop>
          <!-- 头部 -->
          <div class="modal-header">
            <h2>笔记冲突解决</h2>
            <button @click="close" class="close-btn" aria-label="关闭">✕</button>
          </div>

          <!-- 内容 -->
          <div class="modal-body">
            <p class="conflict-description">
              检测到本地版本与远程版本存在差异，请选择如何解决：
            </p>

            <div class="versions-container">
              <!-- 本地版本 -->
              <div class="version-panel local">
                <div class="version-header">
                  <h3>📱 本地版本</h3>
                  <span class="version-time">{{ formatTime(localVersion.updatedAt) }}</span>
                </div>
                <div class="version-content">
                  <div v-if="showPreview" class="markdown-preview" v-html="renderMarkdown(localVersion.content)"></div>
                  <pre v-else class="raw-content">{{ localVersion.content }}</pre>
                </div>
                <div class="version-meta">
                  <span>{{ localVersion.content.length }} 字符</span>
                </div>
              </div>

              <!-- 对比箭头 -->
              <div class="vs-indicator">
                <span>VS</span>
              </div>

              <!-- 远程版本 -->
              <div class="version-panel remote">
                <div class="version-header">
                  <h3>☁️ 远程版本 (Memora)</h3>
                  <span class="version-time">{{ formatTime(remoteVersion.updatedAt) }}</span>
                </div>
                <div class="version-content">
                  <div v-if="showPreview" class="markdown-preview" v-html="renderMarkdown(remoteVersion.content)"></div>
                  <pre v-else class="raw-content">{{ remoteVersion.content }}</pre>
                </div>
                <div class="version-meta">
                  <span>{{ remoteVersion.content.length }} 字符</span>
                </div>
              </div>
            </div>

            <!-- 预览切换 -->
            <div class="view-toggle">
              <button 
                @click="showPreview = !showPreview"
                class="toggle-btn"
              >
                {{ showPreview ? '显示原始文本' : '显示 Markdown 预览' }}
              </button>
            </div>

            <!-- 手动合并区域 -->
            <div v-if="mergeMode" class="merge-editor">
              <div class="merge-header">
                <h3>✏️ 手动合并</h3>
                <p class="merge-hint">请编辑下方内容，完成后点击"应用合并"</p>
              </div>
              <textarea 
                v-model="mergedContent"
                class="merge-textarea"
                rows="15"
                placeholder="在此编辑合并后的内容..."
              ></textarea>
            </div>
          </div>

          <!-- 底部操作 -->
          <div class="modal-footer">
            <div class="actions-left">
              <button @click="close" class="btn btn-secondary">
                取消
              </button>
            </div>
            <div class="actions-right">
              <button 
                v-if="!mergeMode"
                @click="handleResolve('local')" 
                class="btn btn-primary"
                :disabled="resolving"
              >
                <span v-if="!resolving">接受本地版本</span>
                <span v-else class="spinner"></span>
              </button>
              <button 
                v-if="!mergeMode"
                @click="handleResolve('remote')" 
                class="btn btn-primary"
                :disabled="resolving"
              >
                <span v-if="!resolving">接受远程版本</span>
                <span v-else class="spinner"></span>
              </button>
              <button 
                v-if="!mergeMode"
                @click="enterMergeMode" 
                class="btn btn-outline"
              >
                手动合并
              </button>
              <button 
                v-if="mergeMode"
                @click="handleMerge" 
                class="btn btn-success"
                :disabled="resolving || !mergedContent.trim()"
              >
                <span v-if="!resolving">应用合并</span>
                <span v-else class="spinner"></span>
              </button>
            </div>
          </div>

          <!-- 错误提示 -->
          <div v-if="error" class="error-banner">
            {{ error }}
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useSyncStateStore } from '@/stores/syncState'
import { marked } from 'marked'

interface Version {
  content: string
  updatedAt: number
}

interface Props {
  show: boolean
  noteId: string
  localVersion: Version
  remoteVersion: Version
}

const props = defineProps<Props>()
const emit = defineEmits<{
  close: []
  resolved: []
}>()

const syncStateStore = useSyncStateStore()

// 状态
const showPreview = ref(true)
const mergeMode = ref(false)
const mergedContent = ref('')
const resolving = ref(false)
const error = ref<string | null>(null)

// 格式化时间
const formatTime = (timestamp: number) => {
  const date = new Date(timestamp)
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })
}

// 渲染 Markdown
const renderMarkdown = (content: string) => {
  try {
    return marked(content)
  } catch (err) {
    console.error('Markdown render error:', err)
    return content
  }
}

// 进入合并模式
const enterMergeMode = () => {
  mergeMode.value = true
  // 初始化为本地版本内容
  mergedContent.value = props.localVersion.content
}

// 关闭对话框
const close = () => {
  if (resolving.value) return
  emit('close')
  // 重置状态
  setTimeout(() => {
    mergeMode.value = false
    mergedContent.value = ''
    error.value = null
  }, 300)
}

// 处理遮罩点击
const handleOverlayClick = () => {
  close()
}

// 解决冲突
const handleResolve = async (resolution: 'local' | 'remote') => {
  try {
    resolving.value = true
    error.value = null

    await syncStateStore.resolveConflict(props.noteId, resolution)
    
    emit('resolved')
    close()
  } catch (err: any) {
    error.value = err.message || '解决冲突失败，请重试'
  } finally {
    resolving.value = false
  }
}

// 应用合并
const handleMerge = async () => {
  try {
    resolving.value = true
    error.value = null

    // 调用 merge 解决方式
    // 注意：后端需要支持接收合并后的内容
    await syncStateStore.resolveConflict(props.noteId, 'merge')
    
    // TODO: 将 mergedContent 更新到笔记
    // 这需要调用 notes API 更新内容
    
    emit('resolved')
    close()
  } catch (err: any) {
    error.value = err.message || '应用合并失败，请重试'
  } finally {
    resolving.value = false
  }
}
</script>

<style scoped>
/* 模态框遮罩 */
.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  padding: 1rem;
}

.modal-container {
  background: white;
  border-radius: 0.75rem;
  box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.1), 0 10px 10px -5px rgba(0, 0, 0, 0.04);
  max-width: 1200px;
  width: 100%;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* 头部 */
.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 1.5rem;
  border-bottom: 1px solid #e5e7eb;
}

.modal-header h2 {
  margin: 0;
  font-size: 1.25rem;
  font-weight: 600;
  color: #111827;
}

.close-btn {
  background: none;
  border: none;
  font-size: 1.5rem;
  color: #6b7280;
  cursor: pointer;
  padding: 0;
  width: 2rem;
  height: 2rem;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 0.375rem;
  transition: all 0.2s;
}

.close-btn:hover {
  background-color: #f3f4f6;
  color: #111827;
}

/* 主体内容 */
.modal-body {
  flex: 1;
  overflow-y: auto;
  padding: 1.5rem;
}

.conflict-description {
  margin: 0 0 1.5rem 0;
  color: #6b7280;
  font-size: 0.9375rem;
}

/* 版本对比容器 */
.versions-container {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  gap: 1rem;
  margin-bottom: 1rem;
}

.version-panel {
  border: 2px solid #e5e7eb;
  border-radius: 0.5rem;
  overflow: hidden;
}

.version-panel.local {
  border-color: #3b82f6;
}

.version-panel.remote {
  border-color: #10b981;
}

.version-header {
  background-color: #f9fafb;
  padding: 0.75rem 1rem;
  border-bottom: 1px solid #e5e7eb;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.version-header h3 {
  margin: 0;
  font-size: 0.9375rem;
  font-weight: 600;
  color: #111827;
}

.version-time {
  font-size: 0.8125rem;
  color: #6b7280;
}

.version-content {
  padding: 1rem;
  max-height: 400px;
  overflow-y: auto;
  background-color: white;
}

.markdown-preview {
  font-size: 0.9375rem;
  line-height: 1.6;
  color: #111827;
}

.markdown-preview :deep(h1),
.markdown-preview :deep(h2),
.markdown-preview :deep(h3) {
  margin-top: 1rem;
  margin-bottom: 0.5rem;
  font-weight: 600;
}

.markdown-preview :deep(p) {
  margin-bottom: 0.75rem;
}

.markdown-preview :deep(code) {
  background-color: #f3f4f6;
  padding: 0.125rem 0.25rem;
  border-radius: 0.25rem;
  font-size: 0.875rem;
}

.raw-content {
  margin: 0;
  font-family: 'Monaco', 'Menlo', monospace;
  font-size: 0.8125rem;
  line-height: 1.5;
  color: #374151;
  white-space: pre-wrap;
  word-break: break-word;
}

.version-meta {
  padding: 0.5rem 1rem;
  background-color: #f9fafb;
  border-top: 1px solid #e5e7eb;
  font-size: 0.75rem;
  color: #6b7280;
}

/* VS 指示器 */
.vs-indicator {
  display: flex;
  align-items: center;
  justify-content: center;
}

.vs-indicator span {
  background-color: #f3f4f6;
  color: #6b7280;
  padding: 0.5rem 1rem;
  border-radius: 0.375rem;
  font-weight: 600;
  font-size: 0.875rem;
}

/* 视图切换 */
.view-toggle {
  display: flex;
  justify-content: center;
  margin-bottom: 1rem;
}

.toggle-btn {
  background-color: #f3f4f6;
  border: none;
  border-radius: 0.375rem;
  padding: 0.5rem 1rem;
  font-size: 0.875rem;
  color: #374151;
  cursor: pointer;
  transition: all 0.2s;
}

.toggle-btn:hover {
  background-color: #e5e7eb;
}

/* 合并编辑器 */
.merge-editor {
  margin-top: 1.5rem;
  border: 2px solid #f59e0b;
  border-radius: 0.5rem;
  overflow: hidden;
}

.merge-header {
  background-color: #fffbeb;
  padding: 1rem;
  border-bottom: 1px solid #fde68a;
}

.merge-header h3 {
  margin: 0 0 0.5rem 0;
  font-size: 0.9375rem;
  font-weight: 600;
  color: #92400e;
}

.merge-hint {
  margin: 0;
  font-size: 0.8125rem;
  color: #78350f;
}

.merge-textarea {
  width: 100%;
  padding: 1rem;
  border: none;
  font-family: 'Monaco', 'Menlo', monospace;
  font-size: 0.875rem;
  line-height: 1.6;
  resize: vertical;
  min-height: 300px;
}

.merge-textarea:focus {
  outline: none;
}

/* 底部操作 */
.modal-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 1.5rem;
  border-top: 1px solid #e5e7eb;
  background-color: #f9fafb;
}

.actions-left,
.actions-right {
  display: flex;
  gap: 0.75rem;
}

/* 按钮样式 */
.btn {
  padding: 0.625rem 1.25rem;
  border-radius: 0.375rem;
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
  border: none;
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-primary {
  background-color: #3b82f6;
  color: white;
}

.btn-primary:hover:not(:disabled) {
  background-color: #2563eb;
}

.btn-success {
  background-color: #10b981;
  color: white;
}

.btn-success:hover:not(:disabled) {
  background-color: #059669;
}

.btn-secondary {
  background-color: #e5e7eb;
  color: #374151;
}

.btn-secondary:hover {
  background-color: #d1d5db;
}

.btn-outline {
  background-color: transparent;
  border: 1px solid #d1d5db;
  color: #374151;
}

.btn-outline:hover {
  background-color: #f9fafb;
}

/* 加载动画 */
.spinner {
  display: inline-block;
  width: 1rem;
  height: 1rem;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-top-color: white;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

/* 错误提示 */
.error-banner {
  background-color: #fee2e2;
  color: #991b1b;
  padding: 0.75rem 1rem;
  margin: 1rem 1.5rem 0;
  border-radius: 0.375rem;
  font-size: 0.875rem;
}

/* 过渡动画 */
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.3s;
}

.modal-enter-active .modal-container,
.modal-leave-active .modal-container {
  transition: transform 0.3s, opacity 0.3s;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-from .modal-container,
.modal-leave-to .modal-container {
  transform: scale(0.95);
  opacity: 0;
}

/* 响应式 */
@media (max-width: 768px) {
  .versions-container {
    grid-template-columns: 1fr;
    grid-template-rows: auto auto auto;
  }

  .vs-indicator {
    order: 2;
    margin: 0.5rem 0;
  }

  .version-panel.local {
    order: 1;
  }

  .version-panel.remote {
    order: 3;
  }

  .modal-footer {
    flex-direction: column;
    gap: 0.75rem;
  }

  .actions-left,
  .actions-right {
    width: 100%;
    flex-direction: column;
  }

  .btn {
    width: 100%;
    justify-content: center;
  }
}
</style>
