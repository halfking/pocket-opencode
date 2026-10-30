/**
 * _wire-helpers.mjs — 跨语言判据共用的**加载与读源码**工具。
 *
 * ⚠ 为什么要有这个文件（不是洁癖，是实测出来的）：
 *   summary-prompt-parity（§66.4）与本轮的 summary-request-parity（§92）
 *   都需要「用 esbuild 预打包 TS 再 import」「打桩浏览器环境」「剥 Go/TS 注释」
 *   这三件事。第一份把它们**抄**进了自己文件里；本轮照抄就会变成第二份。
 *
 *   而「同一段逻辑抄两遍 ⇒ 同一缺陷必然出现在两处」正是 §80 记过的形状 ——
 *   §80 那次的具体形态是：同一段去重逻辑抄了两份，有测试的那一份有时间轴判据、
 *   没测试的那一份没有，于是同一缺陷只在一处暴露。抄 helper 是同一形状的弱化版：
 *   判据基础设施漂了，两边的门会以不同方式失效，且没人会发现。
 *
 *   ⚠ 反过来说：本文件里的东西**必须真的被两份判据共用**才留得下来。
 *     若将来只剩一个使用者，把那份搬回去并删掉本文件。
 *
 * 命名带下划线前缀：run-mjs-tests.mjs 只认 `.test.mjs` / `.test.ts` 后缀，
 * 所以本文件不会被当成一个没有用例的测试文件跑空。
 */
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))

/** frontend/ 目录（package.json 所在处）。 */
export const FRONTEND_ROOT = path.resolve(HERE, '..', '..', '..')
/** 仓库根。Go 源码要从这里找。 */
export const REPO_ROOT = path.resolve(FRONTEND_ROOT, '..')
/** frontend/node_modules/.cache */
export const CACHE = path.join(FRONTEND_ROOT, 'node_modules', '.cache')

const requireFrom = createRequire(path.join(FRONTEND_ROOT, 'package.json'))
const ESBUILD = requireFrom.resolve('esbuild')

/**
 * 预打包再 import：src/api/* 内部用无扩展名 import，
 * Node 的 ESM 解析器要显式扩展名才能解析。
 *
 * ⚠ outfile 带 pid：两个判据文件在同一进程里各打一次，
 *   同名会互相覆盖，且 import 有缓存 ⇒ 后一个读到前一个的模块。
 *
 * ⚠ 2026-10-29：`stubs` 参数 —— module→源码 的替换表（esbuild onResolve 精确
 *   匹配 import 说明符结尾）。背景：8b34e1c3 给 http/meetings 链加了
 *   blockedModuleWrite（模块级同步策略门），而 syncPolicyStore 在「无激活宿主」
 *   的测试环境里恒判 'local' ⇒ 所有走 meetings POST 的打包判据全体被 409 拦下。
 *   这些判据守的是「请求体形状/提示词契约」，不是策略 ⇒ 用
 *   `stubAlwaysCloudSyncPolicy` 把策略层替成恒 cloud（与 vite.ssrLoadModule +
 *   installSyncPolicyActivationHost 的既有手法等价，只是发生在打包期）。
 */
