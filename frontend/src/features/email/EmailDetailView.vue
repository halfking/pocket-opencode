<!--
  EmailDetailView — single email detail page.
  Loads via emailsStore.listEmails() + filter (temporary until prompt 6
  adds emailsStore.getEmail; replace the lookup once available).
  Auto-marks unread → read on open. Star / mark-read / turn-to-todo actions.
-->
<template>
  <AppLayout>
    <div v-if="loading" class="state">加载中…</div>
    <div v-else-if="!email" class="state">
      <p>未找到该邮件（可能已被删除）。</p>
      <button class="link-btn" @click="goBack">返回邮箱</button>
    </div>

    <article v-else class="detail">
      <header class="meta-card">
        <div class="from-row">
          <div class="from-block" @click="navigateToContact">
            <span class="from-name">{{ email.fromName || email.fromAddress }}</span>
            <span v-if="email.fromName" class="from-addr">&lt;{{ email.fromAddress }}&gt;</span>
          </div>
          <button
            class="star-btn"
            :class="{ active: email.isStarred }"
            @click="toggleStar"
            :aria-label="email.isStarred ? '取消星标' : '加星'"
          >{{ email.isStarred ? '⭐' : '☆' }}</button>
        </div>
        <h2 class="subject">{{ email.subject || '(无主题)' }}</h2>
        <div class="date-row">
          <span class="date">{{ formatDate(email.date) }}</span>
          <span v-if="email.hasAttachments" class="attach">📎 有附件</span>
        </div>
        <div class="tag-row">
          <span v-if="email.category" class="tag" :class="`cat-${email.category}`">
            {{ catLabel(email.category) }}
          </span>
          <span v-if="email.importance === 'high'" class="importance">⭐ 重要</span>
          <span v-else-if="email.importance === 'medium'" class="importance low">一般</span>
          <span v-else-if="email.importance === 'low'" class="importance low">低优</span>
        </div>
      </header>

      <section v-if="email.aiSummary" class="ai-card">
        <div class="ai-title">💡 AI 摘要</div>
        <div class="ai-body">{{ email.aiSummary }}</div>
        <div v-if="email.suggestedAction" class="ai-action">
          <span class="action-label">建议操作</span>
          <span class="action-text">{{ actionLabel(email.suggestedAction) }}</span>
        </div>
      </section>

      <section class="snippet-card">
        <div class="snippet-label">正文预览</div>
        <div class="snippet-body">
          <template v-if="email.snippet">{{ email.snippet }}</template>
          <span v-else class="muted">(无正文预览)</span>
        </div>
      </section>

      <div class="actions">
        <button
          class="action-btn"
          :class="{ done: email.isRead }"
          @click="toggleRead"
        >
          {{ email.isRead ? '✓ 已读' : '标为已读' }}
        </button>
        <button class="action-btn" :disabled="converting" @click="convertToTodo">
          {{ converting ? '创建中…' : '转 Todo' }}
        </button>
      </div>

      <p class="hint">当前本地仅保存邮件摘要片段（约 500 字），完整正文不落本地。</p>
    </article>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import { api } from '../../api/client'
import { useToast } from '../../composables/useToast'
import { findContactByEmail } from '../contact/contacts-store'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'

const route = useRoute()
const router = useRouter()
const toast = useToast()

const email = ref<LocalEmail | null>(null)
const loading = ref(true)
const converting = ref(false)

async function load() {
  const id = route.params.id as string
  loading.value = true
  try {
    // 使用 getEmail（emails-store 已提供）
    const found = await emailsStore.getEmail(id)
    email.value = found
    if (found && !found.isRead) {
      // 自动标已读（后台触发，不阻塞 UI）
      try {
        await emailsStore.markRead(found.id, true)
        found.isRead = true
      } catch (e) {
        console.warn('[email] 自动标记已读失败:', e)
      }
    }
  } finally {
    loading.value = false
  }
}

async function toggleRead() {
  if (!email.value) return
  const next = !email.value.isRead
  await emailsStore.markRead(email.value.id, next)
  email.value.isRead = next
}

async function toggleStar() {
  if (!email.value) return
  email.value.isStarred = !email.value.isStarred
  await emailsStore.setStarred(email.value.id, email.value.isStarred)
}

async function convertToTodo() {
  if (!email.value || converting.value) return
  converting.value = true
  const subject = email.value.subject || '(无主题)'
  const from = email.value.fromName || email.value.fromAddress
  try {
    const task = await api.createTask({
      title: subject,
      description: `${email.value.snippet || ''}\n\n来自：${from}`.trim(),
      source: 'local',
      status: 'active',
      priority: email.value.importance === 'high' ? 'high' : 'medium',
    })
    toast.success(`已转为任务：${task.title}`)
    router.push(`/tasks/${task.id}`)
  } catch (e: any) {
    toast.error(e?.message || '创建任务失败')
  } finally {
    converting.value = false
  }
}

