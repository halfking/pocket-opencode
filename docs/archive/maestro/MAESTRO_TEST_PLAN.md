# OpenPocket Maestro 测试方案

**版本**: 1.0.0  
**日期**: 2026-11-23  
**状态**: Active

---

## 📋 概述

本文档定义了 OpenPocket 应用的完整 Maestro 测试策略，覆盖功能测试、回归测试和性能验证。

### 测试目标

1. **功能完整性**: 验证 34 个前端域的核心功能
2. **回归保护**: 确保代码变更不破坏现有功能
3. **用户体验**: 验证关键用户流程的流畅性
4. **稳定性**: 检测崩溃、ANR 和性能问题

---

## 🎯 测试覆盖范围

### 已覆盖功能域（20/34）

| 域 | 测试套件 | 覆盖率 | 优先级 |
|---|---|---|---|
| **AI 工具** (tasks) | Master, Smoke | 100% | P0 |
| **笔记** (notes) | Master, Regression | 100% | P0 |
| **消息** (messages) | Master, Regression | 80% | P0 |
| **邮箱** (email) | Master, Regression | 70% | P1 |
| **会议** (meetings) | Master | 60% | P1 |
| **任务看板** (acc-board) | Master | 50% | P1 |
| **设置** (settings) | Master | 50% | P1 |
| **RSS** (rss) | Master | 40% | P2 |
| **密码箱** (vault) | Master | 40% | P2 |
| **闪卡** (flashcards) | Regression | 30% | P2 |
| **认证** (auth) | 所有套件 | 100% | P0 |

### 待补充域（14/34）

- AI Chat (ai-chat)
- 智能体库 (agents)
- 市场 (marketplace)
- PKM 知识库 (pkm)
- 财务 (finance)
- 计划任务 (scheduled-tasks)
- 联系人 (contact)
- 聊天 (chat)
- OpenCode 兼容层 (opencode)
- 实例管理 (instances)
- LLM 网关 (gateway) - 部分覆盖
- 学习 (study)
- 日历 (calendar)
- 知识库 (knowledge)

---

## 🧪 测试套件结构

### 1. 冒烟测试 (Smoke Test Suite)

**文件**: `.maestro/smoke-test-suite.yaml`  
**执行时间**: 3-5 分钟  
**运行时机**: 每次构建后、部署前

**测试内容**:
- ✅ 应用启动
- ✅ 登录流程（四种状态）
- ✅ 主要导航（首页/笔记/消息/更多）
- ✅ 核心功能入口（快速提问/笔记创建）

**通过标准**: 100% 通过

```bash
./scripts/run-maestro-suite.sh smoke
```

---

### 2. 完整测试 (Master Test Suite)

**文件**: `.maestro/master-test-suite.yaml`  
**执行时间**: 15-20 分钟  
**运行时机**: 每日构建、发布前

**测试分层**:
- **L0 - 启动与认证**: 必须 100% 通过
- **L1 - 核心功能**: AI/Notes/Email（目标 95%）
- **L2 - 扩展功能**: Meetings/Tasks/RSS（目标 90%）
- **L3 - 高级功能**: ACC/Gateway/Vault（目标 80%）

**通过标准**: L0 100%, L1+L2 ≥ 90%, L3 ≥ 80%

```bash
./scripts/run-maestro-suite.sh master
```

---

### 3. 回归测试 (Regression Test Suite)

**文件**: `.maestro/regression-test-suite.yaml`  
**执行时间**: 10-12 分钟  
**运行时机**: PR 合并前、Hot Fix 前

**测试场景**:
1. 笔记 CRUD 完整流程
2. 任务创建与管理
3. 邮件浏览与操作
4. 闪卡学习流程
5. 消息中心验证

**通过标准**: 100% 通过

```bash
./scripts/run-maestro-suite.sh regression
```

---

## 🔧 环境配置

### 前置条件

1. **Maestro CLI**: 
   ```bash
   curl -Ls "https://get.maestro.mobile.dev" | bash
   ```

2. **Android 设备/模拟器**:
   - API Level ≥ 24
   - 已安装最新 APK
   - 网络连接正常

3. **后端服务**:
   ```bash
   # 方式 1: 本地后端 + 端口转发
   adb reverse tcp:18099 tcp:8088
   
   # 方式 2: 远程后端（在应用设置中配置）
   ```

4. **环境变量**:
   ```bash
   export POCKET_MASTER="12345678"           # 主密码
   export POCKET_DEV_PASS="Veritrans&9527"   # 测试账号密码
   ```

---

## 📊 测试执行

### 快速开始

```bash
# 1. 确保设备连接
adb devices

# 2. 配置端口转发
adb reverse tcp:18099 tcp:8088

# 3. 运行冒烟测试
./scripts/run-maestro-suite.sh smoke

# 4. 运行完整测试
./scripts/run-maestro-suite.sh master

# 5. 运行回归测试
./scripts/run-maestro-suite.sh regression

# 6. 运行所有测试
./scripts/run-maestro-suite.sh all
```

### 单个测试执行

```bash
# 直接运行单个测试文件
maestro test .maestro/notes-crud.yaml

# 带报告输出
maestro test .maestro/notes-crud.yaml \
  --format junit \
  --output test-reports/notes-crud-report.xml
```

---

## 📈 测试报告

### 报告位置

- **JUnit XML**: `test-reports/*.xml`
- **截图**: `logs/maestro/*.png`
- **日志**: `logs/maestro/*.log`
- **汇总**: `test-reports/test-summary-*.txt`

### 报告解读

