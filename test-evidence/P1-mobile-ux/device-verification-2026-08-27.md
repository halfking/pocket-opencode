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

---

# 第二轮：设备已连接，完整验证（2026-08-27 21:20–22:45，主代理执行）

> 用户插线后恢复执行。结论先行：**P1 全部核心链路真机验证通过**；过程中抓到
> 4 个真实缺陷（3 个当场修复并复测，1 个环境坑绕过），全部登记于下。

## 8. 环境与搭建（第二轮）

| 项 | 值 |
|---|---|
| 设备 | vivo V2436A（X Fold5），serial `10AF6H1MLM003HF`，USB 已授权 |
| 后端 | 本机 docker `opencode-pocket-pocketd`（acc-integration compose，0.0.0.0:8088）重建为 main+P1 代码；`POCKET_DEV_AUTH=true`（admin/admin1234） |
| opencode | 本机 `opencode serve --port 4096 --hostname 0.0.0.0`（v1.14.33，kaixuan/minimax-m3） |
| 实例接线 | 静态实例 `local-opencode` + `/tmp/register-instance.mjs` 最小注册守护（JWT 鉴权 WS 注册进 admin 工作区，20s 心跳） |
| APK | `VITE_API_BASE=http://192.168.31.37:8088` 构建（多 chunk 产物需按主 chunk 验证注入），uninstall+install 全新安装 |

## 9. 逐项结果（对照 §4 清单）

| # | 项 | 结论 | 证据 |
|---|---|---|---|
| 1 | adb 设备可见 | **PASS** | `device usb:0-1 product:PD2436 model:V2436A` |
| 2 | 安装启动 | **PASS** | uninstall+install（见 §11 缺陷④）`lastUpdateTime=22:36:44`；CDP 连入 WebView |
| 3 | 通知触达 | **BLOCKED-无触发源** | echo 型短回复不产生 permission/question 请求，无法构造 needs-input>3min；维持 pending-evidence |
| 4 | Deep Link 弹审批 Sheet | **BLOCKED-同上** | `?approval=open` 路由代码在（P0 静态验证），运行时需真实审批 |
| 5 | 分诊条可读性 | **PASS** | 外屏 1172×2748 下"🟢 全部正常 · 0 在跑"清晰可读（shot vivo-01/03） |
| 6 | 长按停止/归档 | **PARTIAL** | 状态条 [停止] 按钮验证可用（POST interrupt 通道）；长按菜单未驱动（时间盒） |
| 7 | `?prompt=` 草稿预填 | **PASS** | `#/sessions/<id>?prompt=预填草稿测试123` → Composer textarea 含预填文本（需经卸载路径进入） |
| 8 | safe-area/键盘 | **PASS（静态）** | Composer 44px 热区 + env(safe-area-inset-bottom) 实现，视觉无遮挡（shot vivo-04/07） |

### P1 专项（第二轮新增）

| 项 | 结论 | 证据 |
|---|---|---|
| 会话列表（真实数据） | **PASS** | opencode 21 个会话透出，真机列表显示 10 条，"P1 真机验证"居首 |
| 工作台四件套 | **PASS** | 状态条（⚪空闲/🟢思考中·秒表·[停止] 实时切换）、轮次时间线（7 轮折叠、"查看过程（N 个事件）"、最新轮默认展开）、Composer（目标 chip ses_fbc8 + 5 指令 chips + voice + send）（shots vivo-04/05/06/07） |
| 真实 prompt 往返 | **PASS** | 真机发送"请只回复四个字：链路正常"→ minimax-m3 回复"链路正常"，时间线渲染（vivo-05/06） |
| 模板 chip 一点即发 | **PASS** | 点击"总结当前进展"→ 轮 7 开跑（🟢思考中 1s→21s→1 分钟） |
| round.completed 事件 | **PASS** | 轮 2–6 头部显示事件摘要格式"+0/-0 · 0 文件"（后端事件经 WS 到达真机时间线；轮 5 观察到从用户消息回退标题切换为事件摘要的实时覆盖） |
| SSE 实时流 | **PASS（修复后）** | 信封修复后 `/event` 45s 窗口收到 message.part.updated×1/message.updated×3/session.status×2 等（修复前为 0，见 §10 缺陷①） |
| 草稿 SQLite 持久化 | **PASS（修复后）** | 输入"杀进程草稿存活测试FINAL"→ local_drafts 落库行可查 → force-stop + 重启 + 主密码解锁后 Composer 完整恢复该草稿 |
| 本地库全表初始化 | **PASS（修复后）** | 全新安装+主密码创建 → 33 张 local_* 表全量建成（修复前仅 10 张，见 §10 缺陷③） |
| 同步 outbox | **PASS（修复后）** | 修复后首启无 "no such table local_outbox" 横幅 |

