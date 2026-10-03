# 恢复脚本的彩排结果（2026-10-02）

对象：`scripts/recover-opencode-pocket-schema.ps1`
状态：**未对生产库执行**。全部动作在一次性库 `pocket_recover_trial` 上完成，该库已 DROP。

## 为什么先彩排

恢复脚本是"库已经没了"这个前提下唯一的东西。而这次事故的教训恰恰是
**测试通过和数据被删可以同时成立**，所以"脚本看起来对"不能作为执行理由。
在一次性库上真跑一遍，是唯一能拿到对照证据的办法。

## 彩排环境

| 项 | 值 |
|---|---|
| 库 | `pocket_recover_trial`（一次性，已 DROP） |
| schema | `opencode_pocket` |
| 端口 | 18120（用完已停，端口已释放） |
| 调度器 | `POCKET_SCHEDULER_ENABLED=false` |
| 生产库 | 全程未被写入，事后核对见文末 |

## 结论一：seed 必须钉 search_path，否则静默写错库

`deploy/sql/chat_agents_seed.sql` 里的表名**没有 schema 限定**，文件内也**没有** `SET search_path`。
A/B 对照（同一个库、同一个文件，只差 search_path）：

| 变体 | 落点 | 行数 | `public` 表数 | 退出码 |
|---|---|---|---|---|
| 不钉 | `public.chat_agents` | **277** | 1 | **0（不报错）** |
| 钉 `-c search_path=opencode_pocket` | `opencode_pocket.chat_agents` | 277 | 0 | 0 |

要点：错的那一版**退出码是 0**。如果不额外核对表落点，它看起来是成功的。
更要紧的是，若在生产库上跑错版本，**污染的是生产库 `postgres` 的 `public` schema**。

脚本因此新增 `PsqlApplyFile()`（钉 `PGOPTIONS`），
外加一条落点断言：若 `public.chat_agents` 出现 **≥200 行**则 abort。

## 结论二：四张关键表里有两张不是 pg.go 建的

第一次彩排进程**没有监听就退出了**，只留下 48 张表——`chat_agents` 和
`llm_gateway_configs` 都不在其中。原因是我给 `POCKET_JWT_SECRET` 的值只有 26 字节，
而签名器要求 ≥32 字节，进程在模块迁移跑到那两张表之前就 `Fatal` 了。

把 secret 换成 47 字节后：**66 张表，四张校验表全部存在**。

这两张表来自各自的模块级迁移（`internal/chatagent/store.go`、
`internal/server/llm_gateway_store.go` 的 `migrate()`），**不在 `internal/db/pg.go` 里**。
所以"重启即自动重建全部结构"这句话只对 pg.go 管的那些表成立；
另外两张要进程**活着跑过**才建得出来。脚本第 3 步的四表校验保留，并补了注释说明这一点。

> 脚本用的 secret 是 `'recover-local-throwaway-' + guid(24)` = 47 字节，安全。
> 这个下限是实测出来的，不是从代码里读出来的。

## 结论三：全新 schema 上网关本来就有 9 个模型，不会分片

| 观测点 | 值 |
|---|---|
| 登录 `POST /api/auth/login` | `auth_method=dev-bypass`、`user_id=user-admin`、`workspace_id=ws_user-admin` |
| 写之前的 GET | `apiKeySet=False`、`models=9`、`preferred=9` |
| POST（带 key） | `ok=True`、返回 9 个模型 |
| 写之后 GET 回读 | `apiKeySet=True`、`models=9`、`baseURL=https://llm.kxpms.cn/v1` |

所以脚本第 6 步的断言从 `models >= 1` 收紧成 **`models >= 9 且 preferred >= 9`**。
理由：这次事故留下的正是"key 有了但 models=0"的分片配置，
`>= 1` 会放过那种坏状态。

同时把取 token 的方式从 `go run ./cmd/gen-jwt`（自己编 `--user user-admin --workspace ws_user-admin`）
换成**走真实登录端点**。好处是顺带证明了重建后的实例真的能鉴权，
而且 workspace id 是登录返回的，不用我去猜 `ws_<userID>` 这个约定。

## 顺带查清：生产 `public` 里有 11 张遗留表

`opencode_pocket` schema 现在**根本不存在**，但 `public` 里有 11 张表。
其中 `public.chat_agents` 有 **3 行**：`c1` / `c2` / `builtin`，workspace 全是 `ws-1`，
名字是 `1` / `2` / `b`，`created_at=1790861466` → **2026-10-01 21:31:06 本地时间**。

- 这 3 行是**测试夹具**，不是用户数据。
- 21:31 正好落在**第一次删库（19:47）之后、第二次（22:48）之前**的窗口里。
- 也就是说：第一次 `DROP SCHEMA` 之后，又跑过一次 `go test`，那次测试按当时的
  `search_path` 在 `public` 里重新建了表并插了夹具。这是因果链的又一环。
- 另外 10 张（`emails`、`email_accounts`、`finance_transactions` 等）**行数全为 0**。

这些遗留物**不是**恢复脚本造成的，脚本也不碰 `public`（`pg.go` 给每个连接钉了 search_path）。
脚本现在会在 preflight 打印一行提示，让人知道它们早就存在。

## 彩排后修掉的脚本缺陷

| # | 缺陷 | 后果 | 修法 |
|---|---|---|---|
| D1 | seed 不钉 search_path | 静默写进 `public`，退出码仍为 0 | `PsqlApplyFile()` + 落点断言 |
| D2 | 自己用 `go run gen-jwt` 编 user/workspace | 猜 ID，且不证明能鉴权 | 改走 `POST /api/auth/login` |
| D3 | seed 的输出被 `Out-Null` 吞掉且不查退出码 | 部分应用无感知 | 捕获输出 + 查退出码 + 失败打印尾部 |
| D4 | `PsqlRun -Quiet` 失败返回 `$null`，`[int]$null` = 0 | **连接失败与"计数为 0"无法区分** | 换 `PsqlScalar()`，失败即 abort |
| D5 | 辅助函数里写了 `SET client_encoding` 前导句 | psql 把命令标记 `SET` 回显成第一行，`[int]` 转换炸掉（dry-run 实测） | 删掉前导句，靠 `PGCLIENTENCODING` |
| D6 | seed 前 `Push-Location` 到仓库根 | 暗示 search_path 跟 cwd 有关，实际无关 | 删掉 |
| D7 | 步骤编号 `7/6` | — | 统一 `n/7` |

D4 和 D5 都是**只有真跑才会暴露**的 bug——D5 正是 dry-run 第一次运行时炸出来的。

## 执行前请注意

1. 脚本**不停**已在运行的 18099 实例（pid 33948），改用独立端口 18110。
   哪套实例最终接管生产库，需要你来定。
2. 脚本**不抓邮件**（`POCKET_SCHEDULER_ENABLED=false`），也**不导入邮箱账户**。
3. 仍需你提供：`POCKET_AUTH_PASS`（≥8 字符）、网关 key、envs loader 位置、admin 口令。
4. 更根本的：`log_statement='ddl'` 没开之前，第三次事故依旧查不出来。
