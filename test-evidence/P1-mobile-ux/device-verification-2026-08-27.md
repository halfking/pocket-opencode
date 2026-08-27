# vivo X Fold5 真机验证 — 设备未连接（整单 blocked）

> **日期**: 2026-08-27 19:20–19:40 (CST)
> **验证人**: 子代理 D（P1 多代理并行 · 真机验证）
> **验证对象**: P0 指挥中心六项（commit 链 `6bc6d18` → `f394aac`，CI run `33066231321` 全绿）
> **总结论**: **BLOCKED — vivo X Fold5 全程未通过 USB 出现**。5 轮 `adb devices`（含 kill/restart server、60s+ 等待）与 macOS USB 总线扫描均无设备。运行时验证零项可执行；已完成 APK 构建链路复现与产物静态验证（见 §3），为下次真机验证扫清构建障碍。

---

## 1. 环境

| 项 | 值 |
|---|---|
| Host | macOS 15.5 (26.5) arm64（macOS Sequoia） |
| adb | `/Users/xutaohuang/Library/Android/sdk/platform-tools/adb`（不在 PATH，需 export） |
| JDK | Oracle JDK 21.0.6（`/Library/Java/JavaVirtualMachines/jdk-21.jdk`，符合 CI `cd321fa` 的 Java 21 要求） |
| 构建工作目录 | `/tmp/opencode-pocket-d`（detached @ `f394aac`，工作区 clean，未触碰主树代码） |
| APK | 本地构建（见 §2），未上真机 |

### adb 原始输出（5 轮，全部为空）

```
$ adb devices -l
List of devices attached
```

轮次时间线（本会话内）：
1. 19:20 初次检查 — 无设备（连 `unauthorized` 条目都没有，说明 USB 层无连接，非授权问题）
2. 19:2x `adb kill-server && adb start-server` 后复查 — 无设备
3. 等待 60s 后复查 — 无设备
4. 19:39（APK 构建完成后）复查 — 无设备
5. 19:40:10 终检 + `system_profiler SPUSBDataType | grep -i "vivo\|android"` — **0 匹配**（USB 总线上无任何 Android/vivo 硬件）

结论：手机未插线或线/口故障，**不是**授权弹窗未确认（授权未确认会显示 `unauthorized` 状态）。按任务纪律记为合法 blocked。

---

## 2. APK 获取

**路线 ① CI 产物 — 不可用**：`gh run view 33066231321` + `gh api .../artifacts` 确认该 run 虽全绿（typecheck+build+node tests 23s；android assembleDebug 2m26s）但 **无任何 artifact**——`frontend.yml` 的 upload-artifact 步骤带 `if: failure()`，仅失败时上传。CI 路线结构性无产物。

**路线 ② 本地构建 — 成功**，命令与 CI `frontend.yml` android 作业一致（npm ci → `npm run build:fast` → `npx cap sync android` → `./gradlew --no-daemon assembleDebug`，JDK 21）：

| 产物 | 值 |
|---|---|
| 路径 | `/tmp/opencode-pocket-d/frontend/android/app/build/outputs/apk/debug/app-debug.apk` |
| 大小 / MD5 | 25,201,907 bytes / `2fd07adfd9ff061761edcc33a43f3853` |
| applicationId | `com.kaixuan.opencode.pocket` |
| versionCode / versionName | 1 / 1.0（debug 变体） |
| minSdk / targetSdk | 24 / 35 |
| cap sync 插件 | @capacitor-community/sqlite@8.1.0、text-to-speech@8.0.2、@capacitor/app@8.1.1、**@capacitor/local-notifications@8.3.1** |

本地构建踩到两个坑（下次验证直接规避）：
1. `frontend/android/local.properties` 缺失 → `SDK location not found`。已写入 `sdk.dir=/Users/xutaohuang/Library/Android/sdk`。
2. Maven Central 直连 `kotlin-build-common-2.0.21.jar` TLS 握手被断（Remote host terminated the handshake）→ 需走本机代理 `127.0.0.1:7897`（gradle `-Dhttps.proxyHost/Port`）。

**⚠️ 遗留环境缺口（下次真机验证前必须解决）**：本次构建未设置 `VITE_API_BASE`（`frontend/.env.android-dev` 不存在，`api/http.ts:6` 默认空字符串=同源 `capacitor://localhost`）→ **该 APK 启动后无法连任何后端**，通知/审批验证需后端配合。真机验证前需 `VITE_API_BASE=http://<局域网IP>:8088` 重新构建（参照 `scripts/build-mobile.mjs` 与 `capacitor.config.ts` 注释），且手机与后端主机同网段。

---

## 3. APK 产物静态验证（blocked 情况下的增量证据）

虽无法运行，但用 aapt / 解包对 APK 做了静态核验，证明 P0 移动链路的**构建产物层面**落地完整：

