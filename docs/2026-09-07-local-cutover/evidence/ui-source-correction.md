# UI 源纠正（2026-09-08）

真机上一包打的是 `pocket-opencode` 工作区（emoji 底栏、无「对话」Tab），看起来像旧壳。

最新产品 UI 在 `ai-native-tools/openpocket/frontend`：

- 底栏 5 Tab：AI / 对话 / 笔记 / 会议 / 邮箱（Material Symbols）
- 顶栏 ≡ 抽屉：PKM、市场、成本、网关、定时自动化等
- 独立 `/ai-chat`（豆包式多轮 / 对比 / 角色）
- 页脚：Redclaw · v1.2.0

已把该 dist 写进本机 frontend 容器；`https://pocket.itestu.cn/` 主包为 `index-BP4t7LQW.js`。

Android APK：`openpocket/frontend/android/app/build/outputs/apk/debug/app-debug.apk`（versionCode 3），已 `adb push` 到 `/data/local/tmp/pocket-latest.apk`。vivo USB 安装卡在系统确认，需真机点允许。
