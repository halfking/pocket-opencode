<!-- 邮件详情：主区正文；语言/回复/更多在导航栏；输入框按需弹出。 -->
<template>
  <HeaderActionsPortal>
    <button type="button" :aria-label="`切换语言（当前 ${langShortLabel(lang)}）`" @click="langOpen = true">
      <span class="material-symbols-outlined" aria-hidden="true">translate</span>
    </button>
    <button type="button" aria-label="回复" @click="openCompose('reply')">
      <span class="material-symbols-outlined" aria-hidden="true">reply</span>
    </button>
    <button type="button" aria-label="更多操作" @click="moreOpen = true">
      <span class="material-symbols-outlined" aria-hidden="true">more_vert</span>
    </button>
  </HeaderActionsPortal>

  <div v-if="loading" class="state" role="status">加载中…</div>
  <ErrorState v-else-if="loadError" title="邮件加载失败" :message="loadError" @retry="load" />
  <div v-else-if="!email" class="state">
    <p>未找到该邮件（可能已被删除）。</p>
    <button class="link-btn" @click="goBack">返回邮箱</button>
  </div>

  <article v-else class="detail">
    <header class="meta">
      <div class="from" @click="navigateToContact">
        <span class="from-name">{{ email.fromName || email.fromAddress }}</span>
        <span v-if="email.fromName" class="from-addr">{{ email.fromAddress }}</span>
      </div>
      <div class="subline">
        <time>{{ formatEmailDate(email.date) }}</time>
        <span v-if="email.hasAttachments">附件</span>
        <span v-if="email.category" class="tag" :class="`cat-${email.category}`">{{ emailCatLabel(email.category) }}</span>
        <span v-if="translating" class="lang-hint">翻译中…</span>
        <span v-else-if="lang !== 'original'" class="lang-hint">{{ langShortLabel(lang) }}</span>
      </div>
      <h1 class="subject">{{ email.subject || '(无主题)' }}</h1>
    </header>
    <p v-if="email.aiSummary" class="ai">{{ email.aiSummary }}</p>
    <div v-if="email.bodyPurged" class="state slim">正文已清除，仅保留标题和摘要。</div>
    <div v-else-if="bodyLoading && !displayBody" class="state slim">正在加载正文…</div>
    <div v-else-if="htmlBody" class="body html" v-html="htmlBody"></div>
    <pre v-else class="body text">{{ displayBody || '(无正文)' }}</pre>
    <p v-if="bodyError" class="body-error">{{ bodyError }}</p>
  </article>

  <EmailDetailMenus
    v-model:lang-open="langOpen"
    v-model:more-open="moreOpen"
    :lang="lang"
    :starred="!!email?.isStarred"
    :is-read="!!email?.isRead"
    :converting="converting"
    @choose-lang="chooseLang"
    @forward="openCompose('forward'); moreOpen = false"
    @todo="openCompose('todo'); moreOpen = false"
    @star="toggleStar(); moreOpen = false"
    @read="toggleRead(); moreOpen = false"
  />
  <EmailComposeSheet
    :kind="composeKind"
    :from-name="email?.fromName || email?.fromAddress || ''"
    :seed="composeSeed"
    :submitting="sending"
    :error="composeError"
    @close="composeKind = 'hidden'"
    @submit="onComposeSubmit"
  />
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../../api/client'
import { emailApi } from '../../api/email'
import { http } from '../../api/http'
import { useToast } from '../../composables/useToast'
import { ErrorState } from '../../components'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { findContactByEmail } from '../contact/contacts-store'
import { pickEmailDetailBody, readEmailBodyLocal, writeEmailBodyLocal } from './email-body-cache'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'
import EmailComposeSheet from './EmailComposeSheet.vue'
import EmailDetailMenus from './EmailDetailMenus.vue'
import { defaultForwardSubject, defaultReplySubject, todoFromDraft, toggleCompose, type ComposeKind } from './compose-mode'
import { emailCatLabel, extractEmailBody, formatEmailDate, quotedForwardBody } from './email-body-format'
import { sanitizeEmailHtml } from './email-detail-format'
import {
  langShortLabel,
  resolveDisplayBody,
  translateEmailBody,
  type EmailLang,
} from './translate-email'

