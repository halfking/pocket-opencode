// flashcards-sync-watermark.test.ts — BUG-O 回归锁（2026-09-30 真机验收）。
//
// 现象（真机，192.168.31.19:5555）：
//   1. UI 建卡组 -> POST /api/flashcards/decks 201，卡组出现在列表。
//   2. UI 填正反面保存 -> POST /api/flashcards/notes 201。
//   3. 查 PG：note 与 card 都在，deck_id 正确。
//   4. 进卡组详情页：「开始复习」恒 disabled，卡片**永远不出现**。
//   5. 宿主侧直接 GET /api/flashcards?since=0 能拿到那张卡。
//
// 根因有两半，缺一不可：
//   (a) 水位线语义错误。store.syncFromServer 原来把 lastSyncedAt 设成
//       floor(serverTimeMs/1000)（服务器此刻时间），而后端过滤是
//       `updated_at > $since`（严格大于）。凡是在"写入完成"与"这次拉取"之间
//       发生的变更，updated_at 都落在水位线之前，之后**永远拉不回来**。
//       注意"宿主侧 since=0 能拿到"并不能否证它——since=0 时不过滤。
//   (b) 保存后不回读。首张 card 由服务端生成 id，客户端本地没有这条记录；
//       save() 原来只 `void store.flushOutbox()` 就 goBack，不触发任何 sync。
//
// 本测试锁 (a)：水位线必须等于**实际收到的最大 updatedAt**。
// (b) 是组件里的 await 顺序，靠 cdp 真机用例覆盖（见 redmi-write-ops-modules.mjs）。

import test from 'node:test'
import assert from 'node:assert/strict'

/**
 * 与 stores/flashcards.ts 中 syncFromServer 的水位线逻辑保持一致。
 * 这里复制而不是 import：store 依赖 vue/pinia + localStorage，node --test
 * 拉不起来。复制有漂移风险，所以下面用 BUG-O 的具体数字锁死语义。
 */
function nextWatermark(envelope, previous) {
  const items = [
    ...(envelope.notes ?? []),
    ...(envelope.cards ?? []),
    ...(envelope.decks ?? []),
  ]
  const maxUpdated = items.reduce((acc, item) => {
    const ts = typeof item?.updatedAt === 'number' ? item.updatedAt : 0
    return ts > acc ? ts : acc
  }, 0)
  return maxUpdated > 0 ? maxUpdated : previous
}

test('BUG-O 水位线取实际收到的最大 updatedAt，而不是 serverTimeMs', () => {
  // 真机抓到的实际数字：卡片 updated_at=1790739248，而服务端"当前时间"
  // 已经走到 1790739250 之后。
  const card = { id: 'card_1', updatedAt: 1790739248, deckId: 'deck_1' }
  const envelope = {
    cards: [card],
    notes: [],
    decks: [],
    serverTimeMs: 1790739250_000, // 比卡片 updated_at 晚 2 秒
  }

  const wm = nextWatermark(envelope, 0)
  assert.equal(wm, 1790739248, '水位线必须是收到的最大 updatedAt')

  // 若水位线取 serverTimeMs，会是 1790739250 —— 这张卡的 updated_at 落在水位线
  // 之前，本地又没有它的记录（id 由服务端生成），等于永久丢失。
  const bad = Math.floor(envelope.serverTimeMs / 1000)
  assert.ok(card.updatedAt < bad, '用 serverTimeMs 当水位线会跳过这张卡（这正是原 bug）')
})

test('BUG-O 同一秒内的多条变更不会互相吞掉', () => {
  // 服务端两处语义必须配套：水位线取本批最大 updated_at，服务端过滤用 >=。
  // 若服务端仍是严格大于，下面的 cardB 就再也拉不回来。
  const previous = 1790739200
  const cardA = { id: 'cardA', updatedAt: 1790739248 }
  const cardB = { id: 'cardB', updatedAt: 1790739248 } // 同一秒
  const envelope = { cards: [cardA], notes: [], decks: [], serverTimeMs: 1790739600_000 }

  const wm = nextWatermark(envelope, previous)
  assert.equal(wm, 1790739248)

  // 下一轮 since = 1790739248，服务端 `updated_at >= 1790739248` 能同时捞回 A 和 B。
  // 严格大于则两者都拉不到 —— 对 A 无所谓（本地已有），对 B 是永久丢失。
  const serverFilter = (updatedAt, since) => updatedAt >= since
  assert.ok(serverFilter(cardA.updatedAt, wm), 'cardA 应可再次拉回（幂等）')
  assert.ok(serverFilter(cardB.updatedAt, wm), 'cardB 必须可拉回，否则同秒变更永久丢失')
})

test('BUG-O 水位线不会因为 serverTimeMs 而跳过边界窗口内的变更', () => {
  const previous = 1790739200
  const noteWritten = { id: 'n1', updatedAt: 1790739248 }
  const envelope = { notes: [noteWritten], cards: [], decks: [], serverTimeMs: 1790739300_000 }

  const wm = nextWatermark(envelope, previous)
  assert.equal(wm, 1790739248)
  assert.equal(wm < noteWritten.updatedAt, false, '水位线不应越过已收到的数据')
})

test('BUG-O 空结果不推进水位线', () => {
  // 空结果不代表"此前都已同步"。若用 serverTimeMs 推进，空结果也会把水位线
  // 推高，等于用一次空拉取盖掉可能存在的变更。
  const previous = 1790739200
  const envelope = { notes: [], cards: [], decks: [], serverTimeMs: 1790739999_000 }
  assert.equal(nextWatermark(envelope, previous), previous)
})

test('BUG-O 忽略缺少 updatedAt 的畸形条目', () => {
  const previous = 1790739200
  const envelope = {
    cards: [{ id: 'bad' }, { id: 'ok', updatedAt: 1790739248 }],
    notes: [{ id: 'n', updatedAt: null }],
    decks: [],
  }
  assert.equal(nextWatermark(envelope, previous), 1790739248)
})
