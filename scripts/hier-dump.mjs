// hier-dump.mjs — 导航到指定路由，抓真机可访问性树并打印可写进 flow 的选择器。
//
// 为什么要这个壳（三个坑都踩过，每个都让「照模板猜选择器」这条路走不通）：
//
//  1. `uiautomator dump` 在这台 MIUI 上会被 SIGKILL（exit 137），连试 5 次全挂。
//     Maestro 自己的 hierarchy 走的是另一条路，能出。
//  2. `maestro hierarchy` 是 **JVM 程序**，中文 Windows 下按 **GBK** 往 stdout 写。
//     直接用 Node 的 utf8 读会全是乱码 —— 而乱码会让你以为「页面上没有这个文案」，
//     于是去 Vue 模板里猜一个 placeholder 写进 flow。必须先按 GBK 解码。
//  3. Maestro 2.11 **默认每次调用都重装 driver**，重装是先卸载再安装，MIUI 拦安装，
//     于是「跑一次就亲手把 driver 卸掉且装不回来」。必须 --no-reinstall-driver。
//     （踩过一次，报 INSTALL_FAILED_USER_RESTRICTED。）
//
// 顺带：正文/控件的 text 会被 WebView 把相邻 span 合并成一个节点，
// 所以本脚本打印的是节点整串，写 flow 时要用正则 `xxx.*` 而不是纯字符串。
//
// 用法：
//   node scripts/hier-dump.mjs '#/tasks'            # 导航 + 抓 + 打印
//   node scripts/hier-dump.mjs '#/tasks' 新任务     # 带过滤子串
import { execFileSync, spawnSync } from 'node:child_process'
import { readFileSync, writeFileSync, existsSync } from 'node:fs'
import { homedir } from 'node:os'

// 可执行文件解析：env 覆盖 -> PATH -> 常见安装位置。
// 为什么不写死：这三个常量原本是某台 Windows 开发机的绝对路径
// （C:/Users/86133/... / C:/workspace/openpocket/... / C:\Program Files\Eclipse Adoptium\...）。
// 换到 macOS 宿主后它们全部指向不存在的文件，于是脚本在第一步就以
// "spawnSync adb ENOENT" 的形式死掉——**看起来像设备问题，
// 实际是脚本自己绑死在一台机器上**。与 maestro-run.mjs 同一处理。
const IS_WIN = process.platform === 'win32'

function whichFirst(candidates) {
  for (const c of candidates) {
    if (!c) continue
    if (c.includes('/') || c.includes('\\')) {
      if (existsSync(c)) return c
      continue
    }
    const r = spawnSync(IS_WIN ? 'where' : 'which', [c], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] })
    const first = (r.stdout || '').split(/\r?\n/).map((s) => s.trim()).filter(Boolean)[0]
    if (r.status === 0 && first) return first
  }
  return null
}

const ADB = process.env.POCKET_ADB_BIN
  || whichFirst([
    IS_WIN ? 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe' : null,
    `${homedir()}/Library/Android/sdk/platform-tools/adb`,
    `${homedir()}/Android/Sdk/platform-tools/adb`,
    'adb',
  ])
  || 'adb'

const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = process.env.POCKET_APP_ID || 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9600'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const JAVA = process.env.JAVA_HOME
  || whichFirst([
    IS_WIN ? 'C:\\Program Files\\Eclipse Adoptium\\jdk-21.0.12.101-hotspot' : null,
    '/usr/libexec/java_home',
  ])
  || ''
const MAESTRO = process.env.POCKET_MAESTRO_BIN
  || whichFirst([
    IS_WIN ? 'C:/workspace/openpocket/logs/maestro/maestro/bin/maestro.bat' : null,
    `${homedir()}/.maestro/bin/maestro`,
    'maestro',
  ])
  || 'maestro'

const HASH = process.argv[2] || '#/tasks'
const FILTER = process.argv[3] || ''
const RAW = `logs/hier-${HASH.replace(/[#/]/g, '_')}.json`

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 30000) => execFileSync(ADB, ['-s', SERIAL, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 10000) } catch { return '' } }

function cdpAttach() {
  const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
  if (!pid) throw new Error('APP_NOT_RUNNING')
  const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
    .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
  const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
  if (!sock) throw new Error('NO_DEVTOOLS_SOCKET')
  adbSoft(['forward', `tcp:${PORT}`, `localabstract:${sock}`])
  return pid
}

