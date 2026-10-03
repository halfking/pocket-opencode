/**
 * 正文字符集回归测试（2026-10-01 真机审计 P0：邮件正文乱码）。
 *
 * 乱码不是字体问题，而是解码层选错编码：国内企业邮箱至今仍用 GBK/GB2312 发信，
 * 此前 base64/QP 解码一律写死 utf-8，GBK 字节被替换成 U+FFFD（「锟斤拷」），
 * 之后再怎么设字体都救不回来——因为原始字节已经丢了。
 *
 * 本文件用**真实 GBK 字节**（不是「用 utf-8 编的中文」）构造报文，确保测的是
 * 真正的历史编码路径。用 node --test 直跑。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { extractEmailBody } from '../email-body-format.ts'
import { decodePartBytes, normalizeCharset, resolveCharsetEncoding } from '../email-charset.ts'

/**
 * GBK 固定字节夹具。
 *
 * 关键：Node 的 `Buffer.from(s,'gbk')` 并不存在（Buffer 只认 utf8/base64/hex），
 * 而这些字节也不能用 TextEncoder 造出来（它只支持 utf-8）。所以下面的 base64
 * 是用 .NET `Encoding.GetEncoding(936)`（真 GBK）离线编码后固化进来的——
 * 测的确实是历史编码路径，不是「用 utf-8 编的中文」那种假样本。
 *
 * 校验方式：每个夹具都注明了它应解码成的文本。
 */
const GBK = {
  // 发票已开具，请查收附件。
  invoiceNotice: 't6LGsdLRv6q+36Osx+uy6crVuL28/qGj',
  // 您的订单已发货。
  orderShipped: 'xPq1xLaptaXS0beiu/Whow==',
  // 会议纪要：本周完成发票系统联调。
  minutes: 'u+HS6bzN0qqjurG+1tzN6rPJt6LGsc+1zbPBqrX3oaM=',
  // 主题：报销单据已提交
  expenseTopic: '1vfM4qO6sajP+rWlvt3S0czhvbs=',
  // 第一行内容
  firstLine: 'tdrSu9DQxNrI3Q==',
  // <html><head><meta charset="gb2312"></head><body><p>增值税电子发票</p></body></html>
  htmlMeta: 'PGh0bWw+PGhlYWQ+PG1ldGEgY2hhcnNldD0iZ2IyMzEyIj48L2hlYWQ+PGJvZHk+PHA+1PbWtcuwtefX07eixrE8L3A+PC9ib2R5PjwvaHRtbD4=',
  // 您的发票开具成功
  invoiceOk: 'xPq1xLeixrG/qr7fs8m5pg==',
  // <html><body><p>发票请查收</p><img src="cid:inv1"></body></html>
  htmlCid: 'PGh0bWw+PGJvZHk+PHA+t6LGscfrsunK1TwvcD48aW1nIHNyYz0iY2lkOmludjEiPjwvYm9keT48L2h0bWw+',
  // 中文测试
  chinese: '1tDOxLLiytQ=',
}

/** base64 → 字节数组。 */
function bytes(b64) {
  return new Uint8Array(Buffer.from(b64, 'base64'))
}

/** base64 → latin1 字符串（模拟「原始字节被当 latin1 收进来」的 8bit 路径）。 */
function latin1Of(b64) {
  const b = Buffer.from(b64, 'base64')
  let s = ''
  for (const byte of b) s += String.fromCharCode(byte)
  return s
}

/** 断言不含替换字符——这是乱码的判定标准。 */
function assertNoMojibake(s, label) {
  const bad = (s.match(/�/g) || []).length
  assert.equal(bad, 0, `${label} 出现 ${bad} 个 U+FFFD 替换字符（乱码）`)
}

test('charset 标签别名归一化（GBK 系）', () => {
  assert.equal(resolveCharsetEncoding('GB2312'), 'gbk')
  assert.equal(resolveCharsetEncoding('gbk'), 'gbk')
  assert.equal(resolveCharsetEncoding('"GBK"'), 'gbk')
  assert.equal(resolveCharsetEncoding('GB_2312-80'), 'gbk')
  assert.equal(resolveCharsetEncoding('Big5'), 'big5')
  assert.equal(resolveCharsetEncoding('utf-8'), 'utf-8')
  assert.equal(resolveCharsetEncoding('nonsense-charset'), '')
  assert.equal(normalizeCharset('  "UTF-8"  '), 'utf-8')
})

test('base64 GBK 正文按声明 charset 解码，无乱码', () => {
  const raw = [
    'Content-Type: text/html; charset=GBK',
    'Content-Transfer-Encoding: base64',
    '',
    GBK.invoiceNotice,
  ].join('\r\n')

  const body = extractEmailBody(raw)
  assertNoMojibake(body, 'GBK base64 正文')
  assert.equal(body, '发票已开具，请查收附件。')
})

test('base64 GB2312 声明（GBK 子集）正常', () => {
  const raw = [
    'Content-Type: text/plain; charset=GB2312',
    'Content-Transfer-Encoding: base64',
    '',
    GBK.orderShipped,
  ].join('\r\n')
  const body = extractEmailBody(raw)
  assertNoMojibake(body, 'GB2312 正文')
  assert.equal(body, '您的订单已发货。')
})

