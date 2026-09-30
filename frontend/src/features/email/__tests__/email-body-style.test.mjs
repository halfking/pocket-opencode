/**
 * 正文字体归一化 + 远程图片预加载 回归测试（2026-10-01 真机审计 P0）。
 *
 * 覆盖两类真机现象：
 *   - 方框字/字重错乱：邮件 font-family 没有 CJK 兜底，汉字落到不含字的字体；
 *   - 图片一个个蹦 / 文字被顶下去：远程 <img> 在渲染时才发起请求。
 *
 * 纯字符串与注入 fetch，node --test 直跑。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import {
  EMAIL_FONT_STACK,
  injectBaseStyle,
  normalizeCssFonts,
  normalizeStyleBlocks,
  stripRemoteFonts,
} from '../email-body-style.ts'
import { collectRemoteImages, preloadRemoteImages } from '../email-image-preload.ts'

// ── 字体 ────────────────────────────────────────────────────────────────────

test('剥离远程 @import 与 @font-face（避免外链字体拖慢/失败）', () => {
  const css = `
    @import url("https://fonts.example.com/roboto.css");
    @font-face { font-family: 'Brand'; src: url(https://cdn.example.com/b.woff2) format('woff2'); }
    body { color: #333; }
  `
  const out = stripRemoteFonts(css)
  assert.ok(!out.includes('@import'), '@import 应被移除')
  assert.ok(!out.includes('@font-face'), '@font-face 应被移除')
  assert.ok(!out.includes('cdn.example.com'), '不应残留外链 URL')
  assert.match(out, /color:\s*#333/, '无关声明应保留')
})

test('font-family 追加 CJK 兜底栈', () => {
  const out = normalizeCssFonts('p { font-family: Helvetica, Arial, sans-serif; }')
  assert.match(out, /Helvetica, Arial, sans-serif/, '保留邮件原始字体')
  assert.ok(out.includes('PingFang SC'), '应追加 CJK 兜底')
  assert.ok(out.includes('Microsoft YaHei'), '应追加安卓可用的中文字体')
})

test('font-family 的 !important 不会压掉兜底', () => {
  const out = normalizeCssFonts('p { font-family: Arial !important; }')
  assert.ok(!/!important/.test(out), '追加兜底后必须清掉 !important')
  assert.ok(out.includes('PingFang SC'))
})

test('等宽字体追加 CJK 等宽栈', () => {
  const out = normalizeCssFonts('code { font-family: Consolas; }')
  assert.ok(out.includes('Noto Sans Mono CJK SC'), '等宽也要有中文兜底')
})

test('已有 CJK 字体的声明不重复追加', () => {
  const css = 'p { font-family: "Microsoft YaHei", sans-serif; }'
  const out = normalizeCssFonts(css)
  assert.ok(!out.includes('PingFang SC'), '已含中文字体则不重复追加')
})

test('<style> 块被逐个归一化，块外内容不动', () => {
  const html = '<p>保留</p><style>p{font-family:Arial}</style><div>也保留</div>'
  const out = normalizeStyleBlocks(html)
  assert.match(out, /<p>保留<\/p>/)
  assert.match(out, /<div>也保留<\/div>/)
  assert.ok(out.includes('PingFang SC'), 'style 内应被归一化')
})

test('基准样式注入：有 head 插 head，有 style 则前置', () => {
  const withHead = injectBaseStyle('<html><head><title>t</title></head><body>x</body></html>')
  assert.match(withHead, /<head><style>/, '应插在 head 开头')

  const withStyle = injectBaseStyle('<html><head><style>p{color:red}</style></head></html>')
  assert.ok(
    withStyle.indexOf('base') < withStyle.lastIndexOf('<style'),
    '基准样式应在邮件自身 style 之前',
  )
  assert.match(withStyle, /max-width:100%/, '应含图片不溢出的兜底')
})

test('基准样式含图片最大宽度约束（防窄屏横向溢出）', () => {
  const out = injectBaseStyle('<div>x</div>')
  assert.ok(out.includes('img{max-width:100%'))
  assert.ok(out.includes(EMAIL_FONT_STACK.split(',')[0].trim()))
})

// ── 远程图片预加载 ─────────────────────────────────────────────────────────

/** 造一个可注入的 fetch：按 URL 返回指定字节/类型。 */
function stubFetch(map, calls = []) {
  return async (url) => {
    calls.push(url)
    const entry = map[url]
    if (!entry) return { ok: false, status: 404, headers: { get: () => null } }
    return {
      ok: true,
      status: 200,
      headers: { get: (h) => (h.toLowerCase() === 'content-type' ? entry.type : null) },
      arrayBuffer: async () => entry.bytes.buffer.slice(entry.bytes.byteOffset, entry.bytes.byteOffset + entry.bytes.byteLength),
    }
  }
}

