# 真机一键接力卡（2026-09-20）

> 这是一张**给任何拿到真机的工程师**的 30 秒接力单。
> 读这一张就够跑完全部 30min 后台验收；细节回 `docs/audits/2026-09-20-real-device-emulator-runbook.md` §1。
>
> **背景**：本环境是 VMware guest，CPU 屏蔽了 VTX，Android emulator 在 qemu TCG 软件模拟下撑不过 90 秒；8 层静态验证全部通过、APK 已构建待装。

---

## 0. 5 秒准备

- 1 台 Android 13+ 真机（厂商任意）
- 1 根 USB 数据线
- 已开 **USB 调试**：设置 → 关于本机 → 连点 7 次「版本号」→ 系统 → 开发者选项 → USB 调试
- 当前仓库 main 已 `1b0447e`，APK 已构建

## 1. 10 秒贴这段 PowerShell

打开 PowerShell，粘贴：

```powershell
$env:PATH = "$env:LOCALAPPDATA\Android\platform-tools;$env:PATH"
cd C:\workspace\openpocket

adb devices                                                                            # 看到 1 行 device
adb install -r frontend\android\app\build\outputs\apk\debug\app-debug.apk               # 装机
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity                        # 启动
adb shell am start -a android.settings.IGNORE_BATTERY_OPTIMIZATION_SETTINGS            # 白名单
adb logcat -c
adb logcat -v time AiStreamService:V AiStreamKeepalive:V *:S                          # 持续监听
```

## 2. 30 秒在 app 内操作

1. 进 AI 对话页
2. 发一句长 prompt（建议 ≥ 30 行的题目，例如「用 Kotlin 写一个 O(1) LRU 缓存 + 完整测试」）
3. 等流式输出开始
4. **按 Home 键** → 此时开始 30 min 计时

## 3. 30 分钟，等待期间不要重启 app

后台程序允许：
- 收通知（30 min 全程应可见「AI 任务进行中」）
- watch 流接续，不掉
- `adb logcat` 中无 `Watchdog triggered`

## 4. 30 min 后回前台

- 接上文流不中断 → 流消息完整
- 通知仍在
- logcat 中 `AiStreamService onStartCommand` 出现多次（keepalive 命中）

## 5. 30 秒回填这张表

| 字段 | 填什么 | 怎么拿 |
|---|---|---|
| 设备型号 | 例 Pixel 8（Android 14） | `adb shell getprop ro.product.model` |
| 厂商策略 | 例 MIUI 后台默认 5min sleep | 厂商·型号 |
| 流消息完整 | ✅ / ❌ | 目视 |
| 通知 30min 仍在 | ✅ / ❌ | 截图 |
| Watchdog 命中 | 0 / N | logcat |
| FGS dumpsys | ✅ / ❌ | `adb shell dumpsys activity services \| Select-String kaixuan` |
| 白名单 | ✅ / ❌ | `adb shell dumpsys deviceidle \| Select-String m. whitelist` |
| Perfetto trace | 文件名 / `未采` | 见下方可选 |

把这段文字贴在 Mavis chat 的下一轮 → 它会更新 STATE.md § 8.6 + 标记 goal complete。

## 6. 可选：Perfetto trace

```powershell
mkdir C:\workspace\openpocket\test-evidence -Force | Out-Null
adb shell perfetto --background -o /data/local/tmp/ai-bg30m.pftrace `
  -t 30m sched freq idle am wm gfx view input hal.sensors camera input_method
Start-Sleep -Seconds 5
adb pull /data/local/tmp/ai-bg30m.pftrace C:\workspace\openpocket\test-evidence\2026-09-20-ai-bg30m.pftrace
# 然后用 https://ui.perfetto.dev/ 打开
# 关键看板：AiStreamService 进程 30min running / 主线程无 deep idle / SSE chunk ≤ 2s
```

## 7. 失败就翻 4 件套排障

| 症状 | 排障命令 | 修复路径 |
|---|---|---|
| App 被杀 | `adb shell ps -A \| Select-String kaixuan` | 回 §1.4 电池白名单 |
| 流中断 | `adb logcat \| Select-String -Pattern 'aiStreamRuntime\|watchdog'` | 查 `docs/design/2026-09-20-ai-background-runtime-verification.md` §3 |
| FGS 没起 | `adb shell dumpsys activity services \| Select-String kaixuan` | JS keepalive 桥断 |
| 网络断 | `adb shell dumpsys connectivity \| Select -First 40` | 厂商策略 |

---

**写于**：2026-09-20 · agent idempotent 卡 · 任何新来接力者 30 秒内读完即可上手
