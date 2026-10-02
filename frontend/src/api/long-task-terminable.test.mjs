// long-task-terminable.test.mjs
//
// 锁住需求「后台执行的 api 可以强行终止」。
//
// 背景：2026-10-03 普查后台作业时这一整类能力基本是空的。口径是「客户端超时
// >= 90 秒的调用点必须能被中止」，15 个长任务调用点里当时只有一部分接了 signal，
// 而会议链那 4 个**接了也白接**：
//
//   meetings.summarize / meetings.refine 的 catch 写成 `catch { return fallback… }`。
//   于是即使调用方传了 signal 并真的 abort 掉，错误也会被 catch 吃掉、走
//   fallbackSummarize / fallbackRefine —— 用户点了「取消」，会议里却多出一份
//   降级摘要/精翻结果。**中止被伪装成了成功**，比「没有中止入口」更坏：前者还
//   给了假的完成信号。
//
// 本文件锁三件事：
//   1. 判据本身（isAbortError）真把中止和失败分开，且跨 realm 也认；
//   2. 15 个长任务调用点**逐个在册**，新增一个而不登记就转红；
//   3. 在册且非 by-design 的调用点，signal 必须真的进了 http 选项，且它所在
//      函数的签名里真的有 signal 形参；catch 降级链必须先放行中止。
//
// 登记表（LONG_TASK_CALL_SITES）同时是**缺口清单**：state 如实记录每个调用点
// 现在到底能不能被用户停掉。这不是装饰——它让「有 plumbing、没按钮」的缺口变成
// 可核对的清单，而不是散在脑子里的印象。
//
// 判据一律**按调用点（http 路径）定位**，不按「函数名」「预算常量」定位：
// 同一个 LONG_REQUEST_TIMEOUT_MS 在 email.ts 里有 4 个调用点、其中 3 个该接
// signal 1 个无所谓，用常量当键根本表达不了这种差异；而按路径定位，判据指的
// 就是它要断言的那一次请求。
//
// 负控在文件末尾，全部要求**断言性失败**。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { isAbortError } from './abort.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..')

/** 客户端预算多长才算「长任务」。90 秒取自会议摘要/笔记总结的下限。 */
const LONG_TASK_FLOOR_MS = 90_000

/**
 * 长任务调用点登记表。键 = (file, path)。
 *
 * state 三态，含义不可互换：
 *   wired    —— 调用方真的持有 AbortController 并传了 signal，界面上也有停止入口
 *   plumbed  —— api 层接了 signal，但**界面还没有停止入口**（用户仍只能干等）
 *   by-design —— 刻意不接，必须在 reason 里写清为什么
 */
