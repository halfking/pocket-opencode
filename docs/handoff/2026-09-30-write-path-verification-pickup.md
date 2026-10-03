# 下一轮验证接力单：六模块 UI 写路径（2026-09-30 19:00-20:30）

> ## ⚠️ 先读这一段：本文件已被部分取代
>
> 本文件写于 2026-09-30 19:00-20:30，当时我在真机上跑六模块写路径，**拿到 0 项 PASS**。
> **但同一时间有一个并行会话正在同一台真机上做同一件事，并且已经拿到 PASS 并推送了。**
> 合并 `origin/main`（`ee92f3e`，22 个提交）后，那批结果才是当前事实：
>
> | 模块 | 并行会话已推送的结果 | 提交 |
> |---|---|---|
> | 网关 | 真机 UI 写路径 **12/12**（连跑两轮），未发现新缺陷 | `5c577e9` |
> | 财务 | 后端契约 21/21 + 真机 UI 写路径 **26/26**（连跑两轮），带 sabotage 证伪 | `f1400eb` |
> | 实例 | 范围**纠正为只读** + 契约 12/12 + 真机读路径 13/13 | `d8ff217` |
> | 闪卡 | BUG-AA 真机验证 13/13（含证伪） | `22dbc3e` |
> | 密码箱 / 市场 | 探针固化 + 对上一轮审计意见逐条回应 | `8e7f030` |
> | 邮箱 | 期间发现并修了 BUG-AB（账户写路径走不通）/ BUG-AC（连接失败显示成成功） | `404853f` |
> | 笔记 | BUG-AH 三个 FTS 触发器一个都没建出来，已修 | `bc8816b` |
>
> **因此下文 §2 里那些 `NO_ENTRY` / `BLOCKED`，多数是「测量被污染」而不是事实。**
> 例如「实例无写入口」——并行会话独立得出的是「实例本就是只读模块」，
> 并且发现 `/api/instances` 对 POST/DELETE/PUT **回 200**（BUG-AE，`d9d40b4`），
> 这才是真缺陷。同类还有 BUG-AD（finance）、BUG-AG（vault 空 blob 覆盖密文）。
>
> **本文件仍有价值的部分**：§0 的并发污染取证方法、§1 的环境前置条件、
> §4 的两个测试方法论翻车记录、§3.3 的 Keystore 契约、以及 `scripts/verify-write-paths.mjs` 本身。
> §2 的判定结论请以上表和 `docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md` 为准。

> **完成度声明只以「已验证 / 未验证」两节为准，禁止外推。**

承接 `docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md` 的 §5「未验证（下一轮必须补）」第 4、5 条
（六个模块 UI 写路径、任务/会话的编辑删除）。

---

## 0. 接力第一件事（别跳过）

上一轮和本轮都被这件事坑过，**它是本仓库最大的测量污染源**：

- 本轮中途我正在跑 `verify-write-paths`，探针查到的 `location.hash` 却是 `#/finance`
  且 `locked=false` —— 说明**有另一个会话/进程正在驱动同一台真机**。
  这直接导致本轮的写路径测量**不可信**：同一个 stamp 的两次运行，
  一次看到「创建按钮 disabled（库锁）」，另一次看到「页面已解锁」。
- 上一轮记录：另一个会话并发跑 `device.mjs` / `cdp.mjs`，造成 `adb kill-server`、
  两次重装 APK 覆盖配置，13 模块验证整轮作废。

**判定规则**：连续两轮同一项结果不一致时，先怀疑被污染，**不要**先怀疑产品。
把设备独占下来再重测，比改代码便宜得多。

独占步骤：

```powershell
# 1. 确认没有别的会话在动仓库/设备（问人，或看 .scratch/ 与 logs/ 有没有别人刚写的文件）
# 2. 确认 adb server 干净、隧道在位
$adb = "$env:LOCALAPPDATA\Android\platform-tools\adb.exe"
& $adb devices -l
& $adb connect 192.168.31.19:5555
& $adb -s 192.168.31.19:5555 reverse tcp:8088 tcp:8088
& $adb -s 192.168.31.19:5555 reverse --list    # 期望能看到 host-30 tcp:8088 tcp:8088
```

