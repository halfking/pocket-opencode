# Android 工具链落地（2026-09-20 · emulator-install）

> 给后续 session 的工具链安装 recipe。当前已落地：
> - JDK 17（AdoptOpenJDK 17.0.0.20）
> - Android cmdline-tools（google 官方 11076708）
> - ANDROID_HOME + PATH 已设置
> - SDK 7 个包 license 已接受
> - platform-tools / platforms;android-34 / build-tools / emulator / system-image 装包进行中
>
> 本文档未来改动：装包完成后回填实测数据到 §3 §4 §5。

---

## 0. 时间

2026-09-20 起逐步落地：JDK → cmdline-tools 拉取 → 解压 → PATH → licenses → 装包。

## 1. 安装路径（已用）

| 步骤 | 命令 | 备注 |
|---|---|---|
| 1.1 | `winget install --id AdoptOpenJDK.OpenJDK.17 --source winget` | Microsoft.OpenJDK.17 winget 包哈希不匹配；改用 AdoptOpenJDK |
| 1.2 | `setx JAVA_HOME "C:\Program Files\AdoptOpenJDK\jdk-17.0.0.20-hotspot"` | 写到 User（无需管理员）|
| 1.3 | `setx PATH "%PATH%;C:\Program Files\AdoptOpenJDK\jdk-17.0.0.20-hotspot\bin"` | User level |
| 2.1 | `Invoke-WebRequest https://dl.google.com/android/repository/commandlinetools-win-11076708_latest.zip -OutFile cmdline-tools.zip` | 153 MB 下载 |
| 2.2 | `Expand-Archive cmdline-tools.zip -Destination "$env:LOCALAPPDATA\Android\cmdline-tools"` | 解压 |
| 2.3 | 重命名：`cmdline-tools/cmdline-tools/*` → `cmdline-tools/latest/*`（再把所有 .bat 移到 `latest/bin/`）| 标准 SDK 布局 |
| 2.4 | `setx ANDROID_HOME %LOCALAPPDATA%\Android` | User level |
| 3.0 | `cmd /c "scripts\android-accept-licenses.cmd" < yes.txt` | 7 个 license 一次性接受（y 输入 12 次冗余）|
| 4.0 | `cmd /c "scripts\android-install-packages.cmd"` | 5 包并行装，预计 30-60 min（下载 1.4 GB）|

> **不要用 Google.AndroidCLI**：仅包装 `android.exe`，不包含 sdkmanager。真正的 SDK 管理仍需 cmdline-tools 官方包。

## 2. PATH 与 env 状态

```
JAVA_HOME       = C:\Program Files\AdoptOpenJDK\jdk-17.0.0.20-hotspot
ANDROID_HOME    = %LOCALAPPDATA%\Android
ANDROID_SDK_ROOT = %LOCALAPPDATA%\Android   (alias)
PATH 顺序:
  1. %JAVA_HOME%\bin                            (java/javac)
  2. %ANDROID_HOME%\cmdline-tools\latest\bin    (sdkmanager / avdmanager)
  3. %ANDROID_HOME%\platform-tools              (adb)
  4. %ANDROID_HOME%\emulator                    (emulator)
```

## 3. 装包完成回填（待完成）

| 包 | 状态 | 大小估算 | 备注 |
|---|---|---|---|
| platform-tools | ⏳ | ~50 MB | adb, fastboot |
| platforms;android-34 | ⏳ | ~80 MB | android.jar |
| build-tools;34.0.0 | ⏳ | ~200 MB | aapt, d8, dx |
| emulator | ⏳ | ~300 MB | x86_64 模拟器运行器 |
| system-images;android-34;google_apis;x86_64 | ⏳ | ~800 MB | Pixel 6 启动盘 |

## 4. 待建 AVD（装包完成后）

```
avdmanager create avd -n pocket-test -k 'system-images;android-34;google_apis;x86_64' \
  --device 'pixel_6' --force
```

> 装包进度回填在 §3。AVD 在装包完成后再建。

## 5. 真机段验收目标

按 [`../design/2026-09-20-ai-background-runtime-verification.md`](../design/2026-09-20-ai-background-runtime-verification.md) §1：

1. `./gradlew assembleDebug` 真机过（装包后即可在 emulator 上跑）
2. 装到 emulator：`adb install app/build/outputs/apk/debug/app-debug.apk`
3. 启动 emulator：`emulator -avd pocket-test -no-snapshot`
4. AI 长 prompt 测试，按 §1 步骤跑 30 min 后台
5. Perfetto 实测按 §2

## 6. 安装脚本索引

| 脚本 | 用途 |
|---|---|
| `scripts/install-env-jdk17.cmd` | JDK + PATH 持久化 |
| `scripts/install-android-sdk.ps1` | 解压 cmdline-tools + 设置 ANDROID_HOME + PATH |
| `scripts/fix-cmdline-layout.ps1` | 把解压的 bat 移到 bin 子目录 |
| `scripts/android-accept-licenses.cmd` | stdin pipe y 接受所有 license |
| `scripts/android-install-packages.cmd` | 5 包并行装 |
| `scripts/find-androidcli.ps1` / `find-androidcmd.ps1` / `find-androidcmd2.ps1` | 调试用 |

---

**写于**：2026-09-20
**作者**：Mavis / mavis orchestrator
**下次更新**：5 包装完后回填 §3 真实数据
