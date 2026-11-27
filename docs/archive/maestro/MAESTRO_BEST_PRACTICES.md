# OpenPocket Maestro 测试最佳实践指南

**版本**: 1.0.0  
**日期**: 2026-11-23

---

## 📚 目录

1. [测试编写最佳实践](#测试编写最佳实践)
2. [选择器策略](#选择器策略)
3. [等待与超时](#等待与超时)
4. [截图策略](#截图策略)
5. [错误处理](#错误处理)
6. [性能优化](#性能优化)
7. [测试数据管理](#测试数据管理)
8. [CI/CD 集成](#cicd-集成)
9. [调试技巧](#调试技巧)
10. [常见陷阱](#常见陷阱)

---

## 1. 测试编写最佳实践

### ✅ 使用 extendedWaitUntil

```yaml
# ✓ 好 - 等待元素出现
- extendedWaitUntil:
    visible: "AI 工具"
    timeout: 30000

# ✗ 差 - 直接断言可能失败
- assertVisible: "AI 工具"
```

**原因**: WebView 异步渲染，直接断言可能在元素渲染前执行。

### ✅ 中英文并列选择器

```yaml
# ✓ 好 - 支持多语言
- tapOn: "笔记|Notes"
- assertVisible: "AI 工具|AI Tools"

# ✗ 差 - 只支持中文
- tapOn: "笔记"
```

**原因**: 应用支持多语言，测试应适配所有语言环境。

### ✅ 合理使用 optional

```yaml
# 可能不存在的元素
- tapOn:
    text: "关闭|Close"
    optional: true

# 可选的操作流程
- runFlow:
    when:
      visible: "某个条件"
    commands:
      - tapOn: "执行操作"
```

**原因**: 提高测试鲁棒性，处理不确定的 UI 状态。

### ✅ 清晰的测试结构

```yaml
appId: com.kaixuan.opencode.pocket
---
# ═══════════════════════════════════════════════════════════════════
# 测试名称
# ═══════════════════════════════════════════════════════════════════
# 
# 用途: 测试目标
# 执行时间: 预估时间
# 前置条件: 依赖列表
#
# ═══════════════════════════════════════════════════════════════════

# 场景 1: 描述
- step1

# 场景 2: 描述
- step2
```

**原因**: 良好的组织结构提高可维护性。

---

## 2. 选择器策略

### 文本选择器

```yaml
# ✓ 精确匹配
- tapOn: "^登录$"

# ✓ 中英文并列
- tapOn: "笔记|Notes"

# ✓ 正则表达式
- tapOn: ".*@.*"  # 匹配邮箱

# ✗ 模糊匹配容易误命中
- tapOn: "登"
```

### 索引选择器

```yaml
# 点击第一个元素
- tapOn:
    index: 0

# 点击第二个元素
- tapOn:
    index: 1
```

**注意**: 索引依赖元素顺序，可能不稳定。

### 坐标选择器

```yaml
# 百分比坐标（推荐）
- tapOn:
    point: "50%,50%"

# 绝对坐标（不推荐）
- tapOn:
    point: "360,967"
```

**原因**: 百分比坐标适应不同屏幕尺寸。

### 属性选择器

```yaml
# 检查启用状态
- tapOn:
    text: "登录"
    enabled: true

# 检查选中状态
- tapOn:
    text: "选项"
    selected: true
```

---

## 3. 等待与超时

### 等待策略

```yaml
# ✓ 动态等待 - 元素出现后立即继续
- extendedWaitUntil:
    visible: "加载完成"
    timeout: 30000

# ✓ 固定等待 - 用于动画或异步操作
- wait: 2000

# ✗ 过长的固定等待浪费时间
- wait: 10000
```

### 超时设置

| 操作类型 | 推荐超时 | 说明 |
|---|---|---|
| 页面加载 | 10-30s | 依赖网络和数据量 |
| 按钮点击响应 | 2-5s | 普通操作 |
| 网络请求 | 10-15s | API 调用 |
| 录音/转写 | 30-60s | 重型操作 |
| 应用启动 | 15-30s | 首次启动更慢 |

### 等待元素消失

```yaml
# 等待加载指示器消失
- extendedWaitUntil:
    notVisible: "加载中"
    timeout: 30000
```

---

## 4. 截图策略

### 截图时机

```yaml
# ✓ 关键步骤后截图
- tapOn: "登录"
- wait: 2000
- takeScreenshot: "01-login-success"

# ✓ 每个测试场景截图
- runFlow: some-flow.yaml
- takeScreenshot: "02-flow-complete"

# ✓ 失败点附近截图
- takeScreenshot: "before-critical-step"
- tapOn: "关键操作"
- takeScreenshot: "after-critical-step"
```

### 截图命名

```yaml
# ✓ 有序且描述性
- takeScreenshot: "01-login"
- takeScreenshot: "02-home"
- takeScreenshot: "99-complete"

# ✗ 无序或不清楚
- takeScreenshot: "screenshot1"
- takeScreenshot: "test"
```

### 截图目录

```
logs/maestro/
├── smoke-01-login.png
├── smoke-02-notes.png
├── master-01-dashboard.png
└── regression-01-note-created.png
```

---

## 5. 错误处理

### 条件执行

```yaml
# 处理可选元素
- runFlow:
    when:
      visible: "弹窗"
    commands:
      - tapOn: "关闭"
      - wait: 1000

# 处理多种状态
- runFlow:
    when:
      visible: "错误信息"
    commands:
      - takeScreenshot: "error-occurred"
      - tapOn: "确定"
```

### 重试策略

```yaml
# 重试点击
- repeat:
    times: 3
    commands:
      - tapOn:
          text: "刷新"
          optional: true
      - wait: 2000
```

### 清理操作

```yaml
# 确保返回初始状态
- tapOn:
    point: "5%,5%"
- wait: 1000
- assertVisible: "首页"
```

---

## 6. 性能优化

### 减少不必要的等待

```yaml
# ✗ 固定等待 5 秒
- wait: 5000
- assertVisible: "元素"

# ✓ 动态等待，最多 5 秒
- extendedWaitUntil:
    visible: "元素"
    timeout: 5000
```

### 并行测试

```bash
# 运行多个独立测试
maestro test test1.yaml & \
maestro test test2.yaml & \
maestro test test3.yaml & \
wait
```

### 复用子流程

```yaml
# 定义子流程 _common-setup.yaml
- launchApp
- runFlow: _login.yaml

# 在测试中复用
- runFlow: _common-setup.yaml
- # 开始测试
```

---

## 7. 测试数据管理

### 环境变量

```yaml
# 使用环境变量
- inputText: ${TEST_USERNAME}
- inputText: ${TEST_PASSWORD}
```

```bash
# 设置环境变量
export TEST_USERNAME="testuser"
export TEST_PASSWORD="testpass"
```

### 测试数据文件

```yaml
# 外部数据文件
envVars:
  - file: test-data.env
```

### 数据清理

```yaml
# 测试前清理
- launchApp:
    clearState: true

# 测试后清理（在脚本中）
adb shell pm clear com.kaixuan.opencode.pocket
```

---

## 8. CI/CD 集成

### GitHub Actions

```yaml
name: Maestro Tests

on: [pull_request, push]

jobs:
  test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Setup Maestro
        run: curl -Ls "https://get.maestro.mobile.dev" | bash
      
      - name: Run Tests
        env:
          POCKET_MASTER: ${{ secrets.POCKET_MASTER }}
          POCKET_DEV_PASS: ${{ secrets.POCKET_DEV_PASS }}
        run: ./scripts/run-maestro-suite.sh smoke
      
      - name: Upload Reports
        if: always()
        uses: actions/upload-artifact@v3
        with:
          name: test-reports
          path: test-reports/
```

### Jenkins Pipeline

```groovy
pipeline {
    agent any
    
    stages {
        stage('Test') {
            steps {
                script {
                    sh '''
                        export POCKET_MASTER="${POCKET_MASTER}"
                        export POCKET_DEV_PASS="${POCKET_DEV_PASS}"
                        ./scripts/run-maestro-suite.sh smoke
                    '''
                }
            }
        }
    }
    
    post {
        always {
            junit 'test-reports/*.xml'
            archiveArtifacts 'logs/maestro/**'
        }
    }
}
```

---

## 9. 调试技巧

### 调试模式

```bash
# 启用调试日志
maestro test --debug test.yaml

# 查看详细输出
maestro test --verbose test.yaml
```

### 实时日志

```bash
# 应用日志
adb logcat | grep pocket

# 系统日志
adb logcat -s ActivityManager
```

### 单步执行

```bash
# 失败后继续
maestro test --continue-on-failure test.yaml

# 交互模式（逐步执行）
maestro studio
```

### 检查无障碍树

```bash
# 导出无障碍树
adb shell uiautomator dump /sdcard/ui.xml
adb pull /sdcard/ui.xml .

# 查看 Maestro 抓取的树
~/.maestro/tests/<timestamp>/hierarchy/*.json
```

---

## 10. 常见陷阱

### ❌ 陷阱 1: 依赖固定等待

```yaml
# ✗ 错误
- tapOn: "按钮"
- wait: 5000  # 盲目等待
- assertVisible: "结果"

# ✓ 正确
- tapOn: "按钮"
- extendedWaitUntil:
    visible: "结果"
    timeout: 10000
```

### ❌ 陷阱 2: 选择器不稳定

```yaml
# ✗ 错误 - 依赖索引
- tapOn:
    index: 2

# ✓ 正确 - 使用文本
- tapOn: "具体按钮名称"
```

### ❌ 陷阱 3: 忽略清理

```yaml
# ✗ 错误 - 状态残留
- tapOn: "打开对话框"
- # 测试结束，对话框未关闭

# ✓ 正确 - 清理状态
- tapOn: "打开对话框"
- # 测试操作
- tapOn: "关闭"
- assertNotVisible: "对话框"
```

### ❌ 陷阱 4: 过度依赖坐标

```yaml
# ✗ 错误 - 硬编码坐标
- tapOn:
    point: "360,967"

# ✓ 正确 - 使用百分比
- tapOn:
    point: "50%,59%"

# ✓ 更好 - 使用选择器
- tapOn: "输入框标签"
```

### ❌ 陷阱 5: 缺少失败处理

```yaml
# ✗ 错误 - 没有错误处理
- tapOn: "可能不存在的按钮"

# ✓ 正确 - 条件处理
- runFlow:
    when:
      visible: "按钮"
    commands:
      - tapOn: "按钮"
```

---

## 📝 检查清单

### 测试编写检查

- [ ] 使用 extendedWaitUntil 而非裸断言
- [ ] 选择器支持中英文
- [ ] 合理的超时时间
- [ ] 关键步骤有截图
- [ ] 有清晰的注释和分段
- [ ] 处理可选元素和错误情况
- [ ] 测试结束后状态清理

### 测试执行检查

- [ ] 环境变量已设置
- [ ] 后端服务正常运行
- [ ] 设备已连接
- [ ] 应用已安装
- [ ] 端口转发已配置
- [ ] 有足够的存储空间

### 测试维护检查

- [ ] 定期更新测试用例
- [ ] 修复失败的测试
- [ ] 补充缺失的测试场景
- [ ] 重构重复代码
- [ ] 更新文档
- [ ] 跟踪覆盖率

---

## 🎓 学习资源

### 官方文档

- [Maestro 官方文档](https://maestro.mobile.dev/)
- [Maestro API 参考](https://maestro.mobile.dev/api-reference)
- [示例测试](https://github.com/mobile-dev-inc/maestro/tree/main/examples)

### 项目资源

- [测试计划](MAESTRO_TEST_PLAN.md)
- [快速启动](MAESTRO_QUICK_START.md)
- [现有测试](.maestro/)

---

**版本**: 1.0.0  
**维护者**: OpenPocket Team  
**最后更新**: 2026-11-23
