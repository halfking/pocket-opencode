# 2026-10-02（第二轮）P0 凭据实测 + dev 旁路硬编码口令 + 卡口补盲区

> 上一轮：`2026-10-02-branch-consolidation-and-secret-gate.md`。
> 本轮从那份 handoff 的「下一轮提示词」接手，**第 1 项（P0 确认）做完的结论是
> 「两把 key 都没轮换」，而且顺着它挖出一个比它更严重的东西。**

---

## 0. 一句话结论

1. **P0 未解除**：网关凭据 `sk-6tGL…` 实测**仍能真实调用 LLM 并计费**
   （错误 key 对照 401），MCP 凭据 `sk-mcp-…` 端点 502 无法判定但未证明失效。
2. **新发现（比 P0 更严重）**：生产代码 `server_assistant.go` 里硬编码了一个
   **admin 旁路口令**，明文出现在 8 个受跟踪文件。上一轮 336c883 修掉了
   bootstrap 建号路径的同类问题，**dev 旁路是残留入口**——这正是
   「修了一处就以为这类闭合了」的又一次复发。已修复 + 清理 + 上护栏。
3. 卡口补了一条 `password-literal` 规则，堵住「口令比 API key 短」这个整类盲区。
4. 收尾：`pickAPIKey` 死函数已删；`fix/email-cache-backfill` + `wt-mailfix` 已清理。
5. 分支普查：**10 个远端分支已完全并入 main**（逐文件法验证），2 个是活跃在制品。

---

## 1. P0 确认：两把 key 都**没有**轮换

### 1.1 网关凭据 `sk-6tGL…` —— 实测仍有效，可计费

从 git 历史取回（`git log --all -S 'sk-6tGL'`，51 位），实测：

| 探测 | 结果 |
|---|---|
| `GET /v1/models` + 泄漏 key | HTTP 200，52793 字节 |
| `GET /v1/models` + **错误 key 对照** | HTTP 200，52793 字节 ← **端点不鉴权** |
| `POST /v1/chat/completions` + 泄漏 key | HTTP 200，真实返回 `"content":"P"` |
| `POST /v1/chat/completions` + **错误 key 对照** | **HTTP 401** |

> ⚠ **第一版探测差点给出错误结论。** 只测 `/v1/models` 时，泄漏 key 和随手编的
> `sk-invalidcontrol-…` 返回**完全相同的状态码和字节数**——那个端点根本不鉴权。
> 如果没有对照组，我会写下「key 仍有效」并把它当成证据，而它其实什么都没证明。
> 换成 `/v1/chat/completions` 后对照组才 401，结论才立得住。
>
> 这是「用局部证据支撑全局结论」的又一次复发，只是这次发生在**我自己**的探测上。

### 1.2 MCP 凭据 `sk-mcp-…` —— 无法判定，按「未证明失效」处理

| 端点 | 泄漏 key | 错误 key 对照 |
|---|---|---|
| `https://mcp.kxpms.cn/acc/mcp` | HTTP 200，返回**前端 HTML** | 同样 200 + 同样 HTML |
| `https://acc.kxpms.cn/mcp`（文档指明的正确端点） | **HTTP 502** | 同样 502 |

第一个 URL 显然不是 MCP 端点（返回的是站点首页）；文档
`docs/archive/2026-07/PLAN_REAL_TASKS.md:15` 明确写了正确端点是后者，但两者
都测不出鉴权行为。**所以这把 key 的存活性本轮无法证明**——按「未证明失效」
处理，与网关那把同等对待。

### 1.3 需要你在服务侧做的事（我做不了）

1. **吊销并轮换网关 key** `sk-6tGL…`（`llm.kxpms.cn` / `llmgo.kxpms.cn`）。
2. **吊销并轮换 MCP key**（ACC，Key ID 17 / `opencode-pocket-mcp`，原到期 2027-06-29）。
3. **轮换 184 的 root SSH 口令**（本轮新发现，见 §2.3）。
4. 新 key 只走环境变量：`POCKET_LLM_GATEWAY_API_KEY` / `POCKET_MCP_API_KEY`。
5. 是否改写 git 历史我**没有做，也不建议现在做**（仍有 6 个并发 worktree 活动，
   属破坏性操作需显式授权）。

