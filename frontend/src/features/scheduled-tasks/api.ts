import { http } from '../../api/http'
import type {
  SchedulePreview,
  ScheduledTask,
  ScheduledTaskInput,
  ScheduledTaskRun,
} from './types'

const base = '/api/scheduled-tasks'

function unwrapTasks(body: unknown): ScheduledTask[] {
  if (Array.isArray(body)) return body as ScheduledTask[]
  if (body && typeof body === 'object' && Array.isArray((body as { tasks?: unknown }).tasks)) {
    return (body as { tasks: ScheduledTask[] }).tasks
  }
  return []
}

function unwrapRuns(body: unknown): ScheduledTaskRun[] {
  if (Array.isArray(body)) return body as ScheduledTaskRun[]
  if (body && typeof body === 'object' && Array.isArray((body as { runs?: unknown }).runs)) {
    return (body as { runs: ScheduledTaskRun[] }).runs
  }
  return []
}

export const scheduledTasksApi = {
  // 增量同步信封（docs/2026-09-09-list-sync-rules.md §3.2）：
  // since（秒）只回传变更行；deletedIds 为其他端删除的墓碑。
  async list(enabledOnly = false, since?: number): Promise<{ tasks: ScheduledTask[]; deletedIds?: string[]; serverTimeMs?: number }> {
    const params = new URLSearchParams()
    if (enabledOnly) params.set('enabled', 'true')
    if (since && since > 0) params.set('since', String(since))
    const query = params.toString()
    const body = await http<unknown>(`${base}${query ? `?${query}` : ''}`)
    if (body && typeof body === 'object' && !Array.isArray(body)) {
      const env = body as { tasks?: unknown; deletedIds?: unknown; serverTimeMs?: unknown }
      return {
        tasks: unwrapTasks(env),
        deletedIds: Array.isArray(env.deletedIds) ? (env.deletedIds as string[]) : [],
        serverTimeMs: typeof env.serverTimeMs === 'number' ? env.serverTimeMs : undefined,
      }
    }
    return { tasks: unwrapTasks(body) }
  },
  async get(id: string): Promise<ScheduledTask> {
    return http<ScheduledTask>(`${base}/${encodeURIComponent(id)}`)
  },
  async create(input: ScheduledTaskInput): Promise<ScheduledTask> {
    return http<ScheduledTask>(base, { method: 'POST', body: JSON.stringify(input) })
  },
  async update(id: string, input: Partial<ScheduledTaskInput>): Promise<ScheduledTask> {
    // The v1 server validates a complete TaskInput before merging its patch.
    // Fill omitted fields from the current resource so small actions (toggle)
    // remain compatible with that contract.
    const current = await this.get(id)
    const body: ScheduledTaskInput = {
      name: input.name ?? current.name,
      description: input.description ?? current.description ?? '',
      kind: input.kind ?? current.kind,
      scheduleKind: input.scheduleKind ?? current.scheduleKind,
      scheduleExpr: input.scheduleExpr ?? current.scheduleExpr,
      timezone: input.timezone ?? current.timezone,
      payload: input.payload ?? current.payload,
      enabled: input.enabled ?? current.enabled,
      maxRuns: input.maxRuns ?? current.maxRuns,
      cooldownSec: input.cooldownSec ?? current.cooldownSec,
      timeoutSec: input.timeoutSec ?? current.timeoutSec,
    }
    return http<ScheduledTask>(`${base}/${encodeURIComponent(id)}`, {
      method: 'PATCH', body: JSON.stringify(body),
    })
  },
  async run(id: string): Promise<{ triggered: boolean; taskId: string }> {
    return http<{ triggered: boolean; taskId: string }>(`${base}/${encodeURIComponent(id)}/run`, { method: 'POST' })
  },
  async remove(id: string): Promise<void> {
    await http<void>(`${base}/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },
  async runs(id: string, limit = 20): Promise<ScheduledTaskRun[]> {
    const body = await http<unknown>(`${base}/${encodeURIComponent(id)}/runs?limit=${limit}`)
    return unwrapRuns(body)
  },
  async preview(input: Pick<ScheduledTaskInput, 'scheduleKind' | 'scheduleExpr' | 'timezone'>): Promise<SchedulePreview> {
    return http<SchedulePreview>(`${base}/preview`, { method: 'POST', body: JSON.stringify(input) })
  },
}
