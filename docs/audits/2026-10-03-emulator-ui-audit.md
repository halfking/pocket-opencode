# 2026-10-03 模拟器 UI 全量审计

> 用户诉求：安装 Android 模拟器 → 拉最新代码合并 → 编译测试通过 → 部署到模拟器
> → 逐功能点测试（键盘联动 / 多行输入 / 图标 / 后台存活）→ 修正 → 回归验证。

> **证据文件位置**：本文引用的 `test-evidence/2026-10-03-audit/*.png` 是本轮
> 运行时产物，该目录按仓库约定在 `.gitignore` 里（`test-evidence/`），
> **不进版本库**——与 `logs/` 同一处理。复现方式见文末 §5。

## 0. 环境（本轮从零装起）

| 组件 | 版本 | 说明 |
|---|---|---|
| JDK | Temurin 17.0.20.1 + **21.0.12.1** | 21 是 `capacitor-camera` 的 toolchain 要求，17 装完 Gradle 直接失败 |
| cmdline-tools | google 11076708 | 官方包；`winget` 源损坏（`0x8a15000f`）不可用 |
| platform-tools / build-tools 36 / platforms;android-36 | — | compileSdk=36 |
| emulator + system-image | `android-35;google_apis;x86_64`（3.5 GB） | |
| AVD | `pocket-test`（pixel_6, 3072 MB, swiftshader） | |

**装包踩的三个坑**（都会静默失败，值得记）：

1. `sdkmanager` 报「Computing updates」1-2 秒就 exit 0 —— **什么都没装**。
   原因：licenses 没被真正接受（`--licenses` 管道喂 y 在本机不生效）。
   处置：直接写 `$ANDROID_HOME/licenses/` 下的哈希文件。
2. 管道 `| Select-Object -Last 3` 会吞掉 sdkmanager 的错误输出，
   让人以为装成功了。**判断安装成功要看磁盘上有没有文件，不看退出码。**
3. `gradle-wrapper.properties` 的 `networkTimeout=10000` 对 214 MB 的
   gradle-8.14.3-all.zip 必然超时。已提到 180000。

**加速可用**：`emulator -accel-check` 报 `WHPX(10.0.18362) is installed and
usable`，实测 **70 秒** `boot_completed=1`。

> 这里差点误判：`Win32_Processor` 的 `VirtualizationFirmwareEnabled=False`
> （本机是 VMware 里的来宾，Hyper-V 也开着），按那组字段应该推断
> 「嵌套虚拟化没暴露 → 模拟器起不来」。**实际 WHPX 是可用的。**
> 教训：这类推断要用工具自己的探测（`-accel-check`）+ 真的 boot 一次来确认，
> 不要拿 CIM 字段当定论。

### 0.1 模拟器本身在本机会反复掉线（后半程的主要时间黑洞）

`pocket-test` 用了大半程后开始出问题：`adb devices` 直接空、`device` 与
`offline` 之间反复跳、`logcat` 里打出 `BOOT_FAILURE` 与
`flags_health_check ... resetting flags`（说明系统判定 boot 循环）。
宿主只有 15.9 GB 内存，qemu 一路涨到 5.9 GB、可用内存掉到 1.9 GB 时
boot 会卡在 `init.svc.bootanim=running` 十分钟以上。

处置与结论：

1. 强杀 qemu 后内存立刻回到 7.4 GB，冷启动恢复正常 —— **先看内存再怪别的**。
2. `emulator -wipe-data` 被安全策略拦下（不可逆销毁数据盘），**没有绕过**。
   改用**新建 AVD**（`pocket-test2`，纯增量、不销毁任何东西），
   `avdmanager create avd -k system-images;android-35;google_apis;x86_64 -d pixel_6`，
   再把 `hw.ramSize` / `hw.gpu.*` 写进 `config.ini`。这条比 wipe 干净。
3. `adb kill-server` 会让设备列表整个清空，qemu 会在几秒内重新注册，
   但**别在 boot 期间反复 kill/start-server**，会把它推回 offline。
4. 新 AVD **没有网络**：`ip route` 空、`wlan0` 是 `NO-CARRIER`、
   `ping 10.0.2.2` 报 `Network is unreachable`。而 `10.0.2.2` 是模拟器
   访问宿主的别名，**登录就卡在这里**。

第 4 条的绕法（本轮实际用上的）：`adb reverse tcp:8088 tcp:8088` +
把 App 的 API 基址指到 `http://127.0.0.1:8088`。`adb reverse` 走 adb
传输而不是设备网络栈，所以 `wlan0` 挂着也能通——页面里
`fetch('http://127.0.0.1:8088/healthz')` 返回 200 `ok` 即为证。

> 注意 API 基址的优先级是 `localStorage > VITE_API_BASE > 同源`
> （`src/config/api-base.ts`）。构建期注入 `VITE_API_BASE` 在这台设备上
> 没生效（App 实际解析到 `https://pocket.itestu.cn`，登录因此失败），
> **最省事的做法是直接写 `localStorage.pocket_api_base`** 再 reload，
> 不用为了换地址重打一次包。

## 1. 代码基线

- 拉取前本地 main 落后 origin/main **243 个提交**、无本地独有提交 → `--ff-only` 干净快进到 `cafb3d6c`。
- 合并后基线：`typecheck` 干净、`test:all` **1804/1804 全绿**（196 个测试文件全部实际执行）。

## 2. 逐条需求的核查结果

### 需求① 键盘不能遮盖正在输入的框 —— 复现了真实缺陷，已修

模拟器 API 35 上点密码框，键盘弹起后**密码框被键盘上沿切掉一半**
（`02-keyboard-password.png`）。CDP 读运行时状态，根因确定：

```
innerHeight           = 915
visualViewport.height = 915      ← 与键盘收起时完全相同
--kb-inset            = ""       ← 从未下发
html.kb-open          = false
```

**键盘弹起时视口毫无变化。** 原 `useKeyboardInset` 只用
`baseline - min(innerHeight, vv.height)` 建模键盘高度，这条路径上恒为 0，
机制整体不触发。