---

## 2. 本轮真正的发现：dev 旁路的硬编码 admin 口令

### 2.1 怎么找到的

不是又一次人手 grep。是把上一轮收进来的
`scripts/recover-opencode-pocket-schema.ps1` 取到主 worktree 之后，
**顺手让卡口扫一遍**——卡口绿了，但我读那个脚本时眼睛先看到了第 308 行。
绿灯和事实不一致，说明卡口有盲区，于是去量盲区有多大。

### 2.2 内容与影响面

`backend/internal/server/server_assistant.go` 的 `devBypassCredentials`：

```go
devPass := s.cfg.DevAuthPass
if devPass == "" {
    devPass = "Veritrans&9527"   // ← 写死在生产代码里
    log.Printf("WARN: ... using built-in dev default password")
}
```

这把口令明文出现在 **8 个受跟踪文件**（Go / sh / ps1 / mjs / py / ts）：

| 文件 | 形态 |
|---|---|
| `backend/internal/server/server_assistant.go:214` | **生产代码**，dev 旁路内置缺省 |
| `backend/start-dev.sh:35` | `${POCKET_AUTH_PASS:-…}` 缺省 |
| `scripts/verify-stt.ps1` (×2) | env + 登录体 |
| `scripts/verify-stt-stream.ps1` (×2) | env + 登录体 |
| `scripts/verify-https-prod.mjs:89` | 登录体 |
| `e2e/web/helpers/auth.ts` / `session.ts` | E2E 缺省 |
| `e2e/android/local-agent-cdp.py:170` | **凭据猜测列表**，还带第二个疑似口令串 |

**为什么严重**：`config.go:364` 只在生产模式校验 `POCKET_DEV_AUTH=false`。
一旦某台实例开着 dev 模式，这把口令就是**公开的 admin 旁路**——
拿到仓库的任何人都能登进去。它不像 API key 那样需要付费才会被注意到。

**与上一轮修复的关系**：336c883 已经把 bootstrap 建号路径的同类问题修掉
（拒绝用内置口令建号）。dev 旁路是**同一个问题的另一个入口**，上一轮没看见。

### 2.3 顺带挖出第三把凭据

`docs/archive/2026-07/DEPLOY_AFTER_184_RESET.md:10` 里有 **184 生产机的 root
SSH 明文口令**（`export SSHPASS='…'`，18 字符）。已打码为 `<184-root-password>`
并加警告。**这把同样需要轮换**——它比两把 API key 权限更高。

### 2.4 处置

- `server_assistant.go`：`devPass == ""` 时**直接拒绝旁路**并告警，不再有任何内置缺省。
- `config.go:85`：字段注释改为「**无缺省值**」。
- 8 个脚本/测试文件：全部改为从环境变量读；缺变量时**如实中止/跳过**，
  而不是拿一把公开口令换一次绿灯。
- `local-agent-cdp.py`：候选口令改从 `E2E_PASSWORD`/`E2E_PASSWORDS` 读，
  并删掉「打印命中口令前 6 字符」那行输出。

---

## 3. 卡口补盲区：`password-literal`

### 3.1 为什么原来漏

`credential-assignment` 要求值是 **≥24 个 base64 字符**。口令通常远短于此
（`Veritrans&9527` 只有 13 字符，还带 `&`），于是**整类漏网**。

### 3.2 新规则

判据：变量名含 `pass`/`pwd` + 引号包裹的**字面量**（排除 `$` 变量插值）+ 长度 ≥ 8，
再经一道强度粗筛（必须**同时含字母与数字**）压噪声。

**为什么不放宽旧规则**：24 字符下限是它不误报一堆测试夹具的关键，放宽会一次
炸出几十条噪声。两条规则各管一段形态，比互相妥协好。

**强度闸门是被负控证明有效的**：把它短路成 `if false` 之后，测试立刻炸出
11+ 条命中（`fix-locale-parity.mjs` 的 i18n 文案、若干测试夹具）——
说明这个闸门真的在挡噪声，不是摆设。

