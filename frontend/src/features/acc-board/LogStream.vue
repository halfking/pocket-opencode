<template>
  <section class="ls" aria-label="编排事件日志流">
    <header class="ls-head">
      <h2 class="ls-title">事件日志</h2>
      <span class="ls-mode" :class="`mode-${mode}`">{{ modeLabel }}</span>
      <span v-if="lines.length" class="ls-count">{{ lines.length }} 条</span>
      <button
        type="button"
        class="ls-pause"
        :disabled="lines.length === 0"
        @click="togglePause"
      >
        {{ paused ? '▶ 自动滚动' : '⏸ 暂停滚动' }}
      </button>
    </header>

    <div ref="bodyEl" class="ls-body" @scroll="onScroll">
      <p v-if="lines.length === 0" class="ls-empty">{{ emptyText }}</p>
      <template v-else>
        <div v-for="l in lines" :key="l.key" class="ls-line" :class="`level-${l.level}`">
          <span class="ls-time">{{ l.time }}</span>
          <span class="ls-sev" aria-hidden="true">{{ l.glyph }}</span>
          <span class="ls-type">{{ l.eventType }}</span>
          <span class="ls-text">
            {{ l.text }}<template v-if="l.transition">（{{ l.transition }}）</template>
          </span>
        </div>
      </template>
    </div>

    <p v-if="notice" class="ls-notice" role="status">{{ notice }}</p>
  </section>
</template>

<script setup lang="ts">
/**
 * 编排事件日志流（M2 任务13 · 功能1）。
 *
 * 数据源策略（try-SSE → 降级轮询，全部容错）：
 *   1. mock 模式 → 轮询 mockGetRunEvents（mock.ts 内存事件，含实时 feed）；
 *   2. 真 ACC 且未配置 token → fetch 流式读 SSE
 *      GET /api/v2/orchestration/runs/:run_id/events/stream
 *      （acc-go orchestration_events_stream.go：`id: <seq>` / `event: <type>` /
 *      `data: <Event JSON>` 帧 + `: keepalive` 注释 + `projection.gap` 合成事件；
 *      支持 ?after=<seq> 续传）。
 *      ⚠ 不用 EventSource：ACC 给每个事件都带 `event:` 名，EventSource 对没有
 *      监听器的命名事件**静默丢弃** ⇒ 无法满足「容错未知事件类型」；fetch 流
 *      逐帧解析才不漏。Authorization 头 fetch 能带，配了 token 也能走。
 *      ⚠ 上界口径（§951）：这条流**天生没有固定长度** ⇒ timeoutMs: 0，真正的
 *      上界是组件卸载/降级时 abort 的 AbortController（L2 同构，见 llm-bff 台账先例）。
 *   3. 其余（配置了 token 但想省流 / SSE 握手失败 / 流中断）→ 轮询
 *      GET /api/v2/orchestration/runs/:run_id/events?after=<seq>
 *      （acc-go orchestration_native.go listEvents；404/405 = 该 ACC 未实现，
 *      显示占位说明，不报错——节点状态仍由详情页 5s 轮询兜底）。
 *
 * 渲染：颜色编码 info=蓝 / warn=黄 / error=红；自动滚动到底部，可暂停/继续
 * （手动按钮 + 用户向上滚动自动暂停、回到底部自动恢复）；无事件时占位说明。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  AccApiError,
  getAccBase,
  getAccToken,
  listAccRunEvents,
  normalizeOrchestrationEvent,
  type AccOrchestrationEvent,
} from '../../api/accApi'
import { fetchWithTimeout } from '../../api/fetch-timeout.ts'
import { isMockActive, mockGetRunEvents, mockStartEventFeed, mockStopEventFeed } from './mock'

defineOptions({ name: 'AccLogStream' })

const props = defineProps<{ runId: string }>()

/** 渲染行（由 AccOrchestrationEvent 投影；key 供 v-for 复用）。 */
interface LogLine {
  key: string
  seq: number
  time: string
  level: 'info' | 'warn' | 'error'
  glyph: string
  eventType: string
  text: string
  transition: string
}

type Mode = 'connecting' | 'sse' | 'poll' | 'mock' | 'unavailable'

const MAX_LINES = 500
const POLL_INTERVAL_MS = 3_000
const MOCK_POLL_INTERVAL_MS = 2_000
const MAX_POLL_FAILS = 3

const lines = ref<LogLine[]>([])
const mode = ref<Mode>(isMockActive() ? 'mock' : 'connecting')
const notice = ref('')
const paused = ref(false)
const bodyEl = ref<HTMLElement | null>(null)

const modeLabel = computed(() => {
  switch (mode.value) {
    case 'connecting': return '连接中…'
    case 'sse': return '实时流 (SSE)'
    case 'poll': return '轮询 (3s)'
    case 'mock': return 'Mock 演示'
    case 'unavailable': return '事件接口不可用'
  }
  return ''
})

