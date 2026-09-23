<template>
  <section class="page">
    <header class="head">
      <button type="button" class="back-btn" aria-label="back" @click="goBack">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1>{{ t('flashcards.deckOptions.title') }}</h1>
      <button type="button" class="save-link" :disabled="!hasChanges" @click="save">
        {{ t('flashcards.edit.save') }}
      </button>
    </header>

    <div v-if="!deck" class="empty">
      <p>{{ t('flashcards.deckOptions.notFound') }}</p>
    </div>

    <form v-else class="form" @submit.prevent="save">
      <label>
        {{ t('flashcards.deckOptions.deckName') }} *
        <input v-model="form.name" required />
      </label>

      <fieldset class="group">
        <legend>{{ t('flashcards.deckOptions.groupDaily') }}</legend>
        <label>
          {{ t('flashcards.deckOptions.newPerDay') }}
          <input v-model.number="form.newPerDay" type="number" min="0" max="9999" />
        </label>
        <label>
          {{ t('flashcards.deckOptions.reviewsPerDay') }}
          <input v-model.number="form.reviewsPerDay" type="number" min="0" max="9999" />
        </label>
      </fieldset>

      <fieldset class="group">
        <legend>{{ t('flashcards.deckOptions.groupLearning') }}</legend>
        <label>
          {{ t('flashcards.deckOptions.learningSteps') }}
          <input v-model="learningStepsInput" :placeholder="t('flashcards.deckOptions.learningStepsHint')" />
        </label>
        <label>
          {{ t('flashcards.deckOptions.graduatingInterval') }}
          <input v-model.number="form.graduatingIntervalDays" type="number" min="1" max="365" />
        </label>
        <label>
          {{ t('flashcards.deckOptions.easyInterval') }}
          <input v-model.number="form.easyIntervalDays" type="number" min="1" max="365" />
        </label>
      </fieldset>

      <fieldset class="group">
        <legend>{{ t('flashcards.deckOptions.groupFsrs') }}</legend>
        <label>
          {{ t('flashcards.deckOptions.desiredRetention') }}
          <input v-model.number="form.desiredRetention" type="number" min="0.5" max="0.99" step="0.01" />
        </label>
        <label>
          {{ t('flashcards.deckOptions.maximumInterval') }}
          <input v-model.number="form.maximumIntervalDays" type="number" min="30" max="36500" />
        </label>
        <label>
          {{ t('flashcards.deckOptions.easyBonus') }}
          <input v-model.number="form.easyBonus" type="number" min="1.0" max="3.0" step="0.05" />
        </label>
        <label>
          {{ t('flashcards.deckOptions.hardInterval') }}
          <input v-model.number="form.hardInterval" type="number" min="0.5" max="2.0" step="0.05" />
        </label>
      </fieldset>

      <p v-if="error" class="error" role="alert">{{ error }}</p>
    </form>
  </section>
</template>

<script setup lang="ts">
/**
 * DeckOptionsView —— 牌组配置（Anki deck options 对齐）。
 *
 * 路由：`/flashcards/decks/:deckId/options`
 *
 * 范围（Phase 4）：
 *   - daily limits（newPerDay / reviewsPerDay）
 *   - learning steps + graduating / easy intervals
 *   - FSRS 参数（desiredRetention / maximumIntervalDays / easyBonus / hardInterval）
 *
 * 暂未入 outbox：直接 store.saveDeckConfig()（前端 Pinia 落 localStorage）。
 * Phase 4.1 增量：走 outbox + 后端 PATCH /decks/:id/config 通道。
 */
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useFlashcardsStore } from '../../stores/flashcards'

defineOptions({ name: 'DeckOptionsView' })

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useFlashcardsStore()

const deckId = computed(() => String(route.params.deckId || ''))
const deck = computed(() => store.deckById(deckId.value))

const error = ref('')

