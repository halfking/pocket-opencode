<!--
  StudyHubView —— 「学习」tab 聚合页面（2026-09-23 TabBar 4+1 重组 Phase 2）。

  设计动机：
  - Flashcards（FSRS 复习）与 Notes（PKM 笔记）都属于「学习」心智，归到同一个 tab。
  - 用户进入 /study 第一眼看到「今日复习」Hero 卡（主交互点），
    再下方是「我的牌组」与「我的笔记」两个分组。
  - 详情页仍走独立路由（/flashcards/decks/:id、/notes/:id），深链保留。

  路由：`/study`
  进入：BottomNav 4 tab 的「学习」入口。
-->
<template>
  <div class="study-hub">
    <!-- Hero 复习卡：今日 FSRS due 总数 + 一键进入 -->
    <button
      class="hero"
      type="button"
      :disabled="totalDue === 0"
      :aria-label="t('study.hero.review')"
      @click="goReview"
    >
      <span class="hero-left">
        <span class="hero-label">{{ t('study.hero.today') }}</span>
        <span class="hero-num">
          <AnimatedNumber :value="totalDue" :duration="600" />
        </span>
        <span class="hero-text">{{ t('study.hero.cardsDue') }}</span>
      </span>
      <span class="hero-cta" aria-hidden="true">
        <span class="material-symbols-outlined">{{ totalDue > 0 ? 'play_arrow' : 'check_circle' }}</span>
        <span>{{ totalDue > 0 ? t('study.hero.start') : t('study.hero.allDone') }}</span>
      </span>
    </button>

    <!-- 我的牌组分组 -->
    <section class="group">
      <header class="group-head">
        <h2>
          <span class="material-symbols-outlined" aria-hidden="true">style</span>
          {{ t('study.decks.title') }}
        </h2>
        <div class="head-actions">
          <button class="link-btn" type="button" :aria-label="t('study.decks.stats')" @click="router.push('/flashcards/stats')">
            <span class="material-symbols-outlined" aria-hidden="true">monitoring</span>
          </button>
          <button class="link-btn" type="button" :aria-label="t('study.decks.browser')" @click="router.push('/flashcards/browser')">
            <span class="material-symbols-outlined" aria-hidden="true">manage_search</span>
          </button>
          <button class="link-btn" type="button" @click="router.push('/flashcards')">
            {{ t('study.decks.all') }}
          </button>
        </div>
      </header>
      <div v-if="store.loading" class="state">
        <Skeleton :count="2" />
      </div>
      <div v-else-if="decks.length === 0" class="empty">
        <p>{{ t('study.decks.empty') }}</p>
        <button class="primary" type="button" @click="goCreateDeck">
          <span class="material-symbols-outlined">add</span>
          {{ t('study.decks.create') }}
        </button>
      </div>
      <ul v-else class="deck-list" data-testid="study-deck-list">
        <li v-for="deck in decks.slice(0, 3)" :key="deck.deckId">
          <button class="deck-card" type="button" @click="openDeck(deck.deckId)">
            <span class="deck-name">{{ deck.name }}</span>
            <span class="deck-badge" :class="{ empty: deck.dueCount === 0 }">
              {{ t('study.decks.dueShort', { count: deck.dueCount }) }}
            </span>
          </button>
        </li>
      </ul>
    </section>

    <!-- 我的笔记分组 -->
    <section class="group">
      <header class="group-head">
        <h2>
          <span class="material-symbols-outlined" aria-hidden="true">edit_note</span>
          {{ t('study.notes.title') }}
        </h2>
        <button class="link-btn" type="button" @click="router.push('/notes')">
          {{ t('study.notes.all') }}
        </button>
      </header>
      <button class="notes-card" type="button" @click="router.push('/notes')">
        <span class="notes-icon" aria-hidden="true">
          <span class="material-symbols-outlined">mic</span>
        </span>
        <span class="notes-info">
          <span class="notes-title">{{ t('study.notes.voice') }}</span>
          <span class="notes-hint">{{ t('study.notes.hint') }}</span>
        </span>
        <span class="material-symbols-outlined chev" aria-hidden="true">chevron_right</span>
      </button>
    </section>
  </div>
</template>

<script setup lang="ts">
/**
 * StudyHubView —— Phase 2 落地：合并 Flashcards + 笔记到 /study。
 *
 * 数据：
 *  - 总 due 数 = sum(deckSummaries.dueCount)
 *  - 仅取前 3 个 deck 卡片，详情跳 /flashcards/decks/:id
 *  - 笔记入口暂走「录音 → 笔记」CTA，深链 /notes
 *
 * 离线 / 首次安装：due 总数为 0 时 Hero 进入 completed 态。
 */
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { AnimatedNumber, Skeleton } from '../../components'
import { useFlashcardsStore } from '../../stores/flashcards'
import { listNotes } from '../notes/notes-store'

defineOptions({ name: 'StudyHubView' })

