# round48 —— 交接单五步全部销账；`-race` 首次拿到权威读数（全 60 包绿）+ 负控踩坑「那段长注释不是 doc comment」

日期：2026-10-18 05:46 → 06:2x（本机时区 +08:00）
基线：`7d661f53`（`feat(gate): check:doc-empty-string-literal`）
承接：会话交接单 `handoff-x_3cfkae.md`（round41 §7.2 的自动门禁 + `go test -race` 验证）

## 一句话结论

交接单的 5 步**全部销账**：新门禁**经独立复核有牙**（含一次「负控不红」的假绿，
已定位为**注入点选错**而非判据坏了）；`go test -race ./...` 拿到本仓**第一份
权威读数** = **EXIT 0 / 60 包 ok / FAIL 0 / DATA RACE 0**，且用 round17 的探针
证明了 **race detector 确实处于武装状态**（不是静默降级跑出来的绿）。
`npm run gates` **46/46 全过**。

---

## 1. 交接单五步逐条销账

| # | 交接单要求 | 证据 | 结论 |
|---|---|---|---|
| 1 | 门禁已接线 | `frontend/package.json:55` + `gates.json` 的 `gates[]`/`ciRuns[]` 均在；`npm run gates` 46 项里含 `check:doc-empty-string-literal` | ✅ |
| 2 | 门禁有敏感度/特异度 | `--selftest` **实跑 24 / 声明 24** 通过（11 敏感度、10 特异度、2 行号、1 变盲、2 自指）；全仓扫描 **0 命中** | ✅ |
| 3 | 负控报红 | 见 §3 —— **第一次负控是假绿**，根因是注入点不是 doc comment；换到真 doc 后 EXIT=1 并点名 `file:76` | ✅（附一处踩坑订正） |
| 4 | 跑 `npm run gates` + smart-quotes | `npm run gates` **EXIT 0，46/46，59.7s**；`check-smart-quotes.mjs --selftest` **20/20**、实跑 OK，EXIT 0 | ✅ |
| 5 | 验证 `go test -race` | 见 §2：**EXIT 0 / 60 ok / FAIL 0 / DATA RACE 0**，且 detector 已自证武装 | ✅ |

---

## 2. `-race` 的权威读数（本仓第一次）

环境（与 round17 那次「跑不了」完全不同）：

```
$ go version            → go1.27.2 darwin/arm64
$ go env CGO_ENABLED    → 1
$ which gcc             → /usr/bin/gcc
```

结果：

```
$ cd backend && go test -race -count=1 ./...
EXIT=0
ok 60 / FAIL 0 / no test files 15
grep -c "DATA RACE\|panic:" → 0
最慢五包：internal/server 47.894s / agent 35.080s / email 24.826s
          livetest 13.882s / repohygiene 12.918s
```

两条历史欠账随这次一起结清：

- **`internal/server` 不再是「只编译未实跑」** —— `ok 47.894s`。
  ⚠ 但**读数要写准**：`audit_pg_test.go:23` 等 PG 集成测试在
  `POCKET_TEST_POSTGRES_DSN` 未设时**自己 `t.Skip`**（本机未设），所以
  「server 包绿」**不等于**「PG 路径被跑过」。这条欠账原样保留。
- **`internal/wecom`**：round17 §5.2 记着它 `TestDecryptFromNetFixture` FAIL 且
  被排除在包集外。本次 `./...` 全量带上 → `ok 8.472s`，**那条 FAIL 已不存在**。

### 2.1 「退出码 0」不能证明 detector 生效（round17 纪律）

沿用 round17 的形状放一个必然触发 data race 的探针（`counter++` 无同步并发写）：

```
$ go test -race -run TestNEGCTRL .
WARNING: DATA RACE
WARNING: DATA RACE
--- FAIL: TestNEGCTRLRaceDetectorIsActuallyArmed (0.00s)
PROBE_EXIT=1
```

⇒ **detector 确实武装**，上面那份全绿是**真在 race 模式下的绿**。
探针已删（它是环境探针，不是回归测试）。

---

## 3. 负控踩坑：那段超长注释**不是 doc comment**

交接单第 3 步点名 `store_select_scan_parity_test.go` 做负控。本轮照做，
在 **L4** 注入 `''` → **门禁 EXIT 0（假绿）**。

