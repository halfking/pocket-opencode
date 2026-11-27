# OpenPocket 综合测试计划与执行方案

**制定日期**: 2026-11-25  
**目标**: 完整测试与验证应用功能，提升覆盖率至 100%  
**方法**: Maestro 移动端自动化测试

---

## 1. 执行摘要

### 1.1 当前状态

- **测试覆盖率**: 68% (23/34 功能域)
- **测试文件数**: 56 个
- **测试套件**: 6 个主套件
- **测试基础设施**: 生产就绪 ✅

### 1.2 测试目标

- **短期目标**: 执行现有测试套件，验证核心功能 (今日完成)
- **中期目标**: 补充缺失的 11 个功能域测试 (1-2 周)
- **长期目标**: 提升 L2/L3 覆盖率至 90%+ (1 个月)

### 1.3 测试策略

```
Phase 1: 环境验证与现有测试执行 (2-3 小时)
  ├─ 环境前置检查
  ├─ 冒烟测试执行
  ├─ 回归测试执行
  └─ 完整测试执行

Phase 2: 功能域缺口分析 (1 小时)
  ├─ 识别未覆盖功能
  ├─ 优先级排序
  └─ 测试设计

Phase 3: 缺口测试补充 (按需)
  ├─ 高优先级功能测试设计
  ├─ 中优先级功能测试设计
  └─ 低优先级功能测试设计

Phase 4: 完整验证报告 (30 分钟)
  ├─ 测试结果汇总
  ├─ 覆盖率分析
  └─ 建议与后续行动
```

---

## 2. 测试环境要求

### 2.1 硬件要求

```
✅ Android 设备或模拟器 (API ≥24)
✅ macOS/Linux 开发机
✅ 稳定网络连接
```

### 2.2 软件要求

```
✅ Maestro CLI (v2.11.0+)
✅ ADB (Android Debug Bridge)
✅ OpenPocket APK (v1.2.0+)
✅ 后端服务 (pocketd)
```

### 2.3 网络要求

```
✅ 端口转发: adb reverse tcp:18099 tcp:8088
✅ 后端可访问: localhost:18099 或 192.168.31.211
✅ 外部服务可达: llm.itestu.cn, pocket.itestu.cn
```

### 2.4 环境变量

```bash
export POCKET_MASTER="12345678"              # 主密码
export POCKET_DEV_PASS="<DEV-PASS-PREFIX>…<masked>"      # 开发环境口令
```

---

## 3. 测试覆盖分析

### 3.1 已覆盖功能域 (23/34, 68%)

#### L0 - 认证层 (1/1, 100%) ✅

| 功能域 | 测试文件 | 覆盖程度 | 状态 |
|--------|----------|----------|------|
| 登录/解锁/主密码 | `_login.yaml`, `_set-master-password.yaml` | 完整 (4 种状态) | ✅ |

#### L1 - 核心功能 (9/11, 82%) 🟡

| 功能域 | 测试文件 | 覆盖程度 | 状态 |
|--------|----------|----------|------|
| AI 工具看板 | `ai-tools-task-board.yaml` | 基础导航 | ✅ |
| 笔记 CRUD | `notes-crud.yaml`, `notes-recording.yaml` | 创建+录音 | ✅ |
| AI Chat | `ai-chat-full-test.yaml` | 多轮对话 | ✅ |
| PKM 知识库 | `pkm-knowledge-test.yaml` | 笔记+链接 | ✅ |
| 消息中心 | `messages-browse.yaml` | 基础浏览 | ✅ |
| 邮箱 | `email-browse.yaml`, `email-detail.yaml` | 列表+详情 | ✅ |
| 导航 | `smoke-test-suite.yaml` | 底部导航 | ✅ |
| 更多菜单 | `more-menu-navigation.yaml` | 完整导航 | ✅ |
| 财务记账 | `finance-accounting-test.yaml` | 浏览+统计 | ✅ |
| **快速提问** | - | ❌ 缺失 | 🔴 |
| **任务创建流程** | - | ❌ 缺失 | 🔴 |

