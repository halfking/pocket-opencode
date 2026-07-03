<template>
  <div class="swipeable-list-item" ref="containerRef">
    <div
      class="swipe-actions swipe-actions--left"
      :style="{ width: leftActionsWidth + 'px' }"
    >
      <button
        v-for="action in leftActions"
        :key="action.id"
        :class="['swipe-action', `swipe-action--${action.type}`]"
        @click="handleActionClick(action)"
      >
        <span class="swipe-action-icon">{{ action.icon }}</span>
        <span class="swipe-action-label">{{ action.label }}</span>
      </button>
    </div>

    <div
      class="swipe-content"
      ref="contentRef"
      :style="{ transform: `translateX(${translateX}px)` }"
      @touchstart="handleTouchStart"
      @touchmove="handleTouchMove"
      @touchend="handleTouchEnd"
      @mousedown="handleMouseDown"
    >
      <slot />
    </div>

    <div
      class="swipe-actions swipe-actions--right"
      :style="{ width: rightActionsWidth + 'px' }"
    >
      <button
        v-for="action in rightActions"
        :key="action.id"
        :class="['swipe-action', `swipe-action--${action.type}`]"
        @click="handleActionClick(action)"
      >
        <span class="swipe-action-icon">{{ action.icon }}</span>
        <span class="swipe-action-label">{{ action.label }}</span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'

export interface SwipeAction {
  id: string
  icon: string
  label: string
  type: 'primary' | 'success' | 'warning' | 'danger'
  onAction: () => void
}

export interface SwipeableListItemProps {
  leftActions?: SwipeAction[]
  rightActions?: SwipeAction[]
  threshold?: number // 触发操作的滑动距离比例
}

const props = withDefaults(defineProps<SwipeableListItemProps>(), {
  leftActions: () => [],
  rightActions: () => [],
  threshold: 0.3,
})

const containerRef = ref<HTMLElement>()
const contentRef = ref<HTMLElement>()
const translateX = ref(0)
const startX = ref(0)
const currentX = ref(0)
const isDragging = ref(false)
const actionWidth = 80 // 每个操作按钮的宽度

const leftActionsWidth = computed(() => props.leftActions.length * actionWidth)
const rightActionsWidth = computed(() => props.rightActions.length * actionWidth)

const handleTouchStart = (e: TouchEvent) => {
  startX.value = e.touches[0].clientX
  currentX.value = translateX.value
  isDragging.value = true
}

const handleTouchMove = (e: TouchEvent) => {
  if (!isDragging.value) return

  const deltaX = e.touches[0].clientX - startX.value
  let newTranslateX = currentX.value + deltaX

  // 限制滑动范围
  const maxLeft = leftActionsWidth.value
  const maxRight = -rightActionsWidth.value

  if (newTranslateX > maxLeft) {
    newTranslateX = maxLeft + (newTranslateX - maxLeft) * 0.3 // 阻尼效果
  } else if (newTranslateX < maxRight) {
    newTranslateX = maxRight + (newTranslateX - maxRight) * 0.3
  }

  translateX.value = newTranslateX
}

const handleTouchEnd = () => {
  if (!isDragging.value) return
  isDragging.value = false

  const absTranslateX = Math.abs(translateX.value)
  const direction = translateX.value > 0 ? 'left' : 'right'

  if (direction === 'left') {
    // 左滑
    if (translateX.value > leftActionsWidth.value * props.threshold) {
      // 完全展开
      translateX.value = leftActionsWidth.value
    } else {
      // 回弹
      translateX.value = 0
    }
  } else {
    // 右滑
    if (absTranslateX > rightActionsWidth.value * props.threshold) {
      // 完全展开
      translateX.value = -rightActionsWidth.value
    } else {
      // 回弹
      translateX.value = 0
    }
  }
}

const handleMouseDown = (e: MouseEvent) => {
  startX.value = e.clientX
  currentX.value = translateX.value
  isDragging.value = true

  const handleMouseMove = (e: MouseEvent) => {
    if (!isDragging.value) return

    const deltaX = e.clientX - startX.value
    let newTranslateX = currentX.value + deltaX

    const maxLeft = leftActionsWidth.value
    const maxRight = -rightActionsWidth.value

    if (newTranslateX > maxLeft) {
      newTranslateX = maxLeft + (newTranslateX - maxLeft) * 0.3
    } else if (newTranslateX < maxRight) {
      newTranslateX = maxRight + (newTranslateX - maxRight) * 0.3
    }

    translateX.value = newTranslateX
  }

  const handleMouseUp = () => {
    handleTouchEnd()
    document.removeEventListener('mousemove', handleMouseMove)
    document.removeEventListener('mouseup', handleMouseUp)
  }

  document.addEventListener('mousemove', handleMouseMove)
  document.addEventListener('mouseup', handleMouseUp)
}

const handleActionClick = (action: SwipeAction) => {
  action.onAction()
  // 执行操作后重置
  translateX.value = 0
}

// 重置滑动状态
const reset = () => {
  translateX.value = 0
}

defineExpose({ reset })
</script>

<style scoped>
.swipeable-list-item {
  position: relative;
  overflow: hidden;
  user-select: none;
  -webkit-user-select: none;
}

.swipe-content {
  position: relative;
  z-index: 2;
  background: var(--color-bg-surface);
  transition: transform 0.3s cubic-bezier(0.4, 0.0, 0.2, 1);
  touch-action: pan-y; /* 允许垂直滚动 */
}

.swipe-actions {
  position: absolute;
  top: 0;
  bottom: 0;
  display: flex;
  align-items: stretch;
  z-index: 1;
}

.swipe-actions--left {
  left: 0;
  flex-direction: row;
}

.swipe-actions--right {
  right: 0;
  flex-direction: row-reverse;
}

.swipe-action {
  width: 80px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  border: none;
  color: white;
  font-size: 12px;
  cursor: pointer;
  transition: all 0.2s ease;
  user-select: none;
}

.swipe-action:active {
  opacity: 0.8;
}

.swipe-action-icon {
  font-size: 20px;
  line-height: 1;
}

.swipe-action-label {
  font-weight: var(--font-weight-medium);
  white-space: nowrap;
}

/* 操作按钮颜色 */
.swipe-action--primary {
  background: var(--color-primary);
}

.swipe-action--success {
  background: var(--color-success);
}

.swipe-action--warning {
  background: var(--color-warning);
}

.swipe-action--danger {
  background: var(--color-error);
}
</style>
