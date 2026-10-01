/**
 * STT 设置页的纯展示辅助（抽出供 node --test 直接加载）。
 *
 * 抽出理由与本仓既有约定一致：仓库没有 vitest / @vue/test-utils / jsdom，
 * 组件级挂载测试不是现成手段，所以「值得测的逻辑」必须放进无 Vue 依赖的
 * 纯函数里，由 node --test 覆盖。
 */

/**
 * 格式化「美元/小时」单价。
 *
 * 为什么不能用 toFixed(2)：2026-10-01 调研出的最便宜档是
 * $0.012/小时（OpenRouter qwen3-asr-0.6b），toFixed(2) 会显示成
 * **$0.01**——把 0.6B 和 whisper-turbo 这类 1 分钱档显示成同一个价格，
 * 用户无从比较，而「尽可能费用少」正是本项目为这个字段存在的理由。
 *
 * 规则（按量级自适应有效位数）：
 *  - ≥ 0.1：2 位小数（$0.38/小时、$0.50/小时 这类主流价格）
 *  - ≥ 0.01：3 位小数（$0.012/小时）
 *  - < 0.01：4 位小数（$0.0086/小时）
 *
 * 0 视为「无公开报价」，由调用方另行处理（返回空串），不显示成 $0.00——
 * 显示 $0.00 会被读成「免费」，那比显示「未知」危险得多。
 */
export function formatCost(usdPerHour: number): string {
  if (!Number.isFinite(usdPerHour) || usdPerHour <= 0) return ''
  const abs = Math.abs(usdPerHour)
  const digits = abs >= 0.1 ? 2 : abs >= 0.01 ? 3 : 4
  return `$${usdPerHour.toFixed(digits)}/小时`
}

/**
 * 一条推荐模型的「即时出字能力」说明。
 *
 * 这是本轮调研最需要向用户传达的取舍：便宜的 ASR 普遍**不支持服务端真流式**
 * （OpenRouter 转写端点上游约 60 秒超时），所以「省钱」与「逐字出字」需要二选一。
 * 把这个取舍显式摆在设置页，而不是让用户选完模型后才发现「怎么不是逐字蹦出来」。
 */
export function streamingHint(m: { streaming?: boolean; group?: string }): string {
  if (m.streaming) return '支持即时流式出字'
  if (m.group === 'external') return '分段出字（不支持逐字流式）'
  return ''
}

/**
 * 单次时长上限的人话说明。
 *
 * 关键信息是「超过会自动切段」——否则用户录了一小时会议，看到「限 30 秒」
 * 只会以为功能用不了，而实际上后端已经内置切分。
 */
export function maxSecondsHint(maxSeconds?: number): string {
  if (!maxSeconds || maxSeconds <= 0) return ''
  return `单次上限 ${maxSeconds} 秒，超过会自动切段转写`
}
