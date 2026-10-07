// check-ci-trigger-surface.mjs —— 门禁的「CI 接线」只对了一半账。
//
// 规则 5（run-gates.mjs）已经挡住了什么
// --------------------------------------
//   「gates 里的每条 check:* 都在 ciRuns 或 ciCoveredElsewhere 里」——
//   也就是**谁执行**这件事被核对过。2026-10-07 实测这条核对是真的有牙的。
//
// 它没核对的那一半
// ----------------
//   执行它的那个 workflow，**在什么样的改动上才会启动**。
//
// 实测（2026-10-07）本仓的 5 个 workflow 的 pull_request 触发面合起来是：
//   backend/** · backend/internal/db/** · deploy/acc-integration/** · e2e/web/**
//   frontend/** · frontend/src/** · frontend/vite.config.ts · Dockerfile*
//   test-evidence/PR11/** · 各 workflow 自身
// 也就是说 **没有一条 paths 覆盖仓库根的 `scripts/`**。
//
// 后果不是「门禁逻辑错了」，是**门禁在某些 PR 上根本没启动**：
//   · 改 backend/**/*.go → 触发 backend.yml，但那份 workflow 不跑 gates-parity
//     ⇒ `check:gofmt`（被测面 1133 个 backend 路径）不在 PR 上执行。
//   · 改 scripts/check-gofmt.mjs 本身 → 任何 workflow 都不触发
//     ⇒ 可以改掉门禁的实现，而 CI 一个 job 都不启动。
//
// ★ 与「守卫存在但没人执行」是同一族事故（gates.json 的 _notGates_why 记的
//   146 个孤儿测试、check:test-coverage 长期只在本机跑），只是又高了一层：
//   那两次是「忘了把名字接进名单」，这次是**名字接进了名单，但没人按下启动键**。
//
// 本门禁做什么、不做什么
// ----------------------
// 做：**量事实**——gates.json 声称的 CI 接线，与 workflow 实际写下的东西对账。
//     一共三张脸，任意一张对不上都会让门禁「配了但没跑」：
//       ① 触发面：门禁的实现文件，是否落在某个 workflow 的 paths 内（会启动吗）
//       ② 驱动：是否真有 workflow 执行 `run-gates.mjs --ci`（ciRuns 谁来跑）
//       ③ 豁免：ciCoveredElsewhere 每一条，是否真有兑现路径
// 不做：不判断「该不该把这些路径接进触发面」——那是取舍（会让每个 backend PR
//     都跑一次 26 条前端门禁，有实打实的 CI 时间成本），由属主拍板。
//     所以现状被钉成基线，只对**新增**报警（棘轮，与 check-dead-api 同款）。
//
// 用法
// ----
//   node scripts/check-ci-trigger-surface.mjs            # 对账 + 棘轮
//   node scripts/check-ci-trigger-surface.mjs --explain   # 逐条列证据
//   node scripts/check-ci-trigger-surface.mjs --update-baseline

