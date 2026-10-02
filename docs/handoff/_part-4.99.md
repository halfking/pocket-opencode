## §4.99 feikemanager1@163.com 追到底：账户是好的，诊断和我的探针各错一次

### §4.99.1 一句话结论

`feikemanager1@163.com` **从来没有过缺陷**。它在正常同步，收件箱是真的空的。
本轮把它当成「最后一个未验证项」追完，结果是**两处结论都被推翻**——一处是并行会话
写进注释的，一处是我自己早前探针的。两处都不是产品的错。

### §4.99.2 真实 IMAP 登录（带对照账户）

`scripts/.scratch-imap-login.mjs`（已删）照抄 App 握手顺序
（`backend/internal/email/fetcher.go:562` `sendClientID`：name=pocketd / version=1.0.0 /
vendor=openpocket / address=<邮箱地址>），对 imap.163.com:993 走
greeting → CAPABILITY → ID → LOGIN → SELECT INBOX，逐步原样打印服务端回复。

**带一个已知可用账户做对照**（`feikemanager@163.com`），否则分不清「这个账户不行」
和「我的脚本或网络不行」——这正是本会话已经栽过好几次的坑。

| 账户 | LOGIN | SELECT |
| --- | --- | --- |
| `feikemanager@163.com`（对照） | `a3 OK LOGIN completed` | `14 EXISTS` / `3 RECENT` |
| `feikemanager1@163.com`（目标） | `a3 OK LOGIN completed` | **`0 EXISTS` / `0 RECENT`** |

⇒ **服务端不拒绝。**（b）「密码错 / 账号未激活 / IP 未放行」被实测推翻。

口令只从环境变量读，不落盘、不打印、不进命令行参数；打印时对口令打码。

### §4.99.3 凭据比对：形态 ≠ 内容

上一步用的是**用户给的口令**。库里存的那份是否同一份，仍未验证——
`diag_credential_health_test.go` 只证明了「能解密 + 16 位形态」，而形态不等于内容：
库里完全可能存着另一条 16 位字符串。

为此建了一次性 `backend/cmd/zz-scratch-creddigest`（已删）：解密后**只比较 sha256**，
只打印布尔值与长度，明文绝不离开进程。5 个账户**全部一致**。

同时带了两条自检（本仓 search_path 缺陷家族的老教训）：
读回 `current_schema()` 必须等于目标 schema；`email_accounts` 行数必须非零，
否则「零行」是查错库而不是「没有账户」。

### §4.99.4 uid=0 的真正成因：邮箱本来就是空的

`last_synced_uid=0` 有两种截然不同的成因：有邮件没抓到（缺陷）／邮箱真的是空的（正常）。
`emails` 表行数是唯一能分开的证据：

| 账户 | uid | last_synced_at | emails 行数 |
| --- | --- | --- | --- |
| `56551681@qq.com` | 10459 | 1790970418 | 98 |
| `feikemanager1@163.com` | **0** | **1790970416** | **0** |
| `feikemanager@163.com` | 1669791329 | 1790970417 | 14 |
| `huangxutao@kxpms.cn` | 11 | 1790970416 | 10 |
| `kimmy.huang@163.com` | 1298896151 | 1790970417 | 58 |

五个 `last_synced_at` 彼此只差 2 秒——刚跑完一轮轮询。`failures=0`、`last_sync_error=""`。

所以「从未成功同步过」这个说法本身就不成立：**它一直在成功同步**，
只是这个账户的 INBOX 里没有邮件，于是 `last_synced_uid` 停在 0。
这正是 `fetcher.go:776-784` 注释里写的那条路径——「没有新邮件」的早退只推进
`last_synced_at`，UID 原样不动。**这是设计，不是缺陷。**

### §4.99.5 我自己的错账：字段名少打一个 ed

早前我从设备读到 5 个账户 `lastSyncAt: null`、`lastError: null`，据此记下了
「设备上 5 个邮箱全部从未同步」。那是**我的探针打错字段名**：

