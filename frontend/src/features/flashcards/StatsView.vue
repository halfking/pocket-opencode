<template>
  <section class="page">
    <header class="head">
      <button type="button" class="back-btn" aria-label="back" @click="goBack">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1>{{ t('flashcards.stats.title') }}</h1>
      <button
        type="button"
        class="range-btn"
        :class="{ active: rangeDays === 7 }"
        @click="rangeDays = 7"
      >7d</button>
      <button
        type="button"
        class="range-btn"
        :class="{ active: rangeDays === 30 }"
        @click="rangeDays = 30"
      >30d</button>
    </header>

    <main class="content">
      <!-- Summary cards -->
      <section class="summary">
        <article class="stat">
          <span class="num">{{ summary.total }}</span>
          <span class="label">{{ t('flashcards.stats.total') }}</span>
        </article>
        <article class="stat">
          <span class="num">{{ summary.mature }}</span>
          <span class="label">{{ t('flashcards.stats.mature') }}</span>
        </article>
        <article class="stat">
          <span class="num">{{ summary.young }}</span>
          <span class="label">{{ t('flashcards.stats.young') }}</span>
        </article>
        <article class="stat">
          <span class="num">{{ summary.newCount }}</span>
          <span class="label">{{ t('flashcards.stats.new') }}</span>
        </article>
      </section>

      <!-- 饼图：卡片状态分布 -->
      <section class="chart-card">
        <h3>{{ t('flashcards.stats.distribution') }}</h3>
        <div class="pie-row">
          <svg viewBox="0 0 100 100" class="pie" :aria-label="t('flashcards.stats.distribution')">
            <circle cx="50" cy="50" r="40" fill="var(--bg-subtle)" />
            <template v-for="(seg, i) in pieSegments" :key="i">
              <circle
                cx="50"
                cy="50"
                r="40"
                fill="transparent"
                :stroke="seg.color"
                stroke-width="20"
                :stroke-dasharray="`${seg.length} ${circumference}`"
                :stroke-dashoffset="seg.offset"
                transform="rotate(-90 50 50)"
              />
            </template>
            <text x="50" y="48" text-anchor="middle" class="pie-text">{{ summary.total }}</text>
            <text x="50" y="62" text-anchor="middle" class="pie-subtext">{{ t('flashcards.stats.total') }}</text>
          </svg>
          <ul class="legend">
            <li v-for="(seg, i) in pieSegments" :key="i">
              <span class="swatch" :style="{ background: seg.color }" />
              <span>{{ seg.label }}</span>
              <span class="count">{{ seg.value }}</span>
            </li>
          </ul>
        </div>
      </section>

      <!-- 折线图：每日复习数 -->
      <section class="chart-card">
        <h3>{{ t('flashcards.stats.reviewsPerDay', { days: rangeDays }) }}</h3>
        <svg
          viewBox="0 0 300 120"
          class="line"
          preserveAspectRatio="none"
          :aria-label="t('flashcards.stats.reviewsPerDay', { days: rangeDays })"
        >
          <!-- baseline grid -->
          <line x1="0" y1="100" x2="300" y2="100" stroke="var(--border)" stroke-width="1" />
          <line x1="0" y1="60" x2="300" y2="60" stroke="var(--bg-subtle)" stroke-width="1" stroke-dasharray="2 4" />
          <!-- area + line -->
          <path v-if="reviewPath" :d="reviewAreaPath" class="area" />
          <path v-if="reviewPath" :d="reviewPath" class="line-path" />
          <!-- last point -->
          <circle v-if="lastReviewPoint" :cx="lastReviewPoint.x" :cy="lastReviewPoint.y" r="3" class="line-dot" />
          <!-- labels -->
          <text v-if="reviewMax > 0" x="0" y="14" class="axis-label">{{ reviewMax }}</text>
          <text x="0" y="114" class="axis-label">{{ dayLabelStart }}</text>
          <text x="300" y="114" text-anchor="end" class="axis-label">{{ dayLabelEnd }}</text>
        </svg>
        <p v-if="reviewTotalCount === 0" class="empty-line">{{ t('flashcards.stats.noReviewsYet') }}</p>
      </section>

      <!-- 折线图：每日 lapse 次数 -->
      <section class="chart-card">
        <h3>{{ t('flashcards.stats.lapsesPerDay', { days: rangeDays }) }}</h3>
        <svg
          viewBox="0 0 300 120"
          class="line"
          preserveAspectRatio="none"
          :aria-label="t('flashcards.stats.lapsesPerDay', { days: rangeDays })"
        >
          <line x1="0" y1="100" x2="300" y2="100" stroke="var(--border)" stroke-width="1" />
          <line v-if="lapseMax > 0" x1="0" y1="60" x2="300" y2="60" stroke="var(--bg-subtle)" stroke-width="1" stroke-dasharray="2 4" />
          <path v-if="lapsePath" :d="lapsePath" class="line-path lapse" />
          <text v-if="lapseMax > 0" x="0" y="14" class="axis-label">{{ lapseMax }}</text>
          <text x="0" y="114" class="axis-label">{{ dayLabelStart }}</text>
          <text x="300" y="114" text-anchor="end" class="axis-label">{{ dayLabelEnd }}</text>
        </svg>
        <p v-if="lapseTotalCount === 0" class="empty-line">{{ t('flashcards.stats.noLapses') }}</p>
      </section>

      <!-- 综合 retention 估算 -->
      <section class="chart-card">
        <h3>{{ t('flashcards.stats.retention') }}</h3>
        <div class="retention-row">
          <div class="retention-bar">
            <div class="retention-fill" :style="{ width: estimatedRetention + '%' }" />
          </div>
          <span class="retention-num">{{ estimatedRetention.toFixed(1) }}%</span>
        </div>
        <p class="retention-hint">
          t('flashcards.stats.retentionHint', {
            again: ratingBreakdown.again,
            hard: ratingBreakdown.hard,
            good: ratingBreakdown.good,
            easy: ratingBreakdown.easy,
          })
        </p>
      </section>
    </main>
  </section>
