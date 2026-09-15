# Pocket 四域名部署收尾 handoff (2026-09-14)

> 前置 handoff: docs/handoff/2026-09-14-pocket-4-domain-deploy.md (755e140)
> 本轮 commit: e99b029 (env.sh -opp 统一) + 2ef05e2 (edge 脚本三件套)

## 结论

四域名公网全通，mesh 透传激活，证书续期 launchd 就绪。四项验收全部通过实测证据闭环。

| # | 目标 | 状态 | 证据 |
|---|------|------|------|
| 1 | Mac 入 mesh + apply-edge-conf.sh | ✅ | netbird Connected IP=100.106.192.58; 252 上游已指向 |
| 2 | 252 curl mesh:4175 + 公网 X-Pocket-Upstream | ✅ | 252 curl → HTTP 200 1453B; 公网头 = 100.106.192.58:4175 |
| 3 | env.sh 252 容器后缀统一 -opp | ✅ | L105=OPP_NAME_SUFFIX="opp" (e99b029 已落); 252 容器全 -opp 后缀 |
| 4 | Mac openpocket-api.kxpms.cn 证书 launchd 续期 | ✅ | plist 已加载 (Sun 03:30); 脚本重写为远端模式; 手跑 89天余量 exit 0 |

## 根因 / 关键判断

| 问题 | 根因 | 修正 |
|------|------|------|
| 252 nginx 上游引用 100.106.192.58 | 该 IP 是 Mac 的 netbird mesh IP——上一轮 Mac 已入 mesh 并跑过 apply-edge-conf.sh | 确认无需重跑，链路已通 |
| Mac certbot renew 不可能成功 | openpocket-api.kxpms.cn DNS 指向 252，HTTP-01 挑战打到 252；Mac 本地永远无法创建 cert lineage | 重写脚本为远端模式：SSH 到 252 检查到期天数→<30天时远端 certbot renew + nginx reload；Mac 仅作定时触发器 |
| env.sh 252 分支"需要修" | 摘要残留过时信息——e99b029 已统一 server 模式后缀为 -opp | 无需改，确认现状即可 |
| apply-edge-conf.sh 0644 无执行权限 | 上轮跑通但权限改动未 commit | chmod +x + 本轮 commit 固化 |

## 改动文件

| 文件 | commit | 行为 |
|------|--------|------|
| `deploy/edge/apply-edge-conf.sh` | 2ef05e2 | chmod 0644→0755（幂等上线 252 四域名 nginx conf） |
| `deploy/edge/renew-openpocket-api-cert.sh` | 2ef05e2 | **全文重写**：Mac certbot renew+scp → SSH 远端检查到期天数→<30天触发远端 certbot renew+nginx reload。去掉本地 certbot/lineage/scp 依赖 |
| `deploy/edge/verify-mesh-edge.sh` | 2ef05e2 | 入库：四步验证（mesh 端口探活 + SSH 252 本机 + 公网头匹配） |
| `deploy/bin/env.sh` | e99b029 | server 模式 `OPP_NAME_SUFFIX="opp"` 统一（防蓝绿双套命名） |
| `~/Library/LaunchAgents/com.pocket.openpocket-api.cert.renew.plist` | (不入仓) | launchd 周日 03:30 触发 renew 脚本，日志 `~/Library/Logs/pocket-cert-renew.log` |

## 测试命令与结果

```bash
# mesh 贯通
ssh -p 25022 root@115.29.212.252 'curl -sI http://100.106.192.58:4175/'   # → HTTP 200
curl -sI https://pocket.itestu.cn/ | grep x-pocket-upstream              # → 100.106.192.58:4175

# 证书续期看门狗（手跑）
./deploy/edge/renew-openpocket-api-cert.sh
# → SSH 通 + 89天余量 + "证书未到期，跳过续期" + exit 0

# launchd 加载确认
launchctl list | grep com.pocket.openpocket-api.cert.renew               # → -  0  (loaded, exit 0)

# env.sh 后缀
source deploy/bin/env.sh; echo $OPP_NAME_SUFFIX                          # → opp (DEPLOY_ENV=server 时)
```

## 遗留风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| Mac netbird session 16h 后过期 | mesh 断 → 公网 pocket.itestu.cn 502 | `netbird up` 重新注册；或改用 setup key 免交互永久注册 |
| pocket-opencode 工作树有并行会话 merge 冲突 | 阻塞常规 git commit | 已用临时 index (GIT_INDEX_FILE) 绕过；冲突解决后可正常操作 |
| openpocket-api.kxpms.cn 返 404 | 后端 API 根路径无路由，非 404 页面错误 | 预期行为——API 需指定路径（如 /api/v1/...）；/healthz 返 200 |
| 252 certbot-renew.timer 与 Mac launchd 续期脚本双轨 | 252 timer 每日自检（主力），Mac 每周日兜底看门狗 | 不冲突：Mac 脚本 <30 天才触发，252 timer 提前续了则 Mac 跳过 |

## 下次启动检查清单

```bash
# 1. netbird 在线？
netbird status | grep -E 'NetBird IP|Management'

# 2. 公网四域名可达？
curl -sI https://pocket.itestu.cn/ | head -5
curl -sI https://openpocket-api.kxpms.cn/healthz | head -3
curl -sI https://pocket.kxpms.cn/ | head -5

# 3. mesh 端到端？
ssh -p 25022 root@115.29.212.252 'curl -sI http://100.106.192.58:4175/' | head -3

# 4. 252 容器健康？
ssh -p 25022 root@115.29.212.252 'docker ps --filter name=opencode-pocket --format "{{.Names}} {{.Status}}"'
```

