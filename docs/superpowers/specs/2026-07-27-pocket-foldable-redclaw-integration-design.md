# Pocket Foldable / RedClaw 整合设计 + 审计（2026-07-27）

> 范围：把折叠屏铺满适配 + 移除冗余标题栏、笔记去过度安全并支持 ENEX 导入、会议录音权限一键式引导、邮件账户/同步/分类/自动回复、AI 入口接入主机级 ACP 发现并经 RedClaw 调度——整合到 OpenCode Pocket。
>
> 本文是目标架构与当前仓库审计，不把工作树中的未提交改动视为已交付功能。特别是邮件 OAuth、正文懒加载、SMTP 自动回复和 RedClaw 邮件控制面，均须按本文的验收条件完成后才能标记为 ready。

---

## 0. 摘要（TL;DR）

| # | 需求 | 现状痛点（一句话） | 设计总策略 |
|---|------|--------------------|-----------|
| 1 | 折叠屏铺满、单一标题栏 | AppLayout 无条件渲染 top-bar，多个 view 自带 `<h1>`；CSS 断点只有 `768/1280`，没有 `≥840dp` 折叠展开态；无 master-detail 容器 | AppLayout 改为按路由 meta 显隐 top-bar；新增 `≥840dp` 折叠展开态 + `SplitLayout` 容器；新建 `useBreakpoint()` JS hook 与 CSS 同步；列出 6 处冗余 `<h1>` 全部移除 |
| 2 | 笔记去过度安全 + Evernote 导入 | SQLCipher 全库加密 + AES-GCM 字段加密 + 登录即初始化 → 刷新即锁；ENEX 完全零支持 | 保留 SQLCipher 全库加密（防设备丢失），**移除字段级 AES-GCM**；新增首启"创建主密码"对话框（与登录密码解耦）；ENEX 解析放在前端（保 E2EE 边界），走 `fast-xml-parser` → `assetStore.upsert({source:'enex_import'})` |
| 3 | 会议麦克风权限引导 | 启动时申请 + WebView `grant(resources)`，但**无 UI 跳系统设置、无预检、无重试 CTA** | 新增 `useOpenAppSettings()`（Capacitor App plugin）+ `MeetingRecordView` 顶部加麦克风预检条（绿/红/灰徽章 + "去系统设置"按钮 + "重新检测"按钮） |
| 4 | 邮件账户/同步/分类/自动回复 | OAuth 组件存在但 start→callback→账户绑定链路断裂；规则动作只部分支持；Vacation 目前只有配置 CRUD；无正文懒加载和仅 Wi-Fi 策略 | pocketd 持有凭证和邮件数据，完成 OAuth/IMAP/SMTP 数据面；新增本地规则与 Vacation 执行器；**RedClaw 只做可审计的策略建议/编排控制面，不接触凭证、默认不接收完整正文，也不直接发信**；新增完整正文懒加载和仅 Wi-Fi 开关 |
| 5 | AI 入口接入主机 ACP + RedClaw 调度 | `/ai` 只是 TasksView 复用，零 RedClaw/ACP 接入；RedClaw 与 ACP 完全解耦 | **新增 `internal/redclaw/discovery.go`**：复用 `registry.NetworkDiscovery` 模式扫 ACP stdio/HTTP/WS 三类端点；**新增 `/api/redclaw/discover` + `/api/redclaw/agents`**；前端 `/ai` 改为新 `AiHubView`（运行中任务/可接管 ACP/可用 RedClaw 模型三栏）；**保留 `/api/redclaw/{health,chat,knowledge}`**，把 ACP `Bridge.Send` 改成可选走 RedClaw 调度 |

---

## 1. 折叠屏铺满 + 单一标题栏（需求 #1）

### 1.1 问题定位

来源文件：

- `frontend/src/app/AppLayout.vue:10-13,32` — AppLayout 无条件渲染 `<header class="top-bar">` + 标题，标题源自 `route.meta.title`。
- `frontend/src/app/AppLayout.vue:93-109` — 唯一断点是 `≥768px` 两列、`≥1280px` 三列，**无 `≥840dp` 折叠展开态**、无 master-detail 容器。
- 6 处 view 自带 `<h1>`，与 top-bar 形成"两个标题"：MeetingListView:3-6、MeetingDetailView:6-8、MeetingRecordView:4、NoteDetailView:21-22、ContactDetailView:7-9、VaultEntryView:77-79、LoginView:7。
- 多个 view 自带 `<h2>` 形成"页面标题 + top-bar"视觉重复：EmailSummaryView:12/50、EmailAccountSetup:11、EmailDetailView:15/28、PkmTodayView:17、ComingSoonView:9、VaultListView:12/17。
- 全局 CSS 断点与 AppLayout 不一致：`breakpoints.css:3-6` 用 `640/1024`，AppLayout 用 `768/1280`，缺少统一约定。
- 没有 `useBreakpoints` 调用：自研 `composables/useBreakpoint.ts:24-48` 写了但**零调用**。
- `vault-list`、`pkm-note-view`、`task-list` 等缺少统一大屏 class。
- 路由表（`router-mobile.ts:62-283`）共 27 条，其中 4 条（`/`、`/login`、`/servers`、无 meta.title）会回退 `OpenCode Pocket` 默认标题。

### 1.2 设计：单一标题栏 + 折叠屏双面板

#### A. AppLayout 改动（最小侵入）

- `AppLayout.vue` 顶部新增判定：`v-if="route.meta.showTopBar !== false"`，由路由 meta 决定。
- 在 `router-mobile.ts` 给所有路由**显式标注** `meta.showTopBar`：
  - 列表/详情/编辑类（11 条路由）→ `showTopBar: true`。
  - **首页 `/ai`**、**Tabs 切换的 inbox**（如 EmailSummaryView 列表页本身已有标题）→ `showTopBar: false`。
- 同时去除 view 自带的 `<h1>`（6 处）+ `<h2>` 顶部标题（6 处）→ 把视觉权重交给 AppLayout 的 top-bar 或页面主内容区。

#### B. 折叠屏断点 + SplitLayout 容器

