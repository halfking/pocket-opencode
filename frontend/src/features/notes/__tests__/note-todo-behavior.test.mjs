// note-todo-behavior.test.mjs — §87：随手记链 `createNoteTodos` 的**行为**门。
//
// ★ 为什么这条链也需要（它此前的门全是源码扫描）
//
// `note-todo-persist.test.ts` 的判据形态是「源码里必须出现 `localDB.run(`」
// 「NoteListView 必须 `await createNoteTodos(`」—— 全是**调用存在**。
// 注入实测（§87.1）：六个值逐个换成空值，**三发 0 报红**，其中包括
// 「`items` 整个丢弃」—— 即语音笔记的行动项全部消失，而门禁一声不响。
//
// 这条链是会议侧 `meeting-ingest.ts` 的**对偶实现**（§66.7 的口径就是照抄它的），
// 会议侧已在 §86 补上行为门；本文件补上另一半。
//
// ★ 装载方式与 §86 同款：esbuild 在打包期替掉 I/O，决策层保持真身。
//   `planNoteTodos`（note-todo-plan.ts）是**真逻辑**，它决定 dueAt / remind / dueText，
//   本门验的就是「执行层有没有把它的决定兑现」。
import assert from 'node:assert/strict'
import { describe, it, before } from 'node:test'
import path from 'node:path'
import fs from 'node:fs'
import os from 'node:os'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { build } from 'esbuild'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..') // → src/features/notes
const WORK = fs.mkdtempSync(path.join(os.tmpdir(), 'pocket-note-todo-'))

const seen = { inserts: [], reminders: [], failNext: 0, lookups: [], neuterLookup: false, queryThrows: false }
globalThis.__noteSeen = seen

