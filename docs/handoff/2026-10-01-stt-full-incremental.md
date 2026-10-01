# 2026-10-01 语音转写服务：即时/全量双能力 + 低成本 ASR 选型

> 本文接续 `2026-10-01-stt-gateway-discovery.md`（上一轮建了「网关自动发现 +
> 外部兜底」双通道）。本轮完成三件事：**全量与即时两种转写服务能力**、
> **低成本 ASR 候选调研落地**、**录音语音提示**。并给出**本地小模型可行性**结论。

---

## §1 先说网关：mimo TTS 实测仍然不通（含对照组）

2026-10-01 04:39 与 04:44 两次实测生产网关 `https://llm.kxpms.cn/v1`，
key 取自 `logs/.gateway-key`：

| 探测 | 结果 |
|---|---|
| `GET /models` | 200，**604 → 598 个模型**（两次拉取数量不同，目录正在变动） |
| 对照组 `deepseek-v4-pro` / `glm-4.5-flash` / `gpt-4o-mini` | **全部 200 正常返回** |
| `mimo-v2.5-tts` / `-voiceclone` / `-voicedesign` | **全部 503 `no_candidate`** |
| `mimo-v2.5-tts` 走 `/audio/speech` | 404 |
| `/v1/tts`、`/tts`、`/v1/speech`、`/v1/text-to-speech` | 全部 404 page not found |
| `mimo-v2.5`（普通 chat） | 503 `no_candidate` |
| `POST /audio/transcriptions` | 404（ASR 端点不存在） |

**对照组是这次探测的关键**：三个普通 chat 模型都 200，说明 key 有效、网关正常，
排除了「key 失效」和「网关故障」两种解释。503 响应体里网关自己写明了原因——
`"task_type":"chat"`，即它把 TTS 模型当 chat 模型路由，而 TTS 是不同的任务类型。

**结论**：在当前网关上，mimo TTS 与 mimo ASR 都**没有可达的调用路径**。
`/v1/*` 之外的所有路径（`/openapi.json`、`/docs`、`/health` 等）都返回同一个
1117 字节的 SPA 兜底页，说明 `/v1/*` 是唯一真实路由前缀。

> 因此本轮**没有**把网关 TTS 接入录音提示——没有可用的端点就无法接入。
> 录音提示改走**设备端 TTS**（见 §4），并保留了将来接网关 TTS 的接口形态。

### §1.1 第三轮复验（06:1x，模型目录又变了）

用户两次反馈「确实打通了 mimo 的 tts 模型」。第三轮用新写的
`scripts/verify-gateway-audio.mjs` 重新取证，结论不变，但**多出两条新信息**：

| 探测 | 结果 |
|---|---|
| `GET /models` | 200，**604 个模型**，音频类 6 个 |
| 音频类模型清单 | `mimo-v2.5-asr`、**`mimo-v2.5-tts`**、`-tts-voiceclone`、`-tts-voicedesign`、`gpt-audio`、`gpt-audio-mini` |
| `POST /audio/speech` | **404 `404 page not found`**（16–26ms，路由级不存在） |
| `POST /audio/transcriptions` | **404 `404 page not found`**（76–77ms，路由级不存在） |
| `mimo-v2.5-tts` 走 `/chat/completions` | 503 `no_candidate`，`request_id=c1e1b46ec176514bcfe02a1aa105efad` |
| `mimo-v2.5-asr` 走 `/chat/completions` | 503 `no_candidate`（同样形态） |

**新信息 1：`mimo-v2.5-asr` 出现在目录里了。** 这是上一轮没有的。但它同样
503——**目录里有 ≠ 有上游**。这一点值得单独记住：本项目网关的 `/models` 返回
604 个模型，其中相当一部分没有任何可用上游，所以「下拉框里能选到」绝不能
当作「能用」的证据。

**新信息 2（最有判别力的一条）：400 与 503 的区别就是「名字错」与「没上游」。**
用户口头给的名字是 `mino-v2.5-tts`。我按这个原名直接打，网关回的是**另一个码**：

```
mino-v2.5-tts → HTTP 400 {"code":"invalid_model",
  "message":"Model 'mino-v2.5-tts' is not supported by this gateway"}
mimo-v2.5-tts → HTTP 503 {"code":"no_candidate",
  "message":"No available provider for model 'mimo-v2.5-tts'"}
```

**400 = 网关根本不认识这个名字；503 = 名字对、但没有可用上游。**
这一对结果比任何措辞都有说服力：既排除了「拼写问题导致调不通」，
也排除了「只是没刷新」——真名 `mimo-v2.5-tts` 确实存在，确实被路由，
只是上游为空。

随后把 8 个 mimo 模型**逐个实测**（每个间隔 7 秒避开 12 次/分限流），
**8/8 全部 503 `no_candidate`**，包括 `-asr`、`-tts`、两个 voice 变体、
`-pro`、两个 v2.6。即整个 mimo 家族在当前网关上零可用上游。

> 方法论教训：查「某个东西在不在」时，**一次精确的否定查询**比十次模糊搜索
> 更有价值。`GET /models` 全量拉到 603 个 id，按 `mino` / `mimo` / `m[n]o`
> 三种拼法筛过，`mino` 命中 0 条——这比「我记得目录里没看到」可靠得多。
> 而网关直接回的 `invalid_model` 是权威判据，不需要我们自己推断。

