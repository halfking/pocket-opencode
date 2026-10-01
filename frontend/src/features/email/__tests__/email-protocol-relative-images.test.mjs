// email-protocol-relative-images.test.mjs
//
// 协议相对图片 URL（`//host/path`）必须被内联。
//
// 2026-10-02 探针实测的缺口：原 collectRemoteImages 的正则只认 `https?://`，
// 于是三种合法写法一个都抓不到：
//   <img src="//host/x.png">   带引号 src
//   <img src=//host/x.png>     无引号 src（HTML 允许）
//   <td style="background-image:url(//host/bg.png)">   CSS url()
//
// 而邮件正文在 WebView 里的基址是应用自己的（capacitor://），
// `//host/x.png` 会被解析成 `capacitor://host/x.png` —— 必然加载失败。
// 这与 2026-10-01 修过的 cid: 是同一类缺陷：同一段 HTML 里的三种合法写法，
// 当时只覆盖了「带引号的 src」一种。
//
// 负控（见文件末尾）：把 normalizeRemoteUrl 改回原样（返回 raw），
// 下面 3 条协议相对用例必须转红，而 2 条 https 回归用例必须仍绿。

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  collectRemoteImageRefs,
  collectRemoteImages,
  normalizeRemoteUrl,
  preloadRemoteImages,
} from '../email-image-preload.ts'

const S = '/'
const host = 'img.example.com'
const rel = `${S}${S}${host}/logo.png`
// 注意冒号：第一版写成 `https${S}${S}…` 得到 `https//host/...`，
// 9 条用例一起红——第一反应会怀疑实现，其实是**测试自己**的字符串拼错。
const abs = `https:${S}${S}${host}/logo.png`

/** fetchOne 只需要一个最小 Response 形状，body 长度够算预算即可。 */
function fakeBytes() {
  return { byteLength: 1024 }
}

describe('协议相对图片 URL 归一', () => {
  it('//host 归一为 https 绝对地址', () => {
    assert.equal(normalizeRemoteUrl(rel), abs)
  })
  it('已经是 https 的原样返回（不得被改写）', () => {
    assert.equal(normalizeRemoteUrl(abs), abs)
    assert.equal(normalizeRemoteUrl(`http${S}${S}${host}/a.png`), `http${S}${S}${host}/a.png`)
  })
})

describe('协议相对图片被收集到', () => {
  it('带引号 src', () => {
    assert.deepEqual(collectRemoteImages(`<img src="${rel}">`), [abs])
  })
  it('无引号 src', () => {
    assert.deepEqual(collectRemoteImages(`<img src=${rel}>`), [abs])
  })
  it('单引号 src', () => {
    assert.deepEqual(collectRemoteImages(`<img src='${rel}'>`), [abs])
  })
  it('CSS url() 出现在 style 属性里（收集阶段不收，但回填阶段要能换）', () => {
    // collect 只看 <img src>，这是既有契约；CSS 的覆盖在下面 inlineDataUri 用例里验。
    const html = `<td style="background-image:url(${rel})"></td>`
    assert.deepEqual(collectRemoteImages(html), [])
    assert.equal(collectRemoteImageRefs(html).length, 0)
  })
  it('去重：同一张图出现两次只收一次，且保留 raw 原样写法', () => {
    const refs = collectRemoteImageRefs(`<img src="${rel}"><img src="${abs}">`)
    assert.equal(refs.length, 1, '归一后应视为同一张图')
    assert.equal(refs[0].raw, rel, 'raw 必须保留 HTML 原样写法，否则回填找不到')
    assert.equal(refs[0].url, abs)
  })
})

describe('协议相对图片被换成 data URI（三种写法）', () => {
  const fetchImpl = async (url) => {
    const u = String(url)
    if (!u.startsWith(`https:${S}${S}`)) throw new Error('not absolute: ' + u)
    return { ok: true, headers: { get: () => 'image/png' }, arrayBuffer: async () => fakeBytes() }
  }
  const opts = { fetchImpl, timeoutMs: 50 }

  it('带引号 src 被替换', async () => {
    const out = await preloadRemoteImages(`<img src="${rel}">`, opts)
    assert.match(out, /src="data:image\/png;base64,/)
    assert.ok(!out.includes(rel), '原协议相对 URL 必须被换掉')
  })
  it('无引号 src 被替换', async () => {
    const out = await preloadRemoteImages(`<img src=${rel}>`, opts)
    assert.match(out, /src=data:image\/png;base64,/)
    assert.ok(!out.includes(rel))
  })
  it('CSS url() 被替换（抓不抓到取决于有没有 <img>，但同 URL 被换掉才算数）', async () => {
    // 造一个既带 <img> 又带 CSS url() 的片段，两者指向同一张图。
    const html = `<img src="${rel}"><td style="background-image:url(${rel})"></td>`
    const out = await preloadRemoteImages(html, opts)
    assert.ok(!out.includes(rel), 'CSS url() 里的协议相对 URL 也必须被换掉')
    assert.equal(out.match(/data:image\/png;base64,/g).length, 2)
  })
  it('https 绝对地址行为不变（回归）', async () => {
    const out = await preloadRemoteImages(`<img src="${abs}">`, opts)
    assert.match(out, /src="data:image\/png;base64,/)
  })
  it('抓取失败时保持原样、不抛错（回归）', async () => {
    const out = await preloadRemoteImages(`<img src="${rel}">`, {
      fetchImpl: async () => ({ ok: false, headers: { get: () => '' }, arrayBuffer: async () => fakeBytes() }),
      timeoutMs: 50,
    })
    assert.ok(out.includes(rel), '失败时必须原样返回')
  })
})

// 负控：把 normalizeRemoteUrl 改成恒等（返回 raw），则 3 条协议相对收集用例
// 与 3 条内联用例必须转红，2 条 https 回归用例必须仍绿。
