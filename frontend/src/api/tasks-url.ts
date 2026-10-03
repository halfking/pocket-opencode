/**
 * `/api/tasks` 的 URL 构造（BUG-J 回归锁，2026-09-30 真机验收）。
 *
 * 单独成模块而不是内联在 client.ts 里，是为了能被 `node --test` 直接加载：
 * client.ts 依赖 pinia store 与无扩展名的相对 import，Node 的 ESM 解析器跑不起来。
 *
 * 历史缺陷（`getTasks` 原内联实现）：先把绝对地址过一遍
 *   `new URL(base + '/api/tasks', origin).toString().replace(origin, '')`
 * 想把同源绝对地址降成相对路径。`CAP_ANDROID_SCHEME=http` 时页面 origin 是
 * 无端口的 `http://localhost`，而 API base 是带端口的 `http://localhost:8088`，
 * replace 命中前缀后得到畸形串 `8088/api/tasks`；fetch 再按相对路径解析成
 * `http://localhost/8088/api/tasks`，命中 Capacitor 本地壳返回 index.html，
 * 触发 `assertNotHTML` 的「API 返回了 HTML 页面而非 JSON」。
 * 该写法在 `androidScheme=https`（origin 为 `https://localhost`）时字符串不匹配、
 * 侥幸不触发，属于 BUG-F 引入 http 逃生舱后才暴露的回归。
 *
 * 约定：base 非空 → 绝对地址；base 为空 → 天然就是同源相对路径，不需要任何 replace。
 */
export interface TaskListFilters {
  workstreamId?: string
  source?: 'acc' | 'opencode' | 'local'
}

export function buildTasksUrl(base: string, instanceId?: string, opts: TaskListFilters = {}): string {
  const params = new URLSearchParams()
  if (instanceId) params.set('instance_id', instanceId)
  if (opts.workstreamId) params.set('workstream_id', opts.workstreamId)
  if (opts.source) params.set('source', opts.source)
  const qs = params.toString()
  return `${base}/api/tasks${qs ? `?${qs}` : ''}`
}
