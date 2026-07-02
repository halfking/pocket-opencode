<!--
  NoteEditView — 笔记新建 / 编辑表单。

  - /notes/new → 新建模式（id === 'new'）
  - /notes/:id/edit → 编辑模式
  - 复用 VoiceRecorderWidget（在 NoteListView 也用的那个）
  - 表单字段：title / content / domain (chip) / tags（逗号分隔）
  - 编辑模式读取已有笔记的字段初始化
-->
<template>
  <div class="note-edit-view">
    <AppLayout>
      <div v-if="loading" class="state">加载中…</div>

      <form v-else class="edit-form" @submit.prevent="onSave">
        <!-- 标题 -->
        <div class="form-group">
          <label for="note-title">标题</label>
          <input
            id="note-title"
            ref="titleInput"
            v-model="form.title"
            type="text"
            placeholder="一句话概括…"
            class="title-input"
          />
        </div>

        <!-- 正文 -->
        <div class="form-group">
          <label for="note-content">
            正文
            <span class="hint">支持 Markdown · 语音录入自动填入</span>
          </label>
          <textarea
            id="note-content"
            v-model="form.content"
            rows="20"
            placeholder="长按下方麦克风开始语音录入，或直接输入文本…"
            class="content-input"
          />
        </div>

        <!-- 域 -->
        <div class="form-group">
          <label>分类</label>
          <div class="domain-chips">
            <button
              v-for="d in DOMAINS"
              :key="d.value"
              type="button"
              class="chip"
              :class="{
                active: form.domain === d.value,
                [`domain-${d.value}`]: form.domain === d.value,
              }"
              @click="form.domain = d.value"
            >
              {{ d.label }}
            </button>
          </div>
        </div>

        <!-- 标签 -->
        <div class="form-group">
          <label for="note-tags">标签 <span class="hint">逗号分隔</span></label>
          <input
            id="note-tags"
            v-model="form.tagsInput"
            type="text"
            placeholder="如：项目周会, OKR"
            class="tags-input"
          />
        </div>

        <!-- 操作 -->
        <div class="form-actions">
          <button type="button" class="action-btn ghost" @click="goBack">取消</button>
          <button type="submit" class="action-btn primary" :disabled="saving || !canSave">
            {{ saving ? '保存中…' : isNew ? '✓ 创建' : '✓ 保存' }}
          </button>
        </div>
      </form>

      <VoiceRecorderWidget @transcribed="onTranscribed" />
    </AppLayout>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import VoiceRecorderWidget from './VoiceRecorderWidget.vue'
import * as notesStore from './notes-store'
import type { LocalNote } from './notes-store'

const route = useRoute()
const router = useRouter()

const DOMAINS = [
  { value: 'work', label: '工作' },
  { value: 'study', label: '学习' },
  { value: 'life', label: '生活' },
  { value: 'idea', label: '想法' },
] as const

type Domain = (typeof DOMAINS)[number]['value']

const loading = ref(true)
const saving = ref(false)
const titleInput = ref<HTMLInputElement | null>(null)

interface FormState {
  title: string
  content: string
  domain: Domain
  tagsInput: string
  audioPath?: string | null
  audioDurationMs?: number
}

const form = reactive<FormState>({
  title: '',
  content: '',
  domain: 'work',
  tagsInput: '',
  audioPath: null,
  audioDurationMs: 0,
})

const routeId = computed(() => (route.params.id as string) ?? '')
const isNew = computed(
  () => route.name === 'note-new' || routeId.value === 'new' || routeId.value === '',
)

const canSave = computed(() => form.content.trim().length > 0)

function parseTagsInput(raw: string): string[] {
  return raw
    .split(/[,，]/)
    .map((t) => t.trim())
    .filter(Boolean)
}

function tagsFromArray(tags: string[] | null | undefined): string {
  return tags && tags.length ? tags.join(', ') : ''
}

