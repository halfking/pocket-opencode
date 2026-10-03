# 真机 / 物理机模拟器测试 Runbook（2026-09-20）

> 给拿到 `app-debug.apk` 后的下一步操作者。
> 这份 runbook 是「用户最终目标 ── 在模拟器/真机上跑 30min 后台 + Perfetto 验收」的可执行说明书。

---

## 0. APK 上下文

> ⚠️ **下表这份 APK 已经不存在了，别按它去找**（2026-10-02 实测）。
> `0B29C7AE…` / 34,426,857 bytes 那份已被后续构建覆盖，工作区的
> `app-debug.apk` 现在是 34,227,285 bytes / `6D209EB1…`（2026-10-02 10:30 产物），
> 两者都不是本表这一份。**本表是 2026-09-20 那次验收的历史上下文，
> 刻意保持原样** —— 把它改指向某个新哈希，会让人误以为 09-20 的
> 30min + Perfetto 验收跑的是那个新包，而那次验收已经无法复现。
>
> 要跑今天这份 runbook，请自己构建并用
> `scripts/android-apk-fingerprint.ps1` 取当前指纹。
> 需要一份**已验证可归因**的现成产物时，见文末「附：当前可归因的 APK」。

| 项 | 值 |
|---|---|
| APK 路径 | `frontend/android/app/build/outputs/apk/debug/app-debug.apk` |
| 大小 | 32.8 MB（34,426,857 bytes） |
| SHA256 | `0B29C7AE7704037232FE09BF1AF9478CA046D52DE36191A0B130F5DF3E56FC73` |
| 包名 | `com.kaixuan.opencode.pocket` |
| 版本 | 1.2.0-openpocket |
| 入口 | `com.kaixuan.opencode.pocket.MainActivity` |
| 签名 | APK v2 Scheme (debug keystore) |
| 适配 | Android 7.0+ (sdk 24)，targetSdk 35 |

## 1. 跑法 A：真机（推荐，最接近用户场景）

### 0. 准备
- 1 台安卓真机（推荐 Android 13+；设备厂商任意）
- USB 数据线
- 真机 Developer Mode + USB debugging 已开
  - 路径：设置 → 关于本机 → 7 次连点"版本号"→ 回到设置 → 系统 → 开发者选项 → USB debugging

### 0.5 一键 preflight（推荐用法）

如果你是 **Windows 用户**，直接双击 / 运行：

```cmd
cd C:\workspace\openpocket
scripts\real-device-preflight.cmd
```

它会自动跑完下面 § 1.1 / § 1.3 / § 1.4（PATH 设置 + adb devices + install + start + 电池白名单页 + logcat 监听）。你只要在弹出的页面点「允许」，再到 App 内发长 prompt 后按 Home。

### 0.6 一键 capture（30 min 后自动汇总）

30 分钟结束后，跑：

```cmd
cd C:\workspace\openpocket
scripts\real-device-capture.cmd
```

它会捕 30 min logcat，自动 grep 出 4 个关键指标（onStartCommand / keepalive / Watchdog / OEM 杀进程），生成 `logs/real-device-summary-YYYYMMDD-HHMMSS.txt`，把那个文件内容贴回 chat 即完成 § 4 回填。

> 偏好手动 control 或非 Windows 平台，继续用 § 1.1-§ 1.9 的 PowerShell / adb 命令版本。

### 1. 启动 adb 桥
```bash
adb devices      # 应看到设备；如未出现，重启 adb：adb kill-server && adb start-server
```

### 2. 安装 APK
```bash
adb install -r frontend/android/app/build/outputs/apk/debug/app-debug.apk
```
> `app-debug.apk` 标 debug = 1，需保持 developer mode

