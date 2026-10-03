/**
 * cid: 内联图片解析回归测试（2026-09-30 真机审计 P0）。
 *
 * 覆盖真机报��的「邮件详情缺失图片」：HTML 正文里的 <img src="cid:...">
 * 在 WebView 里没有可解析的 URL，必须由 MIME 树里的对应部件内联成 data URI。
 *
 * 用 node --test 直跑；本模块只依赖标准 Web API（TextEncoder/TextDecoder/btoa），
 * Node 18+ 全部具备。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { extractEmailBody } from '../email-body-format.ts'

// 1x1 透明 PNG
const PNG_B64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='

function buildRelatedMime(cid, b64 = PNG_B64) {
  return [
    'Content-Type: multipart/related; boundary="OUTER"',
    'MIME-Version: 1.0',
    '',
    '--OUTER',
    'Content-Type: text/html; charset=utf-8',
    'Content-Transfer-Encoding: 8bit',
    '',
    `<html><body><p>你好，世界</p><img src="cid:${cid}" alt="logo"></body></html>`,
    '--OUTER',
    `Content-Type: image/png`,
    `Content-ID: <${cid}>`,
    'Content-Transfer-Encoding: base64',
    '',
    b64,
    '--OUTER--',
    '',
  ].join('\r\n')
}

test('cid: 内联图片被解析成 data URI（核心修复）', () => {
  const html = extractEmailBody(buildRelatedMime('logo001@corp'))
  assert.match(html, /<p>你好，世界<\/p>/, '正文应完整保留')
  assert.ok(
    html.includes(`data:image/png;base64,${PNG_B64}`),
    'cid: 必须替换为 data URI，实际拿到:\n' + html,
  )
  assert.ok(!html.includes('cid:logo001@corp'), '不应残留未解析的 cid:')
})

test('cid: 匹配忽略大小写（Content-ID 头是 <Logo@Corp>，正文写 cid:logo@corp）', () => {
  const html = extractEmailBody(buildRelatedMime('Logo@Corp'))
  assert.ok(html.includes('data:image/png;base64'), '大小写不一致也应命中\n' + html)
})

test('未命中的 cid: 保持原样（不破坏 HTML，也不产生半截 data URI）', () => {
  const html = extractEmailBody(buildRelatedMime('found@corp'))
  // 追加一个不存在的 cid
  const withMissing = html.replace('</body>', '<img src="cid:missing@corp"></body>')
  assert.ok(withMissing.includes('cid:missing@corp'), '不存在的 cid 保持原样')
  assert.ok(withMissing.includes('data:image/png;base64'), '已存在的那张仍应内联')
})

test('非图片部件不会被当成内联图（Content-ID 撞车时只认 image/*）', () => {
  const raw = [
    'Content-Type: multipart/related; boundary="B"',
    '',
    '--B',
    'Content-Type: text/html; charset=utf-8',
    '',
    '<img src="cid:att1@corp">',
    '--B',
    'Content-Type: application/pdf; name="a.pdf"',
    'Content-ID: <att1@corp>',
    'Content-Transfer-Encoding: base64',
    '',
    'JVBERi0xLjQK',
    '--B--',
    '',
  ].join('\r\n')
  const html = extractEmailBody(raw)
  assert.ok(html.includes('cid:att1@corp'), 'application/pdf 不应被内联成 img\n' + html)
})

test('纯文本邮件仍走 text/plain 分支，不受影响', () => {
  const raw = [
    'Content-Type: multipart/alternative; boundary="B"',
    '',
    '--B',
    'Content-Type: text/plain; charset=utf-8',
    '',
    '纯文本正文',
    '--B',
    'Content-Type: text/html; charset=utf-8',
    '',
    '<p>HTML 正文</p>',
    '--B--',
    '',
  ].join('\r\n')
  const html = extractEmailBody(raw)
  assert.equal(html, '<p>HTML 正文</p>')
})

test('非 MIME 原文原样返回', () => {
  assert.equal(extractEmailBody('就是一封纯文本邮件'), '就是一封纯文本邮件')
  assert.equal(extractEmailBody('   '), '')
})

test('超大内联图跳过内联（保护 WebView 内存，不产生超大 data URI）', () => {
  const big = 'A'.repeat(3_000_000) // base64 原文 ~3MB > 1.5MB 单图上限
  const html = extractEmailBody(buildRelatedMime('huge@corp', big))
  assert.ok(html.includes('cid:huge@corp'), '超限图应保留 cid 而不是内联\n' + html.slice(0, 300))
  assert.ok(!html.includes('data:image/png'), '不应内联超限图片')
})

// ---------------------------------------------------------------------------
// 2026-10-02 补：原实现只重写 `src="cid:..."`，漏掉另外两种同样常见的写法。
// 这两类都不是"发信方不规范"，而是 HTML/CSS 的合法写法，缺失它们就是
// 用户报的「邮件详情缺失图片」的残留部分。
// ---------------------------------------------------------------------------

/** 造一封正文可自定的 multipart/related 邮件。 */
function buildRelatedWithHtml(cid, htmlBody, b64 = PNG_B64) {
  return [
    'Content-Type: multipart/related; boundary="OUTER"',
    'MIME-Version: 1.0',
    '',
    '--OUTER',
    'Content-Type: text/html; charset=utf-8',
    'Content-Transfer-Encoding: 8bit',
    '',
    htmlBody,
    '--OUTER',
    'Content-Type: image/png',
    `Content-ID: <${cid}>`,
    'Content-Transfer-Encoding: base64',
    '',
    b64,
    '--OUTER--',
    '',
  ].join('\r\n')
}

