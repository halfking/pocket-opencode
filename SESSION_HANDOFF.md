# OpenCode Pocket — 当前状态与后续任务分配

> 生成时间: 2026-07-07  
> 最新提交: `224e0f6 feat(ui): Codex-style compact AI hub with voice input`

---

## 一、项目概览

OpenCode Pocket 是一个移动端优先的 AI 编程代理管理应用，由 Go 后端 + Vue.js 前端组成，用于管理多个 OpenCode 实例的会话、任务、权限和问题。

**架构栈:**
- 后端: Go (`pocketd`)，PostgreSQL，WebSocket，SSE
- 前端: Vue 3 + Pinia + Capacitor (Android)
- AI 代理: OpenCode 实例（通过 HTTP API 连接）

---

## 二、已完成功能

### 后端 (Go)
| 模块 | 状态 | 说明 |
|------|------|------|
| 认证 (JWT) | ✅ | admin/admin 开发登录，JWT 签发/验证 |
| 实例注册 | ✅ | 从 `POCKET_OPENCODE_INSTANCES` JSON 加载 |
| 会话管理 | ✅ | 列表、创建、删除、消息获取、发送 prompt |
| SSE 事件流 | ✅ | 共享上游连接 + 按会话过滤分发 |
| 任务管理 | ✅ | CRUD + 会话关联 |
| 笔记模块 | ✅ | CRUD + 分类 + 语音笔记 |
| 邮箱模块 | ✅ | IMAP 账户 + 同步 + 分类 + 摘要 |
| 密码箱 | ✅ | 零知识加密同步 |
| LLM 网关 | ✅ | 配置管理 + 热推送到 OpenCode 实例 |
| 权限管理 | ✅ | 事件驱动 + 轮询兜底 + 转发回复 |
| 问题管理 | ✅ | 同权限管理模式 |
| 插件 WebSocket | ✅ | 三种连接类型 (Plugin/Manager/Client) |

### 前端 (Vue 3)
| 模块 | 状态 | 说明 |
|------|------|------|
| 登录/认证 | ✅ | JWT + Lobster 本地加密初始化 |
| AI 任务看板 | ✅ | Codex 风格双面板：运行中 + 会话 + 语音栏 |
| 会话对话 | ✅ | SSE 流式渲染 + 工具调用卡片 + 语音按钮 |
| 会话列表 | ✅ | 分页 + 搜索 + 实例过滤 |
| 任务详情 | ✅ | 操作按钮 (恢复/暂停/完成/附加/删除) |
| 实例列表 | ✅ | 从 API 加载 + 选择持久化 |
| 笔记模块 | ✅ | CRUD + 语义搜索 + 语音录制 |
| 邮箱模块 | ✅ | 分类过滤 + AI 摘要 + 标记已读 |
| 密码箱 | ✅ | 主密码 + 生物识别 + 云同步 |
| 设置页 | ✅ | 用户信息 + LLM 网关配置 |
| 设计系统 | ✅ | CSS tokens + 暗色模式 + 响应式 |

---

## 三、已知问题与待办

### 前端
1. **STT 语音识别未接入** — 录音按钮存在但转写是占位文本 `[语音输入完成]`
2. **任务状态更新未持久化** — `updateStatus()` 只修改本地状态，未调用 API
3. **任务删除未实现** — 只有 `router.push('/ai')`，无 API 调用
4. **部分视图样式不一致** — SessionListView、InstanceListView 仍用硬编码颜色
5. **OpenCode store 端点缺失** — `loadSessionHistory()` 和 `getSessionSummary()` 命中不存在的后端路由
6. **Home.vue 用模拟数据** — 从未使用，可清理

### 后端
1. **STT 端点返回 501** — `POST /api/stt/transcribe` 是 stub
2. **Echo 框架双轨** — `mobile_api.go` 用 Echo，主服务器用 `net/http`，未整合
3. **LLM 网关配置不持久化** — 存在内存变量，重启丢失
4. **游标分页未实现** — 使用 offset 替代
5. **WebSocket origin 检查未实现** — 允许所有来源

