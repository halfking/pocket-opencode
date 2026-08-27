/**
 * health.ts — 任务/会话健康度五态模型（设计方案 v2 §4.1）。
 *
 * P0 近似数据源说明：后端 `session.activity` / `task.health` 事件尚未上线
 * （§5.1，P1 起交付），本模块只用前端已有信号：
 *   - needs-input：待处理审批数（approval.* 事件 + /api/mobile/approvals 拉取）
 *   - stalled：now - updatedAt 超过 STALLED_AFTER_MS
 *   - error：调用方显式传入 hasError（如会话 status === 'error'）
 *   - running：updatedAt 在 ACTIVE_WITHIN_MS 内
 *   - idle：不活跃 / 已完成
 * 事件上线后替换数据源即可，判定阈值与展示语义不变。
 *
 * 纯函数、零运行时依赖，可在 node --test 下直接测试。
 */

/** 健康度五态。优先级：needs-input > stalled > error > running > idle。 */
export type SessionHealth = 'needs-input' | 'stalled' | 'error' | 'running' | 'idle'

/** 与 CSS 色点 token 对应的语义色（映射见 TasksView 样式 .tone-*）。 */
export type HealthTone = 'danger' | 'warning' | 'success' | 'muted'

/** 更新时间在此时长内视为活跃（running）。 */
export const ACTIVE_WITHIN_MS = 2 * 60_000

/** 超过此时长无更新判为疑似卡死（stalled）。设计基线：10 分钟。 */
export const STALLED_AFTER_MS = 10 * 60_000

export interface HealthInput {
  /** 实体是否活跃（completed 任务 / 明确空闲的会话传 false） */
  active?: boolean
  /** 最近一次活动时间；P0 用 task/session 的 updatedAt 近似 */
  updatedAt?: string | number
  /** 待处理审批（permission+question）数量，>0 即 needs-input */
  pendingApprovals?: number
  /** 该条审批最早出现的时间戳（客户端首见时间，近似等待时长） */
  pendingSince?: number
  /** 显式错误信号（如最近一轮失败） */
  hasError?: boolean
  /** 测试注入用 */
  now?: number
}

export interface HealthSignal {
  state: SessionHealth
  tone: HealthTone
  /** 色点 emoji，与设计 §4.1 表格一致 */
  icon: string
  /** 当前动作短语（P0 近似，无 phase 事件） */
  action: string
  /** 距上次活动 / 审批等待时长（人类可读），未知为空串 */
  since: string
  /** since 对应的毫秒时长（排序用），未知为 0 */
  sinceMs: number
}

function toTime(v: string | number | undefined): number | null {
  if (v === undefined || v === null || v === '') return null
  const t = typeof v === 'number' ? v : Date.parse(v)
  return Number.isFinite(t) ? t : null
}

/** 把毫秒时长格式化为中文短句：刚刚 / N 秒 / N 分钟 / N 小时 / N 天。 */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return ''
  if (ms < 10_000) return '刚刚'
  const sec = Math.floor(ms / 1_000)
  if (sec < 60) return `${sec} 秒`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min} 分钟`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr} 小时`
  return `${Math.floor(hr / 24)} 天`
}

/** 计算单个任务/会话的健康信号。 */
export function assessHealth(input: HealthInput): HealthSignal {
  const now = input.now ?? Date.now()
  const last = toTime(input.updatedAt)
  const age = last !== null ? Math.max(0, now - last) : null
  const since = age !== null ? formatDuration(age) : ''
  const sinceMs = age ?? 0

  // idle：不活跃的实体弱化展示（已完成 / 无更新时间）。
  if (input.active === false) {
    return { state: 'idle', tone: 'muted', icon: '⚪', action: '空闲', since, sinceMs }
  }

  // needs-input：等待用户介入，最高优先级。等待时长优先取审批首见时间。
  if ((input.pendingApprovals ?? 0) > 0) {
    const pendingAge =
      typeof input.pendingSince === 'number' ? Math.max(0, now - input.pendingSince) : sinceMs
    return {
      state: 'needs-input',
      tone: 'danger',
      icon: '🔴',
      action: '等审批',
      since: formatDuration(pendingAge),
      sinceMs: pendingAge,
    }
  }

  // stalled：疑似卡死（无响应超过阈值）。
  if (age !== null && age > STALLED_AFTER_MS) {
    return { state: 'stalled', tone: 'warning', icon: '🟠', action: '无响应', since, sinceMs }
  }

  // error：最近一轮失败。
  if (input.hasError) {
    return { state: 'error', tone: 'warning', icon: '❌', action: '上一轮失败', since, sinceMs }
  }

  // updatedAt 未知且无其他信号：视为空闲。
  if (age === null) {
    return { state: 'idle', tone: 'muted', icon: '⚪', action: '空闲', since: '', sinceMs: 0 }
  }

  // running：仍在活跃窗口内（ACTIVE_WITHIN_MS ~ STALLED_AFTER_MS 之间维持 running，
  // 超过 10 分钟才会被判 stalled）。
  return { state: 'running', tone: 'success', icon: '🟢', action: '进行中', since, sinceMs }
}

/** 分诊条聚合结果（设计方案 §3.3 L0）。 */
export interface TriageSummary {
  /** 需要用户介入的条数（needs-input，按实体计） */
  needsInput: number
  /** 疑似卡死条数 */
  stalled: number
  /** 运行中条数 */
  running: number
  /** needsInput + stalled > 0 */
  hasAttention: boolean
}

/** 对一组信号聚合计数。 */
export function summarizeHealth(signals: HealthSignal[]): TriageSummary {
  const summary: TriageSummary = { needsInput: 0, stalled: 0, running: 0, hasAttention: false }
  for (const s of signals) {
    if (s.state === 'needs-input') summary.needsInput++
    else if (s.state === 'stalled') summary.stalled++
    else if (s.state === 'running') summary.running++
  }
  summary.hasAttention = summary.needsInput + summary.stalled > 0
  return summary
}
