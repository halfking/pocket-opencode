/**
 * 输入框 placeholder 长度上限（2026-10-03 真机走查）。
 *
 * ## 病根
 *
 * 本地智能体页的 placeholder 是
 * 「给本地智能体下达任务(Enter 发送,Shift+Enter 换行)」——31 个字符，
 * 而 Redmi 真机的 CSS 视口只有 360px。实测那行字在输入框里被**截到句子中间**，
 * 读起来像排版坏了，而不是像一条占位提示。
 *
 * ## 为什么这条判据不是「检查文案好不好」
 *
 * 它量的是**长度**，不是文案。文案好不好是审美，审美判据会被「改个说法就红」
 * 淹没。这里的边界是硬的：placeholder 必须短到能在窄视口里完整显示，
 * 辅助提示（键盘操作）必须挪到有整行宽度的 hint 行。
 *
 * 阈值取 16：CJK 在 14px 字号下约 14px 宽，360px 视口减去输入框内边距与
 * 发送按钮后可用宽度约 260px，16 个字符 ≈ 224px，留有余量。
 * 这不是实测出来的精确值，是**留了余量的保守值**——真机要是仍被截断，
 * 调小它，而不是把判据删掉。
 *
 * 负控（实测过）：把 placeholder 改回原来那句 31 字符的文案，
 * 本判据转红；把 hint 行的 v-if 去掉，「键盘提示必须离开 placeholder」转红。
 */

import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))   // frontend/src/features/local-agent/__tests__
const REPO = path.join(HERE, '..', '..', '..', '..', '..')  // 仓库根
const readAbs = (p) => fs.readFileSync(path.join(REPO, p), 'utf8')

const SRC = readAbs('frontend/src/features/local-agent/LocalAgentView.vue')

/** COMPOSER_TEXT 常量表（现在 placeholder 与 hint 都从这里取）。 */
function composerText() {
  const start = SRC.indexOf('const COMPOSER_TEXT = {')
  assert.notEqual(start, -1,
    '找不到 COMPOSER_TEXT —— 判据不能靠「没找到就当没问题」过关。' +
    '若你把文案搬到了别处，请同步更新本判据的取法。')
  const end = SRC.indexOf('} as const', start)
  assert.notEqual(end, -1, 'COMPOSER_TEXT 没有正常结束')
  const block = SRC.slice(start, end)

  const pick = (key) => {
    const m = new RegExp(key + ":\\s*'([^']*)'").exec(block)
    assert.ok(m, 'COMPOSER_TEXT 里没有 ' + key)
    return m[1]
  }
  return { placeholder: pick('placeholder'), running: pick('running'), keyHint: pick('keyHint') }
}

test('placeholder 必须短到能在 360px 视口里完整显示', () => {
  const { placeholder } = composerText()
  assert.ok(
    [...placeholder].length <= 16,
    'placeholder 长度 ' + [...placeholder].length + ' > 16：「' + placeholder + '」。' +
      '真机 CSS 视口 360px，这行字会被截到句子中间。' +
      '要补充说明就把内容挪到 hint 行（keyHint），别塞进 placeholder。',
  )
  assert.ok(!/Enter|Shift|发送|换行/.test(placeholder),
    'placeholder 里不该含键盘操作说明：「' + placeholder + '」。' +
      '辅助提示属于 hint 行 —— 挤在 placeholder 里正是它被截断的原因。')
})

test('键盘提示必须存在于 hint 行，且执行中时隐藏', () => {
  const { keyHint } = composerText()
  assert.ok(/Enter/.test(keyHint) && /换行/.test(keyHint),
    'keyHint 必须真的说明键盘操作，实际 =「' + keyHint + '」')

  assert.ok(/v-if="!store\.running"[\s\S]{0,80}?COMPOSER_TEXT\.keyHint/.test(SRC),
    'keyHint 必须在模板里渲染出来（带 v-if="!store.running"，执行中输入框禁用时不该再提示发送）')
})

test('placeholder 不得再内联硬编码长句（防止有人把它改回去）', () => {
  // 这条是形状判据：挡住「直接在模板 :placeholder 里写一长串字」这种回退写法。
  // 它不替代上面两条长度判据 —— 三条都要能独立转红才算数。
  const tmpl = SRC.slice(0, SRC.indexOf('</template>'))
  const inline = /:placeholder="([^"]{12,})"/.exec(tmpl)
  assert.ok(!inline,
    '模板里的 :placeholder 是一段长字面量（' + (inline ? inline[1] : '') + '）。' +
      '请改成读 COMPOSER_TEXT.placeholder，长度上限才有单一真相源。')
})

test('该组件的本地化状态必须被如实记录，不得声称已多语言化', () => {
  // 这个组件通篇硬编码中文（expertLabel 的本地 map 也是）。本轮刻意**没有**
  // 只给两行文案接 vue-i18n —— 那会造成「一半多语言、一半不」的更糟状态。
  // 这条判据把这个决定钉住：将来有人把整个组件本地化了，就该把它删掉。
  const usesI18n = /useI18n/.test(SRC)
  assert.ok(!usesI18n,
    'LocalAgentView 现在用了 vue-i18n —— 若是有意把整个组件本地化，' +
      '请把本用例删掉，并在提交说明里写清覆盖了哪些文案。')
  assert.ok(/expertLabel[\s\S]{0,200}?Record<string, string>/.test(SRC),
    'expertLabel 的本地字典不见了 —— 它是「本组件未接 i18n」这个前提的证据，' +
      '前提变了请同步改本用例。')
})
