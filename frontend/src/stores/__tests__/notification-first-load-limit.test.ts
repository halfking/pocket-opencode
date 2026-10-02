// notification-first-load-limit.test.ts — 通知中心「首次加载拉不满历史」这道缺口。
//
// ## 现象（2026-10-02 真实库推演）
//
// 后端 GET /api/notifications 只有 limit / unread / since 三个参数，
// **没有 offset 或游标**（backend/internal/server/server_notifycenter.go:38-52），
// 而 since 只能取**更新**的（filterNotificationsSince 过滤 created_at > since）。
// store 的增量逻辑又把 since 设成本地 inbox 的最大 created_at。
//
// 结论：客户端**没有任何办法把分页往回翻**。首次加载拿到的就是
// `ORDER BY created_at DESC LIMIT n` 的那 n 条；n 之外的更老通知，
// 在此后每一次增量里都不会被再请求一次 —— 永久不可见。
//
// 临界点：真实库当时 24 条通知（全部 email.important），limit=50 够用。
// 需求 4 的 90 天窗口首次上线会一次性推 32 条 → 总数 56 > 50，
// 于是最旧的 6 条对「首次加载发生在那之后」的客户端永久不可见。
//
// ## 为什么是源码判据而不是跑 store
//
// stores/notification.ts import '../api/notifications'（无扩展名），
// node 的 ESM 解析器解不了，整条依赖链拉不起来。stores/flashcards-sync-watermark.test.ts
// 正是因为这个才**复制**了一份判定逻辑——那是假测试，生产改动不会报警。
// 这里不复制，改用源码断言，并且**只断言那些复制不出来的东西**：
//「首次加载没有硬编码 50」和「前后端两个上限常量相等」。
//
// ## 负控
//
// 把 loadInbox 里的 `firstLoad ? NOTIFICATION_FIRST_LOAD_LIMIT : 50`
// 改回写死的 50 → 「首次加载不能用 50」转红；
// 把 NOTIFICATION_FIRST_LOAD_LIMIT 改成 200 以外的数 → 「两侧上限一致」转红。

import { strict as assert } from 'node:assert'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { test } from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const repo = join(here, '..', '..', '..')
// 本仓的 .ts 是 CRLF，归一化后再切边界，否则「找不到函数」会伪装成断言失败。
const read = (rel: string) => readFileSync(join(repo, rel), 'utf8').replace(/\r\n/g, '\n')

const store = read('src/stores/notification.ts')
const svc = read('../backend/internal/notifycenter/service.go')

test('首次加载不得硬编码 50', () => {
  // 承重断言：首次加载那条分支必须走 NOTIFICATION_FIRST_LOAD_LIMIT。
  // 只断言「文件里出现过 200」是不够的——增量那条分支本来就该是 50。
  assert.match(
    store,
    /firstLoad\s*\?\s*NOTIFICATION_FIRST_LOAD_LIMIT\s*:\s*50/,
    'loadInbox 的 limit 选择必须区分首次与增量；首次硬编码 50 会让超出的历史永久不可见',
  )
  // 首次加载的 limit 必须在 opts.limit 缺省时才生效，调用方显式传值仍优先。
  assert.match(
    store,
    /opts\.limit\s*\?\?\s*\(\s*firstLoad\s*\?/,
    'limit 必须允许调用方用 opts.limit 覆盖（视图可以自己决定拉多少）',
  )
})

test('首次加载必须带 since=0，不能复用增量水位线', () => {
  // 若首次也用 max(created_at)，而本地 inbox 残留了上次会话的数据，
  // 历史就会被当成增量跳过。
  assert.match(
    store,
    /firstLoad\s*\?\s*0\s*:\s*Math\.max\(/,
    '首次加载的 since 必须是 0；沿用水位线会让上次会话遗留的数据把历史整段跳过',
  )
})

test('前端首次加载上限与后端硬上限一致', () => {
  const fe = store.match(/NOTIFICATION_FIRST_LOAD_LIMIT\s*=\s*(\d+)/)
  assert.ok(fe, '找不到 NOTIFICATION_FIRST_LOAD_LIMIT 常量')
  const feLimit = Number(fe![1])

  // 后端：if limit <= 0 || limit > N { limit = 50 } —— N 才是真正的天花板。
  const be = svc.match(/limit\s*<=\s*0\s*\|\|\s*limit\s*>\s*(\d+)/)
  assert.ok(be, '后端 ListNotifications 的 limit 上限没找到，判据需要跟着它走')
  const beLimit = Number(be![1])

  assert.equal(
    feLimit,
    beLimit,
    `前端首次加载要 ${feLimit} 条，后端上限却是 ${beLimit} —— 超出部分会被静默压回 50，改了等于没改`,
  )
})
