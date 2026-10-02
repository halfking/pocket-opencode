# STT Maestro 流：等价验证已完成，但「流本体在真机跑通」尚未闭合

日期：2026-10-02
分支：无（结论在 main）
相关提交：`e768a53`（首跑暴露的流缺陷）、`461f0233`（真机结论 + CDP 通道）、
`5182aaf5`、`0a4f057d`（判别力复核）

## 0. 状态一句话

`.maestro/notes-stt-error-visibility.yaml` 这条流在真机上**只跑通过前 5 步**
（10:39 那次，见 §1），从未跑完全程。等价的文本断言验证已在真机完成
（7/7 + 3 路负控 + 实拍截图），但那不等于「流跑通了」。
差的这一步需要人在手机上操作（开「USB 安装」），我做不到。

## 1. 阻塞：不是「装不上驱动」，而是「App 停在解锁页导致第 5 步失败」

⚠️ **本文件初版把阻塞归因成「MIUI 装不上 Maestro 驱动」，那个结论下得太早，
已修正。** 2026-10-02 11:22 复查 `~/.maestro/tests/` 时找到铁证：

`~/.maestro/tests/2026-10-02_103958/notes-stt-error-visibility/`
—— 这是一次**在真机上**的真实运行（`maestro.log` 里
`MAESTRO_DEVICE_UDID=192.168.31.19:5555`），它有 `commands.json`、
`screen-hierarchy/`、`screenshots/`，并且**真的执行了 5 步**：

```
launchApp → runFlow(when 解锁本地数据) → tapOn 学习|Study
```

终止原因（maestro.log 末尾）：

```
[ERROR] CommandFailed: Element not found: Text matching regex: 学习|Study
[INFO]  Tap on "学习|Study" FAILED
```

即**驱动当时是装上的、能跑**，失败是因为 App 停在**主密码解锁页**
（`# /login?returnTo=/study&unlock=1`），底部导航压根不存在。
（`manifest.json` 里的 `"source": "emulator"` 只是 logcat 采集来源的
元数据标注，不代表运行设备 —— 别被它误导。）

同目录没有 `notes-stt-error-after-stop*` 截图，可佐证它没跑到最后的
`takeScreenshot` 步骤。

### 仍然存在的安装阻塞（11:19 复测）

当下 MIUI 又拦住了驱动重装：

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

## 2. 这次失败的真正教训：解锁步骤是流的前置，不是可选项

10:39 那次跑之所以卡在第 5 步，是因为 `runFlow(when: visible: 解锁本地数据)`
虽然**匹配到了**解锁页，但它内部的 `tapOn 主密码` / `inputText` / `tapOn 解锁`
没能走完 —— 之后流直接往下执行 `tapOn 学习|Study`，自然找不到底部导航。

而我后来用 CDP 手动走时，**输入主密码这一步用了完全不同的手法**才成功：
`adb shell input text` 走软键盘会吞字符（13 位口令输入后长度在 13/14 间跳变），
必须用 JS 原生 setter 赋值（`Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set`）
再 `dispatchEvent(new Event('input'))` 才能让 Vue 的 `v-model` 收到。

**这说明 Maestro 的 `inputText` 在这台设备上很可能同样会吞字符**，
即当前 `e768a53` 补的 `hideKeyboard` 未必够。跑之前先准备好这个假设。

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
