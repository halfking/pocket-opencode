# 转写模板化：把「怎么发」从 switch 里拿出来，以及 MiniMax 直调的实测契约

> 日期：2026-10-08
> 目标：语音转写做成**可配置的模板化**，后台能选不同模式处理（含 MiniMax 原生 API 直调）
> 交付：`backend/internal/stt/provider.go` + `provider_openai.go` + `provider_zhipu.go`、
> `server/server_stt_settings.go`（第四通道）、`frontend` 设置页模板选择

---

## 1. 起点：二维矩阵表达不了真实差异

接手时的形态是 `Channel(auto/gateway/external) × Transport(auto/transcriptions/chat-audio/sse)`。
这个矩阵描述的是「**去哪儿**」，而各家 ASR 的真实差异有一半在「**怎么发**」：

| | 路径 | 语种参数 | 粒度参数 | 响应 |
|---|---|---|---|---|
| OpenAI 兼容 | `/v1/audio/transcriptions` | 表单字段 `language` | `timestamp_granularities[]` | `{text, segments[]}` |
| **MiniMax** | `/v1/speech_to_text` | **HTTP 头** `language` | **`timestamp_level`** | `{text, duration, n_speakers, segments[], trace_id}` |
| 智谱 | `/v4/audio/transcriptions` | 表单字段 | — | SSE 事件 `transcript.text.delta` |

这三行没法用「换一个 Transport 常量」表达：路径、参数名、参数**位置**、响应结构全都不同。
硬塞进矩阵的结果就是每加一家就往 `switch` 里加一个 `case`，case 里 80% 是不相干的样板 ——
上一轮就是这样长出来的（`transcribeFor` 里 100 多行 switch + 三家各自的 if）。

于是改成：**每个上游服务 = 一个 Provider**（`BuildRequest` / `ParseResponse` / `ParseStream`），
由注册表按 id 选择。新增一家 = 新增一个文件 + 注册表加一行，不碰既有分支。

`chat-audio` **故意留在 switch 里不进注册表**：它打的是 `/chat/completions` + `input_audio`，
响应是 OpenAI 风格的 `choices[].message`，不是任何 ASR 的识别结果结构。
硬塞进注册表会让 `Provider` 接口背上「有的实现不走 ASR 端点」的别扭。

## 2. MiniMax 直调的实测契约（2026-10-08，本机直连）

测试音频是真实语音，不是构造的静音样本：
`macOS say -v Tingting` 合成中文 → `afconvert` 16kHz 单声道 WAV，**7.086s / 225KB**。

> ⚠ 过程记录：第一次用 `say -v Flo` 只产出 **0.016 秒**，差点当成「转换坏了」。
> `afinfo` 显示 audio packets=352 才定位到是 TTS 侧没合成出内容，换 `Tingting` 得到 7.09 秒。
> ⇒ 音频夹具必须先验时长，否则「0.016 秒的静音」会被当成有效样本打给上游，
> 而上游对一段近静音仍可能返回 200 与一段幻觉文本。

`POST https://api.minimax.cn/v1/speech_to_text`，`Authorization: Bearer <key>`：

| 请求 | 结果 | 耗时 |
|---|---|---|
| `language: zh` + `response_format=json` | 200，文字**逐字正确**，`duration=7.086` | 0.84s |
| + `response_format=verbose_json` | 200，`n_speakers=1`，`segments[0].speaker="S1"` | 1.30s |
| + `timestamp_level=word` | 200，segments 按**字**切，每段仍带 `speaker` | — |
| `stream=true` | 200 `text/event-stream`，`index` 递增，`finish=true` 事件带 `duration` | — |
| `stream=true` + `verbose_json` | **400** `response_format "verbose_json" cannot be used with stream=true (2013)` | — |
| 错 key | **401** `authorized_error (1004)` | — |
| 不传 `language` | 200（走混合语种识别） | — |

连同文档（`https://platform.minimax.cn/docs/api-reference/speech-to-text.md`），
得到四条硬事实：

1. **`language` 必须放请求头。** 放表单字段不报错，但**不生效**。
   ⇒ 这是本仓最贵的一个 bug 形态：任何「状态码 == 200」的判据都抓不到它。
2. **词级时间戳参数名是 `timestamp_level`**（OpenAI 侧是 `timestamp_granularities[]`）。
   发错名字同样 200、同样不报错，只是不给词级时间戳。
3. **流式与 `verbose_json` 互斥**（上游 400 明确拒绝）。
   ⇒ 「边出字」与「说话人分离」在这个上游上不能同时要，
   设置页因此做成 **radio 二选一**，不是两个 checkbox。
4. **单词录音也回 `speaker="S1"` + `n_speakers=1`。**
   ⇒ 判据不能是「`n_speakers>1` 才算已分离」（那会把单人会议标成未分离），
   也不能照抄 OpenAI 那套。判据是「上游回填了带 speaker 的 segments」。