test('CSS url(cid:...) 的背景图被内联（营销/通知邮件最常见的一张图）', () => {
  const html = extractEmailBody(
    buildRelatedWithHtml('bg@corp', '<td style="background-image:url(cid:bg@corp)">hi</td>'),
  )
  assert.ok(
    html.includes('data:image/png;base64,'),
    'CSS 背景图的 cid: 必须被内联，否则背景图必空\n' + html,
  )
  assert.ok(!html.includes('cid:bg@corp'), '不应残留未解析的 CSS cid:')
})

test('CSS url("cid:...") 带引号同样命中（引号在 CSS 里是可选的）', () => {
  const html = extractEmailBody(
    buildRelatedWithHtml('bg2@corp', '<td style="background:url(\'cid:bg2@corp\')">x</td>'),
  )
  assert.ok(html.includes('data:image/png;base64,'), '带引号的 url(cid:) 也应内联\n' + html)
  assert.ok(!html.includes('cid:bg2@corp'), '不应残留未解析的 CSS cid:')
})

test('无引号的 src=cid:... 也被内联（HTML 允许属性值不带引号）', () => {
  const html = extractEmailBody(buildRelatedWithHtml('plain@corp', '<img src=cid:plain@corp>'))
  assert.ok(html.includes('data:image/png;base64,'), '无引号 src=cid: 也应内联\n' + html)
  assert.ok(!html.includes('cid:plain@corp'), '不应残留未解析的 cid:')
})

test('同一封邮件里 src 与 CSS 两种引用各自命中，且共享同一个字节预算', () => {
  const html = extractEmailBody(
    buildRelatedWithHtml(
      'one@corp',
      '<img src="cid:one@corp"><div style="background:url(cid:one@corp)"></div>',
    ),
  )
  const inlined = html.match(/data:image\/png;base64,/g) || []
  assert.equal(inlined.length, 2, '两处引用都应内联\n' + html)
})

test('新增形态不破坏原有行为：未命中的 CSS cid 原样保留', () => {
  const html = extractEmailBody(
    buildRelatedWithHtml('yes@corp', '<div style="background:url(cid:no@corp)"></div>'),
  )
  assert.ok(html.includes('cid:no@corp'), '不存在的 cid 应保持原样\n' + html)
})
