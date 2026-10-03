// sweep-routes.mjs — 真机全路由巡检：逐个跳转、读 innerText、挑出「假装可用」的页面。
//
// 为什么做这个：BUG-AT（密码箱）是靠人工点到的，但它不是孤例——
// 任何「功能没实现、界面照常展示」的地方长得都一样，而逐个人工点不现实。
// 判据是**内容**：把每个页面的可见文本抓下来，按「原始技术错误 / 渲染失败」的
// 特征串筛一遍，而不是靠 enabled 状态或有没有报错码。
//
// 只扫**静态**路由（path 里没有 :param）。带参数的路由天然会因为 id 不存在而
// 显示「未找到」，那是另一类，混进来会淹没真问题。
import { readFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9427'
const adb = (a, t = 60000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

// ---- 从 router-mobile.ts 抽路由表（数据驱动，不手挑）----
const ROUTER = 'C:/workspace/openpocket/wt3/frontend/src/app/router-mobile.ts'
const routes = []
{
  const lines = readFileSync(ROUTER, 'utf8').split(/\r?\n/)
  let cur = null
  for (const l of lines) {
    const m = l.match(/path:\s*'([^']+)'/)
    if (m) { cur = { path: m[1], name: '', redirect: '' }; routes.push(cur) }
    const n = l.match(/name:\s*'([^']+)'/)
    if (n && cur) cur.name = n[1]
    const r = l.match(/redirect:\s*'([^']+)'/)
    if (r && cur) cur.redirect = r[1]
  }
}
const staticRoutes = routes.filter((r) => !r.path.includes(':') && !r.redirect)

// ---- 特征串：命中即「这一页在把内部实现/失败态扔给用户」----
const RED_FLAGS = [
  [/plugin is not implemented/i, '原生插件未实现'],
  [/not implemented on/i, '原生插件未实现'],
  [/Cannot read propert/i, 'JS 运行时错误'],
  [/is not a function/i, 'JS 运行时错误'],
  [/undefined\s*[:：]/, '渲染出 undefined'],
  [/\[object Object\]/, '渲染出 [object Object]'],
  [/Failed to fetch|NetworkError|ERR_CONNECTION/i, '网络请求失败'],
  [/TypeError|ReferenceError|SyntaxError/, 'JS 异常类型外泄'],
  [/NaN/, '渲染出 NaN'],
]

// 导航栏/外壳的固定文案，用来判断「这一页除了壳什么都没有」
const CHROME_WORDS = ['跳到主要内容', 'menu', 'notifications', '首页', '学习', '会议', '更多', '主导航', 'Redclaw']

const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 12000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: null, err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { value: null, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 160) }
  return { value: v?.result?.value, err: '' }
}

console.log(`扫 ${staticRoutes.length} 条静态路由（跳过 ${routes.length - staticRoutes.length} 条带参数/重定向的）\n`)

const NEUTRAL = '#/__sweep_neutral__'
const findings = []
let lastBody = ''