## 3. 判据：钉「不报错但静默失效」的三处

判据选的**不是**「能不能转出文字」——那太宽，恒真断言就能过。选的是上面 1/2/3 三处，
每处都配一个**变异**（把正确值改错），要求测试变红：

| # | 判据 | 变异 | 结果 |
|---|---|---|---|
| M1 | `language` 走请求头、不进表单 | 改成只写表单 | 红（两条判据同时指出「请求头为空」） |
| M2 | 词级时间戳用 `timestamp_level` | 改成 `timestamp_granularities[]` | 红（错误信息带出实际字段表） |
| M3 | 流式时强制 `response_format=json` | 删掉强制覆盖 | 红（实际发出 `verbose_json`） |
| M4 | 上游拒分离时降级重试 | 删掉 `isDiarizationRejection` 分支 | 红（503 直接冒到用户） |
| M5 | SSE 终止事件的 `duration` 要取到 | 遇 `finish` 就 return | 红（duration=0） |
| M6 | 未知模板 id 显式报错 | 回退到默认模板 | 红（错误里没有那个 id） |

★ **M1 的第一版是崩不是红**：把 `WriteField` 写在 `w.Close()` 之后，
导致 `ContentLength` 与 body 长度不符，`net/http` 直接报
`ContentLength=32530 with Body length 32647`。
那证明的是「我改坏了 HTTP 报文」，不是「判据抓到了 language 位置错误」。
改成在 `Close()` 之前写入才是有效变异。

## 4. 一次判据自己被推翻（智谱事件名）

我先写的判据是「智谱的流喂给 MiniMax 解析器必须拿不到文本」，理由是
「两家事件名不同，所以解析器不该认得对方的流」。

**实测不成立**（两个方向的读数）：

```
智谱流   → 智谱解析器     text="智谱"    duration=0
智谱流   → MiniMax解析器  text="智谱"    duration=0    ← 能凑出文本
MiniMax流 → MiniMax解析器  text="我们"   duration=7.086
MiniMax流 → 智谱解析器     text=""       duration=0
```

不对称的原因是字段名：智谱事件的 `delta` 字段恰好也叫 `delta`，
所以 MiniMax 的「读 delta 就拼」照样吃得下；反过来 MiniMax 事件没有 `type` 字段，
智谱按事件名分派就落空。

⇒ 我那条判据要求了一个**不存在**的严格性。改成钉**能区分两家的那个方向**
（MiniMax 流喂智谱必须为空），它仍然能区分两家，但不再要求产品做无谓的严格。
「凑巧兼容」这件事写进了 `provider.go` 的注释，免得后人误加事件名校验 ——
加了并不会更安全（拿到的仍是正确文本），却会让流里夹一行别的格式直接失败整条转写。

## 5. 实跑暴露的静默失效：分离开关从未生效

单元测试全绿、curl 也全绿，但**本仓代码路径**的实跑读数是：

```
diarized=false segs=0        ← 明明请求了 verbose_json 能力
```

根因：`ModelOption.asr-1.0` 的 `Diarization` 字段**缺省为 false**
⇒ `SupportsDiarization("asr-1.0")` 返回 false ⇒ `ShouldRequestDiarization` 永远不开分离。
用户勾了「说话人分离」也拿不到标签，而界面不会报任何错。

补上 `Diarization: true` / `WordTimestamps: true`（均有实测支撑）后复跑：

```
diarized=true segs=29        ← 按字级时间戳切分，每段带 speaker
```

★ 这条是本轮**最值得记住的一点**：
**curl 能证明「服务端能用」，证明不了「本仓的请求构造是对的」。**
判据若只钉在响应侧，这个缺陷会一直活着。
判据 `TestMiniMaxCapabilitiesDeclared` 现在钉的是「能力声明被真正消费」。

顺带修掉一个默认值错误：`asr-1.0` 预置的 `BaseURL` 原本是**国际站** `api.minimaxi.com`。
本仓用户拿国内 key 打过去会 401，而错误文案是
「login fail: Please carry the API secret key…」，指向「key 没带」而**不会**指向「打错了站」。
已改为 `api.minimax.cn`，并由 `TestMiniMaxDefaultBaseURLIsDomesticStation` 钉住两处常量一致。

## 6. 后台可选模式

- `sttSettingsPayload` 新增第四通道 `minimax`（不是 external 的一个模型 ——
  三处协议差异塞进 external 只能靠 if 区分，那正是要消灭的形态）。
- MiniMax 的 key 存**独立记录 id**（`stt/minimax`），与外部 key 分开：
  两者是不同凭据，用户常只配其一；共用一条会让「清掉外部 key」连带清掉 MiniMax 的。
  前端语义也不同：外部 key 是「空串 = 清空」，MiniMax key 是「**不传 = 不改动**」——
  因为保存设置时会带上用户没改过的字段，把「没提交」当「清空」会导致改一下语种就丢两把 key。
