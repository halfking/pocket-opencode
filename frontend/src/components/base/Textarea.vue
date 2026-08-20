<template>
  <textarea
    :class="textareaClasses"
    :value="modelValue"
    :rows="rows"
    :placeholder="placeholder"
    :disabled="disabled"
    :readonly="readonly"
    @input="handleInput"
    @focus="handleFocus"
    @blur="handleBlur"
  />
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

export interface TextareaProps {
  modelValue?: string | number
  placeholder?: string
  disabled?: boolean
  readonly?: boolean
  error?: boolean
  size?: 'small' | 'medium' | 'large'
  rows?: number
}

const props = withDefaults(defineProps<TextareaProps>(), {
  disabled: false,
  readonly: false,
  error: false,
  size: 'medium',
  rows: 3,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'focus', event: FocusEvent): void
  (e: 'blur', event: FocusEvent): void
}>()

const isFocused = ref(false)

const textareaClasses = computed(() => [
  'textarea',
  `textarea--${props.size}`,
  {
    'textarea--focused': isFocused.value,
    'textarea--error': props.error,
    'textarea--disabled': props.disabled,
  },
])

function handleInput(event: Event) {
  emit('update:modelValue', (event.target as HTMLTextAreaElement).value)
}

function handleFocus(event: FocusEvent) {
  isFocused.value = true
  emit('focus', event)
}

function handleBlur(event: FocusEvent) {
  isFocused.value = false
  emit('blur', event)
}
</script>

<style scoped>
.textarea {
  width: 100%;
  box-sizing: border-box;
  font-family: var(--font-sans);
  color: var(--color-text-primary);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  outline: none;
  resize: vertical;
  transition: border-color var(--duration-base) var(--ease-out), box-shadow var(--duration-base) var(--ease-out);
}

.textarea::placeholder {
  color: var(--color-text-tertiary);
}

.textarea--small {
  min-height: 64px;
  padding: var(--space-2) var(--space-3);
  font-size: 14px;
}

.textarea--medium {
  min-height: 80px;
  padding: var(--space-2) var(--space-4);
  font-size: 16px;
}

.textarea--large {
  min-height: 112px;
  padding: var(--space-3) var(--space-4);
  font-size: 18px;
}

.textarea--focused {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 3px rgba(var(--color-primary-rgb), 0.1);
}

.textarea--error {
  border-color: var(--color-error);
}

.textarea--error.textarea--focused {
  box-shadow: 0 0 0 3px rgba(var(--color-danger-rgb), 0.1);
}

.textarea--disabled {
  opacity: 0.5;
  cursor: not-allowed;
  background: var(--color-bg-base);
}
</style>
