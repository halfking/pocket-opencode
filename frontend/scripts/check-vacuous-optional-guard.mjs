#!/usr/bin/env node
// check-vacuous-optional-guard.mjs — 卡口：可选链比较型 v-if 在实体缺席时恒真。
//
// ## 要防的失效
//
// Vue 模板里写 `v-if="task?.status !== 'active'"`，当 `task` 为 null 时
// `undefined !== 'active'` 求值为 **真**——守卫在「实体根本不存在」时反而放行。
//
// 2026-10-03 真机实测（GET /api/tasks/:id 返回 404）：TaskDetailView 的加载
// 失败被渲染成一个看起来完全正常的任务详情页，标题停在「加载中...」，
// 而 ▶恢复 / ✅完成 / 📎附加 / 🗑 四个按钮全部渲染、点下去却被
// `if (!task.value) return` 静默吞掉。
//
// ## 为什么不是「见可选链比较就报错」
//
// 同样的写法在 TasksView.vue 的右键上下文菜单里出现 5 处，是**正确的**：
// 那个菜单的容器本身已经对 `contextTask` 做了 v-if 门控，外层不放行时
// 内层条件根本不会被求值。
//
// 所以判据必须是**结构性的**：命中可选链比较型守卫时，沿祖先链向上找，
// 必须存在一个对**同一实体**的 v-if 兜底；没有 ⇒ 这条守卫是无效的。
//
// ★ 「兜底」不等于「提到了」：祖先 v-if 只要词面上提到这个实体就算数的话，
//   下面四种祖先全是假通过，而它们的共同形状是**祖先表达式为真 ⇏ 实体非空**：
//     v-if="!task || true"             恒真
//     v-if="banner || task"            banner 为真时照样渲染
//     v-if="typeof task === 'object'"  typeof null === 'object' 为真
//     v-if="!task"                     恰在缺席时为真
//   「祖先为真 ⇒ 内层会被求值」这一步本身是对的，所以这四种全都白放行。
//   现在的判据是蕴含判定 `ancestorGuaranteesNonNull`，配套两道自检：
//   ① `SELF_TEST` 合成夹具（真假已知的祖先表达式，走同一个函数）；
//   ② 扫描结束后拿**记录下来的**祖先表达式重算 gated，堵住「判定函数没被调用」。
//   见这两处的注释。
//
// 逐个点名文件/行号是行不通的（只能追自己的记忆），所以守的是这条形状。
//
// ## 为什么全仓只有 8 处也要卡
//
// 数量少正说明它容易被忽略：这类缺陷不产生任何报错，界面看起来是对的，
// 只是按钮不响应。数量少不是放过的理由。
//
// 用法：node scripts/check-vacuous-optional-guard.mjs [srcRoot]
// 退出码：0 = 通过；1 = 有无效守卫（逐条打印文件/行号/表达式）

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

/**
 * 默认扫描根**相对脚本自身位置**解析，而不是相对 process.cwd()。
 *
 * 本脚本第一版写的是 `frontend/src`：从仓根跑对，被 npm script 调起就变成
 * `frontend/frontend/src` —— 因为 npm 脚本的 cwd 是 frontend。
 * 「在某个 cwd 下才工作」的判据迟早会在 CI 或别人机器上失效，
 * 而它的失效形态是 exit 2（判据失明），不是误判，还算好认。
 */
const HERE = dirname(fileURLToPath(import.meta.url))
const SRC_ROOT = resolve(process.argv[2] || join(HERE, '..', 'src'))
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

/**
 * 可选链 + 与字面量的等/不等比较。
 * 刻意只认「比较」这一种形态：`v-if="a?.b"`（只取值不带比较）在 null 时
 * 求值为 undefined，是**假**，语义正确，不能误报。
 */
const VACUOUS_TEST = /\?\.([A-Za-z_$][\w$]*)(?:\.[A-Za-z_$][\w$]*)*[^=!<>]*?(?:!==|===)/

/**
 * 命中数的下限。**只能手工改这个常量**，不提供命令行开关。
 *
 * ★ 这道门是「计数型」的，零命中是**危险方向**而不是「干净」的证据（§84 的
 *   分类法）：本仓当前有 8 处命中。真出现 0 处，只可能是 VACUOUS_TEST 被改坏、
 *   扫描根变了，或者这 8 处真的被清空了 —— 三种都得有人来看一眼。
 *   给了开关就会变成「顺手敲个参数把门关掉」，所以不给。
 */
