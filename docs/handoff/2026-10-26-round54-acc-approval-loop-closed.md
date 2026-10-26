# 批判式审计轮（四）· 交接（2026-10-26）

> 这一轮把「委托给 ACC」这条链路**最后一跳**打通了：智能体要工具批准时，
> 人能在手机上看到并应答，任务能自己跑完，状态会跟着走。
> 根因不在 openpocket，**在 ACC 上没有那个端点**。

## 一、这一轮的结论（先看这个）

| 功能点 | 上一轮（§20） | 本轮（§21） |
| --- | --- | --- |
| 委托给 ACC | ✅ 通了（第一遍真的通） | ✅ 保持 |
| 停止任务 | ✅ 通了 | ✅ 保持 |
| **工具审批环** | ❌ **必然 permission_denied，且原因不可见** | ✅ **通了**（daemon 兜底 + App 能答） |
| **委托任务的状态** | ❌ **永远停在「进行中」** | ✅ **跟着 ACC 走**（回填 + 广播） |
| ACC 生态读面 | ❌ API 导出了但没人用（门禁红） | ✅ 接上 UI（委托面板旁） |

## 二、根因（一句话）

agent-companion 把工具审批转发给 ACC 的
`POST /api/v2/runtime/commands/{dispatch_id}/permission`，
而 **acc-go 从未实现这条路由**（实测 GET/POST 都 404；
agent-companion 自己的 `ACC_BRIDGE_SPEC.md` 却写着这条 —— 契约在，实现没有）。

⇒ 「委托给 ACC」的任务一碰到需要人工批准的工具，就必然 fail-closed；
而且 agent-companion 侧原来连拒绝原因都不打日志，现场只剩一个没有解释的
`permission_denied`。

## 三、改了什么

### agent-companion（提交 `b51aa41`）

* `internal/accbridge/executor.go`
  * deny 原因**落日志**（dispatch_id / tool_call_id / reason）；
  * ACC 端点「未接线」（404/501）时**回退到本地干预队列**
    （`internal/intervention.Hub`）：人应答 ⇒ 放行；没人应答 ⇒ 仍 fail-closed；
    ACC 答了仍以 ACC 为准；ACC 其它错误 ⇒ **不落队列**，直接拒。
* `internal/config/config.go`：`AC_PERMISSION_TIMEOUT_MS`（默认 120s）。
* `cmd/agent-companion/main.go`：把干预队列接进 accbridge 执行器。
* 判据：`internal/accbridge/permission_local_fallback_test.go`（8 条）。
* 文档：`docs/ACC_PERMISSION_ENDPOINT_GAP_2026-10-26.md`。

### openpocket（提交 `d646ae3d` / `9f8da87f` / `16e60897` / `0b3c69e7`）

* `backend/internal/server/companion_client.go`：
  `ListInterventions` / `DecideIntervention`（★ daemon 读面是 **camelCase**）。
* `backend/internal/server/companion_approval_handler.go`（新）：
  `GET /api/acc/approvals`、`POST /api/acc/approvals/{id}/decision`；
  未配置 ⇒ 503，上游失败 ⇒ 502。
* `backend/internal/tasksync/acc_status.go`（新）+ `scheduler.go`：
  ACC 派单状态回填（15s 一跳）+ `TaskEventNotifier` 广播。
* `backend/internal/task/store.go`：`ListACCBoundActiveTasks`。
* `backend/internal/server/server.go`：本地任务状态词汇加 `failed`。
* 前端：`api/accApprovals.ts`、`stores/accApprovals.ts`、
  `features/tasks/accApprovalLogic.ts`（纯逻辑，option id 只取智能体给过的）、
  `TasksView.vue`（「ACC 待批」卡片 + 「ACC 生态」区块）。
* Maestro：`.maestro/acc-approval.yaml` + `scripts/acc-approval-fixture.mjs`；
  两条 ACC 流登记进 `scripts/.maestro-flows.json`（此前 `acc-delegate-stop.yaml`
  **未登记** ⇒ 等于没被校验）。

## 四、复跑命令

