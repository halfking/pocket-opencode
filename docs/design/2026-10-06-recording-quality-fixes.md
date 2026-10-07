# 录音转写质量：片段重复、识别错误率、收尾无精校

> 2026-10-06 · 面向用户实测反馈的三条质量问题
> 后端：`backend/internal/stt/incremental.go`
> 前端：`meeting-final-transcript.ts` / `useSessionLiveRecord.ts`

## 0. 结论先行

用户反馈三条症状，**三个根因，全部在代码里坐实**，不是主观感受：

| 症状 | 根因 | 位置 |
|---|---|---|
| 片段重复 | **前端整条链路没有任何去重**；后端 `mergeIncremental` 属于无人调用的路径 | `ingest-speech.ts` / `stt/incremental.go` |
| 识别错误率高 | 收尾把**已经错了的文本**丢给 LLM 润色，而不是回到音频重转 | `useSessionLiveRecord.ts` `stop()` |
| 完成时没有精细纠错 | 同上：注释里写「停止时用 TranscribeFull 做一次全量校正」，但**从未被调用** | 同上 |

第二条和第三条是同一个根因的两个说法。

> ⚠️ **本轮最重要的发现**（第一版分析错了）：我最初以为「片段重复」是
> `mergeIncremental` 的精确匹配判据不够好，于是把它改成 LCS 对齐并做了
> 5 处变异验证。改完才发现——**前端根本不调用 `IncrementalTranscriber`**。
> 前端走的是 `VadSegmenter → 每段 sttApi.transcribe → ingestSpeechBlob
> 原样存 segment → '\n' 拼接`，全程零去重。
> ⇒ 第一次修的是一条**没人走的路径**，用户看到的重复一点没少。
>
> 这类错误只有靠「读盘核实真实调用链」才能发现，单元测试全绿不会告诉你。

## 1. 调研：讯飞听见这类产品怎么做

