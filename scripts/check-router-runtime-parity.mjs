#!/usr/bin/env node
// 门禁：**文本解析口径必须与 vue-router 运行期实际持有值逐条一致**。
//
// # 存在理由（2026-10-06）
// scripts/check-hide-app-header.mjs 用**固定 500 字符窗口**从每条 `path:` 往后取
// `meta: {…}`。这个口径今天是准的，但它有一个**当前未爆、已存在的洞**：
// 只要有人把一段长注释写在 `path:` 和 `meta:` 之间（>500 字符），
// `meta` 会被挤出窗口 ⇒ 该路由的 `hideAppHeader: true` **被静默漏掉**
// ⇒ 门禁既不报错也不计入声明数，而是报「全部合规」。
// **判据失明与通过在输出上完全同形** —— 这是本仓最贵的一类缺陷。
//
// 本门禁不判断「该不该声明」，只判断**「文本口径和运行期口径说的是不是同一件事」**：
// 把 router 真正编译执行一次，读 vue-router 实际持有的 routes[]，与文本解析逐条比对。
// 两个口径一旦分叉就报红。文本口径可以被质疑（换个解析器就数出别的数），
// 运行期读数不能 —— 所以这个比对是唯一能把「口径之争」变成「事实」的东西。
//
// 历史背景：同一份 router-mobile.ts 曾被四种文本口径数出三个不同的数
// （grep=16 / path 分段=16 / 500 窗口+meta 捕获=15），
// 根因统一是「按行/按段找字面量会把注释里的字面量算成声明」。
// 权威读数是运行期的 15。
//
// 运行：node scripts/check-router-runtime-parity.mjs [--selftest]
// 退出码：0 = 两种口径一致；1 = 分叉（门禁失明）；2 = 依赖不可用/解析失败（同样拒绝给结论）
import { readFileSync, writeFileSync, unlinkSync, existsSync, mkdtempSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { routesOf } from './check-hide-app-header.mjs'

const require = createRequire(import.meta.url)
const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = resolve(HERE, '..')
const FRONT = join(ROOT, 'frontend')
const ROUTER = join(FRONT, 'src', 'app', 'router-mobile.ts')

/**
 * 真正编译并执行 router，读取 vue-router 实际持有的路由。
 * @param {string} entry esbuild 入口（绝对路径）
 * @returns {Promise<{total:number, hidden:string[], allPaths:string[]}>}
 */
async function runtimeRoutes(entry) {
  const { build } = await import(require.resolve('esbuild', { paths: [FRONT] }))
  const res = await build({
    entryPoints: [entry],
    bundle: true, format: 'esm', platform: 'node', write: false,
    absWorkingDir: FRONT, logLevel: 'silent',
    plugins: [{
      name: 'stub-vue',
      setup(b) {
        // .vue 打桩：本门禁只关心 meta，不关心视图渲染
        b.onResolve({ filter: /\.vue$/ }, () => ({ path: 'v', namespace: 'stub' }))
        b.onLoad({ filter: /.*/, namespace: 'stub' }, () => ({
          contents: 'export default { name: "Stub", render(){return null} }', loader: 'js' }))
      },
    }],
    define: { 'import.meta.env.DEV': 'false', 'import.meta.env.PROD': 'true' },
  })
  const tmp = join(mkdtempSync(join(tmpdir(), 'routerparity-')), 'router.mjs')
  writeFileSync(tmp, res.outputFiles[0].text)

  // createWebHashHistory 需要最小 DOM 全局。node 22 下 navigator 是只读 getter，
  // 不要赋值（会抛 TypeError）—— node 自带一个够用。
  const noop = () => {}
  globalThis.location ??= { href: 'http://localhost/#/', hash: '#/', pathname: '/', search: '', host: 'localhost', protocol: 'http:' }
  globalThis.history ??= { state: null, replaceState: noop, pushState: noop, scrollRestoration: 'auto' }
  globalThis.document ??= {
    baseURI: 'http://localhost/', querySelector: () => null,
    addEventListener: noop, removeEventListener: noop,
    createElement: () => ({ setAttribute: noop, style: {}, appendChild: noop }),
  }
  globalThis.window ??= globalThis
  globalThis.addEventListener ??= noop
  globalThis.removeEventListener ??= noop
  const store = () => { const m = new Map(); return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k) } }
  globalThis.localStorage ??= store()
  globalThis.sessionStorage ??= store()

  const mod = await import(tmp)
  const routes = mod.default.options.routes
  return {
    total: routes.length,
    hidden: routes.filter((r) => r.meta && r.meta.hideAppHeader === true).map((r) => r.path),
    allPaths: routes.map((r) => r.path),
  }
}

