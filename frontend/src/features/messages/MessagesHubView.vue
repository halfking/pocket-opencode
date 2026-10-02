<!--
  MessagesHubView —— 「消息」tab（2026-10-03 全局 IA 重组）。

  ## 重组动机

  重组前这三类东西分散在三处，用户为了「看一眼今天有什么要我处理」要点三个地方：
    - 重要邮件 → /email（要在「重要」分类里翻）
    - 订阅新闻 → /rss
    - 任务与系统消息 → 顶栏铃铛 → /notifications
  它们其实是**同一件事的三个来源**：外部世界给这个人的待处理输入。拆开的结果是
  用户必须自己记住「哪类在哪」，而不是让平台替他聚合。

  重组后：三类合并成一条按时间倒序的统一时间线，顶部 chips 切来源。
  「全部」这一档就是答案本身——今天有什么要我处理，一条流看完。

  ## 三个来源各自能独立失败 —— 这是刻意的

  与 NotesHubView 不同，这里的三个来源**不在同一个存储层**：
    - 邮件 → 本地加密库（local_emails），库未解锁就查不到
    - 订阅 → 服务端 /api/rss/*（remote-only 部署下可能 503）
    - 任务消息 → 服务端 /api/notifications（同样可能 503）
  所以一个源挂掉时另外两个**必须照常显示**。任何一次失败都被吞成「该来源为空 +
  角标不显示」，绝不整页报错——一个订阅源挂了就看不到邮件，是不可接受的退化。

  路由：`/messages`；进入：BottomNav 4 tab 的「消息」入口。
  深链：/email/:id、/rss/items/:id、/notifications 均保留。
-->
<template>
  <div class="msg-hub">
    <DbLockedState
      v-if="dbNotReady"
      :hint="t('messagesHub.title')"
      @relogin="router.push('/login')"
    />

    <template v-else>
      <HeaderActionsPortal>
        <button
          class="hdr-action"
          type="button"
          :aria-label="t('messagesHub.action.markAllRead')"
          :disabled="!unreadTotal"
          data-testid="msg-hub-mark-all"
          @click="markAllRead"
        >
          <span class="material-symbols-outlined" aria-hidden="true">done_all</span>
        </button>
        <button
          class="hdr-action"
          type="button"
          :aria-label="t('messagesHub.action.manageFeeds')"
          data-testid="msg-hub-manage-feeds"
          @click="go('/rss')"
        >
          <span class="material-symbols-outlined" aria-hidden="true">rss_feed</span>
        </button>
      </HeaderActionsPortal>

      <ScrollChromePortal>
        <SourceFilterBar
          v-model="source"
          :options="sourceOptions"
          :aria-label="t('messagesHub.title')"
        />
      </ScrollChromePortal>

      <div v-if="loading" class="state">
        <Skeleton :count="4" />
      </div>

      <template v-else>
        <ul v-if="rows.length" class="row-list" data-testid="messages-hub-list">
          <li v-for="row in rows" :key="`${row.kind}:${row.id}`">
            <button
              class="row"
              type="button"
              :class="{ unread: row.unread, urgent: row.urgent }"
              @click="open(row)"
            >
              <span class="row-icon" :class="row.kind" aria-hidden="true">
                <span class="material-symbols-outlined">{{ sourceIcon(row.kind) }}</span>
              </span>
              <span class="row-main">
                <span class="row-head">
                  <span class="row-source">{{ t(`messagesHub.filter.${row.filterKey}`) }}</span>
                  <span v-if="row.important" class="row-flag">{{ t('messagesHub.badge.important') }}</span>
                  <span class="row-time">{{ formatRelative(row.ts, t) }}</span>
                </span>
                <span class="row-title">{{ row.title }}</span>
                <span v-if="row.subtitle" class="row-subtitle">{{ row.subtitle }}</span>
                <span v-if="row.preview" class="row-preview">{{ row.preview }}</span>
                <span v-if="row.action" class="row-action">
                  <span class="material-symbols-outlined" aria-hidden="true">auto_awesome</span>
                  {{ t('messagesHub.meta.suggestedAction', { action: row.action }) }}
                </span>
              </span>
            </button>
          </li>
        </ul>

        <EmptyState
          v-else
          icon="📭"
          :title="t('messagesHub.empty.title')"
          :hint="t('messagesHub.empty.hint')"
          :action-label="t('messagesHub.action.manageFeeds')"
          size="sm"
          @action="go('/rss/add')"
        />

        <!-- 完整入口：chips 只切「看哪一类」，全量管理（邮件分类规则 / 订阅源）
             需要一个能整体操作的地方。 -->
        <nav v-if="rows.length" class="see-all" :aria-label="t('messagesHub.title')">
          <button type="button" class="see-all-btn" @click="go('/email')">
            <span class="material-symbols-outlined" aria-hidden="true">mail</span>
            <span class="see-all-label">{{ t('messagesHub.action.openMail') }}</span>
            <span class="material-symbols-outlined see-all-chevron" aria-hidden="true">chevron_right</span>
          </button>
          <button type="button" class="see-all-btn" @click="go('/rss')">
            <span class="material-symbols-outlined" aria-hidden="true">rss_feed</span>
            <span class="see-all-label">{{ t('messagesHub.action.manageFeeds') }}</span>
            <span class="material-symbols-outlined see-all-chevron" aria-hidden="true">chevron_right</span>
          </button>
        </nav>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { DbLockedState, EmptyState, Skeleton } from '../../components'
import SourceFilterBar from '../../components/interactive/SourceFilterBar.vue'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import ScrollChromePortal from '../../components/layout/ScrollChromePortal.vue'
import { ICON, type IconName } from '../../constants/icons'
import { rssApi } from '../../api/rss'
import { useNotificationStore } from '../../stores/notification'
import { listEmails, markRead as markEmailRead, type LocalEmail } from '../email/emails-store'
import { formatRelative } from '../../utils/relative-time'

defineOptions({ name: 'MessagesHubView' })

const { t } = useI18n()
const router = useRouter()
const notifications = useNotificationStore()

type RowKind = 'email' | 'rss' | 'task'
interface HubRow {
  kind: RowKind
  filterKey: RowKind
  id: string
  ts: number
  title: string
  subtitle: string
  preview: string
  action: string
  unread: boolean
  urgent: boolean
  important: boolean
  to: string
  /** 点击时的副作用（已读上报）；失败不阻断跳转。 */
  onOpen?: () => void
}

