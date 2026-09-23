<template>
  <section class="page">
    <header class="head">
      <button type="button" class="back-btn" aria-label="back" @click="goBack">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1>{{ t('flashcards.edit.title') }}</h1>
      <button type="button" class="save-link" :disabled="saving || !isValid" @click="save">
        {{ saving ? '…' : t('flashcards.edit.save') }}
      </button>
    </header>

    <!-- Phase 3：模板切换（Basic / Cloze）。 -->
    <div class="template-tabs" role="tablist" :aria-label="t('flashcards.edit.template')">
      <button
        v-for="opt in templateOptions"
        :key="opt.value"
        type="button"
        role="tab"
        class="tab"
        :class="{ active: template === opt.value }"
        :aria-selected="template === opt.value"
        @click="setTemplate(opt.value)"
      >
        <span class="material-symbols-outlined" aria-hidden="true">{{ opt.icon }}</span>
        <span>{{ opt.label }}</span>
      </button>
    </div>

    <form class="form" @submit.prevent="save">
      <!-- Basic 模板：front + back 双 textarea。 -->
      <template v-if="template === 'basic' || template === 'basic_reversed'">
        <label>
          {{ t('flashcards.edit.front') }} *
          <textarea v-model="front" rows="4" required :placeholder="t('flashcards.edit.front')" />
          <!-- Phase 6：front 图片挂载。 -->
          <div v-if="frontMedia.length > 0" class="media-strip">
            <span
              v-for="m in frontMedia"
              :key="m.fileName"
              class="media-thumb"
              :data-filename="m.fileName"
            >
                <img :src="mediaDataUrls[m.fileName] || ''" :alt="m.fileName" />
                <button type="button" class="thumb-x" :aria-label="`remove ${m.fileName}`" @click="removeMedia(m)">
                  <span class="material-symbols-outlined">close</span>
                </button>
              </span>
            <button type="button" class="media-add" @click="pickImage('front', 'camera')">
              <span class="material-symbols-outlined">photo_camera</span>
              <span>{{ t('flashcards.edit.addImageCamera') }}</span>
            </button>
            <button type="button" class="media-add" @click="pickImage('front', 'gallery')">
              <span class="material-symbols-outlined">photo_library</span>
              <span>{{ t('flashcards.edit.addImageGallery') }}</span>
            </button>
          </div>
          <button
            v-else
            type="button"
            class="media-launch"
            @click="showMediaPicker = { role: 'front', open: true }"
          >
            <span class="material-symbols-outlined">image</span>
            <span>{{ t('flashcards.edit.addImage') }}</span>
          </button>
        </label>
        <label>
          {{ t('flashcards.edit.back') }} *
          <textarea v-model="back" rows="6" required :placeholder="t('flashcards.edit.back')" />
          <div v-if="backMedia.length > 0" class="media-strip">
            <span
              v-for="m in backMedia"
              :key="m.fileName"
              class="media-thumb"
              :data-filename="m.fileName"
            >
              <img :src="mediaDataUrls[m.fileName] || ''" :alt="m.fileName" />
              <button type="button" class="thumb-x" :aria-label="`remove ${m.fileName}`" @click="removeMedia(m)">
                <span class="material-symbols-outlined">close</span>
              </button>
            </span>
            <button type="button" class="media-add" @click="pickImage('back', 'camera')">
              <span class="material-symbols-outlined">photo_camera</span>
            </button>
            <button type="button" class="media-add" @click="pickImage('back', 'gallery')">
              <span class="material-symbols-outlined">photo_library</span>
            </button>
          </div>
          <button
            v-else
            type="button"
            class="media-launch"
            @click="showMediaPicker = { role: 'back', open: true }"
          >
            <span class="material-symbols-outlined">image</span>
            <span>{{ t('flashcards.edit.addImage') }}</span>
          </button>
        </label>
      </template>

      <!-- Cloze 模板：单 textarea（front/back 都写同一段 cloze 文本）。 -->
      <template v-else>
        <label>
          {{ t('flashcards.edit.clozeText') }} *
          <textarea
            v-model="clozeText"
            rows="8"
            required
            :placeholder="t('flashcards.edit.clozePlaceholder')"
          />
        </label>
        <p class="hint">
          <span class="material-symbols-outlined" aria-hidden="true">info</span>
          <span>{{ t('flashcards.edit.clozeHint') }}</span>
        </p>
        <p v-if="clozeCount > 0" class="count" data-testid="cloze-count">
          {{ t('flashcards.edit.clozeCount', { count: clozeCount }) }}
        </p>
      </template>

      <label>
        {{ t('flashcards.edit.tags') }}
        <TagInput v-model="tagsModel" />
      </label>

      <label>
        {{ t('flashcards.deck.title') }}
        <select v-model="selectedDeckId">
          <option v-for="deck in deckConfigs" :key="deck.deckId" :value="deck.deckId">
            {{ deck.name }}
          </option>
        </select>
      </label>

      <!-- Phase 4：父牌组选择（嵌套牌组树，最深 3 层）。 -->
      <ParentDeckSelect
        v-if="deckConfigs.length > 1"
        v-model="parentDeckId"
        :exclude-id="noteId || undefined"
        :decks="deckConfigs"
      />

      <p v-if="error" class="error" role="alert">{{ error }}</p>

      <div class="actions">
        <button type="button" @click="goBack">{{ cancelLabel }}</button>
        <button v-if="isEdit" type="button" class="danger" @click="confirmDelete">
          {{ t('flashcards.edit.delete') }}
        </button>
        <button class="primary" type="submit" :disabled="saving || !isValid">
          {{ t('flashcards.edit.save') }}
        </button>
      </div>
    </form>
  </section>
