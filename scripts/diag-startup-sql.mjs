// 定性用（第二手）：把 App 启动时实际发给 SQLite 插件的 SQL 逐条记下来。
//
// 为什么不能直接读库文件：设备上的 databases/lobsterSQLite.db 是 **SQLCipher 加密**的
// （shared_prefs/sqlite_encrypted_shared_prefs.xml 存在，文件头也不是 "SQLite format 3"），
// 拉下来是密文，静态查不了 sqlite_master。
//
// 于是改用运行时观测：Page.addScriptToEvaluateOnNewDocument 在**任何页面脚本之前**注入，
// 把 Capacitor SQLite 插件的 execute/run/select 包一层，记录
//   「语句前 120 字符 → 返回 ok / 错误信息」
// 这样就能直接回答三个问题：
//   1. 启动时到底执行了哪些 CREATE TRIGGER？
//   2. 哪一条报错？
//   3. 那几条报错之后，触发器到底建没建成（有没有重试/后续语句）？
import { execFileSync } from 'node:child_process';

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9282';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 });

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0];
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean);
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`]);
const pages = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
const page = pages.find((t) => t.type === 'page');
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`));
let id = 0; const pending = new Map();
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) });
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
});
await new Promise((r) => ws.addEventListener('open', r));
await send('Runtime.enable');
await send('Page.enable');

// 在任何页面脚本之前挂钩
const HOOK = `
(() => {
  window.__sqlLog = [];
  const rec = (method, args) => {
    let stmt = '';
    try {
      const a0 = args && args[0];
      stmt = (a0 && (a0.statement || a0.query || a0.sql)) || (typeof a0 === 'string' ? a0 : '');
    } catch (e) { stmt = '<unreadable>'; }
    const entry = { method, stmt: String(stmt).replace(/\\s+/g, ' ').trim().slice(0, 200), ok: null, err: null };
    window.__sqlLog.push(entry);
    try {
      const p = orig.apply(null, args);
      if (p && typeof p.then === 'function') {
        return p.then((r) => {
          entry.ok = !!(r && (r.result === undefined ? true : (r.result && r.result.rows ? true : true)));
          return r;
        }, (e) => { entry.ok = false; entry.err = (e && (e.message || e.code)) ? String(e.message || e.code) : String(e); throw e; });
      }
      return p;
    } catch (e) { entry.ok = false; entry.err = String(e && e.message || e); throw e; }
  };
  const install = () => {
    const P = window.Capacitor && window.Capacitor.Plugins;
    if (!P) return false;
    const S = P.SQLite;
    if (!S || S.__hooked) return !!S;
    ['execute', 'executeSet', 'run', 'query', 'select'].forEach((m) => {
      if (typeof S[m] !== 'function') return;
      const orig = S[m].bind(S);
      S.__hooked = true;
      S[m] = function (...args) { return rec(m, args); };
      S[m].__orig = orig;
    });
    return true;
  };
  const iv = setInterval(() => { if (install()) clearInterval(iv); }, 5);
})();
`;
const r = await send('Page.addScriptToEvaluateOnNewDocument', { source: HOOK });
if (r && r.identifier) console.log('注入标识 =', r.identifier);

const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value;
await ev('location.reload()');

const dl = Date.now() + 40000;
while (Date.now() < dl) { await sleep(1000); if ((await ev('location.origin')) === 'http://localhost') break }
await sleep(8000);

const log = await ev('JSON.stringify(window.__sqlLog || [])');
const entries = log ? JSON.parse(log) : [];
console.log(`\n捕获 SQL 调用 ${entries.length} 条\n`);

const trig = entries.filter((e) => /CREATE\s+TRIGGER/i.test(e.stmt));
const failed = entries.filter((e) => e.ok === false);
console.log('=== CREATE TRIGGER 相关 ===');
for (const [i, e] of trig.entries()) {
  console.log(`[${i}] ok=${e.ok} ${e.err ? 'ERR=' + e.err : ''}`);
  console.log(`     ${e.stmt.slice(0, 190)}`);
}
console.log(`\n共 ${trig.length} 条 CREATE TRIGGER，其中报错 ${trig.filter((e) => e.ok === false).length} 条`);

console.log(`\n=== 全部报错调用（${failed.length} 条） ===`);
for (const e of failed) {
  console.log(`  ${e.method}: ${e.stmt.slice(0, 120)}`);
  console.log(`     err = ${String(e.err).slice(0, 180)}`);
}
if (failed.length === 0) console.log('  （本次没有捕到报错 —— 说明挂钩没装上，或错误不走这几个方法）');

console.log(`\n=== 前 25 条调用概览 ===`);
entries.slice(0, 25).forEach((e, i) => console.log(`  ${String(i).padStart(2)} ${e.method.padEnd(9)} ok=${e.ok}  ${e.stmt.slice(0, 90)}`));
