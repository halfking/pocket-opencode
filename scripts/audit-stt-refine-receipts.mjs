#!/usr/bin/env node
// audit-stt-refine-receipts —— §377/§378 精校实验读数的**一致性闸**。
//
// 为什么需要它（不是凭感觉，是 §384 抓出来的）：
//   §383 把五份实验日志的逐次读数**手工汇总**成一张表。那张表 25 格里有 1 格
//   「§377.4 时间点 24/24」是**无据**的 —— overlap.log 的判据行根本没有时间点列，
//   24 = 8×3 是**算出来的**，不是读出来的。
//   而 §383.5 自己知道「这一行未经机器核对」，却没有把它变成会报红的东西。
//   ⇒ 一张纯手工的汇总表 = 一个恒真的检查：没人会手算分母。
//
// ── 三条设计约束（每条都对应一次真实失败）──────────────────────────
//
//   ① **存逐次读数，不存聚合值。**
//      聚合只可能算错一次，且能被 diff 抓到；
//      若存的是聚合值，分子分母各写错一点就会**自洽地骗过所有人**。
//
//   ② **「这列没测」必须与「这列全为 0」区分开。**
//      收据里一律用 null 表示「未测」，**禁止用 0 顶替** ——
//      §384.3① 那格无据数字，正是 0/3 顶替了「不存在」。
//      本闸对「全 null 的列」额外要求：文档那一格必须写成未测形态，
//      出现任何数字都报红。
//
//   ③ **闸要能报出「量具坏了」。**
//      §384.1 实测：解析器首版漏了 `/`，把整列读成不可测，打印出 `时间点：0/0`
//      —— 那比报错危险，因为它长得像一个诚实的读数。
//      ⇒ --meta 造变异样本，每个都必须报红；闸失去区分能力时 rc=2，
//      **而不是安静放行**。
//
// ⚠ 本闸保护的是「文档表格 ↔ 收据」的一致性，**不是**「日志 ↔ 收据」。
//   原始日志已不可复核，收据 JSON 才是唯一长久副本。
//
// ── 2026-10-17（§787.1② 收尾）：收据**确认永久丢失** ───────────────
//
// 查证结果（不是推断）：
//   · `git log --all --diff-filter=A --name-only` 里**从未出现过**该文件
//   · `docs/design/data/` **从未被任何提交跟踪**（也不在任何 .gitignore 里）
//   · 本机已无副本（/tmp、Library、Downloads、Trash 均无）
//   · 引入脚本的提交 `9813ee36` 同时写着「仓里现无可用语料，已核到底」
//     ⇒ 它**一出生就是坏的**
//
// ⇒ 两种处置：恢复语料，或**把无据的 ✅ 撤回**。§787 的处置是后者：
//   §384.2 的 25 格已全部改写为「未测」，并登记进 WAIVERS。
//
// ⚠⚠ 于是本闸换了一个**能做到的**职责：
//
//   收据在 ⇒ 逐格 diff（原来的职责，一字未改）
//   收据不在 ⇒ 核验「文档**如实声明**了它没有凭据」
//
// 后者不是降级。它精确针对**已经发生过的**那个失效：
// 表格声称 24 格已核验，而没有任何机器核对过 —— 而因为它在输出，没人怀疑。
// 「有 ✅ 却拿不出收据」现在**必须**报红，这正是本闸接下来能一直守住的东西。
//
// ⚠ 收据回来时：把 JSON 放回 docs/design/data/，把 §384.2 的格子改回数字，
//   删掉 kind=evidence-lost 的那条豁免即可。脚本的收据分支无需改动。
//
// 用法：
//   node scripts/audit-stt-refine-receipts.mjs          # 审计
//   node scripts/audit-stt-refine-receipts.mjs --meta   # 判据自证
//
// 退出码：0 = 一致；1 = 发现不一致；2 = 判据自证失败（闸坏了，不是文档坏了）。

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(SCRIPT_DIR, '..');

