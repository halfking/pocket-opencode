// 核实一条被质疑的旧结论：handoff 里写过「/api/marketplace/agents 恒 404」。
// 质疑点很具体：那次探测没带 token，回的是 401 而不是 404，
// 所以「404」这个说法当时就立不住 —— 现在把三种情况分开打清楚。
//
//   1. 不带 token       -> 鉴权层结果（很可能 401，这不是路由结论）
//   2. 带 token         -> 才是路由层的真实结果
//   3. 随机路径带 token  -> 阴性对照，证明探针有区分 404 的能力
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

const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: devPass } });
let token = null;
try { token = JSON.parse(login.body).token; } catch { /* below */ }
if (!token) { console.log('登录失败：' + login.status + ' ' + login.body.slice(0, 120)); process.exit(1); }
console.log('登录成功\n');

const rows = [];
const probe = async (label, path, opts) => {
  const r = await api(path, opts);
  rows.push({ label, path, status: r.status, body: r.body.slice(0, 90) });
  console.log(`${String(r.status).padEnd(4)} ${label.padEnd(26)} ${path}`);
  console.log(`     body = ${r.body.slice(0, 90).replace(/\n/g, ' ')}`);
  return r;
};

// 1) 不带 token —— 旧结论的来源
await probe('不带 token', '/api/marketplace/agents');
// 2) 带 token —— 路由层真实结果
await probe('带 token', '/api/marketplace/agents', { token });
// 3) 阴性对照：确实不存在的路径（带 token）
await probe('阴性对照（随机路径）', '/api/marketplace/definitely-not-a-route', { token });
// 4) 对照组：已知存在的同族端点（带 token）
await probe('同族端点（对照）', '/api/marketplace/packages', { token });

console.log('\n=== 结论 ===');
const noTok = rows[0], withTok = rows[1], bogus = rows[2];
if (noTok.status === 401) console.log('不带 token = 401：**旧探测的 401 只是鉴权层，不是路由结论**。');
console.log(`带 token = ${withTok.status} —— 这才是 /api/marketplace/agents 的真实路由行为。`);
console.log(`阴性对照（随机路径，带 token）= ${bogus.status} —— 探针能区分 404。`);
const conclusion = withTok.status === 404
  ? '「恒 404」在**带 token** 的前提下成立，但旧结论是拿未鉴权的 401 得出的，**推理链是错的**。'
  : `「恒 404」**不成立**：带 token 时它回 ${withTok.status}。`;
console.log(conclusion);
