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

/**
 * 协议相对 URL（`//host/path`）归一为 https 绝对地址。
 *
 * 2026-10-02 探针实测的缺口：原 `collectRemoteImages` 的正则只认 `https?://`，
 * 于是 `<img src="//host/x.png">`、无引号的 `src=//host/x.png`、
 * 以及 CSS 的 `background-image:url(//host/x.png)` **三种写法一个都抓不到**，
 * 无法内联。而邮件正文在 WebView 里的基址是应用自己的（capacitor://），
 * 协议相对 URL 会被解析成 `capacitor://host/...` —— 必然加载失败。
 * 这与 cid: 那次是同一类缺陷（同一段 HTML 里三种合法写法，只覆盖了一种）。
 *
 * 归一到 https 而不是 http：邮件图床几乎都支持 https，且不引入明文传输。
 */
export function normalizeRemoteUrl(raw: string): string {
  return raw.startsWith('//') ? `https:${raw}` : raw
}

/** 一处远程图片引用：raw 是 HTML 里的原样写法，url 是可抓取的绝对地址。 */
export interface RemoteImageRef {
  raw: string
  url: string
}

/**
 * 从 HTML 里抓出远程图片引用（去重、保持顺序）。
 *
 * 覆盖四种写法，少一种就等于「那段 HTML 里的图永远不会被内联」：
 *
 *   1. `<img src="URL">`   带引号 / 无引号都收
 *   2. `<img data-src=…>`  懒加载占位。营销与通知邮件极常见：真地址放在
 *      data-src / data-original / data-lazy-src 里，而 src 是个 1×1 追踪像素
 *      或空。**只认 src 等于永远抓不到真图**。
 *   3. `url(URL)`          CSS 背景图。inlineDataUri 一直会替换它，但**收集侧
 *      以前从不抓它**，于是那段替换逻辑是够不到的死代码——背景图永远不会被
 *      内联，只能靠 WebView 自己发请求，而基址是 capacitor://，相对/远程
 *      背景图照样加载不出来。
 *   4. `<table background="URL">` 老式邮件排版背景，Outlook 时代写法。
 *
 * 协议：https 绝对 与 协议相对（`//host/...`）都收；引号可有可无。
 * 返回的 `url` 一律是绝对地址（抓取用），`raw` 保留 HTML 原样写法（回填用），
 * 否则回填时会在 HTML 里找不到 `//host/x.png` 这个原值。
 */
export function collectRemoteImageRefs(html: string): RemoteImageRef[] {
  if (!html) return []
  const out: RemoteImageRef[] = []
  const seen = new Set<string>()
  const push = (raw: string) => {
    if (!raw) return
    const url = normalizeRemoteUrl(raw)
    if (seen.has(url)) return
    seen.add(url)
    out.push({ raw, url })
  }

  // 注意 alternation 顺序：`//` 必须排在 `https?://` 前面，
  // 否则 `https://…` 会先被 `//` 之外的长匹配吃掉一部分。
  const remote = '(?:\\/\\/|https?:\\/\\/)[^"\'\\s>)]+'

  // 1) + 2) <img> 标签里的 src / data-src / data-original / data-lazy-src。
  //
  //    先把标签切出来、再在标签内部找属性，**不能**用
  //    `<img[^>]*?\bsrc\s*=\s*…` 一条正则直接扫全文：那样每个标签只可能
  //    匹配一次（`<img` 锚点 + 惰性量词命中第一个属性后 lastIndex 就越过了
  //    整个标签），于是 `src="追踪像素" data-src="真图"` 只会收到追踪像素，
  //    真图永远收不到——而这恰恰是营销邮件最常见的写法。
  const tagRe = /<img\b[^>]*>/gi
  const attrRe = new RegExp(
    `\\b(?:src|data-src|data-original|data-lazy-src)\\s*=\\s*(["']?)(${remote})\\1`,
    'gi',
  )
  let tm: RegExpExecArray | null
  while ((tm = tagRe.exec(html)) !== null) {
    const tag = tm[0]
    attrRe.lastIndex = 0
    let am: RegExpExecArray | null
    while ((am = attrRe.exec(tag)) !== null) push(am[2])
  }

  // 3) CSS url()：出现在 style 属性里和 <style> 块里，形态相同。
  const cssRe = new RegExp(`url\\(\\s*["']?(${remote})["']?\\s*\\)`, 'gi')
  let m: RegExpExecArray | null
  while ((m = cssRe.exec(html)) !== null) push(m[1])

  // 4) 老式邮件排版的 background 属性：<td background="…"> 比 <table background>
  //    常见得多（每个色块一个单元格），所以这里不限定标签名，任何元素的
  //    background 属性都收。
  const bgRe = new RegExp(`\\sbackground\\s*=\\s*(["']?)(${remote})\\1`, 'gi')
  while ((m = bgRe.exec(html)) !== null) push(m[2])

  return out
}

