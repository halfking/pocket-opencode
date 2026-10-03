import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readdirSync, readFileSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { extractEmailBody } from '../email-body-format.ts'

// 真实语料重放：把 data/email-bodies-raw/ 里 POP3 抓下来的 49 封**真实原文**
// （AES-GCM 解密所得）喂给当前 extractEmailBody，验 §4.124 的结论在真实
// QQ 追踪头上成立，而不是只在等价夹具上成立。
//
// ## 门控
//
//   POCKET_DIAG_REAL_BODIES=<解出的明文目录> node --test <this file>
//
// 语料本身不进仓库（真实邮件 + 体积），所以必须显式指目录。
// **目录为空 / 不存在时 Fatal，不许静默 skip**：一个永远 skip 的重放
// 会被下一轮读成「跑过了没问题」，而它其实一次都没跑。
//
// 仓库里始终会跑的是末尾那条判据自检 —— 它不依赖语料。

const DIR = process.env.POCKET_DIAG_REAL_BODIES || ''

// ── 判据 ────────────────────────────────────────────────────────────────

// 「非 ASCII 文本仍以 QP 转义形态存在」的判据：连续 3 组以上、且每组首字节
// 高位为 1（>= 0x80）的 =XX。
//
// 为什么是「首字节高位」而不是「汉字首字节 E4-E9」：UTF-8 是**流式**的，
// `点=E7=82=B9` 的第二个字节 B9 属于「点」，紧接着 E5 才是「击」的首字节。
// 写成 /(?:=E[4-9A-F][0-9A-F]){3,}/ 会要求首字节逐组重复，
// 对 `=E7=82=B9=E5=87=BB` 这种真实序列**漏判**（2026-10-03 由本文件
// 末尾的判据自检抓出来，不是想出来的）。
// 而 >=0x80 是「非 ASCII 字节」的定义，与字符集无关，中文/emoji/西欧重音都盖得住。
//
// 为什么**不用**绝对计数（=XX 总数 ≥ N）：整页 HTML 里 URL 查询串
// （?v=6&fm=jpeg）、HTML 属性（content="IE=edge"）天然含大量 `=XX` 形态，
// 8 万字节的邮件页轻松过 30 个。实测 2026-10-03：用绝对阈值判，
// 49 封里报出 6 封「QP 未解码」，逐条查上下文全是
// `msgid=6960126090300432146` / `content="IE=edge"` —— 全是误报。
// 阈值随语料长度漂移就等于没有阈值。
const CJK_QP_TRIPLE = /(?:=[89A-Fa-f][0-9A-Fa-f]){3,}/g

// 顶层协议头进了正文 = 走了 return src 兜底（整封报文当正文）。
const PROTO_HEADERS = [
  [/^Received:/m, 'Received:'],
  [/^X-QQ-[^\r\n]*:/m, 'X-QQ-* 追踪头'],
  [/^DKIM-Signature:/m, 'DKIM-Signature'],
  [/^Authentication-Results:/m, 'Authentication-Results'],
  [/^Return-Path:/m, 'Return-Path'],
]

// 裸 base64 载荷：附件/追踪头的 base64 正文泄漏。阈值取 300 字符
// （一个 1x1 GIF 编码后 59 字符，成片泄漏必然远超）。
const B64_BLOB = /^[A-Za-z0-9+/=\r\n\s]{300,}$/m

function findViolations(out) {
  const v = []
  for (const [re, label] of PROTO_HEADERS) if (re.test(out)) v.push(label)
  if (B64_BLOB.test(out)) v.push('裸 base64 载荷')
  const cjk = out.match(CJK_QP_TRIPLE) || []
  if (cjk.length) v.push(`中文 QP 未解码 x${cjk.length}`)
  if (!out.trim()) v.push('正文为空')
  return v
}

// ── 门控内的重放 ────────────────────────────────────────────────────────

test('真实语料重放：49 封 POP3 原文的详情页正文不含协议头/base64/未解码 QP', (t) => {
  if (!DIR) {
    t.skip('设 POCKET_DIAG_REAL_BODIES=<解密后的明文目录> 才跑（语料不进仓库）')
    return
  }
  assert.ok(existsSync(DIR), `POCKET_DIAG_REAL_BODIES 指向的目录不存在：${DIR}`)
  const files = readdirSync(DIR).filter((n) => n.endsWith('.eml')).sort()
  assert.ok(files.length > 0, `${DIR} 下没有 .eml —— 解密步骤没产出，判据从未工作`)

  // 语料前提：QQ 追踪头必须在场，否则这次重放与上一轮的合成夹具等价，
  // 证明不了「在真实追踪头上成立」。
  let withTracker = 0
  const bad = []
  for (const f of files) {
    const raw = readFileSync(join(DIR, f), 'utf8')
    if (/X-QQ-XMRINFO/i.test(raw)) withTracker++
    const v = findViolations(extractEmailBody(raw))
    if (v.length) bad.push(`${f}: ${v.join(', ')}`)
  }
  t.diagnostic(`语料 ${files.length} 封，含 X-QQ-XMRINFO 追踪头 ${withTracker} 封`)
  assert.ok(withTracker > 0, '语料里一封追踪头都没有 ⇒ 这次重放证明不了任何事')
  assert.deepEqual(bad, [], `以下真实邮件的详情页正文仍不干净：\n${bad.join('\n')}`)
})

