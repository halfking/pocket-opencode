#!/usr/bin/env node
/**
 * check-doc-ref-integrity —— 设计文档的「死引用」卡口。
 *
 * ## 它拦的是什么
 *   简报（待拍板清单）与主文档（设计正文）互相引用 `§NNN`。
 *   一旦某个 `§NNN` 在主文档里**没有实体**（从没写过，或被删了），
 *   读者按引用去找会**扑空** —— 而引用者说得言之凿凿，像真的一样。
 *   §933 把这类缺口量成了账单：简报引用的节号里有一批在主文档里查无此节。
 *
 * ## 为什么它以前不存在，以及**这次差点又被量错**（§934 的由来）
 *   §933 那次量缺口用的判据是 `^## §(\d+)` —— **只认 `## §NNN` 一种标题形态**。
 *   而主文档的标题写法其实有**三层形态**，本门禁把三层都认：
 *     · `## §NNN`（新世系）
 *     · `## NNN.`（老世系，§0…§119 那一段）
 *     · `### NNN`（**节写在三级**）与 `### NNN.M`（**只有小节标题的节**）
 *   ⇒ 判据每漏一层，就有一批「已经存在」的节被误判成「死引用」：
 *     69 → 37 → 13 → **2**（brief→main 的节号死引用），小节指针 16 → **2**。
 *   ⇒ ★ **同一个缺口被量成 34 倍的差距** ⇒ ⇒ **口径没钉死之前，计数不可信**。
 *   ⇒ ⇒ 本门禁的两条纪律由这次误判直接推出：
 *     ① 标题抽取必须**认全形态**（`--meta` 里有专门一条断言钉它）；
 *     ② 任何「计数型」结论都要先问「**这个抽取口径漏了哪一类形态**」。
 *
 * ## 判据
 *   ① **简报 → 主文档**：简报引用的每个节号，主文档必须有 `##` 级同名标题；
 *      简报的**严格小节指针**（`（§NNN.M / §NNN.M）` 那种成组枚举），
 *      主文档必须有 `###`/`####` 级同名小节标题。
 *   ② **主文档 → 主文档**：同上，但主文档自己（**剥掉代码围栏** ——
 *      围栏里是脚本输出，里面有几百个像节号的行号，见下方「§ 的第三种用法」）。
 *   ③ 已知缺口用**豁免清单**登记（`scripts/doc-ref-integrity-waivers.json`）：
 *      - 实测有、清单没有 ⇒ 🔴 **新增死引用**（必须回填，或补一条带理由的豁免）；
 *      - 清单有、实测没有 ⇒ 🔴 **豁免过期**（缺口已补 ⇒ 必须删掉这条豁免）；
 *      - 每条豁免必须有**可排查的理由**（空理由 ⇒ 登记无效）。
 *   ⇒ 清单与实测**双向严格相等** ⇒ 豁免不会变成永久藏污纳垢，缺口只能变小。
 *
 * ## 「§」在本仓有**三种**用法，本门只管其中一种
 *   1. **节号**（1–3 位）—— 本门管的；
 *   2. **行号**（4 位以上，如 `§23772` = 第 23772 行）—— 老世系正文里的习惯写法；
 *   3. **日期**（如 `§2026-10-01`）—— 文件名。
 *   ⇒ 抽取用 `§(\d{1,3})\b`：4 位以上自然不匹配（`\b` 挡住了 `§2377` 截断成 `§237`）。
 *
 * ## 量具自证（--meta）
 *   死引用这种判据最危险的失败形态是「**安静地什么都不报**」——
 *   抽取口径一坏，报出来就是「0 个问题」，和真的干净**完全同形**。
 *   ⇒ --meta 用内存样本逐条断言检测器**确实会报**、**确实不会误报**，
 *     其中一条专门钉「两套标题世系都要认」（就是 §933 栽的那一次）。
 *   ⇒ 另有**范围下限哨兵**：主文档节数 / 引用数低于下限 ⇒ 退出码 2
 *     （量具坏了 / 文件被改名，**不是**文档坏了）。
 *
 * ## 退出码
 *   0 = 干净；1 = 发现问题；2 = 判据自证失败（量具坏了，不是文档坏了）。
 *
 * ## 逃逸口（只给自证与变异用）
 *   DOC_REF_ROOT=<dir>       —— 把仓根换成别处（变异在临时副本上做，不碰真文件）
 *   DOC_REF_SCOPE_FLOOR=<n>  —— 改范围下限（用来证明哨兵是活的）
 */

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// ⚠️⚠️ 这里**不能**用 process.cwd()。
// scripts/audit-doc-encoding.mjs 头注释里记过同型事故：cwd 变了 ⇒ 扫描范围静默塌缩
// （516 个 .md 缩成 2 个）⇒ 输出照旧一行行打 PASS ⇒ 「只看了 2/516 还给出自信结论」。
// ⇒ 扫描根必须从**脚本自身位置**推导，与调用者的 cwd 无关。
const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const EXPLICIT_ROOT = process.env.DOC_REF_ROOT ? path.resolve(process.env.DOC_REF_ROOT) : null;
const ROOT = EXPLICIT_ROOT || path.resolve(SCRIPT_DIR, '..');
const SCOPE = EXPLICIT_ROOT ? 'fixture' : 'repo';

