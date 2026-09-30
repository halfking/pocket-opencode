// 决定性判据：设备上 local_notes_fts 的**触发器到底存不存在**。
//
// 前面绕了不少弯路才走到这一步：
//   · 拉库文件 → 是 SQLCipher 加密的，静态读不了
//   · 挂 window.Capacitor.Plugins.SQLite → 插件实际注册名是 **CapacitorSQLite**，挂空了
// 现在直接问插件要 sqlite_master，这是唯一能一锤定音的观测。
//
// 判据（每条都能在「触发器缺失」一侧失败）：
//   1. local_notes_fts 虚表存在吗？—— 不存在则搜索路径整个走不通
//   2. local_notes_ai / _ad / _au 三个触发器各在不在？—— 缺 ad/au = 删改不清索引
//   3. 索引里当前有几行？与 local_notes 实际行数对不对得上？
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9284';
const DB = process.env.POCKET_SQLITE_DB || 'lobster';

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

const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value;

// 等 WebView 就绪
{
  const dl = Date.now() + 25000;
  while (Date.now() < dl) {
    const o = await ev('location.origin');
    if (o === 'http://localhost') break;
    await sleep(1000);
  }
}

// ⚠️ 刚装完 / 未登录时本地库还没打开，查询会报
// 「No available connection for database lobster」。
// 所以这里先完成登录（应用启动时才会 open 本地库），再查 sqlite_master。
const MASTER = process.env.POCKET_MASTER || '';
await ev(`location.hash = '#/login'`);
await sleep(2600);
if (await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)) {
  await ev(`(function(){var el=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(MASTER)});el.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`);
  await sleep(1700);
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('解锁')>=0});if(b)b.click();return 1})()`);
  await sleep(4200);
}
if (await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)) {
  const devPass = (readFileSync('backend/internal/server/server_assistant.go', 'utf8').match(/devPass\s*=\s*"([^"]+)"/) || [])[1] || '';
  const fill = (sel, val) => `(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`;
  await ev(fill('input[placeholder*="用户名"]', 'admin'));
  await ev(fill('input[type="password"]', devPass));
  await sleep(900);
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='登录'});if(b)b.click();return b?1:0})()`);
  await sleep(7000);
}
console.log('origin =', await ev('location.origin'), '（本地库应已随应用启动打开）');

const q = async (statement) => {
  const r = await send('Runtime.evaluate', {
    expression: `(async function(){
      try {
        var S = window.Capacitor.Plugins.CapacitorSQLite;
        if (!S) return JSON.stringify({ err: 'NO_PLUGIN' });
        var res = await S.query({ database: ${JSON.stringify(DB)}, statement: ${JSON.stringify(statement)}, values: [] });
        return JSON.stringify(res);
      } catch (e) { return JSON.stringify({ err: String(e && e.message || e) }); }
    })()`,
    returnByValue: true, awaitPromise: true,
  });
  const raw = r?.result?.value;
  if (!raw) return { err: 'no value' };
  try { return JSON.parse(raw); } catch { return { err: 'unparsable: ' + String(raw).slice(0, 200) }; }
};

const checks = [];
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`); };
const ALL_PASS = () => { checks.every((c) => c.pass) || (console.log('  失败项：\n' + checks.filter((c) => !c.pass).map((c) => '    - ' + c.n).join('\n')), false); };

console.log(`origin = ${(await (await send('Runtime.evaluate', { expression: 'location.origin', returnByValue: true }))?.result?.value)}\n`);

// 1. 先探数据库名对不对
const tables = await q("SELECT name, type FROM sqlite_master WHERE type IN ('table','trigger') AND name LIKE 'local_notes%'");
console.log('sqlite_master 原始返回 =', JSON.stringify(tables).slice(0, 700));
if (tables.err) { console.log('查询失败：', tables.err); process.exit(3); }

// ⚠️ 插件返回的行是**对象数组**（{"name":..,"type":..}），不是二维数组。
// 第一版按 r[0] 取值 → 名字集合恒为空 → 5 条判据全「失败」。
// 那次「全失败」反而是本次定性的关键线索：它让人去看原始返回，才发现
// 虚表在、触发器一个都没有。判据自己出错时的 FAIL 不等于产品有 N 个缺陷。
const rows = (tables.result && tables.result.values) || tables.values || [];
const cell = (r, k) => (Array.isArray(r) ? r[Object.keys(r).indexOf(k)] ?? r[0] : r && r[k]);
console.log(`\n匹配到 ${rows.length} 行：`);
for (const r of rows) console.log(`  ${JSON.stringify(r)}`);

const names = new Set(rows.map((r) => String(cell(r, 'name'))));
check('local_notes_fts 虚表存在', names.has('local_notes_fts'), [...names].join(','));
check('local_notes_ai（INSERT 触发器）存在', names.has('local_notes_ai'), '');
check('local_notes_ad（DELETE 触发器）存在', names.has('local_notes_ad'), '');
check('local_notes_au（UPDATE 触发器）存在', names.has('local_notes_au'), '');

// 2. 索引内容与实际数据对不对得上
const ftsRows = await q('SELECT count(*) FROM local_notes_fts');
const noteRows = await q('SELECT count(*) FROM local_notes');
const fv = JSON.stringify(ftsRows);
const nv = JSON.stringify(noteRows);
console.log(`\nfts 行数返回 = ${fv.slice(0, 200)}`);
console.log(`notes 行数返回 = ${nv.slice(0, 200)}`);

const num = (o) => {
  try {
    const v = (o.result && o.result.values) || o.values || [];
    const first = v[0];
    if (first === undefined) return NaN;
    if (Array.isArray(first)) return Number(first[0]);
    return Number(first['count(*)'] ?? Object.values(first)[0]);
  } catch { return NaN; }
};
const ftsN = num(ftsRows), noteN = num(noteRows);
check('能读出 FTS 索引行数与笔记行数（用于判断索引是否失同步）',
  Number.isFinite(ftsN) && Number.isFinite(noteN), `fts=${ftsN} notes=${noteN}`);
if (Number.isFinite(ftsN) && Number.isFinite(noteN) && noteN > 0) {
  check('索引行数与笔记行数一致（触发器若缺失，删改后必然对不上）', ftsN === noteN, `fts=${ftsN} notes=${noteN}`);
} else {
  console.log('  （当前库为空，跳过一致性判据 —— 空库时「对不上」不构成证据）');
}

const pass = checks.filter((c) => c.pass).length;
console.log(`\n=== 汇总 ===\n${pass}/${checks.length} 通过`);
process.exit(ALL_PASS() ? 0 : 1);
