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
  所以一个源挂掉时另外两个**必须照常显示**。任何一次失败都只降级成
  「顶部一条说明哪个来源没加载上 + 该来源为空」，绝不整页报错——
  一个订阅源挂了就看不到邮件，是不可接受的退化。

  曾经这里用 `dbNotReady`（由**邮件**一路的失败推导）在失败时整页换成
  「本地库未解锁，请解锁」：订阅和任务已经加载成功的内容会被一起收走，
  而且用户被引导去按一个解决不了问题的按钮。listEmails 的失败原因也不止
  「未解锁」（schema 迁移、加密配置、字段类型都能抛），把它一律说成
  「未解锁」本身就是错的诊断。现在改成：整页只保留一条 per-source 错误条，
  仅当**三个来源全挂**时才认为页面不可用。

  路由：`/messages`；进入：BottomNav 4 tab 的「消息」入口。
  深链：/email/:id、/rss/items/:id、/notifications 均保留。
-->
<template>
  <div class="msg-hub">
    <HeaderActionsPortal>
        <button
          class="hdr-action"
          type="button"
          :aria-label="t('messagesHub.action.markAllRead')"
          :disabled="!visibleUnread"
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

      <!-- per-source 降级条：哪个来源没加载上就点名哪个，不牵连已成功的来源。
           只在**确实有来源失败**时出现，全部成功时不占任何纵向空间。 -->
      <p v-if="failedSources.length" class="src-error" role="status" data-testid="msg-hub-src-error">
        <span class="material-symbols-outlined" aria-hidden="true">cloud_off</span>
        <span>{{ t('messagesHub.partialFailure', { sources: failedSources.join(' · ') }) }}</span>
        <button type="button" class="src-retry" @click="load">{{ t('common.retry') }}</button>
      </p>

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
              <!-- 未读此前只靠左边框色 + 字重 + 底色表达，读屏用户完全无从分辨。
                   补一份仅读屏可见的标记；.sr-only 是 scoped 规则，不依赖全局。 -->
              <span v-if="row.unread" class="sr-only">{{ t('messagesHub.unreadBadge', { count: 1 }) }}</span>
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
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { EmptyState, Skeleton } from '../../components'
import SourceFilterBar from '../../components/interactive/SourceFilterBar.vue'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import ScrollChromePortal from '../../components/layout/ScrollChromePortal.vue'
import { ICON, type IconName } from '../../constants/icons'
import { rssApi } from '../../api/rss'
import { useNotificationStore } from '../../stores/notification'
import { listEmails, markRead as markEmailRead, type LocalEmail } from '../email/emails-store'
import { formatRelative, toEpochSeconds } from '../../utils/relative-time'

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
  /**
   * 点击时的副作用（已读上报）。**返回 promise**，好让「全部已读」能等它落地。
   * 失败不阻断跳转：内部各自 catch，界面不弹 toast 打断心流。
   */
  onOpen?: () => Promise<unknown>
}

type Source = 'all' | RowKind

const PAGE = 50

const source = ref<Source>('all')
const loading = ref(true)
/** 加载失败的来源（用 i18n 的来源名展示给用户）。空数组 = 三个都成功。 */
const failedSources = ref<string[]>([])
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
    // LocalEmail.date 是**毫秒**（emails-store 走 emailDateToMs），与
    // RSS/任务的秒混排会让「全部」永远把邮件排在最前——假的统一时间线。
    ts: toEpochSeconds(e.date),
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
    onOpen: () => (e.isRead ? Promise.resolve() : markEmailRead(e.id, true).catch(() => {})),
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
    onOpen: () => (it.status === 'unread' ? rssApi.markRead(it.id).catch(() => {}) : Promise.resolve()),
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
    onOpen: async () => {
      if (n.read_at) return
      // 先本地置已读：否则等服务端往返的这几百毫秒里，这一行仍显示未读，
      // 用户会以为点了没反应。
      n.read_at = Math.floor(Date.now() / 1000)
      await notifications.markRead(n.id).catch(() => {})
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

/**
 * 「全部」chip 上的未读数必须是**全局**的，不是当前筛选的。
 *
 * 早先它直接用了 unreadTotal（= 当前筛选后的 rows），于是切到「订阅」时
 * 「全部」只显示订阅的未读数；订阅已读完时角标整个消失，哪怕邮箱里还压着
 * 20 封——「全部」这个 chip 的语义就是「你一共有多少事没处理」，
 * 跟着筛选走等于让这个语义随用户点哪儿而变。
 */
const globalUnread = computed(() => allRows.value.filter((r) => r.unread).length)
/** 当前筛选下的未读数：只用来决定「全部已读」该不该可点。 */
const visibleUnread = computed(() => rows.value.filter((r) => r.unread).length)

const sourceOptions = computed(() => [
  { value: 'all', label: t('messagesHub.filter.all'), count: globalUnread.value },
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
 *
 * 早先这里 `await nextTick()` 就去重拉——nextTick 只等 DOM flush，**不等网络**，
 * 而 onOpen 里是 fire-and-forget 的上报，于是重拉很可能早于服务端落库，
 * 行会「复活」成未读，用户以为按钮没生效。现在收集 promise 真的等它们落地，
 * 且**不再走 load()**（那会把整页换成骨架屏，每点一次白闪一次）。
 */
async function markAllRead() {
  if (!visibleUnread.value) return
  const jobs: Promise<unknown>[] = []
  for (const row of rows.value) {
    if (!row.unread) continue
    const done = row.onOpen?.()
    if (done) jobs.push(done)
  }
  await Promise.allSettled(jobs)
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
  const [mail, feeds, tasks] = await Promise.allSettled([
    listEmails({ limit: PAGE, folder: '__all__' }),
    rssApi.listItems({ status: 'unread', limit: PAGE }),
    needInbox ? notifications.loadInbox() : Promise.resolve(),
  ])

  const failed: string[] = []
  if (mail.status === 'fulfilled') emails.value = mail.value
  else failed.push(t('messagesHub.filter.email'))
  if (feeds.status === 'fulfilled') rssItems.value = feeds.value
  else failed.push(t('messagesHub.filter.rss'))
  // 无论 loadInbox 成功还是失败，都回落到 store 当前值——它在 state 里
  // 初始化为 []，所以失败时 taskRows 拿到空数组而不是 undefined。
  taskItems.value = Array.isArray(notifications.inbox) ? notifications.inbox : []
  if (tasks.status === 'rejected') failed.push(t('messagesHub.filter.task'))

  failedSources.value = failed
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

/* 仅读屏可见。仓库里另有一份同名规则，但它在别的组件的 <style scoped> 里，
   scoped 不跨组件生效，这里必须自己声明。 */
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

/* per-source 降级条：不是阻塞态，所以不用整页接管；
   用 warning 底色 + 图标点明「哪一路没加载上」，旁边给一个重试。 */
.src-error {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--danger-bg);
  color: var(--danger);
  font-size: var(--text-sm);
  line-height: 1.4;
  /* 错误文案里是来源名拼接，长度不可控，必须能断行 */
  overflow-wrap: anywhere;
}

.src-error .material-symbols-outlined {
  font-size: 17px;
  flex-shrink: 0;
}

.src-error > span:nth-child(2) {
  flex: 1;
  min-width: 0;
}

.src-retry {
  flex-shrink: 0;
  padding: 2px var(--space-2);
  border: 1px solid currentColor;
  border-radius: var(--radius-full);
  background: transparent;
  color: inherit;
  font-size: var(--text-2xs);
  cursor: pointer;
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