const MAIN_DOC_REL = 'docs/design/2026-10-06-recording-quality-fixes.md';
const BRIEF_DOC_REL = 'docs/design/2026-10-11-pending-decisions-brief.md';
const WAIVER_REL = 'scripts/doc-ref-integrity-waivers.json';

/**
 * 范围下限（哨兵）：不是普查，是「范围塌了，去看」的信号。
 * 实测（2026-10-20）：主文档 869 节 / 简报 461 个节号引用 / 主文档 536 个自引用。
 * 下限取 300（≈ 实测的 1/3）—— 文档只会越写越多，正常增删动不了这个量级；
 * 而口径一坏、文件一改名、路径一写错，读数会直接掉到个位数 ⇒ 哨兵立刻响。
 */
const SCOPE_FLOOR = Number(process.env.DOC_REF_SCOPE_FLOOR || 300);

/** 豁免清���的四个分组。顺序即输出顺序。 */
const GROUPS = [
  { key: 'briefToMainSections', label: '简报 → 主文档（节号）' },
  { key: 'briefToMainSubsections', label: '简报 → 主文档（严格小节指针）' },
  { key: 'mainToMainSections', label: '主文档 → 主文档（节号）' },
  { key: 'mainToMainSubsections', label: '主文档 → 主文档（严格小节指针）' },
];

// ── 抽取器 ────────────────────────────────────────────────────────────

/**
 * 剥掉围栏代码块（``` … ```）。
 *
 * ⚠️⚠️ **围栏只能按「行首」判定**，不能用 `count('```')` 配对。
 *   主文档第 64726 行**早就记过这个坑**（「量具坏第 7 次」）：
 *   正文里**提到**围栏的行也会被数进去 —— 本文件有 14 行
 *   （blockquote 里的 `> ```bash`、diff 行、行内提及）⇒ 计数是**奇数**
 *   ⇒ 按顺序两两配对时**从那里起整体错位** ⇒ 我第一版用 `replace(/```[\s\S]*?```/g,'')`
 *   剥掉的不只是围栏，还有**后面 5 万行真内容**（108997 行 → 56174 行）
 *   ⇒ 连 §919–§931 的正文都被当成围栏吃掉了 ⇒ 那一版量出来的「死引用」全是假的。
 *   ⇒ 现行规则：**只有行首（可含缩进与 `> ` 引用前缀）出现 ``` 才算围栏标记**。
 */
export function stripFences(text) {
  const out = [];
  let inside = false;
  for (const line of text.split('\n')) {
    if (FENCE_LINE.test(line)) {
      inside = !inside;
      continue;
    }
    if (!inside) out.push(line);
  }
  return out.join('\n');
}

