// pkm-fix-negctl — 静态卡口 audit-workspace-args.mjs 的**负控**。
//
// 卡口报 0 命中有两种可能：代码真的干净，或者解析器失灵（假绿）。
// 这个脚本把 BUG-AR 的两处修复临时回退掉，验证卡口**确实会红**，随后原样还原。
//
// 两个踩过的坑（都已修正，别改回去）：
//   1. 源文件是**纯 CRLF**。第一版锚点里写死 \n，一个都没匹配上。
//   2. 第一版在循环里逐个校验锚点+逐个写文件：第一个文件已经改完，第二个才报
//      「锚点未命中」并 exit —— 源码被留在回退状态。必须**先校验全部锚点再动任何文件**。
//   3. 文件读写走 fs 原样读写：PowerShell 的 Get-Content/Set-Content -Encoding UTF8
//      会把中文注释写坏。
//
// 用法：node scripts/pkm-fix-negctl.mjs
import { readFileSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'

const ROOT = new URL('..', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const P = (p) => `${ROOT}/frontend/src/features/pkm/${p}`

const targets = [
  {
    file: P('PkmNoteView.vue'),
    label: 'PkmNoteView 新建路径',
    from: /await saveNote\(\{ title: '无标题', html: '', workspaceId: currentWorkspaceId\(\) \}\)/,
    to: "await saveNote({ title: '无标题', html: '' })",
  },
  {
    file: P('PkmEditor.vue'),
    label: 'PkmEditor 自动保存路径',
    // 用 \r?\n 兼容两种行尾，不写死
    from: /(      dailyDate: props\.dailyDate,\r?\n      workspaceId: currentWorkspaceId\(\),\r?\n)/,
    to: (m) => m.replace(/[^\S\n]*workspaceId: currentWorkspaceId\(\),\r?\n/, ''),
  },
]

// ---- 第一步：全部校验，任何一个不命中就一个字节都不写 ----
const plan = targets.map((t) => {
  const orig = readFileSync(t.file, 'utf8')
  if (!t.from.test(orig)) return { ...t, orig, ok: false, next: null }
  const next = orig.replace(t.from, t.to)
  if (next === orig) return { ...t, orig, ok: false, next: null }
  return { ...t, orig, ok: true, next }
})
const bad = plan.filter((p) => !p.ok)
if (bad.length) {
  console.log('锚点未命中，放弃负控（未写入任何文件）：')
  for (const b of bad) console.log(`  - ${b.label}  ${b.file}`)
  process.exit(2)
}

// ---- 第二步：写入 → 跑卡口 → 还原 ----
let out = ''
try {
  for (const p of plan) writeFileSync(p.file, p.next, 'utf8')
  console.log(`已临时回退 ${plan.length} 处修复，卡口输出：\n`)
  out = execFileSync('node', [`${ROOT}/scripts/audit-workspace-args.mjs`], { encoding: 'utf8', maxBuffer: 33554432 })
} catch (e) {
  out = (e.stdout || '') + (e.stderr || '')
} finally {
  for (const p of plan) writeFileSync(p.file, p.orig, 'utf8')
  console.log('=== 已还原 ===')
}
console.log(out)
const flagged = /saveNote/.test(out) && /命中 [1-9]\d* 处/.test(out)
console.log(flagged
  ? '✅ 负控成立：回退后卡口命中 saveNote 漏传 → 它有区分能力，报 0 时是真的干净'
  : '❌ 负控不成立：回退后卡口没报警，这个卡口是假绿，不能用')
process.exit(flagged ? 0 : 1)