**新信息 2：404 与 503 是两种不同的失败，别混为一谈。** 网关连
`/audio/speech` 与 `/audio/transcriptions` 这两个 OpenAI 兼容音频端点
**根本没实现**（16ms 内返回 404，是路由不存在的即时响应）。所以即使日后有人
把 mimo TTS 的上游配上，只要不同时实现这两个端点，本仓库的 TTS/ASR 代码
**依然调不到它**。这是两件独立的事：① 端点路由；② 模型上游。

> **对照组强度的如实说明**：本次探测时网关的 chat 通路正在劣化——
> `minimax-m3` / `glm-5.3` 先返回 429 `rate_limit_exceeded`（限流 12 次/分），
> 隔 45 秒重试则 90 秒超时。所以本轮的「对照组全绿」证据**弱于**前两轮。
> 但本轮结论不依赖对照组：404 是路由层证据、503 `no_candidate` 是网关**主动
> 返回的结构化 JSON**（自带 `alternatives` 与 `request_id`，说明请求已被网关
> 正常解析并路由，只是找不到上游），两者都不是超时或限流能解释的。

### §1.2 一个比「通没通」更要紧的事实：仓库根本没有 TTS 合成功能

`git grep -i 'audio/speech' -- .` 在**全仓 0 命中**（含前端、脚本、测试），
后端也没有任何一处调用 TTS 端点去合成音频。

`git grep -il 'tts' -- backend` 命中 8 个文件，逐个核实过：
`stt/discovery.go`、`stt/discovery_test.go`、`server/server_stt_settings*.go`
是排除逻辑本身；`email/invoice_*.go`、`migration/prompts.go` 命中的是
`atts`（attachments）子串；`chatagent/seed/agents.json` 命中的是种子 prompt
正文里的偶然字母组合。**没有一处是 TTS 合成功能。**

也就是说：**mimo TTS 通不通，都不在这条任务的关键路径上。** 录音语音提示已经
按你选的方案走**设备端 TTS**，且那条链路是真实存在的——`@capacitor-community/
text-to-speech@8.0.2` 已在 `package.json` 与 `package-lock.json` 中锁定安装，
`recordingRuntime.ts:88` 与 `composables/useSpeech.ts:128` 都在用它。
mimo TTS 唯一可能的用途是「服务端合成提示音」（§4.6 的预生成音频），
那需要新增代码，不是等网关就绪即可用。

### §1.3 复验方法：一条命令

```bash
node scripts/verify-gateway-audio.mjs              # 默认测 mimo-v2.5-tts
node scripts/verify-gateway-audio.mjs mimo-v2.5-asr
```

它会依次打 `/audio/speech`、`/audio/transcriptions`、`/chat/completions`，
打印每条路径的状态码与响应体，并对 200/404/503/429 给出判读，最后
**退出码 0 = 至少一条音频路径可用，1 = 全不可用**，可以直接进 CI 或脚本判断。
网关侧修好之后跑这一条就能确认，不需要重读本文档。

> 写这个脚本时踩了一个坑，值得记下来：key 查找原以为「主工作区是 worktree 的
> 父目录」，实际上 `C:\workspace\openpocket` 与 `C:\workspace\openpocket-wt-stt`
> 是**兄弟**关系（worktree 是主工作区的同级目录）。只往上找一层会得到
> `C:\workspace\logs`，永远找不到 key，表现为「key 缺失」的**假故障**。
> 脚本改为扫同级目录后正常。

---

## §2 ASR 选型：便宜的都在 OpenRouter，流式只有 MiniMax/智谱

### §2.1 总表（成本升序）

| 模型 | 服务商 | 折算 $/小时 | 单次上限 | 服务端流式 | 中文能力 |
|---|---|---|---|---|---|
| `qwen/qwen3-asr-0.6b` | OpenRouter | ≈**0.012** | ~60s | ❌ | 30 语言 + **22 种中文方言** |
| `openai/whisper-large-v3-turbo` | OpenRouter | ≈**0.012** | ~60s | ❌ | 99+ 语言，可取词级时间戳 |
| `qwen/qwen3-asr-1.7b` | OpenRouter | ≈0.027 | ~60s | ❌ | 同 0.6B |
| `gpt-4o-mini-transcribe` | OpenAI | 0.18 | 600s | ❌ | 生态最稳 |
| `gpt-4o-transcribe` | OpenAI | 0.36 | 600s | ❌ | AA WER 0.0396 |
| `asr-1.0` | MiniMax | **0.38** | **500s** | ✅ SSE | zh/yue，20 语种，说话人分离 |
| `glm-asr-2512` | 智谱 | 0.50（¥0.06/分） | **30s** | ✅ SSE | 中文 + 英文 + 8 种方言，热词表 |

**核心取舍（已做进设置页）**：便宜的 ASR **普遍不支持服务端真流式**。
OpenRouter 转写端点官方无 `stream` 参数且上游约 60 秒超时；只有 MiniMax 与智谱
提供 SSE。所以「省钱」与「逐字出字」在当前市场**不可兼得**。

