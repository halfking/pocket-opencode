// audit-workspace-args — 静态卡口：找出「按 workspace_id 分区的函数被调用时漏传 workspaceId」。
//
// 背景：BUG-AK（notes 列表）与 BUG-AR（PKM 笔记）是同一个形状——
//   local_* 表按 workspace_id 分区，asset-store.ts:114 / notes-store 用 `?? 'default'` 兜底，
//   调用点漏传就把行写进 default 分区，而读侧按 auth.workspaceId 查 →
//   表现是「保存成功但列表看不见」。两次都是人眼发现的，这里沉淀成可复跑的卡口。
//
// ⚠️ 第一版卡口是**无效的**：只扫位置参数，于是完全看不见 BUG-AR 的真实形状
//    （saveNote({...}) 是对象参数，workspaceId 在对象字面量里）。现已补上第二路。
//
// 判定口径（刻意收窄，避免噪声）：
//   路线 A（位置参数）：参数里出现 workspaceId，且它带默认值（`= 'default'`）或可选
//                       （`workspaceId?`）——这类默认值正是「漏传就静默落 default」的入口。
//                       调用点实参个数 ≤ workspaceId 下标 ⇒ 漏传。
//   路线 B（对象参数）：形参类型注解是内联对象字面量且其中含 workspaceId。
//                       调用点对应实参是对象字面量但**没有** workspaceId 键 ⇒ 漏传。
//
// 已知噪声（人工确认过，不算漏报）：同名函数跨模块撞车（pkm-store.getNote /
// services/flashcards.deleteNote 等），以及注释里出现的调用名。
// 用法：node scripts/audit-workspace-args.mjs
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative, extname } from 'node:path'

const ROOT = new URL('..', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const SRC = join(ROOT, 'frontend', 'src')

function walk(dir, out = []) {
  for (const e of readdirSync(dir)) {
    if (e === 'node_modules' || e === 'dist' || e.startsWith('.')) continue
    const p = join(dir, e)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (['.ts', '.tsx', '.vue'].includes(extname(e)) && !e.endsWith('.d.ts')) out.push(p)
  }
  return out
}

/** 按顶层逗号切分（忽略括号/引号/模板串内的逗号）。 */
function splitTop(s) {
  const out = []
  let depth = 0, cur = '', q = null
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (q) { cur += c; if (c === q && s[i - 1] !== '\\') q = null; continue }
    if (c === '"' || c === "'" || c === '`') { q = c; cur += c; continue }
    if ('([{<'.includes(c)) depth++
    else if (')]}>'.includes(c)) depth--
    if (c === ',' && depth === 0) { out.push(cur.trim()); cur = ''; continue }
    cur += c
  }
  if (cur.trim()) out.push(cur.trim())
  return out
}

function matchParen(s, open) {
  let depth = 0, q = null
  for (let i = open; i < s.length; i++) {
    const c = s[i]
    if (q) { if (c === q && s[i - 1] !== '\\') q = null; continue }
    if (c === '"' || c === "'" || c === '`') { q = c; continue }
    if (c === '(') depth++
    else if (c === ')') { depth--; if (depth === 0) return i }
  }
  return -1
}

const files = walk(SRC)
const relOf = (f) => relative(ROOT, f).replace(/\\/g, '/')
const dirOf = (f) => f.replace(/\\/g, '/').replace(/\/[^/]+$/, '')

/** 每个模块里导出的、workspaceId 可省略的函数：name -> {posIdx, objIdx} */
const defsByFile = new Map()
/** 每个文件从相对模块 import 了哪些名字 */
const importsByFile = new Map()

