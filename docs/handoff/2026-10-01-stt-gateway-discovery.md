# 2026-10-01 会议/笔记录音转写：网关自动发现 ASR + 设置页可调

> 目标：修正会议与笔记的录音功能；通过网关自动寻找能录音转文字的 LLM，
> 从网络调研出最适用的 ASR 模型，再从 `llm.kxpms.cn/v1` 的模型列表里挑
> 2-3 个精度高成本低的候选，放进系统设置界面，允许手工调整。
> 完成后用 Maestro 方法做部署与测试验证。

---

## §1 先说结论：网关今天**一个可用的 ASR 都没有**（实测）

这一节是整份文档的地基。下面每一条都是 2026-10-01 对生产网关
`https://llm.kxpms.cn/v1` 的真实请求结果（key 来自 `logs/.gateway-key`，
探测脚本 `scripts/gw-audio-probe.mjs`）。

| 探测 | 结果 | 含义 |
|---|---|---|
| `GET /v1/models` | 200，**604 个模型**，每条带 `modality` 字段 | 目录里有 ≠ 能用 |
| `modality` 分布 | text 475 / multimodal 108 / vision 13 / embedding 6 / **audio 2** | 只有 2 个 audio |
| `modality=audio` 的两个 | `gpt-audio`、`gpt-audio-mini` | 候选 |
| ASR 命名但被标成 text | `mimo-v2.5-asr` | **只看 modality 会漏掉它** |
| `POST /v1/audio/transcriptions` | **404 page not found** | 网关没有 OpenAI 兼容转写端点 |
| `gpt-audio` / `gpt-audio-mini` | **503 `no_candidate`**「No available provider」 | 列了但没有上游 |
| `mimo-v2.5-asr` | **503 `no_candidate`** | 同上 |
| `nemotron-3-nano-omni-30b-a3b-reasoning` | **503 `no_candidate`** | 同上 |
| `auto` + base64 音频 → `/v1/chat/completions` | **200，但音频被静默丢弃**，模型答「您好，您似乎没有附上录音文件。请重新上传需要转写的音频」 | ⚠️ 最危险的一条 |
| 网关限流 | `x-ratelimit-limit: 12` | 探测必须限速、必须缓存 |

### §1.1 最危险的一条：200 + 幻觉文本

`auto` 模型收到我们塞进去的 base64 音频后，**正常返回 200**，正文是
`<think>…</think>您好，您似乎没有附上录音文件。请重新上传…`，`usage` 里
`total_characters: 0`。

如果按最直觉的方式实现（调 chat/completions，拿到 200 就取 content 当转写），
用户会得到一段含「您似乎没有附上录音文件」的**会议记录**，而且全程零报错。
这正是 `stt.LooksLikeMissingAudio()` 存在的理由：命中即判失败。

### §1.2 由此确定的产品决策

原始要求里「从网关模型列表里挑 2-3 个可用的」在今天**做不到**——列表里
一个能用的都没有。与其假装挑到了，不如把两件事都做掉：

1. **自动发现机制本身要做对**：网关哪天开通了 `gpt-audio` 或 `mimo-v2.5-asr` 的
   provider，设置页点「重新扫描」就能看到，**不需要改代码、不需要发版**。
2. **今天要真能用，就得给一条外部通道**：任何 OpenAI 兼容
   `/audio/transcriptions` 的服务（OpenAI / Groq / 硅基流动 / 阿里百炼…），
   独立一把 key，独立模型选择。

已与用户确认走「双通道」方案（网关自动发现 + 外部兜底），设置页两组预置
分组展示，全部可手工改成任意模型名。

---

## §2 网络调研：精度高 + 成本低的 ASR 候选

调研时间 2026-10，来源与可信度逐条标注。**未知就写未知**——成本看板悄悄
归零比显示「未知」危险得多（与 `quota/pricing.go` 里 "default" 兜底价
的注释同一个道理）。

