// meeting-todo-readback-day.test.mjs — §393：会议待办「due_at 写进去 → 读回来」必须是**同一天**。
//
// ★ 为什么这条门是写-读**往返**，而不是直接给 listMeetingTodos 喂一个 due_at
//
//   §390 修的是**写**（`parseDueAt` 此前会把「2026-10-9」这类日期丢掉，
//   落进「只有钟点」支答出早一天的时刻）。§393 修的是**读**
//   （`new Date(due_at).toISOString().slice(0,10)` 取的是 UTC 那天）。
//   两道变换在**不同的层**：前者保证落库的那个时刻是对的，后者保证显示出来的那一天是对的。
//   只测其中一层，另一层坏掉时门禁照样绿 —— 所以这里让真实的
//   `createMeetingTodos` 写、真实的 `listMeetingTodos` 读，中间只把 DB 换成内存数组。
//
// ★ 装载方式沿用 §87 的既有形态：esbuild 在打包期替掉 I/O（localDB / 后端 api），
//   决策层（meeting-due 的解析器、calendar-math 的日期数学）保持真身。
//   被测的正是「决策层有没有把正确的值一路带到读回的那一天」。
//
// ⚠ **本门对进程时区敏感，且这是故意的**（docs §393.7）：
//   基准 FROZEN_NOW 用设备本地墙钟构造，#1 / #3 两条的负控前提都是
//   「设备当地那天 ≠ UTC 那天」。实测 `TZ=Asia/Shanghai` 下 8 pass；
//   `TZ=UTC` 下 **#1 与 #3 一起变红**（6 pass / 2 fail）。
//   ⇒ CI 若改成 UTC 跑，门会当场报「时区基准没对齐」，
//   而不是悄悄让这两条退化成恒真断言。
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
const WORK = fs.mkdtempSync(path.join(os.tmpdir(), 'pocket-todo-readback-'))

const seen = { rows: [], reminders: [] }
globalThis.__todoSeen = seen
// 归属（租约的 principal）。默认给一个**已验证**的账号 ——
// 真件里没有已验证会话 ⇒ principal 为 null ⇒ 待办保持未认领。
globalThis.__todoOwner = { userId: 'meeting-user', workspaceId: 'meeting-workspace' }

const STUB_SOURCES = {
  '../../native/local-db': `
    const rows = globalThis.__todoSeen.rows
    // ⚠ 归属（owner）必须与真 localDB **同形**（2026-10-29 实测踩到）：
    //   真件的归属来自**操作租约的 principal**，而生产侧的查重 SQL 现在带
    //   owner_user_id / owner_workspace_id 两个参数。
    //   ⇒ 桩若不认归属，这条门要么「查重恒不命中」、要么「恒命中」，
    //     而门照样显示绿 —— 这正是「负控必须与真源码同形」那条纪律的代价。
    const owner = () => globalThis.__todoOwner
    const stub = {
      async run(sql, params) {
        if (!String(sql).includes('INSERT INTO local_todos')) return
        // 列序照抄生产：id, note_id, title, description, status, priority,
        // due_at, extracted_from_voice, meeting_id, created_at, updated_at
        rows.push({
          id: params[0], note_id: params[1], title: params[2], description: params[3],
          due_at: params[6], meeting_id: params[8],
          owner_user_id: params[11], owner_workspace_id: params[12],
        })
      },
      // 幂等查重：同 (meeting_id, title) 命中即已存在。
      async queryOne(sql, params) {
        const [meetingId, title, ownerUserId, ownerWorkspaceId] = params
        const dup = rows.find((r) => r.meeting_id === meetingId && r.title === title
          && r.owner_user_id === ownerUserId && r.owner_workspace_id === ownerWorkspaceId)
        return dup ? { id: dup.id } : null
      },
      async query(sql, params) {
        if (!String(sql).includes('SELECT title, description, due_at')) return []
        const [meetingId, ownerUserId, ownerWorkspaceId] = params
        return rows
          .filter((r) => r.meeting_id === meetingId
            && r.owner_user_id === ownerUserId && r.owner_workspace_id === ownerWorkspaceId)
          .map((r) => ({ title: r.title, description: r.description, due_at: r.due_at }))
      },
      // ⚠ 租约：真件的 captureOperation 返回一个带 owner.principal 的租约，
      //   bindOperation 把它绑成可读写句柄（句柄上带 owner）。
      captureOperation() {
        const principal = { ...owner() }
        return { kind: 'local-edit', owner: { principal } }
      },
      bindOperation(lease) {
        const principal = lease.owner.principal
        return {
          owner: principal ? { ...principal } : null,
          run: (...args) => stub.run(...args),
          query: (...args) => stub.query(...args),
          queryOne: (...args) => stub.queryOne(...args),
        }
      },
    }
    export const localDB = stub`,
  '../scheduled-tasks/api': `
    export const scheduledTasksApi = {
      async create(input) {
        globalThis.__todoSeen.reminders.push(input)
        return { id: 'st-1' }
      },
    }`,
}