const LONG_TASK_CALL_SITES = [
  { file: 'api/email.ts', path: '/api/email/backfill', state: 'wired', surface: '邮件回填',
    reason: 'use-email-inbox 的 backfill controller，界面有「停止」' },
  { file: 'api/email.ts', path: '/api/emails/${encodeURIComponent(id)}/summarize', state: 'wired', surface: '单封邮件摘要',
    reason: 'summarizeEmail 已接 signal，界面可重试/放弃' },
  { file: 'api/email.ts', path: '/api/emails/classify', state: 'wired', surface: '收件箱归类',
    reason: 'cancelClassifyRun，界面有「停止」' },
  { file: 'api/email.ts', path: '/api/emails/move', state: 'plumbed', surface: '邮件移动（IMAP MOVE）',
    reason: 'moveEmails 已接 signal，但 EmailInboxView 只有 moveBusy 禁用态，无停止入口' },
  { file: 'api/email.ts', path: '/api/emails/ops/sync', state: 'plumbed', surface: '离线操作回放',
    reason: 'syncOps 已接 signal，调用点 email-folders-store 无中止器' },
  { file: 'api/email.ts', path: '/api/emails/organize', state: 'plumbed', surface: '智能整理通知邮件',
    reason: 'organizeInbox 已接 signal，但 onOrganize 只有 organizing 禁用态，无停止入口' },
  { file: 'api/email.ts', path: '/api/emails/invoices/extract', state: 'wired', surface: '发票整理',
    reason: 'use-invoice-list 的 controller，界面有「停止本轮整理」' },
  { file: 'api/email.ts', path: '/api/email/pipeline/run', state: 'wired', surface: '邮件流水线（全量）',
    reason: 'email-job-runtime 的 pipeline controller，界面有「停止」' },
  { file: 'api/notes.ts', path: '/api/notes/${id}/summarize', state: 'plumbed', surface: '语音草稿的即时总结',
    reason: 'signal 已透传，NoteListView 的 summarizing 态没有停止入口' },
  { file: 'api/meetings.ts', path: '/api/meetings/${meetingId}/summary', state: 'plumbed', surface: '会议实时摘要',
    reason: 'signal 已透传且中止不再被降级链吞掉，但 useLiveSummary 没有中止入口' },
  { file: 'api/meetings.ts', path: '/api/meetings/${meetingId}/refine', state: 'plumbed', surface: '会议事后精翻',
    reason: '同上；catch 已改成先放行中止，界面还没有停止入口' },
  { file: 'api/stt-settings.ts', path: '/api/stt/discover', state: 'plumbed', surface: '重新扫描网关',
    reason: 'discover 已接 signal，界面只有「扫描中…」禁用态，无停止入口' },
  { file: 'api/stt-settings.ts', path: '/api/stt/transcribe-full', state: 'wired', surface: '录音收尾的兜底全量转写',
    reason: 'NoteRecorderRuntime.transcribeAbort，界面有「停止转写」' },
  { file: 'api/stt-settings.ts', path: '/api/stt/probe', state: 'plumbed', surface: 'STT 试转',
    reason: 'signal 已透传，SettingsSTT 的 running 态没有停止入口' },
  { file: 'api/stt-settings.ts', path: '/api/stt/transcribe-incremental', state: 'by-design', surface: '录音分片增量转写',
    reason: '刻意不接：note-transcription-cancellable 那条不变量把「停止转写」按钮限定在 '
      + "phase==='stopping'，即**录完之后**。录音进行中给中止入口等于允许用户停掉自己正在录的音，"
      + '是错的语义；要接必须先改那条不变量。' },
  { file: 'api/stt.ts', path: '/api/stt/transcribe', state: 'wired', surface: '单段语音转写',
    reason: '会议分片走 MeetingRecorderRuntime.cancelSegmentTranscription；语音输入那条刻意'
      + '跨页存活、由 orphan 交付，不提供中止' },
]

function readSrc(rel) {
  return fs.readFileSync(path.join(SRC, rel), 'utf8')
}

function matchBracket(src, open, a, b) {
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === a) depth++
    else if (src[i] === b) { depth--; if (depth === 0) return i }
  }
  return -1
}

/**
 * 把 `const X = <数字表达式>` 求值成毫秒数；解不出返回 null。
 * 认两种写法：`90_000` 与 `17 * 60_000`。只认纯数字会漏掉邮件那 4 个
 * （10/16/17/3 分钟）——恰好是全表最长的那几个，是最该被登记的。
 */
export function evalBudgetMs(expr) {
  const norm = expr.replace(/_/g, '').trim()
  if (/^\d+$/.test(norm)) return Number(norm)
  const m = /^(\d+)\s*\*\s*(\d+)$/.exec(norm)
  if (m) return Number(m[1]) * Number(m[2])
  return null
}

/** 文件里声明的全部预算常量：名字 → 毫秒。 */
export function budgetConsts(src) {
  const out = {}
  for (const m of src.matchAll(/(?:export\s+)?const\s+(\w+)\s*(?::[^=]+?)?=\s*([^;\n]+)/g)) {
    const ms = evalBudgetMs(m[2])
    if (ms !== null) out[m[1]] = ms
  }
  return out
}

/**
 * 全 api/ 目录的预算常量表（同名以先声明者为准，冲突直接抛错）。
 *
 * 必须跨文件：`LONG_REQUEST_TIMEOUT_MS` 定义在 http.ts，被 email.ts / stt-settings.ts
 * import。只看本文件的常量表时，那 5 个调用点会**静默漏掉**——判据不报红，
 * 只是少看了 5 处（其中 4 处恰恰是本轮刚补的 signal）。这与"漏扫乘法写法"
 * 是同一类失效：不是错，是看不见。
 */