| 模型 | 价格（$/1k 分钟） | 精度依据 | OpenAI 兼容 `/audio/transcriptions` | 来源 |
|---|---|---|---|---|
| `gpt-4o-mini-transcribe` | 3.0 | AA 多语 WER 0.0447 | ✅ | ✅ 官方定价页 |
| `gpt-4o-transcribe` | 6.0 | AA WER 0.0396 | ✅ | ✅ 官方定价页 |
| `gpt-4o-transcribe(-diarize)` | 6.0 | AA WER 0.0396 | ✅ | ✅ 官方 |
| Whisper large-v3 | 6.0 | AA WER 0.0406 | ✅ | ✅ 官方 |
| Groq `whisper-large-v3-turbo` | **0.667** | AA WER 0.0462 | ✅ | ⚠️ 二手（AA），下单前以控制台为准 |
| Deepgram Nova-3（预录·多语） | 5.2 | AA WER 0.0518 | ❌ | ✅ 官方 |
| AssemblyAI universal-3-5-pro | 3.5 | AA WER 0.0312 | ❌ | ✅ 官方 |
| Google Gemini 3.5 Transcribe | 5.0 | AA WER 0.0260 | ❌（走 generateContent 内联音频） | ⚠️ 二手 |
| 阿里 Fun-Realtime-ASR | 未查到 | **AA WER 0.0173（榜内最佳）** | ❌（自有 HTTP/WS） | ⚠️ |
| 阿里 Qwen-Audio-3.1-ASR-Flash-Filetrans | 未查到 | 官方称支持中文方言/热词/说话人分离 | ❌ | ✅ 能力页，价格未取到 |
| `gpt-audio` / `gpt-audio-mini` | 音频 in $32 / $10 每百万 token | ❓ | ❌ | ❓ |

**未能核实**：Groq 官网单价、阿里 ASR 具体单价、腾讯/火山/讯飞定价（页面为
SPA 未渲染出数字）、全部中文 CER 榜单（Open ASR Leaderboard 的仓库地址已 404）。

**重要口径提醒**：AA-WER 是 Artificial Analysis 自建的**多语混合**指数，
**不是中文 CER**。中文精度只能从厂商能力（方言、热词、说话人分离）间接推断。

### §2.1 落到设置页的 3+3 预置

网关组（来自 `/v1/models`，即原始要求「从网关模型列表里挑」的那部分）：

1. `gpt-audio-mini` — 网关 `modality=audio`，mini 档最省
2. `mimo-v2.5-asr` — 网关 ASR 模型（目录里被错标成 text）
3. `gpt-audio` — 网关 `modality=audio`，通用档

外部组（网络调研出的高精度/低成本组合）：

1. `gpt-4o-mini-transcribe` @ $0.18/小时 — 低成本档，日常会议/笔记够用
2. `whisper-large-v3-turbo` @ $0.04/小时 — 最低价档，速度因子高，适合长录音
3. `gpt-4o-transcribe` @ $0.36/小时 — 高精度档，方言/噪声/多人

网关三项在设置页**显示真实探测状态**（当前全是「网关无上游 provider」），
不可用的候选单选框直接禁用。理由见 §1.2。

---

## §3 实现

### §3.1 后端：`internal/stt` 从「写死 Groq」改成「按用户解析目标」

改造前：`Transcriber` 里硬编码 `https://api.groq.com/openai/v1` +
`whisper-large-v3-turbo`，key 来自 `POCKET_GROQ_API_KEY`（用户没给），
所以线上一直是 503 `stt_unavailable`。

| 文件 | 职责 |
|---|---|
| `internal/stt/target.go` | 通道（auto/gateway/external）、传输形态（transcriptions/chat-audio）、6 个预置模型与成本 |
| `internal/stt/discovery.go` | 网关模型目录拉取、候选筛选、**逐个真实探测**、探测结论分类、缓存、幻觉识别 |
| `internal/stt/transcribe.go` | 按目标执行转写：两种传输形态 + 空文本判失败 + 幻觉守卫 + WAV 时长/成本核算 |
| `internal/server/server_stt_settings.go` | 设置存取、目标解析、discover / probe handler |
| `internal/server/server_stt_settings_test.go` | 13 个用例（含「单测不出网」） |
| `internal/stt/discovery_test.go` | 12 个用例 |

