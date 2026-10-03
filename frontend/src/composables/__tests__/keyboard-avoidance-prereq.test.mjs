/**
 * 软键盘避让的**前提条件**守卫（2026-10-03 模拟器 API 35 实测后补）。
 *
 * ## 这组测试守的是什么
 *
 * 2026-10-03 在模拟器（API 35）上点密码框复现了用户诉求里的原话
 * 「键盘不能遮盖正在输入的框」：键盘弹起后密码框被键盘上沿切掉一半。
 * CDP 读到的运行时状态是决定性的：
 *
 *     innerHeight            = 915
 *     visualViewport.height  = 915     ← 与收起时一模一样
 *     --kb-inset             = ""      ← 从未下发
 *     html.kb-open           = false
 *
 * 也就是**键盘弹起时视口完全没有变化**。原实现只用
 * `baseline - min(innerHeight, vv.height)` 建模键盘高度，在这条路径上恒为 0，
 * 于是 `kb-open` 不置位、`--kb-inset` 不下发、聚焦字段不被顶上来。
 *
 * 根因在原生：MainActivity 是 edge-to-edge（setDecorFitsSystemWindows(false)）
 * 且 manifest 未声明 adjustResize，它的 setOnApplyWindowInsetsListener 只读了
 * `Type.systemBars()` 与 `Type.mandatorySystemGestures()`，**没读 `Type.ime()`**。
 * 真机 Android 15 是同一条路径，所以这不是模拟器特例。
 *
 * 下面 4 条测试把这条链上的每个前提分别钉住。
 *
 * ## 一条被本文件否掉的判据（留档，别再写回去）
 *
 * 第一版判据是「含输入框且没自带 overflow-y:auto 的视图都算违规」，报了 16 个
 * 视图（FinanceView / FlashcardEditView / …）。**那是范围过宽，判据错了**：
 * 所有 router view 都挂在 AppLayout 的 `<main class="content">` 里，而它
 * `overflow-y: auto`（AppLayout.vue 的 .content 规则）——祖先链上本来就有滚动
 * 容器，轮不到视图自己滚。壳内视图不需要自带 overflow。
 *
 * 真正的分界是「有没有可滚动祖先」，而对壳内视图那个答案是全局成立的，
 * 所以第 3 条直接断言 AppLayout 那一处即可，而不是逐视图猜。
 */

import { readFile } from 'node:fs/promises'
import { readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import assert from 'node:assert/strict'

const __dirname = fileURLToPath(new URL('.', import.meta.url))
const SRC = join(__dirname, '..', '..')
const ANDROID_SRC = join(SRC, '..', 'android', 'app', 'src', 'main', 'java', 'com',
  'kaixuan', 'opencode', 'pocket')

const MAIN_ACTIVITY = join(ANDROID_SRC, 'MainActivity.java')
const KEYBOARD_INSET = join(SRC, 'composables', 'useKeyboardInset.ts')
const APP_LAYOUT = join(SRC, 'app', 'AppLayout.vue')

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name.startsWith('.')) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (name.endsWith('.vue')) out.push(p)
  }
  return out
}

/** 抽取 <style> 段（scoped 样式也在这段里）。 */
function styleBlock(source) {
  const m = /<style[^>]*>([\s\S]*?)<\/style>/.exec(source)
  return m ? m[1] : ''
}

/**
 * 剥掉 CSS 注释再交给判据。
 *
 * 不是洁癖：修复本身在 .login-view 里留了一段解释「为什么不用
 * align-items:center」的注释，而判据要检查的正是 `align-items:center`
 * 这个字符串——不剥注释的话，判据会去匹配自己人写的说明文字。
 * 判据必须只看**声明**，不看**关于声明的散文**。
 */
