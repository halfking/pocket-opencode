# Phase 1 VAD 测试执行清单

## ✅ 已完成（自动化验证）

- [x] 设备连接验证 (4c308e2e)
- [x] 应用安装确认 (v1.2.0-openpocket)
- [x] VAD模型文件存在确认 (silero_vad.onnx 2.2MB)
- [x] 代码实现审计通过 (VadManager + MeetingRecordService)
- [x] 测试框架准备完成
- [x] 测试文档创建完成

## 📋 待执行（需要人工操作）

### 方式A: 使用实时监控脚本（推荐）

**步骤1**: 在终端启动监控脚本
```bash
chmod +x /tmp/vad-live-test-monitor.sh
/tmp/vad-live-test-monitor.sh
```

**步骤2**: 按照屏幕提示在设备上操作
1. 打开 OpenPocket 应用
2. 进入【会议】功能
3. 点击【开始录音】按钮
4. 执行以下测试：
   - 立即说话："你好，这是测试"（Test 2: 首字延迟）
   - 说3句话，每句间隔1秒（Test 3: 动态分段）
   - 连续说话10秒以上（Test 4: 兜底机制）

**步骤3**: 查看监控脚本自动输出的测试结果

### 方式B: 手动逐步测试

参考文档：`TESTING_QUICK_START.md`

## 🎯 验收标准

根据 manual-test-guide.md，必须满足：

- [ ] **Test 1**: VAD初始化成功（日志显示 `VAD enabled`）
- [ ] **Test 2**: 首字延迟 < 1秒（实际测量）
- [ ] **Test 3**: 动态端点检测工作（3句话分3段）
- [ ] **Test 4**: 10秒兜底机制正常（日志显示 `VAD max duration reached`）
- [ ] **Test 5**: 无崩溃、无ANR

## 📊 测试结果记录

完成测试后，请填写：

| 测试项 | 状态 | 实测值/备注 |
|--------|------|-------------|
| Test 1: VAD初始化 | ⬜ 通过 / ⬜ 失败 | |
| Test 2: 首字延迟 | ⬜ 通过 / ⬜ 失败 | ____ 秒 |
| Test 3: 动态分段 | ⬜ 通过 / ⬜ 失败 | ____ 个分段 |
| Test 4: 兜底机制 | ⬜ 通过 / ⬜ 失败 | |
| Test 5: 稳定性 | ⬜ 通过 / ⬜ 失败 | |

## 📁 相关文件

- **实时监控脚本**: `/tmp/vad-live-test-monitor.sh`
- **快速测试指南**: `TESTING_QUICK_START.md`
- **完整验证报告**: `PHASE1_VAD_DEPLOYMENT_VERIFICATION.md`
- **执行总结**: `DEPLOYMENT_VERIFICATION_SUMMARY.md`
- **原始测试计划**: `.maestro/manual-test-guide.md`

---

**状态**: 等待人工执行设备上的交互测试
**下一步**: 运行监控脚本 → 在设备上操作 → 记录结果
