<template>
  <BottomSheet :open="open" title="笔记属性" @close="onClose">
    <div class="meta">
      <label class="field">
        <span>标题</span>
        <input v-model="form.title" placeholder="自动生成或手动输入" />
      </label>
      <div class="field">
        <span>分类</span>
        <div class="chips">
          <button
            v-for="d in DOMAINS"
            :key="d.value"
            type="button"
            class="chip"
            :class="{ active: form.domain === d.value }"
            @click="form.domain = d.value"
          >{{ d.label }}</button>
        </div>
      </div>
      <label class="field">
        <span>标签</span>
        <div class="tag-row">
          <input v-model="form.tagsInput" placeholder="逗号分隔" />
          <button type="button" class="extract" :disabled="extracting" @click="onExtract">
            {{ extracting ? '提取中…' : '一键提取' }}
          </button>
        </div>
      </label>
      <div class="actions">
        <button type="button" class="btn danger" @click="emit('delete')">删除</button>
        <button type="button" class="btn primary" @click="onSave">保存</button>
      </div>
    </div>
  </BottomSheet>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { BottomSheet } from '../../components'
import { extractLocalTags, inferDomain, mergeTags, suggestTitle } from './note-tags'
import { extractTagsWithAi } from './note-search'

const DOMAINS = [
  { value: 'work', label: '工作' },
  { value: 'study', label: '学习' },
  { value: 'life', label: '生活' },
  { value: 'idea', label: '想法' },
] as const

const props = defineProps<{
  open: boolean
  title?: string | null
  content?: string
  domain?: string | null
  tags?: string[] | null
}>()

const emit = defineEmits<{
  close: []
  save: [data: { title: string; domain: string; tags: string[] }]
  delete: []
}>()

const form = reactive({ title: '', domain: 'work', tagsInput: '' })
const extracting = ref(false)

watch(() => props.open, (v) => {
  if (!v) return
  form.title = props.title || suggestTitle(props.content || '')
  form.domain = props.domain || inferDomain(props.content || '')
  form.tagsInput = (props.tags ?? []).join(', ')
})

async function onExtract() {
  extracting.value = true
  try {
    const local = extractLocalTags(props.content || '')
    let extra: string[] = []
    try { extra = await extractTagsWithAi(props.content || '') } catch { /* offline */ }
    form.tagsInput = mergeTags(form.tagsInput.split(/[,，]/).map((s) => s.trim()), [...local, ...extra]).join(', ')
    if (!props.domain) form.domain = inferDomain(props.content || '')
    if (!form.title) form.title = suggestTitle(props.content || '')
  } finally {
    extracting.value = false
  }
}

function onSave() {
  emit('save', {
    title: form.title.trim(),
    domain: form.domain,
    tags: form.tagsInput.split(/[,，]/).map((s) => s.trim()).filter(Boolean),
  })
}

function onClose() {
  emit('close')
}
</script>

<style scoped>
.meta { padding: 0 var(--space-1) var(--space-3); }
.field { display: flex; flex-direction: column; gap: 6px; margin-bottom: var(--space-3); }
.field > span { font-size: 12px; color: var(--text-muted); }
.field input {
  padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-md);
  background: var(--bg-base); color: var(--text-primary); font-size: 14px;
}
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chip {
  padding: 6px 12px; border-radius: 999px; border: 1px solid var(--border);
  background: var(--bg-card); color: var(--text-secondary); font-size: 13px;
}
.chip.active { background: var(--brand-bg); color: var(--brand-primary); border-color: var(--brand-primary); }
.tag-row { display: flex; gap: 8px; }
.tag-row input { flex: 1; }
.extract {
  flex-shrink: 0; padding: 0 10px; border-radius: var(--radius-md);
  border: 1px solid var(--border); background: var(--bg-subtle); font-size: 12px;
}
.actions { display: flex; gap: var(--space-2); margin-top: var(--space-2); }
.btn { flex: 1; padding: 12px; border-radius: var(--radius-md); border: none; font-weight: 600; }
.btn.primary { background: var(--brand-primary); color: var(--text-inverse); }
.btn.danger { background: var(--danger-bg); color: var(--danger); }
</style>
