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
const ENTRY_KEYS = new Set(['file', 'appId', 'kind', 'runtimeAnchors', 'fixture', 'reset'])
const TOP_KEYS = new Set(['flows', 'runtimeAnchors', 'defaultAppId'])

/**
 * 夹具脚本路径**由名字推导**，不另建一张映射表。
 *
 * 为什么要推导：checker 与 maestro-run.mjs 都要用这张表。若两边各写一份
 * 映射，就会出现「checker 放行的名字 harness 认不出」这种两份真相 ——
 * 而它的表现是 flow 悄悄少了前置/后置步骤，正是「判据失明」的样子。
 * 约定：`fixture: stt-error` ⇒ `scripts/stt-error-fixture.mjs`，
 * harness 以 `--induce` / `--restore` 两个参数调用它。
 *
 * ⚠️ `fixture` 与 `reset` 是**两种语义，不是同义词**，别合并：
 *   · fixture —— 制造一个前提，跑完**必须还原**（哨兵模型留在库里
 *     就等于用户的语音转写是坏的）。
 *   · reset   —— 一次性把场景清干净，**没有「还原」这回事**。
 *     flashcards-test-fixture 清的是「零卡组」这个起点；给它的「还原」
 *     再跑一遍，等于把 flow 刚建好的卡组和卡片又删掉 —— 既破坏数据，
 *     也让 flow 的收尾断言失去意义。
 *     合并成一个开关的后果是「一键毁数据」，不是「少做一步」。
 */
const fixtureScript = (name) => join(ROOT, 'scripts', `${name}-fixture.mjs`)

/** 校验一个夹具/清场名字；不合法就响亮退出（不静默跳过）。 */
function assertFixtureName(file, key, name) {
  if (!/^[a-z0-9][a-z0-9-]*$/.test(String(name))) {
    console.log(`FAIL ${file} 的 ${key} "${name}" 名字不合法（只允许小写字母/数字/连字符）`)
    process.exit(1)
  }
  if (!existsSync(fixtureScript(String(name)))) {
    // 名字写错 ⇒ 脚本不存在 ⇒ harness 拼出的路径也跑不起来。
    // 这里必须响亮退出而不是跳过：静默跳过等于「前置没做但没人知道」。
    console.log(`FAIL ${file} 声明的 ${key} "${name}" 没有对应脚本 ${fixtureScript(String(name))}`)
    process.exit(1)
  }
}

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
    if (e.kind !== undefined && e.kind !== 'probe' && e.kind !== 'main' && e.kind !== 'subflow') {
      console.log(`FAIL ${e.file} 的 kind 只能是 "probe" / "main" / "subflow"，实际 ${JSON.stringify(e.kind)}`)
      process.exit(1)
    }
    if (e.fixture !== undefined) assertFixtureName(e.file, 'fixture', e.fixture)
    if (e.reset !== undefined) assertFixtureName(e.file, 'reset', e.reset)
    if (e.fixture !== undefined && e.reset !== undefined) {
      console.log(`FAIL ${e.file} 同时声明了 fixture 和 reset —— 两者语义不同（一个要还原、一个是清场），请只留一个`)
      process.exit(1)
    }
    cfg.entries.push({
      file: e.file,
      appId: e.appId,
      kind: e.kind ?? 'main',
      runtimeAnchors: e.runtimeAnchors ?? [],
      fixture: e.fixture,
      reset: e.reset,
    })
  }

  // kind: subflow 的**自证**：必须真被某个父流 runFlow 引用。
  //
  // 2026-10-04 真机实测踩到：`_set-master-password.yaml` 是子流 —— 它只被
  // `_login.yaml` 在 `runFlow when visible: 创建主密码` 下引用，自身**没有**任何
  // 存在性判断。而配置把它当普通独立流登记，于是「跑全部 24 条」必然在它身上红：
  // 设备已设过主密码 ⇒ 那个弹窗根本不存在 ⇒ `Element not found: 确认`。
  // 报错指向「确认按钮不见了」，与「产品坏了」和「选择器写错」长得一模一样。
  //
  // 标成 subflow 是对症的修法（它本来就不是独立跑的），但光标还不够：
  // 标了却没被引用 = 死代码。所以这条必须自己会长出那个分支。
  for (const sub of cfg.entries.filter((e) => e.kind === 'subflow')) {
    const base = sub.file.split('/').pop()
    const referenced = cfg.entries
      .filter((e) => e.file !== sub.file)
      .some((e) => runFlowRefs(e.file, base))
    if (!referenced) {
      console.log(`FAIL ${sub.file} 标成了 kind=subflow，但仓里没有任何其它流用 runFlow 引用它（死子流）`)
      process.exit(1)
    }
  }
  return cfg
}

