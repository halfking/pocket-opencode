# 真机 30min 验收 — 设备参考与预填示例（2026-09-20）

> 这张是给用户在真机上跑完 30 min 后回填 `runtime-evidence-summary.md` 时「按设备型号找对应行」的对照参考。
> 多数行用户在真机具体环境下采集后填入即可；这一张只覆盖「像这样填」的范本。

---

## 1. 常见设备的预期值（参考）

| 字段 | Pixel 6/7/8 | Samsung S22/23/24 | Xiaomi MIUI | Huawei EMUI | 一加 OxygenOS |
|---|---|---|---|---|---|
| `ro.product.model` | `Pixel 8` | `SM-S921B` | `Mi 11` | `TAS-AN00` | `KB2000` |
| `ro.build.version.release` | `14` | `14` | `13 / 14` | `12` | `13 / 14` |
| `dumpsys deviceidle m. whitelist` 含 kaixuan | ✅ 默认 | ⚠ 自启动 + 电池 = 全部允许 | ⚠ 须设「无限制」 | ⚠ 须关「电池优化」 | ⚠ 须关「电池优化」 |
| 通知栏 30 min 可见 | ✅ 100% | ⚠ 偶发被「勿扰」 | ⚠ MIUI 默认折叠 | ⚠ EMUI 通知严苛 | ✅ 默认 |
| `Watchdog triggered` 命中 | 0 | 0–1 | 0（OEM 杀在前） | 0（OEM 杀在前） | 0 |
| OEM 杀进程日志 | 0 | 0–1 | 0–1 | 0 | 0 |

> 任意厂商 ROM 如命中 Watchdog > 0 或 OEM 杀进程 > 0，需翻 `docs/design/2026-09-20-ai-background-runtime-verification.md` § 3 失败回退路径。

## 2. 验收字段预填例子（Pixel 6 / Android 14 范本）

```
设备:        Pixel 6 (Android 14, build TQ3A.230901.001)
厂商策略:    Stock AOSP — 默认
流消息数:    完整（30 min 后回前台接上文无丢失）
通知状态:    30 min 仍在
Watchdog:    0 条
FGS dumpsys: AiStreamService 在
白名单:      m whitelist 含 kaixuan
Perfetto:    2026-09-20-ai-bg30m.pftrace (~12 MB)
失败节点:    无
```

## 3. 验收字段预填例子（MIUI 14 / 小米 13 Pro 范本）

```
设备:        Mi 13 Pro (Android 14, MIUI 14.0.4)
厂商策略:    MIUI 后台默认 5min sleep — 必须手动无限制
流消息数:    完整
通知状态:    30 min 仍在（设「重要通知」后）
Watchdog:    0 条
FGS dumpsys: AiStreamService 在
白名单:      m whitelist 含 kaixuan （设了「无限制」+「自启动」）
Perfetto:    2026-09-20-ai-bg30m.pftrace (~12 MB)
失败节点:    §1.4 电池白名单 — 首次设置后才能起跑
```

## 4. 命令复核（让你 5 秒快速拿全 8 行）

```powershell
$env:PATH = "$env:LOCALAPPDATA\Android\platform-tools;$env:PATH"
adb shell getprop ro.product.model
adb shell getprop ro.build.version.release
adb shell getprop ro.build.version.sdk
adb shell dumpsys deviceidle | Select-String "m. whitelist"
adb shell dumpsys activity services | Select-String kaixuan
adb logcat -d -s AiStreamService:V AiStreamKeepalive:V | Select-String "onStartCommand|keepalive"
adb logcat -d -s ActivityManager:I | Select-String "Killed.*kaixuan"
```

把这 8 行输出贴在 Mavis chat 的下一轮，会一次性回填模板 § 1-4。

## 5. 厂商对客服技巧

| 厂商 | 后台保活路径 |
|---|---|
| Xiaomi MIUI 14 | 安全中心 → 自启动 / 关联启动 / 后台运行 → OpenCode Pocket 全开 |
| Huawei EMUI | 手机管家 → 应用启动管理 → OpenCode Pocket → 改成「手动管理」勾 3 项 |
| OPPO ColorOS | 设置 → 电池 → 更多电池设置 → 关闭「睡眠待机优化」 |
| VIVO OriginOS | 设置 → 电池 → 后台高耗电 → OpenCode Pocket 允许 |
| Samsung One UI | 设置 → 电池 → 后台使用限制 → 不要勾 OpenCode Pocket |
| 一加 OxygenOS | 默认无 OOM adj 杀后台，但要关电池优化 |
| 鸿蒙 HarmonyOS | 设置 → 电池 → 启动管理 → OpenCode Pocket 全开 |

---

**写于**：2026-09-20 · 让用户跑真机前心里有数 / 跑完数据对得回 § 4 表
