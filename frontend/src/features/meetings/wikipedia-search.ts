/**
 * wikipedia-search.ts — 第三方（zh.wikipedia）检索的**可测核心**（§926）。
 *
 * ── 为什么要抽出来 ──
 * 原来这段逻辑住在 `meeting-related-search.ts` 里，而那个文件 import 了
 * `native/local-db` ⇒ **Node 里 import 不了** ⇒ 任何判据都只能去扫源码，
 * 扫源码的判据对「超时真的生效吗」这种问题**没有回答力**。
 * ⇒ 按 §881/§879 同一手法：把「零依赖的那一半」搬进纯模块，
 *   调用方留在原处（行为不变），判据就能**真的把请求打出去**。
 *
 * ── §926 的缺陷本体：这条请求**没有等待上界** ──
 * `api/http.ts` 给 App 自己的每一个请求都上了 30 s 上限
 * （`DEFAULT_TIMEOUT_MS`），唯独这里用的是**裸 `fetch`** ⇒
 * 第三方连接一旦卡住（TCP stall / DNS 黑洞 / 中间设备吞包），
 * 这个 promise **永远不落地**：
 *   ① `searchRelatedContext` 用 `Promise.all` 等三条路 ⇒ **整个相关推荐面板的
 *      这一次刷新被无限期挂住**；
 *   ② 而刷新是**每段触发**的（`watch(displaySegments.length)` ⇒ 2.5 s 定时器）
 *      ⇒ 挂住的那一次**不会阻止下一次** ⇒ 段每来一个就再叠一个在途请求
 *      ⇒ **在途的第三方请求数没有上界**。
 * 实测（真实会议语料，35 个不同查询串）：中位 406 ms、p90 532 ms、**max 6932 ms**
 * ⇒ 尾延迟是真的，但**没有一次超过 10 s** ⇒ 取 10 s 作上界
 * **只对「真挂住」生效**，正常路径逐字不变。
 *
 * ⚠ 超时**不新增用户可见的失败面**：超时后走的是与「网络失败」**同一条**
 * 既有语义（这一路没拿到 ⇒ 返回空数组），界面表现与今天断网时完全一致。
 */
import { parseWikipediaOpenSearch, wikipediaSearchUrl } from './meeting-related.ts'
import type { RecommendItem } from './meetings-store.ts'

/**
 * 第三方检索的等待上界（毫秒）。
 *
 * ⚠ 取值依据（实测，非拍脑袋）：中位 406 ms / p90 532 ms / max 6932 ms
 *   ⇒ 10 s 高于实测最大值 1.44 倍，**只对真挂住生效**。
 * ⚠ 它**不是**「多久算慢」的判据，而是「多久算挂住」的判据 ——
 *   比它慢的请求今天也会成功（只是慢），加上界之后会被放弃。
 */
export const WIKIPEDIA_TIMEOUT_MS = 10_000

/**
 * `fetch` 的最小替身形态：判据注入假件，生产用真 `fetch`。
 *
 * ⚠ 只声明用到的两个成员 ⇒ 判据不必伪造整个 Response。
 */
export type WikipediaFetch = (
  url: string,
  init?: { signal?: AbortSignal },
) => Promise<{ ok: boolean; json: () => Promise<unknown> }>

/**
 * 查一次维基百科，拿到参考资料条目。
 *
 * @param query 查询串（空 ⇒ 不发请求）
 * @param opts.fetchImpl  注入的 fetch（判据用；生产缺省用全局 fetch）
 * @param opts.timeoutMs  等待上界（判据用；生产缺省 `WIKIPEDIA_TIMEOUT_MS`）
 * @returns 条目数组；**任何失败（含超时）都返回空数组** ——
 *   与改造前的既有语义逐字一致（`catch { return [] }`）。
 */
export async function fetchWikipediaItems(
  query: string,
  opts?: { fetchImpl?: WikipediaFetch; timeoutMs?: number },
): Promise<RecommendItem[]> {
  const url = wikipediaSearchUrl(query)
  if (!url) return []
  const timeoutMs = opts?.timeoutMs ?? WIKIPEDIA_TIMEOUT_MS
  const doFetch = opts?.fetchImpl ?? (globalThis.fetch as unknown as WikipediaFetch)
  // ⚠ 没有 AbortController 就没有上界：fetch 自己不会超时，
  //   而这里的调用方是 Promise.all ⇒ 一条挂住的路会拖住整个面板刷新。
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    const res = await doFetch(url, { signal: controller.signal })
    if (!res.ok) return []
    return parseWikipediaOpenSearch(await res.json())
  } catch {
    // 超时（AbortError）与网络失败**同义**：这一路没拿到。
    return []
  } finally {
    clearTimeout(timer)
  }
}

/**
 * 维基那一路：**主查 + 至多一次回落**。
 *
 * ⚠⚠ **这条是「每次触发的外部请求预算」**（§926）：
 *   相关推荐的刷新是**每段触发**的 ⇒ 每段都会打到这里 ⇒
 *   实测（真实会议语料 35 个不同查询串）：
 *     · 主查命中 **0/35**（中文 2-token 查询，opensearch 必然落空）
 *     · 落空后回落 31 次，其中命中 3 次
 *     · ⇒ **每次触发 1.89 次外网请求，整体命中率 8.6%**
 *   ⇒ 也就是说：**九成的外网请求什么也没换来**，而且每一次都把
 *   会议内容（最后 6 段里的两个 token）发给了第三方。
 *   ⇒ 「这条路对中文会议还要不要留」是**产品口径**（已登记，未代拍）；
 *   本函数只保证**请求数有上界**（至多 2 次/触发）。
 */
export async function searchRelatedWeb(
  query: string,
  opts?: { fetchImpl?: WikipediaFetch; timeoutMs?: number },
): Promise<RecommendItem[]> {
  const primary = await fetchWikipediaItems(query, opts)
  if (primary.length) return primary
  const fallback = query.split(/\s+/).filter(Boolean).pop() || ''
  if (!fallback || fallback === query) return []
  return fetchWikipediaItems(fallback, opts)
}
