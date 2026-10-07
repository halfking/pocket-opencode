# 会议/随手记：时间点自动入计划日程

> 2026-10-06 · 面向「录音 → 精校 → 即时总结 → 待办 → 计划日程」整条链路
> 涉及文件：`meeting-due.ts` / `meeting-due-plan.ts` / `meeting-due-reminder.ts`
> 及 `meeting-todo-persist.ts` / `meeting-ingest.ts` / `meeting-todos.ts`

## 0. 结论先行

本次修的**不是**新功能，而是堵一个「整条链路静默失效」的缺口：

LLM 摘要返回的 `action_items.due` 是自然语言（「明天下午三点」「周五上午10点」），
而链路下游三处都只认机器格式——

| 位置 | 修复前 | 后果 |
|---|---|---|
| `meeting-todo-persist.ts` `parseDue` | `Date.parse(due)` | 中文 → NaN → `due_at = null`，待办列表看不到期限 |
| `meeting-ingest.ts` `parseDue` | `Date.parse(due)` | 同上（录后精校路径） |
| `meeting-todos.ts` `accHandoffInput` | `scheduleExpr` 恒为 `now+60s` | 「转交」完全无视用户说的期限 |

⇒ 「将一些时间点自动加入计划日程」这条需求此前**没有任何落点**。
本轮把中文期限解析出来，并接到已有的 scheduled-tasks 上。

## 1. 需求四件事的现状核对

先核对需求提到的四件事在仓库中的真实状态，避免重复造轮子：

| 需求 | 状态 | 落点 |
|---|---|---|
| 切段转写要放在一起校对合并 | **已有** | `backend/internal/stt/incremental.go` 的 `mergeIncremental`（rune 级跨段重叠去重，40 rune 上限 / 2 rune 下限 / 整段重复保留判断）+ 前端 `RollingWebmDecoder` 转码 |
| 录音完成后一次精校 | **已有** | `useSessionLiveRecord.stop()` 自动调 `meetingsApi.refine`，后端 `/refine`（kxmemory → LLM fallback），状态置 `refined` |
| 录音时即时总结 + 参考资料与建议 | **已有** | `useLiveSummary`（30s 节流 + topicShift + 3 段阈值）→ `/summary` → `/recommend`（note/email/meeting/contact/web）；`MEETING_SKILLS` 4 种技能（滚动纪要 / 决议清单 / 待办提取 / 缺席速览） |
| **时间点自动加入计划日程** | **缺失** | 本文档主题 |

前三项都不是本轮工作，且不应改动：它们有独立的设计文档
（`docs/2026-07-02-meeting-recording-design.md`、`docs/2026-09-08-meetings-studio.md`）。

## 2. 网上可参考的项目

