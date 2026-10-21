#!/usr/bin/env node
// audit-doc-encoding —— Markdown 文档的编码/结构卫生闸。
//
// 为什么需要它（不是凭感觉，是被坑出来的）：
//   本轮 handoff 第 1872 行出现 2 个 U+FFFD、§4.27 前出现 1 个 U+FEFF，
//   §5 标题被写成 3 份重复；其中 U+FEFF 和「2 份重复的 §5」**在 HEAD 里就已经存在**
//   （是上一轮用 PowerShell Out-File 追加时带进去的）。
//   也就是说这不是偶发，是**反复发生**的写入事故，所以留个闸而不是改完就算。
//
// 检测三类：
//   1. U+FFFD（替换字符）—— 写入时被截断的多字节字符，文本已损坏，内容不可信
//   2. U+FEFF（零宽不换行空格 / BOM）—— 文件中间的 BOM，Markdown 里会渲染成游离不可见字符
//   3. 相邻重复的标题行 —— 追加时被写了两遍的章节标题
//
// ⚠️ U+FFFD 有**豁免栏**（见 WAIVERS），因为其中一部分是**有意保留的证据**。
// 这道闸在加豁免之前是**长期红**的（23 处 / 516 文件 / EXIT=1），
// 而一道永远红的闸等于没有闸 —— 久而久之没人看它，它新增缺陷也照样放过。
// 加豁免不是放宽标准，是把「判据」与「已知例外」分开：
//   · 豁免项必须带**可校验的锚点**与**可排查的理由**，理由为空即失败；
//   · 锚点消失（= 已修好）⇒ 登记过期 ⇒ 必须删掉它（stale 检查，防豁免变成永久藏污纳垢）；
//   · 任何**新**的 U+FFFD 仍然立刻报红。
//
// 判据自证（--meta）：对每一类都在内存里造一个带该缺陷的样本，
// 确认检测器**确实会报**；同时确认干净样本**不会误报**。
// 检测器若失去区分能力，--meta 直接失败 —— 而不是安静地放行。
//
// 退出码：0 = 干净；1 = 发现问题；2 = 判据自证失败（检测器坏了，不是文档坏了）。

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execSync } from 'node:child_process';

// ⚠️⚠️ 这里**不能**用 process.cwd()。
// 2026-10-07 实测事故：`npm run check:doc-encoding` 的 cwd 是 frontend/，
// 于是扫描根变成 openpocket/frontend ⇒ 扫到的 .md 从 516 个**静默缩成 2 个**
// （只剩 I18N_README.md / I18N_USAGE.md），而输出照旧一行行打 PASS ——
// 「只看了 2/516 还给出自信结论」是这道闸最坏的失败形态。
// 手工从仓根跑永远是绿的，所以这个缺陷在接线之前**不可能**被看见。
//   · 扫描根必须从**脚本自身位置**推导，与调用者的 cwd 无关；
//   · 唯一的逃生口是 AUDIT_DOC_ROOT（变异脚本要在临时目录里隔离，见 --help 注释），
//     且一旦使用它，输出会显式标注 模式=fixture，不许假装自己扫全了。
const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const EXPLICIT_ROOT = process.env.AUDIT_DOC_ROOT ? path.resolve(process.env.AUDIT_DOC_ROOT) : null;
const ROOT = EXPLICIT_ROOT || path.resolve(SCRIPT_DIR, '..');
const SCOPE = EXPLICIT_ROOT ? 'fixture' : 'repo';

// 范围塌缩的**下限**（只作哨兵，不作普查）：2026-10-07 实测 516 个 .md（`node scripts/audit-doc-encoding.mjs`）。
// 取 50 是留 10 倍余量 —— 判据不依赖这个数，塌缩到两位数才报，报的是「范围塌了，去看」而不是「文档坏了」。
const REPO_SCOPE_FLOOR = 50;

