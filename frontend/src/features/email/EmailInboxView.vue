<!--
  EmailInboxView — aggregated inbox across IMAP accounts with AI category
  and importance filters. Skeleton page; full body rendering + account
  setup wizard come later.
-->
<template>
  <div class="inbox-page">
    <DbLockedState
      v-if="dbNotReady"
      hint="邮箱功能需要本地加密数据库"
      @relogin="goToLogin"
    />

    <template v-else>
      <!-- 标题栏右侧：发票整理 / 邮箱设置（账户 / 过滤策略 / 处理逻辑） -->
      <HeaderActionsPortal>
        <template v-if="inbox.selectMode.value">
          <button class="chat-icon-btn" type="button" aria-label="取消选择" @click="inbox.exitSelect()">
            <span class="material-symbols-outlined" aria-hidden="true">close</span>
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
          <button class="chat-icon-btn" type="button" aria-label="删除" @click="inbox.enterSelect()">
            <span class="material-symbols-outlined" aria-hidden="true">delete</span>
          </button>
          <button class="chat-icon-btn" type="button" aria-label="更多" @click="inbox.moreOpen.value = !inbox.moreOpen.value">
            <span class="material-symbols-outlined" aria-hidden="true">more_vert</span>
          </button>
        </template>
      </HeaderActionsPortal>
      <div v-if="inbox.moreOpen.value && !inbox.selectMode.value" class="more-menu">
        <button type="button" @click="go('/email/invoices')">发票整理</button>
        <button type="button" @click="go('/email/cleanup')">清理垃圾</button>
        <button type="button" @click="go('/email/settings')">邮箱设置</button>
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
        </div>
      </ScrollChromePortal>

      <PullToRefresh :on-refresh="load" class="inbox-scroll">
    <p v-if="inbox.classifyHint.value" class="sync-hint">
      {{ inbox.classifyHint.value }}
      <button v-if="inbox.classifying.value" type="button" class="linkish" @click="inbox.classifyCancel.value = true">取消</button>
    </p>
    <p v-if="syncHint" class="sync-hint">{{ syncHint }}</p>
    <div v-if="loading" class="state-wrap"><Skeleton :count="5" /></div>
    <EmptyState
      v-else-if="loadError"
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

    <div v-else class="email-list">
      <div
        v-for="m in shownEmails"
        :key="m.id"
        class="email-card"
        :class="{ high: m.importance === 'high', unread: !m.isRead }"
        @click="inbox.selectMode.value ? inbox.toggle(m.id) : open(m.id)"
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
        <div class="row-meta">
          <span v-if="m.category" class="tag" :class="`cat-${m.category}`">{{ catLabel(m.category) }}</span>
          <span v-if="m.importance === 'high'" class="importance">⭐ 重要</span>
          <span v-if="m.hasAttachments" class="attach">📎</span>
          <button v-if="!m.isRead" class="read-btn" @click.stop="markRead(m, true)">标为已读</button>
        </div>
        </div>
      </div>
      <div v-if="emails.length > 0" ref="moreEl" class="more">
        <span v-if="loadingMore">加载中…</span>
        <span v-else-if="hasMore">上拉加载更多</span>
        <span v-else>没有更多了</span>
      </div>
    </div>
    </PullToRefresh>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Skeleton, EmptyState, PullToRefresh, DbLockedState } from '../../components'
import ScrollChromePortal from '@/components/layout/ScrollChromePortal.vue'
import HeaderActionsPortal from '@/components/layout/HeaderActionsPortal.vue'
import { useListSentinel } from '../../composables/use-list-sentinel'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'
import { inboxHasMore, readInboxPage } from './email-inbox-page'
import { runDelegatedEmailFetch } from './email-fetch-run'
import { formatEmailRelTime } from './cleanup-filter'
import { INBOX_CATEGORY_CHIPS, catLabel } from './email-categories'
import { useEmailInbox } from './use-email-inbox'

const router = useRouter()
const emails = ref<LocalEmail[]>([])
const loading = ref(true)
const loadingMore = ref(false)
const hasMore = ref(false)
const loadError = ref('')
const activeCategory = ref<string>('')
const dbNotReady = ref(false)
const syncHint = ref('')
const inbox = useEmailInbox()
const categoryChips = INBOX_CATEGORY_CHIPS
const sinceLocal = ref('')
const untilLocal = ref('')
const shownEmails = computed(() => inbox.visibleEmails(emails.value))

function goToLogin() {
  router.push('/login')
}
function go(path: string) {
  inbox.moreOpen.value = false
  router.push(path)
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
  await load()
}

async function onPurge() {
  if (!inbox.selectedCount.value) return
  if (!window.confirm(`删除选中的 ${inbox.selectedCount.value} 封邮件？正文将清空，仅保留标题和摘要。`)) return
  await inbox.confirmPurge()
  await load()
}