原生侧根因：`MainActivity` 是 edge-to-edge（`setDecorFitsSystemWindows(false)`）、
manifest 未声明 `adjustResize`，而它的 `setOnApplyWindowInsetsListener`
**只读了 `Type.systemBars()` 与 `Type.mandatorySystemGestures()`，没读
`Type.ime()`**。于是 JS 侧没有任何键盘信号。

这**不是模拟器特例**——`useKeyboardInset.ts` 修复前的文件头自己就写着
「真机 Android 15 WebView 不缩、键盘直接盖在视口上（overlay 路径）」。
也就是说文档早就记录了这条路径，而实现建模的是另外两条。实测把它补齐了。

**修法**（沿用项目已有的 `--android-safe-top/bottom` 注入模式）：

1. `MainActivity` 补 `Type.ime()` → 换算 CSS px 注入 `--android-ime-inset`；
   并在 `onPostResume` 补一次重放，避免冷启动即聚焦时初值缺失。
2. `useKeyboardInset` 以原生值为**权威信号**，视口差模型降为回落路径
   （iOS / 桌面 / 无原生层）。两者**取一方为准而非相加**——resize 路径下
   两者描述同一段高度，相加会翻倍。
3. 用 `MutationObserver` 盯 `<html>` 的 style 属性接收变化（CSS 变量变更
   不派发事件）。
4. 顺带把 3 个独立全屏表单页（登录 / 注册 / 找回密码）的垂直居中从
   `align-items:center` 换成容器 `margin:auto`——前者内容高于容器时
   两端同时溢出、顶部滚不回去。

**新增护栏**：`useKeyboardInset.test.mjs` 9 条（双路径建模、迟滞、
聚焦字段顶出、非文本控件不误触发）+ `keyboard-avoidance-prereq.test.mjs` 4 条
（原生读 ime / JS 消费 / AppLayout 滚动前提 / 居中方式）。

> 这套机制此前**一条测试都没有**——1804 个用例里零覆盖。

> **⚠️ 这条结论后来被推翻了**：上面这一轮只在**登录 / 注册**两页验过，
> 而那两页是全屏固定定位表单、不吃根布局的 flex 链。旗舰页 `/ai-chat`
> （自管滚动 + 底部停靠输入区）在键盘弹起时工具行是整条消失的，
> 根因见 §7.5。**教训**：「某两页验过」不能推广成「机制已修好」，
> 凡是带停靠输入区的页面都得在键盘弹起态单独走一遍。

### 需求② 多行输入区域 —— 修了一处「文档承诺但没实现」+ 补了全局下限

`UnifiedComposer.vue` 文件头第 11 行白纸黑字写着
「标准模式：自适应增高（上限 40vh 后滚动）」，而 `onInput` 只发事件、
**没有任何高度逻辑**。多行正文写到第 4 行以后就藏进内部滚动条后面。

`LocalAgentView.vue` 的 `.draft` 是同一个病（`rows=1` + `max-height:120px`
+ `resize:none` + 无长高）。

**修法**：抽出 `composables/useAutoGrowTextarea.ts`（内容驱动高度，
上限留在 CSS 不在 JS 复制一份），接入这两处 + 审批面板的紧凑备注框；
`App.vue` 加全局下限 `textarea:not(.textarea-compact) { min-height:72px;
resize:vertical }`，并给「列表里每项一个」的紧凑型留 `.textarea-compact`
豁免（否则 5 张待批卡片会被撑出 480px 空白）。

**另外发现**：`components/base/Textarea.vue` 设计得不错（min-height 三档 +
`resize: vertical` + focus ring），但**全仓 0 处引用**，是死代码——
21 处 textarea 全部是裸标签各自手写尺寸。

**全仓普查（12 个路由，实测不是推断）**：判据写成可量化的
「`scrollHeight <= clientHeight + 1` 才算内容装得下」，往每个框灌一段
现实长度的文本再量，**只找到 6 个多行框**（其余路由无 textarea）：

| 位置 | 类型 | 默认高 | 装得下 | 长高 |
|---|---|---|---|---|
| `/ai-chat` 输入框 | composer(autoGrow) | 99px | ✅ | ✅ 99→126→331 |
| `/notes/new` 标题 | composer(single-line) | 44px | ✅ | ✅ 44→118→275 |
| `/notes/new` 正文 | composer(autoGrow) | 101px | ✅ | ✅ 101→126→331 |
| `/local-agent` `.draft` | composer(autoGrow) | 40px | ⚠️ 裁 2px | ✅ 40→121 |
| `/flashcards/new` Front | plain | 100px | ✅ | 手动 resize |
| `/flashcards/new` Back | plain | 139px | ✅ | 手动 resize |

唯一失分项是 `/local-agent` 那 2px → §7.6。
**差点改错的地方**：普查第一眼看到「笔记标题框只有 44px、resize: none」
像是需求② 说的「太小」，但灌长文本后它 44→118→275 跟得很稳，且标题
语义上就是单行——真按「44px 不好看」去垫高才是把判据拍成了审美。
所以判据必须是「内容装不装得下」，不是「看起来大不大」。

### 需求③ 图标与方形背景 —— 全部通过（17 条路由实测）

- `check:icons`：134 个名字全在字体子集内，`check:icons:visual` 生成
  132 格目视验证页。
- **实际在浏览器渲染逐格核对**：130 个真实图标全部合出字形，无
  `LIGHT_MODE` 那种文本回退；`name` / `starred` 显示为文本，与
  `check-icon-font.mjs` 判定的 2 个假阳性一致（`starred` 来自
  `item.status === 'starred'` 的比较值，压根不是图标名）。两种独立方法互证。
- 字体侧防护到位：完整 3.45 MB 字体（非早期 8.8 KB 子集，那版真机露馅过）、
  `font-display: block`、`overflow: hidden` 兜底。
- **方形背景已逐屏走完**（解锁主密码后，17 条路由实测）。判据从「机制会
  怎样坏」反推，不是审美阈值：

  | 判据 | 含义 | 实测 |
  |---|---|---|
  | 同屏 `font-size > 中位数 × 3` | 「异常大」的图标 | **0**（各屏 14–24px，同屏内自洽） |
  | 字形行盒高 > 最近裁切祖先的内容高 | 「显示不完整」 | **0** |
  | 图标在近方形背景里偏移 > 3px | 背景里没居中 | **0**（2 条告警是假阳性，见下） |
  | 背景边长 > 72px | 方形背景异常大 | **0**（同上） |

  17 条路由的 `gated` 全部为 `false`（含此前被主密码门挡住的
  `/notes`、`/notes/new`、`/vault`）。