/**
 * **有意保留的 U+FFFD 豁免**。
 *
 * ⚠️ 为什么用**锚点文本**而不是行号：这些文档仍被并发会话追加，行号随写入漂移。
 * 本轮实测同一处 U+FFFD，三方给出行号全不同（L23424 / L23429 / L23440）。
 * ⇒ **行号是坐标不是身份**；登记必须按内容锚定。
 *
 * ⚠️ 为什么这些可以豁免：
 *   · `kind: 'reference'` —— 被引用的那行**本身就是损坏的**（引的是 `build-mobile.mjs:154`
 *     与 `errors.go:173` 的注释原文）。我们**根本不知道原文是哪个字** ——
 *     填任何字都是编造。§162.3 为此专门登记了「恰好 4 个、且都在这两处」的规则，
 *     这道闸此前没读那条规则，于是把它当成新损坏 —— 那正是加豁免的直接原因。
 *   · `kind: 'self-referential'` —— 文档在**讲这件事本身**（正则里的字面量、引用的报错原文）。
 *   · `kind: 'unknown-original'` —— 损坏是真的，但原字不可知；留作证据，不得臆造填补。
 *
 * 每条必须给出 count（锚点所在行的 U+FFFD 个数）与 why（可排查的理由）。
 * 理由为空 ⇒ 登记无效。锚点消失 ⇒ 登记过期（stale）⇒ 报红并要求删除。
 */
const WAIVERS = [
  {
    file: 'docs/design/2026-10-06-recording-quality-fixes.md',
    anchor: '\uFFFD\uFFFD\uFFFD个 /api 拿不到 J',
    count: 3,
    kind: 'reference',
    why: '§162.3 原样引用 build-mobile.mjs:154 的损坏注释行；原字已不可知，改任何字都是编造。文档 §162.3 另登记「恰好 4 个且都在这两处」的规则',
  },
  {
    file: 'docs/design/2026-10-06-recording-quality-fixes.md',
    anchor: '\uFFFD」这种乱码方块',
    count: 1,
    kind: 'reference',
    why: '§162.4 表格引用 backend/internal/agent/errors.go:173 的注释原文，那条注释本身就含一个 U+FFFD 当示例',
  },
  {
    file: 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md',
    anchor: '\uFFFD`，',
    count: 1,
    kind: 'self-referential',
    why: '文档在讲「文件内 1 个 U+FFFD」，那个字面量就在 replacementCount 正则里 —— 自指，不能靠自己消灭',
  },
  {
    file: 'docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md',
    anchor: 'ERR_INVALID_TYPESCRIPT_SYNTAX',
    count: 1,
    kind: 'self-referential',
    why: '引用的 SyntaxError 报错原文里就含 U+FFFD（上游把中文断言按 GBK 输出所致）',
  },
  {
    file: 'docs/handoff/2026-09-30-email-pipeline-verify.md',
    anchor: '\uFFFD\uFFFD\uFFFD硬截止夹住',
    count: 3,
    kind: 'unknown-original',
    why: '「根因：滚动续期没有〔损坏处〕硬截止夹住」；原字不可知，留作损坏证据',
  },
  {
    file: 'docs/handoff/2026-09-30-write-path-verification-pickup.md',
    anchor: '不可复现的观测不',
    count: 3,
    kind: 'unknown-original',
    why: '「不可复现的观测不〔损坏处〕进缺陷列表」；原字不可知，留作损坏证据',
  },
  {
    file: 'docs/handoff/2026-10-01-audit-round.md',
    anchor: '完全不碰那个正在被写的',
    count: 2,
    kind: 'unknown-original',
    why: '「完全不碰那个正在被写的〔损坏处〕作区」；原字不可知，留作损坏证据',
  },
  {
    file: 'docs/handoff/2026-10-01-shared-tree-hazard.md',
    anchor: '\uFFFD\uFFFD"，但它只覆盖',
    count: 2,
    kind: 'unknown-original',
    why: '「这个提交看起来像已经把工作区保住〔损坏处〕」；原字不可知，留作损坏证据',
  },
  {
    file: 'docs/handoff/2026-10-01-email-snippet-mime-dump.md',
    anchor: 'InsertEmail',
    count: 3,
    kind: 'unknown-original',
    why: '根因段落里 snippet 被截断留下的损坏；原字不可知，留作损坏证据',
  },
  {
    file: 'docs/handoff/2026-10-02-round4-stray-go-mod-guard.md',
    anchor: '先确',
    count: 2,
    kind: 'unknown-original',
    why: '「先确〔损坏处〕注入生效」；原字不可知，留作损坏证据',
  },
  {
    file: 'docs/handoff/2026-10-03-round24-invoice-summary-nan-and-ghost-cards.md',
    anchor: 'XML 附件',
    count: 2,
    kind: 'unknown-original',
    why: '「同 ①，〔损坏处〕附件」；原字不可知，留作损坏证据',
  },
];

