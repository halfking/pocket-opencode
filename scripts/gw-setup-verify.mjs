// 网关「默认设置」落地 + 端到端验证（走 pocketd，不自己拼 key）
// 1) POST /api/llm-gateway/config 把 baseURL/apiKey 持久化（DB 旧行会盖过代码默认）
// 2) POST /api/llm-gateway/test 用服务端真实 key 打网关 /v1/models
// 3) POST /api/llm/stream 走应用真正使用的 BFF 流式链路
import { readFileSync } from 'node:fs'

const BASE = 'http://127.0.0.1:8088';
const GW_BASE = 'https://llm.kxpms.cn/v1';
const KEY = process.env.POCKET_GW_KEY
  || readFileSync('logs/.gateway-key', 'utf8').trim();
const PASS = process.env.POCKET_PASS;

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PASS }),
});
const lj = await login.json();
if (!login.ok) { console.log(`登录失败 ${login.status}: ${JSON.stringify(lj).slice(0, 200)}`); process.exit(1); }
const H = { Authorization: `Bearer ${lj.token || lj.access_token}`, 'Content-Type': 'application/json' };
console.log('1) 登录 OK');

const before = await (await fetch(`${BASE}/api/llm-gateway/config`, { headers: H })).json();
console.log(`   改前 baseURL = ${before.baseURL}  apiKeySet=${before.apiKeySet}  (${before.apiKey})`);

const save = await fetch(`${BASE}/api/llm-gateway/config`, {
  method: 'POST', headers: H,
  body: JSON.stringify({ baseURL: GW_BASE, apiKey: KEY }),
});
const sj = await save.json();
console.log(`2) POST config -> ${save.status} ${JSON.stringify(sj).slice(0, 160)}`);
if (!save.ok) process.exit(1);

const after = await (await fetch(`${BASE}/api/llm-gateway/config`, { headers: H })).json();
console.log(`   改后 baseURL = ${after.baseURL}  apiKeySet=${after.apiKeySet}`);

const test = await fetch(`${BASE}/api/llm-gateway/test`, { method: 'POST', headers: H, body: '{}' });
const tj = await test.json().catch(() => ({}));
console.log(`3) POST /api/llm-gateway/test -> ${test.status}`);
console.log(`   ${JSON.stringify(tj).slice(0, 400)}`);

const model = process.env.GW_MODEL || (Array.isArray(tj.models) ? tj.models[0] : null) || after.preferredModels?.[0];
console.log(`   可用模型数 = ${Array.isArray(tj.models) ? tj.models.length : 'n/a'}  选用 = ${model}`);

console.log('4) 走 /api/llm/stream（应用真实链路）');
const t0 = Date.now();
const res = await fetch(`${BASE}/api/llm/stream`, {
  method: 'POST', headers: H,
  body: JSON.stringify({
    model: model || 'gpt-4o-mini',
    messages: [{ role: 'user', content: '只回复两个字：收到' }],
    stream: true,
  }),
});
console.log(`   HTTP ${res.status} · ${res.headers.get('content-type')}`);
if (res.status !== 200) {
  console.log(`   错误体: ${(await res.text()).slice(0, 500)}`);
  process.exit(2);
}
const reader = res.body.getReader();
const dec = new TextDecoder();
let text = '', first = 0, evts = 0, errEvent = null;
while (true) {
  const { done, value } = await reader.read();
  if (done) break;
  if (!first) first = Date.now() - t0;
  for (const ln of dec.decode(value, { stream: true }).split('\n')) {
    const t = ln.trim();
    if (!t.startsWith('data:')) continue;
    evts++;
    const p = t.slice(5).trim();
    if (p === '[DONE]') continue;
    if (process.env.GW_RAW) console.log(`   RAW: ${p.slice(0, 300)}`);
    try {
      const o = JSON.parse(p);
      if (o.error) errEvent = o.error;
      const d = o.choices?.[0]?.delta?.content ?? o.delta?.content ?? o.content ?? '';
      text += d;
    } catch { /* ignore */ }
  }
}
console.log(`   首字节 ${first}ms · 总耗时 ${Date.now() - t0}ms · SSE 事件 ${evts}`);
if (errEvent) console.log(`   流内错误事件: ${JSON.stringify(errEvent)}`);
console.log(`   回复: ${JSON.stringify(text)}`);
console.log(text.trim() ? '\n✅ 网关默认设置已落地且端到端可用' : '\n❌ 未拿到回复内容');
