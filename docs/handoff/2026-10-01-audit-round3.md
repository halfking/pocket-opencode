# 2026-10-01 审计轮 · 第三轮

> 接 `2026-10-01-audit-round.md` / `2026-10-01-audit-round2.md` /
> `2026-10-01-consolidation.md`。本轮按 24h 审计目标执行：拉取合并 →
> 唯一未合并子分支逐文件裁定 → 24h 提交流批判 → 验证落库。
> 只写有对照证据的部分。

## 0. 本轮最重要的一句话

**第二轮交付的 STT 补丁、以及 codex 分支昨晚新推的 3 个部署修复，
本轮都真正落进了 main；但推送时发现 `74b629c` 的提交信息是假的——
写着 `fix(stt)`，diff 里却只有两个文档文件，一行代码都没有。
这类「信息冒充代码」的提交能过审，是因为没人对 diff 与 message 做过交叉核对。**

## 1. 开工前置（按上一轮 §8 三条走）

| 检查 | 结果 |
|---|---|
| `origin/main` 是否已含邮件/STT feature | **中途已落**。02:17 consolidate 会话推了 `1b88264`（全量 email/STT + 密钥脱敏）+ `8103fbf`（STT 补丁落地）+ `5244615` + `23ceded`，本轮改为审计 + 补部署线 |
| 是否有别的会话在写同一工作区 | 本工作区（pocket-opencode）干净且 20s 双查无变化 ✅；但 **C:/workspace/openpocket 克隆三个 worktree 全部活跃**（主区 stt/discovery.go 14 分钟前、wt3 的 logs/ 6 分钟前、consolidate 02:06 刚 merge）——只登记不干预 |
| 工作区是否可入库 | 可。`go build ./...` EXIT 0 |

## 2. codex/platform-goal-20260930 复活后的 4 提交逐文件裁定

上一轮（`ee92f3e` §6.2）已裁定 8f832c6 的 12 文件（9 合 3 剔）并删过分支；
codex 会话随后**重推了分支**并新增 3 个提交（15edc52 / c409d7f / ded6d70，
23:11–23:55）。本轮对 merge-base 后的全部差异（18 文件）重新逐文件对照：

**合入（8 文件，163ec96）：**

| 文件 | 内容 | 为什么是好的 |
|---|---|---|
| `deploy/bin/lib/check-databases.py`（新增） | 只读数据库预检：PG 走 `PG*` 环境变量（密钥不进 argv），查 CONNECT / schema USAGE+CREATE / 表与序列权限；Redis PING；`--can-create` 用 ps+docker 清单拒绝重复置备 | 部署机真实凭据验证，此前只有 TCP 端口探测（端口通≠能登录≠schema 对）；fail-closed 语义 |
| `deploy/bin/tests/database_preflight_test.py` + `test_database_preflight.sh`（新增） | 预检单测 24 条 | 实测 24/24 PASS |
| `deploy/bin/ensure-databases.sh` | DSN 密码 URL 编码、拒绝覆盖已有 DSN、非空数据目录拒绝置备、起容器前 `--can-create` 门禁、**凭据先于卷初始化持久化** | 原版特殊字符密码直接拼坏 DSN；孤儿卷+未知随机密码的数据丢失路径被堵 |
| `deploy/bin/docker-compose.db.yml` | 三服务按**原生端口**映射、去 changeme 默认密码、`pg_isready -h 127.0.0.1` | 原版 `DOCKER_DB_PORT:DOCKER_DB_PORT` 在端口≠5432/6379/3306 时容器侧不通；healthcheck 原版走容器内 unix socket **恒成功（假阳）**——ded6d70 的正修 |
| `deploy/bin/start.sh` | 部署前跑预检；rollback 限非 dry-run | 与预检编排 |
| `deploy-local.sh` | ensure-databases 改 `source`（原版子进程执行，`OPP_*_MODE` **导不回父 shell**，后续模式判断恒空——活 bug）、参数校验、dry-run 只读化、.env 模板补缺不覆盖、网关密钥走 env 不进 argv | 本轮追加一处：`--inventory` 在 Windows MSYS（ps 无 -axo）降级为告警，**dry-run 不再被信息性清单判死**；真实预检路径保持 fail-closed |
| `tests/deploy-integration-test.sh` | 适配新 dry-run 语义；证据目录走 mktemp | 实测 15/15 PASS |

**拒绝（8 处，均为对 main 已修内容的回退，含上一轮已拒的重推）：**

