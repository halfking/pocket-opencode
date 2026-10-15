// meeting-response-keys.test.ts — §109.6：会议响应的**键守恒**门。
//
// ── 这道门量的是 §109 那一族缺陷 ────────────────────────────────────
//
//   服务端 result["agent"] = true   →  前端没接  →  参考资料整块静默消失
//
// 手工修好之后没有留下任何东西阻止它重演：下一个人加一个 `result["xxx"]`
// 就会原样再犯一次，而且 200 + 无报错 + 门全绿。
//
// ── 键从哪来（不是手抄）────────────────────────────────────────────
//
//   frontend/src/api/__tests__/fixtures/meeting-response-keys.json
//   由 Go 侧 meeting_response_contract_test.go **真跑生产构造函数**生成，
//   并在每次 go test 时比对（不一致就红）。手工登记的处理器级键
//   （agent / agent_turns / refine_rejected …）由那一侧做**源码自证**。
//
// ⚠ 我第一版是**扫 Go 源码取键**，实测不成立：它把嵌套键
//   （structured_minutes.action_items）和 ws 广播载荷（{meetingId, items}，
//   那不是响应体）都算成了顶层字段 ⇒ 判据量的不是「响应体里有什么」。
//
// ── 怎么验「有没有被丢」────────────────────────────────────────────
//
//   一次真归一化。输入是**形状真实**的响应体，每个叶子塞一个全局唯一标记
//   （M$<key>$），然后深搜输出里还有没有。
//
//   布尔键（agent / refine_fallback / refine_rejected）**单独再跑一遍**：
//   只让它为 true、其余布尔键为 false ⇒ 输出里出现 `:true` 只可能来自它。
//   ⚠ 不能靠「camelCase 名字在输出里」判归属 —— refine_fallback 出来叫
//     fromFallback，refine_rejected 出来叫 rejected，名字对不上，
//     按名字判会把真丢包判成绿。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { loadBundle, FRONTEND_ROOT } from './_wire-helpers.mjs'

const contract = JSON.parse(
  readFileSync(join(FRONTEND_ROOT, 'src/api/__tests__/fixtures/meeting-response-keys.json'), 'utf8'),
) as Record<string, string[]>

const marker = (k: string) => `M$${k}$`

/**
 * 数值字段用数值探针，不能塞字符串标记。
 *
 * ⚠ 这是本门第一版自己踩的坑：给 tasks_created 塞了字符串标记后，
 *   `Number(raw.tasks_created ?? 0) || undefined` 得到 NaN ⇒ undefined
 *   ⇒ 键「被丢」—— 而生产代码完全正常。
 *   ⇒ 模板与判据必须用**同一个**探针，且探针类型要匹配服务端实际发的类型。
 */
const NUMERIC_PROBE: Record<string, Record<string, number>> = {
  // ⚠ §426：两个 *_input_runes 是服务端记下的**本次 LLM 输入规模**。
  //   它们必须用**数值**探针：塞字符串标记会被 `Number(...)` 变成 NaN
  //   ⇒ undefined ⇒ 键「被丢」—— 而生产代码完全正常（本文件头已记过一次同款坑）。
  summary: { summary_input_runes: 90211 },
  refine: { tasks_created: 90210, refine_input_runes: 90212 },
}

const probe = (kind: string, key: string): unknown => {
  const n = NUMERIC_PROBE[kind]?.[key]
  return n === undefined ? marker(key) : n
}

const needle = (kind: string, key: string): string => String(probe(kind, key))

/** 有意不消费的键，必须写理由。 */
const ALLOW = {
  summary: {
    agent_turns:
      '服务端诊断字段（agent 跑了几轮）。前端目前不展示；' +
      '若将来要做「智能体跑了 N 轮才出结果」的耗时说明，删掉本条并加消费点。',
    agent_tools:
      '服务端诊断字段（agent 调了哪些工具）。同上，目前无消费点。' +
      '§109.6 普查发现：服务端在写、前端零消费 —— 与 agent 同族但无用户可见后果。',
  },
  refine: {},
} as Record<string, Record<string, string>>

/** 形状真实的响应体模板；每个叶子带本键的标记。 */
const TEMPLATES = {
  summary: () => ({
    summary: marker('summary'),
    key_points: [marker('key_points')],
    action_items: [{ text: marker('action_items'), assignee: marker('action_items'), due: marker('action_items') }],
    decisions: [marker('decisions')],
    open_questions: [marker('open_questions')],
    references: [{ title: marker('references'), noteId: marker('references'), why: marker('references') }],
    agent: false,
    summary_fallback: false,
    summary_input_runes: probe('summary', 'summary_input_runes'),
    agent_turns: marker('agent_turns'),
    agent_tools: marker('agent_tools'),
  }),
  refine: () => ({
    refined_transcript: marker('refined_transcript'),
    translations: { en: marker('translations') },
    structured_minutes: {
      agenda: [marker('structured_minutes')],
      decisions: [marker('structured_minutes')],
      action_items: [{ text: marker('structured_minutes'), priority: marker('structured_minutes') }],
      next_meeting: marker('structured_minutes'),
    },
    todos: [{ text: marker('todos'), priority: marker('todos'), due: marker('todos') }],
    note_id: marker('note_id'),
    tasks_created: probe('refine', 'tasks_created'),
    refine_input_runes: probe('refine', 'refine_input_runes'),
    refine_fallback: false,
    refine_rejected: false,
  }),
} as Record<string, () => Record<string, unknown>>

