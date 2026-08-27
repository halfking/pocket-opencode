> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/新架构v1/03-roadmap/里程碑.md`](docs/新架构v1/03-roadmap/里程碑.md)
> Do NOT use this doc for current implementation decisions.
> This doc is a historical plan/analysis from a completed sprint; 规划以 v3 里程碑为准。

# OpenCode Pocket 移动端测试验证计划

**创建日期**: 2026-07-04  
**当前分支**: feat/mobile-ui-components  
**目标**: 将应用安装到真实Android设备上进行全面功能测试

---

## 📋 任务概览

本计划分为以下几个阶段：
1. **构建准备** - 确保所有依赖和配置就绪
2. **APK构建** - 生成可安装的Android应用包
3. **设备安装** - 将应用部署到测试设备
4. **功能测试** - 系统化测试各项功能
5. **问题修复** - 记录并修复发现的问题
6. **发布准备** - 准备生产版本构建

---

## 🔧 阶段一：构建准备

### 1.1 环境检查

- [ ] **验证Node.js版本**
  ```bash
  node --version  # 需要 18+
  npm --version
  ```

- [ ] **验证Java/Android SDK**
  ```bash
  java -version  # 需要 JDK 11+
  echo $ANDROID_HOME  # Android SDK路径
  ```

- [ ] **验证Gradle**
  ```bash
  cd frontend/android
  ./gradlew --version
  ```

### 1.2 后端配置验证

- [ ] **检查后端环境变量配置**
  - 复制 `.env.example` 到 `.env` （如果还没有）
  - 配置必要的环境变量：
    - `POCKET_HTTP_PORT` (默认: 8088)
    - `POCKET_JWT_SECRET` (生产环境必须设置)
    - `POCKET_POSTGRES_DSN` (数据库连接)
    - `POCKET_KXMEMORY_BASE_URL` (AI后端)
    - `POCKET_MCP_BASE_URL` (MCP服务)

- [ ] **本地启动后端服务**
  ```bash
  cd backend
  go run cmd/pocketd/main.go
  ```
  - 验证服务监听在 `http://localhost:8088`
  - 检查日志输出无错误

### 1.3 前端配置验证

- [ ] **检查Capacitor配置**
  - 文件: `frontend/capacitor.config.ts`
  - 确认 `appId`: `com.kaixuan.opencode.pocket`
  - 确认 `appName`: `OpenCode Pocket`
  - 确认 `webDir`: `dist`
  - 确认 `server.url` 已注释（使用本地打包）

- [ ] **检查API配置**
  - 文件: `frontend/src/api/client.ts`
  - 确认API base URL配置正确
  - 移动端应该使用实际服务器地址（不能用localhost）

- [ ] **安装前端依赖**
  ```bash
  cd frontend
  npm install
  ```

---

## 🏗️ 阶段二：APK构建

### 2.1 构建前端资源

- [ ] **清理旧构建**
  ```bash
  cd frontend
  rm -rf dist
  ```

- [ ] **类型检查（可选）**
  ```bash
  npm run typecheck
  ```

- [ ] **构建生产版本**
  ```bash
  npm run build
  ```
  - 检查 `dist/` 目录生成成功
  - 验证关键文件存在：`index.html`, `assets/`

### 2.2 同步到Capacitor

- [ ] **同步Web资源到Android**
  ```bash
  npx cap sync android
  ```
  - 这会将 `dist/` 内容复制到 `android/app/src/main/assets/public/`
  - 检查同步输出无错误

### 2.3 构建Debug APK

- [ ] **构建Debug版本**
  ```bash
  cd android
  ./gradlew assembleDebug
  ```
  
- [ ] **验证APK生成**
  ```bash
  ls -lh app/build/outputs/apk/debug/app-debug.apk
  ```
  - APK位置: `frontend/android/app/build/outputs/apk/debug/app-debug.apk`
  - 记录APK大小和构建时间

### 2.4 构建Release APK（可选）

- [ ] **生成签名密钥（首次）**
  ```bash
  keytool -genkey -v -keystore opencode-pocket-release.keystore \
    -alias opencode-pocket -keyalg RSA -keysize 2048 -validity 10000
  ```
  - 保存密钥文件到安全位置
  - 记录密码到密码管理器

- [ ] **配置签名**
  - 创建 `android/keystore.properties`
  ```properties
  storeFile=/path/to/opencode-pocket-release.keystore
  storePassword=<YOUR_STORE_PASSWORD>
  keyAlias=opencode-pocket
  keyPassword=<YOUR_KEY_PASSWORD>
  ```
  - 添加到 `.gitignore`

