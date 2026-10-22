// check-fetch-bounds.mjs — §951 前端卡口：**每一个出站 fetch 都必须有上界**。
//
// # 这道门在量什么
//
// 一句话：**前端非测试代码里，每一次出站 fetch 都必须能证明「它会结束」。**
//
// # 为什么（§951 的实测依据，不是推理）
//
// `await fetch(...)` 在对端不响应时**永久挂起**。那不只是"慢"——
// 它会击穿依赖状态机的交互：笔记录音 `stop()` 先把 phase 置为 `'stopping'`
// 再做转写 IO，请求不返回 ⇒ phase 永不归位 ⇒ `decideNoteStart` 拒绝新录音、
// `toggle()` 返回 null ⇒ 录音按钮就此失灵。
//
// ★ §950 只管了**后端**的出站 HTTP（它自己的收尾订正里就写明了：
//　 "前端那一族**不是空白**，而是已由既有卡口守住"）——
//　**但那句话只对 `src/api/http.ts` 那条链成立**。
// ⇒ 实测（§951.0 的普查）：前端有 **17 处** `fetch(`（非测试），
//　 其中 **7 处**既没有 `signal` 也没有任何超时 ⇒ 全部无上界：
//
//　 ① `src/api/client.ts` 的 `authFetch` —— **15+ 个模块**在用（任务/会话/实例/网关配置）
//　 ② `src/api/email.ts` 的 3 处发票下载（file / thumb / export）
//　 ③ `src/api/llm-bff.ts` 的局部 `http`（用量/配额）
//　 ④ `src/utils/version.ts` 的 `checkUpdate`（**冷启动**路径）
//　 ⑤ `src/config/api-base.ts` 的 `probeHealthz` 默认实现（设置页「测试连接」）
//　 ⑥ `src/stores/connectivity.ts` 的 `fetchImpl` 透传（移动端同步 ⇒ 离线队列排不空）
//
// ⇒⇒⇒ 处置：新增**唯一**的有界原语 `src/api/fetch-timeout.ts`
//　　（`DEFAULT_TIMEOUT_MS = 30s` + `AbortController` + `TimeoutError`），
//　　上面 7 处全部改走它。
//
// # 口径（为什么这台门能机械判定）
//
//　**A. 枚举**（TypeScript 解析器，不是 grep —— §943/§945/§948/§950 的反复结论）：
//　　每一次「fetch 族调用点」都要登记。三种形态都算：
//　　 ① 直接 `fetch(...)`；
//　　 ② `globalThis.fetch(...)` / `window.fetch(...)`；
//　　 ③ **注入缝**：局部绑定到 fetch 的标识符（`const f = fetch`、
//　　　　 `const f = opts?.fetchImpl ?? globalThis.fetch`、形参默认值 `= fetch`…）
//　　　　 以及本仓惯用的 `fetchImpl` 名字。
//　　⚠ **只认「直接 fetch」是不够的** —— 那正是 §950 漏掉 `new(http.Client)` 的同型事故。
//
//　**B. 判定**每个站点属于哪一类：
//　　① `primitive` —— 走唯一那层（`fetchWithTimeout`）⇒ 有界；
//　　② `self` —— 调用点自己传了 `signal`，且**同一个函数**里有
//　　  `new AbortController` + 一个会 `abort()` 它的 `setTimeout` ⇒ 自证有界；
//　　③ `L1` —— **没有 signal** ⇒ 必须登记在册（L1 台账）；
//　　④ `L2` —— 有 signal 但**不是本地产生**的 ⇒ 上界在调用方 ⇒ 必须登记在册（L2 台账）。
//
//　**C. 两本台账都双向相等 + 绝对条数**，每条必须写清「上界由谁提供」。
//
// # 为什么用「台账」而不是「一律不许 fetch」
//
//　有些 fetch **天生**不是网络请求：
//　 ① `blob:` URL（本地内存）——`src/native/meeting-audio.ts`、
//　 ② SSE 长连接（上界由调用方的 abort signal 提供）——`src/api/llm-buff.ts`。
//　它们是**合法例外**，但必须**登记在册** ⇒ 有人加一条新的例外时会被看见，
//　而不是悄悄多出一个「无上界等待」。
//
// # 范围声明（是「不查」，不是「没风险」）
//
//　① `*.test.ts` / `*.test.mjs` / `__tests__/` 不是生产路径 ⇒ 排除。
//　② 本门只看**前端**（后端那一侧见 backend/internal/repohygiene/smtp_deadline_test.go）。
//　③ ⚠ **本门不检查 `.vue` 模板里的表达式**（只扫 `<script>` 块）；
//　　实测非测试 `.vue` 里 0 处 fetch ⇒ 当前等价，但这是**已知边界**。
//　④ ⚠ **本门不解析跨模块的绑定**（只做**文件内**的局部绑定解析）
//　　⇒ 「某个模块导出的东西其实是个 fetch」这种形态看不见。
//　　⚠ 实测该形态在仓里**不存在**（唯一的跨模块 fetch 绑定是本门认的 `fetchWithTimeout`）。
//
// 用法：
//   node scripts/check-fetch-bounds.mjs          跑判定
//   node scripts/check-fetch-bounds.mjs --list   只打印台账与扫面读数
//
// 退出码：0 = 通过；1 = 判定失败；2 = **拒绝给结论**（量具本身坏了）

