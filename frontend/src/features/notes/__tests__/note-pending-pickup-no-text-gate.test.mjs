// note-pending-pickup-no-text-gate.test.mjs
//
// 锁住「跨页拾取不得因为没有文字就丢弃录音」。
//
// 2026-10-06 真机复现（两臂只差「转写有没有出字」，跨页/录音/时长/桩全同）：
//   臂A 跨页 + 真后端（有字）        local_notes 17→18，新笔记 len=92 dur=16803ms   PASS
//   臂B 跨页 + 桩 mode=empty（无字） local_notes 18→18，**一条都没有**              FAIL
// 桩 fullCalls 1→2 证明臂B 的兜底确实被调用并返回了空文字，不是「压根没调转写」。
//
// 成因：pickupPendingRecording 写成
//     const pending = consumePendingResult()
//     if (pending && pending.text.trim()) { await createVoiceDraft(...) }
//
// 两个事实凑成这个 bug：
//   ① consumePendingResult() 是「取过即清」⇒ 条件不成立时不是「留着以后用」，
//      是**当场销毁**。
//   ② createVoiceDraft 内部写的是 `text || '（语音草稿）'`，**明确接受空文字**。
//      能力在，调用点把它挡在门外。
//
// 触发需要两个条件同时成立（所以窄）：① 跨页停止（走 consumePendingResult 路径）
// ② 转写没出字（用户中止 / 网络失败 / 静音录音）。页面在场走 onMicToggle，拿
// toggleRecording() 的返回值无条件建草稿，不受影响。
//
// 上一轮修的 note-pending-pickup-on-keepalive 只管 onActivated/onMounted 的**接线**，
// 不检查这个条件 —— 「让拾取跑起来」和「跑起来后在什么情况下会静默丢弃」是两个维度。
//
// 本门末尾的负控把旧行为原样塞回去（空字直接 return），本门必须报红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')
const VIEW = path.join(SRC, 'features', 'notes', 'NoteListView.vue')

/** 只保留 <script setup> 块并剥掉注释，但**保留字符串字面量**。
 *
 *  为什么不直接用 stripCommentsAndStrings：那个把字符串也抹成空格，于是
 *  `text || '（语音草稿）'` 变成 `text || '            '`，断言必然落空。
 *  （同一个坑本会话踩过两次：defineOptions 的 name、这里的占位文案。）
 *  凡是要看**字面量内容**的判据，都必须走这个。 */
export function stripCommentsOnly(raw) {
  const s = raw.indexOf('<script setup')
  const e = raw.lastIndexOf('</script>')
  const scoped = s >= 0 && e > s ? raw.slice(s, e) : raw
  return scoped
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:])\/\/[^\n]*/g, (m, p1) => p1 + ' '.repeat(Math.max(0, m.length - p1.length)))
    .replace(/import\s[\s\S]*?from\s+['"][^'"]*['"];?/g, (m) => ' '.repeat(m.length))
}

/** 连字符串一起剥：只用于判「代码结构/控制流」，不用于判字面量内容。 */
export function stripCommentsAndStrings(raw) {
  return stripCommentsOnly(raw)
    .replace(/'(?:[^'\\\n]|\\.)*'/g, (m) => "'" + ' '.repeat(Math.max(0, m.length - 2)) + "'")
    .replace(/"(?:[^"\\\n]|\\.)*"/g, (m) => '"' + ' '.repeat(Math.max(0, m.length - 2)) + '"')
}

/**
 * 从 `marker` 之后取出紧随的大括号体（括号配平，跨过 ()/[] 深度）。
 *
 * 不能用 `marker\s*\([^)]*\)\s*\{`：`async function f(): Promise<void> {`
 * 的参数表里就有 ')'，非贪婪 `[^)]*` 会在那里停住。
 */
export function bodyAfter(src, marker) {
  const at = src.indexOf(marker)
  if (at < 0) return null
  let i = at + marker.length
  let paren = 0
  for (; i < src.length; i++) {
    const c = src[i]
    if (c === "'" || c === '"' || c === '`') {
      const q = c
      for (i++; i < src.length && src[i] !== q; i++) if (src[i] === '\\') i++
      continue
    }
    if (c === '(' || c === '[') paren++
    else if (c === ')' || c === ']') paren--
    else if (c === '{' && (paren <= 0 || src.slice(0, i).trimEnd().endsWith('=>'))) {
      let depth = 0
      for (let k = i; k < src.length; k++) {
        if (src[k] === '{') depth++
        else if (src[k] === '}') { depth--; if (depth === 0) return src.slice(i, k + 1) }
      }
      return null
    }
  }
  return null
}

/**
 * 返回「跨页拾取会丢弃无文字录音」的描述；null = 合规。
 * 单独抽成函数，好让文件末尾的负控对**同一段检测逻辑**跑变异。
 */
