<template>
  <div class="inbox-page">
    <DbLockedState
      v-if="dbNotReady"
      hint="邮箱功能需要本地加密数据库"
      @relogin="goToLogin"
    />

    <template v-else>
      <HeaderActionsPortal>
        <template v-if="inbox.selectMode.value">
          <button class="chat-icon-btn" type="button" aria-label="取消选择" @click="inbox.exitSelect()">
            <span class="material-symbols-outlined" aria-hidden="true">close</span>
          </button>
          <button
            class="chat-icon-btn"
            type="button"
            :aria-label="`移动已选 ${inbox.selectedCount.value} 封到目录`"
            :disabled="!inbox.selectedCount.value || moveBusy"
            @click="moveOpen = true"
          >
            <span class="material-symbols-outlined" aria-hidden="true">drive_file_move</span>
          </button>
          <button
            class="chat-icon-btn"
            type="button"
            :aria-label="`删除已选 ${inbox.selectedCount.value} 封`"
            :disabled="!inbox.selectedCount.value || inbox.purgeBusy.value"
            @click="onPurge"
          >
            <span class="material-symbols-outlined" aria-hidden="true">delete</span>
          </button>
        </template>
        <template v-else>
          <button class="chat-icon-btn" type="button" aria-label="搜索" @click="inbox.toggleSearch()">
            <span class="material-symbols-outlined" aria-hidden="true">search</span>
          </button>
          <button
            class="chat-icon-btn"
            type="button"
            aria-label="归类"
            :disabled="inbox.classifying.value"
            @click="onClassify"
          >
            <span class="material-symbols-outlined" aria-hidden="true">label</span>
          </button>
          <button class="chat-icon-btn" type="button" aria-label="更多" @click="inbox.moreOpen.value = !inbox.moreOpen.value">
            <span class="material-symbols-outlined" aria-hidden="true">more_vert</span>
          </button>
        </template>
      </HeaderActionsPortal>
      <div v-if="inbox.moreOpen.value && !inbox.selectMode.value" class="more-menu">
        <button type="button" @click="go('/email/summary')">{{ t('email.dailySummary') }}</button>
        <button type="button" @click="go('/email/invoices')">发票整理</button>
        <button type="button" @click="go('/email/cleanup')">清理垃圾</button>
        <button type="button" @click="go('/email/folders')">邮件目录</button>
        <button type="button" :disabled="organizing" @click="onOrganize">
          {{ organizing ? '整理中…' : '智能整理' }}
        </button>
        <button type="button" @click="go('/email/settings')">邮箱设置</button>
        <!-- 真机 360dp 实测：删除入口挤在顶栏时标题「邮箱」被截成「邮...」，
             移入更多菜单后顶栏动作 4→3，标题恢复完整。 -->
        <button type="button" @click="onEnterSelectFromMenu">批量操作</button>
      </div>

      <ScrollChromePortal>
        <div class="filters">
          <button
            v-for="c in categoryChips"
            :key="c.value || 'all'"
            class="chip"
            :class="{ active: activeCategory === c.value }"
            @click="setCategory(c.value)"
          >
            {{ c.label }}
          </button>
        </div>
        <div v-if="inbox.searchOpen.value" class="search-bar">
          <input v-model="inbox.search.value.q" class="search-input" placeholder="发件人 / 标题 / 关键字" />
          <input v-model="inbox.search.value.from" class="search-input slim" placeholder="发件人" />
          <input v-model="inbox.search.value.subject" class="search-input slim" placeholder="标题" />
          <input v-model="sinceLocal" type="date" class="search-input slim" />
          <input v-model="untilLocal" type="date" class="search-input slim" />
          <button type="button" class="search-ok" @click="inbox.confirmSearch()">完成</button>
          <button type="button" class="search-ok ghost" @click="inbox.clearSearch()">清除</button>
        </div>
      </ScrollChromePortal>

      <PullToRefresh
        ref="pullRef"
        :on-refresh="onRefresh"
        class="inbox-scroll"
        @scroll-position="onScrollPosition"
      >
    <p v-if="inbox.classifyHint.value" class="sync-hint">
      {{ inbox.classifyHint.value }}
      <button v-if="inbox.classifying.value" type="button" class="linkish" @click="inbox.cancelClassify()">取消</button>
    </p>
    <p v-if="syncHint" class="sync-hint">{{ syncHint }}</p>
    <div v-if="loading" class="state-wrap"><Skeleton :count="5" /></div>
    <EmptyState
      v-else-if="loadError && shownEmails.length === 0"
      icon="⚠️"
      :title="loadError"
      action-label="重试"
      variant="inline"
      @action="load"
    />
    <EmptyState
      v-else-if="shownEmails.length === 0"
      icon="📧"
      title="暂无邮件"
      hint="下拉刷新会从邮箱服务器同步。若仍为空，请检查账户授权码。"
      size="sm"
      variant="inline"
    />

    <!-- TransitionGroup：增量同步落库后的新增/更新/删除以动画呈现（无刷新更新可感知）。
         "more" 哨兵不带 key，放在 TransitionGroup 外避免 move 类误伤。 -->
    <template v-else>
      <!--
        有旧邮件时的失败：**不打断**已加载内容（2026-10-05 修，与 SessionListView 同源）。
        ⚠️ `load()` 是**初次加载与下拉刷新共用**的入口，任何抛出都会设 `loadError`；
        而错误态原本是无条件 `v-else-if="loadError"` 排在列表之前 ⇒ 一次刷新失败
        就把整份收件箱换成整页错误态，已加载的邮件**从屏幕上消失**。
        本仓同类缺陷已在 SessionListView 上由实机读数抓到并修（UI-07e：
        失败前 4 行 → 失败后 0 行，而同一时刻后端数据仍在）—— 这里是同一形状的第二处。
      -->
      <div v-if="loadError" class="list-error-banner" role="alert">
        <span>{{ loadError }}</span>
        <button type="button" class="banner-retry" @click="load">重试</button>
      </div>
      <!-- role/aria/tabindex（2026-10-03 22:5x 真机实测后补）：
           卡片原本是纯 <div class="email-card">，**没有任何可访问语义** ——
           没有 role、没有 aria-label、没有 tabindex。DOM 里 30 张卡片齐活、
           文本也在，但它们在无障碍侧是完全不存在的：
             · 键盘/开关控制用户 tab 不到，收件箱没法用键盘读；
             · 屏幕阅读器只会念出一个无名容器；
             · 依赖无障碍树的自动化（含本仓的 Maestro flow）也选不中它们
               —— 2026-10-03 22:38 实测 email-browse 卡在「点开首行」，
               Maestro 抓下的层级里 `.email-list` 对应节点是个**零子节点**的叶子。

           对照证据（同一次会话、同一台设备、同一套层级抓取）：首页的列表项
           是**带子节点**的（TextView t="Maestro任务" / t="无响应" / t=" · 4 小时"），
           所以这不是 WebView 无障碍桥接的全局问题，是邮箱列表独有的。

           为什么用 listitem 而不是 button：卡片内部还有一个真 <button>
           （「标为已读」）。把外层声明成 button 就成了「可交互元素里嵌可交互
           元素」，是明确的 a11y 反模式，TalkBack 的焦点模型会错乱。
           listitem + tabindex 既给出了结构与焦点，又不与内层按钮冲突。 -->
      <TransitionGroup name="elist" tag="div" class="email-list" role="list" aria-label="邮件列表">
        <div
          v-for="m in shownEmails"
          :key="m.id"
          class="email-card"
          :class="{ high: m.importance === 'high', unread: !m.isRead }"
          role="listitem"
          tabindex="0"
          :aria-label="emailCardAriaLabel(m)"
          @click="inbox.selectMode.value ? inbox.toggle(m.id) : open(m.id)"
          @keydown.enter.prevent="inbox.selectMode.value ? inbox.toggle(m.id) : open(m.id)"
          @keydown.space.prevent="inbox.selectMode.value ? inbox.toggle(m.id) : open(m.id)"
        >
          <label v-if="inbox.selectMode.value" class="pick" @click.stop>
            <input type="checkbox" :checked="inbox.selected.value.has(m.id)" @change="inbox.toggle(m.id)" />
          </label>
          <div class="card-main">
          <div class="row1">
            <span class="from">{{ m.fromName || m.fromAddress }}</span>
            <span class="time">{{ formatEmailRelTime(m.date) }}</span>
          </div>
          <div class="subject">{{ m.subject }}</div>
          <div class="snippet">{{ m.snippet }}</div>
          <div v-if="m.aiSummary" class="ai-summary">💡 {{ m.aiSummary }}</div>
          <!--
            判定依据只在**重要**邮件上显示，且只在 importance=high 且有理由时出现。

            为什么只挂在重要邮件上：action_reason 是 AI 判重要度的理由，
            判成 low/normal 时它解释的是「为什么不重要」，那对用户没有行动价值，
            全量展示会变成每张卡片都挂一行噪音。判成 high 时它回答的是
            「为什么这封要提醒我」，正是用户决定要不要点开时需要的。

            为什么要 title 属性兜底：理由可能很长，卡片高度有限。
            title 让桌面端可悬停看全文，缺省行仍完整可读（CSS 里不截断）。
          -->
          <div
            v-if="m.importance === 'high' && m.actionReason"
            class="reason"
            :title="m.actionReason"
          >{{ m.actionReason }}</div>
          <div class="row-meta">
            <span v-if="m.category" class="tag" :class="`cat-${m.category}`">{{ catLabel(m.category) }}</span>
            <span v-if="m.importance === 'high'" class="importance">⭐ 重要</span>
            <span v-if="m.hasAttachments" class="attach">📎</span>
            <button v-if="!m.isRead" class="read-btn" @click.stop="markRead(m, true)">标为已读</button>
          </div>
          </div>
        </div>
      </TransitionGroup>
      <div v-if="emails.length > 0" ref="moreEl" class="more">
        <!-- 加载中：转圈 + 文案淡入淡出；静态提示则保持常驻，不做位移。 -->
        <span v-if="pageLoading" class="more-loading">
          <span class="material-symbols-outlined more-spin" aria-hidden="true">progress_activity</span>
          <span>正在加载更早的邮件…</span>
        </span>
        <!--
          v-else 挂在 <Transition> 上，v-if / v-else 必须在 Transition **内部** 成相邻兄弟。
          2026-10-01 修复：`v-if` 原本在 Transition 外面（L138），而 hint / end 两条却用了
          `v-else-if` / `v-else` 放在 Transition 内部 —— 编译器直接报
          "v-else/v-else-if has no adjacent v-if or v-else-if"，
          **整个 vite build 失败**，APK 出不来。
        -->
        <Transition v-else name="morefade" mode="out-in">
          <span v-if="hasMore" key="hint">上滑加载更早的邮件</span>
          <span v-else key="end" class="more-end">已到最早一封</span>
        </Transition>
      </div>
    </template>
    </PullToRefresh>

    <!-- 移动到目录：多选模式下的批量移动；详情页单封移动走详情自己的入口。 -->
    <EmailFolderPickerSheet v-model:open="moveOpen" @pick="onMoveToFolder" />

    <!--
      回顶按钮：下滑超过一屏后浮现。
      旧列表没有这个，用户在长列表里想回顶部只能一直上滑。
    -->
    <Transition name="totop">
      <button
        v-if="showScrollTop"
        type="button"
        class="to-top"
        aria-label="回到顶部"
        @click="scrollToTop"
      >
        <span class="material-symbols-outlined" aria-hidden="true">expand_more</span>
      </button>
    </Transition>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Skeleton, EmptyState, PullToRefresh, DbLockedState } from '../../components'
