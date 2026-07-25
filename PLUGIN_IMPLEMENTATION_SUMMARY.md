# OpenCode 插件架构实现 - 项目交付总结

**日期**: 2026-07-06  
**状态**: MVP 原型完成

---

## 🎉 交付成果

### 已完成的组件

#### 1. OpenCode Pocket Plugin ✅
**位置**: `opencode-plugin/`  
**语言**: TypeScript  
**功能**:
- ✅ WebSocket 客户端实现
- ✅ 自动实例注册
- ✅ 会话监听器框架
- ✅ 远程控制接收
- ✅ 心跳和重连机制
- ✅ TypeScript 类型定义

**文件清单**:
```
opencode-plugin/
├── src/
│   ├── index.ts          # 主入口 (350+ 行)
│   └── types.ts          # 类型定义
├── package.json          # 包配置
├── tsconfig.json         # TypeScript 配置
├── tsup.config.ts        # 构建配置
└── README.md             # 完整文档
```

**核心功能**:
```typescript
const plugin = new OpenCodePocketPlugin(config)
await plugin.activate()

// 自动完成:
// 1. 连接 Backend WebSocket
// 2. 注册实例
// 3. 监听会话事件
// 4. 发送心跳
// 5. 处理远程命令
```

#### 2. Instance Manager Service ✅
**位置**: `opencode-manager/`  
**语言**: Go  
**功能**:
- ✅ 守护进程实现
- ✅ OpenCode 进程管理 (启动/停止/重启)
- ✅ 健康检查
- ✅ WebSocket 客户端
- ✅ 自动重连
- ✅ 命令处理

**文件清单**:
```
opencode-manager/
├── main.go               # 主程序 (500+ 行)
├── go.mod                # Go 模块配置
├── install.sh            # 安装脚本
└── README.md             # 完整文档
```

**核心功能**:
```bash
# 自动完成:
# 1. 连接 Backend
# 2. 注册管理器
# 3. 启动 OpenCode (如果 autoStart)
# 4. 健康检查
# 5. 处理远程命令
```

---

## 📦 项目结构

```
opencode-pocket/
├── opencode-plugin/              # OpenCode 插件
│   ├── src/
│   │   ├── index.ts             # 主实现
│   │   └── types.ts             # 类型定义
│   ├── package.json
│   ├── tsconfig.json
│   └── README.md
│
├── opencode-manager/             # 实例管理器
│   ├── main.go                  # Go 实现
│   ├── go.mod
│   ├── install.sh               # 安装脚本
│   └── README.md
│
├── backend/                      # Backend (现有)
│   ├── internal/
│   │   └── websocket/           # WebSocket Hub (待扩展)
│   └── ...
│
└── docs/
    ├── OPENCODE_PLUGIN_ARCHITECTURE.md  # 架构设计
    ├── SIMULATOR_TEST_REPORT.md         # 测试报告
    └── ...
```

---

## 🚀 快速开始

### 1. 构建 Plugin

```bash
cd opencode-plugin
npm install
npm run build

# 生成:
# dist/index.js
# dist/index.d.ts
```

### 2. 构建 Manager

```bash
cd opencode-manager
go mod tidy
go build -o opencode-manager main.go

# 或跨平台构建:
GOOS=darwin GOARCH=arm64 go build -o opencode-manager-darwin-arm64 main.go
GOOS=linux GOARCH=amd64 go build -o opencode-manager-linux-amd64 main.go
```

### 3. 配置和运行

#### Plugin 配置

```json
// .opencode/pocket-plugin.json
{
  "backendURL": "wss://pocket.your-domain.com",
  "instanceID": "dev-macbook",
  "displayName": "开发 MacBook",
  "autoRegister": true,
  "reportInterval": 30,
  "auth": {
    "type": "token",
    "token": "your-token"
  }
}
```

#### Manager 配置

```json
// /etc/opencode-instance-manager/config.json
{
  "backendURL": "wss://pocket.your-domain.com",
  "instanceID": "dev-macbook",
  "opencodePath": "/path/to/opencode",
  "autoStart": true,
  "port": 4096,
  "authToken": "your-token",
  "healthCheck": {
    "interval": 30,
    "timeout": 5
  }
}
```