---

## 四、后续任务分配

### 会话 A: UI 设计与体验优化
**目标:** 完善移动端 UI 设计，提升交互体验，统一设计语言

**重点任务:**
1. 统一所有视图为 CSS tokens（SessionListView、InstanceListView、SettingsView）
2. 接入真实 STT 到语音输入（连接 sherpa-onnx 或 Groq Whisper）
3. 实现任务状态更新和删除的 API 调用
4. 优化会话对话视图的消息渲染（Markdown、代码高亮）
5. 完善空状态和加载状态的视觉设计
6. 实现深色模式适配
7. 手势优化（滑动删除、长按菜单）

### 会话 B: 后端服务与 OpenCode 探测
**目标:** 完善后端 API，实现 OpenCode 实例自动发现和管理

**重点任务:**
1. 实现 STT 端点（集成 Groq Whisper 或本地 sherpa-onnx）
2. 统一 API 框架（消除 Echo 双轨）
3. 实现 OpenCode 实例自动发现（网络扫描 + mDNS）
4. 实现任务状态更新和删除 API
5. LLM 网关配置持久化到 PostgreSQL
6. 实现游标分页
7. 实现 WebSocket origin 检查
8. 补全 OpenCode store 的 session history 和 summary 端点

---

## 五、技术债务

| 项目 | 优先级 | 说明 |
|------|--------|------|
| Echo 双轨 | 高 | `mobile_api.go` 用 Echo，主服务器用 `net/http` |
| 数据库驱动不一致 | 中 | `opencode/store.go` 用 `database/sql`，其他用 `pgxpool` |
| 重复组件 | 低 | 两个 BottomNav 组件 |
| 模拟数据页面 | 低 | Home.vue、ComponentDemo.vue、OptimizedDemo.vue |
| http-proxy 依赖 | 低 | 前端无使用，可移除 |

---

## 六、开发环境

| 组件 | 地址 | 说明 |
|------|------|------|
| 后端 | `http://localhost:8088` | `POCKET_DEV_AUTH=true` |
| OpenCode | `http://127.0.0.1:14096` | local-dev 实例 |
| 模拟器 | `emulator-5554` | Android API 35 |
| 前端 Dev | `http://localhost:5173` | Vite 开发服务器 |

**构建命令:**
```bash
# 前端
cd frontend
VITE_API_BASE=http://10.0.2.2:8088 npx vite build
npx cap sync android
cd android && JAVA_HOME=/Library/Java/JavaVirtualMachines/jdk-21.jdk/Contents/Home ./gradlew assembleDebug

# 后端
cd backend && POCKET_DEV_AUTH=true ./pocketd
```

---

## 七、文件结构

```
opencode-pocket/
├── backend/                    # Go 后端
│   ├── cmd/pocketd/main.go    # 入口
│   ├── internal/
│   │   ├── adapter/           # OpenCode HTTP 适配器
│   │   ├── auth/              # JWT 认证
│   │   ├── config/            # 环境变量配置
│   │   ├── db/                # PostgreSQL 连接池
│   │   ├── opencode/          # OpenCode 领域管理器
│   │   ├── registry/          # 实例注册与发现
│   │   ├── server/            # HTTP 路由与处理器
│   │   └── websocket/         # WebSocket Hub
│   └── start-dev.sh           # 开发启动脚本
├── frontend/                   # Vue 3 前端
│   ├── src/
│   │   ├── api/               # API 客户端 (client, sse, websocket, stt)
│   │   ├── app/               # 路由、布局、App.vue
│   │   ├── components/        # 共享组件 (base, interactive, business)
│   │   ├── features/          # 功能模块 (tasks, sessions, notes, email, vault)
│   │   ├── stores/            # Pinia 状态管理
│   │   ├── styles/            # 设计系统 (tokens, breakpoints, responsive)
│   │   ├── composables/       # 组合式函数
│   │   └── native/            # Capacitor 原生模块
│   ├── capacitor.config.ts
│   └── android/               # Android 原生项目
└── opencode-plugin/            # OpenCode 插件
```