import ScrollChromePortal from '@/components/layout/ScrollChromePortal.vue'
import HeaderActionsPortal from '@/components/layout/HeaderActionsPortal.vue'
import * as emailsStore from './emails-store'
import { emailApi } from '../../api/email'
import type { LocalEmail } from './emails-store'
import { pullInboxFromServer, readInboxPage } from './email-inbox-page'
import { prefetchEmailBody, prefetchEmailBodySeries } from './email-body-prefetch.ts'
import { readEmailBodyLocal, writeEmailBodyLocal } from './email-body-cache.ts'
import { extractEmailBody } from './email-body-format.ts'
import { recordOpsEntry } from './email-folders-store'
import EmailFolderPickerSheet from './EmailFolderPickerSheet.vue'
import { INBOX_PAGE_SIZE } from './email-inbox-pagination.ts'
import { inboxMergeRows, makeInboxFetchPage } from './email-inbox-adapter.ts'
import { useContinuousList } from '../../composables/useContinuousList'
import { runDelegatedEmailFetch } from './email-fetch-run'
import { sanitizeFetchHint } from './email-fetch-plan'
import { formatEmailRelTime } from './cleanup-filter'
import { INBOX_CATEGORY_CHIPS, catLabel } from './email-categories'
import { formatInboxSearchLabel } from './email-inbox-search'
import { useEmailInbox } from './use-email-inbox'
import { setHeaderTitle } from '../../composables/useAppHeaderTitle'
import { useListScene } from '../../composables/use-list-scene'

