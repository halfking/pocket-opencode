/**
 * 「消息」tab 两档看法（时间线 / 日历）的 URL 同步护栏。
 *
 * Run: node --test src/features/messages/hub-view.test.ts
 *
 * 重点是**往返**用例（切到日历再切回来）：本视图已经有来源 chips 与
 * `?source=` 深链，所以切档绝不能把 source 吃掉。写成 `{ view }` 而不是
 * `{ ...current, view }` 就能通过绝大多数正向用例，只有往返用例会红——
 * 而界面上那次退化完全看不出来：深链进来的用户回到时间线时筛选被静默重置。
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { DEFAULT_HUB_VIEW, hubViewQuery, parseHubView } from './hub-view.ts'

describe('parseHubView', () => {
  it('认得 calendar', () => {
    assert.equal(parseHubView('calendar'), 'calendar')
  })

  it('认得 timeline', () => {
    assert.equal(parseHubView('timeline'), 'timeline')
  })

  it('缺省回落时间线', () => {
    assert.equal(DEFAULT_HUB_VIEW, 'timeline')
    assert.equal(parseHubView(undefined), 'timeline')
    assert.equal(parseHubView(null), 'timeline')
    assert.equal(parseHubView(''), 'timeline')
  })

  it('无法解析的值一律回落，不抛错', () => {
    // 深链是人可编辑的、也可能被别处的代码拼错。这里抛异常等于让一个
    // 手输错的 URL 变成白屏，而回落只是「多显示一屏时间线」。
    assert.equal(parseHubView('Calendar'), 'timeline')
    assert.equal(parseHubView('cal'), 'timeline')
    assert.equal(parseHubView(42), 'timeline')
    assert.equal(parseHubView({}), 'timeline')
  })

  it('重复参数（数组值）判为不合法 → 回落', () => {
    // ?view=a&view=b 没有「取哪个」的正确答案。随便取一个会让 URL 与
    // 界面之间出现无法解释的偏差；回落则至少是自洽的默认态。
    assert.equal(parseHubView(['calendar']), 'timeline')
    assert.equal(parseHubView(['timeline', 'calendar']), 'timeline')
  })
})

describe('hubViewQuery', () => {
  it('切到日历：加上 view=calendar', () => {
    assert.deepEqual(hubViewQuery('calendar', {}), { view: 'calendar' })
  })

  it('切回时间线：把 view 删掉而不是写 view=timeline', () => {
    // URL 是这个 tab 的默认态。深链分享出去的应该是干净的 /messages，
    // 而不是带一个谁都不需要的 ?view=timeline。
    assert.deepEqual(hubViewQuery('timeline', { view: 'calendar' }), {})
  })

  it('往返不吃掉已有的 query 参数（深链来源筛选要保住）', () => {
    const deepLink = { source: 'meeting' }
    const onCalendar = hubViewQuery('calendar', deepLink)
    assert.deepEqual(onCalendar, { source: 'meeting', view: 'calendar' })
    assert.deepEqual(hubViewQuery('timeline', onCalendar), { source: 'meeting' })
  })

  it('已经是对的目标态时返回同一个对象（不触发多余导航）', () => {
    const alreadyCalendar = { view: 'calendar' } as const
    assert.equal(hubViewQuery('calendar', alreadyCalendar), alreadyCalendar)

    const alreadyTimeline = {} as const
    assert.equal(hubViewQuery('timeline', alreadyTimeline), alreadyTimeline)
  })

  it('不改传入的对象', () => {
    // 直接改 route.query 会让 vue-router 认为值变了，触发一次多余导航。
    const current = { source: 'email' }
    hubViewQuery('calendar', current)
    assert.deepEqual(current, { source: 'email' })

    const withView = { view: 'calendar' }
    hubViewQuery('timeline', withView)
    assert.deepEqual(withView, { view: 'calendar' })
  })
})
