/**
 * 固定定位浮层的键盘避让守卫（2026-10-03 模拟器 API 35 实测后补）。
 *
 * ## 复现的那一条
 *
 * 登录成功后弹出「创建主密码」对话框。此时键盘还开着（焦点仍在登录页密码框），
 * 对话框底部的「取消 / 确认」按钮**正好被键盘上沿切掉**，而这两个按钮是
 * 唯一能继续的入口——用户被卡在一个看得见标题、看不见按钮的弹窗里。
 *
 * CDP 读到的几何（决定性）：
 *
 *     .dialog-mask  position: fixed; inset: 0;  rectBottom: 915   ← 占满整个视口
 *     .dialog       rectBottom: 624
 *     .actions 按钮  top: 560  bottom: 600
 *     #app          height: 530   （= 915 - --kb-inset 336）
 *
 * 也就是说：内容层（`#app`）随 `--kb-inset` 收缩了，**浮层没有**。
 * `position:fixed; inset:0` 锚的是视口，键盘弹起时视口不变（正是
 * `useKeyboardInset` 那条 overlay 路径），于是浮层底部仍然停在键盘下面。
 *
 * ## 为什么它是一类问题而不是一个 bug
 *
 * `BottomSheet.vue` 早就写对了：`inset: 0 0 var(--kb-inset, 0px) 0`。
 * 但那是**逐个组件自觉**才对的——全仓另有 14 处 `position:fixed; inset:0`
 * 的遮罩，其中一部分同样装着输入框，却没有消费 `--kb-inset`。
 * 靠「记得写」必然漏，所以本文件把前提钉住。
 *
 * ## 判据
 *
 * 自身模板里含 `<input>` / `<textarea>`，且样式里有 `position:fixed; inset:0`
 * 的遮罩规则，**必须**消费 `--kb-inset`。
 *
 * 范围限定在「遮罩规则 + 自身含输入框」是有意的：
 *  - 不含输入框的遮罩（Loading 全屏、UpdateChecker 更新弹层、JsonBlock
 *    全屏预览）本来就不受键盘影响，不该被这条判据卷进来；
 *  - `position:fixed` 但只锚在 top / 右上角的小挂件（PKM 的「保存中」气泡、
 *    设置页顶栏）也不受影响——它们的底边不在键盘区。
 * 这两类都实测过/结构上不成立，所以排除是有依据的，不是放水。
 */

import { readFile } from 'node:fs/promises'
import { readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import assert from 'node:assert/strict'

const __dirname = fileURLToPath(new URL('.', import.meta.url))
const SRC = join(__dirname, '..', '..')

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name.startsWith('.')) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (name.endsWith('.vue')) out.push(p)
  }
  return out
}

const files = walk(SRC)

/** 剥掉 CSS 注释：判据只看声明，不看「关于声明的散文」。 */
function declarationsOnly(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

test('含输入框的 fixed 遮罩必须消费 --kb-inset（否则底部按钮被键盘切掉）', async () => {
  const offenders = []

  for (const file of files) {
    const source = await readFile(file, 'utf8')

    const tpl = /<template>([\s\S]*?)<\/template>/.exec(source)?.[1] ?? ''
    const css = declarationsOnly(
      /<style[^>]*>([\s\S]*?)<\/style>/.exec(source)?.[1] ?? '',
    )

    // 自身模板必须真的有输入框（弹层里塞的输入框也算）
    if (!/<(input|textarea)\b/.test(tpl)) continue

    for (const rule of css.matchAll(/([^{}]*)\{([^}]*)\}/g)) {
      const selector = rule[1].trim()
      const body = rule[2]
      if (!/position:\s*fixed/.test(body)) continue
      if (!/inset:\s*0\b/.test(body)) continue
      if (/kb-inset/.test(body)) continue

      offenders.push(
        `${relative(SRC, file)}  ${selector}\n` +
          `    position:fixed + inset:0 锚的是视口；键盘弹起时视口不变（overlay 路径），\n` +
          `    浮层底边会停在键盘下面——实测「取消/确认」按钮 bottom 落在键盘区内。\n` +
          `    修法：inset 改成 \`inset: 0 0 var(--kb-inset, 0px) 0\`（同 BottomSheet.vue）`,
      )
    }
  }

  assert.deepEqual(
    offenders,
    [],
    `以下遮罩装着输入框却不消费 --kb-inset：\n${offenders.join('\n')}`,
  )
})

test('BottomSheet 保持「底边随键盘抬升」的既有正确写法（回归护栏）', async () => {
  const source = await readFile(join(SRC, 'components', 'base', 'BottomSheet.vue'), 'utf8')
  const css = declarationsOnly(/<style[^>]*>([\s\S]*?)<\/style>/.exec(source)?.[1] ?? '')
  assert.match(
    css,
    /inset:\s*0\s+0\s+var\(--kb-inset/,
    'BottomSheet 的 overlay 必须用 inset: 0 0 var(--kb-inset) 0 —— ' +
      '它是本仓唯一一处正确处理键盘的全屏浮层，判据以它为准。',
  )
})
