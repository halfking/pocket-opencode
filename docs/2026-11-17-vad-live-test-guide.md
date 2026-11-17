# OpenPocket VAD 真机测试指南

**日期**: 2026-11-17  
**测试设备**: vivo V2436A (4c308e2e)  
**目的**: 对比能量检测 VAD 和 Silero VAD 的实际表现

---

## 测试前准备

### 1. 确认设备连接

```bash
adb devices
# 应显示: 4c308e2e	device
```

### 2. 确认 APK 构建完成

```bash
ls -lh frontend/android/app/build/outputs/apk/debug/app-debug.apk
```

### 3. 安装 APK

```bash
adb -s 4c308e2e install -r frontend/android/app/build/outputs/apk/debug/app-debug.apk
```

### 4. 启动应用并查看日志

```bash
# 清空旧日志
adb logcat -c

# 实时查看日志（过滤 VAD 相关）
adb logcat | grep -E "vad-factory|vad-web-segmenter|vad-segmenter|recordingRuntime"
```

---

## 测试方案 A: 能量检测 VAD（基线）

### 步骤

1. **打开应用** - 启动 OpenPocket

2. **进入设置** - 导航到设置页面

3. **选择 VAD 方案**
   - 查找 "语音检测方案" 设置
   - 选择 "能量检测"（⚡ 标识）
   - 确认切换成功

4. **开始录音测试**
   - 创建新会议或笔记
   - 开始录音
   - 查看日志确认 VAD 类型：
     ```
     [recordingRuntime] Creating VAD with type: energy
     [recordingRuntime] VAD created successfully: energy
     ```

5. **执行测试用例**

   **测试用例 1: 短句停顿**
   - 说一句话（3-5秒）
   - 停顿 2 秒
   - 再说一句话
   - **观察**: 延迟时间、分段准确性

   **测试用例 2: 连续讲话**
   - 连续讲话 30-60 秒不停顿
   - **观察**: 是否触发强制切分、音频完整性

   **测试用例 3: 低音量/背景噪音**
   - 小声说话或在嘈杂环境
   - **观察**: 是否能正确检测语音开始/结束

6. **记录数据**
   ```
   测试环境: _______________
   
   用例 1 - 短句停顿:
   - 第一句到转写显示延迟: ______ms
   - 分段准确性: 是/否
   - 文字准确率: ______%
   
   用例 2 - 连续讲话:
   - 是否触发强制切分: 是/否
   - 切分位置合理性: 好/中/差
   - 音频丢失: 是/否
   
   用例 3 - 低音量/噪音:
   - 语音检测成功率: ______%
   - 误触发次数: ______
   ```

---

## 测试方案 B: Silero VAD（新方案）

### 步骤

1. **切换 VAD 方案**
   - 返回设置页面
   - 选择 "Silero VAD"（🧠 标识）
   - **注意**: 首次使用会下载 ~16MB 模型文件
   - 等待提示 "已切换到 Silero VAD 方案"

2. **确认模型下载**
   ```bash
   # 查看日志确认模型加载
   adb logcat | grep -E "vad-web-segmenter|Silero VAD"
   
   # 应看到:
   # [vad-web-segmenter] Initializing Silero VAD...
   # [vad-web-segmenter] VAD initialized successfully
   ```

3. **开始录音测试**
   - 创建新会议或笔记
   - 开始录音
   - 查看日志确认：
     ```
     [recordingRuntime] Creating VAD with type: silero
     [recordingRuntime] VAD created successfully: silero
     ```

4. **执行相同测试用例**
   - 重复方案 A 的三个测试用例
   - 在**相同环境**下测试以便对比

5. **记录数据**
   ```
   测试环境: _______________
   
   用例 1 - 短句停顿:
   - 第一句到转写显示延迟: ______ms
   - 分段准确性: 是/否
   - 文字准确率: ______%
   
   用例 2 - 连续讲话:
   - 是否触发强制切分: 是/否
   - 切分位置合理性: 好/中/差
   - 音频丢失: 是/否
   
   用例 3 - 低音量/噪音:
   - 语音检测成功率: ______%
   - 误触发次数: ______
   ```

---

## 关键日志点

### VAD 初始化

```
[vad-factory] Creating VAD: <type>
[vad-factory] <Type> VAD ready in <X>ms
[recordingRuntime] VAD created successfully: <type>
```