const { t } = useI18n()
const router = useRouter()
const store = useFlashcardsStore()

const decks = computed(() => store.deckSummaries)
const totalDue = computed(() =>
  decks.value.reduce((sum, d) => sum + (d.dueCount ?? 0), 0),
)

onMounted(() => {
  // 不阻塞 UI：先同步拉 cache（sync），再后台 async sync
  try {
    store.loadFromCache()
  } catch {
    /* cache 损坏不致命：syncFromServer 会兜底 */
  }
  void store.syncFromServer().catch(() => {})
  // 笔记列表（用于未来计数；目前只走 CTA）
  void listNotes({ limit: 1 }).catch(() => {})
})

function goReview() {
  if (totalDue.value === 0) return
  const first = decks.value.find((d) => d.dueCount > 0)
  if (first) {
    router.push(`/flashcards/decks/${encodeURIComponent(first.deckId)}/review`)
  } else {
    router.push('/flashcards')
  }
}

function openDeck(deckId: string) {
  router.push(`/flashcards/decks/${encodeURIComponent(deckId)}`)
}

function goCreateDeck() {
  router.push('/flashcards/new')
}
</script>

<style scoped>
.study-hub {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
  padding-bottom: var(--space-3);
}

/* ===== Hero ===== */
.hero {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-4) var(--space-3);
  background: linear-gradient(135deg, var(--brand-primary), var(--brand-primary-2, #2563eb));
  color: var(--text-inverse, #fff);
  border: none;
  border-radius: var(--radius-md);
  text-align: left;
  cursor: pointer;
  min-height: 96px;
  transition: transform var(--duration-fast) var(--ease-out), opacity var(--duration-fast) var(--ease-out);
}

.hero:disabled {
  background: var(--bg-subtle);
  color: var(--text-tertiary);
  cursor: default;
}

.hero:not(:disabled):active {
  transform: scale(0.98);
  opacity: 0.92;
}

.hero-left {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.hero-label {
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  opacity: 0.85;
}

.hero-num {
  font-size: 36px;
  font-weight: var(--font-weight-bold);
  line-height: 1;
  letter-spacing: -0.5px;
}

.hero-text {
  font-size: 13px;
  opacity: 0.9;
}

.hero-cta {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  background: rgba(255, 255, 255, 0.18);
  border-radius: var(--radius-full);
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
}

.hero-cta .material-symbols-outlined {
  font-size: 20px;
}

/* ===== Group ===== */
.group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--space-1);
}

.group-head h2 {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  margin: 0;
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.group-head .material-symbols-outlined {
  font-size: 18px;
  color: var(--brand-primary);
}

.link-btn {
  background: transparent;
  border: none;
  color: var(--brand-primary);
  font-size: 13px;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-md);
}

.head-actions {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.head-actions .link-btn {
  padding: 4px;
  min-width: 28px;
  min-height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.head-actions .link-btn .material-symbols-outlined {
  font-size: 18px;
}

.link-btn:active {
  background: var(--color-bg-hover, rgba(0, 0, 0, 0.04));
}

/* ===== Decks ===== */
.deck-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.deck-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  width: 100%;
  padding: var(--space-3);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
  min-height: 56px;
  transition: background var(--duration-fast) var(--ease-out);
}

.deck-card:active {
  background: var(--color-bg-hover, rgba(0, 0, 0, 0.04));
}

.deck-name {
  flex: 1;
  font-size: 14px;
  font-weight: var(--font-weight-medium);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.deck-badge {
  padding: 2px var(--space-2);
  border-radius: 999px;
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  color: var(--brand-primary, #4c8dff);
  font-size: 12px;
  font-weight: var(--font-weight-semibold);
  flex-shrink: 0;
}

.deck-badge.empty {
  background: var(--bg-subtle);
  color: var(--text-tertiary);
}

/* ===== Notes ===== */
.notes-card {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-3);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
  min-height: 64px;
}

.notes-card:active {
  background: var(--color-bg-hover, rgba(0, 0, 0, 0.04));
}

.notes-icon {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  color: var(--brand-primary, #4c8dff);
  flex-shrink: 0;
}

.notes-icon .material-symbols-outlined {
  font-size: 22px;
}

.notes-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.notes-title {
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
}

.notes-hint {
  font-size: 12px;
  color: var(--text-secondary);
}

.chev {
  font-size: 20px;
  color: var(--text-tertiary, var(--text-muted));
}

.empty {
  padding: var(--space-4) var(--space-3);
  background: var(--bg-card);
  border: 1px dashed var(--border);
  border-radius: var(--radius-md);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: 13px;
}

.empty .primary {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  background: var(--brand-primary);
  color: var(--text-inverse, #fff);
  border: none;
  border-radius: var(--radius-full);
  font-size: 13px;
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
}

.state {
  padding: var(--space-2);
}

.state .material-symbols-outlined {
  font-size: 18px;
}
</style>