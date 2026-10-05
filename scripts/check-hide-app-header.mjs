// 门禁：hideAppHeader 契约 —— 「声明了隐藏顶栏的视图，必须自备头部」。
//
// # 契约原文（AppLayout.vue:181）
//   「hideAppHeader：视图自带全屏头部（会话工作台等）时隐藏壳层顶栏与全局状态条，
//     避免与视图头部双层堆叠」
//
// # 为什么要机器守
// 违反契约的代价全是静默的，而且**比一般缺陷更难发现**：
//   AppLayout.vue:183  showTopBar = showTopBar !== false && hideAppHeader !== true
//   ⇒ 声明 hideAppHeader 的路由，壳层顶栏**整体不渲染**，连带：
//       · :37 那个 v-if="canGoBack" 的返回按钮消失
//       · 页面标题消失
//   HeaderActionsPortal.vue:48 的渲染条件同样是 hideAppHeader !== true
//       ⇒ 视图通过 portal 注入的顶栏按钮（新建/刷新等）**也一起消失**。
//   视图自己又没有头部 ⇒ 路由声明了 canGoBack: true，用户却**无处可退**。
//
// 2026-10-04 真机实证（Xiaomi 2411DRN47C，/settings/scheduled-tasks）：
//   探针两条负向断言都成立 —— 该页面上 `返回` 不存在、`创建自动化` 不存在；
//   同一轮里该页面的 `仅显示启用`/`刷新` 正常渲染（不是空页）。
//   顺带发现：用户一旦有任务，「创建自动化」在页面上一个都不剩
//   （空态那个随数据消失，顶栏那个被 hideAppHeader 禁用）。
//
// 判据为什么是「有没有自带头部」而不是「有没有返回按钮」：
// 契约本身只要求「自带全屏头部」。有些视图（闪卡）自带头部但**没有返回控件**，
// 那是另一类、更小的问题（页内有标题、用户可从内容里走出去），
// 混进这条判据会造成假阳性。**宁可漏报也不误报** —— 假阳性门禁一周内
// 就会被 --list | head 忽略掉，比没有更糟。
//
// 运行：node scripts/check-hide-app-header.mjs [--selftest] [--list]
import { readFileSync, existsSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const SELF = fileURLToPath(import.meta.url)
const ROOT = resolve(dirname(SELF), '..')
const SRC = join(ROOT, 'frontend', 'src')
const ROUTER = join(SRC, 'app', 'router-mobile.ts')

/** 从 router 源码里抽出全部路由声明。 */
export function routesOf(src) {
  const out = []
  for (const m of src.matchAll(/path:\s*'([^']+)'/g)) {
    const body = src.slice(m.index, m.index + 500)
    const comp = body.match(/component:\s*(?:\(\)\s*=>\s*)?import\('([^']+)'\)|component:\s*([A-Za-z0-9_]+)/)
    const meta = body.match(/meta:\s*\{([^}]*)\}/)?.[1] ?? ''
    out.push({
      path: m[1],
      importPath: comp?.[1] ?? null,
      identifier: comp?.[2] ?? null,
      hideAppHeader: /hideAppHeader:\s*true/.test(meta),
      canGoBack: /canGoBack:\s*true/.test(meta),
    })
  }
  return out
}

/**
 * 顶层标识符 → 组件路径。两种形态都要认：
 *   1. 静态 import：import LoginView from '../features/auth/LoginView.vue'
 *   2. 懒加载声明：const ScheduledTaskListView = () => import('../features/…/X.vue')
 *      （router-mobile.ts 里绝大多数视图是这一种；只认 import 会让 13 条路由
 *        解析不到，然后它们就被静默跳过 —— 门禁因此报「全部合规」。）
 */
export function importAliases(src) {
  const map = new Map()
  for (const m of src.matchAll(/import\s+([A-Za-z0-9_]+)\s+from\s+['"]([^'"]+)['"]/g)) map.set(m[1], m[2])
  for (const m of src.matchAll(/const\s+([A-Za-z0-9_]+)\s*=\s*\(\s*\)\s*=>\s*import\(\s*['"]([^'"]+)['"]\s*\)/g)) map.set(m[1], m[2])
  return map
}

/**
 * 视图是否自带头部。
 * 两种形态都算：<template #outer> 外壳，或模板里直接有 <header>。
 */
