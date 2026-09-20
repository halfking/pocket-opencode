# 全任务后台化 + 完整消息通知体系(2026-09-20)

> 目标(用户原话):「将项目中所有的执行任务都转成后台执行,这样在界面切换时,不致于导致任务中断。并且需要建立完整的消息通知,能够对 UI 进行更新及进行系统通知。」
>
> 本文 = 源码审计结论 + 分期落地方案 + 落地状态回填。实施节奏:P0(任务不中断)→ P1(通知体系)→ P2(连接可靠性)。

---

## 1. 现状审计(代码级证据)

### 1.1 已经安全的后台任务(M1-M5 成果,不重复造轮子)

2026-09-09「AI 异步流式输出后台生存」改造后,以下任务已与组件生命周期解耦,**路由切换不影响**:

| 任务 | 承载 | 证据 |
|------|------|------|
| AI 对话流 | `native/aiStreamRuntime.ts` 进程单例(契约明写"组件 unmount 不是取消") | `features/ai-chat/aiChatStore.ts` |
| 会话 SSE | `stores/session.ts` 持有 `SessionSSEClient`,`detach()` 为 no-op | `api/sse.ts` |
| 本地智能体 | `features/local-agent/runtime.ts` 单例 | `agentStore.ts` 镜像 |
| 审批轮询 | `native/approvalsRuntime.ts` 进程单例(main.ts 启动) | `usePendingApprovals` 只订阅 |
| Gateway 实时流 | 模块级 `liveClients` Map(M4) | `GatewayLiveStreamView.vue` |
| OpenCode 实时 WS | `stores/opencode.ts` 模块级 `realtimeWs` 单例 | 见缺口 G4 |
| 离线同步/邮件抓取 | `stores/connectivity.ts` → `MobileSyncRuntime`/`email-fetch-host` | main.ts 启动 |
| KeepAlive 列表页 | `App.vue` `LIST_CACHE_NAMES` 缓存 14 个列表页,切走只 deactivated | `use-list-scene.ts` |

### 1.2 缺口:界面切换会中断/丢失的任务(P0)

| # | 任务 | 证据 | 界面切换后果 |
|---|------|------|--------------|
| G1 | **会议录音 + VAD 分段转写** | `composables/useMeetingRecorder.ts:295-299` `onBeforeUnmount → cleanupMedia()` 停轨道/VAD/原生 BackgroundMic;使用方 `MeetingDetailView.vue`(不在 KeepAlive)、`useSessionLiveRecord.ts` | 录音流永久丢失,meeting 卡在 `recording`。讽刺点:Android BackgroundMic 前台服务本就为后台录音设计,却被 unmount 无条件 stop |
| G2 | **笔记录音 + 实时转写** | `features/notes/useNoteRecording.ts:173-178` `onBeforeUnmount → cleanupMedia()`;转写文本只存组件 ref | 录音停止、在途转写不落草稿 |
| G3 | **UnifiedComposer「AI 优化草稿」被 unmount abort** | `components/business/UnifiedComposer.vue:458-463` `onBeforeUnmount(() => abortOptimize())`;与 `usePromptOptimizer.ts:9-10` 头注释「组件 unmount 不再 abort」的 M1 契约**直接矛盾** | 优化流被杀 |
| G4 | **opencode 实时 WS 被视图反向关闭** | `features/opencode/SessionListView.vue:147-150` `onUnmounted(() => ws.close())`,close 的是 `stores/opencode.ts` 模块级共享单例 | 5s 内实时事件丢失(自动重连自愈) |
| G5 | **语音输入转写结果丢失** | `composables/useVoiceInput.ts:110-113`(仅 UnifiedComposer 使用):`stopRecording` 已发出的 `/api/stt/transcribe` 在 unmount 后完成,结果写入已卸载组件 | 用户口述文本作废 |
| G6 | **邮件翻译结果作废** | `features/email/EmailDetailView.vue` `chooseLang` 结果只写组件本地 `langCache` | 离页重进需重新翻译(花 token) |
| G7 | CostQuota 查询 abort | `features/cost/CostQuotaView.vue:243-246` | 只读查询,重进即重查 —— **明确不改**(产品上离开就该取消) |

