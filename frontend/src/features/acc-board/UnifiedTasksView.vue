<template>
  <div class="page">
    <div class="toolbar">
      <button type="button" :disabled="loadingList || loadingDetail" @click="reload">刷新</button>
    </div>
    <p v-if="mockActive" role="status">关闭演示模式后可读取统一任务。</p>
    <p v-else-if="!hasConnection" role="status">请先登录并在 ACC 看板配置当前账号的凭据。</p>
    <form v-else class="run-search" @submit.prevent="openRun">
      <label for="acc-run-id">Run ID</label>
      <input id="acc-run-id" v-model.trim="draftRunId" autocomplete="off" />
      <button type="submit" :disabled="!draftRunId || loadingDetail">查看 Run</button>
    </form>
    <p v-if="loadingList" role="status">正在读取任务…</p>
    <p v-if="listError" role="alert">{{ listError }}</p>
    <p v-if="loaded && !rows.length">没有统一任务</p>
    <ul v-if="rows.length" class="tasks">
      <li v-for="row in rows" :key="row.task_id">
        <button type="button" :aria-pressed="selected?.task_id === row.task_id" @click="select(row)">
          <strong>{{ row.title || row.task_id }}</strong> <span>{{ row.status }}</span>
        </button>
      </li>
    </ul>
    <button v-if="loaded && nextOffset !== null" type="button" :disabled="loadingList" @click="loadPage">更多任务</button>

    <p v-if="loadingDetail" role="status">正在读取任务进度…</p>
    <p v-if="detailError" role="alert">{{ detailError }}</p>
    <section v-if="detail && detailAccess" aria-label="统一任务进度" class="detail">
      <h2>{{ detail.title }}</h2>
      <p v-if="'description' in detail && detail.description">{{ detail.description }}</p>
      <p v-if="detail.title !== detail.runId">Run {{ detail.runId }}</p>
      <template v-if="'progress' in detail">
        <p>{{ detail.status }} · {{ detail.progress.completed }}/{{ detail.progress.total }} 个节点完成</p>
        <p v-if="detail.progress.review">{{ detail.progress.review }} 个节点待评审</p>
        <progress :value="detail.progress.percentage" max="100" aria-label="节点完成比例" />
      </template>
      <p v-else>{{ detail.status }}</p>
      <div aria-label="费用对账" class="billing">
        <div class="billing-header">
          <h3>费用对账</h3>
          <button type="button" :disabled="loadingBilling" @click="loadBilling">刷新费用</button>
        </div>
        <p role="status">{{ billing ? billingLabel[billing.state] : loadingBilling ? '正在读取账本…' : '账本暂不可读取' }}</p>
        <p v-if="billing?.totals">{{ formatTotals(billing.totals) }}</p>
        <details v-if="billing?.attempts.length">
          <summary>按任务尝试查看</summary>
          <ul>
            <li v-for="row in billing.attempts" :key="JSON.stringify([row.task_id, row.revision_id, row.attempt, row.dispatch_id])">
              {{ row.task_id }} #{{ row.attempt }} · {{ billingLabel[row.state] }}
              <span v-if="row.totals"> · {{ formatTotals(row.totals) }}</span>
            </li>
          </ul>
        </details>
      </div>
      <h3>任务图</h3>
      <p v-if="!detail.nodes.length">暂无任务节点</p>
      <ul v-else>
        <li v-for="node in detail.nodes" :key="node.task_id">
          {{ node.title || node.task_id }} · {{ node.status }}<span v-if="node.parent_task_id"> · 上级 {{ node.parent_task_id }}</span>
        </li>
      </ul>
      <h3 v-if="detail.blockers.length">阻塞依赖</h3>
      <ul v-if="detail.blockers.length">
        <li v-for="edge in detail.blockers" :key="JSON.stringify([edge.from, edge.to])">{{ edge.from }} → {{ edge.to }}</li>
      </ul>
      <LogStream :run-id="detail.runId" :authority="detailAccess" @invalidated="expireAuthority" />
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { listUnifiedTasks, readUnifiedRun, readUnifiedTask, readRunBilling, type BillingTotals, type RunBilling, type UnifiedTask } from '../../api/accUnifiedTasks'
import { AccApiError, type AccRequestAuthority } from '../../api/accApi'
import { WorkspaceBindingRequestError } from '../../api/workspaceBinding'
import { useAuthStore } from '../../stores/auth'
import { getAccLogSession } from './acc-log-session'
import { verifyAccTaskAuthority } from './acc-task-authority'
import LogStream from './LogStream.vue'
import { isMockActive } from './mock'