- **上一轮留的判断被数据推翻**：此前记的是「`.icon-btn` 没有全局最小尺寸
  约束、尺寸全靠各视图手写」，暗示可能有尺寸失控。**实测没有一处出问题**：
  设置页的方形背景统一 ~30 CSS px、图标居中、字形完整（`41-icon-settings.png`）。
  「没有全局约束」是代码结构事实，「会导致显示异常」是没有证据的推断——
  这两条不能混为一谈。

> 普查过程中自己写错过一次判据并当场否掉：初版用「可点区域 < 32px」一刀切，
> 结果把 `.chip`（45×30）、`.link-btn`（62×26）、`.icon-pill`（56×30）
> 全报成过小。chip 高 30px 是正常控件密度，不是缺陷。**需求③ 问的是
> 「方形背景里的图标」，判据就该锁在这类容器上，而不是所有可点元素。**
> 收紧后又冒出 2 条 `.settings-section`（358×363）告警，核实它是普通
> `DIV`、不可点、无 role、6 个子元素——分区卡片，不是图标背景。

### 需求④ 大模型/录音原生化 + 后台存活 —— 代码层核查 + **设备实测都通过**

代码层：

- Manifest：`MeetingRecordService`（`foregroundServiceType="microphone"`）、
  `AiStreamService`（`dataSync`）、`FOREGROUND_SERVICE*` / `WAKE_LOCK` /
  `POST_NOTIFICATIONS` 齐全，`EmailFetchReceiver` 接 `BOOT_COMPLETED` +
  `MY_PACKAGE_REPLACED`。
- `MainActivity` 的 `configChanges` 含 `keyboardHidden|keyboard`，
  键盘弹起不会重建 WebView（键盘避让的前提之一）。
- `AiStreamService`：30 分钟 WakeLock（与设计文档的 30 min 后台矩阵同量级）、
  `update` 时续期、`START_STICKY`。
- `MeetingRecordService`：系统重启（intent==null）时返回 `START_NOT_STICKY`
  且不开录——避免录进一个空意图重启。
- `BackgroundMicPlugin` 注释记录了 2026-10-01 修的一个真实坑：
  `startForegroundService` 后立刻 `resolve()`，遇 `SecurityException` 变无声崩溃。
- JS 侧 `aiStreamKeepalive.ts` 决策表完整（activeCount>0 && isHidden → start；
  幂等去重；30s 心跳兜底流自然结束）。
- 相关测试：`test:native:all` **183/183**、`test:stt` **97/97**、
  `unmount-must-not-abort-streams` 6/6，全绿。

**设备实测（本节推翻了本轮早先的「环境受限做不了」结论）**：

> 早先我判断「模拟器 `-no-audio` 无音频输入源录不了音、未配网关发不出流式
> 请求，所以只能做代码层核查」。**两个前提都只测了一半就下了结论**——
> 录音权限其实 `granted=true`、`dumpsys media.audio_policy` 里有 4 个输入设备，
> 录音能跑；流式则可以用一个本地慢速 SSE 端点代替真实网关。
> 这与本轮开头「`VirtualizationFirmwareEnabled=False` → 模拟器起不来」是
> **同一类错误**：拿环境推断当定论，不实际探一下就收工。

造上游的办法（不改动仓库代码）：`HttpListener` 起一个永结束的 SSE
（每 2 秒吐一行），`adb reverse tcp:8099 tcp:8099` 让设备能访问，再把 App
设置里的网关地址指到 `http://127.0.0.1:8099/v1`。
（注意 API 基址优先级是 `localStorage > VITE_API_BASE > 同源`，
构建期注入在设备上没生效，写 `localStorage.pocket_api_base` 最省事。）

**流式这条腿**（`/ai-chat` 发送后按 HOME 切后台 85 秒）：

| 判据 | 实测 |
|---|---|
| 后台窗口内服务端仍在推流 | **39 个 chunk 到达**（18:13:40–18:15:05，每 2 秒一个） |
| App 进程未被杀掉 | `pid = 2122` 全程未变（5 次采样） |
| 后台期有原生前台服务 | `AiStreamService` `isForeground=true`、`types=0x1`（DATA_SYNC）、通知 `ONGOING_EVENT\|NO_CLEAR` |
| 机制层终态符合仓库既有约定 | 出现 retry chip「上游模型不可用，已切换到 … 重试…」，`docs/guides/E2E_CI.md:52` 明确「90s 后下发 `context deadline exceeded` 也是合法终态」 |

> 探针本身也翻过两次车，都留在这里：`chunk` 计数**不是单调的**——App 会重试，
> 每次重试是新请求、编号从 1 重数，所以「chunk 增长 = 0」是度量缺陷而不是
> 失败，改成「按时间戳数窗口内的 chunk 行数」才得到 39 这个真值。
> 另外 `Write-Output` 与 `return` 混在同一个输出流里会让 `$a.chunk` 取到 `$null`。

**录音这条腿**（`/#/meetings` → 直接调 `BackgroundMic.start()`）：

| 判据 | 实测 |
|---|---|
| 插件 Promise 有结论 | `resolved`（不是静默挂起） |
| 服务真的进了前台 | `MeetingRecordService` `isForeground=true`、`types=0x00000080`（MICROPHONE）、通知 `ONGOING_EVENT\|NO_CLEAR` |
| 切后台 80 秒存活 | T0/T1(+5s)/T2(+40s)/T3(+80s)/T4 五次采样均 `service=yes isForeground=true`，`pid=2122` 未变 |
| 录音计时跨后台继续走 | 回前台后 **02:18 → 02:30**（12 秒真实推进，非冻结） |
| 失败是诚实报的 | 转写失败时明确显示「网关暂无可用的语音转写模型（扫描失败：… resolved address is not allowed）」+「外部语音转写服务未配置 API Key」，**没有假装成功** |