---

## 💡 使用示例

### 场景 1: 集成 Plugin 到 OpenCode

```typescript
// 在 OpenCode 插件系统中
import OpenCodePocketPlugin from '@opencode-pocket/plugin'
import config from './.opencode/pocket-plugin.json'

export async function activate() {
  const plugin = new OpenCodePocketPlugin(config)
  await plugin.activate()
  
  console.log('Pocket plugin activated')
}

export async function deactivate() {
  await plugin.deactivate()
}
```

### 场景 2: 部署 Manager 到服务器

```bash
# 下载并运行安装脚本
curl -fsSL https://install.opencode-pocket.com | bash

# 或手动安装
./install.sh

# 服务自动启动
systemctl status opencode-manager
```

### 场景 3: 远程控制实例

```bash
# 通过移动端 App:
# 1. 查看所有实例列表
# 2. 点击"启动" → Manager 收到命令 → 启动 OpenCode
# 3. Plugin 自动注册 → 实例状态变为 "online"
# 4. 查看实时任务 → 接收 WebSocket 推送
```

---

## 📊 通信协议

### WebSocket 消息类型

#### Plugin → Backend

```json
// 实例注册
{
  "type": "instance.register",
  "data": {
    "id": "dev-macbook",
    "displayName": "开发 MacBook",
    "version": "0.1.0",
    "capabilities": ["session", "summary", "pty"],
    "environment": "development"
  }
}

// 会话创建
{
  "type": "session.created",
  "data": {
    "instanceID": "dev-macbook",
    "session": {
      "id": "session-123",
      "title": "新任务",
      "status": "active"
    }
  }
}

// 心跳
{
  "type": "heartbeat",
  "data": {
    "instanceID": "dev-macbook",
    "timestamp": "2026-07-06T12:00:00Z"
  }
}
```

#### Backend → Plugin

```json
// 远程命令
{
  "type": "command",
  "data": {
    "id": "cmd-123",
    "type": "session.create",
    "data": {
      "prompt": "创建一个新任务"
    }
  }
}

// Ping
{
  "type": "ping"
}
```

#### Manager → Backend

```json
// 管理器注册
{
  "type": "manager.register",
  "data": {
    "instanceID": "dev-macbook",
    "hostname": "MacBook-Pro.local",
    "version": "0.1.0"
  }
}

// 状态上报
{
  "type": "instance.status",
  "data": {
    "instanceID": "dev-macbook",
    "status": "running",
    "pid": 12345,
    "uptime": 3600
  }
}
```

#### Backend → Manager

```json
// 控制命令
{
  "type": "command.start",
  "data": {}
}

{
  "type": "command.stop",
  "data": {}
}

{
  "type": "command.restart",
  "data": {}
}
```

---

## 🔧 下一步工作

### Phase 2: Backend WebSocket Hub 扩展

**需要实现**:

```go
// backend/internal/websocket/plugin_hub.go

type PluginHub struct {
    plugins   map[string]*PluginConnection
    managers  map[string]*ManagerConnection
    clients   map[string]*ClientConnection
    broadcast chan Message
}

func (h *PluginHub) HandlePluginRegister(conn *PluginConnection, data InstanceInfo) {
    // 1. 保存连接
    h.plugins[data.ID] = conn
    
    // 2. 持久化到数据库
    h.registry.RegisterInstance(data)
    
    // 3. 广播到所有客户端
    h.broadcastToClients(Message{
        Type: "instance.online",
        Data: data,
    })
}

func (h *PluginHub) HandleSessionCreated(instanceID string, session SessionInfo) {
    // 1. 保存到数据库
    h.db.CreateSession(session)
    
    // 2. 推送到移动端
    h.broadcastToClients(Message{
        Type: "session.created",
        Data: session,
    })
    
    // 3. 发送推送通知
    h.pushService.Send(session)
}

func (h *PluginHub) SendCommandToInstance(instanceID string, command Command) error {
    conn, ok := h.plugins[instanceID]
    if !ok {
        return fmt.Errorf("instance not connected")
    }
    
    return conn.SendCommand(command)
}
```

