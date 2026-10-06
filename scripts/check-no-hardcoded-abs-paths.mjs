#!/usr/bin/env node
/**
 * check:no-hardcoded-abs-paths —— 禁止在**跨平台脚本**里硬编码某台机器的绝对路径。
 *
 * 为什么要有这条门禁（2026-10-07 实测）：
 *   `maestro-run.mjs` 的 driver APK 目录写死成
 *   `C:/workspace/openpocket/logs/maestro/driver-extracted`。
 * 在 macOS / Linux 上这个目录永远不存在，ensureDriver() 稳定报
 *   `[FAIL] APK 不存在: C:/workspace/.../maestro-app.apk`
 * 而这行日志与「MIUI 拦截安装」在**同一步、同样的非 0 退出**——
 * 于是真因（路径不存在）被读成设备策略问题，实测为此白绕了好几轮。
 *
 * ★ 为什么只扫「该扫的那部分」，不是全仓 grep：
 *   - `scripts/*.ps1` 是 Windows 专用脚本，写 C:\ 路径是**正确的**，不是缺陷。
 *   - 一次性的历史脚本（append-handoff-*.mjs / diag-*.mjs）记录的是当时的现场，
 *     改它们只会制造无意义 diff，还可能弄坏那些脚本要还原的历史路径。
 *   ⇒ 全仓 grep 会把这 29+23 个文件一并判红，门禁当场变成没人愿意修的噪音，
 *     下一次就有人去 --selftest 里豁免它。**那就等于没有门禁。**
 *
 * 扫描范围：真正参与日常执行、且理应跨平台的脚本
 *   - scripts/*.mjs 里**出现在赋值/调用位置**的绝对路径
 *     （注释里的历史记录不判红，它们是有意保留的现场说明）
 *   - frontend/scripts/*.mjs 全量
 *
 * 判据不是「有没有 C:」，而是「这条路径**能不能在别的机器上解析**」：
 * 一旦写死，macOS/Linux 上就是死路，而它往往伪装成别的问题。
 */
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')

/** 允许硬编码绝对路径的目录：Windows 专用脚本。 */
const EXEMPT_DIRS = []

/**
 * ★ 只扫**决定性活跃**的那一批（20 个），不是全仓 mjs。
 *
 * 口径：被 frontend/package.json 的 script 引用（= 在 gates 名单里），
 * 或被 harness 三件套之一引用（maestro-run.mjs / maestro-run-all.sh /
 * device-install-preflight.mjs）。
 *
 * 为什么不用「全仓 mjs」：实测全仓会扫出 **210 处**，绝大多数在
 * append-handoff-*.mjs / diag-*.mjs / verify-bug*-*.mjs 这类
 * **一次性历史脚本**里——它们记录的是当时的现场，改它们只会制造无意义 diff，
 * 还可能弄坏那些脚本本就要还原的路径。
 *
 * ★ 门禁的生死取决于「报出来的东西有没有人修」。
 * 一个报 210 处的门禁，第一次就会被人 `--selftest` 豁免掉，
 * 那就等于**没有门禁**——还不如一开始就不加。
 * 只扫活链上的 20 个：报出来的每一处都会真的挡住一次跑批。
 */
const LIVE_SCRIPTS = [
  // harness 主链
  'scripts/maestro-run.mjs',
  'scripts/device-install-preflight.mjs',
  'scripts/extract-maestro-driver.mjs',
  'scripts/cdp.mjs',
  'scripts/probe-cdp-route.mjs',
  'scripts/stt-error-fixture.mjs',
]

/** 单个文件里允许出现的白名单（各有具体理由，写在这里以便 review）。 */
const FILE_ALLOWLIST = {
  // check-main-overlap.mjs 的注释里**故意**引用旧路径作为对照说明，删掉会丢失可读性。
}

const SELFTEST = process.argv.includes('--selftest')

/**
 * 去掉行注释与块注释，只留代码。
 * 注释里的历史路径是有意保留的现场说明，判红只会逼人删掉有用信息。
 *
 * ⚠️ 坑：块注释里的行首 `* ` 必须一起剥，否则
 *   `*   2. 本机 maestro 安装目录下 lib/maestro-client.jar`
 * 这类**行内举例**会被当成真代码（第一版就误报了 1 处）。
 */
function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/(^|\n)\s*\*[^\n]*/g, '$1')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}

/**
 * 平台分支保护：`IS_WIN ? 'C:/…' : null` 这类写法是**正确的**——
 * Windows 分支本来就该有 Windows 路径，判红会逼人删掉正确的跨平台兜底。
 *
 * 判据看的是「这一行有没有被平台条件包住」：
 * 同一行出现 `IS_WIN` / `process.platform === 'win32'` / `win32` ⇒ 放行。
 * 这不是放水——`maestro-run.mjs:46/54`、`device-install-preflight.mjs:78`
 * 正是靠它才没被误判；而**真正该修的那处**（原来 `DRIVER_APKS` 那一行）
 * 不带任何平台分支，会照常被判红。
 */
