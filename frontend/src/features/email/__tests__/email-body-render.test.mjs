import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  detectBodyFormat,
  looksLikeMarkdown,
  renderBodyByFormat,
  renderMarkdownBody,
  renderTextBody,
  splitBodySections,
  stripQuoteMarkers,
} from '../email-body-render.ts'

// ── 格式探测 ────────────────────────────────────────────────────────────────

test('HTML 优先判为 html（即使里面混了 Markdown 记号）', () => {
  assert.equal(detectBodyFormat('<div><p>Hello</p></div>'), 'html')
  // 营销模板里常有 **加粗** 混排，不能因此把真 HTML 当 Markdown。
  assert.equal(detectBodyFormat('<table><tr><td>**重要**</td></tr></table>'), 'html')
  assert.equal(detectBodyFormat('<br/>'), 'html')
  assert.equal(detectBodyFormat(''), 'text')
})

test('Markdown 需强特征或两条弱特征，避免把普通邮件当 Markdown', () => {
  // 强特征：围栏代码块
  assert.equal(detectBodyFormat('说明：\n```\nfoo\n```'), 'markdown')
  // 强特征：ATX 标题
  assert.equal(detectBodyFormat('# 订单确认\n\n内容'), 'markdown')
  // 强特征：表格
  assert.equal(detectBodyFormat('| 项目 | 金额 |\n| --- | --- |\n| A | 1 |'), 'markdown')
  // 单条弱特征不足以定性
  assert.equal(detectBodyFormat('这是普通邮件 - 带个破折号而已。'), 'text')
  // 两条弱特征才够
  assert.equal(looksLikeMarkdown('- 第一项\n- 第二项'), true)
  assert.equal(looksLikeMarkdown('看这个 `代码` 就行'), false)
})

test('looksLikeMarkdown: 空串与空白不误判', () => {
  assert.equal(looksLikeMarkdown(''), false)
  assert.equal(looksLikeMarkdown('   \n  '), false)
})

// ── 纯文本渲染 ──────────────────────────────────────────────────────────────

test('纯文本按空行切段，软换行留在同一段内以 <br> 呈现', () => {
  const html = renderTextBody('第一行\n第二行\n\n第二段')
  // 软换行必须留在同一段里（QP 会按 76 列硬折，按单换行切会碎成两段）；
  // 呈现上用 <br> 保住原有换行，而不是吞掉。
  assert.match(html, /<p>第一行<br>第二行<\/p>/)
  assert.equal((html.match(/<p>/g) || []).length, 2)
})

test('纯文本：缩进块渲染为 <pre> 保住对齐', () => {
  const html = renderTextBody('    项目      金额\n    订阅费     ¥30')
  assert.match(html, /<pre>/)
})

test('纯文本：项目符号与有序列表正确成 <ul>/<ol>', () => {
  assert.match(renderTextBody('- 甲\n- 乙'), /<ul><li>甲<\/li><li>乙<\/li><\/ul>/)
  assert.match(renderTextBody('1. 甲\n2. 乙'), /<ol><li>甲<\/li><li>乙<\/li><\/ol>/)
  assert.match(renderTextBody('• 甲\n• 乙'), /<ul>/)
})

test('纯文本：裸 URL 自动成链且 href 正确', () => {
  const html = renderTextBody('详见 https://example.com/a?b=1 谢谢')
  assert.match(html, /<a href="https:\/\/example\.com\/a\?b=1"[^>]*>https:\/\/example\.com\/a\?b=1<\/a>/)
  // www. 前缀要补 https
  assert.match(renderTextBody('见 www.example.com'), /href="https:\/\/www\.example\.com"/)
})

test('纯文本：HTML 特殊字符被转义（净化是第二道，这里是第一道）', () => {
  const html = renderTextBody('<script>alert(1)</script> & "引号"')
  assert.match(html, /&lt;script&gt;/)
  assert.ok(!html.includes('<script>'))
  assert.match(html, /&amp;/)
})

// ── Markdown 渲染 ───────────────────────────────────────────────────────────

test('Markdown：标题/列表/强调/行内代码/围栏代码', () => {
  const md = '# 标题\n\n正文 **粗体** 和 `code`\n\n- 一\n- 二\n\n```\nplain\n```'
  const html = renderMarkdownBody(md)
  assert.match(html, /<h1>标题<\/h1>/)
  assert.match(html, /<strong>粗体<\/strong>/)
  assert.match(html, /<code>code<\/code>/)
  assert.match(html, /<ul><li>一<\/li><li>二<\/li><\/ul>/)
  assert.match(html, /<pre><code>plain<\/code><\/pre>/)
})