const BOOL_KEYS = {
  summary: ['agent', 'summary_fallback'],
  refine: ['refine_fallback', 'refine_rejected'],
} as Record<string, string[]>

const camel = (k: string) => k.replace(/_([a-z])/g, (_, c: string) => c.toUpperCase())

const loadMeetings = () => loadBundle(join(FRONTEND_ROOT, 'src/api/meetings.ts'), 'meeting-keys')

const SEGMENTS = [
  { id: 's1', speaker: 'A', startMs: 0, endMs: 1000, text: '今天讨论准确率' },
]

describe('§109.6 会议响应键守恒', () => {
  it('契约夹具本身是有效的（量具先自证）', () => {
    // ⚠ 这一段是全门的前提。fixture 若为空/被截断，下面每条断言都会
    //   「零个键被丢」地通过 —— 那是恒真，不是通过。
    assert.ok(Array.isArray(contract.summary), '契约缺 summary')
    assert.ok(Array.isArray(contract.refine), '契约缺 refine')
    assert.ok(contract.summary.length >= 6, `summary 键只有 ${contract.summary.length} 个，取值路径可能坏了`)
    assert.ok(contract.refine.length >= 5, `refine 键只有 ${contract.refine.length} 个，取值路径可能坏了`)
    assert.ok(contract.summary.includes('agent'), '契约里没有 agent —— 覆盖范围被悄悄缩小了')
    assert.ok(contract.refine.includes('refine_fallback'), '契约里没有 refine_fallback')
    assert.ok(contract.recommendItem.includes('url'), '契约里没有 url（recommend 侧由 Go 门覆盖）')
  })

  for (const kind of ['summary', 'refine'] as const) {
    it(`${kind}：服务端会发的每个顶层键都活过了归一化`, async () => {
      const mod = await loadMeetings()
      const raw = TEMPLATES[kind]()
      const normalize = kind === 'summary'
        ? (r: Record<string, unknown>) => mod.normalizeSummary(r)
        : (r: Record<string, unknown>) => mod.normalizeRefine(r, SEGMENTS)

      const serialized = JSON.stringify(normalize(raw))
      const dropped: string[] = []

      for (const key of contract[kind]) {
        // 模板必须覆盖契约里的每个键。契约新增而模板没补 ⇒ 门转红，
        // 因为「模板没覆盖」和「键被丢了」在输出上长得一模一样。
        if (!(key in raw)) {
          throw new Error(
            `契约新增键 ${kind}.${key}，但本测试的响应体模板里没有它的形状。` +
            `请补进 TEMPLATES[${kind}] —— 不要直接跳过，否则这道门对它是盲的。`,
          )
        }
        if (ALLOW[kind]?.[key]) continue
        if (typeof raw[key] === 'boolean') continue // 布尔键在下面单独隔离验

        if (!serialized.includes(needle(kind, key))) {
          dropped.push(key)
        }
      }
      assert.deepEqual(
        dropped, [],
        `${kind} 响应里这些顶层键被前端静默丢弃：${dropped.join(', ')}\n` +
        `服务端在写、前端不接 ⇒ 调用方无从知道它发生过，且通常伴随 200 + 无报错。\n` +
        `确实有意忽略的，写进 ALLOW.${kind} 并附理由。`,
      )
    })

    it(`${kind}：布尔降级信号单独隔离验（只让它为 true）`, async () => {
      const mod = await loadMeetings()
      const normalize = kind === 'summary'
        ? (r: Record<string, unknown>) => mod.normalizeSummary(r)
        : (r: Record<string, unknown>) => mod.normalizeRefine(r, SEGMENTS)

      const missing: string[] = []
      for (const boolKey of BOOL_KEYS[kind]) {
        const raw = TEMPLATES[kind]()
        for (const other of BOOL_KEYS[kind]) raw[other] = other === boolKey
        // 只 boolKey 是 true ⇒ 输出里的 :true 只可能来自它
        if (!JSON.stringify(normalize(raw)).includes(':true')) missing.push(boolKey)
      }
      assert.deepEqual(missing, [], `${kind} 的布尔键被丢：${missing.join(', ')}`)
    })
  }

  it('键名重命名不影响判定（camelCase 输出也算活过）', async () => {
    const mod = await loadMeetings()
    // ⚠ 第一版这条写反了前提：传了 refined_transcript 就**不该**有 fromFallback，
    //   却断言它必须在 —— 于是测的是「我不理解的生产行为」。
    //   本地兜底的条件是服务端压根没回 refined_transcript。
    const out = mod.normalizeRefine({ tasks_created: 7 }, SEGMENTS)
    assert.equal(out.fromFallback, true, '服务端没回 refined_transcript 时必须标本地兜底')
    assert.ok(out.refinedTranscript.length > 0, '本地兜底必须用转写原文顶上，而不是空串')
    assert.equal(out.tasksCreated, 7)

    const ok = mod.normalizeRefine({ refined_transcript: '精校结果' }, SEGMENTS)
    assert.equal(ok.fromFallback, undefined, '服务端真给了精校结果时不得谎称降级')
    assert.equal(ok.refinedTranscript, '精校结果')
  })
})
