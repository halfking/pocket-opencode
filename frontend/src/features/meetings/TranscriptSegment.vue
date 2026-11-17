<template>
  <div 
    class="transcript-segment"
    :class="{
      'low-confidence': showConfidence,
      'editing': isEditing
    }"
  >
    <!-- Speaker header -->
    <div v-if="segment.speaker" class="speaker-header">
      <div class="speaker-avatar" :style="{ background: segment.speaker.color }">
        {{ segment.speaker.label.charAt(0) }}
      </div>
      <span class="speaker-name">{{ segment.speaker.label }}</span>
      <span class="timestamp">{{ formatTime(segment.startMs) }}</span>
      <span v-if="showConfidence" class="confidence-badge" :class="getConfidenceClass()">
        {{ Math.round(segment.confidence * 100) }}%
      </span>
    </div>

    <!-- Segment text (editable if allowed) -->
    <div 
      ref="textRef"
      class="segment-text"
      :contenteditable="editable && !isEditing"
      @click="onTextClick"
      @blur="onBlur"
      @keydown="onKeydown"
    >
      {{ segment.text }}
    </div>

    <!-- Edit actions -->
    <div v-if="isEditing" class="edit-actions">
      <button type="button" class="btn-save" @click="saveEdit">
        ✓ 保存
      </button>
      <button type="button" class="btn-cancel" @click="cancelEdit">
        ✕ 取消
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import type { TranscriptSegment as TranscriptSegmentType } from './three-layer-renderer'

interface Props {
  segment: TranscriptSegmentType
  editable: boolean
  showConfidence?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  showConfidence: false
})

const emit = defineEmits<{
  edit: [segmentId: string, newText: string]
}>()

const textRef = ref<HTMLElement>()
const isEditing = ref(false)
const originalText = ref('')

function formatTime(ms: number): string {
  const totalSeconds = Math.floor(ms / 1000)
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes.toString().padStart(2, '0')}:${seconds.toString().padStart(2, '0')}`
}

function getConfidenceClass(): string {
  if (props.segment.confidence >= 0.8) return 'high'
  if (props.segment.confidence >= 0.6) return 'medium'
  return 'low'
}

function onTextClick() {
  if (!props.editable) return
  
  isEditing.value = true
  originalText.value = props.segment.text
  
  // Focus and select all text
  setTimeout(() => {
    if (!textRef.value) return
    
    const range = document.createRange()
    range.selectNodeContents(textRef.value)
    const selection = window.getSelection()
    selection?.removeAllRanges()
    selection?.addRange(range)
  }, 0)
}

function saveEdit() {
  if (!textRef.value) return
  
  const newText = textRef.value.textContent?.trim() || ''
  if (newText && newText !== originalText.value) {
    emit('edit', props.segment.id, newText)
  }
  
  isEditing.value = false
}

function cancelEdit() {
  if (textRef.value) {
    textRef.value.textContent = originalText.value
  }
  isEditing.value = false
}

function onBlur() {
  // Don't auto-save on blur, let user click save/cancel
  // This prevents accidental saves when clicking elsewhere
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    saveEdit()
  } else if (e.key === 'Escape') {
    e.preventDefault()
    cancelEdit()
  }
}

// Reset editing state when segment changes
watch(() => props.segment.id, () => {
  isEditing.value = false
})
</script>

<style scoped>
.transcript-segment {
  background: white;
  border-radius: 8px;
  padding: 12px 16px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
  transition: all 0.2s;
}

.transcript-segment.low-confidence {
  background: #fef3c7;
  border-left: 3px solid #f59e0b;
}

.transcript-segment.editing {
  box-shadow: 0 0 0 3px #3b82f6;
}

.speaker-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.speaker-avatar {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  font-weight: bold;
  font-size: 14px;
}

.speaker-name {
  font-weight: 600;
  color: #1f2937;
}

.timestamp {
  color: #6b7280;
  font-size: 0.875em;
  margin-left: auto;
}

.confidence-badge {
  padding: 2px 8px;
  border-radius: 12px;
  font-size: 0.75em;
  font-weight: 600;
}

.confidence-badge.high {
  background: #d1fae5;
  color: #065f46;
}

.confidence-badge.medium {
  background: #fef3c7;
  color: #92400e;
}

.confidence-badge.low {
  background: #fee2e2;
  color: #991b1b;
}

.segment-text {
  color: #1f2937;
  line-height: 1.6;
  font-size: 15px;
  cursor: text;
  min-height: 24px;
  outline: none;
}

.segment-text:focus {
  background: #f3f4f6;
  border-radius: 4px;
  padding: 4px;
}

.edit-actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid #e5e7eb;
}

.btn-save,
.btn-cancel {
  padding: 6px 12px;
  border: none;
  border-radius: 6px;
  font-size: 14px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-save {
  background: #10b981;
  color: white;
}

.btn-save:hover {
  background: #059669;
}

.btn-cancel {
  background: #e5e7eb;
  color: #374151;
}

.btn-cancel:hover {
  background: #d1d5db;
}
</style>
