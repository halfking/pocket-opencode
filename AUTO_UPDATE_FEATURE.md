# 📱 OpenCode Pocket 自动更新功能文档

**版本:** v1.2.0 Build 2  
**完成时间:** 2026-06-29 11:38  
**状态:** ✅ 已实现并部署

---

## 🎉 功能概述

OpenCode Pocket 现已支持**自动检查更新和一键升级**功能，用户可以随时获取最新版本。

### 核心特性
- ✅ 启动时自动检查更新
- ✅ 手动检查更新按钮
- ✅ 版本信息对比展示
- ✅ 更新日志显示
- ✅ 一键下载安装
- ✅ 强制更新支持
- ✅ 版本号和构建号标识

---

## 📊 版本信息显示

### 应用内版本标识
```
版本号: v1.2.0
构建号: Build 2
构建日期: 2026-06-29
应用名称: OpenCode Pocket Mobile
```

### 查看位置
**设置页 → 应用信息**
- 显示完整的版本信息
- 显示构建日期
- 显示 API 地址

---

## 🔄 自动更新机制

### 1. 启动时检查
**触发时机:** 应用启动后自动执行

**流程:**
```
1. App 启动
2. 加载 UpdateChecker 组件
3. 自动调用检查更新 API
4. 如果有新版本，弹出更新对话框
5. 用户选择立即更新或稍后提醒
```

**实现位置:**
- `App.vue` - 集成 UpdateChecker 组件
- `UpdateChecker.vue` - onMounted 时自动检查

### 2. 手动检查
**触发位置:** 设置页 → "检查更新"按钮

**流程:**
```
1. 用户点击"检查更新"
2. 调用 checkUpdate() API
3. 显示结果提示
   - 有更新: 显示版本信息
   - 无更新: "当前已是最新版本"
```

---

## 🎨 更新对话框

### UI 元素
```
┌─────────────────────────┐
│          🎉            │  图标
│      发现新版本         │  标题
│                        │
│  当前版本: v1.1.0 (B1) │  版本对比
│  最新版本: v1.2.0 (B2) │
│  更新大小: 4.2 MB      │
│  发布日期: 2026-06-29  │
│                        │
│  更新内容:              │  更新日志
│  ✨ 全新移动端 UI      │
│  ✨ 添加登录系统       │
│  ...                   │
│                        │
│ [稍后提醒] [立即更新]   │  操作按钮
└─────────────────────────┘
```

### 交互特性
- **优雅动画**: 淡入 + 上滑动画
- **触摸反馈**: 按钮点击缩放效果
- **强制更新**: 不可关闭对话框
- **下载状态**: 按钮显示"下载中..."

---

## 🔧 技术实现

### Backend API

#### 1. 检查更新端点
```
POST /api/app/check-update
```

**请求体:**
```json
{
  "currentVersion": "1.1.0",
  "currentBuild": 1,
  "platform": "android",
  "deviceModel": "vivo V2436A"
}
```

**响应:**
```json
{
  "hasUpdate": true,
  "forceUpdate": false,
  "message": "发现新版本",
  "latest": {
    "version": "1.2.0",
    "buildNumber": 2,
    "downloadUrl": "http://14.103.169.56:8088/api/app/download",
    "fileSize": 4200000,
    "changelog": [
      "✨ 全新移动端 UI 设计",
      "✨ 添加登录系统",
      "..."
    ],
    "forceUpdate": false,
    "releaseDate": "2026-06-29"
  }
}
```

#### 2. 下载 APK 端点
```
GET /api/app/download
```

**响应:**
- Content-Type: application/vnd.android.package-archive
- Content-Disposition: attachment; filename=opencode-pocket.apk
- 返回 APK 文件流

**文件位置:**
```
/data/www/pocket.kxpms.cn/downloads/opencode-pocket-latest.apk
```

### Frontend 实现

#### 版本配置
**文件:** `src/utils/version.ts`
```typescript
export const APP_VERSION = {
  version: '1.2.0',
  buildNumber: 2,
  buildDate: '2026-06-29',
  name: 'OpenCode Pocket Mobile'
}
```

#### 检查更新函数
```typescript
export async function checkUpdate(): Promise<CheckUpdateResponse> {
  const response = await fetch(`${API_BASE}/api/app/check-update`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      currentVersion: APP_VERSION.version,
      currentBuild: APP_VERSION.buildNumber,
      platform: 'android',
      deviceModel: navigator.userAgent
    })
  })
  return response.json()
}
```

#### 更新组件
**文件:** `src/components/UpdateChecker.vue`
- 启动时自动检查
- 显示更新对话框
- 处理下载安装
- 支持强制更新模式

---

## 📝 使用指南

### 用户操作流程

#### 场景 1: 自动更新提示
```
1. 打开应用
2. 自动检查更新（后台进行）
3. 如果有新版本，弹出对话框
4. 查看更新内容
5. 点击"立即更新"
6. 浏览器打开下载链接
7. 下载完成后安装 APK
8. 安装完成后打开应用
```