⚠️ `adb` **不在 PATH**，在 `C:\Users\86133\AppData\Local\Android\platform-tools\adb.exe`。
`scripts/*.mjs` 里都硬编码了这个绝对路径，换机器要改。

---

## 1. 环境前置条件（本轮实测过的，可直接复用）

| 项 | 状态 | 怎么确认 |
|---|---|---|
| adb | ✅ 在 `%LOCALAPPDATA%\Android\platform-tools\adb.exe`，**不在 PATH** | `Test-Path $env:LOCALAPPDATA\Android\platform-tools\adb.exe` |
| 真机 | ✅ `192.168.31.19:5555`（Redmi flame / 2411DRN47C） | `adb devices -l` |
| 模拟器 | ✅ `emulator-5554` 同时在线 | 同上 |
| 隧道 | ✅ `adb reverse tcp:8088 tcp:8088`（名 `host-30`） | `adb reverse --list` |
| PostgreSQL | ✅ 多个 postgres 进程，5432 在听 | `Test-NetConnection 127.0.0.1 -Port 5432` |
| pocketd | ✅ `pocketd-bugag-v5`（18:58 启动）在 8088 | `Test-NetConnection 127.0.0.1 -Port 8088` |
| 后端矩阵 | ✅ **18/20**（与上一轮记录一致） | `node scripts/backend-endpoint-matrix.mjs` |
| 模块可达 | ⚠️ **10/14 RENDERED，4 个 LOGIN_GATED** | `node scripts/verify-modules.mjs` |

18/20 里的两个 404 是**数据为空**不是端点缺失，注意别误报：

- `GET /api/vault/sync/latest` → 404 `no vault for user: no rows in result set`
- `GET /api/marketplace/agents` → 404 `not found`
  （同模块的 `/api/marketplace/packages` 与 `/releases` 都是 200，端点是有的）

### 1.1 🔑 唯一的硬阻塞：真机本地库主密码

`笔记 / 邮箱 / 密码箱 / PKM` 四个模块停在 `/#/login?returnTo=...&unlock=1`，
**根因是本地加密库未解锁，不是模块缺陷**。实测表现：

- 任务页能打开「+ 新任务」面板、能填标题，但 **「创建」按钮是 `disabled`** ——
  这时候记 FAIL 是冤枉产品，记 PASS 是撒谎。脚本已按此分类为 `SKIPPED(GATED)`。

**主密码不要贴进对话，也不要写进仓库。** 放到 gitignore 覆盖的目录里：

```powershell
'你的主密码' | Set-Content -Encoding UTF8 C:\workspace\openpocket\.scratch\pocket-master.txt
```

`.scratch/` 已被 `.gitignore:17` 忽略。脚本按
`$env:POCKET_MOCKET_MASTER` → `.scratch/pocket-master.txt` 顺序读取（前者是 `$env:POCKET_MASTER`）。
**拿到密码后，下一轮第 1 步就是重跑本文件 §2 的脚本**，预计 6 项里能当场关掉 4 项。

---

## 2. 本轮实测结果

命令：

```powershell
$env:POCKET_SERIAL='192.168.31.19:5555'
node scripts/verify-write-paths.mjs            # 全部
node scripts/verify-write-paths.mjs gateway    # 单模块
```

**最终一轮（stamp=9523904）：`PASS=0  FAIL=0  BLOCKED=2  NO_ENTRY=6`。**

> ⚠️ **本轮 0 项通过。六模块写路径缺口一个都没关掉。**
> 下面是事实记录，**任何一条都不要外推成「已验证」**。

