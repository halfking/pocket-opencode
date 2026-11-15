<template>
  <div class="page">
    <!-- 顶部工具条：刷新 / 自动刷新 / 重派 / 移交 / 终止（预留） -->
    <div class="toolbar">
      <label class="auto"><input v-model="autoRefresh" type="checkbox" /> 自动刷新(5s)</label>
      <div class="toolbar-actions">
        <button type="button" class="tool" :disabled="loading" @click="load">刷新</button>
        <!-- M2 任务13：重派（固定 target_host 的 delegate）+ 移交（可 Auto balance）。
             仅 run 活跃时可操作；409 = 已有 pending envelope（对话框内有显式提示）。 -->
        <button
          v-if="run && canHandoff"
          type="button"
          class="tool"
          @click="openHandoff('reassign')"
        >
          重派
        </button>
        <button
          v-if="run && canHandoff"
          type="button"
          class="tool handoff"
          @click="openHandoff('delegate')"
        >
          移交
        </button>
        <!-- TODO(M1 任务7+): ACC 尚无 SOP run 级终止端点（现有 cancel 面只覆盖
             orchestration sessions / runtime commands / flow executions，见
             accApi.cancelAccSopRun 注释）。按钮按契约族预留，404 时降级提示。 -->
        <button
          v-if="run && (run.status === 'running' || run.status === 'pending')"
          type="button"
          class="tool danger"
          :disabled="cancelling"
          @click="cancelRun"
        >
          {{ cancelling ? '终止中…' : '终止' }}
        </button>
      </div>
    </div>

    <!-- Mock 提示横幅：评审环境无 ACC 时可见 -->
    <div v-if="mockActive" class="mock-banner">Mock 演示模式 — 数据为内置样例，审批会推进演示状态机</div>

    <div v-if="error" class="error" role="alert">
      <p>{{ error }}</p>
      <button type="button" @click="load">重试</button>
    </div>

    <div v-else-if="loading && !run" class="state">加载中…</div>
    <div v-else-if="!run" class="state"><p>未找到该 Run</p></div>

    <template v-else>
      <!-- Run 概要 -->
      <section class="summary">
        <div class="summary-row">
          <span class="badge" :class="`tone-${runVisual(run.status).tone}`">
            <template v-if="runVisual(run.status).glyph">{{ runVisual(run.status).glyph }}</template>
            {{ runVisual(run.status).label }}
          </span>
          <span class="run-id">{{ run.runId }}</span>
        </div>
        <div class="summary-meta">
          <span v-if="run.projectId">项目 {{ run.projectId }}</span>
          <span v-if="run.createdAt">{{ formatTime(run.createdAt) }}</span>
          <span>{{ progress.done }}/{{ progress.total }} 节点完成</span>
          <span v-if="progress.handoff > 0">🔄 {{ progress.handoff }} 个移交中</span>
          <span v-if="run.rawStatus && run.rawStatus !== run.status">原始状态: {{ run.rawStatus }}</span>
        </div>
        <!-- M2 任务13：进度条（已完成节点数 / 总节点数） -->
        <div class="progress" role="progressbar" :aria-valuenow="progressPct" aria-valuemin="0" aria-valuemax="100" :aria-label="`节点进度 ${progressPct}%`">
          <div class="progress-fill" :style="{ width: `${progressPct}%` }" />
        </div>
      </section>

      <!-- 节点树：颜色编码 绿=完成 黄=进行中 红=失败 灰=待执行 ⏸=等待审批 -->
      <section class="nodes">
        <h2 class="section-title">节点（{{ run.nodes.length }}）</h2>
        <ol class="node-list">
          <li v-for="(node, idx) in run.nodes" :key="node.nodeId" class="node" :class="{ gate: node.status === 'waiting_approval' }">
            <div class="node-main">
              <span class="node-mark" :class="`tone-${nodeVisual(node.status).tone}`" aria-hidden="true">
                {{ nodeVisual(node.status).glyph ?? '' }}
                <i v-if="!nodeVisual(node.status).glyph" class="dot" />
              </span>
              <div class="node-info">
                <div class="node-title-row">
                  <span class="node-name">{{ node.name }}</span>
                  <span class="node-id">{{ node.nodeId }}</span>
                </div>
                <div class="node-meta">
                  <span class="badge sm" :class="`tone-${nodeVisual(node.status).tone}`">{{ nodeVisual(node.status).label }}</span>
                  <span v-if="node.type" class="chip">{{ node.type }}</span>
                  <span v-if="node.host" class="chip" :class="{ 'chip-host': node.status === 'handoff_pending' }">
                    主机 {{ node.host }}
                  </span>
                  <span v-if="node.handoffEnvelopeId" class="chip env">envelope {{ node.handoffEnvelopeId }}</span>
                  <span v-if="node.allowFailure" class="chip warn">允许失败</span>
                  <span v-if="node.dependencies.length" class="chip dep">依赖: {{ node.dependencies.join(', ') }}</span>
                  <span v-if="idx === 0 && node.dependencies.length === 0" class="chip">起始节点</span>
                </div>
              </div>
              <button
                v-if="node.status === 'waiting_approval'"
                type="button"
                class="approve-btn"
                @click="openApproval(node)"
              >
                审批
              </button>
            </div>
          </li>
        </ol>
      </section>

      <!-- 审批结果提示 -->
      <p v-if="approvalNote" class="approval-note" role="status">{{ approvalNote }}</p>

      <!-- M2 任务13：编排事件日志流（try-SSE → 轮询降级；mock 模式走内置事件 feed） -->
      <LogStream :run-id="run.runId" />
    </template>

    <ApprovalModal
      v-if="approvalNode && run"
      :run-id="run.runId"
      :node="approvalNode"
      :rollback-candidates="rollbackCandidates"
      @close="approvalClosed"
      @submitted="approvalSubmitted"
    />

    <!-- M2 任务13：重派 / 移交对话框（主机选择器 + Auto balance + 原因） -->
    <HandoffDialog
      v-if="handoffMode && run"
      :run-id="run.runId"
      :mode="handoffMode"
      @close="handoffClosed"
      @submitted="handoffSubmitted"
    />
  </div>
