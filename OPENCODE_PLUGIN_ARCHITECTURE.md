# OpenCode 插件 + 管理服务架构方案

**目标**: 实现 OpenCode 实例的全面管理、实时通信和远程控制

---

## 🎯 核心需求分析

### 1. OpenCode 插件
- 在 OpenCode 中运行的插件
- 注册实例到 Pocket Backend
- 上报任务状态和会话数据
- 接收远程控制指令

### 2. 实例管理服务
- 轻量级服务，部署在每台机器上
- 启动/停止 OpenCode 实例
- 监控健康状态
- WebSocket 实时通信

### 3. 推送和通知
- 任务状态变更推送
- 实时会话更新
- 远程操作响应

---

## 🏗️ 系统架构设计

### 整体架构

```
┌──────────────────────────────────────────────────────────┐
│  Android/iOS App (OpenCode Pocket)                       │
│  - 查看所有 OpenCode 实例                                  │
│  - 实时接收任务更新                                        │
│  - 远程启动/停止实例                                       │
│  - 推送通知                                               │
└──────────────────────────────────────────────────────────┘
           ↕ HTTP/WebSocket
┌──────────────────────────────────────────────────────────┐
│  Pocket Backend (Central Server)                         │
│  - 实例注册中心                                            │
│  - WebSocket Hub                                          │
│  - 任务状态聚合                                            │
│  - 推送服务                                               │
└──────────────────────────────────────────────────────────┘
           ↕ WebSocket (Bidirectional)
┌──────────────────────────────────────────────────────────┐
│  机器 1: OpenCode Instance Manager                        │
│  ┌────────────────────────────────────────────────────┐  │
│  │  Instance Manager Service (轻量级守护进程)          │  │
│  │  - WebSocket 客户端                                 │  │
│  │  - 启动/停止 OpenCode                               │  │
│  │  - 健康检查                                         │  │
│  └────────────────────────────────────────────────────┘  │
│           ↕ Local HTTP                                   │
│  ┌────────────────────────────────────────────────────┐  │
│  │  OpenCode Instance (port 4096)                     │  │
│  │  + OpenCode Pocket Plugin                          │  │
│  │    - 启动时自动注册                                  │  │
│  │    - 上报任务状态                                    │  │
│  │    - 监听控制指令                                    │  │
│  └────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────┐
│  机器 2, 3, 4... (同样的架构)                             │
└──────────────────────────────────────────────────────────┘
```

---

## 📦 组件设计

### 组件 1: OpenCode Pocket Plugin

**位置**: OpenCode 插件系统  
**语言**: TypeScript  
**功能**:

```typescript
// opencode-pocket-plugin/src/index.ts

interface PocketPluginConfig {
  backendURL: string          // Pocket Backend 地址
  instanceID: string          // 实例唯一 ID
  displayName: string         // 显示名称
  autoRegister: boolean       // 启动时自动注册
  reportInterval: number      // 状态上报间隔（秒）
}

class OpenCodePocketPlugin {
  private ws: WebSocket
  private config: PocketPluginConfig
  private sessionWatcher: SessionWatcher
  
  async onActivate() {
    // 1. 连接到 Pocket Backend WebSocket
    await this.connectToPocketBackend()
    
    // 2. 注册实例
    await this.registerInstance()
    
    // 3. 启动会话监听器
    this.sessionWatcher = new SessionWatcher()
    this.sessionWatcher.on('session:created', this.onSessionCreated)
    this.sessionWatcher.on('session:updated', this.onSessionUpdated)
    this.sessionWatcher.on('session:completed', this.onSessionCompleted)
    
    // 4. 启动心跳
    this.startHeartbeat()
  }
  
  // 注册实例到 Backend
  async registerInstance() {
    const info = {
      id: this.config.instanceID,
      displayName: this.config.displayName,
      version: this.getOpenCodeVersion(),
      capabilities: ['session', 'summary', 'pty'],
      environment: this.detectEnvironment(),
      machine: {
        hostname: os.hostname(),
        platform: os.platform(),
        arch: os.arch(),
      }
    }
    
    this.ws.send(JSON.stringify({
      type: 'instance.register',
      data: info
    }))
  }
  
  // 监听会话创建
  onSessionCreated(session: Session) {
    this.ws.send(JSON.stringify({
      type: 'session.created',
      data: {
        instanceID: this.config.instanceID,
        session: this.serializeSession(session)
      }
    }))
  }
  
  // 监听会话更新
  onSessionUpdated(session: Session) {
    this.ws.send(JSON.stringify({
      type: 'session.updated',
      data: {
        instanceID: this.config.instanceID,
        sessionID: session.id,
        changes: session.getChanges()
      }
    }))
  }
  
  // 接收远程控制指令
  onRemoteCommand(command: RemoteCommand) {
    switch (command.type) {
      case 'session.create':
        return this.createSession(command.data)
      case 'session.prompt':
        return this.sendPrompt(command.data.sessionID, command.data.prompt)
      case 'session.stop':
        return this.stopSession(command.data.sessionID)
      case 'instance.shutdown':
        return this.shutdown()
    }
  }
}
```