这不是本项目的实现短板，是服务能力的事实约束。因此实现选择是
**分段增量转写**（每 3–15 秒冒出一段），对 7 个候选模型**全部可用**，
而不是把「逐字流式」做成全局开关。

### §2.2 一个必须说清的口径问题

OpenRouter 的 `pricing.prompt` 字段**单位官方未公开**。只有 Whisper 三兄弟可交叉
验证为「美元/秒」（`whisper-1 = 0.0001` = OpenAI 官方 $0.006/分钟），其余模型的
「$/hr = prompt × 3600」是**推断**。

因此代码里 `USDPerHour` 只用于**设置页的量级比较**，不用于结算或预算告警；
`full.go` 里的成本核算也只是估算。真实成本应以响应里的 `usage` 为准。

### §2.3 修掉的一个展示缺陷

原设置页用 `usdPerHour.toFixed(2)` 渲染价格，会把 **$0.012 显示成 $0.01**——
把 0.6B 与 turbo 两个「1 分钱档」压成同一个价格，用户无从比较，而
「尽可能费用少」正是这个字段存在的唯一理由。已抽出 `stt-presentation.ts`
的 `formatCost()` 按量级自适应有效位数，并补了单测锁住。

### §2.4 即时转写如何接进录音链路

笔记录音原本每 3 秒发一片走**单次**转写端点，逐片在本地拼接。3 秒定长切片
必然切在词中间，同一个词会被相邻两片各识别一次，本地拼接得到
「今天今天下午三点」。已改为走即时端点：

| 决策 | 理由 |
|---|---|
| 每场录音生成新 `sessionId` | 跨片去重的累积文本不能跨会话沿用，否则第二场笔记接着第一场显示 |
| 分片**串行**发送（`sliceChain`） | 服务端按到达顺序累积会话文本；并发发送会让响应乱序返回而会话已被改写，表现为网络抖动时偶发跳变丢字 |
| 服务端返回的累计文本**整体替换** | 服务端已去重，本地再拼接等于把边界重复字叠加回去 |
| 定长切片声明 `silenceCut: false` | 3 秒切片是硬切；谎报静音会让服务端按静音边界去重，**吃掉真实的相邻文字** |
| 停止时最后一片带 `isFinal` | `recorder.stop()` 派发的最后一片通常正是用户最后说的那句，漏掉等于丢结尾；带 isFinal 让服务端立刻释放会话而不是等 LRU 淘汰 |
| 停止时等在途分片，上限 10s | 无上限则一个挂死的分片请求会让 `phase` 停在 `stopping`，录音按钮锁死 |
| 单片失败不清空已有文本 | 服务端在单片失败时也会回填累计文本，客户端不应覆盖 |

这 7 条全部由 `recording-voice-prompt.test.mjs` 的「即时转写接线」套件
（8 个源码级契约用例）锁死。

---

## §3 全量转写：为什么必须自己切段

调研确定了一个硬事实：**没有任何 ASR 允许无限长音频单次上传**
（智谱 30 秒 / OpenRouter ~60 秒 / MiniMax 500 秒）。所以「把整场两小时会议
丢给上游」从来就不是一个能工作的请求。

`internal/stt/full.go` 的实现要点：

| 决策 | 理由 |
|---|---|
| 按**静音边界**切，不按固定长度切 | 固定切会把词劈成两半，两侧各丢一半信息（中文常见代价是每段 1–3 个错字） |
| 无静音点时**硬切**兜底 | 连续讲话（没有静音）必须能切，否则整次请求撞上限失败 |
| 静音判定用 **25ms 滑动窗口 RMS**，不用单帧 | 单帧在纯音过零区振幅≈0，会误判静音并切出 0.15s 碎片；窗口必须覆盖至少一个完整基频周期（40Hz 远低于任何语音基频） |
| 切点**持续更新**到静音中点 | 只在阈值那一帧设一次会把切点钉在静音开头，产出「段头紧跟大段静音」的畸形切分 |
| 段长 < 1s 的纯静音段**切出但跳过转写** | 不切会丢音频（早期实现因此既丢音频又算错段数）；切出后跳过既省一次计费调用又保留可审计性 |
| 单段独立 90s 超时 | 用整体 ctx 的话第一段慢会让后面所有段一起超时 |
| 逐段失败**不中断**整体，失败段留显式占位 | 两小时会议第 37 段网络抖动，不该让前后 156 段成果消失 |
| `Succeeded == 0` 才报错 | 失败占位符让 `Text` 永不为空，用 `TrimSpace(Text)` 判空会导致**全失败也返回成功**，上层会把占位文本当会议内容存库 |

`buildWAV` 踩过的坑：必须**同时**改写 RIFF 块总长（偏移 4）与 data 段长度两个
字段。只改后者会产出「声明长度 ≠ 实际长度」的坏 WAV（实测切出的段声明
384000 而实际 68802 字节）。已由 `TestSplitWAVSegmentsAreValidWAVAndLossless`
逐字节守死（各段载荷之和必须等于原载荷）。

---

## §4 录音语音提示（设备端 TTS）

