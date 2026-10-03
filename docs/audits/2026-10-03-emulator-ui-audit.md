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
| 前端全量 | `npm run test:all` | **pass 1819 / fail 0**（基线 1804 + 新增 15） |
| 移动端构建 | `node scripts/build-mobile.mjs android dev` | OK，sanity check 通过 |
| APK | `gradlew assembleDebug` | **BUILD SUCCESSFUL**，32.5 MB |
| 安装 | `adb install -r -g` | Success |
| 启动 | `am start` | `mCurrentFocus=…MainActivity` |

## 4. 遗留

1. **登录之后的功能页本轮没走到**——需要后端。需求③的「方形背景逐屏检查」
   与需求④的「30 分钟后台实测」都卡在这里。
2. 模拟器是 `swiftshader` 软件渲染 + 无音频，**录不了音**；
   `RECORD_AUDIO` 已用 `install -g` 授予但没有真实音频输入源。
3. `components/base/Textarea.vue` 仍是死代码。本轮用全局 CSS 下限代替了
   「统一到基类组件」的做法——要真统一得改 21 处视图，是独立一件事。
4. `.env.android-dev` 是本轮新建的本地配置（gitignored），指向
   `http://10.0.2.2:8088`（模拟器访问宿主的别名）。
5. `gradle-wrapper.properties` 的 `networkTimeout` 10s → 180s 是本轮改的，
   10s 对 214 MB 分发包必然超时，属于真实缺陷，建议保留。

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