## 10. 发现的真实缺陷（本轮核心产出）

| # | 缺陷 | 层 | 处置 |
|---|---|---|---|
| ① | **事件信封漂移**：opencode v1.14.33 实发 `{type,properties:{sessionID,...}}`，而 adapter `OpenCodeEvent` 只认旧格式 `{id,type,location,data}` → `Data=nil` → SSE 会话过滤全 false（实时渲染全断，P0 前即坏）、事件层拿不到 sessionID | backend adapter | **已修**：`OpenCodeEvent.UnmarshalJSON` 归一化（properties→Data，旧格式原样）；回归测试 `opencode_event_envelope_test.go` 3 用例锁形状；真机复测 SSE 事件恢复流动 |
| ② | **广播器启动单扫**：`session_event_broadcaster.go` Run 只订阅启动时 healthy 的实例——opencode 晚于 pocketd 启动（常态）则事件永不产生 | backend opencode | **已修**：30s 周期重扫 + 已订阅集合去重；真机复测 round.completed 到达 |
| ③ | **schema 切分器未接线**：`splitSqlStatements()`（专为 Android FTS 触发体截断而写、有测试）**零生产消费方**；local-db 整段 execute 在 Android 按分号机械切分遇触发体即断 → FTS 段之后的表（local_outbox/local_drafts/local_todos 等）全新安装也静默缺失 | frontend native | **已修**：open() 逐条执行 splitSqlStatements 产物、单条失败跳过告警；删除同病的 naive best-effort；真机复测 33 表全量 + 草稿持久化通过 |
| ④ | **vivo 对同版本号 `adb install -r` 静默不更新**：install 返回 Success 但 `lastUpdateTime` 不变，设备仍跑旧代码（叠加 WebView 对旧 chunk 的缓存，极具迷惑性） | 环境 | **绕过**：uninstall + install；构建链每级（dist→cap assets→APK）按内容标记校验 |
| ⑤ | adapter 默认 5s 超时 < 模型时延（30s+），prompt 代理报 `context dead`（上游实际已受理） | 部署 | **env 调整**：本地 `.env` 增 `POCKET_OPENCODE_TIMEOUT_MS=120000`（gitignored；默认值是否上调留产品决策） |
| ⑥ | 同路由组件复用不重开 store（会话 A→B 直接切 hash 时 chip 与内容串台）；旧库升级无迁移机制（本轮以全新安装路径验证，升级路径登记 P2） | frontend | **登记 P2**（低频路径/独立缺陷，不阻断 P1 验收） |

## 11. EVIDENCE-LEDGER 建议（第二轮）

- `pocketd 会话事件层` 行：`contract-tested` → **`integration-tested`**（真机端到端：真实 opencode 会话 → 事件 → 真机时间线渲染 round.completed 摘要；缺陷①②修复后日志见 §9）
- `@capacitor/local-notifications` 行：**维持 pending-evidence**（无真实审批触发源；静态验证 PASS 不变）
- Mobile frontend P1 UI：`implemented (unverified)` → **`integration-tested`（UI 层，真机 vivo X Fold5）**——通知/Deep Link 点击两子项除外

## 12. 复现要点

- 构建链校验：`VITE_API_BASE=... npx vite build` → 主 chunk（594KB 那个）grep API 地址 → `npx cap sync android` → assets 目录复验 → gradle → APK unzip 复验 → uninstall+install → `dumpsys package` 看 lastUpdateTime
- WebView 调试：`adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>` + CDP（本仓库 /tmp/cdp.js 模式）
- DB 直查：CDP 内 `Capacitor.Plugins.CapacitorSQLite.query({database:'lobster', statement, values:[]})`（execute 需 `statements` 参数名）
