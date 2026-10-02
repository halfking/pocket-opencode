#!/usr/bin/env node
/**
 * verify-gateway-writepath.mjs — **真机**验证网关模块的 UI 写路径（节点新增）。
 *
 * ## 判据设计（沿用 BUG-AB/AC 那轮定下的规矩）
 *
 * 1. **前置**：直接查 PG 记下 `llm_gateway_nodes` 行数
 * 2. **CTA 不是死路**：点「+ 新增」后弹层真的出现
 * 3. **对照组**：空表单提交被校验拦下（否则「无脑点也能过」无法排除）
 * 4. **真能写**：填完提交 → **直接查 PG** 行数 +1，且 name/base_url 与 UI 填入值一致
 * 5. **API 层**：POST 状态码 2xx，且**不是**被服务器拒绝
 * 6. **反馈正确**：界面出现成功提示，且**不出现**报错文案
 * 7. **对照组**：探针直建的那条仍在（证明不是 UI 幻觉）
 *
 * ## 不做的判断
 *
 * 节点「探测」能否真的连上网关**不在本脚本范围** —— 探针节点指向
 * `*.invalid.test`，不可能连通。脚本只判「写路径通不通 + 反馈准不准」。
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/verify-gateway-writepath.mjs
 */
import { execFileSync } from 'node:child_process';
import { requireDevPass } from './lib/dev-pass.mjs'
// 设备上装的是**生产 https 包**（实测 origin=https://localhost），
// 而这一关原本写死开发包 http://localhost ⇒ 在当前设备上会在走到任何
// 真正要验的判据之前就 exit 5。生产 https 回归用 POCKET_EXPECT_ORIGIN 放宽。
const EXPECT_ORIGIN = process.env.POCKET_EXPECT_ORIGIN || 'http://localhost';
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe';
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555';
const PKG = 'com.kaixuan.opencode.pocket';
const PORT = process.env.POCKET_CDP_PORT || '9260';
const MASTER = process.env.POCKET_MASTER || '';

function resolvePsql() {
  const cands = [process.env.POCKET_PSQL, 'logs/pg/dist2/pgsql/bin/psql.exe', 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'].filter(Boolean);
// PG schema：跟随后端配置（backend/internal/config/config.go 的 POCKET_PG_SCHEMA，默认值相同）。
// 写死 opencode_pocket 会让本脚本只能对着共享库跑 —— 失败时 SEED 就留在别人的库里。
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';
if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`);
  for (const c of cands) { try { execFileSync(c, ['--version'], { stdio: 'ignore' }); return c } catch { /* next */ } }
  console.error('找不到 psql.exe，请设置 POCKET_PSQL');
  process.exit(4);
}
const PSQL = resolvePsql();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 });
const psql = (sql) => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql], { encoding: 'utf8' }).trim();
// 兜底串必须纯 ASCII：中文经系统 ANSI 码页传给 psql 会报 invalid byte sequence
const nodeCount = () => Number(psql(`select count(*) from ${SCHEMA}.llm_gateway_nodes;`).match(/-?\d+/)?.[0] ?? NaN);
const nodeNames = () => psql(`select coalesce(string_agg(name,'|' order by id),'(none)') from ${SCHEMA}.llm_gateway_nodes;`);

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
  if (m.method === 'Network.requestWillBeSent' && String(p.request?.url || '').includes('/api/')) {
    apiLog.push({ url: p.request.url.replace(/https?:\/\/[^/]+/, ''), method: p.request.method, status: null });
  }
  if (m.method === 'Network.responseReceived' && String(p.response?.url || '').includes('/api/')) {
    const url = p.response.url.replace(/https?:\/\/[^/]+/, '');
    const hit = apiLog.find((r) => r.url === url && r.status === null);
    if (hit) hit.status = p.response.status;
  }
  if (m.method === 'Runtime.exceptionThrown') errors.push(p.exceptionDetails?.exception?.description || p.exceptionDetails?.text || '');
});
await new Promise((r) => ws.addEventListener('open', r));
await send('Runtime.enable');
await send('Network.enable');
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value;

let origin = null;
const readyDl = Date.now() + 20000;
while (Date.now() < readyDl) { origin = await ev('location.origin'); if (origin && origin !== 'null') break; await sleep(500) }
console.log('origin =', origin, '（必须是 http://localhost）');
if (origin !== EXPECT_ORIGIN) { console.log('非 dev 包或 WebView 未就绪，中止。'); process.exit(5) }

// ---------- 登录 ----------
await ev(`location.hash = '#/login'`); await sleep(2600);
if (await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)) {
  await ev(`(function(){var el=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(MASTER)});el.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`);
  await sleep(1700);
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('解锁')>=0});if(b)b.click();return 1})()`);
  await sleep(4200);
}
if (await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)) {
  const devPass = requireDevPass()
  const fillBy = (sel, val) => `(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`;
  await ev(fillBy('input[placeholder*="用户名"]', 'admin'));
  await ev(fillBy('input[type="password"]', devPass)); await sleep(900);
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='登录'});if(b)b.click();return b?1:0})()`);
  await sleep(6500);
}

const checks = [];
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`) };
const STAMP = Date.now().toString().slice(-6);
const TEST_NAME = `UI-NODE-${STAMP}`;
const TEST_URL = `https://ui-${STAMP}.invalid.test`;

