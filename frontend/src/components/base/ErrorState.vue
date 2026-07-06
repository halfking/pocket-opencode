<!--
  ErrorState — 通用错误状态展示组件。
  支持重试操作。
-->
<template>
  <div class="error-state">
    <div class="error-icon">{{ icon }}</div>
    <h3 v-if="title" class="error-title">{{ title }}</h3>
    <p v-if="message" class="error-message">{{ message }}</p>
    <button
      v-if="retryLabel"
      class="retry-btn"
      @click="$emit('retry')"
    >
      {{ retryLabel }}
    </button>
    <slot />
  </div>
</template>

<script setup lang="ts">
withDefaults(defineProps<{
  icon?: string
  title?: string
  message?: string
  retryLabel?: string
}>(), {
  icon: '⚠️',
  title: '出错了',
  message: '',
  retryLabel: '重试',
})

defineEmits<{
  (e: 'retry'): void
}>()
</script>

<style scoped>
.error-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-6) var(--space-3);
  text-align: center;
  color: var(--text-secondary);
}
.error-icon {
  font-size: 48px;
  margin-bottom: var(--space-3);
}
.error-title {
  font-size: var(--text-md);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0 0 var(--space-1);
}
.error-message {
  font-size: var(--text-sm);
  margin: 0 0 var(--space-3);
  color: var(--danger);
  max-width: 320px;
  word-break: break-word;
}
.retry-btn {
  padding: var(--space-2) var(--space-5);
  background: var(--brand-primary);
  color: var(--text-inverse);
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
  transition: opacity 120ms;
}
.retry-btn:active {
  opacity: 0.8;
}
</style>