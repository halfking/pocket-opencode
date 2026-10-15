// summary-agent-honesty.test.ts — 门禁：**摘要降级时「参考资料」不得静默消失**（§109）。
//
// ── 缺口的事实链 ───────────────────────────────────────────────────
//
// 服务端 `handleMeetingSummary` 只在 **agent 分支**里写 `result["agent"] = true`；
// agent 跑满预算后回落到一次性 chat，那条路径**不带这个键**，
// 而它也**不会**带 `references`。
//
// 前端此前：
//   · `SummaryResult` **根本没声明** `agent`；
//   · `normalizeSummary` 不解析它；
//   · `toLiveSummary` 不带它（live_summary 是 JSON blob，不带就等于丢了）；
//   · 面板 `v-if="references.length"` ⇒ 降级时那一整块**静默消失**。
//
// ⇒ 用户原话「在总结同时，给出一些参考的资料与建议」，
//   在降级时变成「什么都没有，也不说为什么」。
//
// ── 为什么提示语要分两种情况 ───────────────────────────────────────
//
//   agent 跑过但没检索到用户的笔记  ⇒ 那是**真的没有**，不该说成降级
//   agent 没跑成（降级）            ⇒ 应该说清楚
//
// 说错比不说更糟（§30 的不对称原则），所以判据要卡住「不许谎称没找到」。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { loadBundle } from '../../../api/__tests__/_wire-helpers.mjs'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (f: string) => readFileSync(join(HERE, f), 'utf8')

// ⚠ 不能直接 `import ... from '../../../api/meetings.ts'`：
//   它内部 `import { http } from './http'` 是**无扩展名**的，
//   Node 的 ESM 解析器要求显式扩展名 ⇒ ERR_MODULE_NOT_FOUND
//   （与 test-coverage-waivers 里那条 flashcardIo 豁免同一族）。
//   走既有的 esbuild 打包器，与 summary-request-parity.test.mjs 同一形状。
const api = await loadBundle(resolve(HERE, '../../../api/meetings.ts'), 'meetings-api-agent')
const { normalizeSummary, toLiveSummary } = api

const REFS = [{ title: '悬界芯片二期排期', noteId: 'note-1', why: '同一项目的排期笔记' }]
const BODY = {
  summary: '对齐了量产节点。',
  key_points: ['十一月量产'],
  action_items: [{ text: '跟进验收', assignee: '张伟', due: '下周三下午三点' }],
  decisions: ['二期十一月底量产'],
  open_questions: [],
}

const blank = (m: string) => m.replace(/[^\n]/g, ' ')
/**
 * ⚠ 必须连 **HTML 注释**一起剥（§49 纪律在 .vue 上的形态）。
 *   第一版只剥 `/* *` 与 `//`，而面板模板上方那段
 *   「只有 agent !== true 才提示」的 **HTML 说明文字**里就有 agent ——
 *   接线层断言因此恒成立：变异 M3 把模板里那个条件删掉，门照样全绿。
 */
const stripComments = (t: string) => t
  .replace(/<!--[\s\S]*?-->/g, blank)
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

/**
 * 降级提示那条 v-if 的**完整表达式**（正例与负控共用同一个判定）。
 *
 * ⚠ 必须「先按 class 定位到那个 <p>，再取它的 v-if」——
 *   第一版写成「在 v-if 的值里找 muted-note」，而 muted-note 在 **class** 上，
 *   于是这个判定永远返回 null、B 层整组是恒红的假信号。
 */
const noticeCond = (t: string) => {
  // ⚠ §220：定位必须锚在**这句文案**上，不能锚 class。
  //   §220 在正文上方新增了第二条 muted-note 提示（「摘要没生成成功…」），
  //   而「取第一个 muted-note」这个定位器假设了唯一性 ——
  //   前提不再成立时它会静默抓到**另一条**提示，判据测的东西整个变了。
  //   （表现为 `条件里没有 summary 存在性守卫`，看起来像模板坏了。）
  //   ⇒ 按文案定位，class 相同不再是歧义来源。
  const p = t.match(
    /<p[^>]*muted-note[^>]*>[\s\S]{0,400}?本轮没有检索参考资料[\s\S]{0,200}?<\/p>/,
  )
  const tag = (p ?? [])[0] ?? null
  if (!tag) return null
  return (tag.match(/v-if="([^"]*)"/) ?? [])[1] ?? null
}