### 1.3 缺口:通知体系(P1)

前端:
- **N1 通知中心 store 从未接线**:`stores/notification.ts` 的 `loadInbox/subscribeWs` 全仓库无调用方;`/api/notifications` API 客户端齐全但无人用;WS `notification` 事件前端无人处理 → 通知中心 UI 不存在。
- **N2 任务完成无系统通知**:唯一本地通知是审批 3 分钟告警(`useApprovalAlerts`,只挂在 TasksView,离页即停)+ 闪卡每日提醒。`round.completed`/`scheduledtask.*` 均不产生任何用户可感知通知。
- **N3 通知入口/未读徽标不存在**。

后端:
- **N4 推送不定向**:`notifycenter.WebsocketSender` 用全局 `Broadcast`,而 `Notification` 明明带 `user_id`,hub 已有 `BroadcastToUser` —— 多用户串台。
- **N5 无规则即丢弃**:`Service.Dispatch` 中 `matchRule == nil → drop`;现网没人建过规则 → 所有事件被丢,inbox 永远为空。
- **N6 通知源稀少**:只有重要邮件 + 闪卡到期 2 处写通知;定时任务失败不上通知。

连接可靠性(P2):
- **N7 WS 固定 3s 重连无退避**(`api/websocket.ts:7`);重连成功后无 resync 触发(通知/审批只能靠轮询兜底追赶)。
- 死代码:`services/websocket-hub.ts`(demo 专用,从未真正连接)、`backend/internal/websocket/mobile_hub.go`(未接线)—— 本次不动,仅记录。

---

## 2. 方案设计

### 2.0 总原则

1. **沿用 M1 模式**:任务所有权上移到进程级 singleton(globalThis 挂载防 HMR 重复),组件只「订阅 UI + 发起动作」,组件卸载 ≠ 任务取消。取消只属于用户显式操作(stop 按钮/确认弹窗)。
2. **通知两级分发**:后端 notifycenter 负责**持久化 inbox + 前台 WS 定向推送**(跨设备、离线可补拉);前端 notificationDispatcher 负责**即时感知**(前台 toast / 后台系统通知 + deepLink),因为只有前端知道「用户当前在哪个页面、app 是否后台」。
3. **不改的明确划线**:G7(CostQuota abort)、TTS 朗读随页停(产品合理)、`websocket-hub.ts`/`mobile_hub.go` 死代码(单独清理)、FCM/APNs 长链路推送(部署期任务,保持 Noop)。

### 2.1 P0:任务不中断

**G1/G2 录音后台化 —— `native/recordingRuntime.ts` 进程单例**

- 把 `useMeetingRecorder`/`useNoteRecording` 的全部状态(ref)与方法(VAD/声纹/BackgroundMic/STT/计时)整体搬进 `MeetingRecorderRuntime` / `NoteRecorderRuntime` 两个类,`globalThis` 挂载单例。
- composable 变薄壳:attach 到单例,返回同一签名,**删除 onBeforeUnmount 的媒体清理**(只留纯 UI 清理如 header title)。
- 互斥:mic 是独占资源,`meeting.start()` 时若 note 在录则拒绝(反之亦然),读对方单例状态。
- **全局录音指示条 `RecordingPill.vue`**:AppLayout 挂载;当「录音中 && 当前路由不在录音宿主页」时显示(红点 + 时长 + 点击回宿主页 + 停止按钮)。这是录音跨页后用户找回录音状态的入口。
- Android Web 模式差异保持:onHidden 提示仅对 Web getUserMedia 生效;原生 BackgroundMic FGS 本就跨页/跨 app 存活。
- 会议录音跨页后,`MeetingDetailView` 重新进入时从单例恢复 UI(状态本就在单例里,自然恢复);`useSessionLiveRecord` 的 `meetingId` 改由 runtime 会话状态派生(重进页面 attach 到进行中的录音)。

**G3**:删除 `UnifiedComposer.vue` onBeforeUnmount 中的 `abortOptimize()`(对齐 M1 契约;流在 aiStreamRuntime 中自然跑完,防重复 spawn 由 runtime 幂等保证)。

