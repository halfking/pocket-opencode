# 🧱 代码骨架实施说明

**版本**: v1.0.0
**日期**: 2026-07-02
**状态**: 骨架已落地，待接入

> 本文记录本轮已创建/修改的文件、它们的作用，以及把骨架接入可运行状态所需的剩余步骤。配套主方案 [`2026-07-02-android-personal-assistant-plan.md`](./2026-07-02-android-personal-assistant-plan.md)。

---

## ✅ 本轮已完成

### 设计文档（5 份，均在 `docs/`）
| 文档 | 内容 |
|------|------|
| `2026-07-02-android-personal-assistant-plan.md` | 主方案：架构、5 大模块、UI/UX、数据流、路线图 |
| `2026-07-02-android-stt-evaluation.md` | 本地语音识别评估（sherpa-onnx 为主，含设备分级） |
| `2026-07-02-email-assistant-design.md` | 邮箱助手专项设计 |
| `2026-07-02-password-vault-design.md` | 密码箱专项安全设计（含威胁模型） |
| `2026-07-02-implementation-skeleton.md` | 本文档 |

### 前端骨架（16 个文件，在 `frontend/src/`）

**基础设施**
- `styles/tokens.css` — CSS 变量设计系统 + 暗色模式（替换硬编码颜色）
- `api/http.ts` — 统一 fetch 封装 + auth token 注入
- `stores/auth.ts` — Pinia auth store（替换假 admin/admin）
- `stores/device.ts` — 设备分级 + STT 引擎选择
- `app/AppLayout.vue` — 统一 Layout（TopBar + 内容 + BottomNav）
- `components/BottomNav.vue` — 单一底部导航（5 主功能 + 更多菜单）

**API 层（分模块）**
- `api/notes.ts` — 语音笔记 CRUD + 分类 + 搜索
- `api/email.ts` — 多账户/邮件/每日总结
- `api/vault.ts` — 密码箱（委托给原生插件）
- `api/stt.ts` — STT 调度器（本地优先 + Groq 云端兜底）

**原生桥接（Capacitor 插件 TS 接口）**
- `native/keystore.ts` — cap-keystore 插件接口 + web 端降级 stub
- `native/sherpa.ts` — cap-sherpa 插件接口（sherpa-onnx）
- `native/util.ts` — `registerPluginSafely` 通用辅助

**功能骨架页**
- `features/notes/NoteListView.vue` — 笔记列表 + 域色标
- `features/notes/VoiceRecorderWidget.vue` — 浮动录音 FAB（长按录音→转写）
- `features/email/EmailInboxView.vue` — 邮件列表 + 分类筛选 + AI 摘要
- `features/vault/VaultListView.vue` — 密码箱解锁/列表/生成器

**接入点修改**
- `main.ts` — 接入 Pinia + tokens.css
- `app/router-mobile.ts` — 新增 `/ai` `/notes` `/email` `/vault` 路由
- `package.json` — 新增 `pinia` 依赖

### 后端骨架（4 个 Go 包，在 `backend/internal/`，全部 `go build` + `go vet` 通过）
| 包 | 文件 | 状态 |
|----|------|------|
| `notes` | `note.go`, `store.go` | 完整 CRUD（Upsert/List/Delete） |
| `email` | `model.go`, `store.go`, `fetcher.go`, `scheduler.go` | Store 完整；Fetcher/Scheduler 为带 TODO 的骨架 |
| `vault` | `store.go` | 完整（PutLatest/GetLatest 加密 blob） |
| `stt` | `transcribe.go` | 完整 Groq Whisper 云端兜底代理 |

---

## 🔌 接入剩余工作（让骨架跑起来）

### 后端：把新包接进 server.go（遵循现有 task/feishu 模式）

1. **`internal/config/config.go`** — 新增字段：
   ```go
   GroqAPIKey        string // POCKET_GROQ_API_KEY
   EmailMasterKey    string // POCKET_EMAIL_MASTER_KEY  (邮箱凭证加密)
   KxMemoryBaseURL   string // POCKET_KXMEMORY_BASE_URL (转发 AI 请求)
   ```

