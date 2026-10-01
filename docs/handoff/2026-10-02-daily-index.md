# 2026-10-02 当日索引：邮件需求推进全记录

分支：`feat/mail-config-deploy`　当日提交 28 个　当日 handoff 文档 13 份

这份文档只做**索引与结论汇总**，细节在各自文档里。按「结论是否可信」分三档：
已验证 / 已推翻的旧结论 / 仍未验证。目的是让人不必把 13 份文档通读一遍
就能知道现在能信什么、不能信什么。

---

## 一、当日修掉的真缺陷（8 个）

| # | 现象 | 根因 | 提交 |
|---|---|---|---|
| 1 | 摘要显示原始 MIME | 3 个根因（QP 折行、boundary 尾巴、无条件回退） | `064ce292` |
| 2 | 归类循环空转 | 只看「有进展」不看「有进展」 | `48ae6aad` |
| 3 | 下拉刷新只插不更 | `ON CONFLICT DO NOTHING` | `394ec6f7` |
| 4 | 真实库 0 条发票 | 候选窗口 24h 把历史全挡住 | `46d9e779` |
| 5 | 装机包连不上后端 | 构建默认值 `localhost` 在真机不可达 | `f9fe343f` |
| 6 | 字段写了从不读回 | `GetEmailByID` 的 SELECT 漏 `message_id`/`body_purged` | `a706bf55` |
| 7 | 用户改 IMAP 主机被静默丢弃 | LWW 上行只推 3 个字段 | `62763592` |
| 8 | 发票链接下到非 PDF 被静默丢弃 | 只判 `StatusCode != 200` | `a4d9e313` |
| 9 | 提醒提示把排查方向指错 | 只提 AI 分类，不提账户规则 | `f8a85396` |
| 10 | 台账 CSV 合计落在「文件名」列 | 手写格式串逗号数错 | `33239a3f` |
| 11 | 台账 MD「共 N 张」与合计口径不一致 | 头部用 `len(invoices)`，合计用另一套 | `28096955` |
| 12 | 幻影开关 `enable_dangerous_actions` | 注释承诺了一个不存在的闸门 | `992880c6` |

---

## 二、当日推翻的旧结论（3 条）

继承的结论不能默认是对的——今天推翻 3 条、验证通过 1 条。

| 旧结论 | 实情 | 文档 |
|---|---|---|
| 「每日定时流水线是死代码，`SetPipelineRunner` 全仓无调用点」 | **错**。调用点在 `cmd/pocketd/main.go:767`，运行日志有 `pipeline scheduled at 2026-10-02T08:00:00+08:00`，4 个测试早已存在 | `2026-10-02-correction-scheduled-pipeline-and-importance-provenance.md` |
| 「重要提醒靠本地规则引擎，所以真实数据上是通的」 | **归因错**。规则引擎需要账户配 `rules`，真实库 5 个账户 **rules 全为 NULL**，该路径一次都没执行。投递链路确实通（`reminders=24`），但稳态不会触发 | 同上 |
| 「装机包连不上后端是 URL 丢失」 | **错**。真因是 localStorage 按 origin 分区，`androidScheme` 默认 https | `2026-10-02-device-localhost-api-base-unreachable.md` |
| 「列表 `messageId`/`uid` 是死字段，无可证实影响」 | ✅ **成立**。已独立复核：邮件域内除 store 自身的类型/写入/读回外无消费方；`api/email-cleanup.ts` 里的 `uid` 是**响应**字段，请求侧用 accountId/subject/from/since/until，不依赖 uid | 本文档 |

---

## 三、当日修掉的假绿（6 处，其中 3 处是既有测试）

**这是今天最值得记住的一类问题：绿灯的前提是「被测逻辑真的被执行过」。**

| 用例 | 名不副实之处 | 提交 |
|---|---|---|
| `TestHarvestOne_CachedRawStillCountsAttempt` | 名字钉「缓存命中路径」，实测走的是 `Fetcher` 失败路径（邮件 ID 缺 `em-pop3-` 前缀，BodyCache 根本没被查）；且从未建档，每轮 `UpdateInvoiceHarvest` 都在失败 | `1792d3fc` |
| `TestWriteInvoiceSummaryDocs` | 断言字面量 `合计,,,,,,,100.00,`，把「金额落在第 8 列」这个 bug 固化成了期望值 | `33239a3f` |
| 台账 MD 头部用例 | 我自己第一版写成 `Contains(head, "1")`，被日期里的 `2026-10-02` 满足，缺陷在、断言照样绿 | `28096955` |
| `SpamLocalOnly` 幂等断言 | 该字段在真实模式下结构上恒为 0，断言恒真（我自己写的） | `5e1d93d7` |
| 账号对称性字段判据（缺陷 14 期间） | `imapHost: undefined` 骗过「字段名出现过」的判据 | `62763592` |
| `as AuthType` 未被类型剥离覆盖 | describe 块静默跳过，读起来像 pass | 缺陷 14 期间 |

**还有一次是测量错误而非测试问题**：跑负控时漏了 `POCKET_TEST_POSTGRES_DSN`，
`newWorkspaceTestStore` 直接 skip，输出是 `ok`——**skip 和 pass 在汇总里一模一样**。
凡是需要 PG 的用例必须显式设 DSN；看到 `ok` 先确认它不是 skip。

---

## 四、当日补上的验证缺口（5 处，均为「测了零件、没测装配」）

看一个用例叫 "EndToEnd" 之前，先确认它有没有真的经过那个入口。
`os.WriteFile` 手工落盘的「端到端」只测了解析器/渲染器，没测采集器。

