#!/usr/bin/env node
/**
 * verify-instances-readpath.mjs — **真机**验证实例模块的读路径。
 *
 * ## 范围（这一条很重要）
 * 实例模块是**只读设计**：`InstanceListView.vue` 只有刷新与选择，没有创建表单；
 * 后端也没有任何创建/删除实例的 handler。所以「实例的 UI 写路径」是范畴错误，
 * 之前把它列进「待验证写路径」本身就是范围定错了。本脚本验的是：
 *
 *   1. 读路径：列表真的渲染出服务端数据，且**逐字段**与 API 返回一致
 *   2. 契约形状：UI 依赖的 displayName / id / environment / capabilities 都在
 *      （缺一个，页面上就会显示成 `undefined` —— 这类缺陷文本判据抓不到）
 *   3. 刷新是真刷新：点 🔄 会**再次**发起 GET /api/instances（不是空转按钮）
 *   4. 选择是本模块唯一的「写」：点卡片 → 落 localStorage 两把键 + 路由到 /tasks
 *   5. 诚实性：没有未捕获异常，卡片上不出现 `undefined` / `null`
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/verify-instances-readpath.mjs
 */
import { execFileSync } from 'node:child_process';
import { requireDevPass } from './lib/dev-pass.mjs'
import http from 'node:http';

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9272';
const MASTER = process.env.POCKET_MASTER || '';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 });

function api(path, { token, method = 'GET', body } = {}) {
  return new Promise((res) => {
    const payload = body ? JSON.stringify(body) : '';
    const h = {};
    if (token) h.Authorization = 'Bearer ' + token;
    if (payload) { h['Content-Type'] = 'application/json'; h['Content-Length'] = Buffer.byteLength(payload); }
    const r = http.request({ host: '127.0.0.1', port: Number(process.env.POCKET_API_PORT || 8088), path, method, headers: h }, (resp) => {
      let s = ''; resp.on('data', (c) => (s += c)); resp.on('end', () => res({ status: resp.statusCode, body: s }));
    });
    r.on('error', (e) => res({ status: 'ERR', body: e.message }));
    if (payload) r.write(payload);
    r.end();
  });
}
const devPass = requireDevPass()

// ---------- CDP ----------
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0];
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean);
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`]);
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page');
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`));
let id = 0; const pending = new Map(); const errors = []; const consoleErrs = []; const apiLog = [];
// console/exception 只在 reload 之后收：reload 前的页面（本轮之前跑过别的脚本）
// 留下的报错会被算到这一轮头上，判据就成了「历史噪声」。
let collecting = false;
const argText = (a) => {
  if (a && a.value !== undefined) return typeof a.value === 'string' ? a.value : JSON.stringify(a.value);
  if (a && a.description) return a.description;
  if (a && a.preview && a.preview.properties) {
    return '{' + a.preview.properties.map((p) => `${p.name}:${p.value}`).join(',') + '}';
  }
  // CDP 的 RemoteObject 可能是 Error / 对象：上面都取不到就整个序列化，
  // 至少能看出是什么类型，别让判据只吐一个 "Object"（第一版就是这样）。
  try { return JSON.stringify(a); } catch { return String(a); }
};
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) });
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {};
  if (m.method === 'Network.requestWillBeSent' && String(p.request?.url || '').includes('/api/instances')) {
    apiLog.push({ url: p.request.url.replace(/https?:\/\/[^/]+/, ''), method: p.request.method, status: null });
  }
  if (m.method === 'Network.responseReceived' && String(p.response?.url || '').includes('/api/instances')) {
    const url = p.response.url.replace(/https?:\/\/[^/]+/, '');
    const hit = apiLog.find((r) => r.url === url && r.status === null);
    if (hit) hit.status = p.response.status;
  }
  if (!collecting) return;
  if (m.method === 'Runtime.exceptionThrown') errors.push(p.exceptionDetails?.exception?.description || p.exceptionDetails?.text || '');
  if (m.method === 'Runtime.consoleAPICalled' && p.type === 'error') {
    // 整个 args 数组序列化。前一版逐个参数取 value/description/preview，
    // 四条兜底全落空只吐出 "Object"，等于没诊断。
    try { consoleErrs.push(JSON.stringify(p.args).slice(0, 400)); }
    catch { consoleErrs.push('<args 不可序列化>'); }
  }
});
await new Promise((r) => ws.addEventListener('open', r));
await send('Runtime.enable');
await send('Network.enable');
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value;

let origin = null;
const readyDl = Date.now() + 20000;
while (Date.now() < readyDl) { origin = await ev('location.origin'); if (origin && origin !== 'null') break; await sleep(500) }
// 默认要求 dev 包（http://localhost）。生产 https 回归时用
// POCKET_EXPECT_ORIGIN=https://localhost 放宽。
const EXPECT_ORIGIN = process.env.POCKET_EXPECT_ORIGIN || 'http://localhost';
console.log('origin =', origin, `（本次要求 ${EXPECT_ORIGIN}）`);
if (origin !== EXPECT_ORIGIN) { console.log(`origin 与预期不符（要求 ${EXPECT_ORIGIN}），中止。`); process.exit(5) }

