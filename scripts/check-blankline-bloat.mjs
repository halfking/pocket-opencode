// check-blankline-bloat.mjs —— 检测「每行之间插一个空行」这种 gofmt 看不见的格式债
//
// ## 为什么需要这道门禁（2026-10-04 实测）
//
// 提交 231780e1（修 SQL 空串字面量 `''` 被写成 ”）在 7 个 .go 文件里
// **把每一行后面都插了一个空行**：文件行数几乎翻倍（fetcher.go 1274 -> 2422，
// store.go 2599 -> 5563），而 gofmt -l 对此**完全沉默**。
//
// 沉默的原因不是 gofmt 宽容，而是它**真的认为那样是对的**：空行把结构体字段
// 的对齐组切断了，于是 `store  *Store` 被判定为「每组只有一个字段、无需对齐」，
// 塌成 `store *Store` 也合法。⇒ 插入空行 = 破坏对齐组 + 体积翻倍 + 后续每次
// diff 都带上一堆无关空行，而 check-gofmt.mjs 报「0 真债」。
//
// 这个门禁问的是一个 gofmt 不问的问题：**这个文件的空行比例合理吗**。
//
// ## 判据
//
// 阈值 35%：本仓 980 个 .go/.sql 文件里，正常最高约 12%；被污染的 7 个是
// 49%~55%。35% 落在两者之间，且远低于任何合理的 Go 排版上限。
// 同时要求行数 >= 80，避免拿小文件的比例做统计（3 行文件 1 个空行 = 33%）。

import fs from 'node:fs'
import path from 'node:path'

const REPO = path.resolve(import.meta.dirname, '..')
const ROOTS = ['backend']
const RATIO_LIMIT = 0.35
const MIN_LINES = 80
const EXT = new Set(['.go', '.sql'])

function walk(dir, acc = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (e.name === 'vendor' || e.name === 'node_modules') continue
    const p = path.join(dir, e.name)
    if (e.isDirectory()) walk(p, acc)
    else if (EXT.has(path.extname(e.name))) acc.push(p)
  }
  return acc
}

const files = ROOTS.flatMap((r) => walk(path.join(REPO, r)))
const flagged = []
for (const f of files) {
  // 按 git 对象层的真相读：工作区可能是 CRLF，但空行比例与行尾无关。
  const text = fs.readFileSync(f, 'utf8').replace(/\r\n/g, '\n')
  const lines = text.split('\n')
  if (lines.length < MIN_LINES) continue
  const blank = lines.filter((l) => l.trim() === '').length
  const ratio = blank / lines.length
  if (ratio > RATIO_LIMIT) {
    flagged.push({ f: path.relative(REPO, f).split(path.sep).join('/'), lines: lines.length, blank, ratio })
  }
}

console.log(`· 扫描 .go/.sql        : ${files.length}`)
console.log(`· 空行比例 > ${(RATIO_LIMIT * 100).toFixed(0)}% 且行数 >= ${MIN_LINES} : ${flagged.length}`)

if (flagged.length) {
  console.error('\n✗ 疑似「每行之间插空行」污染（gofmt 对此不报红）：\n')
  for (const x of flagged.sort((a, b) => b.ratio - a.ratio)) {
    console.error(`  ${x.f}  行数=${x.lines} 空行=${x.blank} 比例=${(x.ratio * 100).toFixed(0)}%`)
  }
  console.error('\n  这类污染会切断 gofmt 的对齐组，使 check:gofmt 误报「0 真债」。')
  console.error('  修法：确认语义未变后，删掉多余空行再 gofmt（gofmt 会自动补回对齐）。')
  console.error('  判语义未变：逐行把连续空白折叠成单空格后，与上一版本比对，应完全一致。')
  process.exit(1)
}
console.log('\n✓ 没有空行膨胀文件')