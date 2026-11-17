<template>
  <div class="note-links-panel">
    <!-- 头部 -->
    <div class="panel-header">
      <h3>🔗 关联笔记</h3>
      <span class="total-count">({{ totalLinksCount }})</span>
    </div>

    <!-- Tab 切换 -->
    <div class="tabs">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        :class="['tab-btn', { active: activeTab === tab.key }]"
        @click="activeTab = tab.key"
      >
        {{ tab.label }}
        <span class="count">({{ getLinkCount(tab.key) }})</span>
      </button>
    </div>

    <!-- 内容区域 -->
    <div class="panel-content">
      <!-- 加载状态 -->
      <div v-if="loading" class="loading-state">
        <div class="spinner"></div>
        <p>加载中...</p>
      </div>

      <!-- 空状态 -->
      <div v-else-if="currentLinks.length === 0" class="empty-state">
        <div class="empty-icon">{{ getEmptyIcon(activeTab) }}</div>
        <p class="empty-text">{{ getEmptyText(activeTab) }}</p>
      </div>

      <!-- 链接列表 -->
      <div v-else class="links-list">
        <div
          v-for="link in currentLinks"
          :key="link.id"
          class="link-item"
          @click="handleLinkClick(link)"
        >
          <!-- 链接头部 -->
          <div class="link-header">
            <h4 class="link-title">
              <span class="link-icon">{{ getLinkIcon(link) }}</span>
              {{ link.note.title || '无标题' }}
            </h4>
            <div class="link-actions">
              <!-- AI 推荐的置信度 -->
              <span v-if="link.createdBy === 'ai' && link.confidence" class="confidence">
                {{ formatConfidence(link.confidence) }}
              </span>
              <!-- 删除按钮（仅手动链接） -->
              <button
                v-if="link.createdBy === 'user' && activeTab === 'manual'"
                @click.stop="handleDelete(link)"
                class="delete-btn"
                title="删除链接"
              >
                ✕
              </button>
            </div>
          </div>

          <!-- 链接内容预览 -->
          <div v-if="link.note.content" class="link-preview">
            {{ truncateContent(link.note.content) }}
          </div>

          <!-- 链接元信息 -->
          <div class="link-meta">
            <span class="meta-item">
              <span class="meta-icon">📝</span>
              {{ link.note.noteCount || 0 }} 笔记
            </span>
            <span v-if="link.reason" class="meta-item reason">
              <span class="meta-icon">💡</span>
              {{ link.reason }}
            </span>
            <span class="meta-item">
              <span class="meta-icon">🕐</span>
              {{ formatTime(link.createdAt) }}
            </span>
          </div>

          <!-- 链接类型标签 -->
          <div class="link-type">
            <span :class="['type-badge', link.linkType]">
              {{ getLinkTypeLabel(link.linkType) }}
            </span>
          </div>
        </div>
      </div>
    </div>

    <!-- 添加链接按钮（手动链接 tab） -->
    <div v-if="activeTab === 'manual'" class="panel-footer">
      <button @click="handleAddLink" class="btn-add">
        + 添加关联笔记
      </button>
    </div>

    <!-- 确认删除对话框 -->
    <Teleport to="body">
      <Transition name="modal">
        <div v-if="showDeleteConfirm" class="modal-overlay" @click="cancelDelete">
          <div class="confirm-dialog" @click.stop>
            <h3>确认删除</h3>
            <p>确定要删除与「{{ deletingLink?.note.title }}」的链接吗？</p>
            <div class="dialog-actions">
              <button @click="cancelDelete" class="btn-secondary">取消</button>
              <button @click="confirmDelete" class="btn-danger">删除</button>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'

interface Note {
  id: string
  title: string
  content?: string
  noteCount?: number
}

