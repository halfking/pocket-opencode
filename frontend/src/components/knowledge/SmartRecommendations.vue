<template>
  <div class="smart-recommendations">
    <!-- 头部 -->
    <div class="recommendations-header">
      <h3>💡 相关推荐</h3>
      <span v-if="!loading" class="count">{{ recommendations.length }} 条</span>
    </div>

    <!-- 加载状态 -->
    <div v-if="loading" class="loading-state">
      <div class="spinner"></div>
      <p>正在分析相关内容...</p>
    </div>

    <!-- 错误状态 -->
    <div v-else-if="error" class="error-state">
      <span class="error-icon">⚠️</span>
      <p>{{ error }}</p>
      <button @click="() => loadRecommendations(false)" class="retry-btn">重试</button>
    </div>

    <!-- 空状态 -->
    <div v-else-if="recommendations.length === 0" class="empty-state">
      <span class="empty-icon">🔍</span>
      <p>暂无相关推荐</p>
      <span class="empty-hint">继续编辑笔记，AI 会自动发现相关内容</span>
    </div>

    <!-- 推荐列表 -->
    <div v-else class="recommendations-list">
      <div
        v-for="item in sortedRecommendations"
        :key="item.id"
        class="recommendation-item"
        :class="{ 'high-confidence': item.confidence >= 0.8 }"
        @click="handleClick(item)"
      >
        <!-- 置信度指示器 -->
        <div class="confidence-bar">
          <div 
            class="confidence-fill"
            :style="{ width: `${item.confidence * 100}%` }"
            :class="getConfidenceClass(item.confidence)"
          ></div>
        </div>

        <!-- 内容 -->
        <div class="item-content">
          <div class="item-header">
            <h4 class="item-title">{{ item.note.title || '无标题' }}</h4>
            <span class="confidence-badge" :class="getConfidenceClass(item.confidence)">
              {{ formatConfidence(item.confidence) }}
            </span>
          </div>

          <!-- 推荐原因 -->
          <div v-if="item.reason" class="item-reason">
            <span class="reason-icon">💡</span>
            <span class="reason-text">{{ item.reason }}</span>
          </div>

          <!-- 内容预览 -->
          <div v-if="item.note.content" class="item-preview">
            {{ truncateContent(item.note.content) }}
          </div>

          <!-- 元信息 -->
          <div class="item-meta">
            <span v-if="item.note.tags && item.note.tags.length > 0" class="meta-tags">
              <span class="meta-icon">🏷️</span>
              {{ item.note.tags.slice(0, 3).join(', ') }}
            </span>
            <span class="meta-time">
              <span class="meta-icon">🕐</span>
              {{ formatTime(item.note.updatedAt) }}
            </span>
          </div>
        </div>
      </div>
    </div>

    <!-- 底部操作 -->
    <div v-if="recommendations.length > 0" class="recommendations-footer">
      <button @click="loadMore" class="btn-load-more" v-if="hasMore && !loading">
        加载更多
      </button>
      <button @click="refresh" class="btn-refresh">
        🔄 刷新推荐
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { useApiError } from '@/composables/useApiError'

interface Note {
  id: string
  title: string
  content?: string
  tags?: string[]
  updatedAt: number
}

interface Recommendation {
  id: string
  note: Note
  confidence: number  // 0-1
  reason: string      // 推荐原因
  score?: number      // 内部评分
}

interface Props {
  noteId: string
  limit?: number
}

const props = withDefaults(defineProps<Props>(), {
  limit: 5
})

const emit = defineEmits<{
  itemClick: [recommendation: Recommendation]
}>()

const router = useRouter()

// 状态
const apiError = useApiError()
const loading = ref(false)
const error = ref<string | null>(null)
const recommendations = ref<Recommendation[]>([])
const hasMore = ref(false)
const currentOffset = ref(0)

// 按置信度排序
const sortedRecommendations = computed(() => {
  return [...recommendations.value].sort((a, b) => b.confidence - a.confidence)
})

// 加载推荐
const loadRecommendations = async (append = false) => {
  if (!props.noteId) return
  
  try {
    loading.value = true
    error.value = null

    const offset = append ? currentOffset.value : 0
    const response = await api.get<{ recommendations: Recommendation[]; hasMore: boolean }>(
      `/api/notes/${props.noteId}/related`,
      {
        params: {
          limit: props.limit,
          offset
        }
      }
    )

    const newRecommendations = response.data.recommendations || []
    
    if (append) {
      recommendations.value = [...recommendations.value, ...newRecommendations]
    } else {
      recommendations.value = newRecommendations
    }

    hasMore.value = response.data.hasMore || false
    currentOffset.value = offset + newRecommendations.length

  } catch (err: any) {
    // 原始 err.message 不上屏：走共享归一文案，原始信息 console 留档。
    console.error('Failed to load recommendations:', err)
    error.value = apiError(err, '加载推荐失败')
  } finally {
    loading.value = false
  }
}

// 加载更多
const loadMore = () => {
  loadRecommendations(true)
}

// 刷新
const refresh = () => {
  currentOffset.value = 0
  loadRecommendations(false)
}

// 处理点击
const handleClick = (item: Recommendation) => {
  emit('itemClick', item)
  router.push(`/knowledge/notes/${item.note.id}`)
}

// 格式化置信度
const formatConfidence = (confidence: number): string => {
  const percent = Math.round(confidence * 100)
  if (percent >= 90) return '高度相关'
  if (percent >= 70) return '相关'
  if (percent >= 50) return '可能相关'
  return '弱相关'
}