async function navigateToContact() {
  if (!email.value?.fromAddress) return
  try {
    const contact = await findContactByEmail(email.value.fromAddress)
    if (contact) {
      router.push(`/contacts/${contact.id}`)
    } else {
      toast.info('联系人不存在，请先在联系人页面聚合')
    }
  } catch (error: any) {
    toast.error(error?.message || '查找联系人失败')
  }
}

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/email')
}

function formatDate(ms: number) {
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const catLabel = (c: string | null) =>
  ({
    work: '工作', bill: '账单', notification: '通知',
    personal: '私人', marketing: '营销', spam: '垃圾',
  }[c || ''] || c || '')

const actionLabel = (a: string) =>
  ({ reply: '回复', archive: '归档', todo: '待办', ignore: '忽略' }[a] || a)

onMounted(load)
</script>

<style scoped>
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.link-btn {
  margin-top: var(--space-3);
  background: transparent;
  border: none;
  color: var(--brand-primary);
  font-size: 14px;
  cursor: pointer;
}
.detail { display: flex; flex-direction: column; gap: var(--space-3); padding-bottom: var(--space-6); }
.meta-card {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-4);
  box-shadow: var(--shadow-sm);
}
.from-row { display: flex; justify-content: space-between; align-items: flex-start; gap: var(--space-2); }
.from-block { display: flex; flex-direction: column; min-width: 0; }
.from-name { font-weight: 600; color: var(--text-primary); font-size: 15px; }
.from-addr { color: var(--text-muted); font-size: 12px; word-break: break-all; }
.star-btn {
  border: none;
  background: transparent;
  font-size: 20px;
  cursor: pointer;
  padding: 0 var(--space-1);
  color: var(--text-muted);
}
.star-btn.active { color: var(--warning); }
.subject { font-size: 18px; font-weight: 600; margin: var(--space-2) 0; color: var(--text-primary); line-height: 1.4; }
.date-row { display: flex; align-items: center; gap: var(--space-3); color: var(--text-secondary); font-size: 12px; }
.attach { color: var(--text-muted); }
.tag-row { display: flex; gap: var(--space-2); margin-top: var(--space-2); flex-wrap: wrap; }
.tag {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: var(--radius-sm);
}
.cat-work { background: rgba(59,130,246,0.15); color: var(--cat-work); }
.cat-bill { background: rgba(245,158,11,0.15); color: var(--cat-bill); }
.cat-personal { background: rgba(236,72,153,0.15); color: var(--cat-personal); }
.cat-notification { background: rgba(107,114,128,0.15); color: var(--cat-notification); }
.cat-marketing { background: rgba(139,92,246,0.15); color: var(--cat-marketing); }
.cat-spam { background: rgba(107,114,128,0.15); color: var(--cat-spam); }
.importance { font-size: 11px; color: var(--warning); }
.importance.low { color: var(--text-muted); }

.ai-card {
  background: var(--bg-subtle);
  border-left: 3px solid var(--brand-primary);
  border-radius: var(--radius-md);
  padding: var(--space-3) var(--space-4);
}
.ai-title { font-size: 13px; font-weight: 600; color: var(--brand-primary); margin-bottom: var(--space-1); }
.ai-body { font-size: 14px; color: var(--text-primary); line-height: 1.5; }
.ai-action { margin-top: var(--space-2); display: flex; gap: var(--space-2); align-items: center; }
.action-label { font-size: 11px; color: var(--text-muted); }
.action-text { font-size: 13px; color: var(--text-primary); }

.snippet-card {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-4);
  box-shadow: var(--shadow-sm);
}
.snippet-label { font-size: 11px; color: var(--text-muted); margin-bottom: var(--space-2); }
.snippet-body { font-size: 14px; color: var(--text-primary); white-space: pre-wrap; line-height: 1.6; word-break: break-word; }
.muted { color: var(--text-muted); }

.actions { display: flex; gap: var(--space-2); }
.action-btn {
  flex: 1;
  padding: var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: 14px;
  cursor: pointer;
}
.action-btn:active { background: var(--bg-subtle); }
.action-btn.done { color: var(--success); border-color: var(--success); }
.hint { font-size: 12px; color: var(--text-muted); text-align: center; padding: 0 var(--space-3); }
.hint code { font-family: ui-monospace, SFMono-Regular, monospace; background: var(--bg-subtle); padding: 1px 4px; border-radius: 4px; }
</style>
