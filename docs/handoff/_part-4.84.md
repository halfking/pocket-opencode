
# §4.84 PKM 写路径 + 登录表单键盘手势（两条新 flow 绿）+ 主密码解锁上移（本轮）

> 承 §4.83。本轮解掉了 §4.83.6 指出的那个具体阻塞（`${POCKET_MASTER}`），
> 让 `notes-crud.yaml` 第一次跑通；并补上了一条**不需要任何口令**的键盘手势回归。
> 另外更正一条我自己此前说错的结论。

## §4.84.0 结论

- **第四条 flow 绿了**：`notes-crud.yaml` → `2/2 Flows Passed in 40s`, `exit=0`。
  PKM 笔记的「创建 → 改名 → 列表回显」在真机上端到端验证，
  且用**独立于屏幕**的方式复核了落库。
- **第五条 flow 绿了**：新增 `login-gesture.yaml` → `2/2 Flows Passed in 26s`,
  连跑两轮。Maestro 那套软键盘手势（tap 输入框 → 键盘 → `inputText` →
  `hideKeyboard` → tap 提交）在**登录表单**上被验证了。
- `${POCKET_MASTER}` 这条坏路径彻底移除：解锁责任上移到 harness。

## §4.84.1 先澄清一件事：那条解锁分支不是死代码

我上一轮写「`_goto-pkm.yaml` 的 `${POCKET_MASTER}` 被阻塞」，但没验证它
**会不会真的被触发**。2026-10-02 探针实测（`scripts/_probe-pkm.mjs`）：

```
hashBefore = #/ai
导航到 #/pkm/today（含一次性 query 强制 hashchange）
hashAfter  = #/login?returnTo=/pkm/today?__probe=…&unlock=1
localStorage: pocket_crypto_cfg、pocket_crypto_salt   ← PBKDF2 盐 ⇒ 主密码设过
unlockVisible: true
inputs: [{ type: "password", ph: "输入主密码解锁" }]
buttons: [ {text:"解锁", disabled:true}, {text:"退出重新登录 →"}, {text:"后端服务器 · http://127.0…"} ]
```

`routeGuards.ts:126-129` 的 `redirectUnlock` 带 `unlock=1`，
localStorage 里有 `pocket_crypto_salt` ⇒ **主密码确实设过，屏幕确实会出现**。
「它没报错所以大概是死代码」这个推断是不成立的——它是被 `undefined` 静默填错
口令，不是没被触发。

## §4.84.2 主密码实测有效，CDP 解锁端到端可用

用 CDP 填 `输入主密码解锁` 并点 `解锁`（`PocketTest2026`，harness 的默认值）：

| 阶段 | 证据 |
|---|---|
| before | 输入框 `len=0`，`解锁` `disabled: true` |
| afterFill | 输入框 **`len=14`**，`解锁` **`disabled: false`** ← v-model 真接到了值 |
| afterClick | **hash 跳到 `#/pkm/today`**，页面出现 `今日 Daily Note` / `MaestroPKM笔记` |

⇒ 主密码有效，且**不需要坐标**。`_goto-pkm.yaml` 的注释说那个框在无障碍树里
`[EditText] t="" cd=""`、只能按 50%,59% 点——那是对 Maestro 而言。
DOM 里它有 placeholder：**可访问性树里没有的东西，DOM 里有**。

## §4.84.3 harness 新增 `ensureLocalDbUnlocked()`

放在**登录块之后**（解锁屏的前提是「已有登录态」，页面上原话「检测到已有登录态，
但本地加密库未解锁」）。三段设计：

1. 导航到 `#/pkm/today`（带一次性 query）逼守卫把解锁屏弹出来
2. CDP 按 placeholder 填 → 点 `解锁` → 等解锁屏消失
3. **再导航回 `#/pkm/today` 自证**：解锁屏**不再出现**。
   少了第 3 步，「解锁屏消失」可能只是换页副作用，下次导航又被弹回来。

### ⚠️ 判「解锁屏在不在」必须用 bodyText