async function ev(wsUrl, expr, ms = 12000) {
  const ws = new WebSocket(wsUrl)
  const pending = new Map()
  let id = 0
  ws.addEventListener('message', (e) => {
    const m = JSON.parse(e.data)
    // ⚠️ 传 m.result 而不是整条 m：Runtime.evaluate 的响应是
    //    {id, result:{result:{value}}}，传整条再读 .result.value 恒为 undefined，
    //    会被误判成「CDP 断了 / 页面没落地」。踩过，别改回去。
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
  })
  // 页内 await 一律套 Promise.race —— WebView 被节流时 rAF/setTimeout 会被钳到分钟级，
  // 不套超时就会把整个脚本挂死（这个坑在 verify-au-fix*.mjs 上踩过）。
  const open = await Promise.race([
    new Promise((r) => ws.addEventListener('open', () => r(true), { once: true })),
    new Promise((r) => setTimeout(() => r(false), 10000)),
  ])
  if (!open) throw new Error('CDP_OPEN_TIMEOUT')
  const send = (x) => new Promise((r) => {
    const i = ++id
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  const r = await send(expr)
  ws.close()
  if (r?.__t) return null
  if (r?.exceptionDetails) return null
  return r?.result?.value
}

/**
 * 拉 DevTools target 列表，失败就重建转发再试。
 *
 * 为什么必须重试：这台设备上 `maestro hierarchy` 跑完之后，WebView 的 devtools
 * socket 会有一段时间不接受连接，`/json/list` 直接挂到超时（实测连续三轮：
 * 抓树前能连 → 抓完树下一次就超时）。裸 fetch 的结果是「静默假阴性」，
 * 所以每次重试都重建转发再拉，拉不到就明确抛错，绝不返回空列表冒充成功。
 */
async function fetchTargets(attempts = 5) {
  let last = null
  for (let i = 1; i <= attempts; i++) {
    try {
      cdpAttach()
      const r = await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(8000) })
      const list = await r.json()
      if (list.length) return list
      last = new Error('空 target 列表')
    } catch (e) {
      last = e
    }
    console.error(`[hier] /json/list 第 ${i}/${attempts} 次失败：${last?.message || last}，重建转发重试`)
    await sleep(2000)
  }
  throw new Error(`CDP_UNREACHABLE（重试 ${attempts} 次）：${last?.message || last}`)
}

