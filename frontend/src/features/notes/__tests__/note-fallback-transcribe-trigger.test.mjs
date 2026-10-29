// note-fallback-transcribe-trigger.test.mjs
//
// §463 L 组 —— 钉住「兜底全量转写的**触发条件**」这个现状事实。
//
// ── 为什么这道门存在 ──────────────────────────────────────────────────
//
// 随手记落库正文来自哪里，由 `recordingRuntime.ts` 里一个**裸 if** 决定：
//
//     if (cloudAllowed && !this.transcript.value.trim() && audioBlob.size > 0) { … transcribeFull( … }
//
// 实测形态（§463 逐条核过行号）：
//   · 正常路径是 **3 秒定长硬切**（NOTE_CHUNK_MS = 3000）逐片送 transcribeIncremental；
//   · 本分支**只在「一个字都没出」时**才跑；
//   · 而 sendSlice 里只要**任意一片**出过字，this.transcript.value 即非空
//     ⇒ 此后本条件**恒为假** ⇒ 整段音频**从不被用来校对**。
//
// ⇒ 这个 `if` 是随手记**唯一**的「整段校对」机会，且几乎永远为假。
// 决策简报 #15 的 B 格就是为它设的（要不要放宽 = 产品口径，**本门不代拍**）。
//
// 本门守的**不是**「必须这样」，而是「**现在是哪样**」：
// 谁把它改成无条件（= 每条随手记 2x ASR 费用），门会转红并给出正确读法。
//
// ── 量具的四条纪律（全部来自 §462 的假阴性教训）─────────────────────
//
// ① **必须剥注释，且要认全三种引号**（`'` `"` `` ` ``）。
//    否则说明文字里的同形 `if (…)` 会被当成触发条件 ——
//    这正是 §462 K 组第一版报「0 处违规」的同族：**读数不是空，而是看起来像样的错值**。
//    ⚠ 已知简化：模板串 `${ … }` 内嵌的字符串与注释不被单独跟踪。
//      本文件用不到那种形态；若将来出现，这条简化必须先升级再改判据。
//
// ② **必须锚定行首**（防「说明文字当调用」）。
//
// ③ **必须验祖先关系，不能只取「最近的那个 if」。**
//    ⚠ 这是**本轮变异测试实测抓出来的**，不是预防性设计：
//    把守卫整个删掉之后，只按「向上最近的行首 if」扫描会捞到
//    `runStop()` 开头更早的 `if (this.nativeListening)` ——
//    而 `transcribeFull` **并不在** 那个 if 的块里。
//    ⇒ 必须按**花括号跨度**判断包含关系，而不是按距离。
//
// ④ **量具先自证**：第一个用例断言「恰好找到 1 个触发条件且就是已知那个」。
//    找到 0 个 = 量具坏了（不是「没问题」）；找到 ≥2 个 = 形态变了。
//
// 负控与变异实测见文件末尾 —— 两者都**跑过**，否则本门只是装饰。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
// HERE = frontend/src/features/notes/__tests__ → 上溯 3 级 = frontend/src
const SRC = path.resolve(HERE, '..', '..', '..')
const RUNTIME = path.join(SRC, 'native', 'recordingRuntime.ts')

const runtime = fs.readFileSync(RUNTIME, 'utf8')

// ── 量具 ──────────────────────────────────────────────────────────────

/**
 * 把「注释」与「字符串内容」**掩成等长空格**；引号本身与换行保留。
 *
 * 为什么连字符串内容一起掩：后面要做花括号/圆括号配平，
 * 而字符串里出现 `{` `}` `(` `)` 是常事（模板串塞 HTML、CSS、URL）。
 * 只剥注释不做这一步，配平会被字符串带偏 —— 又是一次「看起来像样的错值」。
 *
 * @param {string} src
 * @returns {string} 与 src 等长的字符串，非代码位置为空格
 */
