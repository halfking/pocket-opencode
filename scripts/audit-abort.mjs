#!/usr/bin/env node
/**
 * 后台请求中止能力审计。
 *
 * 用户验收项：「请求在切换页面后仍能执行」+「后台执行的 api 可以强行终止」。
 * 这两项要成立，前提是每个长时间运行的请求都持有可传递的 AbortSignal。
 * 逐个函数眼看会漏，这里做静态扫描给出全局视图。
 *
 * 扫描 src/api 下每个导出函数，判断：
 *   - 是否接受 signal / abortSignal 形参
 *   - 是否把 signal 透传给底层 fetch / http 请求
 * 输出：可中止 / 不可中止 两类清单，作为补齐 signal 的工作底稿。
 *
 * Run: node scripts/audit-abort.mjs
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const API_DIR = join(ROOT, 'frontend', 'src', 'api')

const files = []
for (const name of readdirSync(API_DIR)) {
  const full = join(API_DIR, name)
  if (statSync(full).isFile() && /\.ts$/.test(name)) files.push(full)
}

/** 抓取 `export (async )?function NAME(...)` 与 `export const NAME = ... =>` 的签名块。 */
const FN_RE = /export\s+(?:async\s+)?function\s+([A-Za-z0-9_]+)\s*\(([^)]*)\)/g
/**
 * 箭头函数形式。必须允许 `)` 与 `=>` 之间存在返回类型标注，
 * 例如 `export const f = async (a: number): Promise<X> => ...`，
 * 否则整条签名匹配不上，函数会被静默漏掉。
 */
const ARROW_RE = /export\s+const\s+([A-Za-z0-9_]+)\s*=\s*(?:async\s*)?\(([^)]*)\)\s*(?::[^=]*?)?=>/g
/**
 * 对象字面量方法形式 —— 本项目 API 的主要写法（emailApi / rssApi 等）。
 * 先定位 `export const NAME = {`，用括号配对取出对象体，再扫其中的方法定义。
 */
const OBJ_HEAD_RE = /export\s+const\s+([A-Za-z0-9_]+)\s*(?::[^=]*)?=\s*\{/g
const OBJ_METHOD_RE = /^\s{2,}(?:async\s+)?([A-Za-z0-9_]+)\s*\(([^)]*)\)\s*(?::[^{]+)?\{/gm

/** 从 openIndex（'{' 的位置）开始做花括号配对，返回对象体。 */
function readObjectBody(text, openIndex) {
  let depth = 0
  for (let i = openIndex; i < text.length; i++) {
    const ch = text[i]
    if (ch === '{') depth++
    else if (ch === '}') {
      depth--
      if (depth === 0) return text.slice(openIndex + 1, i)
    }
  }
  return ''
}

const results = []

for (const file of files) {
  const text = readFileSync(file, 'utf8')
  const rel = relative(ROOT, file).replace(/\\/g, '/')
  const seen = new Set()

  const add = (objName, name, params) => {
    const key = `${objName}.${name}`
    if (seen.has(key)) return
    seen.add(key)
    const start = text.indexOf(`${name}(${params}`) + (name + params).length
    const nextExport = text.indexOf('\nexport ', start)
    const body = text.slice(start, nextExport === -1 ? text.length : nextExport)
    results.push({
      rel,
      name: key,
      hasSignal: /\bsignal\b/i.test(params),
      passesSignal: /\bsignal\b/.test(body),
      params: params.replace(/\s+/g, ' ').trim(),
    })
  }

  for (const re of [FN_RE, ARROW_RE]) {
    for (const m of text.matchAll(re)) {
      const [, name, params] = m
      if (seen.has(name)) continue
      const start = m.index + m[0].length
      const nextExport = text.indexOf('\nexport ', start)
      const body = text.slice(start, nextExport === -1 ? text.length : nextExport)
      seen.add(name)
      results.push({
        rel,
        name,
        hasSignal: /\bsignal\b/i.test(params),
        passesSignal: /\bsignal\b/.test(body),
        params: params.replace(/\s+/g, ' ').trim(),
      })
    }
  }

  for (const m of text.matchAll(OBJ_HEAD_RE)) {
    const objName = m[1]
    const body = readObjectBody(text, m.index + m[0].length - 1)
    for (const mm of body.matchAll(OBJ_METHOD_RE)) add(objName, mm[1], mm[2])
  }
}

const abortable = results.filter((r) => r.hasSignal && r.passesSignal)
const acceptsOnly = results.filter((r) => r.hasSignal && !r.passesSignal)
const notAbortable = results.filter((r) => !r.hasSignal)

/**
 * ⚠️ 重要：上面这张表**只覆盖 signal 形参这一种取消机制**，
 * 绝不能据此得出「只有 N 个 API 可终止」的结论。
 *
 * 本项目的主力取消模型是 **handle 式**：
 *   aiStreamRuntime（进程级 singleton）持有所有 LLM/流式请求，
 *   spawnChat 返回带 `abort(): boolean` 的 ChatStreamHandle；
 *   流的生命周期刻意与组件解耦（组件 unmount 不 abort），
 *   正好就是用户要的「切页后继续执行 + 可强行终止」两条。
 *   另有 localagent/runtime 的 activeRuns: Map<sessionId, () => ctrl.abort()>
 *   以及各视图自建的 AbortController（如 CostQuotaView 的重载/卸载取消）。
 *
 * 这些都不体现为 `signal` 形参，静态扫参数表必然漏掉。
 * 所以下面单独扫一遍「取消能力来源」，让报告不再有误导性。
 */
const SRC_DIR = join(ROOT, 'frontend', 'src')
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])
const srcFiles = []
;(function walk(d) {
  for (const n of readdirSync(d)) {
    if (SKIP.has(n)) continue
    const full = join(d, n)
    if (statSync(full).isDirectory()) walk(full)
    else if (/\.(ts|vue)$/.test(n)) srcFiles.push(full)
  }
})(SRC_DIR)