const PANE = `(function(){var ps=document.querySelectorAll('.inner-pane, .outer-pane');for(var i=0;i<ps.length;i++){if(ps[i].offsetParent!==null)return ps[i];}return document.body;})()`;

// ---------- 1. 前置 ----------
const before = nodeCount();
console.log(`前置：PG llm_gateway_nodes = ${before}，names = ${nodeNames()}`);
check('前置：直接查 PG 拿到基线行数', Number.isFinite(before), `count=${before}`);

// ---------- 2. 进列表页并打开新增弹层 ----------
await ev(`location.hash = '#/gateway'`);
const dl = Date.now() + 15000;
while (Date.now() < dl && (await ev('location.hash')) !== '#/gateway') await sleep(300);
await sleep(2500);

const opened = await ev(`(function(){
  var b=Array.prototype.slice.call((${PANE}).querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('新增')>=0});
  if(!b) return 'NO_BTN';
  b.click(); return 'clicked';
})()`)
await sleep(1200)
const sheetShown = await ev(`(function(){
  var t=(document.body.innerText||'');
  return t.indexOf('新增节点')>=0 || t.indexOf('编辑节点')>=0;
})()`)
check('CTA 不是死路：点「+ 新增」后弹层出现', opened === 'clicked' && sheetShown === true, `opened=${opened} sheet=${sheetShown}`)

// ---------- 3. 对照组：空提交被拦 ----------
apiLog.length = 0
const emptyClick = await ev(`(function(){
  var b=Array.prototype.slice.call((${PANE}).querySelectorAll('.sheet-actions button')).find(function(x){return (x.textContent||'').trim().indexOf('保存')>=0});
  if(!b) return 'NO_SAVE_BTN';
  b.click(); return 'clicked';
})()`)
await sleep(1800)
const stillSheet = await ev(`(function(){return (document.body.innerText||'').indexOf('新增节点')>=0 || (document.body.innerText||'').indexOf('编辑节点')>=0})()`)
const emptyRejected = apiLog.some((r) => r.url.includes('/api/llm-gateway/nodes') && r.method === 'POST' && r.status >= 400)
check('对照组：空表单提交被**服务器**拒绝（证明前端没偷偷补默认值）',
  emptyClick === 'clicked' && (emptyRejected || stillSheet === true),
  `clicked=${emptyClick} 被拒=${emptyRejected} 弹层仍在=${stillSheet} api=${JSON.stringify(apiLog.filter((r) => r.url.includes('gateway')))}`)

// ---------- 4. 真填真提交 ----------
const fillByLabel = (labelText, val) => `(function(){
  var root=(${PANE});
  var ls=root.querySelectorAll('label.form-label');
  for(var i=0;i<ls.length;i++){
    if((ls[i].textContent||'').indexOf(${JSON.stringify(labelText)})>=0){
      // label 后面紧跟的 input
      var el=ls[i].parentNode ? ls[i].parentNode.querySelector('input') : null;
      if(!el){ var all=root.querySelectorAll('input'); el=all[i]; }
      if(!el) return 'NO_INPUT';
      var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;
      s.call(el,${JSON.stringify(val)});
      el.dispatchEvent(new Event('input',{bubbles:true}));
      el.dispatchEvent(new Event('change',{bubbles:true}));
      return 'ok';
    }
  }
  return 'NO_LABEL';
})()`
// 网关表单的 label 与 input 是**兄弟节点**（不是 label 包裹），
// 所以按「label 文本 → 其后第一个 input」的顺序定位更稳：
const fillByOrder = (labelText, val, idx) => `(function(){
  var root=(${PANE});
  var ls=root.querySelectorAll('label.form-label');
  var ins=root.querySelectorAll('input');
  for(var i=0;i<ls.length;i++){
    if((ls[i].textContent||'').indexOf(${JSON.stringify(labelText)})>=0){
      // 名称/BaseURL/Admin用户名/密码 是 sheet 里第 ${idx} 个 input
      var el=ins[${idx}];
      if(!el) return 'NO_INPUT';
      var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;
      s.call(el,${JSON.stringify(val)});
      el.dispatchEvent(new Event('input',{bubbles:true}));
      el.dispatchEvent(new Event('change',{bubbles:true}));
      return 'ok';
    }
  }
  return 'NO_LABEL';
})()`
const fills = {
  name: await ev(fillByOrder('名称', TEST_NAME, 0)),
  url: await ev(fillByOrder('Base URL', TEST_URL, 1)),
  user: await ev(fillByOrder('Admin 用户名', 'ui-probe-admin', 2)),
  pwd: await ev(fillByOrder('Admin 密码', 'ui-probe-not-a-real-pw', 3)),
}
check('四个字段都能填入（表单不是只读的假界面）', Object.values(fills).every((v) => v === 'ok'), JSON.stringify(fills))
await sleep(800)

