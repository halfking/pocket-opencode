# Android 本地语音识别技术评估报告

**版本**: v1.0.0
**日期**: 2026-07-02
**状态**: 调研结论 + 推荐方案
**适用项目**: OpenCode Pocket 个人助理 APP

---

## 📌 执行摘要（TL;DR）

原计划评估 "Whisper.cpp tiny/base/small" 作为本地语音识别方案。经过对 2025–2026 年最新基准数据的调研，**核心结论发生根本性转变**：

> ⚠️ **Whisper.cpp 不应作为中文本地识别的首选**。Whisper small 在普通话上准确率仅约 65%，tiny/base 更差。中文本地识别的正确选择是 **sherpa-onnx（Paraformer / SenseVoice）**，CER 8–10%，RTF 0.06–0.15（远快于实时），且为 Android 原生优化。

推荐**三档设备分级 + 本地优先 + 云端兜底**策略：
- **本地主引擎**：sherpa-onnx（中文 Paraformer / 英文可选用 Whisper-base 或 Zipformer）
- **轻量备用**：Vosk small-cn（42 MB，低内存设备）
- **云端兜底**：Groq Whisper Large v3 Turbo（$0.04/小时，228× 实时速度）

---

## 1. 候选方案对比总表

| 方案 | RTF（Android） | RAM 占用 | 模型体积 | 中文支持 | 流式识别 | 许可证 |
|------|---------------|---------|---------|---------|---------|--------|
| **whisper.cpp tiny** | < 1.0 ✅ | 150–250 MB | ~75 MB（q5: 31 MB） | 差 | 手动分块 | MIT |
| **whisper.cpp base** | ~1.0 ⚠️ | 300–500 MB | ~142 MB（q5: 60 MB） | 差 | 手动分块 | MIT |
| **whisper.cpp small** | > 1.0 ❌ | ~2 GB | ~466 MB | 弱（~65% 准确率） | 手动分块 | MIT |
| **Vosk small-cn** | < 1.0 ✅（低 CPU） | ~300 MB | **42 MB** | 中等 | ✅ 原生 | Apache 2.0 |
| **Vosk cn-0.22（大）** | < 1.0 | ~1 GB+ | 1.3 GB | 较好 | ✅ 原生 | Apache 2.0 |
| **sherpa-onnx（Paraformer/SenseVoice）** | **0.06–0.15** ✅✅ | 低 | 70–220 MB | **最佳（8–10% CER）** | ✅ 原生 + VAD | Apache 2.0 |
| Android SpeechRecognizer | 实时 | 系统管理 | 系统管理 | 设备相关 | ✅ | 专有（离线不稳定） |
| Groq Whisper v3-turbo（云） | 228× 实时 | 0 | 0 | 优秀（大模型） | 批量 | API（付费） |
| OpenAI Whisper（云） | 较慢 | 0 | 0 | 优秀 | 批量 | API（付费） |

> RTF（Real-Time Factor）= 处理时长 ÷ 音频时长，< 1.0 表示比实时快。CER（Character Error Rate）越低越好。

---

## 2. 各方案详细分析

### 2.1 whisper.cpp（原评估对象）

**性能特征**：
- tiny / tiny.en：RTF 明显 < 1.0，是唯一真正适合中端 CPU 实时/流式的 Whisper 模型
- base / base.en：RTF ≈ 1.0，临界实时，需合理线程数（`-t`）
- small：RTF > 1.0，大多数手机 CPU 上无法实时，仅适合短音频离线转写

**模型体积（GGML FP16 / 量化）**：

| 模型 | 参数量 | FP16 | q5_1 量化 |
|------|--------|------|----------|
| tiny / tiny.en | ~39M | ~75 MB | ~31 MB |
| base / base.en | ~74M | ~142 MB | ~60 MB |
| small / small.en | ~244M | ~466 MB | ~190 MB |

**中文致命弱点**：Interspeech 2025 报告显示 Whisper small 在带语言感知解码下普通话词准确率约 **65.77%**。tiny/base 更差。对比之下中文专用模型 SenseVoice（CER 8.0%）、Paraformer（CER 9.9%）大幅领先。

**电量**：无 Android 官方基准；社区反馈持续监听耗电显著，建议用 VAD 门控，仅在检测到语音时推理。

**构建问题**：支持 ARM NEON 优化；NDK + CMake 构建有文档；长音频内存尖峰（issue #2310）；**无原生流式**，需手动分块。

**结论**：⚠️ **英文场景可用（base 及以下），中文场景不推荐。** 不作为本 APP 的本地主引擎。

### 2.2 Vosk（Kaldi-based）

