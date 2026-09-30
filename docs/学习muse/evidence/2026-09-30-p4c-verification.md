# P4c 验证证据（2026-09-30，连续学习天数 streak + 里程碑）

**范围**：路线图 P4 第三项 —— 连续学习天数与里程碑播报。
只记录**实际执行并看到输出**的验证；未做的一律标「未验证」。

承接 [2026-09-30-p4-verification.md](2026-09-30-p4-verification.md)。

---

## 1. 改动清单

| 文件 | 性质 | 说明 |
|---|---|---|
| `backend/internal/learning/streak.go` | 新增 | 纯函数 `DayIndex` / `ComputeStreak` / `Milestone` / `NextMilestone` / `MilestoneKey` |
| `backend/internal/learning/streak_test.go` | 新增 | 15 个用例 |
| `backend/internal/learning/store.go` | 改动 | `ActiveDayTimestamps` + `ClaimMilestone` |
| `backend/internal/learning/service.go` | 改动 | `Streak` / `ClaimMilestoneAnnouncement` + `StreakView` |
| `backend/internal/server/learning_handler.go` | 改动 | `GET /api/learning/streak?tz_offset=` |
| `backend/internal/scheduledtask/executors/learning_digest.go` | 改动 | 每日回顾里播报里程碑（exactly-once） |
| `backend/internal/scheduledtask/executors/learning_digest_test.go` | 新增 | **12 个用例——这个执行器此前零测试** |
| `frontend/src/types/learning.ts` | 改动 | `LearningStreak` / `LearningStreakView` |
| `frontend/src/services/learning.ts` | 改动 | `fetchStreak` + `localUtcOffsetSeconds` |
| `frontend/src/features/study/StudyHubView.vue` | 改动 | Hero 下的连续天数徽标 |
| `frontend/scripts/add-streak-locale.mjs` | 新增 | 9 语言 ×3 键（en-US 356 → **359**） |
| `frontend/src/assets/fonts/...woff2` | 改动 | 新增 `local_fire_department`，108 → **109** |

## 2. 验证结果

| 命令 | 结果 |
|---|---|
| `go build ./...` | ✅ 退出码 0 |
| `go test ./internal/learning/ -run "Streak\|DayIndex\|Milestone"` | ✅ **15/15** |
| `go test ./internal/scheduledtask/executors/ -run "LearningDigest"` | ✅ **12/12** |
| `go test ./...`（全量） | ✅ 46 包通过；`internal/agent` / `internal/email` 18 条失败 —— 与基线同名同数 |
| `npx.cmd vue-tsc --noEmit` | ✅ 退出码 0 |
| `node scripts/build-gate.mjs` | ✅ `✓ built in 17.05s` |
| `node --test src/native/__tests__/*.test.mjs` | ✅ 101 / 101 / 0 fail |
| `node scripts/check-viewmodel-gaps.mjs` | ✅ 命中 0 = 阈值 |
| `node scripts/report-locale-gaps.mjs` | ✅ 缺 0 / 多 0；en-US **359 key** |
| `node scripts/verify-i18n.js` | ✅ 退出码 0 |

---

## 3. 一个必须写清楚的规则：连续天数何时才算断

`ComputeStreak` 里最容易写错、也最影响体验的一条：

> **连续记录要活过「最后一次学习日的次日」为止。**
> 昨晚学了、今早还没打开 App，连续天数**仍然是完整的**。

朴素实现会在午夜清零，于是每天早上都给一个昨晚刚学过的人显示 0——而那恰恰是连续记录
最该发力的时刻。这条规则有独立用例 `TestComputeStreakSurvivesUntilTheDayAfter`。

另外两条已用回归用例钉住：

- `TestComputeStreakOldRunIsNotTheCurrentStreak`：活动日 {95,96,97}、今天 100 时
  当前连续必须是 **0** 而不是 3。**第一版实现就是错的**——它从最后一次活动日**往上**数，
  会把旧的一段数满并且永远碰不到 98/99 这两个缺口。
- `TestComputeStreakIgnoresFutureActivity`：晚于 `today` 的时间戳（客户端时钟不准、
  某个节点跑太快）会被丢弃，否则未来的一天能满足「今天已学习」并虚增连续。

## 4. 设计判断

### 4.1 streak 是派生值，不是计数器

不落库、每次从活动日期重算。计数器在崩溃、时区变更、漏 tick 之后都需要修复；
重算是自愈的。代价是每次读要扫一次 `learning_items`——用 730 天的窗口（远超最长的
365 天里程碑）把它限制住。

### 4.2 里程碑去重复用了已有表，**没有新增 DDL**

`learning_reminders` 已有唯一索引 `(workspace_id, user_id, kind, item_id)`。
把「里程碑 N 已播报」记成一行 `kind='streak'`、`item_id='milestone:N'` 的提醒，
这个索引就自动给出「每用户每里程碑恰好一次」——**由 PostgreSQL 保证**，
而不是执行器里一句 check-then-insert 的竞态。

`ReminderStreak` 这个 kind 常量本来就在枚举里（v1 契约遗留），本轮才真正用上。

这个选择是刻意的：在「所有新 DDL 都还没在真库验过一次」的背景下，
再叠一张新表只会让未验证的面变大。

