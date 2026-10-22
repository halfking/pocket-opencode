// meeting-todo-persist-idempotency.test.mjs — §153：`createMeetingTodos` 的**行为**门（幂等那一半）。
//
// ★ 为什么这条要新开一个文件，而不是并进现有门
//
// 现有两道关于 `createMeetingTodos` 的门（`meeting-due-plan.test.ts`、
// `notes/reminder-outcome-honesty.test.ts`）**全是源码扫描**：读文件文本、匹配正则。
// 它们能钉「返回值没被丢弃」「文案拼上了提醒结果」，但**钉不到「有没有真的查重」** ——
// 查重是一次运行期的数据查询，源码里那几行 SQL 文本一个字没变。
//
// ⇒ 形态与 §86 / §87 同款：**esbuild 在打包期替掉 I/O，拿真函数直接跑**。
//   纯逻辑（`draftsFromActionItems` / `resolveTodoDue` / `buildReminderInput`）一律保留真身，
//   只替两个真正的出口：`localDB`（本地库）与 `scheduledTasksApi`（远端提醒）。
//
// ★ 全仓一共三处 `INSERT INTO local_todos`（普查得来，不是手查链得来的）：
//   ① features/notes/note-todo-persist.ts        （§150 修）
//   ② features/meetings/meeting-ingest.ts        （§153 修）
//   ③ features/meetings/meeting-todo-persist.ts   （本文件，§153.1 修）
//   ⇒ 三份实现必须**同形**，改一条语义要同时改三处。
import assert from 'node:assert/strict'
import { describe, it, before } from 'node:test'
import path from 'node:path'
import fs from 'node:fs'
import os from 'node:os'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { build } from 'esbuild'


// ★ 写入口的路径归一（2026-10-22 Windows 实测修掉）
//
//   事故形态：把 Windows 绝对路径**裸插**进单引号字符串字面量 ⇒
//   反斜杠开头的 w / p / f / n 全被 JS 解析成转义序列
//   ⇒ 路径被吃成 "C:workspacepocket-opencode" + 换行 + "ote-todo-persist.ts"
//   ⇒ esbuild 解析不到 ⇒ before 钩子抛错 ⇒ 整组子用例 cancelledByParent
//   ⇒ Linux 上路径是 "/workspace/..."，没有反斜杠 ⇒ **Linux 一直绿**
//   ⇒ 这是 Windows 专属红：不阻塞 CI，但会让 Windows 上的本地读数完全失真。
//
//   修法两步（缺一不可）：
//   ① 正斜杠化：Windows 的 fs 与 esbuild 都认 "C:/a/b" 这种形态
//   ② JSON.stringify：做 JS 字符串字面量转义 ⇒ 路径里再有引号/反斜杠也不会破
//
//   ⚠ 归一在**写进入口**（不是逐个路径改对）—— 与 D6–D10、§88 同族纪律。
//   ⚠ 本 helper **不带 TypeScript 注解**：本仓有用例是 .mjs（纯 JS），
//     写上参数类型注解会直接 SyntaxError（本轮实测踩到）。
//   ⚠ 注释里**绝不能出现反引号**：Node 的 TS stripper 会把 // 注释里的
//     反引号当成模板串开头 ⇒ 后面整行被当代码解析（本轮实测踩到）。
function asModulePath(p) {
  return JSON.stringify(String(p).replace(/\\/g, '/'))
}

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..') // → src/features/meetings
const WORK = fs.mkdtempSync(path.join(os.tmpdir(), 'pocket-meeting-todo-'))

// INSERT 列表的列序：id, note_id, title, description, status, priority, due_at,
//                     extracted_from_voice, meeting_id, created_at, updated_at
const COL = { noteId: 1, title: 2, dueAt: 6, voice: 7, meetingId: 8 }

const seen = { inserts: [], scheduled: [], lookups: [], neuterLookup: false, queryThrows: false }
globalThis.__mtSeen = seen

const STUB_SOURCES = {
  '../../native/local-db': `
    export const localDB = {
      async run(sql, params) {
        if (!String(sql).includes('INSERT INTO local_todos')) return
        globalThis.__mtSeen.inserts.push(params)
      },
      // ⚠ 缺这个方法的话，真代码里那句 queryOne 会 TypeError，
      //   而 TypeError 会被生产代码的 catch 吞掉 ⇒ 幂等整段被静默旁路，
      //   **门却照样全绿**（随手记侧 §150 实测踩过这个坑）。
      async queryOne(sql, params) {
        globalThis.__mtSeen.lookups.push({ sql: String(sql), params })
        if (globalThis.__mtSeen.neuterLookup) return null
        if (globalThis.__mtSeen.queryThrows) throw new Error('模拟查重失败')
        const [meetingId, title] = params
        const dup = globalThis.__mtSeen.inserts.some(
          (r) => r[8] === meetingId && r[2] === title,
        )
        return dup ? { id: 'existing' } : null
      },
    }`,
  '../scheduled-tasks/api': `
    export const scheduledTasksApi = {
      async create(input) { globalThis.__mtSeen.scheduled.push(input); return input },
      async list() { return [] },
    }`,
}