const auth = useAuthStore()
const session = getAccLogSession(auth)
const mockActive = isMockActive()
const hasConnection = computed(() => {
  // The reactive reference changes synchronously with the session and ACC config.
  const owner = session.authority.value && session.runtime.captureAuthority()
  return Boolean(owner?.token && auth.activationStatus === 'ready')
})
const rows = ref<UnifiedTask[]>([])
const selected = ref<UnifiedTask | null>(null)
type Detail = Awaited<ReturnType<typeof readUnifiedTask | typeof readUnifiedRun>>
const detail = ref<Detail | null>(null)
const detailAccess = shallowRef<Awaited<ReturnType<typeof verifyAccTaskAuthority>> | null>(null)
const billing = ref<RunBilling | null>(null)
const loadingBilling = ref(false)
let billingRequest: AbortController | null = null
const billingLabel: Record<string, string> = {
  pending: '等待对账', reconciled: '已对账', partial: '部分已对账', unobserved: '暂无可核验的账目', unverified: '账目待核验',
}
function formatTotals(totals: BillingTotals) {
  return `${totals.requests} 笔请求 · 净扣 ${totals.net_credits.toLocaleString('zh-CN')} credits`
}
const draftRunId = ref('')
const nextOffset = ref<number | null>(0)
const loaded = ref(false)
const loadingList = ref(false), loadingDetail = ref(false)
const listError = ref(''), detailError = ref('')
let listRequest: AbortController | null = null, detailRequest: AbortController | null = null
let expiresAt = Infinity
let expiryTimer: ReturnType<typeof setTimeout> | undefined
let disposed = false

