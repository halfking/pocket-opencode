/**
 * Shared HTTP client with auth token injection.
 * New per-feature api modules (notes.ts, email.ts, vault.ts) build on this
 * instead of calling fetch() directly, so auth headers stay consistent.
 */
import { resolveRuntimeApiBase } from '../config/api-base'
import { useAuthStore } from '../stores/auth'
import { assertNotHTML } from './jsonGuard'
import { isAbortError } from './abort'

// 再导出：client.ts 等既有调用方统一从 ./http 取守卫。
export { assertNotHTML }

export function getApiBase(): string {
  return resolveRuntimeApiBase()
}

/** refresh 端点本身 401 时不得再触发续期重放（防自引用循环）。 */
const REFRESH_PATH = '/api/auth/refresh'

export class ApiError extends Error {
  /** 响应 body 解析后的对象（若有）。让调用方能拿到 409 等结构化错误信息。 */
  body?: any
  status: number
  constructor(status: number, message: string, body?: any) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

/** 发请求前临期主动续期（内部仍单飞）；失败不阻塞本次请求（会再走 401 兜底）。 */
async function maybeRefreshBeforeRequest(path: string): Promise<void> {
  if (path === REFRESH_PATH) return
  try {
    const auth = useAuthStore()
    await auth.maybeRefresh()
  } catch {
    // 忽略：maybeRefresh 内部已吞错
  }
}

/** Wrapper around fetch that injects the Bearer token and parses JSON. */
/**
 * 默认请求超时。
 *
 * 之前所有 fetch 都没有 AbortSignal,一个不响应的后端会让 await 永久挂起。
 * 这不只是"慢",而是会击穿依赖状态机的交互：例如笔记录音 stop() 先把
 * phase 置为 'stopping' 再做转写 IO,请求不返回 → phase 永不归位 →
 * decideNoteStart 拒绝新录音、toggle() 返回 null,录音按钮就此失灵。
 * 切页后台执行的长任务同理会永远占着进行中标记。
 */
const DEFAULT_TIMEOUT_MS = 30_000

/** 少数天然慢的端点(同步/大音频/批量推理)在调用处显式放宽。 */
export const LONG_REQUEST_TIMEOUT_MS = 120_000

export interface HttpOptions extends RequestInit {
  /** 覆盖本次请求的毫秒上限;传 0 表示不设上限(仅限长轮询等已知场景)。 */
  timeoutMs?: number
}

/**
 * 判断一个错误是不是「调用方主动中止」。
 *
 * 权威定义在 ./abort（零依赖，可被护栏直接 import 跑行为断言）；这里再导出，
 * 是为了让既有调用方统一从 ./http 取——错误形态由本文件决定，而
 * httpOnce 里 caller abort 走 `controller.abort()`，`timedOut` 为 false，
 * 于是 fetch 抛出的那个 AbortError 被**原样透传**（第 100-102 行下方）；
 * 只有超时才被换成 TimeoutError。所以：
 *
 *   - `name === 'AbortError'` → 用户/调用方主动中止，**不是失败**
 *   - `TimeoutError`（name = 'TimeoutError'）→ 失败，走降级逻辑
 */
export { isAbortError }

/** 区分「超时」和「服务端返回 4xx/5xx」,让上层能给出可读提示。 */
export class TimeoutError extends Error {
  readonly path: string
  readonly timeoutMs: number
  constructor(path: string, timeoutMs: number) {
    super(`请求超时（${Math.round(timeoutMs / 1000)}s）：${path}`)
    this.name = 'TimeoutError'
    this.path = path
    this.timeoutMs = timeoutMs
  }
}

async function httpOnce<T = any>(path: string, opts: HttpOptions = {}): Promise<T> {
  const auth = useAuthStore()
  const { timeoutMs = DEFAULT_TIMEOUT_MS, signal: callerSignal, ...init } = opts
  const headers: Record<string, string> = {
    ...(init.headers as Record<string, string> | undefined),
  }
  if (auth.token) headers['Authorization'] = `Bearer ${auth.token}`
  if (init.body && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json'
  }

  const controller = new AbortController()
  const onCallerAbort = () => controller.abort()
  if (callerSignal) {
    if (callerSignal.aborted) controller.abort()
    else callerSignal.addEventListener('abort', onCallerAbort, { once: true })
  }
  let timedOut = false
  const timer = timeoutMs > 0
    ? setTimeout(() => { timedOut = true; controller.abort() }, timeoutMs)
    : null

  let res: Response
  try {
    res = await fetch(`${resolveRuntimeApiBase()}${path}`, { ...init, headers, signal: controller.signal })
  } catch (e) {
    if (timedOut) throw new TimeoutError(path, timeoutMs)
    throw e
  } finally {
    if (timer) clearTimeout(timer)
    if (callerSignal) callerSignal.removeEventListener('abort', onCallerAbort)
  }
  if (!res.ok) {
    // 尝试解析响应 body，让调用方能拿到结构化错误（如 409 conflict 的 server_version）
    let parsedBody: any
    try {
      const text = await res.text()
      parsedBody = text ? JSON.parse(text) : undefined
    } catch {
      parsedBody = undefined
    }
    // 服务端结构化错误统一为 {error: "..."}：直接作为 message，
    // 让各视图既有 catch 的 e.message 展示可读原因而非 "Request failed: Internal Server Error"
    const serverMsg =
      typeof parsedBody?.error === 'string' && parsedBody.error.trim()
        ? parsedBody.error.trim()
        : ''
    throw new ApiError(
      res.status,
      serverMsg || `Request failed: ${res.statusText}`,
      parsedBody,
    )
  }
  // 204 No Content
  if (res.status === 204) return undefined as unknown as T
  return assertNotHTML(res).json() as Promise<T>
}

/**
 * http = httpOnce + JWT 滑动续期（runbook §15.2）：
 *  1. 请求前 token 临期（<5min）主动单飞续期；
 *  2. 收到 401 时单飞 refresh 一次并用新 token 重放；refresh 失败才让
 *     401 透传（调用方维持原有错误处理；登出仍由各视图自行决定）。
 * refresh 端点自身与未登录（无 token）请求不参与续期。
 */
export async function http<T = any>(path: string, opts: HttpOptions = {}): Promise<T> {
  await maybeRefreshBeforeRequest(path)
  try {
    return await httpOnce<T>(path, opts)
  } catch (e) {
    if (e instanceof ApiError && e.status === 401 && path !== REFRESH_PATH) {
      const auth = useAuthStore()
      if (auth.token && (await auth.refreshSession())) {
        return httpOnce<T>(path, opts)
      }
      // BUG-I (2026-09-30)：refresh 失败 = 会话不可恢复（token 已被吊销、
      // 过期且无法续期，或后端 POCKET_JWT_SECRET 变更导致旧 token 全部失效）。
      // 此前这里只是把 401 原样抛给调用方，而 auth.ts 注释里写的
      // 「由调用方决定是否登出」没有任何调用方真的实现，结果用户拿着死 token
      // 卡在各个模块里只看到 "invalid or expired token / 重试"，
      // **没有任何回到登录页的路径**（市场模块实测就是这个死胡同）。
      // 这里补上唯一的兜底：清登录态 + 跳回登录页。
      forceReauth()
    }
    throw e
  }
}

/**
 * 不可恢复 401 的兜底清理：单飞，避免并发请求重复跳转。
 *
 * 导出给 api/client.ts 的 authFetch 复用（BUG-AX，2026-10-01 13:05 真机实测）。
 * 之前它只在 http() 这条链上生效，而 `api/client.ts` 整个面（getTasks /
 * getTask / createTask / …，任务、会话、实例等模块都在用）走的是 authFetch，
 * **完全绕过了这个兜底**。实测后果：
 *   · 后端换 JWT secret 之后，设备上的旧 token 全部 401
 *   · 任务页 catch 住错误、把 tasks 置空，页面显示「暂无运行中的任务」
 *     和分诊条的「全部正常 · 0」，**既不报错也不跳登录**
 *   · 用户拿着一枚死 token 卡在各个模块里，和 BUG-I 当初描述的死法一模一样
 * 也就是说 BUG-I 的修复只打了一半：兜底存在，但没覆盖真正被大量调用的那条路。
 */
let reauthInFlight = false
export function forceReauth(): void {
  if (reauthInFlight) return
  // 已在登录页/登录相关路径上就不再跳，避免重定向循环
  const hash = (typeof location !== 'undefined' ? location.hash : '') || ''
  if (/#\/login|\/forgot-password|\/register/.test(hash)) return
  reauthInFlight = true
  try {
    const auth = useAuthStore()
    // 不调 logout()：它会打 /api/auth/logout，而此刻后端必然拒绝，
    // 只会拖慢跳转。这里直接清本地态。
    auth.clearLocal()
  } catch {
    /* 清理失败也要继续跳转，否则用户彻底卡死 */
  }
  try {
    if (typeof location !== 'undefined') {
      location.hash = '#/login?reason=expired'
    }
  } finally {
    // 下一轮导航后允许再次兜底
    setTimeout(() => { reauthInFlight = false }, 1500)
  }
}

