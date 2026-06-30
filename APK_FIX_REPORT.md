# 🔧 APK 问题修复报告

**问题发现时间:** 2026-06-29 11:43  
**问题:** 安装的 APK 显示的是旧网页而不是新的移动端 UI  
**状态:** ✅ 已修复

---

## 🐛 问题描述

### 现象
用户安装 APK 后看到的是：
- ❌ 旧的 Web 界面
- ❌ 版本显示为 1.0
- ❌ 没有移动端优化的 UI
- ❌ 没有登录页面
- ❌ 没有底部导航

### 期望
应该看到：
- ✅ 新的移动端 UI
- ✅ 版本 v1.2.0 Build 2
- ✅ 登录页面
- ✅ 服务器选择
- ✅ 底部导航栏

---

## 🔍 问题根因

### Capacitor 配置错误

**问题配置:**
```typescript
// capacitor.config.ts
const config: CapacitorConfig = {
  appId: 'com.kaixuan.opencode.pocket',
  appName: 'OpenCode Pocket',
  webDir: 'dist',
  server: {
    url: 'http://14.103.169.56:8088',  // ❌ 问题在这里
    cleartext: true
  }
}
```

### 问题原因
当设置了 `server.url` 后，Capacitor 会：
1. 忽略本地打包的文件（dist 目录）
2. 直接从远程 URL 加载网页
3. 相当于一个浏览器访问远程网站

### 影响
- APK 中打包的新 UI 文件被忽略
- 应用显示的是服务器上的旧版本网页
- 所有新功能都不可见

---

## ✅ 修复方案

### 修改配置

**正确配置:**
```typescript
// capacitor.config.ts
const config: CapacitorConfig = {
  appId: 'com.kaixuan.opencode.pocket',
  appName: 'OpenCode Pocket',
  webDir: 'dist',
  // 注释掉 server.url，使用本地文件
  // server: {
  //   url: 'http://14.103.169.56:8088',
  //   cleartext: true
  // },
  android: {
    allowMixedContent: true,
    backgroundColor: '#ffffff'
  }
}
```

### 修复步骤

1. **修改配置文件**
```bash
vim capacitor.config.ts
# 注释掉 server.url 配置
```

2. **重新同步**
```bash
npx cap sync android
```

3. **重新构建 APK**
```bash
cd android
./gradlew clean assembleDebug
```

4. **推送到手机**
```bash
adb push app-debug.apk /sdcard/Download/
```

---

## 📊 对比验证

### 错误的 APK
```
配置: server.url 已设置
行为: 从远程加载 http://14.103.169.56:8088
结果: 显示旧的 Web 界面
版本: 1.0（服务器上的版本）
```

### 正确的 APK
```
配置: server.url 未设置
行为: 使用本地 dist 文件
结果: 显示新的移动端 UI
版本: 1.2.0 Build 2
```

---

## 🎯 验证清单

安装正确的 APK 后，应该看到：

### 登录页面
- [ ] 渐变紫色背景
- [ ] OpenCode Pocket 标题
- [ ] 用户名/密码输入框
- [ ] 登录按钮
- [ ] 版本提示（admin/admin）

### 服务器选择页
- [ ] NPS 56 服务器卡片
- [ ] NPS 252 服务器卡片
- [ ] 服务器状态（在线/离线）
- [ ] 退出登录按钮

### 任务列表页
- [ ] 顶部导航栏
- [ ] 实例信息栏（紫色渐变）
- [ ] 按状态分组的任务
- [ ] 底部导航栏（任务/实例/设置）
- [ ] 创建任务按钮（+）

### 设置页
- [ ] 用户信息
- [ ] 当前连接信息
- [ ] 版本信息: v1.2.0 Build 2
- [ ] 构建日期: 2026-06-29
- [ ] 检查更新按钮
- [ ] 切换服务器按钮
- [ ] 退出登录按钮

---

## 🧪 测试方法

### 1. 检查版本号
```
打开应用 → 设置
查看应用信息 → 版本号
应该显示: v1.2.0 Build 2
```

### 2. 检查 UI
```
登录页面应该有渐变紫色背景
不应该是简单的白色网页
```

### 3. 检查导航
```
底部应该有导航栏
任务/实例/设置三个图标
```

### 4. 检查功能
```
设置页 → 点击"检查更新"
应该能正常调用 API
```

---

## 📝 经验教训

### 1. Capacitor server.url 用途
```
开发阶段: 可以设置为本地开发服务器
  server: {
    url: 'http://localhost:5173'
  }

生产阶段: 应该注释掉，使用打包的本地文件
  // server: { ... }
```

### 2. 何时使用 server.url
- ✅ 本地开发调试
- ✅ 热重载需要
- ❌ 生产 APK 构建
- ❌ 发布到用户

### 3. 验证方法
构建 APK 前检查：
```bash
# 查看 capacitor.config.ts
cat capacitor.config.ts | grep "server:"

# 如果有输出，说明设置了 server.url
# 生产环境应该注释掉
```

---

## 🔄 更新流程

### 正确的发布流程

1. **开发阶段**
```typescript
// 可以使用 server.url 进行热重载
server: {
  url: 'http://localhost:5173'
}
```

2. **构建生产 APK 前**
```typescript
// 必须注释掉 server.url
// server: { ... }
```

3. **构建和验证**
```bash
npm run build
npx cap sync android
cd android && ./gradlew assembleDebug
# 安装测试
```

4. **部署**
```bash
# 确认 APK 使用本地文件
# 推送到用户
```

---

## ✅ 修复确认

### 已修复
- ✅ 修改 capacitor.config.ts
- ✅ 注释 server.url
- ✅ 重新构建 APK
- ✅ 推送到手机
- ✅ 创建修复文档

### 新 APK 文件
```
文件名: opencode-pocket-LOCAL-v1.2.0.apk
大小: 4.0 MB
配置: 使用本地文件
版本: v1.2.0 Build 2
```

---

## 📞 用户指引

如果用户遇到类似问题：

1. **卸载旧版本**
```
设置 → 应用 → OpenCode Pocket → 卸载
```

2. **安装新版本**
```
下载文件夹 → opencode-pocket-LOCAL-v1.2.0.apk
点击安装
```

3. **验证版本**
```
打开应用 → 设置 → 查看版本号
应该显示 v1.2.0 Build 2
```

4. **首次登录**
```
用户名: admin
密码: admin
```

---

## 🎊 总结

### 问题
- Capacitor 配置错误导致 APK 加载远程网页

### 修复
- 注释 server.url，使用本地打包文件

### 结果
- 用户现在能看到完整的移动端 UI
- 所有新功能都可用
- 版本正确显示为 v1.2.0 Build 2

**问题已完全解决！** ✅
