# OpenCode Pocket 移动端测试快速指南

**构建日期**: 2026-07-04 12:49:43  
**APK版本**: Debug v1.0  
**APK大小**: 24 MB (25,106,863 bytes)  
**MD5校验**: f3c662112ca00c93de80fcbad6e57d13

---

## ✅ 已完成的准备工作

### 后端服务
- ✅ 后端已启动并运行在 `http://192.168.31.35:8088`
- ✅ API端点测试通过
- ✅ 健康检查正常

### APK构建
- ✅ 前端已构建（使用最新代码）
- ✅ API配置已设置为局域网地址（`VITE_API_BASE=http://192.168.31.35:8088`）
- ✅ Capacitor同步完成
- ✅ Android APK构建成功

### 构建配置
- **编译SDK**: Android 36
- **目标SDK**: 36
- **最小SDK**: 24
- **JDK**: Oracle JDK 21
- **AndroidX**: 已启用

---

## 📱 三种安装方式

### 方式一：通过ADB安装（推荐 - 最快）

#### 前提条件
1. 手机启用开发者模式：
   - 设置 → 关于手机 → 连续点击"版本号"7次
   
2. 启用USB调试：
   - 设置 → 开发者选项 → USB调试

3. 连接手机到电脑并授权

#### 安装步骤
```bash
# 1. 检查设备连接
adb devices
# 应该显示你的设备，例如：
# List of devices attached
# ABC123456789    device

# 2. 安装APK（-r表示重新安装覆盖）
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend/android
adb install -r app/build/outputs/apk/debug/app-debug.apk

# 3. 验证安装
adb shell pm list packages | grep kaixuan
# 应该显示：package:com.kaixuan.opencode.pocket
```

**预期结果**：
```
Performing Streamed Install
Success
```

---

### 方式二：通过文件传输安装

#### 步骤
1. **将APK复制到手机**
   ```bash
   # 使用ADB推送到手机下载目录
   adb push app/build/outputs/apk/debug/app-debug.apk /sdcard/Download/
   ```
   
   或者通过其他方式（微信、QQ、电子邮件等）发送APK到手机

2. **在手机上安装**
   - 打开文件管理器
   - 进入"下载"目录
   - 点击 `app-debug.apk`
   - 如果提示"未知来源"，需要允许该来源安装应用
   - 点击"安装"

---

### 方式三：通过Android Studio安装

#### 步骤
1. 打开Android Studio
2. File → Open → 选择 `frontend/android` 目录
3. 等待Gradle同步完成
4. 连接手机到电脑
5. 点击工具栏的 "Run" 按钮（绿色三角形）
6. 选择你的设备
7. 应用会自动安装并启动

---

## 🚀 启动和初步测试

### 启动应用

1. **在手机上找到应用图标**
   - 应用名称：OpenCode Pocket
   - 包名：com.kaixuan.opencode.pocket

2. **首次启动检查**
   - [ ] 应用能够正常启动
   - [ ] 无崩溃或闪退
   - [ ] 启动时间 < 5秒

3. **网络连接检查**
   - [ ] 确保手机连接到与电脑相同的WiFi网络（192.168.31.x）
   - [ ] 应用能够连接到后端API

---

## 🧪 核心功能快速测试清单

### 1. 认证测试 (5分钟)

- [ ] **登录界面显示正常**
- [ ] **测试登录（如果启用开发模式）**
  - 用户名：`admin`
  - 密码：`admin`
- [ ] **登录成功后跳转到主界面**
- [ ] **Token持久化（关闭重开应用仍保持登录）**

### 2. 个人助理功能测试 (10分钟)

#### 笔记功能
- [ ] **创建笔记**
  - 点击"新建笔记"
  - 输入标题和内容
  - 保存
- [ ] **查看笔记列表**
- [ ] **编辑笔记**
- [ ] **删除笔记**

#### 保险箱功能
- [ ] **添加密码条目**
- [ ] **查看密码（点击显示/隐藏）**
- [ ] **复制密码到剪贴板**

#### 邮箱功能（如果配置）
- [ ] **查看邮件列表**
- [ ] **打开邮件详情**
- [ ] **刷新邮件**

### 3. OpenCode管理功能测试 (10分钟)

#### 实例管理
- [ ] **查看实例列表**
  - 应该至少显示一个demo实例
- [ ] **检查实例状态（健康/在线）**

#### 任务管理
- [ ] **创建新任务**
  - 标题：测试任务1
  - 描述：这是一个测试任务
  - 状态：待办
- [ ] **查看任务列表**
- [ ] **编辑任务**
- [ ] **更改任务状态**
- [ ] **删除任务**

#### 会话管理
- [ ] **查看会话列表**
- [ ] **搜索会话**

### 4. UI/UX测试 (5分钟)

- [ ] **响应式布局**
  - 竖屏显示正常
  - 横屏显示正常
- [ ] **触摸交互**
  - 按钮点击响应快速
  - 滑动列表流畅
  - 下拉刷新工作正常
- [ ] **导航**
  - 底部导航栏切换页面
  - 返回按钮功能正常
- [ ] **加载状态**
  - Loading指示器显示
  - 错误提示友好

### 5. 性能测试 (5分钟)

- [ ] **应用性能**
  - 页面切换流畅（无卡顿）
  - 列表滚动60fps
  - 内存占用合理