需求：「在录音时，需要用扬声器播放一段语音，不是警告声。」

### §4.1 为什么不是提示音

录音场景里提示音有致命歧义：听到一声短促滴声，用户无法判断是「录音已经收进去了」
（提示音被录进成品）还是「设备出错」。更糟的是提示音本身会被录进音频，
变成会议记录开头的一处噪音，且用户往往回放时才发现——那时会议已记完、无法补救。
语音「开始录音」四个字是自解释的。

### §4.2 一个必须处理的副作用：播报会被麦克风录进去

直觉实现是「采集照常跑、扬声器照常响」，于是这四个字成为会议记录的第一句。
三种解法：

- A. 先播报完再开录：语义最干净，但每次启动多等 1–2 秒，会议里很别扭。
- B. 照常录，转写后正则剔除：脆弱（换文案就漏），且噪音已留在音频里。
- C. **本实现**：MediaRecorder 照常跑（chunk 时序连续、不丢开头），只把 mic
  track 置 `enabled = false`，播报结束或 `guardMs` 到期后恢复。采到静音帧，
  VAD 与转写自然忽略。

`guardMs` 是**安全兜底不是优化**：TTS 引擎的 onend 可能不触发，没有兜底就会让
麦克风永久静音——那比录进四个字严重得多。

### §4.3 队列用 generation 序号而非布尔标志

`stop()` 需要 `clear()` 后再 `announce('stop')`。若用布尔 `dropped` 标志，
`announce` 会把它重置回 false，队列里排着的「继续录音」就会被念出来——
录音已经结束，用户却听到「继续录音」。布尔无法区分「入队时的状态」与「当前状态」，
代号可以。单测 `clear() 丢弃排队内容` 逼出了这个设计。

### §4.4 触发点

| 时机 | 播报 | 静音麦克风 |
|---|---|---|
| 会议录音开始 | 开始录音 | ✅ |
| 会议录音结束（finally） | 录音结束 | — 麦克风已拆 |
| 会议/笔记启动失败 | 录音出错 | — |
| 笔记开始 | 开始录音 | ✅（3s 分片会立刻送第一片） |
| 笔记结束 | 录音结束 | — |

### §4.5 播报不可用时必须**看得见**

播报失败是静默的：没有 TTS 引擎时 `announce()` 直接返回，用户听不到任何声音，
只会认为「这功能没做」或「手机坏了」。国内 ROM 常移除或禁用 Google TTS
（无 GMS 设备尤其常见），所以这不是理论风险。

`probeVoicePromptSupport()` 暴露引擎探测结论，设置页 `/settings/stt` 顶部
「录音语音提示」区块显式展示；不可用时用警示色并说明「本设备未提供语音引擎」，
让用户能区分「设备问题」与「应用问题」。

### §4.6 一个待决项：预生成音频

调研的结论是 5 句固定提示语**预生成 wav 随包**比接本地模型更划算
（约 50–250KB vs 73–311MB），详见 §5.2。本轮**未实现**，因为它需要
一次性的音频生成步骤（选定音色 + 生成 + 提交二进制），且当前设备端 TTS
已满足需求。若真机上发现系统 TTS 缺失或延迟过高，这是首选替代路径，
`RecordingVoicePrompt` 的 `speak` 依赖注入点已经为它留好了位置。


---

## §5 本地小模型可行性结论

需求：「检查有无可能将 tts 的小模型放到本地够执行（或者手机上）。」

### §5.1 现状：本地能力为零

`SherpaPlugin.java` 全部方法都是 `call.reject("sherpa-onnx AAR not integrated")`——
纯骨架，assets 目录无任何模型文件。所以这不是「模型选得不好」，是**尚未接入**。

### §5.2 结论：提示语不要上本地模型，改用**预生成音频**

对「录音开始/结束」这种 **5 句固定短句**：

| 方案 | APK 体积 | 首次延迟 | 离线 | 结论 |
|---|---|---|---|---|
| 系统 TTS（当前实现） | 0 | 取决于设备 | 依赖设备有中文语音包 | 兜底，保留 |
| **预生成 wav/mp3 随包** | **~50–250 KB** | <50ms | ✅ 完全离线 | **✅ 本需求最佳** |
| sherpa `matcha-icefall-zh-baker` | **73 MB** | 未核实 | ✅ | 不划算 |
| sherpa `vits-melo-tts-zh_en` | **163 MB**（MIT） | 未核实 | ✅ | 不划算 |
| sherpa `kokoro-multi-lang-v1_1` | **311 MB**（Apache-2.0） | 未核实 | ✅ | 不划算 |

- **预生成音频省掉 73–311 MB**。为 5 句话付这个体积不成立。
- 句子是常量：改文案 = 重新生成 wav，CI 里一条命令。比维护 sherpa 集成简单一个数量级。
- 可用 MeloTTS（MIT）或 matcha-baker **离线生成一次**，运行时不带模型。
- 保留系统 TTS 作为兜底（预生成文件缺失时回退）。