| 模块 | 操作 | 判定 | 依据 |
|---|---|---|---|
| 网关 | 新增/编辑/删除 | **BLOCKED** | 既无 POST 也无错误提示，锁态判据不成立，原因待定位 |
| 任务 | 创建/编辑/删除 | **BLOCKED** | 标题已填但「创建」仍 `disabled`；当前页无主密码输入框，锁态判据不成立 |
| 费用配额 | 策略写入 | **NO_ENTRY** | 页面无任何可写控件，只读视图 |
| 实例 | 实例写路径 | **NO_ENTRY** | 只有选择器（当前服务器/功能入口），无新增/编辑/删除 |
| 会话 | 编辑/删除 | **NO_ENTRY** | 列表无编辑/删除/归档控件；会话需在 AI 页创建 |
| 市场 | 安装技能 | **NO_ENTRY** | 列表页无可见「安装」按钮（只有「查看版本」） |
| 密码箱 | 条目 CRUD | **NO_ENTRY** ⚠️ | 本轮记录为「已解锁但未找到写入口」——见 §2.3 的前后矛盾 |
| 邮箱 | 账号/规则写路径 | **NO_ENTRY** ⚠️ | 同上 |

### 2.1 两个 `BLOCKED` 到底卡在哪（下一轮从这里接）

- **任务**：`+ 新任务` 面板能开、标题能填，**「创建」按钮 `disabled`**。
  注意当前页面**没有**主密码输入框，所以「库锁」这个判据不成立 ——
  可能是表单还有别的必填项没满足，也可能是产品侧逻辑禁用。**我没定位到，不猜。**
- **网关**：填完保存后**既无 POST 也无错误提示**。上一轮探针（已删，结论见下）
  曾实测到一次 `POST /api/llm-gateway/nodes` → `400 adminUsername is required`，
  说明**写路径的请求确实发得出、后端校验确实在工作**，400 只是我没填上「Admin 用户名」。
  但**没有拿到 201，所以仍不算已验证。**

### 2.2 判定口径（脚本已内置，别改松）

| 判定 | 含义 | 算「已验证」吗 |
|---|---|---|
| `PASS` | 写请求 2xx **且** 列表里出现了刚造的数据 | ✅ 算 |
| `FAIL` | 脚本自身失败，或后端明确拒绝且非环境所致 | ❌ 不算，且要查 |
| `BLOCKED` | 现象明确但**原因未定**（分不清是环境还是产品） | ❌ 不算，需人工定位 |
| `NO_ENTRY` | 该模块 UI 上确实没有写入口（事实，不是缺陷） | ❌ 不算，需人工确认是否该有 |
| `SKIPPED` | 被主密码锁 / 环境未起 | ❌ 不算 |

**只有 `PASS` 算已验证。** `BLOCKED` 是本轮新增的判定 ——
宁可承认「我不知道」，也不要猜一个原因然后记 FAIL（那是替产品认领一个我没定位的缺陷）。

### 2.3 ⚠️ 同一项在不同轮次判定不一致 —— 本轮最重要的发现

| 项 | 早一轮 | 最终轮 |
|---|---|---|
| 密码箱 | `SKIPPED`（主密码锁，`/#/login?...&unlock=1`） | `NO_ENTRY`（已解锁但无写入口） |
| 邮箱 | `SKIPPED`（同上） | `NO_ENTRY`（同上） |

两轮之间**我没有做任何解锁操作**（主密码始终没拿到），
中途探针还查到 `location.hash=#/finance` 且 `locked=false`。

**结论：本轮设备状态被本会话之外的东西改变了。**
要么是另一个会话正在驱动这台真机，要么是 App 自身状态在无操作时漂移。
两种情况都意味着 —— **本轮所有写路径测量都不可作为定论**，包括那 4 个 `NO_ENTRY`。

下一轮务必：独占设备 → 独占设备 → 再测，且**连跑三轮一致**才写进「已验证」
（沿用上一轮 §4.22.5 的标准）。

---

## 3. 下一轮待办（按性价比排序）

### 3.1 拿到主密码后立刻做（预计一次跑完能关掉 4 项）

