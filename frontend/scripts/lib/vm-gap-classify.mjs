// vm-gap-classify.mjs —— ViewModel 缺口的**唯一判定实现**，门与审计脚本共用。
//
// 为什么要有这个文件（2026-10-07）
// --------------------------------
// `check-viewmodel-gaps.mjs` 的文件头写着「与 audit-viewmodel-gaps.mjs 共用判定逻辑，
// 避免重复扫描实现漂移」——**实测没有任何共用**。两份文件里那 15 行正则逐字相同，
// 是复制粘贴；而且那份审计脚本根本不在 gates.json 里。
// ⇒ 注释承诺的正是这条缺陷的成因。修法不是改注释，是把判定抽出来真的共用。
//
// 判据测的是「有没有 ViewModel」，不是「有没有 useXxx( 这个形状」
// ----------------------------------------------------------------
// 旧判据 `/\buse[A-Z]\w*\s*\(/` 的假通过（实测；夹具在 check 脚本的 SELF_TEST 里）：
//   · `useRouter()`（vue-router 内建，几乎每个视图都有）单独就能把缺口"消掉"
//   · composable 名字只出现在 `// TODO: 抽成 useXxxViewModel(...)` 注释里也能消掉
//
// 所以现在这样判：把 `useXxx` 这个名字**解析到它的定义**，再看那个定义自己是不是
// **一条数据通路的持有者**。解析不到、或解析到了但不持有数据通路 ⇒ 视图仍算缺口。
//
// ★ 「数据通路」不能按目录划（2026-10-07 实测两次打脸）
//   ① `/api/` 里混着数据面与纯工具：`src/api/error-message.ts` 131 行全是文案与正则，
//      一个 fetch 都没有，而 `useApiError` 只 import 了它 ⇒ 按目录判会把
//      `useApiError` 错判成数据通路，于是「只用 useApiError + 直接 api.list() 」的
//      视图被错误清空。
//   ② store 身份会沿 import 传播：`useStatusBar` import `stores/theme` 只是为了配色，
//      按「住在 stores/ 或间接碰到 store」判，它会被错判成数据通路。
//
//   ⇒ 改成两个有证据的口径：
//      · **终端 I/O 模块** = 定义文件自身含 `fetch(` / `XMLHttpRequest` / `WebSocket`
//        / `EventSource`。实测本仓只有 client.ts、http.ts、email.ts、llm-bff.ts、
//        websocket*.ts 命中；其余 api/*.ts 靠 import 边连到它们。
//      · composable 持有数据通路 = ①**自身定义**是 store（`defineStore(`，
//        或住在 `src/stores/` 下）或 ② 自身定义**传递闭包**里能到终端 I/O 模块。
//        store 身份**只在自身定义上认**，不沿 import 传播 —— 这正是 ② 的由来。

/**
 * 枚举时跳过的目录名。**单一来源**：门与审计脚本都从这里取。
 *
 * ★ 之前两份各写各的，差一个 `android`/`ios` ⇒ 同一个仓两道工具数出来的模块数
 *   439 与 440 不一样（实测）。「判定同源」如果连**枚举集合**都不同源，
 *   那就只是判定同源、读数不同源 —— 照样会让人对不上账。
 */
export const SCAN_SKIP = new Set(['node_modules', '__tests__', '.git', 'dist', 'android', 'ios'])