/** 两种口径的分叉清单。空数组 = 一致。 */
export function diff(textRoutes, rt) {
  const textAll = textRoutes.map((r) => r.path)
  const textHidden = textRoutes.filter((r) => r.hideAppHeader).map((r) => r.path)
  const missingFromText = rt.hidden.filter((p) => !textHidden.includes(p))
  const onlyInText = textHidden.filter((p) => !rt.hidden.includes(p))
  const routesLostByText = rt.allPaths.filter((p) => !textAll.includes(p))
  const routesInventedByText = textAll.filter((p) => !rt.allPaths.includes(p))
  return { missingFromText, onlyInText, routesLostByText, routesInventedByText }
}

/**
 * 在 **router 同目录** 放一个临时副本跑变异，而不是改真实文件。
 * 同目录 ⇒ `../features/x.vue` 这类相对 import 解析结果完全一致。
 * ⇒ 真实 router-mobile.ts 一次都不被碰，md5 无需比对（结构上就不会污染）。
 */
async function withMutatedRouter(mutator, fn) {
  const probe = join(FRONT, 'src', 'app', 'router-parity-probe.ts')
  if (existsSync(probe)) {
    throw new Error(`临时探针文件已存在（上次运行异常中断？）：${probe} —— 拒绝覆盖`)
  }
  const original = readFileSync(ROUTER, 'utf8')
  const mutated = mutator(original)
  if (mutated === original) throw new Error('变异器没有改动任何内容（变异手法本身失效，不是判据没反应）')
  try {
    writeFileSync(probe, mutated)
    return await fn(probe)
  } finally {
    if (existsSync(probe)) unlinkSync(probe)
  }
}

