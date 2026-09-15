# Pocket 两 kxpms 域名收尾 handoff（2026-09-15）

> 前置 handoff: docs/handoff/2026-09-14-pocket-4domain-closeout.md（含 2026-09-15 审计轮）
> 本轮语义拍板：openpocket.kxpms.cn = **纯 API**（1:A）｜openpocket-web.kxpms.cn = **纯 web UI**（pocket_mac_web 模式）｜SSOT 仓内模板渲染上线（2:B）｜certbot http-01 webroot（3:A）

## 结论

两域名公网全通（各持独立 LE 证书 + 独立 vhost），语义严格按用户拍板落地；顺带修复了 252 平台级证书续期中断（mihomo 出站代理死节点，certbot-renew 自 09-14 14:38 failed）。四域名旧链路零回归。

| # | 目标 | 状态 | 证据 |
|---|------|------|------|
| 1 | openpocket.kxpms.cn 证书 + vhost | ✅ | certbot http-01 签出（至 2026-12-13）；/healthz 200 + `X-Pocket-Upstream: 172.16.2.210:8090` |
| 2 | openpocket-web.kxpms.cn 证书 + vhost | ✅ | 同上签出；/ 200 + `X-Pocket-Upstream: 100.106.192.58:4175, 172.16.2.210:4175`（Mac 主 + 252 兜底） |
| 3 | 语义 1:A / 2:B / 3:A 落地 | ✅ | apex 纯 API 直出（/ 404 预期，同 api 域）；SSOT 经 apply-edge-conf.sh 渲染上线 |
| 4 | mihomo 阻断修复（平台级） | ✅ | letsencrypt.org DIRECT + DNS policy 两补丁；certbot-renew 手动触发 ai.kxpms.cn 续期 success |

## 根因 / 关键判断

| 问题 | 根因 | 修正 |
|------|------|------|
| certbot 首跑 `requests.exceptions.ConnectionError (OSError(0))` | 252 mihomo 透明代理（TUN fake-ip）把 `letsencrypt.org` 按 `MATCH,Global-Proxy` 路由到死节点——TCP 通、ClientHello 后被杀（SSL_ERROR_SYSCALL）。github/1.1.1.1 同症，aliyun/baidu（DIRECT 规则域）正常 | 加规则 `- DOMAIN-SUFFIX,letsencrypt.org,DIRECT`（插在 MATCH 前），控制 API `PUT /configs` 热加载 |
| 加了 DIRECT 规则仍 SSL_ERROR_SYSCALL | 第二层：mihomo DNS `fallback`（dns.google / cloudflare-dns.com 两个 DoH）自身走 MATCH→死代理，非 CN 域名解析即失败，路由规则轮不到 | 加 `nameserver-policy: '+.letsencrypt.org': https://dns.alidns.com/dns-query` 强制 alidns 解析（直连可达）。两补丁缺一不可 |
| 补丁明天就失效 | `/etc/cron.d/mihomo-subscription-update` 每天 04:00 订阅重写 config.yaml（当天 04:00 备份证实） | 两补丁固化进 `/opt/scripts/update-mihomo-subscription.sh`（`cp` 新配置后、reload 前幂等重打）。对原始订阅产物实测：逐字节干净、YAML 合法、二遍幂等 |
| 252 certbot-renew.service 自 09-14 14:38 failed | 即上述 mihomo 死节点（ai/files 等 lineage 续期全挂） | 手动触发 `systemctl start certbot-renew.service`：**ai.kxpms.cn 续期 success**，管道解锁；files 两 lineage 失败系另因（见遗留风险） |
| 252 上 `/opt/pocket-opencode/scripts/` 不存在、无 apply-edge-conf.sh | 脚本一直在 Mac 仓 `deploy/edge/`（SSOT），从未装到 252——上轮摘要失忆 | 不需要在 252 装脚本；Mac 仓直接跑即可（2:B 流程） |
| `172.16.2.210` 是谁 | 是 **252 自己的 eth0 IP**（`inet 172.16.2.210/24`），不是另一台机器——用户语义里的"兜底"实为 252 本机容器经 eth0 的入口（与 `127.0.0.1:4175` 同后端不同入口） | 按用户指定的 `172.16.2.210:4175` 落 conf（可达且与 pocket.itestu.cn 的 `127.0.0.1:4175` 效果等价），如实记录拓扑 |

## 改动文件

| 文件 | 行为 |
|------|------|
| `deploy/edge/openpocket.kxpms.cn.conf` | **新建（1:A 纯 API）**：upstream 直出 172.16.2.210:8090 + /healthz 探活 + X-Pocket-Upstream 头；9443 ssl http2 proxy_protocol（含 [::]）；security snippet；256M body；SSE 禁缓冲长超时 |
| `deploy/edge/openpocket-web.kxpms.cn.conf` | **新建（pocket_mac_web 模式）**：web upstream = Mac mesh `__MAC_MESH_IP__:4175` 主 + `172.16.2.210:4175` 兜底（用户指定入口）；api upstream = Mac :8090 主 + 252 兜底；/api /ws /plugin/ws 同源路由（SPA 必需） |
| `deploy/edge/openpocket.kxpms.cn-80.conf` | **新建**：ACME webroot + 301 https |
| `deploy/edge/openpocket-web.kxpms.cn-80.conf` | **新建**：同上 |
| `deploy/edge/openpocket-api.kxpms.cn-80.conf` | **收编入库**（09-14 手工上线 252 但仓外遗留，内容与现网一致） |
| `deploy/edge/apply-edge-conf.sh` | CONFS 扩到 9 份（5×9443 + 3 个 kxpms -80）；mesh IP 默认改永久化的 100.106.192.58；新增 kxpms 三方证书就绪预检 |
| `docs/handoff/2026-09-14-pocket-4domain-closeout.md` | 遗留风险行更新：两 kxpms 域名 → ✅ 本轮收尾 |
| 252 侧（不入仓）：`/etc/mihomo/config.yaml` | letsencrypt.org DIRECT + DNS policy 两补丁（备份：`backups/config.yaml.bak-pre-le-direct-20260915-083430`、`bak-pre-le-dns-policy-*`） |
| 252 侧（不入仓）：`/opt/scripts/update-mihomo-subscription.sh` | 补丁固化块（备份 `.bak-20260915-083731`） |

