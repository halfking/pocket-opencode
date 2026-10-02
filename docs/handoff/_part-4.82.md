
# §4.82 两条真机 flow 第一次全绿（本轮）

> 承 §4.81：那一轮把两条 flow 的红都定位到了根因，但**一次都没绿过**。
> 本轮绿了。三个根因全是装置自身的缺陷，不是产品缺陷。

## §4.82.0 一句话结论

`smoke-login.yaml` 与 `flashcards-write.yaml` 在 Redmi 2411DRN47C（Android 14 / 720x1640）
上首次 `2/2 Flows Passed`、`exit=0`，且都做过**负控**证明断言有区分力。

```
node scripts/maestro-run.mjs .maestro/smoke-login.yaml        → 2/2 Flows，exit=0（两轮可复现）
node scripts/maestro-run.mjs .maestro/flashcards-write.yaml   → 2/2 Flows，exit=0
负控：POCKET_SKIP_CDP_LOGIN=1 POCKET_ALLOW_UNCERTAIN_START=1
     → [Failed] smoke-login (47s) Assertion is false: "AI 工具" is visible，exit=1
```

## §4.82.1 BUG-V6：CDP 登录块写在了「再走一次路由」之前

写的时候把登录块插在了第 533 行，而「清完 token 之后再走一次路由」在第 582 行。
执行顺序因此变成：

```
resetAppAuth() → CDP 登录 → post-auth 导航
```

而 post-auth 导航那段期望落在 `#/login`，且 `atLogin=false` 时 `exit 3`
⇒ **登录成功之后必然误报 exit 3**。已把登录块整体移到导航块之后。

补丁脚本做了双向自证：替换前断言区域形状确实是「登录在导航之前」，
替换后断言导航行号 < 登录行号；否证不成立直接抛错。
（防的是「补丁没生效却报成功」——这是最坏的失败模式。）

## §4.82.2 BUG-V7：从来没断言过 App 到底在打哪个后端

这条比前两条严重，因为它让**所有健康检查都绿、而结论是别人的**。

事实链：

| 环节 | 内容 |
|---|---|
| 装机 APK 怎么构的 | `frontend/.env.android-dev`，里面是 `VITE_API_BASE=http://192.168.31.20:18099`（**LAN 地址**） |
| 本文件上方三处注释 | 写的是「App 的 API 基址是 `http://127.0.0.1:18099`」——**与事实不符** |
| 实际生效的基址 | `localStorage.pocket_api_base` **优先于**构建默认值（`config/api-base.ts:4`） |
| 那个 key 里是什么 | 2026-10-02 真机读回是 `http://localhost:18099`——**上一轮调试遗留、没人断言过的值** |

⇒ 真缺陷不是「App 不走 reverse」（初版注释就是这么写的，**已被实测否掉**），
而是「**从未断言**」。两种坏法：

- key 缺失 ⇒ 落回构建默认的 LAN `18099` = **同机另一个会话的 pocketd-invoicecheck**（dev 口令不同）
- key 陈旧 ⇒ 指向一个已经没人监听的端口

两种情况下宿主 `/healthz` 200、设备 `adb shell curl` 200、reverse 映射端口正确，
**三道守卫全绿**，而 App 读的是别的后端。dev 口令不同 ⇒ 登录 401 ⇒
「列表恒空但没有任何报错」——就是 §4.80 记的那个差点被当成产品缺陷的现象。

新增 `assertAppUsesReverseBase()`，接进 `preflight()`：

1. 用产品自己支持的开关（设置页「后端服务器」写的同一个 key）写入
   `http://127.0.0.1:<dev>`。`api-base.ts:136-137` 明确：显式填的 loopback
   **不**被 `loopbackBuildRejected` 拒掉，因为「adb reverse 开发流确实需要
   用户主动指定 localhost」——这是设计内的路径。
2. **写入后读回自证**：`setItem` 成功 ≠ 值就是我们要的。
3. `location.reload()`：基址是模块加载期解析的，不重载则页内 fetch 用的还是旧 base，
   而守卫照样拿到 200——又是一次假绿。
4. 用**页内 `fetch`**（App 自己的 WebView + CORS）打 `/healthz` 要求 `200 ok`。
   不用 `adb shell curl`：后者只证明**手机 OS** 能到那个端口，
   两者不是一回事。
5. 日志里打出构建期基址（从本 worktree 的 `.env.android-dev` 读），
   基址对不上时一眼可见。

`POCKET_API_BASE_OVERRIDE=0` 可关闭（只在你确实要测构建期那个 LAN 基址时）。

## §4.82.3 BUG-V8：「再导航一次强制守卫重算」在同路由时是空操作