| 项目 | 借鉴点 | 本项目是否采用 |
|---|---|---|
| [chrono-node](https://github.com/wanasit/chrono) | 时间短语解析的事实标准，覆盖 en/ja/zh 多种语言 | **不引**，见 §3 |
| chrono-node-zh-jt / weather-bot fork | chrono 的中文强化分支 | 不引（中文 fork 停更于 2018） |
| [Meetily](https://github.com/Zackriya-Solutions/meeting-minutes) — 本地会议纪要（6k★），Electron + Whisper | 本地转写 + 纪要一体 | 架构同型，无需改 |
| [ros2_whisper transcript_manager](https://github.com/ros2-whisper) | 用 LCS 融合多段转写 | 思路同型：与本项目 `mergeIncremental` 的重叠去重一致 |
| [stable-ts](https://github.com/jianfch/stable-ts) regroup | 分段重组成稳定时间轴 | 概念一致 |
| CrewAI 多智能体 | 会议纪要多角色分工 | 现有 `useLiveSummary` + `MEETING_SKILLS` 已是同型，不引入框架 |

结论：合并策略自研已同型、总结架构与现状一致，**唯一值得引入的是时间解析的覆盖面**，
而那正是 §3 的取舍点。

## 3. 为什么不引 chrono-node

- 体积：chrono-node 全家桶 100KB+（含 locale 数据），为一个「短语」付出这个代价不划算。
- 输入形态不匹配：chrono 返回 `{index, text}` 的实体数组，是为**任意长文本的实体抽取**
  设计；会议待办的 `due` 本来就是一个短语（LLM 已经抽好了），我们只需要回答
  「这个短语能不能解出一个时刻」。
- 语义必须自控：`下午3点 = 15:00`、`中午12点 = 12:00`（不折算 0 点）、
  `明天无钟点 = 09:00`（上班时间而非当前时刻）、`下周一` 的周偏移口径——
  这些都得自己定义才可控，chrono 的默认行为与我们的产品语义并不一致。

自写约 200 行可覆盖需求场景，且零依赖。

## 4. 语义约定

| 输入 | 结果 | 理由 |
|---|---|---|
| `下午3点` | 15:00 | 中文口语 |
| `凌晨3点` / `晚上11点` | 03:00 / 23:00 | |
| `中午12点` | 12:00 | **不**折算成 0 点 |
| `早上8点半` | 08:30 | |
| `周三`（周三说） | 当天 | 默认**未来最近**，含今天 |
| `下周一`（周三说） | +5 天 = 下周一 | `(7-cur)+target`，见 §5 |
| `明天`（无钟点） | 明天 09:00 | 「明天交」= 明天上班时间，不是明天的当前时刻 |
| `2026-11-20` | 当天 09:00 | 避免 `Date.parse` 的 UTC 漂 8 小时 |
| 只有钟点、已过 | 明天同时刻 | 「下午三点」在下午五点说出口指明天下午三点 |
| 只有钟点、早于 now 超一天 | `null` | 明确指向昨天 ⇒ 放弃，不建假日程 |
| `2026-11-20`（带 `Date.parse` 旧口径） | 漂 8 小时 | 旧实现的真实缺陷，已修 |

**「明确指向过去 ⇒ null」这条只适用于裸钟点**。显式给了日期/周几（「今天上午十点」
「周三」）时锚定那一天，即使默认钟点已经过——周三 15:04 说「周三」= 今天要做，
丢了反而丢待办。

## 5. 两个真实 bug（附变异验证）

修的过程中发现并修掉两处**测试是绿的但行为是错的**：

### bug 1：`八点半` 被截成 `八点`

`findPeriodClock` 用并列备选正则，备选 `[零一二…]+点` 先于 `[零一二…]+点半` 命中，
clock 截成「八点」丢掉「半」⇒ `今天晚上八点半` 得出 20:00 而不是 20:30。

修法：把钟点语法从「一串并列备选」改成**可复用的 HOUR / MIN 片段**，
`半` 天然成为「点」后面可选分钟的一部分，不存在丢后半截。

```ts
const HOUR_RE = `(?:\d{1,2}|[零一二两三四五六七八九十]{1,3})`
// ★ 备选顺序要紧：半/一刻/三刻 必须排在宽松的「中文数字+分?」前面，
// 否则「三点三刻」会被宽松分支吃掉「三」、把「刻」留在外面，45 分变 3 分。
const MIN_RE = `(?:半|一刻|三刻|\d{1,2}\s*分?|[零一二两三四五六七八九十]{1,3}\s*分?)`
const CLOCK_SOURCE = `\d{1,2}\s*[:：]\s*\d{1,2}|${HOUR_RE}\s*[点點時时](?:${MIN_RE})?`
```

### bug 2：`下周一` 多算一周

原公式 `inWeek + 7`（inWeek = 本周内最近的 target）。周三说「下周一」时，
`inWeek` 已经指向 10-12（下周一），再 +7 变成 10-19——整整多一周。

正确口径 `(7-cur) + target`：与 target 在本周的位置无关。
周三(2)说下周一(0) = 5 天 ✓，说下周五(4) = 9 天 ✓。

### 变异验证

两处都做了「注入回旧实现 ⇒ 门禁必须转红」的实测：

| 变异 | 结果 |
|---|---|
| 周公式改回 `inWeek+7` | 2 fail（含新增回归靶子）✓ |
| `MIN_RE` 备选顺序颠倒 | 1 fail（`三点三刻` 45 分 → 3 分）✓ |
| `meeting-ingest.ts` 回退 `Date.parse` | 2 fail（接线层判红）✓ |
| `buildReminderInput` 的 expr 改回 `now+60s` | 1 fail（行为层判红）✓ |

## 6. 链路

```
LLM 摘要 due:「明天下午三点」
        │
        ├─ resolveTodoDue(due, now)          meeting-due-plan.ts（纯逻辑，可单测）
        │     └─ parseDueAt                  meeting-due.ts
        │
        ├─→ local_todos.due_at = parsed.at    两条入口：会中总结 / 录后精校
        │
        └─→ ensureTodoReminder(...)           meeting-due-reminder.ts（唯一带 I/O）
              └─ buildReminderInput → scheduleKind:'at', scheduleExpr: RFC3339
                       │
                       └─ 后端 schedule.go ScheduleAt: time.Parse(RFC3339, expr)
```

**为什么复用 scheduled-tasks 而不是新造日程表**：

- `ScheduleAt` 分支就是 `time.Parse(RFC3339, expr)`，一次性任务到点跑完即过期
  （`ComputeNext` 在 `t.Unix() <= baseSec` 时返回 0 → 自动停用），
  天然就是「提醒」语义，不需要额外的完成态字段；
- `schedule-plan.ts` 的 `DEFAULT_TZ` 已是 `Asia/Shanghai`，口径一致，
  不引入第二套时区语义。

**为什么把纯逻辑和 I/O 拆成两个文件**：`meeting-due-reminder.ts` 要 import
`scheduled-tasks/api`（无扩展名 import，node ESM 解析不了）。纯逻辑放在
`meeting-due-plan.ts`（只做 `import type`），门禁测试才能不 mock HTTP 就直接断言
「expr 带的是真实 due 时刻」。

## 7. 门禁

`meeting-due-plan.test.ts`（11 例）分三层，**每层带负控**：

- **A 行为层**：中文 due 必须解出时刻；提醒 expr 的时刻必须等于 due 解析结果
  （不是 `now+60s`）；payload 带待办原文/负责人/来源。
  负控：解不出的期限仍返回 null；`Date.parse('明天下午三点')` 确实为 NaN
  （把「旧行为」钉成事实，免得有人以为它本来就能解析）。
- **B 接线层**（防回退）：两个入口文件必须调用 `resolveTodoDue` +
  `ensureTodoReminder`，且不得再出现 `Date.parse(due)` 或本地 `parseDue`。
  `accHandoffInput` 必须用 `dueAtToScheduleExpr` 覆盖 `scheduleExpr`。
- **C 时区口径**：expr 必须带东八区偏移（否则所有提醒整体偏 8 小时，
  而「格式对」的断言照样全绿）。负控：零偏移后缀必须被判红。

## 8. 顺带修掉的

- `meeting-todos.test.ts` 原断言 `acc.timezone === 'UTC'`，但 expr 现在带的是
  东八区墙钟，两者不一致会让后端展示偏 8 小时 ⇒ 改为 `'Asia/Shanghai'`
  （与 `schedule-plan.DEFAULT_TZ` 对齐），并补一条负控锁住「有 due / 无 due
  两种情况触发时刻必须不同」。
- `meeting-due.ts` 里一段死代码占位（`const rel = ...; void rel`）和被环境
  改写过的注释字面量，一并清掉。
- `dueAtToScheduleExpr` 的往返断言原写成 `Date.parse(expr) === r.at`，
  这**只在东八区机器上成立**（注释却声称「任意时区都能过」，是假的）。
  改为断言「expr 相对 UTC 墙钟偏移 8 小时」，时区无关且带负控。
