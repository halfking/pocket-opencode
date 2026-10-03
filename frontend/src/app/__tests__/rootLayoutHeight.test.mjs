/**
 * 根布局只有一个 #app（2026-10-03 模拟器 API 35 实测后补）。
 *
 * ## 复现的那一条
 *
 * `/ai-chat` 聚焦输入框、键盘弹起（净高 336.38px）之后，**整条输入区工具行
 * 消失**：麦克风 / 相机 / 附件 / 角色 / 优化 / 发送全被裁掉，tabbar 也被顶到
 * y=947（视口外），下半屏是一大片死区。
 *
 * CDP 量出来的链路：
 *
 *     #app        h=529.667   = 100% - 336.381   ← 对
 *     #app > div  h=193.286   = 529.667 - 336.381 ← 又扣了一遍
 *     .app-layout h=193.286
 *     main.content h=148.5    ← 只剩 top-bar 之后的残余
 *     .composer   h=201.8     ← 溢出，被 main.content 的 overflow:hidden 裁掉
 *     .msg-area   h=20        ← flex:1 + min-height:0 在不定高容器里塌成 0
 *
 * 193.286 = 529.667 − 336.381，与「键盘净高被扣两遍」逐位吻合。
 *
 * ## 根因
 *
 * `index.html` 的挂载点就是 `<div id="app">`，而 `main.ts` 走
 * `app.mount("#app")`：Vue 保留挂载容器，把 App.vue 渲染成它的**子节点**。
 * App.vue 的根节点当时也写着 `id="app"`，于是 DOM 里有两个嵌套的 #app，
 * 而 `#app { height: calc(100% - var(--kb-inset)) }`（styles.css 与 App.vue
 * 各写一份，本来只为「样式表注入顺序无关」）同时命中两者。
 *
 * **无键盘时看不出来**：--kb-inset=0 时两层等高。这个缺陷只在键盘在场时
 * 显形，所以登录/注册页（全屏固定定位表单，不吃这条链）验过是好的，
 * 旗舰页 /ai-chat 却破的。
 *
 * ## 判据为什么这么写
 *
 * 静态判据能守的是「id 唯一」与「高度链不断」两半，合起来等价于
 * 「--kb-inset 只被应用一次」。运行时那一条由设备实测背书（上面的数字）。
 */

import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
import test from 'node:test'
import assert from 'node:assert/strict'

const __dirname = fileURLToPath(new URL('.', import.meta.url))
const APP_VUE = join(__dirname, '..', '..', 'app', 'App.vue')
const MAIN_TS = join(__dirname, '..', '..', 'main.ts')
const STYLES_CSS = join(__dirname, '..', '..', 'styles.css')
// __dirname = frontend/src/app/__tests__ → 上三级是 frontend/（index.html 在那）
const ROOT = join(__dirname, '..', '..', '..')

/** 剥掉 HTML/Vue 注释：判据只看标记与声明。 */
function strip(html) {
  return html.replace(/<!--[\s\S]*?-->/g, '')
}

/** 剥掉 CSS 注释。 */
function decls(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

test('index.html 的挂载点是唯一的 id="app"', async () => {
  const html = await readFile(join(ROOT, 'index.html'), 'utf8')
  const hits = strip(html).match(/id="app"/g) ?? []
  assert.equal(
    hits.length,
    1,
    `index.html 里的 id="app" 出现 ${hits.length} 次。挂载点必须唯一：` +
      `app.mount("#app") 把 App.vue 渲染成它的子节点，App.vue 根节点若再带 ` +
      `同 id 就会嵌套出两个 #app。`,
  )
})

test('App.vue 根节点不再带 id="app"', async () => {
  const src = strip(await readFile(APP_VUE, 'utf8'))
  const template = /<template>([\s\S]*?)<\/template>/.exec(src)?.[1] ?? ''

  // 负控样本：这就是出事那天的写法。
  assert.doesNotMatch(
    template,
    /<div[^>]*\bid="app"/,
    'App.vue 根节点不能带 id="app"：它会被渲染进 index.html 的挂载点里，' +
      '形成两个嵌套的 #app，#app 的 height: calc(100% - var(--kb-inset)) ' +
      '就会把键盘净高扣两遍（实测 529.667 → 193.286，输入区工具行整条被裁）。',
  )
  assert.match(
    template,
    /<div[^>]*\bclass="app-root"/,
    'App.vue 根节点应改用 class="app-root"，让 id 归挂载点独占。',
  )
})

test('App.vue 根节点自己定高，height:100% 接力不断', async () => {
  const css = decls(/<style[^>]*>([\s\S]*?)<\/style>/.exec(await readFile(APP_VUE, 'utf8'))?.[1] ?? '')

  const rule = /\.app-root\s*\{([^}]*)\}/.exec(css)
  assert.ok(rule, '找不到 .app-root 规则块')
  assert.match(
    rule[1],
    /height:\s*100%/,
    '.app-root 必须 height:100%：.app-layout 是 height:100%，这一层若高度 auto，' +
      '百分比回退成 auto，整条 flex 链塌成内容高度，main.content 拿不到剩余空间。',
  )
  assert.match(rule[1], /min-height:\s*0/, '.app-root 需要 min-height:0，否则 flex 子项压不下去。')
})

test('键盘净高只在挂载点扣一次（#app 规则里没有第二处 --kb-inset 扣减）', async () => {
  const appCss = decls(/<style[^>]*>([\s\S]*?)<\/style>/.exec(await readFile(APP_VUE, 'utf8'))?.[1] ?? '')
  const globalCss = decls(await readFile(STYLES_CSS, 'utf8'))

  for (const [name, css] of [['App.vue', appCss], ['styles.css', globalCss]]) {
    const rule = /#app\s*\{([^}]*)\}/.exec(css)
    assert.ok(rule, `${name} 找不到 #app 规则块`)
    const deductions = (rule[1].match(/--kb-inset/g) ?? []).length
    assert.equal(
      deductions,
      1,
      `${name} 的 #app 规则里 --kb-inset 出现 ${deductions} 次，必须只扣一次。` +
        `两份样式表各写一份是允许的（注入顺序无关），但 id 唯一后它们命中的是同一个元素。`,
    )
  }
})

test('main.ts 仍挂载到 #app（诊断脚本依赖 __vue_app__ 挂在挂载点上）', async () => {
  const src = await readFile(MAIN_TS, 'utf8')
  assert.match(
    src,
    /mount\(\s*['"]#app['"]\s*\)/,
    'main.ts 必须 app.mount("#app")：仓库里几十个 scripts/*.mjs 诊断脚本靠 ' +
      'document.querySelector("#app").__vue_app__ 拿 pinia，而 __vue_app__ ' +
      '挂在 mount 元素上。',
  )
})
