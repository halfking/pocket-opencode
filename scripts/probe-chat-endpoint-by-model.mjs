// 查清 llm.kxpms.cn 上 /v1/chat/completions 到底是「端点不支持」还是「模型不支持」。
//
// 背景：此前只知道 chat/completions 超时而 /v1/messages、/v1/responses 正常，
// 于是得出「需要做协议适配」的结论。但那个结论把「端点不支持」和
// 「这个模型在该端点上挂起」混为一谈了——若是后者，改配置即可，不必写几百行适配。
//
// 方法：对多个模型各发一次**极小**请求（max_tokens=8），单独计时。
// 负控：用错密钥发一次，确认网关确实在拒绝而不是静默挂起。
//
// 用法：
//   $env:GATEWAY_KEY='...'
//   node scripts/probe-chat-endpoint-by-model.mjs

const base = process.env.GATEWAY_BASE || 'https://llm.kxpms.cn/v1';
const key = process.env.GATEWAY_KEY;
if (!key) {
  console.error('需要 GATEWAY_KEY 环境变量');
  process.exit(2);
}

// 覆盖几种形态：小模型、大模型、带/不带 thinking 的常见命名
const MODELS = [
  'gpt-4o-mini',
  'gpt-4o',
  'gpt-4.1-mini',
  'claude-3-5-haiku-latest',
  'claude-sonnet-4-5',
  'gemini-2.5-flash',
  'deepseek-chat',
  'qwen-max',
];

const TIMEOUT_MS = 20000;

async function probe(model, useKey = key) {
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), TIMEOUT_MS);
  const t0 = Date.now();
  try {
    const res = await fetch(`${base}/chat/completions`, {
      method: 'POST',
      signal: ac.signal,
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${useKey}`,
      },
      body: JSON.stringify({
        model,
        max_tokens: 8,
        messages: [{ role: 'user', content: 'say ok' }],
      }),
    });
    const text = await res.text();
    const ms = Date.now() - t0;
    let detail = text.slice(0, 120).replace(/\s+/g, ' ');
    try {
      const j = JSON.parse(text);
      detail = j.choices?.[0]?.message?.content
        ? `content="${String(j.choices[0].message.content).slice(0, 40)}"`
        : (j.error?.message || JSON.stringify(j).slice(0, 100));
    } catch { /* 非 JSON */ }
    return { model, ok: res.ok, status: res.status, ms, detail };
  } catch (e) {
    const ms = Date.now() - t0;
    return { model, ok: false, status: 0, ms, detail: e.name === 'AbortError' ? `超时 >${TIMEOUT_MS}ms` : e.message };
  } finally {
    clearTimeout(timer);
  }
}

const rows = [];
for (const m of MODELS) {
  const r = await probe(m);
  rows.push(r);
  console.log(
    `${r.ok ? '✅' : '❌'} ${m.padEnd(28)} ${String(r.status).padEnd(4)} ${String(r.ms).padStart(6)}ms  ${r.detail}`
  );
}

console.log('\n--- 负控：故意用错密钥 ---');
const neg = await probe(MODELS[0], 'sk-definitely-not-a-real-key-000000'); // secret-scan-ok — 负控，故意用错的密钥
console.log(`${neg.ok ? '❌ 网关竟接受了错密钥' : '✅ 网关正确拒绝'} ${neg.status} ${neg.ms}ms ${neg.detail}`);

const worked = rows.filter(r => r.ok);
const timedOut = rows.filter(r => r.detail.includes('超时'));
console.log(`\n结论：${worked.length}/${rows.length} 个模型在 chat/completions 上可用，${timedOut.length} 个超时。`);
if (worked.length > 0) {
  console.log(`→ 端点是通的，之前的「需要协议适配」结论不成立；可用模型：${worked.map(r => r.model).join(', ')}`);
} else if (timedOut.length === rows.length) {
  console.log('→ 全部超时：端点级别不可用，不是模型选择问题。');
} else {
  console.log('→ 可用性随模型变化：需要在产品侧做模型选择而非协议适配。');
}
