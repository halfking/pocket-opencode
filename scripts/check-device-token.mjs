// 读真机 WebView leveldb 里存的 pocket_token，打各个后端端口，报告状态。
// 写文件而不是 node -e：PowerShell 会吞掉内联脚本里的正则转义（本轮又踩一次）。
import { readFileSync } from 'node:fs'
import { existsSync } from 'node:fs'

const dumpPath = process.argv[2]
const ports = process.argv.slice(3).map(Number)
// ⚠️ 2026-10-05：原来缺参数时 exit 1，而 exit 1 在本仓约定里是**真判红**。
//    「用法写错了」和「令牌打各个端口都失败」被压成同一个退出码，
//    一次全量门禁扫描里根本分不开。改成 exit 3（与 run-gates.mjs 头注释一致：
//    **≥3 = 拒绝给结论**），并补上 dumpfile 缺失的检查——
//    少了它会走到 readFileSync(undefined) 抛一个与真因无关的 ENOENT 栈。
if (!ports.length || !dumpPath) {
  console.error('usage: node check-device-token.mjs <dumpfile> <port...>')
  console.error('  这是用法错误，不是「令牌无效」的判定结果，所以退出码 3 而不是 1。')
  process.exit(3)
}

// 同理：dump 文件本身不存在也是**前置缺失**，不是判红。
// 少了这一句，ENOENT 会以未捕获异常的形式带 exit 1 出去，
// 和「令牌打过去全部 401」在扫描结果里长得一模一样。
if (!existsSync(dumpPath)) {
  console.error(`[前置缺失] dump 文件不存在：${dumpPath}`)
  console.error('  没法读到令牌 ⇒ 没跑到被检查对象，退出码 3（不是 1）。')
  process.exit(3)
}

const lines = readFileSync(dumpPath, 'latin1').split(/\r?\n/)
const RE = /^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/

let token = null
outer: for (let i = 0; i < lines.length; i++) {
  if (!lines[i].includes('pocket_token')) continue
  for (let j = i; j < Math.min(i + 4, lines.length); j++) {
    const v = lines[j].trim()
    if (RE.test(v)) { token = v; break outer }
  }
}

if (!token) {
  console.log('TOKEN NOT FOUND in dump (App may be logged out)')
  process.exit(2)
}

const payload = JSON.parse(Buffer.from(token.split('.')[1], 'base64url'))
console.log(`device token: ws=${payload.workspace_id} exp=${new Date(payload.exp * 1000).toISOString()}`)

let anyOk = false
for (const port of ports) {
  try {
    const r = await fetch(`http://127.0.0.1:${port}/api/notifications?limit=200`, {
      headers: { Authorization: `Bearer ${token}` }
    })
    const body = await r.text()
    let n = 'n/a'
    try {
      const b = JSON.parse(body)
      if (Array.isArray(b.notifications)) n = b.notifications.length
    } catch {}
    console.log(`  :${port} -> ${r.status} count=${n}`)
    if (r.status === 200) anyOk = true
  } catch (e) {
    console.log(`  :${port} -> ERR ${e.message}`)
  }
}
process.exit(anyOk ? 0 : 1)
