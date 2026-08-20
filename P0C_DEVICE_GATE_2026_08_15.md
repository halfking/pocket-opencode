# P0-C 设备门验证报告（2026-08-15）

## 结论：✅ 通过（附 1 项真实 bug 修复 + 2 项环境备注）

`frontend/android/`（Capacitor 工程事实源）构建的 debug APK 在 Android 模拟器上完成
P0-C 设备门验证：原生生命周期事件、WS 实时通道、GlobalStatusBar 离线状态条均按
08 规范工作。

## 环境

| 项 | 值 |
|---|---|
| AVD | `pocket_test`（pixel_5，Android 11 / API 30，arm64） |
| 构建 | `frontend/android` `assembleDebug`（JDK 21.0.6，GraalVM jlink 不兼容 AGP，需标准 JDK） |
| APK | `com.kaixuan.opencode.pocket` debug，web 资产来自当日 `npm run build:fast` + `npx cap sync android` |
| 后端 | 本机 `pocketd`（.env，dev auth admin/admin，:8088） |

## 验证项与证据

1. **构建/安装/启动**：`assembleDebug` 成功，流式安装 Success，`am start` 拉起
   `MainActivity`（WebView 正常加载 `https://localhost/#/ai`）。
   注：`monkey` 启动返回 -5，须用 `am start -n` 显式指定组件。
2. **WS 实时通道**：应用经 `ws://10.0.2.2:8088/ws?token=...` 连接宿主 pocketd，
   logcat 出现 `"WebSocket connected"`（chromium console）。
   ⚠️ 首连 500：**发现并修复真实 bug** —— `loggingMiddleware` 的 `responseWriter`
   包装层未透传 `http.Hijacker`，gorilla Upgrade 全部失败
   （`backend/internal/server/middleware.go`，回归测试
   `TestResponseWriterPreservesHijacker`）。此 bug 阻断所有 `/ws` 长连接
   （含本次新增的审批推送事件通道），属设备门抓出的 P0 级问题。
3. **App 生命周期原生事件**：HOME → 回前台，logcat 出现
   `Capacitor/AppPlugin: Notifying listeners for event appStateChange`
   （isActive false→true）。前端 `mobileSyncRuntime` 监听 `appStateChange`
   （logcat 证实监听者已注册并收到通知）；未监听 `pause/resume` 原始事件属设计内。
4. **GlobalStatusBar（08 §2.2/§4.2）**：经 WebView CDP 派发
   `window.dispatchEvent(new Event('offline'))`（与 WebView 原生 offline 事件
   同一处理路径）后，`[role=status][aria-live=polite]` 元素即时显示
   「📴离线中 · 操作将保存到本地」；派发 `online` 后状态条消失。
   outbox drain 逻辑由 `src/native/__tests__`（31/31）覆盖。

## 环境备注（非缺陷）

- **模拟器 quirk**：本 AVD（API 30）上 Chromium `navigator.onLine` 不随
  `cmd connectivity airplane-mode` 翻转（系统层默认网络已 none，WebView 仍报
  online）。故 offline 路径用等价事件注入验证；真机复测时建议直接切飞行模式确认
  原生 NetworkChangeNotifier 行为。
- **构建工具链**：sdkman 当前指向 GraalVM CE 21，其 `jlink` 与 AGP
  `core-for-system-modules.jar` 转换不兼容（`capacitor-android:compileDebugJavaWithJavac`
  失败）。构建需 `JAVA_HOME=/Library/Java/JavaVirtualMachines/jdk-21.jdk/...`。
- **首次安装**会弹录音权限（语音输入插件），DENY 不影响上述验证。

## 复现命令

```bash
cd frontend && npm run build:fast && npx cap sync android
cd android && JAVA_HOME=/Library/Java/JavaVirtualMachines/jdk-21.jdk/Contents/Home ./gradlew assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
# CDP 调试：adb forward tcp:9222 localabstract:webview_devtools_remote_$(adb shell pidof com.kaixuan.opencode.pocket)
```