**候选筛选必须「modality + 名字」双路**（§1 表格第 4 行：网关把
`mimo-v2.5-asr` 标成了 `text`）。只按 modality 筛会漏掉唯一的国产 ASR 候选。

**探测分类**（每种都对应一种真实失败模式）：

| 状态 | 触发条件 | 设置页文案 |
|---|---|---|
| `ok` | 200 + 非空文本 + 未命中幻觉 | 可用 |
| `no_provider` | 503 且 `no_candidate` / `No available provider` | 网关无上游 provider |
| `endpoint_missing` | 404 / 405 | 无转写端点 |
| `audio_ignored` | 200 但命中幻觉文本，或 `total_characters==0` | 上游丢弃音频 |
| `failed` | 其他 | 探测失败（带 detail） |

探测预算 `maxProbeCandidates = 6`：网关限流 12 次/分钟（§1 表格最后一行），
无上限会把设置页点成 429。结论按「地址+key 指纹」缓存 10 分钟。

**幻觉守卫**双信号，缺一不可：
1. 文本正则（中英各若干种说法，含实测的「您似乎没有附上录音文件」与
   `I don't see any audio file`）
2. 结构信号：网关 `usage.total_characters == 0` 且文本为空

误拦的代价是给一条明确报错；漏拦的代价是把幻觉写进用户会议记录。所以
正则宁可放宽——但要避开「附件里没有提到预算上限」这类正常转写里的「没有」。

### §3.2 目标解析语义

```
auto     → 先网关（探测通过的模型）→ 再外部 → 都没有则报错，且两条原因都保留
gateway  → 只用网关；手工指定的模型探测不通过就报错点名该模型，不静默换模型
external → 只用外部服务
```

「手工指定但不可用 → 静默换成别的模型」是最坏的一种行为：用户以为在用 A，
其实在用 B，且精度差异无人知晓。所以必须报错。

外部地址走 `validateGatewayURL`（SSRF 防护）：拒绝 loopback / 私网 /
云元数据端点 / 非 http(s)。

### §3.3 前端：设置页 `/settings/stt`

- `frontend/src/api/stt-settings.ts` — API 客户端 + 探测状态中文映射
- `frontend/src/features/settings/SettingsSTT.vue` — 设置页
- `frontend/src/app/router-mobile.ts` — 路由注册
- `frontend/src/features/settings/SettingsView.vue` — 设置页入口
- `frontend/src/features/settings/__tests__/settings-stt.test.mjs` — 12 个用例

**「试转验证」是刻意设计的**：自动发现只能验证「端点通不通 / 上游有没有 /
会不会丢音频」，**验证不了识别准不准**——那必须有真实语音，而真实语音只在
设备上。所以设置页提供「录 3 秒试转」（真机 MediaRecorder → JSON base64 →
`POST /api/stt/probe`），成功与失败都可见。

---

## §4 顺手修掉的两个真问题（都不是本任务引入的）

### §4.1 错误码被前缀吃掉，前端看不到可行动原因

`frontend/src/api/error-message.ts` 的 `extractErrorCode()` 取**第一个冒号前**
的 `[a-z0-9_]+` 当错误码。而原实现回传的是
`"transcription failed: stt_unavailable: 网关暂无可用的…"`，冒号前是带空格的
短语 → 匹配失败 → 前端退到语义兜底 → 用户看到通用「服务端错误」。

改成：带错误码的原样回传（`stt_unavailable: <精简原因>`），未带码的才套
`transcription failed:` 前缀。同时把逐候选的探测细节从 message 挪到日志——
实测 6 个候选的 503 全文有几百字，塞进 toast 是噪音。

### §4.2 单测会真打生产网关