```powershell
'主密码' | Set-Content -Encoding UTF8 .scratch\pocket-master.txt
$env:POCKET_SERIAL='192.168.31.19:5555'
node scripts/verify-write-paths.mjs
```

预期从 `SKIPPED(GATED)` 转为 `PASS` 的：密码箱条目 CRUD、邮箱账号/规则写路径、任务创建+删除。
跑完把结果追加到本文件 §2，并把 §3.1 勾掉。

### 3.2 需要人工决策的三个 `NO_ENTRY`（自动化验不了，要产品侧回答）

1. **费用配额**是纯只读（用量汇总 + 策略展示）。产品侧确认：策略本来是否就该可写？
   如果该可写，这是功能缺口而非「已验证」。
2. **实例**页只有选择器。实例的增删改是否在别处（设置页 / opencode-manager）？
   上一轮 §5 把「实例」列进六个待验模块，可能一开始就搞错了模块边界 —— 下一轮先定位再验。
3. **会话**没有编辑/删除入口。上一轮记的「任务/会话的编辑、删除未验证」，
   对**任务**成立（长按卡片），对**会话**可能根本不成立（会话由 AI 页产生、由归档管理）。
   需确认「归档」是否就是会话的删除语义。

### 3.3 `Keystore` 原生插件：本轮**未实现**，是有意为之

上一轮 §5 把它记为「代码欠账，不是环境问题」，这点没错。但本轮**没有**动它，理由：

- 它需要新增 `KeystorePlugin.java`（实现 `CapKeystorePlugin` 的 **12 个方法**：
  `isVaultInitialized` / `setupMasterPassword` / `unlockWithBiometric` / `unlockWithPassword` /
  `lock` / `listEntries` / `getEntry` / `saveEntry` / `deleteEntry` /
  `generatePassword` / `evaluateStrength`）、在 `MainActivity` 注册、补 gradle 依赖，
  最后还要出 APK 装机验证 —— 这是一整轮的体量。
- 更关键：**本轮不具备验证它的条件**（装机验证会触发上一轮记录过的「重装 APK 覆盖配置」问题），
  交出无法验证的原生代码，恰好是这份文档从头到尾反对的行为。

接口契约已经完整写在 `frontend/src/src/native/keystore.ts`（`CapKeystorePlugin`），
下一轮实现时直接照它写 Java 侧即可，不需要重新设计。

### 3.4 已完成：BUG-F 明文后端构建守卫（本轮新增）

`frontend/scripts/assert-no-plaintext-backend.mjs`，已接入 `build`（prebuild）与 `build:fast`。

**与既有守卫的关系（别当重复实现删掉）**：`vite.config.ts` 里已经有
`assertApiBaseForBuild`（BUG-D 守卫），它管的是 **`VITE_API_BASE` 不能为空**；
本轮新增的管的是 **`VITE_API_BASE` 不能是非本机的明文 `http://`**。两者是不同的失败模式：

| 场景 | 谁的守卫拦 | 结果 |
|---|---|---|
| `VITE_API_BASE=http://192.168.31.20:8088` | **本轮新增（BUG-F）** | `prebuild` 退出码 1，vite 未启动 |
| 完全不设 `VITE_API_BASE` | 既有 `assertApiBaseForBuild`（BUG-D） | 退出码 1 |
| `VITE_API_BASE=https://pocket.kxpms.cn` | 都不拦 | ✅ `✓ built in 19.20s`，退出码 0 |

本轮新增守卫的行为（5 种情形均已实测）：

- 扫所有 `VITE_*` 中名字含 `API_BASE|API_URL|BACKEND|GATEWAY_URL` 的变量（避免换个变量名绕过）
- 非本机明文 `http://` → 退出码 1
- 本机/模拟器回环（`localhost` / `127.0.0.0/8` / `10.0.2.2` / `*.local`）→ 放行
- 真机明文联调用 `POCKET_ALLOW_PLAINTEXT_API=1` 显式 opt-in
- 回归测试 `frontend/src/config/__tests__/assert-no-plaintext-backend.test.mjs`，**7/7 通过**