#### L2 - 扩展功能 (5/12, 42%) 🟡

| 功能域 | 测试文件 | 覆盖程度 | 状态 |
|--------|----------|----------|------|
| 会议工作台 | `meeting-workspace.yaml` | 基础访问 | ✅ |
| ACC 任务看板 | `acc-board.yaml` | 看板浏览 | ✅ |
| RSS 订阅 | `rss-browse.yaml` | 基础浏览 | ✅ |
| 闪卡学习 | `flashcards-browse.yaml` | 基础浏览 | ✅ |
| 设置 | `settings-llm-gateway.yaml` | 部分设置 | ✅ |
| **智能体库** | - | ❌ 缺失 | 🔴 |
| **市场** | - | ❌ 缺失 | 🔴 |
| **计划任务** | - | ❌ 缺失 | 🔴 |
| **联系人** | - | ❌ 缺失 | 🔴 |
| **聊天模块** | - | ❌ 缺失 | 🔴 |
| **邮件完整流程** | - | ⚠️ 部分 | 🟡 |
| **会议完整流程** | - | ⚠️ 部分 | 🟡 |

#### L3 - 高级功能 (3/11, 27%) 🟡

| 功能域 | 测试文件 | 覆盖程度 | 状态 |
|--------|----------|----------|------|
| LLM 网关设置 | `settings-llm-gateway.yaml` | 配置页面 | ✅ |
| 密码箱 | `vault-browse.yaml` | 基础浏览 | ✅ |
| 更多菜单 | `more-menu-navigation.yaml` | 完整导航 | ✅ |
| **OpenCode 兼容层** | - | ❌ 缺失 | 🔴 |
| **实例管理** | - | ❌ 缺失 | 🔴 |
| **学习模块高级** | - | ❌ 缺失 | 🔴 |
| **日历** | - | ❌ 缺失 | 🔴 |
| **知识库高级** | - | ❌ 缺失 | 🔴 |
| **ACC 完整流程** | - | ⚠️ 部分 | 🟡 |
| **市场发布** | - | ❌ 缺失 | 🔴 |
| **审计日志** | - | ❌ 缺失 | 🔴 |

### 3.2 功能域缺口清单 (11 个完全缺失)

| 优先级 | 功能域 | API 端点 | 前端路由 | 业务价值 |
|--------|--------|----------|----------|----------|
| **P0** | 快速提问 | `POST /api/tasks` | `/ai` | 高 (核心功能) |
| **P0** | 任务创建流程 | `POST /api/tasks` | `/ai` | 高 (核心功能) |
| **P1** | 智能体库 | `GET /api/agents` | `/agents` | 高 (215+ 角色) |
| **P1** | 市场浏览 | `GET /api/marketplace/*` | `/marketplace/*` | 中 (扩展性) |
| **P1** | 日历 | `GET /api/calendar` | `/calendar` | 中 (生产力) |
| **P2** | 计划任务 | `GET /api/scheduled-tasks` | `/scheduled-tasks` | 中 (自动化) |
| **P2** | 联系人 | `GET /api/contacts` | `/contacts` | 中 (关系管理) |
| **P2** | 聊天模块 | `GET /api/chat` | `/chat` | 低 (沟通) |
| **P3** | OpenCode 兼容层 | `GET /api/opencode/*` | - | 低 (兼容性) |
| **P3** | 实例管理 | `GET /api/instances` | - | 低 (管理) |
| **P3** | 学习模块高级 | `GET /api/learning` | `/study` | 低 (增强) |

---

## 4. Phase 1: 现有测试执行计划

### 4.1 环境前置检查 (10 分钟)

```bash
# 步骤 1: 验证 Maestro 安装
maestro --version
# 预期: v2.11.0+

# 步骤 2: 验证 ADB 连接
adb devices
# 预期: 设备列表非空

# 步骤 3: 验证 APK 安装
adb shell pm list packages | grep pocket
# 预期: com.openpocket.app

# 步骤 4: 配置端口转发
adb reverse tcp:18099 tcp:8088
# 预期: 成功

# 步骤 5: 验证后端服务
curl -s http://localhost:18099/api/health
# 预期: {"status":"ok"}

# 步骤 6: 运行环境验证脚本
./scripts/verify-maestro-env.sh
# 预期: 所有检查通过
```