// 获取置信度样式类
const getConfidenceClass = (confidence: number): string => {
  if (confidence >= 0.8) return 'confidence-high'
  if (confidence >= 0.6) return 'confidence-medium'
  return 'confidence-low'
}

// 截断内容
const truncateContent = (content: string, maxLength = 100): string => {
  if (!content) return ''
  const stripped = content.replace(/[#*_`\[\]]/g, '').trim()
  if (stripped.length <= maxLength) return stripped
  return stripped.substring(0, maxLength) + '...'
}

// 格式化时间
const formatTime = (timestamp: number): string => {
  const date = new Date(timestamp)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffDays = Math.floor(diffMs / 86400000)
  
  if (diffDays === 0) return '今天'
  if (diffDays === 1) return '昨天'
  if (diffDays < 7) return `${diffDays} 天前`
  if (diffDays < 30) return `${Math.floor(diffDays / 7)} 周前`
  if (diffDays < 365) return `${Math.floor(diffDays / 30)} 个月前`
  return date.toLocaleDateString('zh-CN')
}

// 监听 noteId 变化
watch(() => props.noteId, (newId) => {
  if (newId) {
    currentOffset.value = 0
    loadRecommendations()
  }
}, { immediate: true })

onMounted(() => {
  loadRecommendations()
})
</script>

<style scoped>
.smart-recommendations {
  background: white;
  border-radius: 0.5rem;
  border: 1px solid #e5e7eb;
  overflow: hidden;
}

/* 头部 */
.recommendations-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem;
  border-bottom: 1px solid #e5e7eb;
  background-color: #f9fafb;
}

.recommendations-header h3 {
  margin: 0;
  font-size: 1rem;
  font-weight: 600;
  color: #111827;
}

.count {
  font-size: 0.875rem;
  color: #6b7280;
}

/* 加载/错误/空状态 */
.loading-state,
.error-state,
.empty-state {
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

.error-icon,
.empty-icon {
  font-size: 2.5rem;
  margin-bottom: 0.75rem;
}

.empty-hint {
  font-size: 0.75rem;
  color: #9ca3af;
  margin-top: 0.5rem;
}

.retry-btn {
  margin-top: 1rem;
  padding: 0.5rem 1rem;
  background-color: #3b82f6;
  color: white;
  border: none;
  border-radius: 0.375rem;
  font-size: 0.875rem;
  cursor: pointer;
}

/* 推荐列表 */
.recommendations-list {
  padding: 0.5rem;
}

.recommendation-item {
  padding: 1rem;
  border: 1px solid #e5e7eb;
  border-radius: 0.5rem;
  margin-bottom: 0.75rem;
  cursor: pointer;
  transition: all 0.2s;
  position: relative;
  overflow: hidden;
}

.recommendation-item:hover {
  border-color: #3b82f6;
  background-color: #eff6ff;
  transform: translateX(2px);
}

.recommendation-item.high-confidence {
  border-left: 3px solid #10b981;
}

/* 置信度条 */
.confidence-bar {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 3px;
  background-color: #f3f4f6;
}

.confidence-fill {
  height: 100%;
  transition: width 0.3s;
}

.confidence-fill.confidence-high {
  background-color: #10b981;
}

.confidence-fill.confidence-medium {
  background-color: #3b82f6;
}

.confidence-fill.confidence-low {
  background-color: #f59e0b;
}

/* 内容 */
.item-content {
  padding-top: 0.5rem;
}

.item-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 0.5rem;
}

.item-title {
  margin: 0;
  font-size: 0.9375rem;
  font-weight: 600;
  color: #111827;
  flex: 1;
}

.confidence-badge {
  font-size: 0.75rem;
  padding: 0.125rem 0.5rem;
  border-radius: 0.25rem;
  font-weight: 500;
  white-space: nowrap;
  margin-left: 0.5rem;
}

.confidence-badge.confidence-high {
  background-color: #d1fae5;
  color: #065f46;
}

.confidence-badge.confidence-medium {
  background-color: #dbeafe;
  color: #1e40af;
}

.confidence-badge.confidence-low {
  background-color: #fef3c7;
  color: #92400e;
}

.item-reason {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  margin-bottom: 0.5rem;
  font-size: 0.8125rem;
  color: #6b7280;
  font-style: italic;
}

.reason-icon {
  font-size: 1rem;
}

.item-preview {
  font-size: 0.8125rem;
  color: #6b7280;
  line-height: 1.5;
  margin-bottom: 0.5rem;
}

.item-meta {
  display: flex;
  gap: 1rem;
  font-size: 0.75rem;
  color: #9ca3af;
}

.meta-tags,
.meta-time {
  display: flex;
  align-items: center;
  gap: 0.25rem;
}

.meta-icon {
  font-size: 0.875rem;
}

/* 底部 */
.recommendations-footer {
  padding: 1rem;
  border-top: 1px solid #e5e7eb;
  display: flex;
  gap: 0.75rem;
  justify-content: center;
  background-color: #f9fafb;
}

.btn-load-more,
.btn-refresh {
  padding: 0.5rem 1rem;
  border-radius: 0.375rem;
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
  border: none;
}

.btn-load-more {
  background-color: #3b82f6;
  color: white;
}

.btn-load-more:hover {
  background-color: #2563eb;
}

.btn-refresh {
  background-color: #e5e7eb;
  color: #374151;
}

.btn-refresh:hover {
  background-color: #d1d5db;
}
</style>
