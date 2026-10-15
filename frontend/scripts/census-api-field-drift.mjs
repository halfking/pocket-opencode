#!/usr/bin/env node
// §113 普查：全 API 面「前端声明了 / Go 侧没有同名 json tag」的字段。
//
// 这是 §111（ingest 端点：前端发 10 键、Go 的 CreateMeetingRequest 只接 2 键）
// 的全 API 面推广。
//
// ── 量具的坑（本器第一版踩过，记在这里）────────────────────────────────
// 第一版只抠 `JSON.stringify({ … })` 的**对象字面量**，结果 22 个带请求体的
// api 文件只抠到 4 个、14 个端点。漏掉的 18 个里绝大多数是
//     body: JSON.stringify(input)          ← 键在 TypeScript interface 里
// ⇒ **「只认对象字面量」是形态假设，不是事实**。
// 本版两条路都走：字面量抠键；变量形态**顺着类型名去抠 interface / type**。
//
// ⚠ 本器**只报事实，不下结论**：
//   「前端声明了、Go 没 tag」既可能是**数据丢失**，也可能是**有意不落云端**
//   （§111.2：那 7 个字段本地库已落，属产品取舍）。区分要看别的证据。
import { readdirSync, readFileSync } from 'node:fs'
import { join, relative, extname, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = join(HERE, '..', '..')
const API_DIR = join(ROOT, 'frontend', 'src', 'api')

const EXPLICIT_KEY = /(^|[^\w.$])([A-Za-z_][A-Za-z0-9_]*)(\s*:\s*[A-Za-z_]|\s*\?:\s*[A-Za-z_])/g
const SHORTHAND_KEY = /([{,])\s*([A-Za-z_][A-Za-z0-9_]*)\s*[,}]/g

function stripComments(s) {
  return s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1')
}
function matchedBraces(s, open) {
  let d = 0
  for (let i = open; i < s.length; i++) {
    const c = s[i]
    if (c === '(' || c === '[' || c === '{') d++
    else if (c === ')' || c === ']' || c === '}') {
      d--
      if (d === 0) return s.slice(open + 1, i)
    }
  }
  return ''
}
function objectKeys(src) {
  const seen = new Set(); const keys = []
  const add = (k) => { if (!k || seen.has(k)) return; seen.add(k); keys.push(k) }
  EXPLICIT_KEY.lastIndex = 0
  for (const m of src.matchAll(EXPLICIT_KEY)) add(m[2])
  SHORTHAND_KEY.lastIndex = 0
  for (const m of src.matchAll(SHORTHAND_KEY)) add(m[2])
  return keys
}

// ── 全仓 TS 类型定义索引：`接口名 -> 字段[]` ──
const typeIndex = new Map()   // 只收 src/ 下的（api 层与其它层共用同一套类型）
function indexTypes(dir) {
  let ents
  try { ents = readdirSync(dir, { withFileTypes: true }) } catch { return }
  for (const e of ents) {
    if (e.name === 'node_modules') continue
    const p = join(dir, e.name)
    if (e.isDirectory()) { indexTypes(p); continue }
    if (!['.ts', '.vue'].includes(extname(p))) continue
    const s = stripComments(readFileSync(p, 'utf8'))
    // ⚠ 必须**括号配平**，不能用 `\{([\s\S]*?)\n\}` —— 非贪婪会在**注释里的 `*/`**
    //   后面遇到第一个 `\n}` 就提前收尾，于是 `FinanceCreateInput`（体里有一段
    //   以 `*/` 结尾的 JSDoc）整个被漏掉，而**读数完全正常**（只是少了几条）。
    //   这是本器第三次栽在「量具形态」上。
    const HEAD = /(?:export\s+)?(?:interface|type)\s+([A-Za-z_$][\w$]*)\s*(?:=\s*)?\{/g
    for (const m of s.matchAll(HEAD)) {
      const fields = objectKeys(matchedBraces(s, m.index + m[0].length - 1))
      if (fields.length) typeIndex.set(m[1], fields)
    }
  }
}
indexTypes(join(ROOT, 'frontend', 'src'))

// ── Go 侧 json tag 池 ──
const goTags = new Set()
function walkGo(dir) {
  let ents
  try { ents = readdirSync(dir, { withFileTypes: true }) } catch { return }
  for (const e of ents) {
    if (e.name === 'vendor' || e.name === 'node_modules') continue
    const p = join(dir, e.name)
    if (e.isDirectory()) walkGo(p)
    else if (extname(p) === '.go' && !p.endsWith('_test.go')) {
      const s = readFileSync(p, 'utf8')
      for (const m of s.matchAll(/json:"([A-Za-z_][A-Za-z0-9_]*)/g)) goTags.add(m[1])
    }
  }
}
walkGo(join(ROOT, 'backend', 'internal'))

// ── 逐端点抠键 ──
const rows = []
let literalSites = 0, typeSites = 0, unresolved = 0

for (const f of readdirSync(API_DIR)) {
  if (extname(f) !== '.ts') continue
  const src = stripComments(readFileSync(join(API_DIR, f), 'utf8'))
  // ⚠ 必须容许**泛型实参**：`http<SyncResponse>('/api/assets/sync', {…})`。
  //   前四版都写成 `http\(`，于是所有带泛型的端点**压根没被扫到** ——
  //   而汇总行把它们混进「未能解析」，读数看着像「类型追不到」，
  //   真因是「这个端点我根本没看见」。⇒ 「没扫到」与「扫到但追不到」必须分两栏。
  const RE = /http(?:<[^>()]*>)?\s*\(\s*[`'"]([^`'"]+)[`'"]\s*,\s*\{/g
  for (const m of src.matchAll(RE)) {
    const url = m[1]
    const opts = matchedBraces(src, m.index + m[0].length - 1)
    const sj = opts.indexOf('JSON.stringify(')
    if (sj < 0) continue
    // ⚠ 必须取**完整标识符**，不能只取 `[0]` 第一个字符。
    //   `JSON.stringify(req)` 抠出来是 `r` ⇒ 与后面的类型标注 `req: SyncRequest` 永远配不上。
    //   而汇总行把它记成「未能解析」，看着像「类型在别处定义」——真因是「我连变量名都只取了一个字母」。
    const afterArg = opts.slice(sj + 'JSON.stringify('.length).replace(/^\s*/, '')
    // 先判**字面量**：紧跟的是 `{` 就是对象字面量，与后面抠不抠得到标识符无关。
    // ⚠ 顺序反了会把字面量整类漏掉（arg 为空时短路 ⇒ 字面量计数变 0，
    //   而 `/api/meetings` 这条**已证实的真缺口**从读数里消失 —— 判据自己坏了）。
    let keys = [], via = ''
    if (afterArg[0] === '{') {
      literalSites++
      keys = objectKeys(matchedBraces(opts, sj + 'JSON.stringify('.length))
      via = '对象字面量'
    } else {
      const argM = /^[A-Za-z_$][\w$]*/.exec(afterArg)
      const arg = argM ? argM[0] : ''
      if (arg === '') {
        unresolved++
        rows.push({ file: f, url, keys: [], missing: [], via: 'JSON.stringify 后面不是标识符（未识别形态）' })
        continue
      }
      // 变量形态：追类型。
      // ⚠ 类型名在**方法签名**里（`create(input: NoteInput): Promise<Note> {`），
      //   而那个签名位于 `http(` **之前** —— 第一版只在 http( 的 options 块里搜，
      //   17 个端点一个都没命中。⇒ 必须从**整个方法体起点**往前看。
      const before = src.slice(0, m.index)
      let typeName = null
      // ── 通用规则，不枚举签名形态 ──
      // 前四版分别只认：方法签名 / 箭头函数 / 属性里的箭头 / ……，
      // 每修一种就冒出下一种（rss.ts 的类型名还在更早一行）。
      // ⇒ 形态枚举这条路走不通。改成**只问一件事**：
      //   「这个变量在**离它最近的那次出现**里，被标注成了什么类型？」
      // 即在 before 里找最后一次 `<变量名>\s*:\s*<类型名>`，类型名是否已索引。
      // 不关心它出现在方法签名、箭头函数、还是对象属性里。
      const ann = new RegExp(`\\b${arg.replace(/[.*+?^\${}()|[\]\\\\]/g, '\\\\$&')}\\s*:\\s*([A-Za-z_$][\\w$]*)`, 'g')
      let last = null
      for (const a of before.matchAll(ann)) last = a[1]
      if (last && typeIndex.has(last)) typeName = last
      if (!typeName && typeIndex.has(arg)) typeName = arg   // 变量本身就是类型名
      if (typeName && typeIndex.has(typeName)) {
        typeSites++
        keys = typeIndex.get(typeName)
        via = `类型 ${typeName}`
      } else {
        unresolved++
        rows.push({ file: f, url, keys: [], missing: [], via: `类型未索引（${arg}）` })
        continue
      }
    }
    rows.push({ file: f, url, keys, via, missing: keys.filter((k) => !goTags.has(k)) })
  }
}

// ── 每个字段**前端有没有真的传过** ──
//
// §113.9 的教训：普查器报出 `voiceSessionId` / `audioFilePath`，我一度写成「新缺口」，
// 追查后发现它们只存在于**类型定义**里，**全仓没有一个调用点传过** ⇒ 死占位，不是数据丢失。
//
// ⇒ 差集必须再分一层「声明了但没人用」 vs 「真在传、只是 Go 没接」。
//   只看「字段名出现在某个对象的键里」会把两者混成一谈。
// 判据：在**值位置**上找 `<字段名>:` 或 `<字段名>,`（对象实参），
//      排除类型定义与 interface 声明本身（那些只出现在「名字: 类型」的位置）。
const bodyValueRe = new Map()   // 字段名 -> 真的被当实参传过的次数
function scanCallSites(dir) {
  let ents
  try { ents = readdirSync(dir, { withFileTypes: true }) } catch { return }
  for (const e of ents) {
    if (e.name === 'node_modules') continue
    const p = join(dir, e.name)
    if (e.isDirectory()) { scanCallSites(p); continue }
    if (!['.ts', '.vue'].includes(extname(p))) continue
    const s = stripComments(readFileSync(p, 'utf8'))
    // 先把 interface/type 体挖掉，剩下的才是「真正在写代码」的地方
    const HEAD2 = /(?:export\s+)?(?:interface|type)\s+[A-Za-z_$][\w$]*\s*(?:=\s*)?\{/g
    let cut = s
    for (const m of s.matchAll(HEAD2)) {
      cut = cut.replace(matchedBraces(s, m.index + m[0].length - 1), '')
    }
    for (const m of cut.matchAll(/([A-Za-z_$][\w$]*)\s*[:,]/g)) {
      bodyValueRe.set(m[1], (bodyValueRe.get(m[1]) || 0) + 1)
    }
  }
}
scanCallSites(join(ROOT, 'frontend', 'src'))

const ok = rows.filter((r) => !r.unresolved)
const withMissing = ok.filter((r) => r.missing.length).sort((a, b) => b.missing.length - a.missing.length)

// 每个差集字段标注「真在传」还是「仅声明」。
//
// ⚠⚠ **边界，必须写明**：本层只对「逐字段列举实参」的写法有效。
//   形如 `JSON.stringify(input)`（**整个对象直接传**）的端点 ——
//   `notes.ts:78` 就是这样 —— 类型里的**每个字段都会被真传出去**，
//   一个实参键都抠不到。此时标「○ 仅类型声明」是**误报**。
//
//   判法：若该端点的请求体是 `JSON.stringify(<单个标识符>)`（不是对象字面量），
//   则类型里的**全部**字段都算「在传」，只有没有任何调用点引用那个类型时才算死占位。
for (const r of withMissing) {
  r.used = {}
  const wholeObject = (() => {
    // ⚠ 必须**按 URL 定位**到本端点自己的 body；
    //   用 `indexOf('JSON.stringify(')` 会取到**文件里第一个**，
    //   同一个文件有多个端点时就会张冠李戴（我在这上面已经栽过两次，见 §113.6）。
    try {
      const src = stripComments(readFileSync(join(API_DIR, r.file), 'utf8'))
      const urlAt = src.indexOf('`' + r.url + '`') >= 0
        ? src.indexOf('`' + r.url + '`')
        : src.indexOf("'" + r.url + "'")
      if (urlAt < 0) return false
      const after = src.slice(urlAt, urlAt + 400)
      const sj = after.indexOf('JSON.stringify(')
      if (sj < 0) return false
      return /JSON\.stringify\(\s*[A-Za-z_$][\w$]*\s*\)/.test(after.slice(sj, sj + 60))
    } catch { return false }
  })()
  for (const k of r.missing) {
    r.used[k] = wholeObject || (bodyValueRe.get(k) || 0) > 0
  }
  r.wholeObject = wholeObject
}

console.log('== §113 全 API 面：前端声明了 / Go 侧**没有同名 json tag**的字段 ==\n')
console.log(`api 文件带请求体的端点: ${rows.length}`)
console.log(`  其中字面量抠出键: ${literalSites}   经类型索引抠出键: ${typeSites}   未能解析: ${unresolved}`)
console.log(`TS 类型索引: ${typeIndex.size} 个   Go json tag 池: ${goTags.size} 个\n`)
console.log('差集  端点'.padEnd(52) + '键来源')
console.log('-'.repeat(88))
for (const r of withMissing) {
  const u = r.url.length > 46 ? r.url.slice(0, 44) + '..' : r.url
  console.log(String(r.missing.length).padEnd(6) + u.padEnd(52) + r.via)
}

console.log(`\n== 汇总 ==`)
console.log(`对账通过（差集为空）: ${ok.length - withMissing.length} / ${ok.length}`)
console.log(`有差集: ${withMissing.length}`)
if (unresolved) {
  console.log(`未能解析（键在别处定义，本器追不到）: ${unresolved}`)
  for (const r of rows.filter((x) => x.unresolved)) console.log(`  ${r.url}  [${r.file}]  ${r.via}`)
}
console.log('\n⚠ 「有差集」≠「有缺陷」：可能是本地已落（§111.2）、或该字段本就只在一侧使用。')
console.log('  本器只报事实，判「该不该落」需要看那个字段有没有别的落库路径。')
console.log('\n★ 每个差集字段额外标注「前端**真在传**」还是「仅类型声明」——')
console.log('  「仅声明」= 全仓没有调用点传过它 ⇒ 死占位字段，不是数据丢失（§113.9）。')
if (withMissing.length) {
  console.log('\n-- 差集明细 --')
  for (const r of withMissing) {
    const live = r.missing.filter((k) => r.used[k])
    const dead = r.missing.filter((k) => !r.used[k])
    console.log(`  ${r.url}  [${r.file}]  (${r.via}, 共 ${r.keys.length} 键)`)
    if (live.length) console.log(`     ★ 真在传、Go 没接: ${live.join(', ')}`)
    if (dead.length) console.log(`     ○ 仅类型声明（全仓无调用点传过）: ${dead.join(', ')}`)
  }
}