function clear(resetDraft = true) {
  listRequest?.abort()
  detailRequest?.abort()
  listRequest = detailRequest = null
  clearTimeout(expiryTimer)
  expiresAt = Infinity
  rows.value = []
  selected.value = detail.value = null
  detailAccess.value = null
  clearBilling()
  if (resetDraft) draftRunId.value = ''
  nextOffset.value = 0
  loaded.value = loadingList.value = loadingDetail.value = false
  listError.value = detailError.value = ''
}
function clearBilling() {
  billingRequest?.abort()
  billingRequest = null
  loadingBilling.value = false
  billing.value = null
}
async function loadBilling() {
  const access = detailAccess.value, runId = detail.value?.runId
  if (!access || !runId || billingRequest) return
  const request = billingRequest = new AbortController()
  loadingBilling.value = true
  try {
    const result = await readRunBilling(access, runId, request.signal)
    access.assertCurrent()
    if (billingRequest === request) billing.value = result
  } catch {
    if (billingRequest === request) billing.value = null
  } finally {
    if (billingRequest === request) {
      billingRequest = null
      loadingBilling.value = false
    }
  }
}
function expireAuthority(error?: unknown) {
  clear(false)
  listError.value = showError(error, '工作区或 ACC 授权已失效，请刷新后重新核验。')
}
function expireAt(deadline: number) {
  // Existing pages can include rows admitted under an earlier binding.
  if (deadline >= expiresAt) return
  expiresAt = deadline
  clearTimeout(expiryTimer)
  expiryTimer = setTimeout(expireAuthority, Math.max(0, deadline - Date.now()))
}
function isDenied(error: unknown) {
  return (error instanceof AccApiError || error instanceof WorkspaceBindingRequestError)
    && (error.status === 401 || error.status === 403)
}
function showError(error: unknown, fallback: string) {
  return error instanceof Error && error.message === 'ACC 凭据与当前账号或租户不一致' ? error.message : fallback
}
async function loadPage() {
  const owner = session.runtime.captureAuthority(), offset = nextOffset.value
  if (mockActive || !hasConnection.value || !owner || listRequest || offset === null) return
  const request = listRequest = new AbortController()
  loadingList.value = true
  listError.value = ''
  try {
    const access = await verifyAccTaskAuthority(owner, auth, request.signal)
    const page = await listUnifiedTasks(access, offset, request.signal)
    if (listRequest !== request) return
    access.assertCurrent()
    expireAt(access.expiresAt)
    const byId = new Map(rows.value.map(row => [row.task_id, row]))
    for (const row of page.rows) byId.set(row.task_id, row)
    rows.value = [...byId.values()]
    nextOffset.value = page.nextOffset
    loaded.value = true
  } catch (error) {
    if (listRequest === request && !(error instanceof Error && error.name === 'AbortError')) {
      if (isDenied(error)) expireAuthority(error)
      else listError.value = showError(error, '统一任务列表读取失败，请刷新后重试。')
    }
  } finally {
    if (listRequest === request) {
      listRequest = null
      loadingList.value = false
    }
  }
}
async function loadDetail(
  selection: UnifiedTask | null, query: (authority: AccRequestAuthority, signal: AbortSignal) => Promise<Detail>,
) {
  const owner = session.runtime.captureAuthority()
  if (mockActive || !hasConnection.value || !owner) return
  detailRequest?.abort()
  const request = detailRequest = new AbortController()
  selected.value = selection
  detail.value = null
  detailAccess.value = null
  clearBilling()
  detailError.value = ''
  loadingDetail.value = true
  try {
    const access = await verifyAccTaskAuthority(owner, auth, request.signal)
    const result = await query(access, request.signal)
    if (detailRequest !== request) return
    access.assertCurrent()
    expireAt(access.expiresAt)
    detailAccess.value = access
    detail.value = result
    void loadBilling()
  } catch (error) {
    if (detailRequest === request && !(error instanceof Error && error.name === 'AbortError')) {
      if (isDenied(error)) expireAuthority(error)
      else detailError.value = showError(error, '任务详情读取失败，请重试。')
    }
  } finally {
    if (detailRequest === request) {
      detailRequest = null
      loadingDetail.value = false
    }
  }
}
function select(row: UnifiedTask) {
  if (rows.value.includes(row)) void loadDetail(row, (authority, signal) => readUnifiedTask(authority, row, signal))
}
function openRun() {
  const runId = draftRunId.value
  if (runId) void loadDetail(null, (authority, signal) => readUnifiedRun(authority, runId, signal))
}
function reload() {
  const selection = selected.value
  const runId = detail.value?.runId
  clear(false)
  void loadPage()
  if (selection) {
    void loadDetail(selection, (authority, signal) => readUnifiedTask(authority, selection, signal))
  } else if (runId) {
    void loadDetail(null, (authority, signal) => readUnifiedRun(authority, runId, signal))
  }
}
watch(() => [session.authority.value, auth.activationStatus], () => {
  clear()
  const owner = session.runtime.captureAuthority()
  queueMicrotask(() => { if (!disposed && owner === session.runtime.captureAuthority()) void loadPage() })
}, { flush: 'sync', immediate: true })
onBeforeUnmount(() => { disposed = true; clear() })
</script>

<style scoped>
.page { height: 100%; overflow: auto; padding: 1rem; }
.toolbar { display: flex; justify-content: flex-end; }
.run-search { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem; }
.tasks { list-style: none; padding: 0; }
.tasks button { display: flex; justify-content: space-between; gap: 0.75rem; padding: 0.6rem; width: 100%; text-align: start; }
.tasks button[aria-pressed="true"] { font-weight: bold; text-decoration: underline; }
.detail { overflow-wrap: anywhere; }
.billing { border: 1px solid var(--border-color, #aab0ba); border-radius: .75rem; padding: .75rem; }
.billing-header { display: flex; align-items: center; justify-content: space-between; gap: .5rem; }
</style>
