// live-summary-fields-rendered.test.ts — §124：**类型里声明了 ≠ 用户看得到**。
//
// ── 缺口的事实链 ───────────────────────────────────────────────────
//
// 讯飞听见的「会议纪要三段式」是：全文概要 - **决策结论** - 待办事项。
// §41.5 那张对标表里写着「会议纪要三段式 ✅ §37 schema」，看着已经对齐。
//
// 但那一行只验了 **schema**（字段存不存在），没验**界面上渲不渲染**。
// 实际情况是：
//
//	提示词 schema 要求输出 decisions / open_questions
//	→ 服务端 parseSummaryJSON 解析
//	→ normalizeSummary 归一化（api/meetings.ts）
//	→ toLiveSummary 带进 live_summary（JSON blob 列）
//	→ LiveSummary 类型里声明
//	→ ★ MeetingInsightPanel **一个都没渲染**
//
// ⇒ 模型在算、类型在声明、blob 在存，**只有界面上没有**。
// 决策是三段式的中间一柱，缺它就只剩两柱。
//
// ★ 这道门不只盯这两个字段：它把 `LiveSummary` 的**全部**字段
//   拿去问「面板渲不渲染」，不在的必须进 allowlist 并附理由。
//   ⇒ 下一个字段被加进类型又忘了渲染时，门会当场抓。
//
// ⚠ 判据形态：扫 `.vue` 模板，**必须先剥 HTML 注释**
//   （§109.4 与 §119 两次同款——注释里提到字段名会让判据恒绿）。
//   且判据卡的是**结构位置**（`v-for` 的源表达式），不是「词出现过」。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { FRONTEND_ROOT } from './_wire-helpers.mjs'

const PANEL = 'src/features/meetings/MeetingInsightPanel.vue'
const STORE = 'src/features/meetings/meetings-store.ts'

/** 剥 HTML 注释（等长空白替换，保持偏移）。漏了它判据就会读到自己写的注释。 */
function stripHtmlComments(src: string): string {
  return src.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))
}

/** 剥 TS 注释。 */
function stripTsComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .split('\n').map((l) => {
      const i = l.indexOf('//')
      return i < 0 ? l : l.slice(0, i)
    }).join('\n')
}

/** 从 `interface LiveSummary { … }` 里取字段名（带 `?` 的也算）。 */
function liveSummaryFields(): string[] {
  const src = stripTsComments(readFileSync(join(FRONTEND_ROOT, STORE), 'utf8'))
  const i = src.indexOf('export interface LiveSummary {')
  assert.ok(i > 0, `在 ${STORE} 里找不到 LiveSummary 接口`)
  let depth = 0
  let body = ''
  for (let k = src.indexOf('{', i); k < src.length; k++) {
    if (src[k] === '{') depth++
    else if (src[k] === '}') {
      depth--
      if (depth === 0) { body = src.slice(src.indexOf('{', i) + 1, k); break }
    }
  }
  return [...body.matchAll(/^\s{2}([A-Za-z_][A-Za-z0-9_]*)\??:/gm)].map((m) => m[1])
}

/**
 * 面板里**确实**渲染的字段。
 *
 * 取自模板与 script 的**结构位置**：
 *  · `v-for="… in X"` / `v-if="X…"` 里的标识符
 *  · `props.summary?.X`
 * 这样判据量的是「界面上有没有真的用它」，而不是「这个词在文件里出现过」。
 */
