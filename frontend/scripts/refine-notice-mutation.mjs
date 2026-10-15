#!/usr/bin/env node
// §35 前端配套（refine_rejected 透出）的变异验证。
//
// ⚠ 为什么放在外部脚本而不是塞进 .test.ts：
//   塞进去的那一版只做字符串比对、**根本没在变异体上重跑断言** ——
//   它恒绿，是「门禁说自己有牙」而不是真的有牙（同 §35 第 5、6 处教训）。
//   这里每处变异都真跑 `node --test`，期望**转红**。
//
// 用法：node scripts/refine-notice-mutation.mjs
import { readFileSync, writeFileSync, rmSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const T = {
  notice: 'src/features/sessions/refine-outcome-notice.ts',
  api: 'src/api/meetings.ts',
  caller: 'src/features/sessions/useSessionLiveRecord.ts',
  gate: 'src/features/sessions/__tests__/refine-rejected-notice.test.ts',
}

const MUTATIONS = [
  ['N1 纯文案层删掉「被拦下」分支', T.notice, (s) =>
    s.replace('if (rejected) {', 'if (false) {')],
  ['N2 纯文案层把 rejected 分支倒过来（先判 fromFallback）', T.notice, (s) =>
    s.replace(
      /if \(rejected\) \{[\s\S]*?\n  \}\n  if \(fromFallback\) \{/,
      'if (fromFallback) {\n    return { kind: \'info\', text: `${adoptSuffix}精翻未生效（云端返回无法解析），显示的是原始转写` }\n  }\n  if (rejected) {',
    )],
  ['N3 纯文案层文案改成「无法解析」（与解析失败同句）', T.notice, (s) =>
    s.replace(/text: `\$\{adoptSuffix\}精校结果被拦下[^`]*/, 'text: `${adoptSuffix}精翻未生效（云端返回无法解析）')],
  ['N4 API 层不认 refine_rejected', T.api, (s) =>
    s.replace('raw.refine_rejected === true || raw.refineRejected === true', 'false')],
  ['N5 API 层算出来但不写进返回值（信号最后一跳丢）', T.api, (s) =>
    s.replace('...(rejected ? { rejected: true } : {}),', '')],
  ['N6 RefineResult 类型删掉 rejected 字段', T.api, (s) =>
    s.replace('rejected?: boolean', 'rejectedRenamed?: boolean')],
  ['N7 调用点不把 result.rejected 传给纯文案层', T.caller, (s) =>
    s.replace('rejected: result.rejected,', 'rejected: false,')],
  ['N8 调用点改回硬编码文案（纯文案层形同虚设）', T.caller, (s) =>
    s.replace(
      /const notice = buildRefineNotice\(\{[\s\S]*?\}\)\n      if \(notice\.kind === 'info'\) toast\.info\(notice\.text\)\n      else toast\.success\(notice\.text\)/,
      "toast.info('精翻未生效（云端返回无法解析），显示的是原始转写')",
    )],
]


const EXTRA = [
  ['N9 refineStatusFor 恒返回 refined（状态层没修）', T.notice, (s) =>
    s.replace("return input.fromFallback || input.rejected ? 'refine-failed' : 'refined'",
              "return 'refined'")],
  ['N10 refineStatusFor 只认 fromFallback，漏掉 rejected', T.notice, (s) =>
    s.replace("return input.fromFallback || input.rejected ? 'refine-failed' : 'refined'",
              "return input.fromFallback ? 'refine-failed' : 'refined'")],
  ['N11 statusText 把「精翻未生效」写成「已完成」（藏进成功桶）',
    'src/features/meetings/meeting-list.ts', (s) =>
      s.replace("'refine-failed': '精翻未生效'", "'refine-failed': '已完成'")],
  ['N12 MeetingStatus 类型删掉 refine-failed', 'src/features/meetings/meetings-store.ts', (s) =>
    s.replace(" | 'refine-failed'", "")],
  ['N13 meeting-ingest 改回写死 status: refined', 'src/features/meetings/meeting-ingest.ts', (s) =>
    s.replace(/status: refineStatus,/g, "status: 'refined',")],
  ['N14 useSessionLiveRecord 改回写死 status: refined',
    'src/features/sessions/useSessionLiveRecord.ts', (s) =>
    s.replace(/status: refineStatusFor\(\{[^}]*\}\),/, "status: 'refined',")],
  ['N15 后端 finalizeMeetingRefine 无条件 refined',
    '../backend/internal/server/server_meeting_ingest.go', (s) =>
      s.replace(/\t\t\tif refineDegraded\(result\) \{\n\t\t\t\texisting\.Status = "refine-failed"\n\t\t\t\}\n/, '')],
]
MUTATIONS.push(...EXTRA)

const gate = path.join(ROOT, T.gate)
const backups = new Map()
const restore = () => {
  for (const [f, s] of backups) writeFileSync(path.join(ROOT, f), s, 'utf8')
  backups.clear()
}

let ok = 0
const rows = []
try {
  for (const [name, file, mutate] of MUTATIONS) {
    const abs = file.startsWith('../') ? path.resolve(ROOT, file) : path.join(ROOT, file)
    const orig = readFileSync(abs, 'utf8')
    backups.set(file, orig)
    const mutated = mutate(orig)
    if (mutated === orig) {
      rows.push([name, '❌ 变异没施上', '锚点没匹配上，够不到那条路径'])
      backups.delete(file)
      continue
    }
    writeFileSync(abs, mutated, 'utf8')
    let out = ''
    let red = false
    try {
      out = execFileSync('node', ['--test', gate], { cwd: ROOT, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
    } catch (e) {
      out = `${e.stdout ?? ''}${e.stderr ?? ''}`
      red = true
    }
    const line = (out.split('\n').find((l) => l.includes('error:')) ?? '').trim()
    if (red) {
      ok++
      rows.push([name, '🔴 转红 ✅', line.slice(0, 110)])
    } else {
      rows.push([name, '🟢 仍绿 ❌', '判据无牙'])
    }
    writeFileSync(abs, orig, 'utf8')
    backups.delete(file)
  }
} finally {
  restore()
}

console.log('='.repeat(74))
for (const [n, v, d] of rows) {
  console.log(`${v.padEnd(12)} ${n}`)
  if (d) console.log(`             ${d}`)
}
console.log('='.repeat(74))
console.log(`转红 ${ok}/${rows.length}`)

// 还原后必须仍然全绿，否则说明「转红」是因为文件坏了
try {
  execFileSync('node', ['--test', gate], { cwd: ROOT, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
  console.log('还原后门禁: OK')
} catch (e) {
  console.log('还原后门禁: FAILED（变异只弄坏了文件，不是判据命中）')
  process.exitCode = 1
}
process.exitCode = ok === rows.length ? 0 : 1
void rmSync