- 新建 `frontend/src/composables/useBreakpoint.ts`（已存在，**扩展**为支持 `foldable-expanded ≥840dp`、`foldable-folded <560dp`）：
  ```ts
  type Breakpoint = 'compact' | 'medium' | 'expanded' | 'wide'
  // compact : <560
  // medium  : 560–839  （折叠屏合盖/小手机）
  // expanded: 840–1279 （折叠屏展开/小平板）
  // wide    : ≥1280    （桌面/大平板）
  ```
- CSS 同步：把 `breakpoints.css` 的 `640/1024` 与 `AppLayout` 的 `768/1280` **合并为 560/840/1280** 三档；提供 `.bp-expanded-only / .bp-compact-only` 工具类。
- 新建 `frontend/src/components/SplitLayout.vue`：
  ```vue
  <SplitLayout :master="$slots.master" :detail="$slots.detail" />
  ```
  - `≥840dp`：左右两栏（master 38% / detail 62%）。
  - `<840dp`：单栏，detail 切换时整体替换 master。
  - 配合 `<KeepAlive>` 缓存 master，减少重渲染。
- 应用 SplitLayout 的页面：笔记（list/detail）、会议（list/detail）、邮件（inbox/detail/summary）、联系人（list/detail）、密码箱（list/entry）、任务（list/detail）、AI 看板（运行中/详情）。

#### C. UI/UX 影响

- 单标题栏：折叠屏合盖时与现状一致；展开时左侧 master 列表占 38% 宽度，**列表与详情同屏可见**，右侧仍显示当前 top-bar 标题（在 master 上方 + detail 顶部不再有页面 h1）。
- 横屏/小屏（≥560 & <840）维持单栏 + 顶部导航，**避免出现挤压布局**。
- 安全区：`env(safe-area-inset-*)` 已存在（`AppLayout.vue:50-52`），SplitLayout 内层 padding 直接复用。

#### D. 兼容性

- 6 处 `<h1>` 移除统一用 PR 处理：每个 view 顶部保留页面专属描述/面包屑即可。
- 不引入新依赖；`useBreakpoint` 用原生 `window.matchMedia`。
- **不重构** `BottomNav`（保留 4 tab + 更多菜单）。

### 1.3 验收

- 在 Pixel Fold / Galaxy Z Fold 模拟器展开态：list + detail 双栏可见，**没有第二个 h1**。
- 合盖态/普通手机：单栏 + top-bar 单标题。
- 横屏 ≥560 且 <840：单栏。
- 桌面 ≥1280：master + detail 双栏，max-width 限宽。
- Playwright/iOS Simulator/Android Emulator 都过。

---

## 2. 笔记去过度安全 + ENEX 导入（需求 #2）

### 2.1 问题定位

来源文件：

- `frontend/src/native/local-db.ts:13-92,49-67` — SQLCipher 全库加密；空密码走 no-encryption，但默认强制 secret。
- `frontend/src/features/notes/notes-store.ts:74-87,109-114,274-302,326-331` — content 字段**再叠一层** AES-GCM；解密失败用 `"[加密内容无法解密]"` 占位。
- `frontend/src/features/auth/LoginView.vue:91-119,151-156` — `needUnlock` 路径依赖"登录密码 = 主密码"。
- `frontend/src/native/crypto.ts:53-78,94-97` — PBKDF2(masterPassword + salt, 100k) 派生 AES key；登录态丢失即 `resetCryptoKey()`，下次进入 `/notes` 即 `dbNotReady=true`。
- `frontend/src/native/lobster-init.ts:6-7,43-69` — 主密码 = localDB key + crypto key 派生源。
- `frontend/src/features/notes/NoteListView.vue:8-17,73-75,83-87` — `dbNotReady` 兜底面板跳 `/login`，**没有原地输入密码解锁**。
- `frontend/src/features/pkm/pkm-store.ts:109` — PKM 已经走明文 `encryptBody:false` 路线，**对比形成"两条路"**。
- 整个工程 `grep evernote|enex|.enex`：**零命中**。
- `frontend/package.json` 无 XML parser；`backend/go.mod` 无 ENEX 解析依赖，但 `encoding/xml` 标准库可用。
- `frontend/src/native/asset-store.ts:110-151,238-249` 与 `frontend/src/native/schema.ts:268-350` 已支持 `local_assets`（kind/workspace/source/sync_mode）+ `local_asset_blobs`，是天然落点。

### 2.2 设计：保留全库加密、移除字段加密、ENEX 前端解析

#### A. 笔记去过度安全（三档可选）

按风险递增顺序，给用户/运维三档：

| 档 | SQLCipher 全库 | 字段 AES-GCM | 主密码 | UX |
|---|---|---|---|---|
| **L0 默认（推荐）** | ✅ 保留 | ❌ 移除 | "首启创建主密码"对话框（与登录密码解耦） | 日常无摩擦，仅在首启/重装时一次性输入 |
| **L1 高级** | ✅ 保留 | ✅ 保留 | 同 L0，但提供"30 天免输入"选项（PBKDF2 key 派生 + 内存留存） | 给 vault/notes 高敏感用户提供 |
| **L2 极简** | ❌ 关掉 | ❌ 关掉 | 无 | 仅用于 dev 或调试态；`local-db.ts:49` 已支持空密码 |

实现：

1. `local_notes` schema 加 `encrypted_content INTEGER DEFAULT 0`；store 层读 `crypto.cfg.encryptField` 决定是否 `encryptString`（`notes-store.ts:74-87`）。
2. 引入 `crypto.cfg` 单例：在 `crypto.ts` 暴露 `getCryptoConfig() / setCryptoConfig()`，持久化到 `localStorage['pocket_crypto_cfg']`，默认 `{ mode: 'field-disabled' }`。
3. 把 `LoginView.vue:151-156` 的 `initLobster(password.value)` 改为：登录后**只设 JWT**；首启/迁移时弹独立"创建/输入主密码"对话框（参考 `vault/setupMasterPassword`）。
4. 在 `NoteListView.vue:8-17` 增加原地解锁面板：状态卡显示"本地数据未解锁" + 密码输入框 + "解锁" + "忘记密码（重置）"按钮。**不再强制跳登录页**。
5. **保留 PKM** 作为明文 FTS 的快捷路径（与 `/notes` 并列），不替换。

