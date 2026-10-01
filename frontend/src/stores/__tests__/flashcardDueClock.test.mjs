/**
 * BUG-AS 回归测试：到期判据必须是**响应式**的（handoff §4.55）。
 *
 * 缺陷：dueByDeck / deckSummaries / dueCardsForDeck 三个 computed 原本读的是
 * `Date.now()`。Vue 的 computed 只在响应式依赖变化时重算，而 `Date.now()`
 * 不是任何 ref ⇒ 卡片在页面打开期间跨过到期时刻，computed 不会重算，
 * 到期数不更新、「开始复习」一直置灰。
 *
 * 本测试的判据不是「函数返回了一个数」，而是**依赖它的 computed 会不会重算**。
 * 为此同一文件里写了两条：
 *   - 正例：用 `dueNowSec()`（响应式）→ 推进时间后 computed 必须给出新结果
 *   - 负例：用 `liveNowSec()`（等价于修复前的 Date.now()）→ 推进时间后 computed
 *           **必须仍是旧结果**
 * 负例是这张测试自带的负控：如果哪天 dueNowSec 又变回直接读 Date.now()，
 * 正例会红；如果负例也变绿了，说明这条断言已经失去鉴别力（测试坏了），同样要红。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { computed, ref } from 'vue'

import {
  dueNowSec,
  liveNowSec,
  startDueClock,
  stopDueClock,
  TICK_MS,
  __setDueNowSecForTest,
  __tickDueClockForTest,
} from '../flashcardDueClock.ts'

/** 与 stores/flashcards.ts 的 dueByDeck 同形的最小复刻。 */
function makeDueCountModel(readNow) {
  const cards = ref([
    { id: 'c1', deckId: 'd1', state: 0, due: 1000, deletedAt: 0 }, // learning，未到期
  ])
  const dueByDeck = computed(() => {
    const now = readNow()
    const map = new Map()
    for (const card of cards.value) {
      if (card.deletedAt && card.deletedAt > 0) continue
      const isLearning = card.state === 0 || card.state === 1 || card.state === 3
      const isDue = isLearning ? card.due <= now : true
      if (!isDue) continue
      map.set(card.deckId, (map.get(card.deckId) ?? 0) + 1)
    }
    return map
  })
  return { cards, dueByDeck }
}

test('BUG-AS 正例：读 dueNowSec() 的 computed 会随 tick 推进重算', () => {
  __setDueNowSecForTest(500) // 早于 card.due=1000 → 未到期
  const { dueByDeck } = makeDueCountModel(() => dueNowSec())
  assert.equal(dueByDeck.value.get('d1'), undefined, '起点：未到期不该计数')

  __setDueNowSecForTest(1500) // 越过 due=1000
  assert.equal(
    dueByDeck.value.get('d1'),
    1,
    'BUG-AS：时间推进到卡片到期之后，到期数必须变成 1（修复前这里恒为 undefined）',
  )

  // 退回未到期，必须再变回去——证明不是「一次性重算后缓存死值」
  __setDueNowSecForTest(500)
  assert.equal(dueByDeck.value.get('d1'), undefined, '时间回退后必须重新变回未到期')
})

test('BUG-AS 负例：读 liveNowSec()（修复前的写法）不会因时间推进重算', () => {
  const real = Date.now
  try {
    Date.now = () => 500 * 1000
    const { dueByDeck } = makeDueCountModel(() => liveNowSec())
    assert.equal(dueByDeck.value.get('d1'), undefined, '起点：未到期')

    Date.now = () => 1500 * 1000 // 越过 due
    assert.equal(
      dueByDeck.value.get('d1'),
      undefined,
      '负控：这条必须仍然是 undefined —— 它复刻的就是修复前的缺陷。' +
        '若它也变成 1，说明 Vue 对非响应式时间做了本不该有的追踪，' +
        '本测试的判据就失去了鉴别力，必须先查清再改。',
    )
  } finally {
    Date.now = real
  }
})