### 4.2 冒烟测试 (Smoke Test) - 5 分钟

**目标**: 快速验证核心路径可用

```bash
# 执行命令
./scripts/run-maestro-suite.sh smoke

# 或直接运行
maestro test .maestro/smoke-test-suite.yaml \
  --format junit \
  --output test-reports/smoke-test-$(date +%Y%m%d-%H%M%S).xml
```

**验证场景**:
1. ✅ 应用启动
2. ✅ 登录流程 (4 种状态自动处理)
3. ✅ 核心导航 (首页 → 笔记 → 消息 → 更多 → 首页)
4. ✅ 功能入口 (快速提问、笔记创建)

**成功标准**: 100% 通过

### 4.3 回归测试 (Regression Test) - 12 分钟

**目标**: 验证核心业务流程未受破坏

```bash
# 执行命令
./scripts/run-maestro-suite.sh regression

# 或直接运行
maestro test .maestro/regression-test-suite.yaml \
  --format junit \
  --output test-reports/regression-test-$(date +%Y%m%d-%H%M%S).xml
```

**验证场景**:
1. ✅ 笔记完整流程 (创建 → 录音 → 编辑 → 保存 → 查看)
2. ✅ 任务创建流程 (快速提问 → 输入 → 提交 → 验证)
3. ✅ 邮件浏览流程 (列表 → 详情 → 返回)
4. ✅ 闪卡学习流程 (进入闪卡页面)
5. ✅ 消息中心验证

**成功标准**: 100% 通过

### 4.4 完整测试 (Master Test) - 20 分钟

**目标**: 验证所有核心功能

```bash
# 执行命令
./scripts/run-maestro-suite.sh master

# 或直接运行
maestro test .maestro/master-test-suite.yaml \
  --format junit \
  --output test-reports/master-test-$(date +%Y%m%d-%H%M%S).xml
```

**验证场景**:
- **L0 - 启动与认证** (必须 100%)
- **L1 - 核心功能** (目标 95%)
- **L2 - 扩展功能** (目标 90%)
- **L3 - 高级功能** (目标 80%)

**成功标准**: 
- L0: 100%
- L1: ≥ 95%
- L2: ≥ 90%
- L3: ≥ 80%

### 4.5 功能专项测试 (30 分钟)

**新增功能测试**:

```bash
# 1. 财务记账测试
maestro test .maestro/finance-accounting-test.yaml

# 2. AI Chat 测试
maestro test .maestro/ai-chat-full-test.yaml

# 3. PKM 知识库测试
maestro test .maestro/pkm-knowledge-test.yaml
```

**ACC 集成测试** (如果需要):

```bash
# ACC 任务看板
maestro test .maestro/acc-board.yaml

# ACC 相关测试
maestro test .maestro/acc-*.yaml
```

---

## 5. Phase 2: 缺口测试设计

### 5.1 P0 高优先级缺口 (2 个)

#### 5.1.1 快速提问流程

**测试文件**: `.maestro/quick-question-flow.yaml` (待创建)

**测试场景**:
```yaml
appId: com.openpocket.app
---
# 快速提问完整流程测试
- runFlow: _login.yaml

# 1. 进入 AI 工具页面
- tapOn: "^首页$|^AI$"
- extendedWaitUntil:
    visible: "AI 工具"
    timeout: 10000

# 2. 点击快速提问按钮
- tapOn: "快速提问|Quick Question"
- extendedWaitUntil:
    visible: "输入|Input"
    timeout: 5000

# 3. 输入问题
- tapOn: "text: 输入你的问题"
- inputText: "请帮我分析一下当前项目的技术栈"
- takeScreenshot: quick-question-01-input

# 4. 提交
- tapOn: "发送|Send"
- extendedWaitUntil:
    visible: "处理中|Processing"
    timeout: 5000
- takeScreenshot: quick-question-02-processing

# 5. 验证任务创建
- wait: 3000
- assertVisible: "任务|Task"
- takeScreenshot: quick-question-03-task-created
```