⚠️ 顺带发现（**既有问题，非本轮引入**）：不设 `VITE_API_BASE` 时 `npm run build` 必失败，
这是 BUG-D 守卫的既有行为。已在 origin/main 基线 worktree 上复现同样失败，确认与本轮无关。

---

## 4. 本轮的方法论教训（比结论更值钱）

### 4.1 我把两次「产品正常」误判成了「产品坏了」

1. **点击表达式漏了 `[index]`** → `el` 拿到的是数组不是元素 →
   `TypeError: el.getBoundingClientRect is not a function` → 网关/市场/任务三个模块全部误报 FAIL。
   根因：我把 `tapExpr` 的表达式直接传进 `tap()`，而 `tap()` 内部会再套一层。
2. **填值后不检查返回值** → 「Admin 用户名」没填上（`NOT_FOUND` 被我丢弃），
   保存时后端正确返回 `400 adminUsername is required`，我却记成「网关写路径 FAIL」。
   真相是写路径完全正常，是我没确认自己那一步做成了。

修法已固化进脚本：`mustFill()` / `mustTap()` 断言每一步的返回码，
失败就抛 `验证脚本自身失败：…`，绝不让「我没做成功」伪装成「产品不行」。

**这条与上一轮「后端没有连接日志 ≠ 握手没到达」同源：
先确认自己那步做成了，再判别人的错。**

### 4.2 `NO_ENTRY` 是有价值的结论，但不能反着推断

「页面上没有可写控件」是**事实**，但它**不能**推出「产品没有这个功能」——
功能可能在别的页面、或由后端接口直接提供。下一轮 §3.2 就是干这个的。

### 4.3 单次绿灯不算数

即使下一轮拿到 `PASS`，按上一轮 §4.22.5 的标准，
**至少连跑三轮一致**才写进「已验证」。本轮所有结论都标注了这一点。

### 4.4 别把「观测不到」写成「不存在」

本轮「网关列表命中=true 但 POST=无请求」这个组合当时无法自洽解释，
在判定为缺陷前先做了 §4.1 的诊断，才发现是脚本问题。
**不可复现的观测不���进缺陷列表**（沿用上一轮 §通用教训七）。

---

## 5. 提交前的验证基线（本轮已跑，供下轮对照）

| 检查 | 结果 | 说明 |
|---|---|---|
| `go build ./...` | ✅ 通过 | |
| `go test ./...` | ⚠️ `internal/agent` + `internal/email` FAIL | **与 origin/main 基线完全相同的 17 个失败**，新增的 learning/task-hierarchy 代码**零回归**。均为 POSIX-on-Windows 问题（shell 假设、文件权限位） |
| `npm run typecheck` | ✅ 通过（`vue-tsc --noEmit`） | 含新增 learning 模块 |
| `node --test src/config/__tests__/assert-no-plaintext-backend.test.mjs` | ✅ 7/7 | 本轮新增 |
| `node --test`（前端全量 115 文件） | ⚠️ 723/724 | 唯一失败 `flashcardIo.test.ts` 为既有问题（无扩展名 import 无法被 `node --test` 解析），已在 origin/main 基线复现同样错误 |
| `npm run build`（`VITE_API_BASE=https://…`） | ✅ `✓ built in 19.20s` | |
| `npm run build`（局域网明文 http） | ✅ 正确拦截，退出码 1 | 本轮新增守卫 |
| `npm run build`（不设 `VITE_API_BASE`） | ⚠️ 失败，退出码 1 | **既有的 BUG-D 守卫**，已在 origin/main 基线复现 |

**基线比对的正确做法**：用 `git worktree add --detach <tmp> origin/main` 拉一份干净基线跑同一条命令，
不要凭印象说「应该是既有问题」。本轮对 `go test` 和 `npm run build` 都做了这个对照。
