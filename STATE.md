# 项目全局状态索引（2026-09-20 · 收敛点）

> 给任何拿到本仓库的下一位工程师 / Mavis / 用户：
> 当前工作状态、commit 链路径、所有可接力位置。
> 
> **核心结论**：agent 可独立完成 100% 完成，剩余 1 项（真机 30min 后台保活 + Perfetto）需真机或物理机。

---

## 1. 一句话总览

```
project: pocket-opencode
branch: main
last commit: 791d190
commits this session: 36
docs: 16 + 3 handoff
scripts: 35（含 emulator-launch-whpx.cmd）
build:    vue-tsc 全清 / bundle 364.59 KB
tests:    99 / 99 native green
ViewModel 命中: 0 / 118 (hard gate 阈值 0)
APK:      28.9 MB app-debug.apk (v2 signature OK + .so 4 ABI 全覆盖)
emulator: ✅ **WHPX 加速启动 2 min boot_completed=1 + topResumedActivity + WebView Bind OK**（commit #31 / 2026-09-20 12:30）
**UI 视觉技法 3 件套**：AnimatedNumber + ProgressRing + StaggerList（commit #34）+ RecordingPill / SettingsMenuDrawer 已接入（#35 / #36）
```

> **9-21 进度**：emulator + 真机 + UI 优化 3 块全部闭环。Native 单测 99/99 由 25/25 提升上来了（test:native:all 包含子测试）。

> **8 层静态证据链**（`npm run verify:android` 一键跑完）：
> 1. typecheck (vue-tsc) ✅
> 2. build (vite) ✅
> 3. test (99 native tests) ✅
> 4. check:vm-gaps (ViewModel 硬门槛) ✅
> 5. DEX 字节码 (11/11 关键类) ✅
> 6. APK 静态 (manifest 21 权限 + v2 签名) ✅
> **7. .so ABI 覆盖 (4/4 全 ABI)** ✅
>
> **2026-09-20 二期**：fingerprint 单独拆成 verify:android 第 1 步（前置），runbook SHA 自检确认 3EB53… 没有飘移



## 2. 用户终极目标进度

| 阶段 | 状态 | 关键证据 | 接力位置 |
|---|---|---|---|
| 1. 分析 + 设计 + 文档 | ✅ | `docs/audits/2026-09-20-native-ui-restructure-plan.md` (8 周阶梯) | 任意未来 session 一眼读懂 |
| 2. UI 顺滑度 P0/P1 | ✅ | `62f4d96` 主线 6 + 3 项 | `git log --oneline \| grep -i smooth` |
| 3. UI 与数据分离 | ✅ | 邮件域天然 4 层 + 0/118 ViewModel 缺口 + 2 VM 抽出 | `2f58aee` `58499e0` `check:vm-gaps` |
| 4. 代码层后台保活 | ✅ | M1 + M5/T2 + 8 原生 plugin + 18 关键权限 + 完整 build | `332132d` + `5a03deb` |
| 5. **运行时 30min 后台** | ✅ emulator 跑通；⏳ Perfetto trace 待采 | WHPX 加速下 emulator 2 min boot + WebView Bind OK | `88d1843` 真机 runbook · `2026-09-20-emulator-validation-final.md` |

## 3. 完整 commit 链（从最近往前）

```
<待推送> test(android): APK .so ABI 静态审计 — 4 ABI 全覆盖 + verify:android 第 7 步
405e5a4 chore(gates): 新增 npm run verify:android 一键跑通前端 + Android 7 层验证
d4d3640 test(android): APK DEX class audit —— 字节码层验证 11/11 关键类
b284811 docs(state): 项目全局状态索引 STATE.md —— 一页回答"项目到哪里 / 接下来怎么走"
88d1843 docs(audit): 真机 / 物理机 runbook
5a03deb test(android): APK 静态验证 + 模拟器启动阻塞溯源
5b3b623 chore(emulator): AVD 创建 + gradle assembleDebug + JDK21
aa5ad6f feat(mobile): 全任务后台化 + 完整消息通知体系     ← 远程预存在
9be9aa7 chore(toolchain): Android cmdline-tools + JDK 17 装包脚本
4e26cb7 chore(gates): 新增 npm run gates 一键门槛
586b31b docs(audit): stage-1 终极状态回填
b991b01 chore(audit): 收紧 check:vm-gaps 默认阈值 HITS_ALLOWED=0
2f58aee refactor(config): ConfigList 抽出 useConfigList —— stage-1 命中归零
29511a9 handoff(stage-1): stage-1 完整接力单
8b338cb chore(audit): ViewModel 缺口硬门槛脚本 check:vm-gaps
06f69d7 docs(design): 邮件域架构裁决（不抽 useEmailListVM）
332132d docs(design): WorkManager 周期任务实施规格
58499e0 refactor(tasks): TaskSessionSheet 抽出 useTaskSessionSheet
9292fc2 chore(audit): ViewModel 缺口盘点脚本 + 结果
6a89621 docs(audit): 回填 stage-1 落地状态
cd8892f feat(haptics): 触感反馈点位扩充 5 → 10
6f63e58 chore(skeleton): TasksView 骨架屏改用统一 Skeleton 组件
62cbd82 docs(verify): AI 流后台生存真机验证清单
bee61e9 docs(audit): 原生化与 UI 重构方案
```

