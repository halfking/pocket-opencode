/**
 * chunkByLimit —— 按固定上限把数组切成若干批。
 *
 * ⚠ §922 的来历：服务端 `handleEmailPurge` **一次最多接受 100 个 ID**
 *   （`len(body.IDs) > 100` ⇒ 超了直接 400「too many ids」），
 *   而前端旧实现**一次全发** ⇒ 用户选中超过 100 封时服务端 400 被调用方
 *   空 catch 吞掉 ⇒ **本地已清、服务端没清**（正文其实没清掉），
 *   界面却毫无提示 ⇒ 前端必须按**同一个上限**分批。
 *
 * ⚠ 为什么单独成文件（而不是内联在 `api/email.ts` 里）：
 *   `api/email.ts` 的 import 图会拖进 pinia/vue ⇒ 裸 node --test 加载不了
 *   （同 `api/__tests__/email-account-lww.test.mjs` 头注释记的同因）
 *   ⇒ 判据只能做**源码扫描**，证明不了「分批真的分对了」。
 *   把纯函数拆出来 ⇒ 判据能**行为断言**它
 *   （§919 的分工：源码扫描防「没写」，行为断言防「写了但不生效」）。
 *
 * ⚠ 语义要点：
 *   · **不丢不重**：每批都是原数组的一段连续切片 ⇒ 合起来正好覆盖全部元素。
 *   · **空数组 ⇒ 空结果**（不该发空请求）。
 *   · **上限 ≤ 0 视为 1**（否则会死循环）⇒ 调用方不该传 0，但别让它挂死。
 */
export function chunkByLimit<T>(items: readonly T[], limit: number): T[][] {
  const step = Math.max(1, Math.floor(limit))
  const out: T[][] = []
  for (let i = 0; i < items.length; i += step) {
    out.push(items.slice(i, i + step))
  }
  return out
}
