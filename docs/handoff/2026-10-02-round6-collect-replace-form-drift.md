# 2026-10-02 round6 —— 收集侧与替换侧形态清单漂移，以及分支审计（全绿无动作）

> 触发：`/goal` 定时任务（拉主分支、清理不活跃子分支、批判与审计 24 小时内的提交、本地修正、合并推送）。
> 执行窗口：2026-10-02 09:46 起。
> 基线：本轮开始时本地 `main = c01e555e`，与 `origin/main` **完全一致**（fetch 后逐字节核对），无需合并。
> 本轮产出：`f8fbd83b`（已 fast-forward 进 main 并推送，远端 `main = f8fbd83b` 已用 `git ls-remote` 复核）。

---

## 0. 一句话结论

本轮找到**一个真缺陷**：`b9838fc2` 声称修好了「背景图与懒加载图永远不会被内联」，
但**只修好了收集侧，替换侧没跟上** —— `background=` 与 `data-original`
会被收集、被下载成 data URI，然后**原样留在 HTML 里**。

分支侧的结论与 round5 一致且更强：**本轮既不合并也不删除任何分支**，
因为两个未合并分支在审计开始前 2 分钟和 4 分钟还有提交，是**活跃并发会话**（详见 §3）。

与 round5 不同的是：**本轮基线是全绿的**（gates 1444 例 0 红、backend 三包全绿）。
缺陷不是被测试抓到的，是**审计读代码 + 写探针实测**抓到的 —— 详见 §4，
这本身是本轮最值得记的一条。

---

## 1. 根因

### 1.1 现象（探针实测，非阅读推断）

`b9838fc2` 的提交信息写的是「远程图片只收集 `<img src>`，背景图与懒加载图永远不会被内联」。
它新增了 `collectRemoteImageRefs`，收 4 类形态；`inlineDataUri` 却仍只认 2 类。
实测（`preloadRemoteImages` + 假 fetch，输出为真实函数返回值）：

| HTML 形态 | 收集 | 内联 | HTML 里仍残留远程地址 |
|---|---|---|---|
| `<img src=…>` | ✅ | ✅ | — |
| `<img data-src=…>` | ✅ | ✅ | — |
| `<img data-lazy-src=…>` | ✅ | ✅ | — |
| CSS `url(…)` | ✅ | ✅ | — |
| **`<td background=…>`** | ✅ | ❌ | **是** |
| **`<img data-original=…>`** | ✅ | ❌ | **是** |

### 1.2 机制

`inlineDataUri` 的属性 alternation 只有 `\bsrc\s*=`：

- **`data-original` 里根本没有 `src` 子串** → `\bsrc` 匹配不到；
  `data-src` 之所以「碰巧能用」，纯粹因为 `\b` 在 `-` 后成立 —— 是巧合，不是设计。
- **`background=` 既不是 `src` 也不是 `url(`** → 同样够不着。

于是**收集侧认 5 类形态、替换侧只认 2 类，两份清单从未被绑在一起**。

### 1.3 后果比「没抓到」更差

这两类图**确实被下载了**（浪费一次往返 + 内存 + base64 计算），
但 HTML 里仍是远程地址。用户看到的还是「缺图」，
而 WebView 的基址是 `capacitor://`，这些图自己发请求也**加载不出来**。
每打开一次这封邮件，就白付一次网络往返。

---

## 2. 为什么既有护栏没抓到（本轮唯一被推翻的判据）

`email-image-form-coverage.test.mjs` 有 21 个用例，其中对 `background=` /
`data-original` 的断言**全部只验「被收集」**（`collectRemoteImages(html).includes(...)`），
**从没有一条验「被替换」**。

> **断言收集，不蕴含断言替换。**

这是本轮最值得记的一条：护栏的形状看起来是对齐主题的
（文件名叫 `email-image-form-**coverage**`，测的也确实是「形态覆盖」），
但它覆盖的是**收集侧的形态清单**，而缺陷在**替换侧**。
两条清单长得几乎一样，护栏只盯着其中一条 —— 于是它在自己的职责范围内完全正确，
在缺陷所在的那一半上完全失明。

对照 round5 的教训（判据被 CRLF 静默废掉）：
两者同族 —— **判据存在 ≠ 判据在判**。
round5 是 needle 匹配不上，本轮是**断言的落点选错了函数**。