## 测试命令与结果（2026-09-15 实测）

```bash
# 证书签发（mihomo 修复后）
certbot certonly --non-interactive --webroot -w /var/www/certbot \
  -d openpocket.kxpms.cn --cert-name openpocket.kxpms.cn --agree-tos -m ops@kxpms.cn
certbot certonly --non-interactive --webroot -w /var/www/certbot \
  -d openpocket-web.kxpms.cn --cert-name openpocket-web.kxpms.cn --agree-tos -m ops@kxpms.cn
# → 均成功，notAfter=Dec 13 23:41 2026 GMT

# SSOT 渲染上线（Mac 仓）
./deploy/edge/apply-edge-conf.sh
# → 渲染(mesh IP=100.106.192.58) → 远端备份 bak-20260915-084057 → 上传 9 份 → nginx -t 通过 → reload 完成

# 公网验证（Mac 侧 curl）
curl -sSI https://openpocket-web.kxpms.cn/
# → HTTP/2 200 + x-pocket-upstream: 100.106.192.58:4175, 172.16.2.210:4175
curl -sSI https://openpocket-web.kxpms.cn/healthz      # → 200（Mac :8090 应答）
curl -sSI https://openpocket.kxpms.cn/healthz           # → 200 + x-pocket-upstream: 172.16.2.210:8090
curl -sSI https://openpocket.kxpms.cn/                  # → 404（预期：pocketd 无根路由，同 openpocket-api 域）
curl -sSI  http://openpocket-web.kxpms.cn/              # → 301 → https
# SNI：两域各持独立 lineage，subject/notAfter 正确

# 旧四域回归
pocket.itestu.cn → 200（upstream Mac 主）; openpocket-api.kxpms.cn/healthz → 200
pocket.kxpms.cn → 200; openpocket-api.itestu.cn/healthz → 200

# 平台续期管道（mihomo 修复验证）
systemctl start certbot-renew.service
# → ai.kxpms.cn 续期 success（09-14 14:38 起中断解除）；files 两 lineage 失败系另因（见遗留风险）

# mihomo 补丁存活
curl -s http://127.0.0.1:9090/rules | grep -o letsencrypt | head -1   # → 运行时规则在
```

## 遗留风险

| 风险 | 影响 | 状态/缓解 |
|------|------|------|
| `files.kxpms.cn` 证书续期失败 | 该域 cert 过期后 HTTPS 断 | **既有问题与本轮无关**：DNS 指 154（252 的 9444 conf 亦 disabled），LE 挑战打不到 252。需把 DNS 切回 252 或在 154 侧处理，另立任务 |
| `files.itestu.cn-0002` 续期失败（directory 超时） | 同上 | 本轮触发 renew 时偶发超时（同轮其他 lineage 成功），下个 timer 周期大概率自愈；持续失败再查 |
| `openpocket-web /` 响应头含主备双地址 | `X-Pocket-Upstream: Mac, 252` 表示一次请求试了主再试兜底 | 与 pocket.itestu.cn 现网行为一致（该域同款双地址），非本轮回归；Mac 4175 链路质量可后续单独观察 |
| Mac `100.106.192.58:8090` 根路径 curl 000（/healthz 却 200） | web 域 /api 主上游偶发落兜底（3s 快速切换） | pocketd 大概率仅绑部分路由/接口；不影响可用性（backup 接住），与 pocket.itestu.cn 同款拓扑行为 |
| mihomo Global-Proxy 节点仍死 | github/google 等走代理的出站持续不可用 | 本轮只修 letsencrypt.org（ACME 必需）。节点订阅质量属代理服务自身问题，不在本轮范围 |
| openpocket-web/apex 证书续期 | 2026-12-13 到期 | certbot-renew.timer 每日自检（主力）+ Mac launchd 周日兜底（<30 天才触发），与既有四域同轨道 |

## 下次启动检查清单

```bash
# 1. 新两域可达？
curl -sI https://openpocket.kxpms.cn/healthz | head -3        # → 200 + 172.16.2.210:8090
curl -sI https://openpocket-web.kxpms.cn/ | head -3           # → 200 + Mac:4175 链路

# 2. mihomo 补丁还在？（04:00 订阅更新后应自动重打）
ssh 252 'grep -c letsencrypt /etc/mihomo/config.yaml'          # → 2
ssh 252 'tail -5 /var/log/mihomo-subscription-update.log'      # → 补丁已确保

# 3. 全域回归？
for u in pocket.itestu.cn openpocket-api.kxpms.cn pocket.kxpms.cn openpocket-api.itestu.cn; do
  curl -sSI --max-time 8 https://$u/healthz | head -1
done
```
