// tdz-declaration-order.test.mjs — `<script setup>` 顶层 const 的 TDZ（先用后声明）
//
// ⚠️ 这条判据是被**一次真实设备事故**逼出来的，来源可查：
//   设备实测捕获 `ReferenceError: Cannot access 'pe' before initialization`，
//   stack 指向 `SessionConversationView-*.js`（`pe` = minify 后的 `pendingApprovalCount`）。
//
//   机制：`useElapsedNow(() => [...])` 的**第一个参数**在**调用点**就被求值 ——
//   因为它内部是 `watch(basesAt, () => schedule())`，而 **Vue 3 的 `watch` 即使没有
//   `immediate: true`，建立时也会先求值一次 source 来收集依赖**。
//   ⇒ 调用点在第 325 行，而它读的 `pendingApprovalCount` 第 345 行才声明 ⇒ TDZ。
//   后果不止报错：`schedule()` 从未跑成 ⇒ `nowTick` 恒为初值，时长副标题不更新。
//
// ⚠️⚠️ **第一版判据锚错了，两次假阴性 + 两次假阳性，全部靠变异抓出来**：
//   1) 正则要求**行首**是 `use*/watch`，实际是 `const x = useY(` ⇒ 一个调用点都没匹配到，
//      **恒报 0**（空集 diff 永远「一致」）。
//   2) deps 只截到箭头符号 `=>` 为止 ⇒ **函数体没进来**，扫到的仍然是 0。
//   3) 修好之后报出 `TasksView.vue:561` 与 `NoteMetaSheet.vue:39` —— **两处都是假阳性**：
//      · TasksView：`useInstanceApprovals` 内部**没有 watch / onMounted**，
//        4 处 `instanceId()` 全在函数体内 ⇒ 参数**不会**被立即求值。
//      · NoteMetaSheet：`form` 出现在 **watch 的第二个参数（callback）** 里，
//        callback 无 `immediate` 不会在声明前执行 ⇒ 不 TDZ。
//      病根：**「composable 的第一个参数会立即求值」不是通则**，
//      它取决于**该 composable 内部有没有 `watch(source)` / `watchEffect`**。
// ⇒ 所以下面**不按名字猜**，而是**读 composable 源码取证**。

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { test } from 'node:test'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = resolve(HERE, '..', '..')

/** 递归列目录下的 .vue。 */
function listVue(dir, out = []) {
  for (const e of readdirSync(dir)) {
    if (e === 'node_modules' || e === 'dist') continue
    const p = join(dir, e)
    const st = statSync(p)
    if (st.isDirectory()) listVue(p, out)
    else if (e.endsWith('.vue')) out.push(p)
  }
  return out
}

/** 剥注释：**用结构而非字面量**，避免「注释里写了变量名」被算成引用。 */
function stripComments(s) {
  return s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '')
}

/** 从 `(` 处取配平参数文本。 */
function balanced(s, i) {
  let d = 0
  for (let j = i; j < s.length; j += 1) {
    if ('([{'.includes(s[j])) d += 1
    else if (')]}'.includes(s[j])) { d -= 1; if (d === 0) return s.slice(i + 1, j) }
  }
  return ''
}

/**
 * 取**第一个**顶层实参。
 *
 * ⚠️ 入参来自 `balanced()`，**外层括号已被剥掉** ⇒ 这里**不能**再等一个闭合
 * `)` 来收口，否则 `() => […]` 会在第 1 个字符（那个 `)`）就被截成 `"("`，
 * 后面所有匹配全落空 ⇒ 又一次「一个调用点都不过 ⇒ 恒真」。
 * 同理 `watch(a, cb)` 只看 `a` —— `cb` 无 `immediate`，不会在声明前执行。
 */
function firstArg(s) {
  let d = 0
  for (let j = 0; j < s.length; j += 1) {
    const c = s[j]
    if ('([{'.includes(c)) d += 1
    else if (')]}'.includes(c)) d -= 1
    else if (c === ',' && d === 0) return s.slice(0, j)
  }
  return s
}

/** composable 真实位置**不固定**（`useElapsedNow` 在 `composables/`，
 *  `useInstanceApprovals` 在 `features/tasks/`）⇒ 假定路径会读不到，
 *  而「读不到」会让负对照的取证结果变成 `known:false`，看起来像「不是立即求值」——
 *  也就是**用一个够不着的文件去证明判据的边界**，等于没证。全仓建一次索引。 */
let TS_INDEX = null
function tsIndex() {
  if (TS_INDEX) return TS_INDEX
  TS_INDEX = new Map()
  const walk = (dir) => {
    for (const e of readdirSync(dir)) {
      if (e === 'node_modules' || e === 'dist' || e.startsWith('.')) continue
      const p = join(dir, e)
      const st = statSync(p)
      if (st.isDirectory()) walk(p)
      else if (e.endsWith('.ts') && !e.endsWith('.d.ts')) TS_INDEX.set(e, p)
    }
  }
  walk(SRC)
  return TS_INDEX
}