**插件配置文件**:

```json
// .opencode/pocket-plugin.json
{
  "backendURL": "wss://pocket.your-domain.com/ws",
  "instanceID": "dev-macbook-pro",
  "displayName": "开发 MacBook Pro",
  "autoRegister": true,
  "reportInterval": 30,
  "auth": {
    "type": "token",
    "token": "<instance-auth-token>"
  }
}
```

---

### 组件 2: Instance Manager Service

**位置**: 每台机器上的守护进程  
**语言**: Go (轻量、跨平台)  
**功能**:

```go
// opencode-instance-manager/main.go

package main

type InstanceManager struct {
    config      Config
    ws          *websocket.Conn
    opencode    *OpenCodeProcess
    healthCheck *HealthChecker
}

type Config struct {
    BackendURL     string   `json:"backendURL"`
    InstanceID     string   `json:"instanceID"`
    OpenCodePath   string   `json:"opencodePath"`
    AutoStart      bool     `json:"autoStart"`
    Port           int      `json:"port"`
}

func (m *InstanceManager) Start() error {
    // 1. 连接到 Backend
    if err := m.connectToBackend(); err != nil {
        return err
    }
    
    // 2. 自动启动 OpenCode（如果配置）
    if m.config.AutoStart {
        if err := m.startOpenCode(); err != nil {
            return err
        }
    }
    
    // 3. 启动健康检查
    go m.healthCheck.Start()
    
    // 4. 监听控制命令
    go m.listenForCommands()
    
    return nil
}

// 启动 OpenCode
func (m *InstanceManager) startOpenCode() error {
    cmd := exec.Command("bun", "run", "dev")
    cmd.Dir = m.config.OpenCodePath
    
    if err := cmd.Start(); err != nil {
        return fmt.Errorf("failed to start OpenCode: %w", err)
    }
    
    m.opencode = &OpenCodeProcess{
        PID:       cmd.Process.Pid,
        StartTime: time.Now(),
    }
    
    // 等待 OpenCode 启动
    if err := m.waitForOpenCode(); err != nil {
        return err
    }
    
    // 上报状态
    m.reportStatus("running")
    
    return nil
}

// 停止 OpenCode
func (m *InstanceManager) stopOpenCode() error {
    if m.opencode == nil {
        return nil
    }
    
    // 优雅停止
    if err := m.opencode.GracefulShutdown(); err != nil {
        // 强制停止
        if err := m.opencode.Kill(); err != nil {
            return err
        }
    }
    
    m.reportStatus("stopped")
    return nil
}

// 监听控制命令
func (m *InstanceManager) listenForCommands() {
    for {
        var msg ControlMessage
        if err := m.ws.ReadJSON(&msg); err != nil {
            log.Printf("WebSocket read error: %v", err)
            m.reconnect()
            continue
        }
        
        switch msg.Type {
        case "instance.start":
            m.startOpenCode()
        case "instance.stop":
            m.stopOpenCode()
        case "instance.restart":
            m.stopOpenCode()
            time.Sleep(2 * time.Second)
            m.startOpenCode()
        case "instance.update":
            m.updateOpenCode(msg.Data)
        }
    }
}

// 健康检查
func (h *HealthChecker) Check() HealthStatus {
    status := HealthStatus{
        Timestamp: time.Now(),
    }
    
    // 检查进程
    status.ProcessRunning = h.isProcessRunning()
    
    // 检查 HTTP API
    resp, err := http.Get("http://localhost:4096/api/health")
    status.APIResponding = (err == nil && resp.StatusCode == 200)
    
    // 检查 CPU/内存
    status.CPU = h.getCPUUsage()
    status.Memory = h.getMemoryUsage()
    
    return status
}
```