#### 5.1.2 任务创建与查看

**测试文件**: `.maestro/task-creation-flow.yaml` (待创建)

**测试场景**:
```yaml
appId: com.openpocket.app
---
# 任务创建与查看完整流程
- runFlow: _login.yaml

# 1. 创建任务
- tapOn: "^首页$|^AI$"
- tapOn: "创建任务|Create Task"
- inputText: "重构用户认证模块"
- tapOn: "确认|Confirm"
- takeScreenshot: task-01-created

# 2. 查看任务列表
- tapOn: "任务列表|Task List"
- extendedWaitUntil:
    visible: "重构用户认证模块"
    timeout: 5000
- takeScreenshot: task-02-list

# 3. 查看任务详情
- tapOn: "重构用户认证模块"
- extendedWaitUntil:
    visible: "任务详情|Task Detail"
    timeout: 5000
- assertVisible: "状态|Status"
- assertVisible: "创建时间|Created"
- takeScreenshot: task-03-detail
```

### 5.2 P1 中高优先级缺口 (3 个)

#### 5.2.1 智能体库浏览

**测试文件**: `.maestro/agents-library-browse.yaml` (待创建)

**测试场景**:
```yaml
appId: com.openpocket.app
---
# 智能体库浏览测试
- runFlow: _login.yaml

# 1. 进入智能体库
- tapOn: "更多|More"
- tapOn: "智能体库|Agents"
- extendedWaitUntil:
    visible: "智能体|Agents"
    timeout: 10000
- takeScreenshot: agents-01-list

# 2. 验证智能体数量
- assertVisible: ".*专家|.*Expert"  # 215+ 角色
- takeScreenshot: agents-02-roles

# 3. 查看智能体详情
- tapOn:
    point: "50%,30%"  # 点击第一个智能体
- wait: 2000
- assertVisible: "描述|Description"
- takeScreenshot: agents-03-detail

# 4. 返回
- back
- assertVisible: "智能体|Agents"
```

#### 5.2.2 市场浏览

**测试文件**: `.maestro/marketplace-browse.yaml` (待创建)

**测试场景**:
```yaml
appId: com.openpocket.app
---
# 市场浏览测试
- runFlow: _login.yaml

# 1. 进入市场
- tapOn: "更多|More"
- tapOn: "市场|Marketplace"
- extendedWaitUntil:
    visible: "技能|Skills|智能体|Agents"
    timeout: 10000
- takeScreenshot: marketplace-01-home

# 2. 浏览技能市场
- tapOn: "技能|Skills"
- wait: 2000
- assertVisible: "技能列表|Skill List"
- takeScreenshot: marketplace-02-skills

# 3. 浏览智能体市场
- tapOn: "智能体|Agents"
- wait: 2000
- assertVisible: "智能体列表|Agent List"
- takeScreenshot: marketplace-03-agents
```

#### 5.2.3 日历视图

**测试文件**: `.maestro/calendar-view.yaml` (待创建)

**测试场景**:
```yaml
appId: com.openpocket.app
---
# 日历视图测试
- runFlow: _login.yaml

# 1. 进入日历
- tapOn: "更多|More"
- tapOn: "日历|Calendar"
- extendedWaitUntil:
    visible: "日历|Calendar"
    timeout: 10000
- takeScreenshot: calendar-01-month-view

# 2. 验证事件显示
- assertVisible: "事件|Events"
- assertVisible: "任务|Tasks"
- takeScreenshot: calendar-02-events

# 3. 切换视图
- tapOn: "周|Week"
- wait: 2000
- takeScreenshot: calendar-03-week-view

# 4. 切换回月视图
- tapOn: "月|Month"
- wait: 2000
- takeScreenshot: calendar-04-month-view
```

### 5.3 P2 中优先级缺口 (3 个)

#### 5.3.1 计划任务

**测试文件**: `.maestro/scheduled-tasks.yaml` (待创建)

#### 5.3.2 联系人

