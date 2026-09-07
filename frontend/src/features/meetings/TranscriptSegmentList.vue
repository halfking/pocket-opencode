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
      <p v-if="seg.translation" class="segment-tr">{{ seg.translation }}</p>
    </div>

    <div v-if="interimText" class="segment interim">
      <div class="segment-meta"><span class="speaker">即时</span></div>
      <p class="segment-text">{{ interimText }}</p>
    </div>
    <div v-else-if="segments.length === 0 && isRecording" class="listening">
      <span class="pulse-dot" /> 正在聆听…
    </div>
    <form class="composer" @submit.prevent="onAppend">
      <input v-model="draft" :placeholder="composerHint" aria-label="补一句转写" />
      <button type="submit" :disabled="!draft.trim()">写入</button>
    </form>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import type { MeetingSegment } from './meetings-store'

const props = withDefaults(defineProps<{
  segments: MeetingSegment[]
  isRecording?: boolean
  interimText?: string
}>(), { interimText: '' })

const emit = defineEmits<{ append: [text: string] }>()
const draft = ref('')
const composerHint = '补一句转写，回车写入左栏并刷新右侧总结'

function onAppend() {
  const text = draft.value.trim()
  if (!text) return
  emit('append', text)
  draft.value = ''
}

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
.segment-tr {
  margin: 4px 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--text-muted);
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

.interim .segment-text { color: var(--text-secondary); }
.composer {
  display: flex; gap: 8px; margin-top: auto; padding-top: var(--space-2);
  position: sticky; bottom: 0; background: var(--bg-base);
}
.composer input {
  flex: 1; min-height: 40px; padding: 0 12px; border: 1px solid var(--border);
  border-radius: var(--radius-md); background: var(--bg-card); color: var(--text-primary);
}
.composer button {
  padding: 0 12px; border: none; border-radius: var(--radius-md);
  background: var(--brand-primary); color: var(--text-inverse); font-weight: 600;
}
.composer button:disabled { opacity: 0.4; }
</style>
