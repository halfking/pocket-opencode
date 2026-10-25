# handoff · 批判式审计轮（四）· 2026-10-25 22:5x

> 上一轮：§20（openpocket `docs/handoff/2026-10-22-round52-…md`）
> 本轮做了什么：**把「App 上的 ACC 功能」从声明变成读数**，并修掉三处互相掩盖的缺陷。

## 0. 一句话现状

| 能力 | 状态 | 证据 |
|---|---|---|
| App「委托给 ACC」（建任务→派发→写绑定） | ✅ 真机上跑通 | `.maestro/acc-delegate-stop.yaml` 全绿；线上 e2e 200 |
| App「停止任务」（任务级，后端到 ACC） | ✅ 真机上跑通 | 同上；线上 e2e：ACC 命令 `cancelled`/`delivered` |
| 智能体识别（ACC 读面） | ✅ 200 | agents 77 / tasks 23 / projects 1 / sessions 23 |
| ACC 部署接线 | ✅ 修好 | 原指向**不存在的** `127.0.0.1:4101` + 旧密钥 |
| 多实例同库启动 | ✅ 修好 | 原为 40P01 死锁，互杀 |
| 智能体工具权限审批环 | ❌ **未打通** | 权限门 30s 超时 ⇒ `permission_denied` |
| 停止「别人建的」ACC 任务 | ❌ 未做 | App 无对应本地任务行 ⇒ 404 |
| 任务历史/生成文件同步 | ❌ 未做 | 本轮没碰 |

## 1. 本轮修的东西（都已推 main）

- openpocket `d0807c74` + `86d01368`；agent-companion `d3d5399`。
- 核心：delegate 无 `run_id` 时由后端建任务+派发+**写绑定**；任务菜单加「⏹ 停止任务」；
  新增 `internal/pgmigrate`（schema 级 advisory lock）修多实例启动互杀；
  ACC 接线/凭据修复脚本（`fix-pocketd-acc-wiring.sh`，含写回 0600 env 文件）。

## 2. 环境事实（下一轮直接用）

```bash
# 权威 ACC 在**另一台机器**（不是本机！本机 192.168.31.33，没有 4101 监听）
ACC=http://100.106.67.198:4101     # == 192.168.31.211:4101
# 密钥：~/kaixuan/acp/.env 的 ACC_AUTH_TOKEN_SECRET（不要打印，只走 env）
# 本机 agent-companion daemon：127.0.0.1:12110（runtime_id=acs-ac30-01）
# 设备侧 pocketd：18099 / 18097（env 文件 ~/kaixuan/acp/logs/rerun-round52/pocketd-18099.env，0600）
# 派单目标已配：POCKET_ACC_DISPATCH_RUNTIME_ID=acs-ac30-01 / _AGENT_ID=opencode
```

⚠️ **ACC 的 runtime-control 面当前不稳定**：同一个 GET 实测 200(2.6s) / 200(22.9s) /
200(67s) / 502(5s 网关超时) 交替出现。客户端已放宽到 60s/4 次重试，但**根因在对面机器**。
⇒ 下一轮做端到端时**必须允许重试**，并把「ACC 502」与「代码缺陷」分开。

## 3. 下一轮该做什么（按价值排序）

1. **打通智能体工具权限的审批环**（当前最大缺口）：
   派单带三元组后，智能体申请工具权限 ⇒ 需有人在 30s 内应答。
   线索：daemon 的权限门 `internal/agentfacade/acp_driver.go`；
   ACC 侧有审批箱（`/api/v2/runtime/commands/{dispatch}/permission`）；
   openpocket 侧有 `/api/mobile/approvals/*`（`internal/server/mobile_approval_handler.go`）。
   目标读数：派单 → 智能体要权限 → App 批 → 智能体继续 → 任务完成。
2. **停止「foreign」ACC 任务**：App 里 ACC 的 canonical 任务没有本地行 ⇒ 404。
   需要一条「按 ACC 任务 id 停」的后端路径（ACC 侧有 command/dispatch 面可查）。
3. **任务历史与生成文件同步**：agent-companion 的会话/产物面（`/api/v1/sessions`、artifact 面）
   在 App 里只读未上 UI。
4. **ACC 读面（tasks/sessions）上 UI**：现在只有 agents 用了（选派单目标）。
5. `tasksync` 的缓存对用户是「死重」（落在 workspace=default，而用户是 ws_user-admin）；
   App 列表里的 ACC 任务来自**实时 MCP 面**，不是缓存 —— 要么让缓存进用户 workspace，
   要么承认它只服务别的用途。

## 4. 复跑命令（都实测过）

```bash
# 后端全量（干净环境 + DSN；不注入 pocketd 运行环境，否则 3 个测试误红）
cd /tmp/op-ac-test/backend
DSN=$(grep -o 'postgresql://[^ ]*' /tmp/op-pocketd.env | head -1)
env -i HOME=$HOME PATH="$HOME/sdk/go/bin:$PATH" POCKET_TEST_POSTGRES_DSN="$DSN" go test ./... -count=1

# 线上端到端（委托 → 派发 → 接单 → 停止 → ACC cancelled）
cd ~/kaixuan/acp/logs/rerun-round52/deliverables && python3 openpocket-delegate-live-e2e.py

# ACC 接入面探针
python3 acc-surface-probe.py

# 设备侧全量（13 条流；负载闸门 load1<25）
cd ~/kaixuan/acp/logs/rerun-round52
POCKET_API_BASE=http://127.0.0.1:18099 WT=/tmp/op-ac-test bash audit-batch4.sh

# 新增流（ACC 委派 + 停止）
cd /tmp/op-app && python3 scripts/acc-delegate-stop-fixture.py   # 先清残留，防假绿
POCKET_API_BASE=http://127.0.0.1:18099 node scripts/maestro-run.mjs .maestro/acc-delegate-stop.yaml

# 部署（重建 + ACC 接线 + 写回 env）
cd /tmp/op-ac-test/backend && go build -o /tmp/pocketd-new ./cmd/pocketd
BIN=/tmp/pocketd-new bash ~/kaixuan/acp/logs/rerun-round52/fix-pocketd-acc-wiring.sh
```

## 5. 纪律（本轮踩到的，别再踩）

- **提交用临时 worktree + `git push origin HEAD:main`**，不要动本地 `main`
  （两仓本地 main 都有并行会话的未提交改动：openpocket 3 条、agent-companion 23 条）。
- **读代码要对 `origin/main`**（`git show origin/main:<path>`），本地工作树是落后的。
- **Maestro 的 `visible` 是完整匹配**：「派给哪个智能体」≠「派给哪个智能体（可选）」；
  「停止任务」≠「⏹ 停止任务」⇒ 判据要写正则（`.*停止任务.*`）。
- **弹窗里的 input 在无障碍树里是 `[EditText] t="" cd=""`** ⇒ 只能按坐标点，
  坐标从失败现场的无障碍树导（`~/.maestro/tests/<ts>/…/screen-hierarchy/*.json`）。
- **断言前先清同名残留**，否则 Maestro 的 visible 会假通过。
- **证据横幅本身也要当判据测**：本轮它错了两次（写死假标签；lsof 取到 cwd）。
  横幅现在运行时现取，并显示 `vcs.modified`。
- **别把 ACC 的 502 当代码缺陷**：先量延迟（`acc-direct-probe.py`）。
- **daemon 的 run 历史是内存态、会被驱逐**：事后取证要靠 ACC 的运行事件/命令面。
