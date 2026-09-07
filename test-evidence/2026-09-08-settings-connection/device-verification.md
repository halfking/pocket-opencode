# 真机验证 2026-09-08 设置连接去重

- 设备：vivo V2436A `10AF6H1MLM003HF`
- 包：`com.kaixuan.opencode.pocket` versionCode 3 / 1.2.0-openpocket
- lastUpdateTime：2026-09-08 04:51:06
- 构建：`MOBILE_FAST=1 VITE_API_BASE=https://pocket.itestu.cn node scripts/build-mobile.mjs android prod` + `assembleDebug`
- 安装：`adb install -r` Success（未用 adb reverse）

| 项 | 结果 |
|---|---|
| 设置「后端服务器」显示 pocket.itestu.cn 且已连接 | PASS |
| 应用信息不再出现 API 地址副本 | PASS |
| 不把 WebView origin `https://localhost` 当成 API | PASS |
| /servers 测连接 → 已连接 | PASS |
| /instances 从 pocketd 拉到列表 | PASS |
| 点 Local OpenCode 双 key 写入并进 /tasks | PASS（id=`local-opencode`） |
| 登录/解锁页展示后端基址 | PASS |

截图：`02b-settings-connection.png` `06-servers-tested.png` `10-settings-local-opencode.png`