const route = useRoute()
const router = useRouter()
const toast = useToast()
const email = ref<LocalEmail | null>(null)
const loading = ref(true)
const loadError = ref('')
const bodyLoading = ref(false)
const bodyText = ref('')
const bodyError = ref('')
const lang = ref<EmailLang>('original')
const langCache = ref<Record<string, string>>({})
const translating = ref(false)
const langOpen = ref(false)
const moreOpen = ref(false)
const composeKind = ref<ComposeKind>('hidden')
const composeSeed = ref('')
const composeError = ref('')
const sending = ref(false)
const converting = ref(false)

const sourceBody = computed(() => bodyText.value || email.value?.snippet || '')
const displayBody = computed(() => resolveDisplayBody(sourceBody.value, langCache.value, lang.value))
const htmlBody = computed(() => sanitizeEmailHtml(displayBody.value))

async function load() {
  loading.value = true
  loadError.value = ''
  bodyText.value = ''
  bodyError.value = ''
  lang.value = 'original'
  langCache.value = {}
  composeKind.value = 'hidden'
  try {
    const found = await emailsStore.getEmail(route.params.id as string)
    email.value = found
    if (found && !found.isRead) {
      try { await emailsStore.markRead(found.id, true); found.isRead = true } catch { /* 不挡正文 */ }
    }
    if (found?.bodyPurged) {
      bodyText.value = ''
    } else if (found) {
      bodyLoading.value = true
      try {
        const cached = await readEmailBodyLocal(found.id)
        if (cached) {
          bodyText.value = cached
          bodyLoading.value = false
        }
        const remoteBody = await emailApi.getEmailBody(found.id)
        if (remoteBody.purged || remoteBody.source === 'purged') {
          bodyText.value = ''
          found.bodyPurged = true
        } else {
          const remote = extractEmailBody(remoteBody.body)
          bodyText.value = pickEmailDetailBody(cached, remote)
          if (remote) await writeEmailBodyLocal(found.id, remote)
        }
      } catch (e: any) {
        if (!bodyText.value) bodyError.value = e?.message || '正文拉取失败'
      } finally { bodyLoading.value = false }
    }
  } catch (e: any) {
    loadError.value = e?.message || '加载邮件失败，请稍后重试。'
  } finally {
    loading.value = false
  }
}

async function chooseLang(next: EmailLang) {
  langOpen.value = false
  if (next === 'original' || langCache.value[next] || !sourceBody.value) {
    lang.value = next
    return
  }
  translating.value = true
  try {
    const out = await translateEmailBody(sourceBody.value, next, async (prompt) => {
      const res = await http<{ content: string }>('/api/llm/chat', {
        method: 'POST',
        body: JSON.stringify({ messages: [{ role: 'user', content: prompt }] }),
      })
      return res.content
    })
    langCache.value = { ...langCache.value, [next]: out }
    lang.value = next
  } catch (e: any) {
    toast.error(e?.message || '翻译失败')
  } finally {
    translating.value = false
  }
}

function openCompose(kind: ComposeKind) {
  const mail = email.value
  composeError.value = ''
  if (kind === 'forward' && mail) {
    composeSeed.value = quotedForwardBody({
      from: mail.fromName ? `${mail.fromName} <${mail.fromAddress}>` : mail.fromAddress,
      date: formatEmailDate(mail.date),
      subject: mail.subject || '(无主题)',
      body: displayBody.value,
    })
  } else if (kind === 'todo' && mail) {
    composeSeed.value = `${mail.subject || '(无主题)'}\n\n${displayBody.value}`.trim()
  } else {
    composeSeed.value = ''
  }
  composeKind.value = toggleCompose(composeKind.value, kind)
}