onMounted(async () => {
  if (isNew.value) {
    loading.value = false
    await focusTitle()
    return
  }

  // 编辑模式：拉已有笔记数据
  loading.value = true
  try {
    const existing = await notesStore.getNote(routeId.value)
    if (existing) hydrate(existing)
  } finally {
    loading.value = false
    await focusTitle()
  }
})

function hydrate(n: LocalNote) {
  form.title = n.title || ''
  form.content = n.content || ''
  form.domain = (n.domain as Domain) || 'work'
  form.tagsInput = tagsFromArray(n.tags)
  form.audioPath = n.audioPath
  form.audioDurationMs = n.audioDurationMs
}

async function focusTitle() {
  await nextTick()
  titleInput.value?.focus()
}

function onTranscribed(result: { text: string; audioPath: string; durationSec: number }) {
  // 把转写文本追加到 content 末尾，用两个换行分隔
  if (form.content.trim()) {
    form.content = `${form.content.trim()}\n\n${result.text}`
  } else {
    form.content = result.text
  }
  form.audioPath = result.audioPath
  form.audioDurationMs = Math.round(result.durationSec * 1000)
}

async function onSave() {
  if (!canSave.value || saving.value) return
  saving.value = true

  const payload = {
    title: form.title.trim() || undefined,
    content: form.content.trim(),
    domain: form.domain,
    tags: parseTagsInput(form.tagsInput),
    audioPath: form.audioPath ?? undefined,
    audioDurationMs: form.audioDurationMs,
  }

  try {
    let savedId: string
    if (isNew.value) {
      const created = await notesStore.createNote(payload)
      savedId = created.id
    } else {
      await notesStore.updateNote(routeId.value, {
        title: form.title.trim() || null,
        content: form.content.trim(),
        domain: form.domain,
        tags: parseTagsInput(form.tagsInput),
      })
      savedId = routeId.value
    }
    router.push(`/notes/${savedId}`)
  } catch (e) {
    console.warn('[note] 保存失败:', e)
    saving.value = false
  }
}

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/notes')
}
</script>

<style scoped>
.note-edit-view { min-height: 100vh; background: var(--bg-base); }
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }

.edit-form { display: flex; flex-direction: column; gap: var(--space-4); padding-bottom: 120px; }

.form-group { display: flex; flex-direction: column; gap: var(--space-2); }
.form-group label {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-secondary);
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
}
.form-group .hint { font-size: 11px; font-weight: 400; color: var(--text-muted); }

.title-input,
.tags-input {
  width: 100%;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: 15px;
  box-sizing: border-box;
  outline: none;
}
.title-input:focus,
.tags-input:focus,
.content-input:focus {
  border-color: var(--brand-primary);
}
.tags-input { font-size: 13px; }

.content-input {
  width: 100%;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: 15px;
  line-height: 1.6;
  resize: vertical;
  font-family: inherit;
  box-sizing: border-box;
  outline: none;
}

.domain-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.chip {
  padding: var(--space-2) var(--space-4);
  border-radius: var(--radius-full);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-secondary);
  font-size: 13px;
  cursor: pointer;
  transition: all 0.15s;
}
.chip:active { transform: scale(0.97); }
.chip.active { color: var(--text-inverse); border-color: transparent; }
.chip.domain-work.active { background: var(--cat-work); }
.chip.domain-study.active { background: var(--cat-study); }
.chip.domain-life.active { background: var(--cat-life); }
.chip.domain-idea.active { background: var(--cat-idea); }

.form-actions {
  display: flex;
  gap: var(--space-3);
  padding-top: var(--space-2);
}
.action-btn {
  flex: 1;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--bg-card);
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  color: var(--text-primary);
}
.action-btn:active { opacity: 0.7; }
.action-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.action-btn.primary {
  background: var(--brand-gradient);
  color: var(--text-inverse);
  border: none;
}
.action-btn.ghost { background: var(--bg-subtle); }
</style>
