<template>
  <div class="slr" role="region" aria-label="会话实时录音">
    <div class="slr-bar">
      <span class="slr-dot" aria-hidden="true" />
      <span class="slr-label">录音中</span>
      <span class="slr-time">{{ recorder.formatElapsed() }}</span>
      <select
        v-if="recorder.inputs.value.length > 1"
        class="slr-mic"
        :value="recorder.selectedInput.value?.deviceId"
        aria-label="选择麦克风"
        @change="onMicChange"
      >
        <option v-for="m in recorder.inputs.value" :key="m.deviceId" :value="m.deviceId">
          {{ kindLabel(m.kind) }} · {{ m.label }}
        </option>
      </select>
      <span v-else-if="recorder.selectedInput.value" class="slr-mic-name">
        {{ recorder.selectedInput.value.label }}
      </span>
      <button type="button" class="slr-stop" @click="emit('stop')">停止</button>
    </div>
    <p v-if="recorder.sttError.value" class="slr-err">{{ recorder.sttError.value }}</p>
    <p v-if="recorder.processingCount.value > 0" class="slr-hint">
      转写中…（{{ recorder.processingCount.value }} 段）
    </p>
    <TranscriptSegmentList
      :segments="recorder.segments.value"
      :is-recording="recorder.isRecording.value"
    />
    <LiveSummaryPanel
      :summary="summary.liveSummary.value"
      :recommendations="summary.recommendations.value"
      :is-updating="summary.isUpdating.value"
    />
  </div>
</template>

<script setup lang="ts">
import TranscriptSegmentList from '../meetings/TranscriptSegmentList.vue'
import LiveSummaryPanel from '../meetings/LiveSummaryPanel.vue'
import type { useMeetingRecorder } from '../../composables/useMeetingRecorder'
import type { useLiveSummary } from '../../composables/useLiveSummary'

const props = defineProps<{
  recorder: ReturnType<typeof useMeetingRecorder>
  summary: ReturnType<typeof useLiveSummary>
}>()

const emit = defineEmits<{ (e: 'stop'): void }>()

function kindLabel(kind: string) {
  const map: Record<string, string> = {
    bluetooth: '蓝牙', headset: '耳机', usb: 'USB', builtin: '内置', unknown: '麦克风',
  }
  return map[kind] ?? kind
}

function onMicChange(e: Event) {
  const id = (e.target as HTMLSelectElement).value
  void props.recorder.switchDevice(id)
}
</script>

<style scoped>
.slr {
  display: flex;
  flex-direction: column;
  max-height: 42vh;
  border-top: 1px solid var(--color-border);
  background: var(--color-bg-surface);
}
.slr-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 44px;
  padding: 8px 12px;
}
.slr-dot {
  width: 8px; height: 8px; border-radius: 50%;
  background: #ef4444;
  animation: slr-pulse 1.2s ease-in-out infinite;
}
@keyframes slr-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.35; }
}
.slr-label { font-size: 13px; font-weight: 600; color: #ef4444; }
.slr-time { font-variant-numeric: tabular-nums; font-size: 13px; color: var(--color-text-secondary); }
.slr-mic, .slr-mic-name {
  flex: 1;
  min-width: 0;
  font-size: 12px;
  color: var(--color-text-secondary);
}
.slr-mic {
  max-width: 46%;
  height: 32px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-bg-base, transparent);
}
.slr-stop {
  height: 32px;
  padding: 0 12px;
  border: none;
  border-radius: 999px;
  background: #ef4444;
  color: #fff;
  font-size: 13px;
}
.slr-err { margin: 0 12px 8px; font-size: 12px; color: #ef4444; }
.slr-hint { margin: 0 12px 8px; font-size: 12px; color: var(--color-text-tertiary); }
</style>
