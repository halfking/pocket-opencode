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
// 判据自证（--meta）：对每一类都在内存里造一个带该缺陷的样本，
// 确认检测器**确实会报**；同时确认干净样本**不会误报**。
// 检测器若失去区分能力，--meta 直接失败 —— 而不是安静地放行。
//
// 退出码：0 = 干净；1 = 发现问题；2 = 判据自证失败（检测器坏了，不是文档坏了）。

import fs from 'node:fs';
import path from 'node:path';
import { execSync } from 'node:child_process';

const ROOT = path.resolve(process.cwd());

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
    console.error('FATAL: 没找到任何 .md 目标，判据失效');
    process.exit(2);
  }

  let total = 0;
  for (const f of files) {
    const rel = path.relative(ROOT, f);
    const text = fs.readFileSync(f, 'utf8');
    const findings = detect(text);
    if (findings.length === 0) {
      console.log(`PASS  ${rel}`);
      continue;
    }
    total += findings.length;
    console.log(`FAIL  ${rel}  (${findings.length})`);
    for (const x of findings) {
      console.log(`        ${x.line}:${x.col}  ${x.kind}  |  ${x.snippet}`);
    }
  }

  console.log('\n=== 汇总 ===');
  console.log(`扫描 ${files.length} 个 .md，发现 ${total} 处问题`);
  process.exit(total === 0 ? 0 : 1);
}

main();
