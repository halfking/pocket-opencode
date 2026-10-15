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
// 三条设计约束（每条都对应一次真实失败）：
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
//      ⇒ --meta 造四个变异样本，每个都必须报红；闸失去区分能力时 rc=2，
//      **而不是安静放行**。
//
// ⚠ 本闸保护的是「文档表格 ↔ 收据」的一致性，**不是**「日志 ↔ 收据」。
//   /tmp/opstt/377/ 不持久 ⇒ 原始日志已不可复核，收据 JSON 是唯一长久副本。
//   这是本仓 §383.4 已登记的同一限制，不因本闸存在而消失。
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

// §384.3①：§383.0 里那格无据数字仍在文档里（按本仓惯例**保留原样**，
// 用追加式勘误纠正）。这里登记成显式豁免，并要求锚点仍然存在 ——
// 锚点消失（= 有人回改了原文）时豁免**过期**，必须重新审视。
const WAIVERS = [
  {
    // ⚠ 这里**不**记绝对路径：audit() 按 root 读文档，
    //   而 --meta 跑在临时目录的副本上 —— 记死路径会让自证永远命中错文件。
    section: '## 383.',
    mustContain: '| §377.4 重叠对照 | 8 | 0 | 0 | 8/8 | 24/24 |',
    reason: '这格的时间点数字是 8×3 算出来的；overlap.log 的判据行没有时间点列（§384.3①）。' +
      '按追加式勘误惯例**保留原文**，纠正写在 §384.3。锚点一旦消失即视为已回改，需重新审视本条豁免。',
    owner: '§384',
  },
];

// ⚠⚠️ 扫描根必须可被**覆盖**，且 --meta 只在覆盖后的临时目录里跑。
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
const NOT_MEASURED = /未测|无此列|不存在|不可测/;

