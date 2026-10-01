<!--
  StudyHubView —— 「学习」中心（docs/学习muse/03-架构方案.md §2）。

  这一页回答一个问题：**今天该学什么**。
  数据来自 Learning Core 的 /api/learning/*：
    - 今日概览（到期卡片 / inbox 待处理 / 复习中 / 今日到期工作项）
    - 学习收件箱（笔记、邮件、RSS、会议…被「加入学习」的材料）
    - 每日回顾提醒（下次触发时间 + 一键开启）

  降级约定：Learning Core 依赖 Postgres，remote-only 部署下 /api/learning/*
  返回 503。此时**不显示任何错误**，而是退回 v1 行为——只展示本地闪卡 due
  汇总（与 2026-09-23 融合重构 Phase 2 一致）。学习中心是增量，不是新的可用性前提。

  路由：`/study`；进入：BottomNav 4 tab 的「学习」入口。
  详情页仍走独立路由（/flashcards/decks/:id、/notes/:id），深链保留。
-->
<template>
  <div class="study-hub">
    <!-- Hero：今日概览。计数优先用服务端 Learning Core，缺省回落到本地闪卡 -->
    <button
      class="hero"
      type="button"
      :disabled="!heroActionable"
      :aria-label="heroLabel"
      data-testid="study-hero"
      @click="goReview"
    >
      <span class="hero-left">
        <span class="hero-label">{{ t('study.hero.today') }}</span>
        <span class="hero-num">
          <AnimatedNumber :value="heroCount" :duration="600" />
        </span>
        <span class="hero-text">{{ heroText }}</span>
      </span>
      <span class="hero-cta" aria-hidden="true">
        <!-- 图标必须用「字面量写在模板里」的名字：material-symbols 子集脚本
             （scripts/build-material-symbols-subset.mjs）只按
             material-symbols-outlined"...>name< 这个正则扫描源码，
             JS 表达式里的字符串（如原来的 play_arrow）扫不到，会缺字。 -->
        <span class="material-symbols-outlined">{{ heroActionable ? 'auto_awesome' : 'check_circle' }}</span>
        <span>{{ heroActionable ? t('study.hero.start') : t('study.hero.allDone') }}</span>
      </span>
    </button>

    <!-- 今日明细：四项计数（仅服务端 Learning Core 可用时出现） -->
    <!-- 连续学习天数：不可用时整块不渲染（区别于「0 天」） -->
    <div v-if="streak" class="streak" data-testid="study-streak">
      <span class="material-symbols-outlined" aria-hidden="true">local_fire_department</span>
      <span class="streak-num">{{ streak.streak.current }}</span>
      <span class="streak-label">{{ t('study.streak.days') }}</span>
      <span v-if="streak.next" class="streak-next">
        {{ t('study.streak.toNext', { count: streak.next - streak.streak.current, next: streak.next }) }}
      </span>
      <span v-else-if="streak.milestone" class="streak-next">
        {{ t('study.streak.milestoneReached', { days: streak.milestone }) }}
      </span>
    </div>

    <section v-if="due" class="group" data-testid="study-due-breakdown">
      <header class="group-head">
        <h2>
          <span class="material-symbols-outlined" aria-hidden="true">psychology</span>
          {{ t('study.due.title') }}
        </h2>
      </header>
      <ul class="due-grid">
        <li v-for="row in dueRows" :key="row.key" class="due-cell" :class="{ zero: row.value === 0 }">
          <span class="material-symbols-outlined" aria-hidden="true">{{ row.icon }}</span>
          <span class="due-value">{{ row.value }}</span>
          <span class="due-label">{{ row.label }}</span>
        </li>
      </ul>
    </section>

    <!-- 每日回顾提醒 -->
    <section class="group" data-testid="study-reminder">
      <header class="group-head">
        <h2>
          <span class="material-symbols-outlined" aria-hidden="true">notifications</span>
          {{ t('study.reminder.title') }}
        </h2>
        <span v-if="nextReminderAtValue > 0" class="reminder-next">
          {{ t('study.reminder.next', { time: nextReminderLabel }) }}
        </span>
      </header>
      <div v-if="!learningAvailable" class="reminder-offline">
        {{ t('study.reminder.offline') }}
      </div>
      <div v-else class="reminder-presets">
        <button
          v-for="preset in reminderPresets"
          :key="preset"
          class="preset-btn"
          :class="{ active: dailyDigestTime === preset }"
          type="button"
          :disabled="reminderSaving"
          :data-testid="`study-reminder-${preset.replace(':', '')}`"
          @click="setDailyDigest(preset)"
        >
          <span class="material-symbols-outlined" aria-hidden="true">
            {{ dailyDigestTime === preset ? 'check' : 'schedule' }}
          </span>
          {{ preset }}
        </button>
      </div>
    </section>

    <!-- 学习收件箱 -->
    <section v-if="learningAvailable" class="group" data-testid="study-inbox">
      <header class="group-head">
        <h2>
          <span class="material-symbols-outlined" aria-hidden="true">archive</span>
          {{ t('study.inbox.title') }}
          <span class="count-badge">{{ inboxItems.length }}</span>
        </h2>
      </header>
      <div v-if="inboxLoading" class="state">
        <Skeleton :count="2" />
      </div>
      <div v-else-if="inboxItems.length === 0" class="empty">
        <p>{{ t('study.inbox.empty') }}</p>
      </div>
      <ul v-else class="inbox-list">
        <li v-for="item in inboxItems" :key="item.id" class="inbox-item">
          <span class="inbox-icon" aria-hidden="true">
            <span class="material-symbols-outlined">{{ sourceIcon(item.sourceKind) }}</span>
          </span>
          <button class="inbox-body" type="button" @click="startItem(item)">
            <span class="inbox-title">{{ item.title }}</span>
            <span class="inbox-meta">{{ t(`study.source.${item.sourceKind}`) }}</span>
          </button>
          <button
            class="inbox-advance"
            type="button"
            :aria-label="t('study.inbox.advance')"
            :disabled="advancingId === item.id"
            @click="advance(item)"
          >
            <span class="material-symbols-outlined" aria-hidden="true">check</span>
          </button>
        </li>
      </ul>
    </section>

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
      <div v-else-if="decks.length === 0" class="empty" data-testid="study-empty">
        <p>{{ t('study.decks.empty') }}</p>
        <!--
          BUG-AA（2026-09-30 真机走查发现）：这里原本是一个按钮，文案是
          「新建牌组 / New deck」，点击却 `router.push('/flashcards/new')` ——
          那是**新建卡片**页。文案与行为不符。
          更糟的是从零状态点进去必然撞上 BUG-U 那个死胡同：
          没有卡组时那页的「保存」恒 disabled（selectedDeckId 为空 → isValid false）。
          改为与 FlashcardListView（BUG-U / BUG-X）**同构**的内联建组：
          走同一个已真机验证过的 store.createDeck，建完 decks computed 立刻更新。
        -->
        <form
          class="deck-create"
          data-testid="study-deck-create-form"
          @submit.prevent="submitCreateDeck"
        >
          <input
            v-model="newDeckName"
            type="text"
            :placeholder="t('flashcards.deck.createPlaceholder')"
            :aria-label="t('flashcards.deck.create')"
            data-testid="study-deck-name-input"
          />
          <button
            class="primary"
            type="submit"
            :disabled="deckCreating || !newDeckName.trim()"
            data-testid="study-deck-create-submit"
          >
            {{ deckCreating ? t('common.loading') : t('flashcards.deck.create') }}
          </button>
        </form>
        <p v-if="deckError" class="error" role="alert" data-testid="study-deck-create-error">
          {{ deckError }}
        </p>
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
 * 数据来源与降级顺序（重要，别在本地缓存 Learning Core 的结果）：
 *  1. Learning Core 可用 → 今日概览 / 收件箱 / 提醒都用服务端口径；
 *  2. 不可用（503 / 离线 / 未登录）→ 只显示本地闪卡 due 汇总，页面其余部分照旧。
 *
 * 服务端口径优先的理由：到期卡数由后端 FSRS 统计（ADR-002），
 * 本地 ts-fsrs 只在某台设备上更新过，两者会不一致；以服务端为准才不会
 * "手机上还提示 30 张，别的端显示 0 张"。
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { ICON, type IconName } from '../../constants/icons'
import { AnimatedNumber, Skeleton } from '../../components'
import { useFlashcardsStore } from '../../stores/flashcards'
import * as learningApi from '../../services/learning'
import type { LearningDueSummary, LearningItem, LearningStreakView } from '../../types/learning'
import {
  dailyRuleTime,
  dueSummaryCount,
  dueSummaryHeadlineKey,
  formatClockTime,
  hasDueWork,
  nextReminderAt,
} from '../../utils/learning-due'
import { listNotes } from '../notes/notes-store'
import { useApiError } from '../../composables/useApiError'

defineOptions({ name: 'StudyHubView' })

const { t } = useI18n()
const router = useRouter()
const store = useFlashcardsStore()

const decks = computed(() => store.deckSummaries)
const localDue = computed(() =>
  decks.value.reduce((sum, d) => sum + (d.dueCount ?? 0), 0),
)

const due = ref<LearningDueSummary | null>(null)
const apiError = useApiError()
/** Learning Core 不可用时为 false —— 决定收件箱 / 提醒区是否出现。 */
const learningAvailable = ref(false)
const inboxItems = ref<LearningItem[]>([])
const inboxLoading = ref(false)
const advancingId = ref('')
const reminderSaving = ref(false)
const reminders = ref<Awaited<ReturnType<typeof learningApi.listReminders>>>([])
/** 连续学习天数；不可用时为 null，整块隐藏而不是显示 0 天。 */
const streak = ref<LearningStreakView | null>(null)