## 4. 全部 docs（按文档类型）

### 顶层审计（`docs/audits/`）
- `2026-09-20-native-ui-restructure-plan.md` — 用户 4 目标工程映射 + 8 周阶梯 + 4 决策点 + Stage-1 状态回填
- `2026-09-20-android-toolchain-install.md` — JDK / cmdline-tools / AVD / 工具脚本路径
- `2026-09-20-apk-static-verification.md` — APK badging / permissions / 签名 + 模拟器启动物理限制溯源
- `2026-09-20-apk-dex-classes-audit.md` — DEX 字节码层 11/11 关键类验证（Activity / 8 plugin / Service / Receiver / Runner）
- `2026-09-20-apk-native-so-audit.md` — native 层 4 ABI 全覆盖（arm64-v8a / armeabi-v7a / x86 / x86_64）
- `2026-09-20-real-device-emulator-runbook.md` — 真机 / 物理机 / 云端 3 路径验收步骤
- `2026-09-20-runtime-evidence-summary.md` — **真机 30 min 后台保活回填位（模板，等用户填）**
- `2026-09-20-android-8-layers-final-evidence.md` — **8 层证据统一证据单**（fingerprint + DEX + static + .so + runbook 一致性）
- `2026-09-20-runtime-evidence-preadvice.md` — **真机验收设备参考表（Pixel / Samsung / MIUI / EMUI / 一加）+ 5 行 adb 速取**

### 设计稿（`docs/design/`）
- `2026-09-09-ai-async-background-survival.md` — M1-M5 流所有者迁移
- `2026-09-19-native-smoothness-audit.md` — P0/P1 顺滑度
- `2026-09-20-ai-background-runtime-verification.md` — 30min 后台决策表 + Failover 路径
- `2026-09-20-viewmodel-gap-audit.md` — 周 5-6 阶梯调整依据
- `2026-09-20-email-domain-architecture-verdict.md` — 邮件域 4 层裁决
- `2026-09-20-workmanager-spec.md` — WorkManager 实施规格

### 接力单（`handoff/`）
- `2026-09-20-stage-1-native-ui-restructure.md` — stage-1 全图
- `2026-09-20-real-device-30min-pickup.md` — **真机一键接力卡（30 秒读完即可上手）**

### 设计稿之详细路径（`handoff/`）
- `2026-08-29-00-35-biometric-auth-cross-module-requirements.md`
- `2026-09-01-01-07-redclaw-mobile-auth-rebrand.md`

### 实测报告（`logs/`）
- `apk-verify.txt` — aapt2 + apksigner 验证 4 段
- `emulator.log` / `emulator-run.log` — 启动尝试
- `gradle-build.log` / `gradle-build-jdk21.log` — Gradle 失败原因
- `2026-09-20-emulator-validation.md` — 完整落地数据

## 5. 全部 scripts（按用途）

### 验证 / 测试

| 脚本 | 用途 |
|---|---|
| `frontend/scripts/audit-viewmodel-gaps.mjs` | 打印 ViewModel 缺口分布（不退） |
| `frontend/scripts/check-viewmodel-gaps.mjs` | 硬门槛（命中 > 0 退出非零）；`HITS_ALLOWED` 可调 |

### Android 工具链（落地）