defineOptions({ name: 'EmailInboxView' })

/**
 * emailCardAriaLabel 组装单张邮件卡片的可访问名。
 *
 * 为什么需要它：卡片在无障碍侧原本是**完全不存在**的（纯 div，无 role /
 * aria-label / tabindex，见模板处的说明与真机实测）。给它一个聚合的名字，
 * 屏幕阅读器才能把「谁、什么时候、什么主题、读过没有」一次念完，而不是
 * 让用户自己在四个兄弟节点间跳。
 *
 * 摘要**故意不放进来**：它最长 500 字符，塞进 label 会让播报变成一段噪音，
 * 而这正是「一封邮件没有摘要」时最该被听见的缺失。
 */
function emailCardAriaLabel(m: LocalEmail): string {
  const parts: string[] = []
  const who = (m.fromName || m.fromAddress || '').trim()
  if (who) parts.push(who)
  const when = formatEmailRelTime(m.date)
  if (when) parts.push(when)
  const subject = (m.subject || '').trim()
  if (subject) parts.push(subject)
  parts.push(m.isRead ? '已读' : '未读')
  return parts.join('，')
}

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const loading = ref(true)
const loadError = ref('')
const activeCategory = ref<string>('')
const dbNotReady = ref(false)
const syncHint = ref('')
const inbox = useEmailInbox()
const categoryChips = INBOX_CATEGORY_CHIPS
const sinceLocal = ref('')
const untilLocal = ref('')
// 目录视图：/email?folder=<name> 时只看该目录（空串 = 收件箱默认视图）。
const activeFolder = ref(typeof route.query.folder === 'string' ? route.query.folder : '')

