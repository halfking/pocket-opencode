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

    <form class="form" @submit.prevent="save">
      <label>
        {{ t('flashcards.edit.front') }} *
        <textarea v-model="front" rows="4" required :placeholder="t('flashcards.edit.front')" />
      </label>
      <label>
        {{ t('flashcards.edit.back') }} *
        <textarea v-model="back" rows="6" required :placeholder="t('flashcards.edit.back')" />
      </label>
      <label>
        {{ t('flashcards.edit.tags') }}
        <input v-model="tagsInput" :placeholder="t('flashcards.edit.tags')" />
      </label>
      <label>
        {{ t('flashcards.deck.title') }}
        <select v-model="selectedDeckId">
          <option v-for="deck in deckConfigs" :key="deck.deckId" :value="deck.deckId">
            {{ deck.name }}
          </option>
        </select>
      </label>

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
 * Save：
 *   - 新建 → store.enqueueCreateNote(input)（写入 outbox；flushOutbox 调
 *     services.createNote）。成功后 router.back。
 *   - 编辑 → store.enqueuePatchNote(noteId, { front, back, tags })。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useFlashcardsStore } from '../../stores/flashcards'

defineOptions({ name: 'FlashcardEditView' })

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useFlashcardsStore()

const noteId = computed(() => (route.params.noteId ? String(route.params.noteId) : ''))
const isEdit = computed(() => Boolean(noteId.value))

const front = ref('')
const back = ref('')
const tagsInput = ref('')
const selectedDeckId = ref('')
const saving = ref(false)
const error = ref('')

const deckConfigs = computed(() => store.deckConfigs)

const isValid = computed(
  () => front.value.trim().length > 0 && back.value.trim().length > 0 && selectedDeckId.value.length > 0,
)

const cancelLabel = computed(() => {
  // locales 没提供 edit.cancel，复用 deck.review / list.title 兜底（保持 i18n 规范）
  return t('flashcards.deck.title')
})

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
  front.value = note.front
  back.value = note.back
  tagsInput.value = (note.tags ?? []).join(', ')
  selectedDeckId.value = note.deckId
}

async function save() {
  if (!isValid.value) return
  saving.value = true
  error.value = ''
  try {
    const tags = tagsInput.value
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
    const input = {
      deckId: selectedDeckId.value,
      front: front.value.trim(),
      back: back.value.trim(),
      tags,
    }
    if (isEdit.value) {
      store.enqueuePatchNote(noteId.value, input)
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
textarea { resize: vertical; }

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
</style>