最后一条是这一腿真正要验的东西。`BackgroundMicPlugin` 的文件头把
「显示正在录音、切后台也不提示、一整场没有声音」列为典型静默失效；
实测下来这条路径的**成功与失败都给出了明确结论**，符合它自己声明的契约。

**30 分钟长时程（16 次采样 / 1822 秒，覆盖 30.4 分钟）**

| 判据 | 实测 |
|---|---|
| 录音前台服务全程存活 | **16/16 次** `mic=MIC` + `MIC_TYPE`（`types=0x00000080`） |
| 任一时刻都有前台服务 | **16/16 次** `fgCount ≥ 1` |
| 进程从未被杀 | **16/16 次** `pid=2122`，全程唯一值 |
| 模拟器无内存泄漏 | qemu 常驻 3262–3300 MB，30 分钟内无增长趋势 |
| 流式 chunk 推进 | `7 → 46`（+39），集中在前 140 秒 |

跑完之后又复查了一次，同一个服务实例的 `createTime` 已经是 **-1h0m0s**
（连续跑了整整 1 小时）、仍是 `isForeground=true`；同期 App 进程
`startUpTime=-2h18m`（2 小时 18 分未重启）。**要求是 30 分钟，实际超出。**

> 采样表里 `ai=---` 不是失败：App 的流式请求有 90s 总预算（见
> `docs/design/2026-09-10-sse-120s-watchdog-diagnosis.md`），到点终止后
> `aiStreamKeepalive` **正确地**把 `AiStreamService` 停掉了——有活跃流才
> 拉前台服务，没有就不占前台。「流式请求结束」与「服务被停掉」是配套行为，
> 把 `ai=---` 记成失败才是误判。chunk 计数停在 46 也是同一个原因。

> 又一次差点误报：中途看到页面出现「🎤 录音」而服务列表里没有
> `MeetingRecordService`，差点当成静默失效。核实后发现那只是**开始录音的
> 入口按钮**，不是进行中状态——真正的进行中状态带实时计时（「录音中 00:04」）。
> 和需求② 里「44px 标题框」那次一样：**先确认现象是什么，再判定是不是缺陷。**

## 3. 验证

| 项目 | 命令 | 结果 |
|---|---|---|
| 类型检查 | `npm run typecheck` | EXIT=0 |
| 前端全量 | `npm run test:all` | **pass 1869 / fail 0**（基线 1804 + 新增 40），206 个测试文件全部实际执行 |
| Go 构建 / 静态检查 | `go build ./...` / `go vet ./...` | EXIT=0 / EXIT=0（本轮 6 个缺陷全在前端，Go 侧未再改动） |
| 移动端构建 | `node scripts/build-mobile.mjs android dev` | OK，sanity check 通过 |
| APK | `gradlew assembleDebug` | **BUILD SUCCESSFUL**，32.7 MB |
| 安装 | `adb install -r -g` | Success |
| 启动 | `am start` | `mCurrentFocus=…MainActivity` |

判据本身的可靠性用**变异测试**验过：把每个修复改回坏写法，确认对应断言
真的会红。13 条变异全部被拦住（清单见 §8.1），避免留下「永远绿」的假护栏。

### 3.1 设备实测的六条硬数据

| 缺陷 | 修复前 | 修复后 | 证据截图 |
|---|---|---|---|
| 键盘无信号 | `--kb-inset=""`、`innerHeight === vv.height` | `--android-ime-inset=336.38`、`kbInset=336.38` | `02-keyboard-password.png` |
| fixed 遮罩不吃键盘 | `.dialog-mask` 底 915；按钮 bottom 600 vs 键盘上沿 579 | 遮罩底边 = 键盘上沿 | `08-after-login.png` |
| 下拉指示器叠内容 | 与 `.work-filters` 完全重叠，opacity 0.25 | `opacity: 0`，指示器底 109 < 内容顶 153 | `09-after-master.png` |
| toast 压工具行 | 底边 255px vs 输入区顶边 289.7px（重叠 34.7px） | `worstOverlapPx = 0`（295px vs 289.7px） | `21-toast-clears-composer.png` |
| **两个 `#app`** | `.app-layout` 193.286 = 529.667 − 336.381，工具行整条消失 | `#app` 数量 1；`.app-root` = 529.667；工具行完整可见 | `26-…-clean.png` → `29-aichat-kb-fixed.png` |
| autoGrow 裁 2px | `scrollHeight 121 / clientHeight 119`，`hiddenPx 2` | `clientHeight 121`，`hiddenPx 0`，`fits: true` | `35-…-sweep.png` → `37-…-border-fix.png` |
| 流式切后台 | — | 后台 85s 内 **39 个 chunk** 到达、`pid` 未变、`AiStreamService isForeground=true` | `logs/sse-probe.log` |
| 录音切后台 | — | `MeetingRecordService types=0x80`，后台 80s 存活，计时 **02:18 → 02:30** | `logs/bg-survival.txt` |
| **录音 30 分钟长时程** | — | **16/16 次采样**服务前台存活、`pid` 全程 2122 未变；跑完复查已连续 1 小时 | `logs/longrun-30min.txt` |

判据本身的可靠性用**变异测试**验过：把每个修复改回坏写法，确认对应断言
真的会红。**13 条变异全部被拦住**（清单见 §8.1），避免留下「永远绿」的假护栏。

### 3.2 对当前 `main` 的再验证（上游又前进了 32 个提交之后）

本节之前的所有设备数据都打在**当时那一版分支**上。之后 `origin/main` 又前进了
32 个提交，其中 `5026bf78` 是一次**全仓 gofmt/CRLF 归一 + 新增 gofmt 门禁与
pre-push 车道**——这类改动有把前端行为冲掉的可能，所以从当前 `main` 重新
构建、重新部署、重新实测一轮。

**构建与门禁**

| 项目 | 结果 |
|---|---|
| `npm run check:crlf-needles` | EXIT=0，209 个测试文件的跨行 needle 全部实测命中（CRLF 归一没打断断言） |
| `npm run typecheck` | EXIT=0 |
| `npm run test:all` | **1875 pass / 0 fail**（较上一轮 1874 多 1，增量来自上游提交） |
| `go build ./...` | EXIT=0 |
| `gradlew assembleDebug` | BUILD SUCCESSFUL，33 MB |
| `adb install -r -g` | Success，新 pid **5116** |

