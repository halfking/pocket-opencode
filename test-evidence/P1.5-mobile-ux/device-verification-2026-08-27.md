# P1.5 界面减负——vivo X Fold5 真机验证记录

> **日期**: 2026-08-27 23:14–23:30
> **执行者**: 主代理（P1.5 会话）
> **改动范围**: 纯前端界面减负（`docs/2026-08-27-p1.5-ui-declutter.md` §2）
> **结论**: **V-1..V-8 全部 PASS**；验证中发现 P1 事件/流层存量缺陷 2 项（§4，已登记 P2 缺陷池，非 P1.5 引入——P1.5 全部 diff 在展示层，未触碰流/store/事件层）

## 1. 环境

| 项 | 值 |
|---|---|
| 设备 | vivo X Fold5（V2436A / PD2436），USB 连接，serial `10AF6H1MLM003HF` |
| APK | P1.5 构建：`VITE_API_BASE=http://192.168.31.37:8088 npm run build:fast` → `npx cap sync android` → `gradlew assembleDebug`；`adb install` 后 `dumpsys package com.kaixuan.opencode.pocket`：`lastUpdateTime=2026-08-27 23:14:43`（更新生效确认；新版 adb install 默认携带替换语义，本次未复现 P1 时代 `install -r` 静默不装坑，但仍以 lastUpdateTime 为准） |
| 后端 | 本机 docker `opencode-pocket-pocketd`（0.0.0.0:8088，P1 镜像，healthy）；`opencode serve`@4096（v1.14.33，provider kaixuan/minimax-m3）；实例注册守护 `/tmp/register-instance.mjs`（instance `local-opencode`，20s 心跳） |
| 驱动 | CDP：`adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>` + `/tmp/cdp.js`（Runtime.evaluate）+ `/tmp/cdp-shot.js`（Page.captureScreenshot，本会话新增） |
| 会话 | `ses_fbc8a135effel0J0LafqJDCE8l`（"P1 真机验证"，P1 轮遗留 + 本轮新增消息） |

## 2. 构建链逐级校验（沿用 P1 §12 方法论）

| 级 | 检查 | 结果 |
|---|---|---|
| dist | 主 chunk（`index-Dg7_ORRf.js`）grep `192.168.31.37:8088` | ✅ 命中 2 处 |
| dist | `index-oLbgjYE3.css` 含 `@font-face 'Material Symbols Outlined'` + `data:font/woff2`（4KB 子集被 vite 内联） | ✅ |
| cap assets | `android/app/src/main/assets/public/assets/index-oLbgjYE3.css` 同上复验 | ✅ |
| APK | `unzip -p app-debug.apk assets/public/assets/*.css` grep 字体；`*.js` grep API 地址 | ✅ 双命中 |

## 3. 验证矩阵（V-1..V-8）

截图基线：`test-evidence/P1.5-mobile-ux/shots/vivo-p15-01..06.png`（对照 P1 基线 `../P1-mobile-ux/shots/vivo-04..07.png`）。