| 缺口 | 补上后 | 提交 |
|---|---|---|
| 发票「多次操作才能下载」跨轮路径无人验证 | 选取查询若写错，既有状态机测试照样全绿 | `1792d3fc` |
| XML 发票从没走过 `harvestOne` | `source=xml-render` + `mergeXMLFields` 补齐四个字段 | `d559015a` |
| 附件发票从没走过 `harvestOne` | PDF 附件 + 拍照图片（扩展名不串） | `58b56427` |
| 清垃圾真实 MOVE 分支从未执行 | 预演零写入 + 本地标记 + **账户隔离** | `5e1d93d7` |
| 台账合计列位 / MD 口径 | 按列解析断言，不数字符串 | `33239a3f` |

其中「账户隔离」值得单说：`MarkEmailsSpamByUID` 的 SQL 是
`WHERE account_id=$1 AND uid = ANY($2)`，而不同邮箱的 IMAP UID **各自独立编号、
撞号是常态**。`account_id` 条件一旦写坏，给 A 标垃圾就会连坐 B。这个属性从未被验过。

---

## 五、需求逐条状态

| 需求 | 状态 | 依据 |
|---|---|---|
| 每天定时或手工收信 | ⚠️ 接线正确，**到点真跑待 08:12 核对** | 旧文档明写「没有等过一次真实触发」 |
| 清理广告垃圾 → 移垃圾箱 | ⚠️ 预演实测 0 命中（最高 30 分 / 阈值 100）；真实分支现已测过，开不开待定 | `28d35c6d` / `5e1d93d7` |
| 发票整理下载 `{费用类型}-{对方单位}-{金额}-{日期}.pdf` | ✅ 真实数据跑通 1 条 | `b68799eb` |
| 可能需要多次操作才能下载 | ✅ 跨轮路径已验证（先 503 后 200） | `1792d3fc` |
| 共享文档 + 列表 + 汇总金额 | ✅ 内容达标（**修了合计列位与口径**） | `33239a3f` / `28096955` |
| PDF 下载地址 / XML 重渲染 | ✅ 两个来源均补上采集器级验证 | `58b56427` / `d559015a` |
| 其它重要邮件提醒 | ❌ **稳态不触发**：rules 全 NULL + 无分类器 | `2026-10-02-correction-scheduled-pipeline-and-importance-provenance.md` |
| A4 2x2/3x3 网格导出 | ✅ 18 个真实产物 | `b68799eb` |
| 默认设备本地执行 | ⚠️ 架构缺口：`frontend/android` 是纯 WebView 应用、无自带 pocketd | 未实施 |
| 邮件窗口查看各类邮件 | ✅ 前端 email/invoice 域 252 例、0 跳过 | 本文档 |
| 邮箱配置入库 + LWW 同步 | ✅ 服务端 409 守卫完整（**修了上行字段缺口**） | `62763592` |

---

## 六、运行中的进程落后于全部修复（重要）

```
pocketd 启动于 01:36，已运行 5 小时以上
当日全部修复都在 06:00 之后
```

**运行中的 pocketd 不含今天任何一个修复。** 08:00 那轮定时任务用的仍是旧二进制，
所以它产出的台账仍会带合计列错位与 MD 口径不一致。

重启需要 `POCKET_EMAIL_MASTER_KEY`（只存在于该进程环境变量中），未经确认未执行。

---

## 七、等用户决定的 8 件事

| # | 问题 | 影响 |
|---|---|---|
| 1 | 是否给 `route-folder` / `trigger-autoreply` 加显式 opt-in | 会真的 IMAP MOVE / SMTP 发信，目前无闸门 |
| 2 | 配 `rules`（含 mark-important）还是接 LLM provider | 决定重要提醒能否真正生效 |
| 3 | 真实库 52 行带外 importance + 24 行 notified_at 清不清 | 通知中心当前显示的是演示数据 |
| 4 | 台账 `exports` 保留策略（现 136 文件零清理，注释却写「重建」） | 涉及删除用户数据目录文件 |
| 5 | InfoQ newsletter 算不算垃圾 | 决定是否调阈值/开真实 MOVE（现在有测试兜底） |
| 6 | `/api/tasks` 静默吞 DB 错误是否改 500 | — |
| 7 | 是否固定注入 master key 并落运维文档 | key 一丢 5 个邮箱凭据全部作废 |
| 8 | 是否提供 master key 以便重启验证 | 否则今日修复不会生效 |

另有 3 件待办：飞书凭证（POCKET_FEISHU_APP_ID/SECRET/INVOICE_CHAT_ID）、
真机无线调试（`192.168.31.19:5555` offline）、
是否重置 `last_synced_uid` 重同步 3 个账户以刷干净 25 封脏摘要。

---

## 八、清理项（未做）

- `InvoiceHarvester.savePDF`（`invoice_harvest.go:418`）：全仓零调用点，函数体只是
  `saveInvoiceFile` 的别名转发
- `ActionReason` / `Email.DeletedAt`：死字段
- 列表 `messageId` / `uid`：客户端本地只写不读（已复核，无影响）
- `MarkEmailsSpamByUID` 的 WHERE 里 `(category IS NULL OR category = '' OR category <> 'spam')`
  三个析取项里第三项让整个条件几乎恒真，语义上等价于 `category IS DISTINCT FROM 'spam'`。
  不是缺陷（行为正确），但条件写得让人以为有额外语义
- `ListHarvestableInvoices` 是 `ORDER BY created_at` + 单轮只处理 20 张：
  pending 超过 20 时同批最老的会连跑 8 轮耗尽重试才轮到下一批。
  **有界、不会永久饿死**，但 pending 多时第 21 张要等 8 天
