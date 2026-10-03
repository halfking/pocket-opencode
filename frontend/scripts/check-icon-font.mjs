// check-icon-font.mjs —— 对**已提交的字体文件**实测每个图标名能否合成连字。
//
// 用法：node scripts/check-icon-font.mjs
//
// ## 它和 check-icon-subset.mjs 的区别
//
// check-icon-subset.mjs 比对的是**名字集合**：扫源码收集名字，与 FALLBACK 求差集。
// 它的盲区是「字体文件本身和名单不同步」——名单改了但没重跑构建脚本，它照样通过，
// 因为它从没打开过字体。
//
// 本脚本打开 `src/assets/fonts/material-symbols-outlined.woff2`，用 harfbuzz 对每个
// 名字做一次真实的 ligature 成形：名字在字体里 → 整串合成 1 个字形；不在 → 退化成
// N 个单字符字形（正是真机上显示 `LIGHT_MODE` 的那个形态）。
//
// 所以它能抓到的、check-icon-subset.mjs 抓不到的：
//   - 字体是陈旧产物（源码加了图标、字体没重建）
//   - 字体被误提交成完整版或别的字体
//   - 任何「名字在名单里但字体里合不出来」的组合
//
// ## 判据与两个对照
//
// 合成成功 = `名字 + 空格` 成形后恰好 2 个字形（1 个图标 + 1 个空格）。
// 脚本自带正反对照，若对照不成立说明是字体没解析成功，此时报出来的「缺字」全是假的。
//
// 注意：harfbuzz 不解 woff2，必须先用 fontverter 转 sfnt。少了这一步字体解析失败，
// 每个字符都退成 .notdef，glyph 数恰好等于字符数——看起来像「全部缺字」的假象。

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { createRequire } from 'node:module'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import * as hb from 'harfbuzzjs'

const require = createRequire(import.meta.url)
const fontverter = require('fontverter')

const __dirname = dirname(fileURLToPath(import.meta.url))
const ROOT = join(__dirname, '..')
const SRC = join(ROOT, 'src')
// 允许用 POCKET_ICON_FONT 指向别的字体——注入实测（故意裁掉字形后验证本脚本会报警）
// 与排障时需要，否则只能对着那一份产物测。
const FONT_PATH = process.env.POCKET_ICON_FONT
  ? join(ROOT, process.env.POCKET_ICON_FONT)
  : join(SRC, 'assets', 'fonts', 'material-symbols-outlined.woff2')

// ── 1. 收集要检查的名字 ────────────────────────────────────────────────
// 三处来源求并集，与 build 脚本的采集规则保持一致，但**只用于「该检查谁」**，
// 真正的判据是字体里能不能合成，不是名单里有没有。

const LITERAL_RE = /material-symbols-outlined"[^>]*>\s*([a-z_]{3,})\s*</g
// 跨行插值：{{ 与 }} 不在同一行也算（AIChatView.vue:59 就是这种）
const INTERP_RE = /material-symbols-outlined"[^>]*>\s*\{\{([\s\S]{0,200}?)\}\}/g
const QUOTED_RE = /'([a-z_]{3,})'/g
// 数据表声明：icon: 'x'
const DECL_RE = /(?:^|[{,(\s])icon(?:Name)?\s*:\s*'([a-z_]{3,})'/g

const wanted = new Map() // name -> Set<来源>

function want(name, where) {
  if (!/^[a-z][a-z0-9_]{2,}$/.test(name)) return
  if (!wanted.has(name)) wanted.set(name, new Set())
  wanted.get(name).add(where)
}

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name.startsWith('.')) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.(vue|ts|css|js|mjs)$/.test(name)) out.push(p)
  }
  return out
}

const files = walk(SRC)
for (const file of files) {
  const text = readFileSync(file, 'utf8')
  const rel = relative(ROOT, file)
  let m
  LITERAL_RE.lastIndex = 0
  while ((m = LITERAL_RE.exec(text)) !== null) want(m[1], `${rel} 字面量`)
  INTERP_RE.lastIndex = 0
  while ((m = INTERP_RE.exec(text)) !== null) {
    QUOTED_RE.lastIndex = 0
    let q
    while ((q = QUOTED_RE.exec(m[1])) !== null) want(q[1], `${rel} 插值`)
  }
  DECL_RE.lastIndex = 0
  while ((m = DECL_RE.exec(text)) !== null) want(m[1], `${rel} 数据表`)
}

// 注册表（src/constants/icons.ts）——动态引用的权威名单
const registrySrc = readFileSync(join(SRC, 'constants', 'icons.ts'), 'utf8')
const ICON_BLOCK = /export const ICON = \{([\s\S]*?)\n\} as const/.exec(registrySrc)
if (!ICON_BLOCK) {
  console.error('[icon-font] 无法从 src/constants/icons.ts 解析 ICON 注册表')
  process.exit(2)
}
const REGISTRY = [...ICON_BLOCK[1].matchAll(/:\s*'([a-z_]+)'/g)].map((m) => m[1])
if (REGISTRY.length < 20) {
  console.error(`[icon-font] ICON 注册表解析异常，只得到 ${REGISTRY.length} 个名字`)
  process.exit(2)
}
for (const name of REGISTRY) want(name, 'constants/icons.ts 注册表')