#### 场景 2: 手动检查更新
```
1. 打开应用
2. 点击底部导航"设置"
3. 点击"检查更新"按钮
4. 查看检查结果
   - 有更新: 显示版本信息
   - 无更新: 提示"当前已是最新版本"
```

---

## 🔐 版本管理

### 发布新版本流程

#### 1. 更新版本信息
```typescript
// src/utils/version.ts
export const APP_VERSION = {
  version: '1.3.0',      // 更新版本号
  buildNumber: 3,        // 递增构建号
  buildDate: '2026-07-01', // 更新日期
  name: 'OpenCode Pocket Mobile'
}
```

#### 2. 更新 Backend 版本信息
```go
// internal/server/server.go - handleCheckUpdate
latestVersion := &VersionInfo{
    Version:     "1.3.0",
    BuildNumber: 3,
    Changelog: []string{
        "✨ 新功能说明",
        "🐛 Bug 修复",
    },
    // ...
}
```

#### 3. 构建新 APK
```bash
npm run build
npx cap sync android
cd android && ./gradlew assembleRelease
```

#### 4. 部署 APK
```bash
# 上传到服务器
scp app-release.apk root@14.103.169.56:/data/www/pocket.kxpms.cn/downloads/opencode-pocket-latest.apk
```

#### 5. 测试
- 用旧版本安装应用
- 打开应用触发自动检查
- 验证更新提示
- 测试下载安装

---

## 🎯 版本号规范

### 语义化版本
```
v主版本.次版本.修订版本

v1.2.0
 │ │ │
 │ │ └─ 修订版本（Bug 修复）
 │ └─── 次版本（新功能）
 └───── 主版本（重大更新）
```

### 构建号
- 每次构建递增
- 用于精确标识版本
- 格式: Build 1, Build 2, Build 3...

### 示例
```
v1.0.0 Build 1 - 初始版本
v1.1.0 Build 2 - 添加移动端 UI
v1.2.0 Build 3 - 添加自动更新
v1.2.1 Build 4 - Bug 修复
v2.0.0 Build 5 - 重大架构升级
```

---

## ⚠️ 强制更新

### 启用强制更新
```go
// Backend
latestVersion := &VersionInfo{
    ForceUpdate: true,  // 设置为 true
    // ...
}
```

### 强制更新行为
- ❌ 不显示"稍后提醒"按钮
- ❌ 不能关闭对话框
- ⚠️ 显示警告提示
- ✅ 必须更新才能继续使用

### 适用场景
- 严重安全漏洞
- API 不兼容的版本
- 必须的功能修复

---

## 📊 更新日志编写规范

### 分类标识
```
✨ 新功能 (Features)
🐛 Bug 修复 (Bug Fixes)
🎨 UI 改进 (UI Improvements)
⚡️ 性能优化 (Performance)
🔒 安全更新 (Security)
📝 文档更新 (Documentation)
```

### 示例
```json
"changelog": [
  "✨ 添加自动更新检查功能",
  "✨ 支持手动检查更新",
  "🎨 优化设置页面布局",
  "🐛 修复任务列表滚动卡顿",
  "⚡️ 提升应用启动速度",
  "🔒 修复安全漏洞"
]
```

---

## 🧪 测试清单

### 功能测试
- [ ] 启动时自动检查更新
- [ ] 手动检查更新按钮
- [ ] 更新对话框显示
- [ ] 版本信息对比
- [ ] 更新日志显示
- [ ] 下载按钮功能
- [ ] "稍后提醒"功能
- [ ] 强制更新模式

### 版本对比测试
- [ ] 旧版本 → 检测到新版本
- [ ] 最新版本 → 提示已是最新
- [ ] 构建号对比正确

### UI/UX 测试
- [ ] 对话框动画流畅
- [ ] 按钮触摸反馈
- [ ] 文字清晰易读
- [ ] 信息完整准确

---

## 🚀 未来增强

### 计划功能
1. **后台下载**
   - 自动下载 APK
   - 显示下载进度
   - 下载完成后提示安装

2. **增量更新**
   - 只下载差异部分
   - 减少流量消耗
   - 更快的更新速度

3. **版本回滚**
   - 支持降级到旧版本
   - 版本历史记录

4. **更新统计**
   - 更新成功率
   - 版本分布统计
   - 用户反馈收集

---

## ✅ 总结

OpenCode Pocket 的自动更新功能已完整实现：

### 已实现
- ✅ 版本号和构建号标识
- ✅ 启动时自动检查
- ✅ 手动检查按钮
- ✅ 更新对话框 UI
- ✅ 版本信息对比
- ✅ 更新日志展示
- ✅ 一键下载安装
- ✅ Backend API 支持
- ✅ 强制更新模式

### 优势
- 📱 用户体验优秀
- 🔄 更新流程简单
- 📊 信息展示完整
- 🎨 UI 美观专业
- 🔧 技术实现完善

**让用户始终保持最新版本！** 🚀
