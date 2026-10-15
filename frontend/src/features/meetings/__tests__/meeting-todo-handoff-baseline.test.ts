// meeting-todo-handoff-baseline.test.ts — §394：「转交到 ACC」把相对期限解析成哪一天？
//
// ★ 这道门钉的是**现状（调用时刻基准）**，不是「正确性」。
//
//   仓库里同一句 `draft.due` 被解析了两次，基准不同：
//     - 写入路径 `createMeetingTodos` → `resolveTodoDue(due, 总结时刻)`
//     - 转交路径 `accHandoffInput`    → `parseDueAt(due, 调用时刻)`
//   实测两者会漂：会议当天点一致，隔一天点晚一天，隔两天点晚两天（docs §394.2）。
//
//   **哪个基准对是产品口径决策，尚未拍板**（§394.4）。
//   ⇒ 所以本门不做「它对不对」的判断，只做两件不会随拍板结果作废的事：
//     1. 把基准做成**显式可见**的：结果必须跟随注入的 `now`，而不是真实时钟；
//     2. 证明本用例**真的能区分两个基准**（负控），否则第 1 条是恒真的。
//   ⇒ 拍板结果落地时，若把默认值从「调用时刻」改成「会议时刻」，
//     本门会**当场变红**，红即提醒「你改了口径，请同步改这里并更新 docs」。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { accHandoffInput } from '../meeting-todos.ts'
import { parseDueAt } from '../meeting-due.ts'

/** 会议发生时刻：2026-10-07 周三 15:00 本地。 */
const MEETING = new Date(2026, 9, 7, 15, 0, 0).getTime()
/** 用户两天后才打开这场会议点「ACC」。 */
const LATER = new Date(2026, 9, 9, 9, 0, 0).getTime()

const DRAFT = { text: '交周报', due: '明天下午三点' }

/** scheduleExpr 是 RFC3339；取它在设备当地的那一天（不依赖 UTC）。 */
function localDayOfExpr(expr: string): string {
  const d = new Date(expr)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/** 独立算出的期望值：直接用真 parseDueAt，不从 accHandoffInput 的输出反推。 */
function expectedDayAt(now: number): string {
  const parsed = parseDueAt(DRAFT.due, now)
  assert.ok(parsed, '前提不成立：解析器解不出「明天下午三点」')
  const d = new Date(parsed.at)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

describe('§394 · 转交路径的期限基准必须是**可注入的**，而不是隐式读真实时钟', () => {
  it('★ 结果必须跟随注入的 now（注入会议时刻 ⇒ 解出会议所说的那一天）', () => {
    const inp = accHandoffInput({ ...DRAFT }, '架构评审', MEETING)
    assert.equal(
      localDayOfExpr(inp.scheduleExpr), expectedDayAt(MEETING),
      `注入会议时刻却解出了别的日子：${inp.scheduleExpr}`,
    )
  })

  it('★★ 真实时钟被拨到两天后时，注入的 now 仍必须赢', () => {
    // 这条是本门的**牙齿**：`accHandoffInput` 若内部仍写 `Date.now()`，
    // 注入值就被忽略 ⇒ 这里立刻红。它证明 `now` 真的接上了解析器。
    const realNow = Date.now
    Date.now = () => LATER
    try {
      const inp = accHandoffInput({ ...DRAFT }, '架构评审', MEETING)
      assert.equal(
        localDayOfExpr(inp.scheduleExpr), expectedDayAt(MEETING),
        `真实时钟已是 ${new Date(LATER).toDateString()}，但注入的会议时刻被无视 ⇒ ` +
        `now 参数没有接上（实际解出 ${inp.scheduleExpr}）`,
      )
    } finally {
      Date.now = realNow
    }
  })

  it('★★ 负控：两个基准在本用例上必须落在**不同的日子**', () => {
    // 若两者恰好同天，上面两条断言无论实现怎么改都成立 ⇒ 它们是恒真的。
    assert.notEqual(
      expectedDayAt(MEETING), expectedDayAt(LATER),
      '负控前提失败：会议时刻与两天后解析出了同一天，本门区分不出基准',
    )
    assert.equal(
      localDayOfExpr(accHandoffInput({ ...DRAFT }, '架构评审', LATER).scheduleExpr),
      expectedDayAt(LATER),
      '换个 now 就该换一天：基准没有真的跟着 now 走',
    )
  })

  it('无期限时退回「调用时刻 + 60 秒」，且同样跟随注入的 now', () => {
    const inp = accHandoffInput({ text: '没有期限的活' }, '架构评审', MEETING)
    assert.equal(
      new Date(inp.scheduleExpr).getTime(), MEETING + 60_000,
      `无期限时退回值没有跟随注入的 now（${inp.scheduleExpr}）`,
    )
  })

  it('转交任务必须带上原始期限表述，便于回溯是哪句话解析出来的', () => {
    const inp = accHandoffInput({ ...DRAFT }, '架构评审', MEETING)
    assert.match(inp.prompt ?? inp.payload.prompt, /明天下午三点/,
      '转交任务的 prompt 里没有原始期限 ⇒ 事后无法核对解析依据')
  })
})