<template>
  <div class="dual-screen-layout" :class="layoutClasses">
    <!-- 主屏 -->
    <div class="main-screen" :style="mainScreenStyle">
      <slot name="main" />
    </div>

    <!-- 副屏 -->
    <div
      v-if="showSecondary"
      class="secondary-screen"
      :style="secondaryScreenStyle"
    >
      <!-- 副屏头部 -->
      <div class="secondary-header">
        <h3 class="secondary-title">{{ secondaryTitle }}</h3>
        <button
          v-if="closable"
          class="close-btn"
          @click="handleClose"
        >
          ✕
        </button>
      </div>

      <!-- 副屏内容 -->
      <div class="secondary-content">
        <slot name="secondary" />
      </div>
    </div>

    <!-- 切换按钮（当副屏隐藏时） -->
    <button
      v-if="!showSecondary && toggleable"
      class="toggle-btn"
      @click="handleToggle"
    >
      {{ toggleIcon }}
    </button>

    <!-- 分隔线（可拖动调整大小） -->
    <div
      v-if="showSecondary && resizable"
      class="resize-handle"
      @mousedown="handleResizeStart"
      @touchstart="handleResizeStart"
    >
      <div class="resize-indicator"></div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'

export type LayoutMode = 'horizontal' | 'vertical' | 'overlay'

export interface DualScreenLayoutProps {
  mode?: LayoutMode
  secondaryTitle?: string
  closable?: boolean
  toggleable?: boolean
  resizable?: boolean
  defaultRatio?: number // 主屏占比 (0-1)
}

const props = withDefaults(defineProps<DualScreenLayoutProps>(), {
  mode: 'horizontal',
  secondaryTitle: 'AI 助手',
  closable: true,
  toggleable: true,
  resizable: true,
  defaultRatio: 0.6,
})

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'toggle', visible: boolean): void
  (e: 'resize', ratio: number): void
}>()

const showSecondary = ref(true)
const mainRatio = ref(props.defaultRatio)
const isResizing = ref(false)

const layoutClasses = computed(() => {
  return [
    `layout--${props.mode}`,
    {
      'layout--resizing': isResizing.value,
    },
  ]
})

const toggleIcon = computed(() => {
  return props.mode === 'horizontal' ? '◀' : '▲'
})

const mainScreenStyle = computed(() => {
  if (!showSecondary.value) {
    return { width: '100%', height: '100%' }
  }

  if (props.mode === 'horizontal') {
    return {
      width: `${mainRatio.value * 100}%`,
      height: '100%',
    }
  } else if (props.mode === 'vertical') {
    return {
      width: '100%',
      height: `${mainRatio.value * 100}%`,
    }
  } else {
    // overlay 模式
    return { width: '100%', height: '100%' }
  }
})

const secondaryScreenStyle = computed(() => {
  if (props.mode === 'horizontal') {
    return {
      width: `${(1 - mainRatio.value) * 100}%`,
      height: '100%',
    }
  } else if (props.mode === 'vertical') {
    return {
      width: '100%',
      height: `${(1 - mainRatio.value) * 100}%`,
    }
  } else {
    // overlay 模式：悬浮在右侧
    return {
      position: 'absolute' as const,
      right: '0',
      top: '0',
      width: '40%',
      height: '100%',
    }
  }
})

const handleClose = () => {
  showSecondary.value = false
  emit('close')
  emit('toggle', false)
}

const handleToggle = () => {
  showSecondary.value = !showSecondary.value
  emit('toggle', showSecondary.value)
}

