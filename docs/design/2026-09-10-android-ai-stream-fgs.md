# T2：Android AiStreamService 前台服务（dataSync）落地与验证

| 字段 | 值 |
|------|----|
| 日期 | 2026-09-10 |
| 任务来源 | handoff 2026-09-10 §2 T2（P0） |
| 设计依据 | [2026-09-09-ai-async-background-survival.md](./2026-09-09-ai-async-background-survival.md) §D5 |
| 验证证据 | `docs/design/2026-09-10-android-fgs-notification-evidence.png`（通知栏实拍） |

---

## 1. 落地内容

| 文件 | 内容 |
|------|------|
| `frontend/android/.../plugins/AiStreamService.java` | dataSync 型前台服务：`ai_stream_sync` 通知通道（IMPORTANCE_LOW）、常驻通知（活跃数文案）、PARTIAL_WAKE_LOCK（30min 上限，UPDATE 续期）、`ACTION_START/STOP/UPDATE` 幂等 |
| `frontend/android/.../plugins/AiStreamKeepalivePlugin.java` | Capacitor 桥（`AiStreamKeepalive`）：`start/stop/update/isRunning`；Android 13+ `POST_NOTIFICATIONS` 运行时权限（PROMPT 时弹系统窗，**被拒也照常起服务**，`permGranted` 如实上报 JS） |
| `AndroidManifest.xml` | `<service .plugins.AiStreamService foregroundServiceType="dataSync"/>`（权限 M5 已备） |
| `MainActivity.java` | `registerPlugin(AiStreamKeepalivePlugin.class)` |
| `frontend/src/native/aiStreamKeepalive.ts` | 决策层：`activeCount>0 && hub.isHidden()` → start（同向幂等）；其余 → stop；start 保持中活跃数变化 → update 刷新通知；30s 心跳兜底收尾；仅 Android Capacitor 注入桥，Web/iOS no-op |
| `main.ts` | `startAiStreamKeepalive()` 一次性接线 |
| `__tests__/aiStreamKeepalive.test.mjs` | 7 个用例覆盖决策表 |

## 2. 关键设计点

1. **启动时机**：只在 lifecycle `hidden` 事件瞬间拉起——此时 App 仍处系统 TOP 状态/宽限期内，
   Android 12+ 的「后台启动 FGS」限制不会命中（见 §3-e）。若错过时机（如心跳补启动）会被
   `ActivityManager DENIED`，这是**预期行为**而非 bug。
2. **停服时机**：回前台 或 活跃流归零（心跳兜底感知，流自然结束没有事件可听）。
3. **通知权限被拒**：FGS 照常跑（服务本身不依赖通知权限），通知不可见，JS 侧收到
   `permGranted=false` 可引导用户开权限。

## 3. AVD 实测（Medium_Phone_API_36.1，2026-09-10 02:1x）

- ✅ 桥接链路：logcat `AiStreamKeepalive.start` → `Background started FGS: Allowed` → 服务起来
- ✅ 通知渲染：通知栏实拍「AI 任务进行中 — 6 个 AI 任务在后台运行」（证据 PNG）
- ✅ WakeLock：`dumpsys power` 可见 `openpocket:ai_stream (partial)` ACQ/REL 配对
- ✅ 自动收尾：流结束后 30s 心跳网格上心跳 `stop` → 服务停、wakelock REL
- ✅ 单测 7/7；`vue-tsc` 0 错
- ⚠️ **发现并修复前置 bug（thenable 陷阱）**：Capacitor 插件 proxy 对未知属性（含 `.then`）
  reject "not implemented"，而 `async` 函数把它当返回值再被 `await` 时，Promise 解约的 thenable
  检查必然触发 `.then` → **`appLifecycleHub` 的 Capacitor `appStateChange` 通道自 M1 起一直静默
  失败**（iOS 同样中招，此前全靠 DOM `visibilitychange` 兜底）。修复：
  `appLifecycleHub.loadCapacitorApp` 改返回 `{ app }` 信封、`aiStreamKeepalive.initBridge` 只写
  模块级变量不返回 proxy。同模式残留在 `email-fetch-native.ts`（EmailFetch.then 报错，功能有
  catch 兜底）——已记 issue 待清。
- ⚠️ **埋点遗留**：后台状态下 heartbeat 补发的 `startForegroundService` 被系统 DENIED
  （`code:DENIED`，预期），无需处理，但日志监控上可当「流活得比服务久」的信号。

## 4. 验证流程复现

```bash
cd frontend && node scripts/build-mobile.mjs android dev && cd android && ./gradlew assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
# 触发（WebView 调试通道方式，绕开 UI 坐标）：
adb forward tcp:9222 localabstract:webview_devtools_remote_$(adb shell pidof com.kaixuan.opencode.pocket)
# CDP Runtime.evaluate：spawnChat 起流 → 伪造 visibilitychange(hidden)
adb shell dumpsys activity services com.kaixuan.opencode.pocket   # 看 AiStreamService isForeground
adb shell cmd statusbar expand-notifications                      # 看常驻通知
```
