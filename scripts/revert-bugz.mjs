#!/usr/bin/env node
/**
 * revert-bugz.mjs — 在「已修 / 修复前」两种状态之间切换，用于**证伪**回归测试。
 *
 * 为什么要证伪：一个恒返回 PASS 的测试和真正有效的测试在报告上完全一样。
 * 做法是把修复回退（wrapUniqueViolation → 裸返回 err），重跑同一批测试，
 * **必须看到它们失败**（报的正是原始 23505），否则说明测试没有区分能力。
 *
 * ## 双向，且必须显式指定方向
 *
 *   node scripts/revert-bugz.mjs on    # 模拟修复前（回退三处调用）
 *   node scripts/revert-bugz.mjs off   # 恢复修复
 *
 * ⚠️ 这两个命令都会**就地修改工作区文件**。`on` 之后若忘了 `off`，
 *    就会把一个「已回退的 BUG-Z」误提交进主分支。因此：
 *    - 方向必须显式给出，不给就拒绝执行并退出；
 *    - 执行前先判定当前状态，状态不符就拒绝（不做盲替换）；
 *    - 执行后立刻打印该文件的 git diff 行数，便于肉眼确认。
 *
 * 用法：node scripts/revert-bugz.mjs on|off
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { execSync } from 'node:child_process'

const P = 'backend/internal/marketplace/marketplace.go'
const mode = process.argv[2]

if (mode !== 'on' && mode !== 'off') {
  console.error('用法: node scripts/revert-bugz.mjs on|off')
  console.error('  on  = 模拟修复前（回退）')
  console.error('  off = 恢复修复')
  process.exit(2)
}

const s = readFileSync(P, 'utf8')

// 三处调用的双向替换。两侧都带足够唯一的上下文，
// 避免误伤同文件里其它形状相同的 return 语句。
const SITES = [
  ['return PackageVersion{}, wrapUniqueViolation(err, "package "+pkgID)', 'return PackageVersion{}, err'],
  ['return PackageVersion{}, wrapUniqueViolation(err, "version "+versionID)', 'return PackageVersion{}, err'],
  ['return ReleaseRef{}, wrapUniqueViolation(err, "release "+releaseID)', 'return ReleaseRef{}, err'],
]

const fixedCount = SITES.filter(([from]) => s.includes(from)).length

let out = s
let n = 0
if (mode === 'on') {
  if (fixedCount !== 3) {
    console.error(`FATAL: 期望 3 处 wrapUniqueViolation 调用，实际匹配到 ${fixedCount} 处。`)
    console.error('       文件可能已被改动或已处于 on 状态，请先 git diff 核对。拒绝盲替换。')
    process.exit(3)
  }
  for (const [from, to] of SITES) {
    out = out.split(from).join(to)
    n++
  }
} else {
  // off：从注释锚点定位回填，注释在 on 之后仍然保留。
  //
  // 锚点必须**换行符无关**：Windows 工作区里 marketplace.go 是 CRLF
  // （core.autocrlf=true，仓库规范形式 LF）。这里第一版把 `\n` 写死在锚点里，
  // 结果在 CRLF 文件上永远匹配不上 —— 而拒绝盲改恰好避免了静默写坏文件。
  // 教训同 BUG-Y：跨平台换行是「写死字面量」的经典陷阱。
  const anchors = [
    [/(后到的那条会撞 marketplace_packages_pkey → 409 而非 500。\r?\n[ \t]*)return PackageVersion\{\}, err/,
      'return PackageVersion{}, wrapUniqueViolation(err, "package "+pkgID)'],
    [/(修之前这里直接冒泡原始 pgx 错误，最终被写成 500。\r?\n[ \t]*)return PackageVersion\{\}, err/,
      'return PackageVersion{}, wrapUniqueViolation(err, "version "+versionID)'],
    [/(`,[ \t]*releaseID,[ \t]*cmd\.VersionID,[ \t]*channel,[ \t]*now\);[ \t]*err[ \t]*!=[ \t]*nil[ \t]*\{\r?\n[ \t]*)return ReleaseRef\{\}, err/,
      'return ReleaseRef{}, wrapUniqueViolation(err, "release "+releaseID)'],
  ]
  for (const [re, to] of anchors) {
    if (!re.test(out)) {
      console.error(`FATAL: off 模式找不到锚点，无法安全回填：${re}`)
      console.error('       文件可能不是 on 状态或已被改动。请 git diff 后手工处理。')
      process.exit(3)
    }
    // ⚠️ 必须用**捕获组回调**而不是字符串替换值：写成 `out.replace(re, to)`
    //    时 $1 会被当普通字符原样落盘（JS 只在字符串替换值里展开 $1），
    //    第一版就是这么把 `return ReleaseRef{}, err` 连同上一行 INSERT 一起写坏的。
    //    而且 `\s*` 会连带吞掉换行 —— 用 [ \t]* 精确限定缩进。
    out = out.replace(re, (_m, g1) => g1 + to)
    n++
  }
  if (fixedCount !== 0) {
    console.error(`FATAL: off 模式下仍匹配到 ${fixedCount} 处未回退的调用，状态不一致，拒绝执行。`)
    process.exit(3)
  }
}

writeFileSync(P, out)
console.log(`${mode === 'on' ? '已回退' : '已恢复'} ${n}/3 处 wrapUniqueViolation 调用（当前：${mode === 'on' ? '模拟修复前' : '修复后'}）`)

try {
  const d = execSync(`git diff --numstat -- ${P}`, { encoding: 'utf8' }).trim()
  console.log(`该文件当前 git diff：${d || '(无差异)'}`)
  if (mode === 'on') {
    console.log('⚠️  工作区此刻是「修复前」状态。跑完证伪测试后务必执行：node scripts/revert-bugz.mjs off')
  }
} catch (e) {
  console.log(`(git diff 读取失败: ${e.message})`)
}
