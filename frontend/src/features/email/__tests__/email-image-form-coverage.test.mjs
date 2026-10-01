// email-image-form-coverage.test.mjs
//
// 远程图片引用的**形态覆盖**：少收一种形态，那段 HTML 里的图就永远不会被内联。
//
// 2026-10-02 查出的自相矛盾：inlineDataUri 一直会替换 CSS 的 url()，它的注释
// 也写着「营销与通知邮件的背景图大量走这条，只处理 src 会让背景图默默空掉」——
// 但 collectRemoteImageRefs 只认 <img src>，url() 压根进不了待抓列表，那段替换
// 逻辑是**够不到的死代码**。既有的 email-protocol-relative-images.test.mjs 也
// 在用例注释里承认了这点：「抓不抓到取决于有没有 <img>」。
//
// 同样漏掉的还有营销邮件常见的两种：
//   · 懒加载占位：真地址在 data-src / data-original / data-lazy-src 上，
//     而 src 是 1×1 追踪像素或空 —— 只认 src 永远抓不到真图；
//   · <table background="…">：老式邮件排版。
//
// 这些写法的共同后果：WebView 基址是 capacitor://，远程/相对背景图自己发请求
// 也加载不出来，于是用户看到的就是「邮件详情缺图片」。

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  collectRemoteImageRefs,
  collectRemoteImages,
  normalizeRemoteUrl,
  preloadRemoteImages,
} from '../email-image-preload.ts'

/** fetchOne 只需要一个最小 Response 形状。 */
function okImage() {
  return {
    ok: true,
    headers: { get: () => 'image/png' },
    arrayBuffer: async () => ({ byteLength: 1024 }),
  }
}

describe('img src：三种引号写法', () => {
  it('双引号 / 单引号 / 无引号 / 属性在中间', () => {
    const html = `
      <img src="https://a.com/1.png">
      <img src='https://a.com/2.png'>
      <img src=https://a.com/3.png>
      <img width="10" src="https://a.com/4.png" alt="x">
    `
    assert.deepEqual(collectRemoteImages(html), [
      'https://a.com/1.png',
      'https://a.com/2.png',
      'https://a.com/3.png',
      'https://a.com/4.png',
    ])
  })
})

describe('img data-src 等懒加载属性（旧实现完全收不到）', () => {
  it('data-src', () => {
    const got = collectRemoteImages(
      '<img src="https://track.com/p.gif" data-src="https://cdn.com/hero.jpg">',
    )
    assert.ok(got.includes('https://cdn.com/hero.jpg'), 'data-src 未被收集: ' + JSON.stringify(got))
  })
  it('data-original', () => {
    const got = collectRemoteImages("<img data-original='https://cdn.com/o.png'>")
    assert.ok(got.includes('https://cdn.com/o.png'), 'data-original 未被收集')
  })
  it('data-lazy-src（无引号）', () => {
    const got = collectRemoteImages('<img data-lazy-src=https://cdn.com/l.webp>')
    assert.ok(got.includes('https://cdn.com/l.webp'), 'data-lazy-src 未被收集')
  })
})

describe('CSS url()（旧实现只替换、不收集 → 死代码）', () => {
  it('style 属性里、不带引号', () => {
    const got = collectRemoteImages(
      '<div style="background-image:url(https://cdn.com/bg1.png)"></div>',
    )
    assert.ok(got.includes('https://cdn.com/bg1.png'), 'style 里的 url() 未被收集')
  })
  it("style 属性里、带引号", () => {
    const got = collectRemoteImages(
      "<div style=\"background: url('https://cdn.com/bg2.png') no-repeat\"></div>",
    )
    assert.ok(got.includes('https://cdn.com/bg2.png'), '带引号的 url() 未被收集')
  })
  it('<style> 块里', () => {
    const got = collectRemoteImages(
      '<style>.h{background-image:url(https://cdn.com/bg3.png)}</style>',
    )
    assert.ok(got.includes('https://cdn.com/bg3.png'), '<style> 块里的 url() 未被收集')
  })
  it('<style> 块里、带双引号', () => {
    const got = collectRemoteImages('<style>.i{background:url("https://cdn.com/bg4.jpg")}</style>')
    assert.ok(got.includes('https://cdn.com/bg4.jpg'), '双引号 url() 未被收集')
  })

  it('纯背景图邮件（整封没有 <img>）也必须被抓到', () => {
    // 这是死代码最直接的体现：没有 <img> 可依附时，旧实现抓到 0 张。
    const html = '<table width="600"><tr><td background="https://cdn.com/tile.png">hi</td></tr></table>'
    assert.ok(
      collectRemoteImages(html).length > 0,
      '整封邮件没有 <img> 时一张都抓不到 —— 背景图永远不会被内联',
    )
  })
})