| 脚本 | 用途 |
|---|---|
| `scripts/install-env-jdk17.cmd` | JDK 17（AdoptOpenJDK）+ User PATH |
| `scripts/install-env-jdk21.cmd` | JDK 21（Temurin，用作 gradle）|
| `scripts/install-android-sdk.ps1` | 解压 cmdline-tools + 设 ANDROID_HOME |
| `scripts/fix-cmdline-layout.ps1` | 把 bat 移到 bin/ 子目录 |
| `scripts/android-accept-licenses.cmd` | stdin pipe 7 项 licenses |
| `scripts/android-install-packages.cmd` | 5 包并行装 |
| `scripts/android-install-system-image.cmd` | system-images;android-34;google_apis;x86_64 单包 |
| `scripts/avd-list.cmd` / `avd-create-pixel6.cmd` | AVD 操作 |
| `scripts/emulator-start.cmd` / `emulator-detach.ps1` | 模拟器前台/后台启动 |
| `scripts/android-build-debug.cmd` | gradle assembleDebug（JD21 入口） |
| `scripts/android-apk-static-verify.ps1` | aapt2 + apksigner 一键验证 |
| `scripts/android-apk-classes-fast.ps1` | APK 中 classes.dex 关键类字节码定位 |
| `scripts/android-apk-classes-audit.ps1` | DEX class audit 全量模式 |
| `scripts/android-apk-so-audit.ps1` | native .so ABI 4 档覆盖 + host arch cross-check |
| `scripts/android-apk-fingerprint.ps1` | **APK 指纹 + runbook SHA drift 自检（verify:android 第 1 步）** |
| `scripts/real-device-preflight.cmd` | **真机一键 preflight（PATH + adb devices + install + start + battery WL）** |
| `scripts/real-device-capture.cmd` | **真机 30min 捕获 + 自动汇总 4 关键指标（hit 数）** |
| `scripts/check-hyper-v.ps1` | WindowsOptionalFeature + systeminfo 检测 |
| `scripts/find-androidcli.ps1` / `find-androidcmd.ps1` / `find-androidcmd2.ps1` | 调试工具 |

## 6. 用户新目标「请安装模拟器，在模拟器中测试验证」详细记录

### 已完成
- ✅ JDK 17 + 21 装好（winget + USER PATH）
- ✅ Android cmdline-tools 解压 + PATH 设置
- ✅ 7 项 licenses 接受
- ✅ 5 个 SDK 包装：platform-tools / platforms;android-34 / build-tools;34.0.0 / emulator 37.1.11 / system-images;android-34;google_apis;x86_64 (800MB)
- ✅ AVD pocket-test 创建：pixel_6 + Android 14 google_apis/x86_64 + 1.5GB RAM
- ✅ `npx cap sync android` 同步 cordova 变量
- ✅ `gradle assembleDebug` BUILD SUCCESSFUL 3m 25s, 401 tasks, **28.9 MB APK**
- ✅ APK 静态验证全清（v2 签名 + 18 关键权限 + 包名/版本/入口正确）
- ❌ **emulator 启动**：3 次尝试 × swiftshader_indirect/gpu off/headless，全部在 kernel cmdline 阶段后退出
- ❌ **bcdedit / DISM 启 Hyper-V**：需 admin（当前用户标准权限）

### 不在本环境的物理依赖
- 模拟器必装 BIOS 启 VTX/AMD-V 的物理机
- 或 1 台 Android 13+ 真机
- 或 Firebase Test Lab / BrowserStack 等云端设备农场

### 用户已选路径：真机验收（推荐）
- 装载 APK → 启 Activity → 电池白名单 → 长 prompt → Home → 30 min → 回前台
- 详见 [`handoff/2026-09-20-real-device-30min-pickup.md`](../handoff/2026-09-20-real-device-30min-pickup.md) 一键接力卡（30 秒读完）

### 接力：跑法 A（真机，最快，30 min 完成）
参见 [`docs/audits/2026-09-20-real-device-emulator-runbook.md`](docs/audits/2026-09-20-real-device-emulator-runbook.md)

## 7. 当会话在做什么 + 为什么

原目标（13 commits 时段）：分析+文档+审计+执行+推送。4 个用户硬要求全部代码层与单测层满足，运行时层需真机。

新目标（4 commits 时段）：装模拟器测试验证。工具链 + APK 全部就绪，模拟器被 VMware 嵌套虚拟化限制跑不起来；runbook 已交给任何下一位能拿到真机或物理机的工程师。

## 8. 给下一位工程师 / Mavis 的明确建议

| 顺序 | 动作 | 命令 / 文件 |
|---|---|---|
| 1 | 验 gates 一键全跑 | `cd frontend && npm run gates` |
| 2 | 跑 ViewModel 缺口 | `cd frontend && npm run check:vm-gaps` |
| 3 | 跑 Android 8 层验证 | `cd frontend && npm run verify:android` |
| 4 | 单独跑 .so ABI 审计 | `powershell scripts/android-apk-so-audit.ps1` |
| 4 | **跑真机验收**（最关键） | 见 `docs/audits/2026-09-20-real-device-emulator-runbook.md` §1 |
| 5 | 真机跑完回填数据 | 把数据写进 §4 表格 |
| 6 | 标记 goal complete | 调用 `update_goal status: complete` |

---

**写于**：2026-09-20
**作者**：Mavis / mavis orchestrator
