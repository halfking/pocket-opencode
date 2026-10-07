// 全仓普查：找「手抄模型名单」——硬编码的模型 ID 数组，以及它们与 SSOT 的漂移。
//
// ## 为什么需要它（背景）
//
// 2026-10-08 在 scripts/probe-preferred-models.mjs 上抓到过一次真事故：
// 那份手抄名单比后端 SSOT 漂了 14 个（当前首选 glm-5.3 从来没被探过），
// 而它自称的注释写着「与 preferred_models 一致」。那次修复是删副本、运行时解析。
//
// 本脚本是那次事故的**泛化版**：全仓扫同类，不只那一个文件。
// 它同时区分**两类**发现，因为它们的严重性完全不同：
//
//   A 类「SSOT 副本」——注释明说「与 X 同源 / 一致」。⇒ 副本漂移就是**已确认的债**，
//     因为那条注释会让人以为它对。要报「与 SSOT 的差集」。
//   B 类「刻意的候选集」——脚本就是要探别的模型（gpt-4o 这类）。
//     ⇒ **不是债**，是刻意的。要报，但明确标成 B 类且不判红。
//   C 类「兜底默认值」——有环境变量可覆盖，注释里带实测依据。⇒ 不判红，只登记。
//
// ## 为什么默认只打印、不判红、不接 gates
//
// 这个仓的规矩（§197 同款）：`audit:*` 不需要登记，也没有通过/不通过语义。
// 本脚本 **rc=0 恒定**，除非它自己坏了（那才是 rc=2）。
// 把它接进 gates 会逼着人给「已知的债」写豁免表，而豁免表可以谎报。
//
// 用法：
//   node scripts/audit-hardcoded-model-lists.mjs          # 打印清单
//   node scripts/audit-hardcoded-model-lists.mjs --drift  # 只看 SSOT 副本的漂移
//
// 退出码：0 = 打印完成（含发现）· 2 = 量具坏（SSOT 读不到 / 扫描面塌缩）

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO = process.env.AUDIT_LIST_ROOT || path.resolve(SCRIPT_DIR, '..');

// SSOT 锚点：必须恰好命中一次，否则量具坏（见 §198 的 M3 —— 正则太松会静默拼两份）
const GO_FILE = path.join(REPO, 'backend/internal/opencode/config_writer.go');
function readSsot() {
  if (!fs.existsSync(GO_FILE)) {
    console.error('量具坏：找不到 SSOT ' + GO_FILE);
    process.exit(2);
  }
  const src = fs.readFileSync(GO_FILE, 'utf8');
  const hits = [...src.matchAll(/var\s+DefaultLLMGatewayPreferredModels\s*=\s*\[\]string\{([^}]*)\}/g)];
  if (hits.length !== 1) {
    console.error(`量具坏：SSOT 锚点命中 ${hits.length} 次（必须恰好 1 次）`);
    process.exit(2);
  }
  const list = [...hits[0][1].matchAll(/"([^"]+)"/g)].map(m => m[1]);
  if (list.length === 0) { console.error('量具坏：SSOT 抽出 0 个'); process.exit(2); }
  return list;
}
const SSOT = readSsot();

// ---- 扫描面：只看「会拿模型名去发请求 / 写配置」的脚本目录 ----
const SCAN_DIRS = ['scripts', 'frontend/scripts'].map(d => path.join(REPO, d)).filter(d => fs.existsSync(d));
const EXTS = new Set(['.mjs', '.js', '.sh', '.ps1']);

function walk(dir, acc = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.name === 'node_modules' || e.name === 'dist' || e.name === '.git') continue;
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, acc);
    else if (EXTS.has(path.extname(e.name))) acc.push(p);
  }
  return acc;
}

// 扫描范围自证（§189 的教训：扫描面塌缩会照打 PASS）
let FILES = [];
for (const d of SCAN_DIRS) FILES = FILES.concat(walk(d));
if (FILES.length < 20) {
  console.error(`量具坏：只扫到 ${FILES.length} 个文件，扫描面疑似塌缩（下限 20）`);
  process.exit(2);
}

