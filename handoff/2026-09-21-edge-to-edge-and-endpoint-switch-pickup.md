# 30 秒验证：全屏背景 + 端点切换（2026-09-21）

> 给任何已装 APK 在手的人（你 / 同事 / 你妈拿的新手机）的接力卡 —— **30 秒读完即可上手**。

## 0. 前置

APK 已经构建并 install 到你的 Android 13+ 真机：

```
路径：frontend\android\app\build\outputs\apk\debug\app-debug.apk
SHA256：9B768E6D2AD0962F8D351C686F28A6F07BD415C51FC1029E7CC3C9A0844C83DF
大小：29.0 MB
package：com.kaixuan.opencode.pocket
入口：com.kaixuan.opencode.pocket.MainActivity
```

装机命令（adb）：

```bash
adb install -r frontend\android\app\build\outputs\apk\debug\app-debug.apk
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

## 1. 全屏背景 30 秒验证

**操作**：直接打开 app。

**看到的对**：
- 主页面顶部**没有白色条**
- 下拉通知时整个状态栏区是 app 主题色（亮色 / 深色 都跟主题匹配，**不再有白色或黑色细边**）
- 底部虚拟导航区也是 app 主题色

**看到的错**（如果有，告诉我们）：
- 顶部有一道**与主题色不同的横条**（通常白条或黑条）—— 那就翻 `capacitor.config.ts` 看 `StatusBar.overlaysWebView: true` 是否还在
- app 还在 `overlaysWebView: false` 老 build —— 重装一把

## 2. 端点切换 30 秒验证

**操作**（按顺序点 5 下）：
1. 打开 app
2. 顶栏最左 ≡ → 抽屉
3. 抽屉里 "设置"组 → "后端服务器"
4. 看到 4 个选项：
   ```
   ┌──────────────────────────────────────┐
   │  构建默认                              │
   │  当前站点（同源）                        │
   │  生产环境（pocket.itestu.cn）  ← 默认  │
   │  备用入口（pocket.kxpms.cn）    ← 新   │
   │  自定义地址                            │
   └──────────────────────────────────────┘
   ```
5. 点「备用入口（pocket.kxpms.cn）」 → 「保存并使用」

**期望**：
- 自动跳到 /login 页，之前的登录 session 被注销（防跨节点态错乱）
- 登录成功表示 `pocket.kxpms.cn` 联得上

**来回切换**：再回设置 → 切回「生产环境（pocket.itestu.cn）」→ 保存并使用 → 再次登录成功。

## 3. 出错怎么办

| 现象 | 可能 | 修法 |
|---|---|---|
| 没看到 4 个选项 | 装的是老 APK | 重装 `app-debug.apk`（SHA `9B768E6D2A…`）|
| 只有 3 个选项 | dist 没编译进新版 i18n | `npm run build:fast && npx cap sync android && gradlew assembleDebug` 重做一遍 |
| 点保存后没跳 /login | `saveAndUse()` 没生效 | 看 logcat：`adb logcat -d --pid=$(adb shell pidof com.kaixuan.opencode.pocket)` 搜索 `persistApiBase` |
| 登录始终失败 | 网络或后端未连通 | 浏览器访问 `https://pocket.itestu.cn/healthz` 或 `https://pocket.kxpms.cn/healthz` 看是否 `ok` |

## 4. 完整验证证据位置

- **代码**：commit `3fad123` + `92b4e13`（main HEAD）
- **APK**：`frontend\android\app\build\outputs\apk\debug\app-debug.apk`
- **截图**：`test-evidence\real-device-2026-09-21\01-home.png` / `02-after-deep.png` / `03-relaunched.png` / `emul-01-home.png`
- **验证文档**：`docs\audits\2026-09-21-server-endpoint-rotation.md`
- **bundle 编译证据**：`dist\assets\*.js` 含 `backupServer: "备用入口（pocket.kxpms.cn）" / "Backup (pocket.kxpms.cn)"` 两语言

---

**写于**：2026-09-21 11:46 · commit `92b4e13` 之上
**作者**：Mavis / mavis orchestrator
**取舍**：30 秒验证不需要你读 runbook，不需要你装 adb 工具，**只要手机 + 你 5 次点**。