interface NoteLink {
  id: string
  note: Note
  linkType: 'related' | 'reference' | 'parent' | 'child' | 'bidirectional'
  confidence?: number  // 0-1, AI 推荐的置信度
  createdBy: 'user' | 'ai'
  reason?: string     // AI 推荐的原因
  createdAt: number
}

interface Props {
  noteId: string
}

const props = defineProps<Props>()
const emit = defineEmits<{
  linkClicked: [link: NoteLink]
  linkAdded: []
  linkDeleted: [linkId: string]
}>()

const router = useRouter()

// 状态
const activeTab = ref<'manual' | 'smart' | 'backlinks'>('manual')
const loading = ref(false)
const manualLinks = ref<NoteLink[]>([])
const smartLinks = ref<NoteLink[]>([])
const backlinks = ref<NoteLink[]>([])
const showDeleteConfirm = ref(false)
const deletingLink = ref<NoteLink | null>(null)

// Tab 配置
const tabs = [
  { key: 'manual' as const, label: '📌 手动链接' },
  { key: 'smart' as const, label: '🤖 AI 推荐' },
  { key: 'backlinks' as const, label: '↩️ 反向链接' }
]

// 计算属性
const currentLinks = computed(() => {
  switch (activeTab.value) {
    case 'manual':
      return manualLinks.value
    case 'smart':
      return smartLinks.value
    case 'backlinks':
      return backlinks.value
    default:
      return []
  }
})

const totalLinksCount = computed(() => {
  return manualLinks.value.length + smartLinks.value.length + backlinks.value.length
})

// 获取链接数量
const getLinkCount = (tabKey: string) => {
  switch (tabKey) {
    case 'manual':
      return manualLinks.value.length
    case 'smart':
      return smartLinks.value.length
    case 'backlinks':
      return backlinks.value.length
    default:
      return 0
  }
}

// 获取链接图标
const getLinkIcon = (link: NoteLink) => {
  if (link.linkType === 'parent') return '⬆️'
  if (link.linkType === 'child') return '⬇️'
  if (link.linkType === 'bidirectional') return '↔️'
  if (link.linkType === 'reference') return '📎'
  return '🔗'
}

// 获取链接类型标签
const getLinkTypeLabel = (linkType: string) => {
  const labels: Record<string, string> = {
    related: '相关',
    reference: '引用',
    parent: '父笔记',
    child: '子笔记',
    bidirectional: '双向'
  }
  return labels[linkType] || linkType
}

// 格式化置信度
const formatConfidence = (confidence: number) => {
  return `${Math.round(confidence * 100)}% 相关`
}

