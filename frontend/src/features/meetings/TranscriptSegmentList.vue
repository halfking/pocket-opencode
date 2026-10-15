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
        <!--
          §132/§131：来源徽章。它不是装饰 —— origin='manual' 决定这一段
          **不再被相邻去重裁碎**（用户校对过的正文享受豁免），
          所以「哪些行是我改过的」必须可见，否则用户会误以为去重在吃他的修改。
        -->
        <span v-if="seg.origin === 'manual'" class="origin-tag">已校对</span>
        <button
          v-if="editable && editingId !== seg.id"
          type="button"
          class="seg-edit-btn"
          :aria-label="`更正第 ${formatMs(seg.startMs)} 句`"
          @click="onStartEdit(seg)"
        >更正</button>
      </div>
      <!--
        ★ §131：这一行此前是**只读**的 `<p class="segment-text">{{ seg.text }}</p>`，
        仓里唯一的写入入口是底部 composer，而它 emit 的是 `append`（追加新段）。
        追加不是更正：原错句不动，改正句还会被 §132 的相邻去重裁碎。
        ⇒ 用户听错的人名/数字在 App 内没有任何更正办法。
        editable 默认 false：共用本页的会话录音面板没有落库/回写链路，
        打开一个没有出口的编辑框比不给更坏。
      -->
      <div v-if="editingId === seg.id" class="seg-editor">
        <textarea
          v-model="editingText"
          rows="3"
          aria-label="更正转写正文"
          @keydown.esc="onCancelEdit"
          @keydown.meta.enter="onCommitEdit"
          @keydown.ctrl.enter="onCommitEdit"
        />
        <div class="seg-editor-actions">
          <button type="button" class="seg-save" @click="onCommitEdit">保存</button>
          <button type="button" class="seg-cancel" @click="onCancelEdit">取消</button>
        </div>
      </div>
      <p v-else class="segment-text">{{ seg.text }}</p>
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
  /**
   * §131：是否开放逐段更正。默认 **false** ——
   * 共用本页的 `SessionLiveRecordPanel` 没有落库/回写链路，
   * 给它开一个没有出口的编辑框，比不给更坏。
   */
  editable?: boolean
}>(), { interimText: '' })

const emit = defineEmits<{
  append: [text: string]
  edit: [payload: { id: string; text: string }]
}>()
const draft = ref('')
const composerHint = '补一句转写，回车写入左栏并刷新右侧总结'
const editingId = ref<string | null>(null)
const editingText = ref('')

function onStartEdit(seg: MeetingSegment) {
  editingId.value = seg.id
  editingText.value = seg.text
}

function onCancelEdit() {
  editingId.value = null
  editingText.value = ''
}

function onCommitEdit() {
  const id = editingId.value
  if (id === null) return
  // 原样把空白交出去，判空/判未变在 `applySegmentTextEdit`（纯函数、可单测）；
  // 组件这层不做第二套判定 —— 两套规则迟早会分叉。
  emit('edit', { id, text: editingText.value })
  onCancelEdit()
}

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
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--brand-primary);
}

.lang-tag {
  font-size: var(--text-xs);
  padding: 1px 6px;
  background: var(--bg-subtle);
  border-radius: var(--radius-full);
  color: var(--text-muted);
}

.time {
  font-size: var(--text-2xs);
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

/* §131：来源徽章 + 逐段更正入口 */
.origin-tag {
  font-size: var(--text-2xs);
  padding: 1px 6px;
  border-radius: var(--radius-full);
  color: var(--success, var(--brand-primary));
  border: 1px solid var(--border);
}
.seg-edit-btn {
  margin-left: auto;
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  background: transparent;
  color: var(--text-secondary);
  font-size: var(--text-2xs);
  padding: 1px 8px;
}
.seg-editor { display: flex; flex-direction: column; gap: 6px; }
.seg-editor textarea {
  width: 100%;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
  font: inherit;
  line-height: 1.6;
  resize: vertical;
}
.seg-editor-actions { display: flex; gap: 8px; }
.seg-editor-actions button {
  padding: 4px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: var(--text-smd);
}
.seg-editor-actions .seg-save {
  background: var(--brand-primary);
  border-color: var(--brand-primary);
  color: var(--text-inverse);
}

.segment-text {
  margin: 0;
  font-size: var(--text-md);
  line-height: 1.6;
  color: var(--text-primary);
}
.segment-tr {
  margin: 4px 0 0;
  font-size: var(--text-smd);
  line-height: 1.5;
  color: var(--text-muted);
}

.listening {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-muted);
  font-size: var(--text-base);
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
