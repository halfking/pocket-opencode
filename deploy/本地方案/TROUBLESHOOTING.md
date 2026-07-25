# 部署与故障排查经验

## 1. 不重复接管共享 PostgreSQL

本机已有 `r112_postgres`（PG17，宿主 `15432`，健康）。Pocket 使用独立 `pocket_local` 数据库，并通过外部 `r112_net` 接入。不要在 Pocket Compose 中再声明 PostgreSQL，也不要对 `r112_postgres` 执行 `down`、`rm` 或 volume 清理。

## 2. Docker Hub 网络不可用时的构建策略

第一次多阶段构建因 BuildKit 无法访问 Docker Hub token 失败，错误为 `DeadlineExceeded`。本机已有 Alpine、Node 和 Nginx 镜像，但 Go builder 镜像元数据无法获取。

当前本地 Compose 使用 `Dockerfile.runtime`：

1. 宿主机用 `GOOS=linux GOARCH=arm64 CGO_ENABLED=0` 编译 `deploy/本地方案/artifacts/pocketd`。
2. Alpine runtime 镜像只复制该静态二进制。
3. `local-up.sh` 默认自动执行这一步。

根 `Dockerfile` 仍保留联网环境下的标准多阶段构建，不应把离线 workaround 当成生产镜像方案。

## 3. 前端端口冲突

本机已有 Node/Vite 进程监听 `4174`。Docker 容器内部 Nginx 健康正常，但宿主请求会命中旧进程。Pocket 本地端口统一为 `4175`，避免停止其他工作区服务。

## 4. users 表初始化

原启动链路依赖 `users` 表已存在，空数据库会导致认证初始化不完整。现在 `auth.EnsureSchema` 在 `UserStore` 构造前幂等创建 `users` 表和 username 索引，随后自动 bootstrap 本地 `admin` 用户。

## 5. 测试环境污染

Go config 默认值测试必须保持 hermetic。`local-test.sh` 先执行单测/vet/build，再加载 `.env.local` 执行 API smoke；不能在脚本开头 source 本地运行环境，否则会把 `POCKET_DEV_AUTH`、`POCKET_EMAIL_FETCH_ENABLED` 等运行配置污染单测。

## 6. 不健康的外部服务

当前 `kxmemory-go-local`、MinIO、Neo4j 状态可能为 unhealthy，但核心 Pocket 本地测试不依赖它们。相关 AI/邮件/Android/OpenCode 测试必须单独标记，不得将其跳过写成通过。
