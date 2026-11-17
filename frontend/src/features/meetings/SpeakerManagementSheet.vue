<template>
  <Sheet v-model="isOpen" title="说话人管理">
    <div class="speaker-management">
      <div class="speaker-count">
        共识别到 <strong>{{ speakers.length }}</strong> 位说话人
      </div>

      <!-- Speaker list -->
      <div class="speaker-list">
        <div
          v-for="speaker in speakers"
          :key="speaker.profileId"
          class="speaker-card"
        >
          <!-- Avatar with color -->
          <div class="speaker-avatar" :style="{ background: speaker.color }">
            {{ (speaker.customName || speaker.label).charAt(0) }}
          </div>

          <!-- Speaker info -->
          <div class="speaker-info">
            <input
              v-model="speaker.customName"
              type="text"
              class="speaker-name-input"
              :placeholder="speaker.label"
              @blur="onRename(speaker)"
            />
            <div class="speaker-stats">
              {{ speaker.segmentCount }} 段对话
            </div>
          </div>

          <!-- Actions -->
          <div class="speaker-actions">
            <button
              type="button"
              class="btn-icon"
              title="选择颜色"
              @click="openColorPicker(speaker)"
            >
              🎨
            </button>
            <button
              type="button"
              class="btn-icon"
              title="合并到其他说话人"
              @click="openMergeDialog(speaker)"
            >
              🔗
            </button>
            <button
              v-if="speakers.length > 1"
              type="button"
              class="btn-icon danger"
              title="删除"
              @click="confirmDelete(speaker)"
            >
              🗑️
            </button>
          </div>
        </div>
      </div>

      <!-- Color picker dialog -->
      <div v-if="colorPickerTarget" class="dialog-overlay" @click.self="closeColorPicker">
        <div class="color-picker-dialog">
          <h3>选择颜色</h3>
          <div class="color-grid">
            <button
              v-for="color in availableColors"
              :key="color"
              class="color-button"
              :style="{ background: color }"
              @click="changeColor(color)"
            />
          </div>
          <button type="button" class="btn-cancel" @click="closeColorPicker">
            取消
          </button>
        </div>
      </div>

      <!-- Merge dialog -->
      <div v-if="mergeSource" class="dialog-overlay" @click.self="closeMergeDialog">
        <div class="merge-dialog">
          <h3>合并说话人</h3>
          <p>将 <strong>{{ mergeSource.customName || mergeSource.label }}</strong> 合并到：</p>
          <div class="merge-targets">
            <button
              v-for="target in mergeCandidates"
              :key="target.profileId"
              class="merge-target"
              @click="executeMerge(target)"
            >
              <div class="speaker-avatar small" :style="{ background: target.color }">
                {{ (target.customName || target.label).charAt(0) }}
              </div>
              <span>{{ target.customName || target.label }}</span>
            </button>
          </div>
          <button type="button" class="btn-cancel" @click="closeMergeDialog">
            取消
          </button>
        </div>
      </div>

      <!-- Actions -->
      <div class="actions">
        <button type="button" class="btn-primary" @click="close">
          完成
        </button>
      </div>
    </div>
  </Sheet>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import Sheet from '@/components/interactive/Sheet.vue'
import type { SpeakerProfile } from '@/native/speaker-diarizer'

interface Props {
  modelValue: boolean
  speakers: SpeakerProfile[]
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  rename: [profileId: string, customName: string]
  changeColor: [profileId: string, color: string]
  merge: [sourceId: string, targetId: string]
  delete: [profileId: string]
}>()

const isOpen = computed({
  get: () => props.modelValue,
  set: (val) => emit('update:modelValue', val)
})

// Color picker state
const colorPickerTarget = ref<SpeakerProfile | null>(null)
const availableColors = [
  '#3B82F6', '#EF4444', '#10B981', '#F59E0B',
  '#8B5CF6', '#EC4899', '#14B8A6', '#F97316',
  '#6366F1', '#F43F5E', '#06B6D4', '#84CC16'
]

// Merge state
const mergeSource = ref<SpeakerProfile | null>(null)
const mergeCandidates = computed(() => 
  props.speakers.filter(s => s.profileId !== mergeSource.value?.profileId)
)

function onRename(speaker: SpeakerProfile) {
  if (speaker.customName?.trim()) {
    emit('rename', speaker.profileId, speaker.customName.trim())
  }
}