function declarationsOnly(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

const files = walk(SRC)

// ---------------------------------------------------------------------------
// 1. 原生必须真的取 ime insets —— 整条链的源头
// ---------------------------------------------------------------------------

test('原生必须取 WindowInsetsCompat.Type.ime() 并注入 --android-ime-inset', async () => {
  const java = await readFile(MAIN_ACTIVITY, 'utf8')

  assert.match(
    java,
    /WindowInsetsCompat\.Type\.ime\(\)/,
    'MainActivity 的 insets 监听没有读 Type.ime()。' +
      'edge-to-edge + 无 adjustResize 时 Android 15 的 IME 既不缩视口也不动 ' +
      'visualViewport（2026-10-03 模拟器 API 35 实测 innerHeight===vv.height 恒为 915），' +
      '不读它就没有任何键盘信号，聚焦输入框必被盖住。',
  )
  assert.match(
    java,
    /--android-ime-inset/,
    '读到了 ime insets 却没有注入 --android-ime-inset，JS 侧 useKeyboardInset 读不到。',
  )
})

// ---------------------------------------------------------------------------
// 2. JS 侧必须消费这个信号
// ---------------------------------------------------------------------------

test('useKeyboardInset 消费原生 --android-ime-inset，而不是只靠视口高度差', async () => {
  const ts = await readFile(KEYBOARD_INSET, 'utf8')

  assert.match(
    ts,
    /--android-ime-inset/,
    'useKeyboardInset 必须读 --android-ime-inset。' +
      '只靠 baseline - min(innerHeight, vv.height) 在「键盘不缩视口」的 overlay 路径上恒为 0。',
  )
  assert.match(
    ts,
    /MutationObserver/,
    '原生用 setProperty 写 CSS 变量，变量变化不派发任何事件；' +
      '没有 MutationObserver 监听 style 属性变更，就收不到键盘显隐。',
  )
})

// ---------------------------------------------------------------------------
// 3. 壳内视图的滚动容器由 AppLayout 提供（全局前提，断言这一处即可）
// ---------------------------------------------------------------------------

test('AppLayout 的 <main class="content"> 是滚动容器（壳内视图的键盘避让前提）', async () => {
  const source = await readFile(APP_LAYOUT, 'utf8')
  const css = declarationsOnly(styleBlock(source))

  // 取出 .content 那条规则块看 overflow-y
  const rule = new RegExp(String.raw`\.content\s*(?:,[^{]*)?\{([^}]*)\}`).exec(css)
  assert.ok(rule, '找不到 .content 规则块')

  assert.match(
    rule[1],
    /overflow-y:\s*auto/,
    '.content 必须是 overflow-y:auto —— 所有 router view 都挂在它里面，' +
      '它是 revealFocusedInput 唯一可依赖的滚动容器。' +
      '（.content.scroll-self / .scroll-split / .fullscreen 另有 hidden 覆盖，' +
      '那些自管滚动的视图由各自声明 scroll-self 承担。）',
  )
})

// ---------------------------------------------------------------------------
// 4. 全屏居中页不要用 align-items:center 承担垂直居中
// ---------------------------------------------------------------------------

test('全屏撑满布局不用 align-items:center 承担垂直居中（会两端溢出且顶部滚不回去）', async () => {
  const offenders = []

  for (const file of files) {
    const source = await readFile(file, 'utf8')
    const css = declarationsOnly(styleBlock(source))
    if (!/min-height:\s*100%/.test(css)) continue
    if (!/<(input|textarea)\b/.test(source)) continue

    // 取承载 min-height:100% 的那个规则块，检查它内部有没有 align-items:center
    const rule = new RegExp(String.raw`[^{}]*\{[^}]*min-height:\s*100%[^}]*\}`).exec(css)
    if (!rule) continue
    if (/align-items:\s*center/.test(rule[0])) {
      offenders.push(
        `${relative(SRC, file)} —— 撑满布局用 align-items:center 垂直居中；` +
          `内容高于容器时它两端同时溢出、顶部溢出部分滚不回去，` +
          `键盘态下用户连标题都够不着。改用容器 margin:auto。`,
      )
    }
  }

  assert.deepEqual(offenders, [], offenders.join('\n'))
})
