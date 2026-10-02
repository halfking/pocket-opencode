// 决定性验证 syncGatewayUserSetting 修复：
//  1) 先用 PUT user-settings 把覆盖层写成「旧域名」，复现 bug 状态
//  2) 再走设置页那条路径 POST /api/llm-gateway/config 写入新域名
//  3) 读回：修复前必然仍是旧域名，修复后必须变成新域名
import { readFileSync, writeFileSync } from 'node:fs'
const KEY = readFileSync('logs/.gateway-key', 'utf8').trim()
const NEW = 'https://llm.kxpms.cn/v1'
const OLD = 'https://llmgo.kxpms.cn/v1'
const MODELS = ['glm-5.3', 'minimax-m3', 'kimi-k3', 'claude-sonnet-5', 'gpt-5.6-terra',
  'claude-opus-5', 'claude-fable-5', 'gpt-5.6-sol', 'gemini-3.5-flash']

const expr = `(async () => {
  const KEY = ${JSON.stringify(KEY)};
  const NEW = ${JSON.stringify(NEW)}, OLD = ${JSON.stringify(OLD)};
  const MODELS = ${JSON.stringify(MODELS)};
  const base = localStorage.getItem('pocket_api_base') || '';
  const token = localStorage.getItem('pocket_token') || '';
  const H = { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' };
  const out = [];
  const read = async () => (await (await fetch(base + '/api/llm-gateway/config', { headers: H })).json());
  const putOverlay = async (u) => {
    const r = await fetch(base + '/api/user-settings/llm_gateway/default', {
      method: 'PUT', headers: H,
      body: JSON.stringify({
        payload: { baseURL: u, format: 'openai-chat', models: MODELS, preferredModels: MODELS },
        secret: KEY, updatedAt: Math.floor(Date.now() / 1000),
      }),
    });
    return r.status;
  };

  // 1) 人为把覆盖层打回旧域名，复现「保存不生效」的前置状态
  out.push('步骤1 PUT 覆盖层为旧域名 -> ' + await putOverlay(OLD));
  out.push('步骤1 读回 = ' + (await read()).baseURL + (await read()).baseURL === OLD ? '  ✅ 已复现 bug 状态' : '');

  // 2) 走设置页真实保存路径
  const post = await fetch(base + '/api/llm-gateway/config', {
    method: 'POST', headers: H, body: JSON.stringify({ baseURL: NEW, apiKey: KEY }),
  });
  out.push('步骤2 POST /api/llm-gateway/config -> ' + post.status);

  // 3) 读回判定
  const after = (await read()).baseURL;
  out.push('步骤3 读回 = ' + after);
  out.push(after === NEW ? '✅ 修复生效：设置页保存后读回一致' : '❌ 修复未生效：读回仍是旧地址');

  const t = await fetch(base + '/api/llm-gateway/test', { method: 'POST', headers: H, body: '{}' });
  const tj = await t.json();
  out.push('步骤4 测试连接 -> ' + t.status + ' 模型数=' + (tj.models ? tj.models.length : JSON.stringify(tj).slice(0, 120)));
  return out.join('\\n');
})()`
writeFileSync('logs/audit/_gw-sync-verify.expr.mjs', expr, 'utf8')
console.error('ok')
