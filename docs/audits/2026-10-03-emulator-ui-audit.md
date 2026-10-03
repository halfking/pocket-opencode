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

### 需求③ 图标与方形背景 —— 图标侧通过，方形背景需实机逐屏看

- `check:icons`：134 个名字全在字体子集内，`check:icons:visual` 生成
  132 格目视验证页。
- **实际在浏览器渲染逐格核对**：130 个真实图标全部合出字形，无
  `LIGHT_MODE` 那种文本回退；`name` / `starred` 显示为文本，与
  `check-icon-font.mjs` 判定的 2 个假阳性一致（`starred` 来自
  `item.status === 'starred'` 的比较值，压根不是图标名）。两种独立方法互证。
- 字体侧防护到位：完整 3.45 MB 字体（非早期 8.8 KB 子集，那版真机露馅过）、
  `font-display: block`、`overflow: hidden` 兜底。
- **未通过的部分**：`font-size: 24px` 是全局固定值（两处），所以「异常大」
  这一类不会发生；但**方形背景/图标按钮没有全局最小尺寸约束**，
  `.icon-btn` 等尺寸全靠各视图手写。这一项**本轮未能在模拟器上逐屏走完**
  （需要登录后端才能进入各功能页），留作遗留项。

### 需求④ 大模型/录音原生化 + 后台存活 —— 代码层核查通过

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

**未验证**：需要真实大模型端点与真实录音权限才能做 30 分钟后台实测，
本轮没有可用后端，属遗留项。

## 3. 验证

| 项目 | 命令 | 结果 |
|---|---|---|
| 类型检查 | `npm run typecheck` | EXIT=0 |
| 前端全量 | `npm run test:all` | **pass 1841 / fail 0**（基线 1804 + 新增 37） |
| 移动端构建 | `node scripts/build-mobile.mjs android dev` | OK，sanity check 通过 |
| APK | `gradlew assembleDebug` | **BUILD SUCCESSFUL**，32.7 MB |
| 安装 | `adb install -r -g` | Success |
| 启动 | `am start` | `mCurrentFocus=…MainActivity` |

判据本身的可靠性用**变异测试**验过：把每个修复改回坏写法，确认对应断言
真的会红（`✅ 判据拦住了`），避免留下「永远绿」的假护栏。toast 那批 5 条与
根布局那批 5 条共 10 条变异全部被拦住。

## 4. 遗留

1. **需求④ 的 30 分钟后台实测**本轮做不了，两个前提都不具备：
   模拟器是 `swiftshader` 软件渲染 + `-no-audio`，**没有音频输入源录不了音**；
   同时没配真实大模型网关（`/ai-chat` 报「请先在「设置 → AI 网关」配置网关
   密钥」），流式请求也发不出去。代码层核查已通过（§2 需求④），
   剩下的是环境受限，不是代码问题。
2. **需求③ 的「方形背景逐屏检查」没走完**：本轮走查了 14 个页面
   （含键盘弹起态），但主密码门后面的 `/notes/new`、`/vault/new`、
   `/scheduled-tasks/new` 等表单页未逐屏核对。已知 `.icon-btn` 等图标按钮
   **没有全局最小尺寸约束**，尺寸全靠各视图手写——这是这一项真正该补的。
3. `components/base/Textarea.vue` 仍是死代码。本轮用全局 CSS 下限代替了
   「统一到基类组件」的做法——要真统一得改 21 处视图，是独立一件事。
4. `.env.android-dev` 是本轮新建的本地配置（gitignored），指向
   `http://10.0.2.2:8088`（模拟器访问宿主的别名）。
5. `gradle-wrapper.properties` 的 `networkTimeout` 10s → 180s 是本轮改的，
   10s 对 214 MB 分发包必然超时，属于真实缺陷，建议保留。
6. **模拟器进程会自行退出**（本轮撞到两次，`adb devices` 直接空）。
   重启约 70s 恢复，不影响结论，但批量走查时要把「设备掉线」当作正常故障
   重新 `adb wait-for-device`，别误读成应用崩溃。
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

## 7. 本轮新发现并修复的五个缺陷

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

## 8. 新增护栏汇总（本轮共 37 条）

| 文件 | 条数 | 守什么 |
|---|---|---|
| `useKeyboardInset.test.mjs` | 9 | overlay/resize 双路径、迟滞、聚焦字段顶出（位移量精确到 32px）、非文本控件不误触发 |
| `keyboard-avoidance-prereq.test.mjs` | 4 | 原生读 `Type.ime()`、JS 消费变量、AppLayout 滚动前提、居中方式 |
| `useAutoGrowTextarea.test.mjs` | 6 | 长内容跟高、删内容缩回、不累积、boxSizing 锁死、先归零再写回 |
| `fixed-overlay-keyboard.test.mjs` | 2 | 含输入框的 fixed 遮罩必须消费 `--kb-inset`；BottomSheet 既有写法回归 |
| `pullToRefreshIndicator.test.mjs` | 6 | 静止位移/不透明度、阈值露出、超拉钳位、单调性、源码形状防漂移 |
| `toastBottomInset.test.mjs` | 5 | `max` 而非相加、`--kb-inset` 只加一次、发布「整条带」而非自身高度、基准是 `#app` 而非 `innerHeight`、卸载清零 |
| `rootLayoutHeight.test.mjs` | 5 | `id="app"` 唯一（index.html / App.vue 两半）、`.app-root` 自己定高、`#app` 规则只扣一次键盘、`main.ts` 仍挂 `#app` |

其中五处是**源码形状断言**（判据与实现钉在一起，防「实现改了、判据没改」）。

### 8.1 两条判据为什么写成「形状」而不是「行为」

`toastBottomInset` 与 `rootLayoutHeight` 守的都是**结构性前提**：

- toast 消费端必须 `max(tabbar, 输入区带)` 而不是相加，且发布端必须以
  `#app` 为基准。写成「数值正确」的行为断言在纯 Node 里测不出来（要真实
  布局），而这两处的失效模式恰好是**悄悄算错一个高度**——不写形状判据，
  下次有人「顺手简化」回 `+` 或 `innerHeight` 就又坏了。
- `id="app"` 重复是纯 DOM 结构问题，行为上只在键盘在场时显形，Node 侧
  完全没有布局引擎可依赖。

代价是判据与实现贴得紧，实现改名要同步改判据——这正是本轮集中使用源码
形状断言时要防的漂移，故每个文件头都写清了「为什么这条必须存在」，
并配了变异测试证明它不是空转。
这类断言在本仓库是第一次集中使用——它不优雅，但比复制一份算式更安全：
复制的那份一定会漂移。
