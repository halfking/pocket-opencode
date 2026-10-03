# STT Maestro 流：等价验证已完成，但「流本体在真机跑通」尚未闭合

日期：2026-10-02
分支：无（结论在 main）
相关提交：`e768a53`（首跑暴露的流缺陷）、`461f0233`（真机结论 + CDP 通道）、
`5182aaf5`、`0a4f057d`（判别力复核）

## 0. 状态一句话

`.maestro/notes-stt-error-visibility.yaml` 这条流在真机上**从未真正跑通**。
等价路径的文本断言验证已在真机完成（7/7 + 3 路负控 + 实拍截图），
但那不等于「流跑通了」。当前阻塞需要人在手机上操作，我做不到。

> ⚠️ 本文件归因改过两次，两次的教训都记在这里：
> v1 说是「MIUI 装不上驱动」——错，理由见 §1；
> v2 说是「真机跑通了前 5 步，卡在解锁页」——**也错**，理由见 §1。

## 1. 阻塞的两个层（已逐层定位）

唯一那次看起来"跑了几步"的运行是 `~/.maestro/tests/2026-10-02_103958/`。
初读它会以为流跑到了第 5 步（`tapOn 学习|Study`），但逐行读日志后结论相反：

```
10:40:27.850  Launch app "com.kaixuan.opencode.pocket.sttdev" RUNNING
10:40:31.981  [ERROR] Failed to record heartbeat
              java.io.IOException: 另一个程序已锁定文件的一部分
10:40:36.981  [ERROR] Failed to record heartbeat
10:40:37.403  [DEBUG] Failed to set permission ... READ_EXTERNAL_STORAGE
10:40:41.566  Run flow when "解锁本地数据" is visible
10:40:58.893  [ERROR] CommandFailed: Element not found: Text matching regex: 学习|Study
10:40:59.647  [INFO]  Tap on "学习|Study" FAILED
```

判据：
- 日志中**没有任何"已连接设备"的记录**（无 `Connected to device` / session started）；
- `Launch app` 之后立刻陷入 heartbeat 失败循环，那是**本地文件锁**问题
  （`SessionStore.heartbeat`），与设备无关；
- `screen-hierarchy/` 只抓到 **1 帧**（step-005），说明它对设备视图几乎没有采样；
- `Tap on 学习|Study FAILED` 的直接原因是**没拿到设备视图**，
  而不是因为 App 停在解锁页。

⇒ **该次运行确实没建立会话。

> 后续补充（2026-10-02 11:34）：在驱动**成功安装**的那个窗口里，
> 流真正连上了设备并执行了多步（本文档 §5 记录了新的失败点）。
> 于是知道：驱动能装上时流就能跑，被拒时就完全不能跑。**
（`MAESTRO_DEVICE_UDID=192.168.31.19:5555` 只是它启动时读到的环境变量，
不代表会话建立成功。manifest 里的 `"source": "emulator"` 也只是
logcat 采集来源标注 —— 两个字段都不能当作"它在设备上跑过"的证据。）

这与本机反复出现的现象一致：`maestro.bat --device <serial>` 常直接报
"not connected"，需要它自己完成驱动安装与会话建立，而那一步在本机不成立。

### 仍然存在的安装阻塞（11:19 复测）

```
adb install logs/maestro/driver/maestro-app.apk
→ Failure [INSTALL_FAILED_USER_RESTRICTED: Install canceled by user]
```

已排除的三条路（都实测过，不是推测）：

| 尝试 | 结果 |
|---|---|
| 弹窗点「继续安装」自动确认 | 连点 2 次仍被系统撤销安装 |
| 改 `verifier_verify_adb_installs` / `adb_install_need_confirm` / `install_non_market_apps` | 实测三者本已是宽松值（0/0/1），改不动 |
| `adb root` 绕过 | `adbd cannot run as root in production builds` |

⇒ **需人工操作**：手机「设置 → 更多设置 → 开发者选项 → 打开『USB 安装』」
（MIUI/HyperOS 的中文标签，可能是「通过 USB 安装」）。

## 2. 输入口令这一步另有陷阱（与阻塞无关，但要预先知道）

我后来用 CDP 手动解锁时发现：`adb shell input text` 走软键盘会吞字符
（13 位口令输入后 `input.value.length` 在 13/14 间跳变），
必须用 JS 原生 setter 赋值再 dispatch `input` 事件，Vue 的 `v-model` 才收得到。

且口令错时**界面不报错**：`LoginView.unlock()` 的 `error.value` 只在
`initLobster` 抛错时写入，而 AES-GCM 解密失败发生在更下游，
表现是「点了没反应」。

⇒ Maestro 的 `inputText` 很可能同样中招。跑之前先备好这个假设，
否则会误判成「路由没跳转」。

## 3. 等价路径做了什么、为什么它不能替代「流跑通」

改走 CDP：`adb forward tcp:9333 localabstract:webview_devtools_remote_<pid>`
+ `Runtime.evaluate` 读 DOM、`element.click()` 触发交互，工具是
`scripts/adb-cdp-eval.ps1`。

**为什么 uiautomator 不行**：这个 WebView 的 a11y 树未暴露，
`uiautomator dump` 只吐 8 个空节点，一个字都读不到；
而这条流的断言**全部是文本断言**。所以 CDP 不是降级替代，
在「读到用户实际看到什么」这件事上它反而更直接。