</template>

<script setup lang="ts">
/**
 * StatsView —— 复习统计（Anki 风格）。
 *
 * 路由：`/flashcards/stats`
 *
 * 图表：
 *   - 饼图：卡片状态分布（new / young / mature）
 *   - 折线图：每日复习数（最近 7/30 天）
 *   - 折线图：每日 lapse 数（Again 评分）
 *   - 横向 bar：综合 retention 估算
 *
 * 数据源：
 *   - cards 实时计算（不需要 reviewLogs）
 *   - reviewLogs 每日复习/lapse 计数
 *
 * 实现：纯 SVG，无外部图表库。
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useFlashcardsStore } from '../../stores/flashcards'
import type { FlashcardState } from '../../types/flashcards'

defineOptions({ name: 'StatsView' })

const { t } = useI18n()
const router = useRouter()
const store = useFlashcardsStore()

const rangeDays = ref<7 | 30>(7)

const SECONDS_PER_DAY = 86400

/* 0 = new, 1 = learning, 2 = review, 3 = relearning */
const STATE_MATURE_THRESHOLD_DAYS = 21 /* Anki 标准：intervalDays ≥ 21 = mature */

/* ===== 派生统计 ===== */
const summary = computed(() => {
  let total = 0
  let newCount = 0
  let young = 0
  let mature = 0
  for (const c of store.cards) {
    if (c.deletedAt) continue
    if (c.state === 0) {
      newCount += 1
      continue
    }
    total += 1
    if (c.state === 2 && c.intervalDays >= STATE_MATURE_THRESHOLD_DAYS) mature += 1
    else young += 1
  }
  return { total, newCount, mature, young }
})

const pieSegments = computed(() => {
  const segs: Array<{ label: string; value: number; color: string; length: number; offset: number }> = []
  const total = summary.value.total + summary.value.newCount
  if (total === 0) return segs
  const data: Array<{ label: string; value: number; color: string }> = [
    { label: t('flashcards.stats.new'), value: summary.value.newCount, color: 'var(--brand-primary)' },
    { label: t('flashcards.stats.young'), value: summary.value.young, color: '#f59e0b' },
    { label: t('flashcards.stats.mature'), value: summary.value.mature, color: 'var(--success, #16a34a)' },
  ]
  const circumference = 2 * Math.PI * 40
  let cursor = 0
  for (const d of data) {
    const len = (d.value / total) * circumference
    segs.push({ ...d, length: len, offset: -cursor })
    cursor += len
  }
  return segs
})

const circumference = 2 * Math.PI * 40

/* ===== 时间窗统计（每日复习/lapse） ===== */
const dayBuckets = computed(() => {
  const now = Math.floor(Date.now() / 1000)
  const days = rangeDays.value
  const buckets = new Array(days).fill(0)
  for (const log of store.reviewLogs) {
    const ageDays = Math.floor((now - log.reviewedAt) / SECONDS_PER_DAY)
    if (ageDays < 0 || ageDays >= days) continue
    buckets[days - 1 - ageDays] += 1
  }
  return buckets
})

const lapseBuckets = computed(() => {
  const now = Math.floor(Date.now() / 1000)
  const days = rangeDays.value
  const buckets = new Array(days).fill(0)
  for (const log of store.reviewLogs) {
    if (log.rating !== 1) continue /* Again only */
    const ageDays = Math.floor((now - log.reviewedAt) / SECONDS_PER_DAY)
    if (ageDays < 0 || ageDays >= days) continue
    buckets[days - 1 - ageDays] += 1
  }
  return buckets
})

const reviewTotalCount = computed(() => dayBuckets.value.reduce((a, b) => a + b, 0))
const lapseTotalCount = computed(() => lapseBuckets.value.reduce((a, b) => a + b, 0))

const reviewMax = computed(() => Math.max(1, ...dayBuckets.value))
const lapseMax = computed(() => Math.max(1, ...lapseBuckets.value))