function collectTargets() {
  const out = [];
  const docsDir = path.join(ROOT, 'docs');
  if (fs.existsSync(docsDir)) {
    const walk = (d) => {
      for (const e of fs.readdirSync(d, { withFileTypes: true })) {
        const p = path.join(d, e.name);
        if (e.isDirectory()) walk(p);
        else if (e.name.endsWith('.md')) out.push(p);
      }
    };
    walk(docsDir);
  }
  for (const e of fs.readdirSync(ROOT, { withFileTypes: true })) {
    if (e.isFile() && e.name.endsWith('.md')) out.push(path.join(ROOT, e.name));
  }
  return out.sort();
}

// 返回 [{ line, col, kind, snippet }]；text 为空数组表示干净
function detect(text) {
  const findings = [];
  const lines = text.split('\n');

  lines.forEach((line, i) => {
    for (let c = 0; c < line.length; c++) {
      const ch = line[c];
      if (ch === '\uFFFD') {
        findings.push({ line: i + 1, col: c + 1, kind: 'U+FFFD 替换字符（写入被截断，文本已损坏）', snippet: line.trim().slice(0, 60) });
      } else if (ch === '\uFEFF') {
        findings.push({ line: i + 1, col: c + 1, kind: 'U+FEFF（BOM/ZWNBSP，会渲染成游离不可见字符）', snippet: line.trim().slice(0, 60) });
      }
    }
  });

  // 相邻重复标题：连续两行都是标题且内容完全相同
  for (let i = 1; i < lines.length; i++) {
    const a = lines[i - 1].trim();
    const b = lines[i].trim();
    if (a && a === b && /^#{1,6}\s/.test(a)) {
      findings.push({ line: i + 1, col: 1, kind: '标题被重复追加', snippet: a.slice(0, 60) });
    }
  }

  return findings;
}

// ---- 判据自证 ----
function meta() {
  const cases = [
    {
      name: 'U+FFFD',
      bad: '正常一行\n这里坏\uFFFD\uFFFD了\n再一行\n',
      badKinds: ['U+FFFD'],
      good: '# 标题\n\n正常正文，没有坏字符。\n\n## 二级标题\n\n- 列表项\n',
    },
    {
      name: 'U+FEFF',
      bad: '正常一行\n\uFEFF# 追加：新章节\n正文\n',
      badKinds: ['U+FEFF'],
      good: '# 标题\n\n正常正文。\n',
    },
    {
      name: '重复标题',
      bad: '正文 A\n\n## 5. 某章节\n\n正文 B\n',
      badKinds: ['标题被重复追加'],
      good: '## 5. 某章节\n\n正文 B\n\n### 子节\n\n正文 C\n',
    },
  ];

  let ok = true;
  for (const c of cases) {
    // 注意：重复标题这个样本要先复制成两份才构成「相邻重复」
    const badText = c.name === '重复标题' ? c.bad.replace('## 5. 某章节', '## 5. 某章节\n## 5. 某章节') : c.bad;

    const gotBad = detect(badText);
    const hits = c.badKinds.filter((k) => gotBad.some((f) => f.kind.includes(k)));
    const goodHits = detect(c.good);

    if (hits.length !== c.badKinds.length) {
      console.error(`  FAIL ${c.name}: 注入缺陷后只报出 [${hits.join(',') || '无'}]，期望全部 [${c.badKinds.join(',')}]`);
      console.error('       实际报出:', JSON.stringify(gotBad));
      ok = false;
    } else if (goodHits.length !== 0) {
      console.error(`  FAIL ${c.name}: 干净样本被误报 ${goodHits.length} 条 —— 检测器过宽`);
      ok = false;
    } else {
      console.log(`  ok   ${c.name}: 注入→报出，干净→不报`);
    }
  }

  if (!ok) {
    console.error('\n判据自证 FAILED：检测器本身失去区分能力，本轮结论不可信。');
    process.exit(2);
  }
  console.log('判据自证通过（3/3 类缺陷均能抓出，且不误报干净文档）\n');
}

