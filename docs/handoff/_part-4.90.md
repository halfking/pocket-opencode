
# §4.90 「写路径能不能往共享开发库写」这个前置：我自己给了答案 —— 3 个自清理 / 6 个不自清理；顺带修掉一处**失败路径不删数据**的污染源

> 承 §4.89。§4.89 说「7 个会写数据的脚本一律没跑，因为它们写的是与另一会话共享的开发库」，
> 把「能不能写」当成一个需要别人拍板的前置。**其实不需要**：能自己判。
> 判据不是「它写了没有」（都写了），而是「**它写的会不会留下**」。

## §4.90.0 结论

- **3 个自清理**（finance 族）：`verify-finance-writepath` / `diag-finance-samescope` /
  `diag-finance-workspace` —— 播完 seed 会 DELETE。**这 3 个可以跑。**
- **6 个不自清理**：`probe-vault-api`、`probe-vault-sync-empty-blob`、
  `probe-gateway-nodes-api`（会建 gateway node）、`probe-email-account-api`（会建邮箱账号）、
  `probe-email-sync-honesty`、`verify-bug-z`（会 submit 3 个 marketplace 版本）——
  **要跑必须先有隔离环境**，本轮不跑。
- **修掉一处真缺陷（BUG-V14）**：那 3 个「自清理」脚本的 DELETE 写在脚本**末尾**，
  **不在 `finally` 里** ⇒ 中间任何抛错都会把 seed 留在**共享**开发库。
  后果不是「自己测试脏了」，是**污染另一会话的基线**。
- 负控证明修完之后异常路径**真的**会删（不是装饰代码）。

## §4.90.1 BUG-V14：自清理不等于「一定会清」

```js
// 修之前（三个脚本同款）
let seedId = null;
try { seedId = JSON.parse(seed.body).id } catch {}
// …中间 200 多行，任何一处 throw 都到不了下面…
if (seedId) { await api(`/api/finance/${seedId}`, { token, method: 'DELETE' }) }
```

这与 **BUG-V10**（`verify-https-prod.mjs` 失败路径不还原覆盖值）**同一类**：
清理写在 happy path 上。而这次的残留物是**共享库里的数据行**，
会被另一会话当成真实数据卷进它的基线——**比 BUG-V10 的「配置值留在生产」更主动地有害**。

修法（三个脚本同款）：

```js
let cleaned = false;
async function cleanupSeed(reason) {
  if (!seedId || cleaned) return;      // 幂等
  cleaned = true;
  const cl = await api(`/api/finance/${seedId}`, { token, method: 'DELETE' });
  console.log(`[cleanup:${reason}] 删除 SEED ${seedId} -> ${cl.status}`);
}
process.on('unhandledRejection', async (e) => { await cleanupSeed('rejection'); process.exit(1) });
process.on('uncaughtException',  async (e) => { await cleanupSeed('exception'); process.exit(1) });
// 末尾也调同一个函数
await cleanupSeed('normal');
```

两个细节：① **`process.on('exit')` 不能 await**，所以钩子要挂
`unhandledRejection` / `uncaughtException`；② 清理函数必须**幂等**，
否则正常路径 + 异常钩子会删两次。

## §4.90.2 负控：证明钩子真的会触发（以及我自己在负控里踩的两个坑）

不碰数据库——`api()` 换成只记录调用的假实现，制造一次未处理 rejection：

```
CLEANED:rejection CALLS=["DELETE /api/finance/SEED-123"]
清理触发次数 = 1（幂等）      异常路径退出码 = 1
```

**做这个负控的过程里我自己写坏了两次，两次都差点让我得出错误结论：**

1. **临时文件与负控脚本同名** ⇒ 它把自己的源码覆盖掉再删掉。
   *自我验证的工具必须把产物写到别处*，否则「工具验证了工具自己」根本不会发生，
   只会更安静地给出假结果。
2. **漏了 `execFileSync` 的 import**，而 `catch` 把这个 `ReferenceError` 吞成
   「stdout 空 + status undefined」——**看起来正好像子进程没输出**。
   判据把自己的失败伪装成了被测对象的结果。修法：`catch` 里**区分**
   「子进程非 0 退出」（`e.status` 是数字）与「我自己的代码抛了」，后者立刻 exit 2。
   修完之后才暴露出第 3 个问题：
3. 我那条断言 `out.includes('DELETE …')` **永远不可能满足**——异常路径清理完立刻
   `process.exit`，`setTimeout` 里的 `console.log` 根本没机会执行。
   *判据里任何依赖「后面还会跑」的前提，都要显式检查那个前提。*

> 三次里有两次是「判据没执行 / 判据看错了地方」，只有一次是实现的问题。
> 这就是为什么负控必须**真的跑**，而不是「看一眼觉得对」。

## §4.90.3 6 个不自清理的脚本：需要隔离环境才能跑

| 脚本 | 写什么 | 留下的东西 |
|---|---|---|
| `probe-vault-api` | `POST /api/vault/sync/` ×2 | vault 同步结果 |
| `probe-vault-sync-empty-blob` | `POST /api/vault/sync/` | 同上 |
| `probe-gateway-nodes-api` | `POST` + `PUT /api/llm-gateway/nodes` | **一个 gateway node** |
| `probe-email-account-api` | `POST /api/email/accounts` | **一个邮箱账号** |
| `probe-email-sync-honesty` | `POST /api/emails/sync` | 同步副作用 |
| `verify-bug-z` | `POST /api/marketplace/submit` ×3 | **3 个 marketplace 版本** |

**建议的隔离方案（未做，等下一轮）**：起一个**独立 PG 数据库**（`POCKET_POSTGRES_DSN`
指过去）+ 一个独立端口的后端，验证跑完直接 drop 库。
这样写路径测试不再需要「用完即删」这种**依赖纪律**的约定——
**纪律靠不住，隔离才靠得住**（本轮这条负控本身就是纪律失效的例子）。

## §4.90.4 本轮遗留

- **6 个不自清理的脚本仍没跑**（见 §4.90.3），它们覆盖的功能点：vault 同步、
  LLM gateway 节点、邮箱账号、邮件同步、marketplace 提交流程。
- 3 个自清理的脚本**代码改完了但还没实跑**——它们都依赖设备 + CDP 通道，
  而设备上的 App 当前指向 **18099（另一会话的后端）**，不是本 worktree 的 18100。
  改 `adb reverse` 是**共享状态**，动手前要先确认对方没在跑。
- CDP / 设备族约 14 个仍硬编码 92xx 端口（与 §4.86 的 138 处同一件事）。
- `default` vs `ws_user-admin` 数据孤岛（§4.26.2 旧项）待设备侧重取 token 核实。
- 22 处硬编码 `MASTER` 兜底；`handleGetInstanceTasks` 疑似死 handler 未修；
  BUG-V11 需生产 env 授权；`POCKET_PROD_PASS` 缺失；`_login.yaml` 孤儿；
  BUG-AX 设备侧负控、闪卡两入口渲染/点击、会议写入设备侧持久化、
  「tap 报 COMPLETED 但没反应」坐标对账、`:param` 模板、gateway 六页；
  待产品定范围 9 项。**「打通所有的功能点」仍不成立。**

### 下一轮建议的第一件事

搭**独立 PG + 独立端口后端**的隔离验证环境（§4.90.3），一次解锁 6 个写路径脚本
和一大批功能点写路径；随后把 3 个已修好清理逻辑的 finance 脚本对着隔离环境实跑，
确认「失败路径也会删」这条不只停在负控里。
