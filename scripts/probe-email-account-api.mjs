// 探测邮箱账户创建的真实后端行为，为真机 UI 验证做准备。
// 重点验证两个预测：
//   1) emailCrypto 未配置时 createEmailAccount 返回 503（不是 201）
//   2) 前端把 >=500 当成「云端暂未实现」，回落写 localStorage → 界面上「成功」但 PG 里没有
// 直接打后端，不经设备，先把后端契约钉死。
import http from 'node:http';
import { requireDevPass } from './lib/dev-pass.mjs'
const HOST = '127.0.0.1';
const PORT = 8088;

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
let token = '';
try { token = JSON.parse(login.body).token || ''; } catch { /* report below */ }
if (!token) { console.log('登录失败', login.status, login.body.slice(0, 200)); process.exit(1); }
console.log('登录成功\n');

const checks = [];
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`); };

// 1. 先看当前能不能列账户（决定前置状态）
const before = await api('/api/email/accounts', { token });
console.log(`GET /api/email/accounts -> ${before.status} ${before.body.slice(0, 160)}`);
check('GET /api/email/accounts 可达（不是 404/501）', before.status === 200, `status=${before.status}`);

// 2. 尝试创建 —— 关键看是 201 还是 503
const stamp = Date.now().toString().slice(-6);
const body = {
  displayName: `PROBE-${stamp}`,
  emailAddress: `probe${stamp}@example.com`,
  imapHost: 'imap.example.com',
  imapPort: 993,
  authType: 'password',
  syncIntervalMin: 15,
  enabled: true,
  password: 'dummy-not-a-real-password',
};
const created = await api('/api/email/accounts', { token, method: 'POST', body });
console.log(`POST /api/email/accounts -> ${created.status} ${created.body.slice(0, 200)}`);

const is503 = created.status === 503;
const is201 = created.status === 201;
if (is503) {
  check('创建返回 503（emailCrypto 未配置：缺 POCKET_EMAIL_MASTER_KEY）', true, created.body.slice(0, 120));
  check('⚠️  前端会把 >=500 回落写 localStorage，界面显示「已保存到本地（云端暂未实现）」', true,
    '—— 对用户掩盖了「后端未配置」这个真实原因');
} else if (is201) {
  check('创建返回 201（emailCrypto 已配置）', true, created.body.slice(0, 120));
} else {
  check('创建返回既非 201 也非 503 —— 需要单独定性', false, `status=${created.status}`);
}

// 3. 阴性对照：确认 503 不是「路由不存在」
const bogus = await api('/api/email/definitely-not-a-route', { token });
console.log(`GET /api/email/definitely-not-a-route -> ${bogus.status}`);
check('阴性对照：随机邮箱路径确实 404（证明探针能区分 404 与 503）', bogus.status === 404, `status=${bogus.status}`);

console.log(`\n=== 汇总 ===\n${checks.filter((c) => c.pass).length}/${checks.length} 通过`);
process.exit(checks.every((c) => c.pass) ? 0 : 1);
