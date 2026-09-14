# Handoff：Pocket 四域名部署（252 边缘 + netbird 透传）

- 日期：2026-09-14
- 任务来源：/goal「检查系统部署，脚本分别部署本地与 252：252 用 pocket.kxpms.cn，本地用 pocket.itestu.cn 经 252 netbird 透传，API 走 openpocket-api 子域」
- 状态：**252 直出双域完成并验收 ✅；itestu 双域配置就绪、公网 200（暂由 252 兜底上游服务，等待 Mac 加入 mesh）**

## 一、任务概要与拓扑

```
公网用户（DNS 四域名全指 115.29.212.252）
   │ 443 = nginx stream SNI 分流（sni-proxy.conf，default → 127.0.0.1:9443）
   ▼
[252 边缘 nginx :9443，PROXY-protocol vhost]
   ├─ pocket.kxpms.cn          → 172.16.2.210:8090(API) + 127.0.0.1:4175(前端)   【252 直出】
   ├─ openpocket-api.kxpms.cn  → 172.16.2.210:8090                               【252 直出，本轮新增】
   ├─ pocket.itestu.cn         → <MAC_MESH_IP>:4175/:8090 主 + 252 兜底          【透传就绪】
   └─ openpocket-api.itestu.cn → <MAC_MESH_IP>:8090 主 + 252 兜底                【透传就绪】
                                        │
                              NetBird mesh（nb-mac-01）
                                        ▼
                              Mac 本机容器（8090→8088 pocketd / 4175→80 前端）
```

## 二、本轮改动（仓库）

| 文件 | 关键行为 |
|---|---|
| `deploy/bin/env.sh` | 252 分支端口 8092/4177 → 实况 8090/4175；注释纠正 cert-manager 只占 loopback |
| `deploy/edge/pocket.itestu.cn.conf` | 透传主上游 `__MAC_MESH_IP__` + 252 兜底 + 3s 连接超时 + `X-Pocket-Upstream` 头 |
| `deploy/edge/openpocket-api.itestu.cn.conf` | 同上（收编 252 在跑配置入库） |
| `deploy/edge/openpocket-api.kxpms.cn.conf` | 新建；upstream 252 本机；证书 openpocket-api.kxpms.cn |
| `deploy/edge/pocket.kxpms-cn-9443.conf` | upstream 内联自包含 `pocket_kxpms_api/web`（解耦原跨文件引用，防 itestu 改 mesh 误伤 kxpms） |
| `deploy/edge/apply-edge-conf.sh` | 幂等应用：渲染 → 备份 → scp → `nginx -t` 门禁 → reload，失败自动回滚 |
| `deploy/bin/README.md` | 新增「252 四域名边缘」章节 + 变更记录 |

## 三、252 服务器侧落地记录

1. **证书**：LE 从 252 直连被间歇阻断（`curl https://acme-v02.api.letsencrypt.org` 000，本机 mihomo 127.0.0.1:7890 上游也不通）。改走 **Mac manual 签发**：`certbot certonly --manual --preferred-challenges http -d openpocket-api.kxpms.cn`（auth/cleanup 钩子经 ssh 写 252 `/var/www/certbot`），有效期至 **2026-12-13**；产物 scp 至 252 `/etc/letsencrypt/live/openpocket-api.kxpms.cn/`。Mac 侧 certbot 配置在 `~/.config/letsencrypt-home/`（用户级目录，无 root）。
2. **80 ACME vhost**：新建 `/etc/nginx/conf.d/openpocket-api.kxpms.cn-80.conf`（webroot + 301），续期依赖它。
3. **四域名 vhost**：`apply-edge-conf.sh` 上线，备份在 `/etc/nginx/conf.d/backups/*.bak-20260914-*`。
4. **`.env.252`**（备份 `.env.252.bak-20260914`）：`POCKET_ALLOWED_ORIGINS` 追加 4 个 https 域，共 6 origin。
5. **pocketd 重建**：镜像 tag 已被蓝绿挪走（`docker.io/library/opencode-pocket:pocket-opp` 不存在，容器按 ID 运行）→ 先 `docker tag <运行容器镜像ID> opencode-pocket:pocket-opp` 再 `up -d --force-recreate --no-build`。**注意命名**：env.sh 的 `OPP_SERVER_NAME=252` 会把容器重建成 `-server-252`，实际部署约定是 `-server-opp`（`/opt/kaixuan/opp` 时代 basename 后缀）——必须在 `source env.sh` **之后** `export OPP_CONTAINER_SUFFIX="-opp"` 再 compose。服务器 `/opt/kaixuan/openpocket/deploy/bin/env.sh` 已同步修正版（备份 `.bak-20260914`）。
6. **重启后验证**：容器 healthy、`docker exec` 内 6 origin 生效、`172.16.2.210:8090/healthz` = ok。

## 四、验收结果（2026-09-14 实测）