- 中文模型：`vosk-model-small-cn-0.22` ≈ 42 MB；`vosk-model-cn-0.22` ≈ 1.3 GB（高精度）
- 速度：真正的流式/实时识别，延迟极低，CPU 占用低，完全离线
- **优于 whisper.cpp**：体积小得多、原生流式、延迟和 RAM 低得多、专为移动端设计
- **弱于 whisper.cpp / sherpa**：准确率较低、无标点/翻译、Kaldi 声学模型落后于神经大模型时代、普通话准确率中等
- 许可证：Apache 2.0

**定位**：低端设备（< 6 GB RAM）的轻量中文方案，或作为 sherpa-onnx 太重时的替代。

### 2.3 sherpa-onnx（next-gen Kaldi）—— ✅ 推荐主引擎

- 基于 ONNX Runtime，支持 **Zipformer / Paraformer / SenseVoice / Whisper** 全系列模型
- 跨平台：Android / iOS / Linux / HarmonyOS
- **移动端 RTF：0.06–0.15** —— 所有候选中最优，远快于实时
- 电量：iPhone 上约 2%/小时（Brightcoding 基准）
- 中文：Paraformer / SenseVoice 模型在普通话上 **CER 8–10%**，显著优于 Whisper small
- 流式：原生流式 ASR + VAD，支持中文方言
- 许可证：Apache 2.0，仓库 github.com/k2-fsa/sherpa-onnx

**定位**：✅ **中高端 Android 的本地主引擎**（中文 Paraformer，英文 Zipformer 或 Whisper-base）。

### 2.4 Android 内置 SpeechRecognizer

- 经典 `android.speech.SpeechRecognizer` 的**离线模式已被实质弃用/移除**，现路由到 Google Play Services，**强依赖云端**
- 较新的 **ML Kit GenAI Speech Recognition**（on-device）处于 **alpha，不稳定，无 SLA/弃用策略，可能破坏向后兼容**
- 需用 `RecognitionSupport` API 查询设备实际支持哪些在线/离线语言
- 各 OEM 厂商碎片化严重

**结论**：可作为免费兜底；**不可作为产品的可靠离线中文基石**。

### 2.5 云端 Whisper API（兜底）

| 提供商 / 模型 | 速度 | 每音频小时费用 | 约每分钟费用 |
|--------------|------|---------------|-------------|
| **Groq — Whisper Large v3 Turbo** | ~228× 实时（1 小时 ≈ 16 秒） | **$0.04/hr** | **$0.00067** |
| Groq — Whisper Large v3 | 164× 实时 | $0.111/hr | $0.00185 |
| OpenAI — Whisper API | 较慢 | ~$0.36/hr | ~$0.006 |

Groq Turbo 比 OpenAI 便宜约 89%。每次请求有 10 秒最低计费。

**结论**：✅ **Groq Whisper Large v3 Turbo 作为云端兜底首选**（便宜、极快、大模型中文最准）。

---

## 3. 设备分级推荐策略

### Tier 1 — 高端机（骁龙 8 Gen 系列，≥ 8 GB RAM）
- **本地主引擎**：sherpa-onnx + Paraformer（中文）/ Whisper-base 或 Paraformer（英文）。RTF ~0.1，最佳普通话 CER，原生流式 + VAD，~2% 电量/小时
- 可选 Whisper small 用于已保存片段的高精度离线转写（非实时）
- **云端兜底**：Groq Whisper Large v3 Turbo，用于嘈杂/长/关键语句或本地置信度低时

### Tier 2 — 中端机（骁龙 7 系列，~6 GB RAM）
- **本地主引擎**：sherpa-onnx Paraformer（int8）或 whisper.cpp base（q5_1，~60 MB，英文）。避免 whisper.cpp small（RTF > 1，~2 GB RAM）
- 若 sherpa-onnx 过重，Vosk small-cn 是轻量替代（42 MB，低 CPU）
- **云端兜底**：Groq，本地处理不了的一切都走云 —— $0.00067/分钟近乎免费

### Tier 3 — 低端机（< 6 GB RAM / 老旧 ARM）
- **本地主引擎**：Vosk small-cn（42 MB）做流式中文；whisper.cpp tiny（q5，~31 MB）做英文短句
- **默认走云端**（Groq Turbo）为主识别；仅在离线时用本地

### 统一兜底触发条件（任意档位）
嘈杂环境、语句长于 ~10–15 秒、本地置信度低、中英混杂（code-switching）、用户反馈识别错 → 路由到 Groq Whisper Large v3 Turbo。

### 混合架构建议
- **VAD 门控流式**（sherpa-onnx 或 Vosk）做热词/命令层（常开、廉价）
- 整句先批送本地 Paraformer；仅在置信度差距触发时回退 Groq 云端
- 这样既省电（避免 whisper.cpp 持续推理）又省钱（仅在必要时走云）

---

## 4. 集成到 OpenCode Pocket 的技术路径

本 APP 采用 **Capacitor 包壳 + Vue3** 架构（非 Kotlin 原生），因此本地语音识别的集成方式需要明确：

### 4.1 推荐路径：Capacitor 自定义插件桥接原生 sherpa-onnx

