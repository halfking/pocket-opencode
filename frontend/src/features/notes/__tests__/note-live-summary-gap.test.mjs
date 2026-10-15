// note-live-summary-gap.test.mjs —— §464
//
// 锁住「随手记链上没有录音中即时总结、没有 references、没有 recommendations」
// 这三件事的**现状**，并写清每条判据**转红时该怎么读**。
//
// ── 为什么这不是「补一个功能」而是一道门 ─────────────────────────────
//
// 需求原文：「**在录音时**需要进行即时总结……并能**在总结同时，给出一些参考的资料与建议**」。
// §454 曾就这半句给出一条阴性：「已实现了，别再查了」——
// **那条结论在它核到的范围内完全正确，但没写范围**（只对会议/会话链成立）。
// 而 §454 的阴性在文档层面是**全局**的 ⇒ 被 §463 那类跨链清点直接套到了随手记上。
//
// ⇒ 本门的作用不是「阻止有人加功能」，而是：
//   **让「随手记没有这些」这件事随时可被机器复核，而不是靠翻文档。**
//
// ⚠ 判据转红时的正确读法（与 note-voice-zero-correction 同一手法）：
//   **不是「代码坏了」**，而是「随手记开始接这条链了 ⇒
//   决策简报 #26 该撤或改写，§464 该更新」。
//   ⚠ 反过来也成立：若你要改它，**先改简报 #26**，别先改代码——
//   否则会出现「代码已加、决策仍说没做」的状态，而 §454 那种错正是这样长出来的.
//
// ── 三条纪律 ───────────────────────────────────────────────────────
//
// ① **必须剥注释，且要认全三种引号**（§463.8）：否则说明文字里的
//    `useLiveSummary(` / `references` 会被当成实现，读数就成了假阳性。
// ② **判据必须认「调用」而不是「提到」**：只 import 不调用不算接上
//    （同 note-voice-zero-correction 的做法：找 `useLiveSummary(` 这个调用形态）。
// ③ **量具先自证**：对照组（会议/会话链）必须**能**被同一个抽取器抽到，
//    否则「随手记 0 处」可能是量具坏了，而不是真的没有。
//
// ── 为什么第 ③ 条尤其重要 ──────────────────────────────────────────
//
// 本门最容易的失败形态是「0 处」——它**看起来就是结论**。
// 而 §463 已经证过一次：空读数可能自带一个听起来很合理的解释。
// ⇒ 本门显式断言**对照组非空**，把「随手记 0 处」变成
// 「对照组 2 处、随手记 0 处」这样一个**有对照的读数**。

import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')          // frontend/src
const NOTES = path.join(SRC, 'features', 'notes')
const MEETINGS = path.join(SRC, 'features', 'meetings')
const SESSIONS = path.join(SRC, 'features', 'sessions')
const COMPOSABLES = path.join(SRC, 'composables')
/** 判据自身文件名 —— 豁免**只有**这一个。 */
const SELF = 'note-live-summary-gap.test.mjs'
const BACKEND = path.resolve(SRC, '..', '..', 'backend', 'internal', 'server')

/** 掩掉注释**正文**与字符串**内容**（引号本身与换行保留）。
 *
 * ⚠⚠ 2026-10-11 当场踩过：**只处理 `//` 与 `/*` 的定界符、不抹正文，也是「看起来对」的写法**——
 *   状态机会正确地跳过注释里的引号，但注释文字**仍留在输出里**，
 *   于是 `// ... useLiveSummary( ... )` 被算成真调用。
 *   而这条负控是**唯一**能抓到它的地方：现状断言在这种情况下只会**偏多**，
 *   而随手记侧的真实读数本来就是 0 ⇒ 偏多的那一侧没有对照，**抓不到**。
 *   ⇒ 「注释被剥掉」必须有一条**只由注释构成**的样本单独验。
 *
 * ⛔ 同族的第二个坑：字符串内容里的键名/调用名也要抹，
 *   否则 `{ 'useLiveSummary(x)' }` 这类字面量会喂绿。
 */
