
## §4.96 更正 §4.95：BUG-V20 的「已坐实」建立在一个**空对照**上，撤回

§4.95 我把 BUG-V20 写成「已坐实」。这轮查下来，**那个结论的支撑不成立**，本节撤回它。

### §4.96.1 我自己又写了一个没建立被测状态的判据

新增 `scripts/diag-task-list-source.mjs` 想回答那个正确的问题
（「删除后服务端 `/api/tasks` 返回里还有没有那条」）。
头一版**只播种、没删除**，`cleanup` 是在 `finally` 里才跑的
⇒ 查询时那条任务本来就在库里，探针据此打印
「服务端返回里还有那条 ⇒ 不是 UI 问题」。

**一个没先建立被测状态的判据，给出的结论看起来和真结论一模一样。**
加上「先删再查」之后才拿到有意义的那组数据。

### §4.96.2 §4.95 那个「点刷新」对照是空的

§4.95 写「点刷新（clicked）后仍能看到」并据此断定「不是缺刷新触发」。
但 `TasksView.vue` 里**没有** `button[aria-label="刷新"]`：

```vue
<PullToRefresh :on-refresh="handleRefresh" class="ai-hub-scroll">
```

刷新是**下拉手势**（`handleRefresh` → `loadTasks` + `loadSessions` + `approvals.refresh`），
页面里根本没有那个 aria-label 的按钮。我的脚本用
`document.querySelector('button[aria-label="刷新"]')` 去找，
**在别的视图（或旧渲染）里匹配到了同名按钮，点它根本不会触发 `handleRefresh`**。

⇒ 「三种刷新方式都不更新」这句话，**第三种是无效对照**。整条结论的强度塌了。

### §4.96.3 产品的删除路径按代码看是对的

`TaskDetailView.vue` 的 `confirmDelete`：

```js
await api.deleteTask(deleted.id)
// 必须在 push 之前登记：push 之后列表页立刻被激活，
// 顺序反了 consumeListDirty 会读到还没置位的状态。
markListDirty('tasks')
router.push('/ai')
```

列表侧 `loadTasks()` 是**整体替换** `tasks.value`（无 `mergeById` 式合并），
`deleteTask` 也会 `filter` 掉。`TasksView` 在 KeepAlive 名单里、返回时靠
`markListDirty` 触发重取——**这套设计就是专门为「详情页删掉→返回」准备的**，
源码注释里还记着它修过一次同样的问题。

而 `localStorage` 里**没有**任务缓存键 ⇒「本地缓存把被删项复活」这条也被证据否掉了。

### §4.96.4 撤回后的准确表述

已确认的事实：
- 服务端删除是干净的：HTTP 200、PG 归零、`/api/tasks` 不再返回那条。
- 产品的删除→回列表路径，**按代码看是对的**。
- `verify-task-writepath.mjs` 走 UI 详情页删除后，列表里那条确实还在（轮询 15s 未消失）。

**尚未确认的是**：这到底是不是产品缺陷。
`verify-task-writepath` 的删除点击用的是「找文本为 删除/确认删除/确定删除 的按钮」，
而确认弹层是 `useConfirm` 的 BottomSheet、`confirmText` 就是「删除」——
**点到的未必是弹层里那个确认按钮**。它可能只点了个空转，
后面 `sleep(3000)` 就直接查库，看到的「PG 无该行」其实是**上一条**删除的结果。

⚠️ 所以现在有两条互斥的可能，都还没排除：
- (a) 产品缺陷：删除后列表确实不刷新；
- (b) 探针缺陷：确认弹层没点中，脚本测的压根不是删除成功后的状态。

**下一步该做的判据**（不要只靠猜）：
1. 抓删除点击后的 **Network 面板**：`DELETE /api/tasks/{id}` 到底发没发出去。
   没发 ⇒ (b)；发了且 200 ⇒ 才是 (a)。
2. 若是 (a)，再对比 `consumeListDirty('tasks')` 是否被调用
   （在 `list-scene-store` 上挂一个只读计数，删完读一次）。
3. `verify-task-writepath` 的确认点击应改成**按选择器点弹层里的确认按钮**
   （`useConfirm` 的 BottomSheet 容器内的主按钮），而不是全页面文本匹配。

### §4.96.5 这一节的教训

「已坐实」这四个字，我在 §4.95 写下它的时候，手上只有：
一条轮询超时 + 一个**点了个不存在的按钮**的对照。
**一个空对照足以让一条观测看起来像三条。**

⇒ 「我试了三种方式都不行」这类陈述，必须能回答
「**这三种方式各自真的触发了目标行为吗**」。
答不上来就只是「我试了三次」。
