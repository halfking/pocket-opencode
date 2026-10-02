// 枚举生产对各个 Origin 回不回 Access-Control-Allow-Origin。
//
// ⚠️ 上一版用 PowerShell 写的，判据是
//     $acao = ($h | Select-String ...).ToString().Trim()
//   没命中时它是 $null，`.ToString()` 抛错，于是 $acao **保留上一轮的旧值**，
//   于是「没有该头」被打印成上一轮那个域名的头 —— 看着像有、其实没有。
//   拿 Node 重写，判据是**本轮这一行**里有没有，且不用「上一次的值」兜底。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const ENDPOINT = 'https://pocket.itestu.cn/api/tasks'

const ORIGINS = [
  'https://pocket.itestu.cn',      // 生产自己的前端域（PLAN.md 声明在白名单里）
  'https://localhost',            // Capacitor 壳 origin（Android 默认 androidScheme）
  'capacitor://localhost',       // Capacitor 备用壳 origin
  'http://localhost',
  'http://127.0.0.1:4175',
  'https://evil.example',         // 对照：必须**不**被放行
]

const rows = []
for (const o of ORIGINS) {
  let out = ''
  try {
    out = execFileSync(ADB, ['-s', S, 'shell',
      `curl -s -D - -o /dev/null --max-time 15 -H 'Origin: ${o}' ${ENDPOINT}`],
      { encoding: 'utf8', timeout: 40000, maxBuffer: 33554432 })
  } catch (e) {
    rows.push({ origin: o, status: 'CURL_FAIL', acao: null, raw: String(e.message).slice(0, 60) })
    continue
  }
  const status = (out.match(/^HTTP\/[\d.]+ (\d+)/m) || [])[1] || '?'
  // 只认**本轮**输出里的那一行，不用任何跨轮状态
  const m = out.match(/^Access-Control-Allow-Origin:\s*(.+)$/im)
  rows.push({ origin: o, status, acao: m ? m[1].trim() : null })
}

console.log(`端点: ${ENDPOINT}\n`)
console.log('Origin'.padEnd(28) + '状态'.padEnd(6) + 'Access-Control-Allow-Origin')
for (const r of rows) {
  const tag = r.acao === null ? '<缺失>' : r.acao
  console.log(r.origin.padEnd(28) + String(r.status).padEnd(6) + tag)
}

const echoed = (o) => rows.find((r) => r.origin === o)?.acao
// ⚠️ 这里原先写成 `echoed(evil) === 'https://evil.example'`，于是「没回显」被判成
//    「竟被放行」—— 方向写反了。恶意 Origin 正确的样子就是**不回显**。
const evilBlocked = echoed('https://evil.example') === null
console.log('\n=== 判读 ===')
console.log(`  生产自己域被放行（回显自身）:            ${echoed('https://pocket.itestu.cn') === 'https://pocket.itestu.cn' ? '✅' : '❌'}`)
console.log(`  恶意 Origin 未被放行（安全侧正常）:      ${evilBlocked ? '✅ 未回显，安全侧正常' : '❌ 竟被回显'}`)
for (const o of ['https://localhost', 'capacitor://localhost', 'http://localhost']) {
  console.log(`  ${o.padEnd(24)} 被放行:      ${echoed(o) === o ? '✅' : '❌ 未放行'}`)
}
console.log('\n  App 每个请求都带 Authorization，属于非简单请求 ⇒ 浏览器必发 OPTIONS 预检。')
console.log('  预检缺 Access-Control-Allow-Origin ⇒ fetch 拒绝 ⇒ TypeError: Failed to fetch。')
process.exit(0)