const defRe = /(?:export\s+)?(?:async\s+)?function\s+([A-Za-z0-9_$]+)\s*\(([\s\S]*?)\)\s*(?::[^{]*)?\{/g
const importRe = /import\s+(?:type\s+)?\{([^}]*)\}\s*from\s*['"]([^'"]+)['"]/g

for (const f of files) {
  const src = readFileSync(f, 'utf8')
  const rel = relOf(f)

  // --- import 表（只要值导入；type-only 不产生调用）---
  const imps = new Map()
  let mi
  while ((mi = importRe.exec(src))) {
    const names = mi[1].split(',').map((s) => s.replace(/\/\*[\s\S]*?\*\//g, '').split(/\s+as\s+/).pop().trim()).filter(Boolean)
    const spec = mi[2]
    imps.set(spec, names)
  }
  importsByFile.set(rel, imps)

  // --- 本文件的导出定义 ---
  const local = new Map()
  let m
  while ((m = defRe.exec(src))) {
    if (!/export\s+(?:async\s+)?function/.test(m[0])) continue
    const [, name, params] = m
    const parts = splitTop(params).filter((p) => p.trim() !== '')
    const rec = { posIdx: null, objIdx: null }

    const wi = parts.findIndex((p) => /^(workspaceId|workspace_id)\b/.test(p))
    if (wi >= 0 && (/=\s*['"`]default['"`]/.test(parts[wi]) || /\?\s*:/.test(parts[wi]))) rec.posIdx = wi

    parts.forEach((p, i) => {
      const brace = p.indexOf('{')
      if (brace < 0 || !p.slice(0, brace).includes(':')) return
      if (/workspaceId\??\s*:/.test(p.slice(brace))) rec.objIdx = i
    })
    if (rec.posIdx !== null || rec.objIdx !== null) local.set(name, rec)
  }
  defsByFile.set(rel, local)
}

/** 把 import 说明符解析成仓库内相对路径（只处理 ./ ../ 形式）。 */
function resolveSpec(fromRel, spec) {
  if (!spec.startsWith('.')) return null
  const base = dirOf(fromRel) + '/' + spec
  const segs = []
  for (const s of base.split('/')) {
    if (s === '.' || s === '') continue
    if (s === '..') segs.pop()
    else segs.push(s)
  }
  const p = segs.join('/')
  for (const cand of [p, p + '.ts', p + '.vue', p + '/index.ts', p + '/index.vue']) {
    if (defsByFile.has(cand)) return cand
  }
  return null
}

const hits = []
const unresolved = new Set()
for (const f of files) {
  const src = readFileSync(f, 'utf8')
  const rel = relOf(f)
  const local = defsByFile.get(rel)
  const imps = importsByFile.get(rel)

  // 本文件可见的定义：本地 + 逐条 import 解析
  const visible = new Map()
  for (const [n, r] of local) visible.set(n, { ...r, from: rel })
  for (const [spec, names] of imps) {
    const target = resolveSpec(rel, spec)
    if (!target) continue
    for (const n of names) {
      if (visible.has(n)) continue          // 本地定义优先
      const d = defsByFile.get(target)?.get(n)
      if (d) visible.set(n, { ...d, from: target })
    }
  }

  for (const [name, r] of visible) {
    const re = new RegExp(`(?<![\\w$.'"\`])${name}\\s*\\(`, 'g')
    let m
    while ((m = re.exec(src))) {
      const open = m.index + m[0].length - 1
      const close = matchParen(src, open)
      if (close < 0) continue
      const before = src.slice(0, m.index)
      const line = before.slice(before.lastIndexOf('\n') + 1)
      if (/^\s*(\*|\/\/|\/\*)/.test(line)) continue
      if (new RegExp(`function\\s+${name}\\s*\\($`).test(line)) continue

      const args = splitTop(src.slice(open + 1, close))
      const ln = before.split('\n').length
      if (r.posIdx !== null && args.length <= r.posIdx) {
        hits.push({ file: rel, line: ln, name, why: `位置参数缺 workspaceId（实参 ${args.length} 个，需第 ${r.posIdx} 位）`, def: r.from })
      }
      if (r.objIdx !== null) {
        const a = args[r.objIdx]
        // 注意要认 ES6 简写属性：`{ workspaceId }` 里没有冒号，
        // 只匹配 `workspaceId:` 会把正确调用误报成漏传（第一版就踩了这个坑）。
        if (a !== undefined && a.trim().startsWith('{') && !/\bworkspaceId\b/.test(a)) {
          hits.push({ file: rel, line: ln, name, why: '对象实参里没有 workspaceId 键', def: r.from })
        }
      }
    }
  }
}

const totalDefs = [...defsByFile.values()].reduce((n, m) => n + m.size, 0)
console.log(`扫描 ${files.length} 个源文件，识别 ${totalDefs} 个「workspaceId 可省略」的导出函数`)
console.log(`命中 ${hits.length} 处漏传：\n`)
for (const h of hits.sort((a, b) => a.file.localeCompare(b.file) || a.line - b.line)) {
  console.log(`  ${h.file}:${h.line}  ${h.name}()  ← ${h.why}   [定义于 ${h.def}]`)
}
console.log(`\n判定按 import 解析到具体模块，同名函数不再互相污染。`)
console.log(`命中为 0 也要先看上面的函数总数——若为 0 说明解析器失灵（假绿），不是代码干净。`)
