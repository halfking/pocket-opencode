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
        <!--
          发件邮箱始终展示。
          需求要求「显示发件的邮箱」：此前只在 fromName 存在时才显示地址，
          两者相同时就只剩一个笼统的名字，用户无法确认这封信到底来自哪个地址。
          展示名与地址相同时不重复渲染。
        -->
        <span
          v-if="email.fromAddress && email.fromAddress !== email.fromName"
          class="from-addr"
          :title="email.fromAddress"
        >{{ email.fromAddress }}</span>
      </div>
      <div class="subline">
        <time>{{ formatEmailDate(email.date) }}</time>
        <span v-if="email.hasAttachments">附件</span>
        <span v-if="email.category" class="tag" :class="`cat-${email.category}`">{{ emailCatLabel(email.category) }}</span>
        <span v-if="translating" class="lang-hint">翻译中…</span>
        <span v-else-if="lang !== 'original'" class="lang-hint">{{ langShortLabel(lang) }}译文</span>
      </div>
      <h1 class="subject">{{ email.subject || '(无主题)' }}</h1>
      <!-- P2：邮件 → 学习条目 / 工作项（标题由服务端解析，见 learning/sources） -->
      <div class="detail-actions">
        <AddToLearningButton source-kind="email" :source-id="email.id" />
        <AddToLearningButton source-kind="email" :source-id="email.id" as-task task-type="comms" />
        <!--
          总结按钮：只在「还没有摘要」时出现。
          需求明确「在没有总结时，总结后就不需要再总结」，所以已有摘要时
          整个按钮撤掉，而不是置灰——置灰会让人反复点同一个没用的按钮。
        -->
        <button
          v-if="!summary"
          type="button"
          class="summarize-btn"
          :disabled="summarizing"
          @click="runSummarize"
        >{{ summarizing ? '总结中…' : 'AI 总结' }}</button>
      </div>
    </header>
    <div v-if="summary" class="ai">
      <span class="ai-label">邮件总结</span>
      <p class="ai-text">{{ summary }}</p>
    </div>
    <div v-if="email.bodyPurged" class="state slim">正文已清除，仅保留标题和摘要。</div>
    <div v-else-if="bodyLoading && !displayBody" class="state slim">正在加载正文…</div>
    <template v-else>
      <!-- 译文/原文切换：默认展示中文译文，这里给一个显式回退入口 -->
      <div v-if="lang !== 'original'" class="lang-bar">
        <span>当前显示：{{ langShortLabel(lang) }}译文</span>
        <button type="button" class="link-btn" @click="lang = 'original'">显示原文</button>
      </div>
      <div v-if="htmlBody" class="body html" v-html="htmlBody"></div>
      <pre v-else class="body text">{{ displayBody || '(无正文)' }}</pre>
    </template>
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
import AddToLearningButton from '../study/AddToLearningButton.vue'
import { ErrorState } from '../../components'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { findContactByEmail } from '../contact/contacts-store'
import { pickEmailDetailBody, readEmailBodyLocal, writeEmailBodyLocal } from './email-body-cache'
import * as emailsStore from './emails-store'
import type { LocalEmail } from './emails-store'
import EmailComposeSheet from './EmailComposeSheet.vue'
import EmailDetailMenus from './EmailDetailMenus.vue'
import { defaultForwardSubject, defaultReplySubject, todoFromDraft, toggleCompose, type ComposeKind } from './compose-mode'
import { emailCatLabel, extractEmailBody, formatEmailDate, quotedForwardBody } from './email-body-format.ts'
import { sanitizeEmailHtml } from './email-detail-format.ts'
import { preloadRemoteImages } from './email-image-preload.ts'
import {
  DEFAULT_EMAIL_LANG,
  isMostlyChinese,
  langShortLabel,
  resolveDisplayBody,
  translateEmailBody,
  type EmailLang,
} from './translate-email.ts'
import { markListDirty } from '../../composables/list-scene-store'
import { useApiError } from '../../composables/useApiError'
import { useAuthStore } from '../../stores/auth'

const apiError = useApiError()
const route = useRoute()
const router = useRouter()
const toast = useToast()
const auth = useAuthStore()

function currentWorkspaceId(): string {
  return auth.workspaceId || 'default'
}
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
/**
 * 渲染用的 HTML：先过净化+排版归一化（字体兜底、剥远程 webfont），
 * 再把远程图预加载成 data URI。图片没就位前不落到 DOM，
 * 避免「文字先出、图一个个蹦出来还把版面顶下去」。
 */
const htmlBody = ref('')

