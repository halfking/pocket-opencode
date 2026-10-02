
# §4.88 BUG-V12 收口：32 个探针从「必然拿不到 token」改成「拿不到就响亮退出」；顺带挖出第二处标签说谎的断言

> 承 §4.87。§4.87 定位了「32 个脚本刮一个已被删除的常量 ⇒ 它们的 401 输出全是没登录」，
> 本轮把这件事**真的修完**，并用双向对照证明修完之后它们**真的能验功能点了**。
> 另外，脚本一活，就露出了里面一处**标签与判据不符**的假 PASS。

## §4.88.0 结论

- **32/32** 脚本改走 `scripts/lib/dev-pass.mjs` 的 `requireDevPass()`。
  门禁复测：`scrape-dev-pass` **32 → 0**（总发现 54 → 22，剩下的全是 `hardcoded-fallback`）。
- **契约是「响亮失败」**：缺口令时打印可操作说明并 `exit 2`，且**在碰设备/发请求之前**。
  双向对照实测：负控 `exit 2` + `DEV_PASS_MISSING`；正控真登录成功、`exit 0`。
- 新增 `scripts/migrate-dev-pass.mjs`（`--dry` 优先、逐文件 `node --check` + 失败即回退、
  UTF-8 往返自证、只认一种形态、匹配不上就**跳过并报告**而不「尽力改一下」）。
- **迁移让一个此前完全死掉的脚本活了，于是露出它里面一处假 PASS**（见 §4.88.3）——
  这本身说明「让探针能跑」不是整理代码，是**把判据重新暴露在证据下**。

## §4.88.1 为什么「响亮失败」比「能登录」更重要

32 个脚本原来的失败是**静默**的：空口令 → 401 → 没有 token → 后面每条探测都按未鉴权跑
→ 输出里一片 401 → **看起来像一份正常的探测报告**。§4.87 已经说明那份输出被当成了
三轮的证据。所以真正要修的不是「让它们能登录」，而是**让「拿不到 token」变成一件显眼的事**。

```js
// scripts/lib/dev-pass.mjs
const VARS = ['POCKET_AUTH_PASS', 'POCKET_DEV_PASS', 'POCKET_MASTER']
export function requireDevPass(opts = {}) { ... exit(2) ... }
```

优先级理由：`POCKET_AUTH_PASS` 是后端 dev bootstrap 真正用的那个（`config.go:101`），最贴切；
`POCKET_MASTER` 排最后，因为它是 App 的主密码，**不要**在探针日志里打出来。

判别力自测（6 项，含一个恒真陷阱）：

| 场景 | 期望 | 结果 |
|---|---|---|
| 有口令 | 返回该值 | ✅ |
| 只有 `POCKET_MASTER` | 也能取到 | ✅ |
| 无口令 | `exit 2` | ✅ |
| 无口令 | 打印说明且含 `DEV_PASS_MISSING` | ✅ 15 行 |
| **`POCKET_AUTH_PASS=''`（空串）** | **仍算「没有」** | ✅ `exit 2` |
| `devUser()` | 读 env，带缺省 | ✅ |

> 空串那条是刻意加的：`process.env.X || fallback` 那种写法里，**空串和没设是两种情况**，
> 而 `if (env[k])` 恰好都能识破；但只要哪天有人改成 `env[k] ?? fallback`，空串就会漏过去。

## §4.88.2 迁移工具自己踩的坑：一条正则，两种句式

第一版迁移跑完是 **18 成功 / 14 失败并回退**。失败的 14 个不是「这些文件特殊」，
而是**同一条正则**对**有无分号**两种情况处理不一致：

```
原文（无分号）：
  const devPass =
    (readFileSync(…).match(/devPass…/) || [])[1] || ''
                      ← 这里的 \s* 把后面的换行符也吞了
替换后：
  const devPass = requireDevPass()function api(path, token, …) {
                      ^^^^^^^^^^^^^^^^ SyntaxError
```

修法：尾部由 `\s*` 改成 `[ \t]*`（只吃行内空白）。**同时加了一条不变量**：

```js
function assertNoTrailingNewline(span, file) {
  if (/\r?\n$/.test(span)) throw new Error(`MIGRATE_BUG_REGRESSION：${file} 的匹配片段以换行结尾…`)
}
```

为什么不只靠 `node --check` 兜底？因为它只在 14/32 上触发，**看不出「为什么有的成功有的失败」**。
把「匹配片段不得以换行结尾」写死成机械检查，下次改这条正则会立刻响。

修完复跑：14/14 成功，`scrape-dev-pass` 归零，`node --check` **32/32 通过**（逐个独立复验）。
迁移后 32 个文件都留下了未使用的 `readFileSync` import，工具第二遍一并清理（同样带回退）。

## §4.88.3 脚本一活，露出的假 PASS：`/api/opencode/instances/stats`

正向对照跑 `probe-instances-api.mjs` 时看到这一行：

```
PASS  /api/opencode/instances/stats 可达（非 404/501/503）  — status=404
```

**标签说不许 404，状态码就是 404，却判了 PASS。** 查下去是两层问题：

1. **判据与标签不符**（原第 96 行）：
   `check('…（非 404/501/503）', stats.status < 500, …)` —— `status < 500` 只排除了
   501/503，**根本没排除 404**。标签在撒谎，而 PASS 把谎言印成了绿灯。
2. **它打的 URL 根本不存在**：真实路由是
   `GET /api/opencode/instances/{instance_id}/stats`（`server_opencode.go:228` 的注释写明），
   **必须带 instance_id**。`handleOpenCodeInstanceOperations` 用
   `len(path) > 6 && path[len(path)-6:] == "/stats"` 分发，裸 `stats` 只有 5 个字符，
   进不去这个分支 → 落 default → 404。