**配置文件**:

```json
// /etc/opencode-instance-manager/config.json
{
  "backendURL": "wss://pocket.your-domain.com/ws",
  "instanceID": "dev-macbook-pro",
  "opencodePath": "/Users/username/workspace/ai/opencode",
  "autoStart": true,
  "port": 4096,
  "auth": {
    "token": "<manager-auth-token>"
  },
  "healthCheck": {
    "interval": 30,
    "timeout": 5
  }
}
```

**安装脚本**:

```bash
#!/bin/bash
# install-instance-manager.sh

# 下载二进制文件
curl -L https://github.com/your-org/opencode-instance-manager/releases/latest/download/opencode-manager-$(uname -s)-$(uname -m) \
  -o /usr/local/bin/opencode-manager

chmod +x /usr/local/bin/opencode-manager

# 创建配置目录
mkdir -p /etc/opencode-instance-manager

# 生成配置文件
cat > /etc/opencode-instance-manager/config.json << EOF
{
  "backendURL": "wss://pocket.your-domain.com/ws",
  "instanceID": "$(hostname)",
  "opencodePath": "$HOME/workspace/ai/opencode",
  "autoStart": true,
  "port": 4096
}
EOF

# 安装为系统服务
# macOS (launchd)
if [[ "$(uname)" == "Darwin" ]]; then
  cat > ~/Library/LaunchAgents/com.opencode.manager.plist << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.opencode.manager</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/opencode-manager</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
EOF
  launchctl load ~/Library/LaunchAgents/com.opencode.manager.plist
fi

# Linux (systemd)
if [[ "$(uname)" == "Linux" ]]; then
  sudo cat > /etc/systemd/system/opencode-manager.service << EOF
[Unit]
Description=OpenCode Instance Manager
After=network.target

[Service]
ExecStart=/usr/local/bin/opencode-manager
Restart=always
User=$USER

[Install]
WantedBy=multi-user.target
EOF
  sudo systemctl daemon-reload
  sudo systemctl enable opencode-manager
  sudo systemctl start opencode-manager
fi

echo "✅ OpenCode Instance Manager 安装完成"
```

---

### 组件 3: Backend WebSocket Hub

**位置**: Pocket Backend  
**语言**: Go  
**功能**:

```go
// backend/internal/websocket/hub.go

type Hub struct {
    // 连接的实例管理器
    managers map[string]*ManagerConnection
    
    // 连接的 OpenCode 插件
    instances map[string]*InstanceConnection
    
    // 连接的移动端客户端
    clients map[string]*ClientConnection
    
    // 广播通道
    broadcast chan Message
    
    // 注册/注销通道
    register   chan Connection
    unregister chan Connection
}

// 处理实例注册
func (h *Hub) handleInstanceRegister(conn *InstanceConnection, data InstanceInfo) {
    h.instances[data.ID] = conn
    
    // 通知所有客户端
    h.broadcastToClients(Message{
        Type: "instance.registered",
        Data: data,
    })
    
    // 持久化到数据库
    h.registry.RegisterInstance(data)
}

// 处理会话更新
func (h *Hub) handleSessionUpdate(instanceID string, update SessionUpdate) {
    // 更新缓存
    h.cache.UpdateSession(instanceID, update)
    
    // 推送到移动端
    h.broadcastToClients(Message{
        Type: "session.updated",
        Data: map[string]interface{}{
            "instanceID": instanceID,
            "update":     update,
        },
    })
    
    // 触发推送通知
    if update.Status == "completed" {
        h.pushNotification(instanceID, update.SessionID, "任务完成")
    }
}

// 远程控制实例
func (h *Hub) sendCommandToInstance(instanceID string, command Command) error {
    conn, ok := h.instances[instanceID]
    if !ok {
        // 实例离线，尝试通过 Manager 启动
        return h.sendCommandToManager(instanceID, command)
    }
    
    return conn.SendCommand(command)
}
```