**注意许可**：Piper 中文 `zh_CN-huayan` 的 MODEL_CARD 明确写
`License: Unknown`，**不可用于商用**；`matcha-icefall-zh-baker`、`aishell3`、
`sherpa-onnx-vits-zh-ll` 的权重在 HF 上 license 字段为空。明确可商用的只有
MeloTTS(MIT) 与 Kokoro(Apache-2.0)。

### §5.3 转写：本地只适合「草稿层」

| 方案 | 体积 | RTF | 成本 |
|---|---|---|---|
| `streaming-zipformer-zh-14M` | ~25 MB (int8) | 0.04–0.15（设备未标注） | 0 |
| `paraformer-zh` int8 | 227 MB | macOS 0.073 | 0 |
| `SenseVoice` int8 | 226 MB | 未核实 | 0 |
| 云端 `qwen3-asr-0.6b` | 0 | 网络 | **$0.0108/小时** |

**成本临界点（明确为估算）**：$0.0108/小时 ⇒ 1000 小时 = $10.8/用户/年。
个人录音 app 日录音量通常 <1 小时（≈$4/年）。**除非「离线/隐私」是硬需求，
纯经济账上云端几乎总是赢。**

分界线建议：**短句/命令词**（<10s）→ 本地流式 zipformer 做草稿；
**长录音** → 结束上传云端精修。这正是 `stt.ts` 里既有的
「local-first + cloud-fallback」策略，只是 local 层从骨架变成真实模型。

集成路径已确认可行：sherpa-onnx Kotlin API 支持从文件路径加载模型，
**意味着模型可以首启下载，不必打进 APK**。工作量估计 1–3 人日
（拷 so 到 jniLibs + 写 Java Plugin 类）。

---

## §6 顺带修掉的既有缺陷

本轮为了让 STT 测试能跑，暴露并修掉了 4 个**与本任务无关但真实存在**的缺陷。
它们都在改动的必经路径上，且都影响生产行为：

1. **`GET /api/stt/config` 必然 panic**（生产 bug）
   `resp.Gateway` 在缓存未命中时为 nil，紧接着无条件调 `resp.Gateway.Best()`。
   而用户**首次打开设置页时缓存必然未命中**——这不是边缘情况，是
   「STT 设置页从来没被成功打开过」。已加判空。
2. **TTS 模型被误判为 ASR 候选**
   `asrNameRe` 含 `voice`，于是 `mimo-v2.5-tts-voiceclone`（音色克隆）等
   纯合成模型全被当作转写候选。它们**探测可能成功**（TTS 端点收到音频能返回 200），
   于是设置页把 TTS 模型标成「转写可用」。已加 `ttsNameRe` 排除，
   并用 `strongASRRe` 让 `whisper-tts-hybrid` 这类混合模型仍被保留。
3. **幻觉守卫漏判一种英文说法**
   正则有 `not (?:see|…)` 与 `didn't (?:receiv|get|hear)`，但漏了
   `I don't see any audio file attached to this message.`（走 `don't` 分支）。
   漏拦的代价是把幻觉写进用户会议记录。已补。
4. **单测会真打生产网关**
   `Server.sttHTTPClient` 字段声明了却从未被使用，两处硬编码
   `gatewayHTTPClient(...)`，所以测试真的出网打 llm.kxpms.cn（上一轮实测 2.3s、
   打完 6 个候选的 503 并吃到 429 限流）。已加注入点并让生产路径真正使用它。
5. **模板标签配对错误只有构建能发现**
   编辑设置页模板时吃掉了一个 `<section>`/`<label>` 开标签，
   `vue-tsc --noEmit` 仍然 exit 0（它只查类型、不查标签配对），
   只有 `vite build` 报 `Invalid end tag`。已补源码级配对断言提前拦截。
   该断言已做**负控对照**：人为删掉一个 `</section>` 时确实转红，恢复后转绿。
6. **无 PG 部署下整个 STT 功能不可用**（本轮最严重的一个，靠黑盒抓到）

   `PUT /api/stt/config` 在没有 `POCKET_POSTGRES_DSN` 的部署上恒返回 400
   `{"error":"user settings store unavailable"}`——`pocketd` 在该模式下正常启动
   （remote-only）但 `s.userSettings` 为 nil。

   后果不是「设置存不下来」这么轻：**整个 STT 功能对用户不可用**。连设置页的
   试转、连手工填外部 key 的通道都进不去，因为根本没有地方存；后续所有转写请求
   都以「未配置 API Key」失败。

   > 上一轮 handoff（§5.4 第 3 条）**声称**已用进程内兜底存储 `sttMemSettings`
   > 修掉，并写明「功能可用，重启回默认值」。但那份实现**并未落在代码里**。
   > 这是「不能仅凭声明认为功能完成」的一个实例：单测之所以没抓到，是因为
   > `internal/server` 的 STT 用例全部通过 `newWorkspaceIsolationServer` 构造，
   > 那个构造**总是**给 `userSettings` 赋值——测试环境与无 PG 部署的差异正好
   > 落在这个盲区里。

   修法：`sttSettingsRepo()` 统一入口，PG 优先、回落 `usersetting.MemStore`
   （已有实现且带锁，不自己造）。边界已在注释里写明：**重启即丢**，不制造虚假
   持久化预期。覆盖它的 `TestSttSettingsWorkWithoutPGStore` 做了负控对照：
   把 fallback 改回原样 → 转红并复现 400；恢复 → 转绿。

