# 邮箱配置部署到真机：一次性操作手册

目标需求：把 5 个真实邮箱的配置存到服务端（admin 名下），再同步到真机客户端本地库。
本文记录**已验证的步骤**与**仍然卡住的点**，避免下次重复排查。

## 1. 已完成并验证（2026-10-01）

### 1.1 服务端 SSOT：5 个账户，IMAP + SMTP 齐备

`POST/PUT /api/email/accounts`（admin 作用域）。部署脚本：

```bash
KAIXUAN_PW=... QQ_PW=... N163_FK_PW=... N163_FK1_PW=... N163_KH_PW=... \
  node scripts/deploy-email-accounts-to-device.mjs --base http://127.0.0.1:18099 --prune
```

实测 **13 PASS / 0 FAIL**，回读校验逐账户确认 IMAP 主机端口与 SMTP 均已落库。

> **本轮修掉的真缺口：SMTP 全部未配置。** 5 个账户原先只有 IMAP，
> `POST /api/email/accounts/{id}/test-smtp` 对每一个都返回 **400**。
> 原因是账户早于 `UpsertSMTPSettingsScoped` 的写入入口存在时创建，
> 之后没人补。目标里两个协议都给了参数，所以这是必须补的。

| 账户 | IMAP | SMTP |
|---|---|---|
| huangxutao@kxpms.cn | imap.exmail.qq.com:993 | smtp.exmail.qq.com:465 |
| 56551681@qq.com | imap.qq.com:993 | smtp.qq.com:465 |
| feikemanager@163.com | imap.163.com:993 | smtp.163.com:465 |
| feikemanager1@163.com | imap.163.com:993 | smtp.163.com:465 |
| kimmy.huang@163.com | imap.163.com:993 | smtp.163.com:465 |

**已删除**两个非目标账户（会随需求 8 同步进真机列表）：
`invoice-fixture@example.com`（IMAP 夹具）、`audit-poc@pocket-audit.test`（审计 PoC）。
它们的邮件/发票历史数据仍在库里，只是账户不再出现。

### 1.2 凭证有效性：真实 IMAP 登录 + 整轮同步

- 三个 163：`lastSyncedAt` 持续刷新，单账户 245~501ms。
- 企业微信 `huangxutao@kxpms.cn`：直连 TLS + LOGIN **698ms 返回 `a2 OK Success login ok`**，
  凭证无误。写入凭证后整轮同步从 **1m20s 降到 1.07s**。
- QQ `56551681@qq.com`：POP3 路径，440~549ms 稳定；发票已落库
  （`其他-杰赛云…-3500.00-2026-10-01.pdf`，`fileSource=pdf-url`），
  文件名符合目标的 `{费用类型}-{对方单位}-{金额}-{日期}.pdf`。

整轮（5 账户）：`POST /api/emails/sync` → **2911ms**，`{"synced":5,"new":2}`，`failed` 为空。

### 1.3 需求 8 的 LWW 下发链路无回归

`node --test src/features/email/__tests__/account-sync.test.mjs` → **5/5 PASS**
（`account-lww.ts` 是生产与测试共用的纯模块，不是复制品）。

## 2. 关键环境事实（别再走错）

### 2.1 真机连的是 **18099**，不是 8088

- `frontend/.env.android-dev` 写的是 `http://192.168.31.20:8088`；
- 但真机 App 登录页显示的后端地址是 `http://127.0.0.1:18099`，
  `wt3/scripts/start-local-backend.ps1` 的注释也明确写了这一点。
- 18099 由 `powershell -ExecutionPolicy Bypass -File scripts\start-local-backend.ps1` 启动，
  **每次真机验证前先确认它在听**（脚本注释里记了一次「后端没了导致任务列表全空、
  看起来像功能坏了」的误判）。
- 18099 实例的邮箱 master key 在 `wt3/backend/data/email_master.key`（自动生成，跨重启稳定）。
  换 worktree 起实例会生成**另一把** key，症状是所有账户
  `decrypt credential: cipher: message authentication failed`。

### 2.2 adb

- adb 路径：`C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe`（不在 PATH）。
- 设备：`192.168.31.19:5555`。