- `deploy/bin/lib/database-detect.sh` 重写 + `test_database_detect.sh`：会回退 `7a3a7d7`
  的 /dev/tcp 优先（Git Bash BusyBox nc 不支持 -z 的真实坑）和 docker/systemd 复用验证层。
  detect 输出协议（`mode:host:port`）未变，ensure-databases 与之兼容，无需带它走。
- `deploy/bin/init-dirs.sh`：回退 `2b5000c` 的 `set -u` 空数组守卫（`${arr[@]+...}`）。
- `Dockerfile.frontend` + `deploy/docker/Dockerfile.frontend-prebuilt`：把 `nginx:1.24-alpine`
  改回 floating `nginx:alpine`——直接回退 BUG-AJ（154 实机 A/B 实证 mainline 在 CentOS 7 起不来）。
- `frontend/package.json` + `package-lock.json`：剥掉 `assert-no-plaintext-backend` 与
  check-i18n/check-icons 卡口（`e787b8d` 刚上的 CI 门禁）；tiptap 精确化与 fast-xml-parser
  钉版在上一轮已按「带测试升级」路径由 `39458c4` 独立完成。
- `frontend/src/config/api-base.ts`：纯删注释，语义零变化，不跟。

**结论**：codex 分支内容已全部吸收/拒绝完毕，远程分支删除（见 §6）。
与 detect 平行演进的那套「登录级预检」思想以 check-databases.py 形态保留，
两者互补：detect 管「有没有实例可复用」，preflight 管「凭据/schema/权限真的能用」。

## 3. `74b629c` 提交信息与内容不符（本轮新发现，已闭环）

- 事实：`git show 74b629c --stat` = round2 handoff + patch 文件，**零代码**。
  但 message 写 `fix(stt): …判 endpoint_missing`。
- 根因：round2 修复代码在未提交工作区（feature 未落地没法提交），
  交付形态是补丁文件；提交时把「意图」写成了「已完成」，且没在 message 里说明
  「代码在 patch 里」。
- 闭环：consolidate 会话 `8103fbf` 已把补丁真正落到 `backend/internal/stt/`
  （discovery_endpoint_missing_test.go 等），本轮合并树上 `go test ./internal/stt/...` 全绿。
- 教训：**交付物是补丁/文档时，message type 必须用 docs/test 而不是 fix**；
  审计口径新增一条——抽验提交必须看 diff 不只看 message。

## 4. 24h 提交流总结与批判（~110 提交，27 修复合并去重后 15 类）

### 4.1 构成

- BUG-D…BUG-AQ 共 43 个编号的修复（移动端写路径/WS/i18n/闪卡/笔记/任务/金库/邮件/部署），
  每个普遍带「根因+回归+阳性对照+证伪」四件套，抽验 BUG-AG/AD/AE/AH/AL 质量合格。
- 两对同名重复提交（BUG-T ×2、i18n errors ×2）= 双会话各写一半后 merge 收敛，
  收敛结果干净；BUG-U 编号被两个问题复用（handoff 已分节管理）。
- 两条 BOM 前缀提交信息（1814d15 / 03565ce），历史不重写。
- docs/handoff 13 个提交：包含一次误判撤回（§4.41.3）、两次措辞修正、
  一次「快照 checkout 静默回退 main」的贵教训（consolidation §4）——**诚实记录的密度明显高于往年**。

### 4.2 批判（本轮新登记）

1. **工作区即发布的幻觉（3 例同根）**：`74b629c` message 冒充代码、
   round1 §2 的 6 处编译错误走到工作区、consolidation §4 的「工作区能编译≠提交能编译」。
   三例同根：没有「提交前 diff 与 message 交叉核对 + 编译必须对提交树跑」的门禁。
2. **局部证据支撑全局结论**：consolidation 第一轮断言「仓库里没有密钥」只 grep 了
   backend/ 和 frontend/，漏了 test-evidence/。已写进其 §5.0 教训，本轮不再展开。
3. **分支复活无登记**：上轮裁定后删除的 codex 分支被原会话重推 3 个提交，
   main 侧无人知晓（本轮 fetch 才发现）。删分支 ≠ 会话终止；跨会话的分支持有约定仍缺失。
4. **并行会话风险仍在**：本轮全程 openpocket 克隆有 3 个活跃 worktree。
   本工作区幸运干净，但「共享工作区」问题没有流程解，只有「先查再动」的个人纪律。

## 5. 密钥处置状态（承接 consolidation §5.0）

- 当前树：完整密钥已脱敏（`<REDACTED-2026-10-01-see-handoff>`），
  `5244615` 又把 handoff 里的前缀缩写也清了；本轮 grep 复核无新命中。
