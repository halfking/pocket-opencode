#!/usr/bin/env node
/**
 * 真机 UI 勘探：把各模块的「可点控件」dump 出来，供写路径验证脚本按事实写断言。
 *
 * 为什么要单独一个勘探脚本（而不是在验证脚本里猜 selector）：
 * 上一轮 redmi-write-ops-modules.mjs 吃过一次亏——先假设「新建卡组」会弹出
 * bottom-sheet，找不到容器就判 FAIL，结论其实是我的假设错了，不是产品缺陷
 * （实际是页内 inline 入口）。所以先看 DOM 有哪些真实控件，再写断言。
 *
 * 用法：
 *   $env:POCKET_SERIAL='192.168.31.19:5555'
 *   node scripts/explore-write-targets.mjs            # 全部模块
 *   node scripts/explore-write-targets.mjs gateway    # 只看网关
 */
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9241'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const sk = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sk.find((s) => s.endsWith(`_${pid}`)) || sk[sk.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x) => {
  const r = await send('Runtime.evaluate', { expression: x, returnByValue: true })
  if (r?.exceptionDetails) {
    const d = r.exceptionDetails
    console.error('  [EVAL ERR]', (d.exception?.description || d.text || '').split('\n')[0])
    return null
  }
  return r?.result?.value
}

const MODULES = [
  ['vault', '#/vault', '密码箱'],
  ['marketplace', '#/marketplace/skills', '市场'],
  ['email', '#/email', '邮箱'],
  ['gateway', '#/gateway', '网关'],
  ['instances', '#/instances', '实例'],
  ['cost', '#/cost', '费用配额'],
  ['tasks', '#/tasks', '任务'],
  ['sessions', '#/sessions', '会话'],
]

const only = process.argv[2]
const list = only ? MODULES.filter(([k]) => k === only) : MODULES

for (const [key, route, label] of list) {
  await ev(`(function(){ Array.from(document.querySelectorAll('.bottom-sheet-overlay,[class*="overlay"],.dialog-backdrop')).forEach(function(o){try{o.click()}catch(e){}}); return 1 })()`)
  await ev(`location.hash = ${JSON.stringify(route)}`)
  await sleep(2800)
  const dump = await ev(`JSON.stringify({
    hash: location.hash,
    gated: /#\\/login/.test(location.hash),
    text: (document.body.innerText||'').replace(/\\s+/g,' ').trim().slice(0,300),
    buttons: Array.from(document.querySelectorAll('button')).map(b=>({t:(b.textContent||'').trim().slice(0,24), off:!!b.disabled, cls:(b.className||'').toString().slice(0,40)})).filter(b=>b.t).slice(0,40),
    inputs: Array.from(document.querySelectorAll('input,textarea,select')).map(i=>({t:i.type||i.tagName, ph:(i.placeholder||'').slice(0,24), aria:(i.getAttribute('aria-label')||'').slice(0,24)})).slice(0,25),
    addBtns: Array.from(document.querySelectorAll('button,a,[role=button]')).map(b=>({tag:b.tagName, t:(b.textContent||'').trim().slice(0,24), off:!!b.disabled, cls:(b.className||'').toString().slice(0,48)})).filter(b=>/新建|创建|添加|新增|\\+|New|Create|Add|保存|删除|编辑/.test(b.t||'')).slice(0,25)
  })`)
  let d = {}
  try { d = JSON.parse(dump || '{}') } catch {}
  console.log(`\n########## ${label} (${key}) ${route} ##########`)
  console.log(`hash=${d.hash}  gated=${d.gated}`)
  console.log(`text: ${(d.text || '').slice(0, 220)}`)
  console.log(`add/edit buttons: ${JSON.stringify(d.addBtns, null, 1)}`)
  console.log(`inputs: ${JSON.stringify(d.inputs)}`)
}
ws.close()
process.exit(0)
