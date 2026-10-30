/**
 * summary-prompt-parity.test.mjs — §66.4 的门：两份**摘要**提示词不许再漂。
 *
 * ── 它守的是什么 ──
 *
 * 摘要（用户需求里的「录音时即时总结」）同样有两条实现：
 *   ① 服务端 `POST /api/meetings/{id}/summary` → Go `buildSummaryPrompt`
 *   ② 前端 `fallbackSummarize()` → `/api/llm/chat` → TS `buildFallbackSummaryPrompt`
 *
 * ② 此前是 `meetings.ts` 里一段**内联模板字符串**，内容只有裸 schema、**零条要求**，
 * 而 schema 里每个字段的范例都是**空值**。这与 §54 在**精校链**上发现并已修复的
 * 缺陷**完全同型**（空数组/空串范例 ⇒ 模型照抄返回空）。
 *
 * 真网关实测（滚动形态，45s 预算，2 样本/模型，**修复前**同一份裸 schema 提示词）：
 *
 *     claude-haiku-4-5      due 6/7
 *     doubao-seed-2-0-mini  due 2/6
 *     glm-5.2（现役）       due 3/9
 *
 * ⇒ 换一下模型就少一半时间点。而 `summaryJSONSchema` 里**没有** next_meeting，
 * 时间点**只有** `action_items[].due` 这一个来源 ⇒ 它空了，
 * 「把时间点自动加入计划日程」在**回落路径上整条断掉**，且不报任何错。
 *
 * ⚠ ② 不是边角情况：后端摘要预算 45s < 网关 `ResponseHeaderTimeout` 85s（§59），
 * 长输入下**后端先放弃** ⇒ 前端必走 ②。
 *
 * ── 为什么是行为门而不是源码 grep ──
 *
 * 源码 grep 只能证明「提示词写在那」，证不了「**发出去的是那一份**」
 * （§51「实现对 ≠ 被用上」）。所以 A/B 两组都**真的跑一遍**，
 * 断言网关**实际收到的请求体**；只有跨语言那一份（Go）只能读源码。
 *
 * ⚠ 读 Go 源码必须**先剥注释** —— 否则本文件自己的说明性注释里
 * 出现的字面量会把「Go 那份没有」这条判据喂绿。
 *
 * 契约清单放在**本文件**（不放 summary-prompt.ts）：`check:dead-features`
 * 判「仅测试引用的导出」是对的，一个只有测试引用的导出就是没人用的导出。
 * 清单与判据同源，防漂能力不受影响，且测试文件本身就是规格。
 */

/**
 * 摘要提示词的契约清单：两份实现都必须字面包含这些内容。
 *
 * ⚠ 全部来自**真网关实测**，不是预防性设计（§54 / §65 / §66.3）：
 *  - 字段要求整体：§54 的同型缺陷（范例全空 ⇒ 产出全空）
 *  - 「due 必须能脱离上下文独立成立」：§65 实测 glm-5.2 填出「那之前」，
 *    产品侧 `resolveTodoDue` 解析失败 ⇒ 永远进不了日程且**不报错**
 */
const CONTRACT = [
  // ★ 非空范例是这一门最承重的一条 —— 它同时服务两端：
  //   服务端/前端两份提示词的 schema，以及「不许退回空数组」这条负控。
  { what: 'action_items 非空范例', literal: '"action_items":[{"text":"","assignee":"","due":""}]' },
  { what: 'summary 字段', literal: '"summary":""' },
  { what: 'key_points 字段', literal: '"key_points":[]' },
  { what: 'decisions 字段', literal: '"decisions":[]' },
  { what: 'open_questions 字段', literal: '"open_questions":[]' },
  { what: 'due 字段', literal: '"due":""' },
  // ⚠ §66.10 加回。删过一次是因为「同窗 A/B 证明规则有害」——
  //   那个 A/B 用的是 due **数量**，把「待定」占位符计成了成功。
  //   解析率口径下方向相反：无规则 32%，带规则 100%。
  { what: '只放真需要人做的事', literal: '只放转写里真实提到' },
  { what: 'due 保留原话时间表述', literal: 'due 保留用户原话里的时间表述' },
  { what: 'due 不换算成日期', literal: '不要自己换算成日期' },
  // ⚠ 同时覆盖 §65 的真 bug：glm-5.2 填「那之前」⇒ resolveTodoDue 解析失败
  { what: 'due 禁指代词', literal: '指代词' },
]


