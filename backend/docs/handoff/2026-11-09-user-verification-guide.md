# Cicada 集成 Phase A - 用户验证指南

**状态**: 代码已完成，需要用户在实际环境中验证  
**日期**: 2026-11-09

---

## ✅ 已完成的工作

### 代码实施
- ✅ 910+ 行代码已创建
- ✅ TypeScript 编译通过
- ✅ Go 编译通过
- ✅ 单元测试通过 (23/23)
- ✅ 服务器运行中

### 服务器状态
- ✅ 后端: http://localhost:8088 (PID 11805)
- ✅ 前端: http://localhost:4174 (PID 7420)

---

## ⚠️ 需要用户验证的项目

### 1. 前端 UI 验证（必需）

**步骤**:
1. 在浏览器打开: http://localhost:4174
2. 检查控制台是否有错误
3. 检查页面是否正常加载

**预期结果**:
- ✓ 页面正常加载，无 JavaScript 错误
- ✓ Vue DevTools 可以看到 `useLiveSubtitleStore`
- ✓ 组件树中存在 `LiveSubtitleOverlay`（可能不可见，因为需要会议触发）

**如果失败**: 
- 查看浏览器控制台错误
- 检查 `/tmp/frontend.log`
- 报告错误信息

---

### 2. 实时字幕功能验证（重要）

**步骤**:
1. 在前端创建或进入一个会议
2. 开始会议录音
3. 观察是否出现字幕悬浮窗

**预期结果**:
- ✓ 开始录音后，字幕悬浮窗自动显示
- ✓ 转写文本实时出现在悬浮窗中
- ✓ 可以拖拽悬浮窗改变位置
- ✓ 可以调整字号（小/中/大）
- ✓ 可以置顶悬浮窗

**如果失败**:
- 检查 `meetingRecorderRuntime` 是否正常工作
- 检查浏览器控制台错误
- 确认 `initLiveSubtitleIntegration()` 已调用

---

### 3. 翻译功能验证（重要）

**前提**: 需要配置 LLM API key

**配置步骤**:
```bash
export POCKET_LLM_GATEWAY_API_KEY="your-api-key"
export POCKET_LLM_GATEWAY_BASE_URL="https://api.openai.com/v1"
# 重启后端
pkill -f pocketd
cd backend && POCKET_HTTP_PORT=8088 POCKET_DEV_AUTH=true \
  POCKET_AUTH_LEGACY_ONLY=true ./pocketd &
```

**测试步骤**:
1. 在字幕悬浮窗中点击"翻译"按钮
2. 观察已有字幕是否出现翻译

**预期结果**:
- ✓ 点击翻译按钮后，字幕下方出现翻译文本
- ✓ 新的字幕片段自动翻译（如果翻译已开启）
- ✓ 翻译延迟 < 3 秒
- ✓ 翻译结果准确

**如果失败**:
- 检查 API key 是否正确
- 查看后端日志 `/tmp/pocketd_run.log`
- 检查网络连接

---

### 4. 跨平台测试（可选，任选一个平台）

#### Android 测试

