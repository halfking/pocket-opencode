# deploy/bin — OpenPocket 目录外部化部署（本地 & 252）

Docker 部署,但**镜像文件、日志、数据、配置、启停脚本全部放在 docker 外的指定目录**,由环境变量驱动,可动态调整。

- 本地(macOS):`~/Downloads/kaixuan/opp`
- 服务器(252):`/opt/kaixuan/opp`

**后端 pocketd 在本地与 252 都部署**(前端默认随两端一起起,可用 `--backend-only` 只起后端);**数据库统一存放在 252 的 docker PG 中**(本地经 SSH tunnel 访问,见下文"数据库拓扑")。

## 目录结构(deploy 外部)

```
${DEPLOY_BASE_DIR}/              # ~/Downloads/kaixuan/opp 或 /opt/kaixuan/opp
├── data/          # 容器 /app/data 的 bind 挂载源(sqlite、上传附件等)
├── logs/          # pocketd.log / frontend.log(docker logs 拉取落盘)+ 状态时间戳
├── config/        # .env.local / .env.server(唯一容器 env 来源,含密钥,勿提交)
├── images/        # 离线镜像 *.tar.gz(save-images.sh 导出、load-images.sh 导入)
└── backup/        # 预留:sqlite/配置快照
```

## 数据库拓扑

openpocket 的**唯一权威 PG 在 252 的 docker 中**(内网 `172.16.2.210:5432`,PG 17 + Citus;252 上所有服务均为 docker 部署):

```
┌─ 252 ─────────────────────────────────────────────┐
│  docker: opencode-pocket-pocketd-server ──┐        │
│  docker: (其它 kaixuan 服务)              ├─▶ PG (docker, 172.16.2.210:5432)
└───────────────────────────────────────────┼────────┘
                                            │
┌─ 本地 Mac ────────────────────────────────┴────────┐
│  docker: opencode-pocket-pocketd-local             │
│    └─▶ host.docker.internal:15432 (宿主)           │
│           └─▶ SSH tunnel(tunnel-252.sh)─▶ 252 PG   │
└─────────────────────────────────────────────────────┘
```

- **252 上的 pocketd**:DSN 直连 `172.16.2.210:5432`,容器经 bridge 出网即可,无需特殊网络。
- **本地 pocketd**:252 的 PG 不对公网开 5432,必须走 SSH tunnel(`tunnel-252.sh` 管理,宿主 `localhost:15432` → 252 内网 PG;容器内 DSN 用 `host.docker.internal:15432`,compose 已配 host-gateway)。
- 默认库/用户/schema:`pocket` / `llm_gateway` / `opencode_pocket`。2026-08-31 经 tunnel 实测确认:252 PG(PG 17.10)已有专用 `pocket` 库,其 public schema 里有一套**零数据**的旧空表(users/tasks/notes/emails 等,早期尝试遗留);本次部署不用它,表由后端 migration 建在 `opencode_pocket` schema,与库内其它对象隔离。public 旧空表可在稳定后手工清理。
- **密码不入库**:生成 `.env.local` 时用 `OPP_PG_PASSWORD=<密码> ./deploy/bin/deploy-local.sh` 注入,或手工编辑 `.env`(在外部目录,不会进 git)。
- tunnel 凭据:推荐 `ssh-copy-id -p 25022 root@115.29.212.252` 配好 SSH key 免密;或 `SSHPASS` 环境变量 + sshpass。

## 快速开始

### 本地

```bash
# 0) 首次:建 SSH tunnel 到 252 PG(需 key 或 SSHPASS + sshpass)
./deploy/bin/tunnel-252.sh up

# 1) 一键部署(建目录 + 生成 .env.local[DSN 指向 252 PG] + tunnel 检查 + 启动)
OPP_PG_PASSWORD=<252的PG密码> ./deploy/bin/deploy-local.sh
./deploy/bin/status.sh                # 容器状态 + /healthz + 目录占用
./deploy/bin/logs.sh                  # docker logs 拉取落盘到 logs/*.log
./deploy/bin/stop.sh                  # 停止(保留数据)
```

`deploy-local.sh` 每次启动前都会检查 tunnel,未就绪则自动尝试建立。生成的 `.env.local`(权限 600)已含随机 JWT 密钥与 252 PG 的 DSN;密码若为占位符,重跑 `OPP_PG_PASSWORD=<密码> ./deploy/bin/deploy-local.sh` 会自动替换 DSN 行。

