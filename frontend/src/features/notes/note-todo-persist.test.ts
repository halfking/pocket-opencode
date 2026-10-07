// note-todo-persist.test.ts — 门禁：「语音笔记里的时间点必须真的进日程」。
//
// 这道门守的是需求「录音时即时总结，并把一些时间点自动加入计划日程」
// 在**随手记侧**的落点。此前这条链路整条不存在：录音停止后只调
// /api/notes/{id}/summarize 拿一个 summary 字符串，服务端从不返回期限，
// 于是「明天下午三点」这类时间点在语音笔记里彻底消失——不进待办、
// 不进日程、界面上也不出现。
//
// 分三层，每层带负控（只写「全绿」的断言等于没写）：
//   A. 行为层：中文期限解出时刻；提醒 expr 带的是真实 due 而非 now+60s。
//   B. 接线层：后端必须返回 action_items、前端必须真的调用 createNoteTodos。
//   C. 负控层：证明 A/B 的判据不是恒真。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { buildReminderInput, resolveTodoDue } from '../meetings/meeting-due-plan.ts'
import { planNoteTodos } from './note-todo-plan.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (f: string) => readFileSync(join(HERE, f), 'utf8')
// HERE = frontend/src/features/notes ⇒ 仓库根要上溯 4 级
// （notes → features → src → frontend → <repo>）。
const readRepo = (f: string) =>
  readFileSync(join(HERE, '..', '..', '..', '..', f), 'utf8')

// 2026-10-07 是周三 15:04（本地）。
const NOW = new Date(2026, 9, 7, 15, 4).getTime()
const MINUTE = 60 * 1000
const HOUR = 60 * MINUTE

describe('A · 行为层：笔记行动项的期限解析与会议侧同口径', () => {
  it('中文期限必须解出时刻（笔记侧复用 resolveTodoDue，不另造解析器）', () => {
    for (const due of ['明天下午三点', '今天晚上八点半', '周五上午10点', '下周一']) {
      const parsed = resolveTodoDue(due, NOW)
      assert.ok(parsed, `「${due}」必须能解析出时刻`)
      assert.ok(parsed.at > NOW, `「${due}」应指向未来`)
    }
  })

  it('负控：解不出的期限仍为 null（不能为了「总能建提醒」而瞎猜时间）', () => {
    for (const due of ['', '   ', '尽快', '有空的时候']) {
      assert.equal(resolveTodoDue(due, NOW), null, `「${due}」不该被硬解出时间`)
    }
    assert.equal(resolveTodoDue(undefined, NOW), null)
    assert.equal(resolveTodoDue(null, NOW), null)
  })

  it('note-voice 来源的提醒 expr 带真实 due，且 prompt 不谎称来自会议', () => {
    const due = '明天下午三点'
    const parsed = resolveTodoDue(due, NOW)!
    const input = buildReminderInput({
      text: '把回滚脚本补上', dueText: due, at: parsed.at, source: 'note-voice',
    })
    assert.equal(input.scheduleKind, 'at')
    assert.equal(
      Math.floor(Date.parse(input.scheduleExpr) / MINUTE),
      Math.floor(parsed.at / MINUTE),
      '提醒 expr 必须是 due 解析出的时刻',
    )
    const payload = input.payload as { prompt: string; source: string; dueAt: number }
    assert.equal(payload.source, 'note-voice')
    assert.match(payload.prompt, /回滚脚本/)
    // 笔记来源没有会议标题：不能读成「来源会议：」（空值）。
    assert.ok(!/来源会议：\s*$|来源会议：\n/.test(payload.prompt), `prompt 不该出现空的来源会议行：${payload.prompt}`)
    assert.match(payload.prompt, /语音笔记/, '笔记来源应标明来自语音笔记')
  })

  it('负控：note-voice 不带标题时不得凭空造出会议名', () => {
    const parsed = resolveTodoDue('明天', NOW)!
    const input = buildReminderInput({ text: 'x', at: parsed.at, source: 'note-voice' })
    const payload = input.payload as { prompt: string }
    assert.ok(!payload.prompt.includes('undefined'), `prompt 不该含 undefined：${payload.prompt}`)
  })

  it('负控对照：Date.parse 对中文 due 确实无解（证明本门在守真问题）', () => {
    assert.ok(Number.isNaN(Date.parse('明天下午三点')), 'Date.parse 不认中文，这条前提不能变')
  })
})

