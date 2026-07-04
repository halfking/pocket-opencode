<template>
  <button
    :class="buttonClasses"
    :disabled="disabled || loading"
    @click="handleClick"
  >
    <span v-if="loading" class="button-spinner"></span>
    <span :class="{ 'button-content--loading': loading }">
      <slot />
    </span>
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'

export interface ButtonProps {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'small' | 'medium' | 'large'
  disabled?: boolean
  loading?: boolean
  block?: boolean
}

const props = withDefaults(defineProps<ButtonProps>(), {
  variant: 'primary',
  size: 'medium',
  disabled: false,
  loading: false,
  block: false,
})

const emit = defineEmits<{
  (e: 'click', event: MouseEvent): void
}>()

const buttonClasses = computed(() => {
  return [
    'button',
    `button--${props.variant}`,
    `button--${props.size}`,
    {
      'button--block': props.block,
      'button--disabled': props.disabled,
      'button--loading': props.loading,
    },
  ]
})

const handleClick = (event: MouseEvent) => {
  if (!props.disabled && !props.loading) {
    emit('click', event)
  }
}
</script>

<style scoped>
.button {
  /* 基础样式 */
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  font-family: var(--font-sans);
  font-weight: var(--font-weight-medium);
  border: none;
  border-radius: var(--radius-md);  /* 修改：lg → md (8px) */
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
  outline: none;
  position: relative;
  user-select: none;
  -webkit-tap-highlight-color: transparent;
}

/* 尺寸变体 - 更紧凑 */
.button--small {
  height: 28px;                    /* 修改：32px → 28px */
  padding: 0 var(--space-2-5);     /* 修改：space-3 → space-2-5 (10px) */
  font-size: 13px;                 /* 修改：14px → 13px */
}

.button--medium {
  height: 36px;                    /* 修改：40px → 36px */
  padding: 0 var(--space-4);       /* 14px */
  font-size: 14px;                 /* 修改：16px → 14px */
}

.button--large {
  height: 42px;                    /* 修改：48px → 42px */
  padding: 0 var(--space-5);       /* 18px */
  font-size: 16px;                 /* 修改：18px → 16px */
}

/* Primary 变体 */
.button--primary {
  background: var(--gradient-primary);
  color: white;
}

.button--primary:hover:not(.button--disabled):not(.button--loading) {
  opacity: 0.9;
  /* 移除 transform 和 box-shadow，更稳定 */
}

.button--primary:active:not(.button--disabled):not(.button--loading) {
  transform: scale(0.95);
}

/* Secondary 变体 */
.button--secondary {
  background: transparent;
  color: var(--color-primary);
  border: 1px solid var(--color-primary);
}

.button--secondary:hover:not(.button--disabled):not(.button--loading) {
  background: rgba(var(--color-primary-rgb), 0.1);
}

.button--secondary:active:not(.button--disabled):not(.button--loading) {
  transform: scale(0.95);
}

/* Ghost 变体 */
.button--ghost {
  background: transparent;
  color: var(--color-text-primary);
}

.button--ghost:hover:not(.button--disabled):not(.button--loading) {
  background: rgba(0, 0, 0, 0.05);
}

.button--ghost:active:not(.button--disabled):not(.button--loading) {
  transform: scale(0.95);
}

/* Danger 变体 */
.button--danger {
  background: var(--color-error);
  color: white;
}

.button--danger:hover:not(.button--disabled):not(.button--loading) {
  opacity: 0.9;
  /* 移除 transform 和 box-shadow */
}

.button--danger:active:not(.button--disabled):not(.button--loading) {
  transform: scale(0.95);
}

/* Block 模式 */
.button--block {
  width: 100%;
}

/* Disabled 状态 */
.button--disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Loading 状态 */
.button--loading {
  cursor: wait;
}

.button-content--loading {
  opacity: 0;
}

/* Loading 旋转器 */
.button-spinner {
  position: absolute;
  width: 16px;
  height: 16px;
  border: 2px solid currentColor;
  border-right-color: transparent;
  border-radius: 50%;
  animation: spin 0.6s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
