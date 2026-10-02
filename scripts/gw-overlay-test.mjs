// 对照实验：
//  A) POST /api/llm-gateway/config  → 读回仍是旧地址（已复现）
//  B) PUT  /api/user-settings/llm_gateway/default → 读回变为新地址？
// 若 B 生效即坐实根因：user setting 覆盖层压过工作区快照。
import { readFileSync, writeFileSync } from 'node:fs'
const KEY = readFileSync('logs/.gateway-key', 'utf8').trim()
const GW = 'https://llm.kxpms.cn/v1'

const expr = `(async () => {
  const KEY = ${JSON.stringify(KEY)};
  const GW = ${JSON.stringify(GW)};
  const base = localStorage.getItem('pocket_api_base') || '';
  const token = localStorage.getItem('pocket_token') || '';
  const H = { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' };
  const out = [];
  const read = async () => (await (await fetch(base + '/api/llm-gateway/config', { headers: H })).json());

  out.push('A. 起点: ' + (await read()).baseURL);

  // B) 写 user setting 覆盖层
  const payload = {
    baseURL: GW, format: 'openai-chat',
    models: ${JSON.stringify(['glm-5.3', 'minimax-m3', 'kimi-k3', 'claude-sonnet-5', 'gpt-5.6-terra', 'claude-opus-5', 'claude-fable-5', 'gpt-5.6-sol', 'gemini-3.5-flash'])},
    preferredModels: ${JSON.stringify(['glm-5.3', 'minimax-m3', 'kimi-k3', 'claude-sonnet-5', 'gpt-5.6-terra', 'claude-opus-5', 'claude-fable-5', 'gpt-5.6-sol', 'gemini-3.5-flash'])},
  };
  const put = await fetch(base + '/api/user-settings/llm_gateway/default', {
    method: 'PUT', headers: H,
    body: JSON.stringify({ payload, secret: KEY, updatedAt: Math.floor(Date.now() / 1000) }),
  });
  const pj = await put.json().catch(() => null);
  out.push('B. PUT user-settings -> ' + put.status + ' applied=' + (pj && pj.applied));
  out.push('B. 读回: ' + (await read()).baseURL + '  ' + ((await read()).baseURL === GW ? '✅ 覆盖层生效，根因坐实' : '❌ 仍不是新地址'));

  // C) 再用设置页那条路径保存一次，验证「修了 sync 之后」是否已能覆盖旧值
  const post = await fetch(base + '/api/llm-gateway/config', {
    method: 'POST', headers: H, body: JSON.stringify({ baseURL: GW, apiKey: KEY }),
  });
  out.push('C. POST config -> ' + post.status);
  out.push('C. 读回: ' + (await read()).baseURL);
  const t = await fetch(base + '/api/llm-gateway/test', { method: 'POST', headers: H, body: '{}' });
  const tj = await t.json();
  out.push('D. 测试连接 -> ' + t.status + ' 模型数=' + (tj.models ? tj.models.length : JSON.stringify(tj).slice(0, 120)));
  return out.join('\\n');
})()`
writeFileSync('logs/audit/_gw-overlay-test.expr.mjs', expr, 'utf8')
console.error('ok')