`newServer` 改成总是装一台 resolver 转写器后，`TestMeetingWorkspaceIsolation`
里的 `meeting/transcribe` 顺着默认网关配置**真的出网打了 llm.kxpms.cn**
（实测 2.3s，把 6 个候选的 503 全打了一遍，还吃到网关限流 429）。

修法两条：
1. `Server.sttHTTPClient` 可注入，测试注入拒绝出网的实现。
2. 假 ASR 走**真实解析路径**（保存一条指向本机 httptest 的 external 设置），
   而不是把 transcriber 换掉——换掉会让 handler 绕过设置解析，测不到该测的。

### §4.3 会议转写不再写占位文本

改造前 `s.transcriber == nil` 时 `m.Transcript` 被写成
`"（STT 未配置，请设置 POCKET_GROQ_API_KEY）"`，然后 `status = "transcribed"`。
等于把一句假转写存进会议记录还标记成功。现在统一返回 502 + 可行动原因 +
`status = "failed"`。

### §4.4 录音失败时真实原因被丢弃（前端，最严重的一处）

`native/recordingRuntime.ts` 的分片转写失败分支写死：

```ts
this.sttError.value = '转写失败，将在下一段重试'
console.warn('[meeting-recorder] segment failed:', e)   // 真实原因只进 console
```

停止链路的兜底转写则是 `this.error.value = e instanceof Error ? e.message : '转写失败'`
——把 `stt_unavailable: …` 连错误码一起怼给界面。

两处都让「网关没开通 ASR」和「网络断了」在用户眼里完全一样，而这两种要采取的
动作完全不同（前者去设置里换模型，后者重试）。§4.1 在后端辛苦整理出的可行动
原因，到界面这一层被丢干净了。

修法不是简单地把 `e.message` 显示出来——那会把
`dial tcp 1.2.3.4:443: i/o timeout` 这类技术串也甩给用户。`api/error-message.ts`
的 `toUserMessage()` 按设计优先用 i18n 通用文案、隐藏原文（注释原话：
「宁可降级为通用文案，绝不把原始错误能力甩给用户」），那条策略对绝大多数场景
正确，但**对 STT 是反例**：映射成 `errors.sttNotConfigured`
（「语音转写服务尚未配置」）等于把唯一可行动的信息也一起盖掉。

所以开了一个**窄口径**特例 `api/stt-error.ts`：只放行带 `stt_unavailable`
错误码的整理文案（剥掉错误码前缀、截断 160 字符），没有稳定错误码的一律走
通用兜底。窄口径的意义：技术串本来就没有错误码，所以「不泄技术噪音」和
「展示可行动原因」两个目标不冲突。

---

## §5 验证

### §5.1 单测

`internal/stt`：**20/20 通过**。
- `discovery_test.go` 12 个：候选筛选（modality + 名字双路）、`no_provider` 分类、
  丢音频识别、幻觉正则正反例、缓存与 key 指纹隔离、ToneWAV 可解析、预置分组、通道归一化
- `transcribe_test.go` 8 个：成功转写带出模型/通道/成本与 1 秒 WAV 时长核算、
  空转写判失败、幻觉文本判失败、chat-audio 丢音频判失败、`<think>` 剥离、
  解析错误原样冒泡、缺 key/缺模型/空 target 在出网前就拒、空音频拒绝、
  旧静态构造契约保持

`internal/server`：**整包全绿**（含本次新增 11 个 STT 用例 + 既有会议隔离等）。

> 过程中的一个插曲值得记：另一个并行会话在 2026-10-01 把
> `internal/opencode/config_writer.go` 里的内置默认网关 key
> `DefaultLLMGatewayAPIKey` **删掉了**（理由成立：那是一把能计费的真实密钥，
> 写进了源码就等于所有部署的默认密钥）。连带影响是「网关默认有 key」这个
> 前提消失，本任务 3 个 STT 用例因此失败。修法是让测试通过
> `POCKET_LLM_GATEWAY_API_KEY` 显式注入 key，而不是假设内置默认值——
> **不要把已移除的常量加回来**。

