// 诊断：https origin 下记账页「刷新后整个视图消失」到底发生了什么。
//
// 现象（同一脚本两次跑，两次不同）：
//   跑1  读路径 0 张卡，其余 24/26
//   跑2  卡片 0 张 + .quick-btn 消失 + .quick-input 消失（「页面就位」却刚通过）
// 「页面就位」在刷新点击后立刻判过，之后视图就没了 —— 说明**组件被卸载**，
// 不是数据没到。所以这里只量三件事：当前路由、DOM 里还剩什么、页面在说什么。
// 不猜、不改代码，先把事实摆出来。
import { execFileSync } from 'node:child_process';

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9300';
const MASTER = process.env.POCKET_MASTER || '';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 });
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0];
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean);
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`]);
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page');
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`));
let id = 0; const pending = new Map(); const exs = []; const cons = [];
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) });
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {};
  if (m.method === 'Runtime.exceptionThrown') exs.push((p.exceptionDetails?.exception?.description || p.exceptionDetails?.text || '').slice(0, 300));
  if (m.method === 'Runtime.consoleAPICalled' && (p.type === 'error' || p.type === 'warning')) {
    const t = (p.args || []).map((a) => (a.value ?? a.description ?? '')).join(' ').slice(0, 220);
    if (t) cons.push(`[${p.type}] ${t}`);
  }
});
await new Promise((r) => ws.addEventListener('open', r));
await send('Runtime.enable');
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value;

const snap = async (tag) => {
  const info = await ev(`(function(){
    var panes = document.querySelectorAll('.inner-pane, .outer-pane');
    var pane = null;
    for (var i=0;i<panes.length;i++){ if(panes[i].offsetParent!==null){pane=panes[i];break;} }
    var root = pane || document.body;
    return JSON.stringify({
      hash: location.hash,
      pane: pane ? (pane.className||'') : '(无可见 pane)',
      quickInput: !!root.querySelector('.quick-input'),
      quickBtn: !!root.querySelector('.quick-btn'),
      cards: root.querySelectorAll('.tx-card').length,
      statsCard: !!root.querySelector('.stats-card'),
      quickAdd: !!root.querySelector('.quick-add'),
      bodyHead: (document.body.innerText||'').replace(/\\s+/g,' ').slice(0,180)
    });
  })()`);
  console.log(`\n--- ${tag} ---\n${info}`);
  return info ? JSON.parse(info) : null;
};

console.log('origin =', await ev('location.origin'));
await snap('起始');

// 复现脚本的步骤：跳到 #/finance，等 1.8s，点刷新
await ev(`location.hash = '#/finance'`);
await sleep(1800);
const clicked = await ev(`(function(){var b=document.querySelector('button[aria-label="刷新"]');if(!b)return 'NO_BTN';b.click();return 'clicked'})()`);
console.log('\n刷新点击 =', clicked);
await snap('刷新后立刻');
await sleep(1500);
await snap('刷新后 1.5s');
await sleep(6000);
await snap('刷新后 7.5s');

console.log('\n=== 捕获到的异常 ===');
if (!exs.length) console.log('  （无）');
exs.slice(0, 6).forEach((x, i) => console.log(`  [${i}] ${x}`));
console.log('\n=== console error/warning ===');
if (!cons.length) console.log('  （无）');
cons.slice(0, 10).forEach((x, i) => console.log(`  [${i}] ${x}`));
