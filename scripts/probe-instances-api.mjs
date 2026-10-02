// 探实例（instances）模块的后端契约。
//
// 范围说明（重要）：实例模块是**只读设计** —— 前端 InstanceListView.vue 只有
// 刷新与选择，没有创建表单；后端也没有任何创建实例的 handler。
// 所以「实例的 UI 写路径」是范畴错误，本探针验的是**读路径 + 契约诚实性**。
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
      let s = ''; resp.on('data', (c) => (s += c)); resp.on('end', () => res({ status: resp.statusCode, body: s }));
    });
    r.on('error', (e) => res({ status: 'ERR', body: e.message }));
    if (payload) r.write(payload);
    r.end();
  });
}

const checks = [];
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`); };

const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: devPass } });
let token = null;
try { token = JSON.parse(login.body).token; } catch { /* below */ }
if (!token) { console.log('登录失败：' + login.status); process.exit(1); }
console.log('登录成功\n');

// ---------- 阴性对照 ----------
const noTok = await api('/api/instances');
console.log(`GET  /api/instances（无 token）-> ${noTok.status}`);
check('阴性对照 A：未鉴权 401（探针能区分 401）', noTok.status === 401, `status=${noTok.status}`);

const bogus = await api('/api/instances/definitely-not-a-route', { token });
console.log(`GET  /api/instances/definitely-not-a-route -> ${bogus.status}`);
check('阴性对照 B：随机实例子路径 404（探针能区分 404）', bogus.status === 404, `status=${bogus.status}`);

// ---------- 1. 列表 ----------
const list = await api('/api/instances', { token });
console.log(`\nGET  /api/instances -> ${list.status}  ${list.body.slice(0, 300)}`);
check('列表端点 200', list.status === 200, `status=${list.status}`);
let insts = [];
try { insts = JSON.parse(list.body).instances || []; } catch { /* below */ }
check('返回 {instances:[...]} 结构', Array.isArray(insts), `n=${insts.length}`);
console.log('实例 =', JSON.stringify(insts.map((i) => ({ id: i.id, name: i.displayName, env: i.environment, health: i.health }))));

// UI 的 InstanceListView 直接渲染 displayName / id / environment / capabilities.length，
// 缺任何一个字段都会在页面上显示成 undefined —— 契约必须保证。
const shapeOk = insts.every((i) => typeof i.id === 'string' && i.id
  && typeof i.displayName === 'string' && i.displayName
  && typeof i.environment === 'string' && i.environment);
check('每条实例都有 id/displayName/environment（UI 不会渲染出 undefined）', shapeOk,
  insts.length ? JSON.stringify(insts[0]) : '(空列表，跳过)');

// ---------- 2. since 过滤真的生效 ----------
// ⚠️ since 收的是**整数 epoch**（秒或毫秒，>1e12 自动按毫秒折算），不是 RFC3339。
// 第一版探针传了 ISO 字符串，ParseInt 失败返回 0 → 不过滤，
// 我差点把「探针假设写错」当成「产品缺陷」报出去。先读 parser 再下结论。
const futureMs = Date.now() + 3600_000;
const since = await api(`/api/instances?since=${futureMs}`, { token });
let sinceInsts = null;
try { sinceInsts = JSON.parse(since.body).instances || []; } catch { /* below */ }
console.log(`\nGET  /api/instances?since=${futureMs}（1 小时后的毫秒 epoch）-> ${since.status}  n=${sinceInsts && sinceInsts.length}`);
check('since 过滤生效（未来的时间戳应滤掉全部）', since.status === 200 && Array.isArray(sinceInsts) && sinceInsts.length === 0,
  `n=${sinceInsts && sinceInsts.length}`);

// 阳性对照：过去的时间戳不该滤掉任何东西（证明上一条不是「恒空」蒙对的）
const pastSec = Math.floor(Date.now() / 1000) - 3600;
const past = await api(`/api/instances?since=${pastSec}`, { token });
let pastInsts = null;
try { pastInsts = JSON.parse(past.body).instances || []; } catch { /* below */ }
console.log(`GET  /api/instances?since=${pastSec}（1 小时前的秒级 epoch）-> ${past.status}  n=${pastInsts && pastInsts.length}`);
check('since 过滤不是「恒空」：过去时间戳仍返回全部', past.status === 200 && Array.isArray(pastInsts) && pastInsts.length === insts.length,
  `n=${pastInsts && pastInsts.length}（基线 ${insts.length}）`);

// ---------- 3. 方法白名单（BUG-AD 同形状：读端点不回 405） ----------
for (const m of ['POST', 'DELETE', 'PUT']) {
  const r = await api('/api/instances', { token, method: m, body: m === 'GET' ? undefined : {} });
  console.log(`\n${m} /api/instances -> ${r.status}  ${r.body.slice(0, 120)}`);
  check(`${m} /api/instances 被拒（读端点不能对写方法回 200）`, r.status === 405 || r.status === 404,
    `status=${r.status}`);
}

// ---------- 4. /api/opencode/instances/ 子路由 ----------
//
// ⚠️ 2026-10-03 修一处**标签说谎**的断言。原第 96 行：
//     check('…stats 可达（非 404/501/503）', stats.status < 500, …)
//   判据 `status < 500` 只排除了 501/503，**没有排除 404** —— 于是 404 照样判 PASS。
//   而且它打的 URL 本身就不存在：真实路由是
//   `GET /api/opencode/instances/{instance_id}/stats`（server_opencode.go:228 的注释），
//   **必须带 instance_id**；`handleOpenCodeInstanceOperations` 用 `len(path) > 6 &&
//   path[-6:] == "/stats"` 分发，裸 `stats`（5 字符）进不去这个分支 → default 404。
//   ⇒ 404 是**正确行为**，坏的是测试。
//   另注：前端全仓**不引用** `opencode/instances`（grep 无命中），所以这组断言是
//   「后端契约」级别的，不是「App 用得到」的级别。
//
// 先取一个真实 instance_id —— 用上面 /api/instances 的结果，不再自造。
const firstInstance = insts?.[0]?.id
if (!firstInstance) {
  check('能从 /api/instances 拿到一个 instance_id（下面两条断言的前提）', false, '列表为空')
} else {
  // 4a. 缺 instance_id ⇒ 必须 404（路由要求 id，这是**正确**的拒绝）
  const noId = await api('/api/opencode/instances/stats', { token });
  console.log(`\nGET  /api/opencode/instances/stats（缺 id）-> ${noId.status}  ${noId.body.slice(0, 120)}`);
  check('缺 instance_id 的 /stats 返回 404（路由要求 id，非缺陷）', noId.status === 404, `status=${noId.status}`);

  // 4b. 带 instance_id ⇒ 必须**过了路由匹配**（非 404/501/503）。
  //     注意判据与标签必须对上：这里显式排除 404/501/503，而不是 `status < 500`。
  const withId = await api(`/api/opencode/instances/${encodeURIComponent(firstInstance)}/stats`, { token });
  console.log(`GET  /api/opencode/instances/${firstInstance}/stats -> ${withId.status}  ${withId.body.slice(0, 200)}`);
  const reachedHandler = ![404, 501, 503].includes(withId.status);
  check(`/api/opencode/instances/${firstInstance}/stats 过路由匹配（非 404/501/503）`,
    reachedHandler, `status=${withId.status}` +
    (reachedHandler && withId.status === 500
      ? '（500 = handler 到了但该实例的 OpenCode API base 未配置，属环境；' +
        '关键是**不是** 404/501/503，即路由已注册）'
      : ''));
}

const other = await api('/api/opencode/instances/anything-else', { token });
console.log(`GET  /api/opencode/instances/anything-else -> ${other.status}`);
check('其余子路径 404（前端从不调用它们，属正常）', other.status === 404, `status=${other.status}`);

const pass = checks.filter((c) => c.pass).length;
console.log(`\n=== 汇总 ===\n${pass}/${checks.length} 通过`);
if (pass !== checks.length) console.log('失败项：\n' + checks.filter((c) => !c.pass).map((c) => '  - ' + c.n).join('\n'));
process.exit(pass === checks.length ? 0 : 1);
