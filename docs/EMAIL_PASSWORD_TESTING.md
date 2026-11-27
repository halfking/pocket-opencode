# 邮箱验证码密码管理功能 - Maestro 测试文档

## 测试概述

本测试套件使用 Maestro 对邮箱验证码密码管理功能进行全面的端到端测试。

## 测试覆盖

### 1. 核心功能测试
- ✅ 已登录用户通过邮箱验证码修改密码
- ✅ 验证码发送和接收
- ✅ 密码修改成功后自动退出
- ✅ 使用新密码重新登录

### 2. 错误场景测试
- ✅ 验证码错误
- ✅ 密码不一致
- ✅ 密码强度不足
- ✅ 邮箱格式错误

### 3. 登录后邮箱检查测试
- ✅ 新用户登录自动弹出邮箱设置
- ✅ 跳过邮箱设置
- ✅ 完成邮箱设置
- ✅ 邮箱验证流程

### 4. UI 组件测试
- ✅ 邮箱验证对话框显示
- ✅ 验证码冷却时间
- ✅ Debug 模式验证码显示
- ✅ 表单验证

## 前置要求

### 1. 环境准备

#### 安装 Maestro
```bash
# macOS/Linux
curl -Ls "https://get.maestro.mobile.dev" | bash

# 验证安装
maestro --version
```

#### Android 设备准备
- 连接 Android 设备或启动模拟器
- 启用 USB 调试
- 验证连接：`adb devices`

### 2. 应用准备

#### 安装最新 APK
```bash
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket
./deploy-211.sh  # 或其他部署脚本
```

#### 后端配置
确保后端配置了 SMTP Debug 模式：

```bash
# backend/.env
POCKET_SMTP_DEBUG_ECHO=true
```

### 3. 测试账号准备

#### 标准测试账号
- **用户名**: admin
- **密码**: admin
- **邮箱**: test@example.com

#### 无邮箱测试账号（可选）
需要手动在数据库中创建一个 email 字段为 NULL 的测试用户：

```sql
-- 创建无邮箱测试用户
INSERT INTO users (id, username, password_hash, role, email, email_verified)
VALUES ('user-noemail', 'testuser', '$2a$...', 'user', NULL, false);
```

或者注册新用户后在数据库中删除邮箱：
```sql
UPDATE users SET email = NULL, email_verified = false WHERE username = 'testuser';
```

### 4. 环境变量配置

创建 `.env.test` 文件：

```bash
# /Users/xutaohuang/workspace/ai-native-tools/client/openpocket/.env.test

# 主密码（用于解锁本地数据）
POCKET_MASTER=TestMaster123

# 标准测试账号
POCKET_DEV_PASS=admin
POCKET_OLD_PASS=admin
POCKET_NEW_PASS=<NEW-PASS>…<masked>

# 测试邮箱
POCKET_TEST_EMAIL=test@example.com

# 无邮箱测试账号（可选）
POCKET_NO_EMAIL_USER=testuser
POCKET_NO_EMAIL_PASS=testpass
POCKET_TEST_EMAIL_NEW=newtest@example.com
```

## 运行测试

### 快速开始

```bash
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket

# 运行完整测试套件
./scripts/run-email-tests.sh

# 或使用 Maestro 直接运行
maestro test .maestro/email-password-suite.yaml
```

### 运行单个测试

```bash
# 只测试密码修改功能
./scripts/run-email-tests.sh change-password

# 只测试错误场景
./scripts/run-email-tests.sh errors

# 只测试登录后邮箱设置
./scripts/run-email-tests.sh login-setup

# 只测试邮箱验证对话框
./scripts/run-email-tests.sh dialog
```

### 手动运行 Maestro 测试

```bash
# 设置环境变量
export POCKET_MASTER="TestMaster123"
export POCKET_DEV_PASS="admin"
export POCKET_TEST_EMAIL="test@example.com"
export POCKET_OLD_PASS="admin"
export POCKET_NEW_PASS="<NEW-PASS>…<masked>"

# 运行测试
maestro test .maestro/email-password-change.yaml
maestro test .maestro/email-password-errors.yaml
maestro test .maestro/email-setup-on-login.yaml
```

## 测试文件说明

### 1. email-password-change.yaml
**测试内容**: 已登录用户修改密码完整流程

**测试步骤**:
1. 登录系统
2. 导航到修改密码页面
3. 输入邮箱并发送验证码
4. 输入验证码和新密码
5. 提交修改
6. 验证自动退出
7. 使用新密码重新登录
8. 恢复旧密码（清理）

**预期结果**:
- 验证码成功发送
- 密码修改成功
- 自动退出到登录页
- 新密码可以登录

### 2. email-password-errors.yaml
**测试内容**: 各种错误场景

**测试场景**:
1. 验证码错误
2. 密码不一致
3. 密码强度不足
4. 邮箱格式错误

**预期结果**:
- 显示正确的错误提示
- 不允许提交无效数据

### 3. email-setup-on-login.yaml
**测试内容**: 登录后邮箱检查和设置流程

**测试步骤**:
1. 使用无邮箱账号登录
2. 验证邮箱设置对话框弹出
3. 测试跳过功能
4. 重新登录并完成邮箱设置
5. 验证邮箱设置成功

