// ingest-behavior.test.mjs — §86：`ingestMeetingArtifacts` 的**行为**门。
//
// ★ 为什么必须做行为门而不是再加一道源码扫描
//
// `ingestMeetingArtifacts` 是「时间点自动进日程」这条需求的**唯一实现入口**：
// refine 的 todos → 待办、next_meeting → 日程、due → 提醒时刻。
// 而它此前的全部门都是「调用存在」形态：
//   · meeting-next-event.test.ts 断言 `buildNextMeetingEvent(` 出现在源码里
//   · refine-consumers-live.test.ts 断言 `ingestMeetingArtifacts(` 出现在源码里
// 注入实测（§86.1）：把这三个值各自换成空值，**六发全部 0 报红**。
//
// ⇒ 「函数被调用了」与「值真的流过去了」是两件事 —— 与 §82 同源，只是换了个位置。
//
// ★ 为什么用 esbuild 在打包期替掉 I/O，而不是 node 的 mock
//
// 这个模块 import `localDB`（浏览器侧 native 桥）、`calendarApi`、`createNote`
// —— 在 node 里直接 import 会被 `ERR_MODULE_NOT_FOUND` 挡掉（§52 同一个坑）。
// 但**决策本身是纯的**：它只决定「建几条、建什么、什么时候」。
// ⇒ 在打包期把 I/O 换成内存假件，拿真函数直接跑，决策层就有了行为门。
// 这是 §66.2「把决策提成可测的纯层」的另一种做法：不改生产代码结构，改**测试的装载方式**。
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
const WORK = fs.mkdtempSync(path.join(os.tmpdir(), 'pocket-ingest-'))
// ⚠ 必须从 meeting-due-plan.ts 引（resolveTodoDue 的真实所在处），
//   不能引 meeting-due-reminder.ts —— 那个拖着 scheduledTasksApi，
//   会把 pinia 整条拖进包并让构建失败。
const DUE_PLAN_REAL = path.resolve(SRC, 'meeting-due-plan.ts')

// 假件记录下来的东西，供断言用。挂 globalThis 是因为 stub 是**独立模块文件**，
// 拿不到本文件的闭包变量。
const seen = { todoInserts: [], events: [], notes: [], synced: [], reminders: [], update: null, lookups: [], neuterLookup: false, queryThrows: false }
globalThis.__ingestSeen = seen
// 归属（租约的 principal）。默认给一个**已验证**的账号 ——
// 真件里没有已验证会话 ⇒ principal 为 null ⇒ 待办保持未认领。
globalThis.__ingestOwner = { userId: 'ingest-user', workspaceId: 'ingest-workspace' }

