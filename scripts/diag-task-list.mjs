// 诊断四：#/ai 上 .task-card 恒为 0，但 PG 里有 6+ 条 active 任务。
// 两种可能必须分开：
//  (a) 产品 bug——列表没把已有任务渲染出来；
//  (b) 夹具/选择器错——任务渲染在别的容器里，或需要筛选/滚动才出现。
// 本脚本把整个 ai-view 的结构与文本全量导出，人工判读，不预设结论。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9365'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const PSQL = process.env.POCKET_PSQL || 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
// PG schema：跟随后端配置（backend/internal/config/config.go 的 POCKET_PG_SCHEMA，默认值相同）。
// 写死 opencode_pocket 会让本脚本只能对着共享库跑 —— 失败时 SEED 就留在别人的库里。
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';
if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const psql = (sql) => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql], { encoding: 'utf8' }).trim()

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const send = (method, params = {}) =>
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value
await send('Runtime.enable')

if (await ev('!!document.querySelector(\'input[placeholder*="主密码"]\')')) {
  await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(700)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()==='解锁');if(b)b.click();return 1})()`)
  await sleep(5000)
}
await ev(`location.hash='#/ai'`)
await sleep(4000)

console.log('=== PG: active 任务数 ===')
console.log(psql(`select count(*) from ${SCHEMA}.tasks where status='active'`))
console.log('=== PG: 各 workspace 分布 ===')
console.log(psql(`select workspace_id || ' -> ' || count(*) from ${SCHEMA}.tasks group by workspace_id`))

const dump = await ev(`(function(){
  var root = document.querySelector('.ai-view') || document.body;
  var counts = {};
  Array.prototype.slice.call(root.querySelectorAll('*')).forEach(function(e){
    var c = (e.className && e.className.toString && typeof e.className !== 'object') ? e.className.toString() : '';
    if (!c) return;
    c.split(/\\s+/).forEach(function(k){ if(k) counts[k] = (counts[k]||0)+1; });
  });
  var top = Object.keys(counts).map(function(k){return k+':'+counts[k]}).sort().slice(0, 60);
  return JSON.stringify({
    hash: location.hash,
    textLen: (root.textContent||'').trim().length,
    text: (root.textContent||'').trim().replace(/\\s+/g,' ').slice(0, 700),
    taskCards: root.querySelectorAll('.task-card').length,
    testids: Array.prototype.slice.call(root.querySelectorAll('[data-testid]')).map(function(e){return e.getAttribute('data-testid')}).slice(0,30),
    classHistogram: top
  }, null, 1);
})()`)
console.log('=== ai-view 结构 ===')
console.log(dump)
ws.close()
process.exit(0)