const emptyText = computed(() => {
  if (mode.value === 'connecting') return '正在连接事件流…'
  if (mode.value === 'unavailable') {
    return '暂无事件数据 — 当前 ACC 未提供可用的日志事件接口（/events 或 /events/stream）。节点状态请看上方轮询。'
  }
  return '暂无事件 — run 可能仍在排队，或 ACC 尚未产生编排事件。这不是错误。'
})

// ---------------------------------------------------------------------------
// 渲染投影
// ---------------------------------------------------------------------------

const SEV_GLYPH: Record<LogLine['level'], string> = { info: '·', warn: '▲', error: '✕' }

/** 严重级：按事件类型与载荷粗判（未知类型一律 info —— 容错优先）。 */
function classifyLevel(e: AccOrchestrationEvent): LogLine['level'] {
  const payloadLevel = typeof e.payload?.level === 'string' ? e.payload.level.toLowerCase() : ''
  if (payloadLevel === 'error' || payloadLevel === 'fatal') return 'error'
  if (payloadLevel === 'warn' || payloadLevel === 'warning') return 'warn'
  const t = e.eventType.toLowerCase()
  if (t.includes('fail') || t.includes('error')) return 'error'
  if (t.includes('blocked') || t.includes('gap') || t.includes('warn') || t.includes('timeout')) return 'warn'
  if (t === 'run.terminal' && (e.newState ?? '').toLowerCase().includes('fail')) return 'error'
  return 'info'
}

/** 人类可读文本：payload.text（mock/未来 ACC）优先，未知类型回退到原始类型名。 */
function describeEvent(e: AccOrchestrationEvent): string {
  const text = e.payload?.text
  if (typeof text === 'string' && text) return text
  const t = e.eventType
  if (t === 'projection.gap') {
    const dropped = typeof e.payload?.dropped === 'number' ? e.payload.dropped : '?'
    return `事件流出现缺口（丢失 ${dropped} 条），已按 seq 重放`
  }
  if (t === 'run.terminal') return `Run 结束：${e.newState ?? 'unknown'}`
  if (t === 'run.created') return 'Run 已创建'
  if (t === 'plan.created') return '执行计划已生成'
  if (t === 'plan.published') return '执行计划已发布'
  if (t === 'task.created') return `任务已创建${e.taskId ? `（${short(e.taskId)}）` : ''}`
  if (t === 'task.ready') return '任务就绪待派发'
  if (t === 'dispatch.created') return `已派发执行${hostSuffix(e)}`
  if (t === 'task.aggregated') return '子任务结果已聚合'
  if (t === 'task.review.approved') return '评审通过'
  return t || '(未知事件)'
}

function hostSuffix(e: AccOrchestrationEvent): string {
  const host = e.payload?.host
  return typeof host === 'string' && host ? ` → ${host}` : ''
}

function short(id: string): string {
  return id.length > 12 ? `${id.slice(0, 12)}…` : id
}

