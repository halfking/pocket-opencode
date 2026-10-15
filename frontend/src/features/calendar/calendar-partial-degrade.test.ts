// calendar-partial-degrade.test.ts — §118：日历 feed 的**部分降级信号**必须活到界面。
//
// ── 缺口的事实链 ───────────────────────────────────────────────────
//
// BuildFeed 合并三路来源：日程事件 / 任务截止 / 定时任务。
// 任一路查询失败时它返回 200 + **已取到的那部分**（这样单个坏查询
// 不会让整个日历白屏），把错误一并返回。
//
// handleCalendarEvents 此前只 `log.Printf`。
// 而 feed.go 的注释写着：「The client degrades, but the operator sees why」
// —— **那句话是假的**：客户端拿到 200 + 少了几条，零信号。
//
// 后果落在用户需求的那条链上：「将一些时间点自动加入日程」——
// 时间点来自任务截止。那一路查询一坏，用户的时间点就从日历上消失，
// 而界面和「我确实没有安排」长得一模一样。
// 这不是理论风险：同一段注释记着「tasks has no timezone 列」就是这么上线过一次的。
//
// ── 判据为什么落在 unwrapFeed ───────────────────────────────────────
//
// feed() 自带 I/O，而「partial 有没有活过解包」是纯数据变换：
// 源码扫描式判据量不到它（把 `=== true` 改成 `Boolean(x)` 照样绿）。
// 与 api/meetings.ts 的 normalizeSummary 同一理由，抽成纯函数即可直接断言。
//
// ★ store 那一跳不靠本门：漏写 `.entries` 会被 vue-tsc 抓到
//   （feed 返回 {entries, partial}，赋给 CalendarEntry[] 直接类型错）。
//   ⇒ 「信号在解包丢了」与「信号在 store 丢了」由两把不同的尺子量，各管一段。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { join } from 'node:path'
import { loadBundle, FRONTEND_ROOT } from '../../api/__tests__/_wire-helpers.mjs'

const loadApi = () =>
  loadBundle(join(FRONTEND_ROOT, 'src/features/calendar/api.ts'), 'calendar-partial')

const entry = { id: 'event:1', source: 'event', title: '周会' }

describe('§118 日历 feed 部分降级信号', () => {
  it('服务端标了 partial=true 时必须透出', async () => {
    const { unwrapFeed } = await loadApi()
    const out = unwrapFeed({ entries: [entry], from: 1, to: 2, serverTimeMs: 3, partial: true })
    assert.equal(out.partial, true, '降级信号在解包这一跳被丢了')
    // 数据仍要带出来：修的是「告诉用户」，不是「把数据也藏起来」。
    assert.equal(out.entries.length, 1)
  })

  it('健康时 partial=false —— 不得无根据地谎报降级', async () => {
    const { unwrapFeed } = await loadApi()
    assert.equal(unwrapFeed({ entries: [entry], partial: false }).partial, false)
  })

  it('老服务端没有这个键时按 false 处理，不得当成降级', async () => {
    const { unwrapFeed } = await loadApi()
    // 老服务端根本不会部分降级（它只写日志），所以「没这个键」= 没事。
    assert.equal(unwrapFeed({ entries: [entry], from: 1, to: 2 }).partial, false)
  })

  it('★ 严格 === true：字符串 "true" 与真值对象都不算降级', async () => {
    const { unwrapFeed } = await loadApi()
    // 这是最容易写松的一处：`if (body.partial)` 会让字符串 "true"
    // 也变成「降级」⇒ 用户被无根据地告知数据不全。
    // 宽松判与漏判的代价方向相反，但**都会**误导用户，所以钉死。
    assert.equal(unwrapFeed({ entries: [], partial: 'true' }).partial, false)
    assert.equal(unwrapFeed({ entries: [], partial: 1 }).partial, false)
    assert.equal(unwrapFeed({ entries: [], partial: {} }).partial, false)
    assert.equal(unwrapFeed({ entries: [], partial: null }).partial, false)
  })

  it('裸数组（无信封）时 entries 照吃、partial 为 false', async () => {
    const { unwrapFeed } = await loadApi()
    const out = unwrapFeed([entry])
    assert.equal(out.entries.length, 1)
    assert.equal(out.partial, false)
  })

  it('entries 缺失或形状不对时安全退回空数组（既有行为不得回归）', async () => {
    const { unwrapFeed } = await loadApi()
    assert.deepEqual(unwrapFeed({}).entries, [])
    assert.deepEqual(unwrapFeed({ entries: null }).entries, [])
    assert.deepEqual(unwrapFeed(null).entries, [])
    assert.deepEqual(unwrapFeed(undefined).entries, [])
    // ⚠ null 条目的 partial 也不能抛：`(null as any)?.partial` 是 undefined。
    assert.equal(unwrapFeed(null).partial, false)
  })

  it('降级与降级同时存在时，两者互不吞掉', async () => {
    const { unwrapFeed } = await loadApi()
    const out = unwrapFeed({
      entries: [entry, { id: 'task:9', source: 'task', title: '交周报' }],
      partial: true,
    })
    assert.equal(out.partial, true)
    assert.equal(out.entries.length, 2, '已取到的数据不得因为要提示就丢掉')
  })
})
