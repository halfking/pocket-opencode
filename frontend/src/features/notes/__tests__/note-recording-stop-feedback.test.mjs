// note-recording-stop-feedback.test.mjs
//
// 用户报的「笔记录音点击停止没反应」在代码里的确切形态。
//
// 状态机：recordingRuntime.runStop() 在**第一行**就把 recording 置 false
// （麦克风立刻释放、计时器立刻停），然后同步等一段**最长 10 分钟**的兜底
// 转写（transcribeFull：客户端超时 11 分钟 / 外层 withTimeout 10 分钟）。
// 这段窗口里：
//
//   recording === false              → FAB 立刻变回「开始录音」的空闲外观
//   v-if="isRecording"               → NoteRecordingStudio 被卸载，实时文本消失
//   error 仍是空的                    → 它只在**失败**时才写
//   再点 FAB → toggle() 见 phase==='stopping' 直接返回进行中的 promise
//
// 也就是说：最长 10 分钟的**零反馈**，而且再点毫无变化。
// 这道测试钉住「收尾窗口必须被界面显式表达」这条契约。

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

import { noteRecorderUiState, nextRecordingState } from '../note-recording.ts'

const HERE = dirname(fileURLToPath(import.meta.url))

describe('noteRecorderUiState —— 收尾窗口必须可见', () => {
  it('stopping：必须表达为忙碌态，并给出文案', () => {
    const s = noteRecorderUiState('stopping')
    assert.equal(s.busy, true, '停止后收尾期间 FAB 必须显示为忙碌/不可点')
    assert.ok(s.statusText.length > 0, '必须有一句「正在转写…」的文案')
    assert.match(s.ariaLabel, /转写|稍候/)
  })

  it('recording：可点、标签是「停止录音」、不显示状态行', () => {
    const s = noteRecorderUiState('recording')
    assert.equal(s.busy, false)
    assert.equal(s.ariaLabel, '停止录音')
    assert.equal(s.statusText, '')
  })

  it('idle：可点、标签是「开始录音」、不显示状态行', () => {
    const s = noteRecorderUiState('idle')
    assert.equal(s.busy, false)
    assert.equal(s.ariaLabel, '开始录音')
    assert.equal(s.statusText, '')
  })

  it('忙碌态只在 stopping 出现（不得污染录音中/空闲）', () => {
    for (const p of ['idle', 'recording']) {
      assert.equal(noteRecorderUiState(p).busy, false, `${p} 不该是忙碌态`)
    }
  })
})

describe('状态机：recording → stopping → idle', () => {
  it('三态闭合，收尾窗口一定被经过', () => {
    const afterToggle = nextRecordingState('idle', 'toggle')
    assert.equal(afterToggle, 'recording')
    const afterStop = nextRecordingState(afterToggle, 'toggle')
    assert.equal(afterStop, 'stopping')
    assert.equal(noteRecorderUiState(afterStop).busy, true)
    assert.equal(nextRecordingState(afterStop, 'drafted'), 'idle')
  })
})

describe('接线：UI 必须真的用上这个状态（结构断言）', () => {
  const widget = readFileSync(join(HERE, '..', 'VoiceRecorderWidget.vue'), 'utf8')
  const list = readFileSync(join(HERE, '..', 'NoteListView.vue'), 'utf8')

  it('FAB 组件提供 busy 形态，而不是只在录音中才变化', () => {
    assert.match(widget, /busy\?:\s*boolean/, 'VoiceRecorderWidget 缺少 busy prop')
    assert.match(widget, /:disabled="busy"/, 'busy 时必须真的禁用按钮')
    assert.match(
      widget,
      /recording,\s*busy/,
      'FAB 的 class 必须同时反映 recording 与 busy——只有 recording 的话，' +
        '点停止后它会立刻退回空闲外观，正是「点了没反应」的观感来源',
    )
  })

  it('列表页渲染「正在转写」状态行', () => {
    assert.match(list, /noteRecorderUiState/, 'NoteListView 没有使用 noteRecorderUiState')
    assert.match(list, /recorderUi\.statusText/, 'NoteListView 没有渲染状态文案')
    assert.match(list, /:busy="recorderUi\.busy"/, 'NoteListView 没有把 busy 传给 FAB')
  })

  it('**对照组**：状态行不能盖住错误行', () => {
    // 没有这条，上面那条可能在错误分支被删掉时照样绿。
    // 两者必须并存：收尾中显示「正在转写」，失败时显示真实原因。
    const errIdx = list.indexOf('v-else-if="recError"')
    const busyIdx = list.indexOf('v-else-if="recorderUi.statusText"')
    assert.ok(errIdx > 0, '错误分支不见了')
    assert.ok(busyIdx > 0, '状态分支不见了')
    assert.ok(errIdx < busyIdx, '错误分支必须排在状态分支前面（v-else-if 是排他的）')
  })
})