/* 表单状态（与 deck config 解耦；用户改但不保存就 goBack 时不污染 store）。 */
const form = reactive({
  name: '',
  newPerDay: 20,
  reviewsPerDay: 200,
  learningStepsMin: [1, 10] as number[],
  graduatingIntervalDays: 1,
  easyIntervalDays: 4,
  desiredRetention: 0.9,
  maximumIntervalDays: 36500,
  easyBonus: 1.3,
  hardInterval: 1.2,
})

/* learningStepsMin 是数组；用逗号分隔的输入框更友好。 */
const learningStepsInput = computed({
  get: () => form.learningStepsMin.join(', '),
  set: (val: string) => {
    const arr = val
      .split(/[,\s]+/)
      .map((s) => Number(s))
      .filter((n) => Number.isFinite(n) && n > 0)
    form.learningStepsMin = arr.length > 0 ? arr : [1, 10]
  },
})

const hasChanges = ref(false)

watch(
  () => JSON.stringify(form),
  () => {
    hasChanges.value = true
  },
)

function hydrate() {
  const d = deck.value
  if (!d) return
  form.name = d.name
  form.newPerDay = d.newPerDay
  form.reviewsPerDay = d.reviewsPerDay
  form.learningStepsMin = [...(d.learningStepsMin ?? [1, 10])]
  form.graduatingIntervalDays = d.graduatingIntervalDays
  form.easyIntervalDays = d.easyIntervalDays
  form.desiredRetention = d.desiredRetention
  form.maximumIntervalDays = d.maximumIntervalDays ?? 36500
  form.easyBonus = d.easyBonus ?? 1.3
  form.hardInterval = d.hardInterval ?? 1.2
  hasChanges.value = false
}

function save() {
  const d = deck.value
  if (!d) return
  error.value = ''
  if (!form.name.trim()) {
    error.value = t('flashcards.deckOptions.nameRequired')
    return
  }
  store.saveDeckConfig({
    ...d,
    name: form.name.trim(),
    newPerDay: form.newPerDay,
    reviewsPerDay: form.reviewsPerDay,
    learningStepsMin: form.learningStepsMin,
    graduatingIntervalDays: form.graduatingIntervalDays,
    easyIntervalDays: form.easyIntervalDays,
    desiredRetention: form.desiredRetention,
    maximumIntervalDays: form.maximumIntervalDays,
    easyBonus: form.easyBonus,
    hardInterval: form.hardInterval,
  })
  hasChanges.value = false
  router.push(`/flashcards/decks/${encodeURIComponent(deckId.value)}`)
}

function goBack() {
  if (window.history.length > 1 && window.history.state?.back) router.back()
  else router.push(`/flashcards/decks/${encodeURIComponent(deckId.value)}`)
}

onMounted(() => {
  store.loadFromCache()
  if (store.deckConfigs.length === 0) void store.refresh().catch(() => {})
  hydrate()
})

watch(deckId, hydrate)
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
.back-btn {
  border: 0;
  background: transparent;
  color: var(--text-primary);
  padding: 6px;
  cursor: pointer;
}
.save-link {
  border: 0;
  background: transparent;
  color: var(--brand-primary);
  font-weight: 600;
  padding: 6px;
  cursor: pointer;
}
.save-link:disabled { color: var(--text-tertiary); cursor: not-allowed; }

.form { display: flex; flex-direction: column; gap: var(--space-3); padding: var(--space-3) var(--space-4) 100px; }

label { display: flex; flex-direction: column; gap: 6px; font-size: 13px; font-weight: 600; color: var(--text-secondary); }

input {
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

.group {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
}

.group legend {
  padding: 0 var(--space-1);
  font-size: 12px;
  font-weight: var(--font-weight-semibold);
  color: var(--text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.4px;
}

.error { margin: 0; padding: var(--space-3); color: var(--danger); background: var(--danger-bg); border-radius: var(--radius-sm); font-size: 13px; }

.empty { padding: var(--space-5); text-align: center; color: var(--text-secondary); }
</style>