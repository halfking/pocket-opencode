/**
 * Toast 的底边避让（2026-10-03 模拟器 API 35 实测后补）。
 *
 * ## 复现的那一条
 *
 * `/ai-chat` 上弹出错误 toast「模型列表获取失败：请先在「设置 → AI 网关」配置
 * 网关密钥」，它**压住了整条输入区工具行**——全屏、麦克风、相机、附件、
 * 角色 chip、✨优化全被盖住。CDP 逐元素量出来的重叠：
 *
 *     .material-symbols-outlined (mic)      vs .toast-message   重叠 22×22
 *     .material-symbols-outlined (image)    vs .toast-message   重叠 22×22
 *     .uc-chip-label (角色)                  vs .toast-message   重叠 26×19
 *     .material-symbols-outlined (auto_awesome) vs .toast-message 重叠 18×…
 *
 * 麦克风是这个 App 的主打能力之一（需求④ 录音），被一条几秒的提示盖住不该
 * 算可接受。
 *
 * ## 根因
 *
 * `.toast { bottom: calc(var(--bottom-chrome-height) + var(--space-4)) }`
 * ——`--bottom-chrome-height` 只是**底部 tabbar**的高度。而输入区是**停靠在
 * tabbar 之上**的，比 tabbar 高得多。少算了输入区那条带，键盘在场时还会被
 * 键盘直接吃掉。
 *
 * ## 修的过程中被打回过一次（这才是本文件真正要守的东西）
 *
 * 第一版让 UnifiedComposer 发布**自身高度**（`.uc` 的 height = 153px），toast
 * 用 `bottom-chrome + composer-inset + kb-inset + space-4` 累加 = 255px。
 * 设备上重新量：`.composer` 包裹层顶边在视口底边往上 **289.7px**，
 * toast 底边只有 255px —— **仍然压住工具行 34.7px**。
 *
 * 因为 `.uc` 自身**不含**调用方包裹层的 padding/border：
 *
 *     .composer { padding: 8px 12px; padding-bottom: calc(8px + var(--app-safe-bottom));
 *                 border-top: 1px solid; }   →  整条带 202px，其中 .uc 只占 153px
 *
 * 所以发布端必须量「#app 底边 → .uc 顶边」这条**整条带**（281px），
 * 而不是自身高度。下面两条判据就是钉这一点的。
 *
 * 另外发布量必须以 `#app` 底边为基准，**不能用 innerHeight**：
 * `#app` 高度是 `calc(100% - var(--kb-inset))`，键盘弹起时 #app 底边与输入区
 * 同步上移，两者相减与键盘无关；换成 innerHeight 就把键盘算进带高里，
 * toast 再加一次 `--kb-inset` —— 同一段高度被算两遍。与本轮键盘修复里
 * 「原生 IME inset 与视口差取一方为准而非相加」是同一类错误，故单列判据。
 */

import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
import test from 'node:test'
import assert from 'node:assert/strict'

const __dirname = fileURLToPath(new URL('.', import.meta.url))
const TOAST = join(__dirname, '..', 'Toast.vue')
const COMPOSER = join(__dirname, '..', '..', 'business', 'UnifiedComposer.vue')