function formatClock(iso: string | undefined): string {
  if (!iso) return '--:--:--'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '--:--:--'
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function appendEvent(e: AccOrchestrationEvent): void {
  const level = classifyLevel(e)
  const oldS = e.oldState ?? ''
  const newS = e.newState ?? ''
  const transition = oldS && newS ? `${oldS} → ${newS}` : ''
  lines.value.push({
    key: `${e.seq}-${e.eventId || e.eventType}`,
    seq: e.seq,
    time: formatClock(e.occurredAt),
    level,
    glyph: SEV_GLYPH[level],
    eventType: e.eventType || 'message',
    text: describeEvent(e),
    transition,
  })
  if (lines.value.length > MAX_LINES) lines.value.splice(0, lines.value.length - MAX_LINES)
  scrollIfAuto()
}

function scrollIfAuto(): void {
  if (paused.value) return
  void nextTick(() => {
    const el = bodyEl.value
    if (!el) return
    el.scrollTop = el.scrollHeight
  })
}

/** 用户向上滚动 → 自动暂停；回到底部 → 自动恢复（与按钮共用一个开关）。 */
function onScroll(): void {
  const el = bodyEl.value
  if (!el) return
  const fromBottom = el.scrollHeight - el.scrollTop - el.clientHeight
  if (fromBottom > 40 && !paused.value) paused.value = true
  else if (fromBottom <= 8 && paused.value) paused.value = false
}

function togglePause(): void {
  paused.value = !paused.value
  if (!paused.value) scrollIfAuto()
}

// ---------------------------------------------------------------------------
// 数据源 1：mock
// ---------------------------------------------------------------------------

let mockTimer: ReturnType<typeof setInterval> | null = null
let pollTimer: ReturnType<typeof setInterval> | null = null
let lastSeq = 0
let pollFails = 0
let disposed = false
let streamAbort: AbortController | null = null

function startMock(): void {
  mode.value = 'mock'
  mockStartEventFeed(props.runId)
  const pollMock = (): void => {
    void mockGetRunEvents(props.runId, lastSeq)
      .then((res) => {
        if (disposed) return
        for (const e of res.events) appendEvent(e)
        lastSeq = Math.max(lastSeq, res.lastSeq)
      })
      .catch(() => {
        /* mock run 不存在：静默（详情页会先报错） */
      })
  }
  pollMock() // 种子事件立即渲染，不等第一个间隔
  mockTimer = setInterval(pollMock, MOCK_POLL_INTERVAL_MS)
}

// ---------------------------------------------------------------------------
// 数据源 2：SSE（fetch 流式读，容忍未知事件类型）
// ---------------------------------------------------------------------------

async function startStream(): Promise<void> {
  mode.value = 'connecting'
  streamAbort = new AbortController()
  const base = getAccBase()
  const token = getAccToken()
  const headers: Record<string, string> = { Accept: 'text/event-stream' }
  if (token) headers.Authorization = `Bearer ${token}`
  const after = lastSeq > 0 ? `?after=${encodeURIComponent(String(lastSeq))}` : ''
  const url = `${base}/api/v2/orchestration/runs/${encodeURIComponent(props.runId)}/events/stream${after}`

  let res: Response
  try {
    // §951 例外口径：SSE 长连接**天生没有固定长度** ⇒ timeoutMs: 0；
    // 真正的上界是 streamAbort（组件卸载/降级轮询时 abort），与 llm-bff 的
    // SSE 台账先例同构（L2：signal 由本组件持有并在离开页面时触发）。
    res = await fetchWithTimeout(
      url,
      { headers, signal: streamAbort.signal },
      { timeoutMs: 0, label: 'acc-run-events-stream' },
    )
  } catch (e) {
    if (disposed || streamAbort?.signal.aborted) return
    degradeToPoll(`实时流连接失败（${e instanceof Error ? e.message : String(e)}），已切换为轮询`)
    return
  }

  if (!res.ok || !res.body) {
    if (disposed) return
    degradeToPoll(`实时流不可用（HTTP ${res.status}），已切换为轮询`)
    return
  }

  mode.value = 'sse'
  notice.value = ''

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done || disposed) break
      buffer += decoder.decode(value, { stream: true })
      let idx = buffer.indexOf('\n\n')
      while (idx >= 0) {
        handleSseFrame(buffer.slice(0, idx))
        buffer = buffer.slice(idx + 2)
        idx = buffer.indexOf('\n\n')
      }
    }
  } catch {
    /* 读中断：网络抖动或 abort —— 走下面的收尾判断 */
  }
  if (disposed) return
  // 流被服务端正常关闭（run 终结/服务重启）→ 降级轮询补齐增量，不丢尾事件。
  degradeToPoll('实时流已结束，切换为轮询以补齐剩余事件')
}

/** 解析一帧 SSE（id/event/data 三行 + keepalive 注释行）。 */
function handleSseFrame(frame: string): void {
  let id = ''
  let eventType = ''
  let data = ''
  for (const rawLine of frame.split('\n')) {
    if (rawLine.startsWith(':')) continue // keepalive 注释
    const colon = rawLine.indexOf(':')
    if (colon < 0) continue
    const field = rawLine.slice(0, colon).trim()
    const value = rawLine.slice(colon + 1).replace(/^ /, '')
    if (field === 'id') id = value
    else if (field === 'event') eventType = value
    else if (field === 'data') data += (data ? '\n' : '') + value
  }
  if (!data) return
  let event: AccOrchestrationEvent
  try {
    event = normalizeOrchestrationEvent(JSON.parse(data) as Record<string, unknown>)
  } catch {
    event = { seq: 0, eventId: '', eventType: eventType || 'message' }
  }
  if (!event.eventType) event.eventType = eventType || 'message'
  const seq = Number(id) || event.seq || lastSeq + 1
  event.seq = seq
  lastSeq = Math.max(lastSeq, seq)
  appendEvent(event)
}

// ---------------------------------------------------------------------------
// 数据源 3：轮询（降级路径 / 配置了 token 的默认路径）
// ---------------------------------------------------------------------------

function startPolling(reason?: string): void {
  mode.value = 'poll'
  if (reason) notice.value = reason
  void pollOnce()
  pollTimer = setInterval(() => void pollOnce(), POLL_INTERVAL_MS)
}

function degradeToPoll(reason: string): void {
  if (disposed) return
  stopStream()
  startPolling(reason)
}

