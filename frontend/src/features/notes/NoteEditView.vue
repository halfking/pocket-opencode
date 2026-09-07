<template>
  <div class="note-edit-view">
      <div v-if="loading" class="state" role="status">加载中…</div>

      <ErrorState
        v-else-if="loadError"
        title="笔记加载失败"
        :message="loadError"
        @retry="load"
      />

      <form v-else class="edit-form" @submit.prevent="onSave">
        <div class="form-group">
          <label for="note-title">标题</label>
          <UnifiedComposer
            v-model="form.title"
            single-line
            placeholder="一句话概括…"
            :enable="{ voice: true, image: false, camera: false, file: false, agent: false, optimize: true }"
            :allow-fullscreen="false"
            submit-label="保存"
            @submit="onSave"
          />
        </div>

        <div class="form-group">
          <label for="note-content">
            正文
            <span class="hint">支持 Markdown · 可全屏编辑 · 语音录入自动填入</span>
          </label>
          <UnifiedComposer
            ref="composerRef"
            v-model="form.content"
            placeholder="点击 ⛶ 全屏编辑，🎙 语音录入，或直接输入文本…"
            :submit-on-enter="false"
            :enable="{ voice: true, image: true, camera: true, file: true, agent: false, optimize: true }"
            submit-label="保存"
            :submitting="saving"
            @submit="onSave"
          />
        </div>

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

        <div class="form-group">
          <label for="note-tags">标签 <span class="hint">逗号分隔</span></label>
          <div class="tag-row">
            <input
              id="note-tags"
              v-model="form.tagsInput"
              type="text"
              placeholder="如：项目周会, OKR"
              class="tags-input"
            />
            <button type="button" class="extract-btn" :disabled="extracting" @click="onExtractTags">
              {{ extracting ? '提取中…' : '一键提取' }}
            </button>
          </div>
          <input ref="videoInput" type="file" accept="video/*" class="hidden-file" @change="onPickVideo" />
          <button type="button" class="video-btn" @click="videoInput?.click()">添加视频</button>
          <p v-if="pendingMedia.length" class="media-hint">已选 {{ pendingMedia.length }} 个附件</p>
        </div>

        <div class="form-actions">
          <button type="button" class="action-btn ghost" @click="goBack">取消</button>
          <button type="submit" class="action-btn primary" :disabled="saving || !canSave">
            {{ saving ? '保存中…' : isNew ? '✓ 创建' : '✓ 保存' }}
          </button>
        </div>
        <p v-if="saveError" class="form-error" role="alert">{{ saveError }}</p>
      </form>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import * as notesStore from './notes-store'
import type { LocalNote, NoteMediaInput } from './notes-store'
import { ErrorState, UnifiedComposer } from '../../components'
import { useAuthStore } from '../../stores/auth'
import {
  attachmentsToMedia,
  extractTagsForForm,
  fileToVideoMedia,
  parseTagsInput,
  tagsFromArray,
} from './note-edit-helpers'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const DOMAINS = [
  { value: 'work', label: '工作' },
  { value: 'study', label: '学习' },
  { value: 'life', label: '生活' },
  { value: 'idea', label: '想法' },
] as const

type Domain = (typeof DOMAINS)[number]['value']

const loading = ref(true)
const saving = ref(false)
const saveError = ref('')
const loadError = ref('')
const extracting = ref(false)
const pendingMedia = ref<NoteMediaInput[]>([])
const videoInput = ref<HTMLInputElement | null>(null)
const composerRef = ref<{ attachments?: { value: { dataUrl: string; name: string }[] } } | null>(null)

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

function collectComposerMedia(): NoteMediaInput[] {
  const exposed = composerRef.value?.attachments as unknown
  const raw = Array.isArray(exposed)
    ? exposed
    : (exposed as { value?: { dataUrl: string; name: string }[] } | undefined)?.value ?? []
  return attachmentsToMedia(raw)
}

function onPickVideo(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (file) pendingMedia.value.push(fileToVideoMedia(file))
}

async function onExtractTags() {
  extracting.value = true
  try {
    const extracted = await extractTagsForForm(form.content, form.tagsInput)
    form.tagsInput = extracted.tagsInput
    if (!form.title) form.title = extracted.title
  } finally {
    extracting.value = false
  }
}

