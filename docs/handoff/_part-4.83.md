
# §4.83 任务写路径真机跑通 + BUG-V9（CDP 端口）+ 回应外部审计（本轮）

> 承 §4.82。本轮把 Goal 审计提的 4 条证据缺口逐条用**当前权威证据**复核，
> 其中两条基于过期证据；同时把 `tasks-crud.yaml` 从「半成品」补成真回归，
> 并修掉一个新暴露的装置缺陷。

## §4.83.0 结论

- **第三条 flow 绿了**：`tasks-crud.yaml` → `2/2 Flows Passed in 34s`, `exit=0`。
  任务写路径（创建 → 列表回显 → 进详情）在真机上端到端验证，PG 落库独立复核。
- **BUG-V9 修复并证成**：CDP 转发端口从「随机 9500+rand(300)」改为
  `adb forward tcp:0`（由 adb 分配空闲端口），碰撞从概率事件变成不可能。
- 审计 4 条里 **2 条成立、2 条基于过期证据**，逐条见 §4.83.4。

## §4.83.1 tasks-crud.yaml：从「半成品」补成真回归

这个 flow 此前在文件头自标「**半成品，不要当回归测试**」——第 6 步是一句
永不成立的断言（`visible: "ZZZ_故意失败_导出任务详情页可访问性树"`），
用来导出任务详情页的可访问性树。

2026-10-02 跑一轮拿到证据（`~/.maestro/tests/2026-10-02_224548/tasks-crud/
screen-hierarchy/step-024-*.json`，99 节点），补成真断言：

| 断言 | 作用 | 会不会恒真 |
|---|---|---|
| `visible: "任务详情"` | 证明真的进了详情页 | 否 |
| `assertNotVisible: "\+ 新任务"` | 列表页专属按钮必须消失 ⇒ 证明离开了列表页 | 否 |
| `assertVisible: { text: "Maestro任务.*" }` | **跨页一致性**：列表里点的卡，详情页必须显示同一标题 | 否 |
| `assertVisible: "进行中"` | 状态徽章 | 否 |
| `assertVisible: { text: ".*暂停.*" }` | 动作行是 enabled 的 Button，不是静态文字 | 否 |

**落库独立复核**（不只看屏幕）：

```
opencode_pocket.tasks → task-1790952387600 | active | Maestro任务
```

**负控**：复制整份 flow，只把第 6 步那条标题断言换成 `PG matrix probe.*`
（这条任务确实存在于库里，但它不是本轮建的，详情页显示的应该是 `Maestro任务`），
其余一字不改：

```
[Failed] _neg-tasks-detail (42s) (Assertion is false: "PG matrix probe.*" is visible)
```

⇒ 第 6 步有判别力，不是恒真。负控副本已删除。

### 前置修复 + 新增夹具

原先 `- runFlow: _login.yaml`（含崩掉的 `${POCKET_DEV_PASS}`），
换成与 smoke/flashcards 一致的前置断言（`AI 工具` 可见、`密码登录` 不可见）。

新增 `scripts/tasks-crud-fixture.mjs`，删掉上一轮同名任务。**必需**，理由与
`flashcards-test-fixture.mjs` 同源：Maestro 判 `visible` 只看节点在不在
无障碍树里，**不看它是不是上一轮留下的**，于是残留会让第 5 步假通过——
恰好在最需要它报警的时候不报。本轮实测夹具生效：`before=1 deleted=1 after=0`。

夹具用 ASCII 前缀 `title LIKE 'Maestro%'` 匹配中文标题：带中文的 WHERE 条件
经 PowerShell 传给 `psql` 会报 `invalid byte sequence for encoding "UTF8": 0xc8 0xce`。

## §4.83.2 BUG-V9：CDP 转发端口随机取值会撞

2026-10-02 真机日志里出现：

```
adb.exe: error: cannot bind listener: cannot bind to 127.0.0.1:9528:
  通常每个套接字地址(协议/网络地址/端口)只允许使用一次。 (10048)
[preflight] fetch 守卫未能判定（…），不阻断
```

`cdpEval` / `setRoute` 原来都是 `9500 + Math.floor(Math.random() * 300)`，
撞上已被占用的端口就抛错。**两种后果差别很大**：

- 落在 `assertFetchIntact` 上 → 它 catch 后只打一句「未能判定，不阻断」，
  run 继续（实测 run 仍 `exit=0`）。也就是说**这个碰撞可以完全静默**：
  守卫没跑成，绿灯照出。
