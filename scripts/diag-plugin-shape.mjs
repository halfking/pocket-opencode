// 探明页面里 SQLite 插件的真实形态，为挂钩找准对象。
// 上一版挂在 window.Capacitor.Plugins.SQLite 上，捕获 0 条 —— 说明
// local-db.ts 用的是模块内 import 的 CapacitorSQLite 单例，与那个对象不是同一个。
import { execFileSync } from 'node:child_process';

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9283';

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
let id = 0; const pending = new Map();
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) });
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); }
});
await new Promise((r) => ws.addEventListener('open', r));
await send('Runtime.enable');
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value;

console.log('origin =', await ev('location.origin'));
console.log('window.Capacitor        =', await ev('typeof window.Capacitor'));
console.log('Capacitor.isNativePlatform =', await ev('window.Capacitor ? window.Capacitor.isNativePlatform : "n/a"'));
console.log('Plugins 名单 =', await ev('window.Capacitor && window.Capacitor.Plugins ? Object.keys(window.Capacitor.Plugins).join(", ") : "(无)"'));
console.log('SQLite 插件存在 =', await ev('window.Capacitor && window.Capacitor.Plugins ? typeof window.Capacitor.Plugins.SQLite : "(无)"'));
console.log('jeepSqliteElement =', await ev('typeof window.jeepSqliteElement'));
console.log('cap-android-bridge =', await ev('!!(window.Capacitor && window.Capacitor.isNativePlatform && window.Capacitor.PluginHeaders)'));
console.log('PluginHeaders =', await ev('window.Capacitor && window.Capacitor.PluginHeaders ? Object.keys(window.Capacitor.PluginHeaders).join(", ") : "(无)"'));

console.log('\n--- 尝试：直接问插件有哪些方法 ---');
console.log(await ev(`(function(){
  try {
    var S = window.Capacitor.Plugins.SQLite;
    if (!S) return '(无 SQLite 插件)';
    var names = [];
    for (var k in S) { names.push(k + (typeof S[k])); }
    return names.join(', ');
  } catch (e) { return 'ERR ' + e.message; }
})()`));

console.log('\n--- 能否直接跑一条只读查询（看 FTS 表与触发器在不在） ---');
const q = await send('Runtime.evaluate', {
  expression: `(async function(){
    try {
      var S = window.Capacitor.Plugins.SQLite;
      if (!S) return 'NO_PLUGIN';
      var r = await S.query({ database: 'lobster', statement: "SELECT name,type FROM sqlite_master WHERE type IN ('trigger','table') AND name LIKE 'local_notes%'" });
      return JSON.stringify(r).slice(0, 600);
    } catch (e) { return 'ERR ' + (e && e.message || e); }
  })()`,
  returnByValue: true, awaitPromise: true,
});
console.log(q?.result?.value ?? JSON.stringify(q));
