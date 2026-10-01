
---

## §4.78 外部审计四条证据缺口的逐条回应 + BUG-V2（本轮）

外部审计给了四条证据缺口。**其中一条经复核是真缺陷，其余三条的定性需要更正。**
本节逐条给出可复现的判据与命令，不采信任何「我记得改过」。

### 4.78.1 回应一：`/api/marketplace/agents` 的 404 —— 成立，审计看到 401 是无 token 情形

审计的观测本身没错（只读探测下拿到 401），**错的是把它当成 404 的反证**。
401 与 404 只有靠「同 token 下的对照路由」才分得开。

当前证据（`node scripts/verify-endpoints-no-device.mjs`，本地 18099，2026-10-01 14:28）：

| 探法 | 结果 |
|---|---|
| 带 token 打 `/api/tasks`（对照） | **200** `{"tasks":[…]}` ⇒ token 有效、鉴权链路通 |
| 带 token 打 `/api/marketplace/agents` | **404** `{"error":"not found"}` |
| **不带** token 打同一路径 | **401** ← 401 的真实来源 |

⇒ **404 成立。** 审计的 401 来自无 token 那一次，两者不矛盾。

同时再次确认生产漂移（本地 vs `https://pocket.itestu.cn`）：

| 路径 | 本地 | 生产 |
|---|---|---|
| `/api/flashcards` | 200 | **404** |
| `/api/flashcards/notes` | 200 | **404** |
| `/api/rss/items` | 200 | **404** |
| `/api/marketplace/packages` | 200 | **404** |
| `/api/chat-agents` | 200 | **500** |
| `/api/marketplace/agents` | 404 | 404 |
| `/api/tasks` | 200 | 200 |

### 4.78.2 回应二：闪卡「新建卡组 → 卡片编辑页」—— **证伪，且我把判据留在了仓库里**

审计报的是「闪卡入口缺陷，『新建卡组』文案却跳到卡片编辑页，只记录未修」。
**复核结论：当前代码不存在该缺陷。** 但这个结论不是靠"我看过"，是靠一条
带自测的判据 `scripts/verify-card-deck-labels.mjs`。

先说事实。两个语义不同的入口用的是**两个不同的 i18n key**：

| 位置 | key | 行为 |
|---|---|---|
| `FlashcardListView.vue:15-18`（外屏/fold 槽位） | `flashcards.list.create` = 「新建**卡片**」 | `goCreate()` → `/flashcards/new` ✅ 自洽 |
| `FlashcardListView.vue:76-101`（有卡组时） | `flashcards.deck.create` = 「新建**卡组**」 | 展开内联建组表单 → `store.createDeck` ✅ |
| `FlashcardListView.vue:45-59`（零卡组时） | 同上 | 内联建组表单 ✅（BUG-U 的修复） |
| `FlashcardEditView.vue:148,155` | 同上 | 建组表单 ✅ |
| `StudyHubView.vue:187,196` | 同上 | 建组表单 ✅ |

**九个语言包逐一对照**（光看 zh-CN 看不出来，所以判据必须逐语言）：

| 语言 | `list.create` | `deck.create` |
|---|---|---|
| zh-CN | 新建卡片 | 新建卡组 |
| zh-TW | 新增卡片 | 新增卡組 |
| en-US | New card | New deck |
| de-DE | Neue Karte | Neuer Stapel |
| es-ES | Nueva tarjeta | Nuevo mazo |
| fr-FR | Nouvelle carte | Nouveau paquet |
| ja-JP | 新しいカード | 新しいデッキ |
| ko-KR | 새 카드 | 새 덱 |
| pt-BR | Novo cartão | Novo baralho |

**判据自带自测**（`--selftest`），对**合成坏样本**跑同一套规则必须转红：

```
语言判据（两 key 同值）              ✅ 转红
视图判据（deck.create 在 form）      ✅ 未误报
视图判据（list.create 绑 goCreate）  ✅ 未误报
视图判据（list.create 挂在建组 form）✅ 转红
自测结论：✅ 判据既能转红也不会误报
```

对真实代码：9 语言 × 3 视图全过，**结论 ✅**。