/** 把文档单元格里 ✅/❌ 与强调标记剥掉，留下可比对的正文。 */
function cleanCell(raw) {
  return raw.replace(/[`*]/g, '').replace(/[✅❌]/g, '').trim();
}

/**
 * 抽出 §384.2 的对账表：`| 实验 | n | 专名修好 | CI修好 | 奥古斯都保住 | 时间点 |`
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

function audit(root = SCAN_ROOT, quiet = false) {
  const { receipts: RECEIPTS, doc: DOC } = paths(root);
  const findings = [];
  const receipts = JSON.parse(fs.readFileSync(RECEIPTS, 'utf8'));
  const doc = fs.readFileSync(DOC, 'utf8');

  // 先过豁免：锚点必须在，且必须真的命中源码/文档（空头支票检查）
  for (const w of WAIVERS) {
    const text = doc;
    if (!w.reason || !w.reason.trim()) {
      findings.push({ kind: '豁免表', msg: `${w.owner} 的豁免没有理由` });
      continue;
    }
    if (!text.includes(w.section)) {
      findings.push({ kind: '豁免过期', msg: `${w.owner}：锚点小节 ${w.section} 已消失` });
      continue;
    }
    if (!text.includes(w.mustContain)) {
      findings.push({
        kind: '豁免过期',
        msg: `${w.owner}：${w.section} 里已找不到 ${JSON.stringify(w.mustContain.slice(0, 40))}…`,
      });
      continue;
    }
    if (!quiet) console.log(`WAIVED  ${w.owner}  ${w.section} 的历史无据格（保留原文，勘误见 §384.3①）`);
  }

  const parsed = parseDocTable(doc);
  if (parsed.error) {
    findings.push({ kind: '结构', msg: parsed.error });
    return { findings, receipts: null };
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
  return { findings, receipts };
}

// ---------------------------------------------------------------- --meta
// 判据自证：每个变异都必须让本闸报红。闸若对变异无反应 ⇒ rc=2。

const MUTATIONS = [
  {
    name: 'M1 篡改一条逐次读数（augustus_kept true→false）',
    run: (r) => {
      const c = r.experiments['377.4'].calls[0];
      c.augustus_kept = !c.augustus_kept;
    },
    expect: '377.4 的「奥古斯都」格会与收据不符',
  },
  {
    name: 'M2 把「未测」的时间点伪造成 0/0（复现 §384.3① 的错法）',
    run: (r) => {
      for (const e of Object.values(r.experiments)) {
        for (const c of e.calls) {
          if (c.time_total === null) {
            c.time_total = 0;
            c.time_kept = 0;
          }
        }
      }
    },
    expect: '收据声称测过、文档说未测 → 时间点格报红',
  },
  {
    name: 'M3 删掉一个实验的全部调用（模拟分母写错）',
    run: (r) => {
      r.experiments['378.2'].calls = r.experiments['378.2'].calls.slice(0, 3);
      r.experiments['378.2'].n_calls = 3;
    },
    expect: '378.2 的 n/奥古斯都/时间点三格与行 n 合计都会报红',
  },
  {
    name: 'M4 文档那格改成无据数字（把「未测」写成 8×3）',
    run: null, // 直接改文档，不动收据
    expect: '§384.2 的 377.4 时间点格报「无据数字」',
  },
];

function meta() {
  // ⭐⭐ 全部变异都发生在**临时目录的副本**上，仓库里那两个文件一次都不被写。
  //   首版 --meta 直接改真实文档再靠 finally 还原 —— 那是定时炸弹：
  //   进程被 kill / 断电 ⇒ 2.8 MB 设计文档停在「被改成 24/24」的中间态，
  //   而那正是本节要防的那类事故。
  const src = paths(ROOT);
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'stt-refine-receipts-meta-'));
  const dst = paths(tmp);
  fs.mkdirSync(path.dirname(dst.receipts), { recursive: true });
  fs.mkdirSync(path.dirname(dst.doc), { recursive: true });
  fs.copyFileSync(src.receipts, dst.receipts);
  fs.copyFileSync(src.doc, dst.doc);
  const pristineReceipts = fs.readFileSync(dst.receipts, 'utf8');
  const pristineDoc = fs.readFileSync(dst.doc, 'utf8');
  console.log(`隔离副本目录 ${tmp}（模式=fixture；仓库文件零写入）\n`);

  let alive = 0;
  let broken = 0;

  try {
    for (const m of MUTATIONS) {
      let caught = false;
      let why = '';
      try {
        if (m.run === null) {
          // M4：改文档那格
          const bad = pristineDoc.replace(
            /(\| §377\.4 重叠对照 \|[^\n]*?)(❌ \*\*日志无此列\*\*)/,
            '$124/24 ❌',
          );
          if (bad === pristineDoc) {
            console.log(`META  ${m.name}\n      ⛔ 没能注入变异（文档结构与预期不符）`);
            broken++;
            continue;
          }
          fs.writeFileSync(dst.doc, bad);
          const res = audit(tmp, true);
          caught = res.findings.some((f) => f.kind === '无据数字');
          why = res.findings.filter((f) => f.kind === '无据数字').map((f) => f.msg).join(' | ');
        } else {
          const receipts = JSON.parse(pristineReceipts);
          m.run(receipts);
          fs.writeFileSync(dst.receipts, JSON.stringify(receipts, null, 1) + '\n');
          const res = audit(tmp, true);
          caught = res.findings.length > 0;
          why = res.findings.map((f) => `${f.kind}: ${f.msg}`).join(' | ');
        }
      } finally {
        fs.writeFileSync(dst.receipts, pristineReceipts);
        fs.writeFileSync(dst.doc, pristineDoc);
      }
      console.log(`META  ${m.name}\n      ${caught ? '✅ 报红' : '❌ 未报红 —— 闸没有牙'}  预期：${m.expect}`);
      if (caught) {
        alive++;
        if (why) console.log(`      捕获：${why.slice(0, 200)}`);
      } else broken++;
    }

    // 阴性对照：干净状态下必须**不**报红，否则这道闸是恒红的
    const clean = audit(tmp, true);
    const quiet = clean.findings.length === 0;
    console.log(`META  N0 干净文档零误报\n      ${quiet ? '✅ 未报红' : '❌ 干净状态也报红了 —— 闸恒红'}`);
    if (!quiet) {
      broken++;
      for (const f of clean.findings) console.log(`      误报：${f.kind}: ${f.msg}`);
    }
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }

  console.log(`\n=== 判据自证 ===\n${alive} 个变异被捕获，${broken} 个未捕获`);
  if (broken > 0) {
    console.log('闸失去区分能力 —— 这是**闸**坏了，不是文档坏了。');
    process.exit(2);
  }
  console.log('闸有牙，且不恒红。');
}

// ---------------------------------------------------------------- main

const arg = process.argv[2];
if (arg === '--meta') {
  meta();
} else {
  const { findings } = audit();
  console.log('\n=== 汇总 ===');
  if (findings.length === 0) {
    console.log('PASS  §384.2 对账表与逐次收据逐格一致');
    process.exit(0);
  }
  for (const f of findings) console.log(`FAIL  ${f.kind}: ${f.msg}`);
  console.log(`\n共 ${findings.length} 处不一致`);
  process.exit(1);
}