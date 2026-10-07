// meeting-due-plan.test.ts — 门禁：「会议时间点必须真的进计划日程」。
//
// 这道门守的是一个曾经**整条链路静默失效**的缺口：
//   LLM 摘要给出 due=「明天下午三点」→ 两处 parseDue 只做 Date.parse
//   → 中文判成 NaN → local_todos.due_at = null（用户看不到期限）
//   → 计划日程里没有任何时间点（需求原文「将一些时间点自动加入计划日程」
//     完全没有落点）。
//
// 本门分三层，**每层都带负控**——只写「全绿」的断言等于没写：
//   A. 行为层：中文 due 必须解析出时刻；提醒 expr 必须带真实 due 而非 now+60s。
//   B. 接线层：两条入口链路（会中总结 / 录后精校）都必须真的调用
//      resolveTodoDue + ensureTodoReminder，且**不得**再出现 Date.parse(due)。
//      ——这一层是防回退的，代码被改回旧写法就会红。
//   C. 负控层：证明 A/B 的判据不是恒真（把输入换成「解不出的期限」时
//      必须转绿为 null / 不建提醒）。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { buildReminderInput, resolveTodoDue } from './meeting-due-plan.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const read = (f: string) => readFileSync(join(HERE, f), 'utf8')

// 2026-10-07 是周三 15:04（本地）。
const NOW = new Date(2026, 9, 7, 15, 4).getTime()
const MINUTE = 60 * 1000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

describe('A · 行为层：中文期限不再是静默 null', () => {
  it('中文 due 必须解析出时刻（旧 Date.parse 口径下这里是 null）', () => {
    const cases = [
      '明天下午三点',
      '今天晚上八点半',
      '周五上午10点',
      '下周一',
      '12月20日',
      '3天后',
    ]
    for (const due of cases) {
      const parsed = resolveTodoDue(due, NOW)
      assert.ok(parsed, `「${due}」必须能解析出时刻（这正是旧链路丢失的信息）`)
      assert.ok(Number.isFinite(parsed.at), `「${due}」的时刻必须是有限 epoch`)
      assert.ok(parsed.at > NOW, `「${due}」应指向未来，实际 ${new Date(parsed.at).toString()}`)
    }
  })

  it('负控：解不出的期限仍返回 null（不能为了「全有」而瞎猜时间）', () => {
    for (const due of ['', '   ', '尽快', '下次一定', '有空的时候']) {
      assert.equal(resolveTodoDue(due, NOW), null, `「${due}」不该被硬解出时间`)
    }
    assert.equal(resolveTodoDue(undefined, NOW), null)
    assert.equal(resolveTodoDue(null, NOW), null)
  })

  it('负控对照：Date.parse 对同一个中文 due 确实给不出时间（证明本门在守真问题）', () => {
    // 旧实现的全部逻辑就是这一行。这条断言把「旧行为」钉成事实，
    // 免得日后有人以为「Date.parse 本来就能解析中文」而直接删掉整条链路。
    assert.ok(Number.isNaN(Date.parse('明天下午三点')), 'Date.parse 不认中文，这条前提不能变')
  })
})

