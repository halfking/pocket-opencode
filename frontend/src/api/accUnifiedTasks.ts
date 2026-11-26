import { postAcc, readAcc, type AccRequestAuthority } from './accApi'

interface UnifiedTask {
  task_id: string
  run_id: string
  title: string
  status: string
}

interface UnifiedTaskPage {
  tasks: UnifiedTask[]
  pagination: { limit: number; offset: number; total: number; has_more: boolean }
}

interface UnifiedTaskDetail extends UnifiedTask {
  description?: string
}

interface UnifiedTaskProgress {
  task_id: string
  status: string
  progress: { completed: number; total: number; percentage: number; review: number }
  updated_at: string
}

interface TaskGraphNode {
  task_id: string
  title?: string
  parent_task_id?: string
  status: string
}

interface TaskEdge { from: string; to: string }

export interface BillingTotals { requests: number; debited_credits: number; net_credits: number }
export interface RunBilling {
  run_id: string
  state: 'pending' | 'reconciled' | 'partial' | 'unobserved'
  totals?: BillingTotals
  attempts: {
    task_id: string; revision_id: string; attempt: number; dispatch_id?: string; state: string; totals?: BillingTotals
  }[]
}

export interface RunCommandReceipt {
  dispatch_id: string
  task_id: string
  attempt: number
  status: string
  can_cancel: boolean
  cancel_state?: 'requested' | 'delivered' | 'effective'
  verified_executor: boolean
}

interface TaskTrajectory {
  view: { scope: string; run_id: string; segments: { kind: string; items?: unknown[] }[] }
}

function isTask(row: UnifiedTask | null): row is UnifiedTask {
  return Boolean(row && typeof row.task_id === 'string' && row.task_id && typeof row.run_id === 'string' && row.run_id
    && typeof row.title === 'string' && typeof row.status === 'string' && row.status)
}

export async function listUnifiedTasks(authority: AccRequestAuthority, offset: number, signal?: AbortSignal) {
  const page = await readAcc<UnifiedTaskPage>(authority, `/api/v2/tasks?limit=20&offset=${offset}`, signal)
  const pagination = page?.pagination
  if (!Array.isArray(page?.tasks) || !page.tasks.every(isTask) || !pagination || pagination.offset !== offset
      || typeof pagination.has_more !== 'boolean' || (pagination.has_more && !page.tasks.length)) {
    throw new Error('Invalid ACC tasks page')
  }
  return { rows: page.tasks, nextOffset: pagination.has_more ? offset + page.tasks.length : null }
}

export async function readUnifiedTask(
  authority: AccRequestAuthority, selection: Pick<UnifiedTask, 'task_id' | 'run_id'>, signal?: AbortSignal,
) {
  const taskPath = `/api/v2/tasks/${encodeURIComponent(selection.task_id)}`
  const task = await readAcc<UnifiedTaskDetail>(authority, taskPath, signal)
  if (!isTask(task) || task.task_id !== selection.task_id || task.run_id !== selection.run_id) {
    throw new Error('ACC task identity changed')
  }
  const [progress, graph] = await Promise.all([
    readAcc<UnifiedTaskProgress>(authority, `${taskPath}/progress`, signal),
    readRunGraph(authority, task.run_id, signal),
  ])
  if (progress?.task_id !== task.task_id || typeof progress.status !== 'string' || !progress.status
      || !progress.progress || ![progress.progress.completed, progress.progress.total, progress.progress.review,
        progress.progress.percentage].every(value => Number.isSafeInteger(value) && value >= 0)
      || progress.progress.percentage > 100 || progress.progress.completed > progress.progress.total) {
    throw new Error('Invalid ACC task progress')
  }
  return { runId: task.run_id, title: task.title || task.task_id, description: task.description,
    status: progress.status, progress: progress.progress, ...graph }
}

function runPath(runId: string) {
  if (!runId || runId === '.' || runId === '..' || /[/\\]/.test(runId)) throw new Error('Invalid ACC run ID')
  return `/api/v2/orchestration/runs/${encodeURIComponent(runId)}`
}

export async function readUnifiedRun(authority: AccRequestAuthority, runId: string, signal?: AbortSignal) {
  const { run } = await readAcc<{ run?: { run_id: string; status: string } }>(
    authority, runPath(runId), signal,
  ) ?? {}
  if (run?.run_id !== runId || typeof run.status !== 'string' || !run.status) {
    throw new Error('Invalid ACC run identity')
  }
  return { runId, title: runId, status: run.status, ...await readRunGraph(authority, runId, signal) }
}

export async function readRunBilling(authority: AccRequestAuthority, runId: string, signal?: AbortSignal): Promise<RunBilling> {
  const report = await readAcc<RunBilling>(authority, `${runPath(runId)}/billing`, signal)
  if (report?.run_id !== runId || !['pending', 'reconciled', 'partial', 'unobserved'].includes(report.state)
    || !Array.isArray(report.attempts)) throw new Error('Invalid ACC run billing')
  return report
}

export async function readRunCommands(authority: AccRequestAuthority, runId: string, signal: AbortSignal) {
  const result = await readAcc<{ run_id: string; commands: RunCommandReceipt[] }>(
    authority, `${runPath(runId)}/commands`, signal,
  )
  if (result?.run_id !== runId || !Array.isArray(result.commands)) throw new Error('Invalid ACC run commands')
  return result.commands
}

export async function cancelRunCommand(authority: AccRequestAuthority, runId: string, dispatchId: string, signal: AbortSignal) {
  if (!/^dispatch_[a-zA-Z0-9_-]{1,160}$/.test(dispatchId)) throw new Error('Invalid ACC dispatch ID')
  const result = await postAcc<{ run_id: string; command: RunCommandReceipt }>(
    authority, `${runPath(runId)}/commands/${encodeURIComponent(dispatchId)}/cancel`, signal,
  )
  if (result?.run_id !== runId || result.command?.dispatch_id !== dispatchId || !result.command.cancel_state) {
    throw new Error('Invalid ACC cancellation receipt')
  }
  return result.command
}

async function readRunGraph(authority: AccRequestAuthority, runId: string, signal?: AbortSignal) {
  const trajectory = await readAcc<TaskTrajectory>(authority, `${runPath(runId)}/trajectory`, signal)
  const view = trajectory?.view
  if (view?.scope !== 'run' || view.run_id !== runId || !Array.isArray(view.segments)) {
    throw new Error('Invalid ACC run trajectory')
  }
  const nodes = view.segments.find(segment => segment?.kind === 'dispatch_tree')?.items ?? []
  const blockers = view.segments.find(segment => segment?.kind === 'blocking_dependencies')?.items ?? []
  if (!Array.isArray(nodes) || !nodes.every((node): node is TaskGraphNode => {
    const row = node as TaskGraphNode | null
    return Boolean(row && typeof row.task_id === 'string' && row.task_id && typeof row.status === 'string'
      && (row.title === undefined || typeof row.title === 'string')
      && (row.parent_task_id === undefined || typeof row.parent_task_id === 'string'))
  }) || !Array.isArray(blockers) || !blockers.every((entry): entry is TaskEdge => {
    const edge = entry as TaskEdge | null
    return Boolean(edge && typeof edge.from === 'string' && typeof edge.to === 'string')
  })) throw new Error('Invalid ACC task graph')
  return { nodes, blockers }
}

export type { UnifiedTask }