#### B. ENEX 导入（前端解析）

流程：

```
[ENEX 文件] (.enex, 可能含附件 base64)
       │
       ▼  (FileReader → ArrayBuffer)
[Zip 解析: JSZip]
       │
       ▼  (fast-xml-parser → DOM)
[note 列表 + resource 列表]
       │
       ▼  (按 note 遍历)
       │
  ┌────┴─────────────────────────────────────────────┐
  │ each note:                                       │
  │   assetStore.upsert({                            │
  │     kind: 'note',                                │
  │     source: 'enex_import',                       │
  │     workspaceId: auth.workspaceId,               │
  │     title,                                       │
  │     bodyText: enexBody → Markdown,               │
  │     metaJson: {tags, originalCreated, enexId,    │
  │                 author, sourceApp, sourceUrl,    │
  │                 latitude, longitude, attributes}  │
  │   })                                             │
  │   for each <resource>:                           │
  │     assetStore.addBlob({                         │
  │       assetId, kind: 'attachment', mime,         │
  │       data: base64 → Uint8Array                  │
  │     })                                           │
  └────┬─────────────────────────────────────────────┘
       │
       ▼
[导入进度 toast: "导入 X / Y"] + 失败回滚策略
```

实现要点：

- 加依赖 `frontend/package.json`：
  - `fast-xml-parser@^4` (~30KB gzipped, 已用 marked 的项目多半会接受)
  - `jszip@^3` (~30KB gzipped，ENEX 本身就是 XML，但很多工具支持 ZIP 容器)
- 新建 `frontend/src/features/imports/EvernoteImporter.vue` + `evernote-parser.ts`。
- 新建 `frontend/src/api/imports.ts`（仅本地资源读取，不上云端，**保留 E2EE**）。
- 入口：`NoteListView.vue` 顶栏加 "导入" 按钮 → 弹文件选择器（`accept=".enex,application/xml,text/xml"`）→ 解析 → 进度提示。
- 失败策略：单条 note 失败 → 跳过并记日志；全部失败 → 弹错误面板。
- 重复检测：基于 `metaJson.enexId` + `metaJson.originalCreated` 做去重，已存在则跳过。

#### C. 与后端关系

- 后端 `internal/notes/`（`note.go:18, store.go:88-159`）只是 kxmemory 元数据缓存，不导入 ENEX。
- 后端**不新增 ENEX 接口**，所有导入都在前端。

### 2.3 验收

- 旧用户升级：首次进 `/notes` 看到"创建主密码"对话框（迁移向导），输入后无需重登。
- 刷新页面：仍在 `/notes`，输入主密码即解锁，**不再跳登录**。
- 导入 `.enex`：100 条笔记约 3–8 秒完成，进度可见，导入后立即出现在 list。
- 字段 AES-GCM 配置默认关闭；可选开启（高级模式）。
- 测试：迁移路径（升级用户/新用户/导入/导出）。

---

## 3. 会议麦克风权限一键式引导（需求 #3）

### 3.1 问题定位

来源文件：

- `frontend/android/app/src/main/AndroidManifest.xml:43` — `RECORD_AUDIO` 声明。
- `frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/MainActivity.java:31-42,51-58` — `WebChromeClient.onPermissionRequest.grant(resources)` + `ensureRecordAudioPermission()`。
- `frontend/src/composables/useMicPermission.ts:14-69` — `state: unknown|granted|denied|unavailable` + `deniedLabel`，**没有任何 Capacitor Bridge 跳系统设置**。
- `frontend/src/features/meetings/MeetingRecordView.vue:60-85,9-12,103-105,117-119` — error-card 只显示文本，**无 CTA 按钮、无预检**。
- `frontend/src/composables/useVoiceRecording.ts:27-65` — `useMicPermission().ensure()` 已有，但 UI 不订阅 `mic.state`。

### 3.2 设计：预检条 + 错误行动按钮 + 跳系统设置

#### A. `useOpenAppSettings()` 新 composable

新建 `frontend/src/composables/useAppSettings.ts`：

```ts
// 用 Capacitor App plugin（@capacitor/app）
// 在 Android 上：调 App.openUrl({ url: 'package:com.kaixuan.opencode.pocket' }) 或
// ACTION_APPLICATION_DETAILS_SETTINGS 跳应用详情页
// iOS：弹"去设置"提示（iOS 不允许从应用跳设置，需要 UI 引导用户手动去）
// Web fallback：window.open('about:preferences')
```

#### B. `MeetingRecordView.vue` 顶栏新增"麦克风预检条"

位置：紧接 `<header>` 下方（在 title-input 之前），独立卡片：

```vue
<MicStatusBar />  <!-- 三态：unknown/granted/denied/unavailable -->
```

`MicStatusBar.vue`：

- granted：显示绿色圆点 + "麦克风就绪"（可隐藏）。
- denied：红色圆点 + "麦克风权限被拒绝" + "去系统设置" 按钮 + "重新检测" 按钮。
- unavailable：橙色圆点 + "未找到可用麦克风设备" + "重新检测" 按钮。
- unknown：灰色圆点 + "点击授权麦克风" 按钮（触发 `mic.ensure()`）。

#### C. error-card 升级

把现有 `MeetingRecordView.vue:15` 的 `<div class="error-card">` 升级为 `<ErrorActionCard :code="errCode" @retry="retryAction" />`：

- 错误码到 CTA 映射：
  - `mic-denied` → "去系统设置" + "重新检测"
  - `mic-busy` → "重新检测" + "切换音频源"
  - `mic-none` → "连接麦克风后重试"
  - `stt-failed` → "重试转写" + "切换本地引擎"
  - `rec-empty` → "重新录制"
- 所有按钮触发具体函数（不是只 toast）。

#### D. 录音按钮行为升级

