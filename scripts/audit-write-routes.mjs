#!/usr/bin/env node
/**
 * audit-write-routes.mjs — 前端写路径 vs 后端路由的**静态前缀**对账。
 *
 * 抓的是「路径前缀压根没注册」那一类缺陷。BUG-L（POST /api/flashcards/notes
 * 恒 405）**不在本脚本能力范围内** —— 它的前缀 /api/flashcards/ 是注册了的，
 * 栽在 handler 内部按 method 拒绝。method 级判定见 probe-write-methods.mjs。
 *
 * 用法：node scripts/audit-write-routes.mjs [--all]
 * 退出码：0 = 无候选；1 = 有候选；2 = 提取器自身坏了（不是"通过"）。
 */
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { extractWritePaths } from './lib/extract-write-paths.mjs'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SRC = join(ROOT, 'frontend/src')
const SERVER_GO = join(ROOT, 'backend/internal/server/server.go')
const SHOW_ALL = process.argv.includes('--all')

const findings = extractWritePaths(SRC)

// ---- 后端路由表 ----
const serverGo = readFileSync(SERVER_GO, 'utf8')
const routes = new Set()
for (const re of [/mux\.HandleFunc\(\s*"([^"]+)"/g, /mux\.Handle\(\s*"([^"]+)"/g]) {
  let r
  while ((r = re.exec(serverGo)) !== null) routes.add(r[1])
}

/** 前缀匹配：/api/x/y 命中已注册的 /api/x/（Go ServeMux 语义）。 */
function matches(pattern) {
  const segs = pattern.split('/').filter(Boolean)
  for (let n = segs.length; n >= 0; n--) {
    const prefix = '/' + segs.slice(0, n).join('/')
    if (routes.has(prefix) || routes.has(prefix === '/' ? '' : `${prefix}/`)) return true
  }
  return false
}

const unmatched = findings.filter((f) => !matches(f.pattern))

console.log(`scanned: frontend/src (${findings.length} write calls parsed)`)
console.log(`backend mux routes: ${routes.size}`)
console.log('')

if (findings.length === 0) {
  console.error('FATAL: 0 write calls parsed — the extractor is broken, NOT a clean bill of health.')
  process.exit(2)
}

if (SHOW_ALL) {
  for (const f of findings) {
    console.log(`  ${matches(f.pattern) ? 'ok  ' : 'MISS'} ${f.method.padEnd(6)} ${f.pattern.padEnd(46)} ${f.file}:${f.line}`)
  }
  console.log('')
}

if (unmatched.length === 0) {
  console.log('OK: every parsed frontend write path maps to a registered backend route prefix.')
  console.log('REMINDER: prefix match cannot see method-level rejections — run probe-write-methods.mjs.')
} else {
  console.log(`CANDIDATES (${unmatched.length}) — 人工复核:`)
  for (const f of unmatched) {
    const hint = f.unresolved ? '  [首段是未知变量，可能误报]' : ''
    console.log(`  ${f.method.padEnd(6)} ${f.pattern.padEnd(46)} ${f.file}:${f.line}${hint}`)
  }
  process.exitCode = 1
}
