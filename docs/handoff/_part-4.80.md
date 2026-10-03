# §4.80 真机判据轮：修好本地测试装置本身，以及两次自我推翻（本轮）

本轮没有新功能。干的事是**把真机测试装置修到可信**，顺带推翻了我自己上一轮的两个结论。

---

## §4.80.1 BUG-V5：`start-local-backend.ps1` 让本地后端**一个都起不来**

上一轮的安全整改把 `devPass = "..."` 这个硬编码常量从
`server_assistant.go` 删了（它以明文躺在 8 个受跟踪文件里，
「dev 模式」实际等于「口令公开的 admin 旁路」）。`devBypassCredentials()`
现在没有显式配置就拒绝旁路——这一步是对的。

但 `scripts/start-local-backend.ps1` 没跟着改，还在运行时从 Go 源码正则里抠那个常量：

```powershell
foreach ($line in (Get-Content $goSrc)) {
  if ($line -match 'devPass\s*=\s*"([^"]+)"') { $devPass = $Matches[1]; break }
}
if (-not $devPass) { throw "could not read devPass constant from $goSrc" }
```

**后果**：这个脚本 100% 抛错。而 `maestro-run.mjs` 的 `ensureBackend()`
正是调它——后端一旦挂掉，整套真机装置失去自愈能力。
失败方式还极具误导性：报出来是「could not read devPass constant」，
读的人会以为是环境问题，不会想到**启动器本身已经死了**。

实测（改动前）：

```
[backend] building pocketd ...
could not read devPass constant from ...\server_assistant.go
exit=1
```

**修法**：单一事实来源改成调用者的环境变量，**故意不给兜底**——
`POCKET_AUTH_PASS`（后端真正读的 `cfg.DevAuthPass`）或别名 `POCKET_DEV_PASS`。
没有默认口令、没有回退、没有再从源码抠一次的口子。缺了就打印可执行指引并 `exit 1`。
明确拒绝启动，好过悄悄把口令放回去。

顺带加 `-JwtSecret` 参数（默认仍是原值，行为不变）——BUG-AX 的真机判据需要它，
原因见 §4.80.4。

**证据**（负控 + 正控）：

| | 结果 |
|---|---|
| 负控：不给口令 | `exit=1`，打印指引，**18100 端口占用数 = 0**（没动别人的进程） |
| 正控：给口令 | `pid=26284 ready on 18100`；登录 OK tokenLen=291；真实 token `/api/tasks`=200(408B)；伪造 token=401；无 token=401 |

---

## §4.80.2 设备会自己熄屏，「App 60s 未进前台」却报得像 App 的锅

preflight 那步「等 App 进入前台」在设备熄屏时**必然** 60s 超时，报出来是

```
[preflight] ❌ App 60s 内未进入前台，中止
```

一句指向 App 的报错，真因却是设备在睡觉：此时启动意图发得出去、
进程也真的起来了（`pidof` 有值），但**屏幕上没有任何窗口**。
拿到这句话的人会去查 App——查错方向。

加 `wakeDevice()`：`keyevent 224` + `wm dismiss-keyguard` + `keyevent 82`
+ `svc power stayon true`，在拉起 App **之前**做。
唤醒失败不直接判死（模拟器没有 keyguard 时 `dismiss` 会报错）。

**证据**（先让设备睡下去作基线，再跑）：

```
负控基线 mWakefulness = Asleep     ← input keyevent 223
正控结果 mWakefulness = Awake
```

判据自证也做了：基线本来就是 Awake 的话脚本 `exit 3` 并说明
「本次 PASS 不能证明 wakeDevice 有用」。

---

## §4.80.3 设备侧的 `adb reverse` 是**共享可变状态**，会被并发会话抢回去

这一条值千金，因为它**制造过一条完整的假结论**。

判据把设备 `tcp:18099` 指到我自己拉起的 `18100`，几分钟后再查：

```
host-25 tcp:18099 tcp:18099      ← 已经不是我的 18100 了
```

于是**整整一轮**诊断是对着**并发会话的后端**跑的。抓出来的启动期流量长这样：

```
200  /api/tasks          Authorization=(无)
401  /api/tasks          Authorization=Bearer eyJ…   ← 有效 token 却 401
```

一个"看起来完全合理"的证据。照着它走，下一步就会得出
「App 拿有效 token 也被踢下线，是产品缺陷」——而真因是**我连的不是自己的后端**。

**修法**（写进判据，不再靠人记得）：