§4.81 写下的「清完 token 之后再走一次路由」这个手法有个前提没写：
**`location.hash` 必须真的发生变化**。浏览器只在字符串变了才发 `hashchange`。
App 已经停在 `#/ai` 时，`location.hash = '#/ai'` 什么都不会发生 ⇒ 路由守卫不重算
⇒ App 带着一个刚被清掉的 token 继续停在业务页上。

这是**负控跑出来的**，不是读代码看出来的：`POCKET_SKIP_CDP_LOGIN=1` 那轮日志里
`已清登录态但 App 停在 #/ai，没有落到登录页`。

修法：追加一次性 query（Vue Router 的 hash 模式正常解析该 query），
保证目标字符串与当前 hash 必然不同：

```js
await setRoute(`${want}?__recheck=${Date.now()}`, 'true', 5000)
```

顺带修掉同一段的两处自伤：

- 原来无条件 `setRoute('#/ai')`，但未登录时守卫会把它弹成 `#/login?returnTo=/ai`，
  hash 永远不等于 `#/ai` ⇒ `setRoute` 必然空转满 30s 才返回 false。
  判据最后只读 hash，于是照样判「通过」——代价是每轮白等 30 秒，
  而且 `setRoute` 的 ready 判据**压根没起作用**（形同虚设）。
- 改成「已经在登录页就跳过导航」，不在才导航且只给 5s。

## §4.82.4 smoke-login.yaml：删掉一条恒真断言

原第 46-50 行：

```yaml
# WebSocket 连上的外部可观测面：右上角状态胶囊
#   🟢 = 已连上。断连时是 🔴，而这**不会**报错，只会静默变红，所以必须显式断言。
- extendedWaitUntil:
    visible: "🟢"
    timeout: 30000
```

**这个注释是错的，断言也是恒真的**：

- `TasksView.vue:42` 的 `{{ triage.hasAttention ? '🔴' : '🟢' }}` 是
  **任务分诊徽章**（`.triage-pill`，`aria-label` 是「全部正常」/「需要你介入」），
  不是连接状态。
- 且它在「0 个运行中任务」的健康态下**同样**显示 🟢「全部正常 · 0」⇒ 恒真。
- 真正的连接面在 `GlobalStatusBar.vue`，而它的 `visible` 计算
  （`GlobalStatusBar.vue:46-52`）在「在线且无待发队列」时**根本不渲染**
  ⇒ 健康态下压根没有可断言的 UI，只能靠 CDP 读 store。
- 本次实测分诊是 🔴（11 项待介入，见截图）——**旧的 🟢 断言在这份数据上本来就该红**。

换上的三条断言全部对着源码核过（不靠肉眼截图猜）：

| 断言 | 源码依据 | 作用 |
|---|---|---|
| `AI 工具` | `AppLayout.vue:45` `<h1 class="title">{{ title }}</h1>`，`title = route.meta.title`（`AppLayout.vue:149`），`/ai` 的 `meta.title` 见 `router-mobile.ts:80` | 证明路由解析 + 标题渲染 |
| `快速提问` | `TasksView.vue:28` `aria-label` | 证明 TasksView 真挂载，不只是 AppLayout 空壳 |
| `密码登录` 用 `assertNotVisible` | `LoginView.vue:252` Tab 标签 | 会话存活守卫：token 失效时 `client.ts:53` forceReauth 会把 App 弹回 `#/login`，这条就红 |

## §4.82.5 登录改由 preflight 用 CDP 填真实表单

§4.81 已经坐实 Maestro 把 `${POCKET_DEV_PASS}` 展开成**字面量 `undefined`**
（`_probe-env.yaml` 实测：密码框内容 `adminPWLEN-undefined`），而 `--env`
会把口令暴露在进程命令行里。两者都不接受，改由 CDP 直接填真实表单。

CDP 侧的两个要点：

1. **按 placeholder 定位，不按下标**。下标取决于当前 Tab 与指纹区块；
   App 停在「解锁」界面（BUG-AV 场景：已登录但 crypto 未初始化）时会填到**错误的框**，
   而**填错框看起来和填对一样**。定位不到时把页面上真实的 placeholder 全部打出来，
   让报错指向「界面不是登录表单」而不是一句没信息量的「0 个输入框」。
2. Vue 受控 input 必须用 `HTMLInputElement.prototype` 上的 value setter
   再派发 `input` 事件。直接写 `el.value` 不触发 v-model 更新
   （写进去了但状态没变，提交仍是空）。

**换掉的是「谁来敲键盘」，不是「被测什么」**：同一个 `LoginView` 表单、
同一个 `POST /api/auth/login`、同一个 401/200 判定。

