<template>
  <div class="loading-spinner" :class="spinnerClasses">
    <div class="spinner-circle"></div>
    <span v-if="text" class="spinner-text">{{ text }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

export interface LoadingProps {
  size?: 'small' | 'medium' | 'large'
  text?: string
  fullscreen?: boolean
}

const props = withDefaults(defineProps<LoadingProps>(), {
  size: 'medium',
  fullscreen: false,
})

const spinnerClasses = computed(() => {
  return [
    `loading-spinner--${props.size}`,
    {
      'loading-spinner--fullscreen': props.fullscreen,
    },
  ]
})
</script>

<style scoped>
.loading-spinner {
  display: inline-flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-3);
}

.loading-spinner--fullscreen {
  position: fixed;
  inset: 0;
  background: var(--color-bg-overlay);
  backdrop-filter: blur(4px);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: 9999;
}

.spinner-circle {
  border: 3px solid var(--color-border);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.loading-spinner--small .spinner-circle {
  width: 20px;
  height: 20px;
  border-width: 2px;
}

.loading-spinner--medium .spinner-circle {
  width: 32px;
  height: 32px;
  border-width: 3px;
}

.loading-spinner--large .spinner-circle {
  width: 48px;
  height: 48px;
  border-width: 4px;
}

.spinner-text {
  font-size: 14px;
  color: var(--color-text-secondary);
  font-weight: var(--font-weight-medium);
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