/** 先跳中立页建立基线，再跳目标页 —— 否则目标页没跳成时，读到的是上一页的陈旧 DOM。 */
for (const r of staticRoutes) {
  const target = '#' + r.path
  const { value, err } = await ev(`(async () => {
    const withTimeout = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
    // 1) 基线：先离开现场
    location.hash = ${JSON.stringify(NEUTRAL)}
    await withTimeout(new Promise((r) => setTimeout(r, 500)), 2000)
    // 2) 目标
    location.hash = ${JSON.stringify(target)}
    await withTimeout(new Promise((r) => setTimeout(r, 1800)), 4000)
    const app = document.querySelector('#app')
    const txt = (app ? app.innerText : '') || ''
    return JSON.stringify({ hash: location.hash, txt: txt.replace(/\\n{2,}/g, '\\n').trim() })
  })()`)
  if (err) { findings.push({ ...r, kind: 'PROBE_FAIL', detail: err }); console.log(`  ?? ${r.path.padEnd(30)} 探针失败: ${err}`); continue }
  const { hash, txt } = JSON.parse(value)

  // ⚠️ 关键断言：hash 必须真的落到目标。第一版没有这条，于是导航中途失效后，
  // 后面每一条都读到同一个页面，却因为「没有技术错误串」被判成 ✅ —— 假阴性。
  //
  // 但要分清两种「没落到目标」：
  //   - 守卫重定向到 #/login?returnTo=...&unlock=1 —— **产品行为正确**
  //     （本地库锁着时就不该让数据路由可达），这类页面**等于没验**，要单独列出来；
  //   - 其它情况 —— 真的没跳过去，算巡检失败。
  if (hash !== target) {
    const guarded = hash.startsWith('#/login?returnTo=')
    findings.push({
      ...r,
      kind: guarded ? 'GUARDED_UNVERIFIED' : 'NAV_FAIL',
      detail: guarded ? `被锁定守卫重定向（${hash}）` : `hash 停在 ${hash}`,
      hash,
    })
    console.log(
      guarded
        ? `  🔒 ${r.path.padEnd(30)} 被锁定守卫重定向 → 本页**未验证**（先解锁再扫）`
        : `  ⚠️  ${r.path.padEnd(30)} 导航未生效：hash=${hash}`,
    )
    continue
  }

  const hits = RED_FLAGS.filter(([re]) => re.test(txt)).map(([, why]) => why)
  const body = txt.split('\n').map((s) => s.trim()).filter((s) => s && !CHROME_WORDS.includes(s))
  // 判「未重渲染」必须比**整段正文**，不能只比首行：很多页首行都是同一个
  // 返回按钮标签（arrow_back），只比首行会把正常的两页误判成陈旧。
  const fullBody = body.join('\n')
  const sameAsPrev = body.length > 0 && fullBody === lastBody
  if (hits.length) {
    findings.push({ ...r, kind: 'RED_FLAG', detail: [...new Set(hits)].join('/'), sample: body.slice(0, 3).join(' | ').slice(0, 90), hash })
    console.log(`  ❌ ${r.path.padEnd(30)} ${[...new Set(hits)].join('/')}   [落地 ${hash}]`)
    console.log(`      ${body.slice(0, 3).join(' | ').slice(0, 110)}`)
  } else if (sameAsPrev) {
    findings.push({ ...r, kind: 'SUSPECT_STALE', detail: `整段正文与上一条完全相同（${fullBody.slice(0, 24)}）`, hash })
    console.log(`  ⚠️  ${r.path.padEnd(30)} 整段正文与上一条相同，疑似未重渲染  [${fullBody.slice(0, 30)}]`)
  } else if (body.length === 0) {
    findings.push({ ...r, kind: 'EMPTY', detail: '除导航壳外无任何内容', hash })
    console.log(`  ⚠️  ${r.path.padEnd(30)} 除导航壳外无内容  [落地 ${hash}]`)
  } else {
    console.log(`  ✅ ${r.path.padEnd(30)} ${body[0].slice(0, 46)}`)
  }
  if (body.length) lastBody = fullBody
  await ev('new Promise(r => setTimeout(r, 150))')
}

console.log(`\n===== 汇总 =====`)
const bad = findings.filter((f) => f.kind === 'RED_FLAG')
const guarded = findings.filter((f) => f.kind === 'GUARDED_UNVERIFIED')
const navFail = findings.filter((f) => f.kind === 'NAV_FAIL')
const stale = findings.filter((f) => f.kind === 'SUSPECT_STALE')
const empty = findings.filter((f) => f.kind === 'EMPTY')
const failed = findings.filter((f) => f.kind === 'PROBE_FAIL')
console.log(`红旗（把技术错误/失败态展示给用户）${bad.length} 条`)
for (const f of bad) console.log(`  ${f.path.padEnd(30)} ${f.detail}   ${f.sample || ''}`)
console.log(`已确认落地并检查 ${staticRoutes.length - guarded.length - navFail.length - failed.length} 条`)
console.log(`被锁定守卫拦下、**未验证** ${guarded.length} 条：${guarded.map((f) => f.path).join(', ')}`)
console.log(`导航未生效 ${navFail.length} 条：${navFail.map((f) => f.path).join(', ')}`)
console.log(`疑似未重渲染 ${stale.length} 条：${stale.map((f) => f.path).join(', ')}`)
console.log(`空页 ${empty.length} 条：${empty.map((f) => f.path).join(', ')}`)
console.log(`探针失败 ${failed.length} 条：${failed.map((f) => f.path).join(', ')}`)
if (guarded.length) {
  console.log(`\n⚠️  有 ${guarded.length} 条因本地库锁定被守卫拦下。**先解锁再跑本脚本**才能覆盖它们。`)
}
ws.close()
process.exit(bad.length || navFail.length || failed.length ? 1 : 0)