</template>

<script setup lang="ts">
/**
 * SOP Run 详情（M1 任务9 · 操作2/3）。
 *
 * - 节点状态颜色编码（绿/黄/红/灰/⏸）映射见 ./statusMeta.ts；
 * - 等待审批的节点直接给出「审批」入口 → ApprovalModal；
 * - LogStream 订阅进程持有的 ACC SSE/降级轮询；此处 5s 轮询只刷新 run/节点详情，
 *   同一 view context 复用在途读取，切页只停止详情轮询与本页结果接纳。
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useApiError } from '../../composables/useApiError'
import { useRoute } from 'vue-router'
import {
  AccApiError,
  cancelAccSopRun,
  getAccSopRun,
  isAccUnreachable,
  type AccHandoffResult,
  type AccSopNode,
  type AccSopRun,
} from '../../api/accApi'
import {
  formatTime,
  isRunActive,
  nodeVisual,
  progressPercent,
  runVisual,
  summarizeProgress,
} from './statusMeta'
import { adoptMockQuery, isMockActive, mockCancelRun, mockGetRun } from './mock'
import ApprovalModal from './ApprovalModal.vue'
import HandoffDialog from './HandoffDialog.vue'
import LogStream from './LogStream.vue'
import { getAccLogSession } from './acc-log-session.ts'

defineOptions({ name: 'AccTaskDetailView' })

const route = useRoute()
const runId = computed(() => String(route.params.runId ?? ''))

const run = ref<AccSopRun | null>(null)
const loading = ref(false)
const error = ref('')
const apiError = useApiError()
const cancelling = ref(false)
const autoRefresh = ref(true)
const approvalNode = ref<AccSopNode | null>(null)
const approvalNote = ref('')
/** M2 任务13：当前打开的移交对话框（null = 关闭）。 */
const handoffMode = ref<'reassign' | 'delegate' | null>(null)
type HandoffSubmission = AccHandoffResult & { mode: 'reassign' | 'delegate' }
const approvalSubmitted = shallowRef<(decision: string) => Promise<void>>()
const approvalClosed = shallowRef<() => void>()
const handoffSubmitted = shallowRef<(result: HandoffSubmission) => Promise<void>>()
const handoffClosed = shallowRef<() => void>()

const mockActive = isMockActive()
const logSession = getAccLogSession()
let disposed = false
let viewVersion = 0
let readVersion = 0
let cancelVersion = 0
interface ViewTicket { owner: object; runId: string; version: number }
let loadFlight: { ticket: ViewTicket; promise: Promise<void> } | null = null

function captureView(): ViewTicket | null {
  const owner = logSession.runtime.captureAuthority()
  if (disposed || !runId.value || !logSession.runtime.isCurrentAuthority(owner)) return null
  return { owner: owner!, runId: runId.value, version: viewVersion }
}

function currentView(ticket: ViewTicket): boolean {
  return !disposed && ticket.version === viewVersion && ticket.runId === runId.value &&
    logSession.runtime.isCurrentAuthority(ticket.owner)
}