describe('A · 行为层：提醒任务带的是真实 due，不是「现在」', () => {
  it('scheduleExpr 里的时刻 = 解析出的 due 时刻', () => {
    const due = '明天下午三点'
    const parsed = resolveTodoDue(due, NOW)!
    const input = buildReminderInput({
      text: '发周报', dueText: due, assignee: '张三', meetingTitle: '周会', at: parsed.at, source: 'meeting-summary',
    })
    assert.equal(input.scheduleKind, 'at')
    // 关键判据：expr 解析出的时刻与 due 解析出的时刻**一致**（精确到分钟）。
    // 旧实现是 new Date(Date.now()+60_000).toISOString()，与 due 无关。
    assert.equal(
      Math.floor(Date.parse(input.scheduleExpr) / MINUTE),
      Math.floor(parsed.at / MINUTE),
      '提醒 expr 必须是 due 解析出的时刻',
    )
    assert.ok(parsed.at - NOW > 12 * HOUR, '「明天下午三点」应距 now 半天以上，便于断言不是 now+60s')
  })

  it('负控：expr 与 now 的距离必须显著大于 60s（钉住「回退到 now+60s」这个旧 bug）', () => {
    const parsed = resolveTodoDue('周五上午10点', NOW)!
    const input = buildReminderInput({ text: '对齐', dueText: '周五上午10点', at: parsed.at, source: 'meeting-ingest' })
    const delta = Math.abs(Date.parse(input.scheduleExpr) - NOW)
    assert.ok(delta > 60 * MINUTE, `expr 距 now 应以小时计，实际只有 ${(delta / MINUTE).toFixed(1)} 分钟`)
    // 负控：旧实现的值必须**不**满足上面这条 —— 证明判据有牙。
    const legacyExpr = new Date(NOW + 60_000).toISOString()
    assert.equal(
      Math.abs(Date.parse(legacyExpr) - NOW) > 60 * MINUTE,
      false,
      '负控：旧实现（now+60s）必须被上面那条判据判红',
    )
  })

  it('提醒入参带上待办原文/负责人/来源，供提醒时复述', () => {
    const parsed = resolveTodoDue('明天', NOW)!
    const input = buildReminderInput({
      text: '补测试用例', dueText: '明天', assignee: '李四', meetingTitle: '架构评审',
      at: parsed.at, source: 'meeting-ingest',
    })
    const payload = input.payload as { prompt: string; todoText: string; source: string }
    assert.equal(input.kind, 'llmbff_summary')
    assert.equal(input.timezone, 'Asia/Shanghai', '时区口径须与 schedule-plan.DEFAULT_TZ 一致')
    assert.equal(input.maxRuns, 1)
    assert.match(payload.prompt, /补测试用例/)
    assert.match(payload.prompt, /李四/)
    assert.match(payload.prompt, /架构评审/)
    assert.equal(payload.source, 'meeting-ingest')
  })
})

describe('B · 接线层：两条入口链路都真的接上了（防回退）', () => {
  const WIRING = [
    { file: 'meeting-todo-persist.ts', path: '会中总结 → createMeetingTodos' },
    { file: 'meeting-ingest.ts', path: '录后精校 → createLocalTodos' },
  ]

  for (const { file, path } of WIRING) {
    it(`${file}（${path}）必须走 resolveTodoDue + ensureTodoReminder`, () => {
      const src = read(file)
      assert.match(src, /from '\.\/meeting-due-reminder'/, `${file} 未导入 meeting-due-reminder`)
      assert.match(src, /resolveTodoDue\(/, `${file} 没有调用 resolveTodoDue（期限仍会静默丢失）`)
      assert.match(src, /ensureTodoReminder\(/, `${file} 没有建提醒（时间点进不了计划日程）`)
      // due_at 落库必须用解析结果，不能再是裸 parseDue(due)。
      assert.match(src, /dueAt \? dueAt\.at : null/, `${file} 的 due_at 未使用解析结果`)
    })

    it(`${file} 不得回退到 Date.parse 解析期限（负控：旧写法必须被判红）`, () => {
      const src = read(file)
      assert.ok(
        !/Date\.parse\(\s*due/.test(src),
        `${file} 仍用 Date.parse 解析 due —— 中文期限会重新变成 null`,
      )
      assert.ok(
        !/function parseDue\b/.test(src),
        `${file} 仍保留本地 parseDue —— 说明某条路径没走统一口径`,
      )
      // 负控：把旧写法塞进同一段文本，判据必须报错（而不是恒真）。
      const legacy = 'function parseDue(due) { const t = Date.parse(due); return isNaN(t) ? null : t }'
      assert.ok(/Date\.parse\(\s*due/.test(legacy), '负控：旧写法样本应被上面的正则捕获')
    })
  }

  it('accHandoffInput 的「转交」也必须用真实 due（负控：旧值恒为 now+60s）', () => {
    const src = read('meeting-todos.ts')
    assert.match(src, /parseDueAt\(/, 'meeting-todos.ts 未接入 parseDueAt')
    // 旧写法：new Date(Date.now() + 60_000).toISOString() 恒定出现。
    assert.ok(
      !/new Date\(Date\.now\(\) \+ 60_000\)\.toISOString\(\)\s*[,}]/.test(src),
      'accHandoffInput 仍把 scheduleExpr 写死成 now+60s',
    )
    assert.ok(
      /scheduleExpr = dueAtToScheduleExpr\(parsed\.at\)/.test(src),
      'accHandoffInput 未用 dueAtToScheduleExpr 覆盖 scheduleExpr',
    )
  })
})
