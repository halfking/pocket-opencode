<!--
  EmailInboxView — aggregated inbox across IMAP accounts with AI category
  and importance filters. Skeleton page; full body rendering + account
  setup wizard come later.
-->
<template>
  <AppLayout>
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
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'

const router = useRouter()
const emails = ref<LocalEmail[]>([])
const loading = ref(true)
const activeCategory = ref<string>('')

const categories: { label: string; value: string }[] = [
  { label: '全部', value: '' },
  { label: '工作', value: 'work' },
  { label: '账单', value: 'bill' },
  { label: '私人', value: 'personal' },
  { label: '通知', value: 'notification' },
]

async function load() {
  loading.value = true
  try {
    emails.value = await emailsStore.listEmails(
      activeCategory.value ? { category: activeCategory.value } : {},
    )
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
.filters { display: flex; gap: var(--space-2); overflow-x: auto; padding-bottom: var(--space-3); }
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
