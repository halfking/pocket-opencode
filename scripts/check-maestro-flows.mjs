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
//
// ---------------------------------------------------------------------------
// 配置：scripts/.maestro-flows.json
// ---------------------------------------------------------------------------
// 2026-10-04 重构原因：脚本第 2 条检查**硬编码**了
//   `if (String(appId) !== 'com.kaixuan.opencode.pocket.sttdev')`
// 也就是「所有 flow 都必须指向并存调试包」。这是当初只为 2 条 sttdev 流写的，
// 拿到 23 条 flow 上跑时，21 条主包 flow 全部报 `FAIL appId 应为 sttdev 并存包`
// —— 必然假阳性，等于这条检查没有判别力。
//
// 同时暴露了三类**性质完全不同**的假阳性来源，必须分开处理，
// 混在一起豁免会把检查变瞎：
//
//   1. 运行时/测试数据锚点：界面上有、源码里没有，因为它是**数据**不是**代码**。
//      例：邮箱账户名 `凯轩企业邮`、种子账户 `kimmy.huang@163.com`
//          （来自 scripts/seed_email_accounts.sh 写库，不是语言包），
//          闪卡 `回归卡组`/`回归正面`（flow 自己 inputText 打出来的）。
//      → 走配置里的 `runtimeAnchors` 声明式豁免。
//   2. 故意失败的探针流：`_probe-*.yaml` 的**设计目标就是断言匹配不到**
//      （`assertVisible: "ZZZ_故意失败_…"` 用来 dump 可访问性树）。
//      对它们做选择器溯源是范畴错误。→ 走 `kind: "probe"` 整条跳过溯源。
//   3. 系统弹窗文案：权限框/MIUI 弹窗按钮，由系统绘制（见 SYSTEM_DIALOG_TEXTS）。
//
// 三类豁免**都必须打印**。静默放过 = 检查自己变瞎，那比误报更糟。
import { readFileSync, existsSync, readdirSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const require2 = createRequire(join(ROOT, 'frontend', 'package.json'))
const { parseAllDocuments } = require2('yaml')

/** 主包 appId。绝大多数 flow 跑主包；调试包要显式声明。 */
const DEFAULT_APP_ID = 'com.kaixuan.opencode.pocket'

/**
 * 读配置并归一化。
 *
 * 支持两种形状，为了不让旧配置（纯字符串数组）失效：
 *   [".maestro/a.yaml", { "file": "...", "appId": "..." }]
 *   { "flows": [...], "runtimeAnchors": [...], "defaultAppId": "..." }
 *
 * ⚠️ 未知字段一律**响亮报错**而不是忽略：写错 key（比如把 runtimeAnchors
 * 拼成 runtimeAnchor）却被静默忽略的话，检查会少放行一堆锚点，
 * 而输出看上去一切正常 —— 这正是「判据失明」最难发现的样子。
 */
const ENTRY_KEYS = new Set(['file', 'appId', 'kind', 'runtimeAnchors'])
const TOP_KEYS = new Set(['flows', 'runtimeAnchors', 'defaultAppId'])

function loadConfig() {
  const raw = JSON.parse(readFileSync(join(ROOT, 'scripts', '.maestro-flows.json'), 'utf8'))
  const cfg = {
    defaultAppId: DEFAULT_APP_ID,
    runtimeAnchors: new Set(),
    entries: [],
  }
  const list = Array.isArray(raw) ? raw : raw?.flows
  if (!Array.isArray(list)) {
    console.log('FAIL 配置格式不对：scripts/.maestro-flows.json 需要是数组，或含 flows 数组的对象')
    process.exit(1)
  }
  if (!Array.isArray(raw)) {
    for (const k of Object.keys(raw)) {
      // `_` 前缀当文档键（JSON 没有注释语法，本仓约定用 _comment 写说明）。
      // 只跳过前缀，不跳过任何真实字段 —— 打错成 `_runtimeAnchors` 仍会被下面的
      // 正文忽略逻辑暴露成「锚点没被豁免」的假失败，而不是静默少放行。
      if (k.startsWith('_')) continue
      if (!TOP_KEYS.has(k)) {
        console.log(`FAIL 配置顶层出现未知字段 "${k}"（允许: ${[...TOP_KEYS].join(' / ')}，或 _ 前缀文档键）`)
        process.exit(1)
      }
    }
    if (typeof raw.defaultAppId === 'string') cfg.defaultAppId = raw.defaultAppId
    for (const a of raw.runtimeAnchors ?? []) cfg.runtimeAnchors.add(a)
  }
  for (const e of list) {
    if (typeof e === 'string') { cfg.entries.push({ file: e }); continue }
    if (!e || typeof e !== 'object') {
      console.log(`FAIL 配置条目既不是字符串也不是对象: ${JSON.stringify(e)}`)
      process.exit(1)
    }
    for (const k of Object.keys(e)) {
      if (!ENTRY_KEYS.has(k)) {
        console.log(`FAIL 配置条目 ${e.file ?? '(缺 file)'} 出现未知字段 "${k}"（允许: ${[...ENTRY_KEYS].join(' / ')}）`)
        process.exit(1)
      }
    }
    if (typeof e.file !== 'string') {
      console.log(`FAIL 配置条目缺 file 字段: ${JSON.stringify(e)}`)
      process.exit(1)
    }
    if (e.kind !== undefined && e.kind !== 'probe' && e.kind !== 'main') {
      console.log(`FAIL ${e.file} 的 kind 只能是 "probe" 或 "main"，实际 ${JSON.stringify(e.kind)}`)
      process.exit(1)
    }
    cfg.entries.push({
      file: e.file,
      appId: e.appId,
      kind: e.kind ?? 'main',
      runtimeAnchors: e.runtimeAnchors ?? [],
    })
  }
  return cfg
}

const CFG = loadConfig()

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
      // 含 `@` 的 token（邮箱）单独放行：拆正则修饰后
      // `.*huangxutao@kxpms\.cn.*` 会碎成 "huangxutao@kxpms" / "cn"，
      // 两个都不满足标识符形状，于是整条锚点被丢进 unchecked 桶 ——
      // 而同一类的 `.*kimmy\.huang@163\.com.*` 却因为切出 "kimmy" 而被真查了。
      // 同一类别里一条查一条不查，判据的覆盖面就在抖。
      const isEmailish = t.includes('@') && t.length >= 6
      if (/[\u4e00-\u9fff]{2,}/.test(t) || /^[A-Za-z][A-Za-z0-9_.-]{3,}$/.test(t) || isEmailish) lits.push(t)
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

/**
 * 剥掉正则修饰，让「声明的数据锚点」能和「流里写的锚点」对上。
 *
 * 必须归一化后再比，否则最常见的写法全部失配：
 *   流里是  `.*kimmy\.huang@163\.com.*`   （转义点号 + 首尾 .*）
 *   声明是  `kimmy.huang@163.com`        （裸字面量）
 * 直接字符串相等 ⇒ 声明了也豁免不掉，运行时数据锚点就成了假阳性来源。
 */
function normalizeAnchor(s) {
  return String(s).replace(/[.*+?^${}()|[\]\\]/g, '')
}

function isRuntimeAnchor(anchor, runtimeAnchors) {
  const n = normalizeAnchor(anchor)
  if (n.length < 4) return false
  for (const r of runtimeAnchors) {
    const rn = normalizeAnchor(r)
    if (rn.length >= 4 && n.includes(rn)) return true
  }
  return false
}

function traceAnchors(anchors, corpus, runtimeAnchors) {
  const missing = []
  const unchecked = []
  const exempted = []
  const runtime = []
  for (const a of anchors) {
    const at = a.trim()
    if (SYSTEM_DIALOG_TEXTS.has(at)) { exempted.push(a); continue }
    // 运行时/测试数据锚点：界面上确实有，但它是**数据**不是**代码**，
    // 所以「源码里没有」是预期而不是 bug。逐条打印，不静默。
    if (isRuntimeAnchor(a, runtimeAnchors)) { runtime.push(a); continue }
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
  return { missing, unchecked, exempted, runtime }
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

function checkFlow(entry, corpus) {
  const f = entry.file
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

  // 2. appId 必须是这条流**自己**声明的那个包
  //
  // 期望值来自配置（entry.appId ?? defaultAppId），不再硬编码 sttdev：
  // 硬编码时 21 条主包 flow 会全部误报，而主包恰好是 CI 与真机日常跑的那批。
  // 反过来，调试包流必须显式声明 appId —— 漏声明就会撞上主包期望值而报红，
  // 这个方向是安全的（响亮失败），不会静默放行。
  const expectedAppId = String(entry.appId ?? CFG.defaultAppId)
  if (String(appId) !== expectedAppId) {
    // 必须打印 appId 而不是 doc.appId：命令列表是数组，doc.appId 恒为
    // undefined，报错里出现 undefined 会让人误判成「解析失败」而不是
    // 「appId 写错了」。（2026-10-01 实测踩过。）
    console.log(`  FAIL appId 应为 ${expectedAppId}，实际 ${appId}`)
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
  if (entry.kind === 'probe') {
    // 探针流（_probe-*.yaml）**故意**断言匹配不到，用来 dump 可访问性树。
    // 对它做溯源是范畴错误：`assertVisible: "ZZZ_故意失败_…"` 按设计
    // 就该找不到出处。所以整条跳过，但必须打印跳过原因 ——
    // 静默跳过会让「这条流根本没被检查」和「这条流检查通过」长得一样。
    //
    // ⚠️ 负控实测（2026-10-04）：光靠「配置里写了 kind: probe」是不够的 ——
    // 把主流程 smoke-login.yaml 误标成 probe，结果直接变 OK（exit 0），
    // 一条真实流程的溯源被整条关掉，检查自己变瞎且毫无提示。
    // 所以这里加一道**可核对**的门槛：文件名必须落在 `_probe-` 前缀上。
    // 误标会当场报红，而不是悄悄放行。
    if (!/(^|\/)_probe-/.test(f)) {
      console.log(`  FAIL 标为 kind=probe 但文件名不是 _probe- 前缀（误标会让本流溯源被整条关闭）: ${f}`)
      problems++
    }
    console.log(`  选择器溯源：跳过（kind=probe 探针流，${anchors.length} 条锚点按设计匹配不到）`)
  } else {
    const runtimeAnchors = new Set([...CFG.runtimeAnchors, ...(entry.runtimeAnchors ?? [])])
    const { missing, unchecked, exempted, runtime } = traceAnchors(anchors, corpus, runtimeAnchors)
    if (missing.length) {
      console.log('  FAIL 以下选择器在源码里找不到出处（真机上大概率匹配不到）:')
      missing.forEach((a) => console.log(`    - ${a}`))
      problems++
    } else {
      const checked = anchors.length - unchecked.length - exempted.length - runtime.length
      console.log(`  选择器溯源：${checked} 条正向锚点在源码中找到出处`)
    }
    if (exempted.length) {
      // 豁免必须可见：否则「这条检查放过了一条选择器」这件事没人知道。
      console.log(`  note ${exempted.length} 条按系统弹窗文案豁免（不在 App 源码里属正常）: ${exempted.join(' / ')}`)
    }
    if (runtime.length) {
      console.log(`  note ${runtime.length} 条按运行时数据锚点豁免（是数据不是代码）: ${runtime.join(' / ')}`)
    }
    if (unchecked.length) {
      console.log(`  note ${unchecked.length} 条锚点剥不出可查字面量，未做溯源: ${unchecked.join(' / ')}`)
    }
  }
  return problems
}

/**
 * 覆盖率报告（只打印，不判红）。
 *
 * 之前一直没答上来的问题：「这个检查到底看了几条流？」
 * 2026-10-04 实测：配置里只有 2 条，.maestro/ 下却有 23 条 yaml。
 * 覆盖率不报出来，「全绿」会被读成「所有流都检查过了」——而事实不是。
 * 只打印不判红，是为了让「要不要把这些流纳进来」成为可见的待决项，
 * 而不是一次静默的口径变更。
 */
function reportCoverage() {
  const dir = join(ROOT, '.maestro')
  if (!existsSync(dir)) return
  const all = readdirSync(dir).filter((n) => /\.ya?ml$/.test(n)).map((n) => `.maestro/${n}`)
  const listed = new Set(CFG.entries.map((e) => e.file))
  const unlisted = all.filter((f) => !listed.has(f))
  if (unlisted.length) {
    console.log(`\n覆盖率：已检查 ${listed.size} 条 / .maestro 下共 ${all.length} 条 yaml`)
    console.log(`  未列入配置的 ${unlisted.length} 条（本次未被检查）:`)
    unlisted.forEach((f) => console.log(`    - ${f}`))
  } else {
    console.log(`\n覆盖率：已检查 ${listed.size} 条 / .maestro 下共 ${all.length} 条 yaml（全部覆盖）`)
  }
}

let bad = 0
const corpus = loadCorpus()
for (const e of CFG.entries) bad += checkFlow(e, corpus)
reportCoverage()
console.log(`\n结果: ${bad === 0 ? 'OK' : bad + ' 项问题'}`)
process.exit(bad === 0 ? 0 : 1)
