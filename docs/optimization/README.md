# OpenPocket 实时转写系统优化文档

本目录包含 OpenPocket 实时转写系统的全局优化方案及实施记录。

## 文档索引

### 规划文档

- **[SUMMARY-OPTIMIZATION-PLAN.md](./SUMMARY-OPTIMIZATION-PLAN.md)** - 三期优化总体方案
  - Phase 1: 延迟优化（VAD动态切片）
  - Phase 2: 准确度提升（Paraformer + 说话人分离）
  - Phase 3: 交互体验（三层UI + 编辑 + 导出）

### 实施文档

- **[IMPLEMENTATION-PROGRESS.md](./IMPLEMENTATION-PROGRESS.md)** - 实施进度跟踪
  - 当前状态: Phase 1 已完成
  - 下一步: 真机验证 → Phase 2 启动

- **[PHASE1-VAD-IMPLEMENTATION.md](./PHASE1-VAD-IMPLEMENTATION.md)** - Phase 1 实施报告
  - VAD模型集成
  - 动态切片改造
  - 降级保障机制
  - 测试验证计划

- **[PHASE1-AUDIT-REPORT.md](./PHASE1-AUDIT-REPORT.md)** - Phase 1 审计报告
  - 代码完整性审计：28/28 检查点通过
  - 技术实现验证：配置/逻辑/降级全部合格
  - 待验证项：真机性能测量

## 快速开始

### 查看优化目标

```bash
cat SUMMARY-OPTIMIZATION-PLAN.md | grep -A 10 "## 优化目标"
```

**核心指标**:
- 首字延迟: 8-10秒 → **<1秒** (8-10倍)
- 字准率: 85% → **95%+** (+10%)
- 说话人识别: 0% → **90%+** (从无到有)

### Phase 1 真机测试

```bash
# 1. 构建APK
cd frontend/android
./gradlew assembleDebug

# 2. 安装到设备
adb install -r app/build/outputs/apk/debug/app-debug.apk

# 3. 运行测试
cd ../..
./backend/scripts/test-vad-latency.sh

# 4. 收集日志并分析
adb logcat > vad-test.log
# 录音10次后按 Ctrl+C
python3 backend/scripts/measure-latency.py vad-test.log
```

### 审计代码完整性

```bash
python3 backend/scripts/audit-phase1.py
```

预期输出：`🎉 Phase 1 实施完整，所有检查点通过！`

## 实施时间线

```
✅ Week 1-3:  Phase 1 延迟优化 (已完成)
  ├─ ✅ Silero VAD集成
  ├─ ✅ MeetingRecordService改造
  └─ ⏳ 真机验证 (本周)

⏳ Week 4-7:  Phase 2 准确度提升 (待开始)
  ├─ Paraformer替换zipformer
  └─ ECAPA-TDNN说话人分离

⏳ Week 8-12: Phase 3 交互优化 (待开始)
  ├─ 三层UI架构
  └─ 编辑功能 + 多格式导出
```

## 关键文件位置

### Android代码

```
frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/plugins/
├── SherpaPlugin.java         # VAD模型管理 (+150行)
├── MeetingRecordService.java # VAD动态切片 (+80行)
```

### 前端代码

```
frontend/src/
├── native/sherpa.ts           # VAD接口定义 (+15行)
├── api/stt.ts                 # 转写API（本地兜底已就绪）
└── features/meetings/
    └── ingest-speech.ts       # 音频处理管道
```

### 测试工具

```
backend/scripts/
├── measure-latency.py         # 延迟精确测量
├── test-vad-latency.sh        # 真机测试脚本
└── audit-phase1.py            # 代码审计工具
```

## 配置开关

### 启用/禁用VAD

编辑 `frontend/android/app/src/main/java/.../MeetingRecordService.java:40`:

```java
private static final boolean VAD_ENABLED = true;  // 启用VAD
// private static final boolean VAD_ENABLED = false;  // 禁用（降级到固定8秒）
```

### VAD参数调优

编辑 `SherpaPlugin.java:326-329`:

```java
vadConfig.setThreshold(0.5f);              // 灵敏度: 0.3(敏感) ~ 0.7(保守)
vadConfig.setMinSilenceDuration(0.5f);     // 静音判定: 0.3s ~ 1.0s
vadConfig.setMinSpeechDuration(0.25f);     // 最小语音: 0.15s ~ 0.5s
```

## 性能目标

| 指标 | 当前 | Phase 1 | Phase 2 | Phase 3 | 目标 |
|------|------|---------|---------|---------|------|
| 首字延迟 | 8-10s | **<1s** | <1s | <1s | <1s |
| 字准率 | 85% | 85% | **95%** | 95% | 95% |
| 说话人识别 | 0% | 0% | **90%** | 90% | 90% |
| 编辑能力 | 0% | 0% | 0% | **100%** | 100% |

## 技术参考

- [Silero VAD](https://github.com/snakers4/silero-vad) - VAD模型
- [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) - 语音识别引擎
- [RealtimeSTT](https://github.com/KoljaB/RealtimeSTT) - 流式架构参考
- [WhisperX](https://github.com/m-bain/whisperX) - VAD预处理参考

## 联系方式

- 项目仓库 Issue
- 技术群讨论
- 每周 Sprint Review

---

**最后更新**: 2026-11-14  
**维护人**: ZCode AI Assistant
