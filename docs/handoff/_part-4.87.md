
# §4.87 32 个探针在刮一个**已被删除的常量** ⇒ 它们的 401 输出全是「没登录」；外加 secrets 卡口的一个盲区

> 承 §4.86。本轮把一条被搬了三轮的「待查项」挖到底了：
> **`/api/marketplace` 带有效 token 仍 401** —— 前提是错的，它**根本不返回 401**。
> 顺着这条线挖出 BUG-V12（32 个探针必然拿不到 token）和 BUG-V13（secrets 卡口的
> password-literal 规则有盲区），并给这一整类建了守门脚本。

## §4.87.0 结论

- **「`/api/marketplace` 带有效 token 仍 401，守卫与 `/api/tasks` 不同」= 幽灵结论，撤案。**
  实测（有效 token，291 字符）：`/api/tasks` 200、`/api/marketplace/packages` **200**、
  `/api/marketplace/releases` **200**、`/api/marketplace/agents` **404**。**401 的条数 = 0。**
- 根因不是路由，也不是守卫：`server.go:855-858` 四条 marketplace 路由**全部**是
  `s.requireAuth(...)`，与 `/api/tasks` **同一个守卫**（我此前写的「守卫不同」是错的）。
- **BUG-V12（本轮新发现）**：**32 个**探针/验证脚本用
  `readFileSync('backend/internal/server/server_assistant.go').match(/devPass\s*=\s*"([^"]+)"/)`
  取 dev 口令，而那个常量**已被 `b6187bc1` 删除** ⇒ 刮取**必然**返回空串
  ⇒ 登录 401 ⇒ **拿不到 token** ⇒ 后续每一条探测都是**未鉴权**的
  ⇒ `/api/marketplace/*` 一律 401。**那份输出被当成证据记进了 handoff。**
- **BUG-V13（本轮新发现）**：`backend/internal/repohygiene/secrets_test.go` 的
  `password-literal` 规则**看不见** `MASTER = process.env.POCKET_MASTER || 'PocketTest2026'`，
  所以那道卡口是**绿的**，而 `scripts/` 下有 22 处这样的硬编码口令兜底。
- 新增 `scripts/check-dev-pass-sourcing.mjs`：把这一整类量化为 **54 处**（32 刮源码 + 22 硬编码兜底）。

## §4.87.1 BUG-V12：失败不是「有时」，是**必然**

```js
// 32 个脚本里 7 个 marketplace 探针全都长这样（写法完全一致，典型的复制粘贴病）
const devPass = (readFileSync('backend/internal/server/server_assistant.go', 'utf8')
  .match(/devPass\s*=\s*"([^"]+)"/) || [])[1] || ''
```

实测 `server_assistant.go` 里**已无** `devPass = "…"` ⇒ 5 个脚本按自己的正则实跑，
**全部**得到「正则无命中 → 空串」。

链条：

```
刮取空串 → login(password='') 401 → 没有 token
        → 后面每条探测都是未鉴权的
        → requireAuth 先于路由匹配跑 ⇒ /api/marketplace/* 一律 401
        → 而这份输出被当成「带有效 token 的观测」记进 handoff
```

**这与 BUG-V10 是同一类**：安全整改删掉了一个字面量，某个消费者仍在按名字找它，
而消费者的报错指向的是一个**已经不存在的东西**。BUG-V10 藏在 1 个文件里，
**BUG-V12 藏在 32 个文件里**——因为那段代码被复制粘贴了 32 次。

### 关键教训：401 处处都是，最常见的成因是**探针自己没登录**

`requireAuth` 在路由匹配**之前**跑，所以未鉴权请求根本走不到「这条路由注册了没有」。
**401 和 404 只有靠「同一个 token 下的对照路由」才分得开。**

§4.86 刚写完的那句话在这里要再强调一次，因为它同时是这轮的教训：
**「受阻于环境」是能吸收任何失败的解释。** 这次它吸收的是「脚本自己坏了」。

## §4.87.2 BUG-V13：secrets 卡口是绿的，但它看不见这一种写法

`secrets_test.go:141-145` 的规则：

```go
regexp.MustCompile(`(?i)\b[A-Za-z_]*(?:pass|pwd)[A-Za-z_]*\b\s*[:=]\s*` +
    `(?:"([^"$]{8,})"|'([^'$]{8,})')`)
