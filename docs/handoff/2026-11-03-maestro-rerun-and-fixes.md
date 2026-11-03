# 2026-11-03 真机 Maestro 复测（4c308e2e）—— 结果、两处修复、新遗留

> 本轮目标：按 2026-11-01 交接单的经验与方案，用 Maestro 方式再测一轮，修正发现的问题，逐条不跳过。
> 结论：**主包 13/13 全绿（干净批次复验）+ login-gesture 绿 + _connectivity-sttdev 绿（交接单遗留打通）**；
> notes-stt-error-visibility 复测被新的客户端侧问题挡住（见 §4）；acc-* 4 条维持环境阻塞口径。

## 1. 环境（本轮差异）

| 项 | 值 | 与 11-01 差异 |
|---|---|---|
| 真机 | Redmi 2411DRN47C，USB `4c308e2e` | 同 |
| 后端 | `./pocketd` :8088（run_in_background 直跑） | 同 |
| **memora** | `set -a; source config/memora.env; set +a && ./bin/current/memora`（:8091） | ⚠️ **必须 `set -a`**：memora.env 是纯 `KEY=value` 无 export，直接 source 不进进程环境 → 二进制回落内置默认（kxuser/memora@8080）必 SASL 失败 |
| Maestro driver | dev.mobile.maestro 2.11.0（extract-maestro-driver.mjs 解包重装过一次） | 见 §3 |
| sttdev 包 | **本地重建**（见 §2 修复②） | 旧包是 10-06 别机签名的 https 构建，已卸载重装 |

## 2. 逐条结果（POCKET_LOGDIR=/tmp/opp-maestro，全部单条跑）

**主包 com.kaixuan.opencode.pocket —— 干净批次 12/12 绿（02:16-02:39，无并发干扰）**：
smoke-login · 2026-10-02-real-login · tasks-crud · notes-crud · flashcards-write ·
email-accounts · email-browse · meetings-entry · messages-hub · more-entries-open ·
more-grid-reach · settings-llm-gateway（llm.itestu.cn 断言仍绿）
**+ login-gesture 绿**（必须 `POCKET_SKIP_CDP_LOGIN=1 POCKET_ALLOW_UNCERTAIN_START=1`，否则 harness 先登录、登录屏不存在 → 必红，这是跑法约定不是缺陷）。

**sttdev 包 com.kaixuan.opencode.pocket.sttdev（POCKET_APP_ID 需显式指定）**：
- `_connectivity-sttdev` ✅ —— **11-01 交接单的遗留项就此打通**。当时判成「harness CDP 双 WebView target 定位」，实测真因是**设备上的 sttdev 包是 2026-10-06 的旧构建（https scheme）**：页面跑在 https://localhost 上、打 http://127.0.0.1:8088 被 mixed-content 拦死 → 登录不动 → 表象是「停在登录页(30s)」。本地 `VITE_API_BASE=http://127.0.0.1:8088 POCKET_ALLOW_PLAINTEXT_API=1 CAP_ANDROID_SCHEME=http node frontend/scripts/build-mobile.mjs android dev --sttdev` 重建（产出 .sttdev 后缀已断言）→ 卸旧（别机签名，install -r 会 INCOMPATIBLE）→ 装新 → CDP/登录/导航全通。
- `notes-stt-error-visibility` ❌ 新问题，见 §4。

**acc 包（本机无 ACC daemon + opencode，维持口径）**：
acc-approval 实跑一轮，夹具按设计**响亮失败**（`[fixture] ❌ induce 失败，跳过本条 flow（前提不成立时它必红，不是产品回归）`）。acc-delegate-stop / acc-history-files / acc-resume-prompt 同类前提，未逐条重跑（同一缺失依赖，在有 daemon 的 rig 上原样可跑）。

## 3. 本轮修复（随本提交落地）

1. **scripts/maestro-run.mjs：force-stop driver 扩展到 MIUI**（`if (!isMiui)` → 无条件）。
   A/B 实测两对：`--no-reinstall-driver` 下复用跨轮设备服务器，上一条 PASS 后下一条在
   `DeviceServerDiedException during 'deviceInfo' (69~91ms)` 秒死；每轮 force-stop 后必绿。
   force-stop 只杀进程不卸载包，不碰 MIUI 安装确认框那条红线 —— 原「MIUI 不能动 driver」
   的边界应是**不卸载**，不是「进程也不能杀」。已 `node --check` 通过 + 连跑 12 条全绿验证。
