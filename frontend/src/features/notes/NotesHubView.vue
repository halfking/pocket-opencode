<!--
  NotesHubView —— 「笔记」tab（2026-10-03 全局 IA 重组）。

  ## 重组动机

  重组前底部导航是：首页 · 学习 · 会议 · 更多。「会议」独占一个一级 tab，但它
  产出的东西**就是笔记**——一场会议跑完录音 → 转写 → AI 纪要，落地物是一份会议
  纪要，用户接着做的动作是「读它 / 改它 / 从它派生任务」，与手记完全同构。
  让「会议」和「笔记」各占一个 tab，等于把**同一种对象的两个阶段**拆成两个入口，
  用户要先想「我这次要记的东西属于会议还是笔记」才能决定点哪个。

  重组后：会议作为笔记的一种**来源**下沉到这里，与手记、PKM 笔记并列，
  由顶部 chips 切换。来源是**筛选维度**，不是**导航目的地**——这是本次重组
  在交互上的核心转变。

  ## 为什么用「chips + 统一流」而不是「分段控件 + 两个子页」

  用户来这个 tab 的动作是「把最近沉淀的东西过一遍」。分段控件意味着看完手记
  要再点一下才能看会议，两类内容被时间线割裂，无法按「最近」统一浏览。
  chips + 统一流让「全部」这一档就是真正的时间倒序流，跨来源可比。

  ## 数据源与降级

  三个来源全部是本地加密库读（local_notes / local_meetings / local_assets），
  因此**一起降级**：库未解锁时整页走 DbLockedState，不做逐源静默失败——
  逐源失败会出现「手记 0 条」的假空态，用户会以为笔记丢了。

  路由：`/notes`；进入：BottomNav 4 tab 的「笔记」入口。
  详情仍走独立深链（/notes/:id、/meetings/:id、/pkm/n/:id），分享与历史不受影响。
