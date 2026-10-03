import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { extractEmailBody } from '../email-body-format.ts'

// 护栏：头字段必须行首锚定（2026-10-03 真实数据，49 封里 23 封命中）。
//
// 这里的夹具逐字取自真实报文（data/email-bodies-raw 解密所得）里 QQ 的
// X-QQ-XMRINFO 追踪头：它把一串字段名**当作值**写在同一行，于是
// `/content-type\s*:\s*([^\s;]+)/i` 抓到的是 `list-unsubscribe` 而不是真正的
// `multipart/alternative`。顶层类型读错 ⇒ 不递归切分 ⇒ 找不到 text 部件
// ⇒ extractEmailBody 落到 return src ⇒ 详情页首屏是 Received: + 成百行
// base64 追踪噪声 + 整片 =E7=82=B9 的 QP 未解码正文。
//
// 断言的是**协议头不许出现在正文里**，而不是「恰好等于某个字符串」——
// 后者在有人把 stripMessageHeader 改成更激进时仍会绿，而那正是回归的形态。

// 夹具形态必须与真实报文一致，否则护栏会「因为别的原因」通过（负控实测踩过）。
//
// 第一版把伪字段名和真正的 Content-Type 分成两行，结果**还原修复后护栏依然全绿**。
// 原因：`/content-type\s*:\s*/` 里的 `\s` 含 `\r\n`，所以旧正则跨行时会把下一行
// 真正的 `Content-Type: multipart/alternative` 一并吞掉，恰好命中正确值。
// 真实邮件里出问题，是因为伪字段名后面跟的是**同一行内的下一个字段名**
// （`content-type : list-unsubscribe : from : ...`），正则只能吃到紧邻的那个。
// 所以这里让伪字段名与真正头**同行**，且中间夹一个别的字段名。
const TRACKER_HEAD = [
  'X-QQ-XMRINFO: h=date : from :\r\n reply-to : to : message-id : subject : mime-version :\r\n content-type : list-unsubscribe : from : list-unsubscribe-post :\r\n list-unsub=0',
  'MIME-Version: 1.0',
  'Content-Type: multipart/alternative; boundary="----=_Part_190350_1625789058.1788566899903"',
  '',
].join('\r\n')

// 同行的极端形态：伪字段名直接顶掉 content-type，且后面没有任何真头能救回来。
const INLINE_SPURIOUS = [
  'X-QQ-XMRINFO: h=date : from : to : subject : mime-version : content-type : reply-to :\r\n sender : cc',
  'MIME-Version: 1.0',
  'Content-Type: multipart/mixed; boundary="BND4"',
  '',
].join('\r\n')

// QP 正文里放中文字节，确保「解码成功」与「原样透传」在断言上可区分。
const QP_HTML = [
  '<!DOCTYPE html><html lang=3D"en"><head><meta charset=3D"UTF-8"></head>',
  '<body><p>=E7=82=B9=E5=87=BB=E8=BF=99=E9=87=8C=E5=8F=96=E6=B6=88=E8=AE=A2=E9=98=85</p>',
  '<img src=3D"cid:logo@corp" alt=3D"logo"></body></html>',
  '',
].join('\r\n')

// 1x1 透明 GIF。只有主判据正确、parts 树建起来时，它才会被 resolveCidImages
// 换成 data: URI；兜底出口拿不到 parts ⇒ cid: 引用原样留在正文里。
const GIF_B64 = 'R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7'

const MSG = [
  'Received: from ptr2.edm.infoq.com.cn (ptr2.edm.infoq.com.cn [120.132.54.43])',
  '\tby newxmmxsza85-0.qq.com (NewMX) with SMTP id 10B986EE',
  '\tfor <56551681@qq.com>; Sat, 05 Sep 2026 09:04:11 +0800',
  TRACKER_HEAD,
  '------=_Part_190350_1625789058.1788566899903',
  'Content-Type: text/html; charset="UTF-8"',
  'Content-Transfer-Encoding: quoted-printable',
  '',
  QP_HTML,
  '------=_Part_190350_1625789058.1788566899903',
  'Content-Type: image/gif',
  'Content-Transfer-Encoding: base64',
  'Content-ID: <logo@corp>',
  '',
  GIF_B64,
  '------=_Part_190350_1625789058.1788566899903--',
  '',
].join('\r\n')