describe('B0 · 行为层：planNoteTodos 决定「哪些条目要建提醒」', () => {
  const mk = (items: Parameters<typeof planNoteTodos>[0]['items']) =>
    planNoteTodos({ items, now: NOW, makeId: (i) => `id-${i}` })

  it('能解出时刻的条目必须标记为要建提醒，且时刻就是 due 解析结果', () => {
    const plans = mk([{ text: '补回滚脚本', due: '明天下午三点' }])
    assert.equal(plans.length, 1)
    assert.equal(plans[0].remind, true, '解出时刻就必须建提醒 —— 否则时间点进不了计划日程')
    assert.equal(plans[0].dueAt, resolveTodoDue('明天下午三点', NOW)!.at)
    assert.equal(plans[0].dueText, '明天下午三点', '要保留用户原话，便于提醒时回溯')
  })

  it('负控：解不出时刻的条目不得建提醒（不建假日程）', () => {
    const plans = mk([{ text: '有空再看看', due: '尽快' }, { text: '没提期限' }])
    assert.equal(plans.length, 2)
    for (const p of plans) {
      assert.equal(p.remind, false, `「${p.text}」不该建提醒`)
      assert.equal(p.dueAt, null)
    }
  })

  it('重复行动项只留一条（切片重叠的转写里模型很容易吐两遍）', () => {
    const plans = mk([
      { text: '补回滚脚本', due: '明天下午三点' },
      { text: '  补回滚脚本  ', due: '明天下午三点' },
      { text: '补回滚脚本', due: '周五上午10点' },
    ])
    assert.equal(plans.length, 1, '同一条行动项不应产生两条待办')
    assert.equal(plans[0].text, '补回滚脚本')
  })

  it('混合输入：三条里只有能解出时刻的那条建提醒', () => {
    const plans = mk([
      { text: '甲', due: '尽快' },
      { text: '乙', due: '周五上午10点' },
      { text: '丙' },
    ])
    const reminding = plans.filter((p) => p.remind)
    assert.equal(reminding.length, 1)
    assert.equal(reminding[0].text, '乙')
  })

  it('assignee 只在非空时带上（空串不该写成「负责人：」）', () => {
    const plans = mk([{ text: '甲', assignee: '   ' }, { text: '乙', assignee: '张三' }])
    assert.equal(plans[0].assignee, undefined)
    assert.equal(plans[1].assignee, '张三')
  })

  it('负控：空/缺字段的输入不得产生任何计划', () => {
    assert.deepEqual(mk([]), [])
    assert.deepEqual(mk(null), [])
    assert.deepEqual(mk(undefined), [])
    assert.deepEqual(mk([{ text: '   ' }]), [])
  })
})