```bash
export PATH="$HOME/sdk/go/bin:$PATH"
ADB=~/Library/Android/sdk/platform-tools/adb

# 1) daemon（带审批兜底）
cd /tmp/ac-e2e && git fetch origin main -q && git checkout <commit>
go build -o /tmp/agent-companion-new ./cmd/agent-companion
cp /tmp/agent-companion-new ~/kaixuan/acp/agent-companion
launchctl kickstart -k gui/$(id -u)/com.agent-companion.npm

# 2) 后端
cd /tmp/op-ac-test/backend && go build -o /tmp/pocketd-new ./cmd/pocketd
cd ~/kaixuan/acp/logs/rerun-round52 && BIN=/tmp/pocketd-new bash fix-pocketd-acc-wiring.sh

# 3) 审批环端到端（脚本层）
cd ~/kaixuan/acp/logs/rerun-round52/deliverables
python3 acc-approval-live-e2e.py          # 派单→待批→应答→ACC succeeded
python3 acc-permission-probe.py           # ACC 上那条端点确实 404

# 4) 真机流（emulator-5556）
cd /tmp/op-app
set -a; . /tmp/op-pocketd.env; set +a
export JAVA_HOME="$HOME/.local/jdk/jdk-21.0.12.1+1/Contents/Home"
export PATH="$JAVA_HOME/bin:$HOME/.local/bin:$HOME/Library/Android/sdk/platform-tools:$PATH"
export POCKET_DEV_PASS="$POCKET_AUTH_PASS" POCKET_AUTH_PASS="$POCKET_AUTH_PASS" \
       POCKET_MASTER="PocketTest2026" POCKET_SERIAL=emulator-5556 \
       POCKET_API_BASE=http://127.0.0.1:18099 \
       POCKET_MAESTRO_BIN="$HOME/.maestro/bin/maestro" \
       POCKET_ADB_BIN=/Users/niuer/Library/Android/sdk/platform-tools/adb \
       POCKET_APP_ID=com.kaixuan.opencode.pocket
node scripts/maestro-run.mjs .maestro/acc-approval.yaml .maestro/acc-delegate-stop.yaml

# 5) 后端全量（干净 env + DSN）
cd /tmp/op-ac-test/backend
DSN=$(grep -o 'postgresql://[^ ]*' /tmp/op-pocketd.env | head -1)
env -i HOME=$HOME PATH="$PATH" POCKET_TEST_POSTGRES_DSN="$DSN" go test ./... -count=1
```

## 五、下一轮该做什么（按优先级）

1. **ACC 侧补两个端点**（本机做不了，需 `.211` 的 SSH）：
   `GET /api/v2/runtime/permissions`（待批清单）与
   `POST /api/v2/runtime/permissions/{id}/decision`（应答 + 解掉 daemon 那个
   挂着的 POST）。补上后，daemon 的兜底应当切回 ACC 为权威。
2. **设备侧全量复跑**（13 条流）—— 本轮只跑了新增/受影响的 2 条。
3. **foreign ACC 任务的停止**：App 里没有本地任务行 ⇒ 停止仍 404。
4. **任务历史 / 生成文件同步**：daemon 的 run 历史会被驱逐 ⇒ 需要把
   ACC 侧的事件/命令面做成可分页的历史读面。
5. **`/api/acc/approvals` 的租户边界**：现在是单机单租户。
6. **ACC runtime-control 面不稳**：同一个 GET 实测 200(2.6s)/200(22.9s)/
   200(67s)/502(5s) 交替，根因在对面机器（.211）。

## 六、纪律（本轮又踩到的）

* **判据自己也要当判据测**：本轮新流第一版是**假绿**（断言匹配到弹窗输入框
  里的文字，委托请求压根没发出去）；夹具注册丢了（flow 以普通字符串登记
  ⇒ harness 不造前提 ⇒ 流红在本就不该红的地方）。
* **坐标判据天生脆弱**：弹窗里多一块内容 ⇒ sheet 变高到顶满屏 ⇒ 输入框
  坐标从 y≈1350 移到 y≈324 ⇒ 56% 那个点戳到了「描述」框。
  ⇒ 前提尽量由**夹具用 API 造**，flow 只验 UI 面。
* **同名选择器**：弹窗标题与提交按钮同名 ⇒ Maestro 会选中标题。
* **设备 ANR**：本轮中途 system 与 App 双弹 ANR（宿主 load1 98）⇒
  设备流必须等 load1 < 25 再开跑。
* **不要动本地 main**：两个仓的本地 main 都落后且带并行会话的未提交改动 ⇒
  一律用临时 worktree + `git push origin HEAD:main`。
