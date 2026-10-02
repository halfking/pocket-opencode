# round27 — data/ 忽略漏洞、并发会话实况、以及一次「三个判据互相矛盾」的裁决

日期：2026-10-03 01:00–01:50
执行：mavis cron 目标「审计与完善」（工作区 `C:\workspace\openpocket`）

---

## 1. 结论与根因

### 1.1 修掉的漏洞：`.gitignore` 声称的意图与实现不符，导致真实邮件正文可被 `git add -A` 卷进仓库

`data/email-bodies-raw/` 是 `data/` 下**唯一**没有被忽略的路径。后果不是理论上的：
`git add -A` 的 dry-run 实测会带出 **45 个真实邮件正文**（`em-pop3-acct-*.bin`，POP3
抓下来的原始 MIME 缓存）。

根因是**意图与实现脱钩**，不是漏写一条规则：

- `.gitignore` 第 76-81 行早在 2026-10-01 就写明了意图——「data/ 下装的是每个用户
  自己的内容，不是源码，所以**整个目录忽略**，而不是只忽略主密钥」。
- 但实现是**逐个列出已知子目录**（`data/email-bodies/`、`data/email-invoices/`、
  `data/*.sqlite`…）。注释描述的是「整目录」，代码写的是「枚举」。
- 于是每多一个子目录就再漏一次。`email-bodies-raw/`（`body_cache.go:16` 的落盘格式，
  8B 大端 UID + base64 密文）就一直没被覆盖。

**密钥没有泄露。** `data/email_master.key` 有一条独立规则一直拦得住，实测
`git add --dry-run data/email_master.key` 被 git 拒绝（`The following paths are ignored`），
`git add -A` 的 45 条里也没有它。漏的只是正文，不是密钥。

### 1.2 关键判断：为什么查「有没有被跟踪」而不是「.gitignore 怎么写」

两者都不成立，`.gitignore` 的写法更弱：

- `.gitignore` 只挡得住 `git add -A`。`git add -f` 照进。
- 别人改规则就失效——本轮就发生过：两个并发会话各自往相邻区域插规则，直接冲突。
- 历史上已经入库的文件，`.gitignore` 完全管不着。

「被跟踪」是事实，「忽略规则」只是意图。所以卡口 `scripts/check-runtime-data-tracked.mjs`
查前者。

---

## 2. 改动文件与关键行为

| 文件 | 改动 | 关键行为 |
|---|---|---|
| `.gitignore` | 加 `data/` 整目录规则；显式补 `data/email-bodies-raw/`、`backend/data/email-bodies-raw/`、`backend/data/email-bodies/`；合并两侧注释 | 意图与实现对齐，今后新增子目录不再需要改这里 |
| `scripts/check-runtime-data-tracked.mjs` | 新增（91 行） | 查 `git ls-files` 在 `data/` 与 `backend/data/` 下有无跟踪项；有则 exit 1 并逐条点名 |
| `docs/handoff/2026-10-03-round27-*.md` | 新增（本文件） | — |

### 2.1 卡口的几个刻意选择

- **ROOT 从脚本自身位置推导，不硬编码绝对路径。** 仓库有 6e89480a
  「闪卡判据一直在判**另一棵源码树**（硬编码 wt3 路径）」的前科；`check-main-overlap.mjs`
  现在也还硬编码着 `C:/workspace/openpocket`。硬编码的卡口在别的 worktree 里会静默给出
  与本仓库无关的绿灯。
- **查跟踪状态，不查忽略规则。** 见 §1.2。
- **带 `--selftest`。** 判据本身也是软件，也需要自己的负控。12 条用例里**故意放了 6 条
  不该命中的正常路径**（`backend/internal/email/pipeline.go`、`frontend/src/api/email.ts`、
  `scripts/check-runtime-data-tracked.mjs`、`docs/handoff/…`、`schema.sql`、`adr/metadata.md`），
  防止规则过宽变成天天误报、最后没人看的假守门。
