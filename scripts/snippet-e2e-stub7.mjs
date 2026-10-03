#!/usr/bin/env node
/**
 * 收窄上一轮 e2e 检查的结论：把「桩的 7 封」和「历史残留行」分开。
 *
 * 上一轮 snippet-e2e-check.mjs 对新账户列出了 50 封，但同步只导入了 7 封 ——
 * 说明 /api/emails?accountId= 的过滤没生效，混进了全库的历史行。那些行的摘要是
 * 修复前写的（QP 未解码、原始 MIME、字面 HTML），不能算在当前代码头上。
 *
 * 这里按**桩夹具的主题**精确取那 7 封，并打印它们的 accountId，用来回答两件事：
 *   1. 桩的 7 封在当前代码下摘要是否干净（→ 端到端是否真的修好了）
 *   2. accountId 过滤是否真的失效（→ 另一个独立缺陷）
 *
 * Run: $env:POCKET_API=...; $env:POCKET_PASS=...; node scripts/snippet-e2e-stub7.mjs
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

const accs = (await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()).accounts || []
const stubAcc = accs.find((a) => a.emailAddress === 'audit-poc@pocket-audit.test')
console.log(`桩账户 = ${stubAcc?.id}  (imap ${stubAcc?.imapHost}:${stubAcc?.imapPort})`)

// 桩夹具 buildMails() 的 7 个主题
const STUB_SUBJECTS = [
  '季度视觉规范 v3（含内嵌示意图）',
  '生产环境变更通知（请确认）',
  '9 月度对账单（附件 + 内嵌图表）',
  '入职材料清单',
  '技术周报：本周 7 个项目进展与 3 个风险',
  '增值税电子普通发票已开具，请查收',
]

// 注意参数名是 account_id（下划线）。踩过的坑：第一版写成 accountId，
// 后端 r.URL.Query().Get("account_id") 取不到 → 过滤被静默忽略 → 返回全工作区
// 446 封，看起来像「跨租户泄漏」的严重缺陷。实际 ListEmailsScoped 已按
// user/workspace 收窄，只是我的过滤条件没生效。**先读 handler 再写探针。**
const res = await (await fetch(`${BASE}/api/emails?account_id=${encodeURIComponent(stubAcc.id)}&limit=500`, { headers: H })).json()
const list = res.emails || res.items || []
console.log(`该 account_id 查询返回 ${list.length} 封`)

const MIME_RE = /--=_Part|Content-Type:|boundary=|MIME-Version|Content-Transfer-Encoding/
const HTML_RE = /<\/?(br|a|p|div|span|table|img|html|body|!DOCTYPE)\b[^>]*>/i
const QP_RE = /=[0-9A-F]{2}/
const LONG_RE = /[!-~]{60,}/

const belongs = list.filter((e) => e.accountId === stubAcc.id)
const foreign = list.filter((e) => e.accountId !== stubAcc.id)
console.log(`  其中 accountId 真的等于桩账户的: ${belongs.length} 封`)
console.log(`  accountId 不等于的（过滤失效的证据）: ${foreign.length} 封`)

console.log('\n===== 桩的 7 封（按主题精确匹配）=====')
let pass = 0, fail = 0
for (const subj of STUB_SUBJECTS) {
  const hit = list.filter((e) => (e.subject || '').includes(subj.slice(0, 12)))
  if (!hit.length) { console.log(`  ??  未找到「${subj}」`); continue }
  for (const e of hit) {
    const s = String(e.snippet ?? '')
    const bad = {
      mime: MIME_RE.test(s), html: HTML_RE.test(s), qp: QP_RE.test(s), long: LONG_RE.test(s),
    }
    const clean = !bad.mime && !bad.html && !bad.qp && !bad.long
    clean ? pass++ : fail++
    console.log(`  ${clean ? 'ok  ' : 'FAIL'} acc=${e.accountId === stubAcc.id ? '本账户' : '★他账户'} len=${String(s.length).padStart(3)} ${JSON.stringify(s.slice(0, 76))}`)
    if (!clean) console.log(`        污染标记: ${JSON.stringify(bad)}`)
  }
}
console.log(`\n桩 7 封：干净 ${pass} / 污染 ${fail}`)

// 空正文那封：snippet 应为空且不能是 MIME
const empty = list.filter((e) => (e.subject || '').trim() === '')
console.log(`\n空主题那封：${empty.length} 封，snippet=${JSON.stringify(String(empty[0]?.snippet ?? '').slice(0, 60))}`)

console.log(`\n===== 结论 =====`)
console.log(fail === 0
  ? '桩的邮件在当前代码下摘要全部干净 ✅'
  : `桩邮件仍有 ${fail} 封摘要被污染 ❌`)
if (foreign.length) console.log(`另注：accountId 过滤失效，混进 ${foreign.length} 封他账户邮件（独立缺陷）`)
process.exit(fail === 0 ? 0 : 1)
