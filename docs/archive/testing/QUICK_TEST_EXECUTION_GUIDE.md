# OpenPocket Maestro 测试执行指南

**状态**: ⚠️ 等待设备连接  
**日期**: 2026-11-25

---

## 当前情况

### ✅ 已完成工作

1. **应用能力全面分析** - 120+ API端点, 25+路由, 10+系统集成
2. **测试覆盖深度评估** - 68%覆盖率, 23/34功能域, 11个缺口已识别
3. **完整测试计划制定** - 4阶段执行计划, P0/P1测试用例已设计
4. **4份详细文档生成**:
   - `OPENPOCKET_CAPABILITIES_SUMMARY.md` (13,000+字)
   - `COMPREHENSIVE_TEST_PLAN_2026-11-25.md` (11,000+字)
   - `OPENPOCKET_TEST_VERIFICATION_REPORT_2026-11-25.md` (20,000+字)
   - `TESTING_COMPLETION_SUMMARY.md`

### ⚠️ 阻塞因素

**Android 设备未连接** - 多次尝试启动模拟器失败，需要手动干预。

---

## 立即执行测试（需要您手动操作）

### 选项 1: 连接物理设备（推荐）

```bash
# 1. 在 Android 设备上启用 USB 调试
# 2. 用 USB 线连接设备到电脑
# 3. 验证连接
adb devices
# 预期: 显示设备列表

# 4. 执行测试（15分钟快速验证）
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket

# 配置环境
export POCKET_MASTER="12345678"
export POCKET_DEV_PASS="Veritrans&9527"
adb reverse tcp:18099 tcp:8088

# 运行测试
./scripts/run-maestro-suite.sh smoke      # 5分钟
./scripts/run-maestro-suite.sh regression # 12分钟

# 查看结果
cat test-reports/test-summary-*.txt
open logs/maestro/
```

### 选项 2: 手动启动模拟器

```bash
# 1. 在 Android Studio 中打开 Device Manager
# 2. 启动任一模拟器（推荐: pocket_test, pocket_fresh）
# 3. 等待模拟器完全启动（可能需要1-2分钟）

# 4. 验证设备
adb devices
# 预期: 显示 emulator-5554 或类似

# 5. 执行测试（同上）
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket
export POCKET_MASTER="12345678"
export POCKET_DEV_PASS="Veritrans&9527"
adb reverse tcp:18099 tcp:8088
./scripts/run-maestro-suite.sh smoke
./scripts/run-maestro-suite.sh regression
```

### 选项 3: 命令行启动模拟器（如果选项2不可用）

```bash
# 1. 设置环境变量并启动
export ANDROID_SDK_ROOT=/Users/xutaohuang/Library/Android/sdk
export ANDROID_HOME=/Users/xutaohuang/Library/Android/sdk

# 2. 启动模拟器（选择一个）
$ANDROID_SDK_ROOT/emulator/emulator -avd pocket_test -no-snapshot-load &

# 3. 等待启动完成（1-2分钟）
adb wait-for-device
sleep 20

# 4. 验证并执行测试
adb devices
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket
export POCKET_MASTER="12345678"
export POCKET_DEV_PASS="Veritrans&9527"
adb reverse tcp:18099 tcp:8088
./scripts/run-maestro-suite.sh smoke
```

---

## 测试执行后查看结果

```bash
# 文本摘要
cat test-reports/test-summary-*.txt

# HTML 报告
open test-reports/test-report-*.html

# 截图（每个关键步骤）
open logs/maestro/

# JUnit XML（CI/CD 集成）
cat test-reports/*.xml
```

---

## 预期测试结果

### Smoke Test (5分钟)
- **验证**: 应用启动 → 登录 → 核心导航 → 功能入口
- **通过标准**: 100%

### Regression Test (12分钟)
- **验证**: 笔记流程 → 任务流程 → 邮件流程 → 闪卡 → 消息
- **通过标准**: 100%

### Master Test (20分钟，可选)
- **验证**: L0-L3 全功能层
- **通过标准**: L0 100%, L1≥95%, L2≥90%, L3≥80%

---

## 故障排查

| 问题 | 解决方案 |
|------|----------|
| `No devices found` | 检查 USB 连接，重启 adb: `adb kill-server && adb start-server` |
| `Connection refused` | 检查后端服务: `curl http://localhost:18099/api/health` |
| `Timeout waiting` | 增加超时或检查应用是否崩溃 |
| 模拟器启动慢 | 使用 `-no-snapshot-load` 或等待更长时间 |
| APK 未安装 | `adb install -r /path/to/app.apk` |

---

## 技术说明

### 为何无法自动启动模拟器

尝试了多种方法但遇到技术限制：
1. **直接 emulator 命令**: 需要 ANDROID_SDK_ROOT 环境变量
2. **MCP 插件**: 启动超时（可能需要 GUI 环境）
3. **后台启动**: 进程启动但设备未注册到 adb

**结论**: 在当前自动化环境中，模拟器启动需要手动干预或 GUI 环境支持。

---

## 文档参考

- **应用能力总结**: `OPENPOCKET_CAPABILITIES_SUMMARY.md`
- **完整测试计划**: `COMPREHENSIVE_TEST_PLAN_2026-11-25.md`
- **验证报告**: `OPENPOCKET_TEST_VERIFICATION_REPORT_2026-11-25.md`
- **完成总结**: `TESTING_COMPLETION_SUMMARY.md`

---

**制作**: AI Assistant  
**日期**: 2026-11-25  
**状态**: 分析完成，等待设备执行测试