/** 这个文件里有没有 `runFlow: <base>`（子流引用）。 */
function runFlowRefs(file, base) {
  try {
    return readFileSync(join(ROOT, file), 'utf8').includes(`runFlow: ${base}`)
  } catch {
    return false
  }
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
    // ⚠️ 裸写法是**合法** Maestro 命令（`- launchApp` / `- back` / `- hideKeyboard`），
    //   在 YAML 里它们是字符串而不是对象。2026-10-04 之前这里直接 continue 掉了，
    //   于是所有裸命令对**整个命令词表检查**都是隐形的 —— `- tapOnn` 这种拼错
    //   的裸命令能一路过到底线。真实的例子：`_connectivity-sttdev.yaml` 里的
    //   `- launchApp` 谁都没看见，直到它在真机上把 App 弄死才暴露。
    if (typeof c === 'string') { out.push({ name: c, depth }); continue }
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
  console.log(`\n== ${f} ==`)

  if (!existsSync(join(ROOT, f))) {
    // 流文件不存在要报成一条干净的 FAIL，而不是甩一个 ENOENT 栈出来：
    // 后者会让人以为检查器坏了，而不是「配置里写了条不存在的流」。
    console.log('  FAIL 流文件不存在（scripts/.maestro-flows.json 里列了它）')
    return 1
  }
  return checkFlowSource(entry, readFileSync(join(ROOT, f), 'utf8'), corpus)
}

/**
 * 真正的检查逻辑，与文件系统解耦。
 *
 * 为什么要拆开：门禁要接进 CI，就必须能**自证它还能报错**。
 * 直接读盘的话，敏感度验证只能靠手工改真文件（2026-10-04 做过 4 次，
 * 每次都要备份还原）。拆开后自检直接喂合成 YAML 字符串，
 * 不碰仓库、不留残留、每次 CI 都能跑。
 */
function checkFlowSource(entry, src, corpus) {
  const f = entry.file
  let problems = 0

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
  // ⚠️ 留一份**未过滤**的原始命令数组：`rawCmds` 里的裸写法命令（`- launchApp`
  //   这类字符串项）才是某些检查唯一能看到它们的入口，见下方 launchApp 禁令。
  //   `cmds` 是给选择器溯源用的（它要的是对象形式的断言），两者不能互相替代。
  const rawCmds = Array.isArray(doc) ? doc : []
  const cmds = rawCmds.filter((c) => typeof c === 'object' && c !== null)
  console.log(`  appId: ${appId}`)
  // 夹具声明要出现在报告里：它是「这条流的前置/后置由 harness 代劳」的
  // 唯一可见处。不打出来的话，读报告的人无从知道前置是谁做的。
  if (entry.fixture) console.log(`  fixture: ${entry.fixture}（harness 跑前 --induce、跑后 --restore）`)
  if (entry.reset) console.log(`  reset: ${entry.reset}（harness 跑前清场一次，**不还原**）`)
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

  // 4. flow 里禁止 launchApp（2026-10-04 真机实测出的硬约束）
  //
  // 后果不是「多启动一次」那么轻：Maestro 的 launchApp 先 am force-stop，
  // 随后的 start 被 MIUI 的 com.miui.securitycenter wakepath 确认框拦下，
  // App 从此回不到前台。实测它先让本条流红在「找不到页面」，
  // 再把 App 弄死，于是**下一条**流开跑前的 pidof 为空、
  // harness 带栈崩掉，后面的 flow 一条都没跑 —— 而报告里只看得到本条的红，
  // 读起来像「跑过了」，实际是「没跑」。
  //
  // preflight 已经用 adb（force-stop + monkey）把 App 拉起来，那条路径实测可靠。
  // 所以这不是「建议」，是禁令：写了就是会带崩整批。
  const launchApp = collectCommands(rawCmds).filter((c) => c.name === 'launchApp')
  if (launchApp.length) {
    console.log(`  FAIL flow 里出现 launchApp ×${launchApp.length}（MIUI 上会 force-stop 后被 wakepath 拦下，App 回不到前台并带崩整批）`)
    console.log('       删掉它：preflight 已经把 App 拉起来了。详见 .maestro/_connectivity-sttdev.yaml 头部。')
    problems++
  }

  // 5. 选择器可溯源
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
  // 子流**被检查**，但不是独立可跑的。把这个区别说出来，否则「24/24 全覆盖」
  // 会被读成「24 条都能单独跑」——而 _set-master-password 这类就是跑不得的。
  const subs = CFG.entries.filter((e) => e.kind === 'subflow')
  if (subs.length) {
    console.log(`  其中 ${subs.length} 条是子流（被检查，但只能由父流 runFlow 引用，不可独立运行）:`)
    subs.forEach((e) => console.log(`    - ${e.file}`))
  }
}

