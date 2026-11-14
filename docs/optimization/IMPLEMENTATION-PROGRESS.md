# OpenPocket 优化方案实施进度报告

> **生成时间**: 2026-11-14  
> **当前状态**: Phase 1 已完成，Phase 2/3 待实施  
> **总体进度**: 25% (1/4 phases)

---

## ✅ Phase 1: 延迟优化 - 已完成

### 实施内容

1. **VAD集成** ✓
   - 添加 Silero VAD 模型支持到 SherpaPlugin
   - 实现动态端点检测逻辑
   - 配置参数：threshold=0.5, minSilence=500ms, minSpeech=250ms

2. **MeetingRecordService改造** ✓
   - 替换固定8秒切片为VAD动态切片
   - 分片范围：500ms-3秒（典型）/ 10秒兜底
   - 降级保障：VAD失败自动回退到固定切片

3. **前端接口暴露** ✓
   - 更新 sherpa.ts 添加 `createVad()` 和 `vadProcess()`
   - 新增 `VadResult` 和更新 `SherpaStatus` 接口

4. **测试工具** ✓
   - `measure-latency.py`: 延迟精确测量工具
   - `test-vad-latency.sh`: 真机测试脚本

### 核心代码修改

| 文件 | 改动 | 行数 |
|------|------|------|
| `SherpaPlugin.java` | 新增VAD方法/VadWrapper | +150 |
| `MeetingRecordService.java` | VAD动态切片逻辑 | +80 |
| `sherpa.ts` | VAD接口定义 | +15 |

### 预期性能

| 指标 | 改造前 | Phase 1后 | 改善 |
|------|--------|-----------|------|
| 首字延迟 | 8-10秒 | **0.5-1秒** | **8-10倍** ⭐ |
| 端点响应 | 8秒固定 | **200-500ms** | **16-40倍** ⭐ |
| CPU开销 | 5% | **<8%** | **+3%** ✓ |

### 待验证项

- [ ] 真机10次录音测量首字延迟分布
- [ ] 对比实验：VAD vs 固定切片（准确率/延迟）
- [ ] 长时录音（30分钟）稳定性
- [ ] 多说话人场景切片正确性

---

## ⏳ Phase 2: 准确度提升 - 待实施

### 计划内容

1. **Paraformer替换zipformer** (预计3-4周)
   - 字准率: 85% → 95% (+10%)
   - 自动标点: 从无到有
   - 模型体积: 50MB → 120MB
   - RTF: 0.12 → 0.15 (+3%)

2. **说话人分离** (ECAPA-TDNN)
   - 本地192维向量提取
   - 余弦相似度聚类（阈值0.85）
   - 自动标注："说话人1" / "说话人2"
   - 点击重命名："张三" / "李四"

3. **云端优化**
   - 固定优先级：mimo-v2.5-asr > groq > minimax > glm
   - 快速失败：单模型超时3秒自动降级

4. **本地兜底增强** ✅ (已在2026-11-14完成)
   - 云端失败返回本地最佳结果
   - UI置信度提示

### 技术准备

```java
// SherpaPlugin.java - Paraformer配置（待实施）
OfflineParaformerModelConfig config = 
  OfflineParaformerModelConfig.builder()
    .model("paraformer-zh-2023-09-14/model.int8.onnx")
    .build();
```

---

## ⏳ Phase 3: 交互体验 - 待实施

### 计划内容

1. **三层渲染架构** (4-5周)
   - Layer 1: 实时流（灰字，500ms刷新）
   - Layer 2: 分段确认（黑字，可编辑）
   - Layer 3: 全量精校（黑色加粗，带标点）

2. **智能滚动策略**
   - 自动跟随 vs 用户主动滚动
   - "新消息"提示徽章

3. **说话人管理UI**
   - 自动分配颜色
   - 点击重命名
   - 合并/分离纠错

4. **点击即改编辑**
   - contentEditable实现
   - 智能纠错（LanguageTool集成）

5. **多格式导出**
   - Word文档（带时间戳）
   - HTML富文本
   - 双语对照
   - SRT字幕
   - TXT纯文本

6. **PiP悬浮窗** (Android)
   - 切换应用时保持字幕可见

### 竞品对照

