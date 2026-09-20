# WorkManager 周期任务实施规格（2026-09-20 · 周 3-4）

> 目标：把现有的 `EmailFetchRunner`（已存在，主线 `frontend/android/.../plugins/`）从"一次性调用"提升为 **WorkManager 周期任务**，可在设备息屏/弱网/低电量下被 Doze-aware 调度。
> 上游：[`../audits/2026-09-20-native-ui-restructure-plan.md` §3 周 3-4](../audits/2026-09-20-native-ui-restructure-plan.md)
> 实施状态：**设计稿（design-proposed）**——等真机 / Android Studio 接入测试

---

## 0. 决策摘要

| 决策点 | 选择 | 理由 |
|---|---|---|
| Worker 框架 | **androidx.work.WorkManager** | Capacitor 项目首选；自 2.x 起已 Doze-aware；Google 官方长寿期支持 |
| 触发策略 | **PeriodicWorkRequest** + OneTime 兜底 | Doze 周期 ≥15 min 最小限制；满足 IMAP sync 语义 |
| Job 实现 | `CoroutineWorker` | lifecycle-aware；与现在 `EmailFetchRunner` 静态 helper 解耦 |
| JS 桥 | 新增 **`@capacitor/keepalive-style`** plugin（命名空间复用家族 `pocket.work`） | 不要打 `@capacitor/core` 主包，避免 800KB 链条 |
| 推/拉混合 | 周期 `+` 显式 trigger | 用户开"邮件→立即同步"按钮时一次 OneTime 立即跑 |
| 引导 OEM 白名单 | 新增设置页："电池优化 → 允许后台运行" 按钮 | 方案 B（[§3.b](#3b-电池优化引导页)）|

---

## 1. 架构总览

```
+---------------------------------------------------+
|  Vue (frontend/src/features/email/...)              |
|  useEmailFetchSchedule() composable                |
|  - register / unregister / runNow()                |
+-----------------------┬---------------------------+
                        | (Capacitor bridge)
+-----------------------┴---------------------------+
|  JS side: pocket-work-bridge.ts                    |
|  registerNativePlugin('PocketWork')                |
+-----------------------┬---------------------------+
                        |
+-----------------------┴---------------------------+
|  Android: PocketWorkPlugin.java (extends Plugin)    |
|  - schedulePeriodic(minutes: 15-60)                |
|  - cancel()                                          |
|  - runOnce()                                         |
+-----------------------┬---------------------------+
                        |
+-----------------------┴---------------------------+
|  Android: EmailFetchWork.kt (extends CoroutineWorker)|
|  - doWork(): EmailFetchRunner.run(ctx)              |
+---------------------------------------------------+
                  ↑
                  | enqueueUniquePeriodicWork("pocket-email-fetch", KEEP, request)
                  |
              WorkManager runtime
```

---

## 2. 文件级实施

### 2.1 新增 Java/Kotlin

#### `EmailFetchWork.kt`
```kotlin
package com.kaixuan.opencode.pocket.plugins

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters

class EmailFetchWork(appContext: Context, params: WorkerParameters) :
    CoroutineWorker(appContext, params) {
  override suspend fun doWork(): Result {
    return try {
      val out = EmailFetchRunner.run(applicationContext)
      // 写本地通知（可选）
      if (out.optInt("newCount", 0) > 0) {
        LocalNotif.notify(
          applicationContext,
          "新邮件 ${out.optInt("newCount", 0)} 封",
          out.optString("syncClassified", "")
        )
      }
      Result.success()
    } catch (e: Throwable) {
      // 4xx 不重试；5xx / 网络 retry
      if (runAttemptCount < 3) Result.retry() else Result.failure()
    }
  }
}
```

#### `PocketWorkScheduler.kt`（薄包装，便于在多个入口复用）
```kotlin
object PocketWorkScheduler {
  const val EMAIL_FETCH_WORK = "pocket-email-fetch"

  fun scheduleEmailFetch(ctx: Context, minutes: Long = 30) {
    val request = PeriodicWorkRequestBuilder<EmailFetchWork>(
      minutes.coerceIn(15, 60), TimeUnit.MINUTES,
    )
      .setConstraints(
        Constraints.Builder()
          .setRequiredNetworkType(NetworkType.CONNECTED)
          .build()
      )
      .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 10, TimeUnit.MINUTES)
      .build()
    WorkManager.getInstance(ctx).enqueueUniquePeriodicWork(
      EMAIL_FETCH_WORK,
      ExistingPeriodicWorkPolicy.KEEP,
      request,
    )
  }

  fun runEmailFetchOnce(ctx: Context) {
    val oneTime = OneTimeWorkRequestBuilder<EmailFetchWork>()
      .setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
      .build()
    WorkManager.getInstance(ctx).enqueueUniqueWork(
      "pocket-email-fetch-once",
      ExistingWorkPolicy.REPLACE,
      oneTime,
    )
  }

  fun cancelEmailFetch(ctx: Context) {
    WorkManager.getInstance(ctx).cancelUniqueWork(EMAIL_FETCH_WORK)
  }
}
```

#### `PocketWorkPlugin.java`（Capacitor 桥）
```java
@CapacitorPlugin(name = "PocketWork")
public class PocketWorkPlugin extends Plugin {
  @PluginMethod
  public void scheduleEmailFetch(PluginCall call) {
    Long minutes = call.getLong("minutes", 30L);
    PocketWorkScheduler.scheduleEmailFetch(getContext(), minutes);
    call.resolve();
  }

  @PluginMethod
  public void runEmailFetchOnce(PluginCall call) {
    PocketWorkScheduler.runEmailFetchOnce(getContext());
    call.resolve();
  }

  @PluginMethod
  public void cancelEmailFetch(PluginCall call) {
    PocketWorkScheduler.cancelEmailFetch(getContext());
    call.resolve();
  }
}
```

注册：`MainActivity.onCreate` 加 `registerPlugin(PocketWorkPlugin.class);`

### 2.2 新增 / 修改的 JS

#### `frontend/src/native/pocketWorkBridge.ts`
```ts
import { registerPlugin } from '@capacitor/core'

export interface PocketWorkPlugin {
  scheduleEmailFetch(opts: { minutes: number }): Promise<void>
  runEmailFetchOnce(): Promise<void>
  cancelEmailFetch(): Promise<void>
}

export const PocketWork = registerPlugin<PocketWorkPlugin>('PocketWork')
```

#### `frontend/src/composables/useEmailFetchSchedule.ts`
```ts
import { Capacitor } from '@capacitor/core'
import { PocketWork } from '@/native/pocketWorkBridge'

export function useEmailFetchSchedule() {
  const isNative = Capacitor.isNativePlatform()

  async function setPeriodic(minutes = 30) {
    if (!isNative) return { ok: false, reason: 'web-noop' } as const
    await PocketWork.scheduleEmailFetch({ minutes })
    return { ok: true } as const
  }

  async function runOnce() {
    if (!isNative) return { ok: false, reason: 'web-noop' } as const
    await PocketWork.runEmailFetchOnce()
    return { ok: true } as const
  }

  async function clear() {
    if (!isNative) return { ok: false, reason: 'web-noop' } as const
    await PocketWork.cancelEmailFetch()
    return { ok: true } as const
  }

  return { setPeriodic, runOnce, clear }
}
```

### 2.3 添加权限

#### `AndroidManifest.xml`
```xml
<!-- 已有 -->
<uses-permission android:name="android.permission.WAKE_LOCK" />
<uses-permission android:name="android.permission.FOREGROUND_SERVICE_DATA_SYNC" />

<!-- 新增 WorkManager 自启动 + 接收开机完成 -->
<uses-permission android:name="android.permission.RECEIVE_BOOT_COMPLETED" />
```
`androidx.work` 依赖在 `app/build.gradle` 已通过 androidx 链隐性带入；如缺则加：
```
implementation 'androidx.work:work-runtime-ktx:2.9.1'
```

---

## 3.a WorkManager 落地步骤

1. 落地 [§2.1 EmailFetchWork.kt] + [§2.1 PocketWorkScheduler.kt]
2. 落地 [§2.1 PocketWorkPlugin.java]，注册到 `MainActivity`
3. 添加 RECEIVE_BOOT_COMPLETED 权限 + WorkManager 依赖
4. 编译 `assembleDebug` 通过
5. 落地 [§2.2 pocketWorkBridge.ts] + [§2.2 useEmailFetchSchedule.ts]
6. Web build 不打 `@capacitor/keepalive-style` 占位插件 → 静态 import 解析阶段需 `import type` 隔离
7. 在 `EmailAccountSetup.vue` 加按钮："开启后台同步" → `setPeriodic(30)`
8. 加 `test:workmanager` Node 单测（mock Plugin）覆盖 `setPeriodic / runOnce / clear` 三种 no-op 路径

### Stage gate

| 关卡 | 命令 | 期望 |
|---|---|---|
| TypeScript | `npm run typecheck` | ✅ |
| Web 构建 | `npm run build:fast` | ✅ bundle 增量 < 5KB |
| Native tests | `npm run test:native` | ✅ 不退步 |
| Android assemble | `./gradlew assembleDebug` | ✅（真机阶段） |
| 周期触发 | 真机 24h 实测 | ≥ 90% 命中率 |

---

## 3.b 电池优化引导页

UX 上需要引导用户把本 app 加入电池白名单，否则 OEM（MIUI/HyperOS/EMUI 等）会"优化"掉 WorkManager 周期任务，命中率下降。

### 文件级

#### `frontend/src/composables/useBatteryOpt.ts`
```ts
import { Capacitor } from '@capacitor/core'
import { App } from '@capacitor/app'

export function useBatteryOpt() {
  async function isIgnoringOpt(): Promise<boolean | null> {
    if (!Capacitor.isNativePlatform()) return null
    // 走原生 intent 桥：Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS
    // 在 plugin 内包装 PowerManager.isIgnoringBatteryOptimizations()
    const r = await BatteryOpt.isIgnoring() // 自定义 plugin
    return r.ignoring
  }

  async function requestIgnore(): Promise<void> {
    // 打开系统弹窗；用户可在 OLED 后台策略屏配置
    await BatteryOpt.requestIgnore()
  }

  return { isIgnoringOpt, requestIgnore }
}
```

#### `frontend/src/features/email/EmailAccountSetup.vue`（嵌入）
在"开启后台同步"按钮旁追加：
- 状态行：`电池已加入白名单 ✓` / `未加入，点击申请`
- 点击 → `requestIgnore()`

### Stage gate

| 关卡 | 期望 |
|---|---|
| 一次进入设置后 → 周期任务首次命中 ≤ 30 min | 真机 |
| OPPO/Vivo 后台策略合规 | OEM 推荐位 |

---

## 4. 不在本规格范围

- 推送到达触发（Push → WorkManager 链路）—— v2 议程
- 跨设备同步状态（多端邮件已读状态合并）—— 后端未就位
- iOS 同期 BGAppRefreshTask —— v2 议程（决策 3 已锁）

---

## 5. 完成判据

- [ ] 4 个新文件落地（Java×3 + Kotlin×2；JS×2）
- [ ] AndroidManifest 权限补完
- [ ] `./gradlew assembleDebug` 通过（真机阶段）
- [ ] 真机 24h 周期命中率 ≥ 90%
- [ ] `npm run test:workmanager` 在 PR pipeline 阻断失败
- [ ] 引导页可在设置页工作流中可见

---

**写于**：2026-09-20
**作者**：Mavis / mavis orchestrator
**下次更新**：真机 assembleDebug 通过后回填
