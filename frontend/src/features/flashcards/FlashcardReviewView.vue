<template>
  <FoldAwareLayout>
    <template #outer>
      <section class="page outer">
        <header class="head">
          <button type="button" class="back-btn" :aria-label="t('common.back') || 'back'" @click="goBack">
            <span class="material-symbols-outlined">arrow_back</span>
          </button>
          <span class="progress">
            {{ Math.min(index + 1, total) }} / {{ total }}
          </span>
        </header>
        <article class="card-display" :class="{ flipped: isFlipped }" @click="flip">
          <div class="face front">
            <p>{{ currentNote?.front ?? '—' }}</p>
            <small>{{ t('flashcards.edit.front') }}</small>
          </div>
        </article>
        <div class="cta">
          <button v-if="!isFlipped" class="primary" type="button" @click="flip">
            {{ t('flashcards.deck.review') }}
          </button>
          <div v-else class="ratings compact">
            <button type="button" class="rating again" @click="rate(1)">{{ t('flashcards.review.again') }}</button>
            <button type="button" class="rating good" @click="rate(3)">{{ t('flashcards.review.good') }}</button>
          </div>
        </div>
      </section>
    </template>

    <template #inner>
      <section class="page inner">
        <header class="head">
          <button type="button" class="back-btn" aria-label="back" @click="goBack">
            <span class="material-symbols-outlined">arrow_back</span>
          </button>
          <h1>{{ t('flashcards.review.title') }}</h1>
          <span class="progress" data-testid="review-progress">
            {{ Math.min(index + 1, total) }} / {{ total }}
          </span>
        </header>

        <div class="progress-bar" role="progressbar" :aria-valuenow="progressPct">
          <div class="progress-bar-fill" :style="{ width: progressPct + '%' }" />
        </div>

        <VivoBatteryWhitelistGuide />

        <article class="card-display" :class="{ flipped: isFlipped }" @click="flip">
          <div class="face front" v-show="!isFlipped">
            <small>{{ t('flashcards.edit.front') }}</small>
            <p>{{ currentNote?.front ?? '—' }}</p>
          </div>
          <div class="face back" v-show="isFlipped">
            <small>{{ t('flashcards.edit.back') }}</small>
            <p>{{ currentNote?.back ?? '—' }}</p>
          </div>
        </article>

        <p v-if="!isFlipped" class="hint" @click="flip">{{ t('flashcards.deck.review') }}</p>

        <div v-if="isFlipped" class="ratings">
          <button type="button" class="rating again" :disabled="busy" @click="rate(1)">
            <span class="material-symbols-outlined">refresh</span>
            <span>{{ t('flashcards.review.again') }}</span>
          </button>
          <button type="button" class="rating hard" :disabled="busy" @click="rate(2)">
            <span class="material-symbols-outlined">trending_down</span>
            <span>{{ t('flashcards.review.hard') }}</span>
          </button>
          <button type="button" class="rating good" :disabled="busy" @click="rate(3)">
            <span class="material-symbols-outlined">check</span>
            <span>{{ t('flashcards.review.good') }}</span>
          </button>
          <button type="button" class="rating easy" :disabled="busy" @click="rate(4)">
            <span class="material-symbols-outlined">trending_up</span>
            <span>{{ t('flashcards.review.easy') }}</span>
          </button>
        </div>

        <p v-if="total > 0" class="remaining">
          {{ t('flashcards.review.remaining', { count: Math.max(total - index - 1, 0) }) }}
        </p>
        <p class="fuzz-note">{{ t('flashcards.review.fuzzNote') }}</p>

        <div v-if="allDone" class="complete" role="status">
          <p>{{ t('flashcards.review.complete') }}</p>
        </div>
      </section>
    </template>
  </FoldAwareLayout>
</template>