import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { describe, it, afterEach } from 'node:test'
import { fileURLToPath } from 'node:url'

import {
  activatePinia, captureRequests, loadBundle, restoreGlobals, stubBrowser, stripGoComments,
  ALWAYS_CLOUD_SYNC_POLICY_STUB,
  ALWAYS_DIRECT_AUTH_STUB,
} from './_wire-helpers.mjs'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const API_DIR = path.resolve(HERE, '..')
const ROOT = path.resolve(API_DIR, '..', '..')
const REPO = path.resolve(ROOT, '..')
const PROMPT_SRC = path.join(API_DIR, '..', 'features', 'meetings', 'summary-prompt.ts')
const MEETINGS_API = path.join(API_DIR, 'meetings.ts')
const GO_FILE = path.join(REPO, 'backend', 'internal', 'server', 'server_meeting.go')

const TRANSCRIPT = '[张伟] 下周三下午三点跟王总开客户评审会，评审会材料我准备对比表。'
const PREV = '上一轮已对齐接口联调进度。'
const SEGMENTS = [{
  id: 's1', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh',
  text: '下周三下午三点跟王总开客户评审会，评审会材料我准备对比表。',
  startMs: 0, endMs: 18000,
}]

afterEach(restoreGlobals)

/**
 * 打桩 fetch：后端摘要一律 500（触发回落），抓下 /api/llm/chat 的真实请求体。
 *
 * ⚠ 这里**不能**像精校那条那样让 /api/llm/chat 也失败：摘要回落
 * 失败时会返回 `emptySummary(fallbackText)`，请求体虽然照样抓得到，
 * 但多一层噪声会掩盖「到底打到了哪个 URL」。这里让它成功返回可解析内容。
 *
 * ⚠ 2026-10-07（§92）：本函数原先是本文件私有的，与新的
 * summary-request-parity.test.mjs 里的打桩器**形态几乎一样**。
 * 「同一段逻辑抄两遍 ⇒ 同一缺陷必然出现在两处」是 §80 记过的形状，
 * 于是工具也合并进 _wire-helpers.mjs 的 captureRequests —— 本函数现在只是它的一组路由表。
 */
function captureFallbackPrompt() {
  return captureRequests({
    '/api/llm/chat': {
      content: JSON.stringify({
        summary: '对齐了评审会时间',
        action_items: [{ text: '准备对比表', due: '下周三下午三点' }],
      }),
    },
  })
}

/** 取 Go 的 `func buildSummaryPrompt(` 函数体，并把源码层的 `\"` 还原成 `"`。 */
function goSummaryPrompt() {
  const code = stripGoComments(fs.readFileSync(GO_FILE, 'utf8'))
  const start = code.indexOf('func buildSummaryPrompt(')
  assert.ok(start > 0, '找不到 Go 的 buildSummaryPrompt —— 判据的前提失效')
  const open = code.indexOf('{', start)
  let depth = 0
  for (let i = open; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') {
      depth--
      if (depth === 0) return code.slice(open, i).replace(/\\"/g, '"')
    }
  }
  throw new Error('buildSummaryPrompt 函数体括号不配平')
}

/**
 * 取 Go 的 `const summaryFieldRules = \`…\`` 原文。
 *
 * ⚠ 必须取**常量本体**：函数体里只有 `%s` 占位符，规则正文住在常量里。
 *   ⚠ 常量里**不许**出现反引号拼接（`+ "`[]`" + `）—— 那会让「下一个反引号」
 *   落在拼接段中间，把正文截断。
 */
function goSummaryFieldRules() {
  const code = stripGoComments(fs.readFileSync(GO_FILE, 'utf8'))
  const at = code.indexOf('const summaryFieldRules = `')
  assert.ok(at > 0, '找不到 Go 的 summaryFieldRules 常量 —— 判据的前提失效')
  const open = code.indexOf('`', at)
  const close = code.indexOf('`', open + 1)
  assert.ok(close > open, 'summaryFieldRules 的反引号不配平')
  return code.slice(open + 1, close)
}