import { createRequire } from 'node:module'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const ts = require('typescript')

const HERE = dirname(fileURLToPath(import.meta.url))
const FRONTEND = join(HERE, '..')
const SRC = join(FRONTEND, 'src')

/** 唯一的有界原语（§951）。 */
const PRIMITIVE = 'fetchWithTimeout'
/** 注入缝的惯用名（本仓里它**永远**装的是一个 fetch）。 */
const SEAM_NAME = 'fetchImpl'
/** 全局对象上挂 fetch 的那几种写法。 */
const GLOBAL_OBJECTS = new Set(['globalThis', 'window', 'self', 'global'])

/**
 * L1 台账：**调用点没有 signal** 的站点。
 *
 * ⚠ 每条必须写清「上界由谁提供」—— 不许为空。
 * ⚠ 键是「文件#函数名」（函数名是**身份**，行号是**坐标** ⇒ 行号会漂）。
 */
const L1_LEDGER = [
  {
    key: 'src/native/meeting-audio.ts#blobUrlToBase64',
    why:
      'blob: URL —— URL.createObjectURL 出来的**本地内存**对象，不是网络请求 ⇒ 没有「等一个不会自己结束的东西」这回事。' +
      '（真要给它加上界反而是错的：录音 blob 可能有几百 MB，30s 会误杀。）',
  },
  {
    key: 'src/features/meetings/meeting-final-transcript.ts#refetchFullTranscript',
    why:
      'blob: URL —— 与上一条同族（loadMeetingAudio 返回的是 createObjectURL 的结果）⇒ 本地内存读取，不经网络。',
  },
  {
    key: 'src/native/mobileSyncRuntime.ts#createRuntimeFetch',
    why:
      '**注入缝**：本调用点拿到的是 deps.fetchImpl，生产实现由 src/stores/connectivity.ts 提供 = ' +
      'api/fetch-timeout.ts 的 fetchWithTimeout（30s 上界）。' +
      '⚠ 本门**证明不了**这一点（deps 的生产绑定在另一个文件里）⇒ 登记在册，由人签字。',
  },
]

/**
 * L2 台账：**signal 来自调用方**的站点。
 *
 * ⚠ 这类站点的上界是**调用方**的责任 ⇒ 必须登记在册。
 */
const L2_LEDGER = [
  {
    key: 'src/api/llm-bff.ts#doFetch',
    why:
      'SSE 长连接（`/api/llm/stream`）—— 这条流**天生**没有固定长度 ⇒ 上界由调用方的 abort signal 提供' +
      '（aiStreamRuntime.spawnChat 建的 controller）。' +
      '⚠ 与后端 outbound.NewStreamingClient 的 SSE 例外口**同构**。',
  },
]

