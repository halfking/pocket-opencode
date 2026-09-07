<!--
  同步批量清垃圾：先 IMAP 同步，再按主题/来源/日期预览，确认后 MOVE 到 Junk。
-->
<template>
  <div class="cleanup-page">
    <header class="page-head">
      <button class="back-btn" type="button" aria-label="返回" @click="goBack">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h2 class="page-title">清理垃圾邮件</h2>
    </header>

    <p class="hint">先同步真实收件箱，再按条件预览。确认后会移到各邮箱的垃圾箱，并从本机列表删除。</p>

    <div class="actions">
      <button class="ghost" type="button" :disabled="syncing" @click="syncFirst">
        {{ syncing ? '同步中…' : '先同步邮件' }}
      </button>
      <span v-if="syncMsg" class="muted">{{ syncMsg }}</span>
    </div>

    <label class="field">
      <span>主题包含</span>
      <input v-model="subject" class="input" placeholder="例如：中奖、unsubscribe" />
    </label>
    <label class="field">
      <span>来源包含（发件人 / 域名）</span>
      <input v-model="from" class="input" placeholder="例如：promo@ 或 shop.com" />
    </label>
    <div class="dates">
      <label class="field">
        <span>开始日期</span>
        <input v-model="sinceLocal" type="datetime-local" class="input" />
      </label>
      <label class="field">
        <span>结束日期</span>
        <input v-model="untilLocal" type="datetime-local" class="input" />
      </label>
    </div>
    <label class="field">
      <span>账户（可选）</span>
      <select v-model="accountId" class="input">
        <option value="">全部账户</option>
        <option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.displayName }} · {{ a.emailAddress }}</option>
      </select>
    </label>

    <p v-if="formError" class="err">{{ formError }}</p>

    <div class="actions">
      <button class="primary" type="button" :disabled="busy" @click="preview">
        {{ busy ? '处理中…' : '预览匹配' }}
      </button>
      <button
        class="danger"
        type="button"
        :disabled="busy || !previewed || matched === 0"
        @click="confirmDelete"
      >
        确认移到垃圾箱（{{ matched }}）
      </button>
    </div>

    <p v-if="resultMsg" :class="resultOk ? 'ok' : 'err'">{{ resultMsg }}</p>
    <ul v-if="failed.length" class="failed">
      <li v-for="(f, i) in failed" :key="i">{{ f }}</li>
    </ul>

    <div v-if="rows.length" class="preview">
      <p class="muted">预览 {{ rows.length }} / {{ matched }} 封</p>
      <article v-for="e in rows" :key="e.id" class="row">
        <div class="from">{{ e.from }}</div>
        <div class="subj">{{ e.subject || '（无主题）' }}</div>
        <div class="muted">{{ formatEmailRelTime(e.date) }}</div>
      </article>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { emailApi, type EmailAccount } from '../../api/email'
import { previewEmailCleanup, runEmailCleanup, type EmailCleanupItem } from '../../api/email-cleanup'
import { ApiError } from '../../api/http'
import { deleteEmailsByIds } from './emails-store'
import { formatEmailRelTime, hasCleanupConstraint } from './cleanup-filter'
import { isLocalTestAddress } from './providers'

const router = useRouter()
const accounts = ref<EmailAccount[]>([])
const subject = ref('')
const from = ref('')
const sinceLocal = ref('')
const untilLocal = ref('')
const accountId = ref('')
const syncing = ref(false)
const syncMsg = ref('')
const busy = ref(false)
const formError = ref('')
const previewed = ref(false)
// 预览成功时的过滤器快照：删除必须按用户确认过的那组条件执行，
// 不能在确认前改输入框偷换成更大的匹配集。
const previewedFilter = ref<ReturnType<typeof currentFilter> | null>(null)
const matched = ref(0)
const rows = ref<EmailCleanupItem[]>([])
const failed = ref<string[]>([])
const resultMsg = ref('')
const resultOk = ref(false)

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/email')
}

function localToSec(v: string): number {
  if (!v) return 0
  const t = Date.parse(v)
  return Number.isFinite(t) ? Math.floor(t / 1000) : 0
}

function currentFilter() {
  return {
    accountId: accountId.value || undefined,
    subject: subject.value.trim() || undefined,
    from: from.value.trim() || undefined,
    since: localToSec(sinceLocal.value) || undefined,
    until: localToSec(untilLocal.value) || undefined,
  }
}

async function loadAccounts() {
  const res = await emailApi.listAccounts()
  accounts.value = (res.accounts ?? []).filter((a) => !isLocalTestAddress(a.emailAddress))
}