按纪律先怀疑量具而不是判据，拿 **gofmt 当独立量具**读同一行：

```
$ gofmt <file> | sed -n '4p'   → 逐字节未变（gofmt 也不改它）
```

⇒ 门禁是对的，**注入点是错的**。该文件 L3–L59 那段超长注释**两端都不贴任何顶层声明**：
上面是 `package email`，下面是**空行 + `import (`**（Go 里 import 前不可能有 doc comment）
⇒ 它是**自由浮动的注释**，不是 doc comment，gofmt 自然不会改写它。
本门「不报」是**正确行为**，不是漏网。

换到该文件里真正的 doc 锚点（紧贴 `type paritySite struct {` 的那段）：

```
$ # L76 注入 importance <> ''
$ gofmt <file> | grep -n "paritySite 描述"
76:// paritySite … （含 importance <> ”）。     ← gofmt 确实改写了 ⇒ 它是 doc

$ node scripts/check-doc-empty-string-literal.mjs
backend/internal/email/store_select_scan_parity_test.go:76  (doc 块 76-76) …
✗ 1 处 … EXIT=1
```

⇒ **门禁有牙，且报出行号 + §3.1/§3.2 的处置指引**。还原后 EXIT=0。

★ 已把这条坑写进脚本头注释（新增「负控怎么做」小节），并给出
**「拿 gofmt 当量具区分」的配方**：注入后 `gofmt <file> | grep -n '<你注入的那行文字>'`，
`''` 变成 `”` ⇒ 是 doc comment（门该报）；原样不变 ⇒ 不是 doc comment（门不该报）。
**否则下一个人会把「注入点选错」误判成「门禁坏了」而去放宽判据。**

---

## 4. 验证

| 命令 | 结果 |
|---|---|
| `node scripts/check-doc-empty-string-literal.mjs --selftest` | EXIT 0 — 实跑 24 / 声明 24 |
| `node scripts/check-doc-empty-string-literal.mjs` | EXIT 0 — 全仓 0 命中 |
| 同上（真 doc 锚点注入 `''`） | **EXIT 1** — 点名 `store_select_scan_parity_test.go:76` |
| `node scripts/check-smart-quotes.mjs --selftest` | EXIT 0 — 20/20 |
| `node scripts/check-smart-quotes.mjs` | EXIT 0 — 无落单弯引号 |
| `npm run gates`（frontend） | **EXIT 0 — 46/46，59.7s** |
| `cd backend && go test -race -count=1 ./...` | **EXIT 0 — ok 60 / FAIL 0 / DATA RACE 0** |
| race 探针（必然触发 data race） | EXIT 1 + `WARNING: DATA RACE` ⇒ detector 武装 |

**代码零改动**：本轮只改了 `scripts/check-doc-empty-string-literal.mjs` 的
**注释头**（新增负控小节）与本文件。判据逻辑一行未动。

---

## 5. 遗留（本轮未动，如实登记）

1. **`internal/server` 的 PG 路径仍未实跑**：`POCKET_TEST_POSTGRES_DSN` 未设时
   相关测试 `t.Skip`，本机不设它。§2 的 `ok 47.894s` **不覆盖**这些。
   解锁前置与安全约束见设计文档 §777.4（三条：专用空库 DSN、先跑
   `TestPGTestsNeverTargetTheProductionSchema`、逐条读豁免表），且**起 PG 需属主授权**。
2. **round41 §7.1（`.gitattributes` 缺 `*.go text eol=lf`）** 仍在。
   本轮跑 gofmt 时用的是**读文件到 stdout**（`gofmt <file>`）而非 `gofmt -w`，
   没有制造全仓行尾伪 diff；但该债本身未动。
3. **本门不覆盖 `/* */` 块注释形式**的 doc（`gofmt` 同样会改写，见脚本头 probe6）。
   Go 代码里几乎只见于包级 doc；round41 的 7 处真损坏全是 `//` 形式，先抓大头。
4. **race 结果是「一次快照」**（沿用 round17 §5.1 的口径）：它证明的是
   「2026-10-18 在 `7d661f53` 上没有 data race」，不是「这个仓库没有 data race」。
   新增并发代码需重跑。