/** 终端 I/O 模块的判据：定义文件自身发起 I/O。 */
const TERMINAL_IO_RE = /\b(?:fetch\s*\(|XMLHttpRequest|new\s+WebSocket|new\s+EventSource)/

/** 运行时 import 的 specifier（type-only 已剔除）。 */
function runtimeSpecifiers(src) {
  let s = src
  s = s.replace(/^[ \t]*import\s+type\s[\s\S]*?from\s+['"][^'"]+['"];?[ \t]*$/gm, '')
  s = s.replace(/^([ \t]*import\s*\{)([\s\S]*?)(\}\s*from\s*['"][^'"]+['"];?)$/gm, (m, head, body, tail) => {
    const kept = body
      .split(',')
      .map(x => x.trim())
      .filter(x => x && !/^type\s/.test(x))
      .join(', ')
    return kept ? head + ' ' + kept + ' ' + tail : ''
  })
  const out = []
  const re = /from\s+['"]([^'"]+)['"]/g
  let m
  while ((m = re.exec(s)) !== null) out.push(m[1])
  return out
}

/**
 * 剔除 type-only import。**单行正则 `[^}]*` 不跨行** ⇒ 多行
 * `import type {\n A,\n B\n} from '../../api/…'` 剔不掉，被算成运行时依赖；
 * 内联 `import { type Task, api } from '…'` 同样剔不掉。
 * 这是**误报方向**（更严），但它让「数据层引用数」不可信，而那个数正是基线棘轮的 key。
 */
export function stripTypeOnlyImports(src) {
  let s = src
  s = s.replace(/^[ \t]*import\s+type\s[\s\S]*?from\s+['"][^'"]+['"];?[ \t]*$/gm, '')
  s = s.replace(/^([ \t]*import\s*\{)([\s\S]*?)(\}\s*from\s*['"][^'"]+['"];?)$/gm, (m, head, body, tail) => {
    const kept = body
      .split(',')
      .map(x => x.trim())
      .filter(x => x && !/^type\s/.test(x))
      .join(', ')
    return kept ? head + ' ' + kept + ' ' + tail : ''
  })
  return s
}

function normalizeSpec(spec) {
  return spec.replace(/^@\//, './')
}

/** 把 import specifier 解析成 srcRoot 下的相对路径；解析不出返回 null。 */
function resolveSpec(fromRel, spec, known) {
  const s = normalizeSpec(spec)
  if (!s.startsWith('.')) return null
  const base = fromRel.split('/').slice(0, -1)
  for (const seg of s.split('/')) {
    if (seg === '.' || seg === '') continue
    if (seg === '..') base.pop()
    else base.push(seg)
  }
  const joined = base.join('/')
  for (const cand of [joined, joined + '.ts', joined + '.js', joined + '/index.ts', joined + '/index.js']) {
    if (known.has(cand)) return cand
  }
  return null
}

/**
 * 建模块图 + 「谁持有数据通路」的判定。
 *
 * @param {{rel:string, src:string}[]} modules srcRoot 下全部 .ts/.js
 * @returns {{composables:Map<string,{file:string,holdsData:boolean}>,
 *            holdsData:(rel:string)=>boolean, modules:Map<string,string>}}
 */
export function buildDataPlane(modules) {
  const byRel = new Map(modules.map(m => [m.rel, m.src]))
  const imports = new Map()
  for (const { rel, src } of modules) {
    const edges = []
    for (const spec of runtimeSpecifiers(src)) {
      const t = resolveSpec(rel, spec, byRel)
      if (t) edges.push(t)
    }
    imports.set(rel, edges)
  }
  const terminal = new Set()
  for (const [rel, src] of byRel) if (TERMINAL_IO_RE.test(stripTypeOnlyImports(src))) terminal.add(rel)

  // ★ 两个 memo **必须分开**，而且环检测的「进行中」标记**不能写进结果 memo**
  //   （2026-10-07 实测：这是本轮最难查的一个 bug）。
  //   第一版把 `holdsData` 的环占位 `memo.set(rel, false)` 和 `reachesTerminal`
  //   的结果记在**同一个 map** 里 ⇒ `reachesTerminal` 一进来就命中
  //   `memo.has(rel)`、直接返回那个占位 `false`
  //   ⇒ **每一个**模块都被判成「不持有数据通路」，只有 store 因为 `||` 短路才为真。
  //   读数长得完全正常（77 个名字、20/57 分组），没有任何报错 ——
  //   而且方向是**少报缺口**，正好是最不该出错的方向。
  const tMemo = new Map() // rel -> 是否可达终端（**只在子树探索完后写**）
  const hMemo = new Map() // rel -> 是否持有数据通路（与 tMemo 严格分开）

  /** 后序记忆化的可达性判定；`seen` 是当前路径栈，回溯时删掉。 */
  function reachesTerminalFrom(start) {
    const seen = new Set()
    function dfs(rel) {
      if (seen.has(rel)) return false // 环：这条边不贡献结论
      if (tMemo.has(rel)) return tMemo.get(rel)
      if (terminal.has(rel)) return true
      seen.add(rel)
      let res = false
      for (const t of imports.get(rel) || []) {
        if (dfs(t)) { res = true; break }
      }
      seen.delete(rel)
      tMemo.set(rel, res)
      return res
    }
    return dfs(start)
  }

  function isOwnStore(rel) {
    const src = byRel.get(rel) || ''
    const d = rel.replace(/\\/g, '/')
    return /\bdefineStore\s*\(/.test(stripTypeOnlyImports(src)) || /(^|\/)src\/stores\//.test(d)
  }
  /** 自身定义是否**持有数据通路**（store 身份只在这里认，不沿 import 传播）。 */
  function holdsData(rel) {
    if (hMemo.has(rel)) return hMemo.get(rel)
    const v = isOwnStore(rel) || reachesTerminalFrom(rel)
    hMemo.set(rel, v)
    return v
  }

  // composable 名字索引：名字 -> { file, holdsData }
  const composables = new Map()
  for (const { rel, src } of modules) {
    const names = new Set()
    for (const m of src.matchAll(/export\s+(?:async\s+)?function\s+(use[A-Z]\w*)\s*\(/g)) names.add(m[1])
    for (const m of src.matchAll(/(?:^|\s)(?:export\s+)?(?:const|let|var)\s+(use[A-Z]\w*)\s*=/g)) names.add(m[1])
    for (const m of src.matchAll(/export\s*\{([^}]*)\}/g)) {
      for (const part of m[1].split(',')) {
        const p = part.trim()
        if (/^use[A-Z]\w*$/.test(p)) names.add(p)
        else {
          const as = p.match(/^use[A-Z]\w*\s+as\s+(\w+)$/)
          if (as) names.add(as[1])
        }
      }
    }
    const hd = holdsData(rel)
    for (const n of names) {
      const prev = composables.get(n)
      // 同名多处定义：只要**有一处**持有数据通路就算数（保守 ⇒ 少报缺口）
      if (!prev) composables.set(n, { file: rel, holdsData: hd })
      else if (!prev.holdsData && hd) composables.set(n, { file: rel, holdsData: true })
    }
  }
  return { composables, holdsData, modules: byRel, terminals: terminal }
}

/**
 * 视图里指向数据面的**运行时** import 条数。
 *
 * 数据面 = `/api/`、`/stores/`、`/services/`。★ `/services/` 必须在里面：
 * 实测 `src/services/learning.ts` 自己 `import { http } from '../api/http'`，
 * 旧判据完全不认识这条通道 ⇒ 只经 services/ 拿数据的视图会**整份不被计数**。
 * 这份清单是**欠近似就是失明**，宁可多算（误报方向）也不能少算。
 */
export function dataLayerRefs(src) {
  let api = 0
  let store = 0
  let service = 0
  for (const spec of runtimeSpecifiers(src)) {
    const t = normalizeSpec(spec)
    if (/(^|\/)stores(\/|$)/.test(t)) store += 1
    else if (/(^|\/)services(\/|$)/.test(t)) service += 1
    else if (/(^|\/)api(\/|$)/.test(t)) api += 1
  }
  return { api, store, service, total: api + store + service }
}

export function localComposableNames(src) {
  const names = new Set()
  for (const m of src.matchAll(/export\s+(?:async\s+)?function\s+(use[A-Z]\w*)\s*\(/g)) names.add(m[1])
  for (const m of src.matchAll(/(?:^|\s)(?:export\s+)?(?:const|let|var)\s+(use[A-Z]\w*)\s*=/g)) names.add(m[1])
  return names
}

export function calledComposableNames(src) {
  return new Set([...src.matchAll(/\b(use[A-Z][A-Za-z0-9_]*)\s*\(/g)].map(m => m[1]))
}

/**
 * 核心判定：**一个视图是不是 ViewModel 缺口**。
 *
 * @param {object} o
 * @param {string} o.rel   相对路径（基线 key 的一部分，**不含行号**）
 * @param {string} o.src   视图源码
 * @param {Map}   o.composables buildDataPlane().composables
 */
export function classifyVue({ rel, src, composables }) {
  const refs = dataLayerRefs(src)
  const called = calledComposableNames(src)
  const local = localComposableNames(src)
  const mediating = []
  const notMediating = []
  const unresolved = []
  for (const name of [...called].sort()) {
    if (local.has(name)) { notMediating.push(name); continue }
    const def = composables.get(name)
    if (!def) { unresolved.push(name); notMediating.push(name); continue }
    if (def.holdsData) mediating.push(name)
    else notMediating.push(name)
  }
  const isGap = refs.total > 0 && mediating.length === 0
  const why =
    refs.total === 0
      ? '不经数据面，无缺口'
      : mediating.length > 0
        ? `经数据通路：${mediating.join(', ')}`
        : `直连数据面（api=${refs.api} stores=${refs.store} services=${refs.service}），` +
          `用到的 composable 都不持有数据通路：${notMediating.join(', ') || '（无）'}` +
          (unresolved.length ? `；解析不到定义：${unresolved.join(', ')}` : '')
  return {
    rel,
    apiCount: refs.api,
    storeCount: refs.store,
    serviceCount: refs.service,
    mediating,
    notMediating,
    unresolved,
    isGap,
    why,
  }
}