#!/usr/bin/env node
/**
 * verify-finance-writepath.mjs — **真机**验证记账模块的 UI 写路径（快速记账 + 删除）。
 *
 * ## 为什么先探 API 再点 UI
 * 契约探针 probe-finance-api.mjs 已确认后端 CRUD 完整（20/20），
 * 所以 UI 上任何失败都**不能**用「后端不支持」解释 —— 这才是真缺陷的判定前提。
 *
 * ## 判据设计（沿用 BUG-AB/AC、网关那轮定下的规矩）
 *
 *  1. **前置**：直接查 PG 记下基线行数与最新 id
 *  2. **API 播种**：先经 API 建一条 SEED 记录，2xx
 *  3. **读路径**：SEED 卡片出现在列表里（证明 UI 渲染的是服务端数据，不是本地幻觉）
 *  4. **对照组 A**：空输入时「记账」按钮 disabled（排除「无脑点也能过」）
 *  5. **解析预览**：填自然语言 → 预览出现且金额/收支正确
 *  6. **真写入**：点「确认入账」→ **直接查 PG** 行数 +1 且字段与 UI 一致
 *  7. **API 层**：POST 状态码 2xx（区分「没发请求」与「被服务器拒」）
 *  8. **反馈正确性**：MutationObserver 抓到的 toast 出现成功文案；
 *     **且**若 PG 未变却出现成功文案 → 判 FAIL（BUG-AC 纪律：不能把失败说成成功）
 *  9. **列表回显**：新卡片含该金额
 * 10. **统计联动**：头部「本月支出」已含新账（证明 stats 也一起刷新了）
 * 11. **删除写路径**：点删除 → **直接查 PG** 行数回落
 * 12. **对照组**：SEED 记录仍在（证明删除没误伤）
 *
 * ## 等待条件只用确定性元素
 * 「等 `.quick-preview` 出现 / 消失」「等含该金额的 `.tx-card` 消失」——
 * **不用**页面上任何常驻文案的子串（第一版栽过：静态提示里的「失败」立刻命中）。
 *
 * ## 证伪模式（--sabotage）
 * 26/26 绿灯本身不能证明判据有区分力。两种人为破坏，判据**必须**失败：
 *   --sabotage=hide-cta      把「记账」按钮从 DOM 摘掉（假界面/死 CTA）
 *   --sabotage=swallow-create 拦掉 POST /api/finance 并回一个假的 201
 *                            —— 复刻 BUG-AC「UI 显示已保存、库里其实没有」
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/verify-finance-writepath.mjs
 */
import { execFileSync } from 'node:child_process';
import { requireDevPass } from './lib/dev-pass.mjs'
import http from 'node:http';

