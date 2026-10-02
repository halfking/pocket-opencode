# Round 10 — 24 小时修正审计（2026-10-02）

接 round9。本轮的全部产出是三个提交（`7698b6f4` / `b3cdd81` / `b20be8e9`）
与 parked 分支上一个提交（`211e590a`）。真正的发现只有一个，但它正好
落在本仓库最贵的那类教训上：**一个看起来已经审过的改动，单独提交是破的。**

---

## 1. 本轮最重要的一条：tokens.css 的两档不能单独补

### 结论

`audit/2026-10-02-pending-human-decisions`（3d067740）里那个 tokens.css 改动
——「补 `--text-2xs(11px)` / `--text-smd(13px)` 两档」——**单独提交会让测试
转红**。它被 round9 记为「补刻度是后续替换的前置条件，数值与现有视觉完全一致」，
那句话本身没错，错的是它**作为一个可独立落地的步骤**。

### 实测

    只补两档刻度，不做替换
    → font-size-token-equal.test.mjs：# fail 2，报「348 处 font-size 数值与
      某个字号 token 相同却仍写死像素」

原因在判据的实现：`loadTokenScale()` **现读 tokens.css**（注释明写「从
tokens.css 现读，不写死刻度」）。判据是「凡数值等于某个 token 就不许写死
像素」——11px / 13px 原本是**刻度外值**，补进刻度的那一瞬间，全仓 348 处
写死的 11px/13px 就从「刻度外，不归本判据管」翻转到「等于 token 却仍写死」。

**判据本身没改，是它正确地报告了新违规。** 这也是为什么「补刻度」与
「做替换」是一个原子步：拆开则中间态必然红。

### 处置

本轮把这一步**补完整**并合进 main（`7698b6f4`）：

- tokens.css 补两档（11px / 13px），阶梯变为 10/11/12/13/14/15/16/18；
- 348 处写死像素替换为 `var(--text-2xs)` / `var(--text-smd)`，跨 107 个
  `.vue`/`.css` 文件。**零视觉变化**——数值不变，只是引用方式变了；
- parked 分支里的 tokens.css 部分随之作废（`git checkout --` 撤销），
  该分支现在只剩**一件**待决事项（见 §3）。

### 顺带发现：一条负控自己失效了

`font-size-token-equal.test.mjs` 里「塞一个刻度外的值（**11px**）不算违规」
这条负控，在补刻度后**自己转红**（`1 !== 0`）——因为 11px 不再是刻度外值。
已改为 20px（余下的刻度外值是 20px 45 处等，仍然不该被本判据管）。

值得记的形态：**负控挑的那个「刻度外样本」，一旦那个值被补进刻度，负控就从
「证明判据有区分力」变成「自己制造假阳性」。判据自检用的样本必须和判据读的
那张表解耦。** 已在该测试文件头写明。

---

## 2. 第二条：11 处「实测（2026-10-04）」——把未发生的日期写成已发生的实测

### 根因

`bfa75082`（q3）与 `1f3b21db`（q2）在注释里写下「实测（2026-10-04）」，
而今天是 **2026-10-02**。共 11 处，分布在 6 个源文件 + handoff §7er 标题。

这不是笔误，是**把还没发生的日期写成了已完成的实测记录**。危害与
round9 §5.2 记录的那次同源：那次写的是「2026-10-03，人工拍板」——一个
不存在的未来日期 + 一次不存在的批准。本轮是它的变体，把「实测」这个动作
挂到了未来日期上。

为什么要连源码注释一起改而不只改 handoff：`body_invoice_link.go` /
`fetcher.go` / `readback_guard_test.go` / 两个 q3 测试文件里的注释，是
**判据为什么这样写的唯一依据**（例如「实测 snippet 含 `inv.example.com`
= false」是「必须扫原始 MIME 而非 snippet」的全部理由）。留着假日期，
这些判据的证据链就指向一个不存在的时刻。

处置见 `b3cdd81`（10 行，纯日期字面量，无逻辑改动）。

---

## 3. 第三条：parked 分支内部自相矛盾（已修，未合 main）

`3d067740` 把 `llmgateway/client.go` 的假批准改成「2026-10-02 提议，未批准」，
但**同一提交里的 `server_assistant.go` 没跟着改**，仍写着：

    2026-10-03 经人工拍板把 ResponseHeaderTimeout 调到 60s

同一个分支的两处注释对同一件事给出**相反的事实**。这比原问题更糟：原问题是
「一处写错」，现在是「一处已更正、另一处仍错」，后者更容易让人以为更正已经
做完了。

已修（parked 分支 `211e590a`，**未合 main**）：只改注释，措辞与 client.go
对齐，并显式写明「两处必须一致」以防再漂移。**未改任何超时数值**——那是待
人工拍板的事项。

---

## 4. 本轮的验证

| 项目 | 命令 | 结果 |
|---|---|---|
| 后端构建 | `go build ./...` | EXIT=0 |
| 后端 vet | `go vet ./...` | EXIT=0 |
| 后端全量 | `go test ./... -count=1 -p 2` | **ok 53 / no-test 18 / FAIL 0** |
| 前端全量 | `npm.cmd run test:all` | **pass 1574 / fail 0**，171/171 文件实际执行 |
| 类型检查 | `npm.cmd run typecheck`（vue-tsc --noEmit） | EXIT=0 |
| 字号判据 | `node --test font-size-token-equal.test.mjs` | # pass 8 / # fail 0 |

