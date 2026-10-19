import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import {
  FRONTEND_ROOT, activatePinia, captureRequests, loadBundle, restoreGlobals, stubBrowser,
} from './_wire-helpers.mjs'

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
/**
 * §916：摘要兜底必须**打得进** `/api/llm/chat` 的 32000 **字节**硬闸。
 *
 * ── 缺陷本体（实测）──
 *   兜底路径把**整场转写**拼进提示词再打 `/api/llm/chat`，
 *   而服务端有 `if len(m.Content) > 32000 { 400 }`
 *   （`server_assistant.go:2948`）⇒ 转写一旦超过闸 ⇒ 请求**必然 400**
 *   ⇒ 兜底**结构性不可达** ⇒ 摘要退化成「转写前 200 字 + summaryFallback 标志」
 *   （而那**不是摘要** —— §220 已把它的形状与信号钉住）。
 *
 * ── ⚠⚠ 单位是**字节**，不是字符（本节最要紧的一条）──
 *   Go 的 `len(string)` 返回**字节数**，而错误信息写的是
 *   "max 32000 **chars** per message" ⇒ **信息与判定不同形**
 *   ⇒ 任何「按字符数修」的实现，对中文内容会**超闸 3 倍**（1 个中文字 = 3 字节）。
 *   ⇒ 实测（Go）：`len("中") == 3`；32000 字节 ≈ **10666 个中文字**
 *     ≈ **39.4 分钟**的会议（@271 中文字/分，§896/§912 口径）。
 *   ⇒ ⚠⚠ 同一枚钉子还更正了 §896/§912 的两个数字：
 *     `/api/embed` 的 16000 闸（`server_assistant.go:2853`，**也是字节**）
 *     只装得下 **5333 个中文字** ≈ **19.7 分钟**，
 *     而那两节当时按「16000 字」算出来的是「63 分钟」。
 *
 * ── 修法 ──
 *   兜底拼提示词**之前**先把提示词装进闸里（按**字节**），且转写取**尾部**
 *   （滚动摘要的结构是「已有摘要 + 新增转写」⇒ 新的内容在尾部）。
 *   ⚠ 低于闸时**逐字不变** ⇒ 短会议行为零变化（下面第 3 条钉住这条）。
 *
 * ── 量具纪律 ──
 *   测字节数用 **Node 自己的** `Buffer.byteLength(s,'utf8')`，
 *   **不**用生产代码里的 `utf8ByteLength`（那是 `TextEncoder`）
 *   ⇒ 两份实现互为对照：口径漂了判据会红，而不是跟着一起错。
 *   ⚠ 语料必须是**中文**：ASCII 下字节数 == 码元数 ⇒ 单位陷阱根本测不出来
 *   （§244 那条门的老教训：ASCII 语料让整份门恒绿）。
 */