### §5.1b 全后端回归

`go test ./... -count=1`：**50 个包 ok，2 个包失败**。两个失败包都与本任务无关，
已取到硬证据（`go list -deps` 确认它们**不依赖** `internal/stt`）：

| 失败包 | 具体原因 | 判定 |
|---|---|---|
| `internal/agent` | `exec: "D:\temp\pocket-agent-echo-...\agent_echo": executable file not found in %PATH%` | 环境：测试要拉起辅助二进制，本机没有 |
| `internal/email` | `key file mode: got 666, want 0600` | 环境：Windows 无 POSIX 权限位，0600 断言不可能成立 |

本任务改动只涉及 `internal/stt` 与 `internal/server`，两者均全绿。

### §5.2 前端

`vue-tsc --noEmit`：**exit 0**。

测试合计 **28/28 通过**（4 个文件）：
- `api/__tests__/stt-error.test.mjs`（本轮新增 10 个）：放行带错误码的整理原因、
  剥前缀、技术串走兜底、空异常、只有错误码无原因、超长截断、字符串/对象两种异常形态；
  另 2 个锁住录音两处 catch 确实改用了 `sttFailureText`（防止回退成写死文案）
- `features/settings/__tests__/settings-stt.test.mjs`（12 个）
- `features/notes/__tests__/note-recording-error-visibility.test.mjs`（既有）、
  `api/__tests__/raw-error-ui.test.mjs`（既有）—— 确认 §4.1/§4.4 的改动
  没有破坏原有的错误可见性约束

### §5.2b 仓库自带卡口 + 生产构建（部署验证）

`npm run gates` 的每一环都单独跑过，全绿：

| 卡口 | 结果 |
|---|---|
| `typecheck`（`vue-tsc --noEmit`） | exit 0 |
| `check:i18n` | ✅ 9 份语言文件 242 个 key 全齐平 |
| `check:vm-gaps` | ✅ 命中 0 = 阈值（通过） |
| `check:icons` | ✅ 扫描 609 文件，所有声明式图标都在字体子集内 |
| `test:native` | **44/44** |
| `assert-no-plaintext-backend`（`prebuild`） | ✓ 守卫通过（无明文后端地址） |
| `vite build` | ✓ built in 15.39s，exit 0 |

> 关于 i18n：本页文案**硬编码中文**，与既有的 `SettingsLLMGateway.vue` 同一路子，
> 因此不新增 key、i18n 卡口无需改动。若后续要支持英文界面，需要连同该页一起
> 抽 key（属另一件事，本轮未做）。

产物里新页面是独立 chunk：
`dist/assets/SettingsSTT-BX9hk_WB.js` 13.58 kB（gzip 5.43 kB）
+ `SettingsSTT-D66_chuk.css` 5.28 kB（gzip 1.33 kB）。
即新增设置页按路由懒加载，没有把主包拖大。

### §5.3 黑盒验证（真实后端 + 真实网关 + 真实中文语音）

`scripts/verify-stt.ps1`：**19/19 PASS，0 FAIL**（在并行会话移除内置网关默认 key
之后**又跑了一遍**，仍然 19/19）。

流程：构建 pocketd → 起进程（dev 旁路，18099）→ 登录取 token → 逐个打接口 →
停进程。语音样本是 Windows SAPI 合成的真实中文
（「今天下午三点开项目评审会，请准备进度报告和预算表。」）。

实测输出要点：

