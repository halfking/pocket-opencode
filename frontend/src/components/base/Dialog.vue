<template>
  <Teleport to="body">
    <Transition name="dialog">
      <div
        v-if="visible"
        class="dialog-overlay"
        @click="handleOverlayClick"
      >
        <div
          class="dialog"
          :class="dialogClasses"
          @click.stop
          role="dialog"
          aria-modal="true"
        >
          <!-- 头部 -->
          <div v-if="title || $slots.header" class="dialog-header">
            <slot name="header">
              <h3 class="dialog-title">{{ title }}</h3>
            </slot>
            <button
              v-if="closable"
              class="dialog-close"
              @click="handleClose"
              aria-label="关闭"
            >
              ✕
            </button>
          </div>

          <!-- 内容 -->
          <div class="dialog-body">
            <slot />
          </div>

          <!-- 底部 -->
          <div v-if="$slots.footer || showFooter" class="dialog-footer">
            <slot name="footer">
              <Button
                v-if="cancelText"
                variant="ghost"
                @click="handleCancel"
              >
                {{ cancelText }}
              </Button>
              <Button
                v-if="confirmText"
                :variant="confirmButtonVariant"
                :loading="loading"
                @click="handleConfirm"
              >
                {{ confirmText }}
              </Button>
            </slot>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import Button from './Button.vue'

export interface DialogProps {
  modelValue?: boolean
  title?: string
  size?: 'small' | 'medium' | 'large'
  closable?: boolean
  closeOnOverlay?: boolean
  showFooter?: boolean
  confirmText?: string
  cancelText?: string
  confirmButtonVariant?: 'primary' | 'danger'
  loading?: boolean
  onConfirm?: () => void | Promise<void>
  onCancel?: () => void
}

const props = withDefaults(defineProps<DialogProps>(), {
  modelValue: false,
  size: 'medium',
  closable: true,
  closeOnOverlay: true,
  showFooter: true,
  confirmText: '确定',
  cancelText: '取消',
  confirmButtonVariant: 'primary',
  loading: false,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'confirm'): void
  (e: 'cancel'): void
  (e: 'close'): void
}>()

const visible = ref(props.modelValue)

watch(() => props.modelValue, (val) => {
  visible.value = val
  if (val) {
    // 阻止背景滚动
    document.body.style.overflow = 'hidden'
  } else {
    document.body.style.overflow = ''
  }
})

const dialogClasses = computed(() => {
  return [`dialog--${props.size}`]
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

const handleConfirm = async () => {
  emit('confirm')
  if (props.onConfirm) {
    await props.onConfirm()
  }
  if (!props.loading) {
    handleClose()
  }
}

const handleCancel = () => {
  emit('cancel')
  if (props.onCancel) {
    props.onCancel()
  }
  handleClose()
}
</script>

<style scoped>
.dialog-overlay {
  position: fixed;
  inset: 0;
  background: var(--color-bg-overlay);
  backdrop-filter: blur(4px);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  z-index: 1400;
}

.dialog {
  background: var(--color-bg-surface);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* 尺寸 */
.dialog--small {
  width: 320px;
  max-width: 100%;
}

.dialog--medium {
  width: 480px;
  max-width: 100%;
}

.dialog--large {
  width: 640px;
  max-width: 100%;
}

.dialog-header {
  position: relative;
  padding: var(--space-6);
  border-bottom: 1px solid var(--color-border);
}

.dialog-title {
  font-size: 18px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin: 0;
  padding-right: var(--space-8);
}

.dialog-close {
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

.dialog-close:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.dialog-body {
  padding: var(--space-6);
  overflow-y: auto;
  flex: 1;
  color: var(--color-text-secondary);
  line-height: 1.6;
}

.dialog-footer {
  padding: var(--space-4) var(--space-6);
  border-top: 1px solid var(--color-border);
  display: flex;
  gap: var(--space-3);
  justify-content: flex-end;
}

/* 动画 */
.dialog-enter-active,
.dialog-leave-active {
  transition: all var(--duration-slow) var(--ease-out);
}

.dialog-enter-active .dialog,
.dialog-leave-active .dialog {
  transition: all var(--duration-slow) var(--ease-spring);
}

.dialog-enter-from,
.dialog-leave-to {
  opacity: 0;
}

.dialog-enter-from .dialog {
  transform: scale(0.9) translateY(20px);
}

.dialog-leave-to .dialog {
  transform: scale(0.95);
}
</style>