---

## 3. 分支审计（结论：不合并、不删除）

### 3.1 分类结果

审计时刻（2026-10-02 09:46）实测：

| 分支 | 最后提交 | 空闲 | 相对 main | 处置 |
|---|---|---|---|---|
| `email-pipeline-snapshot-2026-10-01` | 09:46:20 | **2 分钟** | UNMERGED (140) | **不动 —— 活跃** |
| `feat/mail-config-deploy` | 09:44:51 | **4 分钟** | UNMERGED (45) | **不动 —— 活跃** |
| `ci/wire-style-guards-into-gates` | 06:47:30 | 181 分钟 | MERGED (0) | 保留（有 worktree） |
| `feat/2026-10-01-stt-service` | 23:54:55(前一日) | 594 分钟 | MERGED (0) | 保留（有 worktree，且工作区有未提交改动） |
| `origin/*` 全部远端分支 | — | — | **全部 MERGED** | 无需处理 |

### 3.2 为什么不动那两个未合并分支

定时任务要求检查「**1 小时前**所有没有合并的不活跃的子分支」。
这两个分支的空闲时间是 **2 分钟和 4 分钟** —— 它们**不满足「不活跃」这个前提**。

佐证（reflog，审计时抓的）：

```
09:46:20  email-pipeline-snapshot  commit: docs(email): 三条否定结论 + cherry-pick …
09:44:51  feat/mail-config-deploy  commit: docs(email): 定位 IMAP 硬截止失效的真实机制 …
09:42:05  email-pipeline-snapshot  cherry-pick: fix(email): 没有新邮件的同步也必须推进 …
```

审计过程中我还观察到 `email-pipeline-snapshot` 在两次查询之间
从 `2ee85dc0` **移动到了 `4b23a46c`** —— 分支头在我眼皮底下被推进。
**在这样的分支上做 merge 或 delete，等于把别人的在制品毁掉。**

### 3.3 那两个「已合并且空闲」的分支为什么也不删

`ci/wire-style-guards-into-gates` 与 `feat/2026-10-01-stt-service` 均 `unique=0`
（已完全并入 main，技术上可删），但：

- 两者都**挂在活跃 worktree 上**（`wt-font` / `wt-stt`），删分支会让 worktree 悬空；
- `wt-stt` 的工作区**还有未提交改动**（22 个文件）。

删它们不是本轮任务要求的（「不活跃且未合并的分支」才在范围内），
且代价由别人承担。**保留。**

> 附：`wt-stt` 那些「脏文件」里绝大部分是 `core.autocrlf=true` 造成的 CRLF 噪声
> （`git diff` 里大量文件 0 行实质变化），但 `frontend/src/features/sessions/SessionListView.vue`、
> `scripts/verify-stt-stream.ps1` 等确有实质改动。**不要按 `git status` 的行数判断那是不是垃圾。**

---

## 4. 改动文件与关键行为

| 文件 | 改动 |
|---|---|
| `frontend/src/features/email/email-image-preload.ts` | `inlineDataUri` 的属性 alternation 由 `(?:src)` 补齐为 `(?:src\|data-src\|data-original\|data-lazy-src\|background)`（带引号 + 无引号两条分支同时补），并加注释说明「收集侧与替换侧必须同一份清单」 |
| `frontend/src/features/email/__tests__/email-image-form-coverage.test.mjs` | 新增 3 条**验替换**（而非验收集）的用例：`background=` 端到端、`data-original`/`data-lazy-src`/`data-src` 三连、**9 形态漂移护栏**（任一形态只收集不替换即红） |

**行为变化**：`background=` 与 `data-original` 引用的远程图，
现在会真正被换成 data URI；界面上这两类「缺图」应当消失。

---

## 5. 测试命令与结果

### 5.1 基线（改动前，干净 main）

```
cd backend && go build ./...            → exit 0
cd backend && go vet ./...              → exit 0
go test ./internal/email/...            → ok (4.540s) / ok rules (0.403s)
go test ./internal/server/...           → ok (25.368s)
cd frontend && npm run gates            → GATES_EXIT=0（1444 例 0 红）
```

