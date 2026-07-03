<template>
  <div class="ai-thinking-indicator">
    <div class="thinking-dots">
      <span class="dot"></span>
      <span class="dot"></span>
      <span class="dot"></span>
    </div>
    <span class="thinking-text">{{ text }}</span>
  </div>
</template>

<script setup lang="ts">
export interface AIThinkingIndicatorProps {
  text?: string
}

withDefaults(defineProps<AIThinkingIndicatorProps>(), {
  text: 'AI 思考中...',
})
</script>

<style scoped>
.ai-thinking-indicator {
  display: inline-flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-4);
  background: linear-gradient(
    135deg,
    rgba(139, 92, 246, 0.1) 0%,
    rgba(167, 139, 250, 0.1) 100%
  );
  border-radius: var(--radius-full);
}

.thinking-dots {
  display: flex;
  gap: 6px;
  align-items: center;
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-primary);
  animation: bounce 1.4s ease-in-out infinite;
}

.dot:nth-child(1) {
  animation-delay: 0s;
}

.dot:nth-child(2) {
  animation-delay: 0.2s;
}

.dot:nth-child(3) {
  animation-delay: 0.4s;
}

@keyframes bounce {
  0%,
  60%,
  100% {
    transform: translateY(0);
    opacity: 1;
  }
  30% {
    transform: translateY(-10px);
    opacity: 0.7;
  }
}

.thinking-text {
  font-size: 13px;
  color: var(--color-primary);
  font-weight: var(--font-weight-medium);
}
</style>