- [ ] **构建Release版本**
  ```bash
  ./gradlew assembleRelease
  ```
  - APK位置: `app/build/outputs/apk/release/app-release.apk`

---

## 📱 阶段三：设备安装

### 3.1 设备准备

- [ ] **启用开发者模式**
  - 设置 → 关于手机 → 连续点击"版本号"7次

- [ ] **启用USB调试**
  - 设置 → 开发者选项 → USB调试

- [ ] **连接设备到电脑**
  ```bash
  adb devices
  ```
  - 确认设备显示并已授权

### 3.2 安装应用

**方式一：通过ADB安装**
```bash
cd frontend/android
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

**方式二：直接传输APK**
- 将APK文件复制到手机
- 在手机上点击安装（需要允许"未知来源"）

**方式三：通过Android Studio**
- 打开 `frontend/android` 项目
- 点击 Run 按钮

- [ ] **验证安装成功**
  - 在应用列表中找到"OpenCode Pocket"
  - 记录应用图标显示正常
  - 记录应用大小

---

## 🧪 阶段四：功能测试

### 4.1 启动和初始化测试

- [ ] **首次启动**
  - [ ] 应用能够正常启动
  - [ ] 无崩溃或闪退
  - [ ] 启动时间 < 3秒
  - [ ] Splash screen显示正常（如果有）

- [ ] **网络连接测试**
  - [ ] 检查是否能连接到后端API
  - [ ] 查看网络请求日志（Chrome DevTools Remote Debugging）
  - [ ] 测试离线提示是否正常

### 4.2 认证功能测试

- [ ] **登录流程**
  - [ ] 显示登录界面
  - [ ] 输入用户名/密码
  - [ ] 测试正确凭证登录
  - [ ] 测试错误凭证提示
  - [ ] JWT Token正确保存

- [ ] **开发模式登录（如果启用）**
  - [ ] admin/admin能够登录
  - [ ] 登录后跳转到主界面

- [ ] **Session管理**
  - [ ] 刷新应用后保持登录状态
  - [ ] Token过期后提示重新登录
  - [ ] 退出登录功能正常

### 4.3 个人助理功能测试（Phase 0核心功能）

#### 4.3.1 笔记功能
- [ ] **创建笔记**
  - [ ] 新建笔记界面正常显示
  - [ ] 输入标题和内容
  - [ ] 保存成功并显示在列表
  - [ ] 支持Markdown格式

- [ ] **查看笔记**
  - [ ] 列表显示所有笔记
  - [ ] 点击进入详情页
  - [ ] Markdown渲染正确
  - [ ] 图片/链接显示正常（如果有）

- [ ] **编辑笔记**
  - [ ] 修改内容保存成功
  - [ ] 实时预览工作正常

- [ ] **删除笔记**
  - [ ] 删除确认对话框
  - [ ] 删除后从列表消失

- [ ] **笔记加密**
  - [ ] 敏感笔记加密存储
  - [ ] 解密后正确显示

#### 4.3.2 保险箱功能
- [ ] **添加密码/凭证**
  - [ ] 表单输入正常
  - [ ] 保存后加密存储
  - [ ] 列表显示（密码隐藏）

- [ ] **查看凭证**
  - [ ] 点击显示详情
  - [ ] 密码显示/隐藏切换
  - [ ] 复制到剪贴板功能

- [ ] **编辑和删除凭证**
  - [ ] 修改保存成功
  - [ ] 删除后不可恢复

#### 4.3.3 邮箱集成
- [ ] **OAuth登录**
  - [ ] Google OAuth流程（如果配置）
  - [ ] Microsoft OAuth流程（如果配置）
  - [ ] 授权回调正常

- [ ] **邮件列表**
  - [ ] 显示收件箱邮件
  - [ ] 邮件预览正常
  - [ ] 刷新功能

- [ ] **邮件详情**
  - [ ] 打开邮件查看
  - [ ] HTML邮件渲染
  - [ ] 附件显示（如果有）

- [ ] **IMAP同步**
  - [ ] 后台获取新邮件
  - [ ] 同步状态提示

### 4.4 OpenCode管理功能测试

#### 4.4.1 实例管理
- [ ] **实例列表**
  - [ ] 显示已注册的OpenCode实例
  - [ ] 实例状态显示（在线/离线）
  - [ ] 实例健康检查

- [ ] **添加实例**
  - [ ] 输入实例信息（名称、URL）
  - [ ] 连接测试
  - [ ] 保存成功

- [ ] **编辑/删除实例**
  - [ ] 修改实例信息
  - [ ] 删除实例

#### 4.4.2 会话管理
- [ ] **会话列表**
  - [ ] 显示所有实例的会话
  - [ ] 按实例分组显示
  - [ ] 会话搜索功能

- [ ] **会话详情**
  - [ ] 查看会话摘要
  - [ ] 跳转到OpenCode查看完整会话（如果支持）

#### 4.4.3 任务管理
- [ ] **任务CRUD**
  - [ ] 创建新任务
  - [ ] 任务列表显示
  - [ ] 编辑任务
  - [ ] 删除任务
  - [ ] 任务状态管理（待办/进行中/完成）

- [ ] **任务-会话关联**
  - [ ] 将会话附加到任务
  - [ ] 查看任务关联的所有会话
  - [ ] 解除关联

#### 4.4.4 模型配置
- [ ] **远程配置界面**
  - [ ] 显示当前模型配置
  - [ ] 修改模型提供商
  - [ ] 修改模型参数
  - [ ] 推送配置到OpenCode实例

### 4.5 UI/UX测试（Mobile UI Components）

- [ ] **响应式布局**
  - [ ] 竖屏显示正常
  - [ ] 横屏显示正常
  - [ ] 不同屏幕尺寸适配

- [ ] **触摸交互**
  - [ ] 按钮点击区域足够大
  - [ ] 滑动手势流畅
  - [ ] 下拉刷新功能
  - [ ] 长按菜单（如果有）

- [ ] **导航**
  - [ ] 底部导航栏
  - [ ] 页面切换动画
  - [ ] 返回按钮
  - [ ] 面包屑导航（如果有）

- [ ] **表单输入**
  - [ ] 键盘弹出不遮挡输入框
  - [ ] 输入验证提示
  - [ ] 自动完成（如果有）

- [ ] **加载状态**
  - [ ] Loading指示器显示
  - [ ] 骨架屏（如果有）
  - [ ] 错误提示友好

- [ ] **国际化（i18n）**
  - [ ] 中文界面正常
  - [ ] 英文切换（如果支持）
  - [ ] 语言切换功能

### 4.6 性能测试

- [ ] **应用性能**
  - [ ] 页面加载时间 < 2秒
  - [ ] 滚动列表流畅（60fps）
  - [ ] 内存占用合理（< 200MB）
  - [ ] CPU占用正常

- [ ] **数据库性能**
  - [ ] SQLite查询响应快速
  - [ ] 大量数据时列表加载
  - [ ] 数据库迁移正常

- [ ] **网络性能**
  - [ ] API请求响应时间
  - [ ] 并发请求处理
  - [ ] 超时处理

### 4.7 安全测试

- [ ] **数据加密**
  - [ ] 笔记加密存储验证
  - [ ] 保险箱数据加密
  - [ ] 邮箱凭证加密

- [ ] **JWT安全**
  - [ ] Token在本地安全存储
  - [ ] Token过期处理
  - [ ] 刷新Token机制

- [ ] **网络安全**
  - [ ] HTTPS连接
  - [ ] 证书验证
  - [ ] API认证头正确

### 4.8 边界情况测试

- [ ] **网络异常**
  - [ ] 断网提示
  - [ ] 网络恢复自动重连
  - [ ] 请求重试机制

- [ ] **数据异常**
  - [ ] 空列表状态
  - [ ] 大数据量处理
  - [ ] 特殊字符输入

- [ ] **权限异常**
  - [ ] 未授权访问提示
  - [ ] 存储权限（如果需要）
  - [ ] 网络权限

---

## 🐛 阶段五：问题修复流程

### 5.1 问题记录模板

对于发现的每个问题，使用以下模板记录：

```markdown
### 问题 #N: [简短描述]