### 2.3 发票文件下载/导出 404 与 400 的根因：`dataDir` 跟着进程 CWD 跑

**症状**：在 18099 实例上
- `GET /api/emails/invoices/{id}/file` → **404**
- `POST /api/emails/invoices/export` → **400 `no harvested invoice files in selection`**

**看着像功能坏了，其实不是。** `cmd/pocketd/main.go:65` 是
`dataDir := filepath.Dir(cfg.DBPath)` —— `dataDir` 由**相对**的 `DBPath` 派生，
进程 CWD 一变目录就跟着变。发票采集时写在主工作区 `C:\workspace\openpocket\data\`，
而 18099 实例的 dataDir 指向 `wt3\logs\pocketd-data`，`os.Stat` 自然找不到。
这与 handoff §7b.5 记的 master key 问题是**同一个根因**。

**证实方式**（不是靠推理）：用同一个二进制起一个 `POCKET_DB_PATH` 指向主工作区的
隔离实例（端口 18100），同一张发票立刻正常：

```
单张下载   157,615 bytes  magic=%PDF
A4 2x2     count=1 skipped=0  invoices-a4-2x2-20261001-181824.pdf  157,061 bytes %PDF
A4 3x3     count=1 skipped=0  invoices-a4-3x3-20261001-181824.pdf  157,059 bytes %PDF
```

> **顺带记一个我自己踩过并已排除的误判**：单张导出后我查 `/MediaBox` 看到
> `595 x 396`，而代码常量 `a4HeightPt = 841.89` 是 A4 竖版，于是判「页面被裁成横版」
> —— **这是错的**。原始发票本身就是 `595.2756 x 396.8504`（横向），
> 单张时 pdfcpu 不触发网格重排，保持原尺寸是正确的。
> 多张才排成 A4 竖版，`TestExportInvoiceGrid_2x2FitsOneA4Page` 8 个用例全绿可证。
> **教训：看到尺寸与常量不符，先查被测文件本身，别直接判缺陷。**

### 2.4 A4 导出的调用姿势（两个都踩过）

- `POST /api/emails/invoices/export` 的 `grid` 在 **body** 里（`{ids:[], grid:2|3}`），
  放 query 会 400；
- `ids` **必填**，省略同样 400（`ids required`）；
- 该端点返回 JSON（含 `url`），PDF 要再 `GET <url>` 下载，不是直接返回 PDF。

## 3. 仍然卡住：adb 会话 offline（未完成）

**现象**：`adb connect 192.168.31.19:5555` 返回 `already connected`，
但 `adb devices` 始终是 `offline`；`ping` 通、`Test-NetConnection` 到 5555 也 **True**，
即 TCP 层活着、adbd 协议层不响应。`kill-server` / `start-server` / `disconnect` 后重连均无效。

**已排除**：本机 adb server 状态、端口连通性、同网段其它主机
（`192.168.31.29:5555` 存在但连接被拒，是另一台设备）。

**唯一解**：在手机上把「无线调试」关掉再打开（必要时重启手机），
让 adbd 重新监听并接受本机密钥。恢复后：

```bash
$adb='C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe'
& $adb connect 192.168.31.19:5555
& $adb devices                      # 期望 "device"，不是 "offline"
& $adb -s 192.168.31.19:5555 reverse tcp:18099 tcp:18099
```

**adb 不可用时的退路**：真机与本机在同一 WiFi（`192.168.31.19` / `192.168.31.20`），
`192.168.31.20:18099` 从局域网**实测可达**。App 侧改用
`http://192.168.31.20:18099` 即可直连后端，不需要 adb reverse。
但**装机、截图、UI 自动化仍必须 adb**，所以它终究要恢复。

## 4. 恢复后要做的事

1. `adb reverse tcp:18099 tcp:18099`，确认 App 能登录。
2. 打开 App 的邮箱设置页，触发需求 8 的账户同步（登录后自动跑一次），
   确认列表里是 **5 个**真实邮箱、没有夹具账户。
3. 截图留证：这是本目标唯一还没拿到证据的一环。