/** 扫面读数的下界（防空跑：扫不到东西时「全绿」没有意义）。 */
const MIN_SITES = 8

// ─────────────────────────────────────────────────────────────────────
// 量具
// ─────────────────────────────────────────────────────────────────────

/**
 * 表达式里是否出现了 fetch 形态（`fetch` / `globalThis.fetch` / `x.fetch`）。
 *
 * ⚠⚠ **不钻进函数体**：`const doFetch = async () => fetch(url)` 里，
 *　 `doFetch` 是一个**包装函数**，它本身**不是** fetch
 *　 ⇒ 若把函数体里的 `fetch` 也算进来，`doFetch()` 这个**包装调用**会被误判成
 *　 一个 fetch 站点（实测：llm-bff.ts 的 defaultFetcher 里因此多出 2 个假命中）。
 *　 ⇒ 而函数体**里面**那处真正的 `fetch(...)` 仍会被单独枚举到（它是直接调用）⇒
 *　 **不丢覆盖**，只是不再重复计数。
 */
function containsFetchLike(node) {
  let found = false
  const walk = (n) => {
    if (found || !n) return
    if (ts.isFunctionExpression(n) || ts.isArrowFunction(n) || ts.isFunctionDeclaration(n) ||
        ts.isMethodDeclaration(n) || ts.isGetAccessor(n) || ts.isSetAccessor(n)) return
    if (ts.isIdentifier(n) && n.text === 'fetch') { found = true; return }
    n.forEachChild(walk)
  }
  walk(node)
  return found
}

/**
 * 表达式里是否出现了唯一那层（`fetchWithTimeout`）。
 *
 * ⚠ 同上：不钻进函数体（理由相同）。
 */
function containsPrimitive(node) {
  let found = false
  const walk = (n) => {
    if (found || !n) return
    if (ts.isFunctionExpression(n) || ts.isArrowFunction(n) || ts.isFunctionDeclaration(n) ||
        ts.isMethodDeclaration(n) || ts.isGetAccessor(n) || ts.isSetAccessor(n)) return
    if (ts.isIdentifier(n) && n.text === PRIMITIVE) { found = true; return }
    n.forEachChild(walk)
  }
  walk(node)
  return found
}

/**
 * 收集**文件内**的「局部绑定」：哪些标识符可能装的是 fetch，哪些装的是唯一那层。
 *
 * ⚠ 只做**文件内**解析（不追 import）—— 已知边界，理由见文件头 ④。
 * ⚠ 要**迭代到不动点**：`const a = fetch; const b = a;` 这种传递必须认出来，
 *　　否则 `b(url)` 就看不见了（§950 的同型事故：漏判一族形态）。
 */
function collectBindings(sf) {
  const fetchLike = new Set()
  const primitive = new Set()
  const bindings = []

  const visit = (n) => {
    if (ts.isParameter(n) && n.initializer) {
      bindings.push({ name: n.name.getText(sf), init: n.initializer })
    } else if (ts.isVariableDeclaration(n) && n.initializer && ts.isIdentifier(n.name)) {
      bindings.push({ name: n.name.text, init: n.initializer })
    }
    n.forEachChild(visit)
  }
  visit(sf)

  for (const b of bindings) {
    if (containsPrimitive(b.init)) primitive.add(b.name)
    else if (containsFetchLike(b.init)) fetchLike.add(b.name)
  }
  // 迭代到不动点（传递绑定）。
  for (let round = 0; round < 10; round++) {
    let changed = false
    for (const b of bindings) {
      if (primitive.has(b.name) || fetchLike.has(b.name)) continue
      if (containsPrimitive(b.init)) { primitive.add(b.name); changed = true }
      else if (containsFetchLike(b.init)) { fetchLike.add(b.name); changed = true }
      // ⚠ **标识符 → 标识符**的传递：`const a = fetch; const b = a;`
      //　 `containsFetchLike(b.init)` 认不出来（init 的文本是 "a"，不是 "fetch"）
      //　 ⇒ 必须显式查「init 是不是另一个已知的绑定」（实测：自证臂负控④）。
      else if (ts.isIdentifier(b.init)) {
        if (primitive.has(b.init.text)) { primitive.add(b.name); changed = true }
        else if (fetchLike.has(b.init.text)) { fetchLike.add(b.name); changed = true }
      }
    }
    if (!changed) break
  }
  return { fetchLike, primitive }
}

