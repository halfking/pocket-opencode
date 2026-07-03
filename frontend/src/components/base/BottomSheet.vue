<template>
  <Teleport to="body">
    <Transition name="bottom-sheet">
      <div
        v-if="visible"
        class="bottom-sheet-overlay"
        @click="handleOverlayClick"
      >
        <div
          class="bottom-sheet"
          :class="sheetClasses"
          :style="{ transform: `translateY(${dragOffset}px)` }"
          @click.stop
          @touchstart="handleTouchStart"
          @touchmove="handleTouchMove"
          @touchend="handleTouchEnd"
        >
          <!-- 拖动指示器 -->
          <div class="sheet-handle">
            <div class="handle-bar"></div>
          </div>

          <!-- 头部 -->
          <div v-if="title || $slots.header" class="sheet-header">
            <slot name="header">
              <h3 class="sheet-title">{{ title }}</h3>
            </slot>
            <button
              v-if="closable"
              class="sheet-close"
              @click="handleClose"
              aria-label="关闭"
            >
              ✕
            </button>
          </div>

          <!-- 内容 -->
          <div class="sheet-body">
            <slot />
          </div>

          <!-- 底部 -->
          <div v-if="$slots.footer" class="sheet-footer">
            <slot name="footer" />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'

export interface BottomSheetProps {
  modelValue?: boolean
  title?: string
  height?: 'auto' | 'half' | 'full'
  closable?: boolean
  closeOnOverlay?: boolean
  swipeable?: boolean
}

const props = withDefaults(defineProps<BottomSheetProps>(), {
  modelValue: false,
  height: 'auto',
  closable: true,
  closeOnOverlay: true,
  swipeable: true,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'close'): void
}>()

const visible = ref(props.modelValue)
const dragOffset = ref(0)
const startY = ref(0)
const isDragging = ref(false)

watch(() => props.modelValue, (val) => {
  visible.value = val
  if (val) {
    document.body.style.overflow = 'hidden'
  } else {
    document.body.style.overflow = ''
    dragOffset.value = 0
  }
})

const sheetClasses = computed(() => {
  return [`bottom-sheet--${props.height}`]
})

const handleClose = () => {
  visible.value = false
  emit('update:modelValue', false)
  emit('close')
}

const handleOverlayClick = () => {
  if (props.closeOnOverlay) {
    handleClose()
  }
}

const handleTouchStart = (e: TouchEvent) => {
  if (!props.swipeable) return
  
  // 只在头部区域允许拖动
  const target = e.target as HTMLElement
  if (!target.closest('.sheet-handle') && !target.closest('.sheet-header')) {
    return
  }

  startY.value = e.touches[0].clientY
  isDragging.value = true
}

const handleTouchMove = (e: TouchEvent) => {
  if (!isDragging.value || !props.swipeable) return

  const currentY = e.touches[0].clientY
  const deltaY = currentY - startY.value

  if (deltaY > 0) {
    // 只允许向下拖动
    dragOffset.value = deltaY
  }
}

const handleTouchEnd = () => {
  if (!isDragging.value || !props.swipeable) return

  isDragging.value = false

  // 如果拖动超过 150px，关闭 sheet
  if (dragOffset.value > 150) {
    handleClose()
  } else {
    // 回弹
    dragOffset.value = 0
  }
}
</script>

<style scoped>
.bottom-sheet-overlay {
  position: fixed;
  inset: 0;
  background: var(--color-bg-overlay);
  backdrop-filter: blur(4px);
  z-index: 1300;
  display: flex;
  align-items: flex-end;
}

.bottom-sheet {
  width: 100%;
  max-height: 90vh;
  background: var(--color-bg-surface);
  border-radius: var(--radius-xl) var(--radius-xl) 0 0;
  box-shadow: 0 -4px 20px rgba(0, 0, 0, 0.1);
  display: flex;
  flex-direction: column;
  transition: transform var(--duration-base) cubic-bezier(0.4, 0.0, 0.2, 1);
}

.bottom-sheet--auto {
  max-height: 80vh;
}

.bottom-sheet--half {
  height: 50vh;
}

.bottom-sheet--full {
  height: 90vh;
}

.sheet-handle {
  padding: var(--space-3) 0;
  display: flex;
  justify-content: center;
  cursor: grab;
  user-select: none;
}

.sheet-handle:active {
  cursor: grabbing;
}

.handle-bar {
  width: 40px;
  height: 4px;
  background: var(--color-border);
  border-radius: var(--radius-full);
}

.sheet-header {
  position: relative;
  padding: var(--space-4) var(--space-6);
  border-bottom: 1px solid var(--color-border);
}

.sheet-title {
  font-size: 18px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin: 0;
  padding-right: var(--space-8);
}

.sheet-close {
  position: absolute;
  top: var(--space-4);
  right: var(--space-4);
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: none;
  border: none;
  border-radius: var(--radius-md);
  color: var(--color-text-tertiary);
  cursor: pointer;
  font-size: 20px;
  transition: all var(--duration-fast) var(--ease-out);
}

.sheet-close:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sheet-body {
  padding: var(--space-6);
  overflow-y: auto;
  flex: 1;
}

.sheet-footer {
  padding: var(--space-4) var(--space-6);
  border-top: 1px solid var(--color-border);
}

/* 动画 */
.bottom-sheet-enter-active,
.bottom-sheet-leave-active {
  transition: all var(--duration-slow) var(--ease-out);
}

.bottom-sheet-enter-active .bottom-sheet,
.bottom-sheet-leave-active .bottom-sheet {
  transition: transform var(--duration-slow) cubic-bezier(0.4, 0.0, 0.2, 1);
}

.bottom-sheet-enter-from,
.bottom-sheet-leave-to {
  opacity: 0;
}

.bottom-sheet-enter-from .bottom-sheet {
  transform: translateY(100%);
}

.bottom-sheet-leave-to .bottom-sheet {
  transform: translateY(100%);
}

/* 安全区域 */
@supports (padding-bottom: env(safe-area-inset-bottom)) {
  .sheet-body,
  .sheet-footer {
    padding-bottom: calc(var(--space-6) + env(safe-area-inset-bottom));
  }
}
</style>
