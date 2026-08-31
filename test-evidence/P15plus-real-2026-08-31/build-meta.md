# P1.5+ 真机构建元数据 — 2026-08-31 19:20 CST

> **目的**: 留存构建链与产物指纹,作为真机验证的产物溯源证据。
> **结论**: APK 25.86MB,主 chunk 含 `http://192.168.31.37:8090` × 2;构建链 4 级 (dist → cap assets → APK) 时间戳对齐。

## 1. 构建参数

| 项 | 值 |
|---|---|
| 工作区 | `/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket` |
| branch / HEAD | `main` @ `de4f6af` (working tree 含 21 文件未提交 diff) |
| 工作区 dirty | 21 modified (frontend + MainActivity.java) |
| JDK | Oracle JDK 21 (`/Library/Java/JavaVirtualMachines/jdk-21.jdk`) |
| Node proxy | `127.0.0.1:7897` (Maven Central 直连 TLS 断) |
| LAN IP | `192.168.31.37` (en0, /24) |
| 后端 | docker `opencode-pocket-pocketd-local-opp` 0.0.0.0:8090→8088 (healthy 6h) |
| VITE_API_BASE | `http://192.168.31.37:8090` |
| 构建模式 | `android-dev` (`build-mobile.mjs android dev`) |

## 2. 4 级构建产物指纹

| 级 | 路径 | 大小 | MD5 | 时间 |
|---|---|---|---|---|
| dist (主 chunk) | `frontend/dist/assets/index-cRvclkN-.js` | 640 KB | (sha in vite summary) | 19:20:48 |
| cap assets | `frontend/android/app/src/main/assets/public/assets/index-cRvclkN-.js` | 655654 B | `3af582a7ba02875661dcf37942a4f7c5` | 19:20:49 |
| APK | `frontend/android/app/build/outputs/apk/debug/app-debug.apk` | 25865568 B (25.86 MB) | `89708cce9d35eef27b41f11cba96ad78` | 19:20:58 |
| 安装时间戳 | `dumpsys package com.kaixuan.opencode.pocket` → `lastUpdateTime` | (post-install 见 apk-install-verify.log) | — | — |

## 3. VITE_API_BASE 注入校验

```bash
$ unzip -p frontend/android/app/build/outputs/apk/debug/app-debug.apk \
        assets/public/assets/index-cRvclkN-.js | grep -c "192.168.31.37:8090"
2
```

- **结果**: 命中 2 处 ✅
- `build-mobile.mjs` 自带 sanity check,build 日志: `[build-mobile] sanity check passed: http://192.168.31.37:8090 present in bundle`

## 4. 构建日志关键节点

| 步骤 | 命令 | 结果 |
|---|---|---|
| vite build | `node scripts/build-mobile.mjs android dev` | `✓ built in 2.41s`, 主 chunk `index-cRvclkN-.js` 640KB |
| cap sync | 同上 (`cap sync android`) | sync 0.06s, 5 plugins (@capacitor-community/sqlite@8.1.0, text-to-speech@8.0.2, @capacitor/app@8.1.1, @capacitor/local-notifications@8.3.1, @capacitor/status-bar@8.0.3) |
| gradle | `./gradlew --no-daemon -Dhttps.proxyHost=127.0.0.1 -Dhttps.proxyPort=7897 assembleDebug` | `BUILD SUCCESSFUL in 8s` (247 tasks, 27 executed, 220 up-to-date) |

## 5. 已知避坑

- **vivo `adb install -r` 静默不更新**(P1 缺陷④): 必须 `uninstall + install`
- **proxy 7897** 是必需,否则 Maven Central TLS 握手失败
- **`local.properties`** 已就位 (`/Users/xutaohuang/Library/Android/sdk`)