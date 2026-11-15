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
 * ACC event projection only. The process-owned runtime retains transport, cursor
 * and bounded replay across route changes; this view subscribes and detaches.
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { AccOrchestrationEvent } from '../../api/accApi'
import type { AccLogSnapshot } from './acc-log-runtime.ts'
import { getAccLogSession } from './acc-log-session.ts'
import { isMockActive } from './mock'

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

type Mode = AccLogSnapshot['mode']

const lines = ref<LogLine[]>([])
const mode = ref<Mode>(isMockActive() ? 'mock' : 'connecting')
const notice = ref('')
const logSession = getAccLogSession()
const paused = ref(false)
const bodyEl = ref<HTMLElement | null>(null)

const modeLabel = computed(() => {
  switch (mode.value) {
    case 'connecting': return '连接中…'
    case 'sse': return '实时流 (SSE)'
    case 'poll': return '轮询 (3s)'
    case 'mock': return 'Mock 演示'
    case 'unavailable': return '事件接口不可用'
    case 'ended': return '已结束'
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

function projectEvent(e: AccOrchestrationEvent): LogLine {
  const level = classifyLevel(e)
  const oldS = e.oldState ?? ''
  const newS = e.newState ?? ''
  const transition = oldS && newS ? `${oldS} → ${newS}` : ''
  return {
    key: `${e.seq}-${e.eventId || e.eventType}`,
    seq: e.seq,
    time: formatClock(e.occurredAt),
    level,
    glyph: SEV_GLYPH[level],
    eventType: e.eventType || 'message',
    text: describeEvent(e),
    transition,
  }
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
// 视图订阅（真实流与演示流均由 runtime 持有）
// ---------------------------------------------------------------------------

let detach = () => {}
watch([() => props.runId, logSession.authority], ([runId]) => {
  detach()
  paused.value = false
  detach = logSession.runtime.subscribe(runId, snapshot => {
    lines.value = snapshot.events.map(projectEvent)
    mode.value = snapshot.mode
    notice.value = snapshot.notice
    scrollIfAuto()
  }, isMockActive() ? 'mock' : 'live')
}, { immediate: true, flush: 'sync' })

// Leaving a route releases only this subscriber, never the process-owned IO.
onBeforeUnmount(() => detach())

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
  font-size: var(--text-sm);
  color: #fff;
  background: #94a3b8;
  white-space: nowrap;
}
.ls-mode.mode-sse { background: #16a34a; }
.ls-mode.mode-poll { background: #2563eb; }
.ls-mode.mode-mock { background: #d97706; }
.ls-mode.mode-connecting { background: #94a3b8; }
.ls-mode.mode-unavailable { background: #64748b; }
.ls-count { font-size: var(--text-sm); color: var(--text-tertiary, #888); }
.ls-pause {
  margin-left: auto;
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: var(--text-secondary, #555);
  padding: 4px 10px;
  font-size: var(--text-sm);
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
  font-family: var(--font-mono);
  font-size: var(--text-sm);
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
  font-size: var(--text-sm);
  word-break: break-all;
}
</style>