async function syncFirst() {
  syncing.value = true
  syncMsg.value = ''
  try {
    const r = await emailApi.syncNow()
    const fail = r.failed?.length ? `，失败 ${r.failed.length}` : ''
    syncMsg.value = `已同步 ${r.synced ?? 0} 个账户，新邮件 ${r.new ?? 0}${fail}`
  } catch (e) {
    syncMsg.value = e instanceof ApiError ? e.message : (e instanceof Error ? e.message : '同步失败')
  } finally {
    syncing.value = false
  }
}

function resetPreview() {
  previewed.value = false
  previewedFilter.value = null
  matched.value = 0
  rows.value = []
}

// 过滤条件一旦变化，旧预览即作废，必须重新预览才能执行删除。
watch([subject, from, sinceLocal, untilLocal, accountId], resetPreview)

async function preview() {
  formError.value = ''
  resultMsg.value = ''
  resetPreview()
  const f = currentFilter()
  if (!hasCleanupConstraint(f)) {
    formError.value = '请至少填写主题、来源或日期范围，避免误删全部邮件'
    return
  }
  busy.value = true
  try {
    const r = await previewEmailCleanup(f)
    matched.value = r.matched
    rows.value = r.emails ?? []
    previewedFilter.value = f
    previewed.value = true
    resultMsg.value = r.matched === 0 ? '没有匹配的邮件' : `将处理 ${r.matched} 封`
    resultOk.value = true
  } catch (e) {
    formError.value = e instanceof ApiError ? e.message : (e instanceof Error ? e.message : '预览失败')
  } finally {
    busy.value = false
  }
}

async function confirmDelete() {
  const f = previewedFilter.value
  if (!f || !previewed.value) return
  if (!window.confirm(`确认把 ${matched.value} 封邮件移到垃圾箱？此操作会同步到邮箱服务器。`)) return
  busy.value = true
  formError.value = ''
  try {
    const r = await runEmailCleanup(f)
    const ids = r.deletedIds ?? []
    if (ids.length) await deleteEmailsByIds(ids)
    matched.value = r.matched
    failed.value = r.failed ?? []
    resultOk.value = (r.failed?.length ?? 0) === 0
    resultMsg.value = `已移动 ${r.moved}，删除 ${r.deleted}`
    resetPreview()
  } catch (e) {
    resultOk.value = false
    resultMsg.value = e instanceof ApiError ? e.message : (e instanceof Error ? e.message : '清理失败')
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  try { await loadAccounts() } catch { /* 列表失败不挡过滤 */ }
})
</script>

<style scoped>
.cleanup-page {
  flex: 1; min-height: 0; height: 100%;
  overflow-y: auto; -webkit-overflow-scrolling: touch;
  padding: var(--space-3) var(--space-4) var(--space-6);
  box-sizing: border-box;
  display: flex; flex-direction: column; gap: var(--space-3);
}
.page-head {
  position: sticky; top: 0; z-index: 2;
  display: flex; align-items: center; gap: var(--space-2);
  margin: calc(-1 * var(--space-3)) calc(-1 * var(--space-4)) 0;
  padding: var(--space-3) var(--space-4);
  background: var(--bg-base);
}
.back-btn { border: 0; background: transparent; color: var(--text-primary); padding: 4px; }
.page-title { margin: 0; font-size: 18px; }
.hint, .muted { margin: 0; color: var(--text-muted); font-size: 12px; }
.field { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--text-secondary); }
.input { border: 1px solid var(--border); border-radius: var(--radius-sm); padding: var(--space-2); background: var(--bg-base); color: var(--text-primary); }
.dates { display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-2); }
.actions { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; }
.primary, .ghost, .danger { border-radius: var(--radius-md); padding: var(--space-2) var(--space-3); cursor: pointer; }
.primary { border: 0; background: var(--brand-primary); color: var(--text-inverse); font-weight: 600; }
.ghost { border: 1px solid var(--border); background: var(--bg-card); color: inherit; }
.danger { border: 0; background: var(--danger); color: var(--text-inverse); }
.primary:disabled, .danger:disabled, .ghost:disabled { opacity: 0.5; cursor: not-allowed; }
.err { color: var(--danger); margin: 0; }
.ok { color: var(--success); margin: 0; }
.failed { margin: 0; padding-left: 1.2rem; color: var(--danger); font-size: 12px; }
.preview { display: flex; flex-direction: column; gap: var(--space-2); }
.row { background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius-md); padding: var(--space-2); }
.from { font-weight: 600; font-size: 13px; }
.subj { font-size: 14px; }
</style>
