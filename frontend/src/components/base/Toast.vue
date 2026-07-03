<template>
  <Teleport to="body">
    <Transition name="toast">
      <div
        v-if="visible"
        :class="toastClasses"
        role="alert"
        @click="handleClick"
      >
        <div class="toast-icon" v-if="icon">
          {{ icon }}
        </div>
        <div class="toast-content">
          <div class="toast-message">{{ message }}</div>
          <div v-if="description" class="toast-description">{{ description }}</div>
        </div>
        <button
          v-if="closable"
          class="toast-close"
          @click.stop="close"
          aria-label="关闭"
        >
          ✕
        </button>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'

export interface ToastProps {
  message: string
  description?: string
  type?: 'success' | 'error' | 'warning' | 'info'
  duration?: number
  closable?: boolean
  onClose?: () => void
}

const props = withDefaults(defineProps<ToastProps>(), {
  type: 'info',
  duration: 3000,
  closable: true,
})

const visible = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null

const toastClasses = computed(() => {
  return ['toast', `toast--${props.type}`]
})

const icon = computed(() => {
  const icons = {
    success: '✓',
    error: '✕',
    warning: '⚠',
    info: 'ℹ',
  }
  return icons[props.type]
})

const close = () => {
  visible.value = false
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
  props.onClose?.()
}

const handleClick = () => {
  if (props.closable) {
    close()
  }
}

const startTimer = () => {
  if (props.duration > 0) {
    timer = setTimeout(() => {
      close()
    }, props.duration)
  }
}

onMounted(() => {
  visible.value = true
  startTimer()
})

watch(() => props.duration, () => {
  if (timer) {
    clearTimeout(timer)
  }
  startTimer()
})

defineExpose({
  close,
})
</script>

<style scoped>
.toast {
  position: fixed;
  bottom: calc(80px + env(safe-area-inset-bottom)); /* 避开底部导航 */
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  min-width: 280px;
  max-width: calc(100vw - 32px);
  padding: var(--space-4);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-lg);
  z-index: 9999;
  pointer-events: auto;
}

/* Success */
.toast--success {
  background: var(--color-success);
  color: white;
}

/* Error */
.toast--error {
  background: var(--color-error);
  color: white;
}

/* Warning */
.toast--warning {
  background: var(--color-warning);
  color: white;
}

/* Info */
.toast--info {
  background: var(--color-primary);
  color: white;
}

.toast-icon {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  font-weight: bold;
}

.toast-content {
  flex: 1;
  min-width: 0;
}

.toast-message {
  font-size: 14px;
  font-weight: var(--font-weight-medium);
  line-height: 1.4;
}

.toast-description {
  font-size: 12px;
  opacity: 0.9;
  margin-top: var(--space-1);
  line-height: 1.4;
}

.toast-close {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: none;
  border: none;
  color: currentColor;
  cursor: pointer;
  opacity: 0.8;
  padding: 0;
  font-size: 16px;
  transition: opacity var(--duration-fast) var(--ease-out);
}

.toast-close:hover {
  opacity: 1;
}

/* 动画 */
.toast-enter-active,
.toast-leave-active {
  transition: all var(--duration-base) var(--ease-out);
}

.toast-enter-from {
  opacity: 0;
  transform: translateX(-50%) translateY(20px);
}

.toast-leave-to {
  opacity: 0;
  transform: translateX(-50%) scale(0.9);
}
</style>