### 4.3 先 claim 再推送

顺序是刻意的：推送失败只是**错过一次祝贺**，而不是**每天早上重复祝贺直到用户静音**。
前者代价小得多。

### 4.4 界面区分「0 天」和「查不到」

`fetchStreak` 失败时 `streak` 保持 `null`，整块**不渲染**——而不是显示 0 天。
「你没有连续记录」和「我们查不到」在屏幕上必须是两件事。

---

## 5. 顺带发现并修掉的缺口

`LearningDigestExecutor` **此前完全没有测试**（`go test -run LearningDigest` 返回
`[no tests to run]`）。P0 交付时它只被 build 和路由层覆盖过。

也就是说：这一轮我给一个**零覆盖**的执行器加了里程碑分支。现补 12 个用例，覆盖
「四项全 0 就不推送」「里程碑只播一次」「streak 读失败不能拖垮 digest」
「claim 失败只记日志」「nil notifier 不 panic」等。
`TestLearningDigestMilestoneToleratesNilNotifier` 顺带钉住了一个细节：
没有 notifier 时**连 claim 都不该做**——否则 notifycenter 稍后就绪后，
这一次里程碑就被白白消耗掉了。

---

## 6. 测试抓到的错误

| # | 错误 | 性质 |
|---|---|---|
| 1 | `ComputeStreak` 从最后活动日往上数 → 旧 run 被当成当前 run | **真 bug**（实现） |
| 2 | 走查下界写成 `LastActiveDay` → 多天连续只算 1 天 | **真 bug**（实现） |
| 3 | 未过滤晚于 `today` 的时间戳 | **真 bug**（实现） |
| 4 | `TestComputeStreakGapsBreakRuns` 期望 3（实际 1），注释里自己都写反了 | 测试写错 |
| 5 | `DayIndex(86340, +8h)` 期望 0 | 测试写错：23:59 UTC 在东八区是**次日** 07:59 |
| 6 | `int64` / `int` 混用 | 编译器抓到的类型错 |

第 1、2 条是我写实现时的真实错误，由测试抓出来。这类纯函数**只要能被测就一定该测**——
它们是这个功能里唯一有真正算法难度的部分。

---

## 7. 补做：连续天数计入闪卡复习

上一版把「闪卡复习没有计入连续天数」记成了已知问题。理由是它在另一个包里，
接进来需要新接口与装配。**理由成立，所以本轮做了。**

| 文件 | 改动 |
|---|---|
| `backend/internal/flashcards/store.go` | 新增 `ReviewTimestampsSince`；`EnsureSchema` 补 `idx_flashcard_revlog_user_reviewed` |
| `backend/internal/learning/service.go` | 新增 `ReviewDayCounter` 接口 + `SetReviewDayCounter`；`Streak` 合并两个来源 |
| `backend/internal/learning/streak.go` | 抽出纯函数 `MergeActivityDays` |
| `cmd/pocketd/main.go` | 装配 + 编译期断言 `var _ learning.ReviewDayCounter = (*flashcards.Store)(nil)` |

三个刻意的选择：

1. **不新增 DDL 去重表，只加一个索引。** `flashcard_revlog` 原本只有
   `(card_id, reviewed_at DESC)` 索引，服务「这个用户哪天复习过」这个按用户+时间范围的
   查询只能全表扫。新增 `CREATE INDEX IF NOT EXISTS … (user_id, reviewed_at)`——
   缺索引是**性能**问题不是正确性问题，风险等级和「加一张新表」不一样。
2. **接口而非直接依赖。** learning 包不 import flashcards，靠 Go 的结构化接口满足，
   测试可注入假实现。
3. **合并成一条连续记录，而不是分开统计。** 周一复习、周二收集文章，对用户来说是
   「连续两天」，不是「复习 1 天 + 收集 1 天」——分开既难读也不对。

`MergeActivityDays` 是纯函数，所以「只刷闪卡、不收集材料的用户」这个**原本会显示 0
天的场景**有独立用例钉住（`reviews alone build a streak`）。

复习源查询失败时**降级而非清零**：捕获行为照常计入，只记日志。

---

## 8. 明确未验证的部分

| 项 | 原因 |
|---|---|
| **真实 Postgres** | 跨阶段缺口依旧。本轮新增的 `ActiveDayTimestamps`、`ClaimMilestone`、**`ReviewTimestampsSince`** 三条 SQL 一条都没在真库跑过；新加的 `idx_flashcard_revlog_user_reviewed` 索引也未验证创建成功 |
| 里程碑真的会只播一次 | 「第二次 claim 返回 false」有单测（假 client），但**真实数据库的冲突行为未验证** |
| 闪卡复习计入连续天数 | `MergeActivityDays` 的合并逻辑有 5 条子用例；**但 `ReviewTimestampsSince` 的 SQL 未在真库跑过**，`main.go` 的装配路径也未实测 |
| 时区 | `tz_offset` 由客户端传，缺省按 UTC。跨时区旅行会让日界移动，沿用既有口径未解决 |
| 真机 / 浏览器实测 | 连续天数徽标**未目视确认** |
| streak 窗口 730 天 | 超过两年的历史不参与 `Longest` 计算。未验证是否有用户会关心 |
