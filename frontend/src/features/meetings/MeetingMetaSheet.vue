<template>
  <BottomSheet :open="open" @close="$emit('close')">
    <div class="meta-sheet">
      <h3 class="meta-title">会议信息</h3>

      <label class="field">
        <span>标题</span>
        <input v-model="form.title" placeholder="自动生成或手动输入" />
      </label>

      <label class="field">
        <span>地点</span>
        <input v-model="form.location" placeholder="会议室 / 线上 / 地址" />
      </label>

      <label class="field">
        <span>参与人</span>
        <input
          v-model="participantsInput"
          placeholder="逗号分隔，如：张三, 李四"
          @blur="parseParticipants"
        />
      </label>

      <div class="field readonly">
        <span>开始时间</span>
        <span>{{ formatTime(form.startedAt) }}</span>
      </div>

      <button type="button" class="save-btn" @click="save">保存</button>
    </div>
  </BottomSheet>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { BottomSheet } from '@/components'

const props = defineProps<{
  open: boolean
  title?: string | null
  location?: string | null
  participants?: string[]
  startedAt?: number
}>()

const emit = defineEmits<{
  close: []
  save: [data: { title: string; location: string; participants: string[] }]
}>()

const form = reactive({
  title: '',
  location: '',
  startedAt: Date.now(),
})
const participantsInput = ref('')

watch(() => props.open, (v) => {
  if (v) {
    form.title = props.title ?? ''
    form.location = props.location ?? ''
    form.startedAt = props.startedAt ?? Date.now()
    participantsInput.value = (props.participants ?? []).join(', ')
  }
})

function parseParticipants() {
  form.title = form.title.trim()
}

function save() {
  const participants = participantsInput.value
    .split(/[,，]/)
    .map((s) => s.trim())
    .filter(Boolean)
  emit('save', { title: form.title, location: form.location, participants })
  emit('close')
}

function formatTime(ts: number): string {
  return new Date(ts).toLocaleString('zh-CN', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}
</script>

<style scoped>
.meta-sheet {
  padding: var(--space-4);
}

.meta-title {
  margin: 0 0 var(--space-4);
  font-size: 16px;
  font-weight: 600;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: var(--space-3);
  font-size: 13px;
}

.field span:first-child {
  color: var(--text-muted);
  font-size: 12px;
}

.field input {
  padding: 10px 12px;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  color: var(--text-primary);
  font-size: 14px;
}

.field.readonly {
  flex-direction: row;
  justify-content: space-between;
  align-items: center;
  color: var(--text-secondary);
}

.save-btn {
  width: 100%;
  padding: 12px;
  margin-top: var(--space-2);
  background: var(--brand-primary);
  color: #fff;
  border: none;
  border-radius: var(--radius-md);
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
}
</style>