---

## §7 验证

### §7.1 后端

| 范围 | 结果 |
|---|---|
| `internal/stt` | **ok**（含 full/incremental/discovery 全部用例） |
| `internal/server` | **ok**（含 2 个新端点的 10 个用例） |
| `go build ./...` | exit 0 |
| 全后端 `go test ./...` | **50 包 ok，2 包 FAIL** |


两个失败包是 `internal/agent` 与 `internal/email`，与本任务**零关系**——
`git status` 确认本轮改动**一行都没碰**这两个包，失败原因是明确的 Windows 环境限制：
`%1 is not a valid Win32 application`（.sh 脚本无法在 Windows 执行）、
`key file mode: got 666, want 0600`（Windows 无 POSIX 权限位）。

> 对照实验：同样两个包在**未改动的 `main` 基线上通过**。差异来自基线不同
> （我的 worktree 基于 `consolidate/2026-10-01`），不是本轮引入的回归。
> 为此我顺手把基线里那两处 `toast.error(... err.message)` 的原始错误上屏
> 修成了 `apiError(err, ...)` 包装——并行会话已在 `main` 上修过同样两行。

### §7.2 黑盒验证：真实进程的即时/全量端点（22/22 PASS）
`scripts/verify-stt-stream.ps1` + `scripts/fake-asr.mjs`：**22 PASS / 0 FAIL**。

为什么单测不够：本轮两个新端点走的是**真实 HTTP 链路**——鉴权中间件、请求体
上限中间件、JSON 解码、目标解析、multipart 上行、会话状态机。`httptest`
单测只覆盖 handler 内部，一旦某个中间件把请求挡掉，handler 单测照样全绿。
所以必须起真的 pocketd 进程打真的端口。

用本地假 ASR 的原因：真正可用的外部 ASR key 在本机不可达（`api.openai.com`
i/o timeout），网关侧 2026-10-01 实测也没有任何 ASR 上游。**这能验证链路
正确性，不能验证识别质量**——后者要靠设置页「录 3 秒试转」。

| 检查 | 结果 |
|---|---|
| pocketd 真实启动 + 登录取 token | ✅ |
| PUT config 指向 loopback 假上游 | ✅ key 未回显、只有 `hasExternalKey`、无承载明文凭据的字段 |
| 全量：60 秒音频 | ✅ 切成 **5 段**全部成功、含逐段明细、聚合含每段文本、**无段超过 30 秒**、按时间有序 |
| 即时：分段增量 | ✅ 跨片累积（非替换）、`isFinal` 透传、**新会话不串台**、缺 `sessionId` 被拒 |
| 鉴权 | ✅ 两个端点未认证均被拒 |

**这个黑盒验证抓到了一个单测抓不到的真实 bug**：没有 PG 时的设置保存失败会让
整个 STT 功能不可用，详见 §6 第 6 条。

### §7.3 前端

| 检查 | 结果 |
|---|---|
| `vue-tsc --noEmit` | **exit 0** |
| STT 相关 `node --test`（6 个文件） | **77/77 通过** |
| `assert-no-plaintext-backend` | ✓ 守卫通过 |
| `vite build` | ✓ built in 20.89s，exit 0 |

> 负控对照：模板配对断言已验证「人为删掉一个 `</section>` → 转红、恢复 → 转绿」，
> 确认它不是空跑。

本轮的实质改动：

- `stt-settings.ts` 增 `transcribeFull` / `transcribeIncremental`，并新增
  `SttFullResult` / `SttIncrementalResult` 类型。
- **笔记录音的 3 秒分片改走即时端点**（原来走单次转写 + 本地拼接，
  必然在切点重复识别同一个词）。串行发送、累计文本整体替换、
  停止时最后一片带 `isFinal`——8 条契约测试锁死。
- 录音停止兜底从 `sttApi.transcribe`（单次）换成 `transcribeFull`（带切分），
  超时从 20 秒放大到 10 分钟；部分段失败时明确提示「有 N 段未能转写」。
- `filenameForMimeType` 从 `stt.ts` 抽到 `stt-filename.ts` 供三条转写路径共用
  （两处各写一份映射表会导致「单次能转、全量转不出」）。
- `formatCost` 修掉了 `toFixed(2)` 把 $0.012 显示成 $0.01 的失真。
- 设置页新增「录音语音提示」的引擎可用性展示（播报失败是静默的）。

### §7.4 验证脚本自身踩的三个坑（记录下来免得重犯）

1. **PowerShell 5.1 读无 BOM 的 .ps1 会按 ANSI 解码**，中文变乱码导致
   `ParserError`。既有 `verify-stt.ps1` 首字节是 `239,187,191`（UTF-8 BOM），
   新脚本必须同样带 BOM。
2. **`-not $x -match 'p'` 的优先级陷阱**：PowerShell 里一元 `-not` 比比较
   运算符结合更紧，所以它等价于 `(-not $x) -match 'p'`——判断的其实是
   「JSON 串非空」，永远为真。本轮因此**误报了一次「响应泄露明文 key」**，
   差点去改一段其实安全的代码。改成 `[regex]::IsMatch($json, 'test-key')`
   后才确认响应里只有 `hasExternalKey: true`。