**G4**:删除 `SessionListView.vue` onUnmounted 的 `ws.close()`(共享单例连接的生死由 store 管理,重连逻辑已在 store 内)。

**G5 语音转写结果生存**:`useVoiceInput` 增加模块级「在途转写登记」——unmount 不取消已发出的 HTTP;转写完成后若组件已卸载,把文本**写入剪贴板并 toast 告知**(「语音转写已完成并复制到剪贴板」),文本不丢。

**G6 邮件翻译缓存持久化**:翻译结果写入既有 email 正文缓存层(键:`(emailId, lang)`),重进页面直接命中。

### 2.2 P1:通知体系

**后端(3 处小改)**

1. **定向广播**:`notifycenter.Broadcaster` 接口加 `BroadcastToUser(userID, msgType, payload)`(`*ws.Hub` 已实现,零适配);`WebsocketSender.Send`:`UserID != "" → BroadcastToUser`,否则维持 `Broadcast`。
2. **默认规则兜底**:`Dispatch` 在 `matchRule == nil` 时不再丢弃,回落到**内置默认规则**(channels `[inbox, websocket]`,priority 取事件值或 `normal`)。原语义「无规则=丢弃」导致通知体系形同虚设;显式建的规则仍优先(含免打扰)。
3. **定时任务失败通知源**:`Scheduler` 增加 `SetNotifier(NotificationClient)` 晚绑(main.go 在 notifycenter 就绪后注入,复用 flashcardExec 模式);terminal run 为 `failed` 时 Dispatch 一条 `{source: scheduledtask, kind: task.failed, priority: high}` 通知(成功/跳过不打扰,防刷屏)。WS 的 `scheduledtask.succeeded/failed` 事件照旧,前端另有即时分发。

**前端 —— `services/notificationDispatcher.ts` 进程级分发器(main.ts 启动)**

- 订阅(idempotentWsBus):`notification`(后端 inbox 推送)、`scheduledtask.succeeded|failed|skipped`、`round.completed`、`approval.permission.pending`。
- **分发决策(纯函数,可测)**:`shouldSurface(route, event) → none|toast|system`:
  - 用户正停在事件宿主页(如 scheduledtask.* 在定时任务页、round.completed 在对应会话页)→ `none`(页面自身 UI 已在更新,不打扰);
  - 前台其它页面 → `toast`(即时 UI 更新,inbox 同时入账);
  - 后台(`appLifecycleHub.isHidden()`)→ `system`(LocalNotifications 立即通知,带 `extra.deepLink`,点击回跳对应页)。
- `notification` 事件统一 `store.pushLocal` 入 inbox(未读+1,徽标即亮)。
- 系统通知点击:分发器持有一个全局 `localNotificationActionPerformed` 监听,按自家 deepLink 字符串格式过滤(与 useApprovalAlerts 的对象格式 deepLink、闪卡的 `/flashcards/review` 互不干扰)。
- 原生平台启动时 best-effort 申请 POST_NOTIFICATIONS。

**通知 UI**

- `features/notifications/NotificationsView.vue`:inbox 列表(标题/正文/时间/优先级/未读点)+ 全部已读;点击进 `payload.deepLink`(若有)并标已读。
- 路由 `/notifications`;AppLayout 顶栏常驻**铃铛入口 + 未读徽标**(读 notification store 的 `unreadCount`)。
- main.ts 接线:登录态下 `loadInbox()`(增量 since)+ 分发器启动;登出清空 store。

### 2.3 P2:连接可靠性

- `api/websocket.ts`:指数退避重连(3s 起,×1.8,上限 30s,±20% 抖动,连接成功归零);新增 `onReconnected(cb)` 注册口。
- 重连成功 → resync:`notificationStore.loadInbox()`(增量);审批追赶已有轮询兜底,不重复。

---

## 3. 落地状态(2026-09-20 全部落地)

