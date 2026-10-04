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
  /* 2026-10-06 修正：原为 calc(var(--app-safe-bottom,12px) + var(--space-4))
     = 14px，而 BottomNav 高 var(--bottom-chrome-height) = 56px
     （--bottomnav-height 56 + --app-safe-bottom）。本按钮高 56px ⇒ 底栏压住
     其中 42px（75%），且 .mic 的 z-index 是 --z-fab(60) < --z-bottom-nav(70)
     ⇒ 底栏绘制在按钮之上。改为跟随 chrome token，与 MeetingListView 的
     悬浮按钮同源。 */
  bottom: calc(var(--bottom-chrome-height) + var(--space-4));
  min-width: 72px; height: 56px; padding: 0 16px;
  border: none; border-radius: 999px;
  background: var(--brand-gradient, linear-gradient(135deg, #667eea, #764ba2));
  color: var(--text-inverse); font-weight: 700; font-size: var(--text-base);
  display: flex; align-items: center; justify-content: center; gap: 6px;
  box-shadow: var(--shadow-lg); z-index: var(--z-fab);
}
.mic.recording { background: var(--danger); }
.mic.busy { opacity: 0.75; }
.icon { font-size: var(--text-xl); }
</style>
