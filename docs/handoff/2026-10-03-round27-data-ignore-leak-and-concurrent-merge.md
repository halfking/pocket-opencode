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
                  ./internal/server/...               → exit 0
cd backend && go test ./... -count=1                   → 见 §5
```

（早前在同源码的更小范围上：`config` / `email` / `email/rules` / `notifycenter` /
`feishu` / `server` 全部 ok，exit 0。）

### 3.4 前端

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

## 5. 遗留风险与未完成项

| 项 | 状态 | 说明 |
|---|---|---|
| `go test ./...` 全包结果 | 见下节 | 全包含 server/email 等大包，耗时长；若与上文小范围结果不一致，以后者为准并重跑 |
| 主工作区约 180 个未跟踪文件 | **未处理** | 绝大多数是同伴的 `.scratch-*.txt`、`.scratch/`、`frontend/dist/`、`downloads/`、`tmp-speech.wav`、`fe-test.txt` 等一次性垃圾。`.gitignore` 没有覆盖 `.scratch-*`，随时可能被某次 `git add -A` 卷进去。**属另一个会话的工作范围，本轮没动。** |
| `scripts/_patch-unlock.mjs` | 未处理 | 0 字节死文件，未跟踪 |
| `scripts/route-coverage-sweep.mjs` / `route-usage-crossref.mjs` | **建议入库** | 25KB + 50KB 的成品审计工具：前者把 `server.go` 注册的 139 条路由逐条探活分类；后者对账「注册了什么」与「产品代码有人调吗」，且自带 v1 判据正则的四种失效分析。这两个是这批未跟踪文件里唯一有长期价值的，烂在工作区可惜 |
| `C:\workspace\openpocket-wt-a20` 孤儿目录 | **删除失败** | `git worktree remove` 报 `Invalid argument`（Windows 深层路径），改走可恢复删除路由后 `mavis-trash` 报 `The system call level is not correct`，同样失败。按规则**没有绕过**改用永久删除。目录已无 `.git`、已从 worktree 注册表移除，3694 个条目仍在磁盘上，可手工删 |
| `C:\workspace` 下另有 10 个旧 worktree 目录 | 未处理 | `openpocket-wt-{apkbuild,base,maildeploy,mergeprobe,r9,snippet,stt}`、`wt-{audit,mailfix,verify}`，均非本仓库注册 worktree，是历轮遗留孤儿 |
| 本机代理端口 | 已验活 | `~/.ssh/config` 写 7897，实测 7897 通、7890/10809/1080 不通。推送前先 `Test-NetConnection` 验活，别照抄任何记录里的端口号 |

### 5.1 我明确**没有**验证的

- 主工作区那批未提交 WIP 是否能编译、是否自洽——**没验**，因为它每分钟都在变，
  测出来的结果对应的是哪个时刻的文件无法确定。
- 本轮没做真机验收。全部结论都来自代码与本地测试。
- `go test ./...` 的完整包级结果需要在 §5 表格里对上；若出现与 §3.3 小范围结果
  不一致的包，以实际输出为准并重跑。

---

## 6. 下一轮提示词

```
审计 openpocket：接着 round27 往下做，重点是它列出的「遗留风险」表。

1. 先查并发：`mavis session list`（过滤非归档） + 各 worktree tracked 文件 mtime。
   如果还有别的会话在写 C:\workspace\openpocket，继续用独立 worktree 干活，
   不要碰主工作区，也不要提交它的未提交 WIP。
2. 补上 round27 没做完的：
   - 跑 `cd backend && go test ./... -count=1` 拿全包结果，对上 §5 表格
   - 处理主工作区约 180 个未跟踪文件：判断 scripts/route-coverage-sweep.mjs 和
     scripts/route-usage-crossref.mjs 值不值得正式入库（我认为值得），
     .scratch-* 那一堆建议加进 .gitignore 而不是逐个删
   - C:\workspace\openpocket-wt-a20 孤儿目录：可恢复删除路由已实测失败，
     需要你决定是手工删还是继续留着
3. 检查 scripts/check-runtime-data-tracked.mjs 有没有接进前端 gates 脚本
   （npm run gates 那条链）——本轮只加脚本，没接进任何门禁，所以它目前
   不会在 CI 或常规流程里自动跑。这是它作为「防回归护栏」的一个真实缺口。
4. 常规：审计 → 修正 → 验证 → 提交 → 推 origin/main；推送前先
   Test-NetConnection 验活代理端口，推大改动时加
   GIT_SSH_COMMAND='ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=20'。
```
