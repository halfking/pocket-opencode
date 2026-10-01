#### 4.74 2026-10-01 12:00~13:15：真机轮次的四处更正与两个新缺陷

这一节记的是**我自己的判据出错**和**当前 APK 与仓库代码不一致**两件事，
外加两个由此暴露的真实缺陷。所有结论都有可复现的命令与真机证据。

##### 4.74.1 更正一：`_login.yaml` 只覆盖了三种起始态，漏了第四种

`LoginView.handleLogin` / `completeAuth` 在登录成功后，若
`!cryptoConfig.cfg.hasMasterPassword` 就 `showMasterPasswordDialog = true`
并**直接 return，不跳 `/ai`**（LoginView.vue:332 / 522）。
于是真机上出现第四种起始态：**登录页 + 「创建主密码」模态框**。

它最阴险的地方是**骗过了原有的全部判据**：登录页在模态框背后，
无障碍树里「输入用户名」照样可见，看起来像未登录态 C；而点「登录」
会再弹一次，永远出不去。实测现场（`~/.maestro/tests/2026-10-01_121046`）：
flow 第一条就 FAILED，导出树里同时有登录页和 `android.app.Dialog t="创建主密码"`。

**已修**：新增 `.maestro/_set-master-password.yaml`，并在 `_login.yaml` 里
**判两次**——D-1 处理「开场就带着弹窗」（上一轮 run 死在弹窗上、弹窗残留），
D-2 处理「刚登录完才弹」。少任何一个都会卡住。
弹窗里三个 input 在无障碍树里 `text` 全是空（`type=password` 且无 content-desc，
placeholder 不进 a11y 树），只能按坐标点：`50%,46%` / `50%,53%`，
坐标来自 720x1640 实机导出（弹窗 `[40,480][680,1160]`，高 680，
视口 1640 → 垂直居中，位置稳定）。收尾硬判据也补了
`assertNotVisible: "创建主密码"`。

⚠️ 必须用**与现有本地库相同**的主密码（`$POCKET_MASTER`），理由见 §4.74.6 BUG-AV。

##### 4.74.2 更正二：设备上的 APK 是**旧的**，我一直在测不是当前代码的产物

追「空列表下 `+ 新任务` 点不动」时挖出来的。实测（`scripts/diag-tap-newtask.mjs`）：

| 投递方式 | 结果 |
|---|---|
| A `Input.dispatchTouchEvent`（最接近真手指） | create-task-form = **0** |
| B `Input.dispatchMouseEvent`（Maestro 合成点击走这条） | create-task-form = **0** |
| C DOM `.click()`（对照组） | create-task-form = **1** |

`elementFromPoint` 在按钮中心返回 `div.refresh-text`——下拉刷新提示文字
（bounds `156,107,204,125`）完整盖住了 `+ 新任务`（`148,104,211,128`）。

我一度以为是漏写的 `pointer-events: none`：源码 `PullToRefresh.vue:284` 明明有，
`frontend/dist` 的构建产物里也有。**但设备上跑的 CSS 是另一个版本**
（`scripts/diag-indicator-css.mjs` 直接读设备 CSSOM）：

| | 仓库源码 / dist 产物 | 设备上实际生效 |
|---|---|---|
| scope id | `data-v-fd017b03` | `data-v-c82569d1` |
| `pointer-events` | `none` | **无此声明**（算出来 `auto`） |
| `height` | `56px` | 无此声明（内联 `height:0px`） |
| 定位方式 | 固定 56px + 位移揭开 | `transform: translateY(-100%)` |

⇒ **不是没修，是设备上的 APK 早于这次修复**。这也印证了 §4.67 里
「当前 APK 从未回归」这条欠账是真的会咬人：我前面几轮的真机结论
有一部分是在测一份**不是当前代码**的产物。

**处置**：重建 `frontend` → `cap sync` → `assembleDebug` → `adb install -r -g`，
之后所有真机结论才建立在当前代码上。

##### 4.74.3 更正三：「列表恒空」有两条环境原因，都极像产品缺陷

PG 里 17 条任务一条不少，App 任务页却显示「运行中 0 / 全部正常」且**无任何错误**。
分两步查到，**每一步都先排除了产品**：

1. **宿主 18099 上没有后端**。`18111` 上跑的是**另一个 worktree** 的 pocketd
   （`C:\workspace\openpocket-wt-stt\backend\.verify-bin\pocketd.exe`）。
   App 配的 API 基址是 `http://127.0.0.1:18099`，那会儿没人监听。
   ⇒ 新增 `scripts/start-local-backend.ps1`（含 `POCKET_AUTH_LEGACY_ONLY=true`，
   否则 pocketd 直接拒绝启动），并把 `/healthz` 轮询写进去——
   「进程在」不等于「后端可用」。
