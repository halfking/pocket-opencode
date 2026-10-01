#!/usr/bin/env node
// 冲突解析器：整块保留 ours（HEAD）一侧，丢弃 theirs。
//
// 用途限定在「theirs 一侧在冲突块内是空的」这种情形 —— 此时逐块手工删标记
// 与本脚本等价，但手工漏一处就会把冲突标记带进提交。
//
// 用法：node scripts/resolve-conflicts-ours.mjs <file> [...]
// 非空退出码表示仍残留冲突标记，调用方必须复查。

import { readFileSync, writeFileSync } from 'node:fs'

const START = '<<<<<<<'
const MID = '======='
const END = '>>>>>>>'

let failed = false

for (const path of process.argv.slice(2)) {
  const lines = readFileSync(path, 'utf8').split(/\r?\n/)
  const out = []
  let inConflict = false
  let ours = 0
  let theirs = 0
  let dropped = 0

  for (const line of lines) {
    if (!inConflict && line.startsWith(START)) {
      inConflict = true
      ours = 0
      theirs = 0
      continue
    }
    if (inConflict && line.startsWith(MID)) {
      inConflict = 'theirs'
      continue
    }
    if (inConflict === 'theirs' && line.startsWith(END)) {
      inConflict = false
      continue
    }
    if (inConflict === true) {
      ours++
      out.push(line)
      continue
    }
    if (inConflict === 'theirs') {
      theirs++
      continue
    }
    out.push(line)
  }

  if (inConflict) {
    console.error(`[FAIL] ${path}: 文件中途仍有未闭合的冲突块`)
    failed = true
    continue
  }

  dropped = theirs
  writeFileSync(path, out.join('\n'))

  const remaining = out.filter((l) => l.startsWith(START) || l.startsWith(MID) || l.startsWith(END))
  if (remaining.length) {
    console.error(`[FAIL] ${path}: 仍残留 ${remaining.length} 行冲突标记`)
    failed = true
    continue
  }
  console.log(`[OK] ${path}: 保留 ours ${ours} 行，丢弃 theirs ${dropped} 行`)
}

process.exit(failed ? 1 : 0)
