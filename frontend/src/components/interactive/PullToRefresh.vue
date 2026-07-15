<template>
  <div class="pull-to-refresh" ref="containerRef">
    <div
      class="refresh-indicator"
      :style="{
        height: pullDistance + 'px',
        opacity: pullDistance / threshold,
      }"
    >
      <div
        class="refresh-icon"
        :class="{ 'refresh-icon--spinning': isRefreshing }"
        :style="{ transform: `rotate(${pullDistance * 2}deg)` }"
      >
        {{ isRefreshing ? '⟳' : '↓' }}
      </div>
      <div class="refresh-text">
        {{ refreshText }}
      </div>
    </div>

    <div
      class="refresh-content"
      ref="contentRef"
      :style="{ transform: `translateY(${pullDistance}px)` }"
      @touchstart="handleTouchStart"
      @touchmove="handleTouchMove"
      @touchend="handleTouchEnd"
      @scroll="onContentScroll"
    >
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, inject, onMounted, onUnmounted } from 'vue'
import { SCROLL_CHROME_KEY } from '@/composables/scroll-chrome'

export interface PullToRefreshProps {
  onRefresh: () => Promise<void>
  threshold?: number
  disabled?: boolean
}

const props = withDefaults(defineProps<PullToRefreshProps>(), {
  threshold: 60,
  disabled: false,
})

const scrollChrome = inject(SCROLL_CHROME_KEY, null)

const containerRef = ref<HTMLElement>()
const contentRef = ref<HTMLElement>()
const pullDistance = ref(0)
const startY = ref(0)
const isPulling = ref(false)
const isRefreshing = ref(false)

let lastScrollTop = 0

function onContentScroll() {
  const el = contentRef.value
  if (!el || !scrollChrome?.enabled.value) return
  const top = el.scrollTop
  const delta = top - lastScrollTop
  lastScrollTop = top
  scrollChrome.reportScroll({ scrollTop: top, delta })
}

onMounted(() => {
  contentRef.value?.addEventListener('scroll', onContentScroll, { passive: true })
})

onUnmounted(() => {
  contentRef.value?.removeEventListener('scroll', onContentScroll)
})

const refreshText = computed(() => {
  if (isRefreshing.value) return '刷新中...'
  if (pullDistance.value >= props.threshold) return '松开刷新'
  return '下拉刷新'
})

const handleTouchStart = (e: TouchEvent) => {
  if (props.disabled || isRefreshing.value) return

  // 只在页面顶部时允许下拉
  const scrollTop = contentRef.value?.scrollTop || 0
  if (scrollTop > 0) return

  startY.value = e.touches[0].clientY
  isPulling.value = true
}

const handleTouchMove = (e: TouchEvent) => {
  if (!isPulling.value || props.disabled || isRefreshing.value) return

  const currentY = e.touches[0].clientY
  const deltaY = currentY - startY.value

  if (deltaY > 0) {
    // 阻止默认滚动
    e.preventDefault()

    // 添加阻尼效果
    const damping = 0.5
    pullDistance.value = Math.min(deltaY * damping, props.threshold * 1.5)
  }
}

const handleTouchEnd = async () => {
  if (!isPulling.value || props.disabled) return

  isPulling.value = false

  if (pullDistance.value >= props.threshold) {
    // 触发刷新
    isRefreshing.value = true
    pullDistance.value = props.threshold

    try {
      await props.onRefresh()
    } catch (error) {
      console.error('[PullToRefresh] 刷新失败', error)
    } finally {
      isRefreshing.value = false
      // 动画回弹
      setTimeout(() => {
        pullDistance.value = 0
      }, 100)
    }
  } else {
    // 回弹
    pullDistance.value = 0
  }
}
</script>

<style scoped>
.pull-to-refresh {
  position: relative;
  overflow: hidden;
  height: 100%;
}

.refresh-indicator {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  background: var(--bg-card);
  transform: translateY(-100%);
  z-index: 1;
  transition: opacity var(--duration-fast) var(--ease-out);
}

.refresh-icon {
  font-size: 24px;
  color: var(--brand-primary);
  transition: transform var(--duration-base) var(--ease-out);
}

.refresh-icon--spinning {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.refresh-text {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  font-weight: var(--font-weight-medium);
}

.refresh-content {
  transition: transform var(--duration-base) cubic-bezier(0.4, 0.0, 0.2, 1);
  height: 100%;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
}
</style>