**四条需求在当前 main 上的实测**

| 需求 | 观测 | 结论 |
|---|---|---|
| ① 键盘 | `/ai-chat` 键盘弹起：`.app-layout` 高 **529.7**（= 文档记录的修复后期望值 529.667）、根布局底 578.7 = 键盘上沿 578.6、`uc.bottom 526.7 ≤ 578.6`，整条工具行在键盘上方（截图 `29-aichat-kb-fixed.png`） | 通过 |
| ② 多行输入 | 12 路由 **6 个**多行框全部命中；`hiddenWithoutResize = []`、`hiddenButResizable = []`；`/local-agent` 写入后 `grewH 122.5 / clientH 121 / hiddenPx 0 / fits true` | 通过 |
| ③ 图标 | 17 路由 **137 个图标 / 29 个方形背景**：字形裁切 0、盒子异常大 0、偏心 0、异常大字号 0 | 通过 |
| ④ 后台存活 · 录音腿 | 切后台 **7/7 采样 / 123 秒**：`MeetingRecordService isForeground=true types=0x00000080`，`pid` 全程 5116 未变；回前台后 `createTime=-2m57s`（连续未中断），停止后服务与通知均已撤 | 通过 |
| ④ 后台存活 · 流式腿 | 切后台 **39 个 chunk** 到达（bg+30s 15 个 / bg+60s 30 个 / bg+90s 39 个），`AiStreamService isForeground=true types=0x00000001`（DATA_SYNC），`pid` 全程 5116 未变。J1 流式在后台继续推进 / J2 进程未被杀 / J3 后台有前台服务 —— **三条全 True** | 通过 |

流式腿的 chunk **按时间戳落在后台窗口内的行数**统计，而不是按 `chunk N` 的
编号差。编号非单调：App 重试就是新请求，编号从 1 重数，上一轮就是被这个
坑误导过一次。39 个 chunk 与上一轮完全一致，可复现。

采样表里 `svc=none` 出现在 T0 与 T4 两处，都不是故障：T0 是请求刚发出、
前台服务还没起来；T4 是 90s 总预算到点后 `aiStreamKeepalive` 正确收掉了
`AiStreamService`——「请求结束」与「服务被停掉」是配套行为。

### 3.3 顺带发现：上游新加的 gofmt 门禁在 `main` 上是红的

`5026bf78` 引入 gofmt 门禁后，`main` 上一直有 2 个文件在**真债**名单里，
`npm run check:gofmt` 一直 EXIT=1：

```
归一化后仍不 gofmt（真债）: 2  2 (0.2%)
  backend/cmd/invoiceprobe/main.go
  backend/internal/server/pg_test_isolation_guard_test.go
```

两处都是纯格式，不含语义：`main.go` 是 gofmt 要在以全角括号开头的行注释后
补空格（5 行），`pg_test_isolation_guard_test.go` 是两行连续空行并成一行。

**别把这条读成「911 个文件行尾不干净」**：门禁自己分了类，911 是纯行尾伪债
（工作区 `core.autocrlf=true` 的必然产物，git 归一化后并不脏），真债只有这
2 个。门禁自己的提示也写了「`gofmt -w` 一次就干净了」在本机是错的，所以按
提示跑了两遍。

修复走独立分支 `fix/2026-10-03-gofmt-gate-red`（与本审计分支无关，不混），
`check:gofmt` EXIT=0、真债 2 → 0，`go build` / `go vet` 均 EXIT=0。

### 3.4 这一轮真正抓到的问题：App 空闲自动上锁，把普查静默削掉一半

首轮再验证跑出来「17 路由只有 71 个图标、其中 8 条路由 `i0`」。差点按
「这些页面本来就没图标」收工。实际去查 `landed` 才发现 8 条路由全部落到了
`#/login?returnTo=…&unlock=1`——**App 空闲自动上锁了**，锁屏页自然没有图标，
而且普查脚本不检查这一点，于是 8 条路由被静默跳过。解锁后重跑：

| | 首轮（被锁屏削掉） | 解锁后 |
|---|---|---|
| 图标数 | 71 | **137** |
| 有效路由 | 9 / 17 | **17 / 17** |
| 多行框数 | 4 | **6** |

判据修法：普查结果里逐路由记录 `locked = location.hash.includes('unlock=1')`
并汇总上报，让「没量到」和「量到了是 0」在输出上可区分——否则两者长得一模一样。

**另有一处度量错误已留档**：`git diff --stat origin/main..HEAD`（两点式）
给出「197 个文件 / 3844 删除」，一度判定分支被污染。两点式比较的是两个
**端点**，含上游 32 个提交的落差；`origin/main...HEAD`（三点式）才是
「本分支加了什么」。两者结论差了一个数量级。

## 4. 遗留

1. **需求④ 的 30 分钟长时程已跑通**（16 次采样 / 30.4 分钟，录音前台服务
   16/16 存活、进程 pid 全程未变；跑完后复查服务已连续运行 1 小时）。
   数据见 §2 需求④。此前那条「宿主内存扛不住」的判断同样**只探了一半**——
   杀掉本轮构建遗留的 Gradle 守护进程后腾出 4.3 GB，模拟器全程稳定、
   qemu 内存 30 分钟内无增长。**至此四条需求全部有设备实测支撑**，
   没有「只做了代码层核查」的遗留项。
2. **需求③ 的「方形背景逐屏检查」已走完**（解锁主密码后 17 条路由实测，
   结论见 §2 需求③：异常大 0 / 字形裁切 0 / 背景尺寸异常 0）。
   此前记的「`.icon-btn` 没有全局最小尺寸约束」是代码结构事实，
   但**实测不构成显示缺陷**——结构脆弱 ≠ 当前有问题。若日后新增图标按钮
   仍建议补一条全局下限，那是预防性加固，不是补现在的洞。
3. `components/base/Textarea.vue` 仍是死代码。本轮用全局 CSS 下限代替了
   「统一到基类组件」的做法——要真统一得改 21 处视图，是独立一件事。