- 换端口(8088/4175 被占时):`POCKET_HTTP_PORT=8090 POCKET_FRONTEND_PORT=4176 ./deploy/bin/deploy-local.sh` —— 端口是环境变量,不写进 .env(避免死配置)
- 只起后端:`./deploy/bin/start.sh --backend-only`

### 252 服务器(所有脚本都要带 `DEPLOY_ENV=server`,否则默认按 local 解析路径)

```bash
# ① 本地构建 amd64 镜像并导出(252 是 x86_64;build-images.sh 走宿主机
#    交叉编译:go 产静态二进制 + 前端宿主机构建 dist,再打纯 COPY 镜像,
#    不依赖 arm64-only 的 kx-base,也不用模拟器编译,见 deploy/docker/)
./deploy/bin/build-images.sh --arch amd64
./deploy/bin/save-images.sh

# ② 传输(镜像默认导出到本地 ~/Downloads/kaixuan/opp/images/)
scp ~/Downloads/kaixuan/opp/images/*amd64*.tar.gz user@252:/opt/kaixuan/opp/images/
#    252 上需要本仓库脚本:仅 load/start(不 build)最少要 deploy/bin + deploy/docker
scp -r deploy/bin deploy/docker user@252:<repo-path>/deploy/

# ③ 252 上:建目录 → 放镜像 → 加载 → 填配置 → 启动
sudo DEPLOY_ENV=server ./deploy/bin/init-dirs.sh        # 建 /opt/kaixuan/opp/{data,logs,config,images,backup}
mv /tmp/*.tar.gz /opt/kaixuan/opp/images/
sudo ./deploy/bin/load-images.sh                        # 默认 DEPLOY_ENV=server,可不带
sudo vi /opt/kaixuan/opp/config/.env.server             # 必填项见下
sudo DEPLOY_ENV=server ./deploy/bin/deploy-252.sh       # 自动校验 PG 可达 + 生产门禁后启动(透传 --backend-only)

# 运维(同样带 DEPLOY_ENV=server)
sudo DEPLOY_ENV=server ./deploy/bin/status.sh
sudo DEPLOY_ENV=server ./deploy/bin/logs.sh
sudo DEPLOY_ENV=server ./deploy/bin/stop.sh             # 默认保留数据;数据在 bind mount,--volumes 删不到 data/
```

`.env.server` 必填项(缺任一会被 deploy-252.sh 拒绝):

```
POCKET_ENV=production
POCKET_DEV_AUTH=false
POCKET_JWT_SECRET=<≥32字节随机>            # 后端 Validate 生产强制
POCKET_ALLOWED_ORIGINS=https://<前端域名>   # 后端 Validate 生产强制
POCKET_POSTGRES_DSN=postgresql://llm_gateway:<密码>@172.16.2.210:5432/pocket?sslmode=disable
POCKET_PG_SCHEMA=opencode_pocket
```

### 252 网络注意事项(mihomo 透明代理)

252 上 systemd 的 `mihomo.service` 起 Meta TUN 并注入 `ip rule`(pref 9000+),会把**所有非本机进程发起的转发流量**(即容器对外回包)吸进代理隧道丢弃——症状:容器端口从公网/外部访问 SYN 到达但零回包(超时),而主机本机 curl 正常。**不是安全组问题**(tcpdump eth0 有 SYN 无 SYN-ACK 即可确诊)。

修复:更高优先级的直连规则 + systemd 持久化(252 上已配置):

```bash
ip rule add pref 8999 from 10.89.7.0/24 lookup main   # opp-server-net 固定子网(CNI conflist)
# 持久化单元: /etc/systemd/system/opp-direct-egress.service(oneshot 幂等,开机自动恢复)
```

代价:该网段容器出网不再经 mihomo 代理(当前 pocketd 只连 VPC 内 PG,无影响;若未来容器需代理出网需重新评估)。

## 脚本清单

