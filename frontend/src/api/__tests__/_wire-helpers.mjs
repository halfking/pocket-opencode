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
 */
export async function loadBundle(entry, name) {
  const out = path.join(CACHE, `opk-${name}-${process.pid}.mjs`)
  fs.mkdirSync(CACHE, { recursive: true })
  execFileSync(process.execPath, ['-e', `
    const esbuild = require(${JSON.stringify(ESBUILD)});
    esbuild.build({
      entryPoints: [${JSON.stringify(entry)}],
      bundle: true, format: 'esm', platform: 'browser', write: false,
      external: ['pinia', 'vue'],
      outfile: ${JSON.stringify(out)}, logLevel: 'silent',
    }).then(r => require('fs').writeFileSync(${JSON.stringify(out)}, r.outputFiles[0].contents))
      .catch(e => { console.error(e.message); process.exit(1) })
  `], { cwd: FRONTEND_ROOT, stdio: ['ignore', 'inherit', 'inherit'] })
  return import('file://' + out + `?t=${fs.statSync(out).mtimeMs}`)
}

/** ★ 剥注释用**等长空白替换**，不是删除 —— 删掉会让行号/偏移错位。 */
export const blank = (m) => m.replace(/[^\n]/g, ' ')

/** 剥掉 Go 的注释（块注释 + 行注释）。判据扫源码时**必须**先剥，否则恒真。 */
export const stripGoComments = (t) =>
  t.replace(/\/\*[\s\S]*?\*\//g, blank).split('\n').map((l) => {
    const i = l.indexOf('//')
    return i < 0 ? l : l.slice(0, i)
  }).join('\n')

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