const MIN_HITS = 1

/**
 * 扫到的 `.vue` 数量下限。本仓当前 186，取 150：正常增删组件不会误报，
 * 而**系统性**枚举退化（SKIP 被加了名字、某个子目录 stat 失败、walk 写坏）会撞上。
 * 它抓不住「只丢一两个文件」那种 —— 那是它的边界，不假装能抓。
 * 同样**只能手工改**，不给命令行开关。
 */
const MIN_VUE_FILES = 150

/** 取表达式里被可选链解引用的根标识符，例如 `task?.status` -> `task`。 */
function rootIdentities(expr) {
  const out = new Set()
  const re = /\b([A-Za-z_$][\w$]*)\?\.([A-Za-z_$][\w$]*)/g
  let m
  while ((m = re.exec(expr)) !== null) {
    // `a?.b` 里的 a 也可能是 `foo.a?.b`（嵌套），只取紧邻 `?.` 的那一段
    out.add(m[1])
  }
  return out
}

/**
 * 「祖先表达式为真 ⇒ 该实体非空」——这是一个**蕴含**判定，不是词匹配。
 *
 * ★ 第一版的判据是词级的：`mentions(祖先表达式, 实体)` 只要祖先表达式里
 *   **提到**这个标识符就算兜底，完全不看祖先拿它做了什么。
 *   实测四种假通过（SELF_TEST 里全部有夹具，旧判据对它们一律放行）：
 *
 *     `!task || true`             恒真，实体在不在都渲染
 *     `banner || task`            banner 为真、task 为空时照样渲染
 *     `typeof task === 'object'`  `typeof null === 'object'` 为真
 *     `!task`                     恰在实体缺席时为真
 *
 *   共同形状：**祖先表达式为真 ⇏ 实体非空**。而「祖先为真 ⇒ 内层会被求值」
 *   这一步本身是对的，所以这四种全是假通过 —— 门说「有兜底」，界面照旧在
 *   实体缺席时渲染内层守卫，正是这道门要防的那个 bug。
 *
 * 判据改成小命题判定器，按 JS 的 `&&` 优先于 `||` 展开：
 *   析取 `A || B` ⇒ A 与 B **都**蕴含（只有一支蕴含 = `banner || task`）
 *   合取 `A && B` ⇒ 任一支蕴含即可（`!loading && task` 仍然兜底）
 *   叶子 ⇒ 只认白名单里的「正相 truthy 测式」；`!ident` 显式拒绝
 *
 * 认不出来就判红（保守方向）：宁可多报，也不把看不懂的祖先当成兜底。
 * 真要放行仍有出口 —— ALLOWLIST 那套「声明具体机制 + 每次运行复核机制仍在」。
 */

/** 在 depth 0 处按双字符运算符切开（跳过引号内与括号内）。 */
function splitTopLevel(expr, ch) {
  const out = []
  let depth = 0
  let quote = null
  let start = 0
  for (let i = 0; i < expr.length; i++) {
    const c = expr[i]
    if (quote) {
      if (c === '\\') { i++; continue }
      if (c === quote) quote = null
      continue
    }
    if (c === '"' || c === "'" || c === '`') { quote = c; continue }
    if (c === '(' || c === '[' || c === '{') { depth++; continue }
    if (c === ')' || c === ']' || c === '}') { depth--; continue }
    if (depth === 0 && c === ch && expr[i + 1] === ch) {
      out.push(expr.slice(start, i))
      start = i + 2
      i++
    }
  }
  out.push(expr.slice(start))
  return out.map(s => s.trim())
}

/** 剥掉包住整个表达式的括号（确认括号确实盖住首尾，不是 `f(a) && b`）。 */
function stripOuterParens(s) {
  let cur = s.trim()
  while (cur.startsWith('(') && cur.endsWith(')')) {
    let depth = 0
    let wraps = true
    for (let i = 0; i < cur.length; i++) {
      if (cur[i] === '(') depth++
      else if (cur[i] === ')') {
        depth--
        if (depth === 0 && i < cur.length - 1) { wraps = false; break }
      }
    }
    if (!wraps) break
    cur = cur.slice(1, -1).trim()
  }
  return cur
}

