// §402 — 滚动摘要跨轮时 references 被后一轮无声抹掉
//
// 缺陷：`useLiveSummary.ts` 每 30s / 每 3 段跑一轮滚动摘要，每轮结果
// **整体覆盖** `liveSummary` 并立刻写进 DB。`references` 在这个覆盖范围内。
// 而服务端提示词明写「宁可没有 references 也不要编造」⇒ 某一轮返回空是
// **被明确鼓励的预期行为**。
//
// 实测（__probe402.mjs）：第 1 轮引用 2 条 → 第 2 轮返回空
//   ⇒ 界面 `v-if="references.length"` 整块静默消失
//   ⇒ 降级提示也不出现（判据 `!references.length && agent !== true`，
//     而第 2 轮是一次**成功**的 agent 摘要）
//   ⇒ 且已写进 DB，刷新页面也不会回来
//
// ⭐ 这是 §223「『没拿到』与『没有』是同一个观测」的跨字段版本。
//   `recommendations` 早被同一条教训修过（空结果不覆盖），references 漏了。
//
// 判据分两层：
//   行为层 —— 跑**生产纯函数**，钉「空不覆盖」+「非空必须覆盖」+ 首轮语义。
//   形态层 —— 读 `useLiveSummary.ts` 源码钉**接线**。
//             ⚠ 这层不是冗余：`mergeSummaryReferences` 完全可能变成第二个
//             `listMeetingTodos`（定义了、有测试、就是没人调）。
import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

import { mergeSummaryReferences } from '../meeting-summary-merge.ts'

const here = dirname(fileURLToPath(import.meta.url))
// 本文件在 src/features/meetings/__tests__/ 下；composables/ 与 src 同级 ⇒ 上三级。
const LIVE_SUMMARY_SRC = join(here, '..', '..', '..', 'composables', 'useLiveSummary.ts')

const ref = (noteId, title = `笔记${noteId}`) => ({ noteId, title, why: '延续上次的口径' })

describe('§402 · 滚动摘要跨轮的 references 合并', () => {
  it('#1 新一轮为空 ⇒ 保留上一轮（缺陷形态下这里是 0 条）', () => {
    const prev = [ref('n1'), ref('n2')]
    const out = mergeSummaryReferences(prev, [])
    assert.equal(out.length, 2, '空结果抹掉了上一轮引用到的笔记')
    assert.deepEqual(out.map((r) => r.noteId), ['n1', 'n2'])
  })

  it('#2 阴性对照：新一轮非空必须覆盖（否则「永远返回 prev」也能满足 #1）', () => {
    const prev = [ref('n1'), ref('n2')]
    const next = [ref('n9')]
    const out = mergeSummaryReferences(prev, next)
    assert.equal(out.length, 1)
    assert.equal(out[0].noteId, 'n9', '新一轮的非空结果没有生效')
  })

  it('#3 首轮：prev 为空时用 next', () => {
    const next = [ref('n1')]
    assert.equal(mergeSummaryReferences(undefined, next).length, 1)
    assert.equal(mergeSummaryReferences([], next).length, 1)
    assert.equal(mergeSummaryReferences(null, next).length, 1)
  })

  it('#4 两轮都空 ⇒ 空数组（不是 undefined，界面判据等价但下游不必再判）', () => {
    for (const out of [
      mergeSummaryReferences(undefined, undefined),
      mergeSummaryReferences([], []),
      mergeSummaryReferences(null, undefined),
    ]) {
      assert.ok(Array.isArray(out), '返回的不是数组')
      assert.equal(out.length, 0)
    }
  })

  it('#5 刻意边界：新一轮非空时**完全替换**，不与旧的合并累积', () => {
    // 「参考资料是否应跨轮累积」是**待拍板的产品口径**，本轮明确不替用户拍。
    // 这条判据的作用是：将来若有人「顺手优化成累积」，门要红，
    // 逼他先回文档确认那个决定，而不是无声改变语义。
    const prev = [ref('n1'), ref('n2')]
    const next = [ref('n9')]
    const out = mergeSummaryReferences(prev, next)
    assert.equal(out.length, 1, '出现了跨轮累积 —— 那是待拍板项，不该由代码悄悄决定')
    assert.equal(out[0].noteId, 'n9')
  })

  it('#6 三轮累积：新→空→新，每一轮都不该把已有抹掉', () => {
    let refs = []
    refs = mergeSummaryReferences(refs, [ref('n1')])
    assert.equal(refs.length, 1)
    refs = mergeSummaryReferences(refs, [])            // 中间轮空
    assert.equal(refs.length, 1, '中间轮把已有的抹了')
    refs = mergeSummaryReferences(refs, [ref('n2')])  // 再来新的
    assert.equal(refs.length, 1)
    assert.equal(refs[0].noteId, 'n2')
  })
})

describe('§402 · 形态层：钉住接线（防「定义了但没人调」）', () => {
  const src = readFileSync(LIVE_SUMMARY_SRC, 'utf8')

  it('#7 useLiveSummary 真的调了 mergeSummaryReferences', () => {
    assert.match(src, /import\s*\{[^}]*mergeSummaryReferences[^}]*\}\s*from\s*'[^']*meeting-summary-merge\.ts'/,
      'useLiveSummary.ts 没有 import mergeSummaryReferences —— 本模块会变成第二个 listMeetingTodos')
    assert.match(
      src,
      /references:\s*mergeSummaryReferences\(/,
      'mergeSummaryReferences 被 import 了却没用在 references 上',
    )
  })

  it('#8 裸的整体覆盖已经不在了', () => {
    assert.doesNotMatch(
      src,
      /liveSummary\.value\s*=\s*toLiveSummary\(result\)\s*$/m,
      '仍有 `liveSummary.value = toLiveSummary(result)` 这一行整体覆盖 —— §402 会被静默回退',
    )
  })

  it('#9 写库写的是**合并后**那份，而不是未合并的 toLiveSummary(result)', () => {
    // ⚠ 第一版只比了「合并点在写库点之前」——那是**恒真**的：
    //   把 updateMeeting 的实参换成未合并的 toLiveSummary(result) 时，
    //   两个字符串的位置关系完全不变，判据照样绿。
    // ⇒ 形状对但**内容**退化时，位置判据没有任何判别力，必须钉实参。
    assert.match(
      src,
      /await updateMeeting\(id,\s*\{\s*liveSummary:\s*liveSummary\.value/,
      '写库用的不是合并后的 liveSummary.value ⇒ 内存里显示正常、刷新页面资料又消失（DB 里存的是空的那份）',
    )
    assert.doesNotMatch(
      src,
      /await updateMeeting\(id,\s*\{\s*liveSummary:\s*toLiveSummary\(/,
      '写库直接用了未合并的 toLiveSummary(result) ⇒ 合并只改了内存',
    )
  })
})