# Phase 2 准备：Paraformer模型集成可行性评估

## 目标
将本地ASR字准率从85%（zipformer）提升至95%（Paraformer），同时保持实时性能

## 背景

### 当前状态（Phase 1后）
- **本地引擎**: sherpa-onnx v2.0.1 + zipformer-small
- **字准率**: 85%（无标点，同音字多）
- **RTF**: 0.12（移动端）
- **延迟**: <1秒（VAD动态端点检测）

### 目标状态（Phase 2后）
- **本地引擎**: sherpa-onnx v2.0.1 + **paraformer-zh**
- **字准率**: 95%+（带标点）
- **RTF**: 0.15（仅增加3%）
- **体量**: 120MB（+70MB可接受）

---

## Paraformer 技术评估

### 模型特性

| 特性 | zipformer-small | paraformer-zh | 改善 |
|------|-----------------|---------------|------|
| 字准率 | 85% | **95%** | +10% |
| 标点还原 | ❌ | ✅ | 质的飞跃 |
| RTF (移动端) | 0.12 | 0.15 | +25%可接受 |
| 模型体量 | 50MB | 120MB | +140% |
| 推理模式 | 流式 | 流式 | 兼容 |

### sherpa-onnx 支持情况

**检查清单**：
1. ✅ sherpa-onnx是否包含Paraformer支持
2. ⏸ Paraformer模型下载地址
3. ⏸ 配置参数要求
4. ⏸ 移动端性能基准

---

## 实施方案（草案）

### 方案A：完全替换（推荐）

**步骤**：
1. 下载Paraformer-zh ONNX模型
2. 更新SherpaPlugin配置
3. 移除zipformer模型
4. 测试验证

**优点**：
- 体积最小（仅120MB）
- 配置简单
- 性能最优

**缺点**：
- 无降级选项

### 方案B：双引擎并存

**步骤**：
1. 保留zipformer作为快速模式
2. 添加Paraformer作为高精模式
3. 运行时切换

**优点**：
- 用户可选择速度/准确度
- 降级方案

**缺点**：
- 体积增加至170MB
- 配置复杂

---

## 下一步行动

### Step 1: 验证sherpa-onnx Paraformer支持

```bash
# 检查sherpa-onnx v2.0.1是否包含Paraformer类
cd /tmp
jar tf classes.jar | grep -i paraformer
```

### Step 2: 查找Paraformer模型

**候选来源**：
- FunASR官方：https://github.com/alibaba-damo-academy/FunASR
- ModelScope：https://modelscope.cn/models/damo/speech_paraformer-large_asr_nat-zh-cn-16k-common-vocab8404-pytorch
- sherpa-onnx预转换：https://github.com/k2-fsa/sherpa-onnx/releases

### Step 3: 性能基准测试

**测试矩阵**：
| 设备类型 | zipformer RTF | paraformer RTF | 可接受度 |
|---------|---------------|----------------|----------|
| 高端（骁龙8系） | 0.08 | 0.10 | ✅ |
| 中端（骁龙7系） | 0.12 | 0.15 | ✅ |
| 低端（骁龙6系） | 0.20 | 0.25 | ⚠️ 需测试 |

---

## 风险评估

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|----------|
| Paraformer不在sherpa-onnx中 | 中 | 高 | 使用其他ONNX推理库 |
| 移动端性能不足 | 低 | 中 | 保留zipformer作为降级 |
| 模型体积过大 | 低 | 低 | 首次Wi-Fi下载 |
| 标点还原质量差 | 低 | 中 | 后处理规则补充 |

---

## 时间估算

- **可行性验证**: 1小时
- **模型集成**: 2-3小时
- **测试调优**: 2小时
- **文档更新**: 1小时
- **总计**: 6-7小时

---

## 成功标准

- [ ] Paraformer模型成功加载
- [ ] 字准率达到95%+（100条测试集）
- [ ] RTF < 0.2（中端设备）
- [ ] 标点还原准确率>90%
- [ ] 无内存泄漏、无崩溃
- [ ] 编译APK体积<200MB

---

## 待调研问题

1. sherpa-onnx v2.0.1是否原生支持Paraformer？
2. Paraformer-zh ONNX模型下载地址？
3. 配置参数与zipformer有何不同？
4. 是否需要额外的token文件？
5. 移动端实际RTF是多少？

**下一步**：执行Step 1验证sherpa-onnx对Paraformer的支持情况