/** 叶子：这一小段表达式为真，是否蕴含 `ident` 非空。只认白名单里的正相测式。 */
function impliesTruthy(expr, ident) {
  const s = stripOuterParens(expr)
  if (!s) return false
  const id = ident.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  if (new RegExp('^' + id + '$').test(s)) return true
  // ↓ 这一条在「只认白名单正相测式」的设计下是**冗余**的：`!task` 落不到下面任何
  //   一条白名单，末尾照样 return false。实测把它删掉读数不变（等价变异，
  //   设计文档 §85 M5）。显式写出来是为了让「取反恰在缺席时为真」这件事
  //   在代码里自明，也防着以后有人往白名单里加宽松分支时把它顺手放过。
  if (new RegExp('^!' + id + '$').test(s)) return false          // 恰在缺席时为真
  if (new RegExp('^!{2,}\\(*\\s*' + id + '\\s*\\)*$').test(s)) return true   // !!task / !!(task)
  if (new RegExp('^Boolean\\(\\s*' + id + '\\s*\\)$').test(s)) return true
  if (new RegExp('^' + id + '\\??\\.[\\w$]+(?:\\.[\\w$]+)*$').test(s)) return true // task?.id / task.status
  return false
}

/** 祖先 v-if 表达式为真时，是否保证 `ident` 非空。 */
function ancestorGuaranteesNonNull(expr, ident) {
  const s = stripOuterParens(expr)
  const ors = splitTopLevel(s, '|')
  if (ors.length > 1) return ors.every(part => ancestorGuaranteesNonNull(part, ident))
  const ands = splitTopLevel(s, '&')
  if (ands.length > 1) return ands.some(part => ancestorGuaranteesNonNull(part, ident))
  return impliesTruthy(s, ident)
}

/**
 * 判定自检：合成夹具走**同一条** `ancestorGuaranteesNonNull`，不另写一份判定。
 *
 * 为什么必须有：这道门失明时的读数长得和结论一模一样 ——「祖先无门控 0 处」
 * + ✅ + EXIT=0（判据被改成永远「有兜底」就是这样）。而门当下**看不出**自己
 * 被改坏了，只有把判定函数本身拿已知答案的输入喂一遍才知道。
 * 自检不通过 ⇒ exit 2，且跑在扫描之前。
 */
const SELF_TEST = [
  { name: 'A  同实体正相测式', expr: 'task', expect: true },
  { name: 'A2 双重否定', expr: '!!task', expect: true },
  { name: 'A3 Boolean()', expr: 'Boolean(task)', expect: true },
  { name: 'A4 成员访问取值为真 ⇒ 宿主非空', expr: 'task?.id', expect: true },
  { name: 'A5 合取里另一支无关', expr: '!loading && task', expect: true },
  { name: 'A6 合取另一支在本支之后', expr: 'task && !busy', expect: true },
  { name: 'B  析取含恒真项', expr: '!task || true', expect: false },
  { name: 'C  析取中只有一支提及', expr: 'banner || task', expect: false },
  { name: 'C2 析取里兜底那一支在左边也不够', expr: 'task || banner', expect: false },
  { name: 'D  typeof 对 null 为真', expr: "typeof task === 'object'", expect: false },
  { name: 'D2 instanceof', expr: 'task instanceof Object', expect: false },
  { name: 'E  同实体可选链比较本身就是恒真', expr: "task?.status !== 'loading'", expect: false },
  { name: 'F  取反恰在缺席时为真', expr: '!task', expect: false },
  { name: 'G  不同实体', expr: 'otherThing', expect: false },
  { name: 'H  外层括号不改变结论', expr: '(!task || banner)', expect: false },
  { name: 'H2 外层括号包住真兜底', expr: '(task && !busy)', expect: true },
  { name: 'I  空表达式', expr: '', expect: false },
]

const selfTestBad = []
for (const c of SELF_TEST) {
  let got
  try {
    got = ancestorGuaranteesNonNull(c.expr, 'task')
  } catch (err) {
    got = '抛异常: ' + err.message
  }
  if (got !== c.expect) selfTestBad.push({ ...c, got })
}
if (selfTestBad.length > 0) {
  console.error('❌ 蕴含判定自检不过 —— 判据本身坏掉了，本次结果一律不可信。')
  for (const c of selfTestBad) {
    console.error(`   ${c.name}  v-if="${c.expr}"  期望 gated=${c.expect}，实得 gated=${c.got}`)
  }
  console.error('   这些都是「祖先表达式为真 ⇒ 实体非空」的真假已知样本。')
  console.error('   注意：判据坏了时的读数长得和结论一模一样（0 处违规 + ✅），所以必须在这里拦。')
  process.exit(2)
}
console.log(`  蕴含判定自检：${SELF_TEST.length} 例夹具全通过（含 ${SELF_TEST.filter(c => !c.expect).length} 条必须判红的假兜底）`)

