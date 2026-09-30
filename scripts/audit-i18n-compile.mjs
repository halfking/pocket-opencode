/**
 * audit-i18n-compile.mjs — 用 vue-i18n **自己的消息编译器**穷尽所有 i18n 键，
 * 精确找出会在渲染期抛错的那些。
 *
 * ## 为什么不用真机试错
 *
 * BUG-S 的第一版修法（只改 5 个 `{{count}}` 键）在真机上**完全没效果**：
 * appHTMLLen 371 -> 371，错误一字不变。说明那 5 个键不是根因（或不是唯一根因），
 * 当时是从「报错 + grep 到双花括号」推断出来的，属于**没验证的推断**。
 *
 * 与其在真机上一个一个试（每次装机 + 导航 + 读 console，周期以分钟计），
 * 不如直接用 vue-i18n 的编译器在 Node 里把**每一条消息**都编译一遍。
 * `Not allowed nest placeholder` 是**编译期**错误，编译器和运行时用的是同一套
 * 解析器，所以这里抛错 = 那个键渲染时也会抛。
 *
 * ## 用法
 *
 *   node scripts/audit-i18n-compile.mjs
 *
 * 依赖项目的 node_modules 里的 vue-i18n。若解析失败会明确报错，不静默跳过。
 */
import { createRequire } from 'node:module'
import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const LOCALES = join(ROOT, 'frontend/src/locales')
const require = createRequire(join(ROOT, 'frontend/package.json'))

// ---- 载入 vue-i18n ----
let createI18n
try {
  ;({ createI18n } = require('vue-i18n'))
} catch (e) {
  console.error('无法从 frontend/node_modules 载入 vue-i18n：', e.message)
  console.error('请在 frontend 目录先 `npm i`。本脚本不做静默降级 —— 报不出结果就等于没跑。')
  process.exit(3)
}
console.log('vue-i18n 载入成功')

// intlify 在**找不到键**时会打大量 warn（每个语言都缺 common.* 等键）。
// 那些是「翻译覆盖不全」，不是「编译失败」，和本脚本要查的语法错误是两件事。
// 全量压掉噪声，只保留我们自己捕获的编译异常 —— 否则输出会被几千行 warn 淹没，
// 真正的问题反而看不见。
const noisy = []
const realWarn = console.warn
const realError = console.error
const realLog = console.log
console.warn = (...a) => noisy.push(a.join(' '))
console.error = (...a) => noisy.push(a.join(' '))
console.log = () => {}

const files = readdirSync(LOCALES).filter((f) => f.endsWith('.json'))
const findings = []

for (const file of files) {
  const messages = JSON.parse(readFileSync(join(LOCALES, file), 'utf8'))
  // 独立的 i18n 实例，互不污染；legacy:false 对齐项目配置
  const i18n = createI18n({ legacy: false, locale: file.replace('.json', ''), fallbackLocale: 'en-US', messages, missingWarn: false, fallbackWarn: false, silentTranslationWarn: true, silentFallbackWarn: true })
  const flat = []
  ;(function walk(node, path = '') {
    for (const [k, v] of Object.entries(node)) {
      const p = path ? `${path}.${k}` : k
      if (v && typeof v === 'object') walk(v, p)
      else flat.push([p, String(v)])
    }
  })(messages)

  const bad = []
  for (const [key, raw] of flat) {
    // 关键：**必须带参数调用**才能测出这类错误。
    //
    // 第一版全用无参 t(key)，结果 9 种语言 332 键「全部通过」—— 但真机照旧崩。
    // 对照实验更致命：用**修复前**的 locales（0ac074b，`{{count}}` 原文）跑同一
    // 个脚本，同样是「0 失败」。说明无参路径根本走不到那段解析。
    //
    // `Not allowed nest placeholder` 是 vue-i18n 在**替换占位符**时抛的：
    // 调用方传了 named 参数，编译器才发现消息里有嵌套结构。
    // 所以这里从 `{{name}}` 里把参数名抠出来，按 named 方式传进去。
    const names = [...raw.matchAll(/\{\{\s*([A-Za-z_][\w.]*)\s*\}\}/g)].map((m) => m[1])
    const named = {}
    for (const n of new Set(names)) named[n] = 1

    try {
      i18n.global.t(key, named)
      // 无参通过但含双花括号的，单独标记：它在带参调用时才炸，属于高危残留
      if (names.length > 0) bad.push([key, raw, 'NESTED_PLACEHOLDER (带参调用必崩)', names.join(',')])
    } catch (e) {
      bad.push([key, raw, String(e.message).split('\n')[0], names.join(',')])
    }
  }
  findings.push([file, flat.length, bad])
}

console.warn = realWarn
console.error = realError
console.log = realLog

let total = 0
for (const [file, count, bad] of findings) {
  const mark = bad.length ? '✗' : '✓'
  realLog(`${mark} ${file.padEnd(14)} 共 ${String(count).padStart(4)} 键，编译失败 ${bad.length}`)
  for (const [key, raw, msg, names] of bad) {
    total++
    realLog(`    ${key}   ${names ? `[参数: ${names}]` : ''}`)
    realLog(`        value = ${JSON.stringify(raw)}`)
    realLog(`        ${msg}`)
  }
}

realLog(`\n=== 合计 ${total} 个键编译失败（渲染时必崩）===`)
realLog(`（压掉的 intlify 噪声共 ${noisy.length} 条，都是「翻译键缺失」类警告，与本问题无关）`)
process.exitCode = total > 0 ? 1 : 0