-->
<template>
  <div class="notes-hub">
    <DbLockedState
      v-if="dbNotReady"
      :hint="t('notesHub.title')"
      @relogin="router.push('/login')"
    />

    <template v-else>
      <HeaderActionsPortal>
        <button
          class="hdr-action primary"
          type="button"
          :aria-label="t('notesHub.action.startMeeting')"
          data-testid="notes-hub-start-meeting"
          @click="go('/meetings/new')"
        >
          <span class="material-symbols-outlined" aria-hidden="true">mic</span>
        </button>
        <button
          class="hdr-action"
          type="button"
          :aria-label="t('notesHub.action.newNote')"
          data-testid="notes-hub-new-note"
          @click="go('/notes/new')"
        >
          <span class="material-symbols-outlined" aria-hidden="true">add</span>
        </button>
      </HeaderActionsPortal>

      <ScrollChromePortal>
        <SourceFilterBar
          v-model="source"
          :options="sourceOptions"
          :aria-label="t('notesHub.title')"
        />
      </ScrollChromePortal>

      <div v-if="loading" class="state">
        <Skeleton :count="4" />
      </div>

      <template v-else>
        <!-- 概览条：只统计「加载成功」的来源，三个计数与下面的列表是同一份数据，
             避免出现「数字说 12 条、列表只有 5 条」这种自相矛盾的界面。 -->
        <!-- 概览只列手记与会议：PKM 的量级通常与前两者不同量纲，
             把它塞进同一行会让「0 手记 · 0 会议」在纯 PKM 用户眼里变成
             「我是空的」。PKM 的入口在下方 see-all 里。 -->
        <p v-if="rows.length" class="overview" data-testid="notes-hub-overview">
          <span>{{ t('notesHub.count.notes', { count: counts.note }) }}</span>
          <span class="dot" aria-hidden="true">·</span>
          <span>{{ t('notesHub.count.meetings', { count: counts.meeting }) }}</span>
        </p>

        <ul v-if="rows.length" class="row-list" data-testid="notes-hub-list">
          <li v-for="row in rows" :key="`${row.kind}:${row.id}`">
            <button class="row" type="button" @click="go(row.to)">
              <span class="row-icon" :class="row.kind" aria-hidden="true">
                <span class="material-symbols-outlined">{{ sourceIcon(row.kind) }}</span>
              </span>
              <span class="row-main">
                <span class="row-title">{{ row.title }}</span>
                <span v-if="row.preview" class="row-preview">{{ row.preview }}</span>
                <span class="row-meta">
                  <span class="row-source">{{ t(`notesHub.filter.${row.sourceKey}`) }}</span>
                  <span v-if="row.voice" class="row-flag">
                    <span class="material-symbols-outlined" aria-hidden="true">mic</span>
                    {{ t('notesHub.meta.voice') }}
                  </span>
                  <span class="dot" aria-hidden="true">·</span>
                  <span>{{ formatRelative(row.ts, t) }}</span>
                  <template v-if="row.kind === 'meeting' && row.duration">
                    <span class="dot" aria-hidden="true">·</span>
                    <span>{{ durationLabel(row.duration) }}</span>
                  </template>
                  <template v-if="row.pending">
                    <span class="dot" aria-hidden="true">·</span>
                    <span class="row-pending">{{ t('notesHub.meta.pending') }}</span>
                  </template>
                </span>
              </span>
              <span class="material-symbols-outlined row-chevron" aria-hidden="true">chevron_right</span>
            </button>
          </li>
        </ul>

        <!-- 区分「一个来源都没有」和「当前筛选下没有」：后者要点一下 chips 就有了，
             前者要引导去创建。共用一个空态会让用户在筛选无结果时以为数据丢了。 -->
        <EmptyState
          v-else-if="counts.total === 0"
          icon="📝"
          :title="t('notesHub.empty.title')"
          :hint="t('notesHub.empty.hint')"
          :action-label="t('notesHub.action.record')"
          size="sm"
          @action="go('/notes/new')"
        />
        <EmptyState v-else icon="🔍" :title="t('notesHub.empty.filtered')" size="sm" />

        <!-- 全量入口：chips 只切「看哪一类」，要看全部手记 / 全部会议 / PKM 工作台
             仍然需要一个地方把它们整个摊开。 -->
        <nav v-if="counts.total" class="see-all" :aria-label="t('notesHub.title')">
          <button type="button" class="see-all-btn" @click="go('/notes/voice')">
            <span class="material-symbols-outlined" aria-hidden="true">mic</span>
            <span class="see-all-label">{{ t('notesHub.link.allNotes') }}</span>
            <span class="material-symbols-outlined see-all-chevron" aria-hidden="true">chevron_right</span>
          </button>
          <button type="button" class="see-all-btn" @click="go('/meetings')">
            <span class="material-symbols-outlined" aria-hidden="true">event</span>
            <span class="see-all-label">{{ t('notesHub.link.allMeetings') }}</span>
            <span class="material-symbols-outlined see-all-chevron" aria-hidden="true">chevron_right</span>
          </button>
          <button type="button" class="see-all-btn" @click="go('/pkm/today')">
            <span class="material-symbols-outlined" aria-hidden="true">sticky_note_2</span>
            <span class="see-all-label">{{ t('notesHub.link.allPkm') }}</span>
            <span class="material-symbols-outlined see-all-chevron" aria-hidden="true">chevron_right</span>
          </button>
        </nav>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { DbLockedState, EmptyState, Skeleton } from '../../components'
import SourceFilterBar from '../../components/interactive/SourceFilterBar.vue'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import ScrollChromePortal from '../../components/layout/ScrollChromePortal.vue'
import { ICON, type IconName } from '../../constants/icons'
import { listNotes } from '../notes/notes-store'
import { listMeetings } from '../meetings/meetings-store'
import { listNotes as listPkmNotes } from '../pkm/pkm-store'
import { formatRelative, toEpochSeconds } from '../../utils/relative-time'

defineOptions({ name: 'NotesHubView' })

const { t } = useI18n()
const router = useRouter()