/** 一个函数体里有没有「会 abort 的 setTimeout」。 */
function hasAbortTimer(fnNode) {
  let hasController = false
  let hasAbortTimerCall = false
  const walk = (n) => {
    if (ts.isNewExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === 'AbortController') {
      hasController = true
    }
    if (
      ts.isCallExpression(n) &&
      ts.isIdentifier(n.expression) &&
      n.expression.text === 'setTimeout' &&
      n.arguments.length >= 1
    ) {
      const cb = n.arguments[0]
      const scan = (m) => {
        if (
          ts.isCallExpression(m) &&
          ts.isPropertyAccessExpression(m.expression) &&
          m.expression.name.text === 'abort'
        ) hasAbortTimerCall = true
        m.forEachChild(scan)
      }
      scan(cb)
    }
    n.forEachChild(walk)
  }
  walk(fnNode)
  return hasController && hasAbortTimerCall
}

/** 一个对象字面量里有没有 `signal:` 字段（值不能是字面量 undefined）。 */
function hasSignalProperty(arg) {
  if (!arg || !ts.isObjectLiteralExpression(arg)) return false
  for (const p of arg.properties) {
    // ⚠ 简写形态 `{ signal }` 也是 `signal:` —— 它是 ShorthandPropertyAssignment，
    //　 **不是** PropertyAssignment ⇒ 只认后者会漏掉一整族（实测：自证臂负控②）。
    if (ts.isShorthandPropertyAssignment(p)) {
      if (p.name.text === 'signal') return true
      continue
    }
    if (!ts.isPropertyAssignment(p)) continue
    const name = p.name
    const key = ts.isIdentifier(name) ? name.text : ts.isStringLiteral(name) ? name.text : null
    if (key !== 'signal') continue
    if (ts.isIdentifier(p.initializer) && p.initializer.text === 'undefined') return false
    return true
  }
  return false
}

/**
 * 分析一个源文件，返回它所有的 fetch 族调用点。
 *
 * @param {string} fileName  仅用于报错显示
 * @param {string} text      源码
 */