- 落在 `assertAppUsesReverseBase` 或 CDP 登录块上 → preflight 直接崩，
  而报错「端口被占用」指向的是装置，看不出「真问题是上次没清干净」。

撞的是**上一轮没清干净的 forward**，或同机另一个会话的 forward——端口是
**共享可变状态**，随机撞上的概率随并发会话数上升。

**修法不是「多随机几次然后重试」**（那只把概率推低，没有取消它），
而是 `adb forward tcp:0`：由 adb 分配一个当前空闲的端口并打印出来。
2026-10-02 实测分配到 `55704` / `59207` 等高位端口，`forward --list` 里确实出现。
碰撞因此从「概率事件」变成「不可能」。仍校验返回值必须是正整数，
否则说明 adb 行为变了，不能拿 `NaN` 去拼 URL。

### 负控：占满旧随机区间

把 **9500–9799 全部 300 个端口占满**再跑 harness：

```
occupied old random range: 300 ports (9500..9799)
forward entries now: 303
[preflight] fetch 为原生实现 ✅
[preflight] 已设 pocket_api_base：http://127.0.0.1:18099 → http://127.0.0.1:18099
[preflight] App 已重载，外壳回来了
[preflight] App 内 fetch http://127.0.0.1:18099/healthz → 200 ok ✅
[preflight] 登录成功，已进入 #/ai
[Passed] smoke-login (2s)   2/2 Flows Passed in 10s
=== EXIT=0 ===
```

旧实现在这个压力下选到空闲端口的概率是 **0/300**，必然失败。
⇒ 这个对照能区分「修好了」与「只是这次运气好」。

⚠️ 复现步骤：`for ($p=9500; $p -le 9799; $p++) { adb -s <serial> forward tcp:$p tcp:1 }`，
跑完 `adb -s <serial> forward --remove tcp:$p`。脚本是 ASCII-only 的
（PowerShell 5.1 把无 BOM 的 .ps1 按 ANSI 解析，中文会变乱码并破坏引号配对——
第一版就因为这个直接语法错误，见 `start-local-backend.ps1` 顶部的同款警告）。

## §4.83.3 我自己犯的两个错（都记下来）

### 1. `$pid` 是 PowerShell 只读自动变量 ⇒ 存活检查恒为「已死」

清理残留 forward 时我写了 `$pid = $matches[2]`，PowerShell 直接拒绝赋值，
`$pid` 一直是**宿主 PowerShell 自己的** pid（26764）。于是检查
`adb shell "test -d /proc/$pid"` 测的是设备上根本不存在的一个 pid，
**恒为「已死」**，于是 5 条 forward 全被删除，包括可能活着的。

实际影响为零——但**不是靠那个检查证明的**，而是靠删除**前**实际读到的
`forward --list`：5 条的目标 pid 是 25501 / 27763 / 28988 / 30595 / 8208，
而当时存活的 App pid 是 **12373**，没有一条指向活进程。

判据自身失效却照样给出了破坏性许可。这类事故的共性是
**「检查通过」与「检查有效」是两件事**。

### 2. 用更差的临时版本覆盖了一个已提交的工具

我把临时写的 `_dump-a11y.mjs` 改名成 `scripts/dump-a11y-text.mjs`，
结果 `git status` 显示 `M` —— **那个文件在 HEAD 里已经存在**
（`07bfd143`，42 行，还能处理目录/多文件）。我以为「schema 字段名不对」
其实是我自己写错了字段（文本在 `attributes.text`，原版读的就是
`o.attributes`）。已 `git checkout HEAD --` 还原，并用原版重跑同一棵树验证：
99 节点，输出更全。

**教训**：`Move-Item -Force` 到某个名字之前先 `git ls-files` 查一下。
自造同名文件是这条路上最常见的静默覆盖。

## §4.83.4 回应外部审计的四条（逐条用当前证据）