- [ ] **网络性能**
  - API请求响应时间 < 2秒
  - 离线提示清晰

---

## 🐛 问题记录模板

在测试过程中发现问题，请使用以下格式记录：

```markdown
### 问题 #1: [问题简述]

**优先级**: 🔴 Critical / 🟡 High / 🟢 Medium / ⚪ Low

**分类**: [UI/功能/性能/安全/其他]

**复现步骤**:
1. 
2. 
3. 

**期望行为**:


**实际行为**:


**截图**: [如果有]

**设备信息**:
- 设备型号: 
- Android版本: 
- 应用版本: 1.0

**日志** (可选):
```bash
# 获取应用日志
adb logcat | grep -i "opencode\|kaixuan"
```
```

---

## 📊 测试结果记录

完成测试后，填写以下表格：

| 测试类别 | 通过 | 失败 | 阻塞 | 备注 |
|---------|------|------|------|------|
| 认证功能 | ☐ | ☐ | ☐ | |
| 笔记功能 | ☐ | ☐ | ☐ | |
| 保险箱功能 | ☐ | ☐ | ☐ | |
| 邮箱功能 | ☐ | ☐ | ☐ | |
| 实例管理 | ☐ | ☐ | ☐ | |
| 任务管理 | ☐ | ☐ | ☐ | |
| 会话管理 | ☐ | ☐ | ☐ | |
| UI/UX | ☐ | ☐ | ☐ | |
| 性能 | ☐ | ☐ | ☐ | |

**总体评估**: ☐ 可以发布 / ☐ 需要修复 / ☐ 重大问题

---

## 🛠️ 调试工具

### Chrome远程调试（查看Console和Network）

1. **在电脑Chrome浏览器中打开**
   ```
   chrome://inspect/#devices
   ```

2. **确保手机已通过USB连接**

3. **找到 "OpenCode Pocket" WebView**

4. **点击 "inspect" 打开开发者工具**
   - 可以查看Console日志
   - 查看Network请求
   - 调试JavaScript代码

### ADB日志查看

```bash
# 实时查看应用日志
adb logcat | grep -i "opencode\|capacitor\|webkit"

# 清除日志
adb logcat -c

# 保存日志到文件
adb logcat > /tmp/opencode-pocket-logs.txt

# 查看崩溃日志
adb logcat | grep -i "fatal\|crash"
```

### 应用信息查看

```bash
# 查看应用包信息
adb shell dumpsys package com.kaixuan.opencode.pocket | head -50

# 查看应用存储使用
adb shell du -sh /data/data/com.kaixuan.opencode.pocket

# 查看应用权限
adb shell dumpsys package com.kaixuan.opencode.pocket | grep permission
```

---

## 🔄 重新构建和更新

如果发现问题需要修复代码后重新测试：

```bash
# 1. 修改代码...

# 2. 重新构建前端
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npm run build

# 3. 同步到Capacitor
npx cap sync android

# 4. 构建新APK
cd android
./gradlew assembleDebug

# 5. 重新安装到设备
adb install -r app/build/outputs/apk/debug/app-debug.apk

# 6. 重启应用
adb shell am force-stop com.kaixuan.opencode.pocket
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

---

## 📝 重要提示

### 网络配置
- ⚠️ **确保手机和电脑在同一WiFi网络**
- ⚠️ **后端服务必须可以从 `192.168.31.35:8088` 访问**
- ⚠️ **如果IP地址变化，需要重新构建APK**

### 开发模式登录
如果后端启用了 `POCKET_DEV_AUTH=true`，可以使用：
- 用户名：`admin`
- 密码：`admin`

⚠️ **生产环境必须禁用此功能！**

### 数据持久化
- 应用数据存储在设备本地SQLite数据库
- 卸载应用会清除所有本地数据
- 重新安装保留数据（使用 `adb install -r`）

### 常见问题

**问题1：应用安装失败 "INSTALL_FAILED_UPDATE_INCOMPATIBLE"**
```bash
# 解决：完全卸载旧版本
adb uninstall com.kaixuan.opencode.pocket
adb install app/build/outputs/apk/debug/app-debug.apk
```

**问题2：无法连接到后端API**
- 检查手机WiFi连接
- 检查后端服务是否运行
- 测试网络连通性：
  ```bash
  # 在手机浏览器访问
  http://192.168.31.35:8088/api/instances
  ```

**问题3：应用启动后立即崩溃**
```bash
# 查看崩溃日志
adb logcat *:E | grep -i fatal
```

---

## 📞 支持资源

- **完整测试计划**: [MOBILE_TEST_VERIFICATION_PLAN.md](./MOBILE_TEST_VERIFICATION_PLAN.md)
- **项目文档**: [README.md](./README.md)
- **API文档**: [docs/](./docs/)

---

## ✅ 下一步行动

1. ☐ 按照上述步骤安装APK到测试设备
2. ☐ 执行核心功能快速测试清单
3. ☐ 记录发现的所有问题
4. ☐ 根据优先级修复问题
5. ☐ 重新构建并验证修复
6. ☐ 完成完整测试报告

**预计测试时间**: 30-45分钟（首轮快速测试）

---

**准备就绪！现在可以开始在真实设备上测试应用了！** 🚀
