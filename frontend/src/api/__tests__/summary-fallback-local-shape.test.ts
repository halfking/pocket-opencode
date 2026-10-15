import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { FRONTEND_ROOT, loadBundle } from './_wire-helpers.mjs'

const PANEL = 'src/features/meetings/MeetingInsightPanel.vue'

/** 剥 HTML 注释（等长空白替换，保持偏移）。漏了它判据就会读到自己写的注释。 */
function panelTemplate(): string {
  const src = readFileSync(join(FRONTEND_ROOT, PANEL), 'utf8')
  return src.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))
}

// §220：前端**自己**造出的回落形状必须自带 `summaryFallback`。
//
// 为什么这道门单独存在（跨端那道 `meeting-response-keys.test.ts` 抓不到）：
//   那道门量的是「**服务端发的键**有没有被 normalizeSummary 接住」。
//   本地兜底的两个形状是前端自己造的，服务端压根没参与：
//     ① emptySummary()        ← parseSummaryJson 的两处回落
//     ② fallbackSummarize 的 catch 分支（连降级链自己都失败）
//   两者都不在那道门的取值路径上 ⇒ 去掉 summaryFallback 没有任何门会红。
//
// ⚠ 与同目录 summary-fallback-guard.test.ts 的分工：那道门是**源码扫描**
//   （因为裸 node --test 加载不了本模块），只能证明「代码写在那」；
//   本文件走 esbuild 预打包后**行为断言**，证明它真的会出现在产物里。
//   两者同源不重复：一个防「没写」，一个防「写了但不生效」。

const TRANSCRIPT = '张伟：下周三跟客户过一遍。\n李娜：预算按季度复核。'

describe('§220 前端本地回落形状必须自带 summaryFallback', () => {
  it('emptySummary 置位 summaryFallback，且 summary 是截断后的转写', async () => {
    const mod = await loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 'summary-fallback-local')

    const got = mod.emptySummary(TRANSCRIPT)
    // ★ 核心断言：回落形状必须能被区分。
    //   这个形状结构完整、会被当成功渲染（§220），不置位就退化成 §218 的缺陷。
    assert.equal(got.summaryFallback, true,
      'emptySummary 没置 summaryFallback：它的产物结构完整、会被当成功渲染，' +
      '而 summary 是转写片段 ⇒ 界面无从区分')
    assert.equal(got.summary, TRANSCRIPT.slice(0, 500),
      'emptySummary 的 summary 不再是转写片段：§218 的注释依赖这个事实')

    // 连带：四个结构化字段必须是**空数组**而不是 undefined ——
    // 调用方按数组迭代，读到 undefined 会炸；读到空数组才是「没有」。
    for (const k of ['keyPoints', 'actionItems', 'decisions', 'openQuestions']) {
      assert.ok(Array.isArray(got[k]), `${k} 不是数组（得到 ${typeof got[k]}）`)
      assert.equal(got[k].length, 0, `${k} 应为空：回落不等于「没有待办」，只是没拿到`)
    }
  })

  it('超长转写按 500 字符截断（与服务端 emptySummary 同口径）', async () => {
    const mod = await loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 'summary-fallback-local2')
    const long = 'x'.repeat(1200)
    const got = mod.emptySummary(long)
    assert.equal(got.summary.length, 500, '截断口径变了：前后端会给出不同长度的「转写」')
  })

  it('normalizeSummary 在服务端带信号时必须透出（正向）', async () => {
    const mod = await loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 'summary-fallback-local3')
    const got = mod.normalizeSummary({ summary: '一段转写', summary_fallback: true })
    assert.equal(got.summaryFallback, true,
      '服务端已声明回落，前端没透出 ⇒ 界面会按「一次性摘要」解释，说的正是错的那句')
  })

  it('normalizeSummary 在服务端没带信号时**不得**自己臆造', async () => {
    const mod = await loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 'summary-fallback-local4')
    // 负控：真摘要不该被标成回落。若这里也置位，等于把所有正常摘要都贴上
    // 「这不是摘要」的标签 —— 比不说还糟（§30：宁可少说一句，不可说错一句）。
    const got = mod.normalizeSummary({ summary: '本季度把录制链的准确率定位到切段边界' })
    assert.notEqual(got.summaryFallback, true,
      '真摘要被误标成回落：老数据与真摘要共用 undefined，只有显式 true 才该多说话')
  })
})

// ⚠ 下面这组是**源码结构断言**，不是行为断言。理由与同目录
// summary-fallback-guard.test.ts 同源：模板要真跑起来就得引入 vue 运行时，
// 而本仓测试是裸 node --test。局限写明：它证明「判据写在那且位置对」，
// 不证明「条件真的为真」—— 后者由上面那组行为断言承担一半
// （信号能不能造出来），另一半要真机。
describe('§220 面板措辞不得说错', () => {
  const tpl = panelTemplate()

  it('抽取器有效：剥完注释后仍能看到两条判据（量具先自证）', () => {
    // 若剥注释把整个模板清空，下面两条都会「零命中」地通过 —— 那是恒真。
    const n = (tpl.match(/summary\.summaryFallback|summary\?\.summaryFallback/g) || []).length
    assert.ok(n >= 2, `剥完注释后只剩 ${n} 处 summaryFallback 判据，抽取器可能坏了`)
  })

  it('「已回落到一次性摘要」那句必须排除 summaryFallback', () => {
    assert.match(
      tpl,
      /summary\.agent !== true\s*&&\s*summary\.summaryFallback !== true/,
      '§109 那条降级提示没排除 summaryFallback：连降级链都失败时它会照说' +
      '「已回落到一次性摘要」，而那个一次性的正是失败者 ⇒ 说的正是最不可信的一句',
    )
  })

  it('「这是录音原文」那条必须排在正文**之上**', () => {
    const note = tpl.indexOf('摘要没生成成功')
    const body = tpl.search(/<p[^>]*class="summary"/)
    assert.ok(note > 0, '找不到回落提示')
    assert.ok(body > 0, '找不到摘要正文段落')
    assert.ok(note < body,
      '回落提示排在正文**下面**：用户从上往下读时先看到「会议纪要 + 一段原文」，' +
      '隔了几个空区块才读到「这不是摘要」')
  })

  it('提示必须被 finalSummary 排除（手改稿时正文不是转写）', () => {
    // ⚠ 形态与同模板既有的降级提示一致：`summary &&` 打头再取字段，**不用可选链**。
    //   check:vacuous-guard 会把「祖先链无法保证实体非空」的可选链比较判红 ——
    //   `summary` 是可空 prop，而 template > aside > div 这条链不保证它非空。
    //   语义上 `summary?.x === true` 在实体缺席时是**恒假**（安全方向），
    //   但那道门管的是「可选链比较 + 实体可能缺席」这个形态本身。
    //   ⇒ 这里同时钉住语义与形态：改成实链后两边都满足。
    assert.match(
      tpl,
      /summary\s*&&\s*summary\.summaryFallback === true\s*&&\s*!finalSummary/,
      '提示没排除 finalSummary：用户手改过稿子时正文是 finalSummary，' +
      '此时说「显示的是录音原文」就是说错了',
    )
    assert.doesNotMatch(
      tpl,
      /summary\?\.summaryFallback/,
      '别用可选链比较：summary 是可空 prop，祖先链不保证它非空（check:vacuous-guard 会红）',
    )
  })
})