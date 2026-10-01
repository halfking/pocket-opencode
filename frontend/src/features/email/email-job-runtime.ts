/**
 * 邮件长作业的**进程级**状态（归类 / 发票整理）。
 *
 * 为什么不能放在 composable 的局部 ref 里：
 *
 * `useEmailInbox()` / `useInvoiceList()` 都是普通 composable，每组件实例一份。
 * 视图 `onUnmounted` 之后，那些 ref 连同组件一起没了，但**在途的 fetch 不会
 * 跟着停**——它是一个还挂在事件循环里的 promise。于是 2026-10-03 审计到的
 * 后果链是：
 *
 *   1. 用户点「自动归纳整理」，作业在后台正常跑（最长 20 轮 × 20 封）；
 *   2. 用户切到别的页面 → 组件卸载，进度文案与「取消」按钮一起消失；
 *   3. 用户切回来 → **新的 composable 实例**，`classifying=false`、
 *      `syncing=false`，界面上像什么都没发生过；
 *   4. 用户再点一次 → 起**第二个并发作业**。后端 `emailPipelineMu` 会让第二
 *      个排队，于是这一轮在前端看起来就是「转圈不动」。
 *
 * 这与需求「确认可以在切换页面后仍能执行」+「后台执行的 api 可以强行终止」
 * 两条都冲突：作业确实在跑，但用户既看不见也停不掉。
 *
 * 修法沿用 `recordingRuntime` / `aiStreamRuntime` 已有的进程级单例模式
 * （挂 globalThis，防 HMR 重复实例）：状态与中止器都活着，视图只是它的投影。
 *
 * 作用域上的取舍：**只活在进程内**。不落 localStorage、不跨应用重启恢复——
 * 归类进度是可以重做的作业（服务端幂等，中断后重跑不会写坏数据），
 * 为它引入持久化会带来「重启后卡在 running=true」的新故障，不划算。
 */
import { ref, type Ref } from 'vue'

export interface ClassifyJobState {
  /** 归类循环是否在跑（切页后仍为 true，回来能看到）。 */
  running: Ref<boolean>
  /** 进度/结果文案（'正在归类 20/120'、'已取消，仍有 N 封未归类'…）。 */
  hint: Ref<string>
  /** 用户是否已请求中止；供批间循环读，决定要不要再发一轮。 */
  cancelRequested: Ref<boolean>
  /** 在途 HTTP 的中止器；null = 当前没有在途请求。 */
  controller: AbortController | null
}

export interface PipelineJobState {
  running: Ref<boolean>
  controller: AbortController | null
}

export interface EmailJobStates {
  classify: ClassifyJobState
  pipeline: PipelineJobState
}

const KEY = '__openpocket_emailJobs__'
type GlobalWithJobs = typeof globalThis & { [KEY]?: EmailJobStates }

function createStates(): EmailJobStates {
  return {
    classify: {
      running: ref(false),
      hint: ref(''),
      cancelRequested: ref(false),
      controller: null,
    },
    pipeline: { running: ref(false), controller: null },
  }
}

export const emailJobs: EmailJobStates =
  (globalThis as GlobalWithJobs)[KEY] ?? ((globalThis as GlobalWithJobs)[KEY] = createStates())

/**
 * 开始一轮归类，返回本轮的中止器。
 *
 * 返回前先中止**上一个残留的中止器**：正常路径下上一个会在 finally 里清空，
 * 这里兜的是「上轮异常抛出、finally 之前就断了」这种不该发生的路径。
 */
export function startClassifyRun(): AbortController {
  emailJobs.classify.controller?.abort()
  const c = new AbortController()
  emailJobs.classify.controller = c
  emailJobs.classify.cancelRequested.value = false
  emailJobs.classify.running.value = true
  return c
}

/** 归类结束（无论成功/失败/取消）。只清 running，不动 hint——那是要显示的。 */
export function finishClassifyRun(): void {
  emailJobs.classify.controller = null
  emailJobs.classify.running.value = false
}

/**
 * 强行终止归类：置中止标记 **并** abort 在途请求。
 *
 * 只置标记是不够的——分类器逐封调 LLM，单批可达分钟级，用户会看到
 * 「点了取消没反应」。abort 之后 HTTP 层真的会断，而服务端 handler 是
 * `context.WithTimeout(r.Context(), …)` 派生的，连接断开会让它带着错误一起
 * 退出，所以这个 abort 是**真终止**，不是前端单方面撒手。
 *
 * @returns 是否确实有一个在途请求被中止（false = 刚好批间空隙，仅置了标记）
 */
export function cancelClassifyRun(): boolean {
  emailJobs.classify.cancelRequested.value = true
  const c = emailJobs.classify.controller
  if (!c || c.signal.aborted) return false
  c.abort()
  return true
}

export function startPipelineRun(): AbortController {
  emailJobs.pipeline.controller?.abort()
  const c = new AbortController()
  emailJobs.pipeline.controller = c
  emailJobs.pipeline.running.value = true
  return c
}

export function finishPipelineRun(): void {
  emailJobs.pipeline.controller = null
  emailJobs.pipeline.running.value = false
}

/** 强行终止发票整理。语义同 {@link cancelClassifyRun}。 */
export function cancelPipelineRun(): boolean {
  const c = emailJobs.pipeline.controller
  if (!c || c.signal.aborted) return false
  c.abort()
  return true
}