/**
 * 2026-10-06：从 `useListSentinel` + `email-inbox-pagination.ts` 的自研状态机
 * 迁到 Hyper 连续加载内核（`lib/shell/continuousList.ts` + 本文件上方的适配层）。
 *
 * 三个参数各自对应 UI规范 07 §2.5 逐条核对出的结论：
 *  - `refreshPolicy: 'merge'`：下拉刷新只把第 1 页的新值**并到顶部**并保留
 *    已翻开的分页。原先靠 `applyRefreshPage` 手工实现，且它与「翻页」共用
 *    `advanceInboxPage` 导致游标被刷新推走——那正是 07 §2.8 修掉的缺陷。
 *    内核在**结构上**把两条路径分开（refresh 走 preserveCursor，append 才
 *    推进 nextPage），同类缺陷不会再发生。
 *  - `mergeRows: inboxMergeRows`：排序是领域知识，内核刻意不替调用方决定。
 *  - `fetchPage`：`hasMore` 由适配层从「这页取满没有」推导，所以**不需要**
 *    给本地库加 total 查询。
 */
const {
  rows: emails,
  status: listStatus,
  hasMore,
  sentinelRef: moreEl,
  refresh: refreshList,
  resetQuery: resetListQuery,
  loadMore,
} = useContinuousList<LocalEmail>({
  fetchPage: makeInboxFetchPage(
    {
      readPage: (c, offset, f) => readInboxPage(c, offset, f),
      getCategory: () => activeCategory.value,
      getFolder: () => activeFolder.value,
      countAll: () => emailsStore.countLocalEmails(),
    },
    INBOX_PAGE_SIZE,
  ),
  pageSize: INBOX_PAGE_SIZE,
  refreshPolicy: 'merge',
  mergeRows: inboxMergeRows,
})

/** 「正在翻页」——模板用它显示转圈。内核的状态枚举比旧的布尔组合更直接。 */
const pageLoading = computed(() => listStatus.value === 'loadingNext')

const shownEmails = computed(() => inbox.visibleEmails(emails.value))
const moveOpen = ref(false)
const moveBusy = ref(false)
const organizing = ref(false)

const refreshing = ref(false)

/**
 * 正文预取依赖。与 EmailDetailView 用同一份定义，保证：
 *  - 两边对「怎么取正文」的约定一致；
 *  - 在途去重真正生效（否则两个模块各写一套，重复请求照旧）。
 */
const bodyPrefetchDeps = {
  fetchBody: (id: string) => emailApi.getEmailBody(id),
  readCache: (id: string) => readEmailBodyLocal(id),
  writeCache: (id: string, body: string) => writeEmailBodyLocal(id, body),
  extract: (raw: string) => extractEmailBody(raw),
}

function goToLogin() {
  router.push('/login')
}
function go(path: string) {
  inbox.moreOpen.value = false
  router.push(path)
}

function onEnterSelectFromMenu() {
  inbox.moreOpen.value = false
  inbox.enterSelect()
}

watch([sinceLocal, untilLocal], () => {
  inbox.search.value = {
    ...inbox.search.value,
    sinceMs: sinceLocal.value ? Date.parse(sinceLocal.value) : undefined,
    untilMs: untilLocal.value ? Date.parse(untilLocal.value) + 86_399_000 : undefined,
  }
})

async function onClassify() {
  emails.value = await inbox.runClassify(emails.value)
  await showLocal()
}

async function onPurge() {
  if (!inbox.selectedCount.value) return
  if (!window.confirm(`删除选中的 ${inbox.selectedCount.value} 封邮件？正文将清空，仅保留标题和摘要；同步后服务器侧也会移入垃圾箱。`)) return
  // 删除前记操作日志（delete op）：本地已删的邮件，服务器侧（IMAP 移入垃圾箱）
  // 由目录页「同步到服务器」按钮执行——离线删除也不丢。
  const ids = [...inbox.selected.value]
  for (const id of ids) {
    const m = emails.value.find((x) => x.id === id)
    if (!m) continue
    await recordOpsEntry({
      accountId: m.accountId, emailId: m.id, uid: m.uid ?? 0,
      action: 'delete', targetFolder: '', subject: m.subject || '',
    })
  }
  await inbox.confirmPurge()
  await load()
}

