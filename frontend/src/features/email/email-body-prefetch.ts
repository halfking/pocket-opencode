/**
 * 邮件正文预取（2026-10-01 真机反馈：点进邮件详情失败或非常慢）。
 *
 * 真机症状与根因：点列表项后详情页整屏卡在「加载中…」，最慢能到几十秒。
 * 拆开看 `EmailDetailView.load()` 是一条**完全串行**的 await 链：
 *
 *   getEmail(本地库) → getEmailBody(**网络，服务端还要回 IMAP 拉整封原文**)
 *                    → applyDefaultLang(**可能调 LLM**)
 *                    → renderBody(**预加载远程图，联网**)
 *
 * 而 `loading` 闸门罩住了**整个页面**（连发件人、主题、摘要都在里面）。于是
 * 「网络慢 / LLM 慢 / 图片慢」任意一环卡住，用户看到的就是「进不去详情页」。
 *
 * 这里做两件事：
 *  1. **点击即预取**：列表里 tap 的瞬间就发起正文请求，让网络与路由跳转、
 *     组件挂载**并行**。到详情页时往往已经就绪，首屏几乎无等待。
 *  2. **在途去重**：预取与详情页自身的请求共用同一个 Promise。少了这一步，
 *     同一封邮件会被同时请求两次——真机弱网下第二次往往正是把首屏拖垮的那次。
 *
 * 命中本地缓存的一律不再走网络（正文本就已在本地，没有再拉的道理）。
 *
 * 本模块只做「读取 + 缓存」，不含任何删除语义。
 */

/** 单个 id 的在途请求。用于去重，避免同一封邮件被并发拉两次。 */
const inflight = new Map<string, Promise<string>>()

export interface PrefetchDeps {
  /** 取远端正文（通常是 emailApi.getEmailBody）。 */
  fetchBody: (id: string) => Promise<{ body: string; purged?: boolean; source?: string }>
  /** 读本地缓存。 */
  readCache: (id: string) => Promise<string>
  /** 写本地缓存。 */
  writeCache: (id: string, body: string) => Promise<void>
  /** 从远端报文里抽出可展示正文（注入以便测试）。 */
  extract: (raw: string) => string
}

/** 某封邮件是否正在预取。 */
export function isBodyPrefetching(id: string): boolean {
  return inflight.has(id)
}

/** 当前在途的预取数（测试与调试用）。 */
export function inflightBodyCount(): number {
  return inflight.size
}

/**
 * 预取并缓存一封邮件的正文。
 *
 * 行为：
 *  - 已有缓存 → 直接返回，不再联网。
 *  - 已在途   → 返回同一个 Promise（去重）。
 *  - 拉取失败 → 返回空串并清理在途记录，**不抛错**（预取是加速手段，
 *    失败不应影响列表点击本身）。
 */
export function prefetchEmailBody(id: string, deps: PrefetchDeps): Promise<string> {
  if (!id) return Promise.resolve('')

  const existing = inflight.get(id)
  if (existing) return existing

  const task = (async () => {
    // 先看缓存：命中就没必要联网（正文本就在本地）。
    const cached = await deps.readCache(id).catch(() => '')
    if (cached) return cached

    const remote = await deps.fetchBody(id)
    if (remote?.purged || remote?.source === 'purged') return ''
    const body = deps.extract(remote?.body || '')
    if (body) await deps.writeCache(id, body).catch(() => {})
    return body
  })()
    .catch(() => '')
    .finally(() => {
      inflight.delete(id)
    })

  inflight.set(id, task)
  return task
}

/**
 * 预取「当前这一封 + 紧随其后的几封」。
 *
 * 为什么预取后几封：用户点开详情看完返回，几乎总是往下滑看下一封。若在停留
 * 期间把邻近几封也热好，返回列表再点下一封同样是「秒开」，这才是列表-详情
 * 往返体验的真正瓶颈。
 *
 * 串行而非并发：一次并发太多请求会挤占 WebView 的连接数，反而拖慢当前这封。
 */
export async function prefetchEmailBodySeries(
  ids: string[],
  deps: PrefetchDeps,
  limit = 3,
): Promise<void> {
  for (const id of ids.slice(0, limit)) {
    if (!id) continue
    await prefetchEmailBody(id, deps)
  }
}

/** 仅供测试：清空在途表，避免用例之间互相污染。 */
export function __resetInflightForTest(): void {
  inflight.clear()
}
