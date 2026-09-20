# 真机 / 物理机模拟器测试 Runbook（2026-09-20）

> 给拿到 `app-debug.apk` 后的下一步操作者。
> 这份 runbook 是「用户最终目标 ── 在模拟器/真机上跑 30min 后台 + Perfetto 验收」的可执行说明书。

---

## 0. APK 上下文

| 项 | 值 |
|---|---|
| APK 路径 | `frontend/android/app/build/outputs/apk/debug/app-debug.apk` |
| 大小 | 28.9 MB（30,305,333 bytes） |
| SHA256 | `3EB5366964BC41495E80361A4C68A10FD01085BA3DDCFDD11238C19AB2D97108` |
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

**写于**：2026-09-20
**作者**：Mavis / mavis orchestrator
**下次更新**：真机或物理机上完成 30min 后台验收后回填 §4 表格
