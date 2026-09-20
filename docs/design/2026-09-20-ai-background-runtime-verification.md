# AI 流后台生存 · 真机验证清单（M1 + M5/T2 · 2026-09-20）

> 目标：把决策 4「真机 30min + Perfetto」门禁变成可执行步骤
> 上游：
>
> - 设计 [`./2026-09-09-ai-async-background-survival.md`](./2026-09-09-ai-async-background-survival.md)（M1–M5）
> - 方案 [`../audits/2026-09-20-native-ui-restructure-plan.md`](../audits/2026-09-20-native-ui-restructure-plan.md) §2.4
> - 骨架：`frontend/src/native/{appLifecycleHub,aiStreamRuntime,aiStreamKeepalive}.ts`
> - 原生：`frontend/android/app/src/main/java/.../plugins/{AiStreamKeepalivePlugin,AiStreamService}.java`
>
> 已具备的自动化覆盖（25 个 green）：
>
> - `frontend/src/native/__tests__/appLifecycleHub.test.mjs`（5 项）
> - `frontend/src/native/__tests__/aiStreamKeepalive.test.mjs`（5 项决策表）
> - `frontend/src/native/__tests__/aiStreamRuntime.test.mjs`（13 项含 watchdog 多轮 pause/resume）
>
> 本文档只描述**真机不可被自动化覆盖的部分**。

---

## 0. 准备（一次）

### 0.1 测试机型矩阵

| 厂商 | 系统 | 备注 |
|---|---|---|
| Pixel 8 (API 34+) | 原生 | 参考基线 |
| Vivo / OPPO / 华为（任意） | Android 14 | OEM 后台策略最严酷，必须验 |
| Xiaomi | MIUI/HyperOS | 自启动管理 + 后省电策略 |

### 0.2 必装的工具 / 开关

```bash
# 1. 开启 Perfetto tracing（开发者选项 → 启用 GPU 渲染分析）
adb shell setprop debug.hwui.profile true
adb shell setprop debug.perfetto.cmdline true

# 2. 允许 Capacitor WebView 远程调试
adb shell setprop webview.remote_debugging true   # 仅 debug 包生效（MainActivity 已收口）

# 3. 准备好 deep-link 唤起命令
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity

# 4. 关闭 developer 主动电池优化（让保活"自然"生效，不要靠白名单作弊）
adb shell dumpsys deviceidle whitelist -<app_pkg>  # NOT do this in measurement
```

### 0.3 日志过滤别名

```bash
# JS 层（Capacitor Console 转发）
adb logcat -v time -s "Capacitor/Console" "Capacitor"

# 原生 AI 流服务
adb logcat -v time AiStreamService:V AiStreamKeepalive:V *:S

# Doze 决策
adb logcat -v time -s "BatteryStats" "JobScheduler" "Doze" "UidActive"
```

---

## 1. 真机 30min 后台回归测试

### 1.1 前置

```bash
# 1) APK 装好并启动
adb install -r app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

### 1.2 步骤

1. **进入 AI 对话**：打开一个长 prompt（例如"用 Kotlin 写一个 LRU 缓存，按 LRU 策略淘汰"），触发流式生成
2. **观察预期**：
   - JS 日志：`[aiStreamKeepalive] start failed` 应**不存在**
   - 原生日志：`AiStreamService onStartCommand` + 通知栏出现「AI 任务进行中」
3. **切换到后台**：按 Home 键
4. **等 30 分钟**（期间不要再回前台）
5. **回前台**：再次切换到 AI 对话页面
6. **断言**（全部通过才说明「M1 + M5/T2」联合生效）：
   - 消息**完整到达**，无 "网络中断/超时" 提示
   - 通知**自动消失**（流结束 + 切回前台后，keepalive 应已 stop）
   - JS 日志：`[appLifecycleHub] ... → resumed` 必须出现

### 1.3 失败回退路径（按优先级）

| 现象 | 原因候选 | 修复 |
|---|---|---|
| 切后台后 SSE 立即断开 | Doze 已冻结 WebView | 验证 `WAKE_LOCK` 权限、`AiStreamService` 已 startForeground、通知已声明 channel |
| 回前台发现流断了 | watchdog 误触发 | 看 `[aiStreamRuntime] triggerWatchdog` 出现 → 检视剩余预算算术 |
| 通知一直在但消息没续上 | keepalive 已起 service 但 JS 流已 abort | `aiStreamRuntime.abort(id)` 误调用；查主动取消日志 |
| OEM 厂商直接杀进程 | 后台策略太严 | 给用户引导「电池白名单」开关（v1.5 文档） |

---

## 2. Perfetto 系统级 trace

### 2.1 开 trace

```bash
# 后台 30min trace
adb shell perfetto --background -o /data/local/tmp/ai-bg30m.pftrace \
  -t 30m sched freq idle am wm gfx view input hal.sensors camera input_method
```

### 2.2 关键看板上检查

- **`AiStreamService` 进程**：30min 内不能出现 `kill` 事件
- **`pocket.webview` 主线程**：30min 内不应有 `Doze` 标记（Doze 起作用时 `webview` 进入 `Deep Idle`）
- **SSE fetch chunk 间隔**：观察 AI 长响应字段间隔是否接近 wall-clock（如果被 WebView 拖到 ≥5s 才 1 帧，说明网络栈被冻结）

### 2.3 验收门禁

- ✅ `AiStreamService` 全程未被杀
- ✅ 主线程无 `Deep Idle`
- ✅ SSE chunk 平均间隔 ≤2s
- ✅ 通知栏「AI 任务进行中」常驻

> 任一项 ❌ 即视为该机型验证不通过，需立项 OEM 适配。

---

## 3. 自动化兜底（CI 可跑、覆盖 80% 路径）

```bash
cd frontend
node --test src/native/__tests__/aiStreamRuntime.test.mjs \
                src/native/__tests__/aiStreamKeepalive.test.mjs \
                src/native/__tests__/appLifecycleHub.test.mjs
```

> 期望：30 tests + suites，**0 fail**。这是 PR 合并的硬门槛。

---

## 4. 不在本验证范围

- 跨设备续传（明确不做，见设计 §3.2）
- 后端 session 接管（不含）
- 推送到达测试（v2 议程）

---

## 5. 验证完成的判据

- [ ] 至少 3 款机型实测通过（含至少 1 款国内厂商）
- [ ] Perfetto trace 全程无 Doze / kill
- [ ] CI 跑 30 个 native 单测全绿
- [ ] 回到原会话更新 `docs/knowledge/incidents/` 记录踩坑

---

**写于**：2026-09-20
**作者**：Mavis / mavis orchestrator
**下次更新**：真机跑通后回填数据；如失败则新建 incidents/ 条目跟进
