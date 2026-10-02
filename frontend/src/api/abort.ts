/**
 * 「这次失败是不是用户/调用方主动中止」的**唯一**判据。
 *
 * 单独成文件、且**零 import**，不是为了整洁，是因为它必须能被护栏直接 import
 * 来跑真实行为断言：http.ts 自己 import 了 `../config/api-base`（无扩展名），
 * node 下是 ERR_MODULE_NOT_FOUND，所以任何依赖 http.ts 的测试都只能退回「读源码
 * 文本 + 正则」——而正则判据分不出「判据自己失效」和「性质真的坏了」。
 * 判据文件自己可执行，才谈得上"负控能证明它会红"。
 *
 * 为什么用 `name` 而不是 `instanceof DOMException`：
 *  · Capacitor WebView / 不同 Node 环境下 AbortError 的构造器不保证是本域的
 *    DOMException，`instanceof` 会漏判。漏判的后果是**中止被当成失败**——
 *    用户主动取消，被悄悄换成一份降级结果写进库里（会议摘要、精翻兜底链
 *    都是先 catch 再返回 fallback 的形状）。
 *  · 不用 `signal.aborted` 兜底判断：signal 是入参、错误对象才是出口，
 *    两者不保证同时可见（超时那条路上根本没有调用方的 signal）。
 *
 * 为什么不把 TimeoutError 也算成中止：它是**失败**，上层要据此提示/降级。
 * 两者靠不同的 name 区分，TimeoutError 的 name 见 http.ts。
 */
export function isAbortError(e: unknown): boolean {
  return !!e && typeof e === 'object' && (e as { name?: unknown }).name === 'AbortError'
}
