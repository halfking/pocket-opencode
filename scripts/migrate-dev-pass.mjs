// migrate-dev-pass.mjs —— 把「从源码刮 devPass」批量换成共享的 requireDevPass()。
//
// ## 为什么不手改 32 个文件
//
// 那 32 处是**复制粘贴**出来的（BUG-V12）。逐个手改既慢又容易漏，
// 而漏掉的那一个会以「静默的空口令」形式继续产出看起来像结论的输出。
//
// ## 安全措施（每一条都是为了「改错了要立刻看得见」）
//
// 1) **--dry 先看**：只打印将要改什么，不落盘。
// 2) **逐文件 `node --check`**：语法一坏就**当场回退该文件**，其余继续。
// 3) **UTF-8 往返自证**：读入后 `Buffer.from(s,'utf8')` 必须与原字节一致，
//    否则**拒绝写**（防止编码往返毁掉中文）。
// 4) **只认一种形态**：先量过 32 个全是形态 A；匹配不上的**原样跳过并报告**，
//    绝不「尽力改一下」——那正是把好实现换成瞎实现。
// 5) **不碰 import**：import 的增删单独做，且只在确认该文件的 readFileSync
//    没有别的用处时才删。
import { readFileSync, writeFileSync, readdirSync, statSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const ROOT = join(HERE, '..')          // 门禁输出的是**仓库根相对**路径（scripts/xxx.mjs）
const DRY = process.argv.includes('--dry')

// 形态 A：`const NAME = (readFileSync('…server_assistant.go', 'utf8').match(/devPass…/) || [])[1] || '';`
// \s 能跨行，所以 `const devPass =\n  (readFileSync(` 这种两行写法也吃下。
//
// ⚠️ 尾部**必须**用 `[ \t]*` 而不是 `\s*`：原句常常没有分号，`\s*` 会把后面的
//    换行符一起吞掉，替换后就变成 `const devPass = requireDevPass()function api(...)`。
//    第一版就是这么炸的——18 个成功、14 个 `node --check` 失败并回退。
//    那 14 个失败不是「有的文件特殊」，是**同一条正则**对**有无分号**两种情况处理不一致。
const SHAPE_A = /const\s+([A-Za-z_$][\w$]*)\s*=\s*\n?\s*\(?\s*\(\s*readFileSync\(\s*['"][^'"]*server_assistant\.go['"]\s*,\s*['"]utf8['"]\s*\)\s*\.match\(\s*\/devPass[\s\S]{0,200}?\/\s*[gimsuy]*\s*\)\s*\|\|\s*\[\]\s*\)\s*\[\s*1\s*\][ \t]*(?:\|\|[ \t]*(''|"")[ \t]*)?;?/g

// 不变量：匹配到的片段**不得以换行结尾**。这是上面那个 bug 的机械检测，
// 写在这里而不是只靠 node --check 兜底——因为后者只在 14/32 的文件上触发，
// 看不出「为什么有的成功有的失败」。
function assertNoTrailingNewline(span, file) {
  if (/\r?\n$/.test(span)) {
    throw new Error(`MIGRATE_BUG_REGRESSION：${file} 的匹配片段以换行结尾，替换后会粘住下一行`)
  }
}

function listTargets() {
  const out = execFileSync(process.execPath, [join(HERE, 'check-dev-pass-sourcing.mjs'), '--list'], {
    encoding: 'utf8', maxBuffer: 64 << 20,
  })
  return [...new Set([...out.matchAll(/^\s*(\S+\.mjs):\d+\s+\[scrape-dev-pass\]/gm)].map((m) => m[1]))]
}

const IMPORT_LINE = "import { requireDevPass } from './lib/dev-pass.mjs'"

const results = { migrated: [], skipped: [], failed: [] }
for (const rel of listTargets()) {
  const p = join(ROOT, rel)
  const buf = readFileSync(p)
  const src = buf.toString('utf8')
  // 编码往返自证
  if (!Buffer.from(src, 'utf8').equals(buf)) { results.failed.push([rel, 'UTF-8 往返不一致，拒绝写']); continue }

  SHAPE_A.lastIndex = 0
  const matches = [...src.matchAll(SHAPE_A)]
  if (matches.length === 0) { results.skipped.push(rel); continue }
  if (matches.length > 1) { results.skipped.push(rel); continue }   // 不猜：多处就手改
  try { assertNoTrailingNewline(matches[0][0], rel) } catch (e) { results.failed.push([rel, e.message]); continue }

  let next = src.replace(SHAPE_A, (_m, name) => `const ${name} = requireDevPass()`)

  // 增 import：放在最后一条既有 import 之后；没有 import 就放在文件头第一行后。
  if (!/requireDevPass\(\)/.test(src.split('\n').filter((l) => !l.startsWith('import')).join('\n'))) {
    if (next.includes(IMPORT_LINE)) { /* 已有 */ } else {
      const importRe = /^import .*$/gm
      let last = null, m2
      while ((m2 = importRe.exec(next)) !== null) last = m2
      next = last
        ? next.slice(0, last.index) + IMPORT_LINE + '\n' + next.slice(last.index)
        : IMPORT_LINE + '\n' + next
    }
  }

  if (DRY) { results.migrated.push(rel); continue }

  writeFileSync(p, next, 'utf8')
  // 逐文件语法检查，坏就回退
  try {
    execFileSync(process.execPath, ['--check', p], { stdio: 'pipe' })
    results.migrated.push(rel)
  } catch (e) {
    writeFileSync(p, buf)
    results.failed.push([rel, `node --check 失败，已回退：${String(e.stderr || e).split('\n')[0].slice(0, 90)}`])
  }
}

console.log(`形态 A 迁移：成功 ${results.migrated.length}、跳过 ${results.skipped.length}、失败回退 ${results.failed.length}${DRY ? '（--dry，未落盘）' : ''}`)
if (results.skipped.length) {
  console.log('\n跳过的（形态不匹配，**需要手改**，不要当成已修）：')
  for (const f of results.skipped) console.log(`  ${f}`)
}
if (results.failed.length) {
  console.log('\n失败的：')
  for (const [f, why] of results.failed) console.log(`  ${f}  ${why}`)
}
// ---------------------------------------------------------------------------
// 第二遍：清掉迁移留下的未使用 import。
//
// 形态 A 里 `readFileSync` 只用于刮口令，替换后它就没用了。留着不影响运行，
// 但它是**迁移留下的垃圾**，而且下次有人 grep `readFileSync` 判断「这个脚本读不读文件」
// 时会被误导。目标明确：凡是 import 了 lib/dev-pass.mjs 的文件。
// ---------------------------------------------------------------------------
function pruneUnusedImports() {
  const pruned = []
  for (const e of readdirSync(HERE)) {
    if (!e.endsWith('.mjs')) continue
    const p = join(HERE, e)
    if (!statSync(p).isFile()) continue
    const buf = readFileSync(p)
    const src = buf.toString('utf8')
    if (!src.includes("from './lib/dev-pass.mjs'")) continue
    const imp = /^import\s*\{([^}]*)\}\s*from\s*'node:fs'\s*;?\s*(\r?\n)?/m.exec(src)
    if (!imp) continue
    const names = imp[1].split(',').map((x) => x.trim()).filter(Boolean)
    const body = src.replace(imp[0], '')
    const dead = names.filter((n) => (body.match(new RegExp(`\\b${n}\\b`, 'g')) || []).length === 0)
    if (!dead.length) continue
    const kept = names.filter((n) => !dead.includes(n))
    let next
    if (kept.length === 0) {
      next = src.slice(0, imp.index) + src.slice(imp.index + imp[0].length)
    } else {
      const styled = kept.map((n) => (n === 'readFileSync' ? 'readFileSync' : n)).join(', ')
      next = src.slice(0, imp.index) + `import { ${styled} } from 'node:fs'${imp[2] || ''}` + src.slice(imp.index + imp[0].length)
    }
    if (DRY) { pruned.push(`${e} (删 ${dead.join(',')})`); continue }
    writeFileSync(p, next, 'utf8')
    try {
      execFileSync(process.execPath, ['--check', p], { stdio: 'pipe' })
      pruned.push(`${e} (删 ${dead.join(',')})`)
    } catch {
      writeFileSync(p, buf)   // 回退
      pruned.push(`${e} ✗ 语法失败已回退`)
    }
  }
  return pruned
}

const pruned = pruneUnusedImports()

if (pruned.length) {
  console.log(`\n清理未使用 import：${pruned.length} 个${DRY ? '（--dry）' : ''}`)
  for (const x of pruned) console.log(`  ${x}`)
}
process.exit(results.failed.length ? 1 : 0)