---

## 📱 移动端集成

### WebSocket 客户端

```typescript
// frontend/src/services/websocket-client.ts

class OpenCodeWebSocketClient {
  private ws: WebSocket
  private eventEmitter: EventEmitter
  
  connect() {
    this.ws = new WebSocket('wss://pocket.your-domain.com/ws')
    
    this.ws.onmessage = (event) => {
      const message = JSON.parse(event.data)
      
      switch (message.type) {
        case 'instance.registered':
          this.eventEmitter.emit('instance:online', message.data)
          break
        case 'instance.offline':
          this.eventEmitter.emit('instance:offline', message.data)
          break
        case 'session.created':
          this.eventEmitter.emit('session:created', message.data)
          // 显示通知
          this.showNotification('新任务创建', message.data.session.title)
          break
        case 'session.updated':
          this.eventEmitter.emit('session:updated', message.data)
          break
        case 'session.completed':
          this.eventEmitter.emit('session:completed', message.data)
          // 显示通知
          this.showNotification('任务完成', message.data.session.title)
          break
      }
    }
  }
  
  // 远程启动实例
  async startInstance(instanceID: string) {
    this.ws.send(JSON.stringify({
      type: 'control.start',
      target: instanceID
    }))
  }
  
  // 远程停止实例
  async stopInstance(instanceID: string) {
    this.ws.send(JSON.stringify({
      type: 'control.stop',
      target: instanceID
    }))
  }
  
  // 创建会话
  async createSession(instanceID: string, prompt: string) {
    this.ws.send(JSON.stringify({
      type: 'session.create',
      target: instanceID,
      data: { prompt }
    }))
  }
}
```

---

## 🔔 推送通知集成

### Firebase Cloud Messaging (FCM)

```typescript
// backend/internal/push/fcm.go

type PushNotificationService struct {
    fcmClient *fcm.Client
}

func (s *PushNotificationService) SendSessionNotification(
    deviceToken string,
    instanceID string,
    sessionID string,
    title string,
    message string,
) error {
    notification := &fcm.Message{
        Token: deviceToken,
        Notification: &fcm.Notification{
            Title: title,
            Body:  message,
        },
        Data: map[string]string{
            "type":       "session.update",
            "instanceID": instanceID,
            "sessionID":  sessionID,
        },
        Android: &fcm.AndroidConfig{
            Priority: "high",
        },
        APNS: &fcm.APNSConfig{
            Payload: &fcm.APNSPayload{
                Aps: &fcm.Aps{
                    Sound: "default",
                    Badge: 1,
                },
            },
        },
    }
    
    _, err := s.fcmClient.Send(context.Background(), notification)
    return err
}
```

---

## 📊 数据流示例

### 场景 1: 新任务创建

```
1. 用户在 OpenCode 中创建新会话
   ↓
2. OpenCode Pocket Plugin 监听到事件
   ↓
3. Plugin 通过 WebSocket 发送到 Backend
   {
     type: "session.created",
     instanceID: "dev-macbook",
     session: { id, title, ... }
   }
   ↓
4. Backend 接收并处理
   - 更新数据库
   - 更新缓存
   - 转发到所有连接的移动端
   ↓
5. 移动端接收实时更新
   - 更新任务列表
   - 显示通知
   - 震动提醒
```

### 场景 2: 远程启动实例

