# 真机录音验收：mimo-v2.5-asr 链路打通，但有**两个**独立阻塞点

> 2026-10-05 凌晨。Redmi 2411DRN47C（Android 14 / WebView 126，dpr=2，物理 720x1640）
> + `com.kaixuan.opencode.pocket.sttdev`。全部结论附实测判据，不含推测。

## 〇、一句话结论

**ASR 本身是好的**：真机麦克风录到的真实语音，经 16k WAV 上传后被
`mimo-v2.5-asr` 转写为 **100% 字符重合**的原文。

**但 App 当前的录音路径 100% 失败**，界面只显示「转写失败，将在下一段重试」。
原因是两个互相独立的阻塞点，**都必须在录音侧修，光修一个不够**。

## 一、已打通的部分（不要重复验证）

| 环节 | 判据 | 结果 |
|---|---|---|
| 网关目录 | `GET /v1/models` 605 个模型 | 含 `mimo-v2.5-asr` / `mimo-v2.5-tts` |
| 网关 ASR 直连 | 8.17s 中文语音 POST `/v1/audio/transcriptions` | 200 / 1.33s / 文本与原文逐字一致 |
| 网关 TTS | `/v1/audio/speech` | 200，4.64s 语音 |
| 后端转写 | `POST :8124/api/stt/transcribe` 上传 WAV | 200，`confidence 0.95` |
| **真机端到端** | 真机麦克风录 15s → 16k WAV → 上传 | 200，**字符重合度 100%** |

最后一条的证据（ground truth = 「今天下午三点，会议室开产品评审会，请提前十分钟到场。」）：

```
识别结果：会议室开产品评审会，请提前十分钟到场。今天下午三点，会议室开产品评审会，
         请提前十分钟到场。今天下午三点，会议室开产品评审会，请提前十分钟到场。
         今天下午三点，会议室开。
```

重复 3 遍是因为测试音频 `loop=true` 循环播放，15s ÷ 4.64s ≈ 3.2 轮 —— **不是**转写重复的 bug。

> ⚠️ 后端**必须带 key 启动**：`POCKET_LLM_GATEWAY_API_KEY` 未设时
> `llm_gateway_handler.go:70` 返回空串（源码里刻意没有内置默认 key），
> 录音转写必然报「外部语音转写服务未配置 API Key」。
> 本轮用的是 8124 端口的实例，**没有动既有的 8123**（怕打断别的会话）。

## 二、阻塞点 ①：webm 被网关 400 拒收（**这是真缺陷**）

真机录音的实际容器与网关能吃的格式对不上。

**证据 1 — 网关的原话**（`/api/stt/transcribe-full` 响应体）：

```json
{"ok":false,"error":"stt mimo-v2.5-asr 400: {\"error\":{\"code\":\"invalid_audio_request\",
 \"message\":\"audio format \\\"webm\\\" is not supported by the chat-audio bridge
 (supported: mp3, wav)\"...}}"}
```

**证据 2 — 真机 MediaRecorder 实际产出**（页内实测）：

```json
{"selected":"audio/webm;codecs=opus","actual":"audio/webm;codecs=opus","bytes":243495}
```

`MediaRecorder.isTypeSupported('audio/wav')` = **false**
⇒ 真机**根本录不出 wav**，而 `recorderMime.ts:10` 的候选表把 `audio/webm;codecs=opus`
排在**第一位**。所以真机 100% 产出 webm，100% 被网关拒。

**证据 3 — 对照实验**（同一段音频，只换容器）：

| 容器 | 网关响应 |
|---|---|
| `probe.webm` | **400** `audio format "webm" is not supported` |
| `probe.wav`（同源转出） | **200** `{"duration":4,"text":"<chinese>"}` |

### 修法落点

- **后端**（推荐，一处收口）：`backend/internal/stt/` 在上传上游前把 webm/m4a 解码重编码为
  16k 单声道 WAV。`full.go:23` 现在写的是「webm/opus 不做瞎猜式重封装」——
  那条注释针对的是**切段**（WebM cluster 边界不能按字节偏移硬切），**不等于**不能整体转码。
  整体转码不涉及切段，`ffmpeg`/PyAV 一条命令的事。