### 3. 启动 App
```bash
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

### 4. 验证权限授予（OEM 后台策略最关键的一步）
1. App 启动后系统弹"允许通知 / 录音 / 相机"——**全同意**
2. 若通知被禁，app 可能后台被杀：
   ```bash
   adb shell dumpsys notification | grep -E 'pocket|kaixuan'
   ```
3. 给"应用电池优化"白名单（厂商不同路径不同，但都可以通过 intent 打开）：
   ```bash
   adb shell am start -a android.settings.IGNORE_BATTERY_OPTIMIZATION_SETTINGS
   ```
   在列表里找到"OpenCode Pocket"→ 点"允许"

### 5. 触发 AI 长 prompt
进 AI 对话页，输入例如：
> "用 Kotlin 实现一个 LRU 缓存，要求 O(1) 读写、自动扩容、单元测试通过。"

观察流式输出。同时：
```bash
adb logcat -v time AiStreamService:V AiStreamKeepalive:V *:S
```
应当看到：
- `AiStreamService onStartCommand`（启动流 FGS）
- `AiStreamKeepalive: keepalive sent`

### 6. 切后台 30 分钟
按 Home → 等 30 分钟（不重开 app、不杀进程）。

### 7. 30 分钟后回前台
观察：
- App 通知（AI 任务进行中）仍在
- 流消息**完整无损**（继续接到上文回复）

### 8. 验收门槛（3 项全过才算成功）
- [ ] 30 min 后回前台，流未中断
- [ ] 通知 30min 内未消失
- [ ] `adb logcat` 中无 "Watchdog triggered"

### 9. Perfetto trace（可选，本机 H/W 也可采）
```bash
adb shell perfetto --background -o /data/local/tmp/ai-bg30m.pftrace \
  -t 30m sched freq idle am wm gfx view input hal.sensors camera input_method