function openColorPicker(speaker: SpeakerProfile) {
  colorPickerTarget.value = speaker
}

function closeColorPicker() {
  colorPickerTarget.value = null
}

function changeColor(color: string) {
  if (colorPickerTarget.value) {
    emit('changeColor', colorPickerTarget.value.profileId, color)
    closeColorPicker()
  }
}

function openMergeDialog(speaker: SpeakerProfile) {
  mergeSource.value = speaker
}

function closeMergeDialog() {
  mergeSource.value = null
}

function executeMerge(target: SpeakerProfile) {
  if (mergeSource.value) {
    emit('merge', mergeSource.value.profileId, target.profileId)
    closeMergeDialog()
  }
}

function confirmDelete(speaker: SpeakerProfile) {
  const confirmed = confirm(
    `确定要删除说话人 "${speaker.customName || speaker.label}" 吗？\n\n相关的 ${speaker.segmentCount} 段对话将被标记为"未知说话人"。`
  )
  if (confirmed) {
    emit('delete', speaker.profileId)
  }
}

function close() {
  isOpen.value = false
}
</script>

<style scoped>
.speaker-management {
  padding: 16px;
}

.speaker-count {
  margin-bottom: 16px;
  padding: 12px;
  background: #eff6ff;
  border-radius: 8px;
  text-align: center;
  color: #1e40af;
}

.speaker-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-bottom: 24px;
}

.speaker-card {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  background: white;
  border: 2px solid #e5e7eb;
  border-radius: 8px;
  transition: border-color 0.2s;
}

.speaker-card:hover {
  border-color: #3b82f6;
}

.speaker-avatar {
  width: 48px;
  height: 48px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  font-weight: bold;
  font-size: 20px;
  flex-shrink: 0;
}

.speaker-avatar.small {
  width: 32px;
  height: 32px;
  font-size: 16px;
}

.speaker-info {
  flex: 1;
  min-width: 0;
}

.speaker-name-input {
  width: 100%;
  padding: 6px 8px;
  border: 1px solid #e5e7eb;
  border-radius: 4px;
  font-size: 15px;
  font-weight: 600;
  margin-bottom: 4px;
}

.speaker-name-input:focus {
  outline: none;
  border-color: #3b82f6;
}

.speaker-stats {
  font-size: 13px;
  color: #6b7280;
}

.speaker-actions {
  display: flex;
  gap: 4px;
}

.btn-icon {
  width: 36px;
  height: 36px;
  border: none;
  background: #f3f4f6;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s;
  font-size: 18px;
}

.btn-icon:hover {
  background: #e5e7eb;
  transform: scale(1.05);
}

.btn-icon.danger:hover {
  background: #fee2e2;
}

.dialog-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.color-picker-dialog,
.merge-dialog {
  background: white;
  border-radius: 12px;
  padding: 24px;
  max-width: 90%;
  width: 320px;
  box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.1);
}

.color-picker-dialog h3,
.merge-dialog h3 {
  margin: 0 0 16px;
  font-size: 18px;
  font-weight: 600;
}

.color-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
  margin-bottom: 16px;
}

.color-button {
  width: 100%;
  aspect-ratio: 1;
  border: 3px solid transparent;
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.2s;
}

.color-button:hover {
  border-color: white;
  box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1);
  transform: scale(1.1);
}

.merge-dialog p {
  margin: 0 0 16px;
  color: #6b7280;
}

.merge-targets {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 16px;
}

.merge-target {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border: 2px solid #e5e7eb;
  border-radius: 8px;
  background: white;
  cursor: pointer;
  transition: all 0.2s;
  text-align: left;
}

.merge-target:hover {
  border-color: #3b82f6;
  background: #eff6ff;
}

.btn-cancel {
  width: 100%;
  padding: 10px;
  border: none;
  background: #f3f4f6;
  color: #374151;
  border-radius: 8px;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s;
}

.btn-cancel:hover {
  background: #e5e7eb;
}

.actions {
  margin-top: 24px;
}

.btn-primary {
  width: 100%;
  padding: 12px;
  border: none;
  background: #3b82f6;
  color: white;
  border-radius: 8px;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s;
}

.btn-primary:hover {
  background: #2563eb;
}
</style>