test('Markdown：围栏代码内的 ** 不被当强调', () => {
  const html = renderMarkdownBody('```\n**not bold**\n```')
  assert.match(html, /<pre><code>\*\*not bold\*\*<\/code><\/pre>/)
  assert.ok(!html.includes('<strong>'))
})

test('Markdown：链接渲染且不注入属性', () => {
  const html = renderMarkdownBody('[点我](https://example.com/x)')
  assert.match(html, /<a href="https:\/\/example\.com\/x" rel="noopener noreferrer">点我<\/a>/)
  // 危险协议不得变成可点链接
  const evil = renderMarkdownBody('[x](javascript:alert(1))')
  assert.ok(!evil.includes('href="javascript:'))
})

test('Markdown：表格渲染出 thead/tbody', () => {
  const html = renderMarkdownBody('| 项目 | 金额 |\n| --- | --- |\n| 订阅 | ¥30 |')
  assert.match(html, /<table><thead><tr><th>项目<\/th><th>金额<\/th><\/tr><\/thead>/)
  assert.match(html, /<td>订阅<\/td>/)
})

test('Markdown：引用块嵌套渲染', () => {
  const html = renderMarkdownBody('> 引用一行\n> 第二行')
  assert.match(html, /<blockquote>/)
  assert.match(html, /引用一行/)
})

test('Markdown：原始 HTML 被转义（不引第三方库的窄输出面）', () => {
  const html = renderMarkdownBody('文字 <img src=x onerror=alert(1)> 结束')
  assert.ok(!html.includes('<img'))
  assert.match(html, /&lt;img/)
})

// ── 分段切分 ────────────────────────────────────────────────────────────────

test('切分：Outlook 中文「在 X 写道：」识别为引用起点', () => {
  const text = '收到，谢谢。\n\n在张三 写道：\n> 原始内容\n> 第二行'
  const s = splitBodySections(text)
  assert.equal(s.main, '收到，谢谢。')
  assert.match(s.quoted, /在张三 写道/)
  assert.match(s.quoted, /原始内容/)
})

test('切分：英文「On ... wrote:」与「-----Original Message-----」', () => {
  const a = splitBodySections('Thanks!\n\nOn Mon, Jan 1, 2024 at 10:00 AM John <j@x.com> wrote:\n> hi')
  assert.equal(a.main, 'Thanks!')
  assert.match(a.quoted, /wrote:/)
  const b = splitBodySections('正文内容\n\n-----Original Message-----\nFrom: someone')
  assert.equal(b.main, '正文内容')
  assert.match(b.quoted, /Original Message/)
})

test('切分：连续 > 行也算引用起点', () => {
  const s = splitBodySections('好的\n> 历史一\n> 历史二')
  assert.equal(s.main, '好的')
  assert.match(s.quoted, /历史一/)
})

test('切分：签名档从「--」之后开始，不误吃正文', () => {
  const s = splitBodySections('正文第一段。\n\n-- \n张三\n产品部')
  assert.equal(s.main, '正文第一段。')
  assert.match(s.signature, /张三/)
})

test('切分：退订尾注单独成段', () => {
  const s = splitBodySections('活动详情...\n\n您收到此邮件是因为订阅了我们的推送。\n退订：点击这里')
  assert.equal(s.main, '活动详情...')
  assert.match(s.footer, /退订/)
})

test('切分：正文中间出现的「--」不被当成签名（不吞正文）', () => {
  const s = splitBodySections('第一段。\n\n--\n\n第二段。\n\n第三段。')
  // 签名/退注必须真在尾部，占比过半的命中一律放弃
  assert.match(s.main, /第二段/)
  assert.match(s.main, /第三段/)
})

test('切分：无引用无签名时 main 就是全文', () => {
  const s = splitBodySections('一\n二\n三')
  assert.equal(s.main, '一\n二\n三')
  assert.equal(s.quoted, '')
  assert.equal(s.signature, '')
  assert.equal(s.footer, '')
})

test('切分：空输入不炸', () => {
  const s = splitBodySections('   \n  ')
  assert.equal(s.main, '')
})

test('stripQuoteMarkers 剥掉 > 前缀', () => {
  assert.equal(stripQuoteMarkers('> 甲\n> 乙'), '甲\n乙')
  assert.equal(stripQuoteMarkers('无引用'), '无引用')
})

// ── 分派 ────────────────────────────────────────────────────────────────────

test('renderBodyByFormat: html 原样返回（净化交给 DOMPurify）', () => {
  const html = '<div>hi</div>'
  assert.equal(renderBodyByFormat(html, 'html'), html)
})

test('renderBodyByFormat: 不传格式则自动探测', () => {
  assert.match(renderBodyByFormat('# 标题'), /<h1>/)
  assert.match(renderBodyByFormat('普通正文。'), /<p>普通正文。<\/p>/)
})
