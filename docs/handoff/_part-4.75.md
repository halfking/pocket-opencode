#### 4.75 2026-10-01 13:00~13:50：§4.74 之后又查出的五件事，以及 tasks-crud 的真实状态

本节是对 §4.74 的补充与更正。**先说结论：`tasks-crud.yaml` 至今没有跑绿**，
下面记的是查到哪里、卡在哪、以及哪些是已确证、哪些还只是假设。

##### 4.75.1 「tap 报 COMPLETED 但没反应」的真正机制：陈旧的无障碍坐标

§4.74.6 第 1 条当时只归因到「先点收起导致重排」。继续查发现**同一个坑还有第二种触发方式**，
而且第二种更隐蔽：**任务数据是异步到达的**。

登录后任务页先渲染空态（`+ 新任务` 在 y≈208），数据到了之后列表撑开、
按钮整体下移到 y≈1368，**差约 1100px**。而 `extendedWaitUntil: visible "\\+ 新任务"`
可能在**空态**就满足了，紧接着的 `tapOn` 拿到的是数据到达**之前**的坐标。

已加 `waitForAnimationToEnd` + `assertVisible` 兜底，**但没有稳定治好**：
13:16 / 13:36 / 13:49 三次里仍有两次弹窗没开。
⇒ 目前**只能说是「高度疑似」，不是已确证**：我没有做到在 tap 的同一瞬间
抓一次 a11y 树来证明「Maestro 用的是旧坐标」。

**这条线索本身价值很高**（任何「异步加载 + 固定坐标」的 Maestro flow 都会踩），
下一轮应当先把机制钉死再写 flow，方法见 §4.75.5。

##### 4.75.2 Maestro **不会在选择器里展开 `${VAR}`**（实测两次）

写「断言密码框里的值」时加了一条：

```yaml
- assertVisible: { text: "^${POCKET_DEV_PASS}$" }
```

结果 Maestro 把它变成字面量 **`^undefined$`**，断言必然红。
为排除「变量没传过去」，在启动器里加了一行**只打长度、不打明文**的自检：

```
[preflight] 注入子进程：POCKET_MASTER=14 字符 / POCKET_DEV_PASS=14 字符
```

⇒ 变量确实到了子进程；**同一变量写在 `inputText:` 里能正常展开**（登录确实用对口令了），
**写在选择器里不行**。这是 Maestro 侧的行为，不是我的注入问题。

**已改**：换成不含变量的等价判据 `assertVisible: { text: "登录", enabled: true }`
——两个字段任一为空时登录按钮就是 disabled，所以它同样证明了「都填进去了」。
⚠️ 这是**降低判据精度**的取舍，不是等价替换：值断言能发现「串了字符」，
enabled 断言只能发现「有内容」。用户名那条值断言保留（`^admin$` 不含变量，实测可用），
所以「输入被搅坏」这个风险仍被部分覆盖，但密码字段的字符级正确性不再被断言。
下一轮若要恢复，得先找到 Maestro 侧可用的展开方式。

##### 4.75.3 `spawnSync` 会被孙进程继承的管道句柄拖住（卡了 3 分钟）

让 preflight 自动拉起后端时踩的：ps1 内部用 `Start-Process` 拉 pocketd，
那个孙进程继承了 spawnSync 的 stdout/stderr。`spawnSync` 默认 `stdio:'pipe'`，
于是它一直等这些管道关闭 —— **表现是「后端明明已经起来了（/healthz 200），
preflight 却卡住不动」**。改成 `stdio:'ignore'` 立刻返回。

**已修**（`ensureBackend()`）。

##### 4.75.4 后端 JWT secret 必须固定，否则每次重启都在作废设备上的 token

原本 `start-local-backend.ps1` 每次生成随机 `POCKET_JWT_SECRET`，形成死循环：
重启后端 ⇒ 设备上 token 全部作废 ⇒ App 每个请求 401 ⇒ 任务列表恒空、
「创建」点下去没反应 ⇒ **看起来像 tasks 写路径坏了，真因是环境**。
改成固定 secret（仅限本机 dev；脚本注释里写明共享/生产绝不可用）。

配套地，`maestro-run.mjs` 的 preflight 现在**每次 run 都清掉 App 登录态**，
逼它真实走一遍登录（而不是带着一枚可能已作废的 token 静默跑）。
关掉：`POCKET_RESET_AUTH=0`。

**这一整套（守卫 + 自愈 + 清登录态）是本轮最有复用价值的产出**：
它把一类「所有健康检查都绿、功能却是空的」陷阱变成了显式失败。
⚠️ 守卫的**负控还没做**（故意把后端停掉／把 reverse 指错，看是否真红）——
实际上它**误报过一次正面**：13:41 那轮正是靠它拦下 `adb reverse` 被改回 18111，
所以「能拦住」这件事有正面证据，「不会误伤」还没有。

