# tasks_pkey 重复键闭环验证（2026-09-10）

## 结论
修复 commit 3062fac 的容器化部署验证通过。错误源为本地 deploy-local 容器
`opencode-pocket-pocketd-local-openpocket`（旧镜像 9月8 构建，无修复），
宿主机 dev 进程（backend/pocketd）并非写入方。

## 验证数据（PG 容器 llm-gateway-pg，日志 grep tasks_pkey）
| 时点 | 计数 | 说明 |
|---|---|---|
| 06:51（交接基线） | 508 | 42h 累计 |
| 07:11（宿主进程重启前） | 618 | 每 5min +1 持续增长 |
| 07:25:14（容器重建前） | 621 | 宿主进程重启(07:12)无效→证明写入方是容器 |
| 07:32:48（新容器启动） | 622（终值） | 07:27:25 旧容器最后一次报错 |
| 07:47:32（3 个周期后） | 622 | **新增 0**，总计 3 轮 sync 全部成功 |

## 新容器同步日志（docker logs opencode-pocket-pocketd-local-openpocket）
```
23:32:48 [tasksync] started, interval=5m0s
23:32:48 [tasksync] synced 9 ACC tasks
23:37:48 [tasksync] synced 9 ACC tasks
23:42:48 [tasksync] synced 9 ACC tasks
```
（UTC；+8 = 07:32:48 / 07:37:48 / 07:42:48）

## 部署细节
- 镜像：opencode-pocket:pocket-opp（b2a3e85f，revision label=3062fac，
  `./deploy/bin/build-images.sh --arch arm64 --backend-only`）
- 容器：compose 原位 force-recreate，项目 opencode-pocket-local-openpocket，
  网络 opp-local-net，端口 8090→8088，healthz OK
- 坑：compose 网络名按 `${OPP_NET_NAME:-opp-${DEPLOY_ENV}${OPP_CONTAINER_SUFFIX}-net}`
  派生，与原部署不一致时 force-recreate 会回滚旧镜像容器；
  需显式 OPP_NET_NAME=opp-local-net OPP_NET_EXTERNAL=true OPP_CONTAINER_SUFFIX=-openpocket
- 宿主 dev 进程 18304（backend/start-dev.sh）也已换新二进制（07:12），
  其 tasksync 因缺 POCKET_MCP_TENANT_ID 为 disabled，不参与本验证

## 同 session 附带：上游遗留测试修复（另 commit）
- internal/config ×3：生产校验夹具补 `RSS{HTTPTimeout, MaxConcurrency}`
- internal/email TestDailySummaryUniqueIndexIsWorkspaceAware：pg_constraint
  查询补 current_schema() 限定（原查询跨 schema 扫到 public 遗留约束误报）
- internal/email TestSyncPersistsFetchedEmails：真实产品缺口——上游 Sync 改
  envelope-only fetch 后 snippet 无任何回填路径；新增 fetchSnippetOnConnected
  复用同一连接按需单封补拉 BODY[TEXT]（Peek、无 partial，规避 Greenmail bug）