⇒ **404 是正确行为，坏的是测试。** 实测四种形态：

| 请求 | 状态 | 解读 |
|---|---|---|
| `/api/opencode/instances/stats`（缺 id） | 404 | 正确：路由要求 id |
| `/api/opencode/instances/demo-main/stats` | 500 `instance not found: demo-main` | 到了 handler；该实例的 OpenCode API base 未配置，属**环境** |
| `/api/opencode/instances/nope-not-real/stats` | 500 同上 | handler 不区分实例真伪，都是「取不到 API base」 |
| `/api/opencode/instances/demo-main/tasks` | **404** | `handleGetInstanceTasks`（`server_opencode_discovery.go:78`）有文档，但 dispatcher 只特判 `/stats` 后缀，**它从未被分发到** —— 疑似死 handler |

改法：拆成两条断言，**让标签和判据对上**，并用 `/api/instances` 列表里**真实的**
`instance_id`（不再自造）：

```js
check('缺 instance_id 的 /stats 返回 404（路由要求 id，非缺陷）', noId.status === 404, …)
const reachedHandler = ![404, 501, 503].includes(withId.status)
check(`/api/opencode/instances/${id}/stats 过路由匹配（非 404/501/503）`, reachedHandler,
  `status=${withId.status}` + (/* 500 = handler 到了但 API base 未配置，关键是不是 404/501/503 */))
```

改后 **13/13 通过**。附带一条事实写进注释：**前端全仓不引用 `opencode/instances`**
（grep 无命中），所以这组断言是「后端契约」级别，不是「App 用得到」的级别。

> 这一条印证了一件事：**死脚本里藏着的坏判据，只有在脚本能跑之后才会暴露。**
> 之前它从没红过 —— 不是因为它对，是因为它根本没跑到那里。

## §4.88.4 双向对照（这是「修好了」的证据）

```
负控（不设任何 POCKET_* 口令）
  $ node scripts/probe-instances-api.mjs
  ❌ DEV_PASS_MISSING —— 没有 dev 口令，**不继续跑**。
  exit=2          ← 且在发任何请求之前就退了

正控（POCKET_API_PORT=18100 + POCKET_AUTH_PASS=…）
  登录成功
  PASS  阴性对照 A：未鉴权 401（探针能区分 401）  — status=401
  PASS  阴性对照 B：随机实例子路径 404            — status=404
  PASS  列表端点 200 — {"instances":[{"id":"demo-main",…}]}
  PASS  每条实例都有 id/displayName/environment（UI 不会渲染出 undefined）
  PASS  since 过滤生效（未来的时间戳应滤掉全部）    — n=0
  PASS  since 过滤不是「恒空」：过去时间戳仍返回全部  — n=1
  PASS  DELETE /api/instances 被拒                — status=405
  PASS  PUT    /api/instances 被拒                — status=405
  PASS  缺 instance_id 的 /stats 返回 404
  PASS  /api/opencode/instances/demo-main/stats 过路由匹配（非 404/501/503）
  13/13 通过      exit=0
```

`since` 那两条是**双向**的：只测「未来时间戳 → 0」会被恒空骗过去，加上
「过去时间戳 → 1」才排除了「这个过滤根本没接上」。

> 附一条我自己的测量错误：第一次数 FAIL 时用 PowerShell `Select-String 'FAIL'`，
> 它**大小写不敏感**，匹配到了响应体里的 `get instance API base **failed**`，
> 于是报「1 个 FAIL」。脚本其实 13/13 全过。**是我的计数方法错了，不是脚本坏了** ——
> 与 §4.87 记的「命令没跑起来 / 判据不判它」同一个家族：先确认工具在做什么，再读它的结论。

## §4.88.5 本轮遗留

- **22 处 `MASTER = process.env.POCKET_MASTER || 'PocketTest2026'` 硬编码兜底未清。**
  门禁 `check-dev-pass-sourcing.mjs` 已能报出，但**清理时要一并决定 `POCKET_MASTER`
  是不是该继续当 dev 口令用**（它是 App 主密码，语义上不该）。清完才能动
  `backend/internal/repohygiene/secrets_test.go` 的 `password-literal` 规则（§4.87.2 的盲区）。
- 其余 31 个已迁移脚本**只验了语法，没逐个实跑**——它们多数硬编码 8088 等已不用的端口，
  要逐个跑得先统一 base。这条别当成「32 个都验过了」。
- `handleGetInstanceTasks` 疑似死 handler（§4.88.3 第四行）：有文档、有实现，
  但 dispatcher 不分发。**未修**，也未确认前端是否需要它。
- §4.86.5 / §4.87.5 其余遗留照旧：138 个 `.mjs` 硬编码 CDP 端口；两道门禁均未接 `gates`
  （需先改基线棘轮）；BUG-V11 需生产 env 授权；`POCKET_PROD_PASS` 缺失；`_login.yaml` 孤儿；
  BUG-AX 设备侧负控、闪卡两入口渲染/点击、会议写入设备侧持久化、
  「tap 报 COMPLETED 但没反应」坐标对账、`:param` 模板、gateway 六页；待产品定范围 9 项。

### 下一轮建议的第一件事

把 §4.88.5 的两件事按顺序做：① 用 `POCKET_API_BASE`/`POCKET_API_PORT` 把已迁移脚本的
base 统一到 env，然后**逐个实跑**，把「语法通过」升级成「验过功能点」；
② 决定 `POCKET_MASTER` 的去留，清掉 22 处硬编码兜底，再动 Go 侧规则。