**优先级**: 🔴 Critical / 🟡 High / 🟢 Medium / ⚪ Low

**分类**: [UI/功能/性能/安全/其他]

**复现步骤**:
1. 
2. 
3. 

**期望行为**:


**实际行为**:


**环境信息**:
- 设备: 
- Android版本: 
- 应用版本: 
- 后端版本: 

**截图/日志**:


**修复方案**:


**验证结果**:

```

### 5.2 问题优先级定义

- **🔴 Critical**: 应用崩溃、数据丢失、安全漏洞
- **🟡 High**: 核心功能无法使用、严重UI问题
- **🟢 Medium**: 功能不完整、非严重UI问题
- **⚪ Low**: 优化建议、次要功能问题

### 5.3 修复流程

1. **记录问题** → 使用上述模板详细记录
2. **分类和优先级** → 标记严重程度
3. **分配修复** → 确定负责人
4. **代码修改** → 在代码中修复
5. **本地验证** → 开发环境测试
6. **重新构建** → 生成新APK
7. **设备复测** → 在真实设备上验证修复
8. **更新状态** → 标记为已修复并记录验证结果

---

## 🚀 阶段六：发布准备

### 6.1 Pre-release检查清单

- [ ] **所有Critical和High问题已修复**
- [ ] **所有核心功能测试通过**
- [ ] **性能指标达标**
- [ ] **安全审计通过**

### 6.2 版本管理

- [ ] **更新版本号**
  - 文件: `frontend/android/app/build.gradle`
  - `versionCode` 递增
  - `versionName` 更新（如 1.0.0）

- [ ] **更新Changelog**
  - 创建 `CHANGELOG.md`
  - 记录新功能和修复的问题

### 6.3 Release构建

- [ ] **清理构建**
  ```bash
  cd frontend/android
  ./gradlew clean
  ```

- [ ] **构建Release APK**
  ```bash
  ./gradlew assembleRelease
  ```

- [ ] **验证签名**
  ```bash
  jarsigner -verify -verbose -certs app/build/outputs/apk/release/app-release.apk
  ```

### 6.4 发布渠道（可选）

- [ ] **内部测试发布**
  - 上传到内部文件服务器
  - 生成下载链接
  - 通知测试人员

- [ ] **Google Play Store（未来）**
  - 创建开发者账号
  - 准备应用商店资料
  - 上传APK/AAB

- [ ] **第三方应用市场（中国）**
  - 华为应用市场
  - 小米应用商店
  - 应用宝等

---

## 📊 测试报告模板

测试完成后，生成测试报告：

```markdown
# OpenCode Pocket 移动端测试报告

