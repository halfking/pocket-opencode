// 二分实验：把源码里的**原句**直接丢给插件，看究竟哪一段导致 incomplete input。
//
// 前一版实验（A~E）证明「单条含内部分号的 CREATE TRIGGER 字符串」是**能**建成的，
// 所以「插件按分号机械切分」这个假设是错的。现在用真实 DDL 复现，
// 再逐个替换可疑成分，定位到具体是哪个构造出的问题。
import { execFileSync } from 'node:child_process';

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9288';
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
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); } });
await new Promise((r) => ws.addEventListener('open', r));
await send('Runtime.enable');
const call = async (expr) => {
  const r = await send('Runtime.evaluate', { expression: `(async function(){ try { ${expr} } catch (e) { return JSON.stringify({err:String(e && e.message || e)}) } })()`, returnByValue: true, awaitPromise: true });
  const v = r?.result?.value; if (v === undefined) return { err: 'undefined' };
  try { return JSON.parse(v); } catch { return { raw: String(v).slice(0, 200) }; }
};
const exec = (statements) => call(`var S = window.Capacitor.Plugins.CapacitorSQLite; return JSON.stringify(await S.execute({ database: ${JSON.stringify(DB)}, statements: ${JSON.stringify(statements)} }));`);
const triggers = () => call(`var S = window.Capacitor.Plugins.CapacitorSQLite; return JSON.stringify(await S.query({ database: ${JSON.stringify(DB)}, statement: "SELECT name FROM sqlite_master WHERE type='trigger'", values: [] }));`);

// 源码原句（local-db.ts 与 schema.ts 一致）
const EXACT_AI = `CREATE TRIGGER IF NOT EXISTS zz_ai AFTER INSERT ON local_notes BEGIN
  INSERT INTO local_notes_fts(rowid, title, content)
  VALUES (new.rowid, new.title, COALESCE(NULLIF(new.search_text, ''), new.content));
END;`;

const cases = [
  { n: '1 源码原句（多行 + 真实表达式）', s: EXACT_AI },
  { n: '2 压成一行', s: EXACT_AI.replace(/\s+/g, ' ').trim() },
  { n: '3 去掉 COALESCE/NULLIF', s: `CREATE TRIGGER IF NOT EXISTS zz_ai AFTER INSERT ON local_notes BEGIN
  INSERT INTO local_notes_fts(rowid, title, content)
  VALUES (new.rowid, new.title, new.content);
END;` },
  { n: '4 去掉内层空串字面量', s: `CREATE TRIGGER IF NOT EXISTS zz_ai AFTER INSERT ON local_notes BEGIN
  INSERT INTO local_notes_fts(rowid, title, content)
  VALUES (new.rowid, new.title, COALESCE(new.search_text, new.content));
END;` },
  { n: '5 只有内层语句是 SELECT（对照）', s: `CREATE TRIGGER IF NOT EXISTS zz_ai AFTER INSERT ON local_notes BEGIN
  SELECT new.rowid;
END;` },
  { n: '6 CRLF 换行', s: EXACT_AI.replace(/\n/g, '\r\n') },
  // 判别性用例：同样是**多行**、体内也**有分号**，
  // 但分号后面跟的是空格而不是换行。按「原生层按字面量 ;\n 切分」的假设，
  // 它必须能建成 —— 这一条能把「多行」与「分号+换行」两个变量分开。
  { n: '7 多行但分号后跟空格（判别用例）', s: `CREATE TRIGGER IF NOT EXISTS zz_ai AFTER INSERT ON local_notes BEGIN
  INSERT INTO local_notes_fts(rowid, title, content)
  VALUES (new.rowid, new.title, new.content); END;` },
  // 反向判别：压成一行，但体内保留一个 ;\n（把分号和换行重新拼回去）
  { n: '8 单行但体内含 ;\\n（反向判别）', s: 'CREATE TRIGGER IF NOT EXISTS zz_ai AFTER INSERT ON local_notes BEGIN\nINSERT INTO local_notes_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);\nEND;' },
];

for (const c of cases) {
  await exec('DROP TRIGGER IF EXISTS zz_ai;');
  const r = await exec(c.s);
  const t = await triggers();
  const names = (t.values || []).map((x) => x.name);
  const ok = names.includes('zz_ai');
  console.log(`${ok ? '✅ 建成' : '❌ 失败'}  ${c.n}`);
  if (!ok) console.log(`        err = ${String((r && r.err) || JSON.stringify(r)).slice(0, 200)}`);
}
await exec('DROP TRIGGER IF EXISTS zz_ai;');
console.log('\n清理后 triggers =', JSON.stringify(await triggers()).slice(0, 200));
