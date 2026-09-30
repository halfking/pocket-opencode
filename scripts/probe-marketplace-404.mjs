// 核实验证器的低优先级指控：「/api/marketplace/agents 的 404 说法在只读探测下
// 无法证实（返回 401）」。
//
// 要点：401 是**认证中间件在路由之前**返回的，说明这条路径根本没走到路由匹配。
// 必须带 token 再测，才能区分「路由不存在(404)」和「路由存在但我没带 token(401)」。
// 同时拿一个**确定存在**的同类端点做阳性对照，证明探针本身能区分 404/401/200。
import http from 'node:http';
import { readFileSync } from 'node:fs';

const HOST = '127.0.0.1';
const PORT = 8088;

const devPass =
  (readFileSync('backend/internal/server/server_assistant.go', 'utf8').match(/devPass\s*=\s*"([^"]+)"/) || [])[1] || '';

function api(path, token, method = 'GET') {
  return new Promise((res) => {
    const h = {};
    if (token) h.Authorization = 'Bearer ' + token;
    const r = http.request({ host: HOST, port: PORT, path, method, headers: h }, (resp) => {
      let s = '';
      resp.on('data', (c) => (s += c));
      resp.on('end', () => res({ status: resp.statusCode, body: s }));
    });
    r.on('error', (e) => res({ status: 'ERR', body: e.message }));
    r.end();
  });
}

const login = await api('/api/auth/login', '', 'POST');
// 上面这个没带 body，用专门的登录
const token = await new Promise((res) => {
  const payload = JSON.stringify({ username: 'admin', password: devPass });
  const r = http.request(
    { host: HOST, port: PORT, path: '/api/auth/login', method: 'POST', headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(payload) } },
    (resp) => { let s = ''; resp.on('data', (c) => (s += c)); resp.on('end', () => { try { res(JSON.parse(s).token || ''); } catch { res(''); } }); }
  );
  r.write(payload); r.end();
});
if (!token) { console.log('登录失败，无法继续'); process.exit(1); }
console.log('已登录，拿到 token\n');

// 阴性对照：确定**不存在**的路径，带 token 也应是 404
// 阳性对照：确定**存在**的端点，带 token 应是 200
const CASES = [
  ['/api/marketplace/agents',        '指控对象：handoff 说是 404'],
  ['/api/marketplace/skills',         '指控对象：handoff 说是 404'],
  ['/api/marketplace/installs',       '指控对象：handoff 说是 404'],
  ['/api/marketplace/router',         '指控对象：handoff 说是 404'],
  ['/api/marketplace/packages',       '阳性对照：确定存在，应 200'],
  ['/api/marketplace/definitely-not-a-real-route-xyz', '阴性对照：确定不存在，应 404'],
];

let claims404 = 0;
let mismatch = 0;
for (const [p, note] of CASES) {
  const a = await api(p);           // 无 token
  const b = await api(p, token);    // 带 token
  console.log(`${p}`);
  console.log(`   无 token: ${a.status}    带 token: ${b.status}    — ${note}`);
  console.log(`   body(带token): ${b.body.slice(0, 120)}`);
  const is404 = b.status === 404;
  const is401 = b.status === 401;
  if (is401) {
    console.log(`   ⚠️ 仍返回 401 —— 认证没生效，本次探测**无法判定**该路由是否存在`);
    mismatch++;
  } else if (note.includes('应是 200') && !is404) { /* 阳性对照通过 */ }
  else if (note.includes('应是 404') && is404) { /* 阴性对照通过 */ }
  else if (note.includes('handoff 说是 404')) {
    if (is404) claims404++; else { console.log(`   ❌ handoff 说是 404，实际 ${b.status}`); mismatch++; }
  }
}

console.log(`\n=== 汇总 ===\n被证实为 404 的: ${claims404}/4 个（4 个都是 handoff 点名的）`);
console.log(`无法判定(401): ${mismatch} 个`);
process.exit(mismatch === 0 ? 0 : 1);
