// 列出 llm_gateway 的 user settings，验证「读时 user setting 盖过工作区快照」假设
const BASE = 'http://127.0.0.1:8088';
const r = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: process.env.POCKET_PASS }),
});
const j = await r.json();
const H = { Authorization: `Bearer ${j.token || j.access_token}`, 'Content-Type': 'application/json' };

for (const url of [
  `${BASE}/api/user-settings?namespace=llm_gateway`,
  `${BASE}/api/user-settings/llm_gateway/default`,
  `${BASE}/api/user-settings?namespace=llm_gateway&workspace_id=default`,
]) {
  const res = await fetch(url, { headers: H });
  const body = await res.text();
  console.log(`\nGET ${url.replace(BASE, '')} -> ${res.status}`);
  console.log('  ' + body.slice(0, 400));
}
