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
        <button
          class="chat-icon-btn"
          type="button"
          aria-label="发票整理"
          @click="router.push('/email/invoices')"
        >
          <span class="material-symbols-outlined" aria-hidden="true">receipt_long</span>
        </button>
        <button
          class="chat-icon-btn"
          type="button"
          aria-label="邮箱设置"
          @click="router.push('/email/settings')"
        >
          <span class="material-symbols-outlined" aria-hidden="true">settings</span>
        </button>
      </HeaderActionsPortal>

      <ScrollChromePortal>
        <div class="filters">
          <button
            v-for="c in categories"
            :key="c.value || 'all'"
            class="chip"
            :class="{ active: activeCategory === c.value }"
            @click="setCategory(c.value)"
          >
            {{ c.label }}
          </button>
        </div>
      </ScrollChromePortal>

      <PullToRefresh :on-refresh="load" class="inbox-scroll">
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
      v-else-if="emails.length === 0"
      icon="📧"
      title="暂无邮件"
      hint="添加邮箱账户并同步后即可在此查看"
      size="sm"
      variant="inline"
    />

    <div v-else class="email-list">
      <div
        v-for="m in emails"
        :key="m.id"
        class="email-card"
        :class="{ high: m.importance === 'high', unread: !m.isRead }"
        @click="open(m.id)"
      >
        <div class="row1">
          <span class="from">{{ m.fromName || m.fromAddress }}</span>
          <span class="time">{{ relTime(m.date) }}</span>
        </div>
        <div class="subject">{{ m.subject }}</div>
        <div class="snippet">{{ m.snippet }}</div>
        <div v-if="m.aiSummary" class="ai-summary">💡 {{ m.aiSummary }}</div>
        <div class="row-meta">
          <span v-if="m.category" class="tag" :class="`cat-${m.category}`">{{ catLabel(m.category) }}</span>
          <span v-if="m.importance === 'high'" class="importance">⭐ 重要</span>
          <span v-if="m.hasAttachments" class="attach">📎</span>
          <button
            v-if="!m.isRead"
            class="read-btn"
            @click.stop="markRead(m, true)"
          >标为已读</button>
        </div>
      </div>
    </div>
    </PullToRefresh>
    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Skeleton, EmptyState, PullToRefresh, DbLockedState } from '../../components'
import ScrollChromePortal from '@/components/layout/ScrollChromePortal.vue'
import HeaderActionsPortal from '@/components/layout/HeaderActionsPortal.vue'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'
import { emailApi } from '../../api/email'

const router = useRouter()
const emails = ref<LocalEmail[]>([])
const loading = ref(true)
const loadError = ref('')
const activeCategory = ref<string>('')
const dbNotReady = ref(false)

function goToLogin() {
  router.push('/login')
}

const categories: { label: string; value: string }[] = [
  { label: '全部', value: '' },
  { label: '重要', value: '__important' },         // 虚拟类目：importance='high'
  { label: '垃圾', value: '__spam' },             // 虚拟类目：category='spam'
  { label: '工作', value: 'work' },
  { label: '账单', value: 'bill' },
  { label: '私人', value: 'personal' },
  { label: '通知', value: 'notification' },
]

async function load() {
  loading.value = true
  loadError.value = ''
  dbNotReady.value = false
  try {
    // 在线时先从服务端拉一遍最近邮件，upsert 到本地库（imap_fetch 后置 UX）；
    // 离线时只吃本地镜像。失败一次不阻塞后续 listEmails。
    try {
      await emailsStore.syncEmailsFromServer(200)
    } catch (e: any) {
      console.warn('[email] sync from server:', e?.message || e)
    }
    const cat = activeCategory.value
    const filter = cat === '__important'
      ? { importance: 'high' }
      : cat === '__spam'
        ? { category: 'spam' }
        : cat
          ? { category: cat }
          : {}
    emails.value = await emailsStore.listEmails(filter)
  } catch (e: any) {
    if (e?.message?.includes('LocalDB 未初始化')) {
      dbNotReady.value = true
    } else {
      loadError.value = e?.message || '加载邮件失败'
      console.error('[email] 加载失败:', e)
    }
  } finally {
    loading.value = false
  }
}
function setCategory(c: string) {
  activeCategory.value = c
  load()
}
function open(id: string) { router.push(`/email/${id}`) }

const catLabel = (c: string | null) =>
  ({ work: '工作', bill: '账单', notification: '通知', personal: '私人', marketing: '营销', spam: '垃圾' }[c || ''] || c)

async function markRead(m: LocalEmail, read: boolean) {
  await emailsStore.markRead(m.id, read)
  m.isRead = read
}

function relTime(ms: number) {
  const diff = Date.now() - ms
  const hr = Math.floor(diff / 3600000)
  if (hr < 1) return `${Math.floor(diff / 60000)}分钟前`
  if (hr < 24) return `${hr}小时前`
  return `${Math.floor(hr / 24)}天前`
}

onMounted(load)
</script>

<style scoped>
.inbox-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

/* 标题栏右侧设置入口（与 AIChatView 的 chat-icon-btn 同款视觉） */
.chat-icon-btn {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}
.chat-icon-btn:active { background: var(--bg-hover); }

.filters {
  display: flex;
  gap: var(--space-2);
  overflow-x: auto;
  padding: var(--space-3);
}
.chip {
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-full);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-secondary);
  font-size: 12px;
  white-space: nowrap;
  cursor: pointer;
}
.chip.active { background: var(--brand-primary); color: var(--text-inverse); border-color: var(--brand-primary); }
.inbox-scroll {
  flex: 1;
  min-height: 0;
}
.state-wrap { padding: var(--space-2) 0; }
.email-list { display: flex; flex-direction: column; gap: var(--spacing-list-gap); }
.email-card {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--spacing-card-padding);
  border: 1px solid var(--border);
  cursor: pointer;
  border-left: 3px solid transparent;
}
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
.attach { font-size: 12px; }
.read-btn {
  margin-left: auto;
  font-size: 11px;
  padding: 2px 8px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--brand-primary);
  cursor: pointer;
}
.read-btn:active { background: var(--bg-subtle); }
</style>
