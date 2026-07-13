<!--
  EmailInboxView — aggregated inbox across IMAP accounts with AI category
  and importance filters. Skeleton page; full body rendering + account
  setup wizard come later.
-->
<template>
  <AppLayout>
    <!-- 本地数据库未初始化提示 -->
    <div v-if="dbNotReady" class="state" style="padding: 40px 20px;">
      <p style="font-size: 48px; margin-bottom: 16px;">🔒</p>
      <p style="font-size: 16px; font-weight: 600; margin-bottom: 8px;">本地数据未解锁</p>
      <p style="font-size: 13px; color: var(--text-secondary); margin-bottom: 16px;">
        邮箱功能需要本地加密数据库<br/>请退出重新登录以初始化本地存储
      </p>
      <button class="btn-ghost" @click="goToLogin" style="margin: 0 auto; padding: 8px 24px; border: 1px solid var(--border); border-radius: 8px;">
        重新登录
      </button>
    </div>

    <template v-else>
    <div class="toolbar">
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
      <button class="sync-btn" :disabled="syncing" @click="syncNow">
        {{ syncing ? '同步中…' : '↻ 同步' }}
      </button>
    </div>

    <p v-if="syncMessage" class="sync-message">{{ syncMessage }}</p>

    <div v-if="loading" class="state">加载中…</div>
    <div v-else-if="emails.length === 0" class="state">暂无邮件</div>

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
    </template>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import { emailApi } from '../../api/email'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'

const router = useRouter()
const emails = ref<LocalEmail[]>([])
const loading = ref(true)
const activeCategory = ref<string>('')
const dbNotReady = ref(false)
const syncing = ref(false)
const syncMessage = ref('')

function goToLogin() {
  router.push('/login')
}

const categories: { label: string; value: string }[] = [
  { label: '全部', value: '' },
  { label: '工作', value: 'work' },
  { label: '账单', value: 'bill' },
  { label: '私人', value: 'personal' },
  { label: '通知', value: 'notification' },
]

async function load() {
  loading.value = true
  dbNotReady.value = false
  try {
    emails.value = await emailsStore.listEmails(
      activeCategory.value ? { category: activeCategory.value } : {},
    )
  } catch (e: any) {
    if (e?.message?.includes('LocalDB 未初始化')) {
      dbNotReady.value = true
      console.warn('[email] 本地数据库未初始化，显示降级界面')
    } else {
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

async function syncNow() {
  if (syncing.value) return
  syncing.value = true
  syncMessage.value = ''
  try {
    const result = await emailApi.syncNow()
    syncMessage.value = result.new
      ? `同步完成：新增 ${result.new} 封邮件`
      : '同步完成：没有新邮件'
    await load()
  } catch (e: any) {
    syncMessage.value = e?.message || '同步失败，请检查邮箱配置'
  } finally {
    syncing.value = false
  }
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
.filters { display: flex; gap: var(--space-2); overflow-x: auto; padding-bottom: var(--space-3); }
.toolbar { display: flex; align-items: flex-start; gap: var(--space-2); }
.toolbar .filters { flex: 1; min-width: 0; }
.sync-btn {
  flex-shrink: 0;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  color: var(--brand-primary);
  padding: 5px 9px;
  font-size: 12px;
  cursor: pointer;
}
.sync-btn:disabled { opacity: 0.6; cursor: wait; }
.sync-message { margin: 0 0 var(--space-2); color: var(--text-secondary); font-size: 12px; }
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
.chip.active { background: var(--brand-primary); color: white; border-color: var(--brand-primary); }
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.email-list { display: flex; flex-direction: column; gap: var(--space-2); }
.email-card {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  box-shadow: var(--shadow-sm);
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
.cat-work { background: rgba(59,130,246,0.15); color: var(--cat-work); }
.cat-bill { background: rgba(245,158,11,0.15); color: var(--cat-bill); }
.cat-personal { background: rgba(236,72,153,0.15); color: var(--cat-personal); }
.cat-notification { background: rgba(107,114,128,0.15); color: var(--cat-notification); }
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
