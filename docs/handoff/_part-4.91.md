
## §4.91 隔离验证环境落地：把「不能安全跑」变成「能跑且不污染」，外加两个自己工具里的真缺陷

本节记录一次以「解锁一批跑不了的验证」为目标的改动，以及在做的过程中
**在自己的测量工具里**翻出来的两个缺陷（Bugs V15 / V16）。两个都不是新功能，
但都属同一类：**判据说的话和判据实际做的事不是一回事**。

### §4.91.1 隔离环境：`POCKET_PG_SCHEMA` 确实是硬隔离，不是纸面参数

- 后端：`scripts/start-local-backend.ps1 -Port 18101 -Schema opencode_pocket_verify`
  （DataDir `backend/data-verify`），与共享后端 18099 / 18077 / 18100 并存。
- 隔离依据：`backend/internal/config/config.go:259`
  `PostgresSchema: getEnv("POCKET_PG_SCHEMA", "opencode_pocket")`。

验证脚本 `scripts/verify-schema-isolation.mjs`（11 条判据全过，exit 0）：

| 判据 | 结果 |
|---|---|
| 隔离 schema 收到了这一行（同一 SQL 打 verify 返回 **1**） | PASS |
| 共享 schema 没收到这一行 | PASS |
| public schema 没收到这一行 | PASS |
| DELETE 返回 204 / 按 id 复查隔离已空 / 按 id 复查共享为空 / 按 note 复查已空 | PASS |
| 隔离 schema 总行数回到 POST **前**的基线 | PASS |
| 共享 finance 表、共享 tasks 表总行数均未变 | PASS |
| 阳性对照：共享 schema 确实有数据（`shared.tasks=2`） | PASS |

**为什么要有「阳性对照」**：「共享库里数到 0」在**那张表根本不存在**时也会得到 0。
所以除了阴性结果，还必须证明 psql 真的看得见 `opencode_pocket`（用 `shared.tasks > 0`），
以及同一条 SQL 在隔离 schema 上**确实返回 1**（证明判据不是恒真）。

**三次自伤换来的三条纪律**（都写进了脚本注释）：

1. **基线必须在 POST 之前取。** 第一版把基线取在 POST 之后却标成「播种前基线」，
   于是「删后回到基线」永远差 1。当时的输出是 `verify 总行 1（期望回到基线 2）`
   ——看起来像隔离出了问题，实际上是**判据和它的标签一起说谎**。
2. **「共享里 0」要配阳性对照。** 共享的 finance 表本身就是空的，
   这条的强度有限；脚本会把它当 NOTE 打印出来，而不是假装它很强。
3. **SQL 报错必须响亮退出。** 第一版 `catch` 里 `process.exit(2)`，
   会把「查错了表」伪装成「结果是 0」——而第一版恰恰就写错过表名
   （真表名是 `finance_transactions`，不是 `finance`），报的是
   `relation "…finance" does not exist`。现在改成 `throw`，由异常钩子兜住清理。

**故障注入负控**（`POCKET_FAULT=sql`，在播种**之后**故意执行一条会报错的 SQL）：

```
[清理 uncaughtException] DELETE txn_1790960258955121400 -> 204
uncaughtException: Error: SQL 失败：SELECT count(*) FROM opencode_pocket_verify.no_such_table_xyz
exit=1
```

顶层 `await` 抛错确实会触发 `uncaughtException`，钩子里的 `cleanup()` 真的把行删了。
`process.on('exit')` 不能 await，所以钩子挂在 `unhandledRejection` / `uncaughtException` 上。

顺带清掉一行历史遗留（`ISOLATE-856420`，时间戳早于已知的那次运行 ⇒ 是某次崩溃
在跑到 DELETE 之前就挂了留下的，正是 BUG-V14 那一类）。`--purge-residue` 显式 opt-in，
逐条走 API 删，不裸 SQL 写库。

### §4.91.2 真实写负载下的隔离：共享库 66 张表两轮跑完零变动