function renderedFields(): Set<string> {
  const src = stripHtmlComments(readFileSync(join(FRONTEND_ROOT, PANEL), 'utf8'))
  const out = new Set<string>()
  // props.summary?.xxx —— 结构上就是「从摘要对象里取这个字段」
  for (const m of src.matchAll(/props\.summary\?\.([A-Za-z_][A-Za-z0-9_]*)/g)) out.add(m[1])
  // v-for="(x, i) in summary.xxx" / v-for="x in xxx"
  for (const m of src.matchAll(/v-for="[^"]*\bin\s+(?:summary\??\.)?([A-Za-z_][A-Za-z0-9_]*)/g)) out.add(m[1])
  // v-if="summary?.xxx?.length" 这类判定也算「用了这个字段」
  for (const m of src.matchAll(/summary\?\.([A-Za-z_][A-Za-z0-9_]*)/g)) out.add(m[1])
  // ⚠ 还有一种写法不能漏：模板里可以直接写 `summary.agent !== true`
  //   （**没有** `?.`，因为 Vue 在模板里已解包 props）。
  //   §109 那条降级提示正是这个形态 —— 抽取器只认 `summary?.` 时它整块失明。
  for (const m of src.matchAll(/\bsummary\.([A-Za-z_][A-Za-z0-9_]*)/g)) out.add(m[1])
  return out
}

/** 有意不作为「块」渲染的字段，附理由。 */
const ALLOW: Record<string, string> = {
  summary: '顶部段落经 summaryText（finalSummary || summary?.summary）渲染，不是一个块。',
  updatedAt: '时间戳元数据，界面不展示（要展示也该在别处，不属纪要三段式）。',
}

describe('§124 摘要字段必须真的被渲染', () => {
  it('抽取器有效（量具先自证）', () => {
    const fields = liveSummaryFields()
    // 若抽取器坏了返回空集，下面每条断言都会「零个字段缺失」地通过 —— 那是恒真。
    assert.ok(fields.length >= 5, `LiveSummary 只取到 ${fields.length} 个字段，取值路径可能坏了`)
    assert.ok(fields.includes('decisions'), '抽取器没抓到 decisions —— 它正是本节的主角')
    assert.ok(fields.includes('actionItems'), '抽取器没抓到 actionItems')
  })

  it('LiveSummary 的每个字段都被面板渲染，或已登记理由', () => {
    const fields = liveSummaryFields()
    const used = renderedFields()
    // ⚠ 这里必须是 used.has(f)，不是 `f in used` ——
    //   `in` 只对**对象**有效，对 Set 恒为 false，于是每个字段都被判成「没渲染」。
    //   判据自己红时先怀疑量具（本条就是这样被抓出来的）。
    const missing = fields.filter((f) => !used.has(f) && !ALLOW[f])
    assert.deepEqual(missing, [],
      `这些字段在 LiveSummary 里声明、数据也真到了面板手上，但模板里**没有任何地方渲染它们**：\n` +
      `  ${missing.join('\n  ')}\n` +
      `模型在算、类型在声明、blob 在存，只有用户看不到。\n` +
      `确实有意不展示的，写进 ALLOW 并附理由。`)
  })

  it('决策与待确认问题各有独立的可见判据（不是混在别的块里）', () => {
    const src = stripHtmlComments(readFileSync(join(FRONTEND_ROOT, PANEL), 'utf8'))
    for (const [field, label] of [['decisions', '决策'], ['openQuestions', '待确认问题']] as const) {
      // 结构判据：该字段的 v-for 块里必须带自己的标题文字。
      // ⚠ 卡「字段名出现过」会恒绿 —— 判据要卡**它在带标题的块里被渲染**。
      const re = new RegExp(`<div class="label">${label}</div>[\\s\\S]{0,200}?\\bin\\s+${field}\\b`)
      if (!re.test(src)) {
        assert.fail(`面板里找不到「${label}」块把 ${field} 渲染出来 —— ` +
          `只声明 computed 是不够的，模板里必须有对应的块`)
      }
    }
  })

  it('ALLOW 里的理由不得为空（防止「先跳过，以后再说」）', () => {
    for (const [field, reason] of Object.entries(ALLOW)) {
      assert.ok(reason.trim().length > 0, `ALLOW.${field} 没有写理由`)
    }
    // 反向自证：ALLOW 不得覆盖掉本节的主角，否则门会自己把自己关掉。
    assert.ok(!('decisions' in ALLOW), 'decisions 不得进 ALLOW —— 它是本节要渲染的字段')
    assert.ok(!('openQuestions' in ALLOW), 'openQuestions 不得进 ALLOW')
  })
})