- 大圆形 `record-button` 增加 `disabled` 条件：`mic.state === 'denied' || mic.state === 'unavailable'` 时灰显 + tooltip "请先解决麦克风权限"。
- 点击按钮若 `mic.state === 'unknown'`：先 `mic.ensure()` 再开始录音（不阻塞 UI）。

### 3.3 验收

- 全新用户：进 `/meetings/new` 看到"麦克风状态：未授权 → 点击授权"。
- 拒绝授权：状态变红 + "去系统设置" 按钮可点，跳系统设置页（或弹 iOS 引导）。
- OEM 拦截（Vivo/ColorOS）：状态卡明确说明 + 引导到 `设置 → 应用 → 权限`。
- 设备拔出麦克风：录音自动停止，error-card 提示原因 + 重试 CTA。

---

## 4. 邮件账户/同步/分类/自动回复（需求 #4）

### 4.1 问题定位

来源文件与当前状态：

- `frontend/src/features/email/EmailAccountSetup.vue:135-140,259-289` — 仍以手动 IMAP/密码为主；编辑账户不更新 credential，云端失败时还会回退本地保存，可能造成“已配置”的假成功。
- `frontend/src/api/email.ts:64-108` — 已有账户、SMTP 探针和 Vacation 方法，但尚未暴露 provider 列表、OAuth start/complete、正文 body 或仅 Wi-Fi 策略接口。
- 后端 OAuth 组件存在但链路未闭合：`backend/internal/email/oauth.go:38-58` 未注入有效 `client_id`；`backend/internal/server/server_assistant.go:407-409` 以空 `accountID` 创建 pending entry；callback 未可靠处理加密错误，也没有完成“新建/绑定账户”的完整路径。
- SMTP 数据面未闭合：账户配置没有独立 SMTP username/password API；路由注册与带 account id 的 `test-smtp` handler 不一致；探针的 STARTTLS、凭证拆分语义需要统一。
- `backend/internal/email/fetcher.go:127-181` 只保存小段 snippet；`body_path` 没有实际写入，也没有 `GET /api/emails/{id}/body`；Vacation 目前是配置 CRUD，不能证明已经投递。
- `backend/internal/email/rules/engine.go` 目前只可靠处理 `mark-important`；archive、route-folder、autoreply 等动作仍未执行。客户端推送邮件与 Vacation 写入还需要先验证 account 属于当前 user/workspace。
- 邮件同步、摘要和事件广播存在未 scoped 的调用路径：写入、sync status、按日摘要以及 `BroadcastToUser` 都必须同时带 user 与 workspace。

### 4.2 设计边界：pocketd 数据面，RedClaw 控制面

邮件功能拆成两个明确的平面，避免把远端 AI 网关变成凭证或邮件数据的隐式持有者。

| 平面 | 负责 | 明确不负责 |
|------|------|------------|
| pocketd 数据面 | workspace-scoped 账户、OAuth token/密码密文、IMAP/SMTP 连接、增量同步、正文/附件缓存、规则副作用、Vacation 实际投递、重试与回滚 | 不把凭证交给 RedClaw；不依赖 RedClaw 在线才能读已有邮件 |
| RedClaw 邮件控制面 | 对 envelope、脱敏 snippet 或结构化特征做分类/规则建议；按 workspace 路由策略；返回带版本和幂等键的 action plan；提供可审计的执行状态 | 不保存 OAuth refresh token、密码或完整正文；不直接连 IMAP/SMTP；不直接发送邮件；不绕过 pocketd 的账户归属检查 |

默认数据流：

```text
IMAP fetch → pocketd 保存 envelope/snippet
                    │
                    ├─ 本地确定性规则（可离线、可回放）
                    │
                    └─ 可选 RedClaw 控制面
                       只发送 workspace + account/message 引用 + 脱敏特征
                                  │
                         action plan / no-op + plan_version + idempotency_key
                                  │
                    pocketd 再次校验归属、能力和账户开关后执行
                                  │
                    执行回执/审计事件（不含凭证、正文、附件）
```

RedClaw 不在线时，本地规则和已有账户/邮件功能必须继续工作；控制面建议不可作为同步、阅读或发信的单点依赖。需要完整正文的 AI 操作必须由用户显式触发，并通过一次性、最小权限的 pocketd 代理接口完成；默认不把正文上传到 RedClaw。

### 4.3 设计：OAuth-first + 规则引擎 + 自动回复 + 懒加载正文

#### A. `EmailAccountSetup.vue` 改造

- 把硬编码 4 个模板改为 `emailApi.listProviders()` 拉后端 provider 元数据；provider 响应只返回公开配置和能力，不返回 client secret。
- 表单三选一：
  - “用 {Provider} 登录”按钮（OAuth 路径）；
  - “手动配置 IMAP/SMTP”折叠区（凭证只通过 TLS 提交，服务端立即加密落库，响应永不回显）；
  - “导入已有账户”按钮（只导入本地 vault 引用，不把 vault 明文发送给 RedClaw）。
- `syncIntervalMin` 暴露为 `5/15/30/60 min`；仅 Wi-Fi 是账户级同步策略，由服务端保存并由移动端网络状态作为额外 gate，不能只依赖 `localStorage`。
- 云端写入失败必须显示失败状态，不回退为“已同步”的本地假成功；本地草稿若保留，必须明确标记为 pending 且可重试。

#### B. OAuth 前端与后端闭环

- `emailApi.startOAuth({providerId, accountId?, redirectUri})` 由 pocketd 生成 state/PKCE，并把已配置的 provider `client_id` 注入授权 URL；client secret 只留在服务端。
- pending entry 必须绑定 `user_id + workspace_id + account_id?`，state 一次性、短 TTL、不可跨 workspace 使用；若是新账户，callback 后必须通过显式 `complete` 创建账户并返回新 account id。
- Capacitor 使用系统浏览器/deep link；Web 端使用同源 callback。callback 只交换 code、加密保存 token 并返回通用结果页，HTML 对邮箱地址和错误内容做编码，不把 token 写入 URL、日志或前端持久化存储。
- 成功链路必须可测试：`start → provider callback → complete/bind → first sync`；取消、过期 state、重复 callback 和 provider error 都是可恢复的明确状态。

