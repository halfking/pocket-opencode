// 独立复核：notes-crud 跑的笔记是不是真的写进了设备本地加密库的 local_assets 表。
// 与 flow 的屏幕断言完全独立——只信「屏幕说有」正是不重复犯过的错。
// 访问方式与 scripts/pkm-test-fixture.mjs 一致：页内拿 app 的 db 对象直接查。
import fs from 'node:fs'
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const OUT = process.argv[2]

const adb = (args, t = 30000) =>
  execFileSync(ADB, ['-s', SERIAL, ...args], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function cdpEval(expr, ms = 20000) {
  const appPid = adb(['shell', 'pidof', PKG]).trim().split(/\s+/)[0]
  if (!appPid) throw new Error('APP_NOT_RUNNING')
  const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
    .split(/\r?\n/).map((l) => l.trim()).filter(Boolean)
  // 必须挑当前进程的 socket：设备上常留着死进程的 socket
  const sock = socks.filter((s) => s.endsWith(`_${appPid}`)).pop() || socks[socks.length - 1]
  const port = Number(adb(['forward', 'tcp:0', `localabstract:${sock}`]).trim())
  if (!Number.isInteger(port) || port <= 0) throw new Error('no port')
  try {
    const page = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json())
      .find((t) => t.type === 'page')
    const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${port}/`))
    let id = 0
    const pending = new Map()
    ws.addEventListener('message', (e) => {
      const m = JSON.parse(e.data)
      if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
    })
    await new Promise((r) => ws.addEventListener('open', r))
    const v = await new Promise((r) => {
      const i = ++id
      const t = setTimeout(() => { pending.delete(i); r(null) }, ms)
      pending.set(i, (x) => { clearTimeout(t); r(x) })
      ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: expr, returnByValue: true, awaitPromise: true } }))
    })
    ws.close()
    if (v?.exceptionDetails) return { __err: v.exceptionDetails?.exception?.description || v.exceptionDetails.text }
    return v?.result?.value
  } finally {
    adb(['forward', '--remove', `tcp:${port}`])
  }
}

const raw = await cdpEval(`(async function(){
  try {
    var app = document.querySelector('#app').__vue_app__;
    if (!app) return JSON.stringify({ err: 'no __vue_app__' });
    // db 的取法与 scripts/pkm-test-fixture.mjs 完全一致：走 pinia 的 connectivity
    // store 的 runtime.deps.db()。第一版我试的是 app.config.globalProperties.$db，
    // 拿不到 —— 那条路不存在，别再试。
    var pinia = app.config.globalProperties.$pinia;
    var db = pinia._s.get('connectivity').runtime.deps.db();
    if (!db) return JSON.stringify({ err: 'DB_NOT_READY' });
    // 列名照抄夹具脚本（id, workspace_id, title），别自己猜列名
    var rows = await db.all("SELECT id, workspace_id, title FROM local_assets WHERE kind='note' ORDER BY rowid DESC LIMIT 8");
    return JSON.stringify({ rows: rows });
  } catch (e) { return JSON.stringify({ err: String(e && e.message || e) }); }
})()`)

fs.writeFileSync(OUT, String(raw), 'utf8')
console.log(`-> ${OUT}`)
console.log(String(raw).slice(0, 400))
