# OpenPocket VAD 真机测试 - 准备就绪

**日期**: 2026-11-17 03:16  
**测试设备**: vivo V2436A (4c308e2e)  
**状态**: ✅ 准备完成，等待人工测试

---

## 已完成的准备工作

### 1. 代码实现 ✅

- [x] **VAD 工厂** (`frontend/src/native/vad-factory.ts`)
  - 支持能量检测和 Silero VAD 两种方案
  - 自动回退机制（Silero 失败时回退到能量检测）
  - localStorage 持久化用户偏好

- [x] **Silero VAD 实现** (`frontend/src/native/vad-web-segmenter.ts`)
  - 基于 @ricky0123/vad-web (Silero VAD v5)
  - 自适应噪音环境
  - 完整的错误处理和日志

- [x] **VAD 设置界面** (`frontend/src/features/settings/VadSettingsSheet.vue`)
  - 可视化切换开关
  - 实时性能统计
  - 使用提示和说明

- [x] **集成到录音运行时** (`frontend/src/native/recordingRuntime.ts`)
  - 使用 VAD 工厂创建实例
  - 支持动态切换
  - 完整日志输出

### 2. 资源部署 ✅

- [x] **模型文件** (`frontend/public/vad/`)
  ```
  vad.worklet.bundle.min.js       2.4 KB
  silero_vad.onnx                 2.2 MB
  ort-wasm-simd-threaded.wasm    14.0 MB
  -----------------------------------
  总计:                          ~16 MB
  ```

- [x] **依赖安装**
  ```
  @ricky0123/vad-web@0.0.31
  onnxruntime-web (间接依赖)
  ```

### 3. 构建和部署 ✅

- [x] **前端构建**
  - TypeScript 编译通过
  - Vite 打包完成
  - 资源同步到 Android

- [x] **Android APK**
  - 构建时间: 2026-11-17 03:16
  - 文件大小: 2.0 GB
  - 安装成功: ✅

- [x] **真机部署**
  - 设备: vivo V2436A (4c308e2e)
  - 应用启动: ✅
  - 日志监控: ✅

---

## 测试环境配置

### 应用设置
- **后端地址**: http://192.168.31.32:8088 (本地开发)
- **默认 VAD**: 能量检测（稳定方案）
- **Silero VAD**: 首次使用需下载模型

### 日志监控
```bash
# 实时监控命令（已启动）
adb -s 4c308e2e logcat | grep -E "vad-factory|vad-web-segmenter|vad-segmenter|recordingRuntime"
```

### 快速部署脚本
```bash
# 一键部署和监控
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket
./deploy-and-test.sh
```

---

## 测试步骤（人工操作）

### 阶段 1: 测试能量检测 VAD（10分钟）

1. **打开应用**
   - 在真机上启动 OpenPocket

2. **进入设置**
   - 导航到设置页面
   - 查找"语音检测方案"

3. **选择能量检测**
   - 点击 "⚡ 能量检测" 选项
   - 确认选择（应显示"当前"标签）

4. **创建会议并录音**
   - 返回主页，创建新会议
   - 点击录音按钮开始

5. **查看日志确认**
   ```
   期望日志：
   [recordingRuntime] Creating VAD with type: energy
   [vad-factory] Energy VAD ready in <X>ms
   [recordingRuntime] VAD created successfully: energy
   ```

6. **执行测试用例**
   
   **用例 1: 短句停顿**
   - 说一句话（3-5秒）
   - 停顿 2 秒
   - 再说一句话
   - 记录：从停顿到转写显示的延迟

   **用例 2: 连续讲话**
   - 连续讲话 60 秒不停顿
   - 观察：是否触发强制切分

   **用例 3: 低音量/噪音**
   - 小声说话或在嘈杂环境
   - 观察：检测准确性

### 阶段 2: 测试 Silero VAD（15分钟）

1. **切换 VAD 方案**
   - 返回设置页面
   - 选择 "🧠 Silero VAD"
   - **注意**: 首次使用会下载模型（~16MB）

2. **等待模型加载**
   ```
   期望日志：
   [vad-web-segmenter] Initializing Silero VAD...
   [vad-web-segmenter] VAD initialized successfully
   ```

3. **创建新会议并录音**
   - 创建新会议（确保是新的，不是之前的）
   - 开始录音

4. **查看日志确认**
   ```
   期望日志：
   [recordingRuntime] Creating VAD with type: silero
   [vad-factory] Silero VAD ready in <X>ms
   [recordingRuntime] VAD created successfully: silero
   ```

5. **执行相同测试用例**
   - 重复用例 1、2、3
   - 在相同环境下测试
   - 对比与能量检测的差异

### 阶段 3: 对比分析（5分钟）

1. **收集数据**
   - 延迟时间
   - 分段准确性
   - 转写质量
   - 用户体验

2. **填写测试报告**
   - 打开: `docs/2026-11-17-vad-live-test-report.md`
   - 填写所有测试数据
   - 记录主观感受

---

## 预期结果

### 性能指标

| 指标 | 能量检测 VAD | Silero VAD | 改善 |
|------|------------|-----------|------|
| 初始化时间 | <10ms | ~500ms | - |
| 静音判定延迟 | ~1500ms | ~500ms | **-1000ms** |
| 总体响应延迟 | ~1550ms | ~500ms | **-1050ms** |
| 内存占用 | ~10MB | ~30MB | +20MB |

### 用户体验

**能量检测 VAD**:
- ✅ 启动快速
- ⚠️ 停顿后等待时间长（~1.5秒）
- ⚠️ 噪音环境可能误判

**Silero VAD**:
- ⚠️ 首次启动需要下载和加载
- ✅ 响应快速（停顿后~0.5秒）
- ✅ 噪音适应性好
- ✅ 分段更准确

---

## 关键日志检查点

### ✅ 成功指标

```
# VAD 初始化成功
[vad-factory] Creating VAD: <type>
[vad-factory] <type> VAD ready in <X>ms
[recordingRuntime] VAD created successfully: <type>

# Silero VAD 语音检测
[vad-web-segmenter] Speech started at <X>ms
[vad-web-segmenter] Speech ended at <X>ms
[vad-web-segmenter] Finalizing segment: <start>ms - <end>ms
```

### ⚠️ 警告信号

```
# 强制切分（单段过长）
[vad-xxx-segmenter] 强制切分触发

# 音频不完整
[vad-xxx-segmenter] 段音频不完整

# VAD 初始化失败（自动回退）
[vad-factory] Failed to create Silero VAD, falling back to energy detection
```

---

## 测试完成后

### 1. 保存日志
```bash
adb -s 4c308e2e logcat -d > vad-test-$(date +%Y%m%d-%H%M%S).log
```

### 2. 填写报告
- 完成 `docs/2026-11-17-vad-live-test-report.md`
- 包含量化数据和主观评价

### 3. 决策建议
- Silero VAD 是否达到预期？
- 是否推荐作为默认方案？
- 需要哪些优化？

---

## 支持文档

- **测试指南**: `docs/2026-11-17-vad-live-test-guide.md` (详细步骤)
- **测试报告**: `docs/2026-11-17-vad-live-test-report.md` (填写模板)
- **快速步骤**: `VAD-TEST-STEPS.md` (简明指南)
- **实施报告**: `docs/2026-11-17-phase3-vad-completion.md` (技术详情)

---

## 联系方式

如有问题，查看日志或联系开发团队。

**预计测试时长**: 30-40 分钟  
**关键成功指标**: 延迟降低 ~1000ms (68% 改善)

---

**状态**: ✅ 所有准备工作完成，随时可以开始人工测试
