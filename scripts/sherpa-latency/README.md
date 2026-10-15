# sherpa-latency —— 端上实时转写可行性探针（桌面 JVM 同构测量）

回答一个问题：**实时转写用部署在手机端的流式模型，出字够不够快、算力够不够省？**
（2026-10-06 ASR 多模型轮的可行性实证工具，结论见
`docs/2026-10-06-ondevice-realtime-feasibility.md`。）

## 原理

`LatencyProbe.java` 与 `SherpaPlugin.java` 的实时路径**逐参同构**：
100ms 块 / 16k / 三端点规则（2.4s trailing · 1.2s utterance · 20s max）/
modified_beam_search beam=4 / int8 zipformer-small 双语；另带 SenseVoice
int8 离线档对照。桌面 arm64 JVM 跑同一组 onnx，量四个数：

| 指标 | 含义 | 判据 |
|---|---|---|
| `rtf` | 尽快喂入时的 识别耗时/音频时长 | < 1.0 即「比实时快」，留出低端机余量 |
| `ttfp_ms` | 实时节拍下，语音开始到第一条非空 partial | 会话字幕 < 800ms 体感即「即时」 |
| `partials` | partial 帧数 | 每 100ms 一帧 = 字幕持续刷新 |
| `tail_final_ms` | 最后一块喂入到端点判停+final 文本 | 越小越好，> 2400ms 说明端点规则退化 |
| `sensevoice_offline.latency_ms` | 高精档整段延迟 | 分段终稿刷新节奏的依据 |

## 运行配方（osx-aarch64 示例）

```bash
D=scripts/sherpa-latency
# 1) 依赖三件（不进 git）：
#    AAR 内 classes.jar：unzip -o app/libs/sherpa-onnx-1.13.8.aar classes.jar
#    （AAR 由 frontend/android/app/build.gradle 的 downloadSherpaAar 任务下载）
#    桌面原生库：https://github.com/k2-fsa/sherpa-onnx/releases/download/v1.13.8/sherpa-onnx-native-lib-osx-aarch64-1.13.8.jar
#      解出 sherpa-onnx/native/osx-aarch64/*.dylib 到工作目录
#    kotlin-stdlib-1.8.22.jar：gradle 缓存 modules-2/files-2.1/org.jetbrains.kotlin/kotlin-stdlib/1.8.22/
# 2) 模型（与插件运行时同一组）：
#    sherpa-onnx-streaming-zipformer-small-bilingual-zh-en-2023-02-16-mobile
#    sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17
# 3) 16k 单声道 wav（非 16k 会格式闸拒绝，先 ffmpeg -ar 16000 -ac 1 -sample_fmt s16）

AJ=$HOME/Library/Android/sdk/platforms/android-36/android.jar   # 仅编译期 android stub
javac -cp classes.jar:"$AJ" $D/LatencyProbe.java
java -Djava.library.path=. -cp classes.jar:<kotlin-stdlib.jar>:$D \
  LatencyProbe <zipformerDir> <sensevoiceDir> sample16k.wav
```

## 2026-10-06 实测读数（桌面 osx-aarch64，9.44s 中文样本）

```
rtf=0.0197 (186ms/9.44s)   ttfp=514ms   partials=91（每 100ms 一帧）   tail_final=1ms
zipformer 流式字级相似率 97.8%（无标点，1 同音替代）
sensevoice 离线 190ms，相似率 100%（带标点）
```

端上（arm64 模拟器）同模型读数见 `docs/2026-10-06-local-asr-sherpa-integration.md`
§7：zipformer RTF 0.12、SenseVoice 1.3-1.5s——桌面读数与端上读数差 ~6×，
评估端上余量时按 RTF 0.12 口径，不按桌面口径。
