// PC 侧：登录取 token 并解出 claims，与设备侧对比
const BASE = 'http://127.0.0.1:8088';
const r = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: process.env.POCKET_PASS }),
});
const j = await r.json();
if (!r.ok) { console.log('登录失败', r.status, JSON.stringify(j).slice(0, 200)); process.exit(1); }
const t = j.token || j.access_token;
const b64 = t.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
const claims = JSON.parse(decodeURIComponent(escape(atob(b64 + '='.repeat((4 - b64.length % 4) % 4)))));
console.log('PC  claims = ' + JSON.stringify(claims));
const cfg = await (await fetch(`${BASE}/api/llm-gateway/config`, { headers: { Authorization: `Bearer ${t}` } })).json();
console.log(`PC  baseURL=${cfg.baseURL} apiKeySet=${cfg.apiKeySet}`);