// ── 豁免表 ────────────────────────────────────────────────────────────
//
// 每条豁免都必须带 reason，且 mustContain 的锚点必须仍然存在 ——
// 锚点消失（= 有人回改了原文）时豁免**过期**，必须重新审视。
//
// ⚠ 这里**不**记绝对路径：audit() 按 root 读文档，
//   而 --meta 跑在临时目录的副本上 —— 记死路径会让自证永远命中错文件。
const WAIVERS = [
  {
    kind: 'evidence-lost',
    section: '### 384.2 ',
    mustContain: '凭据丢失，全表未测',
    lostFile: 'docs/design/data/stt-refine-receipts-377-378.json',
    reason:
      '支撑 §384.2 那 25 格的逐次读数收据从未进入版本库（2026-10-17 用 ' +
      '`git log --all --diff-filter=A` 查无此文件，`docs/design/data/` 从未被跟踪），' +
      '本机亦无副本。⇒ 那 24 个 ✅ **从没有被机器核对过**。' +
      '按追加式勘误惯例保留原始数字作为历史记录，但把它们逐格改写为「未测」并在此登记，' +
      '使「有 ✅ 却拿不出凭据」今后必须报红。收据恢复后删掉本条即可。',
    owner: '§787.1②',
  },
];

// ⚠⚠ 扫描根必须可被**覆盖**，且 --meta 只在覆盖后的临时目录里跑。
//   首版 --meta 直接改**仓库里真实的设计文档**再还原 —— 那是定时炸弹：
//   进程被 kill / 机器断电 ⇒ 2.8 MB 的设计文档停在「被改成 24/24」的中间态。
//   同仓 audit-doc-encoding.mjs 早就踩过同一类坑（它用 AUDIT_DOC_ROOT 在临时目录隔离）。
//   ⇒ 这里的覆盖口是 STT_RECEIPTS_ROOT；一旦使用它，输出会显式标注 模式=fixture。
const EXPLICIT_ROOT = process.env.STT_RECEIPTS_ROOT ? path.resolve(process.env.STT_RECEIPTS_ROOT) : null;
const SCAN_ROOT = EXPLICIT_ROOT || ROOT;
const MODE = EXPLICIT_ROOT ? 'fixture' : 'repo';

function paths(root) {
  return {
    receipts: path.join(root, 'docs/design/data/stt-refine-receipts-377-378.json'),
    doc: path.join(root, 'docs/design/2026-10-06-recording-quality-fixes.md'),
  };
}

// ---------------------------------------------------------------- 工具

/** 从收据逐次读数重新聚合。null 一律**不参与**计数。 */
function aggregate(experiment) {
  const calls = experiment.calls;
  const n = calls.length;
  const sumBool = (k) => calls.filter((c) => c[k] === true).length;
  const measured = (k) => calls.filter((c) => c[k] !== null && c[k] !== undefined).length;
  return {
    n,
    name_fixed: sumBool('name_fixed'),
    ci_fixed: sumBool('ci_fixed'),
    augustus_kept: sumBool('augustus_kept'),
    swapped: sumBool('swapped'),
    // ⭐ 约束②：可测数为 0 ⇒ 这一列「没测过」，不能报成 0/0
    time_measured: measured('time_total'),
    time_kept: calls.reduce((a, c) => a + (c.time_kept ?? 0), 0),
    time_total: calls.reduce((a, c) => a + (c.time_total ?? 0), 0),
    n_failed_attempts: experiment.n_failed_attempts,
  };
}

/** 「未测」的文档形态。收窄成明确的几形态，避免把真数字误判成未测。 */
const NOT_MEASURED = /未测|无此列|不存在|不可测|凭据丢失/;

