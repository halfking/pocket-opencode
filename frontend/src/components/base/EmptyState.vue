<template>
  <div class="empty-state" :class="[`empty-state--${size}`, variant]">
    <span class="empty-icon" aria-hidden="true">{{ icon }}</span>
    <p class="empty-title">{{ title }}</p>
    <p v-if="hint" class="empty-hint">{{ hint }}</p>
    <button
      v-if="actionLabel"
      type="button"
      class="empty-action"
      @click="$emit('action')"
    >
      {{ actionLabel }}
    </button>
  </div>
</template>

<script setup lang="ts">
export interface EmptyStateProps {
  icon?: string
  title: string
  hint?: string
  actionLabel?: string
  size?: 'sm' | 'md'
  variant?: 'default' | 'inline'
}

withDefaults(defineProps<EmptyStateProps>(), {
  icon: '📭',
  size: 'md',
  variant: 'default',
})

defineEmits<{ action: [] }>()
</script>

<style scoped>
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: var(--space-6) var(--space-4);
  color: var(--text-muted);
}

.empty-state--inline {
  padding: var(--space-4) var(--space-3);
}

.empty-state--sm {
  padding: var(--space-4) var(--space-3);
}

.empty-icon {
  font-size: 40px;
  line-height: 1;
  margin-bottom: var(--space-2);
}

.empty-state--sm .empty-icon {
  font-size: 28px;
}

.empty-title {
  margin: 0 0 var(--space-1);
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-secondary);
}

.empty-state--sm .empty-title {
  font-size: var(--text-sm);
}

.empty-hint {
  margin: 0 0 var(--space-3);
  font-size: var(--text-sm);
  color: var(--text-muted);
  line-height: 1.4;
}

.empty-state--sm .empty-hint {
  font-size: var(--text-xs);
  margin-bottom: var(--space-2);
}

.empty-action {
  padding: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  color: var(--text-inverse);
  background: var(--brand-primary);
  border: none;
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.empty-action:active {
  opacity: 0.85;
}
</style>
