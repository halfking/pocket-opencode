/**
 * 正文远程图片预加载（2026-10-01 真机审计 P0：图片要预加载）。
 *
 * 邮件正文里的图有三种来源，处理方式不同：
 *   1. `data:` —— cid 内联图已在解析阶段转好，直接可用。
 *   2. `http(s)://` —— 邮件客户端里的追踪图/外链图。WebView 拿到这串 HTML 后
 *      才开始逐张请求，用户看到的是「文字先出来、图片一个个蹦」，
 *      且慢图会把整页往下顶（CLS）。这就是真机报的「图片没预加载」。
 *   3. 其它（相对路径、cid 残留）—— 不动。
 *
 * 这里在**渲染前**把所有远程图抓下来转成 data URI：受控并发 + 体积上限 +
 * 超时，失败的图退回原 URL（不因单张图挂掉而毁掉整封邮件的排版）。
 *
 * 为什么不用 <img loading="lazy">：lazy 恰恰是「用到才加载」，与预加载目标相反；
 * 且 WebView 对 data URI 渲染最快、最省一次往返。
 */

/** 单张图上限；超过就不内联（保留原 URL，避免 base64 撑爆 WebView 内存）。 */
const MAX_IMAGE_BYTES = 2_000_000
/** 整封邮件内联图总量上限。 */
const MAX_TOTAL_BYTES = 8_000_000
/** 同时抓取数。太高会跟正文抢带宽，反而更慢。 */
const CONCURRENCY = 4
/** 单张图超时；超时即放弃内联。 */
const TIMEOUT_MS = 8_000
/** 只处理前 N 张；营销邮件动辄几十张图，全抓不现实。 */
const MAX_IMAGES = 30

export interface PreloadOptions {
  fetchImpl?: typeof fetch
  signal?: AbortSignal
  /** 便于测试注入计时器。 */
  timeoutMs?: number
  maxImages?: number
  maxTotalBytes?: number
}

/** 从 HTML 里抓出远程图片 URL（去重、保持顺序）。 */
export function collectRemoteImages(html: string): string[] {
  if (!html) return []
  const out: string[] = []
  const seen = new Set<string>()
  const re = /<img\b[^>]*?\bsrc\s*=\s*(["']?)(https?:\/\/[^"'\s>]+)\1/gi
  let m: RegExpExecArray | null
  while ((m = re.exec(html)) !== null) {
    const url = m[2]
    if (seen.has(url)) continue
    seen.add(url)
    out.push(url)
  }
  return out
}

/** 只接受能安全内联的 content-type。 */
function isInlineableType(ct: string): boolean {
  return /^(image\/(png|jpeg|jpg|gif|webp|bmp|svg\+xml|avif))$/i.test(ct.split(';')[0].trim())
}

function bytesToBase64(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf)
  let bin = ''
  const CHUNK = 0x8000
  for (let i = 0; i < bytes.length; i += CHUNK) {
    bin += String.fromCharCode.apply(null, Array.from(bytes.subarray(i, i + CHUNK)))
  }
  return btoa(bin)
}

async function fetchOne(
  url: string,
  fetchImpl: typeof fetch,
  timeoutMs: number,
  signal?: AbortSignal,
): Promise<string | null> {
  const ctl = typeof AbortController !== 'undefined' ? new AbortController() : null
  const timer = ctl ? setTimeout(() => ctl.abort(), timeoutMs) : null
  try {
    const res = await fetchImpl(url, {
      // 邮件里的图常常没有跨域 CORS 头；no-cors 拿不到 body，所以仍用默认模式，
      // 失败就走 catch 退回原 URL。
      credentials: 'omit',
      redirect: 'follow',
      signal: ctl?.signal ?? signal,
    })
    if (!res.ok) return null
    const ct = res.headers.get('content-type') || ''
    if (!isInlineableType(ct)) return null
    const buf = await res.arrayBuffer()
    if (buf.byteLength === 0 || buf.byteLength > MAX_IMAGE_BYTES) return null
    return `data:${ct.split(';')[0].trim()};base64,${bytesToBase64(buf)}`
  } catch {
    return null
  } finally {
    if (timer) clearTimeout(timer)
  }
}

/** 受控并发跑 worker，收集成功的结果。 */
async function mapWithConcurrency<T, R>(
  items: T[],
  limit: number,
  worker: (item: T, index: number) => Promise<R>,
): Promise<R[]> {
  const results: R[] = new Array(items.length)
  let cursor = 0
  const runners = Array.from({ length: Math.min(limit, items.length) }, async () => {
    for (;;) {
      const i = cursor++
      if (i >= items.length) return
      results[i] = await worker(items[i], i)
    }
  })
  await Promise.all(runners)
  return results
}

/**
 * 预加载并内联远程图片。
 *
 * 返回**新的 HTML**；入参不变。抓取失败的图保持原样，不抛错——
 * 一张图挂掉不该让整封邮件打不开。
 */
export async function preloadRemoteImages(
  html: string,
  options: PreloadOptions = {},
): Promise<string> {
  if (!html || !/<img\b/i.test(html)) return html
  const fetchImpl = options.fetchImpl ?? (typeof fetch !== 'undefined' ? fetch : null)
  if (!fetchImpl) return html

  const urls = collectRemoteImages(html).slice(0, options.maxImages ?? MAX_IMAGES)
  if (!urls.length) return html

  const timeoutMs = options.timeoutMs ?? TIMEOUT_MS
  const maxTotal = options.maxTotalBytes ?? MAX_TOTAL_BYTES

  let budget = maxTotal
  const inlined = await mapWithConcurrency(urls, CONCURRENCY, async (url) => {
    if (budget <= 0) return null
    const dataUri = await fetchOne(url, fetchImpl, timeoutMs, options.signal)
    if (!dataUri) return null
    // base64 长度 ≈ 原始字节 * 4/3
    const approx = Math.floor((dataUri.length * 3) / 4)
    if (approx > budget) return null
    budget -= approx
    return dataUri
  })

  let out = html
  urls.forEach((url, i) => {
    const dataUri = inlined[i]
    if (!dataUri) return
    // 只替换精确匹配的 src 值，避免误伤其它属性里的同名字符串。
    out = out.replace(
      new RegExp(`(\\bsrc\\s*=\\s*["'])${escapeRe(url)}(["'])`, 'gi'),
      (_m, pre: string, post: string) => `${pre}${dataUri}${post}`,
    )
  })
  return out
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
