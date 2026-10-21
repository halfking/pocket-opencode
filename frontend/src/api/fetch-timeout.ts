/**
 * fetch 的**唯一**有界实现（§951）。
 *
 * ⚠⚠ **为什么单独成文件、且零 import**
 * ① **零 import ⇒ 任何模块都能用它而不会引入循环依赖。**
 *    前端的 import 图不是树：`api/http.ts` → `../stores/auth` → `../api/auth` → `./http`
 *    ⇒ 这条链上再加一个 import 就绕成环（`api/llm-bff.ts` 正是因为这个才自己写了一份
 *    局部 `http`）。而本文件**不依赖任何东西** ⇒ 谁都能安全地 import。
 * ② 它是**唯一**该直接触碰全局 `fetch` 的地方
 *    ⇒ 判据只要盯住这一个文件 + 一份例外台账就行
 *    （§950 的同款口径：「兜底放在唯一的构造口 ⇒ 漏判只可能发生一次」）。
 *
 * ⚠⚠ **它治的是哪一个病**
 * 一个不响应的后端会让 `await fetch(...)` **永久挂起**。那不只是"慢"——
 * 它会击穿依赖状态机的交互：笔记录音 `stop()` 先把 phase 置为 `'stopping'`
 * 再做转写 IO，请求不返回 ⇒ phase 永不归位 ⇒ `decideNoteStart` 拒绝新录音、
 * `toggle()` 返回 null ⇒ 录音按钮就此失灵。
 *
 * ⚠⚠ **`timeoutMs: 0` 是「不设上限」，不是「忘了设」**
 * 这是**登记在册的例外口**（与后端 `outbound.NewStreamingClient` 同构），
 * 只给长轮询 / SSE 这类**天生没有固定长度**的请求用。
 * ⇒ 任何新调用点传 0，都必须在那份台账里写清「为什么它可以没有上界」。
 */

/**
 * 默认请求超时。
 *
 * ⚠ 取 30 s 的理由与 `api/http.ts` 原来的 `DEFAULT_TIMEOUT_MS` 一致（**刻意不改**）：
 * 那 30 s 是被 §929/§930 的预算表钉住的默认档（表里 17 条长路由都必须**大于**它）。
 */
export const DEFAULT_TIMEOUT_MS = 30_000

/** 区分「超时」和「服务端返回 4xx/5xx」，让上层能给出可读提示。 */
export class TimeoutError extends Error {
  /** 出错时用的标识（通常是 API 路径；没给时用 URL）。 */
  readonly path: string
  readonly timeoutMs: number
  constructor(path: string, timeoutMs: number) {
    super(`请求超时（${Math.round(timeoutMs / 1000)}s）：${path}`)
    this.name = 'TimeoutError'
    this.path = path
    this.timeoutMs = timeoutMs
  }
}

export interface FetchTimeoutOptions {
  /** 本次请求的毫秒上限；传 0 表示不设上限（仅限长轮询等已知场景）。 */
  timeoutMs?: number
  /** 出现在超时提示里的标识（默认用 URL）。 */
  label?: string
}

/**
 * 带时间上界的 fetch。
 *
 * ⚠ 与调用方给的 `init.signal` **共存**：两者任意一个触发都会中止请求
 * ⇒ 「超时」与「用户主动取消」都能生效，且**互不覆盖**
 * （超时抛 `TimeoutError`，取消抛 `AbortError` —— 两者的区分见 `api/abort.ts`）。
 */
export async function fetchWithTimeout(
  input: RequestInfo | URL,
  init: RequestInit = {},
  opts: FetchTimeoutOptions = {},
): Promise<Response> {
  const timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS
  const label = opts.label ?? String(input)

  const controller = new AbortController()
  const callerSignal = init.signal
  const onCallerAbort = () => controller.abort()
  if (callerSignal) {
    if (callerSignal.aborted) controller.abort()
    else callerSignal.addEventListener('abort', onCallerAbort, { once: true })
  }
  let timedOut = false
  const timer = timeoutMs > 0
    ? setTimeout(() => { timedOut = true; controller.abort() }, timeoutMs)
    : null

  try {
    return await fetch(input, { ...init, signal: controller.signal })
  } catch (e) {
    // ⚠ 只有**我们自己**掐的才叫超时；调用方取消时原样透传（name === 'AbortError'）。
    if (timedOut) throw new TimeoutError(label, timeoutMs)
    throw e
  } finally {
    if (timer) clearTimeout(timer)
    if (callerSignal) callerSignal.removeEventListener('abort', onCallerAbort)
  }
}