- **错误处理里区分「子进程非 0 退出」和「我自己炸了」。** `e.status` 是数字才归类为
  判据失败；`undefined` 说明是我自己的代码抛异常，立刻暴露而不是伪装成「没发现问题」。

### 2.2 一个实测结论（写进注释以免将来重推）

`.gitignore` 里**无前导斜杠、带尾斜杠**的 `data/` 匹配的是**任意层级**下名为 `data`
的目录。实测：造 `backend/data/email-bodies-raw/probe.bin` 后
`git check-ignore -v` 报出的是 `data/` 那一行，不是任何 `backend/data/...` 规则。

所以 `data/` 这条本身就覆盖了 `backend/data/`。显式写那两行是**冗余的**，写出来是为了
在有人把 `data/` 收窄回逐个列举时，这就是那次的真实原因。

---

## 3. 测试命令与结果

全部在**将要推送的那个合并态**上跑的（不是推之前那个旧绿灯）。

### 3.1 卡口自身

```
node scripts/check-runtime-data-tracked.mjs --selftest
  → selftest: 12/12 通过, exit 0

node scripts/check-runtime-data-tracked.mjs
  → ✓ data/ 与 backend/data/ 下没有任何被跟踪的文件, exit 0
```

### 3.2 负控（判据的判别力，不是「看起来对」）

驱动脚本放 TEMP 而不是仓库里——自我验证的工具不能把产物写进自己所在的位置。

| 步骤 | 期望 | 实测 |
|---|---|---|
| 造 `data/_negctl/probe.bin` | `git add -A --dry-run` 带出 0 条 | 0 条 ✅ |
| 造 `backend/data/email-bodies-raw/probe.bin` | 被忽略 | `check-ignore` exit 0，命中 `data/` ✅ |
| `git add -f` 强行入库 | 判据 exit=1 且点名该文件 | exit=1，点名 `_negctl/probe.bin` ✅ |
| `git reset` 还原 | 判据回绿，索引无残留 | exit=0，索引 0 条 ✅ |

### 3.3 后端

```
cd backend && go build ./...                          → exit 0
cd backend && go vet ./internal/config/... \
                  ./internal/email/... \
                  ./internal/notifycenter/... \
                  ./internal/server/... \
                  ./internal/repohygiene/...          → exit 0
cd backend && go test ./... -count=1                   → exit 0，FAIL/--- 过滤输出为空
```

**注意：上面这条 `go test ./...` 第一次跑是红的**，唯一失败的是
`internal/repohygiene` 的 `TestNoCommittedSecrets`（见 §5.2）。修完后重跑全绿。

### 3.4 一个我造成又改掉的多余改动（记下来）

用 `readFileSync`/`writeFileSync` 批量给 handoff 表格加标记时，我的正则带了
`\s*$`，它把**表格最后一行与后面引用块之间的那个空行**一起吃掉了。
`git show --numstat` 报出 `3 4` 而不是 `3 3`，暴露了它——我最初的断言
（文件体积没异常缩水、行数没变）都没覆盖到这种「少一个空行」的情况。

已用第二个脚本按「最后一个带标记的行 + 其后紧跟的非空行」这个形状精确定位并补回，
`--amend` 后该提交对父提交是干净的 `3 3`，`go test ./internal/repohygiene/...` 仍绿。

教训：**体积断言只挡得住大改动，挡不住「刚好少一行」。** 批量改别人的文件时，
`--numstat` 的增删比是最便宜的自证手段——增删不对称就该立刻去看 diff。

### 3.5 前端

```
cd frontend && node node_modules/vue-tsc/bin/vue-tsc.js --noEmit
  → exit 0

cd frontend && node scripts/run-mjs-tests.mjs
  → ℹ tests 1720 · ℹ pass 1720 · ℹ fail 0 · ✅ 187/187 个测试文件全部被实际执行
```