async function evNow(expr, ms = 15000) {
  for (let i = 1; i <= 4; i++) {
    try {
      const l = await fetchTargets(2)
      const p = l.find((t) => t.type === 'page')
      if (!p) throw new Error('NO_PAGE_TARGET')
      const v = await ev(p.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`), expr, ms)
      if (v !== null && v !== undefined) return v
      console.error(`[hier] evaluate 第 ${i}/4 次返回空，重连重试`)
    } catch (e) {
      console.error(`[hier] evaluate 第 ${i}/4 次失败：${e?.message || e}`)
    }
    await sleep(1500)
  }
  return null
}

const pid = cdpAttach()
const list = await fetchTargets()
const page = list.find((t) => t.type === 'page')
if (!page) { console.error('NO_PAGE_TARGET'); process.exit(4) }
const wsUrl = page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`)

// ── 本地库解锁 ────────────────────────────────────────────────────────────
// 为什么必须先解锁：本地 SQLCipher 库的 AES key 只存在于**进程内存**，
// App 一被 force-stop 就丢；守卫只在「导航到依赖本地库的路由」那一刻才判定，
// 所以停在 #/ai 时**看不出**库锁着，一进 #/notes 就被弹到解锁屏。
// 结果是「抓到的树是解锁屏而不是目标页」——读起来像「页面上没有这个文案」，
// 于是去 Vue 模板里猜一个不存在的文案写进 flow。
//
// 配方与 scripts/maestro-run.mjs 的 ensureLocalDbUnlocked() 同源（那��是
// 实测过的）：判据用 body.innerText（不是标签精确匹配——后者恒为 false），
// 定位用 password/placeholder（不是坐标——坐标依赖机型），按钮用「解锁/认证」。
async function ensureUnlocked() {
  const ON_UNLOCK = '解锁本地数据'
  const readScreen = async () => {
    const raw = await evNow(`(function(){
      try { return JSON.stringify({ hash: location.hash, body: (document.body.innerText || '').replace(/\\s+/g, ' ').slice(0, 1200) }); }
      catch (e) { return JSON.stringify({ err: String(e && e.message || e) }); }
    })()`, 12000)
    try { return JSON.parse(String(raw)) } catch { return { err: '读屏返回非 JSON: ' + raw } }
  }
  // 逼守卫把解锁屏弹出来
  await evNow(`location.hash = ${JSON.stringify('#/pkm/today?__hierunlock=' + Date.now())}; true`, 12000)
  await sleep(2500)
  let s = await readScreen()
  if (!String(s.body || '').includes(ON_UNLOCK)) {
    console.log('[hier] 本地库已解锁')
    return true
  }
  console.log('[hier] 本地库锁着 → 用 CDP 填主密码解锁')
  const filled = await evNow(`(function(){
    var ins = Array.prototype.slice.call(document.querySelectorAll('input'));
    var el = ins.filter(function (e) { return e.type === 'password'; })[0]
      || ins.filter(function (e) { return (e.placeholder || '').indexOf('主密码') >= 0; })[0]
      || ins.filter(function (e) { return e.type === 'password' || e.type === 'text'; })[0];
    if (!el) return 'no-input:' + ins.map(function (e) { return e.placeholder || e.type; }).join('/');
    var set = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
    set.call(el, ${JSON.stringify(MASTER)});
    el.dispatchEvent(new Event('input', { bubbles: true }));
    return 'ok len=' + el.value.length;
  })()`, 12000)
  if (!String(filled).startsWith('ok ')) {
    console.error(`[hier] ❌ 填主密码失败：${filled}`)
    return false
  }
  const clicked = await evNow(`(function(){
    var bs = Array.prototype.slice.call(document.querySelectorAll('button'));
    var b = bs.filter(function (e) { var t=(e.innerText||'').trim(); return t === '解锁' || t.indexOf('认证') >= 0; })[0];
    if (!b) return 'no-button:' + bs.map(function (e) { return (e.innerText||'').trim(); }).join('/');
    if (b.disabled) return 'still-disabled';
    b.click();
    return 'ok clicked';
  })()`, 12000)
  if (!String(clicked).startsWith('ok ')) {
    console.error(`[hier] ❌ 点解锁失败：${clicked}`)
    return false
  }
  for (let i = 0; i < 20; i++) {
    await sleep(1000)
    s = await readScreen()
    if (!String(s.body || '').includes(ON_UNLOCK)) break
  }
  if (String(s.body || '').includes(ON_UNLOCK)) {
    console.error('[hier] ❌ 解锁后解锁屏仍在 —— 主口令不对或解锁未生效')
    return false
  }
  console.log('[hier] 已解锁 ✅')
  return true
}

if (!(await ensureUnlocked())) process.exit(6)

await evNow(`location.hash=${JSON.stringify(HASH)}`)
// ⚠️ 判据不能用「CDP 回读 location.hash」——实测那个回读会假阴性（evaluate 返回 null，
//    但 /json/list 里 URL 明明已经是 #/tasks），差点让我以为导航失败。
//    改用 DevTools 自己的 target 列表当判据：它只反映浏览器当前真实地址。
const wantHash = (u) => (u.split('#')[1] || '')
let ok = false
const deadline = Date.now() + 30000
while (Date.now() < deadline) {
  await sleep(900)
  try {
    const l2 = await fetchTargets(2)
    if (l2.some((t) => t.type === 'page' && wantHash(t.url) === HASH.replace(/^#/, ''))) { ok = true; break }
  } catch { /* 转发抖动，重试 */ }
}
await sleep(2500) // hash 变了不代表 DOM 画完了
console.log(`[hier] ${HASH} → ${ok ? '已落地（DevTools target URL 确认）' : '⚠️ 30s 内未落地'}`)

// 可选前置动作：抓「弹窗打开后」这类中间态时用。
// POCKET_HIER_PRE_JS 指向一个 JS 文件，在导航完成后、抓树之前于页内执行。
// 例：先点「收起」把分诊区折叠掉，任务列表才会进可视区。
const preFile = process.env.POCKET_HIER_PRE_JS
if (preFile) {
  const js = readFileSync(preFile, 'utf8')
  await evNow(js, 20000)
  await sleep(1800)
  console.log(`[hier] 已执行前置动作 ${preFile}`)
}

// ---- 抓树：JVM 按 GBK 输出，必须 --no-reinstall-driver ----
// stderr 单独落盘：之前写成 2>nul，失败时文件是 0 字节却什么都看不到，
// 只能靠猜。空输出必须显式报错，不能当成「页面上什么都没有」。
//
// 引号也踩过：spawnSync('cmd', ['/c', '"C:/...bat" args'>) 在 Windows 上会被
// cmd 判成「不是内部或外部命令」——cmd 不接受这种转义后的引号。
// 交给 Node 用 shell:true 自己拼 `cmd /d /s /c "<整行>"` 才正确。
const errPath = RAW.replace(/\.json$/, '.err.txt')
const line = `"${MAESTRO}" hierarchy --no-ansi --no-reinstall-driver > ${RAW} 2>${errPath}`
spawnSync(line, {
  encoding: 'buffer', timeout: 180000, shell: true,
  env: { ...process.env, JAVA_HOME: JAVA, MAESTRO_CLI_NO_ANALYTICS: 'true', MAESTRO_DEVICE: SERIAL },
})
if (readFileSync(RAW).length === 0) {
  console.error(`[hier] ❌ maestro hierarchy 输出为空，stderr：\n${readFileSync(errPath, 'utf8').slice(0, 800)}`)
  process.exit(5)
}
const buf = readFileSync(RAW)
// 编码自证：认不出「登录」这类已知中文就说明猜错了编码，别把乱码当「页面上没有」。
//
// ⚠️ 2026-11-23 修：原实现**固定按 GBK 解**。那是给「中文 Windows」准备的，
// 但同一段 JVM 输出在 macOS/Linux 上是 **UTF-8**——按 GBK 解会得到
// 「AI 宸ュ叿」这种乱码。它不只是难看：乱码里含有 U+E0A6 之类的私用区字符，
// 于是「页面上没有这个文案」这个**错误结论**被坐实，人会去 Vue 模板里
// 猜一个不存在的 placeholder 写进 flow。
//
// ⇒ 改成**两种编码都试，用探针命中率高的那个**。探针本身也是自证：
// 认不出「AI Work Mess / WebView」就说明两种都不对，如实报出来。
const probes = ['AI Work Mess', 'WebView', '登录', '任务']
function decodeWith(enc) {
  try { return new TextDecoder(enc).decode(buf) } catch { return '' }
}
function score(t) { return probes.filter((s) => t.includes(s)).length }
const candidates = [
  { enc: 'utf-8', text: decodeWith('utf-8') },
  { enc: 'gbk', text: decodeWith('gbk') },
]
candidates.sort((a, b) => score(b.text) - score(a.text))
const chosen = candidates[0]
const hit = probes.filter((s) => chosen.text.includes(s))
console.error(`[hier] 编码判定：选 ${chosen.enc}（探针命中 ${hit.length}/${probes.length}: ${hit.join(' / ') || '<无，可能不是登录页或编码不对>'}）`)
if (score(chosen.text) === 0) {
  console.error(`[hier] ⚠️ 两种编码都没认出任何探针；下面打印的可能是乱码，别据此写 flow 选择器。`)
}
const text = chosen.text
const jsonPath = RAW.replace(/\.json$/, '.utf8.json')
writeFileSync(jsonPath, text, 'utf8')

const root = JSON.parse(text)
const out = []
;(function walk(n, d) {
  const a = n.attributes || {}
  const cls = String(a.clazz || a.class || '')
  const t = String(a.text ?? '')
  const cd = String(a['content-desc'] ?? '')
  const clickable = a.clickable === 'true'
  const keep = t || cd || /EditText|Button|ImageButton|CheckBox/.test(cls) || clickable
  if (keep) {
    const line = [
      '  '.repeat(Math.min(d, 10)),
      `[${cls.replace('android.widget.', '').replace('android.view.', '')}]`,
      `t=${JSON.stringify(t.slice(0, 50))}`,
      cd ? `cd=${JSON.stringify(cd.slice(0, 30))}` : '',
      a.enabled === 'true' ? 'en=1' : 'en=0',
      clickable ? 'CLICK' : '',
      String(a.bounds || ''),
    ].filter(Boolean).join(' ')
    if (!FILTER || line.includes(FILTER)) out.push(line)
  }
  for (const c of n.children || []) walk(c, d + 1)
})(root, 0)
console.log(`[hier] pid=${pid} 节点 ${out.length} 条${FILTER ? '，过滤=' + FILTER : ''}；完整树 ${jsonPath}\n`)
console.log(out.join('\n'))
