<script setup lang="ts">
/**
 * SessionStatusBar — 会话工作台顶部一行式实时状态条（设计方案 v2 §4.3-1）。
 *
 * 展示优先级（§4.1 同款）：
 *   1. 审批待办：`🔴 等待审批 · 5 分钟 [查看]`（审批态来自 approval store，
 *      [查看] 通知父级聚焦既有 ApprovalPanel）
 *   2. 运行态：`🟢 改文件中 · 40s [停止]`（phase 来自 session.activity 事件；
 *      事件不可用时由父级传入消息流推导的降级 phase）
 *   3. 空闲：`⚪ 空闲` 弱化展示
 *
 * 时长 = now - lastEventAt 秒表（事件 last_event_at；降级时传最后一条消息时间
 * 近似），1s tick 实时驱动。审批等待时长用客户端首见时间近似（P0 近似，
 * 与 useInstanceApprovals 同款）。
 *
 * 触摸纪律：整条高度 44px；动作按钮热区 ≥44px 高、≥56px 宽（无 :hover 依赖）。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { PHASE_LABELS, type SessionPhase } from './useSessionEvents'

const props = defineProps<{
  /** 当前 phase（事件 or 降级推导）；null = 完全未知（仅显示审批/兜底态）。 */
  phase: SessionPhase | null
  /** phase 对应的起点（epoch ms，事件 last_event_at 或消息流近似）。 */
  lastEventAt: number | null
  /** 会话是否在流式运行（决定 [停止] 显隐）。 */
  active: boolean
  /** 待审批数（权限 + 问答）。 */
  pendingCount: number
  /** 最早一条待审批的客户端首见时间（ms）；无待办传 null。 */
  approvalFirstSeenAt: number | null
}>()

const emit = defineEmits<{
  (e: 'stop'): void
  (e: 'view-approvals'): void
}>()

const now = ref(Date.now())
let timer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  timer = setInterval(() => {
    now.value = Date.now()
  }, 1000)
})
onBeforeUnmount(() => {
  if (timer !== null) clearInterval(timer)
})

/** 事件不可用时的兜底文案（与旧 top-bar 状态一致）。 */
const fallbackLabel = computed(() => (props.active ? '生成中…' : '空闲'))

const mode = computed<'approval' | 'running' | 'idle' | 'unknown'>(() => {
  if (props.pendingCount > 0) return 'approval'
  if (props.phase === null) return props.active ? 'unknown' : 'idle'
  if (props.phase === 'idle') return props.active ? 'running' : 'idle'
  return 'running'
})

const phaseLabel = computed(() => {
  if (props.phase === null) return fallbackLabel.value
  // 事件说 idle 但 SSE 仍在流式：显示生成中文案，避免"空闲 · 40s"矛盾
  if (props.phase === 'idle') return props.active ? fallbackLabel.value : PHASE_LABELS.idle
  return PHASE_LABELS[props.phase]
})

function elapsedSince(since: number | null): string {
  if (since === null || !Number.isFinite(since)) return ''
  const ms = Math.max(0, now.value - since)
  if (ms < 60_000) return `${Math.floor(ms / 1000)}s`
  const minutes = Math.floor(ms / 60_000)
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  return `${hours} 小时 ${minutes % 60} 分钟`
}

const runningElapsed = computed(() => elapsedSince(props.lastEventAt))
const approvalElapsed = computed(() => elapsedSince(props.approvalFirstSeenAt))

/** 运行态圆点：🟢；空闲 ⚪；审批 🔴（与设计文档 §4.1 配色语义一致）。 */
const dot = computed(() => {
  if (mode.value === 'approval') return '🔴'
  if (mode.value === 'running') return '🟢'
  if (mode.value === 'unknown') return props.active ? '🟢' : '⚪'
  return '⚪'
})

const label = computed(() => {
  if (mode.value === 'approval') return '等待审批'
  if (mode.value === 'idle') return '空闲'
  return phaseLabel.value
})

const elapsedText = computed(() => {
  if (mode.value === 'approval') return approvalElapsed.value
  if (mode.value === 'running') return runningElapsed.value
  return ''
})
</script>

<template>
  <div class="status-bar" :class="`mode-${mode}`" role="status" :aria-label="`${label}${elapsedText ? ' · ' + elapsedText : ''}`">
    <span class="dot" aria-hidden="true">{{ dot }}</span>
    <span class="label">{{ label }}</span>
    <span v-if="elapsedText" class="elapsed">{{ elapsedText }}</span>
    <span class="spacer"></span>
    <button
      v-if="mode === 'approval'"
      type="button"
      class="action"
      @click="emit('view-approvals')"
    >
      查看
    </button>
    <button
      v-else-if="mode === 'running' && active"
      type="button"
      class="action stop"
      @click="emit('stop')"
    >
      停止
    </button>
  </div>
</template>

<style scoped>
.status-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 44px; /* 整条热区 ≥44px */
  padding: 0 var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  font-size: var(--text-sm);
  color: var(--text-primary);
}
.mode-idle {
  color: var(--text-muted);
}
.dot {
  font-size: var(--text-sm);
  line-height: 1;
}
.label {
  font-weight: var(--font-weight-medium);
  white-space: nowrap;
}
.elapsed {
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.spacer {
  flex: 1 1 auto;
}
.action {
  /* 热区 ≥44px 高（铺满状态条）、≥56px 宽；无 :hover 依赖，仅 :active 反馈 */
  align-self: stretch;
  min-width: 56px;
  padding: 0 var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  color: var(--brand-primary);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  display: flex;
  align-items: center;
  justify-content: center;
}
.action.stop {
  color: var(--danger);
  border-color: var(--danger);
}
.action:active {
  background: var(--bg-subtle);
  transform: scale(0.97);
}
</style>