##### 4.75.5 下一轮该怎么把 §4.75.1 钉死（不要直接改 flow 试）

用 Maestro 自己的日志做交叉验证，**不要靠猜**：

1. 跑一次失败 run，读 `~/.maestro/tests/<ts>/tasks-crud/logs/maestro.log`，
   找到 `Tap on "\+ 新任务" RUNNING` 那一行里 Maestro **自己打印的**
   `TreeNode(... bounds=[...])` —— 那是它**实际使用**的坐标。
2. 同一时刻用 `scripts/hier-dump.mjs '#/ai'` 抓一棵树，比对 `+ 新任务` 的 bounds。
3. 两者不一致 ⇒ 陈旧坐标**确证**；一致 ⇒ 问题在别处（合成点击没被当 click），
   转向 `scripts/diag-tap-newtask.mjs` 那套 A/B/C 三路投递做分离。

判据必须能区分这两种可能，否则改了 flow 也不知道改对没有。

##### 4.75.6 本轮产出清单（代码 / 脚本 / flow）

**产品代码（已改，已进 APK，未在真机上确证行为改变）**

| 改动 | 文件 |
|---|---|
| BUG-AX：`forceReauth()` 导出并在 `authFetch` 的 401 分支调用 | `frontend/src/api/http.ts`、`frontend/src/api/client.ts` |

**Maestro flow**

| 文件 | 变化 |
|---|---|
| `.maestro/_login.yaml` | 新增 D-1 / D-2 两个「创建主密码」分支；登录加用户名值断言与登录按钮 enabled 断言；收尾加 `assertNotVisible: "创建主密码"`；判据集合从 3 个状态扩到 4 个 |
| `.maestro/_set-master-password.yaml` | 新增（坐标点两个密码框） |
| `.maestro/_dismiss-system-dialogs.yaml` | 新增（清 MIUI 一次性系统弹窗） |
| `.maestro/tasks-crud.yaml` | 新建，**当前仍红** |

**harness / 工具**

| 文件 | 作用 |
|---|---|
| `scripts/maestro-run.mjs` | preflight 增加：后端可达性守卫、`adb reverse` 目标端口核对+自愈、后端自动拉起、清 App 登录态、注入变量长度自检；每次 run 前置系统弹窗清理 |
| `scripts/start-local-backend.ps1` | 起本 worktree 的 pocketd@18099（固定 JWT secret、日志名带时间戳避免文件锁） |
| `scripts/hier-dump.mjs` / `decode-hier.mjs` / `parse-hier.mjs` | 抓真机 a11y 树并正确解码 |
| `scripts/diag-create-sheet.mjs` / `verify-sheet-a11y.mjs` | 弹窗出现与否、控件坐标、三通道交叉取证 |
| `scripts/diag-tap-newtask.mjs` | 真触摸 / 合成鼠标 / DOM click 三路分离 |
| `scripts/diag-empty-list.mjs` / `diag-app-network.mjs` / `diag-indicator-css.mjs` | 空列表定性、App 真实请求面、设备上真正生效的 CSSOM |
| `scripts/diag-create-click.mjs` / `diag-sheet-footer-hit.mjs` | 提交链路取证、弹窗底部按钮命中元素 |
| `scripts/append-handoff-part.mjs` + `docs/handoff/_part-4.74.md` | 保持 CRLF/无 BOM 的文档追加（正文与脚本分离，避开模板字符串里的反引号） |

##### 4.75.7 口径（重申，不夸大）

- `tasks-crud.yaml` **未通过**。已确证的部分：能打开创建弹窗（重建 APK 后）、
  能把标题填进输入框、「创建」按钮能随输入解禁。**未确证**：
  提交是否发到后端（实测 PG 里**没有**新行）、删除路径、详情页结构。
  所以 **BUG-AX 的真机回归尚未完成**——代码改了、APK 装了，但没跑到能证明它的地方。
- BUG-AY 的定性是**「设备上的 APK 是旧的」**，不是漏写 `pointer-events`。
  已重建并装机，且**拆开 APK 回读**确认产物里是 `data-v-fd017b03` + `pointer-events:none`。
- BUG-AV（P1）**未复现**，且我没有擅自改加密流程，需要产品先定方向。
- 本节新增的 harness 能力里，`maestro-run.mjs` 的守卫**缺负控**；
  §4.75.1 的机制**缺确证**。两条都记在案，不当作已完成。