注意：`npx vue-tsc` 会被本机执行策略拦掉（`npx.ps1` 禁止运行脚本），必须走
`node node_modules/vue-tsc/bin/vue-tsc.js`。

---

## 4. 本轮的并发实况（下一轮必读）

### 4.1 开工时发现 3 个非归档会话正在**同一个主工作区**里实时写入

`mavis session list` + 逐 worktree mtime 扫描 + 读对等会话的最近消息，三方对上：

- `mvs_b9297fbb…`「Maestro 真机全项目测试与功能修复」——正在跑负控，**把生产文件
  `backend/internal/feishu/handler.go` 改写成 `else if false` 再还原**。
- `mvs_51967289…`「openpocket 下一轮修正与 worktree 清理」——00:47 刚提交 `e37b73e2`。
- `mvs_8a0f6bf8…`「完善邮件定时收取与发票处理需求」——正在提交发票 wire-keys 一批。

**因此全程没有碰主工作区。** 所有合并、测试、提交都在隔离 worktree
`C:\workspace\openpocket-wt-r26`（分支 `audit/round26`）里做，主工作区的 8~180 项
未提交 WIP 一律没动、没提交、没推送。

判断并发不能只看 `git status`：status 稳定只说明**树没变**，不说明没人在写。要看
worktree 里 tracked 文件的 mtime。

### 4.2 同一漏洞被两个会话独立发现，合并时冲突——取并集

`.gitignore` 冲突（`UU`），两侧改的是同一件事。处理原则是**保留两侧增量，不做单侧取舍**：

- 我这边：整目录 `data/`（两侧都缺的那一半）。
- 另一侧：实测证据块——49 条 `?? data/email-bodies-raw/…`、两个落盘目录的磁盘格式差异、
  `POCKET_DATA_DIR` 可配到 `backend/data` 的理由。这些我这边没有。
- 补 `backend/data/email-bodies/`：两侧都只写了 `-raw` 那条。

数量不一致不是矛盾：那侧数 49 条 `-uall`，我这侧数 45 条 `add -A` dry-run，统计时刻
的抓取批次不同，指向同一批文件。**合并的是同一次发现，不是两次不同的问题。**

---

## 5. 第二个发现：`TestNoCommittedSecrets` 在 origin/main 上就已经是红的

`go test ./...` 全包跑下来唯一失败项，命中 6 行：

```
docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md:9449-9451
docs/handoff/_part-4.87.md:81-83
```

### 5.1 先做对照，再定性

- `git log origin/main..audit/round26 -- <这两个文件>` **为空** ⇒ 本轮提交没碰过它们
- `git show origin/main:<文件> | grep -c SomeRe` 两个文件**各 3 行** ⇒ 命中行在
  origin/main 上就已存在，由 `04bfc5e8` / `5b123d07` 引入

⇒ **是既有失败，不是我引入的回归。** 这一步不能省：把别人的锅算到自己头上，
和把自己的锅推给别人一样，都比查清楚更糟。

### 5.2 定性：合成夹具，不是真密钥

那 6 行是 markdown 表格，右列写的是「命中」——它们本身就是 `password-literal`
这条规则的文档化演示（讲清楚哪些写法会被这条正则拦下），值是 `SomeRealPassword123`
之类的占位串。与 `secrets_test.go` 自身被整文件豁免属同一类情形。

### 5.3 修法：逐行豁免，不做整文件豁免

扫描器自己偏好的档位是「同一行出现 `secret-scan-ok` 即放行」，且明确规定
**不允许目录级或全局白名单**。这里没走整文件豁免，因为这两个 handoff 正被并发
会话持续追加（§4.94 当时正在写），整文件放行意味着将来谁往里面贴一个**真** key
也会被一并放过。

标记写成行尾 HTML 注释 `<!-- secret-scan-ok：… -->`：markdown 渲染不可见，但原始行
里含标记，判据的 `strings.Contains(line, exemptionMarker)` 能命中。
**没有为了迁就夹具去放宽 `secrets_test.go` 里的判据。**