import { readFileSync, existsSync, writeFileSync, readdirSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { dirname, join, resolve, relative, basename } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const FRONTEND = join(here, '..')
const REPO = join(FRONTEND, '..')
const BASELINE = join(here, 'ci-trigger-surface-baseline.json')

const pkg = JSON.parse(readFileSync(join(FRONTEND, 'package.json'), 'utf8'))
const doc = JSON.parse(readFileSync(join(FRONTEND, 'gates.json'), 'utf8'))
const ciRuns = doc.ciRuns || []

// ---- 1. 解析 workflow 的触发面 ---------------------------------------------
// 只认 `on:` 块里的 `paths:` 子块。push 与 pull_request 分开收，因为
// 「push 上没跑」与「PR 上没跑」后果不同（前者合并后仍会补跑一次）。
//
// ★★ 三种状态必须分开，混成一种就会把读数算反（v1 就在这里栽过）：
//   ① 事件键**不存在**（e2e-web.yml 根本没有 pull_request:）
//      ⇒ 这个 workflow 在该事件上**从不启动**，对覆盖面零贡献。
//   ② 事件键存在、但**没有 paths**（frontend.yml 的 push）
//      ⇒ **无过滤，全部触发**。v1 把 `paths: []` 当成「没覆盖」，
//        于是 push 列整列显示「未覆盖」，而真相是「每次 push 都跑」。
//   ③ 事件键存在且有 paths ⇒ 按模式匹配。
// v1 的报表里印着「（无 paths ⇒ 全触发）」，判定却按 ③ 算——
// **输出与判定自相矛盾，而且矛盾的那一列还是绿的**，比单纯错更危险。
// 加 `present` 字段就是为了让 ① 和 ② 不再共用同一个空数组。
function readWorkflowTriggers(file) {
  const lines = readFileSync(file, 'utf8').split('\n')
  const out = {
    push: { present: false, paths: [], branches: [] },
    pull_request: { present: false, paths: [], branches: [] },
  }
  let i = 0
  let inOn = false
  // 事件名用「当前所处的缩进层」跟踪，而不是回头猜。
  // 上一版硬编码「4 个空格」（push/pull_request 在 `on:` 下缩进 2），
  // 于是事件名永远认不出来 ⇒ paths 收成 0 条。
  // ★ 那次失败被本门自己的「触发面为空就拒绝给结论」挡住了（exit 3），
  //   这正是那条守卫存在的理由：解析器坏了不会伪装成「全部门禁未覆盖」。
  let owner = null
  let ownerIndent = -1
  while (i < lines.length) {
    const line = lines[i]
    if (/^on:\s*$/.test(line)) { inOn = true; i += 1; continue }
    // `on:` 块结束：下一个顶格键
    if (inOn && /^\S/.test(line)) { inOn = false; break }
    if (!inOn) { i += 1; continue }

    const indentOf = (s) => s.length - s.trimStart().length
    if (line.trim() === '') { i += 1; continue }

    const ev = line.match(/^(\s+)(push|pull_request|pull_request_target|workflow_dispatch):\s*$/)
    if (ev) {
      owner = ev[2]
      ownerIndent = ev[1].length
      if (owner in out) out[owner].present = true
      i += 1
      continue
    }
    // branches 是 paths 之外的另一半过滤流。本门**不**用它做判定
    // （它只决定「哪些分支」，不决定「哪些文件」），但必须打印出来：
    // 否则报表上「无 paths ⇒ 全触发」会被读成「任何分支任何文件都触发」。
    const br = line.match(/^(\s+)branches:\s*\[(.*)\]\s*$/)
    if (br) {
      const ownerHere = owner !== null && br[1].length > ownerIndent ? owner : null
      if (ownerHere && ownerHere in out) {
        out[ownerHere].branches = br[2]
          .split(',')
          .map((x) => x.trim().replace(/^['"]|['"]$/g, ''))
          .filter(Boolean)
      }
      i += 1
      continue
    }
    // ★ 自保守卫（设计文档 §175.6）。本函数只认 `paths:` 的**块序列**写法：
    //   若有人写成内联 `paths: ["a", "b"]`，下面那条正则**匹配不上这一行** ⇒
    //   paths 保持空数组 ⇒ 按「事件键存在但无 paths ⇒ 全触发」处理 ⇒
    //   **覆盖面被高估 ⇒ 静默绿**，真有洞时本门看不见，而且连 exit(3) 都不触发。
    //   ★ 与 `branches` 正好相反（它只认内联），但 branches 不参与判决
    //     （输出里印着「本门不建模，只显示」），所以只有 paths 这一格有后果。
    //   宁可拒绝给结论，也不把「认不出」当成「无过滤」。
    if (/^\s+paths:\s*\S/.test(line)) {
      console.error(`❌ ${basename(file)} 第 ${i + 1} 行用了「paths: 冒号后有内容」的写法。`)
      console.error('  本门只认块序列（`- "a/b"` 逐行），认不出内联数组 ⇒ 会把它当成「无过滤」。')
      console.error('  而「无过滤」= 全触发 = 覆盖面被高估 ⇒ 静默绿。拒绝给结论。')
      process.exit(3)
    }
    if (/^\s+paths:\s*$/.test(line)) {
      const indent = indentOf(line)
      // 缩进比事件名浅 ⇒ 这个 paths 不属于任何已知事件（保守：不收）
      const ownerHere = owner !== null && indent > ownerIndent ? owner : null
      i += 1
      while (i < lines.length) {
        const t = lines[i]
        if (t.trim() === '') { i += 1; continue }
        if (indentOf(t) <= indent && t.trim() !== '') break
        const m = t.match(/^\s*-\s*['"]?(.+?)['"]?\s*$/)
        if (m && ownerHere && ownerHere in out) out[ownerHere].paths.push(m[1])
        i += 1
      }
      continue
    }
    i += 1
  }
  return out
}

const WORKFLOW_DIR = join(REPO, '.github', 'workflows')
const perFile = []
const wfNames = existsSync(WORKFLOW_DIR)
  ? readdirSync(WORKFLOW_DIR).filter((n) => n.endsWith('.yml') || n.endsWith('.yaml')).sort()
  : []
for (const name of wfNames) perFile.push({ name, ...readWorkflowTriggers(join(WORKFLOW_DIR, name)) })
if (!wfNames.length) {
  console.error(`❌ 读不到任何 workflow：${WORKFLOW_DIR}`)
  console.error('  没有触发面可比对。本门拒绝把「没读到」当成「触发面为空」。')
  process.exit(3)
}

/** 一个仓库相对路径是否落在某个 paths 模式内。 */
function pathMatches(rel, pattern) {
  if (pattern.endsWith('/**')) {
    const stem = pattern.slice(0, -2) // 去掉 '**'
    return rel === stem || rel.startsWith(stem)
  }
  if (pattern.includes('*')) return globMatch(rel, pattern)
  return rel === pattern
}

function globMatch(str, pattern) {
  const rx = pattern
    .split('**')
    .map((s) => s.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '[^/]*'))
    .join('.*')
  return new RegExp('^' + rx + '$').test(str)
}

/**
 * 某个 workflow 是否会在该事件上因这个路径而启动。
 *
 * ⚠ 必须**逐 workflow 判**，不能只把 paths 求并集——并集丢掉了
 * 「有某个 workflow 压根没有 paths 过滤」这一事实，而那恰好等于**全触发**。
 */
function workflowCovers(wfName, relPath, kind) {
  const w = perFile.find((x) => x.name === wfName)
  if (!w || !w[kind].present) return false
  return w[kind].paths.length === 0 || w[kind].paths.some((p) => pathMatches(relPath, p))
}

// ---- 1b. 解析 workflow 的 run: 命令 ----------------------------------------
// 两种写法都要认：单行 `run: npm run x` 与块标量 `run: |` + 缩进多行。
// ★ 漏掉块标量是最容易犯且最不易自查的一种：11 处 `run: |` 里
//   backend.yml 的 smart-quote 门禁就在块里，只认单行会把「已手列」误判成「没接」。
function readRunCommands(file) {
  const lines = readFileSync(file, 'utf8').split('\n')
  const out = []
  let i = 0
  let job = null
  while (i < lines.length) {
    const line = lines[i]
    const j = line.match(/^ {2}(\S[^:]*):\s*$/)
    if (j) job = j[1]
    const r = line.match(/^(\s+)run:\s*(.*)$/)
    if (r) {
      const indent = r[1].length
      if (r[2].trim() && r[2].trim() !== '|') {
        out.push({ file, job, text: r[2].trim() })
        i += 1
        continue
      }
      // 块标量：收下所有缩进更深的行
      i += 1
      const buf = []
      while (i < lines.length) {
        const t = lines[i]
        if (t.trim() === '') { i += 1; continue }
        if (t.length - t.trimStart().length <= indent) break
        buf.push(t.trim())
        i += 1
      }
      out.push({ file, job, text: buf.join('\n') })
      continue
    }
    i += 1
  }
  return out
}

const runCommands = []
for (const name of wfNames) runCommands.push(...readRunCommands(join(WORKFLOW_DIR, name)))

// 自证：一条 run: 都没解析出来 ⇒ 解析器坏了，不许把「没读到」当成「没人跑」。
if (!runCommands.length) {
  console.error('❌ 一个 run: 命令都没解析出来。')
  console.error('  这不是「workflow 里没有命令」，是解析器没认出单行/块标量两种写法。')
  process.exit(3)
}

// ---- 2. 每条 ciRuns 门禁的实现文件 ----------------------------------------
// 从 npm script 的命令体里取 `scripts/xxx.mjs` 形态的 token，
// 相对 FRONTEND 解析成仓库相对路径。取命令体而不是「脚本名」，
// 因为 --selftest && 真跑 这种写法会有两处调用，且 check:icons 是两条不同脚本。
function implFilesOf(command) {
  const out = new Set()
  const re = /(?:^|\s)((?:\.\.\/)?scripts\/[A-Za-z0-9_.\-]+\.mjs)/g
  let m
  while ((m = re.exec(command))) {
    out.add(relative(REPO, resolve(FRONTEND, m[1])).split('\\').join('/'))
  }
  return [...out]
}

// ---- 2b. 脸②：ciRuns 的驱动在哪 --------------------------------------------
// ★ 这是本门最要紧的一条，也是 §114 之前完全没有对过的一条。
//   ciRuns 是一份**数据**，它自己不会跑；跑它的是 workflow 里那句
//   `node scripts/run-gates.mjs --ci`。把那一句删掉：
//     · run-gates.mjs 规则 5 只做 gates.json ↔ package.json 的文本对账 → 仍然绿
//     · 触发面核对（脸①）与 on.paths 无关 → 仍然绿
//     · gates-parity job 只剩 --list 一步，照样通过
//   ⇒ 26 条 CI 门禁集体静默停跑，而仓库里每一道门都报「通过」。
//   这与「护栏存在但没人执行」同族，但比 §114 那批更彻底：
//   §114 是**个别门禁**在某些 PR 上不跑，这里是**全部**在所有 PR 上不跑。
const driverHits = runCommands.filter(
  (c) => /run-gates\.mjs\b/.test(c.text) && /(^|\s)--ci(\s|$)/m.test(c.text),
)
const driverFacts = driverHits.map((c) => ({
  file: relative(REPO, c.file).split('\\').join('/'),
  job: c.job,
}))
// 真正执行 ciRuns 门禁的 workflow（ciRuns 是一份数据，靠 --ci 那一步驱动）。
const runnerWorkflows = [...new Set(driverFacts.map((d) => basename(d.file)))]

/**
 * 一道门禁的实现文件，在该事件上是否被**跑它的那几个 workflow** 覆盖。
 *
 * ⚠ 这条只回答「有没有 workflow 会启动」，**不回答「跑这道门禁的那个会不会启动」**。
 *   两者在本仓恰好会分叉：backend.yml 的 push 没有 paths ⇒ 任何 push 都启动它，
 *   可它压根不跑 gates。早期版本因此会把 check:gofmt 的 push 列判成「覆盖」——
 *   而真相是「启动的是 backend.yml，gofmt 仍然不跑」。
 *   ★ 所以判据取的是 `runnerWorkflows`（谁真正驱动 ciRuns 那一步），
 *     **不是** `perFile`（全部 workflow）。这两个集合会分叉，混用会把洞读成没洞。
 *
 * 同族的第三格见设计文档 §172：接线状态要拆成「谁执行 / 会不会被启动 / 启动后跑不跑」
 * 三列，压成两列就会给出一个方向明确、而结论相反的读数。
 */
function coveredByRunner(relPath, kind) {
  if (!runnerWorkflows.length) return false
  return runnerWorkflows.some((w) => workflowCovers(w, relPath, kind))
}

const facts = []
for (const name of ciRuns) {
  const command = (pkg.scripts || {})[name]
  const files = implFilesOf(command || '')
  const uncoveredPr = files.filter((f) => !coveredByRunner(f, 'pull_request'))
  const uncoveredPush = files.filter((f) => !coveredByRunner(f, 'push'))
  facts.push({ gate: name, files, uncoveredPr, uncoveredPush })
}


// ---- 2c. 脸③：ciCoveredElsewhere 每条是否有兑现路径 --------------------------
// 这份名单是**手工声明的豁免**，理由写在值里。理由是散文，判据不能读散文
// （那正是「按字面量找」那一族的坑）。所以拆成两条**各自可独立验证**的臂：
//   臂 A：workflow 里真的手列了 `npm run <name>`  → 兑现路径 = 那一步
//   臂 B：workflow 里没有，但它是 test:all 枚举的子集 → 兑现路径 = test:all 那一步
// 两条都不满足 ⇒ 这条豁免没有任何兑现路径，门禁在 CI 上不执行。
// ★ 臂 B 还要求 test:all 本身真的在手列处（臂 A）：否则「被 test:all 覆盖」
//   是一张空头支票——覆盖者自己都没跑。
const ALL_TESTS = 'test:all'
let testAllFiles = null
function filesTestAllRuns() {
  if (testAllFiles) return testAllFiles
  const res = spawnSync(process.execPath, [join(here, 'run-mjs-tests.mjs'), '--print-files'], {
    cwd: FRONTEND,
    encoding: 'utf8',
  })
  if (res.status !== 0) return null
  // 用它自己的枚举（单一事实源），不自写一份遍历
  testAllFiles = new Set(
    res.stdout
      .split('\n')
      .map((s) => s.trim())
      .filter((s) => s.startsWith('src/'))
      .map((s) => s.split('\\').join('/')),
  )
  return testAllFiles
}

function handListed(name) {
  const re = new RegExp('npm\\s+run\\s+' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\b')
  return runCommands.some((c) => re.test(c.text))
}

function readdirDeep(base, glob) {
  // 只处理 `dir/*.ext` 这一种形态（仓库里实际用到的就是它）
  const m = glob.match(/^(.*)\/([^/]+)$/)
  if (!m) return []
  const dir = join(base, m[1])
  if (!existsSync(dir)) return []
  const rx = new RegExp('^' + m[2].replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '[^/]*') + '$')
  return readdirSync(dir).filter((n) => rx.test(n)).map((n) => `${m[1]}/${n}`)
}

/** 该门禁命令体里点名的测试文件，是否都在 test:all 的枚举内。 */
function coveredByTestAll(name) {
  const all = filesTestAllRuns()
  if (!all) return null // 拒绝给结论，而不是「算作通过」
  const toks = (pkg.scripts || {})[name]?.split(/\s+/) || []
  const wanted = []
  for (const t of toks) {
    if (t === 'node' || t === '--test') continue
    if (t.includes('*')) wanted.push(...readdirDeep(FRONTEND, t))
    else if (t.includes('/')) wanted.push(t)
  }
  if (!wanted.length) return false
  return wanted.every((w) => all.has(w.split('\\').join('/')))
}

const exemptionFacts = []
for (const [name, why] of Object.entries(doc.ciCoveredElsewhere || {})) {
  const armA = handListed(name)
  const armB = armA ? null : coveredByTestAll(name)
  // 覆盖者自己必须真的在手列处，否则臂 B 是一张空头支票
  const anchorOk = armB === true && handListed(ALL_TESTS)
  exemptionFacts.push({ name, why, armA, armB, anchorOk, ok: armA || (armB === true && anchorOk) })
}

// ---- 3. 棘轮 ---------------------------------------------------------------
// 现状（未覆盖面）被钉成基线，只对**新增**报警。
// 理由同上：把 backend/** 接进前端触发面会让每个后端 PR 多跑 25 条前端门禁，
// 这是取舍不是 bug；本门不代拍，只保证它不会悄悄变大。
const key = (f) => `trigger :: ${f.gate} :: ${f.uncoveredPr.join(',')}`
const curKeys = [
  ...facts.filter((f) => f.uncoveredPr.length).map(key).sort(),
  // 脸②与脸③的现状也进同一份棘轮。当前二者都满足，因此不产生条目。
  ...(driverFacts.length ? [] : ['driver :: 无任何 workflow 执行 run-gates.mjs --ci']),
  ...exemptionFacts.filter((f) => !f.ok).map((f) => `exemption :: ${f.name}`).sort(),
]

let baseline = { note: '', pr: [] }
if (existsSync(BASELINE)) baseline = JSON.parse(readFileSync(BASELINE, 'utf8'))

if (process.argv.includes('--update-baseline')) {
  writeFileSync(
    BASELINE,
    JSON.stringify(
      {
        note: '每条 ciRuns 门禁的实现文件未被任何 workflow 的 pull_request paths 覆盖的现状；只许减少不许增加。',
        pr: curKeys,
      },
      null,
      2,
    ) + '\n',
    'utf8',
  )
  console.log(`✅ 基线已更新（${curKeys.length} 条未覆盖）`)
  process.exit(0)
}

const baseSet = new Set(baseline.pr || [])
const added = curKeys.filter((k) => !baseSet.has(k))
const removed = (baseline.pr || []).filter((k) => !curKeys.includes(k))

console.log(
  `【CI 触发面卡口】ciRuns ${ciRuns.length} 条 · pull_request 触发面 ` +
    `${perFile.filter((w) => w.pull_request.present).length}/${wfNames.length} 个 workflow · ` +
    `push 触发面 ${perFile.filter((w) => w.push.present).length}/${wfNames.length} 个 workflow`,
)

// 一个 workflow 都不在 pull_request 上 ⇒ 解析器坏了：拒绝给结论。
// ★ 注意不能写成「paths 一条都没有」——`paths: []` 本身是**合法且全触发**的状态，
//   拿它当「解析失败」会把「过滤很宽」误报成「门禁坏了」。
if (!perFile.some((w) => w.pull_request.present)) {
  console.error('\n❌ 没有一个 workflow 声明了 pull_request 事件。')
  console.error('  这不是「触发面为空」，是解析器没认出 on: 块里的事件键。')
  console.error('  在解析器修好之前，本门禁会把它误报成「全部门禁未覆盖」。')
  process.exit(3)
}

if (process.argv.includes('--explain')) {
  console.log('\n--- 逐条证据 ---')
  for (const f of facts) {
    const mark = f.uncoveredPr.length ? '✗' : '✅'
    console.log(`${mark} ${f.gate}`)
    for (const file of f.files) {
      console.log(
        `      ${file.padEnd(46)} PR=${coveredByRunner(file, 'pull_request') ? '覆盖' : '未覆盖'}` +
          ` push=${coveredByRunner(file, 'push') ? '覆盖' : '未覆盖'}` +
          `  （由 ${runnerWorkflows.join(', ') || '（无！）'} 执行）`,
      )
    }
  }
  console.log('\n--- 各 workflow 的触发面 ---')
  for (const w of perFile) {
    console.log(`  ${w.name}`)
    console.log(
      `      PR   : ${
        !w.pull_request.present
          ? '（该 workflow 无 pull_request 事件 ⇒ 从不在 PR 上启动）'
          : w.pull_request.paths.join(', ') || '（有 pull_request 但无 paths ⇒ 全触发）'
      }`,
    )
    console.log(
      `      push : ${
        !w.push.present
          ? '（该 workflow 无 push 事件 ⇒ 从不在 push 上启动）'
          : w.push.paths.join(', ') || '（有 push 但无 paths ⇒ 全触发）'
      }`,
    )
    const brs = [...new Set([w.push.branches, w.pull_request.branches].flat())]
    console.log(`      分支 : ${brs.length ? brs.join(', ') : '（未限制）'}${brs.length ? '（本门不建模，只显示）' : ''}`)
  }
  console.log('\n--- 脸② ciRuns 的驱动 ---')
  for (const d of driverFacts) console.log(`  ✅ ${d.file} · job ${d.job}`)
  if (!driverFacts.length) console.log('  ❌ 没有任何 workflow 执行 `run-gates.mjs --ci`')
  console.log(`\n--- 脸③ ciCoveredElsewhere ${exemptionFacts.length} 条的兑现路径 ---`)
  for (const e of exemptionFacts) {
    const arm = e.armA ? '臂A 手列' : e.armB === true ? '臂B test:all 子集' : e.armB === false ? '臂B 不成立' : '臂B 取不到读数'
    const anchor = e.armA ? '' : e.armB === true ? (e.anchorOk ? '（锚点 test:all 在手列处）' : '（❌ 锚点 test:all 不在手列处 ⇒ 空头支票）') : ''
    console.log(`  ${e.ok ? '✅' : '❌'} ${e.name.padEnd(22)} ${arm}${anchor}`)
  }
}

// test:all 的枚举取不到 ⇒ 臂 B 无法判定 ⇒ 拒绝给结论，不当「通过」。
if (filesTestAllRuns() === null) {
  console.error('\n❌ 取不到 test:all 的文件枚举（run-mjs-tests.mjs --print-files 未成功）。')
  console.error('  ciCoveredElsewhere 里「已被 test:all 覆盖」那一档因此**无法判定**。')
  console.error('  本门不把「判不了」算成「通过」。')
  process.exit(3)
}

if (added.length) {
  console.error()
  for (const a of added) {
    if (a.startsWith('driver ::')) {
      console.error(`❌ 新增：${a}`)
      console.error('  ciRuns 是一份数据，跑它的是 workflow 里那句 `run-gates.mjs --ci`。')
      console.error('  没有那一句 ⇒ 26 条 CI 门禁在所有 PR 上集体静默停跑，')
      console.error('  而 gates-parity job 只剩 --list，照样通过 ⇒ 每一道门都报「通过」。')
    } else if (a.startsWith('exemption ::')) {
      console.error(`❌ 新增：${a} —— 这条 ciCoveredElsewhere 豁免没有任何兑现路径`)
      console.error('  既没在 workflow 里手列，也不是 test:all 枚举的子集 ⇒ CI 上不执行。')
    } else {
      console.error(`❌ 新增「CI 装了门禁但 PR 上不会启动」：${a}`)
      console.error('  这是「护栏存在但没人执行」的高层版本：gates.json 记了它属于 CI，')
      console.error('  但没有任何 workflow 的 paths 覆盖它的实现文件 ⇒ 改这块代码的 PR 上它一次都不跑。')
      console.error('  两种修法，选一种：① 把该路径接进对应 workflow 的 paths；')
      console.error('  ② 确认这是有意取舍，把现状记进 gates.json 的理由里。')
    }
  }
  process.exit(1)
}
if (removed.length) {
  console.log()
  // 措辞刻意不用「已修复」：触发面变宽也可能是误加，只说明差集变了。
  for (const r of removed) console.log(`⤵️ 基线里有、本次已覆盖：${r} —— 若是有意的，跑 --update-baseline 落盘。`)
}
console.log(`\n✅ CI 触发面未新增未覆盖面（棘轮通过，基线 ${baseSet.size} 条）`)
process.exit(0)