// 定性：/api/emails/sync 对一个**不可能连通**的 IMAP 主机返回什么？
// 后端 handleEmailSync 会把失败的账户收进 failed 数组但**仍返回 200**。
// 所以真正要问的是：**前端会不会把 failed 显示成成功。**
//
// ⚠️ 2026-10-03 三处修正（都在隔离环境 18101 上实跑暴露出来的）：
//
// 1) 原脚本**自己不建前置账户**，只 `accounts.find(a => a.imapHost === 'imap.invalid.test')`。
//    在一个干净环境里那个账户根本不存在 ⇒ 前置 FAIL ⇒ 整段探针被 `if (target)` 跳过，
//    而脚本仍然 **exit 0**。自动化无从分辨「跑过了」和「什么都没跑」。
//    现在自己建、自己删。
// 2) 结尾那段结论是**硬编码的**，写的是「前端只读 sync.new，不读 sync.failed，
//    把失败显示成了成功」。这句在写脚本时为真，但
//    `frontend/src/features/email/EmailAccountAddView.vue` 现在**已经读** `sync.failed`
//    并据此 `imapOk = false` —— 结论早已过时，留着会误导下一个读它的人。
//    现在改成**从源码推导**：读前端文件，判它到底读不读 failed。
// 3) 退出码恒为 0。改成反映判定；前置建不起来必须响亮失败。
//
// 清理（BUG-V14 纪律）：DELETE 走 finally + 异常钩子，不写在 happy path 末尾。
import http from 'node:http';
import { readFileSync } from 'node:fs';
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
    const r = http.request({ host: HOST, port: PORT, path, method, headers: h, timeout: 90000 }, (resp) => {
      let s = ''; resp.on('data', (c) => (s += c)); resp.on('end', () => res({ status: resp.statusCode, body: s }));
    });
    r.on('timeout', () => { r.destroy(new Error('request timeout')) });
    r.on('error', (e) => res({ status: 'ERR', body: e.message }));
    if (payload) r.write(payload);
    r.end();
  });
}

// ---------- 判据的可分离性自证（--selftest） ----------
//
// 绿灯本身不能证明判据有区分力。同一个仓库里栽过太多次「恒真判据」，
// 所以这里对**每一条会读外部输入的判据**都跑一遍反向对照：
// 喂它一个必须判 false 的输入，确认它真的返回 false。
function failedReported(j, addr) {
  return !!j && Array.isArray(j.failed) && j.failed.length > 0
}
function readsFailedField(src) {
  return /sync\s*\.\s*failed/.test(src) || /Array\.isArray\(\s*sync\s*\?\s*failed/.test(src)
}
function setsImapOkFalse(src) {
  return readsFailedField(src) && /imapOk\.value\s*=\s*false/.test(src)
}
if (process.argv.includes('--selftest')) {
  const cases = [
    ['failedReported({failed:[a]}) 应为 true', () => failedReported({ failed: ['a@b'] }, 'a@b') === true],
    ['failedReported({failed:[]}) 应为 false（空数组不算报告了失败）', () => failedReported({ failed: [] }) === false],
    ['failedReported({}) 应为 false（没有 failed 字段）', () => failedReported({}) === false],
    ['failedReported(null) 应为 false', () => failedReported(null) === false],
    ['failedReported("not json") 应为 false', () => failedReported('not json') === false],
    ['readsFailedField(有 sync.failed) 应为 true', () => readsFailedField('const failed = Array.isArray(sync.failed)') === true],
    ['readsFailedField(空源码) 应为 false', () => readsFailedField('') === false],
    ['readsFailedField(只读 sync.new) 应为 false', () => readsFailedField('imapMsg = `新邮件 ${sync.new} 封`') === false],
    ['setsImapOkFalse(读 failed 且置 false) 应为 true', () => setsImapOkFalse('if (sync.failed) { imapOk.value = false }') === true],
    ['setsImapOkFalse(读 failed 但仍置 true) 应为 false', () => setsImapOkFalse('if (sync.failed) { imapOk.value = true }') === false],
  ]
  let bad = 0
  for (const [name, fn] of cases) {
    let pass = false
    try { pass = fn() === true } catch (e) { pass = false }
    if (!pass) bad++
    console.log(`  ${pass ? 'PASS' : 'FAIL'}  ${name}`)
  }
  console.log(`\nselftest: ${cases.length - bad}/${cases.length} 通过`)
  process.exitCode = bad ? 1 : 0
  // selftest 之后就结束，不去碰后端
  process.exit(bad ? 1 : 0)
}

const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: devPass } });
const token = JSON.parse(login.body).token;
if (!token) { console.error('登录不通，无法继续'); process.exit(2) }
console.log('登录成功\n');