| 检查 | 结果 |
|---|---|
| `/api/stt/config` | 网关组 3 个（`gpt-audio-mini` / `mimo-v2.5-asr` / `gpt-audio`）、外部组 3 个（`gpt-4o-mini-transcribe` / `whisper-large-v3-turbo` / `gpt-4o-transcribe`）、外部组均带 $/小时、**响应无明文 key**、三通道均有中文说明 |
| `/api/stt/discover` | 2.3s 扫完，网关 549 个模型；6 个候选**全部 `no_provider`**，无一被误标为可用 |
| `/api/stt/probe`（真实中文语音） | `ok=false` + 可行动中文原因；**没有**返回幻觉文本 |
| `/api/stt/transcribe` | 502，错误码 `stt_unavailable` 在**首位**（前端能提取），原因 186 字符（可控） |
| `PUT /api/stt/config` | 外部 key 存下且不回显；`effectiveModel = gpt-4o-mini-transcribe` |
| 占位 key 试转 | `ok=false`，真实失败原因上浮（不是幻觉文本） |
| SSRF | loopback / 云元数据 / 非 http 三种地址全部被拒 |

### §5.4 黑盒验证揪出的 3 个真 bug（都已修）

这三个都是「只读代码/只写单测发现不了、必须真打上游才暴露」的问题：

1. **503 响应有两种形态，只认一种导致整片候选被误标。**
   `/chat/completions` 的 503 带 `code:no_candidate`，但 `/audio/transcriptions`
   的 503 是 `{"error":{"alternatives":{...}}}`，**不含** `no_candidate` 字样。
   第一次跑出来 6 个候选全是 `failed` 而不是 `no_provider`，设置页上就看不出
   「网关没开通」这个事实。修法：`isNoProvider()` 同时认
   `no_candidate` / `No available provider` / `"alternatives"` / `"requested_model"`。
2. **失败原因里内嵌上游原文，撑爆界面。**
   逐候选的 `detail` 是上游 503 的前 200 字符，拼起来 795 字符。
   修法：用户可见原因只放短状态（`网关无上游 provider`），原始 detail 走日志。
3. **无 PG 部署下设置根本存不下来。**
   `pocketd` 没有 `POCKET_POSTGRES_DSN` 时会正常启动（remote-only 模式），
   此时 `s.userSettings == nil`，`PUT /api/stt/config` 直接
   400 `user settings store unavailable` —— 整个功能等于不可用。
   修法：加进程内兜底存储（`sttMemSettings`），功能可用，重启回默认值
   （不制造虚假持久化预期）。

### §5.5 没验到的部分（如实列出）

1. **Android 真机 Maestro 验证做不了**：本机没有 `adb`，也没有模拟器镜像
   （扫过 `C:\` 未找到 `adb.exe` / `emulator.exe`）。所以「真机上录音 →
   停止 → 转写 → 文字进笔记」这条链路**未在设备上验证**。
2. **设置页的真实渲染与交互未验证**。已起 vite dev server + 真实 pocketd，
   浏览器已能加载登录页并正确指向后端 `http://127.0.0.1:18099`，
   但浏览器工具不允许代填登录密码，用户选择跳过。
   因此「页面渲染、重新扫描按钮、推荐模型点击、录 3 秒试转」这些
   **只有源码级契约测试（12/12）、类型检查与生产构建背书，没有运行时截图证据**。
   补充说明：本仓库没有 vitest / @vue/test-utils / jsdom（`node --test` + 源码断言
   是既有约定），所以组件级挂载测试也不是现成手段——要真正补上这项，
   要么给一次浏览器登录，要么引入组件测试基建。
3. **外部 ASR 的真实转写质量未验证**：`api.openai.com` 在本机网络不可达
   （`dial tcp 104.244.43.208:443: i/o timeout`）。已验证的是链路正确
   （配置保存 → 目标解析 → 真实发出请求 → 失败原因如实上浮），
   **没有**验证过一次成功的外部转写。
4. 上述 3 条都需要同一件东西：**一把网络可达的外部 ASR key**（OpenAI / Groq /
   国产任一 OpenAI 兼容服务）。

---

## §6 遗留 / 需要用户或网关侧决定

1. **网关侧没有可用 ASR**（§1）。若要真正走网关通道，需要网关运营方给
   `gpt-audio` 或 `mimo-v2.5-asr` 开 provider。开通后设置页点「重新扫描」
   即可，无需改代码、无需发版。
