// 拿应用真实的首选模型清单去打 /v1/chat/completions。
//
// 为什么单独一个脚本：probe-chat-endpoint-by-model.mjs 用的是「大家熟悉的模型名」
// （gpt-4o 等），全绿；但应用实际发的是网关自己的模型清单（gpt-5.6 / glm-5.2 /
// minimax-m3 …）。两者不是一回事，应用的超时很可能来自后者。
//
// ## 名单不再手抄（2026-10-08）
//
// 原来这里是一份手写副本，注释还写着「与 opencode_pocket.llm_gateway_configs.
// preferred_models 一致」——那句话当时就已经是假的。实测（2026-10-08 20:3x，机器
// 抽三个文件的数组体对拍，不靠肉眼）：
//
//	Go 后端  config_writer.go  (9)  glm-5.3, minimax-m3, kimi-k3, claude-sonnet-5, …
//	TS 前端  llm-gateway.ts    (9)  同上，逐个一致
//	本脚本（旧）              (11)  claude-fable-5, claude-opus-4-8, claude-sonnet-4-6, …
//
//	探针多出 8 个（含 glm-5.2、mimo-v2.5-pro）· 探针缺 6 个（含当时的首选 glm-5.3）
//	合计漂移 14 个。
//
// 漂移的时间线解释它：脚本最后一次改动停在 2026-10-01，而后端常量在 10-03 又改过
// （glm-5.2 → glm-5.3）。手抄副本没有任何机制跟上，所以「一致」是靠人记着的。
//
// 现在名单直接从后端 SSOT 解析。手抄副本能漂移，就不要再留一份。
// 注意 SSOT 是**后端 seed 默认值**；用户若在设置页改过，实际发的以 DB 为准 ——
// 那时用 PREFERRED_OVERRIDE 传逗号分隔的名单覆盖。
//
// 用法：
//   node scripts/probe-preferred-models.mjs --list      # 不需要 key，只核对名单
//   GATEWAY_KEY='...' node scripts/probe-preferred-models.mjs
//   GATEWAY_KEY='...' PREFERRED_OVERRIDE='a,b' node scripts/probe-preferred-models.mjs
//
// 退出码：0 通过 / 1 对象坏（Go 与 TS 两份副本不一致）/ 2 量具坏（锚点没命中）

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
// 逃生口：变异副本放别处时用它把根指回真仓库，否则 __dirname 派生本身就是第二个变异。
const REPO = process.env.PREFERRED_LIST_ROOT || path.resolve(SCRIPT_DIR, '..');
const GO_FILE = path.join(REPO, 'backend/internal/opencode/config_writer.go');
const TS_FILE = path.join(REPO, 'frontend/src/constants/llm-gateway.ts');

const base = process.env.GATEWAY_BASE || 'https://llm.kxpms.cn/v1';

/**
 * 从源码里抽一个字符串数组。锚点必须恰好命中一次 —— 命中 0 次说明文件结构变了
 * （= 量具坏，退出码 2），命中 2 次说明正则太松，会静默拼出两份名单。
 */
function extractArray(file, re, label) {
  if (!fs.existsSync(file)) {
    console.error(`量具坏：找不到 ${label} 源文件 ${file}`);
    process.exit(2);
  }
  const src = fs.readFileSync(file, 'utf8');
  const hits = [...src.matchAll(new RegExp(re.source, re.flags.includes('g') ? re.flags : re.flags + 'g'))];
  if (hits.length !== 1) {
    console.error(`量具坏：${label} 的锚点在 ${path.basename(file)} 里命中 ${hits.length} 次（必须恰好 1 次）`);
    process.exit(2);
  }
  const list = [...hits[0][1].matchAll(/["']([^"']+)["']/g)].map(m => m[1]);
  if (list.length === 0) {
    console.error(`量具坏：${label} 抽出 0 个模型名（数组体形状变了？）`);
    process.exit(2);
  }
  return list;
}

const GO_LIST = extractArray(GO_FILE,
  /var\s+DefaultLLMGatewayPreferredModels\s*=\s*\[\]string\{([^}]*)\}/,
  'Go 后端 DefaultLLMGatewayPreferredModels');
const TS_LIST = extractArray(TS_FILE,
  /DEFAULT_GATEWAY_PREFERRED_MODELS[^=]*=\s*\[([\s\S]*?)\]/,
  'TS 前端 DEFAULT_GATEWAY_PREFERRED_MODELS');

// 后端 seed 与前端兜底预填是两份真副本，会互相漂移。漂了就直接红，别拿着
// 一个「到底该发哪份」都说不清的世界去出探测报告。
const goOnly = GO_LIST.filter(m => !TS_LIST.includes(m));
const tsOnly = TS_LIST.filter(m => !GO_LIST.includes(m));
if (goOnly.length || tsOnly.length) {
  console.error(`❌ Go 与 TS 两份 preferredModels 副本不一致（${goOnly.length + tsOnly.length} 个差异）`);
  if (goOnly.length) console.error(`   只有 Go 有: ${goOnly.join(', ')}`);
  if (tsOnly.length) console.error(`   只有 TS 有: ${tsOnly.join(', ')}`);
  console.error('   先对齐这两份再探测 —— 现在发出去的到底是哪份说不准，报告不可解释。');
  process.exit(1);
}

const overridden = !!process.env.PREFERRED_OVERRIDE;
const PREFERRED = overridden
  ? process.env.PREFERRED_OVERRIDE.split(',').map(s => s.trim()).filter(Boolean)
  : GO_LIST;
const srcLabel = overridden ? 'PREFERRED_OVERRIDE（环境变量覆盖，≠ DB 实际值）' : '后端 SSOT 现场解析';

console.log(`名单来源：${srcLabel}（${PREFERRED.length} 个）`);
console.log(`          ${PREFERRED.join(', ')}\n`);

if (process.argv.includes('--list')) {
  console.log('--list：只核对名单，不发请求。Go 与 TS 两份副本一致，解析成功。');
  process.exit(0);
}

const key = process.env.GATEWAY_KEY;
if (!key) {
  console.error('需要 GATEWAY_KEY（只看名单用 --list）');
  process.exit(2);
}

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
