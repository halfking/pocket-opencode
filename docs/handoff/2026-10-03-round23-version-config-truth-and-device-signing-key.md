# round23 — 版本配置的谎已去掉；真机上另一件更基础的事浮出来了

> 2026-10-03 00:0x。承接 round22。本轮把 round22 §2 表格里**唯一不需要
> 产品决定**的一项做掉了（`loadVersionConfig` 的静默回落），其余仍等决定。

## 1. 已做：版本配置缺失不再静默回落（需求外的正确性修复）

`loadVersionConfig` 原来在读不到 `config/version.json` 时静默返回内置
`1.2.0 / build 2 / 一个写死的下载 URL`，只打一行 Warning、`error = nil`。
round20 把它当"保守行为"留着，这轮判定为**说谎**：回落值与实际发版无关，
于是配置路径写错时（两个启动脚本都从仓库根启 pocketd，这正是当初的真实
成因），真实版本已到 1.5.0 的用户会被告知「当前已是最新版本」。

改动（`e0039c8a`）：返回 `ErrVersionConfigNotFound`（错误里带试过的路径）
→ `handleCheckUpdate` 按 `errors.Is` 分流成 503 + JSON{error, message, detail}
→ App 端 `version.ts` 抛 `VersionConfigUnavailableError`，`SettingsView`
弹指向真正原因的话并回带候选路径，9 份语言文件各加一个 key。

**不是**「请稍后重试」：重试一万次也是同一个结果。

### 负控（三处，全部真改磁盘文件）

| 注入 | 转红的用例 |
|---|---|
| Go 端改回静默回落 | `TestLoadVersionConfig_MissingReturnsErrorNotSilentDefaults`、`TestHandleCheckUpdate_MissingConfigIs503WithDiagnosticBody` |
| App 端 `if (code === ...)` → `if (false)` | `checkUpdate 把该码转成专用错误类，而不是通用失败` |
| **用修复前的二进制跑真进程判据** | `verify-version-config-503.ps1` 报 `A: expected 503, got 200` |

第三条最硬：旧二进制对不存在的配置返回 **200 + 假版本号**，
那不是「差一点」，那正是用户看到的东西。

反向用例 `TestHandleCheckUpdate_ValidConfigStillReturns200` 全程保持绿——
只断言 503 的话，「一律 503」也能全绿。

## 2. 真机上查出来的一件事，比上面那条更要紧

真机（Redmi 2411DRN47C / Android 14，adb `192.168.31.19:5555`）实测：

| 探测 | 结果 |
|---|---|
| 设备 → `127.0.0.1:18099/healthz` | **200** |
| 设备 App 的 `localStorage.pocket_api_base` | `http://127.0.0.1:18099` |
| 该 App 存的 token 在 **18099** 上 | **401 invalid or expired** |
| **同一个 token 在 18100 上** | **200，24 条通知** |

token **没过期**（`exp` = 2026-10-03T14:27Z，还在未来 14 小时）。

**根因已定位到密钥，不只是"两个进程不一样"**：

| | 密钥 |
|---|---|
| 真机 App 的 token 由谁签发 | `pocket-local-dev-jwt-secret-do-not-use-in-shared-env`（`scripts/start-local-backend.ps1:29` 的默认 `$JwtSecret`）——本机实测 HMAC 重算 **MATCH** |
| 18099 那个 pocketd（pid 2196）实际用的 | `pocket-dev-insecure-secret-0000000000`（`config.go:17` 的 `DevDefaultJWTSecret`）——本机实测用它重签的 token 在 18099 上 **200** |
| 18100 那个（pid 28160，`.wt-e2e`） | 认 `start-local-backend.ps1` 那个默认密钥 |

也就是说 **18099 上的后端是「忘了设 `POCKET_JWT_SECRET`」裸跑 dev 默认值**的那个，
它必然带着 `POCKET_DEV_AUTH=true`（`config.go:383-385` 明确拒绝 dev 默认密钥 +
dev auth 的组合，所以它只能在 dev 模式下活着）。

**任何真机验收之前必须先解决它**，否则验的是另一台后端。
最小动作：把 18099 那个进程按 `scripts/start-local-backend.ps1` 重起
（它会设同一个 `$JwtSecret`），或者让 App 用 18100。
不改代码——这是**环境**不一致，不是代码缺陷。


## 3. 需求 4：限流修复已在代码里，但**没上过真机**

`ff574621`（并发会话）把首次加载 limit 抬到 200，并加了
「前后端上限常量必须相等」的断言。本轮补上它缺的那一半——**真数据**。

真库当时只有 24 条通知，于是真机上：

```
limit=50  -> 200 count=24
limit=200 -> 200 count=24
limit=201 -> 200 count=24
limit=500 -> 200 count=24
```

**全都是 24**。上限被接受 / 被静默压回 50，在 24 条数据上无法区分。
任何 `count > 0` 或 `count == 24` 的判据在这种数据下都恒真。

新增 `backend/internal/notifycenter/limit_clamp_test.go`（`f67d7fa7`），
隔离 schema 造 260 条跨过两条线：

```
inserted=260  limit50=50 limit200=200 limit201=50 limit500=50
```

`limit > 200` 确实是**静默**压回 50（不报错，只是少拿 450 条）。
负控：把上限改成 500 → `limit201=201 limit500=260` → 转红。
用例里写了前提自检（`got50 == got200` 直接 Fatal），数据量变小会报
"判据测不出东西"而不是安静通过。

## 4. 本轮踩到并修掉的三个工具链陷阱（都会伪装成"实现坏了"）

1. **PS 5.1 的 `Invoke-WebRequest` 读不到 503 的 body**（非 2xx 抛异常，
   异常路径里 `GetResponseStream()` 已被消费），判据报"503 不带 error 码"——
   探针坏了，实现是好的。改 `curl.exe`。
2. **无 BOM 的 `.ps1` 带中文注释 → ParserError**（PS 5.1 按 ANSI 解码破坏
   引号配对）。已全改 ASCII。
3. **`node -e` 里的反引号被 PowerShell 吞掉**，改用 `.mjs` 文件。

## 5. 仍然等决定（round22 表格未动）

32 条积压提醒策略、移信 intent、网关兜底、汇总文档保留、
验证运行暂存目录、生产发票目录残留、3500 旧名副本、58000 误建档行、
手工触发流水线、真实 IMAP MOVE 授权、推送授权。

**另加一条本轮新增的前置项**：
真机验收前必须先统一那两个 pocketd 的签名密钥（§2），
否则后续所有真机结论都验在错误的对象上。
