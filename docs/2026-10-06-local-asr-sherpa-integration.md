# openpocket 本地 ASR 落地——sherpa-onnx 双引擎（2026-10-06 Phase 4）

> 状态：原生插件 + Gradle 接线 + TS 契约更新已实现；引擎配方经桌面 JVM
> 同源实测（同一组 onnx、同一套配置字段）；Android 包编译验证见 §7。
> 上游方案与网关侧总盘子见 llm-gateway-go
> `docs/design/2026-10-03-audio-transcription-gateway-plan.md` §10。

## 1. 需求与本轮之前的真相

目标形态（录音转写三层金字塔）：**端侧实时粗翻（离线、零成本、隐私）→
录完本地高精整段（可选隐私档）→ 云端高精全转（网关路由/审计/归一）**。

勘察结论（2026-10-06，修正 10-03 设计文档 §6 的「现状」描述）：

- `SherpaPlugin.java` 是 Sprint 3 骨架：全部方法 `reject("sherpa-onnx AAR
  not integrated (Phase 4)")`，工程内无 AAR、无模型、无分发逻辑。
  **本地实时粗翻此前是空壳**，实时出字实际全靠云端 3s 定长切片逐片 POST
  （按秒计费、无网不可用）。
- TS 侧管线早已就位且方向正确：
  - `stt.ts` 本地优先（`sherpa.transcribe(audioPath)` → minConfidence 闸
    → 云端兜底）；
  - `recordingRuntime.ts` 的 `startLiveStt()` 先试 sherpa 流式
    （`nativeListening=true` 时**云端分片完全停发**，天然不双计费），
    失败才回落云端切片；
  - 会议链路录完整段重转（`useSessionLiveRecord` → `transcribe-full`）。
- 因此本轮把力气全部花在原生层：原生插件一通，TS 侧零改动即得
  「本地实时 + 云端兜底」。

## 2. 选型（调研结论，2026-10 联网调研）

| 候选 | 结论 |
|---|---|
| **sherpa-onnx**（k2-fsa，Apache-2.0） | **选定**。端侧覆盖最全：流式 zipformer / SenseVoice / VAD / 声纹一体，CPU 友好，Android AAR + iOS + Node + WASM 官方多端分发，openpocket 全栈可用同一引擎 |
| whisper.cpp（MIT） | 精度好但流式是滑窗近实时，端侧包体与速度逊于 zipformer；Node 无官方 addon。云端精转可作上游，不占端侧 |
| faster-whisper / speaches（MIT） | 服务端方案；speaches 暴露 OpenAI 兼容 `/v1/audio/*`，是网关未来上游候选（multipart 透传形态零代码接入） |
| FunASR/SenseVoice（ModelScope 协议，非 MIT） | 模型权重协议需评估；经 sherpa-onnx 导出 int8 使用，随产品分发前过法务 |
| Moonshine（MIT） | 仅英文，排除 |
| parakeet/canary（CC-BY-4.0） | 无中文，排除 |
| FireRedASR | 中文 SOTA 但 GPU 服务器档，非端侧 |
| Qwen3-ASR 0.6B/1.7B（Apache-2.0，2026-01 开源） | 中文开源新标杆；sherpa-onnx 已有 OfflineQwen3AsrModelConfig 配置类，列为网关 GPU 档与端侧后续候选 |

双通道范式（RealtimeSTT / ufal whisper_streaming / Deepgram 等高星项目
的一致结论）：**VAD/端点切句 → 本地小模型即时出字（interim 灰字）→
原始音频送云端大模型出 final（黑字覆盖）**；云端故障时退化为纯本地结果。

## 3. 双引擎与模型

| 引擎 | 模型 | 体量 | 角色 |
|---|---|---|---|
| `zipformer`（流式） | sherpa-onnx-streaming-zipformer-small-bilingual-zh-en-2023-02-16（mobile 包，int8 encoder + fp32 decoder） | 设备占用 ≈50MB，下载 tar.bz2 357MB | 实时出字：AudioRecord 16k 采集 → 100ms 块 → transducer 增量解码 → `partialResult` 事件；端点三规则（2.4s/1.2s/20s）切句 |
| `sensevoice`（整段） | sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17 | 设备占用 ≈227MB | 本地高精档：`transcribe()` 有它走它（自动标点、≈97% 字准）；没有时 zipformer 整段过 |