function buildPath(buckets: number[], max: number): string {
  if (max === 0) return ''
  const W = 300
  const H = 100
  const step = W / Math.max(1, buckets.length - 1)
  return buckets
    .map((v, i) => {
      const x = i * step
      const y = H - (v / max) * H
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

const reviewPath = computed(() => buildPath(dayBuckets.value, reviewMax.value))
const reviewAreaPath = computed(() => {
  if (!reviewPath.value) return ''
  return `${reviewPath.value} L300,100 L0,100 Z`
})
const lapsePath = computed(() => buildPath(lapseBuckets.value, lapseMax.value))

const lastReviewPoint = computed(() => {
  if (reviewMax.value <= 0 || dayBuckets.value.length === 0) return null
  const last = dayBuckets.value[dayBuckets.value.length - 1]
  const x = 300
  const y = 100 - (last / reviewMax.value) * 100
  return { x, y }
})

/* ===== 评分分布 + retention 估算 ===== */
const ratingBreakdown = computed(() => {
  const counts = { again: 0, hard: 0, good: 0, easy: 0 }
  for (const log of store.reviewLogs) {
    if (log.rating === 1) counts.again += 1
    else if (log.rating === 2) counts.hard += 1
    else if (log.rating === 3) counts.good += 1
    else if (log.rating === 4) counts.easy += 1
  }
  return counts
})

/* 简单估算：Again / 总数 的补值（越小越好；Anki 用真实 FSRS 期望稳定度，
   Phase 5 简化用经验公式）。 */
const estimatedRetention = computed(() => {
  const total = ratingBreakdown.value.again + ratingBreakdown.value.hard + ratingBreakdown.value.good + ratingBreakdown.value.easy
  if (total === 0) return 0
  const correct = total - ratingBreakdown.value.again - ratingBreakdown.value.hard * 0.5
  return Math.max(0, Math.min(100, (correct / total) * 100))
})

/* ===== 时间窗标签 ===== */
const dayLabelStart = computed(() => formatDayAgo(rangeDays.value - 1))
const dayLabelEnd = computed(() => formatDayAgo(0))

function formatDayAgo(daysAgo: number): string {
  const d = new Date(Date.now() - daysAgo * SECONDS_PER_DAY * 1000)
  return `${d.getMonth() + 1}/${d.getDate()}`
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
  gap: var(--space-2);
  padding: var(--space-4);
}
.head h1 { flex: 1; margin: 0; font-size: 18px; color: var(--text-primary); }
.back-btn {
  border: 0;
  background: transparent;
  color: var(--text-primary);
  padding: 6px;
  border-radius: 999px;
  cursor: pointer;
}

.range-btn {
  padding: 6px 10px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-secondary);
  border-radius: var(--radius-sm);
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
}
.range-btn.active {
  border-color: var(--brand-primary);
  color: var(--brand-primary);
  background: var(--brand-bg);
}

.content {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: 0 var(--space-4) 100px;
}

.summary {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--space-2);
}

.stat {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-3) var(--space-2);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  text-align: center;
}

.stat .num {
  font-size: 22px;
  font-weight: var(--font-weight-bold);
  color: var(--text-primary);
  line-height: 1;
}

.stat .label {
  margin-top: 4px;
  font-size: 11px;
  color: var(--text-tertiary);
}

.chart-card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.chart-card h3 {
  margin: 0;
  font-size: 13px;
  font-weight: var(--font-weight-semibold);
  color: var(--text-secondary);
}

.pie-row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.pie {
  width: 120px;
  height: 120px;
  flex-shrink: 0;
}

.pie-text {
  font-size: 18px;
  font-weight: var(--font-weight-bold);
  fill: var(--text-primary);
}

.pie-subtext {
  font-size: 8px;
  fill: var(--text-tertiary);
}

.legend {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex: 1;
}

.legend li {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 13px;
  color: var(--text-primary);
}

.legend .swatch {
  display: inline-block;
  width: 12px;
  height: 12px;
  border-radius: 3px;
  flex-shrink: 0;
}

.legend .count {
  margin-left: auto;
  font-weight: var(--font-weight-semibold);
}

.line {
  width: 100%;
  height: 120px;
}

.line-path {
  fill: none;
  stroke: var(--brand-primary);
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.line-path.lapse {
  stroke: var(--danger, #ef4444);
}

.area {
  fill: rgba(76, 141, 255, 0.12);
  stroke: none;
}

.line-dot {
  fill: var(--brand-primary);
}

.axis-label {
  font-size: 9px;
  fill: var(--text-tertiary);
}

.empty-line {
  margin: 0;
  font-size: 12px;
  color: var(--text-tertiary);
  text-align: center;
}

.retention-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.retention-bar {
  flex: 1;
  height: 10px;
  background: var(--bg-subtle);
  border-radius: 999px;
  overflow: hidden;
}

.retention-fill {
  height: 100%;
  background: var(--brand-gradient, linear-gradient(90deg, var(--brand-primary), var(--success)));
  transition: width 0.4s ease;
}

.retention-num {
  font-size: 18px;
  font-weight: var(--font-weight-bold);
  color: var(--text-primary);
  min-width: 64px;
  text-align: right;
}

.retention-hint {
  margin: 0;
  font-size: 11px;
  color: var(--text-tertiary);
  line-height: 1.4;
}
</style>