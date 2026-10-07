// note-pending-pickup-on-keepalive.test.mjs
//
// 锁住「跨页停止的录音能被拾取回来」这条数据不丢的约束。
//
// 2026-10-06 真机复现（Redmi 2411DRN47C / HyperOS / sttdev）：
//   笔记页点 FAB 开始录音 → 录 19 秒 → 切到 #/ai → 点全局指示条的停止
//   → 回笔记页等 20 秒 → local_notes 恒不新增
//   → 杀进程重启 → 仍是 6 行 / 11 子行
//   19 秒录音的**文字与音频双双消失**，且全局停止的 toast 明确承诺
//   「回笔记页可拾取语音草稿」。
//
// 成因：拾取逻辑（consumePendingResult）**只**挂在 onMounted，而 NoteListView
// 命中 `<KeepAlive :include="LIST_CACHE_NAMES">`（use-list-scene.ts:52 +
// NoteListView.vue 的 defineOptions name 两边对得上）⇒ 切离再回来跑的是
// onActivated，onMounted 不重跑 ⇒ pendingResult 永远没人取；进程一死它就蒸发。
//
// 为什么这道门要显式核对白名单：结论「必须挂 onActivated」的前提是
// **该页面确实被 KeepAlive 缓存**。若哪天有人把 NoteListView 移出
// LIST_CACHE_NAMES，这道门应该报出「前提已变，请重新评估」而不是继续
// 当成一条永远成立的规矩 —— 否则它会变成在解释一个不存在的问题。
//
// 已有 list-scene-cache-integrity.test.mjs 覆盖的是另一个维度（被缓存的列表页
// 会不会数据过期，靠 useListScene / onActivated / 共享 store），
// 而 useListScene 只管刷新列表、不会消费一次性 payload ⇒ 两道门各管一维，
// 缺哪一维都拦不住这个 bug。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
const VIEW = path.join(SRC, 'features', 'notes', 'NoteListView.vue')
const LIST_SCENE = path.join(SRC, 'composables', 'use-list-scene.ts')

/**
 * 只去注释，**保留**字符串字面量。
 *
 * defineOptions({ name: 'NoteListView' }) 里的组件名就是一个字符串 ——
 * 判 KeepAlive 缓存时必须读得到它。第一版把它和字符串一起抹成空格，
 * 于是 viewName() 恒为 null、isCached 恒 false，负控全以
 * 「前提不成立，负控无法执行」失败。症状是：门自己不报风险，
 * 看起来像"通过"，实际上一条断言都没真正跑。
 */
export function stripCommentsOnly(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:])\/\/[^\n]*/g, (m, p1) => p1 + ' '.repeat(Math.max(0, m.length - p1.length)))
}

/**
 * 去注释 **并** 去字符串字面量。
 * 只在"某段代码里有没有出现 onActivated"这类判据上用 —— 注释里写过
 * `onActivated` 不等于代码里有（这个坑 list-scene-cache-integrity 那道门
 * 已经踩过一次并写进注释）。
 */
export function stripCommentsAndStrings(raw) {
  // 判据只在 <script setup> 块内跑：.vue 有 template/script/style 三段，
  // 在整文件里扫会把 <style scoped> 的 CSS 规则当成函数体
  // （bodyAfter 找到的第一个 '{' 可能是 `margin: 0 0 var(--space-2)`）。
  const s = raw.indexOf('<script setup')
  const e = raw.lastIndexOf('</script>')
  const src = s >= 0 && e > s ? raw.slice(s, e) : raw
  return stripCommentsOnly(src)
    // import 必须先剥掉：import { computed, onActivated, onMounted, ref, watch } from 'vue'
    // 里那排标识符后面紧跟着 '{'，bodyAfter() 找 'onMounted' 会先命中这里，
    // 拿 import 的花括号当函数体 —— 于是两个钩子的可达性都判成 false。
    .replace(/import\s[\s\S]*?from\s+['"][^'"]*['"];?/g, (m) => ' '.repeat(m.length))
    .replace(/'(?:[^'\\\n]|\\.)*'/g, (m) => "'" + ' '.repeat(Math.max(0, m.length - 2)) + "'")
    .replace(/"(?:[^"\\\n]|\\.)*"/g, (m) => '"' + ' '.repeat(Math.max(0, m.length - 2)) + '"')
}

const read = (p) => fs.readFileSync(p, 'utf8')
const viewSrc = stripCommentsOnly(read(VIEW))
const viewCode = stripCommentsAndStrings(read(VIEW))
const listSrc = read(LIST_SCENE)

/** KeepAlive include 名单（按名字匹配，必须与 defineOptions name 一致）。 */
export function cachedViewNames() {
  const m = listSrc.match(/export const LIST_CACHE_NAMES = \[([\s\S]*?)\]/)
  if (!m) return null
  return [...m[1].matchAll(/'([^']+)'/g)].map((x) => x[1])
}

/** 该视图的 defineOptions name（KeepAlive 靠它匹配 include）。 */
export function viewName(src) {
  const m = src.match(/defineOptions\(\s*\{[^}]*name:\s*'([^']+)'/)
  return m ? m[1] : null
}

/**
 * 从 `marker` 之后找出紧随其身的大括号体（括号配平），找不到返回 null。
 *
 * 不能用 `marker\s*\([^)]*\)\s*\{` —— `onActivated(async () => {` 的参数表里
 * 就有 `)`，非贪婪的 `[^)]*` 会在那里停住，匹配到 `async () ` 之后接不上 `{`。
 * 这里改成扫到**第一个位于 ()/[] 深度 0 的 `{`** 再配平。
 */
