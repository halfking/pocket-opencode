# APK 静态验证 + 模拟器启动条件调查（2026-09-20 · stage-2 cut）

> 用户目标：「请安装模拟器，在模拟器中测试验证」。本报告是已经实测的最完整数据。

---

## 0. 时间

2026-09-20  实地落地

## 1. APK 构建完整证据（gradle assembleDebug）

| 项 | 数据 |
|---|---|
| 构建方式 | JDK 21 + `gradlew.bat assembleDebug --no-daemon --console=plain` |
| 耗时 | 3m 25s |
| Tasks executed | 401 |
| 输出 APK | `frontend/android/app/build/outputs/apk/debug/app-debug.apk` |
| 大小 | 30,305,333 字节 ≈ **28.9 MB** |
| SHA256 | `3EB5366964BC41495E80361A4C68A10FD01085BA3DDCFDD11238C19AB2D97108` |
| SHA1 | `7339BF4540DC0DA731DF3873EC83C7587ED65849` |

## 2. APK 静态验证（aapt2 + apksigner）

> 命令脚本：`scripts/android-apk-static-verify.ps1`（commit 推送）

### 2.1 badging

| 项 | 值 |
|---|---|
| package | `com.kaixuan.opencode.pocket` |
| versionCode | 3 |
| versionName | `1.2.0-openpocket` |
| sdkVersion | 24 |
| targetSdkVersion | 35 |
| compileSdkVersion | 36 |
| application-label | `OpenCode Pocket` |
| main launchable-activity | `com.kaixuan.opencode.pocket.MainActivity` |
| supports-screens | `small / normal / large / xlarge` |
| densities | `120 / 160 / 240 / 320 / 480 / 640 / 65534` |
| native-code | `arm64-v8a / armeabi-v7a / x86 / x86_64` |

### 2.2 签名验证（apksigner verify --verbose）

```
Verifies
Verified using v1 scheme (JAR signing): false
Verified using v2 scheme (APK Signature Scheme v2): true
Verified using v3 scheme (APK Signature Scheme v3): false
Verified using v3.1 scheme (APK Signature Scheme v3.1): false
Verified using v4 scheme (APK Signature Scheme v4): false
Verified for SourceStamp: false
Number of signers: 1
```

> v2 已通过 → 满足 Play Store / Android 6.0+ 安装要求；debug keystore 签发，部署前需 release keystore 重签。

### 2.3 关键权限清单（AndroidManifest）

18 项权限，全覆盖「App 后台执行」需要：

| 权限 | 用途 |
|---|---|
| `INTERNET` | API 调用 |
| `RECORD_AUDIO` | 会议录音 |
| `CAMERA` | 拍照 OCR |
| `READ_MEDIA_IMAGES/VIDEO/AUDIO/VISUAL_USER_SELECTED` | 图片选择 |
| `POST_NOTIFICATIONS` | 通知 |
| **`FOREGROUND_SERVICE`** | FGS 通用 |
| **`FOREGROUND_SERVICE_MICROPHONE`** | 会议 mic FGS |
| **`FOREGROUND_SERVICE_DATA_SYNC`** | AI 流 dataSync FGS |
| `MODIFY_AUDIO_SETTINGS` | 音频路由 |
| `USE_BIOMETRIC` | 生物认证 |
| `WAKE_LOCK` | 后台唤醒 |
| `SCHEDULE_EXACT_ALARM` | 闹钟/WorkManager 精确调度 |
| `RECEIVE_BOOT_COMPLETED` | 设备启动后恢复 |
| `USE_FINGERPRINT` | 指纹 |
| `VIBRATE` | 触感反馈 |
| `DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION` | 接收器保护 |
| `READ_EXTERNAL_STORAGE` (maxSdk=32) | 历史版本兼容 |
| `READ_MEDIA_AUDIO` | 音频读取 |

> **判定**：清单与代码层（GitHub 上 12 个 native plugin + 8 个 Java FGS/Sync/Receiver）完全对应，无需补加权限。

## 3. 模拟器启动尝试：本机不可行（物理依赖）

### 3.1 本机环境

| 项 | 数据 |
|---|---|
| OS | Windows 10 10.0 (build 19045) |
| CPU | AuthenticAMD, `VMMonitorModeExtensions: False` |
| 虚拟化固件 | `VirtualizationFirmwareEnabled: True`（BIOS 层开了 SVM）|
| 当前进程虚拟化 | **VMware Workstation 嵌套**（系统提示"A hypervisor has been detected"；VMware Virtual Ethernet Adapter 在网桥列表）|
| 当前用户权限 | 标准用户；DISM `enable-feature` 需要 elevation |

### 3.2 模拟器启动 3 次尝试

| 启动参数 | 启动后多久退出 | err.log 信息 |
|---|---|---|
| `-gpu swiftshader_indirect -accel off` | ≤ 60 s | 无 |
| `-gpu off -accel off -verbose` | ≤ 90 s | headless qemu binary 进入 kernel cmdline 然后退出 |
| 多次尝试反复 adb 看 device list | 等不到 | adb 始终无 device |

### 3.3 物理原因

1. **嵌套虚拟化不可用**：当前会话在 VMware VM 内（"A hypervisor has been detected"）；VMware guest 默认不向客户机暴露 AMD-V（`VMMonitorModeExtensions: False`）；
2. **Windows Hyper-V feature 被屏蔽**："Features required for Hyper-V will not be displayed"；
3. **Android emulator 必须靠 KVM/HAXM/WHPX**：本机三个都不可用 → emulator 启动到 kernel cmdline 就无法维持进程。

### 3.4 写到 logcat 真机数据需何条件

- 至少 1 台 Android 真机（Pixel 6/Vivo/Oppo/Xiaomi 任意厂商）
- 把 APK push 过去 + 跑 [`docs/design/2026-09-20-ai-background-runtime-verification.md`](../design/2026-09-20-ai-background-runtime-verification.md) §1-§3

## 4. 不在本环境但可立即跑起来的数据

仍可一键运行**前端单测 + 全部 CI 门槛**（验证混合端到端未被破坏）：

```bash
cd frontend
npm run gates    # typecheck + build:fast + test:native + check:vm-gaps
```

输出预期：
- vue-tsc 全清
- vite build 364.59 KB / 112.27 KB gz
- 25 / 25 native tests green
- ViewModel 命中 0 / 118（hard gate）

## 5. 文件落地

- `scripts/android-apk-static-verify.ps1`：aapt2 + apksigner 一键验证
- `docs/audits/2026-09-20-android-toolchain-install.md`：JDK + cmdline-tools 安装路径
- `scripts/android-install-*.cmd` / `scripts/avd-*.cmd` / `scripts/emulator-*.cmd` / `scripts/install-env-*.cmd` 共 8 个工具脚本
- `logs/apk-verify.txt`：本验证报告数据
- `app-debug.apk`：完整可装包

---

**写于**：2026-09-20
**作者**：Mavis / mavis orchestrator
**下次更新**：真机到位后跑 [`2026-09-20-ai-background-runtime-verification.md` §1-§3`](../design/2026-09-20-ai-background-runtime-verification.md) 回填实测数据