| 脚本 | 作用 |
|---|---|
| `env.sh` | 环境变量中心(被其余脚本 source,勿直接执行) |
| `init-dirs.sh` | 幂等创建 data/logs/config/images/backup 子目录 |
| `deploy-local.sh` | 本地一键部署入口(默认 `~/Downloads/kaixuan/opp`,DSN 指向 252 PG) |
| `deploy-252.sh` | 252 部署入口(默认 `/opt/kaixuan/opp`,root 校验 + PG 可达检查 + 生产门禁) |
| `tunnel-252.sh` | 本地→252 PG 的 SSH tunnel 管理(up/down/status) |
| `build-images.sh` | 从最新源码正式构建两镜像(宿主机 go 交叉编译 + 宿主 npm 构建 dist,配 `deploy/docker/*-prebuilt` Dockerfile,支持 amd64/arm64) |
| `start.sh` | 启动;自动判断 build/no-build(offline-first);`--backend-only` 只起后端 |
| `stop.sh` | 停止;`--volumes` 才会删数据卷(有确认) |
| `status.sh` | compose ps + /healthz + 目录占用 |
| `logs.sh` | 拉取日志落盘 / `--follow` 跟随 / `--rotate` 轮转 |
| `save-images.sh` | 导出镜像到 `images/`(gzip,带 arch+时间戳) |
| `load-images.sh` | 加载 `images/` 下全部离线镜像(`--latest` 只加载最新) |
| `docker-compose.opp.yml` | 本地/252 共用的独立 compose |

## 环境变量

输入(执行前设置即生效):

| 变量 | 默认 | 说明 |
|---|---|---|
| `DEPLOY_ENV` | `local` | `local` / `server`,决定默认根目录与 project 名 |
| `DEPLOY_BASE_DIR` | 本地 `~/Downloads/kaixuan/opp`;server `/opt/kaixuan/opp` | 顶层根目录,覆盖默认值 |
| `POCKET_DATA_DIR` 等 | `${DEPLOY_BASE_DIR}/<sub>` | 单独覆盖某个子目录 |
| `POCKET_HTTP_PORT` | `8090` | 后端宿主端口(8088 已弃用;2026-08-31 定稿) |
| `POCKET_FRONTEND_PORT` | `4175` | 前端宿主端口 |
| `POCKET_PORT_BIND_IP` | 本地 `0.0.0.0`;server `172.16.2.210` | 宿主端口绑定 IP(252 的 127.0.0.1:8090 被 kxpms-cert-manager 占用,须绑 eth0 IP;健康探测地址自动跟随) |
| `OPP_IMAGE_TAG` | `pocket-opp` | 镜像 tag(save/load/compose 共用) |
| `OPP_NET_NAME` | `opp-<env>-net` | compose 网络名 |
| `OPP_NET_EXTERNAL` | `false` | `true` 时并入既有外部网络(见下) |
| `POCKET_ENV_DEBUG` | `0` | `1` 时 env.sh 打印全部生效路径 |
| `OPP_PG_HOST` | local `host.docker.internal`;server `172.16.2.210` | 252 docker PG 地址 |
| `OPP_PG_PORT` | local `15432`(tunnel);server `5432` | PG 端口 |
| `OPP_PG_DB` / `OPP_PG_USER` | `pocket` / `llm_gateway` | 库名/用户(252 专用 pocket 库) |
| `OPP_PG_SCHEMA` | `opencode_pocket` | openpocket 表所在 schema |
| `OPP_PG_PASSWORD` | 无 | 生成 `.env.local` 时注入 DSN 密码(不入库) |
| `OPP_252_SSH_HOST/PORT/USER` | `115.29.212.252`/`25022`/`root` | tunnel-252.sh 的 SSH 目标 |

派生输出(由 `env.sh` 导出,脚本/compose 共用):`POCKET_ENV_FILE`、`POCKET_COMPOSE_FILE`、`POCKET_PROJECT_NAME`、`POCKET_HOST_PORT`(acc 命名别名)等。

## 与 acc-integration 的关系

`deploy/acc-integration/`(local-up.sh 等)是**开发期 acc 联调栈**,依赖其目录内未跟踪的 `.env` 与 `acc-local-net`/`shared-infra` 外部网络,保持原样不动。

本目录是**目录外部化的运行部署**,自带 `docker-compose.opp.yml` + 自建网络,不依赖 acc-integration 的任何本地文件。两者可并存(容器名带 `-local`/`-server` 后缀区分)。

如需让本部署加入 acc 联调网络:

```bash
OPP_NET_EXTERNAL=true OPP_NET_NAME=acc-local-net ./deploy/bin/start.sh
```

(要求网络已存在;compose 不会代建 external 网络。)

## 设计说明