const SABOTAGE = (process.argv.find((a) => a.startsWith('--sabotage=')) || '').split('=')[1] || '';
if (SABOTAGE) console.log(`\n⚠️ 证伪模式：${SABOTAGE} —— 判据**应该**失败，失败才算这个模式跑对\n`);

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9262';
const MASTER = process.env.POCKET_MASTER || '';
// PG schema：这个脚本的判据靠**直接查库**对照 UI 写入，所以 schema 必须和被测后端一致。
// 写死 `opencode_pocket` 意味着它只能对着共享库跑 —— 那正是 BUG-V14 里
// 「失败会把 seed 留在别人的库里」的根源。改成跟随后端配置（config.go 的
// POCKET_PG_SCHEMA，默认值相同），指向隔离后端时本脚本的断言才成立。
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';
if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`);

function resolvePsql() {
  const cands = [process.env.POCKET_PSQL, 'logs/pg/dist2/pgsql/bin/psql.exe', 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'].filter(Boolean);
  for (const c of cands) { try { execFileSync(c, ['--version'], { stdio: 'ignore' }); return c } catch { /* next */ } }
  console.error('找不到 psql.exe，请设置 POCKET_PSQL');
  process.exit(4);
}
const PSQL = resolvePsql();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 });
// 兜底串必须纯 ASCII：中文经系统 ANSI 码页传给 psql 会报 invalid byte sequence
const psql = (sql) => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql], { encoding: 'utf8' }).trim();
const txCount = () => Number(psql(`select count(*) from ${SCHEMA}.finance_transactions;`).match(/-?\d+/)?.[0] ?? NaN);
// 最新一行：id|type|amount|category|source|note
const newestTx = () => {
  const row = psql(`select id||'|'||type||'|'||amount||'|'||category||'|'||source||'|'||coalesce(note,'') from ${SCHEMA}.finance_transactions order by created_at desc, id desc limit 1;`);
  if (!row || row.startsWith('(')) return null;
  const [id, type, amount, category, source, note] = row.split('|');
  return { id, type, amount, category, source, note };
};

// ---------- API（播种 / 清理用） ----------
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
let id = 0; const pending = new Map(); const errors = []; const apiLog = [];
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) });
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {};
  if (m.method === 'Network.requestWillBeSent' && String(p.request?.url || '').includes('/api/finance')) {
    apiLog.push({ url: p.request.url.replace(/https?:\/\/[^/]+/, ''), method: p.request.method, status: null });
  }
  if (m.method === 'Network.responseReceived' && String(p.response?.url || '').includes('/api/finance')) {
    const url = p.response.url.replace(/https?:\/\/[^/]+/, '');
    const hit = apiLog.find((r) => r.url === url && r.status === null);
    if (hit) hit.status = p.response.status;
  }
  if (m.method === 'Runtime.exceptionThrown') errors.push(p.exceptionDetails?.exception?.description || p.exceptionDetails?.text || '');
});
await new Promise((r) => ws.addEventListener('open', r));
await send('Runtime.enable');
await send('Network.enable');
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value;

let origin = null;
const readyDl = Date.now() + 20000;
while (Date.now() < readyDl) { origin = await ev('location.origin'); if (origin && origin !== 'null') break; await sleep(500) }
// 默认要求 dev 包（http://localhost）。做生产 https 回归时用
// POCKET_EXPECT_ORIGIN=https://localhost 放宽 —— 否则脚本会在第一关就退出，
// 根本走不到真正要验的那些判据上。
const EXPECT_ORIGIN = process.env.POCKET_EXPECT_ORIGIN || 'http://localhost';
console.log('origin =', origin, `（本次要求 ${EXPECT_ORIGIN}）`);
if (origin !== EXPECT_ORIGIN) { console.log(`origin 与预期不符（要求 ${EXPECT_ORIGIN}），中止。`); process.exit(5) }

// ⚠️ 证伪模式会改 DOM（hide-cta 摘按钮），而 Vue 的 vdom 仍认为那个节点在，
// 重新 patch 时**不会**把它插回去 —— 于是 sabotage 跨轮泄漏：
// 下一轮即使不指定 --sabotage 也会看到 btn=false，整轮结论作废。
// 这里无条件 reload 一次，保证每轮都从干净页面开始。
await ev('location.reload()');
const rlDl = Date.now() + 30000;
let reloaded = null;
while (Date.now() < rlDl) {
  await sleep(1000);
  reloaded = await ev('location.origin');
  if (reloaded === EXPECT_ORIGIN) break;
}
console.log('reload 后 origin =', reloaded, reloaded === EXPECT_ORIGIN ? '' : '⚠️ 页面未恢复干净');
await sleep(2500);

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
const STAMP = Date.now().toString().slice(-6);
const AMOUNT = '97.77';
const TEST_TEXT = `打车花了 ${AMOUNT} 元`;
const SEED_NOTE = `SEED-${STAMP}`;

const PANE = `(function(){var ps=document.querySelectorAll('.inner-pane, .outer-pane');for(var i=0;i<ps.length;i++){if(ps[i].offsetParent!==null)return ps[i];}return document.querySelector('.page')||document.body;})()`;

// ---------- 1. 前置：查 PG ----------
const before = txCount();
console.log(`前置：PG finance_transactions = ${before}，最新 = ${JSON.stringify(newestTx())}`);
check('前置：直接查 PG 拿到基线行数', Number.isFinite(before), `count=${before}`);

// ---------- 2. API 播种 ----------
const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: devPass } });
let token = null;
try { token = JSON.parse(login.body).token } catch { /* below */ }
if (!token) { console.log('播种失败：登录不通 ' + login.status); process.exit(6) }
const seed = await api('/api/finance', {
  token, method: 'POST',
  body: { type: 'expense', amount: 11.11, category: 'UI测试', note: SEED_NOTE, source: 'manual', note_ref: `probe:seed:${STAMP}` },
});
let seedId = null;
try { seedId = JSON.parse(seed.body).id } catch { /* below */ }
check('API 播种成功（2xx，拿到 id）', seed.status >= 200 && seed.status < 300 && !!seedId, `status=${seed.status} id=${seedId}`);
const afterSeed = txCount();
check('播种后 PG 行数 +1（证明 API 写路径真的落库）', afterSeed === before + 1, `${before} -> ${afterSeed}`);

// ---------- 失败路径也必须删掉 SEED ----------
//
// ⚠️ 2026-10-03 补：原先 DELETE 只写在脚本**末尾**，中间任何抛错都会把这一行
// 留在**共享**开发库里。而那些行会被另一会话当成真实数据卷进它的基线 ——
// 后果比「自己测试脏了」更糟，是**污染别人的运行**。
// 与 BUG-V10（verify-https-prod.mjs 失败路径不还原覆盖值）同一类。
// ⇒ 幂等清理函数 + 挂在 unhandledRejection / uncaughtException 上。
//    process.on('exit') 不能 await，所以用前两个。
let cleaned = false;
async function cleanupSeed(reason) {
  if (!seedId || cleaned) return;
  cleaned = true;
  try {
    const cl = await api(`/api/finance/${seedId}`, { token, method: 'DELETE' });
    console.log(`\n[cleanup:${reason}] 删除 SEED ${seedId} -> ${cl.status}，PG 终值 = ${txCount()}`);
  } catch (e) {
    console.error(`\n[cleanup:${reason}] 删除 SEED ${seedId} 失败：${String(e?.message || e).slice(0, 120)}`);
    console.error(`   ⚠️ 这一行可能留在 ${SCHEMA} 里，需要手工清理：DELETE FROM ${SCHEMA}.finance_transactions WHERE id='${seedId}'`);
  }
}
process.on('unhandledRejection', async (e) => {
  console.error('\n[未处理的 rejection]', e);
  await cleanupSeed('rejection');
  process.exit(1);
});
process.on('uncaughtException', async (e) => {
  console.error('\n[未捕获异常]', e);
  await cleanupSeed('exception');
  process.exit(1);
});

// ---------- 3. 进记账页 ----------
// ⚠️ 踩过的坑：如果设备**已经**在 #/finance，`location.hash = '#/finance'` 不会触发
// 导航，onMounted 不跑，列表是上一轮的陈旧数据 —— 读路径判据会假失败
// （证伪 hide-cta 那一轮就是这么翻车的：共 1 张卡，但那张是上一轮的旧 SEED）。
// 所以这里**无条件**点一次头部「刷新」强制 load()，再轮询等目标卡片出现。
await ev(`location.hash = '#/finance'`);
const dl = Date.now() + 15000;
while (Date.now() < dl && (await ev('location.hash')) !== '#/finance') await sleep(300);
await sleep(1800);
const refreshed = await ev(`(function(){
  var b=document.querySelector('button[aria-label="刷新"]');
  if(!b) return 'NO_REFRESH_BTN';
  b.click(); return 'clicked';
})()`);
console.log('   强制刷新 =', refreshed);
await sleep(1200);

const hasInput = await ev(`!!(${PANE}).querySelector('.quick-input')`);
const hasBtn = await ev(`!!(${PANE}).querySelector('.quick-btn')`);

// 污染守卫：非 hide-cta 模式下按钮本该在。不在 = 上一轮的 sabotage 泄漏了，
// 这一轮的结论会整个作废（实测踩过：swallow-create 退化成 hide-cta）。宁可直接停。
if (!hasBtn && SABOTAGE !== 'hide-cta') {
  console.log('CONTAMINATED：进页时「记账」按钮就不存在，说明上一轮的证伪 sabotage 跨轮泄漏。');
  console.log('           判据已加 reload 隔离；若仍复现，先手动重启 App 再跑。');
  process.exit(7);
}

// 证伪：摘掉「记账」按钮（模拟死 CTA / 假界面）
if (SABOTAGE === 'hide-cta') {
  await ev(`(function(){var b=(${PANE}).querySelector('.quick-btn');if(b)b.remove();return 1})()`);
  console.log('   [sabotage] 已从 DOM 摘除 .quick-btn');
}
check('页面就位：快速记账输入框与「记账」按钮都存在（缺失即 FAIL，不许空过）', hasInput === true && hasBtn === true, `input=${hasInput} btn=${hasBtn}`);

// ---------- 4. 读路径：种下的记录渲染出来了 ----------
// 轮询等目标卡片出现（给 load() 留足时间），而不是只看某一瞬间的快照 ——
// 「有没有把服务端数据画出来」要判的是最终状态，不是某一毫秒。
const findCard = (needle) => ev(`(function(){
  var cs=(${PANE}).querySelectorAll('.tx-card');
  for(var i=0;i<cs.length;i++){ if((cs[i].textContent||'').indexOf(${JSON.stringify(needle)})>=0) return cs[i].textContent; }
  return null;
})()`);
let seedCardText = null;
const seedDl = Date.now() + 15000;
while (Date.now() < seedDl) {
  seedCardText = await findCard(SEED_NOTE);
  if (seedCardText) break;
  await sleep(600);
}
const cardCount = await ev(`(${PANE}).querySelectorAll('.tx-card').length`);
check('读路径：API 播种的记录出现在 UI 列表里（证明渲染的是服务端数据）', !!seedCardText,
  seedCardText ? seedCardText.replace(/\s+/g, ' ').slice(0, 90) : `未找到 ${SEED_NOTE}，共 ${cardCount} 张卡`);

// ---------- 5. 对照组 A：空输入时按钮禁用 ----------
const btnDisabledEmpty = await ev(`(function(){var b=(${PANE}).querySelector('.quick-btn');return b?b.disabled:'NO_BTN'})()`);
check('对照组 A：空输入时「记账」按钮 disabled（排除「无脑点也能过」）', btnDisabledEmpty === true, `disabled=${btnDisabledEmpty}`);

// ---------- 6. 解析预览 ----------
const fillQuick = (val) => `(function(){
  var el=(${PANE}).querySelector('.quick-input'); if(!el) return 'NO_INPUT';
  var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;
  s.call(el,${JSON.stringify(val)});
  el.dispatchEvent(new Event('input',{bubbles:true}));
  return el.value;
})()`;
const filled = await ev(fillQuick(TEST_TEXT));
check('自然语言文本填入输入框并回读一致', filled === TEST_TEXT, `readBack="${filled}"`);

apiLog.length = 0;
await ev(`(function(){var b=(${PANE}).querySelector('.quick-btn');if(b)b.click();return 1})()`);
// 等预览出现 —— 确定性元素，不用文案子串
const pvDl = Date.now() + 20000;
let previewText = null;
while (Date.now() < pvDl) {
  previewText = await ev(`(function(){var p=(${PANE}).querySelector('.quick-preview');return p?p.textContent:null})()`);
  if (previewText) break;
  await sleep(500);
}
console.log('   预览 =', JSON.stringify((previewText || '').replace(/\s+/g, ' ')));
check('点「记账」后预览出现（解析请求走通）', !!previewText, previewText ? '' : '20s 内未出现 .quick-preview');
check('预览里金额与收支方向正确', !!previewText && previewText.includes(AMOUNT) && /支出/.test(previewText),
  previewText ? previewText.replace(/\s+/g, ' ').slice(0, 80) : '(无预览)');
const parsePost = apiLog.find((r) => r.url.includes('/api/finance/parse'));
check('解析接口 2xx（不是被服务器拒）', !!parsePost && parsePost.status >= 200 && parsePost.status < 300,
  parsePost ? `status=${parsePost.status}` : '（未捕获到 parse 请求）');

// ---------- 7. 确认入账 → 真写入 ----------
const beforeSubmit = txCount();
apiLog.length = 0;
// 装 toast 收集器：toast 是瞬态的，轮询会漏，MutationObserver 才抓得住
await ev(`(function(){
  window.__toasts=[];
  if(window.__toastObs) window.__toastObs.disconnect();
  window.__toastObs=new MutationObserver(function(){
    var ns=document.querySelectorAll('[role="alert"]');
    for(var i=0;i<ns.length;i++){
      var t=(ns[i].querySelector('.toast-message')?ns[i].querySelector('.toast-message').textContent:ns[i].textContent)||'';
      if(t && window.__toasts.indexOf(t)<0) window.__toasts.push(t);
    }
  });
  window.__toastObs.observe(document.body,{childList:true,subtree:true});
  return 1;
})()`);

// 证伪：拦掉 POST /api/finance 并回一个假的 201 —— 复刻 BUG-AC
// 「UI 显示已入账、库里其实什么都没发生」。此时**只有**查 PG 的判据能识破，
// toast 判据必然放行（因为 UI 自己确实以为成功了）—— 这正是要证明的事。
if (SABOTAGE === 'swallow-create') {
  await ev(`(function(){
    if(!window.__origFetch) window.__origFetch = window.fetch.bind(window);
    window.fetch = function(input, init){
      var url = (typeof input==='string') ? input : ((input && input.url) || '');
      var method = (init && init.method) || ((input && input.method) || 'GET');
      var u = String(url);
      if(String(method).toUpperCase()==='POST' && /\\/api\\/finance(\\?|$)/.test(u)){
        window.__swallowed = (window.__swallowed||0)+1;
        return Promise.resolve(new Response(
          JSON.stringify({id:'fake-'+Date.now(),type:'expense',amount:${AMOUNT},category:'交通',source:'manual',note:'',created_at:new Date().toISOString(),created:true}),
          {status:201, headers:{'Content-Type':'application/json'}}));
      }
      return window.__origFetch(input, init);
    };
    return 1;
  })()`);
  console.log('   [sabotage] 已拦掉 POST /api/finance 并回假 201');
}

const clickedConfirm = await ev(`(function(){
  var p=(${PANE}).querySelector('.quick-preview');
  if(!p) return 'NO_PREVIEW';
  var b=Array.prototype.slice.call(p.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('确认入账')>=0});
  if(!b) return 'NO_CONFIRM_BTN';
  b.click(); return 'clicked';
})()`);
check('「确认入账」按钮存在且点得动（缺失即 FAIL，不许空过）', clickedConfirm === 'clicked', `result=${clickedConfirm}`);

// 等预览消失 —— 流程走完的确定信号
const goneDl = Date.now() + 25000;
let previewGone = false;
while (Date.now() < goneDl) {
  previewGone = await ev(`!(${PANE}).querySelector('.quick-preview')`);
  if (previewGone === true) break;
  await sleep(500);
}
await sleep(1500);

// ---------- 8. 落库判据：直接查 PG ----------
const after = txCount();
const top = newestTx();
console.log(`   PG: ${beforeSubmit} -> ${after}，最新 = ${JSON.stringify(top)}`);
check('**直接查 PG** 确认真的写进去了（唯一能排除 UI 假象的判据）', after === beforeSubmit + 1, `${beforeSubmit} -> ${after}`);
check('PG 最新一条金额/类型/来源与 UI 预览一致',
  !!top && top.amount === AMOUNT && top.type === 'expense' && top.source === 'manual',
  top ? `amount=${top.amount} type=${top.type} source=${top.source} category=${top.category}` : '(空)');
check('PG 最新一条的备注来自 UI 输入的自然语言原文', !!top && top.note === TEST_TEXT, top ? `note="${top.note}"` : '(空)');

const post = apiLog.find((r) => r.url.includes('/api/finance') && r.method === 'POST' && !r.url.includes('/parse'));
console.log('   API 往返 =', JSON.stringify(apiLog));
check('POST /api/finance 非 4xx/5xx（不是被服务器拒绝）', !!post && post.status >= 200 && post.status < 300,
  post ? `status=${post.status}` : '（未捕获到创建请求）');

// ---------- 9. 反馈正确性 ----------
const toasts = (await ev(`JSON.stringify(window.__toasts||[])`)) ? JSON.parse(await ev(`JSON.stringify(window.__toasts||[])`)) : [];
console.log('   toast =', JSON.stringify(toasts));
const saidOk = toasts.some((t) => /已入账|成功/.test(t));
const saidErr = toasts.some((t) => /失败|错误|不能为空|无效/.test(t));
check('界面给出成功反馈（toast）', saidOk, `toasts=${JSON.stringify(toasts)}`);
check('⚠️ 没有失败类反馈与成功类反馈并存（同 BUG-AC 纪律：不能把失败说成成功）', !(saidOk && saidErr),
  `ok=${saidOk} err=${saidErr}`);
check('⚠️ 没出现「PG 未变却说成功」的假成功', !(saidOk && after !== beforeSubmit + 1),
  `saidOk=${saidOk} PG ${beforeSubmit}->${after}`);

// ---------- 10. 列表回显（轮询等最终状态，不赌某一毫秒的快照） ----------
let newCardText = null;
const echoDl = Date.now() + 15000;
while (Date.now() < echoDl) {
  newCardText = await findCard(AMOUNT);
  if (newCardText) break;
  await sleep(600);
}
check('列表回显：新交易卡出现在 UI 上', !!newCardText, newCardText ? newCardText.replace(/\s+/g, ' ').slice(0, 90) : '未找到新卡片');

// ---------- 11. 统计联动 ----------
const expenseVal = await ev(`(function(){var e=(${PANE}).querySelector('.stat-val.expense');return e?e.textContent:''})()`);
const num = Number((expenseVal || '').replace(/[^0-9.]/g, ''));
check('统计联动：头部「本月支出」已含新账（stats 跟着刷新了）', Number.isFinite(num) && num >= Number(AMOUNT),
  `「${expenseVal}」 解析为 ${num}，应 ≥ ${AMOUNT}`);

// ---------- 12. 删除写路径 ----------
const beforeDel = txCount();
apiLog.length = 0;
const delClick = await ev(`(function(){
  var cs=(${PANE}).querySelectorAll('.tx-card');
  for(var i=0;i<cs.length;i++){
    if((cs[i].textContent||'').indexOf(${JSON.stringify(AMOUNT)})>=0){
      var b=cs[i].querySelector('.tx-del'); if(!b) return 'NO_DEL_BTN';
      b.click(); return 'clicked';
    }
  }
  return 'NO_CARD';
})()`);
check('删除按钮点得动（缺失即 FAIL，不许空过）', delClick === 'clicked', `result=${delClick}`);
// 等该卡片从 DOM 消失 —— 确定性元素信号
const delDl = Date.now() + 25000;
let cardGone = false;
while (Date.now() < delDl) {
  cardGone = await ev(`!Array.prototype.slice.call((${PANE}).querySelectorAll('.tx-card')).some(function(x){return (x.textContent||'').indexOf(${JSON.stringify(AMOUNT)})>=0})`);
  if (cardGone === true) break;
  await sleep(500);
}
await sleep(1200);
const afterDel = txCount();
console.log(`   PG: ${beforeDel} -> ${afterDel}`);
check('**直接查 PG** 确认删除真的生效', afterDel === beforeDel - 1, `${beforeDel} -> ${afterDel}`);
const delReq = apiLog.find((r) => r.method === 'DELETE' && r.url.includes('/api/finance/'));
check('DELETE 请求发出且 2xx', !!delReq && delReq.status >= 200 && delReq.status < 300,
  delReq ? `status=${delReq.status}` : '（未捕获到 DELETE）');
check('UI 上该卡片消失了', cardGone === true);

// ---------- 13. 对照组：SEED 仍在 ----------
const stillSeed = await ev(`(function(){
  var cs=(${PANE}).querySelectorAll('.tx-card');
  for(var i=0;i<cs.length;i++){ if((cs[i].textContent||'').indexOf(${JSON.stringify(SEED_NOTE)})>=0) return true; }
  return false;
})()`);
check('对照组：删除没误伤，SEED 记录仍在 UI 上', stillSeed === true);

// ---------- 14. 异常 ----------
check('无未捕获 JS 异常', errors.length === 0, errors.slice(0, 2).join(' | ') || '0 条');

// ---------- 清理：删掉种下的 SEED（幂等，与失败路径共用同一函数） ----------
await cleanupSeed('normal');

const passed = checks.filter((c) => c.pass).length;
const failed = checks.filter((c) => !c.pass);
console.log('\n=== 汇总 ===');
console.log(`${passed}/${checks.length} 通过`);
failed.forEach((c) => console.log(`  FAIL: ${c.n}`));

if (SABOTAGE) {
  // 证伪模式：判据**必须**抓到破坏。抓到了（fail>0）才算这个模式跑对。
  // 全绿反而说明判据是空转的，这个模式的结论就是「无效」。
  //
  // ⚠️ 匹配前两边都要去掉 `*`：期望键里写了 `**直接查 PG** …`，
  // 判据名里也带 `**`。只剥一边会 `includes` 不到，
  // 于是把「抓到了」误报成「没抓到」——第一版就这么把一次有效证伪判成无效。
  const norm = (s) => String(s).replace(/\*/g, '').replace(/\s+/g, ' ').trim();
  // ⚠️ failed 是 {n, pass} **对象数组**，不是字符串数组。
  // 写 `failed.map(norm)` 会把每个对象 String() 成 "[object Object]"，
  // 于是永远匹配不上、永远报「证伪无效」—— 连续两轮都被这个坑挡住。
  const key = failed.map((f) => norm(f.n));
  const expectKey = SABOTAGE === 'swallow-create'
    ? ['**直接查 PG** 确认真的写进去了', '⚠️ 没出现「PG 未变却说成功」的假成功', 'POST /api/finance 非 4xx/5xx']
    : ['页面就位：快速记账输入框与「记账」按钮都存在', '点「记账」后预览出现（解析请求走通）'];
  const caught = expectKey.every((k) => key.some((n) => n.includes(norm(k))));
  console.log(`\n逐条匹配：${expectKey.map((k) => `${key.some((n) => n.includes(norm(k))) ? 'HIT' : 'MISS'} «${norm(k)}»`).join('  ')}`);
  console.log(`\n证伪判定：${caught ? '✅ 判据在有缺陷一侧如期失败' : '❌ 判据没抓到破坏 —— 本次证伪无效'}`);
  console.log(`  期望抓到：${expectKey.map(norm).join(' / ')}`);
  console.log(`  实际失败：${failed.map((c) => c.n).join(' / ') || '(无)'}`);
  process.exit(caught ? 0 : 1);
}
process.exit(passed === checks.length ? 0 : 1);