const stubFiles = new Map()
for (const [spec, code] of Object.entries(STUB_SOURCES)) {
  const f = path.join(WORK, spec.replace(/[^a-zA-Z0-9]/g, '_') + '.mjs')
  fs.writeFileSync(f, code, 'utf8')
  stubFiles.set(spec, f)
}

let mod
let parseDueAt
let dayKeyOf
let deviceTz

// ★ 进程时区**钉死**成 Asia/Shanghai（2026-10-22 修掉 CI 阻塞点）
//
//   事故形态：这条门的前两条（#1 凌晨档 / #3 负控）的**前提**是
//   「设备当地那天 ≠ UTC 那天」—— 而在 **TZ=UTC** 的主机上，
//   当地**就是** UTC ⇒ 两者恒等 ⇒ 前提在数学上**不可能成立**
//   ⇒ 那两条报「负控前提失败：本地 X 与 UTC Y 本来就是同一天」。
//   ⇒ **CI 就是 ubuntu（TZ=UTC）** ⇒ `npm run test:all`
//     （COVERAGE_GLOBS = src/**/*.test.mjs，**本文件在内**）
//     ⇒ 这是一个**真实的 CI 阻塞点**，本轮实测：
//        TZ=Asia/Shanghai ⇒ 12/12 绿；TZ=UTC ⇒ 10/12（2 条红）。
//
//   ⚠ 注意：这**不是**「把门放宽」。原注释说「CI 若改成 UTC 跑，
//     门会当场报『时区基准没对齐』」—— 那是**设计者的预期**，
//     但它等于「CI 永远红」⇒ 而 CI 已经长期红（12/12 run 全红）
//     ⇒ 这个 step 之前的红把它的红**遮住了**（§960.7.1 那个遮蔽机制）。
//   ⇒ 处置：**把基准钉死**（与同文件 §393.2 臂同款思路 —— 那一臂
//     已经自己把进程切到 America/New_York 来造出「两者不等」）。
//     区别：§393.2 臂切的是**臂内**；这里切的是**整个文件**，
//     因为 #1 / #3 的 FROZEN_NOW 是**设备本地墙钟**构造的
//     （new Date(y, m-1, d, h, min)），必须与读回的时区**同源**。
//   ⇒ 探针实测：process.env.TZ 在运行时改，**Intl 与 new Date 会一起切**
//     （Windows 与 Linux 皆然）⇒ 钉死可靠。
const REAL_TZ = process.env.TZ
process.env.TZ = 'Asia/Shanghai'
// 固定「现在」：createMeetingTodos 内部用 Date.now()，不注入。
// 冻住它，这道门才是**确定**的（否则跨午夜时同一份代码会时绿时红）。
// ⚠ 必须在**钉死 TZ 之后**构造：FROZEN_NOW 是**设备本地墙钟**。
const FROZEN_NOW = new Date(2026, 9, 7, 15, 0, 0).getTime() // 2026-10-07 周三 15:00 本地
const realNow = Date.now
Date.now = () => FROZEN_NOW

