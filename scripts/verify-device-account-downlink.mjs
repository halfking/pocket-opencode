// 用真实服务端的账户数据，跑一遍需求 8 的 LWW 判定，回答一个具体问题：
// **真机首次登录（本地库为空）时，会把哪几个账户下行到本地？**
//
// 这不是重测 planAccountSync 的逻辑（account-sync.test.mjs 已覆盖），
// 而是验证「服务端真实数据 + 生产 LWW 实现」这个组合的结果，
// 尤其是夹具账户被删后真机列表是否干净。
//
// 用法：POCKET_API_BASE=http://127.0.0.1:18099 node scripts/verify-device-account-downlink.mjs
import { readFileSync } from 'node:fs';
import { planAccountSync } from '../frontend/src/features/email/account-lww.ts';

const BASE = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099';

function devPassFromSource() {
  try {
    const s = readFileSync('backend/internal/server/server_assistant.go', 'utf8');
    return (s.match(/devPass = "([^"]+)"/) || [])[1] || '';
  } catch { return ''; }
}
const PASS = process.env.POCKET_ADMIN_PASS || devPassFromSource();

const checks = [];
const check = (n, pass, d = '') => {
  checks.push({ n, pass });
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`);
};

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PASS }),
});
if (!login.ok) { console.error(`登录失败 ${login.status}`); process.exit(1); }
const { token } = await login.json();

const res = await fetch(`${BASE}/api/email/accounts`, { headers: { Authorization: `Bearer ${token}` } });
const { accounts } = await res.json();

const remote = accounts.map((a) => ({
  id: a.id,
  emailAddress: a.emailAddress,
  updatedAt: a.updatedAt ?? a.createdAt ?? 0,
}));

// 场景 1：真机全新安装，本地一个账户都没有。
const fresh = planAccountSync([], remote);
check(
  '真机首次登录：全部账户下行到本地',
  fresh.pullIds.length === remote.length && fresh.pushIds.length === 0,
  `pull=${fresh.pullIds.length}/${remote.length} push=${fresh.pushIds.length}`,
);

// 场景 2：下行后本地与服务端一致 —— 必须是幂等的，不能来回抖动。
const afterPull = planAccountSync(
  remote.map((a) => ({ ...a })),
  remote,
);
check(
  '下行后再同步是幂等的（不重复拉、不反向推）',
  afterPull.pullIds.length === 0 && afterPull.pushIds.length === 0,
  `pull=${afterPull.pullIds.length} push=${afterPull.pushIds.length}`,
);

// 场景 3：真机本地某账户被改新（离线编辑）→ 应上行。
const edited = remote.map((a, i) => (i === 0 ? { ...a, updatedAt: a.updatedAt + 60 } : { ...a }));
const up = planAccountSync(edited, remote);
check(
  '本地更新更晚 → 上行到服务端（离线编辑不丢）',
  up.pushIds.length === 1 && up.pushIds[0] === remote[0].id && up.pullIds.length === 0,
  `push=${JSON.stringify(up.pushIds)} pull=${JSON.stringify(up.pullIds)}`,
);

// 场景 4：服务端更新更晚 → 应下行覆盖本地。
const serverNewer = remote.map((a, i) => (i === 0 ? { ...a, updatedAt: a.updatedAt + 60 } : { ...a }));
const down = planAccountSync(remote, serverNewer);
check(
  '服务端更新更晚 → 下行覆盖本地',
  down.pullIds.length === 1 && down.pullIds[0] === remote[0].id && down.pushIds.length === 0,
  `pull=${JSON.stringify(down.pullIds)} push=${JSON.stringify(down.pushIds)}`,
);

// 场景 5：真机列表里不能有夹具/审计账户。
const junk = remote.filter((a) => /fixture|audit-poc|example\.test/i.test(a.emailAddress));
check('服务端已无非目标（夹具/审计）账户', junk.length === 0, junk.map((a) => a.emailAddress).join(', '));

// 场景 6：目标里的 5 个邮箱一个不少，且都带 updatedAt（LWW 判据依赖它）。
const want = [
  'huangxutao@kxpms.cn', '56551681@qq.com',
  'feikemanager@163.com', 'feikemanager1@163.com', 'kimmy.huang@163.com',
];
const got = new Set(remote.map((a) => a.emailAddress));
const missing = want.filter((w) => !got.has(w));
check('目标 5 个邮箱齐备', missing.length === 0, missing.length ? '缺: ' + missing.join(', ') : `实到 ${got.size} 个`);
const noStamp = remote.filter((a) => !(a.updatedAt > 0));
check('每个账户都有 updatedAt（LWW 判据）', noStamp.length === 0, noStamp.map((a) => a.emailAddress).join(', '));

console.log(`\n服务端实际账户（真机将看到的列表）:`);
for (const a of accounts) {
  console.log(`  ${a.emailAddress.padEnd(24)} imap=${a.imapHost}:${a.imapPort}  updatedAt=${a.updatedAt}`);
}

const failed = checks.filter((c) => !c.pass).length;
console.log(`\n${checks.length - failed} PASS / ${failed} FAIL  base=${BASE}`);
process.exit(failed ? 1 : 0);