**构建步骤**:
```bash
cd frontend
npm run build
npx cap sync android
cd android
./gradlew assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

**测试步骤**:
1. 在 Android 设备上打开应用
2. 创建会议并开始录音
3. 观察字幕悬浮窗是否正常显示
4. 测试翻译功能

**预期结果**:
- ✓ 字幕悬浮窗在 Android 上正常显示
- ✓ 可以拖拽和交互
- ✓ 翻译功能正常工作

#### iOS 测试

**构建步骤**:
```bash
cd frontend
npm run build
npx cap sync ios
open ios/App/App.xcworkspace
# 在 Xcode 中构建并运行
```

**测试步骤**:
同 Android

#### macOS/Web 测试

**测试步骤**:
1. 在 macOS 浏览器（Chrome/Safari）中打开 http://localhost:4174
2. 执行前端 UI 验证和翻译功能验证

---

## 📋 验证清单

请完成以下验证并记录结果：

### 基础验证
- [ ] 前端页面正常加载 (http://localhost:4174)
- [ ] 浏览器控制台无错误
- [ ] LiveSubtitleOverlay 组件存在于组件树

### 功能验证
- [ ] 会议录音触发字幕悬浮窗显示
- [ ] 转写文本实时显示
- [ ] 拖拽悬浮窗功能正常
- [ ] 字号调整功能正常
- [ ] 置顶功能正常

### 翻译验证
- [ ] 配置 LLM API key
- [ ] 点击翻译按钮生效
- [ ] 翻译结果正确显示
- [ ] 新字幕自动翻译（开启后）

### 平台验证（任选一个）
- [ ] Android: 构建成功
- [ ] Android: 字幕功能正常
- [ ] Android: 翻译功能正常
- **或**
- [ ] iOS: 构建成功
- [ ] iOS: 字幕功能正常
- [ ] iOS: 翻译功能正常
- **或**
- [ ] macOS/Web: 字幕功能正常
- [ ] macOS/Web: 翻译功能正常

---

## 🐛 常见问题排查

### 问题 1: 字幕悬浮窗不显示

**检查**:
1. `console.log` 是否有 "LiveSubtitleOverlay" 相关错误
2. `store.visible` 是否为 true
3. `activeMeetingId` 是否有值

**解决**:
```javascript
// 在浏览器控制台手动触发
const store = window.$pinia?.state.value.liveSubtitle
if (store) {
  store.visible = true
  store.addSegment({
    meetingId: 'test',
    speaker: '测试',
    text: 'Hello, this is a test',
    timestamp: Date.now()
  })
}
```

### 问题 2: 翻译 API 返回 401

**原因**: 需要有效的认证 token

**解决**: 在开发模式下，可以临时修改后端跳过认证（仅用于测试）

### 问题 3: 前端编译错误

**检查**:
```bash
cd frontend
npm install
npm run typecheck
```

**解决**: 根据错误信息修复类型问题

---

## 📝 验证报告模板

完成验证后，请填写以下报告：

```
## Cicada Phase A 用户验证报告

日期: ____
测试人: ____

### 1. 前端 UI 验证
- 页面加载: [ ] 成功 / [ ] 失败
- 控制台错误: [ ] 无 / [ ] 有（附错误信息）
- 组件存在: [ ] 是 / [ ] 否

### 2. 字幕功能验证
- 悬浮窗显示: [ ] 正常 / [ ] 异常
- 实时更新: [ ] 正常 / [ ] 异常
- 拖拽功能: [ ] 正常 / [ ] 异常
- 字号调整: [ ] 正常 / [ ] 异常

### 3. 翻译功能验证
- API配置: [ ] 已配置 / [ ] 未配置
- 翻译按钮: [ ] 正常 / [ ] 异常
- 翻译结果: [ ] 正确 / [ ] 错误
- 自动翻译: [ ] 正常 / [ ] 异常

### 4. 平台测试
测试平台: [ ] Android / [ ] iOS / [ ] macOS/Web
- 构建: [ ] 成功 / [ ] 失败
- 字幕: [ ] 正常 / [ ] 异常
- 翻译: [ ] 正常 / [ ] 异常

### 问题记录
（如有问题，请详细描述）

### 截图
（如可能，请附上功能截图）

### 总体评价
[ ] 完全通过 / [ ] 部分通过 / [ ] 未通过

### 建议
（任何改进建议）
```

---

## 🎯 完成标准

Phase A 被认为完全完成，当且仅当：

1. ✅ 前端页面正常加载，无错误
2. ✅ 字幕悬浮窗在会议录音时正常显示
3. ✅ 翻译功能正常工作（至少在一个平台上）
4. ✅ 至少在一个平台（Android/iOS/macOS）上测试通过

---

**当前状态**: 代码完成，等待用户在实际环境验证

请按照上述步骤进行验证，并填写验证报告。如遇到问题，请提供详细错误信息以便调试。