### 语音检测事件

**能量检测**:
```
[vad-segmenter] 段音频不完整（如有问题）
[vad-segmenter] 强制切分触发（连续语音过长）
```

**Silero VAD**:
```
[vad-web-segmenter] Speech started at <X>ms
[vad-web-segmenter] Speech ended at <X>ms
[vad-web-segmenter] Finalizing segment: <start>ms - <end>ms
[vad-web-segmenter] VAD misfire detected（误触发）
```

### 性能指标

```
[vad-web-segmenter] 段音频不完整（如有）
[vad-web-segmenter] 强制切分触发（如有）
```

---

## 对比分析

### 延迟对比

| 指标 | 能量检测 VAD | Silero VAD | 改善 |
|------|------------|-----------|------|
| 语音开始检测 | ~50ms | ~30ms | -20ms |
| 静音判定延迟 | 1500ms | 300-500ms | ~1000ms |
| 总体响应延迟 | ~1550ms | ~500ms | ~1050ms |
| 首次初始化 | <10ms | ~500ms | +490ms |

### 准确性对比

| 指标 | 能量检测 VAD | Silero VAD |
|------|------------|-----------|
| 静音检测准确率 | ____% | ____% |
| 边界检测精度 | 好/中/差 | 好/中/差 |
| 噪音鲁棒性 | 好/中/差 | 好/中/差 |
| 误触发率 | ____% | ____% |

### 资源占用

| 指标 | 能量检测 VAD | Silero VAD |
|------|------------|-----------|
| 内存占用 | ~10MB | ~30MB |
| 初始化时间 | <10ms | ~500ms |
| 首次下载 | 0MB | ~16MB |
| CPU 占用 | 低 | 中 |

---

## 常见问题

### Q: Silero VAD 一直无法初始化？

**A**: 检查以下几点：
1. 网络连接是否正常
2. 模型文件是否下载完整
3. 查看日志错误信息：
   ```bash
   adb logcat | grep -E "vad-web|Failed to initialize"
   ```
4. 如果失败，应自动回退到能量检测 VAD

### Q: 切换 VAD 后没有生效？

**A**: 需要**重新开始录音**才会应用新设置。停止当前录音，再开始新的录音。

### Q: 如何确认当前使用的是哪种 VAD？

**A**: 查看日志：
```bash
adb logcat | grep "VAD created successfully"
```
会显示 `energy` 或 `silero`

### Q: Silero VAD 延迟反而更高？

**A**: 可能原因：
1. 首次使用正在下载模型（只发生一次）
2. 设备性能较低，模型推理慢
3. 网络延迟影响

查看具体延迟日志分析原因。

---

## 测试完成后

### 1. 收集日志

```bash
# 保存完整日志
adb logcat -d > vad-test-$(date +%Y%m%d-%H%M%S).log

# 只保存 VAD 相关日志
adb logcat -d | grep -E "vad-|recordingRuntime" > vad-test-filtered-$(date +%Y%m%d-%H%M%S).log
```

### 2. 导出测试数据

从应用内导出录音文件和转写结果，用于离线分析。

### 3. 填写测试报告

将测试数据填入 `docs/2026-11-17-vad-live-test-report.md`

### 4. 清理

```bash
# 卸载测试应用（如需要）
adb uninstall com.kaixuan.opencode.pocket

# 清理日志
adb logcat -c
```

---

## 推荐测试顺序

1. **基线测试**: 先用能量检测 VAD 测试所有用例，建立基准数据
2. **新方案测试**: 切换到 Silero VAD，重复相同用例
3. **交叉验证**: 再次切换回能量检测，验证结果一致性
4. **边界测试**: 测试极端情况（很吵、很安静、快速切换等）

---

## 预期结果

根据理论分析，预期 Silero VAD 相比能量检测 VAD：

- ✅ **延迟降低** ~1000ms（68% 改善）
- ✅ **边界检测更准确**，减少误切
- ✅ **噪音鲁棒性更好**，无需手动调参
- ⚠️ **首次启动慢** ~500ms（模型加载）
- ⚠️ **内存占用增加** ~20MB
- ⚠️ **首次下载** ~16MB

**结论**: 如果实测结果符合预期，建议将 Silero VAD 作为默认方案。