</template>

<script setup lang="ts">
/**
 * FlashcardEditView — 卡片新建 / 编辑（契约 §2 POST/PATCH /api/flashcards/notes）。
 *
 * 路由：
 *   - /flashcards/new（query ?deckId= 可选默认 deck）
 *   - /flashcards/notes/:noteId/edit（修改已有 note 的 front/back/tags）
 *
 * Phase 3 增：Basic / Cloze 模板切换。
 *   - Basic: front + back 双 textarea。
 *   - Cloze: 单 textarea（Cloze 语法：{{c1::answer}} / {{c1::answer::hint}}）。
 *     保存时 front/back/clozeText 三字段同值冗余写入（前后端协议保留）。
 *
 * Save：
 *   - 新建 → store.enqueueCreateNote(input)。
 *   - 编辑 → store.enqueuePatchNote(noteId, { front, back, tags, template, clozeText })。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useFlashcardsStore } from '../../stores/flashcards'
import { parseCloze } from './utils/cloze'
import { pickAndSaveImage, loadMediaDataUrl, deleteMediaFile, type MediaRef } from './utils/flashcardMedia'
import TagInput from './components/TagInput.vue'
import ParentDeckSelect from './components/ParentDeckSelect.vue'
import type { FlashcardTemplate } from '../../types/flashcards'

defineOptions({ name: 'FlashcardEditView' })

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useFlashcardsStore()

const noteId = computed(() => (route.params.noteId ? String(route.params.noteId) : ''))
const isEdit = computed(() => Boolean(noteId.value))

const front = ref('')
const back = ref('')
const clozeText = ref('')
const template = ref<FlashcardTemplate>('basic')
const tagsModel = ref<string[]>([])
const selectedDeckId = ref('')
/* Phase 4：父牌组（仅在编辑已有 note 时记录，新建时 parent 由选 deck 决定）。 */
const parentDeckId = ref<string | null>(null)
/* Phase 6：媒体引用 + data URL 缓存（按 fileName 索引）。 */
const mediaRefs = ref<MediaRef[]>([])
const mediaDataUrls = ref<Record<string, string>>({})
const showMediaPicker = ref<{ role: MediaRef['role']; open: boolean } | null>(null)
const saving = ref(false)
const error = ref('')

const deckConfigs = computed(() => store.deckConfigs)

const frontMedia = computed(() => mediaRefs.value.filter((m) => m.role === 'front'))
const backMedia = computed(() => mediaRefs.value.filter((m) => m.role === 'back'))

const templateOptions = computed(() => [
  { value: 'basic' as const, icon: 'compare_arrows', label: t('flashcards.edit.templateBasic') },
  { value: 'cloze' as const, icon: 'auto_awesome_motion', label: t('flashcards.edit.templateCloze') },
])

/* Cloze 解析结果（仅 cloze 模板用得到）：给编辑者视觉反馈「几处挖空」。 */
const parsedCloze = computed(() => parseCloze(clozeText.value))
const clozeCount = computed(() => parsedCloze.value.clozeCount)

