// 通过 pocketd 登录 API 拿 token，再读网关配置 + 直接打网关冒烟
const BASE = 'http://127.0.0.1:8088';
const USER = process.env.POCKET_USER || 'admin';
const PASS = process.env.POCKET_PASS;

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: USER, password: PASS }),
});
const lj = await login.json();
if (!login.ok) {
  console.log(`登录失败 ${login.status}: ${JSON.stringify(lj).slice(0, 300)}`);
  process.exit(1);
}
const token = lj.token || lj.access_token || lj.data?.token;
console.log(`登录 OK，token 长度 ${String(token).length}`);
const H = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };

const cfgRes = await fetch(`${BASE}/api/llm-gateway/config`, { headers: H });
const cfg = await cfgRes.json();
console.log(`GET /api/llm-gateway/config -> ${cfgRes.status}`);
console.log(`  baseURL = ${cfg.baseURL ?? cfg.data?.baseURL}`);
const pm = cfg.preferredModels ?? cfg.data?.preferredModels ?? [];
console.log(`  preferredModels(${pm.length}) = ${pm.join(', ')}`);
const hasKey = !!(cfg.apiKey ?? cfg.data?.apiKey);
console.log(`  apiKey 已下发 = ${hasKey}`);

// 用 pocketd 解析出的 baseURL + key 直连网关，验证整条链路
const gw = await fetch(`${cfg.baseURL}/models`, { headers: { Authorization: `Bearer ${cfg.apiKey}` } });
const gj = await gw.json();
console.log(`直连 ${cfg.baseURL}/models -> ${gw.status}，模型数 ${(gj.data || []).length}`);

// 挑一个 preferred 模型实测流式
const model = pm[0] || (gj.data || [])[0]?.id;
console.log(`实测模型 = ${model}`);
const t0 = Date.now();
const res = await fetch(`${cfg.baseURL}/chat/completions`, {
  method: 'POST', headers: { ...H, Authorization: `Bearer ${cfg.apiKey}` },
  body: JSON.stringify({ model, messages: [{ role: 'user', content: '回复两个字：收到' }], max_tokens: 40, stream: true }),
});
console.log(`流式 HTTP ${res.status} · ${res.headers.get('content-type')}`);
if (res.status !== 200) {
  console.log(`错误体: ${(await res.text()).slice(0, 400)}`);
} else {
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let text = '', first = 0, lines = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    if (!first) first = Date.now() - t0;
    lines++;
    for (const ln of dec.decode(value, { stream: true }).split('\n')) {
      if (!ln.startsWith('data:')) continue;
      const p = ln.slice(5).trim();
      if (p === '[DONE]') continue;
      try { text += JSON.parse(p).choices?.[0]?.delta?.content || ''; } catch { /* ignore */ }
    }
  }
  console.log(`首字节 ${first}ms，总耗时 ${Date.now() - t0}ms`);
  console.log(`回复: ${JSON.stringify(text)}`);
}
