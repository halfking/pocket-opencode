# round41 —— gofmt 才是弯引号的真凶；空行污染让 gofmt 门禁失明

日期：2026-10-04　分支：main　触发：24h 修正任务审计

## 1. 结论先行

上一轮（round40，提交 `231780e1`）把「SQL 空串字面量 '' 被写成 ”」的根因
判定为「不是工具，是生成时产生的」，并据此逐处订正 + 加门禁。**这个判定是错的。**

实测（本轮，字节级复现）：**是工具。`go/format` 会主动把 doc comment 里相邻的两个
ASCII 单引号 '' 改写成 U+201D `”`。**

最小复现（5 行文件，无任何本仓脚本参与）：

```go
package p

// SQL: importance <> '' or x
func F() {}
```

`gofmt` 的标准输出是 `// SQL: importance <> ” or x`。用 `go/format.Source` 单独跑
得到同样结果 ⇒ 不是 `gofmt.exe` 被改，是**标准库行为**（go1.27.1 windows/amd64）。

**为什么 round40 搜不到**：`scripts/**` 与 `.github/**` 里当然搜不到——
改写者不在仓库里，在工具链里。而它**只作用于 doc comment**（紧邻顶层声明的注释块），
这正是 7 处命中点全是 doc comment 的原因。

**这意味着 round40 的修复无法自存**：任何人跑一次 `node scripts/check-gofmt.mjs --fix`
（仓库自己写在 `docs/gates-local-vs-ci.md` 里的标准修法），'' 立刻被改回 `”。

## 2. 顺带挖出的第二个缺陷：空行污染让 gofmt 门禁完全失明

`231780e1` 同时在**同样这 7 个文件**里，把每一行后面都插了一个空行：

| 文件 | 行数 | 空行占比 |
|---|---|---|
| email/store.go | 2599 → 5563 | 55% |
| server/server_assistant.go | 3029 → 5706 | 49% |
| email/fetcher.go | 1274 → 2422 | 50% |
| chatagent/store.go | 327 → 637 | 53% |
| chatagent/sqlite_store.go | 313 → 626 | 53% |
| email/store_importance_constraint_test.go | 90 → 157 | 49% |
| task/store_contract_test.go | 293 → 540 | 49% |

**gofmt 对此一声不吭，而且它是对的**：空行把结构体字段的**对齐组**切断了，
于是 `store  *Store` 被判定为「这组只有一个字段、无需对齐」，塌成 `store *Store` 也合法。

⇒ 后果是：文件体积翻倍、每次 diff 都裹着几百行无关空行、而 `check:gofmt` 报「0 真债」。
**这正是「门禁全绿但内容已被破坏」的教科书案例。**

## 3. 两条已验证的判别式（下一轮别再重走）

### 3.1 哪些注释会被 gofmt 改写

实测 5 种位置，只有**紧邻顶层声明的 doc comment** 会被改写：

| 位置 | 结果 |
|---|---|
| 紧邻 `func` 的 doc comment | ❌ 被改成 `”` |
| 紧邻 `type` 的 doc comment | ❌ 被改成 `”` |
| 空行隔开、不算 doc comment | ✅ 保留 |
| 函数体内的注释 | ✅ 保留 |
| 行尾同行注释 | ✅ 保留 |

### 3.2 怎么写才能既准确又 gofmt 稳定

逐个实测候选写法：

| 写法 | 结果 |
|---|---|
| 相邻两单引号（SQL 空串） | ❌ → `”` |
| `""` | ✅ 保留（但 SQL 里是标识符，语义错，不用） |
| 反引号对 | ❌ → `“` |
| 列表项里的空串 | ❌ |
| **doc comment 内的缩进代码块行** | ✅ **保留** |

**缩进代码块是唯一既逐字保留 SQL 字面量、又 gofmt 稳定的写法。**
注意 gofmt 要求代码块前后各有一条空 `//` 分隔线，缺一条它就报红。

## 4. 本轮改动

### 4.1 七个文件：删掉多余空行，语义逐行核对为零变化

做法：以 `231780e1^`（污染前）为模板取回，再按 §3.1 的规则把该修的 `”` 订正回来。
**判据**（不是「看着对」，是逐行机器核对）：把每行的连续空白折叠成单空格后，
新文件与 `231780e1` 的非空行序列**完全一致** ⇒ 除空行与对齐填充外零内容变化。
7/7 通过。

对齐由 gofmt 自动补回（`store  *Store` 等），所以**没有手写任何对齐空格**。

### 4.2 七处 doc comment 改成 gofmt 稳定写法