| 维度 | 讯飞听见 | OpenPocket当前 | Phase 3后 |
|------|----------|----------------|-----------|
| 实时反馈 | 即说即显 | 8秒延迟 | **<1秒** ⭐ |
| 置信度可视化 | 虚线标注 | 无 | **低置信词标注** ⭐ |
| 编辑能力 | 点击即改 | 只读 | **点击即改** ⭐ |
| 说话人标识 | 自动区分 | 缺失 | **自动+重命名** ⭐ |
| 导出格式 | 6种 | 2种 | **5种** ⭐ |

---

## 🎯 整体目标达成情况

### 技术指标

| 指标 | 基线 | Phase 1 | Phase 2 | Phase 3 | 目标 | 当前状态 |
|------|------|---------|---------|---------|------|----------|
| 首字延迟 P95 | 10秒 | **<1秒** | <1秒 | <1秒 | <1秒 | ✓ 预期达标 |
| 端点响应 P95 | 8秒 | **<500ms** | <500ms | <500ms | <500ms | ✓ 预期达标 |
| 字准率(中文) | 85% | 85% | **>95%** | >95% | >95% | ⏳ Phase 2 |
| 说话人准确率 | 0% | 0% | **>90%** | >90% | >90% | ⏳ Phase 2 |
| 可用率 | 95% | 95% | **>99%** | >99% | >99% | ⏳ Phase 2 |
| 编辑能力 | 0% | 0% | 0% | **100%** | 100% | ⏳ Phase 3 |

### 用户体验指标

| 指标 | 基线 | 目标 | 当前状态 | 测量方式 |
|------|------|------|----------|----------|
| 用户满意度 (NPS) | - | >50 | ⏳ 待测量 | 每月调研 |
| 录音时长中位数 | 3分钟 | >10分钟 | ⏳ 待统计 | 埋点 |
| 编辑使用率 | 0% | >30% | ⏳ Phase 3 | 埋点 |
| 导出使用率 | 20% | >50% | ⏳ Phase 3 | 埋点 |

---

## 🗓️ 实施时间线

```
✅ Week 1-3:  Phase 1 延迟优化 (已完成)
  ├─ ✅ Week 1: Silero VAD集成
  ├─ ✅ Week 2: MeetingRecordService改造
  └─ ⏳ Week 3: 真机验证 + A/B测试 (待执行)

⏳ Week 4-7:  Phase 2 准确度提升 (待开始)
  ├─ Week 4-5: Paraformer替换zipformer
  ├─ Week 6: ECAPA-TDNN说话人分离
  └─ Week 7: 云端优化 + 综合测试

⏳ Week 8-12: Phase 3 交互优化 (待开始)
  ├─ Week 8-9: 三层UI重构 + 智能滚动
  ├─ Week 10: 说话人管理 + 编辑功能
  └─ Week 11-12: 导出分享 + PiP + 收尾
```

---

## 📋 近期行动项

### 本周（Week 3）

1. **真机验证** 🔥
   - [ ] 构建APK: `cd frontend/android && ./gradlew assembleDebug`
   - [ ] 安装到测试设备
   - [ ] 运行 `./backend/scripts/test-vad-latency.sh`
   - [ ] 10次录音样本，测量首字延迟

2. **性能监控**
   - [ ] CPU占用（adb shell top）
   - [ ] 内存占用（adb shell dumpsys meminfo）
   - [ ] 电池温度（30分钟录音）

3. **A/B对比**
   - [ ] 构建VAD版本（VAD_ENABLED=true）
   - [ ] 构建固定切片版本（VAD_ENABLED=false）
   - [ ] 对比测试：延迟 + 字准率

### 下周（Week 4）

1. **灰度发布**
   - [ ] 10%用户VAD版本
   - [ ] 监控崩溃率/性能指标
   - [ ] 收集用户反馈

2. **Phase 2启动**
   - [ ] Paraformer模型下载（120MB）
   - [ ] 集成测试环境搭建
   - [ ] ECAPA-TDNN方案设计

---

## 🔗 相关文档

- [SUMMARY-OPTIMIZATION-PLAN.md](./SUMMARY-OPTIMIZATION-PLAN.md): 总体优化方案
- [PHASE1-VAD-IMPLEMENTATION.md](./PHASE1-VAD-IMPLEMENTATION.md): Phase 1实施细节
- [2026-11-14-cicada-phase-a-live-test-report.md](../handoff/2026-11-14-cicada-phase-a-live-test-report.md): 真机测试经验

---

**编制人**: ZCode AI Assistant  
**最后更新**: 2026-11-14  
**下次更新**: Week 3 验证完成后