改完 `go test ./internal/repohygiene/... -count=1` → ok, exit 0。

### 5.4 抄判据时踩的坑

写补标脚本时我用 `/\b(?:pass|pwd)\b/` 去找目标行——那要求 `pass`/`pwd` 是独立词，
于是 `adminPass`、`devPass` 都不匹配，每文件只改到 1 行。是**行数断言**
（预期 6、实际 2）抓住的，不是肉眼。扫描器自己那条规则的形状是
`(?i)\b[A-Za-z_]*(?:pass|pwd)[A-Za-z_]*\b`——标识符**含** pass/pwd 且大小写不敏感。

抄一条判据时要把它的**匹配形状**一并抄准，不要凭印象重写。

---

## 6. 遗留风险与未完成项

| 项 | 状态 | 说明 |
|---|---|---|
| `verify/e2e-20261002-v2` + `.wt-e2e` | **本轮按用户决定保留** | 分支已 100% 合入 origin/main，但 worktree 正被活跃会话实时写入：11 个 tracked 脚本有未提交改动（+107/-18）、2 个新脚本、handoff §4.94、5 个 `.tmp-*.txt`，无 commit 无 stash 兜底。**删了就是真丢**，等那个会话收尾后再处理 |
| `C:\workspace\openpocket-wt-r26` + `audit/round26` | **保留** | 已 100% 推入 origin/main，代码不会丢。留着是因为它的 `frontend/node_modules` 是指向主工作区的**目录联接**，而主工作区正被同伴使用；对联接执行 `git worktree remove` 有误删目标的风险。下一轮要清它时，**先单独删联接，再删 worktree** |
| 主工作区约 180 个未跟踪文件 | 未处理 | 绝大多数是同伴的 `.scratch-*.txt`、`.scratch/`、`frontend/dist/`、`downloads/`、`tmp-speech.wav`、`fe-test.txt` 等一次性垃圾。`.gitignore` 没覆盖 `.scratch-*`，随时可能被某次 `git add -A` 卷进去。属另一个会话的工作范围 |
| `scripts/_patch-unlock.mjs` | 未处理 | 0 字节死文件，未跟踪 |
| `scripts/route-coverage-sweep.mjs` / `route-usage-crossref.mjs` | **建议入库** | 25KB + 50KB 的成品审计工具：前者把 `server.go` 注册的 139 条路由逐条探活分类；后者对账「注册了什么」与「产品代码有人调吗」，且自带 v1 判据正则的四种失效分析。这批未跟踪文件里唯一有长期价值的 |
| `scripts/check-runtime-data-tracked.mjs` 没接进门禁 | **真实缺口** | 本轮只加了脚本，**没接进 `npm run gates` 任何一环**。所以它不会在常规流程或 CI 里自动跑，作为「防回归护栏」目前是半成品。下一轮优先补 |
| `C:\workspace\openpocket-wt-a20` 孤儿目录 | **删除失败** | `git worktree remove` 报 `Invalid argument`（Windows 深层路径），改走可恢复删除路由后 `mavis-trash` 报 `The system call level is not correct`，同样失败。按规则**没有绕过**改用永久删除。目录已无 `.git`、已从注册表移除，3694 个条目仍在磁盘上，可手工删 |
| `C:\workspace` 下另有 10 个旧 worktree 目录 | 未处理 | `openpocket-wt-{apkbuild,base,maildeploy,mergeprobe,r9,snippet,stt}`、`wt-{audit,mailfix,verify}`，均非本仓库注册 worktree，是历轮遗留孤儿 |
| 本机代理端口 | 已验活 | `~/.ssh/config` 写 7897，实测 7897 通、7890/10809/1080 不通 |

### 6.1 我明确**没有**验证的

