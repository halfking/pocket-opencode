// fire-and-forget-audit.test.ts — §850：`void x(...)` 这一族的**登记 + 双向对账**。
//
// 这一族的形状：`void someAsync()` 显式声明「我不处理返回值」⇒
// 一旦 someAsync 会 reject，就是一个**未处理 rejection**（§847-D3 / §848-D4 同族）。
//
// ⚠ 为什么是登记表而不是自动规则：
//   扫面把候选从 156 收窄到 15（见 §850.1），但这 15 条**全都安全**，
//   而安全的原因**不在 callee 自己身上** ——
//     fetchMonth      → 体内没有 catch，但它 await 的 store.load 自己吞错并设 error
//     submitSystemBack→ 体内没有 catch，但它 await 的 dispatchBack 自带 try/catch
//     flush           → 体内没有 catch，但它 await 的 persistTo 自带 catch
//     refreshRelated  → 体内没有 catch，但它 await 的 searchRelatedContext 的三个
//                       子查询各自 catch 后返回 []
//   ⇒「callee 体内必须有 catch」这条机械规则会把这 15 条**全部判红**（假阳）。
//   ⇒ 唯一站得住的形式是：**逐条登记 + 与扫面结果双向对账**，
//     新增 `void x()` 而没登记 ⇒ 这里红。
//
// ⚠ 登记表**不用行号**做键：§844 的 `z-index-ladder` ALLOWLIST 按行号登记，
//   被一次编辑推了 42 行就红（那门注释自己记着这是第五次咬人）。
//   ⇒ 键 = `相对路径 + callee 名 + 该文件内出现次数`，对插入免疫。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, relative } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')
const relOf = (p: string) => relative(SRC, p).split('\\').join('/')

function collect(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(join(SRC, dir))) {
    if (name === '__tests__' || name === 'node_modules') continue
    const full = join(SRC, dir, name)
    let st
    try { st = statSync(full) } catch (err: any) {
      if (err?.code !== 'ENOENT') throw err
      continue
    }
    if (st.isDirectory()) collect(relative(SRC, full), out)
    else if (/\.(ts|vue)$/.test(name)) out.push(full)
  }
  return out
}

const FILES = collect('.')
const strip = (t: string): string =>
  t.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
   .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))