```
Vue 组件 (录音 UI)
    ↓  (Capacitor 插件调用)
cap-plugin-sherpa (TypeScript 接口)
    ↓  (JNI / bridging)
Android 原生 sherpa-onnx (Kotlin/Java)
    ↓  (ONNX Runtime)
Paraformer / SenseVoice 模型
```

**实现要点**：
1. 用 Capacitor 的 [自定义 Android 插件](https://capacitorjs.com/docs/plugins/android) 机制封装 sherpa-onnx 的 Android AAR
2. 插件暴露 `startListening()` / `stopListening()` / `transcribe(audioPath)` 三类方法
3. 录音端用 `@capacitor-community/media-capture` 或浏览器 `MediaRecorder` API
4. 云端兜底逻辑放在 Vue 层，根据置信度/设备档位决定是否调用 Groq

### 4.2 备选路径：纯 Web 方案（受限）
- 浏览器 `Web Speech API` 在 Android WebView 内不可靠，且依赖 Google 云
- 不推荐作为主方案，仅作为 Tier 3 设备的最简兜底

### 4.3 模型分发
- sherpa-onnx Paraformer 模型（~70–220 MB）：首次启动时按设备档位下载，存于 app 内部存储
- 支持模型热切换（用户可在设置里选择本地模型档位）
- 提供模型下载进度、用量统计

---

## 5. 对原 Voice-Notion 方案的修正

原 `2026-07-01-voice-notion-app-plan.md` 假设使用 "Whisper API（云端）" + Flutter。本次评估后建议：

| 维度 | 原方案 | 修正建议 |
|------|--------|---------|
| 本地识别 | 未明确（隐含 Whisper.cpp） | sherpa-onnx Paraformer（中文主），Vosk 兜底（低端） |
| 云端识别 | OpenAI Whisper API | **Groq Whisper Large v3 Turbo**（便宜 89%、快） |
| 客户端 | Flutter | Capacitor + Vue3（扩展 opencode-pocket） |
| 准确率预期 | 中文未量化 | 中文 CER 8–10%（本地）/ < 5%（云端大模型） |

---

## 6. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| sherpa-onnx AAR 集成到 Capacitor 缺现成插件 | 中 | 需自写 Capacitor 插件桥（~2-3 天），参考 sherpa-onnx 官方 Android demo |
| Paraformer 模型 70-220 MB 首次下载 | 中 | 分级下发、WiFi 下载、复用已有缓存 |
| Groq 兜底需联网 | 低 | 仅在本地失败时触发；离线纯本地模式 |
| 持续录音耗电 | 中 | VAD 门控，仅语音段推理；后台用轻量 Vosk |
| 中英混杂识别差 | 中 | 检测混杂自动回退云端大模型 |

---

## 7. 落地里程碑

1. **MVP（第 1-2 周）**：先用 **Groq 云端 Whisper Large v3 Turbo** 跑通"录音→转写→存笔记"主流程，确保端到端可用
2. **第 3-4 周**：集成 sherpa-onnx Paraformer 本地引擎（Capacitor 插件），中高端设备本地优先
3. **第 5 周**：设备分级策略 + Vosk 兜底 + 置信度回退云端逻辑
4. **第 6 周**：VAD 门控、电量优化、模型下载管理 UI

---

## 参考来源

- whisper.cpp WER 基准 issue #2454: https://github.com/ggml-org/whisper.cpp/issues/2454
- whisper.cpp 模型 (HuggingFace): https://huggingface.co/ggerganov/whisper.cpp/tree/main
- whisper.cpp 内存 issue #2310: https://github.com/ggml-org/whisper.cpp/issues/2310
- whisper.cpp Android 构建指南: https://www.insideapp.fr/publications/whisper-android.html
- alphacephei Vosk 模型: https://alphacephei.com/vosk/models
- sherpa-onnx GitHub: https://github.com/k2-fsa/sherpa-onnx
- sherpa-onnx RTF 基准 (Brightcoding): https://www.blog.brightcoding.dev/2025/09/11/sherpa-onnx-unified-speech-recognition-synthesis-and-audio-processing-for-every-platform
- FunASR vs faster-whisper 中文 CER: https://www.funasr.com/en/blog/funasr-vs-faster-whisper-chinese.html
- Interspeech 2025 Whisper small 普通话: https://www.isca-archive.org/interspeech_2025/wu25m_interspeech.pdf
- Android SpeechRecognizer 离线失效 (SO): https://stackoverflow.com/questions/64708403/android-speech-recognizer-no-longer-working-offline
- Google ML Kit GenAI Speech: https://developers.google.com/ml-kit/genai/speech-recognition/android
- Groq 定价: https://groq.com/pricing
- Groq Whisper Large v3 Turbo: https://groq.com/blog/whisper-large-v3-turbo-now-available-on-groq-combining-speed-quality-for-speech-recognition