test('extractEmailBody: 追踪头里的伪 content-type 不得劫持顶层 MIME 类型', () => {
  const out = extractEmailBody(MSG)
  assert.ok(out.length > 0, '必须抽出正文')
  // 协议头一行都不许进正文
  assert.doesNotMatch(out, /^Received:/m)
  assert.doesNotMatch(out, /^X-QQ-XMRINFO:/m)
  assert.doesNotMatch(out, /^MIME-Version:/m)
  // 边界行也不许进正文
  assert.doesNotMatch(out, /^--Part_|^--/, 'multipart 边界行不该出现在正文')
  // QP 必须被解码：中文可读，且不再有 =XX 转义
  assert.match(out, /点击这里取消订阅/)
  assert.doesNotMatch(out, /=E7=82=B9/, 'quoted-printable 未解码')
  assert.doesNotMatch(out, /lang=3D"en"/, '=3D 转义未解码')
  // ↓ 这一条才是**主判据**的护栏（负控实测逼出来的，见下）。
  //
  // 上面那些断言只证明「输出可读」，而兜底出口 stripMultipartWrapper 也能
  // 让它们全绿：顶层类型判错 → 走 return stripMessageHeader → 兜底照样剥出
  // 子部件正文。实测只关 headerField、保留兜底时，这一整个 test 依然 5/5 绿。
  //
  // cid 内联图只有**部件树建起来了**才会被 resolveCidImages 处理：它遍历
  // parts 建 Content-ID 索引。而 parts 非空的前提就是顶层 content-type 被读成
  // multipart/*。所以「cid: 被换成 data:」是主判据正确的唯一可观测证据。
  assert.match(out, /data:image\/gif;base64,R0lGOD/, 'cid: 内联图未被解析 ⇒ 顶层 MIME 类型读错')
  assert.doesNotMatch(out, /cid:logo@corp/, 'cid: 引用没被替换')
})

test('extractEmailBody: 伪字段名在纯文本部件里同样不生效', () => {
  // multipart/mixed + text/plain：顶层也带伪 content-type 的那批真实邮件形态。
  const msg = [
    'From: a@example.com',
    TRACKER_HEAD.replace(
      'Content-Type: multipart/alternative; boundary="----=_Part_190350_1625789058.1788566899903"',
      'Content-Type: multipart/alternative; boundary="BND"',
    ),
    '--BND',
    'Content-Type: text/plain; charset="UTF-8"',
    'Content-Transfer-Encoding: quoted-printable',
    '',
    '=E5=AE=98=E6=96=B9=E6=9C=8D=E5=8A=A1',
    '--BND--',
    '',
  ].join('\r\n')
  const out = extractEmailBody(msg)
  assert.match(out, /官方服务/)
  assert.doesNotMatch(out, /^X-QQ-XMRINFO:/m)
  assert.doesNotMatch(out, /^MIME-Version:/m)
  // 同上：纯文本部件的 cid 索引不适用，用「text/plain 分支被走到」的可观测差异
  // 来锁主判据 —— 若顶层类型被判成 reply-to，parts 里就不会有 text/plain，
  // 走的是兜底出口。这里断言 charset 解码生效（兜底不解码子部件）。
  assert.doesNotMatch(out, /=E5=AE=98/, 'QP 未解码 ⇒ 没走到 decodeTransfer')
})

test('extractEmailBody: 只有附件的部件树不再把整封报文当正文', () => {
  // 出口收紧的护栏：原本这里 return src，详情页会显示一屏 Received:。
  //
  // 断言只锁**顶层协议头**不进正文，不锁子部件头：部件树里没有 text/* 时，
  // 保留子部件头（附件名/类型）是合理的线索展示，剥光反而丢信息。
  // 真正要防的是 `Received:` 那种「整封报文当正文」的形态。
  const msg = [
    'From: a@example.com',
    'MIME-Version: 1.0',
    'Content-Type: multipart/mixed; boundary="B2"',
    '',
    '--B2',
    'Content-Type: application/pdf; name="a.pdf"',
    'Content-Transfer-Encoding: base64',
    'Content-Disposition: attachment; filename="a.pdf"',
    '',
    'JVBERi0xLjQK',
    '--B2--',
    '',
  ].join('\r\n')
  const out = extractEmailBody(msg)
  assert.doesNotMatch(out, /^MIME-Version:/m, '顶层协议头不该进正文')
  assert.doesNotMatch(out, /^Content-Type: multipart/m, '顶层协议头不该进正文')
  assert.doesNotMatch(out, /^From:/m, '顶层协议头不该进正文')
  assert.doesNotMatch(out, /^--B2/, 'multipart 边界行不该进正文')
})

