/**
 * Shared HTTP client with auth token injection.
 * New per-feature api modules (notes.ts, email.ts, vault.ts) build on this
 * instead of calling fetch() directly, so auth headers stay consistent.
 */
import { resolveRuntimeApiBase } from '../config/api-base'
import { useAuthStore, captureAuthSession, captureAuthAttempt, assertAuthAttempt, assertAuthSession, isCurrentAuthSession, type AuthSession } from '../stores/auth'
import { assertNotHTML } from './jsonGuard'
import { isAbortError } from './abort'
import { DEFAULT_TIMEOUT_MS, TimeoutError, fetchWithTimeout } from './fetch-timeout.ts'
import { blockedModuleWrite } from './module-policy.ts'
import { isEmailMetadataWrite } from './email-upload-policy.ts'
import { readCachedModuleMode, recordSyncReportEntry } from '../stores/syncPolicyStore.ts'
import type { SyncModule } from './syncPolicyContract.ts'
import { usesPocketResourceCredentials, pocketResourceScope, pocketResourceCredential, PocketResourceAuthorityError,
  PocketResourceUnavailableError, type PocketResourceCredential } from './pocket-resource'

// 再导出：client.ts 等既有调用方统一从 ./http 取守卫。
export { assertNotHTML }

// ★ §951：超时原语搬到 ./fetch-timeout（零 import ⇒ 谁都能安全 import，不会绕成环）。
// ⚠ 这里**再导出**而不是各自定义：TimeoutError 是靠 `instanceof` 认的
//　 （features/email/use-email-inbox.ts 就是这么判的）⇒ 必须是**同一个类对象**。
export { DEFAULT_TIMEOUT_MS, TimeoutError }

export function getApiBase(): string {
  return resolveRuntimeApiBase()
}

/** refresh 端点本身 401 时不得再触发续期重放（防自引用循环）。 */
const REFRESH_PATH = '/api/auth/refresh'
const PUBLIC_AUTH_PATHS = new Set(['/api/auth/login', '/api/auth/register', '/api/auth/send-code',
  '/api/auth/code-login', '/api/auth/forgot-password', '/api/auth/sso/status', '/api/auth/sso/login', '/api/auth/sso/exchange'])

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
 *
 * ⚠§951：这条注释连同**实现**一起搬到了 `./fetch-timeout`（那里是前端唯一
 * 触碰全局 fetch 的地方）⇒ 本文件只剩「注入 token + 解析 JSON」这一层。
 */

/** 少数天然慢的端点(同步/大音频/批量推理)在调用处显式放宽。 */
export const LONG_REQUEST_TIMEOUT_MS = 120_000

/**
 * `/api/llm/chat` 的**客户端**超时（§929）。
 *
 * ⚠⚠ **为什么必须显式给、而且必须比 120000 还大**：
 * 服务端那条路的**有效预算不是 handler 的 `WithTimeout`**，而是网关客户端的
 * `Transport.ResponseHeaderTimeout`：
 *
 *	backend/internal/llmgateway/client.go:116
 *	  Timeout:               90 * time.Second   ← 整体上限
 *	  ResponseHeaderTimeout: 85 * time.Second   ← **实际可用预算**
 *
 * ⇒ 客户端计时从请求发出开始，服务端的从 handler 进来开始，中间还隔着网络与鉴权
 * ⇒ ⇒ **必须 > 85 s**，否则「服务端还在算、客户端先断开」。
 *
 * ⛔ 2026-10-03 的普查其实已经查出「`/api/meetings/{id}/refine 客户端 30s 服务端 90s」
 * 这条（见 `api-timeout-budget-table.test.mjs` 的头注释）⇒ 但那张表当时只列了
 * **直接调用**那条路，**兜底路径**（浏览器侧打 `/api/llm/chat`）**不在表里**
 * ⇒ ⇒ 没人守它 ⇒ 四个调用点一直吃 `http()` 的默认 **30 s**
 * ⇒ ⇒ **比服务端的有效预算短 55 s** ⇒ 兜底路径在生产上基本是「必然超时」的
 * （§54 实测：网关 auto 路由到 deepseek-v4-flash 时**首字节就要 24~142 s**）。
 *
 * ⚠ 取 120 s 而不是 85 s：留 35 s 给「服务端已经开工、客户端还没收到响应头」的
 *   那段（网络 + 鉴权 + 上游排队）。⚠ 这不是保证成功（§101：同一份提示词
 *   28.8 s / 84.4 s 两次都发生过，**预算落在方差中间**）⇒ 它只让
 *   **本来能在服务端预算内跑完**的请求有机会跑完。
 */