function denied(e: unknown): boolean {
  return e instanceof AccApiError && (e.status === 401 || e.status === 403)
}

// 深链直达详情页也带 ?mock=1 时保持 mock（与看板共用会话级标记）。
adoptMockQuery()

const progress = computed(() => summarizeProgress(run.value?.nodes ?? []))
const progressPct = computed(() => progressPercent(run.value?.nodes ?? []))

/** 重派/移交只在 run 活跃时有意义（完成/失败/取消后 ACC 会 400）。 */
const canHandoff = computed(() =>
  ['running', 'pending', 'waiting_approval', 'handoff_pending'].includes(run.value?.status ?? 'unknown'),
)

/** M2 任务13：重派/移交成功回调 —— 展示 envelope 信息并刷新节点状态。 */
async function onHandoffSubmitted(result: HandoffSubmission, ticket: ViewTicket): Promise<void> {
  if (!currentView(ticket)) return
  const kind = result.mode === 'reassign' ? '重派' : '移交'
  const target = result.targetHost ?? 'Auto balance'
  approvalNote.value = `${kind}请求已提交：envelope_id=${result.envelopeId} · status=${result.status} · 目标=${target}，正在刷新节点状态…`
  handoffMode.value = null
  await load()
}

/** 回退候选：等待审批节点之前的所有节点（审批弹窗的选择器数据源）。 */
const rollbackCandidates = computed<AccSopNode[]>(() => {
  const nodes = run.value?.nodes ?? []
  const idx = nodes.findIndex((n) => n.nodeId === approvalNode.value?.nodeId)
  return idx > 0 ? nodes.slice(0, idx) : nodes
})

function load(): Promise<void> {
  const ticket = captureView()
  if (!ticket) return Promise.resolve()
  // Coalesce ticks for this context. A skipped tick must not supersede a slow read.
  if (loadFlight && loadFlight.ticket.version === ticket.version &&
    loadFlight.ticket.owner === ticket.owner && loadFlight.ticket.runId === ticket.runId) return loadFlight.promise
  const version = ++readVersion
  const current = () => currentView(ticket) && version === readVersion
  const flight = { ticket, promise: Promise.resolve() }
  loadFlight = flight
  loading.value = true
  error.value = ''
  flight.promise = (async () => {
    try {
      const result = mockActive ? await mockGetRun(ticket.runId) : await getAccSopRun(ticket.runId, () => {
        if (!current()) throw Object.assign(new Error('Run detail context changed'), { name: 'AbortError' })
      })
      if (current()) run.value = result
    } catch (e) {
      if (!current()) return
      if (denied(e)) { logSession.runtime.setAuthority(null); return }
      console.warn('[acc-board] run detail failed', e)
      error.value = isAccUnreachable(e)
        ? '无法连接 ACC，请检查「更多 → ACC 看板」设置里的地址与 token。'
        : apiError(e, '任务详情加载失败，请稍后重试')
    } finally {
      if (current()) loading.value = false
      if (loadFlight === flight) loadFlight = null
    }
  })()
  return flight.promise
}

async function cancelRun(): Promise<void> {
  const ticket = captureView()
  if (!ticket || !run.value || run.value.runId !== ticket.runId || cancelling.value) return
  const version = ++cancelVersion
  const current = () => currentView(ticket) && version === cancelVersion
  cancelling.value = true
  approvalNote.value = ''
  try {
    if (mockActive) await mockCancelRun(ticket.runId)
    else await cancelAccSopRun(ticket.runId)
    if (!current()) return
    logSession.runtime.cancel(ticket.runId, ticket.owner)
    // An earlier read may describe the pre-cancel state. Fence its result, but
    // wait for its flight to settle before starting the post-cancel refresh.
    readVersion++
    approvalNote.value = '已请求终止'
    await loadFlight?.promise
    if (!current()) return
    await load()
  } catch (e) {
    if (!current()) return
    if (denied(e)) { logSession.runtime.setAuthority(null); return }
    // 预期路径：ACC 未实现 SOP run 终止端点 → 明确告知而不是静默失败。
    console.warn('[acc-board] run cancellation failed', e)
    approvalNote.value = apiError(e, '终止请求未成功：ACC 当前未提供 SOP run 级终止端点')
  } finally {
    if (current()) cancelling.value = false
  }
}

function openApproval(node: AccSopNode): void {
  const ticket = captureView()
  if (!ticket) return
  // Each child receives a closure over the ticket at opening, never a lookup of
  // the latest modal/owner when a late asynchronous submitted/close event arrives.
  approvalSubmitted.value = decision => onApproved(decision, ticket)
  approvalClosed.value = () => { if (currentView(ticket)) approvalNode.value = null }
  approvalNote.value = ''
  approvalNode.value = node
}