// ---------------------------------------------------------------------------
// §572：phase 复位不只要「在 finally 里」，还必须**罩得住承诺声称的全部范围**
// ---------------------------------------------------------------------------
//
// 承诺原文（recordingRuntime.ts 的 runStop 文档块）：
//
//   「收尾主体。**必须**在 finally 里把 phase 拨回 'idle'：phase 一旦卡在
//    'stopping',decideNoteStart 会拒绝一切新录音、toggle() 也直接返回 null
//    —— 录音 FAB 就永久失灵」
//
// ⛔ 变异 M26-B 实测（把复位从 finally 挪进函数体的 return 之前）：
//    **全量 45 项门禁全通过，EXIT=0** ⇒ 这条承诺**零强制**。
//
// ⭐ 而零强制有一个非平凡的根因，见下面负控②：
//    **照承诺字面写的判据（"复位出现在某个 finally 里"）今天是绿的。**
//    它只能防退化，不能表达今天真实存在的缺口 —— 而缺口是：**窄 finally**：
//      · phase 在 runStop 开头就被拨成 'stopping'；
//      · 而那个带 finally 的 try **晚了 17 行才开**；
//      · ⇒ 两者之间的语句一旦抛出，复位根本不执行，承诺却写着「必须」。
//    所以判据钉的必须是**范围**（try 起点 ≤ phase 翻转），不是**存在性**。

/** 把注释掩成等长空格，**保留**字符串内容（用来搜 'recording' 这种字面量）。 */
function stripComments(src) {
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
      i++; continue
    }
    if (state === 'line') { if (c === '\n') { state = 'code'; i++; continue } out[i] = ' '; i++; continue }
    if (c === '*' && c2 === '/') { out[i] = ' '; out[i + 1] = ' '; state = 'code'; i += 2; continue }
    if (c !== '\n') out[i] = ' '
    i++
  }
  return out.join('')
}

/**
 * 把注释**与字符串内容**都掩成等长空格（用来做花括号配平）。
 *
 * ⚠ 为什么必须是两个掩码：`'recording'` 是合法代码，掩掉就搜不到；
 *   而模板串里的 `${…}` 含花括号，不掩掉就会把配平带偏（§462/§569 同族坑）。
 *   两者都保持**等长**，所以偏移量可以互相换算。
 *
 * ⚠ 本文件不 import `note-fallback-transcribe-trigger.test.mjs` 里那份同款实现：
 *   跨测试文件 import 会把对方的 describe/it 注册进本 suite（跑两遍）。
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
    if (state === 'line') { if (c === '\n') { state = 'code'; i++; continue } out[i] = ' '; i++; continue }
    if (state === 'block') {
      if (c === '*' && c2 === '/') { out[i] = ' '; out[i + 1] = ' '; state = 'code'; i += 2; continue }
      if (c !== '\n') out[i] = ' '
      i++; continue
    }
    if (c === '\\') { out[i] = ' '; if (i + 1 < n && src[i + 1] !== '\n') out[i + 1] = ' '; i += 2; continue }
    const ends = (state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tq' && c === '`')
    if (ends) { state = 'code'; i++; continue }
    if (c !== '\n') out[i] = ' '
    i++
  }
  return out.join('')
}

/** 从 `{` 起做花括号配平，返回匹配的 `}` 的下标（失败返回 -1）。 */
function matchBrace(code, open) {
  let depth = 0
  for (let i = open; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') { depth--; if (depth === 0) return i }
  }
  return -1
}

