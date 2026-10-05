<template>
  <div class="page calendar-page" :class="{ 'is-embedded': embedded }">
    <!--
      嵌入「消息」tab 时**不**注入标题栏。
      HeaderActionsPortal 的注入点 #app-header-actions 是全 App 唯一的一个 DOM 节点，
      同页挂两份不是「两份并存」，而是两个 slot 抢同一个容器、按钮并排挤在一起。
      所以动作上收到宿主：MessagesHubView 在「日历」分段下渲染「新建日程」，
      再通过本组件 expose 出去的 openCreate 触发。独立 /calendar 路由仍走原路径。
    -->
    <HeaderActionsPortal v-if="!embedded">
      <button
        type="button"
        class="header-action"
        :aria-label="t('calendar.action.newEvent')"
        @click="openCreate()"
      >
        <span class="material-symbols-outlined">add</span>
      </button>
    </HeaderActionsPortal>

    <!-- 月份导航：上一个 / 今天 / 下一个 -->
    <header class="cal-toolbar">
      <div class="cal-toolbar-left">
        <button type="button" class="icon-btn" :aria-label="t('calendar.action.prevMonth')" @click="step(-1)">
          <span class="material-symbols-outlined">chevron_left</span>
        </button>
        <h1 class="cal-title">{{ monthLabel }}</h1>
        <button type="button" class="icon-btn" :aria-label="t('calendar.action.nextMonth')" @click="step(1)">
          <span class="material-symbols-outlined">chevron_right</span>
        </button>
      </div>
      <button type="button" class="today-btn" @click="goToday">{{ t('calendar.action.today') }}</button>
    </header>

    <!-- 来源筛选：勾掉即隐藏。颜色之外同时有文字与图标，不靠颜色单独表意。 -->
    <div class="cal-filters" role="group" :aria-label="t('calendar.filter.label')">
      <button
        v-for="source in store.allSources"
        :key="source"
        type="button"
        class="filter-chip"
        :class="{ off: !store.isSourceVisible(source) }"
        :aria-pressed="store.isSourceVisible(source)"
        @click="store.toggleSource(source)"
      >
        <span class="dot" :style="{ background: sourceColor(source) }"></span>
        <span class="material-symbols-outlined filter-icon">{{ sourceIcon(source) }}</span>
        <span>{{ sourceLabel(source) }}</span>
      </button>
    </div>

    <p v-if="store.error" class="error" role="alert">
      {{ apiError(store.error, 'errors.loadCalendarFailed') }}
      <button type="button" @click="reload">{{ t('common.retry') }}</button>
    </p>

    <!-- 星期表头 -->
    <div class="cal-weekdays" aria-hidden="true">
      <span v-for="label in weekdayLabels" :key="label">{{ label }}</span>
    </div>

    <!-- 月宫格：固定 42 格，翻月不跳高度 -->
    <div class="cal-grid" role="grid" :aria-label="monthLabel">
      <button
        v-for="cell in grid"
        :key="cell.dayKey"
        type="button"
        role="gridcell"
        class="cal-cell"
        :class="{
          muted: !cell.inMonth,
          today: cell.isToday,
          selected: cell.dayKey === selectedDay,
        }"
        :aria-selected="cell.dayKey === selectedDay"
        :aria-label="dayAria(cell.dayKey)"
        @click="selectDay(cell.dayKey)"
      >
        <span class="day-num">{{ cell.dayOfMonth }}</span>
        <span class="chips">
          <span
            v-for="chip in chipsFor(cell.dayKey)"
            :key="chip.id"
            class="chip"
            :style="{ background: sourceColor(chip.source) }"
            :title="chip.title"
          >
            <span class="chip-title">{{ chip.title }}</span>
          </span>
          <span v-if="chipOverflow(cell.dayKey) > 0" class="chip more">
            +{{ chipOverflow(cell.dayKey) }}
          </span>
        </span>
      </button>
    </div>

    <!-- 选中日的清单：手机上这是最常被读的一屏，所以详情用列表而不是时间轴 -->
    <section class="day-detail" :aria-label="selectedDayLabel">
      <h2 class="day-detail-title">{{ selectedDayLabel }}</h2>

      <p v-if="store.loading" class="state">{{ t('common.loading') }}</p>
      <p v-else-if="selectedEntries.length === 0" class="state">
        {{ t('calendar.empty.day') }}
      </p>

      <ul v-else class="day-list">
        <li v-for="item in selectedEntries" :key="item.entry.id" class="day-item">
          <span class="item-bar" :style="{ background: sourceColor(item.entry.source) }"></span>
          <!-- 用真 <button> 而不是给 <li> 挂 @click：键盘可达性与回车激活
               免费就有，不必再补 role/tabindex/keydown（见 email 卡片那次的教训）。 -->
          <button
            type="button"
            class="item-body item-open"
            :aria-label="itemAria(item.entry)"
            @click="openEntry(item.entry)"
          >
            <p class="item-title" :class="{ done: item.entry.done }">
              <span class="material-symbols-outlined item-icon">{{ sourceIcon(item.entry.source) }}</span>
              {{ item.entry.title }}
            </p>
            <p class="item-meta">
              <span>{{ item.timeLabel }}</span>
              <span class="item-source">{{ sourceLabel(item.entry.source) }}</span>
              <span v-if="item.entry.done" class="item-done">{{ t('calendar.badge.done') }}</span>
            </p>
          </button>
          <button
            v-if="item.entry.source === 'event'"
            type="button"
            class="icon-btn subtle"
            :aria-label="t('calendar.action.deleteEvent')"
            @click="removeEvent(item.entry.refId)"
          >
            <span class="material-symbols-outlined">delete</span>
          </button>
        </li>
      </ul>

      <button type="button" class="add-row" @click="openCreate()">
        <span class="material-symbols-outlined">add</span>
        {{ t('calendar.action.addOnDay') }}
      </button>
    </section>

    <!-- 新建/编辑事件面板 -->
    <CalendarEventSheet
      v-if="sheetOpen"
      :initial-day="selectedDay"
      :event="editing"
      @close="closeSheet"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import CalendarEventSheet from './CalendarEventSheet.vue'