/**
 * 统一行的形状。
 *
 * `kind` 与 `filterKey` **刻意是两个字段**，尽管值常常看起来该一样：
 * kind 是数据来源（决定图标与跳转），filterKey 是 i18n 命名空间里的 key。
 * 二者不同构——手记这一路的 kind 叫 'note'，而它在 chips 与行标签里的
 * 文案 key 是 'manual'（手记）。曾经把 filterKey 也设成 'note'，
 * 结果模板 `t(\`notesHub.filter.${row.filterKey}\`)` 渲染出字面量
 * "notesHub.filter.note"——静态 i18n 卡口看不见模板字符串，空库冒烟
 * 又因为列表根本不渲染而查不到，只有真机有数据时才会暴露。
 * 分开定义正是为了让这种不一致在类型层面就被看见。
 */
type RowKind = 'note' | 'meeting' | 'pkm'
type SourceKey = 'manual' | 'meeting' | 'pkm'

const KIND_TO_SOURCE_KEY: Record<RowKind, SourceKey> = {
  note: 'manual',
  meeting: 'meeting',
  pkm: 'pkm',
}

interface HubRow {
  kind: RowKind
  sourceKey: SourceKey
  id: string
  /** Unix **秒**——三个来源的原始字段都是毫秒，必须在构造处归一。 */
  ts: number
  title: string
  preview: string
  duration?: number
  pending?: boolean
  voice?: boolean
  to: string
}

type Source = 'all' | RowKind

const PAGE = 50

const source = ref<Source>('all')
const loading = ref(true)
const dbNotReady = ref(false)
const notes = ref<{ id: string; title: string | null; content: string; updatedAt: number; createdByVoice: boolean }[]>([])
const meetings = ref<Awaited<ReturnType<typeof listMeetings>>>([])
const pkmNotes = ref<Awaited<ReturnType<typeof listPkmNotes>>>([])

/**
 * 摘要裁剪。
 *
 * 手记 / 纪要 / PKM 正文分别是纯文本、可能含转写标记的纯文本、TipTap 产出的 HTML。
 * 直接塞进列表会同时踩三个坑：HTML 标签被当文本显示、转写里的时间戳占满整行、
 * 无空格长串（MIME boundary、URL）把行撑破后被祖先 overflow 静默裁掉。
 * 所以统一走 stripHtml → 折叠空白 → 硬截断。
 */