/** Hero 计数：服务端可用就用服务端口径，否则用本地闪卡汇总。 */
const heroCount = computed(() => (learningAvailable.value ? dueSummaryCount(due.value) : localDue.value))
const heroActionable = computed(() =>
  learningAvailable.value ? hasDueWork(due.value) : localDue.value > 0,
)
const heroText = computed(() =>
  learningAvailable.value ? t(dueSummaryHeadlineKey(due.value)) : t('study.hero.cardsDue'),
)
const heroLabel = computed(() => t('study.hero.review'))

const dueRows = computed(() => {
  const d = due.value
  if (!d) return []
  return [
    { key: 'cards', icon: ICON.navStyle, value: d.dueCards, label: t('study.due.cardsDue') },
    { key: 'inbox', icon: ICON.studyArchive, value: d.inbox, label: t('study.due.inboxWaiting') },
    { key: 'review', icon: ICON.studyPsychology, value: d.reviewItems, label: t('study.due.reviewing') },
    { key: 'tasks', icon: ICON.moreChecklist, value: d.dueTasks, label: t('study.due.tasksDue') },
  ]
})

const REMINDER_PRESETS = ['08:00', '20:30', '22:00'] as const
const reminderPresets = REMINDER_PRESETS

const dailyDigestReminder = computed(() =>
  reminders.value.find((r) => r.kind === 'daily_digest') ?? null,
)
const dailyDigestTime = computed(() => dailyRuleTime(dailyDigestReminder.value))
const nextReminderAtValue = computed(() => nextReminderAt(reminders.value))
const nextReminderLabel = computed(() => formatClockTime(nextReminderAtValue.value))