onMounted(() => load())

async function load() {
  if (isNew.value) {
    loading.value = false
    return
  }

  // 编辑模式：拉已有笔记数据
  loading.value = true
  loadError.value = ''
  try {
    const existing = await notesStore.getNote(routeId.value, false, currentWorkspaceId())
    if (existing) {
      existing.content = await notesStore.loadFullContent(existing)
      hydrate(existing)
    }
  } catch (e: any) {
    loadError.value = e?.message || '加载笔记失败，请稍后重试。'
  } finally {
    loading.value = false
  }
}

function hydrate(n: LocalNote) {
  form.title = n.title || ''
  form.content = n.content || ''
  form.domain = (n.domain as Domain) || 'work'
  form.tagsInput = tagsFromArray(n.tags)
  form.audioPath = n.audioPath
  form.audioDurationMs = n.audioDurationMs
}

function currentWorkspaceId(): string {
  return auth.workspaceId || 'default'
}

async function onSave() {
  if (!canSave.value || saving.value) return
  saving.value = true
  saveError.value = ''

  const media = [...pendingMedia.value, ...collectComposerMedia()]
  const payload = {
    title: form.title.trim() || undefined,
    content: form.content.trim(),
    domain: form.domain,
    tags: parseTagsInput(form.tagsInput),
    audioPath: form.audioPath ?? undefined,
    audioDurationMs: form.audioDurationMs,
    workspaceId: currentWorkspaceId(),
    createdByVoice: false,
    media,
  }

  try {
    if (isNew.value) {
      await notesStore.createNote(payload)
    } else {
      await notesStore.updateNote(routeId.value, {
        title: form.title.trim() || null,
        content: form.content.trim(),
        domain: form.domain,
        tags: parseTagsInput(form.tagsInput),
        media,
      }, currentWorkspaceId())
    }
    router.replace('/notes')
  } catch (e: any) {
    console.warn('[note] 保存失败:', e)
    saving.value = false
    saveError.value = e?.message || '保存失败，请稍后重试'
  }
}

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/notes')
}
</script>

<style scoped>
.note-edit-view { min-height: 100%; background: var(--bg-base); }
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.edit-form { display: flex; flex-direction: column; gap: var(--space-4); padding-bottom: 120px; }
.form-group { display: flex; flex-direction: column; gap: var(--space-2); }
.form-group label { font-size: 13px; font-weight: 600; color: var(--text-secondary); }
.form-group .hint { font-size: 11px; font-weight: 400; color: var(--text-muted); }
.tags-input {
  flex: 1; padding: var(--space-3); border-radius: var(--radius-md);
  border: 1px solid var(--border); background: var(--bg-card); color: var(--text-primary);
}
.tag-row { display: flex; gap: 8px; }
.extract-btn, .video-btn {
  padding: 8px 12px; border-radius: var(--radius-md);
  border: 1px solid var(--border); background: var(--bg-subtle); font-size: 12px;
}
.hidden-file { display: none; }
.media-hint { margin: 4px 0 0; font-size: 12px; color: var(--text-muted); }
.domain-chips { display: flex; flex-wrap: wrap; gap: var(--space-2); }
.chip {
  padding: var(--space-2) var(--space-4); border-radius: var(--radius-full);
  border: 1px solid var(--border); background: var(--bg-card); color: var(--text-secondary);
}
.chip.active { color: var(--text-inverse); border-color: transparent; }
.chip.domain-work.active { background: var(--cat-work); }
.chip.domain-study.active { background: var(--cat-study); }
.chip.domain-life.active { background: var(--cat-life); }
.chip.domain-idea.active { background: var(--cat-idea); }
.form-actions { display: flex; gap: var(--space-3); }
.action-btn {
  flex: 1; padding: var(--space-3); border-radius: var(--radius-md);
  border: 1px solid var(--border); background: var(--bg-card); font-weight: 600;
}
.action-btn:disabled { opacity: 0.5; }
.action-btn.primary { background: var(--brand-gradient); color: var(--text-inverse); border: none; }
.action-btn.ghost { background: var(--bg-subtle); }
.form-error { margin: 0; padding: var(--space-3); border-radius: var(--radius-md); background: var(--danger-bg); color: var(--danger); }
</style>