1. 判据**自己**建映射（`adb reverse --remove` + `adb reverse`），并核对
   `adb reverse --list` 里确实有目标映射。
2. 每次测量前**现签**一枚新 token，从**设备侧** `curl` 打 `/api/tasks`，
   必须是 200。宿主侧 200 只证明后端活着，证明不了设备走得到它。

第 2 条用「现签」而不是复用会话里那枚 token：换 JWT secret 之后旧 token
**本来就该 401**，拿它探路必然误报成「设备没走到我的后端」——
这个坑我自己也踩了一轮，判据在换 secret 之后当场报了个假警报。

**顺带验掉了挂着的一项**：「reverse 指错端口」分支。
把映射指向一个没人监听的 19999，设备 `curl` 得 `000`；指回 18100 得 `200`。
⇒ 错映射的判别特征是**连不上**，不是超时。自愈逻辑据此可区分「缺失」与「指错」。

---

## §4.80.4 自我推翻之二：「4 个既有测试失败」其实是 6 条 **SKIP**

上一轮我记着「4 个既有测试失败：task 守卫 404/403 期望差 ×2、learning 时间窗 ×2」。
本轮实跑：

```
$ go test ./internal/server/ -count=1
ok   github.com/halfking/pocket-opencode/backend/internal/server   20.156s
```

绿灯。但**不能就此说「已修」**——去翻 `-v`：

```
--- SKIP: TestTaskWriteGuardBlocksPlainMemberPatch (0.00s)
    task_write_guard_route_test.go:131: skip: POCKET_TEST_POSTGRES_DSN not set (PG integration test)
```

6 条全是 `POCKET_TEST_POSTGRES_DSN` 没设而跳过的 PG 集成测试。
**`ok` 那一行绿灯下面藏着 6 个 SKIP，跳过不等于通过。**

把 DSN 给它们真跑一遍：

```
$env:POCKET_TEST_POSTGRES_DSN='postgresql://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$ go test ./internal/server/ -run "TestTaskWriteGuard" -count=1 -v
--- PASS: TestTaskWriteGuardIsNotSilentlyBypassed (0.09s)
--- PASS: TestTaskWriteGuardBlocksPlainMemberPatch (0.33s)
--- PASS: TestTaskWriteGuardBlocksPlainMemberDelete (0.36s)
--- PASS: TestTaskWriteGuardReadableButNotWritableIs403 (0.31s)
--- PASS: TestTaskWriteGuardAllowsOwnerAndParticipant (0.29s)
--- PASS: TestTaskWriteGuardOwnerDelete (0.33s)
--- PASS: TestTaskWriteGuardUnknownTaskIs404 (0.27s)
ok   …  2.177s
```

`learning` 三条也真跑过（无 skip），全过。

所以我上一轮的记录**两处都错**：它们不是失败，是跳过；给了 DSN 它们是通过的。
**教训**：`ok <pkg> <time>` 单独一行不构成任何结论，必须配 `-v` 数 SKIP。

---

## §4.80.5 「/healthz 有应答」不等于「应答的是我起的那个进程」

判据要求后端**换 JWT secret** 来作废一个真实会话。换 secret 之后它报告：

```
换 secret 后同一个 token：宿主侧 /api/tasks=200，设备侧=200
```

旧 token 依然有效 —— secret 根本没换。但脚本说它启动成功了。

根因在 `start-local-backend.ps1` 的老写法：

```powershell
Stop-Process -Id $existing.OwningProcess -Force
Start-Sleep -Seconds 2
# …然后 Start-Process 起新的
```

固定 sleep 2s 是**猜**的。旧进程往往还没释放端口，新进程 bind 失败秒退，
而随后那个 `/healthz` 轮询问到的**是还没死透的旧进程** → 脚本报 "ready"。

**这是一次完整的假绿灯**：调用方拿着一个并不在生效的 JWT secret 去跑 BUG-AX 回归，
跑出来的任何结论都不成立。是我自己加的 anti-vacuity 守卫把它抓出来的
（它明确要求「作废后旧 token 必须真的 401」），否则这一轮就白跑了。

**修法**（两处，都把"猜"换成"查"）：

1. `Stop-Process` 之后**轮询直到端口真的空了**（最多 15s）；仍被占就
   `exit 1` 并说明"再起一个必然 bind 失败，而 healthz 会从残留进程应答"。
2. healthz 通过之后，再查 `Get-NetTCPConnection ... .OwningProcess`
   **必须等于我起的 `$p.Id`**，否则 `exit 1` 并把那个进程的 stderr 尾部打出来。
   对不上时分别说清"它还活着"还是"它已经退了"。

