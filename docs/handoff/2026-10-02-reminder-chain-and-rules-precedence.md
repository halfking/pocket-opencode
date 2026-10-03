# 2026-10-02（续）重要邮件提醒链路：三个「配了规则也没用」的静默缺陷

> 本文件记录今日第二轮的五个提交：把「对其它重要邮件进行提醒」这条需求从
> **判定 → 派发 → 收件人 → 落库 → 去重** 逐层钉上测试，途中修掉三处会让用户
> 得出「配了规则也没用」结论的静默缺陷。前一轮见
> `2026-10-02-invoice-collection-defects-and-real-device-deploy.md`。

## 0. 一句话结论

提醒链路今天之前**判定层之外全无测试**（`grep NotifyImportantEmail` 在
`_test.go` 里零命中）。补齐后修掉三处缺陷，它们有一个共同点：**症状完全
一样（提醒为 0、报告无异常），但成因分处链路的不同环节**，所以只按现象
排查必然走错方向。

| 提交 | 内容 | 缺陷位置 |
|---|---|---|
| `e92ae0c1` | 补派发链路测试（5 例）+ 扫描容忍 NULL 文本列 | `scanPipelineEmail` |
| `0db1b08e` | 补 LWW 服务端四条边界契约（4 例） | `UpdateAccountLWTScoped` |
| `582db4a5` | 提醒必须解析出收件人 | `notifycenterEmailNotifier` |
| `81704531` | 重跑同步必须刷新规则判出的 importance | `InsertEmailIfNew` |
| `5da2d9e9` | 规则判出的 high 不得被 AI 降级 | `SetClassificationScoped` |

---

## 1. `e92ae0c1` 派发链路零覆盖 + NULL 扫描

已有测试只钉纯函数 `splitReminderCandidates`（判定谁该被提醒）与诊断文案，
真正把邮件交给通知中心、再把成功的那批写回 `notified_at` 的循环
（`pipeline.go` 的 `notifyImportant`）从未被执行过。三种完全不同的情况在报告上
长得一模一样：`remindersSent=0` 可能是「没发出去」、可能是「发了没写回导致
每轮重复推送」、也可能是「**失败的那封被记成已提醒**」。

最后一种最危险：邮件将永远漏提醒，而 `remindersSent` 按成功数记，报告上看不出
异常。新增 5 例（真 PG）全部断言 `notified_at` 的后果。

### 顺带发现：`scanPipelineEmail` 遇 NULL 整条扫描死掉

写测试时插入一行 `from_name` 为 NULL 的邮件直接命中
`cannot scan NULL into *string`，而 `ListEmailsSince` 的三个调用点（垃圾清理 /
发票候选扫描 / 重要提醒）都是「err 就 AddError 后继续」——一行坏数据就让这三步
**同时**静默产出 0。

**证据边界（不要当成已发生的生产故障）**：真库 `opencode_pocket` 124 行、
13 列实测**零 NULL**（含 `from_name`），且 `emails` 只有一个写入者
`InsertEmailIfNew`（传 Go string，`from_name` 走原值而非 `nullStr`）⇒ NULL
当前不可达。DDL 侧这些列都没有 NOT NULL，而本库历史上被手工加过列
（`updated_at`、`folder_name`），所以这是**潜在**脆弱点。改成容忍（按空串处理）
不改变任何当前能跑通的行为。

---

## 2. `0db1b08e` LWW 服务端四条边界

主干（新鲜基准放行 / 过期拒绝且不改行 / 跨 scope 404 / 零基准退化）早有 4 个
用例覆盖。缺的是：

1. **409 回填服务端 `updated_at`** —— `server_assistant.go` 的冲突分支靠它让
   客户端改走下行覆盖，Go 侧从没人钉；
2. **同一秒内两次写入严格递增**（承诺是 `max(now, base+1)`）——已有用例靠
   `time.Sleep(1100ms)` 跨过秒边界，恰好绕开了这个承诺要解决的场景；
3. **凭据不被误改**（两条 SQL 分支占位符编号不同，是事故高发区）；
4. **不存在账户回 404 而非 409**。

### 过程教训：普查 grep 撞上限把「已覆盖」判成「零覆盖」

我最初判「LWW 服务端零覆盖」是**错的**：`grep` 的 `limit=40` 静默截断，40 行全被
其它包的端口字面量占满，`internal/email` 的命中整段被截掉。更糟的是按这个错误
结论新建了**同名文件**，Write 返回 `overwrote existing file`，覆盖掉已有的 127 行
测试（`git checkout --` 已恢复）。**覆盖率普查的 grep 必须翻页到底**；写新文件前
先 `glob` 同名文件。

---

## 3. `582db4a5` 提醒发错人 + 永久漏提醒

`notifycenterEmailNotifier` 是这条需求里**唯一决定「提醒发给谁」**的地方，此前
零覆盖。它有一处静默失败：

```go
if acc, _, err := n.store.GetAccountByID(ctx, e.AccountID); err == nil && acc != nil {
    userID = acc.UserID        // 解析失败被吞掉
}
```

账户查不到时（邮件行还在、账户已删除）`userID` 为空，两个坏结局叠加：