/** 找一条**声明了** hideAppHeader 的路由，在它的 path 与 meta 之间塞进 N 字符注释。 */
function pushMetaOutOfWindow(padTo) {
  return (src) => {
    const m = /(\{\s*path: '\/flashcards'\s*,\s*\n)/.exec(src)
    if (!m) throw new Error('找不到 /flashcards 路由，变异锚点失效')
    const pad = '// ' + 'x'.repeat(Math.max(0, padTo - 3)) + '\n'
    return src.slice(0, m.index + m[0].length) + pad + src.slice(m.index + m[0].length)
  }
}

/**
 * 阴性对照专用：在**第一条未声明** hideAppHeader 的路由的 path 与 meta 之间塞长注释。
 *
 * ⚠️ 这里**动态选取**而不是写死路径 —— 写死过一次，挑中的 `/settings/llm-gateway`
 *    恰恰也声明了 `hideAppHeader: true`（router-mobile.ts:463），于是变异落在
 *    「会掉声明」的路由上，把阴性对照做成了第二个阳性 ⇒ 判据自证失去意义。
 *    「未声明」是**会变的属性**，不是某个路径的常量。
 */
function pushMetaOutOfWindowOnUndeclared(padTo) {
  return (src) => {
    const segs = [...src.matchAll(/path:\s*'([^']+)'/g)]
    for (let i = 0; i < segs.length; i++) {
      const end = i + 1 < segs.length ? segs[i + 1].index : src.length
      if (/hideAppHeader:\s*true/.test(src.slice(segs[i].index, end))) continue
      // 确认它确实是「整段都没有」而不是 meta 落在下一段里
      if (!/meta\s*:/.test(src.slice(segs[i].index, end))) continue
      const after = src.indexOf('\n', segs[i].index)
      const pad = '// ' + 'y'.repeat(Math.max(0, padTo - 3)) + '\n'
      return src.slice(0, after + 1) + pad + src.slice(after + 1)
    }
    throw new Error('找不到任何「未声明 hideAppHeader 且带 meta」的路由，阴性对照无从下手')
  }
}

async function selftest() {
  const results = []
  const add = (name, pass, why) => { results.push({ name, pass, why }); console.log(`  ${pass ? '通过' : '失败'}  ${name}`); if (why) console.log(`         ${why}`) }

  // ---- ① 基线：不动任何东西 ⇒ 必须绿 ----
  {
    const src = readFileSync(ROUTER, 'utf8')
    const d = diff(routesOf(src), await runtimeRoutes(ROUTER))
    const clean = !d.missingFromText.length && !d.onlyInText.length && !d.routesLostByText.length && !d.routesInventedByText.length
    add('① 基线·两种口径一致', clean, clean ? `声明数两法同为 ${routesOf(src).filter((r) => r.hideAppHeader).length} 条` : `分叉：${JSON.stringify(d)}`)
  }

  // ---- ② 真变异：在**声明了**的路由的 path 与 meta 之间塞 >500 字符注释 ----
  // 这正是当前门禁那个「未爆的洞」：meta 被挤出 500 窗口 ⇒ 文本漏掉 1 条，
  // 运行期仍是 15 ⇒ **分叉 ⇒ 必须报红**。
  {
    let d, before, after
    try {
      before = routesOf(readFileSync(ROUTER, 'utf8')).filter((r) => r.hideAppHeader).length
      d = await withMutatedRouter(pushMetaOutOfWindow(700), async (probe) => {
        const after = routesOf(readFileSync(probe, 'utf8')).filter((r) => r.hideAppHeader).length
        return { d: diff(routesOf(readFileSync(probe, 'utf8')), await runtimeRoutes(probe)), after }
      })
      after = d.after
      add('② 真变异·把已声明路由的 meta 挤出 500 窗口 ⇒ 必须报红',
        d.d.missingFromText.length === 1 && d.d.missingFromText[0] === '/flashcards',
        `文本口径 ${before} → ${after}（漏了 1 条），运行期仍 ${15}；漏掉的是 ${JSON.stringify(d.d.missingFromText)}`)
    } catch (e) {
      add('② 真变异·把已声明路由的 meta 挤出 500 窗口 ⇒ 必须报红', false, `★ 变异执行失败：${e.message}`)
    }
  }

  // ---- ③ 阴性对照：同样的长注释塞在**未声明**的路由上 ⇒ 两种口径都看不见 ⇒ 仍绿 ----
  // 没有这一格就分不清 ② 是「真的发现声明被漏掉」还是「只要有长注释就红」。
  {
    try {
      const r = await withMutatedRouter(pushMetaOutOfWindowOnUndeclared(700), async (probe) => {
        const t = routesOf(readFileSync(probe, 'utf8'))
        return diff(t, await runtimeRoutes(probe))
      })
      const clean = !r.missingFromText.length && !r.onlyInText.length && !r.routesLostByText.length && !r.routesInventedByText.length
      add('③ 阴性对照·长注释落在**未声明**路由上 ⇒ 不得误报',
        clean, clean ? '两法口径均未受影响、仍一致 ⇒ ② 的红确实来自「声明被漏掉」而非「有注释就红」' : `★ 误报：${JSON.stringify(r)}`)
    } catch (e) {
      add('③ 阴性对照·长注释落在**未声明**路由上 ⇒ 不得误报', false, `★ 变异执行失败：${e.message}`)
    }
  }

  const bad = results.filter((r) => !r.pass)
  console.log(`\n自检: ${results.length - bad.length}/${results.length} 通过`)
  console.log(`（真实 router-mobile.ts 未被触碰：本门禁在**同目录临时副本**上做变异，跑完即删）`)
  process.exit(bad.length === 0 ? 0 : 1)
}

async function main() {
  if (process.argv.includes('--selftest')) {
    console.log('[check-router-runtime-parity] 自检：验证本检查仍能发现「文本口径与运行期口径分叉」')
    await selftest()
    return
  }
  if (!existsSync(ROUTER)) {
    console.error(`FAIL 找不到 ${ROUTER} —— 判据失明，拒绝给结论`)
    process.exit(2)
  }
  const src = readFileSync(ROUTER, 'utf8')
  const textRoutes = routesOf(src)
  if (textRoutes.length === 0) {
    console.error('FAIL 一条路由都没解析出来 —— 判据失明，拒绝给结论')
    process.exit(2)
  }
  const rt = await runtimeRoutes(ROUTER)
  const d = diff(textRoutes, rt)
  const textHidden = textRoutes.filter((r) => r.hideAppHeader).length
  console.log(`路由总数：文本口径 ${textRoutes.length} / 运行期 ${rt.total}`)
  console.log(`声明 hideAppHeader: true：文本口径 ${textHidden} / 运行期 ${rt.hidden.length}`)
  if (!d.missingFromText.length && !d.onlyInText.length && !d.routesLostByText.length && !d.routesInventedByText.length) {
    console.log('\n✓ 两种口径逐条一致 —— 文本解析没有失明')
    return
  }
  console.error('\n✗ 文本口径与运行期分叉 —— 门禁存在失明区：')
  if (d.missingFromText.length) console.error(`  运行期声明了、文本口径漏掉：\n${d.missingFromText.map((p) => `  - ${p}`).join('\n')}\n    ⇒ 多半是 path 与 meta 之间的内容超出了解析窗口。把该路由的 meta 挪近 path，或修解析口径。`)
  if (d.onlyInText.length) console.error(`  文本口径数出、运行期没有：\n${d.onlyInText.map((p) => `  - ${p}`).join('\n')}\n    ⇒ 多半是注释里写了字面量被当成声明。`)
  if (d.routesLostByText.length) console.error(`  运行期有、文本口径整条路由看不到：\n${d.routesLostByText.map((p) => `  - ${p}`).join('\n')}`)
  if (d.routesInventedByText.length) console.error(`  文本口径有、运行期没有：\n${d.routesInventedByText.map((p) => `  - ${p}`).join('\n')}`)
  console.error('\n这条门禁不判断「该不该声明 hideAppHeader」，只保证「你数出来的那几条 = vue-router 实际持有的那几条」。')
  process.exit(1)
}

try {
  await main()
} catch (e) {
  console.error(`FAIL 判据无法执行（依赖不可用或编译失败）—— 拒绝给结论：${e.message}`)
  process.exit(2)
}
