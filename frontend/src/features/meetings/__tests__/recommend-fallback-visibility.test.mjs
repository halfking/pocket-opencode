/**
 * recommend-fallback-visibility.test.mjs — §223
 *
 * 「相关 · 笔记 / 知识库 / 网络」这一块此前在 `recommendations.length === 0`
 * 时**整块静默消失**，而空数组同时是：
 *   ①「模型认为没有值得建议的建议」（正当结果，提示词明写「没有就返回空数组」）
 *   ②「这条链路压根没拿到」（LLM 报错 / 截断 / 解析失败 / 契约违反）
 * ⇒ 用户需求里「在总结同时给出参考资料与建议」这条在失败时被无声放弃。
 *
 * ⚠ 判据形态：**源码结构断言**。两个面板要真跑起来就得引入 vue 运行时，
 * 而本仓前端测试是裸 `node --test`。局限：它证明「判据写在那且条件形状对」，
 * 不证明「条件真的为真」—— 后者要真机。
 * ⚠ 因此每条都**先剥注释**，否则我自己写的说明文字会让判据恒绿
 * （这是 §109.4 与 §119 两次同款的坑）。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

// ⚠ 层级：本文件在 src/features/meetings/__tests__/ 下，往上**四**级才是 frontend/
//   （比 src/api/__tests__/ 深一级 —— 照抄那边的三层会静默指向 src/，
//   第一次跑就是「量具先自证」那条红。）
const FE = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..', '..')
const read = (rel) => readFileSync(join(FE, rel), 'utf8')

/** 剥 HTML / 注释，等长空白替换保持偏移。 */
const stripped = (src) =>
  src.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

// ⚠ 两个面板的**可见来源不同**，判据必须分开写：
//   LiveSummaryPanel（录音中）只显示 摘要 / 行动项 / 推荐，**没有 references**；
//   MeetingInsightPanel（会后）还有 references（智能体检索到的笔记）。
// ⇒ 把「!references.length」也要求到录音面板上，是我第一版判据写宽了
//   （它逼着一个不存在的字段存在）。判据写宽同样是缺陷 ——
//   它逼着生产代码去满足一个假需求。
const PANELS = [
  ['src/features/meetings/LiveSummaryPanel.vue', '录音中的即时总结面板', false],
  ['src/features/meetings/MeetingInsightPanel.vue', '会后的纪要面板', true],
]

describe('§223 参考资料与建议：没拿到必须说得出来', () => {
  it('抽取器有效：剥完注释后仍能找到判据（量具先自证）', () => {
    // 若剥注释把模板清空，下面每条都会「零命中」地通过 —— 那是恒真。
    const t = stripped(read(PANELS[0][0]))
    assert.ok(t.includes('recommendFallback'), '剥完注释后连 recommendFallback 都找不到了，剥注释器可能剥过头')
  })

  for (const [path, who, hasReferences] of PANELS) {
    it(`${who}：提示必须同时要求「没拿到」与「真的空」`, () => {
      const t = stripped(read(path))
      assert.match(t, /recommendFallback\?:\s*boolean/, `${path}：没有这个 prop`)
      // ⚠ 这段拼的是**字符串字面量**，不是正则字面量 ⇒ 反斜杠必须写两遍。
      //   第一版写成 '!?references\.length\s*&&\s*'，在字符串里
      //   `\.` 退化成 `.`、`\s` 退化成 `s` ⇒ 拼出来的正则永远不匹配，
      //   而报错文案说的是「判据形状不对」，把人引向面板而不是判据。
      //   ⇒ 判据里的转义语境和断言的转义语境必须分别数一遍。
      const refsGuard = hasReferences ? '!?references\\.length\\s*&&\\s*' : ''
      assert.match(
        t,
        new RegExp(`v-if="recommendFallback\\s*&&\\s*${refsGuard}!?recommendations\\.length`),
        `${path}：v-if 必须同时要求 recommendFallback 与「推荐为空」` +
        (hasReferences ? '（本面板还有 references，一并算作「可见来源」）' : '') +
        '——只判「为空」的话，每次模型 legitimately 没有建议时都会弹提示，那是噪音',
      )
    })

    it(`${who}：提示排在推荐块之外（否则它自己会被 v-if 吃掉）`, () => {
      const t = stripped(read(path))
      const note = t.indexOf('本轮没有拿到参考资料与建议')
      const block = t.search(/v-if="recommendations\.length"/)
      assert.ok(note > 0, `${path}：找不到提示文案`)
      assert.ok(block > 0, `${path}：找不到推荐块`)
      assert.ok(note < block, `${path}：提示排在推荐块之后，会被它的 v-if 一起吃掉`)
    })
  }

  it('composable 必须把服务端标志存下来，且拿到结果时要撤销', () => {
    const t = stripped(read('src/composables/useLiveSummary.ts'))
    assert.match(t, /const recommendFallback = ref\(false\)/, '没有 recommendFallback 状态')
    assert.match(t, /recommendFallback\.value = recs\.fallback/, '没有把服务端的 fallback 存下来')
    // ⚠ 这条是反噪音的一半：拿到结果后必须**撤销**上一次的说法，
    //   否则一次失败会让之后每一轮都挂着「没拿到」。
    assert.match(
      t,
      /recommendations\.value = recs\.items[\s\S]{0,200}?recommendFallback\.value = false/,
      '拿到推荐后没有撤销 recommendFallback：一次失败会让之后每一轮都误报',
    )
  })

  it('API 层：网络失败也算「没拿到」，不能归成「没有建议」', () => {
    const t = stripped(read('src/api/meetings.ts'))
    const fn = t.slice(t.indexOf('async recommend('), t.indexOf('async recommend(') + 1400)
    assert.ok(fn, '找不到 recommend 方法')
    assert.match(fn, /fallback:\s*res\.fallback === true/, '没有透出服务端标志')
    assert.match(
      fn,
      /catch\s*\{[\s\S]{0,300}?fallback:\s*true/,
      'catch 分支没置 fallback=true：网络/超时失败会被读成「模型没有建议」',
    )
  })
})