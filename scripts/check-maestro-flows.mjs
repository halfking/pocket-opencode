// 校验新增的 Maestro 流：YAML 合法 + 命令词表在 Maestro 2.11 已知集合内。
// 目的：设备离线跑不了流时，至少保证「不是语法错 / 不是杜撰的命令名」，
// 否则下次真机上线时会把「流写错了」误判成「功能有问题」。
import { readFileSync, existsSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
import { createRequire } from 'node:module'
const require2 = createRequire(join(ROOT, 'frontend', 'package.json'))
const { parseAllDocuments } = require2('yaml')

const FLOWS = JSON.parse(readFileSync(join(ROOT, 'scripts', '.maestro-flows.json'), 'utf8'))

// Maestro 2.11 的顶层命令/子命令。runFlow 是容器，展开后仍要校验内部。
const KNOWN = new Set([
  'launchApp', 'stopApp', 'killApp', 'clearState', 'openLink', 'back', 'hideKeyboard',
  'tapOn', 'longPressOn', 'doubleTapOn', 'inputText', 'inputRandomText', 'inputRandomNumber',
  'eraseText', 'pressKey', 'swipe', 'scroll', 'scrollUntilVisible', 'copyTextFrom',
  'assertVisible', 'assertNotVisible', 'assertTrue', 'extendedWaitUntil', 'waitForAnimationToEnd',
  'runFlow', 'runScript', 'evalScript', 'repeat', 'retry', 'travel', 'setLocation',
  'takeScreenshot', 'startRecording', 'stopRecording', 'setAirplaneMode',
  'toggleAirplaneMode', 'extendedWaitUntil', 'assertWithAI', 'pasteText', 'inputRandomPersonName',
])

let bad = 0
for (const f of FLOWS) {
  // 流文件不存在要报成一条干净的 FAIL，而不是甩一个 ENOENT 栈出来：
  // 后者会让人以为检查器坏了，而不是「配置里写了条不存在的流」。
  if (!existsSync(join(ROOT, f))) {
    console.log(`\n== ${f} ==`)
    console.log(`  FAIL 流文件不存在（scripts/.maestro-flows.json 里列了它）`)
    bad++
    continue
  }
  const src = readFileSync(join(ROOT, f), 'utf8')
  let doc, header
  try {
    // Maestro 用 `---` 分成两个 YAML 文档：头部（appId）与命令列表。
    const docs = parseAllDocuments(src, { logLevel: 'error' })
    for (const d of docs) {
      if (d.errors.length) throw new Error(d.errors[0].message)
    }
    header = docs[0].toJS() || {}
    doc = docs[docs.length - 1].toJS() || []
  } catch (e) {
    console.log(`YAML PARSE FAIL ${f}: ${e.message}`)
    bad++
    continue
  }
  const appId = header.appId ?? doc.appId
  const cmds = Array.isArray(doc) ? doc.filter((c) => typeof c === 'object' && c !== null) : []
  console.log(`\n== ${f} ==`)
  console.log(`  appId: ${appId}`)
  console.log(`  文档数: 2（头部 + 命令列表）  命令数: ${cmds.length}`)

  const unknown = []
  const walk = (list, depth) => {
    for (const c of list) {
      if (typeof c !== 'object' || c === null) continue
      const name = Object.keys(c)[0]
      if (!name) continue
      if (name === 'commands' || name === 'visible' || name === 'notVisible') continue
      if (!KNOWN.has(name)) unknown.push(`${'  '.repeat(depth + 1)}${name}`)
      // runFlow 内嵌 commands
      const inner = c[name]
      if (Array.isArray(inner?.commands)) walk(inner.commands, depth + 1)
      if (Array.isArray(inner?.when?.commands)) walk(inner.when.commands, depth + 1)
    }
  }
  walk(cmds, 0)
  if (unknown.length) {
    console.log(`  未识别的命令（可能是拼错或版本不支持）:`)
    unknown.forEach((u) => console.log(u))
    bad++
  } else {
    console.log(`  命令词表：全部命中已知集合`)
  }

  // 结构性检查：appId 必须是并存包；断言里不能有 emoji（Java 正则会炸）
  if (String(appId) !== 'com.kaixuan.opencode.pocket.sttdev') {
    // 必须打印 appId 而不是 doc.appId：命令列表是数组，doc.appId 恒为
    // undefined，报错里出现 undefined 会让人误判成「解析失败」而不是
    // 「appId 写错了」。（2026-10-01 实测踩过：负控里报 undefined，
    // 差点当成检查器坏了。）
    console.log(`  FAIL appId 应为 sttdev 并存包，实际 ${appId}`)
    bad++
  }
  const emoji = /[\u{1F300}-\u{1FAFF}\u{2705}\u{270E}\u{1F5D1}]/u
  const srcEmoji = src.match(emoji)
  const cmdEmoji = JSON.stringify(cmds).match(emoji)
  if (cmdEmoji) {
    console.log(`  FAIL 命令区出现 emoji（Java 正则会整条判 false）: ${cmdEmoji[0]}`)
    bad++
  } else if (srcEmoji) {
    console.log(`  note 注释区有 emoji（仅注释，不影响执行）: ${srcEmoji[0]}`)
  }
}

console.log(`\n结果: ${bad === 0 ? 'OK' : bad + ' 项问题'}`)
process.exit(bad === 0 ? 0 : 1)