#### C. 规则引擎（v1.5）

规则保存在 pocketd，RedClaw 只可返回建议或在账户显式开启后返回计划。规则 schema 采用版本化结构：`{id, version, enabled, match, actions, params, created_by, updated_at}`。

- 规则类型：`sender-whitelist` / `sender-blacklist` / `subject-keyword` / `domain-match` / `importance-min`。
- 动作：`mark-important`、`auto-archive`、`route-to-folder`、`trigger-autoreply`；每个动作声明能力、风险等级和账户开关。
- 同步钩子：`fetcher` 只提交 envelope/snippet 到规则入口；执行前再次校验 account/workspace 归属；同一 `message_uid + rule_version + action` 只执行一次。
- 规则变更产生审计记录；禁用规则只阻止后续执行，不删除历史结果。失败动作不修改原邮件状态，进入可重放的补偿队列。
- 前端规则编辑器必须显示“本地规则 / RedClaw 建议 / 待批准动作”，不能把建议直接伪装成已执行。

#### D. 自动回复（v1.5）

- `VacationReply` 配置属于 pocketd 的 account/workspace；RedClaw 可以生成模板建议，但不能获得 SMTP 凭证或直接调用 SMTP。
- scheduler 每分钟检查有效时间窗、收件人去重和账户级 `auto_reply_enabled`，以 `account_id + original_message_uid + vacation_version` 作为幂等键；发送前重新验证账户归属和 SMTP 可用性。
- 每次实际发送、跳过、失败都写审计和可重试状态；默认按收件人/原邮件去重，避免循环回复。模板使用受限 `text/template`，禁止执行任意代码。
- 前端显示“配置已保存”和“最近一次实际投递”两个不同状态；只有 SMTP 成功回执才能显示 sent。

#### E. 完整正文懒加载

- 后端新增 `GET /api/emails/{id}/body`，仅接受当前 JWT 的 user/workspace scope，不信任客户端提供的 account/workspace 覆盖值。
- 优先读加密 `body_path`；没有缓存时由 pocketd 使用已归属的 IMAP connection 按 provider UID 拉取、大小限制后缓存，再返回清洗后的正文。正文/附件不通过 RedClaw 控制面转发。
- 前端默认渲染 snippet；“查看完整正文”是用户显式操作，加载/失败/超限状态可见。附件下载另设最小权限接口，默认不自动同步。

#### F. 摘要分组维度

- 后端 `daily_summaries` 加列 `category_breakdown JSONB`、`sender_breakdown JSONB`，聚合查询始终带 user/workspace。
- 前端 `EmailSummaryView` 继续提供“时间线 / 发件人 Top 5 / Action Required / 类别分布” tabs；RedClaw 生成的类别必须标记为 suggestion，直到 pocketd 规则或用户确认后才成为事实。

### 4.4 RedClaw 邮件控制面 API 与事件

控制面 API 仅允许已认证的 workspace 成员调用，workspace/user 从 JWT claims 注入；不得信任 `workspace_id` query 参数、`X-Tenant-ID` 或请求 body 中的替代值。服务端向 RedClaw 发请求时使用同一 claims 生成 tenant context，并在返回后再次校验。

建议接口：

- `POST /api/redclaw/email/plan`：提交一个或多个 envelope/脱敏特征，返回 `plan_id`、`plan_version`、建议动作、风险级别和 `idempotency_key`；不接受密码、OAuth token、完整 MIME 或附件内容。
- `POST /api/redclaw/email/plan/{plan_id}/approve`：用户或受授权服务批准建议；审批记录绑定 user/workspace/rule version，不能跨账户重放。
- `GET /api/redclaw/email/plan/{plan_id}`：查询建议与执行状态，只能看到当前 workspace 的计划。
- `POST /api/redclaw/email/execute`：由 pocketd 执行已批准计划；RedClaw 不能直接触发 SMTP/IMAP 副作用。

事件采用 `email.control.plan_created|approved|executed|failed` 命名，payload 只含引用和状态：`workspace_id`、`account_id`、`message_uid`、`rule_id/version`、`plan_id`、`idempotency_key`、`result`、`occurred_at`。WebSocket 必须按 user+workspace 定向广播；不使用全局 `Broadcast`，也不使用只按 user 的广播作为邮件事件隔离。

### 4.5 前置修复与验收

在实现控制面前必须先修复以下数据面问题：

1. OAuth：注入有效 `client_id`；pending state 绑定 user/workspace/account；完整实现新建或绑定账户；显式处理加密错误、state 重放、provider error 和 HTML 输出编码；统一 provider key（例如 Gmail 不得在 `gmail` 与 `google` 间漂移）。
2. SMTP：修正 `/api/email/accounts/{id}/test-smtp` 路由；提供独立 SMTP host/port/security/username/password 配置；区分 STARTTLS 与 implicit TLS；凭证按字段加密保存，探针不得把密码当 username 或写入日志。
3. 归属：账户、邮件、规则、Vacation、sync status、摘要和所有更新/删除/查询都按 user+workspace scoped；客户端推送邮件必须验证 account ownership，不能用请求体覆盖归属字段。
4. 事件：email/OAuth/sync/RedClaw 事件按 user+workspace 定向；移动端 hub 也必须具备相同 scope，不能用全局 fan-out。
5. 测试：增加 OAuth start→callback→complete→sync、SMTP probe、重复 callback、跨 workspace read/write、事件隔离、规则重放、Vacation 幂等和正文权限集成测试。

验收必须区分“配置存在”和“副作用已发生”：

- Gmail/Outlook：系统浏览器授权后，账户绑定到发起 workspace，首次同步成功；取消/过期/重复 callback 不泄露 token。
- 手动账户：IMAP 与 SMTP 独立配置，STARTTLS/implicit TLS 探针结果准确，密码不回显。
- 规则：白名单等确定性规则可离线执行；RedClaw 建议显示为 pending，审批后才执行；重复同步不会重复归档或回复。
- Vacation：配置保存不等于 sent；同一原邮件在有效窗口内最多一次成功回复，失败可重试且不会循环回复。
- 正文：详情页显式点击后才拉取，越 workspace/account 访问返回 404/403，正文大小和清洗策略生效。
- 仅 Wi-Fi：移动数据下不启动同步任务；服务端策略与客户端网络 gate 同时生效。
---