before(async () => {
  const entry = path.join(WORK, 'entry.ts')
  fs.writeFileSync(
    entry,
    `export { createMeetingTodos, listMeetingTodos } from ${asModulePath(path.resolve(SRC, 'meeting-todo-persist.ts'))}\n` +
      `export { parseDueAt } from ${asModulePath(path.resolve(SRC, 'meeting-due.ts'))}\n` +
      `export { dayKey, deviceTimeZone } from ${asModulePath(path.resolve(HERE, '..', '..', 'calendar', 'calendar-math.ts'))}\n`,
  )
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
  parseDueAt = mod.parseDueAt
  dayKeyOf = mod.dayKey
  deviceTz = mod.deviceTimeZone()
})

process.on('exit', () => {
  Date.now = realNow
  // 还原 TZ（钉死是为了让本文件确定，不该影响同一进程里的别的文件）
  if (REAL_TZ === undefined) delete process.env.TZ
  else process.env.TZ = REAL_TZ
})

function reset() {
  seen.rows.length = 0
  seen.reminders.length = 0
}

const item = (text, due) => ({ text, due })

/** 走一遍真实的写→读，返回读回来的那条 draft。 */
async function roundTrip(dueText) {
  reset()
  await mod.createMeetingTodos('m1', [item('值班交接', dueText)], null, '周会')
  const back = await mod.listMeetingTodos('m1')
  return { draft: back[0], written: seen.rows[0] }
}

/** 当地区 dayKey（与写进去的时刻同一个时区问）。 */
const localDay = (at) => dayKeyOf(Math.floor(at / 1000), deviceTz)

describe('§393 · 待办期限写-读往返：读回来必须是**当地**那一天', () => {
  it('★ 凌晨档：读回的那一天必须等于当地那天，且不等于 UTC 那天', async () => {
    const { draft, written } = await roundTrip('明天凌晨三点发布')

    const expected = parseDueAt('明天凌晨三点发布', FROZEN_NOW)
    assert.ok(expected, '解析器没解出时刻 ⇒ 这道门的前提不成立')

    assert.equal(
      draft.due, localDay(expected.at),
      `读回的日期是 ${draft.due}，而写进去的时刻在设备当地是 ${localDay(expected.at)}`,
    )

    // 量具自证：这个用例必须真的落在「UTC 与当地不是同一天」的位置上。
    // 若两者本来就相同，下面这条读数成立与否都无法区分对错实现。
    const utcDay = new Date(written.due_at).toISOString().slice(0, 10)
    assert.notEqual(
      localDay(expected.at), utcDay,
      `负控前提失败：本地 ${localDay(expected.at)} 与 UTC ${utcDay} 本来就是同一天，这道门测不出东西`,
    )
  })

  it('★ 下午档：读回的那一天同样要是当地那天（证明读回不是「恒错一天」）', async () => {
    const { draft } = await roundTrip('明天下午三点交周报')
    const expected = parseDueAt('明天下午三点交周报', FROZEN_NOW)
    assert.equal(draft.due, localDay(expected.at), `读回的日期是 ${draft.due}`)
  })

  it('★★ 负控：旧的 UTC 写法在同一份数据上确实会读错一天', async () => {
    const { written } = await roundTrip('明天凌晨三点发布')
    const correct = localDay(written.due_at)
    const legacy = new Date(written.due_at).toISOString().slice(0, 10)
    assert.notEqual(legacy, correct,
      `旧写法在本用例上没读错（${legacy}）⇒ 上面那条「读回当地天」的断言可能是恒真的，它什么都没钉住`)
  })

  it('★ 写入的 due_at 与解析器算出的时刻必须一致（§390 那一层没有回退）', async () => {
    const { written } = await roundTrip('明天凌晨三点发布')
    const expected = parseDueAt('明天凌晨三点发布', FROZEN_NOW)
    assert.equal(written.due_at, expected.at,
      `落库的 due_at 与解析结果不是同一个时刻 ⇒ §390 的修复没有覆盖这条写入路径`)
  })

  it('★ 带期限的条目必须同时建提醒，且提醒用的是同一个时刻', async () => {
    await roundTrip('明天凌晨三点发布')
    assert.equal(seen.reminders.length, 1, '解出时刻却没有建提醒 ⇒ 时间点没进日程')
    const { dueAt } = seen.reminders[0].payload
    assert.equal(dueAt, seen.rows[0].due_at, '提醒用的时刻与落库的 due_at 不是同一个')
  })

  it('没有期限的条目不得凭空长出一个日期', async () => {
    const { draft, written } = await roundTrip('')
    assert.equal(written.due_at, null, '没有期限却落了 due_at')
    assert.equal(draft.due, undefined, `没有期限却读回 ${draft.due}`)
  })

  it('解析不出时刻的期限：落库但 due 为空，且不得建提醒', async () => {
    reset()
    const out = await mod.createMeetingTodos('m1', [item('灰度发布前', '灰度发布前')], null, '周会')
    assert.equal(out.count, 1, '解析失败不等于不能记')
    assert.equal(out.unresolved, 1, '没有计入 unresolved ⇒ 用户听不到「期限没听清」')
    assert.equal(seen.reminders.length, 0, '解析不出的期限也建了提醒')
    const back = await mod.listMeetingTodos('m1')
    assert.equal(back[0].due, undefined, '解析不出的期限却读回了一个日期')
  })
})