// ─────────────────────────────────────────────────────────────
// --commits：检查最近 N 条提交信息里有没有 U+FFFD / U+FEFF。
//
// 为什么单独加这个口：`git log` 里 1814d15 / 03565ce 的提交信息开头带了
// U+FEFF —— 那是用 PowerShell `Out-File` 写 commit-msg 文件时混进去的
// （PS 5.1 的 Out-File -Encoding 默认写 BOM）。已推到 origin/main，
// 改历史要 force-push 且与并发会话冲突，**不重写**（详见 handoff §4.27），
// 但必须挡住下一次。写 commit-msg 请用本脚本所在仓库的 write 工具或
// `git commit -m`，**不要用 Out-File / `>` 重定向**。
// ─────────────────────────────────────────────────────────────
function auditCommits(n) {
  const raw = execSync(`git log -${n} --format=%H%x00%B%x00%x01`, { encoding: 'utf8', maxBuffer: 1 << 28 });
  const records = raw.split('\u0001').filter((s) => s.trim());
  if (records.length === 0) {
    console.error('FATAL: 没读到任何提交，判据失效');
    process.exit(2);
  }
  let bad = 0;
  for (const r of records) {
    const [sha, msg] = r.replace(/^\s+/, '').split('\u0000');
    const first = (msg || '').split('\n')[0];
    const hits = [...first].filter((c) => c === '\uFFFD' || c === '\uFEFF');
    if (hits.length) {
      bad++;
      console.log(`FAIL  ${sha.slice(0, 7)}  ${JSON.stringify(first.slice(0, 56))}  (${hits.map((c) => 'U+' + c.codePointAt(0).toString(16).toUpperCase()).join(',')})`);
    } else {
      console.log(`PASS  ${sha.slice(0, 7)}  ${first.slice(0, 56)}`);
    }
  }
  console.log(`\n=== 汇总 ===\n扫描 ${records.length} 条提交信息，异常 ${bad} 条`);
  process.exit(bad === 0 ? 0 : 1);
}

// ---- 豁免栏 ----
/**
 * 把 WAIVERS 解析成「相对路径 → 被豁免的行号集合」，同时校验登记本身是否健康。
 *
 * 三种不健康都算**登记坏了**（判据问题，不是文档问题）⇒ 一律计入并让闸报红：
 *   missing     —— 锚点在文件里找不到 ⇒ 已被人修好 ⇒ 登记过期，必须删
 *   ambiguous   —— 锚点命中多行 ⇒ 这条豁免其实覆盖了不该覆盖的内容
 *   count-mismatch —— 锚点行的 U+FFFD 个数与登记不符 ⇒ 文档变了或锚点选得不准
 */