<script setup lang="ts">
/**
 * FlashcardReviewView — 单 deck 的复习会话（契约 §4 FSRS 主战场）。
 *
 * 流程：
 *   1. 进入路由 → 拉取 deck 下的 due cards（store.dueCardsForDeck）
 *   2. 渲染 front，点击翻面后渲染 back
 *   3. 用户点 Again/Hard/Good/Easy →
 *        - useFsrs.applyReview 算出新 state/due/stability/difficulty/intervalDays
 *        - store.applyReviewLocally 写入 store
 *        - store.enqueueReview 把评分事件入 outbox（offline 友好）
 *   4. 切到下一张；走完后展示「Session complete」
 *
 * Foldable：
 *   - outer：紧凑（仅 front + Again/Good 两档快评）
 *   - inner：完整四档 + 进度条 + 散开提示
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import FoldAwareLayout from './components/FoldAwareLayout.vue'
import VivoBatteryWhitelistGuide from './components/VivoBatteryWhitelistGuide.vue'
import { useFlashcardsStore } from '../../stores/flashcards'
import type { FlashcardCard, FlashcardRating } from '../../types/flashcards'

defineOptions({ name: 'FlashcardReviewView' })

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useFlashcardsStore()

const deckId = computed(() => String(route.params.deckId || ''))

const queue = computed<FlashcardCard[]>(() => store.dueCardsForDeck(deckId.value))
const total = computed(() => queue.value.length)
const index = ref(0)
const isFlipped = ref(false)
const busy = ref(false)
const allDone = computed(() => total.value > 0 && index.value >= total.value)

const currentCard = computed<FlashcardCard | null>(() => {
  if (allDone.value) return null
  return queue.value[index.value] ?? null
})

const currentNote = computed(() => {
  const card = currentCard.value
  if (!card) return null
  return store.notes.find((n) => n.id === card.noteId) ?? null
})

const progressPct = computed(() => {
  if (total.value === 0) return 0
  return Math.min(100, Math.round(((index.value + (isFlipped.value ? 0.5 : 0)) / total.value) * 100))
})

function flip() {
  if (!currentCard.value) return
  isFlipped.value = !isFlipped.value
}

function goBack() {
  if (window.history.length > 1 && window.history.state?.back) router.back()
  else router.push(`/flashcards/decks/${encodeURIComponent(deckId.value)}`)
}

async function rate(rating: FlashcardRating) {
  if (busy.value) return
  const card = currentCard.value
  if (!card) return
  busy.value = true
  try {
    store.applyReviewLocally(card.id, rating)
    store.enqueueReview(card.id, rating, 0)
    isFlipped.value = false
    if (index.value < total.value - 1) index.value += 1
    else index.value = total.value // 触发 allDone
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  store.loadFromCache()
  if (store.cards.length === 0) void store.refresh().catch(() => {})
  // vivo OriginOS 引导：首次进入时把当日提醒挂上
  void import('../../native/localNotifications').then((m) =>
    m.scheduleDailyReview(9, 0, dueCountHint.value).catch(() => {}),
  )
})

const dueCountHint = computed(() => total.value)
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
.head .progress { font-size: 13px; color: var(--text-secondary); }
.back-btn {
  border: 0;
  background: transparent;
  color: var(--text-primary);
  padding: 6px;
  border-radius: 999px;
  cursor: pointer;
}
.progress-bar {
  margin: 0 var(--space-4) var(--space-3);
  height: 4px;
  background: var(--bg-subtle);
  border-radius: 999px;
  overflow: hidden;
}
.progress-bar-fill {
  height: 100%;
  background: var(--brand-gradient);
  transition: width 0.3s ease;
}

.card-display {
  margin: var(--space-3) var(--space-4);
  min-height: 220px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-5) var(--space-4);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  cursor: pointer;
}
.card-display p { font-size: 18px; color: var(--text-primary); margin: 0; line-height: 1.4; }
.card-display small { display: block; font-size: 11px; color: var(--text-muted); margin-bottom: var(--space-2); }
.card-display .back { color: var(--text-primary); }
.hint { text-align: center; font-size: 12px; color: var(--brand-primary); margin: 0 0 var(--space-3); }

.ratings { display: grid; grid-template-columns: repeat(4, 1fr); gap: var(--space-2); padding: 0 var(--space-4); }
.ratings.compact { grid-template-columns: 1fr 1fr; }
.rating {
  border: 1px solid var(--border);
  background: var(--bg-card);
  border-radius: var(--radius-sm);
  padding: 10px 0;
  font: inherit;
  font-size: 13px;
  color: var(--text-primary);
  cursor: pointer;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}
.rating:disabled { opacity: 0.5; cursor: not-allowed; }
.rating.again { color: var(--danger); }
.rating.hard { color: var(--warning, #f59e0b); }
.rating.good { color: var(--success); }
.rating.easy { color: var(--brand-primary); }
.rating .material-symbols-outlined { font-size: 18px; }

.remaining { margin: var(--space-3) var(--space-4) 0; font-size: 12px; color: var(--text-secondary); }
.fuzz-note { margin: var(--space-2) var(--space-4) var(--space-5); font-size: 11px; color: var(--text-muted); }
.complete {
  margin: var(--space-5) var(--space-4);
  padding: var(--space-5);
  border-radius: var(--radius-md);
  background: var(--success-bg);
  color: var(--success);
  text-align: center;
}

.outer { padding: 0 var(--space-3); }
.outer .head { padding-top: var(--space-3); padding-bottom: var(--space-2); }
.outer .head h1 { font-size: 15px; }
.outer .card-display { min-height: 160px; margin: var(--space-2); padding: var(--space-3); }
.outer .card-display p { font-size: 15px; }
.cta { padding: 0 var(--space-3); margin-top: var(--space-3); }
.cta .primary {
  width: 100%;
  padding: 12px 0;
  border: 0;
  border-radius: var(--radius-sm);
  background: var(--brand-gradient);
  color: var(--text-inverse);
  cursor: pointer;
}
</style>