test('extractEmailBody: 伪字段名与真头同行时也必须让位给真头', () => {
  // 负控锚：这一条专门覆盖「伪 content-type 后面紧跟同行另一个字段名」的真实形态。
  // 少写这一条，护栏会在还原修复后仍然全绿（第一版就踩过）。
  const msg = [
    'From: a@example.com',
    INLINE_SPURIOUS,
    '--BND4',
    'Content-Type: text/plain; charset="UTF-8"',
    'Content-Transfer-Encoding: quoted-printable',
    '',
    '=E6=AD=A3=E5=B8=B8=E6=9C=8D=E5=8A=A1',
    '--BND4--',
    '',
  ].join('\r\n')
  const out = extractEmailBody(msg)
  assert.match(out, /正常服务/, '真 Content-Type 应当胜出并解出正文')
  assert.doesNotMatch(out, /^X-QQ-XMRINFO:/m)
  assert.doesNotMatch(out, /^From:/m)
})

test('extractEmailBody: 折行的真实 Content-Type 仍能被读到', () => {
  // 负控：别把「行首锚定」修过头。真实报文里 Content-Type 常折行
  // （boundary 在续行），且字段名本身允许不同大小写。
  const msg = [
    'From: a@example.com',
    'content-TYPE: multipart/alternative;',
    '\tboundary="B3"',
    '',
    '--B3',
    'Content-Type: text/plain; charset=UTF-8',
    '',
    'hello body',
    '--B3--',
    '',
  ].join('\r\n')
  const out = extractEmailBody(msg)
  assert.match(out, /hello body/)
  assert.doesNotMatch(out, /^From:/m)
})

test('extractEmailBody: 伪字段名落在**行首**（冒号前有空格）时同样不生效', () => {
  // round33 负控实测出来的残余漏洞。
  //
  // 上一轮的 headerField 写的是 /^name[ \t]*:/ —— 冒号前容忍空白。它挡得住
  // 「伪字段名在行中间」（行首锚定就够了），挡不住「伪字段名恰好在行首」：
  // QQ 追踪头里伪字段名的形态就是 `content-type : <下一个伪字段名>`，
  // 一旦那一行前面没有折叠缩进的前导空格，整行从 content-type 起被认成真头，
  // 值取到紧跟其后的 list-unsubscribe，顶层 MIME 类型被劫持。
  //
  // 负控（把 headerField 还原成容忍冒号前空格的版本）实测三条判据同时转红：
  // cid 内联图不再被解析、QP 不解码、正文里直接露出裸 base64 载荷 ——
  // 与「49 封里 23 封命中」那个缺陷同一形态，只是触发位置从行中间挪到了行首。
  //
  // 收紧的代价（畸形头 `content-type : multipart/...` 不再被认出来）是可控的：
  // 解析不崩，落到 stripMessageHeader / stripMultipartWrapper 兜底，正文照样可读，
  // 损失的只是 parts 树（cid 图与附件元数据）。
  const msg = [
    'From: a@example.com',
    'X-QQ-XMRINFO: h=date : from :',
    'content-type : list-unsubscribe : from : sender',
    'MIME-Version: 1.0',
    'Content-Type: multipart/related; boundary="B9"',
    '',
    '--B9',
    'Content-Type: text/html; charset="UTF-8"',
    'Content-Transfer-Encoding: quoted-printable',
    '',
    '<img src=3D"cid:logo@corp">=E5=AE=98=E6=96=B9=E6=9C=8D=E5=8A=A1',
    '--B9',
    'Content-Type: image/gif',
    'Content-ID: <logo@corp>',
    '',
    GIF_B64,
    '--B9--',
    '',
  ].join('\r\n')
  const out = extractEmailBody(msg)
  // 主判据的可观测证据：parts 树成立 ⇒ resolveCidImages 跑过 ⇒ cid 变 data: URI。
  // 兜底出口也能让「正文可读」，所以可读性不能当主判据。
  assert.doesNotMatch(out, /cid:logo@corp/, 'cid 引用没被替换 ⇒ 顶层 MIME 类型被行首伪字段名劫持')
  assert.match(out, /data:image\/gif;base64,/, 'cid 内联图未被内联 ⇒ parts 树不成立')
  // 兜底出口不解码子部件，所以「QP 已解码」同样能区分两条出口。
  assert.match(out, /官方服务/, 'QP 未解码 ⇒ 走的是兜底出口而不是 text/html 分支')
  assert.doesNotMatch(out, /=E5=AE=98/, 'quoted-printable 未解码')
  assert.doesNotMatch(out, /R0lGOD/, '附件的裸 base64 载荷漏进正文 ⇒ parts 树没成立')
})