const STUB_SOURCES = {
  '../../native/local-db': `
    export const localDB = {
      async run(sql, params) {
        if (!String(sql).includes('INSERT INTO local_todos')) return
        if (globalThis.__noteSeen.failNext > 0) {
          globalThis.__noteSeen.failNext -= 1
          throw new Error('模拟单条入库失败')
        }
        globalThis.__noteSeen.inserts.push(params)
      },
      // ★§147：生产侧现在会先查重。桩必须与真 localDB **同形** ——
      //   只给 run 的话，真代码里那句 localDB.queryOne 会 TypeError，
      //   而 TypeError 会被本函数的 catch 吞掉 ⇒ 幂等逻辑被静默旁路，
      //   门却还显示「通过」。这正是「负控必须与真源码同形」那条纪律的代价。
      async queryOne(sql, params) {
        globalThis.__noteSeen.lookups.push({ sql: String(sql), params })
        // ★ 负控开关：把查重**拆掉**，用来证明上面那条幂等断言真的有牙。
        if (globalThis.__noteSeen.neuterLookup) return null
        if (globalThis.__noteSeen.queryThrows) throw new Error('模拟查重失败')
        const [noteId, title] = params
        const dup = globalThis.__noteSeen.inserts.some(
          (r) => r[1] === noteId && r[2] === title,
        )
        return dup ? { id: 'existing' } : null
      },
    }`,
  '../meetings/meeting-due-reminder': `
    export async function ensureTodoReminder(input) {
      globalThis.__noteSeen.reminders.push(input)
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
  fs.writeFileSync(entry, `export { createNoteTodos } from '${path.resolve(SRC, 'note-todo-persist.ts')}'\n`)
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
  seen.lookups.length = 0
  seen.reminders.length = 0
  seen.failNext = 0
  seen.neuterLookup = false
  seen.queryThrows = false
}

const item = (text, due, assignee = '') => ({ text, due, assignee })

describe('§87 · 随手记行为门：值必须真的流到待办 / 提醒', () => {
  it('★ items 必须落成待办（丢掉它 ⇒ 语音笔记的行动项全消失且无人发现）', async () => {
    reset()
    const out = await mod.createNoteTodos('n1', [item('整理客户名单', '')], '随手记')
    assert.equal(out.created, 1, 'items 没有变成待办')
    assert.equal(seen.inserts.length, 1)
    assert.ok(seen.inserts[0].includes('整理客户名单'))
    assert.equal(seen.inserts[0][1], 'n1', 'note_id 没落到待办')
  })

  it('★ due 必须落成 due_at（这是「时间点自动进日程」的一半）', async () => {
    reset()
    await mod.createNoteTodos('n1', [item('出对比表', '下周三下午三点')])
    const dueAt = seen.inserts[0][6] // INSERT 第 7 位 = due_at
    assert.ok(typeof dueAt === 'number' && dueAt > 0,
      `due_at 没落进待办（实际 ${JSON.stringify(dueAt)}）`)
  })

  it('★ 解出时刻的期限必须建提醒，且 source 必须是 note-voice', async () => {
    reset()
    const out = await mod.createNoteTodos('n1', [item('出对比表', '下周三下午三点', '张伟')], '随手记')
    assert.equal(out.reminderPlanned, 1)
    assert.equal(out.reminders, 1)
    assert.equal(seen.reminders[0].source, 'note-voice')
    assert.equal(seen.reminders[0].meetingTitle, '随手记', '笔记标题没进提醒')
  })

  it('★ unresolved 必须累加，且**不得**为解不出的期限建提醒', async () => {
    reset()
    const out = await mod.createNoteTodos('n1', [
      item('有期限', '下周三下午三点'),
      item('期限是灰度发布前', '灰度发布前'),
    ])
    assert.equal(out.created, 2, '两条都该落库（解析失败不等于不能记）')
    assert.equal(out.unresolved, 1, `unresolved 计数错（${out.unresolved}）⇒ 用户听不到「期限没听清」`)
    assert.equal(out.reminderPlanned, 1)
    assert.equal(seen.reminders.length, 1, '解析不出的期限也建了提醒')
  })

  it('★ 单条入库失败不中断后面几条', async () => {
    reset()
    seen.failNext = 1
    const out = await mod.createNoteTodos('n1', [
      item('第一条会失败', '下周三下午三点'),
      item('第二条必须留下', '下周三下午三点'),
    ])
    assert.equal(seen.inserts.length, 1, '第二条不该被第一条的失败牵连')
    assert.equal(out.created, 1)
  })

  it('负控：不投 items 时待办数必须是 0', async () => {
    reset()
    const out = await mod.createNoteTodos('n1', [])
    assert.equal(out.created, 0, '负控前提失败：不投 items 却建出了待办')
    assert.equal(seen.inserts.length, 0)
  })
})

// ── §147：同一条行动项不得重复落库，也不得重复建提醒 ──────────────────────
// 真实路径：录完音自动总结（建 N 条）→ 用户改正文里的错字 → 再点总结（又建 N 条）。
// 详见 docs §147；这条门钉的是「同一条不许重复」这一半，
// 「内容变了怎么办」是产品取舍，仍未拍板。
describe('§147 · 幂等：重复总结不许把待办与提醒建两遍', () => {
  it('★ 同一批 items 总结两次 ⇒ 第二次 created=0，且一条待办、一条提醒都没有新增', async () => {
    reset()
    const items = [item('整理客户名单', '明天下午三点', '张伟'), item('回邮件', '')]
    const first = await mod.createNoteTodos('n1', items, '随手记')
    assert.equal(first.created, 2, '首次总结没建出两条待办（幂等逻辑误伤了首跑）')
    assert.equal(seen.reminders.length, 1, '带期限的那条应当建提醒')

    const second = await mod.createNoteTodos('n1', items, '随手记')
    assert.equal(second.created, 0,
      `重复总结又建了 ${second.created} 条 ⇒ 同一件事在待办里出现两遍（note_id 与内容都相同，只有 id 不同）`)
    assert.equal(seen.inserts.length, 2, '库里被插了第三条：重复总结没有查重')
    assert.equal(seen.reminders.length, 1,
      `提醒被建了 ${seen.reminders.length} 次 ⇒ 同一个时间点进了两次日程`)
  })

  it('★ 查重必须按 (note_id, 文本) 两个条件，不能只看文本', async () => {
    reset()
    await mod.createNoteTodos('n1', [item('整理客户名单', '')], '随手记')
    // 另一条笔记里的同一条行动项 **必须**能建出来 ——
    // 否则「不同笔记说了同一件事」会被误判成重复，而那不是重复。
    const other = await mod.createNoteTodos('n2', [item('整理客户名单', '')], '随手记2')
    assert.equal(other.created, 1, '不同笔记的同一条行动项被误判成重复而丢掉了')
    const lookup = seen.lookups[0]
    assert.match(lookup.sql, /note_id/, '查重 SQL 里没有 note_id')
    assert.match(lookup.sql, /extracted_from_voice/,
      '查重没有限定语音来源 ⇒ 用户手工建的同名待办会被当成重复而丢')
  })

  it('★★ 负控：把查重拆掉（桩恒返回 null）⇒ 幂等断言必须当场失效', async () => {
    reset()
    seen.neuterLookup = true          // 查重这一层被废掉
    const items = [item('整理客户名单', '明天下午三点', '张伟')]
    await mod.createNoteTodos('n1', items, '随手记')
    const second = await mod.createNoteTodos('n1', items, '随手记')
    // 这正是修复前的行为：第二次照样插入、照样建提醒。
    assert.equal(second.created, 1,
      '查重被废掉后第二次仍然没建 ⇒ 上面那条「created=0」的断言是恒真的，它什么都没钉住')
    assert.equal(seen.reminders.length, 2,
      '查重被废掉后提醒仍然只有 1 个 ⇒ 上面那条提醒断言是恒真的')
  })

  it('★★ 查重本身失败时必须照常写入（查重是防重复，不是准入门槛）', async () => {
    reset()
    seen.queryThrows = true           // 查重这一层抛异常
    const out = await mod.createNoteTodos('n1', [item('整理客户名单', '明天下午三点')], '随手记')
    assert.equal(out.created, 1,
      '查重失败就把待办一起丢了 ⇒ 需求「时间点自动进日程」在一次 DB 抖动后静默失效')
    assert.equal(seen.inserts.length, 1, '查重失败时没有落库')
    assert.equal(seen.reminders.length, 1, '查重失败时提醒也没建 ⇒ 时间点没进日程')
  })
})