第一版探针用
`document.querySelectorAll('label,div,span,h1,h2')` 找 `textContent === '解锁本地数据'`，
**恒为 false** —— 而同一时刻 `document.body.innerText` 明明以
`解锁本地数据 检测到已有登录态，但本地加密库未解锁。` 开头。

用那个检查当守卫 ⇒ 永远判「已解锁」⇒ 跳过解锁 ⇒ 后面全是不可解读的结果。
**恒为 false 的检查和恒为 true 的一样有害。** 现在用 `bodyText.includes(...)`。

### 解锁会持久

`login-gesture` 那轮日志：`本地库已解锁（#/login?… 无「解锁本地数据」屏）`
⇒ 上一次 run 解的锁，跨 App force-stop / 重新启动仍然有效。

## §4.84.4 起点必须再复位一次（新增的坑）

解锁会把 App 停在 `#/pkm/today`，而三条 flow 的前置都假定在起点路由。
起点复位是在**解锁之前**做的，所以解锁之后必须再复位。
与 BUG-V8 同一个道理：必须制造真实的 hash 变化，守卫才会重算
（`${back}?__afterunlock=${Date.now()}`）。

### ⚠️ 而且要区分「有意不登录」

`POCKET_SKIP_CDP_LOGIN=1`（`login-gesture` 用它，因为那条 flow 就是去测登录屏的）时，
守卫会把 `#/ai` **正确地**弹回 `#/login?returnTo=/ai?…`，
而 `h2.includes('#/ai')` 为 false（那是 `returnTo=/ai`，没有 `#`）
⇒ 被误判成「复位失败」。

修法：`skippedLogin = process.env.POCKET_SKIP_CDP_LOGIN === '1'`，
此时判据改成「落在登录页即正确」。同时 `ensureLocalDbUnlocked()` 在该模式下**跳过**——
没登录时解锁屏要么不出现、要么做了也白做，而且它会把 App 从登录屏带走，
恰好毁掉本轮要测的起点。

## §4.84.5 `_goto-pkm.yaml`：从静默分支改成硬断言

```yaml
- assertNotVisible: "解锁本地数据"
```

原来那段 `runFlow: when: visible: 解锁本地数据` 用的就是坏掉的
`inputText: ${POCKET_MASTER}`。**为什么不再写成条件分支**：
`runFlow when` 在条件不满足时静默跳过，于是「harness 的解锁悄悄回归了」
这件事没有任何人会看见。解锁屏出现就红。

## §4.84.6 `notes-crud.yaml` 首次全绿 + 独立复核

```
node scripts/pkm-test-fixture.mjs
  → {"deleted":[{"id":"ast_muosqyyc_0ggtnz","ws":"ws_user-admin","title":"MaestroPKM笔记"}],"remaining":0}
node scripts/maestro-run.mjs .maestro/notes-crud.yaml
  → [Passed] notes-crud (31s)   2/2 Flows Passed in 40s   exit=0
```

**独立复核**（新增 `scripts/verify-pkm-note.mjs`，只读，不依赖 flow 的屏幕断言）：
PKM 笔记存在**设备本地加密库**的 `local_assets` 表（`kind='note'`），
走 `pinia._s.get('connectivity').runtime.deps.db()` 取 db 实例：

```
{"rows":[{"id":"ast_mur3kclr_zmscrh","workspace_id":"ws_user-admin","title":"MaestroPKM笔记"}]}
```

⚠️ 踩到的：第一版探针用 `app.config.globalProperties.$db` 取 db，取不到。
正确路径是上面那条 pinia 路径（与 `pkm-test-fixture.mjs` 相同），
且 `db.all()` 只接 SQL 一个参数。取不到就静默返回 `undefined` 的那版，
差点被读成「没查到 = 没落库」。

## §4.84.7 新增 `login-gesture.yaml`（不需要任何口令）

### ⚠️ 先更正一条我说错的话

我在 §4.82.5 / §4.83.7 写过「软键盘手势路径无覆盖」。**那是错的。**

`tasks-crud.yaml` 的 `tapOn point 50%,41% + inputText + hideKeyboard`
和 `flashcards-write.yaml` 的 `tapOn point 36%,26% + inputText` **都跑过**，
而且它们各自后续的「按钮由 disabled 变 enabled」断言证明文本**真的落进了输入框**。
所以「表单输入的手势链」一直是被覆盖的。