模型**不进 APK**：首次使用由原生层从 GitHub Releases 下载到
`getExternalFilesDir/sherpa-models/`（`downloadProgress` 事件每 MB 播报），
tar.bz2 解包带 zip-slip 防护。下载体积是已知代价（Wi-Fi 首次触发），
裁剪镜像与「仅 int8 文件的轻量包」列为后续候选。

## 4. 同源精度实测（同一 48s 中文音频，三档对照）

音频 = 小米 TTS 合成的会议纪要样本（ground truth 即合成文本），16k mono：

| 通道 | 结果 | 字准 | 速度 |
|---|---|---|---|
| 云端 `mimo-v2.5-asr`（经网关） | 逐字全对，带标点 | ≈100% | 6.4s（含网络） |
| 本地 SenseVoice int8 | 仅 4 处同音字（端侧→端测/粗转→初转/首字→首次/九成五→9乘5），带标点 | ≈97% | RTF 0.03（M 系 CPU，2 线程） |
| 本地 zipformer 流式 int8 | 同音字较多（转写→转血/一秒→疫苗/验收→蒋燕收），无标点，尾句短截 | ≈85% | RTF 0.03 |

结论直接支撑分层：zipformer 只配当**实时灰字**；本地精翻用 SenseVoice；
最终黑字交给云端。手机 SoC 预计 RTF 0.1-0.5 量级，仍全速实时。

## 5. 实现

- `frontend/android/.../plugins/SherpaPlugin.java`：双引擎初始化、
  `preload/transcribe/startListening/stopListening/status`，
  `extractEmbedding` 留 Phase 5（Web 兜底不受影响）；模型下载 + tar.bz2
  解包 + WAV(16k mono PCM16) 解码；错误带机器码
  （`permission/busy/idle/unsupported_format/invalid_argument/io/engine`）。
  配方按 Android AAR 的 Kotlin 构造签名逐参对齐（javap 核对过 v1.13.8）。
- `frontend/android/app/libs/sherpa-onnx-1.13.8.aar`：官方全 ABI AAR。
  **不进 git**（android/.gitignore 的 `*.aar` 是仓库既有约定）——
  `app/build.gradle` 挂 `downloadSherpaAar` 任务在 `preBuild` 前自动下载
  （已存在则跳过，离线增量构建不受影响）；升级 = 改 URL 与依赖行版本号。
- `app/build.gradle`：AAR（`files()` 依赖）+ `commons-compress:1.26.2`
  （tar.bz2 解包，commons-io/lang3 传递引入）。
- `frontend/src/native/sherpa.ts`：`preload` 收窄为
  `'zipformer'|'sensevoice'`、新增 `status()` 与 `downloadProgress`
  事件、悬空的 2026-07-02 选型文档引用改为本文。
- 调用方零改动：`stt.ts` / `recordingRuntime.ts` / 会议链路按既有
  fallback 顺序自动吃到本地引擎。

## 6. 边界与后续候选

- **iOS / HarmonyOS / Web 无本地引擎**：按既有设计回落云端（HarmonyOS
  有独立构建线；sherpa-onnx 有 iOS 包，接入列为候选）。
- **实时转写的最终一致性**：本地 partial 只进 UI 灰字；整段高精仍以
  「录完重转」为准（会议链路已有），本地/云端 final 的自动覆盖策略
  （全量非空且 ≥ 分段一半才替换）沿用 `meeting-final-transcript.ts`。
- extractEmbedding / 本地 VAD（silero）未接：端点检测已由 transducer
  规则承担；声纹留 Phase 5。
- SenseVoice 权重协议（ModelScope 体系）随产品分发的合规评估挂账。
- 模型下载在国内直连 GitHub 可能慢/失败：候选方案 = 网关/对象存储镜像
  （URL 可配置），本期未做。

## 7. 验证

| 项 | 结果 |
|---|---|
| JVM 同源冒烟（osx-aarch64 jar + 同一组 onnx） | 双引擎 200：zipformer 流式出中文、端点触发；SenseVoice 中文+标点正确 |
| 48s 三档精度对照 | §4 表 |
| Android `assembleDebug` | 本轮验证（见提交信息）；AAR 类签名 javap 逐参核对 |
| TS 门禁 | `typecheck`/`test:stt` 等按 gates.json 跑（提交前） |
| 真机/模拟器端到端（模型下载→实时出字） | 后续候选：需模拟器 x86_64 走模型下载或注入模型目录 |