### 3.3 爆炸半径

加规则前先量过：宽口径 52 处 → 收窄后 11 处 → 清理后新增命中 1 处
（就是那把 SSH 口令）。逐条看过才落规则，没有直接拿宽口径赌。

---

## 4. 护栏与负控

### 4.1 新增 `dev_bypass_no_builtin_password_test.go`（3 条）

| 测试 | 盯什么 |
|---|---|
| `TestDevBypassHasNoBuiltinDefaultPassword` | 空配置时旁路必须拒绝（含历史口令值） |
| `TestDevBypassAcceptsExplicitlyConfiguredPassword` | 显式配置时旁路**必须仍能工作** |
| `TestDevAuthPassHasNoSourceLevelDefault` | 读回自己的源文件，函数体内不许有口令形状字面量 |

**为什么要有第 2 条**：一个恒拒绝的旁路和恒放行的旁路同样是坏护栏。
只测「不许放行」的护栏，会诱导下一个人把功能一起删掉。

### 4.2 负控记录

| 负控 | 做法 | 结果 |
|---|---|---|
| A | 把内置缺省口令还原回 `devBypassCredentials` | 2 红，指到 `dev_bypass_no_builtin_password_test.go:74/128` ✅ |
| B | 让旁路恒拒绝（`if true`） | 1 红，抓到「修复把功能也禁掉了」 ✅ |
| C | 卡口植入合成口令 `Zq7control&Probe9` | 转红，精确到 `file:line` ✅ |
| D | 给 C 加逐行 `secret-scan-ok` | 转绿，豁免精确到行 ✅ |
| E | 短路卡口强度闸门 | 炸出 11+ 条噪声，证明闸门在干活 ✅ |

> 负控 A 的**第一版没转红**，因为 PowerShell 整块字符串替换没匹配上（CRLF），
> 测试其实没被破坏。是靠「注入生效: False」的显式打点才发现的——
> **负控不转红时，先证明你真的改坏了东西**，否则「绿」和「没生效」无法区分。
> 改成行级定位后重做，才拿到有效结论。

### 4.3 护栏自身踩的坑

`TestDevAuthPassHasNoSourceLevelDefault` 第一版用
`strings.Index(body, "\n}\n")` 截取函数体，结果匹配到**内层 if 的收尾**，
把后面整个文件都当成函数体来扫，报出一堆 `writeError` 的错误文案。
**护栏出假阳性比没有护栏更糟**——它会被当噪声忽略。改成大括号配平。

---

## 5. 分支收编（逐文件法）

未用 `git diff` 的任何形式。判据：`git cat-file -e origin/main:<file>` +
`git diff --stat origin/main <branch> -- <file>` + `merge-base --is-ancestor`。

| 分支 | 判定 | 处置 |
|---|---|---|
| `origin/agent/w1-openhands-buzz-openpocket` | 已并入（仅分支有=0、有差异=0） | 远端已可清 |
| `origin/chore/gitignore-test-evidence-root` | 同上 | 远端已可清 |
| `origin/codex/platform-goal-20260930` | 同上 | 远端已可清 |
| `origin/consolidate/2026-10-01` | 同上 | 远端已可清 |
| `origin/feat/harmonyos-phase-a` | 同上 | 远端已可清 |
| `origin/feat/list-sync-rules-2026-09-09` | 同上 | 远端已可清 |
| `origin/feat/single-layer-header-menu` | 同上 | 远端已可清 |
| `origin/feature/flashcards-v1` | 同上 | 远端已可清 |
| `origin/feature/list-detail-state-preservation` | 同上 | 远端已可清 |
| `origin/feature/native-smoothness-p0` | 同上 | 远端已可清 |
| `ci/wire-style-guards-into-gates` | ahead=1，2 个独有文件（恢复脚本 + handoff） | 收编进 main（见 §6） |
| `feat/2026-10-01-stt-service` | ahead=8，7 个独有文件 + 9 个有差异 | **活跃在制品，未动** |
| `feat/mail-config-deploy` | ahead=24，16 独有 + 17 差异 | **活跃在制品，未动** |
| `email-pipeline-snapshot-2026-10-01` | ahead=79，46 独有 + 40 差异 | **活跃在制品，未动** |
| `fix/bugz-marketplace-conflict` | 已并入 | 保留（wt3 有未提交在制品） |
| `wip/pdf-document-plugin` | 已并入 | 保留（.wt-pdf 有在制品） |

