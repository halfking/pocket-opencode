// segment-edit-proofread.test.mjs — §131：逐段更正（交互式校对）必须真的落地。
//
// ── 这道门守的是什么 ──
//
// §131 的取证结论：**转写段在界面上是只读的**。
// `TranscriptSegmentList.vue` 当时只有
//   `<p class="segment-text">{{ seg.text }}</p>`
// 而仓里唯一的「写入」入口是列表底部的 composer，它 emit 的是 `append`
// —— **追加一句新段**，不是更正。追加不是更正：原错句一个字没动，
// 改正句还会被 §132 刚修好的相邻去重裁成碎片。
// ⇒ 用户听错的人名/地名/数字，在 App 内**没有任何更正办法**。
//
// 判据分三层，缺一层就会漏掉一种退化：
//
//   1. **行为层**（`applySegmentTextEdit` 纯函数）：改到哪一段、翻不翻 origin、
//      空文本拒不拒不拒不认、没命中要不要凭空造一段。全都能单测。
//   2. **落库层**（`meetings-store.updateSegmentText`）：`origin` 必须在**同一条
//      UPDATE** 里翻成 'manual'。写成第二次写、或干脆不写，行为层照样绿。
//   3. **接线层**（`.vue`）：`editable` 开了就必须有 `@edit`；页面处理函数必须
//      同时经手落库与内存副本两写者。清单由机器给（扫全树 `.vue`），
//      不是手写 `MeetingDetailView.vue` 一个名字 —— §125 的教训：
//      单点接线门天生看不见第二个调用点。
//
// ── 三条防退化的设计（每条都有对应的负控，不是靠注释撑着）──
//
//   A. **量具先自证**：纯函数真的改到了目标段，且**不改入参**（返回新数组 +
//      新对象）。判据里内置一个「假实现」当负控，证明确认能红。
//   B. **反向保护**：只许翻**被改那一段**的 origin。把全部段顺手标成 'manual'
//      能让「改完不被裁碎」变绿，但等于把 §132 的去重整条关掉 —— 必须挡住。
//   C. **origin 翻转是承重的**：用 §132 同一个复现样本跑两遍
//      （origin='asr' vs 'manual'），证明不翻 origin 时**用户改的那一行会消失**。
//      这条把「翻 origin」从约定变成可观测后果。
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { applySegmentTextEdit } from '../segment-edit.ts'
import { dedupeSegments } from '../meeting-dedup.ts'
import { normalizeUtterance } from '../meeting-utterance.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const MEETINGS = path.resolve(HERE, '..')
const SRC = path.resolve(MEETINGS, '../..')
const LIST_VUE = path.join(MEETINGS, 'TranscriptSegmentList.vue')
const DETAIL_VUE = path.join(MEETINGS, 'MeetingDetailView.vue')

// ───────────────────────── 抽取工具（沿用 §125 已验证的形态）─────────────────────────