function maskNonCode(src) {
  const out = src.split('')
  const n = src.length
  let state = 'code'
  let i = 0
  while (i < n) {
    const c = src[i]
    const c2 = src[i + 1]
    if (state === 'code') {
      if (c === '/' && c2 === '/') { out[i] = ' '; out[i + 1] = ' '; state = 'line'; i += 2; continue }
      if (c === '/' && c2 === '*') { out[i] = ' '; out[i + 1] = ' '; state = 'block'; i += 2; continue }
      if (c === "'") { state = 'sq'; i++; continue }
      if (c === '"') { state = 'dq'; i++; continue }
      if (c === '`') { state = 'tq'; i++; continue }
      i++; continue
    }
    if (state === 'line') {
      if (c === '\n') { state = 'code'; i++; continue }
      out[i] = ' '; i++; continue
    }
    if (state === 'block') {
      if (c === '*' && c2 === '/') { out[i] = ' '; out[i + 1] = ' '; state = 'code'; i += 2; continue }
      if (c !== '\n') out[i] = ' '
      i++; continue
    }
    // 三个字符串态：内容全部抹掉；反斜杠跳过两个字符；只有各自定界符能结束
    if (c === '\\') { out[i] = ' '; if (i + 1 < n && src[i + 1] !== '\n') out[i + 1] = ' '; i += 2; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tq' && c === '`')) { state = 'code'; i++; continue }
    if (c !== '\n') out[i] = ' '
    i++
  }
  return out.join('')
}

/** 递归读一个目录下的文本文件；跳过本判据自身。 */
function readTree(dir) {
  const out = new Map()
  const walk = (d) => {
    for (const name of readdirSync(d)) {
      try {
        const p = path.join(d, name)
        const st = statSync(p)
        if (st.isDirectory()) { walk(p); continue }
        if (!/\.(ts|vue|mjs)$/.test(name)) continue
        if (name === SELF) continue
        out.set(path.relative(dir, p), readFileSync(p, 'utf8'))
      } catch (err) {
        // §632：并发下别的用例会往 `frontend/src/` 下**临时建目录再删掉**
        // （z-index-ladder 的 `__probe_fixture__`）。递归撞上那个窗口会抛
        // ENOENT ⇒ **整条门假红**。只吞 ENOENT，其余照抛。
        if (err?.code !== 'ENOENT') throw err
      }
    }
  }
  if (dir && statSync(dir).isDirectory()) walk(dir)
  return out
}

/** 在**剥过注释**的源码里找「真的调用形态」，而不是「提到这个词」。
 *
 * ⚠ `name` 必须是**裸名**（不带括号）：传 `foo(` 会让 `indexOf` 把括号一起吃掉，
 *  再去括号后面找 `(` 就永远找不到 —— 而读数是「0 处调用」，
 *  **看起来正好就是「没接上」这个结论**（§463.8 的又一次同族）。
 *  这条 2026-10-11 当场踩过：对照组一度读成 0 处。
 */
function callsOf(src, name) {
  const code = maskNonCode(src)
  const hits = []
  let i = 0
  while (i < code.length) {
    const at = code.indexOf(name, i)
    if (at < 0) break
    const after = code.slice(at + name.length, at + name.length + 2).trimStart()
    if (after.startsWith('(')) hits.push(at)
    i = at + name.length
  }
  return hits
}

/** 在**剥过注释**的源码里找「代码中提到过这个标识符」。
 *
 * ⚠ 与 `callsOf` 的区别：`recommendations` 是**变量名**，不是被调用者，
 *  拿调用形态去数它会恒为 0 —— 而 0 正是「随手记没有建议」这个结论本身。
 */
function mentionsOf(src, name) {
  const code = maskNonCode(src)
  const hits = []
  let i = 0
  while (i < code.length) {
    const at = code.indexOf(name, i)
    if (at < 0) break
    hits.push(at)
    i = at + name.length
  }
  return hits
}

const notesTree = readTree(NOTES)
const meetingsTree = readTree(MEETINGS)
const sessionsTree = readTree(SESSIONS)

/** 判据要查的三件事（写成数组，注释里不重复字面量）。 */
const LIVE_CALL = 'useLiveSummary'
const REF_KEY = 'references'
const REC_KEY = 'recommendations'

/** 后端随手记总结的响应体键（从真 handler 里取，不手抄）。 */
function noteSummarizeResponseKeys() {
  const f = readFileSync(path.join(BACKEND, 'server_assistant.go'), 'utf8')
  const at = f.indexOf('func (s *Server) handleNoteSummarize')
  assert.ok(at >= 0, '找不到 handleNoteSummarize —— 量具失效（handler 改名/移动？）')
  // 取该函数体内第一处 writeJSON 的 map 字面量
  const wj = f.indexOf('writeJSON(', at)
  assert.ok(wj > at, 'handleNoteSummarize 里没有 writeJSON —— 量具失效')
  const open = f.indexOf('map[string]any{', wj)
  assert.ok(open > wj && open < wj + 400, '找不到响应 map —— 量具失效')
  let depth = 0
  for (let i = open; i < f.length; i++) {
    if (f[i] === '{') depth++
    else if (f[i] === '}') {
      depth--
      if (depth === 0) {
        const body = f.slice(open, i + 1)
        return [...body.matchAll(/"([a-z_]+)"\s*:/g)].map((m) => m[1])
      }
    }
  }
  throw new Error('响应 map 的花括号不配平 —— 量具失效')
}

describe('§464 · 随手记链上没有录音中即时总结 / references / recommendations', () => {
  // ── 纪律③：量具先自证（对照组必须非空）──────────────────────────
  it('★ 量具自证：同一个抽取器在会议/会话链上**抽得到** useLiveSummary 的调用', () => {
    let hits = 0
    for (const [, s] of meetingsTree) hits += callsOf(s, LIVE_CALL).length
    for (const [, s] of sessionsTree) hits += callsOf(s, LIVE_CALL).length
    assert.ok(
      hits >= 2,
      `对照组只抽到 ${hits} 处 ⇒ 抽取器坏了，「随手记 0 处」这个读数**不可信**（§463.8）`,
    )
  })

  it('★ 量具自证：后端随手记 summarize 的响应键能被抽出来且非空', () => {
    const keys = noteSummarizeResponseKeys()
    assert.ok(keys.length >= 4, `响应键只抽到 ${keys.length} 个 ⇒ 量具坏了`)
    assert.ok(keys.includes('summary'), `响应键里没有 summary，实际：${keys.join(', ')}`)
    assert.ok(keys.includes('action_items'), `响应键里没有 action_items，实际：${keys.join(', ')}`)
  })

  // ── 现状三条 ──────────────────────────────────────────────────────
  it('现状①：随手记模块零处**调用** useLiveSummary（没有录音中即时总结）', () => {
    const hits = []
    for (const [rel, src] of notesTree) {
      for (const at of callsOf(src, LIVE_CALL)) hits.push(rel)
    }
    assert.deepEqual(
      hits, [],
      '随手记开始调用 useLiveSummary 了 ⇒ 决策简报 #26 的前提已变：\n  ' + hits.join('\n  '),
    )
  })

  it('现状②：后端随手记 summarize 的响应体里没有 references', () => {
    const keys = noteSummarizeResponseKeys()
    assert.ok(
      !keys.includes(REF_KEY),
      `handleNoteSummarize 现在返回 ${REF_KEY} 了 ⇒ 简报 #26 / §454 的适用范围都要更新`,
    )
  })

  it('现状③：随手记模块零处消费 recommendations', () => {
    const hits = []
    for (const [rel, src] of notesTree) {
      // ⚠ 这里必须是 `mentionsOf` 而不是 `callsOf`：`recommendations` 是**变量名**，
      //   拿「调用形态」去数它恒为 0 —— 而 0 正是本条断言的结论本身。
      //   这与上面 test 3 的 paren bug 是同一族，只是被各自的负控抓到的时间不同。
      for (const at of mentionsOf(src, REC_KEY)) hits.push(rel)
    }
    assert.deepEqual(
      hits, [],
      '随手记开始消费 recommendations 了 ⇒ 简报 #26 的前提已变：\n  ' + hits.join('\n  '),
    )
  })

  // ── 负控：证明剥注释在起作用，否则门可能整体静音 ──────────────────
  it('★ 负控：说明文字里的同形内容不得被当成调用', () => {
    // 只在注释里写一个调用形态 + 只写一个裸键。
    // 若判据不剥注释，两条现状断言都会被喂绿 ⇒ 这道门就只是装饰。
    const code = maskNonCode(`
      // const x = useLiveSummary(meetingId, segments)
      // const y = { references: [], recommendations: [] }
      /* useLiveSummary(1) */
      /* "references": 1 */
    `)
    assert.equal(callsOf(code, LIVE_CALL).length, 0, '注释里的调用形态被算成了真调用')
    assert.equal(callsOf(code, REF_KEY).length, 0, '注释里的裸键被算成了消费')
  })

  it('★ 负控：只 import 不调用不算「接上了」', () => {
    const code = maskNonCode(
      "import { useLiveSummary } from '../../composables/useLiveSummary'\nconst f = typeof useLiveSummary\n",
    )
    assert.equal(
      callsOf(code, LIVE_CALL).length, 0,
      '只 import / 只取函数值就被算成调用 ⇒ 判据把「提到」当成了「调用」（§462 同族）',
    )
  })

  // ── 正控：对照组在同一个判据下必须是「有」 ────────────────────────
  it('★ 正控：会议链确实有 recommendations 的消费面（否则上面三条是恒真的）', () => {
    let hits = 0
    for (const [, s] of meetingsTree) hits += mentionsOf(s, REC_KEY).length
    assert.ok(hits > 0, '会议链也抽不到 recommendations ⇒ 对照失效，本门会恒绿')
  })
})