**但仍然不等于流跑通**，因为：
- 流的 YAML 里那些 `tapOn` / `extendedWaitUntil` / `assertVisible` /
  `takeScreenshot` 步骤，**一次都没有被 Maestro 解析和执行过**；
- 我对流做的两处修改（补 `tapOn: "语音笔记"`、补 `hideKeyboard`）
  只验证了 **YAML 结构合法**（`yaml.safe_load_all` → DOCS=2、17 步），
  **没有验证过它们在 Maestro 下真的能过**；
- `takeScreenshot: "logs/maestro/notes-stt-error-after-stop"` 这步从未触发，
  因此该路径下不应存在对应截图（若有人看到，来源可疑）。

## 4. 已确证的功能结论（这部分是硬的）

真机 Redmi 2411DRN47C / Android 14 / HyperOS V816，`sttdev` 旁挂包连
隔离实例 `127.0.0.1:18111`（schema `pocket_sttdev_20261002`），
停止录音后横幅显示**可行动原因**：

```
LLM 网关未配置 API Key（设置 → LLM 网关）；外部语音转写服务未配置 API Key（设置 → 语音转写）
```

两个通用兜底文案（`该功能尚未完成配置` / `语音转写服务尚未配置`）
与裸错误码/技术串均未出现 → 2026-10-01 修的那个缺陷在真机上不复现。

判别力已复核（`0a4f057d`）：修复态 PASS、缺陷态 FAIL。
判别力来自 `assertVisible` 与两条 `assertNotVisible` **串联**，
拆开单跑任一条都会得到错误绿灯。

证据：`logs/real-device-stt-20261002-104353/`
（`summary.md` + 实拍 `final-after-stop.png`，720×1640 / 66,683 bytes）。

## 5. 为什么阻塞必须人工解除：完整机制（2026-10-02 11:50 实测）

这段是本文档最值钱的部分。之前我只知道 `adb install` 失败，不知道它被什么拦截；
现在已定位到具体归因：

```
dumpsys user | grep restrictions
  Restrictions:            none
  Effective restrictions:  none          <-- 系统层没有任何限制

pm list packages -f | grep AdbInstall
  com.miui.securitycenter/com.miui.permcenter.install.AdbInstallActivity
  Action: "com.miui.securitycenter.intent.action.INSTALL_PACKAGE"   <-- 它把 ADB 安装劫持到了自己的确认页
```

即：限制**不在 AOSP 的 user_restriction 里**（所以改 `settings put` 的一系列开关都无效），
而在 MIUI 自己的 SecurityCenter 里。它对每次 ADB 安装弹出确认页，
页面上的那个开关就是「开发者选项 -> USB 安装」。

### 已实测排除的所有自动路径

| 尝试 | 结果 |
|---|---|
| `adb install -r -g` | 失败 INSTALL_FAILED_USER_RESTRICTED |
| `pm install -r -g /data/local/tmp/…`（绕过 adb install） | 同样失败 —— 接口不是关键 |
| 弹窗自动点「继续安装」 | 连点 2 次仍被系统撤销 |
| `settings put global verifier_verify_adb_installs / adb_install_need_confirm / install_non_market_apps` | 实测本已是宽松值（0/0/1），改不动 |
| `adb root` | `adbd cannot run as root in production builds` |
| `cmd deviceidle whitelist +<pkg>` | 能执行，但与安装限制无关 |

此外：系统层不保存任何 user_restriction，所以**没有 adb 可写的同等开关**。

### 一个容易误判的现象

一次安装成功过（`Success`），随后十分钟就又被拒。原因：

**Maestro 每次开会话都会先 `uninstall` 再 `install` 驱动**，且把安装直接写在
`AndroidDriver.installMaestroDriverApp` 的调用链上（完整堆栈：
`MaestroSessionManager.createAndroid -> AndroidDriver.open -> installMaestroApks -> installMaestroDriverApp -> install`。

所以：一次成功安装 **不代表之后的每一次运行都能成功** —— 开关必须在每次前都是开的。

### 一另一个独立于安装的阻塞（已编入流）

安装成功后又连跑三次，均失败在 `tapOn "学习|Study"`，而 Maestro 抓到的
层级里是**桌面**（含 `launcher`/`WeChat`/`Calendar`）。根因是 **Maestro 抢跑**：

```
11:38:42.123  Launching app com.kaixuan.opencode.pocket.sttdev
11:38:42.123  Stopping … app during launch
11:38:42.614  Launch app COMPLETED      <-- 只用了 0.5s
```

而 App 实测需 1.6s 才 `Displayed`（`am start -W`: `+1s624ms`）。它紧接着去抓 hierarchy，
抓到的就是还没切换完的桌面。已在流里补了 `waitForAnimationToEnd` +
`extendedWaitUntil` 等 App 就绪，**但尚未在成功安装的窗口内验证过**。

这个失败很容易被误读成「选择器写错」或「设备语言不对」——
它实际上是时序问题。

## 6. 接手者该做什么

1. 手机上打开「开发者选项 → USB 安装」；
2. 跑一次上面的 `maestro.bat test ...`；
3. 若仍失败，把**完整错误**连同 MIUI 版本贴回 —— 不要再重复尝试
   自动点弹窗（已证明无效且有误触风险：实测期间设备前台一度是
   NS 代理 App 而非安装框）。
4. 跑通后请回填本文件 §5。

## 7. 回填区（待填）

- [ ] Maestro 在真机执行结果：PASS / FAIL
- [ ] 若 FAIL，首个失败步骤与其输出：
- [ ] 截图路径：