async function load() {
  loading.value = true
  loadError.value = ''
  bodyText.value = ''
  bodyError.value = ''
  lang.value = 'original'
  langCache.value = {}
  // 摘要：同步流程可能已经给这封邮件写好了 ai_summary，有就直接展示，
  // 也就不会有「再总结一次」的按钮。
  summary.value = ''
  composeKind.value = 'hidden'
  // load 期间屏蔽 watch 触发的重复渲染：正文会被赋值多次、lang 也会变，
  // 若每次都重跑一遍图片预加载，同一封邮件的图会被反复抓取。
  loadingBody = true
  try {
  const found = await emailsStore.getEmail(route.params.id as string)
  email.value = found
  summary.value = (found?.aiSummary || '').trim()
  langCache.value = found ? restoreTranslations(found.id) : {}
    if (found && !found.isRead) {
      try { await emailsStore.markRead(found.id, true); found.isRead = true; markListDirty('email') } catch { /* 不挡正文 */ }
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
        if (!bodyText.value) bodyError.value = apiError(e, 'errors.loadEmailBodyFailed')
      } finally { bodyLoading.value = false }
    }
    // 正文就位后：先按默认语言策略（中文优先）定 lang，再渲染。
    // 两步分开是为了让「原文」立刻可见，不必等翻译往返。
    await applyDefaultLang()
    await renderBody()
  } catch (e: any) {
    loadError.value = apiError(e, 'errors.loadEmailFailed')
  } finally {
    loadingBody = false
    loading.value = false
  }
}

/**
 * 邮件总结（需求：详情页给一个总结按钮，生成后展示，且不再重复总结）。
 *
 * 摘要单独存在 summary 里而不是直接写 email.aiSummary，是为了能在「总结中」
 * 状态下先渲染一个骨架；成功后同时写回 email 对象与本地库，保证列表页
 * 再次进入时不必重新请求。
 */
const summarizing = ref(false)
/** 手动生成的摘要；为空表示尚未总结，此时才显示总结按钮。 */
const summary = ref('')

async function runSummarize() {
  const mail = email.value
  if (!mail || summarizing.value) return
  summarizing.value = true
  try {
    const res = await emailApi.summarizeEmail(mail.id)
    const text = (res.summary || '').trim()
    if (!text) {
      toast.error('未能生成总结，请稍后重试')
      return
    }
    summary.value = text
    // 同步进 email 对象与本地库：跨页面/重进详情时直接命中，不必再请求。
    mail.aiSummary = text
    try { await emailsStore.setAiSummary(mail.id, text) } catch { /* 本地库失败不影响本次展示 */ }
    markListDirty('email')
    toast.success(res.cached ? '已显示已有总结' : '总结已生成')
  } catch (e: any) {
    toast.error(apiError(e, 'errors.operateFailed'))
  } finally {
    summarizing.value = false
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
    persistTranslation(next, out)
    lang.value = next
  } catch (e: any) {
    toast.error(apiError(e, 'errors.operateFailed'))
  } finally {
    translating.value = false
  }
}

/**
 * 渲染当前语言下的正文：净化 + 字体归一化 + 远程图预加载。
 *
 * 单独抽出来是因为它现在是**异步**的（要等图抓完），而 displayBody 是同步
 * computed。preloadSeq 用来丢弃过期结果——快速切语言时先发的请求可能后到，
 * 不做序号校验会让旧语言的 HTML 覆盖新语言。
 */
let preloadSeq = 0
/** load() 期间为 true：此时由 load 自己决定何时渲染，避免重复抓图。 */
let loadingBody = false
async function renderBody() {
  const seq = ++preloadSeq
  const sanitized = sanitizeEmailHtml(displayBody.value)
  if (!sanitized) {
    if (seq === preloadSeq) htmlBody.value = ''
    return
  }
  // 先按净化后的 HTML 渲染（文字立即可见），再异步把图换上，避免白屏等待。
  if (seq === preloadSeq) htmlBody.value = sanitized
  try {
    const withImages = await preloadRemoteImages(sanitized)
    if (seq === preloadSeq) htmlBody.value = withImages
  } catch {
    // 预加载失败就用已净化的版本，不影响可读性。
  }
}

/**
 * 默认语言策略（需求：翻译「默认为中文」）。
 *
 * 正文已是中文 → 直接显示原文，不再花 token 做无意义的「中文译中文」；
 * 否则自动翻成简体中文，并把结果缓存住。两种情况都允许用户手动切回原文。
 */