function analyzeSource(fileName, text) {
  const sf = ts.createSourceFile(fileName, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
  const { fetchLike, primitive } = collectBindings(sf)
  const sites = []

  /**
   * 当前所处的**作用域名**。
   *
   * ⚠ 命名规则（§951 实测踩出来的）：
   *  · 函数声明 ⇒ 用它自己的名字；
   *  · 箭头/函数表达式 ⇒ 用**它被赋给的那个变量名**（父节点是 VariableDeclaration 时）
   *    或**继承**外层具名函数的名字（父节点是 return / 实参时）。
   *  ⇒ 少了这条，`createRuntimeFetch` 里那个「return 出去的箭头」会被记成 `<module>`。
   */
  let scope = '<module>'

  const visit = (n, parent) => {
    // ⚠ 必须**保存并还原**外层作用域：只 push 不 pop 会让内层名字「漏」到后面的兄弟节点上
    //　 （实测：`const onCallerAbort = () => …` 之后的兄弟节点都被记成了 onCallerAbort）。
    const prevScope = scope
    if (isFunctionLike(n)) {
      scope = scopeNameFor(n, parent, scope)
    }
    if (ts.isCallExpression(n)) {
      const callee = n.expression
      let kind = null
      let calleeName = ''
      if (ts.isIdentifier(callee)) {
        calleeName = callee.text
        if (primitive.has(calleeName)) kind = 'primitive'
        else if (calleeName === 'fetch' || calleeName === SEAM_NAME || fetchLike.has(calleeName)) kind = 'fetch'
      } else if (ts.isPropertyAccessExpression(callee)) {
        calleeName = callee.name.text
        if (calleeName === 'fetch' || calleeName === SEAM_NAME) kind = 'fetch'
      }
      if (kind) {
        const site = {
          file: fileName,
          callee: calleeName,
          hasSignal: hasSignalProperty(n.arguments[1]),
          selfBounded: false,
          scope,
        }
        if (kind === 'fetch') {
          if (!site.hasSignal) site.bucket = 'L1'
          else if (hasAbortTimer(nearestFunction(n))) { site.bucket = 'self'; site.selfBounded = true }
          else site.bucket = 'L2'
        } else {
          site.bucket = 'primitive'
        }
        sites.push(site)
      }
    }
    n.forEachChild((c) => visit(c, n))
    scope = prevScope
  }
  visit(sf, null)
  return { sites, sf }
}

/** 是不是「函数类」的节点。 */
function isFunctionLike(n) {
  return ts.isFunctionDeclaration(n) || ts.isFunctionExpression(n) || ts.isArrowFunction(n) ||
    ts.isMethodDeclaration(n) || ts.isGetAccessor(n) || ts.isSetAccessor(n)
}

/** 给一个函数类节点算作用域名。 */
function scopeNameFor(n, parent, inherited) {
  if (ts.isFunctionDeclaration(n) && n.name && n.name.text) return n.name.text
  // ⚠ 对象字面量里的方法（`async execute(args, ctx) {}`）的父节点是**对象字面量**
  //　 而不是 PropertyAssignment ⇒ 必须单独认它的方法名（否则会记成 <module>）。
  if (ts.isMethodDeclaration(n) && n.name && n.name.text) return n.name.text
  if (parent && ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name)) return parent.name.text
  if (parent && ts.isPropertyAssignment(parent) && ts.isIdentifier(parent.name)) return parent.name.text
  return inherited
}

/** 找离给定节点最近的、**含函数体**的祖先（用于「自证有界」判定）。 */
function nearestFunction(node) {
  let cur = node
  while (cur) {
    if (isFunctionLike(cur) && cur.body) return cur
    cur = cur.parent
  }
  return null
}

// ─────────────────────────────────────────────────────────────────────
// 扫面
// ─────────────────────────────────────────────────────────────────────

/** 取 `.vue` 的 `<script>` 块内容（没有就返回 null）。 */
function vueScript(text) {
  const m = /<script\b[^>]*>([\s\S]*?)<\/script>/i.exec(text)
  return m ? m[1] : null
}

function walkSrc(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) { walkSrc(p, out); continue }
    if (/\.test\.(ts|mjs|js)$/.test(name)) continue
    if (name.endsWith('.d.ts')) continue
    if (!/\.(ts|vue)$/.test(name)) continue
    out.push(p)
  }
  return out
}

function scanAll() {
  const files = walkSrc(SRC)
  const sites = []
  for (const abs of files) {
    const rel = relative(FRONTEND, abs).split(sep).join('/')
    const raw = readFileSync(abs, 'utf8')
    const text = rel.endsWith('.vue') ? vueScript(raw) : raw
    if (text == null) continue
    const { sites: s } = analyzeSource(rel, text)
    sites.push(...s)
  }
  sites.sort((a, b) => (a.file + a.scope).localeCompare(b.file + b.scope))
  return { files: files.length, sites }
}

// ─────────────────────────────────────────────────────────────────────
// 判定
// ─────────────────────────────────────────────────────────────────────

function fail(msg) {
  console.error('❌ ' + msg)
  process.exitCode = 1
}

function refuse(msg) {
  console.error('❌ [拒绝给结论] ' + msg)
  process.exitCode = 2
}

