// 探记账（finance）模块的后端契约，为真机 UI 写路径验证做准备。
// 沿用邮箱/网关那套教训：先探 API 再点 UI —— 后端若直接 503/404，
// 可以在不占用设备、不受 UI 选择器干扰的前提下把「后端不支持」和「UI 有 bug」分开。
//
// 判据要求：每条判据在「有缺陷」一侧必须失败过。
//   - 阳性对照（随机路由 404、无 token 401）证明探针能区分 404/401 与正常；
//   - 幂等、统计、删除回读这三项若后端写错就会 FAIL。
import http from 'node:http';
import { requireDevPass } from './lib/dev-pass.mjs'
const HOST = process.env.POCKET_API_HOST || '127.0.0.1';
const PORT = Number(process.env.POCKET_API_PORT || 8088);
const devPass = requireDevPass()

function api(path, { token, method = 'GET', body } = {}) {
  return new Promise((res) => {
    const payload = body ? JSON.stringify(body) : '';
    const h = {};
    if (token) h.Authorization = 'Bearer ' + token;
    if (payload) { h['Content-Type'] = 'application/json'; h['Content-Length'] = Buffer.byteLength(payload); }
    const r = http.request({ host: HOST, port: PORT, path, method, headers: h }, (resp) => {
      let s = '';
      resp.on('data', (c) => (s += c));
      resp.on('end', () => res({ status: resp.statusCode, body: s }));
    });
    r.on('error', (e) => res({ status: 'ERR', body: e.message }));
    if (payload) r.write(payload);
    r.end();
  });
}