const isValid = computed(() => {
  if (!selectedDeckId.value) return false
  if (template.value === 'cloze') {
    return clozeText.value.trim().length > 0 && clozeCount.value > 0
  }
  return front.value.trim().length > 0 && back.value.trim().length > 0
})

const cancelLabel = computed(() => {
  // locales 没提供 edit.cancel，复用 deck.title 兜底
  return t('flashcards.deck.title')
})

function setTemplate(next: FlashcardTemplate) {
  if (template.value === next) return
  // 切换到 cloze 时若 front 是 cloze 语法，自动搬过去（编辑模式无 clozeText 字段时）
  if (next === 'cloze' && !clozeText.value && front.value) {
    clozeText.value = front.value
  }
  // 从 cloze 退回 basic 时若 front 为空，把 cloze 文本塞回 front 让用户不丢内容
  if (template.value === 'cloze' && next !== 'cloze' && !front.value && clozeText.value) {
    front.value = clozeText.value
  }
  template.value = next
}

function goBack() {
  if (window.history.length > 1 && window.history.state?.back) router.back()
  else router.push('/flashcards')
}

function hydrate(noteIdVal: string) {
  const note = store.notes.find((n) => n.id === noteIdVal)
  if (!note) {
    error.value = 'note not found'
    return
  }
  template.value = note.template ?? 'basic'
  front.value = note.front
  back.value = note.back
  clozeText.value = note.clozeText ?? note.front
  tagsModel.value = [...(note.tags ?? [])]
  selectedDeckId.value = note.deckId
  mediaRefs.value = [...(note.mediaRefs ?? [])]
  void hydrateMediaUrls()
}

async function hydrateMediaUrls() {
  for (const m of mediaRefs.value) {
    if (mediaDataUrls.value[m.fileName]) continue
    const url = await loadMediaDataUrl(m.fileName).catch(() => null)
    if (url) mediaDataUrls.value[m.fileName] = url
  }
}

async function pickImage(role: MediaRef['role'], source: 'camera' | 'gallery') {
  error.value = ''
  try {
    const ref = await pickAndSaveImage({ role, source })
    mediaRefs.value = [...mediaRefs.value, ref]
    const url = await loadMediaDataUrl(ref.fileName).catch(() => null)
    if (url) mediaDataUrls.value = { ...mediaDataUrls.value, [ref.fileName]: url }
  } catch (e: any) {
    error.value = e?.message ?? t('flashcards.edit.imagePickFailed')
  }
}

async function removeMedia(ref: MediaRef) {
  mediaRefs.value = mediaRefs.value.filter((m) => m.fileName !== ref.fileName)
  delete mediaDataUrls.value[ref.fileName]
  void deleteMediaFile(ref.fileName).catch(() => {})
}

async function save() {
  if (!isValid.value) return
  saving.value = true
  error.value = ''
  try {
    const tags = tagsModel.value
    const isCloze = template.value === 'cloze'
    const effectiveText = isCloze ? clozeText.value.trim() : front.value.trim()
    const input = {
      deckId: selectedDeckId.value,
      front: isCloze ? effectiveText : front.value.trim(),
      back: isCloze ? effectiveText : back.value.trim(),
      tags,
      template: template.value,
      clozeText: isCloze ? effectiveText : undefined,
      mediaRefs: [...mediaRefs.value],
    }
    if (isEdit.value) {
      store.enqueuePatchNote(noteId.value, input)
      // 父牌组变更只影响 deck config（card 的 deckId 不变 → 树形归位靠 store 后续 patchCard）。
      // 这里先不入 outbox；Phase 4.1 再扩展 deck config 的 outbox 通道。
    } else {
      store.enqueueCreateNote(input)
    }
    void store.flushOutbox().catch(() => {})
    goBack()
  } catch (e: any) {
    error.value = e?.message || 'save failed'
  } finally {
    saving.value = false
  }
}

function confirmDelete() {
  if (!isEdit.value) return
  const ok = typeof window !== 'undefined' && window.confirm(t('flashcards.edit.confirmDelete'))
  if (!ok) return
  store.enqueueDeleteNote(noteId.value)
  void store.flushOutbox().catch(() => {})
  goBack()
}

