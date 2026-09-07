# 真机验证 2026-09-08 用户配置双写 / AI 网关

- 设备：vivo V2436A `10AF6H1MLM003HF`
- 包：`com.kaixuan.opencode.pocket` versionCode 3 / 1.2.0-openpocket
- 构建：`MOBILE_FAST=1 VITE_API_BASE=https://pocket.itestu.cn node scripts/build-mobile.mjs android prod` + `assembleDebug`
- 安装：`adb install -r` Success（未用 adb reverse）
- lastUpdateTime：2026-09-08 06:15:40（含搜索占位符插值修复）

| 项 | 结果 |
|---|---|
| 冷启动进入已登录「AI 工具」 | PASS |
| 菜单 → AI 模型 打开「AI 网关」 | PASS |
| 无逗号分隔模型输入框 / 无 `#gateway-models` | PASS |
| 网关地址 `https://llm.kxpms.cn/v1` | PASS |
| API Key 已设置（留空保留） | PASS |
| 消息格式 OpenAI Chat | PASS |
| 常用模型「已选 11 个」 | PASS |
| 「测试连接」拉到目录（约 692 个，含 OpenAI 分组） | PASS |
| 搜索占位符显示「共 692 个」而非 mustache 原文 | PASS（第二包） |
| 公网 `GET /api/user-settings` 含 `llm_gateway` 11 模型 | PASS |
| 公网 `GET /api/llm-gateway/config` `apiKeySet` + kaixuan URL | PASS |

截图：`01-launch.png` `04-gateway.png` `05-test.png` `06-retest.png`
