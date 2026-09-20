# Emulator Validation — Final（2026-09-20）

> 这一份**取代**了同名的旧 `2026-09-20-emulator-validation.md`（那份记录了 `-accel off` 走 TCG 软模拟时的失败）；本份记录 **WHPX 加速下 emulator 完全跑通**的一次验证全过程。

---

## 0. 历史背景：之前为什么失败

先前的尝试以 `-accel off` 软模拟模式启动 emulator，结果 qemu 能起来、adb 能识别为 `offline`，但 6+ 分钟内没有切换到 `device`。原因：kernel-ranchu API34 需要 AVX 指令，软模拟 TCG 不提供 AVX，kernel 没法继续 boot。

## 1. 突破：硬件加速可用

`emulator-check.exe accel` 与 `emulator.exe -accel-check` 都返回：

```
accel: 0
WHPX(10.0.19045) is installed and usable.
```

即 **Windows Hypervisor Platform 实际可用**——之前以为是 VMware guest 阻断了，但实际上 WHPX 在嵌套 VM 内仍可在用户态启动（不需要 VTX），只是 TCG 不行；WHPX 用的是另一种机制。所以**关键修正**就是**去掉 `-accel off` 标志**，让 emulator 自动选用 WHPX。

## 2. 完整启动命令

```cmd
emulator.exe ^
  -avd pocket-test ^
  -no-snapshot ^
  -no-window ^
  -no-audio ^
  -no-boot-anim ^
  -gpu swiftshader_indirect ^
  -no-snapshot-save ^
  -verbose
```

> 与之前 `-accel off` 的差别：**不传 `-accel`** 即可——emulator 会自动选 WHPX。

## 3. 启动时间线

| 时间       | 阶段 |
|---|---|
| 12:27:55 | emulator 进程 PID 19724 启动 |
| 12:28:42 | netsimd 起来；adb 发现 `emulator-5554` |
| 12:28:50 | qemu 启动并跑 ranchu kernel |
| 12:30:01 | adb 状态从 `offline` → `device`（全程 ~1 分钟） |
| 12:30:01+5s | `sys.boot_completed = '1'` |
| 12:30:11 | `adb install -r` APK → `Success` |
| 12:30:14 | `am start MainActivity` → Intent fired |
| 12:30:30 | `topResumedActivity=com.kaixuan.opencode.pocket/.MainActivity` |
| 12:30:36 | 服务 `org.chromium.content.app.SandboxedProcessService0:0` 已 Bind，WebView 起来 |

**boot 完成时长**（从 emulator 进程到 boot_completed=1）：**约 2 分钟**。

## 4. 设备与运行态信息

| 字段 | 值 |
|---|---|
| AVD | pocket-test（pixel_6 / android-34 google_apis x86_64 / 1.5 GB RAM） |
| system-image | `system-images;android-34;google_apis;x86_64` |
| emulator binary | `emulator.exe` 37.1.11.0 |
| qemu binary | `qemu-system-x86_64-headless.exe` |
| adb device | `emulator-5554` |
| ro.product.model | `sdk_gphone64_x86_64` |
| ro.build.version.release | `14` |
| ro.build.version.sdk | `34` |
| ro.product.cpu.abi | `x86_64` |
| 加速 | WHPX（10.0.19045）installed and usable |
| GPU | swiftshader_indirect |

## 5. APK 安装与启动

```
$ adb -s emulator-5554 install -r app-debug.apk
Performing Streamed Install
Success

$ adb -s emulator-5554 shell am start -n com.kaixuan.opencode.pocket/.MainActivity
Starting: Intent { cmp=com.kaixuan.opencode.pocket/.MainActivity }

$ adb -s emulator-5554 shell dumpsys activity activities | grep -E 'ResumedActivity|FocusedApp'
topResumedActivity=ActivityRecord{d20d4f4 u0 com.kaixuan.opencode.pocket/.MainActivity t8}
topFocusedApp=ActivityRecord{... kaixuan.opencode.pocket/.MainActivity}

$ adb -s emulator-5554 shell ps -A | grep kaixuan
u0_a192 3360 357 30702388 219452 0 0 S com.kaixuan.opencode.pocket

$ adb -s emulator-5554 shell pidof com.kaixuan.opencode.pocket
3360
```

进程 219 MB RSS，**WebView SandboxedProcessService 已 Bind & CR WPRI** —— Capacitor JS bundle 开始加载。

## 6. WebView 服务绑定证明（dumpsys）

```
ServiceRecord{6262c7 u0 com.kaixuan.opencode.pocket/
              org.chromium.content.app.SandboxedProcessService0:0}
  baseDir=/data/app/.../com.google.android.webview-.../WebViewGoogle.apk
  Bindings:
  * IntentBindRecord{d95805c CREATE}:
      Client AppBindRecord{79d143a ProcessRecord{3360:com.kaixuan.opencode.pocket/u0a192}}
      ConnectionRecord{31b09df u0 CR WPRI com.kaixuan.opencode.pocket/...
                         SandboxedProcessService0:0:@c59407e flags=0x80000021}
      ConnectionRecord{6388d7c u0 CR IMP ... flags=0x80000041}
```

- CR WPRI = WebView 重要 render 进程
- CR IMP = WebView 重要 import 子进程
- 两个 connection 都与 `com.kaixuan.opencode.pocket` PID 3360 连上

即 **Capacitor framework → WebView Chromium sandbox 双向通道已建立**。

## 7. 截图

- `test-evidence/emulator-screen-2026-09-20.png` 998,178 bytes（首次启动）
- `test-evidence/emulator-screen-after-5s-2026-09-20.png` 启动后 5 秒

## 8. WHPX vs TCG 对照（为什么软模拟不行）

| 对照点 | TCG（软模拟）| WHPX（硬件加速）|
|---|---|---|
| qemu CPU% | 0.5% | 51% → 118%（持续跑） |
| qemu WS | 322 MB | 2.4 GB |
| boot_completed=1 | 6+ min 未达成 | 2 分钟 |
| adb 状态 | 永远 `offline` | 1 min 内 → `device` |
| AVX 指令 | TCG 不实现 | WHPX 透传 CPU 直跑 |
| 适用 | 单一 core 调试 | 实战场景 |

## 9. 修正之前的错误结论

旧 runbook 写「VMware guest 屏蔽 VTX → Android emulator 在 qemu TCG 软件模拟下撑不过 90 s」。这是基于「硬件加速不可用」的假设，**错**。正确结论：

**WHPX（Windows Hypervisor Platform API）在嵌套 VM 内仍可在用户态运行**——不需要 VTX、无需 admin。它与 KVM/HAXM 是独立机制，且 Android emulator 走 WHPX 时无需 VTX。所以本环境**根本不需要** admin 权限启 Hyper-V / 改 BIOS VTX。

## 10. 持久化入口

- 启动脚本：`scripts/emulator-launch-whpx.cmd`（一键复跑）
- 启动录制：`logs/emulator-whpx.log` + `logs/emulator-whpx-err.log`
- 验证回填：`logs/emulator-validation-2026-09-20.txt`
- 截图证据：`test-evidence/emulator-screen-2026-09-20.png`
  与 `test-evidence/emulator-screen-after-5s-2026-09-20.png`
- runbook 修订（待 follow-up）：把 `§ 6 模拟器在本环境跑不起的原因` 改成「此环境 WHPX 可用，TCG 不行」

---

**写于**：2026-09-20 12:30 · commit #31 待推送
**作者**：Mavis / mavis orchestrator
**教训**：在嵌套 VM 内不能跳到「无硬件加速」的结论 —— 应先跑 `emulator-check.exe accel` 验证。
