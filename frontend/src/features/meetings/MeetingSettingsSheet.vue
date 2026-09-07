<template>
  <BottomSheet :open="open" title="会议设置" @close="$emit('close')">
    <div class="meta">
      <label class="field">
        <span>标题</span>
        <div class="row">
          <input v-model="form.title" placeholder="自动生成或手动输入" />
          <button type="button" class="ghost" @click="onCaptureTitle">抓取</button>
        </div>
      </label>
      <label class="field">
        <span>主题</span>
        <input v-model="form.topic" placeholder="本场会议主题" />
      </label>
      <label class="field">
        <span>地点</span>
        <div class="row">
          <input v-model="form.location" placeholder="会议室 / 线上 / 地址" />
          <button type="button" class="ghost" :disabled="locating" @click="onCaptureLocation">
            {{ locating ? '定位…' : '自动' }}
          </button>
        </div>
      </label>
      <label class="field">
        <span>参与人</span>
        <input v-model="form.participants" placeholder="逗号分隔，如：张三, 李四" />
      </label>
      <label class="field">
        <span>标签</span>
        <div class="row">
          <input v-model="form.tags" placeholder="周会, 预算" />
          <button type="button" class="ghost" @click="onSuggestTags">提取</button>
        </div>
      </label>
      <div class="field">
        <span>总结技能</span>
        <div class="chips">
          <button
            v-for="s in MEETING_SKILLS"
            :key="s.id"
            type="button"
            class="chip"
            :class="{ active: form.summarySkill === s.id }"
            @click="form.summarySkill = s.id"
          >{{ s.label }}</button>
        </div>
        <p class="hint">{{ meetingSkillById(form.summarySkill).hint }}</p>
      </div>
      <button type="button" class="save" @click="onSave">保存</button>
    </div>
  </BottomSheet>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { BottomSheet } from '@/components'
import { MEETING_SKILLS, meetingSkillById } from './meeting-skills'
import { captureDeviceLocation, formatCapturedTitle, parseTagInput, suggestMeetingTags } from './meeting-meta'

const props = defineProps<{
  open: boolean
  title?: string | null
  topic?: string | null
  location?: string | null
  participants?: string[]
  tags?: string[]
  summarySkill?: string | null
  startedAt?: number
  transcriptHint?: string
}>()

const emit = defineEmits<{
  close: []
  save: [data: {
    title: string
    topic: string
    location: string
    participants: string[]
    tags: string[]
    summarySkill: string
  }]
}>()

const locating = ref(false)
const form = reactive({
  title: '', topic: '', location: '', participants: '', tags: '', summarySkill: 'meeting-minutes',
})

watch(() => props.open, (v) => {
  if (!v) return
  form.title = props.title ?? ''
  form.topic = props.topic ?? ''
  form.location = props.location ?? ''
  form.participants = (props.participants ?? []).join(', ')
  form.tags = (props.tags ?? []).join(', ')
  form.summarySkill = props.summarySkill || 'meeting-minutes'
})

function onCaptureTitle() {
  form.title = formatCapturedTitle({
    startedAt: props.startedAt ?? Date.now(),
    location: form.location,
    topic: form.topic,
    firstUtterance: props.transcriptHint,
  })
}

async function onCaptureLocation() {
  locating.value = true
  try {
    const loc = await captureDeviceLocation()
    if (loc) form.location = loc
  } finally {
    locating.value = false
  }
}

function onSuggestTags() {
  const extra = suggestMeetingTags([form.title, form.topic, props.transcriptHint].filter(Boolean).join(' '))
  form.tags = parseTagInput(`${form.tags},${extra.join(',')}`).join(', ')
}

function onSave() {
  emit('save', {
    title: form.title.trim(),
    topic: form.topic.trim(),
    location: form.location.trim(),
    participants: parseTagInput(form.participants.replace(/#/g, '')),
    tags: parseTagInput(form.tags),
    summarySkill: form.summarySkill,
  })
  emit('close')
}
</script>

<style scoped>
.meta { padding: 0 var(--space-1) var(--space-3); }
.field { display: flex; flex-direction: column; gap: 6px; margin-bottom: var(--space-3); }
.field > span { font-size: 12px; color: var(--text-muted); }
.field input {
  flex: 1; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-md);
  background: var(--bg-base); color: var(--text-primary); font-size: 14px;
}
.row { display: flex; gap: 8px; }
.ghost {
  flex-shrink: 0; padding: 0 10px; border-radius: var(--radius-md);
  border: 1px solid var(--border); background: var(--bg-subtle); font-size: 12px;
}
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chip {
  padding: 6px 12px; border-radius: 999px; border: 1px solid var(--border);
  background: var(--bg-card); color: var(--text-secondary); font-size: 13px;
}
.chip.active { background: var(--brand-bg); color: var(--brand-primary); border-color: var(--brand-primary); }
.hint { margin: 0; font-size: 12px; color: var(--text-muted); }
.save {
  width: 100%; padding: 12px; border: none; border-radius: var(--radius-md);
  background: var(--brand-primary); color: var(--text-inverse); font-weight: 600;
}
</style>