- `auto` 的回落顺序改为 **网关 → MiniMax → 外部**（MiniMax 插在外部之前）：
  $0.38/h 但 500 秒整段 + SSE + 分离齐全，而外部默认档不支持流式。
  前端「当前生效」提示同步改成同一顺序 —— 提示与行为相反会让人照错误预期排查。
  ⚠ 只有**已配 MiniMax key** 才进这条路，没配时完全不影响原有行为。

## 7. 顺带修掉的一处判据脆弱性（z-index 门禁）

给 `SettingsSTT.vue` 加样式后，`z-index-ladder.test.mjs` 变红：
它有一条 `positionAt('features/settings/SettingsSTT.vue:634')` 的自证断言，
而 `.top-bar` 的块起始行漂到了 800。

查下来这个 key **已经这样咬过四次**（文件头记着前三次：604→634 等），
每次的处理都是「把手填行号改回去」——治标。

本轮改成**规则块起始行**，与 `bottom-chrome-gate.test.mjs` 早已在用的约定一致。
残留的脆弱性如实记账在文件头：块起始行只在**规则块增删**时漂，
真正的稳定锚点是选择器；`collect()` 现在也产出 `selector` 字段，
但换 key 形态需要同时处理多行选择器与嵌套写法，本轮没一并做。

## 8. 真机验证（真实 pocketd + 真实 MiniMax key）

进程内测试过了**不等于**功能可用。本节全部读数来自真实二进制
（`go build ./cmd/pocketd` → 独立实例 `POCKET_HTTP_PORT=8135` → 真实 HTTP 请求 → 真出网）。
刻意**没有**复用 8124 上那个既有实例，避免影响另一个会话。

| # | 验证项 | 端点 | 读数 |
|---|---|---|---|
| 1 | 模板清单真从注册表出来 | `GET /api/stt/config` | 3 个模板；MiniMax `exclusive:true` `maxSeconds:500`；4 个 channelHints |
| 2 | 配置真持久化 | `PUT` + `GET` 回读 | `channel/provider/minimaxStream/hasMiniMaxKey` 全部一致 |
| 3 | 未知模板**保存时**被拒 | `PUT` | 400，且列出三个可选 id |
| 4 | 试转（SSE 模板） | `POST /api/stt/probe` | 200，文字逐字正确，`transport=sse`，1.67s |
| 5 | 长录音全量 | `POST /api/stt/transcribe-full` | 200，1 段 0 失败，`costCents=0.0748`（$0.38/h × 7.086s 吻合） |
| 6 | 增量跨片去重 | `POST /api/stt/transcribe-incremental` | 第 2 片 `delta` 只含新增，边界无重复 |
| 7 | **说话人分离** | `POST /api/stt/probe`（transcriptions） | `diarized=true`、**29 段**、全部带 `S1` |
| 8 | 互斥兜住 | 存「两个都为真」后打真上游 | **200 + transport=sse**，不是 400 (2013) |
| 9 | 错 key | 同上 | `MiniMax 鉴权失败（401）…请检查设置里的 Key` |

第 7 项与第 5 节的单元测试对照：修复前是 `diarized=false segs=0`。

### 8.1 真机抓到的第二个缺陷：「清除 API Key」从来没生效过

第 9 项之后的收尾步骤要清掉 key 好恢复干净环境，结果发现：

```
① 存真 key          → hasMiniMaxKey = True
② 发 __clear__      → hasMiniMaxKey = True   ← 缺陷
```

**这不是本轮新增功能的问题，而是既有 `__clear__` 机制一直是坏的。**
一层层挖下去有三个独立原因，缺任何一个都修不掉：

1. **保存层顺序**：先 `saveSTTSettings`（把字面量 `"__clear__"` 当真凭据写进存储），
   再走清理分支。第一次请求清理时读到的已经是空串，所以**第一次碰巧成功**，
   之后每次请求都重复「先写回哨兵」⇒ 永远清不掉。
2. **存储层语义**：`usersetting.Put` 把 `Secret == ""` 解释为「**本次不改动密文**」
   （`MemStore.Put` 与 PG `Put` 同义，见 `mem_store.go` / `store.go`）。
   ⇒ 「传空串表示清空」这个写法，**从来就没生效过**。
3. **LWW 把同秒的第二次操作丢弃**：`DecidePut` 用**秒级**时间戳且等值即 `DecisionKeep`。
   ⇒ 即便前两处修好，用户「保存设置 → 紧接着清 key」这个**正常操作序列**，
   只要落在同一秒内，第二次就会被整条丢弃。

修法（三个都要）：

