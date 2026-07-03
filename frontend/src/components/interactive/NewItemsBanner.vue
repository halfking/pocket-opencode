<template>
  <Transition name="banner-slide">
    <button
      v-if="visible"
      class="new-items-banner"
      :class="{ 'is-loading': loading }"
      :disabled="loading"
      aria-live="polite"
      type="button"
      @click="handleClick"
    >
      <span v-if="!loading" class="icon icon-arrow" aria-hidden="true">↓</span>
      <span v-if="loading" class="icon icon-spinner" aria-hidden="true">⟳</span>
      <span class="text">{{ displayText }}</span>
    </button>
  </Transition>
</template>

<script setup lang="ts">
/**
 * NewItemsBanner.vue — 🦞 新条目提示横幅
 *
 * 用法（视图层）：
 *   <NewItemsBanner
 *     :visible="realtime.bannerVisible.value"
 *     :count="realtime.pendingCount.value"
 *     @refresh="realtime.refresh"
 *   />
 *
 * 设计要点：
 *   - sticky 定位在 topbar 下方，进入/退出用 Transition + translateY 动画
 *   - pill 形外观，44px+ 触摸目标，移动友好
 *   - loading 时变旋转图标 + 禁用，防重复点击
 *   - aria-live="polite" 让屏幕阅读器在内容变化时播报
 */
import { computed } from 'vue'

export interface NewItemsBannerProps {
  visible: boolean
  count: number
  loading?: boolean
  /** 可选自定义文案；缺省时按 count 自动拼 "N 项更新，点按刷新" */
  message?: string
}

const props = withDefaults(defineProps<NewItemsBannerProps>(), {
  loading: false,
  message: undefined,
})

const emit = defineEmits<{
  (e: 'refresh'): void
}>()

const displayText = computed(() => {
  if (props.message) return props.message
  if (props.count > 0) return `${props.count} 项更新，点按刷新`
  return '有更新，点按刷新'
})

function handleClick() {
  if (props.loading) return
  emit('refresh')
}
</script>

<style scoped>
.new-items-banner {
  /* sticky 在 topbar 下方；topbar 高度用 CSS 变量，默认 56px */
  position: sticky;
  top: var(--topbar-height, 56px);
  z-index: 5;

  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);

  /* 让 pill 居中 */
  margin: var(--space-3) auto;

  /* 44px+ 触摸目标 */
  min-height: 40px;
  padding: var(--space-2) var(--space-4);

  background: var(--color-primary);
  color: #fff;
  border: none;
  border-radius: var(--radius-full);

  font-size: 13px;
  font-weight: var(--font-weight-medium);
  line-height: 1.2;
  white-space: nowrap;

  cursor: pointer;
  box-shadow: var(--shadow-md);

  transition:
    transform var(--duration-base) var(--ease-out),
    opacity var(--duration-base) var(--ease-out),
    box-shadow var(--duration-fast) var(--ease-out);
}

.new-items-banner:hover {
  transform: translateY(-1px);
  box-shadow: var(--shadow-lg);
}

.new-items-banner:active {
  transform: translateY(0);
}

.new-items-banner.is-loading {
  cursor: wait;
  opacity: 0.85;
}

.new-items-banner:disabled {
  pointer-events: none;
}

.icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
  line-height: 1;
}

.icon-spinner {
  animation: banner-spin 1s linear infinite;
}

@keyframes banner-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

/* Transition：滑入/滑出 */
.banner-slide-enter-active,
.banner-slide-leave-active {
  transition:
    transform var(--duration-base) var(--ease-spring, cubic-bezier(0.34, 1.56, 0.64, 1)),
    opacity var(--duration-base) var(--ease-out);
}

.banner-slide-enter-from,
.banner-slide-leave-to {
  opacity: 0;
  transform: translateY(-12px);
}
</style>