| 静态检查 | 命令要点 | 结果 |
|---|---|---|
| 通知权限已声明 | `aapt dump badging` | `android.permission.POST_NOTIFICATIONS`、`SCHEDULE_EXACT_ALARM`、`RECEIVE_BOOT_COMPLETED`、`WAKE_LOCK` 均在 manifest ✅ |
| 通知服务已打进 APK | `aapt dump xmltree ... AndroidManifest.xml` | `com.capacitorjs.plugins.localnotifications.TimedNotificationPublisher` + NotificationService receiver ✅ |
| 通知逻辑已打进 JS bundle | 解包 `assets/public/assets/*.js` grep | `localNotificationActionPerformed`（点击监听）与通知标题 `"需要审批"` 均存在于 `index-BbST5wFu.js`（另 `web-Cqn9aVCD.js`）✅ |
| 源码触发条件核对 | `frontend/src/composables/useApprovalAlerts.ts` | 阈值 `ALERT_AFTER_MS = 3*60_000`（§4.2-5），30s 轮询 + pending watch 即查；点击→`router.push('/sessions/:id?instance_id=…&approval=open')`（JS 层导航，非 OS intent-filter，manifest 无 deeplink scheme 属预期）✅ |

即：**"构建出含通知能力的 APK" 这一半已复现；"真机上通知真的弹出来、点了真的弹 Sheet" 这一半仍零验证。**

---

## 4. 真机验证清单逐项结果

| # | 验证项 | 操作步骤 | 结果 | 截图 | 结论 |
|---|---|---|---|---|---|
| 1 | adb 可见 vivo 设备 | `adb devices -l` × 5 轮（19:20–19:40，含 kill/restart、60s 等待、USB 总线扫描） | 见 §1，全程空 | 无 | **blocked**（设备未连接） |
| 2 | 安装 APK 并启动 | 预备完成：APK 已构建（§2），`adb install` 待设备 | 未执行 | 无 | **blocked**（依赖 #1） |
| 3 | 通知触达（needs-input >3min） | 预备完成：触发条件已核对（§3），需真机+后端构造 pending | 未执行 | 无 | **blocked**（依赖 #1、#2、后端） |
| 4 | Deep Link → 审批 Sheet | 预备完成：点击→JS 路由→`SessionConversationView.vue:146` `q.approval==='open'` 强制弹 Sheet（源码核对） | 未执行 | 无 | **blocked**（依赖 #3 的通知点击；无通知时可降级用 App 内路由构造 `?approval=open`，仍需设备） |
| 5 | 分诊条/健康信号可读性（外屏截图） | 预备命令：`adb exec-out screencap -p > /tmp/xxx.png` | 未执行 | 无 | **blocked**（依赖 #1） |
| 6 | 长按停止/归档菜单 | 未执行 | — | 无 | **blocked**（依赖 #1） |
| 7 | `?prompt=` 草稿预填 | 源码核对：`SessionConversationView.vue:149` `promptText || q.approval !== undefined` 分支存在；真机需 App 内深链/URL 构造 | 未执行 | 无 | **blocked**（依赖 #1） |
| 8 | safe-area/键盘遮挡基线观察（P1 输入系统） | 未执行 | — | 无 | **blocked**（依赖 #1） |

---

## 5. 汇总

| 状态 | 数量 | 项 |
|---|---|---|
| pass | 0 | — |
| fail | 0 | — |
| blocked | 8 | 全部 8 项（根因单一：设备 #1 未连接） |

## 6. 对 EVIDENCE-LEDGER `@capacitor/local-notifications` 行的建议

**建议状态：维持 `pending-evidence`（不提升、不降级）**，但可增补备注（引用本文件）：

> 2026-08-27 真机验证尝试 blocked（vivo X Fold5 未连接，`test-evidence/P1-mobile-ux/device-verification-2026-08-27.md` §1）。增量进展：本地构建链路复现成功（JDK21+代理 7897，产物 MD5 `2fd07adf…`），静态验证 manifest 已含 POST_NOTIFICATIONS/SCHEDULE_EXACT_ALARM 权限与 TimedNotificationPublisher 服务、JS bundle 已含通知触发与点击导航逻辑。运行时触达/Deep Link 仍零验证，提升条件不变。另登记：真机可验证前需以 `VITE_API_BASE` 指向局域网后端重新构建（§2 遗留缺口）。

（注：该行现值为 `pending-evidence`，docs/STATUS-MATRIX 对 UI/通知整体的级别为 `implemented (unverified)`——两者均不应因本次变更；本次既无真机 pass 证据，也无任何 fail 证据。）

## 7. 下次真机验证的执行清单（交接）

1. 手机插线（数据线非充电线）→ 确认 `adb devices` 出现 `device` 状态（vivo 需开启 USB 调试 + USB 安装，见 `VIVO_USB_DEBUG_GUIDE.md`）。
2. 重建带后端地址的 APK：`cd /tmp/opencode-pocket-d/frontend && VITE_API_BASE=http://<LAN-IP>:8088 npm run build:fast && npx cap sync android && cd android && ./gradlew --no-daemon -Dhttps.proxyHost=127.0.0.1 -Dhttps.proxyPort=7897 assembleDebug`（local.properties 已写好）。
3. `adb install -r app-debug.apk` → 启动 → 首启授权 POST_NOTIFICATIONS。
4. 后端构造 pending（permission 或 question）→ 保持前台/近后台 ≥3min30s（3min 阈值 + 30s 轮询）→ 观察通知。
5. 点通知 → 验证审批 Sheet；补做 §4 #5–#8 与截图（外屏 6" 尺度重点截 L0 分诊条/健康信号）。