2. **`adb reverse` 指向了别的端口**：`host-33 tcp:18099 tcp:18111`。
   这条最阴险：**设备上 `curl 127.0.0.1:18099/healthz` 照样返回 200**，
   所有健康检查都绿，功能却是空的。必须**比对映射目标端口**才能发现。

⇒ `scripts/maestro-run.mjs` 的 preflight 现在有两道守卫：
`assertBackendUp()`（宿主 `/healthz`）与 `assertDeviceReachesBackend()`
（核对 `adb reverse` 目标端口，并从设备侧 curl 复核）。
**这两条守卫的价值不在于现在，在于它们把一类「所有健康检查都绿、
功能却是空的」陷阱变成了显式失败。**

⚠️ 这两道守卫**还没做负控**（故意把后端停掉、看守卫是否真红），属未完成项。

##### 4.74.4 BUG-AV（P1，未修）：`hasMasterPassword` 丢失会误触发「创建主密码」，无任何防护

`hasMasterPassword` **只是 localStorage 里的 `pocket_crypto_cfg` 标志**
（`stores/crypto-config.ts:13`），后端无记录，Keystore 也不保证有
（`persistMasterSecretIfBound` 只在已绑定生物识别时才写）。
标志一丢（MIUI 清站点数据 / 重装 / 存储回收），登录后必弹「创建主密码」，
而本地 SQLCipher 库**可能已经存在且用旧主密码加密**。

弹窗只有「创建」，**没有「用已有主密码解锁」**。用户若输入新密码：

- `local-db.ts:138` 的 `setEncryptionSecret` 抛错被 `catch` 掉，
  注释写着「这种场景下假定密码一致（用户重启 App 时常见）」⇒ **假设成真**；
- 随后 `cryptoConfig.setMasterPassword()` 照常执行 ⇒ UI 认为主密码已创建；
- 实际 DB 仍用旧密钥 ⇒ 下次解锁要旧密码，而用户已经忘了。

**我没有在真机上复现锁死**（要复现需先让标志丢失且库已加密，代价高），
但代码路径是确定的。**需要产品定夺**：是把「已设主密码」这件事落到
WebView 存储之外（Keystore / 服务端），还是在这个弹窗里补「我已有主密码」的解锁入口。
我没有擅自改加密流程。

##### 4.74.5 BUG-AX（P1，已修代码）：401 被渲染成「空列表 + 全部正常」

`http.ts` 里其实**有** 401 兜底（`forceReauth()`，BUG-I 当时加的）。
但 `api/client.ts` 整个面（`getTasks`/`getTask`/`createTask`…，
任务、会话、实例等模块都在用）走的是 `authFetch`，**完全绕过了那条链**：

```ts
// client.ts:27
async function authFetch(input: string, init: RequestInit = {}): Promise<Response> {
  const response = await fetch(input, { ...init, headers })
  if (!response.ok) {
    // …只把 401 包成 ApiError 抛出去，没有任何「清登录态 + 跳登录页」
    throw new ApiError(response.status, message)
  }
}
```

而 `TasksView.loadTasks()` 把错误 `catch` 掉、`tasks.value = []`
（TasksView.vue:1006-1008），**不给用户任何提示**。
实测证据（`scripts/diag-empty-list.mjs`，设备侧）：

```
tasksStatus: 401   body: {"code":"unauthenticated","error":"invalid or expired token"}
cards: 0
emptyShown: "暂无运行中的任务 / 点击「+ 新任务」创建…"
triageText: "🟢 0"
```

⇒ 死 token 被渲染成「你一个任务都没有」，和 BUG-I 当初描述的死法一模一样。
**BUG-I 的修复只打了一半**：兜底存在，但没覆盖真正被大量调用的那条路。

**修法**：把 `forceReauth()` 从 `http.ts` 导出，`authFetch` 遇到 401 时调用它
（清本地态 + 跳 `#/login?reason=expired`）。改动两处，各一行调用。
⚠️ **代码已改，真机回归要等重建装机后重跑 `tasks-crud.yaml` 才有结论。**

##### 4.74.6 真机 harness 的三条硬规矩（都是实测撞出来的，不是推理）

1. **重排之后不能立刻 tap 坐标会变的元素。**
   最初 `tasks-crud` 是「先 tap 收起（折叠分诊区）→ 再 tap + 新任务」，
   后者报 COMPLETED 但弹窗不开。两者坐标差 ~1100px（y≈1368 → y≈202）。
   WebView **异步发布**无障碍树：收起点完 DOM 已重排、树还没跟上，
   Maestro 取到的仍是旧坐标。
   ⇒ 去掉「收起」就通了。**`retryTapIfNoChange` 救不了**：任务列表里
   「无响应 · 13 小时」这类相对时间持续变化，屏幕永远「变了」，它压根不会重试。
