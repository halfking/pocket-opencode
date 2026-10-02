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
  let bad = 0
  for (const [n, fn] of cases) {
    let p = false
    try { p = fn() === true } catch { p = false }
    if (!p) bad++
    console.log(`  ${p ? 'PASS' : 'FAIL'}  ${n}`)
  }
  console.log(`\nselftest: ${cases.length - bad}/${cases.length} 通过`)
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
