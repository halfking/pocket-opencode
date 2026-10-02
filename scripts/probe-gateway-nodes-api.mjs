// 探网关节点的后端契约，为真机 UI 验证做准备。
// 邮箱那次的教训：先探 API 再点 UI —— 后端若直接 503/400，
// 可以在不占用设备、不受 UI 选择器干扰的前提下把「后端不支持」和「UI 有 bug」分开。
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
const token = JSON.parse(login.body).token;
console.log('登录成功\n');

const checks = [];
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`); };

// 1. 列表
const list = await api('/api/llm-gateway/nodes', { token });
console.log(`GET /api/llm-gateway/nodes -> ${list.status}  ${list.body.slice(0, 200)}`);
check('列表端点可达（非 404/501/503）', list.status === 200, `status=${list.status}`);
let nodes = [];
try { nodes = JSON.parse(list.body).nodes || []; } catch { /* below */ }
console.log('现有节点 =', JSON.stringify(nodes.map((n) => ({ id: n.id, name: n.name, base: n.base_url }))));

// 2. 空 body 创建 —— 期望 400（证明有校验）
const empty = await api('/api/llm-gateway/nodes', { token, method: 'POST', body: {} });
console.log(`\nPOST 空 body -> ${empty.status}  ${empty.body.slice(0, 160)}`);
check('空 body 被校验拒绝（证明创建路径有真实校验）', empty.status === 400, `status=${empty.status}`);

// 3. 完整 body 创建
const stamp = Date.now().toString().slice(-6);
const payload = {
  name: `PROBE-NODE-${stamp}`,
  baseURL: `https://probe-${stamp}.invalid.test`,
  adminUsername: 'probe-admin',
  adminPassword: 'probe-not-a-real-password',
  enabled: true,
};
const created = await api('/api/llm-gateway/nodes', { token, method: 'POST', body: payload });
console.log(`\nPOST 完整 body -> ${created.status}  ${created.body.slice(0, 260)}`);
const ok2xx = created.status >= 200 && created.status < 300;
check('完整 body 可创建（2xx）', ok2xx, `status=${created.status}${ok2xx ? '' : ' ' + created.body.slice(0, 160)}`);

let createdId = null;
if (ok2xx) { try { createdId = JSON.parse(created.body).id } catch { /* */ } }

// 4. 阴性对照：随机路径确实 404（证明探针能区分 404 与其它）
const bogus = await api('/api/llm-gateway/definitely-not-a-route', { token });
console.log(`\nGET /api/llm-gateway/definitely-not-a-route -> ${bogus.status}`);
check('阴性对照：随机网关路径 404（探针有区分能力）', bogus.status === 404, `status=${bogus.status}`);

// 5. 更新（PUT）
if (createdId) {
  const upd = await api(`/api/llm-gateway/nodes/${createdId}`, { token, method: 'PUT', body: { ...payload, name: `PROBE-NODE-${stamp}-RENAMED` } });
  console.log(`\nPUT /api/llm-gateway/nodes/${createdId} -> ${upd.status}  ${upd.body.slice(0, 200)}`);
  check('更新可用（2xx）', upd.status >= 200 && upd.status < 300, `status=${upd.status}`);
  const after = await api('/api/llm-gateway/nodes', { token });
  const n2 = (JSON.parse(after.body).nodes || []).find((n) => n.id === createdId);
  check('更新真的生效（name 变为 RENAMED）', !!n2 && /RENAMED/.test(n2.name), n2 ? n2.name : '(找不到)');
}

// 6. 删除（DELETE）—— 顺带留个接口，不自动删
if (createdId) console.log(`\n（探针节点 id=${createdId} 留在库里，稍后由调用方清理）`);

console.log(`\n=== 汇总 ===\n${checks.filter((c) => c.pass).length}/${checks.length} 通过`);
if (createdId) console.log(`PROBE_NODE_ID=${createdId}`);
process.exit(checks.every((c) => c.pass) ? 0 : 1);