⚠️ **如实记录覆盖变窄**：软键盘那套手势（tap 输入框起键盘 → `inputText` →
`hideKeyboard` → tap 提交）两条 flow 都不再走。`_login.yaml` 里
`${POCKET_MASTER}` 那条解锁分支同样没被覆盖——它有和 `${POCKET_DEV_PASS}`
完全相同的展开风险，但本轮没有触发（登录后没出现「解锁本地数据」屏）。
这两项单列为遗留。

## §4.82.6 判据的证据（正控 + 负控 + 独立复核）

**正控**

- `smoke-login.yaml` → `2/2 Flows Passed in 11s`，`exit=0`，连跑两轮一致。
- `flashcards-write.yaml` → `2/2 Flows Passed in 48s`，`exit=0`。

**归属证明**（这是「App 读的到底是不是我这个后端」的直接证据）：

- 截图 `~/.maestro/tests/2026-10-02_222752/smoke-login/takeScreenshot/logs/maestro/smoke-after-login.png`
  里列表是 `Maestro任务` / `probe-a` / `probe-c` / `probe-d` / `BUGAX夹具-*`
  ——**正是本 worktree 开发库的夹具**。同机另一个实例（18099）的数据不会长这样。
- 页内 `fetch` 守卫每一轮都打 `App 内 fetch http://127.0.0.1:18099/healthz → 200 ok`。

**独立复核落库**（不只看屏幕）：

```
flashcard_deck_config = 1   （name=回归卡组）
flashcard_cards       = 1
flashcard_notes       : front=回归正面
```

**负控**（证明断言不是恒真）：

```
POCKET_SKIP_CDP_LOGIN=1 POCKET_ALLOW_UNCERTAIN_START=1
  → flow 真跑到登录页上
  → [Failed] smoke-login (47s)  Assertion is false: "AI 工具" is visible
  → exit=1
```

**夹具自证**：`scripts/flashcards-test-fixture.mjs` 输出
`before [decks|notes|cards|revlog] = 0|0|0|0` → `after` 同为 0，
localStorage 清理读回 `[["flashcards:v1",true],["flashcards:v1:outbox",true]]`。

## §4.82.7 本轮顺手清掉的开发库残留

`opencode_pocket.tasks` 里 11 条 `active` 探针残留，删掉 9 条可归因的
（`Maestro任务` / `probe-a` / `probe-c` / `probe-d` / `BUGAX夹具-*` × 5），
`DELETE 9` 由 `RETURNING id` 自证。

**保留 2 条 `PG matrix probe`**：归属不明，可能是并发会话的，
这张表是共享可变状态，不做无法归因的删除。

⚠️ 踩到的坑：带中文的 `WHERE title='Maestro任务'` 经 PowerShell 传给 `psql` 会
报 `invalid byte sequence for encoding "UTF8": 0xc8 0xce`（GBK 字节）。
改用纯 ASCII 的 id 列表才成功。与 `flashcards-test-fixture.mjs` 注释里
「全部 ASCII，避免 PowerShell/psql 兜底串编码问题」是同一条。

## §4.82.8 遗留（本轮没做，不是有意搁置）

- **软键盘手势路径无覆盖**：两条 flow 都不再走 Maestro 敲键盘。
  要验这条只能单开一条用**错误口令**的 flow（不需要真口令、无泄露面），
  验「点输入框起键盘 → inputText → hideKeyboard → tap 提交」后错误提示可见。
- `_login.yaml` 的 `${POCKET_MASTER}` 解锁分支未覆盖，展开风险同 §4.82.5。
- BUG-AX 设备侧负控、闪卡两入口的渲染/点击、会议写入的设备侧持久化、
  「tap 报 COMPLETED 但没反应」的坐标对账：仍未做。
- `:param` 模板、gateway 六页、生产部署、https 设备侧端到端、Keystore：仍未做。

## §4.82.9 本轮跑法（可复现）

```powershell
$env:POCKET_API_BASE='http://127.0.0.1:18100'   # 宿主后端端口
$env:POCKET_DEVICE_PORT='18099'                 # 设备侧端口（App 用的）
$env:POCKET_DEV_PASS='<口令>'                   # 必须与下面同一个值
$env:POCKET_AUTH_PASS='<口令>'
cd C:\workspace\openpocket\.wt-e2e
node scripts\flashcards-test-fixture.mjs          # 闪卡 flow 前置
node scripts\maestro-run.mjs .maestro\smoke-login.yaml
node scripts\maestro-run.mjs .maestro\flashcards-write.yaml
```

`POCKET_DEV_PASS` 与 `POCKET_AUTH_PASS` **必须是同一个值**：
不一致的表现极具误导性——登录 401 → 任务列表空 → 看起来像「列表功能坏了」。
本轮为此专门用 `scripts/start-local-backend.ps1` 以已知口令重启了 18100，
并实测鉴权三态：真口令 200 + 291 字符 token、`/api/tasks` 200；
伪造口令 401；无 token 401。
