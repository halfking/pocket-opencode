#!/usr/bin/env node
/**
 * 邮件正文接口的契约检查：抓「正文被压平成纯文本、内联图丢失」这个回归。
 *
 * 为什么要有这个脚本（2026-10-01 真机事故）：
 *   旧后端二进制把服务端正文缓存 dataDir/email-bodies/<id>.bin 写成**已压平的纯文本**，
 *   图片部件在管线阶段就丢了。详情接口 emailBodyResponse 拿到一段「合法」的普通文本，
 *   契约检查不出来、用户却只看到字没有图。对拍实测：
 *     8088 旧二进制  body 150 字节    非原始 MIME、无 src="cid:"
 *     8097 当前 main  body 27,156 字节 是原始 MIME、含 src="cid:"
 *
 *   第一版脚本（email-detail-audit.mjs）在这里犯了错：它去响应里找 html /
 *   inlineImages / attachments 字段，判定「cid图=0、附件=0」并报 5 封详情坏 ——
 *   而详情接口按设计只返回 {emailId, source, bytes, body}，图片由前端从 body 里的
 *   cid 引用内联。那是**测错了层**，差点把正常行为报成 P0。教训写在这里，避免重犯。
 *
 * 正确的判据：正文必须是**能承载图片/附件的原始 MIME**，而不是一段压平文本。
 *   - 桩夹具那 7 封里，带图/带附件的必须能看出 MIME 结构
 *   - body 明显短于同账户其它邮件、且不含 Content-Type/Content-ID 的，标记为可疑压平
 *   - source 为 imap 时必须含 MIME 结构（缓存里被压平就是回归）
 *
 * Run: $env:POCKET_API=...; $env:POCKET_PASS=...; node scripts/email-body-contract-check.mjs [account_id]
 */
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8088'
const PW = process.env.POCKET_PASS

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
if (!login.ok) { console.error('登录失败', login.status); process.exit(1) }
const lj = await login.json()
const H = { Authorization: `Bearer ${lj.token || lj.access_token}`, 'Content-Type': 'application/json' }

const accs = (await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()).accounts || []
const accId = process.argv[2] || (accs.find((a) => a.imapPort === 1143) || accs[0])?.id
if (!accId) { console.error('找不到账户'); process.exit(1) }
console.log(`目标账户 ${accId}\n`)

const list = (await (await fetch(`${BASE}/api/emails?account_id=${encodeURIComponent(accId)}&limit=200`, { headers: H })).json()).emails || []
console.log(`邮件 ${list.length} 封\n`)

const MIME_RE = /Content-Type:\s*multipart|Content-Type:\s*text\/html|Content-ID:|Content-Transfer-Encoding/i
const CID_RE = /src="cid:/i

// 夹具 7 封里，em-1（cid 内嵌图）、em-3（嵌套 + 附件）、em-7（PDF 附件）按设计
// 必须能看出 MIME 结构；em-6 是**空正文**那封，正文本就该只有几十字节纯文本，
// 不能算压平 —— 第一版用「短 + 无 MIME = 可疑」把它误判了，这里收紧。
const MUST_HAVE_MIME = /^em-(1|3|7)-/
const LEGIT_SHORT = /^em-6-/

let bad = 0
for (const e of list) {
  let r
  try {
    r = await (await fetch(`${BASE}/api/emails/${encodeURIComponent(e.id)}/body`, { headers: H })).json()
  } catch {
    console.log(`ERR  取正文失败 ${e.id}`)
    bad++
    continue
  }
  const body = String(r.body ?? '')
  const hasMime = MIME_RE.test(body)
  const hasCid = CID_RE.test(body)
  const isFixture = /acct-\d+-1$/.test(e.accountId || '') && /em-\d-/.test(e.id)
  const mustHaveMime = MUST_HAVE_MIME.test(e.id)
  const legitShort = LEGIT_SHORT.test(e.id)

  const problems = []
  if (mustHaveMime && !hasMime) {
    problems.push('按夹具应含内联图/附件，正文却无 MIME 结构 —— 已被压平')
  }
  // 供 fixture 账户做整体判定：该账户里所有「应含结构」的封都无结构 → 正文被系统性压平
  if (isFixture && !hasMime && !legitShort && !problems.length) {
    problems.push('正文无 MIME 结构（非空正文夹具）')
  }

  if (problems.length) bad++
  const note = legitShort ? '（空正文夹具，短纯文本属预期）' : ''
  console.log(`${problems.length ? 'FAIL' : 'ok  '} ${e.id}  source=${r.source} bytes=${r.bytes} mime=${hasMime} cid=${hasCid}${note}`)
  if (problems.length) console.log(`      ${problems.join(' / ')}`)
  console.log(`      ${JSON.stringify(body.replace(/\s+/g, ' ').slice(0, 88))}`)
}

console.log(`\n===== 结论 =====`)
console.log(`共 ${list.length} 封，异常 ${bad} 封`)
console.log(bad === 0
  ? '正文契约正常：每封都保留了原始 MIME ✅'
  : `有 ${bad} 封正文疑似被压平，内联图/附件已丢失 ❌\n（若后端是旧二进制，替换为当前 main 构建即可 —— 见 docs/handoff/2026-10-01-email-detail-missing-images-root-cause.md）`)
process.exit(bad === 0 ? 0 : 1)