/**
 * 读第一页。
 *
 * replace=true 时整表替换（切分类 / 首屏）；false 时把结果并入现有列表
 * （下拉刷新与后台同步），避免把用户已翻开的分页丢掉。
 */
/**
 * 重读本地第一页。
 *
 * ⚠️ 2026-10-06 迁移：分页状态已由 `useContinuousList` 持有，本函数**不再**
 * 自行推进游标或合并分页。`replace` 参数随之失去意义（整表重读与合并重读
 * 都由内核的 `refreshPolicy` 决定），保留它只是不想改调用点。
 *
 * 后台流程仍在多处调用它（拉完新邮件、归类完成后），要的都是
 * 「把本地最新状态反映到列表上」——这正是内核 refresh 的职责。
 */
async function showLocal(replace = true) {
  void replace
  await refreshList()
}

/**
 * 批量移动已选邮件到目录（多选模式）。本地立即生效 + 记操作日志（离线可
 * 重放），服务端 move 尽力即时 IMAP MOVE；失败的操作留在日志里由
 * 目录页的「同步到服务器」按钮收口。
 */
async function onMoveToFolder(folderName: string) {
  const ids = [...inbox.selected.value]
  if (!ids.length || moveBusy.value) return
  moveBusy.value = true
  try {
    const moved = emails.value.filter((m) => ids.includes(m.id))
    for (const m of moved) {
      await recordOpsEntry({
        accountId: m.accountId, emailId: m.id, uid: m.uid ?? 0,
        action: 'move', targetFolder: folderName, subject: m.subject || '',
      })
      await emailsStore.setFolder(m.id, folderName)
      m.folder = folderName
    }
    inbox.exitSelect()
    if (activeFolder.value) await load()
    try {
      const rep = await emailApi.moveEmails(ids, folderName)
      syncHint.value = rep.pending > 0
        ? `已移动 ${rep.moved} 封（${rep.pending} 封待同步到服务器）`
        : `已移动 ${rep.moved} 封到${folderName || '收件箱'}`
    } catch {
      syncHint.value = `已本地移动 ${ids.length} 封，稍后可在「邮件目录」同步到服务器`
    }
  } finally {
    moveBusy.value = false
  }
}

/**
 * 智能整理：识别系统通知类邮件（验证码/物流/订阅/同标题群发…），
 * dryRun 预览数量，确认后整批移入「通知」目录。
 */
async function onOrganize() {
  if (organizing.value) return
  organizing.value = true
  inbox.moreOpen.value = false
  try {
    const preview = await emailApi.organizeInbox({ dryRun: true })
    const count = preview.count ?? 0
    if (count === 0) {
      syncHint.value = '没有识别到系统通知类邮件'
      return
    }
    const folder = preview.folder || '通知'
    if (!window.confirm(`识别到 ${count} 封系统通知类邮件，移入目录「${folder}」？`)) return
    const rep = await emailApi.organizeInbox({ folder })
    const moved = rep.moved ?? 0
    syncHint.value = rep.pending && rep.pending > 0
      ? `已整理 ${moved} 封进「${folder}」（${rep.pending} 封待同步到服务器）`
      : `已整理 ${moved} 封进「${folder}」`
    // 本地镜像同步收敛：服务端已改 folder_name，这里重拉列表。
    await pullInboxFromServer().catch(() => {})
    await showLocal(false)
  } catch (e: any) {
    syncHint.value = e?.message || '智能整理失败'
  } finally {
    organizing.value = false
  }
}

/** 是否有未归类邮件(需要触发自动归类) */
async function hasUncategorized(): Promise<boolean> {
  try {
    const page = await readInboxPage('', 0)
    return page.some((m) => !m.category)
  } catch { return false }
}

async function load() {
  loading.value = emails.value.length === 0
  loadError.value = ''
  dbNotReady.value = false
  try {
    // 首屏整表替换；后续（下拉刷新 / 后台同步）都走合并，保留已翻开的分页。
    await showLocal(true)
  } catch (e: any) {
    if (e?.message?.includes('LocalDB 未初始化')) dbNotReady.value = true
    else loadError.value = sanitizeFetchHint(e?.message || '') || '加载邮件失败'
  }
  loading.value = false
  void (async () => {
    try {
      await pullInboxFromServer()
      await showLocal(false)
    } catch { /* 保持本地列表 */ }
    // 自动归纳整理：拉完新邮件后,只要还有未归类就触发 runClassify 把后端
    // 队列清空(用户无需再点"归类"按钮)。后台静默运行,失败也不冒泡阻塞 UI。
    //
    // 原先这里还有一句一模一样的 showLocal(false)：它与上面那句之间状态没有任何
    // 变化（改稿残留），而 load() 会被 换分类/换目录/删除后/首屏 五处调用，
    // 每次都白读一遍本地库。已删。
    if (await hasUncategorized()) {
      try {
        emails.value = await inbox.runClassify(emails.value)
        await showLocal(false)
      } catch { /* 单封归类失败由 runClassify 内 hint 暴露;此处静默 */ }
    }
    const { hint } = await runDelegatedEmailFetch({ classify: true })
    if (hint) syncHint.value = hint
    try { await showLocal(false) } catch { /* 保持本地列表 */ }
    // 兜底：若 fetch 路径未分类完成,再扫一遍本地未归类,直到清空或被取消。
    let safety = 0
    while (safety++ < 3 && await hasUncategorized() && !inbox.classifying.value) {
      try { emails.value = await inbox.runClassify(emails.value); await showLocal(false) } catch { break }
    }
  })()
}