// 截断内容
const truncateContent = (content: string, maxLength = 120) => {
  if (!content) return ''
  const stripped = content.replace(/[#*_`\[\]]/g, '').trim()
  if (stripped.length <= maxLength) return stripped
  return stripped.substring(0, maxLength) + '...'
}

// 格式化时间
const formatTime = (timestamp: number) => {
  const date = new Date(timestamp)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMins = Math.floor(diffMs / 60000)
  
  if (diffMins < 60) return `${diffMins} 分钟前`
  const diffHours = Math.floor(diffMins / 60)
  if (diffHours < 24) return `${diffHours} 小时前`
  const diffDays = Math.floor(diffHours / 24)
  if (diffDays < 7) return `${diffDays} 天前`
  
  return date.toLocaleDateString('zh-CN')
}

// 获取空状态图标
const getEmptyIcon = (tab: string) => {
  const icons: Record<string, string> = {
    manual: '📝',
    smart: '🤖',
    backlinks: '🔍'
  }
  return icons[tab] || '📭'
}

// 获取空状态文本
const getEmptyText = (tab: string) => {
  const texts: Record<string, string> = {
    manual: '还没有手动添加的关联笔记',
    smart: '暂无 AI 推荐的相关笔记',
    backlinks: '没有其他笔记链接到这里'
  }
  return texts[tab] || '暂无数据'
}

// 加载链接
const loadLinks = async () => {
  if (!props.noteId) return
  
  try {
    loading.value = true
    
    // 调用 API 获取笔记的所有链接
    const response = await api.get<{ links: NoteLink[] }>(`/api/notes/${props.noteId}/links`)
    
    const links = response.data.links || []
    
    // 分类到不同的数组
    manualLinks.value = links.filter((l: NoteLink) => l.createdBy === 'user')
    smartLinks.value = links.filter((l: NoteLink) => l.createdBy === 'ai').sort((a: NoteLink, b: NoteLink) => {
      return (b.confidence || 0) - (a.confidence || 0)
    })
    
    // 获取反向链接
    const backlinksResponse = await api.get<{ links: NoteLink[] }>(`/api/notes/${props.noteId}/backlinks`)
    backlinks.value = backlinksResponse.data.links || []
    
  } catch (err) {
    console.error('Failed to load note links:', err)
  } finally {
    loading.value = false
  }
}

// 处理链接点击
const handleLinkClick = (link: NoteLink) => {
  emit('linkClicked', link)
  // 导航到目标笔记
  router.push(`/knowledge/notes/${link.note.id}`)
}

// 添加链接
const handleAddLink = () => {
  emit('linkAdded')
  // TODO: 打开笔记选择器对话框
}

// 删除链接
const handleDelete = (link: NoteLink) => {
  deletingLink.value = link
  showDeleteConfirm.value = true
}

const confirmDelete = async () => {
  if (!deletingLink.value) return
  
  try {
    await api.delete(`/api/note-links/${deletingLink.value.id}`)
    
    // 从列表中移除
    manualLinks.value = manualLinks.value.filter(l => l.id !== deletingLink.value!.id)
    
    emit('linkDeleted', deletingLink.value.id)
  } catch (err) {
    console.error('Failed to delete link:', err)
  } finally {
    showDeleteConfirm.value = false
    deletingLink.value = null
  }
}

const cancelDelete = () => {
  showDeleteConfirm.value = false
  deletingLink.value = null
}

// 监听 noteId 变化
watch(() => props.noteId, (newId) => {
  if (newId) {
    loadLinks()
  }
}, { immediate: true })

onMounted(() => {
  loadLinks()
})
</script>

<style scoped>
.note-links-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: white;
  border-radius: 0.5rem;
  border: 1px solid #e5e7eb;
  overflow: hidden;
}

/* 头部 */
.panel-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 1rem;
  border-bottom: 1px solid #e5e7eb;
}

.panel-header h3 {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
  color: #111827;
}

.total-count {
  font-size: 0.875rem;
  color: #6b7280;
}

/* Tabs */
.tabs {
  display: flex;
  border-bottom: 1px solid #e5e7eb;
  background-color: #f9fafb;
}

.tab-btn {
  flex: 1;
  padding: 0.75rem 1rem;
  background: none;
  border: none;
  border-bottom: 2px solid transparent;
  font-size: 0.875rem;
  color: #6b7280;
  cursor: pointer;
  transition: all 0.2s;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.25rem;
}

.tab-btn:hover {
  background-color: #f3f4f6;
  color: #111827;
}

.tab-btn.active {
  color: #3b82f6;
  border-bottom-color: #3b82f6;
  background-color: white;
}

.tab-btn .count {
  font-size: 0.75rem;
}

/* 内容区域 */
.panel-content {
  flex: 1;
  overflow-y: auto;
  padding: 0.5rem;
}

/* 加载状态 */
.loading-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 3rem 1rem;
  color: #6b7280;
}

.spinner {
  width: 2rem;
  height: 2rem;
  border: 3px solid #e5e7eb;
  border-top-color: #3b82f6;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  margin-bottom: 1rem;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* 空状态 */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 3rem 1rem;
  color: #9ca3af;
}

.empty-icon {
  font-size: 3rem;
  margin-bottom: 1rem;
}

