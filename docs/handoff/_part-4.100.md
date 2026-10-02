## §4.100 修掉一个真缺陷：/api/app/download 的 404 会被存成假 APK

### §4.100.1 怎么发现的：把「38 个 POST 未探」从散文账变成可重跑的扫描

交接里「38 个 POST 端点未探」是一条**散文记账**，跨很多轮累积，无法复核，
也一定会再次过期。与其继续往散文里追加，不如产出一份能重跑的扫描：
`scripts/sweep-api-readonly.mjs`（本轮新增，**故意不接进 gates**——它需要一台
开着真机 WebView 的设备才能取到 token，CI 里跑不了；gates 里放一个必然失败
或必然跳过的脚本，比不放更糟）。

139 条注册路由逐条 GET，先打对照路由证明 token 有效（401/403 则不产出任何结论、
exit 3）。结果：

| 状态 | 条数 | 含义 |
| --- | --- | --- |
| 200 | 48 | 正常 |
| 400 | 25 | **缺 id 的参数校验**——handler 确实被路由到并做了校验 |
| 405 | 46 | **正确的方法拒绝**（这些是 POST-only 端点） |
| 404 | 14 | 见下 |
| 503 | 1 | `/api/redclaw/health` |

**没有意外 5xx。** 405 与 400 不是「没探到」，而是**另一种覆盖率**：它们证明
handler 活着并做了该做的校验。404 里绝大多数是子树路径被裸探（`/api/agents/`
缺 id、`/api/emails/` 缺 email id），另有三组是**有意的功能关闭**：

- `/api/auth/sso/*`（3 条）→ `sso not enabled`
- `/api/redclaw/*`（3 条）→ `RedClaw bridge not configured`
- `/api/flashcards/`、`/api/learning/` → not found

RedClaw 是**外部集成**（auth mirror / admin 权威源 / LLM 兜底 / 定时执行器），
需要一整套 `POCKET_REDCLAW_*`。本地实例一项没配，503 fail-closed 是**正确行为**，
与 `/api/embed`、飞书密钥同类，属「缺外部资源」而非缺陷。**新增一条待用户提供的资源。**

### §4.100.2 缺陷：404 响应带着 `Content-Disposition: attachment; ...apk`

`handleDownloadAPK` 原来是这个顺序：

```
w.Header().Set("Content-Type", "application/vnd.android.package-archive")
w.Header().Set("Content-Disposition", "attachment; filename=opencode-pocket.apk")
http.ServeFile(w, r, apkPath)     // 文件不在 -> 404
```

实测（`GET /api/app/download`）：

```
STATUS=404   Content-Length: 19
Content-Disposition = attachment; filename=opencode-pocket.apk
Content-Type = text/plain; charset=utf-8
```

`http.Error` 会覆盖 `Content-Type`，**却不会删掉 `Content-Disposition`**。
所以那 19 字节的 `404 page not found` 会以 `opencode-pocket.apk` 为文件名被存下来。
用户点「下载更新」、拿到一个 19 字节的假 APK，安装时才报解析失败——
真实原因（服务器上没部署 APK）在客户端表现为「包坏了」，排查方向被彻底带偏。

**404 长成「下载成功但文件坏了」的样子，这是它自己的错。**

顺带确认：这个端点**不套 `requireAuth`**（路由表里是裸的 `s.handleDownloadAPK`），
这是对的——APK 下载本就该对未登录设备公开。不是漏鉴权。

### §4.100.3 修复：先 stat 再设头 + 路径搬进配置

`backend/internal/server/server.go` 的 `handleDownloadAPK`：

1. `os.Stat` 确认文件在（并单独拒掉「路径是目录」），**再**设下载头；
   失败时走 `http.Error` + 日志记实际路径，**不回显服务端路径给调用方**。
2. 路径不再硬编码：新增 `POCKET_APK_DOWNLOAD_PATH`（`config.Config.APKDownloadPath`）。
   原硬编码值搬成 `config.DefaultAPKDownloadPath`，**默认值故意保持不变**——
   搬进配置是为了让它**可改**，顺手改掉一个正在被某台机器依赖的路径是另一件事，
   而那台机器不在仓库里、也不在本轮能观察到的范围内。

### §4.100.4 判据：6 条 + 负控（含我自己把判据写错的一次更正）

`backend/internal/server/apk_download_test.go`（6 条）：

- 三个**失败路径**：文件缺失 / 路径是目录 / 配置为空退回默认 —— 响应都**不得**
  携带 `Content-Disposition`，也不得宣称自己是 APK。
- 两个**成功路径**（对照组，防退化成「一律不设头」）：文件在时必须照常带下载头
  并**原样服务文件内容**；路径必须来自配置。
- 一条钉住 `DefaultAPKDownloadPath` 未被擅自改动。

**负控**（`logs/apk-negctl-20261003-0410.txt`）：把 handler 退化成「先设头再 stat」，
失败路径整组转红、成功路径整组保持绿，**恰好是这个分布**；真文件 sha256 还原一致
（`2b07db75…`）。

负控脚本自己也栽了一次，值得一提：第一版按「只准红 1 个」判定，报「红了 3 个 = 判据过宽」。
**那条规则本身是错的**——三个失败路径用例守的是**同一个**不变量，只是三个真实入口，
刻意纵深防御；退化实现让它们一起红才是正确的。改成两段式语义后成立：
「该红的整组红 + 该绿的整组绿」。**改的是判据的语义，没删任何断言。**

负控脚本还因为用 `\n` 写变异片段而在 CRLF 的 `server.go` 上**一个字符都匹配不上**
（exit 2 作废）——静默匹配失败正是本会话反复栽的坑，已改为按文件实际行尾构造。

### §4.100.5 口径：本轮没有做到的事

- **未在真机/真后端验证修复效果。** 18099 上跑的是并行会话的二进制
  （`pocketd-invoicenan-fix`，pid 8168，01:32:59 启动），**全程未触碰**：
  重启它等于把代码换到别人脚下（该仓的 restart 脚本自己也这么警告）。
  修复的行为证据来自 `httptest` 直接打真实 handler（`ResponseRecorder` 捕获的就是
  handler 设置的响应头），**不是**「线上已生效」。
- **没有跑 `gofmt -w`。** `server.go` / `config.go` 被 gofmt 标出，但那是
  **既有**问题、且 193 个文件同样被标出——根因是这些文件是 CRLF 而 gofmt 要 LF。
  LF 副本上的 diff 显示真正的问题在别处（`RSSConfig` 的对齐、`wecom` 的 import 顺序，
  都是别人的既有代码）。跑 `gofmt -w` 会在共享仓里制造上百 KB 无关 diff，
  还会顺手改掉并行会话的 import 排序。**没有动。**
- 对两个 CRLF 文件的编辑**保留了 CRLF**（`server.go` CRLF=2596/bareLF=0，
  `config.go` CRLF=734/bareLF=0）；本轮新建的 `apk_download_test.go` 是 LF，
  与我上一轮建的 `app_version_compare.go` 一致。
- `internal/server` 与 `internal/config` 全量测试均 exit 0；`go build ./...` exit 0。
