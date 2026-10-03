# BUG-AJ 证据：nginx:alpine（mainline 1.31.5）在 CentOS 7 起不来，nginx:1.24-alpine 正常

环境：154 服务器（CentOS 7，`Linux 3.10.0-1160.119.1.el7.x86_64`，Docker 26.1.4，
registry-mirror=daocloud）。两者 `Config.User` 均为空（root 启动）。

## A/B 对照（同一台机、同一命令，2026-09-30 21:23-21:24 +08:00）

```
$ docker inspect nginx:alpine --format '{{.Config.User}}'      → 空（root）
$ docker inspect nginx:1.24-alpine --format '{{.Config.User}}' → 空（root）

$ timeout 12 docker run --rm nginx:alpine nginx -g "daemon off;"
2026/09/30 13:23:50 [notice] 1#1: getrlimit(RLIMIT_NOFILE): 1048576:1048576
2026/09/30 13:23:50 [crit] 1#1: pwrite() "/run/nginx.pid" failed (1: Operation not permitted)
nginx: [crit] pwrite() "/run/nginx.pid" failed (1: Operation not permitted)   ← master 直接退出

$ timeout 12 docker run --rm nginx:1.24-alpine nginx -g "daemon off;"
2026/09/30 13:24:02 [notice] 1#1: worker process 30 exited with code 0   ← 正常运行到超时被杀
2026/09/30 13:24:02 [notice] 1#1: worker process 31 exited with code 0
```

root 对 /run 的写权限本身没问题（两个镜像 `sh -c "touch /run/test.pid"` 都 OK），
失败只发生在完整 nginx master 启动路径上——mainline 镜像内部行为与老内核不兼容。

## 首次 --frontend-only 失败现场（frontend 容器运行日志）

```
2026/09/30 13:22:01 [notice] 1#1: nginx/1.31.5      ← floating nginx:alpine 实际拉到 mainline
2026/09/30 13:22:01 [notice] 1#1: built by gcc 15.2.0 (Alpine 15.2.0)
2026/09/30 13:22:01 [notice] 1#1: OS: Linux 3.10.0-1160.119.1.el7.x86_64
2026/09/30 13:22:01 [crit] 1#1: pwrite() "/run/nginx.pid" failed (1: Operation not permitted)
→ start.sh --frontend-only 健康门 60s 超时，版本被标记 *.failed（current 未受影响，回滚机制正常）
```

## 修复

`Dockerfile.frontend` 第二阶段 `FROM nginx:alpine` → `FROM nginx:1.24-alpine`（钉 stable 线）。
修复后 `start.sh --frontend-only` 全链路绿（见 opp-verify-evidence.log / opp-verify-evidence-f.log）。
