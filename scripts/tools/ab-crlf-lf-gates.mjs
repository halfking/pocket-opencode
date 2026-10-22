// ab-crlf-lf-gates.mjs —— CRLF 检出树 × LF 检出树的**全量 CI 门禁对照**。
//
// 用途（2026-10-22 §960.7.9 的结论固化）
// ----------------------------------------
// 判定「本仓的门禁里有没有还在依赖**检出形态**（行尾 / 分隔符 / 路径形态）的」。
//
// ⚠ 为什么不能用静态扫描回答
//   静态扫描（scan-eol-sensitivity.mjs）**不能**回答这个问题 ——
//   实测 15 处候选里 13 处是误报（注释里的历史代码 / 另一条数据流 / 已归一的输入），
//   而且扫描器自己的接线判定有盲区（先少数成 3、又多数成 8，最终权威值是 6）。
//   ⇒ 唯一可信的判据是**同一 commit 在两种检出形态下跑出的结论是否一致**。
//
// 为什么是「工具」不是「门禁」
//   它要**建一个 worktree**、要**跑两遍全量门禁**（本机实测单遍 249.5s）
//   ⇒ 不能接进 gates（会让每跑一次门禁就多花 5 分钟 + 占一个 worktree 槽位）。
//   ⇒ 定位：**定期/合并前手工跑的对照工具**，不是每次提交都跑的门。
//
// 用法
// ----
//   node scripts/tools/ab-crlf-lf-gates.mjs [ref] [选项]
//
//   ref          要对照的 commit（默认 origin/main）
//   --root=PATH  仓库根（默认：按本脚本位置自动推导）
//   --wt=PATH    LF worktree 目录（默认 <root>/../_po-lf-main）
//   --out=DIR    日志输出目录（默认 <root>/.scratch）
//   --keep-wt    对照完**不删** LF worktree（默认：删掉，避免占槽位）
//   --no-gates   只做 LF 树自证，不跑门禁（调试用）
//
// 退出码
// ------
//   0  两侧结论一致且都通过（⇒ 该 commit 上没有检出形态敏感的门禁）
//   1  两侧不一致（⇒ 存在检出形态敏感的门禁，详见两份日志的差异）
//   2  LF 树自证失败（⇒ 对照装置本身坏了，结论无效）
//
// ⚠⚠ Windows 上的两个坑（本脚本已规避，改动时别改回去）
//   ① 不能用 `cmd /c "... > log 2>&1"`：cmd 的重定向目标里**正斜杠会被当成开关分隔符**
//      ⇒ 报「文件名、目录名或卷标语法不正确」（本轮实测踩到）
//      ⇒ 改成**纯管道捕获 + node 自己落盘**（顺带绕开 PowerShell `>` 的 UTF-16LE 陷阱）。
//   ② LF worktree 必须用 `git -c core.autocrlf=false worktree add` 建，
//      且建完要用 `git ls-files --eol` **自证**真的是 w/lf ——
//      否则对照的是「两棵 CRLF 树」⇒ 结论恒等于「一致」⇒ 假绿。
import { execFileSync, spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const SELF = fileURLToPath(import.meta.url)

// ── 参数 ────────────────────────────────────────────────────────────────────
const argv = process.argv.slice(2)
const positional = argv.filter((a) => !a.startsWith('--'))
const opt = (name, dflt) => {
  const hit = argv.find((a) => a.startsWith(`--${name}=`))
  return hit ? hit.slice(name.length + 3) : dflt
}
const flag = (name) => argv.includes(`--${name}`)

const DEFAULT_ROOT = resolve(dirname(SELF), '..', '..')
const ROOT = resolve(opt('root', DEFAULT_ROOT))
const WT = resolve(opt('wt', join(ROOT, '..', '_po-lf-main')))
const OUT = resolve(opt('out', join(ROOT, '.scratch')))
const KEEP_WT = flag('keep-wt')
const NO_GATES = flag('no-gates')

const sh = (cmd, args, cwd = ROOT) =>
  execFileSync(cmd, args, { cwd, encoding: 'utf8', maxBuffer: 256 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'] })

const ref = positional[0] || sh('git', ['rev-parse', 'origin/main']).trim()
const short = ref.slice(0, 9)

mkdirSync(OUT, { recursive: true })
const LOG_CRLF = join(OUT, 'gates-crlf.log')
const LOG_LF = join(OUT, 'gates-lf.log')

console.log(`仓库根   = ${ROOT}`)
console.log(`LF 树    = ${WT}`)
console.log(`日志目录 = ${OUT}`)
console.log(`对照 ref = ${short}`)

// ── 1. LF worktree（core.autocrlf=false ⇒ 真的 LF 检出）─────────────────────
let createdWt = false
if (!existsSync(WT)) {
  console.log('创建 LF worktree …')
  sh('git', ['-c', 'core.autocrlf=false', 'worktree', 'add', '--detach', WT, ref])
  createdWt = true
} else {
  console.log('复用既有 LF worktree，切到 ref …')
  sh('git', ['checkout', '--detach', ref], WT)
}
// node_modules / dist 用 junction 指回主工作区（它们是 gitignore 的）
for (const name of ['node_modules', 'dist']) {
  const link = join(WT, 'frontend', name)
  const target = join(ROOT, 'frontend', name)
  if (!existsSync(link) && existsSync(target)) {
    spawnSync('cmd', ['/c', 'mklink', '/J', link, target], { stdio: 'inherit' })
  }
}
// ★ 自证：这棵树真的是 LF（否则对照的是两棵 CRLF 树 ⇒ 假绿）
const eol = sh('git', ['ls-files', '--eol', 'frontend/src/features/notes/note-todo-persist.ts'], WT)
if (!/w\/lf/.test(eol)) {
  console.error('✗ 这棵树不是 LF 检出（见上面 --eol 输出）⇒ 对照无效，停下。')
  console.error(eol)
  process.exit(2)
}
console.log('LF 树自证：w/lf ✓')

if (NO_GATES) {
  console.log('--no-gates ⇒ 自证后停下。')
  process.exit(0)
}

// ── 2. 两边各跑一遍全量门禁 ───────────────────────────────────────────────
function runGates(cwd, log) {
  console.log(`跑门禁：${cwd}`)
  const r = spawnSync(process.execPath, ['frontend/scripts/run-gates.mjs', '--ci'], {
    cwd, encoding: 'utf8', maxBuffer: 256 * 1024 * 1024,
  })
  const text = `${r.stdout || ''}${r.stderr || ''}`
  writeFileSync(log, text, 'utf8')
  console.log(`  ⇒ ${log}（${text.length} 字符，退出码 ${r.status}）`)
  return r.status
}
const codeCrlf = runGates(ROOT, LOG_CRLF)
const codeLf = runGates(WT, LOG_LF)

// ── 3. 对照 ────────────────────────────────────────────────────────────────
const verdict = (log) => {
  const t = readFileSync(log, 'utf8')
  const m = /\[gates\] (✅ 全部 \d+ 项通过|❌ 第 \d+ 项 .*失败)/.exec(t)
  const list = [...t.matchAll(/\[gates\] ── \[\d+\/\d+\] (.+?) ──/g)].map((x) => x[1].trim())
  return { verdict: m ? m[1] : '（没找到结论行）', list }
}
const a = verdict(LOG_CRLF)
const b = verdict(LOG_LF)

console.log('\n──────── 对照 ────────')
console.log(`CRLF 工作树：${a.verdict}`)
console.log(`LF   检出树：${b.verdict}`)
const sameList = JSON.stringify(a.list) === JSON.stringify(b.list)
console.log(`跑过的门列表一致？${sameList ? '是 ✓' : '否 ✗'}`)

const ok = codeCrlf === 0 && codeLf === 0 && sameList && a.verdict.startsWith('✅') && b.verdict.startsWith('✅')
if (ok) {
  console.log('\n✓ 结论：两棵检出形态下，CI 门禁的**结论与门列表逐条一致** ⇒')
  console.log('  **当前 commit 上没有「仍在依赖检出形态」的门禁**。')
  console.log('  ⇒ 残余风险只剩「未来某次改动可能重新引入」⇒ 防护手段是本对照脚本，不是静态扫描。')
} else {
  console.log('\n✗ 结论：两侧不一致 ⇒ 存在检出形态敏感的门禁（见上面两份日志的差异）。')
}

// ── 4. 清理 ────────────────────────────────────────────────────────────────
// ⚠ 只删**本脚本自己建**的 worktree；复用来的不动（可能是别人在用的）。
// ⚠ 删之前先拆 junction，否则 git worktree remove 会把主工作区的 node_modules 一起带走。
if (createdWt && !KEEP_WT) {
  console.log('\n清理：拆 junction + 删本脚本建的 LF worktree …')
  for (const name of ['node_modules', 'dist']) {
    const link = join(WT, 'frontend', name)
    if (existsSync(link)) spawnSync('cmd', ['/c', 'rmdir', link], { stdio: 'ignore' })
  }
  try {
    sh('git', ['worktree', 'remove', '--force', WT])
    console.log('  ⇒ 已删')
  } catch (e) {
    console.log(`  ⇒ git worktree remove 失败（${e.message?.slice(0, 120)}），改为 rm -rf 后 prune`)
    rmSync(WT, { recursive: true, force: true })
    try { sh('git', ['worktree', 'prune']) } catch { /* prune 失败不致命 */ }
  }
} else if (KEEP_WT) {
  console.log('\n--keep-wt ⇒ 保留 LF worktree。')
}

process.exit(ok ? 0 : 1)
