> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/新架构v1/03-roadmap/里程碑.md`](docs/新架构v1/03-roadmap/里程碑.md)
> Do NOT use this doc for current implementation decisions.
> This doc is a historical plan/analysis from a completed sprint; 规划以 v3 里程碑为准。

# OpenCode Pocket 本地完整测试方案

**测试日期**: 2026-07-04  
**测试目的**: 在部署到生产环境前，完整验证所有功能

---

## 🎯 测试目标

1. ✅ 应用能够正常启动
2. ✅ 后端API连接正常
3. ⏳ **页面导航流畅无阻塞** ← 当前重点
4. ⏳ **自动升级功能** ← 需要添加
5. ⏳ 核心功能完整可用

---

## 📱 当前状态总结

### ✅ 已完成
- APK构建成功 (24 MB)
- 应用安装到手机
- 后端服务运行 (PostgreSQL + JWT认证)
- CORS配置完成
- 网络安全配置 (192.168.31.35允许HTTP)

### ⚠️ 发现的问题
1. **Mixed Content警告** - HTTPS页面访问HTTP API（功能可能受限）
2. **页面导航可能卡顿** - 需要测试验证
3. **缺少自动升级功能** - 需要实现

---

## 🔍 阶段一：页面导航测试（本地测试）

### 1.1 应用启动和首页

**测试步骤**:
1. 启动应用
2. 观察启动时间
3. 检查首页显示

**验证点**:
- [ ] 启动时间 < 3秒
- [ ] 无白屏或卡顿
- [ ] 底部导航栏显示正常
- [ ] 首页内容加载完成

**当前状态**: ✅ 已验证启动正常

---

### 1.2 登录流程测试

**测试步骤**:
1. 如果不在登录页，导航到登录页
2. 输入 admin/admin
3. 点击登录按钮
4. 观察响应和跳转

**验证点**:
- [ ] 登录按钮可点击（无卡死）
- [ ] 显示Loading状态
- [ ] 登录成功后跳转到主界面
- [ ] Token正确保存

**预期API调用**:
```
POST http://192.168.31.35:8088/api/auth/login
Body: {"username":"admin","password":"admin"}
```

**Chrome DevTools验证**:
1. 打开 `chrome://inspect/#devices`
2. 找到应用WebView，点击inspect
3. 查看Network标签，观察登录请求

---

### 1.3 底部导航测试

**测试步骤**:
1. 点击底部导航的每个Tab
2. 观察页面切换

**验证点**:
- [ ] 所有Tab可点击
- [ ] 页面切换流畅（< 500ms）
- [ ] 无卡顿或冻结
- [ ] 返回按钮工作正常

**导航结构**（需要验证实际实现）:
- Tab 1: 首页/任务列表
- Tab 2: 实例管理
- Tab 3: 个人助理
- Tab 4: 设置

---

### 1.4 任务管理流程测试

**测试步骤**:
1. 进入任务列表页
2. 点击"新建任务"按钮
3. 填写任务信息
4. 保存任务
5. 返回列表查看
6. 点击任务进入详情
7. 编辑任务
8. 删除任务

**验证点**:
- [ ] "新建任务"按钮响应快速
- [ ] 表单输入无延迟
- [ ] 保存后立即显示在列表
- [ ] 详情页加载快速
- [ ] 编辑保存成功
- [ ] 删除后从列表消失

**关键API**:
```
GET /api/tasks
POST /api/tasks
GET /api/tasks/{id}
PUT /api/tasks/{id}
DELETE /api/tasks/{id}
```

---

### 1.5 实例管理测试

**测试步骤**:
1. 进入实例列表页
2. 查看实例状态
3. 点击实例查看详情
4. 查看会话列表

**验证点**:
- [ ] 实例列表加载
- [ ] 状态显示正确（healthy/offline）
- [ ] 点击实例无卡顿
- [ ] 会话列表加载

---

### 1.6 个人助理功能测试

#### 笔记功能
**测试步骤**:
1. 进入笔记模块
2. 创建新笔记
3. 查看笔记列表
4. 编辑笔记
5. 删除笔记

