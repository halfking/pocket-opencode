// 专查一条可疑行为：POST /api/vault/sync/ 收到**空 blob** 时回 200 {"ok":true}。
//
// 为什么这条可疑：vault 的密文同步是「上传整块 blob」语义。
// 如果客户端因为原生 Keystore 插件缺失、拿不到任何数据而上传空 blob，
// 服务端又照单全收并回 ok，那么一次「插件没装」的静默失败
// 就会把用户已存的密文**清空**，而两边都显示「同步成功」。
//
// 本脚本验证的是「空 blob 上传是否真的覆盖了已存数据」，不是「状态码好不好看」。
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
if (!token) { console.log('登录失败'); process.exit(1); }
console.log('登录成功\n');

const read = async () => {
  const r = await api('/api/vault/sync/', { token });
  try { return JSON.parse(r.body); } catch { return null; }
};
const write = async (blob, version) => {
  const r = await api('/api/vault/sync/', { token, method: 'POST', body: { blob, version } });
  let j = null; try { j = JSON.parse(r.body); } catch { /* below */ }
  return { status: r.status, body: j };
};

// 步骤 1：先放一段有内容的 blob
const sentinel = 'SENTINEL-' + Date.now().toString().slice(-6);
const w1 = await write(sentinel, 1);
console.log(`1) 写入哨兵 blob=${sentinel} -> ${w1.status} ${JSON.stringify(w1.body)}`);
const r1 = await read();
console.log(`   回读 = ${JSON.stringify(r1)}`);
check('哨兵 blob 写入并回读成功', r1 && r1.blob === sentinel, `blob=${r1 && r1.blob}`);

// 步骤 2：模拟「插件缺失 → 客户端拿不到数据 → 上传空 blob」
const w2 = await write('', (r1?.version ?? 1) + 1);
console.log(`\n2) 上传**空** blob（模拟客户端无数据）-> ${w2.status} ${JSON.stringify(w2.body)}`);
check('空 blob 被拒（应为 4xx）', w2.status >= 400, `status=${w2.status}`);

// 步骤 3：数据是否还在？—— 这条才是「有没有造成损失」
const r2 = await read();
console.log(`   回读 = ${JSON.stringify(r2)}`);
const lost = !!(r2 && r2.blob === '');
check('⚠️ 上传空 blob 后，哨兵数据**仍在**（没有被静默清空）', !lost,
  lost ? `哨兵 ${sentinel} 已被空 blob 覆盖！` : `blob 仍是 ${JSON.stringify(r2 && r2.blob)}`);

if (lost) {
  console.log('\n结论：服务端接受空 blob 并覆盖已有密文，且回 200 ok ——');
  console.log('      一次「客户端没数据」的静默失败会**销毁用户已存的密码箱**，而两边都显示成功。');
  console.log('      这与 BUG-AC（把失败显示成成功）同类，只是后果从「误导」升级为「丢数据」。');
  console.log('      建议修法：blob 为空时返回 400，且不做任何写入。');
} else {
  console.log('\n结论：空 blob 未造成覆盖（服务端可能已有保护，或空 blob 被当作 no-op）。');
}

// 收尾：把哨兵清掉，别把测试数据留在密码箱里
const clr = await write('', (r2?.version ?? 0) + 1);
console.log(`\n收尾：上传空 blob 清理哨兵 -> ${clr.status}，回读 = ${JSON.stringify(await read())}`);

const pass = checks.filter((c) => c.pass).length;
console.log(`\n=== 汇总 ===\n${pass}/${checks.length} 通过`);