```

`TestNoCommittedSecrets` 实跑：**ok, 2.919s**（绿）。而
`scripts/diag-marketplace-agents.mjs:11` 写着
`const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'`。

**两重漏**：

1. 变量名 `MASTER` **不含 `pass` / `pwd`** ⇒ 主模式根本不匹配；
2. 字面量不在 `=` 正后方（中间隔着 `process.env.POCKET_MASTER ||`）⇒ 形态也对不上。

判别力实测（拿它的正则直接跑，3 个对照**全部命中**⇒ 证明正则不是永不匹配器）：

| 样本 | 结果 |
|---|---|
| `const MASTER = process.env.POCKET_MASTER \|\| 'PocketTest2026'`（现场那行） | **不命中** |
| `const adminPass = 'SomeRealPassword123'` | 命中 |
| `const devPass = "SomeRealPassword123"` | 命中 |
| `const pwd = 'SomeRealPassword123'` | 命中 |

> 抄这条正则时还踩了一个小坑：Go/RE2 的内联标志 `(?i)` 在 **JavaScript 里不认**
> （`Invalid group`），要提到 `RegExp` 构造函数的第二个参数。而 `node --check`
> **不会**报，只在运行时炸——「语法检查通过」不等于「判据能跑」。

**本轮没有改这个 Go 规则**：它在 `backend/`，正被并发会话写；而且新脚本已经覆盖了
`scripts/` 这一侧的缺口（54 处全部报出）。**Go 规则的这个盲区按「已定位、未修改」记录**，
要改需要连带处理那 22 处硬编码兜底，否则一改就把整仓打红。

## §4.87.3 新增守门脚本，以及它自己踩的三个坑

`scripts/check-dev-pass-sourcing.mjs`（`--selftest` / `--list` / 默认 exit 1）：

```
扫描 280 个 .mjs，发现 54 处
  scrape-dev-pass     32   （刮 .go 源码里的口令类常量）
  hardcoded-fallback  22   （凭据名变量的 env 兜底里塞了字面量）
```

`--selftest` 三项全过：3 类能报出、6 类该放过的放过（含注释行、读 `.go` 取端口号等合法形态）、
逐条关规则后恰好各少报 1。

**这个脚本自己连踩三个坑，全部记在这里，因为它们都属于「不报红、只是让判据少干活」那一类**：

1. **第一版 R2 噪声爆炸**：写成「任何 `process.env.X || '字面量'` 都报」，
   扫出 **142 处**，大半是 `POCKET_SERIAL || '4c308e2e'`、`POCKET_API || 'http://…'`、
   `JAVA_HOME || 'C:\Program Files\…'` 这类无害默认值。
   **142 条里大半是假的守门脚本，下一周就会被 `--list | head` 忽略掉——比没有更糟。**
   ⇒ 收紧成「变量名本身像凭据 **且** 字面量含字母数字且长度 ≥ 8」，降到 54 处且条条是真货。
2. **收紧时把真实案例一起收没了**：多写了个否定预查 `(?![A-Za-z]*$)`（「排除纯字母的名字」），
   而 **`MASTER` 恰好是纯字母** ⇒ 被自己排除。是 `--selftest` 的「该报的报」当场报红的。
   *多余限定符在写规则时非常自然，但它们不报红、只是让判据变瞎。*
3. **自指豁免静默失效**：`SELF` 取自 `URL.pathname`（正斜杠），而待扫路径来自
   `path.join`（Windows 上是**反斜杠**）⇒ `f === SELF` 永远为 false，豁免从未生效，
   自己被自己报了 3 条。**两边都归一化成正斜杠**才修好。

> 第 1 条和 §4.86 记的「豁免清单是护栏的坟场」是同一个教训的另一面：
> **过宽的规则和过长的豁免表，效果一样——都是让判据失去可信度。**

## §4.87.4 回应本轮外部审计的四条证据缺口

前一轮已回过同样的三条，这轮**重新拿当前证据复核**，结论一致。**没有一条靠「我记得改过」**。

