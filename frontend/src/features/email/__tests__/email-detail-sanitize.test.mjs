/**
 * 邮件 HTML 净化边界（需求 7「邮件窗口查看各类邮件」的安全面）。
 *
 * ## 为什么这个文件存在
 *
 * `sanitizeEmailHtml` 是**整个邮件功能里唯一把不可信输入（发件人可控的 HTML
 * 正文）变成可进 v-html 的东西**的函数。它此前**一条测试都没有**，而且当时
 * 也**测不了**：`dompurify` 在没有 DOM 的环境里 `isSupported === false`，
 * 默认导出甚至不是一个可用的 purify 实例（`DOMPurify.sanitize is not a
 * function`）。所以本文件用 jsdom 把 DOM 装起来再动态 import 被测模块。
 *
 * 如果没有这一步，任何「给净化器补个测试」的尝试都会在 Node 里直接抛
 * TypeError，于是大家自然就绕过它不去测——而这正是最不该没有护栏的地方。
 *
 * ## 每条断言的依据
 *
 * 断言写的是 **2026-10-03 在 DOMPurify 3.4.11 + jsdom 30 下实测到的行为**，
 * 不是从配置里推出来的。配置里写了不等于实际生效——本文件下面三条带
 * 「实测与配置意图不符」注释的用例就是证据：
 *
 *  1. `ALLOWED_TAGS` 明确列了 `'style'`，`email-detail-format.ts` 的模块注释
 *     还专门论证了「必须先注入基准样式再净化」，但 `<style>` **照样被剥掉**，
 *     于是 `injectBaseStyle` / `normalizeStyleBlocks` / `baseEmailStyle` 整条
 *     链路是**死的**（见 styleIsStripped 一节）。
 *  2. `ALLOWED_URI_REGEXP` 只放行 `data:image/...`，但 DOMPurify 对
 *     img/audio/video/source/track 这几个标签另有一条「data: 一律放行」的
 *     内建豁免，于是 `data:text/html` 能进 `<img src>`（见 dataUriEscapeHatch）。
 *  3. `stripRemoteFonts` 只管 `@font-face`，而 `style="background:url(...)"`
 *     是一条**独立于 `<img>` 的远程请求通道**，`preloadRemoteImages` 也不覆盖它
 *     （见 cssUrlIsARemoteFetchChannel）。
 *
 * 第 1、3 条目前对用户没有可感知的损害（前者是外观，后者是隐私面而非崩溃），
 * 如实标注，不夸大成「安全漏洞」。第 2 条在现代引擎里 `<img src=data:text/html>`
 * 不会当文档渲染，同样不构成 XSS。
 */

// jsdom 必须先装好，dompurify 在 import 期就要 window。
import { JSDOM } from 'jsdom'
import test from 'node:test'
import assert from 'node:assert/strict'

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'https://localhost/' })
for (const key of [
  'window', 'document', 'Node', 'Element', 'NodeFilter', 'NodeList',
  'HTMLTemplateElement', 'HTMLFormElement', 'DOMParser', 'NamedNodeMap', 'trustedTypes',
]) {
  if (dom.window[key] !== undefined) globalThis[key] = dom.window[key]
}
globalThis.window = dom.window
globalThis.document = dom.window.document

const { sanitizeEmailHtml } = await import('../email-detail-format.ts')
const { injectBaseStyle } = await import('../email-body-style.ts')
const { preloadRemoteImages } = await import('../email-image-preload.ts')

// ---------------------------------------------------------------------------
// 哨兵：先证明「净化器真的在干活」
// ---------------------------------------------------------------------------
//
// 这一节是整个文件的地基。如果 jsdom 哪天没装成功、或者 dompurify 换了版本
// 变成不支持，DOMPurify 会退化成原样返回，上面那些「危险标签被剥掉」的断言
// 就会**全部变成永真**、静悄悄地全绿。所以必须先钉住「它有删东西的能力」。
test('哨兵：净化器确实在删东西（否则下面所有断言都是永真）', () => {
  assert.equal(typeof globalThis.window, 'object', 'jsdom 的 window 必须就位')
  const out = sanitizeEmailHtml('<p>a</p><script>alert(1)</scr' + 'ipt>')
  assert.equal(out, '<p>a</p>', 'script 标签必须被剥掉；若这里原样返回，说明净化器没生效')
})

// ---------------------------------------------------------------------------
// 基本攻击面：必须被剥掉
// ---------------------------------------------------------------------------
test('script 标签被剥掉', () => {
  assert.equal(sanitizeEmailHtml('<p>a</p><script>alert(1)</scr' + 'ipt>'), '<p>a</p>')
})

test('img 的 onerror 被剥掉，且大小写不敏感', () => {
  const out = sanitizeEmailHtml('<IMG SRC="https://a.example/1.png" ONERROR="alert(1)">')
  assert.equal(out, '<img src="https://a.example/1.png">')
  assert.ok(!/onerror/i.test(out))
})

test('javascript: 链接被剥掉', () => {
  const out = sanitizeEmailHtml('<a href="javascript:alert(1)">x</a>')
  assert.equal(out, '<a>x</a>')
})

test('iframe / object / embed / form / input 全部被剥掉', () => {
  assert.equal(sanitizeEmailHtml('<iframe src="https://evil.example/"></iframe>'), '')
  assert.equal(
    sanitizeEmailHtml('<object data="https://evil.example/x"></object><embed src="https://evil.example/y">'),
    '',
  )
  // form 标签本身剥掉，但内部文字留下（不整段吞掉）
  assert.equal(
    sanitizeEmailHtml('<form action="https://evil.example"><input name="p"><button>go</button></form>'),
    'go',
  )
})