/**
 * 模式级自检：`VACUOUS_TEST` 本身对真假已知的表达式，**两个比较分支分别钉住**。
 *
 * ★ 为什么 SELF_TEST 兜不住这一层（2026-10-07 实测）：
 *   SELF_TEST 验的是**蕴含判定**，全程不经过 `VACUOUS_TEST`。
 *   而下面那道行级/解析器交叉自检比的是**两个计数相等**——
 *   把 `(?:!==|===)` 的两支一起打瞎，两个计数会**等量**从 8 掉到 5，
 *   两路一样沉默，门照样 EXIT=0（实测，见设计文档 §85 M9）。
 *   ⇒ 「两路一致」只说明**不对称**的退化；**对称**的退化只有模式自检能抓。
 *     所以把 `===` 与 `!==` 各自写成一条必须匹配的样本。
 */
const PATTERN_TEST = [
  { name: '=== 分支', expr: "task?.status === 'active'", expect: true },
  { name: '!== 分支', expr: "task?.status !== 'active'", expect: true },
  { name: '多级可选链 + ===', expr: "task?.meta.owner === 'me'", expect: true },
  { name: '析取两侧都是 ===', expr: "s?.status === 'a' || s?.status === 'b'", expect: true },
  { name: '只取值不带比较（语义正确，不该命中）', expr: 'task?.description', expect: false },
  { name: '没有可选链', expr: "task === 'x'", expect: false },
  { name: '完全无关', expr: 'banner', expect: false },
  { name: '空串', expr: '', expect: false },
]

const patternTestBad = []
for (const c of PATTERN_TEST) {
  let got
  try {
    got = VACUOUS_TEST.test(c.expr)
  } catch (err) {
    got = '抛异常: ' + err.message
  }
  if (got !== c.expect) patternTestBad.push({ ...c, got })
}
if (patternTestBad.length > 0) {
  console.error('❌ VACUOUS_TEST 模式自检不过 —— 命中形状被判坏了，下面所有计数都不可信。')
  for (const c of patternTestBad) {
    console.error(`   ${c.name}  v-if="${c.expr}"  期望命中=${c.expect}，实得命中=${c.got}`)
  }
  console.error('   （行级/解析器那两道交叉自检比的是两个**计数相等**，')
  console.error('    两支一起打瞎时两个计数等量下移、互相印证 —— 那类退化它们抓不到。）')
  process.exit(2)
}
console.log(`  模式自检：${PATTERN_TEST.length} 例表达式全通过（=== 与 !== 两支分别钉住）`)

/** 去掉 HTML 注释：注释里出现 <div v-if="a?.b !== 'c'"> 会造成假阳性。 */
function stripHtmlComments(s) {
  return s.replace(/<!--[\s\S]*?-->/g, m => m.replace(/[^\n]/g, ' '))
}

/**
 * 给**交叉自检那一路**用的第二份模板提取实现：逐行剥 script/style 块。
 *
 * ★ 为什么必须有第二份（2026-10-07 实测）：
 *   原来的交叉自检拿 `lineBasedCount` 和 `examined` 比数，但**两者算在同一份 `tpl` 上**
 *   （`stripHtmlComments(templateOf(readFileSync(f,'utf8')))`）。
 *   ⇒ 它只在**解析方法**上独立，**输入是共享的**：
 *     污染 `templateOf`（输入截取类回归）会让两个计数**等量漂移**，自检永远不响。
 *   实测：把第一版那个 `indexOf('</template>')` 截断装回去 ⇒
 *     两个计数**一起从 8 掉到 3**、自检沉默、门转绿、EXIT=0，
 *     而 `TasksView.vue` 的 5 处真实命中**被静默丢弃** ——
 *     那正是下面第 224 行那段报错文案描述的 bug。
 *   ⇒ **这道自检原本要抓的，恰恰是它抓不到的那一类。**
 *
 *   这里用**行状态机**而不是全局正则：算法不同 ⇒ 输入被污染时两路才会分叉。
 *   实测正常态两路同为 8（行状态机版与正则版逐条一致），不会造成常态误报。
 */