/** 剥掉 CSS 注释：判据只看声明。 */
function decls(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

/** 剥掉 JS/TS 注释与块注释，判据只看代码。 */
function code(src) {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1')
}

/**
 * 取出 `fn(` 的实参原文。必须括号配平扫描而不是正则：实参里嵌套了
 * var(--x, 0px)，`[^)]*` 会在 var( 的右括号处就停住，匹配不到后面的内容。
 */
function fnArgs(src, fn) {
  const at = src.indexOf(fn + '(')
  if (at < 0) return null
  let depth = 0
  for (let i = at + fn.length; i < src.length; i++) {
    if (src[i] === '(') depth++
    else if (src[i] === ')') {
      depth--
      if (depth === 0) return src.slice(at + fn.length + 1, i)
    }
  }
  return null
}

test('toast 的 bottom 用 max 取 tabbar 与输入区带，而不是相加', async () => {
  const src = await readFile(TOAST, 'utf8')
  const css = decls(/<style[^>]*>([\s\S]*?)<\/style>/.exec(src)?.[1] ?? '')

  const rule = /\.toast\s*\{([^}]*)\}/.exec(css)
  assert.ok(rule, '找不到 .toast 规则块')
  const bottom = /bottom:\s*calc\(([\s\S]*?)\);/.exec(rule[1])
  assert.ok(bottom, '.toast 必须用 calc() 累加避让量，不能是单值')
  const expr = bottom[1].replace(/\s+/g, ' ')

  const maxArgs = fnArgs(expr, 'max')
  assert.ok(
    maxArgs !== null &&
      maxArgs.includes('--bottom-chrome-height') &&
      maxArgs.includes('--composer-inset'),
    `--composer-inset 量的已经是「含 tabbar 的整条带」（实测 281px），` +
      `必须和 --bottom-chrome-height 取 max。写成相加会把 tabbar 重复算一次，` +
      `toast 平白上浮 88px。实际拿到：${expr}`,
  )
  assert.ok(
    expr.includes('--kb-inset'),
    '`.toast 的 bottom` 缺 --kb-inset（软键盘在场时提示会被键盘直接吃掉）',
  )
  assert.doesNotMatch(
    expr,
    /--bottom-chrome-height[^\n]*\+[^\n]*--composer-inset|--composer-inset[^\n]*\+[^\n]*--bottom-chrome-height/,
    `tabbar 与输入区带不能相加（会把 tabbar 算两遍）：${expr}`,
  )
})

test('toast 不再用写死的单一 bottom 偏移', async () => {
  const src = await readFile(TOAST, 'utf8')
  assert.doesNotMatch(
    src,
    /bottom:\s*calc\(var\(--bottom-chrome-height\)\s*\+\s*var\(--space-4\)\)\s*;/,
    'toast 又退回「只算 tabbar」的旧写法：实测会压住 /ai-chat 的整条输入区工具行。',
  )
})

test('UnifiedComposer 发布 --composer-inset 且卸载时清零', async () => {
  const src = await readFile(COMPOSER, 'utf8')

  assert.ok(
    src.includes('--composer-inset'),
    'UnifiedComposer 必须发布 --composer-inset —— 输入框是内容驱动高度，' +
      'toast 只能从 JS 侧知道它当前占多宽一条带。',
  )
  assert.ok(
    /ResizeObserver/.test(src),
    '要用 ResizeObserver 跟着 autoGrow 的每次高度变化重新发布，不能只在挂载时量一次。',
  )
  assert.ok(
    /onBeforeUnmount[\s\S]{0,400}--composer-inset/.test(src),
    '卸载时必须把 --composer-inset 清零 —— 否则离开带输入区的页面后，' +
      'toast 会被一个已经不存在的元素顶高。',
  )
})

test('发布的是「#app 底边到输入框顶边」的整条带，不是自身高度', async () => {
  const body = code(await readFile(COMPOSER, 'utf8'))

  // 负控样本：自身高度就是错的。实测 .uc height=153 而整条带 281，
  // 差 128px（调用方 .composer 的 padding + padding-bottom + 上边框）。
  assert.doesNotMatch(
    body,
    /getBoundingClientRect\(\)\.height/,
    '不能把自身高度当 --composer-inset：.uc 不含调用方 .composer 的 ' +
      'padding 8px / padding-bottom calc(8px + safe) / 1px 上边框，' +
      '发自身高度会让 toast 仍压住工具行（实测 255px vs 需要 290px）。',
  )
  assert.ok(
    /root\.getBoundingClientRect\(\)\.bottom\s*-\s*el\.getBoundingClientRect\(\)\.top/.test(
      body,
    ),
    '--composer-inset 必须是「#app 底边 - 输入框顶边」，即整条带（含 tabbar）。',
  )
})

test('发布量以 #app 为基准而不是 innerHeight（否则键盘会被算两遍）', async () => {
  const body = code(await readFile(COMPOSER, 'utf8'))

  assert.ok(
    /getElementById\('app'\)/.test(body),
    '必须用 #app 的底边作基准：#app 高度是 calc(100% - var(--kb-inset))，' +
      '键盘弹起时它与输入区同步上移，相减的结果与键盘无关。',
  )
  assert.doesNotMatch(
    body,
    /innerHeight[\s\S]{0,120}--composer-inset/,
    '不能用 innerHeight 量带高：那样键盘高度会被算进 --composer-inset，' +
      'toast 再加一次 --kb-inset 就重复了。与本轮「原生 IME inset 与视口差' +
      '取一方为准而非相加」是同一类错误。',
  )
})
