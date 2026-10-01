#!/usr/bin/env node
/**
 * 端到端验收：真实 MIME → 真实 IMAP 抓取 → 落库摘要是否干净。
 *
 * 这一环补的是单测补不了的：DeriveSnippet 的 9 个用例验的是纯函数，fetcher 的
 * 调用点护栏验的是「源码里接了没」，但两者都没验过**协议层真的把字节喂进来**
 * 之后落库的那一列长什么样。2026-08-31 到今天，原始 MIME 转储就是这么漏到
 * 用户眼前的（真机 /notifications 46/100 元素溢出、12,311px 被静默裁掉）。
 *
 * 判据（逐条邮件独立判定，不抽样）：
 *   badMIME   —— 摘要里出现 MIME 边界 / Content-Type / boundary=
 *   badHTML   —— 摘要里出现字面 HTML 标签（<br/>、<a href、<p>…）
 *   长度      —— 超长不可断串（MIME boundary 正是此类）
 *
 * token 走环境变量，不落盘；输出也不打印任何密钥。
 *
 * Run: $env:POCKET_API=...; $env:POCKET_PASS=...; node scripts/snippet-e2e-check.mjs
 */
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8097'
const PW = process.env.POCKET_PASS

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
if (!login.ok) { console.error('登录失败', login.status); process.exit(1) }
const lj = await login.json()
const H = { Authorization: `Bearer ${lj.token || lj.access_token}`, 'Content-Type': 'application/json' }

const accRes = await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()
const accs = accRes.accounts || []
console.log(`账户数 = ${accs.length}`)
for (const a of accs) {
  console.log(`  ${a.id}  ${a.emailAddress}  imap=${a.imapHost}:${a.imapPort}  enabled=${a.enabled}`)
}

const MIME_RE = /--=_Part|Content-Type:|boundary=|MIME-Version|^\s*--\w+\r?$/m
const HTML_RE = /<\/?(br|a|p|div|span|table|img|html|body)\b[^>]*>/i

let total = 0, badMIME = 0, badHTML = 0, overlong = 0
const rows = []

for (const acc of accs) {
  const url = `${BASE}/api/emails?accountId=${encodeURIComponent(acc.id)}&limit=200`
  const r = await fetch(url, { headers: H })
  if (!r.ok) { console.error(`  ${acc.id} 列表失败 ${r.status}`); continue }
  const j = await r.json()
  const list = j.emails || j.items || []
  console.log(`\n=== ${acc.emailAddress}（${list.length} 封）===`)
  for (const e of list) {
    total++
    const s = e.snippet == null ? '' : String(e.snippet)
    const isMIME = MIME_RE.test(s)
    const isHTML = HTML_RE.test(s)
    // 不可断长串：连续 60 个以上非空白、非中日韩的 ASCII
    const hasLongRun = /[!-~]{60,}/.test(s)
    if (isMIME) badMIME++
    if (isHTML) badHTML++
    if (hasLongRun) overlong++
    rows.push({ acc: acc.emailAddress, subject: (e.subject || '').slice(0, 34), len: s.length, isMIME, isHTML, hasLongRun, snippet: s.slice(0, 90) })
    const flag = isMIME || isHTML || hasLongRun ? 'FAIL' : 'ok  '
    console.log(`  ${flag} len=${String(s.length).padStart(3)} ${(e.subject || '').slice(0, 30).padEnd(32)} ${JSON.stringify(s.slice(0, 70))}`)
  }
}

console.log(`\n===== 汇总 =====`)
console.log(`总邮件数        : ${total}`)
console.log(`摘要含 MIME 转储: ${badMIME}`)
console.log(`摘要含字面 HTML : ${badHTML}`)
console.log(`含超长不可断串  : ${overlong}`)
const pass = badMIME === 0 && badHTML === 0 && overlong === 0
console.log(pass ? '结论：全部干净 ✅' : '结论：仍有污染 ❌')
if (!pass) {
  console.log('\n污染样本：')
  for (const r of rows.filter((x) => x.isMIME || x.isHTML || x.hasLongRun)) console.log('  ' + JSON.stringify(r))
}
process.exit(pass ? 0 : 1)