- 真字段名是 `lastSyncedAt`（`backend/internal/email/model.go:22`），
- 我读的是 `lastSyncAt`（少一个 `ed`），
- 读不到 ⇒ `undefined` ⇒ 落成 `null`。

同一份 JSON 里还有 `folderCount`，而 `folderCount` 在整个 Go 后端**一次都没出现过**——
那份 JSON 本来就是我拼的形状，不是 API 原始返回。教训和上轮那条一样：
**取值必须点名，不能靠「长得像」。**

端到端复核（真打 `POST /api/emails/sync/status`，token 从设备 WebView 取、只报长度）：

```
huangxutao@kxpms.cn    lastSyncedAt=1790970416 lastAttemptAt=1790970416 pending=10
feikemanager1@163.com  lastSyncedAt=1790970416 lastAttemptAt=1790970416 pending=0
feikemanager@163.com   lastSyncedAt=1790970417 lastAttemptAt=1790970417 pending=14
kimmy.huang@163.com    lastSyncedAt=1790970417 lastAttemptAt=1790970417 pending=58
56551681@qq.com        lastSyncedAt=1790970418 lastAttemptAt=1790970418 pending=98
```

与库完全一致。`failures=undefined` 是 `omitempty` 把 0 省略了，属预期，
且前端把 undefined 显示成「从未失败」并不失真。

生产告警判据 `NeverSyncedAccounts`（`classify_run.go:101`）读的是
`LastSyncedAt > 0` 而**不是** uid——所以这个空邮箱账户不会被永久误报。这一条是对的设计。

### §4.99.6 我本轮第三次「读太浅」

`handleEmailSyncStatus` 在 `server.go` 的 `mux.HandleFunc` 列表里**找不到**，
我据此准备记「死端点」。实际它在 `/api/emails/` 子树 handler 内部靠**后缀分派**接住
（`server_assistant.go:1967-1972`）：路径是 `POST /api/emails/sync/status`。

（`/api/emails/sync` 与 `/api/emails/sync/status` 在 Go 1.22 ServeMux 下不冲突：
前者无尾斜杠只匹配自身，后者由 `/api/emails/` 子树接住。）

**判据/读数只扫一层就下结论，是本会话最贵的失误模式**：本轮三处误判
（字段名、路由注册、并行会话的 (b)）形态不同，病根相同。

### §4.99.7 需要更正的是注释，不是代码（我没动，并行会话正在那些文件里作业）

以下三处把「feikemanager1@163.com 是坏的」当成**当前事实**写进了注释，
现已证伪。留着会误导下一个读它们的人：

- `backend/internal/email/diag_credential_health_test.go:158-159`
  —— 凭 `uid==0 && lastAt==0` 推出「凭据完好，问题在服务端侧（密码错/需激活/IP 放行）」。
  这条推断当时**只差一步就是错的**：它把「从未尝试」直接读成「尝试了被拒」。
- `backend/internal/email/classify_run.go:95-99`
- `backend/internal/email/never_synced_accounts_test.go:21-24`

并行会话本轮 03:37 仍在该目录写文件（本轮 03:37 / 03:23 各新增一个被 gitignore 的
`diag_*.go`），**故未改**。生产代码本身没问题，只有注释过时。

### §4.99.8 口径与本轮遗留

- 本轮**没有改动任何产品代码**。新增即三个一次性诊断脚本/目录（IMAP 登录探针、
  凭据摘要比对、真接口复核），验证完已全部删除。
  证据留在 `logs/imap-login-20261003-0400.txt`、`logs/creddigest-20261003-0400.txt`、
  `logs/email-account-state-20261003-0410.txt`、`logs/email-sync-status-20261003-0415.txt`
  （均不含口令明文）。
- 设备侧 `adb forward tcp:9222` 本轮用完已清理（复核为空）；App 连后端用的
  `adb reverse tcp:18099` **原样保留未动**。
- 邮箱链路上真正待办的东西没有变化：§4.99.7 的注释更正、`/api/embed` 的 embedding 模型、
  以及「38 个 POST 端点未探」。