| 审计说法 | 结论 | 证据 |
|---|---|---|
| 「真机 Maestro 从未成功执行一次（零安装包、零运行产物）」 | **不成立（过期证据）** | `~/.maestro/tests/` 下 8 次运行目录（最近 22:36），带 `screenshots` / `screen-hierarchy`；设备上 `com.kaixuan.opencode.pocket` 在装（`lastUpdateTime 2026-10-02 20:40:59`）；本会话已实测 `smoke-login` / `flashcards-write` / `tasks-crud` 三条 flow `exit=0` |
| 「闪卡入口缺陷（『新建卡组』跳卡片编辑页）只记录未修」 | **不成立（已证伪）** | handoff §4.78.2 / §4.81 记载已证伪；判据 `scripts/verify-card-deck-labels.mjs --selftest` 已入库可重跑，9 语言 × 3 视图 `exit=0`（提交 `6e89480a`） |
| 「`/api/marketplace/agents` 的 404 无法证实（返回 401）」 | **原结论成立，审计探针未带 token** | 带有效 token：`/api/marketplace/agents` → **404**；同 token `/api/tasks` → 200、`/api/agents` → 200（**token 有效性由此坐实**）；同一 token 不带 Authorization 头 → 401。⇒ 404 是「路由不存在」不是「鉴权失败」 |
| 「多个功能点写路径与 https 回归仍为未验证，Keystore 插件缺失未实现」 | **成立** | 任务写路径本轮已补（§4.83.1）；闪卡写路径 §4.82 已验。https 设备侧端到端、其余功能点仍未验。Keystore 见 §4.83.5 |

附带发现：`/api/marketplace` 带**有效** token 反而返回 **401**，而
`/api/tasks` 同 token 返回 200 ⇒ 这条路由的守卫与 `/api/tasks` 不同
（可能要求租户/角色）。单列为待查。

## §4.83.5 Keystore：全平台抛错的 stub，密码库功能在**任何**平台都不可用

`frontend/src/native/keystore.ts:60-75` 是 `StubKeystore`，11 个方法全部
`Promise.reject(new Error('cap-keystore plugin not available on this platform'))`：

```
isVaultInitialized / setupMasterPassword / unlockWithBiometric / unlockWithPassword /
lock / listEntries / getEntry / saveEntry / deleteEntry / generatePassword / evaluateStrength
```

文件头注释写着「To register the plugin after implementing it natively」——
原生 Kotlin 侧**从未实现**，所以这不是「Android 上退化」，而是
**Web/PWA 与 Android 都不可用**。UI 侧靠 `isVaultInitialized()` 的
availability 做门控（注释里写 "the UI gates the vault feature on
isVaultInitialized() availability"），而该调用本身就是 reject ⇒
门控拿到的是异常，**密码库功能整体不可达**。

这解释了为什么 `_goto-pkm.yaml` / `_login.yaml` 里的「解锁本地数据」分支
要用 `${POCKET_MASTER}`：本地 SQLCipher 的 AES key 走
`crypto.ts:53 initAppCrypto(masterPassword)` 的 PBKDF2 派生，
与 Keystore 是**两条不同的路径**（Keystore 那条在 Web 上是 stub，
但 PBKDF2 那条不依赖插件）。所以 PKM 本地库能加密、能解锁，
而**密码库功能不能**——两者不要混为一谈。

## §4.83.6 阻塞下一条 flow 的具体位置

`notes-crud.yaml` 的前置是 `- runFlow: _login.yaml` + `- runFlow: _goto-pkm.yaml`，
后者第 69 行：

```yaml
- inputText: ${POCKET_MASTER}
```

**与崩掉的 `${POCKET_DEV_PASS}` 是同一个缺陷**。机制已被 `_goto-pkm.yaml`
自己的注释记录（第 16 行）：

```
❌ evalScript: ${location.hash = '#/more'}
   → TypeError: Cannot set property 'hash' of undefined
     说明 evalScript 不在 WebView 的 JS 上下文里跑
```

⇒ Maestro 的脚本插值跑在 **driver 侧**，不在 WebView 上下文；
变量不在 Maestro 的变量域里时就被替换成字符串 `"undefined"`。
`${POCKET_DEV_PASS}` 的实测证据：框内容 `adminadminundefined`。

所以 `notes-crud` 要能跑，必须像登录那样把主密码交给 CDP 填，
而不是继续在 flow 里用 `${POCKET_MASTER}`。这与 §4.82.5 记录的
「软键盘手势覆盖变窄」是同一个取舍的延续。

## §4.83.7 本轮遗留

- `notes-crud.yaml`：被 §4.83.6 的 `${POCKET_MASTER}` 阻塞，未动。
- 软键盘手势路径仍无覆盖（可用**错误口令**单开一条 flow 验，无真口令泄露面）。
- BUG-AX 设备侧负控、闪卡两入口渲染/点击、会议写入设备侧持久化、
  「tap 报 COMPLETED 但没反应」坐标对账：未做。
- `:param` 模板、gateway 六页、生产部署、https 设备侧端到端：未做。
- `/api/marketplace` 带有效 token 仍 401，守卫与 `/api/tasks` 不同，待查。
- Keystore 原生插件：待产品定范围（见 §4.83.5，已确认不是「只缺 Android」）。
