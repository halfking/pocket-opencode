<!--
  EmailInboxView — aggregated inbox across IMAP accounts with AI category
  and importance filters. Includes compose modal (SMTP send) + on-demand sync.
  Account setup lives in EmailAccountSetup.vue; full body in EmailDetailView.vue.
-->
<template>
  <div class="view-root">
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
      <button class="sync-btn compose-btn" @click="openCompose">✏️ 写信</button>
    </div>

    <p v-if="syncMessage" class="sync-message">{{ syncMessage }}</p>

    <Loading v-if="loading" text="加载邮件中…" />
    <ErrorState
      v-else-if="loadError !== ''"
      title="邮件加载失败"
      :message="loadError"
      @retry="load"
    />
    <EmptyState
      v-else-if="emails.length === 0"
      icon="📬"
      title="暂无邮件"
      message="本地缓存还没有邮件"
      hint="点击上方「同步」从邮箱服务器拉取，或先在设置中配置邮箱账户"
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
    </template>

    <!-- Compose modal -->
    <div v-if="composing" class="compose-modal" role="dialog" aria-modal="true">
      <div class="compose-card">
        <header class="compose-head">
          <strong>发送邮件</strong>
          <button class="link-btn" :disabled="composeSending" @click="closeCompose">关闭</button>
        </header>
        <label class="compose-label">
          收件人（多个用逗号或空格分隔）
          <input v-model="composeTo" type="text" placeholder="alice@example.com, bob@example.com" />
        </label>
        <label class="compose-label">
          主题
          <input v-model="composeSubject" type="text" placeholder="邮件主题" />
        </label>
        <label class="compose-label">
          正文
          <textarea v-model="composeBody" rows="6" placeholder="邮件正文"></textarea>
        </label>
        <p v-if="composeError" class="compose-error">{{ composeError }}</p>
        <div class="compose-actions">
          <button class="btn-ghost" :disabled="composeSending" @click="closeCompose">取消</button>
          <button class="btn-primary" :disabled="composeSending" @click="submitCompose">
            {{ composeSending ? '发送中…' : '发送' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { emailApi } from '../../api/email'
import { EmptyState, ErrorState, Loading } from '../../components'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'

const router = useRouter()
const emails = ref<LocalEmail[]>([])
const loading = ref(true)
const loadError = ref('')
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
  loadError.value = ''
  try {
    emails.value = await emailsStore.listEmails(
      activeCategory.value ? { category: activeCategory.value } : {},
    )
  } catch (e: any) {
    if (e?.message?.includes('LocalDB 未初始化')) {
      dbNotReady.value = true
      console.warn('[email] 本地数据库未初始化，显示降级界面')
    } else {
      // 页面内错误 + 重试（08 §6），保留已加载内容
      loadError.value = e?.message || '读取本地邮件缓存失败，请重试'
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

// Compose dialog state (local — no router entry required for this lightweight flow).
const composing = ref(false)
const composeTo = ref('')
const composeSubject = ref('')
const composeBody = ref('')
const composeSending = ref(false)
const composeError = ref('')

function openCompose() {
  composing.value = true
  composeTo.value = ''
  composeSubject.value = ''
  composeBody.value = ''
  composeError.value = ''
}
function closeCompose() {
  if (composeSending.value) return
  composing.value = false
}
function splitRecipients(raw: string): string[] {
  return raw
    .split(/[,;\s]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}
async function submitCompose() {
  if (composeSending.value) return
  const recipients = splitRecipients(composeTo.value)
  if (recipients.length === 0) {
    composeError.value = '请填写收件人'
    return
  }
  if (!composeSubject.value.trim()) {
    composeError.value = '请填写主题'
    return
  }
  composeSending.value = true
  composeError.value = ''
  try {
    const result = await emailApi.sendEmail({
      to: recipients,
      subject: composeSubject.value,
      body: composeBody.value,
    })
    syncMessage.value = `已发送到 ${result.from} → ${result.to.join(', ')}`
    composing.value = false
  } catch (e: any) {
    composeError.value = e?.message || '发送失败，请检查 SMTP 配置'
  } finally {
    composeSending.value = false
  }
}

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
.compose-btn { margin-left: var(--space-2); }

.compose-modal {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-3);
  z-index: 50;
}
.compose-card {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  width: 100%;
  max-width: 520px;
  padding: var(--space-4);
  box-shadow: var(--shadow-lg);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
.compose-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.compose-label {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  font-size: 13px;
  color: var(--text-secondary);
}
.compose-label input,
.compose-label textarea {
  font-size: 14px;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-elevated);
  color: var(--text-primary);
  resize: vertical;
}
.compose-error {
  color: var(--danger);
  font-size: 12px;
  margin: 0;
}
.compose-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}
.btn-primary {
  padding: 8px 16px;
  border: none;
  border-radius: var(--radius-sm);
  background: var(--brand-primary);
  color: white;
  font-size: 14px;
  cursor: pointer;
}
.btn-primary:disabled { opacity: 0.6; cursor: wait; }
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
