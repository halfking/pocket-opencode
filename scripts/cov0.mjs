// cov0.mjs — 从 coverprofile 里列出 0 覆盖的函数（按未覆盖语句数排序）。
//
// 用法: node cov0.mjs <coverprofile> [topN] [fileFilterSubstring]
//
// coverprofile 每行格式（mode: count）:
//
//   <file>:<startLine>.<startCol>,<endLine>.<endCol> <numStmts> <count>
//
// 末尾是两个独立字段的空格分隔，不是「一个用空格包起来的串」——
// 第一版写成 `split(' ').map(Number)` 再解构，得到的是 [NaN]，
// 于是 count 恒为 NaN，**判据 NaN > 0 恒 false，把全部 3325 个函数
// 报成 0 覆盖**。判据自身坏掉的典型：如果不去核对总数（3325 / 全包），
// 这个输出看起来还挺合理。
//
// Go 的 profile 是**基本块**粒度，不是函数粒度。同一个函数的多个块用
// 相同的函数头行号开头，这里按 (file, 块的首行) 归并成函数级视图。
import { readFileSync } from 'node:fs';

const [, , profilePath, topNArg, fileFilter] = process.argv;
const topN = Number(topNArg || 40);

const raw = readFileSync(profilePath, 'utf8').split(/\r?\n/).filter(Boolean);
const mode = raw[0];
if (!mode.startsWith('mode:')) {
  throw new Error(`unexpected first line: ${mode}`);
}
const body = raw.slice(1);

const fn = new Map();
let totalStmts = 0;
let coveredStmts = 0;

for (const line of body) {
  const parts = line.split(' ');
  if (parts.length < 3) throw new Error(`malformed line: ${line}`);
  const count = Number(parts[parts.length - 1]);
  const numStmts = Number(parts[parts.length - 2]);
  if (!Number.isFinite(count) || !Number.isFinite(numStmts)) {
    throw new Error(`non-numeric fields: ${line}`);
  }
  const loc = parts.slice(0, parts.length - 2).join(' ');
  const [file, range] = loc.split(':');
  const [startLine, startCol] = range.split(',')[0].split('.').map(Number);
  const [endLine] = range.split(',')[1].split('.').map(Number);

  totalStmts += numStmts;
  if (count > 0) coveredStmts += numStmts;

  const key = `${file}|${startLine}|${startCol}`;
  const prev = fn.get(key);
  if (prev) {
    prev.stmts += numStmts;
    prev.covered += count > 0 ? numStmts : 0;
    prev.endLine = Math.max(prev.endLine, endLine);
  } else {
    fn.set(key, {
      file, startLine, endLine, stmts: numStmts,
      covered: count > 0 ? numStmts : 0,
    });
  }
}

// 自检：归并后的语句总数必须等于 profile 的原始总数。
const merged = [...fn.values()];
const mergedStmts = merged.reduce((s, f) => s + f.stmts, 0);
if (mergedStmts !== totalStmts) {
  throw new Error(`merge lost statements: ${mergedStmts} != ${totalStmts}`);
}

let shown = merged;
if (fileFilter) shown = shown.filter((f) => f.file.includes(fileFilter));

const zero = shown
  .filter((f) => f.covered === 0)
  .sort((a, b) => b.stmts - a.stmts || a.file.localeCompare(b.file));

console.log(`mode=${mode}`);
console.log(`statements: ${totalStmts}, covered: ${coveredStmts} (${((coveredStmts / totalStmts) * 100).toFixed(1)}%)`);
console.log(`blocks(merged as fns): ${merged.length}`);
console.log(`zero-covered: ${zero.length} fns / ${zero.reduce((s, f) => s + f.stmts, 0)} stmts`);
if (fileFilter) console.log(`filter=${fileFilter}`);
console.log('');
for (const f of zero.slice(0, topN)) {
  console.log(`${String(f.stmts).padStart(4)}  ${f.file.replace(/^.*internal\//, 'internal/')}:${f.startLine}-${f.endLine}`);
}