export const LLM_CHAT_TIMEOUT_MS = 120_000

export interface HttpOptions extends RequestInit {
  /** Retain the initiating business operation across refresh, parsing and replay. */
  assertCurrent?: () => void
  /** 共享运算端点的业务归属，续期与重放后、实际发送前再次校验。 */
  syncModule?: SyncModule
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

function assertOperationCallback(callback?: () => void): void {
  const result: unknown = callback?.()
  if (result !== undefined) {
    // A promise cannot provide a synchronous admission check. Consume an
    // accidental rejection while refusing to dispatch the request.
    void Promise.resolve(result).catch(() => {})
    throw new TypeError('assertCurrent must be synchronous and return undefined')
  }
}

/** 区分「超时」和「服务端返回 4xx/5xx」,让上层能给出可读提示。 */
// ⚠§951：TimeoutError 的**类定义**在 ./fetch-timeout（上面已再导出）。
//　 留这一行是为了让「这个文件里 TimeoutError 从哪来」有据可查。

async function httpOnce<T = any>(path: string, opts: HttpOptions, session: AuthSession, anonymous = false, resource?: PocketResourceCredential): Promise<T> {
  const auth = session.owner
  const checkSession = anonymous ? assertAuthAttempt : assertAuthSession
  const checkOperation = () => {
    checkSession(auth, session)
    assertOperationCallback(opts.assertCurrent)
    resource?.assertCurrent()
    checkSession(auth, session)
  }
  checkOperation()
  if (opts.body != null && !isEmailMetadataWrite(path, opts.body)) {
    throw new ApiError(409, 'email_credentials_local_only', { error: 'email_credentials_local_only' })
  }
  const blocked = blockedModuleWrite(path, opts.method, readCachedModuleMode)
  if (blocked) {
    recordSyncReportEntry({ module: blocked, status: 'skipped' })
    throw new ApiError(409, 'module_policy_local', { error: 'module_policy_local', module: blocked })
  }
  const { timeoutMs = DEFAULT_TIMEOUT_MS, signal: callerSignal, syncModule, assertCurrent, ...init } = opts
  const headers: Record<string, string> = {
    ...(init.headers as Record<string, string> | undefined),
  }
  if (anonymous) {
    for (const key of Object.keys(headers)) if (key.toLowerCase() === 'authorization') delete headers[key]
  } else if (resource) {
    for (const key of Object.keys(headers)) if (key.toLowerCase() === 'authorization') delete headers[key]
    headers.Authorization = `Bearer ${resource.token}`
  } else if (auth.token) headers['Authorization'] = `Bearer ${auth.token}`
  if (init.body && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json'
  }

  // ⚠§951：上界与中止都交给唯一的那一层（./fetch-timeout）。
  //　 · 超时 → TimeoutError（label 用 API 路径 ⇒ 提示文案与旧实现逐字一致）
  //　 · 调用方取消 → AbortError 原样透传（`name === 'AbortError'` ⇒ 上层当「不是失败」）
  // ⇒ 这里**不再**二次包装：fetch-timeout 抛的就是同一个类、同一组字段。
  checkOperation()
  const blockedBeforeFetch = blockedModuleWrite(path, opts.method, readCachedModuleMode)
  if (blockedBeforeFetch) {
    recordSyncReportEntry({ module: blockedBeforeFetch, status: 'skipped' })
    throw new ApiError(409, 'module_policy_local', { error: 'module_policy_local', module: blockedBeforeFetch })
  }
  if (syncModule && readCachedModuleMode(syncModule) === 'local') {
    recordSyncReportEntry({ module: syncModule, status: 'skipped' })
    throw new ApiError(409, 'module_policy_local', { error: 'module_policy_local', module: syncModule })
  }
  const res = await fetchWithTimeout(
    `${resource?.base ?? resolveRuntimeApiBase()}${path}`,
    { ...init, headers, signal: callerSignal, ...(resource ? { redirect: 'error' as const, credentials: 'omit' as const } : {}) },
    { timeoutMs, label: path },
  )
  checkOperation()
  if (!res.ok) {
    // 尝试解析响应 body，让调用方能拿到结构化错误（如 409 conflict 的 server_version）
    let parsedBody: any
    try {
      const text = await res.text()
      parsedBody = text ? JSON.parse(text) : undefined
    } catch {
      parsedBody = undefined
    }
    checkOperation()
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
  const data = await assertNotHTML(res).json() as T
  checkOperation()
  return data
}

/**
 * http = httpOnce + JWT 滑动续期（runbook §15.2）：
 *  1. 请求前 token 临期（<5min）主动单飞续期；
 *  2. 收到 401 时单飞 refresh 一次并用新 token 重放；refresh 失败才让
 *     401 透传（调用方维持原有错误处理；登出仍由各视图自行决定）。
 * refresh 端点自身与未登录（无 token）请求不参与续期。
 */
export async function http<T = any>(path: string, opts: HttpOptions = {}): Promise<T> {
  const auth = useAuthStore()
  if (PUBLIC_AUTH_PATHS.has(path.split('?')[0])) {
    return httpOnce<T>(path, opts, captureAuthAttempt(auth), true)
  }
  const session = captureAuthSession(auth)
  const checkOperation = () => {
    assertAuthSession(auth, session)
    assertOperationCallback(opts.assertCurrent)
    assertAuthSession(auth, session)
  }
  checkOperation()
  const resourceScope = pocketResourceScope(path, opts.method)
  if (usesPocketResourceCredentials(auth) && resourceScope !== null) {
    if (resourceScope === 'unavailable') throw new PocketResourceUnavailableError()
    const checkResourceOperation = () => {
      checkOperation()
      const blocked = blockedModuleWrite(path, opts.method, readCachedModuleMode)
        ?? (opts.syncModule && readCachedModuleMode(opts.syncModule) === 'local' ? opts.syncModule : null)
      if (blocked) {
        recordSyncReportEntry({ module: blocked, status: 'skipped' })
        throw new ApiError(409, 'module_policy_local', { error: 'module_policy_local', module: blocked })
      }
    }
    checkResourceOperation()
    let resource: PocketResourceCredential | undefined
    try {
      resource = await pocketResourceCredential(auth, session, resourceScope, checkResourceOperation)
      checkResourceOperation()
      try { return await httpOnce<T>(path, opts, session, false, resource) }
      catch (error) {
        checkOperation()
        if (!(error instanceof ApiError) || error.status !== 401) throw error
        resource.invalidate()
        resource = await pocketResourceCredential(auth, session, resourceScope, checkResourceOperation)
        checkResourceOperation()
        return await httpOnce<T>(path, opts, session, false, resource)
      }
    } catch (error) {
      checkOperation()
      if (error instanceof PocketResourceAuthorityError && error.status === 401 && error.code === 'parent_session_invalid') await forceReauth(session)
      throw error
    }
  }
  await maybeRefreshBeforeRequest(path)
  checkOperation()
  try {
    return await httpOnce<T>(path, opts, session)
  } catch (e) {
    checkOperation()
    if (e instanceof ApiError && e.status === 401 && path !== REFRESH_PATH) {
      if (auth.token && (await auth.refreshSession())) {
        checkOperation()
        return httpOnce<T>(path, opts, session)
      }
      checkOperation()
      // BUG-I (2026-09-30)：refresh 失败 = 会话不可恢复（token 已被吊销、
      // 过期且无法续期，或后端 POCKET_JWT_SECRET 变更导致旧 token 全部失效）。
      // 此前这里只是把 401 原样抛给调用方，而 auth.ts 注释里写的
      // 「由调用方决定是否登出」没有任何调用方真的实现，结果用户拿着死 token
      // 卡在各个模块里只看到 "invalid or expired token / 重试"，
      // **没有任何回到登录页的路径**（市场模块实测就是这个死胡同）。
      // 这里补上唯一的兜底：清登录态 + 跳回登录页。
      await forceReauth(session)
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
const reauthSessions = new WeakSet<object>()
export async function forceReauth(expectedSession?: AuthSession): Promise<void> {
  const auth = expectedSession?.owner ?? useAuthStore()
  const session = expectedSession ?? captureAuthSession(auth)
  if (!isCurrentAuthSession(auth, session) || reauthSessions.has(session.generation)) return
  // 已在登录页/登录相关路径上就不再跳，避免重定向循环
  const hash = (typeof location !== 'undefined' ? location.hash : '') || ''
  if (/#\/login|\/forgot-password|\/register/.test(hash)) return
  reauthSessions.add(session.generation)
  try {
    // 不调 logout()：它会打 /api/auth/logout，而此刻后端必然拒绝，
    // 只会拖慢跳转。这里直接清本地态。
    await auth.clearLocal()
  } catch (error) {
    console.warn('[auth] 本地会话清理失败，请重试：', error)
    return
  }
  if (auth.activationStatus !== 'ready' || auth.token) return
  if (typeof location !== 'undefined') {
    location.hash = '#/login?reason=expired'
  }
}

