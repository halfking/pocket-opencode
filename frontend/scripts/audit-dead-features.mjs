// audit-dead-features.mjs — 报「导出了但零调用方」的 **features/** 层符号。
//
// ── 为什么要有它 ──
// check:dead-api 的存在理由正是这类问题，但它 `const apiDir = join(srcRoot, 'api')`
// —— **射程止于 src/api/**。实测 features/ 有 100+ 个零调用导出，
// 其中包含 `ingestMeetingArtifacts`（会议自动生成笔记，全仓零调用）。
//
// 按本仓既有分工（见 gates.json 的 _notGates_why）：
// **静态审计 audit-* 负责报，check-* 负责卡口。**
// 这个脚本只报，不卡 —— 存量清理属于另一个 PR，本脚本的价值是让那个数字
// **可被复查**，而不是让 gates 今天就红。
//
// ── 第一版这个脚本自己就报错了，值得记下来 ──
// v1 统计引用时把**定义文件整个排除**，于是
// `searchRelatedContext` 内部调用 `searchRelatedNotes/Meetings/Web`
// 被判成死代码。三个假阳性，且把 features/ 的总数从 108 虚报成 132。
// ⇒ **「零调用方」这个判据，必须把「同文件自调用」计入。**
//    这与 check-router-parity 记的「文本口径 ≠ 运行期口径」同族：
//    判据的「统计范围」本身就是一个会悄悄改掉结论的变量。
//
// 用法：
//   node scripts/audit-dead-features.mjs                # 全 features/
//   node scripts/audit-dead-features.mjs --dir meetings # 只看 features/<dir>
//   node scripts/audit-dead-features.mjs --selftest     # 负控：验证判据有牙
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SRC = join(ROOT, 'src')
const FEATURES = join(SRC, 'features')

/**
 * 是不是测试文件。
 *
 * ⚠⚠ 2026-10-07 补。**测试文件提到一个符号，不等于它被接线了。**
 * 本门要回答的是「这个能力接进 App 了吗」，而
 * `voiceprint-writes.test.ts` 里为了写负控，字符串和注释里出现了
 * `saveVoiceprint` / `enrollFromAudio` —— 于是这两个**真的没人调**的函数
 * 从死清单里消失了。假绿长得和「我刚把它接上了」一模一样。
 *
 * 只被测试引用的能力 = 写好了但没接进 App，正是本门要报的那一类。
 */
function isTestFile(p) {
  return p.includes('__tests__') || /\.(test|spec)\.[tj]s$/.test(p) || /\.test\.mjs$/.test(p)
}

/** 剥掉块注释与行注释（避开 http:// 里的 //）。注释不是使用。 */
function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}

function walk(dir, out = []) {
  for (const n of readdirSync(dir)) {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) {
      if (n === '__tests__') continue
      walk(p, out)
    } else if (['.ts', '.vue', '.mjs'].includes(p.slice(p.lastIndexOf('.')))) {
      if (isTestFile(p)) continue
      out.push(p)
    }
  }
  return out
}

/** 一个文件里「导出的名字」+「引用主体（去掉 export 声明行与注释后的文本）」。 */
function collect(files) {
  const raw = new Map(files.map((f) => [f, readFileSync(f, 'utf8')]))
  // ★ 去掉 export 声明行：声明本身不算「引用」。
  //   去掉之后剩下的仍是同文件调用（`foo()` 调用 `bar()`），必须计入。
  const body = new Map(
    [...raw].map(([f, t]) => [f, stripComments(t).split('\n').filter((l) => !/^\s*export\s/.test(l)).join('\n')]),
  )
  const items = []
  for (const [file, text] of raw) {
    const names = new Set()
    for (const m of text.matchAll(/export\s+(?:const|function|async\s+function|class)\s+([A-Za-z_$][\w$]*)/g)) {
      names.add(m[1])
    }
    for (const m of text.matchAll(/export\s*\{([^}]+)\}/g)) {
      for (const part of m[1].split(',')) {
        const nm = part.trim().split(/\s+as\s+/).pop()?.trim()
        if (nm && /^[A-Za-z_$][\w$]*$/.test(nm)) names.add(nm)
      }
    }
    for (const name of names) items.push({ file, name, body })
  }
  return items
}

/**
 * 「零调用方」的判定。
 *
 * ⚠⚠ **两个范围都是会悄悄改掉结论的变量，v1 两个都踩了**：
 *   ① 候选集 = features/（哪些符号要查）；
 *   ② **引用统计 = 整个 src**（去哪儿找调用方）。
 * v1 的 ② 也用了 features/，于是 `recordingRuntime.ts`（在 src/native/）对
 * `loadSpeakerProfiles` / `syncMeetingMetadata` 的调用被漏掉，
 * 两个**明明是活的**函数被判成死的。⇒ 引用面必须比候选面**大**。
 */
function walkAll(dir, out = []) {
  for (const n of readdirSync(dir)) {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) walkAll(p, out)
    else if (['.ts', '.vue', '.mjs'].includes(p.slice(p.lastIndexOf('.')))) out.push(p)
  }
  return out
}