| 项目 | 星标 | 可借鉴点 |
|---|---|---|
| [FunASR](https://github.com/modelscope/FunASR) | 18k+ | **两阶段模式**（先流式出字，再用离线模型整段修正）；官方部署矩阵明确支持该模式 |
| [Meetily](https://github.com/Zackriya-Solutions/meeting-minutes) | 31.5k | 本地会议纪要，Parakeet/Whisper 实时转写 + 说话人分离 |
| [WhisperX](https://github.com/bhargaviparanjape/whisperX) | 23k+ | 词级时间戳 + 说话人分离 |
| [whisper.cpp](https://github.com/ggml-org/whisper.cpp) | 53k+ | 本地推理 |
| [OpenWhispr](https://github.com/OpenWhispr/openwhispr) | 9k | 桌面听写，BYOK |
| [WhisperLiveKit](https://github.com/QuentinFuxa/WhisperLiveKit) | 8.8k | 优化 VAD 与音频切片降低延迟 |

**核心结论：所有成熟方案都是两阶段的**——录音时用流式/短段保证"马上看到字"，
结束后用整段音频重跑高精度模型并**替换**流式结果。讯飞听见、Otter、Granola
都是这个架构。

本项目已经写好了第一阶段（`incremental.go` 的短段 + 重叠 + 去重），
而且**注释里明确写了**「停止时再用 TranscribeFull 做一次全量校正」——
但这一步从未被实现。这正是缺口所在。

## 2. 根因一：片段重复（前端从未去重）

### 真实链路（逐行核对 VadSegmenter 后确认）

```
VadSegmenter.finalizeSegment()
  └─ 取片窗口 [speechStartMs - sliceMs(250ms), endMs]   ← sliceMs 用来补偿
                                                          MediaRecorder 缓冲延迟
  └─ 每段独立 POST /stt/transcribe
  └─ ingestSpeechBlob() → saveSegment() → 原样存一条 MeetingSegment
  └─ updateTranscript(segments.map(s => `[${s.speakerLabel}] ${s.text}`).join('\n'))
                                                    ↑ 零去重
```

重复的产生机制：

- `sliceBuffer` 是**累积队列**，只按 `atMs >= cutoff(60s前)` 滚动；
- `speechStartMs` 来自 `requestAnimationFrame` 的能量判定。**RAF 在后台标签页
  会被节流到 1fps 以上**，语音起点的判定因此会**回退**；
- 一旦相邻两段的取片窗口重叠，同一段音频被转写两次
  ⇒ 「今天今天下午三点」。

### 为什么第一版修错了地方

`stt/incremental.go` 里早有一套去重（`mergeIncremental`），但它属于
`IncrementalTranscriber`——**前端从不调用它**。前端走的是逐段
`sttApi.transcribe`，两条路径完全独立。

第一版把 `mergeIncremental` 从「逐字相等」改成「LCS 对齐 + 覆盖率闸」，
并做了 5 处变异验证（后端测试确实全绿）。但**它不在真实路径上**。

> 教训：单元测试全绿 ≠ 修到了用户看到的问题。
> 必须先读盘核实「这段代码在不在实际调用链上」。

### 修法：把同一套算法接到前端

新增 `frontend/src/features/meetings/meeting-dedup.ts`：
- `dedupeSegmentText(committed, next)` — 相邻两段去重；
- `dedupeTranscriptParagraphs(texts)` — 全段落序列去重；
- `renderTranscript(segments)` — 逐段渲染，**净增留在原说话人名下**；
- `dedupeSegments(segments)` — 返回**去重后的副本**（不改原数组）供 LLM 消费。

### ★ 重复不止在「转写全文」那一行（第二版补上的）

只修 `updateTranscript` 是**不够的**。重复文本会顺着 `segments` 流进四处：

| 出口 | 影响 | 状态 |
|---|---|---|
| `ingestSpeechBlob` → `updateTranscript` | 转写全文出现「今天今天」 | 已修 |
| `recordingRuntime.appendText` → `updateTranscript` | **本地 sherpa 实时字幕是另一条录音入口**，同样原样拼接 | 已修 |
| `useLiveSummary` → `/summary` + `/recommend` | 重复进 LLM ⇒ 摘要、关键点、行动项都被污染；还会让 `topicShift` 主题漂移判断失准 | 已修（`dedupeSegments`） |
| `api/meetings.ts` 三条降级路径 | 网络不通时正好走这里，重复照样进 prompt | 已修 |
| `MeetingDetailView.displaySegments` | 整页消费者（渲染/重新总结/待办转交）共用 | 已修（在 computed 源头去重） |

用户说的「质量不行」不只是转写那一行，是**整条内容链路**。

### 阈值：只留一道闸

| 闸 | 是否独立有牙 | 处置 |
|---|---|---|
| `ANCHOR_COVERAGE = 0.6` | ✅ 0.6→0.0 红 5 条；0.6→0.5 红 2 条 | 保留 |
| `MIN_ANCHOR = 4` | ❌ 只放宽它（4→1）全绿；与 coverage 同时放宽才红 | **删除** |
| `maxNetNewRatio`（后端） | ❌ 数学上不可能触发 | **删除**（第一版） |

`MIN_ANCHOR` 只是 coverage 的一次快速短路（L=2 时覆盖率最多 2/n，远低于
0.6），没有独立作用。留着会让人误以为「有两道闸在防误删」。

### 精确匹配的固有局限（记录，未改）

精确匹配**先于** LCS 执行，会吃掉「用户真的重复说的话」：

```
dedupe('今天我们讨论了预算和排期', '预算和排期都要再确认一次')
→ '今天我们讨论了预算和排期都要再确认一次'   ← 「预算和排期」被吃掉
```

这是**原实现就有的行为**，不是本轮引入。它与「切片重叠」在形态上无法区分
（都是「新段以旧段结尾开头」），要修需要引入时间戳或置信度信息，超出本轮
范围。已在此记录，不静默留着。

### 变异验证（前端去重）

| 变异 | 失败数 |
|---|---|
| M1 去掉 LCS 模糊去重 | 3 |
| M2 coverage 0.6→0.0 | 5 |
| M3 coverage 0.6→0.5 | 2 |
| M4 `renderTranscript` 不去重 | 2 |
| M5 净增改为固定长度裁剪 | 3 |
| M6 `recordingRuntime.appendText` 退回原样拼接 | 1 |
| M7 `/recommend` 用回未去重 segments | 2 |
| M8 `api/meetings.ts` 降级路径退回拼接 | 2 |

> 过程中还修了**三个自己的错误**：
> 1. 净增断言写成 `containsAll(...)` 而非精确相等 ⇒ 固定长度裁剪变异蒙混过关；
> 2. coverage 的负控用例被**精确匹配抢先命中**，压根走不到 coverage 闸，
>    导致把 0.6 调到 0.0 仍然全绿。必须让重叠区含差异字符（多一个逗号）
>    才能构造出只压 coverage 闸的样本；
> 3. 变异还原时用了 `/tmp/b1` 这个**已存在的目录名**做备份，`cp` 失败导致
>    变异体残留在 `recordingRuntime.ts` 里。是靠"基线应当全绿"这条断言
>    发现的——否则会带着一个坏掉的实现继续往下走。

### 后端 mergeIncremental 保留

它属于 `IncrementalTranscriber`（将来的真流式通道），本轮不动。
但阈值与前端 `meeting-dedup.ts` 互相标注，**改一处必须同步另一处**。

## 3. 根因二/三：识别错误率 + 没有精细纠错

### 原来的收尾流程

```
录音停止 → 把分段累积的文本 → POST /refine → LLM 润色 → refinedTranscript
```

**问题**：LLM 只能看到已经错了的字符。它不知道音频里原本说的是什么，
所以「润色」修不了：

- 同音字专有名词（"里程" vs "李林"、"财报" vs "才报"）
- 数字、金额、日期
- 切片边界被切断的词

这些是**识别错误**，不是表达问题。润色工具再强也修不了。

### 修法：补上两阶段的第二阶段

```
录音停止 → 从 IndexedDB 取回完整音频 → POST /transcribe 整段重转
        → 采纳判定 → 把高精度文本交给 /refine 做分段/标点/去口水词
```

基础设施本来就是齐的，只差接线：

- 完整录音在 `recordingRuntime.stop()` 时已 `saveMeetingAudio(meetingId, fullBlob)` 落 IndexedDB；
- 后端 `POST /api/meetings/{id}/transcribe` 直接吃音频字节、跑同一套 STT 管线。

**为此改了后端一处**：该端点此前只返回 `{"status","meeting_id"}`，不返回文本，
前端拿不到重转结果。已加 `"transcript": result.Text`（避免前端再 GET 一轮）。

### 采纳判定：不能无脑替换

全量重转**不保证**比分段结果好——用户中途可能换了更差的模型，长音频可能被上游
截断。所以加了 `shouldAdoptFullTranscript`：重转文本比分段文本短一半以上 ⇒
**拒绝采用**，沿用分段结果。

> 宁可让用户看到几个错字，也不要让内容突然少一半——后者用户会以为录音坏了。

### 降级：绝不清空结果

全量重转是**增强**不是必需。所有失败路径（音频不存在、请求失败、超时、
返回空）都返回 `{applied:false}` 并**回落到分段文本**，函数**不抛异常**。
不能让"精校"把用户的录音结果搞丢。

### 变异验证

| 变异 | 结果 |
|---|---|
| M1 移除全量重转调用 | 1 个失败组 ✓ |
| M2 移除采纳判定 | 1 个失败组 ✓ |
| M3 顺序反转（先精校后重转） | 1 个失败组 ✓ |

## 4. 为什么不引 FunASR / whisper.cpp

- 本项目走的是**云端 STT 网关**（`stt/transcribe.go` + 用户可配通道），
  架构是"多供应商可切换"，换成本地引擎会推翻这层抽象；
- FunASR 的价值在**模型与部署矩阵**，而本项目缺的是**两阶段的编排**——
  那是纯工程编排，与用哪个模型正交。先补编排，模型层留给用户配置；
- 若日后要接本地引擎，正确的落点是新增一个 STT 通道，而非改这条编排链路。

## 5. 验证

| 项 | 结果 |
|---|---|
| `go test ./...`（后端全量） | 0 失败 |
| `go test ./internal/stt/` | 全绿（含 5 处变异验证） |
| `npm run test:all` | 2268 pass / 0 fail / 244 文件全执行（连跑 3 次一致） |
| `npm run gates` | 32/32 |
| `vue-tsc --noEmit` | 0 错误 |
| 新增门禁 `meeting-dedup.test.ts` | 23 例，8 处变异验证 |
| 新增门禁 `meeting-final-transcript.test.ts` | 11 例，3 处变异验证 |

## 6. 尚未覆盖（诚实说明）

- ⚠️ **CI 出现过一次无法复现的 `test:all` 失败，成因未定。**
  现象：`run-mjs-tests.mjs` 报「跑到了但一个用例都没产出：
  `src/utils/__tests__/app-version-display-coverage.test.mjs` —— 等于没测」，
  gates 第 3 项因此退出码 1。
  已做的排查与结果：
  | 动作 | 结果 |
  |---|---|
  | 单独跑该文件 | 6/6 通过 |
  | `test:all` 连跑 6 次 | 2268 pass / 0 fail |
  | 人为加 4 个 CPU 占用进程后再跑 2 次 | 2268 pass / 0 fail |
  | 检查 reporter 的 `isFileLevel` | 只认 `.mjs` 不认 `.ts`，但实测 66 个 `.test.ts` 的 census 计数无一为 0，**不是成因** |
  | 读取 census 落盘逻辑 | `writeFileSync` 在 stream `flush` 里，非并发写，无明显竞态 |
  ⇒ **未能复现、无法定位**。不假定它与本轮改动无关（也未找到与本轮改动的关联）。
  下次出现时的定位方法：立即留存完整 `npm run test:all` 输出，
  检查 census JSON 里该文件的事件数是 0 还是缺失（前者=判据误报，后者=真没跑）。
- **未做真机复测**。本轮全部验证是单测 + 变异测试，没有真实录音跑一遍。
  真实 ASR 的输出分歧形态比单测构造的更杂，`ANCHOR_COVERAGE=0.6`
  可能需要按真机数据再调。
- **精确匹配的固有局限未修**（见 §2）：它会吃掉「用户真的重复说的话」。
  要修需要时间戳或置信度信息，已记录未静默留着。
- **未接说话人分离**。FunASR/WhisperX 的 diarization 是下一步的质量提升点，
  本轮只解决重复与错误率。
- **门禁的"顺序"断言是源码文本顺序检查**（`iFull < iRefine`），不是运行时
  时序验证。它能防回退，但不等于验证了真实执行顺序。
- **前后端各有一份去重实现**（Go 与 TS），阈值需要手工同步。