/**
 * 自检：证明这条检查**还能报错**。
 *
 * 为什么必须自带：这条门禁要接进 gates 也就是接进 CI。如果它哪天被改成一个
 * 空转（比如 collectAnchors 提前 return、或豁免集合被人写成通配），
 * 表现会是「全绿」——和检查真的通过**完全同形**，CI 不会拦。
 * 所以门禁必须自带敏感度验证，且每个用例都断言一个**具体**的失败原因，
 * 不能只断言「非 0」（否则「因为别的理由红」也会算通过）。
 *
 * 下面 4 组负控对应 2026-10-04 手工做过的 4 次真实负控，这里固化成可重复执行的形式。
 */
function selftest() {
  const corpus = 'AI 工具 快速提问 笔记 更多 消息 闪卡 邮箱设置 id="real-id"'
  const results = []

  // 敏感度 1：源码里不存在的锚点必须被抓
  const s1 = checkFlowSource({ file: '.maestro/_st-1.yaml' }, yamlOf('com.kaixuan.opencode.pocket', ['- assertVisible: "ZZZ_绝对不存在于任何源码"']), corpus)
  results.push({ name: '敏感度1 不存在的锚点被报出', subject: 'checkFlowSource', pass: s1 > 0 })

  // 敏感度 2：class 名当 id 写必须被抓（这条检查存在的最初理由）
  const s2 = checkFlowSource({ file: '.maestro/_st-2.yaml' }, yamlOf('com.kaixuan.opencode.pocket', ['- tapOn: { id: "fab" }']), corpus)
  results.push({ name: '敏感度2 class 当 id 被报出', subject: 'checkFlowSource', pass: s2 > 0 })

  // 敏感度 3：appId 写错必须被抓
  const s3 = checkFlowSource({ file: '.maestro/_st-3.yaml' }, yamlOf('com.example.wrong', ['- assertVisible: "AI 工具"']), corpus)
  results.push({ name: '敏感度3 appId 不符被报出', subject: 'checkFlowSource', pass: s3 > 0 })

  // 敏感度 4：豁免清单里邮箱域名打错，豁免必须**不放行**
  // （这条最关键：豁免集合一旦变成「近似匹配一切」，门禁就等于没有）
  const bogus = new Set(['kimmy.huang@164.com'])
  const s4 = traceAnchors(['.*kimmy\\.huang@163\\.com.*'], corpus, bogus)
  results.push({ name: '敏感度4 域名打错的豁免不生效', subject: 'traceAnchors', pass: s4.missing.length === 1 && s4.runtime.length === 0 })

  // 敏感度 5：主流程误标 probe 必须被拒（否则一条真流程的溯源被整条关闭）
  const s5 = checkFlowSource({ file: '.maestro/smoke-login.yaml', kind: 'probe' }, yamlOf('com.kaixuan.opencode.pocket', ['- assertVisible: "AI 工具"']), corpus)
  results.push({ name: '敏感度5 主流程误标 probe 被拒', subject: 'checkFlowSource', pass: s5 > 0 })

  // 特异度：真实的源码内锚点必须**不**报错（否则就是又一个必然假阳性）
  const ok = checkFlowSource({ file: '.maestro/_st-ok.yaml' }, yamlOf('com.kaixuan.opencode.pocket', ['- assertVisible: "AI 工具"', '- tapOn: "更多"']), corpus)
  results.push({ name: '特异度 源码内锚点不误报', subject: 'checkFlowSource', pass: ok === 0 })

  // 特异度 2：正则在最常见的两种包裹下，声明过的数据锚点要被正确豁免
  const declared = new Set(['kimmy.huang@163.com', '回归卡组'])
  const t2 = traceAnchors(['.*kimmy\\.huang@163\\.com.*', '回归卡组.*'], corpus, declared)
  results.push({
    name: '特异度2 正则包裹 + 转义点号仍能豁免',
    subject: 'traceAnchors', pass: t2.missing.length === 0 && t2.runtime.length === 2,
  })

  // 可见性：豁免必须真的被打印出来（静默放过 = 检查自己变瞎）
  const printed = []
  const realLog = console.log
  console.log = (...a) => printed.push(a.join(' '))
  const vis = checkFlowSource({ file: '.maestro/_st-vis.yaml' }, yamlOf('com.kaixuan.opencode.pocket', [
    // ⚠️ 这里必须是 `\\.`（YAML 双引号里的转义反斜杠），不能写 `\.`。
    // YAML 的双引号标量里 `\.` 是**非法转义**，解析直接失败 —— 本轮自检
    // 第一次跑就是栽在这：夹具写错，检查器报 YAML PARSE FAIL，
    // 看起来像「可见性判据失灵」，实际是注入根本没生效。
    // 真实 .maestro/email-accounts.yaml:101 用的也是 `\\.`。
    '- assertVisible: ".*kimmy\\\\.huang@163\\\\.com.*"',
    '- tapOn: "我知道了"',
  ]), corpus)
  console.log = realLog
  results.push({
    name: '可见性 两类豁免都被打印',
    subject: 'checkFlowSource', pass: vis === 0 && printed.some((l) => l.includes('运行时数据锚点豁免')) && printed.some((l) => l.includes('系统弹窗文案豁免')),
  })

  // 配置守卫：未知字段必须响亮拒绝（写错 key 被静默忽略 = 判据失明）
  results.push({ name: '配置守卫 未知字段被拒', subject: '配置守卫', pass: unknownTopKeyIsRejected() })

  // 夹具守卫：声明了 fixture 却没有对应脚本 ⇒ 必须响亮拒绝，不能静默跳过
  // （静默跳过 = 前置没做，但报告看上去一切正常）。
  results.push({ name: '配置守卫 夹具脚本缺失被拒', subject: '配置守卫', pass: fixtureGuardIsWired() })

  // reset 语义必须与 fixture 分开：合并的后果是「一键毁数据」而不是「少做一步」
  results.push({ name: '配置守卫 reset 与 fixture 不混用', subject: '配置守卫', pass: resetIsDistinct() })

  // launchApp 禁令：必须能报出，且不误伤正常流
  results.push({ name: '禁令 flow 里的 launchApp 被报出', subject: 'launchApp 禁令', pass: launchAppIsBanned() })
  results.push({ name: '禁令 不误伤没有 launchApp 的流', subject: 'launchApp 禁令', pass: !bannedWithoutLaunchApp() })

  // 子流守卫：标了 subflow 却没人引用 = 死子流，必须报出；
  // 以及 harness 必须拒跑子流（否则报错会指向「确认按钮不见了」这种误导点）。
  results.push({ name: '配置守卫 死子流被拒', subject: '配置守卫', pass: subflowGuardIsWired() })

  const bad = results.filter((r) => !r.pass)

  // ★★★ 覆盖面下限（docs/design §206 形状五「补了用例 ≠ 补保护」+ §208 记的
  //   「`traceAnchors` 只 2 条覆盖、可删光」）。总条数下限挡不住**整组删光**：
  //   把 traceAnchors 那两条一起删掉、别的组补两条进来，results.length 一点没少。
  //
  //   按「被测对象」分组（每条用例一个 subject 字段，机械可数），下限按
  //   **当前实测条数**给 —— 棘轮：删任何一条都要同时改下限，留下一处痕迹。
  const REQUIRED_COVERAGE = [
    ['checkFlowSource', 4],   // 实测 6
    ['traceAnchors', 2],      // 实测 2 —— §208 点名的那个可删光的组
    ['launchApp 禁令', 2],     // 实测 2 —— 正反各一条
    ['配置守卫', 4],          // 实测 4 —— 四个守卫函数各一条
  ]
  const coverageBad = []
  for (const [subject, min] of REQUIRED_COVERAGE) {
    const n = results.filter((r) => r.subject === subject).length
    if (n < min) coverageBad.push(subject)
    console.log(`  覆盖 ${subject}: ${n} 条（下限 ${min}）`)
  }
  for (const subject of coverageBad) {
    console.error(`[maestro-flows] 被测面「${subject}」的用例数低于下限 —— 这一组被删光或腰斩了。`);
  }
  if (coverageBad.length) {
    console.error('   总条数下限挡不住「整组删光」：别的组补进来，results.length 一点没少。');
    process.exit(2);
  }
  for (const r of results) console.log(`  ${r.pass ? '通过' : '失败'}  ${r.name}`)
  // ★ 条数下限闸（2026-10-07，docs/design §94）。原来 `自检: N-bad/N 通过` + `exit(bad===0?0:1)`，
  //   results 为空时打印「0/0 通过」并 EXIT=0，与真通过完全同形（见 §94 的实跑证据）。
  //   这道门一次自检管 14 条，是本仓条数最多的几道之一，空转的代价更大。
  //   下限只能手工改这个常量，不接受命令行参数。
  const MIN_SELFTEST_CASES = 10;
  if (results.length < MIN_SELFTEST_CASES) {
    console.error(`[maestro-flows] 自检只跑了 ${results.length}/${MIN_SELFTEST_CASES} 例 —— 夹具循环或 add 调用被改过。`);
    console.error('   「0/0 通过」不是通过：那是判据失明时的读数，和真通过长得一模一样。');
    process.exit(2);
  }
  console.log(`\n自检: 实跑 ${results.length} 例，通过 ${results.length - bad.length} 例`)
  process.exit(bad.length === 0 ? 0 : 1)
}