4. `.env.android-dev` 是本轮新建的本地配置（gitignored），指向
   `http://10.0.2.2:8088`（模拟器访问宿主的别名）。
5. `gradle-wrapper.properties` 的 `networkTimeout` 10s → 180s 是本轮改的，
   10s 对 214 MB 分发包必然超时，属于真实缺陷，建议保留。
6. **模拟器进程会自行退出**（本轮后半段撞到 5 次，`adb devices` 直接空、
   `device`/`offline` 反复跳、logcat 打 `BOOT_FAILURE`）。宿主 15.9 GB 内存
   被 qemu 吃到只剩 1.9 GB 时 boot 会卡十分钟以上；强杀后内存立刻回到
   7.4 GB 并恢复正常。批量走查时要把「设备掉线」当正常故障重新
   `adb wait-for-device`，别误读成应用崩溃。处置细节见 §0.1。
   最后一次带 `-memory 2048` 冷启动 **42 秒** `boot_completed=1`，
   六个缺陷的设备复验数据都在这台机器上取到。
7. **本轮发现的需求① 覆盖盲区值得单独记**：键盘避让此前只在登录/注册页
   验过（那两页是全屏固定定位表单，不吃根布局的 flex 链），而
   `/ai-chat` 这种「自管滚动 + 底部停靠输入区」的页面当时是破的（§7.5）。
   也就是说「登录页验过」不能推广成「键盘避让已修好」——
   后面新增任何带输入区的停靠式页面，都要在**键盘弹起态**下走一遍。

## 5. 复现本轮实测

```powershell
# 工具链（JDK 21 是 capacitor-camera 的 toolchain 要求）
$env:JAVA_HOME = 'C:\Program Files\AdoptOpenJDK\jdk-21.0.12.1+1'
$env:ANDROID_HOME = "$env:LOCALAPPDATA\Android"
# licenses 直接写哈希文件，sdkmanager --licenses 在本机不生效

# 模拟器
emulator -accel-check                 # 期望：WHPX ... is installed and usable
emulator -avd pocket-test -no-snapshot -gpu swiftshader_indirect -no-audio
adb wait-for-device
adb shell 'while [ "$(getprop sys.boot_completed)" != 1 ]; do sleep 2; done'   # 约 70s

# 构建部署
cd frontend; node scripts/build-mobile.mjs android dev
cd android; .\gradlew assembleDebug
adb install -r -g app\build\outputs\apk\debug\app-debug.apk
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity

# 读运行时状态（这是定位键盘缺陷的关键手段，比截图精确）
adb forward tcp:9222 localabstract:webview_devtools_remote_<pid>
#   pid 取自：adb shell cat /proc/net/unix | findstr webview_devtools
# 然后 GET http://127.0.0.1:9222/json/list 拿 webSocketDebuggerUrl，
# 用 Runtime.evaluate 读：
#   getComputedStyle(document.documentElement).getPropertyValue('--android-ime-inset')
#   document.documentElement.classList  /  window.innerHeight  /  visualViewport.height
```

**没有这一步就看不出键盘缺陷**：截图只能看到「被盖住了」，
读不到「为什么没让位」。本轮根因正是靠 `--kb-inset=""` +
`innerHeight===vv.height` 这两个运行时值定下来的。

## 6. 打通后端（登录后的功能页才能走）

`backend/start-dev.sh` 是 bash 脚本，Windows 上跑不了，需要在 PowerShell 里
复刻同一组环境变量。**本轮在这上面卡了四轮**，每一步都有明确的失败信号：

| 症状 | 真因 | 处置 |
|---|---|---|
| `sdkmanager`/`pocketd` 收不到 `POCKET_AUTH_PASS`，日志打 `dev auth bypass disabled` | 本工具的每次 `bash` 调用都是**全新 shell**，`cd` 与 shell state 不跨调用；`Start-Process` 继承的是**父进程那块陈旧环境块**，不是重新合并 User 作用域 | 把 `set VAR=... &&` 和启动放进**同一条 cmd 命令行** |
| login 仍 401，但日志里已无 WARN | 端口 8088 被**上一个没带环境变量的 pocketd** 占着，新进程 `bind` 失败，healthz 打到的是旧进程 | 杀干净所有 `pocketd.exe` + `cmd /c pocketd`，确认端口 free 再起 |
| `POCKET_REDCLAW_ADMIN_URL must be set` | 同上，`POCKET_AUTH_LEGACY_ONLY` 也没读到 —— 印证是环境块问题不是变量名问题 | 同上 |
| `[Environment]::SetEnvironmentVariable(...)` 循环里部分键没写进去 | 用 `foreach ($k in $vars.Keys)` 批量写时 `Set-Item env:` 抛错被 `-ErrorAction Continue` 吞掉，循环没跑完 | 逐个显式设置并**逐个回读校验** |

**教训**：进程级问题的日志要当第一现场看。上面四次我都在猜环境变量名，
而 `pocketd` 一启动就把「哪个变量没到」打在日志里了——先看日志能省掉三轮试错。

## 7. 本轮新发现并修复的六个缺陷

按发现顺序（都在模拟器上实测复现过）：

### 7.1 键盘避让在 overlay 路径上整体失效（`3e59227a`）

见 §2 需求①。原生没读 `Type.ime()`，视口无信号，机制恒为 0。

### 7.2 含输入框的 fixed 遮罩不消费 `--kb-inset`（`f08dd70b`）

登录成功后的「创建主密码」弹窗，底部「取消/确认」被键盘上沿切掉——
而这是唯一能继续的入口。

根因：`position:fixed; inset:0` 锚的是视口，键盘弹起时视口不变，于是浮层
底部仍停在键盘下。实测 `.dialog-mask` rectBottom=915（满视口）而
`#app` height=530（已收缩）；按钮 top 560 / bottom 600，键盘上沿 ≈ 579。

`BottomSheet.vue` 早有正确写法，全仓另有 7 处同类遮罩没跟上，已统一修掉
（7 行）。范围是量出来的：31 个 `position:fixed` 文件里，只有「遮罩规则 +
自身含 input/textarea」的 7 处真会跟键盘打架。

