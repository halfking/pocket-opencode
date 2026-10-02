// migrate-expect-origin.mjs —— 把写路径脚本里硬写的 dev 包 origin 断言改成 env 驱动。
//
// ## 为什么
//
// 设备上装的是**生产 https 包**（实测 `location.origin = https://localhost`），
// 而这批脚本第一关写的是 `if (origin !== 'http://localhost') { …exit 5 }`。
// 用当前设备跑，它们会在**还没走到任何真正要验的判据**之前就退出，
// 看起来像「脚本坏了」，实则是前置假设不成立。
//
// `verify-finance-writepath.mjs` 早就留了这个口子（`POCKET_EXPECT_ORIGIN`，
// 注释写明「做生产 https 回归时用…否则脚本会在第一关就退出」）。这批脚本漏了。
//
// ## 机械不变量
//
//  1) 每处 `origin !== 'http://localhost'` 都被替换成 `origin !== EXPECT_ORIGIN`
//  2) `const EXPECT_ORIGIN = …` 在每个文件里**恰好**声明一次
//  3) 改完 `node --check` 必须通过（对着**新内容**，写临时文件查）
//  4) 任何一条不满足 → 该文件**不动**
//
// 用法：node scripts/migrate-expect-origin.mjs [--dry]
import { readFileSync, writeFileSync, unlinkSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SELF = path.resolve(fileURLToPath(import.meta.url))
const ROOT = path.resolve(path.dirname(SELF), '..')
const SCRIPTS = path.join(ROOT, 'scripts')
const DRY = process.argv.includes('--dry')

const TARGETS = [
  'verify-email-writepath.mjs',
  'verify-gateway-writepath.mjs',
  'verify-marketplace-install.mjs',
  'verify-bug-u.mjs',
  // 头一版 TARGETS 漏了它，残留检查把它捞出来了 —— 所以迁移完必须再扫一遍全仓
  'verify-bugaa-realdevice.mjs',
]

const DECL = [
  '// 设备上装的是**生产 https 包**（实测 origin=https://localhost），',
  '// 而这一关原本写死开发包 http://localhost ⇒ 在当前设备上会在走到任何',
  '// 真正要验的判据之前就 exit 5。生产 https 回归用 POCKET_EXPECT_ORIGIN 放宽。',
  "const EXPECT_ORIGIN = process.env.POCKET_EXPECT_ORIGIN || 'http://localhost';",
].join('\n')

const NEEDLE = /origin\s*!==\s*'http:\/\/localhost'/g
const changed = []
const skipped = []

for (const name of TARGETS) {
  const f = path.join(SCRIPTS, name)
  const orig = readFileSync(f, 'utf8')
  const hits = (orig.match(NEEDLE) || []).length
  if (hits === 0) { skipped.push({ name, why: '未命中（可能已改过，或断言写法不同）' }); continue }
  if (/const EXPECT_ORIGIN/.test(orig)) { skipped.push({ name, why: '已有 EXPECT_ORIGIN 声明' }); continue }

  let n = 0
  const replaced = orig.replace(NEEDLE, () => { n++; return 'origin !== EXPECT_ORIGIN' })
  const lines = replaced.split('\n')
  // 插声明：放在最后一条 import 之后；没有 import 就放文件靠前第一行代码前
  let ins = -1
  for (let i = 0; i < lines.length; i++) if (/^import /.test(lines[i])) ins = i
  if (ins < 0) {
    for (let i = 0; i < lines.length; i++) { if (/^(const|let|var|function|async)/.test(lines[i])) { ins = i - 1; break } }
  }
  if (ins < 0) { skipped.push({ name, why: '找不到插入锚点' }); continue }
  lines.splice(ins + 1, 0, ...DECL.split('\n'))
  const out = lines.join('\n')

  // 不变量 2
  if ((out.match(/const EXPECT_ORIGIN = /g) || []).length !== 1) { skipped.push({ name, why: 'EXPECT_ORIGIN 声明数不为 1' }); continue }
  // 不变量 3：对着**新内容**查语法（查磁盘旧文件等于没查 —— 踩过）
  const tmp = path.join(path.dirname(f), `.expect-origin-check-${name}`)
  let syntaxOk = true, why = ''
  try {
    writeFileSync(tmp, out, 'utf8')
    execFileSync(process.execPath, ['--check', tmp], { stdio: 'pipe' })
  } catch (e) {
    syntaxOk = false
    why = String(e.stderr || e.message).split('\n').filter((l) => /Error/.test(l))[0] || 'unknown'
  } finally {
    try { unlinkSync(tmp) } catch { /* 临时文件删不掉不该中断 */ }
  }
  if (!syntaxOk) { skipped.push({ name, why: `node --check 失败：${why}` }); continue }

  changed.push({ name, n })
  if (!DRY) writeFileSync(f, out, 'utf8')
}

for (const c of changed) console.log(`  替换 ${c.n} 处  ${c.name}`)
for (const s of skipped) console.log(`  跳过  ${s.name}  —— ${s.why}`)
console.log(`\n${changed.length} 个文件已改，${skipped.length} 个跳过。${DRY ? '（--dry）' : ''}`)
process.exitCode = 0