跑之前/之后各拍一次 `opencode_pocket` 全表行数快照，diff 必须为空：

```
before=66  after=66   SHARED SCHEMA UNCHANGED after 2 full probe runs (66 tables)
```

这是比「单条 finance 行落在哪」强得多的证据：它是**六个真写脚本各跑两遍**之后的
全库零变动。

### §4.91.3 6 个写路径探针首次实跑：6/6 exit 0，27 PASS

`scripts/run-isolated-probes.mjs`（新，串行执行、逐个落日志、区分「脚本失败」与「runner 自己抛了」）：

| 脚本 | exit | PASS | FAIL |
|---|---|---|---|
| probe-vault-api.mjs | 0 | 6 | 0 |
| probe-vault-sync-empty-blob.mjs | 0 | 3 | 0 |
| probe-gateway-nodes-api.mjs | 0 | 6 | 0 |
| probe-email-account-api.mjs | 0 | 3 | 0 |
| probe-email-sync-honesty.mjs | 0 | 5 | 0 |
| verify-bug-z.mjs | 0 | 4 | 0 |

这 6 个是**纯 API** 脚本：读 `POCKET_API_HOST`/`POCKET_API_PORT`，不碰 adb、不碰 CDP、
不直接查 PG ⇒ 把 base 指到隔离后端，写入就落在隔离 schema。

runner 里两处刻意写法：只统计**行首**的 `PASS`/`FAIL`（之前栽在 `Select-String`
大小写不敏感上，响应体里的 `failed` 被数成了 FAIL）；`spawnSync` 的非 0 退出与
「我自己抛了」分开归类。日志目录不存在时 `mkdirSync` 补上，且写日志失败不许中断整轮
——头一版那行 `writeFileSync` 在 `try` 之外，目录不存在时 runner 自己崩掉，
**第一个脚本的结果也一起丢了**。

### §4.91.4 BUG-V15：邮件同步诚实性探针 —— 前置不自建 + 结论硬编码已过时 + 退出码恒 0

首次实跑就报 `FAIL 找到那个指向不存在主机的账户（前置） — (没找到)`，
但 **exit=0**。三处缺陷：

1. **前置不自建。** 脚本只做 `accounts.find(a => a.imapHost === 'imap.invalid.test')`，
   却从不创建那个账户。在干净环境里它**永远**跑不起来。
2. **结论硬编码且已过时。** 脚本结尾无条件打印
   「前端 `EmailAccountAddView` 只读 `sync.new`，不读 `sync.failed`，所以把失败显示成了成功」。
   实测 `frontend/src/features/email/EmailAccountAddView.vue:219-226` **已经读**
   `sync.failed` 并据此 `imapOk.value = false`——那个 bug 早就修了。
   一句过时的断言留在脚本里，会让下一个读它的人以为问题还在。
3. **退出码恒为 0。** 前置缺失时整段探针被 `if (target)` 跳过，
   打印「0/1 通过」然后 exit 0 ——自动化无从分辨「跑过了」和「什么都没跑」。

修法：

- 自己建 `imap.invalid.test`（RFC 2606 保留 TDD，永不可解析）账户，跑完在
  `finally` + 异常钩子里删；建不起来就 `exit 3`，响亮失败。
- 结论改成**从源码推导**：读前端文件，判它到底读不读 `sync.failed`、有没有据此置 false。
  修完实测输出是「前端确实读 / 确实置 false ⇒ **不存在**『把失败显示成成功』的问题」。
- 退出码反映判定。

**判据自证**（`--selftest`，10/10 通过）：每条读外部输入的判据都喂一个必须判 false 的
输入——`{failed: []}`、`{}`、`null`、`'not json'`、空源码、只读 `sync.new` 的源码、
读 failed 却仍置 `true` 的源码。**变盲对照**（`POCKET_FE_FILE` 指向不存在的文件）：