真正没覆盖的只是**登录表单上**那条链——因为它必须用口令，而 Maestro 的口令传递是坏的。

### 做法：用**错误口令**

零泄露面（不需要真口令，也就不用 `--env`），但完整走一遍
tap → 键盘 → `inputText` → `hideKeyboard` → tap 提交，
判据是「后端回了 401 且错误文案上屏」。

三条判据各自都问过「它恒真吗」：

| 判据 | 为什么非恒真 |
|---|---|
| `{text:"登录", enabled:true}` | `LoginView.vue:100` 是 `:disabled="!username \|\| !password \|\| loading"` ⇒ 它变绿**同时证明两个框都收到了文本** |
| `.*用户名或密码错误.*` | `LoginView.vue:536` 的固定文案，只在一次真实 401 往返后出现 |
| `assertNotVisible: "AI 工具"` | 反向确认没真登进去 |

截图证据（`login-gesture-rejected.png`）：用户名 `admin`、密码框 22 位点、
`登录` 按钮已解禁（实心紫）、错误文案 `登录失败：用户名或密码错误`、
仍停在登录页、底部 `后端服务器 · http://127.0.0.1:18099`（覆盖生效）。

## §4.84.8 负控**没有**复现 ⇒ 我删掉了自己的因果故事

复制 `login-gesture.yaml`，只删掉两次 tap 之间那次 `hideKeyboard`，其余一字不改，
重跑 ⇒ **`2/2 Flows Passed`，exit=0**。

⇒ 在这台设备（720x1640）上键盘**没有**遮住密码框，Maestro 的 tapOn 照样移到了焦点。

所以：
- 保留 `hideKeyboard`（防御性，零成本，让每步不依赖上一步的键盘状态）
- 但 flow 注释里那句「不这样做就失败」**没有证据，已删除**
- 顺带更正：2026-10-02 那次登录失败，已证实的根因是
  **Maestro 把 `${POCKET_DEV_PASS}` 展开成字面量 `"undefined"`**
  （`_probe-env.yaml` 坐实：框内容 `adminPWLEN-undefined`），**与键盘遮挡无关**。
  键盘遮挡是我当时并列的**另一个假设，从未被隔离验证**；负控说它在本设备上不成立。

**负控没复现，是「我之前的解释错了」的信号，不是「负控白做了」。**

## §4.84.9 本轮我自己的两个脚本级失误（补丁自证抓到的）

1. **前提假设错**：补丁脚本假定「post-auth 导航块在登录块之后」，
   被自证当场否掉——真实顺序是导航块在**之前**（那正是 BUG-V6 的修复顺序）。
   **先读结构再写补丁。**
2. **自造语法判据**：用「数全文件花括号是否配平」当语法检查，被自证否掉。
   本文件大量使用模板串（`${...}`）和内嵌在 `cdpEval` 里的页脚本，
   字符串里就有花括号，计数天然不可靠。权威判据是 `node --check`。

另外 `.trim()` 匹配 `}` 会把**内层** `if` 的缩进闭合也算上（登录块里就有），
必须列 0 精确匹配。

## §4.84.10 本轮遗留

- `https` 设备侧端到端回归：未做。
- BUG-AX 设备侧负控、闪卡两入口渲染/点击、会议写入设备侧持久化、
  「tap 报 COMPLETED 但没反应」坐标对账：未做。
- `/api/marketplace` 带有效 token 仍 401（守卫与 `/api/tasks` 不同）：未查。
- `:param` 模板、gateway 六页、生产部署：未做。
- Keystore 原生插件（§4.83.5 已确认全平台不可用）、同步编排层、改密入口、
  gateway 四页、BUG-AV、i18n ~800 条：待产品定范围。
- **`_login.yaml` 现在是孤儿**：`notes-crud`/`tasks-crud`/`flashcards-write`
  都不再 `runFlow` 它了，但它仍含 `${POCKET_MASTER}` 与会话锁相关的逻辑。
  下一轮要么删、要么按新前置重写，别留着让人以为还能用。
