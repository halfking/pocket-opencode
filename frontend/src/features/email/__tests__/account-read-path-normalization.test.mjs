// account-read-path-normalization.test.mjs — 钉住「读路径真的调了归一」这条接线。
//
// ## 为什么需要它
//
// account-stamp-units.test.mjs 里那条名为「normalized stamp keeps the LWW guard
// meaningful」的用例，注释写的是：
//
//     这条是**端到端**判据……若 rowToAccount 忘了调归一（或调错），它会转红。
//
// **这句话是错的。** 那条用例自己写着
// `updatedAt: normalizeAccountStamp(localMs)`——它先把毫秒值归一好，再喂给
// planAccountSync。它从头到尾**没有调用 rowToAccount**，所以 rowToAccount 里
// 有没有接线，对它没有任何影响。
//
// 2026-10-02 实测的负控：把 emails-store.ts:rowToAccount 的
// `normalizeAccountStamp(r.updated_at)` 换成裸的 `r.updated_at`，再跑
// account-lww-real / account-stamp-units / account-sync /
// account-push-field-symmetry 四个文件 —— **25 pass / 0 fail，一个都没红**。
//
// 后果就是 handoff §7dg 那个 bug 可以原样回来且无人报警：单位错了，
// 上行 base 恒大于服务端现存值，服务端守卫 `updated_at <= base` 恒成立，
// 「旧的一方覆盖新的一方」被静默架空，而 25 条用例全绿。
//
// ## 为什么用源码断言而不是跑 rowToAccount
//
// rowToAccount 不是导出的，而且 emails-store.ts 依赖 localDB / vue，
// 在纯 node 里 import 会把整条依赖链拖进来（account-lww.ts 抽出纯模块
// 正是因为这个）。所以这里按**函数边界**切出 rowToAccount 的函数体做源码
// 断言 —— 与 Go 侧 backfill_guard_test.go 同一套路。
//
// 切边界而不是手抄整段：手抄的副本会与磁盘文本漂移，anchor 静默失配，
// 脚本变成 no-op 后还会报「通过」。
//
// ## 负控
//
// 把 rowToAccount 里的 normalizeAccountStamp( 去掉 → 本文件转红。
// 实测过。

import { strict as assert } from 'node:assert'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { test } from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
// 本仓的 .ts 是 CRLF。归一化成 LF 再切边界，否则 '\n}\n' 永远匹配不到，
// 断言会以一种「函数不存在」的形式失败——那是个假信号，不是真缺陷。
const source = readFileSync(join(here, '..', 'emails-store.ts'), 'utf8').replace(/\r\n/g, '\n')

/** 按函数边界切出 `function <name>` 的整个函数体。 */
function sliceFunction(src, name) {
  const start = src.indexOf(`function ${name}(`)
  assert.notEqual(start, -1, `emails-store.ts 里找不到 function ${name}(`)
  const end = src.indexOf('\n}\n', start)
  assert.notEqual(end, -1, `function ${name} 的结束花括号没找到，切片会越界`)
  return src.slice(start, end + 3)
}

const rowToAccount = sliceFunction(source, 'rowToAccount')
const saveAccount = sliceFunction(source, 'saveAccount')

test('rowToAccount 把 updated_at 过一遍 normalizeAccountStamp', () => {
  assert.ok(
    rowToAccount.includes('normalizeAccountStamp('),
    'rowToAccount 没有调 normalizeAccountStamp —— updated_at 会以毫秒值直接进 LWW 比较',
  )
  // 只看「函数体里出现过」还不够：它可能只出现在注释里，或者映射的是别的字段。
  // 必须命中「updatedAt: normalizeAccountStamp(...)」这个具体形态。
  assert.match(
    rowToAccount,
    /updatedAt:\s*normalizeAccountStamp\(\s*r\.updated_at\s*\)/,
    'rowToAccount 里 updatedAt 必须由 normalizeAccountStamp(r.updated_at) 产出',
  )
})

test('rowToAccount 不得把 updated_at 原样透传', () => {
  assert.doesNotMatch(
    rowToAccount,
    /updatedAt:\s*r\.updated_at\b/,
    'rowToAccount 把 updated_at 原样透传了（这正是负控用的那处变异）',
  )
})

test('rowToAccount 不得把 last_synced_at / created_at 一起归一', () => {
  // 反向护栏：归一是按列的语义来的，不是通病。last_synced_at 本来就是毫秒
  // 语义（updateSyncState 写 Date.now()），一起归一会把它改坏。
  assert.doesNotMatch(
    rowToAccount,
    /lastSyncedAt:\s*normalizeAccountStamp\(/,
    'last_synced_at 是毫秒语义，不该被归一成秒',
  )
})

test('saveAccount 写侧落的是秒而不是毫秒', () => {
  // 写侧已经改成 Math.floor(Date.now()/1000)。若哪天改回 Date.now()，
  // 库里又会开始混单位；读侧归一只救存量，救不了新写进去的行。
  assert.match(
    saveAccount,
    /Math\.floor\(\s*Date\.now\(\)\s*\/\s*1000\s*\)/,
    'saveAccount 写 updated_at 时必须落 Unix 秒',
  )
})