function preview(raw: string | null | undefined, max = 120): string {
  if (!raw) return ''
  const text = raw
    .replace(/<[^>]*>/g, ' ')   // PKM 的 HTML
    .replace(/[#*_>`~\[\]()]/g, ' ') // markdown 标记
    .replace(/\s+/g, ' ')
    .trim()
  return text.length > max ? `${text.slice(0, max)}…` : text
}

/** 会议标题为空是常态（用户没起名就录了），此时用纪要首句兜底，别让列表出现一排「无标题」。 */
function meetingTitle(m: { title: string | null; summary: string | null; topic: string | null }): string {
  const explicit = m.title?.trim() || m.topic?.trim()
  if (explicit) return explicit
  const firstLine = m.summary?.split('\n').find((l) => l.trim().length > 0)?.trim()
  return firstLine ? preview(firstLine, 40) : t('nav.meetings')
}

const noteRows = computed<HubRow[]>(() =>
  notes.value.map((n) => ({
    kind: 'note',
    sourceKey: KIND_TO_SOURCE_KEY.note,
    id: n.id,
    // LocalNote.updatedAt 是毫秒（写入用 Date.now()），不归一会永远显示「刚刚」。
    ts: toEpochSeconds(n.updatedAt),
    title: n.title?.trim() || t('notesHub.filter.manual'),
    preview: preview(n.content),
    // 语音笔记得挂个标记：它们没有可读的正文摘要，光看两行裁剪后的转写，
    // 用户分不清「这就是全部内容」还是「这里被截断了」。有这个角标就说明
    // 点进去能听原音。
    voice: n.createdByVoice,
    to: `/notes/${encodeURIComponent(n.id)}`,
  })),
)

const meetingRows = computed<HubRow[]>(() =>
  meetings.value.map((m) => ({
    kind: 'meeting',
    sourceKey: KIND_TO_SOURCE_KEY.meeting,
    id: m.id,
    // LocalMeeting.startedAt 是毫秒，同上。
    ts: toEpochSeconds(m.startedAt),
    title: meetingTitle(m),
    preview: preview(m.summary ?? m.refinedTranscript ?? m.transcript),
    duration: m.durationMs,
    // status 停在 recording/processing 说明转写或纪要还在跑：行内给「处理中」，
    // 否则用户点进去看到空白会以为这份纪要坏了。
    pending: m.status === 'recording' || m.status === 'processing',
    to: `/meetings/${encodeURIComponent(m.id)}`,
  })),
)

const pkmRows = computed<HubRow[]>(() =>
  pkmNotes.value.map((p) => ({
    kind: 'pkm',
    sourceKey: KIND_TO_SOURCE_KEY.pkm,
    id: p.id,
    // asset-store 的 now() 是 Date.now()，毫秒。
    ts: toEpochSeconds(p.updatedAt),
    title: p.title?.trim() || t('notesHub.filter.pkm'),
    preview: preview(p.html),
    to: `/pkm/n/${encodeURIComponent(p.id)}`,
  })),
)

const allRows = computed<HubRow[]>(() =>
  [...noteRows.value, ...meetingRows.value, ...pkmRows.value].sort((a, b) => b.ts - a.ts),
)

const rows = computed<HubRow[]>(() =>
  source.value === 'all' ? allRows.value : allRows.value.filter((r) => r.kind === source.value),
)

const counts = computed(() => ({
  note: noteRows.value.length,
  meeting: meetingRows.value.length,
  pkm: pkmRows.value.length,
  total: noteRows.value.length + meetingRows.value.length + pkmNotes.value.length,
}))

const sourceOptions = computed(() => [
  { value: 'all', label: t('notesHub.filter.all'), count: counts.value.total },
  { value: 'note', label: t('notesHub.filter.manual'), count: counts.value.note },
  { value: 'meeting', label: t('notesHub.filter.meeting'), count: counts.value.meeting },
  { value: 'pkm', label: t('notesHub.filter.pkm'), count: counts.value.pkm },
])

function sourceIcon(kind: RowKind): IconName {
  switch (kind) {
    case 'note':
      return ICON.hubSourceNote
    case 'meeting':
      return ICON.hubSourceMeeting
    case 'pkm':
      return ICON.hubSourcePkm
  }
}

/**
 * 会议时长标签。
 *
 * 早先的写法是从 formatDuration() 的输出 `H:MM:SS` / `M:SS` 里 split 再判断小时位，
 * 但 formatDuration 在不足 1 小时时返回的是**两段**（`M:SS`），hour 位根本不存在，
 * 于是 `h === '0'` 恒为 false —— 5 分钟的会议会显示成裸的 "5:00"，
 * meta.minutes 这个 key 从来没被真正用上。这里直接从毫秒算，不再绕格式化器的输出。
 */
function durationLabel(ms: number): string {
  if (!ms || ms <= 0) return ''
  const totalMin = Math.round(ms / 60000)
  // 不足半分钟 → round 会得 0，那时显示「0 min」比不显示更糟：
  // 它暗示这场会议时长为零，而实际只是太短。
  if (totalMin < 1) return ''
  const hours = Math.floor(totalMin / 60)
  const mins = totalMin % 60
  // 整小时不显示多余的 ":00"；其余保留 M:SS 之外的 H:MM 紧凑形态。
  return hours > 0
    ? t('notesHub.meta.hoursMinutes', { hours, minutes: mins })
    : t('notesHub.meta.minutes', { count: mins })
}

function go(to: string) {
  router.push(to)
}

onMounted(load)

/**
 * 三个来源并发拉，任一失败不影响其余——但**首个抛出的错误**决定要不要显示
 * 「库未解锁」。区分依据是错误本身而不是「有没有数据」：空库是完全正常的初始
 * 状态，不能拿它冒充解锁失败。
 */
async function load() {
  loading.value = true
  dbNotReady.value = false
  const settled = await Promise.allSettled([
    listNotes({ limit: PAGE }),
    listMeetings(PAGE),
    listPkmNotes({ limit: PAGE }),
  ])

  const [n, m, p] = settled
  if (n.status === 'fulfilled') notes.value = n.value
  if (m.status === 'fulfilled') meetings.value = m.value
  if (p.status === 'fulfilled') pkmNotes.value = p.value

  const firstError = settled.find((s) => s.status === 'rejected') as
    | PromiseRejectedResult
    | undefined
  if (firstError) {
    // 加密库未解锁时三个调用会一起抛。识别不出来就按「未解锁」处理：比起让用户
    // 面对一个空列表猜为什么，误报一次解锁提示的代价小得多（点一下就回来了）。
    dbNotReady.value = true
    console.warn('[NotesHubView] 笔记来源加载失败：', firstError.reason)
  }
  loading.value = false
}
</script>

<style scoped>
.notes-hub {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: 0 var(--space-3) var(--space-6);
}

.hdr-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border: none;
  border-radius: var(--radius-full);
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}

.hdr-action.primary {
  background: var(--brand-bg);
  color: var(--brand-primary);
}

.hdr-action .material-symbols-outlined {
  font-size: 20px;
}

.overview {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin: 0;
  padding: 0 var(--space-1);
  font-size: var(--text-sm);
  color: var(--text-muted);
}

.dot {
  color: var(--text-tertiary, var(--text-muted));
}

.state {
  padding-top: var(--space-2);
}

.row-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--spacing-list-gap);
}