**验证点**:
- [ ] 创建笔记流畅
- [ ] Markdown渲染正常
- [ ] 列表滚动流畅
- [ ] 编辑保存快速

#### 保险箱功能
**测试步骤**:
1. 进入保险箱
2. 添加密码条目
3. 查看密码（显示/隐藏）
4. 复制到剪贴板

**验证点**:
- [ ] 表单输入无延迟
- [ ] 加密保存成功
- [ ] 显示/隐藏切换快速
- [ ] 复制功能正常

---

## 🔄 阶段二：自动升级功能实现

### 2.1 升级功能需求

**功能描述**:
- 应用启动时检查新版本
- 发现新版本时提示用户
- 用户确认后下载APK
- 下载完成后提示安装

**API端点** (已存在):
```
GET /api/app/check-update
Response: {
  "hasUpdate": true,
  "version": "1.1.0",
  "downloadUrl": "https://m.kxpms.cn/downloads/app-release.apk",
  "releaseNotes": "修复了...",
  "forceUpdate": false
}

GET /api/app/download
Response: APK文件流
```

---

### 2.2 升级流程设计

```
[启动应用]
    ↓
[后台检查更新]
    ↓
[有新版本?] → No → [正常使用]
    ↓ Yes
[显示更新对话框]
    ├─ 强制更新 → [必须更新才能继续]
    └─ 可选更新 → [稍后更新 | 立即更新]
        ↓
[开始下载APK]
    ↓
[显示下载进度]
    ↓
[下载完成]
    ↓
[调用系统安装器]
```

---

### 2.3 实现要点

#### 前端实现
```typescript
// src/utils/appUpdater.ts

export interface UpdateInfo {
  hasUpdate: boolean
  version: string
  downloadUrl: string
  releaseNotes: string
  forceUpdate: boolean
}

export class AppUpdater {
  async checkForUpdates(): Promise<UpdateInfo | null> {
    try {
      const response = await fetch('http://192.168.31.35:8088/api/app/check-update')
      const data = await response.json()
      return data
    } catch (error) {
      console.error('检查更新失败:', error)
      return null
    }
  }

  async downloadAndInstall(downloadUrl: string): Promise<void> {
    // 使用 Capacitor Filesystem 下载
    // 下载到临时目录
    // 调用系统安装器
  }
}
```

#### Capacitor集成
需要添加权限：
- `android.permission.REQUEST_INSTALL_PACKAGES`
- `android.permission.WRITE_EXTERNAL_STORAGE`

---

### 2.4 升级功能测试

**测试步骤**:
1. 后端准备新版本APK
2. 配置 check-update 返回有更新
3. 重启应用
4. 观察更新提示
5. 点击"更新"
6. 观察下载进度
7. 安装新版本

**验证点**:
- [ ] 更新检查不阻塞启动
- [ ] 更新提示UI友好
- [ ] 下载进度显示准确
- [ ] 安装流程顺畅

---

## 🧪 阶段三：完整集成测试

### 3.1 端到端测试场景

#### 场景1：新用户首次使用
```
1. 安装应用
2. 启动（检查更新）
3. 进入登录页
4. 登录
5. 查看引导页面（如果有）
6. 创建第一个任务
7. 查看实例列表
8. 创建第一条笔记
```

#### 场景2：日常使用
```
1. 启动应用
2. 自动登录（Token有效）
3. 查看任务列表
4. 创建新任务
5. 查看会话
6. 添加笔记
7. 退出应用
```

#### 场景3：升级流程
```
1. 启动应用
2. 检测到新版本
3. 显示更新提示
4. 下载并安装
5. 重启应用
6. 验证数据未丢失
```

---

### 3.2 性能测试

**测试指标**:
- [ ] 冷启动时间 < 3秒
- [ ] 热启动时间 < 1秒
- [ ] 页面切换 < 500ms
- [ ] API响应 < 2秒
- [ ] 列表滚动 60fps
- [ ] 内存占用 < 200MB
- [ ] 电量消耗合理

---

### 3.3 兼容性测试

**测试设备**:
- [x] vivo V2436A (当前测试设备)
- [ ] 其他品牌手机（小米、华为、OPPO等）
- [ ] 不同Android版本（8.0, 9.0, 10.0, 11.0+）
- [ ] 不同屏幕尺寸