/**
 * 回顶按钮的显示阈值：约一屏半。
 * 不足一屏时列表根本没有「顶部可回」，按钮会变成无意义的存在。
 */
const SCROLL_TOP_THRESHOLD_PX = 600

/** PullToRefresh 实例：只为拿到它的滚动容器做程序化滚动。 */
const pullRef = ref<InstanceType<typeof PullToRefresh> | null>(null)
const scrollTop = ref(0)
const showScrollTop = computed(() => scrollTop.value > SCROLL_TOP_THRESHOLD_PX)

function onScrollPosition(top: number) {
  scrollTop.value = top
}

/**
 * 回到顶部。
 *
 * 用平滑滚动而不是瞬移：瞬移会让长列表在两帧内重排上百个卡片，
 * 真机上表现为明显卡顿；平滑滚动顺带把 chrome 唤出逻辑也走了一遍。
 * 尊重系统的 prefers-reduced-motion——这类用户对大幅位移动画敏感。
 */
function scrollToTop() {
  const el = pullRef.value?.scrollEl
  if (!el) {
    scrollTop.value = 0
    return
  }
  const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  el.scrollTo({ top: 0, behavior: reduce ? 'auto' : 'smooth' })
}

/**
 * 追加完一页后补一次哨兵检查。
 *
 * IntersectionObserver 只在「交叉状态变化」时回调。若一页加载完时哨兵**仍在视口内**
 * （比如一屏能放下两页），就不会产生新的交叉事件，列表就永远停在这一页——
 * 这是无限滚动最常见的「只加载一页就停」。所以每轮加载后主动补判一次。
 */
async function recheckSentinel() {
  await nextTick()
  const el = moreEl.value
  if (!el) return
  const sentinelTop = el.getBoundingClientRect().top
  const viewportHeight = window.innerHeight || document.documentElement.clientHeight
  // 内核没有内建这个补判（它的 rootMargin 240px 覆盖了绝大多数情形，
  // 但「追加后哨兵仍持续相交 ⇒ IO 不再回调」这个边界仍需调用方兜一次）。
  if (!hasMore.value || pageLoading.value) return
  if (sentinelTop > viewportHeight + 120) return
  await loadMore()
  // 递归直到填满或到底（每层都让出一帧，避免同步递归卡住主线程）。
  if (hasMore.value && !pageLoading.value) void recheckSentinel()
}

/** 下拉刷新：只拉最新并合并到顶部，保留已加载的分页与滚动位置。 */
async function onRefresh() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    const before = new Set(emails.value.map((m) => m.id))
    await pullInboxFromServer()
    // 内核的 merge：第 1 页新值并到顶部、**保留已翻开的分页**、游标不退回
    // （preserveCursor）。这正是 07 §2.8 那个缺陷的正确形态。
    await refreshList()
    const added = emails.value.filter((m) => !before.has(m.id)).length
    syncHint.value = added > 0 ? `新增 ${added} 封邮件` : '已是最新'
  } catch (e: any) {
    syncHint.value = sanitizeFetchHint(e?.message || '') || '刷新失败'
  } finally {
    refreshing.value = false
  }
}
function setCategory(c: string) {
  if (activeCategory.value === c) return
  activeCategory.value = c
  // 换分类 = 换一份数据：提升代次、清游标、回到首页（内核内部完成）。
  resetListQuery()
}
/**
 * 点进详情：**不等待**任何网络。
 *
 * 真机反馈「点进邮件详情失败或非常慢」的关键修复点：点击的**瞬间**就发起正文
 * 预取，让它与路由跳转、组件挂载并行。等到详情页真正要读正文时，请求多半已经
 * 在途甚至完成，首屏几乎无等待。跳转本身永远不被网络拖住。
 */