/**
 * 从 `finally {` 的 `{` 反向找与它配对的 `try {` 的 `{`。
 *
 * ⚠ 反向配平的正确写法是「**先减到 0 再返回**」，不是「在 depth==0 时返回」：
 *   起点就在 try 块**内部**，所以第一个遇到的 `{` 未必是 try 的 `{`
 *   （块内可能还有嵌套 `{}`）。先 `}` 后 `{` 逐对抵消，**把 depth 减到 0 的那个
 *   `{` 才是配对项**。
 *   ⛔ 本函数第一版写成 `if (depth === 0) return i`，在窄 finally 片段上返回了
 *      `runStop` 自己的 `{` ⇒ 判据**恒绿**。只有负控②（窄 finally 片段）能抓住它 ——
 *      负控① 抓的是另一半（finally 里还有没有复位），两者互不覆盖。
 * @returns {number} try 块的 `{` 下标；找不到返回 -1
 */
function matchTryBackwards(code, finallyOpen) {
  let depth = 0
  for (let i = finallyOpen - 1; i >= 0; i--) {
    if (code[i] === '}') depth++
    else if (code[i] === '{') {
      depth--
      if (depth === 0) return i
    }
  }
  return -1
}

/**
 * 判据本体：runStop 里 phase 复位是否被 finally **罩住**。
 *
 * @returns {{ok: boolean, reason?: string, tryLine?: number, flipLine?: number, finallyLine?: number}}
 */