**预期结果**:
- 无邮箱用户登录后自动弹出设置对话框
- 可以跳过设置
- 可以成功设置邮箱

### 4. email-verification-dialog.yaml
**测试内容**: 邮箱验证对话框组件

**测试步骤**:
1. 触发邮箱验证对话框
2. 测试验证码发送
3. 测试冷却时间
4. 测试验证码输入和验证

**预期结果**:
- 对话框正确显示
- 冷却时间机制生效
- 验证码验证成功

### 5. email-password-suite.yaml
**测试内容**: 聚合测试套件

按顺序运行所有上述测试，并处理测试间的清理工作。

## 故障排查

### 测试失败常见原因

#### 1. "visible" 断言失败
**原因**: 页面加载慢或元素未正确渲染

**解决方案**:
- 增加 `extendedWaitUntil` 的超时时间
- 检查网络连接
- 确保后端服务正常运行

#### 2. 验证码错误
**原因**: 
- SMTP_DEBUG_ECHO 未启用
- 验证码已过期
- 验证码在测试中被硬编码

**解决方案**:
```bash
# 确保后端配置正确
POCKET_SMTP_DEBUG_ECHO=true

# 如果使用真实 SMTP，需要从邮箱获取验证码
```

#### 3. 主密码弹窗未处理
**原因**: 本地加密状态不一致

**解决方案**:
```bash
# 清理应用数据
adb shell pm clear com.kaixuan.opencode.pocket

# 或在设备上手动清除应用数据
```

#### 4. 设备未连接
**原因**: ADB 连接问题

**解决方案**:
```bash
# 重启 ADB
adb kill-server
adb start-server

# 验证设备
adb devices

# 如果使用无线调试
adb connect <device-ip>:5555
```

### 查看测试日志

Maestro 会在 `~/.maestro/tests/` 目录下保存测试日志和截图：

```bash
# 查看最新测试日志
ls -lt ~/.maestro/tests/ | head -10

# 查看特定测试的截图
open ~/.maestro/tests/<test-timestamp>/
```

## 持续集成

### GitHub Actions 示例

```yaml
name: Email Password Management E2E Tests

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  maestro-test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Install Maestro
        run: |
          curl -Ls "https://get.maestro.mobile.dev" | bash
          echo "$HOME/.maestro/bin" >> $GITHUB_PATH
      
      - name: Setup Android Emulator
        uses: reactivecircus/android-emulator-runner@v2
        with:
          api-level: 34
          arch: x86_64
          profile: pixel_6
          script: |
            adb wait-for-device
            adb devices
      
      - name: Install APK
        run: |
          # 构建或下载 APK
          # adb install app-debug.apk
      
      - name: Run Maestro Tests
        env:
          POCKET_MASTER: ${{ secrets.POCKET_MASTER }}
          POCKET_TEST_EMAIL: test@example.com
          POCKET_OLD_PASS: admin
          POCKET_NEW_PASS: <NEW-PASS>…<masked>
        run: |
          maestro test .maestro/email-password-suite.yaml
      
      - name: Upload Test Results
        if: always()
        uses: actions/upload-artifact@v3
        with:
          name: maestro-results
          path: ~/.maestro/tests/
```

## 测试最佳实践

### 1. 测试隔离
- 每个测试开始前清理应用状态
- 使用独立的测试账号
- 测试完成后恢复原始状态

### 2. 等待策略
- 使用 `extendedWaitUntil` 而不是固定延迟
- 为慢操作（网络请求、数据库）增加足够的超时
- 验证关键元素的可见性

### 3. 错误处理
- 使用 `optional: true` 处理可选元素
- 使用 `when` 条件处理分支逻辑
- 捕获并记录错误信息

### 4. 国际化
- 使用正则表达式匹配多语言文本
- 例如：`"打开菜单|Open menu"`
- 测试多语言环境

## 性能监控

### 测试执行时间基准

| 测试 | 预期时间 | 超时阈值 |
|------|---------|---------|
| email-password-change | 2-3分钟 | 5分钟 |
| email-password-errors | 1-2分钟 | 3分钟 |
| email-setup-on-login | 1-2分钟 | 3分钟 |
| email-verification-dialog | 1分钟 | 2分钟 |
| 完整测试套件 | 5-7分钟 | 15分钟 |

### 监控指标

- 测试成功率
- 平均执行时间
- 失败原因分布
- 设备/OS 版本覆盖率

## 扩展测试

### 计划中的测试场景

1. **多设备测试**
   - 不同 Android 版本
   - 不同屏幕尺寸
   - 不同语言环境

2. **性能测试**
   - 验证码发送响应时间
   - 密码修改流程耗时
   - UI 响应性能

3. **安全测试**
   - SQL 注入尝试
   - XSS 攻击防护
   - 验证码暴力破解防护

4. **边界条件测试**
   - 超长邮箱地址
   - 特殊字符处理
   - 网络中断恢复

## 联系和支持

如有问题，请：
1. 查看本文档的故障排查部分
2. 查看 Maestro 官方文档：https://maestro.mobile.dev
3. 提交 Issue 到项目仓库

## 更新日志

- 2026-11-23: 初始版本，覆盖核心功能和错误场景