test('meta refresh 被剥掉（不能靠它做跳转）', () => {
  assert.equal(sanitizeEmailHtml('<meta http-equiv="refresh" content="0;url=https://evil.example">'), '')
})

test('svg 内嵌 script 被剥掉', () => {
  assert.equal(sanitizeEmailHtml('<svg><script>alert(1)</scr' + 'ipt></svg>'), '')
})

test('data-* 属性被剥掉（ALLOW_DATA_ATTR: false）', () => {
  assert.equal(sanitizeEmailHtml('<div data-x="1">t</div>'), '<div>t</div>')
})

test('a 上的 target 被剥掉（开新窗由 rel=noopener 语义兜底）', () => {
  assert.equal(sanitizeEmailHtml('<a href="https://ok.example" target="_blank">x</a>'), '<a href="https://ok.example">x</a>')
})

test('cid: 图源被剥掉（cid 内联应在更早的解析阶段转成 data:）', () => {
  assert.equal(sanitizeEmailHtml('<img src="cid:logo@exmail.qq.com">'), '<img>')
})

test('不像 HTML 的输入渲染为空（宁可空白也不当 HTML 塞进 v-html）', () => {
  assert.equal(sanitizeEmailHtml('hello < world and 3 < 5'), '')
  assert.equal(sanitizeEmailHtml(''), '')
})

// ---------------------------------------------------------------------------
// 实测与配置意图不符的三处：钉住**当前真实行为**，别让它们悄悄变
// ---------------------------------------------------------------------------

// 1) <style> 被剥掉 → injectBaseStyle 整条链路是死的
test('实测：<style> 被剥掉，即使 ALLOWED_TAGS 显式列了 style', () => {
  const out = sanitizeEmailHtml('<style>.x{color:red}</style><p class="x">hi</p>')
  assert.equal(out, '<p class="x">hi</p>', '<style> 必须被剥掉；这条断言是「现状钉子」，不是「期望」')
  assert.ok(!out.includes('<style'))
})

test('实测：injectBaseStyle 注入的基准样式在净化后不留痕迹（该函数当前无效）', () => {
  // 先确认注入这一步本身是有产出的——否则「净化后没了」可能只是注入本来就没干活。
  const injected = injectBaseStyle('<p>hi</p>')
  assert.ok(injected.includes('<style'), 'injectBaseStyle 必须真的插入了 <style>，否则下面这行没有意义')
  const sanitized = sanitizeEmailHtml(injected)
  assert.ok(!sanitized.includes('<style'), '净化后 <style> 消失 —— 这就是当前实测行为')
  assert.ok(!sanitized.includes('font-family'), '基准字体族也随之消失，邮件正文拿不到 CJK 兜底字体')
})

// 2) data: 的内建豁免 —— ALLOWED_URI_REGEXP 拦不住 img 上的 data:text/html
test('实测：img 上的 data:text/html 不会被 ALLOWED_URI_REGEXP 拦下（DOMPurify 内建豁免）', () => {
  const out = sanitizeEmailHtml('<img src="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">')
  assert.ok(
    out.includes('data:text/html'),
    '实测它能穿过 ALLOWED_URI_REGXP；现代引擎在 <img> 上下文不渲染它，故不构成 XSS，但配置意图确实没兑现',
  )
  // 对照组：非图片标签的 data: 仍被拦下（豁免只针对 img/audio/video/source/track）
  const a = sanitizeEmailHtml('<a href="data:text/html;base64,AAAA">x</a>')
  assert.ok(!a.includes('data:text/html'), 'a 标签上的 data: 应被 ALLOWED_URI_REGEXP 拦下')
})

// 3) CSS url() 是独立于 <img> 的远程请求通道
test('实测：style 里的 background:url(https://…) 原样保留——一条远程请求通道', () => {
  const out = sanitizeEmailHtml('<div style="background:url(https://track.example.com/bg.png)">bg</div>')
  assert.ok(out.includes('https://track.example.com/bg.png'), 'CSS url() 不会被净化掉')

  // 关键含义：即使日后给 <img> 加了「默认不加载远程图」，这条通道依然在。
  // 所以只改 preloadRemoteImages 不构成完整修复。
  assert.ok(
    !out.includes('data:image'),
    '它不是 data: 内联图，preloadRemoteImages 的采集正则只认 <img src=...>，覆盖不到它',
  )
})

// ---------------------------------------------------------------------------
// 远程请求面：钉住「打开一封邮件会向哪些主机发请求」这件事的现状
// ---------------------------------------------------------------------------
test('实测：净化后 <img> 的远程图仍会随后被 preloadRemoteImages 抓走', async () => {
  const sanitized = sanitizeEmailHtml('<p>t</p><img src="https://track.example.com/px.gif">')
  assert.ok(sanitized.includes('https://track.example.com/px.gif'), '净化不拦 http(s) 图源')

  const calls = []
  await preloadRemoteImages(sanitized, {
    fetchImpl: async (url) => {
      calls.push(url)
      return {
        ok: true,
        status: 200,
        headers: { get: () => 'image/gif' },
        arrayBuffer: async () => new Uint8Array([1, 2, 3]).buffer,
      }
    },
  })
  assert.deepEqual(calls, ['https://track.example.com/px.gif'], '打开详情页就会真的向该主机发一次请求')
})

test('实测：非图片类的远程资源（<a href>）净化后保留，但不会被自动请求', async () => {
  const sanitized = sanitizeEmailHtml('<a href="https://tracker.example/collect">click</a>')
  const calls = []
  await preloadRemoteImages(sanitized, {
    fetchImpl: async (url) => {
      calls.push(url)
      throw new Error('不该被调用')
    },
  })
  assert.deepEqual(calls, [], '链接要用户点才发请求，不构成自动追踪')
})