---

## 🐛 问题追踪

### 已知问题

#### 问题 #1: Mixed Content 警告
**状态**: 🟡 已知，待解决  
**影响**: 中等  
**解决方案**: 
- 选项A: 部署HTTPS后端到 m.kxpms.cn
- 选项B: 配置Nginx反向代理
- 选项C: 使用自签名证书 + 信任配置

#### 问题 #2: 页面导航未完整测试
**状态**: 🔴 待测试  
**影响**: 高  
**下一步**: 执行完整导航测试

#### 问题 #3: 缺少自动升级功能
**状态**: 🔴 待实现  
**影响**: 高  
**下一步**: 实现升级检查和下载逻辑

---

## 📝 测试执行计划

### 今天 (2026-07-04)

#### 立即执行
1. ⏳ **手动测试页面导航** (30分钟)
   - 登录流程
   - 所有Tab切换
   - 任务CRUD流程
   - 笔记CRUD流程

2. ⏳ **记录所有卡顿和问题** (同时进行)
   - 使用Chrome DevTools监控
   - 记录网络请求
   - 记录性能指标

#### 今天晚些时候
3. ⏳ **实现自动升级功能** (2小时)
   - 前端升级检查逻辑
   - 下载和安装集成
   - UI提示组件

4. ⏳ **测试升级流程** (30分钟)
   - 模拟有新版本
   - 完整升级流程
   - 验证数据保留

### 明天 (2026-07-05)

5. ⏳ **配置生产环境** (1小时)
   - Nginx配置更新
   - HTTPS证书验证
   - 域名切换

6. ⏳ **生产环境测试** (1小时)
   - 使用 m.kxpms.cn
   - 完整功能验证
   - 性能测试

7. ⏳ **最终交付** (30分钟)
   - 生成测试报告
   - 文档更新
   - APK发布

---

## 🛠️ 测试工具

### Chrome远程调试
```bash
# 1. 在Chrome打开
chrome://inspect/#devices

# 2. 找到应用WebView，点击inspect
# 3. 使用Console、Network、Performance标签
```

### ADB日志
```bash
# 实时查看应用日志
/Users/xutaohuang/Library/Android/sdk/platform-tools/adb logcat | grep -i "capacitor\|opencode"

# 查看性能
/Users/xutaohuang/Library/Android/sdk/platform-tools/adb shell dumpsys meminfo com.kaixuan.opencode.pocket
```

### 后端日志
```bash
# 查看后端日志
tail -f /tmp/pocketd.log
```

---

## ✅ 测试检查清单

在进行生产部署前，确保所有项目都已完成：

### 功能完整性
- [ ] 登录/登出功能正常
- [ ] 所有页面可导航
- [ ] 任务CRUD完整可用
- [ ] 实例管理正常
- [ ] 笔记功能完整
- [ ] 保险箱功能正常
- [ ] 自动升级功能实现并测试

### 性能和稳定性
- [ ] 无明显卡顿
- [ ] 无崩溃或闪退
- [ ] 内存占用合理
- [ ] 网络请求正常
- [ ] 离线处理合理

### 用户体验
- [ ] UI响应快速
- [ ] Loading状态清晰
- [ ] 错误提示友好
- [ ] 操作流程顺畅

### 安全性
- [ ] HTTPS连接（生产环境）
- [ ] Token安全存储
- [ ] 敏感数据加密
- [ ] API认证正常

---

## 📞 下一步行动

### 现在立即做
1. 🔍 **手动测试所有页面导航**
   - 我会通过Chrome DevTools远程查看
   - 您在手机上操作并告诉我遇到的问题

2. 📝 **记录所有卡顿点**
   - 哪个页面卡？
   - 哪个按钮点击没反应？
   - 哪个操作慢？

### 完成测试后
3. 🔧 **修复发现的问题**
4. 🚀 **实现自动升级功能**
5. 🌐 **配置生产环境部署**

---

**准备好了吗？让我们开始完整的功能测试！** 🚀

请告诉我：
1. 您现在看到的是哪个页面？
2. 能否尝试点击不同的Tab？
3. 有哪些操作感觉卡顿或无响应？