**测试文件**: `.maestro/contacts-browse.yaml` (待创建)

#### 5.3.3 聊天模块

**测试文件**: `.maestro/chat-module.yaml` (待创建)

### 5.4 P3 低优先级缺口 (3 个)

- OpenCode 兼容层
- 实例管理
- 学习模块高级功能

**决策**: Phase 1 完成后根据时间和优先级决定是否实施

---

## 6. Phase 3: 测试执行计划

### 6.1 执行顺序

```
1. 环境验证 (10 分钟)
   └─ verify-maestro-env.sh

2. 核心测试套件 (45 分钟)
   ├─ smoke-test-suite (5 分钟)
   ├─ regression-test-suite (12 分钟)
   ├─ master-test-suite (20 分钟)
   └─ 功能专项测试 (8 分钟)

3. 缺口测试 (按需，1-3 小时)
   ├─ P0: 快速提问 + 任务创建 (20 分钟)
   ├─ P1: 智能体库 + 市场 + 日历 (30 分钟)
   └─ P2/P3: 其他缺口 (可选)

4. 结果汇总 (30 分钟)
   └─ 生成验证报告
```

### 6.2 执行脚本

**一键执行所有现有测试**:

```bash
#!/bin/bash
# comprehensive-test-run.sh

set -e

echo "=========================================="
echo "OpenPocket 综合测试执行"
echo "开始时间: $(date)"
echo "=========================================="

# 1. 环境验证
echo "[1/4] 环境验证..."
./scripts/verify-maestro-env.sh

# 2. 配置环境变量
export POCKET_MASTER="12345678"
export POCKET_DEV_PASS="<DEV-PASS-PREFIX>…<masked>"

# 3. 冒烟测试
echo "[2/4] 冒烟测试 (5 分钟)..."
./scripts/run-maestro-suite.sh smoke

# 4. 回归测试
echo "[3/4] 回归测试 (12 分钟)..."
./scripts/run-maestro-suite.sh regression

# 5. 完整测试
echo "[4/4] 完整测试 (20 分钟)..."
./scripts/run-maestro-suite.sh master

# 6. 生成报告
echo "生成测试报告..."
./scripts/generate-maestro-report.sh

echo "=========================================="
echo "测试完成时间: $(date)"
echo "=========================================="
echo "查看报告:"
echo "  文本: cat test-reports/test-summary-*.txt"
echo "  HTML: open test-reports/test-report-*.html"
echo "  截图: open logs/maestro/"
echo "=========================================="
```

---

## 7. Phase 4: 验证报告模板

### 7.1 报告结构

```markdown
# OpenPocket 测试验证报告

**执行日期**: YYYY-MM-DD
**执行人员**: [姓名]
**测试环境**: [设备型号/Android 版本]
**应用版本**: [APK 版本]

## 1. 执行摘要

- 测试套件执行数: X
- 测试用例总数: Y
- 通过数: Z
- 失败数: N
- 通过率: P%

## 2. 测试结果详情

### 2.1 冒烟测试
- 状态: ✅ 通过 / ❌ 失败
- 执行时间: X 分钟
- 通过率: 100%

### 2.2 回归测试
- 状态: ✅ 通过 / ❌ 失败
- 执行时间: X 分钟
- 通过率: Y%

### 2.3 完整测试
- 状态: ✅ 通过 / ❌ 失败
- 执行时间: X 分钟
- 分层通过率:
  - L0: 100%
  - L1: X%
  - L2: Y%
  - L3: Z%

## 3. 覆盖率分析

| 层级 | 覆盖域 | 总域数 | 覆盖率 |
|------|--------|--------|--------|
| L0 | 1 | 1 | 100% |
| L1 | X | 11 | Y% |
| L2 | X | 12 | Y% |
| L3 | X | 11 | Y% |
| **总计** | **X** | **34** | **Y%** |

## 4. 失败用例分析

[如有失败，列出失败原因和截图]

## 5. 性能指标

- 应用启动时间: X 秒
- 登录响应时间: X 秒
- 页面切换延迟: X 毫秒

## 6. 建议与后续行动

- [ ] 修复失败用例
- [ ] 补充缺失测试
- [ ] 提升 L2/L3 覆盖率
- [ ] 集成到 CI/CD

## 7. 附件

- 测试日志: logs/maestro/
- 测试截图: logs/maestro/
- JUnit 报告: test-reports/*.xml
```