async function applyDefaultLang() {
  const body = sourceBody.value
  if (!body) return
  if (isMostlyChinese(body)) {
    lang.value = 'original'
    return
  }
  const target = DEFAULT_EMAIL_LANG
  if (langCache.value[target]) {
    lang.value = target
    return
  }
  translating.value = true
  try {
    const out = await translateEmailBody(body, target, async (prompt) => {
      const res = await http<{ content: string }>('/api/llm/chat', {
        method: 'POST',
        body: JSON.stringify({ messages: [{ role: 'user', content: prompt }] }),
      })
      return res.content
    })
    langCache.value = { ...langCache.value, [target]: out }
    persistTranslation(target, out)
    lang.value = target
  } catch {
    // 翻译不可用（没配 LLM / 网络问题）时静默退回原文，正文照常显示。
    lang.value = 'original'
  } finally {
    translating.value = false
  }
}

// 翻译结果持久化(P0/G6):离开详情页即丢组件缓存会浪费已花的 token;
// 以邮件 id 为键落 localStorage,重进(甚至重装前的同库)直接命中。
function translationKey(id: string): string {
  return `email_translations:${id}`
}
function restoreTranslations(id: string): Record<string, string> {
  try {
    const raw = localStorage.getItem(translationKey(id))
    return raw ? (JSON.parse(raw) as Record<string, string>) : {}
  } catch { return {} }
}
function persistTranslation(lang: EmailLang, text: string): void {
  const id = email.value?.id
  if (!id) return
  try {
    const merged = { ...restoreTranslations(id), [lang]: text }
    localStorage.setItem(translationKey(id), JSON.stringify(merged))
  } catch { /* 配额满等场景静默,仅退化为不缓存 */ }
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
    composeError.value = apiError(e, 'errors.sendEmailFailed')
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
    composeError.value = apiError(e, 'errors.createTaskFailed')
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
  markListDirty('email')
}

async function toggleStar() {
  if (!email.value) return
  email.value.isStarred = !email.value.isStarred
  await emailsStore.setStarred(email.value.id, email.value.isStarred)
  markListDirty('email')
}

async function navigateToContact() {
  if (!email.value?.fromAddress) return
  try {
    // 联系人按 workspace_id 分区，查找必须用当前登录 workspace，
    // 否则永远落到 'default' 分区、永远提示「联系人不存在」。
    const contact = await findContactByEmail(email.value.fromAddress, currentWorkspaceId())
    if (contact) router.push(`/contacts/${contact.id}`)
    else toast.info('联系人不存在，请先在联系人页面聚合')
  } catch (e: any) {
    toast.error(apiError(e, 'errors.notFound'))
  }
}

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/email')
}

watch(() => route.params.id, load)
onMounted(load)

// 手动切语言后要重新走一遍净化 + 图片预加载（译文的 HTML 结构可能与原文不同）。
// load() 自身负责首屏渲染，这里只响应「用户主动切换」。
watch([lang, bodyText], () => {
  if (!loadingBody) void renderBody()
})
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
.ai {
  margin: 0; padding: var(--space-2) var(--space-3);
  background: var(--bg-subtle); border-radius: var(--radius-md);
}
.ai-label {
  display: block; font-size: 11px; color: var(--text-muted);
  margin-bottom: 2px; letter-spacing: .02em;
}
.ai-text { margin: 0; font-size: 13px; line-height: 1.6; color: var(--text-secondary); }
.summarize-btn {
  padding: 5px 12px; font-size: 12px; border-radius: 8px; cursor: pointer;
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text-primary);
}
.summarize-btn:disabled { opacity: .6; cursor: progress; }
.body { margin: 0; font-size: 15px; line-height: 1.7; color: var(--text-primary); word-break: break-word; }
/* 正文兜底字体栈：邮件自带的 font-family 可能不含汉字，落到 Roboto 会缺字/变方框。 */
.body.text {
  white-space: pre-wrap;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial,
    'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', 'Source Han Sans SC',
    'Noto Sans CJK SC', 'WenQuanYi Micro Hei', sans-serif;
}
.body.html { overflow-wrap: anywhere; }
.body.html :deep(img) { max-width: 100%; height: auto; }
.body.html :deep(a) { color: var(--brand-primary); }
.body.html :deep(table) { max-width: 100%; border-collapse: collapse; }
/* 邮件里常见的固定宽度表格在窄屏会横向溢出，这里允许横向滚动而不是被裁掉。 */
.body.html :deep(td), .body.html :deep(th) { word-break: break-word; }
.lang-bar {
  display: flex; align-items: center; justify-content: space-between; gap: var(--space-2);
  padding: 6px var(--space-3); margin-bottom: var(--space-2);
  background: var(--bg-subtle); border-radius: var(--radius-sm);
  font-size: 12px; color: var(--text-secondary);
}
.body-error { color: var(--danger); font-size: 13px; }
</style>