- **git 历史里仍在**（1319229 / ab2e71b 引入）：轮换密钥 + 历史重写仍是
  **用户最高优先级动作**，机器不代做（force-push 不可逆）。
- 遗留疑点：`llm_gateway_mask_key_test.go:15` 另有一把 `sk-` 夹具字面量，未定性（承接）。

## 6. 分支与 worktree 处置

| 对象 | 状态 | 处置 |
|---|---|---|
| `origin/codex/platform-goal-20260930` | 4 提交已裁定（8 合 8 拒），2.5h 不活跃 | **删除远程分支** |
| `D:/temp/codex-audit` worktree | detached @ 8f832c6，0 脏文件 | 移除 |
| `origin/feature/*` 等 8 分支 | 全部 ahead=0 | 与上轮结论一致，无需再动 |
| openpocket 克隆：`wip/2026-10-01-audit`、`fix/bugz-marketplace-conflict` | ahead=0 但**仍在被写**（consolidation 23ceded 同结论） | 保留，等会话自然结束 |
| `C:/workspace/openpocket-baseline-wt` | 仓库外 worktree，非本轮所建 | 留给其主人 |

## 7. 本轮测试证据（合并树实测）

| 套件 | 结果 |
|---|---|
| `go build ./...` | EXIT 0 |
| `go vet ./...` | 无 undefined / cannot use |
| `npm run typecheck`（vue-tsc） | 退出 0 |
| `go test ./internal/stt/... ./internal/server/` | 全绿 |
| `go test ./internal/scheduledtask/... ./internal/task/` | 全绿 |
| `go test ./internal/email/` | 2 失败 = `TestWriteKeyAtomic_*`（Windows 权限位）+ `TestFetchPOP3MailboxAuthRejected`（本地 TCP 偶发），与 consolidation §3 记录的既知清单**同名同因**，非回归。round1 §4 的「未定性」疑问就此关闭 |
| `bash deploy/bin/tests/test_database_preflight.sh` | 24/24 |
| `bash deploy/bin/tests/test_database_detect.sh` | 7/7（优于上轮记录的 6/1——`7a3a7d7` 让 local-port 用例转绿） |
| `bash tests/deploy-integration-test.sh` | 15/15（含本轮 MSYS 修复后） |

## 8. 遗留（严禁外推）

1. **网关密钥轮换 + 历史重写**——用户动作，最高优先级（§5）。
2. consolidation §5.1 的 3 条产品语义问题（learning ActiveDayTimestamps OR 窗口、
   SnoozeReminder 基准、handleTaskOperations 无 CanWriteWorkItem）——**需产品定夺**，
   其中第三条偏安全，建议优先排期。
3. consolidation §5.2 清理项（真实发票号测试夹具、`sk-` 夹具定性、scripts/ 一次性探针）。
4. 预检的客户端依赖（psql/redis-cli/mysql 客户端 + python3）是部署机新前置，
   `start.sh` fail-closed 会拦住没装的机器——**需要文档化**（.env.example 或 deploy README）。
5. openpocket 克隆三 worktree 的会话结束后，`wip/2026-10-01-audit`、
   `fix/bugz-marketplace-conflict` 才可删；`openpocket-baseline-wt` 同。
6. compose 三服务若同机同启用，端口映射互不冲突（各用原生端口），但
   `DOCKER_DB_PORT` 仍是共享变量——三库并存的部署形态未测，本轮不扩。

## 9. 下一轮提示词

> 接 `2026-10-01-audit-round3.md`。本轮已把 codex 部署线、STT、邮件全量收进 main。
> 下一轮按优先级：
> 1. **（用户侧，先催）**网关密钥 `sk-6tGL…` 轮换完成后，评估历史重写
>    （git filter-repo + force-push + 全员重克隆）。
> 2. `handleTaskOperations` 补 `CanWriteWorkItem` 校验（consolidation §5.1 第三条，
>    同 workspace 普通成员可改删他人 private 工作项）——这是安全修复，不需要产品定夺语义，
>    修复后补 route 级测试。
> 3. 给 `deploy/` 补一页「预检前置」文档：psql/redis-cli/mysql/python3 依赖、
>    fail-closed 行为、MSYS 降级路径（deploy-local.sh --dry-run 的告警）。
> 4. consolidation §5.2 清理：invoice_realworld_test 换合成夹具、
>    `llm_gateway_mask_key_test.go:15` 的 `sk-` 夹具定性、scripts/ 探针归档。
> 5. learning 两条产品语义问题（§8.2 前两条）带着方案找用户定夺，别擅自选。
> 开工前照例：fetch、双查工作区、确认 openpocket 克隆那几个会话是否已停；
> 停了再清 worktree 与分支。