/** 取 Go 的 `const summaryJSONSchema = \`…\`` 原文（函数体里只有占位符，schema 在常量里）。 */
function goSummarySchema() {
  const code = stripGoComments(fs.readFileSync(GO_FILE, 'utf8'))
  const at = code.indexOf('const summaryJSONSchema = `')
  assert.ok(at > 0, '找不到 Go 的 summaryJSONSchema 常量 —— 判据的前提失效')
  const open = code.indexOf('`', at)
  const close = code.indexOf('`', open + 1)
  assert.ok(close > open, 'summaryJSONSchema 的反引号不配平')
  return code.slice(open + 1, close)
}

const realFetch = globalThis.fetch
const realWindow = globalThis.window
const realLocalStorage = globalThis.localStorage
afterEach(() => {
  globalThis.fetch = realFetch
  globalThis.window = realWindow
  globalThis.localStorage = realLocalStorage
})

/**
 * A 组判据提成独立函数，**A 与负控 D 共用**。
 *
 * 负控要证明「这些判据真的有牙」，办法只有一个：把同一个判据函数
 * 喂一个违规输入看它是否真的抛。若负控自己另写一遍断言逻辑，
 * 那它验证的是「负控自己的判断」，与 A 实际用的判据无关 ——
 * 形态上像变异验证，实质什么都没验。
 */
function assertSummaryContract(prompt, contract) {
  for (const { what, literal } of contract) {
    assert.ok(prompt.includes(literal), `摘要提示词缺「${what}」（字面量 ${literal}）`)
  }
  // ★ 单独钉住**非空**范例：空数组/空串范例 ⇒ 模型照抄返回空 ⇒ 下游没有输入。
  assert.ok(!/"action_items":\[\]/.test(prompt),
    'action_items 范例又变回空数组了 —— 真网关实测模型会照抄返回空，时间点进不了日程')
  // 负控：滚动分支曾退化成「相同 JSON 格式」，而「相同」在那条消息里从没出现过。
  assert.ok(!prompt.includes('严格返回相同 JSON 格式'),
    '又用回「相同 JSON 格式」—— 滚动轮消息里没有格式可「相同」，实测会让 action_items 归零')
  assert.ok(!prompt.includes('undefined'), '提示词里出现了 undefined')
}