**已删**：`fix/email-cache-backfill`（已并入、ahead=0；worktree
`C:\workspace\wt-mailfix` 只有一个未跟踪的 `COMMIT_MSG.txt`，内容是该次提交的
正文副本，确认可弃后 `worktree remove --force` + `branch -D`）。

**未动**：`refs/remotes/https/main` 是历史遗留的**伪远端引用**（无对应 remote
配置，2026-09-30 的旧快照）。不属于分支收编范围，仅记录。

> 代理端口又变了：`git fetch` 报 `errno=10061`，实测代理在 **7897**
> （7890/10809/1080 都不通）。按惯例用单命令覆盖，不改全局 ssh config。

---

## 6. `ci/wire-style-guards-into-gates` 收编时发现的问题

这个分支的 2 个独有文件里，恢复脚本本身是好的（幂等、每步验证、拒绝无 `-Confirm`
执行、密钥只走 env），但有 2 处硬伤：

1. `$Exe` 写死了 `C:\workspace\openpocket-wt-font\.verify-bin\pocketd-recover.exe`
   —— 脚本进 main 后会在错误的 worktree 构建。已改为基于 `$PSScriptRoot` 推导。
2. 第 308 行有 §2.2 那把口令的副本。已改为缺 `POCKET_RECOVER_AUTH_PASS` 时中止。

---

## 7. 遗留风险

### P0 —— 需要你在服务侧操作

1. 轮换网关 key `sk-6tGL…`（**实测仍可计费**）。
2. 轮换 MCP key `sk-mcp-…`（存活性未证明失效）。
3. 轮换 184 root SSH 口令。
4. 三把都在 git 历史里，`git log -S` 可取回。删字面量 ≠ 失效。

### P1

5. 10 个远端分支已验证完全并入 main，可删（我没删远端分支——那影响别人的
   本地克隆，且需要显式授权）。
6. 三个活跃在制品分支（stt / mail-config-deploy / email-pipeline）未动。
7. `wt3`（42 个未提交文件）与 `.wt-pdf`（5 个）归属并发会话。
8. 卡口对 `agents.json` / `chat_agents_seed.sql` 整文件豁免（内容本身就是
   凭据模式清单）。若将来往这两个文件里加**真实**私钥，卡口不会报。

### P2

9. `docs/`、`test-evidence/`、`e2e/README.md` 里仍有若干**历史记录**形态的
   那把口令（如「登录凭证：admin / …」）。本轮只清理了代码与脚本里**可执行的**
   那些；纯历史叙述保留原样，如实记录比事后篡改历史更有价值。
   **但要注意：这意味着卡口目前不会因为它们而红。**
10. `sec7bm_ins.txt`（0 字节，未跟踪，2026-10-01 22:36）来源不明，未动。

---

## 8. 下一轮提示词

```
继续 openpocket 的收编/审计轮（先读 docs/handoff/2026-10-02-round2-dev-bypass-password.md）：

1. P0 仍未解除（网关 key 实测仍可计费、SSH 口令待轮换）——先问一句轮换做没做。
2. §7-P1-5：10 个已验证并入 main 的远端分支要不要删（需显式授权）。
3. 普查三个活跃在制品分支（stt / mail-config-deploy / email-pipeline）是否已静止。
4. 卡口目前扫不到 docs/ 与 test-evidence/ 里的历史叙述型口令
   （§7-P2-9）。决定：加规则 / 逐文件豁免 / 还是接受现状并写进文档。
5. 任何新护栏继续走负控：不转红时先证明「你真的改坏了东西」。
6. 记住：负控不转红有三种可能——护栏坏了、改动没生效、判据写错了。
   用显式打点区分，不要靠猜。
```
