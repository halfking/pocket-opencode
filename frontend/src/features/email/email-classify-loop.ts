/**
 * 邮件归类的分批循环（零依赖纯逻辑）。
 *
 * ## 为什么把它从 use-email-inbox.ts 里抽出来
 *
 * 原来的循环长这样：
 *
 * ```ts
 * do {
 *   const report = await emailApi.classifyInbox(20, controller.signal)
 *   ...
 *   if (classifyCancel.value || remain <= 0) break
 * } while (!classifyCancel.value)
 * ```
 *
 * 它唯一的自然退出条件是 `remain <= 0`。而服务端
 * `POST /api/emails/classify` 在**逐封分类全部失败**时会返回
 * `{classified: 0, remaining: N}` 且 N 一封没少（`remaining` 统计的是
 * `category IS NULL OR category=''` 的邮件，失败时根本没写库）。
 * 于是这个循环**没有出口**：用户点一次「归类」，前端无限次打这个端点，
 * 每次服务端都要逐封跑分类，一次点击换一场对已经故障的分类器的持续压测，
 * 而且没有任何退避。
 *
 * 「逐封全部失败」不是边角状态，是最普通的两类真实故障：
 *   - 网关调用报错（503 / key 失效 / 限流）
 *   - 网关返回无法解析的内容（模型没按约定格式输出）
 *
 * 服务端侧的复现见
 * `backend/internal/server/server_email_classify_progress_test.go`。
 *
 * 本模块与 `account-lww.ts` 同一个思路：**把判定抽成生产与测试共用的同一份
 * 实现**，而不是让测试去正则刨源码（刨出来的片段带类型注解，`new Function`
 * 会 SyntaxError —— 这正是 `invoice-money.ts` 抽出前的老问题）。
 *
 * ## 退出条件（四条，顺序有讲究）
 *
 * 1. `cancelled`   —— 用户主动中止，最高优先。
 * 2. `drained`     —— `remaining <= 0`，正常跑完。
 * 3. `no-progress` —— **这一批没能让待归类数量变少**。这是修复的核心，两种写法：
 *      a. `classified === 0`。这条**不需要上一轮的数就能判定**，而且是可靠的：
 *         服务端 `classified` 的定义是「`Category != "" && Error == ""` 的条数」
 *         （server_email_classify.go:82-87），它为 0 就意味着一封都没写库，
 *         `remaining` 自然一封没少。第一批也适用。
 *      b. `remaining` 没有比上一轮少。更隐蔽的形态：声称分类了几封，但
 *         `remaining` 纹丝不动（写库没生效 / 统计口径不一致）。从第二轮起才有意义。
 * 4. `batch-cap`   —— 批次数硬上限，纯兜底。命中时**必须**让调用方知道，
 *    不能静默收工（否则又是一个「数字分不清两种情况」的坑）。
 *
 * 注意 (a) 必须排在「拿上一轮比」之前：第一轮没有上一轮可比，用 `Infinity`
 * 起手会让 `remaining >= Infinity` 恒假，于是**第一批的零进展判不出来**——
 * 这是本模块初版真实踩到的坑，对应用例
 * 「逐封全部失败（classified:0、remaining 恒定）时只发一批就停」。
 */

/** 一批的最小形状。真实的 `EmailClassifyReport` 满足它。 */
export interface ClassifyBatch {
  classified: number
  remaining: number
}

export type ClassifyStop = 'drained' | 'cancelled' | 'no-progress' | 'batch-cap'

export interface ClassifyLoopDeps<T extends ClassifyBatch> {
  /** 取一批（通常是 emailApi.classifyInbox）。 */
  fetchBatch: () => Promise<T>
  /** 消费这一批（写库 / 更新列表 / 更新进度文案）。 */
  onBatch: (batch: T, index: number) => void | Promise<void>
  /** 查询是否已被用户中止。 */
  isCancelled: () => boolean
  /** 批次数硬上限。默认 200（即最多 4000 封/次点击）。 */
  maxBatches?: number
}

export interface ClassifyLoopResult {
  /** 实际发出的批次数。 */
  batches: number
  /** 各批 classified 之和。 */
  classified: number
  /** 最后一轮的 remaining；一次都没跑成功时为 0。 */
  remaining: number
  stopped: ClassifyStop
}

export const DEFAULT_MAX_BATCHES = 200

export async function runClassifyLoop<T extends ClassifyBatch>(
  deps: ClassifyLoopDeps<T>,
): Promise<ClassifyLoopResult> {
  const maxBatches = deps.maxBatches && deps.maxBatches > 0 ? deps.maxBatches : DEFAULT_MAX_BATCHES

  let batches = 0
  let classified = 0
  let remaining = 0
  // 上一轮的 remaining；**第一批没有上一轮**，所以下面用 batches > 1 才拿它比。
  let prevRemaining = 0
  let stopped: ClassifyStop = 'drained'

  for (;;) {
    if (deps.isCancelled()) {
      stopped = 'cancelled'
      break
    }

    const batch = await deps.fetchBatch()
    batches += 1
    const got = Number(batch.classified) || 0
    classified += got
    remaining = Number(batch.remaining) || 0
    await deps.onBatch(batch, batches - 1)

    if (deps.isCancelled()) {
      stopped = 'cancelled'
      break
    }
    if (remaining <= 0) {
      stopped = 'drained'
      break
    }
    // (a) 一封都没归类 = 写库一步没发生 = 再打一次不会有不同结果。
    //     这条不需要上一轮的数，所以第一批同样适用。
    if (got <= 0) {
      stopped = 'no-progress'
      break
    }
    // (b) 声称归类了几封，remaining 却没降 —— 更隐蔽的零进展。
    if (batches > 1 && remaining >= prevRemaining) {
      stopped = 'no-progress'
      break
    }
    if (batches >= maxBatches) {
      stopped = 'batch-cap'
      break
    }
    prevRemaining = remaining
  }

  return { batches, classified, remaining, stopped }
}

/**
 * 把终止原因翻成一句用户看得懂、且**能据此行动**的话。
 *
 * 为什么不能四种情况都显示同一句话：这是本仓库反复吃过的一类亏——一个数字
 * 分不清两种情况，于是「功能没生效」和「确实没东西」长得一模一样（见
 * `reminder_diag_test.go` 记录的 RemindersSent 恒为 0 那次）。这里尤其要分清
 * 「跑完了」和「**分类器坏了所以我停了**」——后者不写出来，用户只会以为点了个
 * 寂寞。
 *
 * `leftover` 是本地列表里仍未归类的封数（调用方算好传进来），不是服务端
 * remaining —— 本地可能有刚同步进来、服务端还没算进去的。
 */
export function classifyStopHint(
  loop: Pick<ClassifyLoopResult, 'stopped' | 'classified'>,
  leftover: number,
): string {
  switch (loop.stopped) {
    case 'no-progress':
      // 措辞要说清是「我主动停了」，且给出最可能的原因。
      return leftover
        ? `分类器没有返回任何结果，已停止；仍有 ${leftover} 封未归类（检查 AI 分类是否已配置）`
        : '归类完成'
    case 'batch-cap':
      return leftover
        ? `已达单次归类上限，已停止；仍有 ${leftover} 封未归类，可再点一次继续`
        : '归类完成'
    case 'cancelled':
      return leftover ? `已取消，仍有 ${leftover} 封未归类` : '归类完成'
    case 'drained':
    default:
      return leftover ? `已暂停，仍有 ${leftover} 封未归类` : '归类完成'
  }
}
