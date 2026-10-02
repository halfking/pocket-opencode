
## §4.95 BUG-V19（退出码恒 0）+ 新门禁，以及一个刚坐实的 UI 缺陷（BUG-V20，待定位）

### §4.95.1 BUG-V19：4 个脚本判出 FAIL 仍然 `process.exit(0)`

`verify-task-writepath.mjs` 跑出 `exit=0 PASS=8 FAIL=1`。退出码是**写死**的：

```js
const passed = checks.filter((c) => c.pass).length
console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过`)
ws.close()
process.exit(0)          // ← 无论 passed 是多少
```

⇒ CI、批量 runner、`&&` 链**全都无从分辨**「跑过了」与「全绿」——
绿灯是被无条件发出去的。与 BUG-V15（邮件同步探针退出码恒 0）同一类，
只是那次的脚本我改了、这一批漏了。

新增门禁 `scripts/check-exit-reflects-verdict.mjs`（`--selftest` 7/7：
敏感度 2 / 特异度 3 / 变盲 2），判据是「含 `const checks = []` 或 `const check = (…)`
的文件里不允许出现无条件收尾的 `process.exit(0)`」，
首跑就抓出 3 个：`verify-notes-crud.mjs`、`verify-notes-inputtext.mjs`、
`verify-scheduled-task-writepath.mjs`。连同 `verify-task-writepath.mjs` 全部改成
`process.exitCode = checks.some(c => !c.pass) ? 1 : 0`，
复跑门禁 **0 命中**。

**刻意保持保守**：只看行首就是 `process.exit(0)` 的收尾行，且同文件没有
`process.exitCode` 赋值、没被 `if (` 包住。全仓有一百多个 `process.exit(0)`，
绝大多数是合理的（幂等追加器、诊断脚本、早退路径）——门禁宁可漏报也不误报。

### §4.95.2 verify-task-writepath 修判据后：8/9，剩一条判红

顺带修掉另一处判据缺陷：原来 `goto('#/ai')` 之后**立刻**读 DOM，
把「慢」和「不刷新」混成一个结论。改成轮询到 15 秒并打印耗时：

```
FAIL  删除后列表不再回显（轮询至多 15s）  — found=true  耗时=15070ms
      ⇒ 15s 内始终不消失，指向「删除后列表不刷新」
exit=1
```

### §4.95.3 BUG-V20（待定位）：任务删除后，列表三种刷新方式都不更新

新增 `scripts/diag-task-list-refresh.mjs` 做定性，实测（隔离库，2026-10-03 01:58）：

```
播种 LISTREFRESH-523881 -> 201 id=task-bee90b47bdbe2f3238ee52391595a47d
① 删除前刷新一次，列表里能看到            = true
② 服务端 DELETE -> 200；PG = 0            ← 服务端确实删了
③ 删除后（不刷新）仍能看到                = true
④ 离开再回来（没点刷新）仍能看到           = true
⑤ 点刷新（clicked）后仍能看到              = true      ← 连手动刷新都救不回来
```

**服务端是对的（200 + PG 归零），UI 三种刷新方式都不更新。**
这不是等待不足，也不是「缺一个刷新触发」——是**列表读到的数据源**不对。

⚠️ **根因尚未定位，不下结论。** 待查的候选（都需要再验，不能现在就选一个）：
- 本地缓存合并：闪卡那边有过一模一样的坑
  （`stores/flashcards.ts:291` 的 `mergeById(本地, 服务端)` 只做增量合并、
  删除只走 `envelope.deletedIds` 增量通道）。任务 store 若是同一模式，
  被删的项会**从本地缓存里复活**。
- 列表查询的作用域/来源与写入端不一致（写入 `ws_user-admin`，列表读别的）。
- 列表走了不同的接口（`.task-card` 渲染的数据未必来自 `/api/tasks`）。

**下一步该做的判据**（不要只靠肉眼看）：开着 `Network` 域，
点刷新后抓 `/api/tasks` 的**响应体**，看服务端返回里到底还有没有那条。
返回里有 ⇒ 前端合并/渲染问题；返回里没有 ⇒ 请求根本没发到隔离后端。
这一条能把上面三个候选一刀切开。

### §4.95.4 5 个写路径脚本的 origin 硬写已解（§4.94.2 已记）

`verify-task-writepath` 已在解开的条件下实跑（8/9，剩 BUG-V20 那条）。
`verify-email / gateway / marketplace / bug-u / bugaa` **尚未逐个实跑**。

### §4.95.5 本轮新增/修改清单

| 文件 | 变化 |
|---|---|
| `scripts/verify-finance-writepath.mjs` | BUG-V18 证伪判据修复 + sabotage 生效现场确认 |
| `scripts/verify-task-writepath.mjs` | 列表回显改轮询 + 退出码反映判定 |
| `scripts/verify-notes-crud.mjs` / `verify-notes-inputtext.mjs` / `verify-scheduled-task-writepath.mjs` | 退出码反映判定 |
| `scripts/verify-email/gateway/marketplace/bug-u/bugaa-*.mjs` | origin 断言改 env |
| `scripts/verify-marketplace-install.mjs` | API 端口改 env |
| `scripts/migrate-expect-origin.mjs`（新） | 批量迁移，带机械不变量 |
| `scripts/check-exit-reflects-verdict.mjs`（新） | 门禁，selftest 7/7 |
| `scripts/diag-task-list-refresh.mjs`（新） | BUG-V20 定性探针 |
| `scripts/run-device-against-isolated.mjs` | 支持透传 `--` 参数（证伪模式要用） |

### §4.95.6 这一节没有解决什么

- BUG-V20 根因未定位，**不能算已修**。
- 5 个脚本未逐个实跑；约 14 处硬编码 CDP 端口未迁 `lib/adb-cdp.mjs`。
- 闪卡两入口的**点击**、BUG-AX 设备侧负控、会议写入设备侧持久化，仍未做。