export async function loadBundle(entry, name, stubs = {}) {
  const out = path.join(CACHE, `opk-${name}-${process.pid}.mjs`)
  fs.mkdirSync(CACHE, { recursive: true })
  const stubDir = path.join(CACHE, 'stubs')
  const stubManifest = []
  for (const [spec, code] of Object.entries(stubs)) {
    fs.mkdirSync(stubDir, { recursive: true })
    const f = path.join(stubDir, `${name}-${spec.replace(/[^a-zA-Z0-9]/g, '_')}.mjs`)
    fs.writeFileSync(f, code, 'utf8')
    stubManifest.push([spec, f])
  }
  // 桩插件整体序列化进子进程脚本：plugin 逻辑在**子进程**里跑，
  // filter 正则按真实 import 说明符转义（锚定结尾）。注意在模板串里
  // 写正则字面量需要双反斜杠 ⇒ 干脆用字符串形式 new RegExp。
  const pluginSrc = stubManifest.length
    ? `
  const stubManifest = ${JSON.stringify(stubManifest)};
  const escapeRe = (s) => s.replace(/[.*+?^'"'"'{}()|[\\]\\\\]/g, String.fromCharCode(92) + '$&');
  const stubPlugin = {
    name: 'wire-helper-stubs',
    setup(b) {
      for (const [spec, file] of stubManifest) {
        const filter = new RegExp(escapeRe(spec) + '$');
        b.onResolve({ filter }, () => ({ path: file }));
      }
    },
  };`
    : ''
  const pluginArg = stubManifest.length ? ',\n      plugins: [stubPlugin]' : ''
  execFileSync(process.execPath, ['-e', `
    const esbuild = require(${JSON.stringify(ESBUILD)});
    ${pluginSrc}
    esbuild.build({
      entryPoints: [${JSON.stringify(entry)}],
      bundle: true, format: 'esm', platform: 'browser', write: false,
      external: ['pinia', 'vue']${pluginArg},
      outfile: ${JSON.stringify(out)}, logLevel: 'silent',
    }).then(r => require('fs').writeFileSync(${JSON.stringify(out)}, r.outputFiles[0].contents))
      .catch(e => { console.error(e.message); process.exit(1) })
  `], { cwd: FRONTEND_ROOT, stdio: ['ignore', 'inherit', 'inherit'] })
  return import('file://' + out + `?t=${fs.statSync(out).mtimeMs}`)
}

/**
 * 恒 cloud 的 syncPolicyStore 桩（打包期替换用）。
 * 只保真 parity 判据会碰到的那几个面：模块模式恒 'cloud'、同步报告为空、
 * 其余导出成可辨识的 no-op（谁用到未覆盖的导出，测试会当场报 undefined）。
 */
export const ALWAYS_CLOUD_SYNC_POLICY_STUB = `
export const SYNC_POLICY_CACHE_KEY = 'op_sync_policy_v1'
export const SYNC_POLICY_LOCAL_INTENTS_KEY = 'op_sync_policy_local_intents_v1'
export const SYNC_REPORT_CACHE_KEY = 'op_sync_report_v1'
export function readCachedModuleMode() { return 'cloud' }
export function recordSyncReportEntry() {}
export function readCachedSyncReport() { return [] }
export function shouldSync() { return true }
export function moduleMode() { return 'cloud' }
export function readCachedSyncPolicy() { return null }
export function installSyncPolicyActivationHost() {}
export function installSyncPolicyStorage() {}
export function installSyncPolicyScopeResolver() {}
export function useSyncPolicyStore() { return {} }
export const SYNC_MODULES = []
export function isSyncModule() { return false }
export function normalizeSyncPolicy(p) { return p }
export function isPortalSyncModule() { return false }
`

/**
 * 恒「直接权威」的 auth 桩（打包期替换用）。
 *
 * ⚠ 2026-10-30：aa8a6dfb 起 http() 对**非直接权威**（authMethod 不在
 *   password/legacy/dev-bypass 里）的请求走资源凭据交换，而 meetings/notes/llm
 *   路径的 scope 是 'unavailable' ⇒ 打包判据里那个「没登录态的裸 store」
 *   （authMethod=''）一进 summarize/refine 就吃 PocketResourceUnavailableError。
 *   这些判据守的是「请求体形状/提示词契约」，不是鉴权面 ⇒ 桩成**直接权威**，
 *   让请求走到 fetch 打桩层。会话函数（capture/assert）从 stores/auth.ts 原样
 *   抄语义：WeakMap 代际 + 「字段变了代际作废」——只是没有 vue watch，
 *   代际不会被动作废，单发请求的判据用不到。
 */