## 5. AI 入口接入主机 ACP + RedClaw 调度（需求 #5）

### 5.1 问题定位

来源文件：

- `backend/internal/redclaw/` 5 个文件：`client.go:42-123`、`bridge.go:13-129`、`auth.go:1-46`、`types.go:5-72`、`audit.go:1-105`。**只有 Chat/Knowledge/Health**，无 ACP/Discovery。
- `backend/internal/agent/` 完整实现：JSON-RPC 2.0、`Transport`（stdio/HTTP/WS）、`Registry`、`adapter_acp_stdio.go:18-552`。**main.go:484-512 显式 Register，不自动扫描**。
- `backend/internal/registry/discovery.go:26-34,86-184` — 端口扫描 `{4096,14096-14100,3000,8080}`，并发 50 路 500ms 超时，**只支持 HTTP /global/health 探测，不扫 stdio/WS**。
- `backend/internal/registry/registry.go:16-94,420-495` — 实例 origin ∈ `discovered|registered|static|acc`，按 workspace 强隔离。
- `backend/internal/registry/workspace_scope_test.go:10-56` — 多租户隔离核心测试。
- `backend/internal/server/server.go:1085-1090` — `pushRedClawEvent` 推送 `redclaw.connected/disconnected`。
- `backend/internal/server/server_redclaw.go:10-108` — 三条代理：`/api/redclaw/{health,chat,knowledge/search}`。
- `backend/cmd/pocketd/main.go:484-512` — Adapter 注册：`opencode` 默认 + 每实例 + 可选 `acp-stdio` 写死路径。
- 前端 `/ai`：`router-mobile.ts:62-72` → `TasksView`，注释明写"复用"，**零 RedClaw/ACP 调用**。
- 前端 `api/` 16 个 .ts 全部无 `redclaw` import；`api/agents.ts` 是 /ai 实际接入的入口（`TasksView.vue:423-462` `sendQuickPrompt` 走 `agentsApi.send`）。
- 前端 WS 事件订阅：`ws-bus.ts:70-105` 只接 `note/email/vault`，**不接 redclaw.*/session.*/task_***；`TasksView.vue:299-308` 死代码订阅 `task_created/updated/session_attached`（后端不发）。

### 5.2 设计：主机 ACP 发现 + RedClaw 调度 + AI 三栏看板

#### A. 后端：ACP 主机级发现（复用 `registry.NetworkDiscovery` 模式）

新建 `backend/internal/redclaw/discovery.go`：

```go
package redclaw

// DiscoveryResult 发现的 ACP 服务
type DiscoveryResult struct {
    Host        string  `json:"host"`
    Port        int     `json:"port"`
    Transport   string  `json:"transport"`  // "stdio" | "http" | "ws"
    Endpoint    string  `json:"endpoint"`   // e.g. "/acp"
    AgentName   string  `json:"agent_name"`
    Version     string  `json:"version"`
    LatencyMs   int64   `json:"latency_ms"`
    WorkspaceID string  `json:"workspace_id"`
}

// Discover 扫描主机可用的 ACP 服务：
//   - HTTP/WS：复用 registry.NetworkDiscovery 的并发 50 路探测，
//     在 default ports 上加探测 "POST /acp" JSON-RPC initialize
//   - stdio：扫描 PATH 中的 echo / claude / codex 可执行文件
//     （仅在 dev mode 或 REDCLAW_DISCOVER_STDIO=true 时启用）
func (c *Client) Discover(ctx, workspaceID) ([]DiscoveryResult, error)
```

实现要点：

- 复用 `registry/discovery.go:86-184` 的并发探测模式，提取公共 `probeFn`。
- HTTP 探测：`POST /acp` body=`{"jsonrpc":"2.0","method":"initialize","params":{},"id":1}`，500ms 超时，验证 `result.serverInfo`。
- WS 探测：`ws://host:port/acp` Upgrade，成功即记录。
- stdio：遍历 `$PATH` 中的 `echo / claude / codex`；存在 + 可执行 → 试探启动 `echo` + `session/list`（仅 dev）。
- 全部结果缓存 60s（避免重复扫描）；通过 RedClaw `BridgeEvent` `redclaw.discovery.completed` 推送。

#### B. 后端：ACP 经 RedClaw 调度

改造 `backend/internal/agentbridge/bridge.go:Bridge.Send`：

- 增加选项 `opts.RedClawPreferred bool`：先调 RedClaw `Chat` 拿到"目标 agent ID"（按 workspace 模型路由），再走 agentbridge。
- 不破坏现有 `sendQuickPrompt` 行为（默认 `RedClawPreferred=false`）。

新增 `backend/internal/redclaw/scheduler.go`：

```go
// ScheduleTask 根据 workspace 路由策略，决定把任务交给：
//   - 本机 agentbridge (opencode / acp-stdio)
//   - 远端 RedClaw 节点
// 依据：模型可用性 / 任务来源 / workspace 配置
func ScheduleTask(req ScheduleRequest) (*ScheduleResponse, error)
```

新增 `POST /api/redclaw/discover` 与 `GET /api/redclaw/agents?workspace_id=...`：

- 触发一次 Discovery + 返回已注册 agents。
- 受 workspace 强隔离（参考 `workspace_scope_test.go:10-56`）。

#### C. 前端：`AiHubView.vue` 三栏看板

替换 `frontend/src/features/tasks/TasksView.vue` 在 `/ai` 路由的使用：

```vue
<AiHubView>  <!-- 三栏（折叠屏展开态）/ 两栏（中等）/ 单栏（紧凑） -->
  ──────────────────────────
  左栏 (master, 38%)：
    A. "运行中"（active tasks，跨主机/跨实例聚合）
       - 每条卡片显示：task 标题 + host + agent + 进度条 + ETA
    B. "可接管 ACP"（redclaw.discover 结果）
       - 卡片：host:port + transport + capabilities
       - CTA："接管" / "忽略"
  右栏 (detail, 62%)：
    C. "RedClaw 模型"（kxmemory / 远端节点）
       - 当前 workspace 可用模型 + 在线状态 + 延迟
       - "快速发起" 输入框
  ──────────────────────────
```