// 每轮 reload，避免上一轮的 DOM 改动泄漏（记账那轮踩过）
await ev('location.reload()');
const rlDl = Date.now() + 30000;
while (Date.now() < rlDl) { await sleep(1000); if ((await ev('location.origin')) === EXPECT_ORIGIN) break }
await sleep(2500);
collecting = true;   // 从这里开始才算这一轮的异常/console 噪声

// ---------- 登录 ----------
await ev(`location.hash = '#/login'`); await sleep(2600);
if (await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)) {
  await ev(`(function(){var el=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(MASTER)});el.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`);
  await sleep(1700);
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('解锁')>=0});if(b)b.click();return 1})()`);
  await sleep(4200);
}
if (await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)) {
  const fillBy = (sel, val) => `(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`;
  await ev(fillBy('input[placeholder*="用户名"]', 'admin'));
  await ev(fillBy('input[type="password"]', devPass)); await sleep(900);
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='登录'});if(b)b.click();return b?1:0})()`);
  await sleep(6500);
}

const checks = [];
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`) };

const PANE = `(function(){var ps=document.querySelectorAll('.inner-pane, .outer-pane');for(var i=0;i<ps.length;i++){if(ps[i].offsetParent!==null)return ps[i];}return document.body;})()`;

// ---------- 0. API 基线（直连，不经过 UI） ----------
const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: devPass } });
let token = null;
try { token = JSON.parse(login.body).token } catch { /* below */ }
if (!token) { console.log('登录不通 ' + login.status); process.exit(6) }
const listRes = await api('/api/instances', { token });
let baseInsts = [];
try { baseInsts = JSON.parse(listRes.body).instances || []; } catch { /* below */ }
console.log(`API 基线：GET /api/instances -> ${listRes.status}，n=${baseInsts.length}`);
console.log('  实例 =', JSON.stringify(baseInsts.map((i) => ({ id: i.id, name: i.displayName, env: i.environment, caps: (i.capabilities || []).length }))));
check('API 基线可达且结构完整（读路径判据的前提）', listRes.status === 200 && Array.isArray(baseInsts) && baseInsts.length > 0,
  `status=${listRes.status} n=${baseInsts.length}`);

// ---------- 1. 进实例页 ----------
// 记下 console 计数基线：下面「读路径期间无 error」只看**进实例页之后**的增量。
// 第一版直接判「全流程无 console.error」，结果把**启动期**就存在的
// 笔记 FTS 触发器 DDL 报错算到了实例模块头上 —— 判据范围定错了，
// 不是实例模块的锅，也不是把判据改松就完事（那样就查不出来了）。
const consoleMark = consoleErrs.length;
const startupErrs = consoleErrs.slice(0, consoleMark);
if (startupErrs.length) {
  console.log(`\n⚠️ 启动期已有 ${startupErrs.length} 条 console.error（**不计入实例验收**，另见 handoff §4.34）：`);
  startupErrs.slice(0, 3).forEach((s) => console.log('     ' + s.replace(/\s+/g, ' ').slice(0, 220)));
}
await ev(`location.hash = '#/instances'`);
const dl = Date.now() + 15000;
while (Date.now() < dl && (await ev('location.hash')) !== '#/instances') await sleep(300);
await sleep(2000);
// 已经在该路由时 hash 赋值不触发 onMounted —— 无条件点一次刷新
const refreshed = await ev(`(function(){var b=document.querySelector('button[aria-label="刷新"]');if(!b)return 'NO_BTN';b.click();return 'clicked'})()`);
console.log('   强制刷新 =', refreshed);
await sleep(1500);

const paneOk = await ev(`!!(${PANE}).querySelector('.instance-list-view')`);
check('页面就位：实例列表视图已渲染（缺失即 FAIL，不许空过）', paneOk === true, `instance-list-view=${paneOk}`);

// ---------- 2. 读路径：卡片数与 API 一致 ----------
let cardCount = 0;
const cdl = Date.now() + 15000;
while (Date.now() < cdl) {
  cardCount = await ev(`(${PANE}).querySelectorAll('.instance-card').length`);
  if (cardCount > 0) break;
  await sleep(600);
}
console.log(`   UI 卡片数 = ${cardCount}，API 实例数 = ${baseInsts.length}`);
check('读路径：UI 卡片数与 API 返回的实例数一致', cardCount === baseInsts.length, `UI=${cardCount} API=${baseInsts.length}`);

// ---------- 3. 逐字段一致（抓 undefined 类缺陷） ----------
const cards = await ev(`(function(){
  var cs=(${PANE}).querySelectorAll('.instance-card');
  var out=[];
  for(var i=0;i<cs.length;i++) out.push({
    title:(cs[i].querySelector('h3')||{}).textContent||'',
    id:(cs[i].querySelector('.instance-id')||{}).textContent||'',
    meta:(cs[i].querySelector('.instance-meta')||{}).textContent||''
  });
  return JSON.stringify(out);
})()`);
const cardList = cards ? JSON.parse(cards) : [];
console.log('   卡片 =', JSON.stringify(cardList));
const allMatch = baseInsts.every((inst) => {
  const c = cardList.find((x) => x.id === inst.id);
  return !!c
    && c.title === inst.displayName
    && c.meta.includes(inst.environment)
    && c.meta.includes(String((inst.capabilities || []).length));
});
check('逐字段一致：displayName / id / environment / 功能数 都对得上', allMatch,
  cardList.length ? JSON.stringify(cardList[0]) : '(无卡片)');

const hasUndefined = cardList.some((c) => /undefined|null|NaN/.test(JSON.stringify(c)));
check('⚠️ 卡片上不出现 undefined / null / NaN（契约形状缺字段的典型症状）', !hasUndefined,
  hasUndefined ? JSON.stringify(cardList) : '干净');

// ---------- 4. 刷新是真刷新 ----------
apiLog.length = 0;
await ev(`(function(){var b=document.querySelector('button[aria-label="刷新"]');if(b)b.click();return 1})()`);
// ⚠️ 第一版在「看到 requestWillBeSent」时就 break，而 responseReceived 还没到，
// 于是 status 永远是 null，判据自己造出一个假失败。
// 这里必须**等到 status 落定**（或超时），才判 2xx。
const rdl = Date.now() + 15000;
let getReq = null;
while (Date.now() < rdl) {
  getReq = apiLog.find((r) => r.method === 'GET' && r.status !== null) || null;
  if (getReq) break;
  await sleep(400);
}
const getCount = apiLog.filter((r) => r.method === 'GET').length;
console.log('   刷新后的 API 往返 =', JSON.stringify(apiLog));
check('刷新是真刷新：点 🔄 后又发了一次 GET /api/instances（不是空转按钮）', getCount > 0,
  `发出 ${getCount} 次 GET`);
check('列表请求 2xx（不是被服务器拒绝）', !!getReq && getReq.status >= 200 && getReq.status < 300,
  getReq ? `status=${getReq.status}` : `（status 未落定，apiLog=${JSON.stringify(apiLog)}）`);

// ---------- 5. 选择（本模块唯一的「写」路径） ----------
await ev(`localStorage.removeItem('selected_instance'); localStorage.removeItem('selected_instance_id'); 1`);
const selClick = await ev(`(function(){
  var c=(${PANE}).querySelector('.instance-card');
  if(!c) return 'NO_CARD';
  c.click(); return 'clicked';
})()`);
check('实例卡片点得动（缺失即 FAIL，不许空过）', selClick === 'clicked', `result=${selClick}`);

// 等路由落到 /tasks —— 确定性信号，不用文案子串
const tdl = Date.now() + 15000;
let onTasks = false;
while (Date.now() < tdl) { onTasks = (await ev('location.hash')) === '#/tasks'; if (onTasks) break; await sleep(500) }
check('选中后路由跳到 /tasks', onTasks === true, `hash=${await ev('location.hash')}`);

const persisted = await ev(`(function(){
  var o=localStorage.getItem('selected_instance');
  var id=localStorage.getItem('selected_instance_id');
  return JSON.stringify({obj:o?JSON.parse(o):null, id:id});
})()`);
const ps = persisted ? JSON.parse(persisted) : { obj: null, id: null };
console.log('   持久化 =', JSON.stringify(ps));
check('选中真的落盘：selected_instance / selected_instance_id 都写了',
  !!ps.obj && !!ps.id, `id=${ps.id}`);
check('落盘内容与 API 返回的实例对得上', !!ps.obj && baseInsts.some((i) => i.id === ps.id && i.displayName === ps.obj.displayName),
  `obj=${JSON.stringify(ps.obj)}`);

// ---------- 6. 异常与诚实性 ----------
check('无未捕获 JS 异常', errors.length === 0, errors.slice(0, 2).join(' | ') || '0 条');
// 只判**进实例页之后**新增的 console.error（见 §1 的 consoleMark 说明）。
const instErrs = consoleErrs.slice(consoleMark);
if (instErrs.length) console.log('   进实例页后的 console.error =\n' + instErrs.map((s) => '     ' + s.replace(/\s+/g, ' ').slice(0, 220)).join('\n'));
check('读路径期间没有新增 console.error', instErrs.length === 0, instErrs.slice(0, 2).join(' | ') || '0 条');
if (startupErrs.length) {
  console.log(`\n📌 另记（不属于实例模块，未修）：启动期 ${startupErrs.length} 条 console.error，`);
  console.log('   其中含本地 SQLite 的 "Execute: incomplete input (code 1) … COALESCE(NULLIF(new|old.search_text …"，');
  console.log('   指向笔记 FTS 触发器 DDL（schema.ts:55-68 / local-db.ts:485-504）。根因与影响面**尚未定性**。');
}

const passed = checks.filter((c) => c.pass).length;
console.log('\n=== 汇总 ===');
console.log(`${passed}/${checks.length} 通过`);
checks.filter((c) => !c.pass).forEach((c) => console.log(`  FAIL: ${c.n}`));
process.exit(passed === checks.length ? 0 : 1);