/** 行首锚定的围栏标记（可含缩进与 `> ` 引用前缀）。 */
const FENCE_LINE = /^\s*(?:>\s*)*```/;

/** 围栏标记的个数（行首锚定口径）—— 必须为偶数，否则配对不可信。 */
export function countFenceMarkers(text) {
  let n = 0;
  for (const line of text.split('\n')) if (FENCE_LINE.test(line)) n++;
  return n;
}

/**
 * 主文档里**可定位**的节号。
 *
 * ⚠️⚠️ 这条规则是 §934 改了三轮才定下来的，每一轮都削掉一大截「死引用」，
 *    每一轮漏的都是**同一类东西**：标题的**写法**。逐轮记录（别再犯）：
 *      ① 只认 `^## §(\d+)` ⇒ 漏了老世系 `## NNN.`（372 个）⇒ 死引用 69 → 37；
 *      ② 只认 `##` 一级 ⇒ 漏了 `### 96 …` 这种**把节写在三级**的（53 个）⇒ 37 → 13；
 *      ③ 只认「首 token 恰好等于该号」⇒ 漏了**只有 `.M` 小节标题**的节（12 个：110 / 866–876）⇒ 13 → 2。
 *    ⇒ 三层盲区叠起来 ⇒ 同一个缺口被量成 **69 / 16**，真值是 **2 / 2**（差 34 倍）。
 *
 * ⇒ 现行规则：**任何层级的标题，只要「首 token 是这个号」就算该号存在**。
 *   首 token 形如 `NNN` / `NNN.` / `NNN.M` / `NNN.M.K` 都算。
 *   （读者顺着 `§NNN` 找过去，能撞上一条「关于 NNN 的标题」就算没扑空。）
 */