3. **响应必须带 `charset=utf-8`**：`Invoke-RestMethod` 在 JSON 没有 charset 时
   按本地 ANSI 解码，中文断言全挂。假 ASR 因此显式返回
   `application/json; charset=utf-8`——这本来也是任何正经 JSON API 该做的。

### §7.5 未能验证（如实列出）

1. **真机未验**：本机无 `adb` 与模拟器镜像，所以「真机录音 → 语音播报 →
   麦克风静音 → 停止 → 转写」这条链路**没有设备证据**。麦克风静音保护
   目前只有源码级与单测背书。
2. **外部 ASR 一次成功转写都没有**：`api.openai.com` 在本机网络不可达
   （`i/o timeout`）。已验证的是链路正确（配置保存 → 目标解析 → 真实发出请求
   → 失败原因如实上浮），**没有**验证过一次真实的成功转写与真实费用。
3. **设置页真实渲染未验**：仓库没有组件测试基建（无 vitest / @vue/test-utils /
   jsdom，`node --test` + 源码断言是既有约定），页面渲染与交互只有源码契约测试。
4. **Android 上的模型 RTF 无一手数据**：官方只有树莓派 4 的基准，
   无任何中文模型在 ARM64 手机上的实测。§5.3 的分界线建议因此是工程判断，
   不是实测结论。

### §7.6 负控对照清单（每条都实测过「改坏就红」）

绿灯本身不说明护栏有效。逐条把被测逻辑改坏，确认测试真的转红：

| 护栏 | 负控做法 | 结果 |
|---|---|---|
| `TestSttSettingsWorkWithoutPGStore` | 移除 `sttSettingsRepo()` 的 MemStore 回落 | 转红，复现 400 |
| 设置页模板配对断言 | 删掉一个 `</section>` | 转红 |
| TTS 排除 `mimo-v2.5-tts` | 从 `ttsNameRe` 移除 `tts` | 转红（见下） |

**最后一条本身是个反面教材，值得单独记。** 我最初加的断言是
`{ID: "mimo-v2.5-tts", Modality: "text"} → false`，理由是「裸 tts 也该排除」。
跑负控时它**没有转红**——因为 `asrNameRe` 本来就不匹配 `mimo-v2.5-tts`，
它被 `asrNameRe` 之外的路径挡住的概率与被 `ttsNameRe` 挡住无从区分，
所以这条断言**恒绿、零保护力**。改成 `Modality: "audio"` 后才有判别力：
只有 `ttsNameRe` 生效时才会返回 `false`，一旦 `tts` 那个分支失效，
`modality=audio` 会立刻把它拉成 `true` 并让测试转红。

> 结论：**「我加了一条测试」不等于「我加了保护」。** 断言必须能在
> 逻辑改坏时转红，否则它只是让覆盖率报表好看一点。负控就是用来发现这种
> 恒绿断言的——所以它不能省。

---

## §8 后台录音（app 切后台不停止）——2026-10-01 追加

需求：「整个录音应该是可后台运行的，app 切换到后台也不能停止。」

### §8.1 先纠正我自己的一个错误结论

排查时我先看仓库根的 `android/` 目录（只有 `README.md` / `package.json` /
`capacitor.config.ts`），于是**断言**「原生工程不存在、`BackgroundMic` 插件
没有实现」。**这是错的。**

真正的原生工程在 `frontend/android/`，66 个文件全部在版本库里，包含
`BackgroundMicPlugin.java`、`MeetingRecordService.java`、`MainActivity.java`
等 16 个源文件。根 `android/` 只是一个**从未被使用的占位目录**。

教训与 §1.1 同源：**「我看了某个目录」不等于「我看过这个东西」**。
断言「不存在」之前，应该先用能证伪的方式查（`git ls-files` 全量列举、
按名字全仓搜），而不是在一个恰好同名但无关的目录里没找到就下结论。
本轮两次犯的都是这个毛病，一次是模型名（`mino` vs `mimo`），一次是目录。

### §8.2 原生侧本来就已经具备的能力

`MeetingRecordService` 是一个 `foregroundServiceType="microphone"` 的前台服务，
用 `AudioRecord` 以 16kHz 采集，每 8 秒切一片 WAV 通过 `partReady` 事件
回传 JS。`AndroidManifest.xml` 里 `RECORD_AUDIO`、`FOREGROUND_SERVICE`、
`FOREGROUND_SERVICE_MICROPHONE`（Android 14+ 必需）、`POST_NOTIFICATIONS`
全部齐备，Java 侧也正确用了三参
`startForeground(..., FOREGROUND_SERVICE_TYPE_MICROPHONE)`。

**所以「生成 Android 工程」这件事不需要做——工程早就在。**
我一度准备跑 `npx cap add android`，结果 CLI 回答
「android platform already exists」，才暴露出这个误判。

### §8.3 真正的问题：后台录音会**静默失效**

既有实现有一处典型静默失效。插件的 `start()` 是这样：

```java
ctx.startForegroundService(i);
call.resolve();      // ← 立刻 resolve，不等服务真的开始
```

