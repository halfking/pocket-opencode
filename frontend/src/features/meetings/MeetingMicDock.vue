<template>
  <button
    type="button"
    class="mic"
    :class="{ recording: recording, busy: busy }"
    :aria-label="recording ? '停止录音' : '开始录音'"
    :aria-pressed="recording"
    :disabled="busy"
    @click="$emit('toggle')"
  >
    <span class="icon" aria-hidden="true">{{ recording ? '⏹' : '🎤' }}</span>
    <span>{{ recording ? '停止' : '录音' }}</span>
  </button>
</template>

<script setup lang="ts">
defineProps<{ recording: boolean; busy?: boolean }>()
defineEmits<{ toggle: [] }>()
</script>

<style scoped>
.mic {
  position: fixed;
  right: var(--space-4);
  bottom: calc(var(--app-safe-bottom, 12px) + var(--space-4));
  min-width: 72px; height: 56px; padding: 0 16px;
  border: none; border-radius: 999px;
  background: var(--brand-gradient, linear-gradient(135deg, #667eea, #764ba2));
  color: var(--text-inverse); font-weight: 700; font-size: 14px;
  display: flex; align-items: center; justify-content: center; gap: 6px;
  box-shadow: var(--shadow-lg); z-index: var(--z-fab);
}
.mic.recording { background: var(--danger); }
.mic.busy { opacity: 0.75; }
.icon { font-size: 18px; }
</style>