/** 剥 JS 注释：状态机判断注释，输出保留字符串内容。 */
function stripJsComments(src) {
  let out = ''
  let i = 0
  const n = src.length
  let state = 'code' // code | line | block | sq | dq | tpl
  while (i < n) {
    const c = src[i]
    const d = src[i + 1]
    if (state === 'code') {
      if (c === '/' && d === '/') { state = 'line'; i += 2; continue }
      if (c === '/' && d === '*') { state = 'block'; i += 2; continue }
      if (c === "'") state = 'sq'
      else if (c === '"') state = 'dq'
      else if (c === '`') state = 'tpl'
      out += c
      i++
      continue
    }
    if (state === 'line') {
      if (c === '\n') { state = 'code'; out += '\n' }
      i++
      continue
    }
    if (state === 'block') {
      if (c === '*' && d === '/') { state = 'code'; i += 2; continue }
      if (c === '\n') out += '\n'
      i++
      continue
    }
    if (c === '\\') { out += c + (d ?? ''); i += 2; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tpl' && c === '`')) {
      state = 'code'
    }
    out += c
    i++
  }
  return out
}

/** HTML 注释换成等量换行，保住行号。 */
function stripHtmlComments(raw) {
  return raw.replace(/<!--[\s\S]*?-->/g, (m) => '\n'.repeat(m.split('\n').length - 1))
}

/** 取 .vue 的模板段（剥 HTML 注释）。 */
function templateOf(raw) {
  const noHtml = stripHtmlComments(raw).replace(/<!--[\s\S]*?-->/g, ' ')
  const m = noHtml.match(/<template[^>]*>([\s\S]*)<\/template>/)
  return m ? m[1] : ''
}

/** 取 .vue / .ts 的可执行代码（.vue 只取 <script>）。 */
function codeOf(raw, file) {
  const noHtml = stripHtmlComments(raw)
  if (!file.endsWith('.vue')) return stripJsComments(noHtml)
  const m = noHtml.match(/<script[^>]*>([\s\S]*?)<\/script>/)
  return m ? stripJsComments(m[1]) : ''
}

/**
 * 从 `from` 之后开始做**字符串感知**的花括号配平，切出整块 `{ … }`。
 *
 * ⚠ 不能用朴素的 `indexOf('{')` + 数括号：函数体里的字符串/模板字面量
 *   里出现 `{`（比如 `toast.warning('用 { } 包住')`）会让配平提前结束，
 *   切出半截函数体 —— 那样「函数体里没有 updateSegmentText(」就会恒成立，
 *   判据变恒真。本仓已被恒真判据骗过多次（见 §125/§132 的量具自证）。
 */
function braceBlockAfter(code, from) {
  const start = code.indexOf('{', from)
  if (start < 0) return ''
  let depth = 0
  let state = 'code'
  for (let i = start; i < code.length; i++) {
    const c = code[i]
    const d = code[i + 1]
    if (state === 'code') {
      if (c === "'") state = 'sq'
      else if (c === '"') state = 'dq'
      else if (c === '`') state = 'tpl'
      else if (c === '{') depth++
      else if (c === '}') {
        depth--
        if (depth === 0) return code.slice(start, i + 1)
      }
      continue
    }
    if (c === '\\') { i++; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tpl' && c === '`')) {
      state = 'code'
    }
  }
  return ''
}

/**
 * 从 `from` 起找**函数体**的左花括号，跳过参数列表。
 *
 * ⚠ 第一版直接 `indexOf('{')` ⇒ 在 `onEditSegment(payload: { id: string; text: string })`
 *   上切出的是**类型字面量**，于是「函数体里没有 applySegmentTextEdit(」恒成立 ——
 *   判据当场变成恒真。判据红先问「量具坏了吗」：这次就是。
 *   正确形态是**括号深度感知**：`(` 里的 `{` 是类型/默认值，不是函数体。
 */
function bodyStart(code, from) {
  let paren = 0
  let state = 'code'
  for (let i = from; i < code.length; i++) {
    const c = code[i]
    if (state === 'code') {
      if (c === "'") state = 'sq'
      else if (c === '"') state = 'dq'
      else if (c === '`') state = 'tpl'
      else if (c === '(') paren++
      else if (c === ')') { if (paren > 0) paren-- } else if (c === '{' && paren === 0) return i
      continue
    }
    if (c === '\\') { i++; continue }
    if ((state === 'sq' && c === "'") || (state === 'dq' && c === '"') || (state === 'tpl' && c === '`')) {
      state = 'code'
    }
  }
  return -1
}

/**
 * 切出 `name` 的函数体。三种真实写法都覆盖：
 *   · `function name(` / `const name = (…) =>` / `class` 里的 `async name(`
 * ⚠ 第三种是本会话实测踩到的：`recordingRuntime.ts` 的 `appendText` 是类方法，
 *   前两种写法都抽不到它 ⇒ 「它确实会早退 return null」那条判据变恒真。
 */
function fnBody(code, name) {
  const decl = new RegExp(
    `(?:function\\s+${name}\\b` +
    `|(?:const|let|var)\\s+${name}\\b[^=\\n]*=\\s*(?:async\\s*)?` +
    `|(?:^|\\n)\\s*(?:(?:private|public|protected|static|readonly|async)\\s+)*${name}\\s*[(<])`,
  )
  const m = code.match(decl)
  if (!m) return ''
  const at = bodyStart(code, m.index + m[0].length)
  if (at < 0) return ''
  return braceBlockAfter(code, at)
}

/** 递归列出 .vue（排除测试与 node_modules）。 */
function vueFiles(dir, acc = []) {
  for (const name of readdirSync(dir)) {
    const full = path.join(dir, name)
    if (statSync(full).isDirectory()) {
      if (name === '__tests__' || name === 'node_modules') continue
      vueFiles(full, acc)
    } else if (full.endsWith('.vue')) acc.push(full)
  }
  return acc
}

// ───────────────────────── 接线判据 ─────────────────────────

/** 模板里所有 `<TranscriptSegmentList …/>` 标签的属性串。 */
function segmentListTags(tpl) {
  return [...tpl.matchAll(/<TranscriptSegmentList\b([^>]*?)\/?>/g)].map((m) => m[1])
}

/** 该标签是否把更正入口打开（`editable` / `:editable="true"`）。 */
function editableOpened(attrs) {
  const m = attrs.match(/(?:^|\s):?editable(?:\s*=\s*"([^"]*)")?/)
  if (!m) return false
  if (m[1] === undefined) return true          // 裸属性 editable ⇒ 真
  return !/^(false|0|''|""|undefined)$/.test(m[1].trim())
}

/**
 * 对**单个**标签判定「是否未接线」；返回原因（空串 = 已接线）。
 *
 * ⚠ 抽成函数而不是把规则内联进扫描循环：负控必须能**真的走它**，
 *   只验两条正则字符串等于没验抽取器（§125 的 S4 就是这么空转的）。
 */
function editUnwiredReason(attrs) {
  if (!editableOpened(attrs)) return ''
  if (/@edit\s*=/.test(attrs)) return ''
  return '开了 editable 却没有 @edit ⇒ 用户的更正会被静默丢弃'
}

/**
 * 全树扫出「渲染了 TranscriptSegmentList 的文件」，并逐个判定
 * 「开了 editable 却没有 @edit」——返回未接线的文件清单。
 *
 * ⚠ 清单由机器给（扫 `src 下的 .vue` 的模板），不是手写文件名。
 *   `SessionLiveRecordPanel.vue` 也在清单里：它共用同一个组件但**没有落库/
 *   回写链路**，所以它不传 `editable`（默认 false）—— 将来有人给它打开时，
 *   这条判据会立刻要求它同时接上 `@edit`，不会多出一个没有出口的编辑框。
 */
function unhandledEdits() {
  const bad = []
  for (const full of vueFiles(SRC)) {
    for (const attrs of segmentListTags(templateOf(readFileSync(full, 'utf8')))) {
      if (editUnwiredReason(attrs)) bad.push(`${path.relative(SRC, full)}：${editUnwiredReason(attrs)}`)
    }
  }
  return bad
}

const DETAIL_CODE = codeOf(readFileSync(DETAIL_VUE, 'utf8'), DETAIL_VUE)
const STORE_CODE = codeOf(readFileSync(path.join(MEETINGS, 'meetings-store.ts'), 'utf8'), 'meetings-store.ts')
const RUNTIME_CODE = codeOf(readFileSync(path.join(SRC, 'native/recordingRuntime.ts'), 'utf8'), 'recordingRuntime.ts')
const LIST_RAW = readFileSync(LIST_VUE, 'utf8')
const LIST_CODE = codeOf(LIST_RAW, LIST_VUE)
const LIST_TPL = templateOf(LIST_RAW)

// ───────────────────────── 夹具 ─────────────────────────

/** 构造一个 ASR 段（与 ingest-speech 的真实字段同形）。 */
function asrSeg(id, text, startMs, endMs = startMs + 1200) {
  return {
    id,
    meetingId: 'm1',
    speakerLabel: '说话人',
    lang: 'zh',
    confidence: 0.86,
    startMs,
    endMs,
    text,
    origin: 'asr',
  }
}

const SEGMENTS = [
  asrSeg('s1', '我们下周三交对比表', 0, 2000),
  asrSeg('s2', '张薇下周三交对比表', 1000, 3000),
  asrSeg('s3', '好', 3200, 4000),
]

describe('§131 · 逐段更正', () => {
  it('量具自证：真的改到了目标段，且不改入参', () => {
    const input = SEGMENTS.map((s) => ({ ...s }))
    const out = applySegmentTextEdit(input, 's2', '张伟下周三交对比表')
    assert.equal(out.changed, true, `没有报告改动（reason=${out.reason}）`)
    assert.equal(out.segments[1].text, '张伟下周三交对比表', '目标段没被改')
    assert.equal(input[1].text, '张薇下周三交对比表', '入参被就地改了 ⇒ 内存里的真实状态被污染')
    assert.notEqual(out.segments[1], input[1], '返回了同一个对象引用 ⇒ 改不动数据库行的映射')
    // 负控：一个「什么都不做」的实现必须被判为不合格 ——
    // 证明上面那几条断言不是恒真的。
    const noop = (segs) => ({ segments: segs, changed: false, reason: 'unchanged' })
    assert.equal(noop(input).segments[1].text, '张薇下周三交对比表', '负控前提失败')
    assert.notEqual(noop(input).segments[1].text, out.segments[1].text,
      '确认项：no-op 与真实现的读数确实不同')
  })

  it('★ 改完的段必须翻成 origin=manual（§132 的去重豁免）', () => {
    const out = applySegmentTextEdit(SEGMENTS, 's2', '张伟下周三交对比表')
    assert.equal(out.segments[1].origin, 'manual',
      '改完仍留在 asr ⇒ 下一次 dedupeSegments 会把用户的更正当重转写裁碎')
  })

  it('★ origin 翻转是承重的：不翻的话用户改的那一行会从界面上消失', () => {
    // 用 §132 同一个形状：机器把同一段音转了两次（时间轴重叠 ⇒ 该去重），
    // 用户把第二行改成人名。改完之后它的正文与上一行**完全相同** ——
    // 这正是「更正」的结果。此时若它还留在 'asr'：
    //   dedupeStep(prev='张伟下周三交对比表', raw='张伟下周三交对比表') ⇒
    //   零净增 + 时间轴重叠 ⇒ `if (step.added) out.push(...)` 整段丢弃
    // ⇒ 用户点完「保存」，那一行凭空消失。这条把「翻 origin」变成可观测后果，
    // 而不是一条约定。
    const prev = asrSeg('s1', '张伟下周三交对比表', 0, 2000)
    const wrong = asrSeg('s2', '张薇下周三交对比表', 1000, 3000)
    const edited = applySegmentTextEdit([prev, wrong], 's2', '张伟下周三交对比表')
    assert.equal(edited.changed, true, '前提自证失败：样本本身没被改')

    // 前提自证 A：改**之前**，机器把改正句裁成一个字（§132 的现象）
    assert.deepEqual(dedupeSegments([prev, wrong]).map((s) => s.text),
      ['张伟下周三交对比表', '薇'],
      '前提自证失败：ASR 形态下这一行没被裁成「薇」⇒ 样本形状变了')

    // 前提自证 B：改了但**仍留在 asr** ⇒ 零净增 + 时间轴重叠 ⇒ 整段从界面上消失
    const stillAsr = dedupeSegments([prev, { ...edited.segments[1], origin: 'asr' }])
      .map((s) => s.text)
    assert.deepEqual(stillAsr, ['张伟下周三交对比表'],
      '前提自证失败：留在 asr 时这一行居然还在 ⇒ 下面那条证明不了 origin 是承重的')
    // 真的：翻了 manual ⇒ 原样显示两行。
    // 显示两行相同内容是**有意的**：用户输入优先于机器规则，
    // 替他静默删掉一行才是数据损坏（与 §132 对追加句的处置同一条原则）。
    const shown = dedupeSegments(edited.segments).map((s) => s.text)
    assert.deepEqual(shown, ['张伟下周三交对比表', '张伟下周三交对比表'],
      '★ 用户的更正必须原样显示。把 origin 留在 asr 时它会被整段吞掉（改完那一行不见了）')
  })

  it('反向保护：只许翻被改那一段的 origin，邻居的正文与来源都不能动', () => {
    const input = SEGMENTS.map((s) => ({ ...s }))
    const out = applySegmentTextEdit(input, 's2', '张伟下周三交对比表')
    for (const i of [0, 2]) {
      assert.deepEqual(out.segments[i], input[i],
        `第 ${i} 段被顺手改了 ⇒ 「把全部段标 manual 让去重失效」这条路是通的` +
        '（§132 的 M2 反向保护同一个洞）')
    }
  })

  it('没命中 id 绝不凭空造一段', () => {
    const input = SEGMENTS.map((s) => ({ ...s }))
    const out = applySegmentTextEdit(input, 's-nope', '随便一句')
    assert.equal(out.changed, false)
    assert.equal(out.reason, 'not-found')
    assert.equal(out.segments, input, '未命中时返回的不是入参同一引用 ⇒ 页面会白白重渲染')
    assert.equal(out.segments.length, SEGMENTS.length, '未命中却改变了段数')
  })

  it('空文本必须拒（否则产出一行「[说话人] 」的空段）', () => {
    for (const blank of ['', '   ', '\n\t ', '　']) {
      const out = applySegmentTextEdit(SEGMENTS, 's2', blank)
      assert.equal(out.changed, false, `空白 ${JSON.stringify(blank)} 被当成正文写了进去`)
      assert.equal(out.reason, 'empty-text', `空白 ${JSON.stringify(blank)} 的原因不对`)
    }
  })

  it('空白折叠必须与 buildUtteranceSegment 同一把尺子（不另立一套规则）', () => {
    const out = applySegmentTextEdit(SEGMENTS, 's2', '  张伟  下周三  交对比表  ')
    assert.equal(out.segments[1].text, normalizeUtterance('  张伟  下周三  交对比表  '),
      '与 normalizeUtterance 的读数不一致 ⇒ 两把尺子迟早分叉')
    assert.equal(out.segments[1].text, '张伟 下周三 交对比表')
  })

  it('原文一字未改时不落库（避免无谓写与无谓翻 origin）', () => {
    const out = applySegmentTextEdit(SEGMENTS, 's2', '  张薇下周三交对比表  ')
    assert.equal(out.changed, false, '仅空白差异也算改动')
    assert.equal(out.reason, 'unchanged')
  })

  it('负控：判据确实能红（把 origin 翻转从实现里去掉）', () => {
    // 与真实现同形的「不翻 origin」版本：只改 text。
    const noFlip = (segs) => {
      const at = segs.findIndex((s) => s.id === 's2')
      const next = segs.slice()
      next[at] = { ...segs[at], text: '张伟下周三交对比表' }
      return { segments: next, changed: true, reason: 'ok', segment: next[at] }
    }
    const bad = noFlip(SEGMENTS)
    assert.equal(bad.changed, true, '负控前提失败：样本连改动都没报告')
    assert.notEqual(bad.segments[1].origin, 'manual',
      '负控前提失败：样本竟然翻了 origin ⇒ 负控不再同形')
    assert.notEqual(bad.segments[1].origin, applySegmentTextEdit(SEGMENTS, 's2', '张伟下周三交对比表').segments[1].origin,
      '确认项：负控样本与真实现的读数确实不同')
  })
})

describe('§131 · 落库层', () => {
  it('量具自证：updateSegmentText 这个函数体抽得到、且确实在改段表', () => {
    const body = fnBody(STORE_CODE, 'updateSegmentText')
    assert.ok(body.length > 0, '抽不到 updateSegmentText 的函数体 ⇒ 下面几条会变成恒真')
    assert.match(body, /local_meeting_segments/, '抽到的不是段表那条 UPDATE')
  })

  it('★ origin 必须在同一条 UPDATE 里翻成 manual', () => {
    const body = fnBody(STORE_CODE, 'updateSegmentText')
    assert.match(body, /UPDATE\s+local_meeting_segments\s+SET\s+text\s*=\s*\?(?:(?!\bUPDATE\b)[^;])*?origin\s*=\s*'manual'/s,
      "updateSegmentText 没有在同一条 UPDATE 里把 origin 翻成 'manual' ⇒ " +
      '用户的更正改完之后仍会被相邻去重裁碎（§132）')
  })

  it('负控：只改 text 不翻 origin ⇒ 必须判不合格', () => {
    const twoStatements = `
      export async function updateSegmentText(segmentId, text) {
        await localDB.run('UPDATE local_meeting_segments SET text = ? WHERE id = ?', [text, segmentId])
      }
    `
    const body = fnBody(stripJsComments(twoStatements), 'updateSegmentText')
    assert.ok(body.length > 0, '负控前提失败：连函数体都切不出来')
    assert.doesNotMatch(body, /UPDATE\s+local_meeting_segments\s+SET\s+text\s*=\s*\?(?:(?!\bUPDATE\b)[^;])*?origin\s*=\s*'manual'/s,
      '负控失败：只改 text 的形态被判成了合格')
  })

  it('负控：分两次写（先 text 后 origin）同样判不合格', () => {
    // 允许这种写法的是**别人**的代码，不是这里：两次写在并发下会留下
    // 「正文已改、origin 还没翻」的窗口，那一刻去重照旧生效 ⇒ 又碎一次。
    const twoWrites = `
      export async function updateSegmentText(segmentId, text) {
        await localDB.run('UPDATE local_meeting_segments SET text = ? WHERE id = ?', [text, segmentId])
        await localDB.run("UPDATE local_meeting_segments SET origin = 'manual' WHERE id = ?", [segmentId])
      }
    `
    const body = fnBody(stripJsComments(twoWrites), 'updateSegmentText')
    assert.doesNotMatch(body, /SET\s+text\s*=\s*\?(?:(?!\bUPDATE\b)[^;])*?origin\s*=\s*'manual'/s,
      '负控失败：两次写的形态被判成了合格')
  })

  it('★ appendText 在没在录时确实返回 null（这是「录完点写入没反应」的依据）', () => {
    // 这条判据把「为什么需要补落库分支」钉在**源码事实**上，而不是我的记忆：
    // 若哪天 runtime 改了语义（改成不录音也能追加），这条会红，提醒把
    // 页面里那支补充分支撤掉而不是留着两套。
    const body = fnBody(RUNTIME_CODE, 'appendText')
    assert.ok(body.length > 0, '抽不到 appendText 的函数体 ⇒ 下面那条断言是恒真的')
    assert.match(body, /if\s*\(!meetingId\)\s*return null/,
      'appendText 的早退形态变了 ⇒ 请重新判断页面里 onAppendUtterance 的落库分支是否还需要')
  })
})

describe('§131 · 接线层', () => {
  it('量具自证：扫得到已开更正入口的渲染方，且清单不是空的', () => {
    const hosts = vueFiles(SRC)
      .filter((f) => segmentListTags(templateOf(readFileSync(f, 'utf8'))).length > 0)
      .map((f) => path.relative(SRC, f))
    assert.ok(hosts.length >= 2, `只扫到 ${hosts.length} 个渲染方（${hosts}）⇒ 抽取器大概率坏了`)
    assert.ok(hosts.includes(path.join('features', 'meetings', 'MeetingDetailView.vue')),
      `详情页没被扫出来 ⇒ 抽取器有盲区（${hosts}）`)
    const opened = vueFiles(SRC).filter((f) =>
      segmentListTags(templateOf(readFileSync(f, 'utf8'))).some(editableOpened))
    assert.ok(opened.length >= 1,
      '没有任何一处打开更正入口 ⇒ 整道接线判据是恒真的（能力不可达却全绿）')
  })

  it('★ 开了 editable 的渲染方必须同时接 @edit', () => {
    assert.deepEqual(unhandledEdits(), [],
      '这些地方打开了逐段更正却没有处理 edit 事件 ⇒ 用户的更正会被静默丢弃')
  })

  it('负控：开了 editable 却没 @edit 的标签必须被判不合格', () => {
    const badTag = ' :segments="segs" :editable="true" @append="onAppend" '
    assert.equal(editableOpened(badTag), true, '负控前提失败：editable 没被判成已打开')
    assert.ok(editUnwiredReason(badTag), '负控失败：开了却没 @edit 的标签被判成了合格')
    // 正控走同一个函数，证明它不是恒假的
    const goodTag = ' :segments="segs" :editable="true" @append="onAppend" @edit="onEditSegment" '
    assert.equal(editUnwiredReason(goodTag), '', '正控失败：接了 @edit 却仍判未接线')
    // 没打开的渲染方不要求 @edit（SessionLiveRecordPanel 就是这一类）
    const offTag = ' :segments="segs" :is-recording="on" '
    assert.equal(editableOpened(offTag), false, '负控前提失败：没传 editable 却判成打开了')
    assert.equal(editUnwiredReason(offTag), '', '没打开更正入口的渲染方不该被判未接线')
  })

  it('editable 默认必须是关的（没有落库链路的渲染方不该凭空多个编辑框）', () => {
    assert.match(LIST_CODE, /editable\?\s*:\s*boolean/,
      'editable 不是可选布尔 ⇒ 共用本页的渲染方会被强制表态')
    const defaults = LIST_CODE.match(/withDefaults\([^)]*\)\s*(?:,\s*(\{[\s\S]*?\}))?/)
    assert.ok(defaults, '抽不到 withDefaults ⇒ 这条断言是恒真的')
    assert.doesNotMatch(defaults[1] ?? '', /editable\s*:\s*true/,
      'editable 的默认值是 true ⇒ 所有渲染方都会显示一个未必有落库出口的编辑框')
  })

  it('★ 页面处理函数必须同时经手落库与内存副本（两写者只改一个 = 改不动）', () => {
    const body = fnBody(DETAIL_CODE, 'onEditSegment')
    assert.ok(body.length > 0, '抽不到 onEditSegment ⇒ 下面三条断言全是恒真的')
    assert.match(body, /applySegmentTextEdit\(/, '没有把判定交给纯函数 ⇒ 组件里另立了一套规则')
    assert.match(body, /updateSegmentText\(/,
      '没有落库 ⇒ 用户改的东西刷新就没了')
    assert.match(body, /editSegmentInMemory\(/,
      '没有同步 runtime 内存副本 ⇒ 录音中 displaySegments 优先读它，界面会立刻显示旧文本')
  })

  it('负控：处理函数里删掉落库调用 ⇒ 必须判不合格', () => {
    const gutted = `
      async function onEditSegment(payload) {
        const res = applySegmentTextEdit(displaySegments.value, payload.id, payload.text)
        if (!res.changed) return
        await editSegmentInMemory(payload.id, res.segment.text)
      }
    `
    const body = fnBody(stripJsComments(gutted), 'onEditSegment')
    assert.ok(body.length > 0, '负控前提失败：切不出函数体')
    assert.doesNotMatch(body, /updateSegmentText\(/, '负控失败：没有落库也判成了合格')
  })

  it('★ storedSegments 不得被喂去重结果（那是展示用子集，不是原始段）', () => {
    // dedupeSegments 会**整段丢弃**零净增的行（`if (step.added) out.push(...)`），
    // 把它的输出写回 storedSegments 等于把「原始段」换成「展示子集」：
    // 下一次 seedSegments（续录恢复）会拿这份子集当完整历史。
    assert.doesNotMatch(DETAIL_CODE, /storedSegments\.value\s*=\s*\[\s*\.\.\.\s*displaySegments\.value\s*\]/,
      'onAppendUtterance 把去重后的 displaySegments 写回了 storedSegments ⇒ 续录会丢段')
    // 正控：证明上面那条正则不是恒假。
    assert.match('storedSegments.value = [...displaySegments.value]',
      /storedSegments\.value\s*=\s*\[\s*\.\.\.\s*displaySegments\.value\s*\]/,
      '正控失败：判据对已知的坏形态都不命中 ⇒ 它永远抓不到东西')
    // 反向：正确的形态（同步 runtime 的原始列表）不得被误伤。
    assert.doesNotMatch('storedSegments.value = [...segments.value]',
      /storedSegments\.value\s*=\s*\[\s*\.\.\.\s*displaySegments\.value\s*\]/,
      '误伤了正确写法')
  })

  it('★ 录完之后点「写入」必须真的有落库分支（appendText 在没在录时返回 null）', () => {
    const body = fnBody(DETAIL_CODE, 'onAppendUtterance')
    assert.ok(body.length > 0, '抽不到 onAppendUtterance ⇒ 这条断言是恒真的')
    assert.match(body, /if\s*\(\s*!saved\s*\)/,
      'onAppendUtterance 不再处理「appendText 什么都没返回」⇒ 判据的前提变了')
    assert.match(body, /saveSegment\(/,
      '录完之后点「写入」仍然是静默无反应（appendText 在 activeMeetingId 为空时返回 null）')
  })

  it('负控：只写 early-return 的原始形态必须判不合格', () => {
    const original = `
      async function onAppendUtterance(text) {
        const saved = await appendText(text)
        if (!saved) return
        storedSegments.value = [...segments.value]
        await refreshRelated()
      }
    `
    const body = fnBody(stripJsComments(original), 'onAppendUtterance')
    assert.ok(body.length > 0, '负控前提失败：切不出函数体')
    assert.doesNotMatch(body, /saveSegment\(/, '负控失败：修复前的形态被判成了合格')
  })

  it('★ 列表组件必须真的把 edit 载荷发出去（带 id 与 text）', () => {
    assert.match(LIST_CODE, /edit\s*:\s*\[\s*payload\s*:\s*\{\s*id\s*:\s*string\s*;\s*text\s*:\s*string\s*\}\s*\]/,
      "TranscriptSegmentList 没有声明带 id/text 的 edit 载荷 ⇒ 页面拿不到要改哪一段")
    assert.match(LIST_CODE, /emit\(\s*'edit'\s*,/,
      '组件里没有真正 emit edit ⇒ 点了保存什么也不会发生')
    // 入口门控必须在模板上（否则「不接线的渲染方也显示编辑框」）。
    assert.match(LIST_TPL, /v-if="editable\b/,
      '模板里的更正入口没有受 editable 门控')
  })

  it('负控：把入口门控去掉 ⇒ 必须判不合格', () => {
    assert.doesNotMatch('  <button type="button" @click="onStartEdit(seg)">更正</button>',
      /v-if="editable\b/, '负控失败：去掉门控的形态仍被判合格')
  })

  it('★ origin 必须可见（用户得知道哪些行是他自己改过的）', () => {
    // origin='manual' 决定这一行**不再被相邻去重裁碎**。看不见它，
    // 用户会以为「去重又在吃我的修改」——而实际上这正是修复生效的证据。
    assert.match(LIST_TPL, /origin\s*===\s*'manual'/,
      "模板没有渲染 origin='manual' 的徽章 ⇒ 用户无从分辨哪些行是自己校对的")
  })
})
