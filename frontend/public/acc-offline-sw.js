/**
 * ACC 看板离线缓存 Service Worker（融合方案 M2 任务13 · 可选加分项）。
 *
 * 范围刻意收窄（不做复杂策略，降低对主应用的影响面）：
 *   - 只拦 GET；非 GET 一律直接放行（不改行为）。
 *   - 同源 `/assets/`（Vite 内容哈希文件名）→ stale-while-revalidate：
 *     先回缓存、后台更新 —— 哈希文件名保证内容不可变，stale 一次无害。
 *   - 导航请求（页面壳）→ network-first，失败回缓存的应用壳（离线可打开页面）。
 *   - 其余一切（API 调用、跨域、ACC 直连）→ 原样放行，**不缓存不修改**
 *     —— 登录态 / ACC Bearer 请求绝不过 SW 的手。
 *
 * 注册点：仅 ACC 看板页（TaskBoardView onMounted）调用 register；
 * scope 显式给 '/'（hash 路由 SPA 的页面 URL 永远是 '/'，窄 scope 匹配不到页面）。
 */

const CACHE_NAME = 'acc-board-offline-v1'
const APP_SHELL = 'index.html'

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => cache.addAll([APP_SHELL])).catch(() => undefined),
  )
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const req = event.request
  if (req.method !== 'GET') return

  const url = new URL(req.url)
  const sameOrigin = url.origin === self.location.origin

  // 应用壳：network-first，离线回缓存（保证断网时看板页面能打开）。
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req)
        .then((res) => {
          const copy = res.clone()
          caches.open(CACHE_NAME).then((c) => c.put(APP_SHELL, copy)).catch(() => undefined)
          return res
        })
        .catch(() => caches.match(APP_SHELL).then((hit) => hit || Response.error())),
    )
    return
  }

  // 静态资源（内容哈希）：stale-while-revalidate。
  if (sameOrigin && url.pathname.startsWith('/assets/')) {
    event.respondWith(
      caches.match(req).then((hit) => {
        const refresh = fetch(req)
          .then((res) => {
            if (res.ok) {
              const copy = res.clone()
              caches.open(CACHE_NAME).then((c) => c.put(req, copy)).catch(() => undefined)
            }
            return res
          })
          .catch(() => hit || Response.error())
        return hit || refresh
      }),
    )
    return
  }

  // 其余（API / 跨域 / ACC 直连）：不 respondWith ⇒ 原样放行。
})