/** 把文档单元格里 ✅/❌ 与强调标记剥掉，留下可比对的正文。 */
function cleanCell(raw) {
  return raw.replace(/[`*]/g, '').replace(/[✅❌]/g, '').trim();
}

/**
 * 抽出 §384.2 的对账表：`| 实验 | n | 专名修好 | CI修好 | 奥古斯都保住 | 时间点|`
 * 按 §377.2 / §377.3 / §377.4 / §378.2 / §378.3 匹配实验号。
 */
function parseDocTable(doc) {
  const lines = doc.split('\n');
  const start = lines.findIndex((l) => /^###\s+384\.2\s/.test(l));
  if (start < 0) return { error: '找不到 §384.2 小节' };
  const rows = {};
  for (let i = start; i < lines.length && !/^###\s+384\.3\s/.test(lines[i]); i++) {
    // ⚠ 实验号是 **三位** 序号：`§377.2` / `§378.3`。
    // 首版写成 `§37[78]\.\d` ⇒ 只吃了 §377 的前两位、第三位对不上 `.`
    // ⇒ 五行全部「缺行」。这与 §384.1 同源：量具自己先坏，且坏得**不像报错**。
    const m = lines[i].match(/^\|\s*(§\d{3}\.\d+)[^|]*\|(.+)\|\s*$/);
    if (!m) continue;
    const cells = m[2].split('|').map(cleanCell);
    // ⚠ 捕获组带 `§`（§377.2），而收据的键不带（`377.2`）。
    //   首版忘了剥 ⇒ 五行全部「缺行」，而闸报的是「文档里没有这一行」，
    //   **不是**「键对不上」⇒ 会把人引去改文档。键错要报成键错。
    rows[m[1].replace(/^§/, '')] = cells; // [n, 专名修好, CI修好, 奥古斯都保住, 时间点]
  }
  return { rows };
}

// ---------------------------------------------------------------- 审计

/** 收据丢失时的判定：文档是否**如实声明**了没有凭据。 */
function auditWithoutReceipts(doc, quiet) {
  const findings = [];
  const lost = WAIVERS.find((w) => w.kind === 'evidence-lost');
  if (!lost) {
    findings.push({
      kind: '豁免表',
      msg: '收据已丢失，但 WAIVERS 里没有 kind=evidence-lost 的登记 ⇒ ' +
        '本闸无法判定「文档是否如实声明」',
    });
  }

  // 豁免自身的健康度：锚点必须在
  for (const w of WAIVERS) {
    if (!w.reason || !w.reason.trim()) {
      findings.push({ kind: '豁免表', msg: `${w.owner} 的豁免没有理由` });
      continue;
    }
    if (!doc.includes(w.section)) {
      findings.push({ kind: '豁免过期', msg: `${w.owner}：锚点小节 ${w.section} 已消失` });
      continue;
    }
    if (!doc.includes(w.mustContain)) {
      findings.push({
        kind: '豁免过期',
        msg: `${w.owner}：${w.section} 附近已找不到 ${JSON.stringify(w.mustContain)}`,
      });
      continue;
    }
  }

  // ★ 核心判定：§384.2 里**每一格**都必须写明未测。
  //   任何一格仍写着裸数字（= 暗示「已核验」）都报红 ——
  //   这正是已经发生过的那次失效：有 ✅ 却没有任何机器核对过。
  const parsed = parseDocTable(doc);
  if (parsed.error) {
    findings.push({ kind: '结构', msg: parsed.error });
    return findings;
  }
  const ids = Object.keys(parsed.rows);
  if (ids.length === 0) {
    findings.push({ kind: '结构', msg: '§384.2 表里一行都没解析出来' });
  }
  let cells = 0;
  for (const id of ids) {
    const row = parsed.rows[id];
    for (let i = 0; i < row.length; i++) {
      cells++;
      if (!NOT_MEASURED.test(row[i])) {
        findings.push({
          kind: '无据数字',
          msg: `${id} 第 ${i + 1} 格写着「${row[i]}」—— 收据已永久丢失，` +
            `没有任何机器核对过它。⇒ 它不能继续以「已核验」的形态存在：` +
            `请改写为「未测」并在 WAIVERS 登记（§384.3⑥）`,
        });
      }
    }
  }
  if (!quiet) {
    console.log(
      `收据缺失（${lost ? lost.lostFile : 'docs/design/data/stt-refine-receipts-377-378.json'}）`,
    );
    console.log(`§384.2 表：${ids.length} 行 / ${cells} 格，全部要求标注「未测」`);
  }
  return findings;
}

function audit(root = SCAN_ROOT, quiet = false) {
  const { receipts: RECEIPTS, doc: DOC } = paths(root);

  // ── 分支一：收据在 ⇒ 逐格 diff（原职责，一字未改）
  if (!fs.existsSync(RECEIPTS)) {
    if (!fs.existsSync(DOC)) {
      console.error(`\n❌ 无法判定（不是「通过」）：设计文档不存在 ${DOC}`);
      process.exit(2);
    }
    const doc = fs.readFileSync(DOC, 'utf8');
    return { findings: auditWithoutReceipts(doc, quiet), receipts: null, mode: 'receipts-lost' };
  }
  if (!fs.existsSync(DOC)) {
    console.error(`\n❌ 无法判定（不是「通过」）：设计文档不存在 ${DOC}`);
    process.exit(2);
  }

  const findings = [];
  let receipts;
  try {
    receipts = JSON.parse(fs.readFileSync(RECEIPTS, 'utf8'));
  } catch (e) {
    console.error(`\n❌ 无法判定（不是「通过」）：收据不是合法 JSON —— ${e.message}`);
    console.error(`   位置：${RECEIPTS}`);
    process.exit(2);
  }
  const doc = fs.readFileSync(DOC, 'utf8');

  // 先过豁免：锚点必须在，且必须真的命中源码/文档（空头支票检查）
  for (const w of WAIVERS) {
    if (!w.reason || !w.reason.trim()) {
      findings.push({ kind: '豁免表', msg: `${w.owner} 的豁免没有理由` });
      continue;
    }
    if (!doc.includes(w.section)) {
      findings.push({ kind: '豁免过期', msg: `${w.owner}：锚点小节 ${w.section} 已消失` });
      continue;
    }
    if (!doc.includes(w.mustContain)) {
      findings.push({
        kind: '豁免过期',
        msg: `${w.owner}：${w.section} 里已找不到 ${JSON.stringify(w.mustContain.slice(0, 40))}…`,
      });
      continue;
    }
    if (!quiet) console.log(`WAIVED  ${w.owner}  ${w.section}（登记仍然成立）`);
  }

  const parsed = parseDocTable(doc);
  if (parsed.error) {
    findings.push({ kind: '结构', msg: parsed.error });
    return { findings, receipts, mode: 'receipts' };
  }

  let rowSum = 0;
  for (const [eid, exp] of Object.entries(receipts.experiments)) {
    const agg = aggregate(exp);
    rowSum += agg.n;
    const cells = parsed.rows[eid];
    if (!cells || cells.length < 5) {
      findings.push({ kind: '缺行', msg: `${eid}（${exp.title}）在 §384.2 表里没有可解析的行` });
      continue;
    }

    const [nC, nameC, ciC, augC, timeC] = cells;

    if (nC !== String(agg.n)) {
      findings.push({ kind: 'n', msg: `${eid} n：文档 ${nC} / 收据 ${agg.n}` });
    }
    if (nameC !== String(agg.name_fixed)) {
      findings.push({ kind: '专名修好', msg: `${eid}：文档 ${nameC} / 收据 ${agg.name_fixed}` });
    }
    if (ciC !== String(agg.ci_fixed)) {
      findings.push({ kind: 'CI修好', msg: `${eid}：文档 ${ciC} / 收据 ${agg.ci_fixed}` });
    }
    if (augC !== `${agg.augustus_kept}/${agg.n}`) {
      findings.push({ kind: '奥古斯都', msg: `${eid}：文档 ${augC} / 收据 ${agg.augustus_kept}/${agg.n}` });
    }

    // ⭐ 约束②：没测过的列，文档里不许出现数字
    if (agg.time_measured === 0) {
      if (!NOT_MEASURED.test(timeC)) {
        findings.push({
          kind: '无据数字',
          msg: `${eid} 时间点：该实验 ${agg.n} 次调用的收据里 time_total 全为 null（未测），` +
            `文档却写了「${timeC}」。0 与「没测」必须区分（§384.3①）`,
        });
      }
    } else {
      const want = `${agg.time_kept}/${agg.time_total}`;
      if (timeC !== want) {
        findings.push({ kind: '时间点', msg: `${eid}：文档 ${timeC} / 收据 ${want}` });
      }
    }
  }

  // 分母自证：§384.2 的行 n 相加必须等于收据里的总调用数
  const totalCalls = Object.values(receipts.experiments).reduce((a, e) => a + e.n_calls, 0);
  if (rowSum !== totalCalls) {
    findings.push({
      kind: '分母',
      msg: `§384.2 行 n 相加 ${rowSum} ≠ 收据总调用 ${totalCalls}`,
    });
  }

  if (!quiet) {
    console.log(
      `\n收据 ${path.relative(ROOT, RECEIPTS)}：${Object.keys(receipts.experiments).length} 组 / ` +
        `${totalCalls} 次成功调用  (模式=${MODE})`,
    );
    console.log(`§384.2 表：${Object.keys(parsed.rows).length} 行，行 n 合计 ${rowSum}`);
  }
  return { findings, receipts, mode: 'receipts' };
}

// ---------------------------------------------------------------- --meta
// 判据自证：**两个分支都要自证**。
// 只自证「收据在」那一支是不够的 —— 收据已经丢了，今天实际跑的是另一支，
// 而那一支若恒绿，它就是 §775 记的那种「因为一直在输出而没人怀疑」。

function meta() {
  const src = paths(ROOT);
  // ⭐⭐ 全部变异都发生在**临时目录的副本**上，仓库里那两个文件一次都不被写。
  //   首版 --meta 直接改真实文档再靠 finally 还原 —— 那是定时炸弹：
  //   进程被 kill / 断电 ⇒ 2.8 MB 的设计文档停在中间态。
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'stt-refine-receipts-meta-'));
  const dst = paths(tmp);
  fs.mkdirSync(path.dirname(dst.receipts), { recursive: true });
  fs.mkdirSync(path.dirname(dst.doc), { recursive: true });

  let alive = 0;
  let broken = 0;
  // ⚠ 两个方向的判据必须分开记，否则「变异报红」与「阴性对照安静」
  // 会共用一个计数器 —— 那样恒红也能凑够 alive。
  const expectRed = (name, res, why, expect) => {
    const ok = res.findings.length > 0;
    console.log(`META  ${name}\n      ${ok ? '✅ 报红' : '❌ 未报红 —— 闸没有牙'}  预期：${expect}`);
    if (ok) {
      alive++;
      if (why) console.log(`      捕获：${why.slice(0, 200)}`);
    } else broken++;
  };
  const expectClean = (name, res, expect) => {
    const clean = res.findings.length === 0;
    console.log(`META  ${name}\n      ${clean ? '✅ 零误报' : '❌ 误报 —— 闸是恒红的'}  预期：${expect}`);
    if (clean) alive++;
    else {
      broken++;
      for (const f of res.findings) console.log(`      误报：${f.kind}: ${f.msg}`);
    }
  };
  const fmt = (res) => res.findings.map((f) => `${f.kind}: ${f.msg}`).join(' | ');

  try {
    if (!fs.existsSync(src.doc)) {
      console.error('设计文档不存在，无法自证');
      process.exit(2);
    }
    const pristineDoc = fs.readFileSync(src.doc, 'utf8');

    // ── 合成夹具：收据 5 组共 12 次调用 ────────────────────────────
    const mk = (n, keep, nameFixed) =>
      Array.from({ length: n }, (_, i) => ({
        name_fixed: nameFixed ? i < 1 : false,
        ci_fixed: false,
        augustus_kept: true,
        time_total: keep * (i + 1),
        time_kept: i + 1,
      }));
    const FIXTURE = {
      experiments: {
        '377.2': { title: '合并 A/B', n_calls: 3, n_failed_attempts: 0, calls: mk(3, 2) },
        '377.3': { title: '术语表 A/B', n_calls: 3, n_failed_attempts: 0, calls: mk(3, 3) },
        // 377.4 的 time_total 全为 null ⇒ 文档那一格必须写成「未测」形态
        '377.4': {
          title: '重叠对照', n_calls: 2, n_failed_attempts: 0,
          calls: [0, 1].map(() => ({ name_fixed: false, ci_fixed: false, augustus_kept: true, time_total: null, time_kept: null })),
        },
        '378.2': { title: '跨模型', n_calls: 2, n_failed_attempts: 0, calls: mk(2, 4) },
        '378.3': { title: 'claude×术语表', n_calls: 2, n_failed_attempts: 0, calls: mk(2, 5, true) },
      },
    };

    // 与夹具逐格一致的**干净文档**。⚠ 必须把 5 行**全部**换掉：
    //   首版只换了 377.2 一行，于是 A1/A2/A3 三个变异「报红」的理由是
    //   另外四行本来就对不上 —— 判据恒红也能满分通过自证。
    //   这正是本仓反复踩的形态：**证明要证明的东西，否则证明等于没证明。**
    const CLEAN_ROWS = {
      '§377.2 合并 A/B': '| §377.2 合并 A/B | 3 | 0 | 0 | 3/3 | 6/12 |',
      '§377.3 术语表 A/B': '| §377.3 术语表 A/B | 3 | 0 | 0 | 3/3 | 6/18 |',
      '§378.2 跨模型': '| §378.2 跨模型 | 2 | 0 | 0 | 2/2 | 3/12 |',
      '§378.3 claude×术语表': '| §378.3 claude×术语表 | 2 | 1 | 0 | 2/2 | 3/15 |',
      '§377.4 重叠对照': '| §377.4 重叠对照 | 2 | 0 | 0 | 2/2 | 未测（无此列） |',
    };
    // ⚠⚠ 只在 **§384.2 小节内部**替换。
    //   首版用 `String.replace(re, …)` 全局替换，结果命中的是 **§383.0** 的
    //   另一张同名表（两表行首完全一样）—— 于是夹具文档里的 §384.2 纹丝未动，
    //   A0「干净夹具」直接误报，A1~A4 四个变异也跟着**因为错误的理由**报红。
    //   ⇒ 判据确实「有牙」，但牙咬的不是被检对象。
    //   这与「grep 错方向」「扫描范围把被检对象排除」是同一族。
    let cleanDoc = pristineDoc;
    {
      const s384 = cleanDoc.search(/^###\s+384\.2\s/m);
      const e384 = cleanDoc.search(/^###\s+384\.3\s/m);
      if (s384 < 0 || e384 < 0 || e384 < s384) {
        console.log('META  ⛔ 找不到 §384.2 / §384.3 小节边界 ⇒ 自证无法进行');
        broken++;
      } else {
        const head = cleanDoc.slice(0, s384);
        const body = cleanDoc.slice(s384, e384);
        const tail = cleanDoc.slice(e384);
        let patched = body;
        for (const [prefix, row] of Object.entries(CLEAN_ROWS)) {
          const re = new RegExp('^\\|\\s*' + prefix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\s*\\|.*$', 'm');
          if (!re.test(patched)) {
            console.log(`META  ⛔ §384.2 里找不到行「${prefix}」⇒ 自证无法进行`);
            broken++;
            continue;
          }
          patched = patched.replace(re, row);
        }
        cleanDoc = head + patched + tail;
      }
    }

    // ── 分支 A：收据在 ──────────────────────────────────────────────
    fs.writeFileSync(dst.receipts, JSON.stringify(FIXTURE, null, 1) + '\n');
    fs.writeFileSync(dst.doc, cleanDoc);
    expectClean('A0 干净夹具零误报（收据在）', audit(tmp, true),
      '干净状态不得误报，否则这道闸是恒红的');

    const MUTATIONS_WITH_RECEIPTS = [
      {
        name: 'A1 篡改一条逐次读数（augustus_kept true→false）',
        run: (r) => { r.experiments['378.2'].calls[0].augustus_kept = false; },
        expect: '378.2 的「奥古斯都」格会与收据不符',
      },
      {
        name: 'A2 把「未测」的时间点伪造成 0/0（复现 §384.3① 的错法）',
        run: (r) => {
          for (const e of Object.values(r.experiments)) {
            for (const c of e.calls) {
              if (c.time_total === null) { c.time_total = 0; c.time_kept = 0; }
            }
          }
        },
        expect: '收据声称测过、文档说未测 → 时间点格报红',
      },
      {
        name: 'A3 删掉一个实验的全部调用（模拟分母写错）',
        run: (r) => { r.experiments['378.2'].calls = []; r.experiments['378.2'].n_calls = 0; },
        expect: '378.2 的 n/奥古斯都/时间点与行 n 合计都会报红',
      },
      {
        name: 'A4 文档某格多打一个数字',
        run: null,
        docMut: (d) => d.replace(/(\| §378\.2 跨模型 \|[^\n]*?)\| 3\/12 \|/, '$1| 9/99 |'),
        expect: '时间点格与收据不符',
      },
    ];
    for (const m of MUTATIONS_WITH_RECEIPTS) {
      // ⚠ 每个变异都必须从**同一份干净状态**出发。
      //   首版只重置文档不重置收据 ⇒ A4 继承了 A3 的变异收据，
      //   于是 A4「报红」的理由其实是 A3 的残留 —— 又是「牙咬的不是被检对象」。
      fs.writeFileSync(dst.doc, cleanDoc);
      fs.writeFileSync(dst.receipts, JSON.stringify(FIXTURE, null, 1) + '\n');
      if (m.run === null) {
        const bad = m.docMut(cleanDoc);
        if (bad === cleanDoc) {
          console.log(`META  ${m.name}\n      ⛔ 没能注入变异（文档结构与预期不符）`);
          broken++;
          continue;
        }
        fs.writeFileSync(dst.doc, bad);
      } else {
        const receipts = JSON.parse(JSON.stringify(FIXTURE));
        m.run(receipts);
        fs.writeFileSync(dst.receipts, JSON.stringify(receipts, null, 1) + '\n');
      }
      const res = audit(tmp, true);
      expectRed(m.name, res, fmt(res), m.expect);
    }

    // ── 分支 B：收据不在（**这才是今天仓库的真实状态**）─────────────
    fs.rmSync(dst.receipts, { force: true });
    fs.writeFileSync(dst.doc, pristineDoc);
    expectClean('B0 仓库真实状态（收据丢失 + 全表未测）零误报',
      audit(tmp, true), '这正是 npm run check:stt-refine-receipts 今天会走的分支');

    // B1：把某一格改回裸数字（= 重新冒充「已核验」）必须报红
    {
      const bad = pristineDoc.replace(/(\| §378\.3 claude×术语表 \|)[^|\n]*\|\s*2 未测/, '$1 10 | 2 |');
      if (bad === pristineDoc) {
        console.log('META  B1 把一格改回裸数字\n      ⛔ 没能注入变异（文档结构与预期不符）');
        broken++;
      } else {
        fs.writeFileSync(dst.doc, bad);
        const res = audit(tmp, true);
        expectRed('B1 把一格改回裸数字（重新冒充「已核验」）', res, fmt(res),
          '§384.2 的无据数字必须报红 —— 这正是已经发生过的那次失效');
      }
    }

    // B2：清空 WAIVERS（= 悄悄把「凭据丢失」这个责任注销掉）
    fs.writeFileSync(dst.doc, pristineDoc);
    const snapshot = WAIVERS.splice(0, WAIVERS.length);
    const resB2 = audit(tmp, true);
    WAIVERS.push(...snapshot);
    expectRed('B2 清空 WAIVERS（注销 evidence-lost 登记）', resB2, fmt(resB2),
      '豁免表被清空时闸必须报红，否则「登记」可以悄悄消失');
    if (snapshot.length !== 1) {
      console.log(`META  ⛔ WAIVERS 应恰好 1 条，实测 ${snapshot.length}`);
      broken++;
    }

    // B3：豁免锚点消失（有人回改了 §384.2 小节标题）
    fs.writeFileSync(dst.doc, pristineDoc.replace(/### 384\.2 /, '### 384.2X '));
    const resB3 = audit(tmp, true);
    fs.writeFileSync(dst.doc, pristineDoc);
    expectRed('B3 豁免锚点小节消失', resB3, fmt(resB3),
      '锚点消失 = 有人回改了原文，豁免必须重新审视');
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }

  console.log(`\n=== 判据自证 ===\n${alive} 项通过，${broken} 项未通过`);
  if (broken > 0) {
    console.log('闸失去区分能力 —— 这是**闸**坏了，不是文档坏了。');
    process.exit(2);
  }
  console.log('闸有牙，且不恒红（收据在 / 收据丢失 两个分支都自证过）。');
}

// ---------------------------------------------------------------- main

const arg = process.argv[2];
if (arg === '--meta') {
  meta();
} else {
  const { findings, mode } = audit();
  console.log('\n=== 汇总 ===');
  if (findings.length === 0) {
    if (mode === 'receipts-lost') {
      console.log('PASS  §384.2 已如实标注「未测」，且凭据丢失有登记');
      console.log('      ⚠ 本门此刻**不**核验那 25 格的数值（收据已永久丢失）。');
      console.log('        它核验的是「没有人拿无据的数字冒充已核验」—— 这正是');
      console.log('        2026-10 之前已经发生过、却因为表格一直在输出而无人怀疑的那次失效。');
    } else {
      console.log('PASS  §384.2 对账表与逐次收据逐格一致');
    }
    process.exit(0);
  }
  for (const f of findings) console.log(`FAIL  ${f.kind}: ${f.msg}`);
  console.log(`\n共 ${findings.length} 处不一致`);
  process.exit(1);
}
