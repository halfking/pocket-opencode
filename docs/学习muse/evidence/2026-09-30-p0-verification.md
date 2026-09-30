# P0 验证证据（2026-09-30）

**范围**：本文只记录**已实际执行并观察到输出**的验证。命令、结果原样列出；
未执行的项明确标为"未验证"，不当作通过。

环境：Windows / PowerShell，Go 1.27.1，仓库根 `C:\workspace\openpocket`。

---

## 1. 编译

```powershell
cd backend; go build ./...        # 退出码 0
```

改动前（HEAD `47803c3`）基线编译同样是 0，改动后仍是 0。

## 2. 新增/改动包的单测

```powershell
cd backend
go test ./internal/learning/     # ok  1.06s
go test ./internal/task/         # ok  0.16s
go test ./internal/server/ -run Learning   # ok  0.32s
```

| 包 | 测试文件 | 覆盖的关键行为 |
|---|---|---|
| `internal/learning` | `scheduler_test.go` | 4 评分 × 4 状态矩阵总有效、毕业间隔、Easy 用 easy 间隔、Again 进 relearning 且计 lapse、难度单调与夹取、留存率方向性（高留存→短间隔）、成功复习间隔递增、脏输入（S=0 / 超大 elapsed）不产生 NaN、确定性、遗忘曲线单调 |
| `internal/learning` | `service_test.go` | Capture/Reminder 请求校验矩阵、`HH:MM` 解析与越界拒绝、每日提醒"今天/明天"选择、免打扰顺延、one-shot 不重复、畸形规则不空转、零摘要不推送策略、无 store 时 fail-closed |
| `internal/task` | `worktype_test.go` | 类型枚举闭合集、分组映射、`AllTypes()` 稳定且按组排序、可见性/来源/参与者角色/事件类型校验、`normalizeWorkItem` 兜底、JSONB 列编解码 |
| `internal/server` | `learning_route_test.go` | 8 条 `/api/learning/*` 路由**非 404**（无 store 时 503）、未知子路径仍 404、`/api/learning/schedule` 无 store 也能算出真实排程、rating/state 越界 400 |

### 测试过程中被测试抓出、并已修复的真实缺陷

按"被测出来才算数"的标准，这三处是实测发现的，不是事后补的说明：

1. **间隔公式符号错误**：`I = S/F·(R^{-1/DECAY} - 1)` 写成了 `(-1)/DECAY`，在 DECAY=-0.5 时算出 `R²-1 < 0`，
   所有卡片间隔被夹到 1 天。修正为 `1/DECAY`（= R^(-2)），并由
   `TestScheduleHigherRetentionShortensInterval` + `TestScheduleSuccessiveGoodsGrowInterval` 双保险锁定。
2. **失败复习残留天级间隔**：review+Again 已进入 relearning（1 分钟后到期），`IntervalDays` 却仍是夹取后的天级值，
   UI 会显示"几天后"。修正为归零。
3. **提醒时间只校验格式不校验范围**：`ruleValue="29:30"` 被接受。改为复用 `parseHHMM` 做范围校验。

另有一处**我自己的测试断言写错**（以为 Easy 的稳定性必然高于 Good）：查仓库内 `ts-fsrs` 5.4.2 的
`next_recall_stability` 源码确认 `w16` 是 `easy_bound`（FSRS-5 默认 0.3332 < 1），
是"上界"而非"奖励"，收益体现在难度下降。实现保持与参考实现一致，测试改为锁定参考语义。

## 3. 全量回归

```powershell
cd backend; go test ./...
# 通过包 48 个，失败包 2 个
```

失败的 2 个包：

| 包 | 失败用例数 |
|---|---|
| `internal/agent` | 16（`TestPiAdapter_*`、`TestStdioTransport_*`、`TestACPStdioAdapter_SubscribeEvents`） |
| `internal/email` | 2（`TestWriteKeyAtomic_CreatesFileWithCorrectMode`、`TestFetchPOP3MailboxAuthRejected`） |

### 基线对照（证明不是本轮引入）

用 `git worktree add .baseline-check HEAD` 拉出**不含本轮改动**的独立工作树，跑同样的两个包：

```powershell
cd .baseline-check\backend; go test ./internal/agent/ ./internal/email/
# 同样失败：agent 16 条、email 2 条，用例名逐条一致
```

→ 判定：**这 18 条失败是既有问题（agent 适配器依赖外部 pi 进程/stdio 环境，
email 两条与 Windows 文件权限/网络语义相关），与本轮改动无关。**
对照工作树已删除。

## 4. 前端类型

`frontend/src/api/client.ts` 的 `Task` 接口删除了后端从不返回的 `category` / `owner`，
改为后端真实返回并校验的 `type` / `typeGroup` / `ownerId` / `assignees` / `dueAt` /
`remindAt` / `parentId` / `originKind` / `originRef` / `tags` / `visibility`。

- 全仓检索确认**没有任何调用方读 `task.category` / `task.owner`**（同名命中全部属于
  vault / email / finance 各自的 `category` 字段），因此删除不产生编译错误。
- `npx.cmd vue-tsc --noEmit -p tsconfig.json` → **退出码 0，无类型错误**
  （本机 PowerShell 执行策略禁止 `npm.ps1`，故直接用 `npx.cmd` 调 vue-tsc）。

## 5. 明确未验证的部分

| 项 | 原因 |
|---|---|
| DDL 实际落库效果 | 需要真实 Postgres；`migrate()`/`EnsureSchema()` 沿用仓库既有幂等范式，**但未在真实库上跑过** |
| `/api/learning/*` 端到端读写 | 需要真实 PG + 登录态；仅做了路由层（503/404/400）断言 |
| 学习提醒真实推送 | APNs/FCM 仍是 `NoopPushSender`（既有状态），前台 WS 路径未做端到端实测 |
| 前端 typecheck | 已通过（`npx.cmd vue-tsc --noEmit` 退出码 0），见 §4 |
| 真机 / 浏览器实测 | P1 才有 UI，P0 无界面可测 |