2. **`visible` 会把折叠线以下 2px 的节点判成「可见」。**
   新建的卡片 bounds 实测 `[94,1638][606,1640]`——只有 2px 高，还被底部
   主导航（y≥1496）压着。Maestro 照样把 `visible` 判真，接着的
   `tapOn` 打在 y≈1639 的窄条上，点了个寂寞。
   ⇒ 必须 `scrollUntilVisible` + `visibilityPercentage: 60`，
   把「可见」拉回「用户真看得见」。
3. **登录必须断言输入框的实际值。**
   实测用户名框里躺的是 `administrationundefinedy` 这种脏值，
   现场只剩一句「登录失败：用户名或密码错误」——会把人引去查后端鉴权，
   而真因是这台设备上合成输入被搅坏。
   ⇒ `_login.yaml` 加 `assertVisible: { text: "^admin$" }` 与密码同款断言，
   失败点立刻落在「输入没落进去」上。

另外三条环境事实（**不记成产品缺陷**）：

- Maestro 是 JVM 程序，中文 Windows 下按 **GBK** 往 stdout 写，
  用 Node 的 utf8 读全是乱码——而乱码会让人以为「页面上没这个文案」
  然后去 Vue 模板里猜 placeholder。⇒ `scripts/hier-dump.mjs` 负责解码，
  `scripts/decode-hier.mjs` 会用「登录 / 用户名」这类已知文案自证编码选对了没有。
- 本机跑完 `maestro hierarchy` 之后，WebView 的 devtools socket 会一段时间
  不接受连接，`/json/list` 挂到超时。⇒ 抓树放最后，抓完就别再指望 CDP。
- **我自己的 CDP 封装一度把整条消息当结果回传再读 `.result.value`**，恒为
  undefined，看起来像「CDP 断了」——实际是取值层级错了（响应是
  `{id, result:{result:{value}}}`）。`cdp.mjs` 传的是 `msg.result` 所以它一直是对的。
  **这个误判差点让我把「socket 抖动」当成环境限制、放弃用 CDP 定位问题。**
  现已在 `hier-dump.mjs` / `diag-*.mjs` 里统一修正并写了注释。

##### 4.74.7 本轮新增工具

| 脚本 | 用途 |
|---|---|
| `scripts/hier-dump.mjs` | 导航 + 抓真机 a11y 树 + GBK 解码 + 打印可写进 flow 的选择器 |
| `scripts/decode-hier.mjs` | 按编码读回 hierarchy 输出，并用已知文案**自证**编码选对 |
| `scripts/parse-hier.mjs` | 把 hierarchy JSON 压成可读列表（BOM 容错） |
| `scripts/diag-create-sheet.mjs` | 弹窗出现与否 + 所有控件的 css 矩形 → Maestro point 百分比 |
| `scripts/verify-sheet-a11y.mjs` | 三条独立通道（CDP / 截图 / 树）交叉证明弹窗状态 |
| `scripts/diag-tap-newtask.mjs` | A 真触摸 / B 合成鼠标 / C DOM click 三路分离「点不动」 |
| `scripts/diag-empty-list.mjs` | 空列表定性：token / 接口状态码 / 命中元素一起量 |
| `scripts/diag-indicator-css.mjs` | 读**设备上真正生效**的 CSSOM（用来发现 APK 是旧的） |
| `scripts/start-local-backend.ps1` | 起本 worktree 的 pocketd@18099，轮询 `/healthz` |
| `.maestro/_set-master-password.yaml` | D 态「创建主密码」子流程 |
| `.maestro/_dismiss-system-dialogs.yaml` | 清 MIUI 一次性系统弹窗（实测会在解锁输密码时抢前台） |
| `.maestro/tasks-crud.yaml` | 任务写路径（创建 → 列表回显 → 进详情），**当前仍是半成品** |

##### 4.74.8 本节口径（不夸大）

- 本节所有「已修」都指**代码已改**，**不等于**已在当前 APK 上真机确证。
- BUG-AV 定性为 P1 但**未复现**，且我没有擅自改加密流程，需要产品先定方向。
- `maestro-run.mjs` 的两道新守卫**还没做负控**。
- `tasks-crud.yaml` 还没跑绿：最后一步仍是「进详情后故意失败取树」，
  因为重建装机后才拿到当前代码的详情页结构。
- 「列表恒空」这两条是**环境问题**（后端没起 / `adb reverse` 指错端口），
  不是产品缺陷；但它们暴露出的「401 无提示」是产品缺陷（BUG-AX）。