const checks = [];
const check = (n, pass, d) => {
  checks.push({ n, pass });
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`);
};

const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: devPass } });
let token = null;
try { token = JSON.parse(login.body).token; } catch { /* below */ }
if (!token) {
  console.log('登录失败：' + login.status + ' ' + login.body.slice(0, 160));
  process.exit(1);
}
console.log('登录成功\n');

const stamp = Date.now().toString().slice(-6);
const NOTE_REF = `probe:finance:${stamp}`;

// ---------- 0. 阴性对照：探针有区分能力 ----------
const bogus = await api('/api/finance/definitely-not-a-route', { token });
console.log(`GET  /api/finance/definitely-not-a-route -> ${bogus.status}`);
check('阴性对照 A：随机记账子路径 404（探针能区分 404）', bogus.status === 404, `status=${bogus.status}`);

const noTok = await api('/api/finance');
console.log(`GET  /api/finance（无 token）-> ${noTok.status}`);
check('阴性对照 B：未鉴权 401（探针能区分 401）', noTok.status === 401, `status=${noTok.status}`);

// ---------- 1. 列表 ----------
const list = await api('/api/finance', { token });
console.log(`\nGET  /api/finance -> ${list.status}  ${list.body.slice(0, 140)}`);
check('列表端点 200', list.status === 200, `status=${list.status}`);
let txs = [];
try { txs = JSON.parse(list.body).transactions || []; } catch { /* below */ }
check('列表返回 {transactions,total} 结构', Array.isArray(txs) && typeof JSON.parse(list.body || '{}').total === 'number', `n=${txs.length}`);

// ---------- 2. 自然语言解析（不落库） ----------
const parsed = await api('/api/finance/parse', { token, method: 'POST', body: { text: '打车花了 32 元' } });
console.log(`\nPOST /api/finance/parse -> ${parsed.status}  ${parsed.body.slice(0, 200)}`);
let pv = null;
try { pv = JSON.parse(parsed.body); } catch { /* below */ }
check('解析端点 200 且能抽出 金额/收支', parsed.status === 200 && pv && pv.amount > 0 && (pv.type === 'expense' || pv.type === 'income'),
  pv ? `type=${pv.type} amount=${pv.amount} category=${pv.category}` : `status=${parsed.status}`);

// 负向解析：识别不了必须报错，**不得**回 200 + amount=0
// （否则 UI 会显示一条「¥0.00」的假预览，用户点「确认入账」才被 CHECK(amount>0) 拒）
const junk = await api('/api/finance/parse', { token, method: 'POST', body: { text: '今天天气不错没有任何金额' } });
console.log(`POST /api/finance/parse（无金额文本）-> ${junk.status}  ${junk.body.slice(0, 160)}`);
let jv = null;
try { jv = JSON.parse(junk.body); } catch { /* 非 JSON 也算「没给出假预览」 */ }
const junkFake = junk.status === 200 && jv && !(jv.amount > 0);
check('无法识别的文本不返回「200 + 金额为 0」的假预览', !junkFake,
  `status=${junk.status} amount=${jv && jv.amount}`);

// ---------- 3. 创建（UI「确认入账」打的就是这条） ----------
const before = (await api('/api/finance', { token }));
const beforeN = (JSON.parse(before.body || '{}').total) || 0;

const createPayload = { type: 'expense', amount: 32, category: '交通', note: `PROBE ${stamp}`, source: 'manual', note_ref: NOTE_REF };
const created = await api('/api/finance', { token, method: 'POST', body: createPayload });
console.log(`\nPOST /api/finance -> ${created.status}  ${created.body.slice(0, 260)}`);
check('创建返回 201（真实新建）', created.status === 201, `status=${created.status}`);
let tx = null;
try { tx = JSON.parse(created.body); } catch { /* below */ }
check('创建响应带 id 且 created=true', !!(tx && tx.id && tx.created === true), tx ? `id=${tx.id} created=${tx.created}` : 'no body');

// ---------- 4. 幂等：同 note_ref 再次提交不得重复入账 ----------
const again = await api('/api/finance', { token, method: 'POST', body: createPayload });
console.log(`\nPOST 同 note_ref 再来一次 -> ${again.status}  ${again.body.slice(0, 200)}`);
let tx2 = null;
try { tx2 = JSON.parse(again.body); } catch { /* below */ }
check('同幂等键返回 200 且 created=false', again.status === 200 && tx2 && tx2.created === false, `status=${again.status} created=${tx2 && tx2.created}`);
check('同幂等键返回同一条 id（不重复入账）', !!(tx && tx2 && tx.id === tx2.id), `${tx && tx.id} vs ${tx2 && tx2.id}`);

// ---------- 5. 列表真的多了一条 ----------
const afterList = await api('/api/finance', { token });
const afterObj = JSON.parse(afterList.body || '{}');
const afterN = afterObj.total || 0;
const found = (afterObj.transactions || []).some((t) => tx && t.id === tx.id);
console.log(`\n列表条数 ${beforeN} -> ${afterN}`);
check('列表条数 +1', afterN === beforeN + 1, `${beforeN} -> ${afterN}`);
check('列表里能按 id 找到新记录', found, `id=${tx && tx.id}`);

// ---------- 6. 按 id 取单条（UI 详情/删除都依赖） ----------
if (tx) {
  const one = await api(`/api/finance/${tx.id}`, { token });
  console.log(`\nGET  /api/finance/${tx.id} -> ${one.status}  ${one.body.slice(0, 180)}`);
  check('按 id 取单条 200', one.status === 200, `status=${one.status}`);
  const missing = await api('/api/finance/00000000-0000-0000-0000-000000000000', { token });
  console.log(`GET  /api/finance/<不存在的 id> -> ${missing.status}`);
  check('不存在的 id 返回 404（不是假 200）', missing.status === 404, `status=${missing.status}`);
}

// ---------- 7. 统计（UI 头部三块金额） ----------
const now = new Date();
const month = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`;
const stats = await api(`/api/finance/stats?month=${month}&tz=${-now.getTimezoneOffset()}`, { token });
console.log(`\nGET  /api/finance/stats?month=${month} -> ${stats.status}  ${stats.body.slice(0, 260)}`);
let st = null;
try { st = JSON.parse(stats.body); } catch { /* below */ }
check('统计 200 且带 month/total_expense/balance', stats.status === 200 && st && typeof st.month === 'string'
  && typeof st.total_expense === 'number' && typeof st.balance === 'number',
  st ? `month=${st.month} expense=${st.total_expense} balance=${st.balance} count=${st.count}` : `status=${stats.status}`);
check('统计里 by_category 含「交通」（新建的分类进了分类占比）', !!(st && st.by_category && st.by_category['交通'] > 0),
  st ? JSON.stringify(st.by_category) : 'no stats');

// tz 非法值必须被拒（证明有真校验，不是随便收）
const badTz = await api('/api/finance/stats?tz=99999', { token });
console.log(`GET  /api/finance/stats?tz=99999 -> ${badTz.status}  ${badTz.body.slice(0, 120)}`);
check('非法 tz 返回 400（有真校验）', badTz.status === 400, `status=${badTz.status}`);

// 方法白名单：BUG-AD。/api/finance/ 下的 parse 与 {id} 都有白名单，
// stats 之前没有 —— DELETE/POST 都会回 200 + 统计结果。
const delStats = await api('/api/finance/stats', { token, method: 'DELETE' });
console.log(`\nDELETE /api/finance/stats -> ${delStats.status}  ${delStats.body.slice(0, 100)}`);
check('stats 子路由有方法白名单（非 GET 回 405，不泄露统计内容）',
  delStats.status === 405 && !/total_income|by_category/.test(delStats.body),
  `status=${delStats.status}`);

// ---------- 8. 删除（UI 删除按钮） ----------
if (tx) {
  const del = await api(`/api/finance/${tx.id}`, { token, method: 'DELETE' });
  console.log(`\nDELETE /api/finance/${tx.id} -> ${del.status}`);
  check('删除返回 204', del.status === 204, `status=${del.status}`);
  const afterDel = await api(`/api/finance/${tx.id}`, { token });
  console.log(`GET  已删除的 id -> ${afterDel.status}`);
  check('删除后取单条 404（真删掉了）', afterDel.status === 404, `status=${afterDel.status}`);
  const finalList = await api('/api/finance', { token });
  const finalN = (JSON.parse(finalList.body || '{}').total) || 0;
  check('删除后列表条数回到初始值', finalN === beforeN, `${beforeN} vs ${finalN}`);
}

const pass = checks.filter((c) => c.pass).length;
console.log(`\n=== 汇总 ===\n${pass}/${checks.length} 通过`);
if (pass !== checks.length) console.log('失败项：\n' + checks.filter((c) => !c.pass).map((c) => '  - ' + c.n).join('\n'));
process.exit(pass === checks.length ? 0 : 1);