function checkLedger(name, ledger, actual, bucket) {
  const actualKeys = actual.map((s) => `${s.file}#${s.scope}`)
  const registered = ledger.map((r) => r.key)
  const actualSet = new Set(actualKeys)
  const registeredSet = new Set(registered)
  for (const key of actualSet) {
    if (!registeredSet.has(key)) {
      fail(`${name} 台账少了一条：${key}\n   ⇒ 该站点没有自证有界 ⇒ 必须登记「上界由谁提供」`)
    }
  }
  for (const row of ledger) {
    if (!actualSet.has(row.key)) {
      fail(`${name} 台账里登记的 ${row.key} 在源码里**已经没有了**\n   ⇒ 请删掉这条登记（台账只许变少）`)
    }
    if (!row.why || !row.why.trim()) {
      fail(`${name} 台账条目 ${row.key} 没有写「上界由谁提供」`)
    }
  }
  const dup = registered.filter((k, i) => registered.indexOf(k) !== i)
  if (dup.length) fail(`${name} 台账里有重复条目：${[...new Set(dup)].join(', ')}`)
  if (actualKeys.length !== ledger.length) {
    fail(`${name} 台账条数 = ${ledger.length}，实测 ${actualKeys.length} ⇒ 台账与源码不一致`)
  }
  return bucket
}

function main() {
  const listOnly = process.argv.includes('--list')
  const { files, sites } = scanAll()

  const l1 = sites.filter((s) => s.bucket === 'L1')
  const l2 = sites.filter((s) => s.bucket === 'L2')
  const self = sites.filter((s) => s.bucket === 'self')
  const prim = sites.filter((s) => s.bucket === 'primitive')

  if (listOnly) {
    console.log(`扫面：${files} 个文件，${sites.length} 个 fetch 族调用点`)
    for (const s of sites) console.log(`  [${s.bucket.padEnd(9)}] ${s.file}#${s.scope} (${s.callee})`)
    return
  }

  // ── 防空跑：扫不到东西时「全绿」没有意义 ──
  if (files < 50) refuse(`只扫到 ${files} 个文件（前端有几百个）⇒ 扫描范围塌缩了`)
  if (sites.length < MIN_SITES) refuse(`只扫到 ${sites.length} 个 fetch 族调用点（期望 ≥ ${MIN_SITES}）⇒ 量具坏了`)

  // ── ① 唯一那层本身必须自证有界 ──
  const primitiveSites = sites.filter((s) => s.file === 'src/api/fetch-timeout.ts')
  if (primitiveSites.length !== 1) {
    fail(`src/api/fetch-timeout.ts 里的 fetch 调用点 = ${primitiveSites.length} 个（期望 1）\n` +
      `   ⇒ 唯一那层必须自己发请求（它是兜底的最后一层）`)
  }
  const selfInPrimitive = self.filter((s) => s.file.endsWith('src/api/fetch-timeout.ts'))
  if (selfInPrimitive.length !== 1) {
    fail(`src/api/fetch-timeout.ts 的 fetch 调用点没有自证有界（缺 AbortController + setTimeout→abort）\n` +
      `   ⇒ 它是**所有**兜底的最后一层 ⇒ 这里没有上界 = 全盘没有上界`)
  }

  // ── ② 两本台账双向相等 + 绝对条数 ──
  checkLedger('L1', L1_LEDGER, l1, 'L1')
  checkLedger('L2', L2_LEDGER, l2, 'L2')
  if (L1_LEDGER.length !== 3) fail(`L1 台账条数 = ${L1_LEDGER.length}（期望 3）⇒ 台账变了必须重新定性`)
  if (L2_LEDGER.length !== 1) fail(`L2 台账条数 = ${L2_LEDGER.length}（期望 1）⇒ 台账变了必须重新定性`)

  // ── ③ 每个站点都必须落在「有界」或「已登记」两类里 ──
  for (const s of sites) {
    if (s.bucket === 'self' || s.bucket === 'primitive') continue
    const key = `${s.file}#${s.scope}`
    if (s.bucket === 'L1' && !L1_LEDGER.some((r) => r.key === key)) {
      fail(`${s.file}（${s.scope}）是一处**没有 signal** 的 fetch ⇒ 无上界等待`)
    }
    if (s.bucket === 'L2' && !L2_LEDGER.some((r) => r.key === key)) {
      fail(`${s.file}（${s.scope}）的 signal 不是本地产生的 ⇒ 上界在调用方，必须登记在 L2 台账`)
    }
  }

  if (process.exitCode) {
    console.error(`\n[check-fetch-bounds] 扫面：${files} 个文件 / ${sites.length} 个 fetch 站点` +
      `（self ${self.length} / primitive ${prim.length} / L1 ${l1.length} / L2 ${l2.length}）`)
    return
  }
  console.log(`✅ [check-fetch-bounds] ${files} 个文件 / ${sites.length} 个 fetch 站点全部有界` +
    `（self ${self.length} / primitive ${prim.length} / 登记例外 L1 ${l1.length} + L2 ${l2.length}）`)
}