| # | 验证点 | 命令/方法 | 结果 | 证据 |
|---|---|---|---|---|
| V-1 | 壳层"← 会话"双头消除（AppLayout 此前未消费 `hideAppHeader`） | 打开 `#/sessions/ses_fbc8...?instance_id=local-opencode` 截图 | ✅ 仅一层应用头部（P1 基线 vivo-04 的四层 → 两层含系统状态栏） | vivo-p15-02 |
| V-2 | 头部结构 `[←][状态图标] 标题+信号副标题 [⋮]` | 同上 | ✅ 左返回/圆形状态图标/标题"P1 真机验证"/副标题信号文本/右 ⋮ | vivo-p15-02 |
| V-3 | 图标字体渲染（P1 基线真机图标渲染为 "rrow_back" 文字） | 全页截图目检 + 首页 | ✅ 所有 material-symbols-outlined 渲染为字形，无 ligature 原文泄漏 | vivo-p15-01/02/03 |
| V-4 | 运行态动态图标：progress_activity 旋转 + aria | 发送 prompt 后 CDP 读 `getComputedStyle(.status-icon .icon).animationName` | ✅ `status-spin`（动画激活）；aria-label=`思考中 · 4s · 点击停止` | vivo-p15-05 |
| V-5 | 运行态单击=停止（无确认，DD-1）+ Toast 引导 | `.status-icon` click → 读 toast | ✅ toast 文本命中"已停止，点击状态图标可继续"；pocketd interrupt 返回 204 | CDP 记录（§5 时间线） |
| V-6 | 空闲态：play_arrow + "空闲 · 点击继续"；轮次完成后 running→idle 迁移 | 轮次结束后读 `.status-icon` className | ✅ `mode-idle` + label`空闲 · 点击继续` | CDP 记录 |
| V-7 | 快速指令面板（原常驻 chips 行消除） | `.quick-btn` click 截图 | ✅ 输入区单行（无 chips 行）；面板 5 项 = 图标+完整文案（play_arrow/stop/subject/science/fast_forward），"停下"danger 红 | vivo-p15-03 |
| V-8 | ⋮ 抽屉收纳（实例信息+统计+轮摘要+导出） | `.detail-btn` click 截图 | ✅ 「💻 实例」区块显示 Local OpenCode / local-opencode（自头部副标题收纳）；统计 4 格 + 轮摘要 + 导出 Markdown 按钮 | vivo-p15-04 |

### V-5/V-6 状态机 CDP 实测时间线（关键读数）

```
23:17:09  prompt-1 发送 → icon=mode-running（aria: 思考中 · 4s · 点击停止；anim=status-spin）
23:17:24  .status-icon click → toast"已停止，点击状态图标可继续"（interrupt 204）
  ...     （该轮 opencode 侧实际已完成——见 §4 缺陷说明：完成信号不达 App）
23:21:57  prompt-3 发送（1s 间隔分步 draft→click）→ mode-running，思考中 · 5s
  ...     轮次完成后 icon 迁移 mode-idle"空闲 · 点击继续"（V-6 ✅）
```

## 4. 验证中发现的存量缺陷（登记 P2，非 P1.5 引入）

### DEFECT-P2-SSE-1（高）SSE 消息流不投递 assistant 输出且无流关闭信号

- 现象：opencode 直查（`GET 4096/session/ses_fbc8.../message`）显示 23:21:57 轮次**已完成**（assistant 已回复"链路正常-2"），App 侧同轮 **100s+ 仍 isStreaming=true**、图标久挂"思考中"、assistant 气泡从未渲染（DOM `.round-timeline .bubble` 数=1，仅用户消息）。
- 后果：完成/停止信号不可达 → 状态图标被上游数据钉死运行态。**状态机本身按其输入正确运转**（V-4/V-6 证明），输入源头（store.isStreaming）被流层误导。
- 定位线索：WebView performance 资源表显示 SSE/长连接资源在轮询循环（部分 responseStatus=0）；opencode 侧消息/活动均在（events snapshot 有 phase/round 推进）→ 断点在 pocketd SSE 桥/前端流处理层（P1 交付，P1.5 未触碰）。
- 处置：登记 P2 缺陷池（与 P2"订阅收窄 + since 追赶"同批修复最合适）。

### DEFECT-P2-DUP-1（中，存疑）同文 prompt 重复投递

- 现象：opencode 消息历史 23:17:09 与 23:17:45 存在两条**完全相同**的 user 消息（"用一句话说明你能做什么，然后开始从 1 数到 20…"），间隔 36s；两个时刻之间用户（验证驱动方）无发送操作。
- 待查：重复 POST 方在 App 侧重发逻辑或 pocketd 重试（需服务端日志）。登记 P2。

## 5. 复现要点

