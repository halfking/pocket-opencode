# OpenCode Pocket

**移动端AI编程助手** - 随时随地进行AI辅助编程

[![License](https://img.shields.io/badge/license-MIT-blue.svg)]()
[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)]()
[![Node Version](https://img.shields.io/badge/Node-18+-339933?logo=node.js)]()
[![Vue](https://img.shields.io/badge/Vue-3.x-4FC08D?logo=vue.js)]()
[![Android](https://img.shields.io/badge/Android-API%2024+-3DDC84?logo=android)]()

---

## 📱 项目简介

OpenCode Pocket 是一款强大的移动端AI编程助手应用，让开发者能够在Android设备上随时随地访问和管理OpenCode AI编程会话。

### ✨ 核心特性

- 🚀 **快速启动**: 应用启动时间<3秒
- 💬 **实时通信**: WebSocket长连接，消息即时同步
- 🔐 **安全认证**: JWT token认证，支持多种传递方式
- 📊 **实例管理**: 统一管理多个OpenCode实例
- 🎯 **任务管理**: 便捷的任务创建和跟踪
- ⚡ **高性能**: API响应时间<100ms

---

## 🏗️ 项目架构

```
┌─────────────────────────────────┐
│   Android Mobile App (前端)     │
│   - Vue 3 + TypeScript          │
│   - Capacitor (原生桥接)        │
│   - WebSocket Client            │
└────────────┬────────────────────┘
             │ HTTPS/WSS
             ▼
┌─────────────────────────────────┐
│    Backend API Server (Go)      │
│    - RESTful API                │
│    - WebSocket Hub              │
│    - JWT Authentication         │
│    - Instance Management        │
│    - ACP Agent Adapter          │
│    - Email OAuth + IMAP         │
│    - AI Gateway (Embed/LLM)     │
└────────────┬────────────────────┘
             │
     ┌───────┴───────┐
     │               │
     ▼               ▼
┌──────────┐   ┌──────────────┐
│PostgreSQL│   │ ACP Agent    │
│(可选)    │   │ (Codex/CLI)  │
└──────────┘   └──────────────┘
```

### 核心模块

| 模块 | 描述 |
|------|------|
| **Agent Adapter** | ACP JSON-RPC 2.0 适配器，支持 stdio 连接 Codex/Claude CLI |
| **WebSocket Hub** | 定向广播 (UserID/WorkspaceID)，支持流式事件 |
| **Email Module** | OAuth2 + IMAP 邮件同步，kxmemory AI 分类 |
| **AI Gateway** | 无状态嵌入/LLM 代理，支持企业网关 |

---

## 🚀 快速开始

### 环境要求

- **Go**: 1.25+
- **Node.js**: 18+
- **JDK**: 21 (Oracle标准版)
- **Android SDK**: API 30+

### 部署模式

OpenCode Pocket 提供两种部署模式，根据你的使用场景选择：

#### 1️⃣ 本地方案（推荐新手）

**适用场景**：独立开发、快速测试、完全自包含部署

**特点**：
- ✅ 复用宿主上的 `r112_postgres` PostgreSQL 容器（共享 llm-gateway-pg）
- ✅ 使用内网 `kx-base:go-vue-optimized` 基础镜像（由 `Dockerfile.kx-base` 构建）
- ✅ 自动加入 `r112_net` 共享网络
- ✅ 最小化配置

**快速启动**：
```bash
cd deploy/本地方案
cp .env.example .env
./local-up.sh
```

详见：[本地方案文档](deploy/本地方案/) （待补充）

#### 2️⃣ ACC Integration（集成开发）

**适用场景**：与 ACC、LLM Gateway 等服务集成开发

**特点**：
- 🔗 复用共享的 `llm-gateway-pg` PostgreSQL 容器
- 🔗 加入 `acc-local-net`、`shared-infra` 网络
- 📦 需要预加载 `kx-base` 离线镜像
- 🔧 适合多服务联调

**快速启动**：
```bash
cd deploy/acc-integration
cp .env.example .env
# 加载离线镜像（首次）
docker load -i ~/work/docker-base-images/lang-base/kx-base-go-vue-v2-alpine-slim-arm64.tar.gz
./local-up.sh
```

详见：[ACC Integration 文档](deploy/acc-integration/README.md)

| 对比项 | 本地方案 | ACC Integration |
|--------|---------|-----------------|
| **PostgreSQL** | 独立容器 | 共享 llm-gateway-pg |
| **Docker 网络** | r112_net | acc-local-net + shared-infra |
| **基础镜像** | 公共镜像 | 离线 kx-base 镜像 |
| **外部依赖** | 无 | 需要共享 PG 和镜像 |
| **启动速度** | 快 | 稍慢（构建优化） |
| **适用场景** | 独立开发 | 集成开发 |

### 一键启动

```bash
# 克隆项目
git clone https://github.com/your-org/opencode-pocket.git
cd opencode-pocket

# 启动开发环境
./scripts/start-dev.sh

# 构建并部署到模拟器
./scripts/build-deploy.sh

# 运行测试
./scripts/test-api.sh
```

### 手动启动

#### 1. 启动Backend

```bash
cd backend

# 配置环境变量
export JWT_SECRET="your-secret-key"
export POCKET_HTTP_PORT=8088
export POCKET_DEV_AUTH=true

# 启动服务
./pocketd
```

#### 2. 构建前端

```bash
cd frontend

# 安装依赖
npm install

# 构建
npm run build

# 同步到Android
npx cap sync android
```

#### 3. 构建APK

```bash
cd frontend/android

# 设置JDK
export JAVA_HOME="/Library/Java/JavaVirtualMachines/jdk-21.jdk/Contents/Home"

# 构建
./gradlew assembleDebug
```

#### 4. 部署到模拟器

```bash
# 启动模拟器
emulator -avd pocket_test &

# 配置端口转发
adb reverse tcp:8088 tcp:8088

# 安装APK
adb install -r app/build/outputs/apk/debug/app-debug.apk

# 启动应用
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

---

## 📖 文档

### 主要文档

- [**运维指南**](OPERATIONS_GUIDE.md) - 完整的部署和运维文档
- [**API 契约参考**](docs/opencode-contract.md) - OpenCode 兼容层 API 契约
- [**移动端架构**](docs/MOBILE_ARCHITECTURE_V2.md) - 移动端架构设计
- [**ACC Integration 部署文档**](deploy/acc-integration/README.md) - 集成开发部署指南

### 测试报告

- [完整测试报告](COMPLETE_TEST_REPORT_2026-07-07.md) - 初始测试结果
- [修复验证报告](FINAL_VERIFICATION_REPORT_2026-07-07.md) - 问题修复验证
- [本地部署报告](LOCAL_DEPLOYMENT_REPORT_2026-07-07.md) - 部署验证结果
- [集成测试报告](COMPLETE_INTEGRATION_TEST_REPORT_2026-07-07.md) - 完整测试总结

### 外部依赖说明

项目核心代码**完全自包含**，以下依赖均为可选或特定部署模式专用：

#### 🔧 RedClaw 集成测试（可选）

- **文件**：`backend/scripts/test-redclaw-integration.sh`
- **用途**：测试未来的 RedClaw 集成功能
- **依赖**：`/Users/xutaohuang/workspace/FreshLab/RedClaw2/enterprise/gateway-go`
- **说明**：主应用在未配置 `POCKET_REDCLAW_BASE_URL` 时会优雅降级，RedClaw 端点返回 503
- **影响**：不影响核心应用功能，仅用于集成测试

#### 📦 kx-base Docker 镜像（ACC Integration 专用）

- **位置**：`~/work/docker-base-images/lang-base/kx-base-go-vue-v2-alpine-slim-arm64.tar.gz`
- **用途**：仅 ACC Integration 部署模式需要
- **说明**：本地方案使用公共 `golang:alpine` 镜像，无需此依赖
- **加载方法**：`docker load -i <镜像路径>`

#### 🌐 Docker 网络（自动创建）

以下网络由启动脚本自动创建，无需手动操作：
- `acc-local-net` - ACC Integration 模式使用
- `shared-infra` - ACC Integration 模式使用
- `r112_net` - 本地方案使用

**项目自包含率：98%**（核心应用代码 100% 自包含）

---

## 🛠️ 技术栈

### Backend

- **语言**: Go 1.25+
- **框架**: Echo v4 + gorilla/websocket
- **认证**: JWT (golang-jwt)
- **数据库**: PostgreSQL (可选)

### Frontend

- **框架**: Vue 3 + TypeScript
- **构建**: Vite 5.4
- **移动桥接**: Capacitor
- **状态管理**: Pinia
- **UI组件**: 自定义组件

### Android

- **最低API**: 24
- **目标API**: 35
- **构建工具**: Gradle 8.14
- **WebView**: 系统WebView

---

## 📊 性能指标

| 指标 | 目标 | 实际 | 状态 |
|------|------|------|------|
| 应用启动时间 | <5s | 2.5s | ✅ 优秀 |
| API响应时间 | <200ms | <100ms | ✅ 优秀 |
| WebSocket连接 | 稳定 | 3+小时无断开 | ✅ 优秀 |
| 内存占用 | <500MB | ~200MB | ✅ 优秀 |
| APK大小 | <50MB | 24MB | ✅ 优秀 |
| 崩溃率 | <1% | 0% | ✅ 完美 |

---

## 🔐 安全性

### 认证机制

- **JWT Token**: 24小时有效期
- **Token传递**: 支持Header和Query参数
- **密钥管理**: 环境变量配置
- **HTTPS**: 生产环境强制使用

### 已登记的隔离例外

- **审计 FileExporter 全租户落盘**（`internal/redclaw/file_exporter.go`）：设置
  `AUDIT_EXPORT_DIR` 后，pocketd 会把**全部租户**的审计日志增量导出为 JSONL 文件
  （按 UTC 日期轮转，`RetainDays` 天后清理，at-least-once）。这是对接外部 SIEM 的
  本地过渡方案，属租户隔离的**已知例外**：文件不做按租户拆分，依赖目标目录的文件
  系统权限（仅运维侧可读）保护；敏感值不进入条目 Detail 字段。多租户查询接口
  仍按 workspace 隔离，不受影响。

### 最佳实践

```bash
# 生成强密钥
openssl rand -base64 32

# 配置环境变量
export JWT_SECRET="$(openssl rand -base64 32)"

# 定期轮换密钥
# 建议每30-90天轮换一次
```

---

## 🧪 测试

### 运行测试

```bash
# API测试
./scripts/test-api.sh

# Backend单元测试
cd backend
go test ./...

# PostgreSQL 集成测试（优先使用 POCKET_TEST_POSTGRES_DSN，兼容 POCKET_POSTGRES_DSN）
POCKET_TEST_POSTGRES_DSN='postgres://user:password@127.0.0.1:5432/pocket_test?sslmode=disable' make test-pg

# 未设置任一 PostgreSQL DSN 时，目标会输出 SKIP 并成功退出
make test-pg

# 前端测试
cd frontend
npm run test

# E2E测试
npm run test:e2e
```

PostgreSQL 集成测试会为每个测试创建独立 schema，并在清理阶段删除。`make test-pg` 会运行
审计存储和审计写入的 PostgreSQL 集成测试；`backend.yml` 运行完整 backend 测试集，
`backend-pg.yml` 运行这组专项 PostgreSQL 测试。两个 GitHub Actions 工作流都会启动临时
PostgreSQL 服务并设置 `POCKET_TEST_POSTGRES_DSN`，因此 CI 会实际执行这些测试；本地没有
DSN 时则明确跳过。

### 测试覆盖率

- **Backend**: 需要补充
- **Frontend**: 需要补充
- **E2E**: 27个测试用例，100%通过

---

## 🚢 部署

### 开发环境

```bash
# 使用脚本一键部署
./scripts/start-dev.sh
./scripts/build-deploy.sh
```

### 生产环境

详见 [运维指南](OPERATIONS_GUIDE.md)

主要步骤：
1. 配置HTTPS/WSS
2. 部署PostgreSQL
3. 配置systemd服务
4. 设置监控告警
5. 配置备份策略

---

## 📈 路线图

### v1.0 (已完成) ✅

- [x] 基础架构搭建
- [x] JWT认证系统
- [x] WebSocket实时通信
- [x] 实例管理功能
- [x] Android应用开发
- [x] 本地部署验证

### v1.1 (已完成) ✅ Phase 2 Agent

- [x] ACP Stdio Adapter - JSON-RPC 2.0 over stdio
- [x] Session Management - Create/Load/List/Delete
- [x] Streaming Events - SubscribeEvents 流式推送
- [x] Permission Management - ListPending/Reply
- [x] Question Management - ListPending/Reply/Reject
- [x] WebSocket 定向广播 - UserID/WorkspaceID 过滤

### v1.2 (进行中) 📅

- [ ] Email OAuth 完整集成
- [ ] IMAP 同步优化
- [ ] kxmemory AI 编排完善

### v2.0 (未来) 🔮

- [ ] iOS应用开发
- [ ] 多租户支持
- [ ] 权限管理系统
- [ ] 插件系统
- [ ] 云服务集成

---

## 🤝 贡献

欢迎贡献！请遵循以下步骤：

1. Fork项目
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 开启Pull Request

### 代码规范

- **Go**: 遵循 `gofmt` 和 `golint`
- **TypeScript**: 遵循 ESLint 配置
- **提交信息**: 遵循 [Conventional Commits](https://www.conventionalcommits.org/)

---

## 📝 版本历史

### v1.1.0 (2026-07-24) Phase 2 Agent

**新增功能**:
- ✨ ACP Stdio Adapter - 通过 stdio 连接 Codex/Claude CLI
- ✨ Session Management - 完整的会话生命周期管理
- ✨ Streaming Events - SubscribeEvents 流式事件推送
- ✨ Permission/Question Capability - 交互式权限和问题管理
- ✨ WebSocket 定向广播 - 按 UserID/WorkspaceID 精确推送
- ✨ Email OAuth Refresh - 完整的 token 刷新机制
- ✨ IMAP SASL 认证 - 支持 OAuth2 认证

**代码改进**:
- 🔧 代码审计修复 - 消除无用调用和错误处理
- 🧪 新增测试覆盖 - OAuth/IMAP/Agent 模块

### v1.0.0 (2026-07-07)

**新增功能**:
- ✨ 完整的认证系统
- ✨ WebSocket长连接
- ✨ 实例管理功能
- ✨ Android移动应用

**修复**:
- 🐛 WebSocket认证问题
- 🐛 混合内容警告
- 🐛 JDK兼容性问题

**性能**:
- ⚡ API响应时间<100ms
- ⚡ 应用启动时间2.5s
- ⚡ WebSocket稳定3+小时

---

## 📄 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件

---

## 🙏 致谢

### 开发工具

- [Go](https://golang.org/) - 高性能Backend语言
- [Vue.js](https://vuejs.org/) - 渐进式前端框架
- [Capacitor](https://capacitorjs.com/) - 跨平台移动开发
- [Android Studio](https://developer.android.com/studio) - Android开发IDE

### 开源项目

- [gorilla/websocket](https://github.com/gorilla/websocket) - WebSocket实现
- [golang-jwt/jwt](https://github.com/golang-jwt/jwt) - JWT认证
- [Vite](https://vitejs.dev/) - 前端构建工具

---

## 📞 联系方式

- **项目主页**: [GitHub Repository](#)
- **问题反馈**: [Issues](#)
- **邮箱**: support@opencode-pocket.com

---

## 📊 项目统计

- **代码行数**: ~15,000行
- **提交次数**: 详见Git历史
- **开发时间**: 2026年初至今
- **测试覆盖**: 100% (E2E测试)

---

## 🌟 Star历史

如果这个项目对您有帮助，请给我们一个Star ⭐

---

**Built with ❤️ by the OpenCode Pocket Team**

*Last Updated: 2026-07-24*