### 7.3 下拉刷新指示器静止时叠在内容上（`5b15e870`）

首页筛选 chip 区上面压着一行半透明灰字「下拉同步邮件」。实测
`.refresh-indicator` top 165/bottom 221 与 `.work-filters` top 152/bottom 214
完全重叠。

根因是两处叠在一起：`indicatorOffset = 56 - pullDistance` 在静止时得 **+56**
（方向写反，把自己推进内容区），而 `indicatorOpacity` 又保底 **0.25**
（位置错了还被「保证」看得见）。

**这一条最值得记的是它的形状**：两个 bug 叠加时，单独修任一个都不足以让
现象消失，很容易误判成「没修好」而继续瞎调。修法是两条一起改
（`min(0, d - H)` + `min(1, progress*1.4)`），并把「静止时位移 ≤ -56」
与「静止时不透明度 = 0」**分别**写成两条断言——只有这样才有一处坏了能立刻定位。

### 7.4 toast 压住整条输入区工具行（`478116bd`）

`/ai-chat` 点发送后弹的错误 toast「请先在「设置 → AI 网关」配置网关密钥」
盖住整条工具行（全屏 / 麦克风 / 相机 / 附件 / 角色 / ✨优化）以及发送按钮，
且 `.toast` 是 `pointer-events: auto`，**点不到下面的麦克风**。麦克风是需求④
的主打能力，被一条 3 秒提示盖住不可接受。

根因：`.toast { bottom: calc(var(--bottom-chrome-height) + var(--space-4)) }`
只算了底部 tabbar，而输入区是**停靠在 tabbar 之上**的。

**这一条修了两轮才成，值得完整记**：

第一版让 `UnifiedComposer` 用 `ResizeObserver` 发布 `--composer-inset =
自身高度`（`.uc` 的 height = 153px），toast 用
`bottom-chrome + composer-inset + kb-inset + space-4` 累加 = **255px**。
设备上一量，输入区顶边在视口底边往上 **289.7px** —— **仍然压住 34.7px**。

因为 `.uc` 自身**不含**调用方 `.composer` 包裹层的东西：

```css
.composer { padding: 8px 12px;
             padding-bottom: calc(8px + var(--app-safe-bottom));  /* +32 安全区 */
             border-top: 1px solid; }        /* 整条带 201.8px，其中 .uc 只占 153px */
```

于是把发布量改成「`#app` 底边 → `.uc` 顶边」这条**整条带**（281px），
消费端从相加改成 `max(--bottom-chrome-height, --composer-inset)`
（带里已经含 tabbar，再加就重复算一次，会平白上浮 88px），
最终 `bottom = 281 + 14 = 295px`，比输入区顶边还高 5.3px。
设备复验 `worstOverlapPx = 0`。

**基准为什么必须是 `#app` 而不是 `innerHeight`**：`#app` 高度是
`calc(100% - var(--kb-inset))`，键盘弹起时它与输入区同步上移，两者相减
与键盘无关，于是发布量恒为 281px（键盘在场时实测仍是 281）。换成
`innerHeight` 就把键盘算进带高里，toast 再加一次 `--kb-inset` —— 同一段
高度算两遍。与 §7.1 里「原生 IME inset 与视口差取一方为准而非相加」是
同一类错误，所以单列了一条判据。

### 7.5 根布局有两个 `#app`，键盘净高被扣两遍（`990fdd67`）

这条是本轮**最严重**的一个，而且是被 §7.4 的验证顺带挖出来的：在
`/ai-chat` 触发 toast 时发现「toast 与输入区重叠 67px」与算出来的 295px
对不上，顺藤摸下去发现键盘弹起时整个 App 塌了。

设备实测（API 35，键盘净高 336.38px，聚焦 `.uc-input`）：

```
#app        h=529.667   = 100% - 336.381     ← 对
#app > div  h=193.286   = 529.667 - 336.381   ← 又扣了一遍
  .app-layout      h=193.286
    .top-bar       h=45
    main.content   h=148.5                   ← 只剩 top-bar 之后的残余
      .ai-chat     h=125
        .msg-area  h=20                     ← flex:1 + min-height:0 塌成 0
        .composer  h=201.8                  ← 溢出，被 main.content 的 overflow:hidden 裁掉
    .bottom-nav    y=947                    ← 被顶到视口外
```

**后果**：输入框还在，但**工具行（麦克风/相机/附件/角色/优化/发送）整条
消失**，下半屏是一大片死区。也就是需求① 在旗舰页上其实是破的。

根因：`index.html` 的挂载点就是 `<div id="app">`，`main.ts` 走
`app.mount("#app")`，Vue 保留挂载容器、把 `App.vue` 渲染成它的**子节点**；
而 `App.vue` 的根节点当时也写着 `id="app"`。于是 DOM 里有两个嵌套的
`#app`，`#app { height: calc(100% - var(--kb-inset)) }` 同时命中两者。
那条规则的注释写的是「与 App.vue 同款规则，双写保证两处样式表注入顺序无关」
——**双写样式是对的，问题是 id 重复让双写变成了「应用两次」**。

193.286 = 529.667 − 336.381，与「扣两遍」逐位吻合。

**为什么之前没发现**：无键盘时 `--kb-inset = 0`，两层等高，一切正常。
登录/注册页是全屏固定定位表单，不吃这条 flex 链，所以需求① 在那两页验过
是好的——**只有 `/ai-chat` 这种「自管滚动 + 底部停靠输入区」的页面会塌**。
也就是说需求① 之前的「已修复」结论只在登录/注册页成立，旗舰页没覆盖到。

修法：`App.vue` 根节点改成 `class="app-root"`（id 归挂载点独占），
并给它 `height: 100%; min-height: 0; overflow: hidden` 让
`.app-layout` 的 `height: 100%` 有确定的接力基准。
仓库里几十个 `scripts/*.mjs` 诊断脚本用
`document.querySelector('#app').__vue_app__` 拿 pinia，
`__vue_app__` 挂在 mount 元素上，取到的仍是挂载容器，不受影响。

**设备复验**（模拟器 API 35，键盘净高 336.38px）：