// ─────────────────────────────────────────────────────────────────────
// 自证臂：证明这台量具真的能分辨
// ─────────────────────────────────────────────────────────────────────

function selfTest() {
  const cases = [
    {
      name: '正控：自带 AbortController + setTimeout→abort ⇒ self',
      src: `
async function f(url: string) {
  const c = new AbortController()
  const t = setTimeout(() => c.abort(), 1000)
  const res = await fetch(url, { signal: c.signal })
  clearTimeout(t)
  return res
}`,
      expect: { self: 1, L1: 0, L2: 0 },
    },
    {
      name: '负控①：裸 fetch（没有 signal）⇒ L1',
      src: `
async function f(url: string) {
  return await fetch(url)
}`,
      expect: { self: 0, L1: 1, L2: 0 },
    },
    {
      name: '负控②：有 signal 但**没有**本地计时器 ⇒ L2（上界在调用方）',
      src: `
async function f(url: string, signal: AbortSignal) {
  return await fetch(url, { signal })
}`,
      expect: { self: 0, L1: 0, L2: 1 },
    },
    {
      name: '负控③：局部绑定到 fetch ⇒ 那处调用也必须被看见',
      src: `
async function f(url: string) {
  const g = globalThis.fetch
  return await g(url)
}`,
      expect: { self: 0, L1: 1, L2: 0 },
    },
    {
      name: '负控④：传递绑定（a = fetch; b = a）⇒ 两层都要被看见',
      src: `
async function f(url: string) {
  const a = fetch
  const b = a
  return await b(url)
}`,
      expect: { self: 0, L1: 1, L2: 0 },
    },
    {
      name: '负控⑤：注入缝 fetchImpl（无默认值）⇒ 必须被看见',
      src: `
async function f(url: string, fetchImpl: typeof fetch) {
  return await fetchImpl(url)
}`,
      expect: { self: 0, L1: 1, L2: 0 },
    },
    {
      name: '正控②：绑定到唯一那层 ⇒ primitive（有界，不进台账）',
      src: `
async function f(url: string) {
  const g = fetchWithTimeout
  return await g(url)
}`,
      expect: { self: 0, L1: 0, L2: 0, primitive: 1 },
    },
    {
      name: '负控⑥：注释里写 fetch( 不算数（★ grep 的老坑）',
      src: `
// 这里提到 fetch(url) 只是注释。
async function f() {}`,
      expect: { self: 0, L1: 0, L2: 0 },
    },
  ]

  let bad = 0
  for (const c of cases) {
    const { sites } = analyzeSource('synthetic.ts', c.src)
    const count = (b) => sites.filter((s) => s.bucket === b).length
    const want = c.expect
    const got = { self: count('self'), L1: count('L1'), L2: count('L2'), primitive: count('primitive') }
    const ok =
      got.self === (want.self ?? 0) &&
      got.L1 === (want.L1 ?? 0) &&
      got.L2 === (want.L2 ?? 0) &&
      got.primitive === (want.primitive ?? 0)
    if (!ok) {
      bad++
      console.error(`❌ 自证臂：${c.name}\n   期望 ${JSON.stringify(want)}，实测 ${JSON.stringify(got)}`)
    }
  }
  if (bad) {
    console.error(`\n[check-fetch-bounds] 自证臂 ${bad}/${cases.length} 条不通过 ⇒ 量具坏了（拒绝给结论）`)
    process.exit(2)
  }
  console.log(`✅ [check-fetch-bounds] 自证臂 ${cases.length}/${cases.length} 条通过（量具有牙）`)
}

selfTest()
main()