1. **越权广播** —— `notifycenter` 的 `WebsocketSender.Send` 注释写明「无 user_id
   退化为全局广播」，别人邮箱里的重要邮件会推给同 workspace 所有在线用户；
2. **永久漏提醒** —— `Dispatch` 不报错，流水线于是把这封记进 `notified_at`
   （＝已提醒），报告一切正常，再也不会重发。

改成解析不出归属就返回错误（带账户 id）：流水线记进 `rep.Errors` 且**不**写
`notified_at`，下轮可重试。顺带修空主题时标题变成「重要邮件：重要邮件」。

### 更正一条我自己上轮的结论

我曾说这层「跨包做不了假 store，必须改 `notifycenter` 的导出口子才能测」——
**错**。`notifycenter.New(pool)`、`NewService(store, sender)` 与 `Sender` 接口
都是导出的，用真 store 跑在临时 schema 上即可，不需要任何设计改动。

---

## 4. `81704531` 「先收信、后配 rules」这条路是断的

真库 5 个账户 `rules` 全为 NULL、`importance` 恒空 ⇒ 提醒一封也发不出去。
解锁最便宜的办法是配 rules（不需 LLM 配额、不需新依赖），于是「先同步过邮件、
之后才配规则」就是用户一定会走的那条路。

`InsertEmailIfNew` 的 `ON CONFLICT (id) DO UPDATE` 只刷新 `snippet` 一列，规则
算出的 `importance=high` 在冲突分支被直接丢掉。症状极具迷惑性：

- 新邮件走 INSERT → 有 importance、有提醒；
- 旧邮件走 DO UPDATE → 永远补不上；
- 而用户唯一能让旧邮件重过规则的办法（重置 `last_synced_uid` 重同步）走的
  **正是 DO UPDATE**。

于是现象是「新邮件有提醒、老邮件没有」，极易被当成规则写错了。修法：
`importance` / `action_reason` 改为「**规则确实判出来才刷新**」，空值保留旧值。

---

## 5. `5da2d9e9` 规则判出的 high 被 AI 降级

与 §4 **症状一模一样、方向相反**：那里是规则没落进库，这里是落进库又被抹掉。
每一环都实测过：

1. 只配 `mark-important` → `importance='high'` 而 `category` **仍为空**；
2. `ListUnclassifiedScoped` 挑待分类邮件过滤的是 **category**（不是 importance）
   ⇒ 这封照常进 AI 队列；
3. `BuildClassifyWrites` 只要求 category 非空，LLM 没给 importance 时是空串；
4. `SetClassificationScoped` 是**全量覆盖** ⇒ `importance` 被写成 `''` 或 `'normal'`。

修法：旧值是 `high` 且 AI 不想给 `high` 时保留 `high`。只保护 `high`、只在
**试图降级**时保护——AI 给 `high` 要能写进去，旧值为空/normal 时按 AI 的写，
否则就成了「一律保持 high」，AI 永远无法提升重要性。

`TestRuleMarkedEmailStillEntersClassifyQueue` 单独钉住第 2 环的**可达性**，不靠
读代码下结论。

### 一次没转红的负控（判据警告）

第一个负控变异是
`CASE WHEN e.importance='high' OR $2='high' THEN 'high' ELSE $2 END`，
**看起来更严格**，但与正确写法在所有可达输入上结果完全相同，**exit 0**——
`$2='high' THEN 'high' ELSE $2` 恒等于 `$2` 本身。换成真正有区分力的变异
（把条件弱化成 `$2 = ''`，只挡空值不挡降级）才转红。

**「更严格」的外观会掩盖「更宽」的语义；no-op 变异比转红更危险，因为它容易被
读成「这条断言冗余」进而把承重的断言删掉。**

---

## 6. 有意保留的语义边界（需要拍板）

`category` 两处**刻意**不参与规则刷新/保护：入库时由 `label-category` 播种，
之后归 AI 分类（`SetClassificationScoped`）拥有。若让规则在重跑时覆盖它，规则
会反过来压过 AI 分类。**这是语义选择不是缺陷**——若希望规则也拥有 category，
改动点已在上文两处标出。

另：`SetClassification`（无 scope 版本）全仓**零调用方**，保留未动。

---

## 7. 验证

- `go vet` 干净。
- `go test -race -count=1 ./internal/email/ ./internal/server/` → exit 0，无
  DATA RACE，无 `(cached)`。逐次耗时见各提交信息。
- 负控共 17 路，全部实测转红（含 1 路被识破的 no-op 变异）。
- `internal/email` 与 `internal/server` 整包是 CRLF，`gofmt -l` 会列出
  170+/175 个文件，那是行尾噪音不是格式问题。

---

## 8. 仍需用户决定（未变）

1. **admin 口令** —— 缺它无法在真机看到邮件列表，「部署到真机」最后一步。
2. **重启 pocketd** —— 今日 26 个提交未进运行进程，需 `POCKET_EMAIL_MASTER_KEY`。
3. **5 个账户配 rules**（路径今天已全程修通）或 LLM 配额。
4. 发票缺陷 A（从 PDF 抽金额，需新依赖）/ B（解 ZIP）、飞书凭证、IMAP 挂死四选一、
   163 POP3 授权、垃圾 MOVE、OAuth 绑定、`category` 归属。