/** 来源图标：名字全部来自 ICON 注册表，保证在 material-symbols 子集内。 */
function sourceIcon(kind: string): IconName {
  switch (kind) {
    case 'note':
      return ICON.studySourceNote
    case 'email':
      return ICON.studySourceEmail
    case 'rss':
      return ICON.studySourceRss
    case 'meeting':
      return ICON.studySourceMeeting
    case 'chat':
      return ICON.studySourceChat
    default:
      return ICON.studySourceDefault
  }
}

onMounted(async () => {
  // 不阻塞 UI：先同步拉 cache（sync），再后台 async sync
  try {
    store.loadFromCache()
  } catch {
    /* cache 损坏不致命：syncFromServer 会兜底 */
  }
  void store.syncFromServer().catch(() => {})
  void listNotes({ limit: 1 }).catch(() => {})
  await loadLearning()
})

async function loadLearning() {
  try {
    const summary = await learningApi.fetchDueSummary()
    due.value = summary
    learningAvailable.value = true
  } catch {
    // 503 / 离线：退回本地闪卡口径，不打扰用户。
    learningAvailable.value = false
    return
  }
  // 收件箱与提醒是次要信息，失败不阻断主流程。
  inboxLoading.value = true
  // 连续天数同样次要：拉不到就整块隐藏，绝不显示「0 天」——
  // 「没有连续记录」和「查不到」在界面上必须区分开。
  void learningApi
    .fetchStreak(learningApi.localUtcOffsetSeconds())
    .then((v) => {
      streak.value = v
    })
    .catch(() => {
      streak.value = null
    })
  try {
    inboxItems.value = await learningApi.listItems({ stage: 'inbox', limit: 5 })
  } catch {
    inboxItems.value = []
  } finally {
    inboxLoading.value = false
  }
  try {
    reminders.value = await learningApi.listReminders('pending', 20)
  } catch {
    reminders.value = []
  }
}

