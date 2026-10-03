// 验证应用自身的 AI 对话链路是否真的可用（用户诉求：完成网关部署与测试验证）。
//
// 为什么不直接打网关：网关侧已经证过 /v1/chat/completions 正常（19 次探测 0 超时），
// 但「网关可用」不等于「应用可用」——应用还要解析配置、选模型、遇 503 no_candidate
// 回退、解析 SSE。真正要回答的是最后这一层。
//
// 用法：
//   $env:POCKET_PROBE_BASE='http://127.0.0.1:8098'
//   $env:POCKET_DEV_PASS='...'
//   node scripts/verify-app-llm-chat.mjs

const base = process.env.POCKET_PROBE_BASE || 'http://127.0.0.1:8098';
const pass = process.env.POCKET_DEV_PASS;
if (!pass) { console.error('需要 POCKET_DEV_PASS'); process.exit(2); }

const TIMEOUT_MS = 40000;

async function call(path, { method = 'GET', token, body, stream = false } = {}) {
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), TIMEOUT_MS);
  const t0 = Date.now();
  try {
    const res = await fetch(`${base}${path}`, {
      method, signal: ac.signal,
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      ...(body ? { body: JSON.stringify(body) } : {}),
    });
    if (stream) {
      const text = await res.text();
      return { status: res.status, ms: Date.now() - t0, text };
    }
    const text = await res.text();
    let json = null;
    try { json = JSON.parse(text); } catch { /* 原样 */ }
    return { status: res.status, ms: Date.now() - t0, json, text };
  } catch (e) {
    return { status: 0, ms: Date.now() - t0, json: null,
      text: e.name === 'AbortError' ? `客户端超时 >${TIMEOUT_MS}ms` : e.message };
  } finally { clearTimeout(timer); }
}

const login = await call('/api/auth/login', { method: 'POST', body: { username: 'admin', password: pass } });
if (login.status !== 200) { console.error(`登录失败 ${login.status}: ${login.text.slice(0, 150)}`); process.exit(1); }
const token = login.json?.token || login.json?.accessToken || login.json?.data?.token;
if (!token) { console.error('登录成功但无 token'); process.exit(1); }
console.log(`登录成功 (${login.ms}ms)`);

// 先看应用自己怎么报告网关状态
const status = await call('/api/integration/status', { token });
if (status.status === 200 && status.json?.integrations?.llm_gateway) {
  const gw = status.json.integrations.llm_gateway;
  console.log(`应用自报 llm_gateway: enabled=${gw.enabled} configured=${gw.configured} source=${gw.source ?? 'n/a'}`);
}

const models = await call('/api/llm/models', { token });
if (models.status === 200) {
  const list = Array.isArray(models.json) ? models.json
    : models.json?.models || models.json?.data || [];
  console.log(`模型列表 ${Array.isArray(list) ? list.length : '?'} 个` +
    (Array.isArray(list) && list.length ? `，前几个: ${list.slice(0, 5).map(m => typeof m === 'string' ? m : m.id).join(', ')}` : ''));
} else {
  console.log(`/api/llm/models → ${models.status}: ${models.text.slice(0, 100)}`);
}

// 真正的对话请求
console.log('\n--- /api/llm/chat ---');
const chat = await call('/api/llm/chat', {
  method: 'POST', token,
  body: { messages: [{ role: 'user', content: 'reply with the single word: ok' }] },
});
console.log(`HTTP ${chat.status}  用时 ${chat.ms}ms`);
const body = chat.json ?? chat.text;
console.log('响应片段: ' + String(typeof body === 'string' ? body : JSON.stringify(body)).slice(0, 300).replace(/\s+/g, ' '));

if (chat.status === 200) process.exit(0);
console.log(`\n结论：应用侧 AI 对话当前不可用（HTTP ${chat.status}）。`);
process.exit(1);