export function bodyAfter(src, marker) {
  const at = src.indexOf(marker)
  if (at < 0) return null
  let i = at + marker.length
  let paren = 0
  for (; i < src.length; i++) {
    const c = src[i]
    if (c === "'" || c === '"' || c === '`') {
      const q = c
      for (i++; i < src.length && src[i] !== q; i++) if (src[i] === '\\') i++
      continue
    }
    if (c === '(' || c === '[') paren++
    else if (c === ')' || c === ']') paren--
    else if (c === '{' && (paren <= 0 || src.slice(0, i).trimEnd().endsWith('=>'))) {
      // 箭头函数的 body 落在**调用参数表内部**（paren 深度 ≥1），
      // 例如 onMounted(async () => { … })；所以光靠 paren<=0 会直接漏掉，
      // bodyAfter 返回 null、两个钩子的可达性全判成 false。
      let depth = 0
      for (let k = i; k < src.length; k++) {
        if (src[k] === '{') depth++
        else if (src[k] === '}') { depth--; if (depth === 0) return src.slice(i, k + 1) }
      }
      return null
    }
  }
  return null
}

/** 顶层 `function <name>(...)` / `const <name> = (…) =>` 的函数体。 */
export function namedBodies(src) {
  const out = new Map()
  const re = /(?:function\s+([A-Za-z_$][\w$]*)|(?:const|let)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?\()/g
  let m
  while ((m = re.exec(src))) {
    const name = m[1] || m[2]
    const body = bodyAfter(src, name)
    if (body) out.set(name, body)
  }
  return out
}

const HOOKS = ['onMounted', 'onActivated']

/** 从某个钩子的函数体出发，看能不能（经函数调用）走到 consumePendingResult。 */
function reachesConsume(body, bodies, seen = new Set()) {
  if (!body) return false
  if (/consumePendingResult/.test(body)) return true
  for (const name of bodies.keys()) {
    if (seen.has(name)) continue
    if (!new RegExp(`(?<![\\w$.])${name}\\s*\\(`).test(body)) continue
    seen.add(name)
    if (reachesConsume(bodies.get(name), bodies, seen)) return true
  }
  return false
}

/**
 * 返回「一次性产物拾取不到」的风险；null = 安全。
 * 抽成函数是为了让负控能对同一段检测逻辑跑变异。
 */
export function findPickupRisks(src, isCached) {
  if (!isCached) return null // 前提不成立：未被缓存 ⇒ onMounted 每次都跑，不构成风险
  if (!/consumePendingResult/.test(src)) {
    return ['consumePendingResult 都不在视图里了 —— 拾取逻辑被移走，请重新评估本门的前提']
  }
  const bodies = namedBodies(src)
  const risks = []
  const reached = {}
  for (const h of HOOKS) {
    reached[h] = reachesConsume(bodyAfter(src, h), bodies)
  }
  if (!reached.onMounted) risks.push('consumePendingResult 不在 onMounted 里')
  if (!reached.onActivated) {
    risks.push(
      'consumePendingResult 不在 onActivated 里：该视图被 KeepAlive 缓存，切离再回来只跑 onActivated，' +
      'pendingResult 永远没人取（2026-10-06 真机复现：19 秒录音跨页停止后文字与音频双双消失）'
    )
  }
  return risks.length ? risks : null
}

const names = cachedViewNames()
const vName = viewName(viewSrc)
const isCached = names !== null && vName !== null && names.includes(vName)

describe('跨页停止的录音必须能被拾取回来', () => {
  it('前提：NoteListView 确实在 KeepAlive 缓存白名单里', () => {
    assert.ok(names, '解析不出 LIST_CACHE_NAMES —— 解析器坏了，先修解析器再谈别的')
    assert.equal(vName, 'NoteListView', 'defineOptions name 变了')
    assert.ok(isCached, `NoteListView 不在 LIST_CACHE_NAMES 里了 ⇒ 前提已变：页面不再被缓存，onMounted 每次都会跑，本门应重新评估而不是继续当规矩`)
  })

  it('拾取逻辑在 onMounted 与 onActivated 上都挂了', () => {
    const risks = findPickupRisks(viewCode, isCached)
    assert.equal(risks, null, risks ? risks.join('\n') : '')
  })
})

describe('负控：把 onActivated 那条去掉必须报红', () => {
  it('负控1：删掉 onActivated 钩子 → 报红', () => {
    assert.ok(isCached, '前提不成立，负控无法执行')
    const mutated = viewCode.replace(/onActivated\(async[\s\S]*?\n\}/, '')
    assert.notEqual(mutated, viewCode, '变异没生效：源码结构与预期不符，先看源码再改门')
    const risks = findPickupRisks(mutated, true)
    assert.ok(risks, '★ 门有洞：删掉 onActivated 竟然判为安全')
    assert.ok(risks.some((r) => /onActivated/.test(r)))
  })

  it('负控2：把 onMounted 换成 onActivated 单挂 → 报红（冷启动也该拾取）', () => {
    assert.ok(isCached, '前提不成立，负控无法执行')
    const mutated = viewCode.replace(/onMounted\s*\(/, 'onSomethingElse(')
    const risks = findPickupRisks(mutated, true)
    assert.ok(risks, '★ 门有洞：onMounted 没了竟然判为安全')
    assert.ok(risks.some((r) => /onMounted/.test(r)))
  })

  it('负控3：前提为「未被缓存」时不报风险（门不能无条件喊狼来了）', () => {
    const risks = findPickupRisks(viewCode, false)
    assert.equal(risks, null, '未被缓存时 onMounted 每次都跑，本来就安全，门不该报风险')
  })
})
