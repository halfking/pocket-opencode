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

/** 祖先的 v-if 表达式里是否提到了该标识符（作为独立词）。 */
function mentions(expr, ident) {
  return new RegExp('(^|[^\\w$.])' + ident.replace(/\$/g, '\\$') + '($|[^\\w$])').test(expr)
}

/** 去掉 HTML 注释：注释里出现 <div v-if="a?.b !== 'c'"> 会造成假阳性。 */
function stripHtmlComments(s) {
  return s.replace(/<!--[\s\S]*?-->/g, m => m.replace(/[^\n]/g, ' '))
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

if (files.length === 0) {
  console.error(`❌ 在 ${SRC_ROOT} 下没找到任何 .vue —— 判据失明了（路径错？），拒绝按「通过」处理`)
  process.exit(2)
}

const violations = []
let examined = 0
let lineBasedCount = 0

for (const f of files) {
  const tpl = stripHtmlComments(templateOf(readFileSync(f, 'utf8')))
  // 交叉自检的对照组：用**完全独立**的行级正则数一遍命中数。
  // 两种方法必须一致：对不上说明标签解析器有盲区，此刻任何「通过」都不可信。
  lineBasedCount += (tpl.match(/v-(?:if|show)\s*=\s*"[^"]*?\?\.[^"]*?(?:!==|===)[^"]*"/g) || []).length

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
      const idents = rootIdentities(vIf)
      const gated = [...stack].reverse().some(a => a.guard && [...idents].some(id => mentions(a.guard, id)))
      if (!gated) {
        const line = tpl.slice(0, m.index).split('\n').length
        violations.push({
          file: relative(SRC_ROOT, f).replace(/\\/g, '/'),
          line,
          expr: vIf,
          idents: [...idents].join(','),
          ancestors: stack.slice(-4).map(a => a.tag + (a.guard ? ' v-if="' + a.guard + '"' : '')),
        })
      }
    }
    if (!selfClose) stack.push({ tag, guard: vIf })
  }
}

console.log(`【可选链比较型守卫】扫了 ${files.length} 个 .vue，命中可选链比较型 v-if ${examined} 处`)
console.log(`  交叉自检：行级正则数到 ${lineBasedCount} 处，标签解析器数到 ${examined} 处`)
if (lineBasedCount !== examined) {
  console.error('❌ 两种计数不一致 —— 标签解析器有盲区，本次结果不可信，拒绝按「通过」处理。')
  console.error('   （本脚本第一版就栽在这里：根模板用 indexOf("</template>") 截取，被嵌套的')
  console.error('    <template #slot> 提前截断，TasksView.vue 的 5 处命中被静默丢弃。）')
  process.exit(2)
}
console.log(`其中祖先无同实体门控的：${violations.length} 处（豁免前）`)

// 逐条套用豁免；机制对不上的豁免不生效，并且单独报出来
const staleAllowlist = []
const real = []
for (const v of violations) {
  const entry = ALLOWLIST.find(a => a.file === v.file && v.idents.split(',').includes(a.ident))
  if (!entry) { real.push(v); continue }
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

for (const v of real) {
  console.log(`  ${v.file}:${v.line}  ${v.expr}`)
  console.log(`      解引用实体: ${v.idents}`)
  console.log(`      祖先链: ${v.ancestors.join('  >  ') || '(无)'}`)
}
console.log()

if (real.length > 0) {
  console.error('❌ 存在「实体缺席时恒真」的无效守卫：用户会看到一个正常但按不动的界面。')
  console.error('修法：给该元素（或它的容器）加 v-if="<实体>" 兜底，而不是靠可选链比较。')
  process.exit(1)
}
console.log('✅ 未发现无效的可选链比较型守卫')
process.exit(0)
