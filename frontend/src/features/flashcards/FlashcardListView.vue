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
          {{ apiError(store.error, t('flashcards.error.loadFailed')) }}
          <button type="button" @click="reload">{{ retryLabel }}</button>
        </div>
        <div v-else-if="decks.length === 0" class="empty" data-testid="flashcards-empty">
          <p>{{ t('flashcards.list.empty') }}</p>
          <!--
            BUG-U（2026-09-30）：零卡组时这里原本只有一个「新建卡片」按钮，
            跳 /flashcards/new —— 但那页的「保存」在没有卡组时恒 disabled
            （selectedDeckId 为空 → isValid false）。用户点进去才发现要先建组，
            而建组入口是那页顶部的另一个输入框。**从零状态看，这是一个死胡同。**
            现在零卡组时直接在本页内联建组；建完列表立刻出现，可继续点「新建卡片」。
          -->
          <form
            class="deck-create"
            data-testid="deck-create-form"
            @submit.prevent="submitCreateDeck"
          >
            <input
              v-model="newDeckName"
              type="text"
              :placeholder="t('flashcards.deck.createPlaceholder')"
              :aria-label="t('flashcards.deck.create')"
            />
            <button class="primary" type="submit" :disabled="deckCreating || !newDeckName.trim()">
              {{ deckCreating ? t('common.loading') : t('flashcards.deck.create') }}
            </button>
          </form>
          <p v-if="deckError" class="error" role="alert">{{ deckError }}</p>
          <!--
            这里**故意不放**「新建卡片」按钮：没有卡组时那页保存恒 disabled，
            摆一个点了必然失败、又不解释原因的按钮比不放更糟。
            建完组卡组立刻出现在下方列表，顶部 + 按钮即可继续建卡。
          -->
        </div>

        <main v-else class="list">
          <!--
            BUG-K follow-up（2026-09-30）：有卡组时列表页原本**没有**建组入口，
            「新建卡组」只存在于 FlashcardEditView 顶部那个表单里 —— 于是加第 2 个
            卡组必须先点「新建卡片」进编辑页才能建，入口语义和位置都不对。
            这里补一个可展开的建组入口，复用同一套 newDeckName/submitCreateDeck。
          -->
          <div class="deck-new">
            <button
              type="button"
              class="deck-toggle"
              data-testid="deck-create-toggle"
              :aria-expanded="showDeckForm"
              @click="showDeckForm = !showDeckForm"
            >
              <span class="material-symbols-outlined">library_add</span>
              <span>{{ t('flashcards.deck.create') }}</span>
            </button>
            <form
              v-if="showDeckForm"
              class="deck-create"
              data-testid="deck-create-form-existing"
              @submit.prevent="submitCreateDeck"
            >
              <input
                v-model="newDeckName"
                type="text"
                :placeholder="t('flashcards.deck.createPlaceholder')"
                :aria-label="t('flashcards.deck.create')"
              />
              <button class="primary" type="submit" :disabled="deckCreating || !newDeckName.trim()">
                {{ deckCreating ? t('common.loading') : t('flashcards.deck.create') }}
              </button>
            </form>
            <p v-if="deckError" class="error" role="alert">{{ deckError }}</p>
          </div>
          <article
            v-for="deck in decks"
            :key="deck.deckId"
            class="card"
            data-testid="flashcards-deck-item"
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
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import FoldAwareLayout from './components/FoldAwareLayout.vue'
import { useFlashcardsStore } from '../../stores/flashcards'
import { useApiError } from '../../composables/useApiError'

defineOptions({ name: 'FlashcardListView' })

const router = useRouter()
const { t } = useI18n()
const store = useFlashcardsStore()
const apiError = useApiError()

const decks = computed(() => store.deckSummaries)

function openDeck(deckId: string) {
  router.push(`/flashcards/decks/${encodeURIComponent(deckId)}`)
}

function goCreate() {
  router.push('/flashcards/new')
}

// BUG-U：零卡组时的内联建组。store.createDeck 建完会把卡组合并进本地缓存并
// persistCache，因此 decks computed 会立刻更新，空态自动消失。
const newDeckName = ref('')
const deckCreating = ref(false)
const deckError = ref('')
// BUG-K follow-up：有卡组时建组表单默认收起，点「新建卡组」才展开。
const showDeckForm = ref(false)

async function submitCreateDeck() {
  const name = newDeckName.value.trim()
  if (!name || deckCreating.value) return
  deckCreating.value = true
  deckError.value = ''
  try {
    await store.createDeck(name)
    newDeckName.value = ''
  } catch (err) {
    deckError.value = apiError(err, t('flashcards.error.loadFailed'))
  } finally {
    deckCreating.value = false
  }
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
.head h1 { margin: 0; font-size: 18px; color: var(--text-primary); }
.add-btn {
  border: 0;
  background: transparent;
  color: var(--brand-primary);
  padding: 6px;
  border-radius: 999px;
  cursor: pointer;
}
.list { display: flex; flex-direction: column; gap: var(--space-3); padding: var(--space-3) var(--space-4) 100px; }
.deck-new { display: flex; flex-direction: column; gap: var(--space-2); }
.deck-toggle {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  align-self: flex-start;
  border: 1px dashed var(--border);
  background: transparent;
  color: var(--text-primary);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md, 8px);
  cursor: pointer;
}
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
.empty .primary:disabled { opacity: 0.5; cursor: not-allowed; }
.deck-create { display: flex; gap: var(--space-2); margin-top: var(--space-3); }
.deck-create input {
  flex: 1;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  color: var(--text-primary);
  font: inherit;
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
.outer .head h1 { font-size: 15px; }
</style>