function resolveWaivers(relToFile) {
  const waived = new Map(); // rel -> Set(lineNo)
  const problems = [];

  for (const w of WAIVERS) {
    if (!w.why || w.why.trim().length < 8) {
      problems.push({ rel: w.file, kind: 'WAIVER_NO_REASON', detail: `豁免 ${w.anchor} 没有可排查的理由（理由为空 = 没登记）` });
      continue;
    }
    const abs = relToFile.get(w.file);
    if (!abs) {
      problems.push({ rel: w.file, kind: 'WAIVER_FILE_MISSING', detail: `豁免指向的文件不存在：${w.file}` });
      continue;
    }
    const lines = fs.readFileSync(abs, 'utf8').split('\n');
    const hits = [];
    lines.forEach((l, i) => { if (l.includes(w.anchor)) hits.push(i + 1); });

    if (hits.length === 0) {
      // ★★ 锚点没命中有**三种**原因，不能一律说「多半是已修好」——
      //    因为三种的处置**相反**，照错误的那条机械执行会把洞重新暴露：
      //      ① 缺陷真被修好了        ⇒ 删登记
      //      ② 缺陷还在、周围文本被改 ⇒ 更新锚点（**不是**删登记）
      //      ③ 登记的锚点本来就写错   ⇒ 更新锚点
      //    判据是**该文件现存的 U+FFFD 数量**：少于登记数才支持 ①。
      //    ⚠️ 下面提到的「两次门都说『多半是已修好』」记的是**修复前**的行为 ——
      //    那时这个位置只有一句建议，锚点没命中就一律劝删登记。
      //    现在已按下面两分支拆开，那句话**只**在 fileFffd < w.count 时才可能出现。
      //    （历史证据，也是这个分支存在的理由：本仓曾连续两次把锚点猜错——
      //      先漏了引号、后把直引号写成弯引号；两次缺陷其实都还在，
      //      而那时的门两次都劝我删登记。）
      const fileFffd = (fs.readFileSync(abs, 'utf8').match(/\uFFFD/g) || []).length;
      // ⚠️ 判据的**精度上限**：上面比的是「整个文件的 U+FFFD 总数」与「**这一条**登记的 count」。
      //    一个文件只有一条登记时两者同义；**同一文件挂多条登记时就不等价了** ——
      //    2026-10-07 实测：11 条登记落在 9 个文件上，其中 2 个文件各有 2 条。
      //    那一刻「文件里还有 FFFD」并不能证明**这一条**对应的缺陷还在
      //    （那些字符可能属于同文件的另一条登记）⇒ 两种情形不许给同一句确定性建议。
      const siblings = WAIVERS.filter((x) => x.file === w.file).length;
      const multi = siblings > 1
        ? `　⚠️ 该文件有 ${siblings} 条登记，按文件总数判断对**这一条**不精确：请逐条核对后再处置。`
        : '';
      if (fileFffd < w.count) {
        problems.push({ rel: w.file, kind: 'WAIVER_STALE', detail: `锚点 "${w.anchor}" 找不到，且该文件现存 U+FFFD(${fileFffd}) 已少于登记数(${w.count}) ⇒ 多半确已修好，请删掉这条登记` });
      } else {
        problems.push({ rel: w.file, kind: 'WAIVER_ANCHOR_DRIFT', detail: `锚点 "${w.anchor}" 没命中，但该文件仍有 ${fileFffd} 个 U+FFFD（登记 count=${w.count}）⇒ ${multi ? '**该文件不止一条登记，不能断定缺陷还在**' : '缺陷大概率还在'}，是**锚点漂了或登记写错**：请更新 anchor，**不要删登记** —— 删掉会把同一处缺陷重新报红${multi}` });
      }
      continue;
    }
    if (hits.length > 1) {
      problems.push({ rel: w.file, kind: 'WAIVER_AMBIGUOUS', detail: `豁免锚点 "${w.anchor}" 命中 ${hits.length} 行（${hits.join(',')}）⇒ 锚点不够唯一` });
      continue;
    }
    const ln = hits[0];
    const got = lines[ln - 1].split('\uFFFD').length - 1;
    if (got !== w.count) {
      problems.push({ rel: w.file, kind: 'WAIVER_COUNT_MISMATCH', detail: `豁免 "${w.anchor}" 登记 count=${w.count}，实际该行有 ${got} 个 U+FFFD` });
      continue;
    }
    if (!waived.has(w.file)) waived.set(w.file, new Set());
    waived.get(w.file).add(ln);
  }

  return { waived, problems };
}

/**
 * **扫描范围自证**（退出码 2 = 量具坏了，不是文档坏了）。
 *
 * 为什么这道闸原本没有它：唯一的范围护栏是 `files.length === 0`，
 * 而「塌缩到 2 个」不是 0 ⇒ 通过。2026-10-07 那次 cwd 事故里它是**侥幸**才红的
 * （9 条 WAIVER_FILE_MISSING 顺带报了出来），如果豁免登记表当时是空的，
 * 同一个缺陷就会**绿着**通过 —— 判据失效与通过在输出上完全同形。
 *
 * 两侧各查各的：
 *   · repo 模式查**结构**（三个 docs 子目录各自存在且非空）+ **下限哨兵**；
 *     查结构而不是查「文件数等于 516」，是因为盘点类数字必然腐烂（文档会被合并/归档）。
 *   · fixture 模式（显式给了 AUDIT_DOC_ROOT）跳过结构查，但**必须**在输出里标注模式。
 */