- **前端**：`recorderMime.ts` 候选顺序不动（改了也只会换成 m4a，网关同样不收，
  白折腾）；真要在前端解，得引入 AudioContext 重采样再打包 WAV。
  ⚠️ 别用「改候选顺序」当修法——`audio/mp4` 同样在网关的拒绝名单外。

## 三、阻塞点 ②：MIUI 回声消除把自播放声音全消（**环境相关，但会伪装成产品缺陷**）

手机自己外放声音时，麦克风录到的是**静音**。

**对照实验**（页内 `AnalyserNode` 逐帧取峰值）：

| 条件 | peak | 说明 |
|---|---|---|
| 纯环境静音（无播放） | **0** | 麦克风本身正常 |
| 扬声器播放 · AEC/NS/AGC **开** | **0.0001**（99 帧无一超 0.01） | **全静** |
| 扬声器播放 · AEC/NS/AGC **关** | **0.042** / rms 0.0044 | 录到了 |

⇒ 阻塞点 ② 的存在，**依赖「用手机自己外放当音源」这个测法**。
真人对着麦克风说话不受影响（AEC 消不掉真实人声）。
**但它会让「播放音乐/视频然后录音」这类验收 100% 失败**，
而失败形态是「转写失败」，看起来和阻塞点 ① 一模一样。

`recordingRuntime.ts:698` 用的是：

```ts
await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, sampleRate: 16000 } })
```

未显式指定 ⇒ AEC/NS/AGC 取浏览器默认（全开）。
**建议**：验收脚本里显式关掉，别改产品代码——
面向真人语音输入的场景，开 AEC 是对的（去掉回声更有利于识别）。

## 四、「后端全 200」不等于转写成功（本轮踩到，值得记）

后端日志里 `/api/stt/transcribe-incremental` **连续 36 次全是 200**，
而界面显示「转写失败」。

原因在 `server_stt_stream.go`：

```go
// 目标解析失败**不**返回错误 HTTP
writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
// 上游转写失败同理
writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
```

**业务错误被包在 200 里**（这是刻意设计：不想让即时文本区丢掉已有内容）。
⇒ 任何用「HTTP 状态码」判断 STT 成败的脚本/门禁都会**恒绿**。
判据必须落在**响应体的 `text` / `error` 字段**上。

本轮就是靠 hook `fetch` 抓响应体才拿到真因；只看后端日志会误判成「已修好」。

## 五、CDP 真机自动化的两个坑（本轮实测）

1. **`screen.width / innerWidth` 都是 CSS 像素**，比值恒为 1。
   我用它算「物理坐标」scale，结果 scale=1 等于没换算，
   `DOM(312,704)` 被当成物理坐标发出去 → 落到屏幕**左半边中部**，
   点到了首页内容区，录音根本没开始（而脚本当时打印「已点击」，看起来是成功的）。
   ✅ 正解：一律用 `Input.dispatchMouseEvent`，`x/y` 给 CSS 像素，由 WebView 自己乘 dpr。

2. **App 切到后台后 CDP socket 仍在但 `/json/list` 返回空**。
   现象是 `adb forward` 成功、socket 存在、`/json/list` 空 ⇒ ECONNREFUSED。
   把 App 重新 `am start` 拉回前台即恢复（实测有效）。

## 六、环境复现步骤

```bash
# 1. 带 key 起后端（不动既有实例）
POCKET_DEV_AUTH=true POCKET_HTTP_PORT=8124 \
POCKET_JWT_SECRET=dev-device-secret-2026-openpocket-device-matrix \
POCKET_DB_PATH=/tmp/opstt/stt8124.sqlite \
POCKET_LLM_GATEWAY_URL=https://llm.kxpms.cn/v1 \
POCKET_LLM_GATEWAY_API_KEY=<key> \
  ./backend/pocketd

# 2. 真机通道
adb -s 4c308e2e reverse tcp:18099 tcp:8124     # App 的 pocket_api_base 就是 18099
adb -s 4c308e2e reverse tcp:18125 tcp:8125     # 播放测试语音用
PID=$(adb -s 4c308e2e shell pidof com.kaixuan.opencode.pocket.sttdev)
adb -s 4c308e2e forward tcp:0 localabstract:webview_devtools_remote_$PID

# 3. STT 配到 mimo
curl -X PUT :8124/api/stt/config -H "Authorization: Bearer $TOK" \
  -d '{"channel":"gateway","gatewayModel":"mimo-v2.5-asr","language":"zh"}'
```

