// 实验：在这台真机的**活的** SQLite 插件上，测哪种写法能真正建出触发器。
//
// 为什么要做实验而不是直接改代码重建：SCHEMA_SQL 那条路已经用
// splitSqlStatements 正确切分、且每次打开都跑，但触发器依然不存在。
// 说明问题不在切分，而在「插件怎么解析一条含内部分号的 CREATE TRIGGER」。
// 改完再重建、再装机验证，一轮 5~10 分钟；先在活的插件上把几种写法各试一次，
// 一分钟就能知道哪种是有效的。
import { execFileSync } from 'node:child_process';

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9286';
const DB = 'lobster';

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

const call = async (expr) => {
  const r = await send('Runtime.evaluate', { expression: `(async function(){ try { ${expr} } catch (e) { return JSON.stringify({err:String(e && e.message || e)}) } })()`, returnByValue: true, awaitPromise: true });
  const v = r?.result?.value;
  if (v === undefined) return { err: 'undefined' };
  try { return JSON.parse(v); } catch { return { raw: String(v).slice(0, 200) }; }
};

// ⚠️ 选项字段是 **statements**（复数），不是 statement。
// 第一版传成 statement，五种写法全报 "Must provide raw SQL statements" ——
// 那是**参数没送到**，不是「这些写法都不行」。判据自身出错的典型。
const execute = (statements, extra = '') => call(
  `var S = window.Capacitor.Plugins.CapacitorSQLite;
   return JSON.stringify(await S.execute(Object.assign({ database: ${JSON.stringify(DB)}, statements: ${JSON.stringify(statements)} }, ${extra || '{}'})));`);

const listTriggers = () => call(
  `var S = window.Capacitor.Plugins.CapacitorSQLite;
   return JSON.stringify(await S.query({ database: ${JSON.stringify(DB)}, statement: "SELECT name FROM sqlite_master WHERE type='trigger'", values: [] }));`);

const S = 'SELECT new.rowid, new.title FROM local_notes LIMIT 0'; // 占位，避免误建
const oneStmt = "CREATE TRIGGER IF NOT EXISTS zz_probe_one AFTER INSERT ON local_notes BEGIN SELECT 1; END;";

console.log('=== 起始状态 ===');
console.log('triggers =', JSON.stringify(await listTriggers()).slice(0, 300));

const variants = [
  { name: 'A 单条字符串（内含分号，末尾带 ;）', run: () => execute(oneStmt) },
  { name: 'B 单条字符串（内含分号，末尾不带 ;）', run: () => execute(oneStmt.replace(/;\s*$/, '')) },
  { name: 'C 数组传多条（每条一个完整触发器）', run: () => execute(JSON.stringify([oneStmt]), '') },
  { name: 'D executeSet（Set 形式）', run: () => call(
      `var S = window.Capacitor.Plugins.CapacitorSQLite;
       return JSON.stringify(await S.executeSet({ database: ${JSON.stringify(DB)}, set: [{ statement: ${JSON.stringify(oneStmt)} }] }));`) },
  { name: 'E 无内部分号的触发器（单语句体）', run: () => execute("CREATE TRIGGER IF NOT EXISTS zz_probe_two AFTER INSERT ON local_notes BEGIN SELECT 1 END;") },
];

const results = [];
for (const v of variants) {
  const r = await v.run();
  const t = await listTriggers();
  const names = (t.values || []).map((x) => x.name);
  const built = names.filter((n) => n.startsWith('zz_probe'));
  results.push({ name: v.name, r, built });
  console.log(`\n${v.name}`);
  console.log(`   返回 = ${JSON.stringify(r).slice(0, 180)}`);
  console.log(`   建出来的探针触发器 = ${JSON.stringify(built)}`);
}

// 清理
await execute('DROP TRIGGER IF EXISTS zz_probe_one;');
await execute('DROP TRIGGER IF EXISTS zz_probe_two;');
console.log('\n=== 清理后 ===');
console.log('triggers =', JSON.stringify(await listTriggers()).slice(0, 300));

console.log('\n=== 结论 ===');
for (const r of results) console.log(`  ${r.built.length ? '✅' : '❌'} ${r.name}`);
const winner = results.find((r) => r.built.length > 0);
console.log(winner ? `\n可用写法：${winner.name}` : '\n五种写法都没建出来 —— 需要换思路（例如不在设备上建触发器，改由应用层维护索引）');
