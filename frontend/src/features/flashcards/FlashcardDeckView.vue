<template>
  <FoldAwareLayout>
    <template #outer>
      <section class="page outer">
        <header class="head">
          <h1>{{ deckName }}</h1>
          <span class="badge" :class="{ empty: dueCount === 0 }">
            {{ t('flashcards.list.dueToday', { count: dueCount }) }}
          </span>
        </header>
        <button class="review" type="button" :disabled="dueCount === 0" @click="startReview">
          {{ t('flashcards.deck.review') }}
        </button>
      </section>
    </template>
    <template #inner>
      <section class="page inner">
        <header class="head">
          <button type="button" class="back-btn" aria-label="back" @click="goBack">
            <span class="material-symbols-outlined">arrow_back</span>
          </button>
          <h1>{{ deckName }}</h1>
          <button type="button" class="add-btn" :aria-label="t('flashcards.deck.addCard')" @click="goCreate">
            <span class="material-symbols-outlined">add</span>
          </button>
        </header>

        <section class="summary">
          <div class="metric">
            <span class="metric-num">{{ totalCards }}</span>
            <span class="metric-label">{{ t('flashcards.deck.title') }}</span>
          </div>
          <div class="metric">
            <span class="metric-num">{{ dueCount }}</span>
            <span class="metric-label">{{ t('flashcards.list.dueToday', { count: dueCount }) }}</span>
          </div>
          <div class="metric">
            <span class="metric-num">{{ learningCount }}</span>
            <span class="metric-label">{{ t('flashcards.review.title') }}</span>
          </div>
        </section>

        <div class="actions">
          <button class="primary review-btn" type="button" :disabled="dueCount === 0" @click="startReview">
            {{ t('flashcards.deck.review') }}
          </button>
          <button class="secondary" type="button" @click="goCreate">
            {{ t('flashcards.deck.addCard') }}
          </button>
        </div>

        <section v-if="cardRows.length === 0" class="empty">
          {{ t('flashcards.deck.noCards') }}
        </section>
        <section v-else class="cards-list" data-testid="deck-card-list">
          <article
            v-for="row in cardRows"
            :key="row.cardId"
            class="card"
            role="button"
            tabindex="0"
            @click="editRow(row.noteId)"
            @keyup.enter="editRow(row.noteId)"
          >
            <p class="front">{{ row.front }}</p>
            <p class="meta">
              <span>{{ row.lastReviewed }}</span>
              <span :class="['state', `state-${row.state}`]">{{ row.stateLabel }}</span>
            </p>
          </article>
        </section>
      </section>
    </template>
  </FoldAwareLayout>
</template>

<script setup lang="ts">
/**
 * FlashcardDeckView — 单个 deck 的详情页（卡片列表 + 复习入口）。
 *
 * 路由：`/flashcards/decks/:deckId`
 * 数据：store.dueCardsForDeck(deckId) → 按 due 排序的 cards
 *       store.cardsByNote(noteId) → 反查 note 的 front 文本
 */
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import FoldAwareLayout from './components/FoldAwareLayout.vue'
import { useFlashcardsStore } from '../../stores/flashcards'

defineOptions({ name: 'FlashcardDeckView' })

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useFlashcardsStore()

const deckId = computed(() => String(route.params.deckId || ''))

const deckConfig = computed(() => store.deckById(deckId.value))
const deckName = computed(() => deckConfig.value?.name ?? (deckId.value || t('flashcards.deck.title')))

const cardsInDeck = computed(() => store.dueCardsForDeck(deckId.value))
const totalCards = computed(
  () => store.cards.filter((c) => c.deckId === deckId.value && !(c.deletedAt && c.deletedAt > 0)).length,
)
const dueCount = computed(() => store.dueByDeck.get(deckId.value) ?? 0)
const learningCount = computed(
  () =>
    cardsInDeck.value.filter((c) => c.state === 1 || c.state === 3).length,
)