export function globalBudgetConsts(dir = path.join(SRC, 'api')) {
  const out = {}
  const where = {}
  for (const name of fs.readdirSync(dir).filter((n) => n.endsWith('.ts'))) {
    for (const [k, v] of Object.entries(budgetConsts(fs.readFileSync(path.join(dir, name), 'utf8')))) {
      if (k in out && out[k] !== v) {
        throw new Error(`预算常量 ${k} 在 ${where[k]} 与 api/${name} 上取值不同（${out[k]} vs ${v}）`)
      }
      out[k] = v
      where[k] = `api/${name}`
    }
  }
  return out
}

/**
 * 枚举文件里所有 http 调用（含跨行、含嵌套泛型）。
 *
 * 三个坑都是实测踩出来的，实现里都留了原因：
 *  ① 不能用 `lastIndexOf('http', p)` 往前找调用起点——文件顶部的
 *     `import { http } from './http'` 里也含 "http"，会命中它，然后配平出一个
 *     与目标调用毫无关系的括号范围。
 *  ② 不能只用 `indexOf(route)` 找路径——路径之间有前缀关系
 *     （`/api/stt/transcribe` 是 `/api/stt/transcribe-full` 的前缀）。
 *     所以比对时要求**带引号的完整字面量**。
 *  ③ 泛型不能用 `http(?:<[^<>()]*>)?\s*\(` 匹配——`http<Record<string, unknown>>(`
 *     是**嵌套**泛型（外层 `<>` 里还套着 `<string, unknown>`），`[^<>()]*` 顶不住，
 *     结果这一整类调用（会议那两个、stt 那一个）全部扫不到：判据不是报红，
 *     而是**静默漏看**。所以这里改成从 `\bhttp\b` 出发按尖括号深度前扫。
 */
export function httpCalls(src) {
  const out = []
  for (const m of src.matchAll(/\bhttp\b/g)) {
    let i = m.index + 4
    while (src[i] === ' ' || src[i] === '\t' || src[i] === '\n' || src[i] === '\r') i++
    if (src[i] === '<') {
      let depth = 0
      for (; i < src.length; i++) {
        if (src[i] === '<') depth++
        else if (src[i] === '>') { depth--; if (depth === 0) { i++; break } }
      }
      while (src[i] === ' ' || src[i] === '\t' || src[i] === '\n' || src[i] === '\r') i++
    }
    if (src[i] !== '(') continue
    const end = matchBracket(src, i, '(', ')')
    if (end < 0) continue
    out.push({ start: m.index, text: src.slice(m.index, end + 1) })
  }
  return out
}

/** 路径是否以完整字面量（带引号/反引号）在这次调用里出现。 */
function hasRouteLiteral(callText, route) {
  return callText.includes(`'${route}'`) || callText.includes('`' + route + '`')
}

export function httpCallAt(src, route) {
  return httpCalls(src).find((c) => hasRouteLiteral(c.text, route)) ?? null
}

/** 这些词后面跟 `(` 也不是函数声明——认错会把 `if (…)` 当成调用点所在的函数。 */
const NOT_A_FN = new Set(['if', 'for', 'while', 'switch', 'catch', 'return', 'await', 'typeof', 'do', 'else', 'function'])