新增 API 客户端 `frontend/src/api/redclaw.ts`：

- `redclawApi.health()` → `GET /api/redclaw/health`
- `redclawApi.discover()` → `POST /api/redclaw/discover`
- `redclawApi.agents()` → `GET /api/redclaw/agents?workspace_id=...`
- `redclawApi.chat(req)` → `POST /api/redclaw/chat`
- WS 事件订阅：扩展 `ws-bus.ts` 增加 `redclaw.discovery.completed`、`redclaw.agent.online/offline`、`task.assigned` 等。

#### D. 与现有 TasksView 的兼容

- 不删 `TasksView.vue`（仍有 `sendQuickPrompt` 等逻辑），改为 `AiHubView.vue` 复用 TasksView 的"运行中/会话/已完成"三段 + 顶部 RedClaw 状态条 + 底部"可接管 ACP"折叠。
- `router-mobile.ts`：`/ai` → `AiHubView`；`/tasks` 保留 `TasksView`（普通任务聚合视图）。
- `TasksView.vue:299-308` 死订阅清掉；ws-bus.ts 实际订阅的事件名以 `server.go:1085` / `plugin_hub.go:498-522` 实际推送名为准。

### 5.3 验收

- `/ai` 三栏可见：左 master（运行中任务 + 可接管 ACP），右 detail（RedClaw 模型 + 快速发起）。
- 折叠屏展开态：双面板同屏；合盖：单栏 tab。
- 点"接管" → 调 `redclawApi.discover` → 列表新增该 host → 后端 agentbridge.Register。
- RedClaw 不在线时，UI 顶部状态条变灰 + "降级使用本地 agent" 提示。
- 多租户隔离：`workspace_id=B` 用户看不到 `workspace_id=A` 的 ACP 主机。

---

## 6. 总体技术决策

### 6.1 整合 RedClaw 的取舍

**RedClaw 当前定位**：独立的 LLM/Knowledge HTTP 网关，租户隔离、聊天、知识检索。

**改造后定位**：

- 保留：LLM Chat、知识检索、健康检查（与 acc 主线解耦）。
- 新增：主机 ACP Discovery、Agent 调度（决策本地 vs 远端）、多节点协调。
- 不动：Bridge 30s 心跳、AuditStore、租户 header 注入。

**取舍依据**：

- 不引入"主从 RedClaw + Pocket"的耦合，保持 Pocket 作为客户端入口。
- ACP 调度经过 RedClaw 是**可选路径**，本地 agentbridge 优先。
- Discovery 结果走 RedClaw BridgeEvent 推送，前端 ws-bus 订阅，与 `note/email/vault` 模式一致。

### 6.2 不在本设计范围（显式 YAGNI）

- 飞书/钉钉/Exchange 邮箱接入（v1.5）。
- Pocket 端侧脱机/多端同步 Evernote（v1.5）。
- 后端 ENEX 解析（前端解析保留 E2EE）。
- 重写 BottomNav / 设计新导航。
- RedClaw 节点的级联调度（多 RedClaw 节点协调，v1.5+）。
- 笔记/邮件的服务端 PKM 同步（与本地 storage 并行运行，v1.5）。

---

## 7. 实施路线（粗排，下一步 writing-plans 阶段细化）

```
Phase A — 折叠屏铺满 + 单一标题栏（基础）
  A1. AppLayout showTopBar meta + 移除 6 处 <h1> + 6 处 <h2>
  A2. 统一断点 560/840/1280（breakpoints.css + AppLayout）
  A3. SplitLayout 组件 + useBreakpoint 扩展
  A4. 6 个列表/详情页接入 SplitLayout

Phase B — 笔记去过度安全
  B1. crypto.cfg 单例 + localStorage 持久化
  B2. notes-store 读取 cfg 决定是否调 encryptString
  B3. LoginView 解除"密码=主密码"耦合
  B4. NoteListView 原地解锁面板

Phase C — ENEX 导入
  C1. fast-xml-parser + jszip 加依赖
  C2. evernote-parser.ts + EvernoteImporter.vue
  C3. assetStore.upsert({source:'enex_import'}) 接入
  C4. NoteListView 顶栏"导入"按钮

Phase D — 会议麦克风引导
  D1. useAppSettings composable（Capacitor App plugin）
  D2. MicStatusBar 组件 + MeetingRecordView 顶栏接入
  D3. error-card 升级为 ErrorActionCard（含 CTA + retry）

Phase E — 邮件数据面与 RedClaw 控制面
  E0. 先修 OAuth client_id、pending user/workspace/account 绑定、callback 错误处理与 provider key
  E1. 修正账户归属与 user+workspace scoped store/query/event；补 SMTP 独立配置和可达路由
  E2. EmailAccountSetup listProviders + OAuth start/callback/complete 三选一表单
  E3. syncIntervalMin + 服务端仅 Wi-Fi 策略与移动端网络 gate
  E4. 完整正文/附件显式懒加载（GET /api/emails/{id}/body）
  E5. 版本化本地规则、幂等 action plan、失败补偿与可撤销执行
  E6. Vacation scheduler + SMTP 实际投递、去重、回执与重试
  E7. RedClaw email plan/approve/execute 控制面与 scoped 事件
  E8. OAuth/SMTP/权限/跨 workspace/规则/Vacation/正文集成测试

Phase F — AI/RedClaw/ACP
  F1. backend/internal/redclaw/discovery.go（HTTP/WS/stdio）
  F2. POST /api/redclaw/discover + GET /api/redclaw/agents
  F3. Bridge.Send 加 RedClawPreferred 可选项
  F4. frontend/src/api/redclaw.ts + ws-bus 扩展
  F5. AiHubView.vue 替换 /ai + 折叠屏适配

Phase G — 测试 + 部署
  G1. Playwright 折叠屏 e2e
  G2. Android Emulator 真机验证
  G3. iOS Simulator 验证
  G4. 多租户隔离回归测试
```