function assertScope(files) {
  const reasons = [];
  if (SCOPE === 'repo') {
    for (const d of ['docs/design', 'docs/handoff', 'docs/audits']) {
      const abs = path.join(ROOT, d);
      if (!fs.existsSync(abs)) {
        reasons.push(`目录不存在：${d}`);
        continue;
      }
      if (!files.some((f) => f.startsWith(abs + path.sep))) {
        reasons.push(`目录存在但一个 .md 都没扫到：${d}`);
      }
    }
    if (files.length < REPO_SCOPE_FLOOR) {
      reasons.push(`只扫到 ${files.length} 个 .md，低于下限 ${REPO_SCOPE_FLOOR} ⇒ 扫描范围疑似塌缩`);
    }
  }
  if (reasons.length) {
    console.error('FATAL: 扫描范围自证失败 —— 判据失效，不是文档坏了。');
    console.error(`  扫描根：${ROOT}（模式：${SCOPE}）`);
    for (const r of reasons) console.error(`  · ${r}`);
    process.exit(2);
  }
}

function main() {
  if (process.argv.includes('--meta')) {
    meta();
    return;
  }

  const ci = process.argv.indexOf('--commits');
  if (ci !== -1) {
    const n = Number(process.argv[ci + 1]) || 20;
    auditCommits(n);
    return;
  }

  const files = collectTargets();
  if (files.length === 0) {
    console.error(`FATAL: 没找到任何 .md 目标，判据失效（扫描根 ${ROOT}，模式 ${SCOPE}）`);
    process.exit(2);
  }
  assertScope(files);

  // ★ 2026-10-22 round50 修掉的**平台假红**（与 D7 同族：路径分隔符）。
  //   豁免登记表 `scripts/doc-encoding-waivers.json` 里的 `file` 字段一律用
  //   **正斜杠**（仓里所有名单型文件都是这个约定）；
  //   而这里的 Map key 来自 `path.relative()` ⇒ Windows 上是**反斜杠**
  //   ⇒ `relToFile.get(w.file)` 永远查不到 ⇒ 每条登记都报
  //   **WAIVER_FILE_MISSING**（本次实测 9 条）⇒
  //   而真正被豁免的 U+FFFD 因为查不到登记，也就**没被豁免** ⇒
  //   ⇒ 文档里那些**已登记的**损坏点被一并报成问题（本次实测 14 处）。
  //   ⚠ 在 Linux 上 `path.relative()` 返回正斜杠 ⇒ 一切对上 ⇒ **CI 一直是绿的**。
  //   ⇒ 修法：key 统一归一成正斜杠（两侧都用同一个函数，保证自洽）。
  const relOf = (f) => path.relative(ROOT, f).split(path.sep).join('/');
  const relToFile = new Map(files.map((f) => [relOf(f), f]));
  const { waived, problems: waiverProblems } = resolveWaivers(relToFile);

  for (const p of waiverProblems) {
    console.log(`FAIL  [豁免登记] ${p.rel}  ${p.kind}\n        ${p.detail}`);
  }

  let total = 0;
  let waivedTotal = 0;
  for (const f of files) {
    // ⚠ 必须与上面 relOf() **逐字同形** —— waived 的 key 来自豁免登记表
    //   （正斜杠），这里若用反斜杠就永远查不到豁免。
    const rel = relOf(f);
    const text = fs.readFileSync(f, 'utf8');
    const all = detect(text);
    const allow = waived.get(rel) || new Set();
    // 只有「被豁免的行上的 U+FFFD」才豁免；重复标题这类结构问题一律照报。
    const findings = all.filter((x) => !(allow.has(x.line) && x.kind.startsWith('U+FFFD')));
    const waivedHere = all.length - findings.length;
    waivedTotal += waivedHere;

    if (findings.length === 0) {
      if (waivedHere > 0) console.log(`PASS  ${rel}  （${waivedHere} 个 U+FFFD 已登记豁免）`);
      else console.log(`PASS  ${rel}`);
      continue;
    }
    total += findings.length;
    console.log(`FAIL  ${rel}  (${findings.length})${waivedHere > 0 ? ` [另有 ${waivedHere} 个已豁免]` : ''}`);
    for (const x of findings) {
      console.log(`        ${x.line}:${x.col}  ${x.kind}  |  ${x.snippet}`);
    }
  }

  console.log('\n=== 汇总 ===');
  console.log(`扫描根 ${ROOT}（模式 ${SCOPE}）`);
  console.log(`扫描 ${files.length} 个 .md，发现 ${total} 处问题${waivedTotal > 0 ? `；另有 ${waivedTotal} 个 U+FFFD 属登记豁免（${WAIVERS.length} 条登记）` : ''}`);
  const bad = total + waiverProblems.length;
  process.exit(bad === 0 ? 0 : 1);
}

main();
