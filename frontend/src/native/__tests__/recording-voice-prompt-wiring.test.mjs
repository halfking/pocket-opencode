// recording-voice-prompt-wiring.test.mjs — §55 的**接线门**（另一半）。
//
// 已有门（recording-voice-prompt-default.test.mjs）覆盖的是：
//   · 纯解析 `voicePromptEnabledByDefault(stored)` 的默认值方向
//   · `RecordingVoicePrompt` 在 muted 时确实不播报
// ★ 两者都**看不见接线**。全仓 `isVoicePromptEnabled` / `setVoicePromptEnabled`
//   只出现在定义处与设置页，**没有任何断言说这两段是连着的**。
//
// 这道门盯的正是那一段，接的是三件会静默回归的事：
//   A. **极性写反**：`setMuted(...)` 的实参若不取反，「关闭」会变成「播报」。
//      ← 纯解析门完全看不见：解析函数照样全绿，只有行为反了。
//   B. **创建分支不再套用设置**：`voicePrompt()` 里那一行若被删，
//      播报单例永远不静音 ⇒ 用户要的「不要语音提示」无声失效，且没有任何门变红。
//   C. **设置页开关是装饰品**：writer 若被换成写死 `true`，
//      UI 上点「关」照样回显「开」，而运行时读的是另一个值。
//
// ⚠ 为什么用源码断言而不用行为门：本仓没有 vitest/tsx/esmock（§66.7 实测），
//   `recordingRuntime.ts` 顶层 import Capacitor，node --test 里 import 不进来，
//   也就没法 mock 掉 localStorage 去跑真函数。仓库既有惯例同此
//   （见 refine-consumers-live.test.ts 的 `read` + `strip`）。
//   ⇒ 因此**每条判据都必须配负控**：证明它抓得住、不是恒真。
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..') // → frontend/src
const read = (rel) => fs.readFileSync(path.resolve(SRC, rel), 'utf8')

/** 剥注释但**保持字符位置**（等长空格）。源码判据一律走它：
 *  注释里写一句「setMuted(!isVoicePromptEnabled())」就足以让存在性判据假命中。 */
const blank = (m) => m.replace(/[^\n]/g, ' ')
const strip = (t) => t
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

/** 取 `function <name>(` 的函数体（按大括号配平，字符串/正则里的括号不计）。 */
function functionBody(src, name) {
  const at = src.indexOf(`function ${name}(`)
  assert.ok(at >= 0, `源码里找不到 function ${name}(`)
  const open = src.indexOf('{', at)
  assert.ok(open > 0, `${name} 没有函数体`)
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--
      if (depth === 0) return src.slice(open + 1, i)
    }
  }
  assert.fail(`${name} 的函数体没有闭合`)
}

/** 取出 `callee(` 的实参原文（按括号配平，支持嵌套）。 */
function argsOf(src, callee, from = 0) {
  const at = src.indexOf(`${callee}(`, from)
  if (at < 0) return null
  const open = at + callee.length
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '(') depth++
    else if (src[i] === ')') {
      depth--
      if (depth === 0) return src.slice(open + 1, i).trim()
    }
  }
  return null
}

const runtime = strip(read('native/recordingRuntime.ts'))
const settings = strip(read('features/settings/SettingsSTT.vue'))

/** `voicePrompt()` 的**创建分支**里那一次 setMuted 的实参。 */
function creationBranchMuteArg() {
  const body = functionBody(runtime, 'voicePrompt')
  const first = body.indexOf('setMuted(')
  assert.ok(first > 0, 'voicePrompt() 里找不到 setMuted 调用')
  // 必须在「if (!g[VOICE_PROMPT_KEY]) { … }」这个创建分支内
  const guardAt = body.indexOf('if (!g[')
  assert.ok(guardAt >= 0 && guardAt < first, 'setMuted 不在创建分支内')
  return argsOf(body, 'setMuted', guardAt)
}

