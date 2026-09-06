<script setup lang="ts">
/**
 * SessionStatusBar — 会话工作台动态状态信号图标（P1.5 界面减负改造）。
 *
 * 设计原则「信号即界面」（设计 v2 §2.2）的入口化：原一行式状态条收敛为
 * 头部 44px 圆形图标按钮，状态与动作合一——
 *   - 审批待办（最高优先级）：notifications_active + 待办角标，红色呼吸动画，
 *     单击 = 查看审批（emit view-approvals）；
 *   - 运行中（phase≠idle 或 SSE 流式）：progress_activity 持续旋转，品牌绿，
 *     单击 = 停止（emit stop；**无二次确认**——停止可经同一图标恢复，误触
 *     成本一次点击，两步介入原则优先，见 docs/2026-08-27-p1.5-ui-declutter.md
 *     设计决策 DD-1；文字模板里的「停下」仍保留二次确认不变）；
 *   - 空闲/停止：play_arrow 弱化色，单击 = 继续（emit continue）。
 *
 * 状态派生（resolveSessionStatusMode/sessionStatusLabel）与时长格式化是
 * useSessionEvents 导出的纯函数，头部副标题（状态·时长文本）与图标共用。
 * 时长 = now - lastEventAt（事件 last_event_at，降级为最后一条消息时间近似），
 * 由 useElapsedNow 自适应节拍驱动（ISSUES #20：空闲态零定时器，不再每秒重绘）。
 *
 * 触摸纪律：44px 热区，仅 :active 反馈，无 :hover 依赖；动画遵循全局
 * prefers-reduced-motion 裁剪。
 */
import { computed } from 'vue'
import { useElapsedNow } from '../../composables/useElapsedNow'
import {
  formatStatusElapsed,
  resolveSessionStatusMode,
  sessionStatusLabel,
  type SessionPhase,
  type SessionStatusMode,
} from './useSessionEvents'

const props = defineProps<{
  /** 当前 phase（事件 or 降级推导）；null = 完全未知（按 active 兜底）。 */
  phase: SessionPhase | null
  /** phase 对应的起点（epoch ms，事件 last_event_at 或消息流近似）。 */
  lastEventAt: number | null
  /** 会话是否在流式运行（决定停止/继续动作与运行图标显隐）。 */
  active: boolean
  /** 待审批数（权限 + 问答）。 */
  pendingCount: number
  /** 最早一条待审批的客户端首见时间（ms）；无待办传 null。 */
  approvalFirstSeenAt: number | null
}>()

const emit = defineEmits<{
  (e: 'stop'): void
  (e: 'continue'): void
  (e: 'view-approvals'): void
}>()

/** 自适应"当前时间"：只在有时长可显示时跳表（ISSUES #20 修复）。 */
const now = useElapsedNow(() => [props.approvalFirstSeenAt, props.lastEventAt])

const mode = computed<SessionStatusMode>(() =>
  resolveSessionStatusMode({
    phase: props.phase,
    active: props.active,
    pendingCount: props.pendingCount,
  }),
)

const label = computed(() =>
  sessionStatusLabel({ phase: props.phase, active: props.active, pendingCount: props.pendingCount }),
)

const elapsedText = computed(() => {
  if (mode.value === 'approval') {
    return formatStatusElapsed(
      props.approvalFirstSeenAt === null ? null : now.value - props.approvalFirstSeenAt,
    )
  }
  if (mode.value === 'running') {
    return formatStatusElapsed(props.lastEventAt === null ? null : now.value - props.lastEventAt)
  }
  return ''
})

/** 图标语义（Material Symbols 子集内）：审批=通知、运行=旋转进度、空闲=播放。 */
const iconName = computed(() => {
  if (mode.value === 'approval') return 'notifications_active'
  if (mode.value === 'running') return 'progress_activity'
  return 'play_arrow'
})

const actionLabel = computed(() => {
  if (mode.value === 'approval') return '点击查看审批'
  if (mode.value === 'running') return '点击停止'
  return '点击继续'
})

const ariaLabel = computed(() => {
  const parts = [label.value]
  if (elapsedText.value) parts.push(elapsedText.value)
  if (props.pendingCount > 0) parts.push(`${props.pendingCount} 项待办`)
  parts.push(actionLabel.value)
  return parts.join(' · ')
})

function onTap(): void {
  if (mode.value === 'approval') emit('view-approvals')
  else if (mode.value === 'running') emit('stop')
  else emit('continue')
}
</script>

<template>
  <button
    type="button"
    class="status-icon"
    :class="`mode-${mode}`"
    role="status"
    :aria-label="ariaLabel"
    @click="onTap"
  >
    <span class="material-symbols-outlined icon" aria-hidden="true">{{ iconName }}</span>
    <span v-if="mode === 'approval' && pendingCount > 0 && pendingCount < 10" class="badge" aria-hidden="true">
      {{ pendingCount }}
    </span>
  </button>
</template>

<style scoped>
/* 44px 圆形热区（信号即入口；无 :hover 依赖） */
.status-icon {
  position: relative;
  flex: 0 0 auto;
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-full);
  border: 1px solid transparent;
  background: var(--bg-subtle);
  color: var(--text-secondary);
  cursor: pointer;
}
.status-icon:active {
  transform: scale(0.92);
}

/* 运行中：品牌绿 + 持续旋转（progress_activity 为圆环图标） */
.status-icon.mode-running {
  background: var(--success-bg);
  color: var(--success);
}
.status-icon.mode-running .icon {
  animation: status-spin 1.6s linear infinite;
}
@keyframes status-spin {
  to {
    transform: rotate(360deg);
  }
}

/* 审批待办：danger 语义 + 呼吸提醒 + 待办角标 */
.status-icon.mode-approval {
  background: var(--danger-bg);
  color: var(--danger);
}
.status-icon.mode-approval .icon {
  animation: status-pulse 1.2s var(--ease-out) infinite;
}
@keyframes status-pulse {
  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.65;
    transform: scale(1.12);
  }
}
.badge {
  position: absolute;
  top: 2px;
  right: 2px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: var(--radius-full);
  background: var(--danger);
  color: var(--text-inverse);
  font-size: 10px;
  font-weight: 700;
  line-height: 16px;
  text-align: center;
}

/* 空闲：弱化展示（信号即界面——无信号不抢注意力），点击=继续 */
.status-icon.mode-idle {
  background: var(--bg-subtle);
  color: var(--text-muted);
}

.icon {
  font-size: 24px;
  line-height: 1;
}
</style>