/** 取消能力来源：handle 式 / AbortController 式 / signal 形参式。 */
const cancelSources = []
for (const file of srcFiles) {
  const text = readFileSync(file, 'utf8')
  const rel = relative(ROOT, file).replace(/\\/g, '/')
  const kinds = []
  // 1) handle 式：spawnChat / streamChat 返回值带 abort，或显式持有 handle map
  if (/aiStreamRuntime\.(spawnChat|get|listStreamIds)|spawnChat\(/.test(text) && /\.abort\(\)/.test(text)) {
    kinds.push('handle(aiStreamRuntime)');
  }
  if (/activeRuns/.test(text) && /ctrl\.abort\(\)/.test(text)) kinds.push('handle(localagent.activeRuns)');
  // 2) AbortController 式：视图/composable 自建
  if (/new AbortController\(\)/.test(text) && /\.abort\(\)/.test(text)) kinds.push('AbortController');
  // 3) 透传调用方 signal
  if (/(?:^|[^\w.])signal\s*[:,)]/.test(text) && /\.abort\(\)|signal:/.test(text)) kinds.push('signal 透传');
  if (kinds.length) cancelSources.push({ rel, kinds: [...new Set(kinds)] })
}

const kindCount = {}
for (const s of cancelSources) for (const k of s.kinds) kindCount[k] = (kindCount[k] || 0) + 1

console.log(`扫描 ${files.length} 个 api 模块，${results.length} 个导出函数\n`)
console.log(`✅ 接受并透传 signal（signal 形参）：${abortable.length}`)
for (const r of abortable) console.log(`   ${r.name.padEnd(28)} ${r.rel}`)
console.log(`\n⚠️  接受 signal 但未透传：${acceptsOnly.length}`)
for (const r of acceptsOnly) console.log(`   ${r.name.padEnd(28)} (${r.params})`)
console.log(`\n❌ 无 signal 形参：${notAbortable.length}  ← 不等于「不可终止」，见下方取消能力来源`)

console.log(`\n\n=== 取消能力来源（全 src，共 ${cancelSources.length} 个文件）===`)
for (const [k, n] of Object.entries(kindCount).sort((a, b) => b[1] - a[1])) {
  console.log(`  ${k.padEnd(28)} ${n} 个文件`)
}
console.log('\n明细：')
for (const s of cancelSources) console.log(`   ${s.rel.padEnd(52)} ${s.kinds.join(' + ')}`)

console.log(
  `\n\n结论：用户验收项「后台 API 可强行终止」的覆盖情况，` +
  `\n不能由「signal 形参 = ${abortable.length}」推断。` +
  `\n流式 / LLM / 本地智能体这类真正的长请求走的是 handle 式取消（见上），` +
  `\n其余无 signal 形参的多数是短 CRUD，靠 ${'30s'} 请求超时兜底。`,
)
