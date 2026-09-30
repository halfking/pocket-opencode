// 邮件详情端到端校验：后端 /api/emails/{id}/body 原文 -> 前端 extractEmailBody -> HTML。
//
// 必须复用前端真实的解析函数（email-body-format.ts）才算端到端：后端响应里
// 没有 htmlBody 字段，HTML 是前端解析 MIME 树得到的。早期版本只看响应字段，
// htmlLen 恒为 0 却报「全部通过」——那是脚本自己的假绿灯。
import { readFileSync } from 'node:fs'
import { extractEmailBody } from '../frontend/src/features/email/email-body-format.ts'

const BASE = process.env.POCKET_API || 'http://127.0.0.1:8088'
const PW = process.env.POCKET_PASS

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
const lj = await login.json()
if (!login.ok) { console.log('登录失败', login.status); process.exit(1) }
const H = { Authorization: `Bearer ${lj.token || lj.access_token}` }

const list = await (await fetch(`${BASE}/api/emails?limit=50`, { headers: H })).json()
const emails = list.emails || []
console.log(`列表 ${emails.length} 封，base=${BASE}\n`)

let fail = 0
for (const e of emails) {
  const r = await fetch(`${BASE}/api/emails/${e.id}/body`, { headers: H })
  const d = await r.json().catch(() => ({}))
  const raw = d.body || ''

  let out = ''
  let parseErr = ''
  try { out = extractEmailBody(raw) } catch (err) { parseErr = String(err && err.message || err) }

  const subj = String(e.subject || '')
  const isHTML = /<[a-z]/i.test(out)
  const imgs = out.match(/<img[^>]*>/gi) || []
  const dataImgs = imgs.filter((s) => /src=["']data:/i.test(s))
  const cidLeft = (out.match(/cid:/gi) || []).length

  const problems = []
  // ⑥ 号 fixture 正文只有一个空格且无主题，空展示是正确行为，不能算缺陷
  const expectEmpty = !subj.trim() && !raw.replace(/\s+/g, '').replace(/^[A-Za-z-]+:.*$/gm, '').trim()
  if (r.status !== 200) problems.push(`HTTP ${r.status}`)
  if (parseErr) problems.push(`解析抛错: ${parseErr}`)
  if (!out.trim() && !expectEmpty) problems.push('正文全空')
  if (/=E[0-9A-F]{2}/i.test(out)) problems.push('quoted-printable 未解码（正文里还有 =XX）')
  if (/锟斤|�/.test(out)) problems.push('字符编码损坏（乱码）')
  // ① cid 内联图：原文有 cid: 就必须变成 data:
  if (cidLeft > 0) problems.push(`残留 ${cidLeft} 个未内联的 cid:`)
  if (imgs.length > 0 && dataImgs.length === 0) problems.push(`有 ${imgs.length} 个 <img> 但没有任何 data: 内联图`)
  if (subj.includes('内嵌示意图') && dataImgs.length === 0) problems.push('cid 邮件的内嵌图没有被内联')
  if (subj.includes('对账单') && !/对账单/.test(out)) problems.push('对账单正文丢失')
  if (out.length < 30 && subj.trim()) problems.push(`正文过短(${out.length})，疑似内容缺失`)

  if (problems.length) fail++
  console.log(`[${problems.length ? 'FAIL' : ' OK '}] ${JSON.stringify(subj.slice(0, 30))}  raw=${raw.length}B -> 展示 ${out.length}B  html=${isHTML} img=${imgs.length}(data:${dataImgs.length})${expectEmpty ? ' [预期空]' : ''}`)
  for (const p of problems) console.log(`        !! ${p}`)
  if (out.trim()) console.log(`        预览: ${out.replace(/\s+/g, ' ').slice(0, 150)}`)
  console.log('')
}
console.log(fail ? `=> ${fail}/${emails.length} 封有问题` : `=> 全部 ${emails.length} 封通过`)
process.exit(fail ? 1 : 0)
