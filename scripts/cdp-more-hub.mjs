#!/usr/bin/env node
/**
 * cdp-more-hub.mjs — 在真机 WebView 里读「更多」页的入口列表。
 *
 * 用途：BUG-P（2026-09-30）——闪卡路由存在、功能全修好，但「更多」页没有它的
 * 入口，用户在正常 UI 导航下进不去。之前三轮验收都用 CDP 直接改 location.hash
 * 绕过了真实入口，所以一直没暴露。
 *
 * 这个探针专门用来**从真实 UI 入口**核对可达性：点「更多」，把九宫格里的
 * to 目标与文案全部读出来。哈希表里的路由在列表里找不到 = 用户到不了。
 *
 * 用法：POCKET_SERIAL=... node scripts/cdp-more-hub.mjs
 */
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9241'
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

// 走到「更多」页
await ev(`location.hash = '#/more'`)
await sleep(3500)
const gotMore = await ev(`!!document.querySelector('a[href*="flashcards"], [href*="/more"]') || location.hash`)
console.log('hash =', await ev('location.hash'))

// 读九宫格入口。
//
// 第一版只抓 `a[href]` / [role=button]，结果只捞到底部导航 5 项 —— 九宫格是
// **点击事件绑定的元素、没有 href**，所以漏掉了。那版输出「闪卡入口缺失」是
// **探针不完整**，不是产品结论。已改成按可见文案抓，覆盖无 href 的情况。
const entries = await ev(`JSON.stringify((function(){
  var out = [];
  // 九宫格条目：既可能是 <a>，也可能是 div/button + click
  var nodes = document.querySelectorAll('a, button, [role="button"], li, article, .hub-item, [class*="grid"] > *');
  Array.prototype.forEach.call(nodes, function(n){
    var txt = (n.innerText || n.textContent || '').trim();
    if (!txt || txt.length > 40) return;
    var href = n.getAttribute && n.getAttribute('href');
    out.push({ href: href || '', text: txt.split('\\n')[0].trim().slice(0, 26) });
  });
  // 去重
  var seen = {}, uniq = [];
  out.forEach(function(o){ var k = o.text + '|' + o.href; if (!seen[k]) { seen[k] = 1; uniq.push(o); } });
  return uniq;
})())`)
const list = JSON.parse(entries || '[]')
console.log(`\n「更多」页可见入口 ${list.length} 个：`)
for (const e of list) console.log(`  ${(e.href || '(no href)').padEnd(26)} ${e.text}`)

// 判据：入口条目里出现闪卡文案即视为可达。模拟器截图里的 10 项九宫格
// （Chat / PKM Notes / Email / RSS / Vault / Scheduled Automation /
//   Skill Market / Agent Market / Local Agent / Workbuddy）就是这种形态。
const FLASHCARD_WORDS = /闪卡|Flashcard|単語|플래시/i
const hasFlashcards = list.some((e) => FLASHCARD_WORDS.test(e.text) || e.href.indexOf('/flashcards') >= 0)

// 完整性自检：如果九宫格一个都没抓到，说明选择器又失效了，不能报"缺失"。
const gridItems = list.filter((e) => !/^(home|study|meetings|more|首页|学习|会议|更多)$/i.test(e.text))
if (gridItems.length === 0) {
  console.log('\nFATAL: 一个九宫格条目都没抓到 —— 探针选择器失效，**本轮不能给出"缺失"结论**。')
  process.exit(2)
}
console.log(`\n九宫格条目 ${gridItems.length} 个（已排除底部导航）`)
console.log(`闪卡入口: ${hasFlashcards ? '存在 ✅' : '缺失 ❌ 用户无法从 UI 到达闪卡模块'}`)
console.log('（BUG-P 的判据。路由存在 ≠ 用户能到 —— 前三轮验收都用 CDP 改 hash 绕过了入口）')
process.exitCode = hasFlashcards ? 0 : 1
