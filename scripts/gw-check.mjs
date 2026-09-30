// 网关连通性验证：非流式 + 流式（SSE）
// 用法：node scripts/gw-check.mjs <baseUrl> <apiKey>
const base = process.argv[2].replace(/\/$/, '');
const key = process.argv[3];

const H = { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json' };

const models = await (await fetch(`${base}/models`, { headers: { Authorization: `Bearer ${key}` } })).json();
const ids = (models.data || []).map((m) => m.id);
console.log(`模型数: ${ids.length}`);
console.log(`前 12 个: ${ids.slice(0, 12).join(', ')}`);

const pick = ['gpt-4o-mini', 'gpt-4o', 'claude-3-5-sonnet', 'gpt-4.1-mini', 'deepseek-chat', 'qwen-max']
  .find((c) => ids.includes(c)) || ids[0];
console.log(`选用模型: ${pick}`);

const t0 = Date.now();
const r = await fetch(`${base}/chat/completions`, {
  method: 'POST', headers: H,
  body: JSON.stringify({ model: pick, messages: [{ role: 'user', content: '用一句话回答：1+1等于几？' }], max_tokens: 60, stream: false }),
});
const j = await r.json();
console.log(`非流式: HTTP ${r.status} 用时 ${Date.now() - t0}ms`);
if (r.status !== 200) {
  console.log(`错误体: ${JSON.stringify(j).slice(0, 500)}`);
} else {
  console.log(`回复: ${JSON.stringify(j.choices?.[0]?.message?.content)}`);
  console.log(`用量: ${JSON.stringify(j.usage)}`);
}

console.log('--- 流式 ---');
const t1 = Date.now();
const res = await fetch(`${base}/chat/completions`, {
  method: 'POST', headers: H,
  body: JSON.stringify({ model: pick, messages: [{ role: 'user', content: '数到十' }], max_tokens: 80, stream: true }),
});
console.log(`流式: HTTP ${res.status} content-type=${res.headers.get('content-type')}`);
if (res.status !== 200) {
  console.log(`错误体: ${(await res.text()).slice(0, 500)}`);
} else {
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  let chunks = 0;
  let text = '';
  let firstAt = 0;
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), 25000);
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!firstAt) firstAt = Date.now() - t1;
      chunks++;
      buf += dec.decode(value, { stream: true });
      const lines = buf.split('\n');
      buf = lines.pop();
      for (const ln of lines) {
        if (!ln.startsWith('data:')) continue;
        const payload = ln.slice(5).trim();
        if (payload === '[DONE]') continue;
        try {
          const o = JSON.parse(payload);
          const d = o.choices?.[0]?.delta?.content;
          if (d) text += d;
        } catch { /* 非 JSON 行忽略 */ }
      }
    }
  } catch (e) {
    console.log(`流式读取中断: ${e.name} ${e.message}`);
  }
  clearTimeout(timer);
  console.log(`首字节 ${firstAt}ms，总耗时 ${Date.now() - t1}ms，SSE 行数 ${chunks}`);
  console.log(`拼接结果: ${JSON.stringify(text)}`);
}
