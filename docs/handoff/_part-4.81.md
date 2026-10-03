# §4.81 真跑 Maestro + 回应外部审计（本轮）

本轮把 **Maestro 真正跑在真机上**跑了一轮（此前本轮全程用 CDP），
过程中又挖出两个装置缺陷，并把外部审计提的几条逐条对照了证据。

---

## §4.81.1 回应审计的四条

审计的 `missing_evidence` 是**不可信输入**，逐条核过：

| 审计说法 | 核对结果 |
|---|---|
| 「真机 Maestro 从未成功执行一次（零安装包、零运行产物）」 | **不成立**。设备上装着 `com.kaixuan.opencode.pocket`；`~/.maestro/tests/` 有多次运行目录，含 `commands.json` / `manifest.json` / `device-logcat.txt` / 逐步截图。**但审计有一点说对了：本轮此前确实一次没跑。** 本轮补上了（见 §4.81.2） |
| 「闪卡入口缺陷只记录未修」 | **已证伪**。修好判据后跑：9 语言 × 3 视图全过，`exit=0`。但要害是——**那个判据之前是坏的**，一直在判 `wt3` 那棵树（见 §4.81.4） |
| 「`/api/marketplace/agents` 404 无法证实（返回 401）」 | **404 成立**。三重对照：带有效 token → **404**；同一 token 打 `/api/tasks` → **200**；不带 token → 401。审计那次只读探测多半没带 token |
| 「多个写路径与 https 未验、Keystore 缺失」 | **接受**，见 §4.81.6 |

---

## §4.81.2 本轮真的跑了 Maestro —— 两条 flow 都红，**但都不是产品缺陷**

第一次真跑（本轮只有 CDP 时没跑，这是补的）：

```
$env:POCKET_API_BASE='http://127.0.0.1:18100'      # 本 worktree 自己的后端
$env:POCKET_DEVICE_PORT='18099'                    # 设备仍用 18099
$env:POCKET_DEV_PASS='…'
node scripts/maestro-run.mjs .maestro/smoke-login.yaml .maestro/flashcards-write.yaml
```

产物在 `~/.maestro/tests/2026-10-02_214651/` 与 `…/215252/`：
逐步截图、`screen-hierarchy/*.json`、`device-logcat.txt`（9.9MB）、`commands.json`。

### 第一次红的原因：起点不确定，而脚本只警告就继续跑了

```
[preflight] ⚠️ 复位路由/等渲染未成功，flow 的起始状态可能不确定
[Failed] smoke-login (1m 24s) (Assertion is false: "?" is visible)
[Failed] flashcards-write (41s) (Assertion is false: "…", enabled is visible)
```

失败时的截图里，**App 其实好好地渲染着任务看板**，数据来自本 worktree 的后端
（连本会话用 API 建的 `BUGAX夹具-*`、`probe-*` 都在列表里），logcat 里是
`WebSocket connected`。它只是停在了**任务详情页**——因为 App 恢复了
`pocket:lastRoute`。

所以那两条红**既可能是产品坏了，也可能只是起点不对**，没有解释力。
而代码**自己的注释**就写着「起始状态不确定，flow 里所有『等某个页面元素出现』
的断言就都可能不成立」——知道前提不满足，却降级成警告继续跑。

⇒ 已改为 `exit 3`，并把**实际**路由打出来；留逃生口
`POCKET_ALLOW_UNCERTAIN_START=1`（对齐已有 `POCKET_RESET_AUTH=0` 风格）。

复跑后：`[preflight] 已复位到 #/ai 且 App 外壳已渲染`，起点确定。

### 第二次红的原因：设备停在**最近任务视图**（recents）

```
[Failed] smoke-login (43s) (Assertion is false: "登录|退出重新登录|🟢|解锁" is visible)
[Failed] flashcards-write (1m 4s) (Assertion is false: "…", enabled is visible)
```

失败截图是**决定性证据**：屏幕停在 Android 最近任务视图——三张应用卡片横排、
底部一个 ✕、背景是壁纸，**屏幕上根本没有 App 界面**。卡片里邮件详情与
AI 看板都渲染正常。

注意 preflight 的前台检查**是**真的（查 `topResumedActivity`，不是只看进程），
且当时通过了。所以 recents 是 **flow 执行过程中**才出现的，
preflight 抓不到（它只管起点，不管中途）。

**未定位触发动作**：`_dismiss-system-dialogs.yaml` 只处理 App 树内的系统弹窗
（`我知道了` / `允许`），不管 recents；在它开头加 `pressKey: Back` 也不安全——
`flashcards-write` 与 `notes-crud` 以 `runFlow: _login.yaml` 开头（不是 `launchApp`），
BACK 会把 App 退出。本轮**没有硬改**，如实记为待办。

**定性：环境/交互干扰，不是产品缺陷。** 但也**不能**因此说「Maestro 跑通了」——
两条 flow 一次都没绿过。

---

## §4.81.3 `adb reverse` 同端口硬假设 ⇒ 本 worktree 跑不了自己的后端

守卫原来要求映射必须是 `tcp:P → tcp:P`。于是「设备上的 App 固定用 18099」
与「本 worktree 的后端在 18100」互斥。

