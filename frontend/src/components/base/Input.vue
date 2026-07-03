<template>
  <input
    :type="type"
    :class="inputClasses"
    :value="modelValue"
    :placeholder="placeholder"
    :disabled="disabled"
    :readonly="readonly"
    @input="handleInput"
    @focus="handleFocus"
    @blur="handleBlur"
  />
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'

export interface InputProps {
  modelValue?: string | number
  type?: 'text' | 'password' | 'email' | 'number' | 'tel' | 'search'
  placeholder?: string
  disabled?: boolean
  readonly?: boolean
  error?: boolean
  size?: 'small' | 'medium' | 'large'
}

const props = withDefaults(defineProps<InputProps>(), {
  type: 'text',
  disabled: false,
  readonly: false,
  error: false,
  size: 'medium',
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'focus', event: FocusEvent): void
  (e: 'blur', event: FocusEvent): void
}>()

const isFocused = ref(false)

const inputClasses = computed(() => {
  return [
    'input',
    `input--${props.size}`,
    {
      'input--focused': isFocused.value,
      'input--error': props.error,
      'input--disabled': props.disabled,
    },
  ]
})

const handleInput = (e: Event) => {
  const target = e.target as HTMLInputElement
  emit('update:modelValue', target.value)
}

const handleFocus = (e: FocusEvent) => {
  isFocused.value = true
  emit('focus', e)
}

const handleBlur = (e: FocusEvent) => {
  isFocused.value = false
  emit('blur', e)
}
</script>

<style scoped>
.input {
  width: 100%;
  font-family: var(--font-sans);
  font-size: 16px;
  color: var(--color-text-primary);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  outline: none;
  transition: all var(--duration-base) var(--ease-out);
}

.input::placeholder {
  color: var(--color-text-tertiary);
}

/* 尺寸 */
.input--small {
  height: 32px;
  padding: 0 var(--space-3);
  font-size: 14px;
}

.input--medium {
  height: 40px;
  padding: 0 var(--space-4);
  font-size: 16px;
}

.input--large {
  height: 48px;
  padding: 0 var(--space-4);
  font-size: 18px;
}

/* 状态 */
.input--focused {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px rgba(var(--color-primary-rgb), 0.1);
}

.input--error {
  border-color: var(--color-error);
}

.input--error.input--focused {
  box-shadow: 0 0 0 3px rgba(239, 68, 68, 0.1);
}

.input--disabled {
  opacity: 0.5;
  cursor: not-allowed;
  background: var(--color-bg-base);
}

/* 搜索框样式 */
.input[type='search'] {
  padding-left: var(--space-10);
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='20' height='20' viewBox='0 0 24 24' fill='none' stroke='%23666' stroke-width='2'%3E%3Ccircle cx='11' cy='11' r='8'/%3E%3Cpath d='m21 21-4.35-4.35'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: 12px center;
}

.input[type='search']::-webkit-search-cancel-button {
  -webkit-appearance: none;
  appearance: none;
}
</style>