const handleResizeStart = (e: MouseEvent | TouchEvent) => {
  if (!props.resizable) return

  isResizing.value = true
  const startPos = 'touches' in e ? e.touches[0] : e
  const startX = startPos.clientX
  const startY = startPos.clientY
  const startRatio = mainRatio.value

  const handleMove = (moveEvent: MouseEvent | TouchEvent) => {
    const pos = 'touches' in moveEvent ? moveEvent.touches[0] : moveEvent
    
    if (props.mode === 'horizontal') {
      const deltaX = pos.clientX - startX
      const containerWidth = (moveEvent.target as HTMLElement).parentElement?.offsetWidth || 1
      const deltaRatio = deltaX / containerWidth
      mainRatio.value = Math.max(0.3, Math.min(0.8, startRatio + deltaRatio))
    } else if (props.mode === 'vertical') {
      const deltaY = pos.clientY - startY
      const containerHeight = (moveEvent.target as HTMLElement).parentElement?.offsetHeight || 1
      const deltaRatio = deltaY / containerHeight
      mainRatio.value = Math.max(0.3, Math.min(0.8, startRatio + deltaRatio))
    }

    emit('resize', mainRatio.value)
  }

  const handleEnd = () => {
    isResizing.value = false
    document.removeEventListener('mousemove', handleMove as any)
    document.removeEventListener('mouseup', handleEnd)
    document.removeEventListener('touchmove', handleMove as any)
    document.removeEventListener('touchend', handleEnd)
  }

  document.addEventListener('mousemove', handleMove as any)
  document.addEventListener('mouseup', handleEnd)
  document.addEventListener('touchmove', handleMove as any)
  document.addEventListener('touchend', handleEnd)
}

defineExpose({
  show: () => {
    showSecondary.value = true
  },
  hide: () => {
    showSecondary.value = false
  },
  toggle: () => {
    showSecondary.value = !showSecondary.value
  },
  setRatio: (ratio: number) => {
    mainRatio.value = Math.max(0.3, Math.min(0.8, ratio))
  },
})
</script>

<style scoped>
.dual-screen-layout {
  position: relative;
  width: 100%;
  height: 100%;
  display: flex;
  background: var(--color-bg-base);
  overflow: hidden;
}

/* 水平布局 */
.layout--horizontal {
  flex-direction: row;
}

/* 垂直布局 */
.layout--vertical {
  flex-direction: column;
}

/* 覆盖布局 */
.layout--overlay {
  position: relative;
}

.main-screen {
  flex-shrink: 0;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  transition: all var(--duration-base) var(--ease-out);
}

.secondary-screen {
  flex-shrink: 0;
  background: var(--color-bg-surface);
  border-left: 1px solid var(--color-border);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  transition: all var(--duration-base) var(--ease-out);
}

.layout--vertical .secondary-screen {
  border-left: none;
  border-top: 1px solid var(--color-border);
}

.layout--overlay .secondary-screen {
  box-shadow: var(--shadow-xl);
  border-radius: var(--radius-lg) 0 0 var(--radius-lg);
}

.secondary-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4);
  border-bottom: 1px solid var(--color-border);
}

.secondary-title {
  font-size: 16px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin: 0;
}

.close-btn {
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: none;
  border: none;
  border-radius: var(--radius-md);
  color: var(--color-text-tertiary);
  font-size: 18px;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.close-btn:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.secondary-content {
  flex: 1;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  padding: var(--space-4);
}

.toggle-btn {
  position: fixed;
  right: var(--space-4);
  bottom: calc(80px + var(--space-4)); /* 避开底部导航 */
  width: 48px;
  height: 48px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--gradient-primary);
  color: white;
  border: none;
  border-radius: 50%;
  font-size: 20px;
  box-shadow: var(--shadow-lg);
  cursor: pointer;
  z-index: 100;
  transition: all var(--duration-base) var(--ease-spring);
}

.toggle-btn:hover {
  transform: scale(1.1);
}

.toggle-btn:active {
  transform: scale(0.95);
}

.resize-handle {
  position: absolute;
  background: transparent;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: ew-resize;
  user-select: none;
}

.layout--horizontal .resize-handle {
  top: 0;
  bottom: 0;
  left: calc(var(--main-ratio, 60%) - 8px);
  width: 16px;
}

.layout--vertical .resize-handle {
  left: 0;
  right: 0;
  top: calc(var(--main-ratio, 60%) - 8px);
  height: 16px;
  cursor: ns-resize;
}

.resize-indicator {
  width: 4px;
  height: 40px;
  background: var(--color-border);
  border-radius: var(--radius-full);
  transition: all var(--duration-fast) var(--ease-out);
}

.layout--vertical .resize-indicator {
  width: 40px;
  height: 4px;
}

.resize-handle:hover .resize-indicator,
.layout--resizing .resize-indicator {
  background: var(--color-primary);
}
</style>