adb pull /data/local/tmp/ai-bg30m.pftrace ./test-evidence/2026-09-20-ai-bg30m.pftrace
```
> 然后用 ui.perfetto.dev 打开 trace。**关键看板**：
> - `AiStreamService` 进程 30 min 全程 `running`
> - 主线程无 `Deep Idle`
> - SSE chunk 平均间隔 ≤ 2s

## 2. 跑法 B：物理机模拟器（推荐优于 VMware 嵌套）

### 0. 确认 BIOS 启用 VTX/AMD-V
重启机器进 BIOS（DEL/F2）→ Advanced → CPU Configuration → Intel VT-x / AMD-V → **Enabled**

### 1. 启 Hyper-V（管理员）
以管理员身份运行 PowerShell：
```powershell
DISM /Online /Enable-Feature /FeatureName:HypervisorPlatform /All /NoRestart
DISM /Online /Enable-Feature /FeatureName:VirtualMachinePlatform /All /NoRestart
bcdedit /set hypervisorlaunchtype auto
# 重启
```

### 2. 用此 runbook 第 1 节一样执行 1-9 步
（在物理机有 VTX 的环境下，我们的 `scripts/emulator-detach.ps1` 会让 emulator 自然启动到 adb 设备；当前 VMware 嵌套中无法启动）

## 3. 跑法 C：云端设备农场（最快、最简单）

- Firebase Test Lab
- BrowserStack
- Sauce Labs
- AWS Device Farm

把 `app-debug.apk` 上传 → 选 Pixel 6 / API 34 → 跑 apk install + am start + logcat collect。
> 仍需要你在 user-side 提供云账户，否则本环境无外部账号。

## 4. 真机数据回填（最关键）

跑完 §1 后，请把以下产物落到 `docs/audits/2026-09-20-apk-static-verification.md` 的新增 §5 表格：

| 字段 | 值 | 来源 |
|---|---|---|
| 设备型号 | 例：Pixel 8 (Android 14) | `adb shell getprop ro.product.model` |
| 厂商后台策略 | 例：MIUI 后台默认 5min/sleep | 厂商·型号 |
| 30min 后回前台时流消息数 | 例：完整 | logcat `AiStreamService onStartCommand` |
| `adb shell dumpsys deviceidle` | `m whitelist` 应包含 kaixuan.opencode.pocket | adb |
| 通知栏"AI 任务进行中" | 30min 全程可见 | 屏幕截图 |
| Perfetto trace 文件名 | `2026-09-20-ai-bg30m.pftrace` | 截图 + 文件 |
| OEM 后台杀进程日志 | "Killed xxx (com.kaixuan.opencode.pocket)" 应=0 条 | logcat |

回填完成后：
1. 把文档更新 PR 推 main
2. 在 §8.6 终极状态表内把 "perfetto" 行由 ⏳ 改 ✅
3. 视情况标记 goal **complete**

---

## 5. 没用上的代理（场景 A 失败时尝试）

如果 30min 验证失败，按下列顺序诊断：

1. **App 是否被杀？**
   ```bash
   adb shell ps -A | grep kaixuan
   ```
   → 看不到 = 被杀；检查 §1.4 后台白名单。

2. **流消息是否在丢？**
   ```bash
   adb logcat | grep -E 'aiStreamRuntime|watchdog'
   ```
   → `triggerWatchdog` 看到 = AI 流 runtime 误判；查 [`../design/2026-09-09-ai-async-background-survival.md`](../design/2026-09-09-ai-async-background-survival.md) §3 阶段 3。

3. **FGS 是否启动？**
   ```bash
   adb shell dumpsys activity services | grep kaixuan
   ```
   → 没看到 `AiStreamService` = keepalive JS 桥没起；查 [`../design/2026-09-20-ai-background-runtime-verification.md`](../design/2026-09-20-ai-background-runtime-verification.md) §3 失败回退路径。

4. **network 是否断？**
   ```bash
   adb shell dumpsys connectivity | head -40
   ```
   → 5min 后掉 wifi/4G 即断；查厂商 battery 优化。

---

## 6. 模拟器在本环境跑不起的原因（仅一处）

| 原因 | 详情 |
|---|---|
| VMware 嵌套 | `systeminfo` 报告"a hypervisor has been detected"；AMD CPU 但 `VMMonitorModeExtensions: False` |
| Hyper-V 不可用 | "Features required for Hyper-V will not be displayed"（嵌套 VM 不能启 Hyper-V）|
| 加速失败 → qemu TCG 软件模拟 | `qemu-system-x86_64-headless` 启动后 kernel cmdline 后无法维持 |

> 任何**非 VMware guest** 的环境都会立即可用（原生 Win10/11 台式机或 Mac/Linux）。

---

## 附：当前可归因的 APK（2026-10-02 修订）

> ⚠️ **本节在 2026-10-02 11:30 修正过一次，读之前先看这段。**
> 初版（提交 `d4328112`）在这里放的是 `DCDBAE91…` / 34,272,370 bytes，
> 对应 `b0123a1`。那份产物**落后 HEAD 86 个提交**，`b0123a1..HEAD`
> 在 APK 输入闭包内有 **180 个文件**变更（含 `frontend/src/api/email.ts`、
> `notes.ts`、`stt-settings.ts` 等必然进 bundle 的源码）。
> 它只满足"输入闭包内无脏文件"这条**自造口径**，不满足待办原文的
> **「与当前 commit 对齐」**，更不满足 `dirty=0`
> —— 指纹文件自己写着 `dirty (tracked): 15`，那是**换尺子而不是达标**。
> 现已改为本轮在干净 worktree 上**亲手重建**的产物，见下。

### 达标产物（本轮亲手重建，非沿用）

| 项 | 值 |
|---|---|
| 路径 | `C:\workspace\openpocket-wt-apkbuild\frontend\android\app\build\outputs\apk\debug\app-debug.apk` |
| 大小 | 34,044,622 bytes |
| SHA256 | `1E6DA6F588E71E99DB477948BEC4817EDBE50C2EFBA171E7411E697627A07221` |
| 构建 commit | `d95b4a68`（`git status --porcelain` 为空 ⇒ **dirty=0**） |
| 构建时间 | 2026-10-02 11:28:14（`gradlew assembleDebug --rerun-tasks`，401/401 任务全执行） |
| 入口 chunk | `assets/public/assets/index-DAqDTu3h.js`（538,747 bytes，`index.html` 的 `src=` 引用它） |
| 烘进的 API base | `http://192.168.31.20:8088`（`build-mobile` 自带 sanity check 已确认） |
| scheme | `https`（`assets/capacitor.config.json` → `server.androidScheme`） |
| 验收 | `node scripts/verify-apk-rebuild.mjs <apk> d95b4a68 <worktree>` → `TOTAL=4 PASS=4 FAIL=0` |