/** 调用点所在函数的「签名 + 参数表」，用于查 signal 形参。 */
export function enclosingFn(src, callStart) {
  const re = /(?:^|\n)[ \t]*(?:async\s+)?(\w+)\s*(?:<[^<>()]*>)?\s*\(/g
  let best = null
  for (let m = re.exec(src); m && m.index < callStart; m = re.exec(src)) {
    if (NOT_A_FN.has(m[1])) continue
    best = { name: m[1], declIndex: m.index, paramOpen: m.index + m[0].length - 1 }
  }
  if (!best) return null
  const paramClose = matchBracket(src, best.paramOpen, '(', ')')
  return { ...best, params: src.slice(best.paramOpen, paramClose + 1) }
}

/**
 * 扫出 api/ 下全部 >= 阈值的 http 调用点。
 *
 * `opts.localConstsOf` 只作用于**本文件**声明的常量；跨文件的那张表默认由
 * globalBudgetConsts 建好，负控只换掉 localConstsOf——这样负控量化的才是
 * 「本文件里 `N * 60_000` 写法被漏掉」这一个变量，不会顺带把跨文件解析也打掉
 * （第一版就是这里把负控做成了两个变量同时变，得到的差集无法解释）。
 */
export function discoverLongCallSites(dir = path.join(SRC, 'api'), opts = {}) {
  const localConstsOf = opts.localConstsOf ?? budgetConsts
  const globals = opts.globalConsts ?? globalBudgetConsts(dir)
  const found = []
  for (const name of fs.readdirSync(dir).filter((n) => n.endsWith('.ts'))) {
    const src = fs.readFileSync(path.join(dir, name), 'utf8')
    const local = localConstsOf(src)
    for (const call of httpCalls(src)) {
      const t = call.text.match(/timeoutMs:\s*(\w+)/)
      if (!t) continue
      const ms = local[t[1]] ?? globals[t[1]]
      if (ms === undefined || ms < LONG_TASK_FLOOR_MS) continue
      const route = call.text.match(/['`](\/api\/[^'`$]*(?:\$\{[^`']*\})?[^'`$]*)['`]/)
      if (!route) continue
      found.push({ file: `api/${name}`, budget: t[1], path: route[1], ms })
    }
  }
  return found
}

/** 判据自己出 bug 时（TS 语法让 new Function 解析失败）绝不能冒充负控通过。 */
function loadPredicate(srcText) {
  let js = srcText
    .replace(/export function isAbortError\(e: unknown\): boolean \{/, 'function isAbortError(e) {')
    .replace(/\(e as \{ name\?: unknown \}\)/g, '(e)')
  if (js === srcText) throw new Error('abort.ts 的形态变了，负控注入需要同步更新（TS 剥离没命中）')
  if (/: unknown|: boolean/.test(js)) throw new Error('abort.ts 里还有没剥掉的 TS 标注，new Function 会解析失败')
  // eslint-disable-next-line no-new-func
  return new Function(`${js}; return isAbortError`)()
}

describe('中止判据本身（先确认判据没坏，再谈性质）', () => {
  const foreign = (() => { const e = new Error('The operation was aborted'); e.name = 'AbortError'; return e })()

  it('认 DOMException 的 AbortError', () => {
    assert.equal(isAbortError(new DOMException('Aborted', 'AbortError')), true)
  })

  it('认跨 realm 的 AbortError（instanceof DOMException 在这里会漏判）', () => {
    // 构造器不是本域 DOMException 的那一种。Capacitor WebView 里 fetch 抛的
    // 就是这个形态；漏判 = 用户取消被当成失败 = 走降级链写进库里。
    assert.equal(foreign instanceof DOMException, false, '样本前提不成立：本样本并非跨 realm')
    assert.equal(isAbortError(foreign), true)
  })

  it('不把超时当成中止（超时是失败，上层要提示/降级）', () => {
    const t = new Error('请求超时')
    t.name = 'TimeoutError'
    assert.equal(isAbortError(t), false)
  })

  it('不把 ApiError / 裸 Error / 空值当成中止', () => {
    for (const e of [Object.assign(new Error('x'), { name: 'ApiError' }), new Error('boom')]) {
      assert.equal(isAbortError(e), false)
    }
    for (const v of [null, undefined, 0, '', 'AbortError', {}]) {
      assert.equal(isAbortError(v), false, `${JSON.stringify(v)} 不该被当成中止`)
    }
  })
})

describe('长任务调用点必须逐个在册（新增而不登记就转红）', () => {
  const discovered = discoverLongCallSites()
  const key = (x) => `${x.file}::${x.path}`
  const seen = discovered.map(key).sort()
  const registered = LONG_TASK_CALL_SITES.map(key).sort()

  it('源码里的长任务调用点与登记表一一对应', () => {
    assert.deepEqual(
      seen, registered,
      '长任务调用点清单与登记表对不上。\n'
      + `源码扫到：\n  ${seen.join('\n  ')}\n登记表：\n  ${registered.join('\n  ')}\n`
      + '新增一个 >= 90 秒的调用点时，请连同中止状态一起登记进 LONG_TASK_CALL_SITES。',
    )
  })

  it('登记表本身没有重复键（重复行会让「一一对应」失去意义）', () => {
    assert.equal(new Set(registered).size, registered.length, '登记表里有重复的 (file, path)')
  })

  it('这张表不是空的、也没有把缺口全涂掉', () => {
    assert.ok(LONG_TASK_CALL_SITES.length >= 15, '登记表条目异常少，判据可能没扫到东西')
    assert.ok(
      LONG_TASK_CALL_SITES.some((r) => r.state === 'plumbed'),
      '登记表里一条 plumbed 都没有 —— 那说明缺口已全补完，本表该删掉而不是空转',
    )
    assert.ok(
      LONG_TASK_CALL_SITES.some((r) => r.state === 'wired'),
      '登记表里一条 wired 都没有 —— 判据没锁住任何已修好的东西',
    )
  })

  it('每一条都写清了 surface / state / reason', () => {
    for (const r of LONG_TASK_CALL_SITES) {
      assert.ok(r.surface && r.surface.trim(), `${r.path} 没写 surface（用户看不到这层差异）`)
      assert.ok(['wired', 'plumbed', 'by-design'].includes(r.state), `${r.path} 的 state="${r.state}" 不是三态之一`)
      assert.ok(r.reason && r.reason.trim().length > 10, `${r.path} 没写 reason（"以后再说"不是理由）`)
    }
  })

  it('by-design 必须真的解释为什么（否则它只是不想做）', () => {
    const byDesign = LONG_TASK_CALL_SITES.filter((x) => x.state === 'by-design')
    assert.ok(byDesign.length > 0, '没有 by-design 条目，那这条断言就是空转')
    for (const r of byDesign) {
      assert.match(r.reason, /刻意|因为|只能|等于/, `${r.path} 标成 by-design 但没给出设计层面的理由`)
    }
  })
})

describe('signal 真的进了 http 选项（不是只写在签名里）', () => {
  for (const site of LONG_TASK_CALL_SITES.filter((s) => s.state !== 'by-design')) {
    it(`${site.file} ${site.path} 带了 signal，且所在函数签名有 signal 形参`, () => {
      const src = readSrc(site.file)
      const call = httpCallAt(src, site.path)
      assert.notEqual(call, null, `${site.file} 里找不到 ${site.path} 的 http 调用（判据可能已失效）`)
      assert.match(
        call.text,
        /(^|[^.\w])signal\s*[,}]/m,
        `${site.path} 的请求选项里没有 signal —— 预算这么久却停不掉，`
        + '用户只能干等到超时',
      )
      const fn = enclosingFn(src, call.start)
      assert.notEqual(fn, null, `找不到 ${site.path} 所在的函数`)
      assert.match(
        fn.params,
        /signal\?:\s*AbortSignal/,
        `${site.path} 所在的 ${fn.name}() 签名里没有 signal 形参，调用方根本没法传`,
      )
    })
  }

  it('isAbortError 从 http 转发出去（调用方统一从 ./http 取）', () => {
    const http = readSrc('api/http.ts')
    assert.match(http, /export \{ isAbortError \}/, 'http.ts 不再转发 isAbortError')
    assert.match(http, /import \{ isAbortError \} from '\.\/abort'/, 'http.ts 没有真去 import 它')
  })
})

describe('中止不得被降级链吞成「假成功」', () => {
  const meetings = readSrc('api/meetings.ts')

  for (const decl of ['async summarize(', 'async refine(']) {
    it(`${decl} 的 catch 先放行中止，再走 fallback`, () => {
      const at = meetings.indexOf(decl)
      assert.notEqual(at, -1, `meetings.ts 里找不到 ${decl}`)
      const body = meetings.slice(at, meetings.indexOf('\n  },', at))
      assert.match(
        body,
        /catch\s*\(e\)[\s\S]*if \(isAbortError\(e\)\) throw e/,
        `${decl} 的 catch 没有放行中止：用户取消会被换成一份降级结果写进会议`,
      )
    })
  }

  for (const decl of ['async function fallbackSummarize(', 'async function fallbackRefine(']) {
    it(`${decl} 自己也会被中止（降级途中取消也不能产出内容）`, () => {
      const at = meetings.indexOf(decl)
      assert.notEqual(at, -1, `meetings.ts 里找不到 ${decl}`)
      const body = meetings.slice(at, at + 2500)
      assert.match(body, /if \(isAbortError\(e\)\) throw e/, `${decl} 会把中止吞掉并返回拼装内容`)
      assert.match(body, /\bsignal\b/, `${decl} 的兜底请求没有接 signal，中止传不进去`)
    })
  }

  it('调用方把中止与失败分开说（取消不是「稍后重试」）', () => {
    const live = readSrc('composables/useLiveSummary.ts')
    assert.match(live, /if \(isAbortError\(e\)\) return/, 'useLiveSummary 会把中止记成 update failed')
    const session = readSrc('features/sessions/useSessionLiveRecord.ts')
    assert.match(session, /if \(isAbortError\(e\)\)[\s\S]*已停止精翻/, 'useSessionLiveRecord.stop 会把取消提示成「稍后重试」')
  })
})

describe('会议分片转写：此前这一层完全停不掉', () => {
  const runtime = readSrc('native/recordingRuntime.ts')
  const ingest = readSrc('features/meetings/ingest-speech.ts')
  const cancelBody = (() => {
    const at = runtime.indexOf('cancelSegmentTranscription(): boolean {')
    if (at < 0) return null
    const open = runtime.indexOf('{', at)
    return runtime.slice(open, matchBracket(runtime, open, '{', '}') + 1)
  })()

  it('分片转写把 signal 传到了 sttApi.transcribe', () => {
    assert.match(ingest, /signal\?:\s*AbortSignal/, 'ingestSpeechBlob 的入参没有 signal')
    assert.match(
      ingest,
      /sttApi\.transcribe\(\{ audioBlob: opts\.blob \}, opts\.signal\)/,
      'signal 没有传到 sttApi.transcribe —— 分片转写仍然是停不掉的那一环',
    )
  })

  it('runtime 持有一个覆盖整场录音的分片中止器', () => {
    assert.match(runtime, /private segmentAbort: AbortController \| null = null/)
    assert.match(runtime, /if \(this\.segmentAbort\) this\.segmentAbort\.abort\(\)/,
      '换场录音没有中止上一场遗留的分片：它们会白烧配额，还会被写进新会议')
    assert.match(runtime, /signal: this\.segmentAbort\?\.signal/,
      'processSegment 没有把分片中止器交给 ingestSpeechBlob')
  })

  it('cancelSegmentTranscription 真的 abort，且没有在途时如实返回 false', () => {
    assert.notEqual(cancelBody, null, '找不到 cancelSegmentTranscription')
    assert.match(cancelBody, /\.abort\(\)/, '没有真的 abort')
    // 只认**条件表达式本身**。此前这里查的是 cancelBody.includes('processingCount')，
    // 而方法里的注释恰好也写了 processingCount —— 判据被注释喂饱，负控怎么
    // 改实现都红不了（这正是注释里记的那类失效）。
    assert.match(
      cancelBody,
      /this\.processingCount\.value === 0/,
      '没有用「真的在途」判定：两场录音之间 segmentAbort 一直留着，会在没有'
      + '任何请求可停时返回 true —— 那又是一个假成功',
    )
    assert.match(cancelBody, /return false/, '没有在途时必须如实返回 false')
  })

  it('分片转写被中止时不弹「转写失败」', () => {
    const at = runtime.indexOf('await ingestSpeechBlob({')
    assert.notEqual(at, -1, '找不到 processSegment 里的 ingestSpeechBlob 调用')
    assert.match(
      runtime.slice(at, at + 1200),
      /if \(this\.segmentAbort\?\.signal\.aborted\) return/,
      '用户中止被当成转写失败提示，会让他以为录音坏了',
    )
  })
})

// ── 负控 ────────────────────────────────────────────────────────────────
// 全部要求**断言性失败**。build/parse 失败（注入出一个语法错的 .ts 让 vue-tsc
// 报红、或 new Function 抛 SyntaxError）不算护栏转红——那是坏注入，不是判据
// 生效。所以 loadPredicate 在剥离 TS 标注失败时**直接抛错**而不是静默通过。

describe('判据自检：负控必须转红', () => {
  const abortSrc = readSrc('api/abort.ts')

  it('isAbortError 退回 instanceof 判据 → 跨 realm 那条转红', () => {
    // 替换**整条 return 表达式**，不能只替换 `.name === 'AbortError'`：
    // 那样会把 `.name` 吃掉，拼出 `(e as {...})['AbortError', ...]` 这种属性
    // 访问，运行时抛 "Cannot read properties of undefined" —— 那是坏注入，
    // 不是判据生效（第一版就栽在这里）。
    const broken = abortSrc.replace(
      /return !!e && typeof e === 'object' && \(e as \{ name\?: unknown \}\)\.name === 'AbortError'/,
      "return e instanceof DOMException && e.name === 'AbortError'",
    )
    assert.notEqual(broken, abortSrc, '负控样本没有真的替换掉判据（锚点没命中）')
    const fn = loadPredicate(broken)

    const foreign = new Error('The operation was aborted')
    foreign.name = 'AbortError'
    assert.equal(foreign instanceof DOMException, false, '样本前提不成立')
    // 判据「认跨 realm」这一条的形态就是 `=== true`；样本里它是 false。
    assert.equal(fn(foreign), false, '负控本该转红却判成了通过 —— 判据分不出跨 realm')
    // 真实现必须仍然通过（证明上面那个 false 是负控造成的）
    assert.equal(isAbortError(foreign), true, '真实实现居然也不认跨 realm，判据已失效')
  })

  it('isAbortError 把超时也算成中止 → 超时那条转红', () => {
    const broken = abortSrc.replace(
      /return !!e && typeof e === 'object' && \(e as \{ name\?: unknown \}\)\.name === 'AbortError'/,
      "return !!e && typeof e === 'object'"
        + " && ['AbortError', 'TimeoutError'].includes(String((e as { name?: unknown }).name))",
    )
    assert.notEqual(broken, abortSrc, '负控样本没有真的改掉判据（锚点没命中）')
    const fn = loadPredicate(broken)
    const t = new Error('请求超时')
    t.name = 'TimeoutError'
    // 判据「不把超时当成中止」的形态就是 `=== false`；样本里它是 true。
    assert.equal(fn(t), true, '负控本该转红却判成了通过')
    assert.equal(isAbortError(t), false, '真实实现居然把超时当中止，判据已失效')
  })

  it('injection 形态变了就抛错，绝不静默变成 no-op（负控假通过的最常见成因）', () => {
    assert.throws(
      () => loadPredicate('export function somethingElse(x) { return x }'),
      /负控注入需要同步更新|没剥掉/,
      '注入点找不到时必须失败；静默返回就等于负控是 no-op',
    )
  })

  it('给源码塞一个未登记的长任务调用点 → 在册性判据转红', () => {
    const injected = [
      ...discoverLongCallSites(),
      { file: 'api/email.ts', path: '/api/emails/brand-new', budget: 'X', ms: 300_000 },
    ]
    assert.notDeepEqual(
      injected.map((x) => `${x.file}::${x.path}`).sort(),
      LONG_TASK_CALL_SITES.map((x) => `${x.file}::${x.path}`).sort(),
      '负控本该转红却判成了通过 —— 新增长任务可以不登记就混过去',
    )
  })

  it('evalBudgetMs 去掉乘法分支 → 4 个最长的预算被静默漏扫（判据自身的盲区）', () => {
    // 这一条最危险：不是红，而是**变瞎**。邮件那 4 个（10/16/17/3 分钟）全靠
    // `N * 60_000` 写法，只认纯数字就会全部漏掉——而它们恰好是全表最长的。
    // 所以这里量化的不是「函数返回值变了」，而是**后果**：在册性判据会少看
    // 4 个调用点，而且不会报红。
    const blindConsts = (src) => {
      const out = {}
      for (const m of src.matchAll(/(?:export\s+)?const\s+(\w+)\s*(?::[^=]+?)?=\s*([^;\n]+)/g)) {
        const norm = m[2].replace(/_/g, '').trim()
        if (/^\d+$/.test(norm)) out[m[1]] = Number(norm)
      }
      return out
    }
    assert.equal(evalBudgetMs('17 * 60_000'), 1_020_000, '真实实现应认得出乘法写法')

    // 本地表与全目录表都要换成瞎版：只换其一会让常量从另一条路被解析出来，
    // 漏扫就消失了——那等于负控什么也没证明。
    const apiDir = path.join(SRC, 'api')
    const blindGlobals = {}
    for (const n of fs.readdirSync(apiDir).filter((x) => x.endsWith('.ts'))) {
      Object.assign(blindGlobals, blindConsts(fs.readFileSync(path.join(apiDir, n), 'utf8')))
    }
    const real = discoverLongCallSites()
    const blind = discoverLongCallSites(apiDir, { localConstsOf: blindConsts, globalConsts: blindGlobals })
    const blindKeys = blind.map((x) => `${x.file}::${x.path}`).sort()
    const realKeys = real.map((x) => `${x.file}::${x.path}`).sort()
    const missed = realKeys.filter((k) => !blindKeys.includes(k))

    // 期望漏掉哪些，自己从源码算，别写死个数——写死的话源码一改这条就成了
    // 「前提变了」的红，而它本来要防的是漏扫。
    const mulBudgets = new Set()
    for (const name of fs.readdirSync(path.join(SRC, 'api')).filter((n) => n.endsWith('.ts'))) {
      const src = fs.readFileSync(path.join(SRC, 'api', name), 'utf8')
      for (const m of src.matchAll(/(?:export\s+)?const\s+(\w+)\s*(?::[^=]+?)?=\s*([^;\n]+)/g)) {
        if (/\*/.test(m[2]) && evalBudgetMs(m[2]) !== null && evalBudgetMs(m[2]) >= LONG_TASK_FLOOR_MS) {
          mulBudgets.add(m[1])
        }
      }
    }
    const expected = real
      .filter((x) => mulBudgets.has(x.budget))
      .map((x) => `${x.file}::${x.path}`).sort()

    assert.ok(expected.length >= 7, `样本前提变了：乘法写法的长预算只剩 ${expected.length} 个`)
    assert.deepEqual(
      missed, expected,
      '漏扫集合与「所有乘法写法的长任务」不一致 —— 负控样本或 evalBudgetMs 的行为变了',
    )
    // 关键：这 4 个漏掉后，剩下的集合与登记表的差集**依然存在**——也就是说
    // 判据不会因为漏扫而变红，它只是悄悄少看了 4 个调用点。这才是要防的失效。
    assert.ok(
      blindKeys.length < realKeys.length,
      '漏扫版本反而看得更多，负控前提不成立',
    )
  })

  it('summarize 的 catch 不放行中止 → 降级链判据转红', () => {
    const meetings = readSrc('api/meetings.ts')
    const at = meetings.indexOf('async summarize(')
    const body = meetings.slice(at, meetings.indexOf('\n  },', at))
    const broken = body.replace('if (isAbortError(e)) throw e', 'void e')
    assert.notEqual(broken, body, '负控样本没有真的摘掉放行（替换没命中）')
    assert.equal(
      /catch\s*\(e\)[\s\S]*if \(isAbortError\(e\)\) throw e/.test(broken),
      false,
      '负控本该转红却判成了通过',
    )
  })

  it('cancelSegmentTranscription 去掉在途判定 → 假成功判据转红', () => {
    const runtime = readSrc('native/recordingRuntime.ts')
    const at = runtime.indexOf('cancelSegmentTranscription(): boolean {')
    const open = runtime.indexOf('{', at)
    const body = runtime.slice(open, matchBracket(runtime, open, '{', '}') + 1)
    const brokenBody = body.replace(/\s*\|\|\s*this\.processingCount\.value === 0/, '')
    assert.notEqual(brokenBody, body, '负控样本没有真的摘掉在途判定（替换没命中）')
    assert.equal(
      /this\.processingCount\.value === 0/.test(brokenBody),
      false,
      '负控本该转红却判成了通过',
    )
    // 反向自检：注释里那个 processingCount 必须**不能**让判据复活——
    // 也就是判据不能写成 includes('processingCount')。
    assert.match(body, /用 processingCount 判定/, '前提变了：方法里已没有那段注释')
    assert.equal(
      brokenBody.includes('processingCount'),
      true,
      '注释里仍含 processingCount —— 这正是判据必须查条件表达式而非 includes 的原因',
    )
  })
})