| 层 | 改法 |
|---|---|
| HTTP | 哨兵在**入口**归一化（`normalizeMinimaxKeyInput` / `normalizeExternalKeyInput`），清空与普通保存走同一条路径，没有「先存后清」的中间态 |
| 存储 | `Record` 增加 `ClearSecret` 字段，让「清空」有独立于空串的表达；`rejectSentinelKey` 防哨兵字面量落库 |
| LWW | `DecidePut` 对 `ClearSecret` **永远返回 Apply** —— 撤销凭据是安全动作，时钟分辨率不该有否决权 |

★ 记忆点：**「显式清空」与「未提交」必须由两个不同字段承载。**
共用一个空串，就注定了其中一个永远表达不出来。

判据：`server_stt_credentials_test.go` 6 条 + `mem_store_test.go` 1 条。
其中「连发三次 `__clear__`」那条是关键 —— 该缺陷**结构上只在序列里显形**
（第一次写、第二次清、第三次又写），单次请求的断言抓不到它。
三个变异全部验证会红：

| 变异 | 结果 |
|---|---|
| MemStore 不遵守 `ClearSecret` | 红（`Secret 应为空，实际 "sk-real"`） |
| `DecidePut` 不给豁免 | 红（`Applied=false`） |
| STT 层回到「传空串=清空」 | 红（第 3 次仍为 true） |

复验：重启后连续两次 `__clear__` → `hasMiniMaxKey=False`，
且清空后试转报的是「**未配置 API Key**」而不是误导性的 401。

### 8.2 顺带修掉的两处真机环境问题（非产品缺陷，记以免复现）

- env 文件里 `POCKET_AUTH_PASS` **出现两次**，后者覆盖前者 ⇒ 直接读文件拿到的是错误口令。
- `say -v Flo` 只合成出 **0.016 秒**音频（换 `Tingting` 得 7.09 秒）。
  ⇒ 音频夹具必须先验时长，否则「近静音样本」会被上游返回 200 + 一段幻觉文本。

## 9. 验证读数

| 项 | 结果 |
|---|---|
| `go build ./...` / `go vet ./...` | 通过 |
| `go test ./...`（后端全量） | 通过 |
| `go test ./internal/stt/` | 通过（含 6 个已验证会红的变异） |
| `go test ./internal/server/ -run 'TestMinimax\|TestExternalClear\|TestSentinel'` | 6/6 通过（3 个变异已验证会红） |
| 实跑 `TestLiveMiniMaxEndToEnd` | text 逐字正确，1.08s |
| 实跑 `TestLiveMiniMaxStream`（SSE） | text 逐字正确，1.40s |
| 实跑 `TestLiveMiniMaxBadKey` | 401 → 「鉴权失败（401）…请检查设置里的 Key」 |
| **真机**（真实 pocketd:8135 + 真实 key） | 9 项全部符合预期，见 §8 表 |
| `npm run test:all`（前端全量） | 3113 项，308/308 文件全执行 |
| `npm run gates` | **42/42 通过** |
| `npx vue-tsc --noEmit` | 通过 |

实跑探针在 `backend/internal/stt/live_minimax_probe_test.go`，**用 build tag 隔离**
（`//go:build stt_live_minimax`）：

```bash
STT_LIVE_MINIMAX_KEY='sk-api-…' POCKET_STT_LIVE_AUDIO=/path/to.wav \
  go test ./internal/stt/ -tags stt_live_minimax -run TestLiveMiniMax -v
```

不用 env-skip 而用 build tag：「读到 key 就跑」会让任何人在本地跑 `go test` 时
意外花钱，CI 上更不该有出网调用。build tag 让「要不要花钱」变成显式选择。

## 10. 未验证 / 遗留（如实记账）

- **智谱 provider 未经实机验证。** 它按 2026-10-01 的文档调研实现，本轮没拿到智谱 key。
  因此 `sttTemplateViews()` 如实标 `supportsStream=false`，不给用户一个会失败的开关。
  拿到 key 后应把读数补进 `provider_zhipu.go` 的注释。
- **`ClearSecret` 在 PG 路径上未经实机验证。** 本轮真机实例是 **remote-only 模式**
  （无 `POCKET_POSTGRES_DSN`），走的是 `MemStore`。
  PG 的 `store.go` 侧已按同一语义改好并有编译与单测覆盖，
  但**没有连真 PG 跑过** —— 「本地能清、线上不能清」正是这类分叉的典型形态，
  接 PG 后应复验一次。
- **`chat-audio` 未模板化**（有意，见 §1）。
- **z-index 门禁的 key 仍是行号**（块起始行），未换成选择器（见 §7）。
- **价格与能力是快照**：`asr-1.0` 的 $0.38/h 来自官方定价页直标（无换算歧义），
  但仍需按官方文档复核；`RecommendedExternalModels` 的整体复核日是 2026-10-06。