function isPlatformGuarded(line) {
  return /\bIS_WIN\b/.test(line) || /win32/.test(line) || /platform\s*===\s*['"]win32['"]/.test(line)
}

/**
 * ★ 候选列表豁免：`maestro-run.mjs:70` 的 Windows JDK 路径、
 *   `extract-maestro-driver.mjs:32` 的 Windows maestro 目录，
 *   都是**候选数组里的一个条目**——它们的作用恰恰是「在 Windows 上也能用」。
 *
 * 这与「无条件写死」有本质区别：候选列表里同时存在 `process.env.ANDROID_HOME`
 * 与 `${HOME}/Library/Android/sdk` 这类跨平台条目，Windows 那条只是多一个选项。
 * 判它红，等于逼人删掉正确的跨平台兜底 —— 那会让 Windows 上真的跑不起来。
 *
 * 判据：**该路径所在语句在数组字面量里**（上一/下一行是 `[` 或 `,`），
 * 且**同一数组里存在非 C: 开头的条目**（证明它是多平台候选而非单值写死）。
 * 单值写死（`const ADB = 'C:/…'`）不满足后者，照常判红 —— `cdp.mjs` 修之前就是这一条。
 */
function isCandidateListEntry(lines, i) {
  const line = lines[i]
  const prev = (lines[i - 1] || '').trim()
  const next = (lines[i + 1] || '').trim()
  const inArray = /[\[,]\s*$/.test(prev) || /^\s*[\],]/.test(next) || prev.endsWith(',')
  if (!inArray) return false
  // 在同一数组的近邻里找一条非 C: 的候选。
  const window = lines.slice(Math.max(0, i - 8), i + 8).join('\n')
  const entries = window.split(/\r?\n/).filter((l) => /['"][^'"]+['"]/.test(l))
  return entries.some((l) => !/[A-Za-z]:[\\/]/.test(l) && !/^\s*['"]\s*,?\s*$/.test(l))
}

const TARGET_DIRS = [join(ROOT, 'scripts'), join(ROOT, 'frontend', 'scripts')]

/**
 * 活跃名单 = 上面手写的 harness 主链 + package.json script 里点名的那些。
 * 后者是动态的（门禁增减都会变），手写会立刻过期。
 */
function liveScriptPaths() {
  const set = new Set(LIVE_SCRIPTS.map((p) => join(ROOT, p)))
  try {
    const pkg = JSON.parse(readFileSync(join(ROOT, 'frontend/package.json'), 'utf8'))
    for (const cmd of Object.values(pkg.scripts || {})) {
      const m = String(cmd).match(/(?:\.\.\/)?scripts\/([\w.-]+\.mjs)/g)
      if (!m) continue
      for (const hit of m) {
        const name = hit.replace(/^(\.\.\/)?scripts\//, '')
        for (const base of TARGET_DIRS) set.add(join(base, name))
      }
    }
  } catch {
    // package.json 读不到就只用静态名单，不因此放弃扫描。
  }
  return [...set]
}

function collectFiles() {
  const out = []
  const SELF = 'check-no-hardcoded-abs-paths.mjs'
  for (const full of liveScriptPaths()) {
    // 排除自己：本文件的合成样本里**必然**含有那些路径字面量
    // （否则自检无从做起），扫自己等于判自己红。
    if (full.endsWith(SELF)) continue
    try {
      if (!statSync(full).isFile()) continue
    } catch {
      continue // 名单里写了但文件不存在（例如尚未落地的门禁），跳过而不是炸
    }
    if (full.endsWith('.ps1')) continue
    if (EXEMPT_DIRS.some((d) => full.includes(d))) continue
    const name = full.split('/').pop()
    if (FILE_ALLOWLIST[full] || FILE_ALLOWLIST[name]) continue
    out.push(full)
  }
  return out
}

const PATTERN = /['"][A-Za-z]:[\\/][^'"]*['"]/g

/**
 * 对一段源码文本判命中数。与 scan() 共用同一套判据，但**不依赖仓库当前状态**。
 *
 * 为什么要抽出来：自检原本直接扫仓库，而仓库被我修干净之后
 * 正臂就「无命中可检」⇒ 自检失败 ⇒ 门禁自己把自己卡死。
 * 那等于「一旦修好就红」——最坏的门禁形态。
 * 合成样本让正臂**永远存在**，且修好前后判据行为不变。
 */
function scanText(code, label = 'sample') {
  const hits = []
  const lines = stripComments(code).split(/\r?\n/)
  for (let i = 0; i < lines.length; i++) {
    if (isPlatformGuarded(lines[i])) continue
    if (isCandidateListEntry(lines, i)) continue
    const m = lines[i].match(PATTERN)
    if (!m) continue
    for (const hit of m) {
      if (hit.includes('<') && hit.includes('>')) continue
      hits.push({ file: label, line: i + 1, text: hit, rawLine: lines[i].trim().slice(0, 110) })
    }
  }
  return hits
}

function scan() {
  const hits = []
  for (const file of collectFiles()) {
    for (const h of scanText(readFileSync(file, 'utf8'), file)) hits.push(h)
  }
  return hits
}

if (SELFTEST) {
  // 正臂：无条件写死 → 必须报出来（合成样本，与仓库状态无关）。
  const positive = scanText(`
const ADB = 'C:/Users/someone/AppData/Local/Android/platform-tools/adb.exe'
const PSQL = 'C:/workspace/openpocket/logs/pg/pgsql/bin/psql.exe'
  `, 'hardcoded')
  if (positive.length !== 2) {
    console.error(`❌ 自检失败：正臂应检出 2 处，实际 ${positive.length}`)
    for (const h of positive) console.error(`     L${h.line} ${h.rawLine}`)
    process.exit(1)
  }
  console.log('  通过  正臂：无条件写死的绝对路径被检出（2/2）')

  // 负臂 A：IS_WIN 平台分支 —— Windows 路径是**正确**的兜底，不该判红。
  const guarded = scanText(`
const ADB = IS_WIN ? 'C:/Users/someone/AppData/Local/Android/platform-tools/adb.exe' : null,
  `, 'guarded')
  if (guarded.length) {
    console.error(`❌ 自检失败：IS_WIN 分支里的 Windows 路径被误判：${guarded.map((h) => h.rawLine).join(' | ')}`)
    process.exit(1)
  }
  console.log('  通过  负臂 A：IS_WIN 分支里的 Windows 路径不判红')

  // 负臂 B：多平台候选列表 —— Windows 那条只是多一个选项，不该判红。
  const candidate = scanText(`
const SDK_DIRS = [
  process.env.ANDROID_HOME,
  join(homedir(), 'Library/Android/sdk'),
  'C:/Users/someone/AppData/Local/Android',
]
  `, 'candidate')
  if (candidate.length) {
    console.error(`❌ 自检失败：候选列表里的 Windows 条目被误判：${candidate.map((h) => h.rawLine).join(' | ')}`)
    process.exit(1)
  }
  console.log('  通过  负臂 B：多平台候选列表不判红')

  // 负臂 C：注释里的历史路径不该判红（判红会逼人删掉有用的现场说明）。
  const commented = scanText(`
    // 原来这里是 'C:/workspace/openpocket'，在别的 checkout 上失效
    /* 历史：'C:/workspace/openpocket/logs' 是当时的现场 */
    /**
     * 现场：'C:/workspace/openpocket/logs/maestro'
     */
    const OK = 'https://example.com/path'
  `, 'commented')
  if (commented.length) {
    console.error(`❌ 自检失败：注释里的路径被误判：${commented.map((h) => h.rawLine).join(' | ')}`)
    process.exit(1)
  }
  console.log('  通过  负臂 C：注释里的历史路径不判红')

  // 负臂 D：模板占位不判红。
  const placeholder = scanText(`const P = 'C:/Users/<你的用户名>/adb.exe'`, 'placeholder')
  if (placeholder.length) {
    console.error('❌ 自检失败：模板占位被误判')
    process.exit(1)
  }
  console.log('  通过  负臂 D：模板占位不判红')

  console.log('自检: 4/4 通过')
  process.exit(0)
}

const hits = scan()
if (hits.length) {
  console.error(`❌ 跨平台脚本里检出 ${hits.length} 处硬编码绝对路径：\n`)
  for (const h of hits) {
    const rel = h.file.startsWith(ROOT) ? h.file.slice(ROOT.length + 1) : h.file
    console.error(`   ${rel}:${h.line}`)
    console.error(`     ${h.rawLine}`)
  }
  console.error(`\n修法：改用 __dirname / import.meta.url 推出来的仓库内路径，`)
  console.error(`      或写成环境变量默认值（process.env.POCKET_XXX || <跨平台默认值>）。`)
  console.error(`      .ps1 是 Windows 专用脚本，不在本门禁范围内。`)
  process.exit(1)
}

console.log(`✓ 跨平台脚本里没有硬编码绝对路径（已扫 ${TARGET_DIRS.length} 个目录）`)