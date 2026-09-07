# 真机验证 2026-09-08 收件箱归类 / 伪删除 / 委托收信

- 设备：vivo V2436A `10AF6H1MLM003HF`
- 包：`com.kaixuan.opencode.pocket` versionCode 3 / 1.2.0-openpocket
- lastUpdateTime：2026-09-08 07:46:34（首装）；二次覆盖安装含 API base 修复
- 构建：`MOBILE_FAST=1 VITE_API_BASE=https://pocket.itestu.cn node scripts/build-mobile.mjs android prod` + `assembleDebug` + `adb install -r`
- 后端：`./deploy-local.sh --build --backend-only` → pocketd `6fe851d`（classify/purge 已进镜像）
- 分支：`feat/email-inbox-classify-fetch`（`449e4c4` 已 push）

| 项 | 结果 |
|---|---|
| `POST /api/emails/classify` / `purge` / `sync` 无 token → 401（路由存在） | PASS |
| `https://pocket.itestu.cn/healthz` → ok | PASS |
| 真机解锁后进入「邮箱」：搜索 / 归类 / 删除 / 更多 | PASS |
| Chip：全部 / 未分类 / 重要 / 工作 / 账单 / 私人 / 通知 / 广告 / 垃圾 | PASS |
| 点「搜索」出现发件人/标题/关键字与日期栏 | PASS |
| 点「更多」：发票整理 / 清理垃圾 / 邮箱设置 | PASS |
| 点「归类」出现「正在归类 1/229 取消」（服务端有未分类邮件） | PASS |
| 随后列表区出现 `Failed to fetch`（后续批次或列表拉取中断） | 记录 |
| 同页 WebView `GET /api/emails?limit=2` → 200，2 封 | PASS（接口可达） |
| 本地列表仍为空骨架（增量拉列表未上屏） | 记录 |
| IMAP 不在 WebView：收信走 pocketd / 原生 `EmailFetch` HTTP | 架构 PASS |

截图：`03-email-tab.png`（解锁）`10-inbox-after-fix.png`（骨架+chip）`11-classify.png`（搜索栏）

未做完：伪删除勾选一封并打开「正文已清除」详情（列表未上屏）。