.empty-text {
  margin: 0;
  font-size: 0.875rem;
}

/* 链接列表 */
.links-list {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.link-item {
  padding: 0.75rem;
  border: 1px solid #e5e7eb;
  border-radius: 0.5rem;
  cursor: pointer;
  transition: all 0.2s;
}

.link-item:hover {
  border-color: #3b82f6;
  background-color: #eff6ff;
  transform: translateX(2px);
}

.link-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 0.5rem;
}

.link-title {
  margin: 0;
  font-size: 0.9375rem;
  font-weight: 500;
  color: #111827;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.link-icon {
  font-size: 1rem;
}

.link-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.confidence {
  font-size: 0.75rem;
  color: #10b981;
  background-color: #d1fae5;
  padding: 0.125rem 0.5rem;
  border-radius: 0.25rem;
  font-weight: 500;
}

.delete-btn {
  background: none;
  border: none;
  color: #9ca3af;
  cursor: pointer;
  font-size: 1rem;
  padding: 0;
  width: 1.5rem;
  height: 1.5rem;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 0.25rem;
  transition: all 0.2s;
}

.delete-btn:hover {
  background-color: #fee2e2;
  color: #dc2626;
}

.link-preview {
  font-size: 0.8125rem;
  color: #6b7280;
  line-height: 1.5;
  margin-bottom: 0.5rem;
}

.link-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  font-size: 0.75rem;
  color: #9ca3af;
  margin-bottom: 0.5rem;
}

.meta-item {
  display: flex;
  align-items: center;
  gap: 0.25rem;
}

.meta-item.reason {
  color: #6b7280;
  font-style: italic;
}

.meta-icon {
  font-size: 0.875rem;
}

.link-type {
  display: flex;
  justify-content: flex-end;
}

.type-badge {
  font-size: 0.6875rem;
  padding: 0.125rem 0.5rem;
  border-radius: 0.25rem;
  font-weight: 500;
}

.type-badge.related {
  background-color: #dbeafe;
  color: #1e40af;
}

.type-badge.reference {
  background-color: #e0e7ff;
  color: #4338ca;
}

.type-badge.parent {
  background-color: #fef3c7;
  color: #92400e;
}

.type-badge.child {
  background-color: #fed7aa;
  color: #9a3412;
}

.type-badge.bidirectional {
  background-color: #d1fae5;
  color: #065f46;
}

/* 底部 */
.panel-footer {
  padding: 1rem;
  border-top: 1px solid #e5e7eb;
  background-color: #f9fafb;
}

.btn-add {
  width: 100%;
  padding: 0.625rem;
  background-color: #3b82f6;
  color: white;
  border: none;
  border-radius: 0.375rem;
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: background-color 0.2s;
}

.btn-add:hover {
  background-color: #2563eb;
}

/* 确认对话框 */
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
  z-index: var(--z-menu);
}

.confirm-dialog {
  background: white;
  border-radius: 0.5rem;
  padding: 1.5rem;
  max-width: 400px;
  box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.1);
}

.confirm-dialog h3 {
  margin: 0 0 1rem 0;
  font-size: 1.125rem;
  font-weight: 600;
  color: #111827;
}

.confirm-dialog p {
  margin: 0 0 1.5rem 0;
  color: #6b7280;
  font-size: 0.9375rem;
}

.dialog-actions {
  display: flex;
  gap: 0.75rem;
  justify-content: flex-end;
}

.btn-secondary,
.btn-danger {
  padding: 0.5rem 1rem;
  border-radius: 0.375rem;
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  border: none;
  transition: all 0.2s;
}

.btn-secondary {
  background-color: #e5e7eb;
  color: #374151;
}

.btn-secondary:hover {
  background-color: #d1d5db;
}

.btn-danger {
  background-color: #dc2626;
  color: white;
}

.btn-danger:hover {
  background-color: #b91c1c;
}

/* 模态框动画 */
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
</style>