// ── 2. 打开字体 ───────────────────────────────────────────────────────
// 基准字体：node_modules 里的上游完整版。它回答「这个名字到底算不算图标名」。
// 子集字体：回答「我们裁进去的字体里合不合成得出来」。
//
// 为什么要基准字体：收集规则必然有假阳性。`{{ item.status === 'starred' ? 'star'
// : 'star_border' }}` 里的 'starred' 是**状态比较值**，不是图标；跨行 `{{`…`}}`
// 之间还可能捞到普通变量名（实测捞到过 `name`）。这类名字在子集字体里当然合不出来，
// 但报成「缺字」是噪声。判据只能是：它在上游完整字体里也合不出来 → 它压根不是图标名，
// 跳过。反过来，上游有、子集没有，才是真的缺字。

function makeShaper(fontBuf) {
  const sfnt = Buffer.from(fontBuf)
  const f = new hb.Font(new hb.Face(new hb.Blob(sfnt)))
  return function ligates(name) {
    const buffer = new hb.Buffer()
    buffer.addText(`${name} `)
    buffer.guessSegmentProperties()
    // material-symbols 的连字在 liga/dlig/calt 下；HarfBuzz 默认不启用 dlig，
    // 不显式开的话一个都合不出来（阳性对照会全灭）。
    hb.shape(
      f,
      buffer,
      ['liga', 'dlig', 'calt', 'rlig', 'ccmp'].map((t) => new hb.Feature(t, 1)),
    )
    return buffer.getGlyphInfos().length === 2
  }
}

const UPSTREAM_PATH = join(ROOT, 'node_modules', 'material-symbols', 'material-symbols-outlined.woff2')
const raw = readFileSync(FONT_PATH)
const isUpstreamIcon = makeShaper(Buffer.from(await fontverter.convert(readFileSync(UPSTREAM_PATH), 'truetype')))
const ligates = makeShaper(Buffer.from(await fontverter.convert(raw, 'truetype')))

// ── 3. 对照先行 ──────────────────────────────────────────────────────
// 对照校验的是**成形器**（能不能正确判断合字），不是子集字体里有没有某个名字。
// 早先版本把对照写死在子集字体上（'light_mode' 必须合字），结果注入一个只含
// 'check' 的字体时，对照先炸掉并以 exit 2 收场，反而报不出真正的「缺 122 个」。
// 解析能力用基准字体验；子集内容用下面的逐个实测报。
const SHAPER_CONTROLS = [
  ['light_mode', true],
  ['zzzznotanicon', false],
]

console.log(`[icon-font] 字体 ${relative(ROOT, FONT_PATH)} ${(raw.length / 1024 / 1024).toFixed(2)} MB`)
console.log(`[icon-font] 基准 ${relative(ROOT, UPSTREAM_PATH)}（用于判定「算不算图标名」）`)
let controlsOk = true
for (const [name, expect] of SHAPER_CONTROLS) {
  const got = isUpstreamIcon(name)
  if (got !== expect) {
    console.error(
      `[icon-font] 成形器对照失败：基准字体里 ${name} 期望 ${expect ? '合字' : '不合字'}，实际 ${got ? '合字' : '不合字'}`,
    )
    console.error('[icon-font] 基准字体没被正确解析，后续结论全部作废。')
    controlsOk = false
  }
}
if (!controlsOk) process.exit(2)

// ── 4. 逐个实测 ──────────────────────────────────────────────────────

const names = [...wanted.keys()].sort()
const missing = []
const notIcons = []
for (const name of names) {
  if (!isUpstreamIcon(name)) {
    notIcons.push(name)
    continue
  }
  if (!ligates(name)) missing.push({ name, where: [...wanted.get(name)].slice(0, 2) })
}

console.log(`[icon-font] 引用到的名字 ${names.length} 个`)
console.log(`[icon-font] 其中真实图标名 ${names.length - notIcons.length} 个，非图标误报 ${notIcons.length} 个`)
console.log(`[icon-font] 注册表 ${REGISTRY.length} 个动态名已并入`)

if (missing.length === 0) {
  console.log('[icon-font] ✅ 全部图标名在字体里都能合成连字')
  process.exit(0)
}

console.error(`[icon-font] ❌ ${missing.length} 个名字在字体里合不出连字，真机会显示字面文本：`)
for (const m of missing) {
  console.error(`  ${m.name.padEnd(24)} ${m.where.join(' / ')}`)
}
console.error('[icon-font] 修法：node scripts/build-material-symbols-subset.mjs 重建字体后提交产物。')
process.exit(1)
