/**
 * ACC Tasks API — 把任务委托给 ACC（Agent Control Center）。
 *
 * 后端通道（pocketd，已就绪）：/api/tasks/delegate
 *   POST /api/tasks/delegate   → 把当前任务委托/转发给 ACC 处理
 *
 * 鉴权复用 http.ts（自动注入 Bearer token + 统一 JSON 错误处理）。
 *
 * 这里我们不假设后端的具体字段命名，按 raw 透传，调用方按需提取 id / 状态。
 */
import { http } from './http'

export interface AccTask {
  id: string
  title: string
  description?: string
  kind?: string
  status?: string
  createdAt?: string
  updatedAt?: string
  [key: string]: any
}

/**
 * 创建一个 ACC 任务（委托给 ACC）。
 *
 * @param input.title       任务标题（必填）
 * @param input.description 任务描述（可选）
 * @param input.kind        任务类型（可选，例：'feature' / 'bug' / 'docs'）
 * @returns                { source: 'acc', raw } — raw 为后端完整响应，方便上层兜底
 */
export function createAccTask(input: {
  title: string
  description?: string
  kind?: string
}): Promise<{ source: 'acc'; raw: any }> {
  return http<{ source: 'acc'; raw: any }>('/api/tasks/delegate', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}