type Source = 'all' | RowKind

const PAGE = 50

const source = ref<Source>('all')
const loading = ref(true)
const dbNotReady = ref(false)
const emails = ref<LocalEmail[]>([])
const rssItems = ref<Awaited<ReturnType<typeof rssApi.listItems>>>([])
const taskItems = ref<typeof notifications.inbox>([])

function preview(raw: string | null | undefined, max = 140): string {
  if (!raw) return ''
  const text = raw
    .replace(/<[^>]*>/g, ' ')
    .replace(/[#*_>`~\[\]()]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  return text.length > max ? `${text.slice(0, max)}…` : text
}

/** RSS 的 publishedAt 可能缺（有的源不给），回落到 fetchedAt，否则整条会排到最末。 */
function rssTime(item: { publishedAt?: string; fetchedAt: string }): number {
  const raw = item.publishedAt || item.fetchedAt
  const ms = raw ? Date.parse(raw) : NaN
  return Number.isNaN(ms) ? 0 : Math.floor(ms / 1000)
}

const emailRows = computed<HubRow[]>(() =>
  emails.value.map((e) => ({
    kind: 'email',
    filterKey: 'email',
    id: e.id,
    // 用邮件本身的收信时间，不用同步时间：同步时间会把三天前的信顶到最上面。
    ts: e.date,
    title: e.subject?.trim() || preview(e.snippet, 40) || t('nav.email'),
    // 「来自 X」而不是裸的发件人：裸名字在通知流里会和「来源」标签视觉打架
    // （上方已经有"邮件"二字），加前缀能让这一行读起来是完整的一句话。
    subtitle: (e.fromName || e.fromAddress)
      ? t('messagesHub.meta.from', { name: e.fromName || e.fromAddress })
      : '',
    preview: preview(e.aiSummary || e.snippet),
    action: e.suggestedAction || '',
    unread: !e.isRead,
    urgent: false,
    important: e.importance === 'high',
    to: `/email/${encodeURIComponent(e.id)}`,
    onOpen: () => { if (!e.isRead) void markEmailRead(e.id, true).catch(() => {}) },
  })),
)

const rssRows = computed<HubRow[]>(() =>
  rssItems.value.map((it) => ({
    kind: 'rss',
    filterKey: 'rss',
    id: it.id,
    ts: rssTime(it),
    title: it.title?.trim() || preview(it.summary, 40) || t('nav.rss'),
    subtitle: it.author || '',
    preview: preview(it.summary),
    action: '',
    unread: it.status === 'unread',
    urgent: false,
    // relevance 是后端算的相关度，≥0.8 才标「重要」——阈值写死在这里而不是
    // 塞进 f(i)，因为它是**产品决定**（多高才算值得打断用户）。
    important: it.relevance >= 0.8,
    to: `/rss/items/${encodeURIComponent(it.id)}`,
    onOpen: () => { if (it.status === 'unread') void rssApi.markRead(it.id).catch(() => {}) },
  })),
)

const taskRows = computed<HubRow[]>(() =>
  taskItems.value.map((n) => ({
    kind: 'task',
    filterKey: 'task',
    id: n.id,
    ts: n.created_at,
    title: n.title?.trim() || t('messagesHub.filter.task'),
    subtitle: n.kind || n.source || '',
    preview: preview(n.body),
    action: '',
    unread: !n.read_at,
    urgent: n.priority === 'urgent' || n.priority === 'high',
    important: n.priority === 'urgent' || n.priority === 'high',
    to: taskTarget(n.source),
    onOpen: () => {
      if (!n.read_at) {
        void notifications.markRead(n.id).catch(() => {})
        n.read_at = Math.floor(Date.now() / 1000)
      }
    },
  })),
)

/** 与 NotificationsView 原有映射保持一致：source → 落地页。 */
function taskTarget(s: string): string {
  if (s === 'scheduledtask') return '/settings/scheduled-tasks'
  if (s === 'email') return '/email'
  if (s === 'flashcards') return '/flashcards'
  return '/notifications'
}

const allRows = computed<HubRow[]>(() =>
  [...emailRows.value, ...rssRows.value, ...taskRows.value].sort((a, b) => b.ts - a.ts),
)

const rows = computed<HubRow[]>(() =>
  source.value === 'all' ? allRows.value : allRows.value.filter((r) => r.kind === source.value),
)

const unreadTotal = computed(() => rows.value.filter((r) => r.unread).length)

const sourceOptions = computed(() => [
  { value: 'all', label: t('messagesHub.filter.all'), count: unreadTotal.value },
  {
    value: 'email',
    label: t('messagesHub.filter.email'),
    count: emailRows.value.filter((r) => r.unread).length,
    emphasis: true,
  },
  { value: 'rss', label: t('messagesHub.filter.rss'), count: rssRows.value.filter((r) => r.unread).length },
  { value: 'task', label: t('messagesHub.filter.task'), count: taskRows.value.filter((r) => r.unread).length },
])

function sourceIcon(kind: RowKind): IconName {
  switch (kind) {
    case 'email':
      return ICON.msgSourceEmail
    case 'rss':
      return ICON.msgSourceRss
    case 'task':
      return ICON.msgSourceTask
  }
}

function go(to: string) {
  router.push(to)
}

function open(row: HubRow) {
  // 顺序很重要：先置已读再跳。倒过来的话用户返回列表时这一行还是「未读」，
  // 会以为刚才那次点击没生效。
  row.onOpen?.()
  if (row.to) go(row.to)
}

/**
 * 「全部已读」只作用于**当前可见的行**（当前 chips 筛选下的那批）。
 *
 * 刻意不做"三个来源无条件全清"：用户在「订阅」筛选下点一下就顺手清掉邮件，
 * 是一次不可逆且大概率非预期的动作。筛选即作用域，是这类批量操作的通用约定。
 * 上报失败不回滚本地态（row.onOpen 内部各自 catch），最后重拉一次与服务端对齐。
 */
async function markAllRead() {
  if (!unreadTotal.value) return
  for (const row of rows.value) {
    if (row.unread) row.onOpen?.()
  }
  await nextTick()
  void load().catch(() => {})
}

onMounted(load)

/**
 * 三个来源并发拉、互不阻塞。**任何一个失败都不会让另外两个消失**——
 * 这是本页与 NotesHubView 最关键的差别（那边三个源同库，必须一起降级）。
 *
 * 通知这一路的取值方式与另外两路不同，踩过一个真实的坑：
 * `notifications.loadInbox()` 的返回类型是 **Promise<void>**（它把结果写进
 * store 自身，不返回数据）。早先这里写的是
 *   `taskItems.value = tasks.value as Notification[]`
 * 而 inbox 为空时走的正是 loadInbox 分支，tasks.value 是 undefined，
 * 于是 taskRows computed 里的 `.map` 抛
 * 「Cannot read properties of undefined (reading 'map')」，
 * **整个「消息」tab 白屏**——而且只在「首次进入、收件箱还是空的」这一种
 * 最常见的情况下发生，已有数据的老用户完全看不到。
 *
 * 现在统一从 store 读最终状态，不再碰 promise 的返回值。
 */
async function load() {
  loading.value = true
  // 有缓存就不重复拉：store 内部本就按 since 增量，重复调用只是白跑一次网络。
  const needInbox = notifications.inbox.length === 0
  // 第三个结果刻意不解构：它的值无意义（见上），要的是 await 之后 store 的状态。
  const [mail, feeds] = await Promise.allSettled([
    listEmails({ limit: PAGE, folder: '__all__' }),
    rssApi.listItems({ status: 'unread', limit: PAGE }),
    needInbox ? notifications.loadInbox() : Promise.resolve(),
  ])

  let locked = false
  if (mail.status === 'fulfilled') emails.value = mail.value
  else locked = true
  if (feeds.status === 'fulfilled') rssItems.value = feeds.value
  // 无论 loadInbox 成功还是失败，都回落到 store 当前值——它在 state 里
  // 初始化为 []，所以失败时 taskRows 拿到空数组而不是 undefined。
  taskItems.value = Array.isArray(notifications.inbox) ? notifications.inbox : []

  // 邮件来自本地加密库：它失败基本等于库没解锁，订阅/任务来自服务端，
  // 它们失败只是这一路没数据。两者不能同等对待。
  dbNotReady.value = locked && emails.value.length === 0
  loading.value = false
}
</script>

<style scoped>
.msg-hub {
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

.hdr-action:disabled {
  opacity: 0.4;
  cursor: default;
}

.hdr-action .material-symbols-outlined {
  font-size: 20px;
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
  align-items: flex-start;
  gap: var(--space-3);
  width: 100%;
  padding: var(--spacing-card-padding);
  background: var(--bg-card);
  border: 1px solid var(--border);
  /* 未读用左边框表达，和邮件/通知两个老列表的既有语言一致 */
  border-left: 3px solid transparent;
  border-radius: var(--radius-md);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
}

.row.unread {
  border-left-color: var(--brand-primary);
  background: var(--bg-elevated);
}

.row.urgent {
  border-left-color: var(--danger);
}

.row:active {
  background: var(--bg-hover, var(--bg-subtle));
}

.row-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  flex-shrink: 0;
  border-radius: var(--radius-full);
  background: var(--bg-subtle);
  color: var(--text-secondary);
}

.row-icon .material-symbols-outlined {
  font-size: 17px;
}

.row-icon.email {
  background: var(--brand-bg);
  color: var(--brand-primary);
}

.row-icon.task {
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

.row-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-2xs);
  color: var(--text-muted);
}

.row-source {
  color: var(--text-secondary);
  font-weight: var(--font-weight-medium, 500);
}

.row-flag {
  padding: 0 5px;
  border-radius: var(--radius-sm);
  background: var(--danger-bg);
  color: var(--danger);
  line-height: 15px;
}

.row-time {
  margin-left: auto;
  flex-shrink: 0;
}

.row-title {
  font-size: var(--text-base);
  font-weight: var(--font-weight-medium, 500);
  overflow-wrap: anywhere;
}

.row.unread .row-title {
  font-weight: var(--font-weight-semibold, 600);
}

.row-subtitle {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-preview {
  font-size: var(--text-smd);
  color: var(--text-secondary);
  line-height: 1.4;
  overflow-wrap: anywhere;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.row-action {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  align-self: flex-start;
  margin-top: 2px;
  padding: 2px var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--brand-bg);
  color: var(--brand-primary);
  font-size: var(--text-2xs);
  overflow-wrap: anywhere;
}

.row-action .material-symbols-outlined {
  font-size: var(--text-smd);
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