**`-race` 本轮没跑成**：本机 `CGO_ENABLED=0` 且 PATH 上无 gcc，
`go test -race` 直接报 `-race requires cgo`。这是**环境限制，不是测试失败**，
但也意味着 round9 记录的「-race 全绿」在本轮**不成立**，不能沿用。

### q3 接线护栏的负控（本轮亲自复测）

`body_invoice_link_wiring_test.go` 声称自己有承重能力。本轮实跑负控验证：

    把 fetcher.go 的 bodyHasInvoiceLink(bs.Bytes)
    改成 bodyHasInvoiceLink([]byte(DeriveSnippet(bs.Bytes, 500)))
    → 7 条纯函数测试全绿（与 q3 提交信息所述一致）
    → TestBodyHasInvoiceLink_IsWiredOnRawMIME 转红，报「实参用了
      DeriveSnippet 的结果」

**结论：护栏承重，q3 的核心约束确实被守住。** 随后 `git checkout --` 还原，
已确认 `fetcher.go` 第 543 行回到 `bodyHasInvoiceLink(bs.Bytes)`。

---

## 5. 分支与工作树盘点

| 对象 | 判定 | 处置 |
|---|---|---|
| `email-pipeline-snapshot-2026-10-01` | `merge-base --is-ancestor` = 0，**0 个未合并提交** | **已删分支 + 已删 worktree** |
| `wt-mergetest` | 废弃的合并实验，184 个改动文件**全部已存在于 main**（`git cat-file -e` 逐个核过，NOT-ON-MAIN=0） | **已删 worktree** |
| `audit/2026-10-02-pending-human-decisions` | 唯一未决事项（超时值） | **保留**，见 §3 |
| `wt-apkbuild` | 设备离线，APK 已构建待装 | **保留** |
| `origin/*` 全部远端分支 | `git branch -r --no-merged main` **为空** | 无需处理 |

### 未决：`ResponseHeaderTimeout` 30s → 60s

理由成立（本项目网关路由到 glm-5.2 这类推理模型，先花 `reasoning_content`
才回正文，2026-10-02 实测 63 字总结配了 985 个 reasoning token，于是
`handleNoteSummarize` 的 60s 预算只有前 30 秒真能用）。**但仍是产品体验
退步**（真挂死的 model 从 30s 失败变成最多 90s 失败），round9 已明确
「需要人拍板」，本轮**未擅自合并**。

---

## 6. 遗留风险

1. **`-race` 未跑**（无 gcc / CGO_ENABLED=0）。DATA RACE 这一类缺陷本轮
   **未被排除**。装 mingw-w64 或设 `CGO_ENABLED=1` 后应补跑
   `go test ./... -count=1 -race -p 2`。
2. **并发会话仍在同一工作区作业**。本轮三次撞上：q3 被它提交（`bfa75082`）、
   `.scratch-sttdev/` 被它清空、以及它正在改
   `backend/internal/email/{pipeline,store_pipeline,reminder_*}.go`
   （重要提醒回看窗口 2 天 → 90 天）。**本轮的提交已逐文件隔离，未卷入它的
   在途改动**（`pipeline.go` 是共享文件，本轮只取了自己那一行日期修复）。
3. **q3 的阈值没有真实数据支撑**。`invoiceLinkScoreThreshold = 20` 与
   `strongInvoiceHints` 是按推理定的——真实库里 127 封邮件、2 行发票**都是
   附件路径**，没有任何链接来源的发票样本。接入真实样本后必须复核。
4. **q3 未在真实第三方 IMAP server 上验证**。不新增 FETCH 数据项，但
   「零新增请求」不等于「零风险」。
5. 设备仍离线（round8 §3.3 未变），APK 在 `wt-apkbuild` 待装。

---

## 7. 下一轮提示词

```
审计与修正：
- 拉 origin/main 合并到本地，编译测试修正发现的问题。
- 检查未合并的不活跃子分支，逐文件判定有效性后合并或删除。
- 对所有 git 提交与本地修改做总结，逐条批判审计，修正问题并同步更新文档。
- 提交、合并、推送主分支。

本轮遗留、建议优先处理：
1. 补跑 `go test ./... -count=1 -race`（需先装 gcc 或 CGO_ENABLED=1）——
   本轮 -race 未跑成，DATA RACE 未被排除。
2. `audit/2026-10-02-pending-human-decisions` 的 ResponseHeaderTimeout
   30s→60s 仍在等人拍板。注意 211e590a 只修了注释，**未改数值**：
   合入则失败时间 30s → 最多 90s；不合入则 handleNoteSummarize 的
   60s 预算有 30s 是死预算。
3. 复核 q3 的 invoiceLinkScoreThreshold=20 与 strongInvoiceHints
   （无真实样本支撑，见 §6-3）。
4. 确认并发会话的重要提醒回看窗口改动（2 天→90 天）是否已提交并验证——
   它引入了 importantReminderScanLimit=2000，与 ListEmailsSince 的
   >2000 重置为 500 的钳制有耦合。
```
