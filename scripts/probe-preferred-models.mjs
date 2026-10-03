// 拿应用真实的首选模型清单去打 /v1/chat/completions。
//
// 为什么单独一个脚本：probe-chat-endpoint-by-model.mjs 用的是「大家熟悉的模型名」
// （gpt-4o 等），全绿；但应用实际发的是网关自己的模型清单（gpt-5.6 / glm-5.2 /
// minimax-m3 …）。两者不是一回事，应用的超时很可能来自后者。
//
// 用法：
//   $env:GATEWAY_KEY='...'
//   node scripts/probe-preferred-models.mjs

const base = process.env.GATEWAY_BASE || 'https://llm.kxpms.cn/v1';
const key = process.env.GATEWAY_KEY;
if (!key) { console.error('需要 GATEWAY_KEY'); process.exit(2); }

// 与 opencode_pocket.llm_gateway_configs.preferred_models 一致
const PREFERRED = [
  'claude-fable-5', 'claude-opus-4-8', 'claude-sonnet-4-6', 'claude-sonnet-5',
  'gpt-5.6', 'gpt-5.5', 'gpt-5.4', 'glm-5.2', 'minimax-m3', 'deepseek-v4-pro', 'mimo-v2.5-pro',
];

const TIMEOUT_MS = 25000;

async function probe(model) {
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), TIMEOUT_MS);
  const t0 = Date.now();
  try {
    const res = await fetch(`${base}/chat/completions`, {
      method: 'POST', signal: ac.signal,
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${key}` },
      body: JSON.stringify({ model, max_tokens: 8, messages: [{ role: 'user', content: 'say ok' }] }),
    });
    const text = await res.text();
    let detail = '';
    try {
      const j = JSON.parse(text);
      detail = j.choices?.[0]?.message?.content
        ? `content="${String(j.choices[0].message.content).slice(0, 30)}"`
        : (j.error?.message || JSON.stringify(j).slice(0, 90));
    } catch { detail = text.slice(0, 90).replace(/\s+/g, ' '); }
    return { model, ok: res.ok, status: res.status, ms: Date.now() - t0, detail };
  } catch (e) {
    return { model, ok: false, status: 0, ms: Date.now() - t0,
      detail: e.name === 'AbortError' ? `超时 >${TIMEOUT_MS}ms（0 字节）` : e.message };
  } finally { clearTimeout(timer); }
}

const rows = [];
for (const m of PREFERRED) {
  const r = await probe(m);
  rows.push(r);
  console.log(`${r.ok ? '✅' : '❌'} ${m.padEnd(20)} ${String(r.status).padEnd(4)} ${String(r.ms).padStart(6)}ms  ${r.detail}`);
}

const ok = rows.filter(r => r.ok);
const to = rows.filter(r => r.detail.includes('超时'));
console.log(`\n可用 ${ok.length}/${rows.length}，超时 ${to.length} 个`);
if (to.length) console.log(`超时的模型：${to.map(r => r.model).join(', ')}`);
if (ok.length) console.log(`可用的模型：${ok.map(r => r.model).join(', ')}`);
