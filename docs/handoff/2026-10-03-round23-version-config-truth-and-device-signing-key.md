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

已加护栏 `scripts/jwt-secret-drift.mjs`（`db8db615`）：用两个已知密钥各签一个
token 去打每个端口，比较**哪个被接受**。只看状态码的话漂移和一致长得一模一样。

## 2b. 真机验收本轮实际做到了哪一步

**18099 已按授权重启完毕（`d6b4db7b` + `274c411b`）**，真机 App 现在
**直连它自己配置的后端，零 workaround**。`adb reverse` 全程保持原始的
18099→18099（上一轮临时指向 18100 的 workaround 已作废）。

> 用户授权的是「按 start-local-backend.ps1 重启」，实际**没有用那个脚本**：
> 它要求 `POCKET_AUTH_PASS`/`POCKET_DEV_PASS`，仓库刻意没有默认值
> （2026-10-03 安全整改删掉了那个明文 devPass 常量），自造会制造新的
> 口令不一致。改用 `scripts/restart-18099-aligned-secret.ps1` 显式设 env，
> 每一项取自被替换实例**自己**的启动日志，且**复用同一个二进制**。

### 第一次重启只修了一半——而且漏的那一半在屏幕上完全看不出来

重启后：token 200 ✓、API 24 条 ✓、healthz 200 ✓，但**真机通知中心显示
「暂无通知」**。WebView console：

```
"Access to fetch at 'http://127.0.0.1:18099/api/notific...' ..."
"Uncaught (in promise) TypeError: Failed to fetch"
```

实测 ACAO：`:18099 ACAO=null` / `:18100 ACAO=https://localhost`。
**CORS 没配 ⇒ 浏览器拦掉响应 ⇒ 前端拿到网络错误而不是 0 条 ⇒ 渲染成空列表。**

> 空列表与被拦掉的请求，**在屏幕上一模一样**。只看截图会判成"数据没了"
> 或"store 逻辑坏了"——我第一反应就是去读 `notification.ts`，
> 因为「API 返回 24 条」与「UI 显示 0 条」互相矛盾，矛盾的是**它们不是同一条链路**。

补 `POCKET_ALLOWED_ORIGINS`（列 `https://localhost,capacitor://localhost,http://localhost`
三个而不是只列 console 里出现的那一个——Android WebView 的 origin 随 scheme 变，
只列碰巧出现过的那个，另一个照样静默失败）后：

| Origin | status | ACAO | count |
|---|---|---|---|
| `https://localhost` | 200 | `https://localhost` | 24 |
| `capacitor://localhost` | 200 | `capacitor://localhost` | 24 |
| `http://localhost` | 200 | `http://localhost` | 24 |
| `https://evil.example` | 200 | **null** | 24 |

陌生 origin 仍被浏览器拦——放行名单没被写成通配。
（`POCKET_ALLOWED_ORIGINS` 在生产模式是必填，`config.go:442-448`，
所以这不是"多写一个保险"，是这个实例一直缺。）

**最终真机验收**：force-stop → 重启 → 更多 → 铃铛 → 通知中心正常渲染
24 条 email.important，且此刻无任何 workaround。

### 重启门禁（读日志，不假设）

```
[OK] Postgres pool initialized (schema="opencode_pocket")
[OK] Email credential self-check: all 5 enabled email account(s) decrypt
[OK] daily pipeline runner injected
[OK] pocketd listening on :18099
[info] pipeline scheduled at 2026-10-03T08:00:00+08:00
```

邮件同步也真的恢复了（`56551681@qq.com sync trace total 58.816s`）。

### 过程中三次"装置坏了"而不是"实现坏了"

1. **`adb shell input tap` 全部无效**——连点「更多」这种有明确视觉反馈的都不动。
   根因在 `dumpsys input`：`FocusedWindows:` 是**空的**，输入焦点层丢了
   （`screencap` 走独立通道所以截图照常，KEYCODE_HOME 也照常生效）。
   按 HOME 再 `am start` 把窗口焦点抢回来后，tap 立刻正常。
2. **`uiautomator dump` 拿到的是别的 App 的窗口**（一个 VPN 界面），
   因为焦点在别处。WebView 应用的 dump 也不含自身节点——
   靠 `content-desc` 找坐标这条路在真机上走不通，只能截图 + 已知布局。
3. **脚本「超时」是宿主在等长命后代进程，不是脚本卡死**。用两个最小复现
   判别出来的：子进程自己退（3s）的版本 4.2s 就返回；子进程长命
   （`ping -t`）的版本挂住。**我差点照着"超时"去"修"一个好脚本。**
   判别方法：让子进程自终止再跑一次，看是否立即返回。

## 2c. 需求 4 在真机上**验不了的那一半**

需求 4 要验的是"首次加载超过 50 条时不丢历史"。真库此刻只有 **24** 条，
真机 UI 渲染 24 条完全正常——**这恰恰证明不了任何东西**。
要验它必须先让库里超过 56 条（24 + 需求 4 明天 08:00 推的 32，round24 §1 已
把 09:00 更正为 08:00）。这件事卡在"32 条积压提醒策略"那个待拍板项上。

## 2d. 顺带暴露的既有缺陷（不在本轮范围，未修）

重启后第一轮同步打了 18 条

```
[email/fetcher] pop3 insert email uidl=...: ERROR: duplicate key value
  violates unique constraint "emails_account_id_message_id_key" (SQLSTATE 23505)
```

逐实例对比：`210827 dup=0 pop3fallback=6` / `233541 dup=0 pop3fallback=2` /
`000540 dup=18 pop3fallback=3`。所以**不是** POP3 兜底本身造成的。

**没有邮件丢失**：23505 打在 `(account_id, message_id)` 唯一索引上，
意味着这些邮件**已经在库里**，插入被拒只是"跳过一条已存在的"，
而代码把它记成 ERROR 而不是幂等跳过。同步本身完成、healthz ok。
这是个先前就存在的潜在缺陷，每次重启后第一轮同步都会暴露出来。




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
3. **`node -e` 里的反引号/引号被 PowerShell 吞掉**，改用 `.mjs` 文件。
   本轮至少踩了三次，已经改成肌肉记忆之外的条件反射。

## 5. 仍然等决定（round22 表格未动）

32 条积压提醒策略、移信 intent、网关兜底、汇总文档保留、
验证运行暂存目录、生产发票目录残留、3500 旧名副本、58000 误建档行、
手工触发流水线、真实 IMAP MOVE 授权、推送授权。

用户本轮已确认：**这些全部保持等决定，不再推进。**

「移信要不要先落成 intent」已被 round24 §2 取消（不是需求 6 的前提），
本轮未再复活它。

推送条件见 round24 §3 补记：已是 fast-forward，但仍需授权才动。