⚠️ 真机 `STREAM_MUSIC` 曾是 **Muted**，播放无声会让整轮验收录到空。跑之前先查：

```bash
adb shell "dumpsys audio | grep -A2 'STREAM_MUSIC:'"   # 要 Muted: false
```

## 八、修复已实施（阻塞点 ①，2026-10-05 第三轮）

属主拍板**走前端转码**，并要求保住即时转写。已实施并真机验证。

### 改了什么

| 文件 | 改动 |
|---|---|
| `frontend/src/native/recording-audio-transcode.ts` | **新增**。`RollingWebmDecoder` + `pcmToWavBytes` / `sliceToWavBytes` / `arrayBufferToBase64` |
| `frontend/src/native/recordingRuntime.ts` | 笔记录音接入转码：分片进 decoder，即时转写发 WAV 新增窗口，`stop()` 兜底走 `takeFull()`；**并修掉一个被暴露的既有缺陷**（见下） |
| `frontend/src/api/stt-settings.ts` | 新增 `transcribeIncrementalBase64`（已有 base64 就不该再过一次 blobToBase64） |
| `frontend/src/native/__tests__/recording-audio-transcode.test.mjs` | **新增** 23 条：纯函数不变量（**直接 import 生产实现**）+ 源码级契约 |
| `docs/handoff/evidence/recording-acceptance-20261005-0352.json` | 真机验收证据：16 次 STT 响应的完整 body + UI 时间线 + 与 ground truth 的比对 |
| `docs/handoff/evidence/recording-silence-proof-20261005-0352.json` | 「App 录到纯数字静音」的量化证据（1656000 样本全 0） |

### 为什么不是「逐片转码」——这是本轮最要紧的技术前提

MediaRecorder 每 3 秒派发一个 webm blob，实测**除第一片外全部无法解码**：

```
片0  22330 B  decodable=true     ← 含 EBML 头 + 初始化段
片1    700 B  decodable=false    UnsupportedError
片2    700 B  decodable=false
片3    111 B  decodable=false
全部拼接 23841 B  decodable=true (9.48s)
```

WebM 是流式容器，初始化段只在开头出现一次。
⇒ 做法是**滚动累计解码 + 时间窗切片**：保留全部分片（本来就是 `this.chunks`
要留的东西，不额外占内存），每来一片就把累计容器重解一次，按
`[已发送秒数, 当前总秒数)` 切出**新增**部分打成 16k WAV。

**只发新增窗口**是关键：服务端会话按到达顺序累积文本，重发旧音频会得到
「今天今天下午三点」这类重复。游标由 `RollingWebmDecoder.sentUpToSec` 维护。

复杂度 O(n²) 解码，实测 3~9 秒音频单次解码 < 30ms，每 3 秒触发一次，可接受。

### 顺带修掉的既有缺陷：界面报告过期状态

第一次装机验收时暴露出来的 —— 转码修好后**立刻**显形：

```
[200] ok=true  text="嗯。这里有人吗？"
[200] ok=true  text="嗯。这里有人吗？会吧？有人吗？没听到这个声音啊。"
... 文本在正常增长 ...
最终 UI：「转写失败，将在下一段重试」
```

静音片段会让上游回 `upstream returned empty transcript` → `error` 被置上；
**后续有语音的分片成功了，error 却从不清除**。界面全程报告「上一次失败」
而不是当前状态 —— 它在说谎。

修法：成功分支里 `this.error.value = ''`。
⚠️ 这条以前**不可见**：转码之前每片都因 webm 被 400 拒收，压根不存在
「先失败后成功」的序列，错误一设到底反而看不出问题。
⇒ **修好上游通路会把下游的潜伏缺陷照出来**，这是本轮的第二条教训。

### 真机验收（2026-10-05 03:28 轮：PASS）

同一台 Redmi、同一段测试语音、**走 App 自己的录音入口**（点 FAB，不绕过 UI）：

```
17 次 STT 请求，全部 200，全部 ok=true，错误 0 条
字符重合度：100.0%
```

累计文本逐片增长（节选）：

```
[1] 今天下午三点，会议室开。
[2] …产品评审会，请提前十分钟到场。
[3] …今天下午三点，会议室开产品评审会。
...
```

