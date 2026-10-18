# 待拍板清单（2026-10-11 当场复核版）

> 这份文件的唯一目的：**让「还差什么才能收口」变成一页可决策的清单。**
> 主设计文档 `2026-10-06-recording-quality-fixes.md` 已 **7 万余行**，决策散落在各 § 里。
> 下面每条给的是**小节标题**，可以搜索复核 —— ⛔ **不要按行号找**：
> §564 已登记 4 处「拿行数当指针」的引用，它们随着文档增长**静默失效**，
> 指向的早已不是当年那段话。
>
> ⚠ **所有读数只在 2026-10-11 成立。** 本仓的「现状类断言」自带保鲜期
> （见主文档 §396 / §404）——动过相关文件的人必须重核这一行。

---

## ⭐ 2026-10-17 收口摘要（置顶；本段是**今天**的状态，下面的历史段落从第 51 行起）

**今天从「等属主输入」推进到「有素材、有读数、有一处已落地修复」。⚠ 章节编号已整体后移**：并发会话先一步落地并占用了 `§787`（它的 `§763.5` 两条待拍板），我这批八节因此重编号为 **§795–§801**，接在它后面（映射 787→795、788→796、790→797、791→798、792→799、793→800、794→801）。**本简报里凡指向 `§795–§801` 的都是重编号后的新号**。

### ✅ 已落地并验过的

| 项 | 状态 |
|---|---|
| `recordingRuntime.ts:701` 收尾同步补 best-effort 护栏 | 抽 `runBestEffort` + 7 用例行为测试；变异①撤护栏 **2 红**、变异②改回裸 `.then(` **1 红**；`test:native` 54/54、`typecheck` EXIT=0。**在 `origin/main` 之上重跑仍绿**（远端 21 个 commit 未碰这三个文件） |
| §781 两条红 | ⛔ **已由并发会话在主文档 §787 处置完毕**（价表判据改为按 `Group` 分组 + 「不得击穿基线最低价 $0.012/h」不变量；`capabilities-stub-gate` 登记表移除 Sherpa）。**我在 `origin/main` 上独立复跑** `stt-price-catalog-load-bearing.test.mjs` + `capabilities-stub-gate.test.mjs` = **14 PASS / 0 FAIL**（旧读数 13 = 11P/2F；分母 13→14 是并发会话新增用例所致，非口径变动）。✅ **后续已整套复跑**：`npm run gates` 在合并后的 main 上实跑 **45/45 通过**（50.9s，与 §787.2 自报一致）——见主文档 §802.1。本机 `gofmt` 在 PATH 上（homebrew），§787.1① 那个「恒停在第 31 项」的坑在此机不复发，被它挡住的 14 项确实执行了 |
| 本会话改动整体回归（在本地 `main` 上量） | 会议链 **771/771**；全量 `test:all` 339 文件 / 4101 事件，当时**唯一失败 = Sherpa 那条**（现已被远端处置） |
| §818 转码失败静默 | 根因（转码失败）与症状（`empty-transcript`）此前分离且**都不出口**：`reason` 是死输出，时长护栏判 ok。已加 `transcodeFailed`/`transcodeError` + 纯函数 `finalizeFailureNotice` + 调用点 warn；**降级行为不变**。变异 A/B/C/D 全中（A 撤留痕 5 红 / B warn 成死代码 1 红 / C 删分支 3 红 / D 断接线 1 红）。第一次修复判据**零牙**（把 warn 改成 `void 0 && console.warn` 仍 8/8 绿）⇒ 决策已从跑不到的 `finalizeRecording` 搬进纯函数 |
| §819 同一个静默 catch 的另外 4 条路 | `ensureGatewayCompatible`（`api/stt.ts:90`、`stt-settings.ts:273/333/384`）同样空 catch 吞掉转码失败。⚠ **这 4 条路上症状是可见的**（`http` 抛错，上层提示「语音识别失败」），不可见的只是**根因** ⇒ 排查的人会去查网关，而真问题在转码。已加可选通报器（默认 `console.warn`，一处覆盖 4 个调用点），通报带 `mimeType`/`bytes`/原始异常；**降级行为一行未改**。变异三个全中（含 §818 漏掉的「信号变死代码」那个）。动手前量过：调用点都是 `onstop` 整段转一次（不刷屏）、node 里 `window.AudioContext` 不存在 ⇒ catch 必然触发 ⇒ 可真断言 |
| §820 **录音落盘失败被吞掉（数据丢失级）** | `recordingRuntime.stop()` 里 `saveMeetingAudio` 失败被一句「ok」吞掉，而 `updateMeeting` **照样**把 `URL.createObjectURL(...)` 写进会议记录并置完成 ⇒ blob URL 只活在当前文档内存里 ⇒ **刷新/重启后音频彻底消失**，且库里谎报「完好」；`loadMeetingAudio` 随后返回 null ⇒ 整段重转永久不可用。成因不是理论上的（WebView IndexedDB 配额 / 60 分钟会议 webm）。已修：落盘失败时写 `audioPath: null` + 一条说清后果的 warn；**不阻断收尾、不改 status、返回值仍非空**（本次会话内它是真的）。变异三个全中（含「把 bug 改回去」6 红） |
| §823 护栏基线的**口径不匹配** | 护栏的墙钟起点 `startTime`（`recordingRuntime.ts:385`）设在采集开始（`vad-segmenter.ts:122` 的 `recorder.start()`）**之后** ⇒ `elapsedMs` 系统性偏短。实测这段偏移：**本页首次录音 ≈190ms**（首个 `AudioContext` 构造），之后每次 **≈0.2ms**。⇒ 「不会误报」的结论**不变**，但 §810.3 的 **7.5× 余量是热态数**；首次录音余量 **≈3.0×**（\|−169\| < 500 仍不报）。⚠ 移动端该开销通常更大、**不可外推**。（§823.5bis 补测 `startLiveCaption()`：同步开销首次 ≈18.5ms、之后 ≈0.1ms ⇒ 首次偏移合计 ≈**208ms**、余量 ≈**2.7×**；Android 的系统引擎那一步**未实测**。）**不改代码** —— 把 `startTime` 提前会改变写进 DB 的 `durationMs`（产品可见数字）且可能触发此前不触发的告警 ⇒ **属产品口径，不代拍** |
| §824 **切换设备丢掉切换前的音频** | `switchDevice` 里 `await this.vadSegmenter?.stop()` **返回值被直接丢弃**，而 `start()` 每次 `new VadSegmenter`（`fullChunks` 从空开始）、`saveMeetingAudio` 全仓只有 `stop()` 里一处调用 ⇒ **切换前录的音频全部丢失**。⭐ **只丢音频、不丢文字**（`onSegment` 逐段实时落库）⇒ 用户看到每段文字都在，但「整段高精度重转」只能覆盖切换之后，且产出的残缺全文**看起来正常**、不一定会被 `full-too-short` 拦下。本轮只加了「不再无声」的告警；**拼接方案待拍板**（WebM 段不可直接相连，必须解码重编码 ⇒ 改存储形态）：A 多键存储+顺序回放 / B 拼成单个 WAV 存 / C 不存音频只标不完整 |
| §825 §824 的下游放大：**错基的回执把后半场的话挂到前半场说话人名下** | 切设备后保存的音频只覆盖 `[K,T]`，而服务端回执的 `startSec/endSec` 是**那段音频的局部坐标**，分段用的是**会议绝对时间** ⇒ 两个坐标系错位 K ⇒ 按时间重叠分桶时，**后半场的话被整段挂到前半场的说话人名下，前半场的实时原文被顶掉**。实测（探针 `attrib2.mjs`）：错基在切点 10%→75% 全程存在，而 `shouldAdoptFullTranscript` 的长度守卫**全程放行**（它按**字数**比，错位按**时间**发生，两个量不同源）。⇒ **文字一条不少、条数一条不少、无空段** ⇒ 用户看不出错，只在回放对应时间点时才发现「不是这时候、也不是这个人说的」。已修：把「音频从会议哪一刻开始」作为**显式事实**从 `rt.stop()` 传到纯决策层，非 0 时整段重转**不参与分段**（`audio-time-base-offset`），沿用实时分段。**刻意不用「时长 vs 回执 max(endSec)」去猜**：两个数都来自墙钟，真机上本就可能差几秒（§811 的 4.48s ⛔ 未实测）⇒ 会在正常录音上误判、把 §40 归因整块关掉。行为判据 11 例 + 变异 7 个全中（含 A' 修复前形态、C 过度拦截、E 偏移不外传）。⚠ **代价**：切过设备的录音，精校改用实时分段，用户少拿一次「纠错过的全文」—— 错基正文比没有正文更糟。真正的解（拼接 + 精确偏移）**并入 §824 的 A/B/C 一起拍** |
| §826 ⛔ **Android 上会议录音从不存在完整音频** | `nativeMode=true`（原生端恒支持）⇒ `new VadSegmenter` 在 `if (!this.nativeMode)` 里根本不执行 ⇒ `stop()` 的 `fullBlob` 恒为 null ⇒ `saveMeetingAudio` 全仓唯一一处调用永不触发；原生侧 `MeetingRecordService` 用 `AudioRecord` 直推 PCM，**无任何容器文件**。⇒ `audioPath` 恒 null、`loadMeetingAudio` 恒 null ⇒ **整段高精度重转在生产主路径上永不发生**，精校只能吃实时分段文本。⚠ 这条事实使 §785/§787/§810 那一系列「整段重转提升质量」的分析在 Android 上**没有发生机会**；此前**零条门或注释**守着（`background-mic-contract.test.mjs` 18 条全在 start 结论/合规上）。本轮只落地「不再无声 + 不与落盘失败混为一谈」（`resolvePersistedAudio` 新增 `nativeMode`，并**在 `cleanupMedia()` 之前**捕获 —— 那函数会把 `nativeMode` 置回 false，晚一步读到的恒为 false ⇒ 诊断变死代码且不报错）。⚠ 顺带更正 §820：**前端根本没有任何音频播放界面**，`audioPath` 只被 API 序列化、笔记徽章、`loadMeetingAudio` 消费 ⇒ 「能播」不成立（已在 §820 挂更正指针）。变异 8 个全中，其中一个 **D 是无效变异**（`'' || null` 没改到行为 ⇒ 0 红），换成真会改行为的 D′ 才打红 |
| §827 **两个门禁在同一行上结构性互斥** | `gofmt` 会把 **doc 注释**里的 `''` 改写成 `”`（U+201D，反引号代码段里也一样，已在 `/tmp` 实测四种写法）；而 `check:smart-quotes` 要求写 ASCII `''`、禁 `”` ⇒ **「doc 注释里逐字记录 SQL 空串」在当前门禁组合下无解**，任一写法都让另一道门红，且 `npm run gates` 在第 31 项就停 ⇒ 整条 45 项跑不完。本轮**不动任何门的语义**：把两处注释的 SQL 片段改成 **tab 缩进代码块**（`''` 逐字保留、两个门同时放行），并明确拒绝把 `''` 写成 `""` —— SQL 里 `""` 是标识符不是空串，注释会从「排版走样」变成「内容撒谎」。⚠ durable 修法未代拍（约定不写 / 豁免其中一道门 / 让 gofmt 豁免引号差异，三选一待你拍） |
| §828 「转写成功但没出字」的那一段：**文字没建行、音频也没存、且一声不吭** | `ingest-speech.ts` 旧顺序是「先 `if (!text.trim()) return null`、**之后**才 `saveAudioPart`」⇒ 转写无字的那一段三样全没有（没建行/没落盘/没提示）。⚠ 它比「转写失败」更隐蔽：`sttApi.transcribe` **成功返回空文本并不抛错** ⇒ `processSegment` 的 catch（唯一会设 `sttError` 的地方）不触发。⚠ 且 §826 让它不可逆：Android 无整段音频，**分片是唯一拷贝**。已修：落盘提到早退**之前** + 落盘失败不再无声 + 文案决策搬进纯函数 `segment-drop-notice.ts`（调用点只剩一条 warn）。判据 10 例 + 变异 6 个全中；⚠ 判据第一版对 `void 0 && console.warn`（信号变死代码）**零牙**，改成钉「这一条语句的形状」后才打红；⚠ 我自己的否定断言也翻过一次车（「不得提到存储」与正确文案「而不是查存储」相撞 ⇒ **正确代码也红**）⇒ 否定断言必须挑不会与正确文本撞车的具体形态。⚠ 登记：`listAudioParts` 全仓**只有写入没有读取方** ⇒ 「崩溃后恢复转写」这个承诺目前没有实现 |
| §829 给 §458 补格：#21 双写在 **web/桌面 Chromium** 上成立 | §458.3 把 #21 的平台范围判为「本机判不了」（外部来源在 iOS 上互相冲突），但漏了 **web 本身**，而 web 是本机唯一能实测的平台。实测（Chromium 153，探针 `.artifacts/s824-probe/speechapi.mjs`）：`SpeechRecognition` 与 `webkitSpeechRecognition` **都在、且是同一个对象、可实例化** ⇒ `pickSpeechRecognition` 返回构造器 ⇒ `if (!Rec) return` 这道防线在 web 上不生效。⚠ 且 `hasForegroundHijack()` 的置位条件是 `localStorage[...] === '1'`（只有 MIUI 被 TTS 抢过前台才置位）⇒ **桌面 web 上恒为 false** ⇒ 与 §458.2 的签名相反：**不是第一场才重复，是每场都重复**。后果比「整句重复」更大：`planRefine` **全函数没有任何按 `origin` 的过滤** ⇒ 两份文本**一起进 LLM prompt**（烧配额 + 让模型去猜哪份对）。⛔ 未实测：真实会话里 Web Speech 会不会回 final（需真麦克风+联网）；Safari/Firefox/移动端均未测；**§458 的 iOS 争议维持原状，本节不翻案**。本轮**不改代码**（#21 在 §443 已定性为产品决定），只把三项从假设变成读数；另**已排除**：字幕失败不是无声的（`onerror` 只静默 `no-speech`/`aborted`） |
| §830 停止时的 10s 上限**到期不得无声**：最后几段不在精校里，徽章仍写「已精校」 | `stop()` 等在途分段有 10s 上限（防挂死，合理），但超时后**继续往下走且一声不吭** ⇒ 收尾合并用**当下**内存分段重渲染正文、`completed` 照写 ⇒ 而 `finalizeRecording` 紧接着**从 DB 读分段**做精校 ⇒ **晚到的几段不在精校输入里**；而 `resolveRefinedView` 在精校成功时展示 `refinedTranscript`（徽章「已精校」）⇒ 用户看到一份**缺了最后几句**、却标着「已精校」的正文。⚠ 那几段**会**自己补写 `transcript`，但精校视图**不展示**那条路 ⇒ 「数据在、默认视图里看不到」是最难发现的一种残缺。已落地：等待之后补一道「还在途吗」判定 + warn（文案搬进纯模块 `unsettled-segments-notice.ts`，措辞与真实 UI 的「看原文」对齐）；**根因未修**（延长上限 / 补跑精校属产品口径）。判据 7 例 + 变异 7 个全中。⚠ **变异 F 第一版 0 红**：我的「前提登记」断言对全文件匹配 10s 超时，而**笔记录音路径**里另有一个 ⇒ 会议那条被改掉它照样绿 ⇒ **前提登记也会指错位置**。⚠ **第 4 次撞上「注释字面量被扫描门当真代码命中」**：`speaker-relabel.test.ts` 用 `indexOf(<合并方法名>)` 断言顺序，我的注释提到了那个名字 ⇒ 正确代码被判红；处置是**改我的措辞不动门禁**，且第二版注释为解释这个坑又写了一遍名字（自我拆台），最终版连名字都不提 |
| §831 「**不得删除原文有的内容**」是写在提示词里却**零强制**的契约 | 服务端 `buildRefinePrompt` 第 3 条明写这句，且同一函数注释记着这条链的职责是**纠错而非改写**。但全仓**没有一处**比较精校前后的体量：服务端只防**响应被截断**（真实抓包 3867 个截断点），客户端只防**空值**（`hasRefined` 空值闸）⇒ 中间那一档「**响应完整、但模型把精校做成了摘要**」无人看守 ⇒ 用户看到少了大段内容的正文，徽章却写「已精校」（诚实性缺陷第三形态：存在但不完整）。已落地：新纯模块 `refine-retention-guard.ts`，接到**两个**产出 `refined_transcript` 的调用点（服务端 `normalizeRefine` + 浏览器兜底 `refineFromLlmContent`）；不通过 ⇒ **正文退回原文** + `fromFallback` 置真 ⇒ 复用既有降级路径，**不需要新增任何用户可见文案**。阈值**沿用 §783 的 `0.5`**（未代拍新值），另加**绝对下限 200 字**防小样本误判。判据 9 例 + 变异 8 个全中。⚠ 判据自己踩了三个坑，其中一个是**最隐蔽的假绿**：`indexOf` 找不到时返回 **-1**，而 `-1 < 任何正数` 恒为真 ⇒ 断言在「判定根本不在」时反而通过 ⇒ **凡是拿 indexOf 返回值做大小比较，必须先判 -1** |
| §832 随手记的**建草稿失败**完全不可见：录音白录 | ✅ 先钉死 §826 的范围：`startBackgroundMic()` 全仓**只有会议路径一处调用**，笔记录音走 `getUserMedia` + `MediaRecorder`（timeslice，由媒体管线驱动、不受 rAF 停摆影响）⇒ **§826 不命中随手记**。⛔ 但笔记侧有 §820 的对应物且**更重**：`onMicToggle` 与 `pickupPendingRecording` 两条建草稿路径**都没有 try/catch**，而建草稿要写原生文件 / Web base64；前端**没有** `unhandledrejection` 兜底 ⇒ 写失败时用户看到「录音正常结束、界面恢复原样、什么都没有」⇒ **整段录音白录且用户完全不知道它存在过**（§820 至少还留下一条谎报完好的记录，这里连记录都没有）。已落地：两条路径加 try/catch + `console.error` + 新增 `draftSaveError` （**独立** `v-if` 错误行，不挂 `v-else-if` 链 —— 失败时录音已停、`recError` 为空，挂在链上会被顶掉）+ 纯模块 `voice-draft-save-notice.ts` 文案（说清不会自动恢复、并给唯一可行动作：先抄文字再重录）。判据 8 例 + 变异 8 个全中。⚠ 变异 H 第一版无效（`String(e)` 在该分支里语义等价）⇒ 又是「0 红 ≠ 判据有洞」 |
| §833 随手记草稿正文**回填失败**完全无声：草稿看起来完整，AI 总结基于残缺正文 | ✅ §832 的**下游姊妹形态**，且**更隐蔽**：§832 是「什么都没留下」（用户知道出事了），这里是「留下一条看起来完整的草稿 + 一份**读起来完全正常**的摘要」。`runStop()` 回填正文（兜底转写之后唯一一次机会）的 catch 是**完全空的** —— 不 console、不写 `this.error`、不产出任何信号。而后果不是「录音丢了」（音频在草稿里好好躺着，误报会让用户白重录）：**① 草稿正文永久停在停止瞬间那版；② `presentVoiceDraft(note, text)` 收到的 `text` 从不写回笔记**（全函数只用它做空值门槛，正文一律以库里那份为准）**⇒ `/summarize` 读的是残缺正文 ⇒ 即时摘要漏了最后几句；③ 那份摘要读起来完全正常 ⇒ 用户无从分辨**。已落地：新纯模块 `voice-draft-update-notice.ts`（判据 `shouldBackfillVoiceDraft` + 文案 + 诊断前缀），catch 补 `console.error` + 写 `this.error`（停止后仍由 `recError` 那行单独渲染、直出不过 `apiError`）；预落库 catch 另补诊断（**不改行为**，那条的用户可见出口由 §832 的 `draftSaveError` 负责）。判据 16 例 + 变异 13 个全中 + 门侧 2 个变异打红。⚠ **触发率不是罕见分支**：回填只在「分片一条都没回来 ⇒ 兜底转写出字」时发生（§463 注释说那条件对**整段校对**几乎永远为假，但对回填**恰好相反**）⇒ 回填失败率 ≈ 兜底转写成功率。⚠ 撞门禁 `stt-error-render-chain` 的「error 写入点不变量」：按**该门自己留的先例**（`EMPTY_RECORDING_NOTICE` 具名白名单 + 「放行名字必须钉死定义」配套用例）加**一个**名字并补两条钉死用例，**没改它的判定语义、没删任何断言、没放宽正则**。⚠ 本轮两条通用式：① **判据函数必须是全函数**（负控传 `undefined` 当场抓出 `TypeError`，第一版 `finalText.trim()` 直接抛）；② **顺序类判据不能锚在某一处写法的字面量上**（§830 同款，本轮第二次）⇒ 改成枚举被比较量的**全部出现**逐个比位置 |
| §834 会议收尾**写库失败**完全无声，且「状态卡在 recording」会**自动开麦**重录 | ✅ 本轮抓到**两个缺陷**，且它们是同一条因果链的上下游。**D1**：会议收尾三处落库里，`saveMeetingAudio` 有 try/catch（§820 落盘失败写 null）、`syncMeetingMetadata` 被 `runBestEffort` 包着（注释明写「失败不得冒泡成未处理拒绝」），**只有写 `status: 'completed'` 的那次 `updateMeeting` 完全没有保护** ⇒ 抛错后：① 异常冒泡出 `stop()` ⇒ pill 那句 `toast.success('会议录音已结束并保存')` **根本不执行**（点完没反应）；② `activeMeetingId` 清零排在它之后 ⇒ runtime 仍认为在录；③ `completed` 全仓只写这一处 ⇒ 会议**永久停在 `recording`**、列表一直显示「录音中」。**D2**：同一个 `recording` 状态，仓库里有**两套相反契约** —— `meetings-live` 的注释与 `useSessionLiveRecord` 的**行为**都写明「提示恢复、**不自动重开采集**」，而 `MeetingDetailView` 却是 `|| m.status === 'recording'` ⇒ **静默自动开麦**：麦克风弹窗 + `seedSegments` + `start({resume:true})` ⇒ **把新录音混进同一场会议**，用户完全没打算录。已落地：纯模块 `meeting-finalize-persist-notice.ts`；收尾写库补 try/catch + `console.error` + `this.toast.error`（**刻意不写 `this.error`**：`MeetingRecorderRuntime` 没有那个 ref，那是笔记侧的；**也不写 `sttError`**，那是转写语义）；`lastFinalizePersistFailed` 提为公开字段并在 stop 开头清零；pill 加 try/catch 且**成功 toast 改为条件式**；详情页 `auto` 只认 `record=1`、`recording` 改为 warning 提示；两处提示语收敛到**同一个常量**。判据 18 例 + 变异 15 个全中。⚠ **删掉自动分支不影响任何合法启动**：三个「开始录音」入口都在 `createMeeting` 后立刻带 `?record=1`，进程内续录走 `micOn = wantMic || isRecording` ⇒ 该分支只在**进程被杀 / 收尾写库失败**时触发，恰是有害的那两种。⚠ 撞两道既有门：把标志放进 `stop()` 返回值会让 `audio-time-base`（§825）与 `meeting-audio-native-mode`（§826）同时打红 —— 它们把签名与那个**单行**返回对象**逐字**钉住 ⇒ **改我的设计、不动那两道门**，结论改走公开字段，并把这条约束钉成用例（★ 本仓真实约束：给 stop() 的返回值加信息前，先看这两道门）。⚠ 本节判据自己翻车三次：① 「无条件」写成「这一行不存在」⇒ 修复本身让判据变红；② 窗口 400 字符没盖住 `await updateMeeting(`（中间隔着 246 字符的中文注释）；③ ⭐ **在自己的注释上翻车**：扫 `catch` 命中的是我注释里写的「没有 try/catch」⇒ 引入 `codeOnly()` 先剥整行注释再判（边界据实标注：只挡整行注释，行尾注释仍可能命中）|
| §835 会议详情页两次主动编辑：**面板先关**与**半改半不改**，都完全无声 | ✅ §832/§833/§834 同族第四轮，但这次是「**失败与成功在界面上不可区分**」与「**只成功了一半**」。**D1 元信息保存**：`MeetingSettingsSheet.onSave()` 是 `emit(save); emit(close)` —— Vue 的 emit **同步**，父组件 `onMetaSave` 是 async 且没人 await ⇒ 面板在写库结果出来之前就关了、form 销毁；而父侧 `await updateMeeting(...)` **没有 try/catch** ⇒ 写失败时：一个未处理的 rejection + **零反馈** + **用户刚填的标题/主题/地点/参与人/标签已从屏幕上消失** ⇒ **面板关了 = 看起来存好了**。⚠ 这比 §834 更主动：§820「谎报存在」只是话没说清，这里**连「保存」这个动作都替你做了**。（对照：笔记侧 `NoteListView.onMetaSave` 本来就是「先写后关」，是会议侧单方面偏离。）**D2 说话人改名**：`labelSpeaker` 是 `for (…) { await updateSegmentSpeaker(…) }` ⇒ 任一段抛错就跳出循环：**前面几段已经改了、后面一段都没改**（无事务不回滚）⇒ 同一个人在正文里一会儿新名一会儿旧名；而 `onSpeakerLabel` 也没有 try/catch。更靠前一处：声纹标签 `upsertVoiceprintLabel` 写失败会**直接跳过整个循环**。已落地：新纯模块 `meeting-edit-failure-notice.ts`（`applySpeakerLabelToSegments` 注入写入因而可测 + 两句文案）；**sheet 删掉 `emit(close)`**（关闭权交父组件）；`onMetaSave` 加 try/catch、**成功才关**、失败 toast 且**保持面板打开**（输入还在可重试）；`onSpeakerLabel` 加 try/catch 并按落点统计提示；`labelSpeaker` 改走可测纯函数、声纹单独兜住、**返回落点统计**。判据 20 例 + 变异 15 个全中。⚠ 修法取舍据实写进注释：「跳过失败继续」而不是「遇错即抛」—— 抛错并不会带来一致性（已写成功的那几段不会回滚），只会丢掉「哪几段没改成」这个信息。⚠ **15 个变异里 5 个首版 0 红，全是判据自身的错**，其中两条可复用：① **固定宽度扫描窗口会串到相邻函数**（把 `try {` 换成 `if (1) {` 却因窗口尾部下一个函数自带 `try {` 而全绿）⇒ 已改成「以下一个函数声明为界」；② **「这一行存在」不等于「它被守卫着」**（`assert.match(/toast.warning(text)/)` 被 `if (false) toast.warning(text)` 骗过）⇒ 断言要要求守卫本身。✅ 顺带登记（未改）：`MeetingMetaSheet.vue` 全仓**零引用**，属简报里「26 个活着但零测试引用的导出」同一类 |
| §836 追加一句话与实时字幕入库：**静默消失**与 `void` 吞掉的 rejection | ✅ §131 一次加了**两条**用户写入路径（「更正一段」+「追加一句」），① 当时就有 try/catch，② `onAppendUtterance` **完全没有** ⇒ 同族缺陷的另一半一直没被看见（§835 修的正是这条链上的另一个入口）。② 上有**三次串行写库**（`rt.appendText` = `saveSegment`+`updateTranscript`；补落库分支再 `saveSegment`+`updateTranscript`+`refreshRelated`）⇒ 静默失败时用户面对的是三种**完全不同**的处境：**A 什么都没存**（他打的话凭空消失）/ **B 只存了一半**（分段进了、`transcript` 列没进 ⇒ 界面看得见、AI 总结·相关推荐·精校**读不到** ⇒ 两处真相不一致）/ **C 全存下了只是收尾没做完**（用户以为失败 ⇒ 重复操作 ⇒ 重复分段）。⚠ 给三者同一句「保存失败请重试」会**教错动作**：B 和 C 的用户重说一句就多一条重复分段。**D2 实时字幕**：`startLiveCaption` 里是 `commit: (text) => { void this.appendText(text) }` ⇒ `void` 不处理 rejection ⇒ 抛错时**什么都不发生**；而这条路径是系统语音识别的**每一句话** ⇒ 整场会议可能一句都没存进去，用户看到的字幕**却一直正常滚动**（谎报存在的第四个实例）。已落地：新纯模块 `meeting-append-notice.ts`（四个阶段各一句文案 + 字幕失败文案）；追加流程整段进 try/catch 并**在每个写库点之前先记 `appendStage`**；字幕 commit 改 `void …catch()`（`console.warn` + 写 `sttError`，⚠ 刻意不用 toast —— 字幕是连续的，toast 会连弹）。判据 14 例 + 变异 16 个全中。⚠ **`appendStage` 的标记位置有语义**：`segment` 必须标在 `await saveSegment()` **之前**，否则 saveSegment 抛错时用户收到「已存一半」⇒ 他不重说 ⇒ 那句话真丢了（独立变异 F 钉住）。⚠⚠ **`onAppendUtterance` 整段保持在同一函数体**：§836 第一版拆成 `appendUtteranceInner` 后，既有门 `segment-edit-proofread`（`fnBody(…, "onAppendUtterance")` 断言 `if (!saved)` 落库分支在函数内）**当场失明** ⇒ 改回内联，并用一次**结构变异**（真把分支搬走）验证加硬后的判据有牙。⚠ 唯一那个首版 0 红的变异值得单记：「只钉 `if (!saved)` 存在」在**整块被搬走、原处只留同名空壳**时仍全绿 ⇒ 已加硬为两标记都要在体内 |
| §837 会议详情页读取失败 = **永久骨架屏**；标题富化失败会**挡住自动开录** | ✅ 本轮症状形态换了一种：不是「东西没存下来」，是**页面挂住**。**D1**：`load()` 是线性的，`loading.value = true` → `await getMeetingWithSegments(...)` → … → `loading.value = false`；中间那行 `loading = false` 排在 await **之后** ⇒ 一抛错就**永远到不了** ⇒ 模板第一层 `<div v-if="loading"><Skeleton/></div>` **永久骨架屏** + 一个未处理 rejection + **零反馈**（没有错误行、没有重试入口）。⚠ 这不是罕见分支：`local-db.ts:859/881` 明确在「锁屏后回来 / 启动早于 `init(dbSecret)` / 切换工作区导致密钥不可用」时抛 `LocalDB 未初始化：…`，**是有明确预期的状态**。⇒ ⭐ **同仓两套契约**：同一次失败，`MeetingListView` 判 `dbNotReady` ⇒ 渲染 `DbLockedState`（带重新登录动作），而详情页把它当**永久加载中**。⚠⚠ **刻意不做的那件事**：「catch 里写 `loading = false」是最容易想到的改法，而它是**错的** —— `meeting` 仍为 null ⇒ 模板第二层 `EmptyState「会议不存在」` ⇒ 把**读取失败**谎报成**会议没了**。**D2**：`onMounted` 的顺序是 `load()` → `captureContext(m)` → `auto`/`onMicToggle()`，而 `captureContext` 里那次 `updateMeeting({location, title})` 抛错 ⇒ **用户从列表点「开始录音」跳过来后麦克风一个都不会打开**，且零反馈；触发面不小（新建会议 title 必空 ⇒ 每次都走到这次写入）。已核实前提：`captureDeviceLocation` **永不抛错**（失败 resolve(null)）⇒ 唯一抛错源就是 updateMeeting。已落地：新纯模块 `meeting-load-notice.ts`（`isLocalDbUnavailable` 与列表页同判据 + 两种失败两句文案）；`load()` 加 try/catch/`finally`（`loading = false` **移进 finally**、失败不清空已显示会议、成功清错误）；模板新增 `v-else-if="loadError && !meeting"` 整页错误态（带重试，**排在 `!meeting` 之前**）+ 行内横幅；`captureContext` 整体兜住并 `console.warn`（**刻意不提示用户**）。判据 17 例 + 变异 13 个全中。⚠ **两档错误契约**沿用仓库既有那道 `list-error-failpath` 门（同源，不同管辖：本视图不用下拉刷新）：**无行可显才接管整页，有内容时降级为非破坏性横幅** ⇒ 整页分支带 `&& !meeting`，行内横幅管「已有会议时刷新失败」。⚠ 唯一那个首版 0 红的变异是**正向对照自身写错**：断言取「`finally {` 起 200 字符」，那 200 字符**越过块尾**吃到函数后面另一行 `loading = false` ⇒ 删掉 finally 里那行仍全绿；已改成以 finally 块**闭合**为界。⇒ 这是本会话**第 4 次**「扫描窗口必须以语法边界为界」（§835 相邻函数、§836 块被搬走、§837 块尾越界） |
| §838 五个「对已有记录的动作」失败零反馈；两处 `catch { /* ok */ }`；笔记删除的顺序 | ✅ 本节把范围从「页面读不到」扩到「用户对已有记录做了动作」。**D1**：五个处理器此前都没有 try/catch —— 会议列表的归档/恢复/删除、会议详情菜单的三个分支（**裸 await + 无条件 `toast.success('已归档')`**，它排在 await 之后所以失败时不出现 ⇒ 用户只看到菜单点了没反应）、笔记元信息保存/删除、笔记详情删除。⚠ 这些操作**都把界面变更排在 await 之后**（没有撒谎，这点是对的），却**一句说明都没有**。**D2**：会议删除后的音频清理两处写成 `catch { /* ok */ }` —— 而 `deleteMeetingAudio` 写的是 **IndexedDB**（`openDB()` + 事务，`onerror` reject）⇒ 失败时音频 blob **永久残留**、零痕迹 ⇒ **静默的存储泄漏**，而注释声称没关系（§821 同款「注释与事实不符」）。⚠ 已核实它**没有 id 重用风险**（uuid ⇒ 不会错配音频），纯属空间泄漏 —— 这决定了它不是紧急项，但也更难被发现。**D3**：`deleteNote` 的顺序是「文件清理 → 标记 deleted_at」⇒ 文件删不掉时**连软删都没发生** ⇒ 用户点删除，笔记原封不动且零提示。⚠⚠ **同文件在媒体替换那处写下了相反原则**（「清理排在写成功之后……删除永远不能排在写之前 —— 写失败就没有回滚了」）⇒ 这条是**遗漏，不是取舍**。已落地：新纯模块 `src/utils/action-failure-notice.ts`（`utils/` 本就是特性无关纯函数的归处）；五处处理器 + 菜单三处分支各加 try/catch + 日志 + toast（**失败即 return**，界面不变）；两处音频清理改独立 catch 并按「主体已删、媒体残留」出文案（**明说不必重试**）；`deleteNote` **软删先行**、文件与向量清理各自兜住、返回 `{ mediaCleaned }`；三个视图文件此前都没有 toast，各引入 `useToast`。判据 19 例 + 变异 18 个全中。⚠ 代价据实写进注释：改顺序后「文件删不掉」从「删除没生效」变成「孤儿文件残留」—— **有意的取舍**，用户意图优先，且与会议侧顺序一致；残留有诊断且被文案显式告知。⚠ **本节 3 个变异首版 0 红，且我自己在同一节里重犯了两个已记录的错**：① ⭐ 我把三个处理器的 try/catch 抽进 `guard()` 辅助函数 ⇒ **本节自己的判据当场报红**（判据锚在 `onArchive` 体内）—— **§836 刚记过的反模式，又犯一次**；② ⭐ 「有 catch」≠「catch 里有处理」：把 `catch (e) {` 改成 `catch {` 而诊断行仍在时 `match(/catch/)` 照样全绿；③ ⭐ D3 组两处清理只隔几行，`slice(at+400)` 把**下一处**的 `catch (e)` 算进来 ⇒ 0 红 ⇒ 改用「锚点后**最近**的 catch」。⇒ 这已是本会话**第 5–6 次**「锚点没有由语法或锚点界定」。⭐ 另有一条教训：**「禁止所有裸 catch」是条假规矩** —— 第一版那么写，命中一堆**正当**既有写法（`catch { draftBanner.value = null }`、`catch { /* empty */ }`…）⇒ 会逼人改无辜代码；已收窄为「完全空且无说明」与「写着 `/* ok */`」两种**吞掉失败**的形状 |
| §839 会话内实时录音链的三个失败形态：建会议无反馈、**收尾被人主动静默**、读不回分段就「正常结束」 | ✅ §838 是「对已有记录的动作」，本节是「**启动与收尾**」，且其中一条是**被人主动 `.catch(() => {})` 掉的**。**①** `start()` 里 `createMeeting` 是裸 await，而 `toggle()` 被 `@live-record="liveRecord.toggle"` **直接绑成事件处理器** ⇒ 写库失败时用户点麦克风**什么都不会发生**。**②⚠** 启动失败后的收尾是 `await updateMeeting(m.id, {status:'completed'}).catch(() => {})`，而**它上面的注释写的正是它失败时会发生的事**（「否则遗留的 recording 状态会在每次进入会话时触发"上次录音未正常结束"警告」）⇒ 清理写失败 ⇒ 会议**永久**停在 `recording`（列表一直显示「录音中」）+ **此后每次进这个会话都弹那条警告**，而用户根本没录成（他只看到「无法开始录音」）⇒ **误导性警告**。⇒ 与 §834/D1 同源（那是 stop 的收尾，这是 start 的收尾），但更隐蔽：它是**被人主动静默**的，不是「忘了写」。**③** `stop()` 里 `getMeetingWithSegments` 是裸的 ⇒ 抛错则 `finalizeRecording`（整段重转→说话人归因→精校→行动项→总结）**整段不发生**，而此时录音已停、状态已写 completed、面板已关 ⇒ 用户看到的是**一次正常的结束**。⚠ ③ 的处置**刻意是「停下」而不是继续跑**：拿不到分段还去跑收尾编排，等于把残缺输入交给一条**会重写 `transcript`** 的链。已落地：新纯模块 `live-record-failure-notice.ts`（三形态三句话）；`createMeeting` 与读分段各进 try/catch；`.catch(() => {})` 改显式 try/catch + `cleanupFailed` 标志 + **单一出口**（收尾失败只说收尾那一句，不连弹两条 error）。判据 13 例 + 变异 13 个全中。⚠ 本节 2 个变异首版 0 红，**都是「窗口/正则切错位置」**（本会话第 7–8 次同族）：① 判据写「`start()` 函数体里有没有 `try`」，而该函数**后面还有收尾那处 try** ⇒ 变异把建会议换成 `if (1) {` 照样全绿 ⇒ 改成「**建会议调用之前**有没有 try」；② 修完又把自己写红 —— 把 `catch (e)` 也塞进「调用之前」的窗口，而 catch 处理器当然在调用**之后** ⇒ 改为「call 在前、catch 在后」的顺序判据；③ 文案断言 `/没有留下|没有创建|没留下/`：删掉后半句后「会议记录**没有创建**成功」仍在 ⇒ 整条废掉 ⇒ 只认「没有留下」。 |
| §840 列表页「首屏读取失败」被显示成「还没有记录」，并把用户引导去新建 | ✅ §837 是**详情页**单次读取（永久骨架屏），本节是**列表页首屏**读取，且失败被谎报成「空」。**①** 首屏读失败时 `listStatus==='failed'` ⇒ 旧 `loading`（只认 `idle`）为 false、行数 0 ⇒ **正好落进 `EmptyState`「还没有会议记录 / 点击右下角麦克风开始第一次会议」**；那颗「加载失败，点击重试」在 `行数>0` 分支里 ⇒ **永不渲染** ⇒ 用户已有会议却看到「一场都没有」，并**被引导去新建一场**。**②⚠ 更深**：`dbNotReady → DbLockedState`（本地库未就绪 ⇒ 重新登录）**根本不可达** —— `resetQuery()` 返回 `void` 不抛、控制器链自带 `.catch` 吞掉 fetch 错误 ⇒ `load()` 里的 catch 是**死代码** ⇒ 「未就绪」与「磁盘忙」在界面上完全同形（实测两种失败 status/行数/dbNotReady 三项全同）。**③** 骨架屏在请求发出前就塌成真空态（`idle`→`refreshing` 是同步的）。**④** `MeetingListView.vue:133` 的注释声称「dbNotReady 仍由 fetchPage 内捕获」，而那个 `fetchPage` **一个 catch 都没有**（§838 同族：注释声称没关系，实际是泄漏）。**⑤** 同仓 `EmailInboxView` 直接 `await showLocal(true)` ⇒ 它是对的 ⇒ **三个列表页里唯一走控制器的两处是坏的**。已落地：新纯模块 `src/utils/list-load-state.ts`；两处 `fetchPage` 加 try/catch + 判「未就绪」+ **`console.error` 带 tag** + **必须 rethrow**（返回空结果会被控制器当成成功 ⇒ 失败分支成死代码，这一条我自己写错过一次）；模板加**带「无行」守卫**的失败分支 + 重试入口；`load()` 注释改为「只兜同步抛出」。判据 **37 例** + 变异 12 个全中，并做了**对真实 `.vue` 的三路端到端变异**（M1 拿守卫 ⇒ 判据 2 红 **且仓库门禁 3 红**，M2 不 rethrow ⇒ 2 红，M3 分支挪位 ⇒ 2 红）。⚠ 顺带把仓库门禁 `list-error-failpath` 的 `MeetingListView` 登记由 `n/a` 改 `enforced`（原 `n/a` 理由「模板里没有任何 error 参与的条件」**正是缺陷本身**），并把量具自证的 `enforced` 计数 2→3 且**加一条钉住该条目本身的断言**（只数个数的话，退回 `n/a` 再补别的条目数目照样对）。⚠ **刻意的踩雷式命名**：标志叫 `readError` 不叫 `readFailed` —— 那道门用 `/error/i` 认错误分支，条件里没有 `error` 一词会被整体跳过、守卫无人看守（**判据的识别词是契约的一部分**）。⚠ 本节 `blockFrom` 量具**连错两版**：① `/^\s*\}\)/m` 先撞上 `listMeetings(size,{…})` 的收尾；② 改括号配平后锚点后第一个 `{` 是参数解构 `({ page })` 的括号、立刻归零只取到 26 字符 ⇒ **锚点后第一个括号不一定是函数体的括号，要先越过 `=>`**。 |
| §841 收件箱的错误态「门禁里是绿的、界面上从来没显示过」，并**更正 §840 的一个错误断言** | ✅ §840 是会议/笔记，本节是收件箱，且**踩的是同一个根因的第三处**。**⛔ 先更正**：§840 我写过「`EmailInboxView` 直接 `await showLocal(true)` ⇒ 它的 catch 可达，**三个列表页里唯一走控制器的两处是坏的**」—— **这句是错的**，我只看了 `load()` 的形状没往下追 `showLocal → refreshList → controller.refresh → loadPage`，那个 `.catch(() => setStatus('failed'))` **吞掉且不重抛** ⇒ `await refreshList()` 正常 resolve ⇒ 正确说法是「**三个都走控制器，三个的 catch 都收不到错误**」。⇒ 这是 §840 自己刚写下的「阴性声明必须抽验」在我身上复发：**凡是要说「某处没问题」的阴性断言，都要问「我追到叶子节点了吗」**。**①** 实测两种失败下 `loadError` 恒 `''`、`dbNotReady` 恒 false、`status` 恒 `failed`、行数 0 ⇒ 落到 `shownEmails.length === 0` ⇒ **谎报「收件箱是空的」**。**②⚠ 本节最要紧**：`list-error-failpath` 把它登记为 `enforced`（错误分支必须带行数守卫），**守卫确实在、门禁确实是绿的**，而错误标志设不上 ⇒ 那条分支**永远不渲染** ⇒ **守卫只是必要条件，不是充分条件**；把真缺陷原样放回去（M3），**仓库门禁依然绿**。**③** `EmailInboxView.vue:123` 注释写「任何抛出都会设 `loadError`」—— **没有任何东西会抛**（§837/§840 注释孤儿同族，第 3 次）。**④** `listStatus` 被解构但只有 `'loadingNext'` 有消费者 ⇒ `'failed'` 在整个模板里**零消费者**，有行时也没有横幅。已落地：`readPage` 依赖加 try/catch（**唯一看得见 e 的地方**，因为 `fetchPage` 由共享的 `makeInboxFetchPage` 生成、没有视图状态可写）+ 必须 rethrow；`readError = loadError !== '' \|\| isListReadError(...)`；整页错误态与横幅都改认 `readError`；共享模块新增「有行」那一支文案（与无行**分两句**，混成一句会让有行场景以为数据是全的）。判据扩到 **58 例**（三个视图进同一张表）+ 四路真实文件端到端变异全中。⚠ **顺手修了自己踩碎的一道门**：`z-index-ladder` 变红（两处裸数字 `z-index: 4/5`，**HEAD 就存在**，不是本节引入）—— 是我的编辑把行号推了 42 行而它的 ALLOWLIST **按行号登记**；该门注释自己记着这已是**第四次**咬人（声明行 → 块起始行 → 这次）⇒ 按既有先例重新编号并留注，**建议改成 `文件:选择器`**，已登记待拍板。⚠ 另外 `list-error-failpath` 自带的 M1/M2/M5 变异是**字面量 replace**，我改写条件后它们**静默变成空操作**并报了个**错误理由**（让人去查枚举器）⇒ 改为按形状 `stripLengthGuard()` 并加 `assert.notEqual`。⚠ 本节探针第一版把数字当数组用（`shown.length` 是 `undefined`）⇒ 一行**假的阴性**读数；「0/否」的第一嫌疑永远是量具坏了。 |
| §842 邮件目录 `reload()` 只有 `finally` 没有 `catch` ⇒ 谎报「还没有自定义目录」，并让新建/删除给出**与真实结果相反**的提示 | ✅ §840/§841 是「错误被控制器吞掉」，本节是**同一个谎报家族的第 4 处**，但机制不同：这里 `reload()` 连 catch 都没有，异常直接逃出去。**①** `listLocalFolders()` 走 `localDB.query()`，而 `local-db.ts:859` 在锁屏/启动顺序/切换工作区时抛 `LocalDB 未初始化`（有明确预期的状态）⇒ 实测异常**逃出 reload**、folders 停在 `[]`、loading 归 false ⇒ 落进 `folders.length === 0 && !loading` ⇒ **谎报「还没有自定义目录」并引导用户去新建**。**②** `onMounted` 是 `void reload()` ⇒ **未处理 rejection**。**③⚠ 本节最阴的一条**：真正被写坏的不是列表而是**提示** —— 新建成功后错误落进**已关闭**的面板（用户完全看不到，只剩成功 toast）；删除成功后**连弹「已删除」+「删除失败」两条矛盾的话**（探针读数：success 目录已删除，随后 error LocalDB 未初始化）。⚠ 但这条**不是额外打的补丁**，而是 ① 的下游后果：`reload()` 自己兜住后两个 catch 根本不会再触发，`onCreate`/`onDeleteFolder` 一行没改。**④** `syncFoldersFromServer().catch(() => {})` 是 §838 明令禁止的空回调形状，改为显式 catch + `console.warn` + 「下面是上次同步到的目录」提示。**⑤** 门禁把本视图登记 `n/a`，理由「唯一的 error 条件是 BottomSheet 内的 createError」—— 而那个 createError 恰恰是**「创建失败」的误报载体**，**登记理由写下的那天就是错的**。已落地：`reload()` 加 catch（不再向外抛 ⇒ ①②③ 一并消失）；`listReadFailureNotice` 新增 `dbUnavailable` 那一支（本地库未就绪 ⇒ 重试没用要重登，与其它失败分两句）；新增 `readError` + 永远非空的 `readErrorNotice`；模板加带「无行」守卫的失败分支 + 有行时横幅；补 `.list-error-banner` 的 scoped 样式（本文件原本没有，不加就是无样式 div）。判据扩到 **67 例** + 五路真实文件端到端变异全中。⚠ **两处形状坑**：① 失败分支必须用 `v-if` 开头 —— 它前面是 `v-for` 按钮，`v-else-if` 没有相邻 `v-if` 兄弟会被 **Vue 编译直接拒绝**（`build:gate` 红）；② `vue-tsc` **不用模板条件收窄后续属性** ⇒ 直接绑 `readError.title` 报 `TS18047 possibly null` ⇒ 需单独一个永远非空的绑定值。⚠ 门禁登记表 `EmailFolderListView` 由 `n/a` 改 `enforced`，`enforced` 计数 3→4 且棘轮改为**逐个钉住**。⚠ **M4 那行值得单独记**：标志名里有 `Error` 一个词，仓库门禁就认得它并看守守卫；改成 `readFailure` 它立刻变瞎 —— **同一个语义，两种命名，一道门一个认一个不认**。⚠ 阴性结论带读数：`SessionListView` **已取证为阴性**（`loadSessions()` 直接 `await api.getAllSessions(...)`，catch 可达、`error` 设得上，登记 enforced 属实）；`TasksView` **未取证**。 |
| §843 任务与会话的 loader 在 catch 里**主动清空**已加载的行 ⇒ 一次失败整屏消失，并引导用户建重复任务 | ✅ §840/§842 是「失败被**显示成**空」，本节是「代码**主动擦掉**」—— 同一个谎报家族里最重的一处，也是那道为它建的防线上唯一的缺口。**①** `loadTasks()` / `loadSessions()` 的 catch 各有一句 `tasks.value = []` / `sessions.value = []` ⇒ 实测失败后两处行数都是 0（对照：成功时保留）⇒ 落进 `v-else` ⇒ 「暂无运行中的任务」+ **「点击「+ 新任务」创建」**（引导建重复任务）；会话区同理「暂无会话」+「点这里提问，开始新对话」；失败路径上**零用户可见说明**（只有一个 `console.error`）。**②⚠ 全部结论所在**：这正是 `list-error-failpath` 契约原文（「刷新失败时**必须保留已经显示的行**」），但它的判据是「挂在 EmptyState 上的**错误分支**必须带 `.length` 守卫」—— **这里连错误分支都没有，只有一句主动清空** ⇒ 那道门无从下手。⇒ **一道门覆盖的是它建模的那个失效形态**：UI-07e 是「错误态遮蔽行」，门去查错误态；后来出现的「代码主动清空」不在那个模型里，**门绿着、缺陷在**。**③** 登记表理由「accStore.error 渲染在弹窗内的 .acc-error」**只描述了委托弹窗那一处**，对列表主数据通路一个字都没说。已落地：两个 catch 改 fail-open（保留已加载行，沿用仓库既有契约 `continuousList.ts` refresh 注释与 UI规范 07 §2）+ 挂 `tasksError`/`sessionsError`；两处各加「带 `.length === 0` 守卫的失败分支 + 可点重试」与「有行时的非破坏性横幅」；补 `.list-error-banner` 的 scoped 样式；登记表 `TasksView` 由 `n/a` 改 `enforced`，计数 4→5。判据扩到 **78 例** + 六路真实文件端到端变异全中。⚠ **M1/M2 那一列的 0 是本节的全部结论**：把两个真缺陷原样放回去，那道门**依然全绿**；而 M3/M4（拿掉守卫）各报 3 条红 ⇒ 它能看守「错误分支的守卫」，看不见「catch 里主动清空」。⚠ **量具抓到本会话最隐蔽的一次假绿**：`blockFrom` 只会处理箭头函数，遇到 `async function loadTasks() {` 找不到 `=>` ⇒ 一路找到后面某个 `=>` ⇒ 返回一段无关切片；§842 的 `noUnguardedTry` 正是在这种垃圾切片上通过的 ⇒ 已重写为两种形状都支持。⚠ 另一次：`indexOf('暂无运行中的任务')` **命中了我自己写的注释**（注释正是在解释这段代码）⇒ 新增 `tplCode()`（剥 HTML 注释）；凡用界面文案当锚点的判据一律走它。⚠ 阴性/覆盖口径：§841/§842 待办点名的视图至此**全部取证完毕**；但**我没有声称全仓所有读失败路径都已覆盖** —— 本节枚举范围是「`list-error-failpath` 登记表里的下拉刷新视图」，非下拉刷新页面的同类缺陷**未取证**。 |
| §844 全仓扫面：store 层清空列表 + 视图从不渲染 store 的 `error` | ✅ §843 是视图层的一处，本节是**同形状的 store 层**那一处，且定性比前几节微妙。**扫面读数**（`.artifacts/s844-sweep/`，652 文件、catch 块内含 `X = []` 的候选 **7 处**）：`stores/opencode.ts::loadSessions`（本节）、`TasksView`（§843 已修）、`useConfigList::load`、`InstanceListView::loadInstances`、`EmailSummaryView::loadList`（**未取证**）、`ScheduledTaskEditView`（清的是预览且有 `previewError` ⇒ 合法）、`opencode.ts::loadInstances`（消费方 `OpenCodeHub.vue` **确实**渲染 error ⇒ 不是谎报，本节未改）。**①⚠ 最微妙的一条**：store 那句无条件 `this.sessions = []` **不是疏忽，它在挡一个更糟的缺陷** —— `selectInstance` 先换 `selectedInstance` 才读数据，读失败又不清就会在**新实例标题下显示旧实例的会话**。但它同样挡住了另一件事：`refresh()` 与 WS 重载调的是**同一个 id**，一次抖动就抹掉正在看的列表。⇒ **同一个赋值，两种调用路径需要相反的行为** ⇒ 引入 `sessionsOwnerId`（当前 sessions 属于哪个实例）把「清不清」变成可判定：成功记录归属，失败时 `sessionsOwnerId !== instanceId` 才清。**②** `features/opencode/SessionListView.vue` 全文件 `error` 出现 **0 次** ⇒ store 設了 `this.error` 但**没有任何消费者** ⇒ 失败时显示「暂无会话」+**「在 OpenCode 中开始一个新对话」**（引导开不必要的新会话），而顶部横幅仍显示「N 活跃 • M 空闲」（同一份被清空的数据算出）⇒ **横幅与正文互相打脸**。⚠ 该视图与 `features/sessions/SessionListView.vue` **同名**，排查极易混淆（§842 取证为阴性的是后者）。已落地：`sessionsOwnerId` + 按归属清空；视图加 `readError`（**不把 store 的技术串甩给用户**）+ 带 `.length === 0` 守卫的失败分支 + 有行时横幅 + 补 scoped 样式。判据扩到 **86 例** + 五路真实文件端到端变异全中。⚠ **M5 第一版 0 红 —— 那是判据无牙不是没缺陷**：我查的是字面量（模板里有没有直接绑 error），而变异是「在 computed 里手搓 `{ title: msg }`」，语义一样、形状查不到 ⇒ 已改为查**结构**（notice 必须来自 `listReadFailureNotice(` 且 `msg` 不得进任何用户可见字段）⇒ 修完 M5 红 1。⚠ **第三次自伤**：我第一版注释里写出了那个容器的标签名 ⇒ 那道门靠 `text.includes('<标签名>')` 判视图类型 ⇒ 本视图被误认成下拉刷新视图，门禁立刻报「未登记」⇒ 已改写措辞。⚠ **本节 5 路变异的门禁那一列全是 0** —— 与 §843 同一结论的加强版：这类缺陷（store 层清空 / 视图不读 error）**完全落在那道门的覆盖范围之外**。⚠ 覆盖口径：判据只查「catch 块体内出现 `X = []`」，**不覆盖** splice/undefined/直接清 IndexedDB，也不覆盖「静默重试后放弃」；7 处候选里 3 处**未取证**。 |
| §845 收掉 §844 自己登记的缺口：配置列表把 composable 的 `error` **明确丢弃**（`void error`） | ✅ 本节先把上节表里 3 处「未处理」**逐个取证收掉**：`EmailSummaryView` 是**假阳性**（`:12` 错误分支排在空态 `:17` 之前，且 `:104-110` 有注释明写它早已改过 —— **它是本仓最正确的一处**）；`InstanceListView` 有 `v-else-if="error"` + 重试 ⇒ **不是谎报**，但 `:115` 仍清空且 `:9` 有刷新按钮 ⇒ **刷新失败会丢行**（**未修**，已登记）；`ConfigList.vue` ⛔ 本节处理。**①⚠ 注释与代码直接矛盾**：`const { instances, loading, error, load } = useConfigList()` 紧跟 `// error 暴露给模板` 与 **`void error`** ⇒ 那句注释描述的机制**就写在它的下一行，而那行是它的反面**。⇒ **本会话第 4 次撞上「注释声称的机制要单独验一次」**（§837/§840/§841 各一次），且前三次是注释孤儿，这次是「注释 + 反面代码相邻」。**②** composable 的 catch 既设 `error` 又清 `instances`，而视图两样都不显示 ⇒ 实测失败时 error 非空、instances 0 行、模板落进「没有可用的实例」+**「请先配置 OpenCode 实例」**（引导做一整套配置）、页面**零重试入口**。**③⚠ 那次清空本来无害，是我加的重试让它变有害**：本页调用点只有 `onMounted`，首次加载时 `instances` 本就是空的 ⇒ 写成「原来就有 bug」是**误判**；是新加的重试入口引入了「有行时点重试失败」这个场景。已落地：删 `void error` 与假注释；`readError` + 永远非空的 `readErrorNotice`；失败分支（带 `.length === 0` 守卫 + 重试）+ 有行时横幅；**真空态加 `!readError`**（否则失败态与真空态仍会同时成立）；composable 的 catch 改为不碰数据。判据扩到 **94 例** + 四路真实文件端到端变异全中。判据里有一条**可机械检查**的核心不变量：`/\bvoid\s+error\b/` 不得出现 —— `void x` 是「我明确不用它」，钉住它就把「注释声称 vs 代码事实」这一族从人工核对变成自动判据。⚠ **两处形状坑**：① 失败分支必须用 `v-if` 而非 `v-else-if`（前面隔着 `v-for`，无相邻兄弟，Vue 编译拒绝）—— ⚠⚠ **这是 §842 同一个坑的第二次**，我在 §842 文档里已把它记成教训却又在 §845 踩中，而抓到它的是仓库的 `vue-template-compile` 门禁、**不是我自己的判据** ⇒ 教训还不够，必须变成一条能自动跑的门。② `ConfigList.vue` 是**脚本在前、模板在后**（多数视图相反）⇒ 量具 `templateOf` 的假设对它不成立，第一次跑 6 条判据一起假红；已改为「脚本区无 `<template` 时从第一个取到最后一个」。⚠ **四处都不在** `list-error-failpath` 管辖内（既没用下拉刷新容器，也没有 EmptyState）⇒ 与 §843/§844 连续三节同一结论：这类缺陷系统性落在那道门之外，它只管「用了下拉刷新 + 挂了空态组件」的视图。 |
| §846 教训要长在**我天天跑的那把尺子**里：`v-else-if` 相邻性进自己的判据 | ⚠ 本节修的是**判据的盲区**，不是产品缺陷，代码侧一行未改。口径先说准：**CI 从来没漏过** —— 仓库既有 `vue-template-compile` 门一直在 `npm run gates` 里覆盖全仓 `.vue`，两次 `v-else-if` 撞坑（§842 / §845）都是它抓到的；**缺的是我自己那份**（`list-load-state.test.ts`），因为它全是**源码文本扫描**，而「这个 `v-else-if` 有没有相邻的 `v-if` 兄弟」是**结构问题**，文本扫描在原理上就无感 —— 而这恰是新写分支时最可能写错的那一处。⇒ §842.7/§845.7 那句「教训要落地成能在 CI 里跑的那一条」**说浅了一层**：教训写在文档/注释/提交信息里都**不会**自动生效，只有长在**我每次改动都会跑的那个文件里的断言**才算数。已落地：新增 describe「§846 · 模板可编译性」**13 例**（94 → **107 全通过**），对 §840–§845 **改过模板的全部 7 个视图**逐个 `parseSFC` + `compileTemplate`（与仓库那道门**共用同一对函数**、同样把 parse 阶段错算失败、同样跳过无模板组件）；另含 6 条负控/正控：孤立 `v-else`、未闭合标签、无模板组件跳过、登记表漂移（**登记表与枚举不一致会静默少扫**）、`assert.notEqual`（**变异没生效要单独报**）。**真实文件**端到端变异读数（`.artifacts/s846-probe/`，非测试内构造）：把 `ConfigList.vue` 真的改成 `v-else-if`（前一个兄弟是 `v-for`）后 —— 自己那份 **105 pass / 2 fail**（红的只有新增那组）、仓库既有门 **186 pass / 1 fail**、复原后 **107/0** 与 **187/0**；**⇒ 这组读数就是本节全部论点：§840–§845 那 105 条文本判据，对一个会让 `vite build` 整体挂掉的缺陷完全无感。**⛔ 未实测：未跑过一次真实构建，模板错误导致构建挂掉的具体表现只有编译器与测试读数（§846） |
| §847 全仓扫面：错误标志被生产，却**没有任何出口**（13 条逐个取证 → **3 处真缺陷**） | 把 §844/§845 的两个单点变成一道可自动跑的审计：枚举 `stores/`+`composables/` 下每个错误标志，断言「至少有一个 `.vue` 的**模板区**引用它」。⚠ 判据必须落在模板区 —— `const { sttError } = useVoiceInput()` 这种解构会让标识符出现在 `<script>` 里，但它**不构成出口**（§845 的 `void error` 就是活证）。**① D1 ⛔ 审批拉取失败完全不可见（human-in-the-loop 死锁）**：`stores/approval.ts::fetchPending()` 失败只设 `error`、保留上一次结果（源码注释「仅记录」），而那一格**全仓无人读**；`ApprovalPanel` 根节点是 `v-if="store.hasPending"`，首屏失败时上次结果是空数组 ⇒ 整块面板不渲染 ⇒ 智能体卡在等人批准、用户看不到。⭐ **一致性对照**：同一屏已为另一条审批路径做好正确形态（`SessionConversationView.vue:642-646`「审批状态拉取失败」+重试）—— **同类数据在别处已被处理，这里没有**。**② D2 ⛔ AI 优化失败完全不可见**：`optimizeError` 全仓 4 处引用**全在 composable 自身**，消费方只解构 3 个字段、调用处连 `onError` 都不传 ⇒ 点「优化」后按钮停住、无文案、草稿不变。**③ D3 ⛔ `loadAgents()` 失败 = 未处理 rejection + 空列表谎报**：`loadAgents` 在无本地缓存分支 `throw e`，**5 个调用点一个都没 catch**（含两处 `await` 在 `onMounted(async)` 里 ⇒ 后续初始化整段不执行）；`AgentLibraryView` 落进「暂无角色」+「创建第一个自定义角色」，引导建重复角色。⚠ **纠正一个我差点写错的结论**：`AgentEditView` 的错写**没有**造成数据丢失（`handleSave` 有三道非空校验挡着），真实后果是「编辑态拿不到角色 ⇒ 落进创建分支 ⇒ 编辑一个角色却新建出第二个」，已加显式拒绝守卫。已落地：横幅用**独立 `v-if` 放在 `hasPending` 之外**（塞进面板里等于没做）；`optimizeError` 接进两处既有提示位；5 个调用点接住 + 两个列表页加失败分支与重试。判据 **28 例** + **四路真实文件端到端变异**全中（变异后 20/8）。⚠ **撞到一道我不知道的既有门且它是对的**：`store-error-consumers` 报 3 处「store error 未归一就渲染」—— `approval.ts` 存的是**原始 message**（必须归一，已改 `useApiError`），而 `chatAgentStore.error` 存的**已归一**（再套一层会被 `toUserMessage` 判为无 i18n key 而退回 fallback、丢掉真实文案）⇒ **归一发生在哪一层要逐字段判断，不能一刀切**。⚠ 三处量具自伤：块注释里写带通配的路径会提前闭合注释（报错行号指向十几行之后）；判据被自己写的 HTML 注释抢锚点而假红（加 `tplCode()` 剥注释）；**变异改内存串而判据读磁盘文件** ⇒ 变异永远为真（加 `overrides` 注入口）。⚠ **探针脚本自己也犯了一次「变异没生效却报成功」** ⇒ 探针脚本本身也要有变异自检。⚠ 口径：只回答「有没有出口」，**有出口是必要条件不是充分条件**；`outlet:false` 的反向断言**故意没做**（`AgentSyncSheet` 有同名局部 `lastError`，按名找出口必然假阳）⛔ 未实测（三处缺陷的真实发生率与真机观感）（§847） |
| §848 把「会 throw 的函数 × 调用点接不接住」做成扫面（158 → 8 → **1 处真缺陷**） | §847-D3 的形态是「刻意 rethrow 的函数被调用时没人接」，那节只登记成 `RETHROWERS` 一条，是**点**不是**面**。本节扫成面：**① 口径三次收窄**：全仓 ±15 行窗口判 catch ⇒ **158**；收窄到「定义方必须在 stores/composables/ + 先剥注释」⇒ **8**；逐条人工复核 ⇒ **1 真缺陷**。⚠ **函数名不能当标识，除非定义方唯一** —— v1 里 `read` 50 / `load` 34 / `remove` 10 是同名不同定义撞车，另有大量注释文档行被当成调用点。**② D4 ⛔ 拍照失败零反馈**：`useCameraCapture.pickImage` 对取消返回 null、对其它错误 `throw err`，注释原话「真实错误上抛**由调用方提示**」—— 而唯一消费方 `UnifiedComposer.onCamera` 是裸 `await`，被 `@click` 直接调用 ⇒ 相机权限被拒时按钮点了没反应，只有一个未处理 rejection。⚠ 这是「注释声称的机制要单独验一次」的第 **5** 次，形态是**跨文件**的：注释在 composable、缺陷在视图 ⇒ **只读生产点永远看不到**。**③ 阴性（带读数）**：`FlashcardEditView` 的 4 个 `@click` 是**同名本地包装**（`:332` 内部有 try/catch + `flashcards.edit.imagePickFailed`）⇒ 全是假阳；这也正是「每个调用点都要有 catch」这条机械判据在 `pickImage` 上**不适用**的原因，已写成判据注释而不是硬套。已落地：`onCamera` 加 try/catch，错误经 `useApiError` 归一写进 `cameraError`，在**两处**工具行（普通 + 全屏）紧挨 `sttError`/`optimizeError` 渲染。判据扩到 **32 例**（含一条**消费方反向对账**，新增消费方会逼我确认它接没接住）+ 真实文件变异 M5 全中（变异后 30/2，复原 32/0）。⛔ 扫面本身只是**候选生成器**，「接住了」用 ±15 行窗口**近似**；探针**不剥 HTML 注释** ⇒ 把我自己写的一条 HTML 注释报成了候选（§848） |
| §849 空 catch 扫面：真正的问题不是「catch 为空」，而是**失败发生在设成功态之后** | 扫面两次收窄：全仓 catch 711 个、其中空 catch **128** 个；只看 `.vue` 且所在函数**被用户动作直接触发** ⇒ **3** 个。⚠ **128 个里绝大多数是正当的**（幂等清理 `/* already stopped */`、尽力预载、用户取消、插件缺失时的降级）⇒ **「凡是空 catch 就算缺陷」这条判据第一天就会是错的**；3 个里还有 2 个是阴性且理由站得住（`StudyHubView.advance`「不要弹 toast 打断心流」、`setDailyDigest`「不显示一个其实没保存成功的选中态」）。**① D5 ⛔ `RssItemDetail.doShare('clipboard')` 界面在主动说谎**：服务端成功 → `shareResult` 已设上并渲染 → 再写剪贴板 → 失败被 `catch { /* 静默 */ }` 吞掉 ⇒ 用户看到复制内容、粘贴出来却是空的上一次内容，**无任何可挽回迹象**。⚠ 与「普通静默 catch」的本质差别：**失败发生在「对外宣称成功」之后** ⇒ 比「没提示」严重一个量级；且这个失败**在本仓被记录过**（`useVoiceInput.ts:64`「WebView 可能无剪贴板权限」）⇒ 不是假想分支。**② 落地时踩到的坑**：刻意**不复用**页面级 `errorMsg` —— 它挂在 `v-else-if="errorMsg"` 上与 `<article>` **互斥**，用它报「复制失败」会让正文整块消失 ⇒ 判据把这条钉成断言（不得是 errorMsg + 必须在 modal-mask 之后）；复位点两处（`openShare()` 与 `doShare()` 开头），否则上次失败的提示会挂在这次成功的分享上。判据 **6 例**，含一条**全仓空 catch 棘轮（上界 21）** + 真实文件变异 M6 全中（变异后 1/5，连棘轮一起红 21→22，复原 6/0）。⚠ **棘轮上界必须按真实基线定**：第一版我拍脑袋写 20 而实测基线就是 21 ⇒ 提交当天就红，且报的是「新增空 catch」，会把人引去查一个**并不存在的回归**。⚠ 另有一处量具自伤：判据文件在 `features/rss/__tests__/` 下，回退层数少一层 ⇒ 报错落在第一个 suite 上，看起来像「判据本身有问题」，容易查错方向（§849） |
| §850 `void x(...)` fire-and-forget 扫面：**净得 0 处缺陷**，换来一道防回归的门 | `void someAsync()` 显式声明「我不处理返回值」⇒ 它一旦 reject 就是未处理 rejection（与 §847-D3 `loadAgents`、§848-D4 `pickImage` 同族）。扫面四次收窄：全仓 **156** → 整条语句无 catch/then/finally **127** → callee 是本文件裸名函数且体内无 catch **16** → 修掉探针括号 bug 后 **15** → 逐条人工读源码：**15 条全部安全，净 0 缺陷**。⭐ **本节最重要的结构观察**：这 15 条**安全的原因不在 callee 自己身上** —— `fetchMonth` await 的 `store.load` 自己 catch 并设 error（视图渲染它且空态带 `!store.error` 守卫，**§840 的正确形态**）；`submitSystemBack` await 的 `dispatchBack` 自带 try/catch；`flush` await 的 `persistTo` 自带 catch；`refreshRelated` await 的 `searchRelatedContext` 三个子查询各自 catch 返回 `[]`。⇒ **「callee 体内必须有 catch」这条机械规则会把这 15 条全部判红（假阳 100%）** ⇒ 只能逐条登记 + 双向对账。判据 **9 例**（登记不许漂移 / 登记条目必须仍存在 / 出现次数对账 / 合计口径固定 15 / 变异负控 ×3）+ 真实文件变异 M7 全中（在 TasksView 里真加一处同形态的未登记 `void` 调用 ⇒ 变红并点名新条目；变异后 7/2，复原 9/0）。⚠ **登记表刻意不用行号做键**（§844 的 `z-index-ladder` 按行号登记被编辑推 42 行就红，已咬五次）⇒ 键 = `路径::callee` + 出现次数。⚠ 量具：探针与判据里**同一个 bug 修了两次** —— `bodyOf()` 用 `indexOf('{')` 找函数体，遇到形参带**含花括号类型注解**的函数会落在类型字面量上、立刻配平归零 ⇒ 把「自己接住了错误的函数」误判成「没接住」（16 → 15，少的正是 `postSystemNotification`）。这与 §845 的 `blockFrom`（TS 返回类型注解）**同族第 2 次**。⚠ 口径：**不声称**「全仓没有未处理 rejection」—— 跨文件导入的 callee（`void store.refresh()` 那 112 条里的一大部分）不在本判据范围内，那属 §847 的族；判据查的是文本形态，不知道 callee 是否真会 reject（§850） |
| §851 `v-html` 绑定审计：10 处里 **9 处已有净化，1 处是存储型 XSS** | 逐处追「表达式喂进去的 HTML 从哪来、有没有净化」：AIChatView / EmailDetailView / EmailSummaryView / NoteDetailView / AgentDetailView / RoundTimeline **全部已有净化链路**（`renderMarkdown` → `sanitizeHtml`，或 `sanitizeEmailHtml` 的 DOMPurify）—— **只有 `RssItemDetail:124` 的 `v-html="item.content"` 是裸的**。**① D6 ⛔ RSS 正文存储型 XSS（三段取证）**：内容**完全由订阅源作者控制**；**服务端原样入库**（`backend/internal/rss/store_entities.go` 把 `r.Content` 直接进 SQL 参数，整个 rss 包 grep 不到任何 sanitize）；**前端零净化**（`features/rss/` 全目录 grep `sanitiz|DOMPurify` **0 命中**）。⚠ 而 `frontend/index.html` **实测没有任何 CSP** ⇒ 没有任何东西拦 `onerror=` 这类内联事件 ⇒ 一条订阅里的 `<img src=x onerror=…>` 会在**设备 WebView** 里执行，而这个 WebView 带着 Capacitor bridge（读得到 localStorage 里的登录态）。⭐ **最强证据是一致性对照**：`utils/markdown.ts::sanitizeHtml` **已经存在**，配置明写 `FORBID_TAGS: ['style','script']`、`ALLOWED_ATTR` 不含 style，头注释还专门说 「without XSS risk」—— 聊天/笔记/会议摘要/Agent 提示词预览**全都走它**，**RSS 是 10 处里唯一没走的那个**。已落地：`safeContent = computed(() => sanitizeHtml(...))`，**没有新写净化器**，直接复用仓库既有的那一个。判据 **6 例**（含「每处 `v-html` 的**表达式形状**」清单 + 共享净化器禁 style/script 的抽查）+ 真实文件变异 M8 全中（变异后 4/2，复原 6/0）。⚠ 判据第一版按**文件**放行、判得太粗：表达式从 `safeContent` 改回 `item.content` 时它一声不响 ⇒ 清单改成钉表达式形状（同「判据的识别词是契约的一部分」）。⚠ **顺带登记第二套净化器口径相反**：`sanitizeEmailHtml` 允许 `style` 标签与属性，而共享 `PURIFY_CONFIG` 明确 FORBID style —— 两套配置各写一份、**没有任何测试让它们对齐**；且 `ALLOWED_URI_REGEXP` 只作用于 URI 属性、**不作用于 CSS** ⇒ 邮件里的 `@import url(...)` 不会被打到（阅读回执 / CSS 属性选择器外泄 / 点击劫持浮层）。⚠ 口径：只覆盖 `v-html`；`:style` 里的 CSS 注入、`<iframe srcdoc>`、以及下发到原生侧的 HTML 不在范围内。⛔ 未实测：**没有构造真实恶意订阅做端到端验证**，真机上 `innerHTML` 注入是否执行也未验（WebView 版本相关）—— 但**缺 CSP 这一点是确定的**，它单独就足以让净化成为必须（§851） |
| §852 URL sink 审计：26 处动态 sink **零校验**，5 处外部可控的接上协议白名单 | §851 抓渲染 sink（`v-html`），本节抓它的兄弟——**URL sink**：枚举 `:href`/`:src`/`window.open`/`location.href`/`location.assign` 里表达式为动态的 ⇒ **26 处**，**全仓没有任何一处校验协议**（前端 grep `startsWith('http` / `javascript:` **0 命中**）。⚠ 但本仓**已经用同一套思路**：`config/api-base.ts` 的 `normalizeApiBase` **拒绝** `javascript:alert(1)`（有测试）、`aiChatStore.ts:501` 把用户图片过滤成 `https://` 或 `data:image/` ⇒ 「RSS 的 URL 没过滤」是**不一致**，不是缺能力。**D7 ⛔ RSS 文章 URL（订阅源作者完全可控，服务端原样入库）直接进 href 与 window.open**：`RssItemDetail:130/:89/:175`、`RssDigestView:193`、`MeetingDetailView:608`（`onOpenRelated` 的 `item.url` 来自 `local_meetings`，源头同样是 RSS）。⚠ 影响面：`<a href="javascript:…">` 在 WebView 里**同源执行**；`intent:`/`market:` 这类自定义协议在 **Android WebView 里会被交给系统解析** ⇒ 一个恶意订阅不只是能骗用户点链接，还能把手伸到**应用之外**。⛔ **未实测**：`target="_blank"` 下 `javascript:` 是否仍执行**依 WebView 版本而定**，本节没在真机验证 ⇒ 修复按**最坏情况**处理（不放行），而不是按「大概不执行」。落地：共享 `utils/safe-url.ts` —— 放行 `https?://`、协议相对 `//host`、`mailto:`；**不放行** `javascript:`/`data:`/`file:`/`content:`/`vbscript:`/`intent:`/`market:`/`tel:`/`sms:`（**会交给系统处理的协议不是「打开一个网页」，是「让别的应用做事」**）；另：含控制符一律拒 ⇒ 防 `java\nscript:` 这类浏览器先剥控制符再解析协议的拼法。判据 **14 例**（纯函数 6 + 接线 4 + 未接线棘轮 + 变异负控 4）+ 真实文件变异 M9 全中（变异后 12/2，复原 14/0）。⚠ **未接线的 9 个文件已逐个读过并写进判据注释**：6 个阴性（本地 data: URL、`<img src>` 不执行 `javascript:`、AIChatView 在 store 里已过滤），3 个登记待拍板（OAuth 配置地址 / 分享地址 / 用户自填网址）。⚠ **棘轮上界我又拍脑袋了**（写 5、实测 9）⇒ 提交当天就红且报「新增未接线」，会把人引去查一个并不存在的回归——**这是 §849 之后第二次犯**。⚠ **我又一次踩了 §849 刚写进记忆的坑**：判据文件在 `src/utils/__tests__/` 下、`join(HERE,'..')` 少回退一层 ⇒ ENOENT **落在第一个 suite 上**，看着像「判据坏了」⇒ **教训写进记忆不等于写进代码，它必须写进那个文件本身**（§852） |
| §853 关掉 §851 自己登记的缺口：CSS 注入面 + 凭据面（**净得 0 处可修缺陷**） | §851.6 明确把「`:style` 绑定的 CSS 注入」记在范围之外，本节关掉它，并把凭据侧证据取齐。**① `:style` 绑定 35 处，逐处看过**：全部是**对象字面量**或**返回对象的 computed** ⇒ Vue 对对象式 `:style` 走 CSSOM 的**单属性赋值**（`el.style[prop]=v`），解析不出「借 `;` 注入别的属性」；**0 处 `cssText`**、**0 处 `setAttribute('style')`**（那才是会整体解析声明块的真正口子）；值**全部**来自本地计算或应用内配置，**没有一处**来自 RSS / 邮件 / LLM 输出。只有**字符串式** `:style` 会被按 `;` 拆开 —— 本仓一处都没有。**② 凭据面**：localStorage 全量清单里**敏感项只有 `pocket_token`（登录 JWT）**一个，它正是 §851 那条 XSS 的**收益**所在。⭐ **网关密钥不在前端**：`api/client.ts` 返回 `apiKeySet` + **掩码后**的 `apiKey`（`sk-****5678`），真值只在服务端 ⇒ 值得钉住的不变量。⚠ **仓库没有通用安全存储通道**：`native/keystore.ts` 是**保险箱专用**插件，头注释写明「目前是会抛的 stub，直到原生插件建出来」⇒ 「把 token 挪进 Keystore」是**架构口径**，不是缺接线 ⇒ 登记不代拍。**③ token 进 URL 是已记录的过渡态（阴性）**：`sse.ts` 注释原话「**生产应由 server 端做 JWT cookie 鉴权。当前 dev 模式 token 注入 query**」，`gateway-live.ts:6` 补充平台约束「EventSource 无法设置 Authorization 头」⇒ 两条理由注释都在；但代价真实存在（查询串进访问日志），替换要改服务端 `requireAuth` ⇒ 登记。console 侧：**0 处**打印 token。判据 **11 例** + 真实文件变异 M10 全中（在 VaultEntryView 里真加一处 `cssText` ⇒ 变红并点名文件行号；变异后 10/1，复原 11/0）。⚠ 判据自己写错两处（读码后改对并写进注释）：`/\?token=/` 匹配不到 SSE —— 源码里那个 `?` 藏在三元表达式 `` `${url.includes('?') ? '&' : '?'}token=` `` 内、**不存在**字面量 `?token=`；WebSocket 那边是 `url.searchParams.set('token', …)` 不是字符串拼接 ⇒ **正则锚点必须按真实写法选**，「看起来该长什么样」和「实际长什么样」要分开（§853） |
| §854 注册给 Vue 的异步回调审计：**42 条候选 → 6 类真缺陷**（用户主动动作失败零反馈） | 起点是一个朴素问法：**用户点了某个按钮，这件事失败了，用户看得见吗？** §847 扫「错误标志被生产却零出口」、§848 扫「会 throw 的函数 × 调用点接不接住」，本节扫它们之间的空白——**既没有错误标志、也没有显式 throw 处理，但 rejection 从注册给 Vue 的事件处理器里逃了出去**。扫面口径：「`.vue` 的 `<script setup>` 里 async function 体内含 `await`、体内无 `try`，且该函数**真的注册给了 Vue**」⇒ **42 条候选**，人工逐条读后 **6 条真缺陷 + 36 条站得住的阴性**（全部登记进判据 REGISTRY）。⚠ **自动初判只能当候选生成器**：探针把 `UnifiedComposer::onMic` 判成「有风险」，人工读才发现 `startRecording` 自己 catch 并设 `sttError`、模板 `:79/:184` 渲染了它 ⇒ 阴性（**与 §850 同族：安全的原因常在 callee 的 callee 里**）。**D8** `ScheduledTaskListView::toggle/remove` 的 `.catch(() => {})` —— store 的 `error` 只由 `load`/`loadOne` 写，`update`/`remove` **都不碰它** ⇒ 连那条横幅都不亮，而删除**已经过了确认弹窗**；**D8b** `ScheduledTaskDetailView::toggle/remove` 是**同一缺陷的第二个落点**（该视图零 toast），而同文件 `runNow` **有** catch ⇒ 作者知道要处理只是漏了；**D9** `NotificationsView::markAllRead` 三段取证全空 —— `stores/notification.ts` grep `error` **0 命中**、视图 grep `store.error` **0 命中**；**D10** `EmailInboxView:438/:525` + `EmailFolderListView:279` 是**三处漏网的裸 `window.confirm`**（项目已立 `ConfirmDialog` 为全局唯一确认弹窗，已改 5 处，BUG-AQ 记明真机点下去**应用彻底假死**）；**D11** `BacklinksPanel::reload` 的 `ready` 同时是「加载中」和「要不要渲染」的开关，而 `getBacklinks` 不吞错 ⇒ **一次失败面板永久消失**且与「加载中」不可区分；**D12** `VaultListView::saveNew` 缺 try/catch —— **同一个 vault 目录**下的 `VaultEntryView::onSave` 本来就有。⭐ 三处都是**一致性对照**形态：不是缺能力，是**同一个仓里已有正确做法、这里没有** ⇒ 修复动作全是**复用**。⚠ 刻意没把 D11 的失败复用成空态：那会让「查询失败」谎报成「没有笔记引用它」（§843 主动清空列表同族），判据里有一条专门钉住「失败态必须与空态可区分」。判据 **22 例**（尺子自证 3 / D8·D8b 7 / D9 2 / D10 2 / D11 3 / D12 2 / 反向对照 1 / 变异负控 3）+ **M11 六路真实文件端到端变异全中**（20–21 pass / 1–2 fail），⚠ **每一路的仓库既有门 `vue-template-compile` 都是 187/0 全绿、`typecheck` EXIT=0** ⇒ 六类缺陷对构建与既有门**完全隐形**。⚠ 判据自己写错两处：`fnBody` 的 `indexOf('{')` **落在注释里那个 `.catch(() => {})` 的花括号上**（⇒ 「锚点后第一个 `{` 不一定是函数体」第 3 次，前两次是 TS 返回类型注解、箭头函数），M11-e 的负控正则 `/\n\s*finally \{/` 匹配不到真实的 `  } finally {` ⇒ 两者都已改对并写进判据注释。⚠ **顺带撞到 `z-index-ladder` ALLOWLIST 第六次被行号漂移咬到**（改 EmailInboxView 推了 17 行，718/805 → 735/822）：先 `git stash` 单文件确认是本轮引入而非既有失败，再**只按既有先例重新编号、不改门禁语义**，并核对两条理由仍对得上 `.more-menu`/`.to-top`。⛔ 未实测：六类缺陷在真机上的**实际发生率**；D10 假死结论沿用既有 BUG-AQ 记录未重新复现；**`useConfirm` 的兜底路径（`useConfirm.ts:47`）在 ConfirmDialog 未挂载时仍回落 `window.confirm`**，可达性未知但一旦可达即等于 D10 未消除 ⇒ 登记不代拍。⚠ 口径：探针**不解析箭头函数 const 形式**（`const f = async () => {}`）⇒ 扫面结论是**下界**不是全集；本节只审计 `.vue` |
| §855 创建类动作的重复提交审计：⭐ **一条机制** + 3 处真缺陷（双击会真的做两次） | §854 扫「点了失败了看得见吗」，本节扫邻居「**点了一下做了两次**」。⭐ **本节的价值主要不在 3 处缺陷，而在一条机制**：「**模板 `:disabled` 能不能拦住第二次点击，取决于它的条件在 handler 的第一个 `await` 之前是否同步变真**」—— Vue 的 DOM 更新在 **nextTick（微任务）**里，而两个独立 click 之间微任务队列**必然排空**。⇒ **「模板里写了 disabled」不等于有保护**，这正是本节两处真缺陷的共同形态。**两个方向都实测过**：`CalendarEventSheet::save` 的 `:disabled` 绑 `store.saving`，而 `store.createEvent` **第一行**就是 `saving.value = true` ⇒ 拦得住；`TasksView::handleCreate` 原来绑 `!newTask.title`，而 `newTask.title` 要 **await 之后**才清空 ⇒ **整个网络往返窗口都可点**。**D13** `TasksView::handleCreate` 创建两条任务（id 是本地 `task-${Date.now()}`），catch 还只有 `console.error`；**D14** `FinanceView::quickConfirm` 入账两条**真实财务流水**（确认按钮**连 `:disabled` 都没有**）；**D15** `FinanceView::remove` 双击时第二次对**已删 id** 发请求 ⇒ 404 ⇒ `toast.error('操作失败')` 而**那条流水确实消失了** ⇒ **界面说谎**。⭐ **三处全是一致性对照**：`NoteEditView`/`NoteDetailView`/三个 `submitCreateDeck` **4 处同类都有重入守卫**；D14 的对照在**同一个文件**里（`:43` 的「记账」按钮有 `quickBusy`，`:49` 的「确认入账」没有）；D15 的 `removingId` 用 **id 不用布尔**（财务列表多行可见，布尔会把整列禁掉）。扫面 48 → **收窄创建类 14** → 人工逐条读 **3 真 + 11 阴性**；⚠ 收窄理由已写进文档（`delete`/`remove` 第二次是 noop，后果轻得多）。判据 **18 例**，核心是**机制反向对照**三例（「置位在 await 前」算保护 / **「disabled 要等 await 后才变」必须判成缺口** / 「没 finally 复位」也算缺口）。**M12 五路端到端变异全中**，⚠ **每路的仓库既有门 `vue-template-compile` 都是 187/0 全绿** ⇒ 对构建与既有门完全隐形。⚠ 探针第一版有个**量具 bug**：守卫判定用**手写名字词表**，漏了 `deckCreating` ⇒ 两个明明有守卫的 `submitCreateDeck` 被报成候选（假阴）⇒ **名字词表穷举不完**，「有守卫」必须按**结构**（置 true + finally 复位）判定。⚠ **换方向的记录（阴性也是有价值的产出）**：开扫前试了三个方向全部阴性 —— `JSON.parse` 防护 **54 处逐条读后全阴**（本仓强项，连 LLM 输出解析都做了「合法但空」的额外判定）、定时器/监听器清理（`setInterval`/`addEventListener` 清理 **0 候选**）、「成功提示早于写入」**2** 处（其中 1 处命中的是**我自己写的注释**，探针自伤第 8 次）；三次探针各坏过一次，全部当场发现（`JSON.parse` 那版「59 处只有 1 处有 try」这个读数本身就不可能 ⇒ **0/1 命中的第一嫌疑永远是量具坏了**）。⛔ 未实测：真机上触摸重复上报的真实概率（本节结论建立在可推理的微任务机制上）；`api.createTask` 是否服务端幂等（若 upsert 则 D13 第二次可能被吸收）⇒ **按最坏情况处理**。⚠ 口径：探针只解析 `.vue`、创建类动词表是穷举的（14 个词）、宽口径另 34 条非创建类动作**未逐条取证** |
| §856 审批链路的静默丢弃与失败零反馈：2 处真缺陷 + ⭐ **一条口径盲区**的发现 | 从**用户输入**这个维度切入（§854 扫「动作失败看不看得见」、§855 扫「会不会做两次」，本节扫「**用户写的东西有没有被悄悄改掉**」）。落点窄但后果最重：`ApprovalPanel` —— **human-in-the-loop 审批面板**。**D16** `onApprove`/`onDeny` 提交前对 >500 字的附言做 `slice(0, 500)`，而输入框**没有 maxlength、没有字数提示、没有任何提示** ⇒ ⛔ **三段取证**：① 后端**根本没有这个限制** —— `PermissionReplyRequest{Reply, Message string}`（`opencode_http.go:722`）无任何长度校验，直接 `json.Marshal` 透传上游（`:819`）⇒ **那个 500 是前端凭空加的**；② 前端也不告知；③ 截断发生在**点「批准」的那一刻**（不是输入时）⇒ 用户无法察觉。⚠ 后果比「字少了」严重：这是 human-in-the-loop 的**最后一道闸门**，用户附言里写的通常是**追加的限制条件**（「可以读，但绝对不要改 package.json」）⇒ 丢掉后半段 ⇒ **Agent 收到不完整的指令，可能做出用户明确禁止的事**，而用户以为限制已传达。修复**只消除「静默」不代拍上限**：不改动用户输入，超限用 `toast.warning` 明确告知；⚠ **刻意不加 `maxlength`**（那等于把「凭空加的限制」变成硬限制），判据里有一条专门钉「不得出现 maxlength」并注明若将来决定要有上限请同步改。**D17** 批准/拒绝/回答/跳过**四处同形**的 `try {…} finally {…}` —— `store.error` **只由 `fetchPending` 写**而四个动作都不设 ⇒ 横幅不亮（文案还写着「审批状态拉取失败」）+ rejection 逃逸 + **用户点「批准」后界面什么都不发生**；修复用**独立的 `actionError`**（刻意不复用 `store.error`：复用等于「批准失败但横幅写着拉取失败」）且放在 `v-if="store.hasPending"` **之外**（批准成功后该项消失 ⇒ `hasPending` 转 false ⇒ 放那个分支就等于「成功了才显示失败」）。⭐⭐⭐ **本节最重要的发现是 §854 的口径盲区**：§854 的口径是「体内**无 try**」，而 `onApprove` 写的是「**有 try 但没有 catch**」⇒ 被完美排除在外；而「**有 finally 却没 catch**」恰是最隐蔽的一类 —— **`finally` 的存在本身证明作者意识到「状态要复位」，只是没意识到「失败要说出来」，这两件事看起来像同一件其实不是** ⇒ §856 立了新口径。⚠ **换方向记录（试 5 个方向、4 个阴性）**：`:key` 用 index **24 处逐条读后全阴**（全是纯静态展示；**`:key="i"` 本身不是缺陷**，Vue 会为属性绑定显式重绑）；`TransitionGroup` key **3 处全稳定**；`v-for` 子元素是组件 10 处（唯一命中是 `<mark>` 不是组件 ⇒ **探针假阳**）；`Array.sort` 比较函数 **43 处全正确**；时区混用 **2 处全阴**且是本仓最扎实的地方（`calendar-math.ts:7` 注释明写已警惕该坑，全文用一贯的 civil date 口径）；**模板绑定未声明变量**探针报 436（几乎全假阳）⇒ ⛔ **改用实验证明**：注入一个不存在的变量名，**`vue-tsc` 精确报红并给出 `Did you mean 'confirming'?`** ⇒ **这一整族已被 CI 第 1 项完全堵死，零可修缺陷** —— 这比「我扫了没找到」有价值得多。判据 **14 例**；**M13 四路端到端变异全中**且**每路的仓库既有门都是 187/0 全绿**。⚠ **M13-a 第一版是 14/0 全绿 —— 判据假阴**：正则写成 `permMessage\[\w+\]` **匹配不到 `permMessage[p.id]`**（`[]` 里带点号）⇒ 已改并在注释里写明 ⭐ 这正是「判据与变异共用同一把尺子」的价值。⚠ 本节两个坑：`indexOf('{')` 落在**形参类型注解** `{ id: string }` 上（**同一坑第 3 次**：§845、§850、本节）⇒ `fnBody` 改为先 strip 再做**圆括号配平**；**注释里引用代码字面量被当真代码（本会话第 9 次）** —— 我在 catch 注释里写了 `v-if="store.error"`，§847 既有判据的变异 A 做 `replace('v-if="store.error"', 'v-else')` ⇒ **命中的是我的注释**，真横幅没被改 ⇒ 按纪律**只改自己措辞**、不动门禁语义。⛔ 未实测：D17 四动作的真实失败率；**OpenCode 上游是否有限制**（本节只证明本仓后端没有） |
| §857 把「有 finally 无 catch」口径**全仓**跑一遍：9 处 → 3 处真缺陷 | §856 结尾留下「新口径只对审批链路断言」，本节做那件**不该等拍板**的事：**扫描取证本身是技术动作**（拍板的只是「要不要做成门禁」）。口径 = 被注册给 Vue 的 async 函数**含 `finally`、不含 `catch`、含 `await`** ⇒ 186 个 `.vue` 里 **9 处**，逐条读后 **3 真 + 6 阴性**（全部登记）。**D19** ⭐⭐ `MeetingListView::startNewMeeting` 是 **§838 的同族漏网**：§838 的注释原文写「这五个处理器此前都没有 try/catch」，而**同文件紧接着的** `onArchive`/`onRestore`/`onDelete` 都带着 §838 的修法（`catch + console.error(RECORD_ACTION_FAIL_TAG) + toast.error(describeRecordActionFailure(…)`），**唯独它只有 `finally`** —— 因为它本来就有 `try/finally`，看起来像「已处理」。后果：用户点「新建会议」（**本应用最核心的入口**，录音→会议）后 `createMeeting` 的 `localDB.run` 失败 ⇒ 不跳转、零反馈、像应用卡死。⚠ 修的时候发现**共享文案工具的措辞会撒谎**：`RecordAction` 原本五条全是「**记录仍在列表里**」的口径，而新建失败时列表里**根本没有**那条记录 ⇒ 新增专属 `case 'start'`（判据里有一条专门钉「文案不得出现『仍在/还在』」）。**D18** `VoiceprintSheet::onClearAll`：`clearVoiceprints` → `localDB.run(DELETE_ALL)` 不吞错，而用户**已经过了「此操作不可撤销」的确认弹窗**却什么都没发生、列表纹丝不动 ⇒ 会以为删掉了却不敢重试或反复点；修复与同文件 `load()` 的 §822 修法**同构**，且提示放在模板互斥链**之外**（塞进 `ul v-else` 的话，下一次加载失败落到 `loadError` 分支时提示会凭空消失）。**D20** `SettingsPermissionsView::refreshAll`：五个 `recheck()` 都不吞错 + `Promise.all` ⇒ 整批逃逸，刷新按钮转一圈结束、权限状态停在旧值、零反馈。⚠ **顺带发现 §850 的登记表把 `refreshAll` 登记成阴性、理由「各子项自带 error 出口」—— 而那条依据本身就是错的**（`checkPermission` 并不吞错）⇒ 它当年「安全地」存在是因为**没人验证那条理由**。判据 **19 例**；**M14 五路端到端变异全中**，⭐ **M14-b（把提示藏进互斥链）是唯一一路被仓库既有门 `vue-template-compile` 抓到的（186/1）**，其余四路对既有门完全隐形。⚠ 本节三个坑：① ⭐⭐ **变异脚本必须保持源码语法合法** —— 判据文件里第一版的 D18 变异多删一个右花括号 ⇒ `fnBody` 配平**提前闭合**、判据取到残缺体 ⇒ 看起来像「判据无牙」其实**是变异自己写坏了源码**（搜索串含前导 `}`、替换串就**不能**含）；② ⭐ **模板注释里的 `v-if="…"` 字面量抢 `indexOf` 锚点**（自伤第 10 次）⇒ 修法是判据侧 strip HTML 注释（§841 已建过 `tplCode()`），不改我的注释；③ ⭐ **§850 的反向对账立刻抓到了我的改动** —— 给 `refreshAll` 加 catch 后它不再被 `scan()` 命中 ⇒ 登记条目成陈旧 ⇒ 断言转红 ⇒ 这是双向对账**最有价值的一次实证**：它把「我改了某个函数」变成一条会红的断言，不需要人记得回头改表。六处阴性里两处判为**产品口径而非缺陷**（定位失败无提示、AI 降级不告知），已单列待拍板。⛔ 未实测：三处的真实失败率；探针只解析 `.vue`、不解析箭头函数 const 形式 ⇒ 读数是**下界** |
| §858 「读失败」被谎报成「从来没有过」：1 处数据面缺陷 + ⭐⭐ **实测推翻我判断**的记录 | §857 留下一个观察：那 9 处候选里 6 处阴性**全靠「callee 把失败编码成空值」救的** ⇒ 「视图写法」这个视角已被扫干净 ⇒ 本节换成**「callee 返回值」**这个没被扫过的视角。探针 `.artifacts/s858-probe/scan_nullish_failure.py` 枚举「`catch` 里 `return null/[]/''/{}`」的生产者 ⇒ **69 处**。**D21** `aiChatStore::loadConversations` 原文 `catch { return null }` ⇒ **读失败与「从来没有过对话」是同一个值**，而 `init()` 落进 `else createConversation()`、末尾 `persist()` 用新建的空对话 `setItem` **同一个 KEY** ⇒ 用户看到「开始你的 AI 对话」，以为从来没有过。⚠ 触发条件并不罕见（配额超限后的部分写入、多标签页并发写、跨版本迁移写坏、浏览器异常退出留下半截）。⭐⭐ **本节最重要的产出是我动手前的判断被实测推翻了**：我写下「一次读失败 = 全部历史**永久覆盖**（不可逆）」，用 **esbuild 把 store 打成 bundle 后在 node 里真跑 `init()`**（含**合法输入的基线对照**与**修复前的负控**）⇒ 读数是「原 KEY 被覆盖」**在修复前后读数相同** ⇒ **数据不是在 `init()` 那一刻丢的**（存储里本来就已经坏了）⇒ 实际后果只有两条：**谎报** + **销毁唯一排查证据** ⇒ 定级必须更正。⭐⭐ 而且**修复本身一度引入了新的界面说谎**：我第一版文案写「原始数据已另存（未被覆盖），**可在 `pocket:ai-chat:corrupt:*` 键下找回**」—— 实测证明**留证里存的就是那串损坏内容、找不回**可解析的历史 ⇒ 文案已改成「应用**无法自动恢复**它 / 已另存**供排查** / 若损坏只发生在末尾，其中可能仍有可手工抢救的片段」。⇒ **这与 §856 修的是同一族：说谎的往往不是「原来的代码」，而是「修复时顺手写的那句话」**。落地只动两处：`aiChatStore.ts` 加 `preserveCorrupt(kind, raw)` + `safeGetItem()`，两个 catch 改为**先留证再置位**、并导出 `conversationsRecovered`/`settingsRecovered`；`AIChatView.vue` 模板顶部加 `recovered-notice`（**放在所有空态分支之外**，理由同 §857 D18）。⚠ **刻意保留** `init()` 的 `else createConversation()`（改掉它界面全空、连输入框都没有）⇒ 判据里有一条专门钉住它，防下一个人「顺手优化」掉。⚠ **复现脚本第一版读数全是错的**：把数据种在 `pocket:ai-chat:v2`，而真实 key 是 `${prefix}:${storageScope()}`、无 workspace/user 时末尾有 `:local` ⇒ store 读到 `null`、**基线读数就不对（2 条历史读成 1 条）** ⇒ 教训：**复现脚本的 key 必须从源码读**，而且这个错误恰好被「基线那一例」抓出来。判据 **11 例**；**M15 三路真实文件端到端变异全中**（9/2、9/2、10/1），而**每路的仓库既有门 `vue-template-compile` 都是 187/0 全绿** ⇒ 对构建与既有门完全隐形。⚠ 附带**否证**一条我自己的假设：「模态内 toast 会被遮住」—— 查 `tokens.css`：`--z-toast: 9999` + `Teleport to="body"` > `--z-sheet: 1300` / `--z-dialog: 1400` ⇒ toast 层级高于所有模态。⛔ 未实测：真机上 `pocket:ai-chat:v2` 损坏的**发生率与形态分布**；「配额超限导致部分写入」是**推断未复现**；本节只扫「`catch` 里返回空值」一族（`?? []`/`|| {}`/默认参数不在读数内）；69 处其余 66 处**未逐条取证** |
| §859 §858 那 69 处候选的逐条分诊：3 处真缺陷 + ⭐ 一条「同族已修、同族漏网」的定罪证据 | §858 结尾留了一笔账「69 处其余 66 处未逐条取证」，本节把它结掉：**3 处真缺陷 + 10 组阴性（每条带读数与理由）+ 1 条量具边界**。起点仍是 §858 的视角切换，但**先把量具修对**：新写 `.artifacts/s859-probe/triage_nullish_producers.py`（正确的函数名 + 反查消费点 + 返回形态分类）⇒ ⚠ 第一版坏了两处且都被读数当场抓住：`ret` 分类全 `?`（`NULLISH` 后加了 `\b`，而 `''`/`[]`/`{}` 以非单词字符结尾）、函数名全是 `if`/`for`（`JsonBlock.vue` 被标成 `[if]`、消费点报 **4214** 个）。修正后读数：**652 文件 / 70 处**（`null` 30、`false` 20、`[]` 9、`''` 7、`{}` 2、`undefined` 2；70 比 69 多的一处是**本节自己新增的**共用留证函数）—— **D22** ⭐⭐ `apply.ts::applyAppPrefs` 把**远端下发**的 `theme`/`deviceTier`/`sttPref` 三个枚举**未校验就落盘**（`theme` 还写进 `<html data-theme>`）；**定罪证据不是我的判断，是同一个函数里的先例** —— `applyRemoteLocale` 的注释原文写着「未规范化的字符串被直接写进 app_locale……现在：只在能匹配到受支持语言时才落盘」，**locale 已因完全相同的原因修过，同函数里的三个字段留在坏的那一边**；读侧对照同样是本仓自带：`theme.ts`/`locale.ts` 都做白名单，而 `stores/device.ts` 只 `as` 断言（`as` 只对编译器说话，且两个 getter 都以「不等于 low」为判据 ⇒ 非法值**静默退化成高档行为**）；⚠ 加重情节：`useDeviceStore` **全仓零消费零入口**（自己的定义文件外 0 引用，无 `.vue`/无测试/无门脚本，也无「遍历所有 store」的机制）却仍在被 `app_prefs` 跨设备同步，且 `recommendedModel` 的 `'vosk-small'` **不在** `sherpa.ts` 的 engine 联合里。**D23** `ScheduledTaskEditView::save` 对**同一个** `parsePayloadText()` 两种待遇：提示词分支 `?? {}` 静默吞掉用户参数、非提示词分支会报错；而 JSON 文本框是 `v-if="!showPrompt"` 且在 `<details>` 里 ⇒ 用户填的参数消失时**他连表单里还留着都看不见**，任务照样保存成功，要等定时任务到点自己跑失败才发现；⚠ 直接改会**引入新阻断**（`formatPayload` 对字符串 payload 原样返回 ⇒ 文本框里是无法解析的裸串 ⇒ 新校验会卡住「编辑一个本来就存在的任务」）⇒ 一并把 `formatPayload` 改成始终产出可解析文本。**D24** `applyChatSettings` 一行内联 `JSON.parse` 的异常落到 `catch { /* ignore */ }` ⇒ **整份远端聊天设置被静默丢弃**，而这个 key（`pocket:ai-chat:settings:v2:<scope>`）**正是 §858/D21 刚硬化过读侧的那一个** ⇒ §858 补了读侧却漏了写侧；修法是「留证 + 以 `{}` 为基底继续应用」（远端才是这次的数据源），并**把留证落点抽成共用函数** `utils/corrupt-preserve.ts`（同一命名空间里两份键名实现必然漂移；键名与落点不变，抽完 §858 判据 11 例原样重跑）。十组阴性逐条登记，其中 ⭐ `navigationContext::restore` 是**这一族的标准样板**（`validateContext` 做 schema 校验、失败不写回、注释写着「半恢复的导航比不恢复更危险」），⚠ `snapshot-store::loadNS` 的阴性**带条件**：现有两个 namespace 写的全是 `dirty:false` 纯缓存，将来若有 `dirty:true` 走这条路结论立刻失效。⭐ 一条**量具边界**：`check:dead-features` 只扫 `features/meetings/`，且 `audit-dead-features.mjs` 的 `--dir` 被硬编码拼成 `src/features/<dir>`（`--dir stores` 直接 `ENOENT`）⇒ `stores/`/`utils/` 完全在死代码审计视野之外，这正是 `useDeviceStore` 能在全绿门禁下长期存在的原因（本节未代改门禁范围）。判据 **14 例**（D22 三条 / D23 四条 / D24 四条 + 尺子自证 + 两条变异负控），核心是**钉「写进去的值」而不是「有没有 setItem」**——校验了却把原始字段写回去是这一族最典型的假修；**M16 四路真实文件端到端变异全中**（2/2/3/1），而每路的 §858 判据（11/0）与仓库既有门 `vue-template-compile`（187/0）**全部隐形**。⚠ 本节三个坑全是「量具自己坏了」这一族：① `fnBody` 取不到泛型函数体（`function readEnum<T>(` 名字后面是 `<` 不是 `(`）⇒ 在**空串**上断言；② `/catch\s*\{([^}]*)\}/` 只取到内层 catch 的第一层 ⇒ 「留证的 catch 里不许 return」永远看不到真块体；③ ⭐⭐ **变异脚本的还原自检用了 grep `prefs.deviceTier)`，而我自己写在说明文字里的正是这一句** ⇒ 自检永远报噪声（M16-a 报「还原失败」），而 M16-a 真正删掉的那一行**没被还原**、留下引用未定义变量的半变异状态 —— ⭐ **是判据基线把它抓出来的**（基线红 2 条）⇒ **还原的权威证据是判据基线全绿，不是 grep 看着像还原了**。⛔ 未实测：真机上远端下发非法枚举值的发生率、定时任务该操作路径的发生率、写侧解析失败发生率；「跨边界枚举未校验」这条口径**尚未全仓扫面**（需先枚举跨端输入 → 本地存储/状态的边界清单）
| §860 把「跨边界枚举必须校验」推到全仓：**净得 0 处新增缺陷** + 一份正例清单 | §859.7 自己留的账。本节结论是阴性，但阴性不等于「扫了没找到」就完事 —— 它同时产出了一份**可复用的正例清单**。⚠ 先说探针失败：第一版把 `load` 也当成「native 桥来源」的名字 ⇒ 读数 **388 处候选、其中 359 处来自 `load(`** ⇒ 信噪比不可用（**分类探针比枚举探针更危险**：通用名字当边界来源等于把半个仓标成候选）⇒ 改成手工枚举边界入口。本仓真实入口只有四个：**config-sync**（❌ D22/D24 已修，§859）、**WebSocket 推送**（✅ 已校验）、**用户导入 ENEX**（✅ 已校验）、**能力探测**（⚠ 已刻意，但文案断言了未知原因 ⇒ 并入第 79 条）。三个正确形态的样板：① `ws-bus` 的 `normalizeTags` 逐项 `typeof` 过滤 + **递归深度上限 10**，`normalizeServerNote` 逐字段 `??` 兜底；② `evernote-parser` 的 `toArray` 形状归一 + `parseDate` **失败收敛成 null 而不是 NaN**（NaN 不等于任何合法时刻，会永久留在库里）；③ `navigationContext::restore` 的 `validateContext` + 「schema 不符就整个丢弃，不做尽力修复」+ **失败路径上没有任何 setItem**。⚠ 一处值得记的**同函数内对照**：`ws-bus` 里 `tags` 逐项做了类型过滤而 `content_type` 只做了 `?? 'text'` 兜底，仍判阴性 —— 因为 `LocalNote.contentType` 的类型就是 `string` 且**下游没有按它分支** ⇒ 判定依据是「类型声明 + 消费端」，不是「有没有校验」。⭐ 真正的产出是**把三个样板钉成防退化判据**（10 例）：`.artifacts` 里的探针会烂，但样板会被下一个人「顺手简化」掉 —— 例如有人把 `filter(typeof tag === 'string')` 改成 `as string[]`，这类改动在 45 项门禁下完全隐形；**M17 真实文件变异两路全中（8/2、8/2），既有门 187/0 全绿，还原后 diff 为空**。⛔ 未实测：三条通道各自脏输入的**真实发生率**（结论建立在「代码形态正确」上，不声称脏输入不会来）；边界清单是**手工枚举**的，将来新增一类跨端输入时**不会自动更新**。⭐ 本节唯一的建议：这条口径**不适合做「全仓发现型」门禁**（净得 0），要做门禁应该做的是上面那份**正例清单**（10 例）（§860.6） |

### 🔴 我主动作废的一个方案（别再照着它做）

我曾把 `minimax-asr-1.0` / `glm-asr` 按 gateway 组**逐档钉进 GOLDEN 价表**（在册 9→11、门 7P/1F→8/8）。并发会话的 **§787 对同一问题给了更优解**：external 组**全部在册**、gateway 组**刻意不逐档钉**，改用「**不得击穿基线最低价 $0.012/h**」的不变量兜底。⇒ **我的逐档方案主动作废，不重复落地**（否则就是两处口径打架）。

### 📊 新取得的读数（真实素材，12.5 分钟口述音频）

- **§796 CER**：去标点全字符 **12.23%** / 纯汉字 **11.17%**；⚠ 参照物是**另一套 ASR 的产物**（带说话人分离时间戳 + 英文幻觉 + `以上内容由AI生成` 尾注 + WeChat 送达），故这是**跨系统分歧**，**不是人机字错率**
- **§797 #58 真实发生率**：用**生产 `VadSegmenter` 本体**（只替身 Web Audio / MediaRecorder / 时钟，替身吐真实 PCM）切真实音频 → 40 段 / 携带率 **99.83%** / **39 对相邻段 0 对共享时间** / 去重改写 **0/39**（95% 上界 ≈7.7%）
- **§799 判定翻转**：生产形态整段（`SplitWAV` 38 段、`strings.Join` **跨段零合并**）→ `adopt: **true**`（3348 vs 3298），护栏**两侧行为都在真实素材上验过**
- **§801 成本侧**：采纳整段后结构按 §800 保住（40 段），**提示词只差 43 个字（+0.9%）**，而它要多付 **38 次计费调用**

### ⛔ 今天两次自我推翻（都留在文档里，别只看新结论）

- **§798**：「护栏照样有牙」用了**比生产更差的基准**（整段 752s 喂短音频模型）⇒ 生产形态下判定翻转
- **§800**：「塌成 1 段、丢说话人」的真因是**没跑 diarization**（`meeting-final-transcript.ts:285` 的前置）⇒ 有标签时 40/40 段与 A/B 全保住

### ✅ 关掉的一个旧悬念（§816，不需要你拍板）

主文档 §810.2 曾留「15s 档双峰，提示存在阈值效应（**待查**）」。**§816 已查到底**：三个假设各被一轮实测**证伪**（VAD 分段数、页内首次录音、长度），真正的量是**尾亏的二值抖动**（2 / 36 / 56ms）。用**音频内时间戳标记**（440→880Hz + Goertzel 双频定位）测得**头亏 ≤ ~10ms 且不随时长增长**（10s 档 4/4/4/4ms，60s 档 4 / −2ms）⇒ 主文档 §811.3「头部丢失上界 ≤2.25s」在桌面路径上**不可能**出现（差两个数量级以上），§811 那个数仍需**真机**解释。

⇒ **对你第 5 项的影响：没有。** 健康漂移最大值 67→69ms，仍比 500ms 容差低 **7.2×**，`max(500ms, 2%)` 不会误报这条结论**不变且被加强**（样本从 20 个增到 35 个）。

⚠ 本轮**两把量具被自己的自检抓出来**，结论里都留着：① 「分段数」那把连续方波下 `segCount` 恒为 1 ⇒ 自变量没动，只能写「测不出」；② 「头尾分解」那把的**和**是恒等式（`p` 被消掉）⇒ 对账差再小也是零信息量。**判别法：分解的和注定恒等，要看的只能是各项，且各项必须配一条非恒等自检。**

⭐ **§817（接着 §816）攻了 §816.9 自陈的边界缺口，结论存活、并更正了一句话**：把标记推到**距头/距尾各 150ms**（150/1000/9000/9850ms）后，头亏仍是 `[−2, 4]`ms、极差 6ms ⇒ 贴边不改变结论（头亏合并 **14 个样本**，标记跨 150ms–30s、录音跨 10s–60s）。⚠ 但要按 §817.2 的口径说：**这些读数与量具自身系统误差同量级** ⇒ 只能说「头亏 ≤ ~10ms、在量具极限内」，**不是**「头亏 = 3ms」，也**不是**「没有头亏」。

⛔ **一处自我更正（§817.3）**：我上条写的「尾亏是**二值抖动**、同长度下逐轮不同」**不准确** —— 10s 档合并 **12 个样本全落 36–42ms**（极差 6ms）⇒ 固定长度下尾亏**高度可复现**；「逐轮不同」只在 24s（63/3）与 60s（56/2）出现过，各仅 2 样本。⇒ 这也**削弱**了 §810.2「双峰」的原始解释力（它当年是 15s 档 5 个样本，而 15s 档复测是 3/3ms）⇒ 更可能是小样本噪声，**不是**稳定的长度阈值。**对你的第 5 项依然没有影响**（护栏余量不变，漂移仍 42–45ms vs 500ms 容差）。

### 🔴 仍等你（我不能代拍）

1. **有效的网关 key**：上次那个被判 `invalid_key / Invalid or expired API key`（远端与本地 `:8782` 都是）⇒ §795 的 HTTP→LLM→DB leg 未验收，**该节不构成任何「LLM 链路已通」结论**
2. **人工逐字稿**（或口述 2–3 分钟我转写）：把 12.23% 的跨系统分歧收敛成真正的 CER
3. **两个产品口径**：① 采纳整段是否加「须显著更完整」条件（§799.3.3 + §801 的成本账；现状是「整段 ≥ 分段一半」对任何静音边界正常切出的整段文本都近乎必然通过）② 是否为 **26 个「活着但零测试引用」的导出**补回归保护（主文档 §802.5 已把这 28 个定性完：**26 个已接线未测试 · 2 个早已在 `dead-features-baseline.json` 在册 · 0 个是漏登记的死代码**；且跨域动作那批的「正确行为」本身涉及产品口径，口径定下来前写测试等于把猜测固化）
4. ~~push~~ —— 已按属主「请按建议继续」在隔离 worktree 上完成合并重整后推送
5. ~~是否落地「收尾解码核对时长」护栏~~ —— **已落地（§805）**：两个纯函数 + 一层可测胶水（`recording-duration-consistency.ts`），19 用例、四个变异全被抓住，只 `console.warn` 不阻断。⇒ **现在只剩「要不要把 warn 升级为阻断 / 要不要改阈值」**（§805.3：相对份额容差 ⇒ 20 分钟丢 4s 判 ok 不报，是显式取舍并由测试钉住）。原口径保留如下：§785 那 4.64 秒缺口**运行时无从察觉**——产品里每个时长数字都是 `Date.now()` 的差，产物真实长度从未被测量。⚠ **成本不是要害**（§804 已更正）：那个整段解码**在 ASR 主链路上本来就在无条件发生**（`wav-encode.ts:28`），真实时长算出来就被丢弃；甚至光看已产出的 WAV 字节就能反推时长（`(bytes-44)/2/16000`）。⇒ 这条现在**不是性能问题**，只是「要不要把已经算出来的时长顺手与墙钟比对」。✅ 那个残留的真洞**已关闭（§818）**：转码静默失败时**不只是时长丢失，而是整条失败链在生产里没有任何出口** —— 根因被 `meeting-final-transcript.ts` 的空 catch 吞掉 ⇒ 退回原 webm ⇒ 网关拒收 ⇒ `reason=empty-transcript` （只剩下游症状）⇒ 而唯一生产调用方 `finalizeRecording` **一个 `reason` 字段都不读** ⇒ `reason` 是死输出；时长护栏在这条路上也判 ok。已修：`transcodeFailed`/`transcodeError` 留痕 + 决策落进纯函数 `finalizeFailureNotice` + 调用点 warn（降级行为**完全不变**，只增可见性）。变异四个全中。⇒ 现在这条上**只剩一个待你拍板项**：触达面要不要从开发者 console 升到用户可见。⚠ **§819 又补了 4 条路的同款信号** ⇒ 现在这条栏里一共有 **3 处**待升档的 warn（时长漂移 / 转码失败×会议路 / 转码失败×增量路），**可以合并成一个决定**：要不要升、升哪几处。⚠ **§820 建议单独拍**：它是**数据丢失**不是质量信号（音频永久没了、且用户以为还在），与上面三处「质量告警」不同类。我的建议：**这一处应该升为用户可见**（落盘失败时明确告诉用户「录音未能保存，请勿刷新」），另三处则仍建议先都不升（健康漂移均值 21ms vs 容差 500ms 有 7.5× 余量，实测上不会误报）。⛔ 但它的**真实发生率未实测**（需真机长录音观察配额），所以标成「建议」而非结论，等你定。，而转码失败属于「本来就有用户可见的报错」的路径，升档的收益要看真机上它的发生率 —— ⛔ 该发生率**未实测**。⚠ 另附一条给下一个人省时间的陷阱：**「拿最后分段 `endMs` 对照 `elapsedMs`」这条最自然的护栏是恒等式、不成立**，两者同出 `Date.now() - startTime`。
6. **随手记补救入口（§832 + §833 同类，本轮合并提出）**：回填失败时完整正文就在 `pendingResult.text` 里，建草稿失败时正文也在界面上 —— 要不要各给一个**一键补救按钮**（「把当前正文补进草稿」/「保存为纯文字」）？⚠ 当前两处都只让用户手动操作，不是一键
7. **随手记错误通道要不要统一（§833.6 第 2 条）**：`this.error` 的语义已从「转写错误」扩到「转写 / 落库收尾错误」，与 §832 的 `draftSaveError` 并存（两处各管一段，好处是上下文不同文案不同，坏处是两套 ref）
8. **遗留录音的续录入口（§834.7 第 1 条）**：现在改成「提示 + 让用户自己点麦克风」（麦克风点一下会 `seedSegments` 续录，语义等价但用户看不出「续录」两个字）—— 要不要给一个**显式按钮**
9. **写库失败要不要自动重试一次（§834.7 第 2 条）**：本轮**未做**，盲重试可能掩盖真实故障
10. **元信息面板失败时保持打开**之后要不要「复制我填的内容」兜底（§835.6 第 1 条）
11. **说话人改名续录历史段**（§276.6）要不要一并补数据结构 —— 给分段加 `profileId`，属存储形态变更
12. **`MeetingMetaSheet.vue` 零引用组件**：删除还是接线保留（§835.6 第 3 条）
13. **「只存了一半」要不要给「重建会议全文」一键修复**（数据都在，只是一次列没同步，§836.6 第 1 条）
14. **字幕入库失败要不要从 sttError 升为 toast**（当前不升，理由见 §836.3）
15. **`transcript` 列与分段表的一致性要不要做常态守卫**（而不是只在失败时提示，§836.6 第 3 条）
16. **详情页「本地库未就绪」要不要直接复用 DbLockedState**（列表页那个，带重新登录动作）而不是本页自绘的 EmptyState（§837.6 第 1 条）
17. **标题富化失败要不要给极轻提示**（当前刻意不提示，标题留空由用户自己填，§837.6 第 2 条）
18. **媒体残留要不要给「清理残留媒体」入口**（§838.7 第 1 条，本轮只报诊断 + 文案告知）
19. **笔记侧补「已删除但媒体未清理」文案**（§838.7 第 2 条，当前只有诊断日志）
20. **「禁止吞掉失败的 catch」要不要升格成仓级规则**（§838.7 第 3 条，当前只覆盖本轮经手的五个文件）
21. **启动失败的残留记录要不要自动兜底**（延迟重试一次清理；§839.7 第 1 条，本轮只让用户手动删）
22. **「读不回分段」要不要排一个稍后自动补做收尾的队列**（§839.7 第 2 条，当前是彻底不做）
23. **`toggle()` 要不要也按「事件处理器边界必须自己说真话」兜一层**（§839.7 第 3 条）
24. **控制器要不要开一条错误通道**（`onError` + `lastError`），让所有走 `useContinuousList` 的视图不必各自在 `fetchPage` 里写 try/catch（§840 是逐页补的，不是从根上收的；§840.6 第 3 条）
25. **`list-error-failpath` 的扫描范围要不要从「用了 `<PullToRefresh>`」放宽到「用了 `useContinuousList`」** —— `NoteListView` 没用下拉刷新 ⇒ **它至今不在那道门的扫描范围内**，本节只能靠自己的判据看守它（§840.6 第 4 条）
26. **`DbLockedState` 恢复可达后要不要加「刚刚失败过一次」的上下文文案**（§840.6 第 2 条）
27. **`z-index-ladder` 的 ALLOWLIST key 要不要从行号改成 `文件:选择器`** —— 同一个 key 已经咬人五次（声明行 → 块起始行 → §841 又一次），改成选择器后对插入免疫（§841.1 D5）
28. ~~`EmailFolderListView` / `TasksView` / `SessionListView` 的扫面~~ **已全部取证完毕**（EmailInboxView 见 §841、EmailFolderListView 见 §842、TasksView 见 §843、SessionListView 已取证为阴性）—— 原问题 —— 本节只覆盖了 `useContinuousList` 的三个消费方，其余**未取证**（§841.6 第 3 条）
29. **收件箱「有行时刷新失败」用非破坏性横幅（现状）还是 toast**（§841.6 第 4 条）
30. ~~`TasksView` 要不要做同样的取证~~ **已取证并修复，见 §843**
31. **`EmailFolderListView` 要不要也用 `DbLockedState`**（像 §837 给详情页做的那样），还是保持「一句话 + 重试」；本轮刻意没有引入新组件（§842.6 第 2 条）
32. **邮件目录页的失败提示要不要给「重新登录」按钮** —— 现在文案提到重新登录但没有入口（§842.6 第 3 条）
33. **`list-error-failpath` 的判据要不要扩一维**：除「错误分支必须带行数守卫」外，再加「**loader 的 catch 不得把列表 ref 置空**」（§843.1 D2）。本轮只在自己判据里实现了它，**没有动那道门的语义**（§843.6 第 1 条）
34. **「失败保留旧行」的陈旧度怎么表达** —— 要不要显示「上次更新时间」（需要给数据打时间戳）（§843.6 第 2 条）
35. **连续读取失败要不要自动重试/退避** —— 当前只有用户手动点重试（§843.6 第 3 条）
36. **`loadInstances` 的 `this.instances = []` 要不要也按归属/有行处理** —— `OpenCodeHub` 会显示错误，但实例选择器仍会被清空，`selectInstance` 的 `instances.find(...)` 随后会走到「实例不存在」分支（§844.5 第 1 条）
37. ~~扫面表里三处「未处理」~~ **已全部取证（§845.1）**：`EmailSummaryView` 是假阳性（早已修过）、`InstanceListView` 有错误出口不是谎报、**`ConfigList.vue` 是真缺陷已修（§845）**—— 但 `InstanceListView` 的「刷新按钮 + 清空」**未修**，转第 40 条
38. **两个同名 `SessionListView.vue`（`features/sessions/` 与 `features/opencode/`）要不要改名** —— §844 排查时已踩过一次混淆风险（§844.5 第 3 条）
39. **`catch 不得把列表置空` 要不要升格成仓级门禁** —— §843 待办第 33 条；§844 的扫面器证明它能一次性捞出 7 处候选，是一道**划算**的门（§844.5 第 4 条）
40. **`InstanceListView` 的「刷新按钮 + 清空 instances」要不要修** —— 有错误出口（不是谎报），但 `:115` 仍清空且 `:9` 有刷新按钮 ⇒ 刷新失败会丢行；**未在设备上验证**（§845.6 第 2 条）
41. ~~「`v-else-if` 相邻性」要不要变成一条能自动跑的门~~ **已落地（§846）** —— ⚠ 口径更正：CI **从来没漏过**，仓库既有 `vue-template-compile` 门一直在 `npm run gates` 里覆盖全仓 `.vue`；缺的只是**我自己那份判据**。已给 `list-load-state.test.ts` 加 13 例（94 → **107**），对**本会话经手的 7 个视图**逐个 `parseSFC` + `compileTemplate`。真实文件端到端变异读数：变异后 `list-load-state` **105 pass / 2 fail**（红的只有新增那组）、仓库门 **186/1**，**§840–§845 那 105 条文本判据全部无感**（§845.7 第 1 条）
42. **`ConfigList.vue` 将来若加「切实例」入口，`instances` 的归属问题会像 §844 的 store 那样出现**（§845.7 第 3 条）
43. **「本会话经手的视图」这张判据登记表要不要变成可推导的** —— §846 的尺子靠**人工登记**覆盖哪 7 个视图（§846.5 第 2 条）；同族的「覆盖范围靠人肉登记」本会话已翻车两次：§844 的 `z-index-ladder` ALLOWLIST 按行号登记被我的编辑推了 42 行、§843 的 `n/a → enforced` 靠人肉点（§846.5 第 2 条；⚠ §847 第 13 条登记表同样是人肉维护的，靠反向对账才不至于静默漏扫）
44. **`chatAgentStore.syncError` 这块死状态要不要清掉** —— 赋值 6 处、**零消费**，且与「视图各自 catch」是两套错误通道；清掉要么删字段、要么让视图改读它（属产品口径）（§847.10 第 1 条）
45. **`store-error-consumers` 的文件级豁免要不要收紧到渲染点级** —— 现状「一处 `apiError(` 放行整文件」，`AgentLibraryView` 正是靠第 99 行一处调用让另外几处原始渲染顺带过关；与 §841「守卫空洞满足」同形（**属门禁语义**）（§847.10 第 2 条）
46. **`AgentDetailView` / `AIChatView` 要不要也给「角色加载失败」一个出口** —— 本节只给两个列表页加了；详情页失败时是 `agent` 为空、聊天页是选择器为空（§847.10 第 3 条）
47. **拍照失败要不要给「去设置」入口** —— 现在只有一句提示；与第 32 条（邮件目录页「重新登录」无入口）同族产品口径（§848.8 第 1 条）
48. **「rethrow 函数有没有未接住的调用点」要不要推进成一条仓级门禁** —— 现状全仓没有任何门检查这件事，§847/§848 都是人工扫；判据体系该收成一条还是各写一组，本身也是产品问题（§848.8 第 2、3 条）
49. **剪贴板写入失败要不要加 `document.execCommand('copy')` 降级** —— 现在选的是「失败就说清 + 让人手动复制」；另一条路是无权限时无感降级（但 execCommand 在新 WebView 上也被弃用）（§849.6 第 1 条）
50. **`RssItemDetail.doShare` 的 catch 还在用原始 `e?.message`（`:63`），而同文件 `toggleStar` 已用 `toUserMessage`** —— 同文件两套口径；它是 `check:raw-error` 的存量基线条目，§849 **未改**（改了会让基线变化，属另一件事）（§849.6 第 2 条）
51. **§849 的「被用户动作触发」近似判定要不要收紧** —— 当前会漏掉经中间函数间接转调的形态，还是接受「它是候选生成器」这个定位（§849.6 第 3 条）
52. **`useSessionDrafts.persistTo` 的「写失败不阻断输入」要不要给出口** —— 草稿的价值全在能存下来，写失败静默 ⇒ 用户以为存了、进程一杀就没；与 §832/§833（随手记草稿）同族，但这是**会话草稿**这一侧，§850 未改（§850.7 第 1 条）
53. **跨文件导入的 callee（`void store.refresh()` 那 112 条里的一大部分）要不要也纳管** —— 现状靠 §847 的 `error-outlet-audit` 间接覆盖，没有「调用点接不接住」这一维（§850.7 第 2 条）
54. **`sanitizeEmailHtml` 允许 `style` 标签这件事要不要收紧** —— 三个选项：① 禁 `<style>` 标签但保留 `style` 属性（多数邮件仍可读）；② 保留但剥掉 CSS 里的 `@import` / `url()`（去掉外泄面、保留排版）；③ 两套净化器合并成一份带参数的共享实现。**本节建议先做 ②**（纯技术加固、不改变产品观感）（§851.7 第 1 条）
55. **邮件正文要不要改成 `iframe` + `sandbox` 预览** —— 邮件客户端通例，能同时解决 CSS 注入与远程图片回执；属产品口径（会改变现有排版与交互）（§851.7 第 2 条）
56. **`index.html` 要不要加一条 CSP** —— §851 实测**没有任何 CSP**；加 CSP 会同时影响其它渲染路径，属独立的一件事（§851.7 第 3 条）
57. **§852.5 那 3 个「未处理」的 URL sink 要不要也接上白名单** —— `provider.authCodeUrl`（服务端 OAuth 配置）/ `shareDocUrl`（分享接口下发）/ `entry.url`（用户自填）⇒ 三者风险都很低，但接上成本也极低（§852.9 第 1 条）
58. **`tel:` / `mailto:` 要不要分开处理** —— 现在 `mailto:` 放行、`tel:` 不放行，是我按「会交给系统处理」这条线划的；若产品上需要「点击拨号」，口径要改（§852.9 第 2 条）
59. **RSS 正文与 RSS 链接要不要在服务端入库时就校验** —— 前端净化是最后一道，前端改版 / 新客户端都可能漏；入库时校验能覆盖所有消费方。属架构口径（§852.9 第 3 条）
60. ⭐⭐⭐ **`pocket_token` 要不要挪出 localStorage** —— 三条路：① 服务端改成 **JWT cookie 鉴权**（`sse.ts` 的注释已经这么写了，需配套改 CORS/同源）；② 原生侧用 **Keystore / Secure Preferences**（但 `native/keystore.ts` 目前是 stub，等于要先把原生插件建起来）；③ 维持现状并接受「同源注入 ⇒ 凭据可被读走」这个已量化的风险。**这是本会话至今最需要属主拍板的一条** —— 它决定了 §851 那条 XSS 的**真实损失上限**（§853.8 第 1 条）
61. **WS/SSE 的 token 查询串要不要去掉** —— 现状是注释写明的 dev 过渡态；生产化要改 `requireAuth` 的例外分支，属服务端改动（§853.8 第 2 条）
62. **`z-index-ladder` 的 ALLOWLIST key 要不要改成 `文件:选择器`** —— 同一个 key 已被行号漂移咬到**第六次**（本轮 §854 改 EmailInboxView 又推了 17 行）。已从「声明行」改到「规则块起始行」两次都没根治 ⇒ 改成选择器才对插入免疫。**属门禁结构改动**，本轮只按先例重新编号（§854.6 / §854.8 第 1 条）
63. **`useConfirm` 的兜底 `window.confirm` 要不要在 native 平台禁用** —— `useConfirm.ts:47` 在 `ConfirmDialog` 未挂载时回落到同步阻塞的 `window.confirm` ⇒ 一旦可达，§854 刚修掉的 D10 等于没修。可达性未知（「极端时序」），本轮未动（§854.7 / §854.8 第 2 条）
64. **marketplace 三个 `runInstall` 的 `installing.value = true` 要不要补 `finally`** —— 现在按钮不会永久 disabled，**只因为** `loadVersions`/`loadReleases`/`install` 三个 callee 恰好都吞错；将来任一改成 rethrow 就会卡死。属防御性加固（§854.8 第 3 条）
65. **`NotificationsView::open()` 的乐观已读要不要给反馈** —— 本地先置 `read_at` 再跳转，服务端失败时刷新会回退 ⇒ 界面短暂说谎。现状是**有意**的取舍（注释写明「失败不挡跳转」），是否补一个回滚提示（§854.8 第 4 条）
66. **`TasksView::handleRefresh` 用 `Promise.all` 而同家族 `MessagesHubView`/`NotesHubView` 用 `allSettled`** —— 同一仓里两种口径。是否统一（改成 `allSettled` 会掩盖部分失败）（§854.8 第 5 条）
67. **§855 宽口径那 34 条非创建类动作要不要逐条取证** —— 本节收窄到「创建类」并逐条读完了那 14 条；抽样确认 `CalendarView::removeEvent` 与三个 `runInstall` 都有等价守卫，但**不声称**其余 34 条安全（§855.8 第 1 条）
68. **「第一个 await 之前变真」要不要做成仓级门禁** —— 本节判据是登记 + 定点断言；覆盖全仓要先补全「创建类动词表」并解决 composable/store 的函数解析（属门禁语义，§855.8 第 2 条）
69. ⭐⭐ **审批附言的上限该是多少、要不要有上限** —— §856 证明了**本仓后端没有这个限制**（`opencode_http.go:722`），那个 500 是前端凭空加的；本轮只消除了「静默」。**若上游 OpenCode 也有限制，该值应由属主给定**；若没有，是否直接删掉上限、允许任意长度附言，属产品口径（§856.10 第 1 条）
70. **「`finally` 无 `catch`」这条新口径要不要推广成门禁** —— §856 只对审批链路四个动作断言；全仓推广要先解决「哪些 `try/finally` 是合法的纯读取」（§856.10 第 2 条）
71. **§854 那条「体内无 try」的口径要不要补「有 finally 必须有 catch」** —— 属既有判据语义变更；本轮只在 §856 的新文件里并行钉了它（§856.10 第 3 条）
72. **定位失败要不要说一声** —— `captureDeviceLocation` 把「权限被拒」与「设备不支持」都折叠成 `null`，`onCaptureLocation` 直接忽略 ⇒ 用户点「获取当前位置」什么都没有。要区分这两种 `null` 并提示吗（§857.8 第 1 条）
73. **AI 提标签降级要不要告知** —— 离线/失败时静默退化为本地规则标签，界面不区分 ⇒ 用户以为用了 AI（§857.8 第 2 条）
74. **「有 finally 无 catch」要不要并入常规轮换扫面** —— 已连续两节产出真缺陷（§856 四处、§857 三处）；做成门禁仍见 §856.10 第 2、3 条（§857.8 第 3 条）
75. **AI 对话历史损坏后要不要提供「尝试自动抢救」** —— §858 只做了「留证 + 如实告知」。末尾截断的 JSON 在很多情况下可修补（补一个 `]` 或 `}` 就能读出大部分历史）；做自动抢救会引入「猜边界」的风险，属产品/技术口径（§858.8 第 1 条）
76. **`pocket:ai-chat:corrupt:*` 的留存与清理策略** —— 现在只写不清，理论上可无限增长（每次损坏多一个键）。要不要限量/定期清理（§858.8 第 2 条）
77. ⭐⭐ **`useDeviceStore` 接回 UI 还是摘掉** —— 它现在**全仓零消费零入口**（自己的定义文件之外 0 引用，无 `.vue`、无测试、无门脚本），却仍在被 `app_prefs` 跨设备同步；且 `recommendedModel` 返回的 `'vosk-small'` **不在** `native/sherpa.ts` 的 engine 联合（`paraformer | sensevoice | whisper-base | zipformer`）里 ⇒ 死状态的取值集合已与真正在跑的引擎漂移。附带一个更根本的问题：**`deviceTier` 是「这台设备的能力」，放进 `app_prefs` 跨设备同步本身就是错的**（高档设备的偏好同步到低档设备，低档设备就以为自己是高档）。接回 UI / 修枚举 / 摘掉同步字段，三选（§859.2、§859.8 第 1 条）
78. **定时任务报「参数 JSON 非法」时用户改不了** —— `showPrompt` 为真时 JSON 文本框是 `v-if="!showPrompt"` 隐藏的（还在 `<details>` 里）⇒ 现在至少会说实话，但用户得先把任务类型改回去才能修。要不要在参数非法时把文本框也显示出来（§859.3、§859.8 第 2 条）
79. **推荐位/能力探测的「失败降级」要不要告知（并入 §857 第 72/73 条）** —— `meeting-related-search` 三个函数失败降级为「没有推荐」；`isKeystoreAvailable` 的文案断言了它**并不知道**的原因（「当前平台**未提供**密码箱原生插件」，而真实情况也可能是插件在但探针失败）（§859.5、§859.8 第 3 条）
80. **`check:dead-features` 的扫描范围要不要扩到 `stores/` 与 `utils/`** —— `scripts/audit-dead-features.mjs` 的 `--dir` 被**硬编码**拼成 `src/features/<dir>`（`--dir stores` 直接 `ENOENT`）⇒ 这两个目录完全在死代码审计视野之外，这正是 `useDeviceStore` 能在全绿门禁下长期存在的原因。属门禁语义变更，本节未代改（§859.5）

---

## #58 最新决定（2026-10-16，属主已拍板①）

属主在问卷中明确选择 **① TS 补齐 Go 护栏，保留两份完整重复长段**。以下历史段落中 #58 的“三选一待拍板”状态由此覆盖，不再是当前阻塞。

已在 `dedupeSegmentTextSoft` 加入全文相同且码点数 `> MAX_OVERLAP` 时原样拼接的保护；不改变精确档、Go、阈值、部分重叠或多段累积后缀的契约。新增生产 soft 路测试覆盖 41/45/50/56/60 码点、40 码点边界与非 BMP 字符，并核对 `dedupeStep`、`dedupeSegments`、`renderTranscript` 两份正文及说话人。

相关三文件测试重跑 **81 PASS / 0 FAIL / 0 SKIP**（276.75ms）；隔离副本撤护栏时 #58 七项 **3 PASS / 4 FAIL**（41/45/50/56 码点红，60 码点与两个边界对照绿）。另一个初版对照因 LCS 覆盖率不足写错预期，已改成真正达到阈值的重叠输入。全量门禁已结束：**通过 2/46，第 3 项 test:all 失败**；全量用例 3572 = PASS 3568 + FAIL 2 + SKIP 2。两条失败为 ASR 价表新增档位未登记及 Sherpa 旧骨架指纹消失；单独复跑 13 项为 11 PASS / 2 FAIL。不能宣称全量通过，未改 GOLDEN 或能力口径消红。详见主文档 §781。

属主同时授权自行使用录音播放测试、从历史查 ASR。已找到公开 sherpa-onnx 四人中文 `openpocket-s762/0-four-speakers-zh.wav`，实测 16kHz、单声道、56.86 秒；当前 shell 四项 ASR 相关 env 均未设置，旧 key 文件和探针脚本在工作区未找到，未发现运行中的 pocketd。连接器第一次抓取公开 URL 返回业务 400；上传该公开音频后返回业务 **402：订阅计划余额耗尽**，两次转写均为空（CLI exit 0 不代表成功），不自动重试。后续已在工作区隔离下载官方 SenseVoice int8 与桌面运行库，完成本地真人音频文件转写：整段 56.86 秒耗时 2.747 秒、输出 65 码点；约 15 秒分段合计输出 134 码点，内容明显不同，质量尚未通过验收；没有人工真值，不能据长度差直接判定错误率，也不能把非空当验收通过。详见 `.artifacts/asr-s782/asr-result.md` 和主文档 §782。尚未做声学播放录制或完整录音→ASR→refine 验收；没有人工参考稿，自然会议发生率和真人 CER 未测。历史通道可用不等于当前可用，受控实验也不等于自然会议发生率。

最新接续（主文档 §783–§784）：实际生产 `SplitWAV` 默认窗口切10段，逐帧合计909771帧，与原音频一致；本地ASR共149码点，其中7段有文字、3段只有“。”。生产 `planRefine` 拒绝65码点整段（`full-too-short(65<74)`），但三段纯句号仍保留到refineSegments，并原样进入显式调用的**前端回落提示词纯函数**；没有执行HTTP/LLM，也未证明实际回落发生。以真实54码点文本构造两份重复，隔离撤护栏红、恢复绿；不是自然重复证据。相关正确五文件联合 **35 PASS / 0 FAIL / 0 SKIP**。过滤口径仍未代拍，完整录音链、CER、自然发生率仍未验收。

声学最新状态（§785）：已实际用内置扬声器播放公开样本并用内置麦克风录制一次；两进程exit0，但62秒录制目标只保存917760帧/57.36秒PCM（差4.64秒，原因未定位）。本地SenseVoice输出17码点，未计算CER，不能判捕获完整或质量通过。此前“尚未做播放录制”由此更新；应用录音/VAD、Android与完整HTTP/LLM/数据库链路仍未验收。录音可能含环境声音，仅留本地、不上传。§786追加：实际SplitWAV切15段，逐帧/逐字节拼接与保存录音一致，可排除切分损失但不能排除录制损失；逐段ASR批次180秒超时，仅4段完成、11段缺失，其中已完成2段纯句号。不用部分结果冒充完整精校输入，未重启超时任务。

**属主最新输入（2026-10-16 05:18，问卷 `ask_b5cc574b55d33c5b828f4262` 为用户明确回复，不是自动超时）**：暂时没有素材或人工逐字参考稿，保留质量/CER/自然发生率未验收；属主将先安全配置已授权测试网关，随后通知复核与测试。该回复不代表网关已经配置或当前可用，也不授权启动PG、充值、改生产服务或上传麦克风录音。收到配置完成通知前不自动重试402、不继续加压ASR；后续即便HTTP/LLM通道跑通，缺真值的质量验收仍不能核销。

**06:27 可执行纯函数回归复核（等待期间记录）**：`meeting-dedup-asr.test.ts`、`meeting-final-transcript.test.ts`、`__tests__/refine-plan.test.ts` 与 `.artifacts/asr-s782/` 内三份生产探针联合执行 52/52 PASS / 0 FAIL / 0 SKIP，15 suites，123.16ms。HEAD `8eb806af`；本轮六个文件全部就位：源码两份、设计文档两份、实验报告一份未跟踪。**该读数只证明纯决策与去重出口没坏，不构成质量验收**。

**20:26 第二次可执行复核（同一等待期间，无属主新输入）**：① 同六文件联合 **52 PASS / 0 FAIL / 0 SKIP，15 suites，241.62ms**（耗时高于 06:27，采样时 load average 50.85/30.50/24.68，耗时不作预算）；② `TestDumpSplitSegments` 补 `STT_DUMP_SEGMENTS`/`STT_DUMP_SOURCE` 重跑实际生产 `SplitWAV`，切出 **15 段**、边界与 §786 逐段一致（seg-0 [0.00–5.24] … seg-14 [53.22–57.36]），与上轮 `acoustic-split/` **15/15 段逐字节 SHA-256 相同**、合计 **917760 帧 == 源录音帧** ⇒ 切分在相同输入上确定性成立，只重复了 §786 的无损结论，未新增切分缺陷证据；③ §781 两条红单独复跑仍为 **13 = PASS 11 + FAIL 2**，失败断言与 §781 登记逐字相同（`minimax-asr-1.0 / glm-asr` 未入 GOLDEN 价表；Sherpa 旧指纹已从 `SherpaPlugin.java` 消失）⇒ 并发会话的提交未消红，仍未代拍口径；④ `git diff --check -- docs/design frontend/src/features/meetings` 退出 0。本轮**无属主新输入**，未改 §781 及以上任何既有结论；HTTP→LLM→DB 验收与 CER 仍缺网关配置通知与人工真值，两条红与质量验收均未销。

**2026-10-17 第二十八轮待办快照续核（8eb806af→23e89988，HEAD=23e89988）**：① `99d9f5bc` 仅清扫 `docs/README.md` 索引行快照描述 30 域/48 模块→25/47（与被描述文档自身 §1/§2 声明对齐），**未触 §781 两条红**、未改既有结论；② `e5faa224` `fix(cors)` 把 Capacitor 固定源 `http://localhost` / `https://localhost` 改为**无条件**放行（此前仅 `devAuth=true` 时放行 ⇒ 真机 2411DRN47C 对 `pocket.itestu.cn`（`POCKET_DEV_AUTH=false`）登录永远停在登录页，curl 200 但 WebView 全挂），并修正 `.maestro/{flashcards-write,meetings-entry}.yaml` 两处真机 flow 契约（清草稿再断言 / 先清浮窗再断言），新增 `cors_capacitor_origin_test.go` 4 个断言；本轮**单独复跑**该文件 `go test ./internal/server/ -run "TestCapacitorAppOrigins|TestDevAuthLocalhostAnyPort|TestCorsMiddlewareEmitsACAO" -count=1` ⇒ **4 PASS / 0 FAIL / 0 SKIP**（0.630s），并复跑 §781 同两条红仍为 **11 PASS / 2 FAIL**、失败断言与登记逐字相同 ⇒ cors 修复**未**消 §781 那两条红（口径仍待拍：`minimax-asr-1.0 / glm-asr` 未入 GOLDEN 价表、Sherpa 旧指纹已消失），与本简报自身**无**回退；③ `23e89988` 即本简报自身，仅文档增量（追加 `06:27` / `20:26` 两枚续核点），源码 `git diff --check -- backend frontend/src` 退出 0。**本轮无功能决策变更**，§781 两条红、CER、HTTP→LLM→DB 验收仍待属主拍板或网关配置通知。 `.artifacts/` 目录 436MB 仍未跟踪（见下方 `.gitignore` 续核）。

**2026-10-17 第二十九轮续核（c8d389e7→994d37d4，恢复时间 10:21，HEAD 已再次漂移）**：① **HEAD 从交接快照的 `c8d389e7` 前移到 `994d37d4`**，区间共 6 个 commit（`9d3c0925` Input.insertText 改主密码填值 + `423dd2a7`/`e4fdc3c6`/`765b62f3`/`b13a9c2d`/`994d37d4` 五条 maestro 修复，含 step0 恢复、ensureDriver 三态、meetings-entry 文本节点合并判据、APP_NOT_RUNNING 重试预算），**均属并发会话的 maestro/harness 范围**：`git diff --stat c8d389e7..HEAD -- docs/design/2026-10-06-recording-quality-fixes.md docs/design/2026-10-11-pending-decisions-brief.md frontend/src/features/meetings` **输出为空** ⇒ 本简报与主文档、以及录音链路源码本轮**一字未改**，交接快照的代码结论仍然有效；② 三源文件纯函数基线 `meeting-dedup-asr.test.ts` + `meeting-final-transcript.test.ts` + `__tests__/refine-plan.test.ts` 复跑 **48 PASS / 0 FAIL / 0 SKIP，15 suites，89.19ms**（目标 48/0/0 达成）；③ `refine-prompt-parity.test.mjs` **7 PASS / 0 FAIL**（2 suites）；④ §781 两条红单独复跑 **13 = PASS 11 + FAIL 2**，失败断言与 §781 登记逐字相同（`minimax-asr-1.0` / `glm-asr` 未入 GOLDEN 价表；`sherpa-onnx AAR not integrated` 指纹已从 `SherpaPlugin.java` 消失）⇒ 6 个并发 commit **未**消红，仍未代拍口径；⑤ cors 判据 `go test ./internal/server/ -run "TestCapacitorAppOrigins|TestDevAuthLocalhostAnyPort|TestCorsMiddlewareEmitsACAO" -count=1` ⇒ **ok（0.678s）**，`e5faa224` 的 4 断言仍绿；⑥ `git diff --check -- docs/design frontend/src/features/meetings` 退出 **0**；⑦ **§795 前置仍未满足**：本 shell `POCKET_LLM_GATEWAY_URL` / `POCKET_LLM_API_KEY` / `OPENPOCKET_LLM_API_KEY` / `LLM_API_KEY` **四项全部 UNSET**，环境里仅有无关的 `LLM_GATEWAY_ADMIN_PASSWORD` ⇒ 历史可用 ≠ 当前可用，未注入、未打任何 `/api/llm/chat`、不代配网关；⑧ `.artifacts/`（436MB）已由 `.gitignore:189` 覆盖，`git check-ignore` 命中，工作树 **clean**。**本轮无属主新输入**（§795 等网关配置完成通知、§796 等人工逐字参考稿均未到），未改 §781 及以上任何既有结论。

**2026-10-17 第三十轮续核（HEAD `6572fd52`，恢复时间 11:22，无属主新输入）**：本轮做两件事，都不需要属主输入。① **交接「阻塞/风险」里两条欠账经取证已是过期快照，不应再被当成待办**：交接写「§627 间歇失败 4 条仍未测真实发生率」，实际主文档 **§639**（真实条件 `npm run gates` 24 轮全绿、0 红，含正控证明量具有牙）＋ **§742**（三档压强：并发实例 K=32/64/128/256 共 **896 次执行 0 命中**，最接近原发条件那档 **95% 上界 ≈0.31%**；裸 `test:all` 15 轮 × 2 文件 = 30 次执行 0 命中 ⇒ 上界 ≈9.5%）**已把发生率量完**；`§757.1/§757.2`（`###` 层，标题 grep 用 `^##` 会漏）还纠正了 §638.8 里一个标签错误（「24 轮 0 红」的分母是**轮**，上界应 ≈3/24 = 12.5%，现正文已改正）。交接另一条「§638 联合 9 处还原变异未做」同样是**快照过期**——§638.8 ⑤ 已明确「**登记不补**」，理由是联合变异要改 8 个文件含 runner 脚本本身、风险高于收益，且 §638.5 的教训正是不要在已验过的结论上继续堆验证；§638.7 的**逐处双向单点**验证（有牙 / 不掩盖别的失败）已完成 ⇒ **按现有决定不补，不要再起这一轮**。② 按 **§757.1 的同一口径**（只读 grep「护栏字样在不在」，非「护栏位置与语义」）在当前 HEAD **重跑时效性核查**：§638 八处加固 ENOENT 命中数逐字未变（`run-mjs-tests.mjs` 2 · `source-scan.mjs` 5 · `vue-template-compile.test.mjs` 3 · `store-error-consumers.test.mjs` 3 · `fixed-overlay-keyboard.test.mjs` 3 · `keyboard-avoidance-prereq.test.mjs` 3 · `css-vars.test.mjs` 3 · `teleport-deep.test.mjs` 3），§633 runner 双通道锚点（`run-mjs-tests.mjs:203`「即使 node 退出码为 0 也不能放行」）与 reporter 累加（`test-file-census-reporter.mjs:41/52-55`）均在 ⇒ **§633 与 §638 结论在 `6572fd52` 仍成立，未被 6 个 maestro/harness commit 回退**。⚠ 该核查只覆盖这 **10 处**，§757.1 自己写明的洞仍然有效：「门不红」推不出「所有已修结论都没被回退」；§795 前置仍未满足（四项网关 key 全 UNSET），§796 仍无人工真值。

**2026-10-17 第三十一轮续核（HEAD `ad4493d0`，恢复时间 11:23，无属主新输入）**：给并发会话那 6 个 commit 补一次**定向门禁复核**（不重跑 10 分钟全量，因为被改的文件有自己专用的门）。① 改动面取证：`git diff --stat c8d389e7..994d37d4` 只触 **8 个文件** —— `.maestro/` 下 5 条 yaml（`email-accounts` / `email-browse` / **`meetings-entry`** / `messages-hub` / `more-entries-open` / `more-grid-reach` / `settings-llm-gateway`）+ `scripts/maestro-run.mjs`（**+263 / −53**）；对 `frontend/` 下 `test|spec|gates|scripts` 的 `--name-only` 过滤 **0 命中** ⇒ **frontend 测试与门禁脚本本轮一字未改**，§781 那 2/46 全量读数在结构上不受影响。② 被改文件专用的门恰好是两项，经**官方 runner** 跑 `node scripts/run-gates.mjs --only check:maestro-flows --only check:no-hardcoded-paths` ⇒ **2/2 通过 · EXIT=0 · 0.7s**：`check:maestro-flows` 结果 **OK**（已检查 **24/25** 条 yaml；未列入配置的 1 条是探针流 `_probe-tap2.yaml`，被检查的 1 条子流 `_set-master-password.yaml` 只能由父流 `runFlow` 引用）；`check:no-hardcoded-paths` 自检 **4/4**（正臂「无条件写死的绝对路径被检出 2/2」＋ 负臂 A `IS_WIN` 的 Windows 路径 / B 多平台候选列表 / C 注释里的历史路径 / D 模板占位）＋ 扫 2 个目录 0 命中。这条门存在的**起因**正是 `maestro-run.mjs` 的 driver APK 目录写死成绝对路径 ⇒ +263 行改写后该门仍绿，是有意义的正面读数。用官方 runner 而非直接 `npm run` 是因为 `--only` 对**不在 gates 名单里**的名字会直接报错 ⇒ 本次能跑通本身即证明这两项**确实接线**。③ ⚠ **更正本会话自己上一轮的一处过度断言**：曾据 `grep -rln "maestro-run" … | head -10` 说它「全仓只有文档引用、无测试覆盖」——那是 **`head -10` 把 16 个 `scripts/*.mjs` 引用截断**造成的假阴性。准确读数是：**16 个代码文件引用它**，且它**有**两道静态门在检 —— `check-no-hardcoded-abs-paths.mjs:56` 把它列为**显式扫描目标**，`check-maestro-flows.mjs:783/811/833` 直接 `readFileSync` 它的源码做契约断言（checker 与 runner 共用同一张选择器溯源表）。因此缺口**不是**「无覆盖」，而是**新增 263 行缺行为级/单元级覆盖**（静态门只查「护栏字样/路径在不在」，不执行新逻辑）。按 §757.1 的口径登记：**只读 grep + 两道门自检**，未跑真机真流，故不声称真机行为已验收。§795 前置仍未满足（四项网关 key 全 UNSET），§796 仍无人工真值。

**2026-10-17 第三十二轮续核（HEAD `843ded8d`，恢复时间 11:31，无属主新输入）**：本轮做了一次**会议链源码覆盖率普查**（不需要属主输入），得到一个此前没被量化的维度，并定位到它是**门禁的盲区**而非门禁漏报。① 口径：`src/features/meetings` 下非测试 `.ts`/`.mjs` 的 `export function` / `export async function` / `export const` 符号共 **132** 个；被测参照物 = `src` 下文件名含 `.test.` 或 `.spec.` 的 ts/mjs/js 共 **339** 个测试文件（与 `check:test-coverage` 自报的「测试文件 339」**可对账一致**）。② 读数：**29/132（22.0%）** 的导出符号在**任何**测试文件里 **0 次出现**（用 `符号名` 全文匹配，不带任何 head 截断，给出完整计数与匹配宽度）。③ ⭐ **为什么这个维度一直没被看见**：`check:test-coverage` 量的方向**相反** —— 它自述首行即「**孤儿测试卡口**：存在却从不被任何 gates 脚本执行的测试文件，新增即失败」，跑出 **337/337 无孤儿 · EXIT=0**。⇒ 本条不是「门禁漏报」，而是**门禁根本不量这一轴**；因此「gates 全绿」**推不出**「导出逻辑都被测到」。④ 29 个的构成：绝大多数是薄存储/SQL 透传（`archiveMeeting`/`deleteMeeting`/`unarchiveMeeting`/`getSegments`/`listVoiceprints`/`clearVoiceprints`/`SQL_{INSERT,EXISTS,DELETE_ONE,DELETE_ALL}` 等）；**带分支的纯函数只有少数**（`topicTokens`/`topicShift`、`formatMeetingWhen`/`formatDuration`/`formatParticipants`、`studioMenuItems`/`canDispatchMeeting`/`meetingAccDispatchInput`、`meetingActionItems`）。⚠ 已逐个读过前一组，**未发现可判定的技术缺陷**：`topicTokens` 的 CJK 二元组与 STOP 过滤自洽，`0.15` 阈值属**过滤口径（产品决定，不代拍）**；`formatMeetingWhen` 走 `toLocaleString('zh-CN')`，其输出本就随 ICU 版本变化，写断言只会得到脆弱测试。⑤ 普查途中发现一条**潜在项，登记不修**：`src/native/recordingRuntime.ts:701` 的收尾 best-effort 同步 `void getMeeting(meetingId).then((m) => { if (m) syncMeetingMetadata(m) })` **没有 `.catch`**（同文件 45/78 行的同类写法都有）。可抛性已逐段取证：`syncMeetingMetadata` **内部自带 `try/catch` 并返回 `false`，永不 reject**；只有 `getMeeting` 会 reject（它 `await localDB.queryOne`）；`src` 下全局 `unhandledrejection`/`window.onerror` 处理器 **0 命中** ⇒ 影响有界：上一行已写入 `status:'completed'`，失败只留一条未处理拒绝日志，不影响收尾。⚠ 取证更正：我一度怀疑 `ingest-speech.ts:78` 的 `void liveTranslate(...)` 也缺 catch，**读原文后确认第 83 行有 `.catch(() => {})`**，该怀疑作废。本轮**不改代码** —— `recordingRuntime.ts` 刚被 `9813ee36`（merge origin/main 后整合多模块增量）动过，且要为 `stop()` 建 MediaRecorder + localDB + TTS 的整套 mock 才够做变异验证，成本高于该潜在项收益 ⇒ 按「技术性修复须配变异双向验证」的纪律登记待拍板。

**2026-10-17 第三十三轮（`ask_a4d442946e95afc70c87abb1` 收到四项回复，HEAD `10af20dc` 起）——本会话第一次拿到真实输入，四项全部落地或如实登记**：
① **§796 真实素材到位**：属主给了 `~/Downloads/录音测试/` 下 `…规划.mp3`（752.292s / 16k 单声道）+ 同名 txt（53 行）。⭐ 但**那份 txt 不是人工逐字稿**，是「ASR 正文 + AI 总结」混合体（带 `说话人1 00:01` 等分离时间戳、含 `Dance you all to this home` 英文幻觉、末尾 `以上内容由AI生成，仅供参考`、xattr 为 WeChat 送达）⇒ 所有比对都是**两套 ASR 之间的分歧**，不是人对机器的 CER。我一度怀疑正文尾部被截断（`…需要去检查` 等断句），**量了行长分布后推翻**：26 行正文长度 4–267 字，短行正好落在说话人切换点，3137 字 ÷ 752.292s = **4.17 字/秒** ⇒ 参考侧覆盖完整。
② **§795 的真实读数**：整段 752.292s 一次喂 ⇒ **1001 码点 / RTF 0.0813**；按 15s 窗切 **51 段**、**一次加载模型**批量跑（为此新写 `.artifacts/asr-s788/LocalAsrBatch.java`，非每段起一个 JVM）⇒ **3341 码点 / RTF 0.0175 / 0 空段**，即**分段多拿 3.34 倍内容且快 4.6 倍**。生产纯函数读数：`shouldAdoptFullTranscript` = **`full-too-short(1001<1670)`（整段被正确拒绝）**、`planRefine` 51 段全保留、`rosterFixes=0`、`renderTranscript` 3697 字符 / 51 行、**0 空段 / 0 纯标点段**（对比 §786 麦克风路径 4/15 完成、2 段纯句号）⇒ §783 在 57s 小样本上的结论**不是小样本假象**。
③ **CER（四种口径，真编辑距离）**：去标点全字符 **12.23%**（2911/3087，距离 356）· 保留标点 16.09% · 纯汉字 **11.17%** · 非汉字 59.85% ⚠分母仅 264 不可单独引用。⛔ **明确不作为真人字错率**：参照物本身是 ASR，且已确证两处参照侧缺陷（英文幻觉、AI 段无对应音频）。
④ **§795 的 HTTP leg ⛔ 卡在凭据**：`.env.example:82` 得生产网关 `https://llm.kxpms.cn/v1`（key 变量 `POCKET_LLM_GATEWAY_API_KEY`）；属主提供的 key 写入仓外 600 权限文件且**全程不回显**；`GET /v1/models` 对 **远端与本地 `:8782` 都是 HTTP 401 `{"code":"invalid_key","message":"Invalid or expired API key"}`** ⇒ 只做这两次只读诊断即止，**不重试、不绕过、不代配**，本节**不构成任何「LLM 链路已通」结论**。
⑤ **属主拍板①已实施**：`minimax-asr-1.0`（$0.38/h）与 `glm-asr`（$0.50/h）两条 gateway 档补进 `stt-price-catalog-load-bearing.test.mjs` 的 GOLDEN（在册 9 → **11**），并注释写明它们与 external 组的 `asr-1.0` / `glm-asr-2512` **是两档不同的东西**。该门 **7 PASS/1 FAIL → 8/8 全绿**；§781 两条红复跑由 **11 PASS/2 FAIL 变为 12 PASS/1 FAIL**，剩下那条正是属主要求**暂不动**的 Sherpa 指纹。变异验证：把 `minimax-asr-1.0` 改成 `0.39` ⇒ **报红并点名该条目**，已还原（`git diff` 空）。　⚠⚠ **后续更正（务必先读）**：此方案**已主动作废、不随本轮合并落地**。并发会话的 §787 对同一问题给了更优解——external 组全在册、gateway 组**刻意不逐档钉**，改用「不得击穿基线最低价」的不变量兜底；且把 `capabilities-stub-gate` 登记表里的 Sherpa 一并移除（属主要求「暂不动」的那条红也已消）。⇒ **上面这段读数只代表当时在本地 `main` 上的实测，不是最终口径**，最终以 §787 为准。
⑥ **属主拍板 `recordingRuntime.ts:701` 已修**：不 inline 写 `.catch`（那种只能被「文件里有没有 catch」这种恒真风险最高的静态判据覆盖，仓库有 `check:vacuous-guard` 盯着），而是抽 `src/native/best-effort.ts` 的 `runBestEffort(label, work)`（可注入依赖、`Promise<void>` 契约、同步 throw 与异步 reject 都吞），调用点改用它，并补 `src/native/__tests__/best-effort.test.ts` **7 用例**。**双向变异都验证过**：撤掉 `.catch` ⇒ **2 红**；把调用点改回裸 `void getMeeting(...).then(` ⇒ **1 红**；两处恢复后均 7/0。`npm run test:native` **54/54 绿**、`npm run typecheck` **EXIT=0**（中途一次 `TS2322`：`.catch` 推出 `Promise<unknown>` 与 `Promise<void>` 契约不符，改用 `then(ok, err)` 形态修复并复验护栏仍有牙）。
⚠ **本轮未 push**；两个 commit（代码 / 文档）均在本地 `main`。

**2026-10-17 第三十四轮（HEAD `a5095c40` 起，无属主新输入）：把 §798 自己留下的待判问题在本地答掉**。① **§798 多要了一个 key**：那个问题（长会议会不会被判 `full-too-short`）只需要**生产形态的整段文本**，不需要远端 ASR —— 两侧都用同一个本地 SenseVoice，变量就只剩切分形态。② **三臂读数**（同一段 752.292s）：生产整段形态（`SplitWAV` 38 段 → `strings.Join(texts,"\n")`，**跨段零合并**，其中 **13 段边界落在静音处**）= **3348 字**；生产分段形态（`VadSegmenter` 40 段）= **3298 字**；整段一次喂（退化臂）= 1001 字。③ **判定翻转**：`shouldAdoptFullTranscript(3348, 3298)` = **`adopt: true` / `ok`**，门槛（分段总量的一半 ≈1649）远超 ⇒ **护栏并没有在长会议上拒绝整段转写**；此前 `full-too-short(1001<1649)` 是退化臂的性质。⇒ **护栏两侧行为都在真实素材上验过**：退化输入拒绝 · 正常输入采纳。④ ⭐ **顺带撞出一条新的产品口径问题（登记不代拍；§800 已把其中一条推翻，见下）**：采纳整段后 `planRefine` 的 `refineSegments` **塌成 1 段**（3348 字、跨度 0→752292ms 覆盖整场、`speakerLabel` 全为 `null`）⇒ 分段边界与说话人归属同时丢失、#58 去重在这条路径上完全失效；而**换来的只有 1.5%**（3348 vs 3298）。⇒ 当前「整段 ≥ 分段一半」这个判据对**任何静音边界正常切出来的整段文本都近乎必然通过**，实际效果接近「长会议一律走整段」。是否要加「整段显著更完整」的条件、或采纳后保留分段结构只换正文，属产品决定，不改判定不改阈值。

**2026-10-17 第三十五轮（HEAD `fdcd6cae` 起，无属主新输入）：§799.3 被我自己推翻。** 复读 `planRefine` 的采纳分支时发现设计里本来就有「把整段文本按时间挂回原分段、只换正文」的机制（`meeting-refine-plan.ts:146` 的 `boundary.texts.map(...{...perSegment[i], text})`），我那轮只看到塌陷就下了结论。两次自查定位到真因：① 探针**只传了 `full.text`、没传 `full.segments`**（补传了 38 条真回执后仍塌）；② 读 `meeting-final-transcript.ts:285` 才发现第一道前置是 **`if (!original.some((s) => (s.speakerLabel ?? '').trim())) return null`** —— 归因要求**原分段至少有一个说话人标签**，而 §797/§799 的分段臂**从未跑 diarization**（全 `speakerLabel: null`，当时还明确写了「不编造 A/B/C/D」）⇒ 归因**按设计**返回 null，落进 `buildRefineBaseSegments` 的兜底分支「压成一条」。**双向验证**（同一份 38 条回执，只改有没有标签）：无标签 ⇒ 1 段/`null`/跨度 0→752292；**有标签（合成 A/B）⇒ 40 段、A/B 都在、首 [0,20000] 末 [780000,752292]、0 段空正文**。⇒ 生产跑 `SpeakerDiarizer`，**正常路径不会塌**。⛔ 合成标签只用于验证机制，**不构成任何说话人准确性结论**。**逐节状态**：§799.1 三臂读数 ✅、§799.2 判定翻转 ✅（`adopt: true`，生产形态不再被判 `full-too-short`）、§799.3.1「边界消失+丢说话人」❌ 推翻、§799.3.2「#58 失效」⚠ 收窄（只发生在无标签兜底路径）、§799.3.3「50% 门槛近乎必然通过、收益仅 1.5%」✅ 仍成立 ⇒ **长会议几乎总会走整段**，代价是整段正文按时间重分到原分段（结构与说话人保留，但每段正文被整段版覆盖）；是否加「须显著更完整」条件仍属产品口径，不代拍。


**2026-10-17 第三十六轮（本会话代码改动后的整体回归核验，无属主新输入）**：今天动了生产代码（`recordingRuntime.ts` 收尾同步）与一道门（价表 GOLDEN），因此把回归面重新走一遍：① **新测试文件没有变成孤儿**：`check:test-coverage` = 测试文件 **340（.mjs 219 / .ts 121）· 338/338 无孤儿 · EXIT=0**，`.ts` 由 120→121 即本轮新增的 `best-effort.test.ts`，被 `run-mjs-tests.mjs` 的 `src/**/*.test.ts` 覆盖。② **会议链全量**：59 个测试文件 **771/771 PASS / 0 FAIL**；三源文件纯函数基线 **48/0/0 · 15 suites**（与既有读数逐字一致）。③ `test:native` **54/54**、`npm run typecheck` **EXIT=0**、`check:vacuous-guard` + `check:dead-features` **2/2 通过**（未接线导出仍是基线 11 条，未新增）。④ **全量 `npm run test:all` 跑完**：枚举/执行 339 个文件、4101 个执行事件，**唯一失败是 `capabilities-stub-gate.test.mjs`（1 个 test:fail）—— 正是属主「暂不动」的 Sherpa 旧指纹那条**；⇒ 本会话的两处代码改动**未引入任何回归**。⑤ ⚠ 顺带记一条**差点自造假 bug 的坑**：`check:test-coverage` 报「340 个文件（.ts 121）」而 `test:all` 报「339（.ts 120）」，我一度以为新测试没被执行。真因是**两个数的口径不同** —— runner 打印的是 `discovered = allFiles − UNRUNNABLE`（只扣 `flashcardIo.test.ts` 这 1 个 `unrunnable`；另一个 `recordingRuntimeMimeFallback` 属 `zeroCase` 不扣）。用 runner 自己的 `--print-files`（同一份枚举代码路径）核对：**第 275 行就是 `src/native/__tests__/best-effort.test.ts`** ⇒ 它既被枚举也被执行。⇒ **引用覆盖数字必须连口径一起写**（原始枚举 vs 扣掉 unrunnable 之后），否则会拿两个不同分母互比。（⚠ 本轮那句「动了一道门（价表 GOLDEN）」对应的代码改动**未**随合并落地，见下方第三十七轮；同段「唯一失败 = 属主暂不动的 Sherpa 那条」现已被并发会话 §787 处置，见顶部摘要。）



**2026-10-17 第三十七轮（合并重整 + push，无属主新输入；属主 17:58「请按建议继续」）**：本地 `main` 已领先 `origin/main` **16 个 commit 且全部未 push**，而远端同时被并发会话推进了 **21 个 commit**。处理方式（不改别人的提交、不覆盖、不盲目 pull）：① 建备份分支 `backup/pre-rebase-49e6a802` 保住全部本地工作；② 在**隔离 worktree**（基于 `origin/main` = `83e53852`）上重放，只取**远端未碰过**的文件——`best-effort.ts` + `best-effort.test.ts` + `recordingRuntime.ts` 三个文件，**价表那笔不重放**（理由见上）；③ 在 `origin/main` 之上**复跑**关键读数：`typecheck` EXIT=0、best-effort **7 PASS / 0 FAIL**、三源文件基线 **48/0/0**（未因重整回退）；④ **八节章节整体重编号 §795–§801** 接在远端 §787 之后，避免出现两个 §787；⑤ §799 标题上挂「先读 §800」的更正指针。⇒ 净结果：远端 21 个 commit 一个没动，我的改动全部在它们之上重放且验过。

**2026-10-18 真机契约补验（maestro 合并收尾；基线 `main=55a050c0`，无属主新输入；不占轮号——feat 线可能已续编）**：补齐 handoff 风险节点名的四项。① **211 实例登录冒烟 ✅**：`POST /api/auth/login`（admin + 本地开发口令）→ **200 + JWT（`auth_method=dev-bypass`）**；192.168.31.211:8090 存活（`/api/health` 404 系无此路由，非宕机）。② **cors 契约在合并后端上的活体复验 ✅（不止单测）**：用 `55a050c0` 构建 pocketd、**`POCKET_DEV_AUTH=false`（生产模式）**、隔离 PG schema、本机 18099：`/healthz` ok；真实口令登录走 **legacy** 通道 200；`Origin: http://localhost` → 响应含 `Access-Control-Allow-Origin: http://localhost`（生产模式下该放行**只**由 e5faa224 的无条件 Capacitor 源逻辑支撑）；负对照 `Origin: http://evil.example` → **无** ACAO。叠加第二十八轮的 `cors_capacitor_origin_test.go` 4 PASS。③ **§692/§693 STT 探针（门槛 =1）专项复跑 ✅（口径）**：`go test ./internal/stt -count=1` **全绿（4.8s）**；`TestLiveASRCandidateCoverage` 未设门环境 → **SKIP「候选集完整性探针：需 POCKET_LIVE_GATEWAY=1」**，与 8eb806af「门环境变量由 2 修回 1」逐字一致；**live 侧未执行**——租户 key 本轮不可得（第三十三轮④已实测 owner key 对 `llm.kxpms.cn` 401，key 不入仓），不重试不绕过。④ **§712/§737 i18n 双轨 + 两条真机 flow：静态侧 ✅、真机侧环境性受阻（如实登记，不造假绿）**：`check-maestro-flows` **OK**（24/25 在检，1 条探针流豁免——与第三十一轮读数一致）；`flashcards-write`/`meetings-entry` 的双轨正则（`暂无卡组.*|No decks yet.*`、`新建卡组|New deck`、`笔记|Notes`、`开始会议|Start meeting` 等）在 main 两 yaml 在位。真机复跑**三次尝试均未进入 flow 体**：(a) pocket 包被发现处于 **disabled**（已 `pm enable`；成因未证——MIUI 自动禁用或他线 rig 所为，若属有意请知会撤销本次 enable）；(b) 设备 4c308e2e 被 **llm-gateway-client 会话的 maestro rig 连续占用**（`maestro.cli.AppKt --device 4c308e2e` 00:12/00:17 两批 + iOS xcodebuild 并行），对 `com.kaixuan.llmgw` 的 `pm disable-user` 在 ~1 分钟内被其环境守护**自动撤销并重夺前台**（pocket 起动后 ~100ms 内被顶回）⇒ 按「不砸并行会话测试现场」纪律停止争抢，并把误伤的 `cn.halfking.nsproxy` 恢复 enabled。harness 三道 preflight 守卫（后端可达 / reverse 自愈 18099→18099 / 前台 60s）全程正确工作，**零假绿**。**遗留**：两流真机绿证待设备空闲补跑（前置：rig 停止后处置 llmgw 看门狗——先例=cn.kxpms.smm 双包 disable 法）；设备侧备注——pocket 的 `pocket_api_base` 被本轮 preflight 改指 18099（审计后端，已停），下次 maestro-run 会按 env 重设，无需手工恢复；IME 未动（百度默认）。


## ★ 一页拍板清单（48 项，按「你今天能动什么」排序）

> 这一页的用途：**不必往下翻**，就能决定今天做哪几件。
> ⚠ **它是一份索引，不是一份论证**：每格只给「问题 + 结论状态 + 指针」。
> 论证、读数、变异记录全部留在正文对应小节 —— **要拍板再翻，不要在这里拍**。
>
> ⚠ **体量如实说**：渲染后约 **1.5 页**（92 行）—— 48 项索引压不到真的一页，
> 与其靠漏项换「一页」，不如承认它是一页半并把覆盖补全。
> ⚠ **页内不留长论证**，因为一旦写进去它就撑不下了：
> 上一版把 #29 的全部读数塞进表格单元，结果这一页既盖不住 #25/#34–#51
> **共 19 项**（已补齐），标题里的件数也与表内行数对不上（已按实际重数）。

### A. 🔓 不需要产品决定 —— 你自己能解开的（6 件）

| # | 做什么 | 解开什么 |
|---|---|---|
| ~~**A1**~~ ✅ 已完成（§474/§481） | 真实 CER 已到手：`mimo-v2.5-asr` 拆开后 **含 ASCII 20.4% / 纯汉字 5.2%（逐条核对后约 1% 字错）**；代码**没有**默认档（模型为空直接报错） | 真实 CER 基线已建立 |
| ⭐⭐ **A6**【**§673 新增**】 | **CI 那 4 处 `node-version: "22"` 要不要钉到具体补丁**（`frontend.yml:47/183/226`、`e2e-web.yml:44`） | ✅ **技术那一半我已落地**：`frontend/package.json` 加 `engines.node >= 22.22.2` ＋ 仓库根 `.nvmrc=22.23.3`，**负控验证**在 22.18.0/22.22.1 上报警、22.22.2/22.23.3 上沉默。⛔ **剩下的才是口径**：CI **保持浮动**（安全补丁自动跟进）还是**钉死**（门禁读数完全可复现、但每次发补丁要手工改 4 处）。⚠ **实测依据**：本仓有**两条独立下限** —— 22.18.0（原生 TS 类型剥离，否则约 100 个测试文件零产出）与 **22.22.2**（`jsdom@30.1.1` 的 engines 声明，绑定的那条）。⚠ CI 写 `"22"` **今天恰好没事**（永远取最新 ≥22.22.2），**但那是巧合不是声明** |
| **A2** | 真机跑一行 `typeof (window.SpeechRecognition \|\| window.webkitSpeechRecognition)`（⚠ **§671.3 已改**：原写作只查 `webkitSpeechRecognition`，比守卫少一个名字） | ⚠ **§671.4–§671.6：这一行**定不掉** #21 的平台归属** —— 否定一方推不出归属另一方，且它只回答 Q1（守卫是否早退）；**Q2「字幕是否真 `commit` 出段」要另外看**。**#42** 的判定不受影响 |
| **A3** | 真机录一段 iOS，中途切后台 | **#22** iOS 是否断录 |
| **A4** | 给工作树一个本地兜底（safety 分支 + `git stash -u`，不 push、不动远端） | **#19** 的止血，与合并策略解耦 |
| ⛔⭐⭐⭐⭐⭐ **A5**【§584 新增·§591.3 扩大·**§597/§599 扩大到全仓真实规模·§603 更正计数并核销主链路·§657.1 口径订正**】 | **工作树里未跟踪文件 + 已修改文件，要不要全部进版本控制**（计数**随开发漂移**，见右侧与 §657 的口径订正） | 见下方 A5 说明 —— 它与 **A4 是同一个决策的两半**；⭐ **§603.2 已按可复现命令重数**（原 §599.2 的表自己就加不到总数，故已作废）；⚠⚠ **§657.1 发现两个口径差 11**：`git status --porcelain` 把未跟踪**目录折叠成一行**（本轮 **205**），`git ls-files --others` 逐文件列出（本轮 **216**）⇒ **引用这个数必须连口径一起写**。已修改 **153**（两种口径一致）。⚠⚠ 未跟踪文件在本地 `main` 与 `origin/main` 两条分支上都不存在（§590 已证两分支 2026-10-06 分叉）· ⭐⭐⭐⭐ **§603.3 的核销结论本轮逐字仍成立**（§657.2 复核）：未跟踪的**后端生产代码**仍是 **3 个**（`meetingagent/{agent.go,tools.go}`、`livetest/ledger.go`）—— 另有 4 个是 `testdata/` 下的语料与夹具，不是生产代码 ⇒ **转写主链路 `internal/stt` 的非测试 `.go` 全部已跟踪**，丢文件不成立 · **§657.3 第二维度当前读数**：门禁可见测试 **1038** 个（已跟踪 875 + 未跟踪 163）⇒ 未跟踪占 **15.7%** |

> ⚠ **A5 的计数会漂，且有两个口径（§657.1，2026-10-14 实测）**：
> ```
> git ls-files --others --exclude-standard | wc -l   # 216（逐文件，推荐用这个）
> git status --porcelain | grep -c '^??'              # 205（未跟踪目录被折叠成一行）
> git diff --name-only HEAD | wc -l                   # 153（两口径一致）
> ```
> ⇒ 同一时刻两口径**差 11**。§603.2 做的「按可复现命令重数」方向是对的，
> 但**没写清落在哪个口径上** ⇒ 换口径重数会被误读成「文件少了/多了」。
> ⇒ ⇒ 这条数字**必须带测量时刻**（本轮：2026-10-14 23:0x）。

> ⚠⚠⚠ **A5 缺一整维：文档未入库量（§675 实测，2026-10-15）**
>
> A5 现有口径只有「未跟踪文件」与「已修改文件」两个计数，
> 而这两个计数**只反映文件粒度，不反映内容量** —— 一份 8 万行的设计文档，
> 在 A5 的账上只是「已修改 1 个文件」。
> 实测 `docs/design/2026-10-06-recording-quality-fixes.md`：
>
> | 口径 | HEAD | 工作树 | **未入库** |
> |---|---|---|---|
> | `## §N` 节数 | 12 | 284 | **272** |
> | `## NN.` 节数 | 227 | 372 | **145** |
> | **合计节数** | **239** | **656** | **417** |
> | 行数 | 33,699 | 80,883 | **47,184** |
>
> ⇒ ⚠ **本文 63.6% 的章节、58.3% 的行数从未进过版本控制。**
> ⇒ ✅ 建议把 A5 的维度**补一格「文档未入库量」**（口径：两套编号合计的节数差 / 行数差），
> **与「测试未入库量」并列但分母不同，不能混着报一个数**。
> ⇒ ⛔ `git log --all -S'## §425'` 命中 **0** ⇒ §425 不是「被删」，
> 是**从未提交** ⇒ 同一批 417 节一旦随工作树丢失，**无法从 git 恢复**。


⭐⭐⭐ **A5 为什么必须和 A4 一起拍（§584 实测）**：
一条门要被执行，需要**三件事同时成立** —— ① 在门名单里（`gates.json` 45 条 + `package.json` 45 项，**都已跟踪**）
② **脚本文件在版本控制里** ③ 有人真的执行它。
⇒ **本仓①齐 · ⛔②缺 27 个 · ⚠③ CI 从不跑全量 gates**（`frontend.yml:120`「CI 从来不跑 gates」，已核原文）。
⇒ ⛔ 阳性证据（**未真去 clone**，用 git 只读查询）：
`git ls-tree -r --name-only HEAD frontend/scripts` = **65 个**，
而 `check-gofmt` / `check-blankline-bloat` / `check-quicknote-summary-gate` 在 HEAD 里 **0 命中**；
`git check-ignore` 确认它们**没有被 .gitignore 排除** ⇒ **纯粹没提交**。
⚠ **CI 不受影响**（只引用 3 个脚本且都已跟踪）；
⚠ 顺带一处过期：`frontend.yml:151` 写「gates 共 **23** 条」，现为 **45** 条。
⇒ ⭐ **「门禁 45/45 通过」这个结论目前只在本机这个工作树上成立。**

⭐⭐⭐⭐⭐ **A5 的范围在 §591.3 又扩大了一次（这次是我自己造成的）**：
　 新写的说话人判据**依赖两样都不在版本控制里的东西**：
　 · `frontend/src/native/speaker-calibration.test.ts`（**未跟踪**）
　 · `frontend/src/native/__tests__/fixtures/`（**整个目录 `git ls-files` 返回空**，
　　 既含既有 TTS 夹具 36 KB，也含本次新建的真人夹具 28 KB）
　 ⇒ ⛔ **已在干净克隆里实测**：那个目录**根本不存在**，那条命令 `EXIT=1`。
　 ⇒ ⚠ 与 §584 那 27 个历史脚本的差别：**这条是本轮新写的代码重复了同一个错误**
　　 ⇒ 说明「门禁链脱节」在当前开发习惯里**仍是默认状态**，不是遗留问题。
　 ⇒ ⭐ 夹具里是**标定向量、不含任何用户音频**（沿用既有约定）⇒ 入库无隐私风险；
　　 ⛔ 但体积（36 KB + 28 KB）是否接受**属范围口径，不代拍**。

⭐⭐⭐⭐⭐⭐⭐ **A5 的范围在 §597 第三次扩大，而且这一次含的是生产代码本身**：
　`git ls-files --others --exclude-standard` 在 `frontend/src` 下数出 **23 个未跟踪的生产源文件**
　（已排除 `__tests__` / `*.test.*` / `fixtures/`）：

　· `features/meetings/VoiceprintSheet.vue`
　· `features/meetings/live-summary-gate.ts`
　· `features/meetings/live-summary-schedule.ts`
　· `features/meetings/meeting-ingest-notice.ts`
　· `features/meetings/meeting-next-event.ts`
　· `features/meetings/meeting-recording-finalize.ts`
　· `features/meetings/meeting-refine-plan.ts`
　· `features/meetings/meeting-reminder-note.ts`
　· `features/meetings/meeting-roster-homophone.ts`
　· `features/meetings/meeting-summary-merge.ts`
　· `features/meetings/pinyin-common.ts`
　· `features/meetings/refine-prompt.ts`
　· `features/meetings/refined-ingest-writeback.ts`
　· `features/meetings/refined-transcript-view.ts`
　· `features/meetings/segment-edit.ts`
　· `features/meetings/summary-prompt.ts`
　· `features/meetings/voiceprint-writes.ts`
　· `features/notes/note-todo-notice.ts`
　· `features/sessions/recording-title.ts`
　· `features/sessions/refine-outcome-notice.ts`
　· `native/speaker-features.ts`
　· `native/speaker-relabel.ts`
　· `utils/text.ts`

　 ⚠⚠⚠ **为什么这比前两块严重得多**：
　 · 前两块是**门禁脚本与夹具**（缺了 ⇒ 门禁在本机形同虚设）；
　 · **这一块是产品功能本身** —— 包括用户原始需求的三条主链路：
　 　`live-summary-gate.ts` / `live-summary-schedule.ts`（**录音时即时总结**）、
　 　`summary-prompt.ts` / `refine-prompt.ts` / `meeting-recording-finalize.ts`（**校对合并 + 精校**）、
　 　`speaker-features.ts`（**整条说话人分离链的地基**）与 `speaker-relabel.ts`（**收尾合并与生产常量**）。
　 ⛔ **`git log --follow` 对 `speaker-features.ts` 返回空** ⇒ 它**从未被提交过**，
　 　所以「它以前是什么样、改过没有」**无法用版本控制回答**。
　 ⚠ **§590 已证本地 `main` 与 `origin/main` 在 2026-10-06 分叉（各自 40 个不同 commit）**
　 ⇒ **这 23 个文件在两条分支上都不存在** ⇒ 任何分支切换 / 合并 / clean 都有丢失它们的风险。
　 ⇒ ⭐ **A5 与 A4 是同一个决策的两半**（A4 = 给工作树做本地兜底：safety 分支 + `git stash -u`）；
　 　**A5 才是根治那半**，`git add` 一下即可，但**决定权在你**（不代拍、也不擅自改 git 状态）。

⛔⛔⛔ **A5 动手前必须先做的一步（§600.6 实测，否则第一个动作就制造新问题）**：
`backend/gwdbg` 是 **14,002,690 B 的 `Mach-O 64-bit executable arm64`**（macOS 编译产物），
而 **`.gitignore` 没有覆盖它**（实测无 `gwdbg` / `*.exe` / `*.bin` 规则）
⇒ ⛔ **直接 `git add -A` 会把这个 14 MB 二进制提交进仓。**
⇒ ✅ **正确顺序：先补 `.gitignore` 规则，再 `git add`。**
⚠ 顺带核过：另两个含 `token` 字样的未跟踪文件是**测试文件名**；未跟踪目录里**没有** `.env` / `*.pem` / 音频 / 模型文件。

⭐⭐⭐⭐⭐ **§603 更正与核销（这条改变了 A5 的风险排序，请以它为准）**：
* ⛔ **原 §599.2 的计数表已作废** —— 它的明细相加 `113+75+10+3 = 201` 却写着总计 **204**，
  即那张表**在写下的当天就没闭合**，而后续三节（§600/§601/§602）都把它当既成事实抄。
  按可复现命令（`git ls-files --others --exclude-standard` / `git diff --name-only HEAD`）重数：
  **未跟踪 214 · 已修改 140 · 合计 354**（明细见上面 A5 行）。
* ✅ **最大那个担心已核销**：「未跟踪文件里有没有转写主链路」——
  `backend/internal/stt` 的 **9 个非测试 `.go` 全部已跟踪**（`discovery/full/incremental/provider/
  provider_openai/provider_zhipu/sniff/target/transcribe.go`），该目录未跟踪的 11 个**全是测试与 testdata**
  （含 `cer_group_coverage_test.go` / `target_chinese_accuracy_gate_test.go` / `cer_ascii_split_gate_test.go` 三道 CER 门）。
  ⭐ 而这 11 个里的三道 CER 门**本轮实测 18 用例全 PASS / 0 SKIP / 0 FAIL**（`internal/stt` ok，`STT_EXIT=0`）。
* ⭐⭐⭐ **「214 个未跟踪文件」这个数听着吓人，但真正需要你操心的只是 26 个**：
  前端生产源 **23**（即上面 §597 那份清单）+ 后端生产源 **3**
  （`meetingagent/agent.go`、`meetingagent/tools.go`、`livetest/ledger.go`）；
  其余 **188** 是测试（前端 92 + 后端 76）、夹具/testdata（前端 5 + 后端 4）、门禁脚本 12、文档 3、二进制 1。
* ⚠ **这 3 个后端文件才是唯一会让后端编译不过的**（已跟踪的 `server_meeting.go` 依赖 `meetingagent`）
  ⇒ 若只想做**最小风险**的那一步，`git add` 这 26 个生产源 + 3 份文档即可；
  全量 214 个入库仍需你拍板（⛔ 我不擅自改 git 状态）。
* ⛔ **撤回一条错误登记**：§600.6 / §601.1 写的「`backend/internal/mcp/client.go` 未跟踪」**不成立**
  —— 它已随 `331ba4f3` 入库，且工作树与 HEAD 一致。

⭐⭐⭐⭐ **§615 把那 214 个拆成了三档可执行的动作**（不再只是一个总数）：
* ① **零风险（22 份）**：未跟踪**测试**，而它们守的生产源码**已跟踪** ⇒ 零编译风险
* ② **必须成对（36 份）**：测试与生产代码**都未跟踪** ⇒ 只提交测试会得到空壳
* ③ ⛔ **已作废**（§616 用第二套解析重做，§617 又用第三套推翻）
* ⭐⭐⭐ **§616.3 最刺眼**：`pending-brief-index-coverage.test.mjs`（就是**护栏门本身**）
  读的是 `docs/design/2026-10-11-pending-decisions-brief.md`，而它**未跟踪**
  ⇒ 干净克隆里这道门**不是在检验**（要么 Skip 要么红）。护栏自己也没被护住。
* ⚠ 同类必须成对提交的三份正压在 Goal 主链路上：
  `stt-local-first-claim-gate` → `speaker-features.ts`（声纹链地基）·
  `live-summary-schedule` → `live-summary-schedule.ts`（**即时总结**）·
  `summary-prompt-parity` / `refine-prompt-parity` → 两个 prompt 文件（**精校**）
（85 = 22 + 36 + 27；⚠ 口径见 §615.1：**解析只覆盖模块导入，不覆盖字符串路径**）
⇒ **档①里最值钱的一批**：`meeting-due.ts` 的 **7 份**（时间点进日程主链路）
+ `meeting-dedup.ts` 4 份 + `meeting-final-transcript.ts` 3 份 + `meeting-due-plan.ts` 2 份。
⇒ ⭐ **因此 A5 有一个真正的最小步**：先只提交这 22 份测试，
把「时间点进日程 + 精校 + 去重」的核心判据一次性入版本控制，**不碰任何生产代码**。
⚠ 但要认清：**提交测试 ≠ 功能进库**（前端仍有 23 个生产源文件未跟踪）。
⚠ 且「22 份」这个档位已被 §617 作废（真实是 **43 份**），
⚠ **最小步也改了**：见下方 §617 更正里那个更小的「先提交一个夹具」的做法。
⛔ 执行顺序仍是你的决定，我不代拍。

⭐⭐⭐⭐⭐ **§617 再次更正（以本条为准，上面 §615/§616 的三档数字全部作废）**：
* ⛔ **根因是量具不是代码**：§615/§616 的普查脚本**只在 `/tmp` 里即兴跑、没留档**。
  按纪律「落盘前必须重跑 git 派生计数」，只能重写解析器 —— 重写就炸出**四个解析器缺陷**：
  ① **漏了「`read()` 读源码文本」这一整类门禁**（`meeting-recording-finalize.ts` 的 9 份依赖**全是这一类**，
  **没有一个是 ES import**）② 漏了三种间接写法（`path.join(DIR,'x.ts')` · `const OWNER='x.ts'` ·
  动态 `import(path.resolve(SRC,'x.ts'))`）③ **同名回退返回裸文件名而索引存全路径 ⇒ 永远 0 命中**
  ④ **自指没排除**（测试提到自己的文件名）。
* ⭐ **§615/§616 的「36 份无目标」不是「真的没有依赖」，是解析器压根没抓到那类引用**；
  §617 初稿那个「无目标 0 份」是**假零**（放宽同名回退后看起来全覆盖）⇒
  ★ **整齐的 0 和 100% 一样，都是量具指纹**。
* ✅ **定案（第三套解析器，表内当场验算闭合 `38+4+43 = 85`，与未跟踪测试数一致）**：
  **43 份可独立提交 / 42 份需成对**（其中 4 份原先只能靠间接写法判出，已逐条读源码点验为真）。
* ⭐⭐⭐ **最小步因此变小**：**先提交 `api/__tests__/_wire-helpers.mjs` 一个夹具**
  （**10 份门禁**建立在它上面而它自己未跟踪 ⇒ 只提交那 10 份而不提交它，**10 份一起失效**），
  再加 `features/meetings/meeting-recording-finalize.ts`（**9 份**门禁）。
* ⚠ **新登记纪律**：凡要在文档里写下计数表格，必须**先留下能原样重算的脚本**并在表旁写出路径 ——
  「我跑过」不是可核销的凭据，「你也能跑出同一个数」才是。
  （脚本现落在 `/tmp/opstt/s617/census.py`，属会话临时目录；**是否入库不代拍**。）
* ⚠ 这份普查**仍是下界**：目录扫描、运行时拼路径、模板内嵌源码字符串一律抓不到。
* ⭐⭐⭐⭐ **§622 三方交叉验证：43/42 那个数**不变**，并作废了一个「看起来更干净」的抽取器**
  * ⛔ **v4（把抽取收紧、只保留 `import` 位置的字符串）读数变成 38/1/46** ——
    看着像「有 3 份假阳性」。**逐条点验那 5 份改判文件后：5 次全错**（4 假阴 + 1 假阳）。
  * ⭐⭐⭐ **新纪律**：**收紧抽取必须同时验证「漏抓」方向** ——
    假阴性会让「可独立提交」这种**乐观结论悄悄变大**，而没人会去数它；
    那 3 份一旦单独入库，**在干净克隆里就是空壳测试**。
    ⇒ 可执行做法：任何「把抽取改干净」的改动，都要拿**人工点验过的小样本**做回归。
  * ✅ **v6（补上 §622 发现的「变参 read」盲点）读数 = 38/4/43，与 §617 逐项相同**
    ⇒ 这个盲点真实存在（`read('api','stt.ts')` 两类抽取器都抓不到），但本轮不改变结论。
  * ⇒ **43 可独立 / 42 需成对**经 v5 / v4+人工点验 / v6 三方验证成立；普查**仍是下界**。

⭐⭐⭐⭐⭐ **§628 补了 A5 的第二个维度（此前从未量化过）：结论判据的入库率**
* 判据 = **测试文件正文出现 `§数字`**（它在引用某个已登记的修复/结论）。
* | | 带 §引用的测试 | 覆盖的节数 |
  |---|---|---|
  | **未跟踪** | **65** | **107** |
  | 已跟踪（对照） | 11 | 23 |
* ⇒ ⭐⭐⭐⭐⭐ **只有未跟踪测试在引用的节 = 100 节**（已跟踪侧零覆盖）。
* ⚠ **诚实标注**：抽检 6 节、20 个引用点，**13 个（65%）上下文确有断言**
  ⇒ **「100」是上界**，真实值约 **60~100 节**；
  精确数要逐节点验 100 次（人工活），**未做** ⇒ **只能当量级用，不能当台账**。
* 具体样本：`useLiveSummary.ts:75-84` 的 **§402 修复**（`references` 不被整体覆盖清空）
  由 `summary-reference-cross-round.test.mjs` / `live-summary-refresh-gate.test.mjs` 钉住，
  **两份都未跟踪** ⇒ 源码的修复会被 commit 带走，**保护它的判据不会**。
* ⇒ ★ **A5 其实有两个性质不同的维度**：
  ① **能不能编译** = 26 个生产源（§618/§620 已实测钉死）；
  ② **结论有没有判据** = 这 65 份未跟踪测试（**本节首次量化**）。
  补齐 26 个 ⇒ 能编译能跑测试，但 §402 这类修复**仍然没有任何保护**。


* ⭐⭐⭐⭐ **§630 抽检扩到 3 份：3/3 全部咬住**（三份判据**都未入库**）
  | 节 | 变异的那一行 | 变异后 | 还原后 |
  |---|---|---|---|
  | §402 | `useLiveSummary.ts:84` 去掉 `mergeSummaryReferences` | 🔴 # fail 1 | ✅ 9 pass |

⭐⭐⭐⭐ **§631 / §632：定位并修掉了一个让门禁间歇变红的**测试隔离竞争**
* **症状**：`test:styles` 间歇红，`ENOENT: scandir 'frontend/src/__probe_fixture__'`。
* **成因**：`z-index-ladder.test.mjs` 会在 **`frontend/src/` 下临时建目录再删掉**，
  而**同一时刻**另有测试在递归遍历整个 `frontend/src`（`node --test` 同 glob 内并发执行）。
* **普查结果**：递归遍历 `frontend/src` 的扫描方共 **6 个**，§631 只堵了 1 个，§632 把其余 **5 个**也加固；
  反向确认**竞争源唯一**（其余 5 份写文件的测试都写在 `os.tmpdir()` 或 `node_modules/.cache`）。
* ✅ 每处加固后**逐个单跑通过**（7 / 6 / 6 / 8 / 5 pass），全量门禁 **45/45**（56.7s · `GATES_EXIT=0`）。
* ⚠ 边界：**竞争窗口很窄、未能主动复现**（glob 连跑 8 次 0 失败）；成因由**那次失败的栈 + 源码**定位。
  §627 那 4 条间歇失败**形状不同，仍单独立项未归因**。
  | §401 | `meeting-dedup.ts:384` 关掉 `renderTranscript` 的 manual 豁免 | 🔴 # fail 1 | ✅ 7 pass |
  | §352 | `meeting-due.ts:384` 把 `nextMonthAnchor` 档位压成恒 1 | 🔴 **# fail 4** | ✅ 6 pass |
  ⇒ 三处还原后 **sha256 逐个回基线**（`0ed97ca2…` / `18cf4f18…` / `676fbce1…`）。
* ⚠ **n=3 不足以宣称 107 节全有牙** ⇒ §628 的「60~100」估区间**不变**。
* ⭐⭐⭐⭐ **两次「变异打偏」的教训**：§352 我连错两次，都表现为「变异后仍全绿」——
  先改**周族** `wLead`（该判据测的是月族），再改月族通用分支（而「下下个月**底/初/中**」走的是 `nextMonthAnchor` **独立分支**）。
  ⇒ **「变异没打红」的第一嫌疑永远是「改错地方了」**；
  ★ 判别动作：**先读判据文件自己写的变异点**（本例第 6-10、20 行就写着「变异自证：把 nextMonthAnchor 改成…」）。
* ⛔ **环境事实：`/tmp` 会被清空** —— 本轮 `/tmp/opstt` 整个消失，连备份文件一起没，
  导致变异一度留在工作树里（`meeting-due.ts` sha `62d07fbc…` ≠ 基线）。
  ⇒ 处置：**反向编辑**还原（逐字改回）+ **逐个核对 sha256**；
  ⛔ 绝不用 `git checkout --`（该文件还有其它未提交改动，会连带丢掉）。
* ⛔ ⛔ **本轮自检工具 `/tmp/opstt/doccheck.py` 也随目录丢失**，重建版**不等价于原件**
  （把散文里的行内反引号当围栏 ⇒ 2051「不平衡」，而正确口径是 **2030 配平**）。
  ⇒ **已停止用重建版当证据**，改用可信口径自查（围栏行计数 / U+FFFD / `grep -c '^## §NNN'`）+ 仓库自带的 `npm run gates`。
* ⭐⭐⭐⭐⭐ **§629 给这个维度补了第一个实证**（变异 ⇒ 未跟踪判据转红 ⇒ 还原后绿）：
  把 `useLiveSummary.ts:84` 的 §402 修复改回缺陷写法
  （`mergeSummaryReferences(...)` → `result.references`），
  跑 `summary-reference-cross-round.test.mjs` ⇒ 🔴 **EXIT=1 / # fail 1**；
  还原后 **EXIT=0 / 9 pass**，且 **sha256 回到基线** `0ed97ca2…fb54c3b719`。
  ⇒ 同时证实「**判据有牙**」+「**判据未入库**」⇒ **只提交已跟踪改动时，这条保护会真的消失。**
* ⛔ 静态口径已到极限：`§` 引用的 469 处里 **303 在注释、165 在字符串（测试名）、仅 1 处在代码**；
  按「引用文件是否含断言」再切一刀 ⇒ **107/107 节**都被含断言的文件引用，**无一可剔除**。
  ⇒ 其余约 100 节的精确比例**仍未核**（估区间 60~100 不变）。


⭐⭐⭐⭐⭐ **§618 用构建实验实测出了 A5 的「地雷」（这条改变执行顺序，请优先看）**：
* ⛔ **「先提交已跟踪的改动、未跟踪的回头再补」这条路不存在** ——
  在「HEAD + 仅已跟踪改动」的干净树上实测：
  `go build ./...` **EXIT=1**（`server_meeting.go:19` 缺 `internal/meetingagent`）·
  `vue-tsc --noEmit` **EXIT=2**（**39 条 TS2307 / 13 个文件**）。
* ✅ **补齐后两个命令都 EXIT=0** ⇒ **最小绿集被实测钉死**：
  **后端 3 个**（`meetingagent/{agent.go,tools.go}` + `livetest/ledger.go`）
  + **前端 23 个**（迭代补齐：20 → 3 → 0 错）。
  ⭐ 这 **3 + 23 = 26** 与 §603 早先数出来的「全仓未跟踪生产代码 26 个」**逐文件一致** ——
  **那份清单不是统计，是能让仓库变绿的下限**。
* ⭐ 所以 **A5 的最小步不是「先提 43 份测试」，而是「这 26 个生产源」**；
  测试/夹具/门禁/文档那 188 个是否入库，仍是产品口径（**不代拍**）。
* ⭐ 顺带查清 §617.8 遗留项⑤：`internal/livetest` **被 4 份已跟踪的 `internal/stt` 探针测试依赖**，
  而那 4 份测试**在 HEAD 里根本没有这个 import**（import 是工作树未提交改动加进去的，各加 26~160 行）
  ⇒ 它就是「本地全绿、提交即红」的典型样本。
* ⚠ 本节只验了**编译**；干净树上的**运行时**依赖（后端测试数据、前端夹具）**未测** ⇒ **§620 已补测，见下**。

⭐⭐⭐⭐⭐ **§621 把 #57 的证据升级成「成因已钉死」**（本轮实跑 CI 的真实命令 `go test -race ./... -count=1`）：
* ✅ **全仓 0 处 `DATA RACE`** · 57 包通过 · 18 包无测试 —— 「有没有竞态」从问号变成实测结论。
* ⛔ 唯一红的是 `internal/agent`，**三条一起红**：`SendPromptHappyPath` (10.03s) ·
  `SendPromptResume` (10.04s) · `NonZeroExit` (10.12s) —— 全部卡在 10.0x 秒。
* ⭐⭐⭐ **成因不是用例坏、也不是竞态，而是「跨包并行 × 固定 10s 时限」**。实测阶梯：
  三条单独跑（`-race`）**全过**（0.34/0.84/1.10s）→ **整包**单独跑 `-race` **全过**（ok 7.99s）
  → 全仓 `./...` 才红。根因是 `adapter_pi_test.go` 里六处写死的
  `context.WithTimeout(…, 10*time.Second)` 撞上「真实子进程在并行下起不来」。
  ⚠ 该文件**已跟踪**，所以与「未入库文件」无关。
* ⇒ 三个选项的代价现在各自可核：① 在 CI 的命令下**本轮就红**（不是偶发）②直接命中时限
  ③同样命中且保住契约、但改动面最大。⛔ **仍不代拍**。
* ⚠ **边界**：CI 机器规格未知 ⇒ 本机红**不等于** CI 红，只能说「同一命令在负载下会红」。


⭐⭐⭐⭐⭐ **§620 复核：§618 结论一字未改，且补上了「运行时」这一半**（含对我自己的纠己）
* ⛔ **先作废我自己的两处实验构造缺陷**：`git archive` 导出的树**没有 `.git`**（⇒ `repohygiene`
  报「找不到仓库根」）· 只打 `frontend/src` 子树补丁**漏了 `frontend/scripts`**（⇒ 造出
  「测试 import 了不存在的导出」的假失败）。**正确构造只有一种**：真 `git clone` + 按
  `git diff --name-only HEAD` 逐个复制，**不整包、也不打子树补丁**。
* ✅ 纠己后重测：`go build` ⛔EXIT=1（同一条错）· `vue-tsc` ⛔EXIT=2 · 39 错 / 13 文件 ·
  最小绿集 **23 → 0 错（逐文件相同）** ⇒ **§618 结论完整复现**。
* ⭐⭐⭐ **补齐 26 个之后整个套件是绿的**：`npm run test:all` **EXIT=0**（252 个测试文件全跑）；
  `go test ./...` 只红 `internal/agent` 一个包 —— 而它红的是 **#57 那一族**。
* ⭐⭐⭐ **#57 的证据升级**：三次分别落在 `SendPromptHappyPath`（§605 工作树）·
  `InterruptSession`（§618 干净树）· `ProviderErrorExitZero`（§620 干净树）
  ⇒ **不是一条用例，是一个族会轮流红**。三选一仍**不代拍**。

⭐⭐⭐⭐⭐ **§619 顺手查出更大的一条：45 项门禁里 24 项在干净克隆里跑不起来**
* ⛔ `package.json` 的 `scripts` 引用了 **53 个** `scripts/*.mjs`，其中 **27 个未跟踪**；
  按门禁名逐项对齐：**45 项门禁里 24 项的脚本未跟踪** ⇒ 干净克隆里 `Cannot find module`。
* ⭐ 而 `npm run gates` 的**入口** `run-gates.mjs` **是已跟踪的**
  ⇒ runner 能起来，然后逐个撞上缺脚本 ⇒ CI 的真实形态是**第一道缺脚本的门就红**。
* ✅ 实测坐实：干净树上 `node scripts/check-gofmt.mjs` ⇒ **NODE_EXIT=1 / MODULE_NOT_FOUND**。
* ⇒ ★ **「本机 45 项全绿」这句话的效力有边界**：它证明**当前工作树自洽**，
  **不是**「已提交状态受这 45 项保护」。§616.3 的「护栏门自己未跟踪」是这条规律的一个样本。

#### ⭐⭐⭐⭐⭐ §599 的两条操作性纪律（不需要拍板，直接照做）

**① 核「某个门禁/判据是否在版本控制里」，不能只查 `package.json`。**
必须按 `run-mjs-tests.mjs` 的 `COVERAGE_GLOBS` 枚举**实际执行集合**再逐个查 `git status` ——
`run-mjs-tests.mjs` 是用 **glob** 收文件的（它头注释写明就是为了避开 glob 失配），
⚠ **glob 收进来的文件不会出现在 `package.json` 的显式列表里**。
⭐ 本轮就是这么查出 `pending-brief-index-coverage.test.mjs`
（**守住本页计数口径的那道门**）自己未跟踪的 —— §591.3 的「27 个门脚本」之所以严重低估，
根因就在这条纪律被漏掉了。

**② 测「门禁会不会漏红」时，必须跑门禁的真实命令，不能用简化命令。**
⚠ `node --test <file>`（默认 reporter）与门禁（自定义 reporter）的失败可观测性**不同** ——
§599.1 实测：同一个 suite 构建失败，**前者退出码 0、后者退出码 1** ⇒ 结论完全相反。
⭐ 所以「我用简化命令量出来退出码是 0」**不能**当作「门禁会漏红」的证据。


### B. ✅ 证据已经定完方向，**只需签字**（5 件）

> 这几条不是「待调研」，是「读数已把选项收敛到一个、等你点头」。

| # | 问题 | 已收敛到 | 指针 |
|---|---|---|---|
| **#25** | 会议链 250ms 重叠 | ⭐ **只剩甲**（去固定重叠改按需回溯）—— 乙「调小」无中间档、丙「改去重口径」上界追不上，**两次独立出局** | §523–§535 |
| **#39** | 数字统一写法 | ⭐ 统一阿拉伯；实测损失**可逆 3 处 / 不可逆 1 处（25%）**。⚠ **§656.2：这个决定被提示词第 4 条禁令顶回去，必须一起拍** | §531/§532 / **§656.2** |

> ⚠⭐ **#39 与精校提示词第 4 条是同一个决定的两半（2026-10-14 实测，§656.2）**：
> 两份提示词里都逐字写着
> `4. **不得改动数字**。你无法从转写判断哪个数字才是对的，猜的那一下必然是错的。`
> （`refine-prompt.ts:105` 与 `server_meeting.go:1418`）。
> ⇒ 那句「不可逆 1 处（25%）」是**允许模型改数字**的前提下测出来的。
> ⇒ ⛔ **若只拍「统一阿拉伯」而不同时放宽第 4 条，这条决定会被提示词原样取消，
> 而且界面上看不出任何异常**（模型照做、输出照旧、没有报错）。
> ⇒ 建议写成**一个复合决策**：「统一阿拉伯 **且** 把第 4 条放宽为『数字按统一写法规范』」。
> **本节只登记，不代拍。**

| **#45** | 本地 ASR 选型 | ⭐ **不立项**（无优势 + 更脆 + 慢 6.5× + 需繁简 pass） | §544 |
| **#47** | 放宽精校提示词第 3/6 条禁令以允许跨段合并？ | ⭐ **暂不放宽，先修切分** | §546 |
| **#50** | 随手记 `SilenceCut=false` 时是否不做跨段去重 | ⭐ **甲＝不去重**（去重的存在理由是切片重叠，随手记不重叠 ⇒ 只剩误删可能）。⭐ **§656.1：现状已经是甲，不必再拍** | §551 / **§656.1** |

> ⭐ **#50 的现状就是推荐答案（2026-10-14 实测，§656.1）**：
> 跨段去重的生产调用点**只有两处**，且**都在会议链** ——
> `composables/useLiveSummary.ts:67`（实时总结）与
> `features/meetings/meeting-refine-plan.ts:100`（会议精校计划）。
> 随手记链 `native/recordingRuntime.ts` 对 `dedupeSegment*` / `lcsNetNew` **零引用**。
> ⇒ 唯一剩给你的是：**要不要把这个偶然写成显式契约**（例如注释或门禁钉住
> 「随手记链不得接去重」）。**当前无需任何代码改动。**
> ⚠ 但**不能**读成「随手记不该去重、已验证无害」——
> 它只说明**当前没有**。**若将来给随手记接精校，去重会随之被接上**，
> 那时这条才真正需要拍。


### C. 🔴 阻塞主链路（20 件）

| # | 问题 | 状态 | 指针 |
|---|---|---|---|
| **#19** | 40/40 分叉怎么处理 | ⭐ 先做 **A4** 兜底，再单独谈合并 | — |
| **#14** | 自动模式在这三个里选中谁 | ⛔ 便宜档在本机网关目录**一个都没有**（670 条实测）⇒ **不是客户端选型，是网关侧要先加种子** | §482/§484/§487 |
| **#14b** | 解析目标与实际调用之间供给挂掉的 race | ⛔ 真实缺口只有这一条；要不要同转写内重试 = 成本口径 | §482.5 |
| **#15 B 格** | 随手记正文从不被整段音频校对过 | ⭐ 选项应拆成两件：① 开整段重转（**代码早已写好**，改的是那个条件）② **补跨段合并/校对**（② 才是需求第一条要的东西） | §478/§480/§481 |
| **#17** | 会议 LLM 调用要不要设每场上限并对用户可见 | ⚠ 上限必须**同时管住 `/summary` 的两条路径** | §451 |
| **#26** | 随手记没有录音中即时总结 / `references` / 建议（0/3） | ⭐ 先拍 A（成本零、让用户知道现状）→ 再在 B 与 D 间选 | — |
| **#29** | CER 主导错误是同音字 | ⛔ **天花板由「标识符/术语」决定，不由精校能力决定** ⇒ 数字类（确定性映射）与术语类该分开。⚠ **补记（§655.2）：已落地的是「人名同音」，术语表这一路输入压根不存在** | §475/§477/§481 / **§655.2** |

> ⚠ **#29 的边界订正（2026-10-14 实测，§655.2）**：
> 同音修复**已实现且已接线**（`deriveRoster` / `repairRosterHomophones` /
> `repairSegmentBoundaryHomophones`，逐段 + 跨段两轮，4 个消费者）。
> ⇒ 但它的名单只有两个输入：`participants` 与 `speakerLabel`
> （`meeting-refine-plan.ts:119-124`）—— **两个都是人名维度，没有任何一路是术语/标识符**。
> ⇒ 这恰好**正面印证**了本条那句「天花板由标识符/术语决定」：
> 机制是齐的，**够不到的是输入集合，不是算法**。

| **#30** | ASR 热词通道从未接线 | ⭐⭐ 成本比想象低：`ProviderRequest` 的注释就是为它写的，缺一个字段 + 三个 Provider 各读一次。⚠ 待拍词从哪来、送多少条、哪些模型支持 | §484 |
| **#31** | 上游返回 **200 + 空文本** 被当成功落库 | ⛔ **已证伪**（§654）—— 守卫在 Provider 层，早于聚合层。真正剩给你拍的只剩「部分段空文本时，占位符要不要留在正文里」 | §484.4 / **§654** |

> ⛔ **#31 的证伪（2026-10-14 正控实测，§654）**：原文「触发条件已实测存在」**在当前代码下不成立**。
> 守卫有三道，逐层读数：
> * 第一道（**真正拦得住的那道**）：`transcribe.go:344-346` —— `TrimSpace` 后为空即 error `upstream returned empty transcript`；
> * chat-audio 另有一道**在 `finish` 之前**的早返回：`transcribe.go:222-225`，`total_characters == 0` ⇒ `dropped the audio`；
> * 第二道：`full.go:487-508` 的 `Succeeded == 0` ⇒ `全量转写没有任何有效文本（N 段全部失败…）`；
> * 出口：`server_stt_stream.go:87-94` 走 `ok:false`，**不是** `ok:true`。
>
> ⇒ **「任一片出过字就算成功」是守卫到位之前的措辞，已过时**；
> 　 「空文本算不算失败」这个问题，代码里**已经实现为「算失败」**。
> ⇒ ⚠️ 之前那句判定是从**只读 `full.go`** 推出来的：只看到第二道，没数清整条链有几道。
> ⇒ **真正剩给你拍的口径**：部分段空文本时，正文里会混入
> 　 「［第 N 段转写失败：upstream returned empty transcript］」占位（随手记侧同时给「有 N 段未能转写」提示）。
> 　 **这些占位符要不要留在用户正文里**，还是只留计数提示？
> ⇒ ✅ 已补常驻门 `backend/internal/stt/empty_text_guard_pin_test.go`（5 测试，M1/M2 双向变异验证），
> 　 钉的是**守卫覆盖面**（`finish` 恰好 2 个调用点）—— 防「将来新增链路忘了走 finish」。
| **#32** | 纪要走 MCP 供外部 Agent 检索，还是服务端内置智能体 | 产品方向而非缺陷；两条路不互斥但同时做成本高 | — |
| **#33** | 分片探针两处静默丢分片 | ✅ **代码已修**（§491）。⛔ **新形态读数从未真跑过** ⇒ 剩一条：拿 >30s 语料重跑确认与 §478 可比 | §491–§493 |
| **#43** | 「到网上搜索」至今没实现 | ⛔ 与用户原话**落差最大**；后端全仓 `web_search` 零命中。⭐ 建议与 **#51 一起拍** | §542 |

> ⚠ **#43 的口径订正（2026-10-14 实测）**：「零命中」**指后端 `.go` 源码零命中**（实测 0）。
> ⚠ **全仓 `grep -rli web_search` 会返回 3 个文件，但没有一个是实现**：
> ① `backend/internal/chatagent/seed/agents.json:660` —— 某智能体 system prompt 里
> **举例用的 JSON 草稿**（`"tools_called": ["web_search"]`，讲「工具调用日志长什么样」）；
> ② `backend/data/chat_agents.sqlite` 与 ③ `backend/pocketd` —— ①的**编译/入库副本**。
> ⇒ 写这条时必须带上「后端 Go 源码」这个限定，否则下一个人 grep 到 3 条会误判成「已实现、登记过期」。
| **#44** | ASR 选型 / 说话人分离 | ✅ 阻塞项已解：实时流式里**现状已接近最优**，漏的是一个原生参数（热词，见 #30）。⚠ **§655.3：与 #30/#36 是同一条，建议一起拍** | §484 / **§655.3** |

> ⭐ **#44 与 #30 是同一条，别当两个阻塞拍（2026-10-14 实测，§655.3）**：
> `ProviderRequest`（`provider.go:88-111`）8 个字段**无热词项**，
> 而 `transcribe.go:526` / `provider_openai.go:84` **确实在发** `prompt` 字段
> ⇒ **参数位在，专用通道不在** ⇒ #44 那句「漏的是一个原生参数」成立，
> 且与 #30/#36 指向同一处。**建议合并成一个决策**（本节只登记，不代拍）。

| **#48** | #15 B格① 的三步执行前置 | 顺序：加采纳守卫 → 失败软提示 → 才放宽门限 | §547 |

> ⚠ **#48 的进度补记（2026-10-14 实测）**：三步里**第①步已经落地**，
> 第②步**未落地**、第③步**未落地**。清单原文只记了顺序，**没记进度**，
> 下一个人会从第①步重做。实测读数：
> * 第①步 ✅ `meeting-refine-plan.ts:85` 已在调 `shouldAdoptFullTranscript(...)`，
>   并在 `:150-151` 暴露 `adopted` / `adoptReason`。
> * 第②步 ⛔ **UI 侧零消费者**：`grep "adoptReason|adopted" frontend/src/features/meetings/*.vue`
>   **0 命中** ⇒ 「失败软提示」那一层压根没接上（这同时复核了 §557 的「零消费者」结论**仍成立**）。
> * 第③步 ⛔ 未动（按 §48 的口径，门限本就**不许**在①②之前放宽）。
| **#49** | 会议链底噪自适应 | ⭐ 方向已实测确认（失效 **8/35 → 1/35**）；⚠ **窗口没有安全裕度，2s 是悬崖下沿**（1s 失效 35/35）；⛔ 只缺 CER 一侧。⚠ **补记（§655.1）：生产代码里自适应一行都没有，两条链都是固定阈值** | §548–§562；⭐ §589：**同一次真机采集可一并解掉 #54** / **§655.1** |

> ⛔ **#49 差两步，不是差一步（2026-10-14 实测，§655.1）**：
> 原文「只缺 CER 一侧」会让人以为「只差把参数接上」。实际读数：
> | 链 | 常量 | 位置 |
> |---|---|---|
> | 前端 VAD | `energyThreshold: 0.015` | `vad-segmenter.ts:40` |
> | Go 全段切分 | `silenceFloor = 0.012` | `full.go:109` |
>
> **两条都是固定常量，全仓无任何滚动窗口噪声估计** ⇒ ① 自适应本身没实现；
> ② 即使实现，§562.0 已抓到量具错误：**§561 实际测的是 20s/40s/100s，
> 而它推荐的「2s」从未被测过**。
> ⇒ ⚠ 并且 `8/35 → 1/35` **不能当成机制安全性的一般证据**：§562.3 实测
> 那份语料里「窗内纯语音」的形态只占 **0.07%**（26/37445），
> **读数好是因为语料几乎构造不出该机制的失效态**。

| **#51** | 「参考资料与建议」在默认部署形态下被静默替换 | ⭐ 推荐加 `source` 路径信号 + 标注来源。⭐ **§662.2：推荐拆两半，「第三种提示」这一半已经在代码里（`MeetingInsightPanel.vue:91/:126`），只有 `source` 信号未做** | §563 / **§662.2** |
| **#34** | 时间点入日程「抽错了且不报错」 | ✅ 骑缝错法已修；待拍是否**扩到裸钟点**（同句无日期锚则 null，推荐） | §524/§540 |
| **#4** | 相对期限以**会议时刻**还是**点击时刻**为基准 | ⭐ 倾向会议时刻；「事后补录老录音」有代价。可注入时钟**已在位**，改一个默认值即可 | §394 |
| **#35** | 随手记切片策略 | ⛔ **已收回**「把窗口调大」；正解是**上静音感知切分**（推荐甲） | §525/§527 |
| **#36** | ASR 热词落点：`prompt` 字段本仓**已经在发** | ⛔ 问题是上游认不认 ⇒ 需实测/上游答复 | §526 |

### D. 🟠 重要但不阻塞（37 件）

| # | 问题 | 指针 |
|---|---|---|
| **#8** | 随手记要不要有说话人标签 | ✅ **§666.2 复核成立**：`segments` 生产侧零消费者、`transcribeFull` 只取 `result.text`（`:1268`/`:1275`，行号漂 50 行）。⭐ **与 #24 建议合并拍** | **§666.2** |
| **#12** | 「网上搜索总结技能」至今没有实现（与 #43 同源） | 外部先例调研已完成。⚠ **§672 补范围**：选 B 要面对**两条链**（前端 4 条内联**无契约** / Go `summaryJSONSchema` **有契约**），且两个 `buildSummaryPrompt` **同名不同义** —— 「改哪条」**是要拍的一部分** | §439 / **§672** |
| **#18** | 去重算法两份手抄的第三道闸要不要对齐 | ✅ TS 侧已补 `MIN_ANCHOR`，分歧 5→0 |
| **#20** | 实时字幕段该继续豁免去重，还是改成 `asr` 享受去重 | ⭐ **推荐 A**（继续豁免，但**改注释与门**让它们不再声称有保护）· 该段 `origin` 是 **`'manual'`**（`meeting-utterance.ts:41`）· ⚠ **§663.2 订正两处数字**：行号 `:471`→`:473`、落点 1 处→**2 处**；并**作废**该节第一版对 sherpa 链的误认 | §438 / **§663.2** |
| **#21** | 两个 ASR 引擎同时写同一张段表 | 选项 A/B/C/D；**先看 A2 的读数**。⚠ **§671：机制成立，但平台归属三格全部降级为「未测到」** —— 原「真机跑一行就能定」的那行**查错了名字集、且结论读反了**，已就地改写 | §443 / **§671** |
| **#22** | iOS 三个 Swift 插件未注册（原写作「整场 0 个 asr 段」，§446 已收回） | ✅ **§669.2 逐字复核成立，且机制证据在已装框架里可直接核对**：`CAPPlugin+LoadInstance.swift:9` 是 `extension CAPBridgedPlugin where Self: CAPPlugin` ⇒ 三个只继承 `CAPPlugin` 的插件实例化入口不可达。**仍先看 A3 的读数** | §446 / **§669.2** |
| **#24** | 两个调用方在为主动丢弃的增强特性付费 | ✅ **§666.3 复核：三个调用方判定逐个成立，且代码注释自带「§457 登记，**未修**」标记**。⭐ **§666.2：它与 #8 是同一处浪费的两面（后端丢一次、前端丢一次）⇒ 建议合并拍** | **§666.2 / §666.3** |
| **#27** | 默认本地路径上「参考资料」一个字都不说就没了 | 推荐 **A**（加事实键 + 第三种提示）；**B 已实测不可行**。⭐ 与 #51 同族 |
| **#28** | 死代码棘轮只覆盖 28 个 features 目录里的 1 个 | 扩面是范围口径。⚠ **§658.4：目录数已漂 28→32，覆盖比例变成 1/32** **§658.4** |
| **#46** | `silenceCut` 是空开关 | ⚠ 方向已被 **#50** 修正。⛔ **§658.2 钉死了「空在哪」：同名词跨了两个语义不同的层，请求里的值根本送不到结果字段** **§658.2** |
| **#55** | 收尾合并后**说话人编号跳号**要不要重编号 | ⭐⭐⭐ **§596 实测的新缺陷，且与 #54 三问完全无关**：`relabel()` 只摘死簇、**从不重编号** ⇒ 用户在真人语料上看到的清单是 `说话人 1 2 3 4 6 7 8 9`（**缺 5**）；改名场景更甚（缺 5、6）。⚠ **不涉及阈值/切段/特征** ⇒ 改它不必碰任何一个阈值。⚠ **不代拍**：①保持现状（跳号＝合并过的痕迹）②收尾重编号（⛔会让用户录音中看到的编号在收尾后改变，打破心理引用）③只在 UI 层重显示（展示与存储不一致）· ✅ **§658.3 复核：`relabelClusters`（`speaker-relabel.ts:193`）里一行重编号都没有，`canonicalOf` 存的就是簇 id ⇒ 本条登记准确** §596 / **§658.3** |

| **#37** | MCP 与「跨会议聚合」是共识级缺口 | ⭐ 本仓**说话人分离更强**；⚠ **§665.3：`grep 跨会议` 有 4 个命中但全是「防止跨会议串台」（§400 已修缺陷），不是聚合 ⇒ 自查时别被骗** | §529 / **§665.3** |
| **#38** | 声纹能力只有会议链有入口（`enrollFromAudio` 零 UI 入口） | ✅ **§659.2 复核：实现与 store 都在，`.vue` 里 0 命中 ⇒ 只差最后一跳** §530 / **§659.2** |
| **#40** | VAD 单段 >60s 音频被静默丢弃 | ✅ 甲/乙已落地（`vad-segmenter.ts:145/:187-194`），**丙未做**。⭐ §663.3：乙已把它变成**用户可见告警**，文案含「页面切后台/rAF 停摆」⇒ 顺带给 A3 留了判据 | **§663.3** |
| **#41** | 随手记解码工作量二次增长 | 安全时长 = 3 × 实时解码倍率；⛔ 缺各档设备倍率实测 |
| **#42** | 字幕文本该不该进最终正文 | ⭐ 已补齐：**不是二选一而是两者同场并存**（重复 34/35 · 覆盖 23/35）；已登记 `silenceMs 1200` 这个并入选项。⚠ **§669.1：它是 #20 的成因**（`buildUtteranceSegment` 复用「手工输入」函数 ⇒ 机器产出的字幕被标成 `manual`）⇒ **建议与 #20 合并拍** | **§669.1** |
| **#30 词源** / **#31** / **#26 兑现度** | 见 C 组 | — |
| **#5** | 行动项要不要有展示面 | 门 `summary-reference-cross-round.test.mjs` 已钉住边界 |
| **#6** | 手动「生成总结」要不要也能产出并落地行动项 | ✅ 三条机制读数**全部成立**（`meetings-ai.ts:46` 纯文本 · 行动项恒取 liveSummary · 单一调用点）。⚠ **§665.2：原「选 B/C 要改门禁」的成本已消失 —— 那条判据已被 §416 改掉** | §410 / **§665.2** |
| **#7** | 拿到 topic 后要不要自动更新会议标题 | ✅ **§668.1 复核成立**：`m.title ||` 短路仍在（`:276`，漂 30 行）⇒ `topic` 分支生产里仍走不到 | **§668.1** |
| **#9** | 跨端/多窗口的会议状态同步要不要接 | ⭐ **§663.1 措辞要改：WS 接得很完整**（单例/鉴权/基址/重连都就位），缺的是「会议」这一类**事件** ⇒ 增量成本远低于「接 WS」；**#10 是它的子集，先拍 #9** | **§663.1** | 
| **#10** | 录音收尾后列表要不要自动更新（**两条**链） | ⭐ **§668.2：机制已在 7 个域推广（20 处调用）⇒ 缺的只是这两条链没调用，增量成本与 tasks 侧同量级**；**先拍 #9 的口径**（跨设备那条路成本完全不同） | §409 / **§668.2** | §409 |
| **#11** | 会议详情页「总结」成功后的 toast 要不要报条数 | ⚠ **§664.2：提醒侧已报（§410 的选项 C 已达成），行动项条数仍不报 ⇒ A 仍待拍板，但成本从「6 处门」升到 **13 处**；`已生成当前总结` 生产侧已 0 命中 | §410.8 / **§664.2** |
| **#13** | 会议同步到云端**失败时一个字都不说** | ✅ **§664.1 复核三条读数全部成立**（`cloudSynced` 生产侧零消费者 · fail-closed 503 · 静默 catch）；⚠ 文件名订正：catch 在 `meeting-ingest.ts:144-146`，且**体完全为空**（错误对象都没留） | §413 / **§664.1** |
| **#16** | 随手记「重新总结」是**替换**还是**追加** | ✅ **§665.1 复核：三条读数逐字成立、零漂移 ⇒ 可直接拍**（A 维持精确匹配 / B 替换 / C 近似归并） | §147/§125.7.2/§420 / **§665.1** |
| **#23** | tag 门控的测试文件在 CI 覆盖空洞里 —— 要不要补一道**编译卡口** | ✅ **§659.1 实测：13 条能编译通过（风险不是编译错，是「从没被执行过」）；而文件头注释给的那条命令路径是错的，照抄会报 no such file** · ⭐⭐ **§676 已把两格补完**：① **`-race` 下 13/13 PASS、连跑 8 轮 0 红 0 DATA RACE**（原记的「-race 下未测」已关闭）；② **卡口该用什么命令已测出来 —— 七条命令的红绿矩阵显示：只有 `go vet -tags security ./security/...` 与 `go test -tags security ...` 有牙，⛔ `go build -tags security ./security/...` 是绿的**（`go build` 结构上不编译 `_test.go`，**加 tag 也不行**）⇒ ⛔ 剩下的只是「要不要让 CI 为 opt-in 包多花一次编译」这个口径决定 | §453 / §659.1 / **§676** |
| **#52** | ASR 换型先问**形态**再问价 | ⭐ 地板价 0.288 元/小时（百炼 Paraformer，静音不计费）**在本仓接不上**（异步 + 公网 URL）；接得上的是同厂 2.50 元/小时那档 | §573 |
| **#53** | 纪要条目要不要能**溯源**回原文段落 | ⭐ 讯飞「纪要溯源」与飞书「音字同步定位」**都是招牌功能**；**§662.1 复核：不是差一个字段，是正向半边完全不存在**（三类纪要产物全为字符串，全仓「纪要→段」0 处，而反向 8 处） | §576 / **§662.1** |
| **#54** | 真机校准 `SAME_SPEAKER_MIN = 0.99`（**或**改走官方两段式 diarization） | ⭐ 2026-07-07 那份文档**自列「第一优先」且至今未做**；§589 已确证：真机跑的是 Web log-mel 48 维（**不是** ECAPA），标定用的是**零环境噪声 TTS 干声** ⇒ 阈值无标定依据。⭐⭐ **§590.2.3 新增**：官方 sherpa 已有**两段式**（segmentation + embedding + FastClustering，且**支持已知簇数** `num-clusters` 与**中文声纹** 3D-Speaker），⇒ **候选解法从「重标一个阈值」变成「换形状」** | §589 / §590 |
| **#57** | `internal/agent` 的 `TestPiAdapter_SendPromptHappyPath` 是**负载敏感**用例，10 秒超时 | ⚠⭐⭐ **§605 实测**：同一份代码连跑两次全量，第 1 次红（`context deadline exceeded`，10.00s 正好是它自己的 ctx 上界）、第 2 次 **58 包全绿**；隔离跑 `-count=3` **3/3 通过（总 0.899s）** ⇒ 不是功能缺陷，是 `writeFakePi` 起的**真实子进程**在全量并行下启动超时。⚠ **为什么值得拍**：CI 跑的是 `go test -race ./...`，`-race` 让启动更慢 ⇒ **CI 触发概率只会更高**。⛔ **不代拍**（三选一）：①维持 10s、接受偶发红 ②调大上界（等于放松「adapter 不许挂死」这条契约）③把子进程启动与被测逻辑解耦（预热/复用 fake 进程，治本但改动面最大） | §605·**§621 证据升级** ⭐⭐⭐⭐⭐ |
| **#58** | 相邻两段**完全相同且 `40 < 长度 ≤ 56`** 时，Go 与 TS **行为分叉**（Go 保留两份完整 / TS **插入原文不存在的拼接字串**）| ⭐⭐⭐ **§608 真函数双跑实测**：Go 的 `mergeIncremental:288` 有「整段重复且超窗口 ⇒ 保留两份」这条分支、并由 `TestMergeIncrementalDoesNotDropRealContent` 显式钉住；TS 的 `dedupeSegmentText` **没有** ⇒ **前端会删掉用户真的说了两遍的内容**（正是该文件与 Go 注释都写明的「误去重的代价是丢掉真实内容，用户永远发现不了」）。⛔ **不代拍**（属产品口径）：①**TS 补齐 Go 那条护栏**（两侧保留两份 ⇒ 用户界面上同一段话会出现两次）②**维持现状**（前端裁一份、后端留两份 ⇒ 两条路径展示不一致，但前端少重复字）③**改 Go 对齐 TS**（⛔ 会推翻一条**已有的、带测试的**契约）。⭐ 已按同步门自己的原则把差异**登记成事实**（TS 门禁 §608 四条 + Go 侧门检查登记还在不在，删掉即红）|
| **#59** | `<dataDir>/fonts` 里有多款字体时只试字典序第一条 —— 部署方放的中文字体被当不存在 | ✅ **技术部分已自行落地**：`invoice_pdf.go` 改用 `pickCJKFont` 逐个试（M1/M2 双向变异）；⏳ 待拍两条口径：① 该目录全无中文字形时是否硬失败 ② 字体选择要不要打日志。⚠ **§681：那一行在测试里从未被执行过**，且顺带发现 2 条负控在 macOS 上恒 skip | §681 |
| **#60** | `loadCompanionOverlay` 用**第三条** dataDir 规则（硬编码 `/app/data`），与 `ResolveDataDir` 不同，且读不到时静默 | ⚠ 机制已证、**影响未测**：`companion.env` 全仓**零产出方**，不知道谁放它之前改哪边都是猜。⏳ 三条待拍：① 未设 `POCKET_DATA_DIR` 时要不要也试 `Dir(POCKET_DB_PATH)`（bootstrap 顺序约束）② 要不要加日志（与 #59② 同族，建议一起拍）③ §682 阴性结论要不要升级成人工巡检项。★ 与 §681 同族：不一致的一方恰好没注释，正确的一方因有注释显得更权威 | §682 |
| **#61** | 「每个 `MeetingSegment` 生产者都带齐 `startMs`/`endMs`」这个不变量**没有判据钉住** | ✅ **§683 实测**：它目前**恒成立**（四个生产者都带齐 + DB `end_ms INTEGER NOT NULL`）⇒ `dedupeSegmentText` 精确档在生产**不可达**、`dedupeSegmentTextSoft` 是**唯一**活路径（比 §634 的「默认」更强）。⏳ 三选一：① 不动只留注释 ② 加判据扫构造点 ③ 收紧 `dedupeSegments` 入参为必填（③ 会连防御分支一起废掉，与①②不是同一决策的三个强度） | §683 |
| **#62** | LCS 去重会把一段话切成**跨位置字串**（「的人」「今补」）—— **Go 与 TS 都有**，全仓零登记 | ⚠ **§685 实测**：两侧同一语料 32/80 vs 33/80，**命中率几乎一样 ⇒ 不是对齐问题，是这套算法的共同设计问题**。body 完整（没丢内容）、delta 短（没多显示），只是界面上那一行语义不通且不报错。⏳ 待拍：这种非连续切分该不该被算法禁止 ⛔ **合成语料读数，真实 ASR 未测** | §685.5 |
| **#63** | `config.getEnv` **不 trim** ⇒ 任何 `POCKET_*` 的纯空白值都被当已配置 | ✅ **§690 已修其下游一处**：`integration_status.go` 诊断页原报「网关已配了」而实际 key 为空（端到端实测 + M1 变异 + 3 条新判据）。⏳ 待拍：`getEnv`（`config.go:597-602`，**上百处调用**）要不要**全仓** trim —— 属行为变更。⚠ 注意此刻 ① `cfg.LLMGatewayAPIKey` 仍会是 `"  "`，只是诊断页不再说谎 | §690 |
| **#64** | `cmd/pocketd` 的 embed/chat 企业网关 **base** 要不要整体改调 `defaultLLMGatewayState().BaseURL` | ⏳ **§692 刻意只对齐空白口径**（`TrimSpace` + 空则回落默认），base 仍取 `cfg.LLMGatewayURL`。因为一并改调会把 `canonicalGatewayURL` 的 **obsolete(`127.0.0.1:8782`) 强制改写**带进 embed/chat —— 属「谁说了算」的产品口径。A 维持现状 / B 两类流量彻底同源 / C 把 obsolete 改写摘掉 | §692 |

#### ⭐⭐⭐⭐⭐ #58 的登记已被 §623 / §624 更正（触发条件、后果、暴露面三处）

* ⛔ **触发条件要加上长度上界**：不是「`c==n && len>40`」，而是 **`40 < len ≤ 56`**。
  定长探针每档 6 条、两侧各跑自己那份真函数：
  **41/44/48/52/55/56 → 各 6/6 全分叉**；**57/58/60/64 → 各 0/6 全一致**
  ⇒ 分界线精确落在 **56/57**。（自然整句 250 条与定长探针 60 条给出同一条线，互为佐证。）
* ⛔ **后果要更正、且更重**：不是「删掉用户真的说了两遍的内容」。
  36 条分叉里把 TS 输出减去第一份得到的尾部逐条比对原文：
  **「是原文连续片段」0/36，「跨位置拼接」36/36**。
  样本：原文 41 字，TS 追加的两个字是 `今里`（句首的「今」+ 句尾的「里」），
  **用户看到的是一句语义不通的话，且系统不报错、不留痕**。
* ⭐⭐⭐⭐ **§624 查清暴露面**：
  * **危险带 = 一段 7.5~16 秒的长话语**（41~56 字 ÷ 3.5~5.5 字/秒）。
    用仓库唯一的真人中文录音夹具（sherpa-onnx 四人 56.86s、官方 10 段）折算，
    **三档语速假设下都是 0/10 落入该带**（最长段 6.55s ≈ 29 字）。
    ⚠ 该夹具自带警告「单样本不足以支撑阈值结论」⇒ **本节不产出发生率**。
  * **读代码确认 `c` 是累积文本**（`mergeIncremental(i.committed, res.Text)`，committed 单调增长）
    ⇒ 触发需要「累积文本恰好只有那一段 + 新段逐字复读它」
    ⇒ **随手记 3 秒定长链基本够不到**（单段 ≈10~16 字）；暴露集中在**一段 9~12 秒的长话语被完整复读**。
  * ⭐⭐⭐⭐ **新登记一条结构性不对称（此前从未登记）**：
    **TS 有一道时间轴护栏，Go 完全没有** ——
    TS `dedupeStep` 在「有时间戳且判为不重叠」时改走 `dedupeSegmentTextSoft`
    （注释明写是为了不吃掉用户真重复说的话），
    而 **Go `mergeIncremental` 全文件搜不到 soft 变体、也没有时间轴分支** ⇒ Go 永远走精确策略。
    ⇒ ⛔ **§626 实测推翻了这一条**：带时间戳 + 正常静音**照样**产出胡话（6/6 全中）。元凶是 exact 与 soft **共用**的 `lcsNetNew`（窗口上限 `MAX_OVERLAP=40`，段长 41~56 时装不下整个相同串 ⇒ 对齐出偏 ⇒ 拼出胡话）；**数据形态完全不影响结果**，唯一条件仍是 `committed === next` 且 `41 ≤ 长度 ≤ 56`。
    ⛔⚠️ **本行曾在 §626 之后残留了一句「带时间戳且两段间有正常静音时，TS 会自动退让，不会产出拼接字串」——该句已被 §626 与 §634 两次推翻，已于 2026-10-14 删除。**
    ⇒ **§634 进一步证明「退让」这条路在生产里是默认路、而不是退路**（见下方 §634 块）：会议链的相邻段**时间轴不重叠** ⇒ `overlapByTiming` 恒 false ⇒ **恒走 soft 路**，而 soft 的第一步就是调那条共用元凶 `lcsNetNew`。
    ⇒ 并且 soft 的设计意图（不吃掉用户真重复说的话）**被时间轴条件挡住了**：soft 本身并不安全，**只要软路真的被走到，它照样出胡话**。
* ⛔ **真实发生率仍未测**（需真人录音 + 真 ASR 两样外部输入）—— **不代拍、不假装有答案**。
* ⚠ §623 我自己踩了两个坑并当场作废：`replace("的","了",1)` 在没有「的」的句子上是**空操作**；
  `zip(a[100:], a[100:])` 配成**同一元素** ⇒ 纪律：**统计前先打印样本、按实际属性重算**。
* ⭐⭐⭐⭐⭐ **§626 实测矩阵（60 条探针 × 3 种数据形态）**：
  段长 **41/44/48/52/55/56 → 三种形态各 6/6 全部产出胡话**；
  **57/58/60/64 → 各 0/6 干净**。
  ⇒ **「靠时间轴躲开」这条路已被实测排除**；三选一仍**不代拍**，但依据又硬了一层。
* ⭐⭐⭐⭐⭐ **纪律**：**「代码里有一道护栏」不等于「它覆盖了这个形状」** ——
  §624 我读 `useExact` 分支就断言「soft 会兜住」，而 soft 与 exact **共用** `lcsNetNew`。
  ⇒ 凡要写「A 情况会被 B 机制兜住」，**必须把那个具体形状喂进真函数跑一次**，否则标成「未验证的推断」。

#### ⭐⭐⭐⭐⭐ #58 的登记已被 **§634** 再次升级：从「函数级缺陷」到「生产默认路径 + 直连用户抱怨」

§634 把量具从「`dedupeSegmentText` / `dedupeSegmentTextSoft` 这两个函数 + 定长探针」
换成 **`planRefine` 真正调用的 `dedupeSegments` + 真实时间轴**。读数如下（**只登记，不代拍**）：

* ⭐⭐⭐⭐⭐ **先钉纯代码事实（这一步不需要实验；顺序不能反）**：
  `dedupeStep`（`meeting-dedup.ts:255-259`）的
  `overlapByTiming := hasTiming && likelyAudioOverlap(prevEndMs, startMs)`（判据：`nextStart < prevEnd`），
  而前端 `vad-segmenter.ts:246` 产出 `{ blob, startMs: speechStartMs, endMs }` ——
  **无 pre-roll、无人为重叠**（全文无 `overlap` / `preRoll` 字段），且下一段起点必然晚于上一段终点
  ⇒ **`overlapByTiming` 在正常会议录音里恒为 false ⇒ 会议链默认走 `dedupeSegmentTextSoft`**，
  而 soft 的第一件事就是调那条共用元凶 `lcsNetNew`
  ⇒ **#58 在生产路径上不是「理论可达」，是默认路径。**
  ⚠ 顺序纪律：**先跑实验会得出「exact 路有 bug」的错结论**（本节曾把实验排在代码事实之前，返工过一次）。
* ⭐⭐⭐⭐⭐ **触发区间被扩大**：按「重叠窗口内散落同音抖动」这个更接近 ASR 的形态，
  **36~64 字全段基本 2/2 复现**，与 §623 那个定长探针边界 `40 < len ≤ 56` **不一致**
  ⇒ ⚠ **按定长探针读出的「56/57 分界线」不适用于真实形态**；
  三种数据形态（`vad-no-overlap` / `no-timing` / `vad-overlap`）逐档读数完全一致。
* ⭐⭐⭐⭐⭐ **净增里约一半是拼接产物，且原样进提示词**：
  判据必须写成「**净增是 `next` 的连续子串**」，不能写成「每个字在输入里出现过」
  （后者太松，45/45 全 0 —— 这是本节第一次假零）。
  样本（len=36, drift=1）：
  输入 `壹然后逐玖过一遍万们先看认下这个上度的整叁情况然接逐项过认遍接下来请王强`
  ⇒ 输出 `壹玖万认上叁接认接下来请王强`，标出的 8 个字**从不连续位置拼出，原文里没有这个串**。
  ⇒ 经 `meetingsApi.refine` → Go `llmMeetingRefine` → `segmentsToText` → **`buildRefinePrompt`**（`server_meeting.go:1008`），
  这份文本**不做任何净化**地进入精校提示词。
* ⚠ **对三选一的影响**：**选项 ②「维持现状」= 默认接受每次精校的输入里都带着拼接产物**，
  代价改由 §35 那套「只改错、不改写」的提示词自己扛 —— 而**提示词是请求，不是保证**（同 §1054 纪律）。
  ⇒ 三选一的依据由此又硬了两层（① 走的是默认路，而不是少数派路径；② 「时间轴会兜住」这道保护不成立）。
* ⛔ **真实发生率仍未测**（本节未解除此 ⛔）：§634 的语料族是**合成**的
  （`next` = 对 `prev` 尾部一段的重转写 + 散落同音错字）。
  这是**一类真实可达**的形态（同一人停顿后重说尾句 / ASR 重复转写），**不是普遍形态**；
  `prev` 是 240 字**累积正文**而非单段（与 §624 登记一致）。
  ⚠ 「时间轴不重叠 + 文本重叠」这个组合本身**存在语义张力**（音频不共享却重转写同一段）——
  用它是因为它**可达**，不是因为它**常见**，真实占比未知。
  ⇒ **#58 真实发生率仍需一段真人会议录音 + 真 ASR 两样外部输入**（把「可达」变成「多常见」的唯一办法）。
* ⚠ **量具自纠 3 次**（记在这里防止重犯）：v1 判据太松（45/45 全 0）· v2 语料不触发
  （LCS 对齐成干净公共前缀 ⇒ 净增必是后缀）· v4 同一句 `repeat(12)` 的**自重复**造成虚高。
  ⇒ 纪律：**统计前先打印样本、按实际属性重算**（与 §623 那两条同源）。

#### ⛔ §743 / §744（2026-10-16）：**「解法不在常量层」已有三条实测支撑**

三选一**无一条直接针对 G≥2 的膨胀/残片**，§729 已指出并问「是否补第 ④ 选项」。
本节把那一格量化完了，结论对选项措辞有直接影响：

| 尝试 | 读数 | 判定 |
|---|---|---|
| **调大 `MAX_OVERLAP`** | 残片 7 → **13（不降反升）**，掏空 7 → **16** | ⛔ 两个指标同时变坏 |
| **调小 `MAX_OVERLAP`（→16）** | 残片/掏空归零，但**真实重复一条没去掉**（重复侧膨胀 33 = 全表最高） | ⛔ 等于**关掉去重** |
| **换参数组合**（`MAX_OVERLAP` × `ANCHOR_COVERAGE`，**24 组**） | 「不残片」的格子全部集中在 `ov ≤ 24`，而那一带去重能力恒为最差 | ⛔ **无一组能同时做到** |

⇒ **「残片」与「还在去重」在参数空间里互斥**，三条路都排除。

> ⛔⚠ **§745 口径修正 + 一个更硬的证据**（2026-10-16）：
> 上面那张表的语料是**30 字不重复段**，**不是 #58 的真实形状**
> （#58 = **相邻两段完全相同且长度 ∈ (40, 56]**，§725 定位）。
> 段长 30 < `MAX_OVERLAP` 40 ⇒ **窗口一开始就被段长夹住**，`MAX_OVERLAP` 当时甚至不是瓶颈。
>
> 用 **#58 真实形状**重做（36 组 `重复段长度 × MAX_OVERLAP`）后：
> - **没有任何一个 `MAX_OVERLAP` 能把拼接产物清零** ——
>   零散落在 `(42,48) (46,32) (46,48) (50,32) (56,32) (56,36)`，**每个取值只对部分长度为零**
> - ⛔ **「`MAX_OVERLAP=32` 能治 #58」是单语料假解法**（只在 `DUP=56` 那一条语料上为 0）
> - 但调大在 #58 语料上确实**单调变好**（40 → 7~8 处；**80 → 2 处**，四档一致最低），
>   而 §743/§744 的语料显示调大让**残片与掏空同时变坏**
>   ⇒ **两个判据方向相反、都归不了零** ⇒ 「解法不在常量层」**在 #58 本尊上被检验过**，结论更强

> ⛔⚠⚠ **§746（2026-10-16）把上面最后一句收回**：在 #58 真实形状上把**原文留存**一并量完之后，
> **调大 `MAX_OVERLAP` 的代价几乎为零**：
>
> | 重复段长度 | 现状 `40` | `80` | 变化 |
> |---|---|---|---|
> | 42 | 8 处 / 96.4% | **2 处 / 96.9%** | 产物 **−75%**，留存 +0.5 |
> | 46 | 7 处 / 97.7% | **2 处 / 97.4%** | 产物 −71%，留存 −0.3 |
> | 50 | 7 处 / 97.0% | **2 处 / 96.5%** | 产物 −71%，留存 −0.5 |
> | 56 | 7 处 / 97.1% | **2 处 / 96.6%** | 产物 −71%，留存 −0.5 |
>
> ⇒ **代价是语料相关的**：§743 语料（30 字不重复段）说「调大代价大」，
> 　 #58 语料（相邻两段完全相同、42–56 字）说「几乎为零」。
> ⇒ ⛔ **「解法不在常量层」不能写成全局结论**。
> ⇒ 📋 拍板时请把「调大到 80」看成
> 　**「一个成本很低、仍不彻底（仍有 2 处）、且会触发 Go↔TS 同步门的候选」**，
> 　而不再是「已被排除」。
> ⚠ 仍待外部输入：生产里这个重复形状**占多大比例**，以及
> 　**96.4%–97.7% 那个留存基准线本身是否可接受**（现有参数下就有 1–2% 探针消失，未定性）。
> 　这两项**合成语料给不出答案**。
>
> ⛔⛔ **§747（2026-10-16）把上面第二条也解决了，而且结论对属主有利**：
> 那 96.4%–97.7% **整列是探针伪影** —— 探针用固定 8 字步长，段边界不落在 8 的倍数上，
> 于是有一批探针系统性地**骑在段边界**上，输出按段重组后自然拼不出那 8 个字。
> **实测：12 条丢失全部跨边界，真丢 0 条**；再扫 8 个相位 × 8 个 G = **64 组真丢恒为 0**。
> 重测 16 格（`DUP` 42/56 × `ov` 24→120）**全部 0 真丢**。
>
> ⇒ ⭐ **在 #58 这个形状上，调大 `MAX_OVERLAP` 是「拼接产物 7–8 处 → 2 处，实测零真丢」**
> 　　⇒ 它应从「已被排除」升级为「**本形状上零代价的候选**」
> ⛔ 但**仍不是修好**：还有 2 处、归不了零，且没有任何取值能对所有重复长度归零
> ⚠ 代价证据**只剩一条**且来自另一种形状：§743 语料（30 字不重复段）上
> 　调大让**逐段变短**的段数 0→16 —— 那条**依然成立**（它用逐段长度对照，不是探针子串测试），
> 　但那是**另一种语料**，占比未知
> ⇒ **决定性输入仍然是真人录音**：两种语料形状在生产里**各占多少**

> ⛔⛔⛔ **§748（2026-10-16）把上面这条代价证据也撤掉了**：那条「代价」量的是
> **逐段变短**，而 `dedupeSegmentTextSoft` 的本职就是去掉重叠
> ⇒ **段变短往往是「去重做对了」，不是「内容丢失」**。
> 用 §747 的修正判据（8 相位 + 排除跨段边界）重测同一语料，每个配置 **2208 个探针**：
>
> | `MAX_OVERLAP` | 24 | **40（现状）** | 56 | 80 | 120 |
> |---|---|---|---|---|---|
> | 真丢原文 | 0 | **0** | **3 / 6** | 0 | 0 |
> | 逐段变短（旧指标） | 11 | 7 | 16 | 13 | 16 |
>
> ⇒ **`ov=120` 变短 16 段而真丢 0** —— 两个指标几乎不相关
> ⇒ ⚠ **`MAX_OVERLAP=56` 是个陷阱**：那档有实测的真实内容丢失
> 　（一处约 10 字的连续区域，三个重叠探针命中同一处）
>
> ⭐ **因此「调大 `MAX_OVERLAP`」现在是证据最厚的候选**：
> 　拼接产物 **7–8 处 → 2 处**，真丢 **0**（两种语料形状、2208+ 探针量级）
> ⛔ 仍有三条：① 归不了零（仍 2 处，且无取值能对所有重复长度归零）
> 　② **别选 56** ③ 触发 Go↔TS 同步门，Go 侧要同步改
>
> ⛔⛔⛔ **§750（2026-10-16）第三次修措辞 —— 上面那个「真丢 0」是恒真判据量出来的**：
> 回执 = 各段拼接 ⇒ 原文天然在输出里。**阳性对照**：原文 6-gram 在输出的命中率
> **98.3%–100%** ⇒ 「探针还在」近乎恒真 ⇒ **那一版的「零代价」作废**。
> 换成**有分辨力**的判据（每段内容唯一 + 段级存在性，每配置 96 次段观测）：
>
> | `MAX_OVERLAP` | 24 | **40（现状）** | 56 | 80 | 120 |
> |---|---|---|---|---|---|
> | 整段丢失（无重复段语料） | 0 | **0** | 0 | 0 | **8** |
> | 整段丢失（**#58 形状**） | 0 | **0** | **2** | **2** | 1 |
> | #58 拼接产物（§745） | 7–9 | **7–8** | 3–8 | **2** | 2–5 |
>
> ⇒ ⭐ **现状 `40` 是唯一「两种语料都 0 整段丢失」的取值**
> ⇒ ⛔ **`ov=80` 在 #58 形状上真的丢 2 段** ⇒ 「零代价候选」**再次撤回**
> ⇒ 📋 「调大 `MAX_OVERLAP`」是**真实取舍**：
> 　**拼接产物少七成（7–8 → 2），代价是 96 次段观测里丢 2 段真实内容**
> ⇒ ⚠ 旧结论仍成立：**没有任何取值能让拼接产物归零**；改它触发 Go↔TS 同步门
> ⇒ **决定性输入仍然是真人录音**：这个取舍对用户**值不值**
>
> 📌 **这条量具翻了三次账，记账在此**：§743（代价大，判据错）→
> §747/§748（代价≈0，判据空转）→ §750（代价真实，判据有分辨力）。
> 打掉空转判据的是**阳性对照**，另外还靠**语料预检**抓到我自己的切片越界 bug
> （段长 `30,30,30,5,2,2,2,…`）。
> ⇒ **纪律**：指标被推翻两次以上时，先证明新指标**有分辨力**，再让它进结论。

> ⛔⛔⛔⛔ **§752（2026-10-16）第四次修措辞 —— 这次不选边，因为两个都合理的判据给出相反排序**：
> 上面 §750 那个「严格」判据（整段逐字出现）在真实 ASR 下**必然误报**：
> 回执是**再转写**，一个逗号差异就让整段不再逐字出现 ⇒ 实测 `punct` 噪声下报 **36/96**「丢失」，
> 而那些不是丢失。改用两个**抗噪**判据（每格 368 个 4-gram 探针、96 次段观测）：
>
> | 判据 | 定义 |
> |---|---|
> | **凭空捏造** | 输出段文本**既不在原段、也不在任何回执里** |
> | **内容留存** | 每个原段的 4-gram 有多少比例出现在输出里（不要求逐字连续） |
>
> | | `MAX_OVERLAP=40`（现状） | `MAX_OVERLAP=80` |
> |---|---|---|
> | 捏造（**#58 形状**） | 11–12 | **4–8（约减半）** |
> | 内容留存 | 100.0% / 99.8–99.9% / 99.2–99.3% / 100.0% | 100.0% / 99.7–99.8% / 99.2% / 100.0% |
>
> ⇒ ⭐ **内容留存在两个取值上几乎相同（≥99.2%），噪声加大也不掉**
> ⇒ ⛔ **但两个判据排序相反**：
> 　**严格**判据说 `ov=40` 更好（丢 0 vs 丢 2）· **宽松**判据说 `ov=80` 更好（留存相同、捏造减半）
> ⇒ ⇒ 差别全在：**被 `ov=80` 重组掉的段，其 4-gram 几乎全在输出里，只是不再逐字连续**
> ⇒ ⇒ ⛔ **「重组但内容都在」算不算「丢失」，是产品口径，合成语料判不了** —— 我**不选边**
>
> 📋 **给属主的最终措辞（⛔ 不代拍）**：
> - `MAX_OVERLAP` 40 → 80：凭空捏造 **11–12 → 4–8**，内容留存**两者都 ≥99.2%**
> - ❓ **需要你定口径**：输出被**重组但内容都在**（4-gram 覆盖 99%+），在用户眼里算「保留」还是算「丢」？
> 　**算保留 ⇒ `ov=80` 是改进，建议采纳；算丢 ⇒ 维持 `40`**
> - ⚠ 两者共有：**捏造归不了零**（仍 4–8 段）· 改它**触发 Go↔TS 同步门**
> - ⇒ 加上真人录音可量化「重组占比」；若重组占比高，第一种口径更站得住
> ⚠ 边界：噪声是**确定性注入**的（不是真实 ASR 分布）· 只测了 `ov` 40/80 两点，`56`/`120` 在噪声下**未测**

> ⛔⛔⛔ **§759 / §760（2026-10-16）两条修正：上面那张表的口径要重读**
>
> **① ⭐ 「重组」在界面上是看得见的，而且现状档更难看**：
> 「4-gram 覆盖 99.2%」是**跨段**口径（原文的 4-gram 在**整条输出**里找）。
> 改成**逐段**口径（不许别段补偿），同一批输出读数完全反过来：
>
> | 口径 | 现状 `40` | 候选 `80` |
> |---|---|---|
> | 跨段留存（§752 用的） | 100.0% | 100.0% |
> | **逐段留存** | **94.9%**（#58 那一段仅 **38.3%**） | **100.0%** |
>
> ⇒ 那个 #58 形状的段**在现状下从 50 字变成 20 字**；用户读完全文内容确实都在
> 　（前一整段原样输出把它补回来了），**但列表里第二行只剩 38%**。
> ⇒ 📌 **给你的问题因此换一个说法**（比「重组算不算丢」贴近体验）：
> 　 **「转写列表里，相邻重复段的第二行只显示前半段 —— 用户会不会觉得漏字？」**
> 　 会 ⇒ `ov=80` 是改进 · 不会 ⇒ 现状可接受。
> ⇒ ⛔ **⚠️ 但 §752 的 99.2% 那个数字本身没错**（在「读完全文不丢」这个语义下是对的），
> 　 错的是它让人以为两档没差别 —— 它没有覆盖界面上真正可见的那个量。
>
> **② ⭐⭐ #58 现象在两条路径上**都**成立 ⇒ 你的三选一前提不变**：
> 实测「未重转」（生产里 `adopt=false` 那条）与「已重转回执归因」两条路径，
> 段文本**逐字相同**（同音纠正 `rosterFixes = 0`）。
>
> **③ ⭐⭐⭐ 但「发生率」必须报三件套，而第三件的真机值从未测过**：
>
> | 维度 | 现状 |
> |---|---|
> ① 路径（未重转 / 已重转归因） | 两条都实测过，现象一致 ✅ |
> ② 语料形状（相邻两段相同、42–56 字） | 已是真实形状 ✅ |
> ③ **`G` = 回执分块大小** | ⛔ **真机值未测** |
>
> ⇒ ③ 为什么关键：合成语料上实测 **`G` 在 1..8 之间摆动时，
> 　 被截短的段落留存率在 38% ↔ 100% 之间摆**（同一份语料，被截短的段还会「换人」），
> 　 且粗回执能让**单段膨胀到 2.56 倍**（128 字 = 2 份自己 + 2 份邻居，**零捏造**）。
> ⇒ ⛔ **§752 把 `G × 噪声` 汇总成「96 次段观测」的单一数字，恰好把这个摆动抹平了。**
>
> **④ ⛔ 另有一条你该知道的路径可达性问题**（不影响现象，影响「它多常见」）：
> §752/§746/§750 的读数全部建立在「已重转回执归因」路径上，而 §50 有现成的一手证据：
> **webm → 网关 400 → 整段重转失败 → `adopt=false`**，「整段重转这一整块从未在真机跑通过」；
> §50.3 修好了转码，但 §50.6 明写「**没有串起来在真机跑一遍**」⇒ **现状未测**。
> ⇒ 我**不**断言它今天仍不可达（缺 GATEWAY key 与真机录音，测不了）。
> ⚠ 另：`meeting-refine-plan.ts` 里那句「§50 对真 webm 的实测形态」**在 §50 原文里找不到出处**
> 　（§52.3 反而把「只有 1 条回执」当门构造的退化用例）⇒ 已如实登记，不代拍。

⚠ **还有一条成本信息**：`ANCHOR_COVERAGE` 与 `MAX_OVERLAP` 一样是 **Go/TS 共享阈值**
　（Go `anchorCoverage = 0.6`），改它会**同时**触发 Go↔TS 同步门 ⇒ 动它比动 `MAX_OVERLAP` 成本更高。

⚠ **量具自纠 1 次**：网格上 `(cov=0.7, ov=24)` 的**合计**看起来是帕累托改进
　（残片 7→0、掏空 7=7、去重 23=23），**逐档一拉不是** ——
　合计相同但坏在完全不同的 G 上（现状坏在 G=4/6/8，候选坏在 G=2）。
　⇒ **调参结论必须逐档看分布，合计只用来筛候选。**

⚠ 两条语料都是**合成**的，只刻画「函数在参数空间的行为」，**不测生产发生率**；
　网格是离散取点，真最优可能落在取点之间。

---

⚠ **本文件的读数只在 2026-10-11 成立**；主文档现已 **7 万余行**（⚠ 刻意不写精确行号 —— 精确行号就是 §564 记的那种会腐化的指针）。
⭐ **阴性条目已逐条标注适用链** —— §463/§464 的教训：没标链的阴性会劝错人。
✅ **计数口径**（⚠ 只有**分组表表头的件数**由门断言；下面这行**自由文本不在门的覆盖范围内**，改时必须自己数一遍）：本页编号覆盖 **#4–#64**；
　 分组件数 **A 6 / B 5 / C 20 / D 37**（即各组**表头声明的件数**）。A 组 6 件**全部是外部输入**（不是编号项），另计。
　⛔ **§681 顺带订正**：此行此前写「A 4 / B 5 / C 20 / D 29」，而表头声明的其实是 **A 6 / D 31** —— **自由文本与表头当时就已经对不上**，正因为这道门只断言表头、不断言这行，所以它一直漂着没人看见。本次随 #59 入册一并按表头重数。
　 ★ 判据：`frontend/src/api/__tests__/pending-brief-index-coverage.test.mjs`
　 （**门数不变，仍 45** —— 测试文件由 `COVERAGE_GLOBS` 自动收录）。三条判据：
　 ① 正文有独立条目的编号 ⇒ **必须**登记进本页；② 本页出现的编号 ⇒ **必须**有正文条目
　 （少数只有一行摘要的走 `NO_BODY_ENTRY`，**逐项写理由且上限 5 条**，不许变垃圾桶）；
　 ③ 每张分组表**表头声明的件数 == 该表实际行数**。三条各配一条负控。
　 ⛔ **§574 实测：在这道门出现之前，上面那句「覆盖 #4–#51 共 48 项」三处都不成立** ——
　 **#4** 有正文条目却不在本页（清单声称覆盖 #4，实际最小编号是 #5）；
　 **#31 / #32 / #33** 在本页有行却**正文没有任何条目**；
　 **D 组表头写「（20 件）」而实测 25 行**。
　 ⇒ ⭐ **「有几条」「覆盖到哪」这类东西，只有变成断言才守得住** ——
　 它们从没被写成断言，只被当作共识传下去，而每传一次都显得更可信。
## 〇、本轮（主文档 §437–§479）新增速览 —— 只关心「这轮查出什么」的话看这一页

围绕用户那句「录音转写的不太准确」，从去重算法一路挖到跨端原生层。

### 🔓 **一次就能解开的阻塞**（§467 把「网关凭据本机不存在」精确到了 4 个 env）

复核结果：这条阻塞**是真的**，不是过期的。`backend/.env` 只有 3 个 `IDENTITY_*` 键，
而网关凭据来自 env。照抄即可：

```bash
export POCKET_LIVE_GATEWAY=1
export POCKET_LLM_GATEWAY_URL=<网关地址>
export POCKET_LLM_GATEWAY_API_KEY=<网关密钥>
export POCKET_LIVE_ASR_MODEL=<例如 mimo-v2.5-asr>
export POCKET_LIVE_ASR_AUDIO=/abs/path/to/16k.wav     # ⚠ 仓内 0 个音频文件，须自备
go test ./internal/stt/ -run 'Live|Probe' -count=1 -v
```

⇒ 这一步同时解掉：**6 个 `live_asr_*` 探针 · 真实 CER · LLM 行为验证 · #24 的修复验证**。

⛔ **顺带查出一件此前没登记的事**（§467.2）：`internal/stt` 逐条读数是
**PASS 114 / SKIP 9**，而 **`go test` 报 `ok`** ——
那 8 条真网关探针**一次都没跑过**，且**不产生任何红灯**。
⚠ 与 §453（13 条安全测试**从未编译**）同族：**绿灯完全掩盖了「这段压根没跑」**。
✅ 已补台账门（`live_probe_skip_ledger_test.go`，3/3，跳过必须登记且不多不少）。

⚠⚠⚠ **§575 更正：这条「照抄即可」不成立，缺的不止那 4 个 env。** 2026-10-14 实测：

| 事实 | 读数 |
|---|---|
| 那几个 key 的 env | **全部不存在**（逐个查，`POCKET_LIVE_GATEWAY` 那一组同样没有） |
| 本机 8782 | ~~**closed**~~ ⭐ **§674 已变**：现在是 **`gateway` pid 51840 在跑**（`llm-gateway-go/bin/2.5.8.2639`，起于 13:10:38），`/health` 回 **401 `missing_key`** |
| PG（5432） | ~~未起~~ ⭐ **§674 已变**：**在监听且是真 Postgres**（`psql` 回 `password authentication failed` = 服务端活着，只差凭据）；⚠ **但「PG 可达」≠「网关已连上 PG」** —— 网关现在被 401 挡在最前面，**这一项未测到** |
| 8080 上跑的是什么 | ⛔ **不是网关，是本仓 `pocketd` 自己**（`/tmp/opstt/pocketd-cur`，`strings` 里有 3929 处 `halfking/pocket-opencode`），它把 `/version` 报成 `module: llm-gateway-go` |
| ⭐ 那个进程的年龄 | 编译 **2026-10-08 11:27**、起于 **11:28**，**已连跑 6 天**；而工作树有 **341 项**未提交 |
| 它能不能当网关用 | **不能**：`/v1/models` → **503 `database_unavailable`**（带假 key 同样 503） |

⇒ 上面那 5 行 `export` 只能解掉第 2 项。解锁实际需要**三件**：

1. **把 PG 起起来** —— `database_unavailable`，模型目录都读不出来；
2. **注入 `POCKET_LLM_GATEWAY_API_KEY`** —— ⛔ **仓库没有内置回落**
   （`config_writer.go` 的权威注释写的是反面：2026-10-01 真发生过租户密钥进 git 历史且**不可撤回**的事故，
   `DefaultLLMGatewayAPIKey` 已被删除；回归门 `llm_gateway_default_init_test.go` 断言「不配 env 时 APIKey 必须为空」）；
3. ⭐ **重编并重启 `pocketd`** —— 现有进程是 6 天前的构建，**前两条即使都满足，量具本身也是过期的**。

⛔ **两件凭据是独立的，别混成一句「缺网关凭据」**：
　**#49 / §556 / #24 的探针要本网关 key**；
　而 **§573 的「语料语音占比实测」要的是阿里云百炼 key**（`DASHSCOPE_API_KEY`）——
　且它那条路是**异步 + 公网 URL**（§573.4）⇒ **本机若不在公网后面，连「能不能跑」都还没验过**，
　⇒ 先验可达性，再谈测量。

⚠ 本节只做了只读探测：**未改代码、未加门、未起服务、未杀进程**。

### 🆕🆕 又一条**阴性的范围**没写（§464）：随手记上「即时总结 + 参考资料 + 建议」是 **0/3**

上一条「已实现」的阴性（§454）**只对会议/会话链成立**，对随手记链完全不成立：
录音中无即时总结、录完后无 `references`、无 recommendations。⇒ 新增 **#26**。
⚠ 这一次错的是**我自己上一轮写的阴性** —— 与 §463 同族：
**阴性必须把「它在哪条链上成立」写进那句话里**，否则会被跨链清点当成全局事实。

⛔ **§463 更正**：本节此前第一句写的是「随手记是另一条独立链，不适用」。
**两个前提都错了**（详见本文件 §448 节的更正框与 #15 的 B 格）：
随手记的实现是 `NoteRecorderRuntime`（**不是** `useVoiceRecording.ts`，那个文件零消费者），
而它是 **3 秒定长硬切逐片转写**（**不是**「整段一次性」）。
⇒ 「#18/#20/#21 不适用于随手记」这个**结论仍然成立**，但**理由完全不同**，
详见 §448 节的对照表。**请以对照表为准，不要回读旧原文。**

### 🆕 本轮（§463）新增：随手记的「段合并层」**在形态上就不存在**

用户需求第一条是「**将不同的段放在一起进行校对合并**」。随手记这条链上，
这一层**不是质量差，是没有**：

| 事实 | 位置 |
|---|---|
| 兜底全量转写**只在「切片一个字都没出」时才跑** | `recordingRuntime.ts:1197` |
| 只要**任意一片**出过字，此后该条件**恒为假** | `:1080-1082` |
| 「出过一次字」≠「出全了」（真机 16 片里 8 片带 error 但同时带 text） | `:1069-1079` |
| ⭐ **全量重转的代码本身早已写好**，只是被这个条件挡着 | `:1218 transcribeFull(…)`，带 abort |

⇒ 已拆成 **#15 的 B 格**（纯 ASR 二次消费，不涉及 LLM，**不依赖**「名字从哪来」那个前提）。
⚠ **选项 D 的写法会让人误判成本**（它把「整段重转」说成要新建一条链，实际是改一个条件）。

### 两条**用户一次真机操作就能定**的（验证成本最低，优先级最高）

| | 预测的现象 | 一次操作 |
|---|---|---|
| **#21** 两个 ASR 引擎同时写同一张段表 | **每次开 App 录的第一场整句重复，之后不重复**（§458 已把机制**代码坐实**：`startLiveCaption()` 在两个分支之外无条件执行，且 `commit → appendText` 与另两个 `processSegment` 同一个出口）。⚠ **§671.2 复核：机制仍成立**（行号已漂移，函数名不变）；⚠ **§671.6：这条签名只在「字幕路存在」的平台上可验**，平台归属本身已降级为「未测到」 | 录「开场 + 连续几分钟」 |
| **#22** iOS 三个 Swift 插件未注册 | **iOS 切后台断录**（Capacitor 8 要求 `CAPPlugin & CAPBridgedPlugin`，本仓三个插件只继承 `CAPPlugin`） | 录着切后台 |

⭐⭐ **真机前先跑这一行，成本接近零**（§458.3）：

```js
// ⚠ §671.3：这行原本写的是 typeof window.webkitSpeechRecognition —— 查少了名字，已改成守卫真正读的那一组
typeof (window.SpeechRecognition || window.webkitSpeechRecognition)
// 'undefined' ⇒ 字幕这条路在**本平台**根本不存在 ⇒ 本平台**不是** #21 的中招平台
// 'object'/'function' ⇒ 守卫**不会**早退，字幕**会**被启动；此时 #21 是否真的发生**还要看 Q2**
```

⛔ **§671 收回原行的两个判读**（原文写的是「能一次定掉 #21 的平台归属」）：
① **查的名字比守卫少一个** —— 守卫是 `SpeechRecognition || webkitSpeechRecognition`
　（`frontend/src/features/meetings/meeting-live-caption.ts:27`），
　**先查无前缀的**；现代 Chromium 暴露的正是无前缀那个 ⇒ 原行会对「代码明明会启动字幕」的机器读出 `undefined`。
② **结论方向反了** —— 原文写「`undefined` ⇒ #21 是 **Android 专属**」。
　否定 iOS **推不出**归属 Android：Android 单路的唯一依据就是「Android 没有该 API」，
　而这条依据本节已被 MDN 兼容表**证伪**（见下）。⇒ 那是「否定一方就断言另一方」，
　而这两方**从未构成全集**（Web/桌面被漏在题干里）。

⚠ **必须分成两问**，只问 Q1 会把「API 在但打不通」误判成「不存在」：

| | 问什么 | 决定什么 |
|---|---|---|
| **Q1** | 上面那行 `typeof` | 守卫**是否早退** |
| **Q2** | 字幕是否真的 `commit` 出段（录音时看段表有没有新增 `origin:'manual'` 段） | **#21 是否发生** |

⚠ **「Android WebView 没有该 API」这条前提已被证伪**：MDN 兼容表
（`SpeechRecognition` 的 `error` / `abort` / `speechend` 等页读数一致）记的是
**`WebView Android` 自 4.4.3 起 Full support**，altname 列明确含 `webkitSpeechRecognition`。
⇒ 原表括注「（Chrome 专有 API）」**自相矛盾** —— `webkit` 前缀是 Safari/WebKit 的前缀，
　Chrome 用的是**无前缀**的 `SpeechRecognition`。
⚠ 但**不要据此翻案**：MDN 说的是 **API 存在**；而 Chromium 系 WebView 缺 Google Speech API key 时
　会在运行期报 `service-not-allowed` ⇒ **Q1 与 Q2 必须都问**。

⚠ 另注：LambdaTest（2026-04-30）/ cobaltcapture 称 iOS 自 Safari 14.5 起**支持**；
另有多篇中文技术问答引 **WebKit Bug #170773「Not planned」**称 iOS Safari **恒为 `undefined`**。
⇒ 两种情形下**代码都安全**（`if (!Rec) return` 是防御式），错的只是**范围判断**。

⛔ **§671.6 结论：`#21` 的平台归属三格全部登记为「未测到」**（Android / iOS / Web-桌面）。
　但 **`#21` 的机制与 A/B/C/D 选项表不受影响** —— 它只依赖
　`startLiveCaption()` 在两个分支之外无条件执行、且没有「已有段生产者就跳过」的守卫，
　**这两条与平台无关**。

### 三条已充分证实的缺陷（等拍板修法）

| | 缺陷 | 证据强度 |
|---|---|---|
| **#18** | 前端去重缺第三道闸 ⇒ 短段丢字重排（真会议里李娜说「没问题」渲染成「没」，并送进 LLM） | ⭐⭐⭐ 2706 条语料双跑（**真 Go 当基准**），19 条分叉**前端 0 条正确**；生产形状与 VAD 参数完全重合 |
| **#20** | 字幕段 `origin:'manual'` ⇒ 一旦重复触发，**永久落库 + 默认展示 + 流进 LLM** | ⭐⭐ 后果链已证；**触发条件未证**（需真机） |
| **#21/#22** | 见上 | ⭐⭐ 结构已证，现象待真机 |

### 两条被**推翻/收回**的（避免照着错结论决策）

- **§444「iOS 整场 0 个 asr 段」已作废**（§446）：错在**假设**插件已注册；
  实际是 iOS 回落到 VAD ⇒ **iOS 反而是双写最严重的平台**。
- **§439「同一 skill id 两套契约」不是新发现**：仓库早有门钉着，还带两条负控。

### 五条**阴性**（省下的是重复劳动）—— 每条都标了**适用链**

⚠ **标适用链不是形式**：§463 那条阴性正因为没标链，才被跨链清点套到了随手记上（见上一条）。

- ~~随手记**结构上不存在**切片重叠重复（整段一次性转写）—— 其唯一实质缺口是 **#15 零精校**。~~
  ⛔ **本条已作废（§463，2026-10-11）：两处都错。**
  随手记是 **3 秒定长硬切 + 云端逐片转写**（`recordingRuntime.ts:704/962`），
  **不是**整段一次性；切片重叠**存在**（硬切必然重叠），
  是被**服务端** `incremental.go:222 mergeIncremental` 去掉的，不是「结构上不存在」。
  ⇒ 正确结论是「**去重在服务端**，不是「这里没有质量风险」」——
  而由它推导出的「唯一实质缺口是 #15」**同样不成立**：
  还有一个独立缺口「**整段校对层不存在**」，已拆成 #15 的 B 格。
  ★ 留在这里而不是删掉，是因为**这条阴性条目本身是需要被审计的产物**：
  阴性条目的作用是劝人别查，错的阴性比错的缺陷更贵。
- 「本仓缺一个假模型模式来测 LLM 链」是伪缺口：`httptest` 已在 **134 个**后端测试文件里。
  —— **适用链：全仓**。
- `#19` 风险模型：origin/main **没坏**（`go build` / `go vet` / 1611 条前端 import 全通过），
  真风险是「**119 已改 + 182 未跟踪只存在于工作树**」。
  —— **适用链：全仓**。
- 🆕 **「时间点自动加入计划日程」在随手记链上也已覆盖**（§465.1）。
  随手记**复用**会议的 `ensureTodoReminder`（`note-todo-persist.ts:20` 直接 import），
  不是自己造了一套 ⇒ 「同一句话得到两个提醒时刻」结构上不会发生。
  —— **适用链：会议 + 随手记（需求第四句）**。
  ⚠✅ 顺带数清提醒写入面共 **5** 处，**自动生产的只有 1 处**
  （`meeting-due-reminder.ts:24`），其余是用户显式动作或配置下发；
  「生产点有几个」这条已由 **§466** 的门补上（9/9，2 种外部变异均转红）——
  守的是更值钱的那个：**三条链各自的 `source` 标识全覆盖且互不串用**。
- 🆕 **「智能体回落是静默假成功」不成立**（§465.2）。
  `MeetingInsightPanel.vue:91` 的条件 `summary.agent !== true` 精确区分了
  「agent 跑过但你的笔记里没有」与「已回落到一次性摘要」，
  且服务端只在**确认 agent 产出结果时**才置 `agent=true`，回落路径从不置。
  ⇒ 用户需求「这个需要有一个智能体来完成这些」在不可用时**会被明确告知**，
  **不会**被谎报成「没检索到相关资料」。—— **适用链：会议/会话的 `/summary`**。

### 附带查出的一条**基础设施**缺口（与录音需求无关，但可立刻处理）

审计 #19 时顺手把本地 Go 验证补上了 —— **这是这棵 120 个已改文件的工作树第一次有 Go 测试读数**：
照抄 CI 口径 `go test ./... -race -count=1`，**57 个包全过、0 FAIL**。

顺带在 build tag 上撞见 **#23**：`backend/security/` 里 **13 条跨租户安全测试从未编译过**
（第 282 行用了不在依赖里的 `require`），**且修好编译后也一次都没测到过东西**
（`http://` 而非 `ws://` ⇒ 请求根本没离开客户端；且没带认证头）。
⚠ **三道关卡全漏**：gofmt 只判格式、`go build` 不编译 `_test.go`、`go test` 不传 tag。
✅ **两处已自行修好，该套件 13/13 全绿**；✅ **生产代码经实测是正确的**（401/403/101 三行读数）。
⛔ 刻意**没**给 CI 加 tag（该文件自述是 opt-in 设计）⇒ 是否补卡口列入 #23 待拍。

### 又一条**阴性**：「给出参考资料与建议」这条需求**已经实现了**（§454）

⚠⚠ **§464 更正范围**：本条阴性**只对「会议 / 会话录音」链成立**，
**对随手记链完全不成立**（随手记的 `summarize` 响应体里**没有** `references`，
也没有 recommendations；且录音中根本没有即时总结）⇒ 已拆出 **#26**。
⚠ 此前本条**没写适用范围**，因而在文档层面被读成了全局结论 —— 那是本条真正的缺陷，
不是结论错。**凡阴性，都必须把「它在哪条链上成立」写进那句话里。**

用户原话里这半句此前没单独核过。核完的结论是**别再查了（限会议/会话链）**：
两条载体都在（references 点开自己的笔记 / recommendations 点开 Bing 搜索）、
两条降级提示都在、三道门都在。

我在这条上**连错两次**并当场推翻：
先以为「回落路径会让参考资料静默消失」⇒ 实际 §109 早有降级提示且判据恰好命中；
再以为「recommend 只是搜索词、点不动」⇒ 实际后端当场拼了 URL、前端 `window.open`。

⇒ 剩下的**唯一待拍点**：recommend 给的是**搜索关键词**（点开是搜索结果页），
不是**已检索好的资料**；要不要改成真检索涉及外部搜索 API 与计费 ⇒ 并入 #12/#14。

### #17 的形状也变了：不是「选一个数」，是「选一条策略」

⚠ §449/§450：**30 分钟会议（4,500–8,100 字）就已达到甚至超过 agent 那条 4000 的上限**；
60 分钟是它的 2.3–4 倍，120 分钟 = **18,000–32,400 字**。
若照抄 agent 的**取尾部**写法，用户会拿到一份**从第 40 分钟开始、且不提示前面没了**的「精校稿」。

**#17 现在有五个侧面，全部要一起拍**：

1. **窗口占比**（0.5？0.3？）—— 依据「120 分钟 = 18,000–32,400 字」；
2. **取法**：滚动链取尾；**一次性摘要与精校取「头 + 尾两段」**（§449）；
3. **两条路径同管**：`/summary` 的 agent 路径截 4000、回落路径**完全不截**（§451）
   —— 只管一条会得到「agent 正常时长好、agent 一挂时长立刻崩」；
4. **超限时对用户说什么**（§452：滚动摘要的 `action_items` **会逐轮遗忘**，
   但日历主路径吃的是精校的整场数据，**不受影响**；受影响的是「精校失败时的补救按钮」）；
5. **`prev` 只带散文、不带 `action_items`** —— 三个修法候选都需真网关 >30 分钟的会才能验（§452）。

⛔ 顺带两条**阴性**：`prev_summary` **是接好的**（`useLiveSummary.ts:72` 确实传了）；
尾部窗口**本身不是静默降级** —— 滚动结果靠 `prev` 累积。

---

## 一、按阻塞程度排序（先拍前三项就能解掉一半）

### 🔴 #6 手动「生成总结」要不要也能产出并落地行动项

**现状读数**：`meetings-ai.ts:46` `summarizeMeeting` 返回 `chunks.join('')`（**纯文本**），
其 system prompt 明确要求模型输出「**行动项（负责人/截止时间若能识别）**」；
而 `MeetingDetailView.vue:316-318` 读的是 `source?.actionItems`——**另一轮滚动摘要的结构化字段**。

⇒ 本轮总结里的行动项**一条都不提取**。走的是滚动摘要的 `action_items`。

**死循环证据**：`onSummarize` 不调 `refresh()`，而 `refresh` 唯一触发点是
`useLiveSummary.ts:106` `watch(segments, …)`；点击不改 segments ⇒
`source` 恒为 null ⇒ 同一句提示重复出现，且每次重新调一次 LLM。

**⚠ 拍板材料必读**：既有门禁 `__tests__/meeting-action-items-source.test.mjs:116` 断言
`buildNoActionItemsNote(false)` **必须**含「再点一次|稍后|重试」。
选 B 或 C 时**必须同时改这条判据**，否则门会红。

| 选项 | 内容 | 代价 |
|---|---|---|
| A | 只改措辞：说明「行动项要等实时总结」 | 用户仍拿不到行动项 |
| B | 手动总结产出结构化 `action_items`，走 `createMeetingTodos` | 多一条 LLM 自由文本解析路径 |
| C | 手动总结改走 `/summary` 的 agent 路径 | 失去 `skillPrompt` 自定义提示词能力 |

**倾向**：B。C 最干净但丢能力，A 只是把话说准。
（`summarizeMeeting` **只**在 `MeetingDetailView.vue:306` 一个调用点，改动面可控。）

---

### 🔴 #7 拿到 topic 后要不要自动更新会议标题

**现状读数**：`MeetingDetailView.vue:246`
`const title = m.title || formatCapturedTitle({ startedAt, location: loc, topic: m.topic })`
—— `m.title ||` **会短路**，而两个创建入口（`MeetingListView.vue:181`、
`MeetingRecordView.vue:21`）建会时就用 `formatCapturedTitle({ startedAt })` 填了标题
（最差也是「10月11日 03:25 会议」）⇒ **`m.title` 恒非空** ⇒ `topic` 分支在生产里走不到。

⇒ `m.topic` 在录后精校时被填上了（§138.3 已把 topic 传给 refine），
但**标题不会自动更新**；只有用户在 `MeetingSettingsSheet` 手动点「捕获标题」
（`:128`，那里传了 `topic` **和** `firstUtterance`）才会用上。

| 选项 | 内容 | 代价 |
|---|---|---|
| A | 保持现状（只手动） | 主题白填 |
| B | 当前标题仍是默认格式时才自动更新 | 需识别「默认格式」 |
| C | 每次精校后都更新 | **会覆盖用户自己起的名字**，最不可取 |

**倾向**：B。判据可做成「当前标题匹配 `formatCapturedTitle({startedAt})` 的输出」。

---

### 🔴 ③「这周」是什么口径

**现状读数**：`TasksView.vue:870-878`

```ts
if (dueFilter.value === 'overdue') return due < nowSec
if (dueFilter.value === 'today')   return due <= endOfToday(nowSec)
return due <= nowSec + 7 * 86400        // ← 「本周」= 未来 7 天滚动窗口
```

⚠ `today` 与 `week` 两档**都没有下界** ⇒ **逾期 30 天的任务同时出现在
「已逾期」「今天」「本周」三个 chip 里**（已实测）。

| 选项 | 内容 | 连带效果 |
|---|---|---|
| A | 未来 7 天滚动（维持现状） | 「本周」给不出具体时刻；下界取 `now` |
| B | 日历周（本周一 00:00 → 下周一） | 下界取本周一 00:00 |
| C | 周五截止 | 与仓内既有两处口径都不一致 |

⭐ **拍它顺便修好筛选下界**——两者是同一个决定的两格。
⚠ 周日/周五两个常见候选**都与既有实现不一致**。

**倾向**：B（日历周更符合「本周」的字面含义），但这是产品语言习惯，我倾向明确指定。

---

## 二、其余待拍板（不阻塞主链路，但会累积成技术债）

### 🔴 #4 相对期限以哪个时刻为基准

**现状读数**：`meeting-todos.ts:34`
`accHandoffInput(draft, meetingTitle, now: number = Date.now())`——**可注入时钟已在位**。

同一句 `draft.due` 被解析两次、基准不同：写入路径用**会议时刻**，
转交路径用**点击时刻**（§394 实测：会议 10-07、说「明天下午三点」，
隔一天点 ⇒ 晚一天；隔两天点 ⇒ 晚两天）。

- A：会议时刻（我倾向；「事后补录老录音」有代价）
- B：点击时刻（现状）
- C：两者并存（现状）

⚠ 基准已做成可注入，**改一个默认值即可**。

### 🔴 #5 行动项要不要有展示面

**现状读数**：`meeting-todo-persist.ts:101` `listMeetingTodos` 是全仓**唯一**
`SELECT … FROM local_todos` 的读回，**仍登记在**
`frontend/scripts/dead-features-baseline.json:9` ⇒ 死代码。

两侧文案不对称：会议侧 `buildMeetingReminderNote` 从不说「已加入 N 条待办」，
随手记侧 `note-todo-notice.ts:102` 却说 ⇒ **随手记那条可能算谎报**。

- A：复活 `listMeetingTodos`，给行动项一个展示面
- B：改措辞
- C：直接写 PG tasks

### 🔴 references 是否应跨轮累积

**现状读数**：`meeting-summary-merge.ts` **刻意不做累积**（新一轮非空就整体替换），
门 `summary-reference-cross-round.test.mjs` 的 #5 专门钉住这个边界。

§402 修的是「**空**不抹掉已有」。是否进一步改成跨轮合并累积，是产品口径。

### 🔴 「会议该叫什么」命名策略

`useSessionLiveRecord.ts:34` 用 `resolveRecordingTitle(sessionId(), sessionTitle())`。
⚠ 它只判断「是不是当前 ID 的前缀」，**不过滤占位符**
（若 `sessionTitle()` 返回 `New session - <时间戳>` 这类占位，会原样当标题）。

⚠ 本机实测过 opencode 标题 66% 是占位符 ⇒ 接进来前需要确认。
⚠ 这与 #7 相邻但不同：#7 是「拿到主题后改不改」，这条是「拿到的标题该不该信」。

### 🔴 #10 录音收尾后列表要不要自动更新（§409 新增）—— 注意是**两条**链

**机制已查清**：`markListDirty(scope)` 只 `dirtyScopes.add(scope)`，
唯一消费点是 `use-list-scene.ts:31` 的 **`onActivated`**
⇒ 语义是「**同 SPA 内**离开列表页又切回时刷新」，
跨设备/跨窗口/多标签**一概不触发**（那些只能靠 WS，而 WS 没接，见 #9）。

⚠ KeepAlive 白名单前提**已核实成立**：`LIST_CACHE_NAMES` 含 `'MeetingListView'`，
`MeetingListView.vue:106` 的 `defineOptions({name})` 与之配对。

**两条链都是坏的**：

| 链 | 现状 |
|---|---|
| 随手记录音收尾 → **会议**列表 | `markListDirty('meetings')` 只有 2 处，都在 `MeetingDetailView.vue:333/413`（手动生成总结/转交待办）。`useSessionLiveRecord.stop()` → `finalizeRecording` **从不标脏** |
| 随手记录音 → **会话**列表 | `useListScene('sessions', …)` 有消费者（`SessionListView.vue:454`），却**全仓零** `markListDirty('sessions')` 调用点 ⇒ **整个 scope 没有任何标脏方** |

⇒ 用户看到：**「明明录了、明明弹了成功 toast，列表里没有这一条」**，
必须手动下拉才出现。⚠ 这是**用户可见**的，不 §408 那种「多端才看得出」。

⚠ 同一 scope 下**手动操作标了脏、自动化链路没标** ——
这是「同一契约只在一部分出口生效」的**第三个实例**（前两个是 §401 的 `origin`、§402 的 `references`）。

| 选项 | 内容 | 代价 |
|---|---|---|
| A | 两条链各补 1 行标脏 | 极小，但**会改变用户可见行为** |
| B | 不补，显式登记「需手动刷新」为已知行为 | 用户会遇到「录了却看不到」 |
| C | 等 #9 选 B/C，走 WS 推送 | 成本最高，但与实时口径统一 |

⚠ 我没自行落地 A：它虽像「补一行兑现既有惯例」，但它改的是用户可见行为，
而那正是 #9 的子集 ⇒ **先拍 #9 的口径，再定 #10**。

⚠ 顺带登记一个**没有门守**的交叉缺口：`markListDirty` 的 7 个 scope
与 `LIST_CACHE_NAMES` 的 14 个名字、以及 `useListScene` 的调用点，
**三份清单各自都对，但没有一张门做交叉对账** ——
与 #9「没有门检查广播有没有人收」是同一族。

### 🔴 #9 跨端/多窗口的会议状态同步要不要接（§408 新增）

**现状读数**（机器数点，非目测）：

| 事件 | 后端广播点 | 前端订阅点 |
|---|---|---|
| `meeting.summary_updated` | 3 | **0** |
| `meeting.recommend_updated` | 2 | **0** |
| `meeting.refined` | 1 | **0** |
| `meeting.synced` | 2 | **0** |
| `email.classified` | **0** | 1 |
| `task_created` / `task_updated` | 0 | 1 / 1 |

⚠ **两侧都错位，但形态相反，处置也不同**（§408.6 已收敛）：

| 情形 | 事件 | 性质 |
|---|---|---|
| 发了没人收 | 4 类 `meeting.*`（8 个广播点） | 真在传帧；浪费带宽 + 缺实时 |
| 收了没人发 | `email.classified` 总线侧订阅 | 后端全仓零提及该字符串；分类是**同步**的，结果就在同一次 HTTP 响应里 ⇒ 永不触发 |
| **两端都未接线** | `email.classified` 全链 | 视图侧 `useRealtimeList`（0 消费者）**已被 `unwired-exports.test.mjs` 登记为基线** ⇒ 已有棘轮门守住，不新增即可 |

⚠ `email.classified` 那段带完整 payload 类型 + `handleClassifiedEvent` 调用，
**看起来像一条已接好的实时通路** ⇒ 下一个人会误以为邮件分类结果会实时推回来。

⚠ `initWsBus()` 在 `main.ts:77` **确实在跑** ⇒ 缺口不是「没启动」，是「没被要求接」。
⚠ 已补核 `origin/main`：**远端也没有 meeting 订阅** ⇒ 不是本地未提交改动造成的假象。

⇒ meeting 这条能长期存在是因为**没有症状**：详情页靠 HTTP 响应自己刷新；
只有多窗口/多端才看得出陈旧（也正是 §403 观察到的「列表页不会自己刷新」）。
`check:*` 全是静态/行为判据，**没有一道检查「广播的事件有没有人收」**。

⭐ 参考那道门的自述（`unwired-exports.test.mjs` 文件头）：
「编译通过、类型通过、gates 全绿、运行时也不报错 —— **只是那件事从来没发生过**」；
它的做法是**棘轮不是清零**（基线写的是「未接线」这个事实，**不是**「已确认要删」）
⇒ 我**没有删任何死代码**，只登记。

| 选项 | 内容 | 代价 |
|---|---|---|
| A | 不接，显式登记「刷新即真值」，并把 4 个事件从服务端删掉 | 多端陈旧；省掉无用广播 |
| B | 在 ws-bus 加 4 条路由 | 需处理「不在该会议页」与请求竞态 |
| C | 只接 `meeting.refined`（耗时长、用户最可能切出去） | 半吊子，但比全不接轻 |

⚠ 选 B/C 必须一并处理「只收不发」那一侧，否则是「补一半的实时」。

### 🔴 #8 随手记要不要有说话人标签（§407 新增，本文件初稿后补入）

**现状读数**：`recordingRuntime.ts:1218` 走 `transcribeFull`（**单次送整段、不切块**），
`:1225` 只取 `result.text`；而同一响应的
`segments?: Array<{speaker,text,startMs,endMs}>`（`stt-settings.ts:156`）**零消费者**。

⇒ 用户选 `microsoft/mai-transcribe-2`（预置注释自称「会议首选：说话人分离+词级时间戳+热词」，
$0.10/h）时，后端**照发**三个开参（`buildVerboseOptions` 对 ≤900 秒的单发全部放行），
前端只要文字 ⇒ **付了钱、挨了 408/500/503 的面、没拿到标签**。

⚠ §120 那道门只判「要不要请求」，**没判「请求回来的有没有人读」** —— 缝在这里。
⚠ 对照：会议录音那条分段路径走本地 diarizer，**有**标签 ⇒ 这是缺口不是全盘没有。

⚠ 另三条链（`useVoiceRecording` / `useVoiceInput`）走 `sttApi.transcribe`，
而 `SttResult`（`api/stt.ts:30`）**压根没有 segments 字段** ⇒ 它们不是「丢了」，
是**连接缝都没有**（§407.5）。

| 选项 | 内容 | 代价 |
|---|---|---|
| A | 先不请求（与 §120 同款处置） | 省钱与失败面；但用户明确选了 MAI 却拿不到它宣称的能力 |
| B | 接上：`MeetingSegment` 带 speaker，正文按说话人分行 | 新增数据通路；需定笔记正文格式 |
| C | 正文末附一行「说话人分布」不改正文 | 半吊子 |

⭐ **选 B 时两条链应一起接**（`transcribeFull` 丢字段 + `transcribe` 没有字段），
否则随手记会一半有标签一半没有。

⚠ 上游限制（非本仓能解）：MAI 的 diarization 对 >900 秒录音会自动关，
长会议**即使接上标签**仍拿不到分离能力。

---

### 🟠 #11 会议详情页「总结」成功后的 toast 要不要报条数（§410.8）

- **问题**：本次只修掉了**谎报**（重复点击时被说成「没有可解析的期限、未建提醒」）。
  但**首跑时**它同样一个字不说 —— 建出 2 条待办、建成 1 条提醒，toast 只剩「已生成当前总结」。
- **当场读数**：同族两个出口都说得更多 ——
  `buildIngestNotice`（录后精校）「N 项行动已加入待办，1 条日程提醒已建」；
  `buildNoteTodoNotice`（随手记）「已加入 N 条待办，1 条日程提醒」。
- **选项**
  | | 内容 | 代价 |
  |---|---|---|
  | **A（倾向）** | 与两个兄弟出口对齐，报条数 | 改 `buildMeetingReminderNote` 结构 ⇒ 要同步改既有门 6 处钉住的字符串 |
  | **B** | 保持现状，只报提醒侧 | 0；用户去待办列表自己看条数 |
  | **C** | 只在「一条都没新建」时说 | 0；**= §410 已落地的最小口径** |
- **回退成本**：C→A 约 1 小时（含变异验证）；B/A 之间切换 1 行。
- ⚠ **为什么不顺手改**：那是**少说**不是**说错**，属产品口径，
  不该夹带在一次谎报修复里（详见 §410.8）。

### 🟠 #12 「网上搜索总结技能」这个能力**至今没有实现**（§411.7，§416.1 扩大）

- **需求原文**：「需要到网上搜索相关的总结的技能」。
- **当场读数**：`meeting-skills.ts` 的 `MEETING_SKILLS` 是 **4 条手写死的提示词**
  （滚动纪要 / 决议清单 / 待办提取 / 缺席速览），由 `MeetingSettingsSheet` 的芯片选择。
  **零联网检索**。`summarySkill` 只影响 `onSummarize` 里 `summarizeMeeting` 的
  `skillPrompt` 一个参数。
- ⚠ **别与已有能力混淆**：`searchRelatedContext` 确实会查维基百科，
  但那是**参考资料检索**（§411 修好的那一路），不是**总结技能检索**。

- ⚠⚠ **§416 新增的一个维度，比上面那条更要紧：技能的作用域只覆盖了三条总结链里的一条。**

  | 链 | 谁触发 | 产出形态 | 读技能？ |
  |---|---|---|---|
  | 录音中即时总结 | 录音中每 30s / 3 段 | **JSON 契约** | ❌ |
  | 录完后手动「总结」 | 用户点一下 | **自由中文文本，无契约** | ✅ |
  | 会后精校 | 录音收尾 | JSON | ❌ |

  ⇒ 用户在录音前挑「决议清单」，**对录音中看到的那份总结零影响**
  （`useLiveSummary.ts:57` 与 `api/meetings.ts` 的 `summarize` 都没有 skill 形参）。
  ⇒ 与 `topic`（`meetings.ts:153` 已诚实记账「摘要链仍不消费」）
  是**同一族形状**：设置摆在界面上，实际只影响一部分路径。
- **选项**
  | | 内容 | 代价 |
  |---|---|---|
  | **A** | 保持现状（4 条内置），把「检索技能」从需求里划掉 | 0；但界面上那个芯片仍然只对录完后的手动总结有效 |
  | **B** | 技能市场：内置若干 + 可从后端/远端拉取更新 | 需后端表 + 缓存 + 失败降级 |
  | **C** | 每次总结由智能体**现场检索**再生成 skillPrompt | 每次总结多一次模型调用；延迟与成本上升 |
  | **D** | 只做「推荐下一个该用哪个技能」而不替换提示词 | 小；不解决「找不到新技能」 |
  | **E**（新） | 先把技能**接进即时总结链**（产物仍走 JSON，技能只改摘要/决议的侧重），再谈检索 | 改 `buildSummaryPrompt` 的形状 + A/B 复核；`tldr` 类技能与 JSON 契约不兼容，要定降级规则 |
- ⚠ 选 C 会影响录音中即时总结的**延迟预算**（`SUMMARY_INTERVAL_MS = 30s`）
  与 §407 的 ASR 成本口径，建议与 #4 一并评估。
- ⚠ **选 B 之前别删 `frontend/src/features/meetings/skills/meeting-minutes.md`**（§672.2：
  文档里原写的 `skills/meeting-minutes.md` 是**相对 meetings 目录的简写**，
  按仓库根读会读空 —— 仓库根**没有** `skills/`）：
  它今天**零消费者**，而 `.md` 正是 B 的天然载体。
  ⚠ **§672.3 订正「唯一」这个说法**：「全仓**唯一**规定 JSON 契约」去掉限域后**不成立** ——
  Go 侧 `server_meeting.go:1005` 的 `summaryJSONSchema` 也是 JSON 契约，
  而且**它才是生产里真正下发的那份**（被 3 条 Go 测试钉住）。
  ✅ **「别删」的建议不变，理由换成**：它是**唯一**把「JSON 契约 + 增量规则」写在**一处**的可读文件。
  ⇒ 它今天的状态是「没人读」，不是「该消失」。
- ⚠ **§672.4 范围补充**：选 B 时要面对的是**两条链**，不是一条 ——
  手动总结读前端 **4 条内联** `MEETING_SKILLS[].prompt`（**无契约**），
  即时总结读 Go `buildSummaryPrompt`（**有契约**，且**刻意不读技能选择器**）。
  ⚠ 两个 `buildSummaryPrompt` **同名不同义**
  （TS：`skillId ⇒ 无契约散文`；Go：`(transcript, prev) ⇒ JSON 契约`）
  —— 改哪条、或两条都改，**是需要拍的一部分**。
- ✅ **作用域事实已进门禁**（`meeting-summary-skill-scope.test.mjs`，§416.4）：
  谁把 skill 接进即时总结、或给技能提示词补上 JSON 契约，那道门会转红，
  提醒更新这份清单 —— 避免「已经不成立的理由被当成第一优先级」。

---

### 🔴 #26【本轮新发现·§464】随手记**录音中既没有即时总结、也没有参考资料与建议** —— 而 §454 那条阴性没限定路径

- **需求原文**：「**在录音时**需要进行即时总结……并能**在总结同时，
  给出一些参考的资料与建议**」。
- **当场读数**（§464 逐条实测）：

  | | 会议 / 会话链 | 随手记链 |
  |---|---|---|
  | 录音中的即时总结 | ✅ `useLiveSummary`（`MeetingDetailView.vue:212` / `useSessionLiveRecord.ts:18`） | ⛔ **零引用** |
  | 录完后的参考资料 | ✅ `references`（点开用户自己的笔记） | ⛔ `handleNoteSummarize` 响应体**无 `references`** |
  | 建议 | ✅ `recommendations`（点开 Bing 搜索） | ⛔ 无 |

  ⇒ 这句话在随手记上的兑现度是 **0/3**。
  ⛔ 而随手记的总结只有 `notesApi.summarize(noteId)` 一条路径，
  它要求 `noteId` 已存在 ⇒ **结构上只能录完之后发生**。

- ⚠ **§454 的阴性因此必须限定范围**：那条「已实现」的结论在**它核到的范围内完全正确**，
  错在没写「限会议/会话链」，于是被跨链清点当成了全局事实。
- ⚠ **与 #12 不是同一格**：#12 是「网上搜索总结技能」这个能力**至今没有实现**；
  本条的第②面是「引用用户自己已有的笔记」，**数据源是现成的**。
- ⭐ **成本判断会改变选项排序**：随手记典型时长是**数秒到几十秒**，
  而即时总结的节流门是「每 30s **或**每 3 段」⇒ **短笔记很可能一次都触发不到**。
  ⇒ 「给随手记加即时总结」**可能是伪需求**；真正高频的是**录完之后**要不要给 references。

  | | 内容 | 代价 |
  |---|---|---|
  | **A** | 明说「随手记只有录完后的总结，没有录音中即时总结」 | 零；与 #15 A 格的「明说这是原始转写」同一手法 |
  | **B** | 录完后的 `summarize` 补 `references`（检索用户自己的笔记） | 一次 LLM + 一次检索；**不依赖** #12，也不依赖 #15 的「名字从哪来」 |
  | **C** | 顺手给随手记也接即时总结（复用 `useLiveSummary`） | 每条一次 `/summary` + `/recommend`；⚠ 短笔记可能触发不到，**投入产出比最差** |
  | **D** | 随手记也给 recommendations | 一次检索；但随手记是速记，**长文本建议可能跑偏** |

⇒ **建议的拍板顺序**：先定 A（成本零、立刻能让用户知道现状），
再在 B 与 D 之间选；**C 建议直接排除**（见上方成本判断）。

---

### 🔴 #29【§474·本项目第一个真实 CER 之后】精校的「术语表」**够不到**主导错误 —— 而缺的证据就是段合并

- **用户原话**：「发现录音转写的不太准确」+「需要将不同的段放在一起进行校对合并」。
  这两条现在**第一次被同一批实测数据连起来了**。

- **实测（`mimo-v2.5-asr`，29 条语料 / 8 类，跑两次）**：

  | 分组 | 第一次 | 第二次 |
  |---|---|---|
  | **英文混排** | **21.3%** | **21.3%** ⭐ 完全一致 |
  | 基础 / 数字 / 口语 / 长句 | 5.9 / 6.5 / 7.4 / 1.4% | 5.9 / 7.8 / 14.8 / 1.4% |
  | 专名 / 同音易错 | 0.0 / 0.0% | 0.0 / 0.0% |
  | **整体** | **10.62%** | **11.36%** |

  ⚠ 单次整体 CER 是**虚假精度**：模型非确定（同一条 `createdAt` 一次成 `credit`、
  一次成「一个是顿,tried」）。**可引用的是「英文混排 21.3%」，不是「整体 10.62%」。**

- **错误的形状是系统性的，不是随机字词错**：

  | 参考 | 识别 | CER |
  |---|---|---|
  | `1.0` / `2.0` / `1.5` | 一点零 / 二点零 / 一点五 | 34.6% |
  | `401` / `500` | 四百零一 / 五百 | 24.1% |
  | `createdAt` | credit | 30.6% |
  | `app yaml` | app 文件 | 37.5% |
  | `npm install` / `npm run build` | ntminstall / ntmrunbuild | 8.6% |

- ⭐ **代码里已经有对症的修复，但它的依据够不到**：
  `buildRefinePrompt` 的注释（`server_meeting.go:1315-1318`）写着它**就是为了**修
  `页百零一`→CI、`countedgettotal`→calculateTotal 这一类 —— 「给了名单/主题就有了依据」。
  ⚠ 而 `metaTermHint`（`:1240-1269`）的依据只有
  **会议主题 / 议题 / 参会人 / 地点** ⇒ `401`、`1.0`、`npm install`、`createdAt`
  **一个都不在里面** ⇒ 精校**结构上够不到**实测中主导 CER 的那一类。

- ⭐ **缺的证据来源恰好是用户需求第一条**：
  同一次会议里，别处若出现过 `Redis` / `v2.5` / `createdAt` 的**正确写法**，
  那就是现成的术语依据 —— 而拿到它需要**把不同段放在一起看**。
  ⇒ **段合并 + 精校合起来正好补上这条断链**，而代码里两处都没接
  （段合并是 #15 B 格；术语表扩源是本条）。

- ⛔ **§477 实测（钉 `gpt-4o-mini` 后 81s 跑完 14×2 臂）**：
  「精校准不准」的**逐条**读数出来了 —— 它**基本没修**这一类，
  而且**会改错**：

  | ASR | 正确答案 | 精校后 | |
  |---|---|---|---|
  | Redis 和 **Tafad** | Kafka | 原样 | 没修 |
  | 更新 **Challog** | changelog | 原样 | 没修 |
  | 跑一遍**页百零一** | CI | **101** | ⛔ 改错 |
  | 从**一点零**升级到**二点零** | 1.0 / 2.0 | 原样 | 改错 |

  ⚠⚠ **但这条读数有个必须先说的限制**：探针小结写着
  「**【目标组】需元数据的样本 0 条**」⇒ 本轮**在结构上没有回答**「元数据有没有用」，
  而「净效应为零」**不能**读成「元数据没用」。
  另有**噪声底噪 30%**（对照组 10 条、两臂输入逐字相同却两臂不同 3 条）。

- ⇒ **对 #29 的前提是个坏消息**：「把候选词喂进术语表就修好了」**未被实测支持**。
  ⇒ **先回答一个更前置的问题：这条路本身够不够？**
  - 数字类（`一点零 → 1.0`）**看起来是确定性映射**（未实测）
  - 术语类（`Tafad → Kafka`）**需要外部知识**，不是映射
  ⇒ **两类应当分开拍**，#29 现在把它们混在一起讨论了。

- **为什么不自行落地**：这要改**精校提示词的内容**，且必须实测
  「改对数上升的同时改错数有没有跟着上升」（§30 那个 `康复`→`根` 的教训：
  只看「多改了几处」是不够的）。⇒ 口径与范围要你定 ⇒ **登记不代拍**。

| | 做法 | 代价 |
|---|---|---|
| **A（推荐）** | 术语表增加一路来源：**从本次转写正文里抽候选词**（字母/数字/`.`/`_` 混排的 token），与现有四项并列喂给精校 | 需要一条抽取规则 + 一次配对实测（改对 vs 改错） |
| B | 只把「本场会议内出现过的正确写法」去重后全量喂入 | 提示词变长；长会议可能挤占预算 |
| C | 不动精校，改在**转写后处理**层做数字还原 | ⚠ 从文本无法判断「四百零一」原本是 `401` 还是真的四百零一 ⇒ 大概率会改错 |

#### ⛔⛔ §481 更正框（2026-10-11）：「主导错误」这个词下得太满，本条的前提要重写

上面表格与摘要里那句「实测 CER 的主导错误是字母数字被读成同音汉字（`401`→四百零一、
`1.0`→一点零）」—— **这个归因不成立**，因为**一半的证据是 TTS 造的**。

回查 §474 第二次跑的逐条读数，按「参考是否含 ASCII」拆开：

| 组 | 条数 | 编辑距离 | 参考字数 | CER |
|---|---|---|---|---|
| 参考含 ASCII | 16 | 93 | 455 | **20.4%** |
| 参考纯汉字 | 5 | 8 | 153 | **5.2%** |

```
参考(书面): 我们从1.0升级到2.0,中间要兼容1.5的客户端。
音频实说  : 「我们从一点零升级到二点零，中间要兼容一点五的客户端」   ← TTS 念法
识别      : 我们从一点零升级到二点零,中间要兼容一点五的客户端。      ← 对音频是**对的**
```

⇒ 「`1.0`→一点零」这类，**ASR 认得没错，是书面参考当成了错的**。
⇒ 纯汉字那 8 个编辑距离**逐条人工核对**：6 个是标点、1 个是 `〇`/`零` 字形变体、
1 个是 `看看`→`看他`。⇒ **干净中文语音上真实字错率约 1% 量级。**

**量具自证**：ASR 请求里的 `prompt` 字段是固定常量
`"以下是普通话的句子，请用简体中文输出。"`，**参考原文只发给了 TTS**、
没被当热词发给 ASR ⇒ 读数不是「ASR 拿到了答案」。

⚠ **但不要把含 ASCII 那档整个丢掉** —— 那档里仍有真错：
`createdAt`→`一个是顿,tried`（两次都错，§477 实测精校还改得更错）、
`changelog`→`Channelbug`、`consumerlag`→`consolelog`。

⇒ **对本条的真正影响**：

1. **「加证据」要解决的**不是中文质量（那儿几乎无可修），
   而是 `createdAt`→`tried` 这一类**标识符/术语**。
2. ⇒ **#29 的天花板由「标识符/术语」这一类决定，不由「精校能力」决定。**
   这比 §477 的「喂了元数据也没用」更进一步：**天花板本身就不高。**
3. ⇒ §477 那个「目标组 0 条」的探针缺陷**更加致命**：天花板被压到很小之后，
   更必须量准 —— 否则连「有没有用」都答不了。
   ✅ **§483 已修一半并收回一半**：目标样本一直有、一直跑到了（离线实测
   `对照 10 / 目标 2 / 负控 2`），坏的是**统计口径** —— 小结把
   `onOnly+offOnly`（**臂间有差异的条数**）当成「样本数」打印，
   而「两臂都修好」「两臂都没修」两档**一条都没进统计**。
   ⭐ 而**被丢掉的那两档恰好就是本条要的天花板**：「两臂都修好」= 模型自身就能修
   = 加外部证据边际收益归零；「两臂都没修」= §477 实测还会被改得更错。
   ⇒ 四档计数 + 空档报错 + 对账已补（门 `refine_target_group_buckets_gate_test.go`，变异 5/5）。
   ✅ **§486 又补一条**：目标组里**一个标识符都没有**（全是人名+音译专名），
   而 §481.3 实测真实主导错误是 `createdAt`/`changelog`/`consumerlag` ⇒
   **#29 问的其实是「元数据对人名有没有用」**。已补 3 条标识符类目标样本
   （`asrOut` 逐字取自真实 ASR 输出，**只有 metadata 是构造的**）⇒ 目标组 **2 → 5**。
   ⛔ **但仍然答不了**，剩下两条阻塞都没动：**噪声底噪 30% vs 5 条样本**（远不够）
   + **模型非确定**（单次读数是虚假精度）。
   ⇒ 阻塞性质已从「量具根本量不到」变成「**样本量与方差还不够**」⇒ **可推进，但需要凭据**。
4. ⇒ A/B/C 三个选项的排序**可能因此改变**（天花板低 ⇒ 收益低），
   但**本轮没有实测支撑任何重排** ⇒ **登记，不代拍**。

**已钉成门**：`cer_ascii_split_gate_test.go`（4/4，外部变异 5/5 转红）——
CER 探针今后**必须**分「含 ASCII / 纯汉字」两档报，纯汉字档为空时自己变红。
⚠ 变异 M3 首轮**全绿**（把「纯汉字」那行改指 ASCII 桶，门只查字面量存在没查配对），
已修为断「label ↔ 桶」的配对。

---

### 🟠 #28【§471 新发现】「写好了没人接线」的棘轮只覆盖 **28 个目录里的 1 个**

- **这件事本来是 §328 的后续**：`notesStore == nil` 那类缺陷之所以活到今天，
  正是「写好了、也有测试、但**没人接线**」这一类。仓里为此建了
  `check:dead-features` 棘轮（`scripts/check-dead-features.mjs` + `audit-dead-features.mjs`）。
- **当场读数（§471.5，28 个目录逐个跑审计）**：

  | | 读数 |
  |---|---|
  | `features/` 下有死导出的目录 | **10** 个：`email` 32 · `meetings` 11 · `calendar` 8 · `chat` 4 · `vault` 3 · `rss` 2 · `imports` 2 · `flashcards` 2 · `tasks` 1 · `opencode` 1 |
  | 有棘轮基线的目录 | **1**（`meetings`，11 条） |
  | **死在基线视野外的** | **55** |

  与本需求同域的：**`calendar` 8**（正是「时间点进日程」的落点目录）、`tasks` 1。
  `email` 32 最大，但与录音需求无关。

- **扩到全 `features/` 是不是「修 bug」**：门自己的文件头写的是「保证**不再新增**」，
  而它把范围限制在 `meetings` 的理由是「**一次删干净属于另一个 PR**」，
  **不是**「不想在别的目录发现新的」⇒ 扩面属于「让门兑现它自己写下的意图」。
- ⚠ **为什么不自行落地**：扩面会让基线一次性涨到 66 条，
  并把 `email` 那 32 条一起锁住（棘轮只许减不许增 ⇒ 会挡住别人的清理节奏）。
  **范围撒多大是口径，登记不代拍。**
  ⇒ 想要一个折中：**只扩到与本需求同域的 `calendar` / `tasks` / `notes`（9 条）**，
  `email` 那 32 条留给它的属主。

- ✅ **本轮已把量具修好了**（§471.2）：审计器原有**已实测的假阳性** ——
  它剥掉「所有以 `export` 开头的整行」，于是**默认参数值**这种形态的引用被一起剥掉
  （`export function f(x = SOME_CONST)`），把活符号报成死的。
  实测原文 2 次 ⇒ 剥完 0 次；修后 `features/` 死导出 **71 → 66**。
  ⚠ 假阳性看着安全，实际会让棘轮把**活符号写进基线**，等于给未来的误报开门。

---

### 🟠 #27【§470 新发现】默认本地路径上「参考资料」**一个字都不说**就没了 —— 协议只有两态

- **需求原文**：「在总结同时，**给出一些参考的资料与建议**」。
- **当场读数（§470.1 全部实测，不是注释）**：

  | 事实 | 出处 |
  |---|---|
  | `backend/.env.example:21` 的 **`POCKET_POSTGRES_DSN=` 默认为空** | ⇒ 照它起本地环境就是 remote-only |
  | `notesStore` 只在 `if pool != nil` 里赋值 | `main.go:133-140` |
  | `meetingStore` **同样**只在 `pool != nil` 里赋值，否则用**内存**会议存储（重启全丢） | `main.go:173-178` / `server.go:282` |

  ⇒ **默认本地开发路径上，智能体一个工具都没注册。**

- **用户看到什么**：`agent === true`（这一轮确实是智能体路径产出的，**没说谎**）+
  `references` 为空 ⇒ `MeetingInsightPanel.vue:91` 条件为假 ⇒ **一句话都不说**。
  摘要还在，参考资料栏**静默消失**，用户无从知道是「没有」还是「没有这个能力」。

- ⛔ **先收回一条假缺陷**（§470.2）：我先据后端内部注释
  「区分『**查过资料的摘要**』与一次性的摘要」（`server_meeting.go:478`）
  判成「`agent` 在说谎」。核对**消费方**的类型注释后**推翻**：
  `meetings-store.ts:77` 写的是「true = 这一轮由服务端智能体路径产出」。
  ⇒ **说松散的是后端注释，不是协议**；要改的是注释，且至今未改（无门禁，登记不代改）。

- **为什么不能自行落地**：三个候选都要**产品口径**（新增第三种提示文案），
  或要**部署策略**决定。⇒ 登记不代拍。
  | | 做法 | 代价 |
  |---|---|---|
  | **A** | 后端加事实键（如 `noteSearch:false`），前端据此说第三种话 | 新键要活过 `normalizeSummary` / `toLiveSummary` **两跳**，否则重开会议会说错 |
  | **B** | 复用 `agent_tools` 区分 | ⛔ **实测不可行**：该键在「有工具但模型没调」时同样为空，与「没工具」**不可区分** |
  | **C** | 要求 remote-only 部署必须有 PG | 这是**部署策略**决定，代码定不了 |

- ⭐ **建议 A**：它同时把「有没有这个能力」变成**可测的事实**，
  而不是靠 UI 措辞去推断；且 §469 已经把后端那侧的工具真相算出来了，
  只是一行没往外送。

---


#### 🆕 §557 追加（2026-10-13）⭐⭐⭐⭐ 同族的**第二个实例**，而且成本比本条低得多

变异扫会议精校链 7 条守卫（基线 152 用例全绿）：**5 条有反应、2 条无反应、0 条 BAD_HIT**。
那 2 条「无反应」不是覆盖缺口 —— 逐条核过，它们**只改 `reason` 不改 `adopt`**
（`shouldAdoptFullTranscript` 的 `full-empty` 与 `segments-empty` 两条守卫，
去掉后落到的分支给出**同样的 `adopt` 值**）。

⇒ ⭐⭐⭐ **而 `adoptReason` 在 `meeting-refine-plan.ts:63/:151` 被赋值后零消费。**
　 它带着四个值：`'full-empty'` / `'full-too-short(N)'` / `'segments-empty'` / `'ok'`。

**实测「整段重转没采纳」时用户看到什么**（真函数）：

| 结局 | `shouldAdopt` | 提示文案 | 会议徽章 |
|---|---|---|---|
| A 重转成功 | `true` `ok` | 录音已结束，**已整段重转**并精翻 | `refined` |
| B **重转为空**（网关失败） | `false` `full-empty` | 录音已结束，精翻完成 | `refined` |
| C **重转太短**（大量段失败） | `false` `full-too-short(2<18)` | 录音已结束，精翻完成 | `refined` |

⇒ ⭐ **toast 其实诚实**（A 与 B 文案不同）—— ⚠ **我动手前猜「一字不差」是错的，实测推翻了它**。
　 ⚠ **徽章相同但不算说谎**：`refined` 的语义是「精翻完成」，而精翻**确实**完成了。
　 ⇒ **真正的缺口是「没人能长期看到发生过什么」**，不是「某处说了假话」。

⇒ ⭐⭐⭐⭐ **精校结果提示这个文件修过两个降级路径、漏了第三个**：
　 它为 `fromFallback` 与 `rejected` 各做了文案 + 状态 + 注释（文件头注释整段在讲这一族），
　 ⛔ **`adopted=false` 一个都没做** —— 而它覆盖两个处境完全不同的失败
　 （`full-empty` = 网关没跑 · `full-too-short` = 跑了但不能用），两者渲染完全相同。

⇒ ⭐ **这直接打在需求上**：§546 说同音字/专名「收尾时**无法靠润色文本修好**」，
　 ⇒ **用户以为高精度那一遍跑过了，实际没跑**，而他的原话就是「转写不准」。

⇒ 🔴 **与本条（A 方案）的关键差别**：
　 本条要**新增事实键**（因为「没注册检索工具」压根没被算出来）；
　 **§557 这条的事实已经算好了，只差把它说出来** ⇒ **成本更低**。


#### 🆕 §558 追加（2026-10-13）⭐⭐⭐⭐⭐ 连它自己都没被守住：**断言消息写「分不清失败还是质量不够」，断言体只写了「非空」**

`refine-plan.test.ts` 里那条判据的**断言消息**写着
「必须给出拒采纳的原因，否则排障时**分不清是转写失败还是质量不够**」，
而断言体只有 `assert.ok(plan.adoptReason, …)` ⇒ ⛔ **它只检查非空**。
把五个 reason 全改成同一个非空串，它照样绿 —— 而那正是它**声称要防止**的事。

⭐ **且 `adoptReason` 零消费者（§557 实测）** ⇒ 它是**唯一**承载「五种处境各自发生了什么」的地方，
　 却没有任何判据保证那些值还能互相区分。

**⭐⭐ 同时收回 §557 的一处覆盖缺口**：那张表**少了一种 reason** ——
　 探针直接调 `shouldAdoptFullTranscript` ⇒ 绕过了 `planRefine` 的三元
　 ⇒ **漏掉 `not-applied`**。**改用真 `planRefine` 重测，五种处境的 reason 互不相同：**

| 形态 | `adopted` | `reason` |
|---|---|---|
| ① 正常采纳 | `true` | `ok` |
| ⭐ **② 网关 400（§50 真机形态）** | `false` | **`not-applied`** |
| ③ applied 但正文为空 | `false` | `full-empty` |
| ④ 质量拒收（短一半以上） | `false` | `full-too-short(3<24)` |
| ⑤ 实时分段全空 | `true` | `segments-empty` |

⇒ ⭐⭐⭐ **② 才是真机最常见的那一种**（网关 400 ⇒ 高精度那一遍**压根没跑**），
　 而它与 ③（跑了但正文为空）是**完全不同的用户处境**，§557 却把两者混为一谈。

✅ **已补两条判据**（加进既有 `refine-plan.test.ts`，门数仍 45，用例 13→15）：
　 ★ 五个 reason **逐个断言具体值** + `uniq.size === 5`；
　 ★ 「网关失败」与「质量不够」必须给出不同 reason（**直接实现原判据声称的那条性质**）。
　 三条变异各精确红 1 条 —— 尤其 **M2 把 7 处 reason 全改成 `'x'` 时旧判据照样绿**，
　 那就是缺口本身。`test:all` 335/335 · `vue-tsc` 干净 · 生产文件 sha256 逐位复原。

⇒ 🔴 **仍然不代拍**：让 `adoptReason` 被说出来是产品文案决定。
　 但本节把**它需要的那个事实**钉成了可执行断言 ——
　 将来拍板时，「五种处境可区分」**不会在没人注意的时候悄悄退化**。

### 🔴 #15 随手记语音草稿**零纠错**——同一段录音，走会议路径会精校，走随手记不会（§150.2，§419 补入）

- **需求原文**：「发现录音转写的不太准确…在录音完成后，**可能还需要一次精校**」。
- **当场读数**（§150.2 已逐环核过行号，非推断）：

  | 环节 | 会议 / 会话录音 | 随手记语音笔记 |
  |---|---|---|
  | 落库正文 | `finalizeRecording` → 精校文本 | `useNoteRecording.create()` 落 `content: text` —— **未经任何加工的切片转写** |
  | 切片去重 | 前端 `meeting-dedup.ts`（§455 已补 `MIN_ANCHOR`） | **服务端** `incremental.go:222 mergeIncremental`（17 个独立行为测试守着） |
  | 整段校对 | `finalizeRecording` 里的全量重转 | ⛔ **代码已存在但被条件挡死**，见下方 B 格 |
  | 同音/专名纠正 | `deriveRoster` + `repairRosterHomophones` | **整套从未接入**（`features/notes/` 下 0 处引用） |
  | 术语表（meta） | refine 收 meta（§138.3） | `buildNoteSummaryPrompt(body)` **签名里没有 meta** |

  ⇒ 捕获门槛最低的那条路径，产出质量最低；同一个人同一段录音，
  先开「会议录音」还是随手记，结果不一样。

- ⛔ **§463 更正**：原文把随手记的落库正文写成「**原始 ASR**」，
  也不准确——它是 **3 秒定长硬切逐片拼接**的结果（`recordingRuntime.ts:704/962`），
  已经过服务端跨片去重，只是**从未被整段音频校对过**。
  ⚠ 这个区别要紧：「原始 ASR」暗示「质量无法改善」，
  实际是「**有一个现成的改善手段没被接上**」。
- ⚠ **前置依赖**：§150 的结论是「随手记侧要纠错，**必须先有一个『名字从哪来』的产品决定**」——
  两个同音纠正函数都吃 `roster`，而随手记是单人速记，**原理上没有名单来源**。
  ⇒ 选项 B/C 都以那个决定为前提，不能单独拍。
  ⇒ ⭐ **但下面新增的 B 格不受这个前提约束**（它不碰名单）。
- **选项**

  | | 内容 | 代价 |
  |---|---|---|
  | **A** | 接受不纠错，在随手记里明说「这是原始转写」 | 小；把已知缺口讲出来而不是让用户自己发现 |
  | **B** | 随手记也等一次 LLM 精校（不依赖名单，纯语言模型纠错） | 多一次模型调用 + 延迟；中文专名纠错的效果需要实测 |
  | **C** | 先做「名字从哪来」（笔记打名字标签 / 从已有通讯录取），再上 B/C 的纠错 | 最大；但它是 B/C 的前提 |
  | **D** | 随手记只做「整段重转 + 精校」，不做名单纠正 | 中 |

- 🆕 **B 格 · 整段校对**（§463 新增，与 A/C 格**不是同一件事**，请**单独拍**）：

  > 用户需求第一条是「**将不同的段放在一起进行校对合并**」。
  > 随手记现在**这一层在形态上就不存在**——不是质量差，是没有这一层。

  | 事实 | 位置 |
  |---|---|
  | 兜底全量转写**只在「切片一个字都没出」时才跑** | `recordingRuntime.ts:1197` `if (!this.transcript.value.trim() && audioBlob.size > 0)` |
  | 只要**任意一片**出过字，这个条件此后**恒为假** | `:1080-1082` 一旦出字即写 `this.transcript.value` |
  | 「出过一次字」≠「出全了」：真机 16 片里 8 片带 error 但同时带 text | `:1069-1079`（2026-10-05 实测） |
  | 全量重转的**代码本身已经写好了** | `:1218 transcribeFull(...)`，最长 10 分钟、带 abort |

  ⇒ ⭐ **选项 D 的写法会让人误判成本**：它把「整段重转」写成要新建一条链，
  实际是「**改一个条件 + 定一次口径**」。这是 B 格与 D 的唯一区别，也是关键区别。

  | | 内容 | 代价 |
  |---|---|---|
  | **B-1** | 保持现状（切片拼接即最终正文） | 零；但需求第一条在这条链上是空的 |
  | **B-2** | 切片出字后**也**跑一次整段重转，用它**替换**正文 | 每条随手记 **2x ASR**；按 §456 仓内最便宜档 ≈¥0.086/h，30 秒随手记的增量成本在**分级以内** |
  | **B-3** | 整段重转结果与切片文本**做合并/校对**（而非替换） | ⛔⛔ **§485 实测推翻了它的隐含假设**：若实现为「复用 `mergeIncremental`」，在**生产切段形态上是 no-op**（`SplitWAV` 产出的段**互不重叠**，而 `mergeIncremental` 的设计前提是「相邻切片**重叠**」）+ 在**真实重复内容**上 4/4 组**误删**（含一例把「第三项议题是人力」改写成「请三是人力」）⇒ **它是错的工具** |
  | **B-5**（§485.5 新增） | 「**跨段校对**」作为独立能力：**不做去重**（原样保留所有段），只做①修边界半词/半句（§480 实测 3s 切出「联调」「回」「后盾」）②借邻段上下文纠正边界识别错误 | ⭐ 这才是用户原话「放在一起**校对**」指的东西。⚠ **是新工作量，不是现有函数的接线**。⚠ 与 #30（ASR 热词）**互补不替代**：热词在 ASR 层解决「词认错」，校正在段层解决「上下文不足导致认错」。待拍：边界半词怎么合 / 邻段上下文给多少 / 真实重复怎么标 |
  | **B-4** | 只在**质量信号可疑时**跑（沿片数/失败率阈值触发） | 成本介于 B-1 与 B-2 之间；需要一个可解释的触发条件 |

  ⚠ B-2/B-3/B-4 都会让收尾窗口变长，而 `note-recording.ts:8` 记着
  「这段窗口最长 10 分钟、此前无任何中止手段」（`canCancel` 已在 #15 之外的另一轮修过）。
  ⇒ **建议与 #10 一并评估**，不要单独拍 B 格。

  #### ⭐ §481 补充（2026-10-11）：B-2/B-3 的「整段重转」**不是整段单发**，这个区别会改变选项含义

  上面 B-2/B-3 都说「跑一次整段重转」。§478/§480 的实测读数是「**整段一次单发**逐字正确」，
  但生产走的 `TranscribeFull` **不是单发**：

  ```
  full.go:424  segmentSec := effectiveSegmentSec(target.Model)        // mimo 未登记 ⇒ 25s
  full.go:426  segments, _ := SplitWAV(audio, segmentSec)             // 静音感知，上限 25s
  full.go:445  for _, seg := range segments { transcribeFor(...) }    // 逐段独立
  full.go:486  out.Text = strings.Join(texts, "\n")                    // ⛔ 跨段零合并
  ```

  ⛔ **`full.go:486` 是纯换行拼接，没有任何跨段校对** ——
  这才是需求第一条「把不同的段放在一起校对合并」在生产侧真正缺的那一环。
  （⚠ `minSilenceCutSec` 注释里「零重复」那句说的是**切分对齐句子**，不是聚合阶段做了合并，
  两件事在注释里挨得很近，容易读成后者。）

  ⇒ **B-2 与 B-3 的差别因此比原先写的更大**：
  B-2 拿到的不是「理想整段」，而是「按静音切开的若干段换行拼起来」；
  **B-3 才是需求真正要的那一层**（把段拼起来校对）。

  ⚠⚠ **只做 B-2 的残余风险**：音频里**没有 ≥800ms 长停顿**时，
  `SplitWAV` 退化成**纯 25 秒硬切**（`SilenceCut=false`），
  边界仍会落在句子中间。实测我们用的 `mimo-v2.5-asr` **不在 `RecommendedModels()` 里**，
  所以段长恒为 25s，与它的实际能力无关。

  ⭐ **好消息（§481.2）**：真人会议本来就有 1575–3475ms 的句间停顿（§121 实测）。
  用**带 1.2s 停顿**的语料跑生产切分器：8 段里 **7 段 `SilenceCut=true`**、
  段长 4.5–8.9s、远未到 25s 硬顶 ⇒ **静音感知设计本身是好的**。
  ⇒ 所以 §480 那组净差（3.0s +9 / 1.0s +30）应当被降级读作
  **「最坏情况：无长停顿 ⇒ 硬切」**，而不是随手记的常态。
  （我自己的 TTS 语料零个 ≥800ms 静音段，所以 §478/§480 只量到了这条退化路——
  这条限制是**语料**的，不是产品的。）

  ⛔ **仍未实测**：探针 `live_prod_segment_probe_test.go` 的 B/C 两臂
  （B=`SplitWAV`+`\n` 拼接 / C=同一批段 + `mergeIncremental` 聚合）**还没跑**，
  需真网关凭据。⇒ **B-3 到底要多少工作量、收益多少，本轮仍无读数** ⇒ 登记，不代拍。

- ⚠ 选 B（A 格）会影响随手记的**等待时间**（兜底转写最长 10 分钟，
  `note-recording.ts:8`）——精校要再叠一段等待，与「点停止没反应」那个老问题同源，
  建议与 #10 一并评估。
- ✅ **结构性事实已进门禁**（`note-voice-zero-correction.test.mjs`，§419）：
  谁把精校接进随手记，那道门会转红，提醒回来更新这一条，
  避免「已经不成立的理由被当成事实继续用」。
- ✅ **B 格的现状也已进门禁**（§463.8 新增）：`note-fallback-transcribe-trigger.test.mjs`
  钉住「`:1197` 的触发条件 = 切片文本为空」。
  ⇒ 谁把兜底改成无条件（即每条随手记 2x 付费），门会转红。

---

### 🔴 #16 随手记「重新总结」是**替换**还是**追加**（§147 / §125.7.2 标记不代拍，§420 补入）

- **当场读数**：
  - 跨调用幂等**已经有了**（§147）：`note-todo-persist.ts` 的 `alreadyExists`
    查 `note_id + title + extracted_from_voice = 1`，并且有 4 条行为门钉住
    （`note-todo-behavior.test.mjs` §147 块，含「拆掉查重必须当场失效」的负控）。
  - ⚠ **但归一化只有 `trim()`**（`note-todo-plan.ts`）⇒ 查重是**精确文本**匹配。
- **为什么这件事仍要你拍**：同一篇笔记点两次「总结」，两次发给模型的是**同一份正文**，
  而模型措辞未必逐字一致 ⇒ 「准备对比表」与「准备一下对比表」**查重失效**
  ⇒ 待办列表出现两条实质相同的条目 + 两条日程提醒。
  ⚠ 频率取决于模型的确定性，**本仓无法离线测**（要真网关），所以只能拍口径、不能拍概率。
- **选项**

  | | 内容 | 代价 |
  |---|---|---|
  | **A** | 维持精确匹配（现状） | 0；措辞漂移时出重复，界面看不出来 |
  | **B** | 重新总结时**替换**：先删掉本篇由语音提取的旧待办，再写新的一批 | 中；需处理「已完成的待办被删」的观感问题 |
  | **C** | **近似归并**：按 due + assignee + 文本相似度合并 | 中高；相似度阈值需要实测，误合并会吞掉真待办 |
  | **D** | 不改归并，只在**提示里**说清「重复总结可能产生相近条目」 | 小；但等于把问题推给用户 |

- ⚠ B 与已修好的**幂等**不冲突但要一起想：`alreadyExists` 保证「同一条不重复」，
  B 要的是「换一批时旧的让位」——两者作用在不同层（前者管同批，后者管跨批）。
- ⚠ 会议侧没有这个问题：会议的行动项来自**实时总结**（单一来源、不反复重生成），
  且 §410 已把「重复点总结」做成幂等 ⇒ **只有随手记这条链需要这条决定**。

---

### 🔴 #13 会议同步到云端**失败时一个字都不说**（§413）

- **当场读数**：`ingestMeetingArtifacts` 返回 9 格，`finalizeRecording` 只抄 7 格，
  漏掉 `cloudSynced`。全仓对它只有两处提及：计算/返回本身，
  以及一道**只钉返回语句形状**的正则（`next-event-honesty.test.ts:194`）
  —— **生产侧零消费者**，且没有被持久化到任何地方。
- **严重程度**：`server_meeting_ingest.go:21` 对 `meetingStore == nil` 是
  **fail-closed 503** ⇒ **任何未配置 PG 的部署里它恒为 false**，
  而 `catch { // 离线或未配置 PG，本地已入库 }` 把它静默吞掉。
  ⇒ 用户以为一切就绪，**实际上这场会只存在于这台设备**。
- **同仓已有正确做法**：邮件链 `email-inbox-page.ts:99` /
  `EmailSpamCleanupView.vue:145` 把成功与失败**分句说**，
  `EmailAccountSetup.vue:37` 有「尚未同步」；邮件侧还留了 BUG-AC 的记录。
- **选项**
  | | 内容 | 代价 |
  |---|---|---|
  | **A（倾向）** | 对齐邮件链：提示里补「未同步到云端」**并**给一个手动重试入口 | 中；要加一个 `syncMeeting` 重试动作 |
  | **B** | 只补提示，不给重试 | 小；用户知道但没法自己解决 |
  | **C** | 会议列表加「未同步」徽章 | 中；要一个可持久化的同步状态列 |
  | **D** | 保持现状 | 0；代价是用户持续被蒙在鼓里 |
- ⚠ **为什么不顺手改**：光说没用，用户需要的是**重试出口**，
  那是一次实打实的产品设计，不该夹带在缺陷修复里。

---

### 🔴 #14 要不要把 ASR 默认档从 whisper 系换成中文专用档（§414）

- **问题**：清单此前**全文 0 处 CER**，只有价格/语言数/流式/diarization 四个维度。
  而本仓主场景是中文会议录音，排在最前的 `openai/whisper-large-v3-turbo`
  恰好是**中文最不准**的一档（中文平均 9.86%、**WenetSpeech 会议场景 18.87%**，
  同期中文专用模型 3.05%–4.56%）。⚠ 本轮已把这段事实补进清单（不需拍板的部分），
  但**默认选谁**是产品决策。
- **另一条与本项目强相关**：Groq 托管 whisper 是最便宜的托管档（~$0.02–0.04/hr），
  但它「**任何价格都不含 diarization**」—— 而会议场景要说话人分离。
- **本地那条路目前是死的**：`capabilities.ts` 已证明 sherpa 插件是骨架
  （Android 5/6 方法 reject、iOS 无实现、无 `.onnx` 模型），
  而 `api/stt.ts:5` 的注释仍写着「Try on-device sherpa-onnx (**Paraformer for Chinese**)」。
- **选项**
  | | 内容 | 代价 |
  |---|---|---|
  | **A** | 保持 whisper 系当默认，只把「价格 vs 精度」的取舍交给用户 | 0（本轮已落地） |
  | **B** | 默认换成含 diarization 的中文档（Speechmatics from $0.129/h / AssemblyAI $0.15/h），whisper 降为兜底 | 中；**需实测 OpenAI 兼容性 + 真实 CER** |
  | **C** | 补完本地 sherpa（真接 AAR + 下 `.onnx` 模型跑 Paraformer/SenseVoice） | 大；但中文最快也最便宜，且离线可用 |
  | **D** | 会议场景默认走 `microsoft/mai-transcribe-2`（$0.10/h 促销，三件套齐全） | 小；但促销价不可长期基准，且 diarization 只支持约 15 分钟 |
- ⚠ **B/C/D 都需要实测**：本仓拿不到真实会议音频，也连不上网关，
  现在拍等于用一份二手数字定架构。

- ⭐ **§435 已把价格与精度并排放成一张表**（数据早就在 `target.go` 的
  `RecommendedExternalModels()` 里，缺的是并排）：

  | $/小时 | 模型 | 流式 | 单次限长 | 说话人分离 | 中文精度 |
  |---|---|---|---|---|---|
  | 0.012 | qwen3-asr-0.6b | — | 60s | — | 22 方言；嘈杂弱于 1.7B |
  | 0.012 | whisper-large-v3-turbo | — | 60s | — | ⛔ **CER 9.86%／会议 18.87%** |
  | 0.012 | nemotron-3.5-asr-streaming | ✅ | 60s | — | zh broad-coverage，慎选 |
  | 0.027 | qwen3-asr-1.7b | — | 60s | — | 0.6B 升级档 |
  | **0.10** | **microsoft/mai-transcribe-2** | — | 60s | ✅ | **WER 3.4%**（⚠ 促销价至年底；分离限 ~15 分钟） |
  | 0.18 | gpt-4o-mini-transcribe | — | 600s | — | WER 0.0447 |
  | 0.36 | gpt-4o-transcribe | — | 600s | — | WER 0.0396 |
  | 0.38 | asr-1.0（minimax 国内站） | ✅ | 500s | ✅ | 2026-10-08 实测分离+词级均可用 |
  | 0.50 | glm-asr-2512 | ✅ | **30s** | — | 中文+8 方言；需先解决余额 429/1113 |

  ⇒ **「最便宜」与「最适合本仓主场景」指向相反，差 8–40 倍**：
  $0.012 那三档**全都没有说话人分离**，而分离正是 §45–§52 一整轮在修的东西；
  其中 whisper 档在中文会议上还是最不准的一档。
  **性价比拐点 = `mai-transcribe-2`（就是上面的选项 D）**；
  要「长会议 + 流式 + 分离」三件齐全只有 `asr-1.0`，代价 3.8 倍。
  ⇒ **建议按 D 拍**，并把「促销价 + 分离限 ~15 分钟」写进设置页，
  而不是等用户踩到。
- ⚠ 那句「连不上网关 ⇒ 拍不了」要**部分收回**：**价格证据在 `origin/main` 的 40 个
  未推送 commit 里**（§434.3：minimax-asr-1.0 $0.38/h、glm-asr $0.50/h），
  本地分支那 5 条网关模型**没有 `USDPerHour`** ⇒ 合并后价格面才完整。
  仍然拍不了的是**真实 CER**（需要真会议音频 + 网关凭据）。
- ⚠ **建议并进 #8 一并拍**：长会议（>15 分钟）开分离时拿不到标签，
  而本仓的处理是「自动关分离、照常返回文字」⇒ 用户拿到更好的文字、拿不到说话人。
  这与 §407（随手记整段收尾丢弃说话人标签）是**同一族后果的两个入口**，
  但 #8 此前只登记了随手记那一条。

#### 🆕 §468 收窄 #14：**「找更便宜的 ASR」在「默认档」这一层从未被问过**

⭐ **本条修正的是我自己在 §456 给的阴性。** §456 的结论是
「仓内最便宜档 $0.012/h ≈ ¥0.086/h ⇒ **『找更便宜的 ASR』现状已满足**」——
⚠ 那句话**只核了价格清单**，没核「默认落在哪一档」。

**读数（`server_stt_settings.go` `resolveSTTTarget`，生产路径）**

auto 的回落顺序是 **网关 → MiniMax → 外部**，各自默认模型：

| 通道 | 默认模型 | $/h | 说明 |
|---|---|---|---|
| 网关 | `usable[0]`（实时探测首条可用） | 随网关 | — |
| MiniMax | **`asr-1.0`** | **0.38** | ⚠ **只在用户已配 MiniMax key 时才走**；代码有完整理由注释（会议要长录音+说话人+即时出字，三件套只有它齐），**不是疏忽** |
| 外部 | **`gpt-4o-mini-transcribe`** | **0.18** | 未配 MiniMax key 时的实际默认 |

**⇒ 缺的那一格是**：

① **「更便宜」与「更准」在本仓清单里互斥** ——
$0.012/h 那三档里的 `whisper-large-v3-turbo` 中文 CER 是 **9.86%／会议 18.87%**，
而会议正是本项目主场景 ⇒「挑最便宜的」**恰好挑到中文最不准的一档**。

② **实际默认既不是最便宜的、也不是最准的**：
`gpt-4o-mini-transcribe` 比清单里最便宜的**贵 15 倍**，比 MiniMax 默认**便宜 2.1 倍**。

⇒ **修正后能拿去拍的一句话**：
**「找更便宜的 ASR」不是「找得到便宜的」，而是「要不要为了便宜牺牲中文精度」**，
且**默认档该不该按价格自动选**至今没人问过。

✅ **该顺序已补上门**（§468.5，`stt_auto_resolution_order_test.go` 2/2，行为级）：
既有 5 条门守的是**错误诚实性**（不许静默换通道/换模型），
**没有一条**断言「auto 里 MiniMax 先于外部」或两个默认模型名。
⇒ 把 `externalTarget` 提到 `minimaxTarget` 前面（后人「优化成本」最可能做的改动）
**会让既有门全绿**，而它把默认成本从 $0.18/h 变成 $0.38/h。
⚠ 门的失败文案写明「这是产品决定 ⇒ 先更新 #14，再回来改这里」。

⚠ 两条**已核实为假**的怀疑（省下重复劳动）：
`asr-1.0` 的 `Diarization: true` **早已补齐**（`target.go:409`，
`:398-408` 那段长注释记的是**已修复的**静默失效，不是现状）；
它的 `$0.38/h` **在价表里**（`:395`）⇒ 成本看板不会显示「未知」。
（这两条我都一度当成缺陷，核对后是我读错了历史注记 / 漏读了条目。）
⚠ **真实 CER 仍然拍不了** —— 需要真会议音频 + 网关凭据（见清单 A1）。

### 🔴 #17 会议 LLM 调用要不要设**每场上限**并**对用户可见**（§424，带数字）

- **问题**：滚动摘要的调度修好了（§424），但**成本本身没有口径**。
  此前登记为「长会议成本无上限也不可见」，本轮把它量化：
  - 一轮刷新 = **2 次** `/summary` + `/recommend` 调用；慢模型下 `/summary`
    还可能内部跑满 agent 3 轮再回落 1 次 ⇒ **单轮最多 5 次 LLM 往返**。
  - 修完调度后：**≈ 2.4 轮/分钟**（10 分钟会议 24 轮实测读数）。
    ⇒ 60 分钟会议 ≈ **290 次**；⚠ 单轮最坏 5 倍 ⇒ **≈1450 次**。
  - ⭐ **四条链里有两条不带转写截断**：`/summary` 的一次性回落、**以及 `/refine` 精校**
    （另两条截：agent 尾 4000 runes、`/recommend` 尾 2000 runes）。
    ⚠ **本条初稿只列了三条摘要链，漏的恰好是需求原文第一句点名的「精校」那一条。**
  - ⭐⭐ **长会议上精校的代价是翻倍的**：`ErrOutputTruncated` 的定义逐字是
    「输出被 `finish_reason=length` 截断」，**只认输出侧**；
    **输入**装不下时上游返 4xx ⇒ 不匹配 ⇒ 后端 502
    ⇒ 前端 `fallbackRefine` 把**同一份超大请求体原样再发一次**给 `/api/llm/chat`
    ⇒ 同样失败 ⇒ 用户拿回原始转写。
    ⇒ **两次全量往返（150s 超时各一次）、零产出**，而「精校」静默没发生。
- **选项**
  | | 内容 | 代价 |
  |---|---|---|
  | **A** | 只把回落链也截尾（与另两条同口径，用既有 `maxTranscriptRunes` 风格），不给整场设总上限 | 小；**但长会议的单轮输入质量下降**（开头内容丢给 prev_summary 承载） |
  | **B** | A + 面板显示「本场已调用 N 次」 | 中；要新增计数与存储位 |
  | **C** | A + 给每场设调用预算，超出后降级为「只精校、不滚动」 | 中；⚠ **预算值是纯产品数字，无客观依据** |
  | **D** | 只做 A，把总上限留给将来有真实账单数据时再定 | 0 |
- **追加一个选项**（本轮核实后加的）：
  | | 内容 | 代价 |
  |---|---|---|
  | **E** | 精校**分窗**：按说话人/时间把转写切成若干窗分别精校再拼接 | 大；但它是**唯一能同时保住质量与成本**的路子（截尾会丢掉会议开头） |

- ⚠ 我**不做默认**：截尾长度（多少字）、总预算（多少次）、分窗怎么切，都是口径，
  不是代码能替你定的。
- ✅ **本条已部分解封（§426）**：服务端现在会在回落响应里带
  `refine_input_runes` / `summary_input_runes`（按 **rune**，与另两条链的
  4000/2000 同单位），前端透出为 `inputRunes`。
  ⇒ 「上次回落时到底喂了多大」**现在可以从响应/日志里读出来**，
  阈值不再只能靠猜。
  ⚠ 仍然测不了的只剩一件：**网关实际模型的上下文窗口**（本机无凭据）。
  两者相除就是阈值 —— 攒够几次真实回落读数即可定，不必现在拍。
- ⚠ 另有一项**本轮无法拍板**：真机上 Web Speech `isFinal` 的到达间隔会不会 <2000ms
  （§424 缺陷一的触达率）。机制已证，触达率需要真机。

---

---

## 三、低优先级（未在本文件当场复核）

以下几项**未做当场复核**，仍按主文档各处 § 的记录看待：

- 是否新增 `MM/DD` 支持及跨年规则 —— ⚠ 措辞已修正（§404.2）：代码里**从未有过** `MM/DD`，
  该条应表述为「是否新增」，不是「曾支持后禁用」
- 待拍板②：录音前/中拿 topic（A）vs 详情页加「按当前设置重新精校」（B）
- ① kxmemory 摘要少拿 Title/Participants（未实测）
- ①a/①b 导入角色分离（五处串联阻断 + mai-transcribe-2）
- references 信任问题 A/B 修法
- `confidence` 三处占位常量（§372：ASR 云端恒 0.95 / 手工段恒 0.7 / 存储层默认 1.0）
- 时间轴三档、§338/§339、§295.5

---

## 四、无法靠拍板解决的结构性阻塞

这几项**不是决策问题**，是环境/凭据缺失：

- **真机 UI 验收**：§388/§390/§393/§394/§400/§401/§402/§410 八处修复**均未做真机验收**
  （验的是纯函数行为与静态接线，不是界面渲染与真实上游输入）
- **后端起不来**：未配置 `POCKET_POSTGRES_DSN`；PG 在 127.0.0.1:5432 跑着但 `llm_gateway` 认证失败
- **真实 ASR CER / 网关实付价 / §290.2 真实数据导出 / 用户最终拿到的摘要**：结构性不可测

---

## 五、已自行落地、未经确认的修复（如需回退请说）

按「让代码兑现它自己写下的意图」自行落地的十一处，回退成本都很低：

| 节 | 修复 | 回退成本 |
|---|---|---|
| §388 | 会议标题不再用裸 `sessionTitle()` | 1 行 |
| §390 | 期限静默错日期（机器日期放宽 `\d{1,2}` + 兜底 fail-closed） | 见 §390 |
| §393 | 待办读回的时区（`deviceTimeZone()` 而非 UTC 切片） | 1 行 |
| §394 | 给 `accHandoffInput` 加可注入 `now`（**默认值不变 ⇒ 行为逐字节不变**） | 1 行 |
| §400 | 跨会议说话人标签落库范围守卫 | 1 行 |
| §401 | `renderTranscript` 补 `origin` 豁免 | 2 行 |
| §402 | 滚动摘要跨轮不抹掉 references | 4 行 + 1 个新模块 |
| §406 | 两处 ASR 断言 `gw != 3` → 守区间（否则该修正**让 CI 变红**） | 2 行 |
| §406 | secret-scan 逐行豁免（合成串）+ 文档命令改用正则 | 2 行 |
| §410 | 「总结」重复点击时不再谎报「没有可解析的期限」（`count` 接进文案） | 3 行 + 1 个新门 |
| §411 | 智能体的参考资料不再被联网检索整体覆盖（独立槽位 `agentRecs`） | 4 行 + 1 个新门 |

⚠ §412–§424 后续自行落地的九处（同一口径，回退成本都很低）：

| 节 | 修复 | 回退成本 |
|---|---|---|
| §415 | `api/stt.ts` 文件头改回如实陈述（原文写死 Groq Whisper，与 §406 起的「用户设置+网关探测」矛盾） | 1 段注释 |
| §416 | `buildNoActionItemsNote` 在**录音中**不再说「可稍后再点一次总结」（那条路走不通） | 1 个形参 + 2 行 |
| §416 | 既有门 `meeting-action-items-source` 的弱不变式换成更强的两态判据 | 1 个用例重写 |
| §418 | 滚动摘要触发决策提成无依赖模块 `live-summary-gate.ts`（原先内联、**零行为门**） | 1 个新模块 + 1 行 |
| §419 | 「随手记语音草稿零纠错」从设计文档的「不代拍」推进到本清单 **#15** | 0（登记） |
| §420 | 「重新总结替换还是追加」同上推进到 **#16** | 0（登记） |
| §421/§422 | 撤销 §421 造的重复门（既有 Go 门 `dedup_threshold_sync_test.go` 早覆盖），并在 §421 原文顶部加推翻块 | 已在库外 |
| §423 | email 包 SELECT↔Scan **七份**手抄列表的逐位对应门 + 修 `ListEmails`（投影 15 / Scan 14） | 1 个新门 + 3 行 |
| §424 | 滚动摘要调度：无 ceiling 的 debounce（饿死）+ `refresh()` 无在飞守卫（并发叠加 + 旧轮盖新轮） | 1 个新模块 + 6 行 |
| §426 | summary / refine 的回落载荷带上**本次 LLM 输入规模**（runes），前端透出为 `inputRunes` | 2 个键 + 2 行归一化 + 1 个新门 |
| §427 | 说话人兜底：`renderTranscript` 曾是全仓唯一没有兜底的出口，会把字面量 `[null]` / `[undefined]` 拼进用户可见转写与 3 个 LLM 提示词；`onSummarize` 的手搓 join 改为复用真身 | 3 行 + 1 个新门 |

⚠ §453–§469 这一段后续自行落地的（同一口径，回退成本都很低）：

| 节 | 修复 | 回退成本 |
|---|---|---|
| §479 | ✅ 分片探针的**写死 300s ctx** 改成按调用数算（纯函数 + **离线门 3/3**）。⚠ 顺带发现真正的阻塞是**窗口设计**：600s 会议要 **1501 次**调用 ≈ 5 小时 ⇒ 正确做法是「从真人会议取 30–60s 代表片段」 |
| §476 | ⚠ **15 个真网关探针里 14 个的用法注释漏了 `-timeout`**（Go 默认 600s，而实测单条 20–60s ⇒ 必然中途被杀，且被杀前的读数**没有「被截断」标记**）。只修了实测过的那一条 | 1 处注释；⚠ 其余 13 条的 `-timeout` 需按各自实测耗时定 ⇒ 登记待办 |
| §474 | ⭐ **A1 解开**：第一个真实 CER（`mimo-v2.5-asr` 英文混排 21.3%，跑两次一致）；顺手修掉 CER 探针**分组表漏掉最大一组**（16/29 条），并补**离线**门 | 1 处分组推导 + 1 个新门；⚠ 发现**代码默认档 `minimax-asr-1.0` 在网关上无供给** |
| §469 | **提示词与接线同源**：`meetingAgentSystemPrompt` 收 `tools`，注册了 `search_notes` 才宣传它。§328 的真网关实测里，无工具时模型去调一个不存在的工具、9/12 运行在智能体层失败，且降级提示因 `agent != true` 为假**一句话都不说** | 1 个签名 + 1 个新门；⚠ 签名变了，**5 处测试调用点已同步**，回退要一起回退 |
| §466 | 提醒的 `source` 标识三条链全覆盖且互不串用（补 §465.1 登记的门缺口） | 1 个新门 |
| §467 | `internal/stt` 那 9 条 skip 变成台账（`live_probe_skip_ledger_test.go`） | 1 个新门 |
| §468 | 给「auto 回落顺序 + 两个默认模型」补门（`stt_auto_resolution_order_test.go`） | 1 个新门 |
| §463/§464 | 给随手记「兜底转写触发条件」「即时总结 0/3 现状」各补一道门 | 2 个新门 |
| §463 | ⛔ **修掉简报里四处同源错误断言**（§448 范围界定的三个前提 + 〇速览首句 + #15 表） | 0（改文档） |
| §460 | 给 §455 的修复补上门禁链缺的**中间一环**（行为级端到端） | 1 个新门 |
| §456 | 切片去重三阈值（`ANCHOR_COVERAGE` / `MIN_ANCHOR` / `MAX_OVERLAP`）前后端同步 | 见 §456 |

⚠ §410 另有两处改动不在上表：
① 既有门 `reminder-outcome-honesty.test.ts` 只放开了解构里的**额外字段**
（`reminders, reminderPlanned, unresolved` 仍须按序出现、仍须直接从 `await` 上解构），
已用变异 **M7**（删掉 `unresolved`）证明该门**仍有牙**；
② §410.7 闭掉了 §406 自己留在**文档说明文字**里的两处合成串字面量
（对齐 §273.9 的脱敏写法，未加豁免、未动扫描器）。

---

## 六、本轮新增的流程性教训（供后续会话直接套用）

1. **既有门红了先怀疑自己的修法**，但核对后若确认「意图被满足、只是形式过窄」，
   处置是**只放开形式**并**立刻用变异证明它仍有牙**——不是删判据，也不是把代码拧成
   满足脆弱正则的形状（实测四格怎么排都过不去）。
2. **新写形态判据前先问「这条约束真是契约吗」**：§410 的第一版门把
   「`count` 必须在左花括号后第一位」当成了契约，与被我调整的那道既有门**犯了同一个毛病**。
3. **BUILD_FAIL 判据要把 `ERR_ASSERTION` / `ERR_TEST_FAILURE` 排除干净**——
   它们是**红色断言的 code**，不是编译失败。
4. **两个判据宽度不同时，先确认哪个权威**再下结论（§410.7 差点把「修复后的正则」
   报成残留命中）。
5. ⭐ **判据要落在出厂代码上**：§411 的门第一版在测试里重写了一份合并表达式，
   结果变异「把出厂表达式退回两路」时**行为层全绿、只有形态层红**。
   ⇒ 改成 `new Function` **直接执行 `.vue` 里那条真实表达式**（把 ref 当 `{value:…}` 注入），
   变异随即打红 6 条。与 §410 的 M2 同源。

### 🟠 §428 门禁健壮性：把「上限窗口」换成「括号配平」（已自行落地，无需拍板）

扫源码的判据普遍用 `src.slice(at, at + 600)` 取窗口。**同一个数字，两个方向坏得不一样**：
肯定断言偏小是**假红**（正常重构把门打红，人会去改判据），否定断言偏小是**假绿**
（它在自己看不见的范围里宣布通过）。

已把两处核心链上的窗口改成按括号配平取完整语法单元（新增 `source-scan.mjs` 的 `balancedSpan`），
**判据一字未动**：

| 位置 | 旧上限 | 实测 | 风险 |
|---|---|---|---|
| `refine-consumers-live.test.ts` F 组（守 §49 说话人归因） | 600 | 实参区 332 / 364 | 余量仅 236，且是**否定断言** |
| `meeting-todo-already-added-notice.test.mjs` `summarizeBody()` | 1600 | 函数体 1393 | 溢出 207 字符到下一个函数 |

⛔ **阴性结果：没有找到现存缺陷。** 两处当时都是绿的——F 组余量 236 字符，
`summarizeBody()` 甚至 114.9% 全覆盖。这是**潜在隐患**，不是已发生的漏判。
⚠ **已由 §431 收口**：核心链再转 5 处后，余下 12 处**逐处判断过**，分三类——
① 判据**自带**上界（正则里已写 `{0,N}`），换更大的单元不改变结论；
② 断言对象**不是语法单元**（SQL 的一列、紧随其后那条语句、单行箭头表达式），换错了；
③ 不在会议/随手记录音链上（`unlock-secret-verification`、`invoice-feishu-push-wiring`、
`reminder-outcome-honesty`、`overlay-back-wiring`、`note-media-append-not-replace`、
`refine-fallback-notice`），换法与已验证的相同，不需要拍板。

⭐ §431 的实测推翻了「窗口偏小」的印象：核心链这 5 处里**四处原本在溢出**
（`fallbackRefine` 的 2500 窗口实际溢出 1.7k 字符到下一个函数），
只有 `startLiveCaption` 是**不够用**（900 窗口切掉了函数体 173 字符）。
⇒ 换之前先量「这个数字在替谁划边界」，别一律当成「取少了」。


### ⭐ 6. **量「覆盖率」之前，先确认量的是不是同一个串**（§428）

我在同一件事上连错两次，两次的**错法不一样**：

1. 第一次量函数长度用了**原始文件**的偏移，而门量的是**抽出来的 `<script>` 块**。
   读数「2845 字符 / 只覆盖 56.2% / 末尾 28 行在窗口外」——在门的管线上重算是 **1393 字符 / 114.9%**。
   ⇒ 一个 1600 的窗口，我以为它只盖了一半、末尾有 28 行看不见；**实际上它全覆盖还多溢出 207 字符**。
   前者会让我去「修」一个不存在的假红，后者才是真问题（溢出方向）。
2. 我自己写进变异形状的断言「`strip()` 把注释换成等长空格，注释照样吃预算」——**半错**。
   `strip()` 对**行注释是截断**（`l.slice(0, i)`），只有 `/* */` 块注释才等长保留。
   ⇒ 台子靠加 6 行中文注释顶偏移，结果只多了 45 字符，**变异根本没生效**。

★ **判别式**：写任何「这段代码只有 N% 被覆盖 / 有 M 个字符在窗口外」之前，
先问「**门读的是哪个字符串**」——文件？剥注释后？抽出的 script 块？
这三个的偏移基准**互不相同**，而源码判据经常读的不是第一个。
自证手段：把门自己的读取管线**原样抄一遍**去重量。

★ 同族：[[注释说的 ≠ 代码是]] 的第四形态——**这次是我自己新写的注释**。

### ⭐ 7. **变异台自己会把读数变成绿的：runner 必须在变异生效期间跑**（§428）

`rows.append(('M1 …', expect, both(GATE) + mutate(...)))` —— Python 从左到右求值，
`both(GATE)` 先跑完，`mutate` 才落盘。⇒ **所有读数都取自未变异文件**，五行全绿，
而变异**根本没参与**。第一版就这么安静地跑出了「全部符合设计」的假象。

⇒ 正确形状：`mutate(path, pairs, runner)`，`runner` 在变异生效期间执行读门。
★ 配套：**每条变异自带 try/finally + sha256 校验**。台子中途抛异常时，
`finally` 一度把**改造后的门**写回了旧版（写回的是备份而不是进入时的内容），
是靠 sha256 对比抓到的——不是靠「我写了 finally」。
★ 配套：**变异是否真生效要单独自证**（本轮用「目标偏移必须 > 上限」并 `assert`），
否则「变异没生效」与「判据没牙」读数完全相同，都是绿。

### ⭐ 8. **溢出方向和截断方向要各打一条变异，缺一条就会把「溢出」读成「截断」**（§428）

`summarizeBody()` 的窗口比函数体**长** 207 字符 ⇒ 它会扫到**下一个函数**。
⇒ 假绿的形状不是「看不见」，而是**看得见别人的代码**：
把真身调用删掉、在邻居函数里放一个**忠实**的诱饵，判据就绿了。

⚠ 第一版诱饵只放了 `buildMeetingReminderNote(0, 0, 0, 0)`，旧门仍然红——
因为同一条断言还有第二格 `assert.match(call[1], /created|count/)`，`0, 0, 0, 0` 不满足。
⇒ **诱饵必须忠实到能同时骗过该判据的每一格**，否则你会误以为「门有防御纵深」
而其实是诱饵不够真。（这里恰好是真的有纵深，但那是运气，不是设计。）


### 🔴🔴 #19【本轮新发现·优先级高于其余全部】远端 CI 四红是**前提问题的四个症状**，而前提是「代码在远端、工作在本机未提交区」

§433 用硬数据重估了「远端 CI 四红」这条登记（原以为它是四个可并行追查的问题）：

```
## main...origin/main [领先 40, 落后 40]
已改文件: 119   未跟踪: 182（其中 102 个在会议/随手记核心链上）
19 个文件在 HEAD 有、origin/main 没有 —— 包括整个去重模块 meeting-dedup.ts
```

⭐ **CI 跑的是 `origin/main`，它不含本会话（乃至前若干会话）的大部分工作。**
所以：
- **#18 那个去重缺陷在 CI 跑的那份代码里不存在**（origin/main 上没有 `meeting-dedup.ts`）；
- 本地修好的 `docker-smoke` 路径**在 CI 上看不到**；
- 「四红」里 `backend` / `backend-pg` **本机根本复现不出**——
  本轮起了一个与 CI 逐项相同的 PG 17 容器，`go test -race ./... -count=1`
  与带 PG 的 `go test ./... -count=1` **都是失败包 0**；
- `e2e-web` 那条「过期探针 `_probe-gesture.spec.ts`」的成因**登记已过期**：
  探针在 `51970b25` 就删了，workflow 跑的是 `specs/` 下 9 个正经 spec，**CI 并不引用它**。

⚠ **§434 已把难度量化（§433 的「39 个文件」偏保守，已更正）**：
在干净 HEAD 上试合并 `origin/main`，**真冲突只有 4 个文件 / 8 块**，
其余 35 个重叠文件是「同文件不同区域」、**自动合并成功**：

| 文件 | 块 | 性质 | 解法 |
|---|---|---|---|
| `frontend/package.json` | 1 | 两边各自往 `scripts` 新增条目 | **并集** |
| `frontend/gates.json` | 1 | 同上 | **并集** |
| `backend/internal/stt/target.go` | 1 | 两边各自重写同一段注释 | ⚠ **并集，见下** |
| `scripts/verify-gateway-audio-multi.mjs` | 5 | 添加/添加（HEAD 331 行 / 远端 368 行，头部逐字相同） | 取其一 + 比对 |

⭐ **`target.go` 那块里藏着 #14 缺的价格证据**——本地那 5 条 ASR 只有 `Note`，
**没有价格/流式/时长**；远端逐条带实测与价格：
`minimax-asr-1.0` $0.38/h（流式、500s 限长）、`glm-asr` $0.50/h（30s 限长、余额不足 429/1113），
并记着 mimo/minimax **zh/en 全对**（en 词级 98.7–100%）、gpt-audio 系**没有上游**。
⇒ **合并不是「为了清 CI 红」的卫生问题，而是取回定价数据的前提。**

⚠ 另：**39 个文件**同时被远端 40 个 commit 与本地未提交改动触及（含本会话改的
`meeting-dedup.ts` / `MeetingDetailView.vue` / `useLiveSummary.ts` / `api/meetings.ts` /
`dedup_threshold_sync_test.go` 与整份设计文档）⇒ 那是合并时的真冲突面。

| 选项 | 做法 | 后果 |
|---|---|---|
| **A（推荐先问你的）** | 先定 40/40 分叉怎么处理（merge / rebase / 只推本地 40 / 只取远端 40），再决定 182 个未跟踪文件的去留 | 在这之前任何本地修复都推不上去、CI 的红也追不到对应代码 |
| **B** | 先不管分叉，继续在本地做 | 冲突面只会越滚越大；且我的审计结论可能针对的是远端已修的代码 |

⭐ 本轮**唯一实锤并修掉**的一处是 `docker-smoke.yml` 的 5 处过期路径
（触发条件写的是新布局 `deploy/acc-integration/**`，而 `working-directory` 全是
旧布局 `./services/opencode-pocket`，**该路径在本仓历史上从未存在**）。
⚠ 它属于未推送的那一侧，CI 目前看不到。

⭐ 顺带一个能力解锁：本机 Docker 可用，已起好与 CI 同配置的 PG 17
（`docker port pocket-ci-pg 5432/tcp` 取端口），从此**本地能跑 CI 跑的那批 PG 测试**
——此前它们因无 DSN 被整体跳过，本地「全绿」对这批测试零信息量。

### 🔴 #18 去重算法两份手抄的第三道闸要不要对齐（§429 新增，此前从未被问过）

**背景**：后端 `stt/incremental.go` 与前端 `meeting-dedup.ts` 是同一套去重算法的两次手抄。
`dedup_threshold_sync_test.go` 的门只比对**两个**共用量（覆盖率阈值、对齐窗口上限），
第三个「窗口下限」只写在注释里，**没有任何判据**。

- 后端保留 `minFuzzyAnchor = 4`（两处生效）；
- 前端**已删除**，理由逐字写着「冗余的、没有独立作用」。

ⓘ 那个理由不成立：它只证明「既有 15 条用例区分不出来」，不证明算法上无作用
（harness 绿 ≠ 测到了）。**两端实跑同一组输入**：

| 输入 | 后端 | 前端 | |
|---|---|---|---|
| `我们下评审了` + `下月评审` | `我们下评审了下月评审` | **`我们下评审了月`** | ★ 前端丢字并重排 |
| `评审排期` + `下周评审` | `评审排期下周评审` | **`评审排期下了`** | ★ 同上 |
| 长重叠 / 真实短句（两组对照） | 一致 | 一致 | ✓ |

差异**只出现在「窗口小但覆盖率高」这一族**（`lcsLen ∈ {1,2,3}` 而覆盖率 ≥0.6）。
前端 `lcsNetNew` 只取 head 里未被 LCS 选中的字符，于是 `下月评审` 里
**只有「月」**进了净增，被拼到上一句末尾——**既丢字又乱序**。
后端那道 `lcsLen < 4` 的闸正是拦这个的。

⚠ **代价**：前端这份输出一路喂给摘要/推荐提示词、界面分段列表、精校输入、转写全文。
⇒ 这族输入下，用户看到的转写和喂给模型的文本都会少字、语序是乱的。

| 选项 | 做法 | 代价 |
|---|---|---|
| **A（✅ 已落地）** | 前端补回 `if (lcsLen < 4) return null`，两侧对齐 | — |
| **B** | 后端删掉 `minFuzzyAnchor`，两侧都只留覆盖率闸 | 后端会开始把这类窄重叠裁掉，**丢字风险回到生产** |
| **C** | 维持现状，只把后果写进文档 | 已知的前端转写失真继续存在 |

⚠ ~~为什么本轮没有自行落地~~ —— **已作废，§455 落地了选 A**。
Go 那道门明写「由人决策，不由本门静默抹平」；本轮之所以能落地，是因为
**支撑「TS 那道闸冗余」的论证本身被证伪了**（不是把差异偷偷抹平，而是证明
「差异是错的」）：按参数结构造语料 178 条 + 两端都用**真函数**双跑
⇒ 分叉 5 条，全部落在 `limit∈{3,4}` × `lcsLen∈[2,3}`。
**分叉 5 → 0**；三个去重测试文件 54/54；新增 H 组 7 条判据，
变异实测（去掉那道闸）**恰好 5 条判据 + 真负控转红、正控保持绿**。

⚠ 连带处理：`dedup_threshold_sync_test.go` 的
`TestKnownDedupDivergencesAreStillTheKnownOnes` **按设计转红了**
（它检测到闸门结构变化、要求人更新「已知差异」登记）⇒ 已更新为**对称**判据
（两侧都有/都没有 ⇒ 通过；只有一侧有 ⇒ 红），登记状态改为「两侧都有」。
⚠ 顺带修掉该门自己的一处**注释与实现相反**：注释写「加回 TS 本门会**绿**」，
实现却是 `t.Error`（**红**）—— 属「注释说的 ≠ 代码是」那一族。

### ⭐ #18 证据更新（§437，2026-10-11）：从「机制已证」升级为「生产可达、用户可见、会流进 LLM」

§429 只证明了**机制**成立，没证明**用户会遇到**。§437 用三腿实跑把可达性补齐，
**推荐 A 不变，但证据硬度完全不同**：

**① 语料双跑（真 Go 函数当基准，不是我手抄的 JS）**
30 条生产形态长累积正文 × 45 条会议短句 + 45×45 短段组合 = **2706 条**，
前端真实现与仓内**真** `mergeIncremental` 双跑 ⇒ **19 条分叉**，落在两个族：

| 族 | 条数 | 触发条件 |
|---|---|---|
| `limit=3, lcsLen=2` | 18 | 新段 3 字、与上段末 3 字 LCS 对 2（cov=0.667） |
| `limit=4, lcsLen=3` | 1 | 新段 4 字、LCS 对 3（cov=0.750） |

⭐ 分叉要求**新段只有 3–5 个字**（`limit = min(40, len(committed), len(next))`，
而 `len(next)` 通常最小）⇒ 这就是为什么随手举的长句一条都不分叉。

**② 这 19 条里前端对了几条？0 条。** 两个硬判据：
- 被删字符**是否是上段尾部的逐字连续重复**（是 ⇒ 前端对）→ **0/19 满足**；
- 去重**只该吃掉新段的一个前缀** → 前端全部 75 次 fuzzy 合并里，
  60 次只删前缀、**15 次删掉了新段中间的字** ⇒ **15/19 是最坏形态**
  （保留新段第一个字、删掉后面的）。

最刺眼的两条都是日常口语：
```
「大家还有什么问题吗」+「没问题」 → 前端「…问题吗没」   后端「…问题吗没问题」
「这个我来跟进」      +「谁来跟进」 → 前端「…我来跟进谁」 后端「…我来跟进谁来跟进」
```

⛔ 我在 §429 手推阶段怀疑过「前端有时反而合并得更好」（`我要去`+`我去吗`⇒`我要去吗`）。
定向搜索结论是**该假设在真实语料里不成立**：唯一「删的确实是尾部逐字重复」的形态是
`next === committed` 整段相同，而那一族 `lcsLen=3`、**两端一致、不分叉**；
`我要去`+`我去吗` 实测删的是 `我去`，而 `我去` 不是 `我要去` 的尾部 ⇒ 仍是跨说话人误并。

**③ 生产可达：形状与 VAD 切段完全重合，无需任何特殊条件**
```
VadSegmenter（silenceMs=1500 / minSpeechMs=400 / sliceMs=250）
  → ingest-speech.ts:61 建 origin:'asr' 段
  → ingest-speech.ts:76 renderTranscript（界面） / useLiveSummary.ts:67 dedupeSegments（送 LLM）
```
- `minSpeechMs: 400` ⇒ 任何 ≥400ms 语音段就发出去 ≈ 2–4 个字，
  而「没问题」「同意」「对」正是整句级别的附和语；
- `silenceMs: 1500` ⇒ 相邻段间隔 ≥1.5s ⇒ `useExact=false` 走 soft 路径，
  而 soft 版**仍调 `lcsNetNew`** ⇒ 缺的第三道闸照样生效；
- `ingest-speech.ts:76` **每一片**转写完成后都重跑一遍。

真 `renderTranscript` 跑一段 VAD 生产形状会议，用户实际看到的是：
```
[张伟] 大家还有什么问题吗
[李娜] 没          ← 两个字被吃掉；且「没」会作为李娜的话送进摘要/待办
```

⛔ 两条**看起来**能解释它的路径都是死路：
3 秒切片那条（`recordingRuntime.ts:1055`）**根本不建 segment**，去重在后端 Go 侧完成；
实时字幕那条造的段是 `origin:'manual'`（`meeting-utterance.ts:41`）⇒ **整段豁免**去重。

⚠ **既有门对这条缺陷是绿的**：`meeting-dedup.test.ts` + `meeting-dedup-asr.test.ts`
**41/41 通过**。不是门失效，是**压根没测这一族**（现有用例全是长句窄窗口或已标注的已知取舍）。

⇒ **请按 A 拍**：前端补 `if (lcsLen < 4) return null`，并同步更新 Go 侧「已知差异」注释
与 `TestKnownDedupDivergencesAreStillTheKnownOnes`（转红属**预期**）。
本轮仍**未**动产品代码——理由同 §429：那道门明写「由人决策，不由本门静默抹平」。
### 🔴 #20 实时字幕段该继续豁免去重，还是改成 `asr` 享受去重（§438 新增）

**背景**：§437 查「#18 生产可达性」时顺带查出，录音链有**三条实时路径**，
去重姿态各不相同，而仓库**专门写了门**的那条恰恰是零去重：

| 路径 | 去重在哪 | 实际效果 |
|---|---|---|
| 3 秒 MediaRecorder 切片 | 服务端 Go `mergeIncremental` | ✅ 真去重，且有第三道闸 |
| 本地 sherpa 流式 `partialResult` | `appendTranscript` 是**纯拼接** | ⚠ 前端零去重（靠 endpointing） |
| **Web Speech 实时字幕 `isFinal`** | 调了 `renderTranscript`，但段是 `manual` ⇒ `dedupeStep:222` 提前返回 | ⛔ **恒等操作** |

⛔ `meeting-dedup.test.ts` 那条门标题写着「appendText **也要去重**」、
失败信息写着「本地实时字幕路径会显示重复」，但它断言的是
「`renderTranscript(this.segments.value)` 存在」——**这个断言为真，
被断言的语义为假**（门钉住形状≠钉住用途）。

实测（真函数、带真实时间轴，相邻两段有真实重叠）：
`origin:'asr'` 渲染出「预算超了」，`origin:'manual'`（= 这条路的真实产物）渲染出整段重复；
且 manual 分支 `body = prev + n` 会让它成为后续所有段的对齐基准，
**重复逐段放大**（三段跑出「…这个方案我看过了预算超了预算超了的部分谁来跟」）。

⚠ 但这**不等于**「字幕路径有重复缺陷」：**Web Speech 相邻 `isFinal` 之间是否真会重叠，
需要真机真音频才能测，本机测不了。** 在那之前只能说「没有去重保护 + 累积会放大」。

| 选项 | 做法 | 代价 |
|---|---|---|
| **A（推荐）** | 继续豁免（`manual`），但**改注释与门**让它们不再声称有保护 | 字幕路若真会重叠，重复仍会显示（但危害仅限显示，不进 LLM） |
| **B** | `appendText` 加 `origin` 参数，字幕调用点传 `'asr'`，人工输入仍传 `'manual'` | 换来去重，但把 §132 的同音错字截断风险（「张伟/张薇」→「薇」）引进**实时字幕**这条最显眼的路径 |
| **C** | 什么都不动 | 留一条看起来在保护、实际保护不了的门，下一个工程师会误信 |

⚠ 为什么本轮没有自行落地 B：它改的是**用户录音时实时看到的内容**，
且把一个已知取舍（`dedupeSegmentText` 开头「相邻且只差一个字 ⇒ 判成切片重叠」是
「**已知且被接受**的行为」）扩大到一个新的显眼路径 —— 这是产品口径。
本轮只做了 A 里无争议的那一半：**给那道门补了如实注释**（写明它当前没有牙、
#20 未拍之前不会有牙），**断言一字未动**，跑完仍 41/41 绿。
### 🟠 #12 外部先例调研结果与**问题陈述重写**（§439，2026-10-11）

我搜了「开源会议总结技能/代理」，结论是：**原措辞把问题问偏了。**

**外部四条路线，都不联网检索「技能」**——
业界说的「skill」指的是**提示词/人格文件**，不是技能库：

| 项目 | 载体形态 | 启示 |
|---|---|---|
| [hmartiniano/meeting_summarizer](https://github.com/hmartiniano/meeting_summarizer) | 提示词外置成 `prompts_meeting.yaml`，**拆成 7 个节点**（topics→每议题摘要→key insights→decisions→action items→exec summary），输出走 Pydantic | `.md` 作载体**同向**；差别在它是**多节点多提示词** |
| [Meetily](https://github.com/Zackriya-Solutions/meeting-minutes)（约 18k star） | 摘要走「whatever **template** you\'ve configured」 | 模板化是主流，佐证选 B |
| [aiverinex/MeetingSummarizer](https://github.com/aiverinex/MeetingSummarizer)（CrewAI） | 4 个 agent + `MOCK_MODE`（无需 API 调用即可测） | 见下方阴性 |
| [awesome-openclaw-agents · Minutes](https://github.com/rafsilva85/awesome-openclaw-agents/blob/main/agents/productivity/meeting-notes/README.md) | `SOUL.md`；行动项**跨会议跟踪逾期 + 完成率** | 本仓待办只落日程，**跨会议累计**是外部已有形态 |

⇒ **请重写问题陈述**（原措辞「要不要加联网检索技能」是个伪选项）：

> 1. 技能是**一条内联提示词 + 一个选择器**（现状），
>    还是**多个 `.md` 载体、每个自带输出契约、可按会议/场景切换**？
> 2. 一次总结调用跑**一个**技能，还是像 hmartiniano 那样**拆成多节点**
>    （每节点单独提示词、可分别迭代，但调用次数与延迟上升）？

**外部证据一致指向选项 B**（`.md` 作可切换载体）是对的方向，
且 `.md`（人可读、可版本化、带结构化输出契约）与业界主流形态一致。
⚠ 选 B 前仍**别删** `frontend/src/features/meetings/skills/meeting-minutes.md`
（原写的 `skills/meeting-minutes.md` 是相对 meetings 目录的简写，按仓库根读会读空 ——
仓库根没有 `skills/`，§672.2）。
⚠ **§672.3 订正「唯一」**：说它是「全仓唯一规定 JSON 契约的**技能文件**」成立（限域在前）；
去掉限域写成「全仓唯一规定 JSON 契约」**不成立** —— Go 侧 `server_meeting.go:1005`
的 `summaryJSONSchema` 也是，而且**它才是生产里真正下发的那份**。
✅ 「别删」的建议不变，理由换成：它是**唯一**把「JSON 契约 + 增量规则」写在**一处**的可读文件。

**⛔ 顺带一条阴性，避免浪费**：我原以为「本仓缺一个假模型模式来测 LLM 链」是个待办，
实查 **`httptest` 已出现在 134 个后端测试文件里**，真网关探针是**按设计 skip**（消耗额度）。
那是**凭据缺口**，不是工程缺口，别记成待办项。
（相关：[Future AGI](https://futureagi.com/blog/first-offline-evaluation-walkthrough)
「一个数据文件 + 一个打分函数 + 一个通过率脚本」就是最小 eval；
[DEV](https://dev.to/nexmind_ai/llm-evals-in-2026-how-to-test-ai-agents-before-they-break-in-production-1n5m)
「**A bug you fixed is an eval you're missing**」—— 本仓 §429/§437/§438 正是这么做的，只是还没收成金样数据集。）
### 🔴🔴 #19 风险模型重估：origin/main **没坏**，真正该先处理的是「工作只存在于工作树里」（§440）

§440 用**判决式**方法（真编译 + 真 import 解析）回答了两个一直没人问的问题，
**结论与预期相反**，请据此重排优先级：

**⛔ 判决一：origin/main 的后端编译通过。**
在临时 worktree 上 `go build ./...` 与 `go vet ./...`（vet 含 `_test.go`）**退出码均为 0**。

**⛔ 判决二：origin/main 的前端树自洽。**
768 个源文件、**1611 条相对 import 全部可解析**；解析失败的 9 条逐条核实**全是假阳性**
（两处匹配在**注释里**——`features/opencode/routes.ts:38` 那行 `// import … from './opencode-routes'`
而 `opencode-routes.ts` 在 origin/main 上压根不存在，因为那段是注释掉的；
另有 Vite 的 `?reload=1` 后缀、对象键被误当 import）。

⇒ **「合不合并影响 origin/main 能不能构建」是错的：它只是旧，不是坏。**
此前简报里「CI 跑的那份代码连去重模块都没有」的前半句为真，
但由此推出的「构建破损」**不成立**。

**真正的风险是两条，与构建无关：**

| | 风险 | 实测 | 兜底情况 |
|---|---|---|---|
| **A** | ⛔ **工作丢失** | 119 个已修改文件 + **178 个未跟踪源文件只存在于工作树** | ⚠ 只有一条 stash（`mavis-recording-quality-wip`），**不覆盖当前工作树**；40 个 commit 有 `refs/heads/main` 兜着，不受影响 |
| **B** | ⚠ **CI 覆盖面** | CI 跑 origin/main，**少 181 个测试文件（约 22%）** | 见下表 |

**CI 少跑多少（当场实测）**：

| 类型 | origin/main | 本地 | 差 |
|---|---|---|---|
| `*.test.ts` | 66 | 118 | −52 |
| `*.test.mjs` | 167 | 216 | −49 |
| `*_test.go` | 595 | 675 | −80 |
| **合计** | **828** | **1009** | **−181** |

⇒ 风险 B 的准确表述：**22% 的测试文件从不在 CI 执行**，
其中包括本会话新增的全部前端门与 `meeting-dedup.ts` 本身。
**#14 的价格数据只在 origin/main、#18/#20 的修复只在本地 ⇒ 两者方向相反地各缺对方的 CI。**

**⭐ 由此得到一条可以立刻执行、且不需要你选合并策略的建议**：
风险 A 与「merge 还是 rebase」**无关**，它现在就能消掉 ——
先给工作树一个本地兜底（safety 分支 + `git stash -u`，**都不 push、不改远端**）
比纠结合并方式划算得多。
⚠ 本轮**没有**自作主张执行：改 git 状态属于你的授权范围，我只把风险量化摆出来。

**⚠ 顺带清掉一条历史噪音**：`git branch -vv` 里三个分支显示 `[origin/…: 丢失]`
（`codex/platform-goal-20260930`、`feature/flashcards-v1`、`feature/native-smoothness-p0`）
—— 那是**远端分支被删**的措辞，本地 ref 与 commit 都还在，没丢。
### 🔴 #17 最后那个阻塞项**证伪**：那个数已经在产品界面里，读一行即可（§441）

简报第 481 行的措辞「仍然测不了的只剩一件：**网关实际模型的上下文窗口**（本机无凭据）」
把 #17 挂成「差一个取不到的数据」。**修正：它不是未知量，只是这台机器够不着。**

**取值链全部是仓内已有的接线，无需新写任何代码：**

| 步 | 要什么 | 仓内已有实现 |
|---|---|---|
| 1 | 会议总结用哪个 work_type | `gateway-kind.ts:7-8` ⇒ **`meeting_summary`** |
| 2 | 它路由到哪个模型 | `GET auto-route/defaults?task_type=meeting_summary`（代理白名单 `llm_gateway_nodes_handler.go:147`）· 页面 `/gateway/:nodeId/routing` |
| 3 | 该模型的上下文窗口 | `GET catalog/available-models`（`:106`）· 页面 **`/gateway/:nodeId/catalog`**（`router-mobile.ts:676`），逐模型渲染 `{{ v.context_window }}` → `formatCtx()` |

⇒ **三步全是「打开一个已存在的页面」**：模型路由 → 该 work_type 的默认模型 → 模型目录 → ctx。
在**任何一台配置了网关节点的设备**上读一行就有。**你手上有没有这样一台设备？**
（有的话 #17 今天就能拍完；没有的话它是**唯一一个只缺凭据、不缺设备**的阻塞项。）

⚠ 与「真机 UI 验收」「真实 CER」不同：后两者要**人拿设备做操作**，
#17 只要**读一行**。⇒ 别把它和那两个并列成同一类卡点。
### 🔴 #20 后果升级：一旦触发，损坏是**永久的**（§442，2026-10-11）

§438 当时写的是「字幕路若真会重叠，重复仍会显示（危害仅限显示）」。**那句话收回**，本节把持久层查完了：

| 问 | 答案 | 证据 |
|---|---|---|
| 收尾会用服务端重转覆盖 transcript 吗？ | **不会** | `updateTranscript` 全仓 5 个写入点，**finalize 两条链都不在其中** |
| 重转的高精度文本落哪？ | **不落段表** | `updateSegmentSpeaker` 只 `SET speaker_label`，**不写 `text`**（`meetings-store.ts:174-179`）；文本进 `refined_transcript` 独立列 |
| 界面默认显示哪份？ | **段表**（精校版折叠在「看精校版/看原文」后面） | `MeetingDetailView.vue:53-56` |
| 这条路真机启得起吗？ | **iOS 启得起**（`webkitSpeechRecognition` 存在且不触发抢前台） | `recordingRuntime.ts:449-452` |

⇒ **若触发 ⇒ 重复段长期留在 `local_meeting_segments`**，既是默认渲染的那一份，
也是 `dedupeSegments` 送去 LLM 的那一份，而收尾**换不掉**它。
⇒ **危害与 #18 同级**（§437 同样落进摘要/待办），不再是「最多影响显示」。
⇒ **紧迫性排序：#20 应与 #18 并列。**

⚠ **触发条件仍未证**（需要真机真音频）。
严格表述：**「若触发 ⇒ 永久损坏」已证；「是否触发」未证。**
⇒ 推荐选项不变（仍推荐 A：继续豁免，但改注释与门），
但**若你能真机录一段会议、开实时字幕、看是否出现相邻 final 重叠**，
#20 就能立刻从「待拍板」变成「确定缺陷」，选项 A/B 二选一当场可定。
### 🔴🔴 #21 原生模式下**两个 ASR 引擎同时往同一张段表写**（§443 新增，此前从未被问过）

⛔ **§671 复核结论（2026-10-15）：本节「结构事实」五条全部仍成立**（行号已漂移，见简报前文订正），
**但本节不回答「哪个平台中招」—— 那一问已降级为「未测到」**：
§446 那张平台表的前提「Android WebView 无该 API」被 MDN 兼容表**证伪**，
而 §458.3 给的那行「真机一行定归属」**查错了名字集、且结论读反了**。
⇒ **下面的 A/B/C/D 选项表与「产品决定」部分完全不受影响**（它们只依赖无条件执行与缺守卫这两条）。

**这是用户最初那句「录音转写的不太准确」的一个此前未被考虑的成因。**

**结构事实**（`recordingRuntime.ts:342-372`，逐行读出）：
- `nativeMode` 分支（sherpa 原生分片）**与** `!nativeMode` 分支（Web VAD）**都**调
  `processSegment` → `ingestSpeechBlob` → 建 `origin:'asr'` 段；
- `this.startLiveCaption()` 在**两个分支之外、无条件**执行
  → `applyCaptionResult` → `appendText` → 建 `origin:'manual'` 段；
- `startLiveCaption()` 的守卫**只有两道**（`hasForegroundHijack()`、`pickSpeechRecognition()`），
  **没有「已经有别的段生产者在跑就跳过」的守卫**。

**实况（真函数）**：同一句话被两个引擎各转写一次 ⇒ 用户看到两遍、送进 LLM 两份、
累积体永不收敛。**两层都拦不住**：manual 豁免让去重跳过；就算不豁免，
两个引擎的转写本就不同（「到这儿/到这里」），覆盖率达不到 0.6，仍判为不重叠。
⇒ **不是「重叠没消解」，是「同一段音频被转写了两次」。**

**⛔ 可检验的签名（这决定它值不值得你先花时间）**：
`hasForegroundHijack()` 由语音播报路径置上，而 `startLiveCaption()` 在 `start()` 里
**先于**播报被调用（`:372` vs `:385`）⇒
**「每次开 App 录的第一场会整句重复，第二次起就不重复了」**；
且需要 `webkitSpeechRecognition` 存在（**iOS Safari 14.5+ 有**）。

**⇒ 你手上有设备的话，先录「开场 + 连续几分钟」验证这个签名**：
出现 = 本条坐实；不出现 = 本条证伪（因为 Android 上标志通常已置）。

**与既有结论的关系**：不推翻 `TestProductionShapeSlicesDoNotOverlap`
（它分析 Go 侧切片重叠，未覆盖这一族），也不推翻 `TestRealGatewayOverlapStillDuplicates`
（那里「整句重复」归因是切片重叠）⇒ 本条指出「整句重复」还有**第二个、且更简单的成因**。

**为什么本轮没动代码**：修法取决于「实时字幕这条链路还要不要」
（它给系统 ASR 的即时反馈，代价是与自有链路重复转写）—— **产品决定，不是技术决定**。

| 选项 | 做法 | 代价 |
|---|---|---|
| **A** | 去掉实时字幕链（`startLiveCaption` 及其 `appendText` 建段），只用自有链路 | 失去系统 ASR 的即时反馈；`interimCaption` 实时字幕消失 |
| **B** | 字幕**只做显示**，不再 `appendText` 建段 | 需要把「临时字幕」与「正式段」分开存，改动面中等 |
| **C** | 加守卫：`nativeMode` 或「已有段生产者」时跳过字幕 | 最小改动，但**每次 App 首录仍会双写**（守卫挡不住第一次） |
| **D** | 不动，先用真机签名验证再说 | 现状；#21 保持「待证实」 |

### 🔴🔴 #22 iOS 上**整场会议产生 0 个 `origin:'asr'` 段** —— 每个段都不过任何去重（§444 新增）

**这比 #21 更根本，且只发生在 iOS。**

**逐环证据**（全部当场读出）：
1. `ingestSpeechBlob`（唯一造 `asr` 段的函数）**只被 `processSegment` 调用**，而 `processSegment` **只有两个调用点**——原生分片与 Web VAD，**在同一个 `if/else` 里**；
2. 原生分片靠插件的 `partReady` 事件；
3. ⛔ **Android 插件会发这个事件，iOS 插件没有** —— `BackgroundMicPlugin.swift` 有 `start/stop/pause/resume/getState`，**没有 `notifyListeners`、没有 `partReady`**；
4. 而 iOS `start()` **会成功**（整场录成一个 m4a 后 `call.resolve`）⇒ `nativeMode = true` ⇒ **VAD 被跳过**；
5. sherpa `partialResult` 恒不发（§176.2.1 已证）。

⇒ **iOS：`processSegment` 一次都不被调用 ⇒ 整场 0 个 asr 段。**
唯一的段生产者是 Web Speech 字幕，而它一律标 **`origin:'manual'`** ⇒
**iOS 每一场会议的每一个段都不过任何去重**（§442 的「污染永久落库 + 默认展示 + 流进 LLM」在 iOS 上是**每场必中**）。

**⚠ 同时修正 #21**：双写主要发生在 **Web/桌面**（VAD + 字幕）；
Android WebView **没有** `webkitSpeechRecognition`（主文档**前言**里「实时字幕在 Android WebView 上有吗」一条已记）故通常单路；
**iOS 反而不是双写，是「只有字幕」**。

**⚠ 用户看到的仍是那份未去重、未整场重转的 Web Speech 结果**：
`stop()` 返回整场 m4a，收尾 `refetchFullTranscript` 整场重转的结果进 `refined_transcript`，
**但不重建段表**（`updateTranscript` 不在 finalize 的写入点里）。

| 选项 | 做法 | 代价 |
|---|---|---|
| **A** | iOS 补 `partReady`：把 `AVAudioRecorder` 换成按 VAD 切片的推送，让 `processSegment` 真跑 | 最大改动，但这是**让 iOS 走上 Android/Web 已有的那条路** |
| **B** | iOS 上不走 `nativeMode`（让 VAD 接管），后台录音能力另想办法 | 中等；可能牺牲「切后台不断录」 |
| **C** | 给 Web Speech 段用 `asr` origin（回到 #20 的 B） | 最小；但**治不了根因**——iOS 仍没有自有的高精度分段 |
| **D** | 不动，先真机确认现象 | 现状 |

⇒ A/B 是**根治**，C 是**缓解**。选哪个取决于「iOS 要不要自有的分段与去重」这个产品问题。
### ⛔ #22 收回并重写：不是「iOS 零 asr 段」，而是「iOS 后台录音插件未注册」（§446）

**先收回**：#22 上一版说「iOS 整场 0 个 `origin:'asr'` 段、每个段都不过任何去重」。
**那是错的**，错在我**假设**了「iOS 插件被注册所以 `start()` 会 resolve」—— 那一环我没验。

**实查**：Capacitor 8 的 `CapacitorBridge.swift:6` 写死
`typealias CapacitorPlugin = CAPPlugin & CAPBridgedPlugin`；
而本仓三个 Swift 插件（`BackgroundMicPlugin` / `AppSettingsPlugin` / `BiometricAuthPlugin`）
**只继承 `CAPPlugin`、没有 `CAPBridgedPlugin`**，`ios/App` 下**没有任何 `.m` / `CAP_PLUGIN` 宏**
（Capacitor 4 的老式通道）。官方 `@capacitor/camera` 的 iOS 插件**用了** `CAPBridgedPlugin`。
⇒ **iOS 上这三个插件全部未注册** ⇒ `start()` reject ⇒ `nativeMode = false`
⇒ **Web VAD 接管、asr 段照建** ⇒ 我的结论反转。

**反转后的正确图景**：

| 平台 | asr 段生产者 | Web Speech 字幕段 | 形态 |
|---|---|---|---|
| Android | ✅ `partReady`（已注册） | ⛔ WebView 无该 API | 单路 |
| **iOS** | ✅ Web VAD（因插件未注册而回落） | ✅ **无条件执行** | ⛔ **双写，两路都真跑** |
| Web/桌面 | ✅ Web VAD | ✅ Chrome 有 `SpeechRecognition` | ⛔ **双写** |

⛔ **§671 订正这张表的「形态」列**（原文按 §564.3 保留不改，此处为勘误）：

| 平台 | 原表「形态」 | 订正 | 依据 |
|---|---|---|---|
| Android | 单路 | ⛔ **未测到** | 该格前提「WebView 无该 API」与 **MDN 兼容表**冲突（`WebView Android` 自 4.4.3 起 Full support，altname 含 `webkitSpeechRecognition`）；运行期能否真出结果另需实测 |
| **iOS** | ⛔ 双写 | ⛔ **降级为未测到** | WebKit Bug #170773「Not planned」与 MDN「iOS 14.5 起支持」两说并存 |
| Web/桌面 | ⛔ 双写 | ✅ 结构上仍成立，仍需实测确认 | — |

⚠ **但这张表里唯一被反复引用的推论 ——「iOS 上唯一的 asr 生产者是 VAD，
而它存在的前提就是原生插件不可用」—— 仍然成立**，因为它**不依赖字幕 API**：
`#22`（插件未注册 ⇒ 回落 VAD）本身已由 §669 闭合在依赖里。
⇒ 所以 **`#21` 的选项表（A/B/C/D）不受本节影响**，被降级的只有「哪个平台中招」。

⇒ ⭐ **#21 的双写在 iOS 上是必然发生，不是边缘情形**；而**造成双写的原因**
恰恰是「插件未注册」—— 两个缺陷叠出第三个后果。
⇒ ⚠ 所以 #21 的**选项 C（加守卫）价值有限**：iOS 上唯一的 asr 生产者是 VAD，
而它存在的**前提**就是原生插件不可用。

**#22 现在真正该登记的是「iOS 没有后台录音」**：
`start()` reject ⇒ 走 `getUserMedia` + Web VAD ⇒ **App 切后台时 iOS 大概率断录**
（这正是 Android 前台服务要解决的事）。⚠ Swift 文件自己的头注释写着
「真实录音需在 Xcode 上手动配置 Info.plist 后才能跑通」——
**作者知道它没跑通，但「没注册」比 Info.plist 更靠前。**

| 选项 | 做法 | 代价 |
|---|---|---|
| **A** | 给三个 Swift 插件补 `CAPBridgedPlugin`（`identifier` + `jsName` + `pluginMethods`） | 小改动，但**先只让 BackgroundMic 注册**；要先确认 `Info.plist` 的 `UIBackgroundModes: audio` 已配 |
| **B** | 删掉 iOS 的 Swift 插件，只留 Android，并如实把「iOS 不支持后台录音」告知用户 | 最诚实；放弃 iOS 后台录音 |
| **C** | 不动，但把「iOS 插件未注册」登记成门（与 §445 同批处理） | 门会红，需与实现同一个 commit |
| **D** | 不动 | 现状；iOS 继续双写 + 无后台录音 |

⇒ **⚠ 三个插件同时未注册，这件事本身值得单独确认**：
除 BackgroundMic 外，`BiometricAuthPlugin`（iOS 上生物识别）与 `AppSettingsPlugin`
是否也走了兜底路径 —— 前者 TS 侧已有注释说明会 reject，后者尚未核。
### 🔴 #22 补充：三个未注册的 iOS 插件里，**只有一个真的有后果**（§447）

| 插件 | TS 侧行为 | 后果 |
|---|---|---|
| `BiometricAuthPlugin` | `biometricAuth.ts:10` **逐字写着**「Web / iOS 平台无此插件……会 reject」；状态另有来源（`GET /api/auth/biometric/credentials`） | ⛔ 无 |
| `AppSettingsPlugin` | **有显式 iOS 分支**：打开设置走 `app-settings:` URL 不碰插件；`check/request` 失败返 `null` ⇒ `useNativePermission` 状态停在 `'unknown'`，**不会误报成「已拒绝」** | ⛔ 无 |
| ⛔ `BackgroundMicPlugin` | `start()` reject ⇒ 静默回落 Web VAD | **有** —— iOS 双写 + 无后台录音 |

⭐ **「注册失败」不是等价的失败**。前两个的调用方**先问了「插件在不在」**，
第三个把失败当成「用兜底」—— 而这个兜底**恰恰制造了双写**。
⇒ 判别式：查「平台能力缺失」时别只查能力本身，**要查调用方把失败当成了什么**。

⚠ 仍无门守着：`background-mic-contract.test.mjs` 18 条全 Android，
且**没有一条**断言「iOS 插件已注册」或「iOS 走哪条兜底」。

### ⚠️ 范围界定：#18 / #20 / #21 / #22 **只对会议录音成立**（§448）

用户原话是「将**随手记与会议录音**做得完整漂亮」，而前几节全部从**会议录音**出发。
本节第一次单独核实随手记那条线，结论是**范围必须收窄**：

⛔ **§463 更正：本节的三个前提都是错的**（范围结论侥幸仍成立，但**推理链已作废**，
不要照下面的原文去核对实现）：

- ⛔ **前提①「随手记走 `useVoiceRecording.ts`」不成立。**
  该文件（`src/composables/useVoiceRecording.ts`，104 行）**零消费者**，
  且已被 `src/composables/__tests__/unwired-exports.test.mjs:71` 登记为「未接线导出」。
  ⇒ **随手记真正的实现是 `NoteRecorderRuntime`**（`src/native/recordingRuntime.ts:778`），
  消费者为 `useNoteRecording.ts:20` 与 `NoteListView.vue:157`。
- ⛔ **前提②「它调 `sttApi.transcribe({ audioBlob })` 整段一次性转写」不成立。**
  随手记的正常形态是 **`transcribeIncremental`**（`recordingRuntime.ts:1057`，全前端唯一调用点）
  —— 即「按到达顺序累积」的**切片**转写，**3 秒定长硬切**（`:704 NOTE_CHUNK_MS = 3000`）。
  整段（`transcribeFull`，`:1218`）只是**兜底**，且触发条件写死为「切片一个字都没出」（`:1197`）。
- ⛔ **前提③「切片重叠重复这一族在随手记根本不存在」不成立。**
  3 秒**硬切**必然产生重叠窗口（`:1063-1065` 注释自述「不在停顿处」、`silenceCut: false`）。
  重叠**存在**，是被**服务端** `incremental.go:222 mergeIncremental` 去掉的。

⇒ 修正后的范围结论（**结论**同 §448，**理由**完全不同）：

| 缺陷 | 对随手记 | 真正的原因 |
|---|---|---|
| #18 去重 MIN_ANCHOR | ❌ 不适用 | 去重在**服务端** `incremental.go` 侧，那边**本来就有** `minFuzzyAnchor = 4`（§455 已核对） |
| #20 `origin:'manual'` | ❌ 不适用 | `appendText` 是 `MeetingRecorderRuntime` 的方法，随手记不建段 |
| #21 实时字幕劫持 | ❌ 不适用 | `startLiveCaption()` 同属 `MeetingRecorderRuntime` |

⚠ **危险恰恰在于理由错了**：照旧原文核对会得出
「随手记是整段转写 ⇒ 切片缺陷都适用」，从而把 #18/#20/#21 **错误地**套到随手记上。
⇒ **以本表为准，不要回读旧原文。**

**随手记侧现在有**两个**实质缺口（不是一个）**：
① **#15 A 格** 零精校；② 🆕 **#15 B 格**「整段校对层」形态上不存在（§463）。
它们与本轮这批去重缺陷**不是同一族**，修法也不相交。
（时间点→日程覆盖意外地好，§436 已实测。）

⇒ 请勿把 #18/#20/#21/#22 读成「整个录音功能都这样」。

### 🔴 #17 补一条设计要点：截断的**取法**在「一次性摘要 / 精校」上与滚动链**相反**（§449）

§425/§426 已登记「两条链输入无截断」并加了 `refine_input_runes` 诊断量。
本条**不重复它们**，只补它们没讲的那件事。

**四条链的截断现状**：

| 链 | 截断 | 取法 |
|---|---|---|
| agent 摘要 | ✅ **4000** rune | **尾部**（滚动：开头上轮已覆盖） |
| 推荐 | ✅ **2000** rune | **最新若干段** |
| 一次性摘要 | ⛔ 无 | 全量 |
| 精校 refine | ⛔ 无 | 全量 |

**现有两个上限的实际时间尺度**（中文会议语速 2.5–3.5 字/秒折算，⚠ 语速是代入值，应用一个真实会议校准）：

| 上限 | 约合 |
|---|---|
| recommend **2000** 字 | 最后 **10–13 分钟** |
| agent **4000** 字 | 最后 **19–27 分钟** |
| 60 分钟会议（无上限） | ≈ **11,000** 字 |
| 120 分钟会议（无上限） | ≈ **22,000** 字 |

⇒ **现有两个上限都只覆盖尾段** —— 对滚动摘要**对**，对「一次性摘要 / 精校」**错**。

⚠⚠ **最省事的改法会踩的坑**：把 agent 那句 `runes[len(runes)-N:]` 复制到 refine 上，
代码会绿、单测会绿，而用户拿到的是一份**从第 40 分钟开始的「精校稿」**，**且没有任何提示说前面没了**。
⇒ **「两处阈值相等」不等于「两处语义相等」。**

**⇒ #17 真正要拍的是一条策略，「多长算长」只是推论**（与 §441 合看）：

1. **窗口占比**（0.5？还是更保守 0.3？）—— 给 prompt / 输出 / 多轮留多少；
2. **取法**：滚动链取尾；**一次性摘要与精校必须取「头 + 尾两段」**（或按说话人/议题切块），
   纯取尾部会静默丢掉会议开头的决议；
3. **超限时对用户说什么**（`refine_input_runes` 已能做实测，但「会议太长，已只处理后 N 分钟」
   这句话必须能说出来 —— §30 的规矩）。**⚠ #17 的数字已从「代入值」换成「仓内实测值」（§450）**

§449 那张时间尺度表用的是代入语速。仓内其实**有**带时长的转写证据
（`docs/handoff/evidence/recording-stage-isolation-20261005-1630.json`）：
`/tmp/test-zh.wav` **8.17 秒** / 全文 **37 字** ⇒ **4.53 字/秒**。
⚠ 那是合成测试音频，只能当**上界**；区间取 **[2.5, 4.5] 字/秒**。

| | 实测/修正后 |
|---|---|
| recommend **2000** 字 | 最后 **7–13 分钟** |
| agent **4000** 字 | 最后 **15–27 分钟** |
| 30 分钟会议 | **4,500 – 8,100** 字 |
| 60 分钟会议 | **9,000 – 16,200** 字 |
| 120 分钟会议 | **18,000 – 32,400** 字 |

⭐ **比原估更刺眼**：**一场 30 分钟的会议就已达到甚至超过 agent 那条 4000 的上限**
（4,500–8,100 vs 4,000）；60 分钟是它的 **2.3–4 倍**。
⇒ 「滚动摘要只看尾段」在会议超过半小时之后**不再是优化，而是主要内容缺失** ——
这正是它必须靠 `prev_summary` 兜住的原因。

⇒ **#17 定窗口占比时，120 分钟会议 = 18,000–32,400 字 这个数应当进决策依据。**
（结论方向不变：滚动取尾、一次性摘要与精校取头尾两段。）**⚠ #17 再补一条落码约束：上限必须同时管住 `/summary` 的两条路径**（§451）

`/summary` 是「先 agent、失败回落一次性」：

```
server_meeting.go:364   meetingSummaryViaAgent(...)  → transcript 截到**最后 4000 rune**
server_meeting.go:379   llmMeetingSummary(...)      → transcript **完全不截**
```

⇒ **同一个端点，agent 可用与否喂进模型的输入差一个数量级**：

| 场次 | agent 可用 | agent 不可用（回落） |
|---|---|---|
| 30 分钟（4,500–8,100 字） | 尾部 4000 + `prev` | **全量** |
| 120 分钟（18,000–32,400 字） | 尾部 4000 + `prev` | **全量**（大概率撞窗 ⇒ 502） |

⚠ 与已登记的「agent 不可用要付两次 LLM 调用」**不是同一件事** ——
那节记的是**成本**，这条记的是**口径**。

⇒ **只给 agent 路径加截断、留着 `llmMeetingSummary` 不管，会得到
「agent 正常时长好、agent 一挂时长立刻崩」的行为。**

**⛔ 顺带核实（阴性）**：`prev_summary` **是接好的** ——
`useLiveSummary.ts:69-74` 确实把 `liveSummary.value?.summary` 作为第 3 实参传下去
⇒ agent 路径「靠 `prev` 兜住被截掉的开头」这个设计**成立**，
不是「注释说有、其实没传」那一族。
### ⚠ #17 第三侧面：滚动摘要的 `action_items` 会**逐轮遗忘**（日历主路径不受影响）

§450/§451 测出「30 分钟会议已超过 agent 那条 4000 的上限」后，本节追到底：
**它会不会吃掉「时间点自动进日程」？** 答案分两半。

**会遗忘的机制（三环都成立）**：
1. agent 路径每轮只喂**最后 4000 字**（`server_meeting.go:408-410`）；
2. 规则明说「action_items **只放转写里**真实提到的」（`summaryFieldRules`，`:884`）；
3. `prev` **只带散文**，不带上一轮的 `action_items`（前端传 `liveSummary.value?.summary`，`useLiveSummary.ts:72`）。

⇒ 会议第 5 分钟定的「下周三下午三点」，到第 20 分钟那轮**已不在「转写」里**，
而规则恰恰要求「不在转写里就别放」⇒ **每轮重新推导，不是沿用**。

**⭐ 但日历主路径不受影响（阴性）**：
待办的权威来源是**精校**，不是滚动摘要 ——
`finalizeRecording → refetchFullTranscript（整场、不截断）→ refine.todos → local_todos`。
⇒ 「时间点自动进日程」吃的是**整场**数据。

**⚠ 受影响的是一条窄路**：`MeetingDetailView.vue:353` 的「生成待办」读的是滚动摘要那份。
正常情况 finalize 已写过、这个按钮幂等 `count=0`；
**但 finalize/精校失败时，它是用户唯一的补救动作** —— 拿到的是**可能已遗忘的** `action_items`，
而界面不提示。

⇒ **不单独立项**，并入 #17 的「超限时对用户说什么」一起拍。修法候选：
① 提示词加「必须保留 `prev` 里已出现的行动项」；② 补救路径明说「来自实时摘要，可能不全」；
③ `prev` 改成带 `action_items` 的结构体。⚠ 三者都需真网关跑一场 >30 分钟的会才能确认 ⇒ **本机不可验**。
---

### 🔴 #23【本轮新发现】tag 门控的测试文件处于 CI 的覆盖空洞里 —— 要不要补一道编译卡口

**这一条与用户需求无关，是审计 #19 时顺带挖出来的基础设施缺口。单独列出是因为它可以立刻处理、成本极低。**

#
### 现场（§453）

`backend/security/cross_tenant_test.go` —— **13 条跨租户安全测试**，
文件头部自述「只依赖生产包 + stdlib，不引入任何第三方依赖」。

实测三步：

1. ⛔ **它从未编译过。** 第 282 行用了 `require.NoError`，但既没 import testify，
   `go.mod` 的 require 块里**也没有 testify**（只在 `go.sum` 作间接依赖存在）。
   → `go vet -tags security` 报 `undefined: require`。
2. ⛔ **修好编译后，它也一次都没测到过东西。** 两处独立缺陷叠加：
   ① `httptest` 给的是 `http://`，而 gorilla `Dial` 只收 `ws://`/`wss://`
   ⇒ 客户端直接报 `malformed ws or wss URL`，**请求根本不离开进程**；
   ② 给 `Dial` 的 header 传了 `nil` ⇒ **整个请求没有 Authorization 头**，
   即使 ① 修好也会在 `auth.Middleware` 被 **401** 挡掉，到不了 handler 的跨租户判断。
   → 旧代码那条 `resp == nil` 的失败读数**恰好满足**断言的预期形态，
   **测试看上去在干活，实际从未触及过服务端**（旁证：`_ = tokenA` 令牌被丢弃）。
3. ✅ **生产代码是对的。** 改用 `ws://` + 真实令牌后三行读数：
   不带头 → **401**（handler 未进入）；tenantA 读 tenantB → **403**（跨租户检查正确触发）；
   tenantA 读自己 → **101** 升级成功。⇒ **跨租户 WS 隔离在生产链上正确。**

#
### 为什么三道关卡都没抓到

| 关卡 | 为什么漏 |
|---|---|
| `npm run gates` 的 `check:gofmt` | **只判格式**。gofmt 对编译不过的代码照样报绿 |
| `backend.yml` 的 `go build ./...` 编译卡口 | **`go build` 不编译 `_test.go`** |
| `backend.yml` / `backend-pg.yml` 的 `go test` | **不传 `-tags security`** ⇒ 该文件根本不参与编译 |

⇒ **三个交点各覆盖一半，中间那个交点无人覆盖。**
⚠ 讽刺的是 `backend.yml:26-30` 的注释恰恰写着「后端曾有一批改动**从未编译过**就合入 main，
所以加 `go build ./...` 编译卡口」—— **而它没覆盖住这个文件。**

#
### 已自行落地的（不需要拍板）

按该文件自述的「stdlib only」意图修了两处，**产品代码零改动**：

- 第 282 行 `require.NoError` → stdlib `t.Fatalf`（不引 testify，避免给 go.mod 加依赖）
- WS 测试 `http://` → `ws://` + 补 Authorization 头，并就地写下**两处缺一不可**的原因

**修完该套件 13/13 全绿**（此前状态：编译不过 ⇒ 从未运行）。
顺带实测：其余 4 个 tag 变体（`greenmail` / `realprobe` / `stt_live_minimax` / `liveprobe`）
**现在全部编译干净**，`security` 是唯一坏的那个。

#
### 待拍板

⛔ **我没有给 CI 加 `-tags security`** —— 该文件头部自述
「run only when the operator explicitly opts in」⇒ **CI 不跑它是设计，不是遗漏**。
改 CI 属拍板项。

| 选项 | 内容 | 代价 |
|---|---|---|
| **A（推荐）** | 加一道**只编译**的卡口：`go vet -tags security ./...`（或 `go build -tags security ./...`）。**不跑测试、不需要 PG、秒级** | 一条 CI 命令，秒级 |
| B | 让 `make test-pg` 带上 `-tags security`，13 条测试在 CI 真跑 | 需确认它们在 CI 环境稳定（目前本机全过，但未在 GitHub runner 上验过） |
| C | 什么都不做 | 该套件继续**全暗**；下一次有人改坏它，仍然没有任何门会响 |

⚠ **建议 A 而非 B**：这 13 条里 `TestWSOriginEnforced` 等依赖 origin/时序行为，
在 GitHub runner 上是否稳定**本机无法证明**（不能自证的事不写进门禁）。
A 只保证「文件至少能编译」，这正是本条缺陷的核心形态。
B 可作为 A 落地稳定后的后续。

⛔ **另一件刻意没做**：`npm run gates` 至今**没有任何 Go 编译/测试门**
（44 门里只有 `check:gofmt`，且它只判格式）。这直接导致
**本轮 120 个已改的 Go 文件在本地没有任何验证** —— 本节是靠我手工跑
`go build` / `go vet` / `go test -race ./...`（57 包全绿）才补上的。
要不要把「Go 编译」也纳入本地 gates，同属拍板项（会改变每次 gates 的耗时）。

---

### ⭐ #14 证据更新（§456，2026-10-11）：用户那句「找更便宜的 ASR」**现状已经满足**，真正该拍的是**词级时间戳**

**① 纯价格：不用换。** 本轮核了两家官方定价文档（火山引擎、阿里云）：

| 口径 | 价（¥/小时，汇率按 ¥7.2/$1 **假设值**） |
|---|---|
| 仓内最便宜档（`qwen3-asr-0.6b` / `whisper-large-v3-turbo` / `nemotron`） | **0.086** |
| **阿里云**录音文件识别**闲时版**（按量最低档）= **国内地板价** | **0.288** |
| 阿里云 / 火山引擎 标准档 | 2.34 – 3.50 |

⇒ 仓内最便宜档是**国内地板价的 1/3.3、标准档的 1/27 ～ 1/41**。
⚠ 但这三档都是**小模型**，中文会议 CER **未实测** —— 而用户抱怨的是精度不是价。

**② ⭐ 真正的新选项：Voxtral Transcribe 2**（2026-02-04，**已核 Mistral 官方公告**）

| | Mini Transcribe V2（批量） | Realtime（流式，**Apache 2.0 开权重**） |
|---|---|---|
| **词级时间戳** | ✅ | ✅ |
| **说话人分离**（带精确起止） | ✅ | ✅ |
| 中文 | ✅（共 13 语种） | ✅ |
| 价格 | **$0.18/h** | **$0.36/h** |

⇒ ⭐ **Mini 的 $0.18/h 与清单里 `gpt-4o-mini-transcribe` 同价，却多了词级时间戳 +
原生说话人分离 + 中文** —— 在我们真正在乎的那条轴上是**严格升级**。

**③ 为什么这件事比「换个更准的模型」更值钱**：
用户抱怨「音频是切段的，转写不准」，
#18 / #20 / #21 / #22 这一整族的根子是**只有段级文本，只能靠模糊文本比对去猜重叠**。
**词级时间戳让重叠变成「可测量」而不是「可猜测」** ⇒ 整族缺陷在架构上被绕开。
⚠ 讽刺的是 `target.go:319` **早就写着** whisper-large-v3-turbo「可取 word 级时间戳」。

⚠ **必须一并拍进去的两条限制**（否则就是拿二手数字定架构）：
① 官方明写「**overlapping speech 时通常只转写一位说话人**」—— 恰是会议最常见场景；
② context biasing **只对英文优化**，其他语言实验性。

⛔ **仍然不进 `RecommendedExternalModels`**：`target.go:294-296` 的自律照旧 ——
「拿不到真实会议音频做 CER 实测，把未实测模型塞进推荐位 = 用二手数字替用户拍板」。

⇒ **#14 现在要拍的只剩一句**：要不要为「词级时间戳 + 原生说话人分离 + 中文」
付 **¥1.30/小时**（Voxtral Mini），以换取**整族去重缺陷的架构性绕开**；
还是继续按 D（`mai-transcribe-2` $0.10/h）走，只精修文本。

**④ 附一条与本仓无关但值得知道的事实**：
31.5k★ 的 Meetily 文档明写「Speaker diarization and **calendar integration**
are listed as **coming soon**」⇒ 连这个领域最大的开源项目都还没做日历集成，
而本仓**已有** `refine.todos → local_todos` 完整链路（§452 已验证主路径吃整场数据）。
⇒ 「时间点自动加入计划日程」这条**不是追赶，是领先**，不必因为竞品没做而降投入。
⚠ Meetily 星数各来源差一个量级（6k / 13k / 31.5k，最新为 2026-09-15），
**引用必须带日期与来源**。

---

### 🔴 #24【本轮新发现】逐段转写入口**在为主动丢弃的增强特性付费**，而 §120 的门没审到这里（§457）

#
### 事实（三个生产调用方，§120 只审了切块的两个）

| 调用方 | 向网关要了什么 | 响应给了谁 | 判定 |
|---|---|---|---|
| `server_stt_settings.go:825`（试转页） | verbose_json + 分离 + 词级时间戳 | `"segments"` + `"diarized"` | ✅ **真的消费了** |
| `server_assistant.go:2765`（**`/api/stt/transcribe`**，前端逐 VAD 段调） | 同上 | 只有 text/confidence/model/… | ⛔ **拿回来全丢** |
| `server_meeting.go:132`（`/api/meetings/:id/transcribe`） | 同上 | **只有 `{status, meeting_id}`** | ⛔ **拿回来全丢** |

⚠ `/api/stt/transcribe` 是**会议与随手记两条链的逐段入口**（§448），
⇒ 每一段都在付这份钱。

#
### ⛔ 代价不只是浪费（§457.3）

§120 自己的注释给了第二重理由：**分离要多带一整面
`408/500/503 diarization_unavailable` 的失败面**。
⇒ 在逐段入口上，一个**只与说话人分离有关**的上游失败会让**整段转写失败** ⇒ 那段文字就没了。
⇒ **直接打在「录音转写的不太准确」上** —— 不是丢一个字，是丢一整段。

⚠ 顺带查出：`TestSingleShotStillRequestsDiarization` 那条「反向保护」，
理由写「单发路径（`server_stt_stream.go:98`）会把 segments 吐出去」——
⛔ **但那是 `transcribe-full` 端点，它的 `segments` 是逐块结果，不是分离/词级时间戳**
⇒ **理由指错了端点**（保护的引擎调用没错，引用的证据不成立）。

#
### 已做 / 未做

- ✅ **已在两个调用方就地补如实注释**（`server_assistant.go`、`server_meeting.go`），行为未改。
- ⛔ **未修**，理由与 #14 同族：**改的是发给真实网关的请求参数，本机没有凭据**，
  「不传分离后上游是否仍 200、是否换出别的失败面」**无法自证**。
- ⛔ **未写成会红的门**（按既有纪律：已登记缺口先留注释）。

| 选项 | 内容 | 代价 |
|---|---|---|
| **A（推荐）** | 两个调用方改走 `plain` 形态，试转页保持开 | 极小；但**需真网关跑一场会议验证** |
| B | 一并给这两个端点加一段 `segments` 输出 | 中；会**再造一个 §31.3 记录过的死能力**（当时正是因此撤回） |
| C | 不动 | 每段继续为丢弃的数据付费，并保留那一面分离专属的失败风险 |

#
### ⭐ 它让 #14 拆成两条独立决策

§456 说「要词级时间戳就得换 Voxtral（¥1.30/h）」。
⚠ §457 的读数把它拆开：

1. **消掉今天就在付的冤枉钱** ⇒ 不换模型、不花钱，**现在就能做**（选项 A）；
2. **要不要为词级时间戳换 Voxtral** ⇒ 才需要换模型、才需要花钱。

⇒ **先拍 #24 的 A，再谈 #14 的②**，两者不要混在一起。

---

### ⭐ #20 收窄版（§459）：根因已钉死，**落点只有一个调用点**，但仍是产品口径

§458 坐实「实时字幕经 `appendText` 写同一张段表」后，本轮把 #20 的根因查到底：

```
实时字幕 → appendText:471 → buildUtteranceSegment
         → meeting-utterance.ts:38-41「这一行是**人手工敲的**」→ origin:'manual'
         → dedupeStep:242  if (manual) return …  ⇒ 该段永不被裁
```

⇒ **根因不是「忘了判 origin」，而是一个为「手工敲入」设计的构造器被用在了机器输出上。**
两条独立证据：
① `meeting-utterance.ts:38` 自述「人手工敲的」；
② `recordingRuntime.ts:476-477` 却写着「去重渲染：**与 ingest-speech.ts 同一口径**」——
⛔ **若作者知道这些段是 manual，就写不出这句话** ⇒ 这是一处**注释说的 ≠ 代码是**。

⚠⭐ **同时收窄我 §455 那次修复的声明**（横幅已加在 §455 结果旁）：

| 段来源 | `origin` | §455 的闸 |
|---|---|---|
| `processSegment`（ASR 段） | `asr` | ✅ 生效 |
| `appendText`（实时字幕段） | `manual` | ⛔ **不生效**（`:242` 提前返回） |

⇒ **§455 修的是「裁得太狠（丢字）」，#20 是「根本不裁」—— 两回事，修法必须分开。**

| 选项 | 内容 | 代价 |
|---|---|---|
| **A（推荐）** | `appendText` 造的段改成 `origin:'asr'`（**落点只有 `recordingRuntime.ts:471` 一处**） | 小；但 `origin` 是**必填 + 落库**，要确认历史行迁移 |
| B | 新增第三种 origin（如 `'caption'`），只有字幕段参与去重 | 中；但要去重、渲染、存储三处一起加分支 |
| C | 维持现状 | 重复**永久落库 + 默认展示 + 流进 LLM**（#20 原结论） |

⚠ **A/B 都需要真机**：重复长什么样取决于 #21 的双写是否真发生（§458 的
`typeof window.webkitSpeechRecognition` 那一行先跑）。**不可在本机验。**

##### 🆕 §552 追加（2026-10-13）：推荐项甲**不是零代价**——它会打开一个已记录的丢字行为

| §552 新增 | 证据 |
|---|---|
| ⭐⭐⭐ **甲的真实成本被低估了** | `meeting-dedup.ts:85-88` 记着一个**已知且被接受**的行为：「相邻且只差一个字 ⇒ 判成切片重叠」，同音错字时会把后一段裁成残字。⇒ **今天字幕段没吃到这个丢字行为，恰恰是因为它被误标成 `manual`；改成 `'asr'`（甲）就是主动把它放进这个已知会丢字的口子里** |
| ⭐⭐ **而且跨引擎比同引擎更容易触发** | 字幕段与 VAD 段是**同一段音频的两个引擎**的产物 —— 正是 §438 描述的形态。⚠ `#21` 说「覆盖率 <0.6 ⇒ 消解不了」讲的是**通常**；§438 讲的是**一旦相近就会被裁**。⇒ **大多数时候不触发，触发时丢的是用户永远发现不了的内容**（§442：一旦触发，损坏是永久的） |
| ⭐⭐ **甲还有「四分之一修正」问题** | `origin` 只是「哪个构造器造的」的**代理**，不是「这条段的来源」。`confidence:0.7` / `speakerLabel:'说话人'` / `lang:'zh'` 与它共享同一个错误前提 ⇒ **只改 `origin` 会得到「享受去重豁免却仍冒充人工输入」的半修正状态** |

⇒ ⚠ **三案都有代价，本条不代拍，只把甲的代价补齐**：
　 甲（原写「代价：小」）**实际上会把一个「文本几乎必然不同、大概率裁不到」的段，
　 换成一个「与相邻段高度同源、裁得到、且裁掉的是真实内容」的段**；
　 乙要去重/渲染/存储三处一起加分支；丙让重复永久落库。
⇒ ⛔ 决定甲的代价之前，需要 **#42 的重合比例**（同音频、同引擎 vs 跨引擎的触发率差）——
　 而那正是 **A2 + 真机录音**才能给的读数。

##### 🆕 §553 追加（2026-10-13）⭐⭐⭐⭐⭐ 甲的代价**已被量化**，而且**非单调**——上面那段代价表要作废

⚠ **先收回上面的一处机制判断**：§552 说「甲会走 §438 那个 **exact 路径**」——
　 复核 `likelyAudioOverlap`（`meeting-dedup.ts:214`）是**严格小于**
　 `nextStartMs < prevEndMs`，而字幕段的 `startMs` **恰好等于**上一段的 `endMs`
　 ⇒ 判定 false ⇒ **走 soft 路径**，不是 exact。
　 ⭐ **但代价反而更重**。实测（只换一个量 `manual`，时间戳一律用生产形状）：

| 跨引擎相似度（同一段音频的两个引擎） | 今天（`manual`） | 甲之后净增 | **甲裁掉** |
|---|---|---|---|
| ⭐ **完全相同** | 15 字 | 15 字 | **0 字** |
| ⭐ 只差一个标点（逗号/句号） | 17 字 | 3 字 | **14 字** |
| 加一个语气词（~95%） | 16 字 | 2 字 | **14 字** |
| 一个同音字（~94%） | 15 字 | 1 字 | **14 字** |
| 措辞不同（~70%） | 16 字 | 16 字 | 0 字 |
| 措辞完全不同（~25%） | 14 字 | 14 字 | 0 字 |

| §552 的说法 | §553 实测 |
|---|---|
| 甲「代价：小」 | ⛔ **代价接近上界：裁掉 93% 内容** |
| 「两引擎越像越危险」 | ⛔ **反了**：完全相同 ⇒ 裁 0 字；**差一个字 ⇒ 裁 93%** |
| 走 §438 的 exact 路径 | ⛔ 走 soft 路径，但 soft 在「几乎相同」时**一样狠** |

⇒ ⭐⭐⭐⭐⭐ **「几乎一样但不完全一样」才是危险档**，
　 而那正是同一段音频经两个引擎转写时**最常见**的样子
　 （同一批词、同一个语序，差一个标点或一个同音字）。
　 **字节级相同反而安全 —— 而真实设备上它从不发生。**

⇒ ⭐⭐⭐ **连带推翻 `background-mic.ts:117` 的一处推理**：
　 「两个引擎产出本就不同 ⇒ 覆盖率 <0.6 ⇒ 去重消解不了」——
　 前半句在 94~98% 档**仍成立**（差一个逗号也算「不同」），
　 **后半句在危险档上是错的** ⇒ **「产出不同」推不出「消解不了」**。

⇒ ⚠ **三案代价重列**：甲 = **裁掉 93% 内容**（上界）· 乙 = 去重/渲染/存储三处加分支 ·
　 丙 = 重复 **100%** 永久进最终正文（§553.1 实测 19/19 形态）。
　 ⇒ **本条不代拍**，但 **甲已经不是「代价小」的那个选项了**。

### 🔴 #25【本轮新发现】会议链的 250ms 重叠：**去掉它比改去重口径划算**，但直接把 `sliceMs` 调成 0 会丢音频（§523）

**背景**：§522 曾把这件事记成「甲（去重叠）vs 乙（改去重口径）二选一」，
本节**收回它的理由**（两条链被当成一条测了），结论侥幸成立但论证要整个换掉。

| | 随手记链 | 会议链 |
|---|---|---|
| 切片窗口 | `takeNewWindow()` **不重叠** | `[speechStartMs-sliceMs, endMs]` **带 250ms** |
| 去重实现 | Go `mergeIncremental` | 前端 `meeting-dedup.ts` |

⇒ §522 把「会议链形状」喂给了「随手记链的函数」。**实测两边在各自真实形状上都是裁 0 字**
　 （随手记 23/23 边界落兜底；会议链 25/25 落兜底，`likelyAudioOverlap` 本身判「重叠=true」是对的）。

**读数**（CER 口径同 §520/§521，338 真值字；音频代价 = 送进 ASR 秒数 ÷ 真实秒数）：

| 形状 | 音频代价 | 生产去重裁掉 | CER | 探索性口径·时间轴比例裁剪 |
|---|---|---|---|---|
| 整段一次 | 1.00× | — | **3.55%** | — |
| 3s 无重叠（随手记真实形状） | 1.00× | **0 字** | 6.21% | 6.21% |
| 3s+250ms（**会议链真实形状**） | 1.08× | **0 字** | 11.54% | 7.10% |
| 3s+1.6s | 2.05× | 148 字 | 67.46% | 18.34% |
| 3s+2.0s | 2.84× | 488 字 | 53.55% | 21.60% |

**要拍的是这个**（不是「甲还是乙」）：

| 选项 | 内容 | 代价 / 风险 |
|---|---|---|
| **A（推荐）** | 窗口起点改成 **`min(上一段 endMs, speechStartMs)`**（**按需回溯**）：只在起点回退时回溯，正常情况**零重叠** | 要改 `vad-segmenter.ts:121-123`；**收益只在真机可见**（合成语料里 `speechStartMs` 从不回退 ⇒ 本机量不到差异） |
| B | 把 `sliceMs` 直接调成 0 | ⛔ **会丢内容**：`speechStartMs` 来自 RAF 能量判定，**后台标签页会被节流**、判定回退，回溯窗口正是防这个的 ⇒ 每次回退最多丢段首 250ms |
| C | 只改去重口径（乙），重叠不动 | ⛔ **实测追不上**：即使去重做到完美（7.10%）仍差于完全不重叠（6.21%） |
| D | 加大重叠到 1.6s+ | ⛔ **实测更差**（18.34%）+ 音频代价 **2.05×**；且**只有**在重叠加大后闸门才开始工作（§523.4） |

⚠ 全部读数在 **`say` 合成语音**上取得。真实会议有 1.5s 静音、VAD 应在静音处切
　 ⇒ **C 的重复率大概率被高估**。但「去重裁 0 字」这个读数**不受语料影响**
　 （它由算法与形状的错配决定）。⇒ **要拍的是设计口径，不是等更多读数**。

### 🔴 #34【本轮新发现】时间点入日程的失败模式不是「抽不出来」，是「**抽错了且不报错**」（§524）

链路：`转写 → LLM 抽 due → parseDueAt → at 型计划任务`（`meeting-due-plan.ts:68-89`）。
本节用 §523 的**真实 ASR 输出**（A/B/C 三形状）量了 `parseDueAt` 这一层。

**✅ 基线是好的**：今天下午三点→15:00、下周一→10-19 09:00、明天下午两点半→14:30、
明天的评审→09:00；百分之十二/三号会议室/三十秒/推迟两天**全部 null** ✅。
⭐ **中文数字在钟点上完全支持** ⇒ 「中文数字→阿拉伯」占 52% 那个问题**在钟点这一路不丢信息**。

**⛔ 两类静默错时刻**（`now` = 2026-10-13 10:00 周二）：

| 类 | 输入 | 结果 | 真相 |
|---|---|---|---|
| 切片吃掉「半」 | 「明天下午2点」 | 14:00 | 14:30（随手记链真实形状里真的发生） |
| 骑缝字 | 「明天下午2点点半」 | 14:00 | 14:30（§521 实测形态） |
| **骑缝 + 丢「下午」** | 「明天下午点两点半」 | **02:30** | 14:30 —— **差 12 小时，零报错** |
| **裸「N号」** | 「地点在3号会议室」 | **11-03 09:00** | 不是日期 |
| 同上 | 「5号楼」/「2号线」 | **11-05 / 11-02** | 不是日期 |
| ⛔ 反向漏报 | 「三号会议室」/「二十号前提交」 | null | （中文数字形态反而安全） |

⭐⭐ **「裸 N号」这个假日程是 ASR 的阿拉伯化喂出来的**：`meeting-due.ts:450` 的
　 `dOnly = /(\d{1,2})\s*[日号]/` **只认阿拉伯数字**、且不要求「号」后是边界
　 ⇒ 中文数字的「三号会议室」安全，识别正确的「3号会议室」反而生成假日程。
　 **ASR 越准，这个假日程越容易被触发。**

| 选项 | 做法 | 代价 |
|---|---|---|
| **A（推荐）** | 裸「N号」要求同句有月/周/今明等日期锚，否则 `null` | 「20号交」这类真日期说法开始漏报（今天它是准的） |
| B | 「号」后跟非边界（楼/室/线/车/院/栋/路）时 `null` | 词表会漏（「3号车厢」「18号楼A座」） |
| C | 维持现状 | 假日程 + 中文数字漏报并存 |

⚠ **曝光量未测**：`due` 由 LLM 抽（`action_items[].due` / `next_meeting.due`），
　 本机无 LLM 凭据、也没有真实 LLM 产出的 `due` 语料 ⇒ 本节读数是**上界**。
　 但「切片吃掉半 / 12 小时偏移」那两条**不经 LLM 也会发生**（LLM 只可能修好或原样透传）。
⚠ 12 小时那条的修法在**切片侧**（#25 的按需回溯），不在解析器侧。



##### 🆕 §566【2026-10-13】⭐⭐⭐ `#34` 的一问改了性质：**那道守卫已经写好了，只是够不着**

`meeting-due.ts` 文件头承诺「裸钟点若明确指向过去就返回 null、**宁可不要也不建假日程**」，
实现就是 `parseDueAt` 末段的 `:546`（自带注释「明确指向昨天 ⇒ 放弃，不建假日程」）。

⇒ ⭐⭐⭐ **它是结构上到不了的分支**：
`at` 由 `localTime(y, mo, d, …)` 构造，而 `y/mo/d` 取自 **`new Date(now)`** ⇒ **`at` 恒为「今天」**，
`now` 也在今天 ⇒ `at − now ∈ (−24h, +24h)` 开区间 ⇒ `at < now − DAY_MS` **恒假**。

| 量 | 读数 |
|---|---|
| 穷举规模 | 366 天 × 每天 205 个基准时刻 × 21 个裸钟点 = **1,583,316 次**（真 `parseDueAt`） |
| `:546` 的 `return null` 分支体 V8 覆盖率 | **count = 0** |
| 同段内必经的 `:547`（`at += DAY_MS`） | **count = 1,583,316**（⇒ 不是没跑到，是没进分支） |
| 返回 null 次数 | **0 / 1,583,316** |

⛔ **顺带更正 §565**：这条行为**已被命名并钉住**（`meeting-due.test.ts` 的
「只有钟点：未来则今天，**已过则明天**」）⇒ **#34 是「改契约」，不是「修 bug」**。
但那条用例只钉了**两个现场时刻**，钉不住「任何时刻都不返回 null」这个结构事实。

⇒ ⚠ **所以 #34 的第二问现在要重述**：
　 ❌ 原表述：「要不要给裸钟点加一道 null 守卫」
　 ✅ 实情：**守卫已经存在（`:546`），只是恒假** ⇒ 要问的是「**要不要把它变成可达的**」。

✅ **已落地**：新判据「裸钟点在一个整天里任何时刻都不会返回 null」（2,227 次扫描）
　（变异 M20 把条件改可达 ⇒ 精确变红并报 `1112/2227 次 null` · 生产文件 sha256 逐位复原）
　+ `:546` 上方与文件头两处如实注释。
⛔ **刻意不删这行死代码** —— #34 若拍板改成归 null，**正确改法正是把这一行变可达**，
　 先删等于把要改的那一行提前删没了。

##### 🆕 §565【2026-10-13】⭐⭐⭐ 用**真 `parseDueAt`** 把 #34 的读数量出来了 —— 它其实**有两个轴**，且**两个轴的危险性相反**

基准：周二 10/13 **15:04**（未复刻、未改生产类）。

| 输入 | 解析成 | 漂移 | 轴 |
|---|---|---|---|
| ⭐ **`3 号`** | **11/03 09:00** | **+20.7 天** | 轴一·缺月份锚 |
| `20 号` / `15 号` | 10/20 · 10/15 | +6.7 · +1.7 天 | 轴一 |
| ⭐ **`下午三点`** | **10/14 15:00** | **+1 天（差 4 分钟就翻页）** | ⭐ 轴二·裸钟点 |
| `3点` | 10/14 03:00 | +0.5 天 | 轴二 |
| `晚上8点` / `上午十点` | 10/13 20:00 · 10/14 10:00 | 今天 / 明天 | 轴二 |
| `那之前` / `尽快` | **null** | — | §65 实测 · 无时间 |

⇒ ⭐⭐⭐ **轴二比轴一危险，原因是它静默**：
　 轴一解出来的是**一个离谱的日子**（+20.7 天）⇒ 用户扫一眼日程就发现 ⇒ **自暴露**；
　 轴二解出来的是**一个完全合理的日子**（明天 15:00）⇒ **用户看不出任何异常**。
⇒ ⭐⭐⭐ 而 §565.1 那套 `unresolved` 机制**只管「完全解不出」，管不了「解错了」** ——
　 `下午三点` 不是 null，它是**一个具体的 `at`** ⇒ 计数恒为 0 ⇒ **一句话都不会说**。

⇒ ⇒ 与 §563 **完全同构**：那里是「三态挤进两值 flag」，这里是「两态挤进同一个返回值」，
　而**「解出了但解错了」被并进了「解出了」那一格**。

⇒ ⚠ **拍板时要拆成两问**（原来只有一问）：
　 ① 裸「N 号」缺月份 ⇒ null 还是补月份？（推荐 null）
　 ② 裸钟点无日期锚 ⇒ null？（**推荐仍是 null**）
　 ③ ⭐ 归 null 之后：「今天已过的钟点」该归今天 / 明天 / null —— ⛔ 这仍是产品口径，本节不代拍。

⇒ ✅ **本节同时核出一条阴性结论**：这条链的**诚实性**没有问题 ——
　 `unresolved` 计数（`meeting-todo-persist.ts:71`）+ 三层判据（行为/接线/负控）都在，
　 且本节用 **变异 M19**（把文案实参写成字面量 `0`）配了**阳性对照** ⇒ 门禁当场变红。
　 ⛔ 我一度推断「会议侧只验了函数名、没验实参」，**被门禁直接打脸** ——
　 第一轮的 grep **被 100 条上限截断**，我把「我没看到的」当成了「不存在的」。


### 🔴 #35【本轮新发现】随手记链的 `NOTE_CHUNK_MS` 是一个**单常量**就能换的准确率，代价是首字延迟（§525）

随手记链的切点只有一个旋钮：`recordingRuntime.ts:704 NOTE_CHUNK_MS = 3000`
（`mediaRecorder.start(NOTE_CHUNK_MS)` 决定片长）。实测**只改窗口、不重叠硬切**：

| 窗口 | 首字延迟 | ASR 调用 | 错误/真值 | CER | 相对整段 |
|---|---|---|---|---|---|
| 整段一次 | — | 4 | 12/338 | 3.55% | 1.00× |
| **3s（现状）** | 3s | 27 | 21/338 | **6.21%** | **1.75×** |
| 6s | 6s | 14 | 18/338 | 5.33% | 1.50× |
| **10s（推荐）** | 10s | 9 | 13/338 | **3.85%** | 1.08× |
| 15s | 15s | 8 | 12/338 | 3.55% | 1.00× |

⭐ **总音频秒数四档恒为 75.8s**（不重叠 ⇒ 按秒计费不变），变的是**调用数 27→8**（省 2/3 请求）
　⇒ 调大窗口**同时**更准、更省请求。
⚠ 但产品诉求原文是「每 3-15 秒冒出一段新文字」⇒ **15s 是上界，不是随手写的**。
⛔ **窗口必须 < 30s**：`glm-asr-2512` 单次上限 30s（`target.go:421`），OpenRouter ~60s，MiniMax 500s。
⚠ **随手记链没有 VAD**：整段路走 `full.go:426 SplitWAV` 静音感知切段，
　随手记链调大窗口**只是不再切碎，拿不到 VAD 的好处** ⇒ 本表是「长窗口+硬切」。
⚠ 读数在 `say` 合成语音上：真人停顿更多 ⇒ **长窗口的收益很可能被高估**（停顿处切更容易撞词中间）。

### 🔴 #36【本轮新发现】#30 ASR 热词表的落点：**`prompt` 字段本仓已经在发**，问题是上游认不认（§526）

⛔ 「加一个热词表」会让人以为要新字段 —— **`prompt` 早就在每次中文转写里发了**
　（`transcribe.go:526` / `provider_openai.go:84`，常量 `target.go:140`）。
⚠ 被三处测试钉成常量：其中 `transcribe_test.go:227`（中文带/非中文不带，**负控对照**）
　与 `transcribe_test.go:266`（常量必须含「简体」）**要保留**，改的是「字面量相等」那一层。
⛔ **上游认不认未测**：常量的实测依据是 2026-10-01 **本机 faster-whisper**，
　不是生产用的 mimo；且 `discovery.go:440` 记着「text prompt is injected by the gateway」的 400 形态
　⇒ 网关可能自己注入 prompt 覆盖我们的。
⇒ **第一个实验不是「加热词表看 CER 降不降」，而是「上游认不认 prompt」。**
⭐ 另一条更直接的路：`glm-asr-2512` 是能力表里**唯一原生支持热词表**的模型
　（`target.go:415-421`，¥0.06/min、限 30s/次）；但 `provider_zhipu.go:13` 自述「**未经实机验证**」。
⛔ 两条路本机都量不了：本地 FunASR `llama-funasr-sensevoice --help` **不接受热词/prompt 参数**，
　网关三个端点都要 key。

### ⛔ #35 收回并改写：随手记链的正解不是把窗口调大，是**上静音感知切分**（§527）

§525 曾推荐「`NOTE_CHUNK_MS` 调 10s，拿回 98% 的差距」。
造一份**带 1.5s 停顿**的语料（20 句 / 106.97s / 366 字）后直接否掉：

| 臂 | 段数 | 平均段长 | 边界对齐静音 | CER |
|---|---|---|---|---|
| A 单发整段 | 1 | 107s | — | 3.85% |
| B **定长 3s（随手记链现状）** | 36 | 3.0s | ❌ | **7.99%** |
| C 定长 10s（§525 推荐） | 11 | 9.7s | ❌ | **5.62%** |
| **D 生产 `SplitWAV(25)` 静音感知** | 20 | 5.35s | ✅ | **3.25%** |
| **E 定长 5.35s（同段数同段长）** | 20 | 5.35s | ❌ | **5.92%** |

⭐ **E 臂是混淆分离的关键**：D 与 E 段数相同、平均段长相同，**只差「边界是否落在静音上」**
　⇒ **3.25% vs 5.92%（差 2.67 个百分点）**；
　而 E→C（段长 5.35s→10s）**只值 0.3 个百分点**
　⇒ ⭐ **收益来自「切在对的地方」，不是「切得少/切得长」。**

⭐⭐ **两条链最终质量差 2.5 倍**：会议链收尾重转走 `transcribeFull` → Go `SplitWAV`
　（`meeting-final-transcript.ts:30` 明写「按静音边界自动切段」）= **3.25%**；
　随手记链走 3s 硬切、无 VAD、整段校对几乎恒为假（§463）= **7.99%**。

| 选项 | 内容 | 代价 |
|---|---|---|
| **A（推荐）** | 随手记链接静音感知切分（前端 `VadSegmenter` 或服务端分段） | **是改造不是调参**；⚠ 前端 `VadSegmenter` 是「能量+RAF」，与 Go `SplitWAV`（PCM 静音阈值）**不是同一个东西**，其切分质量未测 |
| B | 只把 `NOTE_CHUNK_MS` 调到 10s | ⛔ 只到 5.62%，仍差 VAD 2.4 个百分点 |
| C | 维持 3s | 7.99% |

⚠ 边界：停顿是合成的（19 处 × 1.5s，真实会议多数停顿**短于**1.5s ⇒ D 的优势可能被高估）；
　绝对值用本地 FunASR，生产走 `mimo-v2.5-asr`；单句 5.35s 比真实会议段长理想。

### ⭐ #25 证据更新（§528）：两条读数在**另一种语料结构**上各测一遍，结论一致且更强

在 §527 造的**带 1.5s 停顿**语料（20 句 / 106.97s）上重测会议链真实形状：

| 形状 | 片数 | Go `mergeIncremental` | TS `dedupeStep` | naive CER |
|---|---|---|---|---|
| **C 3s+250ms（会议链现状）** | 39 | **裁 0 字**（38/38 落兜底） | **裁 0 字**（丢段 0） | **15.09%** |
| **D 3s 无重叠（随手记链现状）** | 36 | **裁 0 字**（35/35 落兜底） | **裁 0 字** | **7.99%** |

| | 无停顿语料（§523） | **带停顿语料（§528）** |
|---|---|---|
| C / D 倍数 | 1.86× | **1.89×** |
| 生产去重裁掉 | 0 字 | **0 字** |
| `dedupeSegments` | 0 | **0**（39→39 段、36→36 段） |

⇒ ⭐ **「去固定 250ms 重叠是净收益」在两种语料结构上都成立**，
　且**「改去重口径（乙）」在两种语料上都一个字符都回收不了**
　⇒ 它是**算法与形状的结构性错配**（算法要「对齐的后缀=前缀」，重复内容骑在边界上），
　**不是语料造出来的假象**。
⚠ 带停顿时代价**更大**（1.86×→1.89×）：停顿让每片有效语音更少，而重叠是固定 250ms
　⇒ 相对占比更大（C−D 多余字数 22 字 → **33 字**）。⚠ 这只是同向证据，
　「停顿长度 × 重叠代价」的单独扫描未做。
✅ 跨节量具交叉验证：D 臂 7.99% 与 §527 的 B 臂（同形状同音频）**逐位吻合**。

### 🔴 #37【本轮新发现】同类项目对照：MCP 与「跨会议聚合」是**共识级缺口**，而说话人分离本仓**更强**（§529）

检索 2026-10-13 高星同类项目（Meetily 31.5k / Vexa 2.9k / Steno 1.3k / minutes ~1.45k …）后逐项对照：

| 能力 | 本仓现状 | 判定 |
|---|---|---|
| 说话人分离 | `speaker-diarization.ts` 声纹聚类 + `voiceprints-store` **跨会议继承真名** | ⭐ **本仓更细**（同类少见） |
| 实时 + 会后整段两阶段 | 会议链有 `finalizeRecording`；随手记链无（§463） | ✅ 形态与共识一致 |
| **MCP 接入** | `frontend/src` 检索 **0 处** | ⛔ **缺口**（Vexa/minutes/Parrot 都做了） |
| **跨会议行动项聚合** | 未见出口（minutes 有 `minutes actions`） | ⛔ **缺口** |
| **矛盾决策/过期承诺标记** | 未见出口（minutes 有 `minutes consistency`） | ⛔ **缺口** |

⇒ ⭐ **前 3 项都不是「转写准不准」的问题** —— §521–§528 已把转写那一层做完。

| 排序 | 缺口 | 代价 |
|---|---|---|
| 1 | **随手记链接静音感知切分**（§527） | 改造；唯一有实测读数支撑（7.99% vs 3.25%） |
| 2 | **MCP 接入** | 中等；需先定「暴露哪些数据」 |
| 3 | **跨会议聚合 + 矛盾决策标记** | 中等；`due` 链已通到最后一公里（§524），接得上 |

⚠ 来源边界：Star 为 GitHub topic 页快照（二手来源对 Meetily 出现 12.8k/16k/28k/31.5k 四个数）；
　**未逐个打开仓库 README 核对**（能力列是 topic 关键词 + 二手报道），
　与本仓现状那一列才是代码级核对（带行号）—— **两列证据强度不同**。
⛔ 「讯飞听见这类产品」本轮**未单独检索**（被开源项目挤掉），登记为未做。

### 🔴 #38【本轮新发现】声纹能力**只有会议链有入口** —— `enrollFromAudio` 零 UI 入口，两张表只挂在会议详情页（§530）

§529 说「本仓说话人分离比同类更细」——**摊开证据后，那句话只对会议链成立**：

| 环节 | 证据 | 判定 |
|---|---|---|
| 声纹自动识别跨会议 | `recordingRuntime.ts:322-330` 每次 `start()` 都 `loadSpeakerProfiles()` → `diarizer.loadProfiles(...)` | ✅ 机制**已实现** |
| 声纹怎么产生 | `VoiceprintSheet.vue:53` 只导入 `list/delete/rename/clear` | ⛔ **`enrollFromAudio`（预先注册声纹）零 UI 入口** |
| UI 挂在哪 | `VoiceprintSheet` + `SpeakerLabelSheet` **只**在 `MeetingDetailView.vue:104/:108` | ⛔ **随手记链路上零入口** |

⇒ ⭐ 随手记的聚类照样在跑，但**用户永远改不了名、也注册不了声纹**
　⇒ 与 `meeting-roster-homophone.ts:85-87` 已登记的「随手记 `participants` 恒为空」
　**同根因**：会议详情页是唯一能写「谁是谁」的地方。
⚠ 这是「已有 X 是描述还是产物」的**第三种形态**：
　**API 与机制都在，但没有 UI 入口**（`enrollFromAudio` 有实现、有测试、有注释，只有入口不存在）。

**商业产品有、本仓确实没有的**（登记，未排序）：
**口语去除/同义词合并/语篇规整**（直接关系到 §520 那 52% 数字形态噪声）·
**热词的「用户可配」入口**（§526 已证 `prompt` 是硬编码常量）·
**数字格式化**（既是噪声源，也是 #34 假日程的成因）·
行动项按说话人**自动填负责人**（飞书妙记有）· 繁简转换开关（⚠ 口径）。

⚠ 全部产品信息来自**二手来源**，没有试用、没有开官方文档；
　功能表同一格在不同来源间有差异 ⇒ 取交集不取并集。
⚠ 横评「讯飞转写最强但理解稍逊」与本仓 §521–§528 读数**同形**，
　但那比的是产品之间、本仓比的是同一模型的四种切法 ⇒ **不能互相替代**。

### ⛔ #39【本轮新发现】§520「52% 的错误是数字形态」要收窄：它主要是**写法口径**，且**部分不可逆**（§531）

**实测**：把 ASR 输出里剩下的中文数字**统一转成阿拉伯**，CER 不是下降而是
**3.55% → 5.92%（+2.37pp）**。原因：

| 文本 | 中文数字处数 |
|---|---|
| 真值（书面中文数字） | **16**（一7·三3·两2·二1·十2·百1） |
| ASR 转写（逐句 20 次） | **9** |
| ASR 转写（整段 4 段） | **9** |

⇒ ⭐ **ASR 已把 16 处里的 7 处转成阿拉伯，剩下 9 处还是中文 ⇒ 产物是「混写」的**
　⇒ 再统一成阿拉伯，产物就与书面真值**整体错开** ⇒ CER 必然变差。
　 ⇒ **那 +2.37pp 不是识别变差，是「写法与参考不一致」。**
⛔ 顺带否掉一个我一度怀疑的假设：「数字写法随输入形态（短句/长段）而变」——
　**实测不成立**（逐句 9 处 vs 整段 9 处）。

⇒ ⭐ **产品含义**：
· 统一成**阿拉伯**：与 ASR 自然输出、§520 一致、**可逆**；
· 统一成**中文数字**：⛔ **不可行** —— `百分之十二`→`12%` 单位被吞，
　从「12」**无法还原**是「百分之十二」还是「十二点」（§520 实测）⇒ 信息论上不可逆；
· 维持混写（现状）：⛔ 同一份稿子两种写法并存，§524 已实测有实际后果（裸「3号」→ 假日程）。
⇒ **建议（不代拍）**：统一成阿拉伯，并把「数字形态」从 CER 里**单独拆出来评**，
　别混进识别质量指标。这也回答了 #34 那个口径问题的一半：
　**形态不是「识别对不对」，而是「产物长什么样」。**

⚠ 另一格空白：**「口语去除」的收益是 0**（逐字相同 3.55%）——
　本语料里根本没出现「嗯呃啊哦呀呗嘛呢」⇒ 语料缺结构（与 §527 停顿问题同族），
　**不能推广也不能否定**。
⚠ 没有实现任何归一化（一次性量具，未进仓库）；没有逐条分类那 7 处「被转阿拉伯」的损失。

### ⭐ #39 读数更新（§532）：数字形态损失实测**可逆 3 处 / 不可逆 1 处（25%）** —— 比 §531 写的乐观

逐条审计 338 字语料上的 4 处数字形态差异：

| 类别 | 真值 → 产物 |
|---|---|
| **甲 可逆 ×3** | 三点 → **3点** · 三号 → **3号** · 三十秒 → **30秒**（**单位都还在，只是字形变了**） |
| ⛔ **乙 不可逆 ×1** | 百分之十二 → **12**（**百分之/分之 消失**） |

⚠ **量具自己错了一版**：第一版拿「**真值**上下文里有没有单位」判「单位被吞」
　⇒ 把「三点→3点」误判成丢「点」⇒ 4 处全判不可逆。
⇒ ⭐ **正确判据要问「产物里单位还在不在」**。两版读数都留了日志，改判据的理由在 §532.4。

⇒ ⭐ **对 #39（统一成哪种写法）的影响**：
· 统一成**阿拉伯**：**全部可逆**（只需处理 ASR 漏转的中文数字），规则更简单，
　且与 #34「裸 N号 → 假日程」天然同侧；
· 统一成**中文数字**：⚠ **75% 可行**（§531.4 写的「不可行」偏重了），
　⛔ 但**百分比类必须单独立规**；
· 两边都要一条**排除「一下/一起/十分」非数量词**的守卫。

⚠ 样本只有 4 处；真人录音里百分比类出现更频繁 ⇒ **不可逆占比可能被低估**；
　也没测多位数+修饰语（「百分之八十五」）。

### ⭐ #25 读数更新（§533）：**停顿越长，固定 250ms 重叠越有害**（把 §528 的「同向证据」变成读数）

同 20 句 / 同一模型 / 同一音色，只改句间停顿长度：

| 停顿 | D 3s 无重叠 | C 3s+250ms | C−D 多余字数 |
|---|---|---|---|
| 0.0s | 7.69% | **12.72%** | 26 |
| 0.5s | 9.17% | **12.72%** | 28 |
| 1.0s | 7.40% | **15.09%** | 27 |
| 1.5s | 7.99% | **15.09%** | 29 |
| 2.5s | 7.69% | **17.46%** | 36 |

⭐⭐ **D 臂（不重叠）几乎不随停顿变化（7.40–9.17%）⇒ 「停顿让识别变难」不成立**
　⇒ **D 就是对照组**（同 §527 的 E 臂逻辑）；正因为它平坦，
　**C 臂单调上升（12.72→17.46%，+4.74pp）才可解释**。
⭐ **重复量单调上升 26→36 字（占真值 7.69%→10.65%）**。
⚠ 倍数列不单调（1.65/1.39/2.04/1.89/2.27）⇒ **倍数不是读数，单臂趋势才是**。

⛔ **修正 §523**：它用的**无停顿**语料给出的是**曲线下沿**
　⇒ 真实会议应取 **C ≈ 12.7%–15.1% / D ≈ 7.4%–9.2%**，倍数 **1.4×–2.3×**；
　§528 那条 1.89× 是**曲线中段**，属正常而非异常。
⚠ 真实会议停顿通常**短于**1.5s ⇒ 不应取最坏的 17.46%。

机制：停顿越多 ⇒ 3s 窗口里语音占比越低，而重叠是**固定 250ms**
⇒ 在语音上的相对占比越来越大 ⇒ 重复更多。
⇒ **甲（按需回溯）的收益随停顿变长而增大**，与本曲线同向。

⚠ 只有 5 档 / 1 音色 / 同一批 20 句；真实停顿分布未知；
　没测「重叠幅度 × 停顿长度」二维扫描（只扫了停顿这一维）。

### ⛔ #25 判决收口：「把 250ms 调小」**出局** —— 没有中间档（§534）

第三个选项此前没人提：**把 `sliceMs` 从 250ms 调小**（一个常量的改动，比「按需回溯」小得多）。
若存在「小到几乎不产生重复、却仍跨得过一个词」的档位，它会是最优解。**实测不存在。**

3s 窗口；停顿 × 重叠两个正交因子（真值 338 字）：

| 停顿 | 0ms | 125ms | 250ms | 500ms |
|---|---|---|---|---|
| 0.5s · 微平均 CER | 9.17% | 7.99% | 12.72% | **26.92%** |
| 1.5s · 微平均 CER | 7.99% | 11.24% | 15.09% | **27.22%** |
| 0.5s · 输出−真值 | **1 字** | 16 | 29 | 71 |
| 1.5s · 输出−真值 | **3 字** | 19 | 32 | 75 |

⇒ ⭐⭐ **重复量随幅度单调上升，没有免费档位**：125ms 已有 16–19 字（≈真值 5%），
　**只有 0ms 干净**（1–3 字）⇒ 代价 ≈0.14 字/ms，**近似线性**。
⇒ ⚠ **125ms 的 CER 在两个停顿档给出相反符号**（0.5s 优于 0ms、1.5s 劣于 0ms）
　⇒ **它落在噪声内**，不能当「小重叠有用」的证据；结论不依赖它。
⇒ ⭐⭐ **500ms 是灾难**（CER 27%、多出 71–75 字）⇒ 这就是用户报的「片段重复」在
　去重失效时的完整形态，也解释了 §524 的 E/F（1.6s/2.0s 重叠）CER 高达 67%/54%。
⇒ ⭐ 「调小」只是把代价**按比例缩小**，0ms 正是甲要达到的效果 ⇒ **丙收敛到甲，不构成独立选项**。

⚠ 口径：表里那列是「输出长度−真值长度」，是**代理量**；真正支撑「单调」结论的是
　**CER 那一列**（它对少识别/多识别都敏感）。⚠ 真机上的重叠幅度是 **RAF 回退的产物**，
　不是配置项——只有「固定 250ms」能配，本节把幅度当参数扫。

### ⛔ #25 判决收口（二）：「改去重口径」**第二次独立出局** —— 上界也追不上（§535）

§523 已在**无停顿**语料上出过一次裁决（oracle 最好 7.10% vs 不重叠 6.21%，追不上），
但 §527/§528 那批**带停顿**语料的切片文本**从没在上面跑过 oracle**
⇒ 「乙」缺一个更真实语料上的**上界读数**。本节零 ASR 成本（复用 §528 已落盘切片文本）。

| 形状 · 去重口径 | 输出字数 | 编辑距离 | **CER** |
|---|---|---|---|
| **C 3s+250ms · 朴素拼接**（生产现状） | 370 | 51 | **15.09%** |
| C · 时间轴比例裁剪（探索性上界） | 334 | 44 | 13.02% |
| C · difflib 全局对齐（另一个探索口径） | 362 | 43 | **12.72%** |
| **D 3s 无重叠 · 朴素拼接** | 341 | 27 | **7.99%** |
| D · 时间轴比例裁剪 / difflib 全局对齐 | 341 | 27 | 7.99% |

⇒ ⭐⭐ **第二次独立出局**：把去重做到**上界**（两个探索口径里较好的那个 12.72%）
　**仍远差于完全不重叠的 7.99%**；与 §523 无停顿语料**方向一致且差距更大**
　（那边 +0.9pp，这边 **+4.7pp**）。
⇒ ⭐ 改善幅度只有 **16%**（15.09% → 12.72%），而 §528 实测**生产去重裁 0 字**
　⇒ 从「0 字」走到这个上界需要一套**全新算法**，且两个探索口径都不是产品口径。
⇒ ⭐⭐ **D 的三个口径逐字相同**（341 字 / 7.99% / 27 错）—— 这是 **oracle 自身的一致性自证**：
　它们只在有重复时动手 ⇒ **不是因为退化成 no-op 才与 C 同值**。

⚠ **一个必须一起读的坑**：**CER 不是字数的单调函数**。时间轴口径裁 36 字得 13.02%，
　difflib 只裁 8 字反而得 12.72%（更好）⇒ 裁多了会吃掉真内容。
　⇒ 与 §534.4 同一口径纪律：**「输出字数 − 真值字数」不能单独当质量指标**。

✅ **#25 三个选项现已全部有读数**：甲（按需回溯）= 唯一选项 ·
　乙（改去重口径）= **两种语料各出局一次** · 丙（调小重叠）= §534 出局。
　⇒ 剩下的只是**产品口径**：接受「按需回溯」这个改造，还是继续接受现状。

⚠ 边界：两个 oracle **不是生产算法、也不是产品口径**，只用来定上界；
　⛔ 未在多人抢话/重叠发言/噪声等更真实形态下验证；⛔ 语料仍是同一批 20 句、1 个音色。

### 🔴 #40【本轮新发现】会议链 VAD：**单段超过 60s 时音频被静默丢弃**（§536）

**这不是 #25 的同一个问题。** #25 讲的是「固定 250ms 重叠带来重复」，
本条是**另一份切分器**、另一个失败面：**丢音频**。

#### 一、归属：这一份我之前 13 节一次都没量过

| 链 | 切分器 | 量过吗 |
|---|---|---|
| 随手记 | 不重叠窗口 3s + Go `mergeIncremental` | ✅ §523/§525 |
| **会议链（录音期间）** | **浏览器 `VadSegmenter`** | ⛔ **一次都没有** |
| 会议链（收尾重转） | Go `SplitWAV` 静音切分 | ✅ §527 D 臂 |

⇒ ⛔ §527–§535 那张「会议链现状 C 臂 3s+250ms」表量的是**收尾重转**那条链，
　**不是真机录音期间跑的那份**（`vad-segmenter.ts`，生产参数 `energyThreshold: 0.012`）。

#### 二、缺陷本体（真实类实测，零 ASR 成本）

连续语音（无 1.5s 静音）下的单段读数：

| 连续语音 | 段数 | 自报跨度 | **实际携带** | **丢失** |
|---|---|---|---|---|
| 50s | 1 | 51 488ms | 51 500ms | 无 |
| 70s | 1 | 71 488ms | 60 250ms | **11 238ms** |
| 90s | 1 | 91 488ms | 60 250ms | **31 238ms** |
| 120s | 1 | 121 488ms | 60 250ms | **61 238ms** |

机制三句话：`sliceBuffer` 只保留最近 60s（省内存）→ **`finalizeSegment` 正是从它取片**
⇒ **保留窗口同时是段的音频来源** → 而 `startMs/endMs` 仍按真实起止自报。
⇒ ⛔ 丢失量随发言时长**无界增长**，无日志、无告警、无回执。

#### 三、两条到达路径

1. ⛔ **高底噪**（触发条件本身⛔未测，需真人录音）：背景电平 ≤ 0.0117 → 8 段；
   **≥ 0.0118 → 1 段**，**阶跃塌陷，没有中间态**。有效阈值 0.0118 = **−38.6 dBFS**
   （8 位量化只把标称 0.012 挪了 1.2%，不是主因）；
2. ⛔ **页面切后台**：`tick()` 挂在 `requestAnimationFrame` 上，不可见时不回调；
   而 `MediaRecorder` 继续出片、继续把 `sliceBuffer` 挤到只剩 60s
   ⇒ 实测丢 31.2s，与「90s 连续语音」档逐位相同。
   ⚠ 产品知道这个风险但**只做了告知**（`onHidden` 弹 toast「请保持应用在前台」），无防御。

#### 四、⭐ 严重度：内容能补回，**说话人结构不能**

- ✅ **文本不丢** —— `meeting-recording-finalize.ts:136` 在 refine **之前**调
  `refetchFullTranscript` → `transcribeFull` → 服务端静音切分对整段 webm 重转；
- ⛔ **说话人整场塌成一个** —— `attributeFullTranscript` 按**时间重叠**把整段回执挂回
  原 VAD 段（每段一个桶）；塌成 1 段时**全场回执全进那一个桶**；
- ⛔ **精校输入只剩一行** ⇒ 提示词第 6 条「保留每行开头的 `[说话人]` 标记」恒真满足。

⇒ ⚠⚠ **只按「我们有全量重转」去审计会得出「没问题」** —— 与 §530 的「随手记侧零入口」同族：
**能力存在 ≠ 能力在用户那条路上生效**。

#### 五、⭐⭐ 方法论教训：这批语料的静音是**数字零**

`meet.wav` 有 **25.2%** 的 100ms 窗 RMS < 1e-9，`silence.wav` **100%** 为 0
⇒ 真实静音是**有底噪的连续信号** ⇒ ⛔ **§527–§535 的全部「会议链现状」读数
都取自「静音=数字零」的语料，结构性地看不见单段塌陷这个形态**
⇒ 不是某一节的疏漏，是**整批语料的共同盲区**（与 §527「语料缺结构 ⇒ 机制结构性失明」同族，第二例）。

#### 六、待拍板

**建议同批做的两条纯技术修复**（甲/乙，不改产品口径）：

| | 做法 | 解决 |
|---|---|---|
| **甲** | 给 `VadSegmenter` 加 `maxSpeechMs`（如 20s）**强制分段**，超限就 finalize 并前移 `speechStartMs` | 把段长有界化 ⇒ 永远进不了 60s 窗口 ⇒ 两条路径一起消失 |
| **乙** | `finalizeSegment` 里切片覆盖不完跨度时**不许静默出段**（补齐或显式留「缺 N 秒」） | 止损：宁可少一段也别**假装完整** |
| 丙 | 底噪自适应（估噪声底 + 动态阈值） | 解决**塌陷**本身，但不解决丢失；且会影响 §528 那些读数的可比性 |

⇒ 🔴 **需你拍板的一点**：「底噪高时是否接受单段长音频」是**产品口径**，与 #35 一起拍。
⚠ 甲/乙 我可以按上面直接实现（纯前端、不动 ASR 口径、不改现有读数），但**未擅自改行为**。

### ✅ #40 修复落地（§537）：**甲/乙 已实现**，丙未做

**取值不是随手取的**：会议链每次段转写走 `sttApi.transcribe`，后端单次路径**不切段**
（`SplitWAV` 只在 `transcribeFull` 用），而能力表里最紧的上游是
`glm-asr-2512` 的 **`MaxSeconds: 30`** ⇒ 段长必须留在 30s 以下，否则被上游直接拒收。
选 **20s**，远大于正常段长 1.5–4s ⇒ 正常形态根本碰不到。

修复前后（同一替身、同一口径）：

| 场景 | 修前 | 修后 |
|---|---|---|
| 正常（6 段×3s） | 6 段 / 4500–4750ms | ⭐ **逐位相同，无回退** |
| 连续 70s | 1 段，**丢 11 238ms** | 4 段，最长 20 000ms，**丢 0** |
| 连续 90s | 1 段，**丢 31 238ms** | 5 段，最长 20 000ms，**丢 0** |
| 连续 120s | 1 段，**丢 61 238ms** | 6 段，最长 21 488ms，**丢 0** |
| 高底噪 bg=0.02 | **1 段** | 2 段，最长 21 488ms |
| rAF 停摆 32s | 1 段，**丢 31 238ms** | 5 段，**丢 0**（甲 顺带修好） |
| rAF 停摆 92s（> 60s 窗口） | — | 9 段，首段起点对齐到 39 750ms，**告警 1 条** |

#### ⭐⭐ 修法自己有两个坑（都是实测逼出来的）

1. **起点回推会把「丢失」藏进对齐动作里** —— 为让时间轴诚实（说话人归属依赖它），
   把 `speechStartMs` 对齐到保留窗口边缘，结果 `span` 被拉回 `covered`，
   放在 `finalizeSegment` 里的覆盖检查**永远绿** ⇒ 丢得**看不见**了。
   ⇒ **告警必须发在检测到丢失的那一刻**（起点回推处），不能发在事后检查里。
   ⇒ 通则：**任何「先把元数据对齐/修正」的兜底，都会让针对产物的不变量检查失效**；
   不变量检查要挂在**事件源**上，不是挂在**被修正过的量**上。
2. **一次 `finalizeSegment` 发大段 ⇒ 段长反而比封顶还长**（停摆 32s 那档实测 `span=40 000ms`）
   ⇒ 改成 `while` 循环按 20s 逐段发出。

⚠ 我自己的断言也错过一次：原写「停摆 32s 必须告警」，实测零告警**且零丢失**
⇒ **前提被推翻时先查量具的前提，而不是放宽断言。**

#### 修完之后仍然存在

- ⛔ **段数塌陷本身仍在**（丙未做，属产品口径）：底噪 > 0.0118 时仍只切 20s 等份段，
  不是按语义边界；
- ⛔ **停摆 > 60s 仍会丢**（保留窗口的物理上限），但现在是**有告警的丢**；
- ⚠ 段长上界 21 488ms = 末段按静音收尾天然多带一个 `silenceMs`，不是漏网。

⚠ 临时量具按惯例量完即删，**未留在仓里** ⇒ 本项目前**没有门禁**，
若要长期守住需要另立测试（我不擅自加第 46 项门）。
⇒ 🔴 **仍需你拍板**：丙（底噪自适应）要不要做 —— 它会把阈值动态化，
　从而影响 §528 那些读数的可比性。
### ✅ #40 回归门已补（§538）：常驻测试 + 变异自证，**门数仍 45**

新增 `frontend/src/native/__tests__/vad-segmenter.test.ts`（5 用例），
接进 **已有的 `test:native`** ⇒ **不是新增第 46 项门**；
`test:native` 用例数 47 → **52**，`npm run gates` **45/45 通过（72.0s）**。

变异自证（门有没有牙）：

| 变异 | 读数 |
|---|---|
| **M1** 拆掉强制封顶 | ⛔ **4/5 红**（连续语音 70/90/120s · 高底噪 · 停摆<60s · 停摆>60s） |
| **M2** 拆掉丢失告警 | ⛔ **精确 1 条红**（只有「停摆>60s」） |
| 还原 | 5/5 绿，`diff` 与备份**逐位一致** |

⚠ M2 只红 1 条**是正确结果**：告警只服务「停摆超过保留窗口」这一条路径，
让它在别的用例也红反而是把判据写宽了。

⭐ **顺带修了一处我自己量具的口径错误**：第一版测试没传 `maxSpeechMs`，
守的其实是**类默认值**而不是生产接线 ⇒ 生产把 `20_000` 改掉门也会全绿。
现已与 `recordingRuntime.ts` 显式传的值对齐。

⚠ **这道门守什么、不守什么**：
- ✅ 守住：音频丢失（3 档时长）· 段长上界 · 丢失可见性 · 时间轴诚实性；
- ⛔ **不守丙**：底噪自适应未做，段数塌陷本身仍在，门只保证「塌陷了也不丢音频」；
- ⛔ 不守真机差异（真实底噪 / MediaRecorder 漂移 / rAF 节流节奏）——
  但**丢音频已与这三项解耦**：段长有界 ⇒ 任何触发形态都进不了 60s 窗口。
### 🔴 #41【本轮新发现】随手记链解码工作量**二次增长**：安全时长 = 3 × 实时解码倍率（§539）

**不是丢音频，是工作量随时长平方增长。** 随手记每 3 秒一片，
`takeNewWindow()` 每次都把**累计全部音频**重新 `decodeAudioData` 一遍
（`recording-audio-transcode.ts:228-231`，每片都调、无节流）。

累计喂给解码器的音频（注入点精确计数，**不依赖设备与真实解码速度**）：

| 录音时长 | 累计喂给解码器 | = 录音时长的 | 公式 T²/6 |
|---|---|---|---|
| 60s | **630s** | 10.5× | 630s |
| 300s | **15 150s** | 50.5× | 15 150s |
| 1200s | **240 600s** | 200.5× | 240 600s |
| 1800s | **540 900s** | 300.5× | 540 900s |

⭐ 自证：时长 ×4 ⇒ 解码总量 **×15.9**（线性应为 ×4）。

#### 翻译成可用的产品约束

第 k 片要解 3k 秒音频，必须在下一个 3s 窗口内解完 ⇒ **不失速条件：实时解码倍率 > T/3**

| 设备倍率 | 推出的安全时长上限 |
|---|---|
| 200× | 10 分钟 |
| 100× | **5 分钟** |
| 30×（中低端手机） | **90 秒** |

⚠ **倍率是唯一未测的量**（需真机），但公式与上限的形状不需要测。
⇒ ⭐ 而「随手记」正是短记录场景 ⇒ 这条约束**直接落在最常用的用法上**。

#### 失速后不是「慢」，是自我放大

`sliceChain` 串行化每片（`recordingRuntime.ts:1024`），而 `ondataavailable` 推片不走这条链
⇒ 解码器持续增长、链条持续积压，**每排一队要解的音频比上一队更多**
⇒ 即时转写越来越落后于用户正在说的话，**且没有任何提示**。

#### 阴性结论（顺手排除，别重复查）

- ⛔ `RollingWebmDecoder` **不是** §536 那类「保留窗口即数据来源」的缺陷：
  `this.parts` **从不被裁剪**，只推进游标，`takeFull()` 返回全量 ⇒ 不丢音频。
  ⇒ **判别动作：看它裁不裁，别看名字里有没有「Rolling」。**
- ⛔ **会议链没有这个问题**：每段是 ≤20s 独立 blob，API 层有
  `ensureGatewayCompatible(...)`（`stt.ts:90`）转码，不做累计重解。

#### 待拍板（改法是架构级，本轮**未改任何代码**）

| | 做法 | 代价 |
|---|---|---|
| 甲 | 增量解码（保留初始化段 + 有界尾片） | 需验证「从中段起解 webm」的可行性与音质 |
| 乙 | 给随手记设时长上限并提示 | 短，超限怎么办？ |
| 丙 | 分段落盘后拼接 | 改动最大 |
⇒ 🔴 **需你拍板**：走哪条；以及「随手记」这个场景你预期录多长
　（预期时长直接决定要不要动架构）。
### ✅ #34 读数更新（§540）：骑缝错法**已修**；且发现 #34 应**扩到裸钟点**

**一、骑缝两种错法已修（技术缺陷，非产品口径）**

| 输入 | 修前 | 修后 |
|---|---|---|
| 「明天下午点两点半」 | 02:30（差 12 小时） | **14:30** |
| 「明天下午点三点」 | 03:00 | **15:00** |
| 「晚上点八点」 | 08:00 且日错 | **20:00** |
| 「明天下午2点点半」 | 14:00（差 30 分钟） | **14:30** |
| 「明天下午2点点20分」 | 14:00 | **14:20** |
| 「上午10点点一刻」 | 10:00 | **10:15** |

修复前 9 个骑缝句 **0 个**正确，正常 6 例全对；修复后骑缝全对、正常 6 例**逐位不变**。
两处骑缝时按本文件立场**退回「只有日期」09:00**，而不是静默给一个错分钟的时刻。

门：新增 `meeting-due-seam.test.ts`（18 用例，归既有 `test:all`，**门数仍 45**）；
变异拆 P1 红 2 / 拆 P2 红 4 / 拆兜底守卫红 2，还原 18/18 且逐位一致。

**二、⭐ #34 的口径应当扩到「裸钟点」**

本节实测：**裸「两点」→ 凭空造出 10-14 02:00**。
这和裸「N号」是**同一类**（句中没有日期锚却硬造一个），而且危害更大——
**02:00 根本不是人说过的时刻**，而「下月 3 号」至少还在日历范围内。

⇒ 🔴 **#34 请按一条口径一起拍**：A（同句无日期锚 ⇒ null）同时覆盖
　① 裸「N号」　② 裸钟点　③ 其它裸日期形态。
　本节**未替你决定**，只登记。
⚠ 另有一条**明确不修**并已登记：「明天三点四点」→ 03:04（相邻双钟点，输入本身歧义；
　收紧会伤到「五点二十点」= 20:00 这类口语）。
### 🔴 #42【本轮新发现】录音期间**同一段音频被两套 ASR 各转写一遍**，两份都进最终正文（§541）

| | 用哪套 ASR | 落到哪 |
|---|---|---|
| **实时字幕** | **Web Speech API**（系统引擎） | `isFinal` → `appendText` → **`saveSegment` 落库** |
| **分片转写** | **网关 ASR** | `onSegment` → `processSegment` → `saveSegment` 落库 |

⇒ 两条链在 `start()` 里**同时启动**，同一段音频转写两次、两份都进同一批会议分段。
⇒ 唯一关闭条件是 `hasForegroundHijack()`（MIUI 抢前台那类设备），**默认 false**。

#### ⭐ 真正的伤害不是「多几行」，是**那一行永远改不掉**

`appendText` 用 `buildUtteranceSegment`，而那个函数的注释写着「这一行是**人手工敲的**…`origin: 'manual'`」
⇒ ⛔ **机器产出的字幕被标成「用户亲手写的」**（连带 `speakerLabel` 是字面量、
`confidence: 0.7` 是占位、`endMs` 是**编的**）。
⇒ 而 `attributeFullTranscript` 对它的立场是「分到某段却没分到正文时，**保留该段自己的原文本**」。

实测（真实纯函数）：收尾全量重转之后，字幕段**仍带着自己的旧文本**，
正文里同一句话出现两次（一次高质量、一次低质量）。

⇒ ⭐⭐ **全量重转这一整套，唯独漏掉了实时字幕那一段**——
　而它恰恰是**质量最低的那份**，却因 `origin: 'manual'` 被拒绝覆盖。

#### ⭐ 我第一版的假设被自己的对照实验否掉

我本来要论证「标 manual 让去重失效」，单变量实验（两条 asr vs 一条 asr+一条 manual）**两条都是 2 次**
⇒ 重复**不是**标签造成的，是 `dedupeStep` 压根不合并**整段重复**（它只裁相邻重叠尾巴）
⇒ **修去重方向本来就错**。

#### 待你拍板（涉及分段契约，我不代拍）

| | 做法 | 影响 |
|---|---|---|
| **甲**（推荐） | `SegmentOrigin` 增加 `'caption'`，字幕不再冒充人工段 | `origin` 列是**无约束 TEXT**，DB 零迁移；但要改类型、读回强制、去重/归属两条链的立场。改完好处：重转会用高精度文本覆盖它 |
| 乙 | `appendText` 不落库，只喂 `interimCaption` | 最彻底，但**网关不可达时字幕是唯一文本**，会连内容一起丢 ⇒ 必须配降级 |
| 丙 | 两条链择一：能拿到网关 ASR 就不启动 Web Speech | 改动最小，但实时字幕比分片快，体感会变慢 |

⇒ 🔴 **最关键的一问：最终正文你希望保留哪一份？**
⇒ ⚠ 本节**未改任何代码**；⚠ 真机上两套引擎的**实际重合比例未测**（理论上同音频同内容）。

#### 🆕 §552 追加（2026-10-13）：本条标题要**降一档确定性**，且选项空间被削掉一个

> ⛔⛔⛔ **本小节整体已被 §553 推翻并重写 —— 见下方「§553 追加」。**
> 保留原文是为了记录**我先断言、后核对**的顺序（先写 §552 时只读了签名）。
> ⚠ **不要按本小节的三条读数做决定**；以 §553 的实测为准。

| §552 新增 | 证据 | 对本条的影响 |
|---|---|---|
| ⭐⭐⭐ **「两份都进最终正文」是有条件的** | `planRefine`（`meeting-refine-plan.ts:100-109`）二选一：**A** `adopt.adopt && full.text` → `buildRefineBaseSegments` 的基线是**整段重转结果**，字幕段文本**在这一步就被丢弃**；**B** 否则 → 直接回落那张**双份段表**，而生产提示词第 3 条「不得删除」+ 第 6 条让 LLM **结构上无法移除重复** | ⛔ **§553 实测这条是反的**：字幕段 100% 保留旧文本，A/B 两支都成立 |
| ⭐⭐⭐⭐ **A 是意图不是事实**（⛔ 未验） | `toWav` ✅ 已接（`meeting-recording-finalize.ts:143`，§50 修复在位）；但**网关逐段回执** ⛔、**真 webm 转 wav 后 SplitWAV 能否切多段** ⛔、**`finalizeRecording` 端到端跑通** ⛔（§50 自述「node 里跑不到」，现由接缝门 + 源码门两段拼着覆盖） | ⇒ 按「能力存在 ≠ 能力在用户那条路上生效」，**不能假定重复是暂态的** ⇒ ⚠ **不能只验去重，必须把 A 链一起验** |
| ⭐⭐⭐⭐ **假时间轴是接口缺口，不可修** | `SpeechRecLike.onresult`（`meeting-live-caption.ts:12`）类型只有 `{ isFinal; 0?: { transcript } }` —— **Web Speech 不提供逐句时间戳** ⇒ 字幕段永远拿不到真 `startMs`/`endMs` | ⭐ **事实仍成立**（接口缺口），但 §553 实测它**不造成误归属**（0/19）⇒ 它不再是「选项被削」的理由，只是一个未修的接口事实 |
| ⭐⭐ 误标是**四个字段**共享一个错误前提 | `origin`(41) / `confidence:0.7`(34) / `speakerLabel:'说话人'`(18) / `lang:'zh'`(19) 全都按「入参是手写的一句话」赋值 | ⚠ **只改 `origin` 得到的是四分之一修正**（仍享受豁免，却仍冒充人工、仍带假置信度、说话人仍不可用）⇒ 这条**改变甲的代价**，不改变缺陷存在性（那四个字段本条正文早已列出，§552 就地勘误过） |

⇒ ⚠ **仍然只有那一句需要你拍**：最终正文保留哪一份。
　 ⛔ 但**验的顺序要改**：A2（一行 `webkitSpeechRecognition`）原只为定 #21 的平台归属，
　　⭐ **§552 给它加了第二个用途 —— 决定「重复是暂态还是永久」** ⇒ **A2 从「定平台」升为「定重复持久性」。**

#### 🆕 §553 追加（2026-10-13）⭐⭐⭐⭐ 实测推翻 §552 的两条断言 —— **本条结论比原来更强，不是更弱**

| §553 实测（用**真函数**，不是复刻） | 读数 | 对本条的影响 |
|---|---|---|
| ⭐⭐⭐⭐ **字幕段 100% 保留自己的旧文本** | 6 种交织形态 × 3 种回执切分 + 1 种退化形态 = **19 格，回执落到字幕段 0 次、字幕段保留旧文本 19/19**（唯一的例外是「一条 VAD 段都没有」的退化形态，那时字幕段是唯一候选桶，但它们的 `speakerLabel` 全是「说话人」，覆盖了也换不回任何真实归属） | ⭐⭐⭐⭐ **「两份都进最终正文」是无条件的**（A 支 B 支都成立），**本条标题一个字都不用改**。⚠ **收回 §552 那句「标题要降一档确定性」—— 不要降** |
| ⭐⭐ **假时间轴不造成误归属** | 含「字幕段被用户改成真名」这一**可达**形态（`segment-edit.ts:64` 改文本会把 `origin` 翻 `manual`，而字幕段本来就是 `manual`）⇒ 误归属 **0/3**，与纯 ASR 对照臂相同 | ⇒ 「假时间轴不可修」是接口事实（`SpeechRecLike.onresult` 无时间字段），但它**不是**害处来源 ⇒ §552 拿它当「选项被削」的理由**不成立** |
| ⭐⭐⭐⭐ **段表与说话人分布的代价** | 4 条 ASR 段 + 4 条字幕段 = **8 段进 refine**，其中 **4 段（50%）说话人是兜底的「说话人」**，且那 4 段的文本是**另一个引擎的低精度版本** | ⇒ 害处的准确形状 = **漏掉纠错 + 一半兜底说话人**，**不是误归属** |
| ⭐⭐⭐⭐⭐ **#20 甲的代价被量化，且非单调** | 跨引擎相似度扫描：⭐ **完全相同 ⇒ 裁 0 字**；⭐ **只差一个标点 / 语气词 / 同音字 ⇒ 裁掉 14 字（93%）**；~70% 及以下 ⇒ 裁 0 字 | ⇒ ⭐⭐⭐⭐ **「两引擎越像越危险」是错的** —— 危险的是「几乎一样但不完全一样」，而那正是同一段音频经两个引擎转写时**最常见**的样子；**字节级相同反而安全，而真实设备上它从不发生** |
| ⭐⭐⭐ **连带推翻 `background-mic.ts:117` 的推理** | 该处写「两个引擎产出本就不同 ⇒ 覆盖率 <0.6 ⇒ **去重消解不了**」 | ⇒ 前半句在 94~98% 档**仍成立**（差一个逗号也算「不同」），**后半句在危险档上是错的** ⇒ **「产出不同」推不出「消解不了」** |

⇒ 🔴 **结论没变，反而更硬**：重复**无条件**进最终正文，而 #20 甲**不是零代价**，
　 且其代价接近上界。**唯一缺的读数是真机上两引擎文本落在哪一档相似度**
　 —— 那决定甲的实际代价是 0 字还是 14 字。**A2 + 真机录音，仍然是同一个卡点。**

#### 🆕 §554 追加（2026-10-13）⭐⭐⭐⭐⭐ **本条不是「修 bug」，是「推翻一条被测试名字钉着的有意取舍」**

| §554 实测 | 读数 | 对本条的影响 |
|---|---|---|
| ⭐⭐⭐⭐ **机制是同一行代码，且被一条测试显式守着** | 变异 M1（删掉 `attributeFullTranscript:329` 的 `\|\| o.text`）精确红 **1** 条，那条测试的名字是「**整段重转完全没覆盖到的段，保留它自己的原文本（宁可重复也不丢内容）**」 | ⇒ 🔴 **#42 的重复之所以永久，是因为仓里有一条写着理由的测试在守着它。** 要修它的人必须知道自己在推翻哪一条、为什么当初这么选 —— **这一点原文没写，是本节补的** |
| ⭐⭐⭐⭐⭐ **字幕段的归属由「回执数 / 总段数」这个比值决定，与它自称的时间轴无关** | 扫 18 格：**≤0.5 ⇒ 0%** 保留旧文本 · **0.67~0.75 ⇒ 泄漏 33~50%** · **≥1.00 ⇒ 100%**，最大时间错位 **10.5 秒** | ⇒ ⛔ **收回 §553.2 那句「假时间轴不造成误归属」** —— 它的 0/19 是**语料比例造出来的**（12 段配 3 回执，结构上轮不到残桶）。这是「语料缺什么结构，机制就对什么失明」的**第四例** |
| ⭐⭐⭐⭐ **一行活代码零覆盖** | `if (best < 0) best = pickBucket(..., false)` 是**活代码**（1 段配 6 回执时，没有它第 2~6 条回执被静默丢弃 = 丢 83% 高精度文本），但变异 M3 删掉它，**335/335 个测试文件仍全绿** | ⇒ ✅ **已补判据**（加进既有 `refine-speaker-attribution.test.ts`，门数仍 45）⇒ M3 现在精确红 1 条 |

⇒ 🔴 **拍板时要知道的三件事**（按重要性）：
　 ① **重复是有意的取舍**（「宁可重复也不丢内容」），不是疏漏；
　 ② 字幕段的**归属**在低比值下是安全的（保留旧文本 ⇒ 只是重复），
　　在**比值 ≥1 时才会错挂**（错位可达 10.5 秒）—— ⛔ **生产真实比值未测**；
　 ③ ⚠ **§548/§550 实测形态推算比值约 0.2~0.4 ⇒ 大概率落在 0% 那一侧**，
　　但那是推算不是测量，真实会议的静音分布会改写它。

#### 🆕 §555 追加（2026-10-13）⭐⭐⭐⭐⭐ 把「推算」测掉了 —— **方向是反的**，而真相比推算更难处理

| §555 实测 | 读数 |
|---|---|
| ⭐⭐⭐⭐ **两侧分母都量出来了**（**真 `VadSegmenter`** 生产参数 0.012 + **真 `stt.SplitWAV(data,25)`**，同一批 §550 的 35 组网格） | 比值：无字幕假设 min 0.69 / **均 1.18** / max 4.60 · 1:1 交织假设 min 0.34 / **均 0.59** / max 2.30 |
| ⛔ **推翻 §554.6 的推算**（那里写「0.2~0.4 ⇒ 落在 0% 那侧」） | ⛔ **方向反了**：无字幕假设下均值 **1.18 已在 1.0 那一侧**；1:1 假设下**只有 12/35 组**落在 0% 那侧 |
| ⭐⭐⭐ **机制是配置事实不是音频事实** | `SplitWAV` 的 `minSilenceMS=800` 比 `VadSegmenter` 的 `silenceMs=1500` **更细** ⇒ **回执 ≥ 实时段是常态**（23/35 组） |
| ⭐⭐⭐⭐⭐ **闭环：不是二选一，是同场并存** | 把**实测段数对**喂进真 `attributeFullTranscript`：字幕段**保留旧文本 34/35** · **拿到回执 23/35** · **两者兼有 22/35** · 典型时间错位 **约 5.3 秒**（VAD 失效格可达 **65 秒**） |

⇒ ⭐⭐⭐⭐ **本条要拍的那句「最终正文保留哪一份」，现在多了一层**：
　 字幕链不只制造**重复**，它还把**高精度正文挂到自称时间戳是编的段上**
　 —— 而 `attributeFullTranscript` 存在的**全部意义**就是按时间把正文挂回说话人。
　 ⚠ §553.1 的「100% 保留」与 §554.2 的「≥1 ⇒ 100% 覆盖」**都是语料产物**，
　 正确表述是**两者在多数形态里同时发生**。

⇒ ⚠ **一个配置事实带出来的新选项（已登记，不代拍）**：
　 把前端 `silenceMs` 从 **1500 降到 800**（与 `SplitWAV` 对齐）就能让比值下降。
　 ⚠ **这不是 §550 排除的那个「调低阈值」** —— §550 排的是 `energyThreshold`
　 （降它会把失效线从 −38 移到 −48 dBFS），这里动的是 `silenceMs`（**切段耐心**，不改变「哪段是语音」）。
　 ⛔ **代价在别处且方向相反**：段数上去了 ⇒ 段更短 ⇒ §546 量到的**边界损伤比例上升**
　 （3s 硬切 SV 22/27 = 81.5% · GLM 81/83 = 97.6%）。
　 ⇒ **两个代价反向，且都不是本机能定的** ⇒ 并入 #42 一起拍。

#### 🆕 §556 追加（2026-10-13）⭐⭐⭐⭐⭐ **`silenceMs` 扫描：拐点在 1200，不是 800** —— §555.5 我自己提的方案被自己的数据推翻

| `silenceMs` | VAD 段均 | 比值（1:1 交织） | **落在 0% 侧** | **平均泄漏率** |
|---|---|---|---|---|
| **1500（生产）** | 14.7 | 0.59 | **12 / 35** | **17%** |
| ⭐ **1200** | 16.7 | 0.53 | **34 / 35** | **3%** |
| 1000 | 16.8 | 0.53 | 34 / 35 | 3% |
| 800（§555.5 提的） | 17.0 | 0.52 | 34 / 35 | 3% |
| 600 | 17.2 | 0.52 | 34 / 35 | 3% |

⇒ ⭐⭐⭐⭐ **拐点在 1200**：1500→1200 就把「落在 0% 侧」从 12/35 拉到 **34/35**、泄漏率 **17%→3%**；
　 而 **1200/1000/800/600 四档读数完全相同**，VAD 段数却从 16.7 涨到 17.2
　 ⇒ **800 相对 1200 零额外收益**。⇒ **1200 在两个轴上同时优于 800**（收益相同、代价更小）。

⇒ ⭐⭐ **「与 `SplitWAV` 的 800ms 对齐」这个框架本身误导**：
　 真正起作用的是**跨过 1200 这个拐点**，不是对齐某个具体数字。
　 ⭐ **要压的是「比值」，不是「段数」** —— 比值进到 0.5 以下后，
　 再压 `silenceMs` 只是徒增段长碎片（§546 那一侧的代价）。

⇒ ⚠ **⛔ 两侧只量了一侧**：本扫描只答「比值落到哪一侧」。
　 另一侧（段从 14.7 涨到 16.7 ⇒ 段更短 ⇒ §546 的边界损伤上升多少）
　 **方向已知、幅度未测**（要真 ASR，⛔ 无凭据）⇒ **本条仍不代拍**。


#### 🆕 §559 追加（2026-10-13）⭐⭐ 语料限制**已量过**，结论站得住

§555/§556 的因果链建立在「两个切分器的静音阈值差决定谁切得更细」上，
而「细多少」由**语料的停顿分布**决定 ⇒ 该量而不该假设。

**量完的读数**（正确量具：16000Hz / 20ms 窗 / 阈值 0.012 / 真正被驱动的 35 个网格文件）：

| | 读数 |
|---|---|
| 出现 **≥800ms** 停顿的组 | **27 / 35** |
| 出现 **≥1500ms** 停顿的组 | **27 / 35** |
| 每组最长停顿 | 中位 **1640ms** · 最大 **7840ms** |

⇒ ⭐⭐⭐ **§555/§556 的因果链成立**：`silenceMs` 确实有东西可作用（每组约 19 个 ≥1500ms 停顿）。

⚠ **但顺带看清一件事**：那 **8 个「零静音游程」的组**（`s-*n-20` / `s-*n-25`）
在 0.012 阈值下 **RMS 从不掉下去** ⇒ VAD 看到连续讲话 ⇒ 段数退化成封顶行为（5~6 段）。
⇒ ⭐ **「段数掉到 5~6」有两种同形成因**：① VAD 失效（§548 登记）② **底噪高到掩住一切**。

⇒ ⚠ **仍然成立的限制**（语料问题不是量具问题）：本语料 TTS 合成，
**每组恰好约 19 个 ≥1500ms 停顿**这种规整结构真人会议不会有
⇒ **具体数字**会随真实静音分布变化，但**机制**不依赖语料规整度。
### 📋 #43 用户诉求**逐条对账**（§542）：十个子项，5 项有真实空白

| 子项 | 状态 |
|---|---|
| 转写不准 | ✅ **查出并修掉 3 个缺陷**（§536 丢音频 / §540 骑缝字 / §541 字幕双写） |
| 不同段放一起校对合并 | ⚠ 已实现但**实测无效**：生产去重裁 0 字，oracle 上界也追不上 ⇒ 该改切分不是去重 |
| 录音后一次精校 | ✅ 链路完整（重转 → 挂说话人 → refine） |
| **录音时即时总结** | ✅ **已实现**（`useLiveSummary` + `live-summary-schedule`，§424 修过两次） |
| 参考资料 | ⚠ 有，但**来源是用户自己写过的笔记**（UI 如实写「参考资料 · 你写过的笔记」） |
| 建议 | ✅ 有（优先检索 kxmemory；无则 LLM 兜底 + Bing 搜索链接，UI 明确区分可信度） |
| 智能体 | ✅ `backend/internal/meetingagent`（有界 tool-calling 循环） |
| **「到网上搜索」** | ⛔ **仍没有**。`web` 建议只把 query 拼成 Bing 链接（`server_meeting.go:703`）、**没真取回网页**；智能体工具面实测**只有 `search_notes`**（`meetingTools()`，且系统提示词明确**禁止编造引用**）· ⚠ **措辞收窄**（§600）：不是「全仓无联网工具」，而是「全仓**无任何能取回网页内容的工具**；仓里已有一个完整 MCP 客户端 `backend/internal/mcp/client.go`（27KB + 6 测试），但只连内部 ACC 的 task 工具」· ⭐⭐⭐⭐⭐ **接入路径已完全打通**（§601 核销 §600.4 的未核项）：`mcp.Client` **原生支持公网标准鉴权** `Authorization: Bearer <api-key>`，且在 **401/400** 时自动从 HS256 JWT 回退（`client.go:226-234`）⇒ `scopes`/`tenant_id` **不构成阻塞**（它们只进 JWT claim，回退分支根本不发 JWT）· ⇒ **缺的全部只剩两件**：一段「MCP tool → `meetingagent.Tool`」的**适配代码**，以及**一个公网搜索型 MCP server 的 URL + API key**（外部输入）· ⚠⚠ **一个需当场验证的安全取舍**：回退只认 **401/400**，**403 不回退**。对内网这是**对的**（403 = token 有效但 scope 不够，回退等于绕过授权 ⇒ 不可接受），但公网 server 常用 **403 表达「key 无效」** ⇒ 接入时**必须先发一次 `initialize` 看它返 401/400 还是 403**。⛔ 加 403 回退会**破坏内网授权语义**，故登记不代拍· ⛔ **全程未实测**（无可打的公网 server）⇒ 「能接上」是**静态结论**，非实测|
| 时间点自动入日程 | ✅ 已接线，并修了 2 个解析缺陷 |
| 学习讯飞/高星开源 | ⚠ 已对照，**全部二手来源**（未开官方文档、未试用） |
| 更便宜的 ASR | ⛔ 无法验证（`model_pricing`/`model_offers` 都无 ASR 报价；三个远端端点 401） |

⇒ 🔴 **与你的原话有落差的一处**：参考资料与建议**都已有**，但它们来自**你的笔记**和
**模型现编的搜索建议**，不是从网上取回的资料。**「上网搜索」这一半未实现。**

#### ⭐⭐ 自我更正：差点把「即时总结」写成「没实现」

查的时候我只 grep 了 `recordingRuntime.ts`（零命中），正准备写下
「**这条需求完全没有实现**」。**那是错的** —— `liveSummary` 有完整实现，
只是在 `composables/useLiveSummary.ts` + `features/meetings/live-summary-schedule.ts`，
外加 `local_meetings.live_summary` 列与面板消费点。

⇒ ⭐ **教训**：「某文件里 grep 不到」只能支撑「**该文件里没有**」，
　不能支撑「**这个能力没有**」。跨文件的装配链会让单文件搜索天然失效。
　⇒ 判别动作：能力不在预期文件时，先 `grep -rn <字段名> src/`，
　把「谁写它、谁读它」两端各找一次再下结论。
⚠ 更危险的是我**差一点把这个错误结论写进两份文档**——那会成为下一轮的错误基线。
### 🔴 #44【阻塞项已解】ASR 真实报价：**实时流式里现状已接近最优**，但漏了一个原生参数

**⛔ 「无价格源」这个阻塞项本节解掉**：价格源不在系统里，在**网上**。

| 服务 | **元/小时** | 实时流式 |
|---|---|---|
| **阿里云百炼 `paraformer-v2`** | ⭐ **0.288** | ❌ 异步任务 |
| **智谱 `GLM-ASR-2512`（本仓在用）** | ⭐ **0.72** | ✅ |
| 阿里云 `fun-asr-flash-8k-realtime` | 0.792 | ✅ |
| 阿里云 `paraformer-realtime-v2` | 0.864 | ✅ |
| OpenAI `gpt-4o-mini-transcribe` | ≈1.26 | ✅ |
| OpenAI `gpt-4o-transcribe` | ≈2.52 | ✅ |
| **OpenAI `gpt-4o-transcribe-diarize`** | ≈2.52 | ✅ ⚠ **带说话人分离，与不带同价** |

#### 三条结论

1. ⭐ **实时场景下现状已接近最优**：`GLM-ASR` 0.72 是实时里最便宜的
   （比阿里云最便宜的实时档 0.792 还低），真正便宜的那档 0.288 是**异步任务式**、不适合分片实时。
   ⇒ 「更便宜的 ASR」在实时场景**不需要换模型**。
2. ⭐⭐ **`gpt-4o-transcribe-diarize` 与不带分离的同价** ⇒ **服务端说话人分离可以不自研**。
   本仓现有 `speaker-diarization` + `speaker-embedding` + `speaker-relabel` 一整套
   （§410 记录：同人/异人余弦分布重叠 0.0460、标签膨胀 2.31×→0.92×）。
   ⇒ 贵 **3.5×** 换「不用维护」属**产品口径** ⇒ 🔴 **需你拍板**。
3. ⚠ **别走转售**：同是 GLM-ASR，官方 **0.012 元/分钟**，第三方转售 **0.015～0.06 元/分钟**
   （贵 **1.25×～5×**）⇒ 网关必须直连官方。

#### ⭐ 对 #36（热词）的直接影响：本仓**漏了一个参数**

§526 实测本仓只发 `prompt`（`transcribe.go:526` / `provider_openai.go:84`），
**没发 `hotwords`**。而上游文档写明 GLM-ASR 有**独立的原生入参**：

- `hotwords: array[string]` —— 热词表，建议 ≤ **100 个**
- `prompt` —— 长文本场景的**上下文**（两者是不同参数）

⇒ ⭐ #36 从「先验上游认不认 `prompt`」升级为一个**更确定的动作**：
　`hotwords` 是原生入参，不是要「争取」的能力。
⚠ 但「本仓有没有可用的热词来源」（用户自己的笔记/联系人/项目名能否拼成热词表）仍是独立问题。

⚠ **本节是价格不是质量**：三档的 CER 在本仓口径下**完全未测**
（本仓唯一质量读数来自本机 `sensevoice-small`，不能外推到云端模型）。
⚠ 官方页会变，引用需带抓取日期；多家有 36,000 秒/月 的免费额度，会改变小用量下的排序。


### 🔴🔴 #45【§544 新增】本地 ASR 选型：**第一组实测出来是「不划算」**，且本节推翻了自己第一眼的结论

§543 解掉「无价格源」之后就该比质量。这件事**不需要网关凭据**——两个引擎都能离线跑，
语料复用 §527 的 `meet.wav`（106.97s / 338 字 / 20 句，macOS Tingting TTS 合成）。

**因子表（口径1 = §527 原口径，同一 `norm`/`lev` 未改一字）**

| 切段策略 | 段数 | sensevoice-small-q8 | GLM-ASR-Nano-q4_k_m |
|---|---|---|---|
| 整段单发 106.97s | 1 | 3.85% | **93.20%** |
| **生产 `SplitWAV`(25) 静音感知** | 20 | 3.25% | 2.96% |
| 3s 定长硬切（随手记链现状） | 36 | 7.99% | **24.56%** |

`sensevoice` 一列 **三项精确复现 §527**（3.85 / 3.25 / 7.99）⇒ 量具自证通过，GLM 列可读。

⭐⭐⭐ **但「GLM 更好」是口径给的假象。** `sensevoice` 会把数字规范成阿拉伯数字
（3点 / 12% / 30秒），GLM 原样输出中文数字，而真值语料写的是中文数字
⇒ §527 的 `norm()` 不做数字归一 ⇒ **这项差异被整条记成 sensevoice 的错误**。

| 生产形态（SplitWAV） | 口径1 | +数字归一 | +繁简转换（条件假设） |
|---|---|---|---|
| **sensevoice** | 3.25% | **0.60%** | **0.60%** |
| GLM | 2.96% | 2.39% | 0.90% |

⇒ **口径1 下 GLM 领先 0.29pp；口径2 下 sensevoice 好 4 倍；补上繁简 pass 仍略输。**

**其余四条读数**

1. ⭐ **长音频塌陷是真实截断**：106.97s 整段只回第一句。
   逐一排除了 prompt-cache（`--no-cache-prompt` 重跑**逐位相同**，**我判定的「真凶」被推翻**）、
   丢块（server 日志 4 块全在、`truncated=0`）、指令缺失。
   ⚠ cstr 那份 README 宣称的 **655s 单趟在本接法上不成立**。
2. ⭐ **先劣化后塌陷**：36s 内完好，46s 起掉覆盖率，且**非单调**（67.94s 的 90.5% 好于 83.85s 的 51.7%）。
   **插字/错序/语义改写发生在覆盖率还 >100% 时** ⇒ 只盯覆盖率会漏掉这一段。
3. ⭐ **段长不是同一个旋钮**：`sensevoice` 5s→30s 从 8.28% 单调降到 4.73%；
   `GLM` 在 10.95%～21.60% 之间**无规律起伏** ⇒ 调段长救不了 GLM 的硬切。
4. ⭐⭐ **最强的单变量证据**：段数 20 vs 22、平均段长 5.35s vs 4.85s 几乎相同，
   **只差边界是否落在静音上** ⇒ GLM **2.96% vs 18.64%（6.3 倍）**。

**换型的代价**：GLM 2.55s/段 vs sensevoice 0.39s/段（**慢 6.5×**，
M4 Max / llama.cpp b9430 / 单 slot / 含 HTTP+ffmpeg 开销）；
体积 1.78GB（1.06GB LLM + **720MB mmproj**）vs 254MB。
另需**一道繁简转换 pass**（GLM 稳定输出 `結論 暫 開發 點 們 過`，sensevoice 全程 0 个繁体）。

⚠ **另有一类数值指标看不见的伤害**：GLM 把 `异步提交` 写成 `一步提交`
—— **语义反转**（同步/异步），CER 与同音字同量级但危害完全不同。

⚠⚠ **本读数结构上看不见 GLM 的主打优势**：低音量/耳语、Wenet Meeting（噪声+重叠语音）、
方言 —— **三项本语料全部缺席**（TTS 合成、无底噪、无重叠、纯普通话）。
⇒ 这与 §536 是同一族盲区（「语料缺什么结构，机制就对什么失明」）。

**三个选项**

| 选项 | 内容 |
|---|---|
| **甲（推荐）** | **不立项**。本节读数是「无优势 + 更脆 + 慢 6.5× + 多一道繁简 pass」，不足以支撑换型；精力放在 §537 已修的切分缺陷与 #35 的静音感知切分上收益更大 |
| 乙 | 立项但**门槛前置**——先拿到真人噪声语料（阻塞项 1），按同一套因子表重测，含噪下明确胜出才换 |
| 丙 | 立刻换，并接受四项配套：静音对齐切段（≤7s）、永不长音频单趟、加繁简转换 pass、按 2.55s/段 重估实时性 |

**对既有条目的影响**

- **#35**（随手记切段）：结论不变且被加强——静音感知切分对两个引擎都对，且**对 GLM 是 6.3 倍差别**；
  当前 3s 硬切对 GLM 是 22.99%，是全表最差格。
- **#39**（数字统一写法）：**从排版口味升级为有实测支撑的一条**——数字写法差在 CER 上值 **2.7 个百分点**，
  足以改变模型排名。
- **#44**（云端选型）：**不受影响**。§543「实时场景 GLM-ASR 0.72 元/h 已接近最优」是云端价格；
  本节只是说明**再往下往本地换没有便宜可占**。两件事不要混谈。
- **#41**（随手记解码）：**未测**。`RollingWebmDecoder` 是 JS 解 webm，与本节原生模型无关；
  2.55s/段 是 **GLM 推理**耗时，**不可代入 #41**。

⚠ **未测项（不得当已测）**：真人噪声语料下的 CER、transformers 参考实现下的 655s 单趟、
方言与低音量、其他设备档位、含重叠语音时的表现。


### 🔴 #46【§545 新增】`silenceCut` 是**空开关** —— 服务端搬进 struct 就丢，字段注释承诺的「更保守去重」从未实现

§544 把「边界对齐静音」确立成最强杠杆（**6.3 倍**）之后，顺手问「随手记为什么走不了静音感知切分」，
结果撞上一条**从未被兑现的注释承诺**。

**证据链**

1. `backend/internal/stt/incremental.go` 头注释写死契约：「短段必须**在静音处切**……由调用方的 VAD 负责」
   ⇒ 服务端假定调用方已切好。而**随手记恰恰是反例**（`takeNewWindow()` 纯新增窗口、彼此不重叠）。
2. 字段注释写「为假说明是强制时间切片……**聚合时要更保守地去重**」
   ⇒ ⭐⭐⭐ 而聚合函数 `mergeIncremental(committed, next)` **签名里没有这个参数**，
   去重只有**一套**阈值 ⇒ **「更保守」这个分支不存在**。
3. 全仓两端核对（§542 纪律：单文件 grep 不足）：
   - `full.go` 的 `Segment.SilenceCut`：写于 `full.go:211`，**只有测试**读它做日志/断言；
   - `incremental.go` 的 `IncrementalChunk.SilenceCut`：写于 `server_stt_stream.go:295`，**零读**；
   - 前端 `recordingRuntime.ts:1079` 是唯一调用点、写死 `false`，**全仓无处传 `true`**。
4. **变异实证（两条都跑绿）**：M1 把赋值改成常量 `false`（忽略 HTTP 字段）→ 全绿；
   **M2 删掉字段声明 + 全部 4 处赋值 → 编译通过、`go vet` 干净、全绿**。
   ⇒ M2 是决定性的：**整个字段可以删掉而世界不变**。还原后 4 文件 sha256 逐位一致。

⚠ **别把「改了没变红」和「删得掉」当同一强度**：Go 静态编译能删得掉，
　才证明**无人读**——这比单次「变异没红」强一个量级。
⚠ 还原基线是「动手前的快照」不是 HEAD（`incremental.go` 本节之前已有工作树改动）。

**对 #35 的直接影响（会改变拍板内容）**

| 收益来源 | 存在吗 |
|---|---|
| 切分本身（边界对齐静音） | ⭐ **有**，6.3 倍 |
| 把 `silenceCut` 翻成 `true` | ⭐ **没有**，空开关 |

⇒ ⭐⭐ 若有人按直觉实现甲案，会顺手传 `true` 并以为「少做了一步」
　 ⇒ **收益全在切分侧，不在这个开关**。

**已落地**：两处**如实注释**（`incremental.go` 字段 + `server_stt_stream.go` 的 JSON 字段）。
**刻意没做**：删字段（⚠ 不删死代码，且它是对外 JSON 契约；删掉会把 #35 的待拍板决定
变成已完成的实现事实）· 实现「更保守」分支（属产品口径）· 加「断言它仍是死字段」的门
（⭐ 不擅自加第 46 项门，且那会变成守着「我们没实现某功能」，将来真实现时反而挡路）。

**三个选项**

| 选项 | 内容 |
|---|---|
| **甲（推荐）** | **不收紧，维持现状**。`incremental.go` 自己写着「宁高不低」——收紧会把真实内容当重叠吃掉，**用户永远发现不了**；且随手记当前**不重叠**，去重在那里本就是兜底，对一个不需要去重的场景调阈值收益不明 |
| 乙 | 收紧（硬切时调高 `minFuzzyAnchor` / `anchorCoverage`）。⚠ 需要**缺陷语料**（真机真出现过「相邻片被误判重叠」）才有判据，否则是凭感觉调一个「宁高不低」的闸门，很可能净损失 |
| 丙 | 只把接口语义对齐（承认是「预留」开关），行为不变，并在 API 文档写明当前无效果 |

⚠ **未测**：真机上随手记链是否真出现过「相邻片被误判重叠而丢字」；
`mergeIncremental` 在**真重叠**调用方下的误判率；**`IncrementalOverlapSec` 为何零使用点**
（⚠ 那本身是另一条待查，且与 #15 B 格「补跨段合并」直接相关）。


### 🔴🔴 #47【§546 新增·用户原话第一条】跨段合并的落地状态与天花板 —— 结论是「先修切分，不要先做合并」

用户原话第一条技术主张：「音频是切段的，可能在输出时需要**将不同的段放在一起进行校对合并**」。
本节核了它的落地状态，并量了天花板。

**四个层次的落地状态**

| 层 | 状态 | 证据 |
|---|---|---|
| ① ASR 切分 | 会议链 ✅ 按静音切；**随手记 ❌ 3s 定长硬切** | §527·§544 |
| ② 聚合 | ⭐ **零合并** `strings.Join(texts,"\n")` | `backend/internal/stt/full.go:486` |
| ③ 后处理 | 只覆盖**名单人名**（`repairSegmentBoundaryHomophones` §80），**只换字、不删字、不并段**，名单缺则安全失效 | `meeting-roster-homophone.ts:263` |
| ④ LLM 精校 | ⭐ **有能力但被两条禁令挡住** | 见下 |

⚠ **③ 的范围要说准**：它是「跨段**人名**同音纠正」，不是「跨段**合并**」——名字像、不是同一件事，
把它当需求落地会得到**假阳性**。

**④ 的具体阻塞**：精校提示词第 3 条「**不得删除原文有的内容**」+ 第 6 条「**保留每行 [说话人] 标记，
格式与原文一致**」，**合起来结构性禁止跨段合并**：合并必须删冗余字（第 3 条管），
且转写是每段一行、合并会改行结构（第 6 条管）。前后端两侧同款（`refine-prompt.ts` /
`server_meeting.go:1388-1395`）。
⚠ 且三条禁令**内部矛盾**：第 5 条允许纠正「能从上下文确定的错误」，跨段冗余正是这类。

⚠⚠ **不能据此断言是缺陷**：§35/§54 有真网关实测支撑这些禁令（「润色」会把**正确**句子
改写成 `CI`→`101`，而**误改用户发现不了**）⇒ 动它属产品口径且有反向风险，登记不代拍。

**⭐ 天花板读数（跨段损伤占 CER 多少）**

判据：逐段拼接 + 记录段边界 + difflib opcode 定位编辑，**边界窗口 ±3 字**。

| 臂 | CER | 错误数 | **边界内** | **占比** | 替换/漏字/多字（总→边界内） |
|---|---|---|---|---|---|
| SplitWAV \| sensevoice | 3.25% | 11 | 4 | 36.4% | 11→4 |
| **3s 硬切 \| sensevoice（随手记现状）** | **7.99%** | 27 | **22** | **81.5%** | 19→14 / 1→1 / 7→7 |
| **3s 硬切 \| GLM** | **24.56%** | 83 | **81** | **97.6%** | 11→9 / **71→71** / 1→1 |

⇒ ⭐ **随手记链现在 81.5% 的错误落在段边界 ±3 字内**；GLM 那臂 97.6% 且 **83 个里 71 个是「漏字」**
⇒ 形态：**同段音频被切两半，两片各自漏掉/重复出半个词**。GLM 是「丢字」，sensevoice 是「听错」，
**两种失效形状不同**。

**⭐⭐ 但结论是「先修切分」—— 两个方案攻击的是同一批错误，不可叠加**

| 方案 | 随手记链（27 错误 / 22 边界内）结果 |
|---|---|
| **甲 上静音感知切分**（`SplitWAV` 已实现并在跑） | **11 错误（边界内 4）**，CER 3.25% ⇒ 边界错误 **22→4（5.5×）** |
| 乙 做跨段合并（哪怕「完美」预言机） | 27−22 = 5 错误，CER 1.48% |

⚠ 上表看着乙更好，但**对照不成立**：① 两者攻击同一批 22 个边界错误，不是两笔可叠加的收益，
甲做完只剩 4 个；② 「完美合并」需要预言机（不可得），而甲要的能力**已经写好在跑**；
③ 现实中的合并必须**放松 §35 实测过的两条禁令**（有反向证据），
**用有实测风险的手段换一个不可达的理论最优，不划算**。
⇒ ⭐ 在**生产形态（SplitWAV）** 上，合并天花板只有 11−4 = 7 个错误 = **2.07pp**。

**一句话**：⭐⭐ **跨段合并不是一个独立的收益项，它是「切分没做对」的症状。先修切分（#35 甲案），
跨段损伤自己少 82%；那时再谈合并，才是谈剩下的 18%。**

**对 #15 B 格**：本节给 ②「补跨段合并」补上了它此前没有的**量级** ⇒ **只做 ①（已写好，改条件即可）**，
**不建议先做 ②**（它的收益上限取决于 ① 做完还剩 4 个边界伤）。

**三个选项**

| 选项 | 内容 |
|---|---|
| **甲（推荐）** | **暂不放宽那两条禁令，先修切分**。边界损伤 22→4 靠的是切分不是提示词；那两条禁令有实测反向证据 |
| 乙 | **只放宽第 6 条**（允许合并行、仍禁删内容）—— ⚠ **解决不了主症状**：重复识别必须删字才修得掉，删字是第 3 条管的 |
| 丙 | **先取读数再拍**：拿到网关凭据后做配对实验（同一批跨段损伤转写，现提示词 vs 放宽版）。⭐ **两个指标必须一起报**——修复率**与**「误改正确内容」，只报修复率会重蹈 §35 只看修复不看误改的覆辙 |

⛔ **未测（不得当已测）**：⭐ **精校 LLM 到底修没修这些跨段错误**（需网关凭据）
⇒ §546.2 的「被挡住」目前是**代码事实**（禁令存在）**不是读数**（禁令造成漏修），别把前者当后者 ·
真人会议录音上的边界损伤率（现语料停顿固定 1.5s，真实会议停顿分布会显著改变边界密度）·
**#42 字幕链与本节的交互**（字幕段的边界与 ASR 段完全不同，合并对象可能不一致，未查）。


### 🔴🔴 #48【§547 新增·阻塞 #15 B 格①】随手记兜底**缺了会议链早已写好的采纳守卫** —— 放宽门限前必须先拆的雷

§546 给出「先修切分」后，我去查那个结论的**可执行成本**，结果发现一颗雷。

**先说好消息：能力全在，只卡一个门限**
完整音频（`recordingRuntime.ts:1169` stop 时由 `this.chunks` 拼出）✅·
服务端 `transcribeFull` 自带静音切段（`full.go:426`，注释明写「服务端现在会自动按静音边界切段并聚合」）✅·
超长录音不被上游上限卡死 ✅· 有中止器 `transcribeAbort` ✅
⇒ ❌ 只卡在 `:1229` 的 `if (!this.transcript.value.trim() && audioBlob.size > 0)`
—— **只在一个字都没出时才跑**。

⚠ 注释自己写明这是**产品口径**（「已登记为 #15 的 B 格，不在本文件代拍」）⇒ **本轮不改它**。

**⭐ 雷本体：两条链在「采纳整段重转结果」上缺了同一个守卫**

| | 会议链 | 随手记兜底 |
|---|---|---|
| 采纳判据 | `shouldAdoptFullTranscript`：**空文本拒** + **变短一半以上拒** | ⭐ **无守卫**：`this.transcript.value = result.text` **无条件覆盖** |
| 失败/超时 | `catch` ⇒ `{applied:false, reason}` ⇒ **静默降级，沿用分段文本** | `catch` ⇒ `this.error.value='转写失败'` ⇒ **弹错误** |

⚠⚠⭐ **而且「有文本 + 挂「转写失败」」在这个文件里已经真实发生过一次**：
`recordingRuntime.ts:1091` 记着 2026-10-05 真机实测——「界面全程挂着「转写失败」，
而 text 同时在增长」（16 片里 8 片带 error）。**那次修的是增量路径**，
**兜底路径是同一种形状、没拿到同一种处理**。

⚠ 另有一维注释里没有：**`transcribeFull` 是严格串行**（`full.go:445` 无并发），
每段独立 90s，前端 `withTimeout` 上限 **10 分钟**。
而 `minSilenceCutSec=5` 意味着**短句会议只能切出 ~5~7s 的段**
⇒ ⚠ **§527/§544 语料的 5.35s/段是现实形态，不是悲观假设**。
⇒ 30 分录音 @2s/段 = **11.2 分 > 10 分钟上限**；60 分 @1s/段 = **同样超**。
⇒ **注释的成本模型只有「2x ASR」（钱），真正会先撞上的是「收尾等 10 分钟」（时间）**，
　且 `phase='stopping'` 期间录音按钮是锁的。
⛔ 0.39s/段是**本机 sensevoice 实测**、非云端读数 ⇒ 上表是**公式不是结论**（凭据阻塞）。

**今天是潜伏缺陷，不是现网缺陷** —— 兜底只在文本为空时跑，
守卫的 `segments-empty` 分支此时必然 `adopt:true` ⇒ **缺守卫今天完全无害**。
**一旦 #15 B格① 放宽门限，它立刻变成现网缺陷**：① 超时在好文本上弹「转写失败」；
② 段失败偏多 ⇒ 重转短一半 ⇒ 无守卫覆盖 ⇒ **用户内容凭空变少**。

**⭐ #15 B格① 的执行清单（三步缺一不可）**

| # | 步骤 | 落点 | 为什么必须在这步 |
|---|---|---|---|
| 1 | 给随手记兜底加 `shouldAdoptFullTranscript`（或等价纯函数） | `recordingRuntime.ts:1257` 前 | 不加则段失败时**覆盖掉好文本** |
| 2 | 兜底失败/超时时，若文本**非空**则走软提示而非 `error` | 同上 `catch` | 不改则长录音**在好文本上弹「转写失败」** |
| 3 | **才**放宽 `:1229` 门限 | `recordingRuntime.ts:1229` | 前两步是它的前置 |

⚠ 第 1、2 步的守卫逻辑**已存在且已被单测覆盖**（`meeting-final-transcript.ts`）⇒ 不用重写判据。
⚠ ⭐ **第 2 步同样是行为保持的**（今天文本为空才进分支，「非空则软提示」今天不可达）；
　**只有第 3 步是产品决定**。

**本轮刻意没落地那个守卫**（两条理由）：
① 门限未开时它是**不可达代码**，现在加就是给自己埋「守着一个未实现功能」的门
（§545 刚批评过 `silenceCut` 是同类，不能转头再犯）；② 改失败呈现会改变今天用户可见行为 ⇒ 属产品口径。

⛔ **未测**：云端单段延迟（凭据）· 真实会议停顿分布（5.35s/段是从 §121 的
1575–3475ms 停顿**推得**的，不是实测统计）· 长录音收尾真机端到端时长与用户感受 ·
若采纳第 3 步，**`#42` 字幕链（`origin:'manual'`）会与之交互**——字幕段也在 `this.segments` 里，
重转文本与字幕段的对应关系**未查**。




##### 🆕 §567【2026-10-13】⭐⭐⭐⭐⭐ 扫全仓 **226 处**「承诺型注释」，第一条就抓到**零强制的承诺**

把 §566 的判别动作（凡有「绝不/宁可/一定不会」就去问「哪一行保证它」）扫到全仓，
录音/会议/随手记/会话四条链共 **226 处**，逐条证伪不现实 ⇒ 抽第一条。

| 项 | 读数 |
|---|---|
| 承诺原文 | `recordingRuntime.ts:578`「合并是**增强**不是必需：失败就保持录音期间的原样，**绝不能让它把已经落库的分段转写搞丢**」 |
| ⭐ 保护范围 | try/catch **只包住 `relabel()` 一行**；而后面还有内存回贴 · `updateSegmentSpeaker` 落库 · ⭐ **`updateTranscript` 整篇重写转写正文** —— **全在保护之外** |
| ⭐ 调用点 | `stop():677` 是**裸 await**，**紧挨着** `:678` 的 `status: 'completed'`；`finally` 只重置 `stopping` + 播报，**不捕获** |
| ⇒ 真实失效 | ⭐⭐⭐ **一次增强失败 ⇒ 异常冒泡 ⇒ completed 永不写入 ⇒ 会议永远卡在「录音中」，而用户已听到「录音结束」播报** |
| ⛔ 零强制 | 25+ 条 `speaker-relabel` 用例 + 逐字实抄的 `cross-meeting-speaker-relabel` —— **没有一条覆盖那个 catch** |

**变异 M21（删掉那句承诺的唯一执行者）**：把 `:574-581` 的 catch **整段删掉**
⇒ **全量 45 项门禁依然全绿**（82.8s · exit=0）
⇒ ⇒ 删掉承诺的保护，**没有任何一条门禁会红**。

⚠ 严格说那句承诺的**字面表述仍成立**（段早在合并前就落库了），**坏掉的是它的意图** ——
　 「增强不是必需」被实现成了「增强失败会阻断主链路」。

✅ **已落地**：调用点补 try/catch（4 行 · **门数不变**）
　+ 判据「★ 收尾合并失败不得阻断 `status:completed`」
　⭐ 它钉的是**承诺本身**而非「有没有 catch」，并防住一种骗法：
　 **M22**（把 `updateMeeting` 整个挪进 `try`，语法合法）⇒ 精确变红
　 ⇒ 因为「只查有没有 catch」会被「顺手把整个 if 块都吞了」骗过，而那种写法收尾照样断。
　 门禁带修复 **45/45 通过**（83.9s）。

⛔ **不代拍**：「合并失败要不要提示用户」属产品口径（当前只落日志、不提示）。
⇒ 并入 **#48 / #46** 一族（「增强特性不得阻断主链路」），**不新增编号**。


### 🔴🔴 #49【§548 新增·升级 #40】会议链 VAD 的**底噪断点已实测钉死**：噪底一过 −38 dBFS，静音切分**彻底归零**

§536/§537 修好了「单段超 60s 丢音频」，但那条读数里留着一条**从没被测过的推论**：
`tick()` 判据是 `rms >= energyThreshold(0.012)` 判为语音
⇒ **只要底噪让静音 RMS 不再低于 0.012，`finalizeSegment` 就永远不被调用**。
而 §536 的语料静音是**数字零** ⇒ 这条路径从未被触发。

**实测（106.97s 语料加底噪，包络喂进生产 VadSegmenter）**

| 静音RMS | dBFS | 段数 | **切在停顿** | 均段长 | **告警** |
|---|---|---|---|---|---|
| 0.000000（干净） | −∞ | 15 | **14/15** | 7.33s | 0 |
| 0.010233 | −39.8 | 13 | 12/13 | 8.42s | 0 |
| **0.011495** | **−38.8** | **7（基线 47%）** | 6/7 | **15.43s** | 0 |
| **0.012894** | **−37.8** | **6（基线 40%）** | ⭐ **0/6** | **17.83s** | **0** |
| 0.016225 | −35.8 | 6 | ⭐ **0/6** | 17.83s | 0 |

⇒ ⭐⭐⭐ **RMS 一旦越过 0.012：6 段里 0 段切在停顿上，全部退化成 20 秒封顶硬切。**
　 ⇒ **§527 实测 6.3 倍的「静音感知切分」在噪底过线的录音上贡献为 0。**

**两个必须一起说的性质**

① ⭐ **是质量退化不是数据丢失**：各档音频携带覆盖率 **100~102.8%、零丢弃**
　 ⇒ **§537 的封顶修法在全部噪底下都成立**（它正是在 VAD 归零后兜住段长的那道）
　 ⚠ 但它只保证「不丢」，不保证「切得对」。
② ⭐⭐ **全程零告警**：15 段 → 0/6 段切在停顿，`console.warn` 一次都没触发
　 ⇒ **这个塌陷在日志里毫无痕迹**，你无法从日志判断某场会议的 VAD 有没有在工作。
　 ⚠ 与 §536 的丢音频不同——那次至少有告警，**这次一条都没有**。

**对 #40（底噪自适应）的直接影响：从「要不要做」变成「有没有不做得起」**

> **没有底噪适应时，任何底噪高于约 −38 dBFS 的录音，静音切分贡献为 0。**

⚠ 阈值离语音还很远（语音 RMS 恒 0.1379 ≈ 阈值 **11.5 倍**）
⇒ **问题不在「阈值太高」，而在「阈值是绝对的」**：它假定静音永远接近数字零，
　**对底噪没有任何适应能力**。
⭐ **技术事实部分**：把 `energyThreshold` 从绝对值改成「噪声底估计 + 相对门限」。
　形状已给出：噪声底 ≈ 静音段 RMS 的**滚动低分位**（本语料语音/静音比恒 11.5×，门限留余量即可）。
⚠ **门限取几倍、估计窗多长、切换要不要滞回 = 产品/算法口径 ⇒ 登记不代拍（#49）**。

**未测**：真实会议室底噪电平（需真机）⇒ **不能断言现网已越线**，但 −38 dBFS
**不是苛刻门槛**（HVAC/风扇/远端麦底噪都在其附近）⇒ **不是一个可以假定安全的值** ·
噪声频谱（本轮是**白噪声**，真实底噪偏粉/棕）· 噪底**是否随时间漂移**（本节是静态底噪）·
断点在真实语音上的位置（本语料语音/静音比恒为 11.5×）。

⚠️ **§548 方法论留证（可直接套用）**：① ⭐ **「一批读数几乎完全相同」就是量具坏了的第一信号**
（我第一版把 `volume` 加在了语音上而非噪声上，12 份读数全等 0.577，被读数自己抓住）；
② ⭐ **「实测行为翻转点落在配置值上」是量具自证的强形式**——同时证明「量具在动」与
「阈值确实是那个阈值」；③ ⭐⭐ **`onSegment` 不暴露 `forced` 时可从**输入包络**反推**——
判据不必寄生在输出上，要挂在**事件源**上。


### ✅ §549【已落地】#49 的可观测性缺口已补：**强制切分现在会发告警**

§548.3 的读数里那条「**零告警**」已经修掉（`frontend/src/native/vad-segmenter.ts`）。
**行为保持**：只多一条日志，音频/段数/段内容/用户可见输出一律不变。

**⚠ 但这次修复的过程本身是一条教训，值得单看**

第一版判据写的是「**VAD 未能在停顿处分段**」（断言底噪压住了判定），结果**被既有的测试直接打回**：

```
not ok 2 - 连续语音超过保留窗口时不再丢音频，且段长被 maxSpeechMs 封住
not ok 4 - 页面切后台（rAF 停摆）不足 60s：音频一片没丢
      AssertionError: expected 0, actual 1
```

⇒ 那两条是**真·连续讲话**形态（70s/90s/120s 不间断说话），**那里强制切分是正确行为**
—— 本来就没有停顿可切。⇒ 我把「行为正确」当成了「行为异常」。

⇒ ⭐⭐⭐ **真正的根因：两种成因在当前这一层的读数完全同形**

| 成因 | 本层能看到什么 |
|---|---|
| 底噪高于阈值 ⇒ 静音被判成语音 | 「连续 20s 没看到静音」 |
| 讲话确实连续 ⇒ 本就无停顿 | 「连续 20s 没看到静音」 |

判别需要「同期底噪估计」，**而那恰恰是这一层拿不到的**（它只看到自己的 RMS）。
第二版想用「RMS 离阈值多远」当分类器 —— ⭐ **那是症状，不是成因**，
**用一个启发式冒充诊断**，会在没测到的形态上给出自信的错误答案。

⇒ **最终形态：只报事实，不下诊断。** 告警里带上
**「此刻实测 RMS + 阈值」+ 两种可能的读法**，把判别权交回读日志的人。

⇒ ⭐⭐ **附带的另一条**：§538 那道门不只是钉住「不丢音频」，它顺带成了新判据的
**第一个反对者**。**一个门能「否证」后来的判据，比它能「通过」更有价值。**

**顺带把两条既有断言也收窄了**：那两条 `warnings.length === 0`，从**它们自己的说明文字**
看本意都是「不该报**丢音频**」，只是写成了「零告警」⇒ 改成按消息过滤
（`lossWarnings()`），**原意一分不丢**，判据反而更精确。

**门禁与自证**：`vad-segmenter.test.ts` 用例 **5 → 7**（接入既有 `test:native`，**门数仍 45**）。
三条变异**各自精确打红新加的那一条**、其余 6 条不受影响 ⇒ **判据有牙且不误伤**：
M1 整块移除告警 / M2 去掉「每场只报一次」守卫 / M3 去掉告警里的实测 RMS。
还原后 `sha256` 逐位一致。`vue-tsc` 干净，**`npm run gates` 45/45（85.9s）**。

⭐⭐⭐ **方法论（本节最值钱的一条）**：
**当两种成因在当前这一层的读数完全同形时，这一层就不能下诊断，只能报事实。
症状离阈值多远，不等于成因是什么。**
第一版犯的错是**在现象层下结论**；第二版犯的错更隐蔽，是**拿一个看起来相关的量
当分类器**。⇒ 可执行：**想下诊断前先问「我要用的判别量，这一层拿得到吗」**；
拿不到就**把量报出来、把判断交出去**，并在消息里写明两种可能。
⚠ **别因为「加了诊断显得更聪明」而下沉判别——诊断放错层比没有诊断更坏**，
它会把一个**诚实的未知**变成一个**自信的错误**。


### 🔴🔴 #49 更新【§550 实测】「把阈值调低」这条更便宜的路**已被排除**：失效面 9→19 组

§548 留了一个便宜问题：**不用自适应，把 `energyThreshold` 直接调低行不行？**
看起来很有理——§548 实测语音 RMS 恒为 0.1379，是阈值 0.012 的 **11.5 倍**，余量看着很大。
**本节把它测了：不能，而且它比现状更差。**

**三套方案、同一个真实类、同一批 35 组语料**（4 档讲话电平 × 5 档底噪 ⇒ SNR 跨 1.04~476.9）
判据 = **有效段** =（段数 / 干净语料段数）×（切在停顿比例）

| 方案 | **失效组数（有效段 < 50%）** |
|---|---|
| A 绝对 0.012（生产） | **9 / 35** |
| **B 绝对 0.004（调低）** | **19 / 35** ⭐ **比生产扩大一倍** |
| C 自适应 k=3 | 10 / 35 |

**它做坏的全是「正常语音 + 中等底噪」这种最常见的形态：**
`s0n-35`（底噪 0.0051、SNR 26.9）⇒ A **15段/14切 100%** · B **6段/0切 0%**；
`s0n-30`、`s-12n-35` 同样从 87%/146% 掉到 **0%**。
**它唯一赢的是「讲话极轻」那一角**（语音 0.0089 < 阈值 ⇒ 生产把讲话切碎）：
`s-24n-45` ⇒ A 12% · B 163% · C 181%。
⇒ **拿最常见形态大面积失效，换边角形态一条——不划算。**

⭐⭐ **为什么直觉是错的（这条最值得记住）**：
绝对阈值要工作必须**同时**满足 `底噪 < T < 语音`。
「语音是阈值的 11.5 倍，余量大 ⇒ 可以调低」**只核了语音侧，没核底噪侧**——
**调低 T 不是「离语音更远」，而是「离底噪更近」**：
`s0n-35` 的底噪 0.0051 > T=0.004 ⇒ **T 掉到底噪之下** ⇒ 静音永远被判成语音。
⇒ ⭐⭐ **「调低阈值」没有降低失效线，它把失效线从 −38 dBFS 移到了 −48 dBFS**，
**让方案在更常见的场景里先失效**。

⭐⭐⭐ **自适应是「底噪 > 阈值」那一格唯一还能工作的方案**（那一格正是 §548 判定的真风险）：

| 语料 | 底噪 | SNR | A | B | **C 自适应** |
|---|---|---|---|---|---|
| `s0n-25` | 0.0162 | 8.5 | 0% | 0% | **163%** |
| `s0n-20` | 0.0288 | 4.9 | 0% | 0% | **181%** |
| `s-12n-35` | 0.0051 | 6.8 | 146% | 0% | **181%** |

⚠ **但自适应不能裸做**，它自己有两条实测失效：
① **SNR ≤ ~2.2 时产出 0 段**（`s-24n-35` SNR 1.97 ⇒ 0 段）——⭐ **比 A 的 6 段更糟：一段都没有**
　 ⇒ 必须加**绝对地板** `max(k × 底噪, 下限)`；
② **极高 SNR 下反而略逊**（`s0n-60` SNR 477 ⇒ 43% vs A 的 100%）
　 ⇒ 因为本臂用的是**全局**底噪而语料内底噪随时间起伏 ⇒ 必须改**滚动估计**（+ 滞回）。

⇒ ⭐ **#49 的形状变了**：从「要不要做」变成「**方向已定（做），但缺三个参数**」——
　k 取多少 · 绝对地板取多少 · 滚动窗口多长（+ 滞回的进出差）。

⛔ **未测**：k 的取值（只测了 3）· 地板取值 · 滞回 · **真实会议电平是连续分布，
本节电平是人工扫的** · **C 在真机上的 CPU 开销**（滚动估计每秒要维护分位数）。
⚠ **C 臂性质要说准**：它是「全局底噪」的自适应，**不是生产可用的实现**；
它回答的是「这个**方向**有没有用」（有，且唯一），**没回答「参数取多少」**。


#### 🆕 §560【2026-10-13】⭐⭐⭐⭐⭐ **三个参数里的第二个已算成区间；而且冒出第三个组件**

| # | 参数 | §560 的实测答案 |
|---|---|---|
| ② | **绝对地板** | ⭐⭐⭐⭐ 必须落在 **`0.009138 < F < 0.012567`**，**宽 0.003429**。生产当前值 0.012 距上界只剩 **0.000567** ⇒ **任何 ≥0.0126 的地板会直接杀死 `s-24n-30`** |
| ① | **k** | ⭐⭐⭐ **k 单独不够用**：要覆盖 35 格需 `k < SNR`，而**最小 SNR = 1.0** ⇒ `k<1` 等于「比底噪还低」 ⇒ 纯 `k × 底噪` **在数学上不可能覆盖全部格子**（与上面「SNR ≤2.2 产出 0 段」是同一件事的两种说法） |
| 🆕 | **语音侧估计器** | ⭐⭐⭐⭐ **原先没列出的第三个组件**：正确形式必须是**双侧钳制** `T = clamp(k×底噪, F_abs, β×语音)` ⇒ **必须有语音电平估计器**，否则上界无从取 |
| ③ | **滚动窗口** | ⛔ 仍需真人连续录音 |

**判据（逐格算出来的）**：阈值必须夹在 `(底噪, 语音)` 开区间里。生产 0.012：
**25/35 格可用、10 格不可用**，且不可用分**两个方向相反的族**（窗 20ms，共 5348 帧）——
A 族 **8 格**（底噪 0.0162~0.0289 > 0.012）：`<0.012` 的帧 **0** ⇒ 静音不是静音；
B 族 **2 格**（语音 0.0089/0.0101 < 0.012）：`≥0.012` 的帧只有 **103/604** ⇒ 全是静音。

⇒ ⭐⭐⭐⭐ **「做自适应」这个方向的最强支撑**：每格区间宽度从 **0.001174 到 0.1376**，
　 **固定阈值在数学上不可能同时适配 35 格**；而把阈值放进 `(底噪, 语音)` **35 格全部可行**
　 （实测每格 `语音 > 底噪`，**无空区间**）。
⚠ **代价要说清**：区间最窄那格只有 **0.001174** 宽（`s-24n-20`）
　 ⇒ 估计器必须很准，**落哪一边都是失败**（落底噪侧 = 全是静音 · 落语音侧 = 全是语音）。


##### 🆕 §561【2026-10-13】⭐⭐⭐⭐⭐⭐ **补测了 §560 那个「从没被测过」的钳制版本 —— 失效 9/35 → 0/35**

§550 测的「自适应」是 `k × 全局底噪`、**没有上钳**；§560 从 SNR 定义推出必须钳制，
⇒ **带钳制这一版从未被测过**，而它才是 #49 成立与否的关键。**本节测了。**

| 臂 | 段数均 | 切中率均 | **失效格** | 最差格 |
|---|---|---|---|---|
| **A 固定 0.012（生产现状）** | 14.7 | 13.3 | **9 / 35** | `s-24n-25` 0% |
| **B 预言机·区间中点（t'=0.5，不钳制）** | 19.7 | 19.7 | **1 / 35** | `s-24n-20` 30% |
| **C 预言机·钳制 k=3 β=0.7** | 19.9 | 19.9 | **0 / 35** | — |
| ⭐ **D 滚动 p10/p90，W=200 帧（2s）** | **20.0** | **20.0** | **0 / 35** | — |
| D 滚动 W=400（4s）/ W=1000（10s） | 20.0 | 20.0 | 0 / 35 | **与 2s 逐位相同** |

⚠ **量具自证**：A 臂**精确复现 §550 的 9/35**。（第一版我把包络按 5ms 索引而它实际是
　 100ms 一格 ⇒ 只跑了一半音频、A 臂报 35/35；**是对照臂抓到的**。）

⇒ ⭐⭐⭐⭐ **三条结论**：
① **在失效格上差别是决定性的**：`s-12n-20` / `s-18n-20` 上 A 给 **6 段 / 0 切中**（连续讲话只能靠 20s 封顶），
　 **D 给 20 段 / 20 切中** ⇒ 每一条边界都落在停顿上。
② ⭐⭐⭐⭐ **预言机不是必需的** —— 纯局部的滚动 p10/p90 **追平了用真值的预言机**。
　 ⇒ §560 说的「第三个组件（语音侧估计器）」**不需要另一套机制**，底噪与语音
　 **同一个窗口的两个分位数**就够了。
③ ⭐⭐⭐⭐ **第三个参数（滚动窗口）在 2s~10s 内完全不敏感** ⇒ **取最便宜的 2s**。

⭐⭐⭐ **顺带解掉了 §560 那个 0.0034 宽的原始 RMS 窗口约束**：归一化后阈值是无量纲分数 `t'`，
　 而 `t'` 必须落在 `(0,1)`（B 臂纯中点 1/35 失效 ⇒ **纯中点不够，钳制必要**）。

⚠ **代价与限制（必须一起记）**：健康音频上段数 **+5%**（19→20 段，均长 7.1s→5.35s）；
　 ⚠ **§546 说段越短边界损伤越高**（3s 硬切 SV 22/27=81.5% · GLM 81/83=97.6%）
　 ⇒ **这段长度是否真伤 CER ⛔ 未测（需真 ASR）**；
　 ⚠ CPU 开销未测（每 100ms 维护窗口分位数）；
　 ⚠ 滚动估计的固有假设是「窗口内同时含语音与静音」，**持续讲话段里会退化**——
　 　 实测在 A 族格上仍成立（20/20）**是噪声起伏兜住的，机制上不保证**；
　 ⚠ 语料仍是 TTS 合成。

⇒ 🔴 **#49 现在不是「缺三个参数」了**：方向已被实测确认（9/35→0/35），
　 三个参数**全部不敏感或已消解**（钳制必需、取 `t'`≈0.7、窗口取 2s），
　 **剩下的唯一未知是 CER 那一侧** —— ⛔ 缺凭据。


##### 🆕 §562【2026-10-13】⛔⭐⭐⭐⭐ 回查 §561 的「取 2s」：**标签错 10 倍，而 2s 恰好在一道悬崖上**

§561 写下的参数表有一处**标签与量具对不上**：`clamp.ts:54` 是 `STEP_MS = 100`（100ms/格），
而 `:112-114` 把 `W=200/400/1000 帧` 标成 **「2s / 4s / 10s」**。

⇒ **200 帧 × 100ms = 20s。** 实际测过的是 **20s / 40s / 100s**，**2s 从未被测过**。
⇒ 而滚动估计的机制假设是「窗内同时含语音与静音」，**窗口越大越容易满足**
　 ⇒ §561 测过的窗口**全都偏大**，而推荐值恰好落在假设**最难满足**的一侧。

**⚠ 量具关**：A 臂（固定 0.012 = 生产现状）先逐格复现 §561 登记读数 —— **35/35 一致**（失效 9/35、均值 114.0%），
　不过则脚本直接退出。**2s 那张表就是这么量出来的：**

| W | 段数均 | 有效段均 | **失效格** |
|---|---|---|---|
| 0.5s | 6.1 | 18% | **35/35** |
| 1s | 8.3 | 34% | **34/35** |
| ⭐ **2s** | 19.9 | **189%** | **0/35** |
| 3s / 5s / 10s / 20s | 20.0 | 190% | 0/35 |

⇒ ⭐⭐⭐ **2s 不是「2~10s 都不敏感」这个平台上的一点，它是悬崖边缘 —— 1s 与 2s 之间没有过渡带。**
　 机制：W ≤ 1s ⇒ `p10` 被语音抬高 ⇒ 阈值高于语音 ⇒ **整条音频判为连续语音**
　 ⇒ 只能靠 `maxSpeechMs=20000` 硬切 ⇒ **全语料统一 6 段** ⇒ VAD 整个失效、退化成定时器。

**⛔ 更正 §561 的三条：**

| §561 登记 | §562 实测 |
|---|---|
| D 失效 **0/35** | ⛔ **生产分辨率下是 1/35**（0/35 是 100ms 分辨率的产物） |
| 「窗口 2s~10s 不敏感」 | 真实区间是 **20~100s** |
| 持续讲话退化 = 滚动估计的风险 | ⛔ 归属记错：退化归 **`maxSpeechMs=20000` 封顶**，D 无新增失效 |

⇒ ⭐⭐⭐ **复核包络分辨率后的结论**（生产是 `vad-segmenter.ts:93` `fftSize=512` ⇒ **16ms**，
　比 §550 的 100ms 细 6 倍；悬崖位置对这个分辨率敏感，必须复测）：
　 **悬崖没有左移**（1s 失效 35/35、2s 失效 1/35）⇒ **「2s 是最小可用值」对分辨率不敏感**，
　 这是本节最硬的一条（两个独立量具同一结论）。
　 但残留的 **1 格 = `s-24n-20`**：底噪 0.028882 / 语音 0.03006 ⇒ **SNR 1.04**、
　 **语音中位数只比底噪高 4%**、带宽 **0.001178**（就是 §560 记的区间宽度下界）
　 ⇒ 全网格 SNR 最小值，**包络阈值的物理硬底，调窗口/调分位数都救不了**，
　 要救它得换**特征**（谱特征/MFCC）或换麦克风增益/降噪。

**⛔ 顺带抓到本节自己的一个无效测试**：为验证「持续讲话会退化」而构造的
「无停顿形态」（删掉源静音区 300/1069 格、保留 72%、压到 76.9s），
**根本没有打破机制假设** —— 纯语音窗占比只从 **0.07% → 0.10%（放大 1×）**。
⇒ ⛔ 它对那条结论**不构成证据**。
⇒ 而 §562.3 量到：**纯语音窗在本语料里只有 26/37415 帧（0.07%）**，
　 且与预言机判决 **0/26 完全反转**（真反转，不是小分母噪声）。
⇒ ⭐⭐⭐ 所以 §561 那句「实测成立是靠噪声起伏兜住的，机制上不保证」要再往前推一步：
　 **不是机制不保证，是这份语料连机制的失效态都构造不出来。**
　 「语料缺什么结构，机制就对什么失明」第 5 例：**缺「窗内电平平坦的连续讲话」**。

⇒ 🔴 **#49 的参数表按上表改写**，并**必须写上「窗口没有安全裕度」** ——
　 它在 §561 里是被「不敏感」三个字盖住的。
⇒ ⛔ **CER 那一侧仍未测**（需真 ASR + 网关凭据），「均长 5.35s 是否伤 CER」依旧悬空。

### 🔴 #50【§551 新增·修正 #46 的方向】随手记链上的去重阶段：**没有正收益，只有误删风险**

§547 顺手记下的待查项「`IncrementalOverlapSec` 为何零使用点」，顺着问出一件更本质的事。

**问题的形状**：`mergeIncremental` 的存在理由写在 `incremental.go:19-21`——
「切片有 1 秒重叠，ASR 对重叠区的两次识别基本一致，直接拼接会得到『今天今天下午三点…』」。
而随手记送的是 `takeNewWindow()` 的**纯新增窗口、彼此不重叠**。
⇒ ⭐⭐ **对不重叠输入，去重的每次触发都不是「消掉重复」，而是「编辑用户的话」**，
而该函数自己的注释写着判据偏向：「**宁高不低……误判的代价是丢掉整句真实内容（用户永远发现不了）**」。

**⚠ 测量过程本身比结论更值得看（三次同族假阴性）**

| 轮次 | 口径 | 读数 |
|---|---|---|
| ① | §544 四个臂的真实逐段输出（共 **104 段**）逐段喂真函数 | 触发去重 **0 次** |
| ② | **穷举**：每句话在每个内部位置切开（= 随手记硬切做的事），**306 个切点** | 误判 **0 次** |
| ③ | ⭐ **对照组**（阴性结论必须自证探针有牙） | ⭐⭐ **当场抓到真误判，净删 7 字** |

⇒ 前两轮是**假阴性**，根因与 §536（静音是数字零）、§544（无噪声无重叠）**完全同族**：
**语料里 20 句互不重复 ⇒ 结构上不可能产生「移位重复」形态。**
⇒ ⭐⭐ **「语料缺什么结构，机制就对什么失明」——这是我记录的第三次同例。**

**机制与量化**（全部来自真函数 `mergeIncremental`）

| 案例 | 净删 | 性质 |
|---|---|---|
| **注释原案例**「…预算和排期，还有人」+「排期还有人员安排…」 | **0 字** | ✅ 当年的 bug 已被现阈值挡住 |
| 「今天下午三点我们开」+「下午三点我们开项目」 | **7 字** | ⭐ **误删真实内容**（本轮最大） |
| 「我们先看一下这个排期」+「排期还有人员安排」 | 2 字 | 误删 |
| 「会议纪要要同步到知识库」+「知识库里要补上决议」 | 3 字 | 误删 |
| 「好的好的」+「好的我们先记录」 | 2 字 | ⭐ **这一例去重是对的** |

⇒ ⭐⭐⭐ **「删错的」与「删对的」在输出上完全同形**（都是「输出比拼接短」），
**读输出分不出哪个是误删**。

⭐⭐ **而这个误判代码注释里早就写着，连同它的下场**（`incremental.go:369` 附近）：
「2026-10-06 真 ASR 实测发现……改法**它同时吃掉了真实内容**……
**真重叠与「换句式但用词相同」在结构上不可区分**……
**真正能分开它们的信号不在文本里，而在时间**（`chunk.StartSec/EndSec` +「语速×重叠秒数」）」。
⇒ 本节补的是**量化与边界**，机制本身是「已知、修过、又撤回」的。

**⭐ 与 #46 的关系：方向被修正了**

| 把 `SilenceCut=false` 实现成 | 对随手记（不重叠）意味着 |
|---|---|
| 「更保守」（§545 注释原意） | 把阈值**调高** ⇒ 真重复也去不掉，**误删照旧** |
| ⭐ **「不去重」**（本节读数指向的） | **每次触发都可能是误删** ⇒ 对不重叠输入，这是唯一能根除误删的形状 |

⇒ ⭐⭐⭐ **去重的存在理由是「切片有重叠」。没有重叠，去重就没有正收益可保护。**
⚠ **属产品口径**（用户真说了两遍同样的词时，保不保留？）⇒ 登记 #50，不代拍。

**三个选项**

| 选项 | 内容 |
|---|---|
| **甲（推荐）** | **`SilenceCut=false` 时不做跨段去重**。依据：调用方已声明不重叠，去重只剩误删可能；且它自己的注释说宁高不低。⚠ 需先确认 #42（字幕链）——两套不同引擎的输出混进同一个累加器，误判率可能更高 |
| 乙 | 收紧模糊匹配阈值 | ⚠ **注释已记录这条路走过并撤回**（修好一个、吃掉另一个），再走一遍大概率是过拟合 |
| 丙 | 用注释建议的**时间信号**（`StartSec/EndSec` + 语速）限制 limit 上界 | ⭐ 方向对，但**语速没有可靠来源**，本节未验其可行性 ⇒ 需先造语料 |

⛔ **未测**：**真实会议上的触发率**（104 段真实输出上 0 次触发，而合成移位重复能删 7 字
⇒ **机制已确认存在，真实发生率未测**）· 注释建议的**时间信号方案可行性** ·
**与 #42 字幕链的交互**（两套引擎的输出之间做模糊匹配，误判率可能高于同引擎相邻段）。


### 🔴🔴 #51【§563 新增】「参考资料与建议」在**默认部署形态**下被静默替换成模型现编的条目

§563 端到端核 `/recommend` 整条链，核出一处**三态挤进两值 flag**的缺陷。

| # | 真实状态 | `items` | `fallback` |
|---|---|---|---|
| ① | kxmemory 检索完（**含「库里确实没有」**） | 真实条目或 `[]` | `false` |
| ② | ⭐ **kxmemory 压根没参与**，LLM 正常作答 | **模型现编的 `web` 条目** | **`false`** |
| ③ | LLM 输出无法解析 / 调用失败 | `[]` | `true` |

⇒ ⭐⭐⭐ **根因**：`server_meeting.go:669` 的 `fellBack` 来自 `parseRecommendJSONWithFallback`，
　 而它**只在解析失败时返回 true**，解析成功一律 false
　 ⇒ 这个键实际语义是「**模型有没有好好说话**」，**不是**「有没有走主源」。
⇒ ⭐⭐⭐ **而 `:615` 的注释明确要求它是后者**：「键在这里必须出现，否则前端分不出
　『主源说没有』与『压根没走到主源』」⇒ **注释描述的是一个从未实现的契约。**

**⭐ 状态 ② 不是边缘情形，是默认形态**
（`deploy/bin/deploy-local.sh:92` 是 `POCKET_KXMEMORY_BASE_URL=` 空值 ·
`main.go:411` 只在非空时构造 client · 两个 email 测试注释都写着「**生产上就是未设**」）

⇒ 前端两处诚实提示语（`MeetingInsightPanel.vue:114` · `LiveSummaryPanel.vue:43`）
　 都以 `recommendFallback` 为前提 ⇒ **默认形态下一句都不会说**。
⇒ 用户看到的是小标题 **「相关 · 笔记 / 知识库 / 网络」** 下面一列模型现编、
　 url 指向 **Bing 搜索结果页**（从没联网）的条目
　 ⇒ ⭐ **该形态下「笔记」「知识库」两种来源结构上不可能出现**（② 只产出 `type:"web"`）。
⚠ **有一处做对了**：条目行只渲染 `title` 与类型标签，**没有渲染模型生成的 `snippet`** ⇒ 危害面小了一半。

⇒ ⭐⭐⭐ **§223 不是没修，是修在了另一根轴**：
　 它把信号建在「解析失败」（罕见、且失败后本来就没内容可说）上，
　 缺的是「有没有走主源」（默认形态、每次都中招、**且有内容可看**）。
　 只按 §223 的读数结案 ⇒ 会得到「这条链已修好」的错觉，而它在默认形态下从未修好过。

✅ **已落地（未改行为）**：特征化判据
　 `TestMeetingRecommend_FallbackFlagIsNotAPathSignal`（两条变异各精确红 1 ·
　 生产文件 sha256 逐位复原 · ⛔ 第一版变异编译失败，按纪律记为无效变异）
　 + 四处如实注释（后端 2 · 前端 2）。

**三个选项**

| 选项 | 内容 |
|---|---|
| ⭐ **甲（推荐）** | **加 `source: kxmemory\|llm` 路径信号**，并在条目区标注来源。依据：它是**唯一**能让状态 ② 变可见的改法；不改则默认形态永远无法说出一句话 |
| 乙 | 只改小标题（把「网络」换成中性词，如「延伸建议」）⇒ 成本最低，但**用户仍不知道这不是检索结果** |
| 丙 | 不改。承认默认形态下这一块就是「模型给的灵感线索」，与「参考资料」是两回事 | 

⚠ **若做，必须三处一起改**：两处提示语条件 + `server_meeting_recommend_flag_test.go`
　 现有那条判据（它把 `fallback` 钉成解析信号）⇒ 否则会出现两个语义不同的 flag。
⛔ **与 #43（联网检索）的关系**：状态 ② 的条目本质是「**模型记忆 + 搜索链接**」。
　 **若 #43 决定做真联网检索，② 这条路应当被替换掉而不是打补丁** —— 两个决策最好一起拍。

### 🔴 #52【§573 新增】ASR 换型：**先问接入形态与计费口径，再问单价**

**现状读数**（已核）：`backend/internal/stt/provider_openai.go`（OpenAI 兼容
`/audio/transcriptions`，**multipart 直传、同步取文本**）与 `provider_zhipu.go` —— 本仓只有这两种接法。

**厂商一手计费页读数**（2026-10-14 核）：

| 方案 | 单价 | ⭐ 计费口径 |
|---|---|---|
| 阿里云百炼 **Paraformer 语音识别** `paraformer-1` | **0.00008 元/秒 = 0.288 元/小时** | ⭐⭐⭐ **只对判定为语音内容的时长计费，静音不计费**；不限量无梯度；每月 10 小时免费额度 |
| 阿里云 ISI 录音文件识别（标准版） | 2.50 元/小时 | 按**音频时长** |
| 火山豆包**录音文件识别模型 2.0** | 0.8 元/小时（资源包 0.67） | — |
| 火山豆包流式语音识别模型 2.0 | 1.0 元/小时 | — |

⚠ **但 Paraformer 那条地板价在本仓接不上**：它是**异步文件转写**
（提交 `file_urls` → 轮询 `wait`/`fetch` → 结果 URL **24 小时**过期），要求**公网可访问的 URL**；
而本仓唯一能传音频的入口是**同步直传**。

⇒ ⭐ **同一个厂商里，接得上的是 2.50 元/小时那档，接不上的才是 0.288。**

**三个选项**

| 选项 | 内容 |
|---|---|
| ⭐ **甲（推荐）** | **先不换。** ⚠⭐⭐ **§579 把推荐从「稳妥」升级成「划算」**：推荐理由不只是「接不上」，还是**接得上也没更便宜** —— 用价表内含的隐含汇率（`glm-asr-2512` 备注 `0.06 元/分钟 ≈ 3.6 元/小时` ↔ 标价 `$0.50/h` ⇒ **7.2**）换算后：**能接上的中文专用 ASR（GLM ¥3.6/h）比仓库在册最便宜的 `qwen3-asr-0.6b`（$0.012 ≈ ¥0.086/h）贵约 42 倍**；而 §573 那个真正便宜的 Paraformer（¥0.288/h）**恰好落在接不上的那一类**。⇒ **中国云不是价格洼地，它只是「不在本仓的可达集里」** |
| 乙 | 新增「上传 OSS → 异步任务 → 轮询 → 拉结果」provider，把 Paraformer 接进来 | 工程量非零；收益是单价 2.50 → 0.288 **且静音不计费** |
| 丙 | 就此不再评估 ASR 价格 | ⚠ 失去发现更便宜/更好方案的能力 |

⭐⭐⭐ **本条真正的产出不是「换不换」，是那个此前完全没进过决策的维度 —— 计费口径。**
　同一段会议录音：按音频时长计 vs 按语音内容时长计，
　若语音占比 50%，实际每场成本差约 **17 倍**，而不是单价之比的 8.7 倍。
　⛔ **语音占比 50% 是假设不是实测** —— 但这件事**本机就能测**：
　百炼每次返回带 `content_duration` 与 `original_duration`，
　拿 §555 的 35 条 wav 跑一遍即可 ⇒ 比值就是本仓语料的实测语音占比。
　⛔ 仍卡在**网关/百炼凭据**（与 #49、§556 同一个 blocker）。

⭐ 与 **#14** 同源且互相印证：#14 从「延迟/成本」侧得出「**不是客户端选型，是网关侧要先加种子**」，
　§573 从「价格/形态」侧独立得出同一形状的结论 ⇒ 两条不同路径指向同一处，权重更高。
⛔ 与 **#45**（本地 ASR 不立项）不冲突：#45 否的是**本地自建**，本条问的是**云端换型**。

⚠ **不要重开 #45（§579）**：`qwen3-asr-0.6b` 只要 **$0.012/小时**（≈¥0.086/h），
　本地自建相对这个价的优势**近零**（#45 的理由：无优势 + 更脆 + 慢 6.5×，仍然成立）。
　⛔ 常见的一个**跳两级**推论：「找到 ¥0.288 的地板价 ⇒ 那不如自建」—— **前半句是云上价，
　且它接不上；后面对标的自建是 $0.012，不是 ¥0.288。**

⛔ **另有一件范围口径待拍（§579）**：仓库价表 `internal/stt/target.go` 只有 **9 条 external +
5 条 gateway** 的美元价，**没有一条中国云厂商**。
　§573 的数字只覆盖四家中国厂商 ⇒ **该不该把其中某几条补进价表**（成本看板据此展示）
　属**范围口径**，登记**不代拍**，与本项一起拍。
　⛔⛔ **勘误（§580.5）**：上面「5 条 gateway 的美元价」**不成立** ——
　`grep -c "USDPerHour:" target.go` = **9**，全在 external 组；
　**gateway 组那 5 条根本没有价格字段**。
　⚠ 这**不是缺陷**（`KnownUSDPerHour` 的注释 `:433-434` 写着「未知就是未知：
　报价缺失时宁可显示「未知」，也不要让成本看板悄悄归零」⇒ 那 5 条返回 0 是**符合意图**），
　但照原句去数会以为网关档有价。原文不改，指针在此。

⛔ **再有一件范围口径待拍（§580.6，新增）**：**Go 侧真验证不在门禁链上。**
　实测：`npm run gates` 的 45 项里**没有 `go test`**，
　`frontend/package.json` 里也**没有任何**跑 `go test` / `go vet` 的 script
　⇒ 仓里仅有的两道价格门（`TestKnownUSDPerHour` 只钉 1 条数值 + 「未知→0」；
　`…SortedByCost` 只钉升序）**只有人手动敲 `go test ./...` 才会碰到**。
　⇒ ⭐ **实测零强制**：把 `glm-asr-2512` 的 `$0.50` 改成 `$0.45`（**仍最大、仍升序**）
　⇒ `go test ./...` **EXIT=0**、`npm run test:stt` **111/111 全绿**。
　⭐ §580 已在**前端侧**补了一道钉住价表的门（9 条 golden + 隐含汇率带 + 增删可见），
　**但它不能替代 Go 侧** ⇒ 「要不要把 Go 测试接进门禁链」属**工具链范围口径**，
　影响面（CI 时长、缓存、跨平台）远超一节 ⇒ 登记**不代拍**，与本项一起拍。

### 🔴 #53【§576 新增】纪要条目要不要能**溯源**回原文段落 —— 讯飞与飞书都拿它当招牌

**对标一手来源**（不是二手转述）：
- 讯飞听见官方专栏：「自适应纪要」的三大特性之一是**可溯源** ——
  「AI 纪要生成内容**都有依据可以追溯**，点击纪要内容的图标，
  能够快速定位到对应的**录音原文**、图片、笔记」
  （`iflyrec.com/zhuanxie/691d5def.html`）
- 飞书妙记官网：**音字同步定位** ——「点击文字即可跳转对应音视频片段」
  （`feishu.cn/product/minutes`）

⇒ 两家**独立地**把「从纪要跳回原文位置」放在最显眼的位置。

**本仓实测**（三个互相独立的口径，同一结论）：

| 口径 | 读数 |
|---|---|
| ① grep `sourceSegment`/`segmentRef`/`evidenceRef`/`anchorSegment`/`sourceStartMs` | **零命中** |
| ② 前端 `LiveSummary` / `ActionItem` | `ActionItem` 只有 `text` / `assignee` / `due` |
| ③ 后端 `summaryJSONSchema` | `action_items":[{"text","assignee","due"}]` |

而 `MeetingSegment` **有 `startMs` / `endMs`** ⇒ ⭐⭐⭐ **坐标齐了，指针一个都没有。**

**三个选项**

| 选项 | 内容 |
|---|---|
| ⭐ **甲（推荐）** | **先结构、后交互**：给 `ActionItem` / `keyPoints` 加**可选**锚点（段号或时间区间）并让 LLM 回填，然后才做点击跳转。⭐ **只做交互而不加锚点是做不出来的**；只加锚点不做交互则是零成本、可撤销的投资 |
| ⭐⭐ **甲′（§577 新增，可能更便宜）** | **纯本地事后对齐**，**完全不动提示词契约**：用摘要条目与各段做文本重叠匹配，反推锚点。⭐ **可行性已核**：本仓已有现成机器（`meeting-dedup.ts` 的 `ANCHOR_COVERAGE=0.6` / `MIN_ANCHOR=4` 的 LCS + 回溯），可复用。⭐⭐ **§578 又给它加了一条代码里可读的定界**：摘要契约三条全是**「摘录 / 保留原话」**口径（「只根据转写…禁止编造」「action_items 只放转写里真实提到」「due 保留用户原话里的时间表述」），**没有一条要求改写** ⇒ 逐字对齐是**被契约鼓励**的行为。⛔ 但契约不是保证：§65 实测 `glm-5.2` 填出过「那之前」 |
| 乙 | 只做交互（点击关键词跳到最近的段）⇒ ⛔ **锚点不存在时只能做模糊跳转**，与 §575 的教训同源：用「看起来能用」掩盖「结构上没做」 |
| 丙 | 不做。保留纯文本纪要 |

⚠ **甲 vs 甲′ 怎么选**：⚠ **甲′ 的准确率绝对值本轮测不了**，原因已核：
　`docs/design/data/` 那份收据是 **CER 口径**，只存每次调用的元数据
　（`model` / `arm` / `time_total` / `degraded`），**没有配对的「摘要 + 转写」**。
　⛔ 也不打算用**测试夹具**当 LLM 真实产出 —— 仓里那批 `due` 语料（「下周三下午三点」
　「十一月底之前」等 12 类）**是前几轮手写的**，只有「那之前」一个是 §65 的真网关实测反例
　⇒ 用它算遵守率 = **用自己造的样本测自己**。

⭐⭐⭐ **但问法可以更准（§578）**：那个「测不出来的数」不必当一个总数 ——
　**逐字率必然按字段分层，而方向由契约给出**：

| 字段 | 契约约束 | 逐字率期望 | 甲′ 先做？ |
|---|---|---|---|
| `action_items[].due` | ⭐⭐⭐ 最硬（「保留用户原话」还给了两个示例） | 最高 | ⭐ 是 |
| `action_items[].text` | 强（「只放转写里真实提到的事」） | 高 | ⭐ 是 |
| `key_points` / `decisions` / `open_questions` | 弱（无专门约束） | 中低 | 次之 |
| `summary` | ⭐ 最弱（它**就是**概括性字段） | 最低 | ⚠ 不要先做 |

⇒ ⭐ **两条路都有明确的落点顺序：`due` → `action_items.text` → 其余**，不要从 `summary` 起步。
⇒ ⭐⭐ **这条顺序反过来让「甲」也变便宜了**：**先给 `due` 和 `action_items.text` 加锚点**
　（每场就 3–8 条），其余字段留空 ⇒ **不必一次改全 schema**。

⚠ **代价要一起看**：走甲（让 LLM 回填段号）就得改摘要提示词的 JSON 契约，
　而**逐字锚定**正是 **#47** 拍过「暂不放宽、先修切分」的那一类约束
　（同族取舍见 `refine-prompt-parity.test.mjs` 的「due 必须能脱离上下文独立成立」）。
⇒ ⭐ **甲 与 #47 一起拍；甲′ 不受 #47 约束**（它不改提示词），
　这是两条路最实质的差别 —— 而 §576 只写了甲，**漏了甲′ 这条更便宜的**。

⭐ 顺带补两条**同类**读数（都已在别处登记，这里只补对标口径）：
· **章节化分段**（飞书「AI 智能切分章节」· 讯飞「议题智能速览」）：
　本仓 `topicShift` **只当 `useLiveSummary` 的刷新时机**，**无任何可见落点** ⇒ 有机制、无产品面；
· **说话人声纹记忆**（讯飞「一次录入、跨会议自动识别」）：能力在位但**零 UI 入口**（=#38）。

### 🔴 #54【§589 新增】真机校准 `SAME_SPEAKER_MIN`：**2026-07-07 那份文档自列「第一优先」，至今未做**

`docs/design/2026-10-07-speaker-diarization.md:549` 逐字：
「**真机校准 `SAME_SPEAKER_MIN = 0.99`｜仍未做，且仍是第一优先**」。
⛔ 这件事**不在本页任何一条待拍板项里**，直到 §589 才被登记进来。

### §589.6 为什么它比文档自己写的更紧迫

⛔ **先撤回一条我自己准备写的推断**：我以为「阈值是用谱向量标的、真机跑的是 ECAPA
⇒ 特征空间失配」。**核完前提不成立**：

| | 证据 |
|---|---|
| 标定用的哪一路特征 | 夹具 `_provenance.feature` 逐字：「speaker-features.ts 的 **logMelEmbedding（48 维）**」；实测两组各 27 键、维度集合 `{48}` |
| 真机跑的哪一路 | 原生插件恒 reject ⇒ `speaker-embedding.ts:45` 的 `catch` 落到 `logMelEmbedding` |

⇒ **同一路、同一维度 ⇒ 阈值没有与特征脱钩。**

⇒ ⭐⭐⭐ **真正缺标定依据的是「音频来源」**：夹具是 **MiniMax TTS 干声**（9 音色 × 3 句，
`pipeline` 只有一步 ffmpeg 转码、**无加噪步骤**），而仓库里**已经有**一个同方向的读数 ——
`__tests__/synth-voice.ts:84-86` 逐字：「实测同一人在 **0.001 幅度底噪**下相似度就掉到
**0.9815**，**已跌破 0.99 阈值**」。

⇒ ⚠ **推论（未核，不是实测）**：真机会议室有真实底噪 ⇒「同人被拆开」的真实比例应当
**远高于**夹具的 7.4%（长段）/ 66.7%（短段）⇒ §587 实测的标签膨胀 **2.31×** 会更重。

⚠ 边界：那个 0.9815 是**合成语音 + 合成白噪**，不能直接当结论 ⇒ **必须实测**。

### ⭐ 与 #49 是**同一次采集**

两者都要「真机多人会议录音 + 底噪/电平分布」，而**不需要产品口径，只需要一次采集**：

| | 要什么 | 采完能定什么 |
|---|---|---|
| **#49** | 真实会议室底噪与电平分布 | VAD 断点的门限倍数、估计窗长度、要不要滞回 |
| **#54** | 同一人/异人在**真实音频**上的相似度分布 | `SAME_SPEAKER_MIN` 该取多少、要不要改成按已知参会人数强制分配 |

⇒ ⭐⭐⭐ **⇒ 建议与 #49 合并成一次真机采集任务，而不是排成两次。**
⚠ **不重开 #45**（本地 ASR 不立项）—— #54 与它无关：#54 问的是**阈值取多少**，
不是**要不要换 ASR**。

### ⭐ 顺带登记两件 §589 的范围口径（不代拍）

1. **标定注释里那五组分位数要不要写成断言**（§589.6）：
   注释称长片段「同人 min 0.9850 / 异人 max 0.9901」，而**夹具当前实测是 0.9865 / 0.9943**
   （设计文档 `2026-10-07-speaker-diarization.md:441-442` 与实测**逐位吻合**）。
   ⛔ 现在门只钉**率**（误并 ≤2%、拆分 ≤15%、短段拆分 >50%），**一组分位数都没钉**
   ⇒ ⭐ **这是 §574「有几条从来没被写成断言」在数值上的同型复发**。
   ⚠ 代价要说清：**加了断言，夹具一换版本就会红**（而换版本本身是正常的）⇒ 属范围口径。
2. **`ai-shell` 这个实测入口已不存在**：`2026-10-07` 文档与
   `speaker-calibration.test.ts:36` 都写「真机阈值仍需用 `ai-shell` 录音实测复核」，
   而本机 `~/.minimax/skills/` 下**无 `ai-shell`** ⇒ 那条指引本身是条死链（已随 §589 标注）。

### ✅ §589 已自行落地的部分（不需要拍板）

- ⛔ **撤回一条「新缺口」指控**：`Sherpa` 恒 reject 这件事**不是新发现**——
  `capabilities.ts` 的 `NOT_IMPLEMENTED_PLUGINS` 已登记，且有两道门守着
  （`capabilities-stub-gate` + §415 的 `stt-local-first-claim-gate`）。
- ⭐⭐⭐⭐⭐ **真缺口**：`speaker-diarization.ts` 一度写着「原生 cap-sherpa ECAPA **已具备**」，
  与能力表**直接矛盾**，而门全绿 —— 因为那道诚实性门的匹配面**只有 `api/stt.ts` 的文件头**。
  ✅ 已修注释 + 已把「同一类主张」扩进那道门（**门数仍 45**），真变异验证 RED → 复原一致。
- ✅ 两处指向**已删除文档**的死链（`sherpa.ts:6`、`SherpaPlugin.java:13`）已标注为已删除。
- ⛔⭐⭐ **`speaker-calibration.test.ts` 与 `stt-local-first-claim-gate.test.mjs` 均未跟踪**
  ⇒ §584 的「门脚本不在版本控制」在说话人链上**又中一次**，且这次是**承重的**
  ⇒ 已并入 **A5** 的范围。

### 🔴 #54 更新【§590.2.3 一手源】候选解法从「重标阈值」变成「换形状」

读了官方 **Speaker Diarization 专题页 + models 页**（一手源）后，
**#54 的问题描述要改**：它不再只是「0.99 这个数取多少」。

| | 形状 | 聚类 | 中文 |
|---|---|---|---|
| 本仓 | 单段聚类：每个 VAD 段算 48 维向量 → 与已有 profile 逐个比余弦 | 贪心 + 固定阈值 0.99 | 无专用模型 |
| 官方 | **两段式**：segmentation（pyannote / reverb）产出「谁在何时说话」区间 → embedding（3D-Speaker / NeMo）→ **FastClustering** | 谱聚类变体，⭐ **可显式给 `num-clusters`** | ⭐ **有**：`3dspeaker_..._sv_zh-cn` |

⇒ ⭐⭐⭐ **而 2026-10-07 文档 §9.5.4 与本仓注释早就写着**「要真正对齐需要二者之一：
① 用**已知参会人数**约束下的强制分配；② 换成真实声纹模型」
⇒ **官方 API 恰好把这两件都做进了产品面**（`--clustering.num-clusters=4` + 中文声纹模型）。

⚠ **因此 #54 要拍的不再只是「阈值取多少」，而是三问**：
1. **继续用本仓单段聚类**，只把阈值按真机重新标一遍（成本最低，但**单阈值原理上不够**这条结论仍成立）；
2. **只加「已知参会人数」约束**（复用现有 `participants` 输入，不引入新依赖）；
3. **改走官方两段式**（要引 AAR + 换 embedding 模型 + 重写聚类段，**接入成本本节未算**）。

⛔ **明确未做**：未跑官方任何一个模型；未算 AAR 体积与双 ABI 打包成本；
⛔ **最关键的未核项**：官方示例是**整段 56.9 秒**音频，
而本仓的痛点恰恰是 **1.5~4 秒短段**（§589.6：短段同人拆开 66.7%）
⇒ **「官方这条路能否解决短段问题」目前没有任何证据**（见设计文档 §590.5 第 3 条）。

⚠ 官方页面上唯一采信的数字是 CPU RTF **0.170~0.452**（macOS、单线程），
⛔ **不代表安卓真机速度**。

### 🔴🔴 #54 实测更新【§591】`0.99` 在**真人中文短段上 100% 拆开**，且「区间重叠」在本样本上**不成立**

用 **sherpa-onnx 官方仓库的真实人声中文录音**（`0-four-speakers-zh.wav`，16k/mono/56.86s，
分段真值取自官方 models 页的 diarization 输出）实测本仓 `logMelEmbedding` 48 维：

| 窗长 | 同人对（区间） | 异人对（区间） | 阈值 0.99 下 |
|---|---|---|---|
| 1.5s | 27 对 `[0.798 ~ 0.976]` | 93 对 `[0.191 ~ 0.756]` | 拆开 **100%** / 误并 **0%** |
| 2.0s | 13 对 `[0.874 ~ 0.982]` | 42 对 `[0.274 ~ 0.742]` | 拆开 **100%** / 误并 **0%** |
| 3.0s | 1 对 `[0.978]` | 9 对 `[0.359 ~ 0.683]` | 拆开 **100%** / 误并 **0%** |

⇒ ⭐⭐⭐⭐⭐ **两条结论**：

1. ⛔⭐ **`SAME_SPEAKER_MIN = 0.99` 在真人中文短段上会让「同人被拆开」= 100%**
   （同人相似度**上界只有 0.976 / 0.982**，全部低于 0.99）
   ⇒ 每段话都新建一个说话人 ⇒ 整场会议出现 N 个「说话人 N」。
   ⇒ **它不是「偏紧」，是「在这类音频上恒失效」。**
2. ⭐⭐⭐⭐⭐ **反过来说明「存在可行阈值」**：1.5s 窗下同人下界 0.798 > 异人上界 0.756
   ⇒ **区间不重叠** ⇒ ⛔ **§589.6 记的「分布重叠 ⇒ 单阈值原理上就不够」是 TTS 夹具上的观察，
   在真人中文上（本样本）不成立。**
   ⇒ **⇒ 「换成可用阈值」这条最便宜的修法在这份样本上数学上成立。**

⚠ **两条都只在这一个样本上成立**（单文件、4 人、56.86 秒）⇒ **不可外推**。
⚠ **阈值取多少仍是产品口径，不代拍**（本仓设计前提是「宁可错拆不可错并」，
而「误并 0%」只在本样本成立）。

⛔ **仍未做（§591.1）**：**没跑任何 sherpa 模型** ⇒ **官方两段式 vs 本仓单段聚类的对照仍未做**；
本节测的是「本仓特征在真人中文上的性质」，**不是**「官方方案的效果」。
⇒ **「改阈值」与「改形状」的取舍，本节给不出数据**，两个选项仍在并列。

### 🔴🔴🔴 #54 权重更新【§592 实机对照】**「只重标阈值」这一问被降级**：间隔差 **4.39 倍**

在**同一份真人中文音频、同一个官方分段真值**上，本机实跑官方 3D-Speaker 中文声纹（512 维），
与本仓 log-mel 48 维逐对对比：

| | 本仓 log-mel 48 维 | 官方 3D-Speaker 512 维 |
|---|---|---|
| 同人对 | 27 对 `[0.798 ~ 0.976]` | 8 对 `[0.436 ~ 0.769]` |
| 异人对 | 93 对 `[0.191 ~ 0.756]` | 37 对 `[-0.069 ~ 0.251]` |
| **可分间隔（同人下界 − 异人上界）** | **+0.042** | ⭐ **+0.185** |

⇒ ⭐⭐⭐⭐⭐ **间隔差 0.185 / 0.042 = 4.39 倍**。

⭐⭐⭐⭐⭐ **⇒ 这不是「阈值调得准不准」的问题，是「特征能不能分」的问题**：
　· 本仓即使把阈值调进那 0.042 的窄缝，**任何一点分布漂移**（换人、换麦克风、底噪变化）
　　都会立刻吃掉它 —— ⭐ **本仓的异人分布跨度是 0.565，是间隔的 13 倍**；
　· 官方有 0.185 的余量（异人跨度仅 0.320）。
⚠ 与 §591 的「存在可行阈值」**并存不矛盾**：**数学上可行 ≠ 工程上稳**。

⇒ ⭐⭐⭐ **⇒ #54 的第一问（只重标阈值）应当降级**，第二问（已知参会人数约束）
　与第三问（改走官方两段式）权重上升。
⚠ **但第三问的成本仍未算清**：官方 embedding 模型 **fp32 38 MB**，
⛔ int8 体积未测 ⇒ **移动端打包成本未知**；
⛔ 且官方 **Python 绑定不暴露逐段结果**、`build-macos.sh` **不构建 CLI**
　⇒ **若要接，这两处会直接挡住第一步**（已实测，不是推测）。

⭐ **环境自证**：本机实跑官方 diarization 得 `4 speakers / 10 segments`，
与官方文档逐字一致，fp32 与 int8 两档同结果 ⇒ 读数可信。
⛔ 仍是**单样本**（1 文件 / 4 人 / 56.86 秒）⇒ **不可外推**；
⛔ 本次是 **macOS CPU**，⛔ **不得据此声称「安卓上实时」**。

### 🔴🔴🔴🔴 #54 再次升格【§593】问题被改写：**先问「要不要换切段机制」，再问特征与阈值**

同一份真人中文音频上，两条 VAD 逐段对比：

| | 段数 | 语音总时长 |
|---|---|---|
| 本仓 `VadSegmenter`（`silenceMs=1500`） | **6 段** | 30.720s |
| 官方 pyannote segmentation | **10 段** | 30.595s |
| 差 | **少 4 段** | 仅少 0.125s（**+0.4%**） |

⇒ ⭐⭐⭐⭐⭐ **语音总时长几乎相同 ⇒ 两条 VAD 找的是同一批语音；
　但本仓把其中 4 段合并成了 1 段 `0.630 -- 16.910`（16.28 秒），
　而那一段里官方分出了 3 个说话人**（speaker_0 / speaker_1×2 / speaker_2）。

#### ⭐⭐⭐⭐⭐ ⇒ 所以 #54 的问法要整个换掉

⛔ **「换 512 维声纹」解决不了它** —— 一段 16 秒里混着 3 个人，
无论用什么特征、什么阈值，**单段 embedding 都不可能判出「这是谁」**。
⇒ **必须先回答「切段要不要换机制」，特征与阈值才是第二层。**

⭐⭐⭐⭐⭐ **而根因是机制性的，调参解决不了**（三处实际静音时长已核）：

| 段间 | 实际静音 | ≥1500ms？ |
|---|---|---|
| 6.865 → 7.017 | **0.152s** | ⛔ 否 |
| 10.747 → 11.455 | **0.708s** | ⛔ 否 |
| 13.632 → 13.750 | **0.118s** | ⛔ 否 |

**换人说话的停顿（0.12~0.71s）远短于同一人内部的换气停顿**，
而 `vad-segmenter.ts:38` 的判据**只看能量与静音时长、完全不看「是不是换人了」**
⇒ ⛔ 静音阈值调到 0.1s 会把一个人的一句话切碎（§588.3 那族取舍）。

#### ⇒ #54 现在是这三问（按依赖顺序，不是并列）

1. ⭐ **切段要不要从「静音判据」换成「能识别换人」的判据**（官方 pyannote segmentation 就是这一层）；
2. ⭐ 若换 ⇒ **特征要不要换成 512 维中文声纹**（§592 实测间隔 +0.185 vs 本仓 +0.042，**4.39 倍**）；
3. ⭐ 簇数要不要用**已知参会人数约束**（官方 `--clustering.num-clusters`）。
⚠ ⛔ 第 1 问的成本最大也最未算清（**切段是主链路，动它影响转写与精校，不止说话人**）⇒ **不代拍**。
⛔ 三问都仍是**单样本**（1 文件 / 4 人 / 56.86 秒）读数，**不可外推**。

⭐ **复验已做（§593.5）**：该结论在**四档复刻口径**下全部一致（10ms/16ms/32ms 帧 × 浮点/uint8 量化 RMS），都是 **6 段、最长 16.2~16.3 秒、合并位置相同**
⇒ **不是复刻参数的产物**；即使完全按生产口径（rAF 周期 + uint8 量化）复刻，那 16 秒**仍是 1 段**
⇒ ⭐ **「被合并」由 `silenceMs=1500` 与实际换人停顿（0.12~0.71s）的量级差决定，不是任何可调参数的产物**。



⭐⭐⭐ **⇒ §592 的「换特征」结论被重估**：它解决的是**第二层**，
　而在换切段之前，**第一层的缺口大到让第二层的改进无法显现**。

### 🔴🔴🔴🔴🔴 #54 净效果【§594 实测】**一段话 = 一个说话人标签，全程零合并**（已撤回一处在途指控）

> ⛔⛔⛔ **本节的核心断言已被 §595 撤回；本节原文保留，仅加指针。**
> ⛔ 撤回「**与设计前提正面冲突、落在『无限错拆』那一侧**」——它只测了**增量阶段**；
> §595 实测**整场**为 **10→8、零错并、膨胀 2.0×**（真值 4）⇒ 与 `speaker-relabel.ts:11` 的
> 「2.31× / 错并 0」**量级同型**，落在设计前提那侧。
> ⛔ 连带收窄标题：「全程零合并」→ **「增量阶段从不合并」**（收尾 `relabel()` 实测合并了 2 次）。
> ⚠ 且本节「零合并」那个读数**一度被量具 bug 支撑**（`label` vs `profileId` 混用）。
> ✅ 仍然成立：增量阶段 45 对最大相似度 **0.9873** < 0.99 ⇒ 实时路径确实每段新建。
> 📍 详见下一节「#54 净效果再更新【§595 实测收尾】」。

用仓内**真实的** `SpeakerDiarizer` + 真实 `logMelEmbedding`（直接 import 生产代码，不复刻）：

| 切段方式 | 段数 | **产出标签数** | 真值人数 |
|---|---|---|---|
| 官方分段（每段 1 人） | 10 | ⭐ **10**（`说话人 1…10`，**无一合并**） | 4 |
| 本仓 VAD 分段（首段混 3 人） | 6 | **6** | 4 |

⭐ **机制**：45 对里最相似的那对只有 **0.9873**，差阈值 **−0.0027**
⇒ **没有任何一对达到 0.99** ⇒ `identify()` 必然每段新建说话人。

⛔⛔ **撤回 §593 末尾的「错并」指控**（它在被自己的变异打掉后作废）：
　变异台把 10 段的真值**全改成同一人**再跑 ⇒ 仍输出 **10 个标签、零合并**
⇒ **不是「合并出错」，是「合并从未生效」** ⇒ 用户看到的是
　**「说话人 1、说话人 2、说话人 3……」一路数下去，同一个人被切成 N 个身份**
　（§587 的「膨胀 2.31×」是其温和版本）。

⭐⭐⭐⭐⭐ **⇒ 这与本仓 `speaker-diarization.ts:22-29` 自写的设计前提正面冲突**
　（逐字：「错误合并→**严重**；错误拆分→只是标签多、难看……宁可多几个标签，不可错配人」）
　—— 而实际落进的是**「无限错拆」那一侧**。

✅ **阈值口径已核销**：`recordingRuntime.ts:322` 是 `new SpeakerDiarizer()`（无参数），
全仓唯一生产构造点 ⇒ **本节读数直接适用于生产路径**。

⛔ **但本节测的是逐段增量路径，而 `relabel`（收尾整场累积修补，`:130`）
本该把碎片收回** ⇒ ⭐ **「收尾后还剩几个标签」是本条最关键的未核项**，
它决定这是「用户可见缺陷」还是「只在过程中可见、收尾自愈」。

### 🔴🔴🔴🔴🔴🔴 #54 净效果再更新【§595 实测收尾】⛔ **撤回「无限错拆」**：整场是「2.0× 膨胀 + 零错并」

上一节末尾那个「最关键的未核项」已测完。用仓内**真实的** `SpeakerDiarizer` 跑完
**增量 → `relabel()` 收尾**的完整闭环（`/tmp/opstt/s595/relabel2.mjs`）：

| 切段方式 | 段数 | 增量标签 | relabel 合并条目 | **收尾后簇数** | 真值 | **错并** |
|---|---|---|---|---|---|---|
| 官方分段（每段 1 人） | 10 | 10 | 2（`5→1`、`10→1`） | ⭐ **8** | 4 | ✅ **0** |
| 本仓 VAD（首段混 3 人） | 6 | 6 | 1（`6→2`） | **5** | 4 | ⛔ **1** |

⭐⭐⭐⭐⭐ **⇒ 撤回上一节的「无限错拆 / 与设计前提正面冲突」**：
　`relabel()` 把真值 0 的三段**完整收回**（簇 1 ← `{0,0,0}`），
　其余 8 个簇**每一个都是纯簇、零错并** ⇒ 整场 **膨胀 2.0×**，
　与 `speaker-relabel.ts:11` 自陈的「2.31× / 错并 0」**量级同型**
　（⚠ 非同口径：那是 TTS 夹具读数，本次未跑同口径对照）。
⇒ **落在 `speaker-diarization.ts:22-29` 设计前提那侧，不与之冲突。**

> ⛔⛔ **上面最后两句已被 §597.2 撤回，原文保留不动。**
> §597 用仓内 TTS 夹具跑同口径对照：`RELABEL_MERGE_MIN = 0.97` 在该夹具上**产生 2 个错并**
> （`speaker-relabel.ts:40` 自陈「1 例」，**低估了一倍**）⇒ 夹具那一侧落在「**错并**」而非「欠合并」，
> 与 `:82-85` 论证的取舍**相反**；且 `:45`/`:91` 那句「真人更接近、错合并会更多」被**证伪**
> （真人 **0** 个错并 vs 夹具 **2** 个，方向相反）。
> ⚠ **更根本的**：`speaker-relabel.ts:33-38` 那张表**复现不出来**（分母隐含 13 而夹具真值 9；
> 「0.99 档 0 次合并」被实测打掉；0.97 与 0.95 两行**都标「← 采用」**自相矛盾）
> ⇒ **一个生产常量 0.97 的唯一依据，是一张无法复现且内部矛盾的表**。详见设计文档 §597。
> ✅ **不撤回的部分**：「真人中文上 `0.97` 只收回 2/6、一次都没错并」—— 那是实测事实。

⛔ **连带上节的标题也要收窄**：「全程零合并」→ **「增量阶段从不合并」**。
　✅ 上一节**仍然成立**的部分：增量阶段 45 对最大相似度 0.9873 < 0.99 ⇒ **实时路径确实每段新建**。

⭐⭐⭐⭐⭐ **撤回的形态比结论更值钱**：这是
　「**我测了链路上的一段，把那一段的读数当成了整条链路的读数**」——
　而代码里**早就存在**一个专门针对该现象的补偿机制（`speaker-relabel.ts` 整个文件就是）。
⇒ 纪律：**凡结论形状是「某机制 X 从未/从不发生」，必须先核完整条链路的所有阶段再断言「从不」。**

⭐⭐⭐⭐⭐ **新归属：唯一的错并完全出自切段层，聚类层修不了它**
　本仓 VAD 那 1 个错并 = 簇 1 ← `{0,1,2}` = §593 那段 16.28s 里的**三个人被压成同一个「说话人 1」**。
　`relabel()` 的动作集**只有「合并」，没有「拆分」** ⇒ 任何阈值、任何特征都救不了它。
⇒ **同一个用户可见缺陷有两个来源，量级完全不同**：
　**错拆**（聚类层，两个阈值只**部分**能收）· **错并**（切段层，**只能靠改切段**）。

⇒ **#54 三问的权重（§595 之后，不代拍）**：

| 问 | 变化 |
|---|---|
| **① 切段要不要换成「能识别换人」的判据** | ⭐⭐⭐⭐⭐ **权重最高，且是三问里唯一能修掉错并的杠杆。** 仍是主链路（影响转写与精校），成本最大 ⇒ **不代拍** |
| **② 特征要不要换 512 维中文声纹** | ⭐⭐ **收益扩大到第二层**：§592 的间隔 +0.185 是单段对单段；`relabel()` 比的是**聚类均值**（统计量更稳）⇒ 若该间隔在均值上同样成立，`0.97` 会宽松得多，有可能一次收回真值。⛔ **未测** |
| **③ 簇数要不要用已知参会人数约束** | ⭐⭐⭐ **新增论据**：收尾后 8 簇 vs 真值 4，而收尾阶段天然就是「已拿到全部样本、且可整体重分配」的位置 ⇒ 正是官方 `--clustering.num-clusters` 对应的位置，产品已有 `participants` 输入 |

⚠ **阈值仍不代拍**：实测只登记读数 —— 真人中文上 `RELABEL_MERGE_MIN = 0.97`
　**只收回 2/6 个碎片，但一次都没错并** ⇒ 落点正是 `speaker-relabel.ts:82-85` 自陈的那一侧
　（「按『错合并 ≫ 欠合并』的代价不对称……**欠合并才是该选的那一侧**」），
　兑现了它 `:91` 那句「⚠ **仍然是 TTS 标定**，真人会议更接近」。

⚠ **实测读数偏乐观，两条生产过滤未走**（⛔ 未核）：
　① `planSpeakerRelabel` 的 `isPlaceholderSpeaker` 过滤（只回贴仍是占位标签的段）
　② `segmentProfiles` 若未记录则 `remap` 为空（合并做在 `profiles` 上但段标签不回贴）
　⇒ 用户实际看到的标签数可能 **少于**表里的 8 / 5。

⛔ **边界**：单样本（1 份 / 4 人 / 56.86s）**不可外推**；本次仍用本仓 `relabel()` 跑官方分段
　（官方**第二段聚类**与本仓 relabel 的对照仍未做）；未在真机/浏览器跑；官方 embedding int8 体积未测。

⭐⭐⭐⭐⭐ **附带记一条量具通则（本次脚本改了三版才拿到上面的数）**：
　**真值/标签一旦从 `string` 变成数组、或从复合值变成原子值，
　所有「按值去重 / 集合相等 / 相等比较」的下游会静默改变语义。**
　症状不是崩，是**读数变成另一个同样体面的数**——本次依次读出
　「零错并」→「错并 1」→「本仓 1 个错并 / 官方 0 个错并」。
　具体三处：① 用 `identify()` 的 `label`（`说话人 N`）查 `canonicalOf`（键是 `sp-…`）
　⇒ **恒读出「零合并」**（⚠ 这正是上一节「零合并」那个读数的来源）；
　② 真值 `'0+1+2'` 当原子标签 ⇒ 藏掉了 §593 的错并；
　③ 改成数组后 `new Set` 变成**对引用去重** ⇒ 恒判「混人簇」。

### 🔴🔴🔴 #55 新增【§596 实测】**收尾合并后说话人编号跳号** —— 与 #54 完全无关的一个层面

§596 走完 `mergeOverSplitSpeakers()`（`recordingRuntime.ts:571-619`）的**完整生产数据流**后，
除确认 §595.7 未核项①之外，挖出一个**全新的、与 #54 三问没有任何交集**的缺陷。

⭐⭐⭐⭐⭐ **读数**（用户实际看到的清单 = `syncSpeakers()` = `getProfiles().map(p => p.label)`）：

| 场景 | 用户看到的清单 | 编号序列 |
|---|---|---|
| 官方分段（10 段 / 真值 4 人） | `说话人 1 2 3 4` **`6 7 8 9`** | ⛔ **缺 5** |
| 本仓 VAD 分段（6 段 / 真值 4 人） | `说话人 1 2 3 4 5` | ✅ 连续 |
| 负控：一段已被用户改名为「张伟」 | `说话人 1 2 3 4` **`7 8 9`** `张伟` | ⛔ **缺 5、6** |

**机制**：`SpeakerDiarizer.relabel()` 只做两件事 —— 摘死簇（`:269-270`）与只更新 `sampleCount`（`:282-285`）。
⭐ **它从不重编号**，而 `label` 是 `createProfile` 一次性生成的 `说话人 ${nextId}`（`:306`）
⇒ 被吸收的编号**在清单里留下空洞**。
⚠ 触发条件可精确刻画：**当被吸收的 profile 不是最大编号时就会留缺口**
（VAD 场景只吸收 `6→2`，摘掉的是最大编号 ⇒ 恰好连续）。

⭐⭐⭐⭐⭐ **为什么它必须与 #54 拆成两条**：
**#54 问的是「怎么判断谁是谁」（准确性，涉及阈值/切段/特征）；这一条是「编号怎么呈现」（UX）。**
**改它不需要碰任何一个阈值** ⇒ 它可以在 #54 拍板之前**先行拍板**，不必排队等。

⚠⛔ 全仓**没有任何**关于此事的登记或注释（唯一 grep 命中是邮件分页的「不得跳号」，与本条无关）。

#### 🔴 三个选项（**不代拍**）

| 选项 | 做法 | 代价 |
|---|---|---|
| **① 保持现状** | 跳号就是「发生过合并」的痕迹 | 用户看到 `1 2 3 4 6 7 8 9`，会以为漏了个人 |
| **② 收尾重编号** | 合并后把存活簇重排成 `1…N` | ⛔ 用户录音**过程中**看到的编号会在收尾**后改变**（「说话人 7」可能变成「说话人 5」）⇒ **打破心理引用** |
| **③ 只在 UI 层重显示** | 底层 `label` 不动，展示层按存活顺序编号 | 展示与存储不一致；已落库段的 `speakerLabel` 仍是旧号 |

⚠ 三个选项**都不是纯技术修复**（② 尤其会改变用户可见的稳定标识）⇒ **登记不代拍**。
⭐ 无论怎么拍，都**不影响** §595.8 那 7 条判据（它们断的是**身份数**，不是编号）。

#### ⭐ 顺带关闭 §595.7 未核项①

§595.2 报的「8」**没有**被 `planSpeakerRelabel` / `syncSpeakers` 两道生产过滤打折
（实测 `plan.updates` = 2 条非空）⇒ **§595.2 的 8 就是用户实际看到的 8**。
⭐⭐⭐ 并顺带证成一条**结构性事实**（比跑探针更强）：
`recordingRuntime.ts:594` 的 `if (plan.updates.length === 0) return` 排在 `syncSpeakers()` **之前**，
看起来像「合并做了、清单不刷新」的隐患，但**它结构上不会在「发生了合并」时触发** ——
四个前提逐条核到代码行：`labelSpeaker` 同步改 profile 与段标签 ⇒ `pinned` 只由 `labelProfile` 置位
⇒ 安全线 1 保证**被 drop 的必不是锚点**（其段标签必仍是占位）⇒ 必进 `updates`；
且 `labelOf(next)` 取的是最终存活簇的 label，必存在。**合并一旦发生 ⇒ `updates` 必非空 ⇒ 清单必刷新。**

⚠ 同批记一条**最阴险的量具坑（§596.4）**：第一版读数脚本把真值 `[0,1,2]` 在存 `trace` 时
`join('+')` 成字符串，回读时**逐字符展开** ⇒ 多出一个 `'+'` 被当成真值
⇒ 它报出的**结论恰好正确**（首段确实混 3 人），**但归因是错的**。
⇒ ★ 判别动作：打印混人簇时**必须连成员真值一起打**，只打「混人簇个数」会顺利通过。

### 🔴🔴 #57 新增【§605 实测】**一个负载敏感的既有测试**：`TestPiAdapter_SendPromptHappyPath`

§605 在给 §604 补完判据后跑 `go test ./... -count=1` 回归时撞到的**与本项目录音链路无关**、
但会影响 CI 的既有问题。

**读数**（同一份代码、同一台机器、连跑两次全量）：

| 轮次 | 结果 |
|---|---|
| 第 1 次 | **EXIT=1**，`internal/agent` 失败（36.287s）|
| 第 2 次 | **EXIT=0**，**58 包全绿 / 0 FAIL** |

失败明细逐字：`--- FAIL: TestPiAdapter_SendPromptHappyPath (10.00s)` /
`adapter_pi_test.go:123: SendPrompt: agent request timed out: context deadline exceeded`。

⭐ **10.00s 正好是它自己的 ctx 上界**（`adapter_pi_test.go:111` 的
`context.WithTimeout(context.Background(), 10*time.Second)`）⇒ 是**超时**，不是断言不匹配。

⭐⭐⭐ 隔离跑 3 次全部通过、总耗时 **0.899s** ⇒ **不是功能缺陷，是负载敏感**：
该用例走 `writeFakePi(t)` 起一个**真实子进程**，全量并行时进程启动可能超过 10 秒。

⚠⚠ **为什么值得单独立项**：CI 的命令是 `go test -race ./... -count=1`
（`.github/workflows/backend.yml`），**`-race` 让可执行文件更大、启动更慢**
⇒ **CI 上触发概率只会比本机更高，不会更低**。
⚠ 并且：§602 记的「58 包全绿」**不能当作这条用例稳定的证据** —— 那一次的通过是运气。

⛔ **不代拍**（三选一）：
① 维持 10 秒、接受偶发红；
② 调大上界 —— ⛔ 等于放松「agent adapter 不许挂死」这条契约；
③ 把子进程启动与被测逻辑解耦（预热 / 复用 fake 进程）—— 治本，但属测试结构改动、改动面最大。

⛔ **未核**：CI 上该用例的实际失败率（本机无 CI 读数）；`-race` 下是否更易触发（未实测）。

### 🔴🔴 #58 新增【§608 实测】**相邻两段完全相同且超过 40 字时，Go 与 TS 行为分叉**

§607 补上第三对常量的**值**比对之后，剩下的问题是：**参数全相等，两侧对同一输入是否仍同解？**
本节用 §455 同样的方法回答：**造语料，两侧各跑自己那一份实现，逐条比对输出**（不推演）。

**读数**（语料：`committed` 与 `next` **完全相同**）：

| 段长 | Go 输出 | TS 输出 | |
|---|---|---|---|
| 「甲」× 60 | **120**（保留两份）| **80**（裁掉 40 字）| ⛔ 分叉 |
| 45 字真实句子重复一遍 | **90** | **80** | ⛔ 分叉 |
| 「甲」× 40（正好等于阈值）| 40 | 40 | ✅ |
| 35 字真实句子重复一遍 | 35 | 35 | ✅ |

⇒ 分界线正好在 `MAX_OVERLAP = 40`：**超过它，Go 保留两份、TS 裁掉一份。**

**机制**：`mergeIncremental:288` 有一条专门的分支
「若 `next` 整段就是 `committed`（且超窗口）⇒ 更可能是用户真的重复说了一遍，**保留两份**」，
并由 `TestMergeIncrementalDoesNotDropRealContent` 显式钉住（`60 rune → 120`）；
`dedupeSegmentText` **没有**这条，它直接走 LCS 那条路，把其中的 40 字当成切片重叠裁掉。

⚠ **后果的性质**：TS 那一侧是**删掉用户真的说了两遍的内容** ——
与本仓反复强调的那句完全同向：「误去重的代价是丢掉真实内容，用户永远发现不了」。
⚠ 且该形状**零测试覆盖**：Go 侧有专门用例，TS 侧这一形状**一条都没有**（`meeting-dedup.test.ts` 734 行里查无）。

⛔ **本轮只登记、不代拍**，三选一（属**产品口径**）：
① **TS 补齐 Go 那条护栏**（两侧都保留两份）—— 代价是用户界面上同一段话会出现两次；
② **维持现状**（前端裁一份、后端留两份）—— 代价是两条路径展示不一致，但前端少重复字；
③ **改 Go 对齐 TS** —— ⛔ 会推翻一条**已有的、带测试的**契约，风险最大。

⭐ **登记方式**（按后端同步门自己的原则「把一个行为差异偷偷改成一致，比留着它更危险」）：
* TS 侧 `meeting-dedup.test.ts` 新增 `§608` 一组 **4 条用例**，钉住现状读数 + 一条「正向要求」；
* Go 侧 `TestKnownDedupDivergencesAreStillTheKnownOnes` 新增检查：**那段登记被删掉就报红**
  （已用真变异验过：删掉 §608 段 ⇒ FAIL，还原后 PASS）。

⛔ **未核**：除常量与这一条外，两份实现是否还有**别的**行为分叉 —— 本节只测了这一种形状，
⛔ **没有**做全语料差分（第 ③ 项「除常量外的算法分叉」仍开着）。

---

### 📋 #59【§681 新增·已自行落地技术部分】`FindChineseFont` 的 `<dataDir>/fonts` 级只试 `matches[0]`：**那行代码在测试里从未被执行过**

**本条已自行落地的部分不需要拍板**（技术性修复 + 双向变异验证已完成）：
`invoice_pdf.go` 的 dataDir 分支改为复用 `pickCJKFont` 逐个试；
`invoice_font_cjk_test.go` 新增 2 条判据（正向 + 负控）。
详见 `2026-10-06-recording-quality-fixes.md` §681。

**下面两条是产品口径，登记不代拍：**

**① fonts/ 目录存在但全无中文字形时，是否应当像 env 分支那样硬失败？**

- 现行为：静默回退系统字体。
- 备选：返回空并报「`/app/data/fonts/` 里的字体都没有中文字形」。
- 争点：`fonts/` 目录存在本身**未必**代表操作员的明确意图（可能是历史遗留杂物）。
  改成硬失败会把「目录里躺着个旧 .ttf + 系统字体完全正常」的好端部署打成 `failed`。
  这与 §681.8.1 是同一个问题：第 1 级（env）拒绝静默替换，第 2 级允许——
  **这个不一致要不要抹平，取决于「多严算失败」，属口径。**

**② 字体选择是否应当可观测？**

- 现状：无论选中哪一级都**没有日志**，只有 `server_email_pipeline.go:220`
  在「三种方式全找不到字体」时打一行。
- 后果：「我放了字体但没生效」这类问题**无法自证**。本节能发现这个缺陷，
  靠的是读源码 + 构造夹具，不是靠日志。
- 备选：选中时打一条 info 级日志（含来源级别与字体路径）。
- 争点：日志量与隐私（字体路径不是敏感信息，成本低），属口径。

**③ 决策简报那句「本页编号覆盖 #4–#N」的自由文本，要不要也纳入 §574 那道门的断言范围？**

- 现状：门只断言**各组表头的件数 == 实际行数**；「覆盖范围」这句话本身**不在门的覆盖范围内**
  （那行自己写着这条免责声明）。
- ⚠ **§681.9 已实测：这行当时就已经是错的** —— 写「A 4 / D 29」，而表头声明的是 **A 6 / D 31**。
  也就是说它从 §574 到现在一直在漂，**没有任何东西会看见**，因为门不断言它。
  已随 #59 入册一并订正为「A 6 / B 5 / C 20 / D 32（即各组表头声明的件数）」，并把订正过程写进那行本身。
- 争点：加严既有门属取舍；且判据必须与被检面**同粒度**——
  §574 负控④ 已经记着「散文里的 `#N` 提及不算登记」，照着扩很容易写出恒绿的门。

⚠ **本条的实际影响面要如实说**：本仓库**没有任何 Dockerfile 层或脚本会自动往
`data/fonts/` 放字体**（`grep -rn 'data/fonts|dataDir/fonts' --glob '!*_test.go'`
只命中代码、注释、报错文案与一条历史 handoff 记录）
⇒ 默认部署下这条分支大概率从不命中，**缺陷真实发生率未测到**，不估。
真正影响面更大的是 §681.3 那半：修复前 `TestPickCJKFont_SkipsLatinOnlyEntry` 与
`TestFindChineseFont_AcceptsRealCJKFont` **在 macOS 上恒 skip**，而前者的注释
自称是「负控的载体：把 `fontHasCJK` 的检查去掉，它必然转红」——负控在开发平台上一直是断的。

---

### 📋 #60【§682 新增】`loadCompanionOverlay` 用**第三条** dataDir 规则 —— 与 `ResolveDataDir` 不是同一条

**机制已证、影响未测**，详见 `2026-10-06-recording-quality-fixes.md` §682.3。

`config.ResolveDataDir`（`config.go:722`）本身完全正确；但 `config.go:739`
的 `loadCompanionOverlay` 自己去读 `POCKET_DATA_DIR`，未设时**硬编码回落 `/app/data`**，
与 `ResolveDataDir` 的 `Dir(POCKET_DB_PATH)` **不是同一条规则**，
且 `os.ReadFile` 出错直接 `return`——**一个字都不说**。

⚠ 与 §681 同族，方向反过来：那边是「一个函数里两条分支不一致」，
这边是「两个函数各算一次」。**不一致的那一方恰好是没注释的那一方，
而正确的一方因为有注释显得更权威** —— `main.go:67` 甚至专门警告过
「必须用 `ResolveDataDir` 而不是裸的 `filepath.Dir(cfg.DBPath)`」，
**于是没人怀疑还有第二份实现**。`/app/data` 是全仓唯一一处测试外的硬编码。

**本条只登记，不代拍，且有一个前置事实必须先补齐：**

⛔ **`companion.env` 在本仓没有任何产出方** —— `grep -rn 'companion\.env'`
（全类型）只有 1 个消费者（`config.go:744`）、**0 个生产者**。
**在「谁会放这个文件」被说清楚之前，改哪一边都是在猜。**

**① `POCKET_DATA_DIR` 未设时，`loadCompanionOverlay` 要不要也试 `Dir(POCKET_DB_PATH)`？**
- 改则：非 Docker 部署（compose 设 `POCKET_DATA_DIR=/app/data`；
  5 个本地脚本也都设了它 ⇒ 这两处现状恰好一致）之外的部署能读到 overlay。
- 不改则：现状保持，overlay 静默读不到。
- ⚠ 它处在 bootstrap 顺序里（`Load()` 的**第一句**就是它，此时 `cfg.DBPath` 还没读），
  这是一条**有部署面影响**的行为变更 ⇒ 属口径，不代拍。

**② 要不要给这条路径加日志？**（至少在未设 `POCKET_DATA_DIR` 时打出它实际找了哪个目录）
现状 `os.ReadFile` 出错就静默 `return`。与 **#59② 同族**（字体选择无可观测性），
建议两条一起拍。

**③ §682.2 的阴性结论要不要升级成「逐函数人读同族分支口径」的巡检项？**
自动扫描**已被证明只能测注释分布、测不到口径**（v1/v2 两个扫描器的读数见 §682.1/§682.2），
所以这不是「加一道门」，是一次性或周期性的人工读码工作 ⇒ 属工作量口径。

⚠ **引用本条阴性结论时的正确说法**：「在文档枚举了多级回退、且分支注释分布不均的
**127 个函数**中，没有第二个 §681 形状的实例」——
**不是**「本仓不存在第二个 §681」。自动扫描证明不了「没被写成断言的东西」。

---

### 📋 #61【§683 新增】「所有 `MeetingSegment` 生产者都带齐 `startMs`/`endMs`」**没有任何判据钉住**

详见 `2026-10-06-recording-quality-fixes.md` §683。技术部分已自行落地（注释，零行为改动）。

**§683 的实测结论（比 §634 更强）**：`dedupeStep` 选档的那一行是
`useExact = hasTiming ? overlapByTiming : true`（`meeting-dedup.ts:258`），
精确档有**两个**入口，而 §634 只钉死了第一个（时间轴判重叠恒 false）：

| 入口 | 条件 | 实测 |
|---|---|---|
| ① 时间轴判为重叠 | `hasTiming && likelyAudioOverlap` | **恒 false**（§634） |
| ② **缺时间戳兜底** | `!hasTiming` ⇒ `true` | **§683 新增：也走不到** |

⇒ 四个生产者（`vad-segmenter.ts:246` / `ingest-speech.ts:57` / `meeting-utterance.ts:36`
/ `meeting-final-transcript.ts:403`）都带齐时间轴，DB 列 `end_ms INTEGER NOT NULL`
（`native/schema.ts:333`）⇒ **`hasTiming` 恒 true**
⇒ ★★★ **`dedupeSegmentText`（精确档）在生产里不可达，`dedupeSegmentTextSoft` 是唯一活路径。**

⇒ 对 **#58** 是**双向**的：⬆ 加重（不再是「默认路径」，**没有退路**，两条链都过 soft）；
⬇ 收窄（精确档的既有测试钉的是生产上不存在的状态，包括 `meeting-dedup.test.ts:544-545`
那条专门造 `startMs: undefined` 的用例——⚠ 但**不建议删**，它钉的是防御性代码，方向是对的）。

⚠ **真正的缺口不是「那条分支走不到」，而是「让它走不到的那个不变量没人钉」**：
`meetings-store.ts:321` 的 `r.end_ms as number` 靠 DDL 的 `NOT NULL` 兜住，
但那条 DDL 与那条 cast 之间**没有断言连接**；将来新增一条漏了 `endMs` 的链路，
`useExact` 会**静默**切到精确档，而没有任何测试会在生产形状上发现。

⛔ **三选一（属口径与工作量，不代拍）**：
① 不动，只留 §683 的注释（零成本，缺口原样保留）；
② 加一道判据：扫 `MeetingSegment` 构造点，断言每处都显式给了两个时间字段
（或断言 `end_ms INTEGER NOT NULL` 与那个 cast 同时存在）；
③ 把 `dedupeSegments` 入参从 `startMs?/endMs?` 收紧为必填 —— 最硬，但波及三个调用点，
且**防御分支 `: true` 也就没有存在意义了**。
⚠ ③ 会改变 §683.3 所说「防御性代码」的性质，所以三个选项**不是同一决策的三个强度**。

⭐ **★ §688 已把依据从「读值」升到「读类型」，五个环节逐个钉死，结论不变**：
`MeetingSegment.endMs: number` 必填（`meetings-store.ts:55`）·
DB `end_ms INTEGER NOT NULL`（`schema.ts:333`，**写不进去 null，会抛错**）·
`AttributableSegment` 的 `Pick` **带了** `'startMs'|'endMs'`（`meeting-final-transcript.ts:232`）·
`attributeFullTranscript` 是**纯展开** `({...o, text})`，从不新建段（`:327`）·
`RefineBaseInput.elapsedMs: number` **必填**（`:375`）⇒ 兜底出口的 `endMs` 恒为数字。
⇒ **§684/§685/§686 全部结论不受影响。**
⭐ **§688.4 补完**：上面第 2 条的「运行时兜底」**三层错误路径逐个查过，无一层吞异常**——
`saveSegment`（`meetings-store.ts:258`）无 try/catch · `localDB.run`（`local-db.ts:793`）无 try/catch ·
Capacitor `conn.run` 约束冲突返回 rejected promise
⇒ **缺 `endMs` 的段进不了库，且会让故障显形**（不是静默存 null）。

⭐ **★ 选项 ② 的门怎么设计，§688 已给结论：不要用「扫构造点」**——
§687.2 证明那个正则**看不到展开式构造**（而 `attributeFullTranscript` 正是展开式）。
⇒ 推荐改成**打在声明处的四条文本断言**（复用 `dedup_threshold_sync_test.go`
已有的「把另一侧当文本读」手法）：`MeetingSegment` 两字段不带 `?` ·
DDL 两列含 `NOT NULL` · `AttributableSegment` 的 `Pick` 含两个字段 · `elapsedMs` 不带 `?`。
⇒ 这样对「新建段 / 展开复制 / `Pick` 搬运」三种构造方式一视同仁。
⚠ **本节只出设计、不落地**（增门属待拍，且「Go 测试是否进 CI」本身也未拍）；

⭐ **★ §689 结清了一条老账（「仍未核」清单里的 §612.2）**
- 跨语言基准**今天仍然有效**：Go `TestMergeIncrementalPinsCrossLangBaseline` 实测 PASS ·
  TS `:515-521` 两条 `goBaseline` PASS（在 45/45 内）。§612.2.3 登记的局限也已被
  `crosslang_baseline_pin_test.go` 关闭。
- ⚠ **但它守的是产品当前不产生的场景**：它那两条语料**故意让段时间轴重叠**
  （`0–3000` / `2500–6000`）才走得到精确档；而 §634 已实测前端送的是**纯新增窗口**，
  `IncrementalOverlapSec` **零使用点**（「带重叠」这项设计从未接线）。
  ⇒ **建议原样保留**，并在 overlap 真接线那天升级为「已生效场景」的守卫。
- ⚠ **同时收窄 §684 的说法**：§608 那 4 条是**直调内层函数、绕过唯一的选档入口
  `dedupeStep`** ⇒ 结构上不可达；而 §612.2 这两条**走了外层**、只是喂了不现实的输入
  ⇒ 入口会选中它，只是当前无此场景。**两者处置不同，别混。**
且它**只钉声明、不钉调用点**——硬写 `endMs: undefined as any` 仍会照绿，
这条残余风险如实登记在 #61 里，由属主一并定。

---

### ★★★ #58 更新【§684 实测】登记的那条分叉与生产里真实发生的**不是同一条代码路径** —— ① 的落点与代价都变了

⛔ **不代拍。** 本节只给三选一补读数，选项本身不变。详见
`2026-10-06-recording-quality-fixes.md` §684。

**一、生产形状上复现（`dedupeSegments` + 真实不重叠时间轴 ⇒ 按 §683 必走 soft）**
段长 38→60 全扫：**41–56 全带分叉**（`out[1] !== 原文`），38–40 与 57+ 不分叉，
分界线与 §608 的「`40 < len ≤ 56`」逐字吻合；产物是**跨位置拼接**——
41 字那条给出「**今补**」（「今」取自段首、「补」取自中段，两字原文从不相邻）。

**二、候选修法实测（探针，已还原 SHA `e2134ea6…` 零残留，未落地）**
把 Go `incremental.go:288` 的护栏搬进 `dedupeSegmentTextSoft`：
**全带 23/23 与 Go 逐字节一致**，且**现有 4 个去重测试文件 76/76 仍全绿**。

**三、⭐ 关键事实：§608 的 4 条登记调的是 `dedupeSegmentText(s, s)` —— 精确档**

| | 路径 | 生产可达 | §608 登记 |
|---|---|---|---|
| §608 登记的分叉 | `dedupeSegmentText`（精确档） | ❌ **不可达**（§683 实测） | ✅ 钉了 4 条 |
| §684.1 测到的分叉 | `dedupeSegmentTextSoft`（soft） | ✅ **唯一活路径** | ❌ **未登记** |

⇒ 同一个触发带、同一种产物，**生产只能走到未登记的那条**。
⇒ 这也解释了「行为都变了却 76/76 全绿」：**§608 的登记不是保护现状，
而是把一条死路径的现状钉住了**——它拦不住任何生产形状的改动。

**⇒ 对三选一的修正（选项不变，落点与成本变了）**

- ⛔ **① 的落点不在 `dedupeSegmentText`**。在精确档加护栏按 §683 的读数
  **对生产零影响**：会让 §608 的 4 条登记转红，而用户什么都没变
  ⇒ 典型的「改了、门红了、缺陷没修」。
- ✅ **落点在 `dedupeSegmentTextSoft` / `lcsNetNew`**（§684.2 实测有效且不撞现有判据）。
- ⚠ **① 的真实成本此前被低估**：还要**同时改 §608 那 4 条登记**（它们钉的是死路径）。
  不是「两行代码」。
- ⛔ **③ 的风险评估不变**：Go `mergeIncremental` **恒走精确策略**
  （§626：Go 全文件无 soft 变体、无时间轴分支）⇒ ③ 等于让 Go 永久失去
  「整段重复保留两份」这条护栏，代价是 Go 侧开始删用户真重复说的话。

---

### ★★★ #58 再更新【§685 跨语言双跑 80 对】严重性要**下调**、修法要**上调**

⛔ **不代拍。** 详见 `2026-10-06-recording-quality-fixes.md` §685。

搭了真函数双跑跑道（Go `mergeIncremental` × TS 生产形状 `dedupeSegments`），
语料 10 类变异 × 8 档长度 = **80 对**。**结论推翻了 §684 的定性两次**。

**一、护栏只消掉 21 条分叉里的 4 条**

| | 总数 | 无护栏分叉 | 加护栏后 |
|---|---|---|---|
| `identical` | 8 | 7 | **3** |
| `suffix_short` | 8 | 8 | **8** |
| `reorder` | 8 | 6 | **6** |
| 其余 7 类 | 各 8 | 0 | 0 |
| **合计** | **80** | **21** | **17** |

⇒ 护栏只关掉 `identical` 且 `len > 40` 的 **4 条**（41/45/50/56 逐档转一致），
**其余 13 条一条都碰不到** ⇒ 「① = 两行代码」这个估计**错**。

**二、⭐ 「跨位置拼接」不是 TS 的特性，Go 侧同样命中**

用正确的不变量（**展示的段文本必须是 `next` 的连续后缀**）重测：

| | 段文本被拼成跨位置字串 |
|---|---|
| TS（无护栏 / 加护栏） | **37/80 / 33/80** |
| **Go 真函数** | **32/80** |

⇒ 两侧命中率几乎一样 ⇒ **它是 LCS 去重这条设计的共同属性，不是 Go/TS 分界**。

**三、⇒ #58 的严重性下调，修法上调**

- ⬇ 「TS 插入原文不存在的拼接字串」**只在「相邻两段逐字相同且 41–56 字」这一格成立**。
  按原文理解会以为问题面比实际大得多。
- ⬆ 护栏对这一格是**彻底修复**（4 → 0），且 §684.2 实测不撞现有 76 条判据。
- ⭐ `suffix_short` 8 + `reorder` 6 + `identical len≤40` 3 共 17 条，
  性质是「**Go 去掉一份重复、TS 保留两份**」——TS 是**更保守**的一侧，
  与「误去重的代价是丢掉真实内容」方向一致 ⇒ **不是缺陷，是口径差**，② 正是为它们准备的。

**四、两次自我推翻都记在案**

- 语料第一版 `punct` 变异是**空操作**（基准句里没有「，」），
  那 8 条其实是 `identical` 的重复计数 ⇒ 已给生成器加「变异必须真的改变输入」的断言。
- `go test` 第二次跑返回 `(cached)`：Go 缓存键含 env **路径**但**不含被读文件内容**
  ⇒ 语料换了输出是旧的 ⇒ 必须 `-count=1` 且核对两侧行数。

**⚠ 留白**：读数全部来自**合成语料**，真实 ASR 文本的各类占比未测；
只跑了**两段**，`dedupeSegments` 的多段累积（生产真实形态）**未跑**
⇒ **21/80 不是分叉率，是一个下界样本**。

---

### 🔴 #58 再更新【§725 实测】**主因不是 ASR 抖动，是 `MAX_OVERLAP=40` 的窗口截断** —— 触发条件被重新定位

⛔ **不代拍。** 详见 `2026-10-06-recording-quality-fixes.md` §725。

这条**改变 #58 的定性依据**，请配合 §685 一起读。

**一、可证伪的预测与实测（drift=0，完全无 ASR 抖动，只扫重叠长度）**

`meeting-dedup.ts:74` 是 `MAX_OVERLAP = 40`，而 `lcsNetNew:136-139` 把对齐窗口封顶在这：

```ts
const limit = Math.min(MAX_OVERLAP, cs.length, ns.length)
const tail = cs.slice(cs.length - limit)   // committed 尾部 40 字
const head = ns.slice(0, limit)            // next 头部 40 字
```

⇒ 真实重叠 `> 40` 时两个窗口覆盖的是重叠区的**不同子区间**，LCS 在错位窗口上对齐。

> 预测：重叠 ≤ 40 不发生；> 40 发生。**实测：异常区间 42–56，分界点精确在 40/42。**

**二、⭐ #58 至少是三条，不是一条**

| # | 触发条件 | 后果 | 需要 ASR 抖动吗 |
|---|---|---|---|
| 1 | 重叠 **42–56 字** | 净增混入重叠残片（原 #58 拼接症状） | **不需要** |
| 2 | 重叠 **≥ 58 字** | 重叠**完全没识别**，整段重复保留（净增≈82/12） | **不需要** |
| 3 | 任意重叠 + 逐字抖动 | **新内容被部分删除**（`overlap=20,drift=1` ⇒ 净增 32/12 且丢失新内容） | 需要 |

⇒ 第 2 条的机制：`limit=40` 时 `lcsLen/limit ≈ 22/40 = 0.55 < ANCHOR_COVERAGE(0.6)`
　⇒ **两道闸都作用在截断后的窗口上** ⇒ 重叠越长越容易整体失效。
⇒ 第 3 条与第 1 条是**两个不同缺陷**（前者只删新内容，不产生拼接）。

**三、⚠ 订正 §634 的成因归因**

§634 的语料 `overlapLen = clamp(round(len*0.8), 30, 44)`，**上限 44 > 40**
⇒ 那一版语料**本来就横跨了 40 这条线**，把现象全归给「ASR 逐字抖动」**是错的**。
⇒ 原读数「36~64 字基本 2/2 复现」**不能当生产发生率用**。

**四、已测后果与未知发生率分开登记**

⚠ **回溯订正（2026-10-16）**：原文“在长会议里并不罕见”没有真人会议样本支持，撤回这条发生率断言。
触发条件（>40 字文本重叠）在合成实验中**不需要 ASR 噪声**，但生产占比仍未知。
主文档 §726 后续按生产构造链在合成输入上测到零 ASR 抖动仍删掉 54 字内容；该后果证据不因撤回“常见”而作废，但也不能推出真实会议发生率。
候选修法落点仍是 `dedupeSegmentTextSoft` / `lcsNetNew`，方案继续待属主拍板。

本次复核：三份 `meeting-dedup.test.ts` / `meeting-dedup-asr.test.ts` / `meeting-two-stage-asr.test.ts` 合计 **74 PASS / 0 FAIL / 0 SKIP**（18 suites，843.841ms）。这是当前相关测试基线，不代表 #58 已修复，也不是真 ASR 端到端验收。

**五、⚠ 留白（未测）**

生产里那段文本重叠**实际有多长仍未测到**。已排除一个来源：
`refetchFullTranscript`（`meeting-final-transcript.ts:101-151`）是
**整文件单次重转写、无分片** ⇒ 重叠长度不由分片边界决定，
而由 ASR 段边界抖动决定 ⇒ **这一格仍只能靠真人录音 + 真 ASR**（GATEWAY key 仍阻塞）。

---

### 📋 #62【§685.5 新登记】LCS 去重会把一段话切成**跨位置字串** —— Go 与 TS 都有，全仓零登记

⛔ **不并入 #58**：按 §637 的同一条纪律——**机制形状相同不等于同一个问题**。
#58 要解决的是「Go/TS 对齐」，#62 问的是「这套算法两边要不要一起改」。

**读数（跨语言双跑 80 对，见 §685）**

| | 段文本被拼成跨位置字串 |
|---|---|
| TS（无护栏 / 加护栏后） | **37/80 / 33/80** |
| **Go 真函数** | **32/80** |

⇒ 两侧命中率几乎一样 ⇒ **不是 Go/TS 对齐问题，是 LCS 去重这条设计的共同属性**。

**样本**（`insert_mid`，len=20）：

```
  next             = '今天下午三点开项目评的审会，请各团队负责人'
  展示出来的段文本  = '的人'        ← 既不是 next 的前缀，也不是后缀
```

⇒ 用户在界面上看到的一行是「**的人**」——语义不通，且不报错。
**但注意性质**：累积 body 完整（没丢内容）、delta 短（没多显示），
只是**这一行被切成了乱码**。⇒ 它既不是 §41 记的「误去重吃掉真实内容」，
也不是 #58 记的「插入原文不存在的拼接」——**是第三种**，全仓至今没有任何一处登记。

⏳ **待拍**：这种非连续切分该不该被算法禁止（例如「delta 必须是 next 的连续后缀，
否则放弃去重」）。

⚠ **两条边界必须一起说，否则就是我自己在 §682.1 犯过的错**：
① 读数来自**合成语料**（人工构造的增删/标点变异），真实 ASR 文本**未测**；
② 它**两侧都有** ⇒ 不是「对齐」问题，改它要同时动两侧，属于算法口径而非同步问题。

---

### ★★★ #58 再更新【§686 多段链实测】触发类**只在第 2 段可达**，第 3 段起**结构上不可能**

⛔ **不代拍。** 详见 `2026-10-06-recording-quality-fixes.md` §686。本节**未改动任何生产代码**。

§685 留下的缺口是「只跑了两段」。补跑 9 条多段链（3–6 段，含真实会议形态的 14 字短段链），
两侧都用**累积 committed**（Go `incremental.go:205` / TS `meeting-dedup.ts:326` 同形）。

**⭐ 主结论：#58 那一格要成立，必须同时满足三条**

1. 重复落在**第 2 段** —— 从第 3 段起 `committed ≥ len(段1)+len(段2) > max(单段长度)`，
   而 `next` 永远只是一个单段 ⇒ **`next === committed` 结构上不可能**。**9/9 条链验证成立**，
   且该推论与段数无关（`committed` 单调增长）。
2. 第 1 段长度落在 **(40, 56]** 这 17 个值里。
3. 第 2 段与第 1 段**逐字相同**（不是相似、不是重叠）。

⇒ ⚠ 第 3 条最苛刻：它要求 ASR 对**两个音频不重叠的相邻片段**产出**完全一样的字串**。
而 §634 已实测会议链相邻段不重叠、§649 折算下真实语料最长段约 29 字
——**连条件 2 都不满足**。

**⇒ 属主该怎么用这个结论**

| | §685 之后 | §686 之后 |
|---|---|---|
| #58 那一格在哪 | 「逐字相同且 41–56 字」 | **再加一条：必须落在第 2 段** |
| 真实语料可达性 | 「极低」 | **已有真实语料上三条件不可能同时成立** |

⚠ **诚实边界**：这是**结构可达性**，不是发生率——
它证明「不存在别的入口」，**不**证明「这个入口不会出现」。
要变成概率仍然只有真人录音 + 真 ASR。

**⇒ 另：9 条链里 0 条丢内容。** 所有分叉的长度差全部来自「重复段保不保留」这一口径，
且 **9/9 条 TS 正文仍以段 1 原文开头**（与 §685.3 的 `corrupt` 不变量一致）。
分叉形态：A41/A56/C 类是「TS 更短」（保留了 Go 保留的那份重复），
`B41_dup_at_3_5segs` 与 **`D_realistic_dup_at_4`（真实会议形态）是「TS 更长（冗余）」**。

---

### 📋 #63【§690 新增】`config.getEnv` **不 trim** ⇒ 任何 `POCKET_*` 的纯空白值都被当已配置

详见 `2026-10-06-recording-quality-fixes.md` §690。
**本条的下游一处已自行落地修复**（技术部分）：诊断页 `integration_status.go`
原会在 `POCKET_LLM_GATEWAY_API_KEY="  "` 时报「网关已配置」，
而 `defaultLLMGatewayState().APIKey` 实测为 `""` ⇒ **诊断页说谎**，
正是该函数自己注释里写着怕的那件事。端到端实测 + M1 变异 + 3 条新判据（M1 只打红两条空白格，负控保持绿）。

⚠ **纯空白 env 不是臆想的场景**：本仓 `datadir_test.go` 的 `TestResolveDataDir_TrimsWhitespace`
早就把它定为真实现象（「env 文件、脚本拼接很容易带上首尾空格」），
**dataDir 那侧跟上了 TrimSpace，gateway 这侧没跟上**。

⛔ **待拍（属产品口径，不代拍）：`config.getEnv` 要不要也 trim？**
- 现状：`getEnv(key, fallback)` 只判 `value != ""`，**不 trim**（`config.go:597-602`），
  被**上百处**调用。
- 全仓性 trim 会改变**每一个** env 的空值判定 ⇒ 行为变更。
- ⚠ 若选「都 trim」，本轮这处一次性对齐；
  若选「都不 trim」，那**该撤的是 `defaultLLMGatewayState` 里的 `TrimSpace`**。

⭐ **★ §691 已把 #63 的第二个具体后果钉出来（同样刻意不补，等你一次拍定）**
`POCKET_DISK_HOME` 也是两套口径：`config.go:369` 走 `getEnv`（不 trim）⇒
`cfg.DiskHome` 在 `"  "` 时是 `"  "`；而 `session_bundle.go:165` 的 `remapDiskHome`
走 `TrimSpace` ⇒ 当未设。消费点 `cmd/pocketd/main.go:646-648`
`if home == "" { home = os.UserHomeDir() }` ⇒ **纯空白时不会回落到真实 home**
⇒ `disk.NewWithHome("  ")` 扫不存在的目录 ⇒ 磁盘会话发现失效（有日志，非全静默）。
⚠ 为什么不单独补它：§690 那处是**同一个函数内自相矛盾**（修它不碰任何既定口径）；
这处是**跨函数不一致**，根就是 #63。逐个补消费方是 #63 该一次拍定的事
（补 3 个与补 30 个差别不大，但「要不要全仓对齐」必须一次做）。

#### 🟢 2026-10-16 补（§774）：**拍 #63 现在有测试兜底了**，且发现**第三兄弟的行为不一致**（新缺陷，已测出未修）

⭐ **先给你要拍板时用得上的**：本轮已给 `getEnv` / `getEnvInt` / `getEnvDuration` **补上单元测试**
（此前三兄弟里只有 `getFirstEnv` 有测试，而 `getEnv` 有 **98 处调用**、**零直接测试**）。
⇒ 你现在拍「全仓 trim」时，**会有测试立刻告诉你行为变了**，而不是改完才发现别处跟着变。
⚠ 那些测试是**固化当前行为**的（含缺陷）⇒ 拍 trim 后它们**会转红，那是预期的**。

⚠⚠ **但本轮查出 #63 旁边有一个它没覆盖的、更隐蔽的缺陷**：

| 函数 | 有 `TrimSpace`？ | 运维写下 `POCKET_SMTP_PORT=" 587"`（带空格）会得到 |
|---|---|---|
| `getEnv` | ❌ | `" 587"`（状态误报 —— **这就是 #63**）|
| `getEnvDuration` | ✅ | `587s`（正常）|
| **`getEnvInt`** | ❌ | ⛔ **静默变成 fallback `0`**，无 error、无日志、无 panic |

⚠ 影响 15 处调用，含 `POCKET_SMTP_PORT`（→0）、`POCKET_TIMEZONE_OFFSET_SEC`（→0，时区整个错掉）。
⚠ 与 #63 的差别是**性质**不是程度：#63 是「**状态显示错了**」，这条是「**行为被改了且完全不可见**」。

⛔ **这一条不在 #63 的范围内**（#63 只谈 `getEnv` 的 98 处调用，不含 `getEnvInt` 的 15 处），
⇒ 登记为**独立待拍板项**（要不要 log / 启动即校验 / 宽容解析），**不代拍**。
⇒ 但它已被**测试钉住**：变异 M1 给 `getEnvInt` 补上 `TrimSpace`（= 模拟缺陷被修）⇒ **2 条测试转红**，
　消息直接写明「**说明 getEnvInt 已补 TrimSpace ⇒ 请同步更新上面两条 ⭐ 用例**」。

📌 顺带：#63 原文里的行号 `config.go:597-602` **已漂移**（本轮删了一行 2026-07-26 就存在的**重复 godoc**），
本轮**未改** #63 原文以免改写历史。

⭐ **★ §691 同时抓到一个已修的第二个真缺陷**：
`POCKET_LLM_GATEWAY_ALLOW_PRIVATE`（安全审计 SEC-08 的「**高影响放宽开关**」）
在 `cmd/pocketd/main.go` 的启动日志里**另判一次**（`!= ""`），
而 `.env.example:298` 恰恰把它设成字符串 `"false"`（**非空**）
⇒ **照示例部署的每个实例**都会打出 `allow_private_hosts=true` 而闸门实际是关的。
⚠ 它不改行为（闸门一直是对的），是**状态误报**；但报的恰是运维想确认的那个状态。
已修：日志改为复用 `server.LLMGatewayAllowPrivate()`（唯一实现）+ 3 条判据
（真值表 18 格 / `.env.example` 真实默认值格 / 「只有一份实现」的形状判据）+ M1 变异。
  ⇒ 两条路互斥，**必须一起拍**。

⭐ 附带产出：§690 把 #60 的形状做成了一把可复用的量具
（**同一个规则字面量出现在 ≥2 个不同函数里** ⇒ 21 个候选，v1 因命中全是路由路径而作废）。
**余下 20 个候选尚未逐个复核**，其中
`POCKET_OPENCODE_CONFIG_TOKEN`（`adapter/opencode_config.go:93` / `server/llm_gateway_handler.go:575`）
与 `POCKET_DISK_HOME`（`config.go:369` / `server/session_bundle.go:165`）是两处实现，待查。

⭐ **★ §692 又钉出 #63 的第三个具体后果（本次是「行为级」，不是状态误报）**
`cmd/pocketd/main.go` 启动期（Phase C 的 `/api/embed`、`/api/llm/chat`）读的是
`cfg.LLMGatewayURL` / `cfg.LLMGatewayAPIKey`（`getEnv`，**不 trim**），
判定只有 `!= ""`；而 `/api/v1/llm/*` 网关代理路径走
`defaultLLMGatewayState()`（`TrimSpace` + `canonicalGatewayURL`）。
⇒ **同一个 env 被读出两个值**，实测 4 档：

| env | `main.go` 读到 | 网关代理路径 | 后果 |
|---|---|---|---|
| `"  "`（纯空白） | `"  "` / `"  "` ⇒ 判「已配置」 | 默认网关 / `""` ⇒ 判「未配置」 | **误进企业网关分支**（地址与凭据都空白），而设置页同时报「网关未配置」 |
| `" https://gw/v1 "` | 带空格原样 | trim 后同一值 | 两侧**指向不同地址、用不同凭据** |
| 未设 / 干净值 | 一致 | 一致 | — |

已修：抽出 `resolveGatewayBootstrap()`，地址按空白归一、
凭据直接复用新增导出 `server.LLMGatewayEnvAPIKey()`（唯一实现）
+ 4 条判据（3 档真值表 / 5 格同源 / **前提哨兵**断言 `getEnv` 仍不 trim）+ M1 变异打红 4 条。
顺带订正了同处一段与代码相反的旧注释（「未注入时用内置默认 key」——内置计费 key 早已移除）。

⭐ **★ §755 把 #63 的分母从「上百处调用」换成真实结构**（拍板成本估计据此重算）

你拍的是「要不要全仓 trim」，而此前登记里只有一句「**上百处调用**」。
全量普查（`find` 全部非测试 Go 源 + **剥注释后**计数）读数：

| 读取路径 | 处数 | trim 状态 |
|---|---|---|
| `getEnv(` | **97** | ❌ 不 trim |
| `getEnvInt(` | **14** | ❌ 不 trim（且失败方向相反，见下） |
| `getFirstEnv(` | **7** | ❌ 不 trim |
| `getEnvDuration(` | 5 | ✅ 函数内部已 `TrimSpace` |
| `strings.TrimSpace(os.Getenv(...))` | 18 | ✅ 已 trim |
| 裸 `os.Getenv(...)` | **26** | ❌ 不 trim（`pocketd` 主二进制 8 · `internal` 9 · `cmd/` 一次性探针 9） |

⇒ **不 trim 合计 144 处 / 已 trim 23 处 ⇒ 同一仓两种口径并存。**

⭐⭐ **但真正的结构比「144 处」好得多**：那 111 个 `getEnv`/`getEnvInt` 调用
是 **111 个不重复的 key、每个恰好 1 处**，且**几乎全部集中在
`internal/config/config.go` 的同一个 `Config` 结构体字面量里**
（`config.go:284`–`:369` 一段连续字段初始化）。
⇒ 📌 **要修的是 3 个函数（`getEnv`/`getEnvInt`/`getFirstEnv` 各自加一次 `TrimSpace`），
　  不是改 144 个调用点。** 改动面比原先登记的小两个数量级。

⛔ **两类失败的方向是相反的**（`getEnvInt` 的问题此前完全没登记）：

| 函数 | env = `"  "` 时 | 后果 |
|---|---|---|
| `getEnv("X","def")` | 当成**已配置**，返回 `"  "` | 下游拿到空白 ⇒ 误判已配置 / 在空白路径上操作 |
| `getEnvInt("X",42)` | `Atoi` 失败 ⇒ **静默回落 42** | ⭐ **用户设的值被丢弃，且无任何日志** |

实测证据（Go 1.x，`strconv.Atoi` 不 trim）：
`"42"`→42 ✅ · `"  42  "`/`" 42"`/`"42 "`/`"\t42\t"` **全部 err** ⇒ 走 `return fallback`。

⇒ trim 后两者**都**回落 fallback，方向一致 ⇒ **一次拍定可同时修掉这两种**。

⚠ **风险分层**（111 个 key 中真正危险的是**路径类 8 个**）：
`POCKET_DB_PATH` · `POCKET_DATA_DIR` · `POCKET_APK_DOWNLOAD_PATH` ·
`POCKET_KXMEMORY_{NOTE_CLASSIFY,EMAIL_CLASSIFY,DAILY_SUMMARY}_PATH` ·
`POCKET_DISK_SESSIONS_WORKSPACE` · `POCKET_DISK_HOME`
⇒ 空白 ⇒ **在空白路径上操作**（静默、可能落错地方）。
　其余是凭据类（**22**）/ 连接串类（**16**）/ 开关类（**12**）/ 其他（**53**，未逐个定性）
　—— 空白会**吵闹失败**（认证失败、连不上），低风险。
　**配平自检 53+22+16+12+8 = 111 ✅**
⚠ 分类器第一版把 `POCKET_MARKETPLACE_ROOT_PUBKEY`（**公钥内容**，非路径）误算成路径类
　（`_ROOT` 误吃）⇒ **已给分类器加负向断言修正**，不是「在文档里写一句已剔除」
　（那会让文档与量具读数永久不一致）。

⛔ **不代拍**：登记表与 `config.go` 一个字未改（`getEnv` 仍不 trim 的前提哨兵断言也在 §692 用例里钉着，
　改它会让那 4 条判据的前提失效 ⇒ **若采纳，需同批更新那 4 条判据**）。

⭐ **★ §692 撞出一条比上面所有缺陷都值钱的方法论读数**
`internal/config/config_test.go` 的 `TestProductionConfigValidation`
（8 格表：默认密钥 / 短密钥 / DevAuth / TLS / PG / origins…）
**一直是恒真的门**：base 自己就过不了校验（`POCKET_REDCLAW_ADMIN_URL`），
而该检查排在所有被测检查**之后** ⇒ **8 个用例全部因这同一条无关检查而失败，
表格从未证明过是「哪条」校验拒的**。
探针读数（同一进程打三种密钥）：32 个空格、32 个 `a`、`" " + 31a`
**返回的是同一个错误** ⇒ 顺带证实 **JWT 校验确实放行了 32 个空格**
（`len([]byte(secret)) < 32` 按字节数算，32 空格恰好 32 字节）。
两层都已修：`Validate()` 改按 `TrimSpace` 后长度；表补齐 base（`AuthLegacyOnly` 等 8 项，
取值与 `Load()` 默认一致）+ **前提哨兵**（先断言 `base.Validate() == nil`）
+ 每格 `wantErrContains` 断言**失败来自这一格想验证的那条**校验。
M1（退回未 trim）**只有空白那格红**，其余 8 格绿 ⇒ 修复专一，且**新表确实在区分**
（旧表在同一个变异下会 9/9 全绿）。
⚠ 补齐 base 的过程本身是证据：每补一项就露出一层恒真
（RedClaw → HTTP_PORT → TIMEOUT_MS → REDCLAW_TIMEOUT_SEC → RSS_HTTP_TIMEOUT），**共 5 层**。

### 📋 #64【§692 新增】embed/chat 分支要不要跟随网关的 obsolete(8782) 改写

**一句话**：`cmd/pocketd/main.go` 的企业网关 base 该不该整体改调
`defaultLLMGatewayState().BaseURL`。

**已修的边界**：§692 只对齐了**空白口径**（`strings.TrimSpace` + 空则回落默认），
凭据已改为复用唯一实现；**base 仍取自 `cfg.LLMGatewayURL`**。

**为什么不顺手一起改**：`canonicalGatewayURL` 除了 trim，还做了一件事——
把 obsolete 的本机 docker 默认 `127.0.0.1:8782` **强制改写成企业默认网关**。
若把 `main.go` 的 base 也换成 `defaultLLMGatewayState().BaseURL`，
embed/chat 就会**开始跟随这条 obsolete 改写**，
即「还指着本机 8782 的实例，其 embed/chat 会被改道到企业网关」。
这是产品口径，不是空白 bug。

**三选一**：
- **A（当前状态，建议先维持）**：只对齐空白，base 继续读 cfg。现状下指向 8782 的实例，
  embed/chat 仍走 8782，与 `/api/v1/llm/*` 走企业网关**不一致**（这是今天就存在的分叉，不是新引入）。
- **B**：base 也改调 `defaultLLMGatewayState().BaseURL` ⇒ 两类流量彻底同源，
  代价是 embed/chat 跟随 obsolete 改写（需确认这是期望的）。
- **C**：反向对齐——把 obsolete 改写从 `canonicalGatewayURL` 里摘掉，让两边都尊重显式配置。

**不代拍的理由**：A/B/C 改变的是「谁说了算」，属产品口径；
而 §692 的缺陷（空白导致误进分支、带空格导致两侧指向不同）已由 A 单独消除。


---

# 📌 附录 A：2026-10-15 证据审计批次（§701–§716）新增与变更的待拍板项

> 本附录汇总的是**本轮实测新发现**的项。
> ⚠ **其中 3 项已在本轮被实测推翻或落地，不再需要拍板**（见 A.0）。

## A.0 ⛔ 本轮**已自行落地**或**已被推翻**（不需要拍板）

| 原表述 | 现在的实测 | 出处 |
|---|---|---|
| 「`npm ls` 报 **41 条 UNMET DEPENDENCY** ⇒ 影响 native/真机路径」 | ⛔ **41 个依赖全部已装、版本匹配，缺失 0 个**；npm 把「版本比下限新」判成 missing | §709 |
| 「起测试 PG 解锁 **537 条（13.4%）**，本仓性价比最高的外部输入」 | ⚠ **确定收益 181 条（4.5%）**；623 是上界；`email`+`server` 的 428 条还需另外 4 类环境变量 | §710 |
| 「Go 侧唯一红是 `TestMeetingIngestRequestFieldsDrift`」 | ✅ **实为 3 条**，另 2 条此前从未报过；已全部归因 | §710–§712 |

## A.1 🔴 `adapter_pi_test.go` 的两层墙钟：**提预算是错方向**

**证据齐备（§711 / §713 / §715）**：
- 两条 FAIL 是 `TestPiAdapter_SendPromptHappyPath` / `TestPiAdapter_ProviderErrorExitZero`；
  失败原文是 `agent request timed out: context deadline exceeded`，耗时**精确 10.00s / 10.02s**。
- ⭐ **它们根本不测超时**：用 `FAKE_PI_OUTPUT`（本地 fixture）+ `/bin/sh` 假脚本（**不编译不下载**），
  期望的终态是 `AGENT_UPSTREAM`（provider 错误），不是 `AGENT_TIMEOUT`。单包跑 **0.17s 通过**。
- 并行度对照（同机同码，只改 `-p`）：`internal/agent` 包级耗时
  **4.5s（单包）→ 5.4s（`-p 1`）→ 15.9s（`-p 8`）→ 29.9s（`-p 16`）→ 37.2s（CI 的 `-race`）**
  ⇒ **累计 6.9 倍**，而墙钟预算是 **10s** ⇒ **CI 下余量仅 27%**。
- ⚠ **还有第二层**：`collectEvents` 自带 `time.After(5 * time.Second)`，
  与 `ctx` 的 10s 叠加 ⇒ **只提 `ctx` 那一处不够**。
- 代码注释自证：**这两条在 Linux CI 上是真实执行的**（只有 Windows 跳过）。

**三选一**：
- **① 提高预算（10s → 30s/60s）**：⚠ **建议排除**。它不测超时、fixture 全本地、正常路径 0.17s
  ⇒ 提预算只是把阈值推远，**掩盖「并发拖慢」这个真信号**。
- **② 改成事件同步**（`collectEvents` 等「要的事件到齐」而不是「等墙钟」）：✅ **方向成立**，
  但会**改掉测试的失败语义**，属工程口径。
- **③ 登记为 flaky**：可作兜底，但它连「超时」都不是 flaky，是**负载下的偶发失败**；
  登记会掩盖「CI 余量 27%」这个真问题。

**不代拍的理由**：②③ 改的是测试的失败语义，属工程口径决定。

## A.2 📋 §96.3 等「历史诊断段」是否统一加**时间戳 / 修复标记**

**背景（§707 / §708 实测）**：全仓文档核验 1355+ 条引用，
只找到 **1 条**「结论失效」——文档称 `CreateScoped` 把 `Status` 硬写 `"recording"`，
而代码里已无此行为（`grep -rn '"recording"' backend/internal/chat_summary/` **0 命中**）。

**但它不是「文档写错了」**，它是 §96.3「根因是三件事叠在一起」的第 1 点证据。
逐条核该节三个点：

| §96.3 的点 | 实测 | 判定 |
|---|---|---|
| 1. `CreateMeetingRequest` 只有 1 个 json tag，其余 9 键静默丢弃 | **10 个字段全带 tag**（`id`/`title`/`location`/`participants`/`startedAt`/`durationMs`/`summary`/`refinedTranscript`/`noteId`/`status`） | ⛔ **已修复** |
| 2. 服务端永远自己发号、忽略请求里的 `id` | `types.go:51-62` 注释逐字写着「ID 是客户端提供的会议 ID（可选）」；`nextMeetingID` 降级为「客户端没带 id 时」的兜底 | ⛔ **已修复** |
| 3. `syncMeeting` 返回 `Promise<void>` + 前端从不对 `/api/meetings` 发 GET | `Promise<void>` 仍在；全仓该路径**只有 1 处调用且只有 `POST`**（⚠ 文档写「4 个 POST」也略有夸大） | ✅ **仍成立** |

⇒ ⇒ **处置方向**：这类段落该**加时间标注**（「截至 §96.3 当时」/「已于 ×× 修复」），
　**不是**逐字改正内容（改正会**破坏论证链**）。
⛔ **不代拍**：是否对历史诊断段统一加标记，属**文档体例**决定。

## A.3 📋 `byteSliceAllowlist` 用**行号做 key**

**实测（§705.5）**：该表里记着 `"file.go:1191"`，而 `file.go` **该文件已不存在** ⇒ 该条目**已失效**。

⇒ 行号会随代码演进漂移 ⇒ **这类 key 天然会失效**。
⛔ **不代拍**：是否把 key 从行号改成符号名，属**表结构**决定。
（与 A.4 同源，都属「引用体例」。）

## A.4 📋 文档里的**短 basename 引用**要不要强制补目录

**实测（§706.4）**：12 条引用因 basename 不唯一而**指向了错误文件**，去重后只有 7 组：

| 文档写的 | 符号真实所在 |
|---|---|
| `discovery.go` | `backend/internal/stt/discovery.go` |
| `main.go` | `backend/cmd/pocketd/main.go` |
| `types.go` | `backend/internal/learning/types.go` |
| `client.go` | `backend/internal/llmgateway/client.go` |
| `store.go` | `backend/internal/notes/store.go` / `backend/internal/email/store.go` |
| `runtime.ts` | `frontend/src/lib/shell/runtime.ts` |

⇒ ⭐ **可核对的分界线**：**长文件名的引用 100% 正确**，错的全集中在 **≤12 字符的短名**。
⛔ **不代拍**：是否要求短 basename 补全目录，属**文档体例**决定。

## A.5 📋 `knownDroppedTags` 收窄的**残留确认**

⚠ **本轮已按「技术性修复」自行落地**（§714，变异双向验证通过，Go 全量 0 FAIL）：
`knownDroppedTags`(7→0) / `knownOverwrittenFields`(1→0)。

**但两件事需要属主确认**：
1. ⚠ **负控从 3 臂变 2 臂**（「少一个键」臂在空 base 下会 panic，已用 `t.Skip` 如实跳过）。
2. ⛔ 若属主认为两张名单**应保留非空**（例如仍想登记某几个字段作为「已知云端不全」），
   需给出**具体键名**——但当前实测差集为**空**，保留任何非空项都会立刻报红。

## A.6 📋 门禁「失败即停」造成的 **43 项遮蔽**

**实测（§702）**：门禁是「失败即停」；因 item 3（`test:all`）红，
**item 4–46 共 43 项（93.5%）在当前代码上一次都没跑过**。
⇒ 逐项单独跑：**43 / 43 全绿**。
⇒ ⚠ 这**不是缺陷**（失败即停是设计意图），但它意味着
「gates 红」≠「其余部分有问题」，也 ≠「其余部分没问题」——
在 item 3 红的这段时间里，那 43 项的状态是**「未知」**。
⛔ **不代拍**：是否调整失败即停策略（如「跑完全部再汇总」）属**工程口径**。

## A.7 📋 前端 ingest 那条二选一：**两条缺口缺一不可**

**实测（§703）**：`ingest-behavior.test.mjs` 的打包失败**有两个独立缺口**：

| 只做这一条 | 结果 |
|---|---|
| 只加 `mainFields` | ⛔ 撞第二层 `[🍍] getActivePinia()`（12 fail） |
| 只补 `stores/auth` stub | ⛔ 仍卡在 `pinia.mjs:7` |
| **两条一起** | ✅ 打包通过 |

⇒ ⭐ **请把这两条当成一条决策看**，分开拍会拍出坏结果。
⇒ 另：同型的另 3 个测试（`note-todo-behavior` / `meeting-todo-readback-day` /
`meeting-todo-persist-idempotency`）**没有**「stub 会把 pinia 拖进包」的注释，
也没有守卫 ⇒ 是否补注释 + 加「入口不得引入值引用 pinia」的守卫，属**工程口径**。


## A.8 🔴 `server_meeting_ingest.go` upsert 分支丢 `status`：**这不是不一致，是有理由的不对称——但它有一条可达的用户可见后果**

> 本项取代 §719.6 的原始登记（那一条定性错了，见下）。

### A.8.1 先订正 §719.6

§719.6 当时写的是「upsert 分支不回填 status，与 `CreateScoped` 行为不一致」。
读完上下文后发现**低估了现有设计**：

- `server_meeting_ingest.go:118-121` 在分支正上方写着书面理由：
  「这不是『覆盖写』，是**同步同一份事实**…两次 POST 的 title 同源，所以这里只改 title，
  不碰任何另一路才写的字段。」
- `meeting_sync_id_upsert_test.go:197-202` 守着「二次同步不许把 `status` 从 `refined` 冲掉」。

⇒ 它不是没人想过的不一致，是**刻意的、且有理由的不对称**。

### A.8.2 但不对称确实有代价（实测，不是推测）

临时探针（量完即删，留档 `/Users/xutaohuang/.minimax/tmp/openpocket-s721/`）：

```
读数1 create 分支：请求带 status=refined ⇒ 行内 status="refined"
读数2 ①无status建行 ⇒ status="recording"；②带status=refined重同步 ⇒ status="recording"
```

⇒ **行一旦存在，客户端此后每次同步带的 `status` 一律被丢弃**，客户端**无法修复**这一行。

### A.8.3 ⛔ ⛔ **本小节已被 §723 推翻，不要照它决策**

原标题：「生产里会发生什么（逐跳源码核对）」。

⇒ 当时断言「精校没到达服务端 ⇒ 云端行停在 `recording` ⇒ 两设备永久不一致」。
**§723 逐跳反查后这条链在第 ② 跳就断了**：精校请求抛错时
`meeting-recording-finalize.ts:231` 的 catch **跳过整个 `ingestMeetingArtifacts`**，
`refineStatusFor` 的输入压根不存在 ⇒ 第二次同步不发生 ⇒ upsert 分支没被走到。

⇒ 原文保留仅供对照。完整证据（3 条）见设计文档 **§723**。

| 跳 | 位置 | 事实 |
|---|---|---|
| ① | `meeting-ingest.ts:314` `syncMeetingMetadata` | 停止录音时的 fire-and-forget 那条 POST，**先于**精校发出，传 `status: meeting.status`（新建时 `'recording'`） |
| ② | 建行走 create 分支 | 采纳请求值 ⇒ **云端行从一开始就是 `recording`** |
| ③ | `meeting-recording-finalize.ts:187-190` | `ingestMeetingArtifacts` 只包在 `try` 里，**不以精校成功为前提** |
| ④ | `meeting-ingest.ts:117,147` | 状态取 `refineStatusFor(...)`，可以是 **`'refine-failed'`** |
| ⑤ | upsert 分支 | 该字段被丢弃 ⇒ **行停在 `recording`** |

⇒ ⭐ **净后果**：精校请求**没到达服务端**时（超时/5xx/断网——正是 §711/§713/§715 追的那类失败），
　本地已提示「精翻未生效」，**云端那一行却永远显示「录音中」**。
　两台设备同一条会议状态**永久不一致**，且无任何报错。

⚠ **诚实边界**：机制已**实测**（读数 2），时序已**逐跳核对**，
　但**整条 HTTP 序列未端到端跑过**（阻塞：GATEWAY key 缺失）⇒ 末步是源码推断。

### A.8.4 ⭐ 决定性读数：**①和②在现有整套测试里不可区分**

变异实验 M-A（仅实验，已还原，SHA `021784045355…d229` 与原文件逐字一致）：

给 upsert 分支补上 `if st := strings.TrimSpace(req.Status); st != "" { existing.Status = st }`，

```
会议域定向 6 条测试  → ok 3.415s
internal/server 整包  → ok 19.258s   0 FAIL
```

⇒ ⭐ **现有的承重门（`:197`）并不区分这两个选项** ——
　在它构造的场景里（sync①→refine→sync②，第二次带 `status=refined`）
　回填与不回填的结果**都是 `refined`**。

⇒ 这条读数有两个用处：
1. **选②不需要拆测试**（不会撞红任何现有门）；
2. ⚠ 反过来，**不管选①还是②，现有测试都不会替你守住这条语义** ——
   真要落地任一边，都得**补一条能区分二者的门**。

### A.8.5 请拍板（**已改写：原来的三选一作废**）

> ⛔ 原三选一（①维持 / ②回填 / ③折中）**建立在「有可达后果」这个前提上，
> 而该前提已被 §723 推翻** ⇒ 三个选项之间没有实质取舍了。

**§723 的净结论：当前生产路径上，这个丢弃是良性的。**

它之所以安全，靠的是两条**没有任何门守着**的隐式不变量：

| # | 不变量 | 谁在依赖 |
|---|---|---|
| ① | 已存在行的终态**只由精校端点写**（`server_meeting_ingest.go:202-205` 用 `refineDegraded` 分流） | upsert 分支不回填，是安全的 |
| ② | 两侧降级判定**等价**（Go `refine_fallback‖refine_rejected` vs 前端同两条 + `serverRefined==null`，后两条因 `refined_transcript` 无 `omitempty` 而永不触发） | 前端算出的值与服务端已写入的一致 |

⇒ **真正该拍的只剩一个工程口径问题**：

| | 方案 | 说明 |
|---|---|---|
| **①** | **不改行为，把这两条不变量写成注释/门** | 行为正确，风险是**静默失效**：有人给 `refined_transcript` 加 `omitempty`，或只改一侧谓词 ⇒ 安全性消失而**现有测试一条都不红**（§722.4 M-A 实测：整包 19.258s / 0 FAIL） |
| ② | 完全不处理 | 维持现状的裸奔状态 |

⇒ ⚠ ③（跨边界 fixture 断言，仿 `meeting-response-keys.test.ts` ↔ `meeting_response_contract_test.go`）
　 是①里最彻底的做法，但成本最高。

⇒ 我的读数：**建议 ①，且只补文档 + 一条窄门**，因为这个安全性目前**完全靠默契撑着**。
　**但这是工程口径，不代拍。**

⚠✅ **§724 已把 §723.3 那一格补成实测**：两侧各跑一遍真函数，
　7 个真实形状全一致、**3 个负控全被抓出** ⇒「等价」现在不是读代码读出来的，是测出来的。

⇒ 多一条**支持③的读数**：跨边界探针**已经证明跑得起来**，不是纸面设想。
　③ 的成本只剩「固化成常驻测试 + 让夹具由 Go 测试生成」，**不涉及未验证的技术风险**。

⚠ 覆盖面提醒：§724 枚举的是 10 个夹具，**足以支撑「今天是对的」，
　不足以支撑「将来任何改动后仍是对的」** —— 后者正是这道题要买的东西。


---

### 🔴🔴 #58 关键更新【§726 实测】**生产为什么会命中：回执粒度 ≫ VAD 分段粒度**，且**零抖动下已实测删掉 54 字**

⛔ **不代拍。** 详见 `2026-10-06-recording-quality-fixes.md` §726。请与 §725、§685 一起读。

§725 留了一格「生产重叠多长未测」。本节用**生产真实构造链**测掉了：
`original(VAD 分段) + full receipts → attributeFullTranscript → dedupeSegments`。

**一、决定性变量是 G = 每个回执覆盖几个 VAD 段（整张表 drift=0，无 ASR 抖动）**

| G | 各段长度 | 相邻重叠(最大) | 去重删掉 | 落点 |
|---|---|---|---|---|
| 1 | 30,30,30,… | 0 | 0 | 干净区 |
| 2 | 60,30,60,30,… | 30 | 0 | 干净区(≤40) |
| 3 | 90,30,30,90,… | **57** | 0 | ★ §725 路径1 |
| 4 | 120,30,30,30,… | **57** | **54 字** | ★ 拼接 + **删内容** |

**二、生产的 G 是多少（两侧源码常数）**

| 侧 | 常数 | 段长 |
|---|---|---|
| 实时 VAD | `vad-segmenter.ts:37-41` `minSpeechMs:400` / `maxSpeechMs:20_000` | **0.4–20 秒** |
| 整段重转写回执 | `stt/full.go:80-87` `defaultSegmentSec=25` / `maxSegmentSec=25` | **~15.8–25 秒**（实测 79s 录音切 5 段，均 15.8s） |

⇒ G ≈ 0.8…40；按每段至少 3 秒算，**典型 G ≈ 5** ⇒ **G≥3 是常态**。

**三、⭐ 严重性要上调 —— 不是因为拼接，是因为静默删内容**

- §725 的拼接物：至少还留着「原文 + 残片」，用户能看出不对；
- **§726 的 G=4：在完全无 ASR 抖动的输入上删掉 54 字**（240 → 186）。
  这正是 `meeting-dedup.ts` 头部自己写的那句
  「**误去重的代价是丢掉真实内容，用户永远发现不了**」——
  **静默、无提示、用户无从发现。**

**四、⚠ 必须一起记的前提（否则会高估）**

整条链只在**整段重转写被采纳**时成立
（`meeting-refine-plan.ts:100-109` 的 `adopt.adopt && full.text`）；
否则直接用原始 VAD 分段 ⇒ G=1 ⇒ **不发生**。

⇒ 诚实的严重性判断：
- 机制**已证可达**（纯函数，零噪声可复现，生产 G 常态 ≥3）；
- ⚠ **采纳率未测** ⇒「多大比例的会议会命中」仍是未知数
  （依赖 `shouldAdoptFullTranscript` + §50 的 webm→wav / SplitWAV；需真人录音 + 真 ASR）。

**五、对三选一的影响**

- ③「改 Go 对齐 TS」的风险**上升**：TS 侧现在已知会**静默删内容**，
  让 Go 跟着删等于把一个静默缺陷推广到后端。
- ①「TS 补齐 Go 护栏」**不足以覆盖** §726 这条（它护的是 `identical` 全重复场景，
  不是 G≥3 的粒度不匹配）。
- ⇒ ⚠ **三个选项里没有一个直接针对「G≥3 ⇒ 删内容」** ——
  这可能意味着 #58 的选项表**本身漏了一格**。是否补第 ④ 选项，属产品口径，**不代拍**。

**六、量具纠错（如实记录）**

§726 第一版语料给 6 段套了同一模板、共享 20 字公共尾巴
⇒ 量出「相邻重叠 0 却删掉 83/150 字」的荒谬读数
⇒ **又踩了 §634 警告过的自相似坑**。v2/v3 换成逐字互不重复的语料后才可用。
⚠ 这已是同一类错误的第二次（§634 第三版、§726 v1）——
**自相似语料是这个模块最高频的量具陷阱。**


---

### 🔴 #58 再更新【§727 实测】**「静默删内容」被推翻：分母用错了**，真实症状是重复没清干净

⛔ **不代拍。** 详见 `2026-10-06-recording-quality-fixes.md` §727。**请与 §725 / §726 一起读。**

**一、错在哪**

§726 那句「G=4 删掉 54 字 ⇒ 静默丢内容」用的是 **`baseSegments`(420 字) 当分母**，
而 420 这个数**本身含 180 字重复**（粗回执覆盖 4 个分段 ⇒ 3 个桶回落本地 ASR 文本，
与回执文本描述同一段音频）。

⇒ 「删掉 54」= 去重**从 180 字重复里只削掉约 30%**。

**二、`planRefine` 端到端读数（真函数，`adopted=true` / `adoptReason=ok`）**

| 形态 | baseSegments | refineSegments | 用户真正说的 | 相对真值 |
|---|---|---|---|---|
| G=1 | 240 | 240 | 240 | ±0 |
| G=2 | 360 | 360 | 240 | **+120（+50%）** |
| G=4 | 420 | 366 | 240 | **+126（+52%）** |

⇒ **8 段 refineSegments 全部能在整段重转全文里原样找到** ⇒ **一个字都没丢**。

**三、⭐ 净结论（当前正确版本）**

> 用户可见的后果不是「丢内容」，而是**同一段音频被写了两遍**，
> 且这个重复**只被削掉约 30%，约 52% 原样进了精校/摘要/推荐**。

⇒ 这与 `meeting-dedup.ts:306-310` 头部写的是**同一件事**
（「重复文本…会顺着 segments 流进 LLM」）。

**四、两次修正的净账**

| 时点 | 说法 | 状态 |
|---|---|---|
| §725 | 拼接物进精校提示词 | ✅ 成立（drift=0 可复现） |
| §726.4 | ⭐ 静默删内容，最严重 | ⛔ **已推翻**（分母用错） |
| §727 | 重复约 52% 进入下游，去重只削约 30% | ← **当前** |

⇒ **严重性方向不变（仍需处理），但症状描述换了**：不是「少了」，是「多了、没清干净」。

**五、⚠ 新增的一条通用纪律（本轮最值得带走的一条）**

**「删了 N 字」只有在分母是「用户真正说的内容」时才有意义。**
拿一个**已经含重复的中间产物**当分母 ⇒ 会把「削重复」读成「丢内容」，
而且读数越干净越可信（负数、正数都自洽）⇒ **不会触发「自相矛盾」那条自查**。

⚠ 这类错误比「输出自相矛盾」更隐蔽：它**不会自己暴露**。

**六、留白**

- ⚠ 真实 ASR 下重复率取决于归属命中率与回执切块，**未测**。
- ⚠ 「8 段全部能在全文里原样找到」只在**我的语料**上成立
  （回执文本就是按分段拼的）⇒ 不外推到真实回执。


---

### 🔴🔴 #58 第三次更新【§728 逐段实测】**修正再被推翻：内容丢失是真的**，且与重复**同时**发生

⛔ **不代拍。** 详见 `2026-10-06-recording-quality-fixes.md` §728。**请与 §725/§726/§727 一起读。**

**一、逐段现场（G=4）**

| 段 | 原长 | 现长 | Δ | 现象 |
|---|---|---|---|---|
| 0、4 | 30+30 | **120+120** | **+180** | 吸收整块回执 ⇒ 重复膨胀 |
| 1、5 | 30+30 | **3+3** | **−54** | 被削成三字残片（「过上线」「户设计」）⇒ 真实内容丢失 |
| 合计 | 240 | 366 | +126 | **净正数把第 ② 项完全掩盖** |

⇒ §726 说的「删掉 54 字」**是对的**（第 1、5 段各 30→3）；
　§727 说的「+126 重复」**也是对的**。
　⇒ **我上轮「只可能是重复、不可能丢内容」的推论是错的** —— 净正数不代表没有负项。

**二、根因**

一个覆盖 4 个分段的回执被挂到**其中一个**桶，其余 3 个桶回落各自 30 字本地文本
⇒ `dedupeSegments` 判定邻段「是重复」⇒ 削到 3 字 ⇒ 剩下的是**噪声残片**，进精校毫无价值。

**三、⚠ 方法论：这条是「弱读数被当成强证据」，两次，方向相反**

- §726：弱读数（净长比较）被当成强证据说成「丢了内容」——**方向对、依据错**；
- §727：弱读数（子串存在）被当成强证据说成「没丢内容」——**方向错、依据也弱**。

⇒ ★★★ **判据的强度要匹配结论的强度**：
「能在真值里原样找到」只支撑「没被编造」，**对「段被掏空」完全失明**
（被削到 3 个残字仍是全文子串）。
⇒ 要支撑「没丢内容」，必须**逐段长度对照**（原长 vs 现长）。

**四、当前正确版本（供拍板）**

> G≥3（生产典型 G≈5）时同一场会议会**同时**出现：
> ① 少数段膨胀成 120 字整块回执（重复）
> ② 相邻段被削成 **3 字残片**（真实内容丢失）
> 净值为 +126，**净数会把第 ② 项完全掩盖**。

⇒ **③「改 Go 对齐 TS」风险最高**（会把「削成 3 字残片」推广到后端）；
　① 的护栏只覆盖 `identical` 全重复，**盖不住 G≥3**。

**五、留白**

- ⚠ 「两个效应同时出现」只在 **G=4** 这一格实测到；G=3 只量到重叠 57、删除 0
  ⇒ **「G=3 是否也产生残片未测」**，不外推。
- ⚠ 真实 ASR 下残片的出现概率取决于归属命中率，**未测**。


---

### ✅ #58 **最终读数表**【§729 收敛测量】G=1..8 一次量全 · 判据=逐段原长vs现长 · 含负控

⛔ **不代拍。** 详见 `2026-10-06-recording-quality-fixes.md` §729。
⚠ §725/§726/§727/§728 四轮各换一层/一分母，结论翻了三次 —— **本节是收敛版，请以这张表为准**。

**为什么需要收敛版**：前四轮的判据分别是「净长比较」「子串存在」「逐段长度」，
每一种只能看见一部分现象。本轮固定成**唯一能同时看见「膨胀」与「掏空」**的形式。

**读数表（N=12，每段 30 字，真值 360 字；负控：G=1 必须全零 ✅ 通过）**

| G | 原→现 | 净增 | 膨胀段 | 残片段(≤10字) |
|---|---|---|---|---|
| **1** | 360→360 | **+0** | — | — |
| 2 | 360→540 | +180 | 6 段(30→60) | — |
| **3** | 360→600 | +240 | 4 段(30→90) | **—（无残片）** |
| 4 | 360→549 | +189 | 3 段(30→120) | 3 段(30→**3**) |
| 5 | 360→630 | +270 | 3 段(30→150) | — |
| 6 | 360→612 | +252 | 2 段(30→180) | 2 段(30→**6**) |
| 7 | 360→660 | +300 | 2 段(30→210) | — |
| 8 | 360→612 | +252 | 2 段(30→240) | 2 段(30→**9 / 3**) |

**三条结论**

1. ⭐ **膨胀在 G≥2 必然发生**（G=2..8 每一格都有），重复率实测 **+50%~+75%**。
2. ⚠ **残片不是单调的**：G=4/6/8 出现，G=2/3/5/7 不出现
   ⇒ **推翻了 §728.6 的留白**：「G=3 是否也产生残片」—— **已测：不产生**。
   ⇒ 残片取决于**段如何分桶**，不是 G 越大越糟。
3. 生产典型 **G≈5** 恰好落在「只膨胀不残片」那格，但 G 是连续变量，
   **4/6/8 同样都是真实可能值** ⇒ **不能只报一种症状**。

**最终版结论**

> 整段重转写被采纳且**回执切块比 VAD 分段粗（G≥2，生产常态）**时，精校提示词里**同时**混入：
> ① **膨胀**（段扩成整块回执，30→60…240，重复率 +50%~+75%）
> ② **残片**（段被削到 3–9 字，是噪声不是内容，G=4/6/8 实测）
> 净值为 **+180~+300**，**净正数会把第 ② 项完全掩盖**
> ⇒ **任何只看总字数的判据都会漏掉内容丢失那一半。**

**对三选一**：③「改 Go 对齐 TS」风险最高（把削残片推广到后端）；
① 护栏只覆盖 `identical` 全重复、**盖不住 G≥2 的膨胀**；
⇒ **三选项仍无一直接针对 G≥2**，属**未代拍**的产品口径。

**留白（勿外推）**
- 「残片出现在 4/6/8 而非 3/5/7」这个**奇偶形状依赖 N 与段长的算术**，
  真实 ASR 下分布**未测**；
- 判据阈值（膨胀>1.5×、残片≤10 字）是**我定的**，换阈值会改变归类，
  但不会改变 **G=1 全零**这个负控；
- **整段重转写的真实采纳率仍未测**（需真人录音 + 真 ASR）。


#### ⛔⛔⛔ #58 第四次更新【§762 实测】上面那句「**生产典型 `G≈5`**、`G≥2` 是**生产常态**」被**真人录音实测推翻**：`G = 0.600`

⚠⚠⚠ **这是回溯订正**：请**不要**照上面 §729 的「生产常态 `G≥2`」做决策。

`G` = 每回执覆盖几个 VAD 段 = `N_base ÷ N_receipt`。§729 的 `G=1..8` 扫描里
`G` 是**人为设定的**参数，**没有一个值来自真机**。
§762 用**真源码 + 真人录音**把两端都测了：

| 项 | 读数 | 来源 |
|---|---|---|
| `N_base`（本仓 `VadSegmenter`） | **6** | 夹具实测端点（⚠ §593.5 **离线复刻**，夹具自标「⛔ 不可当真机量引用」）|
| `N_receipt`（`stt.SplitWAV`，`segmentSec=25`） | **10** | 真源码在真人 wav 上跑出来的 |
| ⇒ **`G`** | **0.600**（剔掉实质静音回执后 `0.750`）| |

⛔ §728/§729 假定的 `G` 区间是 **0.8 … 40**、典型 **5**
⇒ **实测低于那个下界**，`0.750` 也只是刚到下界。

**于是 §729 的三档结论要重读一遍**：

| §729 结论 | §762 之后 |
|---|---|
| 「`G≥2` 必然发生膨胀」 | ⚠ **真实形态 `G=0.600` 不在这个范围** ⇒ 与膨胀机制无关 |
| 「残片出现在 G=4/6/8」 | ⛔ 同上，真实形态扫不到 |
| 「生产典型 `G≈5`」 | ⛔ **被实测推翻**（`0.600`）|

**但 `G<1` 有另一个方向的后果，§729 没测过**（`G=1..8` 全在 `G≥1`）：

| 新读数（真几何，与文本无关） | 值 |
|---|---|
| 跨**多个** VAD 段的回执 | **0/10** |
| 一个 VAD 段都覆盖不到的回执 | **3/10** |
| 每个短 VAD 段只拿到回执时间窗的 | **32.4% ~ 64.9%** |

⇒ ⭐ **§729 的「单段膨胀 2.56 倍」机制在这套几何下触发不了**（它要求「一条回执盖多个 VAD 段」）；
　取而代之的是**短段塌缩**：v1 只有 2.54s，落在一条 7.85s 的回执里 ⇒ 只能拿到 **32.4%** 的时间窗。
　用真 `planRefine` 跑（合成回执文本 + 真几何）：`adopted=true / reason=ok`，
　逐段 **42 → 51 / 4 / 36 / 31 / 53 / 46** 字 ⇒ **v1 从 42 字塌到 4 字**。

⚠ 三点保留，**别把 `G=0.600` 当成 #58 的发生率**：
① `N_base` 不是真机量（离线复刻端点）② 回执文本未测（需 GATEWAY key）
③ 单样本（1 文件 / 4 人 / 56.86 秒，夹具自标「不可外推」）

⚠⚠ 另：**§729 的「只看总字数会漏掉丢失那一半」结论仍然成立**，
　但 §762 实测到**另一处只看总量的漏洞**：
`shouldAdoptFullTranscript`（`meeting-final-transcript.ts:216`）**只比总量**
（`fullClean.length < segText.length * 0.5`），**没有逐段下限**
⇒ 短段塌到 32% 时间窗占比**照样被采纳**。
⚠ 这与 #58 **不并案**（机理不同：#58 是去重算法，这是归因的时间窗切片）。

### 🔴🔴 请拍板③【新】【§762.3】`IsUsefulSegment` 用**时长代理**判「纯静音」 ⇒ 真人录音上 **11%~30% 的回执是纯静音却照常计费**

⛔ **不代拍**（属工程口径：涉及计费行为变更 + 已有判据方向被钉住）。
详见 `2026-10-06-recording-quality-fixes.md` §762.3。

**缺陷**：`full.go:446` 的注释写「**跳过纯静音的碎段**：不送上游（省一次计费调用）」，
而实现的判据是 `IsUsefulSegment()` = **时长 ≥ 1.0 秒**（`full.go:159`）
⇒ **判据与意图不同轴：静音越长，越容易被当成「值得转写」而送上游。**

**读数**（真 `frameRMS` + 真 `silenceFloorSquared`，与切段同一套判据）：

| 音频时长 | 回执总数 | 整段纯静音且被放行 | 占回执比例 | 其中 ≥90% 静音 |
|---|---|---|---|---|
| 56.9s | 10 | 2（4.40s） | **20.0%** | 1 |
| 113.7s | 19 | 3 | 15.8% | 2 |
| 284.3s | 46 | 6 | 13.0% | 5 |
| 909.8s | 145 | 17 | 11.7% | 16 |
| 1819.5s | 289 | 33 | 11.4% | 32 |
| 3582.2s | 568 | 64（171.80s） | **11.3%** | 63 |

⇒ 一小时会议约 **64 次白花调用**（比例收敛到约 **11.3%**）。
⚠ 该表是**真人素材重复拼接**，测的是 `SplitWAV` 对**该素材**的行为，
⛔ **不可外推到真实会议**（真实会议的静音形态未测）。

**已有判据的状态**：`full_test.go:523-528` 早就记着变异结果
「`minUsefulSegmentSec: 1.0 → 0` ⇒ **该阈值零判据**」，
⚠ 即**「滤得更松」那侧没有门**。§762 的真人读数独立印证：568/568 全放行，该阈值**完全空转**。

**选项**

| # | 方案 | 代价 / 风险 |
|---|---|---|
| **①（推荐）** | 让判据与意图对齐：复用 `SplitWAV` **已经在算**的静音信息，给 `Segment` 加一个 `AllSilence` 标记 | 会改变计费行为；`TestTranscribeFullSkipsTooShortSegments`（`full_test.go:523`）钉的是「**高于**阈值 ⇒ 必须打上游」，改判据后**要确认它的夹具不是整段静音**，否则门会红 |
| ② | 维持现状（时长代理）| 继续按 11%~30% 白花调用 |
| ③ | 只把 `minUsefulSegmentSec` 调大（如 5s）| ⚠ 治不了本：门槛抬高只会让**更长**的静音被放行，且会丢掉真实的短句 ⇒ **不建议** |

⚠ **共同未测**：ASR 对整段静音的实际返回（**空文本 vs 幻觉**）—— 需 GATEWAY key。
　若 ASR 会**幻觉出内容**，那这 11%~30% 就不只是浪费，还会往会议纪要里塞假文本（**风险更大**）。


## A.9 📋 §612.2 的「后端基准快照」要不要改成**由 Go 真函数生成的夹具**

⛔ **不代拍**（工程口径：维护成本取舍）。

**§730 实测的现状（不是风险清单，是已发生的事实）**

| 项 | 读数 |
|---|---|
| 快照**今天**还对吗 | ✅ **逐字一致** —— 跑当前源码的真 `mergeIncremental` 两条语料，输出与 TS 里写死的 `goBaseline` 完全相同 |
| 断言有牙吗 | ✅ **两个方向都能打红** —— M1 改 `goBaseline` → 52 pass/**1 fail**；M2 改 `beforeFix` → 52 pass/**1 fail**；还原后 SHA 一致、回跑 **53 pass / 0 fail** |

⇒ **§612.3 那条风险成立** —— 且 **§731 实测：今天（2026-10-15）的 merge `9813ee36`
真的同时改了 Go 与 TS 两个算法文件**（Go 侧只改注释、未动算法核心；
TS 侧把 `MIN_ANCHOR` 闸加了回来，方向朝 Go 对齐）⇒ **这次方向是对的，下次可能是反的。**

⭐ **今天这次是靠人眼 `git show | grep` 逐行核发现的，没有任何自动门会发现它**
（TS 输出没变 ⇒ TS 测试自然全绿）⇒ 这正是选项 ② 要买的东西。

| | 方案 | 说明 |
|---|---|---|
| ① | **维持现状** | 今天是对的；将来 Go 侧改了 `minFuzzyAnchor`/`ANCHOR_COVERAGE`/`MAX_OVERLAP` 而基准没重生成时，**TS 这层不会报警** |
| ② | 改成**由 Go 真函数生成夹具**、每次比对 | ⭐ **这不是新发明** —— `truncate_unit_fixture_test.go` 已经用过一次，并写明了动机（见下） |

### ⭐ §732 普查结果：跨语言同步点共 **5 处**，全部有门，**本项是其中最弱的一个**

判据阳性对照已自证（能抓到已知的 dedup 三常量）；执行率 **5/5**。

| 强度 | 同步点 | 门机制 |
|---|---|---|
| ⭐⭐⭐ | `utils/text.ts` 截断 ↔ Go `truncateStr` | **真 Go 产出 fixture，前端读它比对** |
| ⭐⭐⭐ | `sessionEvents.ts` ↔ `session_event_broadcaster.go` | 两侧 fixture 测试锁形状 |
| ⭐⭐ | `summary-prompt.ts` | TS 行为断言与 Go 源码断言共用 |
| ⭐⭐ | `notification.ts` 上限 200 ↔ Go | 源码文本断言（⚠ §733 实测：它**不是漏报，是假红**） |
| **⚠ 最弱** | **§612.2 dedup 基准（本项）** | **手写历史快照** |

⇒ ⭐⭐⭐ **`truncate_unit_fixture_test.go` 的注释已把这条模式的动机写死了**：

> 「前端那道门在 **JS 里复刻**了 Go 的 truncateStr 再去比 ⇒ 量的是『我自己写的 JS 实现 vs 我自己写的 JS 实现』。
> 如果 Go 侧语义变了……**那道门不会知道**。」
> 「这里把真身固化成一份 fixture……于是它量的对象变成**产品代码本身**。」

⇒ ⇒ **选项 ② = 把仓库已有的、写明理由的模式，补到唯一没采用它的那一处。**

### §733 补测：① 那道源码断言的失败模式是**假红**，不是「漏」

两个变异臂（`cp` 备份 + SHA 还原）：

| 变异 | Go 行为变了吗 | TS 门反应 |
|---|---|---|
| **M1** `limit <= 0 \|\| limit > 200` → 调换条件顺序（**语义等价**） | ❌ 没变 | **转红**：`后端…limit 上限没找到，判据需要跟着它走` |
| **M2** 上限 `200` → `300` | ✅ 真变了 | **转红**：`前端要 200 条，后端上限却是 300 —— 超出部分会被静默压回 50` |
| 还原 | — | SHA `5213e8393a10e80c…59fa4` 一致、回跑 **3 pass / 0 fail** |

⇒ ⚠ **它不是漏报型的弱门，是噪声型的门**：无害重构与真变化**用同一条红线报**。

| 机制 | 对真变化 | 对无害重构 | 实测 |
|---|---|---|---|
| 源码文本断言（①） | ✅ 报，信息量大 | ⚠ **也报** | §733 实测 |
| 真 Go 生成 fixture（③） | ✅ 报，精确到哪条语料 | ✅ **静默** | ✅ **§734 实测，预测成立** |

⇒ ⚠ ① 这种门真正的侵蚀路径**不是漏报**，而是「老是无缘无故红」⇒ **被人放宽正则以求太平**。

### §734 实测：③ fixture 门 **既有牙、又安静**（两个预测都验过）

| 变异 | Go 语义变了吗 | 实测 |
|---|---|---|
| **M1** `cut := s[:n]` → `string([]rune(s)[:n])`（按字节→按 rune） | ✅ 变了 | **红**：`truncateStr 语义已变`，逐条列出 `runes 期望值过期：fixture=76 实测=200` |
| **M2** 反转早返回（**语义完全等价**） | ❌ 没变 | **绿**（Go `ok 0.974s` · 前端 8 pass / 0 fail） |
| 还原 | — | SHA `b03e0f00…0d474` 一致、fixture 文件未被改写、回跑 `ok 0.838s` |

### ⚠ 诚实的取舍收窄（这才是 A.9 真正要买的）

**现状①在「TS 侧变」这个方向上并不弱** —— §730 的 M1/M2 已证明它有牙。
它**唯一的**弱点是：**Go 侧变而基准未重生成时无人报警**（§730.3 / §731.4）。

⇒ **选项 ② 要买的就是这一条**，不是「更严」，也不是「更安静」
⇒ **取舍收窄成一句话**：是否愿意为「Go 侧改动能被自动发现」付一次夹具机制的维护成本。

> ⛔ **§737.2 订正（2026-10-15，原文保留供对照）**：上面那条「唯一弱点」**已被实测推翻**。
> 把 Go `minFuzzyAnchor` 从 4 改成 2（真算法漂移）后，
> **`backend/internal/stt/crosslang_baseline_pin_test.go` 转红**，
> 报错文案直接写着「Go 侧行为已漂移，**TS 侧 §612.2 绑定的基准随之过期**」；
> 同一时刻 TS 侧 `meeting-dedup.test.ts` **53 pass / 0 fail** 保持沉默。
>
> ⇒ **④ 已经有人在管了**，不必再为它付夹具成本。
> ⇒ 它剩下的缺口窄到一条：**没有任何东西断言两份手抄字面量彼此相等**
> （只改 Go 那份 ⇒ Go 红 / TS 绿；只改 TS 那份 ⇒ TS 红 / Go 绿）。

### ⚠⚠ §735 修正：⑤ **不是**生成的 fixture，是**手抄两份常量** —— 我 §732 读注释就排了序

⑤ 的注释写「两侧 fixture 测试锁定序列化形状」，读代码后是**两份各抄一份的常量**：
TS 侧全仓唯一提到那个 Go 文件的地方是第 6 行**注释**，**从不读取**它的产物
（对照 ③：`truncate-unit-parity-244.test.mjs:71` 是真的 `readFileSync` 读 Go 生成的 JSON）。

变异实测（改 Go 的 `phase` → `phase_v2`）：

| 侧 | 实测 |
|---|---|
| Go `TestSessionEventBroadcaster_WireContractFixtures` | ✅ **红**（`broadcast #1/#2/#5 wire JSON mismatch`） |
| **TS `sessionEvents.test.mjs`** | ✅ **绿：6 pass / 0 fail** |
| 还原 | SHA `da14ceec5708…ae76` 一致、回跑 `ok 0.638s` |

⇒ ⛔ ⑤ 注释里那句「**任何一侧改字段名/枚举值，另一侧打红**」**实测不成立** ——
　那只在**语言内**成立，**跨语言那一半没有任何机制**。

### ⭐ 五处基准的**跨语言漂移**总表（这是 A.9 真正的样子）

| # | 同步点 | 基准机制 | Go 侧漂移会自动报吗 | 无害重构误报? | 实测 |
|---|---|---|---|---|---|
| **③** | `text.ts` 截断 | **真 Go 生成 JSON，TS 读它** | ✅ **会**（精确到哪条语料） | ✅ **不会（安静）** | §734 |
| **②** | `summary-prompt` | 抽 Go 源码 + **锚点包含检查** | ⚠ **只覆盖丢锚点**；**内容漂移不报**（改字实测全绿） | ⚠ **会 —— 结构层假红**（私有函数改名就红） | §736 + **§737** |
| **④** | dedup 基准 | **两份手写快照** | ⛔ **会，但只在 Go 侧说话**（TS 侧全绿） | — | §730 + **§737** |
| **①** | `notification` 上限 | 源码正则抠数字 | ❌ 不会 | ⚠ **会（假红）** | §733 |
| **⑤** | `sessionEvents` | **两份手抄常量** | ❌ **不会**（双向独立已实测：Go 改→Go 红，TS 改→TS 红，各自为政） | — | §735 + **§737** |

⇒ ⭐ **仍只有 ③ 一处是「真 Go 生成 ⇒ TS 消费」的闭环**。
　**①⑤ 零跨语言覆盖**；**② 内容层漏报 / 结构层假红（两个方向的债）**；
　**④ 只差「两份字面量彼此相等没人断言」这一条**。

⇒ ⇒ **A.9 的准确表述**（§737 之后）：
　不是「④ 要不要改成 fixture」（④ 的主要缺口**已由 Go 侧常驻断言买掉**），
　而是「**①②⑤ 三处零跨语言覆盖的手工基准，要不要逐个改成 ③ 那种闭环**」，
　外加 ② 的结构层假红要不要单独还债。

**§736 的 ② 实测读数**（双向变异）：

| 变异 | Go 契约变了吗 | 实测 |
|---|---|---|
| **M1** Go 规则正文**增补**一个字 | ⚠ 锚点仍在 | **绿 4 pass / 0 fail**（不报） |
| **M2** Go 丢掉 `CONTRACT` 锚点字面量 | ✅ 锚点消失 | **红**：`服务端提示词缺「只放真需要人做的事」…两份实现已经漂了` |
| 还原 | — | SHA `b03e0f00…0d474` 一致、回跑 **4 pass / 0 fail** |

⚠ 量具事故如实记：M2 首次尝试断言「该字面量出现 1 次」，实际 **2 次** ⇒ 变异**没落地**，
　那一轮的「4 pass」只是未变异基线、**不是读数**；改正断言后才拿到真读数。

⚠ 但这是**设计选择**不是「有洞」：四处手工基准共同点是「改动要记得同步」，
　③ 的代价是「Go 语义每次变都要重生成 fixture」——两者各付各的成本，
　**本简报不主张哪一种更好，属未代拍**。

⚠ **别把这条的绿当成「#58 已钉住」**：§612.3 已说明两块覆盖面**不相交**
　（本条覆盖 `lcsLen ∈ [2,4)`，#58 那一格是 `c == n 且 len > 40`）。

⚠ 未测：Go 侧改动历史**没有**逐条 `git log` 比对，只核了**当下**。


---

### ⛔ §737（2026-10-15）：**A.9 的选项表要再改一次** —— ④ 已被实测排除出「零覆盖」

§736 留了三处「未测」，本节全部做完（5 个变异臂，每臂都带变异落地断言）。
**其中一条推翻了 §732.3 / §734.4 / §736.4 三处原文**，已回溯插 ⛔ 横幅订正。

| 臂 | 变异（行为是否改变） | 读数 |
|---|---|---|
| **M-A** | Go `minFuzzyAnchor` **4 → 2**（真算法漂移） | **Go pin 红**（文案点名「TS 侧 §612.2 绑定的基准随之过期」）· **TS 53/0 绿** |
| **M-A2** | 只改 Go 那份基线字面量 | Go 红 · TS 53/0 绿 |
| **M-B** | Go 私有函数**改名**（行为不变，`go build` 通过） | ② 门 **红**：`找不到 Go 的 buildSummaryPrompt —— 判据的前提失效` |
| **M-C** | Go 规则文本改一句**非契约**措辞 | ② 门 **4/0 绿** · Go 门也绿 ⇒ **两侧文本已漂移，无人报** |
| **M-D** | TS 夹具**输入** `file_write` → `file_write_v2` | TS **红**（5/1）· Go **绿** ⇒ **⑤ 双向独立实测完成** |

**对属主的实际影响（只改措辞，不改结论方向）**：

1. **④ 不必为它付夹具成本** —— 后端已有常驻断言在报，且报错文案就是冲 TS 基准写的。
   它剩下的缺口只有「两份手抄字面量彼此相等没人断言」这一条。
2. **真正零跨语言覆盖的是 ①⑤**（⑤ 双向独立已有实测），**② 是内容层漏报 + 结构层假红两个方向的债**。
3. **仍只有 ③ 一处是闭环**（真 Go 生成 ⇒ TS 消费）。

**另有一处已自行落地的改动**（非产品口径，不需拍板）：
`frontend/src/features/meetings/summary-prompt.ts` 两处注释宣称
「与服务端**逐字同源**（跨语言门会比这一段/这一行）」——
实测 **两半都不成立**：门只做 11 条锚点的**包含检查**，
把 CONTRACT 逐条扣掉后 `SUMMARY_FIELD_RULES` **有三整段约 50 字不在覆盖内**（M-C 正改在第一段里、门全绿）。
两处注释已改成如实描述并附残余覆盖率。

⚠ 本节全部读数都是「**门会不会响**」，不是「提示词质量会不会变」——
　**缺 GATEWAY key，全程未测真实网关行为**。


---

## A.10 🔴🔴 **当前 HEAD（`cc11500f`）的门禁是红的** —— 两条**新**的阻塞项，都已拍板前挂着

§737 收尾回跑 `npm run gates` 时**第一次撞见红的**（此前多节基线都是全绿）。
⚠ 已排除本会话改动：把唯一改过的源文件换回 HEAD 版本，三条失败读数**逐字相同**。

| 文件 | HEAD 读数 | 性质 | 处置 |
|---|---|---|---|
| `ingest-behavior.test.mjs` | 12 tests / **0 pass / 0 fail**（全 `cancelledByParent`） | ⛔ **§86/§150 承重行为门整体瘫掉** | ✅ **已修**（补 auth 桩）→ **12 pass / 0 fail**，变异打红过 |
| `stt-price-catalog-load-bearing.test.mjs` | 8 tests / 5 pass / **3 fail** | 1 真发现 + 2 负控漂移 | ✅ 负控已修 → **7 pass / 1 fail**；真发现待拍板 |
| `capabilities-stub-gate.test.mjs` | 5 tests / 4 pass / **1 fail** | Sherpa 登记表过期 | ⛔ **待拍板** |

⇒ **红文件 3 → 2 · `test:fail` 事件 19 → 3**；剩下两条都是属主口径。

### A.10.1 🔴 请拍板①：**ASR 价表要重算「可达集里最便宜的是谁」**

`target.go` 的 **gateway 组**新增两档，而 §580 的 `GOLDEN` 只覆盖 external 组：

    minimax-asr-1.0   USDPerHour: 0.38   实测可用（2026-10-06 zh/en 全对）
    glm-asr           USDPerHour: 0.50   数据面已接通；智谱凭据余额不足 429/1113，充值后零改动可用

⚠ **与旧待办的措辞差别**：不是「新增模型」，而是 **gateway 组与 external 组并存**
　（`asr-1.0` $0.38/h 与 `glm-asr-2512` $0.50/h **仍在 external 组**）
　⇒ 重算时**两组都要看**，且 `glm-asr` 现在是**凭据余额不足**状态，要算进「可达集」吗？

⇒ **不代拍**：§579 的「中文专用 ASR 贵约 42 倍」这个结论**整个架在这张表上**。

#### 🔴 请拍板①的**真实工作量比门上写的更大**【§764 重算已完成】

那道红门写的是「补两行 GOLDEN + 重算『可达集里最便宜的是谁』」。
**重算已经做完了**（双控通过：负控 `0/0`、正控价格逐字相符；换算全交给代码，价表现读不手抄）：

| 结论 | §579 写的 | 窄口径 | 宽口径 |
|---|---|---|---|
| ① **「可达集里最便宜的是谁」** | `qwen3-asr-0.6b` **$0.012** | **不变** | **不变** |
| ② 「能接上的中文**专用** ASR 贵 N 倍」| **约 42×** | **41.7×** | ⭐ **31.7×** |
| ③ 「中国云厂商不是价格洼地」| ✅ | ✅（比 ¥0.288 地板价仍贵 **12.5×**）| ✅（仍贵 **9.5×**）|

⇒ ⭐ **只有「42 倍」这个数字在动**，定性结论两个口径下都不被推翻。

⚠️⭐ 而这不是「§579 已过时」，是**漏算** —— 三条独立证据：
① `minimax-asr-1.0` 进 `target.go` 的 commit 是 `438595ce`（**2026-10-07**）；
② 写 §579 的 commit `9813ee36`（**2026-10-15**）是它的**后继**（`--is-ancestor` = YES）；
③ `git show 9813ee36:…target.go` 里 `Model: "minimax-asr-1.0"` 命中，
且 `USDPerHour: 0.38` **与今天逐字相同**
⇒ **42 倍是在它已经躺在同一张表里、带着同样的价的情况下算的。**

⭐ 它值得算进去的仓内证据（`target.go:238` 备注原文）：
**「实测可用（2026-10-06 zh/en 全对）· SSE 流式 · 500s 限长」** ⇒ 它既**可达**又**支持中文**，
且 **$0.38 < `glm-asr-2512` 的 $0.50**。

⇒ **请拍的就只剩一件事**（⛔ 不代拍，属**产品/采购口径**）：
**`minimax-asr-1.0` 算不算「中文专用 ASR」？**

| 选项 | 后果 |
|---|---|
| **① 算**（口径 = 「支持中文的可达 ASR」）| 最便宜的中文可达 ASR = `minimax-asr-1.0` $0.38 ≈ ¥2.736/h ⇒ **§579.2 的「约 42 倍」要改成 32 倍**；GOLDEN 补 `minimax-asr-1.0: 0.38` |
| **② 不算**（口径 = 中文**优先的专用**模型）| 倍数维持 **41.7×**，§579 数字不动；GOLDEN 仍**必须**补 `minimax-asr-1.0: 0.38`（它是真价格，③ 判据要求有价必在册）|
| ⚠ 两个选项下 **`glm-asr: 0.50` 都要补进 GOLDEN**（③ 判据已经因为它报红）|

⚠ **无论选①还是②，这道红门都要处理** —— 它现在卡着 `main` 的 CI（§763.5 / 见下节）。

#### 🟢 2026-10-16 补：**`glm-asr` 那个子问题，有个探针能直接答 —— 而它今天照文档操作跑不起来（已修）**

⚠ 先说清边界，**不夸大**：有一个专门测「网关实际供应了哪些 ASR 候选」的探针
`TestLiveASRCandidateCoverage`（`backend/internal/stt/live_asr_candidate_coverage_probe_test.go`）。
⛔ **它答不了上面那个口径选择** —— 我读了生产判定 `discovery.go:67 IsASRCandidate`，
它只判「是不是 ASR 候选」（强标记 / 非 TTS / `modality=audio` / 名字规则），**完全不判中文**
⇒ 「`minimax-asr-1.0` 算不算中文专用」仍然只能由你拍。

⭐ **但它能答另一个子问题**：它 `ListGatewayModels` 列网关 `/models` 后报差集
⇒ 能给出 **`glm-asr` 到底在不在网关真实供应的列表里**（现在 A.10.1 里
「`glm-asr` 凭据余额不足，它算不算可达」这一问，正是靠 `target.go` 的登记备注推的，没实测过）。

⛔ **而它今天照仓里四处说明操作，必然跳过**（`§773.3`）：守卫是 `!= "2"`，
但 skip 消息 / 用法注释 / §467 台账**三处都写着 `POCKET_LIVE_GATEWAY=1`**
⇒ 可复现：`POCKET_LIVE_GATEWAY=1 go test … -run TestLiveASRCandidateCoverage` ⇒ `--- SKIP (0.00s)`。
判定为**笔误**（五条独立证据，其中最强的一条：全仓守卫分布 **25 个 `!= "1"` + 这 1 个 `!= "2"`**）。

✅ **已落地 1 行技术性修复**（`2` → `1`），变异双向验证：
设 `=1` 不给 URL/KEY 时，修复前 `--- SKIP`、修复后**真的去列模型**
（`列网关模型：Get "/models": unsupported protocol scheme ""`）；
照台账设全 3 个键时失败原因**前进到网络层**（`…/v1/models: EOF`）⇒ 无回归；
不设 env 仍 `--- SKIP` ⇒ 守卫没被破坏成恒真。

⇒ ⭐ **所以：只要你给出 `POCKET_LLM_GATEWAY_URL` + `POCKET_LLM_GATEWAY_API_KEY`
（⚠ 不需要音频、不发转写请求，它只列目录，最省额度）**，
这个子问题就能从「按登记推算」升级成「按网关实测」。

⛔ **但那条修复目前没有任何门在守**（变异实测：守卫改成 `3` ⇒ `go test ./internal/stt` 仍 `ok`）。
⇒ 「要不要加『守卫 == skip 消息 == 台账 unlock』一致性门」属测试工程口径，**登记不代拍**。

### A.10.2 🔴 请拍板②：**Sherpa 移不移出 `NOT_IMPLEMENTED_PLUGINS`** —— 且它带着平台分裂

门按设计转红，核实**不是误报**：Sherpa 确已被真正实现
（`93260c9b` Phase 4 落地 · `8c5af5c9` 真机 instrumented 4/4 绿 · 类注释已改成「替换 7 月骨架」 ·
6 个 `@PluginMethod` 里只剩 `extractEmbedding` reject，标为 Phase 5）。

⚠⚠ **但照门里的话直接移出去，会制造一个新的说谎**（实测）：

| 事实 | 读数 |
|---|---|
| Android `SherpaPlugin.java` | ✅ 已实现 |
| **iOS 侧 Swift 实现** | ❌ **一个都没有** |
| 仓内 `.onnx` 模型文件 | ❌ 没有（运行时按 URL 下载） |

　而这张表存在的**全部理由**就是 §179 实测的那句：
　「不加这一栏，能力快照会报 `recognition.asr = 'model'`，而真实调用 100% reject
　　—— **能力探测绿灯、功能全无**」。

**三选一**：
- **① 保持登记** —— 门继续红着，代价是长期红
- **② 改成 per-platform 结构后再移出**（推荐方向）：登记表按平台分别声明
- **③ 直接移出、接受 iOS 说谎** —— ⚠ **不建议**，与该表的设计意图正好相反

#### ⛔⛔ §765（2026-10-16）**回溯订正上面③的写法**：它说「接受 **iOS 说谎**」—— 这条**事实错误**

⚠ 我原来写「③ 直接移出、**接受 iOS 说谎**」⇒ **这句不成立**：

| 事实 | 读数 |
|---|---|
| iOS 有没有 `Sherpa` | ⛔ **`grep -rn Sherpa ios/` = 0 命中**（iOS 只有 `AppSettings` / `BackgroundMic` / `BiometricAuth` 三个 Swift 插件）|
| ⇒ `isPluginAvailable('Sherpa')` 在 iOS 上 | **false**（压根没注册）|
| ⇒ 移出登记对 iOS 的影响 | **零**（那条登记只用来压 Android 的假阳性）|

⇒ ⚠⚠ **我那个「不建议」的理由当时就没写出来、也确实不成立。**
记录它是因为：**一个没有依据的倾向性意见，会让属主以为「已经权衡过了」。**

#### 🔴 但③**仍然不安全** —— 理由与 iOS 无关，是 ASR/OCR 耦合（§765.2 真函数实测）

`capabilities.ts` 用**一个**布尔同时驱动两个不同能力：

```
 44   recognition: ['Sherpa'],          // ← 只有 Sherpa，一个纯 ASR 插件
189      ocr: recognition ? 'model' : 'none',
190      asr: recognition ? 'model' : 'none',
```

用**真函数** `detectCapabilities` + 注入探针（`inShell=true / platform='android' /
pluginAvailable∈{Sherpa,BackgroundMic}`，**真源码零改动**）跑出来：

| | `ocr` | `asr` |
|---|---|---|
| 现状（Sherpa 在登记表里）| `none` | `none` |
| **删掉 Sherpa 登记表项**后 | ⛔ **`model`** | `model` |

⇒ `asr='model'` **正确**（Android 5 个 ASR 方法是真实现），
　`ocr='model'` ⛔ **是假声明**（仓内**零 OCR 实现**）
⇒ 而这正是 `NOT_IMPLEMENTED_PLUGINS` 模块存在的目的（防「探测绿灯、功能全无」）。

⚠⚠⚠ **反直觉的结构**：**那条过期的登记表项，正在掩盖 ASR/OCR 耦合缺陷。**
移出它会同时暴露两件事：ASR 变成真（好）· OCR 变成假（坏）。

⇒ 📌 **②（推荐方向）的范围要加一条**：能力表必须**把 `ocr` 与 `asr` 拆开**
　（例如 `recognitionAsr: ['Sherpa']` / `recognitionOcr: []`），
　否则移出登记那一刻就造出 `ocr='model'` 的假声明。

⚠ 另：移出登记还会**连带触发第二道门** —— `stt-local-first-claim-gate.test.mjs`
　明写「只有在登记成立时才要求文件头如实说『走云端』⇒ 骨架接上时**自动切换到另一个形状**」
⇒ ⇒ **这不是单点改动，是「2 道门 + 登记表 1 项 + 能力表拆分 + 1 个文件头」的协同改动。**

⚠ 再加一条要一并权衡的（代码事实，非推测）：`ensureModel`（`SherpaPlugin.java:513`）
　首次 `preload` 要从 **GitHub releases 下载模型**，下载不到就抛
⇒ 移出登记后能力报 `asr='model'`，而首次使用仍可能因下载失败而 reject
⇒ 「探测绿灯 → 首次使用要联网 → 下载失败才报错」**正是本模块要防的那种绿灯**
⇒ 是否为「模型尚未就位」单开一个能力维度，属**工程口径**，⛔ 不代拍。

#### ⚙ §754（2026-10-16）证据升级：Android 侧**运行时库已接入**，且登记表 `why` 有一句**在说假话**

复核「仓内无 Sherpa 资产」那条证据时发现 `libs/sherpa-onnx-1.13.8.aar.part`
已变成**完整的 50 MB AAR**（今日 00:18 落地，26 个文件）。追完整条链：

| 环节 | 读数 |
|---|---|
| AAR 来源 | `app/build.gradle:78-104` **构建时自动下载**（`.part` 分片 + rename） |
| 构建是否真用 | `:111` `implementation files('libs/sherpa-onnx-1.13.8.aar')` ✅ |
| 是否进 git | ❌ `android/.gitignore:5` `*.aar` ⇒ **本地构建副产物** |
| AAR 内容 | 4 个 JNI `.so`（`libonnxruntime` 22 MB / `-jni` 4.8 MB / `-c-api` 4.5 MB / `-cxx-api` 440 KB） |
| **ABI** | ⚠ **只有 `arm64-v8a`** |
| AAR 内 `.onnx` | **0 个**（AAR 是运行时库，不含模型） |
| iOS `.swift` 命中 | **0**（零实现不变） |

⇒ ⭐ **`.part → .aar` 本身就是一条读数**：它说明**有别的会话跑过 Android 构建**。

⛔ **登记表 `why` 有一句与源码不符**（`capabilities.ts:78`）：
　它写「6 个 `@PluginMethod` 里，**5 个**只 call.reject」，
　源码实测 **6 个方法只有 `extractEmbedding` 一个 reject**（`:200`「not implemented (Phase 5)」），
　其余 5 个是 Phase 4 真实现。
⇒ `why` 字段自称是「未实现的直接证据（供排查用）」，**它现在在说假话**。

⇒ 📌 **三选一的依据因此更强，方向仍是 ②**：真实状态已不是笼统的「Sherpa 未实现」，而是
　**「Android 5/6 已实现（差 `extractEmbedding`）+ 运行时已接入 + 模型未入库 + iOS 零实现 + 仅 arm64」**。
　⇒ ⛔ **不代拍**：登记表一个字未改（`fingerprint` 消失是**门在报警**，正是它该做的事）。

#### 🔴🔴🔴 2026-10-16 补：**A.10.1 与 A.10.2 已经是 `main` 上 CI 的实际红因**，且 CI 不兜

§763 实测：本地 `npm run gates` 长期停在 **`2/46`（第 3 项 `test:all` 停）**，
那两个红文件的**失败原文**就是上面两条决策本身：

| 红文件 | 失败判据**原文** | 对应 |
|---|---|---|
| `stt-price-catalog-load-bearing.test.mjs` | 「多出了不在册的档：**`minimax-asr-1.0` / `glm-asr`**（$0.38/h / $0.5/h）⇒ §579 的「可达集里最便宜的是谁」**需要重算**，并把它的价补进 GOLDEN」（实测 `2 !== 0`）| ⭐ **A.10.1** |
| `capabilities-stub-gate.test.mjs` | 「**Sherpa 的指纹 "sherpa-onnx AAR not integrated" 已在 `SherpaPlugin.java` 里消失** ⇒ 骨架很可能被真正实现了，**请把它从 `NOT_IMPLEMENTED_PLUGINS` 移出去**」| ⭐ **A.10.2** |

**指纹是谁删的**：`93260c9b`（2026-10-06，`feat(stt): 本地 ASR Phase 4 落地——sherpa-onnx 双引擎替换空壳插件`），
后续 `8c5af5c9` 还修了 AAR 运行时空值崩溃；工作树对该文件**零改动**
⇒ 指纹是被**已提交的实现**真删的 ⇒ **门红得对，登记表确实过期**。

**为什么急**：
`frontend.yml:171` 明写 **`continue-on-error: false`**（注释专门写
「not use continue-on-error to mask P0 regressions」），
而 **`test:all` 是 CI 的一步**（`frontend.yml:132`）
⇒ ⭐⭐⭐ **`main` 的 CI 现在就是红的，红因恰好就是这两条待拍板。**
拍完它们，这道门才可能转绿。

⚠ 但**顺序不能反**：门红**不是**该由我自行消红的理由 ——
`A.10.1` 要定的是「`glm-asr` 余额不足算不算可达」（采购/产品口径），
`A.10.2` 要定的是 Sherpa 的 per-platform 结构（架构口径）⇒ **一律不代拍**。

#### 📎 附：一条与 #63 有关的**新读数**【§763.4】

普查设计文档里「已修 + 带代码锚点」的结论后发现：
`getEnv`（3 个测试引用）、`getFirstEnv`（2 个）**都有门**，
而 ⛔ **`getEnvInt` 一个测试都没有** —— 它偏偏是三兄弟里**唯一有缺陷**的那个
（§755.6：`getEnvInt(" 42 ")` ⇒ `Atoi` 失败 ⇒ **静默回落 fallback，用户设的值被丢弃且无日志**）。
⇒ ⚠ 这**不是**要求现在就补门（#63 整体待拍板），
　但它是「拍 #63 时应当一并处理」的一条具体依据。

### A.10.3 ✅ 已自行落地（不需要拍板）

- **`ingest-behavior` 补 `../../stores/auth` 桩** ⇒ 门从「完全不跑」恢复到 **12 pass / 0 fail**，
  并用「拆掉幂等去重」变异验证它**真承重**（§150 转红）
- **价表门两处负控守卫修复**：① 守卫探针改用与 `replace` 相同的串（原来是**假红**）；
  ④ 锚点改为按行取（原来被上游改 Note 打瘫）。两处都做了变异验证仍会咬
- ⚠ **登记不预防性改动**：另 3 个也用 `platform: 'neutral'` 的测试同样没有 auth 桩，
  今天全绿，但 import 图一旦碰到 `stores/auth` 会走上完全相同的瘫掉路径。**未测。**


---

### ⛔ §739（2026-10-16）：**A.9 的分母被换掉了** —— 从「4 处」扩到「12 处 + 58 个文件」

§732.2 / §737.8 两处都登记过同一个弱点：「五处跨语言同步点」的分母
**来自源码自声明**。§739 做了全量普查（**339 个前端测试 / 406 个 Go 产源文件**），
判据带 **6 个合成控（3 正 3 负）**。

| 桶 | 文件数 | 说明 |
|---|---|---|
| **A 硬读 backend** | **13** | 读 **Go 产物（夹具）= 1**；读 **Go 源码或源码目录 = 12** |
| B 手抄契约 token（上界，去通用串后） | 86 | 208 个 token |
| **B 下界**（仅 snake_case / 点分小写契约名） | **58** | 130 个 token |

⇒ ⭐ **前端 339 个测试里，真正「读 Go 生成产物」的只有 1 个**（§734 那个闭环）；
　**读 Go 源码做文本/数值断言的有 12 个**；另有 **58 个文件**只靠「两边都写着同一个字符串」。

⇒ ⇒ **A.9 的选项表必须按这三个桶重写**：此前它说的是「4 处手工基准」，
　实际范围是 **12 处源码断言 + 58 个文件级 token 手抄**。
　**结论方向不变**（仍属工程口径、不代拍），但**工作量估计会差一个量级**。

⚠ **两个方向都已实测**：①⑤ 零跨语言覆盖（§737）；④ 只差「两份字面量彼此相等没人断言」。

⚠ **诚实标注 B 桶**：token 共现**不是耦合的证明** —— 已剔掉 `package.json` / `audio/wav` /
　`application/json` / `127.0.0.1` 这类通用串，下界只收契约型命名，
　**但仍未逐 token 做语义确认**。

⚠ **未测**：12 处「读 Go 源码」只有形态普查，**没有逐个做变异定性** ——
　§733/§736 已证明这一族里**既有会假红的也有会漏报的**，不能据形态推断强度；
　**反向（Go 侧测试读 TS）未普查**；判据只认 `readFile*`，未对「import 型闭环」单独设控。

⚠ **量具自身被抓出三个洞**（这次普查最有价值的部分）：
　① `field_or_enum` 只收下划线不收点 ⇒ **整类漏掉** `session.activity` 那种 wire 事件名（合成正控抓出，109→119）
　② 把**注释里提路径**当成真读 ⇒ 假阳（合成负控抓出，119→96；§735 已写过这件事，第一版判据又栽一次）
　③ 路径经 `path.join(ROOT,'backend',…)` **变量传递**时抓不到 ⇒ 假阴（**靠记忆里的反例**才发现，A 5→13）
　⇒ **假阳与假阴两个方向都会坏，且没有哪个控能同时覆盖两边。**


## A.11 🔴🔴🔴 请拍板④【新】【§766】多人长段里**有一个说话人会从名单里彻底消失** —— 说的是他 10.4% 的发言

⛔ **不代拍**（属工程口径）。详见 `2026-10-06-recording-quality-fixes.md` §766。

§593 早就钉死「本仓 VAD 把 speaker_0/1/2 的 **4 段**话合并成 1 段 **16.28 秒**」，
但它停在那里没往下走。§766 用**真几何 + 官方说话人真值 + 真 `planRefine`** 追完了。

**读数**

| 项 | 值 |
|---|---|
| v0 内的说话人分布 | `speaker_0` 40.7% / `speaker_1` **38.6%** / `speaker_2` 20.7% |
| ⇒ 段标签打成 | `[说话人0]`（`speaker-relabel.ts` 明写「**错合并 0 是刻意的**」⇒ 一段只出一个标签）|
| ⇒ 标签错的时长占比 | **59.3%** |
| `speaker_1` 全场发言 | **5.91s（10.4%），且只在 v0 内部** |
| ⛔ 真 `planRefine` 的 `roster` | **`["[说话人0]","[说话人3]","[说话人2]"]`** ⇒ **没有 `speaker_1`** |

**自证**：把 v0 的标签手工补成 3 个人 ⇒ 缺失变空 ⇒ 证明名单确实**只由段标签推出**。

**后果会连累三处**（源码事实）：LLM 精校提示词的在场人名单 ·
`repairRosterHomophones` 的同音纠正范围 · 服务端 `participantLeak` 的泄漏名单。
同时 v0 的那句「说话人0 说了这段」本身就是**错的**。

**选项**

| # | 方案 | 代价 |
|---|---|---|
| ① **接受现状**并登记 | 0 | 10.4% 时长的发言在纪要里**没有任何归属**，且他的同音字不纠正 |
| ~~② 在精校阶段补全名单（用声纹聚类的全部簇推名单）~~ | ⛔ **已出局（§767）**：聚类输入里**没有 `speaker_1` 这个样本**（他只混在 `v0` 内）⇒ **结构上变不出来** |
| ③ **改切段判据**（把「换人」也当切点）| ⛔ 大 | §593 已证「换人停顿 0.12~0.71s 远短于同人换气 ⇒ **调参解决不了**」⇒ 要引入换人检测 |

#### ⚙ §767（2026-10-16）**全真端到端**：6 段 → **5 个标签**，而真实只有 **4 个人** —— 而 5≈4 是**巧合**

链：真实 48 维嵌入 → **真 `SpeakerDiarizer`** × 6 → **真 `relabel()`** → **真 `planRefine`**（`adopted=true`）。

| VAD 段 | 真实说话人 | 真标签 |
|---|---|---|
| `v0` | **0 / 1 / 2** | `[说话人 1]` ⛔ **它自己**（cos(v0,speaker_0)=0.81~0.84 **<** 阈值 0.99 ⇒ 没并进任何人）|
| `v1`/`v5` | 0 | `[说话人 2]`（正确合并）|
| `v2` / `v3` | 3 | `[说话人 3]` / ⛔ `[说话人 4]`（**同一个人被拆开**）|
| `v4` | 2 | `[说话人 5]` |

⚠⚠⚠ **`roster` = 5 条，而真实 4 人；这个「5 ≈ 4」是三个缺陷互相抵消**：
**−1 人**（`speaker_1` 消失）+ **+1 幽灵**（`[说话人 1]` 代表 3 人的混合，不是任何人）+ **膨胀**（`speaker_3` 拆成两个）。

⇒ ⭐⭐⭐⭐ **因此：任何只比「标签数量 vs 真实人数」的自检都会被这个巧合放过。**
　真正的判据必须**逐段把「标签」与「该段的真实说话人集合」对齐**。
⇒ ⭐ 同时这也解释了 §587「膨胀 2.31×」为何与本节「只多 1」方向相反 ——
　**膨胀（拆）与丢失（混）同时存在，聚合计数上互相掩盖。**

⚠ **回溯订正**：§766 那节的标签是**按重叠时长构造**的（把 `v0` 写成 `[说话人0]`），
⚠ **与真实不符**（真跑出来 `v0` 自成 `[说话人 1]`）⇒ 已在设计文档 §766 前插订正块。
　§766 的**结论**（名单里没有 `speaker_1`）仍成立，但**机制**是「v0 自成标签」而非「错标成别人」。

⚠ **未测**：真实会议里「多人长段」的**发生率**（本节是**单样本**：1 文件 / 4 人 / 56.86 秒）；
⚠ **未测**：v0 的标签取「主导者」是**按重叠时长推出的构造**，不是跑真聚类实得的；
⚠ **未测**：任一方案在真实音频上的效果。

---

## A.12 🔴🔴 请拍板⑤【新】【§775】**起一个 PG 实例 ⇒ 解锁 450 条测试** —— ⚠ 它们**本次全被跳过**，但**历史上跑过**（原写「从未跑过」已订正，见下）

### 测量结果（`go test ./... -v -count=1`，2026-10-16 本机实测）

| 项 | 读数 |
|---|---|
| 顶层测试函数合计 | **3046** |
| PASS | 2411 |
| ⛔ **SKIP** | **635（20.8%）** |
| FAIL | 0 |

⇒ CI 的 Go 步骤报「全绿」，而 **每 5 个测试就有 1 个根本没执行**。
⇒ 主因极其集中：

| 原因 | 条数 | 占比 |
|---|---|---|
| ⛔ **`POCKET_TEST_POSTGRES_DSN` 未设** | **450** | **70.9%** |
| 其它 env（`POCKET_LIVE_GATEWAY=1` / `POCKET_DIAG_*` / `POCKET_REAL_MAIL_DSN` / 字体 …）| 96 | 15.1% |
| helper 内的 `Skipf` | 89 | 14.0% |

⇒ ⛔ **`internal/db` 一个包就 324 条跳过** ⇒ 该包几乎全是 PG 测试。
⚠⚠ **订正**：本文初稿写「它的测试覆盖历史是 0」—— ⛔ **那是过度推断，已作废**。
　实测只支持「**本次无 `POCKET_TEST_POSTGRES_DSN` 的运行里它们全部被跳过**」；
　而 `internal/server/pg_test_isolation_guard_test.go` 头注释记录了 **2026-10-02 的实测**，
　`git log` 也有 5 个 commit 在反复修这些测试 ⇒ ⭐ **它们历史上跑过，还被修过**。

### 解锁成本很低（已读源码确认，非猜测）

`newIsolatedPGPool` / `testPGDSN` 都读 `POCKET_TEST_POSTGRES_DSN`，
拿到 DSN 后**自己建隔离池** ⇒ **只要一个能连的空 PG 实例，不需要预建 schema**（全仓 140 处引用它）。

本机条件**已具备**（只读探测，未启动任何东西）：
`/opt/homebrew/opt/postgresql@17` ✅ · `libpq` 的 `initdb/pg_ctl/psql` ✅ · **Docker 29.8.0 在跑** ✅

### ⛔ 请你拍的是「**要不要现在知道它们坏到什么程度**」

⚠ **关键读数，订正后**：
- ✅ 编译没问题（`go vet ./...` 无输出，`go build ./...` OK）；
- ✅ ⭐ **PG 隔离护栏是活的**：`internal/server` 那 10 个判据实测 **10/10 PASS、零 SKIP**
  （静态判据、不连库 ⇒ 在 CI 里真实生效）；
- ⚠ **风险陈述已被两次收窄（§777.2 / §779.4，读数在细化）**：
- ⛔ 不能再说「因为它们从未跑过所以必然大面积红」（2026-10-02 有实测证据说明它们跑过并被修过）；
- ⛔ 也不能说「schema 都在所以不会红」——
  ⚠ **§779 用 `go/ast` 做完对账后，只验了「表」这一层**：74 张建表 / 生产 71，
  **回溯订正（§780）**：已抽取并核对的候选中未发现默认路径缺表证据，**不保证**起 PG 后没有 `relation does not exist`；DDL 两版计数一致不能证明引用侧完整；
- ⚠⚠ **「列」层尝试未做成（§780）**：投影归属正控仅 **1/5 PASS**，schema、带点列、JOIN、子查询均失败，因此不能用这版量具排除缺列；而列漏查恰恰是本仓**真实发生过**的缺陷类型
  （`store_getbyid_columns_guard_test.go` 就是为 `message_id` / `body_purged` 列漏查而立的）；
- ⇒ ⭐ **「第一次跑会红多少」只能靠实跑测出**；本次静态对账只缩小了已核对候选的疑点，未完成全量缺表/缺列排除。
  ⇒ **风险因此比初稿写的小，但仍然是未知的**。

⚠⚠⚠ **起 PG 前必读的三条安全前置**（§777.4）—— 这不是「起个库就行」：

1. **DSN 必须指向空的、专用的库**，绝不能指向生产库或生产的 `search_path`。
   ⛔ 依据：commit `0119a735` 的标题就是「测试回落读 `POCKET_POSTGRES_DSN` **会在生产库建表建 schema**」，
   而护栏注释里的**献祭 schema 对照实验**记录了 `customer-success-manager` 被 `DELETE` 掉
   —— ⚠ **本仓历史上真实发生过**。
2. ⭐ **第一次跑之前先跑这道静态门**（不连库，几秒）：
   `go test ./internal/server -run TestPGTestsNeverTargetTheProductionSchema`
   ⚠⚠ **因为这道门保护的是 CI，不保护本机第一次跑** —— 测试在你本机跑时它还没机会介入。
3. ⚠ 门里那张「不隔离 schema」豁免表，逐条看它登记的写操作（规则 4 就是为此存在）。

⇒ 这是**工程权衡**不是事实问题：现在解锁 = 立刻拿到 450 条的真实健康度（可能同时新增修复项）；
　不解锁 = 继续「全绿」，但那个绿覆盖不到 1/5 的测试。

⛔ **本轮没有起 PG** —— 属会话级决策，需属主授权。

⭐ 顺带修正一处可能的误解：`§467` 那道 skip 台账门**只护 `internal/stt` 一个包**
（`filepath.Glob("*.go")` 没有 `../`）⇒ 它覆盖的是 635 条里的 **10 条 ≈ 1.6%**，
标题里那个「9 个」是 **stt 包内**的数。已在设计文档 §467 内插限定块。

⭐ 基线清单已落盘（仓库外）：`/Users/xutaohuang/.minimax/tmp/s774/skip-baseline-635.txt`
（639 行 = 3 行头 + 635 条 + 尾换行，格式 `<包>\t<测试名>\t<skip 原因>`）
⇒ 起 PG 后用 `diff` 可**逐条**回答「解锁了多少、哪些仍被挡」。

### 附属待拍板

- **§467 的 skip 台账要不要扩到全仓？** ⚠ 静态层面有个硬坑已量化：
  **只扫测试函数体会低估 4 倍**（166 vs 635），因为多数 store 类测试的 skip 写在
  **28 个 helper** 里（`newTestStore` / `newTestPGStore` / `newWorkspaceTestStore` / `newIsolatedPGPool` …）。
  ⇒ 扩到全仓前必须先解决「怎么在静态层面追到 helper 里的 skip」，否则会建出一张漏掉 2/3 目标的门。