| 项 | 内容 | 状态 | 关键落点 |
|----|------|------|----------|
| P0-G1/G2 | 录音 runtime 单例 + 全局录音指示条 | ✅ | `native/recordingRuntime.ts`(Meeting/NoteRecorderRuntime,globalThis 单例);`native/recordingPolicy.ts`(启动/互斥纯决策,10 用例);`composables/useMeetingRecorder.ts`、`features/notes/useNoteRecording.ts` 改薄壳(按 meetingId 圈定只读视图);`components/RecordingPill.vue`(AppLayout 挂载,切离宿主页可见/回跳/停止);NoteListView 消费 `pendingResult` 补建语音草稿 |
| P0-G3 | UnifiedComposer 去 unmount abort | ✅ | `UnifiedComposer.vue` onBeforeUnmount 不再调 `abortOptimize()`(对齐 M1 契约) |
| P0-G4 | SessionListView 不再 close 共享 WS | ✅ | 删除 `onUnmounted(() => ws.close())`,连接生死归 store |
| P0-G5 | 语音转写结果生存 | ✅ | `useVoiceInput.ts`:在途 HTTP 不因 unmount 取消;孤儿结果落剪贴板 + toast;unmount 时仍在录则收尾转写已捕获音频 |
| P0-G6 | 邮件翻译缓存持久化 | ✅ | `EmailDetailView.vue`:翻译结果按 `email_translations:<id>` 落 localStorage,重进直接命中 |
| P1-后端 | 定向广播 + 默认规则兜底 + 定时任务失败通知 | ✅ | `notifycenter/service.go`(Broadcaster 加 BroadcastToUser,按 UserID 定向;无规则回落内置默认 inbox+websocket);`scheduledtask/scheduler.go`(Notifier 晚绑,failed→`scheduledtask/task.failed` 高优通知,成功不打扰);`cmd/pocketd/main.go` 接线 schedRef.SetNotifier;测试 8 个用例锁定行为 |
| P1-前端 | notificationDispatcher + main.ts 接线 | ✅ | `services/notificationDispatchPolicy.ts`(describeEvent/decideSurfacing 纯决策,15 用例);`services/notificationDispatcher.ts`(订阅 7 类事件→inbox 入账/toast/系统通知+deepLink 回跳;原生权限申请);main.ts `startNotificationDispatcher(pinia, router)` |
| P1-UI | 铃铛入口 + 未读徽标 + NotificationsView | ✅ | `features/notifications/NotificationsView.vue`(列表/已读/全部已读/source 跳转);路由 `/notifications`;AppLayout 顶栏常驻铃铛 + 未读徽标 |
| P2 | WS 退避重连 + resync | ✅ | `api/reconnectPolicy.ts`(3s×1.8 上限 30s ±20% 抖动,抖动后绝对钳制,5 用例);`api/websocket.ts` 退避 + `onConnected` 回调;分发器在每次连接成功时增量补拉 inbox |

**验证记录(2026-09-20)**:
- 前端 `vue-tsc --noEmit` 通过;node --test 全量 265 例,除 `config/api-base.test.ts` 1 个存量失败(未改动的 HEAD 上同样失败,已用 `git stash` 对照验证)外全绿;新增 3 个测试文件 30 例全绿。
- 后端 `go build ./...` 通过;`go test ./internal/notifycenter ./internal/scheduledtask ./cmd/pocketd` 全绿;`internal/server`、`internal/agent`、`internal/email` 的失败为存量问题(Windows 下无法 fork/exec `.sh` 假代理 + meeting 隔离用例),stash 对照确认与本次无关。
- Android 真机手动验收项(录音跨页、定时任务失败系统通知、通知中心徽标)待装机验证 —— 见 §4。

## 4. 验证

- 前端:`npm run typecheck`(vue-tsc)+ `node --test`(新增 notificationDispatcher 决策纯函数、recordingRuntime 互斥、WS 退避计算等单测)+ 既有套件回归。
- 后端:`go build ./...` + `go test ./...`(notifycenter/scheduler 包新增用例)。
- 真机(Android)手动验收项:会议录音中切页/切后台 → RecordingPill 可见、录音不断;定时任务失败 → 系统通知出现、点击回跳;通知中心未读徽标与列表联动。
