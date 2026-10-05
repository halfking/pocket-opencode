// 门禁：返回路径的判定契约（行为 + 接线）。
//
// # 这道门守什么
//
// 2026-10-06 审计 lib/shell 时抓到两个真缺陷，症状都是「按返回没反应」，
// 但它们分别住在**判定**和**记账**两层，单测此前只覆盖了 BackDispatcher
// 的优先级逻辑，没有覆盖「vue-router 适配层怎么判定导航成功」和
// 「四个返回来源是否真的都走同一套裁决」。
//
// ## 缺陷 A（判定层）：pop() 用固定时长猜导航是否成功
//   原实现 `await new Promise(r => setTimeout(r, 0))` 之后读 currentRoute。
//   本仓 72 个路由**全部**是 import() 懒加载，导航必然跨多个宏任务
//   ⇒ 一个宏任务后路径必然还没变 ⇒ pop() 恒 false ⇒ 每次返回记 blocked。
//   四个来源（顶栏返回钮 / Esc / 内容区左滑 / Android backButton）同时失效。
//
// ## 缺陷 B（记账层）：后退被记成 push
//   vue-router v4 的 afterEach(to, from, failure) **不给导航方向**。
//   浏览器后退是 history 前进、路径逐级回退，被 open({openedBy:'push'}) 记下
//   ⇒ entries 单调增长（前进 3 步按 2 次返回：3 条 → 5 条，cursor 2 → 4）
//   ⇒ backDispatcher 的 `ctx.cursor > 0` 恒真 ⇒ 永远到不了 §5 fallback
//      与 §6 交还系统。
//
// # 为什么用「跑真代码」而不是 grep
//
// 只 grep `setTimeout` 或函数名是装饰性护栏：把判据改成别的写法照样绿，
// 而把真代码跑一遍才能证明契约真的成立。所以本门禁**导入真实的
// createShellRuntime**，用一只忠实复刻 vue-router 的假 router 跑场景。
//
// # 判据失明怎么办
//
// 接线侧（四个来源）如果一个都认不出来，本门禁**拒绝给结论**并 exit 3，
// 而不是输出「✓ 合规」—— 失明与通过在输出上必须不同形。
//
// 运行：node scripts/check-back-navigation.mjs [--selftest] [--list]
import { readFileSync, existsSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const SELF = fileURLToPath(import.meta.url)
const ROOT = resolve(dirname(SELF), '..')
const SHELL = join(ROOT, 'frontend', 'src', 'lib', 'shell')
const RUNTIME = join(SHELL, 'runtime.ts')
const LAYOUT = join(ROOT, 'frontend', 'src', 'app', 'AppLayout.vue')

/**
 * 接线判据：四个来源各自路由到哪个执行函数，那个执行函数是否调用 dispatchBack。
 *
 * ⚠️ 早先一版用「命中位置前后 1200 字符里有没有 dispatchBack」——
 *    那是**装饰性护栏**：顶栏返回钮的模板绑定 `@click="goBack"` 在文件头部，
 *    实现 `async function goBack()` 在 400 行之后，窗口根本罩不到，
 *    于是真代码被判「没走 dispatchBack」（假阳性），而门禁自检也一起红。
 *    改成「模板来源 → 执行函数 → 执行函数体内必须有 dispatchBack」，
 *    判据才落在真正的收口点上。
 */
export const BACK_SOURCES = [
  { id: '顶栏返回钮', template: /@click="goBack"/, executor: /(?:async\s+)?function\s+goBack\s*\(/ },
  { id: 'Android backButton', template: /addListener\(\s*'backButton'/, executor: /(?:async\s+)?function\s+submitSystemBack\s*\(/ },
  { id: 'Esc 键', template: /addEventListener\(\s*'keydown'/, executor: /function\s+onKeydown\s*\(/ },
  { id: '内容区左滑', template: /addEventListener\(\s*'touchend'/, executor: /function\s+onTouchEnd\s*\(/ },
]

/** 从 `function foo(` 处起，按大括号配平取出函数体。 */
export function functionBody(src, declRe) {
  const m = declRe.exec(src)
  if (!m) return null
  const start = src.indexOf('{', m.index)
  if (start < 0) return null
  let depth = 0
  for (let i = start; i < src.length; i += 1) {
    const c = src[i]
    if (c === '{') depth += 1
    else if (c === '}') {
      depth -= 1
      if (depth === 0) return src.slice(start, i + 1)
    }
  }
  return null
}

/**
 * 接线判据。
 *
 * 返回 { missing, recognized, blind }：
 *   missing   —— 模板来源在，但执行函数没调用 dispatchBack（真违规）
 *   recognized —— 认出来的来源数
 *   blind     —— 连模板来源都认不出来（判据够不着，**不能**当通过）
 */
export function wiringViolations(src) {
  const missing = []
  let recognized = 0
  for (const s of BACK_SOURCES) {
    if (!s.template.test(src)) continue
    recognized += 1
    const body = functionBody(src, s.executor)
    if (body === null) { missing.push(`${s.id}（找不到执行函数）`); continue }
    if (!/dispatchBack\(/.test(body)) missing.push(`${s.id}→${s.executor.source} 没走 dispatchBack`)
  }
  return { missing, recognized, blind: recognized === 0 }
}

/**
 * 忠实复刻 vue-router 的假 router。
 *
 * 关键两条，缺一条判据就是无效对照：
 *   1. 导航**异步**（懒加载 chunk 用 asyncTicks 控制耗时）；
 *   2. 落定时**触发 afterEach**；被守卫阻止时 failure 非空且路径不变。
 * 判据挂在 afterEach 上，假实现不触发它 ⇒ 必然超时 ⇒ 对照组也红。
 */
export function makeFakeRouter({ asyncTicks = 4, guardBlock = false } = {}) {
  const hooks = { after: [], before: [] }
  const route = { fullPath: '/home', name: 'home', meta: {} }
  const stack = ['/home']
  const settleNav = (target, failure) => {
    if (!failure) route.fullPath = target
    for (const h of hooks.after) h(route, null, failure)
  }
  return {
    route,
    currentRoute: { value: route },
    async push(to) { const p = String(to); stack.push(p); route.fullPath = p },
    async replace(to) { const p = String(to); stack[stack.length - 1] = p; route.fullPath = p },
    back() {
      if (guardBlock) { setTimeout(() => settleNav(route.fullPath, new Error('blocked by guard')), asyncTicks); return }
      stack.pop()
      const target = stack[stack.length - 1]
      let n = 0
      const tick = () => {
        n += 1
        if (n < asyncTicks) { setTimeout(tick, 0); return }
        settleNav(target, null)
      }
      setTimeout(tick, 0)
    },
    afterEach(h) { hooks.after.push(h); return () => {} },
    beforeEach(h) { hooks.before.push(h); return () => {} },
    navigate(fullPath, name) {
      if (fullPath !== route.fullPath) stack.push(fullPath)
      route.fullPath = fullPath; route.name = name; route.meta = { title: fullPath }
      for (const h of hooks.after) h(route, null, null)
    },
  }
}

/**
 * 行为判据。传入 createShellRuntime / dispatchBack 以便负控替换。
 *
 * 返回 [{ name, pass, detail }]。
 */
export async function scenarios(rt) {
  const { createShellRuntime, dispatchBack } = rt
  const out = []
  const add = (name, pass, detail = '') => out.push({ name, pass, detail })

  const stack3 = (opts) => {
    const r = makeFakeRouter(opts)
    const runtime = createShellRuntime(r, { scope: { serverId: 's1', accountId: 'a1' } })
    r.navigate('/home', 'home'); r.navigate('/detail', 'detail'); r.navigate('/note', 'note')
    return { r, runtime }
  }

  // ① 典型懒加载导航：必须判 page-popped，且路径真的回退
  {
    const { r, runtime } = stack3({ asyncTicks: 4 })
    const o = await dispatchBack(runtime)
    add('懒加载导航判 page-popped（不是 blocked）', o.kind === 'page-popped', `实得 ${o.kind}`)
    add('路径真的回退到前驱', r.currentRoute.value.fullPath === '/detail', `实得 ${r.currentRoute.value.fullPath}`)
  }

  // ② 慢网冷 chunk：耗时更长也必须成立
  {
    const { r, runtime } = stack3({ asyncTicks: 16 })
    const o = await dispatchBack(runtime)
    add('慢网导航同样判 page-popped', o.kind === 'page-popped', `实得 ${o.kind}`)
  }

  // ③ 守卫真的拦下：必须仍是 blocked（不许被过度修正成 page-popped）
  {
    const { r, runtime } = stack3({ asyncTicks: 4, guardBlock: true })
    const o = await dispatchBack(runtime)
    add('守卫拦下时仍记 blocked', o.kind === 'blocked', `实得 ${o.kind}`)
    add('守卫拦下时路径不变', r.currentRoute.value.fullPath === '/note', `实得 ${r.currentRoute.value.fullPath}`)
  }

  // ④ 后退必须让账变短（缺陷 B 的直接判据）
  {
    const { runtime } = stack3({ asyncTicks: 4 })
    const before = runtime.store.snapshot()
    await dispatchBack(runtime)
    const s = runtime.store.snapshot()
    add('后退后 entries 变短而非单调增长', s.entries.length < before.entries.length,
      `${before.entries.length} → ${s.entries.length}`)
    add('后退后 cursor 减 1', s.cursor === before.cursor - 1, `${before.cursor} → ${s.cursor}`)
    add('后退记为 pop 而不是 push', s.operations.at(-1)?.type === 'pop', `实得 ${s.operations.at(-1)?.type}`)
  }

  // ⑤ 连续两次返回：逐级回退并归零（cursor 恒 > 0 就到不了 fallback/交还系统）
  {
    const { runtime } = stack3({ asyncTicks: 4 })
    await dispatchBack(runtime)
    await dispatchBack(runtime)
    const s = runtime.store.snapshot()
    add('连续两次返回后 cursor 归零', s.cursor === 0, `实得 ${s.cursor}`)
    add('连续两次返回后栈收缩到起点', s.entries.length === 1, `实得 ${s.entries.length}`)
  }

  return out
}

/** 被测真代码。动态 import 便于负控替换。
 *  ⚠️ Windows 上必须转成 file:// URL：裸绝对路径（'C:\...'）会被 ESM loader
 *     当成未知协议 'c:' 拒掉（ERR_UNSUPPORTED_ESM_URL_SCHEME）。 */
async function loadRealRuntime() {
  const m = await import(pathToFileURL(RUNTIME).href)
  return { createShellRuntime: m.createShellRuntime, dispatchBack: m.dispatchBack }
}

async function selftest() {
  const results = []
  const add = (n, p) => results.push({ name: n, pass: p })

  // 特异度：真代码必须全绿
  const real = await loadRealRuntime()
  const realResults = await scenarios(real)
  const realBad = realResults.filter((r) => !r.pass)
  add(`真代码 ${realResults.length - realBad.length}/${realResults.length} 全绿`, realBad.length === 0)

  // 变盲对照：造一个「旧实现」替身（setTimeout(0) 判定 + 只记 push），
  // 判据必须转红 —— 否则这道门是哑的。
  const buggy = await loadBuggyRuntime()
  const buggyResults = await scenarios(buggy)
  const buggyBad = buggyResults.filter((r) => !r.pass)
  add(`变盲对照：旧实现被判红（${buggyBad.length} 条红）`, buggyBad.length >= 4)

  // 接线敏感度：真 AppLayout 四个来源都在
  const goodLayout = readFileSync(LAYOUT, 'utf8')
  const w0 = wiringViolations(goodLayout)
  add(`接线·真 AppLayout 四个来源都在（认出 ${w0.recognized}/4，缺 ${w0.missing.length}）`,
    w0.recognized === 4 && w0.missing.length === 0)

  // 接线敏感度：逐个来源单独改回各自为政，必须**逐个**被报出来。
  // 早先一版是全局替换 dispatchBack(shellRuntime)，只能证明「有一个会红」，
  // 证明不了四条来源各自都被守。逐个改才守得住「4/4」这个数。
  for (const s of BACK_SOURCES) {
    const broken = goodLayout.replace(s.executor, s.executor.source.replace('function', 'function __broken'))
    const r = wiringViolations(broken)
    const stillFound = r.missing.some((m) => m.startsWith(s.id))
    add(`接线敏感度·单独打断「${s.id}」被报出`, stillFound)
  }

  // 口径：认不出任何来源 ⇒ blind，不能当成通过
  add('口径·认不出来源判 blind 而非通过', wiringViolations('<template><div/></template>').blind === true)

  for (const r of results) console.log(`  ${r.pass ? '通过' : '失败'}  ${r.name}`)
  const bad = results.filter((r) => !r.pass)
  console.log(`\n自检: ${results.length - bad.length}/${results.length} 通过`)
  process.exit(bad.length === 0 ? 0 : 1)
}

/**
 * 负控用的「旧实现」替身：完整复刻 A12 + A13 两个缺陷。
 * 它的唯一作用是证明 scenarios() 有区分度。
 */
async function loadBuggyRuntime() {
  const { NavigationContextStore } = await import(pathToFileURL(join(SHELL, 'navigationContext.ts')).href)
  return {
    createShellRuntime(router) {
      const store = new NavigationContextStore()
      const titles = { beginRender() {}, register() {}, clearAll() {} }
      let scope = { serverId: 's1', accountId: 'a1' }
      const back = {
        overlays: [], inFlight: null,
        setRouter() {},
        reset() {},
        affordance: () => ({ actionKind: 'back', label: '返回', closeable: true }),
        back(ctx) {
          // 缺陷 B 复刻：cursor > 0 时永远走 pop，pop 用 setTimeout(0) 判定
          if (ctx.cursor > 0) {
            const before = router.currentRoute.value.fullPath
            router.back()
            return new Promise((res) => setTimeout(() => {
              res({ kind: router.currentRoute.value.fullPath !== before ? 'page-popped' : 'blocked', reason: 'router-guard' })
            }, 0))
          }
          return Promise.resolve({ kind: 'noop', reason: 'no-predecessor' })
        },
      }
      // 缺陷 B 复刻：afterEach 一律记 push（没有 popTo 分支）
      router.afterEach((to, _from, failure) => {
        if (failure) return
        const route = to
        const cur = store.current()
        const sameEntry = cur && cur.fullPath.split('?')[0] === route.fullPath.split('?')[0]
        store.open({
          fullPath: route.fullPath, presentation: 'page',
          openedBy: sameEntry ? 'replace' : 'push', scope,
          routeName: typeof route.name === 'string' ? route.name : undefined,
          title: route.meta?.title, titleSource: route.meta?.title ? 'route' : 'registered',
        })
      })
      return { store, titles, back, diagnostics: [], setScope(s) { scope = s }, scope: () => scope, dispose() {} }
    },
    dispatchBack: async (runtime) => {
      try { return await runtime.back.back(runtime.store.snapshot()) }
      catch { return { kind: 'blocked', reason: 'exception' } }
    },
  }
}

if (process.argv.includes('--selftest')) {
  await selftest()
} else {
  for (const f of [RUNTIME, LAYOUT]) {
    if (!existsSync(f)) {
      console.error(`FAIL 找不到 ${f} —— 判据失明，拒绝给结论`)
      process.exit(3)
    }
  }

  const rt = await loadRealRuntime()
  const results = await scenarios(rt)
  const bad = results.filter((r) => !r.pass)

  const w = wiringViolations(readFileSync(LAYOUT, 'utf8'))
  if (w.blind) {
    console.error(`FAIL 一个返回来源都认不出来 —— 判据失明，拒绝给结论（不是「通过」）`)
    process.exit(3)
  }

  if (process.argv.includes('--list')) {
    results.forEach((r) => console.log(`  ${r.pass ? '✓' : '✗'} ${r.name}${r.detail ? '  ' + r.detail : ''}`))
    w.missing.forEach((id) => console.log(`  ✗ 接线：${id} 没走 dispatchBack`))
    process.exit(0)
  }

  if (bad.length || w.missing.length) {
    console.error(`\n✗ 返回路径契约被破坏（行为 ${bad.length} 条 / 接线 ${w.missing.length} 条）：`)
    for (const r of bad) console.error(`  - 行为：${r.name}${r.detail ? '  ' + r.detail : ''}`)
    for (const id of w.missing) console.error(`  - 接线：${id} 没走 dispatchBack`)
    console.error('\n契约：')
    console.error('  1. 导航成功与否必须由 vue-router 自己的 afterEach 落定判定，不得用固定时长猜。')
    console.error('     本仓 72 个路由全是懒加载，固定时长必然误判 → 按返回没反应。')
    console.error('  2. 后退必须记成 pop（NavigationContextStore.popTo），不得一律 open({openedBy:\'push\'})。')
    console.error('     否则 entries 单调增长，cursor 恒 > 0，永远到不了 fallback 与交还系统。')
    console.error('  3. 四个返回来源必须都只发意图、由 dispatchBack 统一裁决。')
    process.exit(1)
  }

  console.log(`\n✓ 返回路径契约成立：行为 ${results.length}/${results.length}，四个来源均走 dispatchBack`)
}
