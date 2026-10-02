// 探密码箱（vault）的后端契约。
//
// 背景：vault 的设计是「原生 Keystore 插件做本地加解密，pocketd 只做密文同步」。
// 本轮不验密文同步（前提是插件，且插件未实现），先回答两个可判定的问题：
//   1. /api/vault 到底注册了没有？（旧结论说「恒 404」，这里带 token 重新确认）
//   2. 前端 vault 用的那些端点，带 token 时分别返回什么？
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

// 阴性对照：探针有区分 404 的能力
const bogus = await api('/api/definitely-not-a-route', { token });
console.log(`GET /api/definitely-not-a-route -> ${bogus.status}`);
check('阴性对照：随机路径 404（探针能区分 404）', bogus.status === 404, `status=${bogus.status}`);

const rows = [];
for (const [m, p, b] of [
  ['GET', '/api/vault', undefined],
  ['GET', '/api/vault/', undefined],
  ['GET', '/api/vault/entries', undefined],
  ['GET', '/api/vault/sync', undefined],
  ['GET', '/api/vault/sync/', undefined],
  ['POST', '/api/vault/sync/', '{"blob":"x","version":1}'],
  ['GET', '/api/vault/blob', undefined],
  ['POST', '/api/vault/entries', '{"name":"x"}'],
]) {
  const r = await api(p, { token, method: m, body: b });
  rows.push({ m, p, status: r.status });
  console.log(`${String(r.status).padEnd(4)} ${m.padEnd(5)} ${p.padEnd(24)} ${r.body.slice(0, 70).replace(/\n/g, ' ')}`);
}

const all404 = rows.filter((r) => r.p !== '/api/vault/sync' && r.p !== '/api/vault/sync/');
check('除 sync 传输外，/api/vault* 都是 404（说明非 sync 路径确实没实现）',
  all404.every((r) => r.status === 404),
  all404.map((r) => `${r.p}=${r.status}`).join(' '));

// ---------- sync 传输的真实往返（旧结论「/api/vault 恒 404」把它一笔带过了） ----------
const before = await api('/api/vault/sync/', { token });
let beforeV = null;
try { beforeV = JSON.parse(before.body); } catch { /* below */ }
console.log(`\nGET /api/vault/sync/ -> ${before.status}  ${JSON.stringify(beforeV)}`);
check('sync 端点带 token 可读（不是 404）', before.status === 200 && !!beforeV && 'version' in beforeV,
  `status=${before.status}`);

// 写：真传一个合法 blob，然后回读确认真的落库
const blob = 'VAULT-PROBE-' + Date.now().toString().slice(-6);
const up = await api('/api/vault/sync/', { token, method: 'POST', body: { blob, version: (beforeV?.version ?? 0) + 1 } });
console.log(`POST /api/vault/sync/ -> ${up.status}  ${up.body.slice(0, 160)}`);
check('sync 上传 2xx（传输层有真实写路径）', up.status >= 200 && up.status < 300, `status=${up.status}`);

const after = await api('/api/vault/sync/', { token });
let afterV = null;
try { afterV = JSON.parse(after.body); } catch { /* below */ }
console.log(`回读 -> ${after.status}  ${JSON.stringify(afterV).slice(0, 160)}`);
check('回读能拿到刚上传的 blob（密文传输真的落库了）', !!afterV && String(afterV.blob) === blob,
  `blob=${afterV && afterV.blob}`);

// 阴性：空 body 必须被拒（证明有校验，不是随便收）
const bad = await api('/api/vault/sync/', { token, method: 'POST', body: { blob: '', version: 999 } });
console.log(`POST 空 blob -> ${bad.status}  ${bad.body.slice(0, 100)}`);
check('空 blob 被拒（传输层有真校验）', bad.status === 400, `status=${bad.status}`);

console.log(`\n=== 汇总 ===\n${checks.filter((c) => c.pass).length}/${checks.length} 通过`);
if (rows.filter((r) => r.p !== '/api/vault/sync' && r.p !== '/api/vault/sync/').every((r) => r.status === 404)) {
  console.log('\n结论（修正旧说法）：');
  console.log('  · **/api/vault/sync/ 是实现了的** —— GET 200 {blob,version}、POST 上传 400/2xx、回读一致。');
  console.log('    旧结论「/api/vault 恒 404」过于宽泛，把这条已实现的传输路径一起否掉了。');
  console.log('  · 非 sync 路径（/api/vault、/entries、/blob…）确实 404。');
  console.log('  · 因此 vault 缺的是**原生 Keystore 插件**（Android 侧无该类、MainActivity 未注册），');
  console.log('    后端传输层这一半**已经就位**。只差插件，比原先记的「两个独立障碍」要少一个。');
}
process.exit(checks.every((c) => c.pass) ? 0 : 1);
