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
  /*
   * 底边要避开两样东西（2026-10-03 模拟器 API 35 实测，/ai-chat 错误 toast
   * 压住整条工具行 + 发送按钮，覆盖麦克风/相机/附件/角色/优化）：
   *
   *   max(--bottom-chrome-height, --composer-inset)
   *       底部那一整条带。没有输入区的页面只有 tabbar（--composer-inset
   *       回落 0）；有输入区的页面 --composer-inset 量的是
   *       「#app 底边 → 输入框顶边」，**已经把 tabbar 算在里面了**，
   *       所以这里必须取 max 而不是相加——相加会把 tabbar 重复算一次，
   *       toast 平白上浮 88px。
   *
   *   --kb-inset
   *       软键盘净高。键盘在场时整个 #app 上移，输入区带高与键盘无关，
   *       这一份只能在这里加（与「键盘那一份只算一次」是同一条理由）。
   */
  bottom: calc(
    max(var(--bottom-chrome-height), var(--composer-inset, 0px)) + var(--kb-inset, 0px) +
      var(--space-4)
  );
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
  z-index: var(--z-toast);
  pointer-events: auto;
}

/* Success */
.toast--success {
  background: var(--color-success);
  color: var(--text-inverse);
}

/* Error */
.toast--error {
  background: var(--color-error);
  color: var(--text-inverse);
}

/* Warning */
.toast--warning {
  background: var(--color-warning);
  color: var(--text-inverse);
}

/* Info */
.toast--info {
  background: var(--color-primary);
  color: var(--text-inverse);
}

.toast-icon {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--text-lg);
  font-weight: bold;
}

.toast-content {
  flex: 1;
  min-width: 0;
}

.toast-message {
  font-size: var(--text-base);
  font-weight: var(--font-weight-medium);
  line-height: 1.4;
}

.toast-description {
  font-size: var(--text-sm);
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
  font-size: var(--text-lg);
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