// 剥注释：注释里的模型名是「举例」，不是「硬编码」。Go/shell/JS 三种注释都要剥。
function stripComments(src, file) {
  const ext = path.extname(file);
  if (ext === '.sh' || ext === '.ps1') {
    return src.split('\n').filter(l => !/^\s*(#|\*)/.test(l)).join('\n');
  }
  // JS：剥 // 与 /* */（够用；本仓脚本没有正则字面量里带 // 的写法）
  return src.replace(/\/\*[\s\S]*?\*\//g, '').split('\n').map(l => l.replace(/\/\/.*$/, '')).join('\n');
}

// ⚠ 第三个量具缺陷，同一处（2026-10-08）—— 这次最隐蔽：
//   尾段改成 `(?:-[a-z0-9.]+)*` 后，`gpt-5.6-sol` 在 `gpt-5` 处就被 `[a-z0-9]+` 吃掉
//   停住，`.6` 不匹配 `-`；而 `\b` 让 `gpt-5` 成了一个合法词边界 ⇒ 抽出 `gpt-5`、
//   `glm-5`、`gemini-3`。于是 9 项的真副本被报成「缺 gpt-5.6-sol」。
//   ⇒ **调字符类是在猜模型名长什么样，而模型名的形状不是我能猜的**（带点、带连字符、
//     版本号位数都在变：glm-5.3 / gpt-5.6-sol / gemini-3.5-flash）。
//
//   正确修法：**SSOT 整名优先**。既然要回答的问题是「这些 token 里哪些是 SSOT 的成员」，
//   就先用 SSOT 的字面串去做整名匹配（`String.includes`），再拿正则去捞「不属于 SSOT 的」。
//   顺序反过来，量具就不再依赖自己那份字符类猜得对不对。
const MODEL_RE = /\b(?:glm|claude|gpt|mimo|minimax|kimi|deepseek|gemini|qwen|doubao|ernie)-[a-z0-9]+(?:-[a-z0-9.]+)*\b/gi;
const SSOT_SET = new Set(SSOT.map(s => s.toLowerCase()));

/**
 * 从剥完注释的代码正文里取模型名：
 *   1) SSOT 每个名字用字面边界匹配先取一遍（稳，不靠字符类）；
 *   2) 正则捞剩下的，只用于报「多出来的名字」（那些本来就不该在 SSOT 里）。
 */
function extractModels(body) {
  const lower = body.toLowerCase();
  const found = new Set();
  for (const s of SSOT) {
    const re = new RegExp(`(?<![a-z0-9.-])${s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?![a-z0-9.-])`, 'g');
    if (re.test(lower)) found.add(s);
  }
  for (const m of body.matchAll(MODEL_RE)) {
    const t = m[0].toLowerCase();
    if (SSOT_SET.has(t)) continue;
    // 正则的截断碎片：gpt-5 ⊂ gpt-5.6-sol、glm-5 ⊂ glm-5.3、gemini-3 ⊂ gemini-3.5-flash。
    // 它们不是「多出来的名字」，是同一次贪婪匹配的残渣 —— 剔除，否则会把
    // 一份逐字一致的副本报成漂移（我在这上面已经骗了自己两轮，见文件头三处 ⚠）。
    const isFragment = SSOT.some(s => s.startsWith(t) || t.startsWith(s));
    if (isFragment) continue;
    found.add(t);
  }
  return [...found].sort();
}

// A 类线索：注释里自称同源/一致 —— 剥注释前在**原文**里找（线索本来就在注释里）。
//
// ⚠ 这里踩过一次量具缺陷，务必看懂再改（2026-10-08）：
//   第一版用 `与\s*…[^。\n]*同源` 这类单行正则，结果 **A 类 0 个** —— 而
//   `seed_llm_gateway.sh` 的注释明明写着「与 backend/internal/opencode/config_writer.go 的
//   # DefaultLLMGatewayPreferredModels 同源」，只是**被 `#` 分成了两行**，
//   而 `[^。\n]*` 掐断了跨行匹配 ⇒ 真阳性被判成 B 类。
//   ⇒ 「A 类 0 个」在这里**不是干净，是瞎**。
//   修法：先按注释分隔符切块，块内**去掉换行**再匹配，同块内任意位置出现即可。
function claimsSsotClaim(raw, file) {
  // 按行首注释符切块：js 的 // 与 */ 内部、sh 的 #、ps1 的 # 视为注释块
  const lines = raw.split('\n');
  const isComment = (l) => {
    const t = l.trim();
    if (!t) return true;                        // 注释常被 #/// 分成多行，空行也算块内
    return t.startsWith('//') || t.startsWith('#') || t.startsWith('*') || t.startsWith('/*');
  };
  let block = [];
  const blocks = [];
  for (const l of lines) {
    if (isComment(l)) block.push(l);
    else { if (block.length) blocks.push(block.join(' ')); block = []; }
  }
  if (block.length) blocks.push(block.join(' '));
  const hay = blocks.join('\n').replace(/\s+/g, ' ');
  const NAME = '(?:backend\\/internal\\/opencode|config_writer\\.go|llm-gateway\\.ts|DefaultLLMGatewayPreferredModels|DEFAULT_GATEWAY_PREFERRED_MODELS|preferred_models|preferredModels)';
  return new RegExp('与[^。]{0,120}?' + NAME).test(hay) || new RegExp(NAME + '[^。]{0,120}?(?:同源|一致)').test(hay);
}

const findings = [];
for (const f of FILES) {
  const raw = fs.readFileSync(f, 'utf8');
  const rel = path.relative(REPO, f);
  const body = stripComments(raw, f);
  const models = extractModels(body);
  if (models.length === 0) continue;

  const claimsSsot = claimsSsotClaim(raw, f);
  const hasEnvOverride = /\bprocess\.env\.[A-Z_]*MODEL|\$env:GW_LLM_MODEL|GW_LLM_MODEL/.test(raw);
  let cls = 'B';
  if (claimsSsot) cls = 'A';
  else if (hasEnvOverride) cls = 'C';

  // 只有 A 类算漂移（且必须与 SSOT 是同一族：含 ≥2 个 SSOT 成员）
  const overlap = models.filter(m => SSOT.includes(m));
  let drift = null;
  if (cls === 'A' && overlap.length >= 2) {
    drift = {
      extra: models.filter(m => !SSOT.includes(m)),
      missing: SSOT.filter(m => !models.includes(m)),
    };
  }
  findings.push({ rel, cls, models, overlap, drift, hasEnvOverride });
}

findings.sort((a, b) => (a.cls + a.rel).localeCompare(b.cls + b.rel));

const LABEL = {
  A: 'A 类 · SSOT 副本（注释自称同源）—— 漂移即已确认的债',
  B: 'B 类 · 刻意的候选集/探测集 —— 不是债',
  C: 'C 类 · 有 env 可覆盖的兜底默认值 —— 只登记',
};

console.log(`扫描根 ${REPO}`);
console.log(`扫描 ${FILES.length} 个脚本（${SCAN_DIRS.map(d => path.relative(REPO, d)).join(' + ')}）`);
console.log(`SSOT ${SSOT.length} 个：${SSOT.join(', ')}\n`);

const onlyDrift = process.argv.includes('--drift');
let aCount = 0, driftCount = 0;

for (const g of ['A', 'B', 'C']) {
  const rows = findings.filter(f => f.cls === g);
  if (rows.length === 0) continue;
  // ⚠ --drift 会过滤掉一致项，**但表头原本按 rows.length 打印** ⇒ 输出自相矛盾：
  //   印「A 类 · … · 1 个 ---\n\n」而底下空无一物，读者无法知道那个 1 是谁、是 ok 还是漂移。
  //   修法：先算出这个组在当前模式下真要打几行，打 0 行就连表头都不印。
  const shown = onlyDrift
    ? rows.filter(r => r.cls === 'A' && r.drift && (r.drift.extra.length || r.drift.missing.length))
    : rows;
  if (shown.length === 0) continue;
  console.log(`--- ${LABEL[g]} · ${shown.length}${onlyDrift && shown.length !== rows.length ? ` / 共 ${rows.length}` : ''} 个 ---`);
  for (const r of shown) {
    const tail = r.hasEnvOverride ? ' [有 env 覆盖]' : '';
    if (r.cls === 'A' && r.drift) {
      const ok = !r.drift.extra.length && !r.drift.missing.length;
      console.log(`  ${ok ? 'ok  ' : 'DRIFT'} ${r.rel}${tail}`);
      console.log(`         命中 SSOT ${r.overlap.length}/${SSOT.length}` +
        (ok ? '（与 SSOT 一致）' : `　多: ${r.drift.extra.join(', ') || '-'}　缺: ${r.drift.missing.join(', ') || '-'}`));
      if (!ok) driftCount++;
    } else {
      aCount++;
      console.log(`  --   ${r.rel}${tail}　（${r.models.length} 个模型名，与 SSOT 重叠 ${r.overlap.length}）`);
    }
  }
  console.log('');
}

// 汇总必须区分「总数」与「本次打了几条」，否则 --drift 下「A 类 1 个」与上面空白对不上
const shownA = onlyDrift
  ? findings.filter(f => f.cls === 'A' && f.drift && (f.drift.extra.length || f.drift.missing.length)).length
  : findings.filter(f => f.cls === 'A').length;
console.log(`汇总：A 类 ${findings.filter(f => f.cls === 'A').length} 个` +
  `（其中漂移 ${driftCount}${onlyDrift ? `，本次列出 ${shownA} 条` : ''}）` +
  ` · B 类 ${findings.filter(f => f.cls === 'B').length} 个` +
  ` · C 类 ${findings.filter(f => f.cls === 'C').length} 个` +
  (onlyDrift ? '（B/C 两类在 --drift 下不列出）' : ''));
console.log(`本脚本恒 rc=0 —— 它只登记发现，不判定通过/不通过（§197 同款：audit:* 无需登记）。`);
if (driftCount) console.log(`⚠ A 类漂移 ${driftCount} 个：副本与 SSOT 已不一致，注释里的「同源/一致」是假的。`);