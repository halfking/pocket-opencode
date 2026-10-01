
---

## §4.77 BUG-AW：会议数据零持久化（本轮修复 + 真进程 A/B 证实）

设备第六次恢复失败（§4.77.1），真机工作依旧停滞。本轮改为把**不依赖设备**
的缺陷结掉。选中的是上一轮发现但一直没处理的严重项：**会议记录完全不落库**。

### 4.77.1 设备恢复：第 6 次，仍失败（环境，非产品）

| 尝试 | 结果 |
|---|---|
| `adb devices -l` | `4c308e2e offline` + `192.168.31.19:5555 offline` |
| `kill-server` + 有界 20s `start-server` | 服务起来了，设备仍 offline |
| 有界 25s `adb connect 192.168.31.19:5555` | `failed to connect`（不是 `already connected` 也不是超时，是明确拒绝） |
| 复核 `adb devices` | 两台仍 `offline` |

前一轮已排除本机因素（5037 无第二个 adb server、ping 通、TCP 5555 通）。
**本轮不再消耗时间在 adb 上**：这是设备侧 adbd 的问题，机器侧无解。
⇒ `tasks-crud.yaml` 仍红、BUG-AX 真机回归仍未完成，**这两条的口径不变**。

### 4.77.2 BUG-AW 根因

`backend/internal/server/server.go` 里 `meetingStore: meeting.NewStore()`，
而 `internal/meeting/store.go` 的 `NewStore()` 返回的是
`meetings map[string]*Meeting` + `tombstones` —— **纯内存，零持久化**。

PG schema `opencode_pocket` 里没有任何 meeting 表，与代码一致。
**所以后端每次重启，所有会议记录（逐字稿、摘要、关键决策、待办清单）全部消失。**
在这个产品里这是代价最高的一类数据：丢了找不回来，却只活在内存里。

**顺带结掉一个旧疑问**：此前「后端有 7 条 `mtg_*` 但 UI 里看不到」，
根因就是重启清空 + `mtg_*` id 与前端 `meeting-*` 命名不一致被误读，
**不是两套 id 体系不通**（见 §4.76 之前那轮的端点对照）。

### 4.77.3 修法：照抄仓库自己已有的 finance 范式

本仓库早就有正确范式：`finance.NewStore()`（内存）作默认，
`main.go` 在 pool 就绪时经 `srv.SetFinanceStore(finance.NewPGStore(...))` 覆盖。
**没有另起一套设计**，直接对齐：

| 文件 | 改动 |
|---|---|
| `internal/meeting/pg_store.go` | **新增**：`PGStore` + `NewPGStore(ctx, pool)`，自建 `meetings` / `meeting_tombstones` 两表；JSONB 往返；删除与墓碑同事务 |
| `internal/meeting/store.go` | 新增 `MeetingStore` 接口（6 个方法），`Server`/resolver 的依赖类型从 `*meeting.Store` 改为它；`var _ MeetingStore = (*PGStore)(nil)` 编译期兜底 |
| `internal/server/server.go` | 字段 / `SetMeetingStore` / `MeetingStore()` 三处签名改接口；setter 加 nil 守卫 |
| `internal/learning/sources/resolver.go` | 字段与构造参数改接口 |
| `cmd/pocketd/main.go` | pool 就绪时建 PG store 并注入；**注入点必须在 `sources.New(..., srv.MeetingStore())` 之前**，否则 learning resolver 会拿到已被替换掉的内存 store（已在代码注释里写死这条） |

无 PG 的测试环境仍走内存版，零依赖。

### 4.77.4 判据：两套，互相独立

**（A）单元判据 `internal/meeting/pg_store_test.go`** — 每个测试独立 PG schema。

核心是**成对正负控** `TestPersistence_PositiveAndNegativeControl`：
同一场景（建 7 条 → 换新实例 → 读）对两种实现给**相反期望**：

| 实现 | 期望 | 实测 |
|---|---|---|
| `PGStore` | 换新实例后 7 条都在 | ✅ |
| 内存 `*Store` | 换新实例后 0 条 | ✅ |

成对的理由：只测 PGStore 的话，「读到的其实是同一个 map」这种假绿测不出来。

