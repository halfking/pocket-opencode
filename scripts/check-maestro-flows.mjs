// Maestro 流静态检查器
//
// 为什么需要它：真机不可用时，Maestro 流最容易坏的不是语法，而是
// **选择器指向一个不存在的元素**。2026-10-01 实测踩过 —— 录音 FAB 写成
// `tapOn: id: "fab"`，而源码 frontend/src/features/notes/VoiceRecorderWidget.vue
// 里是 <button class="fab" :aria-label="recording ? '停止录音' : '开始录音'>，
// 没有 id 属性。class 在 WebView a11y 树里也不保证暴露，真机上大概率点不到。
// 这类错在设备在线时表现为「流红了但看不出为什么」，很费时间。
//
// 所以这里在**离线状态下**就能拦住：命令词表、appId、emoji、选择器溯源。
//
// 运行：node scripts/check-maestro-flows.mjs   （不依赖 cwd，仓库根自动定位）
import { readFileSync, existsSync, readdirSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const require2 = createRequire(join(ROOT, 'frontend', 'package.json'))
const { parseAllDocuments } = require2('yaml')

const FLOWS = JSON.parse(readFileSync(join(ROOT, 'scripts', '.maestro-flows.json'), 'utf8'))

/** Maestro 2.11 的命令/子命令。只做「是不是杜撰的名字」这一层检查。 */
const KNOWN = new Set([
  'launchApp', 'stopApp', 'killApp', 'clearState', 'openLink', 'back', 'hideKeyboard',
  'tapOn', 'longPressOn', 'doubleTapOn', 'inputText', 'inputRandomText', 'inputRandomNumber',
  'eraseText', 'pressKey', 'swipe', 'scroll', 'scrollUntilVisible', 'copyTextFrom',
  'assertVisible', 'assertNotVisible', 'assertTrue', 'extendedWaitUntil', 'waitForAnimationToEnd',
  'runFlow', 'runScript', 'evalScript', 'repeat', 'retry', 'travel', 'setLocation',
  'takeScreenshot', 'startRecording', 'stopRecording', 'setAirplaneMode',
  'toggleAirplaneMode', 'assertWithAI', 'pasteText',
])

/** 只会出现在「必须能在界面或源码里找到」的锚点上的命令。 */
const POSITIVE_CMDS = new Set([
  'assertVisible', 'tapOn', 'longPressOn', 'doubleTapOn', 'extendedWaitUntil',
])

/** 收集流里的正向文本锚点。 */
function collectAnchors(cmds) {
  const out = []
  const push = (cmd, v) => {
    if (!POSITIVE_CMDS.has(cmd)) return
    if (typeof v === 'string') { out.push(v); return }
    if (v && typeof v === 'object') {
      if (typeof v.text === 'string') out.push(v.text)
      if (typeof v.visible === 'string') out.push(v.visible)
      if (typeof v.id === 'string') out.push(`id:${v.id}`)
    }
  }
  const walk = (list) => {
    for (const c of list) {
      if (typeof c !== 'object' || c === null) continue
      for (const [cmd, v] of Object.entries(c)) {
        push(cmd, v)
        if (v && typeof v === 'object') {
          if (Array.isArray(v.commands)) walk(v.commands)
          if (Array.isArray(v.when?.commands)) walk(v.when.commands)
        }
      }
    }
  }
  walk(cmds)
  return [...new Set(out)]
}

/**
 * 从锚点里剥掉正则修饰，只留字面部分去源码里找。
 *
 * Maestro 的 text 是正则：`.*语音转写.*`、`.*(A|B).*` 都合法。
 * 按 `|` 拆分支、去修饰字符，再取「足够长且像标识」的中文词或英文词。
 * 一个都切不出来的跳过并单独报出 —— 宁可说「没查」也不要假失败。
 */
function literalOf(anchor) {
  const lits = []
  for (const br of anchor.split('|')) {
    const cleaned = br.replace(/[.*+?^${}()|[\]\\]/g, ' ').trim()
    if (!cleaned) continue
    for (const t of cleaned.split(/\s+/).filter(Boolean)) {
      if (/[\u4e00-\u9fff]{2,}/.test(t) || /^[A-Za-z][A-Za-z0-9_.-]{3,}$/.test(t)) lits.push(t)
    }
  }
  return [...new Set(lits)]
}

/** 读一遍 frontend/src 下的 .vue/.ts/.json，拼成可搜索的语料。 */
function loadCorpus() {
  const root = join(ROOT, 'frontend', 'src')
  const files = []
  const walkDirs = (d) => {
    for (const e of readdirSync(d, { withFileTypes: true })) {
      if (e.name === 'node_modules' || e.name === 'dist') continue
      const p = join(d, e.name)
      if (e.isDirectory()) walkDirs(p)
      else if (/\.(vue|ts|json)$/.test(e.name)) files.push(p)
    }
  }
  if (existsSync(root)) walkDirs(root)
  return files.map((f) => readFileSync(f, 'utf8')).join('\n')
}

/**
 * 把锚点回源码里追。
 *
 * 溯源范围刻意包含 locales/*.json：界面文案是 i18n 的，文本锚点常常
 * 只存在于语言包里而不在 .vue 里（实测「学习」就只在 zh-CN.json）。
 * 只搜 .vue 会把这类合法锚点误判成「找不到出处」。
 */
// Android 运行时权限框 / 系统 UI 的按钮文案。
//
// 这些字符串由**系统**绘制，永远不可能出现在 App 源码或语言包里，
// 所以「在源码里找不到出处」对它们是必然的假阳性，而不是 bug。
// 2026-10-04 实测踩到：MIUI 的麦克风权限框三个按钮是
//   拒绝 / 本次使用允许 / 仅在使用中允许
// flow 用它当 tapOn 锚点，于是被溯源检查判成「真机上大概率匹配不到」——
// 而这条 flow 当时其实**跑得通**（注入前提后 2/2 绿）。
//
// ⚠️ 刻意做窄：只有这几条**实测出现在本项目真机上的系统文案**才豁免，
// 且**必须打印出来**（见下方 traceAnchors 的 exempted 桶）——
// 静默放过会让这条检查自己变瞎，那比误报更糟。
// 新增条目前先确认它确实是系统文案，而不是忘了在 App 里写的锚点。
const SYSTEM_DIALOG_TEXTS = new Set([
  '拒绝',            // 权限框：拒绝
  '本次使用允许',    // 权限框：仅本次
  '仅在使用中允许',  // 权限框：仅使用期间（2026-10-04 实测，本项目录音流用它）
  '我知道了',        // MIUI 首次启动弹窗
  '始终允许',
  '仅此一次',
])

function traceAnchors(anchors, corpus) {
  const missing = []
  const unchecked = []
  const exempted = []
  for (const a of anchors) {
    if (SYSTEM_DIALOG_TEXTS.has(a.trim())) { exempted.push(a); continue }
    if (a.startsWith('id:')) {
      // id 锚点必须在源码里真的是 id 属性，class 不算 ——
      // 这条正是要拦的 bug：把 class="fab" 写成 id: "fab"。
      const id = a.slice(3)
      if (!corpus.includes(`id="${id}"`) && !corpus.includes(`id='${id}'`)) {
        missing.push(`${a}  （源码里没有 id="${id}" 这个属性；class 不算 id）`)
      }
      continue
    }
    const lits = literalOf(a)
    if (lits.length === 0) { unchecked.push(a); continue }
    const notFound = lits.filter((l) => !corpus.includes(l))
    if (notFound.length) missing.push(`${a}  （源码中找不到: ${notFound.join(' / ')}）`)
  }
  return { missing, unchecked, exempted }
}

/** 收集命令名，顺带展开 runFlow 的内嵌 commands。 */
function collectCommands(cmds, depth = 0) {
  const out = []
  for (const c of cmds) {
    if (typeof c !== 'object' || c === null) continue
    const name = Object.keys(c)[0]
    if (!name) continue
    if (name === 'commands' || name === 'visible' || name === 'notVisible') continue
    out.push({ name, depth })
    const inner = c[name]
    if (Array.isArray(inner?.commands)) out.push(...collectCommands(inner.commands, depth + 1))
    if (Array.isArray(inner?.when?.commands)) out.push(...collectCommands(inner.when.commands, depth + 1))
  }
  return out
}

function checkFlow(f, corpus) {
  let problems = 0
  console.log(`\n== ${f} ==`)

  if (!existsSync(join(ROOT, f))) {
    // 流文件不存在要报成一条干净的 FAIL，而不是甩一个 ENOENT 栈出来：
    // 后者会让人以为检查器坏了，而不是「配置里写了条不存在的流」。
    console.log('  FAIL 流文件不存在（scripts/.maestro-flows.json 里列了它）')
    return 1
  }
  const src = readFileSync(join(ROOT, f), 'utf8')

  let header, doc
  try {
    // Maestro 用 `---` 分成两个 YAML 文档：头部（appId）与命令列表。
    const docs = parseAllDocuments(src, { logLevel: 'error' })
    for (const d of docs) if (d.errors.length) throw new Error(d.errors[0].message)
    header = docs[0].toJS() || {}
    doc = docs[docs.length - 1].toJS() || []
  } catch (e) {
    console.log(`  YAML PARSE FAIL: ${e.message}`)
    return 1
  }
  const appId = header.appId ?? doc.appId
  const cmds = Array.isArray(doc) ? doc.filter((c) => typeof c === 'object' && c !== null) : []
  console.log(`  appId: ${appId}`)
  console.log(`  文档数: 2（头部 + 命令列表）  命令数: ${cmds.length}`)

  // 1. 命令词表
  const unknown = collectCommands(cmds)
    .filter((c) => !KNOWN.has(c.name))
    .map((c) => `${'  '.repeat(c.depth + 1)}${c.name}`)
  if (unknown.length) {
    console.log('  未识别的命令（可能是拼错或版本不支持）:')
    unknown.forEach((u) => console.log(u))
    problems++
  } else {
    console.log('  命令词表：全部命中已知集合')
  }

  // 2. appId 必须是并存包
  if (String(appId) !== 'com.kaixuan.opencode.pocket.sttdev') {
    // 必须打印 appId 而不是 doc.appId：命令列表是数组，doc.appId 恒为
    // undefined，报错里出现 undefined 会让人误判成「解析失败」而不是
    // 「appId 写错了」。（2026-10-01 实测踩过。）
    console.log(`  FAIL appId 应为 sttdev 并存包，实际 ${appId}`)
    problems++
  }

  // 3. 命令区不得含 emoji —— Java 正则里这些字符会让整条断言直接判 false
  const emoji = /[\u{1F300}-\u{1FAFF}\u{2705}\u{270E}\u{1F5D1}]/u
  const cmdEmoji = JSON.stringify(cmds).match(emoji)
  const srcEmoji = src.match(emoji)
  if (cmdEmoji) {
    console.log(`  FAIL 命令区出现 emoji（Java 正则会整条判 false）: ${cmdEmoji[0]}`)
    problems++
  } else if (srcEmoji) {
    console.log(`  note 注释区有 emoji（仅注释，不影响执行）: ${srcEmoji[0]}`)
  }

  // 4. 选择器可溯源
  const anchors = collectAnchors(cmds)
  const { missing, unchecked, exempted } = traceAnchors(anchors, corpus)
  if (missing.length) {
    console.log('  FAIL 以下选择器在源码里找不到出处（真机上大概率匹配不到）:')
    missing.forEach((a) => console.log(`    - ${a}`))
    problems++
  } else {
    const checked = anchors.length - unchecked.length - exempted.length
    console.log(`  选择器溯源：${checked} 条正向锚点在源码中找到出处`)
  }
  if (exempted.length) {
    // 豁免必须可见：否则「这条检查放过了一条选择器」这件事没人知道。
    console.log(`  note ${exempted.length} 条按系统弹窗文案豁免（不在 App 源码里属正常）: ${exempted.join(' / ')}`)
  }
  if (unchecked.length) {
    console.log(`  note ${unchecked.length} 条锚点剥不出可查字面量，未做溯源: ${unchecked.join(' / ')}`)
  }
  return problems
}

let bad = 0
const corpus = loadCorpus()
for (const f of FLOWS) bad += checkFlow(f, corpus)
console.log(`\n结果: ${bad === 0 ? 'OK' : bad + ' 项问题'}`)
process.exit(bad === 0 ? 0 : 1)