/** 合成一条合法的 Maestro 流（头部 + 命令列表）。 */
function yamlOf(appId, cmds) {
  return `appId: ${appId}\n---\n${cmds.join('\n')}\n`
}

/**
 * 配置守卫的验证：拿一个未知顶层字段去解析，必须抛错。
 * 单独写成函数是因为 loadConfig 内部直接 process.exit(1)，
 * 自检里不能让它把整个进程带走。
 */
function unknownTopKeyIsRejected() {
  const src = readFileSync(join(ROOT, 'scripts', 'check-maestro-flows.mjs'), 'utf8')
  // 不去真跑 loadConfig（它会 exit），而是核对它对未知键的处理是否真在
  if (!new RegExp('FAIL 配置顶层出现未知' + '字段').test(src)) return false
  // 且允许 _ 前缀文档键
  if (!/if \(k\.startsWith\('_'\)\) continue/.test(src)) return false
  return true
}

/**
 * launchApp 禁令的**两个方向**都要验。
 *
 * 只验「该报的报了」不够 —— 禁令本身也可以变成一个静默把真流挡住的开关，
 * 所以同时要验「没有 launchApp 的正常流不会被误判」。
 * ⚠️ 注入必须真的进到命令区：这里断言 printed 里出现该 FAIL 文案，
 *    若 checkFlowSource 因别的原因提前返回，这条会假通过，
 *    所以同时要求另一条（无 launchApp）**不**产生该文案。
 */
