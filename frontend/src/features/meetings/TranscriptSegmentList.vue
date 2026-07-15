<template>
  <div ref="containerRef" class="transcript-list">
    <div
      v-for="seg in segments"
      :key="seg.id"
      class="segment"
    >
      <div class="segment-meta">
        <span class="speaker">{{ seg.speakerLabel ?? '说话人' }}</span>
        <span class="lang-tag">{{ langLabel(seg.lang) }}</span>
        <span class="time">{{ formatMs(seg.startMs) }}</span>
      </div>
      <p class="segment-text">{{ seg.text }}</p>
    </div>

    <div v-if="segments.length === 0 && isRecording" class="listening">
      <span class="pulse-dot" /> 正在聆听…
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import type { MeetingSegment } from './meetings-store'

const props = defineProps<{
  segments: MeetingSegment[]
  isRecording?: boolean
}>()

const containerRef = ref<HTMLElement>()

const LANG_LABELS: Record<string, string> = {
  zh: '中文', en: 'EN', ja: 'JA', ko: 'KO', mixed: '混合', fr: 'FR', de: 'DE',
}

function langLabel(lang: string): string {
  return LANG_LABELS[lang] ?? lang.toUpperCase()
}

function formatMs(ms: number): string {
  const s = Math.floor(ms / 1000)
  const m = Math.floor(s / 60)
  return `${m}:${(s % 60).toString().padStart(2, '0')}`
}

watch(() => props.segments.length, async () => {
  await nextTick()
  if (containerRef.value) {
    containerRef.value.scrollTop = containerRef.value.scrollHeight
  }
})
</script>

<style scoped>
.transcript-list {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-3);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.segment {
  animation: fade-in 0.3s ease;
}

@keyframes fade-in {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
}

.segment-meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: 4px;
}

.speaker {
  font-size: 12px;
  font-weight: 600;
  color: var(--brand-primary);
}

.lang-tag {
  font-size: 10px;
  padding: 1px 6px;
  background: var(--bg-subtle);
  border-radius: var(--radius-full);
  color: var(--text-muted);
}

.time {
  font-size: 11px;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.segment-text {
  margin: 0;
  font-size: 15px;
  line-height: 1.6;
  color: var(--text-primary);
}

.listening {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-muted);
  font-size: 14px;
  padding: var(--space-4) 0;
}

.pulse-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-error, #ef4444);
  animation: pulse 1.5s ease infinite;
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}
</style>