export const ALWAYS_DIRECT_AUTH_STUB = `
import { defineStore } from 'pinia'
const sessions = new WeakMap()
export function sessionState(auth) {
  let state = sessions.get(auth)
  if (!state) { state = { generation: {}, applyingRefresh: false, disposed: false }; sessions.set(auth, state) }
  return state
}
export function captureAuthAttempt(auth) { return { owner: auth, generation: sessionState(auth).generation } }
export function assertAuthAttempt(auth, session) {
  const state = sessionState(auth)
  if (session.owner === auth && !state.disposed && state.generation === session.generation) return
  throw Object.assign(new Error('Sign-in attempt was superseded.'), { name: 'AbortError' })
}
export function captureAuthSession(auth) {
  if (auth.activationStatus !== 'ready') throw Object.assign(new Error('Session activation is not ready.'), { name: 'AbortError' })
  return captureAuthAttempt(auth)
}
export function isCurrentAuthSession(auth, session) {
  const state = sessionState(auth)
  return auth.activationStatus === 'ready' && session.owner === auth && !state.disposed && state.generation === session.generation
}
export function assertAuthSession(auth, session) {
  if (isCurrentAuthSession(auth, session)) return
  const error = new Error('The session changed before the request completed.')
  error.name = 'AbortError'
  throw error
}
export const useAuthStore = defineStore('auth', {
  state: () => ({ token: 'stub-direct-token', user: 'stub-user', userId: 'stub-user', workspaceId: 'stub-workspace', authMethod: 'password', activationStatus: 'ready' }),
  actions: {
    maybeRefresh() { return Promise.resolve() },
    refreshSession() { return Promise.resolve() },
  },
})
`

/** ★ 剥注释用**等长空白替换**，不是删除 —— 删掉会让行号/偏移错位。 */
export const blank = (m) => m.replace(/[^\n]/g, ' ')

/**
 * 剥掉 Go 的注释（块注释 + 行注释）。判据扫源码时**必须**先剥，否则恒真。
 *
 * ⚠⚠⚠ §930：这一份是**唯一**的 quote-aware 实现；仓里此前有 **5 份拷贝**
 *   （本文件、`api-timeout-budget-table.test.mjs`、`email-long-request-budget.test.mjs`、
 *   `refine-prompt-parity.test.mjs`、`summary-prompt-parity.test.mjs`），
 *   全都是「先做块注释的非贪婪配对、再按行首双斜杠切行」
 *   ⇒ **字符串字面量里的斜杠会被当成注释起点** ⇒ ⇒ **吞掉真代码**。
 *
 *   实测的两个地雷：
 *   ① `backend/internal/server/server.go:103` 的**注释里**写着一条带通配符的
 *      路径 ⇒ 块注释配对从那里一直吃到下一个结束符
 *      ⇒ **把后面整个 const 块吃掉** ⇒ 判据读到「后端没有这个常量」
 *      ⇒ **量具坏了却像是后端改了**（`email-long-request-budget.test.mjs`
 *      的 `goCodeLines` 头注释记着这件事）。
 *   ② `server.go` 的 `longLivedPatterns` 里是**字符串字面量**（路径中带通配符）
 *      ⇒ 后面紧跟的另一个字面量里也有结束符 ⇒ 朴素的块注释正则会把
 *      **这两行名单**吃掉 ⇒ **读出来的名单少三行**（§930 探针实测）。
 *
 * ⇒ 本实现走**状态机**：字符串字面量里的斜杠一律不是注释。
 *   ⚠ 注释替换成**等长空白**（保行数与偏移）—— 判据常按行号定位。
 *
 * ⚠ 写这段注释时踩过一次：正文里若出现**块注释的结束符**，会把自己的注释
 *   提前闭合 ⇒ 下一个词被当成代码 ⇒ SyntaxError。⇒ 注释里别写结束符。
 */