import { useCalendarStore } from './store'
import { SOURCE_META, MAX_CHIPS_PER_CELL, type CalendarEntry, type CalendarSource } from './types'
import {
  dayKey,
  deviceTimeZone,
  monthKey,
  parseDayKey,
  shiftMonth,
  type DayKey,
} from './calendar-math.ts'
import { bucketForDay } from './calendar-feed.ts'
// 呈现层逻辑全部走这个已测模块；组件里不再留一份区间判断
// （组件里那份用闭区间，跨天条目会重复出现在相邻天）。
import {
  chipTimeLabel,
  chipsForDay,
  dayTitle,
  fetchWindowForMonth,
  monthTitle,
  resolveSelectedDay,
  weekdayLabels as buildWeekdayLabels,
} from './calendar-view.ts'
import { useApiError } from '../../composables/useApiError'

const { t } = useI18n()
const router = useRouter()
const apiError = useApiError()
const store = useCalendarStore()

/**
 * 嵌入模式：日历作为「消息」tab 的第二个分段被挂进来。
 *
 * 只关掉标题栏注入与外层内边距，月宫格 / 当日议程 / 编辑面板全部原样复用。
 * 状态不隔离也不需要隔离——useCalendarStore 是 Pinia 单例，嵌在 tab 里和
 * 独立 /calendar 路由用的是**同一份** entries 与筛选，来回切不会丢当前月份。
 */
const props = withDefaults(defineProps<{ embedded?: boolean }>(), { embedded: false })
const embedded = computed(() => props.embedded)

const tz = deviceTimeZone()
/** 今天每天都在走；用定时器对齐本地午夜，否则应用跨过零点后「今天」会停在昨天。 */
const today = ref(dayKey(Math.floor(Date.now() / 1000), tz))
const currentMonth = ref(monthKey(
  parseDayKey(today.value).year,
  parseDayKey(today.value).month,
))
const selectedDay = ref<DayKey>(today.value)
const sheetOpen = ref(false)
const editing = ref<CalendarEntry | null>(null)

const grid = computed(() => fetchWindowForMonth(currentMonth.value, tz, today.value).grid)

const monthLabel = computed(() => monthTitle(currentMonth.value))

const weekdayLabels = buildWeekdayLabels()

const selectedDayLabel = computed(() => dayTitle(selectedDay.value))

const dayAria = (key: DayKey): string => {
  const count = store.countOn(key)
  return `${key}${count > 0 ? ` · ${t('calendar.aria.entriesCount', count)}` : ''}`
}

