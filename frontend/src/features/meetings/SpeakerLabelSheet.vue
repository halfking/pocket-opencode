<template>
  <BottomSheet :open="open" @close="$emit('close')">
    <div class="speaker-sheet">
      <h3 class="sheet-title">标注说话人</h3>
      <p class="sheet-hint">选择一位说话人并输入姓名，之后同类声纹将自动识别</p>

      <div v-if="speakers.length === 0" class="empty">
        转写开始后说话人将出现在此
      </div>

      <div v-else class="speaker-list">
        <button
          v-for="sp in speakers"
          :key="sp.profileId"
          type="button"
          class="speaker-row"
          :class="{ active: selectedId === sp.profileId }"
          @click="selectedId = sp.profileId"
        >
          <span class="avatar">👤</span>
          <span class="name">{{ sp.label }}</span>
          <span v-if="sp.label.startsWith('说话人')" class="tag">待标注</span>
        </button>
      </div>

      <label v-if="selectedId" class="field">
        <span>显示名称</span>
        <input
          v-model="displayName"
          placeholder="如：张三"
          @keyup.enter="save"
        />
      </label>

      <button
        type="button"
        class="save-btn"
        :disabled="!selectedId || !displayName.trim()"
        @click="save"
      >
        保存标注
      </button>
    </div>
  </BottomSheet>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { BottomSheet } from '@/components'

const props = defineProps<{
  open: boolean
  speakers: { profileId: string; label: string }[]
}>()

const emit = defineEmits<{
  close: []
  label: [profileId: string, displayName: string]
}>()

const selectedId = ref('')
const displayName = ref('')

watch(() => props.open, (v) => {
  if (v) {
    selectedId.value = props.speakers[0]?.profileId ?? ''
    displayName.value = ''
  }
})

watch(selectedId, (id) => {
  const sp = props.speakers.find((s) => s.profileId === id)
  if (sp && !sp.label.startsWith('说话人')) {
    displayName.value = sp.label
  } else {
    displayName.value = ''
  }
})

function save() {
  if (!selectedId.value || !displayName.value.trim()) return
  emit('label', selectedId.value, displayName.value.trim())
  emit('close')
}
</script>

<style scoped>
.speaker-sheet {
  padding: var(--space-4);
}

.sheet-title {
  margin: 0 0 var(--space-2);
  font-size: 16px;
  font-weight: 600;
}

.sheet-hint {
  margin: 0 0 var(--space-4);
  font-size: 13px;
  color: var(--text-muted);
  line-height: 1.5;
}

.empty {
  padding: var(--space-4);
  text-align: center;
  color: var(--text-muted);
  font-size: 14px;
}

.speaker-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-bottom: var(--space-4);
}

.speaker-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: 12px;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  cursor: pointer;
  text-align: left;
  width: 100%;
}

.speaker-row.active {
  border-color: var(--brand-primary);
  background: rgba(var(--brand-primary-rgb, 102, 126, 234), 0.08);
}

.avatar { font-size: 20px; }

.name {
  flex: 1;
  font-size: 15px;
  font-weight: 500;
  color: var(--text-primary);
}

.tag {
  font-size: 11px;
  padding: 2px 8px;
  background: var(--bg-subtle);
  border-radius: var(--radius-full);
  color: var(--text-muted);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: var(--space-3);
  font-size: 13px;
}

.field span { color: var(--text-muted); font-size: 12px; }

.field input {
  padding: 10px 12px;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  font-size: 14px;
}

.save-btn {
  width: 100%;
  padding: 12px;
  background: var(--brand-primary);
  color: #fff;
  border: none;
  border-radius: var(--radius-md);
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
}

.save-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
