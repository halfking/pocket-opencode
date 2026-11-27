# 🎯 OpenPocket Phase 1 VAD 测试说明

**重要**: 代码已就绪，APK已安装，仅需15分钟人工验证！

## 快速开始 (3步)

### 第1步: 启动监控 (电脑)

在终端运行:
```bash
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket
./test-vad-manual.sh
```

### 第2步: 操作手机

按脚本提示在手机上操作:
1. 打开 OpenPocket 应用
2. 创建或进入一个会议
3. 点击「开始录音」按钮
4. 对着手机说: "你好，这是VAD延迟测试"
5. 停顿1秒
6. 再说: "第二句话"
7. 点击「停止录音」

### 第3步: 查看结果

脚本会自动分析并显示:
- ✅ VAD初始化是否成功
- ✅ 检测到多少次语音端点
- ✅ 每次端点的时长
- ✅ 完整日志导出路径

## 验收标准

Phase 1成功的标志:
- [ ] 日志显示 "VAD enabled"
- [ ] 首字出现延迟 < 1秒 (手动观察)
- [ ] 两句话分成2个片段 (不是合并成1个)
- [ ] 每个片段时长约0.5-2秒

## 如遇问题

### 问题1: 应用崩溃或无法录音
```bash
# 重启应用
adb shell am force-stop com.kaixuan.opencode.pocket
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

### 问题2: 看到 "VAD initialization failed"
这是预期的回退行为，表示VAD不可用，系统使用固定8秒切片。
检查日志了解失败原因。

### 问题3: 没有任何VAD日志
可能原因:
- 录音功能未触发（未点击按钮）
- 麦克风权限未授予
- 后端服务未配置

解决: 确保在手机上完成完整操作流程。

## 详细测试指南

参见: `docs/PHASE1_AUDIT_AND_TESTING_COMPLETE.md`

## 联系支持

遇到技术问题请提供:
1. 完整日志文件 (脚本会自动导出)
2. 手机型号和Android版本
3. 操作步骤和观察到的现象
