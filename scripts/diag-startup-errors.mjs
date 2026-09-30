// 定性用：设备启动期那 9 条 console.error 到底是谁抛的。
//
// 上一轮只拿到 message（"Execute: incomplete input (code 1) …"），
// 无法区分是 schema.ts 的批量建表路径还是 local-db.ts 的迁移路径抛的 ——
// 两处都含同一段触发器 DDL，文本上看不出来。
//
// CDP 的 Runtime.consoleAPICalled 带 stackTrace.callFrames，
// 直接给出 sourceURL + lineNumber + columnNumber。这一条就能定位到文件与行。
import { execFileSync } from 'node:child_process';

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9281';

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
let id = 0; const pending = new Map(); const hits = [];
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) });

// 整个 args 数组序列化。逐个参数取 value/description/preview 的写法
// 在这个场景下四条兜底全落空，只吐一个 "Object"，等于没诊断。
const argText = (a) => {
  if (a && a.preview && a.preview.properties && a.preview.properties.length) {
    return a.preview.properties.map((p) => `${p.name}:${p.value}`).join(' ');
  }
  if (a && a.value !== undefined) return typeof a.value === 'string' ? a.value : JSON.stringify(a.value);
  if (a && a.description && a.description !== 'Object') return a.description;
  try { return JSON.stringify(a); } catch { return String(a); }
};

ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {};
  if (m.method === 'Runtime.consoleAPICalled' && p.type === 'error') {
    hits.push({
      args: (p.args || []).map(argText).join(' ').slice(0, 260),
      frames: ((p.stackTrace && p.stackTrace.callFrames) || []).slice(0, 6).map((f) => ({
        file: String(f.url || '').split('/').slice(-1)[0],
        line: (f.lineNumber ?? -1) + 1,
        col: (f.columnNumber ?? -1) + 1,
        fn: f.functionName || '(anonymous)',
      })),
    });
  }
});
await new Promise((r) => ws.addEventListener('open', r));
await send('Runtime.enable');
await send('Log.enable');

// 强制重载，让启动期的报错重新发生
await send('Page.enable');
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value;
await ev('location.reload()');

const dl = Date.now() + 40000;
while (Date.now() < dl) { await sleep(1000); if ((await ev('location.origin')) === 'http://localhost') break }
await sleep(6000);

console.log(`\n捕获 console.error ${hits.length} 条：\n`);
const bySite = new Map();
for (const [i, h] of hits.entries()) {
  console.log(`--- [${i}] ${h.args}`);
  for (const f of h.frames) console.log(`      ${f.fn}  @ ${f.file}:${f.line}:${f.col}`);
  const top = h.frames[0];
  if (top) bySite.set(`${top.file}:${top.line}`, (bySite.get(`${top.file}:${top.line}`) || 0) + 1);
  if (!h.frames.length) console.log('      (无堆栈)');
  console.log('');
}

console.log('=== 按抛出点归并 ===');
if (bySite.size === 0) console.log('没有任何堆栈 —— 报错不是从业务代码 throw 出来的。');
for (const [site, n] of bySite) console.log(`  ${site}  ×${n}`);

// 只关心 SQLite 编译失败那类
const sqlHits = hits.filter((h) => /incomplete input|Execute:/.test(h.args));
console.log(`\n=== SQLite 编译失败类：${sqlHits.length} 条 ===`);
const sites = new Set();
for (const h of sqlHits) for (const f of h.frames) sites.add(`${f.file}:${f.line} (${f.fn})`);
for (const s of sites) console.log('  ' + s);
