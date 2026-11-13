# Cicada Phase A 真机测试快速开始

> **目标**: 在真机上快速部署和测试 Cicada 实时字幕与翻译功能  
> **预计时间**: 15-30 分钟  
> **更新时间**: 2026-01-09

---

## 🚀 快速开始（3 步）

### 1️⃣ 连接设备

```bash
# 通过 USB 连接真机，并在手机上：
# - 启用"开发者选项"
# - 打开"USB 调试"
# - 授权此计算机调试

# 验证连接
adb devices
# 应该看到类似输出：
# 4c308e2e    device
```

### 2️⃣ 启动后端服务（如果未运行）

```bash
# 打开新终端窗口
cd /Users/xutaohuang/workspace/ai-native-tools/ai-platform/memora
./pocketd

# 保持此终端运行
```

### 3️⃣ 运行部署测试脚本

```bash
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket
./scripts/deploy-and-test-cicada.sh
```

脚本会自动完成：
- ✅ 检查环境和工具
- ✅ 检测连接的设备
- ✅ 验证后端服务
- ✅ 构建或使用现有 APK
- ✅ 安装到设备
- ✅ 启动应用
- ✅ 开始日志监控
- ✅ 可选运行 Maestro 自动化测试

---

## 📋 手动测试检查清单

脚本运行完成后，在设备上手动测试以下功能：

### ✅ 基础功能（5 分钟）
- [ ] 登录应用
- [ ] 进入"会议"页面
- [ ] 开始录音
- [ ] **字幕悬浮窗是否出现？**
- [ ] 说话，字幕是否实时显示？

### ✅ 交互功能（5 分钟）
- [ ] 拖动悬浮窗到不同位置
- [ ] 调整字号（小/中/大）
- [ ] 悬浮窗是否始终在最前面？

### ✅ 翻译功能（10 分钟）
- [ ] 打开翻译开关
- [ ] 说中文，观察英文翻译
- [ ] 说英文，观察中文翻译
- [ ] 翻译延迟是否 < 2 秒？

### ✅ 导出功能（5 分钟）
- [ ] 停止录音
- [ ] 导出 SRT 格式
- [ ] 导出 TXT 格式
- [ ] 文件内容是否正确？

### ✅ 稳定性测试（10 分钟）
- [ ] 连续录音 10 分钟
- [ ] 切换到其他应用
- [ ] 旋转屏幕
- [ ] 应用是否稳定运行？

---

## 📝 测试报告

完成测试后，更新以下文件：

```bash
# 1. 更新测试报告
vim docs/handoff/2026-11-09-cicada-device-testing-report.md

# 2. 填写测试项目的复选框
# 3. 记录发现的问题
# 4. 添加性能数据
```

---

## 🐛 常见问题

### Q1: `adb: device not found`
**解决方案**:
```bash
# 1. 检查 USB 连接
adb devices

# 2. 重启 adb 服务
adb kill-server
adb start-server

# 3. 在手机上重新授权
```

### Q2: 后端服务未响应
**解决方案**:
```bash
# 1. 检查进程
ps aux | grep pocketd

# 2. 检查端口
lsof -i :8080

# 3. 重启服务
cd /Users/xutaohuang/workspace/ai-native-tools/ai-platform/memora
./pocketd
```

### Q3: 字幕悬浮窗不显示
**排查步骤**:
```bash
# 1. 查看实时日志
adb logcat | grep -E "LiveSubtitle|Cicada"

# 2. 检查组件是否集成
grep -r "LiveSubtitleOverlay" frontend/src/App.vue

# 3. 验证录音状态
# 在应用中检查录音是否真正开始
```

### Q4: 翻译不工作
**排查步骤**:
```bash
# 1. 测试后端 API
curl -X POST http://localhost:8080/api/meetings/translate \
  -H "Content-Type: application/json" \
  -d '{"text":"你好","targetLang":"en"}'

# 2. 检查网络请求
adb logcat | grep "translate"

# 3. 验证路由注册
# 查看后端日志是否注册了 /api/meetings/translate 路由
```

---

## 📚 相关文档

- **详细测试指南**: [docs/testing/cicada-phase-a-manual-test-guide.md](../testing/cicada-phase-a-manual-test-guide.md)
- **集成报告**: [docs/handoff/2026-11-09-cicada-integration-phase-a.md](../handoff/2026-11-09-cicada-integration-phase-a.md)
- **测试报告**: [docs/handoff/2026-11-09-cicada-device-testing-report.md](../handoff/2026-11-09-cicada-device-testing-report.md)
- **设计文档**: [docs/design/2026-11-09-cicada-inspired-meeting-enhancement.md](../design/2026-11-09-cicada-inspired-meeting-enhancement.md)

---

## 🔧 高级选项

### 仅安装（不构建）
```bash
adb install -r frontend/android/app/build/outputs/apk/debug/app-debug.apk
```

### 仅构建（不安装）
```bash
cd frontend
npm run build:fast
npx cap sync android
cd android
./gradlew assembleDebug
```

### 运行 Maestro 测试
```bash
# 使用项目脚本（推荐）
node scripts/maestro-run.mjs .maestro/cicada-phase-a-live-subtitle.yaml

# 或直接使用 Maestro
maestro test .maestro/cicada-phase-a-live-subtitle.yaml
```

### 查看实时日志
```bash
# 所有日志
adb logcat

# 只看 Cicada 相关
adb logcat | grep -E "SystemAudio|LiveSubtitle|Cicada"

# 保存到文件
adb logcat > cicada-test-$(date +%Y%m%d-%H%M%S).log
```

### 性能监控
```bash
# 内存占用
adb shell dumpsys meminfo com.kaixuan.opencode.pocket | grep TOTAL

# CPU 占用
adb shell top -n 1 | grep pocket

# 电池状态
adb shell dumpsys battery
```

---

## ✅ 成功标准

Phase A 测试通过的标准：

1. **功能完整性**: 9/9 测试项通过
2. **性能指标**:
   - 转写延迟 < 1s
   - 翻译延迟 < 2s
   - 内存占用 < 500MB
   - CPU 占用 < 30%
3. **稳定性**: 10 分钟录音无崩溃
4. **用户体验**: 悬浮窗流畅，拖拽响应快

---

## 📞 支持

如果遇到问题：
1. 查看日志文件（脚本会生成）
2. 参考详细测试指南
3. 检查已知问题列表

**测试愉快！** 🎉