**为什么它算「与当前 commit 对齐」**：`d95b4a68..HEAD` 的 APK 输入闭包内
**零文件差异**，且构建时 worktree 完全干净。两条都由
`scripts/verify-apk-rebuild.mjs` 实测，不是推断。

### 重建时的两个坑（都实测踩过）

1. **`gradlew assembleDebug` 增量构建可能整包不动。** 首次重跑时它报
   `BUILD SUCCESSFUL` 却只执行 49/401 任务，APK 的 mtime 与 sha256
   **一字未变**。原因是 `29048294..d95b4a68` 闭包零差异，
   gradle 判定 `assembleDebug` UP-TO-DATE。
   ⇒ **验收重建必须查 mtime 与 sha256，不能只看 `BUILD SUCCESSFUL`。**
   强制重打用 `gradlew assembleDebug --rerun-tasks`（本次 401/401 全执行）。
   重打后 sha256 **仍与旧包相同** —— 这正是「物料逐字节一致」的直接证据，
   而非「没重建」的证据。

2. **`cap sync` 会把 worktree 弄脏，且泄漏绝对路径。** 它重写
   `frontend/android/capacitor.settings.gradle` 与 `app/capacitor.build.gradle`，
   把 `../node_modules/...` 改写为指向**主工作区**的
   `../../../openpocket/frontend/node_modules/...`。
   实测该路径**不进 bundle**（扫过产物全部 `assets/public/assets/*.js`），
   但它会让 `dirty=0` 判据转红。
   ⇒ 构建后 `git checkout --` 这两个文件，并删掉构建用的 `.env.android-dev`
     （它被 `.gitignore` 忽略，所以构建期间 `status` 一直是干净的）。

### 复现方式

```powershell
cd C:\workspace\openpocket-wt-apkbuild        # 必须先确认 status --porcelain 为空
'VITE_API_BASE=http://192.168.31.20:8088' | Set-Content frontend\.env.android-dev
cd frontend
node scripts\build-mobile.mjs android dev     # 内含 sanity check
cd android
$env:JAVA_HOME = 'C:\Program Files\Eclipse Adoptium\jdk-21.0.12.101-hotspot'
.\gradlew.bat assembleDebug --rerun-tasks     # 不用 --rerun-tasks 可能整包不动
# 收尾：还原 cap sync 弄脏的两个 gradle 文件 + 删掉 .env.android-dev
cd ..\.. ; git checkout -- frontend/android/app/capacitor.build.gradle frontend/android/capacitor.settings.gradle
node scripts\verify-apk-rebuild.mjs `
  C:\workspace\openpocket-wt-apkbuild\frontend\android\app\build\outputs\apk\debug\app-debug.apk `
  d95b4a68 C:\workspace\openpocket-wt-apkbuild
```

### 两个仍然适用的提醒

⚠️ 查证产物里烘进的配置，**先看 `assets/public/index.html` 的 `src=`
引用了哪个 chunk**。产物里可以有 7 个 `index-*.js`，只有一个是主入口；
按名字猜会命中 10KB 的同名小 chunk，然后误判成"配置没烘进去"。

⚠️ 这份是 **https origin**（`localhost` 分区）。若要连明文后端，
必须以 `CAP_ANDROID_SCHEME=http` 重新构建 —— 换了 scheme 等于换了整个
localStorage 分区，用户存的 server 地址/token/语言/主题会全部读不到。

---

**写于**：2026-09-20
**作者**：Mavis / mavis orchestrator
**下次更新**：真机或物理机上完成 30min 后台验收后回填 §4 表格