但服务随后可能**当场就死**，而且不会通知任何人：

| 失败点 | 现象 |
|---|---|
| `RECORD_AUDIO` 未授予 | `startForeground(..., TYPE_MICROPHONE)` 抛 `SecurityException`，进程崩 |
| 麦克风被电话/其他应用占用 | `AudioRecord` 状态异常，录音线程直接死 |
| 服务重启（`START_STICKY`） | `intent` 为 null，**录进一个 `meetingId=""` 的幽灵会议** |

这三种情况下 JS 侧都已经 `nativeMode = true`，于是不再挂「切后台将停止录音」
的告警，用户看到的是「正在录音」的通知、界面显示正常，**一整场没有任何声音**。
这正是「不能仅凭声明认为功能完成」要防的那类问题：UI 说成功，功能是空的。

### §8.4 改了什么

1. **`start()` 变成有结论的 Promise。** 服务侧真正开始采音后才
   `reportStart(true)`，失败 `reportStart(false, 原因)`；插件侧
   `settlePendingStart()` 据此 resolve / reject，另有 **3 秒兜底**，
   服务在回报前崩溃也不会让 JS 无限等待。
2. **先查权限再拉服务。** 插件侧用
   `ContextCompat.checkSelfPermission(RECORD_AUDIO)` 提前判，
   把「权限没给」变成一条明确的 reject，而不是让它在服务里变成无声崩溃。
3. **启动失败不留空壳。** 新增 `failAndQuit()`：回报失败 → 撤下「正在录音」
   通知 → `stopSelf()`。否则服务已经 `startForeground` 过却没有录音线程，
   用户会一直挂着一个永不消失的「正在录音进行中」通知。
4. **`START_STICKY` 重启不再产生幽灵录音。** `intent == null` 时直接
   `stopSelf()` 并回报失败。
5. **事件统一回主线程。** `emitPart` / `emitError` 改为投递到插件的
   `Handler`，避免从 worker 线程碰桥接状态；同一队列保证分片先后顺序不变。
6. **前端不再吞掉失败原因。** `startBackgroundMic` 返回
   `{ ok, reason }` 而不是裸 `boolean`；`recordingRuntime` 记下原因，
   用户切后台时看到的是
   「切到后台将停止录音（后台录音不可用：<真实原因>）」而不是一句
   没有信息量的「请保持应用在前台」。

### §8.5 验证

`frontend/src/native/__tests__/background-mic-contract.test.mjs`：**18/18 通过**，
覆盖插件名两端对齐、start 语义、失败不留空壳、幽灵录音、Android 14+ 合规
前置条件、失败原因可见。

负控对照 **5/5 有效**（把被测逻辑改回去，确认测试转红）：

| 负控 | 结果 |
|---|---|
| 在 `startForegroundService` 后加回 `call.resolve()` | 转红 |
| 删掉 `intent == null` 守卫 | 转红 |
| 删掉 `failAndQuit` 里的 `stopForeground` | 转红 |
| 把 catch 分支改回 `return { ok: false }` | 转红 |
| 切后台提示里去掉失败原因 | 转红 |

> 第 4 条**第一次负控是无效的**：我最初只断言「文件里出现过 `reason:`」，
> 结果把 catch 分支的 reason 删掉后，另一行
> `return { ok: false, reason: '后台录音插件未注册' }` 仍能让它全绿。
> 改成锚定 catch 分支本身才真正有判别力。这是本轮第二次抓到恒绿断言。

全量复验：`vue-tsc --noEmit` exit 0；STT + 录音相关 7 个测试文件
**98/98 通过**；`assert-no-plaintext-backend` 通过；`vite build` built in 26.31s；
黑盒 `verify-stt-stream.ps1` **22/22 PASS**（实时增量 + 全量切段聚合未受影响）。

### §8.6 没能验证的（必须说清）

**Java 代码一次都没有编译过。** 本机 `ANDROID_HOME` 指向
`C:\Users\86133\AppData\Local\Android`，但该目录下**没有 `Sdk` 子目录**，
即**未安装 Android SDK**；也没有 gradle 命令。因此：

- 无法 `./gradlew assembleDebug`，**语法错误与类型错误都发现不了**。
  我只做了配平检查（`{}` 与 `()` 计数相等）与逐行人工复核——
  这**不能替代编译**。首次真机构建前请预期可能需要修编译错误。
- 无法在真机/模拟器上验证「切后台仍在录音」。本机无 `adb`、无模拟器镜像。

**真机验证步骤**（需要 Android SDK + 设备）：

```bash
cd frontend
npx cap sync android
cd android && ./gradlew assembleDebug
# 装到设备后：
# 1. 首次进会议录音，确认通知栏出现「正在录音」常驻通知
# 2. 按 Home 切后台，等 30s 以上
# 3. 回前台：应能看到已转写出的文字（说明后台期间服务仍在推 partReady）
# 4. 通知栏应有常驻通知；切后台期间录音指示器应保持
# 5. 撤销 RECORD_AUDIO 权限后重新开始录音 → 应**明确失败并提示**，
#    而不是显示「正在录音」却无声
```