export function extractDefinedSections(text) {
  const out = new Set();
  for (const m of text.matchAll(/^#{1,6}[ \t]+(\S+)/gm)) {
    const tok = m[1].replace(/^§[ \t]*/, '').trim();
    const mm = /^(\d+)(?:\.\d+)*\.?$/.exec(tok);
    if (mm) out.add(Number(mm[1]));
  }
  return out;
}

/**
 * 主文档里**可定位**的小节号。
 * 规则同上但更严一档：首 token 必须以 `NNN.M` 开头
 * （`### 66.1` 与 `#### 66.1.1` 都让 `66.1` 可定位）。
 */
export function extractDefinedSubsections(text) {
  const out = new Set();
  for (const m of text.matchAll(/^#{1,6}[ \t]+(\S+)/gm)) {
    const tok = m[1].replace(/^§[ \t]*/, '').trim();
    const mm = /^(\d+)\.(\d+)/.exec(tok);
    if (mm) out.add(`${Number(mm[1])}.${Number(mm[2])}`);
  }
  return out;
}

/**
 * 文本里引用的节号。**只认 1–3 位**：
 * 4 位以上在本仓是行号（`§23772`）或日期（`§2026-10-01`），不是节引用。
 */
export function extractSectionRefs(text) {
  const out = new Set();
  for (const m of text.matchAll(/§(\d{1,3})\b/g)) out.add(Number(m[1]));
  return out;
}

/**
 * **严格小节指针**：成组枚举的 `§NNN.M`（形如 `（§919.1 / §919.2 / §919.5）`）。
 *
 * ⚠️ 为什么只认这一种形态：仓里 `§NNN.M` 还有**宽松用法**（散文里的
 * 「§845.1」=「§845 的第 1 段」），那种指向的节是**平铺**的、没有 `.M` 子标题
 * ⇒ 拿它当硬判据会造出一大批假红。严格形态是作者**显式枚举**的
 * ⇒ 指错就是真错。
 */
export function extractStrictSubPointers(text) {
  const out = new Set();
  const groups = text.matchAll(/（[ \t]*((?:§\d{1,3}\.\d+(?:[ \t]*[/、][ \t]*)?)+)[ \t]*）/g);
  for (const g of groups) {
    for (const m of g[1].matchAll(/§(\d{1,3})\.(\d+)/g)) out.add(`${Number(m[1])}.${Number(m[2])}`);
  }
  return out;
}

const setDiff = (a, b) => [...a].filter((x) => !b.has(x)).sort(cmpRef);
const cmpRef = (a, b) => {
  const [ai, aj] = String(a).split('.').map(Number);
  const [bi, bj] = String(b).split('.').map(Number);
  return ai !== bi ? ai - bi : (aj ?? 0) - (bj ?? 0);
};

/**
 * 核心分析：纯函数（不碰文件系统）⇒ --meta 能用内存样本直接调它。
 * 返回实测到的死引用与统计量。
 */
export function analyze(mainText, briefText) {
  // ⚠️ 定义与引用**都要**剥围栏：
  //   · 引用侧：围栏里是脚本输出，里面有几百个像节号的行号；
  //   · 定义侧：围栏里可能出现 `## §NNN` 这样的行（主文档有 36 行「像标题的行」），
  //     它们不是节 ⇒ 不剥就会把「围栏里的假节」当成真节（反向盲区）。
  const mainPlain = stripFences(mainText);
  const briefPlain = stripFences(briefText);
  const mainSections = extractDefinedSections(mainPlain);
  const mainSubs = extractDefinedSubsections(mainPlain);
  const briefSecRefs = extractSectionRefs(briefPlain);
  const briefPtrs = extractStrictSubPointers(briefPlain);
  const mainSecRefs = extractSectionRefs(mainPlain);
  const mainPtrs = extractStrictSubPointers(mainPlain);
  return {
    mainSections,
    mainSubs,
    dead: {
      briefToMainSections: setDiff(briefSecRefs, mainSections),
      briefToMainSubsections: setDiff(briefPtrs, mainSubs),
      mainToMainSections: setDiff(mainSecRefs, mainSections),
      mainToMainSubsections: setDiff(mainPtrs, mainSubs),
    },
    stats: {
      mainSections: mainSections.size,
      mainSubs: mainSubs.size,
      briefSecRefs: briefSecRefs.size,
      briefPtrs: briefPtrs.size,
      mainSecRefs: mainSecRefs.size,
      mainPtrs: mainPtrs.size,
      fenceMarkers: countFenceMarkers(mainText),
    },
  };
}

// ── 豁免清单比对 ──────────────────────────────────────────────────────

/**
 * 把实测死引用与豁免清单对账。
 * 返回每组的：新增（实测有、清单没有）、过期（清单有、实测没有）、缺理由、重复项。
 */
export function reconcile(dead, waivers) {
  const out = {};
  for (const g of GROUPS) {
    const measured = new Set((dead[g.key] || []).map(String));
    const list = Array.isArray(waivers[g.key]) ? waivers[g.key] : [];
    const waived = new Set();
    const noWhy = [];
    const dup = [];
    for (const item of list) {
      const ref = String(item?.ref ?? '');
      if (!ref) continue;
      if (waived.has(ref)) dup.push(ref);
      waived.add(ref);
      if (!item?.why || !String(item.why).trim()) noWhy.push(ref);
    }
    out[g.key] = {
      measured: [...measured].sort(cmpRef),
      newRefs: [...measured].filter((r) => !waived.has(r)).sort(cmpRef),
      staleRefs: [...waived].filter((r) => !measured.has(r)).sort(cmpRef),
      noWhy: noWhy.sort(cmpRef),
      dupRefs: [...new Set(dup)].sort(cmpRef),
      waivedCount: waived.size,
    };
  }
  return out;
}

/** 在源文本里找某个引用的第一处出现（给人看的上下文）。 */
export function findRefContext(text, ref) {
  const needle = `§${ref}`;
  const idx = text.indexOf(needle);
  if (idx < 0) return null;
  const line = text.slice(0, idx).split('\n').length;
  const start = text.lastIndexOf('\n', idx) + 1;
  const end = text.indexOf('\n', idx);
  const snippet = text.slice(start, end < 0 ? undefined : end).trim();
  return { line, snippet: snippet.length > 150 ? snippet.slice(0, 150) + '…' : snippet };
}

// ── 量具自证（--meta）────────────────────────────────────────────────

/**
 * 自证要回答的问题只有一个：**检测器还能不能区分「有死引用」和「没有死引用」**。
 * 每条断言都给**具体期望值**，不是「有反应」。
 */
export function runMeta() {
  const cases = [];
  const add = (name, ok, detail) => cases.push({ name, ok: !!ok, detail });

  // ① **三层盲区一次钉全** —— §933.5 就栽在这三层上（它只认 `## §NNN`）。
  //    · 老世系 `## N.`      · 节写在三级 `### N`      · 只有 `.M` 小节标题的节
  {
    const main = [
      '## 5. 老式节',        // ① 老世系
      '## §6 新式节',       // ① 新世系
      '### 7 三级写的节',    // ② 节写在三级
      '### §8.1 只有小节标题的节', // ③ 没有 §8 这个标题，但 §8.1 在
      '#### 8.1.1 更深一层',  // ③ 更深一层也算 §8.1 可定位
    ].join('\n');
    const brief = '见 §5、§6、§7、§8、§8.1。\n';
    const r = analyze(main, brief);
    add(
      '三层盲区一次钉全（老世系 / 三级节 / 只有 .M 小节的节都要算存在）',
      r.dead.briefToMainSections.length === 0 && r.dead.briefToMainSubsections.length === 0,
      `死引用应为 0，实测 节=${JSON.stringify(r.dead.briefToMainSections)} 小节=${JSON.stringify(r.dead.briefToMainSubsections)}`,
    );
  }
  // ② 老式世系的**小节**（`### 6.1`）也要认。
  {
    const main = '## 6. 老式节\n### 6.1 子\n';
    const brief = '见 §6.1。\n';
    const r = analyze(main, brief);
    add(
      '老式世系的小节（`### N.M`）要认',
      r.dead.briefToMainSubsections.length === 0,
      `实测 ${JSON.stringify(r.dead.briefToMainSubsections)}`,
    );
  }
  // ③ **负控**：干净样本必须报 0（不许误报）。
  {
    const main = '## §100 存在\n### §100.1 子\n';
    const brief = '见 §100 与 §100.1。\n';
    const r = analyze(main, brief);
    add(
      '负控：全部可解析的样本必须报 0 条',
      r.dead.briefToMainSections.length === 0 && r.dead.briefToMainSubsections.length === 0,
      `实测 ${JSON.stringify(r.dead)}`,
    );
  }
  // ④ **有牙**：缺失的节号必须被报出来，且只报它。
  {
    const main = '## §100 存在\n';
    const brief = '见 §100 与 §200。\n';
    const r = analyze(main, brief);
    add(
      '有牙：缺失的节号必须且只必须被报出来',
      r.dead.briefToMainSections.length === 1 && r.dead.briefToMainSections[0] === 200,
      `实测 ${JSON.stringify(r.dead.briefToMainSections)}（应为 [200]）`,
    );
  }
  // ⑤ 严格小节指针的有牙版本。
  {
    const main = '## §100 存在\n### §100.1 子\n';
    const brief = '见（§100.1 / §200.1）。\n';
    const r = analyze(main, brief);
    add(
      '有牙：严格小节指针缺失必须被报出来',
      r.dead.briefToMainSubsections.length === 1 && r.dead.briefToMainSubsections[0] === '200.1',
      `实测 ${JSON.stringify(r.dead.briefToMainSubsections)}（应为 ["200.1"]）`,
    );
  }
  // ⑥ **围栏里的节号不算定义**（围栏是脚本输出，里面有行号）。
  {
    const main = '## §100 存在\n```\n## §999 脚本输出里的假节\n```\n';
    const brief = '见 §999。\n';
    const r = analyze(main, brief);
    add(
      '围栏里的内容不算「已定义」（否则脚本输出会被当成节）',
      r.dead.briefToMainSections.length === 1 && r.dead.briefToMainSections[0] === 999,
      `实测 ${JSON.stringify(r.dead.briefToMainSections)}（应为 [999]）`,
    );
  }
  // ⑦ **4 位以上不是节引用**（行号 / 日期用法）。
  {
    const main = '## §100 存在\n';
    const brief = '见 §23772（行号）与 §2026-10-01（日期）与 §100。\n';
    const r = analyze(main, brief);
    add(
      '4 位以上的 `§`（行号/日期）不算节引用',
      r.dead.briefToMainSections.length === 0,
      `实测 ${JSON.stringify(r.dead.briefToMainSections)}（应为 []）`,
    );
  }
  // ⑧ **宽松小节引用不算严格指针**（散文里的 §845.1 不进严格判据）。
  {
    const main = '## §845 平铺节\n';
    const brief = '散文里提到 §845.1。\n';
    const r = analyze(main, brief);
    add(
      '宽松的小节引用不进严格判据（成组枚举才算）',
      r.dead.briefToMainSubsections.length === 0,
      `实测 ${JSON.stringify(r.dead.briefToMainSubsections)}`,
    );
  }
  // ⑨ 主文档 → 主文档方向也有牙。
  {
    const main = '## §100 存在\n\n正文里说 §300 不存在。\n';
    const brief = '见 §100。\n';
    const r = analyze(main, brief);
    add(
      '主文档自引用方向同样有牙',
      r.dead.mainToMainSections.length === 1 && r.dead.mainToMainSections[0] === 300,
      `实测 ${JSON.stringify(r.dead.mainToMainSections)}（应为 [300]）`,
    );
  }
  // ⑩ 豁免引擎：多/少/空理由，三种都要被抓。
  {
    const dead = { briefToMainSections: ['200'], briefToMainSubsections: [], mainToMainSections: [], mainToMainSubsections: [] };
    const r1 = reconcile(dead, { briefToMainSections: [{ ref: '200', why: '理由' }] });
    add('豁免引擎：清单与实测一致时零告警',
      r1.briefToMainSections.newRefs.length === 0 && r1.briefToMainSections.staleRefs.length === 0,
      JSON.stringify(r1.briefToMainSections));
    const r2 = reconcile(dead, { briefToMainSections: [] });
    add('豁免引擎：实测有、清单没有 ⇒ 报新增',
      r2.briefToMainSections.newRefs.length === 1 && r2.briefToMainSections.newRefs[0] === '200',
      JSON.stringify(r2.briefToMainSections.newRefs));
    const r3 = reconcile(dead, { briefToMainSections: [{ ref: '200', why: '理由' }, { ref: '999', why: '理由' }] });
    add('豁免引擎：清单有、实测没有 ⇒ 报过期',
      r3.briefToMainSections.staleRefs.length === 1 && r3.briefToMainSections.staleRefs[0] === '999',
      JSON.stringify(r3.briefToMainSections.staleRefs));
    const r4 = reconcile(dead, { briefToMainSections: [{ ref: '200', why: '  ' }] });
    add('豁免引擎：豁免缺理由 ⇒ 判无效',
      r4.briefToMainSections.noWhy.length === 1,
      JSON.stringify(r4.briefToMainSections.noWhy));
  }
  // ⑫ **行内提及围栏不算标记**（主文档 L64726 记过：14 行正文提到了 ```）。
  //     差一个标记 ⇒ 两两配对整体错位 ⇒ 后面 5 万行真内容会被当成围栏吃掉。
  //     ⚠ 样本里那句「行内提及」必须放在**围栏之前**：只有这样错位才会显形
  //     （若放在之后，错位只吃掉那句提及，定义集恰好不变 ⇒ 自证会假通过）。
  {
    const main = '正文里提到 ``` 这个符号。\n## §100 存在\n```\n## §900 围栏里的假节\n```\n';
    const markers = countFenceMarkers(main);
    const brief = '见 §900 与 §100。\n';
    const r = analyze(main, brief);
    add(
      '正文里「提到」``` 的行不算围栏标记（否则配对错位 ⇒ 真内容被吃）',
      markers === 2 && r.dead.briefToMainSections.length === 1 && r.dead.briefToMainSections[0] === 900,
      `标记数 ${markers}（应为 2），死引用 ${JSON.stringify(r.dead.briefToMainSections)}（应为 [900]）`,
    );
  }
  // ⑬ 范围下限哨兵：口径一坏（读到 0 节）必须响。
  {
    const ok = scopeOk({ mainSections: 869, briefSecRefs: 461, mainSecRefs: 536 }, 300);
    const bad = scopeOk({ mainSections: 2, briefSecRefs: 1, mainSecRefs: 1 }, 300);
    add('范围下限哨兵：读到几乎空的内容必须判「量具坏了」',
      ok === null && bad !== null,
      `ok=${JSON.stringify(ok)} bad=${JSON.stringify(bad)}`);
  }
  return cases;
}

/** 范围哨兵：低于下限 ⇒ 返回原因字符串；正常 ⇒ null。 */
export function scopeOk(stats, floor) {
  const bad = [];
  if (stats.mainSections < floor) bad.push(`主文档节数 ${stats.mainSections} < 下限 ${floor}`);
  if (stats.briefSecRefs < floor) bad.push(`简报节号引用数 ${stats.briefSecRefs} < 下限 ${floor}`);
  if (stats.mainSecRefs < floor) bad.push(`主文档自引用节号数 ${stats.mainSecRefs} < 下限 ${floor}`);
  return bad.length ? bad.join('；') : null;
}

// ── 主流程 ────────────────────────────────────────────────────────────

function readOrFail(rel) {
  const p = path.join(ROOT, rel);
  if (!fs.existsSync(p)) {
    console.error(`[doc-ref] 找不到文件：${rel}（相对根 ${ROOT}）`);
    process.exit(2);
  }
  return fs.readFileSync(p, 'utf-8');
}

function main() {
  const args = process.argv.slice(2);

  // ── --meta：量具自证 ──
  if (args.includes('--meta')) {
    const cases = runMeta();
    const failed = cases.filter((c) => !c.ok);
    console.log('【死引用卡口·量具自证】检测器还能不能区分「有死引用」与「没有死引用」：');
    for (const c of cases) console.log(`  ${c.ok ? '✅' : '❌'} ${c.name}${c.ok ? '' : ` —— ${c.detail}`}`);
    if (failed.length) {
      console.error(`\n❌ 量具自证失败 ${failed.length}/${cases.length} 条 ⇒ 判据坏了（不是文档坏了），退出码 2`);
      process.exit(2);
    }
    console.log(`\n✅ 量具自证 ${cases.length}/${cases.length} 全通过`);
    if (args.length === 1) return; // 只跑 --meta
  }

  const mainText = readOrFail(MAIN_DOC_REL);
  const briefText = readOrFail(BRIEF_DOC_REL);
  const waiverPath = path.join(ROOT, WAIVER_REL);
  if (!fs.existsSync(waiverPath)) {
    console.error(`[doc-ref] 找不到豁免清单：${WAIVER_REL}`);
    process.exit(2);
  }
  let waiverDoc;
  try {
    waiverDoc = JSON.parse(fs.readFileSync(waiverPath, 'utf-8'));
  } catch (e) {
    console.error(`[doc-ref] 豁免清单不是合法 JSON：${e.message}`);
    process.exit(2);
  }

  const r = analyze(mainText, briefText);
  const scopeProblem = scopeOk(r.stats, SCOPE_FLOOR);
  if (scopeProblem) {
    console.error(`[doc-ref] ⚠️ 扫描范围疑似塌缩：${scopeProblem}`);
    console.error('[doc-ref] ⇒ 判据没真正读到内容（量具坏了 / 文件被改名），退出码 2');
    process.exit(2);
  }
  if (r.stats.fenceMarkers % 2 !== 0) {
    console.error(`[doc-ref] ⚠️ 主文档的代码围栏标记数为奇数（${r.stats.fenceMarkers}，行首锚定口径）⇒ 剥围栏的结果不可信，退出码 2`);
    process.exit(2);
  }

  const rec = reconcile(r.dead, waiverDoc.waivers || {});
  const pinned = waiverDoc.pinned || {};

  console.log(`【死引用卡口】${MAIN_DOC_REL} ↔ ${BRIEF_DOC_REL}（模式=${SCOPE}）`);
  console.log(`  主文档：节 ${r.stats.mainSections} 个（## 级，两套标题世系并集）/ 小节 ${r.stats.mainSubs} 个（###·#### 级）`);
  console.log(`  引用：简报 节号 ${r.stats.briefSecRefs} 个 / 严格小节指针 ${r.stats.briefPtrs} 个`);
  console.log(`        主文档自引用 节号 ${r.stats.mainSecRefs} 个 / 严格小节指针 ${r.stats.mainPtrs} 个（已剥代码围栏 ${r.stats.fenceMarkers} 个标记）`);

  let bad = 0;
  for (const g of GROUPS) {
    const x = rec[g.key];
    const pin = pinned[g.key];
    const pinOk = pin === x.measured.length;
    const label = `${g.label}：死 ${x.measured.length} 条 / 豁免 ${x.waivedCount} 条`;
    const issues = [];
    if (x.newRefs.length) issues.push(`🔴 新增死引用 ${x.newRefs.length} 个（清单里没有）：${x.newRefs.map((r2) => '§' + r2).join('、')}`);
    if (x.staleRefs.length) issues.push(`🔴 豁免过期 ${x.staleRefs.length} 条（缺口已补 ⇒ 必须删掉）：${x.staleRefs.map((r2) => '§' + r2).join('、')}`);
    if (x.noWhy.length) issues.push(`🔴 豁免缺可排查理由 ${x.noWhy.length} 条：${x.noWhy.map((r2) => '§' + r2).join('、')}`);
    if (x.dupRefs.length) issues.push(`🔴 豁免清单里有重复项：${x.dupRefs.map((r2) => '§' + r2).join('、')}`);
    if (!pinOk) issues.push(`🔴 钉住的条数 ${pin} ≠ 实测 ${x.measured.length} ⇒ 清单必须与实测同步更新`);
    if (issues.length) {
      bad += issues.length;
      console.log(`  ❌ ${label}`);
      for (const i of issues) console.log(`       ${i}`);
      for (const ref of [...x.newRefs, ...x.staleRefs]) {
        const src = g.key.startsWith('brief') ? briefText : mainText;
        const c = findRefContext(src, ref);
        if (c) console.log(`         §${ref} 首次出现在 ${g.key.startsWith('brief') ? BRIEF_DOC_REL : MAIN_DOC_REL}:${c.line} —— ${c.snippet}`);
      }
    } else {
      console.log(`  ✅ ${label}（全部已登记）`);
    }
  }

  if (bad) {
    console.error(`\n❌ 死引用卡口未通过：${bad} 项 ⇒ 要么回填，要么在 ${WAIVER_REL} 里补一条**带理由**的豁免`);
    process.exit(1);
  }
  const totalWaived = GROUPS.reduce((n, g) => n + rec[g.key].waivedCount, 0);
  console.log(`\n✅ 死引用卡口通过：${totalWaived} 条已知缺口全部登记在案（${WAIVER_REL}），无新增、无过期。`);
}

main();
