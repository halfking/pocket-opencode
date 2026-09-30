#!/usr/bin/env node
/**
 * audit-apierror-keys.mjs — 核对**所有** `apiError(x, 'errors.Y')` 调用点的键，
 * 在 9 个语言里是否都存在。
 *
 * ## 为什么需要它
 *
 * `useApiError` 有两种调用约定，历史上混用过：
 *   - 传 **key**：`apiError(e, 'errors.saveFailed')` —— 主流（80+ 处）
 *   - 传**已翻译字符串**：`apiError(e, t('flashcards.error.saveFailed'))` —— 少数
 *
 * 合并两条分支时，这两种约定同时存在于 `useApiError` 的两个候选实现里。
 * 选错一边不会报编译错，只会在**运行时把文案渲染成原始 key**（vue-i18n 对
 * 未知 key 返回 key 本身），属于静默失效 —— 正是最难发现的那一类。
 *
 * 所以本脚本扫**调用点字面量**：
 *   1. 出现「传 t(...)」的调用点 → 报告（约定不一致）
 *   2. 出现「传 'errors.X'」的调用点 → 核对 X 在 9 语言里都存在
 *
 * 判据必须能区分：第 1 条要能在「0 处不一致」时返回 0 失败，
 * 第 2 条要能真的报出缺键 —— 否则恒返回 OK 和真正有效在报告上一样。
 *
 * 用法：node scripts/audit-apierror-keys.mjs
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

const ROOT = 'frontend/src'
const LOCALES = join(ROOT, 'locales')

/** 递归收集源文件（跳过 node_modules / dist） */
function walk(dir, out = []) {
  for (const e of readdirSync(dir)) {
    if (e === 'node_modules' || e === 'dist') continue
    const p = join(dir, e)
    const st = statSync(p)
    if (st.isDirectory()) walk(p, out)
    else if (/\.(ts|vue)$/.test(e)) out.push(p)
  }
  return out
}

const files = walk(ROOT)

// 载入 9 语言
const locales = {}
for (const f of readdirSync(LOCALES).filter((x) => x.endsWith('.json'))) {
  locales[f.replace(/\.json$/, '')] = JSON.parse(readFileSync(join(LOCALES, f), 'utf8'))
}
const locNames = Object.keys(locales)
const has = (obj, path) => path.split('.').reduce((a, k) => (a && typeof a === 'object' ? a[k] : undefined), obj) !== undefined

// 扫调用点
const literalKeys = new Map() // key -> [file:line]
const translatedCallSites = []
for (const f of files) {
  const src = readFileSync(f, 'utf8')
  src.split('\n').forEach((line, i) => {
    if (!/apiError\(/.test(line)) return
    // 注释行不算
    const code = line.replace(/^\s*(\*|\/\/).*$/, '')
    if (/apiError\([^,]+,\s*t\(/.test(code)) {
      translatedCallSites.push(`${f}:${i + 1}  ${code.trim()}`)
    }
    const m = code.match(/apiError\([^,]+,\s*'([a-zA-Z0-9_.]+)'/)
    if (m) {
      if (!literalKeys.has(m[1])) literalKeys.set(m[1], [])
      literalKeys.get(m[1]).push(`${f}:${i + 1}`)
    }
  })
}

console.log(`源文件 ${files.length} 个，语言 ${locNames.length} 种`)
console.log(`apiError 字面量 key 调用点: ${[...literalKeys.values()].reduce((a, b) => a + b.length, 0)} 处，${literalKeys.size} 个不同 key`)
console.log(`apiError 传 t(...) 的调用点: ${translatedCallSites.length} 处`)

let fail = 0

console.log('\n=== 检查 1：调用约定是否一致（应全部传 key）===')
if (translatedCallSites.length > 0) {
  fail++
  console.log(`FAIL  ${translatedCallSites.length} 处仍在传已翻译字符串，与 useApiError 的 key 约定不一致：`)
  translatedCallSites.forEach((s) => console.log('    ' + s))
} else {
  console.log('PASS  全部调用点都传 key，约定一致 ✅')
}

console.log('\n=== 检查 2：每个 key 在 9 语言里都存在 ===')
const missing = []
for (const [key, sites] of [...literalKeys.entries()].sort()) {
  const bad = locNames.filter((l) => !has(locales[l], key))
  if (bad.length) {
    missing.push({ key, sites, bad })
    console.log(`FAIL  ${key}  缺失于: ${bad.join(', ')}   (${sites.length} 处调用)`)
  }
}
if (missing.length === 0) {
  console.log(`PASS  ${literalKeys.size} 个 key × ${locNames.length} 语言 = ${literalKeys.size * locNames.length} 次核对，全部存在 ✅`)
} else {
  fail++
  console.log(`\n共 ${missing.length} 个 key 存在缺失`)
}

console.log('\n=== 检查 3：判据自证（能区分通/不通）===')
// 故意查一个一定不存在的 key，确认检测逻辑不是恒返回 OK
const probe = 'errors.__audit_probe_should_not_exist__'
const probeBad = locNames.filter((l) => !has(locales[l], probe))
if (probeBad.length === locNames.length) {
  console.log(`PASS  探针 key 在全部 ${locNames.length} 种语言里都被判为缺失 → 检测逻辑有效 ✅`)
} else {
  fail++
  console.log(`FAIL  探针 key 未被全部判为缺失，检测逻辑失效`)
}

console.log(`\n结论: ${fail === 0 ? '全部通过' : fail + ' 项失败'}`)
process.exit(fail === 0 ? 0 : 1)
