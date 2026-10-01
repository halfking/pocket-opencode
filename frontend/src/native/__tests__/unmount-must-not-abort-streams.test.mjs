/**
 * 切页不得杀掉在途请求/流（2026-10-02 审计）。
 *
 * 用户要求：「检查所有的请求是否在后台执行，确认可以在切换页面后仍能执行」。
 * 这一条此前**只由注释保证**，没有任何测试兜着。
 *
 * ## 现状：runtime 侧测得很扎实，组件侧是空的
 *
 * `aiStreamRuntime.test.mjs` 已经测了真正重要的运行时性质：
 *   - L103「流所有权上移：调用方释放引用后流仍跑（模拟 unmount 不 abort）」
 *   - abort 区分 reason='user' 与 'watchdog'
 *   - hidden / frozen 期间不 abort
 *
 * 但那全部是 **runtime 自己的行为**。runtime 再怎么保证"流归我管"，
 * 也拦不住组件在 `onUnmounted` 里直接 `handle.abort()` —— 那是另一个对象、
 * 另一行代码。`UnifiedComposer.vue` 的 M1 契约就写在注释里：
 *   「组件 unmount 不 abort 优化流——流所有权归 aiStreamRuntime，
 *     自然跑完；取消只属于用户显式操作」
 * 明天有人往 `onBeforeUnmount` 里补一句 `optimizer.abort()`，
 * runtime 的全部测试照样全绿，而用户看到的是「一切页就断流」。
 *
 * 所以这里守的是**组件侧的那一半**：视图文件里不许出现
 * 「卸载钩子 + 真的 abort」这种组合。
 *
 * ## 判据为什么是「文件级」而不是「钩子体内」
 *
 * 提取 `onUnmounted(() => { ... })` 的平衡括号需要真解析；用字符窗口去框
 * 则对注释长度极度敏感（`[\s\S]{0,400}` 曾被一段中文注释撑爆而误报，
 * 见 note-recording-error-visibility.test.mjs）。文件级判据粗，但**粗是安全的**：
 * 误报进白名单、写明理由即可，不会漏报真问题。
 *
 * 第一版判据含 `cancel\w*\(`，实测 4 处命中，逐个核对后 **3 处是误报**：
 *   ProgressRing.vue / WaveformVisualizer.vue → 命中的是 `cancelAnimationFrame`
 *     （动画帧，不是请求）
 *   EmailInboxView.vue → 命中的是模板里的 `@click="inbox.cancelClassify()"`,
 *     它的 unmount 只做 `setHeaderTitle(null)`，根本没有 abort
 * 收紧为 `\.abort\(\)` 后命中降到 1 处，且 `.ts` 侧 0 处 —— 稳定。
 *
 * Run: node --test src/native/__tests__/unmount-must-not-abort-streams.test.mjs
 */
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, extname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

// 本文件在 src/native/__tests__/ 下，往上三级才是 frontend/ 根
// （第一版写成两级，扫到了 frontend/src/src，ENOENT 直接崩——
//  护栏自己先崩，总比空跑通过好，但显然该修）。
const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const SKIP_DIRS = new Set(['node_modules', '.git', 'dist', 'android', 'ios'])

/**
 * 逐文件豁免。每条都要写可核查的理由，不接受「应该没事」。
 * 新增条目时必须回答：它 abort 的是**在途请求**还是**动画帧/定时器**？
 */
const ALLOW = new Map([
  [
    'src/features/cost/CostQuotaView.vue',
    'abort 的是 onMounted 里发起的**本页读取**（配额列表加载），不是后台作业。' +
    '页面被卸载时中止它是对的：否则响应回来会写进已卸载组件的 ref。' +
    '与「切页后仍能执行」要保护的对象不同——那类对象是流与长任务，' +
    '归属 aiStreamRuntime / 用户显式发起的批处理。',
  ],
])

function collectVue(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collectVue(full, out)
    else if (extname(name) === '.vue') out.push(full)
  }
  return out
}

const files = collectVue(SRC).map((f) => ({
  rel: relative(ROOT, f).replace(/\\/g, '/'),
  text: readFileSync(f, 'utf8'),
}))