- 界面**不再**显示「转写失败」✔
- 文本重复是测试音频 `loop=true` 连播 4 遍所致（15s ÷ 4.64s），**不是**去重失效 ✔

### 真机验收（2026-10-05 03:52 轮：PARTIAL —— 暴露了一个新的真问题）

同一台设备重跑（这次把证据落盘了），结果**不同**：

```
16 片增量全部 upstream returned empty transcript
最终文本：「嗯。」   字符重合度 0%
```

⇒ 转码是好的（没有 webm 400），但**这次录到的是静音**。
取 App 自己录到的音频验到底（`evidence/recording-silence-proof-*.json`）：

```
App 录 35 个分片 / 103.5 秒 → 拼接解码 → 1656000 个样本
nonZeroSamples: 0    peak: 0    min: 0
ASR: {"duration":104,"text":""}  HTTP 200
```

**纯数字静音，一个非零样本都没有。**
这不是 AEC —— AEC 只削回声、会留底噪（00:45 那轮实测 AEC 关时 rms 0.00386
仍有信号）。全零说明**麦克风数据根本没进 WebView**。

同一时刻、同一台设备，原生 `MediaRecorder`（不经 App 代码）录到 `peak=0.10`；
且**没有 App 录音时**原生流也只有 `peak=0.00007`。
⇒ 失败点在**设备当前的拾音路径**（扬声器→麦克风），不是 App 录音代码。
（音量键无效、`dumpsys audio_flinger` 报 `No active record clients`、
STREAM_MUSIC speaker 音量 15 次按键不动——设备侧状态本轮不稳定。）

### 由此又修掉一个真缺陷：错误清理的判据选错了

03:52 轮界面**全程**显示「转写失败」，而 text 同时在增长。查证据文件：

```
16 片里 8 片**同时**带 text 和 error
例：{ok:true, text:"嗯。", error:"stt ... empty transcript"}
```

读后端 `internal/stt/incremental.go` 才明白：某片失败时它返回
`{text: <已累积文本>, error: <本片错误>}` —— **两者同时存在是常态**。

⇒ 我上一轮把清理写在 `if (res.error) return` 之后，**永远走不到**（那些片确实带 error）。
正确判据是「**本片有没有出字**」。已改，契约断言也随之重写
（并保留两个错法的记录：① 放在 error 分支后 ② 按「无 error」判 —— 都不成立）。

### 关于判据本身的再一条教训

本轮至少四次「结论看着对、实际是错的」，来源都不是不谨慎，而是**推断代替测量**：

| 我以为 | 实测 |
|---|---|
| 是 AEC 消掉了自播放 | 全零样本 = 根本没进数据，不是 AEC |
| App 录音代码有问题 | 同一时刻原生流正常，App 的也是全零 ⇒ 设备侧 |
| track 被 `announceSilenced` 卡在 disabled | 3 秒后已恢复 `enabled:true`，但电平仍 0 |
| 清 error 的判据是「本片无 error」 | 8/16 片同时有 text 和 error |

⇒ **每个「原因」都应该有一次把它单独证伪的测量**，而不是找到一个说得通的解释就往下走。

⚠️ 验收过程中一次假红（值得记）：我为了绕开阻塞点 ②，给
`navigator.mediaDevices.getUserMedia` 打了 AEC 关闭补丁，
结果把约束对象改坏 → 「麦克风权限被拒绝」，界面显示未录音。
**那是我的注入写错了，不是产品缺陷** —— 重装授权、去掉补丁后一次通过。
⇒ 判「产品坏了」之前先分清：是**被测对象**的问题还是**验证手段**的问题。

### 判据经验（本轮踩到并当场修掉一条恒真判据）

新写的契约断言「解码器必须滚动累计而非逐片解码」初版用的是
**负向文本匹配**（`doesNotMatch` 逐片取片）。实测负对照：
把两处 `new Blob(this.parts, ...)` 都改成 `this.parts[length-1]` 后，
**判据依然全绿** —— 它是恒真的。

改成计数式硬约束（`takeNewWindow`/`takeFull`/`fullBlob` 各必须用一次
全量 parts 拼接，全文恰好 3 处）后，负对照才正确变红。
⇒ 负向文本匹配看起来能防回退，实际写法一变就漏；**能数的地方就数**。

