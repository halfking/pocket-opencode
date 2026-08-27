> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/governance/STATUS-MATRIX.md`](docs/governance/STATUS-MATRIX.md)
> Do NOT use this doc for current implementation decisions.
> This doc is a point-in-time sprint report; 当前能力以治理矩阵为准。

# OpenCode Pocket 部署前自我审计报告

**审计日期**: 2026-07-04 16:39  
**审计目的**: 确保应用可以正常使用，避免已知问题

---

## 🔍 审计清单

### 1. 后端服务审计

#### 1.1 服务状态
```bash
✅ 后端进程运行中
✅ 监听端口: 8088
✅ PostgreSQL数据库连接正常
✅ JWT认证配置完成
✅ CORS已配置
```

#### 1.2 API端点测试
```bash
需要验证的端点：
- GET  /api/instances
- POST /api/auth/login
- GET  /api/tasks
- GET  /api/notes
- GET  /api/email
- GET  /api/vault
```

#### 1.3 认证系统
```bash
✅ 开发登录模式: POCKET_DEV_AUTH=true
✅ JWT Secret配置
✅ 用户表已创建
✅ admin用户已创建
```

---

### 2. 前端构建审计

#### 2.1 环境配置
```bash
检查项：
- VITE_API_BASE配置
- 构建产物完整性
- 资源文件大小
```

#### 2.2 路由配置
```bash
关键路由：
- / → /ai (重定向)
- /login
- /ai (首页)
- /notes, /email, /vault, /meetings
- /settings
```

#### 2.3 依赖检查
```bash
关键依赖：
- Vue 3
- Vue Router
- Pinia (状态管理)
- Capacitor
- vue-i18n
```

---

### 3. Android构建审计

#### 3.1 Capacitor配置
```bash
- appId: com.kaixuan.opencode.pocket
- appName: OpenCode Pocket
- webDir: dist
- 混合内容配置
```

#### 3.2 网络安全配置
```bash
需要检查：
- 192.168.31.35是否在允许列表
- cleartext traffic配置
- AndroidManifest权限
```

#### 3.3 APK信息
```bash
- 大小: 24 MB
- 版本: 1.0
- 构建类型: Debug
```

---

### 4. 关键功能审计

#### 4.1 必须工作的功能
```bash
优先级1 (Critical):
- [ ] 应用启动
- [ ] 登录功能
- [ ] 底部导航切换
- [ ] API网络请求

优先级2 (High):
- [ ] 任务CRUD
- [ ] 笔记CRUD
- [ ] 实例列表查看

优先级3 (Medium):
- [ ] 密码箱功能
- [ ] 邮箱功能
- [ ] 设置页面
```

#### 4.2 已知问题
```bash
1. Mixed Content警告 (HTTPS→HTTP)
   - 影响: 警告但可能不影响功能
   - 解决方案: 部署HTTPS后端或配置例外

2. WebSocket连接失败
   - 影响: 实时通知不可用
   - 解决方案: 升级到WSS或禁用WebSocket

3. 龙虾存储初始化
   - 影响: 可能影响加密功能
   - 需要测试: 登录后是否正确初始化
```

---

### 5. 代码质量审计

#### 5.1 路由守卫逻辑
```typescript
需要检查：
- 未登录访问受保护页面 → /login
- 已登录访问/login → /ai
- 需要龙虾初始化的页面检查
```

#### 5.2 错误处理
```bash
关键点：
- API请求失败处理
- 网络断开处理
- Token过期处理
- 404页面
```

#### 5.3 状态管理
```bash
Store检查：
- auth store (登录状态)
- tasks store
- notes store
- 其他业务store
```

---

## 🚨 发现的潜在问题

### 问题1: Mixed Content警告
**严重程度**: 🟡 Medium  
**描述**: HTTPS页面访问HTTP API会被浏览器警告  
**影响**: 可能导致某些请求被阻止  
**状态**: 已知，已配置allowMixedContent  
**建议**: 
- 短期: 验证功能是否正常
- 长期: 部署HTTPS后端

### 问题2: WebSocket连接失败
**严重程度**: 🟢 Low  
**描述**: WSS连接失败（HTTP/HTTPS混合）  
**影响**: 实时通知不可用，但不影响核心功能  
**状态**: 已知，可接受  
**建议**: 后续升级到WSS

### 问题3: 龙虾存储初始化不确定
**严重程度**: 🟡 Medium  
**描述**: 不确定龙虾本地加密存储是否正确初始化  
**影响**: 可能影响笔记、邮箱、密码箱功能  
**状态**: 需要测试验证  
**建议**: 登录后检查console日志

---

## ✅ 审计前测试

让我先执行一些关键测试：