**证据**（换回 SECRET_A 实测）：

```
[backend] port 18100 held by pid 16204, stopping it first
[backend] port 18100 released by pid 16204
[backend] pid=21008 ready on 18100, schema=opencode_pocket, port-owner-verified
新 owner pid = 21008（旧=16204）已更换=True
换 secret 后旧 token /api/tasks = 401   ← 期望 401
```

**注意**：`Join-String` 是 PowerShell 6+ 的 cmdlet，本机是 5.1。第一版把
secret 哈希打进行水里时用了它，直接炸。写 ps1 之前先确认 cmdlet 版本。

---

## §4.80.6 行尾：本仓库有混排 CRLF 的文件，别信 `git diff --stat`

改 `scripts/maestro-run.mjs` 时 `git diff` 报 **949 行变更**。实际只加了 27 行。

原因：该文件在 HEAD 里是**混排行尾**——460 CRLF / 34 bare LF / 2 bare CR。
任何"保存"动作（编辑器、某些工具）都会把整份规范化成 CRLF，于是每一行都算改动。

```
HEAD        : 496 行 / 28353 字节 / CRLF=460 bareLF=34 bareCR=2
规范化后    : 523 行 / 29817 字节 / CRLF=523 bareLF=0  bareCR=0
真实改动    : +27 行
```

**修法**：从 HEAD 字节重建、只插入新增行，行尾分布保持
`487 CRLF / 34 bare LF / 2 bare CR`。校验三样：diff stat、`--ignore-cr-at-eol` 的 diff、字节级行尾分布。

**工具层面的坑**：本轮实测 `edit` 工具会把整个文件规范化成 CRLF（我补一个空行，
949 行的假 diff立刻回来了），而 `write` 工具保持 LF 原样。
⇒ 改这类文件只能用字节级脚本（`readFileSync`/`writeFileSync`），
`edit` 一律不用。这条比 diff 本身更值钱。

**内容没丢，这点是单独验过的**：行数 496→523 净 +27、消失 0 行、
`node --check` 通过。差点因为"949 行"就去查是不是被 stash 卷走了。

---

## §4.80.7 BUG-AX 真机回归：**通过**（对照绿 + 被测绿，exit=0）

这是本轮唯一的功能性结论。判据 `scripts/verify-bug-ax-401-on-device.mjs`
整轮重写了七次才拿到可信结果，下面记的是**判据本身**学到的东西，
因为它的价值比这次通过更高。

### 复现方式：换 JWT secret，而不是伪造 token

原先设想「写入一枚伪造 token → 等它 401」。**实测这条路到不了被测代码**：

- App 启动时 POST `/api/auth/refresh` 做 JWT 滑动续期（`http.ts` 的 `REFRESH_PATH`），
  伪造 token 在**启动期**就被 401 清掉；
- 后端没有吊销/黑名单（`/api/auth/logout` 只撤 RedClaw session，本地 dev 走
  `POCKET_AUTH_LEGACY_ONLY`，没有 RedClaw）；
- 于是 `/api/tasks` 压根不会发出，anti-vacuity 守卫正确报红——
  但那只证明判据不可用，不证明修复。

改成**换 JWT secret**，让一个真实且已在应用内的会话被服务端作废。
这正是 `client.ts:46-52` 注释里点名的触发场景。为了它才给
`start-local-backend.ps1` 加了 `-JwtSecret`。

### 判别式不能用「暂无运行中的任务」

```
TasksView.vue:198
  :title="activeTasks.length > 0 ? '当前筛选下没有任务' : '暂无运行中的任务'"
  activeTasks = tasks.filter(t => t.status === 'active')
```

没有运行中任务是**健康状态**，那句文案照样出现。
用它当 BUG-AX 的症状信号 = **恒真判据**（我第一版就是这么写的，判据全绿而什么都没测到）。

改成：先 `POST /api/tasks {title, status:'active'}` 造夹具，
判别式变成「对照支必须**看见**夹具任务；被测支（401 之后）必须**看不见**」。

载荷是实测出来的，不是猜的：多传 `type:'note'` → 400；`source:'acc'` 是只读的 → 403；
响应码是 **201** 不是 200（判据写死 `!==200` 时把一次成功的创建报成了失败）。

### 执行模型：一次 App 启动 = 一个状态

更早一版把两个分支放在**同一个 App 进程内**交替改 localStorage 再 `reload`，
结果分支串台：有效 token 支没发请求就跳登录，伪造 token 支却拿到 `/api/tasks`=200。
`location.reload()` 之后 CDP 执行上下文可能已重建 ⇒ **写入的上下文和读取的不是同一个**。
这不是调参能解决的竞态，只能改架构：