watch(noteId, (val) => {
  if (val) hydrate(val)
})

onMounted(() => {
  store.loadFromCache()
  if (store.deckConfigs.length === 0) void store.refresh().catch(() => {})
  if (!selectedDeckId.value) {
    const qDeck = route.query.deckId ? String(route.query.deckId) : ''
    selectedDeckId.value = qDeck || store.deckConfigs[0]?.deckId || ''
  }
  if (isEdit.value) hydrate(noteId.value)
})
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); display: flex; flex-direction: column; }
.head {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-4);
}
.head h1 { flex: 1; margin: 0; font-size: 18px; color: var(--text-primary); }
.back-btn, .save-link {
  border: 0;
  background: transparent;
  color: var(--text-primary);
  padding: 6px;
  cursor: pointer;
}
.save-link { color: var(--brand-primary); font-weight: 600; }

/* 模板切换 tab（M3 segmented button 风格） */
.template-tabs {
  display: flex;
  gap: var(--space-1);
  margin: 0 var(--space-4) var(--space-2);
  padding: 4px;
  background: var(--bg-subtle);
  border-radius: var(--radius-full);
  width: fit-content;
}

.tab {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-3);
  background: transparent;
  border: none;
  border-radius: var(--radius-full);
  color: var(--text-secondary);
  font-size: 13px;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  min-height: 32px;
}

.tab .material-symbols-outlined {
  font-size: 16px;
}

.tab.active {
  background: var(--bg-card);
  color: var(--brand-primary);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
}

.form { display: flex; flex-direction: column; gap: var(--space-4); padding: var(--space-3) var(--space-4) 100px; }
label { display: flex; flex-direction: column; gap: 6px; font-size: 13px; font-weight: 600; color: var(--text-secondary); }
input, textarea, select {
  width: 100%;
  box-sizing: border-box;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  color: var(--text-primary);
  font: inherit;
  font-size: 14px;
}
textarea { resize: vertical; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; }

.hint {
  display: flex;
  align-items: flex-start;
  gap: var(--space-1);
  margin: -4px 0 0;
  padding: var(--space-2) var(--space-3);
  background: var(--brand-bg, rgba(76, 141, 255, 0.06));
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.4;
}

.hint .material-symbols-outlined {
  font-size: 16px;
  color: var(--brand-primary);
  flex-shrink: 0;
  margin-top: 1px;
}

.count {
  margin: -4px 0 0;
  padding: 0 var(--space-1);
  color: var(--brand-primary);
  font-size: 12px;
  font-weight: var(--font-weight-semibold);
}

.actions { display: flex; gap: var(--space-3); }
.actions button {
  flex: 1;
  padding: 10px 0;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  color: var(--text-primary);
  font: inherit;
  font-size: 14px;
  cursor: pointer;
}
.actions .primary { background: var(--brand-gradient); border: 0; color: var(--text-inverse); }
.actions .danger { color: var(--danger); border-color: var(--danger); }
.actions button:disabled { opacity: 0.5; cursor: not-allowed; }
.error { margin: 0; padding: var(--space-3); color: var(--danger); background: var(--danger-bg); border-radius: var(--radius-sm); font-size: 13px; }

/* Phase 6：媒体挂载 */
.media-launch {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 6px 10px;
  background: transparent;
  border: 1px dashed var(--border);
  border-radius: var(--radius-sm);
  color: var(--brand-primary);
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  align-self: flex-start;
  min-height: 32px;
}

.media-launch .material-symbols-outlined { font-size: 16px; }

.media-strip {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-1);
}

.media-thumb {
  position: relative;
  display: inline-block;
  width: 64px;
  height: 64px;
  border-radius: var(--radius-sm);
  overflow: hidden;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
}

.media-thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.thumb-x {
  position: absolute;
  top: 2px;
  right: 2px;
  width: 20px;
  height: 20px;
  border: 0;
  border-radius: 50%;
  background: rgba(0, 0, 0, 0.6);
  color: #fff;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.thumb-x .material-symbols-outlined { font-size: 14px; }

.media-add {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 64px;
  height: 64px;
  border: 1px dashed var(--border);
  background: transparent;
  color: var(--text-tertiary);
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.media-add .material-symbols-outlined { font-size: 20px; }
.media-add:hover { color: var(--brand-primary); border-color: var(--brand-primary); }
</style>