export function findSilentDropViolation(body) {
  if (!body) return 'pickupPendingRecording() 函数体没找到（门形同虚设，必须先修这个）'

  // ① 以「文字为空」为由提前 return 的分支。
  //    注意 `if (!pending) return` 是**合法**的（没有产物才走），条件里出现
  //    `.text` 才算 bug —— 第一版把 pending 单独也收进可选组，修好之后照样报红。
  //
  // ★ 这里必须用 [^;{]* 而不是 [^)]*：条件里带调用（pending.text.trim()）时
  //   `if (` 后面有**两个**右括号，`[^)]*\)` 只能吃到 trim() 的那个，
  //   接着 `\s*return` 前面是另一个 `)` ⇒ 判据在真有 bug 时也不报红。
  //   （本会话第三次栽在「括号里套括号」上；bodyAfter 的 paren 深度是同一个教训。）
  if (/if\s*\([^;{]*\.text[^;{]*\)\s*return/.test(body)) {
    return 'pickupPendingRecording() 里存在「以文字为空为由提前 return」的分支：' +
      'consumePendingResult() 取过即清，这个 return 等于当场销毁录音（音频也一起没了）'
  }
  // ② 把整段调用包在「有字才做」的条件里（旧版写法）
  if (/\(\s*pending\s*&&\s*pending\.text\.trim\(\)/.test(body)) {
    return 'pickupPendingRecording() 的准入条件里含 pending.text.trim()：' +
      '没有文字的录音连草稿都建不出来'
  }
  // ③ 拿到 pending 之后必须真的建出点什么
  if (!/adoptVoiceDraft\(|createVoiceDraft\(/.test(body)) {
    return 'pickupPendingRecording() 拿到 pending 之后既没 adoptVoiceDraft 也没 createVoiceDraft：' +
      '录音被读出来然后丢掉'
  }
  return null
}

const read = (p) => fs.readFileSync(p, 'utf8')
const pickupBody = () => bodyAfter(stripCommentsAndStrings(read(VIEW)), 'pickupPendingRecording')

describe('跨页拾取：没有文字的录音也必须留下', () => {
  it('不因 text 为空而提前返回，且会建出草稿', () => {
    assert.equal(findSilentDropViolation(pickupBody()), null)
  })

  it('前提：consumePendingResult 确实是「取过即清」', () => {
    // 没有这一条，「提前 return 只是先留着」就成了一种辩护，判据会失去牙。
    // 必须先剥注释：不剥的话 bodyAfter 会先命中注释里提到的字面量。
    const rt = stripCommentsOnly(read(path.join(SRC, 'native', 'recordingRuntime.ts')))
    const fn = bodyAfter(rt, 'consumePendingResult')
    assert.ok(fn, 'consumePendingResult() 没找到')
    assert.match(fn, /this\.pendingResult\s*=\s*null/, 'pendingResult 竟然没被清空，「取过即清」的前提不成立')
  })

  it('前提：createVoiceDraft 接受空文字（能力本来就有，只是被门槛挡住）', () => {
    // 这里要看**字面量内容**，所以只能用 stripCommentsOnly：
    // stripCommentsAndStrings 会把 '（语音草稿）' 抹成空格，断言必然落空。
    // marker 必须带 `async function` 前缀：直接用 'createVoiceDraft' 会先命中
    // onMicToggle 里的**调用点**，bodyAfter 拿 onMicToggle 的花括号当函数体 ——
    // 与历史上 bodyAfter 命中 import 里那个 onMounted 是同一个坑。
    const view = stripCommentsOnly(read(VIEW))
    const fn = bodyAfter(view, 'async function createVoiceDraft')
    assert.ok(fn, 'createVoiceDraft() 声明没找到（注意别用调用点当 marker）')
    assert.match(fn, /text\s*\|\|\s*['"]（语音草稿）['"]/, 'createVoiceDraft 不再为空文字兜底，前提变了')
  })
})

// ---------------------------------------------------------------------------
// 负控：把旧行为原样塞回去（空字直接 return），本门必须报红。
// 没有这一段，上面的 it() 只是一段看着像断言的文本。
// ---------------------------------------------------------------------------
describe('负控：加回 text 门槛后必须报红', () => {
  it('变异体（无字即 return）被判为违规', () => {
    const body = pickupBody()
    assert.ok(body, '拿不到 pickupPendingRecording() 函数体，负控无法执行')
    assert.ok(/if\s*\(\s*!pending\s*\)\s*return/.test(body),
      '★ 变异前提不成立：源码里已经没有 `if (!pending) return`，本负控已失去意义')

    // 最小忠实变异 = 旧版行为：文字为空就 return，录音当场销毁
    const mutated = body.replace(/if\s*\(\s*!pending\s*\)\s*return/, 'if (!pending) return\n  if (!pending.text.trim()) return')
    const violation = findSilentDropViolation(mutated)
    assert.ok(violation, '★ 门有洞：把无字即 return 加回来竟然判为合规')
    assert.match(violation, /当场销毁|pending\.text\.trim/)
  })

  it('变异体（准入条件里带回 text.trim()）被判为违规', () => {
    const body = pickupBody()
    assert.ok(body, '拿不到 pickupPendingRecording() 函数体，负控无法执行')
    const mutated = body.replace(/if\s*\(\s*!pending\s*\)\s*return/, 'if (pending && pending.text.trim()) return')
    const violation = findSilentDropViolation(mutated)
    assert.ok(violation, '★ 门有洞：把 text.trim() 门槛加回来竟然判为合规')
  })
})