function templateOfLinewise(src) {
  const out = []
  let skipping = false
  for (const line of src.split('\n')) {
    const opens = /<(?:script|style)\b/i.test(line)
    const closes = /<\/(?:script|style)>/i.test(line)
    if (opens && !closes) { skipping = true; out.push(''); continue }
    if (skipping) { if (closes) skipping = false; out.push(''); continue }
    out.push(line)
  }
  return out.join('\n')
}

/**
 * 取出模板部分。
 *
 * 不要用 `indexOf('</template>')` 去找根模板的结束位置：视图里有
 * `<template #slot>` 这样的**嵌套 slot 模板**，它们的 `</template>` 会把区间
 * 提前截断。本脚本第一版就是这么写的，结果 TasksView.vue 里 5 处命中被静默丢弃
 * （行级审计数到 8，标签解析器只数到 3）——「少提取」不会报错，只会少报问题，
 * 是最难发现的一种失明。
 *
 * 改成「整份文件里删掉 script / style 段」，剩下的就是模板，不依赖任何配对。
 */
function templateOf(src) {
  return src
    .replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, m => m.replace(/[^\n]/g, ' '))
    .replace(/<style\b[^>]*>[\s\S]*?<\/style>/gi, m => m.replace(/[^\n]/g, ' '))
}

/**
 * 豁免：每条都**必须**引用一条可复核的具体机制，而不是只写一句理由。
 *
 * 判据每次运行都会亲自验证 `requires` 里的每一条（文件必须存在、且必须匹配
 * 指定正则）。机制一旦被删——比如有人把 `:model-value="showContextMenu && !!contextTask"`
 * 改回只绑 `showContextMenu`，或 BottomSheet 不再在根节点 v-if——豁免自动失效，
 * 该处重新变成违规并 exit 1。
 *
 * 这样「豁免」不会变成「永久放行」，这正是它与白名单的唯一区别。
 */
const ALLOWLIST = [
  {
    file: 'features/tasks/TasksView.vue',
    ident: 'contextTask',
    minHits: 4,
    reason:
      '右键上下文菜单经 <BottomSheet> 渲染，其 :model-value="showContextMenu && !!contextTask" ' +
      '在实体缺席时把 modelValue 置假；BottomSheet 根节点 v-if="visible" 使整棵子树不渲染。' +
      '门控写在组件属性上而不是 v-if 上，静态判据看不见这一跳。',
    requires: [
      {
        file: 'features/tasks/TasksView.vue',
        pattern: /:model-value\s*=\s*"[^"]*&&\s*!!contextTask/,
        why: 'TasksView 必须仍把 contextTask 合取进 BottomSheet 的 model-value',
      },
      {
        file: 'components/base/BottomSheet.vue',
        pattern: /v-if\s*=\s*"visible"/,
        why: 'BottomSheet 根节点必须仍以 visible 门控，否则子树不会真的被挡住',
      },
    ],
  },
  {
    file: 'features/tasks/TasksView.vue',
    ident: 'contextSession',
    minHits: 1,
    reason:
      '同上：会话操作区在同一个 BottomSheet 内，门控来自 :model-value 上的 !!contextTask；' +
      'contextSession 只在该面板已打开时才有意义。',
    requires: [
      {
        file: 'features/tasks/TasksView.vue',
        pattern: /:model-value\s*=\s*"[^"]*&&\s*!!contextTask/,
        why: 'TasksView 必须仍把 contextTask 合取进 BottomSheet 的 model-value',
      },
      {
        file: 'components/base/BottomSheet.vue',
        pattern: /v-if\s*=\s*"visible"/,
        why: 'BottomSheet 根节点必须仍以 visible 门控',
      },
    ],
  },
]

/** 复核一条豁免所声明的机制是否还在。返回 null = 仍然成立。 */
function verifyAllowlist(entry, root) {
  for (const req of entry.requires) {
    const p = join(root, req.file)
    let src
    try { src = readFileSync(p, 'utf8') } catch { return { file: req.file, why: '文件不存在' } }
    if (!req.pattern.test(src)) return { file: req.file, why: req.why }
  }
  return null
}