function deadExports(candidateFiles, allFiles, testFiles = []) {
  const allBody = new Map(
    allFiles.map((f) => [f, stripComments(readFileSync(f, 'utf8')).split('\n')
      .filter((l) => !/^\s*export\s/.test(l)).join('\n')]),
  )
  const testBody = new Map(testFiles.map((f) => [f, stripComments(readFileSync(f, 'utf8'))]))
  const out = []
  for (const { file, name } of collect(candidateFiles)) {
    const re = new RegExp(`\\b${name.replace(/\$/g, '\\$')}\\b`, 'g')
    let hits = 0
    for (const [f, t] of allBody) {
      if (f === file) continue
      hits += (t.match(re) || []).length
    }
    hits += (allBody.get(file).match(re) || []).length // ★ 同文件自调用
    if (hits === 0) {
      // 没人从 App 调它。再看测试里有没有：区分「彻底死」与「只被测过、没接进 App」。
      // 两者都要棘轮管住（能力写好了却没人能用），但报出来给人看的含义不同。
      let testOnly = false
      for (const [, t] of testBody) {
        if (new RegExp(`\\b${name.replace(/\$/g, '\\$')}\\b`).test(t)) { testOnly = true; break }
      }
      out.push({ file, name, testOnly })
    }
  }
  return out
}

// ── 负控：判据必须对「真有调用方」与「自调用」都放行，对「真的没人用」才报 ──
if (process.argv.includes('--selftest')) {
  const dir = mkdtempSync(join(tmpdir(), 'deadfeat-'))
  try {
    writeFileSync(join(dir, 'caller.ts'), 'import { used } from "./lib"\nfunction go(){ return used() }\ngo()\n')
    // ★ 只有**注释**里提到 lib2 —— 注释不是使用，它必须仍被判成死的。
    writeFileSync(join(dir, 'commentonly.ts'), '// 历史原因：以前这里调过 mentionedOnly()\nconst K = 1\nvoid K\n')
    // ★ 只有**测试文件**里提到 lib3 —— 测试引用不等于接进 App，必须仍判成死的。
    writeFileSync(join(dir, 'lib3.test.ts'), 'import { testedOnly } from "./lib3"\ntestedOnly()\n')
    writeFileSync(join(dir, 'lib.ts'), [
      'export function used(){ return 1 }',         // 跨文件被 caller 调用 → 不该报
      'function selfOnly(){ return inner() }',       // ★ 不导出：它自己不该进候选集
      'function inner(){ return 2 }',               // 仅被同文件 selfOnly 调用 → 不该报
      'export function orphan(){ return 3 }',       // 真没人用 → 该报
      'export function declaredOnly(){ return 4 }', // 只有声明、无调用 → 该报
      'export function mentionedOnly(){ return 5 }',// 只被注释提到 → 该报
      '',
    ].join('\n'))
    writeFileSync(join(dir, 'lib3.ts'), 'export function testedOnly(){ return 6 }\n')
    const files = walk(dir)
    const names = deadExports(files, files).map((d) => d.name).sort()
    const expect = ['declaredOnly', 'mentionedOnly', 'orphan', 'testedOnly']
    const mustPass = ['used', 'inner']
    // ★ 方向别写反：`wronglyReported` = 本该放行却出现在死代码清单里的。
    const wronglyReported = mustPass.filter((n) => names.includes(n))
    const ok = JSON.stringify(names) === JSON.stringify(expect) && wronglyReported.length === 0
    console.log(`  期望只报 ${JSON.stringify(expect)}（真死：没人用 / 只有声明 / 只有注释提到 / 只有测试提到）`)
    console.log(`  期望放行 ${JSON.stringify(mustPass)}（跨文件调用 / 同文件自调用）`)
    console.log(ok ? `自检 4/4 通过 —— 实得 ${JSON.stringify(names)}`
                   : `自检失败：期望 ${JSON.stringify(expect)}，实得 ${JSON.stringify(names)}；被误报=${JSON.stringify(wronglyReported)}`)
    process.exit(ok ? 0 : 1)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

const only = process.argv.includes('--dir')
  ? process.argv[process.argv.indexOf('--dir') + 1]
  : null
const scope = only ? join(FEATURES, only) : FEATURES
if (!scope.startsWith(FEATURES)) {
  console.error('--dir 只能取 features/ 下的子目录名')
  process.exit(2)
}

const candidates = walk(scope)
const dead = deadExports(candidates, walk(SRC), walkAll(SRC).filter(isTestFile))
const rel = (f) => f.replace(`${ROOT}/`, '')

// `--json`：给 check:dead-features 的棘轮用。
// ⚠ 打印给人看的那段**不能**被拿去解析 —— 文件分组、缩进、中文标题都在变。
// 解析一个给人看的报表，是把两件本来互不影响的事焊在一起：改一次排版就静默改掉门禁结论。
if (process.argv.includes('--json')) {
  console.log(JSON.stringify({
    scope: rel(scope),
    candidates: candidates.length,
    dead: dead.map((d) => ({ file: rel(d.file), name: d.name, testOnly: d.testOnly })),
  }, null, 2))
  process.exit(0)
}

console.log(`扫描 ${rel(scope)}（候选 ${candidates.length} 个文件，引用面 = 整个 src，测试与注释不算引用）：未被 App 引用 ${dead.length} 个`)
const byFile = new Map()
for (const d of dead) {
  if (!byFile.has(d.file)) byFile.set(d.file, [])
  byFile.get(d.file).push(d.name + (d.testOnly ? '（仅测试引用）' : ''))
}
for (const [f, names] of byFile) {
  console.log(`  ${rel(f)}`)
  for (const n of names) console.log(`      ${n}`)
}
if (!dead.length) console.log('  （无）')