2. **sttdev 重建**（见上）—— 严格说是环境修复不是代码修复；副产品是确认
   maestro-run.mjs 的 CDP socket 按 pid 匹配在双 WebView 并存下**本来就没问题**，
   11-01 的「target 定位」怀疑可以撤销。

## 4. notes-stt-error-visibility 的新阻塞（已定位到哪一层）

现象：夹具注入成功（gatewayModel=zz-no-such-model-for-test），录音 UI 正常起（权限已授、
即时转写面板在、停止钮在），但 **40 秒内零分片**：页面 performance resource 无任何
/api/stt 请求、后端同期零 transcribe 请求、WebView 控制台无 `[meeting-recorder] segment failed`
—— 横幅是上一次会话残留的「转写失败，将在下一段重试」（processSegment 的兜底文案）。

对照证据：
- **主包同代码同设备原生链路是通的**：02:29 meetings-entry 期间后端收到 2 条相隔 8s 的
  `POST /api/stt/transcribe`（= MeetingRecordService PART_MS=8000 的原生分片节奏），
  且后端正确回 502 + `stt_unavailable:`（「网关暂无可用的语音转写模型」—— 断言正则命中它，
  即若分片能到后端，本流是绿的）。
- sttdev 侧疑点：BackgroundMicPlugin 桥对 `listInputs()` 15s 超时（CapacitorSQLite 桥同时正常），
  设备上无 MeetingRecordService 存活记录。倾向「新装包 + MIUI 后台麦克风限制 / 服务状态卡死」，
  已安排 adb reboot 恢复实验（见 §5）。
- 后端侧另有口径：pocketd 访问 llm.itestu.cn 恒报 `resolved address is not allowed`
  （fake-IP 198.18.x 被后端出网守卫拒），stt_unavailable 文案由该路径产出 —— 不影响本流
  判定（它要的就是 stt_unavailable 上屏），但 STT 真实可用性在本地 rig 上始终受限。

## 5. 设备中断与本轮未竟事项

- 03:07 `adb reboot`（为清 §4 的原生服务状态）后设备 20+ 分钟未重新枚举
  （大概率 MIUI 重启后在屏上等「允许 USB 调试」或 USB 掉线），reboot 实验未完成。
  设备回来后只需：重跑 `notes-stt-error-visibility` 一条；若分片恢复（后端出现 8s 节奏的
  transcribe 502），本流应直接绿。
- 跑批期间两次被 **~/workspace/ssm-all/smm-client 的 Cursor agent** 并发抢设备（其 Maestro
  批次 00-bootstrap…73-node-kline 在同一台 4c308e2e 上循环跑）：表现为我方 driver 被卸、
  会话秒死、`~/.maestro/tests` 出现对方产物目录（70-node-bootstrap 等）。跑 openpocket 套件前
  先 `ps aux | grep [m]aestro` 查并发；两个套件不能同设备并行。
- MIUI 安装确认框看门进程（0.6s 轮询 mCurrentFocus，见 AdbInstallActivity 即
  `input tap 207 1498`）实测有效，装 driver/装 APK 时挂上即可。

## 6. 复现（增量部分）

```bash
# memora（8091）—— set -a 是关键
cd ~/kaixuan/memora && set -a && source config/memora.env && set +a && exec ./bin/current/memora

# sttdev 重建 + 换装（卸旧是必须：旧包别机签名）
cd frontend && VITE_API_BASE=http://127.0.0.1:8088 POCKET_ALLOW_PLAINTEXT_API=1 \
  CAP_ANDROID_SCHEME=http node scripts/build-mobile.mjs android dev --sttdev
adb -s 4c308e2e uninstall com.kaixuan.opencode.pocket.sttdev
# （弹窗看门进程挂着）
adb -s 4c308e2e install -r -g android/app/build/outputs/apk/debug/app-debug.apk

# sttdev 流
POCKET_APP_ID=com.kaixuan.opencode.pocket.sttdev … bash scripts/maestro-run-all.sh \
  .maestro/_connectivity-sttdev.yaml .maestro/notes-stt-error-visibility.yaml
```
