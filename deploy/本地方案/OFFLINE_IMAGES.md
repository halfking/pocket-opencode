# 离线镜像清单与运行规则

## 运行规则

`local-up.sh` 是离线运行入口：

- 不执行 `docker pull`
- 不执行 Docker build
- 使用 `docker compose --pull never ... up -d --no-build`
- 缺少本地镜像时立即失败，不访问 Docker Hub

代码变更后需要显式构建时执行：

```bash
./deploy/本地方案/local-build-images.sh
```

该脚本使用宿主机 Go 编译 ARM64 binary，并以 `--pull=false --network=none` 构建 runtime 镜像。前端构建需要已有 npm cache/node_modules；若 npm cache 不完整，应先在允许联网的构建环境生成前端镜像并 `docker save`。

## 当前已验证的镜像

| 镜像 | 架构 | 用途 |
| --- | --- | --- |
| `opencode-pocket:pocket-local` | arm64/linux | Pocket runtime |
| `opencode-pocket-frontend:pocket-local` | arm64/linux | Nginx frontend |
| `alpine:3.20` / `alpine:latest` | arm64/linux | runtime 基础镜像 |
| `node:22-bookworm-slim` | arm64/linux | frontend builder |
| `nginx:alpine` | arm64/linux | frontend runtime |
| `kx-go-service-base:latest-runtime` | arm64/linux | 可选 Go runtime 基础镜像 |
| `kx-base:go-vue` | arm64/linux | 可选 Go/Vue builder 基础镜像 |

镜像架构以 `docker image inspect` 实际输出为准，不以 tar 包 manifest 文档为准。

## 镜像导入

从本地镜像包导入示例：

```bash
docker load -i ~/work/docker-base-images/v2-saved/<image>.tar.gz
```

导入后必须确认：

```bash
docker image inspect <image> --format '{{.Architecture}}/{{.Os}}'
```

不要使用 amd64 镜像替代 arm64 本地运行镜像。

## 仍需注意

根 `Dockerfile` 和 `Dockerfile.frontend` 是联网环境下的多阶段构建定义；离线日常部署不要调用根 `deploy/build-image.sh`，使用本目录的 prebuilt image 流程。