async function load() {
  loading.value = true
  loadError.value = ''
  dbNotReady.value = false
  try {
    const page = await readInboxPage(activeCategory.value, 0)
    emails.value = page
    hasMore.value = inboxHasMore(page.length)
    if (page.length) loading.value = false
  } catch (e: any) {
    if (e?.message?.includes('LocalDB 未初始化')) dbNotReady.value = true
    else loadError.value = e?.message || '加载邮件失败'
  }
  void runDelegatedEmailFetch({ classify: true }).then(async ({ hint }) => {
    syncHint.value = hint
    try {
      const page = await readInboxPage(activeCategory.value, 0)
      emails.value = page
      hasMore.value = inboxHasMore(page.length)
    } catch { /* 保持已上屏的本地列表 */ }
    loading.value = false
  })
}

async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value) return
  loadingMore.value = true
  try {
    const page = await readInboxPage(activeCategory.value, emails.value.length)
    emails.value = [...emails.value, ...page.filter((m) => !emails.value.some((e) => e.id === m.id))]
    hasMore.value = inboxHasMore(page.length)
  } finally {
    loadingMore.value = false
  }
}

const { moreEl } = useListSentinel(loadMore)
function setCategory(c: string) {
  activeCategory.value = c
  load()
}
function open(id: string) { router.push(`/email/${id}`) }

async function markRead(m: LocalEmail, read: boolean) {
  await emailsStore.markRead(m.id, read)
  m.isRead = read
}

onMounted(load)
</script>

<style scoped>
.inbox-page { display: flex; flex-direction: column; height: 100%; min-height: 0; position: relative; }
.chat-icon-btn { width: 40px; height: 40px; display: flex; align-items: center; justify-content: center; border: none; border-radius: var(--radius-md); background: transparent; color: var(--text-secondary); }
.chat-icon-btn:active { background: var(--bg-hover); }
.more-menu { position: absolute; right: 8px; top: 8px; z-index: 4; display: flex; flex-direction: column; background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius-md); }
.more-menu button { border: none; background: transparent; text-align: left; padding: 10px 14px; color: var(--text-primary); }
.search-bar { display: flex; flex-wrap: wrap; gap: 6px; padding: 0 var(--space-3) var(--space-2); }
.search-input { flex: 1 1 140px; min-height: 36px; border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 0 8px; background: var(--bg-card); color: var(--text-primary); font-size: 13px; }
.search-input.slim { flex: 0 1 110px; }
.pick { display: flex; align-items: center; margin-right: 8px; }
.card-main { flex: 1; min-width: 0; }
.linkish { border: none; background: none; color: var(--brand-primary); font-size: 11px; }
.filters { display: flex; gap: var(--space-2); overflow-x: auto; padding: var(--space-3); }
.chip { padding: var(--space-1) var(--space-3); border-radius: var(--radius-full); border: 1px solid var(--border); background: var(--bg-card); color: var(--text-secondary); font-size: 12px; white-space: nowrap; }
.chip.active { background: var(--brand-primary); color: var(--text-inverse); border-color: var(--brand-primary); }
.inbox-scroll { flex: 1; min-height: 0; }
.state-wrap { padding: var(--space-2) 0; }
.email-list { display: flex; flex-direction: column; gap: var(--spacing-list-gap); }
.email-card { display: flex; background: var(--bg-card); border-radius: var(--radius-md); padding: var(--spacing-card-padding); border: 1px solid var(--border); border-left: 3px solid transparent; }
.email-card.high { border-left-color: var(--danger); }
.email-card.unread { background: var(--bg-elevated); }
.row1 { display: flex; justify-content: space-between; font-size: 13px; margin-bottom: 2px; }
.from { font-weight: 600; color: var(--text-primary); }
.time { color: var(--text-muted); font-size: 11px; }
.subject { font-size: 14px; font-weight: 500; margin-bottom: var(--space-1); }
.snippet { color: var(--text-secondary); font-size: 12px; -webkit-line-clamp: 1; -webkit-box-orient: vertical; display: -webkit-box; overflow: hidden; }
.ai-summary { margin-top: var(--space-1); font-size: 12px; color: var(--brand-primary); background: var(--bg-subtle); padding: var(--space-1) var(--space-2); border-radius: var(--radius-sm); }
.row-meta { display: flex; gap: var(--space-2); align-items: center; margin-top: var(--space-2); }
.tag { font-size: 10px; padding: 1px 6px; border-radius: var(--radius-sm); }
.cat-work { background: var(--cat-work-bg); color: var(--cat-work); }
.cat-bill { background: var(--cat-bill-bg); color: var(--cat-bill); }
.cat-personal { background: var(--cat-personal-bg); color: var(--cat-personal); }
.cat-notification { background: var(--cat-notification-bg); color: var(--cat-notification); }
.cat-marketing { background: var(--cat-marketing-bg); color: var(--cat-marketing); }
.cat-spam { background: var(--cat-spam-bg); color: var(--cat-spam); }
.importance { font-size: 11px; color: var(--warning); }
.read-btn { margin-left: auto; font-size: 11px; padding: 2px 8px; border-radius: var(--radius-sm); border: 1px solid var(--border); background: var(--bg-card); color: var(--brand-primary); }
.sync-hint { margin: 0 var(--space-3) var(--space-2); font-size: 11px; color: var(--text-muted); }
.more { padding: 16px 0 24px; text-align: center; font-size: 12px; color: var(--text-muted); }
</style>