interface CardRow {
  cardId: string
  noteId: string
  front: string
  lastReviewed: string
  state: number
  stateLabel: string
}

const cardRows = computed<CardRow[]>(() => {
  const stateLabels = ['New', 'Learning', 'Review', 'Relearning']
  return cardsInDeck.value.map((card) => {
    const note = store.notes.find((n) => n.id === card.noteId)
    return {
      cardId: card.id,
      noteId: card.noteId,
      front: note?.front ?? '—',
      lastReviewed:
        card.lastReviewAt > 0 ? new Date(card.lastReviewAt * 1000).toLocaleString() : '—',
      state: card.state,
      stateLabel: stateLabels[card.state] ?? 'New',
    }
  })
})

function startReview() {
  router.push(`/flashcards/decks/${encodeURIComponent(deckId.value)}/review`)
}

function goCreate() {
  router.push({ path: '/flashcards/new', query: { deckId: deckId.value } })
}

function editRow(noteId: string) {
  router.push(`/flashcards/notes/${encodeURIComponent(noteId)}/edit`)
}

function goBack() {
  if (window.history.length > 1 && window.history.state?.back) router.back()
  else router.push('/flashcards')
}

onMounted(() => {
  store.loadFromCache()
  if (store.cards.length === 0) void store.refresh().catch(() => {})
})
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); display: flex; flex-direction: column; }
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4);
  gap: var(--space-3);
}
.head h1 { flex: 1; margin: 0; font-size: 18px; color: var(--text-primary); }
.back-btn, .add-btn {
  border: 0;
  background: transparent;
  color: var(--text-primary);
  padding: 6px;
  border-radius: 999px;
  cursor: pointer;
}
.add-btn { color: var(--brand-primary); }

.summary {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: var(--space-3);
  padding: 0 var(--space-4);
}
.metric {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  text-align: center;
}
.metric-num { display: block; font-size: 22px; font-weight: 600; color: var(--text-primary); }
.metric-label { font-size: 11px; color: var(--text-secondary); }

.actions { display: flex; gap: var(--space-3); padding: var(--space-4); }
.actions button {
  flex: 1;
  padding: 11px 0;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
  cursor: pointer;
  font: inherit;
  font-size: 14px;
}
.actions .primary { background: var(--brand-gradient); color: var(--text-inverse); border: 0; }
.actions button:disabled { opacity: 0.5; cursor: not-allowed; }

.cards-list { display: flex; flex-direction: column; gap: var(--space-2); padding: 0 var(--space-4) 100px; }
.card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 6px;
  color: inherit;
  font: inherit;
}
.card .front { margin: 0; font-size: 14px; color: var(--text-primary); }
.card .meta {
  margin: 0;
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  color: var(--text-secondary);
}
.state { padding: 2px 8px; border-radius: 999px; }
.state-0 { background: var(--bg-subtle); color: var(--text-secondary); }
.state-1 { background: var(--warning-bg, rgba(245, 158, 11, 0.15)); color: var(--warning, #f59e0b); }
.state-2 { background: var(--success-bg); color: var(--success); }
.state-3 { background: var(--danger-bg); color: var(--danger); }

.empty { padding: 60px var(--space-4); text-align: center; color: var(--text-secondary); }

.outer .head h1 { font-size: 15px; }
.outer { padding: 0 var(--space-3); }
.review {
  margin: var(--space-4) var(--space-3) 0;
  padding: 12px 0;
  border: 0;
  border-radius: var(--radius-sm);
  background: var(--brand-gradient);
  color: var(--text-inverse);
  cursor: pointer;
  font: inherit;
  font-size: 14px;
}
.review:disabled { opacity: 0.5; cursor: not-allowed; }
.badge {
  font-size: 11px;
  padding: 3px 9px;
  border-radius: 999px;
  background: var(--brand-primary);
  color: var(--text-inverse);
}
.badge.empty { background: var(--bg-subtle); color: var(--text-secondary); }
</style>