1. 阶段 1：启动 App → 写入目标 localStorage → **读回自证** → `force-stop` 杀掉
2. 阶段 2：重新冷启动 → 这个进程**生下来就带着目标状态** → 只在这里测量

### 七轮里撞到的判据自身缺陷（每一条都先害出一轮假结论）

| # | 缺陷 | 症状 | 修法 |
|---|---|---|---|
| 1 | 顶层 `return` | `SyntaxError: Illegal return statement` | IIFE 包裹（`Runtime.evaluate` 不是 `eval()`） |
| 2 | 只读 `exceptionDetails.text` | 报「Uncaught」，根因不可见 | 读 `exception.description`（`.text` 恒为 "Uncaught"） |
| 3 | 写完立刻 `force-stop` | token/lastRoute 全是旧值 | 读回自证 + 给 WebView 刷盘留 2.5s |
| 4 | 没等 App 启动逻辑跑完就写 | `WRITE_VERIFY_FAILED`（读回 0 字符） | 先等 8s；写入带 3 次重试 |
| 5 | 冷启动后清空 netLog | 控制支永远看不到 `/api/tasks` | 不清；数据是 TasksView **冷启动挂载时**取的 |
| 6 | 归属校验复用旧 token | 换 secret 后报假 `DEVICE_PATH_MISMATCH` | 每次**现签**新 token 探路 |
| 7 | 夹具响应码写死 200 | 把成功的创建报成失败 | 认 2xx |

### 四道反空洞检查（缺一不可）

1. 写入当场读回自证
2. 必须真的发过 `/api/tasks`（否则"没看见任务"只是"没请求"）
3. 作废后旧 token 必须**从设备侧**真的 401（否则"没跳登录"什么都不能说明）
4. 冷启动后 App 持有的 token 必须仍被后端接受
   （App 会续期，**不能**比字符串——实测冷启动后同为 291 字符但内容已变）

### 结果

```
后端 :18100 登录 ok  token=291 字符
夹具任务已建：id=task-… status=active title=BUGAX夹具-…
[开局] 设备→:18100 归属已确认（设备侧带真 token 访问 = 200）

── 分支1 对照 ──
/api/tasks 响应 = 200 | 200
在登录页 = false    看见夹具任务 = true    token 还在 = 是（291 字符）
判定：✅ 对照成立（判据能区分登录页与应用内，且能看见 200 与真实数据）

── 分支2 被测（换 JWT secret 作废真实会话）──
[换 secret 后] 归属已确认
换 secret 后同一个 token：宿主侧 /api/tasks=401，设备侧=401
本轮 /api/* 全量 = 200 /api/app/check-update | 200 /api/email/accounts |
  200 /api/tasks | 200 /api/sessions | 200 /api/app/check-update |
  401 /api/email/accounts | 401 /api/tasks | 401 /api/sessions | 200 … |
  401 /api/auth/refresh | 401 /api/auth/refresh | 404 /api/auth/sso/status
hash = #/login?returnTo=/ai    在登录页 = true
看见夹具任务 = false            token 还在 = 否（已被清）
判定：✅ 401 走的是 forceReauth，没有被吞成空列表

结论：✅ BUG-AX 真机回归通过（对照绿 + 被测绿）   exit=0
```

对照支**连续三轮可重复**地绿，不是单次侥幸。

### ⚠️ 它**没有**证明的部分

- 终态 hash 是 `#/login?returnTo=/ai`——这是**路由守卫**的重定向形状
  （`routeGuards.ts:120` 用 `returnTo`）；而 `forceReauth()` 设的是
  `#/login?reason=expired`。两条都触发了。
  所以：**端到端行为**（401 后跳登录、清 token、不渲染空列表）已证，
  但**具体是哪一行**兜住 401 的，尚未隔离。
- 缺的负控：把 `client.ts:53` 撤掉重打 APK，确认"空列表 + 卡在应用内 + token 还在"
  真的回来了。**没有这个负控，就不能说"是这一行修好的"**——只能说
  "当前构建的行为符合修复后的预期"。

### 判据自证

对照支的作用就是防止"永远停在登录页"也能通过。
它连续三轮都验证了「能看见 200、能在应用内、能量到夹具任务」，
所以它**有区分力**；被测支又要求 401 + 跳登录 + 清 token + 夹具消失。
两条同时成立才有那个 exit=0。
