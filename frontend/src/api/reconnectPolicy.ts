/**
 * reconnectPolicy — WS 重连退避纯策略(2026-09-20 通知体系 P2)。
 *
 * 3s 起、×1.8、上限 30s、±20% 抖动、成功后归零(归零由调用方管理)。
 * 拆成独立无依赖模块以便 node --test 直接覆盖(api/websocket.ts 的
 * import 链含浏览器全局,Node 下不可加载)。
 */
export const RECONNECT_BASE_MS = 3000
export const RECONNECT_FACTOR = 1.8
export const RECONNECT_MAX_MS = 30000
export const RECONNECT_JITTER = 0.2

export function nextReconnectDelay(attempt: number, rand: () => number = Math.random): number {
  const a = Math.max(0, attempt)
  let d = RECONNECT_BASE_MS * Math.pow(RECONNECT_FACTOR, a)
  if (d > RECONNECT_MAX_MS) d = RECONNECT_MAX_MS
  // 抖动后再钳一次:30s 是绝对上限(最大等待时间的用户可预期性)。
  const jitter = 1 + (rand() * 2 - 1) * RECONNECT_JITTER
  const out = Math.round(d * jitter)
  return Math.min(Math.max(out, 0), RECONNECT_MAX_MS)
}
