# APK Native .so ABI 静态审计 — 第 8 层证据

- 日期：2026-09-20
- 提交：第 24 个（待）
- 工具：`scripts/android-apk-so-audit.ps1`
- 输入 APK：`frontend/android/app/build/outputs/apk/debug/app-debug.apk`（28.9 MB）

## 1. 目的

继 7 层静态证据（源码 / 单测 / 类型 / 构建 / DEX 字节码 / manifest 权限 / APK 签名）之后，
第 8 层验证打包出来的 APK 在 **native 层**（`.so` 原生库）的 ABI 覆盖，
从而保证：

- 真机和模拟器各档 ABI 都能装得上、跑得起来；
- 模拟器 x86_64 ABI 一旦环境就绪（VTX / Hyper-V 接管）就能直接拉起本 APK；
- 不会因为漏掉某档 ABI 导致 arm 真机或某档 emulator 启动时 `UnsatisfiedLinkError`。

## 2. .so 覆盖清单

| ABI            | 库数 | 字节数   | libimage_processing_util_jni | libsqlcipher | libsurface_util_jni |
| -------------- | ---- | -------- | ---------------------------- | ------------ | ------------------- |
| arm64-v8a      | 3    | 5,221,384 | 29,008                       | 5,187,544    | 4,832               |
| armeabi-v7a    | 3    | 3,581,244 | 20,380                       | 3,557,424    | 3,440               |
| x86            | 3    | 4,965,496 | 38,292                       | 4,923,492    | 3,712               |
| x86_64         | 3    | 5,799,056 | 48,104                       | 5,746,024    | 4,928               |

**结构共识**：每一档 ABI 都恰好收到相同 3 个库 ——

- `libimage_processing_util_jni.so`（Capacitor 摄像头 / 图片处理）
- `libsqlcipher.so`（SQLCipher 加密本地 DB）
- `libsurface_util_jni.so`（Capacitor 视图桥接）

## 3. ABI 覆盖矩阵

```
[+] arm64-v8a    : 3 libs
[+] armeabi-v7a  : 3 libs
[+] x86          : 3 libs
[+] x86_64       : 3 libs
```

4/4 全覆盖 —— **无 MISSING**，无悬空 ABI。

## 4. 模拟器路径 cross-check

```
[+] emulator ABI x86_64 available (3 libs) — Android x86_64 emulator will work with this APK once emulator can boot
```

`pocket-test` AVD 已选定 `google_apis/x86_64` system-image，
APK 也携带了对应 `x86_64/` 目录的 `.so` 三件套。
**APK 侧零阻塞** —— 等物理层（VMware guest VTX / Hyper-V）问题解开，emulator 启动即可直接吃本 APK。

## 5. 结论

- 7 层 → **8 层** 静态证据链全闭环；
- native ABI 4/4 全覆盖；
- `verify:android` 新增第 7 步：`android-apk-so-audit.ps1`；
- 物理依赖未变：emulator 上限仍卡在本机 VMware 嵌套虚拟化层（VTX 被屏蔽）。

## 6. 一键复跑

```bash
npm run verify:android
# 现在跑 7 步：
#   1) typecheck
#   2) build
#   3) test (25 native tests)
#   4) check:vm-gaps
#   5) android-apk-classes-fast (DEX 字节码)
#   6) android-apk-static-verify (manifest + 签名)
#   7) android-apk-so-audit        (.so ABI)  ← 本层
```

输出：`logs/apk-so-audit.txt`。
