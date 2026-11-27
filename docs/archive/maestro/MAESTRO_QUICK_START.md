# OpenPocket Maestro 测试快速启动指南

**适用人群**: 开发者、测试人员、CI/CD 维护者  
**预计时间**: 10 分钟完成首次测试

---

## ⚡ 3 分钟快速开始

```bash
# 1. 配置环境变量
export POCKET_MASTER="12345678"
export POCKET_DEV_PASS="<DEV-PASS-PREFIX>…<masked>"

# 2. 配置端口转发
adb reverse tcp:18099 tcp:8088

# 3. 运行冒烟测试
./scripts/run-maestro-suite.sh smoke
```

**预期结果**: 3-5 分钟后看到 "所有测试通过 ✓"

---

## 📋 完整步骤

### 步骤 1: 检查前置条件

```bash
# 验证 Maestro 安装
maestro --version
# 预期: 2.11.0 或更高

# 验证 ADB 安装
adb --version
# 预期: Android Debug Bridge version 1.0.41

# 验证设备连接
adb devices
# 预期: 至少一个 device 状态的设备

# 运行完整环境检查
./scripts/verify-maestro-env.sh
```

**输出示例**:
```
═══════════════════════════════════════════════════════════════════
OpenPocket Maestro 环境检查
═══════════════════════════════════════════════════════════════════

[✓] Maestro 已安装: 2.11.0
[✓] ADB 已安装: Android Debug Bridge version 1.0.41
[✓] 发现 1 个设备/模拟器
[✓] OpenPocket 已安装
[!] 端口转发未配置 ← 需要修复
[!] 后端服务不可访问 ← 需要修复
```

### 步骤 2: 启动后端服务

**选项 A: 本地部署**
```bash
# 在新终端窗口
cd backend
export POCKET_JWT_SECRET="your-secret-key-here"
export POCKET_HTTP_PORT=8088
go run ./cmd/pocketd/main.go
```

**选项 B: Docker 部署**
```bash
cd deploy/本地方案
./local-up.sh
```

**选项 C: 使用远程后端**
```bash
# 在应用设置中配置远程服务器地址
# 无需本地后端
```

验证后端可访问:
```bash
curl http://127.0.0.1:8088/api/health
# 预期: {"status":"ok"}
```

### 步骤 3: 配置环境

```bash
# 方式 A: 临时设置（当前会话）
export POCKET_MASTER="12345678"
export POCKET_DEV_PASS="<DEV-PASS-PREFIX>…<masked>"

# 方式 B: 永久设置（推荐）
echo 'export POCKET_MASTER="12345678"' >> ~/.zshrc
echo 'export POCKET_DEV_PASS="<DEV-PASS-PREFIX>…<masked>"' >> ~/.zshrc
source ~/.zshrc

# 配置端口转发
adb reverse tcp:18099 tcp:8088

# 验证配置
adb reverse --list | grep 18099
# 预期: tcp:18099 -> tcp:8088
```

### 步骤 4: 安装应用（如需要）

```bash
# 构建 APK
cd frontend/android
./gradlew assembleDebug

# 安装到设备
adb install -r app/build/outputs/apk/debug/app-debug.apk

# 验证安装
adb shell pm list packages | grep pocket
# 预期: package:com.kaixuan.opencode.pocket
```

### 步骤 5: 运行测试

#### 冒烟测试（推荐首次）

```bash
./scripts/run-maestro-suite.sh smoke
```

**执行内容**:
- ✅ 应用启动验证
- ✅ 登录流程（四种状态）
- ✅ 核心导航（首页/笔记/消息/更多）
- ✅ 功能入口（快速提问/笔记创建）

**预期时间**: 3-5 分钟  
**预期结果**: 
```
[INFO] 冒烟测试完成: 1 通过, 0 失败
[SUCCESS] 所有测试通过 ✓
```

#### 完整测试（发布前）

```bash
./scripts/run-maestro-suite.sh master
```

