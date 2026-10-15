# 端上实时转写可行性结论（2026-10-06 ASR 多模型轮）

**结论先行：实时转写用部署在手机端的流式模型可行，且应作为实时档首选；
云端（网关多供应商）承担高精全量与后处理，两端互补而非二选一。**

## 1. 证据

### 1.1 出字及时性（本轮实测，桌面 arm64 JVM 逐参同构探针）

探针 `scripts/sherpa-latency/LatencyProbe.java` 与
`SherpaPlugin.java` 实时路径逐参同构（100ms 块 / 16k / 三端点规则 /
modified_beam_search beam=4 / int8 zipformer-small 双语）：

| 指标 | 读数（9.44s 中文样本） | 判读 |
|---|---|---|
| TTFP（语音起点→第一条非空 partial） | **514ms** | 会话字幕「即时」体感线（≈800ms）以内 |
| partial 节奏 | 91 帧 / 9.44s ≈ **每 100ms 一帧** | 字幕持续刷新，无长空窗 |
| 尾延迟（语音结束→端点判停 final） | **≈1ms**（流内已判停） | final 不拖尾 |
| RTF（尽快喂入） | **0.0197**（186ms 处理 9.44s） | 桌面 CPU 余量 ~50× |

端上口径（上一轮 instrumented 实测，arm64 模拟器 Android 16）：zipformer
**RTF 0.12**、SenseVoice 离线 1.3-1.5s/段。桌面与端上差 ~6×，即使中低端
真机再慢 3-5×，RTF 仍在 0.36-0.6 区间——**低于 1.0 实时线，可行**。

### 1.2 精度分层（同源样本）

| 档 | 引擎 | 字级相似率 | 标点 | 延迟 |
|---|---|---|---|---|
| 端上实时 | zipformer-small int8 流式 | 97.8%（1 同音替代：实→时） | 无 | partial 每 100ms |
| 端上高精 | SenseVoice int8 离线 | **100%** | 有（ITN 开） | 190ms/9.4s（桌面）· 1.3-1.5s（端上） |
| 云端高精 | 网关 mimo-v2.5-asr / minimax-asr-1.0 | 100%（2026-10-06 矩阵，EN 词级亦 100%） | 有 | 1.4-2.8s 往返 |

### 1.3 形态结论（三层金字塔落地口径）

1. **实时字幕档 = 端上 zipformer 流式**：零网络依赖、零按秒计费、隐私
   （音频不出端），出字 500ms 级。已有插件实现 + instrumented 4/4 绿。
2. **段落终稿 = 端上 SenseVoice**（离线、带标点）或**云端整段高精**
   （录音结束后一次性）。两者选择由「是否允许出网 + 网络质量」决定，
   stt.ts 的本地优先→置信度闸→云端兜底链路已覆盖。
3. **云侧多供应商**（本轮新增）：minimax-asr-1.0 / glm-asr 接入网关后，
   云端档不再单点依赖小米；精度矩阵见 `scripts/verify-gateway-audio-multi.mjs`。

## 2. 已知短板与对策

| 短板 | 实测表现 | 对策 |
|---|---|---|
| 流式无标点/无 ITN | 「时时转写」同音替代 + 裸文本 | final 段过网关 `/v1/audio/refine`（标点+ITN+热词字形，本轮已上线） |
| 流式不挂热词 | 专名靠模型通用知识 | zipformer 支持 hotwords 参数（createStream 传词表），插件未配——列为候选，网关 refine 的 hotwords 已可先行兜底 |
| 内存 | zipformer+SenseVoice 双模型包 ~600MB | 按需下载（插件已有 downloadProgress）；低配机可只装 zipformer |
| 端上读数口径 | 桌面 RTF 0.02 ≠ 端上 RTF 0.12 | 一切端上容量评估按 0.12 口径（模拟器），真机 首验后更新 |

## 3. 反面约束（何时不选端上实时）

- 通话/外放回采音频：端上 VAD 未接（silero 未集成），回采噪声会拖累
  端点规则；此时用云端流式（minimax SSE）更稳。
- 多语种长尾（ja/ko/yue 之外）：zipformer-small 双语只覆盖 zh/en。
- 需要说话人分离的实时输出：端上实时档无此能力（extractEmbedding 留
  Phase 5）；云端 asr-1.0 有分离但非实时逐字。

## 4. 记录

- 探针运行配方与复现命令：`scripts/sherpa-latency/README.md`。
- 本轮云端矩阵：`scripts/verify-gateway-audio-multi.mjs`（六段取证）。
- 桌面 JVM 同构测量与端上 instrumented 的关系：桌面提供**快速迭代口径**，
  端上提供**发布口径**；两者读数差异（6×）已在上表钉死。
