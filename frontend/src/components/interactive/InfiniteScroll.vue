<template>
  <div
    ref="containerRef"
    class="infinite-scroll"
    @scroll="handleScroll"
  >
    <slot />
    
    <!-- 加载指示器 -->
    <div v-if="loading" class="infinite-scroll-loading">
      <Loading size="small" />
      <span class="loading-text">{{ loadingText }}</span>
    </div>
    
    <!-- 无更多数据提示 -->
    <div v-if="noMore && !loading" class="infinite-scroll-no-more">
      <span class="no-more-text">{{ noMoreText }}</span>
    </div>
    
    <!-- 错误提示 -->
    <div v-if="error && !loading" class="infinite-scroll-error">
      <span class="error-text">{{ errorText }}</span>
      <button class="retry-button" @click="handleRetry">
        重试
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import Loading from '../base/Loading.vue'

export interface InfiniteScrollProps {
  onLoad: () => Promise<void>
  distance?: number
  disabled?: boolean
  immediate?: boolean
  loadingText?: string
  noMoreText?: string
  errorText?: string
}

const props = withDefaults(defineProps<InfiniteScrollProps>(), {
  distance: 100,
  disabled: false,
  immediate: true,
  loadingText: '加载中...',
  noMoreText: '没有更多了',
  errorText: '加载失败',
})

const emit = defineEmits<{
  (e: 'load'): void
  (e: 'error', error: Error): void
}>()

const containerRef = ref<HTMLElement>()
const loading = ref(false)
const noMore = ref(false)
const error = ref(false)

const checkScroll = () => {
  if (!containerRef.value || loading.value || noMore.value || props.disabled) {
    return
  }

  const container = containerRef.value
  const scrollTop = container.scrollTop
  const scrollHeight = container.scrollHeight
  const clientHeight = container.clientHeight

  // 计算距离底部的距离
  const distanceToBottom = scrollHeight - scrollTop - clientHeight

  if (distanceToBottom <= props.distance) {
    load()
  }
}

const load = async () => {
  if (loading.value) return

  loading.value = true
  error.value = false
  emit('load')

  try {
    await props.onLoad()
  } catch (err) {
    console.error('[InfiniteScroll] 加载失败', err)
    error.value = true
    emit('error', err as Error)
  } finally {
    loading.value = false
  }
}

const handleScroll = () => {
  checkScroll()
}

const handleRetry = () => {
  error.value = false
  load()
}

// 设置无更多数据状态
const setNoMore = (value: boolean) => {
  noMore.value = value
}

// 重置状态
const reset = () => {
  loading.value = false
  noMore.value = false
  error.value = false
}

onMounted(() => {
  if (props.immediate) {
    // 首次加载
    load()
  }
})

defineExpose({
  setNoMore,
  reset,
  load,
})
</script>

<style scoped>
.infinite-scroll {
  height: 100%;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
}

.infinite-scroll-loading,
.infinite-scroll-no-more,
.infinite-scroll-error {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6);
  color: var(--color-text-secondary);
  font-size: 14px;
}

.loading-text,
.no-more-text,
.error-text {
  font-weight: var(--font-weight-medium);
}

.no-more-text {
  opacity: 0.6;
}

.infinite-scroll-error {
  flex-direction: column;
  gap: var(--space-3);
}

.error-text {
  color: var(--color-error);
}

.retry-button {
  padding: var(--space-2) var(--space-4);
  background: var(--color-primary);
  color: white;
  border: none;
  border-radius: var(--radius-md);
  font-size: 14px;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.retry-button:hover {
  opacity: 0.9;
}

.retry-button:active {
  transform: scale(0.95);
}
</style>
