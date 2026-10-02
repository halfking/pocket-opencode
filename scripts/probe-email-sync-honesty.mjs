// 定性：/api/emails/sync 对一个**不可能连通**的 IMAP 主机返回什么？
// 后端 handleEmailSync 会把失败的账户收进 failed 数组但**仍返回 200**。
// 如果前端不读 failed，就会把失败显示成「同步成功」。
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
const token = JSON.parse(login.body).token;
console.log('登录成功\n');

const checks = [];
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`); };

// 取账户列表，确认那个 imap.invalid.test 的账户还在
const list = await api('/api/email/accounts', { token });
const accounts = JSON.parse(list.body).accounts || [];
console.log('账户 =', JSON.stringify(accounts.map((a) => ({ id: a.id, addr: a.emailAddress, host: a.imapHost, enabled: a.enabled })), null, 1));
const target = accounts.find((a) => a.imapHost === 'imap.invalid.test');
check('找到那个指向不存在主机的账户（前置）', !!target, target ? target.emailAddress : '(没找到)');

if (target) {
  const sync = await api('/api/emails/sync', { token, method: 'POST', body: { account_id: target.id } });
  console.log(`\nPOST /api/emails/sync -> HTTP ${sync.status}`);
  console.log('body =', sync.body);
  let j = null;
  try { j = JSON.parse(sync.body) } catch { /* below */ }
  check('后端在 failed 数组里如实报告了连不上的账户',
    !!j && Array.isArray(j.failed) && j.failed.includes(target.emailAddress),
    j ? JSON.stringify(j.failed) : '(非 JSON)');
  check('但 HTTP 状态码仍是 200（前端若不读 failed 就会显示成成功）',
    sync.status === 200, `status=${sync.status}`);
  console.log(`\n结论：后端如实报告了失败；前端 EmailAccountAddView 只读 \`sync.new\`，不读 \`sync.failed\`，`);
  console.log('      所以界面上显示的是「IMAP：同步成功，新邮件 0 封」——**把失败显示成了成功**。');
}

console.log(`\n=== 汇总 ===\n${checks.filter((c) => c.pass).length}/${checks.length} 通过`);