function launchAppIsBanned() {
  const realLog = console.log
  const printed = []
  console.log = (...a) => printed.push(a.join(' '))
  const n = checkFlowSource({ file: '.maestro/_st-launchapp.yaml' },
    yamlOf('com.kaixuan.opencode.pocket', ['- launchApp', '- assertVisible: "AI 工具"']), loadCorpus())
  console.log = realLog
  return n > 0 && printed.some((l) => l.includes('出现 launchApp'))
}

function bannedWithoutLaunchApp() {
  const realLog = console.log
  const printed = []
  console.log = (...a) => printed.push(a.join(' '))
  const n = checkFlowSource({ file: '.maestro/_st-nolaunch.yaml' },
    yamlOf('com.kaixuan.opencode.pocket', ['- assertVisible: "AI 工具"']), loadCorpus())
  console.log = realLog
  return printed.some((l) => l.includes('出现 launchApp')) || n < 0
}

/**
 * 子流守卫 + harness 拒跑子流的验证。
 *
 * 两层都要在，缺一层就会出现「看起来在查、实际不管用」：
 *   checker：标了 subflow 却没人 runFlow 引用 ⇒ 死子流 ⇒ 报红。
 *   harness：有人独立跑子流 ⇒ 明确拒绝并说明原因。
 * 少了 harness 那层，独立跑子流会红在「确认按钮不见了」——
 * 报错点离根隔了整条调用链，读起来像产品坏了。
 */