const checks = [];
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`); };

// ---------- 自建前置 ----------
const BAD_HOST = 'imap.invalid.test';       // .test 是 RFC 2606 保留 TLD，永不可解析
const ADDR = `honesty-${Date.now().toString().slice(-8)}@example.com`;
let createdId = null;
let cleaned = false;
async function cleanup(reason) {
  if (!createdId || cleaned) return;
  cleaned = true;
  const r = await api(`/api/email/accounts/${createdId}`, { token, method: 'DELETE' });
  console.log(`\n[cleanup:${reason}] DELETE 账户 ${createdId} -> ${r.status}`);
  createdId = null;
}
for (const sig of ['unhandledRejection', 'uncaughtException']) {
  process.on(sig, async (e) => { console.error(`\n[${sig}]`, e); await cleanup(sig); process.exit(1) });
}

const mk = await api('/api/email/accounts', {
  token, method: 'POST',
  body: {
    displayName: 'probe-honesty', emailAddress: ADDR,
    imapHost: BAD_HOST, imapPort: 993,
    authType: 'password', password: 'not-a-real-password',
    syncIntervalMin: 15, enabled: true,
  },
});
let mkj = null; try { mkj = JSON.parse(mk.body) } catch { /* below */ }
createdId = mkj && (mkj.id || (mkj.account && mkj.account.id));
check('前置：自建一个指向不可解析主机的账户', mk.status >= 200 && mk.status < 300 && !!createdId,
  `status=${mk.status} id=${createdId || '-'} ${mk.status >= 300 ? mk.body.slice(0, 160) : ''}`);
if (!createdId) { console.error('建不出前置账户，整段探针无从谈起 —— 响亮失败，不许 exit 0'); process.exit(3) }

try {
  // ---------- 同步一个连不上的账户 ----------
  const sync = await api('/api/emails/sync', { token, method: 'POST', body: { account_id: createdId } });
  console.log(`\nPOST /api/emails/sync -> HTTP ${sync.status}`);
  console.log('body =', String(sync.body).slice(0, 400));
  let j = null; try { j = JSON.parse(sync.body) } catch { /* below */ }
  check('后端在 failed 数组里如实报告了连不上的账户', failedReported(j, ADDR),
    j ? JSON.stringify(j.failed) : '(非 JSON)');
  check('但 HTTP 状态码仍是 200 —— 前端若不读 failed 就会显示成成功',
    sync.status === 200, `status=${sync.status}`);

  // ---------- 结论从源码推导，不硬编码 ----------
  console.log('\n--- 前端是否读 failed（从源码判定，不靠记忆）---');
  // POCKET_FE_FILE 只为**变盲对照**存在：指向一个不存在的文件，
  // 那两条前端判据必须转红 —— 证明它们不是恒真。
  const FE = process.env.POCKET_FE_FILE || 'frontend/src/features/email/EmailAccountAddView.vue';
  let src = '';
  try { src = readFileSync(FE, 'utf8') } catch (e) { console.error(`  读不到 ${FE}：${e.message}`) }
  const readsFailed = readsFailedField(src);
  const setsFalseOnFailed = setsImapOkFalse(src);
  console.log(`  ${FE}`);
  console.log(`  读 sync.failed：${readsFailed ? '是' : '否'}`);
  console.log(`  据此把 imapOk 置 false：${setsFalseOnFailed ? '是' : '否'}`);
  check('前端确实读 sync.failed（否则失败会被显示成成功）', readsFailed);
  check('前端据 failed 把结果置为失败（不是仍显示「同步成功」）', setsFalseOnFailed);
  console.log(readsFailed
    ? '  ⇒ 结论：后端如实报告失败，前端也如实呈现。**不存在**「把失败显示成成功」的问题。'
    : '  ⇒ 结论：前端不读 failed ⇒ 失败会被显示成成功，这是真缺陷。');
} finally {
  await cleanup('normal');
}

const passed = checks.filter((c) => c.pass).length;
console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过`);
if (passed !== checks.length) {
  console.error(`\n✗ 有 ${checks.length - passed} 条不通过。前置建不起来、或 failed 没被如实报告/呈现，都算失败。`);
}
process.exitCode = passed === checks.length ? 0 : 1;