### Phase 3: 移动端集成

**需要实现**:

```typescript
// frontend/src/services/plugin-websocket.ts

class PluginWebSocketClient {
  connect() {
    this.ws = new WebSocket('wss://pocket.your-domain.com/ws?type=client')
    
    this.ws.onmessage = (event) => {
      const msg = JSON.parse(event.data)
      
      switch (msg.type) {
        case 'instance.online':
          this.onInstanceOnline(msg.data)
          break
        case 'session.created':
          this.showNotification('新任务', msg.data.session.title)
          break
      }
    }
  }
  
  startInstance(instanceID: string) {
    this.ws.send(JSON.stringify({
      type: 'control.start',
      target: instanceID
    }))
  }
}
```

### Phase 4: 推送通知

**需要实现**:

```typescript
// frontend/src/services/push-notifications.ts

import { PushNotifications } from '@capacitor/push-notifications'

export async function setupPushNotifications() {
  await PushNotifications.register()
  
  PushNotifications.addListener('pushNotificationReceived', (notification) => {
    console.log('Push received:', notification)
  })
}
```

---

## 📈 实施时间表

### 已完成 (本次)
- ✅ OpenCode Plugin 实现
- ✅ Instance Manager 实现
- ✅ 文档和配置

### Phase 2 (1 周)
- Backend WebSocket Hub 扩展
- 实例注册中心
- 消息路由

### Phase 3 (1 周)
- 移动端 WebSocket 集成
- 实时 UI 更新
- 远程控制界面

### Phase 4 (3-5 天)
- 推送通知集成 (FCM)
- 测试和优化
- 部署文档

---

## 🎯 使用场景演示

### 场景 A: 开发者工作流

```
1. 开发者在 MacBook 上运行 OpenCode
   ↓
2. Plugin 自动注册到 Backend
   ↓
3. 移动端 App 显示 "MacBook 在线"
   ↓
4. 开发者创建新会话
   ↓
5. 移动端实时收到通知 "新任务: 实现登录功能"
   ↓
6. 任务完成后，移动端收到推送 "任务完成"
```

### 场景 B: 远程管理

```
1. 用户外出，需要启动家里的 OpenCode
   ↓
2. 打开移动端 App，点击 "启动实例"
   ↓
3. Manager 收到命令，启动 OpenCode
   ↓
4. Plugin 自动注册
   ↓
5. 移动端显示 "实例已启动，状态: healthy"
   ↓
6. 用户可以查看所有任务和会话
```

### 场景 C: 多实例协作

```
1. 开发者有 3 台机器：MacBook, Linux 服务器, Windows PC
   ↓
2. 每台都运行 Manager + Plugin
   ↓
3. 移动端 App 显示所有 3 个实例
   ↓
4. 可以查看所有实例的任务汇总
   ↓
5. 可以远程控制任意实例
   ↓
6. 所有任务状态实时同步
```

---

## 📝 总结

### 本次完成的工作

1. ✅ 设计了完整的三层架构
2. ✅ 实现了 OpenCode Plugin (TypeScript)
3. ✅ 实现了 Instance Manager (Go)
4. ✅ 提供了完整的文档和配置
5. ✅ 创建了安装脚本
6. ✅ 定义了通信协议

### 项目状态

- **OpenCode Plugin**: MVP 完成，可以开始集成测试
- **Instance Manager**: MVP 完成，可以开始部署测试
- **Backend Hub**: 需要扩展 (Phase 2)
- **移动端**: 需要集成 (Phase 3)

### 下一步建议

1. **立即**: 测试 Plugin 和 Manager 的独立功能
2. **本周**: 实现 Backend WebSocket Hub 扩展
3. **下周**: 集成移动端并端到端测试

---

**项目位置**: `opencode-pocket/`  
**文档**: 
- `opencode-plugin/README.md`
- `opencode-manager/README.md`
- `OPENCODE_PLUGIN_ARCHITECTURE.md`

**联系**: 如有问题，请查看文档或提 Issue

---

**创建时间**: 2026-07-06  
**作者**: Kiro AI  
**版本**: MVP v0.1.0
