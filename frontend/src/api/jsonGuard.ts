/**
 * jsonGuard — 零依赖的响应守卫（不得 import Pinia/Capacitor 等任何模块，
 * native/mobileSyncRuntime 等纯 runtime 代码也要引用）。
 *
 * 移动端 APK 漏注入 VITE_API_BASE 时，/api/* 会落到 Capacitor WebView 本地
 * 资源服务并被 SPA 兜底成 index.html（200, text/html），调用方 res.json()
 * 只会抛出难懂的 "Unexpected token '<'"。在解析前识别 text/html，换成
 * 可定位的错误信息。仅拦 text/html：204 无 body 由调用方先行短路，
 * 流式响应（text/event-stream）不适用本守卫。
 */
export function assertNotHTML(res: Response): Response {
  const ct = (res.headers.get('content-type') || '').toLowerCase()
  if (ct.includes('text/html')) {
    throw new Error(
      'API 返回了 HTML 页面而非 JSON：通常是移动端打包漏注入 VITE_API_BASE（请求落到 WebView 本地 index.html），或被网关/代理重定向。请用 scripts/build-mobile.mjs 并确认 VITE_API_BASE 已注入',
    )
  }
  return res
}