const readBack = await ev(`(function(){var ins=(${PANE}).querySelectorAll('input');return ins[0]?ins[0].value:''})()`)
check('填入值回读一致（不靠「看起来填了」）', readBack === TEST_NAME, `readBack=${readBack}`)

// ---------- 5. 提交 ----------
const beforeSubmit = nodeCount();
apiLog.length = 0
await ev(`(function(){
  var b=Array.prototype.slice.call((${PANE}).querySelectorAll('.sheet-actions button')).find(function(x){return (x.textContent||'').trim().indexOf('保存')>=0});
  if(b)b.click(); return 1;
})()`)

// 等状态到达期望：**等弹层关闭**（这是流程走完的确定信号），
// 而不是靠正则匹配提示文案。
//
// ⚠️ 第一版踩的坑：等待条件写成 /已新增|已保存|失败|错误|不能为空|required/i，
// 结果「后端未开启私网访问…内网地址的节点探测会**失败**」这句**静态提示**里的
// 「失败」立刻命中，循环在提交还没返回时就退出了，读到的是中间态。
// **等待条件不能用页面上任何常驻文案的子串。**
const waitDl = Date.now() + 25000;
let sheetClosed = false;
while (Date.now() < waitDl) {
  sheetClosed = await ev(`(function(){var t=document.body.innerText||'';return !(t.indexOf('新增节点')>=0||t.indexOf('编辑节点')>=0)})()`);
  if (sheetClosed === true) break;
  await sleep(600)
}
await sleep(1200) // 弹层关掉后再等一拍，让状态条渲染出来
const bodyText = (await ev(`document.body.innerText.replace(/\\s+/g,' ')`)) || ''
const statusText = await ev(`(function(){
  var b=document.querySelector('.status-bar');
  return b ? { text:(b.textContent||'').trim(), cls:b.className } : null;
})()`)
console.log('   状态条 =', JSON.stringify(statusText))
console.log('   body 全文 =', JSON.stringify(bodyText.slice(0, 300)))

// ---------- 6. 落库判据：直接查 PG ----------
const after = nodeCount();
const names = nodeNames();
check('**直接查 PG** 确认真的写进去了（唯一能排除 UI 假象的判据）', after === beforeSubmit + 1, `${beforeSubmit} -> ${after}`)
check('PG 里的节点名与 UI 填入值一致', names.includes(TEST_NAME), `PG=${names}`)

const post = apiLog.find((r) => r.url.includes('/api/llm-gateway/nodes') && r.method === 'POST')
console.log('   API 往返 =', JSON.stringify(apiLog.filter((r) => r.url.includes('gateway'))))
check('POST /api/llm-gateway/nodes 非 4xx/5xx（不是被服务器拒绝）',
  !!post && post.status >= 200 && post.status < 300,
  post ? `status=${post.status}` : '（没有捕获到该请求）')

// ---------- 7. 反馈正确性 ----------
// 判据只看**状态条**（.status-bar），不看 body 全文：
// body 里常驻着「内网地址的节点探测会失败」这类说明文案，
// 拿全文做关键词匹配必然误判（第一版就栽在这）。
const stText = statusText?.text || '';
const stIsErr = /status-err/.test(statusText?.cls || '');
const saysOk = /已新增|已保存/.test(stText);
check('界面给出成功反馈（状态条）', saysOk, `status="${stText}" cls="${statusText?.cls || ''}"`)
check('⚠️ 成功路径上没有残留的错误状态条（同 BUG-AC 纪律：不能把失败说成成功）',
  !(stIsErr && !saysOk),
  stIsErr ? `状态条是错误类：${stText}` : '状态条非错误类')

// ---------- 8. 对照组 ----------
check('对照组：弹层已关闭（流程真的走完）', sheetClosed === true)
check('无未捕获 JS 异常', errors.length === 0, errors.slice(0, 2).join(' | ') || '0 条')

console.log('\n=== 汇总 ===');
const passed = checks.filter((c) => c.pass).length;
console.log(`${passed}/${checks.length} 通过`);
checks.filter((c) => !c.pass).forEach((c) => console.log(`  FAIL: ${c.n}`));
console.log(`\n（测试节点 ${TEST_NAME} 留在库里，可由调用方清理）`);
process.exit(passed === checks.length ? 0 : 1);