- 构建链与安装：§2 + `adb install`（核对 lastUpdateTime）
- CDP：`adb shell cat /proc/net/unix | grep webview` 取 pid → forward → `/tmp/cdp.js` / `/tmp/cdp-shot.js`
- 状态机读数：`.status-icon` 的 className（mode-*）与 aria-label 即信号三态 + 时长的权威读数
- 发送 prompt 的 CDP 合成输入**必须分两步 eval**（先 setter+dispatch input，隔 ≥1s 再 click send）——同 eval 内 click 会读到未刷新的 canSend（本会话教训，prompt-2 因此空转）

## 6. 遗留交接

- 手机 App 已更新为 P1.5 构建（本地库与登录态保留，主密码仍为测试值 `p1-verify-2026`——**建议用户改回**）
- 本机 pocketd 容器/opencode serve/注册守护维持运行（同 P1 交接）
- P1.5 提交不含 BottomNav.vue / router-mobile.ts / ai-chat 等并行流文件（其属主仍在活跃写入，git status 持续增长）

---

## 7. 追加轮（P1.5+，同日 23:43–23:5x）：距顶修复 + 活跃/归档分区

### 7.1 需求 A：详情页标题距顶（量化前后对照）

| 量测 | 修复前 | 修复后 |
|---|---|---|
| 详情页头部距顶 | **90px**（39 body + 39 session-view + 12 content padding） | **39px**（贴状态栏下沿）✅ |
| /ai 顶栏标题文字 top | ~78px（顶栏内双重 padding） | **51px** ✅ |

全 UI 审计 7 处 safe-area-inset-top（详见设计文档 §7.1 表）：修 AppLayout top-bar + session-view 两处双重；SettingsLLMGateway 为 fixed 自管（正确不动）；并行流 3 文件登记交接。

### 7.2 需求 B：活跃/归档分区（V-11..V-15）

| # | 验证点 | 方法 | 结果 |
|---|---|---|---|
| V-11 | 分段切换渲染，默认活跃 | `#/sessions` DOM 读 mode-tab | ✅ 活跃20/归档0，aria-selected=active |
| V-12 | 归档过滤 | localStorage 注入归档态（真实 loadArchivedIds 读取路径）→ 重进列表 | ✅ 活跃19/归档1 |
| V-13 | 归档区徽标 + 空态 | 切归档 tab | ✅ "已归档"徽标 1 张卡片；清空后空态提示 |
| V-14 | 左滑恢复 | 合成 TouchEvent 手势（SwipeableListItem touchstart/move/end，位移 240px）→ 揭示动作 → 点击「恢复」 | ✅ 活跃20/归档0 |
| V-15 | chrome-sub 五视图工具栏恢复 | `#app-chrome-sub` 目标恢复后 /sessions 渲染 | ✅ 搜索框+分区切换（chrome-sub h=111px） |

截图：`shots/vivo-p15-07-detail-top-fixed.png`（距顶 39px）/ `vivo-p15-08-list-active.png` / `vivo-p15-09-list-archived.png`（归档区） / `vivo-p15-10-archived-restored.png`（恢复后空态）。

### 7.3 连带修复存量 bug

**ScrollChromePortal 目标丢失**（8ddbc43 引入 `#app-chrome-sub`，后续 AppLayout 重构丢失）：sessions/email/instances/meetings/vault 五个视图搜索工具栏静默不渲染。AppLayout 恢复目标元素后真机确认渲染恢复（V-15）。

### 7.4 复现要点追加

- 归档存储 key：`pocket_session_archive_v1:<encodeURIComponent(workspaceId)>:<encodeURIComponent(instanceId|all)>`（workspace 取 `pocket_workspace_id`）
- 合成滑动手势需构造 Touch/TouchEvent 三段序列（start→move×8→end），位移 ≥ 动作宽 × 0.3 阈值
- 重装后首次进入需重新输主密码解锁（本次为 `p1-verify-2026`）