| 环节 | 修复前 | 修复后 |
|---|---|---|
| DOM 里 `#app` 数量 | 2（嵌套） | **1** |
| 外层 `#app` 高度 | 529.667 | 529.667 |
| 内层（`.app-root`）高度 | **193.286**（= 529.667 − 336.381） | **529.667**（= 100% 接力） |
| `main.content` 高度 | 148.5 | **484.9** |
| `.composer` | 142.5..344.3（溢出被裁） | **364.9..566.7**（完整落在键盘上沿 578.6 之上） |
| 工具行（麦克风/相机/发送） | **整条消失** | **完整可见**（截图 `29-aichat-kb-fixed.png`） |

### 7.6 autoGrow 在 border-box 下少算边框，最后一行被裁 2px

需求②「内容展示要尽可能完整、不要看不完整」的最后一处漏网。

`useAutoGrowTextarea` 的核心是 `height = el.scrollHeight`。但 `scrollHeight`
是「内容 + padding、**不含 border**」，而它同时把 `boxSizing` 锁成
`border-box`——border-box 下 `height` 覆盖的是「padding 盒 + 上下边框」。
两者口径差一个上下边框之和，于是内容盒比内容矮 2px。

设备实测（`/local-agent` 的 `.draft`，1px 边框）：

```
写入一段现实长度的任务描述后：scrollHeight = 121, clientHeight = 119  → 裁 2px
```

修法：`height = scrollHeight + borderTopWidth + borderBottomWidth`，
边框值经 `target.ownerDocument.defaultView.getComputedStyle` 取，
取不到（SSR / Node 单测）时退化成 0 而不是抛错。

**设备复验**（同一台 AVD、同一个 `/local-agent` 页面、同一段文本，修复前后）：

| | `scrollHeight` | `clientHeight` | `hiddenPx` | `fits` |
|---|---|---|---|---|
| 修复前 | 121 | 119 | **2** | ❌ |
| 修复后 | 122.52（写回 123） | **121** | **0** | ✅ |

同一次普查里 `hiddenWithoutResize` 与 `hiddenButResizable` 都是空数组——
**全仓再没有「内容装不下自己」的框了**。

**这一条值得记的是判据的来历**：不是「看起来好像少了两像素」，而是先把
「内容装得下」的必要条件写成可量化的 `scrollHeight <= clientHeight`，
再拿现实长度文本灌进去逐页量。12 个路由跑下来只有 6 个多行框，
其中 5 个本来就合格，唯一的失分项就是这 2px——**如果当初按「44px 太小了」
的直觉去改笔记标题框，就会改错地方**（标题框是语义单行，autoGrow +
30vh 上限是对的）。

## 8. 新增护栏汇总（本轮共 40 条）

| 文件 | 条数 | 守什么 |
|---|---|---|
| `useKeyboardInset.test.mjs` | 9 | overlay/resize 双路径、迟滞、聚焦字段顶出（位移量精确到 32px）、非文本控件不误触发 |
| `keyboard-avoidance-prereq.test.mjs` | 4 | 原生读 `Type.ime()`、JS 消费变量、AppLayout 滚动前提、居中方式 |
| `useAutoGrowTextarea.test.mjs` | 8 | 长内容跟高、删内容缩回、不累积、boxSizing 锁死、**border-box 补边框**、无边框不加、先归零再写回、空值静默 |
| `fixed-overlay-keyboard.test.mjs` | 2 | 含输入框的 fixed 遮罩必须消费 `--kb-inset`；BottomSheet 既有写法回归 |
| `pullToRefreshIndicator.test.mjs` | 6 | 静止位移/不透明度、阈值露出、超拉钳位、单调性、源码形状防漂移 |
| `toastBottomInset.test.mjs` | 5 | `max` 而非相加、`--kb-inset` 只加一次、发布「整条带」而非自身高度、基准是 `#app` 而非 `innerHeight`、卸载清零 |
| `rootLayoutHeight.test.mjs` | 5 | `id="app"` 唯一（index.html / App.vue 两半）、`.app-root` 自己定高、`#app` 规则只扣一次键盘、`main.ts` 仍挂 `#app` |

其中五处是**源码形状断言**（判据与实现钉在一起，防「实现改了、判据没改」）。

### 8.1 判据做过变异测试，不是「永远绿」

把每个修复改回坏写法，确认对应断言真的会红：

```
✅ toast: max 退回相加（tabbar 会被算两遍）
✅ toast: 退回只算 tabbar 的旧写法
✅ composer: 退回发布自身高度
✅ composer: 改用 innerHeight（键盘会被算两遍）
✅ composer: 去掉卸载清零
✅ App.vue 根节点加回 id="app"
✅ App.vue 根节点不给高度（height:100% 链断）
✅ #app 规则里再加一次 --kb-inset 扣减
✅ index.html 再加一个 id="app"
✅ main.ts 改挂到别的选择器
✅ autoGrow: 去掉边框补偿
✅ autoGrow: 只补上边框不补下边框
✅ autoGrow: 无条件加 4px（凭空加像素）
```

13 条变异全部被拦住。没有这一步，形状断言很容易变成「看着挺严、
其实正则永远匹配不上」的假护栏。

### 8.2 两条判据为什么写成「形状」而不是「行为」

`toastBottomInset` 与 `rootLayoutHeight` 守的都是**结构性前提**：

- toast 消费端必须 `max(tabbar, 输入区带)` 而不是相加，且发布端必须以
  `#app` 为基准。写成「数值正确」的行为断言在纯 Node 里测不出来（要真实
  布局），而这两处的失效模式恰好是**悄悄算错一个高度**——不写形状判据，
  下次有人「顺手简化」回 `+` 或 `innerHeight` 就又坏了。
- `id="app"` 重复是纯 DOM 结构问题，行为上只在键盘在场时显形，Node 侧
  完全没有布局引擎可依赖。

代价是判据与实现贴得紧，实现改名要同步改判据——这正是集中使用源码
形状断言时要防的漂移，故每个文件头都写清了「为什么这条必须存在」，
并配了变异测试证明它不是空转。
这类断言在本仓库是第一次集中使用——它不优雅，但比复制一份算式更安全：
复制的那份一定会漂移。
