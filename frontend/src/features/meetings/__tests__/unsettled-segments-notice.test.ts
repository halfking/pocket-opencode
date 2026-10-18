// unsettled-segments-notice.test.ts — §830：停止时的 10 秒等待上限**到期不得无声**。
//
// 缺陷：等在途分段转写有 10s 上限（防挂死，合理），但超时后 `stop()` 继续往下走：
//   · mergeOverSplitSpeakers() 用当下内存分段重渲染正文；
//   · status='completed' 照写；
//   · 调用方紧接的 finalizeRecording() 从 DB 读分段做整段重转与精校。
// ⇒ 晚到的几段**不在精校输入里**；而 resolveRefinedView 在精校成功时展示
//   refinedTranscript（徽章「已精校」）⇒ 用户看到一份缺了最后几句、却标着「已精校」的正文。
//
// ⚠ 那几段稍后会自己补写 transcript，但精校视图不展示那条路
//   ⇒ 「数据在、用户看不到」是最难被发现的一种残缺。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { describeUnsettledSegments, UNSETTLED_SEGMENT_LIMIT_MS } from '../unsettled-segments-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const RT = readFileSync(join(HERE, '../../../native/recordingRuntime.ts'), 'utf8')

describe('§830 · 文案层（行为，纯函数）', () => {
  const w = describeUnsettledSegments({ pending: 2, limitMs: 10_000 })

  it('★ 必须说清还剩几段与等了多少', () => {
    assert.match(w, /仍有 2 段/, '没说清剩几段 ⇒ 读者不知道丢的是几句话')
    assert.match(w, /10000ms/, '没说清上限 ⇒ 无法与日志里的其他超时对照')
  })

  it('★★ 必须说清后果：这些段**不会**进精校，而用户默认看的是精校版', () => {
    assert.match(w, /不会.*进入本次整段重转与精校/, '没说清不会进精校')
    assert.match(w, /精校视图|精校后的正文/, '没说清用户默认看到的是哪一份')
  })

  it('★★ 必须指出补救入口，且要与真实 UI 措辞一致', () => {
    // ⚠ 不能凭空写一个不存在的入口（§820 的教训：注释/文案描述的必须真实存在）。
    //   MeetingDetailView.vue:51 的按钮标签就是「看原文」/「看精校版」。
    assert.match(w, /看原文/, '没说清去哪里看补齐的那几段')
  })

  it('负控：pending 为 0 或非法值时不得产出 NaN / 负数', () => {
    for (const v of [0, -3, Number.NaN]) {
      const t = describeUnsettledSegments({ pending: v })
      assert.doesNotMatch(t, /NaN|-\d/, `非法输入产出了假读数：${t}`)
    }
    assert.equal(UNSETTLED_SEGMENT_LIMIT_MS, 10_000, '上限常量变了 —— 调用点与文案要对齐')
  })
})

// ── 接线层（源码扫描，⚠ 对「信号变死代码」零牙，只保证它没被搬走）──
describe('§830 · 接线（扫描层，零牙已如实标注）', () => {
  it('★★ 等待之后必须有一道「还在途吗」的判定并出声', () => {
    const race = RT.indexOf('Promise.allSettled([...this.inFlightSegments])')
    assert.ok(race > 0, '找不到在途分段的等待点')
    const window = RT.slice(race, race + 700)
    // ⚠ 钉「整条语句的形状」而不是「出现过 console.warn」——
    //   否则 `void 0 && console.warn(...)`（信号变死代码）照样全绿（§818 实测）。
    assert.match(window, /^\s*console\.warn\(describeUnsettledSegments\(\{/m,
      '等待之后必须直接 warn 决策结果；没有它，超时就是完全无声的')
    assert.match(window, /if \(this\.inFlightSegments\.size\)/,
      '缺少「超时那一刻仍在途」的判定 ⇒ 每次停止都会喊，正常路径变噪声')
    assert.match(window, /pending:\s*this\.inFlightSegments\.size/,
      '没有把真实在途数量传进去 ⇒ 排查时看不到到底缺几句')
  })

  it('⚠ 前提登记：等待上限仍在（别把防挂死的保护顺手删了）', () => {
    // ⚠⚠ 必须**限定在同一段窗口里**断言：第一版直接对全文件 assert
    //   `/setTimeout\(resolve, 10_000\)/`，而**笔记录音路径**里另有一个 10s 超时
    //   ⇒ 变异把会议这条改成 0ms 之后判据照样全绿（0 红）。
    //   ⇒ 「前提登记」这类断言同样会指错位置，别以为它天然安全。
    const race = RT.indexOf('Promise.allSettled([...this.inFlightSegments])')
    assert.ok(race > 0, '找不到在途分段的等待点')
    assert.match(RT.slice(race, race + 400), /setTimeout\(resolve, 10_000\)/,
      '这条路径的 10s 上限不见了 —— 防挂死的保护被移除了')
  })

  it('⚠ 前提登记：用户确实能切到原文（否则文案里的补救入口是假的）', () => {
    const view = readFileSync(join(HERE, '../MeetingDetailView.vue'), 'utf8')
    assert.match(view, /看原文/, 'MeetingDetailView 没有「看原文」切换了 ⇒ §830 的文案会指向不存在的入口')
  })
})