describe('<table background=…>', () => {
  it('老式邮件排版背景', () => {
    const got = collectRemoteImages('<table background="https://cdn.com/t.png"><tr><td>x</td></tr></table>')
    assert.ok(got.includes('https://cdn.com/t.png'), 'table background 未被收集')
  })
})

describe('协议相对 //host 在新增形态上同样要收', () => {
  it('data-src 与 CSS url() 里的 //host', () => {
    const html = `
      <img data-src="//cdn.com/a.png">
      <div style="background:url(//cdn.com/b.png)"></div>
    `
    assert.deepEqual(collectRemoteImages(html), ['https://cdn.com/a.png', 'https://cdn.com/b.png'])
  })
  it('normalizeRemoteUrl 只动 // 前缀', () => {
    assert.equal(normalizeRemoteUrl('//x/y'), 'https://x/y')
    assert.equal(normalizeRemoteUrl('https://x/y'), 'https://x/y')
    assert.equal(normalizeRemoteUrl('http://x/y'), 'http://x/y')
  })
})

describe('去重与不误收（回归）', () => {
  it('同一张图出现多次只收一次', () => {
    const html = `
      <img src="https://cdn.com/x.png">
      <div style="background:url(https://cdn.com/x.png)"></div>
      <img data-src="https://cdn.com/x.png">
    `
    assert.deepEqual(collectRemoteImages(html), ['https://cdn.com/x.png'])
  })
  it('data: / cid: / 相对路径 / 纯文本 一律不收', () => {
    const html = `
      <img src="data:image/png;base64,AAAA">
      <img src="cid:logo001">
      <img src="/relative/path.png">
      <a href="https://example.com/page">链接</a>
      <p>见 https://example.com/not-an-image 谢谢</p>
    `
    assert.deepEqual(collectRemoteImages(html), [])
  })
  it('collectRemoteImageRefs 保留 raw 原样写法（回填要用）', () => {
    const refs = collectRemoteImageRefs('<img data-src="//cdn.com/a.png">')
    assert.equal(refs.length, 1)
    assert.equal(refs[0].raw, '//cdn.com/a.png')
    assert.equal(refs[0].url, 'https://cdn.com/a.png')
  })
})

describe('preloadRemoteImages 端到端', () => {
  it('背景图与懒加载图都会被抓取并内联', async () => {
    const seen = []
    const html = `
      <img src="https://cdn.com/hero.png" data-src="https://cdn.com/hero2.png">
      <div style="background-image:url(https://cdn.com/bg.png)"></div>
      <style>.h{background:url("https://cdn.com/hdr.png")}</style>
    `
    const out = await preloadRemoteImages(html, {
      fetchImpl: async (u) => {
        seen.push(String(u))
        return okImage()
      },
    })
    for (const n of ['hero.png', 'hero2.png', 'bg.png', 'hdr.png']) {
      assert.ok(seen.includes('https://cdn.com/' + n), '没被抓取: ' + n + ' → ' + JSON.stringify(seen))
    }
    assert.ok(!/https:\/\/cdn\.com\//.test(out), 'HTML 里仍残留远程地址:\n' + out)
    assert.equal(out.match(/data:image\/png;base64,/g).length, 4)
  })

  it('纯背景图邮件（无 <img>）也能内联', async () => {
    const out = await preloadRemoteImages(
      '<div style="background:url(https://cdn.com/only.png)"></div>',
      { fetchImpl: async () => okImage() },
    )
    assert.ok(out.includes('data:image/png;base64,'), '背景图没有被内联: ' + out)
  })

  it('抓取失败时保留原 URL，不毁排版（回归）', async () => {
    const html = '<div style="background:url(https://cdn.com/bad.png)"></div>'
    const out = await preloadRemoteImages(html, {
      fetchImpl: async () => {
        throw new Error('network down')
      },
    })
    assert.ok(out.includes('https://cdn.com/bad.png'))
  })
})