function openHandoff(mode: 'reassign' | 'delegate'): void {
  const ticket = captureView()
  if (!ticket) return
  handoffSubmitted.value = result => onHandoffSubmitted(result, ticket)
  handoffClosed.value = () => { if (currentView(ticket)) handoffMode.value = null }
  handoffMode.value = mode
}

async function onApproved(decision: string, ticket: ViewTicket): Promise<void> {
  if (!currentView(ticket)) return
  const label = decision === 'approve' ? '已通过' : decision === 'reject' ? '已驳回' : '已退回'
  approvalNote.value = `审批${label}，正在刷新状态…`
  await load()
}

// 5s 详情轮询（仅 run 活跃 + 开关打开 + 无审批/取消操作时）。日志流独立归 runtime。
let timer: ReturnType<typeof setInterval> | null = null
function stopTimer(): void {
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
}
function syncTimer(): void {
  const active = autoRefresh.value && !cancelling.value && !approvalNode.value && isRunActive(run.value?.status ?? 'unknown') && !mockActive
  if (active && timer === null) timer = setInterval(load, 5000)
  if (!active && timer !== null) stopTimer()
}

watch([autoRefresh, cancelling, () => run.value?.status, approvalNode], syncTimer, { immediate: true })
watch([runId, logSession.authority], () => {
  viewVersion++; readVersion++; cancelVersion++
  loadFlight = null
  run.value = null
  error.value = approvalNote.value = ''
  loading.value = cancelling.value = false
  approvalNode.value = null
  handoffMode.value = null
  approvalSubmitted.value = handoffSubmitted.value = undefined
  approvalClosed.value = handoffClosed.value = undefined
  stopTimer()
  const ticket = captureView()
  // A synchronous address/credential or A→B→A update admits only the final owner.
  if (ticket) queueMicrotask(() => { if (currentView(ticket)) void load() })
}, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => {
  disposed = true
  viewVersion++; readVersion++; cancelVersion++
  loadFlight = null
  stopTimer()
})
</script>

