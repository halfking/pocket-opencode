// verify-build-identity.mjs - 产物级门禁：确认「读原生版本」这个修复**真的进了构建产物**。
//
// ## 为什么需要这一层（2026-10-03）
//
// 判据跑在**源码**上，类型检查也跑在源码上。两者都绿并不代表修复进了 APK：
// vite 会 tree-shake，从没被 import 的组件不会进任何 chunk，改名/拆包也能让
// 代码「还在仓库里、但不在产物里」。这正是 §4.74.2 的形态——
// 仓库里是对的、设备上跑的不是，而没有任何一步会红。
//
// 所以这里查的是**字节**：构建完成后扫 dist/assets/*.js，确认
//   1. 新增的 i18n 键 buildDateNote 出现在产物里（locale 接线生效）；
//   2. 原生读版本的调用 getInfo 出现在产物里（解析逻辑真被打包）；
//   3. 回退常量 2026-06-29 仍在（无原生时还有东西可退）。
//
// 第 3 条防的是另一类事故：有人「顺手清掉用不到的回退」，于是非原生环境
// （web / Harmony）直接显示空版本号。
//
// ## 用法
//
//   node scripts/verify-build-identity.mjs                 # 查 dist/
//   node scripts/verify-build-identity.mjs <outDir>        # 查别的产物目录
//
// 退出码：0 通过 / 3 产物目录不可读或没有 js（此时所有检查都会**空转通过**，
// 必须单独报错）/ 4 有检查项缺失。
//
// 建议接进移动端构建：build-mobile.mjs 在 cap sync 之前跑它。
//
// 备注：正文里不使用反引号模板字符串 —— write 工具的 content 是 JSON 编码的，
// 转义反引号在 JSON 解码时会丢反斜杠，落盘成裸反引号并提前终止字符串。

import { readdirSync, readFileSync, statSync, existsSync } from 'node:fs'
import { join, resolve } from 'node:path'

const outDir = resolve(process.argv[2] || 'dist')
const assets = join(outDir, 'assets')

if (!existsSync(assets)) {
  console.error('[FAIL] 产物目录不存在：' + assets)
  console.error('  先构建再跑本门禁。若只想验证某次隔离构建，把它的 outDir 作为参数传进来。')
  process.exit(3)
}

const files = readdirSync(assets).filter((f) => f.endsWith('.js'))
if (files.length === 0) {
  // 关键：产物里没有 js 时，下面每个「没找到就是失败」的检查都会**空转通过**。
  // 那是「没有证据」，不是「通过」，必须在这里就停。
  console.error('[FAIL] ' + assets + ' 里没有任何 .js chunk —— 所有检查都会空转通过，本门禁拒绝在这种状态下给结论。')
  process.exit(3)
}

console.log('扫描 ' + files.length + ' 个 js chunk：' + assets)

// 2026-10-03 追加：构建时刻注入。vite.config.ts 用 define 把 __BUILD_TIME__
// 替换成本次构建的真实时刻。这一条防的是「define 被误删 / 改名」——
// 那时设置页会静默退回硬编码的 2026-06-29，而类型检查与单测都不会红，
// 因为 __BUILD_TIME__ 在 node 下本来就不存在。
const tsPattern = /"20\d\d-\d\d-\d\d \d\d:\d\d:\d\d UTC[+-]\d\d:\d\d"/

let bad = 0

const hits = { buildDateNote: [], getInfo: [], fallbackDate: [], buildTime: [] }
let totalBytes = 0

for (const f of files) {
  const p = join(assets, f)
  totalBytes += statSync(p).size
  // latin1 是字节等价的：不会重新编码 CJK，也不会因为非法 UTF-8 序列抛错。
  const s = readFileSync(p).toString('latin1')
  if (s.includes('buildDateNote')) hits.buildDateNote.push(f)
  if (s.includes('getInfo')) hits.getInfo.push(f)
  if (s.includes('2026-06-29')) hits.fallbackDate.push(f)
  if (tsPattern.test(s)) hits.buildTime.push(f)
  // 未被替换的标识符：说明 define 没生效（哪怕产物里恰好还有时间戳，
  // 出现这个字面量也说明注入路径坏了，值得单独报出来）
  if (s.includes('__BUILD_TIME__')) {
    console.error('[FAIL] 产物里出现未替换的 __BUILD_TIME__ 字面量 —— vite define 没有生效。')
    console.error('  检查 vite.config.ts 的 define 段是否被删除或改名。')
    bad++
  }
}

console.log('js 总体积：' + totalBytes + ' 字节')
console.log('')
for (const k of Object.keys(hits)) {
  console.log('  含 ' + k.padEnd(16) + '：' + JSON.stringify(hits[k]))
}
console.log('')

if (hits.buildDateNote.length === 0) {
  console.error('[FAIL] 产物里没有 buildDateNote —— 设置页的「构建日期」来源标注没进构建。')
  console.error('  要么 locale 改动没被构建吃到，要么该键被 tree-shake 掉了。')
  bad++
}
if (hits.getInfo.length === 0) {
  console.error('[FAIL] 产物里没有 getInfo —— 读原生 BuildConfig 的逻辑**不在产物中**。')
  console.error('  后果：设置页与更新弹窗继续显示 TS 常量，也就是 §4.74.2 那个「看不出设备装的是哪个构建」的坑。')
  console.error('  注意：vue-tsc 通过并不能证明这一点 —— 它检查的是源码，不是产物。')
  bad++
}
if (hits.fallbackDate.length === 0) {
  console.error('[FAIL] 产物里没有回退常量 2026-06-29 —— 非原生环境（web / Harmony）将显示空版本号。')
  bad++
}
if (hits.buildTime.length === 0) {
  console.error('[FAIL] 产物里没有编译期构建时刻 —— 设置页会退回硬编码的 2026-06-29，')
  console.error('  也就是「构建日期」又变成与产物无关的假信息。检查 vite.config.ts 的 define.__BUILD_TIME__。')
  bad++
}

if (bad > 0) {
  console.error('')
  console.error('门禁未通过（' + bad + ' 项）。')
  process.exit(4)
}

console.log('[OK] locale 接线、原生版本读取、回退常量、编译期构建时刻四者都在产物里。')
process.exit(0)