function subflowGuardIsWired() {
  const src = readFileSync(join(ROOT, 'scripts', 'check-maestro-flows.mjs'), 'utf8')
  // ★ 原来这里只找 `死子流` 三个字，而那三个字在**注释**（行640/712）与
  //   **本用例自己的名字**（行642）里都出现 ⇒ 实测删掉真守卫行 175，判据照样通过。
  //   这与 §127.1 是**两回事**：那一条是模式匹配到自身正则；
  //   这一条是模式**太短**，短到注释与用例名就能顶替它。
  //   ⇒ 锚点必须落在**只有真守卫才写得出**的那一整句上。
  if (!new RegExp('标成了 kind=subflow' + '，但仓里没有任何其它流用 runFlow 引用它').test(src)) return false
  // ★ 原来这里是「runFlow: + 模板插值」与「runFlowRefs」两个分支的**择一**，
  //   后面那个分支会被**函数声明本身**满足 ⇒ 实测 M20（行为等价的改写：把判定体里
  //   「runFlow: 」后面那段模板插值换成 split+some）之后，锚点仍由
  //   「调用点 + 函数声明」顶住，自检照样 14/14。
  //   ⇒ 锚点只留判定本体那一处；那个函数名是**声明**，不是决定。
  // ⚠ 本行为此踩了两个坑，都记在这儿：
  //   ① 直接写成裸正则字面量 ⇒ 它在**正则语义上匹配自己那一行**（加转义也救不了），
  //      M20 又变绿。⇒ 必须拼接。
  //   ★★ 把锚点原文抄进这段注释 ⇒ 注释自己成了新的顶替者。
  //      本轮已经因此**两次**把刚修好的判据重新弄坏。⇒ 说明文字不许复现锚点。
  if (!new RegExp('runFlow: ' + '\\$\\{base\\}').test(src)) return false
  if (!new RegExp('子流（被检查，' + '但只能由父流').test(src)) return false
  const harness = readFileSync(join(ROOT, 'scripts', 'maestro-run.mjs'), 'utf8')
  return /subFlows\.has\(flow\)/.test(harness) && /是子流，不能独立运行/.test(harness)
}

/**
 * 夹具守卫 + harness 接线的验证。
 *
 * 为什么只做到「源码里确实有」这一层：loadConfig 内部直接 process.exit(1)，
 * 自检里不能让它把进程带走（与 unknownTopKeyIsRejected 同理）。
 * 真正的行为证据在真机跑那条流时拿：夹具 induce → flow 通过 → restore，
 * 每一步都用 psql 查过 user_settings。源码这一层只保证「规则还在、接线还在」。
 */