- 主工作区那批未提交 WIP 是否能编译、是否自洽——**没验**，它每分钟都在变。
- `.wt-e2e` 里那 20 项在制品能否编译——**没验**，同理由。
- 本轮**没有做真机验收**。全部结论来自代码与本地测试。

---

## 7. 本轮推送

`origin/main` → `0fbba2e6`（`git ls-remote origin refs/heads/main` 核实过，不只看 push 的回显）。

本轮自己的提交（从新到旧）：

| 提交 | 内容 |
|---|---|
| `0fbba2e6` | docs(handoff)：本文件 |
| `f66210a7` | fix(repohygiene)：6 行合成示例逐行豁免，`TestNoCommittedSecrets` 转绿 |
| `69837eaa` | merge(main)：`.gitignore` 冲突取并集（两侧是同一次发现） |
| `84e320d0` | Merge origin/main into audit/round26 |
| `1b4499f1` | fix(gitignore)：并入并发会话的发现（两个落盘目录 + backend/data 侧） |
| `3adb04ba` | fix(gitignore)：`data/` 整目录忽略 + 新增卡口 |

分支清理：`audit/round20`、`mvs/email-fixes-20261002` 已删（均 100% 合入，用
`git branch -d` 让 git 自己把关，没用 `-D`）。

---

## 8. 下一轮提示词

```
审计 openpocket：接着 round27 往下做，重点是它 §6「遗留风险」表里标着
「未处理 / 真实缺口」的那几项。先读那个文件，别重复本轮已做完的事。

优先级从高到低：

1. 把 scripts/check-runtime-data-tracked.mjs 接进 npm run gates。
   本轮只加了脚本没接门禁，所以它不会自动跑——作为防回归护栏目前是半成品。
   门禁跑在 frontend/ 下，注意 runner 真路径是 frontend/scripts/run-mjs-tests.mjs，
   不是仓库根的 scripts/run-mjs-tests.mjs（那个文件不存在）。
   接完要跑一次负控：临时 git add -f 一个 data/ 下的文件，确认 gates 真的会红。

2. 处理 .wt-e2e（verify/e2e-20261002-v2）。本轮按用户决定保留，理由是当时
   有 20 项在制品没提交、且会话还在实时写。**动手前先重新查它的状态**：
   git -C .wt-e2e status --porcelain + tracked 文件 mtime。若已收尾提交，
   删 worktree 再 git branch -d；`git worktree remove` 在本机可能报
   Invalid argument（深层路径），那种情况下报告并交回用户决定，不要自行绕。

3. 处理主工作区约 180 个未跟踪文件：判断 scripts/route-coverage-sweep.mjs 和
   scripts/route-usage-crossref.mjs 值不值得正式入库（本轮认为值得，
   它们是这批里唯一有长期价值的成品工具），.scratch-* 那一堆建议加进
   .gitignore 而不是逐个删。

4. 收尾 worktree 清理：C:\workspace\openpocket-wt-a20 孤儿目录（可恢复删除
   路由已实测失败，需用户决定），以及 C:\workspace 下另有 10 个历轮遗留孤儿目录。
   清 openpocket-wt-r26 时**先删 frontend/node_modules 目录联接再删 worktree**，
   它是指向主工作区的联接，直接删 worktree 有误删目标的风险。

5. 常规：审计 → 修正 → 验证 → 提交 → 推 origin/main。
   推送前先 Test-NetConnection 127.0.0.1 -p 7897 验活代理
   （端口会漂，别照抄任何记录里的端口号），推大改动时加
   GIT_SSH_COMMAND='ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=20'。

开工第一步永远是查并发：mavis session list 过滤非归档 + 各 worktree tracked
文件 mtime。这轮开工时发现 3 个会话在同一个主工作区实时写入，其中一个正把
backend/internal/feishu/handler.go 改写成负控状态。status 稳定只说明树没变，
不代表没人在写。发现有并发就用独立 worktree 干活。
```
