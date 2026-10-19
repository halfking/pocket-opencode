import assert from 'node:assert/strict'
import { afterEach, beforeEach, describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
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
 *   （`server_assistant.go` 的 `handleLLMChat`）⇒ 转写一旦超过闸 ⇒ 请求**必然 400**
 *   ⇒ 兜底**结构性不可达** ⇒ 摘要退化成「转写前 200 字 + summaryFallback 标志」
 *   （而那**不是摘要** —— §220 已把它的形状与信号钉住）。
 *
 * ── ⚠⚠ 单位是**字节**，不是字符（本节最要紧的一条）──
 *   Go 的 `len(string)` 返回**字节数**，而错误信息写的是
 *   "max 32000 **chars** per message" ⇒ **信息与判定不同形**
 *   ⇒ 任何「按字符数修」的实现，对中文内容会**超闸 3 倍**（1 个中文字 = 3 字节）。
 *   ⇒ 实测（Go）：`len("中") == 3`；32000 字节 ≈ **10666 个中文字**
 *     （**闸的语义**；⚠ §920 更正了换算成会议时长的那一半：真实提示词斜率是
 *     **844 字节/分**（真实 ASR 语料实测，含 `[说话人] ` 前缀）⇒ 摘要兜底越闸点
 *     **37.1 分钟**、精校兜底 **33.9 分钟** ⇒ 原写的「39.4 分钟」偏高）。
 *   ⇒ ⚠⚠ 同一枚钉子还更正了 §896/§912 的两个数字：
 *     `/api/embed` 的 16000 闸（`server_assistant.go` 的 `handleEmbed`，**也是字节**）
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

/**
 * §919：服务端**字节闸**的**调用方登记簿** —— 让这一族**不再复发**。
 *
 * ── 为什么要有这张表 ──
 *   这个家族缺陷在同一个仓里被撞到**三次**（§916 摘要、§917 嵌入、§919 流式纪要），
 *   根因永远是同一句：**服务端判的是 UTF-8 字节，前端按字符理解**。
 *   §918 把服务端那几道内容闸全查过了，**但调用方这一侧还会长出新的**
 *   （§919 实测：§916 修好了 `/api/llm/chat`，却漏了**同一个值的** `/api/llm/stream`
 *    ⇒ 两条会议纪要路径能力不一致：一条能用、一条对长会议**必然 400**）。
 *   ⇒ 「新增一个调用点 ⇒ 忘了按字节裁」这件事，**只能靠登记逼出来**：
 *   没人会主动想起「这个端点也有字节闸」。
 *
 * ── 这张表的形状照抄 `api-timeout-budget-table.test.mjs` ──
 *   那张表守的是同一种东西：「客户端超时必须 > 服务端预算」，
 *   它的文件头已经写明了失效模式 ——
 *   「手工核对的失效模式很清楚：下次有人加一条长路由，表不会自己更新」
 *   ⇒ 它的解法是**数据驱动的登记表**。本表同构。
 *
 * ── 闸值本身钉在哪 ──
 *   服务端 4 道 A 类内容闸（§918 的分类：内容长度闸 / 计数闸 / 请求体上限），
 *   闸值与前端镜像常量的对齐由 **Go 侧**那道跨语言门钉住
 *   （`server_llm_chat_test.go` 的 4 路比较）⇒ 本表**不重复钉**闸值。
 *
 * ── 扫描口径（两条，都是实测踩出来的）──
 *   ① **必须剥注释**：`src/api/llm-bff.ts:138` 的 JSDoc 里写着
 *      `llmBffApi.streamChat({ messages, model }, ...)` ⇒ 不剥就是**假阳**，
 *      而「一条注释把登记表变成空气门」是真实发生过的（§917 的单位门就是）。
 *   ② **必须按调用形态匹配**（`http<…>('/api/xxx'`、`llmBffApi.streamChat(`），
 *      不是裸端点字符串 ⇒ 注释/文档里提到端点的地方不算调用点。
 */
describe('§919 服务端字节闸的调用方登记簿（每个调用点都得说得出内容怎么被限住）', () => {
  interface Gate {
    /** 端点路径。 */
    endpoint: string
    /** 服务端那道闸的值（**字节**）。⚠ 闸值本身由 Go 侧跨语言门钉住。 */
    gateBytes: number
    /** 该端点在前端的**全部**调用形态（已知封装层也列进来）。 */
    callForms: RegExp[]
    /** 真正**构造请求**的位置（裸 fetch / http(…端点)）。 */
    exitForms: RegExp[]
    /** 出口数期望值。⚠ 有人绕过封装裸写 fetch ⇒ 这里会变 ⇒ 当场红。 */
    exitCount: number
  }

  const GATES: Gate[] = [
    {
      endpoint: '/api/llm/chat',
      gateBytes: 32000,
      callForms: [/http\s*(<[^>]*>)?\s*\(\s*['"`]\/api\/llm\/chat/g],
      exitForms: [/http\s*(<[^>]*>)?\s*\(\s*['"`]\/api\/llm\/chat/g],
      exitCount: 4,
    },
    {
      // ⚠ 这道闸是 §919 才发现的：它是**另一个 handler**（server_llmbff.go:127），
      //   闸值与 chat 那道**同值**（32000）⇒ 改一处不会改另一处。
      endpoint: '/api/llm/stream',
      gateBytes: 32000,
      // ⚠ 这条通道有**封装**（llm-bff.ts）⇒ 业务调用形态是封装的方法名，
      //   真正构造请求的位置在封装内部（`fetch(`${base}/api/llm/stream`)`）。
      //   ⚠ `aiStreamRuntime.spawnChat(` 也在列 —— `localagent/llm-stream.ts`
      //   **绕过**了 llmBffApi 直接调它 ⇒ 只扫封装方法会漏掉它。
      callForms: [/llmBffApi\.streamChat\s*\(/g, /aiStreamRuntime\.spawnChat\s*\(/g],
      exitForms: [/fetch\s*\(\s*`[^`]*\/api\/llm\/stream/g],
      exitCount: 1,
    },
    {
      endpoint: '/api/embed',
      gateBytes: 16000,
      callForms: [/http\s*(<[^>]*>)?\s*\(\s*['"`]\/api\/embed/g],
      exitForms: [/http\s*(<[^>]*>)?\s*\(\s*['"`]\/api\/embed/g],
      exitCount: 2,
    },
    {
      // ⚠ 这道闸判的是 **body 里的 subject 字段**（`len(body.Subject) > 500`），
      //   不是「消息内容」⇒ 它量的对象与前三个不同，但同样是**字节**闸。
      endpoint: '/api/email/send',
      gateBytes: 500,
      callForms: [/http\s*(<[^>]*>)?\s*\(\s*['"`]\/api\/email\/send/g],
      exitForms: [/http\s*(<[^>]*>)?\s*\(\s*['"`]\/api\/email\/send/g],
      exitCount: 1,
    },
  ]

  interface Site {
    /** 属于哪道闸。 */
    gate: string
    /** 调用点所在文件（相对 frontend/）。 */
    file: string
    /** 出现在该调用点**附近**（±600 字符）的标记 ⇒ 用来把同文件的多个调用点分开。 */
    marker: string
    /** 内容**怎么被限住** —— 说不出就是没限住。 */
    bound: string
    /**
     * 可否当场重测（重测键；null = 不重测）。
     * ⚠ `null` 有两种正当理由：
     *   ① 已被别的判据钉住（同一件事不重复钉）；
     *   ② **没有上限**（内容多大发多大）⇒ 钉「它超闸」等于把缺陷钉成期望，⛔ 不可。
     */
    retest: string | null
  }

  /**
   * 登记簿：**每一行 = 一个调用点 + 它的内容怎么被限住**。
   *
   * ⚠ 判别标准（沿用 §918 的那条）：**用户能不能自己绕开**。
   *   · 内容是**不可编辑的派生输入**（会议转写、邮件原主题）
   *     ⇒ 用户绕不开 ⇒ **必须修**（§918 邮件主题、§919 流式转写）。
   *   · 内容是**用户可编辑的输入**（草稿、聊天消息、查询词）
   *     ⇒ 用户自己能改短 ⇒ **登记不修**。
   *   · 失败**优雅降级** ⇒ 登记不修。
   */
  const SITES: Site[] = [
    // ── /api/llm/chat（4 个调用点）──
    {
      gate: '/api/llm/chat',
      file: 'src/api/meetings.ts',
      marker: "kind: 'meeting_summary'",
      bound: '§916 的 fitSummaryPromptToGate 按字节装配（转写取尾部 + 已有摘要取头部）',
      retest: null,   // §916 的三条行为判据已钉
    },
    {
      gate: '/api/llm/chat',
      file: 'src/api/meetings.ts',
      marker: "kind: 'meeting_refine'",
      // ⚠⚠ §920 **更正了本行的定性**：原来写「⚠ 无上限 ⇒ 登记不修」，
      //   但那**违反了我自己定的判别标准**（§918：「用户能不能自己绕开」）——
      //   转写是**用户不能编辑的派生输入** ⇒ 用户**绕不开** ⇒ 属「**必须修**」类。
      //   ⚠ 修法（裁尾 / 分段精校 / 别的）是**产品口径**（简报 140 第 ② 问）⇒ 不代拍。
      //   ⇒ §920 只做了两件事：① 量化（越闸点 **33.9 分钟**，比摘要那条早 3.2 分钟）
      //     ② 让「它必然 400」**出声**（降级行为一行未改）。
      bound: '⚠ **无上限**：整场转写直接进提示词 ⇒ 越闸点 **33.9 分钟**（§920 实测，' +
        '比摘要兜底的 37.1 分钟早 3.2 分钟）⇒ 必然 400 ⇒ 用户拿到未精校原文。' +
        '⚠ **按 §918 的判别标准它属「必须修」**（转写不可编辑 ⇒ 用户绕不开），' +
        '但修法是产品口径（简报 140 第 ② 问）⇒ §920 只加了 warn，不代拍',
      retest: null,   // §920 的门已钉（在 §920 自己的 describe 里）⇒ 本表不重测
    },
    {
      gate: '/api/llm/chat',
      file: 'src/features/email/EmailDetailView.vue',
      marker: 'async function llmChat(',
      bound: '邮件翻译走 translateBodyPipeline ⇒ **切块**后逐块翻译。' +
        '实测最大块 6000 码元（中文 18000 字节）⇒ 余量 44%',
      retest: 'translate',
    },
    {
      gate: '/api/llm/chat',
      file: 'src/features/meetings/live-translate.ts',
      marker: "kind: 'live_translate'",
      bound: '每次只送**一个 ASR 段**（调用方 ingest-speech.ts:98 传 `saved.text`）⇒ 单段远小于闸',
      retest: null,
    },
    // ── /api/llm/stream（7 个调用点）──
    {
      gate: '/api/llm/stream',
      file: 'src/api/llm-bff.ts',
      marker: 'aiStreamRuntime.spawnChat(id, input, {',
      bound: '**relay**：封装本身把 input 交给 runtime。真正的出口是本文件的 ' +
        '`fetch(`${base}/api/llm/stream`)`（全仓唯一）⇒ 内容由下面 6 个业务调用点各自限住',
      retest: null,
    },
    {
      gate: '/api/llm/stream',
      file: 'src/composables/usePromptOptimizer.ts',
      marker: 'llmBffApi.streamChat(',
      bound: '用户**草稿**（可编辑输入）⇒ 超了用户自己能改短 ⇒ 登记不修',
      retest: null,
    },
    {
      gate: '/api/llm/stream',
      file: 'src/features/ai-chat/aiChatStore.ts',
      marker: "kind: 'chat'",
      bound: '每条 message 各自送自己的内容（**用户可编辑**）⇒ 超了用户能删掉那条 ⇒ 登记不修',
      retest: null,
    },
    {
      gate: '/api/llm/stream',
      file: 'src/features/ai-chat/aiChatStore.ts',
      marker: "kind: 'optimize'",
      bound: '「原问题 + 待检查的回答」⇒ 回答是模型生成的 ⇒ 用户能删掉那条回答 ⇒ 登记不修',
      retest: null,
    },
    {
      gate: '/api/llm/stream',
      file: 'src/features/meetings/meetings-ai.ts',
      marker: "kind: 'meeting_summary'",
      bound: '§919 的 fitToStreamGate 按字节装配（转写取尾部 + 省略说明；system 过长时取头部）',
      retest: 'stream-gate',
    },
    {
      gate: '/api/llm/stream',
      file: 'src/features/notes/note-search.ts',
      marker: 'llmBffApi.streamChat(',
      bound: '两条：① extractTagsWithAi 送 `content.slice(0, 4000)` 码元' +
        '（UTF-8 单码点最多 4 字节 ⇒ 最坏 16000 字节，余量 50%）' +
        '② searchNotesWithIntent 送「问题 + 20 条候选目录」⇒ 候选 summary **无上限**' +
        '⇒ 可能超 ⇒ 失败时**优雅**降级为本地抽取式摘要',
      retest: 'slice-cap',
    },
    {
      gate: '/api/llm/stream',
      file: 'src/localagent/llm-stream.ts',
      marker: 'aiStreamRuntime.spawnChat(id, input, handlers)',
      bound: 'local agent 的会话历史（模型/工具产生）⇒ 用户能新开对话绕开 ⇒ 登记不修。' +
        '⚠ 它**绕过**了 llmBffApi 封装 ⇒ 出口仍在 llm-bff.ts（出口唯一性门盯着）',
      retest: null,
    },
    // ── /api/embed（2 个调用点）──
    {
      gate: '/api/embed',
      file: 'src/features/notes/notes-persist.ts',
      marker: "http<{ embedding: number[]; model: string }>('/api/embed'",
      bound: '§917 的 splitForEmbedding + capChunksByBytes ⇒ 每一块 ≤ 16000 字节',
      retest: null,   // §917 的行为判据已钉
    },
    {
      gate: '/api/embed',
      file: 'src/features/notes/notes-search.ts',
      marker: "http<{ embedding: number[]; model: string }>('/api/embed'",
      bound: '只送**查询词**（用户输入）⇒ 登记不修',
      retest: null,
    },
    // ── /api/email/send（1 个调用点）──
    {
      gate: '/api/email/send',
      file: 'src/api/email.ts',
      marker: "http('/api/email/send'",
      bound: '主题由 §918 的 defaultReplySubject/defaultForwardSubject **按字节**裁到 ≤ 500 字节' +
        '（原主题用户**不能编辑** ⇒ 派生输入 ⇒ 必须与服务端同口径）',
      retest: 'email-subject',
    },
  ]

  /**
   * 剥掉注释（等长空白替换，**保持偏移**）。
   *
   * ⚠ 为什么必须剥（实测）：`src/api/llm-bff.ts:138` 的 JSDoc 里写着
   *   `llmBffApi.streamChat({ messages, model }, ...)` ⇒ 不剥就是**假阳**。
   * ⚠ 字符串字面量**保留**（只剥注释）⇒ 这样 marker 匹配与调用点扫描看的是同一份文本。
   */
  function stripComments(src: string): string {
    let out = ''
    let i = 0
    let inBlock = false
    let inStr = ''
    while (i < src.length) {
      const c = src[i]
      const two = src.slice(i, i + 2)
      if (inBlock) {
        if (two === '*/') { out += '  '; i += 2; inBlock = false; continue }
        out += c === '\n' ? '\n' : ' '
        i++
        continue
      }
      if (inStr) {
        out += c
        if (c === '\\') { out += src[i + 1] ?? ''; i += 2; continue }
        if (c === inStr) inStr = ''
        i++
        continue
      }
      if (two === '/*') { out += '  '; i += 2; inBlock = true; continue }
      if (two === '//') {
        while (i < src.length && src[i] !== '\n') { out += ' '; i++ }
        continue
      }
      if (c === '"' || c === "'" || c === '`') { inStr = c; out += c; i++; continue }
      out += c
      i++
    }
    return out
  }

  interface SiteHit { gate: string; file: string; window: string }

  /** 扫出前端**真实**的调用点（按调用形态匹配 + 先剥注释）。 */
  function scanSites(): SiteHit[] {
    const out: SiteHit[] = []
    const walk = (dir: string, rel: string) => {
      for (const e of readdirSync(dir)) {
        if (e === '__tests__' || e === 'node_modules') continue
        const abs = join(dir, e)
        const relPath = rel ? `${rel}/${e}` : e
        if (statSync(abs).isDirectory()) { walk(abs, relPath); continue }
        if (!/\.(ts|vue)$/.test(e) || /\.test\.(ts|mjs)$/.test(e)) continue
        // ⚠ 剥注释后再扫：否则 JSDoc 里的示例代码会变成假阳。
        const src = stripComments(readFileSync(abs, 'utf8'))
        for (const g of GATES) {
          for (const re of g.callForms) {
            re.lastIndex = 0
            let m: RegExpExecArray | null
            while ((m = re.exec(src)) !== null) {
              out.push({
                gate: g.endpoint,
                file: relPath,
                window: src.slice(Math.max(0, m.index - 600), m.index + 600),
              })
            }
          }
        }
      }
    }
    walk(FRONTEND_ROOT, '')
    return out
  }

  /** 某个闸的**真正出口**数（裸 fetch / http(…端点)）。 */
  function countExits(g: Gate): number {
    let n = 0
    const walk = (dir: string) => {
      for (const e of readdirSync(dir)) {
        if (e === '__tests__' || e === 'node_modules') continue
        const abs = join(dir, e)
        if (statSync(abs).isDirectory()) { walk(abs); continue }
        if (!/\.(ts|vue)$/.test(e) || /\.test\.(ts|mjs)$/.test(e)) continue
        const src = stripComments(readFileSync(abs, 'utf8'))
        for (const re of g.exitForms) {
          re.lastIndex = 0
          while (re.exec(src) !== null) n++
        }
      }
    }
    walk(FRONTEND_ROOT)
    return n
  }

  it('★ 每一个真实调用点都必须在登记簿里（新增调用点必须登记）', () => {
    const sites = scanSites()
    assert.ok(sites.length > 0, '量具失效：一个调用点都没扫到 ⇒ 扫描正则坏了')
    // ⚠★ **数量**也要钉：只断言「每个都登记了」不够 ——
    //   「一个都没扫到」和「全都登记了」在结果上长得一样（都绿）。
    //   ⇒ 扫到的个数必须**恰好**等于登记簿的行数：多一个 = 有人加了新调用点没登记，
    //     少一个 = 扫描口径漏了（量具坏了）或有人删了调用点没改登记簿。
    assert.equal(sites.length, SITES.length,
      `扫到 ${sites.length} 个调用点，登记簿有 ${SITES.length} 行 ⇒ ` +
      `两者必须**恰好**相等（多 = 有未登记的新调用点；少 = 扫描口径漏了或调用点被删）`)
    for (const s of sites) {
      const hits = SITES.filter((r) => r.gate === s.gate && r.file === s.file && s.window.includes(r.marker))
      assert.equal(hits.length, 1,
        `${s.file} 的 ${s.gate} 有一个调用点，但登记簿里匹配到 ${hits.length} 行` +
        `（要求恰好 1 行）⇒ 新增调用点必须先在这里登记：它送出的内容怎么被限住？` +
        `⚠ 本表只覆盖表里列出的调用形态（裸 fetch 的相对路径写法扫不到）。`)
    }
  })

  it('★ 登记簿的每一行都还找得到它的调用点（不许留过期行）', () => {
    const sites = scanSites()
    for (const r of SITES) {
      const hit = sites.some((s) => s.gate === r.gate && s.file === r.file && s.window.includes(r.marker))
      assert.ok(hit,
        `登记项 ${r.gate} / ${r.file} / ${r.marker} 找不到对应的调用点了 ⇒ 它已失效，` +
        `请更新或删掉这一行（留着会让「登记簿」变成一张不反映现实的清单）`)
    }
  })

  it('★ 每个闸的真正出口数必须与登记一致（有人绕过封装裸写 fetch 就会红）', () => {
    for (const g of GATES) {
      const n = countExits(g)
      assert.equal(n, g.exitCount,
        `${g.endpoint} 的真正出口数是 ${n}，登记的是 ${g.exitCount} ⇒ ` +
        `要么有人绕过封装裸写了请求（必须登记 + 限住），要么某个出口被删了（登记要改）`)
    }
  })

  it('★ 重测：流式会议纪要的两条 message 各自必须过字节闸（§919 的本体）', async () => {
    const mod = await loadBundle(
      join(FRONTEND_ROOT, 'src/features/meetings/meetings-ai.ts'), 's919-stream-gate')
    const GATE = 32000
    const bytes = (s: string) => Buffer.byteLength(s, 'utf8')
    const SYSTEM = '你是会议记录助手。请用中文输出结构化会议纪要。'

    // ① 低于闸 ⇒ **逐字不变**（短会议行为零变化）。
    const short = '会议讨论了季度预算。'.repeat(100)
    const ok = mod.fitToStreamGate(SYSTEM, short)
    assert.equal(ok.transcript, short, '低于闸时转写被改了 ⇒ 短会议的产物形状变了')
    assert.equal(ok.system, SYSTEM, '低于闸时 system 被改了 ⇒ 短会议的指令变了')

    // ② ★ 单位陷阱：**字符数不超闸、字节数超闸**的转写必须被裁。
    //    11000 个中文字 = 33000 字节（字符数 11000 « 32000）⇒ 必须被裁。
    const trap = '会议讨论季度预算与交付里程碑排期。'.repeat(1000)
    assert.ok(trap.length < GATE, '前置失效：这条语料的字符数本来就该低于闸')
    assert.ok(bytes(trap) > GATE, '前置失效：这条语料的字节数本来就该超过闸')
    const fitted = mod.fitToStreamGate(SYSTEM, trap)
    assert.ok(bytes(fitted.transcript) <= GATE,
      `裁完还是 ${bytes(fitted.transcript)} 字节 > 闸 ${GATE} ⇒ 这条请求**必然 400**`)
    assert.ok(bytes(fitted.system) <= GATE,
      `裁完 system 还是 ${bytes(fitted.system)} 字节 > 闸 ${GATE} ⇒ 逐条判闸 ⇒ 必然 400`)
    assert.ok(fitted.system.includes('尾部'),
      '裁了转写却没告诉模型内容被截断 ⇒ 模型会把片段当成整场会议 ⇒ 会编造不存在的内容')

    // ③ ★ 保留**尾部**：会议结论/行动项在后段 ⇒ 裁头会丢它们。
    assert.ok(fitted.transcript.endsWith('排期。'),
      '裁剪没有保留尾部 ⇒ 会议结论/行动项（§66.4：摘要链里时间点的唯一来源）会被丢掉')

    // ④ 极端：转写巨大 + system 也巨大 ⇒ 两条仍各自过闸。
    const huge = '甲'.repeat(200000)
    const bigSystem = '指令。'.repeat(20000)
    const both = mod.fitToStreamGate(bigSystem, huge)
    assert.ok(bytes(both.transcript) <= GATE && bytes(both.system) <= GATE,
      `system 与转写都超长时裁不住：system ${bytes(both.system)} / 转写 ${bytes(both.transcript)} 字节`)
  })

  it('★ 重测：邮件翻译每块的提示词必须过字节闸', async () => {
    const te = await loadBundle(
      join(FRONTEND_ROOT, 'src/features/email/translate-email.ts'), 's919-te')
    const GATE = 32000
    // ⚠ 语料要覆盖**密度**与**结构**两种维度：
    //   ① 纯中文（3 字节/字）② emoji（4 字节/字）③ ASCII ④ HTML/URL 密集
    //   ⇒ 密度决定同一块字符数下的字节数，结构决定占位符保护是缩还是涨。
    const corpora: [string, string][] = [
      ['纯中文', '会议纪要讨论季度预算与交付里程碑排期以及客户跟进安排事项与负责人确认。'.repeat(400)],
      ['emoji 密集', '会议纪要🔥讨论季度预算🚀与交付里程碑排期📌以及客户跟进安排✅'.repeat(400)],
      ['ASCII', 'Meeting notes discuss quarterly budget and delivery milestones and client follow-up. '.repeat(400)],
      ['HTML/URL 密集', '<p>见 <a href="https://example.com/a/b/c?x=1">链接</a> 金额 1234.56 邮箱 a.b@c.de</p>\n\n'.repeat(200)],
    ]
    let worst = 0
    for (const [name, body] of corpora) {
      const chunks = te.splitForTranslation(body)
      assert.ok(chunks.length > 0, `${name}：切出 0 块`)
      for (const c of chunks) {
        const prompt = te.buildTranslatePrompt(te.protectSegments(c).text, 'en')
        const b = Buffer.byteLength(prompt, 'utf8')
        worst = Math.max(worst, b)
        assert.ok(b <= GATE,
          `${name}：有一块的提示词 ${b} 字节 > 闸 ${GATE} 字节 ⇒ 这一块**必然 400** ⇒ ` +
          '翻译会**静默**回落成原文 ⇒ 用户看到一封半中半英的邮件')
      }
    }
    assert.ok(worst > 0, '量具失效：一个字节都没量到')
  })

  it('★ 重测：note-search 的码元上限换算成字节后必须离闸有余量', () => {
    // ⚠ 这条是**源码数值 + 算术**：从源码读出 `content.slice(0, N)` 的 N，
    //   再用「UTF-8 单码点最多 4 字节」换算成最坏字节数，与闸比。
    //   ⇒ 它能抓住「有人把 4000 改成 60000」（⇒ 最坏 240000 字节 ⇒ 必然 400），
    //   而纯算术断言（4000×4 ≤ 16000）是**恒真式**，抓不住任何东西。
    const src = readFileSync(join(FRONTEND_ROOT, 'src/features/notes/note-search.ts'), 'utf8')
    const m = /content\.slice\(0,\s*(\d+)\)/.exec(stripComments(src))
    assert.ok(m, '量具失效：note-search.ts 里找不到 `content.slice(0, N)` ⇒ 写法变了，判据要跟上')
    const cap = Number(m[1])
    const worstBytes = cap * 4          // UTF-8 单码点最多 4 字节（星平面字符）
    const GATE = 32000
    assert.ok(worstBytes <= GATE * 0.75,
      `note-search 的 ${cap} 码元上限 ⇒ 最坏 ${worstBytes} 字节，` +
      `离 ${GATE} 字节的闸余量不足 25% ⇒ 容易被吃光`)
  })

  it('★ 重测：邮件主题必须按字节裁到闸内（§918）', async () => {
    const cm = await loadBundle(
      join(FRONTEND_ROOT, 'src/features/email/compose-mode.ts'), 's919-subject')
    const GATE = 500
    // ⚠ 语料必须是中文：500 字节 ≈ 166 中文字 ⇒ 300 字的中文主题必然超闸。
    const longSubject = '关于季度预算与交付里程碑排期以及客户跟进安排事项与负责人确认的会议纪要'.repeat(6)
    for (const [name, fn] of [
      ['回复', cm.defaultReplySubject],
      ['转发', cm.defaultForwardSubject],
    ] as [string, (s: string) => string][]) {
      const got = fn(longSubject)
      const b = Buffer.byteLength(got, 'utf8')
      assert.ok(b <= GATE, `${name}主题 ${b} 字节 > 闸 ${GATE} 字节 ⇒ 这封信永远发不出去`)
      assert.ok(/^(Re|Fwd):/.test(got), `${name}主题丢了前缀：${got.slice(0, 20)}`)
    }
  })

  /**
   * 本 describe 里**真正实现**的重测（retest 键 → 用例标题）。
   *
   * ⚠⚠ 为什么要有这张表（**这是本节自己踩出来的坑**）：
   *   `SITES[].retest` 这个字段最初**没有任何判据消费它** ⇒ 「我声明要重测」
   *   只是一行字 ⇒ 登记簿照样会长出「说得出怎么限住、但**没人验证过**」的行。
   *   ⇒ 本表把「声明」与「实现」**双向**钉住：
   *     ① 键集合必须**完全一致**（加一行没加重测 ⇒ 红；删了重测没改表 ⇒ 红）；
   *     ② 每个键对应的**用例必须逐字存在**（否则「实现了」只是键表上的一行字）。
   */
  const IMPLEMENTED_RETESTS: Record<string, string> = {
    translate: '★ 重测：邮件翻译每块的提示词必须过字节闸',
    'stream-gate': '★ 重测：流式会议纪要的两条 message 各自必须过字节闸（§919 的本体）',
    'slice-cap': '★ 重测：note-search 的码元上限换算成字节后必须离闸有余量',
    'email-subject': '★ 重测：邮件主题必须按字节裁到闸内（§918）',
  }

  it('★ 登记簿里标了 retest 的行，本表必须真的重测它（retest 不许是空头支票）', () => {
    const declared = [...new Set(SITES.map((s) => s.retest).filter((r): r is string => r !== null))].sort()
    assert.deepEqual(declared, Object.keys(IMPLEMENTED_RETESTS).sort(),
      `登记簿声明要重测的键（${declared.join(',') || '一个都没有'}）与本表实现的重测键` +
      `（${Object.keys(IMPLEMENTED_RETESTS).sort().join(',')}）必须**完全一致**`)
    // ⚠ 并且：每个键对应的**用例必须真的在这个文件里**（标题逐字匹配）⇒
    //   否则「实现了」只是键表上的一行字。
    const self = readFileSync(
      join(FRONTEND_ROOT, 'src/api/__tests__/summary-fallback-local-shape.test.ts'), 'utf8')
    for (const [key, title] of Object.entries(IMPLEMENTED_RETESTS)) {
      assert.ok(self.includes(`it('${title}'`),
        `retest「${key}」对应的用例不见了（标题：${title}）⇒ 要么用例被删了，要么键表过期`)
    }
  })

  it('★ 负控 ①：登记簿少一行 ⇒ 「每个调用点都必须登记」当场转红', () => {
    const sites = scanSites()
    const shrunk = SITES.slice(1)   // 故意删掉第一行
    const unregistered = sites.filter(
      (s) => !shrunk.some((r) => r.gate === s.gate && r.file === s.file && s.window.includes(r.marker)))
    assert.ok(unregistered.length > 0,
      '负控本该抓出未登记的调用点却没抓到 ⇒ 上面那条判据没在读登记簿')
  })

  it('★ 负控 ②：把一行的 marker 改错 ⇒ 该调用点立刻变成「未登记」', () => {
    // ⚠ 这条证明「marker 匹配是**精确**的」—— 若匹配是模糊的（文件级），
    //   把 marker 写错也照样能匹配上 ⇒ 这条负控就抓不到东西。
    const sites = scanSites()
    const broken = SITES.map((r, i) => (i === 0 ? { ...r, marker: 'kind: NOT_EXIST_ANYWHERE' } : r))
    const unregistered = sites.filter(
      (s) => !broken.some((r) => r.gate === s.gate && r.file === s.file && s.window.includes(r.marker)))
    assert.ok(unregistered.length > 0,
      '把 marker 改错居然还匹配得上 ⇒ marker 匹配是模糊的（文件级）⇒ 登记簿分不清同文件的多个调用点')
  })
})

/**
 * §920：精校兜底（`fallbackRefine`）的提示词**没有上限** ⇒ 超过 32000 **字节**时，
 * 服务端那道闸（`len(m.Content) > 32000`）会**必然 400** ⇒ 用户拿到的是
 * **未精校的原文**。
 *
 * ── 为什么这一条值得单立一节（它不在 §919 的登记簿里被修）──
 *   §919 的登记簿把 `meeting_refine` 这一行标成「⚠ 无上限 ⇒ 登记不修」。
 *   ⚠⚠ **那个定性违反了我自己定的判别标准**（§918 定的）：
 *   「**用户能不能自己绕开**」——转写是**用户不能编辑的派生输入** ⇒ 用户**绕不开**
 *   ⇒ 按那条标准它属于「**必须修**」类，不是「登记不修」类。
 *   ⇒ 本节把它**从「登记」升级成「已实测的缺陷」**，并把**修法**（产品口径）交回属主。
 *
 * ── 实测读数（真实 ASR 语料 `.artifacts/asr-s788/seg-asr/merged.txt`）──
 *   · 真实斜率 = 844 字节/分（`renderTranscript` 的输出**含** `[说话人] ` 前缀）
 *   · 越闸点 ≈ **33.9 分钟**（精校模板开销 3172 字节 ⇒ 比摘要那条的 522 大得多
 *     ⇒ **比摘要兜底早 3.2 分钟**撞闸）
 *   · ⚠⚠ **不对称**：同一份转写走后端那条路（`/api/meetings/{id}/refine`）
 *     **没有**这道 32000 字节闸（实测 62.5 分钟的会议送 70301 字节照样发得出去）
 *     ⇒ 「**后端精校失败时，长会议的用户一定拿不到精校**」
 *   · 这条兜底是**高频**路径（§54：后端 60s 超时 ⇒ 前端必走这里）
 *
 * ── 本节**只出声、不改降级行为**（与 §818/§819/§820 同款纪律）──
 *   修法（裁尾 / 分段精校 / 别的）是**产品口径**（简报 140 第 ② 问）⇒ 不代拍。
 *
 * ⚠ 判据里**不钉分钟数**：那依赖 `.artifacts/` 的真实语料（已 gitignore）⇒
 *   判据只钉**字节层面的性质**（真实语料读数在注释与简报里）。
 */
describe('§920 精校兜底超字节闸时必须出声（降级行为一行未改）', () => {
  const GATE = 32000
  const META = { title: '季度会议' }

  beforeEach(async () => {
    stubBrowser()
    await activatePinia()
  })
  afterEach(() => { restoreGlobals() })

  /**
   * 造一场 minutes 分钟的会议。
   *
   * ⚠⚠ **每行内容必须互不相同** —— 内容相同会被 `renderTranscript` 的**去重**
   *   裁掉（那是**正确**行为：相邻重复的发言只留净增）。
   *   ⚠ 本节第一版探针每行内容完全相同 ⇒ 产物只剩原文 55% ⇒ **读数失真**
   *     （与 §916 探针「`slice(0,66)` 截的是 33 字的串」是同族量具事故）。
   */
  function makeSegments(minutes: number) {
    const lineChars = 66
    const count = Math.ceil((minutes * 271) / lineChars)
    const base = '会议讨论季度预算与交付里程碑排期以及客户跟进安排事项与负责人确认。'
    const out = []
    for (let i = 0; i < count; i++) {
      const head = `第${i}：`
      const need = lineChars - head.length
      out.push({
        id: `s${i}`, meetingId: 'm1', speakerLabel: '说话人', lang: 'zh',
        confidence: 0.9, startMs: i * 20000, endMs: (i + 1) * 20000,
        text: head + base.repeat(Math.ceil(need / base.length)).slice(0, need),
        origin: 'asr',
      })
    }
    return out
  }

  /** 跑一次 refine（后端 502 ⇒ 走前端兜底），抓 warn / 实际请求 / 产物。 */
  async function runRefine(minutes: number) {
    const mod = await loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 's920-mod')
    const md = await loadBundle(join(FRONTEND_ROOT, 'src/features/meetings/meeting-dedup.ts'), 's920-md')
    const rp = await loadBundle(join(FRONTEND_ROOT, 'src/features/meetings/refine-prompt.ts'), 's920-rp')
    const segs = makeSegments(minutes)
    const seen = captureRequests({ '/api/meetings/m1/refine': 502, '/api/llm/chat': 400 })
    const warns: string[] = []
    const realWarn = console.warn
    console.warn = (...a: unknown[]) => { warns.push(a.map(String).join(' ')) }
    let res: unknown
    try {
      res = await mod.meetingsApi.refine('m1', segs, ['en'], META)
    } finally {
      console.warn = realWarn
    }
    const req = seen.find((s) => s.path === '/api/llm/chat')
    return {
      segs, warns, req, res,
      expected: rp.buildFallbackRefinePrompt(md.renderTranscript(segs), META),
    }
  }

  it('★ 超闸时必须出声，且 warn 里的字节数就是**实际发出去**的字节数', async () => {
    const r = await runRefine(60)
    // ⚠ 不能靠「没调用就当没违规」过关：先证明兜底请求**确实打了**。
    assert.ok(r.req, '兜底请求没打出来 ⇒ 这条判据测不到任何东西')
    const content = r.req.body.messages[0].content
    const actual = Buffer.byteLength(content, 'utf8')
    assert.ok(actual > GATE,
      `前置失效：60 分钟的兜底提示词只有 ${actual} 字节（未超闸 ${GATE}）⇒ 这条用例测不到「超闸」`)

    const hit = r.warns.find((w) => w.includes('refine-fallback'))
    assert.ok(hit, `超闸却没有出声（warns=${JSON.stringify(r.warns)}）⇒ 「它必然 400」这件事仍是静默的`)
    // ⚠⚠ 断言「warn 里出现的**每一个**『N 字节』都必须是这两个数之一」——
    //   ① `actual`（实测的提示词字节数）② `GATE`（闸值）。
    //   ⚠ 为什么不用「含 32000」这种宽松写法：只查「含」的话，
    //   **主形态被改**（32000→16000）会红，但**另有一处错值并存**抓不到
    //   ⇒ 开发者会在同一条 warn 里看到两个闸值 ⇒ 那比写错一个更糟。
    //   ⚠ 变异 V4 就是这么长牙的：V4a（主形态被改）与 V4c（并存错值）都红，
    //   而 V4b（多写一遍**正确**的 32000，语义等价）不红。
    const byteNumbers = [...hit.matchAll(/(\d+) 字节/g)].map((m) => Number(m[1]))
    assert.ok(byteNumbers.length > 0, `warn 里没有「N 字节」形态，量不到：${hit}`)
    for (const n of byteNumbers) {
      assert.ok(n === actual || n === GATE,
        `warn 里出现了既不是实测字节数（${actual}）也不是闸值（${GATE}）的数字 ${n} 字节 ⇒ ` +
        `同一条 warn 里出现两个闸值 ⇒ 开发者会读到矛盾的说明：${hit}`)
    }
    assert.ok(hit.includes(`> 闸 ${GATE} 字节`),
      `warn 里没有「> 闸 ${GATE} 字节」这个形态 ⇒ 闸值可能写错了：${hit}`)
    // ⚠★ **口径自证**：warn 里报的字节数必须与**实际发出去的**那个 content 一致。
    //   ⇒ 它抓的是「warn 报的是估算 / 报的是别的那个字符串」。
    assert.ok(hit.includes(`${actual} 字节`),
      `warn 报的字节数与实际发出去的 ${actual} 字节不一致 ⇒ 量的不是同一个东西：${hit}`)
  })

  it('★ 负控：闸内时**不得**出声（否则 warn 就成了噪声，久了没人看）', async () => {
    const r = await runRefine(10)
    assert.ok(r.req, '兜底请求没打出来')
    const content = r.req.body.messages[0].content
    const actual = Buffer.byteLength(content, 'utf8')
    assert.ok(actual <= GATE,
      `前置失效：10 分钟的兜底提示词已经 ${actual} 字节（超闸 ${GATE}）⇒ 这条负控失效`)
    const hit = r.warns.find((w) => w.includes('refine-fallback'))
    assert.equal(hit, undefined, `闸内却出声了：${hit}`)
  })

  it('★ 降级行为**一行未改**：发出去的 content 与提示词逐字一致', async () => {
    // ⚠ 这条钉的是「我加的只是 warn，没动送出去的东西」。
    //   ⇒ 防止将来有人「顺手裁一下」而**没人发现产物变了**。
    const r = await runRefine(60)
    assert.ok(r.req, '兜底请求没打出来')
    const content = r.req.body.messages[0].content
    assert.equal(content, r.expected,
      '兜底发出去的 content 与 buildFallbackRefinePrompt(renderTranscript(segments)) 不一致 ⇒ ' +
      '要么提示词构造被改了，要么转写来源被改了 ⇒ §920 只加了 warn，这两处都不该动')
  })

  it('★ 失败形态：400 之后用户拿到的是**未精校的原文**（不是空白）', async () => {
    // ⚠ 这一条是**行为事实**（不是「必然 400」那个缺陷）⇒ 将来若改成裁剪/分段，
    //   产物形状会变 ⇒ **这条判据会红 ⇒ 那时人应该更新它**（它不是把缺陷钉成期望）。
    const r = await runRefine(60)
    assert.ok(r.req, '兜底请求没打出来')
    const res = r.res as { refinedTranscript: string; fromFallback?: boolean; retentionReason?: string }
    assert.equal(res.fromFallback, true, '兜底产物没有置 fromFallback ⇒ 界面无从区分「没精校」')
    assert.ok(res.refinedTranscript.length > 0, '兜底产物是空串 ⇒ 用户看到的是空白而不是原文')
    assert.equal(res.retentionReason, 'refined-empty',
      `retentionReason 变了（${res.retentionReason}）⇒ 用户侧文案（refine-outcome-notice.ts）要跟着看`)
  })
})
