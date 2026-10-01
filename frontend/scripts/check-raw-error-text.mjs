// check-raw-error-text.mjs — 「原始错误上屏」卡口：把后端/JS 的原始 message
// 直接显示给用户的写法。
//
// 为什么要有它：api/error-message.ts 的文件头记录了这个缺陷的真机证据——
// Redmi 14R 5G 上 RSS 页把 `rss_unavailable: store not configured` 直接渲染给
// 用户。于是写了 toUserMessage / apiError 这一套映射，并逐步接进各个视图。
// 但「逐步接入」没有完成：截至本卡口建立时，绝大多数视图的 catch 仍然是
//     someError.value = e instanceof Error ? e.message : String(e)
// 这行——它把「后端说了什么」当成「用户需要看到什么」，于是屏幕上出现
// `Failed to fetch`、`ECONNREFUSED`、英文技术码。
//
// 一次改完 20 个视图、几十处属于另一个 PR 的事，而且没有真机就不能确认改对了。
// 因此本卡口是**棘轮**：把存量钉成基线，只许减少不许增加。
//
// ── 沿用 check-dead-api.mjs 踩过的坑 ──
// 1) 计数必须在**剥掉注释**的源码上做。views 里的注释大量引用 e.message 作为
//    「原来是什么样」的例证（LoginView/RssListView/VaultListView…），不剥注释
//    会把它们全算成违规。
// 2) console.* 里的 e.message 是**正确用法**（留档原始信息），必须排除。
// 3) 判据要窄。`e.message.includes('LocalDB 未初始化')` 这类是把消息当**判据**
//    用而不是上屏，放行；否则基线第一天就是错的。
import { readFileSync, readdirSync, statSync, existsSync, writeFileSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const srcRoot = join(here, '..', 'src')
const BASELINE = join(here, 'raw-error-baseline.json')

function walk(dir, out = []) {
  for (const n of readdirSync(dir)) {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (['.ts', '.vue', '.js', '.mjs'].includes(p.slice(p.lastIndexOf('.')))) out.push(p)
  }
  return out
}

function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}

const isTest = (p) => p.includes('__tests__') || /\.(test|spec)\.[tj]s$/.test(p)

// 上屏的落点：模板插值、响应式 ref、toast、状态条。
// 只认这些容器，避免把「把 message 当判据用」误伤。
const SINK = /({{\s*[^}]*\b(?:error|err|msg|message|status|text)\b[^}]*}}|toast\.(?:error|success|warn|info)\(|\.value\s*=|setStatus\(|setResult\()/
// 原始错误的取法
const RAW = /\b(?:e|err|ex|error|exception)\s*\.\s*(?:message|body|statusText)\b|String\(\s*(?:e|err|ex)\s*\)/
// 已经是修好的写法
const MAPPED = /\b(?:apiError|toUserMessage|sttFailureText|errorText)\s*\(/

/**
 * 判断这一行是否真的把原始消息**送进了上屏容器**。
 *
 * 第一版只匹配 SINK 与 RAW 的共现，抓出了假阳性：
 *   if (e instanceof Error && e.message.includes('LocalDB 未初始化')) dbNotReady.value = true
 * 这里 `.value =` 命中了 SINK、`.message` 命中了 RAW，但消息是**判据**，
 * 赋给 dbNotReady 的是 `true`，屏幕上什么都不会出现。
 * ⇒ 对 `.value =` 形式的行，必须要求 RAW 出现在 `=` **右侧**。
 *   toast.error(...) / 模板插值则是整段实参都要看，不做此限制。
 */
function actuallyDisplays(line) {
  if (/console\./.test(line)) return false
  if (MAPPED.test(line)) return false
  const assignAt = line.search(/\.value\s*=/)
  if (assignAt >= 0) return RAW.test(line.slice(assignAt))
  return SINK.test(line) && RAW.test(line)
}

const findings = []
for (const p of walk(srcRoot)) {
  if (isTest(p)) continue
  const rel = relative(srcRoot, p).replace(/\\/g, '/')
  const rawLines = readFileSync(p, 'utf8').split('\n')
  const lines = stripComments(rawLines.join('\n')).split('\n')
  lines.forEach((line, i) => {
    // 豁免注解必须在**未剥注释**的原始行上判断——注释本身会被剥掉。
    // 用途：这一处的 message 是我们自己定义的错误类（如 DownloadUnsupportedError），
    // 文案是给人看的，不属于「后端原文直出」。
    if (/raw-error-ok/.test(rawLines[i] || '')) return
    if (!actuallyDisplays(line)) return
    findings.push({ file: rel, line: i + 1, code: line.trim().replace(/\s+/g, ' ') })
  })
}

const byFile = {}
for (const f of findings) (byFile[f.file] ||= []).push(f)

console.log(`【原始错误上屏卡口】扫描 ${walk(srcRoot).length} 个源文件，命中 ${findings.length} 处 / ${Object.keys(byFile).length} 个文件\n`)
for (const [f, list] of Object.entries(byFile).sort()) {
  console.log(`  ${f}  (${list.length})`)
  for (const x of list) console.log(`      :${x.line}  ${x.code.slice(0, 96)}`)
}

// ---- 棘轮 ----
let baseline = { note: '把后端/JS 原始 message 直接上屏的存量；只许减少不许增加。', sites: [] }
if (existsSync(BASELINE)) {
  try {
    baseline = JSON.parse(readFileSync(BASELINE, 'utf8'))
  } catch (e) {
    console.error(`\n❌ 基线解析失败：${BASELINE}\n   ${e.message}`)
    process.exit(2)
  }
}
const cur = findings.map((f) => `${f.file}:${f.line}`).sort()

if (process.argv.includes('--update-baseline')) {
  writeFileSync(BASELINE, JSON.stringify({ ...baseline, sites: cur }, null, 2) + '\n', 'utf8')
  console.log(`\n✅ 基线已更新（${cur.length} 条）`)
  process.exit(0)
}

const baseSet = new Set(baseline.sites || [])
const added = cur.filter((x) => !baseSet.has(x))
const removed = (baseline.sites || []).filter((x) => !cur.includes(x))

if (added.length) {
  console.error()
  for (const a of added) {
    const [file, ln] = a.split(':')
    const hit = findings.find((f) => `${f.file}:${f.line}` === a)
    console.error(`❌ 新增「原始错误上屏」：${a}`)
    console.error(`     ${hit ? hit.code : ''}`)
  }
  console.error()
  console.error('改法：把 `e instanceof Error ? e.message : String(e)` 换成已存在的映射，')
  console.error('      优先复用 composables/useApiError.ts 的 apiError(e, 兜底文案)，')
  console.error('      或 api/error-message.ts 的 toUserMessage(e, t, 兜底文案)。')
  console.error('若这一处确实该原样显示（如我们自己定义的 DownloadUnsupportedError），')
  console.error('请在同行加 // raw-error-ok: 原因，并跑 --update-baseline 落盘。')
  process.exit(1)
}
if (removed.length) {
  console.log()
  for (const r of removed) console.log(`⤵️ 已清掉：${r} —— 确认后跑 --update-baseline 落盘。`)
}
console.log(`\n✅ 原始错误上屏未新增（棘轮通过，基线 ${baseSet.size} 条）`)
process.exit(0)