/** 一行里有没有任何 catch/then/finally 链；有就说明作者处理过了。 */
const hasChain = (line: string): boolean => /\.(?:catch|then|finally)\s*\(/.test(line)

/**
 * 取本文件里 `name` 的函数体；找不到返回 null。
 *
 * ⚠⚠ 必须**先按 `(` 配平跳过形参列表**再找函数体的 `{`：
 *   遇到形参带**含花括号的类型注解**的函数（如
 *   `async function f(x: { a: number })`），直接 `indexOf('{')` 会落在
 *   **类型字面量**上，配平立刻归零 ⇒ 取到一个没有 catch 的空壳
 *   ⇒ 把「自己接住了错误的函数」误判成「没接住」。
 *   我在探针里踩过一次（`postSystemNotification`），判据里不再犯。
 */
function bodyOf(code: string, name: string): string | null {
  let m = new RegExp(`(?:^|\\n)\\s*(?:export\\s+)?(?:async\\s+)?function\\s+${name}\\s*\\(`).exec(code)
  if (!m) {
    m = new RegExp(`(?:^|\\n)\\s*(?:export\\s+)?(?:const|let)\\s+${name}\\s*(?::[^=]{0,80})?=\\s*async\\s*\\(`).exec(code)
  }
  if (!m) return null
  let i = code.indexOf('(', m.index)
  let pd = 0
  for (; i < code.length && i < m.index + 600; i += 1) {
    if (code[i] === '(') pd += 1
    else if (code[i] === ')') { pd -= 1; if (pd === 0) { i += 1; break } }
  }
  // 可能还有 TS 返回类型注解 / `=>`，一律跳到下一个 `{`
  while (i < code.length && !/\s/.test(code[i]) && code[i] !== '{') {
    if (code[i] === ':' || code[i] === '=') {
      const b = code.indexOf('{', i)
      if (b < 0 || b - i > 120) return null
      i = b
      break
    }
    i += 1
  }
  while (i < code.length && /\s/.test(code[i])) i += 1
  if (code[i] !== '{') return null
  let depth = 0
  for (let k = i; k < code.length && k < i + 4000; k += 1) {
    if (code[k] === '{') depth += 1
    else if (code[k] === '}') { depth -= 1; if (depth === 0) return code.slice(i, k + 1) }
  }
  return ''
}

/**
 * 扫出需要人判断的位置：`void x(...)` 且整条语句无 catch/then/finally，
 * **且 callee 是本文件定义的裸名函数**（方法调用与跨文件导入另有判据），
 * **且它的函数体内没有任何 catch**（有 catch ⇒ 它自己接住了）。
 */
function scan(): Map<string, number> {
  const found = new Map<string, number>()
  for (const f of FILES) {
    const code = strip(readFileSync(f, 'utf8'))
    for (const line of code.split('\n')) {
      if (hasChain(line)) continue
      const m = /(?:^|[^.\w])void\s+([\w]+)\s*\(/.exec(line)
      if (!m) continue
      const body = bodyOf(code, m[1])
      if (body === null) continue
      if (/\bcatch\b/.test(body)) continue
      const key = `${relOf(f)}::${m[1]}`
      found.set(key, (found.get(key) ?? 0) + 1)
    }
  }
  return found
}

/** 本节人工逐条复核过的 15 条（2026-10-18 基线）。 */
const REGISTRY: Array<{ key: string; count: number; why: string }> = [
  { key: 'app/AppLayout.vue::submitSystemBack', count: 1,
    why: '它 await 的 dispatchBack() 自带 try/catch 并返回 { kind: "blocked" }（lib/shell/runtime.ts:308）' },
  { key: 'components/UpdateChecker.vue::maybePrefetch', count: 1,
    why: '预取是后台尽力而为；真安装路径 openLocalApk 另有完整回退（失败退回远端下载）' },
  { key: 'composables/usePendingApprovals.ts::refresh', count: 1,
    why: '它只 await runtime.refresh()，而该路径的错误由 ApprovalPanel / SessionConversationView 的横幅承接' },
  { key: 'features/calendar/CalendarView.vue::fetchMonth', count: 3,
    why: '它 await 的 store.load() 自己 catch 并设 error；视图 :52 渲染该 error 且空态带 !store.error 守卫（§840 正确形态）' },
  { key: 'features/email/EmailInboxView.vue::recheckSentinel', count: 1,
    why: '哨兵几何重算，同步失败无外部可观测面' },
  { key: 'features/email/email-fetch-host.ts::bindNative', count: 1,
    why: '原生事件绑定，失败在邮件页表现为读失败横幅（已在 §841/§842 覆盖）' },
  { key: 'features/email/use-invoice-list.ts::loadThumbs', count: 3,
    why: '缩略图是渐进增强；单张失败由 use-invoice-thumbs 内部兜住' },
  { key: 'features/meetings/MeetingDetailView.vue::refreshRelated', count: 1,
    why: '它 await 的 searchRelatedContext 的三个子查询（notes/meetings/web）各自 catch 后返回 []' },
  { key: 'features/sessions/useSessionDrafts.ts::ensureStore', count: 1,
    why: '它的调用方各自 try/catch（hydrate / clear / persistTo 三处都有）' },
  { key: 'features/sessions/useSessionDrafts.ts::flush', count: 1,
    why: '它 await 的 persistTo() 自带 catch（「写失败不阻断输入」）—— ⚠ 该静默本身已登记为待拍板' },
  // ⚠ 2026-10-18（§857/D20）：`SettingsPermissionsView::refreshAll` 曾在此登记
  //   （count 1，理由「各子项自带 error 出口」）。
  //   §857 给它补了 `catch` ⇒ `scan()` 的第 4 步「函数体内有 catch 就跳过」不再命中它
  //   ⇒ 它**已经不属于「无 catch 的 void x()」这一族** ⇒ 从本表移除。
  //   ⭐ 这条正是「登记表反向对账」存在的意义：它把「我改了某个函数」这件事
  //     变成了一条会红的断言，而不是靠人记得回头改表。
  //   ⚠ 顺带更正原理由：它当初的阴性依据是「各子项自带 error 出口」，
  //     而 §857 实测 `appSettings.checkPermission` **并不吞错** ⇒ 那条依据本身
  //     就是错的（这也是它为什么会在 §857 变成真缺陷）。
]

describe('§850 · `void x(...)` fire-and-forget：登记表与扫面双向对账', () => {
  it('★ 尺子非空转：全仓 .ts/.vue 至少 200 个', () => {
    assert.ok(FILES.length > 200, `只扫到 ${FILES.length} 个文件，路径写错了 ⇒ 本文件会空转`)
  })

  it('★★★ 登记表不许漂移：扫出来的必须都在登记表里', () => {
    const found = scan()
    const registered = new Map(REGISTRY.map((r) => [r.key, r.count]))
    const missing = [...found.keys()].filter((k) => !registered.has(k))
    assert.deepEqual(missing, [],
      `这些 \`void x()\` 没有登记：\n  ${missing.join('\n  ')}\n` +
      '⇒ 新增时必须逐条确认「它到底会不会 reject、失败有没有出口」，' +
      '然后写进 REGISTRY 并写清理由')
  })

  it('★★★ 登记的条目必须仍然存在（改名/删除会逼你重新判断）', () => {
    const found = scan()
    const stale = REGISTRY.filter((r) => !found.has(r.key)).map((r) => r.key)
    assert.deepEqual(stale, [],
      `登记表里的这些位置已经找不到了：\n  ${stale.join('\n  ')}\n` +
      '⇒ 可能是改名、删除、或行形态变了；确认后更新登记表')
  })

  it('★★ 出现次数必须一致（同一文件里同一 callee 的第 N 次调用）', () => {
    const found = scan()
    const bad: string[] = []
    for (const r of REGISTRY) {
      const n = found.get(r.key)
      if (n !== undefined && n !== r.count) {
        bad.push(`${r.key}：登记 ${r.count} 处，实际 ${n} 处`)
      }
    }
    assert.deepEqual(bad, [], bad.join('\n'))
  })

  it('★★ 每条登记都必须写清「为什么它安全」', () => {
    for (const r of REGISTRY) {
      assert.ok(r.why.length > 15,
        `${r.key} 的登记理由太短 ⇒ 与「随手登记」分不开`)
    }
  })

  it('★ 合计口径固定：14 处（基线 = 2026-10-18 §857 修正后）', () => {
    // ⚠ 2026-10-18（§857）：基线由 **15** 改为 **14** ——
    //   `SettingsPermissionsView::refreshAll` 补了 catch 后离开了本族（见 REGISTRY 处的说明）。
    //   ⭐ 按「棘轮基线必须按实测更新」处理，且**必须写清是哪一条、为什么变** ——
    //     否则这道断言会在某天被人当成「有人偷偷删了登记」而反向放宽。
    const total = REGISTRY.reduce((n, r) => n + r.count, 0)
    assert.equal(total, 14, `登记表合计 ${total} 处，与本节记录的基线 14 不符`)
  })
})

describe('§850 · 变异负控（尺子必须有牙）', () => {
  it('★ 新增一条未登记的 `void x()` ⇒ 登记表判据转红', () => {
    // 变异是**对判据自己的登记表**做的：模拟「有人加了一处 void 调用但没登记」。
    const before = scan()
    const key = 'app/AppLayout.vue::submitSystemBack'
    const found = new Map(before)
    found.set('features/zzz/ProbeView.vue::brandNewFireAndForget', 1)
    const registered = new Map(REGISTRY.map((r) => [r.key, r.count]))
    const missing = [...found.keys()].filter((k) => !registered.has(k))
    assert.deepEqual(missing, ['features/zzz/ProbeView.vue::brandNewFireAndForget'])
  })

  it('★ 改掉一处的 catch 链 ⇒ hasChain 判据转红', () => {
    assert.equal(hasChain('void x().catch(() => {})'), true)
    assert.equal(hasChain('void x().then(a).catch(b)'), true)
    assert.equal(hasChain('void x().finally(() => {})'), true)
    assert.equal(hasChain('void x()'), false)
  })

  it('★ 注释里的 void 调用不算（否则说明文字会被当成代码）', () => {
    const stripped = strip('// 以前是 void retry()\nconst a = 1\n')
    assert.equal(/void\s+\w+\s*\(/.test(stripped), false,
      '注释没被剥掉 ⇒ 以后没法在注释里引用这种写法')
    const tplStripped = strip('<!-- 以前是 void retry() -->\n<b>x</b>\n')
    assert.equal(/void\s+\w+\s*\(/.test(tplStripped), false,
      'HTML 注释没被剥掉 ⇒ 同上')
  })
})