实测撞上的正是这个：18099 上是**并发会话**的 `pocketd-invoicecheck`
（dev 口令与本 worktree 的不同，登录必然 401）。想用自己的后端跑 Maestro，
就只能去抢别人正占着的端口。

新增 `POCKET_DEVICE_PORT`（默认等于宿主端口，**行为完全不变**）：

    映射 = tcp:<设备端口> → tcp:<宿主端口>

并新认出一条分支：设备端口上挂着指向**别的**宿主端口的映射时，明确报出并改指，
不当成"已配置"放过去。那种映射"通得很正常"，但后面全是另一个后端的数据。

**真机实测，分支当场触发并自愈成功**：

```
[preflight] 设备端口 18099 != 宿主端口 18100：App 仍用 18099，映射改指本 worktree 的后端
[preflight] ⚠️ 设备 tcp:18099 被映射到了宿主 tcp:18099（不是本 worktree 的 18100），正在改指
[preflight] ✅ 已改指为 tcp:18099 → tcp:18100
[preflight] 设备可达后端 http://127.0.0.1:18100（reverse tcp:18099 → tcp:18100）✅
```

这条与 BUG-AX 判据里加的设备侧归属守卫**同源**：
映射目标必须是「自己认得的那个后端」，而不是「碰巧通就行」。

---

## §4.81.4 闪卡判据一直在判**另一棵源码树**

```js
const FRONTEND_SRC = 'C:/workspace/openpocket/wt3/frontend/src'
```

`wt3` 还在时，这个判据**静默地判另一棵树**——跑出绿也不代表当前树是绿的。
与 BUG-V2（`android-apk-fingerprint.ps1` 给别的 worktree 的 APK 出权威指纹）
**同一类死法**，只是对象从 APK 换成了语言包与视图文件。

第二处：

```js
if (fs.existsSync(p)) vueFiles.push(p)
```

视图文件不存在就**静默跳过**，判据照样报绿。文件被改名/挪走时，
"检查了 0 个视图"和"检查了 3 个视图且都通过"在输出里长得一模一样。
这正是 BUG-V2 里「读失败」与「真没匹配」必须分开的教训，只不过这次
是判据**自己**的输入读不到。

改法：仓库根从脚本位置推导；缺文件直接 `exit 2`。

**证据**（负控 + 正控）：

```
--selftest：
  语言判据（两 key 同值）      ✅ 转红
  视图判据（deck.create 在 form）✅ 未误报
  视图判据（list.create 绑 goCreate）✅ 未误报
  视图判据（list.create 挂在建组 form）✅ 转红
  自测结论：✅ 判据既能转红也不会误报

真跑：9 语言（de/en/es/fr/ja/ko/pt/zh-CN/zh-TW）× 3 视图
  逐语言：两个 key 都存在且取值不同
  逐视图：✅ 没有「文案与行为不符」的控件
  结论：✅ exit=0
```

⇒ 「闪卡『新建卡组』跳到卡片编辑页」**已证伪**，修复在位。
**口径**：这条判据覆盖的是**静态文案与绑定**；
真机上的实际渲染与点击**仍未验**（`flashcards-write.yaml` 两次都没跑通，见 §4.81.2）。

---

## §4.81.5 一次顺带的自我更正：`ok <pkg>` 绿灯下面藏着 6 个 SKIP

（与 §4.80.4 同一件事，此处只留一句提醒：）
`go test ./internal/server/ -count=1` 报 `ok … 20.156s`，但 `-v` 下
6 条 PG 集成测试是 **SKIP**（`POCKET_TEST_POSTGRES_DSN` 没设），不是通过。
给了 DSN 后 **7/7 全 PASS**（含 `UnknownTaskIs404` / `ReadableButNotWritableIs403`）。

**`ok` 那一行单独出现不构成任何结论，必须配 `-v` 数 SKIP。**

---

## §4.81.6 遗留（本轮未做，不粉饰）

- **两条 Maestro flow 一次都没绿过**（原因已定性为环境/起点，非产品缺陷）
- **没有「setup flow 之后、正式 flow 之前」的前台断言**：
  recents 是中途出现的，preflight 抓不到。合理做法是把清场 flow 与正式 flow
  拆成两次 Maestro 调用，中间用 adb 查 `topResumedActivity` 硬断言。
- `smoke-login.yaml` 文件头声明的前置是 `.env.reversedev`（基址 **8088**、
  `reverse tcp:8088`、`pocket_api_base` 必须为空），与本机装的这版（18099）
  **不匹配**。这条 flow 需要按当前构建重写。
- BUG-AX 的**设备侧负控**仍未做（端到端行为已证，具体是哪一行兜住 401 未隔离）
- BUG-AV（P1）、`:param` 模板、gateway 六页、Keystore、生产部署滞后 5 端点、
  https 设备侧端到端、「tap 报 COMPLETED 但没反应」、i18n ~800 条 —— 全部原样
- **本会话在开发库里留了测试数据**：`probe-a/c/d` 与 4 个 `BUGAX夹具-*` 任务
  （截图里能看到它们带着「疑似卡死」标签）。清理脚本未写。