**执行内容**:
- ✅ L0: 启动与认证
- ✅ L1: 核心功能（AI/Notes/Email）
- ✅ L2: 扩展功能（Meetings/Tasks/RSS）
- ✅ L3: 高级功能（ACC/Gateway/Vault）

**预期时间**: 15-20 分钟  
**预期结果**: 10+ 个测试场景通过

#### 回归测试（PR 验证）

```bash
./scripts/run-maestro-suite.sh regression
```

**执行内容**:
- ✅ 笔记 CRUD 完整流程
- ✅ 任务创建与管理
- ✅ 邮件浏览与操作
- ✅ 闪卡学习流程
- ✅ 消息中心验证

**预期时间**: 10-12 分钟

#### 运行所有测试

```bash
./scripts/run-maestro-suite.sh all
```

**预期时间**: 30-40 分钟

### 步骤 6: 查看结果

```bash
# 查看测试摘要
cat test-reports/test-summary-*.txt

# 查看截图
ls -lh logs/maestro/*.png
open logs/maestro/  # macOS
xdg-open logs/maestro/  # Linux

# 查看详细日志
tail -f logs/maestro/smoke-test-suite.log

# 查看 JUnit 报告（可导入 CI）
ls test-reports/*.xml
```

---

## 🎯 运行单个测试

```bash
# 直接运行单个测试文件
maestro test .maestro/notes-crud.yaml

# 生成报告
maestro test .maestro/notes-crud.yaml \
  --format junit \
  --output test-reports/notes-crud.xml

# 查看实时日志
maestro test .maestro/notes-crud.yaml --debug

# 录制视频
maestro test .maestro/notes-crud.yaml \
  --include-tags video
```

---

## 🐛 故障排查

### 问题 1: "No devices found"

**原因**: 设备未连接或 ADB 未识别

**解决**:
```bash
# 重启 ADB
adb kill-server
adb start-server

# 检查设备
adb devices

# 如果使用模拟器
emulator -avd pocket_test &
```

### 问题 2: "Connection refused"

**原因**: 后端服务未启动或端口转发未配置

**解决**:
```bash
# 检查后端
curl http://127.0.0.1:8088/api/health

# 重新配置端口转发
adb reverse tcp:18099 tcp:8088

# 验证转发
adb reverse --list
```

### 问题 3: "Timeout waiting for element"

**原因**: 应用加载慢或元素选择器错误

**解决**:
```bash
# 清除应用数据重试
adb shell pm clear com.kaixuan.opencode.pocket

# 增加超时时间（编辑测试文件）
# 将 timeout: 30000 改为 timeout: 60000

# 检查应用日志
adb logcat | grep pocket
```

### 问题 4: "Login failed"

**原因**: 环境变量未设置或密码错误

**解决**:
```bash
# 检查环境变量
echo $POCKET_MASTER
echo $POCKET_DEV_PASS

# 重新设置
export POCKET_MASTER="12345678"
export POCKET_DEV_PASS="<DEV-PASS-PREFIX>…<masked>"

# 验证
./scripts/verify-maestro-env.sh
```

### 问题 5: "App not installed"

**原因**: APK 未安装或版本不匹配

**解决**:
```bash
# 检查安装
adb shell pm list packages | grep pocket

# 重新安装
adb install -r frontend/android/app/build/outputs/apk/debug/app-debug.apk

# 强制重装
adb uninstall com.kaixuan.opencode.pocket
adb install frontend/android/app/build/outputs/apk/debug/app-debug.apk
```

---

## 📊 测试结果解读

### 成功示例

```
═══════════════════════════════════════════════════════════════════
OpenPocket Maestro 测试报告
═══════════════════════════════════════════════════════════════════

日期: 2026-11-23 15:30:00
套件: smoke

测试结果:
  ✓ smoke-test-suite: PASSED (180s)

截图生成:
  smoke-01-logged-in.png
  smoke-02-notes.png
  smoke-03-messages.png
  smoke-04-more.png
  smoke-05-quick-question.png
  smoke-06-note-create.png
  smoke-99-complete.png

[SUCCESS] 所有测试通过 ✓
```

### 失败示例