test('声明 utf-8 实际 GBK（老系统谎报）能自动纠正', () => {
  // 头里写 utf-8，字节是 GBK——这是国内老系统的典型行为。
  const raw = [
    'Content-Type: text/plain; charset=utf-8',
    'Content-Transfer-Encoding: base64',
    '',
    GBK.minutes,
  ].join('\r\n')
  const body = extractEmailBody(raw)
  assertNoMojibake(body, '谎报 utf-8 的 GBK 正文')
  assert.equal(body, '会议纪要：本周完成发票系统联调。')
})

test('quoted-printable GBK 正文按 charset 解码', () => {
  // QP 里汉字以 =XX 逐字节表示，声明 GBK。
  const qp = Array.from(bytes(GBK.expenseTopic), (b) => '=' + b.toString(16).toUpperCase().padStart(2, '0')).join('')
  const raw = [
    'Content-Type: text/plain; charset=GBK',
    'Content-Transfer-Encoding: quoted-printable',
    '',
    qp,
  ].join('\r\n')
  const body = extractEmailBody(raw)
  assertNoMojibake(body, 'QP GBK 正文')
  assert.equal(body, '主题：报销单据已提交')
})

test('QP 软换行（=\\r\\n 折行）后的 GBK 仍正确', () => {
  const all = Array.from(bytes(GBK.firstLine), (b) => '=' + b.toString(16).toUpperCase().padStart(2, '0')).join('')
  const raw = [
    'Content-Type: text/plain; charset=GBK',
    'Content-Transfer-Encoding: quoted-printable',
    '',
    all.slice(0, 6) + '=\r\n' + all.slice(6),
  ].join('\r\n')
  const body = extractEmailBody(raw)
  assertNoMojibake(body, 'QP 软换行 GBK')
  assert.equal(body, '第一行内容')
})

test('真 UTF-8 正文不被误判改写', () => {
  const raw = [
    'Content-Type: text/plain; charset=utf-8',
    'Content-Transfer-Encoding: base64',
    '',
    Buffer.from('Invoice #A-1234 total ¥1280.00', 'utf8').toString('base64'),
  ].join('\r\n')
  assert.equal(extractEmailBody(raw), 'Invoice #A-1234 total ¥1280.00')
})

test('HTML <meta charset> 覆盖头部谎报（头 utf-8、文档内 GB2312）', () => {
  const raw = [
    'Content-Type: text/html; charset=utf-8',
    'Content-Transfer-Encoding: base64',
    '',
    GBK.htmlMeta,
  ].join('\r\n')
  const body = extractEmailBody(raw)
  assertNoMojibake(body, 'meta charset 纠正后的正文')
  assert.match(body, /增值税电子发票/)
})

test('8bit GBK 正文（无 transfer-encoding 混淆）按 charset 解', () => {
  const raw = [
    'Content-Type: text/plain; charset=GBK',
    'Content-Transfer-Encoding: 8bit',
    '',
    latin1Of(GBK.invoiceOk),
  ].join('\r\n')
  const body = extractEmailBody(raw)
  assertNoMojibake(body, '8bit GBK 正文')
  assert.equal(body, '您的发票开具成功')
})

test('GBK 正文 + cid 内联图同时正确（发票邮件典型结构）', () => {
  const png =
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='
  const raw = [
    'Content-Type: multipart/related; boundary="B"',
    'MIME-Version: 1.0',
    '',
    '--B',
    'Content-Type: text/html; charset=GBK',
    'Content-Transfer-Encoding: base64',
    '',
    GBK.htmlCid,
    '--B',
    'Content-Type: image/png',
    'Content-ID: <inv1>',
    'Content-Transfer-Encoding: base64',
    '',
    png,
    '--B--',
  ].join('\r\n')

  const body = extractEmailBody(raw)
  assertNoMojibake(body, 'GBK + cid 正文')
  assert.match(body, /发票请查收/)
  assert.match(body, /data:image\/png;base64,/, 'cid 图应内联为 data URI')
})

test('非 MIME 纯文本原样返回，不做多余解码', () => {
  assert.equal(extractEmailBody('就是一段纯文本正文'), '就是一段纯文本正文')
  assert.equal(extractEmailBody(''), '')
})

test('未知 charset 不抛错，退回 UTF-8（旧行为）', () => {
  const raw = [
    'Content-Type: text/plain; charset=x-unknown-charset',
    'Content-Transfer-Encoding: base64',
    '',
    Buffer.from('plain ascii body', 'utf8').toString('base64'),
  ].join('\r\n')
  assert.equal(extractEmailBody(raw), 'plain ascii body')
})

test('decodePartBytes 直接按 charset 解字节', () => {
  assert.equal(decodePartBytes(bytes(GBK.chinese), 'GBK'), '中文测试')
  assert.equal(decodePartBytes(bytes(GBK.chinese), 'gb18030'), '中文测试')
  // 故意按 utf-8 解 GBK 必然出替换字符——这正是修复前真机看到的现象。
  assertNoMojibake(decodePartBytes(bytes(GBK.chinese), 'utf-8'), '故意错解 utf-8')
})

