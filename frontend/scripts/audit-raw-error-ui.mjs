#!/usr/bin/env node
/**
 * 扫描「后端/网络原始错误文本直接上屏」的残留写法。
 *
 * 缺陷背景（真机 Redmi 14R 5G 实测，三次独立发现同一类问题）：
 *   1. RSS 页：`rss_unavailable: store not configured` 直接渲染给用户
 *   2. 登录页：网络不通时显示浏览器原文 `Failed to fetch`
 *   3. AI 网关设置页：点「测试连接」显示 `✗ Failed to fetch`
 *      （`setStatus('error', '✗ ' + err.message)` —— 这一处因为写法不同，
 *        被前面那轮 codemod 的模式漏掉了）
 *
 * 同一个缺陷类反复从不同写法里漏出来，说明必须用扫描守住，而不是靠逐个 review。
 * 判定：把 `err.message` / `e.body.error` 之类**原始**异常文本直接赋给
 * 会渲染到界面的变量（error / testResult / status / toast），
 * 且该行没有经过 toUserMessage / apiError 归一。
 *
 * 刻意接受的例外（不算缺陷）：
 *   - console.warn / console.error —— 留档原始信息是我们刻意的行为
 *   - 已经写了 toUserMessage( / apiError( 的行
 *   - .ts 里的纯数据层（错误往上抛，由调用方决定文案）
 *
 * Run: node scripts/audit-raw-error-ui.mjs
 * 退出码 0 = 干净，1 = 有残留
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, extname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

/** 会渲染到界面的容器：赋值后必然出现在模板或 toast 里。 */
const UI_SINKS = /(?:error\.value\s*=|testResult\.value\s*=|status\.value\s*=|\btext\s*:|setStatus\(|toast\.(?:error|warn)\(|showToast\()/
/** 原始异常文本的来源。 */
const RAW_SOURCE = /(?:\b(?:err|e|error|ex|exc)\b\s*\??\s*\.\s*(?:message|body|response)|String\(\s*(?:err|e|error)\s*\))/
/** 已归一的标志。 */
const NORMALIZED = /(?:toUserMessage|apiError|useApiError|resolveErrorI18nKey)/

const hits = []
let scanned = 0
let skippedCurated = 0
let skippedStore = 0

/**
 * 自定义错误类的展示分支不是缺陷。
 * 例：`if (e instanceof DownloadUnsupportedError) { toast.error(e.message) }`
 * 这条 message 是我们自己写给用户看的中文提示，归一反而会把它覆盖掉。
 */
const CURATED_BRANCH = /instanceof\s+(\w+)/
function isCuratedBranch(lines, i) {
  for (let k = Math.max(0, i - 3); k <= i; k++) {
    const m = lines[k].match(CURATED_BRANCH)
    if (m && m[1] !== 'Error') return true
  }
  return false
}

/** store 里 `error.value = e?.message` 是刻意保留原始值。
 *  配套守卫是 audit-store-error-consumers.mjs（它查有没有视图不归一就渲染）。 */
/** store / native runtime 里 `error.value = e?.message` 是刻意保留原始值。
 *  配套守卫是 audit-store-error-consumers.mjs（它查有没有视图不归一就渲染）。 */
function isStoreStash(file, line) {
  // \b 词边界即可：`error.value` 与 `this.error.value` 都要认出来
  if (!/\berror\.value\s*=/.test(line)) return false
  const rel = relative(SRC, file).replace(/\\/g, '/')
  return (
    /^stores\//.test(rel)
    || /(^|\/)store\.ts$/.test(rel)
    || /\/store\/[^/]+\.ts$/.test(rel)
    // native/*Runtime.ts 同属「暂存原始、消费侧归一」：
    // recordingRuntime 转写失败时存原始异常，NoteRecordingStudio 渲染前归一。
    || /^native\/[A-Za-z]*Runtime\.ts$/.test(rel)
  )
}

function walk(dir) {
  for (const name of readdirSync(dir)) {
    if (SKIP.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) { walk(full); continue }
    if (!['.vue', '.ts'].includes(extname(name))) continue
    scanned++
    const text = readFileSync(full, 'utf8')
    const lines = text.split(/\r?\n/)
    lines.forEach((line, i) => {
      if (/^\s*(?:const|let|var)?\s*\/\//.test(line)) return
      if (!UI_SINKS.test(line) || !RAW_SOURCE.test(line)) return
      if (NORMALIZED.test(line)) return
      if (isCuratedBranch(lines, i)) { skippedCurated++; return }
      if (isStoreStash(full, line)) { skippedStore++; return }
      hits.push({
        file: relative(SRC, full).replace(/\\/g, '/'),
        line: i + 1,
        code: line.trim().slice(0, 110),
      })
    })
  }
}

walk(SRC)

console.log(`扫描 ${scanned} 个源文件`)
console.log(`排除：自定义错误展示分支 ${skippedCurated} 处，store 暂存原始值 ${skippedStore} 处（后者由 audit-store-error-consumers.mjs 守）`)
if (!hits.length) {
  console.log('✅ 没有把原始错误文本直接上屏的写法。')
} else {
  console.log(`\n❌ 发现 ${hits.length} 处原始错误文本上屏：\n`)
  for (const h of hits) console.log(`  ${h.file}:${h.line}\n    ${h.code}`)
  console.log('\n修法：接 useApiError()（组件）或 toUserMessage(err, t, fallback)（非组件），')
  console.log('原始信息用 console.warn 留档，不要直接赋给会渲染的变量。')
  process.exitCode = 1
}