**口径**：这条判据只覆盖**静态文案与模板绑定**。真机上的实际渲染与点击
仍然需要设备回归 —— 设备不可用，所以「运行时也没问题」这句话我**没有证据**。

### 4.78.3 回应三：Maestro「零安装包、零运行产物」—— 部分成立，已修真正的问题

审计说"真机 Maestro 从未成功执行一次（零安装包、零运行产物）"。
前半句不准确：APK 确实构建过（`app-debug.apk` 34.5 MB，2026-10-01 13:11），
真机证据文件也存在（`logs/real-device-stt-20261001-*/ui-*.xml` 等 uiautomator dump）。
**但后半句指出的问题是真的，而且比"没产物"严重得多**：

> **BUG-V2（P1，假绿）：`scripts/android-apk-fingerprint.ps1` 的 APK 路径是硬编码的
> `C:\workspace\openpocket\frontend\...`，即主检出目录，与它被从哪个 worktree 调用无关。**

这台机器上**两个 APK 都存在**，所以脚本会 **exit 0** 并写出一份看起来很权威的指纹，
连"runbook SHA256 是否 DRIFT"的判断也一起给出 —— **而它描述的是另一棵源码树**。
一个会为错误产物背书的验证闸门，比没有闸门更糟。

修法：

1. 所有路径改为从 `$PSScriptRoot` 推导（`-RepoRoot` 可覆盖），脚本只给自己所在的检出树背书；
2. 指纹报告新增 **git 归因**：commit / branch / 已跟踪脏文件数，报告能落到具体源码状态；
3. APK 缺失改为**硬失败 exit 1**，绝不再静默跳过或回退到别的检出；
4. 工作区脏时在报告里大声提示"这份 APK 无法归因到某个 commit"。

**负控实测**（指向一个没有 APK 的目录）：

```
FAIL - APK missing at ...\frontend\android\app\build\outputs\apk\debug\app-debug.apk
      This is now a hard error on purpose (BUG-V2): fingerprinting some other
      checkout's APK would attest the wrong source tree.
NEGCTL EXIT=1
负控报告是否被写出：不存在 ✅
```

正常跑则正确指到 wt3 自己的 APK，并带上 commit `c3b71df` 与脏文件警告。

**口径**：设备证据目前**不在仓库里**（`logs/` 被 gitignore），
所以审计从仓库侧看不到，这是事实。本轮能做的是让指纹**可归因、可复现**，
而不是把 34 MB 的 APK 提交进 git。

### 4.78.4 回应四：https 回归、Keystore、多处写路径仍未验证 —— 接受，无异议

这一条成立。现状照旧，一句话都没有多说的余地：

- 设备 WebView 无外网 ⇒ **https 设备侧端到端未完成**（服务端侧已验过）
- Keystore 原生插件**未实现**
- 多个写路径、`gateway/*` 六页、`:param` 模板扫描、BUG-AV —— 全部**未验或未修**
- 生产后端落后 5 个端点（见 §4.78.1 表）

### 4.78.5 顺着 BUG-V2 挖出来的另外两个真缺陷：BUG-V3 / BUG-V4

为了把 APK 重建到「可归因」，本轮不得不走官方构建入口
`node scripts/build-mobile.mjs android dev`（`vite.config.ts` 的守卫明令
「任何绕过该脚本的构建都不再受保护」）。**结果这条路径在本机根本跑不完**，
连着挖出两个 Windows 专属缺陷：

**BUG-V3：`cap sync` 步必然失败。**
同一个脚本里，vite build 那步 `spawnSync("npm", …, { shell: true })`，
而 `cap sync` 那步**没带 `shell`**。Windows 上 `npx` 是 `npx.cmd`，
不带 shell 的 `spawnSync` 执行不了 `.cmd`，返回 `status: null` + `ENOENT`，
被下面的检查判成 `cap sync failed (exit=null)`。
坐实（`scripts/probe-npx-shell.mjs`）：

```
platform = win32
npx (无 shell)   status=null  error=ENOENT  stdout=""
npx (shell:true) status=0     error=none    stdout="10.9.8"
```

