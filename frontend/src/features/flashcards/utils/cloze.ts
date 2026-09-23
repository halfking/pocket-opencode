/**
 * Cloze parser / renderer (Phase 3 of Anki feature injection).
 *
 * 语法（Anki 兼容）：
 *   {{c1::answer}}           — 挖空，无提示
 *   {{c1::answer::hint}}     — 挖空，带提示
 *   {{c2::another answer}}    — 第二个挖空（顺序无关）
 *
 * 设计动机：
 * - 单 note 多 cloze：每个 c<n> 产生一张独立的 card（FSRS 独立调度）。
 * - 但 Phase 3 简化：1 note = 1 card，前端渲染时把全部挖空展示，1 张复习
 *   卡片完成 N 个挖空的揭露。这避免了 store 层的 expand 逻辑（Phase 3.5 再做）。
 *
 * 安全：
 * - 不接受嵌套 `{{c1::x{{c2::y}}::hint}}`（避免歧义；Anki 也不支持）。
 * - 不接受空 cloze `{{c1::}}`（视为 0 张 cloze，回退纯文本）。
 */

export type ClozeSegment =
    | { type: 'text'; text: string }
    | { type: 'cloze'; index: number; answer: string; hint?: string }

export interface ParsedCloze {
  segments: ClozeSegment[]
  clozeCount: number
  /** c<n> 编号集合（用于复习 UI 提示「几张挖空」）。 */
  indices: number[]
}

/* 形如 {{c1::answer}} 或 {{c1::answer::hint}}。 */
const CLOZE_RE = /\{\{c(\d+)::([^}]*?)(?:::([^}]*?))?\}\}/g

export function parseCloze(input: string): ParsedCloze {
  const segments: ClozeSegment[] = []
  const indices = new Set<number>()
  let clozeCount = 0
  let cursor = 0

  // 用 matchAll 避免全局 lastIndex 副作用（多 review 实例同时解析也安全）
  const matches = Array.from(input.matchAll(CLOZE_RE))
  for (const m of matches) {
    const [whole, idxStr, answer, hint] = m
    const index = Number(idxStr)
    if (!Number.isFinite(index) || index <= 0) continue
    if (answer.length === 0) continue // 跳过空 cloze

    if (m.index! > cursor) {
      segments.push({ type: 'text', text: input.slice(cursor, m.index) })
    }
    segments.push({ type: 'cloze', index, answer, hint })
    indices.add(index)
    clozeCount += 1
    cursor = m.index! + whole.length
  }

  if (cursor < input.length) {
    segments.push({ type: 'text', text: input.slice(cursor) })
  }

  return { segments, clozeCount, indices: [...indices].sort((a, b) => a - b) }
}

export function isClozeText(input: string): boolean {
  // 复用 parseCloze：空 cloze 不算（与 parseCloze 语义一致）。
  // 不直接用 CLOZE_RE.test()，因为 global regex 的 test() 有 lastIndex 状态，
  // 第二次调用会从上次位置开始而漏掉。
  return parseCloze(input).clozeCount > 0
}

/**
 * 渲染 cloze 为前端 segments（用于 Vue 模板里逐段渲染，避免 XSS）。
 * 返回的 segments 顺序与原文一致；cloze 段保留 answer 字段供「显示答案」切换。
 */
export function renderCloze(input: string): ParsedCloze {
  return parseCloze(input)
}

/**
 * 提取用于「在编辑时高亮挖空位置」的提示信息。
 * 返回每个 cloze 的位置区间（offset 相对 input 起点）。
 */
export interface ClozeSpan {
  index: number
  start: number
  end: number
  answer: string
  hint?: string
}

export function locateClozeSpans(input: string): ClozeSpan[] {
  const spans: ClozeSpan[] = []
  for (const m of input.matchAll(CLOZE_RE)) {
    const [whole, idxStr, answer, hint] = m
    const index = Number(idxStr)
    if (!Number.isFinite(index) || index <= 0) continue
    if (answer.length === 0) continue
    spans.push({
      index,
      start: m.index!,
      end: m.index! + whole.length,
      answer,
      hint,
    })
  }
  return spans
}