describe('A：静音实参的**极性**必须与「开启」相反', () => {
  it('创建分支传入的是「取反后的开启态」', () => {
    const arg = creationBranchMuteArg()
    assert.match(
      arg,
      /^!\s*[A-Za-z_$][\w$]*\s*\(\s*\)$/,
      `创建分支的静音实参不是「某个无参函数的取反」，实参=${JSON.stringify(arg)}\n`
      + '⇒ 极性可能写反了：「关闭」会变成「播报」，而纯解析门看不见这件事',
    )
  })

  it('★ 取反的那个函数必须真的去读持久化状态，不能是个常量', () => {
    const arg = creationBranchMuteArg()
    const name = arg.replace(/^!\s*/, '').replace(/\s*\(\s*\)$/, '')
    const body = functionBody(runtime, name)
    assert.match(
      body,
      /localStorage[\s\S]{0,80}?getItem/,
      `${name}() 里没有读 localStorage ⇒ 它不读设置 ⇒ 静音与否与用户开关无关`,
    )
  })

  it('负控：极性写反的实现必须被本组抓到（合成样本，不依赖活代码）', () => {
    const inverted = argsOf('function f(){\n  g.setMuted(isVoicePromptEnabled())\n}', 'setMuted')
    assert.ok(
      !/^!\s*[A-Za-z_$][\w$]*\s*\(\s*\)$/.test(inverted),
      '负控样本构造错了：不取反的形态不该通过极性判据',
    )
  })
})

describe('B：创建分支必须真的套用设置（删掉就静默失效）', () => {
  it('voicePrompt() 的创建分支里有且只有一次 setMuted', () => {
    const body = functionBody(runtime, 'voicePrompt')
    const n = (body.match(/setMuted\s*\(/g) ?? []).length
    assert.equal(
      n, 1,
      `voicePrompt() 里 setMuted 出现 ${n} 次，期望 1 次。\n`
      + '· 0 次 ⇒ 播报单例永不安静 ⇒ 用户要的「不播报」无声失效\n'
      + '· 多次 ⇒ 每次 announce 都在重设静音，而 setMuted(true) 会 clear() 丢队列\n'
      + '（每次重设会丢弃排队中的播报，那时「设置生效」与「队列被清空」就分不开了）',
    )
  })

  it('负控：删掉那一行的形态必须被本组抓到（合成样本）', () => {
    const body = functionBody('function f(){\n  if (!g[k]) {\n    g[k] = new P()\n  }\n  return g[k]\n}', 'f')
    const n = (body.match(/setMuted\s*\(/g) ?? []).length
    assert.equal(n, 0, '负控样本构造错了：删掉调用的形态不该通过计数')
  })
})

describe('C：设置页开关必须真的写穿（不是装饰品）', () => {
  it('设置页同时引入「读」与「写」两个入口', () => {
    const names = [...settings.matchAll(/import\s*\{([^}]*)\}\s*from\s*'[^']*recordingRuntime'/g)]
      .flatMap((m) => m[1].split(',').map((s) => s.trim()).filter(Boolean))
    assert.ok(names.length >= 2,
      `设置页只从 recordingRuntime 引入了 ${names.length} 个符号（${names.join(',') || '无'}）\n`
      + '⇒ 开关要么读不到当前状态、要么写不回去，是个装饰品')
  })

  it('★ 写入口拿到的是「用户这次选的值」，不是写死的常量', () => {
    const writer = settings.match(/([A-Za-z_$][\w$]*)\s*\(\s*(on|enabled|next|v)\s*\)/)
    assert.ok(writer,
      '设置页里找不到「把新值传给写入口」的调用 ⇒ 开关点不动')
    const arg = writer[2]
    assert.ok(
      !/^(true|false|'1'|'0'|"1"|"0")$/.test(arg),
      `写入口被写死成 ${arg} ⇒ 点「关」也会写成开启，而 UI 照样回显「开」`,
    )
  })

  it('负控：写死常量的形态必须被本组抓到（合成样本）', () => {
    const hard = "async function toggle(on: boolean){\n  setVoicePromptEnabled(true)\n}"
    const m = hard.match(/([A-Za-z_$][\w$]*)\s*\(\s*(on|enabled|next|v)\s*\)/)
    assert.equal(m, null, '负控样本构造错了：写死常量不该匹配到「传入新值」')
  })
})