function goReview() {
  if (!heroActionable.value) return
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

// BUG-AA：空态原本是「新建牌组」按钮跳 /flashcards/new（新建卡片页）——
// 文案与行为不符，且从零状态进去必然撞 BUG-U 的死胡同。
// 改为与 FlashcardListView 同构的内联建组。store.createDeck 建完会把卡组合并进
// 本地缓存并 persistCache，因此 decks computed 立刻更新，空态自动消失。
const newDeckName = ref('')
const deckCreating = ref(false)
const deckError = ref('')

async function submitCreateDeck() {
  const name = newDeckName.value.trim()
  if (!name || deckCreating.value) return
  deckCreating.value = true
  deckError.value = ''
  try {
    await store.createDeck(name)
    newDeckName.value = ''
  } catch (err) {
    deckError.value = apiError(err, '加载学习卡片失败')
  } finally {
    deckCreating.value = false
  }
}

/** 收件箱条目「开始学习」：推进到 learning 阶段。 */
async function advance(item: LearningItem) {
  advancingId.value = item.id
  try {
    await learningApi.updateStage(item.id, 'learning')
    inboxItems.value = inboxItems.value.filter((i) => i.id !== item.id)
    if (due.value) due.value = { ...due.value, inbox: Math.max(0, due.value.inbox - 1) }
  } catch {
    // 失败保持原样：用户可以再点一次，不要在这里弹 toast 打断心流。
  } finally {
    advancingId.value = ''
  }
}

function startItem(item: LearningItem) {
  if (item.deckId) {
    router.push(`/flashcards/decks/${encodeURIComponent(item.deckId)}`)
    return
  }
  void advance(item)
}

/** 一键设置每日回顾时间。服务端按 (user, kind, itemId) 幂等 upsert。 */
async function setDailyDigest(time: string) {
  if (reminderSaving.value) return
  reminderSaving.value = true
  try {
    // nextDueAt 用「今天该时间的下一个时刻」：本地构造当天 HH:MM，
    // 已过则顺延到明天。服务端只校验它为正，具体的"下一次"由调度器算。
    const [hh, mm] = time.split(':').map(Number)
    const next = new Date()
    next.setHours(hh, mm, 0, 0)
    if (next.getTime() <= Date.now()) next.setDate(next.getDate() + 1)

    const saved = await learningApi.upsertReminder({
      kind: 'daily_digest',
      ruleKind: 'daily',
      ruleValue: time,
      nextDueAt: Math.floor(next.getTime() / 1000),
    })
    const idx = reminders.value.findIndex((r) => r.kind === 'daily_digest')
    if (idx >= 0) reminders.value[idx] = saved
    else reminders.value = [...reminders.value, saved]
  } catch {
    // 保持原值，界面不会显示一个其实没保存成功的选中态。
  } finally {
    reminderSaving.value = false
  }
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

/* ===== 连续学习天数 ===== */
.streak {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
  margin: 0 0 var(--space-2);
  padding: 4px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  font-size: 12px;
  color: var(--text-secondary);
}

.streak .material-symbols-outlined {
  font-size: 15px;
  color: #e8a33d;
  align-self: center;
}

.streak-num {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
}

.streak-next {
  color: var(--text-tertiary, #888);
}

/* ===== 今日明细 ===== */
.due-grid {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--space-2);
}

.due-cell {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  padding: var(--space-3) var(--space-1);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.due-cell.zero {
  opacity: 0.55;
}

.due-cell .material-symbols-outlined {
  font-size: 18px;
  color: var(--brand-primary, #4c8dff);
}

.due-value {
  font-size: 20px;
  font-weight: var(--font-weight-bold);
  color: var(--text-primary);
  font-family: var(--font-mono);
}

.due-label {
  font-size: 11px;
  color: var(--text-secondary);
  text-align: center;
  line-height: 1.3;
}

/* ===== 提醒 ===== */
.reminder-next {
  font-size: 12px;
  color: var(--text-secondary);
  font-family: var(--font-mono);
}

.reminder-offline {
  padding: var(--space-3);
  background: var(--bg-card);
  border: 1px dashed var(--border);
  border-radius: var(--radius-md);
  font-size: 12px;
  color: var(--text-tertiary);
}

.reminder-presets {
  display: flex;
  gap: var(--space-2);
}

.preset-btn {
  flex: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  padding: var(--space-3) var(--space-2);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-size: 13px;
  font-family: var(--font-mono);
  cursor: pointer;
  min-height: 44px;
}

.preset-btn .material-symbols-outlined {
  font-size: 16px;
  color: var(--text-tertiary);
}

.preset-btn.active {
  border-color: var(--brand-primary);
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  color: var(--brand-primary, #4c8dff);
  font-weight: var(--font-weight-semibold);
}

.preset-btn.active .material-symbols-outlined {
  color: var(--brand-primary, #4c8dff);
}

.preset-btn:disabled {
  opacity: 0.6;
  cursor: default;
}

/* ===== 收件箱 ===== */
.count-badge {
  padding: 1px var(--space-2);
  border-radius: 999px;
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  color: var(--brand-primary, #4c8dff);
  font-size: 12px;
  font-weight: var(--font-weight-semibold);
}

.inbox-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.inbox-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.inbox-icon {
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  color: var(--brand-primary, #4c8dff);
  flex-shrink: 0;
}

.inbox-icon .material-symbols-outlined {
  font-size: 18px;
}

.inbox-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  background: transparent;
  border: none;
  color: inherit;
  text-align: left;
  cursor: pointer;
  padding: 0;
}

.inbox-title {
  font-size: 14px;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 100%;
}

.inbox-meta {
  font-size: 11px;
  color: var(--text-tertiary);
}

.inbox-advance {
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-full);
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  flex-shrink: 0;
}

.inbox-advance .material-symbols-outlined {
  font-size: 18px;
}

.inbox-advance:disabled {
  opacity: 0.5;
  cursor: default;
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

/* BUG-AA：空态内联建卡组。样式与 FlashcardListView 的 .deck-create 保持一致。 */
.deck-create {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-3);
}
.deck-create input {
  flex: 1;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  color: var(--text-primary);
  font: inherit;
}
.deck-create .primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.error {
  margin: var(--space-3) 0 0;
  padding: var(--space-2);
  color: var(--danger);
  background: var(--danger-bg);
  border-radius: var(--radius-sm);
  font-size: 12px;
}

.state {
  padding: var(--space-2);
}

.state .material-symbols-outlined {
  font-size: 18px;
}
</style>
