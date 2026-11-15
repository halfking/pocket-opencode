/**
 * ACC 看板的状态视觉编码（M1 任务9 验收口径 + M2 任务13 图标统一）：
 *   绿 = 完成、黄 = 进行中、红 = 失败、灰 = 待执行、⏸ = 等待审批、🔄 = 移交中。
 *
 * M2 任务13：状态图标统一为任务书指定的字符集 ——
 *   ✅完成 / ⏳进行中 / ⏸暂停(等待审批) / ❌失败 / 🔄移交中(handoff_pending)。
 *   pending / cancelled / skipped / unknown 不在指定清单里，保留灰点原样。
 *
 * 独立成纯函数模块：TaskBoard / TaskDetail 共用同一张表，保证两页颜色一致。
 * 颜色用字面量而不是主题 token —— 审批态的颜色是**语义**不是皮肤，五个状态
 * 色在任何主题下都必须可区分。
 */

import type { AccNodeStatus, AccRunStatus } from '../../api/accApi'

export type StatusTone = 'done' | 'run' | 'fail' | 'pending' | 'gate' | 'handoff' | 'cancel' | 'skip' | 'unknown'

export interface StatusVisual {
  tone: StatusTone
  /** 中文标签（看板徽章直接显示）。 */
  label: string
  /** 等待审批用 ⏸、移交中用 🔄、完成/进行/失败用任务书指定字符；其余用色点。 */
  glyph?: string
}

const NODE_VISUAL: Record<AccNodeStatus, StatusVisual> = {
  completed: { tone: 'done', label: '完成', glyph: '✅' },
  running: { tone: 'run', label: '进行中', glyph: '⏳' },
  failed: { tone: 'fail', label: '失败', glyph: '❌' },
  pending: { tone: 'pending', label: '待执行' },
  waiting_approval: { tone: 'gate', label: '等待审批', glyph: '⏸' },
  handoff_pending: { tone: 'handoff', label: '移交中', glyph: '🔄' },
  cancelled: { tone: 'cancel', label: '已取消' },
  skipped: { tone: 'skip', label: '已跳过' },
  unknown: { tone: 'unknown', label: '未知' },
}

const RUN_VISUAL: Record<AccRunStatus, StatusVisual> = {
  completed: { tone: 'done', label: '已完成', glyph: '✅' },
  running: { tone: 'run', label: '运行中', glyph: '⏳' },
  failed: { tone: 'fail', label: '失败', glyph: '❌' },
  pending: { tone: 'pending', label: '排队中' },
  waiting_approval: { tone: 'gate', label: '等待审批', glyph: '⏸' },
  handoff_pending: { tone: 'handoff', label: '移交中', glyph: '🔄' },
  cancelled: { tone: 'cancel', label: '已取消' },
  unknown: { tone: 'unknown', label: '未知' },
}

export function nodeVisual(status: AccNodeStatus): StatusVisual {
  return NODE_VISUAL[status] ?? NODE_VISUAL.unknown
}

export function runVisual(status: AccRunStatus): StatusVisual {
  return RUN_VISUAL[status] ?? RUN_VISUAL.unknown
}

/** run 是否处于活跃态（详情页据此决定是否自动轮询）。移交中也算活跃（仍在推进）。 */
export function isRunActive(status: AccRunStatus): boolean {
  return status === 'running' || status === 'pending' || status === 'handoff_pending'
}

/** 一个 run 的节点进度摘要（看板卡片副标题 + 详情页进度条）。 */
export function summarizeProgress(nodes: { status: AccNodeStatus }[]): {
  done: number
  total: number
  waiting: number
  handoff: number
} {
  const done = nodes.filter((n) => n.status === 'completed' || n.status === 'skipped').length
  const waiting = nodes.filter((n) => n.status === 'waiting_approval').length
  const handoff = nodes.filter((n) => n.status === 'handoff_pending').length
  return { done, total: nodes.length, waiting, handoff }
}

/** 进度条百分比（0-100，无节点时 0）。 */
export function progressPercent(nodes: { status: AccNodeStatus }[]): number {
  const p = summarizeProgress(nodes)
  if (p.total === 0) return 0
  return Math.round((p.done / p.total) * 100)
}

export function formatTime(iso: string | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