后果比"构建失败"更糟：**vite 已经构建成功、dist 已更新**，脚本却非零退出，
磁盘上留下一个"改了一半"的产物 —— 正是 §4.76 记的"APK 比源码旧"那类假绿的温床。

**BUG-V4：sanity check 的报错信息在撒谎。**
修掉 BUG-V3 后构建继续走，随即挂在 sanity check：
`expected API base http://127.0.0.1:18099 not found in dist/assets`。
**但那个基址当时就在 dist 里。** 原因：该检查用
`execFileSync("grep", ["-rlF", …])`，而 **Windows 上没有 grep**，抛 ENOENT，
被同一个 `catch` 吞成"没找到"。

这是本项目反复出现的那类失效：一个**指向错误原因**的报错，会把人一路引去查
`VITE_API_BASE`、查 `.env`、查 mode，永远查不到真凶。原作者对 grep 的退出码
（1=无匹配 / 2=出错）考虑得很细，却漏了"工具根本不存在"这一种。

修法：sanity check 改为**用 Node 直接遍历读取** dist，跨平台无外部依赖；
并且把「读失败」与「真没匹配」分成两种报错，绝不再把前者报成后者。
**「没匹配 ⇒ 必须失败」的原意完整保留**（静默跳过检查正是 2026-09-05 空基址
APK 出事的路径）。

**验证**（真实构建路径，不是单测）：

```
[build-mobile] cap sync android
√ update android in 736.67ms
[info] Sync finished in 1.615s
[build-mobile] sanity check passed: http://127.0.0.1:18099 present in dist\assets\index-pYSV8KUS.js
[build-mobile] OK — android/dev (mode=android-dev)
BUILD EXIT=0
```

再 `gradlew assembleDebug` → `BUILD SUCCESSFUL in 24s`。
新 APK：`SHA256 87EC728A82CE3DD3136478CA2BBCAFAF02FADE635B25C0462208DC3B01D77C35`，
34509259 字节，14:43:48，由 `c3b71df` 的前端源码构建
（`cap sync` 改写的 `capacitor.build.gradle` / `capacitor.settings.gradle`
已 `git checkout` 还原，未提交 —— 它们带 worktree 专属路径，提交会弄坏主检出目录）。

**口径**：`dirty=2` 指的是本轮这两个**构建脚本**的改动，不进 bundle；
指纹报告已能区分这件事，但"APK 完全等于某个 commit"这句话要等这两个脚本
提交后再重跑一次指纹才算闭环。

### 4.78.6 本轮新增/修改

| 文件 | 说明 |
|---|---|
| `scripts/verify-card-deck-labels.mjs` **新增** | 闪卡两入口文案/行为一致性判据，**自带合成坏样本自测**；覆盖 9 语言 × 3 视图 |
| `scripts/probe-npx-shell.mjs` **新增** | BUG-V3 的最小坐实脚本（`npx` 有无 shell 的对照） |
| `scripts/android-apk-fingerprint.ps1` | **BUG-V2 修复**：路径仓库相对化 + git 归因 + 缺 APK 硬失败 |
| `frontend/scripts/build-mobile.mjs` | **BUG-V3 修复**：`cap sync` 补 `shell: true`；**BUG-V4 修复**：sanity check 去 grep 化 + 读错误与未匹配分离 |
| `docs/handoff/_part-4.78.md` | 本节片段源 |

### 4.78.7 口径

- 本节**新增的产品/工具缺陷共三个**：BUG-V2（指纹脚本为错误产物背书）、
  BUG-V3（官方 Android 构建路径在 Windows 上必然失败）、
  BUG-V4（构建守卫报错指向错误原因）。三个都已修，前两个有负控，
  第三个由真实构建路径跑通来证。
- 审计的闪卡那条**证伪**，依据是可复现判据而非记忆；判据已入库，**下轮可重跑**。
- 审计的 marketplace/agents 那条**定性更正**：404 成立，401 是无 token 情形。
- **设备仍然不可用**（第 7 次确认，两台都 `offline`），
  本轮真机侧依然**零推进**；`tasks-crud.yaml` 与 BUG-AX 真机回归仍未完成。
  新 APK 已就绪且可归因，**设备一回来即可直接装跑**。