async function onComposeSubmit(payload: { text: string; to: string[] }) {
  const mail = email.value
  if (!mail || sending.value) return
  const text = payload.text.trim()
  if (!text) return
  if (composeKind.value === 'todo') return createTodo(text)
  if (composeKind.value === 'forward' && payload.to.length === 0) {
    composeError.value = '请填写转发收件人'
    return
  }
  sending.value = true
  composeError.value = ''
  try {
    const reply = composeKind.value === 'reply'
    await emailApi.sendEmail({
      accountId: mail.accountId,
      to: reply ? [mail.fromAddress] : payload.to,
      subject: reply ? defaultReplySubject(mail.subject || '') : defaultForwardSubject(mail.subject || ''),
      body: text,
    })
    toast.success(reply ? '回复已发送' : '转发已发送')
    composeKind.value = 'hidden'
  } catch (e: any) {
    composeError.value = e?.message || '发送失败'
  } finally {
    sending.value = false
  }
}

async function createTodo(text: string) {
  if (!email.value || converting.value) return
  converting.value = true
  sending.value = true
  try {
    const draft = todoFromDraft(text, email.value.subject || '(无主题)')
    const task = await api.createTask({
      title: draft.title,
      description: draft.description,
      source: 'local',
      status: 'active',
      priority: email.value.importance === 'high' ? 'high' : 'medium',
    })
    toast.success(`已转为任务：${task.title}`)
    composeKind.value = 'hidden'
    router.push(`/tasks/${task.id}`)
  } catch (e: any) {
    composeError.value = e?.message || '创建任务失败'
  } finally {
    converting.value = false
    sending.value = false
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

async function navigateToContact() {
  if (!email.value?.fromAddress) return
  try {
    const contact = await findContactByEmail(email.value.fromAddress)
    if (contact) router.push(`/contacts/${contact.id}`)
    else toast.info('联系人不存在，请先在联系人页面聚合')
  } catch (e: any) {
    toast.error(e?.message || '查找联系人失败')
  }
}

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/email')
}

watch(() => route.params.id, load)
onMounted(load)
</script>


<style scoped>
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.state.slim { padding: var(--space-4); }
.link-btn { background: none; border: none; color: var(--brand-primary); cursor: pointer; }
.detail { display: flex; flex-direction: column; gap: var(--space-3); padding-bottom: var(--space-6); }
.meta { padding-bottom: var(--space-2); border-bottom: 1px solid var(--border); }
.from { display: flex; flex-direction: column; min-width: 0; }
.from-name { font-weight: 600; font-size: 15px; color: var(--text-primary); }
.from-addr { font-size: 12px; color: var(--text-muted); word-break: break-all; }
.subline { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; margin-top: 4px; font-size: 12px; color: var(--text-secondary); }
.subject { font-size: 20px; font-weight: 650; margin: var(--space-2) 0 0; line-height: 1.35; color: var(--text-primary); }
.tag { font-size: 11px; padding: 1px 6px; border-radius: var(--radius-sm); }
.cat-work { background: var(--cat-work-bg); color: var(--cat-work); }
.cat-bill { background: var(--cat-bill-bg); color: var(--cat-bill); }
.cat-personal { background: var(--cat-personal-bg); color: var(--cat-personal); }
.cat-notification { background: var(--cat-notification-bg); color: var(--cat-notification); }
.cat-marketing { background: var(--cat-marketing-bg); color: var(--cat-marketing); }
.cat-spam { background: var(--cat-spam-bg); color: var(--cat-spam); }
.lang-hint { color: var(--brand-primary); }
.ai { margin: 0; font-size: 13px; line-height: 1.5; color: var(--text-secondary); padding: var(--space-2) var(--space-3); background: var(--bg-subtle); border-radius: var(--radius-md); }
.body { margin: 0; font-size: 15px; line-height: 1.7; color: var(--text-primary); word-break: break-word; }
.body.text { white-space: pre-wrap; font-family: inherit; }
.body.html :deep(img) { max-width: 100%; height: auto; }
.body.html :deep(a) { color: var(--brand-primary); }
.body-error { color: var(--danger); font-size: 13px; }
</style>