/**
 * 一个 composable 的**第一个参数是否会被立即求值**？
 * 取证方式：读它的源码，命中 `watch(<第一形参名>` 或 `watchEffect(` 即认定会。
 * 命中 `watch(() => …)` 这种**内部再包一层**的不算 —— 那种包装函数本身不是 source。
 *
 * ⚠️ 第一形参名**必须从 composable 源码里解析**，不能由调用点提供：
 *   调用点写的是 `useElapsedNow(() => [...])`，箭头是**无名**的；
 *   拿一个猜出来的名字去匹配 `watch(<名>` 必然不中 ⇒ eager 恒 false
 *   ⇒ **一个调用点都不过 ⇒ 判据退化成恒真**（这个坑我踩过一次）。
 */
function evaluatesFirstArgEagerly(composable) {
  const f = tsIndex().get(`${composable}.ts`)
  if (!f) return { known: false, eager: false, why: '全仓找不到该 composable 源文件' }
  const body = stripComments(readFileSync(f, 'utf8'))
  // export function useXxx(first, …)  /  export function useXxx({ … }, …)
  const sig = new RegExp(`export\\s+function\\s+${composable}\\s*\\(\\s*\\{?\\s*([\\w$]+)`).exec(body)
  const firstParam = sig ? sig[1] : null
  if (!firstParam) return { known: true, eager: false, file: f, why: '取不到第一形参名' }
  const watchDirect = new RegExp(`\\bwatch\\(\\s*${firstParam}\\b`).test(body)
  const watchEffect = /\bwatchEffect\s*\(/.test(body)
  return { known: true, eager: watchDirect || watchEffect, file: f, firstParam, watchDirect, watchEffect }
}

function vueFiles() {
  return listVue(SRC)
}

test('【量具自证】composable 取证器确实能区分「立即求值」与「延后求值」', () => {
  // 正例：useElapsedNow 内部是 watch(basesAt, …) ⇒ 第一个参数立即求值
  const eager = evaluatesFirstArgEagerly('useElapsedNow')
  assert.equal(eager.known, true, `读不到 useElapsedNow 源码（${eager.file}）⇒ 取证器失效`)
  assert.equal(eager.eager, true, 'useElapsedNow 内部 watch(basesAt) ⇒ 应当判定为立即求值')
  assert.equal(eager.watchDirect, true)

  // 反例：useInstanceApprovals 内部无 watch/onMounted ⇒ 第一个参数**不**立即求值
  const lazy = evaluatesFirstArgEagerly('useInstanceApprovals')
  assert.equal(lazy.known, true, `读不到 useInstanceApprovals 源码（${lazy.file}）`)
  assert.equal(lazy.eager, false,
    'useInstanceApprovals 内部无 watch(source) ⇒ 第一个参数是惰性的。'
    + '若这里变 true，说明该 composable 改了实现，判据的边界要重新评估')
})

test('TDZ：立即求值的第一个参数里，不得引用「声明在其之后」的顶层 const', () => {
  const offenders = []
  const checked = []

  for (const file of vueFiles()) {
    const raw = readFileSync(file, 'utf8')
    const m = /<script setup[^>]*>([\s\S]*?)<\/script>/.exec(raw)
    if (!m) continue
    const code = stripComments(m[1])
    const rel = file.slice(SRC.length + 1)

    // 顶层声明（行首无缩进）
    const decl = new Map()
    for (const d of code.matchAll(/^(?:export\s+)?(?:const|let|function|class)\s+(\w+)/gm)) {
      if (!decl.has(d[1])) decl.set(d[1], code.slice(0, d.index).split('\n').length)
    }

    for (const call of code.matchAll(/(?<![\w.$])(watch|watchEffect|use[A-Z]\w*)\s*\(/g)) {
      const lineStart = code.lastIndexOf('\n', call.index) + 1
      if (code.slice(lineStart, call.index).trimStart().startsWith('import')) continue
      const name = call[1]
      const line = code.slice(0, call.index).split('\n').length
      // call[0] 形如 `useX(`；找它后面那个 '('
      const openIdx = call.index + call[0].lastIndexOf('(')
      const first = firstArg(balanced(code, openIdx))

      // 第一个实参必须是「箭头函数」，且它的形参无名（`() =>`）
      const am = /^\s*(?:\(\s*\)|\(\s*[^()]*\)|[\w$]+)\s*=>/.exec(first)
      if (!am) continue
      // composable 名的第一参数：useXxx(a, b) ⇒ a；watch(source, cb) ⇒ source
      const eager = name === 'watch' || name === 'watchEffect'
        ? { known: true, eager: true }
        : evaluatesFirstArgEagerly(name)
      if (!eager.eager) continue
      checked.push(`${rel} ${name}@${line}`)

      for (const id of new Set(first.matchAll(/\b([A-Za-z_$][\w$]*)\b(?=\s*(?:\.|\[|\)))/g))) {
        const nm = id[1]
        if (decl.has(nm) && decl.get(nm) > line) {
          offenders.push(`${rel}:${line}  ${name}(…) 第一个参数用到 ${nm}，但它在第 ${decl.get(nm)} 行才声明`)
        }
      }
    }
  }

  assert.ok(checked.length > 0, '一个立即求值的调用点都没扫到 ⇒ 判据已退化成恒真')
  assert.deepEqual(offenders, [], `检出 ${offenders.length} 处 TDZ（先用后声明）：\n  ${offenders.join('\n  ')}`)
})

function firstArgName(arrow) {
  const m = /^\s*\(\s*([\w$]+)\s*\)\s*=>/.exec(arrow)
  return m ? m[1] : null
}