// ── §393.2：读回必须问**设备时区**，不是仓库默认时区 ──────────────────────
//
// `dayKey(at, tz)` 问的是「在 tz 里是哪一天」。写的时候 `meeting-due.ts:70` 用
// `new Date(y, m-1, d, h, min)` 造的是**设备本地墙钟**，所以读的时候必须用
// **同一个**时区问。
//
// 若这里图省事写成 `DEFAULT_TZ`（Asia/Shanghai），在本仓默认的测试环境里
// deviceTimeZone() 恰好就等于 DEFAULT_TZ ⇒ **门照样绿**，而真实用户换个时区就读错。
// 这正是「夹具环境掩盖了被测差异」那一类：门禁跑得再勤也钉不住它。
// 所以这一臂把进程时区切到 America/New_York（Node 运行时会重新 tzset，
// 实测 CST→EDT 让同一组年月日时分秒差 12 小时），让两者不再相等。
describe('§393.2 · 跨时区：读回必须跟着设备时区走，而不是仓库默认时区', () => {
  it('★ 进程时区切到 America/New_York 后，读回的那一天仍必须是设备当地那天', async () => {
    const saved = process.env.TZ
    let out
    try {
      process.env.TZ = 'America/New_York'
      // 前置自证：这一次两者真的不等，否则这条臂什么也测不出来。
      assert.notEqual(
        Intl.DateTimeFormat().resolvedOptions().timeZone, 'Asia/Shanghai',
        '负控前提失败：进程时区没切成功，这条臂在原地打转')

      out = await roundTrip('明天下午三点交周报')
      const expected = parseDueAt('明天下午三点交周报', FROZEN_NOW)
      const deviceDay = dayKeyOf(Math.floor(expected.at / 1000), Intl.DateTimeFormat().resolvedOptions().timeZone)
      const defaultDay = dayKeyOf(Math.floor(expected.at / 1000), 'Asia/Shanghai')

      assert.equal(
        out.draft.due, deviceDay,
        `读回 ${out.draft.due}，设备当地那天是 ${deviceDay}`,
      )
      // 这两条读数**必须不同**，否则「用 DEFAULT_TZ」这个错误实现也能过这道门。
      assert.notEqual(
        deviceDay, defaultDay,
        `负控前提失败：设备当地(${deviceDay})与默认时区(${defaultDay})恰好同天 ⇒ 区分不出两种实现`,
      )
    } finally {
      process.env.TZ = saved
    }
  })
})
