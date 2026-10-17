/**
 * safeExternalUrl — 外部链接 sink 的协议白名单。
 *
 * §852 取证：全仓 26 处**动态** URL sink（`:href` / `:src` / `window.open` /
 * `location.href` / `location.assign`），**没有任何一处校验协议** ——
 * 而 RSS 文章的 `url` 字段是**订阅源作者完全可控**的（服务端
 * `backend/internal/rss/store_entities.go` 原样入库），它直接进了
 * `<a :href>` 与 `window.open`。
 *
 * 为什么需要：`<a href="javascript:…">` 在 WebView 里同源执行；
 * Android WebView 对 `intent:` / `market:` 这类自定义协议还会交给系统去解析
 * ⇒ 一个恶意订阅不只是能骗用户点链接，还能把手伸到应用之外。
 *
 * ⛔ 未实测：`target="_blank"` 下 `javascript:` 是否仍执行，**依 WebView 版本而定**，
 * 本节没有在真机上验证过。这里按**最坏情况**处理（不放行）。
 *
 * 本仓在别处已经用同一套思路：`api/api-base.ts` 拒绝 `javascript:` 开头的
 * `normalizeApiBase`，`aiChatStore` 把图片过滤成 `https://` 或 `data:image/`。
 * ⇒ 这里把它抽成共享函数，让各处 sink 共用一份口径。
 */

/**
 * 控制符与空白：浏览器在解析 URL 协议前会**剥掉**它们，
 * 所以 `java\nscript:alert(1)` 这类拼法照样能执行 —— 必须在判定协议之前先拒掉。
 * ⚠ 这里必须用**转义写法**：写成字面控制字符会让源文件变成二进制、且不可读。
 */
const CONTROL_CHARS = /[\u0000-\u0020\u007f-\u009f]/

/**
 * 只放行 `http(s):`、协议相对 `//host` 与 `mailto:`；其余一律返回空串。
 *
 * ⚠ 协议相对地址（`//evil.com`）与显式 http(s) 等价，放行是安全的；
 *   而 `tel:` / `sms:` / `intent:` 这类**会交给系统处理**的协议**不放行**
 *   —— 它们不是「打开一个网页」，是「让别的应用做事」。
 */
export function safeExternalUrl(raw?: string | null): string {
  // ⚠ 非字符串入参（模板里绑定到数字/对象时）不得抛 —— 它在模板里被直接调用，
  //   抛错会把整个渲染带崩，比「放行一个坏 URL」严重得多。
  if (raw === null || raw === undefined) return ''
  const v = String(raw).trim()
  if (!v) return ''
  if (CONTROL_CHARS.test(v)) return ''
  if (/^https?:\/\//i.test(v)) return v
  if (/^\/\//.test(v)) return v
  if (/^mailto:/i.test(v)) return v
  return ''
}

/** 判定形式：`isSafeExternalUrl(x)`。 */
export function isSafeExternalUrl(raw?: string | null): boolean {
  return safeExternalUrl(raw) !== ''
}
