// 端到端验证 main 构建出的 pocketd：延后提醒不能把提醒往回拉。
//
// 走真实 HTTP 链路（登录 → 建提醒 → snooze → 读回），不碰数据库，
// 因为这条修复的价值恰恰在于「服务对外表现正确」，而单测只覆盖到 store 层。
//
// 密码从环境变量 POCKET_DEV_PASS 读，不写进仓库。
//
// 用法：
//   $env:POCKET_PROBE_BASE='http://127.0.0.1:8098'
//   $env:POCKET_DEV_PASS='...'
//   node scripts/verify-snooze-not-backwards.mjs

const base = process.env.POCKET_PROBE_BASE || 'http://127.0.0.1:8098';
const pass = process.env.POCKET_DEV_PASS;
if (!pass) {
  console.error('需要 POCKET_DEV_PASS 环境变量（dev 旁路密码不入库）');
  process.exit(2);
}

const nowSec = () => Math.floor(Date.now() / 1000);

async function call(path, { method = 'GET', token, body } = {}) {
  const res = await fetch(`${base}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    ...(body ? { body: JSON.stringify(body) } : {}),
  });
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* 非 JSON 原样保留 */ }
  return { status: res.status, json, text };
}

function fail(msg) { console.error(`FAIL: ${msg}`); process.exit(1); }

const login = await call('/api/auth/login', {
  method: 'POST',
  body: { username: 'admin', password: pass },
});
if (login.status !== 200) fail(`登录返回 ${login.status}: ${login.text.slice(0, 200)}`);
const token = login.json?.token || login.json?.accessToken || login.json?.data?.token;
if (!token) fail(`登录成功但没拿到 token，响应字段: ${Object.keys(login.json || {}).join(',')}`);

const DAY = 86400;
const created = await call('/api/learning/reminders', {
  method: 'POST',
  token,
  body: {
    kind: 'daily_digest',
    ruleKind: 'daily',
    ruleValue: '08:00',
    // 排到 24 小时后 —— 这正是原实现会把它往前拉的场景
    nextDueAt: nowSec() + DAY,
  },
});
if (created.status !== 200 && created.status !== 201) {
  fail(`建提醒返回 ${created.status}: ${created.text.slice(0, 200)}`);
}
const id = created.json?.id || created.json?.reminder?.id;
if (!id) fail(`建提醒成功但没返回 id，字段: ${Object.keys(created.json || {}).join(',')}`);

const scheduledAt = created.json?.nextDueAt ?? created.json?.reminder?.nextDueAt;
console.log(`建提醒 id=${id} nextDueAt=${scheduledAt}（now+${scheduledAt - nowSec()}s）`);

const snoozed = await call(`/api/learning/reminders/${id}/snooze`, {
  method: 'POST',
  token,
  body: { minutes: 120 },
});
if (snoozed.status !== 200) fail(`snooze 返回 ${snoozed.status}: ${snoozed.text.slice(0, 200)}`);

// 读回，以服务端为准，不信返回值
const list = await call('/api/learning/reminders', { token });
if (list.status !== 200) fail(`列提醒返回 ${list.status}`);
const items = Array.isArray(list.json) ? list.json
  : list.json?.reminders || list.json?.items || list.json?.data || [];
const mine = items.find(r => (r.id || r.reminderId) === id);
if (!mine) fail(`读回列表里找不到 ${id}，返回: ${list.text.slice(0, 200)}`);

const after = mine.nextDueAt ?? mine.next_due_at;
const deltaHours = ((after - nowSec()) / 3600).toFixed(2);
console.log(`snooze 120 分钟后 nextDueAt=${after}（now+${deltaHours}h）`);

if (after <= scheduledAt) {
  fail(`延后把提醒从 now+24h 拉回到了 now+${deltaHours}h —— 修复未生效`);
}
console.log(`PASS: 延后只会把提醒推得更晚（${scheduledAt} → ${after}）`);

// 清理：ack 掉这条探针提醒
await call(`/api/learning/reminders/${id}/ack`, { method: 'POST', token, body: {} });
console.log('已 ack 清理探针提醒');
