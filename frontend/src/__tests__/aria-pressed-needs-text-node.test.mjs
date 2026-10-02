// aria-pressed-needs-text-node.test.mjs
//
// 带 aria-pressed 的 <button> 必须有一个**非 aria-hidden 的文字子节点**。
//
// ── 这条规则是怎么来的（真机实测，不是推测）──
//
// 2026-10-03 在 vivo V2436A / Android 16 / WebView 153 上跑 Maestro 真机流，
// `assertVisible: "快速提问"` 连续重试 11 次、每次 hierarchy 都成功返回
// （55~146ms），却**永远找不到**这个元素，最后整条流挂在 120s 超时。
//
// 逐层定位，结论是 Chromium 没问题、**Android 桥接层丢了名字**：
//
//   1) DOM 侧：元素在，44×44、display:flex、visibility:visible、
//      无任何 aria-hidden 祖先（CDP 读 getComputedStyle 实测）。
//   2) Chromium 侧：Accessibility.getPartialAXTree 返回
//      role="button"、name="快速提问"、pressed="false" —— **完全正确**。
//   3) Android 侧：uiautomator dump 里同一个元素是
//      android.widget.ToggleButton{text='', contentDescription=''} —— 名字没了。
//
// 判别式很干净：同样是 button + aria-label，
//   · aria-expanded / aria-haspopup ⇒ 映射成 android.widget.Button，名字**保留**
//     （「全部正常」「打开菜单」两个按钮实测都拿得到 contentDescription）
//   · aria-pressed               ⇒ 映射成 android.widget.ToggleButton，名字**丢弃**
// 该桥接只保留来自**内容子节点**的文字，纯 aria-label 不算数。
//
// 修法与验证：往按钮里加一个 `<span class="sr-only">快速提问</span>`
// （不是 aria-hidden）之后，同一棵树的同一元素变成
// ToggleButton{text='快速提问'}，断言立刻成立。⇒ 规则是「要有文字子节点」，
// 不是「要改掉 aria-pressed」——后者会丢掉 pressed 状态，读屏用户反而更糟。
//
// ⚠️ 别拿 DevTools 的 a11y 面板 / getPartialAXTree 当判据：它们停在第 2 层，
// 看不到第 3 层的丢失。这正是本缺陷能一路活到真机流量的原因。
//
// 本规则**只对 aria-pressed 收口**：普通 button 的 aria-label 在 Android 上
// 是好的（见上面「全部正常」的反证），把它们一并要求文字子节点属于过度约束。

import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, extname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

// 本文件在 frontend/src/__tests__/，退两级才是 frontend，退一级是 src。
const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const SRC = join(ROOT, 'src')
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

function collect(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collect(full, out)
    else if (['.ts', '.vue'].includes(extname(name))) out.push(full)
  }
  return out
}

/** 取出 <template> 正文；没有 <template> 的文件返回空串。 */
function templateOf(src) {
  const m = src.match(/<template>([\s\S]*?)<\/template>/)
  return m ? m[1] : ''
}

/**
 * 按标签配对切出每个 <button …>…</button> 的完整片段。
 *
 * 为什么不能用非贪婪正则 `/<button[\s\S]*?<\/button>/g`：那是**贪婪度**
 * 不是配对，遇到嵌套 button（<button> 里再套 <button>）会截错。
 * 仓库里确实有嵌套写法，所以老老实实扫标签深度。
 */
function buttonBlocks(tpl) {
  const blocks = []
  const tagRe = /<(\/?)button\b([^>]*)>/g
  let m
  let open = null
  while ((m = tagRe.exec(tpl)) !== null) {
    if (!m[1]) {
      if (open === null) open = { start: m.index, attrs: m[2] }
      // 嵌套 button：外层那个的结束标签在下面统一收，这里只记最外层起点。
    } else if (open !== null) {
      const text = tpl.slice(open.start, m.index + m[0].length)
      blocks.push({ attrs: open.attrs, inner: text, start: open.start })
      open = null
    }
  }
  return blocks
}

