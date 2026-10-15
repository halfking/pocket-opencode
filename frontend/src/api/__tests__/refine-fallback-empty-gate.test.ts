import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { join } from 'node:path'
import { FRONTEND_ROOT, loadBundle } from './_wire-helpers.mjs'

// §221：兜底精校的**空值闸**。
//
// 缺陷形态：`parsed.refined_transcript ?? transcript` 只挡 `null`/`undefined`，
// **挡不住 `""`** —— 而 `{"refined_transcript":"", …}` 是真网关实测过的形态
// （服务端 `parseRefineJSON` 的注释记着「真网关实测滚动摘要时就见过」，
//   并为它专门加了一道空值闸）。
// ⇒ 那时 `refinedTranscript` 是**空串**：用户看到「精校结果一片空白」，
// 而这条路径存在的理由恰恰是「宁可给原文也别给空」。
//
// ⚠ 为什么这条不与服务端那条重复（两个不同的洞）：
//   服务端 `parseRefineJSON` 的空值闸是**真的判失败**（返回 error → refine_fallback）；
//   前端这条兜底路径**必须**给得出东西（后端 502 之后只剩这一条路），
//   所以它的正确处置是**回落到原文**而不是报错。
//   两者形状不同，但**空串都必须被认出**。

const TRANSCRIPT = '张伟：下周三跟客户过一遍。\n李娜：预算按季度复核。'

describe('§221 兜底精校必须认出空 refined_transcript', () => {
  const load = () => loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 'refine-empty-gate')

  it('阳性对照：真精校结果必须原样交出（不得一律回落成原文）', async () => {
    const mod = await load()
    const refined = '【张伟】下周三与客户评审悬界芯片。'
    const got = mod.refineFromLlmContent(JSON.stringify({ refined_transcript: refined }), TRANSCRIPT)
    // ★ 没有这一条，「永远返回 transcript」这种把功能改瘫的变异也会判绿。
    assert.equal(got.refinedTranscript, refined,
      '真精校结果被换成原文了：兜底路径宁可给原文也别给空，但**有**精校结果时必须用它')
    assert.equal(got.fromFallback, true, '兜底路径产出的结果必须自报 fromFallback')
  })

  const empties: [string, string][] = [
    ['空串（真机实测形态）', '{"refined_transcript":""}'],
    ['全空白', '{"refined_transcript":"   \\n\\t "}'],
    ['null', '{"refined_transcript":null}'],
    ['缺这个键', '{"translations":{}}'],
    ['整体是空对象', '{}'],
    ['不是 JSON 的散文', '好的，我来帮你校对这段内容。'],
    ['空响应（catch 分支的输入）', ''],
  ]

  for (const [name, content] of empties) {
    it(`${name} ⇒ 回落到原文，不得给空串`, async () => {
      const mod = await load()
      const got = mod.refineFromLlmContent(content, TRANSCRIPT)
      assert.equal(got.refinedTranscript, TRANSCRIPT,
        `${name}：精校结果应是原文，却拿到 ${JSON.stringify(got.refinedTranscript)}（空串 = 界面一片空白）`)
      assert.ok(got.refinedTranscript.length > 0, '不得给空串')
      assert.equal(got.fromFallback, true)
    })
  }

  it('空 refined_transcript 不得把结构化字段一并丢掉（回落≠清空）', async () => {
    const mod = await load()
    // ⚠ 与「整体不可解析」不同：模型**解析成功**了，只是精校正文为空。
    //   这时它的结构化字段（行动项/日程）仍然是**真的**，
    //   整份丢掉等于把「原文回填」误当成「这一轮什么都没产出」。
    const got = mod.refineFromLlmContent(JSON.stringify({
      refined_transcript: '',
      structured_minutes: { action_items: [{ text: '周五前给续期结论', due: '2026-10-09' }] },
      todos: [{ text: '把术语表接进摘要链' }],
    }), TRANSCRIPT)
    assert.equal(got.refinedTranscript, TRANSCRIPT, '正文应回落成原文')
    assert.equal(got.structuredMinutes.actionItems.length, 1,
      '结构化行动项被一起丢了：回落的是正文，不是这一轮的全部产出')
    assert.equal(got.structuredMinutes.actionItems[0].due, '2026-10-09')
    assert.equal(got.todos.length, 1)
  })

  it('合法响应缺结构化字段时必须是空数组而不是 undefined（调用方按数组迭代）', async () => {
    const mod = await load()
    const got = mod.refineFromLlmContent('{"refined_transcript":"精校正文"}', TRANSCRIPT)
    for (const [k, v] of [['agenda', got.structuredMinutes.agenda],
                           ['decisions', got.structuredMinutes.decisions],
                           ['actionItems', got.structuredMinutes.actionItems]] as const) {
      assert.ok(Array.isArray(v), `${k} 不是数组（${typeof v}）：调用方按数组迭代，读到 undefined 会炸`)
      assert.equal(v.length, 0, `${k} 应为空`)
    }
    assert.ok(Array.isArray(got.todos), 'todos 不是数组')
    assert.equal(got.structuredMinutes.nextMeeting, null)
  })
})