// ── 形态普查：把「有没有这个变体」也变成可复跑的断言 ─────────────────────
//
// 2026-10-03 实测结论（49 封）：
//   · 伪字段名形态一律是**行中**（DKIM-Signature 的折叠续行里，
//     形如 `  reply-to : to : ... content-type : list-unsubscribe : ...`）
//     —— 带空格前缀，冒号前也有空格。
//   · 行首 `content-type`（冒号**零空格**）确实 49 封全有，但值**全部是
//     合法 MIME 类型**（multipart/…; text/…; application/…），
//     也就是真正的部件头，不是伪字段名。
//   · 行首 + 冒号前带空格的伪 content-type：**0 封**。
//
// 所以「行首 content-type: 零空格」这个变体在真实语料里**不存在**，
// 而 round33 修的「行首 + 冒号前空格」在真实语料里也**没有反例样本**
// （A/B 实测：把 headerField 换回容忍冒号前空格的版本，49 封输出逐字节
// 相同，差异 0/49）。它的价值由合成夹具那条护栏证明，不是由语料证明。
// 下面两条断言把这个观察钉住：哪天语料真长出这种行首伪字段名，
// 重放会以「行首伪 content-type 出现」的形式报出来，而不是悄悄变绿。
const SPURIOUS_FIELD_LINE_START = /^[A-Za-z-]+[ \t]+:/m
const MIME_TYPE_VALUE = /^[ \t]*(multipart|text|image|application|message|audio|video)\/[^\s;]+/i

test('真实语料形态普查：行首 content-type 都是真 MIME 头，伪字段名不落行首', (t) => {
  if (!DIR) {
    t.skip('设 POCKET_DIAG_REAL_BODIES=<解密后的明文目录> 才跑（语料不进仓库）')
    return
  }
  const files = readdirSync(DIR).filter((n) => n.endsWith('.eml')).sort()
  assert.ok(files.length > 0, `${DIR} 下没有 .eml`)

  const zeroSpace = new Map() // 行首 content-type（冒号零空格）的取值分布
  const offenders = []
  for (const f of files) {
    for (const line of readFileSync(join(DIR, f), 'utf8').split(/\r?\n/)) {
      const m = /^([A-Za-z-]+)([ \t]*):([^\r\n]*)/.exec(line)
      if (!m) continue
      const [, name, wsBeforeColon, value] = m
      if (name.toLowerCase() !== 'content-type') continue
      if (wsBeforeColon === '') {
        const key = value.trim().slice(0, 30)
        zeroSpace.set(key, (zeroSpace.get(key) || 0) + 1)
        // 行首零空格的 content-type 若取值不是合法 MIME 类型，
        // 那就是「伪字段名伪装成真头」——即本轮要观察的变体。
        if (!MIME_TYPE_VALUE.test(value)) offenders.push(`${f}: ${line.slice(0, 90)}`)
      } else {
        // 行首 + 冒号前有空格 = round33 修的那个形态。语料里应为 0。
        offenders.push(`${f}: [行首冒号前空格] ${line.slice(0, 90)}`)
      }
    }
  }
  t.diagnostic(`行首 content-type（零空格）取值 ${zeroSpace.size} 种，例：${[...zeroSpace].slice(0, 3).map(([k, v]) => `${k} x${v}`).join(' ; ')}`)
  assert.deepEqual(offenders, [], '真实语料里出现了「行首伪 content-type」，行首锚定的结论需要重估')
})

// ── 判据自检（不依赖语料，任何机器都跑） ─────────────────────────────────
//
// 防的是「判据恒暗」：findViolations 若对已知脏的输入都不报，那它在上面
// 两条里报「全部干净」是零信息量的。已知脏的形态就是 §4.124 修的那个 —
// 整封报文被 return src 原样当正文。
test('判据自检：findViolations 对「整封报文当正文」的形态必须报出违规', () => {
  const dirty = [
    'Received: from ptr2.edm.infoq.com.cn (ptr2.edm.infoq.com.cn [120.132.54.43])',
    '\tby newxmmxsza85-0.qq.com (NewMX) with SMTP id 10B986EE',
    'X-QQ-XMRINFO: h=date : from : content-type : list-unsubscribe',
    '',
    '<p>=E7=82=B9=E5=87=BB=E8=BF=99=E9=87=8C</p>',
    'R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7'.repeat(8),
  ].join('\r\n')
  const v = findViolations(dirty)
  assert.ok(v.includes('Received:'), '判据对行首 Received: 失明')
  assert.ok(v.includes('X-QQ-* 追踪头'), '判据对追踪头失明')
  assert.ok(v.includes('裸 base64 载荷'), '判据对裸 base64 失明')
  assert.ok(v.some((x) => x.startsWith('中文 QP 未解码')), '判据对未解码 QP 失明')

  // 反向：干净正文不许被误报（否则上面两条就是「全红也绿」的空判据）。
  const clean = '<html><body><p>点击这里取消订阅</p><img src="https://x/y.png?w=640&h=427"></body></html>'
  assert.deepEqual(findViolations(clean), [], '干净正文被误报 —— 判据过宽')
})