/** 去掉标签、只留可见文字（把 aria-hidden 子树整个丢掉）。 */
function visibleText(inner) {
  // 先删掉 aria-hidden="true" 的整个子树（可能嵌套，用深度计数）。
  let s = inner
  for (;;) {
    const open = s.match(/<(\w+)[^>]*aria-hidden="true"[^>]*>/)
    if (!open) break
    const tag = open[1]
    let depth = 0
    const scan = new RegExp(`<\\/?${tag}\\b[^>]*>`, 'g')
    scan.lastIndex = open.index
    let t
    let end = -1
    while ((t = scan.exec(s)) !== null) {
      if (t[0].startsWith(`</`)) {
        depth--
        if (depth === 0) { end = t.index + t[0].length; break }
      } else depth++
    }
    if (end < 0) break
    s = s.slice(0, open.index) + s.slice(end)
  }
  // 只剥标签，**保留插值原文**。
  //
  // 第一版在后面又加了一句 `.replace(/\{\{[^}]*\}\}/g, ' ')` 把插值换成空格，
  // 然后再 `.trim()` —— 于是 `{{ f.label }}`、`{{ recording ? '停止' : '录音' }}`
  // 这些**渲染出来明明有文字**的按钮全被判成「无文字子节点」，
  // 报出 LocalAgentView / MeetingListView / MeetingMicDock / SettingsLLMGateway
  // 四个假阳性。插值就是文字节点本身，替换掉等于把要找的东西删了。
  return s.replace(/<[^>]*>/g, ' ').trim()
}

describe('aria-pressed button 必须有非 aria-hidden 的文字子节点', () => {
  it('自查：判定函数能抓出缺陷样本、放过正常样本', () => {
    // 缺陷样本：就是真机上出问题的那段（aria-label + 全 aria-hidden 内容）
    const bad = `<button :aria-pressed="open" aria-label="快速提问">
      <span class="material-symbols-outlined" aria-hidden="true">forum</span>
    </button>`
    // 正常样本：内容里有真文字
    const good = `<button :aria-pressed="sel" aria-label="全部正常">
      <span class="chip-label">{{ opt.label }}</span>
    </button>`
    // 正常样本：aria-label 但**没有** aria-pressed —— 不在本规则范围内
    const notPressed = `<button aria-label="打开菜单"><span aria-hidden="true">menu</span></button>`

    const hit = (tpl) =>
      buttonBlocks(tpl).some((b) => /aria-pressed/.test(b.attrs) && visibleText(b.inner) === '')

    assert.equal(hit(bad), true, '缺陷样本必须被抓出来，否则这条规则是空跑')
    assert.equal(hit(good), false, '有文字子节点的不该被抓')
    assert.equal(hit(notPressed), false, '非 aria-pressed 不该被抓（普通 button 的 aria-label 是好的）')
  })

  it('src 下没有「aria-pressed + 无文字子节点」的 button', () => {
    const offenders = []
    for (const file of collect(SRC)) {
      const src = readFileSync(file, 'utf8')
      const tpl = templateOf(src)
      if (!tpl) continue
      const tplOffset = src.indexOf(tpl)
      for (const b of buttonBlocks(tpl)) {
        if (!/aria-pressed/.test(b.attrs)) continue
        if (visibleText(b.inner) !== '') continue
        const line = src.slice(0, tplOffset + b.start).split('\n').length
        offenders.push(`${file.replace(`${ROOT}/`, '')}:${line}  <button${b.attrs.split('\n')[0]}…>`)
      }
    }
    assert.deepEqual(
      offenders,
      [],
      '这些 aria-pressed 按钮在 Android 上会变成无名的 ToggleButton（读屏播报为空，Maestro 也 assert 不到）：\n  '
        + offenders.join('\n  '),
    )
  })
})