2. **`cmd/pocketd/main.go`** — 构造新 store 并传入 server：
   ```go
   notesStore, _ := notes.NewStore(cfg.DBPath)
   emailStore, _ := email.NewStore(cfg.DBPath)
   vaultStore, _ := vault.NewStore(cfg.DBPath)
   transcriber := stt.NewTranscriber(cfg.GroqAPIKey, "", "")
   // 扩展 server.New 签名
   ```

3. **`internal/server/server.go`** — Server 结构体新增字段 + 注册路由：
   ```go
   mux.HandleFunc("/api/notes", s.handleNotes)
   mux.HandleFunc("/api/notes/", s.handleNoteOps)
   mux.HandleFunc("/api/email/accounts", s.handleEmailAccounts)
   mux.HandleFunc("/api/emails", s.handleEmails)
   mux.HandleFunc("/api/emails/", s.handleEmailOps)
   mux.HandleFunc("/api/email/summaries", s.handleEmailSummaries)
   mux.HandleFunc("/api/vault/sync", s.handleVaultSync)
   mux.HandleFunc("/api/stt/transcribe", s.handleSttTranscribe)
   ```
   每个 handler 沿用 `handleTasks` / `handleTaskOperations` 的模式（method 分发 + splitPath 子路径）。

4. **启动调度器**：main.go 中 `scheduler := email.NewScheduler(emailStore, fetcher); scheduler.Start(ctx)`。

### 前端：安装依赖 + 修复接入

1. `cd frontend && npm install`（拉取 pinia）
2. 现有 view（TasksView/SessionListView/InstanceListView/SettingsView）的 bottom-nav 重复标记暂时保留；建议逐步把它们改成用 `<AppLayout>` 包裹（非阻塞，可后做）
3. LoginView 接入 `useAuthStore().setLegacyUser(...)`（向后兼容现有 localStorage flag）

### 原生插件（Phase 1/3/4 再做）
- `cap-keystore`（Kotlin）：见密码箱设计文档第 4 节接口
- `cap-sherpa`（Kotlin）：封装 sherpa-onnx Android AAR
- 装入现成 Capacitor 插件：`@capacitor-community/biometric`、`@capacitor/local-notifications`

---

## 🗺️ 骨架对应的实施阶段

| 阶段 | 对应骨架 | 剩余实现 |
|------|---------|---------|
| Phase 0 基础设施 | tokens.css, AppLayout, BottomNav, auth store, http.ts | 把现有 view 迁到 AppLayout；接真实登录 API |
| Phase 1 语音笔记 MVP | notes store, notes api, NoteListView, VoiceRecorderWidget, stt transcriber | kxmemory 笔记 API；录音持久化为文件；server.handleNotes |
| Phase 2 邮箱助手 | email store/fetcher/scheduler, email api, EmailInboxView | go-imap 接入；kxmemory 分类/总结；账户配置向导 |
| Phase 3 密码箱 | vault store, vault api, keystore.ts, VaultListView | cap-keystore 原生插件；生物识别；加密同步 |
| Phase 4 会议 + 本地 STT | sherpa.ts, device store, stt.ts | cap-sherpa 原生插件；声纹；会议模块页 |

---

## ⚠️ 当前骨架的已知简化

1. **STT**：`stt.ts` 已实现本地优先+云端兜底逻辑，但 `sherpa.transcribe` 在原生插件就绪前会抛错，会自动走云端（需服务端 Groq key）
2. **录音**：`VoiceRecorderWidget` 用浏览器 MediaRecorder，转写后 `audioPath` 当前是 blob URL；生产需写入文件供原生 STT 读取
3. **Auth**：`useAuthStore` 保留 legacy `pocket_user` flag 兼容，真实 JWT 登录 API 待 Phase 0 接入
4. **Pinia**：依赖已加到 package.json，需 `npm install` 后才能构建

骨架设计为可渐进增强：每个模块在缺失原生能力时优雅降级（抛错→UI 提示），不会阻塞其他模块。
