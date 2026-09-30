// 自测：用文本协议验证 stub 服务器（不依赖 pocketd）
// 注意：必须用「持久行队列」而不是在 data 回调里就地消费——连接建立时
// server 的 greeting 早于第一次 readLine() 挂上，就地消费会把它丢掉并死等。
import net from 'node:net'

const PORT = Number(process.env.IMAP_PORT || 1143)
const lines = []
let waiter = null
let carry = Buffer.alloc(0)

function push(line) {
  if (waiter) { const w = waiter; waiter = null; w(line) } else lines.push(line)
}
function take() {
  if (lines.length) return Promise.resolve(lines.shift())
  return new Promise((res) => { waiter = res })
}

const sock = net.connect(PORT, '127.0.0.1')
sock.on('data', (d) => {
  carry = Buffer.concat([carry, d])
  let i
  while ((i = carry.indexOf('\r\n')) >= 0) {
    push(carry.subarray(0, i).toString('utf8'))
    carry = carry.subarray(i + 2)
  }
})
sock.on('error', (e) => { console.log('socket error:', e.message); process.exit(2) })

async function send(cmd, label) {
  sock.write(cmd + '\r\n')
  const r = await take()
  console.log(`${(label || cmd).padEnd(40)} -> ${r.slice(0, 105)}`)
  return r
}

const greeting = await take()
console.log('greeting'.padEnd(40), '->', greeting.slice(0, 70))

await send('t1 CAPABILITY', 'CAPABILITY')
await send('t2 LOGIN me@pocket-audit.local secret', 'LOGIN')
await send('t3 ID ("name" "pocket-audit")', 'ID')
await send('t4 SELECT INBOX', 'SELECT INBOX')
await send('t5 UID SEARCH ALL', 'UID SEARCH ALL')
await send('t6 UID FETCH 1 (UID ENVELOPE INTERNALDATE)', 'UID FETCH envelope')

// BODY[] literal：响应里第一行以 {N} 结尾，随后是 N 字节原文，最后一行以 ) 结尾
sock.write('t7 UID FETCH 1 BODY.PEEK[]<0.33554432>\r\n')
let first = await take()
let acc = first + '\n'
const m = /\{(\d+)\}$/.exec(first)
if (m) {
  const n = Number(m[1])
  while (Buffer.byteLength(acc, 'utf8') < n + 12) acc += (await take()) + '\n'
}
while (!/\)\s*$/.test(acc.trimEnd())) acc += (await take()) + '\n'
const cidRef = acc.includes('cid:logo-v3@audit')
const cidHead = acc.includes('Content-ID: <logo-v3@audit>')
const b64Img = /Content-ID: <logo-v3@audit>[\s\S]*?[A-Za-z0-9+/]{200,}/.test(acc)
console.log(`${'BODY[] 抓取'.padEnd(40)} -> ${acc.length}B  cid引用=${cidRef}  内嵌图头=${cidHead}  base64图体=${b64Img}`)

send('t8 LOGOUT', 'LOGOUT')
sock.end()

const ok = cidRef && cidHead && b64Img
console.log(ok ? '\n✅ stub 服务器协议自测通过' : '\n❌ 自测失败')
process.exit(ok ? 0 : 1)