test('tick 真的会把响应式时间推进（否则正例只是被手动拨动的假象）', () => {
  const real = Date.now
  try {
    Date.now = () => 500 * 1000
    __tickDueClockForTest()
    const afterFirst = dueNowSec()
    assert.equal(afterFirst, 500, 'tick 后 ref 必须等于当时的真实秒级时间')

    Date.now = () => 1500 * 1000
    __tickDueClockForTest()
    assert.equal(dueNowSec(), 1500, '时间往前走后再 tick，ref 必须跟着走')
    assert.ok(dueNowSec() > afterFirst, 'ref 必须是递增推进，不是固定值')
  } finally {
    Date.now = real
  }
})

test('startDueClock 真的挂了定时器，且重复启动不会留下多个', () => {
  // 只断言「没抛错」是没有鉴别力的：这里替换掉 setInterval，
  // 直接数「到底注册了几个定时器、间隔是多少」。
  const realSetInterval = globalThis.setInterval
  const realClearInterval = globalThis.clearInterval
  const registered = []
  const cleared = []
  globalThis.setInterval = (fn, ms) => {
    registered.push({ fn, ms })
    return { fake: true, id: registered.length }
  }
  globalThis.clearInterval = (h) => cleared.push(h)
  try {
    startDueClock()
    startDueClock()
    startDueClock()
    assert.equal(registered.length, 1, `重复启动 3 次只应注册 1 个定时器，实际 ${registered.length} 个`)
    assert.equal(registered[0].ms, TICK_MS, `定时器间隔必须是 TICK_MS=${TICK_MS}，实际 ${registered[0].ms}`)

    stopDueClock()
    assert.equal(cleared.length, 1, '停掉时必须清掉那一个定时器')
    // 幂等：再停一次不应重复 clear
    stopDueClock()
    assert.equal(cleared.length, 1, `重复 stop 不应重复清理，实际 clear 了 ${cleared.length} 次`)

    // 停掉之后还能再启动（幂等不等于「一次性」）
    startDueClock()
    assert.equal(registered.length, 2, 'stop 之后必须能重新启动，否则热重载/测试会把它永久停掉')
  } finally {
    globalThis.setInterval = realSetInterval
    globalThis.clearInterval = realClearInterval
    stopDueClock()
  }
})

test('页面不可见时不 tick，回前台立刻补一次（后台暂停策略真的生效）', () => {
  const realDoc = globalThis.document
  const realSetInterval = globalThis.setInterval
  const realClearInterval = globalThis.clearInterval
  let intervalCb = null
  let visibilityListener = null
  // 真实定时器要等 30s 才有意义，测试里换成手动触发——但必须**真的调它**，
  // 否则「后台不推进」只是断言了一个没被碰过的基线，是空断言。
  globalThis.setInterval = (fn) => {
    intervalCb = fn
    return { fn }
  }
  globalThis.clearInterval = () => {}
  globalThis.document = {
    hidden: true,
    addEventListener: (type, fn) => { if (type === 'visibilitychange') visibilityListener = fn },
    removeEventListener: () => { visibilityListener = null },
  }
  try {
    const real = Date.now
    try {
      __setDueNowSecForTest(500) // 显式建立基线，不依赖上一个用例留下的值
      Date.now = () => 500 * 1000
      startDueClock()
      assert.ok(visibilityListener, 'startDueClock 必须注册 visibilitychange 监听，否则回前台不会补 tick')
      assert.ok(intervalCb, 'startDueClock 必须挂上定时器')

      // 后台：时间已经走到 1500，但定时器周期到了也不能推进
      Date.now = () => 1500 * 1000
      intervalCb()
      assert.equal(dueNowSec(), 500, '后台：定时器触发也不得推进到期时间（省电策略）')

      // 回前台：立刻补一次 tick，不用等下一个 30s 周期
      globalThis.document.hidden = false
      visibilityListener()
      assert.equal(dueNowSec(), 1500, '回到前台必须立刻补一次 tick，否则界面会显示过期的到期数')
    } finally {
      Date.now = real
    }
  } finally {
    globalThis.setInterval = realSetInterval
    globalThis.clearInterval = realClearInterval
    stopDueClock()
    if (realDoc === undefined) delete globalThis.document
    else globalThis.document = realDoc
  }
})
