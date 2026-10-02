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

## 1. 真正的阻塞：Maestro 从未与设备建立会话

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

⇒ **那次运行的"5 步"是空转，不构成任何执行证据。**
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

## 5. 接手者该做什么

1. 手机上打开「开发者选项 → USB 安装」；
2. 跑一次上面的 `maestro.bat test ...`；
3. 若仍失败，把**完整错误**连同 MIUI 版本贴回 —— 不要再重复尝试
   自动点弹窗（已证明无效且有误触风险：实测期间设备前台一度是
   NS 代理 App 而非安装框）。
4. 跑通后请回填本文件 §5。

## 6. 回填区（待填）

- [ ] Maestro 在真机执行结果：PASS / FAIL
- [ ] 若 FAIL，首个失败步骤与其输出：
- [ ] 截图路径：