function open(id: string) {
  void prefetchEmailBody(id, bodyPrefetchDeps)
  // 顺手把「下一封」也热好：用户看完返回几乎总是往下滑看下一封，
  // 提前热好能让这趟往返同样是秒开。
  const idx = shownEmails.value.findIndex((m) => m.id === id)
  if (idx >= 0) {
    const ahead = shownEmails.value.slice(idx + 1, idx + 3).map((m) => m.id)
    if (ahead.length) void prefetchEmailBodySeries(ahead, bodyPrefetchDeps, 2)
  }
  router.push(`/email/${id}`)
}

async function markRead(m: LocalEmail, read: boolean) {
  await emailsStore.markRead(m.id, read)
  m.isRead = read
}

watch(() => inbox.search.value, (s) => {
  setHeaderTitle(formatInboxSearchLabel(s) || null)
}, { deep: true })
// 目录视图标题：进入目录时把页头换成目录名，返回收件箱恢复默认。
watch(activeFolder, (f) => {
  setHeaderTitle(f ? `目录：${f}` : null)
  resetListQuery()
})
// 目录页点目录跳 /email?folder=x：KeepAlive 下组件不重建，靠路由查询驱动。
watch(() => route.query.folder, (v) => {
  const f = typeof v === 'string' ? v : ''
  if (f !== activeFolder.value) activeFolder.value = f
})
onMounted(load)
/* KeepAlive 现场保持：筛选/分类保留在组件实例上；仅当详情页登记过
   email 数据变更（已读/加星等）才刷新，并恢复滚动位置。 */
useListScene('email', load)
onUnmounted(() => setHeaderTitle(null))
</script>

<style scoped>
.inbox-page { display: flex; flex-direction: column; height: 100%; min-height: 0; position: relative; }
.chat-icon-btn { width: 40px; height: 40px; display: flex; align-items: center; justify-content: center; border: none; border-radius: var(--radius-md); background: transparent; color: var(--text-secondary); }
.chat-icon-btn:active { background: var(--bg-hover); }
.more-menu { position: absolute; right: 8px; top: 8px; z-index: 4; display: flex; flex-direction: column; background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius-md); }
.more-menu button { border: none; background: transparent; text-align: left; padding: 10px 14px; color: var(--text-primary); }
.search-bar { display: flex; flex-wrap: wrap; gap: 6px; padding: 0 var(--space-3) var(--space-2); }
.search-input { flex: 1 1 140px; min-height: 36px; border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 0 8px; background: var(--bg-card); color: var(--text-primary); font-size: var(--text-smd); }
.search-input.slim { flex: 0 1 110px; }
.search-ok { min-height: 36px; padding: 0 12px; border: none; border-radius: var(--radius-sm); background: var(--brand-primary); color: var(--text-inverse); }
.search-ok.ghost { background: transparent; color: var(--text-secondary); border: 1px solid var(--border); }
.pick { display: flex; align-items: center; margin-right: 8px; }
.card-main { flex: 1; min-width: 0; }
.linkish { border: none; background: none; color: var(--brand-primary); font-size: var(--text-2xs); }
.filters { display: flex; gap: var(--space-2); overflow-x: auto; padding: var(--space-3);
  /* 横向 chip 条隐藏滚动条：与 AIChatView.context-row / NoteListView.context-row 一致，
     真机实测未隐藏时 chip 行下方常驻一条突兀灰条。 */
  scrollbar-width: none;
  -ms-overflow-style: none;
}
.filters::-webkit-scrollbar { display: none; }
.chip { padding: var(--space-1) var(--space-3); border-radius: var(--radius-full); border: 1px solid var(--border); background: var(--bg-card); color: var(--text-secondary); font-size: var(--text-sm); white-space: nowrap; }
.chip.active { background: var(--brand-primary); color: var(--text-inverse); border-color: var(--brand-primary); }
.inbox-scroll { flex: 1; min-height: 0; }
.state-wrap { padding: var(--space-2) 0; }
/* 有旧邮件时的失败提示：不替换列表，只在列表上方挂一条（2026-10-05）。
   与 SessionListView 的同名类**逐字一致** —— 同一形状的缺陷、同一种修法，
   门禁 list-error-failpath 逐个视图检查这个约定。 */