describe('§66.4：摘要链的两份提示词', () => {
  it('A. buildFallbackSummaryPrompt 满足契约（首轮 + 滚动两形态）', async () => {
    const mod = await loadBundle(PROMPT_SRC, 'summary-prompt')

    const first = mod.buildFallbackSummaryPrompt(TRANSCRIPT)
    assertSummaryContract(first, CONTRACT)
    assert.ok(first.includes(TRANSCRIPT), '首轮提示词里没有转写正文')

    const rolling = mod.buildFallbackSummaryPrompt(TRANSCRIPT, PREV)
    assertSummaryContract(rolling, CONTRACT)
    assert.ok(rolling.includes(PREV), '滚动轮没有带上已有摘要 —— 滚动摘要会退化成首轮')
    assert.ok(rolling.includes(TRANSCRIPT), '滚动轮提示词里没有转写正文')

    // 转写必须**恰好一次**：多拼一次输入就翻倍，滚动轮首尾相接会逐轮累积。
    assert.equal(rolling.split(TRANSCRIPT).length - 1, 1, '滚动轮里转写出现了多次')
    // 空串/纯空格的 prevSummary 必须当「首轮」处理，别拼出「已有摘要：\n\n」。
    const blankPrev = mod.buildFallbackSummaryPrompt(TRANSCRIPT, '   ')
    assert.ok(!blankPrev.includes('已有摘要'), '空 prevSummary 仍然拼出了滚动分支')
  })

  it('B. summarize() 失败后，发给 /api/llm/chat 的**就是**这份提示词', async () => {
    stubBrowser()
    await activatePinia()
    const mod = await loadBundle(MEETINGS_API, 'meetings-api', { '../stores/syncPolicyStore.ts': ALWAYS_CLOUD_SYNC_POLICY_STUB, '../stores/auth': ALWAYS_DIRECT_AUTH_STUB })
    const seen = captureFallbackPrompt()

    await mod.meetingsApi.summarize('m1', SEGMENTS, PREV)

    const chat = seen.find((s) => s.path === '/api/llm/chat')
    assert.ok(chat, `回落没有打 /api/llm/chat（实际打了 ${seen.map((s) => s.path).join(', ')}）` +
      ' —— 判据不能靠「没调用就当没违规」过关')
    const sent = chat.body.messages[0].content

    // ★ 这才是「实现被用上」的证据：断言**真正发出去的那份**。
    assertSummaryContract(sent, CONTRACT)
    assert.ok(sent.includes(PREV), '发出去的提示词没带上已有摘要')
    assert.ok(sent.includes(TRANSCRIPT), '发出去的提示词里没有转写正文')
  })

  it('C. 跨语言：Go 那份必须同时满足同一份契约清单', async () => {
    const go = goSummaryPrompt() + '\n' + goSummarySchema() + '\n' + goSummaryFieldRules()

    for (const { what, literal } of CONTRACT) {
      assert.ok(go.includes(literal),
        `服务端提示词缺「${what}」（字面量 ${literal}）—— 两份实现已经漂了，` +
        `改一处请同步改另一处，并更新本文件的 CONTRACT 清单`)
    }
    assert.ok(!/"action_items":\[\]/.test(go), '服务端 action_items 范例不是非空形态')
    // 清单本身不能被清空跑路（否则 C 组变成恒真）。
    assert.ok(CONTRACT.length >= 5,
      `契约清单只剩 ${CONTRACT.length} 条 —— 它是本门的不变量，不许被缩成一条`)
  })

  it('D. 负控：判据真的有牙（把违规提示词喂给 A 组同一个判据）', async () => {
    const mod = await loadBundle(PROMPT_SRC, 'summary-prompt')
    const good = mod.buildFallbackSummaryPrompt(TRANSCRIPT)

    // ① 契约循环本身会抓：一份「只有裸 schema」的提示词必然过不了。
    const bare = '{"summary":"","key_points":[],"action_items":[],"decisions":[],"open_questions":[]}'
    assert.throws(() => assertSummaryContract(bare, CONTRACT), /缺「/)

    // ② 复活「相同 JSON 格式」—— 滚动轮那条消息里没有格式可「相同」。
    assert.throws(
      () => assertSummaryContract(good + '\n请更新摘要，严格返回相同 JSON 格式', CONTRACT),
      /相同 JSON 格式/,
    )

    // ②' 规则必须**承重**：把「due 禁指代词」那一整句删掉，判据必须抓到。
    // ⚠ 替换串必须**真的不含**被查字面量。第一版把「指代词」换成
    //   「（原为指代词禁令）」—— 换完还是含「指代词」，判据当然抓不到，
    //   报「Missing expected exception」。自己写的负控把自己骗了。
    assert.throws(
      () => assertSummaryContract(good.replace(/也不要用「那之前」「到时候」这类指代词。/, ''), CONTRACT),
      /due 禁指代词/,
    )

    // ③ ★ 空数组守卫必须是**承重**的：只靠契约字面量把关的话，
    //   「schema 变回空、但规则文字里还留着非空字面量」这种情况就漏了。
    //   所以负控要构造成**契约过得去、只有空数组守卫会响**的那种形态。
    const sneaky = good.replace('"action_items":[{"text":"","assignee":"","due":""}]', '"action_items":[]')
      + '\n"action_items":[{"text":"","assignee":"","due":""}]'
    assert.throws(() => assertSummaryContract(sneaky, CONTRACT), /action_items 范例又变回空数组/)

    // ④ undefined 也必须被抓（meta/参数为 undefined 时拼进提示词）。
    assert.throws(() => assertSummaryContract(good + '\nundefined', CONTRACT), /undefined/)
  })
})
