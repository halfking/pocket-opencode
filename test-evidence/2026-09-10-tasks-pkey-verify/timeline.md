# tasks_pkey 重复键闭环验证 — 时间线（2026-09-10）

## 结论（预填，最终数据见 verification.md）
- 错误源不是宿主机 dev 进程（backend/pocketd），而是 **deploy-local 容器化实例**
  `opencode-pocket-pocketd-local-openpocket`（8090→8088，镜像 9月8 构建、无修复）。
- 该容器 tasksync 启用（MCP→host.docker.internal:4101），每 5min 拉 ACC 任务，
  旧 ParseToolTasks 按行解析产出垃圾 id → task.Store.CreateTask INSERT 撞 tasks_pkey。
- 修复：以 3062fac 重建 arm64 镜像（revision label=3062fac），原位重建容器。

## 时间线（+08:00）
- 06:47 commit 3062fac 推送 origin/main
- 06:51 交接文档基线：PG 日志 tasks_pkey=508（42h 累计）
- 07:11 镜像重建 pocketd.new（宿主 dev 进程用）；基线计数 618
- 07:12:02 宿主 dev 进程重启（97336→18304，新二进制）——错误仍每 5min 继续
  （07:12:25 / 07:17:25 / 07:22:25），证明写入方另有其人
- 07:24 docker ps 全量排查 → 定位容器实例（Up 47h，9月8 旧镜像）
- 07:25:14 基线计数 621；重建 arm64 镜像（rev=3062fac）
- 07:26-07:29 compose 原位重建两次失败（网络名不一致→回滚旧镜像）
- 07:32:48 修正 OPP_NET_NAME/OPP_NET_EXTERNAL 后重建成功：
  image=b2a3e85f rev=3062fac，端口 8090、网络 opp-local-net、healthz OK
- 07:32:48 新容器首轮同步：`[tasksync] synced 9 ACC tasks`，PG 零报错

## 验证方法
docker logs llm-gateway-pg 2>&1 | grep -c 'tasks_pkey'  （修复后新增长度=0）