function phaseResetCoverage(src) {
  const code = maskNonCode(src)
  const lit = stripComments(src)
  const lineOf = (off) => src.slice(0, off).split('\n').length

  const rsAt = code.indexOf('private async runStop(')
  if (rsAt < 0) return { ok: false, reason: '找不到 private async runStop(（量具失效）' }
  const rsOpen = code.indexOf('{', rsAt)
  const rsClose = matchBrace(code, rsOpen)
  if (rsOpen < 0 || rsClose < 0) return { ok: false, reason: 'runStop() 花括号配平失败，判据读数不可信' }

  // ① 进入 'stopping' 的那次翻转：必须恰好一处，否则「范围」无从谈起
  const flips = [...lit.matchAll(/this\.phase\.value\s*=\s*nextRecordingState\(\s*'recording'\s*,\s*'toggle'\s*\)/g)]
    .map((m) => m.index)
    .filter((i) => i > rsOpen && i < rsClose)
  if (flips.length !== 1) {
    return { ok: false, reason: `runStop() 内「拨成 stopping」的赋值找到 ${flips.length} 处（预期恰好 1）⇒ 形态变了，先确认判据` }
  }
  const flipAt = flips[0]

  // ② 带 phase 复位的那个 finally：runStop 内必须恰好一个
  const fins = [...code.matchAll(/finally\s*\{/g)]
    .map((m) => code.indexOf('{', m.index))
    .filter((i) => i > rsOpen && i < rsClose)
    .filter((open) => lit.slice(open, matchBrace(code, open) + 1).includes('this.phase.value'))
  if (fins.length !== 1) {
    return { ok: false, reason: `runStop() 内「finally 里做 phase 复位」的 finally 找到 ${fins.length} 个（预期恰好 1）⇒ 形态变了，先确认判据` }
  }
  const finOpen = fins[0]

  // ③ ★ 范围：承载 finally 的 try 必须**在翻转之前**就开
  const tryOpen = matchTryBackwards(code, finOpen)
  if (tryOpen < 0) return { ok: false, reason: '找不到与该 finally 配对的 try' }
  if (tryOpen > flipAt) {
    return {
      ok: false,
      reason:
        `窄 finally：phase 在第 ${lineOf(flipAt)} 行被拨成 'stopping'，` +
        `而承载 finally 的 try 第 ${lineOf(tryOpen)} 行才开 —— ` +
        `中间这 ${lineOf(tryOpen) - lineOf(flipAt)} 行的语句抛错时 finally 不执行，` +
        `phase 永久卡在 'stopping'，录音 FAB 就此失灵（正是这条承诺要防的事）`,
      tryLine: lineOf(tryOpen), flipLine: lineOf(flipAt), finallyLine: lineOf(finOpen),
    }
  }
  return { ok: true, tryLine: lineOf(tryOpen), flipLine: lineOf(flipAt), finallyLine: lineOf(finOpen) }
}

const RUNTIME_SRC = readFileSync(join(HERE, '..', '..', '..', 'native', 'recordingRuntime.ts'), 'utf8')

describe('§572：phase 复位必须被 finally 罩住（范围，不只是存在性）', () => {
  it('量具自证：runStop() 内 try 起点不晚于 phase 翻转', () => {
    const r = phaseResetCoverage(RUNTIME_SRC)
    assert.ok(r.ok, `判据红：${r.reason}`)
  })

  it('★ 负控①（M26-B 形态）：复位从 finally 挪进函数体 ⇒ 必须红', () => {
    // 这正是实测零强制的那一版：复位挪到 return 之前，happy path 仍正常，
    // 只有「主体抛错时归位」那一半被破坏 —— 45 项门禁全绿，但承诺已破。
    const mutated = RUNTIME_SRC
      .replace(/^.*this\.phase\.value = nextRecordingState\('stopping', 'drafted'\)\n/m, '')
      .replace(
        /^.*return \{ text: finalText, audioBlob, durationMs, draftId \}$/m,
        (ln) => `${ln}\n      this.phase.value = nextRecordingState('stopping', 'drafted')`,
      )
    assert.notEqual(mutated, RUNTIME_SRC, '变异没命中')
    const r = phaseResetCoverage(mutated)
    assert.equal(r.ok, false, '把复位挪出 finally 后判据仍然绿 ⇒ 它是恒真的，删掉重写')
    assert.match(r.reason, /finally 里做 phase 复位|窄 finally/, `实际理由：${r.reason}`)
  })

  it('★ 负控②：窄 finally 形态（= §572 修复前的真实代码）⇒ 必须红，而**粗判据是绿的**', () => {
    // 最小忠实片段：try 晚于 phase 翻转开，finally 里有复位。
    const narrow = [
      '  private async runStop() {',
      "    const blobType = 'audio/webm'",
      "    this.phase.value = nextRecordingState('recording', 'toggle')",
      '    this.recording.value = false',
      '    try {',
      '      await this.slowIo()',
      '    } finally {',
      "      this.phase.value = nextRecordingState('stopping', 'drafted')",
      '    }',
      '  }',
    ].join('\n')

    // 对照：照承诺**字面**写的粗判据 —— 「复位出现在某个 finally 里」
    const crude = /finally\s*\{[\s\S]*?this\.phase\.value\s*=/.test(narrow)
    assert.equal(crude, true,
      '粗判据在这个片段上应当是绿的 —— 若它也红，说明本用例想证明的那件事不成立')

    // 本门用的强度判据必须红
    const r = phaseResetCoverage(narrow)
    assert.equal(r.ok, false, '窄 finally 形态没有被判据抓住')
    assert.match(r.reason, /窄 finally/, `实际理由：${r.reason}`)
  })

  it('负控③：字符串里的花括号不得把配平带偏（§462/§569 同族）', () => {
    const noisy = RUNTIME_SRC.replace(
      'private async runStop()',
      'private async runStop() { const noise = `{ unbalanced ( brace } body { text `; void noise',
    )
    assert.notEqual(noisy, RUNTIME_SRC, '负控样本没插进去')
    const r = phaseResetCoverage(noisy)
    assert.ok(r.ok, `负控下量具失效：${r.reason}`)
  })
})
