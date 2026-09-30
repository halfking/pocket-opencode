// 用设备自身的 token 写入网关配置（等价于用户在「设置 → AI 网关」里保存）
// 必须在设备侧执行：不同 token 带不同 workspace_id，PC 登录拿到的是
// ws_user-admin，而 App 实际用的是 default，改错行等于没改。
import { readFileSync } from 'node:fs'

const KEY = readFileSync('logs/.gateway-key', 'utf8').trim();
const GW = 'https://llm.kxpms.cn/v1';
const body = JSON.stringify({ baseURL: GW, apiKey: KEY });

const expr = `(async () => {
  const KEY = ${JSON.stringify(KEY)};
  const GW = ${JSON.stringify(GW)};
  const base = localStorage.getItem('pocket_api_base') || '';
  const token = localStorage.getItem('pocket_token') || localStorage.getItem('token') || '';
  const H = { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' };
  const out = [];
  const before = await (await fetch(base + '/api/llm-gateway/config', { headers: H })).json();
  out.push('改前 baseURL=' + before.baseURL + ' apiKeySet=' + before.apiKeySet);
  const r = await fetch(base + '/api/llm-gateway/config', {
    method: 'POST', headers: H, body: JSON.stringify({ baseURL: GW, apiKey: KEY }),
  });
  out.push('POST -> ' + r.status);
  const after = await (await fetch(base + '/api/llm-gateway/config', { headers: H })).json();
  out.push('改后 baseURL=' + after.baseURL + ' apiKeySet=' + after.apiKeySet + ' models=' + after.models.length);
  const t = await fetch(base + '/api/llm-gateway/test', { method: 'POST', headers: H, body: '{}' });
  const tj = await t.json();
  out.push('test -> ' + t.status + ' models=' + (tj.models ? tj.models.length : JSON.stringify(tj).slice(0,200)));
  return out.join('\\n');
})()`;

// 表达式落盘，交给 cdp.mjs eval-file 执行（避免把长表达式塞进 PowerShell 参数）
import { writeFileSync } from 'node:fs'
writeFileSync('logs/audit/_gw-config-on-device.expr.mjs', expr, 'utf8');
console.error('已写入 logs/audit/_gw-config-on-device.expr.mjs (' + expr.length + ' bytes)');
