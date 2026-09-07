> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/新架构v1/03-roadmap/里程碑.md`](docs/新架构v1/03-roadmap/里程碑.md)
> Do NOT use this doc for current implementation decisions.
> This doc is a historical plan/analysis from a completed sprint; 规划以 v3 里程碑为准。

# OpenCode Pocket 模拟器测试快速启动指南

## 🚀 快速开始（5 分钟）

### 1. 启动 Backend
```bash
cd backend
./start-dev.sh
```

应该看到:
```
✅ Backend 启动成功 (PID: xxxxx)
✅ 健康检查通过
✅ 登录测试通过
```

### 2. 运行 API 测试
```bash
cd ..
./run-api-tests-simple.sh
```

预期结果:
```
✅ Backend 健康检查: PASS
✅ 登录 API: PASS
✅ Token 验证: PASS
✅ 实例列表: PASS (1 个实例)

通过: 4/4
```

### 3. 手动测试模拟器（如果有）

#### 准备模拟器
```bash
# 设置 adb 路径
export PATH=$PATH:~/Library/Android/sdk/platform-tools

# 检查模拟器
adb devices

# 配置网络
adb reverse tcp:8088 tcp:8088

# 安装 APK
adb install -r android/app/build/outputs/apk/debug/app-debug.apk

# 启动应用
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

#### 测试登录
1. 在模拟器中输入:
   - 用户名: `admin`
   - 密码: `admin`
2. 点击登录
3. 验证跳转到主页

#### 测试实例列表
1. 点击底部导航"实例"标签
2. 应该看到 1 个实例: `demo-main`
3. 状态显示为: `healthy`

---

## 📋 已知问题

### 问题 1: Backend 登录失败
**症状**: `{"error":"invalid credentials"}`

**解决**:
```bash
# 停止 backend
killall pocketd

# 使用启动脚本
cd backend
./start-dev.sh
```

### 问题 2: 任务创建失败 (503)
**症状**: `local task store not configured (remote-only mode)`

**原因**: PostgreSQL 未配置

**影响**: 
- ✅ 登录功能正常
- ✅ 实例列表正常
- ❌ 无法创建本地任务

**解决**: 暂时使用 remote-only 模式，或配置 PostgreSQL

### 问题 3: 会话列表 404
**症状**: API 返回 `404 page not found`

**原因**: API 端点未实现

**状态**: 待开发

---

## 📊 测试状态

**完成的测试** ✅:
- [x] Backend 健康检查
- [x] 登录 API
- [x] Token 验证
- [x] 实例列表 API

**待完成的测试** ⏳:
- [ ] 手动 UI 测试（登录流程）
- [ ] 手动 UI 测试（实例列表）
- [ ] 会话管理 API（需要先实现）

**测试通过率**: 4/4 核心测试 (100%)

---

## 📖 相关文档

- 完整测试计划: `SIMULATOR_TEST_PLAN.md`
- 测试报告: `SIMULATOR_TEST_REPORT.md`
- 测试结果: `test-evidence/20260706-121304/`

---

**最后更新**: 2026-07-06 12:15