function chipsFor(day: DayKey): CalendarEntry[] {
  return chipsForDay(store.visibleEntries, day, tz, MAX_CHIPS_PER_CELL).visible
}

function chipOverflow(day: DayKey): number {
  return chipsForDay(store.visibleEntries, day, tz, MAX_CHIPS_PER_CELL).overflow
}

const selectedEntries = computed(() => {
  const bucket = bucketForDay(store.visibleEntries, selectedDay.value, tz)
  const labels = { allDay: t('calendar.label.allDay'), dueBy: t('calendar.label.dueBy') }
  return [...bucket.allDay, ...bucket.timed].map((entry) => ({
    entry,
    timeLabel: chipTimeLabel(entry, tz, labels),
  }))
})

function sourceColor(source: CalendarSource): string {
  return SOURCE_META[source]?.color ?? 'var(--brand-primary)'
}

function sourceIcon(source: CalendarSource): string {
  return SOURCE_META[source]?.materialIcon ?? 'event'
}

function sourceLabel(source: CalendarSource): string {
  return t(`calendar.source.${source}`)
}

/** 取数窗口覆盖整张 42 格宫格 —— 含相邻月补白，否则翻到月末会看到空白相邻月。 */
async function fetchMonth(month = currentMonth.value, force = false): Promise<void> {
  // 窗口覆盖整张 42 格宫格（含相邻月补白），否则月末那几格永远是空的。
  const { from, to } = fetchWindowForMonth(month, tz, today.value)
  await store.load(from, to, force)
}

function step(delta: number): void {
  currentMonth.value = shiftMonth(currentMonth.value, delta)
}

function goToday(): void {
  today.value = dayKey(Math.floor(Date.now() / 1000), tz)
  const civil = parseDayKey(today.value)
  currentMonth.value = monthKey(civil.year, civil.month)
  selectedDay.value = today.value
}

function selectDay(day: DayKey): void {
  // 点到相邻月补白格时跟着翻月，否则选中日在当前视图之外，界面看起来没反应。
  const next = resolveSelectedDay(day, currentMonth.value, grid.value)
  selectedDay.value = next.selectedDay
  currentMonth.value = next.month
}

function reload(): void {
  void fetchMonth(currentMonth.value, true)
}

function openCreate(): void {
  editing.value = null
  sheetOpen.value = true
}

/**
 * 供宿主（MessagesHubView）在嵌入态驱动「新建日程」。
 * 页面内另有「在这天添加日程」行，所以这只是一条快捷路径，不是唯一入口。
 */
defineExpose({ openCreate })

/**
 * 点条目时去哪儿。
 *
 * 三类来源的归属不同，不能一律开编辑面板：
 *  · 日程事件由日历拥有 → 开编辑面板（改的是真数据）。
 *  · 任务 / 定时任务由各自的域拥有 → **跳转到它真正的详情页**。
 *    日历只是读它们；在这里给它们开一个编辑面板会写进日历自己的表，
 *    用户改了任务却在任务列表里看不到变化 —— 那就是一份影子副本。
 *    这也是 `refId` 一路带到 UI 的意义：点得回源头。
 */
function openEntry(entry: CalendarEntry): void {
  if (entry.source === 'event') {
    editing.value = entry
    sheetOpen.value = true
    return
  }
  if (entry.source === 'task') {
    void router.push(`/tasks/${encodeURIComponent(entry.refId)}`)
    return
  }
  void router.push(`/settings/scheduled-tasks/${encodeURIComponent(entry.refId)}`)
}

function itemAria(entry: CalendarEntry): string {
  const action = entry.source === 'event'
    ? t('calendar.action.editEvent')
    : t('calendar.action.openSource')
  return `${action}：${entry.title}`
}

function closeSheet(): void {
  sheetOpen.value = false
  editing.value = null
}

async function removeEvent(id: string): Promise<void> {
  await store.deleteEvent(id)
}

watch(currentMonth, (month) => {
  void fetchMonth(month)
})

onMounted(() => {
  void fetchMonth(currentMonth.value)
  // 每分钟校正一次「今天」：应用长时间挂在后台跨过零点时也能跟上。
  const timer = setInterval(() => {
    const fresh = dayKey(Math.floor(Date.now() / 1000), tz)
    if (fresh !== today.value) today.value = fresh
  }, 60000)
  return () => clearInterval(timer)
})
</script>