// 注意 `(?:ed)?`：Vue 的 API 有 onUnmounted **和** onBeforeUnmount。
// 第一版漏了 `(?:ed)?`，于是只匹配 onBeforeUnmount(，
// 把所有用 onUnmounted( 的文件（Vue 里更常见的那种）整体漏掉 ——
// 判据近乎恒假，护栏"永远绿"。负控注入 `onUnmounted` 里的 abort 没转红，
// 就是这个原因。这个坑和正则里"空分支匹配一切"是同一类：绿灯不代表有效。
const hasUnmountHook = (t) => /on(?:Before)?Unmount(?:ed)?\s*\(/.test(t)
// `\babort\s*\(` 而不是 `\.abort\(\)`：后者要求字面 `.abort()`，
// 于是 `__h?.abort?.()`、`const a = x.abort; a()` 这类真实写法会漏网。
// 用词边界既不会误伤 cancelAnimationFrame（它不含 abort 这个词），
// 又能覆盖解构/别名引用。实测收紧前的 `.abort()` 版本在本仓库命中 1 处，
// 换成词边界版仍是 1 处（CostQuotaView），无新增误报。
const hasRealAbort = (t) => /\babort\s*\(/.test(t)

const withHook = files.filter((f) => hasUnmountHook(f.text))
const violations = withHook
  .filter((f) => hasRealAbort(f.text) && !ALLOW.has(f.rel))
  .map((f) => f.rel)

describe('切页不得 abort 在途请求/流', () => {
  it('判据本身认得两个 Vue API 名（防空跑通过）', () => {
    // 直接锁住第一版的 bug：正则漏了 (?:ed)?，onUnmounted( 整个匹配不到。
    // 这条断言比"数量 > N"更准——它问的是判据的语义，不是碰巧扫到了多少文件。
    for (const api of ['onUnmounted(', 'onBeforeUnmount(']) {
      assert.ok(
        hasUnmountHook(`x; ${api} () => {}`),
        `判据认不出 ${api}，该形式下护栏会永远绿`,
      )
    }
    // 反向：别把不存在的 API 也当命中
    assert.equal(hasUnmountHook('onUnmountedX(() => {})'), false)
  })

  it('扫描范围本身有效（防空跑通过）', () => {
    assert.ok(files.length > 100, `只扫描到 ${files.length} 个 .vue，扫描范围可能失效`)
    // 实测 2026-10-02：178 个 .vue 里有 30 个带卸载钩子。
    // 第一版判据（漏了 onUnmounted）只数到更少，靠 "> 5" 这条松断言蒙混过关。
    assert.ok(
      withHook.length >= 20,
      `只找到 ${withHook.length} 个带卸载钩子的 .vue（实测应为 30 左右）；判据多半又匹配不到真实代码了`,
    )
  })

  it('白名单里没有失效条目（文件被改名/删除时要立刻报出来）', () => {
    const known = new Set(files.map((f) => f.rel))
    const stale = [...ALLOW.keys()].filter((k) => !known.has(k))
    assert.equal(
      stale.length,
      0,
      `白名单指向了不存在的文件，豁免已经失去意义（文件改名或该豁免可以撤了）：\n  - ${stale.join('\n  - ')}`,
    )
  })

  it('没有视图在卸载钩子里 abort 在途请求', () => {
    assert.equal(
      violations.length,
      0,
      `发现 ${violations.length} 个视图同时存在「卸载钩子」与「.abort()」，` +
      '切页会杀掉在途请求/流：\n  - ' + violations.join('\n  - ') +
      '\n\n修法：流与长任务的所有权移到进程级 runtime（见 aiStreamRuntime），' +
      '卸载钩子只负责解除监听/清定时器；用户要中止就提供显式的取消入口' +
      '（参照 use-email-inbox 的 cancelClassify）。确实该豁免就写进上面的 ALLOW 并说明理由。',
    )
  })

  it('取消只能由用户显式触发，不是由卸载触发', () => {
    // 回归护栏：邮件归类提供「取消」按钮且真的 abort 在途 HTTP。
    // 这条锁住的是**能力存在**，避免将来重构时把唯一的中止入口删掉。
    const view = files.find((f) => f.rel === 'src/features/email/EmailInboxView.vue')
    assert.ok(view, '找不到 EmailInboxView.vue')
    assert.match(
      view.text,
      /@click="inbox\.cancelClassify\(\)"/,
      '邮件归类的「取消」按钮必须仍然接到 cancelClassify',
    )
    const inbox = readFileSync(join(SRC, 'features', 'email', 'use-email-inbox.ts'), 'utf8')
    assert.match(
      inbox,
      /function cancelClassify\(\)\s*\{[\s\S]{0,200}?\.abort\(\)/,
      'cancelClassify 必须真的 abort 在途请求，只置取消标记会让当前这批跑满',
    )
  })
})