.list-error-banner {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  background: var(--danger-bg);
  color: var(--danger);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  border-top: 1px solid rgba(239, 68, 68, 0.2);
}
.banner-retry {
  flex: 0 0 auto;
  background: transparent;
  color: inherit;
  border: 1px solid currentColor;
  border-radius: var(--radius-sm);
  padding: 2px var(--space-2);
  font-size: var(--text-sm);
  cursor: pointer;
}
.email-list { display: flex; flex-direction: column; gap: var(--spacing-list-gap); position: relative; }
/* 增量同步动画：新邮件自上滑入、删除滑出并让位、其余项平滑上移补位 */
.elist-enter-active { transition: opacity 0.35s ease, transform 0.35s ease; }
.elist-enter-from { opacity: 0; transform: translateY(-10px); }
.elist-leave-active { transition: opacity 0.25s ease, transform 0.25s ease; position: absolute; left: 0; right: 0; }
.elist-leave-to { opacity: 0; transform: translateX(28px); }
.elist-move { transition: transform 0.3s ease; }
.email-card { display: flex; background: var(--bg-card); border-radius: var(--radius-md); padding: var(--spacing-card-padding); border: 1px solid var(--border); border-left: 3px solid transparent; }
.email-card.high { border-left-color: var(--danger); }
.email-card.unread { background: var(--bg-elevated); }
.row1 { display: flex; justify-content: space-between; font-size: var(--text-smd); margin-bottom: 2px; }
.from { font-weight: 600; color: var(--text-primary); }
.time { color: var(--text-muted); font-size: var(--text-2xs); }
.subject { font-size: var(--text-base); font-weight: 500; margin-bottom: var(--space-1); }
.snippet { color: var(--text-secondary); font-size: var(--text-sm); -webkit-line-clamp: 1; -webkit-box-orient: vertical; display: -webkit-box; overflow: hidden; }
.ai-summary { margin-top: var(--space-1); font-size: var(--text-sm); color: var(--brand-primary); background: var(--bg-subtle); padding: var(--space-1) var(--space-2); border-radius: var(--radius-sm); }
/* 判定依据：与 .ai-summary 同族的提示块，但用中性色 + 左侧竖线，视觉权重低于摘要。
   不截断（换行完整显示）——理由是判为重要的唯一依据，截掉等于没给。 */
.reason { margin-top: var(--space-1); font-size: var(--text-xs); color: var(--text-secondary); background: var(--bg-subtle); padding: var(--space-1) var(--space-2); border-left: 2px solid var(--warning); border-radius: var(--radius-sm); word-break: break-word; }
.row-meta { display: flex; gap: var(--space-2); align-items: center; margin-top: var(--space-2); }
.tag { font-size: var(--text-xs); padding: 1px 6px; border-radius: var(--radius-sm); }
.cat-work { background: var(--cat-work-bg); color: var(--cat-work); }
.cat-bill { background: var(--cat-bill-bg); color: var(--cat-bill); }
.cat-personal { background: var(--cat-personal-bg); color: var(--cat-personal); }
.cat-notification { background: var(--cat-notification-bg); color: var(--cat-notification); }
.cat-marketing { background: var(--cat-marketing-bg); color: var(--cat-marketing); }
.cat-spam { background: var(--cat-spam-bg); color: var(--cat-spam); }
.importance { font-size: var(--text-2xs); color: var(--warning); }
.read-btn { margin-left: auto; font-size: var(--text-2xs); padding: 2px 8px; border-radius: var(--radius-sm); border: 1px solid var(--border); background: var(--bg-card); color: var(--brand-primary); }
.sync-hint { margin: 0 var(--space-3) var(--space-2); font-size: var(--text-2xs); color: var(--text-muted); }
.more { padding: 16px 0 24px; text-align: center; font-size: var(--text-sm); color: var(--text-muted); }
.more-loading { display: inline-flex; align-items: center; gap: 6px; }
.more-spin { font-size: var(--text-md); color: var(--brand-primary); animation: more-spin 900ms linear infinite; }
@keyframes more-spin { to { transform: rotate(360deg); } }
.more-end { opacity: .7; }
/* 加载更多三态互切：淡入淡出 + 轻微上移，避免文案硬切。 */
.morefade-enter-active, .morefade-leave-active { transition: opacity .2s ease, transform .2s ease; }
.morefade-enter-from { opacity: 0; transform: translateY(4px); }
.morefade-leave-to { opacity: 0; transform: translateY(-4px); }

/* 回顶按钮：右下悬浮，圆形。expand_more 本身就是向上双箭头，无需再 rotate。 */
.to-top {
  position: absolute; right: 14px; bottom: 18px; z-index: 5;
  width: 40px; height: 40px; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  border: 1px solid var(--border); background: var(--bg-card);
  color: var(--brand-primary); box-shadow: 0 4px 14px rgba(0,0,0,.16);
  cursor: pointer; padding: 0;
}
.to-top:active { background: var(--bg-hover); }
.to-top .material-symbols-outlined { font-size: 22px; }
/* 浮现/隐去：上移 + 淡入，像从列表里「浮起来」。 */
.totop-enter-active { transition: opacity .24s var(--ease-out), transform .24s var(--ease-out); }
.totop-leave-active { transition: opacity .18s var(--ease-out), transform .18s var(--ease-out); }
.totop-enter-from, .totop-leave-to { opacity: 0; transform: translateY(10px) scale(.9); }

</style>