| 验收项 | 结果 |
|---|---|
| 四域名 DNS → 115.29.212.252 | ✅ |
| 公网 80/443/9443/4175/8090 可达 | ✅（早前 nmap「closed」为误报） |
| `https://pocket.kxpms.cn/healthz` | ✅ 200 |
| `https://openpocket-api.kxpms.cn/healthz`（新证书，无 -k） | ✅ 200 |
| `https://pocket.itestu.cn/` | ✅ 200（0.2s，兜底 127.0.0.1:4175，响应头可见 mesh 探测切换） |
| `https://openpocket-api.itestu.cn/healthz` | ✅ 200（同上） |
| 252 → mesh IP ping | ✅ 通（~40ms）——但该 IP 当前非本机持有（见第六节） |
| 252 pocketd 容器 CORS | ✅ 6 origin |

## 五、关键事实与常用命令

```bash
# 应用/重应用边缘配置（mesh IP 变更时）
POCKET_MAC_MESH_IP=<新IP> ./deploy/edge/apply-edge-conf.sh

# mesh 链路三连测（252 上）
ping -c2 100.106.126.138
curl -m3 -o /dev/null -w '%{http_code}\n' http://100.106.126.138:4175/
curl -m3 -o/dev/null -w '%{http_code}\n' http://100.106.126.138:8090/healthz

# 252 pocketd 重建（保持 -opp 命名！）
cd /opt/kaixuan/openpocket/deploy/bin && export DEPLOY_ENV=server OPP_SERVER_NAME=252 \
  && source ./env.sh && export OPP_CONTAINER_SUFFIX="-opp" \
  && docker compose -p opencode-pocket-server-opp -f docker-compose.opp.yml up -d --force-recreate --no-build pocketd

# Mac 续期 openpocket-api.kxpms.cn（然后 scp 到 252 同路径）
certbot renew --config-dir ~/.config/letsencrypt-home --work-dir ~/.config/letsencrypt-work --logs-dir ~/.config/letsencrypt-logs
```

证书矩阵（2026-09-14）：`pocket.itestu.cn` 至 2026-12-06；`openpocket-api.itestu.cn` 至 2026-12-11；`kxpms-aiworkos-edge`（SAN: pocket.kxpms.cn + redclaw.kxpms.cn）至 2026-12-08；`openpocket-api.kxpms.cn` 至 2026-12-13。

## 六、遗留风险与下一步

1. **Mac 未入 mesh（透传未激活）**：本机无 netbird 客户端（无 daemon、无 ~/.netbird、无 GUI 安装），mesh IP 100.106.126.138 当前由其他设备/VM 持有（ping 通但 4175/8090 不通）。252 管理面容器健康（management 4h 前重启过）。**补齐步骤（需用户交互）**：
   - Mac 上：`sudo netbird service install && sudo netbird service start && netbird up --management-url https://netbird.itestu.cn`（浏览器完成登录；或向 dashboard 管理员要 setup key）；
   - 记录新 mesh IP 后：`POCKET_MAC_MESH_IP=<新IP> ./deploy/edge/apply-edge-conf.sh`（IP 未变则免）；
   - 252 上 `curl http://<MAC_MESH_IP>:4175/` 返回前端 HTML 即透传激活。
   - 走过场替代：Mac 无 sudo 时可用特权容器跑 netbird 客户端 + socat 转发（未实施）。
2. **LE 续期分裂**：252 上其他证书照常续期（等 LE 可达窗口）；`openpocket-api.kxpms.cn` 只能从 Mac 续（manual 钩子），建议给 Mac 加 launchd 定时 `certbot renew` + 续期后 scp + 远端 `nginx -t && systemctl reload nginx`。
3. **env.sh 后缀语义陷阱**：`OPP_SERVER_NAME=252` 推导的容器后缀与线上 `-opp` 不一致，下轮蓝绿部署若直接跑根级 `./deploy-252.sh` 会造出第二套 `-252` 命名容器。建议后续把 env.sh 的 252 分支后缀改回 `opp` 或在 deploy-252.sh 里显式固定。
4. **252 兜底掩盖透传断链**：itestu 在 mesh 断时自动用 252 本机容器服务（可用性优先）。以 `X-Pocket-Upstream` 响应头判别实际服务方；若要严格「本地才是真」，需去掉 backup。
5. `.env.252` / 证书 / 各备份均在服务器本地，未入仓（应如此）；仓内模板为唯一配置 SSOT，服务器手改会被下次 apply 覆盖。

## 七、引用

- 模板/脚本：`deploy/edge/`
- 上一轮 cutover 参考：`docs/2026-09-07-local-cutover/`（PLAN.md / CHECKLIST.md / nginx/）
- 252 边缘 stream 分流：`/etc/nginx/stream.d/sni-proxy.conf`（`default` → 9443，新增 kxpms 子域无需改它）