按「该字面量是不是这段注释的重点」分两种处理：

- **字面量本身是重点**（4 处）→ 放进缩进代码块，逐字保留：
  `email/fetcher.go`（EXCLUDED.snippet 的 CASE WHEN）、`email/store.go`、
  `email/store_importance_constraint_test.go`、`task/store_contract_test.go`。
- **只是顺带提到**（3 处）→ 改用中文措辞，与该文件既有风格一致：
  `server/server_assistant.go`（传空字符串，同段上文本来就写「空字符串」）、
  `chatagent/store.go` 与 `chatagent/sqlite_store.go`（workspace_id 为空串）。

`email/store.go` 里 `“副作用型”` 那对中文引号**保持原样**——它是成对正文引号，
不是 SQL 空串，被 round40 的扫描规则正确地放过了。

### 4.3 新增门禁 check:blankline-bloat

`scripts/check-blankline-bloat.mjs`：空行占比 > 35% 且行数 ≥ 80 即判红。
阈值依据：本仓 980 个 .go/.sql 正常最高约 12%，被污染的 7 个是 49%~55%，35% 落在中间。

已接进 `frontend/package.json` 与 `frontend/gates.json` 的 `gates[]` + `ciRuns[]`
（`run-gates.mjs` 会核对每个 `check:*` 都在 gates 或 notGates 里，漏接线会直接退出非 0）。

**判据自证（负控）**：把 `store_importance_constraint_test.go` 复制一份、
在每行间插空行（还原 231780e1 的破坏形态，占比 0.55），门禁 EXIT=1 并点名该文件；
还原后 EXIT=0。
第一版负控是**假绿**——注入只多加了一个换行（93→94 行），门禁没报。
按「没落盘 / 没生效」重做后才拿到真负控。

## 5. 验证

```
cd backend && go build ./...                                     # EXIT=0
cd backend && go vet ./internal/{email,chatagent,task,server}/... # EXIT=0
cd backend && go test ./internal/{email,chatagent,task}/...      # 全绿（email 15.6s）
node scripts/check-gofmt.mjs            # 真债 7 -> 0，EXIT=0
node scripts/check-blankline-bloat.mjs   # 0，EXIT=0（负控下 EXIT=1）
```

## 6. 本轮其它处置

- 合并 `origin/main`（98 个提交）。唯一冲突 `scripts/flashcards-test-fixture.mjs`：
  取上游侧——它是本地的超集（多出「先离开闪卡路由」这一步与弯路④），
  且 `sh` 已在第 64 行定义，本地侧反而要在此处重复定义。
- 清掉 2 个 **0 字节**的遗留测试文件（`store_upsert_messageid_test.go` 在仓库根、
  `diag_real_fetch_snippet_stages_test.go`）。0 字节 .go 会被 `go build` 当成
  「缺 package 子句」直接编译失败，是编译地雷。
- 删除 4 个远端分支：`audit/2026-10-04-main-health`、
  `fix/email-upsert-messageid-conflict`、`fix/2026-10-03-autogrow-border-compensation`、
  `fix/2026-10-03-gofmt-gate-red`。删除前逐个核实：独有提交 0、`origin/main...branch`
  diff 0、且都是 `origin/main` 的祖先 ⇒ **没有任何独有工作可捞**。
- 保留 `docs/round37-section10`（本地）：它有独有提交且挂在 `.wt-build` worktree 上，
  正在被人用。

## 7. 遗留风险 / 下一轮注意

1. **`.gitattributes` 仍然缺**。本机 `core.autocrlf=true`，检出后 840 个 .go 是 CRLF，
   `gofmt -l` 因此报 840 个（`check-gofmt.mjs` 已把这类行尾噪声归一化掉，所以门禁是绿的，
   但**裸跑 `gofmt -w <dir>` 会把整个 backend 的行尾刷成 LF**，制造全仓伪 diff）。
   加 `*.go text eol=lf` 能一劳永逸，但会牵动一次 renormalize，值得单独一轮做。
2. **gofmt 门禁管不到语义**。它只判「格式」，而本轮两个缺陷（空行污染、弯引号）
   一个它看不见、一个它自己就是凶手。`check:blankline-bloat` 补的是第一个洞；
   第二个洞靠 §3.2 的写法约定 + 人工 review，**目前没有自动门禁**。
   若要自动，可加一条：doc comment 里出现相邻单引号即报红（需把 §3.1 那张表一起写进文档）。
3. **本轮只跑了 3 个包的测试**。`internal/server` 只做了 build + vet，
   它的测试需要 PG 隔离环境，未在本轮执行。