```
FAIL  前端确实读 sync.failed（否则失败会被显示成成功）
FAIL  前端据 failed 把结果置为失败
3/5 通过    exit=1
```

后端那三条仍 PASS ⇒ 不是整体变盲，只是前端那两条对「读不到文件」敏感。

修完实跑：5/5 PASS，exit 0，账户删净（隔离 schema 里 `honesty-*` 两轮都清掉了）。

### §4.91.5 BUG-V16：30 处写死 PG schema，把写路径脚本锁死在共享库上

一批「直接查库对照」的探针把 `opencode_pocket.` 写进了 SQL。两个后果：

1. 它们**只能**对着共享开发库跑 ⇒ 失败时 SEED 留在**另一会话**的库里（BUG-V14 的放大器）。
2. 想在隔离后端上验证它们时，断言会去查**另一个** schema ——
   要么假失败，要么更糟：静悄悄对着错库给出「通过」。

- 门禁 `scripts/check-pg-schema-hardcoded.mjs`（新）：`--selftest` **11/11 通过**
  （敏感度 2 / 特异度 5 / 变盲 2 / 自指豁免 1 / 注释归类 1），实跑 **0 命中**。
- 迁移 `scripts/migrate-pg-schema.mjs`（新）：12 个文件 30 处，`flashcards-test-fixture.mjs`
  与 `marshal-probe.mjs` 手改（前者是**跨行模板串**，逐行匹配处理不了，且它的 DELETE
  是自清理部分，值得手工）。

**迁移脚本自己也翻车了一次，值得记**：

- 锚点规则太松：`process.env.POCKET_PSQL,` 这行落在 `resolvePsql()` 里**多行数组
  字面量的中间**，SCHEMA 声明被插进去 ⇒ `verify-bugaa-realdevice.mjs` 语法错误。
- 而 `node --check` 查的是**磁盘上的旧文件**（改完还没落盘），所以放行了。
  **判据没对着被测对象。** 两处都修了：锚点必须在语句边界
  （行尾 `;` 或行首 `const|let|function`），语法检查改为写临时文件后检查新内容、
  `finally` 里删。

**负控**：把 `verify-bugaa-realdevice.mjs` 的改前版本取成 `_negctl-bugaa.mjs` 再跑一次迁移 ——
现在它**跳过并说明原因**（「找不到语句边界上的 POCKET_PSQL / psql 帮助函数锚点」），
不再产出坏文件。旧行为是静悄悄写坏。

判据与门禁的一致性也踩了一次：迁移头一版按**全文**数 `opencode_pocket.` 出现次数，
而门禁按行排除整行注释 ⇒ 7 个跳过里有 6 个纯属这个不一致。已统一成同一个计数函数。

### §4.91.6 隔离环境解锁不了什么（重要边界，别高估它）

`verify-finance-writepath.mjs` / `diag-finance-*` 这类**真机 UI** 脚本，
隔离环境**救不了**：UI 走哪个后端由设备侧 `adb reverse` 决定（当前指向 18099，
是并发会话的后端），不是由脚本的 env 决定。改那个映射是**共享可变状态**，
没跟对方确认之前不动。

所以这轮的准确表述是：**3 个 finance 脚本已 schema 化（可对着隔离后端跑），但未实跑**。
未实跑 ≠ 已验证。

### §4.91.7 本轮自己的工具翻了车（记账）

- runner 的 `writeFileSync` 在 `try` 之外，目录不存在时 runner 自己崩，第一个脚本结果一起丢。
- 迁移的 `node --check` 查旧文件（上面已详述）。
- 迁移的注释/代码判定与门禁不一致，导致 6 个假跳过。
- `Out-File -Encoding UTF8` 造夹具时带出 BOM，`node --check` 报
  `Invalid or unexpected token` ——那是夹具的问题不是迁移的问题。
  **看到语法错先确认错在哪个文件**：那次的 BOM 来自 PowerShell，不是代码。
