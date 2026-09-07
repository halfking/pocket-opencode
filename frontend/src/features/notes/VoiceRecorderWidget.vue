<!--
  列表页录音 FAB：点击切换开始/停止（不再长按）。
-->
<template>
  <div class="recorder-fab">
    <button
      class="fab"
      :class="{ recording }"
      type="button"
      :aria-label="recording ? '停止录音' : '开始录音'"
      :aria-pressed="recording"
      @click="$emit('toggle')"
    >
      <span class="fab-icon" aria-hidden="true">{{ recording ? '⏹' : '🎤' }}</span>
    </button>
    <div v-if="recording" class="pulse" aria-hidden="true" />
  </div>
</template>

<script setup lang="ts">
defineProps<{ recording: boolean }>()
defineEmits<{ toggle: [] }>()
</script>

<style scoped>
.recorder-fab {
  position: fixed;
  right: var(--space-5);
  bottom: calc(var(--bottom-chrome-height) + var(--space-4));
  z-index: var(--z-fab);
}
.fab {
  width: 60px;
  height: 60px;
  border-radius: 50%;
  border: none;
  background: var(--brand-gradient);
  color: var(--text-inverse);
  font-size: 24px;
  box-shadow: var(--shadow-lg);
  cursor: pointer;
}
.fab.recording { transform: scale(1.1); background: var(--danger); }
.pulse {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  border: 2px solid var(--danger);
  animation: pulse var(--duration-slow) infinite;
}
@keyframes pulse {
  0% { transform: scale(1); opacity: 0.8; }
  100% { transform: scale(1.8); opacity: 0; }
}
</style>