---

## 8. 成功标准

### 8.1 Phase 1 成功标准

✅ **环境验证**: 所有前置检查通过  
✅ **冒烟测试**: 100% 通过  
✅ **回归测试**: 100% 通过  
✅ **完整测试**: L0 100%, L1 ≥95%, L2 ≥90%, L3 ≥80%

### 8.2 Phase 2 成功标准

✅ **缺口识别**: 11 个功能域明确识别  
✅ **优先级排序**: P0/P1/P2/P3 分类完成  
✅ **测试设计**: P0/P1 测试用例设计完成

### 8.3 整体成功标准

✅ **测试执行**: 所有现有测试套件执行完成  
✅ **报告生成**: 完整验证报告生成  
✅ **覆盖率分析**: 明确当前覆盖率和缺口  
✅ **后续计划**: 提供清晰的改进路线图

---

## 9. 风险与缓解

### 9.1 风险识别

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|----------|
| 设备连接失败 | 中 | 高 | 提前验证 ADB 连接 |
| 后端服务不可用 | 中 | 高 | 验证服务健康状态 |
| 网络超时 | 低 | 中 | 增加超时阈值 |
| 测试环境不一致 | 低 | 中 | 使用标准化环境 |
| 测试数据污染 | 中 | 中 | 测试前清理数据 |

### 9.2 回退计划

如果自动化测试遇到阻塞问题:
1. 记录失败原因和环境状态
2. 执行手工验证核心流程
3. 生成部分验证报告
4. 标注自动化测试问题

---

## 10. 时间估算

| 阶段 | 活动 | 预计时间 |
|------|------|----------|
| **Phase 1** | 环境验证 | 10 分钟 |
| | 冒烟测试 | 5 分钟 |
| | 回归测试 | 12 分钟 |
| | 完整测试 | 20 分钟 |
| | 功能专项测试 | 8 分钟 |
| | **Phase 1 小计** | **55 分钟** |
| **Phase 2** | 缺口分析 | 30 分钟 |
| | P0 测试设计 | 30 分钟 |
| | **Phase 2 小计** | **1 小时** |
| **Phase 3** | P0 测试执行 | 20 分钟 |
| | P1 测试执行 | 30 分钟 |
| | **Phase 3 小计** | **50 分钟** |
| **Phase 4** | 结果汇总 | 15 分钟 |
| | 报告生成 | 15 分钟 |
| | **Phase 4 小计** | **30 分钟** |
| **总计** | | **3 小时 15 分钟** |

**说明**: 
- Phase 1 是**必须**完成的核心验证
- Phase 2-3 可根据时间和需求调整
- 建议优先完成 Phase 1, 确保核心功能验证

---

## 11. 下一步行动

### 11.1 立即执行 (今日)

```bash
# 1. 验证环境
./scripts/verify-maestro-env.sh

# 2. 执行核心测试套件
./scripts/run-maestro-suite.sh smoke
./scripts/run-maestro-suite.sh regression
./scripts/run-maestro-suite.sh master

# 3. 生成初步报告
./scripts/generate-maestro-report.sh
```

### 11.2 短期任务 (1-2 周)

- [ ] 创建 P0 缺口测试文件
- [ ] 执行 P0 测试并验证
- [ ] 创建 P1 缺口测试文件
- [ ] 执行 P1 测试并验证
- [ ] 更新覆盖率报告

### 11.3 中期目标 (1 个月)

- [ ] 补充所有 11 个功能域测试
- [ ] 提升 L2 覆盖率至 80%+
- [ ] 提升 L3 覆盖率至 60%+
- [ ] 集成到 CI/CD pipeline

---

**文档维护者**: AI Assistant  
**制定日期**: 2026-11-25  
**版本**: 1.0