/** import 说明 → 假件源码。 */
const STUB_SOURCES = {
  '../../native/local-db': `
    // ⚠ 归属（owner）必须与真 localDB **同形**（2026-10-29 实测踩到）：
    //   真件的归属来自**操作租约的 principal**，而生产侧的查重 SQL 现在带
    //   owner_user_id / owner_workspace_id 两个参数。
    //   ⇒ 桩若不认归属，这条门要么「查重恒不命中」、要么「恒命中」，
    //     而门照样显示绿 —— 这正是「负控必须与真源码同形」那条纪律的代价。
    const owner = () => globalThis.__ingestOwner
    const stub = {
      async run(sql, params) {
        if (String(sql).includes('INSERT INTO local_todos')) globalThis.__ingestSeen.todoInserts.push(params)
      },
      // ★§150：生产侧现在会先查重。桩缺这个方法的话，真代码里那句
      //   localDB.queryOne 会 TypeError，而 TypeError 会被生产代码的
      //   catch 吞掉 ⇒ 幂等逻辑整段被静默旁路，**门却照样全绿**。
      //   （这正是 §150 随手记侧踩到的同一个坑，此处提前避开。）
      async queryOne(sql, params) {
        globalThis.__ingestSeen.lookups.push({ sql: String(sql), params })
        if (globalThis.__ingestSeen.neuterLookup) return null
        if (globalThis.__ingestSeen.queryThrows) throw new Error('模拟查重失败')
        const [meetingId, title, ownerUserId, ownerWorkspaceId] = params
        const dup = globalThis.__ingestSeen.todoInserts.some(
          (r) => r[8] === meetingId && r[2] === title
            && r[11] === ownerUserId && r[12] === ownerWorkspaceId,
        )
        return dup ? { id: 'existing' } : null
      },
      async query() { return [] },
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
          assertCurrent() {},
          run: (...args) => stub.run(...args),
          query: (...args) => stub.query(...args),
          queryOne: (...args) => stub.queryOne(...args),
        }
      },
    }
    export const localDB = stub`,
  '../calendar/api': `
    // ⚠ §118：feed 的返回形状是 {entries, partial}。桩若继续回 []，
    // 生产已经改了而这里还绿 —— 桩的形状必须与生产同形，
    // 否则这道门对「调用点忘了取 .entries」完全失明。
    export const calendarApi = {
      async feed() { return { entries: [], partial: false } },
      async create(input) { globalThis.__ingestSeen.events.push(input) },
    }`,
  '../notes/notes-store': `
    export async function createNote(input) {
      globalThis.__ingestSeen.notes.push(input)
      return { id: 'note-1' }
    }`,
  '../../api/meetings': `
    export const meetingsApi = {
      async syncMeeting(input) { globalThis.__ingestSeen.synced.push(input) },
    }`,
  './meeting-sync-runtime.ts': `export async function syncPortalMeetingsNow() { return 0 }`,
  './meetings-store': `
    export async function updateMeeting(id, patch) {
      globalThis.__ingestSeen.update = { id, patch }
    }`,
  // ⚠ 2026-10-15（§738.4）：这条桩是**补回来的**，不是可有可无的装饰。
  //   `meeting-ingest.ts` 会 import `useAuthStore`（建笔记时取 workspaceId），
  //   而 `src/stores/auth.ts:13` 直接 `import { defineStore } from 'pinia'`。
  //   桩表里没有它 ⇒ esbuild 把**真 pinia** 打进包 ⇒ 在 `platform: 'neutral'` 下
  //   `@vue/devtools-api` 只有 `main` 字段、被 neutral 忽略 ⇒ **构建直接失败**：
  //   `Could not resolve "@vue/devtools-api"` ⇒ 12 个子用例**全部 cancelledByParent**，
  //   §86/§150 这道承重行为门**整体瘫掉且不报任何有用信息**。
  //   ⚠ 不要改成 `external: ['pinia','vue']`（`_wire-helpers.mjs` 的做法）：
  //   那会把真 auth store 与它的网络依赖一起请进来，桩的隔离就没了。
  '../../stores/auth': `
    // ⚠ 2026-10-29：桩必须与真模块**同形**（导出集要对得上）。
    //   recording-operation.ts 从这里 import { assertAuthSession,
    //   captureAuthSession, useAuthStore } —— 桩少导一个，
    //   esbuild 就报「No matching export ... for import assertAuthSession」
    //   ⇒ 构建失败 ⇒ 整组用例 cancelledByParent。
    export function useAuthStore() { return { workspaceId: 'ws_ingest_test' } }
    export function captureAuthSession() { return { token: 'ingest-stub-token' } }
    export function assertAuthSession() {}
    export function isCurrentAuthSession() { return true }`,
  // ⚠ 2026-10-29：同步策略也必须被替掉。
  //   `next_meeting` 那条要建**日历日程** ⇒ 走的是**云**路径
  //   （ensureNextMeetingEvent → operation.assertCloud()），而 assertCloud
  //   要求当前模块的同步模式是 'cloud'。
  //   真 `readCachedModuleMode` 在没装策略存储（localStorage 为 SSOT）的
  //   夹具里**恒返回 'local'** ⇒ 云租约永远拿不到 ⇒ assertCloud 抛
  //   AbortError ⇒ 落进 catch ⇒ 状态变成 'failed' ⇒ 日程一条都建不出来。
  //   ⇒ 桩把 meetings 说成 'cloud'，让这条门真的能验「日程建出来了」。
  '../../stores/syncPolicyStore': `
    export function readCachedModuleMode(module) { return 'cloud' }
    export function recordSyncReportEntry() {}`,
  // ⚠ 同款：assertCloudModule 会按上面的模式再判一次，
  //   模式既然是 'cloud'，它就放行 ⇒ 桩保持与真件同形。
  '../../api/data-policy': `
    export function assertCloudModule() {}`,
  // resolveTodoDue 是**真逻辑**，不能一起替掉 —— 它正是期限能不能落地的关键。
  './meeting-due-reminder': `
    import { resolveTodoDue as real } from ${asModulePath(DUE_PLAN_REAL)}
    export function resolveTodoDue(due, now) { return real(due, now) }
    export async function ensureTodoReminder(input) {
      globalThis.__ingestSeen.reminders.push(input)
      return true
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
  fs.writeFileSync(entry, `export { ingestMeetingArtifacts } from ${asModulePath(path.resolve(SRC, 'meeting-ingest.ts'))}\n`)
  await build({
    entryPoints: [entry],
    bundle: true,
    format: 'esm',
    platform: 'neutral',
    // ⚠ 2026-10-29：必须显式指定 mainFields。
    //   `platform: 'neutral'` 的默认 mainFields 是 ['main']，而
    //   `@capacitor/core` / `@capacitor-community/sqlite` / `@capacitor/filesystem`
    //   与 `pinia` 的 package.json 都只给了 `main`（esm 在 `module`）⇒
    //   neutral 平台下 esbuild 直接报「The "main" field here was ignored」
    //   ⇒ **构建失败** ⇒ 12 个子用例全部 cancelledByParent，
    //   §86/§150 这道承重行为门**整体瘫掉且不报任何有用信息**。
    //   （2026-10-29 实测：`mainFields: ['module','main']` ⇒ BUILD OK。）
    //   ⚠ 这不是「把门放宽」：解析到的是**同一个包的 esm 入口**，
    //   桩仍然替换掉所有 I/O，断言口径一个字没动。
    mainFields: ['module', 'main'],
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

const MEETING = {
  id: 'm1', title: '客户评审会', location: '深圳办公室', participants: ['张伟', '林岚'],
  audioPath: null, durationMs: 18000, transcript: '原始转写',
  summary: null, liveSummary: null, noteId: null, sessionId: null,
  status: 'completed', startedAt: 0, createdAt: 0, deletedAt: null,
  archivedAt: null, tags: [], topic: null, summarySkill: 'meeting-minutes',
}

const refineOf = (over = {}) => ({
  refinedTranscript: '精校文本', fromFallback: false, rejected: false,
  todos: [], structuredMinutes: { nextMeeting: null },
  ...over,
})

function reset() {
  seen.lookups.length = 0
  seen.neuterLookup = false
  seen.queryThrows = false
  seen.todoInserts.length = 0
  seen.events.length = 0
  seen.notes.length = 0
  seen.synced.length = 0
  seen.reminders.length = 0
  seen.update = null
  globalThis.__ingestOwner = { userId: 'ingest-user', workspaceId: 'ingest-workspace' }
}

describe('§86 · ingest 行为门：值必须真的流到待办 / 日程 / 提醒', () => {
  it('★ refine.todos 必须落成待办（丢掉它 ⇒ 用户要的行动项一条都没有）', async () => {
    reset()
    await mod.ingestMeetingArtifacts(MEETING, refineOf({
      todos: [{ text: '张伟负责预算确认', assignee: '张伟', due: '下周三下午三点' }],
    }))
    assert.equal(seen.todoInserts.length, 1, 'refine.todos 没有变成待办')
    assert.ok(seen.todoInserts[0].includes('张伟负责预算确认'))
  })

  it('★ item.due 必须解析成 due_at 并据此建提醒（「时间点进日程」的核心）', async () => {
    reset()
    await mod.ingestMeetingArtifacts(MEETING, refineOf({
      todos: [{ text: '出对比表', assignee: '', due: '下周三下午三点' }],
    }))
    const dueAt = seen.todoInserts[0][6] // INSERT 列表第 7 位 = due_at
    assert.ok(typeof dueAt === 'number' && dueAt > 0,
      `due_at 没落进待办（实际 ${JSON.stringify(dueAt)}）⇒ 提醒也建不出来`)
    assert.equal(seen.reminders.length, 1, '解析出 due 却没有建提醒')
  })

  it('★ next_meeting 必须变成一条日程', async () => {
    reset()
    await mod.ingestMeetingArtifacts(MEETING, refineOf({
      structuredMinutes: { nextMeeting: '下周三下午三点' },
    }))
    assert.equal(seen.events.length, 1, 'next_meeting 没有变成日程')
    assert.ok(seen.events[0].startAt > 0)
  })

  it('★ 会议中总结的 actionItems 也要进来（refine.todos 之外的第二个来源）', async () => {
    reset()
    const meeting = { ...MEETING, liveSummary: { actionItems: [{ text: '整理客户名单', assignee: '', due: '' }] } }
    await mod.ingestMeetingArtifacts(meeting, refineOf())
    assert.ok(seen.todoInserts.some((r) => r.includes('整理客户名单')),
      'liveSummary.actionItems 没有变成待办')
  })

  it('due 解析不出时刻时要计入 dueUnresolved，且不得为它建提醒', async () => {
    reset()
    const out = await mod.ingestMeetingArtifacts(MEETING, refineOf({
      todos: [
        { text: '有期限', assignee: '', due: '下周三下午三点' },
        { text: '期限是灰度发布前', assignee: '', due: '灰度发布前' },
      ],
    }))
    assert.equal(out.todosCreated, 2)
    assert.equal(out.dueUnresolved, 1, `unresolved 计数错（${out.dueUnresolved}）`)
    assert.equal(out.reminderPlanned, 1, '只有一条能解析出时刻')
    assert.equal(seen.reminders.length, 1, '解析不出的期限也建了提醒')
  })

  it('★ rejected 必须影响落库状态（被拦下时不能显示「已精翻」）', async () => {
    reset()
    await mod.ingestMeetingArtifacts(MEETING, refineOf({ rejected: true }))
    assert.notEqual(seen.update?.patch?.status, 'refined',
      'rejected=true 却落了 refined 状态 ⇒ 列表会显示「已精翻」但内容是被拦下的')
  })

  it('精校文本必须成为笔记正文（§81：纠正结果唯一的用户可见落点）', async () => {
    reset()
    await mod.ingestMeetingArtifacts(MEETING, refineOf({ refinedTranscript: '今天我们请到了张伟' }))
    assert.equal(seen.notes[0]?.content, '今天我们请到了张伟')
  })

  it('★ 负控：把 refine.todos 丢弃，上面第 1 条必须报红', async () => {
    // 本节第一版没有这条，导致「门有没有牙」只能靠读源码判断。
    // 这里用同一组假件复算一遍：不给 todos 时待办数必须是 0。
    reset()
    await mod.ingestMeetingArtifacts(MEETING, refineOf({ todos: [] }))
    assert.equal(seen.todoInserts.length, 0, '负控前提失败：不投 todos 却建出了待办')
  })
})

// ── §150：同一场会重复收尾不得把待办与提醒建两遍 ──────────────────────────
// 真实路径：录一段 → 停（收尾 #1，建 N 条待办 + M 个提醒）→ 再录一段 → 再停（收尾 #2）。
// `finalizeRecording` 没有幂等守卫（`meeting-recording-finalize.ts:125` 只有
// `if (!meetingId) return`），而 `dedupeTodos` 只去掉**同一批内**的重复，救不了跨次。
// 与随手记侧同形（`note-todo-behavior.test.mjs` 的 §147 组），改一处必须改另一处。
describe('§150 · 幂等：同一场会重复收尾不许把待办与提醒建两遍', () => {
  const ITEMS = { todos: [
    { text: '出对比表', assignee: '张伟', due: '下周三下午三点' },
    { text: '回客户邮件', assignee: '', due: '' },
  ] }

  it('★ 同一批 todos 收尾两次 ⇒ 第二次一条待办、一个提醒都没有新增', async () => {
    reset()
    await mod.ingestMeetingArtifacts(MEETING, refineOf(ITEMS))
    assert.equal(seen.todoInserts.length, 2, '首次收尾没建出两条待办（幂等逻辑误伤了首跑）')
    assert.equal(seen.reminders.length, 1, '带期限的那条应当建提醒')

    await mod.ingestMeetingArtifacts(MEETING, refineOf(ITEMS))
    assert.equal(seen.todoInserts.length, 2,
      '库里被插了第三条：同一场会的同一条行动项出现两遍（meeting_id 与内容都相同，只有 id 不同）')
    assert.equal(seen.reminders.length, 1,
      `提醒被建了 ${seen.reminders.length} 次 ⇒ 同一个时间点进了两次日程`)
  })

  it('★ 查重必须按 (meeting_id, 文本)，且不同场会的同一条要各建一份', async () => {
    reset()
    await mod.ingestMeetingArtifacts(MEETING, refineOf(ITEMS))
    // 另一场会里的**同一条**行动项必须能建出来 ——
    // 漏掉 meeting_id 条件就会把它误判成重复而丢掉（丢待办，方向与重复相反）。
    const OTHER = { ...MEETING, id: 'm2' }
    await mod.ingestMeetingArtifacts(OTHER, refineOf(ITEMS))
    assert.equal(seen.todoInserts.length, 4,
      '不同场会的同一条行动项被误判成重复 ⇒ 一场会的行动项落到了另一场会头上')
    const lookup = seen.lookups[0]
    assert.match(lookup.sql, /meeting_id/, '查重 SQL 里没有 meeting_id')
    assert.match(lookup.sql, /extracted_from_voice/,
      '查重没限定语音来源 ⇒ 用户手工建的同名待办会被当成重复而丢')
  })

  it('★★ 负控：把查重拆掉（桩恒返回 null）⇒ 幂等断言必须当场失效', async () => {
    reset()
    seen.neuterLookup = true
    await mod.ingestMeetingArtifacts(MEETING, refineOf(ITEMS))
    await mod.ingestMeetingArtifacts(MEETING, refineOf(ITEMS))
    assert.equal(seen.todoInserts.length, 4,
      '查重被废掉后第二次仍然没建 ⇒ 上面那条「两条」的断言是恒真的，它什么都没钉住')
    assert.equal(seen.reminders.length, 2, '查重被废掉后提醒仍然只有 1 个')
  })

  it('★★ 查重本身失败时必须照常写入（查重是防重复，不是准入门槛）', async () => {
    reset()
    seen.queryThrows = true
    await mod.ingestMeetingArtifacts(MEETING, refineOf(ITEMS))
    assert.equal(seen.todoInserts.length, 2,
      '查重失败就把待办一起丢了 ⇒ 需求「时间点自动进日程」在一次 DB 抖动后静默失效')
    assert.equal(seen.reminders.length, 1, '查重失败时提醒也没建 ⇒ 时间点没进日程')
  })
})