其余覆盖：JSONB 三列往返（`key_decisions`/`action_items`/`tags`）、
墓碑跨重启、workspace/owner 隔离、越权删/改、更新不存在必须报错、入参校验、
200 条并发创建 ID 唯一。

**（B）真进程 A/B 判据 `scripts/verify-meeting-persistence-ab.mjs`** —
两个**真实 pocketd 进程**、两个独立 PG schema、同一个探针，只换期望值：

| | 建 3 条 | 杀进程重启后 | 按 id 复查 | PG 直读 |
|---|---|---|---|---|
| 老版本 `11b7da5`（内存） | 201 ×3 | **0 条** | 404 / 404 / 404 | **根本没有 `meetings` 表** |
| 新版本（PG） | 201 ×3 | **3 条** | 200 / 200 / 200 | **3 行** |

脚本先 `DROP SCHEMA IF EXISTS` 再跑，可重复执行。
JWT secret 固定 ⇒ 重启后 token 仍有效，**「登录态没了」不会被混成「数据没了」**。

### 4.77.5 判据自己也坏了两次（都记下来）

绿了不算数。以下两处是**判据自己的缺陷**，是跑负控时暴露的：

1. **`pgxpool.Close()` 在失败路径上死锁。**
   负控（把 `CreateScoped` 的 INSERT 短路成 no-op）时，断言失败 →
   `t.Fatalf` → `runtime.Goexit` → 跑 cleanup → `pgxpool.Close()` 内部
   `puddle.Pool` 的 WaitGroup 永远等不到归零，
   **测试不是干脆报错而是挂到 `-timeout` 才 panic（45s）**。
   判据一坏，「红」就变成难查的 timeout。
   修法：`closeQuietly()` 有界关闭，且**先 DROP SCHEMA 再关连接**。
   修完负控 **5.4s 干脆报错**。

2. **A/B 脚本两侧套了同一套期望。**
   第一版老侧按 id 复查全 404 ⇒ `every(ok)` 为假 ⇒ 明明复现了缺陷却被判
   「判据不自洽」。**是判据写错，不是被测代码**。
   改法：每侧带 `expectByIdOk`（老侧 `false`、新侧 `true`），
   期望随被测版本变，但**探针与断言本身完全相同**。
   顺带修掉 `psql` 调用（本机这个构建把 DSN 之后的 `-t/-A/-c` 全当多余参数吞掉，
   改用 `PGHOST/PGPORT/...` 环境变量），并让「表不存在」单独报成
   「该版本会议只存内存」而不是空字符串。

### 4.77.6 全量测试：4 个失败，**基线对照确认是既有问题**

`go build ./...` exit 0。`go test` 有 4 个失败：

| 测试 | 失败信息 |
|---|---|
| `TestTaskWriteGuardBlocksPlainMemberPatch` | `bob PATCH someone else's private work item = 404, want 403` |
| `TestTaskWriteGuardBlocksPlainMemberDelete` | `bob DELETE … = 404, want 403` |
| `TestActiveDayTimestamps` | `narrow window got 2 timestamps, want 1: [1790831929 1790835529]` |
| `TestReminderLifecycle` | `an acked reminder must never come due, got 1` 等 3 条 |

**对照方法**：`git worktree add --detach` 出 `11b7da5` 的干净副本，
在**同一条 DSN、同一时刻**跑同一组测试 → **4 条逐字复现**。
⇒ **既有问题，不是本轮引入的回归。**
（那两个 task 守卫是 404/403 期望差；两个 learning 的是时间窗/时钟精度，
与 `store.go` 里记的「Windows 上 `time.Now()` 没有纳秒精度」同类。）

### 4.77.7 口径

- **BUG-AW：已修，且在两个真实进程上 A/B 证实**（老侧丢、新侧留、PG 行数对得上）。
  这不是「已改代码」的声明，是同一条探针在两侧给出相反结果的实测。
- **设备：仍然不可用（第 6 次失败）。** 真机相关的一切——`tasks-crud.yaml`、
  BUG-AX 真机回归、「tap 没反应」的机制——**本轮一条都没有推进**。
- **「陈旧坐标」假设仍未确证**，「reverse 指错端口自愈分支」仍未验，口径不变。
- 本轮**没有引入新的产品行为变更**；改动集中在后端存储层 + 测试判据。
