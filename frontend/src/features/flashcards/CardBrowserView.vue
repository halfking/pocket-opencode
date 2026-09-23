<template>
  <section class="page">
    <header class="head">
      <button type="button" class="back-btn" aria-label="back" @click="goBack">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1>{{ t('flashcards.browser.title') }}</h1>
      <span class="result-count" data-testid="result-count">
        {{ t('flashcards.browser.resultCount', { count: results.length }) }}
      </span>
    </header>

    <!-- 搜索框 -->
    <div class="search-bar">
      <span class="material-symbols-outlined search-icon" aria-hidden="true">search</span>
      <input
        v-model="query"
        type="search"
        class="search-input"
        :placeholder="t('flashcards.browser.searchPlaceholder')"
        :aria-label="t('flashcards.browser.searchPlaceholder')"
      />
    </div>

    <!-- 过滤 chips 行 -->
    <div class="filters">
      <!-- Deck 过滤 -->
      <button
        type="button"
        class="chip"
        :class="{ active: deckFilter }"
        @click="deckFilter ? (deckFilter = '') : (deckPickerOpen = true)"
      >
        <span class="material-symbols-outlined" aria-hidden="true">folder</span>
        <span>{{ deckFilter ? deckName(deckFilter) : t('flashcards.browser.allDecks') }}</span>
      </button>
      <!-- Tag 过滤 -->
      <button
        type="button"
        class="chip"
        :class="{ active: tagFilters.length > 0 }"
        @click="tagPickerOpen = true"
      >
        <span class="material-symbols-outlined" aria-hidden="true">label</span>
        <span>
          {{ tagFilters.length > 0
            ? t('flashcards.browser.tagsSelected', { count: tagFilters.length })
            : t('flashcards.browser.allTags') }}
        </span>
      </button>
      <!-- State 过滤（循环按钮：全部 → 新 → 学习中 → 复习 → 重学） -->
      <button
        type="button"
        class="chip"
        :class="{ active: stateFilter !== null }"
        @click="cycleStateFilter"
      >
        <span class="material-symbols-outlined" aria-hidden="true">layers</span>
        <span>{{ stateLabel }}</span>
      </button>
      <button
        v-if="query || deckFilter || tagFilters.length || stateFilter !== null"
        type="button"
        class="clear"
        @click="clearFilters"
      >
        <span class="material-symbols-outlined" aria-hidden="true">filter_alt_off</span>
        <span>{{ t('flashcards.browser.clear') }}</span>
      </button>
    </div>

    <!-- 结果列表 -->
    <main v-if="results.length === 0" class="empty">
      <p>{{ t('flashcards.browser.empty') }}</p>
    </main>
    <main v-else class="list">
      <article
        v-for="row in results"
        :key="row.note.id"
        class="row"
        role="button"
        tabindex="0"
        @click="edit(row.note.id)"
        @keyup.enter="edit(row.note.id)"
      >
        <p class="row-front">
          <span v-if="row.stateBadge" class="state-badge" :class="`state-${row.stateBadge}`">
            {{ row.stateBadgeLabel }}
          </span>
          <HighlightedText :text="row.displayFront" :query="query" />
        </p>
        <p class="row-back">
          <HighlightedText :text="row.displayBack" :query="query" />
        </p>
        <p class="row-meta">
          <span>{{ row.deckName }}</span>
          <span v-if="row.tagList.length > 0" class="meta-tags">
            <span v-for="t in row.tagList" :key="t" class="meta-tag">{{ t }}</span>
          </span>
        </p>
      </article>
    </main>

    <!-- Deck 选择底部 sheet -->
    <BottomSheet v-if="deckPickerOpen" :model-value="deckPickerOpen" placement="bottom" @update:model-value="(v) => (deckPickerOpen = v)">
      <div class="picker">
        <h3>{{ t('flashcards.browser.pickDeck') }}</h3>
        <ul>
          <li>
            <button
              type="button"
              class="picker-row"
              :class="{ active: !deckFilter }"
              @click="selectDeck('')"
            >— {{ t('flashcards.browser.allDecks') }} —</button>
          </li>
          <li v-for="d in decks" :key="d.deckId">
            <button
              type="button"
              class="picker-row"
              :class="{ active: deckFilter === d.deckId }"
              @click="selectDeck(d.deckId)"
            >{{ d.name }}</button>
          </li>
        </ul>
      </div>
    </BottomSheet>

    <!-- Tag 选择底部 sheet -->
    <BottomSheet v-if="tagPickerOpen" :model-value="tagPickerOpen" placement="bottom" @update:model-value="(v) => (tagPickerOpen = v)">
      <div class="picker">
        <h3>{{ t('flashcards.browser.pickTags') }}</h3>
        <ul v-if="allTags.length > 0">
          <li v-for="t in allTags" :key="t">
            <button
              type="button"
              class="picker-row"
              :class="{ active: tagFilters.includes(t) }"
              @click="toggleTag(t)"
            >
              <span>{{ t }}</span>
              <span v-if="tagFilters.includes(t)" class="material-symbols-outlined">check</span>
            </button>
          </li>
        </ul>
        <p v-else class="empty-tags">{{ t('flashcards.browser.noTags') }}</p>
      </div>
    </BottomSheet>
  </section>