/** 从 HTML 里抓出远程图片 URL（去重、保持顺序，绝对地址）。 */
export function collectRemoteImages(html: string): string[] {
  return collectRemoteImageRefs(html).map((r) => r.url)
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
  if (!html) return html
  // 注意：**不能**用 `if (!/<img\b/i.test(html)) return html` 提前返回。
  // 排版型邮件（很多通知/营销邮件）整封只有表格 + CSS 背景图，一个 <img> 都没有；
  // 那样早退会让这批邮件的图**一张都进不了待抓列表**，而 WebView 基址是
  // capacitor://，这些背景图自己发请求也加载不出来 —— 详情页表现为「缺图」。
  // 是否真有可内联的图，交给下面的 collectRemoteImageRefs 判。
  const fetchImpl = options.fetchImpl ?? (typeof fetch !== 'undefined' ? fetch : null)
  if (!fetchImpl) return html

  const refs = collectRemoteImageRefs(html).slice(0, options.maxImages ?? MAX_IMAGES)
  if (!refs.length) return html

  const timeoutMs = options.timeoutMs ?? TIMEOUT_MS
  const maxTotal = options.maxTotalBytes ?? MAX_TOTAL_BYTES

  let budget = maxTotal
  const inlined = await mapWithConcurrency(refs, CONCURRENCY, async (ref) => {
    if (budget <= 0) return null
    const dataUri = await fetchOne(ref.url, fetchImpl, timeoutMs, options.signal)
    if (!dataUri) return null
    // base64 长度 ≈ 原始字节 * 4/3
    const approx = Math.floor((dataUri.length * 3) / 4)
    if (approx > budget) return null
    budget -= approx
    return dataUri
  })

  let out = html
  refs.forEach((ref, i) => {
    const dataUri = inlined[i]
    if (!dataUri) return
    out = inlineDataUri(out, ref.raw, ref.url, dataUri)
  })
  return out
}

/**
 * 把 HTML 里所有指向该图片的引用换成 data URI。
 *
 * 必须覆盖三种合法写法，否则「抓到了但没换上」等于没抓到：
 *   1. 带引号 src   `<img src="URL">` / `src='URL'`
 *   2. 无引号 src   `<img src=URL>`（HTML 允许）
 *   3. CSS url()    `background-image:url(URL)` / `url("URL")` —— 营销与通知邮件的
 *      背景图大量走这条，只处理 src 会让背景图默默空掉（cid: 那次已修过一次，
 *      这次是同一段逻辑的远程图分支）。
 *
 * raw 与 url 都要替换：抓取用的是归一后的 url，而 HTML 里写的是 raw。
 */
function inlineDataUri(html: string, raw: string, url: string, dataUri: string): string {
  let out = html
  for (const form of new Set([raw, url])) {
    const esc = escapeRe(form)
    // 1) 带引号 src
    out = out.replace(
      new RegExp(`(\\bsrc\\s*=\\s*["'])${esc}(["'])`, 'gi'),
      (_m, pre: string, post: string) => `${pre}${dataUri}${post}`,
    )
    // 2) 无引号 src（后面必须是非引号/空白/尖括号，避免吃掉半个属性）
    out = out.replace(
      new RegExp(`(\\bsrc\\s*=\\s*)${esc}(?=["'\\s>])`, 'gi'),
      (_m, pre: string) => `${pre}${dataUri}`,
    )
    // 3) CSS url()：带引号与不带引号两种
    out = out.replace(
      new RegExp(`(url\\(\\s*["']?)${esc}(["']?\\s*\\))`, 'gi'),
      (_m, pre: string, post: string) => `${pre}${dataUri}${post}`,
    )
  }
  return out
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