const PNG = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01])

test('收集远程图片 URL 并去重保序', () => {
  const html = `
    <img src="https://a.example.com/1.png">
    <img src='https://b.example.com/2.png'>
    <img src="https://a.example.com/1.png">
    <img src="data:image/png;base64,AAAA">
    <img src="cid:x@y">
  `
  const out = collectRemoteImages(html)
  assert.deepEqual(out, ['https://a.example.com/1.png', 'https://b.example.com/2.png'])
})

test('远程图片被预加载内联为 data URI', async () => {
  const calls = []
  const html = '<p>hi</p><img src="https://a.example.com/1.png">'
  const out = await preloadRemoteImages(html, {
    fetchImpl: stubFetch({ 'https://a.example.com/1.png': { type: 'image/png', bytes: PNG } }, calls),
  })
  assert.match(out, /data:image\/png;base64,/, '应内联为 data URI')
  assert.ok(!out.includes('https://a.example.com/1.png'), '原 URL 应被替换')
  assert.match(out, /<p>hi<\/p>/, '正文不受影响')
  assert.deepEqual(calls, ['https://a.example.com/1.png'], '应真的发起了一次抓取')
})

test('单引号 src 也能替换', async () => {
  const html = "<img src='https://a.example.com/1.png'>"
  const out = await preloadRemoteImages(html, {
    fetchImpl: stubFetch({ 'https://a.example.com/1.png': { type: 'image/png', bytes: PNG } }),
  })
  assert.match(out, /data:image\/png;base64,/)
})

test('抓取失败的图保留原 URL，不影响其余图与排版', async () => {
  const calls = []
  const html = '<img src="https://ok.example.com/1.png"><img src="https://bad.example.com/2.png">'
  const out = await preloadRemoteImages(html, {
    fetchImpl: stubFetch({ 'https://ok.example.com/1.png': { type: 'image/png', bytes: PNG } }, calls),
  })
  assert.match(out, /data:image\/png;base64,/, '成功图内联')
  assert.ok(out.includes('https://bad.example.com/2.png'), '失败图保留原 URL')
})

test('非图片 content-type 不内联（避免把 HTML 错误页当图）', async () => {
  const out = await preloadRemoteImages('<img src="https://a.example.com/x">', {
    fetchImpl: stubFetch({ 'https://a.example.com/x': { type: 'text/html', bytes: new Uint8Array([1, 2]) } }),
  })
  assert.ok(out.includes('https://a.example.com/x'), 'HTML 响应不应被内联')
})

test('超过单图上限不内联（防 WebView 内存爆）', async () => {
  const big = new Uint8Array(2_000_001)
  const out = await preloadRemoteImages('<img src="https://a.example.com/big.png">', {
    fetchImpl: stubFetch({ 'https://a.example.com/big.png': { type: 'image/png', bytes: big } }),
  })
  assert.ok(out.includes('https://a.example.com/big.png'), '超大图应跳过内联')
})

test('并发受限：不超过 CONCURRENCY 同时在飞', async () => {
  let inFlight = 0
  let peak = 0
  const urls = Array.from({ length: 12 }, (_, i) => `https://a.example.com/${i}.png`)
  const fetchImpl = async (url) => {
    inFlight++
    peak = Math.max(peak, inFlight)
    await new Promise((r) => setTimeout(r, 5))
    inFlight--
    return {
      ok: true,
      status: 200,
      headers: { get: () => 'image/png' },
      arrayBuffer: async () => PNG.buffer,
    }
  }
  const html = urls.map((u) => `<img src="${u}">`).join('')
  await preloadRemoteImages(html, { fetchImpl })
  assert.ok(peak <= 4, `并发峰值应 ≤4，实际 ${peak}`)
  assert.ok(peak > 1, '应确实并发（否则预加载没意义）')
})

test('无远程图时零请求直接返回', async () => {
  let called = 0
  const out = await preloadRemoteImages('<img src="data:image/png;base64,AA"><img src="cid:a@b">', {
    fetchImpl: async () => {
      called++
      throw new Error('不应被调用')
    },
  })
  assert.equal(called, 0)
  assert.match(out, /data:image\/png;base64,AA/)
})

test('img 数量超上限时只处理前 N 张', async () => {
  const calls = []
  const urls = Array.from({ length: 10 }, (_, i) => `https://a.example.com/${i}.png`)
  const html = urls.map((u) => `<img src="${u}">`).join('')
  await preloadRemoteImages(html, {
    fetchImpl: stubFetch(
      Object.fromEntries(urls.map((u) => [u, { type: 'image/png', bytes: PNG }])),
      calls,
    ),
    maxImages: 4,
  })
  assert.equal(calls.length, 4, `应只抓 4 张，实际 ${calls.length}`)
})