---

## 8. 审计（Analyst 自审）

### 8.1 完整性

| 需求 | 设计覆盖 | 缺口 |
|------|----------|------|
| 1 折叠屏 + 单一标题栏 | ✅ AppLayout meta + SplitLayout + 断点统一 + 6 处标题清理 | 无 |
| 2 笔记去过度安全 + ENEX | ✅ 三档 + 原地解锁 + ENEX 前端解析 | 旧用户迁移路径需要在 B 阶段细化 |
| 3 会议麦克风引导 | ✅ useAppSettings + MicStatusBar + ErrorActionCard | iOS 上"跳设置"限制，需要 UI 文案 fallback |
| 4 邮件账户/同步/分类/自动回复 | ✅ 已定义 pocketd 数据面与 RedClaw 控制面边界、OAuth/SMTP 前置修复、版本化规则、正文懒加载、仅 Wi-Fi 与审计验收 | 当前仓库仍有 OAuth 绑定、SMTP 路由、正文/自动回复执行、workspace scope 和事件隔离缺口；完成 Phase E 后才可标记 ready |
| 5 AI 入口 + ACP + RedClaw | ✅ Discovery + 调度 + AiHubView + ws-bus 扩展 | 当前仓库仍只有 health/chat/knowledge 三路；stdio 扫描仅 dev mode（出于安全） |

### 8.2 风险点

1. **Phase B 笔记去加密**：移除字段 AES-GCM 后，"vault" 是否还依赖？需要审计 `crypto.ts` 是否被 vault/`useVaultCrypto` 调用；若依赖，保留 `crypto.cfg` 提供"按需开启"。
2. **Phase D iOS 限制**：iOS 不允许 app 跳系统设置，需要 fallback 文案引导（"请到 设置 > Pocket > 麦克风"）。
3. **Phase E 邮件控制面**：RedClaw 只处理最小化、脱敏的 envelope/snippet 特征；完整正文、凭证、SMTP 副作用留在 pocketd。任何需要扩大数据范围的 AI 能力都必须单独审批并更新隐私/租户测试。
4. **Phase E OAuth/SMTP 数据面**：OAuth deep link、provider client_id、账户绑定和 SMTP security mode 必须先通过集成测试；文档中的目标 API 不代表当前路由已经存在。
5. **Phase E 幂等与回滚**：规则、自动回复和控制面计划必须有版本、幂等键、审计状态和补偿路径；不能以配置 CRUD 或“已生成计划”代替实际副作用回执。
6. **Phase F stdio 扫描**：误把系统关键可执行文件当 agent，需白名单 + 路径前缀校验（如 `/usr/local/bin/` + `codex/claude/echo`）。
7. **Phase F 多租户**：Discovery 必须带 `workspace_id`，结果集按 workspace 过滤；参考 `workspace_scope_test.go:10-56` 的覆盖测试。
8. **ENEX 性能**：100MB+ ENEX 在 WebView 内存可能爆，E2E 测试覆盖；提供"分批导入" UI 兜底。

### 8.3 显式排除

- **不引入** VueUse（已有自研 `useBreakpoint`，扩展即可）。
- **不引入** Capacitor Community SQLCipher 以外的加密插件。
- **不重构** `BottomNav`、`BottomNav` 的 4+1 模式保留。
- **不引入** Redux/Pinia 改造，沿用现有 `stores/` 模式。
- **不删** `TasksView.vue`，仅把 `/ai` 切到 `AiHubView`。

### 8.4 验收矩阵

| 场景 | 期望 | 验证手段 |
|------|------|----------|
| Pixel Fold 展开 | 列表 + 详情双面板，**无第二标题** | Android Emulator + Playwright |
| Galaxy 合盖 | 单栏 + top-bar 单标题 | Android Emulator |
| iPhone 14 Pro | 单栏 + top-bar | iOS Simulator |
| 笔记 ENEX 导入 100 条 | < 8s，进度可见 | Playwright |
| Gmail OAuth | 系统浏览器授权 + 回调落地 | Android 真机 |
| 飞书邮箱 | UI 提示"v1.5" | UI 截图 |
| RedClaw 离线 | 顶部状态条灰 + 降级提示 | 单元测试 |
| Discovery 多租户隔离 | ws-A 看不到 ws-B | `workspace_scope_test.go` 扩展 |
| 麦克风拒绝 | MicStatusBar 红 + 去设置按钮 | Android Emulator |
| iOS 麦克风拒绝 | 文案引导到"设置 > Pocket > 麦克风" | iOS Simulator |

### 8.5 与既有审计/Round 对齐

- R8 P1 租户隔离（`docs/security/R8.md`）：本设计所有 Discovery / Agents / Email accounts 都强制 `workspace_id` 隔离，回归 `workspace_scope_test.go:10-56`。
- R7 P2 资源耗尽（`AUDIT_R7_FIXES_VERIFICATION.md`）：ENEX 分批导入避免内存爆。
- 红龙虾（RedClaw）当前未染指 ACP（`/api/redclaw/*` 三条 + 30s 心跳），本设计扩展为"ACP 调度可选经过"，**不破坏**现有 chat/knowledge/health。
- 字段级 AES-GCM（`notes-store.ts:74-87`）：本设计默认关闭，仅在高级模式开启，与 vault/PKM 共存。

---

## 9. 待用户确认点

请用户在进入 writing-plans 阶段前确认以下三点：

1. **Phase B 默认档（去字段加密 + 保留 SQLCipher）是否可接受**？如要保留双层加密，Phase B 范围需调整（不引入 `crypto.cfg`，仅做"原地解锁"）。
2. **Phase E 飞书/钉钉/Exchange 是否推迟 v1.5**？若必须本期内，Phase E 范围需扩张（约 +1 周）。
3. **Phase F stdio 扫描范围**？默认仅 dev mode 启用（出于安全）。生产启用需要白名单配置。

确认后即进入 writing-plans 阶段细化每个 Phase 的 task 列表。