const stubFiles = new Map()
for (const [spec, code] of Object.entries(STUB_SOURCES)) {
  const f = path.join(WORK, spec.replace(/[^a-zA-Z0-9]/g, '_') + '.mjs')
  fs.writeFileSync(f, code, 'utf8')
  stubFiles.set(spec, f)
}

let mod

before(async () => {
  const entry = path.join(WORK, 'entry.ts')
  fs.writeFileSync(entry,
    `export { createMeetingTodos } from ${asModulePath(path.resolve(SRC, 'meeting-todo-persist.ts'))}\n`)
  await build({
    entryPoints: [entry],
    bundle: true,
    format: 'esm',
    platform: 'neutral',
    outfile: path.join(WORK, 'out.mjs'),
    logLevel: 'error',
    plugins: [{
      name: 'stub-io',
      setup(b) {
        for (const [spec, file] of stubFiles) {
          const re = new RegExp(spec.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$')
          b.onResolve({ filter: re }, () => ({ path: file }))
        }
      },
    }],
  })
  mod = await import(pathToFileURL(path.join(WORK, 'out.mjs')).href)
})

function reset() {
  seen.inserts.length = 0
  seen.scheduled.length = 0
  seen.lookups.length = 0
  seen.neuterLookup = false
  seen.queryThrows = false
}

const ITEMS = [
  { text: '出对比表', assignee: '张伟', due: '明天下午三点' },
  { text: '回客户邮件', assignee: '', due: '' },
]

describe('§153 · 幂等：详情页「总结」重复点不许把待办与提醒建两遍', () => {
  it('★ 同一批 items 连点两次 ⇒ 第二次一条待办、一个提醒都没有新增', async () => {
    reset()
    const first = await mod.createMeetingTodos('m1', ITEMS, null, '客户评审会')
    assert.equal(first.count, 2, '首次没建出两条待办（幂等逻辑误伤了首跑）')
    assert.equal(seen.inserts.length, 2)
    assert.equal(first.reminders, 1, '带期限的那条应当建提醒')
    assert.equal(seen.scheduled.length, 1, '提醒没有真的发到 scheduledTasksApi')

    const second = await mod.createMeetingTodos('m1', ITEMS, null, '客户评审会')
    assert.equal(second.count, 0,
      `重复点又建了 ${second.count} 条 ⇒ 同一条行动项在待办里出现两遍（meeting_id 与内容都相同，只有 id 不同）`)
    assert.equal(seen.inserts.length, 2, '库里被插了第三条')
    assert.equal(seen.scheduled.length, 1,
      `提醒发了 ${seen.scheduled.length} 次 ⇒ 同一个时间点进了两次日程`)
  })

  it('★ 查重必须按 (meeting_id, 文本)，且不同场会的同一条要各建一份', async () => {
    reset()
    await mod.createMeetingTodos('m1', ITEMS, null, '会1')
    await mod.createMeetingTodos('m2', ITEMS, null, '会2')
    assert.equal(seen.inserts.length, 4,
      '不同场会的同一条行动项被误判成重复 ⇒ 一场会的行动项落到了另一场会头上')
    const lookup = seen.lookups[0]
    assert.match(lookup.sql, /meeting_id/, '查重 SQL 里没有 meeting_id')
    assert.match(lookup.sql, /extracted_from_voice/,
      '查重没限定语音来源 ⇒ 用户手工建的同名待办会被当成重复而丢')
    // 插进去的行必须真的带上了 meeting_id（否则上面那条断言在测一个不生效的字段）
    assert.equal(seen.inserts[0][COL.meetingId], 'm1', 'INSERT 没有带上 meeting_id')
    assert.equal(seen.inserts[0][COL.voice], 1, 'INSERT 没有标记 extracted_from_voice')
  })

  it('★★ 负控：把查重拆掉（桩恒返回 null）⇒ 幂等断言必须当场失效', async () => {
    reset()
    seen.neuterLookup = true
    await mod.createMeetingTodos('m1', ITEMS, null, '会1')
    await mod.createMeetingTodos('m1', ITEMS, null, '会1')
    assert.equal(seen.inserts.length, 4,
      '查重被废掉后第二次仍然没建 ⇒ 上面那条「两条」的断言是恒真的，它什么都没钉住')
    assert.equal(seen.scheduled.length, 2, '查重被废掉后提醒仍然只有 1 个')
  })

  it('★★ 查重本身失败时必须照常写入（查重是防重复，不是准入门槛）', async () => {
    reset()
    seen.queryThrows = true
    const out = await mod.createMeetingTodos('m1', ITEMS, null, '会1')
    assert.equal(out.count, 2,
      '查重失败就把待办一起丢了 ⇒ 需求「时间点自动进日程」在一次 DB 抖动后静默失效')
    assert.equal(seen.inserts.length, 2, '查重失败时没有落库')
    assert.equal(out.reminders, 1, '查重失败时提醒也没建 ⇒ 时间点没进日程')
  })
})
