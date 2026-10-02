
## §4.107 BUG-V20 定案：是**真产品缺陷**，不是探针假象

§4.106 留了两条互斥可能并说「都不算已坐实」。这轮把两条都排掉了。

### §4.107.1 先给共享 helper 补上 `send` / `on`

`lib/adb-cdp.mjs` 原来只暴露 `ev`（= `Runtime.evaluate`），
**抓不到 CDP 事件**。要开 `Network.enable`、订阅 `Network.requestWillBeSent`，
就得自己再搭一遍 WebSocket。现已补上：

- `send(method, params)` —— 返回**整个 result 消息**（不是 `result.result`），
  因为 `Network.getResponseBody` 的载荷在 `result.body`，只回传内层会整个丢掉。
- `on(method, handler)` —— 订阅事件，返回退订函数；`close()` 时清空。

### §4.107.2 排除 (b)：DELETE 请求**确实发出去了**

新增 `scripts/diag-task-delete-network.mjs`：播种 → 进详情页 → 点 `.action-btn.delete`
→ **按选择器**点确认按钮（不再全页面文本匹配）→ 抓网络。

```
点 .action-btn.delete = clicked
确认弹层 = {"dialog":true,"title":"删除任务","footerButtons":["取消","删除"]}
点确认按钮 = clicked:删除
PG = 0
   req  DELETE  /api/tasks/task-902c653b85f914aa682f32e38a2070ea
判定：DELETE 已发出 ⇒ 不是「探针点空了」
```

顺带确认了弹层结构：它**不是** BottomSheet，是 `Dialog`
（`ConfirmDialog.vue` → `Dialog.vue`），footer 里 `["取消","删除"]`，**确认是最后一个**。

⚠️ 那个判定行里「响应 200」是**我自己的判据错**：
`seen.find(s => s.kind === 'res')` 抓的是数组里**第一个**响应（详情页 GET 的 200），
不是 DELETE 对应的那个。事实只有两条：**`req DELETE` 存在**、**PG = 0**。
⇒ **「200」这个字我不采信，结论不依赖它。**

### §4.107.3 用修好的探针重跑 verify-task-writepath：缺陷复现

把 `verify-task-writepath.mjs` 的确认点击从**全页面文本匹配**
改成**按选择器点 `.dialog .dialog-footer` 里的最后一个按钮**，
并加一条硬闸：点不中就 `exitCode = 8` 并打印「本轮删除判据全部作废」。

复跑（隔离库 18101）：

```
点确认按钮 = clicked:删除
PASS  删除后 PG 无该行              — count=0
FAIL  删除后列表不再回显（轮询至多 15s） — found=true  耗时=15015ms
8/9 通过
```

⇒ **通过 UI 详情页删除任务，服务端删除成功（PG 归零），
返回列表后那张卡片仍然显示，15 秒不消失。**
**(a) 成立：这是真产品缺陷。**

### §4.107.4 链路按代码看是完整的，缺口在链路内部

```js
// TaskDetailView.confirmDelete
await api.deleteTask(deleted.id)
markListDirty('tasks')     // 注释明说必须在 push 之前
router.push('/ai')

// TasksView
useListScene('tasks', handleRefresh)   // onActivated → consumeListDirty → handleRefresh
async function handleRefresh() { await Promise.all([loadTasks(), loadSessions(), approvals.refresh()]) }
async function loadTasks() { … tasks.value = (await api.getTasks(undefined)) || [] … }   // 整体替换，无合并
```

设置端、消费端、替换语义**都对**。所以缺口只可能在这三者之间：

- `onActivated` 没有真的触发（KeepAlive 名单命中、但返回时组件状态与预期不同）；
- 脏标记被**别人先消费掉了**（`consumeListDirty` 是 delete-and-return，
  若有第二处 `useListScene('tasks', …)` 先跑，它就把标记吃掉了）；
- `handleRefresh` 跑了但 `loadTasks()` 拿到的东西仍含那条（已排除：接口返回里没有）。

### §4.107.5 下一步那一条判据（工具已就位）

`lib/adb-cdp.mjs` 现在能订阅事件了，所以**一条观测就能切开**：

> 返回列表后，`GET /api/tasks` **有没有发出去**？
> - 没发 ⇒ `onActivated`/`consumeListDirty` 这段没生效（脏标记被吃或没触发）
> - 发了 ⇒ 接口返回里已无该条（已证），那么卡片还在就是**渲染/计算属性**层的问题

同时可以顺手在页面上挂一个只读探针：
`peekListDirty('tasks')` 在 push 前 / onActivated 后的取值，
以及数一下 `useListScene('tasks', …)` 在代码里到底有几处注册
（`consumeListDirty` 是 delete-and-return，**多处注册会互相抢**）。

### §4.107.6 这一节的净结论

- BUG-V20 **已确认存在**（服务端对、UI 不更新），**尚未定位到具体那一行**，**未修**。
- 本轮修的是**探针**：确认点击从文本匹配改成选择器，并加了「点不中即作废本轮」的硬闸。
  在这个硬闸之前，那条 FAIL 的可信度是打折的。