<style scoped>
.calendar-page {
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

/*
  嵌进 MessagesHubView 时，宿主（.msg-hub）已经有 `padding: 0 var(--space-3)`。
  两层一起生效就是左右各 24px，月宫格在 360dp 上被压到不足三列宽 —— 所以嵌入态
  把自己的 padding 交出去。这里不靠 `:deep` 或父选择器，直接由 embedded prop 驱动，
  避免把「谁负责留白」这件事拆到两个文件里互相猜。
*/
.calendar-page.is-embedded {
  padding: 0;
}

.cal-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.cal-toolbar-left {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.cal-title {
  font-size: 1.05rem;
  font-weight: 600;
  margin: 0;
  /* 月份标题可能很长（多语言下尤甚），省略而不是把工具栏挤变形 */
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 40px;
  min-height: 40px;
  border: none;
  border-radius: 10px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}

.icon-btn:active { background: var(--bg-subtle); }
.icon-btn.subtle { min-width: 36px; min-height: 36px; color: var(--text-muted); }

.today-btn {
  min-height: 36px;
  padding: 0 14px;
  border-radius: 999px;
  border: 1px solid var(--border-strong);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: 0.85rem;
  cursor: pointer;
}

.cal-filters {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.filter-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 32px;
  padding: 0 10px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: 0.78rem;
  cursor: pointer;
}

.filter-chip.off { opacity: 0.45; }
.filter-chip .dot { width: 8px; height: 8px; border-radius: 50%; }
.filter-icon { font-size: var(--text-md); }

.cal-weekdays {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  font-size: 0.72rem;
  color: var(--text-muted);
  text-align: center;
}

.cal-grid {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  /* 42 格固定高度：翻月不发生布局跳动 */
  grid-auto-rows: minmax(62px, auto);
  gap: 2px;
}

.cal-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 3px;
  border: 1px solid transparent;
  border-radius: 8px;
  background: var(--bg-card);
  text-align: left;
  overflow: hidden;
  cursor: pointer;
}

.cal-cell.muted .day-num { color: var(--text-muted); }
.cal-cell.today { border-color: var(--brand-primary); }
.cal-cell.selected { background: var(--brand-bg); }
.cal-cell.selected.today { border-width: 2px; }

.day-num {
  font-size: 0.78rem;
  color: var(--text-primary);
  font-weight: 500;
  align-self: flex-start;
  min-width: 20px;
  text-align: center;
}

.cal-cell.today .day-num {
  background: var(--brand-primary);
  color: var(--text-inverse);
  border-radius: 999px;
}

.chips {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.chip {
  display: block;
  padding: 1px 3px;
  border-radius: 3px;
  font-size: 0.62rem;
  line-height: 1.3;
  color: #fff;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chip-title { pointer-events: none; }
.chip.more { background: var(--bg-subtle); color: var(--text-secondary); text-align: center; }

.day-detail {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.day-detail-title { font-size: 0.95rem; margin: 0; }

.day-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }

.day-item {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.item-bar { width: 3px; align-self: stretch; border-radius: 2px; flex: none; }
.item-body { flex: 1; min-width: 0; }
.item-open {
  display: block;
  width: 100%;
  text-align: start;
  border: none;
  background: transparent;
  padding: 4px 6px;
  margin: -4px -6px;
  border-radius: 8px;
  font: inherit;
  color: inherit;
  cursor: pointer;
}
.item-open:active { background: var(--overlay-subtle); }

.item-title {
  margin: 0;
  font-size: 0.88rem;
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.item-title.done { text-decoration: line-through; color: var(--text-muted); }
.item-icon { font-size: var(--text-md); color: var(--text-secondary); flex: none; }

.item-meta {
  margin: 0;
  font-size: 0.72rem;
  color: var(--text-muted);
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.item-source { color: var(--text-secondary); }
.item-done { color: var(--success); }

.add-row {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  min-height: 40px;
  border: 1px dashed var(--border-strong);
  border-radius: 10px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 0.85rem;
  cursor: pointer;
}

.state, .error { color: var(--text-secondary); font-size: 0.85rem; margin: 0; }
.error { color: var(--error); }
.error button { margin-inline-start: 8px; }
</style>