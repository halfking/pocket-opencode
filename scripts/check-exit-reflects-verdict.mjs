#!/usr/bin/env node
/**
 * check-exit-reflects-verdict.mjs — 门禁：跑 FAIL-able 判据的脚本，退出码必须反映判定。
 *
 * ## 为什么
 *
 * BUG-V15（邮件同步探针）与 BUG-V19（任务写路径探针）是同一个病：
 * 脚本判出一堆 FAIL，最后 `process.exit(0)`。
 * ⇒ 退出码恒为「成功」，CI / 批量 runner / `&&` 链**全都无从分辨**
 * 「跑过了」与「全绿」。绿灯是被无条件发出去的。
 *
 * 判据：脚本里若出现 FAIL-able 判据的典型结构
 *   - `const checks = []`（收集判据结果的数组），或
 *   - `const check = (n, pass, …)`（判据发射器），
 * 那么文件里**不允许**再出现**无条件**的 `process.exit(0)` 收尾。
 * 允许的是 `process.exitCode = failed ? 1 : 0` 这类按判定取值的形式。
 *
 * 「无条件」的判定要保守：只看**行首就是** `process.exit(0)` 的收尾行，
 * 且同一文件里没有任何 `process.exitCode` 赋值、没有 `if (` 包着它。
 * 宁可漏报也不误报 —— 误报会让门禁变成噪声。
 *
 * 用法：
 *   node scripts/check-exit-reflects-verdict.mjs            # 有命中 exit 1
 *   node scripts/check-exit-reflects-verdict.mjs --selftest
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SELF = path.resolve(fileURLToPath(import.meta.url))
const ROOT = path.resolve(path.dirname(SELF), '..')
const SCRIPTS = path.join(ROOT, 'scripts')

/** 该文件是否含 FAIL-able 判据结构。纯函数，供 selftest 喂样本。 */
export function hasFailableJudge(src) {
  return /const\s+checks\s*=\s*\[\s*\]/.test(src) || /const\s+check\s*=\s*\(/.test(src)
}

/** 是否存在**无条件**的 process.exit(0) 收尾。 */
export function hasUnconditionalExit0(src) {
  if (/process\.exitCode\s*=/.test(src)) return false          // 已经按判定取值了
  const lines = src.split(/\r?\n/)
  for (let i = 0; i < lines.length; i++) {
    if (!/^\s*process\.exit\(0\)\s*;?\s*$/.test(lines[i])) continue
    // 往上看两行：若被 if 包住，就不是无条件收尾
    const ctx = lines.slice(Math.max(0, i - 2), i).join('\n')
    if (/\bif\s*\(/.test(ctx)) continue
    return true
  }
  return false
}

function walk(dir, acc = []) {
  for (const e of readdirSync(dir)) {
    const p = path.join(dir, e)
    if (statSync(p).isDirectory()) { if (e !== 'node_modules' && e !== '.git') walk(p, acc) }
    else if (e.endsWith('.mjs')) acc.push(p)
  }
  return acc
}

if (process.argv.includes('--selftest')) {
  const cases = [
    ['敏感度·有判据 + 无条件 exit(0) 应判为违规', () =>
      hasFailableJudge('const checks = [];') && hasUnconditionalExit0('const checks = [];\nconsole.log(checks)\nprocess.exit(0)\n') === true],
    ['敏感度·check 发射器也算判据', () =>
      hasFailableJudge('const check = (n, pass) => {}') === true],
    ['特异度·已用 exitCode 取值 → 不算违规', () =>
      hasUnconditionalExit0('const checks=[]\nprocess.exitCode = failed ? 1 : 0\n') === false],
    ['特异度·exit(0) 被 if 包住 → 不算违规', () =>
      hasUnconditionalExit0('if (ok) {\n  process.exit(0)\n}\n') === false],
    ['特异度·没有判据结构 → 不算', () => hasFailableJudge('console.log(1)') === false],
    ['变盲·空源码不报', () => hasUnconditionalExit0('') === false && hasFailableJudge('') === false],
    ['变盲·只有 exitCode 赋值 → 不报', () => hasUnconditionalExit0('process.exitCode = 0\n') === false],
  ]

  // ★★★ 自指用例（docs/design §208 状态③「豁免不可达 + 一条都没有」）。
  //   补它**不许照抄** check-smart-quotes 那一条：那边 EXTS={.go,.sql} 而 SELF 是 .mjs，
  //   自指路径**结构上就到不了**，用例恒真（状态②）。所以下面只依赖**结构性**事实。
  //
  //   ⚠ 已实测（别把它当前提）：本文件 hasFailableJudge(SELF)=true 与
  //   hasUnconditionalExit0(SELF)=false **各自是靠头注释里的原文**成立的 ——
  //   注释里写着 `const checks = []` 和 `process.exitCode = failed ? 1 : 0`，
  //   两条正则命中的分别是**散文**。改一次措辞就会翻 ⇒ 不是可依赖的前提。
  const SELF_SRC = readFileSync(SELF, 'utf8')
  cases.push(
    // 前提（结构性）：豁免不是空转的 —— 去掉 :100，主扫描真的会把自己送进判据。
    // 实测 walk(scripts) 产出 379 个 .mjs 且含 SELF；这条钉的就是那个来源。
    ['自指·前提：walk() 真的枚举到 SELF，去掉 :100 豁免就会自报', () =>
      SELF.endsWith('.mjs') && walk(SCRIPTS).some((f) => path.resolve(f) === SELF)],
    // ★ 直接对着 §208 那个风险：**语法层**看自身有没有行首的无条件 exit(0)。
    //   为什么不能只靠上面那条合取：`hasUnconditionalExit0` 开头就
    //   `if (/process\.exitCode\s*=/.test(src)) return false`，而本文件那处
    //   `process.exitCode = failed ? 1 : 0` **只在头注释里** ⇒ 真有人加一行
    //   行首 exit(0)，合取**照样为假**、上面那条**照样绿** —— 正好漏掉要抓的场景。
    //   ⇒ 必须有一条不经过那个早退的断言。两个条件各自独立，别并成一条：
    //     并了就分不清转红的是「自身违规了」还是「口径变了」。
    ['自指·语法层：自身没有行首的无条件 process.exit(0)', () =>
      !/^\s*process\.exit\(0\)\s*;?\s*$/m.test(SELF_SRC)],
    // 不变量：自身当前不满足主扫描那条合取 ⇒ 摘掉 :100 不改变本次结论。
    ['自指·不变量：按主扫描的合取，自身当前不违规', () =>
      (hasFailableJudge(SELF_SRC) && hasUnconditionalExit0(SELF_SRC)) === false],
    // 钉住「不变量那条为什么现在过」：判据的口径是**有 exitCode 赋值就放行**。
    // 否则它将来转红时，看不出是自身违规了、还是口径变了。
    ['自指·口径：有 exitCode 赋值时行首 exit(0) 不判违规（不变量之所以过的原因）', () =>
      hasUnconditionalExit0('process.exitCode = 0\nprocess.exit(0)\n') === false],
  )

  let bad = 0
  for (const [n, fn] of cases) {
    let p = false
    try { p = fn() === true } catch { p = false }
    if (!p) bad++
    console.log(`  ${p ? 'PASS' : 'FAIL'}  ${n}`)
  }
  console.log(`\nselftest: 实跑 ${cases.length} 例，通过 ${cases.length - bad} 例`)
  // ★ 条数下限闸（2026-10-07，docs/design §94）。原来 `selftest: N-bad/N 通过` + `exit(bad?1:0)`，
  //   cases 为空时打印「0/0 通过」并 EXIT=0，与真通过完全同形（见 §94 的实跑证据）。
  //   ★ 这道门尤其不能空转：它守的正是「退出码是否反映判定」，而它自己空转时
  //     退出码同样是 0 —— 守卫失效的样子和它要抓的病一模一样。
  //   下限只能手工改这个常量，不接受命令行参数。
  //   ⚠ 2026-10-08 提高 5 -> 11：补了 4 条自指用例（docs/design §208 状态③）。
  //     原来 7 条配下限 5，**删掉任意 2 条都还是绿的** —— 而「删掉自指用例」
  //     正是让这道门退回状态③（没人说话）的那一步，所以下限必须等于当前条数。
  const MIN_SELFTEST_CASES = 11;
  if (cases.length < MIN_SELFTEST_CASES) {
    console.error(`[exit-reflects-verdict] 自检只跑了 ${cases.length}/${MIN_SELFTEST_CASES} 例 —— 夹具循环或 push 被改过。`);
    console.error('   「0/0 通过」不是通过：守卫自己空转时的读数和它要抓的病一模一样。');
    process.exit(2);
  }
  process.exit(bad ? 1 : 0)
}

const hits = []
for (const f of walk(SCRIPTS)) {
  if (path.resolve(f) === SELF) continue
  const src = readFileSync(f, 'utf8')
  if (hasFailableJudge(src) && hasUnconditionalExit0(src)) {
    const ln = src.split(/\r?\n/).findIndex((l) => /^\s*process\.exit\(0\)\s*;?\s*$/.test(l)) + 1
    hits.push({ file: path.relative(ROOT, f), line: ln })
  }
}
for (const h of hits) console.log(`${h.file}:${h.line}  跑 FAIL-able 判据却无条件 process.exit(0)`)

if (process.argv.includes('--list')) { console.log(`\n${hits.length} 处`); process.exit(0) }
if (hits.length) {
  console.error(`\n✗ ${hits.length} 个脚本的退出码不反映判定 —— 调用方会把失败当成功。`)
  console.error('   改法：process.exitCode = failed ? 1 : 0（不要用 process.exit，那会跳过 close）')
  process.exit(1)
}
console.log('OK：跑判据的脚本退出码都反映判定')