<style scoped>
.page { height: 100%; min-height: 0; overflow-y: auto; -webkit-overflow-scrolling: touch; background: var(--bg-base); }
.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: var(--space-3, 12px);
  border-bottom: 1px solid var(--border, #e5e7eb);
}
.auto { color: var(--text-secondary, #555); font-size: var(--text-smd, 13px); display: flex; gap: 6px; align-items: center; }
.toolbar-actions { display: flex; gap: 8px; }
.tool {
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: var(--bg-card, #fff);
  color: var(--text-primary, #111);
  padding: 7px 12px;
  font-size: var(--text-smd, 13px);
  cursor: pointer;
}
.tool:disabled { opacity: 0.5; }
.tool.danger { color: #dc2626; border-color: #fca5a5; }
/* M2 任务13：移交入口（紫色 = 移交语义色，与 🔄 状态色一致） */
.tool.handoff { color: #8b5cf6; border-color: #c4b5fd; }

/* M2 任务13：节点进度条 */
.progress {
  height: 6px;
  border-radius: 999px;
  background: var(--bg-base, #f1f5f9);
  overflow: hidden;
}
.progress-fill {
  height: 100%;
  border-radius: 999px;
  background: #16a34a;
  transition: width 0.3s ease;
  min-width: 0;
}

.mock-banner {
  margin: var(--space-3, 12px);
  padding: 8px 12px;
  border-radius: var(--radius-sm, 8px);
  background: #fef3c7;
  color: #92400e;
  font-size: var(--text-smd, 13px);
}

.error {
  margin: var(--space-3, 12px);
  padding: 10px 12px;
  border: 1px solid #fca5a5;
  border-radius: var(--radius-sm, 8px);
  background: #fef2f2;
  color: #dc2626;
  font-size: var(--text-smd, 13px);
  display: flex;
  gap: 10px;
  align-items: center;
  justify-content: space-between;
}
.error p { margin: 0; word-break: break-all; }
.error button {
  border: 1px solid #fca5a5;
  border-radius: var(--radius-sm, 8px);
  background: #fff;
  color: #dc2626;
  padding: 6px 12px;
  cursor: pointer;
  white-space: nowrap;
}
.state { padding: 32px 16px; text-align: center; color: var(--text-tertiary, #888); font-size: var(--text-smd, 13px); }

.summary {
  margin: var(--space-3, 12px);
  padding: 12px;
  background: var(--bg-card, #fff);
  border: 1px solid var(--border, #e5e7eb);
  border-radius: var(--radius-md, 12px);
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.summary-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.run-id { font-size: var(--text-smd, 13px); color: var(--text-primary, #111); font-weight: 600; word-break: break-all; }
.summary-meta { display: flex; gap: 10px; flex-wrap: wrap; font-size: var(--text-smd, 13px); color: var(--text-tertiary, #888); }

.section-title { margin: 0 0 8px; font-size: var(--text-smd, 13px); color: var(--text-secondary, #555); font-weight: 600; }
.nodes { margin: 0 var(--space-3, 12px) var(--space-3, 12px); }
.node-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
.node {
  background: var(--bg-card, #fff);
  border: 1px solid var(--border, #e5e7eb);
  border-radius: var(--radius-md, 12px);
  padding: 10px 12px;
}
.node.gate { border-color: #f59e0b; background: #fffbeb; }
.node-main { display: flex; align-items: flex-start; gap: 10px; }
.node-mark {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  font-size: var(--text-base);
  line-height: 1;
}
.dot { width: 10px; height: 10px; border-radius: 50%; display: block; }
.node-mark.tone-done { background: #dcfce7; color: #16a34a; }
.node-mark.tone-done .dot { background: #16a34a; }
.node-mark.tone-run { background: #fef9c3; color: #ca8a04; }
.node-mark.tone-run .dot { background: #eab308; }
.node-mark.tone-fail { background: #fee2e2; color: #dc2626; }
.node-mark.tone-fail .dot { background: #dc2626; }
.node-mark.tone-pending { background: #f1f5f9; color: #94a3b8; }
.node-mark.tone-pending .dot { background: #94a3b8; }
.node-mark.tone-gate { background: #fef3c7; color: #b45309; }
.node-mark.tone-handoff { background: #ede9fe; color: #8b5cf6; }
.node-mark.tone-cancel { background: #f1f5f9; color: #64748b; }
.node-mark.tone-cancel .dot { background: #64748b; }
.node-mark.tone-skip { background: #f1f5f9; color: #64748b; }
.node-mark.tone-skip .dot { background: #64748b; }
.node-mark.tone-unknown { background: #f1f5f9; color: #94a3b8; }
.node-mark.tone-unknown .dot { background: #94a3b8; }

.node-info { flex: 1; min-width: 0; }
.node-title-row { display: flex; align-items: baseline; gap: 8px; flex-wrap: wrap; }
.node-name { font-size: var(--text-md, 15px); color: var(--text-primary, #111); font-weight: 600; }
.node-id { font-size: var(--text-smd, 13px); color: var(--text-tertiary, #888); word-break: break-all; }
.node-meta { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; margin-top: 6px; }

.badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 10px;
  border-radius: 999px;
  font-size: var(--text-smd, 13px);
  font-weight: 600;
  color: #fff;
}
.badge.sm { padding: 1px 8px; font-weight: 500; font-size: var(--text-sm); }
.badge.tone-done { background: #16a34a; }
.badge.tone-run { background: #eab308; }
.badge.tone-fail { background: #dc2626; }
.badge.tone-pending { background: #94a3b8; }
.badge.tone-gate { background: #f59e0b; }
.badge.tone-handoff { background: #8b5cf6; }
.badge.tone-cancel { background: #64748b; }
.badge.tone-skip { background: #64748b; }
.badge.tone-unknown { background: #94a3b8; }

.chip {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 999px;
  border: 1px solid var(--border, #e5e7eb);
  color: var(--text-secondary, #555);
  font-size: var(--text-sm);
}
.chip.warn { color: #d97706; border-color: #fcd34d; }
.chip.dep { color: var(--text-tertiary, #888); }
/* M2 任务13：移交中节点的主机 chip 高亮 + envelope chip */
.chip-host { color: #8b5cf6; border-color: #c4b5fd; font-weight: 600; }
.chip.env { color: #8b5cf6; border-color: #ddd6fe; word-break: break-all; }

.approve-btn {
  border: none;
  border-radius: var(--radius-sm, 8px);
  background: #f59e0b;
  color: #fff;
  padding: 8px 14px;
  font-size: var(--text-smd, 13px);
  font-weight: 600;
  cursor: pointer;
  flex-shrink: 0;
}
.approval-note {
  margin: 0 var(--space-3, 12px) var(--space-3, 12px);
  padding: 8px 12px;
  border-radius: var(--radius-sm, 8px);
  background: var(--bg-card, #fff);
  border: 1px solid var(--border, #e5e7eb);
  color: var(--text-secondary, #555);
  font-size: var(--text-smd, 13px);
  word-break: break-all;
}
</style>
