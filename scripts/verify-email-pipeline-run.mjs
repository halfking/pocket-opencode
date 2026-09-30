// 邮件流水线验证：真实例上跑一轮，并逐条核对本轮改动的可观测面。
// 用法：$env:POCKET_PASS=...; node scripts/verify-email-pipeline-run.mjs
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8099'
const PW = process.env.POCKET_PASS

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
const lj = await login.json()
if (!login.ok) { console.log('登录失败', login.status, JSON.stringify(lj).slice(0, 200)); process.exit(1) }
const H = { Authorization: `Bearer ${lj.token || lj.access_token}`, 'Content-Type': 'application/json' }
let pass = 0, fail = 0
const check = (name, ok, detail = '') => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? '  → ' + detail : ''}`)
  ok ? pass++ : fail++
}

// 1) 账户列表（后续 LWW 用例的前置）
const accRes = await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()
const accs = accRes.accounts || []
console.log(`账户数 = ${accs.length}`)

// 2) 手动触发一轮完整流水线（定时那条路由单测 + 启动日志覆盖，这里验证手动路）
const t0 = Date.now()
const runRes = await fetch(`${BASE}/api/email/pipeline/run`, { method: 'POST', headers: H, body: '{}' })
const rep = await runRes.json().catch(() => ({}))
console.log(`\nPOST /api/email/pipeline/run → ${runRes.status}，耗时 ${Date.now() - t0}ms`)
console.log(JSON.stringify(rep, null, 2).slice(0, 1200))
check('手动流水线返回 200', runRes.status === 200, `status=${runRes.status}`)
check('返回体含流水线报告字段', 'accountsSynced' in rep && 'invoices' in rep, Object.keys(rep).join(','))

// 3) LWW 守卫：带旧基准版本写 → 409 且不回写
if (accs.length) {
  const a = accs[0]
  const before = a.updatedAt ?? 0
  const stale = { displayName: (a.displayName || 'stale-probe') + '-stale', updatedAt: Math.max(0, before - 600) }
  const r1 = await fetch(`${BASE}/api/email/accounts/${a.id}`, {
    method: 'PUT', headers: H, body: JSON.stringify(stale),
  })
  const b1 = await r1.json().catch(() => ({}))
  check('旧基准版本写入被拒 409', r1.status === 409, `status=${r1.status} ${JSON.stringify(b1).slice(0, 120)}`)

  // 4) 基准版本正确 → 200，且 updatedAt 变大
  const fresh = { displayName: (a.displayName || 'fresh-probe') + '-fresh', updatedAt: before }
  const r2 = await fetch(`${BASE}/api/email/accounts/${a.id}`, {
    method: 'PUT', headers: H, body: JSON.stringify(fresh),
  })
  const b2 = await r2.json().catch(() => ({}))
  check('同基准版本写入成功', r2.status === 200, `status=${r2.status}`)
  const after = b2.updatedAt ?? 0
  check('updated_at 单调递增', after > before, `${before} → ${after}`)

  // 5) 再用同一个旧基准写一次 → 必须又被拒（证明守卫不是一次性生效）
  const r3 = await fetch(`${BASE}/api/email/accounts/${a.id}`, {
    method: 'PUT', headers: H, body: JSON.stringify(stale),
  })
  check('重复用旧基准仍被拒 409', r3.status === 409, `status=${r3.status}`)
}

// 6) 发票汇总（含共享汇总文档 + 合计金额）
const sumRes = await fetch(`${BASE}/api/emails/invoices/summary`, { headers: H })
const sum = await sumRes.json().catch(() => ({}))
console.log(`\n发票汇总 → ${sumRes.status}：count=${sum.count} amountTotal=${sum.amountTotal} csv=${sum.shareDocCsv}`)
check('汇总接口 200', sumRes.status === 200, `status=${sumRes.status}`)
check('汇总含合计金额字段', typeof sum.amountTotal === 'number', String(sum.amountTotal))
check('汇总生成共享文档(CSV/MD)', !!sum.shareDocCsv || sum.count === 0, `${sum.shareDocCsv} / ${sum.shareDocMd}`)

// 7) A4 网格导出（用有文件的发票；没有就如实跳过）
const invRes = await (await fetch(`${BASE}/api/emails/invoices?limit=200`, { headers: H })).json()
const invs = (invRes.invoices || []).filter((i) => i.filePath)
if (invs.length) {
  const ids = invs.slice(0, 5).map((i) => i.id)
  for (const grid of [2, 3]) {
    const r = await fetch(`${BASE}/api/emails/invoices/export`, {
      method: 'POST', headers: H, body: JSON.stringify({ ids, grid }),
    })
    const b = await r.json().catch(() => ({}))
    check(`A4 ${grid}x${grid} 网格导出`, r.status === 200 && !!b.file, `status=${r.status} file=${b.file} count=${b.count}`)
    if (b.file) {
      const dl = await fetch(`${BASE}${b.url}`, { headers: { Authorization: H.Authorization } })
      const buf = Buffer.from(await dl.arrayBuffer())
      check(`导出文件可下载且是 PDF(${grid}x${grid})`, dl.status === 200 && buf.subarray(0, 4).toString() === '%PDF', `${dl.status} ${buf.length}B`)
    }
  }
} else {
  console.log('\nSKIP  发票网格导出：本账号下没有已下载文件的发票（不据此宣称已验证）')
}

console.log(`\n合计 ${pass} PASS / ${fail} FAIL`)
process.exit(fail ? 1 : 0)