function fixtureGuardIsWired() {
  const src = readFileSync(join(ROOT, 'scripts', 'check-maestro-flows.mjs'), 'utf8')
  // 规则还在：fixture 是允许字段
  if (!/ENTRY_KEYS = new Set\(\[[^\]]*'fixture'/.test(src)) return false
  // 规则还在：脚本缺失时报错。
  // ★ 原来这里找的是 `声明的 fixture`，而源码里真实写的是模板 `声明的 ${key}`
  //   —— 那串字面量**只在插值之后才存在**，静态源码里永远找不到。
  //   它之所以一直「通过」，是因为那行判据 grep 的是自己（见 §127）。
  // ★★ 改成「没有对应脚本」那六个字**还是不够**（§128）：注释里也有一份。
  //   实测 M18：把上面那条真守卫（响亮拒绝）整行删掉、语法合法，自检仍 **14/14 · EXIT=0**。
  //   ⇒ 锚点要取「没有对应脚本」六字**加上紧随其后的那段插值调用**（全文仅 1 处），
  //     用 includes 精确匹配（这段里有 $ { }，走正则会被当成元字符）。
  //   ⚠⚠ 写这条说明时我**把锚点原文抄进了注释**，全文命中立刻变成 2 次 ——
  //     注释自己成了新的顶替者，M18 又变绿。**说明文字绝不能复现锚点**。
  if (!src.includes('没有对应脚本 ${fixture' + 'Script')) return false
  // 接线还在：harness 真的按名字拼出脚本路径并调用了 --induce / --restore
  const harness = readFileSync(join(ROOT, 'scripts', 'maestro-run.mjs'), 'utf8')
  if (!/\$\{name\}-fixture\.mjs/.test(harness)) return false
  if (!harness.includes("'--induce'") || !harness.includes("'--restore'")) return false
  // 关键：还原必须挂在 finally 上。挂在「flow 成功之后」的话，
  // flow 一红就把哨兵模型留在库里 = 用户的语音转写从此是坏的。
  return /finally\s*\{[^}]*runFixture/.test(harness)
}

/**
 * `reset` 必须与 `fixture` 保持两种语义。
 *
 * 验证点：
 *   1. 配置里 reset 是独立字段，且同时声明两者会被拒；
 *   2. harness 跑 reset 时**不传任何参数**（清场脚本不接受 --induce/--restore）；
 *   3. harness 的 finally 只对 fixture 调 --restore，reset 不进还原分支。
 * 第 3 条是安全性的要害：若 reset 也走还原，清场就会被再跑一遍，
 * 把 flow 刚建的卡组连同断言依据一起删掉。
 */
function resetIsDistinct() {
  const src = readFileSync(join(ROOT, 'scripts', 'check-maestro-flows.mjs'), 'utf8')
  if (!/ENTRY_KEYS = new Set\(\[[^\]]*'reset'/.test(src)) return false
  if (!new RegExp('同时声明了 ' + 'fixture 和 reset').test(src)) return false
  const harness = readFileSync(join(ROOT, 'scripts', 'maestro-run.mjs'), 'utf8')
  // reset 走「无参数」调用
  if (!/runFixture\(fx\.name, undefined\)/.test(harness)) return false
  // finally 里只认 kind === 'fixture'
  if (!/fx\?\.kind === 'fixture'/.test(harness)) return false
  // 且 reset 分支里不得出现 --induce / --restore
  const resetBranch = harness.slice(harness.indexOf("fx.kind === 'reset'"))
  const branchEnd = resetBranch.indexOf('} else')
  if (branchEnd > 0 && /--(induce|restore)/.test(resetBranch.slice(0, branchEnd))) return false
  return true
}

if (process.argv.includes('--selftest')) {
  console.log('[check-maestro-flows] 自检：验证本检查仍能报错')
  selftest()
  process.exit(0)
}

let bad = 0
const corpus = loadCorpus()
for (const e of CFG.entries) bad += checkFlow(e, corpus)
reportCoverage()
console.log(`\n结果: ${bad === 0 ? 'OK' : bad + ' 项问题'}`)
process.exit(bad === 0 ? 0 : 1)