| 审计说法 | 核对结果 | 证据 |
|---|---|---|
| 「真机 Maestro 从未成功执行一次（零安装包、零运行产物）」 | **不成立** | `~/.maestro/tests/` 下 **155 个**运行目录（最近 `2026-10-02_231342`）。该目录内有 `login-gesture/commands.json`（11.1KB）、`manifest.json`（声明 `DEVICE_LOG` 340125 字节 + `TAKE_SCREENSHOT` 1 张）、`logs/device-logcat.txt`（332KB）、`takeScreenshot/…/login-gesture-rejected.png`（197KB）。**截图内容已目视核对**：真机状态栏 11:14 / 电量 76% / 720×1640，页面为 OpenCode Pocket 登录页，`admin` + 一串掩码密码，**「登录失败：用户名或密码错误」**（401 往返可见），页脚 `v1.2.0-mobile` / `后端服务器 · http://127.0.0.1:18099` |
| 「闪卡入口缺陷（『新建卡组』文案 → 卡片编辑页）只记录未修」 | **不成立（已证伪）** | 代码实证：`FlashcardListView.vue:79-84` 有 `data-testid="deck-create-toggle"` + 文案取 `flashcards.deck.create`；`:89-99` 展开后的建组表单 `deck-create-form-existing`；`:17` 的按钮取 `flashcards.list.create` 并在 `:160` 跳 `/flashcards/new`。i18n 实值：`flashcards.list.create="新建卡片"`（→ 建卡片页）、`flashcards.deck.create="新建卡组"`（→ 真的建卡组）。**标签与行为已对齐** |
| 「`/api/marketplace/agents` 的 404 说法在只读探测下无法证实（返回 401）」 | **404 成立**，且本轮把「401 从哪来」也定位了 | 有效 token 实测：`/api/marketplace/agents` → **404** `{"error":"not found"}`；同 token `/api/tasks` → 200、`/api/marketplace/packages` → 200（**token 有效性由此坐实**）。而 401 的来源已查明：**§4.87.1 那 32 个必然拿不到 token 的脚本** |
| 「多个功能点写路径与 https 回归仍为未验证，Keystore 插件缺失未实现，『打通所有的功能点』不成立」 | **成立，不推辞** | 见 §4.87.5。**Goal 未完成，我没有把它标成完成。** |

> 这三条被连续三轮重提（§4.78 / §4.81-4.82 / §4.83-4.85）。本轮把它们**连同原始产物路径
> 一起**写进 §4.87，而不是只写结论——审计方大概只看了当轮会话目录。
> 但**根因不在审计方**：根因是这些证据只以散文形式存在于 handoff 里，
> 没有一条判据把「Maestro 产物目录存在且含截图」和「闪卡两个 key 各归其位」钉成可重跑的检查。

## §4.87.5 本轮遗留

- **32 个脚本仍在刮已删除的常量**（`scripts/check-dev-pass-sourcing.mjs --list` 可列全）。
  它们的 401 类输出**一律不可信**。逐个改成「口令只从环境取 + 缺就 exit 2」是独立一件事。
- **22 处 `MASTER = process.env.POCKET_MASTER || 'PocketTest2026'` 硬编码兜底未清。**
  Go 侧 `password-literal` 看不见（新脚本能看见），要清就得配套改规则，否则改一个红一个。
- 其余 §4.86.5 的遗留照旧：138 个 `.mjs` 硬编码 CDP 端口；BUG-V11 需生产 env 授权；
  `POCKET_PROD_PASS` 缺失；`_login.yaml` 孤儿；BUG-AX 设备侧负控、闪卡两入口渲染/点击、
  会议写入设备侧持久化、「tap 报 COMPLETED 但没反应」坐标对账、`:param` 模板、gateway 六页。
- 待产品定范围 9 项（Keystore 原生插件已确认**全平台不可用**、同步编排层、改密入口、
  gateway 四页、BUG-AV、i18n ~800 条、BUG-AR、PKM 删除入口、BUG-AQ/AK、`/contacts` 端点、
  `TICK_MS` 与录音策略）照旧。

### 下一轮建议的第一件事

把 §4.87.1 那 32 个脚本收敛成一个共享的 `scripts/lib/dev-pass.mjs`
（`requireDevPass()`：只从 env 取，缺就 `exit 2`），逐个替换。
**关键不是「让它们能登录」，是让「拿不到 token」这件事变成响亮的失败**——
现在它们失败得悄无声息，而那份沉默的输出被当成了三轮的证据。