- **日志**:pocketd 目前日志只写 stderr(后端无文件日志配置),`logs.sh` 负责从 `docker logs` 拉取追加到 `logs/pocketd.log`;compose 已把 `/var/log/pocketd` 挂到外部 `logs/`,后端未来支持文件日志时无需改 compose。
- **数据**:`/app/data` 由命名卷改为 bind 挂载 `data/`,容器重建不丢数据;`stop.sh` 默认不删任何数据。业务数据(任务/笔记/邮件/用户等)统一存 252 docker PG 的 `opencode_pocket` schema,本地与 252 的后端连同一个库。
- **镜像**:`pull_policy: never`,完全 offline-first;`start.sh` 自动判断"已有镜像直接跑 / 缺镜像且有 kx-base 则现场构建 / 都没有则报错指路"。
- **生产门禁**(deploy-252.sh):`/opt`、`/srv` 下必须 root;`.env.server` 必须存在且 `POCKET_ENV=production`;`POCKET_DEV_AUTH=true` 拒绝启动。

## 变更记录

- 2026-08-31(五):端口定稿与生产凭据切换——8088 全面弃用,`POCKET_HTTP_PORT` 默认改 8090;新增 `POCKET_PORT_BIND_IP`(默认本地 0.0.0.0/server 172.16.2.210,252 的 127.0.0.1:8090 被 kxpms-cert-manager 占用,pocketd 绑 eth0 IP 规避,compose 端口映射与 start/status 健康探测同步适配);admin 密码随机化(bcrypt 直接更新共享库 `opencode_pocket.users`,新值记入两端 env 文件 `POCKET_AUTH_PASS`,生产鉴权走 DB 哈希不受影响)。公网 8090/4175 打通——安全组本就放行,真正根因是 252 的 mihomo Meta-TUN 策略路由吞掉容器对外回包,以 `ip rule pref 8999 from 10.89.7.0/24 lookup main` + systemd `opp-direct-egress.service` 持久化解决(详见"252 网络注意事项")。
- 2026-08-31(四):新增 `build-images.sh` + `deploy/docker/{Dockerfile.pocketd-prebuilt,Dockerfile.frontend-prebuilt}` 正式构建链路——宿主机 go 交叉编译静态二进制(GOOS=linux GOARCH=amd64/arm64,CGO_ENABLED=0)+ 宿主 npm 构建 dist(架构无关),runtime 镜像纯 COPY(基础镜像按 `--platform` 由 registry 解析),彻底绕开"amd64 需模拟器编译"与"kx-base 离线包仅 arm64"两个限制;镜像打 OCI label(revision/created/version)供审计。当日以该链路完成 252 首次实机部署(amd64 镜像 save→scp→load,`/opt/kaixuan/opp` 落地,双容器 healthy,生产模式真实登录+鉴权 API+前端全通,公网 8088/4175 待阿里云安全组放行,内网 172.16.2.210 可用)与本地 arm64 重建切换。
- 2026-08-31(三):审计修复(P0×1/P1×6/精选 P2)——load-images.sh 默认 `DEPLOY_ENV=server`+空数组保护+mtime 选最新;logs.sh `--follow` 空 service 修复、`--rotate` 尊重 `--service`;deploy-local.sh 占位密码重跑自动重注入 DSN、`.env.local` chmod 600、DSN 行 printf 写入防 shell 展开、模板不再写端口(端口统一环境变量);start.sh `--backend-only` 镜像判定、env file 缺失提示 DEPLOY_ENV=server、curl/wget 双探测、amd64 机器禁 arm64 kx-base 构建;stop/status/logs 同步 252 提示;deploy-252.sh 门禁去引号/去 CRLF、透传参数;init-dirs.sh data/images 自忽略+root 属主提示;tunnel-252.sh 加 StrictHostKeyChecking=accept-new;README 252 流程补 DEPLOY_ENV=server/init-dirs/必填项。
- 2026-08-31(二):按部署定稿更新——后端 pocketd 部署于本地 + 252;数据库统一为 252 docker 中的 PG(经 tunnel 实测 PG 17.10,选定既有专用 `pocket` 库 + `opencode_pocket` schema);本地经 `tunnel-252.sh` SSH tunnel 访问,252 直连 `172.16.2.210:5432`;`deploy-local.sh` 生成 DSN 指向 252 并在启动前检查/自动建立 tunnel;`deploy-252.sh` 增加 PG 可达性检查与 DSN 必填校验;`start.sh` 新增 `--backend-only`。
- 2026-08-31(一):初版。新增 `deploy/bin/` 目录外部化部署体系(env/init-dirs/deploy-local/deploy-252/start/stop/status/logs/save-images/load-images + docker-compose.opp.yml);acc-integration 与本地方案栈不受影响。