5 条负对照最终全部验证会红：逐片解码 / 窗口从 0 开始 / 发原始 webm /
cleanupMedia 里 dispose decoder / 删掉清 error 那行。

## 九、返工记录（评审指出 3 处，全部已改）

### 9.1 【高】我把一条门禁弄红了

新增 `sttSettingsApi.transcribeIncrementalBase64` 后，
`npm run test:all` 转红（41 pass / 1 fail）：

```
not ok 2 - 长任务调用点必须逐个在册（新增而不登记就转红）
  源码扫到：api/stt-settings.ts::/api/stt/transcribe-incremental
            api/stt-settings.ts::/api/stt/transcribe-incremental   ← 两次
  登记表  ：…::/api/stt/transcribe-incremental                       ← 一条
```

**正确修法不是「再登记一行」**（那会让同一路径两行、且掩盖了重复调用点），
而是**合并成一个方法**：`transcribeIncremental` 接受 `audioBlob` 或
`audioBase64` 二选一。合并后调用点回到一处，门禁自然恢复。
门禁恢复后 `test:all` = **2110 pass / 0 fail**。

### 9.2 【中】7 条「纯函数」单测根本没测生产代码

初版把 `pcmToWavBytes` / `sliceToWavBytes` / `arrayBufferToBase64`
**在测试文件里重抄了一遍**。后果：改真实实现（比如 16k→8k）这 7 条全绿。

当时给自己找的理由是「node 的 type-strip 不支持参数属性」——
**那是自找的，且只有一行**（把 `constructor(private readonly x)` 改成
普通构造器赋值）。本仓本来就有 8 处 mjs 测试直接 `import ... from '../x.ts'`
的先例（如 `src/native/__tests__/outboxDrain.test.mjs`）。

已改为直接 import 生产实现，并加了一条护栏断言：
**测试文件里不得出现这三个函数的本地定义**（防止将来又退回复制粘贴）。

**变异实验（验证器要求，本轮补做）**：

| 变异 | 结果 |
|---|---|
| `TARGET_SAMPLE_RATE` 16000 → 8000 | **7 条变红** ✔ |
| WAV 头声道 1 → 2 | **1 条变红** ✔ |
| 窗口起点从游标改回 0（重发累计） | **2 条变红** ✔ |
| base64 分块 → 一次性展开 | **2 条变红** ✔ |

采样率那条红 7 条，是「测试真的在跑生产代码」最直接的证据
——重抄副本的版本在这里必然全绿。

⚠️ 第 4 个变异第一次**没红**：sed 模式没匹配上（源文件多了一个
`as unknown as number[]` 类型断言），误以为判据恒真。改用 python 精确
替换后立刻变红。⇒ **「变异没导致变红」先确认变异真的生效了**，
再怀疑判据。

### 9.3 【低】`durationSec` getter 语义与注释不符，且是死代码

```ts
/** 当前累计音频的总时长（秒）。 */   ← 注释这么说
get durationSec(): number { return this.sentUpToSec }   ← 返回的是已发送游标
```

全仓无调用方。已删（连同测试里的引用）。

### 9.4 关于判据本身的教训（复用价值最高的一条）

本轮我一共修了**三条恒真判据**，形态完全不同：

1. `doesNotMatch(/逐片取片/)` —— 变异后仍全绿。改成**计数式硬约束**
   （「恰好 3 处全量拼接」）才咬住。
2. `readFileSync(...).slice(0, 2000)` 里找 import —— 注释太长，import 在
   偏移 >2000 处，**永远找不到**、恒红。改成全文匹配。
3. 剥注释前就匹配「不得有参数属性」—— 注释里**写着那个反例原文**，恒红。
   改成先剥注释。

**共同形态：判据写的是「文本长什么样」，而不是「行为是什么」。**
文本一改（加注释、换写法、调格式）它就失效，且失效方向还不同 ——
有的恒绿（假安全感），有的恒红（假警报）。
⇒ 能用**数值/行为**判的就别用文本判；必须读文本时，
**先剥注释**、**别截断**、**别只写负向匹配**。

## 十、待决

- 阻塞点 ② 是否要动产品代码：**建议不动**。真人说话场景 AEC 是对的
  （去回声更有利于识别），只在「手机自播放验收」这个测法下才致命。
  验收脚本显式关 AEC 即可。