export const stripGoComments = (t) => {
  const src = String(t)
  let out = ''
  let i = 0
  const n = src.length
  while (i < n) {
    const c = src[i]
    // ① 先判字符串字面量（Go 有双引号串、反引号原始串、字符字面量）
    if (c === '"' || c === '`' || c === "'") {
      const quote = c
      out += c
      i += 1
      while (i < n) {
        if (quote !== '`' && src[i] === '\\') {
          out += src.slice(i, i + 2)
          i += 2
          continue
        }
        out += src[i]
        if (src[i] === quote) {
          i += 1
          break
        }
        i += 1
      }
      continue
    }
    // ② 行注释
    if (c === '/' && src[i + 1] === '/') {
      while (i < n && src[i] !== '\n') {
        out += ' '
        i += 1
      }
      continue
    }
    // ③ 块注释
    if (c === '/' && src[i + 1] === '*') {
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) {
        out += src[i] === '\n' ? '\n' : ' '
        i += 1
      }
      if (i < n) {
        out += '  '
        i += 2
      }
      continue
    }
    out += c
    i += 1
  }
  return out
}

/** 从 URL 里取 pathname 做**精确**比较（子串匹配量的是形状，不是同一个端点）。 */
export function pathnameOf(u) {
  try { return new URL(String(u)).pathname } catch { return String(u) }
}

/** 装一个够 src/api 跑起来的最小浏览器环境。 */
export function stubBrowser() {
  globalThis.localStorage = {
    getItem: () => null, setItem: () => {}, removeItem: () => {},
    key: () => null, get length() { return 0 },
  }
  globalThis.window = {
    addEventListener() {}, removeEventListener() {}, removeAllListeners() {},
    location: { href: 'https://localhost/', origin: 'https://localhost' },
    navigator: { userAgent: 'node-test' },
  }
}

/**
 * 激活一个空的 pinia。
 *
 * ⚠ 必需，不是可选：http.ts 的 `httpOnce()` **第一行**就是 `useAuthStore()`，
 *   而那里没有 try/catch。pinia 没激活时它抛
 *   「getActivePinia() was called with no active Pinia」，
 *   这个异常会被调用方的 catch 吞掉并走降级链 ——
 *   于是「fetch 一个都没打」，而**返回值的形状仍然合法**（emptySummary）。
 *
 *   ★ 这个坑的形态特别阴：判据写成「抓请求体 + 断言字段」时，
 *     漏了这步会得到「一个都没打」而不是某个字段不对，
 *     但如果判据只断言返回值，就会**全绿且毫无意义**。
 *   ⇒ 凡是要真的跑 src/api 的判据，都必须先调这个函数。
 */
export async function activatePinia() {
  const { createPinia, setActivePinia } = await import('pinia')
  setActivePinia(createPinia())
}

const realFetch = globalThis.fetch
const realWindow = globalThis.window
const realLocalStorage = globalThis.localStorage

/** 还原全局环境。⚠ 必须放在 afterEach，否则下一组用例继承污染的 fetch。 */
export function restoreGlobals() {
  globalThis.fetch = realFetch
  globalThis.window = realWindow
  globalThis.localStorage = realLocalStorage
}

/**
 * 打桩 fetch：记录每个请求的 **URL、pathname 与解析后的请求体**，
 * 并按 `routes` 里的 pathname → 状态码/响应体应答。
 *
 * ⚠ 记**解析后的 body**（JSON.parse 过），不是原始字符串 ——
 *   判据要断言的是「发出去的那个对象」，断言原始字符串等于在断言格式。
 */
export function captureRequests(routes = {}) {
  const seen = []
  globalThis.fetch = async (url, init) => {
    const path = pathnameOf(url)
    seen.push({
      url: String(url),
      path,
      body: init?.body ? JSON.parse(String(init.body)) : null,
    })
    const route = routes[path]
    const status = typeof route === 'number' ? route : (route ? 200 : 404)
    const payload = route && typeof route === 'object' ? route : { error: 'no route' }
    return {
      ok: status >= 200 && status < 300,
      status,
      headers: { get: (k) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
      async json() { return payload },
    }
  }
  return seen
}

/** 断言「seen 里确实打过某个端点」—— 判据不许靠「没调用就当没违规」过关。 */
export function assertHit(seen, pathname) {
  const hit = seen.find((s) => s.path === pathname)
  assert.ok(hit,
    `没有打到 ${pathname}（实际打了 ${[...new Set(seen.map((s) => s.path))].join(', ') || '一个都没打'}）` +
    ' —— 判据不能靠「没调用就当没违规」过关')
  return hit
}