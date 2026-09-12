# T02 非空补流 + 产物恢复 — 真实 30 分钟离线窗口证据包

日期：2026-09-12。**结果：真实 30 分钟 Pocket 进程离线窗口，期间任务被真实执行（独立 runner + checker + ACC 证据门），Pocket 重连后同一 task/run 绑定与游标持久，`after=2` 补流返回窗口内产生的全部 10 个事件，产物 SHA-256 在 runner receipt / checker verdict / ACC 事件载荷 / 磁盘字节四处一致。** 本文补齐 [W1 交接审计](../../../../../../ai-native-tools/docs/来自其他模块/openhands-buzz-integration/handoff-audit-2026-09-11.md) §3 中 T02 的两项残缺断言。

## 时间线（UTC，2026-09-11 晚）

| 时刻 | 事件 |
|---|---|
| 19:36 | delegate 绑定：Pocket `t02-pocket-task-w1` ↔ ACC `run_d18ba3f2-541f-4322-862b-85d0185d1247`；初始游标 **2**（run.created, task.created） |
| 19:37:22 | **Pocket 进程停止（离线开始）** |
| 19:40–19:47 | 离线期间执行（supervisor 路径，信任形同 2026-09-10 standalone T16）：dispatch → claim(fence=1) → t16-runner 真实 OpenCode 执行 → 独立 checker 13 边界用例 → ACC 证据门 complete 200 → review 200，task **succeeded**。attempt 1 因 harness 目录布局缺陷被**诚实取消**（complete success=false, error_code=harness_setup_retry），retry 后 attempt 2 成功——两次尝试全程在 http.jsonl |
| 20:07:38 | Pocket 重启（同一 PG schema `w1_t02`，绑定/游标持久），重新登录 200 |
| 20:07:40 | `GET /api/tasks/t02-pocket-task-w1` → 200（同一 task 投影）；`events?after=2` → **非空补流** |
| 20:09+ | 复核：完整重放 10/10 事件（`replay-full.sse`，3873 字节） |

## 补流事件（seq 3–12）

```text
3  dispatch.created   (attempt 1)
4  dispatch.leased
5  dispatch.started
6  dispatch.failed        ← attempt 1 诚实失败（harness_retry）
7  dispatch.created   (attempt 2, retry)
8  dispatch.created       ← retry 端点补发（payload 丢失致 invalid_payload，见"范围声明"）
9  dispatch.leased
10 dispatch.started
11 dispatch.completed      ← 载荷含 artifact_sha256=cd8cc258b8dd6a0d…
12 task.review.approved    ← task succeeded
```

产物一致性（4 处逐位一致）：runner receipt = checker verdict = ACC dispatch.completed 事件载荷 = 磁盘 `port_validation.py` 实测 SHA-256 `cd8cc258b8dd6a0d1d5b2a08b411463bf2ca0bc2a28e35aa383109efd9492171`。

## 断言判定

| 断言 | 结果 |
|---|---|
| 30 分钟真实离线（进程级） | **PASS**（1831s / 1937s 两次恢复实测） |
| 重连后同一 task/run 绑定 + 游标持久 | **PASS**（PG schema `w1_t02` 持久化，重登录后 GET task 200） |
| 非空补流（`after=initial_cursor`） | **PASS**（10 个窗口内事件全量补齐，无重复无遗漏） |
| 产物恢复（真实执行产物经事件流可见且字节可验证） | **PASS**（4 端 digest 一致 + checker 13 用例独立验证） |

## 文件

- `replay-full.sse` — 权威补流捕获（10/10 帧）
- `offline-state.json` / `recover-state.json` — 离线起点与恢复终态（含截断观察备注）
- `http.jsonl` — 全程请求/响应（Authorization 不记录；login 响应中 JWT 已脱敏）
- `receipt.json` / `receipt.sig` / `trusted-key.pub` / `expected.json` / `events.jsonl` / `checker-verdict.json` — 窗口内真实执行证据
- `t02_driver.py` / `t02_execute_offline.py` — 试验脚本

## 范围声明与诚实记录

- Pocket 本地任务行 status 保持 `queued`（投影薄设计，canonical 状态在 ACC 事件流）——本断言的"状态恢复"以事件流投影为准。
- 离线窗口内执行走 supervisor 路径而非 companion daemon：daemon 只订阅自身 command run，Pocket 绑定的是独立 orchestration run；两 run 域统一派发是 W2 接线工作。
- retry 端点重新派发时丢失原始 input payload（attempt 2 走 orchestration dispatch 重新携带），该缺口已记入 W1 收口审计 §7。
- 重启后首次 urllib 读取出现过一次截断（止于 seq 5），curl 与后续读取均完整；`replay-full.sse` 为权威捕获。
- 口令/密钥不入 git：Pocket dev 口令与 ACC trial secret 仅经环境变量注入；http.jsonl 中 JWT 已脱敏。