2. **外部通道需要一把真实且网络可达的 key**。这是解锁 §5.5 里 3 项未验证
   内容的唯一前提。
3. **需要一台 Android 真机或模拟器**才能做真机 Maestro 端到端验证。
4. `internal/server` 单测已补跑并全绿（见 §5.1），此项无遗留。
5. 中文 CER 缺公开可信榜单（Open ASR Leaderboard 仓库 404）。若要按中文
   精度而非多语指数排序，需要自建评测集。
6. 阿里 Fun-Realtime-ASR（AA WER 0.0173，榜内最佳）**非 OpenAI 兼容**，
   要接需自写适配层；本轮未实现。

## §7 复现命令

```bash

# 单测（stt 包 + server 包）
cd backend && go test ./internal/stt/... ./internal/server/...

# 前端类型检查 + 契约测试（28 个用例）
cd frontend && node node_modules/vue-tsc/bin/vue-tsc.js --noEmit
cd frontend && node --test src/api/__tests__/stt-error.test.mjs src/features/settings/__tests__/settings-stt.test.mjs src/features/notes/__tests__/note-recording-error-visibility.test.mjs src/api/__tests__/raw-error-ui.test.mjs

# 生产构建（部署验证）
cd frontend && node scripts/assert-no-plaintext-backend.mjs
cd frontend && MOBILE_ALLOW_EMPTY_API_BASE=1 node node_modules/vite/bin/vite.js build

# 黑盒验证（会自己构建 pocketd、起进程、打真实网关、最后停进程）
powershell -ExecutionPolicy Bypass -File scripts/verify-stt.ps1

# 网关侧原始探测（不依赖本仓库改动）
node scripts/gw-audio-probe.mjs
```

---

## §9 真机联调本机后端：不要改用户 App 的「后端服务器」设置

2026-10-01 真机验证时踩出来的坑，逐条都有对照证据。**任何人想在真机上验本机
pocketd 的改动，都按这里来。**

### §9.1 为什么不能直接改 base

`frontend/src/config/api-base.ts` 的 `resolveRuntimeApiBase`：`VITE_API_BASE` 为空时
Capacitor 壳会回退生产入口 `https://pocket.itestu.cn`；而 localStorage 里的
`pocket_api_base` 覆盖**优先级高于构建期 base**，且 localStorage 按 origin + 包名隔离。
于是调试包即使带着 `VITE_API_BASE=http://127.0.0.1:18099` 装上去，只要正式包里存过
覆盖，它照样打生产——表现是 `/api/stt/config` 404（生产没有 STT 路由），前端显示
「读取语音转写设置失败：找不到对应的内容」。

改设置页里的后端地址确实能切过去，但 `persistApiBase` 会连带清掉已登录 session，
等于把用户手机上的登录态洗掉。**所以不这么做。**

### §9.2 正解：挂一个并存调试包

`frontend/android/app/build.gradle` 的 debug buildType 支持 `-PsttDevApp`：

```
gradlew assembleDebug -PsttDevApp     # → com.kaixuan.opencode.pocket.sttdev
```

与正式包并存、数据互不干扰，验证完 `adb uninstall com.kaixuan.opencode.pocket.sttdev`
即可。不传该属性时 applicationId 与行为和以前完全一致。

### §9.3 三个必须同时满足的条件（缺一个就是「连不上」）

1. `adb reverse tcp:<port> tcp:<port>`（真机没有 localhost，用 emulator 的
   `10.0.2.2` 不适用于真机）。
2. 本机 pocketd **必须**带 `POCKET_DEV_AUTH=true`。否则 `buildOriginChecker`
   不放行 `localhost` / `127.0.0.1`，`corsMiddleware` 不发
   `Access-Control-Allow-Origin`，WebView 侧每个请求都是 CORS 失败——而
   `scripts/verify-stt.ps1` 起的实例默认就带这个变量。