const files = []
;(function walk(d) {
  let entries
  try { entries = readdirSync(d) } catch { return }
  for (const name of entries) {
    if (SKIP.has(name)) continue
    const p = join(d, name)
    let st
    try { st = statSync(p) } catch { continue }
    if (st.isDirectory()) walk(p)
    else if (p.endsWith('.vue')) files.push(p)
  }
})(SRC_ROOT)

/**
 * 第二份枚举：**迭代式 + `withFileTypes`**（上面那份是递归 + `statSync`）。
 *
 * ★ 为什么必须有两份（2026-10-07 实测，原设计文档 §85 M11）：
 *   原来两个计数都算在**同一个 per-file 循环**里。往循环顶部加一句
 *   `if (f.endsWith('TasksView.vue')) continue` —— 行级正则计数和标签解析器
 *   计数**一起**从 8 掉到 3、两者仍然相等、交叉自检沉默、门照样 ✅ EXIT=0，
 *   而 TasksView 那 5 处真实命中被静默丢弃。
 *   ⇒ 「两路计数相等」只能说明**提取方法**的差异没出问题；
 *     只要退化发生在**两路共用的枚举/循环**上，它们必然等量漂移、互相印证。
 *     这与 §73 那次「两路共用同一份输入」是同一个病，只是这次共用的是循环。
 *
 *   两份枚举用**不同遍历机制**、各自独立成表 ⇒ 枚举坏掉时只有一侧少，
 *   计数对不上，交叉自检才响得了。
 */
const filesForLineCount = []
;(() => {
  const queue = [SRC_ROOT]
  while (queue.length) {
    const d = queue.pop()
    let entries
    try { entries = readdirSync(d, { withFileTypes: true }) } catch { continue }
    for (const e of entries) {
      if (SKIP.has(e.name)) continue
      const p = join(d, e.name)
      if (e.isDirectory()) queue.push(p)
      else if (e.isFile() && p.endsWith('.vue')) filesForLineCount.push(p)
    }
  }
})()

if (files.length === 0) {
  console.error(`❌ 在 ${SRC_ROOT} 下没找到任何 .vue —— 判据失明了（路径错？），拒绝按「通过」处理`)
  process.exit(2)
}
if (filesForLineCount.length !== files.length) {
  console.error(`❌ 两份枚举的 .vue 数量不一致：递归 statSync 版 ${files.length}，迭代 withFileTypes 版 ${filesForLineCount.length}`)
  console.error('   枚举本身就不确定，后面的计数不可信。')
  process.exit(2)
}
if (files.length < MIN_VUE_FILES) {
  console.error(`❌ 只扫到 ${files.length} 个 .vue < 下限 ${MIN_VUE_FILES} —— 枚举漏了东西，拒绝按「通过」处理。`)
  console.error('   （上限意义的下限：它抓的是**系统性**枚举退化，抓不住只丢一两个文件。')
  console.error('    本仓当前 186 个；下限取得比它低不少，正常增删组件不会误报。')
  console.error('    真要下调请手工改 MIN_VUE_FILES 常量，不给命令行开关。）')
  process.exit(2)
}

const hits = []
let examined = 0
let lineBasedCount = 0
let ancestorChecks = 0