```
═══════════════════════════════════════════════════════════════════
OpenPocket Maestro 测试报告
═══════════════════════════════════════════════════════════════════

日期: 2026-11-23 14:30:00
套件: master

测试结果:
  ✓ L0 - 启动与认证: 1/1 通过 (100%)
  ✓ L1 - 核心功能: 3/3 通过 (100%)
  ✓ L2 - 扩展功能: 4/4 通过 (100%)
  ⚠ L3 - 高级功能: 2/3 通过 (67%)

总计: 10/11 通过 (91%)
```

---

## 🐛 故障排查

### 常见问题

#### 1. 测试超时

**现象**: `extendedWaitUntil timeout after 60000ms`

**原因**:
- 后端服务未启动
- 端口转发未配置
- 网络延迟过高
- 应用卡在某个状态

**解决**:
```bash
# 检查后端
curl http://127.0.0.1:18099/api/health

# 检查端口转发
adb reverse --list

# 重新配置
adb reverse tcp:18099 tcp:8088
```

#### 2. 登录失败

**现象**: `Assert that "密码登录" is not visible... FAILED`

**原因**:
- 主密码错误
- 账号密码错误
- 本地数据库损坏

**解决**:
```bash
# 清除应用数据
adb shell pm clear com.kaixuan.opencode.pocket

# 重新运行测试
./scripts/run-maestro-suite.sh smoke
```

#### 3. 元素找不到

**现象**: `No element found matching "某个文本"`

**原因**:
- 中英文语言不匹配
- 控件未渲染
- 选择器错误

**解决**:
- 使用中英文并列选择器: `"笔记|Notes"`
- 增加等待时间: `wait: 2000`
- 使用坐标点击: `point: "50%,50%"`

---

## 🔄 CI/CD 集成

### GitHub Actions

```yaml
name: Maestro Tests

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

jobs:
  maestro-tests:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Setup Maestro
        run: |
          curl -Ls "https://get.maestro.mobile.dev" | bash
          
      - name: Setup Android Emulator
        uses: reactivecircus/android-emulator-runner@v2
        with:
          api-level: 30
          target: default
          arch: x86_64
          
      - name: Build APK
        run: |
          cd frontend/android
          ./gradlew assembleDebug
          
      - name: Install APK
        run: |
          adb install -r frontend/android/app/build/outputs/apk/debug/app-debug.apk
          
      - name: Run Smoke Tests
        run: |
          ./scripts/run-maestro-suite.sh smoke
          
      - name: Upload Test Reports
        if: always()
        uses: actions/upload-artifact@v3
        with:
          name: maestro-reports
          path: test-reports/
```

---

## 📝 测试用例编写规范

### 命名规范

- **测试套件**: `*-test-suite.yaml`
- **子流程**: `_*.yaml` (下划线开头)
- **功能测试**: `<feature>-<action>.yaml`

### 结构规范

```yaml
appId: com.kaixuan.opencode.pocket
---
# ═══════════════════════════════════════════════════════════════════
# 测试名称
# ═══════════════════════════════════════════════════════════════════
# 
# 用途: 简短描述
# 执行时间: 预估时间
# 前置条件: 列出依赖
#
# ═══════════════════════════════════════════════════════════════════

# 测试步骤
- launchApp
- extendedWaitUntil:
    visible: "某个元素"
    timeout: 30000
    
- takeScreenshot: "step-01-description"
```

### 最佳实践

1. **使用 extendedWaitUntil 而非裸断言**
   ```yaml
   # ✓ 好
   - extendedWaitUntil:
       visible: "AI 工具"
       timeout: 10000
   
   # ✗ 差
   - assertVisible: "AI 工具"
   ```

2. **中英文并列选择器**
   ```yaml
   # ✓ 适配多语言
   - tapOn: "笔记|Notes"
   
   # ✗ 只支持中文
   - tapOn: "笔记"
   ```

3. **合理使用 optional**
   ```yaml
   # 对可能不存在的元素
   - tapOn:
       text: "关闭|Close"
       optional: true
   ```

4. **充分的截图**
   ```yaml
   # 关键步骤都要截图
   - takeScreenshot: "suite-01-after-login"
   - takeScreenshot: "suite-02-note-created"
   - takeScreenshot: "suite-99-final-state"
   ```

---

## 📅 测试计划

### 短期计划（1-2 周）

- [ ] 补充 AI Chat 测试
- [ ] 补充 PKM 知识库测试
- [ ] 补充财务模块测试
- [ ] 提高会议工作台覆盖率至 80%
- [ ] 添加性能基准测试

### 中期计划（1 个月）

- [ ] 补充所有 34 个域的基础测试
- [ ] 建立测试数据管理机制
- [ ] 集成 CI/CD 自动化
- [ ] 建立测试报告仪表板

### 长期计划（3 个月）

- [ ] iOS 平台测试套件
- [ ] HarmonyOS 平台测试套件
- [ ] 性能测试套件
- [ ] 可访问性测试套件
- [ ] 国际化测试套件

---

## 📚 参考资料

- [Maestro 官方文档](https://maestro.mobile.dev/)
- [OpenPocket 功能特性大盘](../docs/2026-09-08-feature-inventory.md)
- [OpenPocket 需求与设计](../docs/2026-09-08-requirements-design.md)
- [现有测试文件](.maestro/)

---

## 🤝 贡献指南

### 添加新测试

1. 在 `.maestro/` 目录创建测试文件
2. 遵循命名和结构规范
3. 在本文档中更新覆盖范围
4. 提交 PR 并附带测试报告

### 报告问题

发现测试问题时，请提供：
- 测试文件名
- 失败步骤
- 错误截图
- 设备信息
- 日志文件

---

**维护者**: OpenPocket Team  
**最后更新**: 2026-11-23
