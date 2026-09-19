<template>
  <FoldAwareLayout>
    <template #outer>
      <section class="page outer">
        <header class="head">
          <h1>{{ t('flashcards.list.title') }}</h1>
        </header>
        <main class="list">
          <button v-if="decks.length" class="card" type="button" @click="openDeck(decks[0].deckId)">
            <span class="name">{{ decks[0].name }}</span>
            <span class="badge" :class="{ empty: decks[0].dueCount === 0 }">
              {{ t('flashcards.list.dueToday', { count: decks[0].dueCount }) }}
            </span>
          </button>
          <button class="add" type="button" @click="goCreate">
            <span class="material-symbols-outlined">add</span>
            <span>{{ t('flashcards.list.create') }}</span>
          </button>
        </main>
      </section>
    </template>
    <template #inner>
      <section class="page inner">
        <header class="head">
          <h1>{{ t('flashcards.list.title') }}</h1>
          <button class="add-btn" type="button" aria-label="create" @click="goCreate">
            <span class="material-symbols-outlined">add</span>
          </button>
        </header>

        <div v-if="store.loading" class="state" role="status">{{ t('common.loading') || '加载中…' }}</div>
        <div v-else-if="store.error" class="error" role="alert">
          {{ store.error }}
          <button type="button" @click="reload">{{ retryLabel }}</button>
        </div>
        <div v-else-if="decks.length === 0" class="empty">
          <p>{{ t('flashcards.list.empty') }}</p>
          <button class="primary" type="button" @click="goCreate">{{ t('flashcards.list.create') }}</button>
        </div>

        <main v-else class="list">
          <article
            v-for="deck in decks"
            :key="deck.deckId"
            class="card"
            role="button"
            tabindex="0"
            @click="openDeck(deck.deckId)"
            @keyup.enter="openDeck(deck.deckId)"
          >
            <div class="card-head">
              <h2>{{ deck.name }}</h2>
              <span class="badge" :class="{ empty: deck.dueCount === 0 }">
                {{ t('flashcards.list.dueToday', { count: deck.dueCount }) }}
              </span>
            </div>
            <p class="meta">
              <span>{{ totalLabel(deck.totalCards) }}</span>
              <span>{{ t('flashcards.list.dueToday', { count: deck.dueCount }) }}</span>
            </p>
          </article>
        </main>
      </section>
    </template>
  </FoldAwareLayout>
</template>

<script setup lang="ts">
/**
 * FlashcardListView — 卡组列表（契约 §2 入口）。
 *
 * 路由：`/flashcards`
 * 依赖：stores/flashcards.ts（summaries）+ services/flashcards.ts。
 * Foldable：双 slot；外屏单卡紧凑视图，内屏完整列表。
 */
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import FoldAwareLayout from './components/FoldAwareLayout.vue'
import { useFlashcardsStore } from '../../stores/flashcards'

defineOptions({ name: 'FlashcardListView' })

const router = useRouter()
const { t } = useI18n()
const store = useFlashcardsStore()

const decks = computed(() => store.deckSummaries)

function openDeck(deckId: string) {
  router.push(`/flashcards/decks/${encodeURIComponent(deckId)}`)
}

function goCreate() {
  router.push('/flashcards/new')
}

const retryLabel = computed(() => t('flashcards.error.loadFailed') || 'Retry')

function totalLabel(total: number) {
  return `${total} cards`
}

async function reload() {
  await store.refresh().catch(() => {})
}

onMounted(async () => {
  store.loadFromCache()
  await store.refresh().catch(() => {})
})
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); display: flex; flex-direction: column; }
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4) var(--space-4) var(--space-2);
}
.head h1 { margin: 0; font-size: 20px; color: var(--text-primary); }
.add-btn {
  border: 0;
  background: transparent;
  color: var(--brand-primary);
  padding: 6px;
  border-radius: 999px;
  cursor: pointer;
}
.list { display: flex; flex-direction: column; gap: var(--space-3); padding: var(--space-3) var(--space-4) 100px; }
.card {
  text-align: left;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  padding: var(--space-4);
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 6px;
  color: inherit;
  font: inherit;
}
.card-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.card h2 { margin: 0; font-size: 15px; color: var(--text-primary); }
.badge {
  font-size: 11px;
  padding: 3px 9px;
  border-radius: 999px;
  background: var(--brand-primary);
  color: var(--text-inverse);
}
.badge.empty { background: var(--bg-subtle); color: var(--text-secondary); }
.meta { margin: 0; font-size: 12px; color: var(--text-secondary); display: flex; gap: 14px; }
.empty { text-align: center; padding: 60px var(--space-4); color: var(--text-secondary); }
.empty .primary {
  margin-top: var(--space-3);
  padding: 10px 18px;
  background: var(--brand-gradient);
  border: 0;
  color: var(--text-inverse);
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.error { margin: var(--space-3); padding: var(--space-3); color: var(--danger); background: var(--danger-bg); border-radius: var(--radius-sm); display: flex; gap: 8px; align-items: center; }
.error button { border: 1px solid var(--danger); background: transparent; color: var(--danger); padding: 4px 10px; border-radius: var(--radius-sm); cursor: pointer; }
.state { padding: 48px var(--space-3); text-align: center; color: var(--text-secondary); }
.add {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: var(--space-3);
  border: 1px dashed var(--border);
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--brand-primary);
  cursor: pointer;
}
.outer { padding: 0 var(--space-3); }
.outer .head { padding-top: var(--space-3); padding-bottom: 0; }
.outer .head h1 { font-size: 17px; }
</style>