```
1. 用户在移动端点击"启动实例"
   ↓
2. 移动端发送控制命令
   {
     type: "control.start",
     target: "dev-macbook"
   }
   ↓
3. Backend 转发到 Instance Manager
   ↓
4. Instance Manager 启动 OpenCode
   - 执行 bun run dev
   - 等待启动完成
   ↓
5. OpenCode 启动后，Plugin 自动注册
   ↓
6. Backend 收到注册，通知移动端
   {
     type: "instance.online",
     instanceID: "dev-macbook",
     status: "healthy"
   }
   ↓
7. 移动端更新 UI
   - 实例状态变为"在线"
   - 显示绿色指示灯
```

---

## 🚀 实施计划

### Phase 1: OpenCode Plugin 开发 (1-2 周)

**任务**:
1. 创建 OpenCode 插件项目结构
2. 实现 WebSocket 客户端
3. 实现会话监听器
4. 实现远程控制接收
5. 编写插件配置和文档

**交付物**:
- OpenCode Pocket Plugin (npm package)
- 插件配置指南
- API 文档

### Phase 2: Instance Manager 开发 (1-2 周)

**任务**:
1. 创建 Go 项目
2. 实现 WebSocket 客户端
3. 实现 OpenCode 进程管理
4. 实现健康检查
5. 跨平台打包

**交付物**:
- Instance Manager 二进制文件 (macOS, Linux, Windows)
- 安装脚本
- systemd/launchd 配置
- 配置指南

### Phase 3: Backend WebSocket Hub (1 周)

**任务**:
1. 扩展现有 WebSocket Hub
2. 实现实例注册中心
3. 实现消息路由
4. 实现推送通知集成

**交付物**:
- Backend 更新
- WebSocket API 文档
- 部署指南

### Phase 4: 移动端集成 (1 周)

**任务**:
1. 实现 WebSocket 客户端
2. 实现实时更新 UI
3. 实现远程控制功能
4. 实现推送通知

**交付物**:
- 更新的移动端应用
- 用户指南

### Phase 5: 测试和优化 (1 周)

**任务**:
1. 端到端测试
2. 性能优化
3. 安全加固
4. 文档完善

---

## 💡 技术选型建议

### OpenCode Plugin
- **语言**: TypeScript
- **框架**: OpenCode Plugin API
- **WebSocket**: `ws` 库
- **构建**: Vite/esbuild

### Instance Manager
- **语言**: Go
- **WebSocket**: `gorilla/websocket`
- **进程管理**: `os/exec`
- **跨平台**: 支持 macOS, Linux, Windows

### Backend
- **语言**: Go (现有)
- **WebSocket**: `gorilla/websocket` (现有)
- **推送**: FCM SDK
- **数据库**: PostgreSQL (现有)

### 移动端
- **语言**: TypeScript + Vue 3
- **WebSocket**: `@vueuse/core` useWebSocket
- **推送**: Capacitor Push Notifications
- **本地存储**: SQLite (现有)

---

## 🔒 安全考虑

### 1. 认证和授权
- 实例注册需要 token
- WebSocket 连接需要 JWT
- 远程控制需要权限验证

### 2. 加密通信
- 使用 WSS (WebSocket Secure)
- TLS 1.3+
- 证书验证

### 3. 访问控制
- 基于角色的访问控制 (RBAC)
- 实例所有权验证
- 操作日志记录

---

## 📈 扩展性考虑

### 1. 负载均衡
- Backend 可以水平扩展
- WebSocket 连接分发
- Redis Pub/Sub 用于跨实例通信

### 2. 高可用
- Instance Manager 自动重启
- Backend 多实例部署
- 数据库主从复制

### 3. 监控和告警
- Prometheus metrics
- Grafana dashboard
- 告警规则配置

---

## 📝 下一步

想要我帮你：
1. ✅ 开始实现 OpenCode Plugin 原型？
2. ✅ 编写 Instance Manager 详细规范？
3. ✅ 设计 WebSocket 消息协议？
4. ✅ 创建项目结构和初始代码？

---

**创建时间**: 2026-07-06  
**作者**: Kiro AI  
**状态**: 设计方案完成，等待实施决策