```
测试结果:
  ✗ smoke-test-suite: FAILED at step 15 (120s)

失败步骤:
  Assert that "笔记|Notes" is visible... FAILED

错误日志:
  logs/maestro/smoke-test-suite.log

失败截图:
  logs/maestro/smoke-06-failure.png

建议:
  1. 检查应用是否响应
  2. 检查网络连接
  3. 查看详细日志
```

---

## 🔄 CI/CD 集成

### GitHub Actions

在 `.github/workflows/maestro-tests.yml`:

```yaml
name: Maestro Tests

on:
  pull_request:
  push:
    branches: [main]

jobs:
  test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Setup Maestro
        run: |
          curl -Ls "https://get.maestro.mobile.dev" | bash
          export PATH="$PATH:$HOME/.maestro/bin"
      
      - name: Setup Android Emulator
        uses: reactivecircus/android-emulator-runner@v2
        with:
          api-level: 30
          script: |
            export POCKET_MASTER="${{ secrets.POCKET_MASTER }}"
            export POCKET_DEV_PASS="${{ secrets.POCKET_DEV_PASS }}"
            ./scripts/run-maestro-suite.sh smoke
      
      - name: Upload Reports
        if: always()
        uses: actions/upload-artifact@v3
        with:
          name: maestro-reports
          path: |
            test-reports/
            logs/maestro/
```

### Jenkins

```groovy
pipeline {
    agent any
    
    environment {
        POCKET_MASTER = credentials('pocket-master-password')
        POCKET_DEV_PASS = credentials('pocket-dev-password')
    }
    
    stages {
        stage('Test') {
            steps {
                sh '''
                    adb reverse tcp:18099 tcp:8088
                    ./scripts/run-maestro-suite.sh smoke
                '''
            }
        }
        
        stage('Report') {
            steps {
                junit 'test-reports/*.xml'
                archiveArtifacts 'logs/maestro/**'
            }
        }
    }
}
```

---

## 📝 最佳实践

### 1. 测试前准备

- ✅ 清除应用数据获得干净状态
- ✅ 确保后端服务健康
- ✅ 检查设备存储空间充足
- ✅ 关闭其他可能干扰的应用

### 2. 测试执行

- ✅ 从冒烟测试开始
- ✅ 失败后查看日志和截图
- ✅ 重现问题时记录详细步骤
- ✅ 保存测试报告供分析

### 3. 测试维护

- ✅ 定期更新测试用例
- ✅ 补充缺失的测试场景
- ✅ 重构重复的测试代码
- ✅ 文档化已知问题

---

## 🎓 进阶技巧

### 自定义测试

创建新测试文件 `.maestro/my-test.yaml`:

```yaml
appId: com.kaixuan.opencode.pocket
---
- launchApp
- runFlow: _login.yaml

# 你的测试步骤
- tapOn: "某个按钮"
- wait: 2000
- assertVisible: "预期结果"
- takeScreenshot: "my-test-result"
```

运行:
```bash
maestro test .maestro/my-test.yaml
```

### 条件测试

```yaml
# 只在元素存在时执行
- runFlow:
    when:
      visible: "某个元素"
    commands:
      - tapOn: "某个元素"
      - wait: 1000
```

### 循环测试

```yaml
# 重复操作
- repeat:
    times: 3
    commands:
      - tapOn: "刷新"
      - wait: 2000
```

### 数据驱动

```yaml
# 使用环境变量
- inputText: ${TEST_USERNAME}
- inputText: ${TEST_PASSWORD}
```

---

## 📞 获取帮助

- **项目文档**: [MAESTRO_TEST_PLAN.md](MAESTRO_TEST_PLAN.md)
- **测试报告**: [MAESTRO_TEST_REPORT_TEMPLATE.md](MAESTRO_TEST_REPORT_TEMPLATE.md)
- **Maestro 官方**: https://maestro.mobile.dev/
- **问题反馈**: 在项目中创建 Issue

---

**版本**: 1.0.0  
**最后更新**: 2026-11-23  
**维护者**: OpenPocket Team