</template>

<script setup lang="ts">
/**
 * CardBrowserView —— 卡片浏览器（Anki `/` 对齐）。
 *
 * 路由：`/flashcards/browser`
 *
 * 范围：
 *   - 跨 deck 全局搜索 front / back 文本（大小写不敏感）
 *   - 按 deck 过滤（单选）
 *   - 按 tag 过滤（多选）
 *   - 按 state 过滤（4 态循环：all → new → learning → review → relearning）
 *   - 点击 row → /flashcards/notes/:id/edit
 *
 * 性能：纯前端 computed；notes 数量大时（>5k）按需替换为 debounce + 分页。
 * Phase 5 简化：本地小数据量，先 in-memory 过滤。
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import BottomSheet from '../../components/base/BottomSheet.vue'
import HighlightedText from './components/HighlightedText.vue'
import { useFlashcardsStore } from '../../stores/flashcards'
import type { FlashcardCard, FlashcardNote, FlashcardState } from '../../types/flashcards'

defineOptions({ name: 'CardBrowserView' })

const { t } = useI18n()
const router = useRouter()
const store = useFlashcardsStore()

const query = ref('')
const deckFilter = ref('')
const tagFilters = ref<string[]>([])
const stateFilter = ref<FlashcardState | null>(null)
const deckPickerOpen = ref(false)
const tagPickerOpen = ref(false)

const decks = computed(() => store.deckConfigs)

const allTags = computed(() => {
  const set = new Set<string>()
  for (const n of store.notes) {
    for (const tag of n.tags ?? []) set.add(tag)
  }
  return [...set].sort()
})

const STATE_CYCLE: (FlashcardState | null)[] = [null, 0, 1, 2, 3]
const STATE_LABEL_KEY: Record<string, string> = {
  '': 'all',
  '0': 'new',
  '1': 'learning',
  '2': 'review',
  '3': 'relearning',
}

const stateLabel = computed(() => {
  const k = stateFilter.value === null ? '' : String(stateFilter.value)
  return t(`flashcards.browser.state.${STATE_LABEL_KEY[k]}`)
})

function cycleStateFilter() {
  const cur = stateFilter.value === null ? '' : String(stateFilter.value)
  const idx = STATE_CYCLE.findIndex((s) => (s === null ? '' : String(s)) === cur)
  const next = STATE_CYCLE[(idx + 1) % STATE_CYCLE.length] ?? null
  stateFilter.value = next
}

function selectDeck(id: string) {
  deckFilter.value = id
  deckPickerOpen.value = false
}

function toggleTag(t: string) {
  if (tagFilters.value.includes(t)) {
    tagFilters.value = tagFilters.value.filter((x) => x !== t)
  } else {
    tagFilters.value = [...tagFilters.value, t]
  }
}

function clearFilters() {
  query.value = ''
  deckFilter.value = ''
  tagFilters.value = []
  stateFilter.value = null
}

function deckName(deckId: string): string {
  return store.deckById(deckId)?.name ?? deckId
}

function edit(noteId: string) {
  router.push(`/flashcards/notes/${encodeURIComponent(noteId)}/edit`)
}

function goBack() {
  if (window.history.length > 1 && window.history.state?.back) router.back()
  else router.push('/flashcards')
}

interface BrowserRow {
  note: FlashcardNote
  card: FlashcardCard | null
  displayFront: string
  displayBack: string
  stateBadge: FlashcardState | null
  stateBadgeLabel: string
  deckName: string
  tagList: string[]
}

const results = computed<BrowserRow[]>(() => {
  const q = query.value.trim().toLowerCase()
  const noteIdToCard = new Map<string, FlashcardCard>()
  for (const c of store.cards) {
    if (!c.deletedAt) noteIdToCard.set(c.noteId, c)
  }
  const rows: BrowserRow[] = []
  for (const n of store.notes) {
    if (n.deletedAt) continue
    if (deckFilter.value && n.deckId !== deckFilter.value) continue
    if (tagFilters.value.length > 0) {
      const has = tagFilters.value.every((t) => (n.tags ?? []).includes(t))
      if (!has) continue
    }
    const card = noteIdToCard.get(n.id)
    if (stateFilter.value !== null) {
      if (!card || card.state !== stateFilter.value) continue
    }
    if (q) {
      const hay = `${n.front}\n${n.back}`.toLowerCase()
      if (!hay.includes(q)) continue
    }
    rows.push({
      note: n,
      card: card ?? null,
      displayFront: n.front.slice(0, 200),
      displayBack: n.back.slice(0, 200),
      stateBadge: card ? card.state : null,
      stateBadgeLabel: card ? STATE_LABEL_KEY[String(card.state)] ?? 'new' : 'new',
      deckName: deckName(n.deckId),
      tagList: (n.tags ?? []).slice(0, 5),
    })
  }
  // 默认按 deck + 创建时间倒序（无 q 时）
  if (!q) {
    rows.sort((a, b) => b.note.createdAt - a.note.createdAt)
  }
  return rows
})

onMounted(() => {
  store.loadFromCache()
  if (store.notes.length === 0) void store.refresh().catch(() => {})
})
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); display: flex; flex-direction: column; }

.head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-4);
}
.head h1 { flex: 1; margin: 0; font-size: 18px; color: var(--text-primary); }
.result-count { font-size: 12px; color: var(--text-tertiary); }
.back-btn {
  border: 0;
  background: transparent;
  color: var(--text-primary);
  padding: 6px;
  border-radius: 999px;
  cursor: pointer;
}

.search-bar {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0 var(--space-4) var(--space-2);
  padding: 8px 12px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
}

.search-icon { color: var(--text-tertiary); font-size: 18px; }
.search-input {
  flex: 1;
  border: none;
  outline: none;
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  font-size: 14px;
}

.filters {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  padding: 0 var(--space-4) var(--space-2);
}

.chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 6px 10px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 999px;
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  min-height: 32px;
}

.chip .material-symbols-outlined {
  font-size: 14px;
}

.chip.active {
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  border-color: var(--brand-primary);
  color: var(--brand-primary);
}

.clear {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 6px 10px;
  background: transparent;
  border: none;
  color: var(--text-tertiary);
  font-size: 12px;
  cursor: pointer;
}

.list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: 0 var(--space-4) 100px;
}

.row {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  cursor: pointer;
}

.row:active { background: var(--color-bg-hover, rgba(0, 0, 0, 0.04)); }

.row-front {
  margin: 0;
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  line-height: 1.4;
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.row-back {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.row-meta {
  margin: var(--space-2) 0 0;
  font-size: 11px;
  color: var(--text-tertiary);
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
  align-items: center;
}

.meta-tags { display: inline-flex; gap: 4px; flex-wrap: wrap; }
.meta-tag {
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--bg-subtle);
  color: var(--text-secondary);
}

.state-badge {
  display: inline-block;
  font-size: 10px;
  font-weight: var(--font-weight-semibold);
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--bg-subtle);
  color: var(--text-tertiary);
  flex-shrink: 0;
}

.state-badge.state-0 { background: var(--brand-bg, rgba(76, 141, 255, 0.12)); color: var(--brand-primary); }
.state-badge.state-1 { background: rgba(245, 158, 11, 0.12); color: #f59e0b; }
.state-badge.state-2 { background: rgba(34, 197, 94, 0.12); color: #16a34a; }
.state-badge.state-3 { background: rgba(239, 68, 68, 0.12); color: #ef4444; }

.empty {
  padding: var(--space-5);
  color: var(--text-secondary);
  text-align: center;
}

.picker {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
}

.picker h3 {
  margin: 0;
  font-size: 15px;
  font-weight: var(--font-weight-semibold);
}

.picker ul {
  list-style: none;
  margin: 0;
  padding: 0;
  background: var(--bg-card);
  border-radius: var(--radius-sm);
  overflow: hidden;
}

.picker-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  min-height: 44px;
  padding: var(--space-2) var(--space-3);
  background: transparent;
  border: none;
  border-bottom: 1px solid var(--border);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
  font: inherit;
  font-size: 14px;
}

.picker ul li:last-child .picker-row { border-bottom: none; }
.picker-row.active { color: var(--brand-primary); font-weight: var(--font-weight-semibold); }

.empty-tags { color: var(--text-secondary); font-size: 13px; padding: var(--space-3); }
</style>