async function pollOnce(): Promise<void> {
  if (disposed) return
  try {
    const events = await listAccRunEvents(props.runId, lastSeq)
    pollFails = 0
    if (mode.value !== 'poll') mode.value = 'poll'
    for (const e of events) {
      if (e.seq > lastSeq) lastSeq = e.seq
      appendEvent(e)
    }
    if (notice.value.startsWith('轮询暂时失败')) notice.value = ''
  } catch (e) {
    if (disposed) return
    // 该 ACC 没实现事件接口（或 run 不存在）→ 占位说明，不再空转。
    if (e instanceof AccApiError && (e.status === 404 || e.status === 405 || e.status === 400)) {
      stopPolling()
      mode.value = 'unavailable'
      notice.value = ''
      return
    }
    pollFails += 1
    if (pollFails >= MAX_POLL_FAILS) {
      stopPolling()
      mode.value = 'unavailable'
      notice.value = ''
      return
    }
    notice.value = `轮询暂时失败（${pollFails}/${MAX_POLL_FAILS}）：${e instanceof Error ? e.message : String(e)}`
  }
}

function stopPolling(): void {
  if (pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

function stopStream(): void {
  if (streamAbort) {
    streamAbort.abort()
    streamAbort = null
  }
}

function stopMock(): void {
  if (mockTimer !== null) {
    clearInterval(mockTimer)
    mockTimer = null
  }
  mockStopEventFeed(props.runId)
}

// ---------------------------------------------------------------------------
// 生命周期
// ---------------------------------------------------------------------------

onMounted(() => {
  if (isMockActive()) {
    startMock()
    return
  }
  // 有 token 时直接轮询（fetch 流能带 Authorization，仍先试 SSE —— 失败自动降级）。
  void startStream().catch(() => degradeToPoll('实时流异常，已切换为轮询'))
})

onBeforeUnmount(() => {
  disposed = true
  stopMock()
  stopPolling()
  stopStream()
})
</script>

<style scoped>
.ls {
  margin: 0 var(--space-3, 12px) var(--space-3, 12px);
  background: var(--bg-card, #fff);
  border: 1px solid var(--border, #e5e7eb);
  border-radius: var(--radius-md, 12px);
  overflow: hidden;
}
.ls-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border, #e5e7eb);
}
.ls-title { margin: 0; font-size: var(--text-smd, 13px); color: var(--text-secondary, #555); font-weight: 600; }
.ls-mode {
  padding: 1px 8px;
  border-radius: 999px;
  font-size: 12px;
  color: #fff;
  background: #94a3b8;
  white-space: nowrap;
}
.ls-mode.mode-sse { background: #16a34a; }
.ls-mode.mode-poll { background: #2563eb; }
.ls-mode.mode-mock { background: #d97706; }
.ls-mode.mode-connecting { background: #94a3b8; }
.ls-mode.mode-unavailable { background: #64748b; }
.ls-count { font-size: 12px; color: var(--text-tertiary, #888); }
.ls-pause {
  margin-left: auto;
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: var(--text-secondary, #555);
  padding: 4px 10px;
  font-size: 12px;
  cursor: pointer;
  white-space: nowrap;
}
.ls-pause:disabled { opacity: 0.4; cursor: not-allowed; }

.ls-body {
  height: 240px;
  overflow-y: auto;
  overscroll-behavior: contain;
  background: #0f172a;
  padding: 8px 10px;
  /* §css-vars：等宽字体走 --font-mono token，不写死字体栈。
     写死 `ui-monospace, SFMono-Regular, …` 会让中文掉回默认字体（与正文不一致），
     且违反「外观只有一处真相（token）」的纪律。 */
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.6;
}
.ls-empty {
  margin: 0;
  padding: 24px 8px;
  color: #94a3b8;
  font-family: inherit;
  text-align: center;
  font-size: var(--text-smd, 13px);
}
.ls-line { display: flex; gap: 6px; align-items: baseline; word-break: break-all; }
.ls-time { color: #64748b; flex-shrink: 0; }
.ls-sev { flex-shrink: 0; width: 12px; text-align: center; }
.ls-type { color: #94a3b8; flex-shrink: 0; }
.ls-text { color: #e2e8f0; min-width: 0; }
.ls-line.level-info .ls-sev, .ls-line.level-info .ls-text { color: #7dd3fc; }
.ls-line.level-info .ls-text { color: #bae6fd; }
.ls-line.level-warn .ls-sev, .ls-line.level-warn .ls-type { color: #fbbf24; }
.ls-line.level-warn .ls-text { color: #fde68a; }
.ls-line.level-error .ls-sev, .ls-line.level-error .ls-type { color: #f87171; }
.ls-line.level-error .ls-text { color: #fecaca; }

.ls-notice {
  margin: 0;
  padding: 8px 12px;
  border-top: 1px solid var(--border, #e5e7eb);
  color: var(--text-tertiary, #888);
  font-size: 12px;
  word-break: break-all;
}
</style>
