# 真机 30 分钟后台保活 — Runtime Evidence Summary（2026-09-20）

> 这是「用户在真机上跑完 30min 后台保活验收」后的回填位。
> 当前为模板。等用户填完跑回，会一起合并到 `2026-09-20-real-device-emulator-runbook.md` §4 表格与 `STATE.md` §2 表「5. 运行时 30min 后台」行。

---

## 0. APK 指纹（自动由 fingerprint 脚本产出）

| 字段 | 值 |
|---|---|
| SHA256 | （自动从 `logs/apk-fingerprint.txt` 同步） |
| versionName | `1.2.0-openpocket` |
| versionCode | `3` |
| minSdk / targetSdk | 24 / 35 |
| applicationId | `com.kaixuan.opencode.pocket` |
| entryActivity | `com.kaixuan.opencode.pocket.MainActivity` |
| 签名 | APK v2 Scheme — debug keystore |

> 自动化验证步骤：`cd frontend && npm run verify:android`
> 第 1 步就是本指纹脚本，会自动比对 runbook SHA256 是否飘移。

## 1. 设备与系统环境

| 字段 | 期望值 | 实测 | 来源 |
|---|---|---|---|
| 设备型号 | Android 13+ 真机 | ⬜ | `adb shell getprop ro.product.model` |
| 厂商 / ROM | 任意 | ⬜ | 厂商·型号 |
| Android 版本 | ≥ 13 | ⬜ | `adb shell getprop ro.build.version.release` |
| USB 调试 | 已开 | ⬜ | 设置 |

## 2. 30 分钟后台验收

| 验收项 | 期望 | 实测 |
|---|---|---|
| ⏱ 实际等时 | 30 min | ⬜ |
| App 进程是否存活 | 全部 30 min 期间持续 | ⬜ |
| 通知「AI 任务进行中」是否 30 min 可见 | ✅ | ⬜ |
| 流消息回前台是否完整 | ✅ | ⬜ |
| `AiStreamService onStartCommand` 命中次数 | ≥ 1 | ⬜ |
| `AiStreamKeepalive: keepalive sent` 命中次数 | ≥ 1 | ⬜ |
| logcat 中 "Watchdog triggered" 命中 | 0 | ⬜ |
| `dumpsys deviceidle m. whitelist` 含 `kaixuan.opencode.pocket` | ✅ | ⬜ |
| OEM 后台杀进程日志 | 0 条 | ⬜ |

## 3. Perfetto trace（如采）

| 关键看板 | 期望 | 实测 |
|---|---|---|
| `AiStreamService` 进程 30 min `running` | ✅ | ⬜ |
| 主线程 `Deep Idle` 区段数 | 0 | ⬜ |
| SSE chunk 平均到达间隔 | ≤ 2s | ⬜ |
| WakeLock 持锁时间 | ≥ 28 min（持续跑流应给锁） | ⬜ |
| 文件名 | — | ⬜ |
| 文件大小 | — | ⬜ |
| 路径 | `test-evidence/2026-09-20-ai-bg30m.pftrace` | ⬜ |

## 4. 失败明细（如有，按 4 件套排障）

| 排障维度 | 命中 | 描述 |
|---|---|---|
| App 被杀 | ⬜ |  |
| 流中断 (Watchdog) | ⬜ |  |
| FGS 没起 | ⬜ |  |
| 网络断 (厂商策略) | ⬜ |  |

## 5. 验收结论

- [ ] **PASS** —— 30 min 后台保活已验证，通知/流/FGS/Watchdog 全部达标
- [ ] **PASS (partial)** —— 通过但有非关键异常
- [ ] **FAIL** —— 关键异常，按 §4 表回溯

## 6. 后续动作清单

| 顺序 | 动作 | 命令 / 文件 |
|---|---|---|
| 1 | 把本文件 1-5 节回填完整 | 手填 / 粘贴 |
| 2 | 把数据折叠进 `2026-09-20-real-device-emulator-runbook.md` § 4 表格 | edit |
| 3 | 把 `STATE.md` §2 表「5. 运行时 30min 后台」行的「⏳ Perfetto trace 待采」改 ✅ | edit |
| 4 | 提交推送（commit #XX） | `git add … && git commit … && git push` |
| 5 | `update_goal status: complete` | mavis tool |

---

**写于**：2026-09-20 · agent 模板 · 等用户真机数据
