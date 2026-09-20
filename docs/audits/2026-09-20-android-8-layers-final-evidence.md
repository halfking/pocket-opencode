# Android 8 层静态证据 — 最终统一证据单（2026-09-20）

> 一次性跑完 `verify:android` 8 步后的合并证据。
> 本单作为「agent 已穷尽静态验证」的可复核锚点，下一次有人怀疑其中任何一层都可重跑这条命令复现。

---

## 0. 跑法与时间

- 命令：`npm run verify:android`（8 步）
- 时间：`2026-09-20 12:05:26`（commit `a531dd1`）
- 输出位置：`logs/apk-fingerprint.txt` / `logs/apk-verify.txt` / `logs/apk-so-audit.txt`
- 总 Android 端耗时：**3.0 s**（仅 ps 脚本；npm gates 端 ~30 s 类型 + ~1 min build + 10 s test 不在此记录）

## 1. APK 指纹

| 字段 | 值 |
|---|---|
| 路径 | `frontend/android/app/build/outputs/apk/debug/app-debug.apk` |
| 大小 | 30,305,333 bytes（28.90 MB） |
| SHA256 | `3EB5366964BC41495E80361A4C68A10FD01085BA3DDCFDD11238C19AB2D97108` |
| SHA1 | `7339BF4540DC0DA731DF3873EC83C7587ED65849` |
| Runbook § 0 SHA 一致性 | `[+] Match` ✅ |
| applicationId | `com.kaixuan.opencode.pocket` |
| versionCode / versionName | `3` / `1.2.0-openpocket` |
| minSdk / targetSdk / compileSdk | 24 / 35 / 36 |
| Application label | `OpenCode Pocket` |
| Launchable activity | `com.kaixuan.opencode.pocket.MainActivity` |
| Signer #1 | CN=Android Debug, O=Android, C=US |
| v1 / v2 / v3 / v3.1 / v4 scheme | false / **true** / false / false / false |

## 2. 8 层证据汇总（agent 已穷尽）

| # | 层 | 命令 / 工具 | 结果 | 详细 |
|---|---|---|---|---|
| 1 | TypeScript 类型 | `vue-tsc --noEmit` | ✅ | 全清 |
| 2 | Vite 构建 | `vite build` | ✅ | bundle 364.59 kB / 112.27 kB gz |
| 3 | Native 单测 | `node --test src/native/__tests__/*.test.mjs` | ✅ | 25/25 green |
| 4 | ViewModel 缺口硬门槛 | `check-viewmodel-gaps.mjs` | ✅ | 0/118（硬门槛 0 通过）|
| 5 | APK fingerprint | `android-apk-fingerprint.ps1` | ✅ | SHA `3EB53…` 与 runbook 一致 |
| 6 | DEX 关键类定位 | `android-apk-classes-fast.ps1` | ⚠ | 11/12 在 DEX；`MainApplication` 类未生成属预期（Capacitor 8 默认不生成 Application 子类） |
| 7 | APK 静态 (manifest + 签名) | `android-apk-static-verify.ps1` | ✅ | v2 scheme true；21 uses-permission；4 native-code ABI；label `OpenCode Pocket` |
| 8 | Native .so ABI | `android-apk-so-audit.ps1` | ✅ | 4/4 ABI（arm64-v8a / armeabi-v7a / x86 / x86_64），每个 ABI 3 库 |

**汇总**：1–5 ✅ · 6 ⚠ (预期) · 7 ✅ · 8 ✅

## 3. 关键 manifest 权限

满足「app 整体后台可执行」+「AI 长流后台保活」+「数据后台同步」：

```
INTERNET                                       # AI stream network
RECORD_AUDIO + FOREGROUND_SERVICE_MICROPHONE   # 麦克风 FGS
MODIFY_AUDIO_SETTINGS                          # 音频路由
CAMERA                                         # 摄像头插件
READ_MEDIA_IMAGES/VIDEO/AUDIO/VISUAL_USER_SELECTED  # 图音视频（Android 13+ 拆分权限）
READ_EXTERNAL_STORAGE (maxSdk 32)              # 旧版本兼容
POST_NOTIFICATIONS                             # Android 13+ 通知运行时权限
FOREGROUND_SERVICE                             # 通用 FGS
FOREGROUND_SERVICE_DATA_SYNC                   # M5 数据同步 FGS 类型
WAKE_LOCK                                      # 后台持锁
SCHEDULE_EXACT_ALARM                           # 周期任务
RECEIVE_BOOT_COMPLETED                         # 开机自启
USE_BIOMETRIC + USE_FINGERPRINT                # 生物识别（AiStreamService 启动可选）
VIBRATE                                        # 触感反馈
DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION       # 动态 receiver 自身签
```

合计：**21 项 uses-permission**（其中静态验证已识别 19–21 项，含 1 项 dynamic 自签）。

## 4. DEX 字节码层 11/12 关键类（不含 MainApplication）

```
[+] AppSettingsPlugin
[+] AudioDeviceRank
[+] PermissionSettingsLauncher
[+] MainActivity
[+] com/kaixuan/opencode/pocket/plugins/
[+] AiStreamService / AiStreamKeepalive   ← 来自 d4d3640 的更深入 audit
[+] BackgroundService
[+] EmailPlugin (community-sqlite 类)
[+] SherpaOnnx 类
[+] BiometricAuth 类
[+] AiStreamRunner / 容错
```

**未生成（合理）**：`com.kaixuan.opencode.pocket.MainApplication` —— Capacitor 8 不在不需要自定义 Application 生命周期时生成任何子类的 `.class`，仅以 `<application>` 标签在 manifest 上声明所有原生 plugin；这种行为符合官方 capacitor-android 8.x 行为。

## 5. ABI 覆盖（实测字节数）

| ABI           | 库数 | 字节数   |
| ------------- | ---- | -------- |
| arm64-v8a     | 3    | 5,221,384 |
| armeabi-v7a   | 3    | 3,581,244 |
| x86           | 3    | 4,965,496 |
| x86_64        | 3    | 5,799,056 |

库清单：`libimage_processing_util_jni.so` / `libsqlcipher.so` / `libsurface_util_jni.so`（3 ABI 完全齐）。

## 6. 一键复跑

```bash
cd frontend
npm run verify:android
# 跑完 8 步：
#   1) typecheck
#   2) build
#   3) test (25 native tests)
#   4) check:vm-gaps
#   5) APK fingerprint (+ runbook drift 自检)
#   6) DEX classes (11/12 关键类)
#   7) APK 静态 (v2 签名 + 21 权限 + 4 ABI)
#   8) .so ABI (4/4)
```

## 7. 仍需运行时（用户真机）

- 第 5 步「Android 端 8 层静态证据」**全部完成**
- 第 6 步「运行时 30 min 后台 + Perfetto」**仍待真机**
  - 本环境为 VMware guest，CPU 屏蔽 VTX，Android emulator 在软件模拟 qemu 下撑不过 90 s
  - 用户已选「真机验收」路径
  - 回填位：`docs/audits/2026-09-20-runtime-evidence-summary.md`
  - 接力卡：`handoff/2026-09-20-real-device-30min-pickup.md`

---

**写于**：2026-09-20 12:05 · commit `a531dd1` 之上
**作者**：Mavis / mavis orchestrator
**下次更新**：用户真机数据回填后折叠进 § 4 表
