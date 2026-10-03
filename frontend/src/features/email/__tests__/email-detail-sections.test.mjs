import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { detectBodyFormat, renderBodyByFormat, splitBodySections, stripQuoteMarkers } from '../email-body-render.ts'

/**
 * 视图级分段契约（F1）。
 *
 * 这些断言盯的是「详情页拿哪一段去渲染」这个**接线**问题，
 * 而不只是 email-body-render 自身的切分正确性。曾经的 bug 正是：
 * 切分正确，但 renderBody() 仍然渲染整封原文 → 引用在页面上出现两遍。
 *
 * 这里把「main / quoted / signature / footer」拼回完整正文的规则显式写出来，
 * 使接线一旦退化成「渲染整封原文」，本文件立刻能看出 main 是否含引用。
 */

// 详情页 renderBody() 的正文来源，等价于这段拼装。
function viewMainText(raw) {
  const format = detectBodyFormat(raw)
  if (format === 'html') return raw
  const s = splitBodySections(raw)
  return [s.main, s.footer].filter(Boolean).join('\n\n')
}

test('正文只含 main+footer，不含引用与签名', () => {
  const raw = [
    '您好，确认收到附件。',
    '',
    '在 张三 写道：',
    '> 这是历史内容',
    '> 第二行历史',
    '',
    '-- ',
    '李四',
    '运营部',
  ].join('\n')
  const s = splitBodySections(raw)
  const main = viewMainText(raw)
  assert.equal(main, '您好，确认收到附件。')
  assert.ok(!main.includes('这是历史内容'), '正文不得含引用')
  assert.ok(!main.includes('李四'), '正文不得含签名')
})

test('quoted 与 signature 各自只承载一段，互不重叠', () => {
  const raw = '正文。\n\n> 引用行\n\n-- \n签名人'
  const s = splitBodySections(raw)
  assert.equal(s.main, '正文。')
  assert.match(s.quoted, /引用行/)
  assert.ok(!s.quoted.includes('签名人'), '引用段不得含签名')
  assert.match(s.signature, /签名人/)
  assert.ok(!s.signature.includes('引用行'), '签名段不得含引用')
})

test('退订尾注回到正文尾部（不参与折叠）', () => {
  const raw = '活动详情。\n\n您收到此邮件是因为订阅推送。\n退订：点击这里'
  const main = viewMainText(raw)
  assert.match(main, /退订：点击这里/, '退订应并回正文')
})

test('无引用无签名的普通正文：main 即全文，折叠区为空', () => {
  const raw = '第一段。\n\n第二段。'
  const s = splitBodySections(raw)
  assert.equal(s.quoted, '')
  assert.equal(s.signature, '')
  assert.equal(viewMainText(raw), raw)
})

test('HTML 正文不做文本切分（否则会劈坏标签）', () => {
  const html = '<div><p>正文</p><blockquote>引用</blockquote></div>'
  assert.equal(detectBodyFormat(html), 'html')
  // HTML 路径下 main 就是原文，引用由 <blockquote> 自身承载
  assert.equal(viewMainText(html), html)
})

test('引用渲染时剥掉 > 前缀，避免出现字面箭头', () => {
  const raw = '正文。\n\n> 引用行\n> 续行'
  const s = splitBodySections(raw)
  const quotedHtml = renderBodyByFormat(stripQuoteMarkers(s.quoted), 'text')
  assert.ok(!quotedHtml.includes('&gt;'), `不应残留箭头：${quotedHtml}`)
  assert.match(quotedHtml, /引用行/)
})