describe('§916 摘要兜底必须打得进 /api/llm/chat 的 32000 字节硬闸', () => {
  /** ⚠ 刻意**不**从生产代码 import 这个数：写死服务端那个值，
   *  前端的镜像常量一旦漂移，本判据立刻红（否则两边一起错就没人知道）。 */
  const GATE = 32000

  const bytes = (s: string) => Buffer.byteLength(s, 'utf8')
  const chars = (s: string) => [...s].length

  /** 会议转写的真实形态：中文为主。 */
  const FILLER = '会议纪要讨论季度预算与交付里程碑排期以及客户跟进安排事项'

  /** 每段内容**互不相同**（含序号）⇒ `renderTranscript` 的相邻去重不会把段丢掉
   *  （§244 ④ 的教训：整段重复会被去重逻辑整段丢弃 ⇒ 截断压根不发生）。 */
  const mkSegments = (n: number, charsPer: number) => Array.from({ length: n }, (_, i) => ({
    id: `seg-${i}`,
    meetingId: 'm1',
    speakerLabel: '张明',
    lang: 'zh',
    confidence: 1,
    startMs: i * 14_600,
    endMs: (i + 1) * 14_600,
    text: `第${i}段` + FILLER.repeat(Math.ceil(charsPer / FILLER.length) + 1).slice(0, charsPer),
    translation: null,
    origin: 'asr',
  }))

  // ⚠ loadBundle 每次都要跑一遍 esbuild（~1s）⇒ 模块只装一次，5 个用例共用。
  let _api: any = null
  let _prompt: any = null
  let _dedup: any = null
  const apiMod = async () => (_api ??= await loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 's916-api'))
  const promptMod = async () => (_prompt ??= await loadBundle(join(FRONTEND_ROOT, 'src/features/meetings/summary-prompt.ts'), 's916-prompt'))
  const dedupMod = async () => (_dedup ??= await loadBundle(join(FRONTEND_ROOT, 'src/features/meetings/meeting-dedup.ts'), 's916-dedup'))

  /** 让主摘要端点失败 ⇒ 必然走兜底；再把兜底那次请求抓下来。 */
  async function runFallback(segments: unknown[], prev?: string) {
    stubBrowser()
    await activatePinia()
    const mod = await apiMod()
    const seen = captureRequests({})   // 一个路由都没配 ⇒ 全部 404
    try {
      await mod.meetingsApi.summarize('m1', segments as never, prev)
      const hit = seen.find((s: { path: string }) => s.path === '/api/llm/chat')
      assert.ok(hit, `没有打 /api/llm/chat（实际打了 ${seen.map((s: { path: string }) => s.path).join(', ') || '一个都没打'}）`)
      return String(hit.body.messages[0].content)
    } finally {
      restoreGlobals()
    }
  }

  it('★ 长会议：兜底提示词的**字节数**必须 ≤ 闸（超了 ⇒ 这条请求必然 400）', async () => {
    const content = await runFallback(mkSegments(200, 300))
    assert.ok(bytes(content) <= GATE,
      `兜底提示词 ${bytes(content)} 字节 > 闸 ${GATE} 字节 ⇒ 这条请求**必然 400** ⇒ ` +
      '兜底永远走不到 ⇒ 摘要只会是「转写前 200 字」')
  })

  it('★ 单位陷阱：**字符数不超闸、字节数超闸**的转写必须被裁到闸的字节数以内', async () => {
    const segs = mkSegments(10, 3000)
    const full = (await dedupMod()).renderTranscript(segs)
    // ⚠ 前置：这条用例必须真的踩在「按字符判会放行、按字节判会拦下」的缝里。
    //   两个前置都不过 ⇒ 它量不到单位差异（是夹具坏了，不是产品好了）。
    assert.ok(chars(full) <= GATE,
      `前置失效：输入转写有 ${chars(full)} 个字符 > ${GATE} ⇒ 这条用例量不到单位差异`)
    assert.ok(bytes(full) > GATE,
      `前置失效：输入转写只有 ${bytes(full)} 字节 ≤ ${GATE} ⇒ 这条用例量不到单位差异`)

    const content = await runFallback(segs)
    assert.ok(bytes(content) <= GATE,
      `兜底提示词 ${bytes(content)} 字节 > 闸 ${GATE} 字节。` +
      `输入转写 ${chars(full)} 字符 / ${bytes(full)} 字节 —— 字符数不超闸、字节数超闸，` +
      '**按字符判的实现会把它原样放行**，而服务端按字节判 ⇒ 必然 400')
  })

  it('★ 负控：低于闸的会议，兜底提示词**逐字未变**（短会议行为零变化）', async () => {
    const segs = mkSegments(5, 100)
    const full = (await dedupMod()).renderTranscript(segs)
    // ⚠ 期望值用**产品自己的**提示词拼装器算，不是这里复刻一份模板。
    const expected = (await promptMod()).buildFallbackSummaryPrompt(full, undefined)
    assert.ok(bytes(expected) < GATE, '前置失效：这条用例的夹具本来就该低于闸')

    const content = await runFallback(segs)
    assert.equal(content, expected,
      '低于闸时兜底提示词被改了 ⇒ 短会议的行为变了（这条是「零变化」的护栏）')
  })

  it('★ 裁剪必须取**尾部**：最新说的话不能被裁掉', async () => {
    const segs = mkSegments(200, 300)
    segs[0].text = 'FIRSTHEAD ' + segs[0].text
    segs[segs.length - 1].text = segs[segs.length - 1].text + ' ZZZLAST'
    const full = (await dedupMod()).renderTranscript(segs)
    assert.ok(full.includes('FIRSTHEAD') && full.includes('ZZZLAST'),
      '前置失效：标记没进渲染结果（去重把它吃了？）⇒ 这条用例量不到裁剪方向')

    const content = await runFallback(segs)
    assert.ok(content.includes('ZZZLAST'),
      '兜底提示词的**结尾**没有最后一段的正文 ⇒ 裁剪取错了头（滚动摘要靠尾部才是新的内容）')
    assert.ok(!content.includes('FIRSTHEAD'),
      '开头还在 ⇒ 说明压根没裁（长会议下这条请求会 400）')
  })

  it('★ 连「已有摘要」本身都超闸时，也必须打得进闸（否则 400 照旧）', async () => {
    const segs = mkSegments(50, 300)
    // ⚠ 上一轮摘要由模型产出，而 §101 明确**不设** `max_tokens`
    //   ⇒ 它的长度不受控 ⇒ 「已有摘要自己就超闸」不是纯理论情形。
    const prev = '上一轮摘要：客户跟进排在十月中旬。'.repeat(3000)
    assert.ok(bytes(prev) > GATE, '前置失效：这条用例的「已有摘要」本来就该超闸')

    const content = await runFallback(segs, prev)
    assert.ok(bytes(content) <= GATE,
      `兜底提示词 ${bytes(content)} 字节 > 闸 ${GATE} 字节（已有摘要自己就超了闸）⇒ 必然 400`)
  })
})