> gates 在**全新 worktree** 里第一次跑会因缺 `node_modules` 报 `'vue-tsc' is not recognized`，
> 那不是代码问题 —— 先 `npm ci`（本机实测 1m，added 293 packages）再跑。

### 5.2 改动后

```
node --test src/features/email/__tests__/email-image-form-coverage.test.mjs
  → # tests 21  # pass 21  # fail 0     exit 0
npm run gates                           → GATES_EXIT=0
```

### 5.3 负控（判据是否真的在判）

把 alternation 退回 `(?:src)`（即还原成有缺陷的实现）后重跑：

```
# tests 21  # pass 18  # fail 3
    not ok 3 - background= 属性：抓到了就必须换上（不只是被抓到）
    not ok 4 - data-original / data-lazy-src：抓到了就必须换上
    not ok 5 - 收集侧与替换侧的形态清单不得漂移（护栏）
```

恢复修复后 21/21 全绿。**判据确实会红，不是恒真。**

---

## 6. 遗留风险

1. **本轮只覆盖了本机可静态验证的部分。** 172 个提交 / 484 个文件，
   我只对与「本轮发现的缺陷同族」的区域（收集↔替换这类**两份清单是否对齐**）
   做了实测抽查，**不等于** 24 小时内所有改动都已被逐条验证。
   其余部分的可信度仍等于各自提交自称的水位。

2. **两条形态清单的漂移护栏只护住了这一个文件。**
   `collectRemoteImageRefs` / `inlineDataUri` 的一致性现在由测试保证，
   但**仓库里没有「凡是存在 collect/replace 配对就要断言替换」的通用卡口**。
   同类缺陷（尤其在 `cid:` 内联那条线上 —— 它历史上已经犯过两次）仍可能复发。

3. **界面效果未做真机验证。** 「背景图/懒加载图不再缺图」目前只有
   单测与探针级证据；需要在真机打开一封含 `<td background=>` 的邮件确认。

4. **两个活跃分支仍未收编。** 它们的 140 + 45 个提交里有
   `fix(email): 台账 CSV 的合计金额落在了「文件名」列`、
   `fix(feishu): 消息 content 被多包一层` 这类**看起来是真修复**的提交，
   但它们在写入中，**本轮不碰是对的**。需在它们停稳后的下一轮处理。

5. **代理端口会漂移。** 本轮实测 `7900` 已死、`7897` 存活
   （`7900/7890/10809/1080/10808/33210` 全部不通）。
   提交前请按此覆盖，**不要改全局 `~/.ssh/config`**。

---

## 7. 下一轮提示词

> 直接贴给下一个 `/goal` 轮次：

1. **先查并发再动分支。** `git reflog --date=iso --all | Select-String '<今天的 0X:>'`
   看有没有别的会话在提交；空闲 < 1h 的分支一律视为活跃、不合并不删除。
   审计期间盯一次分支头有没有移动（我本轮观察到 `2ee85dc0 → 4b23a46c`）。
2. **代理**：本机 7897 存活，7900 已死。用
   `$env:GIT_SSH_COMMAND='ssh -o ProxyCommand="C:/Progra~1/Git/mingw64/bin/connect.exe -H 127.0.0.1:7897 %h %p" -o ServerAliveInterval=15 -o ServerAliveCountMax=20 -o TCPKeepAlive=yes'`，
   **不要改全局 config**。push 含二进制大文件时这个 keepalive 是必需的。
3. **新建 worktree 跑基线**（`git worktree add … -b audit/<date> main`），
   记得先 `npm ci`，否则 gates 会假红在 `'vue-tsc' is not recognized`。
4. **审计方向（承接本轮 §6.2）**：查 `cid:` 内联那条线是否也存在
   「收集认 N 种 / 替换认 M 种」的漂移 —— 它历史上已犯过两次同类错。
   判据写法照抄本轮：**对每种形态断言「输出里不再有远程地址」**，而不是断言「被收集到」。
5. **每个新护栏先写负控再提交**：把被测逻辑改回旧样，确认新用例转红，再恢复。
   判据匹配**代码结构或输出**，不要匹配源码文本。
6. 收编那两个活跃分支前，先 `git merge origin/main` 看清远端新增了什么，
   冲突要**保留两侧增量**逐个比对语义，不要 `git checkout <branch> -- <paths>`。