describe('A · 数据层：agent 降级信号必须活过每一跳', () => {
  it('★ 服务端给了 agent:true ⇒ 必须透出来', () => {
    const r = normalizeSummary({ ...BODY, agent: true, references: REFS })
    assert.equal(r.agent, true, 'agent:true 在 normalizeSummary 被丢掉了 ⇒ 降级信息死在第一跳')
    assert.equal(r.references?.length, 1)
  })

  it('★ 回落路径（没有 agent 键）⇒ agent 必须是 undefined，不能变 false', () => {
    // ⚠ 用 undefined 而不是 false：老数据没有这个键，语义是「不知道」，
    //   由展示层决定说不说；写成 false 等于凭空断言「一定降级过」。
    const r = normalizeSummary({ ...BODY })
    assert.equal('agent' in r, false,
      `回落响应里不该凭空造出 agent（实际 ${JSON.stringify(r.agent)}）`)
  })

  it('agent:false / agent:"true" 等非 true 值一律不认', () => {
    for (const v of [false, 'true', 1, null]) {
      const r = normalizeSummary({ ...BODY, agent: v })
      assert.equal('agent' in r, false, `agent=${JSON.stringify(v)} 不该被当成 agent 路径`)
    }
  })

  it('★ 信号必须活过 toLiveSummary（重新加载后还在）', () => {
    // live_summary 是 JSON blob 列，不需要迁移；
    // 但**不带**过去，重新加载后界面就会开始说假话。
    const ls = toLiveSummary(normalizeSummary({ ...BODY, agent: true, references: REFS }))
    assert.equal(ls.agent, true, 'agent 没进 LiveSummary ⇒ 重开会议后降级提示会说错')
  })
})

describe('B · 接线层：面板只在真降级时说那句话', () => {
  const src = stripComments(read('../MeetingInsightPanel.vue'))

  it('★ 条件必须同时含三件事：summary 存在 · 没有 references · agent 没跑成', () => {
    const cond = noticeCond(src)
    assert.ok(cond, '找不到那条降级提示的 v-if ⇒ 降级时界面仍然只字不提')
    // ⚠ 必须卡「条件**首项**是独立的 summary 真值守卫」，
    //   不能只判「出现过 summary」—— `summary.agent !== true` 里也有这个词，
    //   那样删掉守卫后判据照样命中（负控 2 就是这么失败了一次）。
    assert.match(cond, /^\s*summary\s*&&/, `条件里没有 summary 存在性守卫：${cond}`)
    assert.match(cond, /!references\.length/, `条件没有挂在「没有参考资料」上：${cond}`)
    assert.match(cond, /summary\.agent\s*!==\s*true/, `条件里没有 agent 判定：${cond}`)
  })

  it('负控：判据必须在本文件上真的咬得住（注释里的同名词不算数）', () => {
    // 正控：真源码里那条提示存在。
    assert.ok(noticeCond(src), '正控失败：真源码里找不到那条提示')

    // ★ 负控 1：模板条件删掉 agent 那一段 —— 必须判红。
    const noAgent = src.replace(
      'summary && !references.length && summary.agent !== true',
      'summary && !references.length',
    )
    assert.equal(
      /summary\.agent\s*!==\s*true/.test(noticeCond(noAgent) ?? ''),
      false,
      '负控失效：删掉 agent 条件后判据仍命中',
    )

    // ★ 负控 2：删掉 summary 存在性守卫 —— 必须判红。
    const noGuard = src.replace(
      'summary && !references.length',
      '!references.length',
    )
    assert.equal(
      /^\s*summary\s*&&/.test(noticeCond(noGuard) ?? ''),
      false,
      '负控失效：删掉 summary 守卫后判据仍命中',
    )

    // ★ 负控 3：剥完注释后 agent 仍然存在 —— 证明正控**不是**被
    //   注释里那句说明文字顶住的（第一版就是这样恒绿的）。
    assert.ok(
      /agent/.test(src),
      '剥完注释后真源码里反而找不到 agent —— 剥注释器可能剥过头了',
    )
  })
})