3. 明文后端要用 `CAP_ANDROID_SCHEME=http` 构建（`frontend/capacitor.config.ts`
   里已写好的逃生舱）。默认 `https` 壳下 `http://` 的 XHR 被 mixed content 拦死，
   表现为 `fetch` **一直 pending、不报错**。

### §9.4 改完端口/reverse 一定要重启 App

WebView 会缓存到旧实例的连接池。实测：换了 reverse 之后 fetch 仍报旧实例的
CORS 错误，`force-stop` + 重启即恢复。

### §9.5 `screencap` 返回 0 字节时改用 CDP

设备上 `adb exec-out screencap -p` / `screencap -p /sdcard/x.png` 全部返回 0 字节时
（SurfaceFlinger 抖动、或另一会话正在占设备），用 WebView 调试协议读页面文本：

```
adb forward tcp:9303 localabstract:webview_devtools_remote_<pid>
# Node 22 自带 WebSocket，直接发 CDP：Runtime.evaluate / Input.dispatchMouseEvent
```

注意两点：① `Runtime.evaluate` 里 `el.click()` **不算用户手势**，
`getUserMedia` 一类 API 会拒绝，要用 `Input.dispatchMouseEvent`；
② 页面被切到后台时渲染进程被冻结，CDP 会无响应——先把 App 拉回前台。
---

## §10 最终验证矩阵（2026-10-01 13:20）

分支 `feat/2026-10-01-stt-service`。每一行的「怎么验的」都写在右列，
**绿灯本身不算证据**——负控对照一栏才是。

| 层面 | 结果 | 怎么验的 |
|---|---|---|
| `go test ./internal/stt/... ./internal/server/...` | 全绿 | 每次改完强制 `-count=1`，不复用缓存 |
| `go vet` / `vue-tsc --noEmit` | 无输出 | — |
| 前端断言 | 103/103 | `node --test`（无 vitest/jsdom，直接断言源码与渲染链） |
| 黑盒·转写主链路 | **19/0** | `scripts/verify-stt.ps1`，真打 llm.kxpms.cn |
| 黑盒·长录音/即时增量 | **22/0** | `scripts/verify-stt-stream.ps1`，真进程真端口 + 本地假 ASR |
| 真机·设置页 | ✅ | 读到本地后端真实配置；「重新扫描网关」出 602 模型 / 0 可用 / 逐个中文原因 |
| 真机·会议录音 | ✅ | 录音中真实失败原因上屏；停止后转写区**无占位文本** |
| 真机·外部成功路径 | 部分 | 后端直连 + 假上游：6.68 秒真实中文语音完整到达上游并回传文本；**设备麦克风 → 外部真实服务**这一段未验（无 key） |
| 负控对照 | 6 组 | JSON 泄漏、line-clamp、isNoProvider 新形状、language 字段（单次 + 逐段）、转写响应字段、设置页「当前生效」——逐条实测改回去会转红 |

### §10.1 仍然没验的一件事

**真实 ASR 服务的识别质量**。原因不是代码问题：`api.openai.com` 在本机
i/o timeout，网关侧 2026-10-01 实测一个可用 ASR 上游都没有。
假 ASR 只能证明「音频真的到了上游、请求形状正确、返回被正确解析」，
**不能**证明「中文识别得准」。设置页的「录 3 秒试转」就是为这件事准备的，
需要一把能用的外部 key。

### §10.2 两个反复咬人的环境事实

1. **本机 pocketd 必须带 `POCKET_DEV_AUTH=true`**，否则 `corsMiddleware`
   不发 `Access-Control-Allow-Origin`，WebView 每个请求都 CORS 失败；
   而不带 PG 时设置落在**进程内**的 `sttFallbackSettings`——重启即丢，
   验证脚本每次都要重新 PUT。
2. 另一会话会周期性抢占真机：我的 App 一被切到后台，WebView 渲染进程冻结、
   CDP 无响应，`screencap` 还会间歇性返回 0 字节。要抢时间就得把整条操作
   压进一次连续执行。