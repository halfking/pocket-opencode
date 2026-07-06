<!--
  EmptyState — 全屏/区块的空状态展示。
  用于没有数据时的友好引导。
-->
<template>
  <div class="empty-state">
    <div class="empty-icon">{{ icon }}</div>
    <h3 v-if="title" class="empty-title">{{ title }}</h3>
    <p v-if="message" class="empty-message">{{ message }}</p>
    <p v-if="hint" class="empty-hint">{{ hint }}</p>
    <button
      v-if="actionLabel"
      class="empty-action"
      @click="$emit('action')"
    >
      {{ actionLabel }}
    </button>
    <slot />
  </div>
</template>

<script setup lang="ts">
withDefaults(defineProps<{
  icon?: string
  title?: string
  message?: string
  hint?: string
  actionLabel?: string
}>(), {
  icon: '📭',
  title: '',
  message: '',
  hint: '',
  actionLabel: '',
})

defineEmits<{
  (e: 'action'): void
}>()
</script>

<style scoped>
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-6) var(--space-3);
  text-align: center;
  color: var(--text-secondary);
}
.empty-icon {
  font-size: 48px;
  margin-bottom: var(--space-3);
  opacity: 0.8;
}
.empty-title {
  font-size: var(--text-md);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0 0 var(--space-1);
}
.empty-message {
  font-size: var(--text-sm);
  margin: 0 0 var(--space-1);
  color: var(--text-secondary);
}
.empty-hint {
  font-size: var(--text-xs);
  margin: 0 0 var(--space-3);
  color: var(--text-muted);
}
.empty-action {
  margin-top: var(--space-3);
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
.empty-action:active {
  opacity: 0.8;
}
</style>