describe('B · 接线层：后端返回 action_items，前端真的建待办（防回退）', () => {  it('后端笔记总结必须返回 action_items 且解析出结构化条目', () => {
    const src = readRepo('backend/internal/server/server_assistant.go')
    assert.match(src, /parseNoteSummaryPayload/, '笔记总结未接入 action_items 解析')
    assert.match(
      src, /"action_items":\s*actionItems/,
      'POST /api/notes/{id}/summarize 的响应里没有 action_items 字段',
    )
  })

  it('解析失败时 summary 必须回落成模型原文（不能因格式抖动让总结消失）', () => {
    const src = readRepo('backend/internal/server/server_assistant.go')
    assert.match(
      src, /func parseNoteSummaryPayload\(content string\) \(string, \[\]noteActionItem\)/,
      'parseNoteSummaryPayload 签名不符：无法确认它是否返回 (summary, items) 两值',
    )
    const fn = src.slice(src.indexOf('func parseNoteSummaryPayload'))
    assert.match(
      fn.slice(0, 1200), /return content, \[\]noteActionItem\{\}/,
      'JSON 解析失败时必须把模型原文当 summary 返回（改动前的行为）',
    )
  })

  it('前端必须在拿到总结后调用 createNoteTodos', () => {
    const src = read('NoteListView.vue')
    assert.match(src, /from '\.\/note-todo-persist'/, 'NoteListView 未导入 note-todo-persist')
    assert.match(src, /action_items: actionItems/, '未从 summarize 响应里取 action_items')
    assert.match(src, /await createNoteTodos\(/, '拿到行动项后没有真的建待办')
  })

  it('createNoteTodos 必须写 local_todos 并执行计划里的提醒决定', () => {
    const src = read('note-todo-persist.ts')
    assert.match(src, /INSERT INTO local_todos/, '没有写入 local_todos')
    assert.match(src, /from '\.\/note-todo-plan\.ts'/, '未复用纯逻辑层 planNoteTodos')
    assert.match(src, /planNoteTodos\(/, '没有走 planNoteTodos（提醒判定会与纯逻辑脱节）')
    assert.match(src, /ensureTodoReminder\(/, '没有建计划日程提醒')
    // due_at 必须用计划里的解析结果。
    assert.match(src, /'pending', 'medium', plan\.dueAt/, 'due_at 未使用 plan.dueAt')
    // ★ 关键：提醒必须由 plan.remind 驱动。把 `if (plan.remind && ...)` 改成
    //   别的条件（或直接删掉分支）会让下面 B0 的行为断言转红 —— 而只靠
    //   「文件里有 ensureTodoReminder(」这句文本是抓不到的（实测：该变异全绿）。
    assert.match(
      src, /if \(plan\.remind && plan\.dueAt !== null\)/,
      '提醒分支必须由 plan.remind 驱动',
    )
  })

  it('纯逻辑层必须复用会议侧的期限解析（不另造口径）', () => {
    const src = read('note-todo-plan.ts')
    assert.match(src, /from '\.\.\/meetings\/meeting-due-plan\.ts'/, '未复用 resolveTodoDue')
    assert.match(src, /resolveTodoDue\(/, '纯逻辑层没有解析期限')
    // 会议侧的 source 联合类型必须含 note-voice，否则这里传不进去。
    assert.match(
      read('../meetings/meeting-due-plan.ts'), /'meeting-ingest' \| 'meeting-summary' \| 'note-voice'/,
      'ReminderInputArgs.source 缺 note-voice 变体',
    )
  })

  it('负控：不得回退到 Date.parse 解析笔记期限', () => {
    for (const f of ['note-todo-persist.ts', 'NoteListView.vue']) {
      const src = read(f)
      assert.ok(!/Date\.parse\(\s*due/.test(src), `${f} 仍用 Date.parse 解析 due`)
    }
  })

  it('笔记侧的 API 类型必须声明 action_items（否则前端拿不到）', () => {
    const src = read('../../api/notes.ts')
    assert.match(src, /action_items\?: NoteActionItem\[\]/, 'notesApi.summarize 的返回类型缺 action_items')
    assert.match(src, /export interface NoteActionItem/, 'NoteActionItem 未导出')
  })

  it('负控样本：接线层判据能抓住「没接线」的旧形态', () => {
    // 把 createNoteTodos 调用去掉，判据必须报错。
    const withCall = "const r = await notesApi.summarize(id)\nawait createNoteTodos(id, action_items)"
    const withoutCall = 'const r = await notesApi.summarize(id)'
    assert.match(withCall, /await createNoteTodos\(/)
    assert.ok(!/await createNoteTodos\(/.test(withoutCall), '负控：未接线的样本不该匹配')
  })
})
