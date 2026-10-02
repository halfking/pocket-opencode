#!/usr/bin/env node
/**
 * audit-entry-reachability.mjs — 真机上逐个点「更多」页的入口，验证**落地页可达**。
 *
 * 背景：BUG-P（/flashcards 有路由没入口）与 BUG-Q（/scheduled-tasks 入口指向
 * 不存在的路由）都是静态读代码能看出来、但只有**真点一遍**才能确认后果的缺陷。
 * 静态对账只能发现「路径不存在」，发现不了「点进去是空白页但没报错」这类。
 *
 * 方法：CDP 进 #/more，遍历九宫格条目，逐个点击，然后等路由稳定，判定：
 *   - 落地 hash 是否为空 / 仍是 #/more（点了没反应）
 *   - 落地页 body 是否空白（排除 loader/错误态）
 *   - 是否出现「页面未找到」「404」类文案
 *
 * 注意：CDP 驱动按钮要派发完整指针序列（pointerdown -> mousedown -> pointerup
 * -> mouseup -> click），只调 el.click() 对部分组件无效。实测过的坑，写在这里
 * 免得下一轮重新踩。
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/audit-entry-reachability.mjs
 */
import { execFileSync } from 'node:child_process'
import { requireDevPass } from './lib/dev-pass.mjs'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9243'
const MASTER = process.env.POCKET_MASTER || ''
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

// 恢复会话
await ev(`location.hash = '#/login'`); await sleep(2500)
if (await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)) {
  await ev(`(function(){var el=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(MASTER)});el.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(1600)
  await ev(fullClickText('解锁')); await sleep(4000)
}
if (await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)) {
  const devPass = requireDevPass()
  const fillBy = (sel, val) => `(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`
  await ev(fillBy('input[placeholder*="用户名"]', 'admin'))
  await ev(fillBy('input[type="password"]', devPass)); await sleep(900)
  await ev(fullClickText('登录')); await sleep(6000)
}
console.log('session hash =', await ev('location.hash'))

// 读九宫格条目：图标名 + 文案 + 预期路由
await ev(`location.hash = '#/more'`); await sleep(3000)
const items = JSON.parse(await ev(`JSON.stringify((function(){
  var out = [];
  var nodes = document.querySelectorAll('a, button, [role="button"], li, [class*="item"], [class*="tile"]');
  Array.prototype.forEach.call(nodes, function(n){
    var txt = (n.innerText||'').trim();
    if (!txt || txt.length > 40) return;
    var first = txt.split('\\n')[0].trim();
    if (!first) return;
    out.push({ text: first, full: txt.replace(/\\n/g,'|').slice(0,60), href: (n.getAttribute&&n.getAttribute('href'))||'' });
  });
  var seen={}, uniq=[];
  out.forEach(function(o){ if(!seen[o.full]){seen[o.full]=1;uniq.push(o);} });
  return uniq;
})())`) || '[]')

// 排除壳层元素（页头、底部导航、分组标题）
const NOISE = /^(menu|notifications|更多功能|运维与高级|home|study|meetings|more|首页|学习|会议|更多|跳到主要内容)$/i
const entries = items.filter((e) => !NOISE.test(e.text))
console.log(`\n待验入口 ${entries.length} 个\n`)

const results = []
for (const e of entries) {
  // 回到「更多」页再点，否则上一次的落地页会干扰
  await ev(`location.hash = '#/more'`); await sleep(1800)
  const before = await ev('location.hash')
  const clicked = await ev(fullClickText(e.text))
  await sleep(2600)
  const after = await ev('location.hash')
  const body = ((await ev(`(document.body.innerText||'').replace(/\\s+/g,' ').trim()`)) || '')
  const blank = body.length < 12
  const notFound = /404|页面未找到|not found|找不到页面/i.test(body)
  const stuck = after === before
  const verdict = stuck ? 'STUCK(点了没反应)'
    : notFound ? 'NOT_FOUND'
    : blank ? 'BLANK(空白页)'
    : 'OK'
  results.push({ label: e.text, before, after, verdict, bodyLen: body.length })
  console.log(`${verdict.padEnd(18)} ${e.text.padEnd(14)} ${String(before).padEnd(10)} -> ${after}   bodyLen=${body.length}`)
}

const bad = results.filter((r) => r.verdict !== 'OK')
console.log(`\n=== SUMMARY: ${results.length - bad.length} / ${results.length} 入口可达 ===`)
if (bad.length) {
  console.log('\n异常入口：')
  for (const b of bad) console.log(`  ${b.verdict.padEnd(18)} ${b.label.padEnd(14)} ${b.before} -> ${b.after}`)
}
process.exitCode = bad.length ? 1 : 0

/** 完整指针序列点击。只 el.click() 对部分组件无效（实测踩过）。 */
function fullClickText(text) {
  return `(function(){
    var t = ${JSON.stringify(text)};
    var nodes = Array.prototype.slice.call(document.querySelectorAll('a,button,[role="button"],li,[class*="item"]'));
    var el = nodes.find(function(n){ var x=(n.innerText||'').trim().split('\\n')[0].trim(); return x === t; })
           || nodes.find(function(n){ return (n.innerText||'').trim().split('\\n')[0].trim().indexOf(t) >= 0; });
    if (!el) return 'NOT_FOUND';
    var target = el.closest('button,a,[role="button"]') || el;
    ['pointerdown','mousedown','pointerup','mouseup','click'].forEach(function(type){
      target.dispatchEvent(new PointerEvent(type, { bubbles:true, cancelable:true, view:window }));
    });
    return 'clicked';
  })()`
}