---

# 审计轮（2026-09-15）

> 本轮 commit: 5609ca7 (stash 冲突清算) + 审计提交（见 git log）
> 上轮遗留三项全部处置：netbird 永久化 ✅ / stash 冲突清算 ✅ / 两个 kxpms 域名 = 评估完毕待语义拍板

## 结论

上轮遗留风险 ①② 已闭环；审计另发现并修正 3 处缺陷（1 处本轮自己引入、2 处上轮入库即坏）。四域名贯通验证脚本修复后 4/4 ALL PASS。

## 根因 / 修正

| # | 问题 | 根因 | 修正 |
|---|------|------|------|
| 1 | **Mac netbird 每 24h 强制重注册**（上轮风险①） | Mac peer（XUTAOdeMacBook-Pro.local）是全网 11 个 peer 里唯一 `login_expiration_enabled=true` 的——账号默认策略 86400s 只对它生效 | 管理 API `PUT /api/peers/<id>`（必须带 `name`，否则 500）置 `login_expiration_enabled=false`。零停机：IP 保持 100.106.192.58，`Session expires` 行消失。另建 never-expires reusable key `workstations-permanent-20260915`，明文存 `252:/opt/netbird/openpocket/setup-key-workstations-permanent`，重注册：`netbird up --management-url https://netbird.itestu.cn --setup-key <KEY>` |
| 2 | **工作树 13 文件 UU 冲突阻塞 git 操作**（上轮风险②） | 不是 merge 冲突：7-16 浏览器 QC WIP（stash@{0}）pop 到 studio 主线上的 stash-pop 冲突，且前会话半解决留了坏档（SettingsLLMGateway 多余 `</div>` 致 vue 模板编译失败；router 重复 meeting-record 路由） | 5609ca7：12 文件取 HEAD（studio/approvalsRuntime/RoundTimeline/useMicPermission 新架构已覆盖 stash 意图），7 文件落地 stash 有效增量（BottomSheet model-value 迁移、DbLockedState 文案、meetings.ts fromFallback、依赖 jeep-sqlite/sql.js/playwright 进 jspdf/jszip 出、lock 重生成）。stash@{0} 保留作旧版 UI 存档 |
| 3 | **main.ts 首屏回归**（5609ca7 引入，审计发现） | 合并时把 jeep-sqlite 初始化（15s 超时）挡在 `useThemeStore`+`mount` 之前——HEAD 明确要求皮肤"首帧前"应用避免暗色闪白；且 local-db.ts 开库路径内部已 `await initSqliteWeb()+initWebStore()`，前置初始化纯冗余 | main.ts 整体还原 2ef05e2 原版。教训：**Web 端 sqlite 初始化的 SSOT 在 local-db.ts 开库路径，任何入口层预初始化都不要加** |
| 4 | verify-mesh-edge.sh 在当前 Mac 上必 exit 4（上轮入库即坏） | netbird >=0.78 的 `status --json` 已无 `.localPeerState`，本机地址在顶层 `.netbirdIp`（CIDR 带 `/16` 后缀） | 取值改 `.netbirdIp // .localPeerState.ip // .localPeerState.fqdn` + 去 CIDR 尾 |
| 5 | verify-mesh-edge.sh step2 必超时（同上） | 在 Mac 上 curl **自己的** mesh IP——netbird userspace 接口不 hairpin；且回源方向应是 252→Mac | step2 改为 SSH 到 252 探 `Mac:8090/healthz + :4175/`（nginx upstream 实际方向） |
| 6 | verify-mesh-edge.sh step3 8090 假阳性 | 252 上 `127.0.0.1:8090` 是无关 python 进程；pocket 兜底容器绑 `172.16.2.210`；且 API 根路径 404 是预期（陷阱③） | step3 探 `172.16.2.210:8090/healthz`；8090 一律用 /healthz 探活 |

## 测试命令与结果（2026-09-15 实测）

```bash
npm run build                                   # vue-tsc --noEmit + vite build ✓ built in 2.91s
./deploy/edge/verify-mesh-edge.sh               # 4/4 ALL PASS, exit 0（修复后首跑）
netbird status | grep -i session                # 无 Session expires 行（永久化生效）
curl -s https://openpocket-api.kxpms.cn/healthz # 200（kxpms 侧同验）
```

## 遗留风险（更新）

| 风险 | 影响 | 状态/缓解 |
|------|------|------|
| openpocket.kxpms.cn / openpocket-web.kxpms.cn | DNS 已指 252 但无证书无 vhost，HTTPS 落默认 server | **✅ 2026-09-15 收尾完成**：语义已拍板（apex=纯 API 直出 172.16.2.210:8090；web=UI pocket_mac_web 模式 Mac :4175 主 + 172.16.2.210:4175 兜底），双证双 vhost 上线全通；顺带修复 mihomo letsencrypt 阻断。见 docs/handoff/2026-09-15-pocket-kxpms-apex-web-closeout.md |
| stash@{0} | 含已废弃的旧版 MeetingDetail/Record UI | 保留作存档；确认不要可 `git stash drop` |
| 工作树未追踪文件 | docs/handoff(本文件)、frontend/.scratch/、material-symbols-outlined.ttf(HEAD 用 woff2)、features/sessions/components/JsonTreeView.vue(零引用孤儿) | 未清；JsonTreeView/ttf 确认无用可删 |
| netbird 管理 token / 管理员凭据明文 | /root/myvpn/scripts/netbird-config/ 脚本内含明文凭据 | 既有现状；如需收敛改用 service-user + token 轮换 |