export function viewHasOwnHeader(src) {
  if (/<template\s+#outer/.test(src)) return true
  // 只看 <template> 区块内的 <header，避免把脚本注释里的字样算进来
  const tpl = src.slice(0, src.indexOf('</template>') >= 0 ? src.indexOf('</template>') : src.length)
  return /<header[\s>]/.test(tpl)
}

/**
 * 纯判据：哪些路由违反了 hideAppHeader 契约。
 *
 * 返回 { bad, unchecked }：
 *   bad      —— 确认违规（视图解析到了，且没有自备头部）
 *   unchecked —— **声明了 hideAppHeader 但组件没解析到**，判据够不着。
 *
 * unchecked 绝不能当成通过：2026-10-04 实测，路径基准目录写错时
 * 23 条全部落到 unchecked，输出却是「✓ 全部合规」——
 * **判据失明与通过在输出上完全同形**。所以顶层看到 unchecked>0 必须响亮退出。
 */
export function violations(routes, resolveView) {
  const bad = []
  const unchecked = []
  for (const r of routes) {
    if (!r.hideAppHeader) continue
    const view = resolveView(r)
    if (view === null) { unchecked.push(r.path); continue }
    if (!viewHasOwnHeader(view)) {
      bad.push({ path: r.path, canGoBack: r.canGoBack, why: '声明 hideAppHeader 但视图没有自备头部' })
    }
  }
  return { bad, unchecked }
}

function loadView(r, aliases) {
  let rel = r.importPath
  if (!rel && r.identifier) rel = aliases.get(r.identifier)
  if (!rel) return null
  // ⚠️ 基准目录是**路由表所在目录**，不是 frontend/src。
  //   router-mobile.ts 在 src/app/，它的 import 写的是 '../features/x.vue'，
  //   按 src/ 拼会落到 src/../features/…（即 src 外），existsSync 失败 →
  //   loadView 返回 null → 违规被静默跳过 → 门禁报「全部合规」。
  //   这就是它自己注释里写的那个失效形态：判据失明长得和通过一模一样。
  //   （2026-10-04 实际踩到过一轮，23 条全绿而 scheduled-tasks 明明没有头部。）
  const p = join(dirname(ROUTER), rel)
  for (const cand of [p, `${p}.vue`, `${p}.ts`]) {
    if (existsSync(cand) && cand.endsWith('.vue')) return readFileSync(cand, 'utf8')
  }
  return null
}

function selftest() {
  const results = []
  const add = (n, p) => results.push({ name: n, pass: p })
  const R = (hideAppHeader, canGoBack = true) => [{ path: '/x', hideAppHeader, canGoBack }]
  const view = (s) => () => s

  // 敏感度：声明了 hideAppHeader 但视图无头部 ⇒ 必须报
  add('敏感度·无自备头部被报出', violations(R(true), view('<template><div class="page"><button>刷新</button></div></template>')).bad.length === 1)

  // 特异度：视图自备 #outer 外壳 ⇒ 不报
  add('特异度·自带 #outer 外壳不报', violations(R(true), view('<template #outer><header class="head"><h1>标题</h1></header></template>')).bad.length === 0)

  // 特异度：视图自带 <header> ⇒ 不报
  add('特异度·自带 <header> 不报', violations(R(true), view('<template><header class="head"><h1>标题</h1></header><main/></template>')).bad.length === 0)

  // 特异度：没声明 hideAppHeader 的路由完全不参与判据
  add('特异度·未声明 hideAppHeader 不参与', violations(R(false), view('<template><div/></template>')).bad.length === 0)

  // 关键口径：组件解析不到时**不能**算通过，必须进 unchecked
  {
    const r = violations(R(true), () => null)
    add('口径·组件解析不到进 unchecked 而不是通过', r.bad.length === 0 && r.unchecked.length === 1)
  }

  // 口径：#outer 与 <header> 只在模板内才算，脚本注释里的字样不算
  add('口径·脚本注释里的 <header 不算', violations(R(true), view('<template><div/></template>\n<script setup>\n// 以前这里有过 <header class="head">\n</script>')).bad.length === 1)

  const bad = results.filter((r) => !r.pass)
  for (const r of results) console.log(`  ${r.pass ? '通过' : '失败'}  ${r.name}`)
  console.log(`\n自检: ${results.length - bad.length}/${results.length} 通过`)
  process.exit(bad.length === 0 ? 0 : 1)
}

if (process.argv.includes('--selftest')) {
  console.log('[check-hide-app-header] 自检：验证本检查仍能报错')
  selftest()
  process.exit(0)
}

if (!existsSync(ROUTER)) {
  console.error(`FAIL 找不到路由表 ${ROUTER} —— 判据失明，拒绝给结论`)
  process.exit(1)
}
const src = readFileSync(ROUTER, 'utf8')
const routes = routesOf(src)
if (routes.length === 0) {
  console.error('FAIL 一条路由都没解析出来 —— 判据失明，拒绝给结论')
  process.exit(1)
}
const aliases = importAliases(src)
const hidden = routes.filter((r) => r.hideAppHeader)
console.log(`路由总数 ${routes.length}；声明 hideAppHeader: true 的 ${hidden.length} 条`)

const { bad, unchecked } = violations(routes, (r) => loadView(r, aliases))
if (unchecked.length) {
  console.error(`\n✗ ${unchecked.length} 条声明了 hideAppHeader 的路由，组件没解析到 —— 判据够不着，**拒绝给结论**：`)
  unchecked.forEach((p) => console.error(`  - ${p}`))
  console.error('\n这不是「通过」。本门禁只对解析成功的视图负责；把上面的路径补进')
  console.error('scripts/check-hide-app-header.mjs 的解析逻辑，或先确认那些视图是否已改名/已删。')
  process.exit(1)
}
if (process.argv.includes('--list')) {
  bad.forEach((b) => console.log(`  ${b.path} — ${b.why}${b.canGoBack ? '（canGoBack: true ⇒ 页内无路可退）' : ''}`))
  process.exit(0)
}
if (bad.length) {
  console.error(`\n✗ ${bad.length} 条路由声明了 hideAppHeader: true，但视图没有自备头部：`)
  bad.forEach((b) => console.error(`  - ${b.path}${b.canGoBack ? '  （canGoBack: true ⇒ 页内无路可退）' : ''}`))
  console.error('\n契约（AppLayout.vue:181）：hideAppHeader 只用于「视图自带全屏头部」时。')
  console.error('否则壳层顶栏、返回按钮、页面标题、顶栏注入按钮会**一起**消失（AppLayout.vue:37/183、')
  console.error('HeaderActionsPortal.vue:48）。二选一：给视图自带头部，或去掉该路由的 hideAppHeader。')
  process.exit(1)
}
console.log('\n✓ 声明 hideAppHeader 的视图都自备了头部')