// 行级对照计数：**独立一遍循环**，走第二份枚举（`filesForLineCount`）。
// ★ 绝不能放回下面那个 per-file 循环里 —— 两路共用一次遍历时，任何按文件的
//   退化都会让两个计数等量下移、交叉自检沉默（见 filesForLineCount 上的注释）。
for (const f of filesForLineCount) {
  const tplForLineCount = stripHtmlComments(templateOfLinewise(readFileSync(f, 'utf8')))
  lineBasedCount += (tplForLineCount.match(/v-(?:if|show)\s*=\s*"[^"]*?\?\.[^"]*?(?:!==|===)[^"]*"/g) || []).length
}

for (const f of files) {
  const tpl = stripHtmlComments(templateOf(readFileSync(f, 'utf8')))

  // 逐标签扫描并维护开元素栈，命中时向上找祖先门控
  const stack = [] // { tag, guard }
  const tagRe = /<(\/?)([A-Za-z][\w.-]*)((?:[^>"']|"[^"]*"|'[^']*')*?)(\/?)>/g
  let m
  while ((m = tagRe.exec(tpl)) !== null) {
    const [, closing, tag, rawAttrs, selfClose] = m
    if (closing) {
      // 容忍未闭合：用栈顶匹配，匹配不上就 pop 到空
      for (let k = stack.length - 1; k >= 0; k--) {
        if (stack[k].tag === tag) { stack.length = k; break }
      }
      continue
    }
    const vIf = (rawAttrs.match(/\bv-if\s*=\s*"([^"]*)"/) || [])[1] || null
    if (vIf && VACUOUS_TEST.test(vIf)) {
      examined++
      const idents = [...rootIdentities(vIf)]
      // 祖先链上**每一个** v-if 表达式都送进蕴含判定（故意不短路），
      // 并把表达式原文一并记进 hit.ancestorGuards —— 末尾的复核要用它。
      const ancestorGuards = stack.map(a => a.guard).filter(Boolean)
      let gated = false
      for (const g of ancestorGuards) {
        for (const id of idents) {
          ancestorChecks++
          if (ancestorGuaranteesNonNull(g, id)) gated = true
        }
      }
      const hit = {
        file: relative(SRC_ROOT, f).replace(/\\/g, '/'),
        line: tpl.slice(0, m.index).split('\n').length,
        expr: vIf,
        idents: idents.join(','),
        identsList: idents,
        ancestorGuards,
        gated,
        ancestors: stack.slice(-4).map(a => a.tag + (a.guard ? ' v-if="' + a.guard + '"' : '')),
      }
      hits.push(hit)
    }
    if (!selfClose) stack.push({ tag, guard: vIf })
  }
}

const violations = hits.filter(h => !h.gated)

console.log(`【可选链比较型守卫】扫了 ${files.length} 个 .vue，命中可选链比较型 v-if ${examined} 处`)
console.log(`  交叉自检：行级正则数到 ${lineBasedCount} 处，标签解析器数到 ${examined} 处`)
if (lineBasedCount !== examined) {
  console.error('❌ 两种计数不一致 —— 标签解析器有盲区，本次结果不可信，拒绝按「通过」处理。')
  console.error('   （本脚本第一版就栽在这里：根模板用 indexOf("</template>") 截取，被嵌套的')
  console.error('    <template #slot> 提前截断，TasksView.vue 的 5 处命中被静默丢弃。）')
  process.exit(2)
}

if (examined < MIN_HITS) {
  console.error(`❌ 命中 ${examined} 处 < 下限 ${MIN_HITS} —— 这是**危险方向**，不是「没有这类缺陷」的证据。`)
  console.error('   要么 VACUOUS_TEST 被改坏了、要么扫描根没扫到、要么这 8 处真的被清空了。')
  console.error('   三种都得有人来判断。真的清空了：手工改上面的 MIN_HITS 常量（本脚本不给命令行开关）。')
  process.exit(2)
}

// 复核：拿**记录下来的祖先表达式**重算一遍 gated，必须与扫描时逐条一致。
//
// 这道自检守的是**调用点**：上面的 SELF_TEST 兜住判定函数本身，但它管不到
// 「判定函数有没有被调用」。有人把调用点改回词级匹配、或直接写死 true/false，
// SELF_TEST 照样全绿，而门的读数仍然是「0 处违规 + ✅」。
for (const h of hits) {
  let again = false
  for (const g of h.ancestorGuards) {
    for (const id of h.identsList) {
      if (ancestorGuaranteesNonNull(g, id)) again = true
    }
  }
  if (again !== h.gated) {
    console.error(`❌ 复核不一致：${h.file}:${h.line}  扫描时 gated=${h.gated}，用记录的祖先表达式重算得 ${again}`)
    console.error('   祖先 v-if：' + (h.ancestorGuards.map(g => '"' + g + '"').join(' , ') || '(无)'))
    console.error('   判定结论没有被稳定重算出来 —— 判定与记录之间脱节，本次结果不可信。')
    process.exit(2)
  }
}
console.log(`  复核：${hits.length} 处命中用记录的祖先表达式重算，判定逐条一致（祖先门控求值 ${ancestorChecks} 次）`)
console.log(`其中祖先无法保证该实体非空的：${violations.length} 处（豁免前）`)

// 逐条套用豁免；机制对不上的豁免不生效，并且单独报出来
const staleAllowlist = []
const real = []
const allowlistHits = new Map()
for (const v of violations) {
  const entry = ALLOWLIST.find(a => a.file === v.file && v.idents.split(',').includes(a.ident))
  if (!entry) { real.push(v); continue }
  allowlistHits.set(entry, (allowlistHits.get(entry) || 0) + 1)
  const broken = verifyAllowlist(entry, SRC_ROOT)
  if (broken) {
    // 同一条豁免可能被多处命中，别把同一条失效提示重复打印 N 遍
    if (!staleAllowlist.some(s => s.entry === entry)) staleAllowlist.push({ entry, broken })
    real.push(v)
  } else {
    console.log(`  [豁免] ${v.file}:${v.line}  ${v.expr}`)
    console.log(`         解引用实体: ${v.idents} —— 门控在 ${entry.requires.map(r => r.file).join(' + ')}，已复核仍在`)
  }
}

// ★ 清单与现实对账：每条豁免声明它覆盖**几处**命中点，实际少了就红。
//
//   为什么需要（2026-10-07 实测，§85 M13）：两份枚举**同时**漏掉同一个文件时，
//   行级与解析器两个计数等量下移、交叉自检沉默、文件数 186→185 也够不到
//   MIN_VUE_FILES ⇒ 门输出「0 处违规 + ✅ + EXIT=0」，而 TasksView 那 5 处
//   真实命中被静默丢弃。这条对账专治**这一类**，而且它用的就是文件里本来就有的
//   清单，不需要新造魔数：
//     · 少了 ⇒ 要么扫描没覆盖到（判据问题），要么代码真被清掉了（该退休这条豁免）
//     · 两种都只能由人来定，所以 exit 1 而不是猜一个
const shrunkAllowlist = []
for (const entry of ALLOWLIST) {
  const n = allowlistHits.get(entry) || 0
  if (n < entry.minHits) {
    shrunkAllowlist.push({ entry, declared: entry.minHits, seen: n })
  }
}
console.log()
console.log(`豁免后仍需处理的：${real.length} 处`)
console.log()

for (const s of staleAllowlist) {
  console.error(`⚠️  豁免已失效：${s.entry.file} / ${s.entry.ident}`)
  console.error(`    理由：${s.broken.file} —— ${s.broken.why}`)
  console.error(`    原注释：${s.entry.reason}`)
  console.error(`    处理：要么恢复该机制，要么删掉这条豁免并真的修好代码。`)
  console.error()
}

for (const s of shrunkAllowlist) {
  console.error(`⚠️  豁免覆盖的命中点少了：${s.entry.file} / ${s.entry.ident}`)
  console.error(`    声明覆盖 ${s.declared} 处，实际只扫到 ${s.seen} 处。`)
  console.error(`    两种可能，门分不出来，只能由人判断：`)
  console.error(`      ① 判据失明（某个文件没被扫到 —— 检查扫描根、SKIP、walk）`)
  console.error(`      ② 代码真的改了（这几处命中被清掉 ⇒ 这条豁免该退休了）`)
  console.error(`    注意：${s.entry.requires.map(r => r.file).join(' + ')} 的机制复核是通过的，`)
  console.error(`    所以这不是「豁免理由不成立」，而是「豁免的对象不见了/没被看见」。`)
  console.error()
}

for (const v of real) {
  console.log(`  ${v.file}:${v.line}  ${v.expr}`)
  console.log(`      解引用实体: ${v.idents}`)
  console.log(`      祖先链: ${v.ancestors.join('  >  ') || '(无)'}`)
  if (v.ancestorGuards.length) {
    console.log(`      祖先 v-if: ${v.ancestorGuards.map(g => '"' + g + '"').join('  ,  ')} —— 都不足以保证它非空`)
    console.log(`      （"提到了该实体"不算兜底：要看祖先表达式为真时它是否必然非空）`)
  }
}
console.log()

if (real.length > 0) {
  console.error('❌ 存在「实体缺席时恒真」的无效守卫：用户会看到一个正常但按不动的界面。')
  console.error('修法：给该元素（或它的容器）加 v-if="<实体>" 兜底，而不是靠可选链比较。')
  process.exit(1)
}
if (shrunkAllowlist.length > 0) {
  console.error(`❌ ${shrunkAllowlist.length} 条豁免覆盖的命中点比声明的少 —— 清单与现实对不上。`)
  console.error('   在说清是「判据失明」还是「代码真改了」之前，本次结果不能按通过处理。')
  process.exit(1)
}
console.log('✅ 未发现无效的可选链比较型守卫')
process.exit(0)