**测试日期**: YYYY-MM-DD
**测试人员**: 
**应用版本**: 
**设备信息**: 

## 测试摘要
- 测试用例总数: 
- 通过: 
- 失败: 
- 阻塞: 

## 关键发现
### ✅ 工作正常的功能


### ❌ 存在问题的功能


### ⚠️ 需要改进的地方


## 性能指标
- 启动时间: 
- 平均响应时间: 
- 内存占用: 
- 电量消耗: 

## 建议和后续行动


```

---

## 🛠️ 调试工具和技巧

### Chrome远程调试

```bash
# 1. 启动应用
# 2. 在Chrome中打开
chrome://inspect/#devices

# 3. 点击 inspect 查看Console和Network
```

### ADB日志查看

```bash
# 查看应用日志
adb logcat | grep -i "opencode"

# 清除日志
adb logcat -c

# 保存日志到文件
adb logcat > app-logs.txt
```

### Capacitor调试

```bash
# 在应用中打开Chrome DevTools
# Settings → About → 连续点击版本号启用开发者菜单
```

---

## 📝 相关文档

- [README.md](./README.md) - 项目概述
- [LOCAL_INTEGRATION_TEST_PLAN.md](./LOCAL_INTEGRATION_TEST_PLAN.md) - 本地集成测试
- [OPENCODE_MOBILE_MANAGEMENT_PLAN.md](./OPENCODE_MOBILE_MANAGEMENT_PLAN.md) - 移动端管理计划
- [COMPONENTS_IMPLEMENTATION_SUMMARY.md](./COMPONENTS_IMPLEMENTATION_SUMMARY.md) - 组件实现总结
- [frontend/capacitor.config.ts](./frontend/capacitor.config.ts) - Capacitor配置

---

## 🎯 快速开始（快速测试路径）

如果您想快速开始测试，执行以下步骤：

```bash
# 1. 启动后端
cd backend
go run cmd/pocketd/main.go &

# 2. 构建前端
cd ../frontend
npm run build

# 3. 同步到Android
npx cap sync android

# 4. 构建APK
cd android
./gradlew assembleDebug

# 5. 安装到设备
adb install -r app/build/outputs/apk/debug/app-debug.apk

# 6. 启动应用并开始测试
```

---

**注意事项**:
- 确保后端服务可以从移动设备访问（不能用localhost，需要用实际IP或域名）
- 测试过程中随时记录问题和想法
- 优先测试核心功能，再测试边界情况
- 保持测试环境的一致性，避免环境差异导致的问题

**下一步行动**: 开始执行阶段一的构建准备任务 ✅