.row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--spacing-card-padding);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
}

.row:active {
  background: var(--bg-hover, var(--bg-subtle));
}

.row-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  flex-shrink: 0;
  border-radius: var(--radius-sm);
  background: var(--bg-subtle);
  color: var(--text-secondary);
}

.row-icon .material-symbols-outlined {
  font-size: 19px;
}

.row-icon.meeting {
  background: var(--brand-bg);
  color: var(--brand-primary);
}

.row-icon.pkm {
  background: var(--danger-bg);
  color: var(--danger);
}

.row-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.row-title {
  font-size: var(--text-base);
  font-weight: var(--font-weight-medium, 500);
  /* 三类来源的标题都可能带不可断长串（URL、MIME boundary）。没有这条，
     标题会把 .row-main 撑破，祖先 overflow-x:hidden 把溢出部分静默裁掉。 */
  overflow-wrap: anywhere;
}

.row-preview {
  font-size: var(--text-smd);
  color: var(--text-secondary);
  line-height: 1.4;
  overflow-wrap: anywhere;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.row-meta {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-wrap: wrap;
  font-size: var(--text-2xs);
  color: var(--text-muted);
}

.row-source {
  color: var(--text-secondary);
}

.row-pending {
  color: var(--warn, var(--brand-primary));
}

/* 语音笔记角标：图标 + 文案，比纯文字更容易在长列表里被扫到。 */
.row-flag {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 0 5px;
  border-radius: var(--radius-sm);
  background: var(--brand-bg);
  color: var(--brand-primary);
  line-height: 15px;
}

.row-flag .material-symbols-outlined {
  font-size: var(--text-sm);
}

.row-chevron {
  font-size: var(--text-xl);
  color: var(--text-tertiary, var(--text-muted));
  flex-shrink: 0;
}

.see-all {
  display: flex;
  flex-direction: column;
  margin-top: var(--space-2);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.see-all-btn {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  min-height: 46px;
  padding: var(--space-3);
  border: none;
  border-bottom: 1px solid var(--border);
  background: transparent;
  color: var(--text-primary);
  text-align: left;
  font-size: var(--text-base);
  cursor: pointer;
}

.see-all-btn:last-child {
  border-bottom: none;
}

.see-all-btn:active {
  background: var(--bg-hover, var(--bg-subtle));
}

.see-all-btn > .material-symbols-outlined:first-child {
  font-size: 19px;
  color: var(--text-secondary);
}

.see-all-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.see-all-chevron {
  font-size: var(--text-xl);
  color: var(--text-tertiary, var(--text-muted));
  flex-shrink: 0;
}
</style>
