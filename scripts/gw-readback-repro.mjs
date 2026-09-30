// 复现「保存后立即读回拿到旧 baseURL」：交替写入两个 URL，各读一次
import { readFileSync, writeFileSync } from 'node:fs'
const KEY = readFileSync('logs/.gateway-key', 'utf8').trim()
const A = 'https://llm.kxpms.cn/v1'
const B = 'https://llmgo.kxpms.cn/v1'

const expr = `(async () => {
  const KEY = ${JSON.stringify(KEY)};
  const A = ${JSON.stringify(A)}, B = ${JSON.stringify(B)};
  const base = localStorage.getItem('pocket_api_base') || '';
  const token = localStorage.getItem('pocket_token') || '';
  const H = { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' };
  const out = [];
  const set = async (u, tag) => {
    const r = await fetch(base + '/api/llm-gateway/config', {
      method: 'POST', headers: H, body: JSON.stringify({ baseURL: u, apiKey: KEY }),
    });
    const g1 = await (await fetch(base + '/api/llm-gateway/config', { headers: H })).json();
    out.push(tag + ' 写入=' + u + '  POST=' + r.status + '  立即读回=' + g1.baseURL
      + '  ' + (g1.baseURL === u ? 'OK' : '❌ 读回不一致'));
    return g1;
  };
  await set(B, '第1步');
  await sleep(300);
  await set(A, '第2步');
  await sleep(300);
  await set(A, '第3步(重复写同一个)');
  const fin = await (await fetch(base + '/api/llm-gateway/config', { headers: H })).json();
  out.push('最终 baseURL=' + fin.baseURL + ' apiKeySet=' + fin.apiKeySet);
  return out.join('\\n');
  function sleep(ms){ return new Promise(r=>setTimeout(r,ms)); }
})()`
writeFileSync('logs/audit/_gw-readback.expr.mjs', expr, 'utf8')
console.error('ok')