export function maskNonCode(src) {
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
    // 三个字符串态：内容全部掩掉；反斜杠跳过两个字符；只有各自定界符能结束
    if (c === '\\') { out[i] = ' '; if (i + 1 < n && src[i + 1] !== '\n') out[i + 1] = ' '; i += 2; continue }
    const ends = (state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tq' && c === '`')
    if (ends) { state = 'code'; i++; continue }   // 定界符本身保留
    if (c !== '\n') out[i] = ' '
    i++
  }
  return out.join('')
}

/**
 * 取 `if ( … )` 的条件文本与右括号后的偏移。
 * @returns {{condition: string, afterParen: number}|null}
 */
export function readIfCondition(code, from) {
  const open = code.indexOf('(', from)
  if (open < 0) return null
  let depth = 0
  for (let i = open; i < code.length; i++) {
    if (code[i] === '(') depth++
    else if (code[i] === ')') {
      depth--
      if (depth === 0) return { condition: code.slice(open + 1, i), afterParen: i + 1 }
    }
  }
  return null
}

/**
 * 从 `from` 起找该语句的**块跨度**：第一个不在括号内的 `{`，配平到它的 `}`。
 * @returns {{open: number, close: number}|null}
 */
export function statementBlockSpan(code, from) {
  let paren = 0
  let open = -1
  for (let i = from; i < code.length; i++) {
    const c = code[i]
    if (c === '(' || c === '[') { paren++; continue }
    if (c === ')' || c === ']') { paren--; continue }
    if (paren !== 0) continue
    if (c === '{') { open = i; break }
    // 单行 if-body（`if (x) return`）没有块 —— 对本门而言即「不是候选」
    if (c === '\n') return null
  }
  if (open < 0) return null
  let depth = 0
  for (let i = open; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') {
      depth--
      if (depth === 0) return { open, close: i }
    }
  }
  return null
}

/**
 * 找出**真正包住** `transcribeFull(` 调用的那个行首 `if` 的条件。
 *
 * @returns {{found: boolean, condition: string|null, line: number, reason?: string}}
 */
export function fallbackGuardCondition(src) {
  const code = maskNonCode(src)
  const callAt = code.indexOf('sttSettingsApi.transcribeFull(')
  if (callAt < 0) return { found: false, condition: null, line: -1, reason: '找不到 transcribeFull 调用点' }

  const lineStarts = [0]
  for (let i = 0; i < code.length; i++) if (code[i] === '\n') lineStarts.push(i + 1)
  const lineOf = (off) => {
    let lo = 0
    let hi = lineStarts.length - 1
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1
      if (lineStarts[mid] <= off) lo = mid
      else hi = mid - 1
    }
    return lo
  }

  for (let ln = lineOf(callAt); ln >= 0; ln--) {
    const start = lineStarts[ln]
    const nl = code.indexOf('\n', start)
    const lineText = code.slice(start, nl < 0 ? code.length : nl)
    const trimmed = lineText.trimStart()
    // ⚠ 锚定行首必须**先对整行 trimStart 再比对**；
    //   写成 `code.slice(start, start + 2).trimStart()` 是错的 ——
    //   那 2 个字符本身就是缩进，trim 后变成空串，于是**没有任何一行能匹配**，
    //   读数是「找不到守卫」，看起来像代码变了，其实量具从一开始就没工作过。
    if (!trimmed.startsWith('if')) continue   // 纪律②：锚定行首
    const head = start + (lineText.length - trimmed.length)
    const cond = readIfCondition(code, head)
    if (!cond) continue
    const span = statementBlockSpan(code, cond.afterParen)
    if (!span) continue                                                       // 单行 if-body 不是候选
    // ★ 纪律③：必须**包含**调用点，不能只看距离
    if (span.open <= callAt && callAt <= span.close) {
      return { found: true, condition: cond.condition, line: ln + 1 }
    }
  }
  return { found: false, condition: null, line: -1, reason: '没有任何行首 if 的块真正包住该调用 ⇒ 兜底转写已变成无条件执行' }
}

/** 判据本体：条件必须仍然依赖「切片文本为空」这个量。 */
function assertGuardedByEmptySliceText(condition, where) {
  assert.match(
    condition,
    /!\s*this\.transcript\.value\s*\.\s*trim\s*\(\s*\)/,
    `${where}：兜底转写的触发条件不再依赖「切片文本为空」——\n` +
      '  这意味着随手记会在切片已出字时**也**跑整段重转（每条 2x ASR 费用），\n' +
      '  或者切片文本永远非空、整段重转成为死代码。\n' +
      '  ⚠ 两种都是行为变更：若是产品决定，请先更新决策简报 #15 的 B 格再改这里。',
  )
  assert.match(
    condition,
    /audioBlob\.size/,
    `${where}：条件里没有 audioBlob.size —— 空音频也会触发一次最长 10 分钟的兜底转写。`,
  )
}

describe('L 组 · 随手记兜底全量转写的触发条件（§463）', () => {
  // ── 纪律④：量具先自证 ────────────────────────────────────────────
  it('★ 量具自证：恰好找到 1 个真正包住调用的行首 if，且就是已知那个', () => {
    const g = fallbackGuardCondition(runtime)
    assert.ok(
      g.found,
      `量具失效，找不到触发条件（${g.reason}）。\n` +
        '  ⚠ 这**不是**「没问题」——按 §462，找到 0 个时必须先怀疑量具，再怀疑代码。',
    )
    assert.equal(
      g.condition.replace(/\s+/g, ' ').trim(),
      'cloudAllowed && !this.transcript.value.trim() && audioBlob.size > 0',
      `触发条件变了：${g.condition}\n` +
        '  若这是有意的行为变更 ⇒ 先更新决策简报 #15 的 B 格，再回来改这条登记。\n' +
        '  ⚠ 2026-10-29：条件前多了 `cloudAllowed &&` —— 兜底全量转写是**云端**调用，\n' +
        '    账号切换或云端权限被撤销后不该再发起。这是有意的行为变更（会议/录音租约切片）。',
    )
  })

  // ── 正控：现状必须绿 ───────────────────────────────────────────────
  it('现状：兜底转写只在「切片一个字都没出」时才跑', () => {
    const g = fallbackGuardCondition(runtime)
    assert.ok(g.found, `量具失效：${g.reason}`)
    assertGuardedByEmptySliceText(g.condition, '现状')
  })

  it('现状：触发条件处留有 §463 的就地更正注释（防解释被静默删掉）', () => {
    // ⚠ 这**不是**断言「注释说的 == 代码是」（那种注释会腐烂）。
    //   真行为由上一条钉住；这里只防「行为没变、解释被删」，
    //   否则下一个读代码的人会重新得出「随手记是整段一次性转写」那个已被证伪的结论。
    assert.match(runtime, /§463/, 'recordingRuntime.ts 里 §463 的就地更正注释不见了')
  })

  // ── 变异实测 ───────────────────────────────────────────────────────
  it('变异：放宽成「只要有音频就跑」⇒ 判据必须转红', () => {
    const mutated = runtime.replace(
      /if \(cloudAllowed && !this\.transcript\.value\.trim\(\) && audioBlob\.size > 0\) \{/,
      'if (audioBlob.size > 0) {',
    )
    assert.notEqual(mutated, runtime, '变异没命中（源码形态已变，先确认负控样本还成立）')
    const g = fallbackGuardCondition(mutated)
    assert.ok(g.found, `变异后应仍有一个包住调用的 if，却报找不到：${g.reason}`)
    assert.throws(
      () => assertGuardedByEmptySliceText(g.condition, '放宽样本'),
      /不再依赖「切片文本为空」/,
      '放宽触发条件后判据**没有**转红 ⇒ 它是恒真的，删掉重写',
    )
  })

  it('变异：换成恒真 if (true) ⇒ 判据必须转红', () => {
    const mutated = runtime.replace(
      /if \(cloudAllowed && !this\.transcript\.value\.trim\(\) && audioBlob\.size > 0\) \{/,
      'if (true) {',
    )
    assert.notEqual(mutated, runtime, '变异没命中')
    const g = fallbackGuardCondition(mutated)
    assert.ok(g.found, `量具失效：${g.reason}`)
    assert.throws(
      () => assertGuardedByEmptySliceText(g.condition, '恒真样本'),
      /不再依赖「切片文本为空」/,
      '条件换成恒真后判据没有转红 ⇒ 它对条件内容不敏感',
    )
  })

  it('★ 变异：整个守卫被删掉 ⇒ 判据必须报「找不到条件」，且不得被更早的 if 顶包', () => {
    // 这条是**纪律③ 的实测依据**：runStop() 开头更早就有
    // `if (this.nativeListening) { … }`，而 transcribeFull **不在** 那个块里。
    // 只按「向上最近的行首 if」扫描的实现会在这里捞错对象并误判为「有守卫」。
    const mutated = runtime.replace(
      /if \(cloudAllowed && !this\.transcript\.value\.trim\(\) && audioBlob\.size > 0\) \{/,
      '// 守卫被删掉了（模拟一次「优化」：直接重转，别管切片有没有出字）',
    )
    assert.notEqual(mutated, runtime, '变异没命中')
    const g = fallbackGuardCondition(mutated)
    assert.equal(
      g.found, false,
      '守卫已删却仍报告「找到了触发条件」⇒ 抽取器把更早的 if 误当成了祖先（跨度判定失效）',
    )
    assert.match(g.reason || '', /无条件执行/, `「找不到」时必须说明原因，实际：${g.reason}`)
  })

  it('变异：把条件改挂到时长上 ⇒ 判据必须转红', () => {
    const mutated = runtime.replace(
      /if \(cloudAllowed && !this\.transcript\.value\.trim\(\) && audioBlob\.size > 0\) \{/,
      'if (this.elapsedMs.value > 30000) {',
    )
    assert.notEqual(mutated, runtime, '变异没命中')
    const g = fallbackGuardCondition(mutated)
    assert.ok(g.found, `量具失效：${g.reason}`)
    assert.throws(
      () => assertGuardedByEmptySliceText(g.condition, '时长触发样本'),
      /不再依赖「切片文本为空」/,
      '条件换成时长判据后判据没有转红 ⇒ 它对条件内容不敏感',
    )
  })

  // ── 负控：证明剥注释与行首锚定真的在起作用（否则门可能整体静音）──
  it('★ 负控：注释里出现同形 if 行，判据必须仍然只认那一个真触发条件', () => {
    const decoy = `
      // if (cloudAllowed && !this.transcript.value.trim() && audioBlob.size > 0) {
      //   说明文字里的同形条件，不是触发条件
      // }
      /*
      if (cloudAllowed && !this.transcript.value.trim() && audioBlob.size > 0) {
      */
    `
    const injected = runtime.replace('private async runStop()', `private async runStop()\n${decoy}`)
    assert.notEqual(injected, runtime, '负控样本没插进去')
    const g = fallbackGuardCondition(injected)
    assert.ok(g.found, `负控下量具失效：${g.reason}`)
    assert.equal(
      g.condition.replace(/\s+/g, ' ').trim(),
      'cloudAllowed && !this.transcript.value.trim() && audioBlob.size > 0',
      '注释里的同形条件被当成了触发条件 ⇒ 剥注释没生效（§462 同族）',
    )
  })

  it('★ 负控：块注释里的同形条件不得被当成触发条件', () => {
    const injected = runtime.replace(
      'private async runStop()',
      'private async runStop() /* 前提：if (!this.transcript.value.trim() && audioBlob.size > 0) 时才有本分支 */',
    )
    assert.notEqual(injected, runtime, '负控样本没插进去')
    const g = fallbackGuardCondition(injected)
    assert.ok(g.found, `负控下量具失效：${g.reason}`)
    assert.equal(
      g.condition.replace(/\s+/g, ' ').trim(),
      'cloudAllowed && !this.transcript.value.trim() && audioBlob.size > 0',
      '块注释里的同形条件被当成了触发条件 ⇒ 块注释没剥干净',
    )
  })

  it('★ 负控：字符串里的花括号不得带偏配平（§462 记过的同族坑）', () => {
    // 在守卫与调用点之间塞一段含 `{ } ( )` 的字符串；
    // 若配平被带偏，要么找不到守卫、要么找到错的祖先。
    const injected = runtime.replace(
      'private async runStop()',
      `private async runStop() { const noise = \`{ unbalanced ( brace } body { text \`; void noise`,
    )
    assert.notEqual(injected, runtime, '负控样本没插进去')
    const g = fallbackGuardCondition(injected)
    assert.ok(g.found, `负控下量具失效：${g.reason}`)
    assert.equal(
      g.condition.replace(/\s+/g, ' ').trim(),
      'cloudAllowed && !this.transcript.value.trim() && audioBlob.size > 0',
      '字符串里的花括号把配平带偏了 ⇒ 掩码没有覆盖字符串内容',
    )
  })
})
