// 逐个验证「默认常用模型」是否真的可用（直连网关，隔离是网关挂还是应用挂）
import { readFileSync } from 'node:fs'

const GW = 'https://llm.kxpms.cn/v1';
const KEY = process.env.POCKET_GW_KEY || readFileSync('logs/.gateway-key', 'utf8').trim();
const MODELS = (process.env.GW_MODELS || [
  // 与 backend/internal/opencode/config_writer.go 的
  // DefaultLLMGatewayPreferredModels 同源（用户 2026-09-30 指定）
  'glm-5.2', 'minimax-m3', 'kimi-k3', 'claude-sonnet-5', 'gpt-5.6-terra',
  'claude-opus-5', 'claude-fable-5', 'gpt-5.6-sol', 'gemini-3.5-flash',
  'gpt-4o-mini',
].join(',')).split(',').map((s) => s.trim()).filter(Boolean);

const TIMEOUT = Number(process.env.GW_TIMEOUT || 45000);
const catalog = new Set(
  (await (await fetch(`${GW}/models`, { headers: { Authorization: `Bearer ${KEY}` } })).json())
    .data.map((m) => m.id),
);
console.log(`目录共 ${catalog.size} 个模型；逐个实测（单模型超时 ${TIMEOUT}ms）\n`);

const rows = [];
for (const m of MODELS) {
  if (!catalog.has(m)) { rows.push({ m, r: 'NOT_IN_CATALOG' }); console.log(`${m.padEnd(18)} 不在目录`); continue; }
  const t0 = Date.now();
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), TIMEOUT);
  let verdict = 'OK', first = 0, text = '', err = '';
  try {
    const res = await fetch(`${GW}/chat/completions`, {
      method: 'POST', signal: ctl.signal,
      headers: { Authorization: `Bearer ${KEY}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: m, messages: [{ role: 'user', content: '回复：好' }], max_tokens: 16, stream: true }),
    });
    if (res.status !== 200) {
      verdict = `HTTP_${res.status}`;
      err = (await res.text()).slice(0, 120);
    } else {
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        if (!first) first = Date.now() - t0;
        for (const ln of dec.decode(value, { stream: true }).split('\n')) {
          const s = ln.trim();
          if (!s.startsWith('data:')) continue;
          const p = s.slice(5).trim();
          if (p === '[DONE]') continue;
          try { text += JSON.parse(p).choices?.[0]?.delta?.content || ''; } catch { /* ignore */ }
        }
      }
      if (!text.trim()) { verdict = 'EMPTY_STREAM'; err = '200 但 0 个内容 delta'; }
    }
  } catch (e) {
    verdict = e.name === 'AbortError' ? 'TIMEOUT' : 'ERR';
    err = e.message;
  }
  clearTimeout(timer);
  const ms = Date.now() - t0;
  rows.push({ m, r: verdict, ms, first, text: text.slice(0, 20) });
  const flag = verdict === 'OK' ? '✅' : '❌';
  console.log(`${flag} ${m.padEnd(18)} ${String(ms).padStart(6)}ms  ${verdict}  ${JSON.stringify(text.slice(0, 20))} ${err}`);
}

const ok = rows.filter((r) => r.r === 'OK').map((r) => r.m);
const bad = rows.filter((r) => r.r !== 'OK');
console.log(`\n可用 ${ok.length}/${rows.length}`);
if (ok.length) console.log(`可用: ${ok.join(', ')}`);
if (bad.length) console.log(`不可用: ${bad.map((b) => `${b.m}(${b.r})`).join(', ')}`);
console.log(`\n建议默认模型 = ${ok[0] || '（无可用）'}`);
